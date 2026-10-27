// Shoebox: a small, self-hosted document archive.
// Scan or upload, OCR, file into year/month folders, find it again.
package main

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var version = "dev"

//go:embed web/dist
var webFS embed.FS

type Config struct {
	Listen    string // SB_LISTEN
	DataDir   string // SB_DATA: database, inbox, caches (local disk, never on NFS)
	Archive   string // SB_ARCHIVE: the archive folder (may be an NFS mount)
	BaseURL   string // SB_BASE_URL, optional
	TLSCert   string // SB_TLS_CERT / SB_TLS_KEY: serve HTTPS directly (the camera needs HTTPS)
	TLSKey    string
	TrustProx bool // SB_TRUST_PROXY: take the client IP from X-Forwarded-For
}

type App struct {
	cfg        Config
	db         *sql.DB
	jobs       *Jobs
	importer   *Importer
	watcher    *Watcher
	setupCode  string
	limiter    *limiter
	ceremonies ceremonies // passkey sign-ins and registrations in progress
	updates    updater    // what GitHub said about the newest release
}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version":
			fmt.Println(version)
			return
		case "healthcheck":
			healthcheck()
			return
		case "update-apply":
			updateApply()
			return
		}
	}
	cfg := Config{
		Listen:    env("SB_LISTEN", ":8080"),
		DataDir:   env("SB_DATA", "./data"),
		BaseURL:   strings.TrimRight(env("SB_BASE_URL", ""), "/"),
		TLSCert:   env("SB_TLS_CERT", ""),
		TLSKey:    env("SB_TLS_KEY", ""),
		TrustProx: env("SB_TRUST_PROXY", "false") == "true",
	}
	cfg.Archive = env("SB_ARCHIVE", filepath.Join(cfg.DataDir, "archive"))
	app, err := newApp(cfg)
	if err != nil {
		log.Fatal(err)
	}
	app.run()
}

func newApp(cfg Config) (*App, error) {
	for _, d := range []string{cfg.DataDir, cfg.Archive, filepath.Join(cfg.DataDir, "inbox"), filepath.Join(cfg.DataDir, "cache"),
		filepath.Join(cfg.DataDir, "originals"), filepath.Join(cfg.DataDir, "trash")} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return nil, fmt.Errorf("create %s: %w", d, err)
		}
	}
	// Temp files for uploads and OCR stay on the data disk and are cleared on start.
	tmp := filepath.Join(cfg.DataDir, "tmp")
	os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return nil, err
	}
	os.Setenv("TMPDIR", tmp)

	db, err := openDB(filepath.Join(cfg.DataDir, "shoebox.db"))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("database schema: %w", err)
	}
	a := &App{cfg: cfg, db: db, limiter: newLimiter(10, time.Minute)}
	a.jobs = newJobs(a)
	a.importer = &Importer{app: a}
	a.watcher = &Watcher{app: a}
	n, _ := a.userCount()
	if n == 0 {
		a.setupCode = randomCode()
		log.Printf("No account yet. Open Shoebox in the browser and use this setup code: %s", a.setupCode)
	}
	checkTools()
	return a, nil
}

func (a *App) run() {
	srv := &http.Server{
		Addr:              a.cfg.Listen,
		Handler:           a.routes(),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.jobs.start(ctx)
	go a.watcher.loop(ctx)
	go a.housekeeping(ctx)
	go func() {
		log.Printf("Shoebox %s listening on %s (data: %s, archive: %s)", version, a.cfg.Listen, a.cfg.DataDir, a.cfg.Archive)
		var err error
		if a.cfg.TLSCert != "" && a.cfg.TLSKey != "" {
			err = srv.ListenAndServeTLS(a.cfg.TLSCert, a.cfg.TLSKey)
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	cancel()
	sctx, scancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer scancel()
	srv.Shutdown(sctx)
	a.db.Close()
}

func (a *App) housekeeping(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, time.Now().Unix())
			a.maybeBackup()
			a.maybeAutoUpdate(ctx)
		}
	}
}

func healthcheck() {
	addr := env("SB_LISTEN", ":8080")
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	c := &http.Client{Timeout: 3 * time.Second}
	res, err := c.Get("http://" + addr + "/healthz")
	if err != nil || res.StatusCode != 200 {
		os.Exit(1)
	}
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })

	// session
	mux.HandleFunc("GET /api/me", a.handleMe)
	mux.HandleFunc("POST /api/setup", a.handleSetup)
	mux.HandleFunc("POST /api/login", a.handleLogin)
	mux.HandleFunc("POST /api/logout", a.handleLogout)
	mux.HandleFunc("POST /api/login/passkey/begin", a.handlePasskeyLoginBegin)
	mux.HandleFunc("POST /api/login/passkey/finish", a.handlePasskeyLoginFinish)
	mux.HandleFunc("GET /api/passkeys", a.auth(a.handlePasskeys))
	mux.HandleFunc("POST /api/passkeys/register/begin", a.auth(a.handlePasskeyRegisterBegin))
	mux.HandleFunc("POST /api/passkeys/register/finish", a.auth(a.handlePasskeyRegisterFinish))
	mux.HandleFunc("DELETE /api/passkeys/{id}", a.auth(a.handlePasskeyDelete))

	// documents
	mux.HandleFunc("GET /api/overview", a.auth(a.handleOverview))
	mux.HandleFunc("GET /api/docs", a.auth(a.handleDocs))
	mux.HandleFunc("GET /api/docs/{id}", a.auth(a.handleDoc))
	mux.HandleFunc("PATCH /api/docs/{id}", a.member(a.handleDocUpdate))
	mux.HandleFunc("POST /api/docs/{id}/file", a.member(a.handleDocFile))
	mux.HandleFunc("DELETE /api/docs/{id}", a.member(a.handleDocDelete))
	mux.HandleFunc("GET /api/docs/{id}/pdf", a.auth(a.handleDocPDF))
	mux.HandleFunc("GET /api/docs/{id}/thumb", a.auth(a.handleDocThumb))
	mux.HandleFunc("GET /api/docs/{id}/page/{n}", a.auth(a.handleDocPage))
	mux.HandleFunc("POST /api/scan", a.member(a.handleScan))
	mux.HandleFunc("POST /api/upload", a.member(a.handleUpload))
	mux.HandleFunc("GET /api/jobs", a.auth(a.handleJobs))
	mux.HandleFunc("DELETE /api/jobs/{id}", a.member(a.handleJobDismiss))
	mux.HandleFunc("GET /api/tree", a.auth(a.handleTree))
	mux.HandleFunc("GET /api/tags", a.auth(a.handleTags))
	mux.HandleFunc("PATCH /api/tags/{id}", a.member(a.handleTagUpdate))
	mux.HandleFunc("DELETE /api/tags/{id}", a.member(a.handleTagDelete))
	mux.HandleFunc("GET /api/senders", a.auth(a.handleSenders))
	mux.HandleFunc("PATCH /api/senders/{id}", a.member(a.handleSenderUpdate))
	mux.HandleFunc("DELETE /api/senders/{id}", a.member(a.handleSenderDelete))

	// settings
	mux.HandleFunc("GET /api/settings", a.auth(a.handleSettings))
	mux.HandleFunc("PUT /api/settings", a.admin(a.handleSettingsSave))
	mux.HandleFunc("POST /api/settings/ai-test", a.admin(a.handleAITest))
	mux.HandleFunc("GET /api/storage", a.auth(a.handleStorage))
	mux.HandleFunc("POST /api/storage/rescan", a.admin(a.handleRescan))
	mux.HandleFunc("GET /api/update", a.admin(a.handleUpdateInfo))
	mux.HandleFunc("POST /api/update", a.admin(a.handleUpdateStart))
	mux.HandleFunc("GET /api/backups", a.admin(a.handleBackups))
	mux.HandleFunc("POST /api/backups", a.admin(a.handleBackupNow))
	mux.HandleFunc("GET /api/stats", a.auth(a.handleStats))
	mux.HandleFunc("GET /api/rules", a.auth(a.handleRules))
	mux.HandleFunc("POST /api/rules", a.member(a.handleRuleSave))
	mux.HandleFunc("PUT /api/rules/{id}", a.member(a.handleRuleSave))
	mux.HandleFunc("DELETE /api/rules/{id}", a.member(a.handleRuleDelete))
	mux.HandleFunc("POST /api/rules/test", a.auth(a.handleRuleTest))
	mux.HandleFunc("GET /api/rules/suggestions", a.member(a.handleRuleSuggestions))
	mux.HandleFunc("POST /api/rules/suggestions/dismiss", a.member(a.handleRuleSuggestionDismiss))
	mux.HandleFunc("GET /api/docs/{id}/learned", a.auth(a.handleDocLearned))
	mux.HandleFunc("GET /api/docs/{id}/words/{n}", a.auth(a.handleDocWords))
	mux.HandleFunc("POST /api/parse", a.auth(a.handleParse))
	mux.HandleFunc("POST /api/docs/{id}/pages", a.member(a.handleDocPages))
	mux.HandleFunc("GET /api/docs/{id}/pagesize/{n}", a.auth(a.handlePageSize))
	mux.HandleFunc("POST /api/docs/{id}/stamp", a.member(a.handleDocStamp))
	mux.HandleFunc("DELETE /api/docs/{id}/stamps", a.member(a.handleDocUnstamp))
	mux.HandleFunc("GET /api/users", a.admin(a.handleUsers))
	mux.HandleFunc("POST /api/users", a.admin(a.handleUserAdd))
	mux.HandleFunc("PATCH /api/users/{id}", a.admin(a.handleUserUpdate))
	mux.HandleFunc("DELETE /api/users/{id}", a.admin(a.handleUserDelete))
	mux.HandleFunc("GET /api/libraries", a.auth(a.handleLibraries))
	mux.HandleFunc("GET /api/libraries/all", a.admin(a.handleLibrariesAll))
	mux.HandleFunc("POST /api/libraries", a.admin(a.handleLibraryCreate))
	mux.HandleFunc("PATCH /api/libraries/{id}", a.admin(a.handleLibraryUpdate))
	mux.HandleFunc("DELETE /api/libraries/{id}", a.admin(a.handleLibraryDelete))
	mux.HandleFunc("PUT /api/libraries/{id}/members", a.admin(a.handleLibraryMember))
	mux.HandleFunc("GET /api/tokens", a.auth(a.handleTokens))
	mux.HandleFunc("POST /api/tokens", a.auth(a.handleTokenCreate))
	mux.HandleFunc("DELETE /api/tokens/{id}", a.auth(a.handleTokenDelete))
	mux.HandleFunc("POST /api/account/password", a.auth(a.handlePassword))

	// Paperless import
	mux.HandleFunc("POST /api/import/paperless/check", a.admin(a.handleImportCheck))
	mux.HandleFunc("POST /api/import/paperless/start", a.admin(a.handleImportStart))
	mux.HandleFunc("POST /api/import/paperless/stop", a.admin(a.handleImportStop))
	mux.HandleFunc("GET /api/import/status", a.admin(a.handleImportStatus))

	// the app (single page, client-side routes)
	dist, _ := fs.Sub(webFS, "web/dist")
	mux.Handle("GET /", spa(dist))
	return a.secure(mux)
}

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
	cfg       Config
	db        *sql.DB
	jobs      *Jobs
	importer  *Importer
	watcher   *Watcher
	setupCode string
	limiter   *limiter
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

	// documents
	mux.HandleFunc("GET /api/overview", a.auth(a.handleOverview))
	mux.HandleFunc("GET /api/docs", a.auth(a.handleDocs))
	mux.HandleFunc("GET /api/docs/{id}", a.auth(a.handleDoc))
	mux.HandleFunc("PATCH /api/docs/{id}", a.auth(a.handleDocUpdate))
	mux.HandleFunc("POST /api/docs/{id}/file", a.auth(a.handleDocFile))
	mux.HandleFunc("DELETE /api/docs/{id}", a.auth(a.handleDocDelete))
	mux.HandleFunc("GET /api/docs/{id}/pdf", a.auth(a.handleDocPDF))
	mux.HandleFunc("GET /api/docs/{id}/thumb", a.auth(a.handleDocThumb))
	mux.HandleFunc("GET /api/docs/{id}/page/{n}", a.auth(a.handleDocPage))
	mux.HandleFunc("POST /api/scan", a.auth(a.handleScan))
	mux.HandleFunc("POST /api/upload", a.auth(a.handleUpload))
	mux.HandleFunc("GET /api/jobs", a.auth(a.handleJobs))
	mux.HandleFunc("DELETE /api/jobs/{id}", a.auth(a.handleJobDismiss))
	mux.HandleFunc("GET /api/tree", a.auth(a.handleTree))
	mux.HandleFunc("GET /api/tags", a.auth(a.handleTags))
	mux.HandleFunc("PATCH /api/tags/{id}", a.auth(a.handleTagUpdate))
	mux.HandleFunc("DELETE /api/tags/{id}", a.auth(a.handleTagDelete))
	mux.HandleFunc("GET /api/senders", a.auth(a.handleSenders))
	mux.HandleFunc("PATCH /api/senders/{id}", a.auth(a.handleSenderUpdate))
	mux.HandleFunc("DELETE /api/senders/{id}", a.auth(a.handleSenderDelete))

	// settings
	mux.HandleFunc("GET /api/settings", a.auth(a.handleSettings))
	mux.HandleFunc("PUT /api/settings", a.auth(a.handleSettingsSave))
	mux.HandleFunc("POST /api/settings/ai-test", a.auth(a.handleAITest))
	mux.HandleFunc("GET /api/storage", a.auth(a.handleStorage))
	mux.HandleFunc("POST /api/storage/rescan", a.auth(a.handleRescan))
	mux.HandleFunc("GET /api/rules", a.auth(a.handleRules))
	mux.HandleFunc("POST /api/rules", a.auth(a.handleRuleSave))
	mux.HandleFunc("PUT /api/rules/{id}", a.auth(a.handleRuleSave))
	mux.HandleFunc("DELETE /api/rules/{id}", a.auth(a.handleRuleDelete))
	mux.HandleFunc("POST /api/rules/test", a.auth(a.handleRuleTest))
	mux.HandleFunc("GET /api/users", a.auth(a.handleUsers))
	mux.HandleFunc("POST /api/users", a.auth(a.handleUserAdd))
	mux.HandleFunc("DELETE /api/users/{id}", a.auth(a.handleUserDelete))
	mux.HandleFunc("POST /api/account/password", a.auth(a.handlePassword))

	// Paperless import
	mux.HandleFunc("POST /api/import/paperless/check", a.auth(a.handleImportCheck))
	mux.HandleFunc("POST /api/import/paperless/start", a.auth(a.handleImportStart))
	mux.HandleFunc("POST /api/import/paperless/stop", a.auth(a.handleImportStop))
	mux.HandleFunc("GET /api/import/status", a.auth(a.handleImportStatus))

	// the app (single page, client-side routes)
	dist, _ := fs.Sub(webFS, "web/dist")
	mux.Handle("GET /", spa(dist))
	return a.secure(mux)
}

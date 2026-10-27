package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Updates from GitHub releases, started in the app.
//
// The app runs without root rights and can't replace its own program, which
// is good. So the work is split: the app checks for new releases, makes a
// database backup and writes a request file; a small root service that
// install.sh sets up (systemd path unit "shoebox-update") notices the file
// and runs "shoebox update-apply": it downloads the release, checks it
// against the release's SHA256SUMS, swaps the program, restarts Shoebox and
// goes back to the previous program if the new one doesn't come up.
// Releases are not signed; the checksum guards against broken downloads.

const defaultUpdateRepo = "lcsfls/shoebox"

var reVersion = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)

// newerVersion: is a newer than b? Both "vX.Y.Z"; anything else is never newer.
func newerVersion(a, b string) bool {
	ma, mb := reVersion.FindStringSubmatch(a), reVersion.FindStringSubmatch(b)
	if ma == nil || mb == nil {
		return false
	}
	for i := 1; i <= 3; i++ {
		x, _ := strconv.Atoi(ma[i])
		y, _ := strconv.Atoi(mb[i])
		if x != y {
			return x > y
		}
	}
	return false
}

type releaseInfo struct {
	Tag       string `json:"tag"`
	Name      string `json:"name"`
	Notes     string `json:"notes"`
	Published string `json:"published"`
	URL       string `json:"url"`
}

type updateStatus struct {
	State   string `json:"state"` // requested | downloading | installing | restarting | done | failed | rolled_back
	From    string `json:"from"`
	To      string `json:"to"`
	Message string `json:"message"`
	Time    string `json:"time"`
}

type updater struct {
	mu      sync.Mutex
	latest  *releaseInfo
	checked time.Time
	err     string
}

func updateRepo() string { return env("SB_UPDATE_REPO", defaultUpdateRepo) }

// updaterInstalled: install.sh sets SB_UPDATER=systemd when it set up the
// root service. Docker and hand-made installs update their own way.
func updaterInstalled() bool { return env("SB_UPDATER", "") == "systemd" }

func (a *App) updateDir() string { return filepath.Join(a.cfg.DataDir, "update") }

var githubAPI = "https://api.github.com"

func (a *App) latestRelease(ctx context.Context, force bool) (*releaseInfo, error) {
	u := &a.updates
	u.mu.Lock()
	defer u.mu.Unlock()
	if !force && time.Since(u.checked) < time.Hour && (u.latest != nil || u.err != "") {
		if u.err != "" {
			return nil, errors.New(u.err)
		}
		return u.latest, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", githubAPI+"/repos/"+updateRepo()+"/releases/latest", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "shoebox/"+version)
	u.checked = time.Now()
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		u.err = "GitHub is not reachable: " + err.Error()
		return nil, errors.New(u.err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		u.err = fmt.Sprintf("GitHub answered %d", res.StatusCode)
		return nil, errors.New(u.err)
	}
	var gh struct {
		Tag       string `json:"tag_name"`
		Name      string `json:"name"`
		Body      string `json:"body"`
		Published string `json:"published_at"`
		URL       string `json:"html_url"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&gh); err != nil {
		u.err = "unexpected answer from GitHub"
		return nil, errors.New(u.err)
	}
	u.err = ""
	u.latest = &releaseInfo{Tag: gh.Tag, Name: gh.Name, Notes: gh.Body, Published: gh.Published, URL: gh.URL}
	return u.latest, nil
}

func (a *App) readUpdateStatus() *updateStatus {
	b, err := os.ReadFile(filepath.Join(a.updateDir(), "status.json"))
	if err != nil {
		return nil
	}
	var s updateStatus
	if json.Unmarshal(b, &s) != nil {
		return nil
	}
	return &s
}

func (a *App) handleUpdateInfo(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{
		"current":   version,
		"installed": updaterInstalled(),
		"release":   reVersion.MatchString(version),
		"status":    a.readUpdateStatus(),
		"repo":      updateRepo(),
	}
	rel, err := a.latestRelease(r.Context(), r.URL.Query().Get("check") == "1")
	if err != nil {
		out["error"] = err.Error()
	} else {
		out["latest"] = rel
		out["newer"] = newerVersion(rel.Tag, version)
	}
	writeJSON(w, 200, out)
}

func (a *App) handleUpdateStart(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Version string `json:"version"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if err := a.requestUpdate(in.Version); err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// requestUpdate makes a backup and hands the update to the root service.
func (a *App) requestUpdate(v string) error {
	if !updaterInstalled() {
		return errors.New("This installation updates another way (see Settings → Updates).")
	}
	if !reVersion.MatchString(v) {
		return errors.New("Unknown version.")
	}
	if s := a.readUpdateStatus(); s != nil && (s.State == "requested" || s.State == "downloading" || s.State == "installing" || s.State == "restarting") {
		if t, err := time.Parse(time.RFC3339, s.Time); err == nil && time.Since(t) < 15*time.Minute {
			return errors.New("An update is already running.")
		}
	}
	if _, err := a.backup(); err != nil {
		return fmt.Errorf("The backup before the update failed, so nothing was changed: %w", err)
	}
	dir := a.updateDir()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	st := updateStatus{State: "requested", From: version, To: v, Message: "Waiting for the updater", Time: now()}
	b, _ := json.Marshal(st)
	os.WriteFile(filepath.Join(dir, "status.json"), b, 0o644)
	req, _ := json.Marshal(map[string]string{"version": v, "time": now()})
	// the root service watches this file
	return os.WriteFile(filepath.Join(dir, "request.json"), req, 0o640)
}

// maybeAutoUpdate runs from the hourly housekeeping.
func (a *App) maybeAutoUpdate(ctx context.Context) {
	s := a.settings()
	if !s.UpdateAuto || !updaterInstalled() || !reVersion.MatchString(version) || time.Now().Hour() != s.UpdateHour {
		return
	}
	rel, err := a.latestRelease(ctx, true)
	if err != nil || !newerVersion(rel.Tag, version) {
		return
	}
	log.Printf("updating to %s (automatic updates are on)", rel.Tag)
	if err := a.requestUpdate(rel.Tag); err != nil {
		log.Printf("automatic update: %v", err)
	}
}

// --- the root side: "shoebox update-apply" ---

type applyEnv struct {
	dataDir  string
	binPath  string
	download string // base for release files, e.g. https://github.com/<repo>/releases/download
	restart  func() error
	healthy  func(want string) error
	logf     func(string, ...any)
}

func updateApply() {
	bin, err := os.Executable()
	if err == nil {
		bin, _ = filepath.EvalSymlinks(bin)
	}
	listen := env("SB_LISTEN", ":8080")
	if strings.HasPrefix(listen, ":") {
		listen = "127.0.0.1" + listen
	}
	e := applyEnv{
		dataDir:  env("SB_DATA", "./data"),
		binPath:  bin,
		download: "https://github.com/" + updateRepo() + "/releases/download",
		restart:  func() error { return exec.Command("systemctl", "restart", "shoebox").Run() },
		healthy: func(want string) error {
			c := &http.Client{Timeout: 3 * time.Second}
			deadline := time.Now().Add(60 * time.Second)
			for time.Now().Before(deadline) {
				time.Sleep(2 * time.Second)
				res, err := c.Get("http://" + listen + "/api/me")
				if err != nil {
					continue
				}
				var me struct {
					Version string `json:"version"`
				}
				json.NewDecoder(res.Body).Decode(&me)
				res.Body.Close()
				if me.Version == want {
					return nil
				}
			}
			return errors.New("the new version didn't come up within a minute")
		},
		logf: log.Printf,
	}
	if err := applyUpdate(context.Background(), e); err != nil {
		log.Printf("update: %v", err)
		os.Exit(1)
	}
}

// writeStatus runs as root in a folder the shoebox user owns: never follow
// a link someone may have put there (O_EXCL after removing the old file).
func writeStatus(dir string, s updateStatus) {
	s.Time = now()
	b, _ := json.Marshal(s)
	tmp := filepath.Join(dir, "status.json.tmp")
	os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return
	}
	_, err = f.Write(b)
	f.Close()
	if err == nil {
		os.Rename(tmp, filepath.Join(dir, "status.json"))
	}
}

func applyUpdate(ctx context.Context, e applyEnv) error {
	dir := filepath.Join(e.dataDir, "update")
	reqPath := filepath.Join(dir, "request.json")
	raw, err := os.ReadFile(reqPath)
	if err != nil {
		return nil // nothing requested (the path unit also fires when the file goes away)
	}
	os.Remove(reqPath)
	var req struct {
		Version string `json:"version"`
	}
	json.Unmarshal(raw, &req)
	cur := currentBinaryVersion(e.binPath)
	st := updateStatus{From: cur, To: req.Version}
	fail := func(state, msg string) error {
		st.State, st.Message = state, msg
		writeStatus(dir, st)
		return errors.New(msg)
	}
	if !reVersion.MatchString(req.Version) {
		return fail("failed", "unknown version requested")
	}
	st.State, st.Message = "downloading", "Downloading "+req.Version
	writeStatus(dir, st)

	name := "shoebox-linux-" + runtime.GOARCH
	sums, err := fetch(ctx, e.download+"/"+req.Version+"/SHA256SUMS", 1<<16)
	if err != nil {
		return fail("failed", "Couldn't download the checksums: "+err.Error())
	}
	want := ""
	sc := bufio.NewScanner(strings.NewReader(string(sums)))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			want = strings.ToLower(f[0])
		}
	}
	if len(want) != 64 {
		return fail("failed", "The release has no checksum for "+name+".")
	}
	newPath := e.binPath + ".new"
	if err := download(ctx, e.download+"/"+req.Version+"/"+name, newPath); err != nil {
		os.Remove(newPath)
		return fail("failed", "Couldn't download the program: "+err.Error())
	}
	if got := fileSum(newPath); got != want {
		os.Remove(newPath)
		return fail("failed", "The download is damaged (checksum doesn't match). Nothing was changed.")
	}
	os.Chmod(newPath, 0o755)
	if v := currentBinaryVersion(newPath); v != req.Version {
		os.Remove(newPath)
		return fail("failed", fmt.Sprintf("The downloaded program says it is %q, not %s.", v, req.Version))
	}

	st.State, st.Message = "installing", "Installing "+req.Version
	writeStatus(dir, st)
	prev := e.binPath + ".prev"
	os.Remove(prev)
	if err := os.Link(e.binPath, prev); err != nil {
		if err := copyBinary(e.binPath, prev); err != nil {
			os.Remove(newPath)
			return fail("failed", "Couldn't keep the current program: "+err.Error())
		}
	}
	if err := os.Rename(newPath, e.binPath); err != nil {
		os.Remove(newPath)
		return fail("failed", "Couldn't install the program: "+err.Error())
	}

	st.State, st.Message = "restarting", "Restarting Shoebox"
	writeStatus(dir, st)
	if err := e.restart(); err == nil {
		if err = e.healthy(req.Version); err == nil {
			st.State, st.Message = "done", "Updated to "+req.Version
			writeStatus(dir, st)
			e.logf("updated %s → %s", cur, req.Version)
			return nil
		}
		e.logf("update: %v; going back to %s", err, cur)
	}
	// roll back
	if err := os.Rename(prev, e.binPath); err != nil {
		return fail("failed", "The new version didn't start and going back failed too: "+err.Error()+". Run the installer again.")
	}
	e.restart()
	return fail("rolled_back", req.Version+" didn't start, so Shoebox went back to "+cur+". See journalctl -u shoebox.")
}

func currentBinaryVersion(path string) string {
	out, err := exec.Command(path, "version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

var updateClient = &http.Client{Timeout: 5 * time.Minute}

func fetch(ctx context.Context, url string, max int64) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("User-Agent", "shoebox-updater")
	res, err := updateClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return io.ReadAll(io.LimitReader(res.Body, max))
}

func download(ctx context.Context, url, dst string) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("User-Agent", "shoebox-updater")
	res, err := updateClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, io.LimitReader(res.Body, 500<<20)); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func fileSum(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil))
}

func copyBinary(src, dst string) error {
	if err := streamCopy(src, dst); err != nil {
		return err
	}
	return os.Chmod(dst, 0o755)
}

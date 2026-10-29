//go:build !nosqlite

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	// newApp points TMPDIR into its data folder; restore it after the test
	t.Setenv("TMPDIR", os.TempDir())
	a, err := newApp(Config{DataDir: filepath.Join(dir, "data"), Archive: filepath.Join(dir, "archive")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.db.Close() })
	s := a.settings()
	s.Languages = testLang()
	a.saveSettings(s)
	return a
}

// famDir is the folder of the first (family) library: archive/family.
func famDir(a *App) string {
	p, _ := a.libPath(1)
	return p
}

func waitJob(t *testing.T, a *App, job *Job) Job {
	t.Helper()
	ctx := context.Background()
	a.jobs.run(ctx, <-a.jobs.queue)
	for _, j := range a.jobs.snapshot() {
		if j.ID == job.ID {
			return j
		}
	}
	t.Fatal("job vanished")
	return Job{}
}

// scanJob queues the test letter as a scan; variant changes a pixel so two
// scans of "the same letter" aren't byte-identical.
func scanJob(t *testing.T, a *App, variant int) *Job {
	dir, _ := os.MkdirTemp("", "scan")
	p := filepath.Join(dir, "in-000.png")
	f, _ := os.Open("testdata/letter.png")
	img, err := png.Decode(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	rgba := image.NewGray(img.Bounds())
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			rgba.Set(x, y, img.At(x, y))
		}
	}
	rgba.Set(variant, 0, color.Gray{200})
	out, _ := os.Create(p)
	png.Encode(out, rgba)
	out.Close()
	return a.jobs.add(&Job{Kind: "scan", Name: "test", files: []string{p}, dir: dir})
}

// imagePDF writes a PDF that is just a JPEG, without any text: like a cheap scanner makes.
func imagePDF(t *testing.T, jpg []byte, path string) {
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(jpg))
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	var offs []int
	obj := func(s string) {
		offs = append(offs, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", len(offs), s)
	}
	b.WriteString("%PDF-1.4\n")
	obj("<< /Type /Catalog /Pages 2 0 R >>")
	obj("<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	obj("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /XObject << /Im0 4 0 R >> >> /Contents 5 0 R >>")
	offs = append(offs, b.Len())
	fmt.Fprintf(&b, "4 0 obj\n<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceGray /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>\nstream\n", cfg.Width, cfg.Height, len(jpg))
	b.Write(jpg)
	b.WriteString("\nendstream\nendobj\n")
	content := "q 595 0 0 842 0 0 cm /Im0 Do Q"
	obj(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(offs)+1)
	for _, o := range offs {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offs)+1, xref)
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanReviewFileSearch(t *testing.T) {
	needTools(t, "tesseract", "qpdf", "pdftotext", "pdfinfo")
	a := testApp(t)

	// no rule yet: the scan waits in the review inbox
	j := waitJob(t, a, scanJob(t, a, 1))
	if j.State != "done" {
		t.Fatalf("job %s: %s", j.State, j.Error)
	}
	d, err := a.getDoc(j.DocID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != "inbox" || d.Date != "2026-10-24" || d.AmountCents == nil || *d.AmountCents != 8420 || d.Title != "Rechnung" {
		t.Fatalf("unexpected doc %+v", d)
	}
	if _, err := os.Stat(filepath.Join(a.cfg.DataDir, "inbox", d.Path)); err != nil {
		t.Fatal("inbox file missing")
	}

	// review: set sender and title, then file it
	title, sender := "Stromrechnung Oktober", "Stadtwerke Musterstadt"
	tags := []string{"Strom", "Rechnung"}
	if err := a.patchDoc(d.ID, DocPatch{Title: &title, Sender: &sender, Tags: &tags}); err != nil {
		t.Fatal(err)
	}
	if err := a.fileDoc(context.Background(), d.ID); err != nil {
		t.Fatal(err)
	}
	d, _ = a.getDoc(d.ID)
	want := "2026/10/2026-10-24_Stadtwerke-Musterstadt_Stromrechnung-Oktober.pdf"
	if d.Status != "filed" || d.Path != want {
		t.Fatalf("filed as %s %q", d.Status, d.Path)
	}
	if _, err := os.Stat(filepath.Join(famDir(a), want)); err != nil {
		t.Fatal("archive file missing")
	}

	// search: words in the text, prefixes, the sender, the tags
	for _, q := range []string{"gesamtbetrag", "strom", "musterst", "Stromrechnung oktober"} {
		docs, total, err := a.listDocs(DocQuery{Library: 1, Q: q})
		if err != nil || total != 1 || docs[0].ID != d.ID {
			t.Errorf("search %q: %d results, %v", q, total, err)
		}
	}
	if _, total, _ := a.listDocs(DocQuery{Library: 1, Q: "wasserrechnung"}); total != 0 {
		t.Error("search found something that isn't there")
	}
	if _, total, _ := a.listDocs(DocQuery{Library: 1, Year: "2026", Month: "10"}); total != 1 {
		t.Error("month filter")
	}
	if _, total, _ := a.listDocs(DocQuery{Library: 1, Tag: "Strom"}); total != 1 {
		t.Error("tag filter")
	}

	// renaming moves the file and keeps it findable
	newTitle := "Strom Oktober"
	a.patchDoc(d.ID, DocPatch{Title: &newTitle})
	if err := a.fileDoc(context.Background(), d.ID); err != nil {
		t.Fatal(err)
	}
	d, _ = a.getDoc(d.ID)
	if !strings.HasSuffix(d.Path, "_Strom-Oktober.pdf") {
		t.Errorf("not renamed: %s", d.Path)
	}
	if _, err := os.Stat(filepath.Join(famDir(a), want)); err == nil {
		t.Error("old file still there")
	}

	// a second scan of the same letter with a rule: sorted, but still waits in Review
	a.db.Exec(`INSERT INTO rules(name, kind, pattern, nocase, sender, tags, title, position, enabled)
		VALUES('Stadtwerke', 'any', 'stadtwerke', 1, 'Stadtwerke Musterstadt', '["Strom"]', 'Stromrechnung', 0, 1)`)
	j2 := waitJob(t, a, scanJob(t, a, 2))
	d2, _ := a.getDoc(j2.DocID)
	if d2 == nil || d2.Status != "inbox" || d2.Sender != "Stadtwerke Musterstadt" || d2.Rule != "Stadtwerke" {
		t.Fatalf("rule match: %+v (%s)", d2, j2.Error)
	}
}

func TestFolderWatcher(t *testing.T) {
	needTools(t, "tesseract", "qpdf", "pdftotext", "pdfinfo", "pdftoppm")
	a := testApp(t)
	ctx := context.Background()

	// a scanned PDF without text, copied by hand into archive/2026/01
	f, _ := os.Open("testdata/letter.png")
	img, err := png.Decode(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	gray := image.NewGray(img.Bounds())
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			gray.Set(x, y, img.At(x, y))
		}
	}
	var jb bytes.Buffer
	jpeg.Encode(&jb, gray, &jpeg.Options{Quality: 90})
	noText := filepath.Join(famDir(a), "2026", "01", "Scan_0001.pdf")
	os.MkdirAll(filepath.Dir(noText), 0o755)
	imagePDF(t, jb.Bytes(), noText)
	if hasText(pdfText(ctx, noText)) {
		t.Fatal("test PDF should have no text")
	}
	old := time.Now().Add(-time.Minute)
	os.Chtimes(noText, old, old)

	a.watcher.scan()
	select {
	case job := <-a.jobs.queue:
		a.jobs.run(ctx, job)
	default:
		t.Fatal("watcher queued nothing")
	}
	docs, total, _ := a.listDocs(DocQuery{Library: 1, Q: "stadtwerke"})
	if total != 1 {
		t.Fatalf("folder file not in the library (%d)", total)
	}
	d := docs[0]
	if d.Source != "folder" || d.Path != "2026/01/Scan_0001.pdf" {
		t.Errorf("doc %+v", d)
	}
	if !strings.HasPrefix(d.Date, "2026-01") {
		t.Errorf("date %s should come from the 2026/01 folder", d.Date)
	}

	// moved by hand: followed, not duplicated
	moved := filepath.Join(famDir(a), "2026", "02", "Strom.pdf")
	os.MkdirAll(filepath.Dir(moved), 0o755)
	os.Rename(noText, moved)
	os.Chtimes(moved, old, old)
	a.watcher.scan()
	for len(a.jobs.queue) > 0 {
		a.jobs.run(ctx, <-a.jobs.queue)
	}
	if _, total, _ := a.listDocs(DocQuery{Library: 1, Q: "stadtwerke"}); total != 1 {
		t.Errorf("after move: %d documents", total)
	}
	d, _ = a.getDoc(d.ID)
	if d.Path != "2026/02/Strom.pdf" || d.Status != "filed" {
		t.Errorf("after move: %s %s", d.Status, d.Path)
	}

	// deleted by hand: gone from the list
	os.Remove(moved)
	a.watcher.scan()
	if _, total, _ := a.listDocs(DocQuery{Library: 1}); total != 0 {
		t.Error("deleted file still listed")
	}
}

func TestHTTPAuth(t *testing.T) {
	a := testApp(t)
	srv := httptest.NewServer(a.routes())
	defer srv.Close()
	c := srv.Client()
	post := func(path string, body any, cookie *http.Cookie) *http.Response {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", srv.URL+path, bytes.NewReader(b))
		req.Header.Set("Origin", srv.URL)
		req.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	res, _ := c.Get(srv.URL + "/api/docs")
	if res.StatusCode != 401 {
		t.Fatalf("docs without session: %d", res.StatusCode)
	}
	if res := post("/api/setup", map[string]string{"code": "WRONG", "username": "anna", "password": "long-password-1"}, nil); res.StatusCode != 403 {
		t.Fatalf("wrong setup code: %d", res.StatusCode)
	}
	res = post("/api/setup", map[string]string{"code": a.setupCode, "username": "anna", "password": "long-password-1"}, nil)
	if res.StatusCode != 200 {
		t.Fatalf("setup: %d", res.StatusCode)
	}
	var cookie *http.Cookie
	for _, ck := range res.Cookies() {
		if ck.Name == sessionCookie {
			cookie = ck
		}
	}
	req, _ := http.NewRequest("GET", srv.URL+"/api/overview", nil)
	req.AddCookie(cookie)
	if res, _ := c.Do(req); res.StatusCode != 200 {
		t.Fatalf("overview: %d", res.StatusCode)
	}
	// cross-site change refused
	req, _ = http.NewRequest("PUT", srv.URL+"/api/settings", strings.NewReader("{}"))
	req.Header.Set("Origin", "https://evil.example")
	req.AddCookie(cookie)
	if res, _ := c.Do(req); res.StatusCode != 403 {
		t.Fatalf("cross-site: %d", res.StatusCode)
	}
	if res := post("/api/login", map[string]string{"username": "anna", "password": "nope"}, nil); res.StatusCode != 401 {
		t.Fatalf("bad login: %d", res.StatusCode)
	}
	if res := post("/api/login", map[string]string{"username": "Anna", "password": "long-password-1"}, nil); res.StatusCode != 200 {
		t.Fatalf("login: %d", res.StatusCode)
	}
	// the app shell is served for client routes
	res, _ = c.Get(srv.URL + "/archive/2026")
	if res.StatusCode != 200 && res.StatusCode != 500 {
		t.Fatalf("spa: %d", res.StatusCode)
	}
}

func TestRolesAndTokens(t *testing.T) {
	a := testApp(t)
	srv := httptest.NewServer(a.routes())
	defer srv.Close()
	c := srv.Client()
	do := func(method, path string, body any, cookie *http.Cookie, bearer string) (*http.Response, map[string]any) {
		var rd *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		} else {
			rd = bytes.NewReader(nil)
		}
		req, _ := http.NewRequest(method, srv.URL+path, rd)
		if cookie != nil {
			req.Header.Set("Origin", srv.URL)
			req.AddCookie(cookie)
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		req.Header.Set("Content-Type", "application/json")
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		json.NewDecoder(res.Body).Decode(&out)
		res.Body.Close()
		return res, out
	}
	session := func(res *http.Response) *http.Cookie {
		for _, ck := range res.Cookies() {
			if ck.Name == sessionCookie {
				return ck
			}
		}
		t.Fatal("no session cookie")
		return nil
	}
	res, _ := do("POST", "/api/setup", map[string]string{"code": a.setupCode, "username": "anna", "password": "long-password-1"}, &http.Cookie{Name: "x", Value: "y"}, "")
	admin := session(res)

	// a viewer may read, not change
	if res, out := do("POST", "/api/users", map[string]string{"username": "ben", "password": "long-password-2", "role": "viewer"}, admin, ""); res.StatusCode != 200 {
		t.Fatalf("add viewer: %d %v", res.StatusCode, out)
	}
	res, _ = do("POST", "/api/login", map[string]string{"username": "ben", "password": "long-password-2"}, &http.Cookie{Name: "x", Value: "y"}, "")
	viewer := session(res)
	if res, _ := do("GET", "/api/docs", nil, viewer, ""); res.StatusCode != 200 {
		t.Fatalf("viewer reads: %d", res.StatusCode)
	}
	if res, _ := do("PUT", "/api/settings", defaultSettings(), viewer, ""); res.StatusCode != 403 {
		t.Fatalf("viewer changes settings: %d", res.StatusCode)
	}
	if res, _ := do("POST", "/api/scan", nil, viewer, ""); res.StatusCode != 403 {
		t.Fatalf("viewer scans: %d", res.StatusCode)
	}
	// the last admin stays an admin
	var annaID int64
	a.db.QueryRow(`SELECT id FROM users WHERE username = 'anna'`).Scan(&annaID)
	if res, _ := do("DELETE", fmt.Sprintf("/api/users/%d", annaID), nil, admin, ""); res.StatusCode != 400 {
		t.Fatalf("deleting yourself: %d", res.StatusCode)
	}

	// API tokens: no cookie, no Origin needed; read-only means read-only
	_, out := do("POST", "/api/tokens", map[string]any{"name": "script", "readonly": false}, admin, "")
	tok, _ := out["token"].(string)
	if !strings.HasPrefix(tok, "sb_") {
		t.Fatalf("token: %v", out)
	}
	if res, _ := do("GET", "/api/docs", nil, nil, tok); res.StatusCode != 200 {
		t.Fatalf("token reads: %d", res.StatusCode)
	}
	if res, _ := do("PUT", "/api/settings", defaultSettings(), nil, tok); res.StatusCode != 200 {
		t.Fatalf("admin token writes: %d", res.StatusCode)
	}
	if res, _ := do("POST", "/api/tokens", map[string]any{"name": "x"}, nil, tok); res.StatusCode != 403 {
		t.Fatalf("token creating tokens: %d", res.StatusCode)
	}
	_, out = do("POST", "/api/tokens", map[string]any{"name": "ro", "readonly": true}, admin, "")
	ro, _ := out["token"].(string)
	if res, _ := do("PUT", "/api/settings", defaultSettings(), nil, ro); res.StatusCode != 403 {
		t.Fatalf("read-only token writes: %d", res.StatusCode)
	}
	if res, _ := do("GET", "/api/docs", nil, nil, "sb_wrong"); res.StatusCode != 401 {
		t.Fatalf("wrong token: %d", res.StatusCode)
	}
	// revoked
	var tid int64
	a.db.QueryRow(`SELECT id FROM api_tokens WHERE name = 'script'`).Scan(&tid)
	do("DELETE", fmt.Sprintf("/api/tokens/%d", tid), nil, admin, "")
	if res, _ := do("GET", "/api/docs", nil, nil, tok); res.StatusCode != 401 {
		t.Fatalf("revoked token: %d", res.StatusCode)
	}
	// passkey sign-in starts (the browser part is tested end to end)
	// (passkeys need a host name, not an IP address)
	req, _ := http.NewRequest("POST", srv.URL+"/api/login/passkey/begin", nil)
	req.Host = "localhost"
	req.Header.Set("Origin", "http://localhost")
	if res, err := c.Do(req); err != nil || res.StatusCode != 200 {
		t.Fatalf("passkey begin: %v %v", err, res.StatusCode)
	}
}

func TestLearningAndSuggestions(t *testing.T) {
	a := testApp(t)
	letter := func(sender, extra string) string {
		return sender + " GmbH\nKundenservice Postfach 12 34\nIhre Abrechnung für Wasser und Abwasser\n" + extra + "\nZählerstand Trinkwasser Frischwasser Schmutzwasser Gebühren\n"
	}
	for i, extra := range []string{"Januar", "Februar", "März", "April"} {
		if _, err := a.insertDoc(NewDoc{Status: "filed", Path: fmt.Sprintf("w%d.pdf", i), Title: "Wasser " + extra, Sender: "Wasserwerk Beispiel",
			Tags: []string{"Wasser", "Nebenkosten"}, Text: letter("Wasserwerk Beispiel", extra)}); err != nil {
			t.Fatal(err)
		}
	}
	for i, extra := range []string{"Kfz", "Hausrat"} {
		a.insertDoc(NewDoc{Status: "filed", Path: fmt.Sprintf("v%d.pdf", i), Title: "Versicherung " + extra, Sender: "Beispiel Versicherung",
			Tags: []string{"Versicherung"}, Text: "Beispiel Versicherung AG\nIhr Versicherungsschein " + extra + "\nBeitrag Police Laufzeit Selbstbeteiligung"})
	}
	l := a.learn(1, letter("Irgendwer", "Mai"), 0)
	if !containsFold(l.Tags, "Wasser") || containsFold(l.Tags, "Versicherung") {
		t.Errorf("learned tags %v", l.Tags)
	}
	if l.Sender != "Wasserwerk Beispiel" {
		t.Errorf("learned sender %q", l.Sender)
	}
	sg := a.ruleSuggestions(1)
	var found *RuleSuggestion
	for i := range sg {
		if sg[i].Sender == "Wasserwerk Beispiel" {
			found = &sg[i]
		}
	}
	if found == nil {
		t.Fatalf("no suggestion for the water company: %+v", sg)
	}
	if found.Covered != 4 || !containsFold(found.Tags, "Wasser") {
		t.Errorf("suggestion %+v", *found)
	}
	r := &Rule{Kind: "exact", Pattern: found.Pattern, NoCase: true}
	if !r.matches(letter("Wasserwerk Beispiel", "Juni")) {
		t.Errorf("suggested pattern %q doesn't match the next letter", found.Pattern)
	}

	// backups go into the archive folder
	b, err := a.backup()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(a.cfg.Archive, backupDir, b.Name)); err != nil {
		t.Fatal(err)
	}
	if n := len(a.listBackups()); n != 1 {
		t.Errorf("%d backups listed", n)
	}
}

// rawBody is sent as it is (a multipart upload) instead of as JSON.
type rawBody struct {
	ctype string
	data  []byte
}

type doFunc func(method, path string, body any, cookie *http.Cookie, lib int64) (int, map[string]any, []any)

// testHTTP: the app behind a test server; do sends JSON as a signed-in
// account (in a library, 0 for none), session signs in.
func testHTTP(t *testing.T, a *App) (doFunc, func(path string, body any) *http.Cookie) {
	srv := httptest.NewServer(a.routes())
	t.Cleanup(srv.Close)
	c := srv.Client()
	do := func(method, path string, body any, cookie *http.Cookie, lib int64) (int, map[string]any, []any) {
		ctype := "application/json"
		var b []byte
		if raw, ok := body.(rawBody); ok {
			ctype, b = raw.ctype, raw.data
		} else {
			b, _ = json.Marshal(body)
		}
		req, _ := http.NewRequest(method, srv.URL+path, bytes.NewReader(b))
		req.Header.Set("Origin", srv.URL)
		req.Header.Set("Content-Type", ctype)
		if lib > 0 {
			req.Header.Set("X-Library", fmt.Sprint(lib))
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var raw any
		json.NewDecoder(res.Body).Decode(&raw)
		m, _ := raw.(map[string]any)
		l, _ := raw.([]any)
		return res.StatusCode, m, l
	}
	session := func(path string, body any) *http.Cookie {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", srv.URL+path, bytes.NewReader(b))
		req.Header.Set("Origin", srv.URL)
		req.Header.Set("Content-Type", "application/json")
		res, err := c.Do(req)
		if err != nil || res.StatusCode != 200 {
			t.Fatalf("%s: %v %v", path, err, res.StatusCode)
		}
		for _, ck := range res.Cookies() {
			if ck.Name == sessionCookie {
				return ck
			}
		}
		t.Fatal("no session")
		return nil
	}
	return do, session
}

func TestLibraries(t *testing.T) {
	a := testApp(t)
	do, session := testHTTP(t, a)
	anna := session("/api/setup", map[string]string{"code": a.setupCode, "username": "anna", "password": "long-password-1"})
	// ben only gets the shared library
	if code, out, _ := do("POST", "/api/users", map[string]any{"username": "ben", "password": "long-password-2", "role": "member"}, anna, 0); code != 200 {
		t.Fatalf("add ben: %d %v", code, out)
	}
	ben := session("/api/login", map[string]string{"username": "ben", "password": "long-password-2"})

	// a private library for anna, in its own folder
	priv := filepath.Join(t.TempDir(), "anna-private")
	code, out, _ := do("POST", "/api/libraries", map[string]string{"name": "Anna privat", "path": priv}, anna, 0)
	if code != 200 {
		t.Fatalf("create library: %d %v", code, out)
	}
	lib2 := int64(out["id"].(float64))
	if code, _, _ := do("POST", "/api/libraries", map[string]string{"name": "Bad", "path": a.cfg.DataDir}, anna, 0); code != 400 {
		t.Errorf("library in the data folder accepted: %d", code)
	}
	if code, _, _ := do("POST", "/api/libraries", map[string]string{"name": "Taken", "path": filepath.Join(famDir(a), "2026")}, anna, 0); code != 200 {
		t.Errorf("an empty folder inside the first library was refused: %d", code)
	}
	if code, _, _ := do("POST", "/api/libraries", map[string]string{"name": "Ben", "path": priv + "2"}, ben, 0); code != 403 {
		t.Errorf("non-admin created a library: %d", code)
	}

	// a document in the private library, filed into its folder
	inbox := filepath.Join(a.cfg.DataDir, "inbox", "99.pdf")
	f, _ := os.Open("testdata/letter.png")
	img, _ := png.Decode(f)
	f.Close()
	var jb bytes.Buffer
	jpeg.Encode(&jb, img, &jpeg.Options{Quality: 80})
	imagePDF(t, jb.Bytes(), inbox)
	id, err := a.insertDoc(NewDoc{LibraryID: lib2, Status: "inbox", Path: "99.pdf", Title: "Arztrechnung", Sender: "Praxis Beispiel", Date: "2026-05-04", Tags: []string{"Privat"}, Text: "Praxis Beispiel Rechnung"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.fileDoc(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	d, _ := a.getDoc(id)
	if _, err := os.Stat(filepath.Join(priv, d.Path)); err != nil || !strings.HasPrefix(d.Path, "2026/05/") {
		t.Fatalf("filed into %q (in %s): %v", d.Path, priv, err)
	}
	if _, err := os.Stat(filepath.Join(famDir(a), d.Path)); err == nil {
		t.Error("the private document landed in the shared folder too")
	}

	// ben sees nothing of it
	if code, _, _ := do("GET", fmt.Sprintf("/api/docs/%d", id), nil, ben, 0); code != 404 {
		t.Errorf("ben opens the private document: %d", code)
	}
	if code, _, _ := do("GET", fmt.Sprintf("/api/docs/%d/pdf", id), nil, ben, 0); code != 404 {
		t.Errorf("ben downloads the private document: %d", code)
	}
	if code, _, _ := do("GET", "/api/docs", nil, ben, lib2); code != 403 {
		t.Errorf("ben lists the private library: %d", code)
	}
	_, out, _ = do("GET", "/api/docs?q=praxis", nil, ben, 1)
	if out["total"].(float64) != 0 {
		t.Errorf("the private document shows in the shared library: %v", out)
	}
	_, out, _ = do("GET", "/api/docs?q=praxis", nil, anna, lib2)
	if out["total"].(float64) != 1 {
		t.Errorf("anna doesn't find her document: %v", out)
	}
	// tags belong to their library
	_, _, tags := do("GET", "/api/tags", nil, anna, 1)
	for _, x := range tags {
		if x.(map[string]any)["name"] == "Privat" {
			t.Error("the private tag shows in the shared library")
		}
	}
	// share it with ben as a reader: he reads, but can't change
	var benID int64
	a.db.QueryRow(`SELECT id FROM users WHERE username = 'ben'`).Scan(&benID)
	if code, _, _ := do("PUT", fmt.Sprintf("/api/libraries/%d/members", lib2), map[string]any{"user_id": benID, "role": "reader"}, anna, 0); code != 200 {
		t.Fatalf("share: %d", code)
	}
	if code, _, _ := do("GET", fmt.Sprintf("/api/docs/%d", id), nil, ben, 0); code != 200 {
		t.Errorf("reader can't open: %d", code)
	}
	if code, _, _ := do("PATCH", fmt.Sprintf("/api/docs/%d", id), map[string]string{"title": "x"}, ben, 0); code != 403 {
		t.Errorf("reader changed a document: %d", code)
	}
	if code, _, _ := do("POST", "/api/rules", map[string]any{"kind": "any", "pattern": "x", "nocase": true, "enabled": true}, ben, lib2); code != 403 {
		t.Errorf("reader added a rule: %d", code)
	}
	// the folder watcher looks through both folders, each into its own library
	os.MkdirAll(filepath.Join(priv, "2025", "12"), 0o755)
	imagePDF(t, jb.Bytes(), filepath.Join(priv, "2025", "12", "Brief.pdf"))
	old := time.Now().Add(-time.Minute)
	os.Chtimes(filepath.Join(priv, "2025", "12", "Brief.pdf"), old, old)
	a.watcher.scan()
	ctx := context.Background()
	for len(a.jobs.queue) > 0 {
		a.jobs.run(ctx, <-a.jobs.queue)
	}
	var lib int64
	if err := a.db.QueryRow(`SELECT library_id FROM docs WHERE path = '2025/12/Brief.pdf'`).Scan(&lib); err != nil || lib != lib2 {
		t.Errorf("hand-copied file: library %d, %v", lib, err)
	}
	// moving a document to another library: readers can't, editors take file, tags and sender along
	if code, _, _ := do("POST", fmt.Sprintf("/api/docs/%d/move", id), map[string]any{"library": 1}, ben, 0); code != 403 {
		t.Errorf("a reader moved a document: %d", code)
	}
	if code, out, _ := do("POST", fmt.Sprintf("/api/docs/%d/move", id), map[string]any{"library": 1}, anna, 0); code != 200 {
		t.Fatalf("move: %d %v", code, out)
	}
	moved, _ := a.getDoc(id)
	if _, err := os.Stat(filepath.Join(famDir(a), moved.Path)); err != nil || moved.LibraryID != 1 || moved.Sender != "Praxis Beispiel" || len(moved.Tags) != 1 {
		t.Errorf("after the move: %+v %v", moved, err)
	}
	if _, err := os.Stat(filepath.Join(priv, d.Path)); err == nil {
		t.Error("the file stayed in the old library's folder")
	}
	// only empty libraries can go
	if code, _, _ := do("DELETE", fmt.Sprintf("/api/libraries/%d", lib2), nil, anna, 0); code != 400 {
		t.Errorf("deleted a library with documents: %d", code)
	}
}

func TestLibraryOwnFolder(t *testing.T) {
	a := testApp(t)
	// as before Homes: the first library at the top of the archive folder
	if famDir(a) != filepath.Join(a.cfg.Archive, "family") {
		t.Fatalf("on start the first library went to %s", famDir(a))
	}
	os.Remove(famDir(a))
	a.db.Exec(`UPDATE libraries SET path = '' WHERE id = 1`)
	if _, err := a.createUser("anna", "Anna", "a-long-password", "admin"); err != nil {
		t.Fatal(err)
	}
	// a filed document of the first library, and the folder of a second one
	f, _ := os.Open("testdata/letter.png")
	img, _ := png.Decode(f)
	f.Close()
	var jb bytes.Buffer
	jpeg.Encode(&jb, img, &jpeg.Options{Quality: 80})
	imagePDF(t, jb.Bytes(), filepath.Join(a.cfg.DataDir, "inbox", "1.pdf"))
	id, _ := a.insertDoc(NewDoc{Status: "inbox", Path: "1.pdf", Title: "Rechnung", Sender: "Stadtwerke", Date: "2026-10-24", Text: "Stadtwerke"})
	if err := a.fileDoc(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(a.cfg.Archive, "private"), 0o750)
	a.db.Exec(`INSERT INTO libraries(name, path, created_at) VALUES('Private', ?, ?)`, filepath.Join(a.cfg.Archive, "private"), now())
	os.MkdirAll(filepath.Join(a.cfg.Archive, ".shoebox-backup"), 0o750)

	req := httptest.NewRequest("POST", "/api/libraries/1/own-folder", strings.NewReader(`{"folder":"shared"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.handleLibraryOwnFolder(rec, req)
	if rec.Code != 200 {
		t.Fatalf("move: %d %s", rec.Code, rec.Body.String())
	}
	d, _ := a.getDoc(id)
	p, err := a.docFile(d)
	if err != nil || !strings.HasPrefix(p, filepath.Join(a.cfg.Archive, "shared")+string(filepath.Separator)) || !fileExists(p) {
		t.Fatalf("document now at %s (%v)", p, err)
	}
	for _, stays := range []string{"private", ".shoebox-backup", "home/anna"} {
		if !fileExists(filepath.Join(a.cfg.Archive, stays)) {
			t.Errorf("%s was moved", stays)
		}
	}
	// the folder watcher finds nothing missing
	a.watcher.scan()
	if d, _ := a.getDoc(id); d.Status != "filed" {
		t.Errorf("after the move: %s", d.Status)
	}
}

func TestHomes(t *testing.T) {
	needTools(t, "tesseract", "qpdf", "pdftotext", "pdfinfo")
	a := testApp(t)
	do, session := testHTTP(t, a)
	anna := session("/api/setup", map[string]string{"code": a.setupCode, "username": "anna", "password": "long-password-1"})
	if code, out, _ := do("POST", "/api/users", map[string]any{"username": "ben", "password": "long-password-2", "role": "member"}, anna, 0); code != 200 {
		t.Fatalf("add ben: %d %v", code, out)
	}
	ben := session("/api/login", map[string]string{"username": "ben", "password": "long-password-2"})

	// each sees their own Home first, then Family
	libsOf := func(c *http.Cookie) []any { _, _, l := do("GET", "/api/libraries", nil, c, 0); return l }
	al, bl := libsOf(anna), libsOf(ben)
	if len(al) != 2 || len(bl) != 2 {
		t.Fatalf("libraries: anna %v, ben %v", al, bl)
	}
	aHome, bHome := al[0].(map[string]any), bl[0].(map[string]any)
	annaHome, benHome := int64(aHome["id"].(float64)), int64(bHome["id"].(float64))
	if aHome["home"] != true || aHome["name"] != "Home" || al[1].(map[string]any)["name"] != "Family" || annaHome == benHome {
		t.Fatalf("anna's libraries: %v", al)
	}
	if aHome["path"] != filepath.Join(a.cfg.Archive, "home", "anna") || famDir(a) != filepath.Join(a.cfg.Archive, "family") {
		t.Errorf("folders: %v, family %s", aHome["path"], famDir(a))
	}
	// private, also from admins
	_, all, _ := do("GET", "/api/libraries/all", nil, anna, 0)
	for _, l := range all["libraries"].([]any) {
		if int64(l.(map[string]any)["id"].(float64)) == benHome {
			t.Error("the admin sees ben's Home")
		}
	}
	if code, _, _ := do("PUT", fmt.Sprintf("/api/libraries/%d/members", benHome), map[string]any{"user_id": 1, "role": "reader"}, anna, 0); code != 400 {
		t.Errorf("admin joined ben's Home: %d", code)
	}
	if code, _, _ := do("GET", "/api/docs", nil, anna, benHome); code != 403 {
		t.Errorf("anna reads ben's Home: %d", code)
	}

	// a scan lands in the Home, even while Family is picked, and Family's rules sort it
	a.db.Exec(`INSERT INTO rules(name, kind, pattern, nocase, sender, tags, title, position, enabled, library_id)
		VALUES('Stadtwerke', 'any', 'stadtwerke', 1, 'Stadtwerke Musterstadt', '["Strom"]', 'Stromrechnung', 0, 1, 1)`)
	var mb bytes.Buffer
	mw := multipart.NewWriter(&mb)
	fw, _ := mw.CreateFormFile("pages", "page-1.png")
	img, _ := os.ReadFile("testdata/letter.png")
	fw.Write(img)
	mw.Close()
	code, out, _ := do("POST", "/api/scan", rawBody{mw.FormDataContentType(), mb.Bytes()}, anna, 1)
	if code != 200 || int64(out["library"].(float64)) != annaHome {
		t.Fatalf("scan: %d %v", code, out)
	}
	a.jobs.run(context.Background(), <-a.jobs.queue)
	var j Job
	for _, x := range a.jobs.snapshot() {
		j = x
	}
	d, _ := a.getDoc(j.DocID)
	if d == nil || d.LibraryID != annaHome || d.Status != "inbox" || d.Rule != "Stadtwerke" || d.Sender != "Stadtwerke Musterstadt" {
		t.Fatalf("scanned: %+v (%s)", d, j.Error)
	}
	// shared with Family from Review
	if code, out, _ := do("POST", fmt.Sprintf("/api/docs/%d/move", d.ID), map[string]any{"library": 1}, anna, annaHome); code != 200 {
		t.Fatalf("move to Family: %d %v", code, out)
	}
	if code, out, _ := do("GET", fmt.Sprintf("/api/docs/%d", d.ID), nil, ben, 1); code != 200 {
		t.Errorf("ben can't see it in Family: %d %v", code, out)
	}

	// an account goes with its Home, but only an empty one
	id, _ := a.insertDoc(NewDoc{Status: "inbox", Path: "x.pdf", Title: "Ben's", LibraryID: benHome})
	if code, _, _ := do("DELETE", "/api/users/2", nil, anna, 0); code != 400 {
		t.Errorf("deleted ben with documents in his Home: %d", code)
	}
	a.db.Exec(`DELETE FROM docs WHERE id = ?`, id)
	if code, out, _ := do("DELETE", "/api/users/2", nil, anna, 0); code != 200 {
		t.Fatalf("delete ben: %d %v", code, out)
	}
	if a.homeOwner(benHome) != 0 || a.homeLib(2) != 0 {
		t.Error("ben's Home is left over")
	}
}

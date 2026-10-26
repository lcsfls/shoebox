package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Importer copies documents, tags, correspondents, document types, notes,
// custom fields and matching rules from a running Paperless-ngx through its
// REST API. Paperless is only read. Documents already imported are skipped,
// so the import can run again (and test runs are safe).
type Importer struct {
	app    *App
	mu     sync.Mutex
	st     ImportStatus
	cancel context.CancelFunc
	userID int64 // who started the import
}

type ImportStatus struct {
	Running  bool     `json:"running"`
	Done     int      `json:"done"`
	Skipped  int      `json:"skipped"`
	Failed   int      `json:"failed"`
	Total    int      `json:"total"`
	Current  string   `json:"current"`
	Rules    int      `json:"rules"`
	AutoOnly int      `json:"auto_only"`
	Errors   []string `json:"errors"`
	Finished string   `json:"finished,omitempty"`
}

type plClient struct {
	base, token string
	http        *http.Client
}

func newPL(base, token string) (*plClient, error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	base = strings.TrimSuffix(base, "/api")
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("Enter the Paperless address, e.g. http://paperless.local:8000")
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("Enter an API token (Paperless: your profile → API Auth Token).")
	}
	return &plClient{base: base, token: strings.TrimSpace(token), http: &http.Client{Timeout: 5 * time.Minute}}, nil
}

func (c *plClient) get(ctx context.Context, path string, out any) error {
	u := path
	if !strings.HasPrefix(path, "http") {
		u = c.base + path
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Accept", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == 401 || res.StatusCode == 403 {
		return errors.New("Paperless refused the token.")
	}
	if res.StatusCode != 200 {
		return fmt.Errorf("Paperless answered %d for %s", res.StatusCode, path)
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// all follows Paperless' pagination.
func plAll[T any](ctx context.Context, c *plClient, path string) ([]T, error) {
	var out []T
	next := path
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	next += sep + "page_size=100"
	for next != "" {
		var page struct {
			Count   int    `json:"count"`
			Next    string `json:"next"`
			Results []T    `json:"results"`
		}
		if err := c.get(ctx, next, &page); err != nil {
			return nil, err
		}
		out = append(out, page.Results...)
		next = page.Next
		// Paperless builds "next" from its own idea of its address; keep ours.
		if next != "" && !strings.HasPrefix(next, c.base) {
			if u, err := url.Parse(next); err == nil {
				if b, err := url.Parse(c.base); err == nil {
					u.Scheme, u.Host = b.Scheme, b.Host
					next = u.String()
				}
			}
		}
	}
	return out, nil
}

type plNamed struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Color     string `json:"color"`
	Match     string `json:"match"`
	Algorithm int    `json:"matching_algorithm"`
	NoCase    bool   `json:"is_insensitive"`
}

type plDoc struct {
	ID            int             `json:"id"`
	Title         string          `json:"title"`
	Content       string          `json:"content"`
	Correspondent *int            `json:"correspondent"`
	DocumentType  *int            `json:"document_type"`
	Tags          []int           `json:"tags"`
	Created       string          `json:"created"`
	ASN           *int            `json:"archive_serial_number"`
	Notes         json.RawMessage `json:"notes"` // objects in newer Paperless, ids in older
	CustomFields  []struct {
		Field int `json:"field"`
		Value any `json:"value"`
	} `json:"custom_fields"`
	OriginalName string `json:"original_file_name"`
}

type plNote struct {
	Note string `json:"note"`
}

func (d plDoc) noteTexts() []string {
	var notes []plNote
	json.Unmarshal(d.Notes, &notes) // ids (older versions) simply give nothing
	var out []string
	for _, n := range notes {
		if s := strings.TrimSpace(n.Note); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func (a *App) handleImportCheck(w http.ResponseWriter, r *http.Request) {
	var in struct{ URL, Token string }
	if !readJSON(w, r, &in) {
		return
	}
	c, err := newPL(in.URL, in.Token)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	count := func(path string) int {
		var p struct {
			Count int `json:"count"`
		}
		if err := c.get(ctx, path+"?page_size=1", &p); err != nil {
			return -1
		}
		return p.Count
	}
	docs := count("/api/documents/")
	if docs < 0 {
		var p struct{}
		err := c.get(ctx, "/api/documents/?page_size=1", &p)
		fail(w, 502, "Can't read from Paperless: "+err.Error())
		return
	}
	res := map[string]any{"documents": docs, "tags": count("/api/tags/"), "correspondents": count("/api/correspondents/"),
		"document_types": count("/api/document_types/"), "custom_fields": count("/api/custom_fields/")}
	// how many matching rules carry over
	conv, auto := 0, 0
	for _, p := range []string{"/api/tags/", "/api/correspondents/", "/api/document_types/"} {
		items, err := plAll[plNamed](ctx, c, p)
		if err != nil {
			continue
		}
		for _, it := range items {
			switch {
			case it.Algorithm == 6:
				auto++
			case it.Algorithm >= 1 && it.Algorithm <= 5 && strings.TrimSpace(it.Match) != "":
				conv++
			}
		}
	}
	res["rules"], res["auto_rules"] = conv, auto
	writeJSON(w, 200, res)
}

func (a *App) handleImportStart(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL, Token string
		Limit      int  `json:"limit"`
		Original   bool `json:"original"` // download originals instead of Paperless' archived (OCR'd) PDFs
	}
	if !readJSON(w, r, &in) {
		return
	}
	c, err := newPL(in.URL, in.Token)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	im := a.importer
	im.mu.Lock()
	if im.st.Running {
		im.mu.Unlock()
		fail(w, 409, "An import is already running.")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	im.cancel = cancel
	im.st = ImportStatus{Running: true, Errors: []string{}}
	im.userID = userFrom(r).ID
	im.mu.Unlock()
	go im.run(ctx, c, in.Limit, in.Original)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) handleImportStop(w http.ResponseWriter, r *http.Request) {
	a.importer.mu.Lock()
	if a.importer.cancel != nil {
		a.importer.cancel()
	}
	a.importer.mu.Unlock()
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) handleImportStatus(w http.ResponseWriter, r *http.Request) {
	a.importer.mu.Lock()
	st := a.importer.st
	st.Errors = append([]string(nil), st.Errors...)
	a.importer.mu.Unlock()
	writeJSON(w, 200, st)
}

func (im *Importer) update(f func(*ImportStatus)) {
	im.mu.Lock()
	f(&im.st)
	im.mu.Unlock()
}

func (im *Importer) fail(msg string) {
	im.update(func(s *ImportStatus) {
		s.Failed++
		if len(s.Errors) < 50 {
			s.Errors = append(s.Errors, msg)
		}
	})
}

var plKinds = map[int]string{1: "any", 2: "all", 3: "exact", 4: "regex", 5: "fuzzy"}

func (im *Importer) run(ctx context.Context, c *plClient, limit int, original bool) {
	a := im.app
	defer im.update(func(s *ImportStatus) {
		s.Running, s.Current, s.Finished = false, "", now()
	})
	tags, err1 := plAll[plNamed](ctx, c, "/api/tags/")
	corrs, err2 := plAll[plNamed](ctx, c, "/api/correspondents/")
	types, err3 := plAll[plNamed](ctx, c, "/api/document_types/")
	if err := errors.Join(err1, err2, err3); err != nil {
		im.fail(err.Error())
		return
	}
	var fields []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	if f, err := plAll[struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}](ctx, c, "/api/custom_fields/"); err == nil {
		fields = f
	}
	tagName, corrName, typeName, fieldName := map[int]string{}, map[int]string{}, map[int]string{}, map[int]string{}
	for _, t := range tags {
		tagName[t.ID] = t.Name
		a.db.Exec(`INSERT INTO tags(name, color) VALUES(?, ?) ON CONFLICT(name) DO NOTHING`, t.Name, nearestTagColor(t.Color))
	}
	for _, t := range corrs {
		corrName[t.ID] = t.Name
		senderID(a.db, t.Name)
	}
	for _, t := range types {
		typeName[t.ID] = "type:" + t.Name
	}
	for _, f := range fields {
		fieldName[f.ID] = f.Name
	}

	// matching rules → sorting rules (only once: skip ones that already exist)
	addRule := func(it plNamed, sender string, tags []string) {
		kind, ok := plKinds[it.Algorithm]
		if !ok || strings.TrimSpace(it.Match) == "" {
			if it.Algorithm == 6 {
				im.update(func(s *ImportStatus) { s.AutoOnly++ })
			}
			return
		}
		var n int
		a.db.QueryRow(`SELECT count(*) FROM rules WHERE name = ? AND pattern = ?`, "Paperless: "+it.Name, it.Match).Scan(&n)
		if n > 0 {
			return
		}
		tb, _ := json.Marshal(tags)
		if tags == nil {
			tb = []byte("[]")
		}
		a.db.Exec(`INSERT INTO rules(name, kind, pattern, nocase, sender, tags, title, position, enabled) VALUES(?,?,?,?,?,?,?,100,1)`,
			"Paperless: "+it.Name, kind, it.Match, it.NoCase, sender, string(tb), "")
		im.update(func(s *ImportStatus) { s.Rules++ })
	}
	for _, t := range tags {
		addRule(t, "", []string{t.Name})
	}
	for _, t := range corrs {
		addRule(t, t.Name, nil)
	}
	for _, t := range types {
		addRule(t, "", []string{"type:" + t.Name})
	}

	docs, err := plAll[plDoc](ctx, c, "/api/documents/?ordering=created")
	if err != nil {
		im.fail(err.Error())
		return
	}
	if limit > 0 && limit < len(docs) {
		docs = docs[len(docs)-limit:] // the newest ones
	}
	im.update(func(s *ImportStatus) { s.Total = len(docs) })
	for _, pd := range docs {
		if ctx.Err() != nil {
			return
		}
		im.update(func(s *ImportStatus) { s.Current = pd.Title })
		var exists int
		a.db.QueryRow(`SELECT count(*) FROM docs WHERE paperless_id = ?`, pd.ID).Scan(&exists)
		if exists > 0 {
			im.update(func(s *ImportStatus) { s.Skipped++ })
			continue
		}
		if err := im.importDoc(ctx, c, pd, original, tagName, corrName, typeName, fieldName); err != nil {
			var dup dupError
			if errors.As(err, &dup) {
				im.update(func(s *ImportStatus) { s.Skipped++ })
				continue
			}
			im.fail(fmt.Sprintf("#%d %s: %v", pd.ID, pd.Title, err))
			continue
		}
		im.update(func(s *ImportStatus) { s.Done++ })
	}
}

func (im *Importer) importDoc(ctx context.Context, c *plClient, pd plDoc, original bool, tagName, corrName, typeName, fieldName map[int]string) error {
	a := im.app
	dir, err := os.MkdirTemp("", "paperless")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	u := fmt.Sprintf("%s/api/documents/%d/download/", c.base, pd.ID)
	if original {
		u += "?original=true"
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	req.Header.Set("Authorization", "Token "+c.token)
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("download answered %d", res.StatusCode)
	}
	tmp := filepath.Join(dir, "doc.pdf")
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, res.Body); err != nil {
		f.Close()
		return err
	}
	f.Close()
	head := make([]byte, 5)
	if hf, err := os.Open(tmp); err == nil {
		io.ReadFull(hf, head)
		hf.Close()
	}
	if string(head) != "%PDF-" {
		// an original that isn't a PDF (photo, office file): take the archived version instead
		if original {
			return im.importDoc(ctx, c, pd, false, tagName, corrName, typeName, fieldName)
		}
		return errors.New("not a PDF")
	}
	sha, size, err := fileSHA(tmp)
	if err != nil {
		return err
	}
	if d, err := a.docBySHA(sha, false); err == nil {
		return dupError{d}
	}
	text := pd.Content
	ocr := "paperless"
	if !hasText(text) {
		text = pdfText(ctx, tmp)
		ocr = "pdf"
	}
	var tags []string
	for _, t := range pd.Tags {
		if n, ok := tagName[t]; ok {
			tags = append(tags, n)
		}
	}
	if pd.DocumentType != nil {
		if n, ok := typeName[*pd.DocumentType]; ok {
			tags = append(tags, n)
		}
	}
	sender := ""
	if pd.Correspondent != nil {
		sender = corrName[*pd.Correspondent]
	}
	date := ""
	if len(pd.Created) >= 10 && plausibleDate(pd.Created[:10], time.Now()) {
		date = pd.Created[:10]
	} else {
		// Paperless sometimes reads a far-off date from the text; look again
		date = findDate(text, time.Now())
	}
	title := clean(pd.Title, 200)
	if !hasLetters(title) {
		if t := guessTitle(text); t != "" {
			title = t
		}
	}
	fields := map[string]string{}
	for _, cf := range pd.CustomFields {
		if cf.Value == nil {
			continue
		}
		name := fieldName[cf.Field]
		if name == "" {
			name = fmt.Sprintf("field %d", cf.Field)
		}
		fields[name] = fmt.Sprint(cf.Value)
	}
	if pd.ASN != nil {
		fields["ASN"] = fmt.Sprint(*pd.ASN)
	}
	notes := pd.noteTexts()
	f2 := findAmount(text)
	id, err := a.insertDoc(NewDoc{
		Status: "inbox", SHA256: sha, FileSHA: sha, Title: title, Sender: sender, Date: date, AddedBy: im.userID,
		AmountCents: f2, Pages: pdfPages(ctx, tmp), Size: size, Source: "paperless", OCR: ocr,
		Notes: strings.Join(notes, "\n\n"), Fields: fields, Tags: tags, Text: text, PaperlessID: int64(pd.ID),
	})
	if err != nil {
		return err
	}
	name := fmt.Sprintf("%d.pdf", id)
	if err := moveFile(tmp, filepath.Join(a.cfg.DataDir, "inbox", name)); err != nil {
		a.db.Exec(`DELETE FROM docs WHERE id = ?`, id)
		return err
	}
	a.db.Exec(`UPDATE docs SET path = ? WHERE id = ?`, name, id)
	if err := a.fileDoc(ctx, id); err != nil {
		log.Printf("import: filing %d: %v", id, err)
		return err
	}
	return nil
}

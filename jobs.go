package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// A Job turns incoming files into a document: OCR, guess the facts,
// then put it in the review inbox.
type Job struct {
	ID      int64     `json:"id"`
	Kind    string    `json:"kind"` // scan | upload | folder
	Name    string    `json:"name"`
	State   string    `json:"state"` // queued | working | done | failed
	Step    string    `json:"step"`
	Error   string    `json:"error,omitempty"`
	DocID   int64     `json:"doc_id,omitempty"`
	Created time.Time `json:"created"`

	LibraryID int64 `json:"library_id"`

	files  []string // scan: page images; upload: one file; folder: archive-relative path
	dir    string   // temp dir owned by the job
	userID int64    // who scanned or uploaded it
}

type Jobs struct {
	app    *App
	mu     sync.Mutex
	next   int64
	list   []*Job
	queue  chan *Job
	folder map[string]bool // archive paths already queued
}

func newJobs(a *App) *Jobs {
	return &Jobs{app: a, queue: make(chan *Job, 10000), folder: map[string]bool{}}
}

func (j *Jobs) start(ctx context.Context) {
	n := j.app.settings().Workers
	for i := 0; i < max(n, 1); i++ {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case job := <-j.queue:
					j.run(ctx, job)
				}
			}
		}()
	}
}

func (j *Jobs) add(job *Job) *Job {
	if job.LibraryID == 0 {
		job.LibraryID = 1
	}
	j.mu.Lock()
	j.next++
	job.ID = j.next
	job.State = "queued"
	job.Created = time.Now()
	j.list = append(j.list, job)
	if len(j.list) > 200 {
		// forget old finished jobs
		keep := j.list[:0]
		for i, x := range j.list {
			if x.State == "queued" || x.State == "working" || i >= len(j.list)-100 {
				keep = append(keep, x)
			}
		}
		j.list = keep
	}
	j.mu.Unlock()
	j.queue <- job
	return job
}

func (j *Jobs) addFolder(lib int64, rel string) {
	key := fmt.Sprintf("%d:%s", lib, rel)
	j.mu.Lock()
	if j.folder[key] {
		j.mu.Unlock()
		return
	}
	j.folder[key] = true
	j.mu.Unlock()
	j.add(&Job{Kind: "folder", Name: rel, files: []string{rel}, LibraryID: lib})
}

func (j *Jobs) set(job *Job, f func(*Job)) {
	j.mu.Lock()
	f(job)
	j.mu.Unlock()
}

func (j *Jobs) snapshot() []Job {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]Job, 0, len(j.list))
	for i := len(j.list) - 1; i >= 0; i-- {
		out = append(out, *j.list[i])
	}
	return out
}

func (j *Jobs) dismiss(id int64) {
	j.mu.Lock()
	defer j.mu.Unlock()
	keep := j.list[:0]
	for _, x := range j.list {
		if x.ID != id || x.State == "queued" || x.State == "working" {
			keep = append(keep, x)
		}
	}
	j.list = keep
}

func (j *Jobs) run(ctx context.Context, job *Job) {
	j.set(job, func(x *Job) { x.State = "working"; x.Step = "Reading text" })
	var id int64
	var err error
	switch job.Kind {
	case "folder":
		id, err = j.app.processFolderFile(ctx, job.LibraryID, job.files[0], func(s string) { j.set(job, func(x *Job) { x.Step = s }) })
		j.mu.Lock()
		delete(j.folder, fmt.Sprintf("%d:%s", job.LibraryID, job.files[0]))
		j.mu.Unlock()
	default:
		id, err = j.app.processIncoming(ctx, job, func(s string) { j.set(job, func(x *Job) { x.Step = s }) })
	}
	if job.dir != "" {
		os.RemoveAll(job.dir)
	}
	j.set(job, func(x *Job) {
		x.Step = ""
		x.DocID = id
		if err != nil {
			x.State = "failed"
			x.Error = err.Error()
			log.Printf("job %d (%s %s): %v", x.ID, x.Kind, x.Name, err)
		} else {
			x.State = "done"
		}
	})
}

func fileSHA(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

type dupError struct{ doc *Doc }

func (e dupError) Error() string {
	t := e.doc.Title
	if t == "" {
		t = filepath.Base(e.doc.Path)
	}
	return fmt.Sprintf("Already in the archive: “%s”", t)
}

// processIncoming handles scans and uploads.
func (a *App) processIncoming(ctx context.Context, job *Job, step func(string)) (int64, error) {
	s := a.settings()
	out := filepath.Join(job.dir, "document.pdf")
	var text, ocr string
	first := job.files[0]
	isPDF := strings.EqualFold(filepath.Ext(first), ".pdf")

	srcSHA, _, err := fileSHA(first)
	if err != nil {
		return 0, err
	}
	if !isPDF || len(job.files) > 1 {
		srcSHA = "" // a new scan is never a duplicate of an earlier one
	}
	if srcSHA != "" {
		if d, err := a.docBySHA(job.LibraryID, srcSHA, false); err == nil {
			return d.ID, dupError{d}
		}
	}

	started := time.Now()
	if isPDF {
		step("Reading text")
		text = pdfText(ctx, first)
		if hasText(text) {
			ocr = "pdf"
			if err := os.Rename(first, out); err != nil {
				return 0, err
			}
		} else {
			step("Recognizing text")
			if text, err = ocrPDF(ctx, first, out, s.Languages); err != nil {
				return 0, err
			}
			ocr = "tesseract"
		}
	} else {
		step("Recognizing text")
		if text, err = ocrImages(ctx, job.files, s.Languages, out); err != nil {
			return 0, err
		}
		ocr = "tesseract"
	}

	var aiFacts *Facts
	if s.AI.Enabled && s.AI.BaseURL != "" && s.AI.Model != "" {
		step("Reading with the AI model")
		if t, err := a.aiOCR(ctx, s.AI, out, job.dir); err != nil {
			log.Printf("AI text recognition failed, keeping Tesseract's text: %v", err)
		} else if hasText(t) {
			text, ocr = t, ocr+"+ai"
		}
		if s.AI.Fields {
			if f, err := a.aiFields(ctx, s.AI, job.LibraryID, text); err == nil {
				aiFacts = f
			} else {
				log.Printf("AI fields: %v", err)
			}
		}
	}

	ocrMillis := time.Since(started).Milliseconds()
	step("Sorting")
	f := a.analyze(job.LibraryID, text)
	mergeAI(&f, aiFacts)
	fsha, size, _ := fileSHA(out)
	if d, err := a.docBySHA(job.LibraryID, fsha, false); err == nil {
		return d.ID, dupError{d}
	}
	title := f.Title
	if title == "" && job.Kind == "upload" {
		if t := titleFromFile(job.Name); hasLetters(t) {
			title = t
		}
	}
	n := NewDoc{
		Status: "inbox", SHA256: srcSHA, FileSHA: fsha, Title: title, Sender: f.Sender, Date: f.Date,
		AmountCents: f.AmountCents, Pages: pdfPages(ctx, out), Size: size, Source: job.Kind, OCR: ocr,
		Rule: f.Rule, Tags: f.Tags, Text: text, AddedBy: job.userID, OCRMillis: ocrMillis, LibraryID: job.LibraryID,
	}
	id, err := a.insertDoc(n)
	if err != nil {
		return 0, err
	}
	name := fmt.Sprintf("%d.pdf", id)
	if err := moveFile(out, filepath.Join(a.cfg.DataDir, "inbox", name)); err != nil {
		a.db.Exec(`DELETE FROM docs WHERE id = ?`, id)
		return 0, err
	}
	a.db.Exec(`UPDATE docs SET path = ? WHERE id = ?`, name, id)
	// every new document waits in Review: it's filed only once someone looked at it
	return id, nil
}

func mergeAI(f *Facts, ai *Facts) {
	if ai == nil {
		return
	}
	// Explicit sorting rules win over the model; the model wins over the simple guessers.
	if ai.Date != "" {
		f.Date = ai.Date
	}
	if ai.AmountCents != nil {
		f.AmountCents = ai.AmountCents
	}
	if f.Rule == "" && ai.Sender != "" {
		f.Sender = ai.Sender
	}
	if ai.Title != "" {
		f.Title = ai.Title
	}
	for _, t := range ai.Tags {
		if !containsFold(f.Tags, t) {
			f.Tags = append(f.Tags, t)
		}
	}
	if f.Sender != "" && f.Date != "" {
		f.Matched = true
	}
}

var leadingDate = regexp.MustCompile(`^\d{4}-\d{2}(-\d{2})?[ _.-]*`)

func titleFromFile(name string) string {
	stem := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	stem = leadingDate.ReplaceAllString(stem, "")
	stem = strings.NewReplacer("_", " – ", "-", " ").Replace(stem)
	stem = strings.Join(strings.Fields(stem), " ")
	return clean(stem, 200)
}

// --- filing ---

var archiveMu sync.Mutex // filing and the folder watcher don't run at the same time

var unsafeName = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]+`)

func pathPart(s string) string {
	s = unsafeName.ReplaceAllString(strings.TrimSpace(s), "-")
	s = strings.Join(strings.Fields(s), "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-._ ")
	if r := []rune(s); len(r) > 80 {
		s = string(r[:80])
	}
	return s
}

// targetPath renders the file name pattern for a document.
func targetPath(pattern string, d *Doc) string {
	date := d.Date
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	title := d.Title
	if title == "" {
		title = "Document"
	}
	r := strings.NewReplacer(
		"{year}", date[:4], "{month}", date[5:7], "{day}", date[8:10], "{date}", date,
		"{sender}", pathPart(d.Sender), "{title}", pathPart(title), "{id}", fmt.Sprint(d.ID),
		"{tags}", pathPart(strings.Join(d.Tags, "-")))
	var parts []string
	for _, seg := range strings.Split(pattern, "/") {
		seg = r.Replace(seg)
		for strings.Contains(seg, "__") {
			seg = strings.ReplaceAll(seg, "__", "_")
		}
		seg = strings.Trim(seg, "_- .")
		if seg != "" && seg != ".." {
			parts = append(parts, seg)
		}
	}
	if len(parts) == 0 {
		parts = []string{pathPart(title)}
	}
	return filepath.Join(parts...) + ".pdf"
}

func uniquePath(root, rel, self string) string {
	ext := filepath.Ext(rel)
	base := strings.TrimSuffix(rel, ext)
	cand := rel
	for i := 2; ; i++ {
		if cand == self {
			return cand
		}
		if _, err := os.Stat(filepath.Join(root, cand)); errors.Is(err, os.ErrNotExist) {
			return cand
		}
		cand = fmt.Sprintf("%s-%d%s", base, i, ext)
	}
}

// inArchive turns a path inside a library's folder into an absolute one.
func (a *App) inArchive(lib int64, rel string) (string, error) {
	root, err := a.libPath(lib)
	if err != nil {
		return "", err
	}
	p := filepath.Join(root, rel)
	if rel == "" || !strings.HasPrefix(p, filepath.Clean(root)+string(os.PathSeparator)) {
		return "", errors.New("path outside the archive")
	}
	return p, nil
}

// docFile is the absolute path of a document's PDF.
func (a *App) docFile(d *Doc) (string, error) {
	if d.Status == "inbox" {
		return filepath.Join(a.cfg.DataDir, "inbox", filepath.Base(d.Path)), nil
	}
	return a.inArchive(d.LibraryID, d.Path)
}

// fileDoc moves a document from the inbox into the archive, or renames a filed
// one after its title, sender or date changed. Files that were put into the
// archive by hand stay where they are; only their metadata is updated.
func (a *App) fileDoc(ctx context.Context, id int64) error {
	// filing moves files: once started, finish even if the browser goes away
	ctx = context.WithoutCancel(ctx)
	d, err := a.getDoc(id)
	if err != nil {
		return err
	}
	src, err := a.docFile(d)
	if err != nil {
		return err
	}
	if d.Status == "inbox" {
		// metadata on the local disk first: faster than on a network share
		if err := writeMeta(ctx, src, d); err != nil {
			log.Printf("metadata for %d: %v", id, err)
		}
	}
	root, err := a.libPath(d.LibraryID)
	if err != nil {
		return err
	}
	archiveMu.Lock()
	defer archiveMu.Unlock()
	rel := d.Path
	if d.Status == "inbox" || d.Source != "folder" {
		rel = uniquePath(root, targetPath(a.settings().Pattern, d), d.Path)
	}
	dst, err := a.inArchive(d.LibraryID, rel)
	if err != nil {
		return err
	}
	if src != dst {
		if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			return fmt.Errorf("can't create the folder in the archive: %w", err)
		}
		if err := moveFile(src, dst); err != nil {
			return fmt.Errorf("can't write to the archive: %w", err)
		}
		removeEmptyDirs(root, filepath.Dir(src))
	}
	if d.Status != "inbox" {
		if err := writeMeta(ctx, dst, d); err != nil {
			log.Printf("metadata for %d: %v", id, err)
		}
	}
	fsha, size, _ := fileSHA(dst)
	var mtime int64
	if st, err := os.Stat(dst); err == nil {
		mtime = st.ModTime().UnixNano()
	}
	_, err = a.db.Exec(`UPDATE docs SET status = 'filed', path = ?, file_sha = ?, size = ?, mtime = ?, updated_at = ? WHERE id = ?`,
		rel, fsha, size, mtime, now(), id)
	a.dropCache(id)
	return err
}

// removeEmptyDirs cleans up month/year folders left empty by a rename.
func removeEmptyDirs(root, dir string) {
	root = filepath.Clean(root)
	for dir = filepath.Clean(dir); strings.HasPrefix(dir, root+string(os.PathSeparator)); dir = filepath.Dir(dir) {
		if err := os.Remove(dir); err != nil {
			return
		}
	}
}

// --- files that appear in the archive ---

var ymPath = regexp.MustCompile(`(?:^|/)((?:19|20)\d{2})(?:/|[-_ ])(0[1-9]|1[0-2])(?:/|[-_ ]|$)`)
var yPath = regexp.MustCompile(`(?:^|/)((?:19|20)\d{2})(?:/|$)`)

// processFolderFile adds a PDF someone copied into the archive (e.g. onto the
// NAS) to the library. It stays where it is; it only gets a text layer if it
// has none.
func (a *App) processFolderFile(ctx context.Context, lib int64, rel string, step func(string)) (int64, error) {
	abs, err := a.inArchive(lib, rel)
	if err != nil {
		return 0, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return 0, err
	}
	srcSHA, _, err := fileSHA(abs)
	if err != nil {
		return 0, err
	}
	if d, err := a.docBySHA(lib, srcSHA, true); err == nil {
		if d.Status == "filed" {
			if p, err := a.inArchive(lib, d.Path); err == nil && d.Path != rel && fileExists(p) {
				return d.ID, dupError{d}
			}
		}
		// The known file was moved or renamed: follow it.
		a.db.Exec(`UPDATE docs SET status = 'filed', path = ?, mtime = ?, size = ?, updated_at = ? WHERE id = ?`, rel, st.ModTime().UnixNano(), st.Size(), now(), d.ID)
		return d.ID, nil
	}
	s := a.settings()
	text := pdfText(ctx, abs)
	ocr := "pdf"
	var ocrMillis int64
	if !hasText(text) && s.WatchOCR {
		step("Recognizing text")
		started := time.Now()
		// keep the untouched original on the local disk, just in case
		copyFile(abs, filepath.Join(a.cfg.DataDir, "originals", srcSHA+".pdf"))
		tmp := filepath.Join(filepath.Dir(abs), ".shoebox-"+randomToken(6)+".pdf")
		t, err := ocrPDF(ctx, abs, tmp, s.Languages)
		if err != nil {
			os.Remove(tmp)
			return 0, err
		}
		archiveMu.Lock()
		err = os.Rename(tmp, abs)
		archiveMu.Unlock()
		if err != nil {
			os.Remove(tmp)
			return 0, err
		}
		text, ocr = t, "tesseract"
		ocrMillis = time.Since(started).Milliseconds()
	}
	var aiFacts *Facts
	if s.AI.Enabled && s.AI.BaseURL != "" && s.AI.Model != "" {
		dir, _ := os.MkdirTemp("", "ai")
		if t, err := a.aiOCR(ctx, s.AI, abs, dir); err == nil && hasText(t) {
			text, ocr = t, ocr+"+ai"
		}
		os.RemoveAll(dir)
		if s.AI.Fields {
			aiFacts, _ = a.aiFields(ctx, s.AI, lib, text)
		}
	}
	step("Sorting")
	meta := readMeta(ctx, abs)
	f := a.analyze(lib, text)
	mergeAI(&f, aiFacts)

	date := meta.Date
	slash := filepath.ToSlash(rel)
	if date == "" {
		if m := ymPath.FindStringSubmatch(slash); m != nil {
			ym := m[1] + "-" + m[2]
			if strings.HasPrefix(f.Date, ym) {
				date = f.Date // the text has the exact day of that month
			} else {
				date = ym + "-01"
			}
		} else if m := yPath.FindStringSubmatch(slash); m != nil {
			if strings.HasPrefix(f.Date, m[1]) {
				date = f.Date
			} else {
				date = m[1] + "-01-01"
			}
		} else if f.Date != "" {
			date = f.Date
		} else {
			date = st.ModTime().Format("2006-01-02")
		}
	}
	title := meta.Title
	if title == "" {
		title = titleFromFile(rel)
	}
	sender := meta.Author
	if sender == "" {
		sender = f.Sender
	}
	tags := meta.Tags
	for _, t := range f.Tags {
		if !containsFold(tags, t) {
			tags = append(tags, t)
		}
	}
	st, _ = os.Stat(abs)
	fsha, size, _ := fileSHA(abs)
	return a.insertDoc(NewDoc{
		Status: "filed", Path: rel, SHA256: srcSHA, FileSHA: fsha, Title: title, Sender: sender, Date: date,
		AmountCents: f.AmountCents, Pages: pdfPages(ctx, abs), Size: size, MTime: st.ModTime().UnixNano(),
		Source: "folder", OCR: ocr, Rule: f.Rule, Tags: tags, Text: text, OCRMillis: ocrMillis, LibraryID: lib,
	})
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

package main

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const maxUpload = 200 << 20

func (a *App) handleOverview(w http.ResponseWriter, r *http.Request) {
	lib, ok := a.currentLib(w, r, false)
	if !ok {
		return
	}
	var inbox, filed, missing int
	a.db.QueryRow(`SELECT count(*) FROM docs WHERE library_id = ? AND status = 'inbox'`, lib).Scan(&inbox)
	a.db.QueryRow(`SELECT count(*) FROM docs WHERE library_id = ? AND status = 'filed'`, lib).Scan(&filed)
	a.db.QueryRow(`SELECT count(*) FROM docs WHERE library_id = ? AND status = 'missing'`, lib).Scan(&missing)
	review, _, _ := a.listDocs(DocQuery{Library: lib, Status: "inbox", Sort: "added", Limit: 12})
	recent, _, _ := a.listDocs(DocQuery{Library: lib, Status: "filed", Sort: "added", Limit: 8})
	busy := 0
	for _, j := range a.jobs.snapshot() {
		if j.LibraryID == lib && (j.State == "queued" || j.State == "working") {
			busy++
		}
	}
	writeJSON(w, 200, map[string]any{"inbox": inbox, "filed": filed, "missing": missing, "review": review, "recent": recent, "busy": busy})
}

func (a *App) handleDocs(w http.ResponseWriter, r *http.Request) {
	lib, ok := a.currentLib(w, r, false)
	if !ok {
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	docs, total, err := a.listDocs(DocQuery{Library: lib, Q: q.Get("q"), Status: q.Get("status"), Year: q.Get("year"), Month: q.Get("month"),
		Tag: q.Get("tag"), Sender: q.Get("sender"), Sort: q.Get("sort"), Limit: limit, Offset: offset})
	if err != nil {
		fail(w, 400, "Search failed: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"docs": docs, "total": total})
}

func (a *App) handleDoc(w http.ResponseWriter, r *http.Request) {
	d, ok := a.loadDoc(w, r, false)
	if !ok {
		return
	}
	writeJSON(w, 200, map[string]any{"doc": d, "text": a.docText(d.ID), "stamps": a.docStamps(d.ID)})
}

func (a *App) handleDocUpdate(w http.ResponseWriter, r *http.Request) {
	d, ok := a.loadDoc(w, r, true)
	if !ok {
		return
	}
	id := d.ID
	var p DocPatch
	if !readJSON(w, r, &p) {
		return
	}
	if err := a.patchDoc(id, p); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if d.Status == "filed" {
		// rename or move the file to match, and refresh its metadata
		if err := a.fileDoc(r.Context(), id); err != nil {
			fail(w, 500, "Saved, but the file could not be updated: "+err.Error())
			return
		}
	}
	d, _ = a.getDoc(id)
	writeJSON(w, 200, map[string]any{"doc": d})
}

// handleDocFile files a document from the review inbox (with the last edits).
func (a *App) handleDocFile(w http.ResponseWriter, r *http.Request) {
	d0, ok := a.loadDoc(w, r, true)
	if !ok {
		return
	}
	id := d0.ID
	var p DocPatch
	if r.ContentLength > 0 && !readJSON(w, r, &p) {
		return
	}
	if err := a.patchDoc(id, p); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if err := a.fileDoc(r.Context(), id); err != nil {
		fail(w, 500, err.Error())
		return
	}
	d, _ := a.getDoc(id)
	writeJSON(w, 200, map[string]any{"doc": d})
}

// handleDocDelete moves the PDF to the trash folder on the data disk and
// removes it from the library. Nothing is destroyed right away.
func (a *App) handleDocDelete(w http.ResponseWriter, r *http.Request) {
	d, ok := a.loadDoc(w, r, true)
	if !ok {
		return
	}
	if src, err := a.docFile(d); err == nil {
		archiveMu.Lock()
		dst := filepath.Join(a.cfg.DataDir, "trash", fmt.Sprintf("%s_%d_%s", time.Now().Format("20060102-150405"), d.ID, filepath.Base(src)))
		if err := moveFile(src, dst); err != nil && !os.IsNotExist(err) {
			archiveMu.Unlock()
			fail(w, 500, "Could not move the file to the trash: "+err.Error())
			return
		}
		if root, err := a.libPath(d.LibraryID); err == nil && d.Status != "inbox" {
			removeEmptyDirs(root, filepath.Dir(src))
		}
		archiveMu.Unlock()
	}
	a.db.Exec(`DELETE FROM docs WHERE id = ?`, d.ID)
	a.db.Exec(`DELETE FROM docs_fts WHERE rowid = ?`, d.ID)
	a.dropCache(d.ID)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) handleDocPDF(w http.ResponseWriter, r *http.Request) {
	d, ok := a.loadDoc(w, r, false)
	if !ok {
		return
	}
	p, err := a.docFile(d)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		http.Error(w, "The file is not reachable.", 404)
		return
	}
	defer f.Close()
	st, _ := f.Stat()
	name := filepath.Base(p)
	if d.Status == "inbox" {
		name = pathPart(d.Title) + ".pdf"
	}
	disp := "inline"
	if r.URL.Query().Get("dl") == "1" {
		disp = "attachment"
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", disp+"; filename*=UTF-8''"+url.PathEscape(name))
	w.Header().Set("Content-Security-Policy", "sandbox")
	w.Header().Set("Cache-Control", "private, no-cache")
	http.ServeContent(w, r, "", st.ModTime(), f)
}

func (a *App) cacheFile(id int64, name string) string {
	return filepath.Join(a.cfg.DataDir, "cache", fmt.Sprintf("%d-%s", id, name))
}

func (a *App) dropCache(id int64) {
	files, _ := filepath.Glob(filepath.Join(a.cfg.DataDir, "cache", fmt.Sprintf("%d-*", id)))
	for _, f := range files {
		os.Remove(f)
	}
}

// pageImage renders (and caches) one page as JPEG: phones show these instead of the PDF.
func (a *App) pageImage(ctx context.Context, d *Doc, page int, width int, name string) (string, error) {
	out := a.cacheFile(d.ID, name)
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}
	src, err := a.docFile(d)
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "page")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	prefix := filepath.Join(dir, "p")
	_, err = run(ctx, time.Minute, "pdftoppm", "-f", strconv.Itoa(page), "-l", strconv.Itoa(page), "-scale-to-x", strconv.Itoa(width),
		"-scale-to-y", "-1", "-jpeg", "-jpegopt", "quality=82", "-singlefile", src, prefix)
	if err != nil {
		return "", err
	}
	return out, moveFile(prefix+".jpg", out)
}

func (a *App) serveImage(w http.ResponseWriter, r *http.Request, page, width int, name string) {
	d, ok := a.loadDoc(w, r, false)
	if !ok {
		return
	}
	if page < 1 || (d.Pages > 0 && page > d.Pages) {
		http.NotFound(w, r)
		return
	}
	p, err := a.pageImage(r.Context(), d, page, width, name)
	if err != nil {
		http.Error(w, "Preview not available.", 404)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("Content-Type", "image/jpeg")
	http.ServeFile(w, r, p)
}

func (a *App) handleDocThumb(w http.ResponseWriter, r *http.Request) {
	a.serveImage(w, r, 1, 360, "thumb.jpg")
}

func (a *App) handleDocPage(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.PathValue("n"))
	width := 1400
	if v, _ := strconv.Atoi(r.URL.Query().Get("w")); v >= 2000 {
		width = 2400 // for cropping a page again
	}
	a.serveImage(w, r, n, width, fmt.Sprintf("page-%d-%d.jpg", n, width))
}

// --- incoming files ---

func (a *App) saveParts(r *http.Request, field string, dir string, allow func(name string, head []byte) (string, bool)) ([]string, []string, error) {
	var paths, names []string
	files := r.MultipartForm.File[field]
	for i, fh := range files {
		src, err := fh.Open()
		if err != nil {
			return nil, nil, err
		}
		head := make([]byte, 512)
		n, _ := io.ReadFull(src, head)
		ext, ok := allow(fh.Filename, head[:n])
		if !ok {
			src.Close()
			return nil, nil, fmt.Errorf("%s is not a PDF or an image Shoebox can read (JPEG, PNG, TIFF, WebP).", fh.Filename)
		}
		src.Seek(0, io.SeekStart)
		p := filepath.Join(dir, fmt.Sprintf("in-%03d%s", i, ext))
		if err := saveTo(src, p); err != nil {
			src.Close()
			return nil, nil, err
		}
		src.Close()
		paths = append(paths, p)
		names = append(names, fh.Filename)
	}
	return paths, names, nil
}

func saveTo(src multipart.File, p string) error {
	dst, err := os.Create(p)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	return dst.Close()
}

func sniffKind(name string, head []byte) (string, bool) {
	ct := http.DetectContentType(head)
	switch {
	case strings.HasPrefix(string(head), "%PDF"):
		return ".pdf", true
	case ct == "image/jpeg":
		return ".jpg", true
	case ct == "image/png":
		return ".png", true
	case ct == "image/webp":
		return ".webp", true
	case len(head) > 4 && (string(head[:4]) == "II*\x00" || string(head[:4]) == "MM\x00*"):
		return ".tif", true
	}
	return "", false
}

func imagesOnly(name string, head []byte) (string, bool) {
	ext, ok := sniffKind(name, head)
	return ext, ok && ext != ".pdf"
}

// intakeLib: where new scans and uploads go. Into the account's own Home,
// to be looked at there and shared with Family from Review; ?library=
// picks another one (scripts, the API).
func (a *App) intakeLib(w http.ResponseWriter, r *http.Request) (int64, bool) {
	u := userFrom(r)
	if r.URL.Query().Get("library") == "" {
		if home := a.homeLib(u.ID); home > 0 && a.libRole(u, home) == "editor" {
			return home, true
		}
	}
	return a.currentLib(w, r, true)
}

// handleScan: the pages of one document, photographed and straightened in the browser.
func (a *App) handleScan(w http.ResponseWriter, r *http.Request) {
	lib, ok := a.intakeLib(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		fail(w, 400, "The upload is too large or broken.")
		return
	}
	dir, err := os.MkdirTemp("", "scan")
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	pages, _, err := a.saveParts(r, "pages", dir, imagesOnly)
	if err != nil || len(pages) == 0 {
		os.RemoveAll(dir)
		if err == nil {
			err = fmt.Errorf("No pages received.")
		}
		fail(w, 400, err.Error())
		return
	}
	job := a.jobs.add(&Job{Kind: "scan", Name: fmt.Sprintf("Scan, %d page(s)", len(pages)), files: pages, dir: dir, userID: userFrom(r).ID, LibraryID: lib})
	writeJSON(w, 200, map[string]any{"job": job.ID, "library": lib})
}

// handleUpload: PDFs and photos; each file becomes its own document.
func (a *App) handleUpload(w http.ResponseWriter, r *http.Request) {
	lib, ok := a.intakeLib(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		fail(w, 400, "The upload is too large or broken.")
		return
	}
	var ids []int64
	for i, fh := range r.MultipartForm.File["files"] {
		dir, err := os.MkdirTemp("", "upload")
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		src, err := fh.Open()
		if err != nil {
			os.RemoveAll(dir)
			continue
		}
		head := make([]byte, 512)
		n, _ := io.ReadFull(src, head)
		ext, ok := sniffKind(fh.Filename, head[:n])
		if !ok {
			src.Close()
			os.RemoveAll(dir)
			fail(w, 400, fh.Filename+" is not a PDF or an image Shoebox can read (JPEG, PNG, TIFF, WebP).")
			return
		}
		src.Seek(0, io.SeekStart)
		p := filepath.Join(dir, fmt.Sprintf("in-%03d%s", i, ext))
		err = saveTo(src, p)
		src.Close()
		if err != nil {
			os.RemoveAll(dir)
			fail(w, 500, err.Error())
			return
		}
		job := a.jobs.add(&Job{Kind: "upload", Name: clean(fh.Filename, 200), files: []string{p}, dir: dir, userID: userFrom(r).ID, LibraryID: lib})
		ids = append(ids, job.ID)
	}
	if len(ids) == 0 {
		fail(w, 400, "No files received.")
		return
	}
	writeJSON(w, 200, map[string]any{"jobs": ids, "library": lib})
}

// handleJobs lists the work on the libraries the account belongs to.
func (a *App) handleJobs(w http.ResponseWriter, r *http.Request) {
	mine := map[int64]bool{}
	for _, l := range a.userLibraries(userFrom(r)) {
		mine[l.ID] = true
	}
	out := []Job{}
	for _, j := range a.jobs.snapshot() {
		if mine[j.LibraryID] {
			out = append(out, j)
		}
	}
	writeJSON(w, 200, out)
}

func (a *App) handleJobDismiss(w http.ResponseWriter, r *http.Request) {
	a.jobs.dismiss(pathID(r, "id"))
	writeJSON(w, 200, map[string]any{"ok": true})
}

// --- browsing ---

func (a *App) handleTree(w http.ResponseWriter, r *http.Request) {
	lib, ok := a.currentLib(w, r, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(`SELECT substr(doc_date,1,4), substr(doc_date,6,2), count(*) FROM docs WHERE status = 'filed' AND library_id = ?
		GROUP BY 1, 2 ORDER BY 1 DESC, 2 DESC`, lib)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	defer rows.Close()
	type month struct {
		Month string `json:"month"`
		Count int    `json:"count"`
	}
	type year struct {
		Year   string  `json:"year"`
		Count  int     `json:"count"`
		Months []month `json:"months"`
	}
	var out []*year
	for rows.Next() {
		var y, m string
		var n int
		rows.Scan(&y, &m, &n)
		if y == "" {
			y = "undated"
		}
		if len(out) == 0 || out[len(out)-1].Year != y {
			out = append(out, &year{Year: y})
		}
		cur := out[len(out)-1]
		cur.Count += n
		if m != "" {
			cur.Months = append(cur.Months, month{m, n})
		}
	}
	if out == nil {
		out = []*year{}
	}
	writeJSON(w, 200, out)
}

type named struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
	Count int    `json:"count"`
}

func (a *App) listNamed(w http.ResponseWriter, q string, args ...any) {
	rows, err := a.db.Query(q, args...)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	defer rows.Close()
	out := []named{}
	for rows.Next() {
		var n named
		rows.Scan(&n.ID, &n.Name, &n.Color, &n.Count)
		out = append(out, n)
	}
	writeJSON(w, 200, out)
}

func (a *App) handleTags(w http.ResponseWriter, r *http.Request) {
	lib, ok := a.currentLib(w, r, false)
	if !ok {
		return
	}
	a.listNamed(w, `SELECT t.id, t.name, t.color, (SELECT count(*) FROM doc_tags dt JOIN docs d ON d.id = dt.doc_id
		WHERE dt.tag_id = t.id AND d.status = 'filed') FROM tags t WHERE t.library_id = ? ORDER BY t.name COLLATE NOCASE`, lib)
}

func (a *App) handleSenders(w http.ResponseWriter, r *http.Request) {
	lib, ok := a.currentLib(w, r, false)
	if !ok {
		return
	}
	a.listNamed(w, `SELECT s.id, s.name, '', (SELECT count(*) FROM docs d WHERE d.sender_id = s.id AND d.status = 'filed')
		FROM senders s WHERE s.library_id = ? ORDER BY s.name COLLATE NOCASE`, lib)
}

// namedAccess: a tag or sender may be changed by editors of its library.
func (a *App) namedAccess(w http.ResponseWriter, r *http.Request, table string, id int64) bool {
	var lib int64
	if a.db.QueryRow(`SELECT library_id FROM `+table+` WHERE id = ?`, id).Scan(&lib) != nil {
		fail(w, 404, "Not found.")
		return false
	}
	switch a.libRole(userFrom(r), lib) {
	case "editor":
		return true
	case "":
		fail(w, 404, "Not found.")
	default:
		fail(w, 403, "You can only read in this library.")
	}
	return false
}

// rename; renaming onto an existing name merges the two
func (a *App) renameNamed(w http.ResponseWriter, r *http.Request, table string) {
	if !a.namedAccess(w, r, table, pathID(r, "id")) {
		return
	}
	var in struct{ Name string }
	if !readJSON(w, r, &in) {
		return
	}
	a.renameTo(w, pathID(r, "id"), table, in.Name)
}

func (a *App) renameTo(w http.ResponseWriter, id int64, table, newName string) {
	name := clean(newName, 120)
	if name == "" {
		fail(w, 400, "The name can't be empty.")
		return
	}
	tx, err := a.db.Begin()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	defer tx.Rollback()
	var other int64
	tx.QueryRow(`SELECT id FROM `+table+` WHERE name = ? AND id <> ? AND library_id = (SELECT library_id FROM `+table+` WHERE id = ?)`, name, id, id).Scan(&other)
	var affected []int64
	collect := func(q string, args ...any) {
		rows, err := tx.Query(q, args...)
		if err != nil {
			return
		}
		defer rows.Close()
		for rows.Next() {
			var x int64
			rows.Scan(&x)
			affected = append(affected, x)
		}
	}
	if table == "tags" {
		collect(`SELECT doc_id FROM doc_tags WHERE tag_id = ?`, id)
		if other > 0 {
			tx.Exec(`INSERT OR IGNORE INTO doc_tags(doc_id, tag_id) SELECT doc_id, ? FROM doc_tags WHERE tag_id = ?`, other, id)
			tx.Exec(`DELETE FROM tags WHERE id = ?`, id)
		} else {
			tx.Exec(`UPDATE tags SET name = ? WHERE id = ?`, name, id)
		}
	} else {
		collect(`SELECT id FROM docs WHERE sender_id = ?`, id)
		if other > 0 {
			tx.Exec(`UPDATE docs SET sender_id = ? WHERE sender_id = ?`, other, id)
			tx.Exec(`DELETE FROM senders WHERE id = ?`, id)
		} else {
			tx.Exec(`UPDATE senders SET name = ? WHERE id = ?`, name, id)
		}
	}
	for _, d := range affected {
		reindex(tx, d, nil)
	}
	if err := tx.Commit(); err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "merged": other > 0})
}

// tag colors are names from the app's palette; empty = picked from the name
var tagColors = map[string]bool{"": true, "sand": true, "clay": true, "rose": true, "plum": true, "sky": true,
	"teal": true, "moss": true, "olive": true, "amber": true, "slate": true}

// nearestTagColor maps a color from elsewhere (Paperless uses "#a6cee3") to
// the closest one of the palette.
func nearestTagColor(hex string) string {
	ref := map[string][3]int{"sand": {200, 162, 122}, "clay": {201, 113, 75}, "rose": {201, 91, 120}, "plum": {142, 98, 174},
		"sky": {77, 139, 194}, "teal": {46, 154, 140}, "moss": {94, 154, 69}, "olive": {163, 154, 58}, "amber": {224, 168, 46}, "slate": {123, 127, 140}}
	var r, g, b int
	if _, err := fmt.Sscanf(strings.TrimPrefix(hex, "#"), "%02x%02x%02x", &r, &g, &b); err != nil {
		return ""
	}
	best, bd := "", 1<<30
	for k, c := range ref {
		d := (r-c[0])*(r-c[0]) + (g-c[1])*(g-c[1]) + (b-c[2])*(b-c[2])
		if d < bd || (d == bd && k < best) {
			best, bd = k, d
		}
	}
	return best
}

func (a *App) handleTagUpdate(w http.ResponseWriter, r *http.Request) {
	if !a.namedAccess(w, r, "tags", pathID(r, "id")) {
		return
	}
	var in struct {
		Name  *string `json:"name"`
		Color *string `json:"color"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	id := pathID(r, "id")
	if in.Color != nil {
		if !tagColors[*in.Color] {
			fail(w, 400, "Unknown color.")
			return
		}
		a.db.Exec(`UPDATE tags SET color = ? WHERE id = ?`, *in.Color, id)
		if in.Name == nil {
			writeJSON(w, 200, map[string]any{"ok": true})
			return
		}
	}
	if in.Name == nil {
		fail(w, 400, "Nothing to change.")
		return
	}
	a.renameTo(w, id, "tags", *in.Name)
}
func (a *App) handleSenderUpdate(w http.ResponseWriter, r *http.Request) {
	a.renameNamed(w, r, "senders")
}

func (a *App) handleTagDelete(w http.ResponseWriter, r *http.Request) {
	id := pathID(r, "id")
	if !a.namedAccess(w, r, "tags", id) {
		return
	}
	var docs []int64
	rows, _ := a.db.Query(`SELECT doc_id FROM doc_tags WHERE tag_id = ?`, id)
	for rows != nil && rows.Next() {
		var x int64
		rows.Scan(&x)
		docs = append(docs, x)
	}
	if rows != nil {
		rows.Close()
	}
	a.db.Exec(`DELETE FROM tags WHERE id = ?`, id)
	for _, d := range docs {
		reindex(a.db, d, nil)
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) handleSenderDelete(w http.ResponseWriter, r *http.Request) {
	id := pathID(r, "id")
	if !a.namedAccess(w, r, "senders", id) {
		return
	}
	a.db.Exec(`UPDATE docs SET sender_id = NULL WHERE sender_id = ?`, id)
	a.db.Exec(`DELETE FROM senders WHERE id = ?`, id)
	writeJSON(w, 200, map[string]any{"ok": true})
}

// --- storage ---

func (a *App) handleStorage(w http.ResponseWriter, r *http.Request) {
	root := a.cfg.Archive
	res := map[string]any{"archive": root, "data": a.cfg.DataDir}
	var st syscall.Statfs_t
	if err := syscall.Statfs(root, &st); err == nil {
		res["free"] = int64(st.Bavail) * int64(st.Bsize)
		res["total"] = int64(st.Blocks) * int64(st.Bsize)
	}
	res["fs"] = mountType(root)
	probe := filepath.Join(root, ".shoebox-write-test")
	if err := os.WriteFile(probe, []byte("ok"), 0o640); err != nil {
		res["writable"] = false
		res["write_error"] = err.Error()
	} else {
		os.Remove(probe)
		res["writable"] = true
	}
	last, found, errMsg := a.watcher.status()
	if !last.IsZero() {
		res["last_scan"] = last.UTC().Format(time.RFC3339)
	}
	res["last_found"] = found
	res["scan_error"] = errMsg
	var missing int
	a.db.QueryRow(`SELECT count(*) FROM docs WHERE status = 'missing'`).Scan(&missing)
	res["missing"] = missing
	res["uid"] = os.Getuid()
	var libs []map[string]any
	for _, l := range a.allLibraries() {
		if l.ID == 1 {
			continue
		}
		m := map[string]any{"id": l.ID, "name": l.Name, "path": l.Path, "docs": l.Docs, "fs": mountType(l.Path)}
		if err := syscall.Statfs(l.Path, &st); err == nil {
			m["free"] = int64(st.Bavail) * int64(st.Bsize)
			m["total"] = int64(st.Blocks) * int64(st.Bsize)
		}
		probe := filepath.Join(l.Path, ".shoebox-write-test")
		if err := os.WriteFile(probe, []byte("ok"), 0o640); err != nil {
			m["writable"], m["write_error"] = false, err.Error()
		} else {
			os.Remove(probe)
			m["writable"] = true
		}
		libs = append(libs, m)
	}
	res["libraries"] = libs
	writeJSON(w, 200, res)
}

// mountType finds the file system type of the mount a path lives on (nfs, ext4 …).
func mountType(p string) string {
	b, err := os.ReadFile("/proc/self/mounts")
	if err != nil {
		return ""
	}
	p, _ = filepath.Abs(p)
	if rp, err := filepath.EvalSymlinks(p); err == nil {
		p = rp
	}
	best, typ := "", ""
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		mp := strings.ReplaceAll(f[1], `\040`, " ")
		if (p == mp || strings.HasPrefix(p, strings.TrimRight(mp, "/")+"/")) && len(mp) > len(best) {
			best, typ = mp, f[2]
		}
	}
	return typ
}

func (a *App) handleRescan(w http.ResponseWriter, r *http.Request) {
	a.watcher.kick()
	writeJSON(w, 200, map[string]any{"ok": true})
}

// spa serves the built app; unknown paths get index.html (client-side routes).
func spa(dist fs.FS) http.Handler {
	files := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if strings.HasPrefix(p, "api/") {
			fail(w, 404, "Not found.")
			return
		}
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(dist, p); err != nil {
			p = "index.html"
		}
		switch {
		case p == "index.html" || p == "sw.js" || p == "manifest.webmanifest":
			w.Header().Set("Cache-Control", "no-cache")
		case strings.HasPrefix(p, "assets/"):
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		if p == "index.html" {
			b, err := fs.ReadFile(dist, "index.html")
			if err != nil {
				http.Error(w, "The app was not built (run the web build).", 500)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(b)
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/" + p
		files.ServeHTTP(w, r2)
	})
}

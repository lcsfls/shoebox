package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Writing letters: drafts per library, a letterhead per library, a preview,
// and "finish": the PDF goes into the archive like any other document
// (tag "Postausgang" / "Sent"), ready to print.

func (a *App) letterhead(lib int64) Letterhead {
	var lh Letterhead
	var raw string
	if a.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, fmt.Sprintf("letterhead:%d", lib)).Scan(&raw) == nil {
		json.Unmarshal([]byte(raw), &lh)
	}
	if lh.HasLogo {
		if b, err := os.ReadFile(a.logoPath(lib)); err == nil {
			lh.logoJPEG = b
		} else {
			lh.HasLogo = false
		}
	}
	return lh
}

func (a *App) saveLetterhead(lib int64, lh Letterhead) {
	b, _ := json.Marshal(lh)
	a.db.Exec(`INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, fmt.Sprintf("letterhead:%d", lib), string(b))
}

func (a *App) logoPath(lib int64) string {
	return filepath.Join(a.cfg.DataDir, "letterheads", fmt.Sprintf("%d.jpg", lib))
}

func (a *App) handleLetterhead(w http.ResponseWriter, r *http.Request) {
	lib, ok := a.currentLib(w, r, false)
	if !ok {
		return
	}
	writeJSON(w, 200, a.letterhead(lib))
}

func (a *App) handleLetterheadSave(w http.ResponseWriter, r *http.Request) {
	lib, ok := a.currentLib(w, r, true)
	if !ok {
		return
	}
	var in Letterhead
	if !readJSON(w, r, &in) {
		return
	}
	cur := a.letterhead(lib)
	lh := Letterhead{Name: clean(in.Name, 120), Address: cleanLines(in.Address, 4, 120), Phone: clean(in.Phone, 60),
		Email: clean(in.Email, 120), Web: clean(in.Web, 120), Footer: cleanLines(in.Footer, 17, 120), Signer: clean(in.Signer, 120),
		HasLogo: cur.HasLogo, LogoW: cur.LogoW, LogoH: cur.LogoH}
	a.saveLetterhead(lib, lh)
	writeJSON(w, 200, lh)
}

func (a *App) handleLogoUpload(w http.ResponseWriter, r *http.Request) {
	lib, ok := a.currentLib(w, r, true)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 5<<20)
	if err := r.ParseMultipartForm(5 << 20); err != nil {
		fail(w, 400, "The picture is too large (at most 5 MB).")
		return
	}
	f, _, err := r.FormFile("logo")
	if err != nil {
		fail(w, 400, "No picture received.")
		return
	}
	defer f.Close()
	data, _ := io.ReadAll(f)
	jpg, pw, ph, err := logoToJPEG(data)
	if err != nil {
		fail(w, 400, "The logo: "+err.Error()+".")
		return
	}
	os.MkdirAll(filepath.Dir(a.logoPath(lib)), 0o750)
	if err := os.WriteFile(a.logoPath(lib), jpg, 0o640); err != nil {
		fail(w, 500, err.Error())
		return
	}
	lh := a.letterhead(lib)
	lh.HasLogo, lh.LogoW, lh.LogoH = true, pw, ph
	a.saveLetterhead(lib, lh)
	writeJSON(w, 200, lh)
}

func (a *App) handleLogoDelete(w http.ResponseWriter, r *http.Request) {
	lib, ok := a.currentLib(w, r, true)
	if !ok {
		return
	}
	os.Remove(a.logoPath(lib))
	lh := a.letterhead(lib)
	lh.HasLogo, lh.LogoW, lh.LogoH = false, 0, 0
	a.saveLetterhead(lib, lh)
	writeJSON(w, 200, lh)
}

func (a *App) handleLogo(w http.ResponseWriter, r *http.Request) {
	lib, ok := a.currentLib(w, r, false)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, a.logoPath(lib))
}

func cleanLines(s string, maxLines, maxLen int) string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r", ""), "\n") {
		if len(out) >= maxLines {
			break
		}
		out = append(out, clean(l, maxLen))
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n ")
}

// --- letters ---

const letterCols = `l.id, l.status, l.form, l.lang, l.recipient, l.your_ref, l.our_ref, l.date, l.subject, l.salutation, l.body,
	l.closing, l.signature, l.enclosures, COALESCE(l.doc_id, 0), COALESCE((SELECT COALESCE(NULLIF(u.name,''), u.username) FROM users u WHERE u.id = l.created_by), ''),
	l.created_at, l.updated_at`

func scanLetter(row scanner) (*Letter, error) {
	var l Letter
	err := row.Scan(&l.ID, &l.Status, &l.Form, &l.Lang, &l.Recipient, &l.YourRef, &l.OurRef, &l.Date, &l.Subject, &l.Salutation, &l.Body,
		&l.Closing, &l.Signature, &l.Enclosures, &l.DocID, &l.CreatedBy, &l.CreatedAt, &l.UpdatedAt)
	return &l, err
}

// loadLetter reads the letter of the request, in the current library.
func (a *App) loadLetter(w http.ResponseWriter, r *http.Request, edit bool) (*Letter, int64, bool) {
	lib, ok := a.currentLib(w, r, edit)
	if !ok {
		return nil, 0, false
	}
	l, err := scanLetter(a.db.QueryRow(`SELECT `+letterCols+` FROM letters l WHERE l.id = ? AND l.library_id = ?`, pathID(r, "id"), lib))
	if err != nil {
		fail(w, 404, "No such letter.")
		return nil, 0, false
	}
	return l, lib, true
}

func (a *App) handleLetters(w http.ResponseWriter, r *http.Request) {
	lib, ok := a.currentLib(w, r, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(`SELECT `+letterCols+` FROM letters l WHERE l.library_id = ? ORDER BY l.status = 'final', l.updated_at DESC LIMIT 300`, lib)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	defer rows.Close()
	out := []*Letter{}
	for rows.Next() {
		if l, err := scanLetter(rows); err == nil {
			out = append(out, l)
		}
	}
	writeJSON(w, 200, out)
}

// handleLetterCreate starts a draft; from=<id> copies another letter.
func (a *App) handleLetterCreate(w http.ResponseWriter, r *http.Request) {
	lib, ok := a.currentLib(w, r, true)
	if !ok {
		return
	}
	var in struct {
		From int64  `json:"from"`
		Lang string `json:"lang"`
	}
	readJSON(w, r, &in)
	lh := a.letterhead(lib)
	l := Letter{Form: "B", Lang: "de", Date: time.Now().Format("2006-01-02"), Salutation: "Sehr geehrte Damen und Herren,",
		Closing: "Mit freundlichen Grüßen", Signature: firstNonEmpty(lh.Signer, lh.Name, userFrom(r).Name)}
	if in.Lang == "en" {
		l.Lang, l.Salutation, l.Closing = "en", "Dear Sir or Madam,", "Yours faithfully"
	}
	if in.From > 0 {
		if src, err := scanLetter(a.db.QueryRow(`SELECT `+letterCols+` FROM letters l WHERE l.id = ? AND l.library_id = ?`, in.From, lib)); err == nil {
			l = *src
			l.Date = time.Now().Format("2006-01-02")
		}
	}
	ts := now()
	res, err := a.db.Exec(`INSERT INTO letters(library_id, status, form, lang, recipient, your_ref, our_ref, date, subject, salutation, body, closing, signature, enclosures, created_by, created_at, updated_at)
		VALUES(?, 'draft', ?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, lib, l.Form, l.Lang, l.Recipient, l.YourRef, l.OurRef, l.Date, l.Subject, l.Salutation, l.Body,
		l.Closing, l.Signature, l.Enclosures, nullID(userFrom(r).ID), ts, ts)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, 200, map[string]any{"id": id})
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if strings.TrimSpace(x) != "" {
			return x
		}
	}
	return ""
}

func (a *App) handleLetter(w http.ResponseWriter, r *http.Request) {
	l, _, ok := a.loadLetter(w, r, false)
	if !ok {
		return
	}
	writeJSON(w, 200, l)
}

func (a *App) handleLetterSave(w http.ResponseWriter, r *http.Request) {
	l, _, ok := a.loadLetter(w, r, true)
	if !ok {
		return
	}
	if l.Status != "draft" {
		fail(w, 400, "This letter is finished. Make a copy to change it.")
		return
	}
	var in Letter
	if !readJSON(w, r, &in) {
		return
	}
	if in.Form != "A" {
		in.Form = "B"
	}
	if in.Lang != "en" {
		in.Lang = "de"
	}
	if !validDate(in.Date) {
		in.Date = time.Now().Format("2006-01-02")
	}
	a.db.Exec(`UPDATE letters SET form=?, lang=?, recipient=?, your_ref=?, our_ref=?, date=?, subject=?, salutation=?, body=?, closing=?, signature=?, enclosures=?, updated_at=? WHERE id=?`,
		in.Form, in.Lang, cleanLines(in.Recipient, 6, 120), clean(in.YourRef, 60), clean(in.OurRef, 60), in.Date, clean(in.Subject, 200),
		clean(in.Salutation, 200), limitText(in.Body, 60000), clean(in.Closing, 200), clean(in.Signature, 200),
		cleanLines(in.Enclosures, 12, 120), now(), l.ID)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) handleLetterDelete(w http.ResponseWriter, r *http.Request) {
	l, _, ok := a.loadLetter(w, r, true)
	if !ok {
		return
	}
	if l.Status != "draft" {
		fail(w, 400, "Finished letters stay; delete the document in the archive instead.")
		return
	}
	a.db.Exec(`DELETE FROM letters WHERE id = ?`, l.ID)
	writeJSON(w, 200, map[string]any{"ok": true})
}

// handleLetterPDF: the draft as a PDF (to look at or print), or the
// archived document of a finished letter.
func (a *App) handleLetterPDF(w http.ResponseWriter, r *http.Request) {
	l, lib, ok := a.loadLetter(w, r, false)
	if !ok {
		return
	}
	if l.Status == "final" && l.DocID > 0 {
		if d, err := a.getDoc(l.DocID); err == nil {
			if p, err := a.docFile(d); err == nil {
				w.Header().Set("Content-Type", "application/pdf")
				w.Header().Set("Content-Disposition", "inline; filename=\""+letterFileName(l)+"\"")
				http.ServeFile(w, r, p)
				return
			}
		}
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", "inline; filename=\""+letterFileName(l)+"\"")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(renderLetter(a.letterhead(lib), *l))
}

func letterFileName(l *Letter) string {
	n := pathPart(l.Subject)
	if n == "" {
		n = "Brief"
	}
	return l.Date + "_" + n + ".pdf"
}

// handleLetterPreview renders the pages as pictures (what will be printed).
func (a *App) handleLetterPreview(w http.ResponseWriter, r *http.Request) {
	l, lib, ok := a.loadLetter(w, r, false)
	if !ok {
		return
	}
	// the edits not saved yet can come along
	if r.ContentLength > 0 {
		var in Letter
		if readJSON(w, r, &in) {
			in.ID, in.Status = l.ID, l.Status
			if in.Form != "A" {
				in.Form = "B"
			}
			l = &in
		} else {
			return
		}
	}
	dir, err := os.MkdirTemp("", "letter")
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	defer os.RemoveAll(dir)
	// sharp on a high-resolution screen too (the PDF itself is vector text)
	pdf := filepath.Join(dir, "letter.pdf")
	os.WriteFile(pdf, renderLetter(a.letterhead(lib), *l), 0o600)
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "pdftoppm", "-r", "130", "-jpeg", "-jpegopt", "quality=85", pdf, filepath.Join(dir, "p")).Run(); err != nil {
		fail(w, 500, "Preview failed: "+err.Error())
		return
	}
	files, _ := filepath.Glob(filepath.Join(dir, "p-*.jpg"))
	pages := []string{}
	for _, f := range sortedPages(files) {
		b, _ := os.ReadFile(f)
		pages = append(pages, "data:image/jpeg;base64,"+b64(b))
	}
	writeJSON(w, 200, map[string]any{"pages": pages})
}

// handleLetterFinish puts the letter into the archive and freezes it.
func (a *App) handleLetterFinish(w http.ResponseWriter, r *http.Request) {
	l, lib, ok := a.loadLetter(w, r, true)
	if !ok {
		return
	}
	if l.Status != "draft" {
		fail(w, 400, "This letter is already finished.")
		return
	}
	if strings.TrimSpace(l.Recipient) == "" || strings.TrimSpace(l.Body) == "" {
		fail(w, 400, "The letter needs a recipient and some text.")
		return
	}
	pdf := renderLetter(a.letterhead(lib), *l)
	name := fmt.Sprintf("letter-%d-%s.pdf", l.ID, randomToken(4))
	p := filepath.Join(a.cfg.DataDir, "inbox", name)
	if err := os.WriteFile(p, pdf, 0o640); err != nil {
		fail(w, 500, err.Error())
		return
	}
	recipient := strings.TrimSpace(strings.SplitN(strings.TrimSpace(l.Recipient), "\n", 2)[0])
	tag := "Postausgang"
	if l.Lang == "en" {
		tag = "Sent"
	}
	text := pdfText(r.Context(), p)
	fsha, size, _ := fileSHA(p)
	title := l.Subject
	if title == "" {
		title = "Brief an " + recipient
	}
	id, err := a.insertDoc(NewDoc{LibraryID: lib, Status: "inbox", Path: name, SHA256: fsha, FileSHA: fsha, Title: clean(title, 200),
		Sender: recipient, Date: l.Date, Pages: pdfPages(r.Context(), p), Size: size, Source: "letter", OCR: "pdf",
		Tags: []string{tag}, Text: text, AddedBy: userFrom(r).ID})
	if err != nil {
		os.Remove(p)
		fail(w, 500, err.Error())
		return
	}
	if err := a.fileDoc(r.Context(), id); err != nil {
		fail(w, 500, "Saved, but filing failed: "+err.Error())
		return
	}
	a.db.Exec(`UPDATE letters SET status = 'final', doc_id = ?, updated_at = ? WHERE id = ?`, id, now(), l.ID)
	writeJSON(w, 200, map[string]any{"ok": true, "doc_id": id})
}

// handleRecipients: addresses used before in this library, newest first.
func (a *App) handleRecipients(w http.ResponseWriter, r *http.Request) {
	lib, ok := a.currentLib(w, r, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(`SELECT recipient, max(updated_at) m FROM letters WHERE library_id = ? AND recipient <> '' GROUP BY recipient ORDER BY m DESC LIMIT 40`, lib)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		var m sql.NullString
		rows.Scan(&s, &m)
		out = append(out, s)
	}
	writeJSON(w, 200, out)
}

func sortedPages(files []string) []string {
	num := func(f string) int {
		b := strings.TrimSuffix(filepath.Base(f), ".jpg")
		n, _ := strconv.Atoi(b[strings.LastIndex(b, "-")+1:])
		return n
	}
	for i := 1; i < len(files); i++ {
		for j := i; j > 0 && num(files[j]) < num(files[j-1]); j-- {
			files[j], files[j-1] = files[j-1], files[j]
		}
	}
	return files
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func limitText(s string, n int) string {
	s = strings.TrimRight(strings.ReplaceAll(s, "\r", ""), " \n")
	if len(s) > n {
		s = s[:n]
	}
	return s
}

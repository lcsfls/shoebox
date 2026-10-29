package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Stamps burnt into the PDF: "EINGANG" when a letter came in, "BEZAHLT"
// when it's paid, with the date, who stamped it and the SHA-256 of the
// file as it was before (so a copy can be checked against the archive).
// The stamp is real PDF text and lines, laid over the page with qpdf; the
// page underneath stays as it is, and its text stays searchable.

// stampWidth is the stamp's width as a share of the page width.
const stampWidth = 0.27

// the stamp is drawn at this size (points) and scaled to the page
const stampW, stampH = 150.0, 70.0

type stampSpec struct {
	Label string // EINGANG, BEZAHLT
	Date  string // as printed, e.g. 27.10.2026
	Note  string // optional, e.g. "per Überweisung"
	By    string // who stamped it
	Hash  string // hex SHA-256 of the file before the stamp
	Color [3]float64
	X, Y  float64 // top left of the stamp, as shares of the shown page
	Angle float64 // degrees, counter-clockwise; keep 0 so the stamp's words stay searchable
}

var rePageInfo = regexp.MustCompile(`Page\s+\d+\s+size:\s+([\d.]+) x ([\d.]+) pts`)
var rePageRot = regexp.MustCompile(`Page\s+\d+\s+rot:\s+(\d+)`)

// pageBox is a page's size in points as it is shown (rotation applied).
func pageBox(ctx context.Context, path string, page int) (w, h float64, rot int, err error) {
	out, err := run(ctx, time.Minute, "pdfinfo", "-f", strconv.Itoa(page), "-l", strconv.Itoa(page), path)
	if err != nil {
		return 0, 0, 0, err
	}
	m := rePageInfo.FindSubmatch(out)
	if m == nil {
		return 0, 0, 0, errors.New("unknown page size")
	}
	w, _ = strconv.ParseFloat(string(m[1]), 64)
	h, _ = strconv.ParseFloat(string(m[2]), 64)
	if r := rePageRot.FindSubmatch(out); r != nil {
		rot, _ = strconv.Atoi(string(r[1]))
	}
	if rot == 90 || rot == 270 {
		w, h = h, w
	}
	return w, h, rot, nil
}

// winAnsi writes text for a PDF string in WinAnsiEncoding (umlauts, €).
func winAnsi(s string) string {
	var b strings.Builder
	for _, r := range s {
		var c int
		switch {
		case r == '(' || r == ')' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
			continue
		case r < 128 && r >= 32:
			b.WriteRune(r)
			continue
		case r >= 0xA0 && r <= 0xFF:
			c = int(r)
		case r == '€':
			c = 0x80
		case r == '–':
			c = 0x96
		case r == '—':
			c = 0x97
		case r == '„':
			c = 0x84
		case r == '“':
			c = 0x93
		case r == '”':
			c = 0x94
		case r == '‚':
			c = 0x82
		case r == '‘':
			c = 0x91
		case r == '’':
			c = 0x92
		case r == '…':
			c = 0x85
		case r == '·':
			c = 0xB7
		default:
			x, ok := ansi(r) // Š, Ž, Œ and the rest of WinAnsi
			if !ok {
				b.WriteByte('?')
				continue
			}
			c = int(x)
		}
		fmt.Fprintf(&b, "\\%03o", c)
	}
	return b.String()
}

// textWidth estimates Helvetica text width (for centering).
func textWidth(s string, size float64, bold bool) float64 {
	n := 0.0
	for _, r := range s {
		switch {
		case r == ' ':
			n += 0.278
		case strings.ContainsRune("iljI.,:;!|'", r):
			n += 0.28
		case strings.ContainsRune("mwMW", r):
			n += 0.86
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9', strings.ContainsRune("ÄÖÜ", r):
			n += 0.68
		default:
			n += 0.55
		}
	}
	if bold {
		n *= 1.06
	}
	return n * size
}

// stampPDF makes a one-page PDF the size of the page (unturned, w × h) with
// the stamp on it; pw × ph is the page as shown, turned by rot degrees.
func stampPDF(pw, ph float64, rot int, s stampSpec) []byte {
	w, h := pw, ph
	if rot == 90 || rot == 270 {
		w, h = ph, pw
	}
	k := stampWidth * pw / stampW // scale from drawing size to the page
	x0 := s.X * pw
	y0 := ph - s.Y*ph - stampH*k // PDF y grows upwards
	cx, cy := stampW/2, stampH/2
	a := s.Angle * math.Pi / 180
	cos, sin := math.Cos(a), math.Sin(a)
	col := fmt.Sprintf("%.3f %.3f %.3f", s.Color[0], s.Color[1], s.Color[2])

	var c bytes.Buffer
	c.WriteString("q\n")
	// the page is shown turned: draw in the shown page's coordinates
	switch rot {
	case 90:
		fmt.Fprintf(&c, "0 1 -1 0 %.2f 0 cm\n", w)
	case 180:
		fmt.Fprintf(&c, "-1 0 0 -1 %.2f %.2f cm\n", w, h)
	case 270:
		fmt.Fprintf(&c, "0 -1 1 0 0 %.2f cm\n", h)
	}
	// place, scale, then turn around the stamp's centre
	fmt.Fprintf(&c, "%.4f 0 0 %.4f %.2f %.2f cm\n", k, k, x0, y0)
	fmt.Fprintf(&c, "1 0 0 1 %.2f %.2f cm\n", cx, cy)
	fmt.Fprintf(&c, "%.5f %.5f %.5f %.5f 0 0 cm\n", cos, sin, -sin, cos)
	fmt.Fprintf(&c, "1 0 0 1 %.2f %.2f cm\n", -cx, -cy)
	fmt.Fprintf(&c, "%s RG %s rg\n", col, col)
	// two frames, like an ink stamp
	c.WriteString("2.2 w 1 1 148 68 re S\n0.7 w 4.5 4.5 141 61 re S\n")
	center := func(text, font string, size, y float64, bold bool) {
		if text == "" {
			return
		}
		x := (stampW - textWidth(text, size, bold)) / 2
		if x < 7 {
			x = 7
		}
		fmt.Fprintf(&c, "BT /%s %.1f Tf %.2f %.2f Td (%s) Tj ET\n", font, size, x, y, winAnsi(text))
	}
	label := strings.ToUpper(s.Label)
	size := 20.0
	if w := textWidth(label, size, true); w > 132 {
		size = size * 132 / w
	}
	center(label, "F2", size, 45, true)
	fmt.Fprintf(&c, "0.6 w 14 41 m 136 41 l S\n")
	center(s.Date, "F2", 10.5, 29, true)
	line := s.By
	if s.Note != "" {
		line = s.Note
		if s.By != "" {
			line += " · " + s.By
		}
	}
	center(clean(line, 60), "F1", 7.5, 19, false)
	if len(s.Hash) >= 16 {
		h := "SHA-256 " + s.Hash[0:4] + " " + s.Hash[4:8] + " " + s.Hash[8:12] + " " + s.Hash[12:16] + " …"
		fmt.Fprintf(&c, "BT /F3 5.6 Tf %.2f 9.5 Td (%s) Tj ET\n", (stampW-float64(len(h))*0.6*5.6)/2, winAnsi(h))
	}
	c.WriteString("Q\n")

	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.2f %.2f] /Rotate %d /Resources << /Font << /F1 5 0 R /F2 6 0 R /F3 7 0 R >> >> /Contents 4 0 R >>", w, h, rot),
		fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", c.Len(), c.String()),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding /WinAnsiEncoding >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Courier /Encoding /WinAnsiEncoding >>",
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	offs := make([]int, len(objs))
	for i, o := range objs {
		offs[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, o := range offs {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return b.Bytes()
}

// burnStamp lays the stamp over one page of src and writes dst.
func burnStamp(ctx context.Context, src, dst string, page int, s stampSpec) error {
	pw, ph, rot, err := pageBox(ctx, src, page)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "stamp")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	sp := filepath.Join(dir, "stamp.pdf")
	if err := os.WriteFile(sp, stampPDF(pw, ph, rot, s), 0o600); err != nil {
		return err
	}
	// qpdf places the overlay as the page is shown, rotation included
	_, err = run(ctx, 2*time.Minute, "qpdf", src, "--overlay", sp, "--to="+strconv.Itoa(page), "--from=1", "--", dst)
	if err != nil {
		// qpdf exits with 3 for warnings but still writes the file
		if st, e := os.Stat(dst); e == nil && st.Size() > 0 {
			return nil
		}
	}
	return err
}

// --- the API ---

type stampRow struct {
	ID    int64   `json:"id"`
	Kind  string  `json:"kind"`
	Label string  `json:"label"`
	Date  string  `json:"date"`
	Note  string  `json:"note"`
	Page  int     `json:"page"`
	By    string  `json:"by"`
	Hash  string  `json:"hash"`
	At    string  `json:"created_at"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
}

func (a *App) docStamps(id int64) []stampRow {
	out := []stampRow{}
	rows, err := a.db.Query(`SELECT s.id, s.kind, s.label, s.date, s.note, s.page, COALESCE(NULLIF(u.name,''), u.username, ''), s.hash, s.created_at, s.x, s.y
		FROM stamps s LEFT JOIN users u ON u.id = s.user_id WHERE s.doc_id = ? ORDER BY s.id`, id)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var s stampRow
		rows.Scan(&s.ID, &s.Kind, &s.Label, &s.Date, &s.Note, &s.Page, &s.By, &s.Hash, &s.At, &s.X, &s.Y)
		out = append(out, s)
	}
	return out
}

var stampColors = map[string][3]float64{
	"in":   {0.12, 0.31, 0.62}, // ink blue
	"paid": {0.70, 0.15, 0.12}, // ink red
}

// handlePageSize: a page's size as shown, for placing a stamp.
func (a *App) handlePageSize(w http.ResponseWriter, r *http.Request) {
	d, ok := a.loadDoc(w, r, false)
	if !ok {
		return
	}
	n, _ := strconv.Atoi(r.PathValue("n"))
	src, err := a.docFile(d)
	if err != nil || n < 1 {
		fail(w, 404, "No such page.")
		return
	}
	pw, ph, _, err := pageBox(r.Context(), src, n)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	out := map[string]any{"w": pw, "h": ph, "stamp_w": stampWidth, "stamp_ratio": stampH / stampW}
	if x, y, ok := a.stampSpot(r.Context(), d, src, n, pw, ph); ok {
		out["x"], out["y"] = x, y
	}
	writeJSON(w, 200, out)
}

// stampSpot: a free spot for the next stamp, away from the text and the
// stamps already on the page.
func (a *App) stampSpot(ctx context.Context, d *Doc, src string, page int, pw, ph float64) (float64, float64, bool) {
	b, err := run(ctx, time.Minute, "pdftotext", "-bbox", "-f", strconv.Itoa(page), "-l", strconv.Itoa(page), src, "-")
	if err != nil {
		return 0, 0, false
	}
	words := parseBBox(string(b), pw, ph)
	sh := stampWidth * (stampH / stampW) * pw / ph
	for _, s := range a.docStamps(d.ID) {
		if s.Page == page && (s.X > 0 || s.Y > 0) {
			words = append(words, pageWord{X: s.X, Y: s.Y, W: stampWidth, H: sh})
		}
	}
	x, y := freeSpot(words, stampWidth, sh)
	return x, y, true
}

// freeSpot picks the first corner spot that no word (or stamp) overlaps.
func freeSpot(taken []pageWord, sw, sh float64) (float64, float64) {
	const m, pad = 0.035, 0.01
	spots := [][2]float64{{1 - sw - m, m}, {1 - sw - m, 0.12}, {1 - sw - m, 0.22}, {m, m},
		{1 - sw - m, 1 - sh - m}, {m, 1 - sh - m}, {(1 - sw) / 2, 1 - sh - m}}
	for _, p := range spots {
		free := true
		for _, w := range taken {
			if w.X < p[0]+sw+pad && w.X+w.W > p[0]-pad && w.Y < p[1]+sh+pad && w.Y+w.H > p[1]-pad {
				free = false
				break
			}
		}
		if free {
			return p[0], p[1]
		}
	}
	return spots[0][0], spots[0][1]
}

func (a *App) beforeStamps(id int64) string {
	return filepath.Join(a.cfg.DataDir, "originals", fmt.Sprintf("doc-%d-before-stamps.pdf", id))
}

// replaceDocFile puts a changed PDF in place of a document's file and
// refreshes what Shoebox knows about it (size, hash, text, previews).
func (a *App) replaceDocFile(ctx context.Context, d *Doc, newPDF string) error {
	dst, err := a.docFile(d)
	if err != nil {
		return err
	}
	archiveMu.Lock()
	err = streamCopy(newPDF, dst)
	archiveMu.Unlock()
	if err != nil {
		return err
	}
	fsha, size, _ := fileSHA(dst)
	var mtime int64
	if st, err := os.Stat(dst); err == nil {
		mtime = st.ModTime().UnixNano()
	}
	text := pdfText(ctx, dst)
	a.db.Exec(`UPDATE docs SET pages = ?, size = ?, mtime = ?, file_sha = ?, updated_at = ? WHERE id = ?`, pdfPages(ctx, dst), size, mtime, fsha, now(), d.ID)
	reindex(a.db, d.ID, &text)
	a.dropCache(d.ID)
	return nil
}

func (a *App) handleDocStamp(w http.ResponseWriter, r *http.Request) {
	d, ok := a.loadDoc(w, r, true)
	if !ok {
		return
	}
	var in struct {
		Kind string  `json:"kind"` // in | paid
		Page int     `json:"page"`
		X    float64 `json:"x"`
		Y    float64 `json:"y"`
		Date string  `json:"date"` // YYYY-MM-DD, default today
		Note string  `json:"note"`
		Lang string  `json:"lang"` // de | en, default from the settings
		Auto bool    `json:"auto"` // not moved by hand: find the free spot now
	}
	if !readJSON(w, r, &in) {
		return
	}
	col, known := stampColors[in.Kind]
	if !known {
		fail(w, 400, "Unknown stamp.")
		return
	}
	if in.Page < 1 || (d.Pages > 0 && in.Page > d.Pages) {
		in.Page = 1
	}
	in.X = math.Min(math.Max(in.X, 0), 1-stampWidth)
	in.Y = math.Min(math.Max(in.Y, 0), 0.95)
	if in.Date == "" {
		in.Date = time.Now().Format("2006-01-02")
	}
	day, err := time.Parse("2006-01-02", in.Date)
	if err != nil {
		fail(w, 400, "Unknown date.")
		return
	}
	s := a.settings()
	lang := in.Lang
	if lang != "en" && lang != "de" {
		lang = s.StampLang
	}
	label, dateFmt := stampLabel(s, lang, in.Kind)
	src, err := a.docFile(d)
	if err != nil {
		fail(w, 404, "The file is not reachable.")
		return
	}
	if in.Auto {
		if pw, ph, _, err := pageBox(r.Context(), src, in.Page); err == nil {
			if x, y, ok := a.stampSpot(r.Context(), d, src, in.Page, pw, ph); ok {
				in.X, in.Y = x, y
			}
		}
	}
	hash := fileSum(src)
	// keep the document as it was before its first stamp
	if keep := a.beforeStamps(d.ID); !fileExists(keep) {
		if err := streamCopy(src, keep); err != nil {
			fail(w, 500, "Couldn't keep a copy before stamping: "+err.Error())
			return
		}
	}
	u := userFrom(r)
	tmp, err := os.MkdirTemp("", "stamped")
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	defer os.RemoveAll(tmp)
	out := filepath.Join(tmp, "out.pdf")
	spec := stampSpec{Label: label, Date: day.Format(dateFmt), Note: clean(in.Note, 40), By: u.Name, Hash: hash, Color: col, X: in.X, Y: in.Y}
	if err := burnStamp(r.Context(), src, out, in.Page, spec); err != nil {
		fail(w, 500, "Stamping failed: "+err.Error())
		return
	}
	if err := a.replaceDocFile(r.Context(), d, out); err != nil {
		fail(w, 500, "Couldn't save the stamped document: "+err.Error())
		return
	}
	a.db.Exec(`INSERT INTO stamps(doc_id, kind, label, date, note, page, user_id, hash, created_at, x, y) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		d.ID, in.Kind, label, in.Date, spec.Note, in.Page, nullID(u.ID), hash, now(), in.X, in.Y)
	// the date shows with the document's facts, e.g. "Bezahlt: 27.10.2026"
	if d.Fields == nil {
		d.Fields = map[string]string{}
	}
	d.Fields[label] = spec.Date
	fb, _ := json.Marshal(d.Fields)
	a.db.Exec(`UPDATE docs SET fields = ? WHERE id = ?`, string(fb), d.ID)
	nd, _ := a.getDoc(d.ID)
	writeJSON(w, 200, map[string]any{"doc": nd, "stamps": a.docStamps(d.ID)})
}

// handleDocUnstamp brings back the document as it was before its first stamp.
func (a *App) handleDocUnstamp(w http.ResponseWriter, r *http.Request) {
	d, ok := a.loadDoc(w, r, true)
	if !ok {
		return
	}
	keep := a.beforeStamps(d.ID)
	if !fileExists(keep) {
		fail(w, 404, "There is no version from before the stamps.")
		return
	}
	if err := a.replaceDocFile(r.Context(), d, keep); err != nil {
		fail(w, 500, err.Error())
		return
	}
	for _, s := range a.docStamps(d.ID) {
		delete(d.Fields, s.Label)
	}
	fb, _ := json.Marshal(d.Fields)
	a.db.Exec(`UPDATE docs SET fields = ? WHERE id = ?`, string(fb), d.ID)
	a.db.Exec(`DELETE FROM stamps WHERE doc_id = ?`, d.ID)
	os.Remove(keep)
	nd, _ := a.getDoc(d.ID)
	writeJSON(w, 200, map[string]any{"doc": nd, "stamps": []stampRow{}})
}

// stampLabel: the label of a stamp in a language, and how it prints dates.
func stampLabel(s Settings, lang, kind string) (string, string) {
	if lang == "en" {
		l := s.StampInEn
		if kind == "paid" {
			l = s.StampPaidEn
		}
		if l = clean(l, 24); l == "" {
			l = map[string]string{"in": "Received", "paid": "Paid"}[kind]
		}
		return l, "2 Jan 2006"
	}
	l := s.StampIn
	if kind == "paid" {
		l = s.StampPaid
	}
	if l = clean(l, 24); l == "" {
		l = map[string]string{"in": "Eingang", "paid": "Bezahlt"}[kind]
	}
	return l, "02.01.2006"
}

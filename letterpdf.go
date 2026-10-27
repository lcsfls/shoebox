package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png"
	"strings"
	"time"
)

// Letters after DIN 5008 (form A or B), set as a PDF with Helvetica: the
// sender's letterhead, the return address and the address field in the
// window of a DL envelope, the information block, subject, text, closing,
// enclosures, a footer, fold marks and a punch mark. Long letters run on
// to more pages, numbered "Seite 1 von 2".

const mm = 72 / 25.4 // points per millimetre

// Letterhead: who writes, per library.
type Letterhead struct {
	Name     string `json:"name"`    // e.g. "Anna Beispiel" or a company
	Address  string `json:"address"` // street, ZIP and town, one per line
	Phone    string `json:"phone"`
	Email    string `json:"email"`
	Web      string `json:"web"`
	Footer   string `json:"footer"`   // up to three columns, separated by a blank line (bank, tax number …)
	Signer   string `json:"signer"`   // name under the signature, default Name
	HasLogo  bool   `json:"has_logo"` // a logo was uploaded
	logoJPEG []byte
	logoW    int
	logoH    int
}

type Letter struct {
	ID         int64  `json:"id"`
	Status     string `json:"status"`
	Form       string `json:"form"` // A | B
	Lang       string `json:"lang"` // de | en
	Recipient  string `json:"recipient"`
	YourRef    string `json:"your_ref"`
	OurRef     string `json:"our_ref"`
	Date       string `json:"date"` // YYYY-MM-DD
	Subject    string `json:"subject"`
	Salutation string `json:"salutation"`
	Body       string `json:"body"`
	Closing    string `json:"closing"`
	Signature  string `json:"signature"`
	Enclosures string `json:"enclosures"`
	DocID      int64  `json:"doc_id"`
	CreatedBy  string `json:"created_by"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

var letterWords = map[string]map[string]string{
	"de": {"your": "Ihr Zeichen", "our": "Unser Zeichen", "phone": "Telefon", "email": "E-Mail", "date": "Datum",
		"page": "Seite %d von %d", "encl": "Anlagen", "encl1": "Anlage"},
	"en": {"your": "Your reference", "our": "Our reference", "phone": "Phone", "email": "Email", "date": "Date",
		"page": "Page %d of %d", "encl": "Enclosures", "encl1": "Enclosure"},
}

var monthsDE = []string{"Januar", "Februar", "März", "April", "Mai", "Juni", "Juli", "August", "September", "Oktober", "November", "Dezember"}

func letterDate(iso, lang string) string {
	t, err := time.Parse("2006-01-02", iso)
	if err != nil {
		return iso
	}
	if lang == "en" {
		return t.Format("2 January 2006")
	}
	return fmt.Sprintf("%d. %s %d", t.Day(), monthsDE[t.Month()-1], t.Year())
}

// ansi maps a character to its WinAnsiEncoding byte (for widths).
func ansi(r rune) (byte, bool) {
	if r >= 32 && r < 127 || r >= 0xA0 && r <= 0xFF {
		return byte(r), true
	}
	for b, x := range map[byte]rune{0x80: '€', 0x82: '‚', 0x84: '„', 0x85: '…', 0x91: '‘', 0x92: '’', 0x93: '“', 0x94: '”', 0x96: '–', 0x97: '—', 0x8A: 'Š', 0x9A: 'š', 0x8E: 'Ž', 0x9E: 'ž', 0x9C: 'œ', 0x8C: 'Œ'} {
		if x == r {
			return b, true
		}
	}
	return '?', false
}

// textW is the width of a text in points.
func textW(s string, size float64, bold bool) float64 {
	tbl := &helveticaWidths
	if bold {
		tbl = &helveticaBoldWidths
	}
	n := 0
	for _, r := range s {
		b, _ := ansi(r)
		n += int(tbl[b])
	}
	return float64(n) * size / 1000
}

// wrap breaks a paragraph into lines no wider than w points.
func wrap(par string, size, w float64, bold bool) []string {
	words := strings.Fields(par)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	cur := ""
	for _, word := range words {
		try := word
		if cur != "" {
			try = cur + " " + word
		}
		if textW(try, size, bold) <= w || cur == "" {
			// a single word wider than the line is cut
			for cur == "" && textW(try, size, bold) > w && len([]rune(try)) > 1 {
				r := []rune(try)
				k := len(r)
				for k > 1 && textW(string(r[:k]), size, bold) > w {
					k--
				}
				lines = append(lines, string(r[:k]))
				try = string(r[k:])
			}
			cur = try
			continue
		}
		lines = append(lines, cur)
		cur = word
	}
	return append(lines, cur)
}

// a piece of text placed on a page (y from the top, in points)
type textRun struct {
	x, y  float64
	size  float64
	bold  bool
	gray  bool
	right bool // x is the right edge
	text  string
}

type letterPage struct {
	runs  []textRun
	lines [][4]float64 // x1, y1, x2, y2 from the top
}

// layoutLetter places everything; it returns the pages (numbered later).
func layoutLetter(lh Letterhead, l Letter) []*letterPage {
	words := letterWords["de"]
	if l.Lang == "en" {
		words = letterWords["en"]
	}
	formA := l.Form == "A"
	const left, right = 25 * mm, 190 * mm
	width := right - left
	body := 11.0
	lead := 13.6 // points between lines of text
	page := &letterPage{}
	pages := []*letterPage{page}

	// letterhead: name, then the address and contact in a small line
	head := 18 * mm
	if formA {
		head = 13 * mm
	}
	if lh.Name != "" {
		page.runs = append(page.runs, textRun{x: left, y: head, size: 15, bold: true, text: lh.Name})
	}
	var info []string
	for _, s := range strings.Split(lh.Address, "\n") {
		if s = strings.TrimSpace(s); s != "" {
			info = append(info, s)
		}
	}
	contact := []string{}
	if lh.Phone != "" {
		contact = append(contact, lh.Phone)
	}
	if lh.Email != "" {
		contact = append(contact, lh.Email)
	}
	if lh.Web != "" {
		contact = append(contact, lh.Web)
	}
	small := strings.Join(append(append([]string{}, info...), contact...), "  ·  ")
	for i, ln := range wrap(small, 8.5, width-(70*mm)*boolf(lh.HasLogo), false) {
		page.runs = append(page.runs, textRun{x: left, y: head + 6*mm + float64(i)*10.5, size: 8.5, gray: true, text: ln})
	}

	// the address field: return address, then the recipient
	field := 45 * mm
	zone := 17.7 * mm
	if formA {
		field, zone = 27*mm, 12.7*mm
	}
	if lh.Name != "" || len(info) > 0 {
		ret := strings.Join(append([]string{lh.Name}, info...), " · ")
		for textW(ret, 7, false) > 80*mm && len(ret) > 10 {
			ret = string([]rune(ret)[:len([]rune(ret))-2])
		}
		page.runs = append(page.runs, textRun{x: left, y: field + zone - 2.2*mm, size: 7, gray: true, text: ret})
		page.lines = append(page.lines, [4]float64{left, field + zone - 1.4*mm, left + textW(ret, 7, false), field + zone - 1.4*mm})
	}
	ry := field + zone + 4.2*mm
	n := 0
	for _, s := range strings.Split(l.Recipient, "\n") {
		if s = strings.TrimSpace(s); s == "" || n >= 6 {
			continue
		}
		page.runs = append(page.runs, textRun{x: left, y: ry, size: 10.5, text: s})
		ry += 4.23 * mm
		n++
	}

	// the information block on the right
	iy := 50 * mm
	if formA {
		iy = 32 * mm
	}
	ix := 125 * mm
	row := func(label, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		page.runs = append(page.runs, textRun{x: ix, y: iy, size: 8, gray: true, text: label})
		page.runs = append(page.runs, textRun{x: ix + 27*mm, y: iy, size: 9.5, text: value})
		iy += 4.6 * mm
	}
	row(words["your"], l.YourRef)
	row(words["our"], l.OurRef)
	row(words["phone"], lh.Phone)
	row(words["email"], lh.Email)
	row(words["date"], letterDate(l.Date, l.Lang))

	// the text
	y := 98.46 * mm
	if formA {
		y = 80.46 * mm
	}
	bottom := 262 * mm
	newPage := func() {
		page = &letterPage{}
		pages = append(pages, page)
		y = 25 * mm
	}
	put := func(text string, size float64, bold bool) {
		if y > bottom {
			newPage()
		}
		page.runs = append(page.runs, textRun{x: left, y: y, size: size, bold: bold, text: text})
		y += lead
	}
	blank := func() { y += lead }
	if s := strings.TrimSpace(l.Subject); s != "" {
		for _, ln := range wrap(s, body, width, true) {
			put(ln, body, true)
		}
		blank()
	}
	if s := strings.TrimSpace(l.Salutation); s != "" {
		put(s, body, false)
		blank()
	}
	pars := strings.Split(strings.ReplaceAll(strings.TrimSpace(l.Body), "\r", ""), "\n")
	for i, p := range pars {
		if strings.TrimSpace(p) == "" {
			if i > 0 && strings.TrimSpace(pars[i-1]) != "" {
				blank()
			}
			continue
		}
		indent := 0.0
		bullet := ""
		if t := strings.TrimSpace(p); strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "• ") {
			bullet, p, indent = "–", t[2:], 5*mm
		}
		for k, ln := range wrap(p, body, width-indent, false) {
			if y > bottom {
				newPage()
			}
			if bullet != "" && k == 0 {
				page.runs = append(page.runs, textRun{x: left, y: y, size: body, text: bullet})
			}
			page.runs = append(page.runs, textRun{x: left + indent, y: y, size: body, text: ln})
			y += lead
		}
	}
	// closing and signature stay together
	if s := strings.TrimSpace(l.Closing); s != "" || strings.TrimSpace(l.Signature) != "" {
		if y+lead*5 > bottom {
			newPage()
		} else {
			blank()
		}
		if s != "" {
			put(s, body, false)
		}
		y += lead * 3
		if sig := strings.TrimSpace(l.Signature); sig != "" {
			put(sig, body, false)
		}
	}
	if e := strings.TrimSpace(l.Enclosures); e != "" {
		items := []string{}
		for _, s := range strings.Split(e, "\n") {
			if s = strings.TrimSpace(s); s != "" {
				items = append(items, s)
			}
		}
		if y+lead*float64(len(items)+2) > bottom {
			newPage()
		} else {
			blank()
		}
		label := words["encl"]
		if len(items) == 1 {
			label = words["encl1"]
		}
		put(label, body, true)
		for _, it := range items {
			put(it, body, false)
		}
	}
	return pages
}

func boolf(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// renderLetter makes the PDF.
func renderLetter(lh Letterhead, l Letter) []byte {
	pages := layoutLetter(lh, l)
	words := letterWords["de"]
	if l.Lang == "en" {
		words = letterWords["en"]
	}
	const W, H = 210 * mm, 297 * mm
	formA := l.Form == "A"
	cols := []string{}
	for _, c := range strings.Split(strings.ReplaceAll(strings.TrimSpace(lh.Footer), "\r", ""), "\n\n") {
		if c = strings.TrimSpace(c); c != "" && len(cols) < 3 {
			cols = append(cols, c)
		}
	}

	var objs []string
	add := func(o string) int { objs = append(objs, o); return len(objs) }
	add("<< /Type /Catalog /Pages 2 0 R >>")
	add("") // pages, filled in at the end
	f1 := add("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>")
	f2 := add("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding /WinAnsiEncoding >>")
	logo := 0
	if lh.HasLogo && len(lh.logoJPEG) > 0 {
		logo = add(fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>\nstream\n%s\nendstream",
			lh.logoW, lh.logoH, len(lh.logoJPEG), lh.logoJPEG))
	}
	var kids []string
	for i, p := range pages {
		var c bytes.Buffer
		line := func(x1, y1, x2, y2, w, g float64) {
			fmt.Fprintf(&c, "%.2f G %.2f w %.2f %.2f m %.2f %.2f l S\n", g, w, x1, H-y1, x2, H-y2)
		}
		text := func(r textRun) {
			font := "F1"
			if r.bold {
				font = "F2"
			}
			x := r.x
			if r.right {
				x -= textW(r.text, r.size, r.bold)
			}
			g := 0.0
			if r.gray {
				g = 0.35
			}
			fmt.Fprintf(&c, "%.2f g BT /%s %.1f Tf %.2f %.2f Td (%s) Tj ET\n", g, font, r.size, x, H-r.y, winAnsi(r.text))
		}
		// fold marks and the punch mark (page 1)
		if i == 0 {
			f1y, f2y := 105.0, 210.0
			if formA {
				f1y, f2y = 87, 192
			}
			line(3*mm, f1y*mm, 8*mm, f1y*mm, 0.4, 0.55)
			line(3*mm, f2y*mm, 8*mm, f2y*mm, 0.4, 0.55)
			line(3*mm, 148.5*mm, 10*mm, 148.5*mm, 0.4, 0.55)
			if logo > 0 {
				// top right, at most 60 × 25 mm
				maxW, maxH := 60*mm, 25*mm
				s := min(maxW/float64(lh.logoW), maxH/float64(lh.logoH))
				w, h := float64(lh.logoW)*s, float64(lh.logoH)*s
				top := 12 * mm
				if formA {
					top = 8 * mm
				}
				fmt.Fprintf(&c, "q %.2f 0 0 %.2f %.2f %.2f cm /Im1 Do Q\n", w, h, 190*mm-w, H-top-h)
			}
		}
		for _, l := range p.lines {
			line(l[0], l[1], l[2], l[3], 0.3, 0.45)
		}
		for _, r := range p.runs {
			text(r)
		}
		if len(pages) > 1 {
			text(textRun{x: 190 * mm, y: 270 * mm, size: 8, gray: true, right: true, text: fmt.Sprintf(words["page"], i+1, len(pages))})
		}
		// footer
		if len(cols) > 0 {
			line(25*mm, 276*mm, 190*mm, 276*mm, 0.3, 0.7)
			cw := 165 * mm / float64(len(cols))
			for k, col := range cols {
				for j, ln := range strings.Split(col, "\n") {
					if j >= 5 {
						break
					}
					text(textRun{x: 25*mm + float64(k)*cw, y: 280*mm + float64(j)*9.5, size: 7.5, gray: true, text: strings.TrimSpace(ln)})
				}
			}
		}
		res := fmt.Sprintf("/Font << /F1 %d 0 R /F2 %d 0 R >>", f1, f2)
		if logo > 0 && i == 0 {
			res += fmt.Sprintf(" /XObject << /Im1 %d 0 R >>", logo)
		}
		content := add(fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", c.Len(), c.String()))
		pg := add(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.2f %.2f] /Resources << %s >> /Contents %d 0 R >>", W, H, res, content))
		kids = append(kids, fmt.Sprintf("%d 0 R", pg))
	}
	objs[1] = fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(kids))
	info := add(fmt.Sprintf("<< /Title (%s) /Author (%s) /Producer (Shoebox) /CreationDate (D:%s) >>",
		winAnsi(l.Subject), winAnsi(lh.Name), time.Now().UTC().Format("20060102150405Z")))

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
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R /Info %d 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, info, xref)
	return b.Bytes()
}

// logoToJPEG turns an uploaded PNG or JPEG into a JPEG on white (PDFs
// embed JPEG as is; transparency becomes white paper).
func logoToJPEG(data []byte) ([]byte, int, int, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("not a PNG or JPEG picture")
	}
	b := img.Bounds()
	if b.Dx() > 2000 || b.Dy() > 2000 {
		return nil, 0, 0, fmt.Errorf("the logo is too large (at most 2000 × 2000 pixels)")
	}
	rgba := image.NewRGBA(b)
	draw.Draw(rgba, b, &image.Uniform{color.White}, image.Point{}, draw.Src)
	draw.Draw(rgba, b, img, b.Min, draw.Over)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, rgba, &jpeg.Options{Quality: 92}); err != nil {
		return nil, 0, 0, err
	}
	return out.Bytes(), b.Dx(), b.Dy(), nil
}

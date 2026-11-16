package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// External programs Shoebox uses. All are standard packages on Debian/Ubuntu/Alpine:
//
//	tesseract-ocr (+ language data), poppler-utils, qpdf, libimage-exiftool-perl
var tools = []string{"tesseract", "pdftoppm", "pdftotext", "pdfinfo", "qpdf", "exiftool"}

func checkTools() {
	for _, t := range tools {
		if _, err := exec.LookPath(t); err != nil {
			log.Printf("warning: %s not found; install it, or some features won't work", t)
		}
	}
}

func run(ctx context.Context, timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if len(msg) > 400 {
			msg = msg[:400]
		}
		return out.Bytes(), fmt.Errorf("%s: %v %s", name, err, msg)
	}
	return out.Bytes(), nil
}

// pdfText extracts the text layer of a PDF.
func pdfText(ctx context.Context, path string) string {
	out, err := run(ctx, 2*time.Minute, "pdftotext", "-layout", "-enc", "UTF-8", path, "-")
	if err != nil {
		return ""
	}
	return string(out)
}

func hasText(text string) bool {
	n := 0
	for _, r := range text {
		if r > ' ' {
			n++
			if n > 40 {
				return true
			}
		}
	}
	return false
}

var pagesRe = regexp.MustCompile(`(?m)^Pages:\s+(\d+)`)

func pdfPages(ctx context.Context, path string) int {
	out, err := run(ctx, time.Minute, "pdfinfo", path)
	if err != nil {
		return 0
	}
	if m := pagesRe.FindSubmatch(out); m != nil {
		n, _ := strconv.Atoi(string(m[1]))
		return n
	}
	return 0
}

// ocrImages turns page images into one searchable PDF (text layer from Tesseract).
func ocrImages(ctx context.Context, images []string, langs, out string) (string, error) {
	dir := filepath.Dir(out)
	var pdfs []string
	var text strings.Builder
	for i, img := range images {
		base := filepath.Join(dir, fmt.Sprintf("ocr-%03d", i))
		if _, err := run(ctx, 5*time.Minute, "tesseract", img, base, "-l", langs, "--psm", "1", "pdf", "txt"); err != nil {
			return "", err
		}
		pdfs = append(pdfs, base+".pdf")
		if b, err := os.ReadFile(base + ".txt"); err == nil {
			text.Write(b)
			text.WriteString("\n\f")
		}
	}
	if err := mergePDFs(ctx, pdfs, out); err != nil {
		return "", err
	}
	return text.String(), nil
}

func mergePDFs(ctx context.Context, in []string, out string) error {
	if len(in) == 1 {
		return os.Rename(in[0], out)
	}
	args := append([]string{"--empty", "--pages"}, in...)
	args = append(args, "--", out)
	_, err := run(ctx, 5*time.Minute, "qpdf", args...)
	return err
}

// rasterize renders PDF pages as images (for OCR or the AI model).
func rasterize(ctx context.Context, pdf, dir string, dpi int, format string, first, last int) ([]string, error) {
	args := []string{"-r", strconv.Itoa(dpi)}
	if format == "jpeg" {
		args = append(args, "-jpeg", "-jpegopt", "quality=85")
	} else {
		args = append(args, "-png")
	}
	if first > 0 {
		args = append(args, "-f", strconv.Itoa(first))
	}
	if last > 0 {
		args = append(args, "-l", strconv.Itoa(last))
	}
	prefix := filepath.Join(dir, "page")
	if _, err := run(ctx, 10*time.Minute, "pdftoppm", append(args, pdf, prefix)...); err != nil {
		return nil, err
	}
	files, _ := filepath.Glob(prefix + "-*")
	sort.Strings(files)
	return files, nil
}

// ocrPDF adds a text layer to a scanned PDF without one.
func ocrPDF(ctx context.Context, in, out, langs string) (string, error) {
	dir, err := os.MkdirTemp("", "ocrpdf")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	imgs, err := rasterize(ctx, in, dir, 300, "png", 0, 0)
	if err != nil {
		return "", err
	}
	if len(imgs) == 0 {
		return "", fmt.Errorf("no pages")
	}
	tmp := filepath.Join(dir, "out.pdf")
	text, err := ocrImages(ctx, imgs, langs, tmp)
	if err != nil {
		return "", err
	}
	return text, moveFile(tmp, out)
}

func tesseractLanguages() []string {
	out, err := run(context.Background(), 10*time.Second, "tesseract", "--list-langs")
	if err != nil {
		return nil
	}
	var langs []string
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !strings.Contains(l, " ") && l != "osd" {
			langs = append(langs, l)
		}
	}
	return langs
}

// writeMeta stores title, sender, tags and date in the PDF itself (Info and XMP),
// so the archive folder describes itself and the index can be rebuilt from it.
func writeMeta(ctx context.Context, path string, d *Doc) error {
	if _, err := exec.LookPath("exiftool"); err != nil {
		return nil
	}
	args := []string{"-q", "-q", "-overwrite_original", "-m",
		"-PDF:Title=" + d.Title, "-XMP-dc:Title=" + d.Title,
		"-PDF:Author=" + d.Sender, "-XMP-dc:Creator=" + d.Sender,
		"-PDF:Keywords=" + strings.Join(d.Tags, ", "), "-XMP-dc:Subject="}
	for _, t := range d.Tags {
		args = append(args, "-XMP-dc:Subject+="+t)
	}
	if d.Date != "" {
		args = append(args, "-XMP-dc:Date="+d.Date)
	}
	args = append(args, "-XMP-pdf:Producer=Shoebox", path)
	_, err := run(ctx, time.Minute, "exiftool", args...)
	return err
}

type fileMeta struct {
	Title, Author, Date string
	Tags                []string
}

// readMeta reads what writeMeta wrote (used for files that show up in the archive).
func readMeta(ctx context.Context, path string) fileMeta {
	var m fileMeta
	out, err := run(ctx, time.Minute, "exiftool", "-s", "-s", "-s", "-f", "-PDF:Title", "-PDF:Author", "-XMP-dc:Date", "-PDF:Keywords", "-XMP-pdf:Producer", path)
	if err != nil {
		return m
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	get := func(i int) string {
		if i < len(lines) && strings.TrimSpace(lines[i]) != "-" {
			return strings.TrimSpace(lines[i])
		}
		return ""
	}
	if get(4) != "Shoebox" {
		// Only trust metadata Shoebox wrote; other producers fill these fields with junk.
		return fileMeta{Title: get(0)}
	}
	m.Title, m.Author = get(0), get(1)
	if d := get(2); len(d) >= 10 {
		m.Date = strings.ReplaceAll(d[:10], ":", "-")
	}
	for _, t := range strings.Split(get(3), ",") {
		if t = strings.TrimSpace(t); t != "" {
			m.Tags = append(m.Tags, t)
		}
	}
	return m
}

// moveFile renames, or copies across file systems (local disk → NFS).
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := copyFile(src, dst); err != nil {
		return err
	}
	return os.Remove(src)
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	tmp := dst + ".part"
	if err := os.WriteFile(tmp, b, 0o640); err != nil {
		return err
	}
	f, err := os.Open(tmp)
	if err == nil {
		f.Sync()
		f.Close()
	}
	return os.Rename(tmp, dst)
}

// imagesToPDF puts photographed pages into a PDF as they are, without
// recognizing text (the AI model reads them): JPEGs go in untouched, other
// pictures as JPEG. Each page is A4 wide (A4 high when it lies on its side).
func imagesToPDF(images []string, out string) error {
	type page struct {
		jpg  []byte
		w, h int
		gray bool
	}
	var pages []page
	for _, p := range images {
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
		if err != nil {
			return fmt.Errorf("%s: %w", filepath.Base(p), err)
		}
		gray := cfg.ColorModel == color.GrayModel
		if format != "jpeg" || cfg.ColorModel == color.CMYKModel {
			img, _, err := image.Decode(bytes.NewReader(raw))
			if err != nil {
				return err
			}
			var b bytes.Buffer
			if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 90}); err != nil {
				return err
			}
			raw = b.Bytes()
			_, gray = img.(*image.Gray)
		}
		pages = append(pages, page{raw, cfg.Width, cfg.Height, gray})
	}
	if len(pages) == 0 {
		return errors.New("no pages")
	}
	var objs []string
	add := func(o string) int { objs = append(objs, o); return len(objs) }
	add("<< /Type /Catalog /Pages 2 0 R >>")
	add("")
	var kids []string
	for i, pg := range pages {
		pw := 595.28 // A4 in points
		if pg.w > pg.h {
			pw = 841.89
		}
		ph := pw * float64(pg.h) / float64(pg.w)
		cs := "/DeviceRGB"
		if pg.gray {
			cs = "/DeviceGray"
		}
		im := add(fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace %s /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>\nstream\n%s\nendstream",
			pg.w, pg.h, cs, len(pg.jpg), pg.jpg))
		content := fmt.Sprintf("q %.2f 0 0 %.2f 0 0 cm /Im%d Do Q", pw, ph, i)
		c := add(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
		kids = append(kids, fmt.Sprintf("%d 0 R", add(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.2f %.2f] /Resources << /XObject << /Im%d %d 0 R >> >> /Contents %d 0 R >>", pw, ph, i, im, c))))
	}
	objs[1] = fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(kids))
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
	return os.WriteFile(out, b.Bytes(), 0o640)
}

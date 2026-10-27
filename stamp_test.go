package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Burns a stamp into an OCR'd letter, straight and turned by 90°, and
// checks that its words land where they were placed on the shown page.
func TestBurnStamp(t *testing.T) {
	for _, tool := range []string{"qpdf", "pdftotext", "pdfinfo", "tesseract"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " missing")
		}
	}
	dir := t.TempDir()
	ctx := context.Background()
	src := filepath.Join(dir, "letter.pdf")
	if _, err := ocrImages(ctx, []string{"testdata/letter.png"}, testLang(), src); err != nil {
		t.Fatal(err)
	}
	rot := filepath.Join(dir, "turned.pdf")
	if _, err := run(ctx, time.Minute, "qpdf", "--rotate=+90", src, rot); err != nil && !fileExists(rot) {
		t.Fatal(err)
	}
	for _, in := range []string{src, rot} {
		out := strings.TrimSuffix(in, ".pdf") + "-stamped.pdf"
		spec := stampSpec{Label: "Eingang", Date: "27.10.2026", Note: "per Post", By: "Anna", Hash: strings.Repeat("ab12", 16),
			Color: [3]float64{0.12, 0.31, 0.61}, X: 0.62, Y: 0.05}
		if err := burnStamp(ctx, in, out, 1, spec); err != nil {
			t.Fatal(err)
		}
		bbox, err := run(ctx, time.Minute, "pdftotext", "-bbox", "-f", "1", "-l", "1", out, "-")
		if err != nil {
			t.Fatal(err)
		}
		var hit *pageWord
		sw, sh, _, _ := pageBox(ctx, out, 1)
		words := parseBBox(string(bbox), sw, sh)
		for i := range words {
			if words[i].T == "EINGANG" {
				hit = &words[i]
			}
		}
		if hit == nil {
			t.Errorf("%s: the stamp label is not in the text", filepath.Base(in))
		}
		cx, cy := hit.X+hit.W/2, hit.Y+hit.H/2
		if cx < 0.62 || cx > 0.62+stampWidth || cy < 0.04 || cy > 0.25 {
			t.Errorf("%s: label at %.2f,%.2f, expected in the top right corner", filepath.Base(in), cx, cy)
		}
		// the letter's own text is still there
		if !strings.Contains(string(bbox), "Gesamtbetrag") && !strings.Contains(string(bbox), "Musterstadt") {
			t.Errorf("%s: the page's text is gone", filepath.Base(in))
		}
		if d := os.Getenv("SB_KEEP_STAMPED"); d != "" {
			os.WriteFile(filepath.Join(d, filepath.Base(out)), mustRead(out), 0o644)
		}
		os.Remove(out)
	}
}

func TestWinAnsi(t *testing.T) {
	if got := winAnsi("Überweisung (bar) 5€"); got != `\334berweisung \(bar\) 5\200` {
		t.Errorf("winAnsi = %q", got)
	}
}

func mustRead(p string) []byte { b, _ := os.ReadFile(p); return b }

func TestStampLabel(t *testing.T) {
	s := defaultSettings()
	if l, f := stampLabel(s, "en", "paid"); l != "Paid" || f != "2 Jan 2006" {
		t.Errorf("en: %q %q", l, f)
	}
	if l, _ := stampLabel(s, "de", "in"); l != "Eingang" {
		t.Errorf("de: %q", l)
	}
	s.StampPaidEn = ""
	if l, _ := stampLabel(s, "en", "paid"); l != "Paid" {
		t.Errorf("empty label: %q", l)
	}
}

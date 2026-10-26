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

func needTools(t *testing.T, names ...string) {
	for _, n := range names {
		if _, err := exec.LookPath(n); err != nil {
			t.Skipf("%s not installed", n)
		}
	}
}

func testLang() string {
	if l := os.Getenv("SB_TEST_LANG"); l != "" {
		return l
	}
	return "eng"
}

// The OCR pipeline: a photographed page becomes a one-page PDF with a text
// layer, and the facts can be read from it.
func TestOCRPipeline(t *testing.T) {
	needTools(t, "tesseract", "qpdf", "pdftotext", "pdfinfo", "pdftoppm")
	dir := t.TempDir()
	ctx := context.Background()
	img1 := filepath.Join(dir, "p1.png")
	img2 := filepath.Join(dir, "p2.png")
	for _, p := range []string{img1, img2} {
		b, err := os.ReadFile("testdata/letter.png")
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(p, b, 0o644)
	}
	out := filepath.Join(dir, "doc.pdf")
	text, err := ocrImages(ctx, []string{img1, img2}, testLang(), out)
	if err != nil {
		t.Fatal(err)
	}
	if n := pdfPages(ctx, out); n != 2 {
		t.Fatalf("pages = %d, want 2", n)
	}
	layer := pdfText(ctx, out)
	for _, want := range []string{"Stadtwerke", "Gesamtbetrag"} {
		if !strings.Contains(text, want) || !strings.Contains(layer, want) {
			t.Errorf("%q missing from OCR text or text layer", want)
		}
	}
	if d := findDate(layer, time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)); d != "2026-10-24" {
		t.Errorf("date = %q", d)
	}
	if a := findAmount(layer); a == nil || *a != 8420 {
		t.Errorf("amount = %v", a)
	}

	// a scanned PDF without text gets a text layer
	imgs, err := rasterize(ctx, out, dir, 100, "png", 1, 1)
	if err != nil || len(imgs) != 1 {
		t.Fatalf("rasterize: %v %v", imgs, err)
	}
	if hasText("   \n\f  ") {
		t.Error("whitespace counted as text")
	}
}

func TestMetadataRoundTrip(t *testing.T) {
	needTools(t, "tesseract", "qpdf", "exiftool", "pdftotext")
	dir := t.TempDir()
	ctx := context.Background()
	out := filepath.Join(dir, "doc.pdf")
	if _, err := ocrImages(ctx, []string{"testdata/letter.png"}, testLang(), out); err != nil {
		t.Fatal(err)
	}
	d := &Doc{Title: "Stromrechnung Oktober", Sender: "Stadtwerke", Date: "2026-10-24", Tags: []string{"Strom", "Rechnung"}}
	if err := writeMeta(ctx, out, d); err != nil {
		t.Fatal(err)
	}
	m := readMeta(ctx, out)
	if m.Title != d.Title || m.Author != d.Sender || m.Date != d.Date || strings.Join(m.Tags, ",") != "Strom,Rechnung" {
		t.Errorf("read back %+v", m)
	}
}

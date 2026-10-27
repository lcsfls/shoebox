package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func pdfWordsOf(t *testing.T, pdf []byte, page string) ([]pageWord, string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "l.pdf")
	os.WriteFile(p, pdf, 0o600)
	out, err := exec.Command("pdftotext", "-bbox", "-f", page, "-l", page, p, "-").Output()
	if err != nil {
		t.Fatalf("pdftotext: %v", err)
	}
	txt, _ := exec.Command("pdftotext", "-layout", p, "-").Output()
	return parseBBox(string(out), 0, 0), string(txt)
}

func findWord(ws []pageWord, s string) *pageWord {
	for i := range ws {
		if ws[i].T == s {
			return &ws[i]
		}
	}
	return nil
}

func TestLetterLayout(t *testing.T) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("no pdftotext")
	}
	lh := Letterhead{Name: "Beispiel Werkstatt", Address: "Am Markt 3\n12345 Musterstadt", Phone: "0123 456789",
		Footer: "Beispiel Werkstatt\nAm Markt 3\n\nBank: Musterbank\nIBAN DE00 0000"}
	body := strings.Repeat("Dies ist ein längerer Absatz, der über mehrere Zeilen umbrochen werden muss, damit die zweite Seite entsteht. ", 8)
	l := Letter{Form: "B", Lang: "de", Recipient: "Empfänger GmbH\nHauptstraße 1\n54321 Zielstadt", Date: "2026-03-04",
		Subject: "Kündigung Vertrag", Salutation: "Sehr geehrte Damen und Herren,",
		Body: strings.Repeat(body+"\n\n", 6), Closing: "Mit freundlichen Grüßen", Signature: "Erika Muster", OurRef: "AZ-17"}

	for _, form := range []string{"B", "A"} {
		l.Form = form
		ws, txt := pdfWordsOf(t, renderLetter(lh, l), "1")
		top, zone := 45.0, 17.7 // the address field from the top, in mm
		if form == "A" {
			top, zone = 27.0, 12.7
		}
		w := findWord(ws, "Empfänger")
		if w == nil {
			t.Fatalf("form %s: recipient missing:\n%s", form, txt)
		}
		x, y := w.X*210, w.Y*297
		if x < 24 || x > 30 || y < top+zone-1 || y > top+45 {
			t.Errorf("form %s: recipient at %.1f/%.1f mm, outside the address window", form, x, y)
		}
		if s := findWord(ws, "Kündigung"); s == nil || s.Y*297 < top+45 || s.Y*297 > top+62 {
			t.Errorf("form %s: subject misplaced: %+v", form, s)
		}
		if d := findWord(ws, "März"); d == nil || d.X*210 < 124 {
			t.Errorf("form %s: date not in the info block: %+v", form, d)
		}
		for _, want := range []string{"Seite 1 von", "Bank: Musterbank", "AZ-17", "Hauptstraße"} {
			if !strings.Contains(txt, want) {
				t.Errorf("form %s: %q missing", form, want)
			}
		}
		if !strings.Contains(txt, "Erika Muster") || !strings.Contains(txt, "Seite 2 von") {
			t.Errorf("form %s: expected a second page with the signature", form)
		}
	}

	l.Lang, l.Body, l.Form = "en", "Short.", "B"
	_, txt := pdfWordsOf(t, renderLetter(lh, l), "1")
	if !strings.Contains(txt, "4 March 2026") || strings.Contains(txt, "Seite") {
		t.Errorf("english letter:\n%s", txt)
	}
}

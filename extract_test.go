package main

import (
	"testing"
	"time"
)

var refNow = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

func TestFindDate(t *testing.T) {
	cases := []struct{ text, want string }{
		{"Stadtwerke\nKundennummer 12345\nRechnungsdatum: 24.10.2026\nLieferzeitraum 01.09.2026 - 30.09.2026", "2026-10-24"},
		{"Köln, den 3. März 2025\nSehr geehrte", "2025-03-03"},
		{"Invoice date October 5, 2026", "2026-10-05"},
		{"Bescheid vom 2024-11-30 über", "2024-11-30"},
		{"geboren 01.01.1960, Datum 14.02.26", "2026-02-14"},
		{"Telefon 0221 12.34.56", ""},
		{"Lieferung 31.02.2026 und 15.03.2026", "2026-03-15"},
		{"Termin am 01.01.2031", ""}, // too far in the future
		{"Bundesanzeiger\nVerlag\nTransparenzregister\nAmsterdamer Straße 192 · 50735 Köln\nTelefon (0800) 12 34-207\nDatum: 09. Februar 2026\nFrist bis 10.05.2034", "2026-02-09"},
		{"Datum:09.Februar 2026", "2026-02-09"},
	}
	for _, c := range cases {
		if got := findDate(c.text, refNow); got != c.want {
			t.Errorf("findDate(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}

func TestFindAmount(t *testing.T) {
	cases := []struct {
		text string
		want int64
	}{
		{"Arbeitspreis 12,50 €\nGrundpreis 9,90 €\nGesamtbetrag 84,20 €", 8420},
		{"Summe EUR 1.234,56", 123456},
		{"Posten 5,00 EUR\nPosten 120,00 EUR", 12000},
		{"Zu zahlen: 2 499,00 €", 249900},
	}
	for _, c := range cases {
		got := findAmount(c.text)
		if got == nil || *got != c.want {
			t.Errorf("findAmount(%q) = %v, want %d", c.text, got, c.want)
		}
	}
	if findAmount("keine Beträge hier, nur 12 Stück") != nil {
		t.Error("found an amount where there is none")
	}
}

func TestFTSQuery(t *testing.T) {
	cases := map[string]string{
		"strom":                   `"strom"*`,
		"strom 2026":              `"strom"* AND "2026"*`,
		`"neue Heizung" rechnung`: `"neue Heizung" AND "rechnung"*`,
		"a*b (c) -d":              `"a"* AND "b"* AND "c"* AND "d"*`,
		"   ":                     "",
	}
	for in, want := range cases {
		if got := ftsQuery(in); got != want {
			t.Errorf("ftsQuery(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestTargetPath(t *testing.T) {
	d := &Doc{ID: 7, Title: "Stromrechnung Oktober", Sender: "Stadtwerke Köln", Date: "2026-10-24", Tags: []string{"Strom"}}
	if got := targetPath("{year}/{month}/{date}_{sender}_{title}", d); got != "2026/10/2026-10-24_Stadtwerke-Köln_Stromrechnung-Oktober.pdf" {
		t.Errorf("got %s", got)
	}
	d2 := &Doc{ID: 8, Title: "a/b: c?", Date: "2025-01-02"}
	if got := targetPath("{year}/{month}/{date}_{sender}_{title}", d2); got != "2025/01/2025-01-02_a-b-c.pdf" {
		t.Errorf("got %s", got)
	}
	d3 := &Doc{ID: 9, Title: "x", Date: "2025-01-02"}
	if got := targetPath("../{year}/../{title}", d3); got != "2025/x.pdf" {
		t.Errorf("traversal not removed: %s", got)
	}
}

func TestRules(t *testing.T) {
	text := "STADTWERKE Musterstadt GmbH\nIhre Stromrechnung für Oktober\nVertragskonto 4711"
	cases := []struct {
		r    Rule
		want bool
	}{
		{Rule{Kind: "any", Pattern: "gas strom", NoCase: true}, false}, // whole words only: "Stromrechnung" is not "strom"
		{Rule{Kind: "any", Pattern: "gas stromrechnung", NoCase: true}, true},
		{Rule{Kind: "all", Pattern: "stadtwerke oktober", NoCase: true}, true},
		{Rule{Kind: "all", Pattern: "stadtwerke november", NoCase: true}, false},
		{Rule{Kind: "exact", Pattern: "Stadtwerke Musterstadt", NoCase: true}, true},
		{Rule{Kind: "exact", Pattern: "Stadtwerke Musterstadt", NoCase: false}, false},
		{Rule{Kind: "regex", Pattern: `Vertragskonto \d{4}`}, true},
		{Rule{Kind: "fuzzy", Pattern: "Stadtwcrke Musterstadt", NoCase: true}, true},
		{Rule{Kind: "fuzzy", Pattern: "Wasserwerke Beispielort", NoCase: true}, false},
	}
	for _, c := range cases {
		if got := c.r.matches(text); got != c.want {
			t.Errorf("%s %q = %v, want %v", c.r.Kind, c.r.Pattern, got, c.want)
		}
	}
}

func TestTitleFromFile(t *testing.T) {
	if got := titleFromFile("2026-01-15_Hausverwaltung_Nebenkosten-2025.pdf"); got != "Hausverwaltung – Nebenkosten 2025" {
		t.Errorf("got %q", got)
	}
	if got := titleFromFile("Scan0003.PDF"); got != "Scan0003" {
		t.Errorf("got %q", got)
	}
}

func TestYearMonthFromPath(t *testing.T) {
	for p, want := range map[string]string{
		"2026/01/rechnung.pdf":        "2026-01",
		"docs/2026/01/rechnung.pdf":   "2026-01",
		"2026-03 Steuer/bescheid.pdf": "2026-03",
		"2026/rechnung.pdf":           "",
	} {
		got := ""
		if m := ymPath.FindStringSubmatch(p); m != nil {
			got = m[1] + "-" + m[2]
		}
		if got != want {
			t.Errorf("%s: got %q want %q", p, got, want)
		}
	}
}

func TestGuessTitleAndSender(t *testing.T) {
	text := "Muster Versicherung AG\nIhre Rechnung Nr. 12\nMahnung folgt"
	if g := guessTitle(text); g != "Rechnung" {
		t.Errorf("guessTitle = %q", g)
	}
	if s := knownSender(text, []string{"AG", "Muster Versicherung", "Versicherung"}); s != "Muster Versicherung" {
		t.Errorf("knownSender = %q", s)
	}
}

func TestTitleChecks(t *testing.T) {
	for in, want := range map[string]bool{"2026-03-01": false, "Scan_0001": false, "IMG 2041": false, "Mietvertrag": true, "Kfz-Steuer 2026": true} {
		if got := hasLetters(in); got != want {
			t.Errorf("hasLetters(%q) = %v", in, got)
		}
	}
	if g := guessTitle("Firma\nBetreff: Mitteilung zum Transparenzregister\nSehr geehrte"); g != "Mitteilung zum Transparenzregister" {
		t.Errorf("subject title = %q", g)
	}
	if plausibleDate("2034-05-10", refNow) || !plausibleDate("2026-02-09", refNow) {
		t.Error("plausibleDate")
	}
}

func TestGuessSender(t *testing.T) {
	cases := map[string]string{
		"Stadtwerke Musterstadt GmbH\nHauptstrasse 1\nRechnung": "Stadtwerke Musterstadt",
		"\n  Hausverwaltung Beispiel GmbH & Co. KG\nAm Markt 5": "Hausverwaltung Beispiel",
		"Beispiel Versicherung AG\n":                            "Beispiel Versicherung",
		"Max Mustermann\nMusterweg 3\n12345 Musterstadt":        "",
		"Lieber Kunde, wir danken":                              "",
	}
	for in, want := range cases {
		if got := guessSender(in); got != want {
			t.Errorf("guessSender(%q) = %q, want %q", in, got, want)
		}
	}
}

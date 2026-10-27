package main

import "testing"

func TestParseBBox(t *testing.T) {
	in := `<doc><page width="595.000000" height="842.000000">
<word xMin="59.5" yMin="84.2" xMax="119.0" yMax="94.2">Datum:</word>
<word xMin="125.0" yMin="84.2" xMax="180.0" yMax="94.2">09.02.2026</word>
<word xMin="10" yMin="10" xMax="20" yMax="20">B&amp;B</word></page></doc>`
	w := parseBBox(in, 0, 0)
	if len(w) != 3 || w[1].T != "09.02.2026" || w[2].T != "B&B" {
		t.Fatalf("words %+v", w)
	}
	if w[0].X < 0.099 || w[0].X > 0.101 || w[0].Y < 0.099 || w[0].Y > 0.101 {
		t.Errorf("position %+v", w[0])
	}
}

func TestLooseAmount(t *testing.T) {
	for in, want := range map[string]int64{"84,20": 8420, "1.234,56": 123456, "84.20": 8420, "12": 1200, "2 499,00": 249900, "€ 7,5": 750} {
		if got := looseAmount(in); got == nil || *got != want {
			t.Errorf("looseAmount(%q) = %v, want %d", in, got, want)
		}
	}
}

package main

import (
	"encoding/json"
	"html"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Words with their place on the page, for picking a date, an amount or a
// name straight from the document. They come from the PDF's text layer
// (which Shoebox adds to every scan), so it works for any searchable PDF.

type pageWord struct {
	T string  `json:"t"`
	X float64 `json:"x"` // left, as a share of the page width
	Y float64 `json:"y"` // top, as a share of the page height
	W float64 `json:"w"`
	H float64 `json:"h"`
}

var (
	rePageSize = regexp.MustCompile(`<page width="([\d.]+)" height="([\d.]+)"`)
	reWord     = regexp.MustCompile(`<word xMin="([\d.]+)" yMin="([\d.]+)" xMax="([\d.]+)" yMax="([\d.]+)">([^<]*)</word>`)
)

func parseBBox(s string) []pageWord {
	m := rePageSize.FindStringSubmatch(s)
	if m == nil {
		return nil
	}
	pw, _ := strconv.ParseFloat(m[1], 64)
	ph, _ := strconv.ParseFloat(m[2], 64)
	if pw <= 0 || ph <= 0 {
		return nil
	}
	out := []pageWord{}
	for _, w := range reWord.FindAllStringSubmatch(s, -1) {
		x0, _ := strconv.ParseFloat(w[1], 64)
		y0, _ := strconv.ParseFloat(w[2], 64)
		x1, _ := strconv.ParseFloat(w[3], 64)
		y1, _ := strconv.ParseFloat(w[4], 64)
		t := strings.TrimSpace(html.UnescapeString(w[5]))
		if t == "" {
			continue
		}
		out = append(out, pageWord{t, x0 / pw, y0 / ph, (x1 - x0) / pw, (y1 - y0) / ph})
	}
	return out
}

func (a *App) handleDocWords(w http.ResponseWriter, r *http.Request) {
	d, ok := a.loadDoc(w, r, false)
	if !ok {
		return
	}
	n, _ := strconv.Atoi(r.PathValue("n"))
	if n < 1 || (d.Pages > 0 && n > d.Pages) {
		http.NotFound(w, r)
		return
	}
	cache := a.cacheFile(d.ID, "words-"+strconv.Itoa(n)+".json")
	if b, err := os.ReadFile(cache); err == nil {
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
		return
	}
	src, err := a.docFile(d)
	if err != nil {
		fail(w, 404, "The file is not reachable.")
		return
	}
	out, err := run(r.Context(), time.Minute, "pdftotext", "-bbox", "-f", strconv.Itoa(n), "-l", strconv.Itoa(n), src, "-")
	if err != nil {
		fail(w, 500, "Couldn't read the words of this page.")
		return
	}
	words := parseBBox(string(out))
	if words == nil {
		words = []pageWord{}
	}
	b, _ := json.Marshal(words)
	os.WriteFile(cache, b, 0o640)
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

// handleParse turns marked text into a field value: a date, an amount, a name.
func (a *App) handleParse(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind string `json:"kind"`
		Text string `json:"text"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	text := normalize(in.Text)
	res := map[string]any{"text": text}
	switch in.Kind {
	case "date":
		d := findDate(text, time.Now().AddDate(50, 0, 0)) // marked by hand: any year is fine
		res["value"] = d
	case "amount":
		if c := findAmount(text); c != nil {
			res["value"] = *c
		} else if c := looseAmount(text); c != nil {
			res["value"] = *c
		}
	default:
		res["value"] = clean(strings.Trim(text, " ,.;:"), 200)
	}
	writeJSON(w, 200, res)
}

var reLooseAmount = regexp.MustCompile(`-?\d{1,3}(?:[.\s]\d{3})*(?:[,.]\d{1,2})?|-?\d+(?:[,.]\d{1,2})?`)

// looseAmount reads "84,20", "1.234,56" or "84.20" without a currency sign.
func looseAmount(s string) *int64 {
	m := reLooseAmount.FindString(s)
	if m == "" {
		return nil
	}
	m = strings.ReplaceAll(m, " ", "")
	// the last separator followed by 1-2 digits is the decimal one
	dec := ""
	if i := strings.LastIndexAny(m, ",."); i >= 0 && len(m)-i-1 <= 2 {
		dec = m[i+1:]
		m = m[:i]
	}
	m = strings.NewReplacer(".", "", ",", "").Replace(m)
	for len(dec) < 2 {
		dec += "0"
	}
	v, err := strconv.ParseInt(m+dec, 10, 64)
	if err != nil {
		return nil
	}
	return &v
}

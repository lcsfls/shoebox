package main

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Facts guessed from the text of a document. The review screen shows them
// as suggestions; sorting rules can override sender, tags and title.
type Facts struct {
	Date        string
	AmountCents *int64
	Sender      string
	Title       string
	Tags        []string
	Rule        string
	Matched     bool     // a rule or a known sender matched
	Learned     []string // tags suggested by similar filed documents
}

var months = map[string]int{
	"januar": 1, "jan": 1, "january": 1, "jänner": 1,
	"februar": 2, "feb": 2, "february": 2,
	"märz": 3, "maerz": 3, "mär": 3, "mar": 3, "march": 3,
	"april": 4, "apr": 4,
	"mai": 5, "may": 5,
	"juni": 6, "jun": 6, "june": 6,
	"juli": 7, "jul": 7, "july": 7,
	"august": 8, "aug": 8,
	"september": 9, "sep": 9, "sept": 9,
	"oktober": 10, "okt": 10, "october": 10, "oct": 10,
	"november": 11, "nov": 11,
	"dezember": 12, "dez": 12, "december": 12, "dec": 12,
}

var (
	reDateDot   = regexp.MustCompile(`\b(\d{1,2})\s?[./]\s?(\d{1,2})\s?[./]\s?((?:19|20)?\d{2})\b`)
	reDateISO   = regexp.MustCompile(`\b((?:19|20)\d{2})-(\d{2})-(\d{2})\b`)
	reDateWords = regexp.MustCompile(`(?i)\b(\d{1,2})(?:\.\s*|\s+)([a-zäö]{3,9})\.?\s*((?:19|20)\d{2})\b`)
	reDateEN    = regexp.MustCompile(`(?i)\b([a-z]{3,9})\.?\s+(\d{1,2}),?\s+((?:19|20)\d{2})\b`)
	reDateCue   = regexp.MustCompile(`(?i)(rechnungsdatum|belegdatum|datum|date|vom|ausgestellt|invoice date|bescheid vom|stand)`)
)

type dateHit struct {
	t   time.Time
	pos int
	cue bool
}

// findDate picks the most likely document date: one next to a cue word
// ("Rechnungsdatum", "Datum", "vom" …) wins, otherwise the first plausible
// date near the top of the first page.
func findDate(text string, nowT time.Time) string {
	if len(text) > 6000 {
		text = text[:6000]
	}
	var hits []dateHit
	add := func(y, m, d, pos int) {
		if y < 100 {
			y += 2000
			if y > nowT.Year()+1 {
				y -= 100
			}
		}
		if m < 1 || m > 12 || d < 1 || d > 31 || y < 1980 {
			return
		}
		t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
		if t.Month() != time.Month(m) || t.After(nowT.AddDate(1, 0, 0)) {
			return
		}
		from := max(0, pos-40)
		cue := reDateCue.MatchString(text[from:pos])
		hits = append(hits, dateHit{t, pos, cue})
	}
	for _, m := range reDateDot.FindAllStringSubmatchIndex(text, -1) {
		d, _ := strconv.Atoi(text[m[2]:m[3]])
		mo, _ := strconv.Atoi(text[m[4]:m[5]])
		y, _ := strconv.Atoi(text[m[6]:m[7]])
		add(y, mo, d, m[0])
	}
	for _, m := range reDateISO.FindAllStringSubmatchIndex(text, -1) {
		y, _ := strconv.Atoi(text[m[2]:m[3]])
		mo, _ := strconv.Atoi(text[m[4]:m[5]])
		d, _ := strconv.Atoi(text[m[6]:m[7]])
		add(y, mo, d, m[0])
	}
	for _, m := range reDateWords.FindAllStringSubmatchIndex(text, -1) {
		if mo, ok := months[strings.ToLower(text[m[4]:m[5]])]; ok {
			d, _ := strconv.Atoi(text[m[2]:m[3]])
			y, _ := strconv.Atoi(text[m[6]:m[7]])
			add(y, mo, d, m[0])
		}
	}
	for _, m := range reDateEN.FindAllStringSubmatchIndex(text, -1) {
		if mo, ok := months[strings.ToLower(text[m[2]:m[3]])]; ok {
			d, _ := strconv.Atoi(text[m[4]:m[5]])
			y, _ := strconv.Atoi(text[m[6]:m[7]])
			add(y, mo, d, m[0])
		}
	}
	if len(hits) == 0 {
		return ""
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].cue != hits[j].cue {
			return hits[i].cue
		}
		return hits[i].pos < hits[j].pos
	})
	return hits[0].t.Format("2006-01-02")
}

var (
	reAmount    = regexp.MustCompile(`(?i)(?:€|eur)\s*(-?\d{1,3}(?:[.\s]\d{3})*(?:,\d{2})|-?\d+(?:,\d{2}))|(-?\d{1,3}(?:[.\s]\d{3})*(?:,\d{2})|-?\d+(?:,\d{2}))\s*(?:€|eur\b)`)
	reAmountCue = regexp.MustCompile(`(?i)(gesamtbetrag|rechnungsbetrag|endbetrag|zu zahlen|zahlbetrag|gesamt|summe|betrag|total|amount due)`)
)

// findAmount looks for the total: a euro amount right after a cue word
// ("Gesamtbetrag", "zu zahlen" …), otherwise the largest euro amount.
func findAmount(text string) *int64 {
	var best *int64
	bestCue := false
	for _, m := range reAmountIdx(text) {
		raw := m.value
		raw = strings.NewReplacer(".", "", " ", "").Replace(raw)
		raw = strings.Replace(raw, ",", ".", 1)
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil || f <= 0 || f > 10_000_000 {
			continue
		}
		cents := int64(f*100 + 0.5)
		from := max(0, m.pos-60)
		cue := reAmountCue.MatchString(text[from:m.pos])
		switch {
		case best == nil, cue && !bestCue, cue == bestCue && cents > *best:
			v := cents
			best, bestCue = &v, cue
		}
	}
	return best
}

type amountHit struct {
	value string
	pos   int
}

func reAmountIdx(text string) []amountHit {
	var out []amountHit
	for _, m := range reAmount.FindAllStringSubmatchIndex(text, -1) {
		switch {
		case m[2] >= 0:
			out = append(out, amountHit{text[m[2]:m[3]], m[0]})
		case m[4] >= 0:
			out = append(out, amountHit{text[m[4]:m[5]], m[0]})
		}
	}
	return out
}

// Common document kinds, used for a first title when no rule sets one.
var docKinds = []struct{ word, title string }{
	{"gutschrift", "Gutschrift"}, {"mahnung", "Mahnung"}, {"rechnung", "Rechnung"}, {"invoice", "Invoice"},
	{"kündigung", "Kündigung"}, {"vertrag", "Vertrag"}, {"bescheid", "Bescheid"}, {"angebot", "Angebot"},
	{"kontoauszug", "Kontoauszug"}, {"lohnabrechnung", "Lohnabrechnung"}, {"gehaltsabrechnung", "Gehaltsabrechnung"},
	{"versicherungsschein", "Versicherungsschein"}, {"police", "Police"}, {"quittung", "Quittung"}, {"receipt", "Receipt"},
	{"bestätigung", "Bestätigung"}, {"lieferschein", "Lieferschein"}, {"abrechnung", "Abrechnung"}, {"zeugnis", "Zeugnis"},
	{"steuererklärung", "Steuererklärung"}, {"vorauszahlung", "Vorauszahlung"}, {"festsetzung", "Festsetzung"},
	{"bescheinigung", "Bescheinigung"}, {"zahlungserinnerung", "Zahlungserinnerung"}, {"erinnerung", "Erinnerung"},
	{"mitteilung", "Mitteilung"}, {"anhörung", "Anhörung"}, {"einladung", "Einladung"}, {"kostenvoranschlag", "Kostenvoranschlag"},
}

var reSubject = regexp.MustCompile(`(?im)^\s*(?:betreff|betr\.|subject|re|unser zeichen ihr schreiben)\s*:\s*(.{4,90})$`)

// guessTitle uses the subject line ("Betreff: …") when the letter has one,
// otherwise the kind of document named first ("Rechnung", "Bescheid" …).
func guessTitle(text string) string {
	if m := reSubject.FindStringSubmatch(firstChars(text, 4000)); m != nil {
		if t := strings.TrimSpace(strings.Trim(m[1], " .:-")); hasLetters(t) {
			return clean(t, 120)
		}
	}
	head := strings.ToLower(text)
	if len(head) > 3000 {
		head = head[:3000]
	}
	best, bestPos := "", len(head)+1
	for _, k := range docKinds {
		if i := strings.Index(head, k.word); i >= 0 && i < bestPos {
			best, bestPos = k.title, i
		}
	}
	return best
}

var legalForm = regexp.MustCompile(`(?i)[\s,]+(gmbh\s*&\s*co\.?\s*kg|ggmbh|gmbh|mbh|ag|kg|ohg|ug(\s*\(haftungsbeschränkt\))?|e\.\s?v\.|se|ltd\.?|inc\.?|llc|s\.a\.|b\.v\.)$`)

// guessSender looks for a company name near the top of a letter: a short
// line ending in a legal form such as "GmbH" or "AG". The legal form is
// dropped, "Stadtwerke Musterstadt GmbH" becomes "Stadtwerke Musterstadt".
func guessSender(text string) string {
	n := 0
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if n++; n > 10 {
			break
		}
		if len(line) > 60 || strings.ContainsAny(line, "0123456789@") {
			continue
		}
		if m := legalForm.FindStringIndex(line); m != nil && m[0] > 2 {
			return strings.TrimSpace(line[:m[0]])
		}
	}
	return ""
}

func firstChars(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// hasLetters is false for titles like "2026-03-01" or "Scan_0001".
func hasLetters(s string) bool {
	n := 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			n++
		}
	}
	return n >= 3 && !reScanName.MatchString(s)
}

var reScanName = regexp.MustCompile(`(?i)^(scan|img|image|dok|doc|document|dokument|file|datei|pxl|dsc|cam|bild|untitled|unbenannt)[\s_-]*\d*$`)

// plausibleDate: a real date, not before 1980 and not more than a year ahead.
func plausibleDate(s string, nowT time.Time) bool {
	t, err := time.Parse("2006-01-02", s)
	return err == nil && t.Year() >= 1980 && !t.After(nowT.AddDate(1, 0, 0))
}

// knownSender finds a sender that already exists and is named near the top.
func knownSender(text string, senders []string) string {
	head := strings.ToLower(text)
	if len(head) > 2500 {
		head = head[:2500]
	}
	best, bestPos := "", len(head)+1
	for _, s := range senders {
		if len([]rune(s)) < 3 {
			continue
		}
		i := indexWord(head, strings.ToLower(s))
		if i >= 0 && i < bestPos {
			best, bestPos = s, i
		}
	}
	return best
}

// indexWord finds needle as a whole word (not inside another word).
func indexWord(hay, needle string) int {
	off := 0
	for {
		i := strings.Index(hay[off:], needle)
		if i < 0 {
			return -1
		}
		i += off
		before := i == 0 || !isWordRune(lastRune(hay[:i]))
		end := i + len(needle)
		after := end >= len(hay) || !isWordRune(firstRune(hay[end:]))
		if before && after {
			return i
		}
		off = i + 1
	}
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
func firstRune(s string) rune {
	for _, r := range s {
		return r
	}
	return 0
}
func lastRune(s string) rune {
	r := []rune(s)
	if len(r) == 0 {
		return 0
	}
	return r[len(r)-1]
}

// analyze combines rules, known senders and the guessers.
func (a *App) analyze(text string) Facts {
	f := Facts{Date: findDate(text, time.Now()), AmountCents: findAmount(text)}
	rules, _ := a.loadRules()
	for _, r := range rules {
		if !r.Enabled || !r.matches(text) {
			continue
		}
		f.Matched = true
		if f.Rule == "" {
			f.Rule = r.Name
		}
		if r.Sender != "" && f.Sender == "" {
			f.Sender = r.Sender
		}
		if r.Title != "" && f.Title == "" {
			f.Title = r.Title
		}
		for _, t := range r.Tags {
			if !containsFold(f.Tags, t) {
				f.Tags = append(f.Tags, t)
			}
		}
	}
	if f.Sender == "" {
		if s := knownSender(text, a.senderNames()); s != "" {
			f.Sender = s
			f.Matched = true
		}
	}
	if a.settings().Learn {
		l := a.learn(text, 0)
		for _, t := range l.Tags {
			if !containsFold(f.Tags, t) {
				f.Tags = append(f.Tags, t)
				f.Learned = append(f.Learned, t)
			}
		}
		if f.Sender == "" {
			f.Sender = l.Sender
		}
	}
	if f.Sender == "" {
		f.Sender = guessSender(text)
	}
	if f.Title == "" {
		f.Title = guessTitle(text)
	}
	return f
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

func (a *App) senderNames() []string {
	rows, err := a.db.Query(`SELECT name FROM senders`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		rows.Scan(&s)
		out = append(out, s)
	}
	return out
}

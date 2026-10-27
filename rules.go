package main

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"unicode"
)

// Rule: when the text matches, set sender, add tags, set a title.
// The match kinds are the ones Paperless uses, so its rules carry over.
type Rule struct {
	ID       int64    `json:"id"`
	Name     string   `json:"name"`
	Kind     string   `json:"kind"` // any | all | exact | regex | fuzzy
	Pattern  string   `json:"pattern"`
	NoCase   bool     `json:"nocase"`
	Sender   string   `json:"sender"`
	Tags     []string `json:"tags"`
	Title    string   `json:"title"`
	Position int      `json:"position"`
	Enabled  bool     `json:"enabled"`
	re       *regexp.Regexp
}

var ruleKinds = map[string]bool{"any": true, "all": true, "exact": true, "regex": true, "fuzzy": true}

func (a *App) loadRules(lib int64) ([]*Rule, error) {
	rows, err := a.db.Query(`SELECT id, name, kind, pattern, nocase, sender, tags, title, position, enabled FROM rules WHERE library_id = ? ORDER BY position, id`, lib)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Rule{}
	for rows.Next() {
		var r Rule
		var tags string
		rows.Scan(&r.ID, &r.Name, &r.Kind, &r.Pattern, &r.NoCase, &r.Sender, &tags, &r.Title, &r.Position, &r.Enabled)
		json.Unmarshal([]byte(tags), &r.Tags)
		if r.Tags == nil {
			r.Tags = []string{}
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

func words(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '.' && r != '@' && r != '&'
	})
}

// normalize collapses whitespace (OCR text has odd line breaks and spacing).
func normalize(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func (r *Rule) matches(text string) bool {
	if strings.TrimSpace(r.Pattern) == "" {
		return false
	}
	t, p := text, r.Pattern
	if r.NoCase {
		t, p = strings.ToLower(t), strings.ToLower(p)
	}
	switch r.Kind {
	case "any":
		for _, w := range words(p) {
			if indexWord(t, w) >= 0 {
				return true
			}
		}
	case "all":
		ws := words(p)
		for _, w := range ws {
			if indexWord(t, w) < 0 {
				return false
			}
		}
		return len(ws) > 0
	case "exact":
		return strings.Contains(normalize(t), normalize(p))
	case "regex":
		if r.re == nil {
			pat := r.Pattern
			if r.NoCase {
				pat = "(?i)" + pat
			}
			re, err := regexp.Compile(pat)
			if err != nil {
				return false
			}
			r.re = re
		}
		return r.re.MatchString(text)
	case "fuzzy":
		return fuzzyContains(normalize(t), normalize(p))
	}
	return false
}

// fuzzyContains: the pattern appears with at most ~20 % of its letters wrong
// (OCR mistakes like "Stadtwerke" → "Stadtwcrke").
func fuzzyContains(text, pat string) bool {
	if pat == "" {
		return false
	}
	if strings.Contains(text, pat) {
		return true
	}
	tr, pr := []rune(text), []rune(pat)
	if len(pr) > 64 || len(tr) > 200000 {
		return false
	}
	maxErr := len(pr) / 5
	// Sellers' approximate substring matching (edit distance, free start in the text).
	prev := make([]int, len(pr)+1)
	cur := make([]int, len(pr)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(tr); i++ {
		cur[0] = 0
		for j := 1; j <= len(pr); j++ {
			cost := 1
			if tr[i-1] == pr[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j-1]+cost, prev[j]+1, cur[j-1]+1)
		}
		if cur[len(pr)] <= maxErr {
			return true
		}
		prev, cur = cur, prev
	}
	return false
}

func (a *App) handleRules(w http.ResponseWriter, r *http.Request) {
	lib, ok := a.currentLib(w, r, false)
	if !ok {
		return
	}
	rules, err := a.loadRules(lib)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, rules)
}

func (a *App) handleRuleSave(w http.ResponseWriter, r *http.Request) {
	lib, ok := a.currentLib(w, r, true)
	if !ok {
		return
	}
	var in Rule
	if !readJSON(w, r, &in) {
		return
	}
	in.Name = clean(in.Name, 80)
	in.Pattern = clean(in.Pattern, 1000)
	in.Sender = clean(in.Sender, 120)
	in.Title = clean(in.Title, 200)
	if !ruleKinds[in.Kind] {
		fail(w, 400, "Unknown match kind.")
		return
	}
	if in.Pattern == "" {
		fail(w, 400, "The rule needs something to look for.")
		return
	}
	if in.Kind == "regex" {
		if _, err := regexp.Compile(in.Pattern); err != nil {
			fail(w, 400, "The regular expression is invalid: "+err.Error())
			return
		}
	}
	if in.Name == "" {
		in.Name = in.Pattern
		if in.Sender != "" {
			in.Name = in.Sender
		}
	}
	var tags []string
	for _, t := range in.Tags {
		if t = clean(t, 60); t != "" {
			tags = append(tags, t)
		}
	}
	tb, _ := json.Marshal(tags)
	if tags == nil {
		tb = []byte("[]")
	}
	if id := pathID(r, "id"); id > 0 {
		a.db.Exec(`UPDATE rules SET name=?, kind=?, pattern=?, nocase=?, sender=?, tags=?, title=?, position=?, enabled=? WHERE id=? AND library_id=?`,
			in.Name, in.Kind, in.Pattern, in.NoCase, in.Sender, string(tb), in.Title, in.Position, in.Enabled, id, lib)
	} else {
		a.db.Exec(`INSERT INTO rules(name, kind, pattern, nocase, sender, tags, title, position, enabled, library_id) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			in.Name, in.Kind, in.Pattern, in.NoCase, in.Sender, string(tb), in.Title, in.Position, in.Enabled, lib)
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) handleRuleDelete(w http.ResponseWriter, r *http.Request) {
	lib, ok := a.currentLib(w, r, true)
	if !ok {
		return
	}
	a.db.Exec(`DELETE FROM rules WHERE id = ? AND library_id = ?`, pathID(r, "id"), lib)
	writeJSON(w, 200, map[string]any{"ok": true})
}

// handleRuleTest runs all rules against a text, or against a stored document.
func (a *App) handleRuleTest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text  string `json:"text"`
		DocID int64  `json:"doc_id"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	lib, ok := a.currentLib(w, r, false)
	if !ok {
		return
	}
	text := in.Text
	if in.DocID > 0 {
		d, err := a.getDoc(in.DocID)
		if err != nil || d.LibraryID != lib {
			fail(w, 404, "No such document.")
			return
		}
		text = a.docText(in.DocID)
	}
	f := a.analyze(lib, text)
	writeJSON(w, 200, map[string]any{"date": f.Date, "amount_cents": f.AmountCents, "sender": f.Sender, "title": f.Title, "tags": f.Tags, "rule": f.Rule})
}

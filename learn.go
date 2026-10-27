package main

import (
	"encoding/json"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Learning from the archive, without a model to train: a new document gets
// the tags (and the sender) that most of its closest filed neighbours share.
// The neighbours come from the full-text index: the document's most telling
// words, searched with OR, ranked by BM25.

const (
	learnNeighbours = 8
	learnShare      = 0.6 // weighted share of neighbours that must agree
	learnMinVotes   = 2   // and at least this many of them
)

// foldWord matches the index's tokenizer: lower case, accents removed
// (unicode61 remove_diacritics 2).
func foldWord(w string) string {
	return accentFolder.Replace(strings.ToLower(w))
}

var accentFolder = strings.NewReplacer(
	"ä", "a", "ö", "o", "ü", "u", "á", "a", "à", "a", "â", "a", "ã", "a", "å", "a",
	"é", "e", "è", "e", "ê", "e", "ë", "e", "í", "i", "ì", "i", "î", "i", "ï", "i",
	"ó", "o", "ò", "o", "ô", "o", "õ", "o", "ú", "u", "ù", "u", "û", "u",
	"ç", "c", "ñ", "n", "ý", "y", "ÿ", "y", "š", "s", "ž", "z", "č", "c", "ł", "l")

var stopWords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`aber alle allem allen aller alles als also auch auf aus bei beim bis bitte das dass dein deine dem den der des die dies diese diesem diesen dieser dieses doch dort durch ein eine einem einen einer eines falls fuer für gegen hat hatte haben hier ihr ihre ihrem ihren ihrer ihres ist jede jedem jeden jeder jedes kann kein keine mit nach nicht noch nur oder ohne sehr sein seine sich sie sind so über ueber um und uns unser unsere unter vom von vor wann war waren was weil wenn werden wie wir wird wurde zum zur zwischen sehr geehrte geehrter damen herren freundlichen grüßen gruessen mit the and for with your from this that have will are you our datum seite page telefon email www`) {
		stopWords[foldWord(w)] = true
	}
}

// keyTerms picks up to n words of the text that are frequent in it and
// rare in the archive (tf-idf).
func (a *App) keyTerms(text string, n int) []string {
	tf := map[string]int{}
	count := 0
	for _, w := range strings.FieldsFunc(firstChars(text, 20000), func(r rune) bool { return !unicode.IsLetter(r) }) {
		w = foldWord(w)
		if len([]rune(w)) < 4 || stopWords[w] {
			continue
		}
		tf[w]++
		if count++; count > 3000 {
			break
		}
	}
	if len(tf) == 0 {
		return nil
	}
	if len(tf) > 300 {
		// keep the most frequent words; one index lookup each follows
		type wc struct {
			w string
			c int
		}
		var l []wc
		for w, c := range tf {
			l = append(l, wc{w, c})
		}
		sort.Slice(l, func(i, j int) bool { return l[i].c > l[j].c })
		tf = map[string]int{}
		for _, x := range l[:300] {
			tf[x.w] = x.c
		}
	}
	var total int
	a.db.QueryRow(`SELECT count(*) FROM docs WHERE status = 'filed'`).Scan(&total)
	if total == 0 {
		return nil
	}
	type scored struct {
		w string
		s float64
	}
	var list []scored
	for w, c := range tf {
		var df int
		a.db.QueryRow(`SELECT doc FROM docs_vocab WHERE term = ?`, w).Scan(&df)
		if df == 0 || (total >= 20 && float64(df) > 0.4*float64(total)) {
			continue // unknown words don't find neighbours; very common ones don't tell them apart
		}
		list = append(list, scored{w, (1 + math.Log(float64(c))) * math.Log(float64(total+1)/float64(df))})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].s > list[j].s })
	out := []string{}
	for i := 0; i < len(list) && i < n; i++ {
		out = append(out, list[i].w)
	}
	return out
}

type neighbour struct {
	id     int64
	sender string
	tags   []string
	weight float64
}

func (a *App) neighbours(text string, exclude int64) []neighbour {
	terms := a.keyTerms(text, 30)
	if len(terms) < 3 {
		return nil
	}
	q := make([]string, len(terms))
	for i, t := range terms {
		q[i] = `"` + strings.ReplaceAll(t, `"`, ``) + `"`
	}
	rows, err := a.db.Query(`SELECT d.id, COALESCE(s.name,''),
		COALESCE((SELECT group_concat(t.name, char(31)) FROM doc_tags dt JOIN tags t ON t.id = dt.tag_id WHERE dt.doc_id = d.id), ''),
		bm25(docs_fts, 1.0, 1.0, 0.0, 1.0)
		FROM docs_fts JOIN docs d ON d.id = docs_fts.rowid LEFT JOIN senders s ON s.id = d.sender_id
		WHERE docs_fts MATCH ? AND d.status = 'filed' AND d.id <> ?
		ORDER BY bm25(docs_fts, 1.0, 1.0, 0.0, 1.0) LIMIT ?`, strings.Join(q, " OR "), exclude, learnNeighbours)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []neighbour
	for rows.Next() {
		var n neighbour
		var tags string
		var score float64
		rows.Scan(&n.id, &n.sender, &tags, &score)
		if tags != "" {
			n.tags = strings.Split(tags, "\x1f")
		}
		n.weight = -score // bm25 is negative; more negative = closer
		if n.weight <= 0 {
			n.weight = 0.001
		}
		out = append(out, n)
	}
	return out
}

// Learned is what the neighbours agree on.
type Learned struct {
	Tags   []string `json:"tags"`
	Sender string   `json:"sender"`
}

func (a *App) learn(text string, exclude int64) Learned {
	ns := a.neighbours(text, exclude)
	out := Learned{Tags: []string{}}
	if len(ns) < learnMinVotes {
		return out
	}
	var total float64
	tagW, tagN := map[string]float64{}, map[string]int{}
	sendW, sendN := map[string]float64{}, map[string]int{}
	for _, n := range ns {
		total += n.weight
		for _, t := range n.tags {
			tagW[t] += n.weight
			tagN[t]++
		}
		if n.sender != "" {
			sendW[n.sender] += n.weight
			sendN[n.sender]++
		}
	}
	for t, w := range tagW {
		if w/total >= learnShare && tagN[t] >= learnMinVotes {
			out.Tags = append(out.Tags, t)
		}
	}
	sort.Slice(out.Tags, func(i, j int) bool { return tagW[out.Tags[i]] > tagW[out.Tags[j]] })
	if len(out.Tags) > 4 {
		out.Tags = out.Tags[:4]
	}
	best, bw := "", 0.0
	for s, w := range sendW {
		if w > bw {
			best, bw = s, w
		}
	}
	if best != "" && bw/total >= learnShare && sendN[best] >= learnMinVotes {
		out.Sender = best
	}
	return out
}

// GET /api/docs/{id}/learned: what similar documents suggest (Review shows it).
func (a *App) handleDocLearned(w http.ResponseWriter, r *http.Request) {
	id := pathID(r, "id")
	writeJSON(w, 200, a.learn(a.docText(id), id))
}

// --- rule suggestions ---

// RuleSuggestion is a sorting rule Shoebox proposes from the archive: a line
// that appears in (nearly) every letter of one sender and in no other.
type RuleSuggestion struct {
	Key     string   `json:"key"`
	Sender  string   `json:"sender"`
	Pattern string   `json:"pattern"`
	Tags    []string `json:"tags"`
	Docs    int      `json:"docs"`    // documents of that sender
	Covered int      `json:"covered"` // of those, how many contain the line
}

var reBoringLine = regexp.MustCompile(`(?i)^(seite|page|datum|date|tel|telefon|fax|e-?mail|www\.|iban|bic|ust|steuer-?nr|amtsgericht|geschäftsführ|sehr geehrte|mit freundlichen)`)

// candidateLines are the stable-looking lines at the top and bottom of a
// letter (letterhead, footer), normalized.
func candidateLines(text string) map[string]bool {
	lines := strings.Split(text, "\n")
	pick := lines
	if len(lines) > 60 {
		pick = append(append([]string{}, lines[:40]...), lines[len(lines)-20:]...)
	}
	out := map[string]bool{}
	for _, l := range pick {
		l = normalize(l)
		n := len([]rune(l))
		if n < 6 || n > 70 || reBoringLine.MatchString(l) {
			continue
		}
		letters, digits := 0, 0
		for _, r := range l {
			if unicode.IsLetter(r) {
				letters++
			} else if unicode.IsDigit(r) {
				digits++
			}
		}
		if letters < 5 || digits > letters {
			continue
		}
		// dates and amounts change from letter to letter
		if reDateDot.MatchString(l) || reDateWords.MatchString(l) || reAmount.MatchString(l) {
			continue
		}
		out[strings.ToLower(l)] = true
	}
	return out
}

func (a *App) ruleSuggestions() []RuleSuggestion {
	rules, _ := a.loadRules()
	covered := map[string]bool{}
	for _, r := range rules {
		if r.Sender != "" {
			covered[strings.ToLower(r.Sender)] = true
		}
	}
	dismissed := map[string]bool{}
	var raw string
	if a.db.QueryRow(`SELECT value FROM settings WHERE key = 'rule_suggestions_dismissed'`).Scan(&raw) == nil {
		var keys []string
		json.Unmarshal([]byte(raw), &keys)
		for _, k := range keys {
			dismissed[k] = true
		}
	}

	type docT struct {
		sender string
		tags   []string
		lines  map[string]bool
	}
	rows, err := a.db.Query(`SELECT COALESCE(s.name,''), f.body,
		COALESCE((SELECT group_concat(t.name, char(31)) FROM doc_tags dt JOIN tags t ON t.id = dt.tag_id WHERE dt.doc_id = d.id), '')
		FROM docs d JOIN docs_fts f ON f.rowid = d.id LEFT JOIN senders s ON s.id = d.sender_id
		WHERE d.status = 'filed' ORDER BY d.id DESC LIMIT 3000`)
	if err != nil {
		return nil
	}
	var all []docT
	bySender := map[string][]int{}
	for rows.Next() {
		var d docT
		var body, tags string
		rows.Scan(&d.sender, &body, &tags)
		if tags != "" {
			d.tags = strings.Split(tags, "\x1f")
		}
		d.lines = candidateLines(body)
		all = append(all, d)
		if d.sender != "" {
			bySender[d.sender] = append(bySender[d.sender], len(all)-1)
		}
	}
	rows.Close()

	// in how many documents of each sender every line appears
	lineDocs := map[string]map[string]int{}
	for _, d := range all {
		for l := range d.lines {
			m := lineDocs[l]
			if m == nil {
				m = map[string]int{}
				lineDocs[l] = m
			}
			m[d.sender]++
		}
	}

	var out []RuleSuggestion
	for sender, idx := range bySender {
		n := len(idx)
		if n < 3 || covered[strings.ToLower(sender)] {
			continue
		}
		need := int(math.Ceil(0.8 * float64(n)))
		bestLine, bestCov := "", 0
		seen := map[string]bool{}
		for _, i := range idx {
			for l := range all[i].lines {
				if seen[l] {
					continue
				}
				seen[l] = true
				m := lineDocs[l]
				cov := m[sender]
				if cov < need {
					continue
				}
				others := 0
				for s, c := range m {
					if s != sender {
						others += c
					}
				}
				if others > 0 {
					continue
				}
				// prefer more documents covered, then lines naming the sender, then longer lines
				better := cov > bestCov ||
					(cov == bestCov && strings.Contains(l, strings.ToLower(sender)) && !strings.Contains(bestLine, strings.ToLower(sender))) ||
					(cov == bestCov && len(l) > len(bestLine) && strings.Contains(l, strings.ToLower(sender)) == strings.Contains(bestLine, strings.ToLower(sender)))
				if better {
					bestLine, bestCov = l, cov
				}
			}
		}
		if bestLine == "" {
			continue
		}
		tagCount := map[string]int{}
		for _, i := range idx {
			for _, t := range all[i].tags {
				tagCount[t]++
			}
		}
		tags := []string{}
		for t, c := range tagCount {
			if c >= need {
				tags = append(tags, t)
			}
		}
		sort.Strings(tags)
		key := tokenHash(strings.ToLower(sender) + "\n" + bestLine)[:16]
		if dismissed[key] {
			continue
		}
		out = append(out, RuleSuggestion{Key: key, Sender: sender, Pattern: bestLine, Tags: tags, Docs: n, Covered: bestCov})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Docs > out[j].Docs })
	return out
}

func (a *App) handleRuleSuggestions(w http.ResponseWriter, r *http.Request) {
	out := a.ruleSuggestions()
	if out == nil {
		out = []RuleSuggestion{}
	}
	writeJSON(w, 200, out)
}

func (a *App) handleRuleSuggestionDismiss(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key string `json:"key"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	var raw string
	var keys []string
	if a.db.QueryRow(`SELECT value FROM settings WHERE key = 'rule_suggestions_dismissed'`).Scan(&raw) == nil {
		json.Unmarshal([]byte(raw), &keys)
	}
	keys = append(keys, clean(in.Key, 32))
	b, _ := json.Marshal(keys)
	a.db.Exec(`INSERT INTO settings(key, value) VALUES('rule_suggestions_dismissed', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, string(b))
	writeJSON(w, 200, map[string]any{"ok": true})
}

// tagNames lists the most used tags.
func (a *App) tagNames(n int) []string {
	rows, err := a.db.Query(`SELECT t.name FROM tags t LEFT JOIN doc_tags dt ON dt.tag_id = t.id GROUP BY t.id ORDER BY count(dt.doc_id) DESC, t.name LIMIT ?`, n)
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

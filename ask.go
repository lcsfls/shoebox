package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Asking the archive: "Wann läuft der Handyvertrag aus?" The closest
// documents are looked up (by meaning when an embedding model is set up,
// otherwise by the question's words), only in libraries the account can
// read, and the model answers from those alone, naming them as [1], [2] …
// What isn't in them, it says it doesn't know.

const askPrompt = `You answer questions about a household's own document archive (letters, invoices, contracts, notices).
Use ONLY the documents below. Answer briefly, in the language of the question.
Name the documents you use as [1], [2] … right where you use them.
When the documents don't answer the question, say so plainly; never guess dates, amounts or names.
For sums, add only the amounts listed with the documents and show the items you added.
Dates: today is %s.

DOCUMENTS
%s
QUESTION
%s`

const (
	askDocs  = 6    // documents given to the model
	askChars = 9000 // and at most this much of their text
)

type askSource struct {
	N       int    `json:"n"`
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	Sender  string `json:"sender"`
	Date    string `json:"date"`
	Library string `json:"library"`
}

func (a *App) handleAsk(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Question string `json:"question"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	q := strings.TrimSpace(in.Question)
	if q == "" {
		fail(w, 400, "Ask a question.")
		return
	}
	s := a.settings().AI
	if !s.Enabled || s.BaseURL == "" || s.Model == "" {
		fail(w, 400, "Asking needs an AI model (Settings → Text recognition & AI).")
		return
	}
	answer, sources, err := a.ask(r.Context(), s, a.userLibraries(userFrom(r)), clean(q, 500))
	if err != nil {
		fail(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"answer": answer, "sources": sources})
}

func (a *App) ask(ctx context.Context, s AISettings, libs []Library, q string) (string, []askSource, error) {
	names := map[int64]string{}
	for _, l := range libs {
		names[l.ID] = l.Name
	}
	type cand struct {
		id     int64
		score  float64
		pieces []string
	}
	found := map[int64]*cand{}
	add := func(id int64, score float64, piece string) {
		c := found[id]
		if c == nil {
			c = &cand{id: id}
			found[id] = c
		}
		c.score += score
		if piece != "" && len(c.pieces) < 3 {
			c.pieces = append(c.pieces, piece)
		}
	}
	// by meaning: the closest pieces of every library one can read
	if s.embedOn() {
		if qv, err := a.queryVector(ctx, s, q); err == nil {
			for _, l := range libs {
				lv := a.libVectors(l.ID, s.EmbedModel)
				var hits []struct {
					i int
					s float32
				}
				for i, v := range lv.vecs {
					hits = append(hits, struct {
						i int
						s float32
					}{i, dot(qv, v)})
				}
				sort.Slice(hits, func(x, y int) bool { return hits[x].s > hits[y].s })
				for rank, h := range hits {
					if rank == 12 || h.s < 0.3 {
						break
					}
					var t string
					a.db.QueryRow(`SELECT text FROM doc_chunks WHERE doc_id = ? AND idx = ?`, lv.docs[h.i], lv.idx[h.i]).Scan(&t)
					add(lv.docs[h.i], float64(h.s), t)
				}
			}
		}
	}
	// by the question's words (also when there is no embedding model)
	if terms := a.keyTerms(q, 8); len(terms) > 0 {
		quoted := make([]string, len(terms))
		for i, t := range terms {
			quoted[i] = `"` + strings.ReplaceAll(t, `"`, ``) + `"*`
		}
		for _, l := range libs {
			rows, err := a.db.Query(`SELECT d.id FROM docs_fts JOIN docs d ON d.id = docs_fts.rowid
				WHERE docs_fts MATCH ? AND d.library_id = ? AND d.status <> 'missing'
				ORDER BY bm25(docs_fts, 8.0, 4.0, 4.0, 1.0) LIMIT 6`, strings.Join(quoted, " OR "), l.ID)
			if err != nil {
				continue
			}
			rank := 0
			for rows.Next() {
				var id int64
				rows.Scan(&id)
				add(id, 0.5/float64(rank+1), "")
				rank++
			}
			rows.Close()
		}
	}
	list := make([]*cand, 0, len(found))
	for _, c := range found {
		list = append(list, c)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].score > list[j].score })
	if len(list) > askDocs {
		list = list[:askDocs]
	}
	if len(list) == 0 {
		return "I found no documents about that.", []askSource{}, nil
	}
	// the documents for the model: what Shoebox knows, then the closest pieces of text
	var ctxText strings.Builder
	sources := []askSource{}
	budget := askChars
	for i, c := range list {
		d, err := a.getDoc(c.id)
		if err != nil {
			continue
		}
		n := i + 1
		sources = append(sources, askSource{N: n, ID: d.ID, Title: d.Title, Sender: d.Sender, Date: d.Date, Library: names[d.LibraryID]})
		fmt.Fprintf(&ctxText, "[%d] %q", n, d.Title)
		if d.Sender != "" {
			fmt.Fprintf(&ctxText, ", from %s", d.Sender)
		}
		if d.Date != "" {
			fmt.Fprintf(&ctxText, ", dated %s", d.Date)
		}
		if d.AmountCents != nil {
			fmt.Fprintf(&ctxText, ", amount %d.%02d", *d.AmountCents/100, abs64(*d.AmountCents%100))
		}
		if d.Due != "" {
			fmt.Fprintf(&ctxText, ", deadline %s (%s)", d.Due, d.Action)
		}
		if len(d.Tags) > 0 {
			fmt.Fprintf(&ctxText, ", tags %s", strings.Join(d.Tags, ", "))
		}
		ctxText.WriteString("\n")
		if d.Summary != "" {
			ctxText.WriteString("Summary: " + d.Summary + "\n")
		}
		text := strings.Join(c.pieces, "\n…\n")
		if text == "" {
			text = a.docText(d.ID)
		}
		share := budget / (len(list) - i)
		text = shorten(strings.TrimSpace(text), max(share, 300))
		budget -= len(text)
		ctxText.WriteString(text + "\n\n")
	}
	prompt := fmt.Sprintf(askPrompt, todayISO(), ctxText.String(), q)
	ans, err := a.chat(ctx, s, []chatMessage{{Role: "user", Content: prompt}}, 700)
	if err != nil {
		return "", nil, err
	}
	ans = strings.TrimSpace(stripThinking(ans))
	if ans == "" {
		return "", nil, errors.New("the model gave no answer")
	}
	// the sources it named first, the others after
	cited := map[int]bool{}
	for _, m := range reCite.FindAllStringSubmatch(ans, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil {
			cited[n] = true
		}
	}
	sort.SliceStable(sources, func(i, j int) bool { return cited[sources[i].N] && !cited[sources[j].N] })
	return ans, sources, nil
}

var reCite = regexp.MustCompile(`\[(\d+)\]`)

// stripThinking drops a <think>…</think> block, which "thinking" models put first.
func stripThinking(s string) string {
	if i := strings.Index(s, "</think>"); i >= 0 && strings.Contains(s[:i], "<think>") {
		return s[i+len("</think>"):]
	}
	return s
}

func todayISO() string { return time.Now().Format("2006-01-02") }

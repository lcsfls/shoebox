package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Text recognition with an AI model behind an OpenAI-compatible API:
// vLLM, Ollama, llama.cpp server, LM Studio … Works with OCR models like
// Baidu's (served by vLLM) as well as general vision models.
// Tesseract still builds the searchable PDF; the model's text is used for
// search and for recognizing title, date and sender.

const defaultOCRPrompt = "Transcribe all text on this document page exactly as written, in reading order. Keep line breaks. Output only the text."

// enrichPrompt: one call fills in the facts, the tags from the archive's own
// words, keywords, a one-sentence summary and a deadline with what to do.
// The archive's examples stand in for training: no learning period.
const enrichPrompt = `You read a scanned letter, invoice, notice or contract for a household archive.
Answer with one JSON object only:
- title: short title in the document's language, like "Stromrechnung Oktober"
- date: the document's date as YYYY-MM-DD, or ""
- sender: the company, office or person who sent it, or ""
- amount: the total amount as a number, or null
- tags: the tags from the EXISTING TAGS list that fit (at most 4); spell them exactly as listed
- new_tags: at most 2 new tags, only when nothing in the list fits, else []
- keywords: 3 to 6 things worth searching for later: contract, customer, invoice or file numbers, product, place, person
- summary: one sentence in the document's language: what it is and what it means for the reader
- due: the date by which the reader has to do something (pay, answer, object, hand in, cancel) as YYYY-MM-DD, or "" when there is no such date
- action: what the reader has to do, in a few words in the document's language (like "Betrag überweisen", "Widerspruch prüfen", "Unterlagen einreichen"), or "" when nothing is to be done
`

// the shape of the answer, for servers that can hold the model to it
var enrichSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"title": map[string]any{"type": "string"}, "date": map[string]any{"type": "string"},
		"sender": map[string]any{"type": "string"}, "amount": map[string]any{"type": []string{"number", "null"}},
		"tags":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"new_tags": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"keywords": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"summary":  map[string]any{"type": "string"}, "due": map[string]any{"type": "string"}, "action": map[string]any{"type": "string"},
	},
	"required":             []string{"title", "date", "sender", "amount", "tags", "new_tags", "keywords", "summary", "due", "action"},
	"additionalProperties": false,
}

type chatMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

func (a *App) chat(ctx context.Context, s AISettings, msgs []chatMessage, maxTokens int) (string, error) {
	return a.chatAs(ctx, s, msgs, maxTokens, nil)
}

// chatJSON asks for an answer in the given JSON schema (vLLM, Ollama, LM
// Studio and llama.cpp keep the model to it); a server that refuses that
// is asked again without it.
func (a *App) chatJSON(ctx context.Context, s AISettings, msgs []chatMessage, maxTokens int, schema map[string]any) (string, error) {
	ans, err := a.chatAs(ctx, s, msgs, maxTokens, schema)
	var se serverError
	if errors.As(err, &se) && se.status >= 400 && se.status < 500 {
		return a.chatAs(ctx, s, msgs, maxTokens, nil)
	}
	return ans, err
}

type serverError struct {
	status int
	msg    string
}

func (e serverError) Error() string {
	return fmt.Sprintf("model server answered %d: %s", e.status, e.msg)
}

func (a *App) chatAs(ctx context.Context, s AISettings, msgs []chatMessage, maxTokens int, schema map[string]any) (string, error) {
	req0 := map[string]any{"model": s.Model, "messages": msgs, "temperature": 0, "max_tokens": maxTokens}
	if schema != nil {
		req0["response_format"] = map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "facts", "schema": schema, "strict": true}}
	}
	body, _ := json.Marshal(req0)
	ctx, cancel := context.WithTimeout(ctx, time.Duration(s.TimeoutSec)*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", s.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.APIKey)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if res.StatusCode != 200 {
		return "", serverError{res.StatusCode, strings.TrimSpace(string(raw[:min(len(raw), 300)]))}
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Choices) == 0 {
		return "", errors.New("unexpected answer from the model server")
	}
	return out.Choices[0].Message.Content, nil
}

// aiOCR sends the pages as images and returns the transcribed text.
func (a *App) aiOCR(ctx context.Context, s AISettings, pdf, dir string) (string, error) {
	imgs, err := rasterize(ctx, pdf, dir, 150, "jpeg", 1, s.MaxPages)
	if err != nil {
		return "", err
	}
	prompt := s.Prompt
	if prompt == "" {
		prompt = defaultOCRPrompt
	}
	var text strings.Builder
	for _, img := range imgs {
		b, err := os.ReadFile(img)
		if err != nil {
			return "", err
		}
		msg := chatMessage{Role: "user", Content: []map[string]any{
			{"type": "image_url", "image_url": map[string]string{"url": "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(b)}},
			{"type": "text", "text": prompt},
		}}
		t, err := a.chat(ctx, s, []chatMessage{msg}, 4096)
		if err != nil {
			return "", err
		}
		text.WriteString(stripFences(t))
		text.WriteString("\n\f")
	}
	return text.String(), nil
}

var fenceRe = regexp.MustCompile("(?s)^\\s*```[a-zA-Z]*\\n(.*?)\\n?```\\s*$")

func stripFences(s string) string {
	if m := fenceRe.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return s
}

// aiFields asks the model for title, date, sender, amount and tags.
func (a *App) aiFields(ctx context.Context, s AISettings, lib int64, text string) (*Facts, error) {
	if len(text) > 12000 {
		text = text[:12000]
	}
	libs := a.sortingLibs(lib) // a Home also knows Family's words
	var vocab, senders []string
	for _, l := range libs {
		for _, t := range a.tagNames(l, 80) {
			if !containsFold(vocab, t) {
				vocab = append(vocab, t)
			}
		}
		senders = append(senders, a.senderNames(l)...)
	}
	var b strings.Builder
	b.WriteString(enrichPrompt)
	fmt.Fprintf(&b, "\nToday is %s.\n", time.Now().Format("2006-01-02"))
	if len(vocab) > 0 {
		b.WriteString("\nEXISTING TAGS: " + strings.Join(vocab, ", ") + "\n")
	} else {
		b.WriteString("\nEXISTING TAGS: (none yet: suggest up to 2 in new_tags)\n")
	}
	if len(senders) > 0 && len(senders) <= 200 {
		b.WriteString("Known senders (use the same spelling): " + strings.Join(senders, ", ") + "\n")
	}
	if ex := a.enrichExamples(libs, text); ex != "" {
		b.WriteString("\nHow this archive filed similar documents (follow its tags):\n" + ex)
	}
	b.WriteString("\nDocument text:\n")
	ans, err := a.chatJSON(ctx, s, []chatMessage{{Role: "user", Content: b.String() + text}}, 700, enrichSchema)
	if err != nil {
		return nil, err
	}
	return parseEnrich(ans, vocab)
}

// enrichExamples: up to four filed documents most like this one, with their tags.
func (a *App) enrichExamples(libs []int64, text string) string {
	var ns []neighbour
	for _, l := range libs {
		ns = append(ns, a.neighbours(l, text, 0)...)
	}
	sort.SliceStable(ns, func(i, j int) bool { return ns[i].weight > ns[j].weight })
	var b strings.Builder
	for i, n := range ns {
		if i == 4 {
			break
		}
		var title string
		a.db.QueryRow(`SELECT title FROM docs WHERE id = ?`, n.id).Scan(&title)
		fmt.Fprintf(&b, "- %q from %s: tags %s\n", title, n.sender, strings.Join(n.tags, ", "))
	}
	return b.String()
}

// parseEnrich checks the model's answer field by field: tags only from the
// archive's words (new ones only while it has hardly any), real dates, a
// deadline that isn't before the document.
func parseEnrich(ans string, vocab []string) (*Facts, error) {
	ans = stripFences(ans)
	if i, j := strings.Index(ans, "{"), strings.LastIndex(ans, "}"); i >= 0 && j > i {
		ans = ans[i : j+1]
	}
	var out struct {
		Title    string   `json:"title"`
		Date     string   `json:"date"`
		Sender   string   `json:"sender"`
		Amount   any      `json:"amount"`
		Tags     []string `json:"tags"`
		NewTags  []string `json:"new_tags"`
		Keywords []string `json:"keywords"`
		Summary  string   `json:"summary"`
		Due      string   `json:"due"`
		Action   string   `json:"action"`
	}
	if err := json.Unmarshal([]byte(ans), &out); err != nil {
		return nil, fmt.Errorf("the model didn't answer with JSON: %w", err)
	}
	f := &Facts{Title: clean(out.Title, 200), Sender: clean(out.Sender, 120), Summary: clean(out.Summary, 400), Action: clean(out.Action, 80)}
	if validDate(out.Date) {
		f.Date = out.Date
	}
	switch v := out.Amount.(type) {
	case float64:
		c := int64(v*100 + 0.5)
		f.AmountCents = &c
	case string:
		if x, err := strconv.ParseFloat(strings.Replace(strings.ReplaceAll(v, ".", ""), ",", ".", 1), 64); err == nil {
			c := int64(x*100 + 0.5)
			f.AmountCents = &c
		}
	}
	for _, t := range out.Tags {
		for _, v := range vocab {
			if strings.EqualFold(strings.TrimSpace(t), v) && !containsFold(f.Tags, v) && len(f.Tags) < 4 {
				f.Tags = append(f.Tags, v)
			}
		}
	}
	// new words only while the archive has hardly any of its own, so it doesn't sprawl
	if len(vocab) < 10 {
		for _, t := range out.NewTags {
			if t = clean(t, 40); t != "" && !containsFold(f.Tags, t) && len(f.Tags) < 4 {
				f.Tags = append(f.Tags, t)
			}
		}
	}
	f.Keywords = splitKeywords(cleanKeywords(out.Keywords))
	if d := validDue(out.Due); d != "" && (f.Date == "" || d >= f.Date) && d <= time.Now().AddDate(3, 0, 0).Format("2006-01-02") {
		f.Due = d
	}
	return f, nil
}

// handleAITest checks the connection: lists the models the server offers.
func (a *App) handleAITest(w http.ResponseWriter, r *http.Request) {
	var in AISettings
	if !readJSON(w, r, &in) {
		return
	}
	if in.APIKey == "" {
		in.APIKey = a.settings().AI.APIKey
	}
	base := strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	if base == "" {
		fail(w, 400, "Enter the address of the model server.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", base+"/models", nil)
	if err != nil {
		fail(w, 400, "The address is invalid.")
		return
	}
	if in.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+in.APIKey)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		fail(w, 502, "Can't reach the model server: "+err.Error())
		return
	}
	defer res.Body.Close()
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != 200 || json.Unmarshal(raw, &out) != nil {
		fail(w, 502, fmt.Sprintf("The server answered %d. Is this an OpenAI-compatible address ending in /v1?", res.StatusCode))
		return
	}
	var models []string
	found := false
	for _, m := range out.Data {
		models = append(models, m.ID)
		if m.ID == in.Model {
			found = true
		}
	}
	writeJSON(w, 200, map[string]any{"ok": true, "models": models, "model_found": found})
}

// enrichDoc asks the model about a document already in Shoebox: its
// summary, keywords, deadline and what to do are replaced, its tags gain
// the fitting ones from the archive's words, and title, sender, date and
// amount are filled in only where empty (what a person set stays).
func (a *App) enrichDoc(ctx context.Context, id int64) error {
	s := a.settings().AI
	if !s.Enabled || s.BaseURL == "" || s.Model == "" {
		return errors.New("Set up an AI model first (Settings → Text recognition & AI).")
	}
	d, err := a.getDoc(id)
	if err != nil {
		return err
	}
	text := a.docText(id)
	if !hasText(text) {
		return errors.New("This document has no text to read.")
	}
	f, err := a.aiFields(ctx, s, d.LibraryID, text)
	if err != nil {
		return err
	}
	p := DocPatch{Summary: &f.Summary, Keywords: &f.Keywords, Due: &f.Due, Action: &f.Action}
	tags := append([]string{}, d.Tags...)
	for _, t := range f.Tags {
		if !containsFold(tags, t) {
			tags = append(tags, t)
		}
	}
	p.Tags = &tags
	if d.Title == "" && f.Title != "" {
		p.Title = &f.Title
	}
	if d.Sender == "" && f.Sender != "" {
		p.Sender = &f.Sender
	}
	if d.Date == "" && f.Date != "" {
		p.Date = &f.Date
	}
	if d.AmountCents == nil && f.AmountCents != nil {
		p.AmountCents = f.AmountCents
	}
	if err := a.patchDoc(id, p); err != nil {
		return err
	}
	if d.Status == "filed" {
		return a.fileDoc(ctx, id) // name, metadata and the .md follow
	}
	return nil
}

func (a *App) handleDocEnrich(w http.ResponseWriter, r *http.Request) {
	d, ok := a.loadDoc(w, r, true)
	if !ok {
		return
	}
	if err := a.enrichDoc(r.Context(), d.ID); err != nil {
		fail(w, 400, err.Error())
		return
	}
	d, _ = a.getDoc(d.ID)
	writeJSON(w, 200, map[string]any{"doc": d})
}

// enrichAll: the documents of a library that have no summary yet, one
// after the other in the background (an archive from before, or from
// Paperless). One run at a time; it can be stopped.
type enrichRun struct {
	mu      sync.Mutex
	running bool
	lib     int64
	done    int
	failed  int
	total   int
	lastErr string
	stop    chan struct{}
}

func (a *App) enrichPending(lib int64) int {
	var n int
	a.db.QueryRow(`SELECT count(*) FROM docs WHERE library_id = ? AND summary = '' AND status <> 'missing'`, lib).Scan(&n)
	return n
}

func (a *App) handleEnrichAll(w http.ResponseWriter, r *http.Request) {
	lib, ok := a.currentLib(w, r, true)
	if !ok {
		return
	}
	run := &a.enrich
	run.mu.Lock()
	defer run.mu.Unlock()
	if r.Method == http.MethodGet {
		writeJSON(w, 200, map[string]any{"running": run.running, "library": run.lib, "done": run.done, "failed": run.failed,
			"total": run.total, "error": run.lastErr, "pending": a.enrichPending(lib)})
		return
	}
	if r.URL.Query().Get("stop") != "" {
		if run.running {
			close(run.stop)
			run.running = false
		}
		writeJSON(w, 200, map[string]any{"ok": true})
		return
	}
	if run.running {
		fail(w, 409, "A run is already going.")
		return
	}
	s := a.settings().AI
	if !s.Enabled || s.BaseURL == "" || s.Model == "" {
		fail(w, 400, "Set up an AI model first.")
		return
	}
	rows, err := a.db.Query(`SELECT id FROM docs WHERE library_id = ? AND summary = '' AND status <> 'missing' ORDER BY id DESC`, lib)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	run.running, run.lib, run.total, run.done, run.failed, run.lastErr, run.stop = true, lib, len(ids), 0, 0, "", make(chan struct{})
	stop := run.stop
	go func() {
		for _, id := range ids {
			select {
			case <-stop:
				return
			default:
			}
			err := a.enrichDoc(context.Background(), id)
			run.mu.Lock()
			if err != nil {
				run.failed++
				run.lastErr = err.Error()
			} else {
				run.done++
			}
			run.mu.Unlock()
		}
		run.mu.Lock()
		if run.stop == stop {
			run.running = false
		}
		run.mu.Unlock()
	}()
	writeJSON(w, 200, map[string]any{"ok": true, "total": len(ids)})
}

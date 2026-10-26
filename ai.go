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
	"strconv"
	"strings"
	"time"
)

// Text recognition with an AI model behind an OpenAI-compatible API:
// vLLM, Ollama, llama.cpp server, LM Studio … Works with OCR models like
// Baidu's (served by vLLM) as well as general vision models.
// Tesseract still builds the searchable PDF; the model's text is used for
// search and for recognizing title, date and sender.

const defaultOCRPrompt = "Transcribe all text on this document page exactly as written, in reading order. Keep line breaks. Output only the text."

const fieldsPrompt = `You read a scanned letter or invoice and answer with JSON only:
{"title": short title in the document's language (e.g. "Stromrechnung Oktober"), "date": the document date as YYYY-MM-DD or "",
 "sender": the company or person who sent it or "", "amount": the total amount as a number or null, "tags": up to 3 short keywords}
Document text:
`

type chatMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

func (a *App) chat(ctx context.Context, s AISettings, msgs []chatMessage, maxTokens int) (string, error) {
	body, _ := json.Marshal(map[string]any{"model": s.Model, "messages": msgs, "temperature": 0, "max_tokens": maxTokens})
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
		return "", fmt.Errorf("model server answered %d: %s", res.StatusCode, strings.TrimSpace(string(raw[:min(len(raw), 300)])))
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
func (a *App) aiFields(ctx context.Context, s AISettings, text string) (*Facts, error) {
	if len(text) > 12000 {
		text = text[:12000]
	}
	prompt := fieldsPrompt
	if tags := a.tagNames(60); len(tags) > 0 {
		// so the model reuses the archive's tags instead of inventing new spellings
		prompt = strings.Replace(prompt, "Document text:", "Prefer these existing tags when they fit: "+strings.Join(tags, ", ")+"\nDocument text:", 1)
	}
	if senders := a.senderNames(); len(senders) > 0 && len(senders) <= 200 {
		prompt = strings.Replace(prompt, "Document text:", "Known senders (use the same spelling): "+strings.Join(senders, ", ")+"\nDocument text:", 1)
	}
	ans, err := a.chat(ctx, s, []chatMessage{{Role: "user", Content: prompt + text}}, 400)
	if err != nil {
		return nil, err
	}
	ans = stripFences(ans)
	if i, j := strings.Index(ans, "{"), strings.LastIndex(ans, "}"); i >= 0 && j > i {
		ans = ans[i : j+1]
	}
	var out struct {
		Title  string   `json:"title"`
		Date   string   `json:"date"`
		Sender string   `json:"sender"`
		Amount any      `json:"amount"`
		Tags   []string `json:"tags"`
	}
	if err := json.Unmarshal([]byte(ans), &out); err != nil {
		return nil, fmt.Errorf("the model didn't answer with JSON: %w", err)
	}
	f := &Facts{Title: clean(out.Title, 200), Sender: clean(out.Sender, 120)}
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
		if t = clean(t, 40); t != "" && len(f.Tags) < 3 {
			f.Tags = append(f.Tags, t)
		}
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

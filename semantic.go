package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Semantic search: every document is cut into pieces (a head with title,
// sender, tags, summary and keywords, then its text), each piece becomes a
// vector from an embedding model (any OpenAI-compatible /embeddings, e.g.
// bge-m3 in Ollama), kept in SQLite. A search asks the same model for the
// question's vector and mixes the closest documents with the full-text hits
// (reciprocal rank fusion), so "Fahrzeug" also finds the "Kfz-Versicherung".
// A family archive is a few thousand documents: comparing with every
// vector in memory is quick enough, no vector database needed.

const (
	chunkChars  = 1200 // text per piece
	chunkMax    = 8    // pieces of text per document (the first pages say what it is)
	embedBatch  = 16
	fuseK       = 60.0 // reciprocal rank fusion
	semanticTop = 100
)

func (s AISettings) embedOn() bool { return s.Enabled && s.EmbedModel != "" && s.embedBase() != "" }
func (s AISettings) embedBase() string {
	if s.EmbedURL != "" {
		return s.EmbedURL
	}
	return s.BaseURL
}

// embed asks the server for one vector per text, normalized to length 1.
func (a *App) embed(ctx context.Context, s AISettings, texts []string) ([][]float32, error) {
	body, _ := json.Marshal(map[string]any{"model": s.EmbedModel, "input": texts})
	ctx, cancel := context.WithTimeout(ctx, time.Duration(max(s.TimeoutSec, 30))*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", s.embedBase()+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.APIKey)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("embedding server answered %d: %s", res.StatusCode, strings.TrimSpace(string(raw[:min(len(raw), 300)])))
	}
	var out struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Data) != len(texts) {
		return nil, errors.New("unexpected answer from the embedding server")
	}
	vecs := make([][]float32, len(texts))
	for i, d := range out.Data {
		k := d.Index
		if k < 0 || k >= len(vecs) {
			k = i
		}
		vecs[k] = unitVec(d.Embedding)
	}
	return vecs, nil
}

func unitVec(v []float64) []float32 {
	var n float64
	for _, x := range v {
		n += x * x
	}
	n = math.Sqrt(n)
	if n == 0 {
		n = 1
	}
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(x / n)
	}
	return out
}

func vecBytes(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(x))
	}
	return b
}

func bytesVec(b []byte) []float32 {
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return v
}

func dot(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}
	var s float32
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

// chunks: the head (what Shoebox knows about it), then the text in pieces at paragraph breaks.
func chunks(d *Doc, text string) []string {
	head := strings.TrimSpace(strings.Join([]string{d.Title, d.Sender, d.Date, strings.Join(d.Tags, ", "), d.Summary, strings.Join(d.Keywords, ", ")}, "\n"))
	out := []string{head}
	var cur strings.Builder
	flush := func() {
		if t := strings.TrimSpace(cur.String()); len(t) > 40 && len(out) <= chunkMax {
			out = append(out, t)
		}
		cur.Reset()
	}
	for _, par := range strings.FieldsFunc(text, func(r rune) bool { return r == '\f' }) {
		for _, line := range strings.Split(par, "\n\n") {
			line = strings.Join(strings.Fields(line), " ")
			if cur.Len()+len(line) > chunkChars {
				flush()
			}
			for len(line) > chunkChars { // a paragraph longer than a piece
				cut := strings.LastIndex(line[:chunkChars], " ")
				if cut < chunkChars/2 {
					cut = chunkChars
				}
				cur.WriteString(line[:cut])
				flush()
				line = strings.TrimSpace(line[cut:])
			}
			cur.WriteString(line + "\n")
		}
	}
	flush()
	return out
}

// --- the indexer: documents without vectors of the current model, a few at a time ---

type embedder struct {
	mu      sync.Mutex
	lastErr string
	lastAt  time.Time
	kick    chan struct{}
}

func (a *App) kickEmbedder() {
	select {
	case a.embedding.kick <- struct{}{}:
	default:
	}
}

func (a *App) embedLoop(ctx context.Context) {
	wait := 5 * time.Second
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.embedding.kick:
		case <-time.After(wait):
		}
		s := a.settings().AI
		if !s.embedOn() {
			wait = time.Minute
			continue
		}
		n, err := a.embedSome(ctx, s, 20)
		a.embedding.mu.Lock()
		if err != nil {
			a.embedding.lastErr, a.embedding.lastAt = err.Error(), time.Now()
		} else if n > 0 {
			a.embedding.lastErr = ""
		}
		a.embedding.mu.Unlock()
		switch {
		case err != nil:
			log.Printf("semantic search: %v", err)
			wait = time.Minute
		case n > 0:
			wait = 200 * time.Millisecond // more waiting
		default:
			wait = 30 * time.Second
		}
	}
}

// embedSome makes the vectors of up to n documents; it says how many.
func (a *App) embedSome(ctx context.Context, s AISettings, n int) (int, error) {
	rows, err := a.db.Query(`SELECT id FROM docs WHERE embed_model <> ? AND status <> 'missing' ORDER BY id DESC LIMIT ?`, s.EmbedModel, n)
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	done := 0
	for _, id := range ids {
		d, err := a.getDoc(id)
		if err != nil {
			continue
		}
		pieces := chunks(d, a.docText(id))
		var vecs [][]float32
		for i := 0; i < len(pieces); i += embedBatch {
			v, err := a.embed(ctx, s, pieces[i:min(i+embedBatch, len(pieces))])
			if err != nil {
				return done, err
			}
			vecs = append(vecs, v...)
		}
		tx, err := a.db.Begin()
		if err != nil {
			return done, err
		}
		tx.Exec(`DELETE FROM doc_chunks WHERE doc_id = ?`, id)
		for i, p := range pieces {
			tx.Exec(`INSERT INTO doc_chunks(doc_id, idx, text, vec) VALUES(?,?,?,?)`, id, i, p, vecBytes(vecs[i]))
		}
		tx.Exec(`UPDATE docs SET embed_model = ? WHERE id = ?`, s.EmbedModel, id)
		if err := tx.Commit(); err != nil {
			return done, err
		}
		a.vectors.changed()
		done++
	}
	return done, nil
}

// --- the vectors in memory, per library, rebuilt after changes ---

type vecIndex struct {
	mu      sync.Mutex
	version int64
	libs    map[int64]*libVectors
}

type libVectors struct {
	version int64
	model   string
	docs    []int64
	idx     []int
	vecs    [][]float32
}

func (v *vecIndex) changed() {
	v.mu.Lock()
	v.version++
	v.mu.Unlock()
}

func (a *App) libVectors(lib int64, model string) *libVectors {
	v := &a.vectors
	v.mu.Lock()
	defer v.mu.Unlock()
	if lv := v.libs[lib]; lv != nil && lv.version == v.version && lv.model == model {
		return lv
	}
	lv := &libVectors{version: v.version, model: model}
	rows, err := a.db.Query(`SELECT c.doc_id, c.idx, c.vec FROM doc_chunks c JOIN docs d ON d.id = c.doc_id
		WHERE d.library_id = ? AND d.embed_model = ? AND d.status <> 'missing'`, lib, model)
	if err == nil {
		for rows.Next() {
			var id int64
			var i int
			var b []byte
			rows.Scan(&id, &i, &b)
			lv.docs, lv.idx, lv.vecs = append(lv.docs, id), append(lv.idx, i), append(lv.vecs, bytesVec(b))
		}
		rows.Close()
	}
	if v.libs == nil {
		v.libs = map[int64]*libVectors{}
	}
	v.libs[lib] = lv
	return lv
}

type semHit struct {
	id    int64
	score float32
	piece int
}

// nearest: the documents whose best piece is closest to the vector.
func (lv *libVectors) nearest(q []float32, n int) []semHit {
	best := map[int64]semHit{}
	for i, v := range lv.vecs {
		s := dot(q, v)
		if h, ok := best[lv.docs[i]]; !ok || s > h.score {
			best[lv.docs[i]] = semHit{lv.docs[i], s, lv.idx[i]}
		}
	}
	out := make([]semHit, 0, len(best))
	for _, h := range best {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].score > out[j].score })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// query vectors for the last questions (typing a search asks again and again)
var queryCache = struct {
	sync.Mutex
	m map[string][]float32
}{m: map[string][]float32{}}

func (a *App) queryVector(ctx context.Context, s AISettings, q string) ([]float32, error) {
	key := s.EmbedModel + "\x00" + q
	queryCache.Lock()
	v, ok := queryCache.m[key]
	queryCache.Unlock()
	if ok {
		return v, nil
	}
	vs, err := a.embed(ctx, s, []string{q})
	if err != nil {
		return nil, err
	}
	queryCache.Lock()
	if len(queryCache.m) > 500 {
		queryCache.m = map[string][]float32{}
	}
	queryCache.m[key] = vs[0]
	queryCache.Unlock()
	return vs[0], nil
}

// semanticSearch: full-text hits and the closest documents by meaning,
// mixed by rank; the other filters (state, year, tags, sender) apply to
// both. Documents found only by meaning show their closest piece.
func (a *App) semanticSearch(ctx context.Context, f DocQuery) ([]*Doc, int, error) {
	s := a.settings().AI
	qv, err := a.queryVector(ctx, s, f.Q)
	if err != nil {
		return nil, 0, err
	}
	hits := a.libVectors(f.Library, s.EmbedModel).nearest(qv, semanticTop)
	// keep the ones near the best: far-off documents are noise, not meaning
	var near []semHit
	for _, h := range hits {
		if h.score >= 0.3 && (len(near) == 0 || h.score >= near[0].score-0.2) {
			near = append(near, h)
		}
	}
	// full-text hits with the same filters, in their order
	ft := f
	ft.Sort, ft.Limit, ft.Offset = "relevance", 200, 0
	textDocs, _, err := a.listDocs(ft)
	if err != nil {
		return nil, 0, err
	}
	// the meaning hits that pass the filters
	pass := map[int64]bool{}
	if len(near) > 0 {
		ids := make([]any, len(near))
		for i, h := range near {
			ids[i] = h.id
		}
		ff := f
		ff.Q, ff.Sort, ff.Limit, ff.Offset, ff.IDs = "", "", 200, 0, ids
		allowed, _, err := a.listDocs(ff)
		if err != nil {
			return nil, 0, err
		}
		for _, d := range allowed {
			pass[d.ID] = true
		}
	}
	score := map[int64]float64{}
	byID := map[int64]*Doc{}
	for i, d := range textDocs {
		score[d.ID] += 1 / (fuseK + float64(i+1))
		byID[d.ID] = d
		d.Why = "text"
	}
	piece := map[int64]int{}
	rank := 0
	for _, h := range near {
		if !pass[h.id] {
			continue
		}
		rank++
		score[h.id] += 1 / (fuseK + float64(rank))
		piece[h.id] = h.piece
		if d := byID[h.id]; d != nil {
			d.Why = "both"
		}
	}
	order := make([]int64, 0, len(score))
	for id := range score {
		order = append(order, id)
	}
	sort.SliceStable(order, func(i, j int) bool { return score[order[i]] > score[order[j]] })
	total := len(order)
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 60
	}
	if f.Offset > len(order) {
		f.Offset = len(order)
	}
	order = order[f.Offset:min(len(order), f.Offset+f.Limit)]
	out := make([]*Doc, 0, len(order))
	for _, id := range order {
		d := byID[id]
		if d == nil {
			if d, err = a.getDoc(id); err != nil {
				continue
			}
			d.Why = "meaning"
			// the closest piece of text; for the head (title, sender …) the summary or the text's start says more
			var t string
			if piece[id] == 0 && d.Summary != "" {
				t = d.Summary
			} else {
				a.db.QueryRow(`SELECT text FROM doc_chunks WHERE doc_id = ? AND idx >= ? ORDER BY idx LIMIT 1`, id, max(piece[id], 1)).Scan(&t)
			}
			d.Snippet = shorten(strings.Join(strings.Fields(t), " "), 220)
		}
		out = append(out, d)
	}
	return out, total, nil
}

func shorten(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + " …"
}

// handleSemanticStatus: how far the indexer is (the AI settings show it).
func (a *App) handleSemanticStatus(w http.ResponseWriter, r *http.Request) {
	s := a.settings().AI
	var total, done int
	a.db.QueryRow(`SELECT count(*), COALESCE(sum(embed_model = ?),0) FROM docs WHERE status <> 'missing'`, s.EmbedModel).Scan(&total, &done)
	a.embedding.mu.Lock()
	defer a.embedding.mu.Unlock()
	writeJSON(w, 200, map[string]any{"on": s.embedOn(), "model": s.EmbedModel, "total": total, "done": done, "error": a.embedding.lastErr})
}

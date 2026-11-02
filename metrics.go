package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Metrics for a statistics server: one set of numbers, in the formats the
// usual tools read. Pulled from /api/metrics (Prometheus/OpenMetrics text,
// ?format=json or ?format=influx) with an admin's API token, or pushed by
// Shoebox every few minutes to a URL (Settings → Statistics export).
//
// Shared libraries are listed one by one; the private Homes only as their
// sum (library="Homes"), so nobody learns what is in someone else's Home.

type metric struct {
	name   string
	help   string
	kind   string // gauge | counter
	labels [][2]string
	value  float64
}

var started = time.Now()

func (a *App) collectMetrics() []metric {
	var out []metric
	add := func(name, kind, help string, v float64, labels ...[2]string) {
		out = append(out, metric{name: name, kind: kind, help: help, labels: labels, value: v})
	}
	lbl := func(k, v string) [2]string { return [2]string{k, v} }

	add("shoebox_info", "gauge", "Version of Shoebox (always 1).", 1, lbl("version", version))
	add("shoebox_start_time_seconds", "gauge", "When Shoebox started (Unix time).", float64(started.Unix()))

	// per library; Homes summed
	group := func(lib Library) string {
		if lib.OwnerID != 0 {
			return "Homes"
		}
		return lib.Name
	}
	type libNums struct{ filed, inbox, missing, pages, bytes float64 }
	nums := map[string]*libNums{}
	sources := map[[2]string]float64{}
	groups := map[int64]string{}
	var order []string
	for _, l := range a.allLibraries() {
		g := group(l)
		groups[l.ID] = g
		if nums[g] == nil {
			nums[g] = &libNums{}
			order = append(order, g)
		}
	}
	if rows, err := a.db.Query(`SELECT library_id, status, source, count(*), COALESCE(sum(pages),0), COALESCE(sum(size),0) FROM docs GROUP BY library_id, status, source`); err == nil {
		for rows.Next() {
			var lib int64
			var status, source string
			var n, pages, size float64
			rows.Scan(&lib, &status, &source, &n, &pages, &size)
			g, ok := groups[lib]
			if !ok {
				continue
			}
			x := nums[g]
			switch status {
			case "filed":
				x.filed += n
			case "inbox":
				x.inbox += n
			case "missing":
				x.missing += n
			}
			if status != "missing" {
				x.pages += pages
				x.bytes += size
				sources[[2]string{g, source}] += n
			}
		}
		rows.Close()
	}
	sort.SliceStable(order, func(i, j int) bool { return order[i] != "Homes" && order[j] == "Homes" })
	for _, g := range order {
		x := nums[g]
		add("shoebox_documents", "gauge", "Documents by library and state (filed, inbox = waiting in Review, missing = file gone).", x.filed, lbl("library", g), lbl("status", "filed"))
		add("shoebox_documents", "gauge", "", x.inbox, lbl("library", g), lbl("status", "inbox"))
		add("shoebox_documents", "gauge", "", x.missing, lbl("library", g), lbl("status", "missing"))
		add("shoebox_pages", "gauge", "Pages of the documents by library.", x.pages, lbl("library", g))
		add("shoebox_bytes", "gauge", "Size of the documents by library.", x.bytes, lbl("library", g))
	}
	var sk [][2]string
	for k := range sources {
		sk = append(sk, k)
	}
	sort.Slice(sk, func(i, j int) bool { return sk[i][0]+sk[i][1] < sk[j][0]+sk[j][1] })
	for _, k := range sk {
		add("shoebox_documents_by_source", "gauge", "Documents by library and how they came in (scan, upload, folder, paperless, letter).", sources[k], lbl("library", k[0]), lbl("source", k[1]))
	}

	var ocrMs, ocrDocs float64
	a.db.QueryRow(`SELECT COALESCE(sum(ocr_ms),0), COALESCE(sum(ocr_ms>0),0) FROM docs`).Scan(&ocrMs, &ocrDocs)
	add("shoebox_ocr_seconds_total", "counter", "Time spent recognizing text, all documents.", ocrMs/1000)
	add("shoebox_ocr_documents_total", "counter", "Documents whose text Shoebox recognized.", ocrDocs)

	jobs := map[string]float64{"queued": 0, "working": 0, "done": 0, "failed": 0}
	for _, j := range a.jobs.snapshot() {
		jobs[j.State]++
	}
	for _, s := range []string{"queued", "working", "done", "failed"} {
		add("shoebox_jobs", "gauge", "Scans, uploads and found files being processed, by state (since the start).", jobs[s], lbl("state", s))
	}

	var users, libs float64
	a.db.QueryRow(`SELECT count(*) FROM users`).Scan(&users)
	a.db.QueryRow(`SELECT count(*) FROM libraries WHERE owner_id IS NULL`).Scan(&libs)
	add("shoebox_users", "gauge", "Accounts.", users)
	add("shoebox_libraries", "gauge", "Shared libraries (without the Homes).", libs)

	var st syscall.Statfs_t
	if err := syscall.Statfs(a.cfg.Archive, &st); err == nil {
		add("shoebox_archive_free_bytes", "gauge", "Free space where the archive folder lies.", float64(st.Bavail)*float64(st.Bsize))
		add("shoebox_archive_size_bytes", "gauge", "Size of the disk the archive folder lies on.", float64(st.Blocks)*float64(st.Bsize))
	}
	if last, _, errMsg := a.watcher.status(); !last.IsZero() {
		add("shoebox_folder_scan_timestamp_seconds", "gauge", "When the archive folder was last looked through (Unix time).", float64(last.Unix()))
		add("shoebox_folder_scan_error", "gauge", "1 when the last look through the archive folder failed.", b2f(errMsg != ""))
	}
	var lastBackup string
	a.db.QueryRow(`SELECT value FROM settings WHERE key = 'last_backup'`).Scan(&lastBackup)
	if t, err := time.Parse(time.RFC3339, lastBackup); err == nil {
		add("shoebox_backup_timestamp_seconds", "gauge", "When the database was last backed up (Unix time).", float64(t.Unix()))
	}
	a.updates.mu.Lock()
	if rel := a.updates.latest; rel != nil {
		add("shoebox_update_available", "gauge", "1 when a newer release is out.", b2f(newerVersion(rel.Tag, version)), lbl("latest", rel.Tag))
	}
	a.updates.mu.Unlock()
	return out
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// --- formats ---

func num(v float64) string {
	if v == math.Trunc(v) && math.Abs(v) < 1e15 {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// prometheusText: the Prometheus text format (also read as OpenMetrics by most).
func prometheusText(ms []metric) []byte {
	var b bytes.Buffer
	seen := map[string]bool{}
	esc := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	for _, m := range ms {
		if !seen[m.name] {
			seen[m.name] = true
			if m.help != "" {
				fmt.Fprintf(&b, "# HELP %s %s\n", m.name, m.help)
			}
			fmt.Fprintf(&b, "# TYPE %s %s\n", m.name, m.kind)
		}
		b.WriteString(m.name)
		if len(m.labels) > 0 {
			b.WriteByte('{')
			for i, l := range m.labels {
				if i > 0 {
					b.WriteByte(',')
				}
				fmt.Fprintf(&b, `%s="%s"`, l[0], esc.Replace(l[1]))
			}
			b.WriteByte('}')
		}
		fmt.Fprintf(&b, " %s\n", num(m.value))
	}
	return b.Bytes()
}

// influxLines: InfluxDB line protocol, one measurement per metric, the
// labels as tags and the number as the field "value".
func influxLines(ms []metric, at time.Time) []byte {
	var b bytes.Buffer
	esc := strings.NewReplacer(",", `\,`, " ", `\ `, "=", `\=`)
	for _, m := range ms {
		b.WriteString(m.name)
		for _, l := range m.labels {
			if l[1] != "" {
				fmt.Fprintf(&b, ",%s=%s", l[0], esc.Replace(l[1]))
			}
		}
		fmt.Fprintf(&b, " value=%s %d\n", num(m.value), at.UnixNano())
	}
	return b.Bytes()
}

// metricsJSON: the totals at the top (easy to pick for a Home Assistant
// sensor or Uptime Kuma), the libraries by name, and every metric as a list.
func metricsJSON(ms []metric) map[string]any {
	totals := map[string]float64{"documents": 0, "filed": 0, "inbox": 0, "missing": 0, "pages": 0, "bytes": 0}
	libs := map[string]map[string]float64{}
	lib := func(name string) map[string]float64 {
		if libs[name] == nil {
			libs[name] = map[string]float64{}
		}
		return libs[name]
	}
	list := []map[string]any{}
	out := map[string]any{"version": version, "time": time.Now().UTC().Format(time.RFC3339)}
	for _, m := range ms {
		labels := map[string]string{}
		for _, l := range m.labels {
			labels[l[0]] = l[1]
		}
		list = append(list, map[string]any{"name": m.name, "labels": labels, "value": m.value})
		switch m.name {
		case "shoebox_documents":
			totals[labels["status"]] += m.value
			lib(labels["library"])[labels["status"]] += m.value
			if labels["status"] != "missing" {
				totals["documents"] += m.value
			}
		case "shoebox_pages":
			totals["pages"] += m.value
			lib(labels["library"])["pages"] = m.value
		case "shoebox_bytes":
			totals["bytes"] += m.value
			lib(labels["library"])["bytes"] = m.value
		case "shoebox_jobs":
			out["jobs_"+labels["state"]] = m.value
		case "shoebox_info", "shoebox_documents_by_source":
		default:
			out[strings.TrimPrefix(m.name, "shoebox_")] = m.value
		}
	}
	for k, v := range totals {
		out[k] = v
	}
	out["libraries"] = libs
	out["metrics"] = list
	return out
}

// handleMetrics: GET /api/metrics[?format=prometheus|openmetrics|json|influx].
// Admins only (a read-only token of an admin is the right key for a
// statistics server). Tools that can't send a header may put a read-only
// token into ?token=.
func (a *App) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if tok := r.URL.Query().Get("token"); tok != "" && r.Header.Get("Authorization") == "" {
		u := a.tokenUser(tok)
		if u == nil {
			fail(w, 401, "Unknown or revoked API token.")
			return
		}
		if u.Role != "viewer" {
			fail(w, 403, "Only read-only tokens go into a URL; send this one as Authorization: Bearer … instead.")
			return
		}
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	a.auth(func(w http.ResponseWriter, r *http.Request) {
		var role string
		a.db.QueryRow(`SELECT role FROM users WHERE id = ?`, userFrom(r).ID).Scan(&role)
		if role != "admin" {
			fail(w, 403, "Statistics for a server need an admin's API token.")
			return
		}
		ms := a.collectMetrics()
		format := r.URL.Query().Get("format")
		if format == "" && strings.Contains(r.Header.Get("Accept"), "application/json") {
			format = "json"
		}
		w.Header().Set("Cache-Control", "no-store")
		switch format {
		case "json":
			writeJSON(w, 200, metricsJSON(ms))
		case "influx":
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Write(influxLines(ms, time.Now()))
		case "", "prometheus", "openmetrics":
			w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
			w.Write(prometheusText(ms))
		default:
			fail(w, 400, "Formats: prometheus, json, influx.")
		}
	})(w, r)
}

// --- pushing ---

// MetricsPush: where Shoebox sends its numbers by itself.
type MetricsPush struct {
	URL     string `json:"url"`     // empty = off
	Format  string `json:"format"`  // json | influx | prometheus
	Minutes int    `json:"minutes"` // how often
	Header  string `json:"header"`  // optional, e.g. "Authorization: Token abc"
}

func (a *App) pushMetrics(ctx context.Context, p MetricsPush) error {
	if p.URL == "" {
		return errors.New("No address set.")
	}
	if !strings.HasPrefix(p.URL, "http://") && !strings.HasPrefix(p.URL, "https://") {
		return errors.New("The address starts with http:// or https://.")
	}
	ms := a.collectMetrics()
	var body []byte
	ctype := "text/plain; charset=utf-8"
	switch p.Format {
	case "influx":
		body = influxLines(ms, time.Now())
	case "prometheus":
		body = prometheusText(ms) // e.g. a Pushgateway: …/metrics/job/shoebox
		ctype = "text/plain; version=0.0.4; charset=utf-8"
	default:
		body, _ = json.Marshal(metricsJSON(ms))
		ctype = "application/json"
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", p.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", ctype)
	req.Header.Set("User-Agent", "Shoebox/"+version)
	if k, v, ok := strings.Cut(p.Header, ":"); ok && strings.TrimSpace(k) != "" {
		req.Header.Set(strings.TrimSpace(k), strings.TrimSpace(v))
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 300))
		return fmt.Errorf("the server answered %d: %s", res.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

// metricsPusher sends the numbers on the schedule in the settings.
func (a *App) metricsPusher(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	var last time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		p := a.settings().Metrics
		if p.URL == "" || time.Since(last) < time.Duration(max(p.Minutes, 1))*time.Minute-10*time.Second {
			continue
		}
		last = time.Now()
		err := a.pushMetrics(ctx, p)
		a.metricsMu.Lock()
		a.metricsLast, a.metricsErr = last, ""
		if err != nil {
			a.metricsErr = err.Error()
			log.Printf("statistics export: %v", err)
		}
		a.metricsMu.Unlock()
	}
}

// handleMetricsPush: send once now (the "Test" button), and the state of the export.
func (a *App) handleMetricsPush(w http.ResponseWriter, r *http.Request) {
	var p MetricsPush
	if !readJSON(w, r, &p) {
		return
	}
	if p.Header == "" {
		p.Header = a.settings().Metrics.Header // stored, never shown
	}
	err := a.pushMetrics(r.Context(), p)
	a.metricsMu.Lock()
	a.metricsLast, a.metricsErr = time.Now(), ""
	if err != nil {
		a.metricsErr = err.Error()
	}
	a.metricsMu.Unlock()
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) handleMetricsStatus(w http.ResponseWriter, r *http.Request) {
	a.metricsMu.Lock()
	defer a.metricsMu.Unlock()
	out := map[string]any{"error": a.metricsErr}
	if !a.metricsLast.IsZero() {
		out["last"] = a.metricsLast.UTC().Format(time.RFC3339)
	}
	writeJSON(w, 200, out)
}

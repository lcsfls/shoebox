package main

import (
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Statistics for the curious: what came in, how, and how much.

type count struct {
	Key   string `json:"key"`
	Docs  int    `json:"docs"`
	Pages int    `json:"pages"`
}

func (a *App) countBy(q string, args ...any) []count {
	rows, err := a.db.Query(q, args...)
	if err != nil {
		return []count{}
	}
	defer rows.Close()
	out := []count{}
	for rows.Next() {
		var c count
		rows.Scan(&c.Key, &c.Docs, &c.Pages)
		out = append(out, c)
	}
	return out
}

func (a *App) handleStats(w http.ResponseWriter, r *http.Request) {
	res := map[string]any{}
	var docs, pages, filed, inbox, missing, ruled int
	var size, chars, ocrMs, ocrDocs int64
	a.db.QueryRow(`SELECT count(*), COALESCE(sum(pages),0), COALESCE(sum(size),0),
		COALESCE(sum(status='filed'),0), COALESCE(sum(status='inbox'),0), COALESCE(sum(status='missing'),0),
		COALESCE(sum(rule<>''),0), COALESCE(sum(ocr_ms),0), COALESCE(sum(ocr_ms>0),0) FROM docs`).
		Scan(&docs, &pages, &size, &filed, &inbox, &missing, &ruled, &ocrMs, &ocrDocs)
	a.db.QueryRow(`SELECT COALESCE(sum(length(body)),0) FROM docs_fts`).Scan(&chars)
	res["totals"] = map[string]any{
		"docs": docs, "pages": pages, "size": size, "filed": filed, "inbox": inbox, "missing": missing,
		"by_rule": ruled, "text_chars": chars, "ocr_ms": ocrMs, "ocr_docs": ocrDocs,
	}
	res["sources"] = a.countBy(`SELECT source, count(*), COALESCE(sum(pages),0) FROM docs GROUP BY source ORDER BY count(*) DESC`)
	res["ocr"] = a.countBy(`SELECT ocr, count(*), COALESCE(sum(pages),0) FROM docs GROUP BY ocr ORDER BY count(*) DESC`)
	// added per month, the last two years
	since := time.Now().AddDate(-2, 0, 0).Format("2006-01")
	res["added"] = a.countBy(`SELECT substr(created_at,1,7) m, count(*), COALESCE(sum(pages),0) FROM docs WHERE substr(created_at,1,7) >= ? GROUP BY m ORDER BY m`, since)
	res["years"] = a.countBy(`SELECT substr(doc_date,1,4) y, count(*), COALESCE(sum(pages),0) FROM docs WHERE doc_date <> '' AND status <> 'inbox' GROUP BY y ORDER BY y`)
	res["senders"] = a.countBy(`SELECT s.name, count(*), COALESCE(sum(d.pages),0) FROM docs d JOIN senders s ON s.id = d.sender_id GROUP BY s.id ORDER BY count(*) DESC LIMIT 10`)
	res["tags"] = a.countBy(`SELECT t.name, count(*), COALESCE(sum(d.pages),0) FROM doc_tags dt JOIN tags t ON t.id = dt.tag_id JOIN docs d ON d.id = dt.doc_id GROUP BY t.id ORDER BY count(*) DESC LIMIT 10`)
	res["people"] = a.countBy(`SELECT COALESCE(NULLIF(u.name,''), u.username), count(*), COALESCE(sum(d.pages),0) FROM docs d JOIN users u ON u.id = d.added_by GROUP BY u.id ORDER BY count(*) DESC`)
	// local time of day the scans happen (created_at is UTC)
	hours := make([]int, 24)
	weekdays := make([]int, 7)
	if rows, err := a.db.Query(`SELECT created_at FROM docs WHERE source IN ('scan','upload')`); err == nil {
		for rows.Next() {
			var s string
			rows.Scan(&s)
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				t = t.Local()
				hours[t.Hour()]++
				weekdays[(int(t.Weekday())+6)%7]++ // Monday first
			}
		}
		rows.Close()
	}
	res["hours"] = hours
	res["weekdays"] = weekdays
	var amount int64
	var amountDocs int
	a.db.QueryRow(`SELECT COALESCE(sum(amount_cents),0), count(amount_cents) FROM docs WHERE amount_cents IS NOT NULL AND substr(doc_date,1,4) = ?`, time.Now().Format("2006")).Scan(&amount, &amountDocs)
	res["amount_this_year"] = map[string]any{"cents": amount, "docs": amountDocs}
	var tagsN, sendersN, rulesN, usersN int
	a.db.QueryRow(`SELECT (SELECT count(*) FROM tags), (SELECT count(*) FROM senders), (SELECT count(*) FROM rules), (SELECT count(*) FROM users)`).Scan(&tagsN, &sendersN, &rulesN, &usersN)
	res["counts"] = map[string]int{"tags": tagsN, "senders": sendersN, "rules": rulesN, "users": usersN}
	var oldest string
	a.db.QueryRow(`SELECT COALESCE(min(doc_date),'') FROM docs WHERE doc_date <> ''`).Scan(&oldest)
	res["oldest"] = oldest
	res["db_size"] = fileSize(filepath.Join(a.cfg.DataDir, "shoebox.db")) + fileSize(filepath.Join(a.cfg.DataDir, "shoebox.db-wal"))
	res["cache_size"] = dirSize(filepath.Join(a.cfg.DataDir, "cache"))
	writeJSON(w, 200, res)
}

func fileSize(p string) int64 {
	if st, err := os.Stat(p); err == nil {
		return st.Size()
	}
	return 0
}

func dirSize(root string) int64 {
	var n int64
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

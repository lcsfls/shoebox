package main

import (
	"net/http"
	"strings"
)

// API tokens let scripts use the same API as the app: send
// "Authorization: Bearer sb_…". Each token belongs to an account and acts
// with its role; a read-only token can only read.

type apiToken struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Prefix   string `json:"prefix"`
	ReadOnly bool   `json:"readonly"`
	Created  string `json:"created_at"`
	LastUsed string `json:"last_used_at"`
}

func (a *App) handleTokens(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.Query(`SELECT id, name, prefix, readonly, created_at, last_used_at FROM api_tokens WHERE user_id = ? ORDER BY id`, userFrom(r).ID)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	defer rows.Close()
	out := []apiToken{}
	for rows.Next() {
		var t apiToken
		rows.Scan(&t.ID, &t.Name, &t.Prefix, &t.ReadOnly, &t.Created, &t.LastUsed)
		out = append(out, t)
	}
	writeJSON(w, 200, out)
}

func (a *App) handleTokenCreate(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if u.viaToken {
		fail(w, 403, "Create API tokens in the app, not with another token.")
		return
	}
	var in struct {
		Name     string `json:"name"`
		ReadOnly bool   `json:"readonly"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	name := clean(in.Name, 60)
	if name == "" {
		fail(w, 400, "Give the token a name, e.g. the script that uses it.")
		return
	}
	tok := "sb_" + randomToken(30)
	prefix := tok[:9]
	_, err := a.db.Exec(`INSERT INTO api_tokens(user_id, name, hash, prefix, readonly, created_at) VALUES(?,?,?,?,?,?)`,
		u.ID, name, tokenHash(tok), prefix, in.ReadOnly, now())
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"token": tok, "prefix": prefix})
}

func (a *App) handleTokenDelete(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if u.viaToken {
		fail(w, 403, "Revoke API tokens in the app.")
		return
	}
	a.db.Exec(`DELETE FROM api_tokens WHERE id = ? AND user_id = ?`, pathID(r, "id"), u.ID)
	writeJSON(w, 200, map[string]any{"ok": true})
}

// baseURL is the address people use to open Shoebox, for examples and
// passkeys: SB_BASE_URL, or what the browser asked for.
func (a *App) baseURL(r *http.Request) string {
	if a.cfg.BaseURL != "" {
		return strings.TrimRight(a.cfg.BaseURL, "/")
	}
	scheme := "http"
	if r.TLS != nil || (a.cfg.TrustProx && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")) {
		scheme = "https"
	}
	host := r.Host
	if fh := r.Header.Get("X-Forwarded-Host"); fh != "" && a.cfg.TrustProx {
		host = fh
	}
	return scheme + "://" + host
}

package main

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	sessionCookie = "shoebox_session"
	sessionDays   = 180 // long, so the phone stays signed in
	pbkdfIter     = 310000
)

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
	Role     string `json:"role"` // admin | member | viewer
	viaToken bool   // signed in with an API token instead of the session cookie
}

// Roles, from least to most: viewers read and download, members also scan,
// upload, review and edit documents, admins also change settings, rules
// and accounts.
const (
	roleViewer = 1
	roleMember = 2
	roleAdmin  = 3
)

func roleLevel(r string) int {
	switch r {
	case "admin":
		return roleAdmin
	case "member":
		return roleMember
	case "viewer":
		return roleViewer
	}
	return 0
}

func validRole(r string) bool { return roleLevel(r) > 0 }

type ctxKey struct{}

func userFrom(r *http.Request) *User {
	u, _ := r.Context().Value(ctxKey{}).(*User)
	return u
}

func hashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	rand.Read(salt)
	key, err := pbkdf2.Key(sha256.New, pw, salt, pbkdfIter, 32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", pbkdfIter, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func checkPassword(stored, pw string) bool {
	p := strings.Split(stored, "$")
	if len(p) != 4 || p[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(p[1])
	if err != nil {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(p[2])
	want, err2 := base64.RawStdEncoding.DecodeString(p[3])
	if err1 != nil || err2 != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, iter, len(want))
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

func randomToken(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func randomCode() string {
	b := make([]byte, 5)
	rand.Read(b)
	return strings.ToUpper(hex.EncodeToString(b))[:8]
}

func tokenHash(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

func (a *App) userCount() (int, error) {
	var n int
	err := a.db.QueryRow(`SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

var usernameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,31}$`)

func (a *App) createUser(username, name, pw, role string) (int64, error) {
	if !validRole(role) {
		return 0, errors.New("Unknown role.")
	}
	username = strings.ToLower(strings.TrimSpace(username))
	if !usernameRe.MatchString(username) {
		return 0, errors.New("Username: 2–32 characters, lowercase letters, digits, dot, dash or underscore.")
	}
	if utf8.RuneCountInString(pw) < 10 {
		return 0, errors.New("The password needs at least 10 characters.")
	}
	h, err := hashPassword(pw)
	if err != nil {
		return 0, err
	}
	res, err := a.db.Exec(`INSERT INTO users(username, name, pass, role, handle, created_at) VALUES(?,?,?,?,?,?)`,
		username, clean(name, 60), h, role, randomBytes(32), now())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return 0, errors.New("That username is taken.")
		}
		return 0, err
	}
	id, err := res.LastInsertId()
	if err == nil {
		if _, herr := a.ensureHome(id, username); herr != nil {
			log.Printf("no Home for %s: %v", username, herr)
		}
	}
	return id, err
}

func (a *App) startSession(w http.ResponseWriter, r *http.Request, uid int64) {
	tok := randomToken(32)
	exp := time.Now().Add(sessionDays * 24 * time.Hour)
	a.db.Exec(`INSERT INTO sessions(token, user_id, expires_at) VALUES(?,?,?)`, tokenHash(tok), uid, exp.Unix())
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: tok, Path: "/", Expires: exp, HttpOnly: true,
		Secure: r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"), SameSite: http.SameSiteLaxMode})
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}

func (a *App) currentUser(r *http.Request) *User {
	if t, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return a.tokenUser(strings.TrimSpace(t))
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	var u User
	err = a.db.QueryRow(`SELECT u.id, u.username, u.name, u.role FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token = ? AND s.expires_at > ?`, tokenHash(c.Value), time.Now().Unix()).Scan(&u.ID, &u.Username, &u.Name, &u.Role)
	if err != nil {
		return nil
	}
	if u.Name == "" {
		u.Name = u.Username
	}
	return &u
}

// tokenUser signs in with an API token ("sb_…"). A read-only token acts as
// a viewer, whatever the account's role.
func (a *App) tokenUser(tok string) *User {
	if !strings.HasPrefix(tok, "sb_") {
		return nil
	}
	var u User
	var id int64
	var ro bool
	err := a.db.QueryRow(`SELECT t.id, t.readonly, u.id, u.username, u.name, u.role FROM api_tokens t JOIN users u ON u.id = t.user_id
		WHERE t.hash = ?`, tokenHash(tok)).Scan(&id, &ro, &u.ID, &u.Username, &u.Name, &u.Role)
	if err != nil {
		return nil
	}
	if ro {
		u.Role = "viewer"
	}
	if u.Name == "" {
		u.Name = u.Username
	}
	u.viaToken = true
	a.db.Exec(`UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, now(), id)
	return &u
}

// auth guards API handlers: any signed-in account may read.
func (a *App) auth(h http.HandlerFunc) http.HandlerFunc { return a.guard(roleViewer, h) }

// member guards changes to documents: scanning, uploading, editing.
func (a *App) member(h http.HandlerFunc) http.HandlerFunc { return a.guard(roleMember, h) }

// admin guards settings, rules, accounts and the import.
func (a *App) admin(h http.HandlerFunc) http.HandlerFunc { return a.guard(roleAdmin, h) }

// guard needs a session or an API token and the role for the action.
// Changes made with the session cookie must come from this site (Origin
// check); API tokens are sent by scripts, not browsers, so no cookie can be
// misused there.
func (a *App) guard(level int, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := a.currentUser(r)
		if u == nil {
			if r.Header.Get("Authorization") != "" {
				fail(w, 401, "Unknown or revoked API token.")
				return
			}
			fail(w, 401, "Please sign in.")
			return
		}
		if !u.viaToken && r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r) {
			fail(w, 403, "Request from another site rejected.")
			return
		}
		if roleLevel(u.Role) < level {
			fail(w, 403, "Your account can't do this. Ask an admin.")
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, u)))
	}
}

func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		// Older browsers on same-origin fetches; require the app's header instead.
		return r.Header.Get("X-Shoebox") == "1"
	}
	host := r.Host
	if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
		host = fh
	}
	return strings.TrimPrefix(strings.TrimPrefix(o, "https://"), "http://") == host
}

func (a *App) handleMe(w http.ResponseWriter, r *http.Request) {
	n, _ := a.userCount()
	u := a.currentUser(r)
	out := map[string]any{"user": u, "needs_setup": n == 0, "version": version}
	if u != nil {
		out["libraries"] = a.userLibraries(u)
	}
	writeJSON(w, 200, out)
}

func (a *App) handleSetup(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		fail(w, 403, "Request from another site rejected.")
		return
	}
	if n, _ := a.userCount(); n > 0 || a.setupCode == "" {
		fail(w, 409, "Shoebox is already set up.")
		return
	}
	if !a.limiter.allow(a.clientIP(r)) {
		fail(w, 429, "Too many attempts. Wait a minute.")
		return
	}
	var in struct{ Code, Username, Name, Password string }
	if !readJSON(w, r, &in) {
		return
	}
	if subtle.ConstantTimeCompare([]byte(strings.ToUpper(strings.TrimSpace(in.Code))), []byte(a.setupCode)) != 1 {
		fail(w, 403, "Wrong setup code. It is printed in the server log when Shoebox starts.")
		return
	}
	id, err := a.createUser(in.Username, in.Name, in.Password, "admin")
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	a.db.Exec(`INSERT OR IGNORE INTO library_members(library_id, user_id, role) VALUES(1, ?, 'editor')`, id)
	a.setupCode = ""
	a.startSession(w, r, id)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		fail(w, 403, "Request from another site rejected.")
		return
	}
	if !a.limiter.allow(a.clientIP(r)) {
		fail(w, 429, "Too many attempts. Wait a minute.")
		return
	}
	var in struct{ Username, Password string }
	if !readJSON(w, r, &in) {
		return
	}
	var id int64
	var hash string
	err := a.db.QueryRow(`SELECT id, pass FROM users WHERE username = ?`, strings.ToLower(strings.TrimSpace(in.Username))).Scan(&id, &hash)
	if err != nil {
		checkPassword("pbkdf2-sha256$310000$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", in.Password) // same timing
	}
	if err != nil || !checkPassword(hash, in.Password) {
		fail(w, 401, "Wrong username or password.")
		return
	}
	a.startSession(w, r, id)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		a.db.Exec(`DELETE FROM sessions WHERE token = ?`, tokenHash(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) handleUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.Query(`SELECT u.id, u.username, u.name, u.role,
		(SELECT count(*) FROM passkeys p WHERE p.user_id = u.id),
		(SELECT count(*) FROM docs d WHERE d.added_by = u.id)
		FROM users u ORDER BY u.id`)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	defer rows.Close()
	type row struct {
		User
		Passkeys int `json:"passkeys"`
		Docs     int `json:"docs"`
	}
	out := []row{}
	for rows.Next() {
		var u row
		rows.Scan(&u.ID, &u.Username, &u.Name, &u.Role, &u.Passkeys, &u.Docs)
		out = append(out, u)
	}
	writeJSON(w, 200, out)
}

func (a *App) handleUserAdd(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username, Name, Password, Role string
		Libraries                      []struct {
			ID   int64  `json:"id"`
			Role string `json:"role"`
		} `json:"libraries"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Role == "" {
		in.Role = "member"
	}
	id, err := a.createUser(in.Username, in.Name, in.Password, in.Role)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if in.Libraries == nil {
		a.db.Exec(`INSERT OR IGNORE INTO library_members(library_id, user_id, role) VALUES(1, ?, 'editor')`, id)
	}
	for _, l := range in.Libraries {
		if validLibRole(l.Role) {
			a.db.Exec(`INSERT OR IGNORE INTO library_members(library_id, user_id, role) SELECT id, ?, ? FROM libraries WHERE id = ?`, id, l.Role, l.ID)
		}
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) adminCount() int {
	var n int
	a.db.QueryRow(`SELECT count(*) FROM users WHERE role = 'admin'`).Scan(&n)
	return n
}

// handleUserUpdate changes another account's name, role or password (admins).
func (a *App) handleUserUpdate(w http.ResponseWriter, r *http.Request) {
	id := pathID(r, "id")
	var in struct {
		Name     *string
		Role     *string
		Password *string
	}
	if !readJSON(w, r, &in) {
		return
	}
	var role string
	if err := a.db.QueryRow(`SELECT role FROM users WHERE id = ?`, id).Scan(&role); err != nil {
		fail(w, 404, "No such account.")
		return
	}
	if in.Role != nil && *in.Role != role {
		if !validRole(*in.Role) {
			fail(w, 400, "Unknown role.")
			return
		}
		if role == "admin" && a.adminCount() <= 1 {
			fail(w, 400, "Shoebox needs at least one admin.")
			return
		}
		a.db.Exec(`UPDATE users SET role = ? WHERE id = ?`, *in.Role, id)
	}
	if in.Name != nil {
		a.db.Exec(`UPDATE users SET name = ? WHERE id = ?`, clean(*in.Name, 60), id)
	}
	if in.Password != nil {
		if utf8.RuneCountInString(*in.Password) < 10 {
			fail(w, 400, "The password needs at least 10 characters.")
			return
		}
		h, _ := hashPassword(*in.Password)
		a.db.Exec(`UPDATE users SET pass = ? WHERE id = ?`, h, id)
		a.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, id)
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) handleUserDelete(w http.ResponseWriter, r *http.Request) {
	id := pathID(r, "id")
	if id == userFrom(r).ID {
		fail(w, 400, "You can't delete your own account.")
		return
	}
	var role string
	a.db.QueryRow(`SELECT role FROM users WHERE id = ?`, id).Scan(&role)
	if role == "admin" && a.adminCount() <= 1 {
		fail(w, 400, "Shoebox needs at least one admin.")
		return
	}
	// the Home goes with the account, but only empty: nobody else can see
	// what is in it, so its owner moves the documents to Family first
	home := a.homeLib(id)
	if home > 0 {
		var n int
		a.db.QueryRow(`SELECT count(*) FROM docs WHERE library_id = ? AND status <> 'missing'`, home).Scan(&n)
		if n > 0 {
			fail(w, 400, fmt.Sprintf("This account still has %d document(s) in its Home. Sign in as it and move them to Family (or delete them) first.", n))
			return
		}
	}
	a.db.Exec(`DELETE FROM users WHERE id = ?`, id)
	if home > 0 {
		a.db.Exec(`DELETE FROM docs WHERE library_id = ?`, home)
		for _, t := range []string{"rules", "tags", "senders", "letters", "libraries"} {
			col := "library_id"
			if t == "libraries" {
				col = "id"
			}
			a.db.Exec(`DELETE FROM `+t+` WHERE `+col+` = ?`, home)
		}
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) handlePassword(w http.ResponseWriter, r *http.Request) {
	var in struct{ Current, New string }
	if !readJSON(w, r, &in) {
		return
	}
	u := userFrom(r)
	if u.viaToken {
		fail(w, 403, "Change the password in the app.")
		return
	}
	var hash string
	a.db.QueryRow(`SELECT pass FROM users WHERE id = ?`, u.ID).Scan(&hash)
	if !checkPassword(hash, in.Current) {
		fail(w, 400, "The current password is wrong.")
		return
	}
	if utf8.RuneCountInString(in.New) < 10 {
		fail(w, 400, "The password needs at least 10 characters.")
		return
	}
	h, _ := hashPassword(in.New)
	a.db.Exec(`UPDATE users SET pass = ? WHERE id = ?`, h, u.ID)
	// sign out everywhere else
	if c, err := r.Cookie(sessionCookie); err == nil {
		a.db.Exec(`DELETE FROM sessions WHERE user_id = ? AND token <> ?`, u.ID, tokenHash(c.Value))
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// --- small helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		fail(w, 400, "Invalid request.")
		return false
	}
	return true
}

func pathID(r *http.Request, name string) int64 {
	n, _ := strconv.ParseInt(r.PathValue(name), 10, 64)
	return n
}

// clean trims, removes control characters (except newlines) and caps the length.
func clean(s string, max int) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, s))
	if utf8.RuneCountInString(s) > max {
		s = string([]rune(s)[:max])
	}
	return s
}

func (a *App) clientIP(r *http.Request) string {
	ip := r.RemoteAddr
	if a.cfg.TrustProx {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			ip = strings.TrimSpace(strings.Split(xff, ",")[0])
		}
	}
	if h, _, err := net.SplitHostPort(ip); err == nil {
		ip = h
	}
	return ip
}

// limiter: at most n attempts per key per window (sign-in, setup).
type limiter struct {
	mu     sync.Mutex
	n      int
	window time.Duration
	hits   map[string][]time.Time
}

func newLimiter(n int, window time.Duration) *limiter {
	return &limiter{n: n, window: window, hits: map[string][]time.Time{}}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := time.Now().Add(-l.window)
	h := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cut) {
			h = append(h, t)
		}
	}
	if len(h) >= l.n {
		l.hits[key] = h
		return false
	}
	l.hits[key] = append(h, time.Now())
	return true
}

func (a *App) secure(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Referrer-Policy", "same-origin")
		hd.Set("X-Frame-Options", "DENY")
		hd.Set("Permissions-Policy", "camera=(self), microphone=(), geolocation=()")
		hd.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' blob: data:; media-src 'self' blob:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; worker-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if r.TLS != nil {
			hd.Set("Strict-Transport-Security", "max-age=31536000")
		}
		h.ServeHTTP(w, r)
	})
}

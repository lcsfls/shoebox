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
}

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

func (a *App) createUser(username, name, pw string) (int64, error) {
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
	res, err := a.db.Exec(`INSERT INTO users(username, name, pass, created_at) VALUES(?,?,?,?)`, username, clean(name, 60), h, now())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return 0, errors.New("That username is taken.")
		}
		return 0, err
	}
	return res.LastInsertId()
}

func (a *App) startSession(w http.ResponseWriter, r *http.Request, uid int64) {
	tok := randomToken(32)
	exp := time.Now().Add(sessionDays * 24 * time.Hour)
	a.db.Exec(`INSERT INTO sessions(token, user_id, expires_at) VALUES(?,?,?)`, tokenHash(tok), uid, exp.Unix())
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: tok, Path: "/", Expires: exp, HttpOnly: true,
		Secure: r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"), SameSite: http.SameSiteLaxMode})
}

func (a *App) currentUser(r *http.Request) *User {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	var u User
	err = a.db.QueryRow(`SELECT u.id, u.username, u.name FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token = ? AND s.expires_at > ?`, tokenHash(c.Value), time.Now().Unix()).Scan(&u.ID, &u.Username, &u.Name)
	if err != nil {
		return nil
	}
	if u.Name == "" {
		u.Name = u.Username
	}
	return &u
}

// auth guards API handlers: a session is required, and changes must come
// from this site (Origin check) as JSON or a form posted by the app.
func (a *App) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := a.currentUser(r)
		if u == nil {
			fail(w, 401, "Please sign in.")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r) {
			fail(w, 403, "Request from another site rejected.")
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
	writeJSON(w, 200, map[string]any{"user": u, "needs_setup": n == 0, "version": version})
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
	id, err := a.createUser(in.Username, in.Name, in.Password)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
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
	rows, err := a.db.Query(`SELECT id, username, name FROM users ORDER BY id`)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		var u User
		rows.Scan(&u.ID, &u.Username, &u.Name)
		out = append(out, u)
	}
	writeJSON(w, 200, out)
}

func (a *App) handleUserAdd(w http.ResponseWriter, r *http.Request) {
	var in struct{ Username, Name, Password string }
	if !readJSON(w, r, &in) {
		return
	}
	if _, err := a.createUser(in.Username, in.Name, in.Password); err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) handleUserDelete(w http.ResponseWriter, r *http.Request) {
	id := pathID(r, "id")
	if id == userFrom(r).ID {
		fail(w, 400, "You can't delete your own account.")
		return
	}
	a.db.Exec(`DELETE FROM users WHERE id = ?`, id)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) handlePassword(w http.ResponseWriter, r *http.Request) {
	var in struct{ Current, New string }
	if !readJSON(w, r, &in) {
		return
	}
	u := userFrom(r)
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

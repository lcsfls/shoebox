package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// Passkeys (WebAuthn): sign in with the phone's or computer's screen lock
// or a security key instead of the password. Browsers only offer them on
// HTTPS (or http://localhost).

const ceremonyCookie = "shoebox_wa"

type ceremony struct {
	data    webauthn.SessionData
	userID  int64
	expires time.Time
}

type ceremonies struct {
	mu sync.Mutex
	m  map[string]ceremony
}

func (c *ceremonies) put(w http.ResponseWriter, r *http.Request, data *webauthn.SessionData, userID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]ceremony{}
	}
	for k, v := range c.m {
		if time.Now().After(v.expires) {
			delete(c.m, k)
		}
	}
	key := randomToken(24)
	c.m[key] = ceremony{*data, userID, time.Now().Add(5 * time.Minute)}
	http.SetCookie(w, &http.Cookie{Name: ceremonyCookie, Value: key, Path: "/api/", MaxAge: 300, HttpOnly: true,
		Secure: r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"), SameSite: http.SameSiteStrictMode})
}

// take returns the ceremony started by this browser, once.
func (c *ceremonies) take(w http.ResponseWriter, r *http.Request) (ceremony, bool) {
	ck, err := r.Cookie(ceremonyCookie)
	http.SetCookie(w, &http.Cookie{Name: ceremonyCookie, Value: "", Path: "/api/", MaxAge: -1, HttpOnly: true})
	if err != nil {
		return ceremony{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[ck.Value]
	delete(c.m, ck.Value)
	if !ok || time.Now().After(v.expires) {
		return ceremony{}, false
	}
	return v, true
}

// webAuthn is set up per request: the relying party is the host name people
// use to open Shoebox (SB_BASE_URL, or the page that asks).
func (a *App) webAuthn(r *http.Request) (*webauthn.WebAuthn, error) {
	origin := a.cfg.BaseURL
	if origin == "" {
		if o := r.Header.Get("Origin"); o != "" && sameOrigin(r) {
			origin = o
		} else {
			origin = a.baseURL(r)
		}
	}
	u, err := url.Parse(origin)
	if err != nil || u.Hostname() == "" {
		return nil, errors.New("can't tell the address of this Shoebox; set SB_BASE_URL")
	}
	return webauthn.New(&webauthn.Config{
		RPID:          u.Hostname(),
		RPDisplayName: "Shoebox",
		RPOrigins:     []string{u.Scheme + "://" + u.Host},
	})
}

type waUser struct {
	id      int64
	handle  []byte
	name    string
	display string
	creds   []webauthn.Credential
}

func (u *waUser) WebAuthnID() []byte                         { return u.handle }
func (u *waUser) WebAuthnName() string                       { return u.name }
func (u *waUser) WebAuthnDisplayName() string                { return u.display }
func (u *waUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

func (a *App) loadWAUser(id int64) (*waUser, error) {
	u := &waUser{id: id}
	var name string
	if err := a.db.QueryRow(`SELECT username, name, handle FROM users WHERE id = ?`, id).Scan(&u.name, &name, &u.handle); err != nil {
		return nil, err
	}
	u.display = name
	if u.display == "" {
		u.display = u.name
	}
	if len(u.handle) == 0 {
		// accounts from before passkeys: give them a random handle now
		u.handle = randomBytes(32)
		a.db.Exec(`UPDATE users SET handle = ? WHERE id = ?`, u.handle, id)
	}
	rows, err := a.db.Query(`SELECT data FROM passkeys WHERE user_id = ?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var data string
		rows.Scan(&data)
		var c webauthn.Credential
		if json.Unmarshal([]byte(data), &c) == nil {
			u.creds = append(u.creds, c)
		}
	}
	return u, nil
}

func (a *App) handlePasskeyRegisterBegin(w http.ResponseWriter, r *http.Request) {
	me := userFrom(r)
	if me.viaToken {
		fail(w, 403, "Add passkeys in the app.")
		return
	}
	wa, err := a.webAuthn(r)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	u, err := a.loadWAUser(me.ID)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	creation, data, err := wa.BeginRegistration(u,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithExclusions(webauthn.Credentials(u.creds).CredentialDescriptors()),
	)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	a.ceremonies.put(w, r, data, me.ID)
	writeJSON(w, 200, creation)
}

func (a *App) handlePasskeyRegisterFinish(w http.ResponseWriter, r *http.Request) {
	me := userFrom(r)
	c, ok := a.ceremonies.take(w, r)
	if !ok || c.userID != me.ID {
		fail(w, 400, "That took too long. Try again.")
		return
	}
	wa, err := a.webAuthn(r)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	u, err := a.loadWAUser(me.ID)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	cred, err := wa.FinishRegistration(u, c.data, r)
	if err != nil {
		fail(w, 400, "The passkey couldn't be checked: "+passkeyError(err))
		return
	}
	name := clean(r.URL.Query().Get("name"), 60)
	if name == "" {
		name = "Passkey"
	}
	data, _ := json.Marshal(cred)
	if _, err := a.db.Exec(`INSERT INTO passkeys(user_id, name, cred_id, data, created_at) VALUES(?,?,?,?,?)`,
		me.ID, name, cred.ID, string(data), now()); err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

type passkeyInfo struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Created  string `json:"created_at"`
	LastUsed string `json:"last_used_at"`
}

func (a *App) handlePasskeys(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.Query(`SELECT id, name, created_at, last_used_at FROM passkeys WHERE user_id = ? ORDER BY id`, userFrom(r).ID)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	defer rows.Close()
	out := []passkeyInfo{}
	for rows.Next() {
		var p passkeyInfo
		rows.Scan(&p.ID, &p.Name, &p.Created, &p.LastUsed)
		out = append(out, p)
	}
	writeJSON(w, 200, out)
}

func (a *App) handlePasskeyDelete(w http.ResponseWriter, r *http.Request) {
	me := userFrom(r)
	if me.viaToken {
		fail(w, 403, "Remove passkeys in the app.")
		return
	}
	a.db.Exec(`DELETE FROM passkeys WHERE id = ? AND user_id = ?`, pathID(r, "id"), me.ID)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) handlePasskeyLoginBegin(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		fail(w, 403, "Request from another site rejected.")
		return
	}
	if !a.limiter.allow(a.clientIP(r)) {
		fail(w, 429, "Too many attempts. Wait a minute.")
		return
	}
	wa, err := a.webAuthn(r)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	assertion, data, err := wa.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationPreferred))
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	a.ceremonies.put(w, r, data, 0)
	writeJSON(w, 200, assertion)
}

func (a *App) handlePasskeyLoginFinish(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		fail(w, 403, "Request from another site rejected.")
		return
	}
	c, ok := a.ceremonies.take(w, r)
	if !ok {
		fail(w, 400, "That took too long. Try again.")
		return
	}
	wa, err := a.webAuthn(r)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	var found *waUser
	user, cred, err := wa.FinishPasskeyLogin(func(rawID, handle []byte) (webauthn.User, error) {
		var id int64
		if err := a.db.QueryRow(`SELECT id FROM users WHERE handle = ?`, handle).Scan(&id); err != nil {
			return nil, errors.New("unknown passkey")
		}
		u, err := a.loadWAUser(id)
		found = u
		return u, err
	}, c.data, r)
	if err != nil || user == nil || found == nil || cred == nil {
		if err != nil {
			log.Printf("passkey sign-in: %s", passkeyError(err))
		}
		fail(w, 401, "This passkey isn't known here. Sign in with your password and add it under Settings → Account.")
		return
	}
	data, _ := json.Marshal(cred)
	a.db.Exec(`UPDATE passkeys SET data = ?, last_used_at = ? WHERE cred_id = ? AND user_id = ?`, string(data), now(), cred.ID, found.id)
	a.startSession(w, r, found.id)
	writeJSON(w, 200, map[string]any{"ok": true})
}

// passkeyError keeps the library's message short and readable.
func passkeyError(err error) string {
	var pe *protocol.Error
	if errors.As(err, &pe) && pe.Details != "" {
		return pe.Details
	}
	return err.Error()
}

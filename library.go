package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Libraries: separate archives, each with its own folder, documents, tags,
// senders and sorting rules, shared with chosen accounts. Library 1 lives in
// SB_ARCHIVE; the others in folders an admin picks. In a library an account
// is an editor (adds and changes documents, tags, rules) or a reader.

type Library struct {
	ID      int64       `json:"id"`
	Name    string      `json:"name"`
	Path    string      `json:"path"`
	Role    string      `json:"role,omitempty"` // the signed-in account's role here
	Docs    int         `json:"docs"`
	Members []libMember `json:"members,omitempty"`
}

type libMember struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	Name     string `json:"name"`
	Role     string `json:"role"`
}

func validLibRole(r string) bool { return r == "editor" || r == "reader" }

// libPath is the folder of a library.
func (a *App) libPath(id int64) (string, error) {
	if id == 1 {
		return a.cfg.Archive, nil
	}
	var p string
	if err := a.db.QueryRow(`SELECT path FROM libraries WHERE id = ?`, id).Scan(&p); err != nil {
		return "", errors.New("no such library")
	}
	if p == "" {
		return "", errors.New("the library has no folder")
	}
	return p, nil
}

// allLibraries with their folders (for the folder watcher and admins).
func (a *App) allLibraries() []Library {
	rows, err := a.db.Query(`SELECT l.id, l.name, l.path, (SELECT count(*) FROM docs d WHERE d.library_id = l.id) FROM libraries l ORDER BY l.id`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Library
	for rows.Next() {
		var l Library
		rows.Scan(&l.ID, &l.Name, &l.Path, &l.Docs)
		if l.ID == 1 {
			l.Path = a.cfg.Archive
		}
		out = append(out, l)
	}
	return out
}

// userLibraries: the libraries an account belongs to, with its role there.
func (a *App) userLibraries(u *User) []Library {
	rows, err := a.db.Query(`SELECT l.id, l.name, l.path, m.role, (SELECT count(*) FROM docs d WHERE d.library_id = l.id AND d.status <> 'missing')
		FROM libraries l JOIN library_members m ON m.library_id = l.id WHERE m.user_id = ? ORDER BY l.id`, u.ID)
	if err != nil {
		return []Library{}
	}
	defer rows.Close()
	out := []Library{}
	for rows.Next() {
		var l Library
		rows.Scan(&l.ID, &l.Name, &l.Path, &l.Role, &l.Docs)
		if l.ID == 1 {
			l.Path = a.cfg.Archive
		}
		if u.Role == "viewer" { // a read-only API token
			l.Role = "reader"
		}
		out = append(out, l)
	}
	return out
}

func (a *App) libRole(u *User, lib int64) string {
	var role string
	if a.db.QueryRow(`SELECT role FROM library_members WHERE library_id = ? AND user_id = ?`, lib, u.ID).Scan(&role) != nil {
		return ""
	}
	if u.Role == "viewer" {
		return "reader"
	}
	return role
}

// currentLib is the library a request is about: the X-Library header (the
// app sends the one picked in the sidebar) or ?library=, otherwise the
// account's first library. edit asks for the editor role there.
func (a *App) currentLib(w http.ResponseWriter, r *http.Request, edit bool) (int64, bool) {
	u := userFrom(r)
	v := r.Header.Get("X-Library")
	if v == "" {
		v = r.URL.Query().Get("library")
	}
	var id int64
	if v != "" {
		id, _ = strconv.ParseInt(v, 10, 64)
	} else if libs := a.userLibraries(u); len(libs) > 0 {
		id = libs[0].ID
	}
	role := ""
	if id > 0 {
		role = a.libRole(u, id)
	}
	if role == "" {
		fail(w, 403, "You don't have access to this library.")
		return 0, false
	}
	if edit && role != "editor" {
		fail(w, 403, "You can only read in this library.")
		return 0, false
	}
	return id, true
}

// docAccess checks the account may see (or change) a document.
func (a *App) docAccess(w http.ResponseWriter, r *http.Request, d *Doc, edit bool) bool {
	role := a.libRole(userFrom(r), d.LibraryID)
	if role == "" {
		fail(w, 404, "No such document.")
		return false
	}
	if edit && role != "editor" {
		fail(w, 403, "You can only read in this library.")
		return false
	}
	return true
}

// loadDoc reads the document of the request and checks access.
func (a *App) loadDoc(w http.ResponseWriter, r *http.Request, edit bool) (*Doc, bool) {
	d, err := a.getDoc(pathID(r, "id"))
	if err != nil {
		fail(w, 404, "No such document.")
		return nil, false
	}
	if !a.docAccess(w, r, d, edit) {
		return nil, false
	}
	return d, true
}

// --- managing libraries (admins) ---

func (a *App) handleLibraries(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, a.userLibraries(userFrom(r)))
}

func (a *App) handleLibrariesAll(w http.ResponseWriter, r *http.Request) {
	libs := a.allLibraries()
	for i := range libs {
		rows, err := a.db.Query(`SELECT u.id, u.username, u.name, m.role FROM library_members m JOIN users u ON u.id = m.user_id WHERE m.library_id = ? ORDER BY u.username`, libs[i].ID)
		if err != nil {
			continue
		}
		libs[i].Members = []libMember{}
		for rows.Next() {
			var m libMember
			rows.Scan(&m.UserID, &m.Username, &m.Name, &m.Role)
			libs[i].Members = append(libs[i].Members, m)
		}
		rows.Close()
	}
	if libs == nil {
		libs = []Library{}
	}
	writeJSON(w, 200, libs)
}

// checkLibraryPath: an absolute folder Shoebox can write to, not its data
// folder and not the folder of another library. Folders inside another
// library's folder are fine; that library's folder watcher leaves them alone.
func (a *App) checkLibraryPath(p string, self int64) (string, error) {
	p = strings.TrimSpace(p)
	if !filepath.IsAbs(p) {
		return "", errors.New("Give the full path of the folder, e.g. /srv/shoebox/family.")
	}
	p = filepath.Clean(p)
	data, _ := filepath.Abs(a.cfg.DataDir)
	if p == data || strings.HasPrefix(p, data+string(filepath.Separator)) || strings.HasPrefix(data, p+string(filepath.Separator)) {
		return "", errors.New("Not the data folder: keep libraries apart from the database.")
	}
	for _, l := range a.allLibraries() {
		if l.ID != self && filepath.Clean(l.Path) == p {
			return "", errors.New("“" + l.Name + "” already uses this folder.")
		}
		if l.ID != self && strings.HasPrefix(filepath.Clean(l.Path), p+string(filepath.Separator)) {
			return "", errors.New("The folder of “" + l.Name + "” is inside this one. Pick a folder next to it or inside it instead.")
		}
		// inside another library's folder: only where it has no documents yet,
		// otherwise they would change library behind its back
		if l.ID != self && strings.HasPrefix(p, filepath.Clean(l.Path)+string(filepath.Separator)) {
			rel, _ := filepath.Rel(filepath.Clean(l.Path), p)
			var n int
			a.db.QueryRow(`SELECT count(*) FROM docs WHERE library_id = ? AND (path = ? OR path LIKE ? ESCAPE '\')`, l.ID, rel,
				strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(filepath.ToSlash(rel))+"/%").Scan(&n)
			if n > 0 {
				return "", fmt.Errorf("“%s” already has %d documents in this folder. Pick a new folder.", l.Name, n)
			}
		}
	}
	if err := os.MkdirAll(p, 0o750); err != nil {
		return "", errors.New("Shoebox can't create or open this folder: " + err.Error())
	}
	probe := filepath.Join(p, ".shoebox-write-test")
	if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
		return "", errors.New("Shoebox can't write into this folder: " + err.Error())
	}
	os.Remove(probe)
	return p, nil
}

func (a *App) handleLibraryCreate(w http.ResponseWriter, r *http.Request) {
	var in struct{ Name, Path string }
	if !readJSON(w, r, &in) {
		return
	}
	name := clean(in.Name, 60)
	if name == "" {
		fail(w, 400, "Give the library a name.")
		return
	}
	p, err := a.checkLibraryPath(in.Path, 0)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	res, err := a.db.Exec(`INSERT INTO libraries(name, path, created_at) VALUES(?,?,?)`, name, p, now())
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	id, _ := res.LastInsertId()
	a.db.Exec(`INSERT INTO library_members(library_id, user_id, role) VALUES(?,?, 'editor')`, id, userFrom(r).ID)
	a.watcher.kick()
	writeJSON(w, 200, map[string]any{"id": id})
}

func (a *App) handleLibraryUpdate(w http.ResponseWriter, r *http.Request) {
	id := pathID(r, "id")
	var in struct {
		Name *string `json:"name"`
		Path *string `json:"path"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	var cur string
	if a.db.QueryRow(`SELECT path FROM libraries WHERE id = ?`, id).Scan(&cur) != nil {
		fail(w, 404, "No such library.")
		return
	}
	if in.Path != nil {
		if id == 1 {
			fail(w, 400, "This library's folder is set with SB_ARCHIVE on the server.")
			return
		}
		var filed int
		a.db.QueryRow(`SELECT count(*) FROM docs WHERE library_id = ? AND status <> 'inbox'`, id).Scan(&filed)
		p, err := a.checkLibraryPath(*in.Path, id)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		if filed > 0 && p != cur {
			fail(w, 400, "The library has documents in its folder. Move the folder on the server yourself and point it here only after that.")
			return
		}
		a.db.Exec(`UPDATE libraries SET path = ? WHERE id = ?`, p, id)
	}
	if in.Name != nil {
		if n := clean(*in.Name, 60); n != "" {
			a.db.Exec(`UPDATE libraries SET name = ? WHERE id = ?`, n, id)
		}
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) handleLibraryDelete(w http.ResponseWriter, r *http.Request) {
	id := pathID(r, "id")
	if id == 1 {
		fail(w, 400, "The first library stays.")
		return
	}
	var n int
	a.db.QueryRow(`SELECT count(*) FROM docs WHERE library_id = ?`, id).Scan(&n)
	if n > 0 {
		fail(w, 400, "Only empty libraries can be removed. Its folder on the server is never deleted.")
		return
	}
	tx, err := a.db.Begin()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	defer tx.Rollback()
	tx.Exec(`DELETE FROM rules WHERE library_id = ?`, id)
	tx.Exec(`DELETE FROM tags WHERE library_id = ?`, id)
	tx.Exec(`DELETE FROM senders WHERE library_id = ?`, id)
	tx.Exec(`DELETE FROM libraries WHERE id = ?`, id)
	tx.Commit()
	writeJSON(w, 200, map[string]any{"ok": true})
}

// handleLibraryMember adds an account, changes its role or (role "") removes it.
func (a *App) handleLibraryMember(w http.ResponseWriter, r *http.Request) {
	id := pathID(r, "id")
	var in struct {
		UserID int64  `json:"user_id"`
		Role   string `json:"role"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	var x int
	if a.db.QueryRow(`SELECT 1 FROM libraries WHERE id = ?`, id).Scan(&x) != nil || a.db.QueryRow(`SELECT 1 FROM users WHERE id = ?`, in.UserID).Scan(&x) != nil {
		fail(w, 404, "No such library or account.")
		return
	}
	if in.Role == "" {
		a.db.Exec(`DELETE FROM library_members WHERE library_id = ? AND user_id = ?`, id, in.UserID)
	} else if validLibRole(in.Role) {
		a.db.Exec(`INSERT INTO library_members(library_id, user_id, role) VALUES(?,?,?) ON CONFLICT(library_id, user_id) DO UPDATE SET role = excluded.role`, id, in.UserID, in.Role)
	} else {
		fail(w, 400, "Unknown role.")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

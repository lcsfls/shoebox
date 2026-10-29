package main

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Libraries: separate archives, each with its own folder, documents, tags,
// senders and sorting rules, shared with chosen accounts. Library 1 is the
// family's (SB_ARCHIVE/family); every account also has a private Home
// (SB_ARCHIVE/home/<username>) that only it sees, where its scans and
// uploads land. Others live in folders an admin picks. In a library an
// account is an editor (adds and changes documents, tags, rules) or a reader.

type Library struct {
	ID      int64       `json:"id"`
	Name    string      `json:"name"`
	Path    string      `json:"path"`
	Role    string      `json:"role,omitempty"` // the signed-in account's role here
	Home    bool        `json:"home,omitempty"` // the signed-in account's private Home
	OwnerID int64       `json:"-"`              // set for a Home: whose it is
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
	var p string
	if err := a.db.QueryRow(`SELECT path FROM libraries WHERE id = ?`, id).Scan(&p); err != nil {
		return "", errors.New("no such library")
	}
	if id == 1 && p == "" {
		return a.cfg.Archive, nil // until it moves into a folder of its own
	}
	if p == "" {
		return "", errors.New("the library has no folder")
	}
	return p, nil
}

// allLibraries with their folders (for the folder watcher and admins).
func (a *App) allLibraries() []Library {
	rows, err := a.db.Query(`SELECT l.id, l.name, l.path, coalesce(l.owner_id, 0), (SELECT count(*) FROM docs d WHERE d.library_id = l.id) FROM libraries l ORDER BY l.id`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Library
	for rows.Next() {
		var l Library
		rows.Scan(&l.ID, &l.Name, &l.Path, &l.OwnerID, &l.Docs)
		if l.ID == 1 && l.Path == "" {
			l.Path = a.cfg.Archive
		}
		out = append(out, l)
	}
	return out
}

// userLibraries: the libraries an account belongs to, with its role there;
// its Home first.
func (a *App) userLibraries(u *User) []Library {
	rows, err := a.db.Query(`SELECT l.id, l.name, l.path, coalesce(l.owner_id, 0), m.role, (SELECT count(*) FROM docs d WHERE d.library_id = l.id AND d.status <> 'missing')
		FROM libraries l JOIN library_members m ON m.library_id = l.id WHERE m.user_id = ? ORDER BY l.owner_id IS NULL, l.id`, u.ID)
	if err != nil {
		return []Library{}
	}
	defer rows.Close()
	out := []Library{}
	for rows.Next() {
		var l Library
		rows.Scan(&l.ID, &l.Name, &l.Path, &l.OwnerID, &l.Role, &l.Docs)
		if l.ID == 1 && l.Path == "" {
			l.Path = a.cfg.Archive
		}
		l.Home = l.OwnerID != 0 && l.OwnerID == u.ID
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
	me := userFrom(r).ID
	libs := []Library{}
	for _, l := range a.allLibraries() {
		if l.OwnerID != 0 && l.OwnerID != me {
			continue // someone else's Home stays private, also from admins
		}
		l.Home = l.OwnerID != 0
		libs = append(libs, l)
	}
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
	writeJSON(w, 200, map[string]any{"root": filepath.Clean(a.cfg.Archive), "libraries": libs})
}

// handleLibraryOwnFolder moves the first library's files from the top of the
// archive folder into a folder of its own (e.g. archive/family), so every
// library is a folder side by side. Shoebox does this by itself on start;
// this is for when that failed.
func (a *App) handleLibraryOwnFolder(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Folder string `json:"folder"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	target, moved, code, err := a.moveFirstLibrary(in.Folder)
	if err != nil {
		fail(w, code, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "path": target, "moved": moved})
}

// moveFirstLibrary: see handleLibraryOwnFolder. Paths in the database are
// relative to the library's folder, so they stay as they are. On failure it
// gives the HTTP status that fits.
func (a *App) moveFirstLibrary(folder string) (string, int, int, error) {
	var cur string
	a.db.QueryRow(`SELECT path FROM libraries WHERE id = 1`).Scan(&cur)
	if cur != "" {
		return "", 0, 400, errors.New("This library already has a folder of its own.")
	}
	name := pathPart(folder)
	if name == "" || strings.HasPrefix(name, ".") || reYearFolder.MatchString(name) {
		return "", 0, 400, errors.New("Pick a folder name like “family” (not a year).")
	}
	root := filepath.Clean(a.cfg.Archive)
	target := filepath.Join(root, name)
	others := map[string]bool{}
	for _, l := range a.allLibraries() {
		if l.ID != 1 {
			others[filepath.Clean(l.Path)] = true
		}
	}
	if others[target] {
		return "", 0, 400, errors.New("Another library uses that folder.")
	}
	if ents, err := os.ReadDir(target); err == nil && len(ents) > 0 {
		return "", 0, 400, errors.New("That folder exists and isn't empty. Pick another name.")
	}
	// a folder that holds other libraries (archive/home) stays where it is
	holdsOther := func(p string) bool {
		for o := range others {
			if o == p || strings.HasPrefix(o, p+string(filepath.Separator)) {
				return true
			}
		}
		return false
	}
	archiveMu.Lock()
	defer archiveMu.Unlock()
	if err := os.MkdirAll(target, 0o750); err != nil {
		return "", 0, 500, errors.New("Can't create the folder: " + err.Error())
	}
	ents, err := os.ReadDir(root)
	if err != nil {
		return "", 0, 500, err
	}
	moved := 0
	for _, e := range ents {
		p := filepath.Join(root, e.Name())
		// leave the folder itself, other libraries, backups and hidden files
		if p == target || holdsOther(p) || strings.HasPrefix(e.Name(), ".") || skipDir(e.Name()) {
			continue
		}
		if err := os.Rename(p, filepath.Join(target, e.Name())); err != nil {
			// put back what moved so far
			for _, b := range ents {
				q := filepath.Join(target, b.Name())
				if _, err := os.Stat(q); err == nil && q != target {
					os.Rename(q, filepath.Join(root, b.Name()))
				}
			}
			return "", 0, 500, errors.New("Moving " + e.Name() + " failed, nothing was changed: " + err.Error())
		}
		moved++
	}
	a.db.Exec(`UPDATE libraries SET path = ? WHERE id = 1`, target)
	log.Printf("library 1 moved into %s (%d entries)", target, moved)
	return target, moved, 200, nil
}

// homeLib is the account's private Home, 0 if it has none.
func (a *App) homeLib(userID int64) int64 {
	var id int64
	a.db.QueryRow(`SELECT id FROM libraries WHERE owner_id = ?`, userID).Scan(&id)
	return id
}

// ensureHome gives an account its Home (archive/home/<username>) if it
// has none yet.
func (a *App) ensureHome(userID int64, username string) (int64, error) {
	if id := a.homeLib(userID); id > 0 {
		return id, nil
	}
	p := filepath.Join(filepath.Clean(a.cfg.Archive), "home", pathPart(username))
	if err := os.MkdirAll(p, 0o750); err != nil {
		return 0, err
	}
	res, err := a.db.Exec(`INSERT INTO libraries(name, path, owner_id, created_at) VALUES('Home', ?, ?, ?)`, p, userID, now())
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	a.db.Exec(`INSERT OR IGNORE INTO library_members(library_id, user_id, role) VALUES(?, ?, 'editor')`, id, userID)
	return id, nil
}

// setupLibraries on start: the family library in a folder of its own
// (archive/family) and a Home for every account.
func (a *App) setupLibraries() {
	var cur string
	a.db.QueryRow(`SELECT path FROM libraries WHERE id = 1`).Scan(&cur)
	if cur == "" {
		if _, _, _, err := a.moveFirstLibrary("family"); err != nil {
			log.Printf("the family library stays at the top of the archive folder: %v", err)
		}
	}
	rows, err := a.db.Query(`SELECT id, username FROM users WHERE id NOT IN (SELECT owner_id FROM libraries WHERE owner_id IS NOT NULL)`)
	if err != nil {
		return
	}
	type acct struct {
		id   int64
		name string
	}
	var todo []acct
	for rows.Next() {
		var x acct
		rows.Scan(&x.id, &x.name)
		todo = append(todo, x)
	}
	rows.Close()
	for _, x := range todo {
		if _, err := a.ensureHome(x.id, x.name); err != nil {
			log.Printf("no Home for %s: %v", x.name, err)
		}
	}
}

// homeOwner: whose Home a library is (0: none).
func (a *App) homeOwner(lib int64) int64 {
	var owner int64
	a.db.QueryRow(`SELECT coalesce(owner_id, 0) FROM libraries WHERE id = ?`, lib).Scan(&owner)
	return owner
}

// sortingLibs: whose rules, senders and filed documents sort a new document.
// A Home has few of its own, so it also uses those of the shared libraries
// its owner edits.
func (a *App) sortingLibs(lib int64) []int64 {
	out := []int64{lib}
	owner := a.homeOwner(lib)
	if owner == 0 {
		return out
	}
	rows, err := a.db.Query(`SELECT l.id FROM libraries l JOIN library_members m ON m.library_id = l.id
		WHERE m.user_id = ? AND m.role = 'editor' AND l.owner_id IS NULL ORDER BY l.id`, owner)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		out = append(out, id)
	}
	return out
}

var reYearFolder = regexp.MustCompile(`^\d{4}$`)

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
	var owner int64
	if a.db.QueryRow(`SELECT path, coalesce(owner_id, 0) FROM libraries WHERE id = ?`, id).Scan(&cur, &owner) != nil || (owner != 0 && owner != userFrom(r).ID) {
		fail(w, 404, "No such library.")
		return
	}
	if in.Path != nil && owner != 0 {
		fail(w, 400, "A Home's folder is archive/home/<username>.")
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
	if a.homeOwner(id) != 0 {
		fail(w, 400, "A Home stays as long as its account.")
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
	if a.homeOwner(id) != 0 {
		fail(w, 400, "A Home stays private: only its owner sees it.")
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

// handleDocMove puts a document into another library: its file goes into
// that library's folder (same year/month path) and its tags and sender are
// matched by name there.
func (a *App) handleDocMove(w http.ResponseWriter, r *http.Request) {
	d, ok := a.loadDoc(w, r, true)
	if !ok {
		return
	}
	var in struct {
		Library int64 `json:"library"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Library == d.LibraryID {
		writeJSON(w, 200, map[string]any{"ok": true})
		return
	}
	if a.libRole(userFrom(r), in.Library) != "editor" {
		fail(w, 403, "You can't add documents to that library.")
		return
	}
	if dup, err := a.docBySHA(in.Library, d.SHA256, false); err == nil && dup != nil {
		fail(w, 409, "That library already has this document.")
		return
	}
	archiveMu.Lock()
	defer archiveMu.Unlock()
	rel := d.Path
	var src, dst, oldRoot string
	if d.Status != "inbox" {
		var err error
		if oldRoot, err = a.libPath(d.LibraryID); err == nil {
			src, err = a.inArchive(d.LibraryID, d.Path)
		}
		newRoot, err2 := a.libPath(in.Library)
		if err != nil || err2 != nil {
			fail(w, 500, "The library folders can't be found.")
			return
		}
		rel = uniquePath(newRoot, d.Path, "")
		if dst, err = a.inArchive(in.Library, rel); err != nil {
			fail(w, 400, err.Error())
			return
		}
		os.MkdirAll(filepath.Dir(dst), 0o750)
		if err := moveFile(src, dst); err != nil {
			fail(w, 500, "Can't move the file: "+err.Error())
			return
		}
	}
	err := func() error {
		tx, err := a.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		sid, err := senderID(tx, in.Library, d.Sender)
		if err != nil {
			return err
		}
		if err := setTags(tx, in.Library, d.ID, d.Tags); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE docs SET library_id = ?, sender_id = ?, path = ?, updated_at = ? WHERE id = ?`, in.Library, sid, rel, now(), d.ID); err != nil {
			return err
		}
		return tx.Commit()
	}()
	if err != nil {
		if dst != "" {
			moveFile(dst, src)
		}
		fail(w, 500, err.Error())
		return
	}
	if src != "" {
		removeEmptyDirs(oldRoot, filepath.Dir(src))
	}
	a.dropCache(d.ID)
	writeJSON(w, 200, map[string]any{"ok": true})
}

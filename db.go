package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

const schema = `
CREATE TABLE IF NOT EXISTS users(
	id INTEGER PRIMARY KEY,
	username TEXT NOT NULL UNIQUE COLLATE NOCASE,
	name TEXT NOT NULL DEFAULT '',
	pass TEXT NOT NULL,
	created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS sessions(
	token TEXT PRIMARY KEY,
	user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	expires_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS senders(
	id INTEGER PRIMARY KEY,
	name TEXT NOT NULL UNIQUE COLLATE NOCASE);
CREATE TABLE IF NOT EXISTS tags(
	id INTEGER PRIMARY KEY,
	name TEXT NOT NULL UNIQUE COLLATE NOCASE,
	color TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS docs(
	id INTEGER PRIMARY KEY,
	status TEXT NOT NULL,                -- inbox | filed | missing
	path TEXT NOT NULL DEFAULT '',       -- inbox: file name in <data>/inbox, filed: path inside the archive
	sha256 TEXT NOT NULL DEFAULT '',     -- of the file as it came in (duplicate check)
	file_sha TEXT NOT NULL DEFAULT '',   -- of the stored file (after OCR and metadata)
	title TEXT NOT NULL DEFAULT '',
	sender_id INTEGER REFERENCES senders(id) ON DELETE SET NULL,
	doc_date TEXT NOT NULL DEFAULT '',   -- YYYY-MM-DD
	amount_cents INTEGER,
	pages INTEGER NOT NULL DEFAULT 0,
	size INTEGER NOT NULL DEFAULT 0,
	mtime INTEGER NOT NULL DEFAULT 0,
	source TEXT NOT NULL DEFAULT '',     -- scan | upload | folder | paperless
	ocr TEXT NOT NULL DEFAULT '',        -- how the text was obtained
	notes TEXT NOT NULL DEFAULT '',
	fields TEXT NOT NULL DEFAULT '{}',   -- extra fields as JSON (e.g. from Paperless custom fields)
	rule TEXT NOT NULL DEFAULT '',       -- name of the sorting rule that matched
	paperless_id INTEGER,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS docs_status_date ON docs(status, doc_date);
CREATE INDEX IF NOT EXISTS docs_sha ON docs(sha256);
CREATE INDEX IF NOT EXISTS docs_file_sha ON docs(file_sha);
CREATE INDEX IF NOT EXISTS docs_path ON docs(path);
CREATE INDEX IF NOT EXISTS docs_paperless ON docs(paperless_id);
CREATE TABLE IF NOT EXISTS doc_tags(
	doc_id INTEGER NOT NULL REFERENCES docs(id) ON DELETE CASCADE,
	tag_id INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
	PRIMARY KEY(doc_id, tag_id));
CREATE TABLE IF NOT EXISTS rules(
	id INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	kind TEXT NOT NULL,                  -- any | all | exact | regex | fuzzy
	pattern TEXT NOT NULL,
	nocase INTEGER NOT NULL DEFAULT 1,
	sender TEXT NOT NULL DEFAULT '',
	tags TEXT NOT NULL DEFAULT '[]',
	title TEXT NOT NULL DEFAULT '',
	position INTEGER NOT NULL DEFAULT 0,
	enabled INTEGER NOT NULL DEFAULT 1);
CREATE TABLE IF NOT EXISTS settings(
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL);
CREATE VIRTUAL TABLE IF NOT EXISTS docs_fts USING fts5(
	title, sender, tags, body,
	tokenize = 'unicode61 remove_diacritics 2');
`

// migrations run once each, in order, after the base schema; PRAGMA
// user_version remembers how many have run.
var migrations = []string{
	// 1: roles, passkeys, API tokens, who added a document, OCR time
	`ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'admin';
	ALTER TABLE users ADD COLUMN handle BLOB;
	ALTER TABLE docs ADD COLUMN added_by INTEGER REFERENCES users(id) ON DELETE SET NULL;
	ALTER TABLE docs ADD COLUMN ocr_ms INTEGER NOT NULL DEFAULT 0;
	CREATE INDEX IF NOT EXISTS docs_created ON docs(created_at);
	CREATE TABLE IF NOT EXISTS api_tokens(
		id INTEGER PRIMARY KEY,
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		name TEXT NOT NULL,
		hash TEXT NOT NULL UNIQUE,
		prefix TEXT NOT NULL,
		readonly INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL,
		last_used_at TEXT NOT NULL DEFAULT '');
	CREATE TABLE IF NOT EXISTS passkeys(
		id INTEGER PRIMARY KEY,
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		name TEXT NOT NULL,
		cred_id BLOB NOT NULL UNIQUE,
		data TEXT NOT NULL,
		created_at TEXT NOT NULL,
		last_used_at TEXT NOT NULL DEFAULT '');
	CREATE VIRTUAL TABLE IF NOT EXISTS docs_vocab USING fts5vocab(docs_fts, 'row');`,

	// 2: libraries. Everything so far goes into library 1 (its folder is
	// SB_ARCHIVE), shared by all accounts; viewers become readers there.
	// Tags and senders get unique names per library, so their tables are
	// rebuilt (runs with foreign keys off, see migrate).
	`CREATE TABLE libraries(
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		path TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL);
	CREATE TABLE library_members(
		library_id INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		role TEXT NOT NULL,
		PRIMARY KEY(library_id, user_id));
	INSERT INTO libraries(id, name, path, created_at) VALUES(1, 'Shared', '', strftime('%Y-%m-%dT%H:%M:%SZ', 'now'));
	INSERT INTO library_members(library_id, user_id, role) SELECT 1, id, CASE role WHEN 'viewer' THEN 'reader' ELSE 'editor' END FROM users;
	UPDATE users SET role = 'member' WHERE role = 'viewer';
	ALTER TABLE docs ADD COLUMN library_id INTEGER NOT NULL DEFAULT 1;
	CREATE INDEX docs_library ON docs(library_id, status, doc_date);
	CREATE TABLE senders_new(
		id INTEGER PRIMARY KEY,
		library_id INTEGER NOT NULL DEFAULT 1,
		name TEXT NOT NULL COLLATE NOCASE,
		UNIQUE(library_id, name));
	INSERT INTO senders_new(id, library_id, name) SELECT id, 1, name FROM senders;
	DROP TABLE senders;
	ALTER TABLE senders_new RENAME TO senders;
	CREATE TABLE tags_new(
		id INTEGER PRIMARY KEY,
		library_id INTEGER NOT NULL DEFAULT 1,
		name TEXT NOT NULL COLLATE NOCASE,
		color TEXT NOT NULL DEFAULT '',
		UNIQUE(library_id, name));
	INSERT INTO tags_new(id, library_id, name, color) SELECT id, 1, name, color FROM tags;
	DROP TABLE tags;
	ALTER TABLE tags_new RENAME TO tags;
	ALTER TABLE rules ADD COLUMN library_id INTEGER NOT NULL DEFAULT 1;`,

	// 3: stamps burnt into documents
	`CREATE TABLE stamps(
		id INTEGER PRIMARY KEY,
		doc_id INTEGER NOT NULL REFERENCES docs(id) ON DELETE CASCADE,
		kind TEXT NOT NULL,
		label TEXT NOT NULL,
		date TEXT NOT NULL,
		note TEXT NOT NULL DEFAULT '',
		page INTEGER NOT NULL,
		user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
		hash TEXT NOT NULL,
		created_at TEXT NOT NULL);
	CREATE INDEX stamps_doc ON stamps(doc_id);`,

	// 4: where a stamp sits, so the next one finds another spot
	`ALTER TABLE stamps ADD COLUMN x REAL NOT NULL DEFAULT 0;
	ALTER TABLE stamps ADD COLUMN y REAL NOT NULL DEFAULT 0;`,

	// 5: letters written in Shoebox
	`CREATE TABLE letters(
		id INTEGER PRIMARY KEY,
		library_id INTEGER NOT NULL,
		status TEXT NOT NULL DEFAULT 'draft',   -- draft | final
		form TEXT NOT NULL DEFAULT 'B',         -- DIN 5008 form A or B
		lang TEXT NOT NULL DEFAULT 'de',
		recipient TEXT NOT NULL DEFAULT '',
		your_ref TEXT NOT NULL DEFAULT '',
		our_ref TEXT NOT NULL DEFAULT '',
		date TEXT NOT NULL DEFAULT '',
		subject TEXT NOT NULL DEFAULT '',
		salutation TEXT NOT NULL DEFAULT '',
		body TEXT NOT NULL DEFAULT '',
		closing TEXT NOT NULL DEFAULT '',
		signature TEXT NOT NULL DEFAULT '',
		enclosures TEXT NOT NULL DEFAULT '',
		doc_id INTEGER REFERENCES docs(id) ON DELETE SET NULL,
		created_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL);
	CREATE INDEX letters_library ON letters(library_id, status, updated_at);`,
	// 6: a private Home library for each account (owner_id); the first library is the family's
	`ALTER TABLE libraries ADD COLUMN owner_id INTEGER REFERENCES users(id) ON DELETE SET NULL;
	UPDATE libraries SET name = 'Family' WHERE id = 1 AND name = 'Shared';`,
}

func migrate(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	if v < len(migrations) {
		// one connection with foreign keys off, so tables can be rebuilt
		// without cascading deletes (the SQLite way to change constraints)
		ctx := context.Background()
		conn, err := db.Conn(ctx)
		if err != nil {
			return err
		}
		defer conn.Close()
		if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
			return err
		}
		for i := v; i < len(migrations); i++ {
			tx, err := conn.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(migrations[i]); err != nil {
				tx.Rollback()
				return fmt.Errorf("database update %d: %w", i+1, err)
			}
			if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
				tx.Rollback()
				return err
			}
			if err := tx.Commit(); err != nil {
				return err
			}
		}
		rows, err := conn.QueryContext(ctx, `PRAGMA foreign_key_check`)
		if err == nil {
			bad := rows.Next()
			rows.Close()
			if bad {
				log.Printf("database update: some references don't match (PRAGMA foreign_key_check)")
			}
		}
		conn.ExecContext(ctx, `PRAGMA foreign_keys = ON`)
	}
	// tags imported before the palette existed carry Paperless' hex colors
	rows, err := db.Query(`SELECT id, color FROM tags WHERE color LIKE '#%'`)
	if err == nil {
		fix := map[int64]string{}
		for rows.Next() {
			var id int64
			var c string
			rows.Scan(&id, &c)
			fix[id] = nearestTagColor(c)
		}
		rows.Close()
		for id, c := range fix {
			db.Exec(`UPDATE tags SET color = ? WHERE id = ?`, c, id)
		}
	}
	return nil
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

// Doc is a document as the API returns it.
type Doc struct {
	ID          int64             `json:"id"`
	Status      string            `json:"status"`
	Path        string            `json:"path"`
	Title       string            `json:"title"`
	Sender      string            `json:"sender"`
	SenderID    int64             `json:"sender_id"`
	Date        string            `json:"date"`
	AmountCents *int64            `json:"amount_cents"`
	Pages       int               `json:"pages"`
	Size        int64             `json:"size"`
	Source      string            `json:"source"`
	OCR         string            `json:"ocr"`
	Notes       string            `json:"notes"`
	Fields      map[string]string `json:"fields"`
	Rule        string            `json:"rule"`
	Tags        []string          `json:"tags"`
	AddedBy     string            `json:"added_by"`
	LibraryID   int64             `json:"library_id"`
	Snippet     string            `json:"snippet,omitempty"`
	CreatedAt   string            `json:"created_at"`
	UpdatedAt   string            `json:"updated_at"`
	SHA256      string            `json:"-"`
	MTime       int64             `json:"-"`
}

const docCols = `d.id, d.status, d.path, d.title, COALESCE(s.name,''), COALESCE(d.sender_id,0), d.doc_date, d.amount_cents,
	d.pages, d.size, d.source, d.ocr, d.notes, d.fields, d.rule, d.created_at, d.updated_at, d.sha256, d.mtime,
	COALESCE((SELECT group_concat(t.name, char(31)) FROM doc_tags dt JOIN tags t ON t.id = dt.tag_id WHERE dt.doc_id = d.id), ''),
	COALESCE((SELECT COALESCE(NULLIF(u.name,''), u.username) FROM users u WHERE u.id = d.added_by), ''), d.library_id`

type scanner interface{ Scan(...any) error }

func scanDoc(row scanner, extra ...any) (*Doc, error) {
	var d Doc
	var amount sql.NullInt64
	var fields, tags string
	dest := []any{&d.ID, &d.Status, &d.Path, &d.Title, &d.Sender, &d.SenderID, &d.Date, &amount,
		&d.Pages, &d.Size, &d.Source, &d.OCR, &d.Notes, &fields, &d.Rule, &d.CreatedAt, &d.UpdatedAt, &d.SHA256, &d.MTime, &tags, &d.AddedBy, &d.LibraryID}
	if err := row.Scan(append(dest, extra...)...); err != nil {
		return nil, err
	}
	if amount.Valid {
		v := amount.Int64
		d.AmountCents = &v
	}
	d.Fields = map[string]string{}
	json.Unmarshal([]byte(fields), &d.Fields)
	d.Tags = []string{}
	if tags != "" {
		d.Tags = strings.Split(tags, "\x1f")
	}
	return &d, nil
}

func (a *App) getDoc(id int64) (*Doc, error) {
	return scanDoc(a.db.QueryRow(`SELECT `+docCols+` FROM docs d LEFT JOIN senders s ON s.id = d.sender_id WHERE d.id = ?`, id))
}

// docBySHA finds a document by the hash of its incoming or stored file.
// Documents whose file went missing count only with withMissing (the
// archive scan uses that to follow files moved by hand).
func (a *App) docBySHA(lib int64, sha string, withMissing bool) (*Doc, error) {
	q := `SELECT ` + docCols + ` FROM docs d LEFT JOIN senders s ON s.id = d.sender_id WHERE (d.sha256 = ?1 OR d.file_sha = ?1) AND d.library_id = ?2`
	if !withMissing {
		q += ` AND d.status <> 'missing'`
	}
	return scanDoc(a.db.QueryRow(q+` ORDER BY d.status = 'missing' LIMIT 1`, sha, lib))
}

// senderID finds or creates a sender. Empty name: no sender.
func senderID(tx execer, lib int64, name string) (sql.NullInt64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return sql.NullInt64{}, nil
	}
	if _, err := tx.Exec(`INSERT INTO senders(library_id, name) VALUES(?, ?) ON CONFLICT(library_id, name) DO NOTHING`, lib, name); err != nil {
		return sql.NullInt64{}, err
	}
	var id int64
	err := tx.QueryRow(`SELECT id FROM senders WHERE library_id = ? AND name = ?`, lib, name).Scan(&id)
	return sql.NullInt64{Int64: id, Valid: err == nil}, err
}

type execer interface {
	Exec(string, ...any) (sql.Result, error)
	QueryRow(string, ...any) *sql.Row
}

func setTags(tx execer, lib, docID int64, tags []string) error {
	if _, err := tx.Exec(`DELETE FROM doc_tags WHERE doc_id = ?`, docID); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" || seen[strings.ToLower(t)] {
			continue
		}
		seen[strings.ToLower(t)] = true
		if _, err := tx.Exec(`INSERT INTO tags(library_id, name) VALUES(?, ?) ON CONFLICT(library_id, name) DO NOTHING`, lib, t); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO doc_tags(doc_id, tag_id) SELECT ?, id FROM tags WHERE library_id = ? AND name = ?`, docID, lib, t); err != nil {
			return err
		}
	}
	return nil
}

// reindex refreshes the full-text entry of a document. body == nil keeps the stored text.
func reindex(tx execer, id int64, body *string) error {
	var title, sender, tags string
	err := tx.QueryRow(`SELECT d.title, COALESCE(s.name,''),
		COALESCE((SELECT group_concat(t.name, ' ') FROM doc_tags dt JOIN tags t ON t.id = dt.tag_id WHERE dt.doc_id = d.id), '')
		FROM docs d LEFT JOIN senders s ON s.id = d.sender_id WHERE d.id = ?`, id).Scan(&title, &sender, &tags)
	if err != nil {
		return err
	}
	text := ""
	if body != nil {
		text = *body
	} else {
		tx.QueryRow(`SELECT body FROM docs_fts WHERE rowid = ?`, id).Scan(&text)
	}
	if _, err := tx.Exec(`DELETE FROM docs_fts WHERE rowid = ?`, id); err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO docs_fts(rowid, title, sender, tags, body) VALUES(?,?,?,?,?)`, id, title, sender, tags, text)
	return err
}

func (a *App) docText(id int64) string {
	var s string
	a.db.QueryRow(`SELECT body FROM docs_fts WHERE rowid = ?`, id).Scan(&s)
	return s
}

// NewDoc is what the processing pipeline hands over.
type NewDoc struct {
	LibraryID   int64
	AddedBy     int64
	OCRMillis   int64
	Status      string
	Path        string
	SHA256      string
	FileSHA     string
	Title       string
	Sender      string
	Date        string
	AmountCents *int64
	Pages       int
	Size        int64
	MTime       int64
	Source      string
	OCR         string
	Notes       string
	Fields      map[string]string
	Rule        string
	Tags        []string
	Text        string
	PaperlessID int64
}

func (a *App) insertDoc(n NewDoc) (int64, error) {
	tx, err := a.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if n.LibraryID == 0 {
		n.LibraryID = 1
	}
	sid, err := senderID(tx, n.LibraryID, n.Sender)
	if err != nil {
		return 0, err
	}
	fields, _ := json.Marshal(n.Fields)
	if n.Fields == nil {
		fields = []byte("{}")
	}
	var amount, pid any
	if n.AmountCents != nil {
		amount = *n.AmountCents
	}
	if n.PaperlessID != 0 {
		pid = n.PaperlessID
	}
	ts := now()
	res, err := tx.Exec(`INSERT INTO docs(status, path, sha256, file_sha, title, sender_id, doc_date, amount_cents, pages, size, mtime,
		source, ocr, notes, fields, rule, paperless_id, created_at, updated_at, added_by, ocr_ms, library_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		n.Status, n.Path, n.SHA256, n.FileSHA, n.Title, sid, n.Date, amount, n.Pages, n.Size, n.MTime,
		n.Source, n.OCR, n.Notes, string(fields), n.Rule, pid, ts, ts, nullID(n.AddedBy), n.OCRMillis, n.LibraryID)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	if err := setTags(tx, n.LibraryID, id, n.Tags); err != nil {
		return 0, err
	}
	if err := reindex(tx, id, &n.Text); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// DocPatch is an edit from the review or document screen.
type DocPatch struct {
	Title       *string   `json:"title"`
	Sender      *string   `json:"sender"`
	Date        *string   `json:"date"`
	AmountCents *int64    `json:"amount_cents"`
	NoAmount    bool      `json:"no_amount"`
	Notes       *string   `json:"notes"`
	Tags        *[]string `json:"tags"`
}

func (a *App) patchDoc(id int64, p DocPatch) error {
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var lib int64
	if err := tx.QueryRow(`SELECT library_id FROM docs WHERE id = ?`, id).Scan(&lib); err != nil {
		return err
	}
	if p.Title != nil {
		tx.Exec(`UPDATE docs SET title = ? WHERE id = ?`, clean(*p.Title, 200), id)
	}
	if p.Sender != nil {
		sid, err := senderID(tx, lib, clean(*p.Sender, 120))
		if err != nil {
			return err
		}
		tx.Exec(`UPDATE docs SET sender_id = ? WHERE id = ?`, sid, id)
	}
	if p.Date != nil {
		d := strings.TrimSpace(*p.Date)
		if d != "" && !validDate(d) {
			return errors.New("Date must look like 2026-10-24.")
		}
		tx.Exec(`UPDATE docs SET doc_date = ? WHERE id = ?`, d, id)
	}
	if p.NoAmount {
		tx.Exec(`UPDATE docs SET amount_cents = NULL WHERE id = ?`, id)
	} else if p.AmountCents != nil {
		tx.Exec(`UPDATE docs SET amount_cents = ? WHERE id = ?`, *p.AmountCents, id)
	}
	if p.Notes != nil {
		tx.Exec(`UPDATE docs SET notes = ? WHERE id = ?`, clean(*p.Notes, 10000), id)
	}
	if p.Tags != nil {
		if err := setTags(tx, lib, id, *p.Tags); err != nil {
			return err
		}
	}
	tx.Exec(`UPDATE docs SET updated_at = ? WHERE id = ?`, now(), id)
	if err := reindex(tx, id, nil); err != nil {
		return err
	}
	return tx.Commit()
}

func nullID(id int64) any {
	if id <= 0 {
		return nil
	}
	return id
}

func validDate(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

// ftsQuery turns what someone types into an FTS5 query: every word must
// appear, words are prefix-matched, quotes keep phrases together.
func ftsQuery(q string) string {
	var parts []string
	var cur strings.Builder
	inQuote := false
	flush := func(quoted bool) {
		w := strings.TrimSpace(cur.String())
		cur.Reset()
		if w == "" {
			return
		}
		w = strings.ReplaceAll(w, `"`, "")
		if quoted {
			parts = append(parts, `"`+w+`"`)
		} else {
			parts = append(parts, `"`+w+`"*`)
		}
	}
	for _, r := range q {
		switch {
		case r == '"':
			flush(inQuote)
			inQuote = !inQuote
		case !inQuote && (r == ' ' || r == '\t' || r == ',' || r == ';'):
			flush(false)
		case r == '*' || r == '(' || r == ')' || r == ':' || r == '^' || r == '-' && cur.Len() == 0:
			// operators are not for normal people; treat as separators
			if !inQuote {
				flush(false)
			}
		default:
			cur.WriteRune(r)
		}
	}
	flush(inQuote)
	return strings.Join(parts, " AND ")
}

type DocQuery struct {
	Library                                   int64 // required: one library at a time
	Q, Status, Year, Month, Tag, Sender, Sort string
	Limit, Offset                             int
}

func (a *App) listDocs(f DocQuery) ([]*Doc, int, error) {
	where := []string{`d.library_id = ?`}
	args := []any{f.Library}
	from := `docs d LEFT JOIN senders s ON s.id = d.sender_id`
	cols := docCols + `, ''`
	if fq := ftsQuery(f.Q); fq != "" {
		from = `docs_fts JOIN docs d ON d.id = docs_fts.rowid LEFT JOIN senders s ON s.id = d.sender_id`
		cols = docCols + `, snippet(docs_fts, 3, char(2), char(3), '…', 12)`
		where = append(where, `docs_fts MATCH ?`)
		args = append(args, fq)
	}
	switch f.Status {
	case "inbox":
		where = append(where, `d.status = 'inbox'`)
	case "all":
		where = append(where, `d.status <> 'missing'`)
	default:
		where = append(where, `d.status = 'filed'`)
	}
	if f.Year != "" {
		if f.Year == "undated" {
			where = append(where, `d.doc_date = ''`)
		} else {
			p := f.Year
			if f.Month != "" {
				p += "-" + f.Month
			}
			where = append(where, `d.doc_date LIKE ?`)
			args = append(args, p+"%")
		}
	}
	if f.Tag != "" {
		where = append(where, `EXISTS (SELECT 1 FROM doc_tags dt JOIN tags t ON t.id = dt.tag_id WHERE dt.doc_id = d.id AND t.name = ?)`)
		args = append(args, f.Tag)
	}
	if f.Sender != "" {
		where = append(where, `s.name = ?`)
		args = append(args, f.Sender)
	}
	w := strings.Join(where, " AND ")
	var total int
	if err := a.db.QueryRow(`SELECT count(*) FROM `+from+` WHERE `+w, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := `d.doc_date DESC, d.id DESC`
	switch f.Sort {
	case "added":
		order = `d.id DESC`
	case "relevance":
		if f.Q != "" {
			order = `bm25(docs_fts, 8.0, 4.0, 4.0, 1.0)`
		}
	case "sender":
		order = `s.name COLLATE NOCASE, d.doc_date DESC`
	case "amount":
		order = `d.amount_cents IS NULL, d.amount_cents DESC`
	}
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 60
	}
	rows, err := a.db.Query(fmt.Sprintf(`SELECT %s FROM %s WHERE %s ORDER BY %s LIMIT %d OFFSET %d`, cols, from, w, order, f.Limit, f.Offset), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []*Doc{}
	for rows.Next() {
		var snip string
		d, err := scanDoc(rows, &snip)
		if err != nil {
			return nil, 0, err
		}
		d.Snippet = snip
		out = append(out, d)
	}
	return out, total, rows.Err()
}

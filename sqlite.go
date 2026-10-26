//go:build !nosqlite

package main

import (
	"database/sql"

	"github.com/ncruces/go-sqlite3/driver"
	"github.com/ncruces/go-sqlite3/ext/fts5"
)

// openDB opens the SQLite database with full-text search (FTS5).
// It must live on a local disk: SQLite's file locking is not reliable on NFS.
func openDB(path string) (*sql.DB, error) {
	db, err := driver.Open("file:"+path+"?_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)&_pragma=journal_mode(wal)&_pragma=synchronous(normal)", fts5.Register)
	if err != nil {
		return nil, err
	}
	// Every connection runs its own SQLite instance; a few are plenty.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	return db, nil
}

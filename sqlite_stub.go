//go:build nosqlite

package main

import (
	"database/sql"
	"errors"
)

// Build tag nosqlite: compiles without the SQLite driver (for quick checks only).
func openDB(path string) (*sql.DB, error) {
	return nil, errors.New("built without SQLite (tag nosqlite)")
}

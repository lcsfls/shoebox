package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Database backups into the archive folder (for example the NAS share):
// a consistent copy made with VACUUM INTO on the local disk, then copied
// over. The PDFs waiting in Review are copied along, since they only live
// on the local disk until they are filed.

const backupDir = ".shoebox-backup" // hidden: the archive scan skips it

var backupMu sync.Mutex

type backupFile struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Time string `json:"time"`
}

func (a *App) backupPath() string { return filepath.Join(a.cfg.Archive, backupDir) }

func (a *App) backup() (backupFile, error) {
	backupMu.Lock()
	defer backupMu.Unlock()
	dir := a.backupPath()
	if err := os.MkdirAll(filepath.Join(dir, "inbox"), 0o750); err != nil {
		return backupFile{}, fmt.Errorf("can't write into the archive folder: %w", err)
	}
	name := "shoebox-" + time.Now().Format("2006-01-02-1504") + ".db"
	local := filepath.Join(a.cfg.DataDir, "tmp", name)
	os.Remove(local)
	if _, err := a.db.Exec(`VACUUM INTO ?`, local); err != nil {
		return backupFile{}, fmt.Errorf("copying the database: %w", err)
	}
	defer os.Remove(local)
	dst := filepath.Join(dir, name)
	if err := streamCopy(local, dst); err != nil {
		return backupFile{}, fmt.Errorf("writing the backup: %w", err)
	}
	// documents in Review: copy what isn't there yet, drop what was filed since
	inbox := filepath.Join(a.cfg.DataDir, "inbox")
	want := map[string]bool{}
	if ents, err := os.ReadDir(inbox); err == nil {
		for _, e := range ents {
			if e.IsDir() {
				continue
			}
			want[e.Name()] = true
			target := filepath.Join(dir, "inbox", e.Name())
			if info, err := e.Info(); err == nil {
				if st, err := os.Stat(target); err == nil && st.Size() == info.Size() {
					continue
				}
			}
			if err := streamCopy(filepath.Join(inbox, e.Name()), target); err != nil {
				log.Printf("backup of the review inbox: %v", err)
			}
		}
	}
	if ents, err := os.ReadDir(filepath.Join(dir, "inbox")); err == nil {
		for _, e := range ents {
			if !want[e.Name()] {
				os.Remove(filepath.Join(dir, "inbox", e.Name()))
			}
		}
	}
	a.pruneBackups()
	st, _ := os.Stat(dst)
	a.db.Exec(`INSERT INTO settings(key, value) VALUES('last_backup', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, now())
	return backupFile{Name: name, Size: st.Size(), Time: st.ModTime().UTC().Format(time.RFC3339)}, nil
}

func streamCopy(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".part"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func (a *App) listBackups() []backupFile {
	ents, err := os.ReadDir(a.backupPath())
	out := []backupFile{}
	if err != nil {
		return out
	}
	for _, e := range ents {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "shoebox-") || !strings.HasSuffix(e.Name(), ".db") {
			continue
		}
		if info, err := e.Info(); err == nil {
			out = append(out, backupFile{e.Name(), info.Size(), info.ModTime().UTC().Format(time.RFC3339)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name })
	return out
}

func (a *App) pruneBackups() {
	keep := a.settings().BackupKeep
	if keep < 1 {
		keep = 14
	}
	for i, b := range a.listBackups() {
		if i >= keep {
			os.Remove(filepath.Join(a.backupPath(), b.Name))
		}
	}
}

// maybeBackup runs the daily backup when it is due (called every hour).
func (a *App) maybeBackup() {
	if !a.settings().BackupDaily {
		return
	}
	var last string
	a.db.QueryRow(`SELECT value FROM settings WHERE key = 'last_backup'`).Scan(&last)
	if t, err := time.Parse(time.RFC3339, last); err == nil && time.Since(t) < 23*time.Hour {
		return
	}
	if _, err := a.backup(); err != nil {
		log.Printf("daily backup: %v", err)
	}
}

func (a *App) handleBackups(w http.ResponseWriter, r *http.Request) {
	var last string
	a.db.QueryRow(`SELECT value FROM settings WHERE key = 'last_backup'`).Scan(&last)
	writeJSON(w, 200, map[string]any{"folder": a.backupPath(), "last": last, "files": a.listBackups()})
}

func (a *App) handleBackupNow(w http.ResponseWriter, r *http.Request) {
	b, err := a.backup()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, b)
}

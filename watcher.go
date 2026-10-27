package main

import (
	"context"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Watcher looks through the archive folder every few minutes. PDFs someone
// copied there (for example straight onto the NAS) are added to the library,
// moved or renamed ones are followed, and deleted ones disappear from it.
// It polls instead of using inotify, because inotify doesn't see changes
// made on an NFS server.
type Watcher struct {
	app     *App
	kickCh  chan struct{}
	once    sync.Once
	mu      sync.Mutex
	last    time.Time
	lastErr string
	found   int
}

func (w *Watcher) ch() chan struct{} {
	w.once.Do(func() { w.kickCh = make(chan struct{}, 1) })
	return w.kickCh
}

// kick asks for a scan now.
func (w *Watcher) kick() {
	select {
	case w.ch() <- struct{}{}:
	default:
	}
}

func (w *Watcher) loop(ctx context.Context) {
	time.Sleep(5 * time.Second)
	w.scan()
	for {
		mins := w.app.settings().WatchMinutes
		wait := time.Duration(mins) * time.Minute
		if mins <= 0 {
			wait = 24 * time.Hour
		}
		select {
		case <-ctx.Done():
			return
		case <-w.ch():
		case <-time.After(wait):
			if mins <= 0 {
				continue
			}
		}
		w.scan()
	}
}

type known struct {
	id     int64
	size   int64
	mtime  int64
	status string
}

func skipDir(name string) bool {
	return strings.HasPrefix(name, ".") || name == "@eaDir" || name == "#recycle" || name == "#snapshot" ||
		name == "lost+found" || name == "$RECYCLE.BIN" || name == "System Volume Information"
}

func (w *Watcher) scan() {
	libs := w.app.allLibraries()
	roots := map[string]bool{}
	for _, l := range libs {
		roots[filepath.Clean(l.Path)] = true
	}
	found, missing := 0, 0
	var errs []string
	for _, l := range libs {
		n, m, err := w.scanLibrary(l, roots)
		found += n
		missing += m
		if err != "" {
			if len(libs) > 1 {
				err = l.Name + ": " + err
			}
			errs = append(errs, err)
		}
	}
	if found > 0 || missing > 0 {
		log.Printf("archive scan: %d new, %d gone", found, missing)
	}
	w.setStatus(found, strings.Join(errs, "; "))
}

// scanLibrary looks through one library's folder. Folders that belong to
// another library (one may sit inside another) are left to that one.
func (w *Watcher) scanLibrary(l Library, roots map[string]bool) (int, int, string) {
	a := w.app
	root := filepath.Clean(l.Path)
	if _, err := os.Stat(root); err != nil {
		return 0, 0, "The folder is not reachable: " + err.Error()
	}
	rows, err := a.db.Query(`SELECT id, path, size, mtime, status FROM docs WHERE library_id = ? AND status IN ('filed','missing')`, l.ID)
	if err != nil {
		return 0, 0, err.Error()
	}
	byPath := map[string]known{}
	for rows.Next() {
		var k known
		var p string
		rows.Scan(&k.id, &p, &k.size, &k.mtime, &k.status)
		byPath[p] = k
	}
	rows.Close()

	archiveMu.Lock()
	seen := map[string]bool{}
	var newFiles []string
	walkErr := filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return err
			}
			return nil // unreadable folder: skip it
		}
		if e.IsDir() {
			if p != root && (skipDir(e.Name()) || roots[filepath.Clean(p)]) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(e.Name(), ".") || !strings.EqualFold(filepath.Ext(e.Name()), ".pdf") {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		info, err := e.Info()
		if err != nil {
			return nil
		}
		seen[rel] = true
		k, ok := byPath[rel]
		switch {
		case !ok:
			// still being copied? wait for the next round
			if time.Since(info.ModTime()) > 20*time.Second {
				newFiles = append(newFiles, rel)
			}
		case k.status == "missing":
			a.db.Exec(`UPDATE docs SET status = 'filed', size = ?, mtime = ? WHERE id = ?`, info.Size(), info.ModTime().UnixNano(), k.id)
		case k.size != info.Size() || k.mtime != info.ModTime().UnixNano():
			// changed in place (e.g. edited elsewhere): refresh size and text
			a.db.Exec(`UPDATE docs SET size = ?, mtime = ?, updated_at = ? WHERE id = ?`, info.Size(), info.ModTime().UnixNano(), now(), k.id)
			go a.refreshText(k.id, p)
		}
		return nil
	})
	missing := 0
	if walkErr == nil {
		for p, k := range byPath {
			if !seen[p] && k.status == "filed" {
				a.db.Exec(`UPDATE docs SET status = 'missing' WHERE id = ?`, k.id)
				missing++
			}
		}
	}
	archiveMu.Unlock()

	for _, rel := range newFiles {
		a.jobs.addFolder(l.ID, rel)
	}
	if walkErr != nil {
		return len(newFiles), missing, walkErr.Error()
	}
	return len(newFiles), missing, ""
}

func (w *Watcher) setStatus(found int, errMsg string) {
	w.mu.Lock()
	w.last, w.found, w.lastErr = time.Now(), found, errMsg
	w.mu.Unlock()
}

func (w *Watcher) status() (time.Time, int, string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.last, w.found, w.lastErr
}

func (a *App) refreshText(id int64, abs string) {
	text := pdfText(context.Background(), abs)
	if text == "" {
		return
	}
	a.db.Exec(`UPDATE docs SET pages = ? WHERE id = ?`, pdfPages(context.Background(), abs), id)
	reindex(a.db, id, &text)
	a.dropCache(id)
}

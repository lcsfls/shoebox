package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Editing the pages of a document afterwards: new order, turned or removed
// pages, and pages replaced by a newly cropped picture (which gets its own
// text layer). The untouched original stays in <data>/originals.

type pagePlan struct {
	Src     int    `json:"src"`     // page number in the current file
	Rotate  int    `json:"rotate"`  // 0, 90, 180, 270 (clockwise)
	Replace string `json:"replace"` // name of an uploaded image that takes the page's place
}

func (a *App) handleDocPages(w http.ResponseWriter, r *http.Request) {
	d, ok := a.loadDoc(w, r, true)
	if !ok {
		return
	}
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		fail(w, 400, "Couldn't read the request.")
		return
	}
	var plan []pagePlan
	if err := json.Unmarshal([]byte(r.FormValue("plan")), &plan); err != nil || len(plan) == 0 {
		fail(w, 400, "A document needs at least one page.")
		return
	}
	if len(plan) > 500 {
		fail(w, 400, "Too many pages.")
		return
	}
	src, err := a.docFile(d)
	if err != nil {
		fail(w, 404, "The file is not reachable.")
		return
	}
	dir, err := os.MkdirTemp("", "pages")
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	defer os.RemoveAll(dir)

	ctx := r.Context()
	langs := a.settings().Languages
	var parts []string
	for i, p := range plan {
		if p.Rotate%90 != 0 || p.Rotate < 0 || p.Rotate >= 360 {
			fail(w, 400, "Pages turn in steps of 90°.")
			return
		}
		out := filepath.Join(dir, fmt.Sprintf("part-%04d.pdf", i))
		if p.Replace != "" {
			files := r.MultipartForm.File[p.Replace]
			if len(files) != 1 {
				fail(w, 400, "The new picture of a page is missing.")
				return
			}
			sub := filepath.Join(dir, fmt.Sprintf("img-%04d", i))
			os.Mkdir(sub, 0o700)
			paths, _, err := a.saveParts(r, p.Replace, sub, sniffImage)
			if err != nil {
				fail(w, 400, err.Error())
				return
			}
			if _, err := ocrImages(ctx, paths, langs, out); err != nil {
				fail(w, 500, "Reading the new page failed: "+err.Error())
				return
			}
		} else {
			if p.Src < 1 || p.Src > d.Pages {
				fail(w, 400, "Unknown page "+strconv.Itoa(p.Src)+".")
				return
			}
			if _, err := run(ctx, time.Minute, "qpdf", "--empty", "--pages", src, strconv.Itoa(p.Src), "--", out); err != nil {
				fail(w, 500, "Couldn't take the page out: "+err.Error())
				return
			}
		}
		if p.Rotate != 0 {
			rot := out + ".rot.pdf"
			if _, err := run(ctx, time.Minute, "qpdf", "--rotate=+"+strconv.Itoa(p.Rotate), out, rot); err != nil {
				fail(w, 500, "Couldn't turn the page: "+err.Error())
				return
			}
			os.Rename(rot, out)
		}
		parts = append(parts, out)
	}
	merged := filepath.Join(dir, "document.pdf")
	if len(parts) == 1 {
		// qpdf keeps it a proper single-page file
		err = os.Rename(parts[0], merged)
	} else {
		err = mergePDFs(ctx, parts, merged)
	}
	if err != nil {
		fail(w, 500, "Couldn't put the pages together: "+err.Error())
		return
	}
	text := pdfText(ctx, merged)

	// keep the first version, once
	orig := filepath.Join(a.cfg.DataDir, "originals", d.SHA256+".pdf")
	if d.SHA256 == "" {
		orig = filepath.Join(a.cfg.DataDir, "originals", fmt.Sprintf("doc-%d.pdf", d.ID))
	}
	if _, err := os.Stat(orig); os.IsNotExist(err) {
		if err := streamCopy(src, orig); err != nil {
			log.Printf("keeping the original of %d: %v", d.ID, err)
		}
	}

	if d.Status != "inbox" {
		if err := writeMeta(ctx, merged, d); err != nil {
			log.Printf("metadata for %d: %v", d.ID, err)
		}
	}
	archiveMu.Lock()
	err = streamCopy(merged, src)
	archiveMu.Unlock()
	if err != nil {
		fail(w, 500, "Couldn't save the document: "+err.Error())
		return
	}
	fsha, size, _ := fileSHA(src)
	var mtime int64
	if st, err := os.Stat(src); err == nil {
		mtime = st.ModTime().UnixNano()
	}
	a.db.Exec(`UPDATE docs SET pages = ?, size = ?, mtime = ?, file_sha = ?, updated_at = ? WHERE id = ?`,
		pdfPages(ctx, src), size, mtime, fsha, now(), d.ID)
	reindex(a.db, d.ID, &text)
	a.dropCache(d.ID)
	nd, _ := a.getDoc(d.ID)
	writeJSON(w, 200, nd)
}

// sniffImage accepts the pictures the page editor sends.
func sniffImage(name string, head []byte) (string, bool) {
	switch http.DetectContentType(head) {
	case "image/jpeg":
		return ".jpg", true
	case "image/png":
		return ".png", true
	}
	return "", false
}

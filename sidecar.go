package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Sidecar files (Settings → General): next to every filed PDF a .md with
// the same name holding what Shoebox knows about it, the summary and
// deadline too, readable in any file manager or notes app (Obsidian …)
// without Shoebox. Front matter in YAML, then the summary and the notes.

func sidecarPath(pdf string) string { return strings.TrimSuffix(pdf, filepath.Ext(pdf)) + ".md" }

func sidecarText(d *Doc) string {
	q := strconv.Quote
	list := func(xs []string) string {
		out := make([]string, len(xs))
		for i, x := range xs {
			out[i] = q(x)
		}
		return "[" + strings.Join(out, ", ") + "]"
	}
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: %s\n", q(d.Title))
	if d.Sender != "" {
		fmt.Fprintf(&b, "sender: %s\n", q(d.Sender))
	}
	if d.Date != "" {
		fmt.Fprintf(&b, "date: %s\n", d.Date)
	}
	if d.AmountCents != nil {
		fmt.Fprintf(&b, "amount: %d.%02d\n", *d.AmountCents/100, abs64(*d.AmountCents%100))
	}
	fmt.Fprintf(&b, "tags: %s\n", list(d.Tags))
	if len(d.Keywords) > 0 {
		fmt.Fprintf(&b, "keywords: %s\n", list(d.Keywords))
	}
	if d.Due != "" {
		fmt.Fprintf(&b, "due: %s\n", d.Due)
	}
	if d.Action != "" {
		fmt.Fprintf(&b, "action: %s\n", q(d.Action))
	}
	fmt.Fprintf(&b, "pdf: %s\n", q(filepath.Base(d.Path)))
	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "# %s\n", d.Title)
	if d.Summary != "" {
		fmt.Fprintf(&b, "\n%s\n", d.Summary)
	}
	if d.Notes != "" {
		fmt.Fprintf(&b, "\n## Notes\n\n%s\n", d.Notes)
	}
	return b.String()
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// writeSidecar refreshes the .md next to a filed PDF (only when the setting is on).
func (a *App) writeSidecar(pdf string, id int64) {
	if !a.settings().Sidecar {
		return
	}
	d, err := a.getDoc(id)
	if err != nil {
		return
	}
	os.WriteFile(sidecarPath(pdf), []byte(sidecarText(d)), 0o640)
}

// moveSidecar takes the .md along when its PDF moves (whatever the setting:
// one written earlier shouldn't be left behind).
func moveSidecar(from, to string) {
	if _, err := os.Stat(sidecarPath(from)); err == nil {
		moveFile(sidecarPath(from), sidecarPath(to))
	}
}

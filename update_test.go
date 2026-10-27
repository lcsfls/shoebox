package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNewerVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v0.2.1", "v0.2.0", true}, {"v0.10.0", "v0.9.9", true}, {"v1.0.0", "v0.99.0", true},
		{"v0.2.0", "v0.2.0", false}, {"v0.1.9", "v0.2.0", false}, {"v0.3.0", "dev", false}, {"v0.3.0", "ci-abc", false}, {"latest", "v0.1.0", false},
	}
	for _, c := range cases {
		if got := newerVersion(c.a, c.b); got != c.want {
			t.Errorf("newerVersion(%s, %s) = %v", c.a, c.b, got)
		}
	}
}

// a stand-in program that only answers "version"
func fakeBinary(v string) []byte { return []byte("#!/bin/sh\necho " + v + "\n") }

func setupUpdate(t *testing.T, newBin []byte, sums string) (applyEnv, string, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	os.MkdirAll(filepath.Join(data, "update"), 0o755)
	bin := filepath.Join(dir, "shoebox")
	os.WriteFile(bin, fakeBinary("v0.2.0"), 0o755)
	name := "shoebox-linux-" + runtime.GOARCH
	if sums == "" {
		h := sha256.Sum256(newBin)
		sums = hex.EncodeToString(h[:]) + "  " + name + "\n0000  other\n"
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0.3.0/SHA256SUMS":
			fmt.Fprint(w, sums)
		case "/v0.3.0/" + name:
			w.Write(newBin)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	os.WriteFile(filepath.Join(data, "update", "request.json"), []byte(`{"version":"v0.3.0"}`), 0o644)
	e := applyEnv{dataDir: data, binPath: bin, download: srv.URL, restart: func() error { return nil },
		healthy: func(want string) error { return nil }, logf: t.Logf}
	return e, data, srv
}

func status(t *testing.T, data string) updateStatus {
	var s updateStatus
	b, _ := os.ReadFile(filepath.Join(data, "update", "status.json"))
	json.Unmarshal(b, &s)
	return s
}

func TestApplyUpdate(t *testing.T) {
	// works
	e, data, _ := setupUpdate(t, fakeBinary("v0.3.0"), "")
	if err := applyUpdate(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if v := currentBinaryVersion(e.binPath); v != "v0.3.0" {
		t.Errorf("installed %q", v)
	}
	if s := status(t, data); s.State != "done" || s.From != "v0.2.0" {
		t.Errorf("status %+v", s)
	}
	if _, err := os.Stat(filepath.Join(data, "update", "request.json")); !os.IsNotExist(err) {
		t.Error("request not consumed")
	}

	// damaged download: nothing changes
	e, data, _ = setupUpdate(t, fakeBinary("v0.3.0"), "0123456789012345678901234567890123456789012345678901234567890123  shoebox-linux-"+runtime.GOARCH+"\n")
	if err := applyUpdate(context.Background(), e); err == nil {
		t.Fatal("damaged download accepted")
	}
	if v := currentBinaryVersion(e.binPath); v != "v0.2.0" {
		t.Errorf("after a damaged download: %q", v)
	}
	if s := status(t, data); s.State != "failed" {
		t.Errorf("status %+v", s)
	}

	// doesn't start: back to the old program
	e, data, _ = setupUpdate(t, fakeBinary("v0.3.0"), "")
	restarts := 0
	e.restart = func() error { restarts++; return nil }
	e.healthy = func(string) error { return errors.New("no answer") }
	if err := applyUpdate(context.Background(), e); err == nil {
		t.Fatal("expected a rollback")
	}
	if v := currentBinaryVersion(e.binPath); v != "v0.2.0" || restarts != 2 {
		t.Errorf("after rollback: %q, %d restarts", v, restarts)
	}
	if s := status(t, data); s.State != "rolled_back" {
		t.Errorf("status %+v", s)
	}

	// a program that claims another version is refused
	e, _, _ = setupUpdate(t, fakeBinary("v6.6.6"), "")
	if err := applyUpdate(context.Background(), e); err == nil {
		t.Fatal("wrong version accepted")
	}
}

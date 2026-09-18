package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0}, {"v1.2.3", "1.2.3", 0}, {"1.10", "1.9", 1}, {"1.2", "1.2.1", -1}, {"2.0.0", "1.99.99", 1},
		{"1.0.0-rc1", "1.0.0", -1}, {"1.0.0", "1.0.0-rc1", 1}, {"1.0.0-rc2", "1.0.0-rc1", 1},
	}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	for _, v := range []string{"dev", "", "ci-abc1234", "v", "main"} {
		if !IsDev(v) {
			t.Errorf("IsDev(%q) should be true", v)
		}
	}
	for _, v := range []string{"1.0.0", "v2.3", "1.0.0-rc1"} {
		if IsDev(v) {
			t.Errorf("IsDev(%q) should be false", v)
		}
	}
	if ValidRepo("owner") || ValidRepo("a/b/c") || !ValidRepo("jxburros/GWatch") {
		t.Error("ValidRepo misclassifies")
	}
}

func TestCheckDownloadSwap(t *testing.T) {
	payload := []byte("#!/bin/sh\necho new\n")
	sum := sha256.Sum256(payload)
	mux := http.NewServeMux()
	var srvURL string
	mux.HandleFunc("/repos/acme/gwatch/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Errorf("missing accept header")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"tag_name":"v2.5.0","html_url":"https://example.com/rel","body":"notes","published_at":"2026-09-01T10:00:00Z","assets":[
			{"name":"gwatch-linux-amd64.sha256","size":70,"browser_download_url":"` + srvURL + `/dl/gwatch-linux-amd64.sha256"},
			{"name":"gwatch-linux-amd64","size":` + strconv.Itoa(len(payload)) + `,"browser_download_url":"` + srvURL + `/dl/gwatch-linux-amd64"},
			{"name":"gwatch-windows-amd64.exe","size":5,"browser_download_url":"` + srvURL + `/dl/win"}]}`))
	})
	mux.HandleFunc("/repos/acme/empty/releases/latest", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) })
	mux.HandleFunc("/dl/gwatch-linux-amd64", func(w http.ResponseWriter, r *http.Request) { w.Write(payload) })
	mux.HandleFunc("/dl/gwatch-linux-amd64.sha256", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(hex.EncodeToString(sum[:]) + "  gwatch-linux-amd64\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	srvURL = srv.URL

	c := &Client{APIBase: srv.URL, HTTP: srv.Client(), GOOS: "linux", GOARCH: "amd64"}
	info, err := c.Check(context.Background(), "acme/gwatch", "2.4.1")
	if err != nil {
		t.Fatal(err)
	}
	if !info.UpdateAvailable || info.LatestVersion != "2.5.0" || info.AssetName != "gwatch-linux-amd64" || info.ReleaseURL != "https://example.com/rel" || info.PublishedAt == nil {
		t.Fatalf("info: %+v", info)
	}
	if info2, _ := c.Check(context.Background(), "acme/gwatch", "2.5.0"); info2.UpdateAvailable {
		t.Fatal("same version should not be an update")
	}
	if info3, _ := c.Check(context.Background(), "acme/gwatch", "dev"); !info3.UpdateAvailable || !info3.CurrentIsDev {
		t.Fatal("dev builds should always see the release")
	}
	if _, err := c.Check(context.Background(), "acme/empty", "1.0"); err == nil || !strings.Contains(err.Error(), "no releases") {
		t.Fatalf("expected no-releases error, got %v", err)
	}
	if _, err := c.Check(context.Background(), "bad repo", "1.0"); err == nil {
		t.Fatal("expected invalid repo error")
	}
	win := &Client{APIBase: srv.URL, HTTP: srv.Client(), GOOS: "windows", GOARCH: "amd64"}
	if wi, _ := win.Check(context.Background(), "acme/gwatch", "1.0"); wi.AssetName != "gwatch-windows-amd64.exe" {
		t.Fatalf("windows asset: %+v", wi)
	}
	arm := &Client{APIBase: srv.URL, HTTP: srv.Client(), GOOS: "linux", GOARCH: "arm64"}
	if ai, _ := arm.Check(context.Background(), "acme/gwatch", "1.0"); ai.AssetURL != "" {
		t.Fatalf("arm64 should have no asset: %+v", ai)
	}

	dir := t.TempDir()
	exe := filepath.Join(dir, "gwatch")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	newPath, err := c.Download(context.Background(), info, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := Swap(exe, newPath); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(exe)
	old, _ := os.ReadFile(exe + ".old")
	if string(got) != string(payload) || string(old) != "old" {
		t.Fatalf("swap: exe=%q old=%q", got, old)
	}
	if !DirWritable(exe) {
		t.Fatal("temp dir should be writable")
	}
	// checksum mismatch is rejected
	bad := info
	bad.AssetURL = srv.URL + "/dl/win"
	bad.AssetSize = 0
	mux.HandleFunc("/dl/win", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("hello")) })
	mux.HandleFunc("/dl/win.sha256", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(strings.Repeat("0", 64))) })
	if _, err := c.Download(context.Background(), bad, dir); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("expected checksum error, got %v", err)
	}
	if _, err := c.Download(context.Background(), model_noAsset(), dir); err != ErrNoAsset {
		t.Fatalf("expected ErrNoAsset, got %v", err)
	}
}

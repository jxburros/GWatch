package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func probeWith(t *testing.T, cfg config) (string, error) {
	t.Helper()
	return probeServer(context.Background(), cfg)
}

func TestProbeKnowsAGWatchFromSomethingElse(t *testing.T) {
	isolate(t)
	health := `{"serviceRunning":true,"schedulerRunning":true,"now":"2026-10-23T00:00:00Z"}`
	mux := http.NewServeMux()
	mux.HandleFunc("/gw/api/health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(health)) })
	mux.HandleFunc("/paused/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"serviceRunning":true,"schedulerRunning":false}`))
	})
	mux.HandleFunc("/other/api/health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"status":"ok"}`)) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if line, err := probeWith(t, config{server: srv.URL + "/gw"}); err != nil || !strings.Contains(line, "GWatch is answering") {
		t.Errorf("a GWatch: %q %v", line, err)
	}
	if line, err := probeWith(t, config{server: srv.URL + "/paused"}); err != nil || !strings.Contains(line, "paused") {
		t.Errorf("a GWatch whose checks are paused is still a GWatch: %q %v", line, err)
	}
	if _, err := probeWith(t, config{server: srv.URL + "/other"}); err == nil || !strings.Contains(err.Error(), "not a GWatch") {
		t.Errorf("something else that answers: %v", err)
	}
	if _, err := probeWith(t, config{server: srv.URL + "/nothing"}); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("a 404: %v", err)
	}
	if _, err := probeWith(t, config{server: ""}); err == nil {
		t.Error("no server should be an error")
	}
	// A plain-HTTP GWatch addressed as HTTPS.
	if _, err := probeWith(t, config{server: "https://" + strings.TrimPrefix(srv.URL, "http://") + "/gw"}); err == nil || !strings.Contains(err.Error(), "http://") {
		t.Errorf("HTTPS to an HTTP server should say to use http://: %v", err)
	}
}

func TestProbeExplainsAnUntrustedCertificate(t *testing.T) {
	isolate(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"schedulerRunning":true}`))
	}))
	defer srv.Close()
	if _, err := probeWith(t, config{server: srv.URL}); err == nil || !strings.Contains(err.Error(), "--insecure") {
		t.Errorf("a self-signed certificate should say how to accept it: %v", err)
	}
	if _, err := probeWith(t, config{server: srv.URL, insecure: true}); err != nil {
		t.Errorf("accepted explicitly: %v", err)
	}
}

func TestProbeAndInstallLeaveAResultForTheSetupProgram(t *testing.T) {
	dir := isolate(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"schedulerRunning":true}`))
	}))
	defer srv.Close()
	old := resultFile
	t.Cleanup(func() { resultFile = old; resultTarget = nil })
	resultFile = filepath.Join(dir, "result.ini")

	if err := runProbe(config{server: srv.URL}); err != nil {
		t.Fatal(err)
	}
	if got := readResult(t, resultFile); !strings.Contains(got, "ok=1") || !strings.Contains(got, "GWatch is answering") {
		t.Errorf("probe result:\n%s", got)
	}

	resultTarget = &config{server: "https://g:7230", pairedAs: "nas", pairedNode: 7}
	noteInstallResult(nil, "running")
	got := readResult(t, resultFile)
	for _, want := range []string{"[result]", "ok=1", "name=nas", "url=https://g:7230/#/nodes/7", "service=running"} {
		if !strings.Contains(got, want) {
			t.Errorf("install result lacks %q:\n%s", want, got)
		}
	}
	noteInstallResult(os.ErrDeadlineExceeded, "")
	if got := readResult(t, resultFile); !strings.Contains(got, "ok=0") || strings.Count(got, "\n") > 6 {
		t.Errorf("a failure, on one line:\n%s", got)
	}
}

func readResult(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		u := make([]uint16, 0, len(b)/2)
		for i := 2; i+1 < len(b); i += 2 {
			u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
		}
		return string(utf16.Decode(u))
	}
	return string(b)
}

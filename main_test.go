package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
)

func TestRunHTTPCheckSuccess(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	res, err := runHTTPCheck(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success, got failure: %s", res.Message)
	}
}

func TestRunDNSCheckSuccess(t *testing.T) {
	res, err := runDNSCheck(context.Background(), "localhost")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected DNS success, got failure: %s", res.Message)
	}
}

func TestRunPingCheckSuccess(t *testing.T) {
	original := pingCommand
	t.Cleanup(func() { pingCommand = original })

	pingCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "exit 0")
	}

	res, err := runPingCheck(context.Background(), "localhost", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected ping success, got failure: %s", res.Message)
	}
}

func TestCheckHandlerRejectsWrongMethod(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/check", nil)
	rr := httptest.NewRecorder()

	checkHandler(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
}

func TestCheckHandlerReturnsResult(t *testing.T) {
	original := pingCommand
	t.Cleanup(func() { pingCommand = original })
	pingCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "exit 0")
	}

	body := `{"type":"ping","target":"localhost","timeoutSeconds":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/check", strings.NewReader(body))
	rr := httptest.NewRecorder()

	checkHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var res checkResult
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success response")
	}
}

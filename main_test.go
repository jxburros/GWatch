package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func TestRunHTTPCheckSuccess(t *testing.T) {
	t.Setenv("GWATCH_ALLOW_PRIVATE_HTTP_TARGETS", "true")

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

func TestRunHTTPCheckRejectsLocalhost(t *testing.T) {
	_, err := runHTTPCheck(context.Background(), "http://127.0.0.1")
	if err == nil {
		t.Fatal("expected localhost/private IP rejection")
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
		return helperPingCommand(ctx, 0)
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
		return helperPingCommand(ctx, 0)
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

func TestCheckHandlerRejectsUnknownFields(t *testing.T) {
	body := `{"type":"dns","target":"localhost","unexpected":"field"}`
	req := httptest.NewRequest(http.MethodPost, "/api/check", strings.NewReader(body))
	rr := httptest.NewRecorder()

	checkHandler(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestRunCheckValidationAndDefaultTimeout(t *testing.T) {
	t.Run("empty target", func(t *testing.T) {
		_, err := runCheck(context.Background(), checkRequest{Type: checkTypeDNS, Target: "  "})
		if err == nil {
			t.Fatal("expected error for empty target")
		}
	})

	t.Run("unsupported type", func(t *testing.T) {
		_, err := runCheck(context.Background(), checkRequest{Type: checkType("smtp"), Target: "example.com"})
		if err == nil {
			t.Fatal("expected error for unsupported type")
		}
	})

	t.Run("default timeout for ping", func(t *testing.T) {
		original := pingCommand
		t.Cleanup(func() { pingCommand = original })

		var gotTimeout string
		pingCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
			for i := 0; i < len(args)-1; i++ {
				if args[i] == "-W" || args[i] == "-w" {
					gotTimeout = args[i+1]
					break
				}
			}
			return helperPingCommand(ctx, 0)
		}

		_, err := runCheck(context.Background(), checkRequest{Type: checkTypePing, Target: "example.com"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		expected := "5"
		if runtimeIsWindows() {
			expected = "5000"
		}
		if gotTimeout != expected {
			t.Fatalf("expected timeout arg %q, got %q", expected, gotTimeout)
		}
	})
}

func helperPingCommand(ctx context.Context, exitCode int) *exec.Cmd {
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestPingHelperProcess", "--", strconv.Itoa(exitCode))
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
	return cmd
}

func TestPingHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	args := os.Args
	sep := -1
	for i, a := range args {
		if a == "--" {
			sep = i
			break
		}
	}
	if sep == -1 || sep+1 >= len(args) {
		fmt.Fprintln(os.Stderr, "missing helper exit code")
		os.Exit(2)
	}

	code, err := strconv.Atoi(args[sep+1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid helper exit code")
		os.Exit(2)
	}
	os.Exit(code)
}

func runtimeIsWindows() bool {
	return os.PathSeparator == '\\'
}

package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// The agent is the one part of GWatch that runs on a machine nobody is
// watching, installed by someone who may never open a terminal on it again.
// Everything here is a thing that, if it broke, would break out of reach.

// ---- pairing ----

// TestPairStoresTokenAndReports covers the whole enrolment: the code is sent,
// the token that comes back is kept where the service will find it, and a
// first reading goes out under that token — which is what turns "the code was
// accepted" into "this machine is being watched".
func TestPairStoresTokenAndReports(t *testing.T) {
	var paired, reported bool
	var sentToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agents/pair":
			paired = true
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["code"] != "ABCDEFGH" {
				t.Errorf("server received code %q, want the tidied ABCDEFGH", body["code"])
			}
			if body["hostname"] != "chosen-name" {
				t.Errorf("server received hostname %q, want the --name override", body["hostname"])
			}
			if body["version"] != version {
				t.Errorf("server received version %q, want %q", body["version"], version)
			}
			w.Write([]byte(`{"token":"gwa_secret","agent":{"name":"chosen-name"}}`))
		case "/ingest/metrics":
			reported = true
			sentToken = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Errorf("unexpected request to %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	tokenPath := filepath.Join(t.TempDir(), "agent-token")
	t.Setenv("GWATCH_AGENT_TOKEN_FILE", tokenPath)

	cfg := config{server: srv.URL, code: " abcd-efgh ", name: "chosen-name", interval: time.Minute}
	if err := pair(&cfg); err != nil {
		t.Fatalf("pair: %v", err)
	}
	if !paired || !reported {
		t.Fatalf("paired=%v reported=%v; pairing must also prove the token works", paired, reported)
	}
	if sentToken != "gwa_secret" {
		t.Errorf("first reading used token %q, want the one pairing returned", sentToken)
	}
	if cfg.token != "gwa_secret" || cfg.code != "" {
		t.Errorf("after pairing cfg carries token=%q code=%q; an install must carry on as a token install", cfg.token, cfg.code)
	}

	// The token has to be on disk, or the service installed a moment later —
	// which is given no arguments a person typed — has nothing to report with.
	stored, err := readStoredToken()
	if err != nil {
		t.Fatalf("read stored token: %v", err)
	}
	if stored != "gwa_secret" {
		t.Errorf("stored token = %q, want gwa_secret", stored)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(tokenPath)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Errorf("token file mode = %04o, want 0600: it is a credential", mode)
		}
	}
}

// TestPairRefusesAndExplains checks the refusals someone setting a machine up
// actually hits, each of which has to say what to do rather than what failed.
func TestPairRefusesAndExplains(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"that pairing code has expired"}`))
	}))
	defer srv.Close()
	t.Setenv("GWATCH_AGENT_TOKEN_FILE", filepath.Join(t.TempDir(), "agent-token"))

	for _, tc := range []struct {
		name string
		cfg  config
		want string
	}{
		{"no server", config{code: "ABCD-EFGH"}, "server URL is required"},
		{"no code", config{server: "https://gwatch.lan:7230"}, "pairing code is required"},
		{"rejected by the server", config{server: srv.URL, code: "ABCD-EFGH"}, "that pairing code has expired"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			err := pair(&cfg)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}

// TestPairSurvivesAnUnsavableToken: the code is spent the moment the server
// accepts it, so a token that cannot be written must still reach the person
// who is standing there — losing it means going back for another code.
func TestPairSurvivesAnUnsavableToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/agents/pair" {
			w.Write([]byte(`{"token":"gwa_secret","agent":{"name":"nas"}}`))
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	// A directory where the token file's own parent cannot be created.
	blocked := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GWATCH_AGENT_TOKEN_FILE", filepath.Join(blocked, "sub", "agent-token"))

	cfg := config{server: srv.URL, code: "ABCD-EFGH", interval: time.Minute}
	if err := pair(&cfg); err != nil {
		t.Fatalf("pairing must not fail because the token could not be saved: %v", err)
	}
	if cfg.token != "gwa_secret" {
		t.Errorf("token = %q; it must still be in hand", cfg.token)
	}
}

func TestTidyCode(t *testing.T) {
	for in, want := range map[string]string{
		" abcd-efgh ": "ABCDEFGH",
		"abcd efgh":   "ABCDEFGH",
		"ABCD-EFGH":   "ABCDEFGH",
		"a.b,c/d":     "ABCD",
		"":            "",
	} {
		if got := tidyCode(in); got != want {
			t.Errorf("tidyCode(%q) = %q, want %q", in, got, want)
		}
	}
}

// ---- reporting ----

// TestSendReportsRejectionsUsefully: a token the server will never accept must
// not read like a network blip in a log nobody is watching.
func TestSendReportsRejectionsUsefully(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   string
	}{
		{http.StatusUnauthorized, "register the machine again"},
		{http.StatusInternalServerError, "the server answered HTTP 500"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			w.Write([]byte("no"))
		}))
		cfg := config{server: srv.URL, token: "gwa_x", interval: time.Minute}
		err := send(context.Background(), newClient(cfg), cfg, model.HostMetrics{})
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("HTTP %d gave %v, want something containing %q", tc.status, err, tc.want)
		}
	}
}

// TestNextBackoff: an unreachable server must be retried patiently and
// forever, without the wait running away.
func TestNextBackoff(t *testing.T) {
	interval := time.Minute
	got := nextBackoff(0, interval)
	if got != interval {
		t.Errorf("first backoff = %s, want the interval %s", got, interval)
	}
	for i := 0; i < 20; i++ {
		got = nextBackoff(got, interval)
	}
	if got != maxBackoff {
		t.Errorf("backoff settled at %s, want the %s cap", got, maxBackoff)
	}
}

// TestJitteredStaysInRange guards the spread that keeps a fleet from acting in
// lockstep: it must never shorten a wait to nothing, which would turn a
// six-hourly update check into a hot loop against GitHub.
func TestJitteredStaysInRange(t *testing.T) {
	base := time.Minute
	for i := 0; i < 500; i++ {
		got := jittered(base, base/10)
		if got < base/2 || got > base+base/10 {
			t.Fatalf("jittered(%s) = %s, outside the allowed spread", base, got)
		}
	}
	if got := jittered(base, 0); got != base {
		t.Errorf("jittered with no window = %s, want exactly %s", got, base)
	}
	// The update check's own spread, which is the one that matters for a
	// fleet installed by a single script.
	for i := 0; i < 500; i++ {
		got := jittered(updateCheckInterval, updateJitter)
		if got < updateCheckInterval-updateJitter/2 || got > updateCheckInterval+updateJitter/2 {
			t.Fatalf("update check jitter produced %s", got)
		}
	}
}

// ---- configuration and the installed service ----

// TestValidateExplainsWhatIsMissing: these messages are read by someone whose
// machine is not reporting, so each has to name the fix.
func TestValidateExplainsWhatIsMissing(t *testing.T) {
	for _, tc := range []struct {
		name, cmd string
		cfg       config
		want      string
	}{
		{"no token", "run", config{server: "https://x", interval: time.Minute}, "token is required"},
		{"no server", "run", config{token: "t", interval: time.Minute}, "server URL is required"},
		{"interval too small", "run", config{token: "t", server: "https://x", interval: time.Second}, "at least"},
		{"bad listen", "serve", config{token: "t", listen: "nonsense"}, "must be host:port"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.validate(tc.cmd)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("validate = %v, want something containing %q", err, tc.want)
			}
		})
	}
	ok := config{token: "t", server: "https://x", interval: time.Minute}
	if err := ok.validate("run"); err != nil {
		t.Errorf("a complete configuration was refused: %v", err)
	}
}

// TestServiceArgsCarryEverySetting is the test for the failure mode where a
// machine is set up carefully by hand and then the installed service quietly
// runs with different settings, because the command line it was registered
// with dropped one.
func TestServiceArgsCarryEverySetting(t *testing.T) {
	cfg := config{
		server: "https://gwatch.lan:7230", token: "gwa_x", interval: 30 * time.Second,
		insecure: true, name: "nas", autoUpdate: false, repo: "someone/fork",
	}
	args := strings.Join(cfg.serviceArgs("install"), " ")
	for _, want := range []string{
		"run", "--token gwa_x", "--server https://gwatch.lan:7230", "--interval 30s",
		"--insecure", "--name nas", "--auto-update=false", "--repo someone/fork",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("service arguments %q are missing %q", args, want)
		}
	}

	// The defaults are the other half: an agent left on automatic updates must
	// not have that written onto its command line as though it were a choice,
	// and the stock repository is not worth repeating.
	plain := config{server: "https://x", token: "t", interval: time.Minute, autoUpdate: true, repo: defaultRepo}
	pargs := strings.Join(plain.serviceArgs("install"), " ")
	if strings.Contains(pargs, "auto-update") || strings.Contains(pargs, "--repo") {
		t.Errorf("default service arguments should stay quiet, got %q", pargs)
	}

	// serve mode listens rather than reports, so it must not be registered
	// with a server and an interval it does not use.
	serveArgs := strings.Join(config{listen: "0.0.0.0:9713", token: "t", server: "https://x"}.serviceArgs("serve"), " ")
	if !strings.Contains(serveArgs, "serve") || !strings.Contains(serveArgs, "--listen 0.0.0.0:9713") {
		t.Errorf("serve arguments = %q", serveArgs)
	}
	if strings.Contains(serveArgs, "--server") {
		t.Errorf("serve arguments should not carry a server: %q", serveArgs)
	}
}

// TestBaseURLNeverDowngrades: the URL carries a credential, so a bare host
// must become HTTPS rather than HTTP.
func TestBaseURLNeverDowngrades(t *testing.T) {
	for in, want := range map[string]string{
		"gwatch.lan:7230":          "https://gwatch.lan:7230",
		"  gwatch.lan:7230/  ":     "https://gwatch.lan:7230",
		"http://gwatch.lan:7230":   "http://gwatch.lan:7230",
		"https://gwatch.lan:7230/": "https://gwatch.lan:7230",
	} {
		if got := (config{server: in}).baseURL(); got != want {
			t.Errorf("baseURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEnvBoolLeavesTheDefaultOnNonsense(t *testing.T) {
	for _, tc := range []struct {
		value string
		def   bool
		want  bool
	}{
		{"", true, true},
		{"off", true, false},
		{"FALSE", true, false},
		{"0", true, false},
		{"on", false, true},
		{"yes", false, true},
		// A misspelling must never be read as "off": an agent that silently
		// stopped updating itself is the failure this whole feature exists to
		// prevent.
		{"maybe", true, true},
		{"nope", true, true},
	} {
		t.Setenv("GWATCH_TEST_BOOL", tc.value)
		if got := envBool("GWATCH_TEST_BOOL", tc.def); got != tc.want {
			t.Errorf("envBool(%q, %v) = %v, want %v", tc.value, tc.def, got, tc.want)
		}
	}
}

// ---- serve mode ----

// TestServeNeedsTheToken: serve mode is the one shape in which something can
// reach the agent, so the door has to be shut to everyone without the token.
func TestServeNeedsTheToken(t *testing.T) {
	prg := &program{cfg: config{token: "gwa_secret", listen: "127.0.0.1:0"}, mode: "serve"}
	srv := httptest.NewServer(prg.metricsHandler())
	defer srv.Close()

	for _, tc := range []struct {
		name, auth string
		want       int
	}{
		{"no token", "", http.StatusUnauthorized},
		{"wrong token", "Bearer nope", http.StatusUnauthorized},
		{"right token", "Bearer gwa_secret", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", srv.URL+"/metrics", nil)
			if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			io.Copy(io.Discard, resp.Body)
			if resp.StatusCode != tc.want {
				t.Errorf("GET /metrics %s = %d, want %d", tc.name, resp.StatusCode, tc.want)
			}
		})
	}
}

package main

// End-to-end tests: this agent's own code against a real GWatch server (the
// internal/api handler over an httptest listener, with a real engine and a
// throwaway database), so that what the two sides say to each other is tested
// as it is spoken rather than as each side imagines the other.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jxburros/GWatch/internal/api"
	"github.com/jxburros/GWatch/internal/checks"
	"github.com/jxburros/GWatch/internal/engine"
	"github.com/jxburros/GWatch/internal/logging"
	"github.com/jxburros/GWatch/internal/mailer"
	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store/storetest"
)

// gwatchServer starts a real GWatch with no users, which the API treats as an
// administrator's first run, so the test can register and rotate freely.
func gwatchServer(t *testing.T) *httptest.Server {
	t.Helper()
	st := storetest.Open(t)
	lg, _ := logging.New("", nil)
	run := func(ctx context.Context, c model.Check, o checks.Options) model.Result {
		return model.Result{Timestamp: time.Now(), Success: true, Status: model.StatusUp, Attempts: 1}
	}
	send := func(ctx context.Context, s model.SMTPSettings, m mailer.Message) error { return nil }
	eng := engine.New(st, lg, engine.Options{Version: "test", Run: run, Send: send})
	if err := eng.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Stop)
	dir := t.TempDir()
	srv := &api.Server{Engine: eng, Store: st, Log: lg, Version: "test", DataDir: dir, BackupDir: filepath.Join(dir, "backups"),
		Web: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// adminCall is a request to GWatch's own API, as the web UI makes it.
func adminCall(t *testing.T, ts *httptest.Server, method, path string, body, out any) int {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, ts.URL+path, rdr)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if out != nil && resp.StatusCode < 300 {
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("%s %s: %v (%s)", method, path, err, data)
		}
	}
	return resp.StatusCode
}

// pairedAgent registers a machine in GWatch and sets this agent up with its
// token in agent.json, the way pairing leaves it.
func pairedAgent(t *testing.T, ts *httptest.Server) (config, model.Agent, string) {
	t.Helper()
	dir := isolate(t)
	var reg struct {
		Token string      `json:"token"`
		Agent model.Agent `json:"agent"`
	}
	if code := adminCall(t, ts, "POST", "/api/agents", map[string]any{"name": "nas"}, &reg); code != 201 {
		t.Fatalf("register: %d", code)
	}
	writeFile(t, filepath.Join(dir, "agent.json"), fmt.Sprintf(`{"server":%q,"token":%q,"interval":"1m"}`, ts.URL, reg.Token))
	cfg, problems, _ := baseConfig()
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	if cfg.token != reg.Token || cfg.tokenSource != srcFile {
		t.Fatalf("the agent did not take its token from agent.json: %+v", cfg)
	}
	return cfg, reg.Agent, reg.Token
}

func agentNamed(t *testing.T, ts *httptest.Server, id int64) model.Agent {
	t.Helper()
	var agents []model.Agent
	adminCall(t, ts, "GET", "/api/agents", nil, &agents)
	for _, a := range agents {
		if a.ID == id {
			return a
		}
	}
	t.Fatalf("agent %d is not listed", id)
	return model.Agent{}
}

func liveReading() model.HostMetrics {
	cpu := 12.0
	return model.HostMetrics{Hostname: "nas", OS: "linux", Arch: "amd64", Timestamp: time.Now(),
		CPU: model.HostCPU{Cores: 2, UsagePct: &cpu}, Memory: model.HostMemory{TotalBytes: 100, UsedBytes: 10, UsedPct: 10}}
}

// Rotating a token from GWatch, all the way round: the administrator rotates,
// the agent is handed the new token when it next asks for its settings, writes
// it into agent.json before using it, reports with it — which retires the old
// one — and a restart finds the new token in the file.
func TestRotationEndToEnd(t *testing.T) {
	ts := gwatchServer(t)
	cfg, agent, oldTok := pairedAgent(t, ts)
	ctx := context.Background()
	client := newClient(cfg)
	p := &program{cfg: cfg, mode: "run"}
	rt := newRuntime(cfg)

	var rot struct {
		Token string `json:"token"`
	}
	if code := adminCall(t, ts, "POST", fmt.Sprintf("/api/agents/%d/rotate", agent.ID), map[string]any{}, &rot); code != 200 || rot.Token == "" {
		t.Fatalf("rotate: %d", code)
	}
	if !agentNamed(t, ts, agent.ID).RotationPending {
		t.Fatal("GWatch should show the rotation as waiting")
	}

	// Until the agent has the new token, the old one keeps working: no gap.
	c := cfg
	c.token = rt.currentToken()
	if err := send(ctx, client, c, liveReading()); err != nil {
		t.Fatalf("the old token should work while the rotation waits: %v", err)
	}

	if _, err := p.checkInOnce(ctx, rt, client); err != nil {
		t.Fatalf("check-in: %v", err)
	}
	if rt.currentToken() != rot.Token {
		t.Fatalf("the agent did not take up the new token")
	}
	if saved, _ := readStoredToken(); saved != rot.Token {
		t.Fatalf("agent.json should hold the new token before it is used, has %q", saved)
	}

	// The first request with the new token is what retires the old one.
	c.token = rt.currentToken()
	if err := send(ctx, client, c, liveReading()); err != nil {
		t.Fatalf("a reading with the new token: %v", err)
	}
	if agentNamed(t, ts, agent.ID).RotationPending {
		t.Error("the rotation should be finished once the new token has been used")
	}
	c.token = oldTok
	var sf *sendFailure
	if err := send(ctx, client, c, liveReading()); !errors.As(err, &sf) {
		t.Errorf("the old token should be refused now, got %v", err)
	}

	// A restart reads the new token from the file, and GWatch has nothing more
	// to tell it about tokens.
	again, _, _ := baseConfig()
	if again.token != rot.Token {
		t.Errorf("after a restart the agent has %q", again.token)
	}
	settings, err := askSettings(ctx, client, again, again.token, model.AgentCheckIn{Version: version})
	if err != nil || settings.Token != "" {
		t.Errorf("a finished rotation should hand out nothing: %v %+v", err, settings)
	}
}

// Backfill, all the way round: readings held through an outage outlive a
// restart of the agent and are filed by GWatch at the times they were taken.
func TestBackfillEndToEnd(t *testing.T) {
	ts := gwatchServer(t)
	cfg, _, tok := pairedAgent(t, ts)
	ctx := context.Background()
	client := newClient(cfg)

	// GWatch is unreachable for a while: three readings are held and kept.
	q := openReadingQueue(cfg, queueFile())
	taken := []time.Time{}
	for i := 3; i >= 1; i-- {
		m := liveReading()
		m.Timestamp = time.Now().Add(-time.Duration(i) * 10 * time.Minute).Truncate(time.Millisecond)
		taken = append(taken, m.Timestamp)
		q.add(m)
		q.flush(ctx, down)
		q.sync()
	}

	// The agent restarts and GWatch is back.
	q = openReadingQueue(cfg, queueFile())
	if q.len() != 3 {
		t.Fatalf("held %d readings after the restart, want 3", q.len())
	}
	var filed []time.Time
	sent, err := q.flush(ctx, func(m model.HostMetrics) error {
		c := cfg
		c.token = tok
		if !isHeld(c, m, time.Now()) {
			t.Errorf("a reading from %s ago was not marked as held", time.Since(m.Timestamp))
		}
		b, _ := json.Marshal(m)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.ingestURL(), bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set(heldHeader, "1")
		resp, err := client.Do(req)
		if err != nil {
			return &sendFailure{err: err, retry: true}
		}
		defer resp.Body.Close()
		var out struct {
			TS time.Time `json:"ts"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		if resp.StatusCode != http.StatusAccepted {
			return &sendFailure{err: fmt.Errorf("HTTP %d", resp.StatusCode)}
		}
		filed = append(filed, out.TS)
		return nil
	})
	q.sync()
	if err != nil || sent != 3 {
		t.Fatalf("sent %d: %v", sent, err)
	}
	for i := range taken {
		if !filed[i].Equal(taken[i]) {
			t.Errorf("reading %d was taken at %s and filed at %s", i, taken[i], filed[i])
		}
	}
	if again := openReadingQueue(cfg, queueFile()); again.len() != 0 {
		t.Errorf("delivered readings should not be held again: %d", again.len())
	}
}

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/checks"
	"github.com/jxburros/GWatch/internal/model"
)

// stubRunner replaces the check runner with one that records what it was
// asked to run and succeeds.
type stubRunner struct {
	mu   sync.Mutex
	runs map[int64]int
	seen map[model.CheckType]int
}

func stubChecks(t *testing.T) *stubRunner {
	t.Helper()
	s := &stubRunner{runs: map[int64]int{}, seen: map[model.CheckType]int{}}
	old, oldSpread, oldEvery := runCollected, firstRunSpread, collectorSendEvery
	runCollected = func(ctx context.Context, c model.Check, o checks.Options) model.Result {
		s.mu.Lock()
		s.runs[c.ID]++
		s.seen[c.Type]++
		s.mu.Unlock()
		lat := 2.0
		return model.Result{CheckID: c.ID, Timestamp: time.Now(), Success: true, Status: model.StatusUp, Message: "ok", LatencyMS: &lat, Attempts: 1}
	}
	firstRunSpread, collectorSendEvery = 5*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { runCollected, firstRunSpread, collectorSendEvery = old, oldSpread, oldEvery })
	return s
}

func (s *stubRunner) count(id int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runs[id]
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func pingDef(id int64) model.CollectorCheck {
	return model.CollectorCheck{ID: id, Type: model.CheckPing, Name: "Ping", Target: "192.168.50.1", IntervalSeconds: 60, TimeoutSeconds: 5}
}

func TestCollectorIsOffUnlessTheMachineSaysSo(t *testing.T) {
	dir := isolate(t)
	c, _, _ := baseConfig()
	if c.collector {
		t.Fatal("a machine with no settings must not be a collector")
	}
	path := filepath.Join(dir, "agent.json")
	writeFile(t, path, `{"collector": true}`)
	c, problems, warnings := baseConfig()
	if !c.collector || c.sources["collector"] != srcFile || len(problems)+len(warnings) > 0 {
		t.Fatalf("collector=%v source=%s problems=%v warnings=%v", c.collector, c.sources["collector"], problems, warnings)
	}
	t.Setenv("GWATCH_AGENT_COLLECTOR", "false")
	if c, _, _ = baseConfig(); c.collector {
		t.Fatal("the environment should override the file")
	}
	writeFile(t, path, `{"collector": "yes"}`)
	if _, problems, _ = baseConfig(); len(problems) != 1 {
		t.Fatalf("a non-boolean collector setting should be a problem: %v", problems)
	}
}

// GWatch alone cannot make a machine a collector: an agent whose own file does
// not allow it says so at check-in, and runs nothing whatever comes back.
func TestCheckInWithoutLocalPermissionRunsNothing(t *testing.T) {
	isolate(t)
	stub := stubChecks(t)
	var said model.AgentCheckIn
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&said)
		// A GWatch that hands out checks anyway (it must not, but the agent
		// does not rely on that).
		json.NewEncoder(w).Encode(model.AgentSettings{Collector: &model.CollectorAssignment{Checks: []model.CollectorCheck{pingDef(1)}}})
	}))
	defer srv.Close()
	cfg := defaultConfig()
	cfg.server, cfg.token = srv.URL, "gwa_test0000000000000000"
	p := &program{cfg: cfg, mode: "run"}
	rt := newRuntime(cfg)
	if _, err := p.checkInOnce(context.Background(), rt, srv.Client()); err != nil {
		t.Fatal(err)
	}
	if said.Collector {
		t.Fatal("the agent claimed its file allows collecting")
	}
	time.Sleep(50 * time.Millisecond)
	if stub.count(1) != 0 || rt.collector != nil {
		t.Fatal("a machine that has not opted in ran a check")
	}
}

// The agent's own check of what it is handed: a script, a hardware check or a
// target that is not an address is refused and never reaches the runner, and
// an assignment withdrawn stops everything.
func TestCollectorRefusesWhatItMustNotRun(t *testing.T) {
	isolate(t)
	stub := stubChecks(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := defaultConfig()
	cfg.collector = true
	col := newCollectorRunner(ctx, cfg, newRuntime(cfg), http.DefaultClient)
	go func() {
		for range col.results {
		}
	}()
	col.assign(&model.CollectorAssignment{Checks: []model.CollectorCheck{
		{ID: 1, Type: model.CheckCustom, Target: "x", IntervalSeconds: 60, Config: model.CheckConfig{Command: "rm -rf /"}},
		{ID: 2, Type: model.CheckSystem, Target: "x", IntervalSeconds: 60},
		{ID: 3, Type: model.CheckTCP, Target: "host; reboot", IntervalSeconds: 60, Config: model.CheckConfig{Port: 22}},
		pingDef(4),
	}})
	waitUntil(t, "the ping to run", func() bool { return stub.count(4) == 1 })
	time.Sleep(30 * time.Millisecond)
	stub.mu.Lock()
	if stub.seen[model.CheckCustom]+stub.seen[model.CheckSystem]+stub.runs[3] != 0 {
		stub.mu.Unlock()
		t.Fatalf("refused checks ran: %+v", stub.seen)
	}
	stub.mu.Unlock()
	if st := col.status(); st.Checks != 1 || !st.Active {
		t.Fatalf("status %+v", st)
	}
	col.assign(nil)
	if st := col.status(); st.Checks != 0 || st.Active {
		t.Fatalf("after withdrawal: %+v", st)
	}
}

// Results go out in batches; through an outage they are held, and once GWatch
// answers they are delivered, oldest first, those old enough marked as held.
func TestCollectorResultsAreHeldThroughAnOutage(t *testing.T) {
	isolate(t)
	stubChecks(t)
	var mu sync.Mutex
	failing := true
	var got []model.CollectorResult
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ingest/results" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if failing {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		var b model.CollectorBatch
		_ = json.NewDecoder(r.Body).Decode(&b)
		got = append(got, b.Results...)
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"accepted":1}`))
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := defaultConfig()
	cfg.server, cfg.token, cfg.collector, cfg.buffer = srv.URL, "gwa_test0000000000000000", true, time.Hour
	col := newCollectorRunner(ctx, cfg, newRuntime(cfg), srv.Client())
	done := make(chan struct{})
	go func() { col.sendLoop(ctx); close(done) }()
	col.assign(&model.CollectorAssignment{Checks: []model.CollectorCheck{}})

	// An old result, as though taken before the outage began.
	old := model.Result{CheckID: 7, Timestamp: time.Now().Add(-10 * time.Minute), Success: true, Status: model.StatusUp}
	col.results <- old
	col.results <- model.Result{CheckID: 7, Timestamp: time.Now(), Success: true, Status: model.StatusUp}
	waitUntil(t, "results to be held", func() bool { return col.status().Held == 2 })

	mu.Lock()
	failing = false
	mu.Unlock()
	waitUntil(t, "results to be delivered", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) >= 2
	})
	// The server has the results before the agent reads its answer; the
	// agent lets go of them only once it has.
	waitUntil(t, "delivered results to be let go", func() bool { return col.status().Held == 0 })
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if !got[0].Held || got[1].Held || got[0].Result.CheckID != 7 || !got[0].Result.Timestamp.Before(got[1].Result.Timestamp) {
		t.Fatalf("delivered %+v", got)
	}
}

// A collector with nothing due still says it is alive, so GWatch can tell
// "nothing to report" from "gone".
func TestCollectorSendsAHeartbeat(t *testing.T) {
	isolate(t)
	stubChecks(t)
	beats := make(chan int, 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b model.CollectorBatch
		_ = json.NewDecoder(r.Body).Decode(&b)
		beats <- len(b.Results)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := defaultConfig()
	cfg.server, cfg.token, cfg.collector = srv.URL, "gwa_test0000000000000000", true
	col := newCollectorRunner(ctx, cfg, newRuntime(cfg), srv.Client())
	go col.sendLoop(ctx)
	col.assign(&model.CollectorAssignment{Checks: []model.CollectorCheck{}})
	select {
	case n := <-beats:
		if n != 0 {
			t.Fatalf("a heartbeat should be empty, carried %d", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no heartbeat")
	}
}

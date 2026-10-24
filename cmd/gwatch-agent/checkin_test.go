package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/sysmetrics"
)

const (
	oldToken = "gwa_oldtoken000000000000000000"
	newToken = "gwa_newtoken000000000000000000"
)

func runtimeFor(t *testing.T, fromFile bool) (*agentRuntime, config) {
	t.Helper()
	dir := isolate(t)
	path := filepath.Join(dir, "agent.json")
	writeFile(t, path, `{"server":"https://s:7230","token":"`+oldToken+`"}`)
	cfg, problems, _ := baseConfig()
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	if !fromFile {
		cfg.tokenSource = srcEnv
		cfg.sources["token"] = srcEnv
	}
	return newRuntime(cfg), cfg
}

func TestRotationIsTakenUpWhenTheFileOwnsTheToken(t *testing.T) {
	rt, cfg := runtimeFor(t, true)
	rt.apply(cfg, model.AgentSettings{CheckInSeconds: 900, Token: newToken})
	if rt.currentToken() != newToken {
		t.Fatalf("token in use: %s", rt.currentToken())
	}
	got, _ := readStoredToken()
	if got != newToken {
		t.Errorf("the new token must be in the file before it is used, got %s", got)
	}
}

// A token that comes from the environment or a flag would come straight back
// at the next restart, after GWatch had retired the copy in the file.
func TestRotationIsLeftAloneWhenSomethingElseOwnsTheToken(t *testing.T) {
	rt, cfg := runtimeFor(t, false)
	rt.apply(cfg, model.AgentSettings{CheckInSeconds: 900, Token: newToken})
	if rt.currentToken() != oldToken {
		t.Errorf("switched tokens although the file would not keep it: %s", rt.currentToken())
	}
	if got, _ := readStoredToken(); got != oldToken {
		t.Errorf("the file was rewritten: %s", got)
	}
}

func TestReplacementTokensAreCheckedLikeTheNetworkData_TheyAre(t *testing.T) {
	rt, cfg := runtimeFor(t, true)
	for _, bad := range []string{"hello", "gwa_", "gwa_short", "gwa_has space 00000000000000", "gwa_" + strings.Repeat("a", 200), "gwa_semi;colon0000000000000", "gwa_newline\n0000000000000"} {
		rt.apply(cfg, model.AgentSettings{CheckInSeconds: 900, Token: bad})
		if rt.currentToken() != oldToken {
			t.Errorf("accepted %q as a token", bad)
		}
	}
}

func TestSettingsFromGWatchAreCheckedAndRemembered(t *testing.T) {
	rt, cfg := runtimeFor(t, true)
	sel := model.CollectSelection{Metrics: []string{"cpu", "net"}, Interfaces: []string{"eth0"}}
	rt.apply(cfg, model.AgentSettings{CheckInSeconds: 900, Collect: sel})
	if got := rt.effective(); len(got.Interfaces) != 1 || len(got.Metrics) != 2 {
		t.Errorf("effective selection %+v", got)
	}
	st, err := readAgentState()
	if err != nil || len(st.Collect.Interfaces) != 1 {
		t.Fatalf("the choice should be remembered for the next start: %v %+v", err, st)
	}
	// A fresh runtime picks it up before it has been able to ask anyone.
	if again := newRuntime(cfg).effective(); len(again.Interfaces) != 1 {
		t.Errorf("a restart forgot the selection: %+v", again)
	}
	// Nonsense is ignored and does not wipe a good selection.
	rt.apply(cfg, model.AgentSettings{CheckInSeconds: 900, Collect: model.CollectSelection{Metrics: []string{"gpu"}}})
	if got := rt.effective(); len(got.Interfaces) != 1 {
		t.Errorf("an invalid selection replaced a good one: %+v", got)
	}
}

func TestTheMachinesOwnLimitIsNeverWidened(t *testing.T) {
	_, cfg := runtimeFor(t, true)
	cfg.collect = model.CollectSelection{Interfaces: []string{"eth0"}}
	rt := newRuntime(cfg)
	rt.apply(cfg, model.AgentSettings{CheckInSeconds: 900, Collect: model.CollectSelection{Interfaces: []string{"eth0", "docker0"}}})
	if got := rt.effective().Interfaces; len(got) != 1 || got[0] != "eth0" {
		t.Errorf("GWatch widened the machine's limit: %v", got)
	}
	// Nothing in common sends nothing, not everything.
	rt.apply(cfg, model.AgentSettings{CheckInSeconds: 900, Collect: model.CollectSelection{Interfaces: []string{"wlan0"}}})
	m := rt.shape(model.HostMetrics{Interfaces: []model.HostInterface{{Name: "eth0", Up: true}, {Name: "wlan0", Up: true}}})
	if len(m.Interfaces) != 0 {
		t.Errorf("a limit and a choice with nothing in common sent %v", m.Interfaces)
	}
}

func TestInventoryIsWhatTheMachineMayOffer(t *testing.T) {
	_, cfg := runtimeFor(t, true)
	cfg.collect = model.CollectSelection{Interfaces: []string{"eth0", "docker0"}}
	rt := newRuntime(cfg)
	rt.shape(model.HostMetrics{Interfaces: []model.HostInterface{
		{Name: "eth0", Up: true}, {Name: "docker0", Up: true}, {Name: "wlan0", Up: true},
	}})
	rt.mu.Lock()
	offered, all := rt.inventory, rt.inventoryAll
	rt.mu.Unlock()
	if len(offered.Interfaces) != 2 {
		t.Errorf("GWatch is offered %v; only what this machine allows", offered.Interfaces)
	}
	if len(all.Interfaces) != 3 {
		t.Errorf("the panel sees everything: %v", all.Interfaces)
	}
	var docker model.InventoryItem
	for _, i := range offered.Interfaces {
		if i.Name == "docker0" {
			docker = i
		}
	}
	if docker.Auto {
		t.Error("docker0 should be offered but not ticked by default")
	}
}

func TestCheckInIntervalIsBoundedAndTheMachineMayInsist(t *testing.T) {
	rt, cfg := runtimeFor(t, true)
	if got := rt.apply(cfg, model.AgentSettings{CheckInSeconds: 1}); got != model.CheckInMin {
		t.Errorf("a silly interval from the network must be bounded: %v", got)
	}
	if got := rt.apply(cfg, model.AgentSettings{CheckInSeconds: 10_000_000}); got != model.CheckInMax {
		t.Errorf("a silly interval from the network must be bounded: %v", got)
	}
	cfg.checkIn, cfg.sources["checkIn"] = 3*time.Minute, srcFile
	if got := rt.apply(cfg, model.AgentSettings{CheckInSeconds: 3600}); got != 3*time.Minute {
		t.Errorf("an interval set on the machine wins over a suggestion: %v", got)
	}
}

func TestAskingForSettingsOverHTTP(t *testing.T) {
	var gotAuth string
	var gotReq model.AgentCheckIn
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotReq)
		_ = json.NewEncoder(w).Encode(model.AgentSettings{CheckInSeconds: 600, Collect: model.CollectSelection{Mounts: []string{"/"}}})
	}))
	defer srv.Close()
	cfg := config{server: srv.URL, interval: time.Minute}
	out, err := askSettings(context.Background(), newClient(cfg), cfg, "gwa_x", model.AgentCheckIn{Version: "9", Inventory: &model.Inventory{Mounts: []model.InventoryItem{{Name: "/"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer gwa_x" || gotReq.Version != "9" || gotReq.Inventory == nil || out.CheckInSeconds != 600 || out.Collect.Mounts[0] != "/" {
		t.Errorf("auth=%q req=%+v out=%+v", gotAuth, gotReq, out)
	}

	old := httptest.NewServer(http.NotFoundHandler())
	defer old.Close()
	cfg.server = old.URL
	if _, err := askSettings(context.Background(), newClient(cfg), cfg, "gwa_x", model.AgentCheckIn{}); !errors.Is(err, errNoCheckIn) {
		t.Errorf("a GWatch from before check-ins should be a quiet non-event, got %v", err)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html>")) }))
	defer bad.Close()
	cfg.server = bad.URL
	if _, err := askSettings(context.Background(), newClient(cfg), cfg, "gwa_x", model.AgentCheckIn{}); err == nil {
		t.Error("a reply that is not a settings reply must be an error")
	}
}

// ---- the queue ----

func reading(n int) model.HostMetrics {
	return model.HostMetrics{Hostname: "h", Timestamp: time.Unix(int64(n), 0)}
}

func TestQueueHoldsOldestFirstAndForgetsTheOldestWhenFull(t *testing.T) {
	q := newReadingQueue(config{buffer: 3 * time.Minute, interval: time.Minute})
	for i := 1; i <= 5; i++ {
		q.add(reading(i))
	}
	if q.len() != 3 || q.held[0].m.Timestamp.Unix() != 3 {
		t.Fatalf("a full queue keeps the newest: %v", q.held)
	}
	none := newReadingQueue(config{buffer: 0, interval: time.Minute})
	none.add(reading(1))
	none.add(reading(2))
	if none.len() != 1 || none.held[0].m.Timestamp.Unix() != 2 {
		t.Errorf("with no buffer only the reading in flight is kept: %v", none.held)
	}
}

func TestQueueSendsInOrderAndStopsWhereTheNetworkDoes(t *testing.T) {
	q := newReadingQueue(config{buffer: time.Hour, interval: time.Minute})
	for i := 1; i <= 5; i++ {
		q.add(reading(i))
	}
	var sent []int64
	fail := errors.New("down")
	n, err := q.flush(context.Background(), func(m model.HostMetrics) error {
		if len(sent) == 2 {
			return &sendFailure{err: fail, retry: true}
		}
		sent = append(sent, m.Timestamp.Unix())
		return nil
	})
	if n != 2 || !errors.Is(err, fail) || q.len() != 3 || q.held[0].m.Timestamp.Unix() != 3 {
		t.Fatalf("sent=%d err=%v held=%v", n, err, q.held)
	}
	if sent[0] != 1 || sent[1] != 2 {
		t.Errorf("readings must go oldest first so the newest is the last the server sees: %v", sent)
	}
	n, err = q.flush(context.Background(), func(m model.HostMetrics) error { return nil })
	if err != nil || n != 3 || q.len() != 0 {
		t.Errorf("a recovered network drains the queue: %d %v %d", n, err, q.len())
	}
}

// A reading the server calls malformed must not block everything behind it.
func TestQueueDropsWhatTheServerRefusesOutright(t *testing.T) {
	q := newReadingQueue(config{buffer: time.Hour, interval: time.Minute})
	q.add(reading(1))
	q.add(reading(2))
	var sent []int64
	n, err := q.flush(context.Background(), func(m model.HostMetrics) error {
		if m.Timestamp.Unix() == 1 {
			return &sendFailure{err: errors.New("HTTP 400"), retry: false}
		}
		sent = append(sent, m.Timestamp.Unix())
		return nil
	})
	if err != nil || n != 1 || q.len() != 0 || sent[0] != 2 {
		t.Errorf("n=%d err=%v held=%d sent=%v", n, err, q.len(), sent)
	}
}

func TestQueueCatchesUpGraduallyAfterALongOutage(t *testing.T) {
	q := newReadingQueue(config{buffer: 48 * time.Hour, interval: time.Minute})
	for i := 0; i < 500; i++ {
		q.add(reading(i))
	}
	n, err := q.flush(context.Background(), func(model.HostMetrics) error { return nil })
	if err != nil || n != maxFlushPerCycle || q.len() != 500-maxFlushPerCycle {
		t.Errorf("one cycle should send a bounded burst: sent %d, held %d, %v", n, q.len(), err)
	}
}

func TestSendClassifiesFailuresForTheQueue(t *testing.T) {
	for _, tc := range []struct {
		status int
		retry  bool
	}{{500, true}, {503, true}, {429, true}, {401, true}, {400, false}, {413, false}} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status) }))
		cfg := config{server: srv.URL, token: "gwa_x", interval: time.Minute}
		err := send(context.Background(), newClient(cfg), cfg, model.HostMetrics{})
		srv.Close()
		var sf *sendFailure
		if !errors.As(err, &sf) || sf.retry != tc.retry {
			t.Errorf("HTTP %d: retry=%v (err %v)", tc.status, sf != nil && sf.retry, err)
		}
	}
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	cfg := config{server: dead.URL, token: "gwa_x", interval: time.Minute}
	var sf *sendFailure
	if err := send(context.Background(), newClient(cfg), cfg, model.HostMetrics{}); !errors.As(err, &sf) || !sf.retry {
		t.Errorf("an unreachable server is worth retrying: %v", err)
	}
}

func TestStatusFileCarriesNoSecretsAndNoReadings(t *testing.T) {
	rt, cfg := runtimeFor(t, true)
	rt.shape(model.HostMetrics{Interfaces: []model.HostInterface{{Name: "eth0", Up: true}}})
	rt.noteReport(nil, 0)
	rt.writeStatus(cfg, 2)
	b, err := os.ReadFile(statusFile())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), oldToken) || strings.Contains(string(b), "token") {
		t.Errorf("the status file must not hold the token:\n%s", b)
	}
	st, err := readAgentStatus()
	if err != nil || st.Held != 2 || !st.ReportOK || st.Inventory == nil || len(st.Inventory.Interfaces) != 1 {
		t.Errorf("status %+v err %v", st, err)
	}
}

// A check-in that names a newer agent is applied on the check-in goroutine
// while the reporting loop writes the status file, which shows that version;
// under -race this caught the unlocked write.
func TestLatestAgentIsRecordedWithoutRacingTheStatusFile(t *testing.T) {
	rt, cfg := runtimeFor(t, true)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			rt.writeStatus(cfg, -1)
		}
	}()
	for i := 0; i < 20; i++ {
		rt.apply(cfg, model.AgentSettings{CheckInSeconds: 900, LatestAgent: fmt.Sprintf("999.0.%d", i)})
	}
	wg.Wait()
	rt.writeStatus(cfg, -1)
	st, err := readAgentStatus()
	if err != nil || st.LatestAgent != "999.0.19" {
		t.Fatalf("status %+v err %v", st, err)
	}
}

// While GWatch cannot be reached the agent keeps taking a reading every
// interval — only its attempts to deliver back off — so what is held through an
// outage is a reading per interval, and what arrives afterwards arrives oldest
// first. (An earlier version waited out each backoff before taking its next
// reading, and held one per attempt.)
func TestAnOutageHoldsAReadingPerIntervalAndDeliversThemInOrder(t *testing.T) {
	isolate(t)
	// Readings are stubbed so the test times the loop rather than this
	// machine's hardware, which on a busy CI runner can take longer than the
	// whole outage.
	old := takeReading
	takeReading = func(context.Context, config, *sysmetrics.Collector) (model.HostMetrics, error) {
		return model.HostMetrics{Hostname: "outage-test", Timestamp: time.Now()}, nil
	}
	t.Cleanup(func() { takeReading = old })

	// The outage is the first failedAttempts deliveries, not a stretch of wall
	// clock, so a slow machine lengthens it rather than shortening it.
	const failedAttempts = 5
	var mu sync.Mutex
	var got []time.Time
	var attempts int
	var recovered time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if attempts < failedAttempts {
			attempts++
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if recovered.IsZero() {
			recovered = time.Now()
		}
		var m model.HostMetrics
		if json.NewDecoder(r.Body).Decode(&m) == nil {
			got = append(got, m.Timestamp)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	cfg := defaultConfig()
	cfg.server, cfg.token, cfg.interval, cfg.buffer = srv.URL, oldToken, 30*time.Millisecond, time.Hour
	cfg.tokenSource = srcEnv
	p := &program{cfg: cfg, mode: "run"}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.report(ctx); close(done) }()

	// Delivery backs off 30, 60, 120, 240 and 480ms after the five failures,
	// close to a second in which a reading is taken every 30ms. Wait until
	// the agent has recovered and delivered well past what it held.
	deadline := time.Now().Add(30 * time.Second)
	for {
		mu.Lock()
		n, back := len(got), !recovered.IsZero()
		mu.Unlock()
		if back && n >= 40 {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatalf("the agent delivered %d readings in 30s (recovered: %v)", n, back)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	before := 0
	for i, ts := range got {
		if i > 0 && ts.Before(got[i-1]) {
			t.Fatalf("readings must be delivered oldest first: %v before %v", got[i-1], ts)
		}
		if ts.Before(recovered) {
			before++
		}
	}
	// A loop that took one reading per attempt would hold one per failure plus
	// the one delivered on recovery, six in all; taking one every interval
	// through the backoff holds about thirty. Twice the failures is far from
	// both.
	if before < 2*failedAttempts {
		t.Errorf("only %d readings from the outage were delivered (%d in all); the agent stopped taking readings while it waited", before, len(got))
	}
}

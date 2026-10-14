package engine

import (
	"context"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
)

// pushedMachine is a node with one hardware check reading a registered agent.
func pushedMachine(t *testing.T, st *store.Store, threshold int) (model.Node, model.Check, model.Agent) {
	t.Helper()
	ctx := context.Background()
	agent, err := st.CreateAgent(ctx, "nas", nil, "gwa_x", "hash"+time.Now().String(), "test")
	if err != nil {
		t.Fatal(err)
	}
	cfg := model.SystemDefaults()
	cfg.HostSource, cfg.AgentID = model.HostSourceAgent, agent.ID
	node, err := st.CreateNode(ctx, model.Node{Name: "nas", Enabled: true, Checks: []model.Check{{
		Type: model.CheckSystem, Name: "Hardware health", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 10,
		Retries: 1, FailureThreshold: threshold, Config: cfg,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return node, node.Checks[0], agent
}

func machineReading(host string, cpu, mem float64, at time.Time) model.HostMetrics {
	return model.HostMetrics{
		Hostname: host, OS: "linux", Arch: "amd64", AgentVersion: "1.0.0", Timestamp: at,
		CPU:         model.HostCPU{Cores: 4, UsagePct: &cpu},
		Memory:      model.HostMemory{TotalBytes: 1000, UsedBytes: uint64(mem * 10), UsedPct: mem},
		Filesystems: []model.HostFilesystem{{Mount: "/", TotalBytes: 100, UsedPct: 40}},
	}
}

func resultsOf(t *testing.T, st *store.Store, checkID int64) []model.Result {
	t.Helper()
	rs, err := st.ResultsBetween(context.Background(), checkID, time.Now().Add(-72*time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

// A machine that pushes is judged when its reading arrives: the result, the
// check's state and its history all come from that, not from the schedule.
func TestAPushedReadingIsJudgedWhenItArrives(t *testing.T) {
	e, st, net, _ := setup(t)
	ctx := context.Background()
	_, check, agent := pushedMachine(t, st, 2)
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()

	if _, err := e.IngestHostReading(ctx, agent, machineReading("nas.local", 95, 40, time.Now()), "10.0.0.5:1"); err != nil {
		t.Fatal(err)
	}
	rs := resultsOf(t, st, check.ID)
	if len(rs) != 1 {
		t.Fatalf("want one result recorded for the reading, got %d", len(rs))
	}
	if rs[0].Metrics["cpu"] != 95 || rs[0].Details.Host == nil || rs[0].Details.Host.Hostname != "nas.local" {
		t.Errorf("the result should carry the metrics and the snapshot: %+v", rs[0])
	}
	if rs[0].Status != model.StatusDegraded {
		t.Errorf("processor at 95%% is over the 90%% warning line: %s", rs[0].Status)
	}
	state, _ := e.State(check.ID)
	if state.LastRunAt == nil || state.Status != model.StatusDegraded {
		t.Errorf("the check's state should follow the reading: %+v", state)
	}
	net.mu.Lock()
	defer net.mu.Unlock()
	if net.runs[check.ID] != 0 {
		t.Errorf("nothing ran the check; the reading arrived: %d scheduled runs", net.runs[check.ID])
	}
	// The reading is also the machine's newest, in memory.
	if got, ok := e.Hosts().Latest(agent.HostKey()); !ok || got.Hostname != "nas.local" {
		t.Errorf("newest reading: %+v", got)
	}
}

// While readings keep arriving the schedule has nothing to add: a run would
// only judge the same reading a second time.
func TestAFreshReadingKeepsTheScheduleQuiet(t *testing.T) {
	e, st, net, _ := setup(t)
	ctx := context.Background()
	_, check, agent := pushedMachine(t, st, 2)
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	if _, err := e.IngestHostReading(ctx, agent, machineReading("nas.local", 10, 40, time.Now()), ""); err != nil {
		t.Fatal(err)
	}

	e.mu.Lock()
	past := time.Now().Add(-time.Second)
	e.states[check.ID].NextRunAt = &past
	e.mu.Unlock()
	e.tick(time.Now())
	time.Sleep(150 * time.Millisecond)

	net.mu.Lock()
	runs := net.runs[check.ID]
	net.mu.Unlock()
	if runs != 0 {
		t.Fatalf("a due check with a fresh reading must not run, got %d runs", runs)
	}
	e.mu.Lock()
	next := e.states[check.ID].NextRunAt
	e.mu.Unlock()
	if next == nil || !next.After(time.Now()) {
		t.Errorf("the schedule should have moved on: %v", next)
	}
}

// Once readings stop, the schedule is what notices.
func TestTheScheduleNoticesWhenReadingsStop(t *testing.T) {
	e, st, net, _ := setup(t)
	ctx := context.Background()
	_, check, agent := pushedMachine(t, st, 2)
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	// The last reading was ten minutes ago, far past the check's staleness limit.
	if _, err := e.IngestHostReading(ctx, agent, machineReading("nas.local", 10, 40, time.Now().Add(-10*time.Minute)), ""); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	past := time.Now().Add(-time.Second)
	e.states[check.ID].NextRunAt = &past
	e.mu.Unlock()
	e.tick(time.Now())
	waitFor(t, func() bool {
		net.mu.Lock()
		defer net.mu.Unlock()
		return net.runs[check.ID] >= 1
	})
}

// Down and recovery work through a pushed reading exactly as they do through a
// run: the alert path is the same path.
func TestPushedReadingsDriveDownAndRecovery(t *testing.T) {
	e, st, _, _ := setup(t)
	ctx := context.Background()
	_, check, agent := pushedMachine(t, st, 1)
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()

	if _, err := e.IngestHostReading(ctx, agent, machineReading("nas.local", 10, 99, time.Now()), ""); err != nil {
		t.Fatal(err)
	}
	if s, _ := e.State(check.ID); s.Status != model.StatusDown {
		t.Fatalf("memory at 99%% is over the critical line: %s", s.Status)
	}
	if _, err := e.IngestHostReading(ctx, agent, machineReading("nas.local", 10, 30, time.Now().Add(2*time.Minute)), ""); err != nil {
		t.Fatal(err)
	}
	if s, _ := e.State(check.ID); s.Status != model.StatusUp {
		t.Fatalf("a healthy reading should recover the check: %s", s.Status)
	}
	types := eventTypes(t, st, check.ID)
	if types[model.EventDown] != 1 || types[model.EventRecovered] != 1 {
		t.Errorf("events: %v", types)
	}
}

// What an agent held back through an outage is added to the history at the time
// it was taken, and is not news: no verdict on the check, no alert.
func TestHeldBackReadingsAreHistoryOnly(t *testing.T) {
	e, st, _, mb := setup(t)
	ctx := context.Background()
	_, check, agent := pushedMachine(t, st, 1)
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()

	then := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	// Memory at 99% would take a live check down. Two hours ago, it is history.
	if _, err := e.IngestHostReading(ctx, agent, machineReading("nas.local", 10, 99, then), ""); err != nil {
		t.Fatal(err)
	}
	rs := resultsOf(t, st, check.ID)
	if len(rs) != 1 {
		t.Fatalf("the held-back reading should be in the history: %d results", len(rs))
	}
	if d := rs[0].Timestamp.Sub(then); d > time.Second || d < -time.Second {
		t.Errorf("it belongs where it was taken (%s), not at arrival (%s)", then, rs[0].Timestamp)
	}
	if s, _ := e.State(check.ID); s.Status == model.StatusDown || s.LastRunAt != nil {
		t.Errorf("a held-back reading must not move the check's state: %+v", s)
	}
	if mb.count() != 0 {
		t.Error("a held-back reading must not alert")
	}
	// The same reading again, or one within the check's spacing of it, is not
	// recorded twice.
	for _, offset := range []time.Duration{0, 10 * time.Second, -10 * time.Second} {
		if _, err := e.IngestHostReading(ctx, agent, machineReading("nas.local", 10, 99, then.Add(offset)), ""); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(resultsOf(t, st, check.ID)); got != 1 {
		t.Errorf("a retried reading must not be recorded again: %d results", got)
	}
	// One a whole interval apart is a new point.
	if _, err := e.IngestHostReading(ctx, agent, machineReading("nas.local", 10, 40, then.Add(time.Minute)), ""); err != nil {
		t.Fatal(err)
	}
	if got := len(resultsOf(t, st, check.ID)); got != 2 {
		t.Errorf("want two points a minute apart: %d", got)
	}
	// And the newest reading in memory is not dragged backwards by any of it.
	now := time.Now()
	if _, err := e.IngestHostReading(ctx, agent, machineReading("current", 10, 40, now), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.IngestHostReading(ctx, agent, machineReading("older", 10, 40, now.Add(-time.Hour)), ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.Hosts().Latest(agent.HostKey()); got.Hostname != "current" {
		t.Errorf("newest reading went backwards: %s", got.Hostname)
	}
}

// A machine that reports faster than its check runs does not turn the check
// into a result per reading.
func TestFastReportersAreRecordedAtTheChecksPace(t *testing.T) {
	e, st, _, _ := setup(t)
	ctx := context.Background()
	_, check, agent := pushedMachine(t, st, 2)
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	base := time.Now().Add(-30 * time.Second)
	for i := 0; i < 6; i++ {
		if _, err := e.IngestHostReading(ctx, agent, machineReading("nas.local", 10, 40, base.Add(time.Duration(i)*5*time.Second)), ""); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(resultsOf(t, st, check.ID)); got != 1 {
		t.Errorf("six readings in thirty seconds against a one-minute check: %d results", got)
	}
}

// After a restart the newest reading is read back out of what the check
// recorded, so a restart does not make the machine look silent.
func TestARestartFindsTheNewestReadingInTheChecksResults(t *testing.T) {
	e, st, _, _ := setup(t)
	ctx := context.Background()
	_, _, agent := pushedMachine(t, st, 2)
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.IngestHostReading(ctx, agent, machineReading("nas.local", 33, 40, time.Now()), ""); err != nil {
		t.Fatal(err)
	}
	e.Stop()

	e2, _, _, _ := setupOn(t, st)
	if err := e2.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer e2.Stop()
	got, err := e2.Hosts().AgentHost(ctx, agent.ID)
	if err != nil {
		t.Fatalf("a machine with a recorded reading should not look silent after a restart: %v", err)
	}
	if got.Hostname != "nas.local" || got.CPU.UsagePct == nil || *got.CPU.UsagePct != 33 || got.Key != agent.HostKey() {
		t.Errorf("restored reading: %+v", got)
	}
}

// A machine that only exists as readings nobody checks has nowhere to keep them.
func TestAMachineNoCheckWatchesKeepsOnlyItsNewestReading(t *testing.T) {
	e, st, _, _ := setup(t)
	ctx := context.Background()
	agent, err := st.CreateAgent(ctx, "orphan", nil, "gwa_y", "hash-orphan", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	if _, err := e.IngestHostReading(ctx, agent, machineReading("orphan.local", 10, 40, time.Now()), ""); err != nil {
		t.Fatal(err)
	}
	if got, ok := e.Hosts().Latest(agent.HostKey()); !ok || got.Hostname != "orphan.local" {
		t.Errorf("the newest reading is held: %+v", got)
	}
	raw, _, _, _, _, _ := st.Counts(ctx)
	if raw != 0 {
		t.Errorf("with no check there is nothing to record a result for, got %d", raw)
	}
}

func TestEvaluatingForAnAgentIgnoresOtherAgentsChecks(t *testing.T) {
	e, st, _, _ := setup(t)
	ctx := context.Background()
	_, mine, agentA := pushedMachine(t, st, 2)
	_, theirs, _ := pushedMachine(t, st, 2)
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	if _, err := e.IngestHostReading(ctx, agentA, machineReading("a", 10, 40, time.Now()), ""); err != nil {
		t.Fatal(err)
	}
	if len(resultsOf(t, st, mine.ID)) != 1 || len(resultsOf(t, st, theirs.ID)) != 0 {
		t.Error("a reading is judged by the checks that watch its machine, and by no others")
	}
}

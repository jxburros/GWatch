package hostmon

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
	"github.com/jxburros/GWatch/internal/store/storetest"
)

func newTest(t *testing.T) (*Monitor, *store.Store) {
	t.Helper()
	st := storetest.Open(t)
	return New(st), st
}

func reading(hostname string, cpu float64, ts time.Time) model.HostMetrics {
	return model.HostMetrics{
		Key: "whatever-the-agent-claims", Hostname: hostname, OS: "linux", Arch: "arm64",
		AgentVersion: "1.0.0", Timestamp: ts,
		CPU:    model.HostCPU{Cores: 4, UsagePct: &cpu},
		Memory: model.HostMemory{TotalBytes: 100, UsedBytes: 30, UsedPct: 30},
	}
}

// A token names one machine, so a reading is filed under that machine whatever
// the payload says. Otherwise a stolen token could rewrite another machine's
// history.
func TestAcceptIgnoresTheClaimedKey(t *testing.T) {
	m, st := newTest(t)
	ctx := context.Background()
	agent, err := st.CreateAgent(ctx, "nas", nil, "p", "hash", "")
	if err != nil {
		t.Fatal(err)
	}

	claimed := reading("nas.local", 40, time.Now())
	claimed.Key = "local" // try to overwrite this computer's reading
	stored, err := m.Accept(ctx, agent, claimed, "10.0.0.5:1234")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Key != agent.HostKey() {
		t.Fatalf("want the reading filed under %q, got %q", agent.HostKey(), stored.Key)
	}
	if _, ok := m.Latest(model.HostKeyLocal); ok {
		t.Error("the agent wrote into this computer's reading")
	}
	got, ok := m.Latest(agent.HostKey())
	if !ok || got.Hostname != "nas.local" {
		t.Errorf("reading not held: %+v", got)
	}

	// Reporting in records what the machine says it is.
	agent, _ = st.GetAgent(ctx, agent.ID)
	if agent.LastSeenAt == nil || agent.LastAddr != "10.0.0.5:1234" || agent.LastVersion != "1.0.0" {
		t.Errorf("the report was not recorded against the agent: %+v", agent)
	}
}

// A reporting machine's clock must not park a reading outside the window a
// chart can draw.
func TestAcceptSettlesImplausibleTimestamps(t *testing.T) {
	m, st := newTest(t)
	ctx := context.Background()
	agent, _ := st.CreateAgent(ctx, "nas", nil, "p", "hash", "")
	now := time.Now()

	tests := []struct {
		name     string
		ts       time.Time
		wantKept bool
	}{
		{"a few seconds of skew is kept", now.Add(-20 * time.Second), true},
		{"catching up after an outage is kept", now.Add(-2 * time.Hour), true},
		{"a clock years fast is replaced", now.AddDate(2, 0, 0), false},
		{"a clock years slow is replaced", now.AddDate(-2, 0, 0), false},
		{"no timestamp at all is replaced", time.Time{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stored, err := m.Accept(ctx, agent, reading("nas", 10, tt.ts), "")
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantKept {
				if !stored.Timestamp.Equal(tt.ts) {
					t.Errorf("want the machine's own time %s, got %s", tt.ts, stored.Timestamp)
				}
				return
			}
			if drift := time.Since(stored.Timestamp); drift < 0 || drift > time.Minute {
				t.Errorf("want arrival time, got %s (%s off)", stored.Timestamp, drift)
			}
		})
	}
}

// Everything an agent sends is stored and later displayed, so nothing arrives
// unbounded.
func TestAcceptBoundsWhatTheAgentSends(t *testing.T) {
	m, st := newTest(t)
	ctx := context.Background()
	agent, _ := st.CreateAgent(ctx, "nas", nil, "p", "hash", "")

	huge := reading(strings.Repeat("h", 5000), 10, time.Now())
	huge.AgentVersion = strings.Repeat("v", 5000)
	huge.OS = strings.Repeat("o", 5000)
	for i := 0; i < 50; i++ {
		huge.Warnings = append(huge.Warnings, strings.Repeat("w", 5000))
	}
	stored, err := m.Accept(ctx, agent, huge, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Warnings) > 8 {
		t.Errorf("warnings not bounded: %d", len(stored.Warnings))
	}
	for _, w := range stored.Warnings {
		if len(w) > 200 {
			t.Errorf("a warning is %d characters long", len(w))
		}
	}
	agent, _ = st.GetAgent(ctx, agent.ID)
	if len(agent.Hostname) > 120 || len(agent.LastVersion) > 40 || len(agent.OS) > 40 {
		t.Errorf("agent identity not bounded: %+v", agent)
	}
}

// Accepting a reading holds it as the machine's newest and does not store it:
// what is kept is the result of the machine's check judging it.
func TestAcceptKeepsNothingOfItsOwn(t *testing.T) {
	m, st := newTest(t)
	ctx := context.Background()
	agent, _ := st.CreateAgent(ctx, "nas", nil, "p", "hash", "")
	if _, err := m.Accept(ctx, agent, reading("nas.local", 10, time.Now()), ""); err != nil {
		t.Fatal(err)
	}
	// A new Monitor over the same database has no reading and no way to find
	// one: there is nowhere in the database a reading lives apart from results.
	if _, err := New(st).AgentHost(ctx, agent.ID); !errors.Is(err, ErrNoReading) {
		t.Fatalf("a restart finds nothing without a Restore hook, got %v", err)
	}
}

// After a restart the newest reading comes back from what the machine's check
// recorded, so a restart does not make every check report its machine down.
func TestAgentHostRestoresFromWhatTheCheckRecorded(t *testing.T) {
	m, st := newTest(t)
	ctx := context.Background()
	agent, _ := st.CreateAgent(ctx, "nas", nil, "p", "hash", "")

	if _, err := m.AgentHost(ctx, agent.ID); !errors.Is(err, ErrNoReading) {
		t.Fatalf("want ErrNoReading before the machine reports, got %v", err)
	}
	if _, err := m.Accept(ctx, agent, reading("nas.local", 10, time.Now()), ""); err != nil {
		t.Fatal(err)
	}
	if got, err := m.AgentHost(ctx, agent.ID); err != nil || got.Hostname != "nas.local" {
		t.Fatalf("from memory: %+v %v", got, err)
	}

	restarted := New(st)
	asked := ""
	restarted.Restore = func(_ context.Context, key string) (model.HostMetrics, bool) {
		asked = key
		return reading("restored.local", 5, time.Now()), true
	}
	got, err := restarted.AgentHost(ctx, agent.ID)
	if err != nil || got.Hostname != "restored.local" || asked != agent.HostKey() {
		t.Fatalf("after a restart: %+v %v (asked %q)", got, err, asked)
	}
	// It is remembered, so the next look does not ask again.
	asked = ""
	if _, err := restarted.AgentHost(ctx, agent.ID); err != nil || asked != "" {
		t.Errorf("a restored reading should be held: %v asked=%q", err, asked)
	}
}

// Readings a machine held back through an outage arrive after the ones taken
// since, and must not push its newest reading backwards.
func TestAnOlderReadingNeverReplacesANewerOne(t *testing.T) {
	m, st := newTest(t)
	ctx := context.Background()
	agent, _ := st.CreateAgent(ctx, "nas", nil, "p", "hash", "")
	now := time.Now()
	if _, err := m.Accept(ctx, agent, reading("new", 10, now), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Accept(ctx, agent, reading("old", 10, now.Add(-time.Hour)), ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.Latest(agent.HostKey()); got.Hostname != "new" {
		t.Errorf("the newest reading was replaced by an older one: %s", got.Hostname)
	}
}

// A revoked machine must stop answering checks from memory, not just stop
// being accepted.
func TestForgetDropsTheCachedReading(t *testing.T) {
	m, st := newTest(t)
	ctx := context.Background()
	agent, _ := st.CreateAgent(ctx, "nas", nil, "p", "hash", "")
	if _, err := m.Accept(ctx, agent, reading("nas.local", 10, time.Now()), ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Latest(agent.HostKey()); !ok {
		t.Fatal("reading should be cached")
	}
	m.Forget(agent.HostKey())
	if _, ok := m.Latest(agent.HostKey()); ok {
		t.Error("reading should have been dropped")
	}
}

// A sampling failure is reported once, not on every tick, so a machine with no
// readable hardware cannot fill the log.
func TestErrorsAreReportedOncePerTransition(t *testing.T) {
	m, _ := newTest(t)
	var seen []string
	m.OnError = func(err error) { seen = append(seen, err.Error()) }

	m.reportError(errors.New("boom"))
	m.reportError(errors.New("boom"))
	m.reportError(errors.New("boom"))
	if len(seen) != 1 {
		t.Fatalf("want one report, got %v", seen)
	}
	m.reportError(nil)
	m.reportError(errors.New("boom"))
	if len(seen) != 2 {
		t.Fatalf("a recovery then a new failure should report again, got %v", seen)
	}
}

// The sampler runs on its own schedule, but a check may fire before the first
// tick; it still needs something to evaluate.
func TestLocalHostReadsOnDemand(t *testing.T) {
	m, _ := newTest(t)
	got, err := m.LocalHost(context.Background())
	if err != nil {
		t.Skipf("no hardware readings on this platform: %v", err)
	}
	if got.Hostname == "" || got.CPU.Cores == 0 {
		t.Fatalf("empty reading: %+v", got)
	}
	if got.Key != model.HostKeyLocal {
		t.Errorf("key: %q", got.Key)
	}
}

func TestRunSamplesAndNotifies(t *testing.T) {
	m, _ := newTest(t)
	notified := make(chan string, 4)
	m.Interval = time.Hour // only the immediate first sample matters here
	m.OnSample = func(key string) {
		select {
		case notified <- key:
		default:
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()

	select {
	case key := <-notified:
		if key != model.HostKeyLocal {
			t.Errorf("key: %q", key)
		}
	case <-time.After(10 * time.Second):
		cancel()
		<-done
		t.Skip("no hardware readings on this platform")
	}
	cancel()
	<-done

	if got, ok := m.Latest(model.HostKeyLocal); !ok || got.Hostname == "" {
		t.Fatalf("the sample was not held: %+v", got)
	}
}

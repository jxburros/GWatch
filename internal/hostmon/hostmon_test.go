package hostmon

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
)

func newTest(t *testing.T) (*Monitor, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
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
func TestIngestIgnoresTheClaimedKey(t *testing.T) {
	m, st := newTest(t)
	ctx := context.Background()
	agent, err := st.CreateAgent(ctx, "nas", nil, "p", "hash", "")
	if err != nil {
		t.Fatal(err)
	}

	claimed := reading("nas.local", 40, time.Now())
	claimed.Key = "local" // try to overwrite this computer's history
	stored, err := m.Ingest(ctx, agent, claimed, "10.0.0.5:1234")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Key != agent.HostKey() {
		t.Fatalf("want the reading filed under %q, got %q", agent.HostKey(), stored.Key)
	}
	if _, err := st.LatestHostSample(ctx, model.HostKeyLocal); !errors.Is(err, store.ErrNotFound) {
		t.Error("the agent wrote into this computer's history")
	}
	sample, err := st.LatestHostSample(ctx, agent.HostKey())
	if err != nil {
		t.Fatal(err)
	}
	if sample.Metrics.Hostname != "nas.local" {
		t.Errorf("reading not stored: %+v", sample.Metrics)
	}

	// Reporting in records what the machine says it is.
	agent, _ = st.GetAgent(ctx, agent.ID)
	if agent.LastSeenAt == nil || agent.LastAddr != "10.0.0.5:1234" || agent.LastVersion != "1.0.0" {
		t.Errorf("the report was not recorded against the agent: %+v", agent)
	}
}

// A reporting machine's clock must not park a reading outside the window a
// chart can draw.
func TestIngestSettlesImplausibleTimestamps(t *testing.T) {
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
			stored, err := m.Ingest(ctx, agent, reading("nas", 10, tt.ts), "")
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
func TestIngestBoundsWhatTheAgentSends(t *testing.T) {
	m, st := newTest(t)
	ctx := context.Background()
	agent, _ := st.CreateAgent(ctx, "nas", nil, "p", "hash", "")

	huge := reading(strings.Repeat("h", 5000), 10, time.Now())
	huge.AgentVersion = strings.Repeat("v", 5000)
	huge.OS = strings.Repeat("o", 5000)
	for i := 0; i < 50; i++ {
		huge.Warnings = append(huge.Warnings, strings.Repeat("w", 5000))
	}
	stored, err := m.Ingest(ctx, agent, huge, "")
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

func TestAgentHostFallsBackToTheDatabase(t *testing.T) {
	m, st := newTest(t)
	ctx := context.Background()
	agent, _ := st.CreateAgent(ctx, "nas", nil, "p", "hash", "")

	if _, err := m.AgentHost(ctx, agent.ID); !errors.Is(err, ErrNoReading) {
		t.Fatalf("want ErrNoReading before the machine reports, got %v", err)
	}
	if _, err := m.Ingest(ctx, agent, reading("nas.local", 10, time.Now()), ""); err != nil {
		t.Fatal(err)
	}
	if got, err := m.AgentHost(ctx, agent.ID); err != nil || got.Hostname != "nas.local" {
		t.Fatalf("from memory: %+v %v", got, err)
	}

	// A fresh Monitor has nothing cached — a restart must not lose the
	// machine's last reading, or every check would report it down.
	restarted := New(st)
	got, err := restarted.AgentHost(ctx, agent.ID)
	if err != nil || got.Hostname != "nas.local" {
		t.Fatalf("after a restart: %+v %v", got, err)
	}
}

// A revoked machine must stop answering checks from memory, not just stop
// being accepted.
func TestForgetDropsTheCachedReading(t *testing.T) {
	m, st := newTest(t)
	ctx := context.Background()
	agent, _ := st.CreateAgent(ctx, "nas", nil, "p", "hash", "")
	if _, err := m.Ingest(ctx, agent, reading("nas.local", 10, time.Now()), ""); err != nil {
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

func TestRecordRequiresAKey(t *testing.T) {
	m, _ := newTest(t)
	if err := m.Record(context.Background(), model.HostMetrics{}); err == nil {
		t.Fatal("a reading with no host key should be refused")
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

func TestRunStoresAndNotifies(t *testing.T) {
	m, st := newTest(t)
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

	if _, err := st.LatestHostSample(context.Background(), model.HostKeyLocal); err != nil {
		t.Fatalf("the sample was not stored: %v", err)
	}
}

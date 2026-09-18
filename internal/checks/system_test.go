package checks

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

func pct(v float64) *float64 { return &v }

// healthyReading is a machine with nothing wrong with it, taken just now.
func healthyReading(now time.Time) model.HostMetrics {
	return model.HostMetrics{
		Key:       model.HostKeyLocal,
		Hostname:  "nas.local",
		Timestamp: now,
		CPU:       model.HostCPU{Cores: 4, UsagePct: pct(12), LoadPerCore: pct(0.3)},
		Memory:    model.HostMemory{TotalBytes: 1000, UsedBytes: 400, UsedPct: 40},
		Filesystems: []model.HostFilesystem{
			{Mount: "/", TotalBytes: 100, UsedPct: 30},
			{Mount: "/srv", TotalBytes: 100, UsedPct: 50},
		},
	}
}

func systemCheck(cfg model.CheckConfig) model.Check {
	return model.Check{ID: 1, Type: model.CheckSystem, Name: "Hardware", IntervalSeconds: 60, Config: cfg}
}

func TestEvaluateHostHealthy(t *testing.T) {
	now := time.Now()
	res := evaluateHost(systemCheck(model.SystemDefaults()), healthyReading(now), now)
	if !res.Success {
		t.Fatalf("a healthy machine should pass: %s", res.Error)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", res.Warnings)
	}
	if res.Details.Host == nil || res.Details.Host.Hostname != "nas.local" {
		t.Error("the reading should be attached to the result for the inspector")
	}
	if res.Details.HostAgeSec == nil || *res.Details.HostAgeSec > 1 {
		t.Errorf("age of a fresh reading: %v", res.Details.HostAgeSec)
	}
	if !strings.Contains(res.Message, "CPU 12%") || !strings.Contains(res.Message, "disk 50% (/srv)") {
		t.Errorf("message should summarise the reading, got %q", res.Message)
	}
}

// Crossing a warning threshold is a degraded check; crossing the critical one
// is a down check. They are different events, not the same alert twice.
func TestEvaluateHostWarningVersusCritical(t *testing.T) {
	now := time.Now()
	cfg := model.SystemDefaults()

	warm := healthyReading(now)
	warm.Filesystems[1].UsedPct = 88 // over the 85% warning, under the 95% critical
	res := evaluateHost(systemCheck(cfg), warm, now)
	if !res.Success {
		t.Fatalf("a warning should not fail the check: %s", res.Error)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "Disk /srv") {
		t.Fatalf("want one disk warning, got %v", res.Warnings)
	}
	// finalize is what turns warnings into the degraded status.
	if got := finalize(res, systemCheck(cfg), Options{}, now).Status; got != model.StatusDegraded {
		t.Errorf("want degraded, got %s", got)
	}

	full := healthyReading(now)
	full.Filesystems[1].UsedPct = 96
	res = evaluateHost(systemCheck(cfg), full, now)
	if res.Success {
		t.Fatal("a critical threshold should fail the check")
	}
	if !strings.Contains(res.Error, "Disk /srv is 96%") {
		t.Errorf("error should name the filesystem and the figure, got %q", res.Error)
	}
	// Even a failing result keeps the reading, so the inspector can show what
	// the machine looked like at the moment it went down.
	if res.Details.Host == nil {
		t.Error("the reading should survive a failure")
	}
}

// A machine that stops reporting is down. That is the whole point of an agent
// that pushes: silence is the signal.
func TestEvaluateHostStaleReading(t *testing.T) {
	now := time.Now()
	old := healthyReading(now.Add(-10 * time.Minute))
	res := evaluateHost(systemCheck(model.SystemDefaults()), old, now)
	if res.Success {
		t.Fatal("a ten minute old reading should fail a check that runs every minute")
	}
	if !strings.Contains(res.Error, "has not reported") {
		t.Errorf("error should say the machine stopped reporting, got %q", res.Error)
	}

	// An explicit allowance overrides the interval-derived default.
	cfg := model.SystemDefaults()
	cfg.StaleAfterSeconds = 3600
	if res := evaluateHost(systemCheck(cfg), old, now); !res.Success {
		t.Errorf("a one hour allowance should accept a ten minute old reading: %s", res.Error)
	}
}

func TestHumanAge(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{5 * time.Second, "5 seconds"},
		{time.Second, "1 second"},
		{time.Minute, "1 minute"},
		{90 * time.Second, "1 minute"},
		{10 * time.Minute, "10 minutes"},
		{3 * time.Hour, "3.0 hours"},
		{72 * time.Hour, "3 days"},
	}
	for _, tt := range tests {
		if got := humanAge(tt.d); got != tt.want {
			t.Errorf("humanAge(%s) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

// The staleness deadline is floored, so a check running every 10 seconds does
// not report the machine down over ordinary scheduling jitter.
func TestStaleAfterHasAFloor(t *testing.T) {
	quick := model.Check{Type: model.CheckSystem, IntervalSeconds: 10}
	if got := staleAfter(quick); got != minStaleSeconds*time.Second {
		t.Errorf("want the %ds floor, got %s", minStaleSeconds, got)
	}
	slow := model.Check{Type: model.CheckSystem, IntervalSeconds: 300}
	if got := staleAfter(slow); got != 900*time.Second {
		t.Errorf("want three intervals, got %s", got)
	}
}

func TestEvaluateHostOnlyWatchesSelectedMounts(t *testing.T) {
	now := time.Now()
	cfg := model.SystemDefaults()
	cfg.DiskMounts = []string{"/"}

	m := healthyReading(now)
	m.Filesystems[1].UsedPct = 99 // /srv is full, but the check does not watch it
	res := evaluateHost(systemCheck(cfg), m, now)
	if !res.Success || len(res.Warnings) != 0 {
		t.Fatalf("a filesystem outside the list must be ignored: %v %v", res.Error, res.Warnings)
	}

	// A named mount that is not present is not an error: a removable volume
	// that is unplugged is not a hardware fault.
	cfg.DiskMounts = []string{"/backup"}
	if res := evaluateHost(systemCheck(cfg), m, now); !res.Success {
		t.Errorf("an absent mount should not fail the check: %s", res.Error)
	}
}

// Where a platform cannot report processor utilisation, load per core carries
// the same meaning and the check must still watch something.
func TestEvaluateHostFallsBackToLoadPerCore(t *testing.T) {
	now := time.Now()
	m := healthyReading(now)
	m.CPU.UsagePct = nil
	m.CPU.LoadPerCore = pct(4)
	m.Warnings = []string{"processor utilisation is not available on macOS"}

	res := evaluateHost(systemCheck(model.SystemDefaults()), m, now)
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "Load per core") {
		t.Fatalf("want a load warning, got %v", res.Warnings)
	}
	// A collector gap is reported but is not itself a hardware problem.
	if !res.Success {
		t.Errorf("a collector gap should not fail the check: %s", res.Error)
	}
	if !strings.Contains(res.Message, "not available on macOS") {
		t.Errorf("the collector's own warning should be surfaced, got %q", res.Message)
	}
}

// A threshold set to zero is off, so a check can watch the disk and ignore
// everything else.
func TestEvaluateHostZeroThresholdIsOff(t *testing.T) {
	now := time.Now()
	m := healthyReading(now)
	m.CPU.UsagePct = pct(100)
	m.Memory.UsedPct = 100

	res := evaluateHost(systemCheck(model.CheckConfig{DiskWarnPct: 85}), m, now)
	if !res.Success || len(res.Warnings) != 0 {
		t.Fatalf("thresholds left at zero should be off: %v %v", res.Error, res.Warnings)
	}
}

func TestEvaluateHostToleratesAClockAhead(t *testing.T) {
	now := time.Now()
	future := healthyReading(now.Add(30 * time.Second))
	res := evaluateHost(systemCheck(model.SystemDefaults()), future, now)
	if !res.Success {
		t.Fatalf("a machine whose clock runs ahead is not stale: %s", res.Error)
	}
	if res.Details.HostAgeSec == nil || *res.Details.HostAgeSec != 0 {
		t.Errorf("a reading from the future should read as current, got %v", res.Details.HostAgeSec)
	}
}

// ---- fetching ----

type stubHosts struct {
	local model.HostMetrics
	agent map[int64]model.HostMetrics
	err   error
}

func (s stubHosts) LocalHost(context.Context) (model.HostMetrics, error) {
	return s.local, s.err
}

func (s stubHosts) AgentHost(_ context.Context, id int64) (model.HostMetrics, error) {
	m, ok := s.agent[id]
	if !ok {
		return model.HostMetrics{}, errNoAgentReading
	}
	return m, nil
}

var errNoAgentReading = &stubError{"no reading yet"}

type stubError struct{ msg string }

func (e *stubError) Error() string { return e.msg }

func TestRunSystemCheckReadsTheConfiguredSource(t *testing.T) {
	now := time.Now()
	hosts := stubHosts{
		local: healthyReading(now),
		agent: map[int64]model.HostMetrics{7: healthyReading(now)},
	}

	local := systemCheck(model.SystemDefaults())
	if res := Run(context.Background(), local, Options{Hosts: hosts}); !res.Success {
		t.Errorf("local source: %s", res.Error)
	}

	cfg := model.SystemDefaults()
	cfg.HostSource, cfg.AgentID = model.HostSourceAgent, 7
	if res := Run(context.Background(), systemCheck(cfg), Options{Hosts: hosts}); !res.Success {
		t.Errorf("agent source: %s", res.Error)
	}

	cfg.AgentID = 99
	res := Run(context.Background(), systemCheck(cfg), Options{Hosts: hosts})
	if res.Success || !strings.Contains(res.Error, "no reading from this machine yet") {
		t.Errorf("an agent that never reported should fail clearly, got %q", res.Error)
	}
}

// Without a reader the check says so rather than panicking. This is the path
// taken by anything running a check outside the engine.
func TestRunSystemCheckWithoutAReader(t *testing.T) {
	res := Run(context.Background(), systemCheck(model.SystemDefaults()), Options{})
	if res.Success || !strings.Contains(res.Error, "unavailable") {
		t.Errorf("want a clear failure, got %q / %q", res.Error, res.Message)
	}
}

func TestScrapeHostMetrics(t *testing.T) {
	now := time.Now()
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if gotAuth != "Bearer secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(healthyReading(now))
	}))
	defer srv.Close()

	cfg := model.SystemDefaults()
	cfg.HostSource = model.HostSourceURL
	cfg.MetricsURL = srv.URL + "/metrics"
	cfg.MetricsToken = "secret"
	check := systemCheck(cfg)

	res := Run(context.Background(), check, Options{})
	if !res.Success {
		t.Fatalf("scrape failed: %s", res.Error)
	}
	if gotAuth != "Bearer secret" {
		t.Errorf("the token should be sent as a bearer credential, got %q", gotAuth)
	}
	// A scraped machine has no identity of its own, so the reading is filed
	// under the check that fetched it.
	if res.Details.Host.Key != model.URLHostKey(check.ID) {
		t.Errorf("host key: got %q", res.Details.Host.Key)
	}

	cfg.MetricsToken = "wrong"
	res = Run(context.Background(), systemCheck(cfg), Options{})
	if res.Success || !strings.Contains(res.Error, "refused the token") {
		t.Errorf("want a token failure, got %q", res.Error)
	}
}

func TestScrapeHostMetricsRejectsNonsense(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html>not a hardware reading</html>"))
	}))
	defer srv.Close()

	cfg := model.CheckConfig{HostSource: model.HostSourceURL, MetricsURL: srv.URL}
	res := Run(context.Background(), systemCheck(cfg), Options{})
	if res.Success || !strings.Contains(res.Error, "did not return a GWatch hardware reading") {
		t.Errorf("want a clear parse failure, got %q", res.Error)
	}
}

func TestValidateSystemCheck(t *testing.T) {
	tests := []struct {
		name    string
		cfg     model.CheckConfig
		wantErr string
	}{
		{"defaults are valid", model.SystemDefaults(), ""},
		{"unknown source", model.CheckConfig{HostSource: "carrier pigeon"}, "unsupported hardware source"},
		{"agent with no machine", model.CheckConfig{HostSource: model.HostSourceAgent}, "which registered machine"},
		{"url with no url", model.CheckConfig{HostSource: model.HostSourceURL}, "metrics URL is required"},
		{"percentage out of range", model.CheckConfig{CPUWarnPct: 140}, "between 0 (off) and 100"},
		{"critical below warning", model.CheckConfig{DiskWarnPct: 90, DiskCritPct: 50}, "at or above its warning threshold"},
		{"negative load", model.CheckConfig{LoadWarnPerCore: -1}, "cannot be negative"},
		{"negative staleness", model.CheckConfig{StaleAfterSeconds: -5}, "cannot be negative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(systemCheck(tt.cfg), "")
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("want no error, got %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("want an error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

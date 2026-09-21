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

// metricByKey finds one metric's verdict in a result.
func metricByKey(t *testing.T, res model.Result, key string) model.MetricResult {
	t.Helper()
	for _, m := range res.Details.MetricResults {
		if m.Key == key {
			return m
		}
	}
	t.Fatalf("no metric %q in %+v", key, res.Details.MetricResults)
	return model.MetricResult{}
}

// Every reading becomes a metric with a key and its own verdict, and every one
// is recorded under that key so it can be charted and asked for by name.
func TestEvaluateHostRecordsEveryMetric(t *testing.T) {
	now := time.Now()
	m := healthyReading(now)
	m.Memory.SwapTotalBytes, m.Memory.SwapUsedPct = 1000, pct(3)
	m.Filesystems[1].InodesUsedPct = pct(12)
	m.Interfaces = []model.HostInterface{{Name: "eth0", RxBytesPerSec: pct(1500), TxBytesPerSec: pct(400)}}
	m.Disks = []model.HostDiskIO{{Name: "sda", ReadBytesPerSec: pct(20000), WriteBytesPerSec: pct(5000), BusyPct: pct(7)}}

	res := evaluateHost(systemCheck(model.SystemDefaults()), m, now)
	if !res.Success {
		t.Fatalf("a healthy machine should pass: %s", res.Error)
	}
	want := map[string]float64{
		"cpu": 12, "load": 0.3, "memory": 40, "swap": 3,
		"disk:/": 30, "disk:/srv": 50, "inodes:/srv": 12,
		"net:eth0.rx": 1500, "net:eth0.tx": 400,
		"diskio:sda.read": 20000, "diskio:sda.write": 5000, "diskio:sda.busy": 7,
	}
	for key, v := range want {
		if got, ok := res.Metrics[key]; !ok || got != v {
			t.Errorf("Metrics[%q] = %v (%v), want %v", key, got, ok, v)
		}
		mr := metricByKey(t, res, key)
		if mr.Status != model.StatusUp || mr.Reason != "" {
			t.Errorf("%s should be up with no reason, got %s %q", key, mr.Status, mr.Reason)
		}
		if mr.Unit != model.SystemMetricUnit(key) {
			t.Errorf("%s unit = %q, want %q", key, mr.Unit, model.SystemMetricUnit(key))
		}
	}
	if len(res.Metrics) != len(want) {
		t.Errorf("recorded %d metrics, want %d: %v", len(res.Metrics), len(want), res.Metrics)
	}
	if res.Details.Host == nil || res.Details.Host.Hostname != "nas.local" {
		t.Error("the reading should be attached to the result for the inspector")
	}
	if !strings.Contains(res.Message, "CPU 12%") || !strings.Contains(res.Message, "disk 50% (/srv)") {
		t.Errorf("message should summarise the reading, got %q", res.Message)
	}
}

// Each metric has its own verdict; the check's is the worst of them. A
// warning on the disk is a degraded check with the disk named, a critical one
// is a down check — and the memory beside it stays up either way.
func TestEvaluateHostJudgesMetricsSeparately(t *testing.T) {
	now := time.Now()
	cfg := model.SystemDefaults()

	warm := healthyReading(now)
	warm.Filesystems[1].UsedPct = 88 // over the 85% warning, under the 95% critical
	res := evaluateHost(systemCheck(cfg), warm, now)
	if !res.Success {
		t.Fatalf("a warning should not fail the check: %s", res.Error)
	}
	disk := metricByKey(t, res, "disk:/srv")
	if disk.Status != model.StatusDegraded || !strings.Contains(disk.Reason, "Disk /srv is 88%") || !strings.Contains(disk.Reason, "85% warning") {
		t.Errorf("disk verdict: %+v", disk)
	}
	if mem := metricByKey(t, res, "memory"); mem.Status != model.StatusUp {
		t.Errorf("memory should be unaffected by the disk, got %s", mem.Status)
	}
	if len(res.Warnings) != 1 || res.Warnings[0] != disk.Reason {
		t.Errorf("the warnings list should carry the metric's reason, got %v", res.Warnings)
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
	if got := metricByKey(t, res, "disk:/srv").Status; got != model.StatusDown {
		t.Errorf("disk verdict = %s, want down", got)
	}
	if !strings.Contains(res.Error, "Disk /srv is 96%") {
		t.Errorf("error should name the filesystem and the figure, got %q", res.Error)
	}
	// Even a failing result keeps the reading and the metrics, so the
	// inspector and the charts show what the machine looked like as it went.
	if res.Details.Host == nil || res.Metrics["disk:/srv"] != 96 || len(res.Details.MetricResults) == 0 {
		t.Error("the reading and metrics should survive a failure")
	}
}

// A threshold for one instance replaces the family's for that instance alone.
func TestEvaluateHostInstanceOverridesFamily(t *testing.T) {
	now := time.Now()
	cfg := model.CheckConfig{MetricThresholds: []model.MetricThreshold{
		{Metric: model.MetricDisk, Warn: model.Float(85), Crit: model.Float(95)},
		{Metric: "disk:/srv", Warn: model.Float(40)}, // the media disk is always fullish; no critical
	}}
	m := healthyReading(now)
	m.Filesystems[0].UsedPct = 50 // / follows the family: fine
	m.Filesystems[1].UsedPct = 99 // /srv follows its own entry: warning only
	res := evaluateHost(systemCheck(cfg), m, now)
	if !res.Success {
		t.Fatalf("the override has no critical level, so the check must not fail: %s", res.Error)
	}
	if got := metricByKey(t, res, "disk:/").Status; got != model.StatusUp {
		t.Errorf("/ = %s, want up", got)
	}
	if got := metricByKey(t, res, "disk:/srv").Status; got != model.StatusDegraded {
		t.Errorf("/srv = %s, want degraded from its own threshold", got)
	}

	// An interface entry governs both of its directions; a direction entry
	// governs only itself.
	cfg = model.CheckConfig{MetricThresholds: []model.MetricThreshold{
		{Metric: "net:eth0", Warn: model.Float(1000)},
		{Metric: "net:eth1.tx", Crit: model.Float(10)},
	}}
	m = healthyReading(now)
	m.Interfaces = []model.HostInterface{
		{Name: "eth0", RxBytesPerSec: pct(5000), TxBytesPerSec: pct(10)},
		{Name: "eth1", RxBytesPerSec: pct(5000), TxBytesPerSec: pct(50)},
	}
	res = evaluateHost(systemCheck(cfg), m, now)
	for key, want := range map[string]model.Status{"net:eth0.rx": model.StatusDegraded, "net:eth0.tx": model.StatusUp, "net:eth1.rx": model.StatusUp, "net:eth1.tx": model.StatusDown} {
		if got := metricByKey(t, res, key).Status; got != want {
			t.Errorf("%s = %s, want %s", key, got, want)
		}
	}
}

// A "below" threshold is for a reading where less is worse.
func TestEvaluateHostBelowThreshold(t *testing.T) {
	now := time.Now()
	cfg := model.CheckConfig{MetricThresholds: []model.MetricThreshold{
		{Metric: "net:eth0.rx", Warn: model.Float(100), Crit: model.Float(10), Below: true},
	}}
	m := healthyReading(now)
	m.Interfaces = []model.HostInterface{{Name: "eth0", RxBytesPerSec: pct(50)}}
	res := evaluateHost(systemCheck(cfg), m, now)
	rx := metricByKey(t, res, "net:eth0.rx")
	if rx.Status != model.StatusDegraded || !strings.Contains(rx.Reason, "at or below") {
		t.Errorf("50 B/s under a 100 B/s floor should warn, got %+v", rx)
	}
	m.Interfaces[0].RxBytesPerSec = pct(0)
	if got := metricByKey(t, evaluateHost(systemCheck(cfg), m, now), "net:eth0.rx").Status; got != model.StatusDown {
		t.Errorf("a silent interface should be critical, got %s", got)
	}
}

// A check saved before the list existed keeps working: its flat fields are
// read with the same meaning, inodes following the disk pair.
func TestEvaluateHostReadsLegacyThresholds(t *testing.T) {
	now := time.Now()
	legacy := model.CheckConfig{CPUWarnPct: 90, MemWarnPct: 90, MemCritPct: 97, DiskWarnPct: 85, DiskCritPct: 95, LoadWarnPerCore: 2}
	list := legacy.EffectiveMetricThresholds()
	keys := map[string]model.MetricThreshold{}
	for _, t := range list {
		keys[t.Metric] = t
	}
	if len(keys) != 5 || keys["inodes"].Warn == nil || *keys["inodes"].Crit != 95 || keys["swap"].Warn != nil {
		t.Fatalf("conversion = %+v", list)
	}
	if !legacy.HasSystemThresholds() {
		t.Error("legacy fields count as thresholds")
	}

	m := healthyReading(now)
	m.Filesystems[1].UsedPct = 88
	m.Filesystems[1].InodesUsedPct = pct(96)
	res := evaluateHost(systemCheck(legacy), m, now)
	if got := metricByKey(t, res, "disk:/srv").Status; got != model.StatusDegraded {
		t.Errorf("disk under legacy fields = %s, want degraded", got)
	}
	if got := metricByKey(t, res, "inodes:/srv").Status; got != model.StatusDown {
		t.Errorf("inodes under legacy fields = %s, want down", got)
	}
	// Normalising moves the check onto the list and clears the flat fields.
	legacy.NormalizeMetricThresholds()
	if len(legacy.MetricThresholds) != 5 || legacy.DiskCritPct != 0 {
		t.Errorf("normalised = %+v", legacy)
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
	if _, ok := res.Metrics["disk:/srv"]; ok {
		t.Error("an unwatched filesystem should not be recorded either")
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
	if got := metricByKey(t, res, "load").Status; got != model.StatusDegraded {
		t.Fatalf("want a load warning, got %s", got)
	}
	if _, ok := res.Metrics["cpu"]; ok {
		t.Error("an unavailable reading is absent, not zero")
	}
	// A collector gap is reported but is not itself a hardware problem.
	if !res.Success {
		t.Errorf("a collector gap should not fail the check: %s", res.Error)
	}
	if !strings.Contains(res.Message, "not available on macOS") {
		t.Errorf("the collector's own warning should be surfaced, got %q", res.Message)
	}
}

// A metric with no threshold is still measured and recorded, so a check can
// watch the disk and merely chart everything else.
func TestEvaluateHostUnthresholdedMetricIsRecordedNotJudged(t *testing.T) {
	now := time.Now()
	m := healthyReading(now)
	m.CPU.UsagePct = pct(100)
	m.Memory.UsedPct = 100

	res := evaluateHost(systemCheck(model.CheckConfig{MetricThresholds: []model.MetricThreshold{{Metric: model.MetricDisk, Warn: model.Float(85)}}}), m, now)
	if !res.Success || len(res.Warnings) != 0 {
		t.Fatalf("metrics without thresholds must not warn: %v %v", res.Error, res.Warnings)
	}
	if res.Metrics["cpu"] != 100 || metricByKey(t, res, "cpu").Status != model.StatusUp {
		t.Errorf("cpu should be recorded and up: %v", res.Metrics)
	}
	// A level of 0 on an "above" threshold is off, as the flat fields
	// treated it, so an old configuration that says "0 means off" still does.
	zero := model.CheckConfig{MetricThresholds: []model.MetricThreshold{{Metric: model.MetricCPU, Warn: model.Float(0)}}}
	if got := metricByKey(t, evaluateHost(systemCheck(zero), m, now), "cpu").Status; got != model.StatusUp {
		t.Errorf("a zero level should be off, got %s", got)
	}
}

// A hardware check's chartable metrics: the four every machine has, plus any
// instance a threshold names; the family entries themselves are not series.
func TestSystemCheckMetricUnits(t *testing.T) {
	cfg := model.SystemDefaults()
	cfg.MetricThresholds = append(cfg.MetricThresholds, model.MetricThreshold{Metric: "disk:/srv", Warn: model.Float(90)})
	units := systemCheck(cfg).MetricUnits()
	for key, want := range map[string]string{"cpu": "%", "memory": "%", "swap": "%", "load": "", "disk:/srv": "%"} {
		if got, ok := units[key]; !ok || got != want {
			t.Errorf("units[%q] = %q (%v), want %q", key, got, ok, want)
		}
	}
	if _, ok := units["disk"]; ok {
		t.Error("a family is not a series of its own")
	}
	// A key the configuration never mentioned still has a unit, from its family.
	if got := systemCheck(cfg).MetricUnit("net:eth0.rx"); got != "B/s" {
		t.Errorf("unit of an unlisted interface = %q, want B/s", got)
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
		{"list: unknown metric", model.CheckConfig{MetricThresholds: []model.MetricThreshold{{Metric: "gpu", Warn: model.Float(50)}}}, "unknown hardware metric"},
		{"list: instance on a singleton", model.CheckConfig{MetricThresholds: []model.MetricThreshold{{Metric: "cpu:0", Warn: model.Float(50)}}}, "cannot name an instance"},
		{"list: duplicate", model.CheckConfig{MetricThresholds: []model.MetricThreshold{{Metric: "disk", Warn: model.Float(50)}, {Metric: "disk", Warn: model.Float(60)}}}, "both apply"},
		{"list: percentage out of range", model.CheckConfig{MetricThresholds: []model.MetricThreshold{{Metric: "disk:/srv", Crit: model.Float(140)}}}, "between 0 (off) and 100"},
		{"list: critical below warning", model.CheckConfig{MetricThresholds: []model.MetricThreshold{{Metric: "memory", Warn: model.Float(90), Crit: model.Float(50)}}}, "at or above its warning threshold"},
		{"list: below with critical above warning", model.CheckConfig{MetricThresholds: []model.MetricThreshold{{Metric: "net:eth0", Warn: model.Float(10), Crit: model.Float(50), Below: true}}}, "at or below its warning threshold"},
		{"list: negative rate", model.CheckConfig{MetricThresholds: []model.MetricThreshold{{Metric: "net", Warn: model.Float(-1)}}}, "cannot be negative"},
		{"list: instance override is fine", model.CheckConfig{MetricThresholds: []model.MetricThreshold{{Metric: "disk", Warn: model.Float(85)}, {Metric: "disk:/srv", Warn: model.Float(95)}, {Metric: "diskio:sda.busy", Warn: model.Float(90)}}}, ""},
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

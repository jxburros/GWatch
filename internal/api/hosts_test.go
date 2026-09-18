package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// postReading submits a hardware reading the way an agent does, with the token
// in an Authorization header and nothing else.
func postReading(t *testing.T, url, token string, m model.HostMetrics) (int, string) {
	t.Helper()
	b, _ := json.Marshal(m)
	req, err := http.NewRequest(http.MethodPost, url+"/ingest/metrics", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.String()
}

func reading(hostname string, cpu float64) model.HostMetrics {
	return model.HostMetrics{
		Hostname:     hostname,
		OS:           "linux",
		Arch:         "arm64",
		AgentVersion: "1.0.0",
		Timestamp:    time.Now(),
		CPU:          model.HostCPU{Cores: 4, UsagePct: &cpu},
		Memory:       model.HostMemory{TotalBytes: 1000, UsedBytes: 300, UsedPct: 30},
		Filesystems:  []model.HostFilesystem{{Mount: "/", TotalBytes: 100, UsedPct: 42}},
	}
}

func TestAgentRegistrationAndIngest(t *testing.T) {
	ts, _ := newTestServer(t)

	var created struct {
		Token string      `json:"token"`
		Agent model.Agent `json:"agent"`
	}
	if code := call(t, ts, "POST", "/api/agents", map[string]any{"name": "nas"}, &created); code != 201 {
		t.Fatalf("register: %d", code)
	}
	if created.Token == "" || created.Agent.ID == 0 {
		t.Fatalf("no token minted: %+v", created)
	}
	// The token is shown once and only its hash is kept, so listing agents
	// must never hand it back.
	var agents []model.Agent
	call(t, ts, "GET", "/api/agents", nil, &agents)
	if len(agents) != 1 || agents[0].Prefix == "" {
		t.Fatalf("agent list: %+v", agents)
	}
	body, _ := json.Marshal(agents)
	if bytes.Contains(body, []byte(created.Token)) {
		t.Fatal("the agent list leaked the token")
	}

	if code, body := postReading(t, ts.URL, created.Token, reading("nas.local", 55)); code != 202 {
		t.Fatalf("ingest: %d %s", code, body)
	}

	var hosts []model.HostSummary
	call(t, ts, "GET", "/api/hosts", nil, &hosts)
	var nas *model.HostSummary
	for i := range hosts {
		if hosts[i].Key == created.Agent.HostKey() {
			nas = &hosts[i]
		}
	}
	if nas == nil {
		t.Fatalf("the reporting machine is missing from the hardware list: %+v", hosts)
	}
	if nas.Metrics == nil || nas.Metrics.Hostname != "nas.local" {
		t.Fatalf("reading not attached: %+v", nas)
	}
	if nas.Status != model.StatusUp || nas.Stale {
		t.Errorf("a machine that just reported should be up: %+v", nas)
	}
	// The reading is filed under the agent's own key whatever it claimed.
	if nas.Metrics.Key != created.Agent.HostKey() {
		t.Errorf("host key: got %q", nas.Metrics.Key)
	}
	// Reporting in records what the machine says it is.
	call(t, ts, "GET", "/api/agents", nil, &agents)
	if agents[0].LastSeenAt == nil || agents[0].Hostname != "nas.local" || agents[0].Arch != "arm64" {
		t.Errorf("the report was not recorded against the agent: %+v", agents[0])
	}

	var series struct {
		Samples []model.HostSample `json:"samples"`
	}
	call(t, ts, "GET", "/api/hosts/"+created.Agent.HostKey()+"/history?range=1h", nil, &series)
	if len(series.Samples) != 1 || series.Samples[0].CPUPct == nil || *series.Samples[0].CPUPct != 55 {
		t.Fatalf("history: %+v", series.Samples)
	}
}

// An agent token is not a way into GWatch. It opens the ingest route and
// nothing else, and the ingest route accepts nothing but a reading.
func TestAgentTokenGrantsNothingButIngest(t *testing.T) {
	ts, srv := newTestServer(t)
	var created struct {
		Token string      `json:"token"`
		Agent model.Agent `json:"agent"`
	}
	call(t, ts, "POST", "/api/agents", map[string]any{"name": "nas"}, &created)
	// A client on this computer is already an administrator, so the question
	// only means anything from somewhere else.
	allowTestRemote(srv)

	// Presented as an API key from off this machine, it is simply not a
	// credential: every route refuses it.
	for _, path := range []string{"/api/nodes", "/api/settings", "/api/hosts", "/api/agents"} {
		code, body, _ := as(t, ts, creds{APIKey: created.Token, Remote: "192.168.1.50:9999"}, "GET", path, nil, nil)
		if code != 401 && code != 403 {
			t.Errorf("%s accepted an agent token: %d %s", path, code, body)
		}
	}
	// And it cannot make changes through the ingest route either.
	if code, _ := postReading(t, ts.URL, "gwa_notarealtokenatallxxxxxxxxxxxxxxxxxxxxxxx", reading("x", 1)); code != 401 {
		t.Errorf("an unknown token should be refused, got %d", code)
	}
	if code, _ := postReading(t, ts.URL, "", reading("x", 1)); code != 401 {
		t.Errorf("no token should be refused, got %d", code)
	}
}

func TestRevokedAgentStopsBeingAccepted(t *testing.T) {
	ts, _ := newTestServer(t)
	var created struct {
		Token string      `json:"token"`
		Agent model.Agent `json:"agent"`
	}
	call(t, ts, "POST", "/api/agents", map[string]any{"name": "nas"}, &created)
	if code, _ := postReading(t, ts.URL, created.Token, reading("nas.local", 10)); code != 202 {
		t.Fatal("first reading should be accepted")
	}

	if code := call(t, ts, "DELETE", "/api/agents/1", nil, nil); code != 204 {
		t.Fatalf("revoke: %d", code)
	}
	if code, _ := postReading(t, ts.URL, created.Token, reading("nas.local", 10)); code != 401 {
		t.Errorf("a revoked token must stop working immediately, got %d", code)
	}

	// Revoking keeps the registration and its readings, so the record of what
	// that machine was doing survives.
	var hosts []model.HostSummary
	call(t, ts, "GET", "/api/hosts", nil, &hosts)
	found := false
	for _, h := range hosts {
		if h.Key == created.Agent.HostKey() {
			found = true
			if h.Status != model.StatusPaused {
				t.Errorf("a revoked machine should read as paused, got %s", h.Status)
			}
			if h.Metrics == nil {
				t.Error("its past readings should still be there")
			}
		}
	}
	if !found {
		t.Error("the revoked machine vanished from the list")
	}

	// Purging is the destructive form and takes the readings with it.
	if code := call(t, ts, "DELETE", "/api/agents/1?purge=1", nil, nil); code != 204 {
		t.Fatalf("purge: %d", code)
	}
	call(t, ts, "GET", "/api/hosts", nil, &hosts)
	for _, h := range hosts {
		if h.Key == created.Agent.HostKey() {
			t.Error("the purged machine is still listed")
		}
	}
}

// A machine that has stopped reporting is the signal an agent exists to give,
// so the list has to show it rather than the last reading as though it were
// current.
func TestHostSummaryMarksSilentMachines(t *testing.T) {
	fresh := hostSummary(model.HostSummary{Key: "agent:1", Source: model.HostSourceAgent},
		model.HostSample{Key: "agent:1", Timestamp: time.Now()})
	if fresh.Stale || fresh.Status != model.StatusUp {
		t.Errorf("a machine that just reported: %+v", fresh)
	}

	silent := hostSummary(model.HostSummary{Key: "agent:1", Source: model.HostSourceAgent},
		model.HostSample{Key: "agent:1", Timestamp: time.Now().Add(-time.Hour)})
	if !silent.Stale || silent.Status != model.StatusDown {
		t.Errorf("a machine that stopped reporting: %+v", silent)
	}

	never := hostSummary(model.HostSummary{Key: "agent:1", Source: model.HostSourceAgent}, model.HostSample{})
	if never.Status != model.StatusUnknown || never.Metrics != nil {
		t.Errorf("a machine that never reported: %+v", never)
	}
}

func TestLocalHardwareIsListed(t *testing.T) {
	ts, srv := newTestServer(t)
	// The sampler runs on its own schedule; ask for a reading directly so the
	// test does not depend on when the first tick lands.
	if _, err := srv.Engine.Hosts().LocalHost(t.Context()); err != nil {
		t.Skipf("no hardware readings on this platform: %v", err)
	}

	var hosts []model.HostSummary
	if code := call(t, ts, "GET", "/api/hosts", nil, &hosts); code != 200 {
		t.Fatalf("list: %d", code)
	}
	if len(hosts) == 0 || hosts[0].Key != model.HostKeyLocal {
		t.Fatalf("this computer should be the first machine listed: %+v", hosts)
	}
	if hosts[0].Source != model.HostSourceLocal {
		t.Errorf("source: %q", hosts[0].Source)
	}
}

func TestDownsampleHostSamplesKeepsTheShape(t *testing.T) {
	var in []model.HostSample
	base := time.Now().Add(-time.Hour)
	for i := 0; i < 1000; i++ {
		v := float64(i % 100)
		in = append(in, model.HostSample{Key: "local", Timestamp: base.Add(time.Duration(i) * time.Second), CPUPct: &v})
	}
	out := downsampleHostSamples(in, 100)
	if len(out) != 100 {
		t.Fatalf("want 100 points, got %d", len(out))
	}
	if !out[0].Timestamp.Before(out[99].Timestamp) {
		t.Error("points are not in time order")
	}
	for _, s := range out {
		if s.CPUPct == nil {
			t.Fatal("a bucket lost its value")
		}
	}
	// Below the limit nothing is touched: a short range shows every reading
	// exactly as it was taken.
	if got := downsampleHostSamples(in[:50], 100); len(got) != 50 {
		t.Errorf("want the readings untouched, got %d", len(got))
	}
}

// Averaging must not invent a zero for a metric no reading in the bucket had.
func TestAverageHostSamplesKeepsMissingMetricsMissing(t *testing.T) {
	cpu := 40.0
	bucket := []model.HostSample{
		{Key: "local", Timestamp: time.Now(), CPUPct: &cpu},
		{Key: "local", Timestamp: time.Now()},
	}
	got := averageHostSamples(bucket)
	if got.CPUPct == nil || *got.CPUPct != 40 {
		t.Errorf("processor: %v", got.CPUPct)
	}
	if got.MemPct != nil {
		t.Errorf("memory was never reported and must stay absent, got %v", *got.MemPct)
	}
}

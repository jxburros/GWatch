package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/auth"
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

// mintPairing asks for a pairing code the way the Hardware page does.
func mintPairing(t *testing.T, ts *httptest.Server, name string) (code string, pairing model.PairingCode) {
	t.Helper()
	var out struct {
		Code    string            `json:"code"`
		Pairing model.PairingCode `json:"pairing"`
	}
	if status := call(t, ts, "POST", "/api/agents/pairings", map[string]any{"name": name}, &out); status != 201 {
		t.Fatalf("mint pairing code: %d", status)
	}
	if out.Code == "" || out.Pairing.ID == 0 {
		t.Fatalf("no code minted: %+v", out)
	}
	return out.Code, out.Pairing
}

// redeem presents a code the way gwatch-agent does: no credential at all, just
// the code and what the machine says it is.
func redeem(t *testing.T, ts *httptest.Server, code string) (int, string) {
	t.Helper()
	b, _ := json.Marshal(map[string]string{
		"code": code, "hostname": "nas.local", "os": "linux", "arch": "arm64", "version": "1.0.0",
	})
	resp, err := http.Post(ts.URL+"/api/agents/pair", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.String()
}

func TestPairingCodeEnrolsOneMachine(t *testing.T) {
	ts, _ := newTestServer(t)
	code, pairing := mintPairing(t, ts, "nas")

	if pairing.ExpiresAt.Sub(pairing.CreatedAt) > pairingCodeLifetime+time.Minute {
		t.Errorf("a code should be short-lived, this one lasts %s", pairing.ExpiresAt.Sub(pairing.CreatedAt))
	}
	// The code is shown once. Listing the invitations afterwards must not
	// hand it back, exactly as listing agents must not hand back a token.
	var list []model.PairingCode
	call(t, ts, "GET", "/api/agents/pairings", nil, &list)
	if len(list) != 1 || list[0].Redeemed() {
		t.Fatalf("pairing list: %+v", list)
	}
	blob, _ := json.Marshal(list)
	if bytes.Contains(blob, []byte(code)) {
		t.Fatal("the pairing list leaked the code")
	}

	// Typed with the dashes in the wrong place and in lower case, as it will
	// be, it still works.
	typed := strings.ToLower(strings.ReplaceAll(code, "-", " "))
	status, body := redeem(t, ts, typed)
	if status != 201 {
		t.Fatalf("redeem: %d %s", status, body)
	}
	var out struct {
		Token string      `json:"token"`
		Agent model.Agent `json:"agent"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if !auth.LooksLikeAgentToken(out.Token) {
		t.Fatalf("a pairing should yield an agent token, got %q", out.Token)
	}
	if out.Agent.Name != "nas" || out.Agent.Hostname != "nas.local" || out.Agent.OS != "linux" {
		t.Fatalf("unexpected agent: %+v", out.Agent)
	}

	// The token that came out is a real one, and it is good for exactly the
	// one thing an agent token is ever good for.
	if code, body := postReading(t, ts.URL, out.Token, reading("nas.local", 42)); code != 202 {
		t.Fatalf("the paired token should be accepted: %d %s", code, body)
	}

	// The invitation is spent, and says which machine it produced.
	call(t, ts, "GET", "/api/agents/pairings", nil, &list)
	if len(list) != 1 || !list[0].Redeemed() || list[0].AgentID == nil || *list[0].AgentID != out.Agent.ID {
		t.Fatalf("the invitation should record the machine it enrolled: %+v", list)
	}
	if status, _ := redeem(t, ts, code); status != 401 {
		t.Errorf("a spent code must not enrol a second machine, got %d", status)
	}
}

// Unknown, malformed, expired, cancelled and already-used codes must be
// indistinguishable from outside: anything else is an oracle a guesser can
// use to learn which of its attempts was close.
func TestPairingRejectionsAreIndistinguishable(t *testing.T) {
	ts, srv := newTestServer(t)
	ctx := context.Background()

	spent, _ := mintPairing(t, ts, "spent")
	if status, body := redeem(t, ts, spent); status != 201 {
		t.Fatalf("setup: %d %s", status, body)
	}
	cancelled, cancelledPairing := mintPairing(t, ts, "cancelled")
	if status := call(t, ts, "DELETE", fmt.Sprintf("/api/agents/pairings/%d", cancelledPairing.ID), nil, nil); status != 204 {
		t.Fatalf("cancel: %d", status)
	}
	// An expired code cannot be made through the API — it would mean waiting a
	// quarter of an hour — so it is minted straight into the store.
	expired, err := auth.NewPairingCode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Store.CreatePairingCode(ctx, "expired", nil, auth.HashToken(expired), "test",
		time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	unknown, err := auth.NewPairingCode()
	if err != nil {
		t.Fatal(err)
	}

	var first string
	for _, c := range []struct{ label, code string }{
		{"unknown", unknown},
		{"expired", expired},
		{"cancelled", cancelled},
		{"already used", spent},
		{"not a code at all", "hello there"},
		{"an agent token", "gwa_abcdefghijklmnopqrstuvwxyz234567"},
		{"empty", ""},
	} {
		status, body := redeem(t, ts, c.code)
		if status != http.StatusUnauthorized {
			t.Errorf("%s: got %d, want 401", c.label, status)
			continue
		}
		if first == "" {
			first = body
		} else if body != first {
			t.Errorf("%s answered %q, but an unknown code answers %q; the two must not be tellable apart",
				c.label, strings.TrimSpace(body), strings.TrimSpace(first))
		}
	}
	if !strings.Contains(first, pairingRejected) {
		t.Errorf("the rejection should say what to do next, got %q", first)
	}
	// Nothing was enrolled by any of that.
	var agents []model.Agent
	call(t, ts, "GET", "/api/agents", nil, &agents)
	if len(agents) != 1 {
		t.Errorf("only the one real pairing should have registered a machine, got %d", len(agents))
	}
}

// Guessing is what a short code has to survive, so a wrong one costs from the
// same per-IP failure budget as a wrong password.
func TestPairingIsRateLimited(t *testing.T) {
	ts, _ := newTestServer(t)
	wrong, err := auth.NewPairingCode()
	if err != nil {
		t.Fatal(err)
	}
	limited := false
	for i := 0; i < failureLimit+2; i++ {
		status, _ := redeem(t, ts, wrong)
		if status == http.StatusTooManyRequests {
			limited = true
			break
		}
		if status != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d", i, status)
		}
	}
	if !limited {
		t.Fatalf("guessing should run out of attempts within %d tries", failureLimit+2)
	}
	// A real code presented from a blocked address is refused too: the limiter
	// fails closed rather than letting one lucky guess through.
	code, _ := mintPairing(t, ts, "nas")
	if status, _ := redeem(t, ts, code); status != http.StatusTooManyRequests {
		t.Errorf("the limiter should hold even for a valid code, got %d", status)
	}
}

// Redeeming is the one agent route with no credential on it, so the routes
// around it must still be shut.
func TestPairingAdminRoutesNeedAnAdministrator(t *testing.T) {
	ts, srv := newTestServer(t)
	allowTestRemote(srv)
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/agents/pairings"},
		{"POST", "/api/agents/pairings"},
		{"DELETE", "/api/agents/pairings/1"},
	} {
		status, body, _ := as(t, ts, creds{Remote: "192.168.1.50:9999"}, c.method, c.path, nil, nil)
		if status != 401 && status != 403 {
			t.Errorf("%s %s was open to a stranger: %d %s", c.method, c.path, status, body)
		}
	}
	// Minting a code needs a name, exactly as registering a machine does.
	if status := call(t, ts, "POST", "/api/agents/pairings", map[string]any{"name": "  "}, nil); status != 400 {
		t.Errorf("a nameless invitation should be refused, got %d", status)
	}
	if status := call(t, ts, "POST", "/api/agents/pairings", map[string]any{"name": "nas", "nodeId": 4242}, nil); status != 400 {
		t.Errorf("an invitation for a node that does not exist should be refused, got %d", status)
	}
}

// A machine is a node like any other, so pairing one has to leave a node
// behind carrying a hardware check pointed at that machine. Without it a
// paired machine would report readings nothing was watching.
func TestPairingCreatesTheMachinesNode(t *testing.T) {
	ts, _ := newTestServer(t)
	code, _ := mintPairing(t, ts, "nas")

	var before []model.Node
	call(t, ts, "GET", "/api/nodes", nil, &before)
	node := findNodeNamed(t, before, "nas")
	if len(node.Checks) != 1 || node.Checks[0].Type != model.CheckSystem {
		t.Fatalf("the node should carry one hardware check: %+v", node.Checks)
	}
	if node.Checks[0].Enabled {
		t.Error("the check has no machine to read yet, so it should start switched off")
	}

	status, body := redeem(t, ts, code)
	if status != 201 {
		t.Fatalf("redeem: %d %s", status, body)
	}
	var out struct {
		Agent model.Agent `json:"agent"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if out.Agent.NodeID == nil || *out.Agent.NodeID != node.ID {
		t.Fatalf("the agent should belong to the node made for it: %+v", out.Agent)
	}

	var after []model.Node
	call(t, ts, "GET", "/api/nodes", nil, &after)
	node = findNodeNamed(t, after, "nas")
	if len(node.Checks) != 1 {
		t.Fatalf("pairing should not add checks: %+v", node.Checks)
	}
	c := node.Checks[0]
	if !c.Enabled || c.Config.HostSource != model.HostSourceAgent || c.Config.AgentID != out.Agent.ID {
		t.Fatalf("the check should now read the paired machine: enabled=%v config=%+v", c.Enabled, c.Config)
	}
}

func findNodeNamed(t *testing.T, nodes []model.Node, name string) model.Node {
	t.Helper()
	for _, n := range nodes {
		if n.Name == name {
			return n
		}
	}
	t.Fatalf("no node named %q in %+v", name, nodes)
	return model.Node{}
}

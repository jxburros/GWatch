package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// postResults sends a batch the way a collector does.
func postResults(t *testing.T, ts *httptest.Server, token string, batch model.CollectorBatch) (int, model.CollectorBatchReply) {
	t.Helper()
	b, _ := json.Marshal(batch)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/ingest/results", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out model.CollectorBatchReply
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func setCollector(t *testing.T, ts *httptest.Server, id int64, on bool) model.Agent {
	t.Helper()
	var out model.Agent
	if code := call(t, ts, "PUT", "/api/agents/"+itoa(id)+"/collector", map[string]any{"enabled": on}, &out); code != 200 {
		t.Fatalf("set collector: %d", code)
	}
	return out
}

// The whole round trip, and the rule it rests on: the checks — and the SNMP
// community one of them carries — go to an agent only when an administrator
// has turned collecting on for it AND the machine says its own configuration
// allows it. Either half alone gets nothing.
func TestCollectorNeedsBothSidesAndThenRunsTheNodesChecks(t *testing.T) {
	ts, srv := newTestServer(t)
	a := registerAgent(t, ts, "branch-pi")
	id := a.Agent.ID

	var node model.Node
	code := call(t, ts, "POST", "/api/nodes", model.Node{Name: "Branch router", Host: "192.168.50.1", Enabled: true, CollectorID: &id, Checks: []model.Check{
		{Type: model.CheckPing, Name: "Ping", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5},
		{Type: model.CheckSNMP, Name: "SNMP", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5,
			Config: model.CheckConfig{SNMPCommunity: "s3cret", SNMPOIDs: []model.SNMPOID{{OID: "1.3.6.1.2.1.1.3.0", Name: "uptime"}}}},
	}}, &node)
	if code != 201 && code != 200 {
		t.Fatalf("create node: %d", code)
	}
	if node.CollectorID == nil || *node.CollectorID != id {
		t.Fatalf("the node should check from the agent: %+v", node.CollectorID)
	}
	ping := node.Checks[0].ID

	// The machine allows it, GWatch does not (yet).
	if _, s := checkIn(t, ts, a.Token, model.AgentCheckIn{Collector: true}); s.Collector != nil {
		t.Fatal("an agent GWatch has not made a collector was handed checks")
	}
	if got := getAgent(t, ts, id); got.CollectorStatus != model.CollectorOff || !got.CollectorAllowed || got.CollectorChecks != 2 {
		t.Fatalf("agent before: %+v", got)
	}
	if code, _ := postResults(t, ts, a.Token, model.CollectorBatch{}); code != http.StatusForbidden {
		t.Fatalf("results from a non-collector: %d", code)
	}

	// GWatch does, the machine does not.
	setCollector(t, ts, id, true)
	if _, s := checkIn(t, ts, a.Token, model.AgentCheckIn{}); s.Collector != nil {
		t.Fatal("checks (and their community string) went to a machine that has not opted in")
	}
	if got := getAgent(t, ts, id); got.CollectorStatus != model.CollectorWaiting {
		t.Fatalf("status %q, want waiting", got.CollectorStatus)
	}

	// Both.
	_, s := checkIn(t, ts, a.Token, model.AgentCheckIn{Collector: true})
	if s.Collector == nil || len(s.Collector.Checks) != 2 {
		t.Fatalf("the collector should be given its two checks: %+v", s.Collector)
	}
	if s.Collector.Checks[1].Config.SNMPCommunity != "s3cret" || s.Collector.Checks[0].Target != "192.168.50.1" {
		t.Fatalf("the definitions are incomplete: %+v", s.Collector.Checks)
	}
	if s.CheckInSeconds > 300 {
		t.Errorf("a collector should check in often, not every %ds", s.CheckInSeconds)
	}

	// Run now is refused here: this server is not where the check runs.
	if code := call(t, ts, "POST", "/api/checks/"+itoa(ping)+"/run", nil, nil); code != http.StatusConflict {
		t.Fatalf("run now on a collected check: %d", code)
	}

	lat := 3.0
	code, reply := postResults(t, ts, a.Token, model.CollectorBatch{Results: []model.CollectorResult{
		{Result: model.Result{CheckID: ping, Timestamp: time.Now(), Success: true, Status: model.StatusUp, Message: "ok", LatencyMS: &lat}},
	}})
	if code != http.StatusAccepted || reply.Accepted != 1 {
		t.Fatalf("results: %d %+v", code, reply)
	}
	if st, _ := srv.Engine.State(ping); st.Status != model.StatusUp {
		t.Fatalf("the result did not reach the check: %+v", st)
	}
	if got := getAgent(t, ts, id); got.CollectorStatus != model.CollectorRunning || got.CollectorSeenAt == nil {
		t.Fatalf("agent after results: %+v", got)
	}

	// A restore pauses the engine: the batch is refused with a 503 the
	// collector holds its results through.
	if err := srv.Engine.Pause(context.Background()); err != nil {
		t.Fatal(err)
	}
	if code, _ := postResults(t, ts, a.Token, model.CollectorBatch{}); code != http.StatusServiceUnavailable {
		t.Fatalf("results during a pause: %d", code)
	}
	srv.Engine.Resume()

	// Off again: no checks at the next check-in, and results are refused.
	setCollector(t, ts, id, false)
	if _, s := checkIn(t, ts, a.Token, model.AgentCheckIn{Collector: true}); s.Collector != nil {
		t.Fatal("a collector turned off was still handed checks")
	}
	if code, _ := postResults(t, ts, a.Token, model.CollectorBatch{}); code != http.StatusForbidden {
		t.Fatalf("results after turning off: %d", code)
	}
}

func TestNodeCannotCheckFromAMissingOrRevokedAgent(t *testing.T) {
	ts, _ := newTestServer(t)
	missing := int64(4242)
	if code := call(t, ts, "POST", "/api/nodes", model.Node{Name: "x", Host: "10.0.0.1", Enabled: true, CollectorID: &missing}, nil); code != http.StatusBadRequest {
		t.Fatalf("missing collector: %d", code)
	}
	a := registerAgent(t, ts, "gone")
	if code := call(t, ts, "DELETE", "/api/agents/"+itoa(a.Agent.ID), nil, nil); code != http.StatusNoContent {
		t.Fatalf("revoke: %d", code)
	}
	id := a.Agent.ID
	if code := call(t, ts, "POST", "/api/nodes", model.Node{Name: "y", Host: "10.0.0.2", Enabled: true, CollectorID: &id}, nil); code != http.StatusBadRequest {
		t.Fatalf("revoked collector: %d", code)
	}
	zero := int64(0)
	var n model.Node
	if code := call(t, ts, "POST", "/api/nodes", model.Node{Name: "z", Host: "10.0.0.3", Enabled: true, CollectorID: &zero}, &n); code != 201 && code != 200 {
		t.Fatalf("collector 0 means this server: %d", code)
	}
	if n.CollectorID != nil {
		t.Fatal("collector 0 should be stored as this server")
	}
}

// The new routes keep to the agent-route rules: /ingest/results needs an
// agent token and nothing else will do, and the collector switch is an
// administrator's.
func TestCollectorRoutesAreGuarded(t *testing.T) {
	ts, _ := newTestServer(t)
	if code, _ := postResults(t, ts, "gwa_not_a_real_token_at_all", model.CollectorBatch{}); code != http.StatusUnauthorized {
		t.Fatalf("results with a bad token: %d", code)
	}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/ingest/results", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET results: %d", resp.StatusCode)
	}
	if pol := policyFor("PUT", "/api/agents/7/collector"); pol.Level != levelAdmin || pol.Key != keyDeny {
		t.Fatalf("the collector switch should be an administrator's alone: %+v", pol)
	}
}

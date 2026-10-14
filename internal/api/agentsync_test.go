package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jxburros/GWatch/internal/auth"
	"github.com/jxburros/GWatch/internal/model"
)

type registered struct {
	Token string      `json:"token"`
	Agent model.Agent `json:"agent"`
}

func registerAgent(t *testing.T, ts *httptest.Server, name string) registered {
	t.Helper()
	var out registered
	if code := call(t, ts, "POST", "/api/agents", map[string]any{"name": name}, &out); code != 201 {
		t.Fatalf("register %s: %d", name, code)
	}
	return out
}

// checkIn asks for settings the way an agent does: a token and nothing else.
func checkIn(t *testing.T, ts *httptest.Server, token string, body any) (int, model.AgentSettings) {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/ingest/checkin", rdr)
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
	var out model.AgentSettings
	if resp.StatusCode == 200 {
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode, out
}

func getAgent(t *testing.T, ts *httptest.Server, id int64) model.Agent {
	t.Helper()
	var agents []model.Agent
	call(t, ts, "GET", "/api/agents", nil, &agents)
	for _, a := range agents {
		if a.ID == id {
			return a
		}
	}
	t.Fatalf("agent %d not listed", id)
	return model.Agent{}
}

type revealed struct {
	Token    string `json:"token"`
	Pending  string `json:"pending"`
	Viewable bool   `json:"viewable"`
}

func reveal(t *testing.T, ts *httptest.Server, id int64) revealed {
	t.Helper()
	var out revealed
	if code := call(t, ts, "POST", "/api/agents/"+itoa(id)+"/token", nil, &out); code != 200 {
		t.Fatalf("reveal: %d", code)
	}
	return out
}

func TestAgentTokenCanBeReadBackByAnAdministrator(t *testing.T) {
	ts, _ := newTestServer(t)
	a := registerAgent(t, ts, "nas")

	if !getAgent(t, ts, a.Agent.ID).TokenViewable {
		t.Fatal("a freshly registered agent's token should be viewable")
	}
	got := reveal(t, ts, a.Agent.ID)
	if !got.Viewable || got.Token != a.Token {
		t.Fatalf("revealed %+v, want the token that was issued", got)
	}
	// Viewing a credential is an event someone should be able to find later.
	var events []model.Event
	call(t, ts, "GET", "/api/events?limit=50", nil, &events)
	seen := false
	for _, e := range events {
		if strings.Contains(e.Title, "Agent token viewed") {
			seen = true
		}
	}
	if !seen {
		t.Error("reading a token back should be audited")
	}
	// The list never carries it, whatever the viewable flag says.
	var agents []model.Agent
	call(t, ts, "GET", "/api/agents", nil, &agents)
	blob, _ := json.Marshal(agents)
	if bytes.Contains(blob, []byte(a.Token)) {
		t.Fatal("the agent list leaked the token")
	}
}

func TestPairedAgentTokenIsViewableToo(t *testing.T) {
	ts, _ := newTestServer(t)
	code, _ := mintPairing(t, ts, "nas")
	status, body := redeem(t, ts, code)
	if status != 201 {
		t.Fatalf("redeem: %d %s", status, body)
	}
	var out registered
	_ = json.Unmarshal([]byte(body), &out)
	if got := reveal(t, ts, out.Agent.ID); got.Token != out.Token {
		t.Fatalf("revealed %q, want the paired token", got.Token)
	}
}

// An agent registered before tokens were kept only has a digest. It must keep
// working, and say plainly that its token cannot be shown.
func TestAgentWithOnlyADigestCannotBeRevealed(t *testing.T) {
	ts, srv := newTestServer(t)
	token, prefix, _ := auth.NewAgentToken()
	agent, err := srv.Store.CreateAgent(context.Background(), "legacy", nil, prefix, auth.HashToken(token), "test")
	if err != nil {
		t.Fatal(err)
	}
	if getAgent(t, ts, agent.ID).TokenViewable {
		t.Error("a digest-only agent should not claim a viewable token")
	}
	if got := reveal(t, ts, agent.ID); got.Viewable || got.Token != "" {
		t.Errorf("revealed %+v for a digest-only agent", got)
	}
	if code, _ := postReading(t, ts.URL, token, reading("legacy", 10)); code != 202 {
		t.Errorf("a digest-only agent should still report, got %d", code)
	}
}

func TestRotationIsPickedUpAtCheckIn(t *testing.T) {
	ts, _ := newTestServer(t)
	a := registerAgent(t, ts, "nas")

	var rot struct {
		Token      string      `json:"token"`
		RetiredOld bool        `json:"retiredOld"`
		Agent      model.Agent `json:"agent"`
	}
	if code := call(t, ts, "POST", "/api/agents/"+itoa(a.Agent.ID)+"/rotate", map[string]any{}, &rot); code != 200 {
		t.Fatalf("rotate: %d", code)
	}
	if rot.Token == "" || rot.Token == a.Token || rot.RetiredOld || !rot.Agent.RotationPending {
		t.Fatalf("unexpected rotation: %+v", rot)
	}
	// Until the new token is used, the old one is still good: that is what
	// makes the changeover gapless.
	if code, _ := postReading(t, ts.URL, a.Token, reading("nas", 10)); code != 202 {
		t.Fatalf("the old token should work during a rotation, got %d", code)
	}
	pending := reveal(t, ts, a.Agent.ID)
	if pending.Token != a.Token || pending.Pending != rot.Token {
		t.Fatalf("both tokens should be viewable while a rotation waits: %+v", pending)
	}

	// An agent holding the old token is handed the new one.
	code, settings := checkIn(t, ts, a.Token, nil)
	if code != 200 || settings.Token != rot.Token {
		t.Fatalf("check-in with the old token: %d %+v", code, settings)
	}
	// The new token's first use retires the old one.
	if code, _ := postReading(t, ts.URL, rot.Token, reading("nas", 11)); code != 202 {
		t.Fatalf("the new token should work: %d", code)
	}
	if code, _ := postReading(t, ts.URL, a.Token, reading("nas", 12)); code != 401 {
		t.Errorf("the old token should be dead once the new one is used, got %d", code)
	}
	after := getAgent(t, ts, a.Agent.ID)
	if after.RotationPending {
		t.Error("the rotation should be finished")
	}
	if got := reveal(t, ts, a.Agent.ID); got.Token != rot.Token || got.Pending != "" {
		t.Errorf("after the rotation the new token is the token: %+v", got)
	}
	if after.Prefix != auth.KeyPrefix(rot.Token) {
		t.Errorf("prefix %q should follow the token %q", after.Prefix, auth.KeyPrefix(rot.Token))
	}
	// Having the new token, a check-in is told nothing more about tokens.
	if _, settings := checkIn(t, ts, rot.Token, nil); settings.Token != "" {
		t.Error("a finished rotation should not hand the token out again")
	}
}

// Replacing now is for a token that may have leaked, so it must not hand the
// replacement to anyone who asks, and the old token must die at once.
func TestRetireOldRotationDeliversNothing(t *testing.T) {
	ts, _ := newTestServer(t)
	a := registerAgent(t, ts, "nas")
	var rot struct {
		Token string `json:"token"`
	}
	if code := call(t, ts, "POST", "/api/agents/"+itoa(a.Agent.ID)+"/rotate", map[string]any{"retireOld": true}, &rot); code != 200 {
		t.Fatalf("rotate: %d", code)
	}
	if code, _ := postReading(t, ts.URL, a.Token, reading("nas", 1)); code != 401 {
		t.Errorf("the old token should stop working at once, got %d", code)
	}
	if code, _ := postReading(t, ts.URL, rot.Token, reading("nas", 1)); code != 202 {
		t.Errorf("the new token should work, got %d", code)
	}
	if code, _ := checkIn(t, ts, a.Token, nil); code != 401 {
		t.Errorf("the old token should not be able to check in, got %d", code)
	}
	if _, settings := checkIn(t, ts, rot.Token, nil); settings.Token != "" {
		t.Error("nothing should be delivered after a replace-now rotation")
	}
}

func TestRotationCanBeCancelled(t *testing.T) {
	ts, _ := newTestServer(t)
	a := registerAgent(t, ts, "nas")
	var rot struct {
		Token string `json:"token"`
	}
	call(t, ts, "POST", "/api/agents/"+itoa(a.Agent.ID)+"/rotate", map[string]any{}, &rot)
	if code := call(t, ts, "POST", "/api/agents/"+itoa(a.Agent.ID)+"/rotate/cancel", nil, nil); code != 204 {
		t.Fatalf("cancel: %d", code)
	}
	if getAgent(t, ts, a.Agent.ID).RotationPending {
		t.Error("a cancelled rotation should not be pending")
	}
	if code, _ := postReading(t, ts.URL, rot.Token, reading("nas", 1)); code != 401 {
		t.Errorf("a cancelled replacement must not work, got %d", code)
	}
	if _, settings := checkIn(t, ts, a.Token, nil); settings.Token != "" {
		t.Error("a cancelled rotation should hand nothing out")
	}
}

func TestCheckInNeverHandsOneMachinesTokenToAnother(t *testing.T) {
	ts, _ := newTestServer(t)
	a := registerAgent(t, ts, "a")
	b := registerAgent(t, ts, "b")
	call(t, ts, "POST", "/api/agents/"+itoa(a.Agent.ID)+"/rotate", map[string]any{}, nil)
	if _, settings := checkIn(t, ts, b.Token, nil); settings.Token != "" {
		t.Fatal("machine b was given a token while a was being rotated")
	}
}

func TestCheckInStoresInventoryAndReturnsTheSelection(t *testing.T) {
	ts, _ := newTestServer(t)
	a := registerAgent(t, ts, "nas")

	// Nothing chosen yet: the answer is the automatic selection, a sane
	// interval and no token.
	code, settings := checkIn(t, ts, a.Token, model.AgentCheckIn{
		Version: "0.7.0",
		Inventory: &model.Inventory{
			Interfaces: []model.InventoryItem{{Name: "eth0", Auto: true}, {Name: "docker0"}},
			Mounts:     []model.InventoryItem{{Name: "/", Auto: true}},
		},
	})
	if code != 200 || !settings.Collect.IsZero() || settings.CheckInSeconds <= 0 || settings.Token != "" {
		t.Fatalf("first check-in: %d %+v", code, settings)
	}
	got := getAgent(t, ts, a.Agent.ID)
	if got.Inventory == nil || len(got.Inventory.Interfaces) != 2 || got.CheckedInAt == nil {
		t.Fatalf("the inventory should be kept: %+v", got)
	}

	// An administrator chooses; the next check-in carries it.
	sel := model.CollectSelection{Metrics: []string{"cpu", "net"}, Interfaces: []string{"eth0"}}
	if code := call(t, ts, "PUT", "/api/agents/"+itoa(a.Agent.ID)+"/collect", sel, nil); code != 200 {
		t.Fatalf("set collect: %d", code)
	}
	_, settings = checkIn(t, ts, a.Token, nil)
	if len(settings.Collect.Interfaces) != 1 || settings.Collect.Interfaces[0] != "eth0" || len(settings.Collect.Metrics) != 2 {
		t.Fatalf("selection not returned: %+v", settings.Collect)
	}
	// A check-in without an inventory leaves the last one alone.
	if got := getAgent(t, ts, a.Agent.ID); got.Inventory == nil {
		t.Error("an empty check-in wiped the inventory")
	}

	// Garbage is refused rather than stored.
	if code := call(t, ts, "PUT", "/api/agents/"+itoa(a.Agent.ID)+"/collect", map[string]any{"metrics": []string{"nonsense"}}, nil); code != 400 {
		t.Errorf("an unknown metric family should be refused, got %d", code)
	}
	// And back to automatic.
	call(t, ts, "PUT", "/api/agents/"+itoa(a.Agent.ID)+"/collect", model.CollectSelection{}, nil)
	if _, settings = checkIn(t, ts, a.Token, nil); !settings.Collect.IsZero() {
		t.Errorf("an empty selection means automatic, got %+v", settings.Collect)
	}
}

func TestCheckInNeedsAValidTokenAndPost(t *testing.T) {
	ts, _ := newTestServer(t)
	if code, _ := checkIn(t, ts, "", nil); code != 401 {
		t.Errorf("no token: %d", code)
	}
	if code, _ := checkIn(t, ts, "gwa_notarealtokenatallxxxxxxxxxxxxxxxxxxxxxxx", nil); code != 401 {
		t.Errorf("unknown token: %d", code)
	}
	resp, err := http.Get(ts.URL + "/ingest/checkin")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET should be refused, got %d", resp.StatusCode)
	}
}

func TestAgentCredentialRoutesNeedAnAdministrator(t *testing.T) {
	ts, srv := newTestServer(t)
	allowTestRemote(srv)
	a := registerAgent(t, ts, "nas")
	base := "/api/agents/" + itoa(a.Agent.ID)
	for _, c := range []struct{ method, path string }{
		{"POST", base + "/token"},
		{"POST", base + "/rotate"},
		{"POST", base + "/rotate/cancel"},
		{"PUT", base + "/collect"},
	} {
		// A stranger, and an agent's own token presented as an API key.
		for _, cr := range []creds{{Remote: "192.168.1.50:9999"}, {APIKey: a.Token, Remote: "192.168.1.50:9999"}} {
			status, body, _ := as(t, ts, cr, c.method, c.path, nil, nil)
			if status != 401 && status != 403 {
				t.Errorf("%s %s was open to %+v: %d %s", c.method, c.path, cr, status, body)
			}
		}
	}
}

func TestRevokedMachineCannotCheckInOrBeRotated(t *testing.T) {
	ts, _ := newTestServer(t)
	a := registerAgent(t, ts, "nas")
	call(t, ts, "DELETE", "/api/agents/"+itoa(a.Agent.ID), nil, nil)
	if code, _ := checkIn(t, ts, a.Token, nil); code != 401 {
		t.Errorf("a revoked machine checked in: %d", code)
	}
	if code := call(t, ts, "POST", "/api/agents/"+itoa(a.Agent.ID)+"/rotate", map[string]any{}, nil); code != 409 {
		t.Errorf("rotating a revoked machine should be refused, got %d", code)
	}
}

// What is chosen while adding a machine rides with its pairing code and is the
// agent's selection from the moment it exists.
func TestChoicesMadeWhileAddingAMachineReachTheAgent(t *testing.T) {
	ts, _ := newTestServer(t)
	var minted struct {
		Code string `json:"code"`
	}
	sel := model.CollectSelection{Metrics: []string{"cpu", "memory", "disk"}}
	if status := call(t, ts, "POST", "/api/agents/pairings", map[string]any{"name": "nas", "collect": sel}, &minted); status != 201 {
		t.Fatalf("mint: %d", status)
	}
	status, body := redeem(t, ts, minted.Code)
	if status != 201 {
		t.Fatalf("redeem: %d %s", status, body)
	}
	var out registered
	_ = json.Unmarshal([]byte(body), &out)
	if len(out.Agent.Collect.Metrics) != 3 {
		t.Errorf("the redeemed agent should carry the choice: %+v", out.Agent.Collect)
	}
	_, settings := checkIn(t, ts, out.Token, nil)
	if len(settings.Collect.Metrics) != 3 {
		t.Errorf("the first check-in should deliver it: %+v", settings.Collect)
	}
	if status := call(t, ts, "POST", "/api/agents/pairings", map[string]any{"name": "x", "collect": map[string]any{"metrics": []string{"gpu"}}}, nil); status != 400 {
		t.Errorf("nonsense should be refused when the code is minted, got %d", status)
	}
}

// Choosing "add the machine to this node" has to end with the node watching the
// machine, even though an existing node has no waiting hardware check.
func TestPairingToAnExistingNodeAddsItsHardwareCheck(t *testing.T) {
	ts, _ := newTestServer(t)
	var node model.Node
	if status := call(t, ts, "POST", "/api/nodes", model.Node{Name: "Existing NAS", Host: "192.168.1.9", Enabled: true,
		Checks: []model.Check{{Type: model.CheckPing, Name: "Ping", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5, Retries: 1, FailureThreshold: 2}}}, &node); status != 201 {
		t.Fatalf("create node: %d", status)
	}
	var minted struct {
		Code string `json:"code"`
	}
	if status := call(t, ts, "POST", "/api/agents/pairings", map[string]any{"name": "nas", "nodeId": node.ID}, &minted); status != 201 {
		t.Fatalf("mint: %d", status)
	}
	status, body := redeem(t, ts, minted.Code)
	if status != 201 {
		t.Fatalf("redeem: %d %s", status, body)
	}
	var out registered
	_ = json.Unmarshal([]byte(body), &out)

	var after model.Node
	call(t, ts, "GET", "/api/nodes/"+itoa(node.ID), nil, &after)
	var hw []model.Check
	for _, c := range after.Checks {
		if c.Type == model.CheckSystem {
			hw = append(hw, c)
		}
	}
	if len(after.Checks) != 2 || len(hw) != 1 || !hw[0].Enabled || hw[0].Config.AgentID != out.Agent.ID || hw[0].Config.HostSource != model.HostSourceAgent {
		t.Fatalf("the node should keep its ping and gain a hardware check reading the machine: %+v", after.Checks)
	}

	// Pairing a second machine to the same node adds a second check rather than
	// stealing the first one's.
	call(t, ts, "POST", "/api/agents/pairings", map[string]any{"name": "nas2", "nodeId": node.ID}, &minted)
	if status, _ := redeem(t, ts, minted.Code); status != 201 {
		t.Fatal("second redeem failed")
	}
	call(t, ts, "GET", "/api/nodes/"+itoa(node.ID), nil, &after)
	n := 0
	for _, c := range after.Checks {
		if c.Type == model.CheckSystem {
			n++
		}
	}
	if n != 2 {
		t.Errorf("each machine gets its own hardware check, got %d", n)
	}
}

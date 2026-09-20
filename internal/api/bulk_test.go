package api

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jxburros/GWatch/internal/engine"
	"github.com/jxburros/GWatch/internal/model"
)

// makeBulkNodes creates two nodes with a ping and an HTTP check each. Every
// bulk test starts from the same shape, so the counts in the assertions mean
// something on their own.
func makeBulkNodes(t *testing.T, ts *httptest.Server) (a, b nodeDoc) {
	t.Helper()
	mk := func(name, host, group string) nodeDoc {
		body := map[string]any{"name": name, "host": host, "groups": []string{group}, "tags": []string{"core"}, "enabled": true, "importance": "normal",
			"checks": []map[string]any{
				{"type": "ping", "name": name + " ping", "enabled": true, "intervalSeconds": 60, "timeoutSeconds": 5, "config": map[string]any{"pingCount": 4}},
				{"type": "http", "name": name + " web", "enabled": true, "intervalSeconds": 60, "timeoutSeconds": 10, "config": map[string]any{"target": "https://" + host + "/", "expectedStatus": "200-399"}},
			}}
		var out nodeDoc
		if code := call(t, ts, "POST", "/api/nodes", body, &out); code != 201 {
			t.Fatalf("create %s: %d", name, code)
		}
		return out
	}
	return mk("Alpha", "10.0.0.1", "Home"), mk("Beta", "10.0.0.2", "Home")
}

func TestBulkEditAppliesOneIntervalAcrossNodesAndChecks(t *testing.T) {
	ts, srv := newTestServer(t)
	a, b := makeBulkNodes(t, ts)

	// A third node, not selected: it must come back untouched, which is what
	// makes "applies to the selection" a claim rather than a coincidence.
	var c nodeDoc
	call(t, ts, "POST", "/api/nodes", map[string]any{"name": "Gamma", "host": "10.0.0.3", "enabled": true,
		"checks": []map[string]any{{"type": "ping", "name": "Gamma ping", "enabled": true, "intervalSeconds": 60, "timeoutSeconds": 5}}}, &c)

	// Listen for the engine reload, so the test can show the running
	// configuration was told about the change and told once.
	updates := srv.Engine.Subscribe()
	defer srv.Engine.Unsubscribe(updates)

	before := countEvents(t, ts)
	var res bulkResponse
	body := map[string]any{
		"nodeIds":  []int64{a.ID},
		"checkIds": []int64{b.Checks[0].ID},
		"check":    map[string]any{"intervalSeconds": 120},
	}
	if code := call(t, ts, "PATCH", "/api/nodes/bulk", body, &res); code != 200 {
		t.Fatalf("bulk: %d", code)
	}
	// Both of Alpha's checks, and the one check of Beta that was named.
	if res.Checks != 3 || res.Nodes != 0 {
		t.Fatalf("counts: %+v", res)
	}
	if len(res.Changes) != 1 || res.Changes[0] != "interval → 120 s" {
		t.Fatalf("changes: %+v", res.Changes)
	}

	for _, id := range []int64{a.Checks[0].ID, a.Checks[1].ID, b.Checks[0].ID} {
		if got := intervalOf(t, ts, id); got != 120 {
			t.Errorf("check %d: interval %d, want 120", id, got)
		}
	}
	// Beta's second check was in neither list, and neither was Gamma.
	if got := intervalOf(t, ts, b.Checks[1].ID); got != 60 {
		t.Errorf("unselected check of a partly selected node changed to %d", got)
	}
	if got := intervalOf(t, ts, c.Checks[0].ID); got != 60 {
		t.Errorf("unselected node changed to %d", got)
	}

	// One reload, and exactly one entry in the timeline naming what happened.
	if n := drainConfigUpdates(updates); n != 1 {
		t.Errorf("engine reloaded %d times, want 1", n)
	}
	events := recentEvents(t, ts)
	if len(events)-before != 1 {
		t.Fatalf("bulk edit recorded %d events, want 1", len(events)-before)
	}
	ev := events[0]
	if ev.Type != model.EventConfigChanged || !strings.Contains(ev.Title, "interval → 120 s on 3 checks") {
		t.Fatalf("audit entry: %+v", ev)
	}
	if ev.Actor == "" {
		t.Errorf("bulk edit was not attributed: %+v", ev)
	}
}

func TestBulkEditTypeFilterNarrowsTheSelection(t *testing.T) {
	ts, _ := newTestServer(t)
	a, b := makeBulkNodes(t, ts)

	var res bulkResponse
	body := map[string]any{
		"nodeIds":     []int64{a.ID, b.ID},
		"check":       map[string]any{"intervalSeconds": 300},
		"checkFilter": map[string]any{"types": []string{"ping"}},
	}
	if code := call(t, ts, "PATCH", "/api/nodes/bulk", body, &res); code != 200 {
		t.Fatalf("bulk: %d", code)
	}
	if res.Checks != 2 {
		t.Fatalf("filter should have left two ping checks: %+v", res)
	}
	if intervalOf(t, ts, a.Checks[0].ID) != 300 || intervalOf(t, ts, b.Checks[0].ID) != 300 {
		t.Error("the ping checks did not take the new interval")
	}
	if intervalOf(t, ts, a.Checks[1].ID) != 60 || intervalOf(t, ts, b.Checks[1].ID) != 60 {
		t.Error("an HTTP check was changed despite the ping filter")
	}

	// A type nothing can run is refused rather than quietly matching nothing.
	if code := call(t, ts, "PATCH", "/api/nodes/bulk", map[string]any{
		"nodeIds": []int64{a.ID}, "check": map[string]any{"intervalSeconds": 300},
		"checkFilter": map[string]any{"types": []string{"smoke-signal"}},
	}, nil); code != 400 {
		t.Fatalf("unknown check type: %d", code)
	}
	// A filter that matches nothing in the selection says so.
	if code := call(t, ts, "PATCH", "/api/nodes/bulk", map[string]any{
		"nodeIds": []int64{a.ID}, "check": map[string]any{"intervalSeconds": 300},
		"checkFilter": map[string]any{"types": []string{"dns"}},
	}, nil); code != 400 {
		t.Fatalf("filter matching nothing: %d", code)
	}
}

func TestBulkEditGroupsTagsAndImportance(t *testing.T) {
	ts, _ := newTestServer(t)
	a, b := makeBulkNodes(t, ts)

	var res bulkResponse
	body := map[string]any{
		"nodeIds": []int64{a.ID, b.ID},
		"node": map[string]any{
			"addGroups":    []string{"Critical"},
			"removeGroups": []string{"home"}, // case-insensitive, as everywhere else
			"addTags":      []string{"paged", "CORE"},
			"removeTags":   []string{"nonexistent"},
			"importance":   "high",
		},
	}
	if code := call(t, ts, "PATCH", "/api/nodes/bulk", body, &res); code != 200 {
		t.Fatalf("bulk: %d", code)
	}
	if res.Nodes != 2 || res.Checks != 0 {
		t.Fatalf("counts: %+v", res)
	}
	for _, id := range []int64{a.ID, b.ID} {
		n := getNode(t, ts, id)
		if len(n.Groups) != 1 || n.Groups[0] != "Critical" {
			t.Errorf("node %d groups %v", id, n.Groups)
		}
		if n.Group != "Critical" {
			t.Errorf("node %d: the deprecated alias was not kept in step: %q", id, n.Group)
		}
		// "CORE" folds onto the "core" already there rather than doubling it.
		if len(n.Tags) != 2 || n.Tags[0] != "core" || n.Tags[1] != "paged" {
			t.Errorf("node %d tags %v", id, n.Tags)
		}
		if n.Importance != model.ImportanceHigh {
			t.Errorf("node %d importance %q", id, n.Importance)
		}
	}

	// Replacing the whole list is a separate verb from adding to it.
	if code := call(t, ts, "PATCH", "/api/nodes/bulk", map[string]any{
		"nodeIds": []int64{a.ID}, "node": map[string]any{"tags": []string{"only"}},
	}, nil); code != 200 {
		t.Fatal("replace tags")
	}
	if tags := getNode(t, ts, a.ID).Tags; len(tags) != 1 || tags[0] != "only" {
		t.Errorf("tags after replace: %v", tags)
	}
	// An invalid importance is refused before anything is written.
	if code := call(t, ts, "PATCH", "/api/nodes/bulk", map[string]any{
		"nodeIds": []int64{a.ID}, "node": map[string]any{"importance": "urgent"},
	}, nil); code != 400 {
		t.Fatalf("invalid importance: %d", code)
	}
}

func TestBulkEditDisablesChecksAndTheEngineNotices(t *testing.T) {
	ts, srv := newTestServer(t)
	a, _ := makeBulkNodes(t, ts)

	var res bulkResponse
	if code := call(t, ts, "PATCH", "/api/nodes/bulk", map[string]any{
		"nodeIds": []int64{a.ID}, "check": map[string]any{"enabled": false},
	}, &res); code != 200 {
		t.Fatalf("bulk: %d", code)
	}
	if res.Checks != 2 || len(res.Changes) != 1 || res.Changes[0] != "disabled" {
		t.Fatalf("response: %+v", res)
	}
	// Pausing a check is something only the engine does, and only when it has
	// reloaded — so this is the running configuration answering, not the row.
	n := getNode(t, ts, a.ID)
	for _, c := range n.Checks {
		if c.Enabled {
			t.Errorf("check %q still enabled", c.Name)
		}
		if st, ok := srv.Engine.State(c.ID); !ok || st.Status != model.StatusPaused {
			t.Errorf("check %q: engine state %+v", c.Name, st)
		}
	}
}

func TestBulkEditRefusesWhatItCannotDo(t *testing.T) {
	ts, _ := newTestServer(t)
	a, _ := makeBulkNodes(t, ts)

	cases := []struct {
		why  string
		body map[string]any
	}{
		{"nothing selected", map[string]any{"check": map[string]any{"intervalSeconds": 120}}},
		{"nothing to change", map[string]any{"nodeIds": []int64{a.ID}}},
		{"an empty patch is still nothing to change", map[string]any{"nodeIds": []int64{a.ID}, "node": map[string]any{}}},
		{"an unknown config key", map[string]any{"nodeIds": []int64{a.ID}, "check": map[string]any{"config": map[string]any{"pingCount": 9}}}},
		{"an unknown check field", map[string]any{"nodeIds": []int64{a.ID}, "check": map[string]any{"name": "renamed"}}},
		{"an unknown node field", map[string]any{"nodeIds": []int64{a.ID}, "node": map[string]any{"host": "10.0.0.9"}}},
		{"a check type change", map[string]any{"nodeIds": []int64{a.ID}, "check": map[string]any{"type": "tcp"}}},
		{"an interval under the minimum", map[string]any{"nodeIds": []int64{a.ID}, "check": map[string]any{"intervalSeconds": 3}}},
		{"a node that is gone", map[string]any{"nodeIds": []int64{a.ID + 9999}, "node": map[string]any{"importance": "low"}}},
		{"a node depending on itself", map[string]any{"nodeIds": []int64{a.ID}, "node": map[string]any{"dependsOnNodeId": a.ID}}},
	}
	for _, c := range cases {
		if code := call(t, ts, "PATCH", "/api/nodes/bulk", c.body, nil); code != 400 {
			t.Errorf("%s: got %d, want 400", c.why, code)
		}
	}

	// The unknown-config-key message names the key and the ones that would
	// have worked, because the caller cannot see this list anywhere else.
	code, body, _ := as(t, ts, creds{}, "PATCH", "/api/nodes/bulk", map[string]any{
		"nodeIds": []int64{a.ID}, "check": map[string]any{"config": map[string]any{"pingCount": 9}},
	}, nil)
	if code != 400 || !strings.Contains(body, "pingCount") || !strings.Contains(body, "latencyWarnMs") {
		t.Fatalf("unknown key message: %d %s", code, body)
	}

	// Nothing above wrote anything.
	if intervalOf(t, ts, a.Checks[0].ID) != 60 || getNode(t, ts, a.ID).Importance != model.ImportanceNormal {
		t.Error("a refused bulk edit changed something anyway")
	}
}

func TestBulkEditSetsWhitelistedConfigAndAlerts(t *testing.T) {
	ts, _ := newTestServer(t)
	a, _ := makeBulkNodes(t, ts)

	var res bulkResponse
	if code := call(t, ts, "PATCH", "/api/nodes/bulk", map[string]any{
		"nodeIds": []int64{a.ID},
		"check": map[string]any{
			"config": map[string]any{"latencyWarnMs": 250, "certWarnDays": 21},
			"alerts": map[string]any{"enabled": false, "cooldownMinutes": 30},
		},
	}, &res); code != 200 {
		t.Fatalf("bulk: %d", code)
	}
	if len(res.Changes) != 3 {
		t.Fatalf("changes: %+v", res.Changes)
	}
	for _, c := range getNode(t, ts, a.ID).Checks {
		if c.Config.LatencyWarnMS != 250 || c.Config.CertWarnDays != 21 {
			t.Errorf("check %q config %+v", c.Name, c.Config)
		}
		if c.Alerts == nil || c.Alerts.Enabled == nil || *c.Alerts.Enabled || c.Alerts.CooldownMinutes == nil || *c.Alerts.CooldownMinutes != 30 {
			t.Errorf("check %q alerts %+v", c.Name, c.Alerts)
		}
	}
	// An explicit null puts every check back on the global settings.
	if code := call(t, ts, "PATCH", "/api/nodes/bulk", map[string]any{
		"nodeIds": []int64{a.ID}, "check": map[string]any{"alerts": nil},
	}, nil); code != 200 {
		t.Fatal("clear alerts")
	}
	for _, c := range getNode(t, ts, a.ID).Checks {
		if c.Alerts != nil {
			t.Errorf("check %q still overrides alerts: %+v", c.Name, c.Alerts)
		}
	}
	// A ping method nothing supports is caught by the same validator the
	// single-check path uses.
	if code := call(t, ts, "PATCH", "/api/nodes/bulk", map[string]any{
		"nodeIds": []int64{a.ID}, "checkFilter": map[string]any{"types": []string{"ping"}},
		"check": map[string]any{"config": map[string]any{"pingMethod": "telepathy"}},
	}, nil); code != 400 {
		t.Error("an invalid ping method should be refused")
	}
}

func TestBulkEditIsAdminOnlyAndRefusesAReadOnlyKey(t *testing.T) {
	ts, srv := newTestServer(t)
	allowTestRemote(srv)
	a, _ := makeBulkNodes(t, ts)

	admin := bootstrapAdmin(t, ts, "pat", "correct horse battery")
	readKey := mintKey(t, ts, admin, "reader", "read")
	writeKey := mintKey(t, ts, admin, "writer", "readwrite")
	viewerCookie := func() string {
		if code, body, _ := as(t, ts, creds{Cookie: admin}, "POST", "/api/users", map[string]string{"username": "view", "password": "correct horse battery", "role": "viewer"}, nil); code != 201 {
			t.Fatalf("create viewer: %d %s", code, body)
		}
		return login(t, ts, "view", "correct horse battery")
	}()

	patch := map[string]any{"nodeIds": []int64{a.ID}, "node": map[string]any{"importance": "low"}}
	for _, c := range []struct {
		why  string
		c    creds
		want int
	}{
		{"a read-only key", creds{APIKey: readKey}, 403},
		{"a viewer", creds{Cookie: viewerCookie}, 403},
		{"a client off this machine with no credential", creds{Remote: "192.168.1.30:1"}, 401},
		{"a read-write key", creds{APIKey: writeKey}, 200},
		{"an administrator", creds{Cookie: admin}, 200},
	} {
		if code, body, _ := as(t, ts, c.c, "PATCH", "/api/nodes/bulk", patch, nil); code != c.want {
			t.Errorf("%s: %d, want %d (%s)", c.why, code, c.want, body)
		}
	}
}

// ---- helpers ----

func getNode(t *testing.T, ts *httptest.Server, id int64) model.Node {
	t.Helper()
	var doc nodeDoc
	if code := call(t, ts, "GET", fmt.Sprintf("/api/nodes/%d", id), nil, &doc); code != 200 {
		t.Fatalf("get node %d: %d", id, code)
	}
	return doc.Node
}

func intervalOf(t *testing.T, ts *httptest.Server, checkID int64) int {
	t.Helper()
	var nodes []nodeDoc
	if code := call(t, ts, "GET", "/api/nodes", nil, &nodes); code != 200 {
		t.Fatalf("list nodes: %d", code)
	}
	for _, n := range nodes {
		for _, c := range n.Checks {
			if c.ID == checkID {
				return c.IntervalSeconds
			}
		}
	}
	t.Fatalf("check %d not found", checkID)
	return 0
}

func recentEvents(t *testing.T, ts *httptest.Server) []model.Event {
	t.Helper()
	var evs []model.Event
	if code := call(t, ts, "GET", "/api/events?type=config_changed&limit=50", nil, &evs); code != 200 {
		t.Fatalf("events: %d", code)
	}
	return evs
}

func countEvents(t *testing.T, ts *httptest.Server) int {
	t.Helper()
	return len(recentEvents(t, ts))
}

// drainConfigUpdates counts the "config" broadcasts waiting on the channel.
func drainConfigUpdates(ch chan engine.Update) int {
	n := 0
	for {
		select {
		case u := <-ch:
			if u.Kind == "config" {
				n++
			}
		default:
			return n
		}
	}
}

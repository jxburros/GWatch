package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jxburros/GWatch/internal/model"
)

// The rules API (#31): validation answers 400 with a reason, a saved rule
// comes back with its state, the test endpoint runs every action, and a
// delete takes the rule away.
func TestRulesAPI(t *testing.T) {
	ts, _ := newTestServer(t)
	var hits int32
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer hook.Close()

	var a, b nodeDoc
	for i, doc := range []*nodeDoc{&a, &b} {
		if code := call(t, ts, "POST", "/api/nodes", map[string]any{"name": fmt.Sprintf("DNS %d", i+1), "host": fmt.Sprintf("192.168.1.%d", i+2), "enabled": true,
			"checks": []map[string]any{{"type": "dns", "name": "DNS", "enabled": true, "intervalSeconds": 60, "config": map[string]any{"target": "example.com"}}}}, doc); code != 201 {
			t.Fatalf("create node %d: %d", i+1, code)
		}
	}
	checkA := a.Checks[0].ID
	action := map[string]any{"type": "http", "url": hook.URL}
	condA := map[string]any{"kind": "status", "checkId": checkA, "status": "down"}
	condB := map[string]any{"kind": "status", "nodeId": b.ID, "status": "degraded"}

	// Validation, one reason at a time.
	bad := []struct {
		name string
		body map[string]any
		want string
	}{
		{"no name", map[string]any{"join": "all", "conditions": []any{condA}, "actions": []any{action}}, "name"},
		{"no conditions", map[string]any{"name": "x", "join": "all", "conditions": []any{}, "actions": []any{action}}, "condition"},
		{"no actions", map[string]any{"name": "x", "join": "all", "conditions": []any{condA}, "actions": []any{}}, "action"},
		{"bad join", map[string]any{"name": "x", "join": "most", "conditions": []any{condA}, "actions": []any{action}}, "join"},
		{"at_least too big", map[string]any{"name": "x", "join": "at_least", "atLeast": 3, "conditions": []any{condA, condB}, "actions": []any{action}}, "between 1 and 2"},
		{"at_least zero", map[string]any{"name": "x", "join": "at_least", "atLeast": 0, "conditions": []any{condA}, "actions": []any{action}}, "between 1 and 1"},
		{"unknown check", map[string]any{"name": "x", "join": "all", "conditions": []any{map[string]any{"kind": "status", "checkId": 9999, "status": "down"}}, "actions": []any{action}}, "check does not exist"},
		{"unknown node", map[string]any{"name": "x", "join": "all", "conditions": []any{map[string]any{"kind": "status", "nodeId": 9999, "status": "down"}}, "actions": []any{action}}, "node does not exist"},
		{"neither node nor check", map[string]any{"name": "x", "join": "all", "conditions": []any{map[string]any{"kind": "status", "status": "down"}}, "actions": []any{action}}, "pick a node"},
		{"bad status", map[string]any{"name": "x", "join": "all", "conditions": []any{map[string]any{"kind": "status", "checkId": checkA, "status": "up"}}, "actions": []any{action}}, "down or degraded"},
		{"bad kind", map[string]any{"name": "x", "join": "all", "conditions": []any{map[string]any{"kind": "metric", "checkId": checkA, "status": "down"}}, "actions": []any{action}}, "kind"},
		{"bad action", map[string]any{"name": "x", "join": "all", "conditions": []any{condA}, "actions": []any{map[string]any{"type": "http"}}}, "action 1"},
	}
	for _, c := range bad {
		code, body := callBody(t, ts, "POST", "/api/rules", c.body)
		if code != 400 {
			t.Fatalf("%s: expected 400, got %d", c.name, code)
		}
		if !strings.Contains(strings.ToLower(body), strings.ToLower(c.want)) {
			t.Fatalf("%s: error %q should mention %q", c.name, body, c.want)
		}
	}

	// Create: the answer carries the rule and a not-met state.
	var created ruleDoc
	if code := call(t, ts, "POST", "/api/rules", map[string]any{"name": "Both DNS servers in trouble", "enabled": true, "join": "at_least", "atLeast": 2,
		"conditions": []any{condA, condB}, "actions": []any{action, map[string]any{"type": "http", "url": hook.URL + "/second"}}, "cooldownMinutes": 10, "notifyCleared": true}, &created); code != 200 {
		t.Fatalf("create rule: %d", code)
	}
	if created.ID == 0 || created.Join != model.RuleJoinAtLeast || created.AtLeast != 2 || len(created.Conditions) != 2 || len(created.Actions) != 2 || created.State.Met || created.State.RuleID != created.ID {
		t.Fatalf("created: %+v", created)
	}
	if created.Conditions[0].CheckID == nil || *created.Conditions[0].CheckID != checkA || created.Conditions[0].NodeID != nil {
		t.Fatalf("check condition: %+v", created.Conditions[0])
	}
	var list []ruleDoc
	if code := call(t, ts, "GET", "/api/rules", nil, &list); code != 200 || len(list) != 1 || list[0].Name != created.Name {
		t.Fatalf("list: %d %+v", code, list)
	}
	var one ruleDoc
	if code := call(t, ts, "GET", fmt.Sprintf("/api/rules/%d", created.ID), nil, &one); code != 200 || one.ID != created.ID {
		t.Fatalf("get: %d %+v", code, one)
	}
	if code := call(t, ts, "GET", "/api/rules/9999", nil, nil); code != 404 {
		t.Fatalf("get missing: %d", code)
	}

	// Update keeps the id; "all" drops the count.
	created.Join = model.RuleJoinAll
	created.Enabled = false
	var updated ruleDoc
	if code := call(t, ts, "PUT", fmt.Sprintf("/api/rules/%d", created.ID), created.Rule, &updated); code != 200 || updated.ID != created.ID || updated.Join != model.RuleJoinAll || updated.AtLeast != 0 || updated.Enabled {
		t.Fatalf("update: %d %+v", code, updated)
	}

	// The test endpoint runs each action once with sample values.
	var results []model.ActionResult
	if code := call(t, ts, "POST", fmt.Sprintf("/api/rules/%d/test", created.ID), nil, &results); code != 200 || len(results) != 2 || !results[0].OK || !results[1].OK {
		t.Fatalf("test: %d %+v", code, results)
	}
	if atomic.LoadInt32(&hits) != 2 {
		t.Fatalf("test should have called the hook twice, got %d", hits)
	}

	// Saving and deleting leave audit entries.
	var events []model.Event
	call(t, ts, "GET", "/api/events?type=config_changed&limit=50", nil, &events)
	if !hasEventTitled(events, "Rule saved: Both DNS servers in trouble") {
		t.Fatalf("no audit entry for the save: %+v", events)
	}
	if code := call(t, ts, "DELETE", fmt.Sprintf("/api/rules/%d", created.ID), nil, nil); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	if code := call(t, ts, "DELETE", fmt.Sprintf("/api/rules/%d", created.ID), nil, nil); code != 404 {
		t.Fatalf("second delete: %d", code)
	}
	call(t, ts, "GET", "/api/rules", nil, &list)
	if len(list) != 0 {
		t.Fatalf("list after delete: %+v", list)
	}
	call(t, ts, "GET", "/api/events?type=config_changed&limit=50", nil, &events)
	if !hasEventTitled(events, "Rule deleted: Both DNS servers in trouble") {
		t.Fatalf("no audit entry for the delete: %+v", events)
	}
}

func hasEventTitled(events []model.Event, title string) bool {
	for _, e := range events {
		if e.Title == title {
			return true
		}
	}
	return false
}

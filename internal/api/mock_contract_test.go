package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// The browser's demo backend, web/mock.js, answers the same routes as this
// package and is kept in step with it by hand (#118). This test is the Go half
// of the contract that keeps the two honest: it seeds a test server, asks it
// a set of GETs and writes the *shape* of each answer — which keys, holding
// which JSON types — to tests/contract/api-shapes.json. The node half,
// tests/web/mock-contract.test.mjs, asks web/mock.js the same routes and
// fails when the mock leaves out a key the service sends or sends it as a
// different type.
//
// When a handler's output changes, this test fails until the golden file is
// written again:
//
//	GWATCH_UPDATE_CONTRACT=1 go test ./internal/api -run TestMockContractShapes
//
// and then `npm test` says whether web/mock.js needs the same change.

const contractFile = "../../tests/contract/api-shapes.json"

// contractRoutes are the GETs compared. {node} and {check} stand for the
// seeded node and its HTTP check. Routes whose answer depends on the machine
// (health, version, database, hosts, logs) or on the network are left out:
// their shape would differ between a developer's machine and CI.
var contractRoutes = []string{
	"/api/me",
	"/api/auth/setup",
	"/api/status",
	"/api/overview",
	"/api/wallboard",
	"/api/nodes",
	"/api/nodes/{node}",
	"/api/groups",
	"/api/checks/{check}/state",
	"/api/checks/{check}/results",
	"/api/history?checkId={check}&range=24h",
	"/api/events",
	"/api/maintenance",
	"/api/dashboards",
	"/api/map/layout",
	"/api/wallboards",
	"/api/settings",
	"/api/triggers",
	"/api/rules",
	"/api/automation/meta",
	"/api/endpoints",
	"/api/agents",
	"/api/agents/pairings",
	"/api/charts",
	"/api/incidents?state=all",
	"/api/users",
	"/api/apikeys",
	"/api/reports",
	"/api/mcp/status",
}

// shapeOf describes a decoded JSON value: "string", "number", "boolean" or
// "null" for a scalar, {"object": {key: shape}} for an object and
// {"array": shape} for an array, the element shape being every element's
// merged (null for an empty array: nothing is known about its elements).
func shapeOf(v any) any {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case float64, json.Number:
		return "number"
	case bool:
		return "boolean"
	case []any:
		var el any
		for _, e := range x {
			el = mergeShape(el, shapeOf(e))
		}
		return map[string]any{"array": el}
	case map[string]any:
		keys := map[string]any{}
		for k, e := range x {
			keys[k] = shapeOf(e)
		}
		return map[string]any{"object": keys}
	}
	return fmt.Sprintf("unknown %T", v)
}

// mergeShape combines two shapes of the same place: objects take the union
// of their keys, and "null" gives way to whatever the other one says.
func mergeShape(a, b any) any {
	if a == nil || a == "null" {
		return b
	}
	if b == nil || b == "null" {
		return a
	}
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)
	if !aok || !bok {
		return a
	}
	if ao, ok := am["object"].(map[string]any); ok {
		if bo, ok := bm["object"].(map[string]any); ok {
			out := map[string]any{}
			for k, v := range ao {
				out[k] = v
			}
			for k, v := range bo {
				out[k] = mergeShape(out[k], v)
			}
			return map[string]any{"object": out}
		}
	}
	if _, ok := am["array"]; ok {
		if _, ok := bm["array"]; ok {
			return map[string]any{"array": mergeShape(am["array"], bm["array"])}
		}
	}
	return a
}

func TestMockContractShapes(t *testing.T) {
	// The golden file is the default SQLite store's answers; the other
	// databases the CI runs this package against are not what the mock copies.
	if os.Getenv("GWATCH_TEST_DB") != "" {
		t.Skip("the contract is recorded against the default store")
	}
	ts, _ := newTestServer(t)

	// Seed one of most things, so the shapes have something in them.
	node := map[string]any{"name": "Router", "host": "192.168.1.1", "groups": []string{"Home Network"}, "tags": []string{"core"}, "enabled": true, "importance": "critical",
		"checks": []map[string]any{
			{"type": "ping", "name": "Ping", "enabled": true, "intervalSeconds": 60, "timeoutSeconds": 5},
			{"type": "http", "name": "Admin page", "enabled": true, "intervalSeconds": 60, "config": map[string]any{"target": "http://192.168.1.1/", "groups": []string{"Web"}}},
		}}
	var created nodeDoc
	if code := call(t, ts, "POST", "/api/nodes", node, &created); code != 201 {
		t.Fatalf("create node: %d", code)
	}
	if code := call(t, ts, "POST", fmt.Sprintf("/api/nodes/%d/run", created.ID), nil, nil); code != 200 {
		t.Fatalf("run node: %d", code)
	}
	if code := call(t, ts, "POST", "/api/dashboards", map[string]any{"name": "Home", "widgets": []map[string]any{{"type": "summary"}}}, nil); code != 200 {
		t.Fatalf("dashboard: %d", code)
	}
	mw := map[string]any{"name": "Reboot", "group": "Home Network", "enabled": true, "startAt": time.Now().Add(-time.Minute).Format(time.RFC3339), "endAt": time.Now().Add(time.Hour).Format(time.RFC3339)}
	if code := call(t, ts, "POST", "/api/maintenance", mw, nil); code != 200 {
		t.Fatalf("maintenance: %d", code)
	}
	seeds := []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/wallboards", map[string]any{"name": "Lobby"}},
		{"PUT", "/api/charts", []map[string]any{{"name": "Latency", "config": map[string]any{"metric": "avg", "range": "24h"}}}},
		{"POST", "/api/triggers", map[string]any{"nodeId": created.ID, "name": "Notify", "enabled": true, "on": []string{"down"}, "action": map[string]any{"type": "http", "url": "http://127.0.0.1:9/hook"}}},
		{"POST", "/api/endpoints", map[string]any{"name": "Router rebooted", "slug": "router-rebooted", "enabled": true, "method": "POST", "token": "s3cret-token-for-the-contract", "action": map[string]any{"type": "run_node", "nodeId": created.ID}}},
	}
	for _, s := range seeds {
		if code := call(t, ts, s.method, s.path, s.body, nil); code >= 300 {
			t.Fatalf("%s %s: %d", s.method, s.path, code)
		}
	}

	replacer := strings.NewReplacer("{node}", fmt.Sprint(created.ID), "{check}", fmt.Sprint(created.Checks[1].ID))
	got := map[string]any{}
	for _, route := range contractRoutes {
		resp, err := http.Get(ts.URL + replacer.Replace(route))
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("GET %s: %d %s", route, resp.StatusCode, data)
		}
		var v any
		if err := json.Unmarshal(data, &v); err != nil {
			t.Fatalf("GET %s: %v", route, err)
		}
		got["GET "+route] = shapeOf(v)
	}

	keys := make([]string, 0, len(got))
	for k := range got {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var buf bytes.Buffer
	buf.WriteString("{\n")
	for i, k := range keys {
		b, _ := json.Marshal(got[k]) // map keys come out sorted
		kb, _ := json.Marshal(k)
		fmt.Fprintf(&buf, "  %s: %s", kb, b)
		if i < len(keys)-1 {
			buf.WriteString(",")
		}
		buf.WriteString("\n")
	}
	buf.WriteString("}\n")

	if os.Getenv("GWATCH_UPDATE_CONTRACT") != "" {
		if err := os.MkdirAll(filepath.Dir(contractFile), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(contractFile, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(contractFile)
	if err != nil {
		t.Fatalf("%v — write it with GWATCH_UPDATE_CONTRACT=1 go test ./internal/api -run TestMockContractShapes", err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), buf.Bytes()) {
		var old map[string]json.RawMessage
		_ = json.Unmarshal(want, &old)
		var changed []string
		for _, k := range keys {
			b, _ := json.Marshal(got[k])
			if !bytes.Equal(old[k], b) {
				changed = append(changed, fmt.Sprintf("%s\n\twas %s\n\tnow %s", k, old[k], b))
			}
		}
		t.Fatalf("the API's answers no longer match %s:\n%s\nIf the change is meant, write the file again with\n\tGWATCH_UPDATE_CONTRACT=1 go test ./internal/api -run TestMockContractShapes\nthen run `npm test`: tests/web/mock-contract.test.mjs says what web/mock.js must change to match.", contractFile, strings.Join(changed, "\n"))
	}
}

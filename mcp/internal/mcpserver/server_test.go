package mcpserver_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jxburros/GWatch/mcp/internal/fakegwatch"
	"github.com/jxburros/GWatch/mcp/internal/gwatch"
	"github.com/jxburros/GWatch/mcp/internal/mcpserver"
	"github.com/jxburros/GWatch/mcp/internal/tools"
)

const (
	readKey  = "gw_readonly_key"
	writeKey = "gw_readwrite_key"
)

// session starts a fake GWatch and a real MCP session against it, connected to
// an in-process client over a pipe. It returns the client session and the fake,
// and cleans both up.
func session(t *testing.T, key string, allowWrite bool) (*mcp.ClientSession, *fakegwatch.Server) {
	t.Helper()
	fake := fakegwatch.New()
	fake.AddKey(readKey, "Assistant (read)", fakegwatch.ScopeRead)
	fake.AddKey(writeKey, "Assistant (read-write)", fakegwatch.ScopeReadWrite)
	t.Cleanup(fake.Close)

	client := gwatch.New(fake.URL, key, "gwatch-mcp/test", 5*time.Second)
	srv := mcpserver.New(tools.New(client, allowWrite), "gwatch", "test")

	serverTr, clientTr := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := srv.Connect(ctx, serverTr, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })

	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil).
		Connect(ctx, clientTr, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, fake
}

// toolNames lists the tools the server advertises, which is the client-visible
// half of the read/write split.
func toolNames(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	var names []string
	for _, tool := range res.Tools {
		if tool.InputSchema == nil {
			t.Errorf("tool %s has no input schema", tool.Name)
		}
		if strings.TrimSpace(tool.Description) == "" {
			t.Errorf("tool %s has no description", tool.Name)
		}
		names = append(names, tool.Name)
	}
	return names
}

// call runs a tool and returns its text content and whether it was an error.
func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("tools/call %s: protocol error: %v", name, err)
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String(), res.IsError
}

// jsonPart returns the compact JSON that follows the one-line summary.
func jsonPart(t *testing.T, text string) map[string]any {
	t.Helper()
	_, rest, found := strings.Cut(text, "\n")
	if !found {
		t.Fatalf("result has no JSON line:\n%s", text)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(rest), &doc); err != nil {
		t.Fatalf("result JSON is not an object: %v\n%s", err, rest)
	}
	return doc
}

func has(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// TestInitializeAndListReadOnly covers the handshake and the tool surface a
// read-only key gets: every read tool, and no write tool at all.
func TestInitializeAndListReadOnly(t *testing.T) {
	cs, _ := session(t, readKey, false)

	if got := cs.InitializeResult().ServerInfo.Name; got != "gwatch" {
		t.Errorf("server name = %q, want gwatch", got)
	}
	if !strings.Contains(cs.InitializeResult().Instructions, "read-only key") {
		t.Errorf("instructions do not mention the read-only boundary:\n%s", cs.InitializeResult().Instructions)
	}
	if err := cs.Ping(context.Background(), nil); err != nil {
		t.Errorf("ping: %v", err)
	}

	names := toolNames(t, cs)
	for _, want := range []string{
		"gwatch_overview", "gwatch_list_nodes", "gwatch_get_node", "gwatch_check_results",
		"gwatch_history", "gwatch_events", "gwatch_health", "gwatch_templates", "gwatch_groups",
	} {
		if !has(names, want) {
			t.Errorf("read tool %s missing from tools/list: %v", want, names)
		}
	}
	for _, unwanted := range []string{
		"gwatch_create_node", "gwatch_update_node", "gwatch_delete_node", "gwatch_set_node_enabled",
		"gwatch_run_node", "gwatch_silence_node", "gwatch_add_note", "gwatch_test_check",
	} {
		if has(names, unwanted) {
			t.Errorf("write tool %s advertised to a read-only session: %v", unwanted, names)
		}
	}
}

// TestListReadWrite checks the other half: --allow-write plus a readwrite key
// registers the write tools.
func TestListReadWrite(t *testing.T) {
	cs, _ := session(t, writeKey, true)
	names := toolNames(t, cs)
	for _, want := range []string{
		"gwatch_create_node", "gwatch_update_node", "gwatch_delete_node", "gwatch_set_node_enabled",
		"gwatch_run_node", "gwatch_silence_node", "gwatch_add_note", "gwatch_test_check",
	} {
		if !has(names, want) {
			t.Errorf("write tool %s missing from tools/list: %v", want, names)
		}
	}
	if len(names) != 17 {
		t.Errorf("expected 9 read + 8 write tools, got %d: %v", len(names), names)
	}
}

// TestReadTools exercises the read tools a client actually reaches for.
func TestReadTools(t *testing.T) {
	cs, fake := session(t, readKey, false)

	t.Run("overview", func(t *testing.T) {
		text, isErr := call(t, cs, "gwatch_overview", nil)
		if isErr {
			t.Fatalf("overview failed: %s", text)
		}
		if !strings.HasPrefix(text, "1 of 2 checks up; 1 down.") {
			t.Errorf("summary line does not lead with the tally: %q", firstLine(text))
		}
		doc := jsonPart(t, text)
		for _, k := range []string{"summary", "groups", "attention", "incidents"} {
			if _, ok := doc[k]; !ok {
				t.Errorf("overview JSON is missing %q", k)
			}
		}
	})

	t.Run("list_nodes", func(t *testing.T) {
		text, isErr := call(t, cs, "gwatch_list_nodes", nil)
		if isErr {
			t.Fatalf("list_nodes failed: %s", text)
		}
		doc := jsonPart(t, text)
		nodes, _ := doc["nodes"].([]any)
		if len(nodes) != 2 {
			t.Fatalf("want 2 nodes, got %d", len(nodes))
		}
		first, _ := nodes[0].(map[string]any)
		checks, _ := first["checks"].([]any)
		if len(checks) != 1 {
			t.Fatalf("want 1 check on the first node, got %d", len(checks))
		}
		chk, _ := checks[0].(map[string]any)
		for _, k := range []string{"id", "name", "type", "status", "lastMessage"} {
			if _, ok := chk[k]; !ok {
				t.Errorf("check summary is missing %q: %v", k, chk)
			}
		}
	})

	t.Run("list_nodes filters client-side", func(t *testing.T) {
		text, isErr := call(t, cs, "gwatch_list_nodes", map[string]any{"group": "public"})
		if isErr {
			t.Fatalf("filtered list failed: %s", text)
		}
		nodes, _ := jsonPart(t, text)["nodes"].([]any)
		if len(nodes) != 1 {
			t.Fatalf("group filter should leave 1 node, got %d", len(nodes))
		}
		// The router is in two groups; naming its second one finds it, which
		// is the whole point of matching any group rather than the first.
		text, _ = call(t, cs, "gwatch_list_nodes", map[string]any{"group": "critical"})
		nodes, _ = jsonPart(t, text)["nodes"].([]any)
		if len(nodes) != 1 {
			t.Fatalf("a filter on a node's second group should leave 1 node, got %d", len(nodes))
		}
		if first, _ := nodes[0].(map[string]any); first["name"] != "Router" {
			t.Fatalf("second-group filter found the wrong node: %v", first)
		}
		if first, _ := nodes[0].(map[string]any); len(first["groups"].([]any)) != 2 {
			t.Fatalf("the node summary should carry every group: %v", first)
		}
		text, _ = call(t, cs, "gwatch_list_nodes", map[string]any{"tag": "INFRA"})
		nodes, _ = jsonPart(t, text)["nodes"].([]any)
		if len(nodes) != 1 {
			t.Fatalf("tag filter should leave 1 node, got %d", len(nodes))
		}
		text, _ = call(t, cs, "gwatch_list_nodes", map[string]any{"status": "down"})
		nodes, _ = jsonPart(t, text)["nodes"].([]any)
		if len(nodes) != 1 {
			t.Fatalf("status filter should leave 1 node, got %d", len(nodes))
		}
	})

	t.Run("history is capped by striding", func(t *testing.T) {
		text, isErr := call(t, cs, "gwatch_history", map[string]any{"checkId": 11, "range": "24h"})
		if isErr {
			t.Fatalf("history failed: %s", text)
		}
		series, _ := jsonPart(t, text)["series"].([]any)
		if len(series) != 1 {
			t.Fatalf("want 1 series, got %d", len(series))
		}
		points, _ := series[0].(map[string]any)["points"].([]any)
		if len(points) != 200 {
			t.Errorf("500 raw points should be strided to 200, got %d", len(points))
		}
		if !strings.Contains(text, "dropped by even striding") {
			t.Errorf("the summary should say the series was downsampled: %q", firstLine(text))
		}
	})

	t.Run("history rejects a bad range", func(t *testing.T) {
		text, isErr := call(t, cs, "gwatch_history", map[string]any{"checkId": 11, "range": "6h"})
		if !isErr {
			t.Fatalf("an invalid range should be a tool error, got: %s", text)
		}
		if !strings.Contains(text, "1h, 24h, 7d, 30d, 1y") {
			t.Errorf("the error should list the accepted ranges: %s", text)
		}
	})

	t.Run("events", func(t *testing.T) {
		text, isErr := call(t, cs, "gwatch_events", map[string]any{"type": "down", "limit": 10})
		if isErr {
			t.Fatalf("events failed: %s", text)
		}
		events, _ := jsonPart(t, text)["events"].([]any)
		if len(events) != 1 {
			t.Fatalf("want 1 down event, got %d", len(events))
		}
	})

	t.Run("health and groups", func(t *testing.T) {
		if text, isErr := call(t, cs, "gwatch_health", nil); isErr {
			t.Fatalf("health failed: %s", text)
		}
		if text, isErr := call(t, cs, "gwatch_groups", nil); isErr {
			t.Fatalf("groups failed: %s", text)
		}
		if text, isErr := call(t, cs, "gwatch_templates", nil); isErr {
			t.Fatalf("templates failed: %s", text)
		}
	})

	t.Run("check_results", func(t *testing.T) {
		text, isErr := call(t, cs, "gwatch_check_results", map[string]any{"checkId": 11, "limit": 5})
		if isErr {
			t.Fatalf("check_results failed: %s", text)
		}
		if results, _ := jsonPart(t, text)["results"].([]any); len(results) != 5 {
			t.Errorf("want 5 results, got %d", len(results))
		}
	})

	// Everything above must have gone through the versioned prefix with the
	// key in X-API-Key and the server's own user agent.
	for _, r := range fake.Recorded() {
		if !strings.HasPrefix(r.Path, gwatch.APIPrefix+"/") {
			t.Errorf("request to %s did not use the %s prefix", r.Path, gwatch.APIPrefix)
		}
		if r.APIKey != readKey {
			t.Errorf("request to %s carried API key %q", r.Path, r.APIKey)
		}
		if !strings.HasPrefix(r.UserAgent, "gwatch-mcp/") {
			t.Errorf("request to %s had user agent %q", r.Path, r.UserAgent)
		}
	}
}

// TestWriteRejectedServerSideWithReadKey is the ROADMAP 3.2 "done when": even
// when the write tools are registered — here because the operator passed
// --allow-write with a key that turns out to be read-only — GWatch refuses
// every write itself, and the refusal reaches the client as a tool error
// carrying the server's own 403 message.
func TestWriteRejectedServerSideWithReadKey(t *testing.T) {
	cs, fake := session(t, readKey, true)

	writes := []struct {
		name string
		args map[string]any
	}{
		{"gwatch_create_node", map[string]any{"name": "New", "host": "10.0.0.1"}},
		{"gwatch_update_node", map[string]any{"id": 1, "name": "Renamed"}},
		{"gwatch_delete_node", map[string]any{"id": 1, "confirm": true}},
		{"gwatch_set_node_enabled", map[string]any{"id": 1, "enabled": false}},
		{"gwatch_run_node", map[string]any{"id": 1}},
		{"gwatch_silence_node", map[string]any{"id": 1, "minutes": 30}},
		{"gwatch_add_note", map[string]any{"text": "hello"}},
		{"gwatch_test_check", map[string]any{"check": map[string]any{"type": "ping", "name": "Ping"}}},
	}
	for _, w := range writes {
		t.Run(w.name, func(t *testing.T) {
			text, isErr := call(t, cs, w.name, w.args)
			if !isErr {
				t.Fatalf("%s succeeded with a read-only key: %s", w.name, text)
			}
			if !strings.Contains(text, "403") {
				t.Errorf("%s: the tool error should carry the HTTP status: %s", w.name, text)
			}
			if !strings.Contains(text, fakegwatch.ReadOnlyMessage) {
				t.Errorf("%s: the tool error should carry GWatch's own message: %s", w.name, text)
			}
		})
	}

	// gwatch_update_node reads before it writes, so it may have issued a GET;
	// what matters is that nothing changed. A read key must never have been
	// retried with anything else, and no request may have got a 2xx on a
	// write route.
	for _, r := range fake.Recorded() {
		if r.APIKey != readKey {
			t.Errorf("a request went out with a different credential (%q) after a 403", r.APIKey)
		}
		if r.Method != "GET" && r.Status < 400 {
			t.Errorf("write request %s %s was not refused (status %d)", r.Method, r.Path, r.Status)
		}
	}
	var nodes []any
	if err := gwatch.New(fake.URL, readKey, "gwatch-mcp/test", 5*time.Second).
		Get(context.Background(), "/nodes", nil, &nodes); err != nil {
		t.Fatalf("re-reading nodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Errorf("the node list changed despite every write being refused: %d nodes", len(nodes))
	}
}

// TestWriteToolsWithReadWriteKey shows the same tools working when the key is
// entitled, so the test above is about the scope and not about broken tools.
func TestWriteToolsWithReadWriteKey(t *testing.T) {
	cs, _ := session(t, writeKey, true)

	text, isErr := call(t, cs, "gwatch_create_node", map[string]any{
		"name": "NAS", "host": "192.168.1.50", "groups": []any{"Home Network", "Storage"},
		"tags": []any{"storage"}, "importance": "high",
		"checks": []any{map[string]any{
			"type": "tcp", "name": "SMB", "intervalSeconds": 120,
			"config": map[string]any{"port": 445},
		}},
	})
	if isErr {
		t.Fatalf("create_node failed: %s", text)
	}
	if !strings.HasPrefix(text, "Created node 3 \"NAS\"") {
		t.Errorf("unexpected summary: %q", firstLine(text))
	}
	created := jsonPart(t, text)
	if groups, _ := created["groups"].([]any); len(groups) != 2 || groups[0] != "Home Network" {
		t.Errorf("created node should carry both groups: %v", created["groups"])
	}
	if created["group"] != "Home Network" {
		t.Errorf("the deprecated group field should be the first group: %v", created["group"])
	}

	// A caller written against the old single-group tool still works: group
	// alone becomes the node's whole group list.
	if text, isErr := call(t, cs, "gwatch_update_node", map[string]any{"id": 3, "group": "Storage"}); isErr {
		t.Fatalf("update_node with the legacy group failed: %s", text)
	} else if groups, _ := jsonPart(t, text)["groups"].([]any); len(groups) != 1 || groups[0] != "Storage" {
		t.Errorf("a legacy group update should replace the list: %v", groups)
	}

	if text, isErr := call(t, cs, "gwatch_update_node", map[string]any{"id": 3, "groups": []any{"Storage", "Home Network"}}); isErr {
		t.Fatalf("update_node failed: %s", text)
	}
	if text, isErr := call(t, cs, "gwatch_set_node_enabled", map[string]any{"id": 3, "enabled": false}); isErr {
		t.Fatalf("set_node_enabled failed: %s", text)
	}
	if text, isErr := call(t, cs, "gwatch_silence_node", map[string]any{"id": 3, "minutes": 30}); isErr {
		t.Fatalf("silence_node failed: %s", text)
	}
	if text, isErr := call(t, cs, "gwatch_run_node", map[string]any{"id": 3}); isErr {
		t.Fatalf("run_node failed: %s", text)
	}
	if text, isErr := call(t, cs, "gwatch_add_note", map[string]any{"text": "moved the NAS", "nodeId": 3}); isErr {
		t.Fatalf("add_note failed: %s", text)
	}
	if text, isErr := call(t, cs, "gwatch_test_check", map[string]any{
		"check": map[string]any{"type": "ping", "name": "Ping"}, "nodeHost": "192.168.1.50"}); isErr {
		t.Fatalf("test_check failed: %s", text)
	}
	if text, isErr := call(t, cs, "gwatch_delete_node", map[string]any{"id": 3, "confirm": true}); isErr {
		t.Fatalf("delete_node failed: %s", text)
	}
}

// TestDeleteNeedsConfirm keeps the destructive tool behind an explicit
// acknowledgement, before any request is made.
func TestDeleteNeedsConfirm(t *testing.T) {
	cs, fake := session(t, writeKey, true)
	before := len(fake.Recorded())

	text, isErr := call(t, cs, "gwatch_delete_node", map[string]any{"id": 1})
	if !isErr {
		t.Fatalf("delete without confirm should be a tool error, got: %s", text)
	}
	if !strings.Contains(text, "confirm: true") {
		t.Errorf("the error should say how to confirm: %s", text)
	}
	if n := len(fake.Recorded()) - before; n != 0 {
		t.Errorf("delete without confirm still made %d request(s) to GWatch", n)
	}
	text, isErr = call(t, cs, "gwatch_delete_node", map[string]any{"id": 1, "confirm": false})
	if !isErr {
		t.Fatalf("confirm:false should be refused too, got: %s", text)
	}
}

// TestUnknownTool is a protocol-level error, not a tool error: there is no
// tool whose result could carry it.
func TestUnknownTool(t *testing.T) {
	cs, _ := session(t, readKey, false)
	_, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "gwatch_drop_database"})
	if err == nil {
		t.Fatal("calling an unregistered tool should be a JSON-RPC error")
	}
	if !strings.Contains(err.Error(), "gwatch_drop_database") {
		t.Errorf("the error should name the tool: %v", err)
	}
}

// TestBadKeyIs401 checks the message a misconfigured key produces.
func TestBadKeyIs401(t *testing.T) {
	cs, _ := session(t, "gw_not_a_key", false)
	text, isErr := call(t, cs, "gwatch_overview", nil)
	if !isErr {
		t.Fatalf("an unknown key should fail: %s", text)
	}
	if !strings.Contains(text, "401") || !strings.Contains(text, "rejected the API key") {
		t.Errorf("unhelpful 401 message: %s", text)
	}
}

// TestDeniedRouteStaysDenied guards the decision not to ship tools for
// settings, backups, triggers, endpoints, users or keys: even a readwrite key
// is refused there, so a tool for them could only ever return 403.
func TestDeniedRouteStaysDenied(t *testing.T) {
	fake := fakegwatch.New()
	fake.AddKey(writeKey, "Assistant", fakegwatch.ScopeReadWrite)
	t.Cleanup(fake.Close)
	client := gwatch.New(fake.URL, writeKey, "gwatch-mcp/test", 5*time.Second)

	for _, path := range []string{"/settings", "/backups", "/triggers", "/endpoints", "/users", "/apikeys", "/logs"} {
		err := client.Get(context.Background(), path, nil, nil)
		var apiErr *gwatch.Error
		if err == nil {
			t.Errorf("%s was allowed to a readwrite key", path)
			continue
		}
		if !asAPIError(err, &apiErr) || !apiErr.Forbidden() {
			t.Errorf("%s: want 403, got %v", path, err)
		}
	}
}

func asAPIError(err error, target **gwatch.Error) bool {
	e, ok := err.(*gwatch.Error)
	if ok {
		*target = e
	}
	return ok
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// Package e2e holds a manual end-to-end check against a real GWatch.
//
// It is skipped unless both GWATCH_E2E_URL and GWATCH_E2E_KEY are set, so it
// runs in CI against a disposable real-server fixture. Use it manually when
// setting an install up, or after changing the client:
//
//	GWATCH_E2E_URL=http://127.0.0.1:7230 GWATCH_E2E_KEY=gw_… go test ./internal/e2e -v
//
// A read-only key is enough; the test only reads. Set GWATCH_E2E_WRITE=1 as
// well to also assert that the key's scope is readwrite (it still writes
// nothing — creating nodes on someone's real monitor is not a test's job).
package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jxburros/GWatch/mcp/internal/gwatch"
	"github.com/jxburros/GWatch/mcp/internal/mcpserver"
	"github.com/jxburros/GWatch/mcp/internal/tools"
)

func TestAgainstRealGWatch(t *testing.T) {
	url, key := os.Getenv("GWATCH_E2E_URL"), os.Getenv("GWATCH_E2E_KEY")
	if url == "" || key == "" {
		t.Skip("set GWATCH_E2E_URL and GWATCH_E2E_KEY to run this against a real GWatch")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := gwatch.New(url, key, "gwatch-mcp/e2e", 10*time.Second)
	me, err := client.Me(ctx)
	if err != nil {
		t.Fatalf("GET %s/me: %v", gwatch.APIPrefix, err)
	}
	t.Logf("principal: %s", me.Describe())
	if os.Getenv("GWATCH_E2E_WRITE") != "" && me.Scope != "readwrite" {
		t.Fatalf("GWATCH_E2E_WRITE is set but the key's scope is %q", me.Scope)
	}

	// Read-only session: whatever the key can do, no write tool is registered.
	srv := mcpserver.New(tools.New(client, false), "gwatch", "e2e")
	serverTr, clientTr := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, serverTr, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "test"}, nil).Connect(ctx, clientTr, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer cs.Close()

	list, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	for _, tool := range list.Tools {
		if strings.HasPrefix(tool.Name, "gwatch_create") || strings.HasPrefix(tool.Name, "gwatch_delete") {
			t.Errorf("a write tool was registered without --allow-write: %s", tool.Name)
		}
	}
	t.Logf("%d tools registered", len(list.Tools))

	for _, name := range []string{"gwatch_overview", "gwatch_list_nodes", "gwatch_health", "gwatch_groups", "gwatch_events"} {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		text := ""
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				text += tc.Text
			}
		}
		if res.IsError {
			t.Errorf("%s returned a tool error: %s", name, text)
			continue
		}
		_, rest, found := strings.Cut(text, "<gwatch-monitoring-data>\n")
		body, _, closed := strings.Cut(rest, "\n</gwatch-monitoring-data>")
		if !found || !closed || !json.Valid([]byte(body)) {
			t.Errorf("%s: missing valid untrusted-data envelope", name)
		}

	}
}

func TestRealServerAPIKeyBoundaries(t *testing.T) {
	base, readKey, writeKey := os.Getenv("GWATCH_E2E_URL"), os.Getenv("GWATCH_E2E_READ_KEY"), os.Getenv("GWATCH_E2E_WRITE_KEY")
	if base == "" || readKey == "" || writeKey == "" {
		t.Skip("requires disposable real-server keys")
	}
	ctx := context.Background()
	read := gwatch.New(base, readKey, "gwatch-mcp/e2e", 10*time.Second)
	write := gwatch.New(base, writeKey, "gwatch-mcp/e2e", 10*time.Second)
	check := map[string]any{"type": "custom", "name": "Forbidden", "config": map[string]any{"command": "echo rejected"}}
	for _, tc := range []struct {
		name   string
		client *gwatch.Client
		path   string
		body   any
	}{
		{"read key cannot write", read, "/nodes", map[string]any{"name": "Rejected", "host": "localhost", "checks": []any{check}}},
		{"write key cannot create custom", write, "/nodes", map[string]any{"name": "Rejected", "host": "localhost", "checks": []any{check}}},
		{"write key cannot test custom", write, "/checks/test", map[string]any{"nodeHost": "localhost", "check": check}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out any
			err := tc.client.Post(ctx, tc.path, tc.body, &out)
			var apiErr *gwatch.Error
			if !errors.As(err, &apiErr) || apiErr.Status != 403 {
				t.Fatalf("want HTTP 403, got %v", err)
			}
		})
	}
}

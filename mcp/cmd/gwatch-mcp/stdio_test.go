package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jxburros/GWatch/mcp/internal/fakegwatch"
)

// TestStdioTransport runs the real binary the way an MCP client does — as a
// subprocess speaking newline-delimited JSON-RPC on stdin/stdout — against a
// fake GWatch. The in-process tests in internal/mcpserver cover the tools;
// this one covers main(): flag and environment handling, the /api/v1/me probe
// that decides the scope, and the stdio framing itself.
func TestStdioTransport(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary")
	}
	bin := build(t)

	fake := fakegwatch.New()
	defer fake.Close()
	fake.AddKey("gw_read", "Assistant", fakegwatch.ScopeRead)
	fake.AddKey("gw_write", "Assistant RW", fakegwatch.ScopeReadWrite)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, tc := range []struct {
		name      string
		env       []string
		args      []string
		wantWrite bool
	}{
		{
			name: "read key, environment configuration",
			env:  []string{"GWATCH_URL=" + fake.URL, "GWATCH_API_KEY=gw_read"},
		},
		{
			name: "readwrite key without the flag",
			env:  []string{"GWATCH_URL=" + fake.URL, "GWATCH_API_KEY=gw_write"},
		},
		{
			name:      "readwrite key with GWATCH_MCP_ALLOW_WRITE",
			env:       []string{"GWATCH_URL=" + fake.URL, "GWATCH_API_KEY=gw_write", "GWATCH_MCP_ALLOW_WRITE=1"},
			wantWrite: true,
		},
		{
			name:      "readwrite key with --allow-write",
			args:      []string{"--url", fake.URL, "--api-key", "gw_write", "--allow-write"},
			wantWrite: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bin, tc.args...)
			cmd.Env = append([]string{"PATH=" + pathEnv()}, tc.env...)
			cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil).
				Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
			if err != nil {
				t.Fatalf("connecting over stdio: %v", err)
			}
			defer cs.Close()

			if err := cs.Ping(ctx, nil); err != nil {
				t.Fatalf("ping: %v", err)
			}
			list, err := cs.ListTools(ctx, nil)
			if err != nil {
				t.Fatalf("tools/list: %v", err)
			}
			hasWrite := false
			for _, tool := range list.Tools {
				if tool.Name == "gwatch_create_node" {
					hasWrite = true
				}
			}
			if hasWrite != tc.wantWrite {
				var names []string
				for _, tool := range list.Tools {
					names = append(names, tool.Name)
				}
				t.Fatalf("write tools present = %v, want %v: %v", hasWrite, tc.wantWrite, names)
			}

			res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "gwatch_overview"})
			if err != nil {
				t.Fatalf("tools/call: %v", err)
			}
			if res.IsError {
				t.Fatalf("overview returned a tool error: %v", res.Content)
			}
			text, _ := res.Content[0].(*mcp.TextContent)
			if text == nil || !strings.Contains(text.Text, "checks up") {
				t.Errorf("unexpected overview result: %+v", res.Content)
			}
		})
	}
}

// build compiles the binary once for this test.
func build(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "gwatch-mcp")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", out, ".")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, b)
	}
	return out
}

// pathEnv keeps the child's environment down to PATH plus whatever the case
// sets, so a stray GWATCH_… variable in the test runner's environment cannot
// change what is being asserted.
func pathEnv() string { return os.Getenv("PATH") }

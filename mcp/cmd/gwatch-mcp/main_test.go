package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/jxburros/GWatch/mcp/internal/fakegwatch"
	"github.com/jxburros/GWatch/mcp/internal/gwatch"
)

// me builds the principal GET /api/v1/me would report for a key of that scope.
func me(scope string) gwatch.Me {
	return gwatch.Me{Kind: "apikey", Name: "Assistant", Scope: scope, CanWrite: scope == "readwrite", SignedIn: true}
}

// TestWriteAllowed pins the two-condition gate: the operator has to ask for
// write access on the command line *and* GWatch has to say the key is entitled
// to it. Either one alone leaves the write tools unregistered, with a reason a
// person can act on.
func TestWriteAllowed(t *testing.T) {
	cases := []struct {
		name  string
		flag  bool
		scope string
		want  bool
		says  string
	}{
		{"default", false, "read", false, "--allow-write was not given"},
		{"readwrite key without the flag", false, "readwrite", false, "--allow-write was not given"},
		{"flag with a read key", true, "read", false, "scope as \"read\""},
		{"flag and a readwrite key", true, "readwrite", true, ""},
		{"flag with no scope at all", true, "", false, "does not report a readwrite scope"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, why := writeAllowed(tc.flag, me(tc.scope))
			if got != tc.want {
				t.Fatalf("writeAllowed(%v, %q) = %v, want %v (%s)", tc.flag, tc.scope, got, tc.want, why)
			}
			if tc.says != "" && !strings.Contains(why, tc.says) {
				t.Errorf("reason %q does not mention %q", why, tc.says)
			}
			if tc.want && why != "" {
				t.Errorf("an allowed case should have no reason, got %q", why)
			}
		})
	}
}

// TestRunRefusesWithoutKey: no key, no server — and the message says where to
// get one, which is the only part of setup that cannot be automated.
func TestRunRefusesWithoutKey(t *testing.T) {
	t.Setenv("GWATCH_API_KEY", "")
	var out, errOut bytes.Buffer
	err := run([]string{"--url", "http://127.0.0.1:1"}, &out, &errOut)
	if err == nil {
		t.Fatal("starting without an API key should fail")
	}
	if !strings.Contains(err.Error(), "Settings › Users & access") {
		t.Errorf("the error should say how to create a key:\n%s", err)
	}
}

// TestVersionCommand does not need a key.
func TestVersionCommand(t *testing.T) {
	t.Setenv("GWATCH_API_KEY", "")
	var out, errOut bytes.Buffer
	if err := run([]string{"version"}, &out, &errOut); err != nil {
		t.Fatalf("version: %v", err)
	}
	if !strings.HasPrefix(out.String(), "gwatch-mcp ") {
		t.Errorf("unexpected version output: %q", out.String())
	}
}

// TestCheckCommand is the setup aid: it has to name the principal, the scope
// and whether write tools are on.
func TestCheckCommand(t *testing.T) {
	fake := fakegwatch.New()
	defer fake.Close()
	fake.AddKey("gw_read", "Assistant", fakegwatch.ScopeRead)
	fake.AddKey("gw_write", "Assistant RW", fakegwatch.ScopeReadWrite)

	t.Run("read key", func(t *testing.T) {
		var out, errOut bytes.Buffer
		if err := run([]string{"check", "--url", fake.URL, "--api-key", "gw_read"}, &out, &errOut); err != nil {
			t.Fatalf("check: %v (%s)", err, errOut.String())
		}
		s := out.String()
		for _, want := range []string{"Connection:  ok", `API key "Assistant"`, "Scope:       read", "Write tools: disabled", "9 read, 0 write"} {
			if !strings.Contains(s, want) {
				t.Errorf("check output is missing %q:\n%s", want, s)
			}
		}
	})

	t.Run("readwrite key with --allow-write", func(t *testing.T) {
		var out, errOut bytes.Buffer
		if err := run([]string{"check", "--url", fake.URL, "--api-key", "gw_write", "--allow-write"}, &out, &errOut); err != nil {
			t.Fatalf("check: %v (%s)", err, errOut.String())
		}
		s := out.String()
		for _, want := range []string{"Scope:       readwrite", "Write tools: ENABLED", "9 read, 8 write"} {
			if !strings.Contains(s, want) {
				t.Errorf("check output is missing %q:\n%s", want, s)
			}
		}
	})

	t.Run("unknown key", func(t *testing.T) {
		var out, errOut bytes.Buffer
		err := run([]string{"check", "--url", fake.URL, "--api-key", "gw_nope"}, &out, &errOut)
		if err == nil {
			t.Fatal("an unknown key should fail the check")
		}
		if !strings.Contains(err.Error(), "401") {
			t.Errorf("want a 401 in the error, got %v", err)
		}
	})
}

// TestEnvDefaults covers the environment forms an MCP client config uses.
func TestEnvDefaults(t *testing.T) {
	t.Setenv("GWATCH_URL", "")
	if got := envOr("GWATCH_URL", defaultURL); got != defaultURL {
		t.Errorf("empty env should fall back to %s, got %s", defaultURL, got)
	}
	t.Setenv("GWATCH_MCP_ALLOW_WRITE", "1")
	if !envBool("GWATCH_MCP_ALLOW_WRITE") {
		t.Error("GWATCH_MCP_ALLOW_WRITE=1 should enable write")
	}
	t.Setenv("GWATCH_MCP_ALLOW_WRITE", "no")
	if envBool("GWATCH_MCP_ALLOW_WRITE") {
		t.Error("GWATCH_MCP_ALLOW_WRITE=no should not enable write")
	}
	t.Setenv("GWATCH_MCP_TIMEOUT", "45s")
	if got := envDuration("GWATCH_MCP_TIMEOUT", time.Second); got != 45*time.Second {
		t.Errorf("timeout = %v, want 45s", got)
	}
	t.Setenv("GWATCH_MCP_TIMEOUT", "nonsense")
	if got := envDuration("GWATCH_MCP_TIMEOUT", time.Second); got != time.Second {
		t.Errorf("an unparseable timeout should fall back to the default, got %v", got)
	}
}

package checks

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// ---- helper-process pattern (à la os/exec's own tests) --------------------
//
// The "command" under test is this very test binary, re-invoked with
// GO_WANT_HELPER_PROCESS=1 so TestHelperProcess drives a small scripted
// behaviour and exits, instead of running the real test suite. This keeps
// the custom-check tests deterministic and shell-free on every platform
// custom.go itself supports, including Windows CI.

func helperCommand(t *testing.T, behavior string, extra ...string) string {
	t.Helper()
	exe := os.Args[0]
	parts := []string{quoteArg(exe), "-test.run=TestHelperProcess", "--", behavior}
	parts = append(parts, extra...)
	return strings.Join(parts, " ")
}

func quoteArg(s string) string {
	if strings.ContainsAny(s, " \t") {
		return `"` + s + `"`
	}
	return s
}

func customCheck(command string, extra model.CheckConfig) model.Check {
	extra.Command = command
	if extra.Target == "" {
		extra.Target = "example.test"
	}
	if extra.Env == nil {
		extra.Env = map[string]string{}
	}
	extra.Env["GO_WANT_HELPER_PROCESS"] = "1"
	return model.Check{ID: 1, Type: model.CheckCustom, Name: "custom", Enabled: true, TimeoutSeconds: 5, Config: extra}
}

// TestHelperProcess is not a real test: it is a scriptable subprocess body,
// gated behind an environment variable so `go test` never runs its
// behaviours directly.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	args := os.Args
	idx := -1
	for i, a := range args {
		if a == "--" {
			idx = i
			break
		}
	}
	if idx == -1 || idx+1 >= len(args) {
		os.Exit(3)
	}
	behavior := args[idx+1]
	rest := args[idx+2:]
	switch behavior {
	case "exit0":
		fmt.Println("hello stdout")
		os.Exit(0)
	case "exit1":
		fmt.Println("boom")
		os.Exit(1)
	case "exit2":
		fmt.Println("status=degraded")
		fmt.Println("message=cpu high")
		os.Exit(2)
	case "status-override":
		fmt.Println("status=up")
		fmt.Println("this line is not a control line")
		os.Exit(1)
	case "message-latency":
		fmt.Println("message=all good")
		fmt.Println("latency_ms=42.5")
		os.Exit(0)
	case "error-line":
		fmt.Println("error=disk full")
		os.Exit(1)
	case "sleep":
		time.Sleep(5 * time.Second)
		os.Exit(0)
	case "echo-args":
		fmt.Println(strings.Join(rest, "|"))
		os.Exit(0)
	default:
		os.Exit(3)
	}
}

// ---- actual tests -----------------------------------------------------

func TestCustomCheckExitCodes(t *testing.T) {
	// Exit 0: up, no explicit status/message lines.
	res := run(t, customCheck(helperCommand(t, "exit0"), model.CheckConfig{}), Options{})
	if !res.Success || res.Status != model.StatusUp {
		t.Fatalf("exit0: expected up, got %+v", res)
	}
	if res.Details.Output != "hello stdout" {
		t.Errorf("exit0: output = %q", res.Details.Output)
	}
	if res.LatencyMS == nil {
		t.Errorf("exit0: expected an elapsed-time latency")
	}

	// Exit 2: degraded (warning), with a status/message contract.
	res = run(t, customCheck(helperCommand(t, "exit2"), model.CheckConfig{}), Options{})
	if !res.Success || res.Status != model.StatusDegraded {
		t.Fatalf("exit2: expected degraded, got %+v", res)
	}
	if res.Message != "cpu high" {
		t.Errorf("exit2: message = %q", res.Message)
	}
	if len(res.Warnings) != 1 || res.Warnings[0] != "cpu high" {
		t.Errorf("exit2: warnings = %v", res.Warnings)
	}

	// Any other exit code: down.
	res = run(t, customCheck(helperCommand(t, "exit1"), model.CheckConfig{}), Options{})
	if res.Success || res.Status != model.StatusDown {
		t.Fatalf("exit1: expected down, got %+v", res)
	}
	if res.Message != "exited with status 1" {
		t.Errorf("exit1: message = %q", res.Message)
	}
	if res.Details.Output != "boom" {
		t.Errorf("exit1: output = %q", res.Details.Output)
	}
}

func TestCustomCheckStatusOverride(t *testing.T) {
	// status=up in the output overrides a non-zero exit code.
	res := run(t, customCheck(helperCommand(t, "status-override"), model.CheckConfig{}), Options{})
	if !res.Success || res.Status != model.StatusUp {
		t.Fatalf("expected status= to override exit code: %+v", res)
	}
	if res.Details.Output != "this line is not a control line" {
		t.Errorf("output should keep the non-control line: %q", res.Details.Output)
	}
}

func TestCustomCheckMessageAndLatency(t *testing.T) {
	res := run(t, customCheck(helperCommand(t, "message-latency"), model.CheckConfig{}), Options{})
	if !res.Success || res.Message != "all good" {
		t.Fatalf("expected message parsed: %+v", res)
	}
	if res.LatencyMS == nil || *res.LatencyMS != 42.5 {
		t.Errorf("latency_ms not parsed: %+v", res.LatencyMS)
	}
}

func TestCustomCheckErrorLine(t *testing.T) {
	res := run(t, customCheck(helperCommand(t, "error-line"), model.CheckConfig{}), Options{})
	if res.Success || res.Status != model.StatusDown {
		t.Fatalf("expected down: %+v", res)
	}
	if res.Message != "disk full" || res.Error != "disk full" {
		t.Errorf("expected error= to populate message/error: %+v", res)
	}
}

func TestCustomCheckTimeout(t *testing.T) {
	c := customCheck(helperCommand(t, "sleep"), model.CheckConfig{})
	c.TimeoutSeconds = 1
	res := run(t, c, Options{})
	if res.Success || res.Message != "timed out" || res.Error != "timed out" {
		t.Fatalf("expected timeout: %+v", res)
	}
}

func TestCustomCheckMissingCommand(t *testing.T) {
	res := run(t, customCheck("", model.CheckConfig{}), Options{})
	if res.Success || !strings.Contains(res.Error, "command") {
		t.Fatalf("expected missing-command error: %+v", res)
	}
	if err := Validate(customCheck("", model.CheckConfig{Target: "x"}), ""); err == nil || !strings.Contains(err.Error(), "command") {
		t.Errorf("Validate should reject an empty command: %v", err)
	}
}

// TestCustomCheckTargetSubstitution proves a {{target}} containing spaces
// stays a single argument: the placeholder is replaced after the command
// line has already been split into arguments, never before.
func TestCustomCheckTargetSubstitution(t *testing.T) {
	c := customCheck(helperCommand(t, "echo-args", "{{target}}"), model.CheckConfig{Target: "my host name"})
	res := run(t, c, Options{})
	if !res.Success {
		t.Fatalf("expected success: %+v", res)
	}
	if res.Details.Output != "my host name" {
		t.Fatalf("target should arrive as one argument, got %q", res.Details.Output)
	}
}

func TestCustomCheckValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     model.CheckConfig
		wantErr string
	}{
		{"missing command", model.CheckConfig{Target: "x"}, "command"},
		{"blank workdir", model.CheckConfig{Target: "x", Command: "echo hi", WorkDir: "   "}, "working directory"},
		{"bad env key", model.CheckConfig{Target: "x", Command: "echo hi", Env: map[string]string{"1BAD": "v"}}, "environment variable"},
		{"ok", model.CheckConfig{Target: "x", Command: "echo hi", WorkDir: "/tmp", Env: map[string]string{"FOO": "bar"}}, ""},
	}
	for _, tc := range cases {
		err := Validate(model.Check{Type: model.CheckCustom, Config: tc.cfg}, "")
		if tc.wantErr == "" {
			if err != nil {
				t.Errorf("%s: unexpected error %v", tc.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: err = %v, want containing %q", tc.name, err, tc.wantErr)
		}
	}
}

func TestSplitCommandLine(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"echo hi", []string{"echo", "hi"}},
		{`echo "hi there"`, []string{"echo", "hi there"}},
		{"echo 'hi there' again", []string{"echo", "hi there", "again"}},
		{"  a   b  ", []string{"a", "b"}},
	}
	for _, tc := range cases {
		got := splitCommandLine(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("splitCommandLine(%q) = %v, want %v", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("splitCommandLine(%q) = %v, want %v", tc.in, got, tc.want)
				break
			}
		}
	}
}

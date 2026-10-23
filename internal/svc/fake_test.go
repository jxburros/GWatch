package svc

import (
	"errors"
	"strings"
	"testing"
)

// fakeRunner records every command and answers from a table keyed by the
// command line. A command not in the table succeeds with no output, which is
// what systemctl, rc-update and launchctl mostly do.
type fakeRunner struct {
	calls   []string
	answers map[string]fakeAnswer
}

type fakeAnswer struct {
	out  string
	code int
}

func (f *fakeRunner) run(name string, args ...string) (string, int, error) {
	line := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, line)
	a, ok := f.answers[line]
	if !ok || a.code == 0 {
		return a.out, 0, nil
	}
	return a.out, a.code, errors.New(line + ": exit status")
}

func (f *fakeRunner) expect(t *testing.T, want ...string) {
	t.Helper()
	if strings.Join(f.calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("commands run:\n  %s\nwant:\n  %s", strings.Join(f.calls, "\n  "), strings.Join(want, "\n  "))
	}
	f.calls = nil
}

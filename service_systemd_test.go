package main

import (
	"errors"
	"flag"
	"fmt"
	"testing"
)

// Settings that are wrong stop a systemd service for good (78); anything else
// lets Restart=on-failure try again.
func TestFatalExitCode(t *testing.T) {
	if got := fatalExitCode(configError{errors.New("database settings: bad")}); got != configExitCode {
		t.Errorf("config error: %d, want %d", got, configExitCode)
	}
	if got := fatalExitCode(fmt.Errorf("start: %w", configError{errors.New("x")})); got != configExitCode {
		t.Errorf("wrapped config error: %d, want %d", got, configExitCode)
	}
	if got := fatalExitCode(errors.New("open database: connection refused")); got != 1 {
		t.Errorf("other error: %d, want 1", got)
	}
}

func TestUsageExitCode(t *testing.T) {
	t.Setenv("INVOCATION_ID", "")
	if got := usageExitCode("run"); got != 2 {
		t.Errorf("run from a terminal: %d, want 2", got)
	}
	t.Setenv("INVOCATION_ID", "abc")
	if got := usageExitCode("run"); got != configExitCode {
		t.Errorf("run under systemd: %d, want %d", got, configExitCode)
	}
	if got := usageExitCode("install"); got != 2 {
		t.Errorf("install: %d, want 2", got)
	}
}

// A flag left at its default must not count as given, or re-running install
// would reset the listen address an earlier install chose.
func TestGivenFlags(t *testing.T) {
	t.Setenv("GWATCH_DATA_DIR", "")
	t.Setenv("GWATCH_LISTEN", "")
	t.Setenv("GWATCH_TRUSTED_PROXIES", "")
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.String("listen", "127.0.0.1:7230", "")
	fs.String("data-dir", "/x", "")
	if err := fs.Parse([]string{"--listen", "0.0.0.0:7230"}); err != nil {
		t.Fatal(err)
	}
	given := givenFlags(fs)
	if !given["listen"] || given["data-dir"] {
		t.Errorf("given = %v, want listen only", given)
	}
	t.Setenv("GWATCH_DATA_DIR", "/srv/gwatch")
	if !givenFlags(fs)["data-dir"] {
		t.Error("GWATCH_DATA_DIR in the environment should count as given")
	}
}

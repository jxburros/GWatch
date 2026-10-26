//go:build linux

package sysmetrics

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jxburros/GWatch/internal/model"
)

// processNames walks a captured /proc: numbered directories only, kernel
// threads left out, long names recovered from the command line.
func TestProcessNamesFromProc(t *testing.T) {
	dir := t.TempDir()
	write := func(pid, comm, cmdline string) {
		p := filepath.Join(dir, pid)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(p, "comm"), []byte(comm+"\n"), 0o644)
		os.WriteFile(filepath.Join(p, "cmdline"), []byte(cmdline), 0o644)
	}
	write("1", "systemd", "/sbin/init\x00splash\x00")
	write("2", "kthreadd", "")
	write("812", "nginx", "nginx: master process\x00")
	write("813", "nginx", "nginx: worker process\x00")
	write("990", "Plex Media Serv", "/usr/lib/plexmediaserver/Plex Media Server\x00")
	os.MkdirAll(filepath.Join(dir, "self"), 0o755)
	old := procRoot
	procRoot = dir
	defer func() { procRoot = old }()

	names, err := processNames(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := countProcesses(names, []string{"nginx", "Plex Media Server", "kthreadd", "absent"}, false)
	want := []model.HostProcess{{Name: "nginx", Count: 2}, {Name: "Plex Media Server", Count: 1}, {Name: "kthreadd", Count: 0}, {Name: "absent", Count: 0}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

// The watched units go to systemctl in one call, with "--" before them, and
// the answer comes back under the names that were asked for.
func TestServiceStatesAsksSystemctlOnce(t *testing.T) {
	var calls [][]string
	old := runSystemctl
	runSystemctl = func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, args)
		return []byte(systemctlShowFixture), nil
	}
	defer func() { runSystemctl = old }()

	got, err := serviceStates(context.Background(), []string{"nginx", "smbd.service", "nosuch", "sshd"})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || !strings.Contains(strings.Join(calls[0], " "), "-- nginx.service smbd.service nosuch.service sshd.service") {
		t.Errorf("calls: %q", calls)
	}
	if len(got) != 4 || !got[0].Running || got[2].State != "not found" {
		t.Errorf("got %+v", got)
	}

	// A machine without systemd says so, and the reading carries the warning
	// instead of four stopped services.
	runSystemctl = func(context.Context, ...string) ([]byte, error) { return nil, errNoSystemctl }
	var m model.HostMetrics
	warnings := NewCollector("").readWatched(context.Background(), &m, watchLists{services: []string{"nginx"}})
	svcs := m.Services
	if svcs != nil || len(warnings) != 1 || !strings.Contains(warnings[0], "systemctl is not installed") {
		t.Errorf("no systemd: %+v %q", svcs, warnings)
	}
}

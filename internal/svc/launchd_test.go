package svc

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// What `launchctl print system/<label>` says, cut down to the lines that
// matter, for a running daemon and for one that is loaded but not running.
const (
	printRunning = "system/GWatchAgent = {\n\tactive count = 1\n\tpath = /Library/LaunchDaemons/GWatchAgent.plist\n\tstate = running\n\n\tprogram = /usr/local/libexec/gwatch-agent/gwatch-agent\n\tpid = 412\n}\n"
	printWaiting = "system/GWatchAgent = {\n\tactive count = 0\n\tpath = /Library/LaunchDaemons/GWatchAgent.plist\n\tstate = not running\n\n\tlast exit code = 3\n}\n"
)

func TestLaunchdRunning(t *testing.T) {
	if !launchdRunning(printRunning) || launchdRunning(printWaiting) || launchdRunning("") {
		t.Error("launchdRunning misread launchctl print")
	}
	if !launchdRunning("{\n\tpid = 12\n}") {
		t.Error("a pid line without a state line is a running job")
	}
}

func TestLaunchd(t *testing.T) {
	root := fsRoot(t.TempDir())
	const print = "launchctl print system/GWatchAgent"
	r := &fakeRunner{answers: map[string]fakeAnswer{print: {"", 113}}}
	m := launchdManager{root: root, r: r}
	plist := root.path("/Library/LaunchDaemons/GWatchAgent.plist")

	if _, err := m.status(agentConfig); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("status before install: %v", err)
	}
	if err := m.start(agentConfig); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("start before install: %v", err)
	}
	if err := m.install(agentConfig, agentExe); err != nil {
		t.Fatal(err)
	}
	r.expect(t) // installing does not load it
	if fi, _ := os.Stat(plist); fi.Mode().Perm() != 0644 {
		t.Errorf("plist mode %v", fi.Mode().Perm())
	}
	if s := readRoot(t, root, "/Library/LaunchDaemons/GWatchAgent.plist"); !strings.Contains(s, "<string>"+agentExe+"</string>") {
		t.Errorf("plist:\n%s", s)
	}
	if err := m.install(agentConfig, agentExe); err == nil {
		t.Error("a second install was not refused")
	}

	// Not loaded: stopped; start bootstraps it; stop has nothing to do.
	if st, err := m.status(agentConfig); st != StatusStopped || err != nil {
		t.Errorf("status %v, %v", st, err)
	}
	if err := m.start(agentConfig); err != nil {
		t.Fatal(err)
	}
	if err := m.stop(agentConfig); err != nil {
		t.Fatal(err)
	}
	r.expect(t, print, print, "launchctl bootstrap system "+plist, print)

	// Loaded and waiting: start kicks it.
	r.answers[print] = fakeAnswer{printWaiting, 0}
	if st, _ := m.status(agentConfig); st != StatusStopped {
		t.Errorf("status of a waiting job %v", st)
	}
	if err := m.start(agentConfig); err != nil {
		t.Fatal(err)
	}
	r.expect(t, print, print, "launchctl kickstart system/GWatchAgent")

	// Running: status says so, start is a no-op, restart kills and starts,
	// stop unloads, uninstall unloads and removes.
	r.answers[print] = fakeAnswer{printRunning, 0}
	if st, err := m.status(agentConfig); st != StatusRunning || err != nil {
		t.Errorf("status %v, %v", st, err)
	}
	for _, f := range []func(Config) error{m.start, m.restart, m.stop, m.uninstall} {
		if err := f(agentConfig); err != nil {
			t.Fatal(err)
		}
	}
	r.expect(t, print, print, print, "launchctl kickstart -k system/GWatchAgent", print, "launchctl bootout system/GWatchAgent", print, "launchctl bootout system/GWatchAgent")
	if root.exists("/Library/LaunchDaemons/GWatchAgent.plist") {
		t.Error("plist outlived the uninstall")
	}
}

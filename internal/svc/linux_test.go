package svc

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var agentConfig = Config{
	Name:        "GWatchAgent",
	DisplayName: "GWatch Hardware Agent",
	Description: "Reports this computer's health.",
	Arguments:   []string{"run"},
	EnvFile:     "/etc/gwatch-agent/agent.env",
}

const agentExe = "/usr/local/libexec/gwatch-agent/gwatch-agent"

func readRoot(t *testing.T, root fsRoot, p string) string {
	t.Helper()
	b, err := os.ReadFile(root.path(p))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSystemdInstallUninstall(t *testing.T) {
	root := fsRoot(t.TempDir())
	r := &fakeRunner{}
	m := systemdManager{root: root, r: r}

	if err := m.install(agentConfig, agentExe); err != nil {
		t.Fatal(err)
	}
	r.expect(t, "systemctl daemon-reload", "systemctl enable GWatchAgent.service")
	unit := readRoot(t, root, "/etc/systemd/system/GWatchAgent.service")
	if !strings.Contains(unit, "ExecStart="+agentExe+` "run"`) {
		t.Errorf("unit:\n%s", unit)
	}
	if fi, _ := os.Stat(root.path("/etc/systemd/system/GWatchAgent.service")); runtime.GOOS != "windows" && fi.Mode().Perm() != 0644 {
		t.Errorf("unit mode %v", fi.Mode().Perm())
	}

	// A second install is refused and leaves the unit alone.
	if err := m.install(agentConfig, "/elsewhere"); err == nil || !strings.Contains(err.Error(), "already installed") {
		t.Errorf("second install: %v", err)
	}
	r.expect(t)

	if err := m.uninstall(agentConfig); err != nil {
		t.Fatal(err)
	}
	r.expect(t, "systemctl disable GWatchAgent.service", "systemctl daemon-reload")
	if root.exists("/etc/systemd/system/GWatchAgent.service") {
		t.Error("unit outlived the uninstall")
	}
	if err := m.uninstall(agentConfig); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("uninstalling again: %v", err)
	}
}

func TestSystemdControl(t *testing.T) {
	r := &fakeRunner{}
	m := systemdManager{root: fsRoot(t.TempDir()), r: r}
	for _, f := range []func(Config) error{m.start, m.stop, m.restart} {
		if err := f(agentConfig); err != nil {
			t.Fatal(err)
		}
	}
	r.expect(t, "systemctl start GWatchAgent.service", "systemctl stop GWatchAgent.service", "systemctl restart GWatchAgent.service")
}

func TestSystemdStatus(t *testing.T) {
	const isActive = "systemctl is-active GWatchAgent.service"
	const listFiles = "systemctl list-unit-files --type=service --no-legend GWatchAgent.service"
	for _, tc := range []struct {
		name    string
		answers map[string]fakeAnswer
		want    Status
		wantErr error
		anyErr  bool
	}{
		{"active", map[string]fakeAnswer{isActive: {"active\n", 0}}, StatusRunning, nil, false},
		{"activating", map[string]fakeAnswer{isActive: {"activating\n", 3}}, StatusRunning, nil, false},
		{"stopped", map[string]fakeAnswer{isActive: {"inactive\n", 3}, listFiles: {"GWatchAgent.service enabled enabled\n", 0}}, StatusStopped, nil, false},
		{"not installed", map[string]fakeAnswer{isActive: {"inactive\n", 3}, listFiles: {"", 1}}, StatusUnknown, ErrNotInstalled, false},
		{"failed", map[string]fakeAnswer{isActive: {"failed\n", 3}}, StatusUnknown, nil, true},
		{"no systemctl", map[string]fakeAnswer{isActive: {"", -1}}, StatusUnknown, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := systemdManager{root: fsRoot(t.TempDir()), r: &fakeRunner{answers: tc.answers}}
			got, err := m.status(agentConfig)
			if got != tc.want {
				t.Errorf("status %v, want %v", got, tc.want)
			}
			switch {
			case tc.wantErr != nil && !errors.Is(err, tc.wantErr):
				t.Errorf("err %v, want %v", err, tc.wantErr)
			case tc.wantErr == nil && tc.anyErr != (err != nil):
				t.Errorf("err %v", err)
			}
		})
	}
}

func TestOpenRC(t *testing.T) {
	root := fsRoot(t.TempDir())
	r := &fakeRunner{answers: map[string]fakeAnswer{}}
	m := openrcManager{root: root, r: r}

	if _, err := m.status(agentConfig); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("status before install: %v", err)
	}
	if err := m.install(agentConfig, agentExe); err != nil {
		t.Fatal(err)
	}
	r.expect(t, "rc-update add GWatchAgent default")
	if fi, _ := os.Stat(root.path("/etc/init.d/GWatchAgent")); runtime.GOOS != "windows" && fi.Mode().Perm() != 0755 {
		t.Errorf("script mode %v", fi.Mode().Perm())
	}
	if s := readRoot(t, root, "/etc/init.d/GWatchAgent"); !strings.Contains(s, "command='"+agentExe+"'") {
		t.Errorf("script:\n%s", s)
	}

	if st, err := m.status(agentConfig); st != StatusRunning || err != nil {
		t.Errorf("status %v, %v", st, err)
	}
	r.answers["rc-service GWatchAgent status"] = fakeAnswer{" * status: stopped\n", 3}
	if st, err := m.status(agentConfig); st != StatusStopped || err != nil {
		t.Errorf("status %v, %v", st, err)
	}
	r.calls = nil

	if err := m.start(agentConfig); err != nil {
		t.Fatal(err)
	}
	if err := m.stop(agentConfig); err != nil {
		t.Fatal(err)
	}
	if err := m.uninstall(agentConfig); err != nil {
		t.Fatal(err)
	}
	r.expect(t, "rc-service GWatchAgent start", "rc-service GWatchAgent stop", "rc-update del GWatchAgent")
	if root.exists("/etc/init.d/GWatchAgent") {
		t.Error("script outlived the uninstall")
	}
}

func TestSysV(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SysV runlevel links are symlinks, which Windows only makes for administrators")
	}
	root := fsRoot(t.TempDir())
	for _, d := range []string{"/etc/rc2.d", "/etc/rc3.d", "/etc/rc0.d"} {
		if err := os.MkdirAll(root.path(d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	script := root.path("/etc/init.d/GWatchAgent")
	r := &fakeRunner{answers: map[string]fakeAnswer{script + " status": {"Stopped\n", 3}}}
	m := sysvManager{root: root, r: r}
	if managerRestartsOnExit(m) {
		t.Error("a System V script does not start the program again when it exits")
	}
	if !managerRestartsOnExit(systemdManager{}) || !managerRestartsOnExit(openrcManager{}) || !managerRestartsOnExit(launchdManager{}) {
		t.Error("systemd, OpenRC and launchd all restart a service that exits")
	}

	if err := m.install(agentConfig, agentExe); err != nil {
		t.Fatal(err)
	}
	r.expect(t)
	for _, l := range []string{"/etc/rc2.d/S50GWatchAgent", "/etc/rc3.d/S50GWatchAgent", "/etc/rc0.d/K02GWatchAgent"} {
		if target, err := os.Readlink(root.path(l)); err != nil || target != "/etc/init.d/GWatchAgent" {
			t.Errorf("%s -> %q, %v", l, target, err)
		}
	}
	if root.exists("/etc/rc5.d/S50GWatchAgent") {
		t.Error("a link was made in a runlevel directory that does not exist")
	}
	if st, err := m.status(agentConfig); st != StatusStopped || err != nil {
		t.Errorf("status %v, %v", st, err)
	}
	if err := m.start(agentConfig); err != nil {
		t.Fatal(err)
	}
	r.expect(t, script+" status", script+" start")

	if err := m.uninstall(agentConfig); err != nil {
		t.Fatal(err)
	}
	entries, _ := filepath.Glob(root.path("/etc/rc*.d/*"))
	if len(entries) != 0 || root.exists("/etc/init.d/GWatchAgent") {
		t.Errorf("left behind: %v", entries)
	}
	if _, err := m.status(agentConfig); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("status after uninstall: %v", err)
	}
}

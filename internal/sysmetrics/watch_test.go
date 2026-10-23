package sysmetrics

import (
	"context"
	"reflect"
	"testing"

	"github.com/jxburros/GWatch/internal/model"
)

// The parsers below read captured output from real machines; they are plain
// text functions so they are tested on every platform.

const systemctlShowFixture = `Id=nginx.service
LoadState=loaded
ActiveState=active
SubState=running
Description=A high performance web server and a reverse proxy server

Id=smbd.service
LoadState=loaded
ActiveState=failed
SubState=failed
Description=Samba SMB Daemon

Id=nosuch.service
LoadState=not-found
ActiveState=inactive
SubState=dead
Description=nosuch.service

Id=ssh.service
LoadState=loaded
ActiveState=reloading
SubState=reload
Description=OpenBSD Secure Shell server
`

func TestSystemdServicesFromShow(t *testing.T) {
	names := []string{"nginx", "smbd.service", "nosuch", "sshd"}
	got, err := systemdServices(names, parseSystemctlShow(systemctlShowFixture))
	if err != nil {
		t.Fatal(err)
	}
	want := []model.HostService{
		{Name: "nginx", Running: true, State: "active (running)", Description: "A high performance web server and a reverse proxy server"},
		{Name: "smbd.service", Running: false, State: "failed", Description: "Samba SMB Daemon"},
		{Name: "nosuch", Running: false, State: "not found"},
		// An alias (sshd is ssh.service on Debian) answers under the name
		// that was asked for, which is what the metric key is.
		{Name: "sshd", Running: true, State: "reloading (reload)", Description: "OpenBSD Secure Shell server"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	if _, err := systemdServices(names[:2], parseSystemctlShow(systemctlShowFixture)); err == nil {
		t.Error("a count mismatch must not pair names with the wrong units")
	}
}

func TestSystemdUnitName(t *testing.T) {
	for in, want := range map[string]string{
		"nginx":           "nginx.service",
		"nginx.service":   "nginx.service",
		"php8.2-fpm":      "php8.2-fpm.service",
		"docker.socket":   "docker.socket",
		"getty@tty1":      "getty@tty1.service",
		"backup.timer":    "backup.timer",
		"srv-media.mount": "srv-media.mount",
	} {
		if got := systemdUnitName(in); got != want {
			t.Errorf("%s: got %s, want %s", in, got, want)
		}
	}
}

const listUnitsFixture = `cron.service                         loaded    active   running Regular background program processing daemon
● smbd.service                       loaded    failed   failed  Samba SMB Daemon
docker.service                       loaded    inactive dead    Docker Application Container Engine
plymouth-quit-wait.service           not-found inactive dead    plymouth-quit-wait.service
systemd-fsck@dev-sda1.service        loaded    active   exited  File System Check on /dev/sda1
`

func TestParseSystemctlListUnits(t *testing.T) {
	got := parseSystemctlListUnits(listUnitsFixture)
	want := []model.HostService{
		{Name: "cron", Running: true, State: "active (running)", Description: "Regular background program processing daemon"},
		{Name: "smbd", Running: false, State: "failed", Description: "Samba SMB Daemon"},
		{Name: "docker", Running: false, State: "inactive (dead)", Description: "Docker Application Container Engine"},
		{Name: "systemd-fsck@dev-sda1", Running: true, State: "active (exited)", Description: "File System Check on /dev/sda1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestProcName(t *testing.T) {
	cases := []struct{ comm, cmdline, want string }{
		{"nginx\n", "nginx: master process /usr/sbin/nginx\x00", "nginx"},
		// comm is cut at 15 characters; argv[0] says what the rest was.
		{"Plex Media Serv\n", "/usr/lib/plexmediaserver/Plex Media Server\x00", "Plex Media Server"},
		// A program that rewrote its argv[0] keeps the kernel's name.
		{"postgres-backgr\n", "postgres: checkpointer\x00", "postgres-backgr"},
		// Kernel threads have no command line.
		{"kworker/0:1\n", "", ""},
	}
	for _, c := range cases {
		if got := procName(c.comm, c.cmdline); got != c.want {
			t.Errorf("%q: got %q, want %q", c.comm, got, c.want)
		}
	}
}

func TestCountProcesses(t *testing.T) {
	running := []string{"nginx", "nginx", "nginx", "sshd", "Plex Media Server.exe", "svchost.exe"}
	got := countProcesses(running, []string{"nginx", "plex", "sshd"}, false)
	want := []model.HostProcess{{Name: "nginx", Count: 3}, {Name: "plex", Count: 0}, {Name: "sshd", Count: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("exact: got %+v", got)
	}
	// Windows: no case, and the .exe is optional on either side.
	got = countProcesses(running, []string{"plex media server", "SVCHOST.EXE", "NGINX"}, true)
	want = []model.HostProcess{{Name: "plex media server", Count: 1}, {Name: "SVCHOST.EXE", Count: 1}, {Name: "NGINX", Count: 3}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("windows: got %+v", got)
	}
}

const launchctlFixture = `PID	Status	Label
-	0	com.apple.SafariHistoryServiceAgent
412	0	com.openssh.sshd
-	78	org.postgresql.postgres
1203	-9	homebrew.mxcl.nginx
`

func TestLaunchctlList(t *testing.T) {
	all := parseLaunchctlList(launchctlFixture)
	if len(all) != 4 {
		t.Fatalf("got %+v", all)
	}
	got := pickServices(all, []string{"sshd", "org.postgresql.postgres", "nginx", "absent"}, launchdMatch)
	want := []model.HostService{
		{Name: "sshd", Running: true, State: "running"},
		{Name: "org.postgresql.postgres", State: "not running (last exit status 78)"},
		{Name: "nginx", Running: true, State: "running"},
		{Name: "absent", State: "not found"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestWindowsServicePicking(t *testing.T) {
	all := []model.HostService{
		{Name: "Spooler", Description: "Print Spooler", Running: true, State: windowsServiceState(4)},
		{Name: "wuauserv", Description: "Windows Update", State: windowsServiceState(1)},
	}
	got := pickServices(all, []string{"spooler", "Windows Update"}, windowsServiceMatch)
	if !got[0].Running || got[0].Name != "spooler" || got[1].Running || got[1].State != "stopped" {
		t.Errorf("got %+v", got)
	}
	if windowsServiceState(2) != "start pending" || windowsServiceState(42) != "state 42" {
		t.Error("state words")
	}
}

func TestParsePsComm(t *testing.T) {
	got := parsePsComm("launchd\nsyslogd\nPlex Media Server\n\nsyslogd\n")
	inv := processInventory(got)
	want := []model.InventoryItem{
		{Name: "launchd", Detail: "1 running"},
		{Name: "Plex Media Server", Detail: "1 running"},
		{Name: "syslogd", Detail: "2 running"},
	}
	if !reflect.DeepEqual(inv, want) {
		t.Errorf("got %+v", inv)
	}
}

// Nothing watched means nothing is looked for: readWatched is never called
// and a reading carries neither list.
func TestCollectorWatchesNothingByDefault(t *testing.T) {
	c := NewCollector("")
	c.SetWatch([]string{" ", ""}, nil)
	if c.services != nil || c.processes != nil {
		t.Errorf("blank names should leave nothing watched: %+v %+v", c.services, c.processes)
	}
	svcs, procs, warn := readWatched(context.Background(), nil, nil)
	if svcs != nil || procs != nil || warn != nil {
		t.Error("readWatched with nothing to watch must do nothing")
	}
}

// A selection keeps only the services and programs it names, and leaves
// them alone when it names none.
func TestApplyKeepsWatchedNames(t *testing.T) {
	m := model.HostMetrics{
		Services:  []model.HostService{{Name: "nginx", Running: true}, {Name: "smbd"}},
		Processes: []model.HostProcess{{Name: "plex", Count: 1}},
	}
	all := Apply(m, model.CollectSelection{Metrics: []string{model.MetricCPU}}, true)
	if len(all.Services) != 2 || len(all.Processes) != 1 {
		t.Errorf("a family selection must not drop watched names: %+v", all)
	}
	some := Apply(m, model.CollectSelection{Services: []string{"nginx"}, Processes: []string{"other"}}, true)
	if len(some.Services) != 1 || some.Services[0].Name != "nginx" || len(some.Processes) != 0 {
		t.Errorf("got %+v", some)
	}
}

//go:build linux

package serviceinstall

import (
	"os"
	"strings"
	"testing"
)

// unitLines returns the directives of a unit as key -> values, in order, so a
// test can ask what the file says without caring about comments or layout.
func unitLines(unit string) map[string][]string {
	out := map[string][]string{}
	for _, l := range strings.Split(unit, "\n") {
		if l == "" || l[0] == '#' || l[0] == '[' {
			continue
		}
		k, v, ok := strings.Cut(l, "=")
		if ok {
			out[k] = append(out[k], v)
		}
	}
	return out
}

func one(t *testing.T, d map[string][]string, key string) string {
	t.Helper()
	if len(d[key]) != 1 {
		t.Fatalf("%s appears %d times, want once: %v", key, len(d[key]), d[key])
	}
	return d[key][0]
}

func TestSystemdUnitDefaults(t *testing.T) {
	unit := SystemdUnit(UnitConfig{Executable: BinaryPath, User: ServiceUser, DataDir: DefaultDataDir, EnvFile: EnvPath, Listen: "127.0.0.1:7230"})
	d := unitLines(unit)
	want := map[string]string{
		"User":                     "gwatch",
		"Group":                    "gwatch",
		"ExecStart":                "/usr/local/bin/gwatch run",
		"EnvironmentFile":          "-/etc/gwatch/gwatch.env",
		"Restart":                  "on-failure",
		"RestartSec":               "5s",
		"RestartPreventExitStatus": "78",
		"StateDirectory":           "gwatch",
		"StateDirectoryMode":       "0700",
		"ReadWritePaths":           "/var/lib/gwatch",
		"UMask":                    "0077",
		"AmbientCapabilities":      "CAP_NET_RAW",
		"CapabilityBoundingSet":    "CAP_NET_RAW",
		"NoNewPrivileges":          "yes",
		"ProtectSystem":            "strict",
		"ProtectHome":              "yes",
		"PrivateTmp":               "yes",
		"PrivateDevices":           "yes",
		"RestrictAddressFamilies":  "AF_INET AF_INET6 AF_UNIX AF_NETLINK",
		"WantedBy":                 "multi-user.target",
		"After":                    "network-online.target",
		"Wants":                    "network-online.target",
		"SystemCallArchitectures":  "native",
		"RestrictSUIDSGID":         "yes",
		"ProtectKernelTunables":    "yes",
		"ProtectControlGroups":     "yes",
		"LockPersonality":          "yes",
		"RestrictNamespaces":       "yes",
		"StandardOutput":           "journal",
		"Description":              "GWatch Network Monitor",
		"StartLimitBurst":          "10",
		"StartLimitIntervalSec":    "60",
		"Type":                     "simple",
		"ProtectHostname":          "yes",
		"ProtectClock":             "yes",
		"ProtectKernelModules":     "yes",
		"ProtectKernelLogs":        "yes",
		"RestrictRealtime":         "yes",
		"StandardError":            "journal",
		"Documentation":            "https://github.com/jxburros/GWatch/blob/main/docs/INSTALL-LINUX.md",
	}
	for k, v := range want {
		if got := one(t, d, k); got != v {
			t.Errorf("%s=%s, want %s", k, got, v)
		}
	}
	// Script actions run interpreters that generate code; this would break
	// them, and it would apply to every program the service starts.
	if _, ok := d["MemoryDenyWriteExecute"]; ok {
		t.Error("MemoryDenyWriteExecute must stay off: it breaks script actions run by node and other JITs")
	}
	if _, ok := d["SyslogLevelPrefix"]; ok {
		t.Error("unexpected SyslogLevelPrefix")
	}
	if !strings.HasSuffix(unit, "\n") {
		t.Error("unit should end with a newline")
	}
}

func TestSystemdUnitLowPortGetsBindCapability(t *testing.T) {
	d := unitLines(SystemdUnit(UnitConfig{Executable: BinaryPath, User: ServiceUser, DataDir: DefaultDataDir, EnvFile: EnvPath, Listen: "0.0.0.0:80"}))
	for _, k := range []string{"AmbientCapabilities", "CapabilityBoundingSet"} {
		if got := one(t, d, k); got != "CAP_NET_RAW CAP_NET_BIND_SERVICE" {
			t.Errorf("%s=%s, want both capabilities for port 80", k, got)
		}
	}
}

func TestSystemdUnitCustomDataDir(t *testing.T) {
	d := unitLines(SystemdUnit(UnitConfig{Executable: BinaryPath, User: ServiceUser, DataDir: "/srv/gwatch%1", EnvFile: EnvPath, Listen: "127.0.0.1:7230"}))
	if _, ok := d["StateDirectory"]; ok {
		t.Error("StateDirectory is only for the default /var/lib/gwatch")
	}
	if got := one(t, d, "ReadWritePaths"); got != "/srv/gwatch%%1" {
		t.Errorf("ReadWritePaths=%s, want the path with its percent sign escaped", got)
	}
}

func TestNeedsBindCapability(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:7230": false, "0.0.0.0:80": true, "[::]:443": true, ":1023": true,
		":1024": false, "0.0.0.0:0": false, "nonsense": false, "host:http": false,
	} {
		if got := NeedsBindCapability(addr); got != want {
			t.Errorf("NeedsBindCapability(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestCheckUnitPath(t *testing.T) {
	for _, ok := range []string{"/var/lib/gwatch", "/srv/monitor/data", "/opt/gwatch"} {
		if err := CheckUnitPath("data", ok); err != nil {
			t.Errorf("%s refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"relative/dir", "/srv/my data", "/home/pat/gwatch", "/root/.local/share/gwatch", "/root", "/run/user/1000/x", `/srv/a"b`, "/var", "/var/lib", "/etc/", "/usr/local", "/"} {
		if err := CheckUnitPath("data", bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	if UnderProtectedHome("/homework") {
		t.Error("/homework is not under /home")
	}
}

func TestParseEnv(t *testing.T) {
	got := ParseEnv(`# comment
; another
GWATCH_DATA_DIR="/var/lib/gwatch"
export GWATCH_LISTEN=0.0.0.0:7230
GWATCH_DB_PASSWORD="a \"quoted\" \\ pass"
SINGLE='lit\eral'
  SPACED = value
broken line
=novalue
`)
	want := map[string]string{
		"GWATCH_DATA_DIR":    "/var/lib/gwatch",
		"GWATCH_LISTEN":      "0.0.0.0:7230",
		"GWATCH_DB_PASSWORD": `a "quoted" \ pass`,
		"SINGLE":             `lit\eral`,
		"SPACED":             "value",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

// The file an older release wrote (Go %q quoting) must still be read.
func TestParseEnvReadsOldInstallFile(t *testing.T) {
	got := ParseEnv("GWATCH_DATA_DIR=\"/root/.local/share/gwatch\"\nGWATCH_LISTEN=\"127.0.0.1:7230\"\n")
	if got["GWATCH_DATA_DIR"] != "/root/.local/share/gwatch" || got["GWATCH_LISTEN"] != "127.0.0.1:7230" {
		t.Errorf("got %v", got)
	}
}

func TestMergeEnv(t *testing.T) {
	existing := "# mine\nGWATCH_LISTEN=\"127.0.0.1:7230\"\nGWATCH_DB_PASSWORD=\"s3cret\"\nGWATCH_LISTEN=\"0.0.0.0:1\"\n"
	out, err := MergeEnv(existing, map[string]string{"GWATCH_LISTEN": "0.0.0.0:7230", "GWATCH_DATA_DIR": `/srv/"odd"`})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "# mine\nGWATCH_LISTEN=\"0.0.0.0:7230\"\nGWATCH_DB_PASSWORD=\"s3cret\"\n") {
		t.Errorf("existing lines not kept in place:\n%s", out)
	}
	if !strings.Contains(out, "# GWATCH_LISTEN=\"0.0.0.0:1\"") {
		t.Errorf("duplicate line not commented out:\n%s", out)
	}
	parsed := ParseEnv(out)
	if parsed["GWATCH_LISTEN"] != "0.0.0.0:7230" || parsed["GWATCH_DATA_DIR"] != `/srv/"odd"` || parsed["GWATCH_DB_PASSWORD"] != "s3cret" {
		t.Errorf("round trip lost something: %v", parsed)
	}
	// Running it again changes nothing.
	again, _ := MergeEnv(out, map[string]string{"GWATCH_LISTEN": "0.0.0.0:7230", "GWATCH_DATA_DIR": `/srv/"odd"`})
	if again != out {
		t.Errorf("merge is not idempotent:\n%s\nthen\n%s", out, again)
	}
	fresh, _ := MergeEnv("", map[string]string{"GWATCH_LISTEN": "127.0.0.1:7230"})
	if !strings.HasPrefix(fresh, "# Settings for the GWatch service") || !strings.HasSuffix(fresh, "GWATCH_LISTEN=\"127.0.0.1:7230\"\n") {
		t.Errorf("new file:\n%s", fresh)
	}
	if _, err := MergeEnv("", map[string]string{"X": "a\nb"}); err == nil {
		t.Error("a value with a line break was accepted")
	}
}

func TestSafeToPurge(t *testing.T) {
	cases := []struct {
		dir   string
		names []string
		ok    bool
	}{
		{"/var/lib/gwatch", []string{"anything"}, true},
		{"/srv/gwatch", nil, true},
		{"/srv/gwatch", []string{"gwatch.db", "logs"}, true},
		{"/srv/gwatch", []string{"database.json"}, true},
		{"/srv/photos", []string{"holiday.jpg"}, false},
		{"/", nil, false},
		{"/var", []string{"gwatch.db"}, false},
		{"/var/lib/", nil, false},
		{"/etc", nil, false},
		{"relative", nil, false},
	}
	for _, c := range cases {
		err := SafeToPurge(c.dir, c.names)
		if (err == nil) != c.ok {
			t.Errorf("SafeToPurge(%q, %v) = %v, want ok=%v", c.dir, c.names, err, c.ok)
		}
	}
}

func TestMoveDataDir(t *testing.T) {
	base := t.TempDir()
	from, to := base+"/root/.local/share/gwatch", base+"/var/lib/gwatch"
	if err := moveDataDir(from, to); err != nil {
		t.Fatalf("a missing source is nothing to move: %v", err)
	}
	if err := os.MkdirAll(from, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(from+"/gwatch.db", []byte("db"), 0600); err != nil {
		t.Fatal(err)
	}
	// An empty destination (StateDirectory= may have made one) is no obstacle.
	if err := os.MkdirAll(to, 0700); err != nil {
		t.Fatal(err)
	}
	if err := moveDataDir(from, to); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(to + "/gwatch.db"); err != nil || string(b) != "db" {
		t.Fatalf("database not moved: %q %v", b, err)
	}
	// A destination with files in it is never merged into.
	if err := os.MkdirAll(from, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(from+"/gwatch.db", []byte("other"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := moveDataDir(from, to); err == nil {
		t.Fatal("moved over a destination that already holds a database")
	}
}

func TestWriteFileAtomicSetsMode(t *testing.T) {
	p := t.TempDir() + "/gwatch.env"
	if err := writeFileAtomic(p, []byte("A=1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil || fi.Mode().Perm() != 0600 {
		t.Fatalf("mode %v, err %v", fi.Mode(), err)
	}
}

package svc

import (
	"encoding/xml"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jxburros/GWatch/internal/serviceinstall"
)

// The agent's unit is serviceinstall.SystemdScript with its environment file
// swapped; for the paths it is installed at it must come out as the kardianos
// library rendered it, so that an agent installed by an earlier release and
// one installed now run the same unit.
func TestSystemdUnitFromSystemdScript(t *testing.T) {
	c := Config{
		Name:        "GWatchAgent",
		Description: "Reports this computer's health.",
		Arguments:   []string{"run"},
		SystemdUnit: strings.ReplaceAll(serviceinstall.SystemdScript, "/etc/gwatch/gwatch.env", "/etc/gwatch-agent/agent.env"),
	}
	got, err := SystemdUnit(c, "/usr/local/libexec/gwatch-agent/gwatch-agent")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Description=Reports this computer's health.\n",
		"ExecStart=/usr/local/libexec/gwatch-agent/gwatch-agent \"run\"\n",
		"EnvironmentFile=-/etc/gwatch-agent/agent.env\n",
		"Restart=on-failure\n",
		"RestartSec=5s\n",
		"WantedBy=multi-user.target\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("unit lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "User=") {
		t.Errorf("unit names a user; the agent runs as root:\n%s", got)
	}
}

func TestSystemdUnitQuoting(t *testing.T) {
	got, err := SystemdUnit(Config{Name: "x", Arguments: []string{"run", "--config", `/srv/a "b" 100%\c`}}, "/opt/my app/x")
	if err != nil {
		t.Fatal(err)
	}
	want := `ExecStart=/opt/my\x20app/x "run" "--config" "/srv/a \"b\" 100%%\\c"`
	if !strings.Contains(got, want+"\n") {
		t.Errorf("ExecStart:\n%s\nwant line %s", got, want)
	}
	// systemd expands $NAME and ${NAME} in ExecStart='s arguments, so a
	// literal dollar there must be doubled or the service is started with
	// another path. The program path itself is not expanded.
	got, err = SystemdUnit(Config{Name: "x", Arguments: []string{"--config", `/srv/$HOME/${X}.json`}}, "/opt/$x/app")
	if err != nil {
		t.Fatal(err)
	}
	if want := `ExecStart=/opt/$x/app "--config" "/srv/$$HOME/$${X}.json"`; !strings.Contains(got, want+"\n") {
		t.Errorf("ExecStart:\n%s\nwant line %s", got, want)
	}
	if _, err := SystemdUnit(Config{Name: "x", SystemdUnit: "{{.Nope"}, "/x"); err == nil {
		t.Error("a broken template rendered without an error")
	}
}

func TestLaunchdPlist(t *testing.T) {
	got := LaunchdPlist(Config{Name: "GWatchAgent", Arguments: []string{"run", "--config", "/a&b/<c>.json"}}, "/usr/local/libexec/gwatch-agent/gwatch-agent")
	// It has to be well-formed XML, or launchd refuses the whole file.
	dec := xml.NewDecoder(strings.NewReader(got))
	var strs []string
	inString := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("not well-formed: %v\n%s", err, got)
		}
		switch v := tok.(type) {
		case xml.StartElement:
			inString = v.Name.Local == "string"
		case xml.CharData:
			if inString {
				strs = append(strs, string(v))
			}
		case xml.EndElement:
			inString = false
		}
	}
	want := []string{"GWatchAgent", "/usr/local/libexec/gwatch-agent/gwatch-agent", "run", "--config", "/a&b/<c>.json", "/var/log/GWatchAgent.out.log", "/var/log/GWatchAgent.err.log"}
	if strings.Join(strs, "|") != strings.Join(want, "|") {
		t.Errorf("strings %q, want %q", strs, want)
	}
	for _, key := range []string{"<key>KeepAlive</key>\n\t<true/>", "<key>RunAtLoad</key>\n\t<true/>", "<key>Disabled</key>\n\t<false/>"} {
		if !strings.Contains(got, key) {
			t.Errorf("plist lacks %q:\n%s", key, got)
		}
	}
}

func TestShQuote(t *testing.T) {
	for in, want := range map[string]string{
		"":          "''",
		"run":       "'run'",
		"it's":      `'it'\''s'`,
		"$HOME `x`": "'$HOME `x`'",
	} {
		if got := shQuote(in); got != want {
			t.Errorf("shQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

// shell runs a script with sh and returns what it printed, skipping the test
// on a machine without one.
func shell(t *testing.T, script string) string {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	out, err := exec.Command(sh, "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("sh: %v\n%s\n--- script ---\n%s", err, out, script)
	}
	return string(out)
}

// OpenRC evals command_args, so what matters is that the words come back
// out exactly as they went in, whatever is in them; and the environment file
// has to reach the program.
func TestOpenRCScriptArguments(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, "it's.env")
	if err := os.WriteFile(env, []byte("GWATCH_LISTEN=\"0.0.0.0:7230\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"run", "--config", `/srv/it's "here"/$HOME/a b`, ""}
	script := OpenRCScript(Config{Name: "GWatch", DisplayName: "GWatch Network Monitor", Description: "A monitor; it's \"fine\"", Arguments: args, EnvFile: env}, "/usr/local/libexec/gwatch/gwatch")
	if !strings.HasPrefix(script, "#!/sbin/openrc-run\n") || !strings.Contains(script, "supervisor=supervise-daemon\n") {
		t.Fatalf("not an OpenRC script:\n%s", script)
	}
	out := shell(t, script+"\neval \"set -- $command_args\"\nfor a in \"$command\" \"$@\"; do printf '[%s]\\n' \"$a\"; done\necho \"$name|$GWATCH_LISTEN\"\neval \"set -- $supervise_daemon_args\"\necho \"$@\"\n")
	want := "[/usr/local/libexec/gwatch/gwatch]\n[run]\n[--config]\n[/srv/it's \"here\"/$HOME/a b]\n[]\nGWatch Network Monitor|0.0.0.0:7230\n--stdout /var/log/gwatch.log --stderr /var/log/gwatch.err\n"
	if out != want {
		t.Errorf("got\n%s\nwant\n%s", out, want)
	}
}

func TestSysVScript(t *testing.T) {
	script := SysVScript(Config{Name: "GWatchAgent", DisplayName: "GWatch Hardware Agent", Description: "Reports\nhealth.", Arguments: []string{"run", "--config", "it's"}, EnvFile: "/etc/gwatch-agent/agent.env"}, "/opt/my app/gwatch-agent")
	for _, want := range []string{
		"#!/bin/sh\n",
		"# Description:       Reports health.\n",
		"exec '/opt/my app/gwatch-agent' 'run' '--config' 'it'\\''s'\n",
		"pid_file='/var/run/GWatchAgent.pid'\n",
		"stdout_log='/var/log/gwatch-agent.log'\n",
		". '/etc/gwatch-agent/agent.env'\n",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q:\n%s", want, script)
		}
	}
	if sh, err := exec.LookPath("sh"); err == nil {
		if out, err := exec.Command(sh, "-n", "-c", script).CombinedOutput(); err != nil {
			t.Errorf("sh -n: %v\n%s", err, out)
		}
	}
}

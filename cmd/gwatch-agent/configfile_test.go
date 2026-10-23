package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// isolate points the agent's data directory at a fresh temporary one and clears
// everything in the environment that would change what it reads.
func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("GWATCH_AGENT_TOKEN_FILE", filepath.Join(dir, "agent-token"))
	for _, k := range []string{"GWATCH_AGENT_CONFIG", "GWATCH_SERVER", "GWATCH_AGENT_TOKEN", "GWATCH_AGENT_INTERVAL",
		"GWATCH_AGENT_LISTEN", "GWATCH_AGENT_CERT_PIN", "GWATCH_AGENT_REPO", "GWATCH_AGENT_AUTO_UPDATE", "GWATCH_HOST_ROOT", "GWATCH_AGENT_CODE"} {
		t.Setenv(k, "")
	}
	old := configPathOverride
	configPathOverride = ""
	t.Cleanup(func() { configPathOverride = old })
	return dir
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestNoFileMeansDefaults(t *testing.T) {
	isolate(t)
	c, problems, warnings := baseConfig()
	if len(problems)+len(warnings) > 0 || c.fileExists {
		t.Fatalf("problems=%v warnings=%v exists=%v", problems, warnings, c.fileExists)
	}
	if c.interval != defaultInterval || c.checkIn != defaultCheckIn || c.buffer != defaultBuffer || !c.autoUpdate || c.repo != defaultRepo {
		t.Errorf("defaults: %+v", c)
	}
	for k, src := range c.sources {
		if src != srcDefault {
			t.Errorf("%s came from %q with nothing configured", k, src)
		}
	}
}

func TestTheFileIsReadAndTheEnvironmentOverridesIt(t *testing.T) {
	dir := isolate(t)
	writeFile(t, filepath.Join(dir, "agent.json"), `{
  "server": "https://from-file:7230",
  "token": "gwa_filetoken0000000000000000",
  "name": "from file",
  "interval": "45s",
  "checkIn": "5m",
  "buffer": 7200,
  "autoUpdate": false,
  "collect": {"interfaces": ["eth0", "eth0"], "metrics": ["cpu"]}
}`)
	c, problems, warnings := baseConfig()
	if len(problems)+len(warnings) > 0 {
		t.Fatalf("problems=%v warnings=%v", problems, warnings)
	}
	if c.server != "https://from-file:7230" || c.name != "from file" || c.interval != 45*time.Second ||
		c.checkIn != 5*time.Minute || c.buffer != 2*time.Hour || c.autoUpdate {
		t.Errorf("file settings: %+v", c)
	}
	if len(c.collect.Interfaces) != 1 || c.collect.Metrics[0] != "cpu" {
		t.Errorf("collect: %+v", c.collect)
	}
	if c.sources["interval"] != srcFile || c.sources["token"] != srcFile || c.sources["listen"] != srcDefault {
		t.Errorf("sources: %v", c.sources)
	}

	t.Setenv("GWATCH_SERVER", "https://from-env:7230")
	t.Setenv("GWATCH_AGENT_INTERVAL", "20s")
	c, _, _ = baseConfig()
	if c.server != "https://from-env:7230" || c.interval != 20*time.Second || c.sources["server"] != srcEnv {
		t.Errorf("the environment should win over the file: %+v %v", c, c.sources)
	}
	if c.name != "from file" {
		t.Errorf("settings the environment does not mention stay as the file has them")
	}
	if ov := overrides(c); len(ov) != 2 {
		t.Errorf("config validate should say which file settings are overridden, got %v", ov)
	}
}

func TestMistakesInTheFileAreNamed(t *testing.T) {
	dir := isolate(t)
	path := filepath.Join(dir, "agent.json")

	writeFile(t, path, `{"interval": "banana", "checkIn": "1s", "buffer": "999h", "autoUpdate": "yes", "intervall": "60s"}`)
	_, problems, warnings := baseConfig()
	joined := strings.Join(problems, "\n")
	for _, want := range []string{"interval", "checkIn", "buffer", "autoUpdate"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no problem reported for %s: %v", want, problems)
		}
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "intervall") {
		t.Errorf("a misspelt key should be called out: %v", warnings)
	}

	writeFile(t, path, "{\n  \"server\": \"x\",\n  \"token\": \n}\n")
	_, problems, _ = baseConfig()
	if len(problems) != 1 || !strings.Contains(problems[0], "line 4") {
		t.Errorf("a syntax error should say where: %v", problems)
	}

	writeFile(t, path, `{"collect": {"metrics": ["gpu"]}}`)
	_, problems, _ = baseConfig()
	if len(problems) != 1 || !strings.Contains(problems[0], "gpu") {
		t.Errorf("an unknown metric family should be named: %v", problems)
	}
}

func TestRewritingTheTokenTouchesNothingElse(t *testing.T) {
	dir := isolate(t)
	path := filepath.Join(dir, "agent.json")
	writeFile(t, path, `{"name":"nas","server":"https://s:7230","token":"gwa_old00000000000000000000","note-to-self":{"keep":["me"]},"interval":"2m"}`)
	if err := storeToken(path, "gwa_new00000000000000000000"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	var back map[string]json.RawMessage
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("rewritten file is not JSON: %v\n%s", err, b)
	}
	if string(back["token"]) != `"gwa_new00000000000000000000"` || string(back["interval"]) != `"2m"` || string(back["name"]) != `"nas"` {
		t.Errorf("settings changed: %s", b)
	}
	if !strings.Contains(string(back["note-to-self"]), "keep") {
		t.Errorf("a key the agent does not know was lost: %s", b)
	}
	// Known keys come out in the usual order, not alphabetical.
	if strings.Index(string(b), `"server"`) > strings.Index(string(b), `"token"`) || strings.Index(string(b), `"token"`) > strings.Index(string(b), `"interval"`) {
		t.Errorf("unexpected order:\n%s", b)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0o600 {
			t.Errorf("mode %04o", info.Mode().Perm())
		}
	}
}

func TestLegacyFilesAreReadThenMigrated(t *testing.T) {
	dir := isolate(t)
	writeFile(t, filepath.Join(dir, "agent-token"), "gwa_legacy0000000000000000\n")
	writeFile(t, filepath.Join(dir, "agent-settings.json"), `{"server":"https://old:7230","insecure":true,"name":"oldname"}`)

	c, problems, _ := baseConfig()
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	if c.token != "gwa_legacy0000000000000000" || c.server != "https://old:7230" || !c.insecure || c.name != "oldname" || c.fileExists {
		t.Errorf("legacy settings not read: %+v", c)
	}
	if c.tokenSource != srcLegacy {
		t.Errorf("token source %q", c.tokenSource)
	}

	if !migrateLegacy(c) {
		t.Fatal("migration should have written the file")
	}
	c2, problems, _ := baseConfig()
	if len(problems) > 0 || !c2.fileExists {
		t.Fatalf("after migration: %v %+v", problems, c2)
	}
	if c2.token != c.token || c2.server != c.server || !c2.insecure || c2.name != "oldname" || c2.sources["token"] != srcFile {
		t.Errorf("migrated settings: %+v %v", c2, c2.sources)
	}
	// The old files stay, so rolling back to an older agent still works.
	if _, err := os.Stat(filepath.Join(dir, "agent-token")); err != nil {
		t.Error("the old token file was removed")
	}
	if migrateLegacy(c2) {
		t.Error("an agent that already has a file is not migrated again")
	}

	// A rotation keeps the old file in step.
	if err := storeToken(configFile(), "gwa_rotated00000000000000"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "agent-token")); strings.TrimSpace(string(b)) != "gwa_rotated00000000000000" {
		t.Errorf("legacy token file after rotation: %q", b)
	}
}

func TestPointingAtAnotherServerDropsTrustInTheFirst(t *testing.T) {
	c := config{server: "https://other:7230", fileServer: "https://file:7230", insecure: true, certPin: "abc", sources: map[string]string{}}
	c.dropServerBoundTrust(false, false)
	if c.insecure || c.certPin != "" {
		t.Errorf("insecure/pin followed the agent to a different server: %+v", c)
	}
	c = config{server: "https://other:7230", fileServer: "https://file:7230", insecure: true, certPin: "abc", sources: map[string]string{}}
	c.dropServerBoundTrust(true, true)
	if !c.insecure || c.certPin != "abc" {
		t.Errorf("explicitly given trust must be kept: %+v", c)
	}
	c = config{server: "https://file:7230/", fileServer: "https://file:7230", insecure: true, sources: map[string]string{}}
	c.dropServerBoundTrust(false, false)
	if !c.insecure {
		t.Error("the same server (trailing slash aside) keeps what the file said about it")
	}
}

func TestDescribeTokenShowsEnoughToRecogniseAndNotMore(t *testing.T) {
	tok := "gwa_abcdefghijklmnopqrstuvwxyz234567abcdefgh"
	short := describeToken(tok, false)
	if strings.Contains(short, "mnopqrst") || !strings.HasPrefix(short, tok[:12]) || !strings.Contains(short, tok[len(tok)-4:]) {
		t.Errorf("preview %q", short)
	}
	if describeToken(tok, true) != tok {
		t.Error("the full token should be printed on request")
	}
	if describeToken("", false) != "(none)" {
		t.Error("no token")
	}
}

func TestDurationsReadAsTextOrSeconds(t *testing.T) {
	for in, want := range map[string]time.Duration{`"90s"`: 90 * time.Second, `"15m"`: 15 * time.Minute, `"6h"`: 6 * time.Hour, `30`: 30 * time.Second, `"45"`: 45 * time.Second} {
		got, err := parseDurationJSON(json.RawMessage(in))
		if err != nil || got != want {
			t.Errorf("%s -> %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{`"soon"`, `true`, `[1]`} {
		if _, err := parseDurationJSON(json.RawMessage(in)); err == nil {
			t.Errorf("%s was accepted as a duration", in)
		}
	}
	for d, want := range map[time.Duration]string{time.Minute: "1m", 90 * time.Second: "90s", 6 * time.Hour: "6h", 0: "0s"} {
		if got := formatDuration(d); got != want {
			t.Errorf("formatDuration(%v) = %s, want %s", d, got, want)
		}
	}
}

func TestConfigValidateReportsAndFails(t *testing.T) {
	dir := isolate(t)
	writeFile(t, filepath.Join(dir, "agent.json"), `{"serverr":"x"}`)
	c, problems, warnings := baseConfig()
	if err := runConfigCommand("validate", c, problems, warnings, false); err == nil {
		t.Error("a file with a misspelt key should fail validation")
	}
	writeFile(t, filepath.Join(dir, "agent.json"), `{"server":"https://s:7230"}`)
	c, problems, warnings = baseConfig()
	if err := runConfigCommand("validate", c, problems, warnings, false); err != nil {
		t.Errorf("a good file failed validation: %v", err)
	}
	if err := runConfigCommand("nonsense", c, nil, nil, false); err == nil {
		t.Error("an unknown subcommand should be refused")
	}
	_ = model.CollectSelection{}
}

// A power cut while the file was being written leaves it empty, full of zeros
// or cut off. The agent must come back on the copy every save keeps, not exit
// as "not configured" (which the packaged unit does not restart).
func TestADamagedFileFallsBackToTheCopy(t *testing.T) {
	for name, damaged := range map[string]string{
		"empty":   "",
		"zeros":   strings.Repeat("\x00", 120),
		"cut off": `{"server": "https://gwatch.lan:7230", "tok`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := isolate(t)
			path := filepath.Join(dir, "agent.json")
			doc := &configDoc{path: path, raw: map[string]json.RawMessage{}}
			doc.set("server", "https://gwatch.lan:7230")
			doc.set("token", "gwa_filetoken0000000000000000")
			if err := doc.save(); err != nil {
				t.Fatal(err)
			}
			writeFile(t, path, damaged)
			c, problems, warnings := baseConfig()
			if len(problems) > 0 {
				t.Fatalf("problems: %v", problems)
			}
			if c.server != "https://gwatch.lan:7230" || c.token != "gwa_filetoken0000000000000000" {
				t.Errorf("did not read the copy: server=%q token=%q", c.server, c.token)
			}
			if len(warnings) == 0 || !strings.Contains(warnings[0], "damaged") {
				t.Errorf("warnings = %v, want one saying the file was damaged", warnings)
			}
		})
	}
}

// A mistake made by hand is not a power cut: it is reported, not papered over
// with the copy.
func TestAHandEditMistakeIsStillAnError(t *testing.T) {
	dir := isolate(t)
	path := filepath.Join(dir, "agent.json")
	doc := &configDoc{path: path, raw: map[string]json.RawMessage{}}
	doc.set("server", "https://gwatch.lan:7230")
	if err := doc.save(); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, "{\n  \"server\": \"https://other:7230\",,\n}\n")
	if _, problems, _ := baseConfig(); len(problems) == 0 {
		t.Fatal("a syntax error in the middle of the file was not reported")
	}
}

func TestKeepConfigBackupCopiesAWholeFile(t *testing.T) {
	dir := isolate(t)
	path := filepath.Join(dir, "agent.json")
	writeFile(t, path, `{"server": "https://gwatch.lan:7230"}`)
	keepConfigBackup(path)
	b, err := os.ReadFile(backupPath(path))
	if err != nil || !strings.Contains(string(b), "gwatch.lan") {
		t.Fatalf("backup = %q, %v", b, err)
	}
	writeFile(t, path, "")
	keepConfigBackup(path)
	if b, _ := os.ReadFile(backupPath(path)); !strings.Contains(string(b), "gwatch.lan") {
		t.Fatal("an empty file overwrote the backup")
	}
}

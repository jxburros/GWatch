package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/update"
)

// A packaged agent and its package manager must never both think they own the
// binary. These tests are the "never" half: a build made for a package does
// not download, does not swap and does not roll back, whatever it is asked,
// and nothing a package publishes can be mistaken for a self-update asset.

// asPackaged makes this test binary a packaged build for the length of a test.
func asPackaged(t *testing.T, by string) {
	t.Helper()
	old := packagedBy
	packagedBy = by
	t.Cleanup(func() { packagedBy = old })
}

var everyPackage = []string{"deb", "rpm", "homebrew", "winget", "docker", "some-future-package"}

// TestPackagedBuildNeverReplacesItself goes straight to applyUpdate — the one
// function every install path runs through — with a release that is newer and
// has a build for this platform. A packaged build must refuse before it
// fetches anything, and say how to update instead. An unknown packagedBy
// value is included on purpose: a typo in a packaging job has to fail towards
// "the package manager owns this".
func TestPackagedBuildNeverReplacesItself(t *testing.T) {
	var fetched atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetched.Store(true)
		w.Write([]byte("PAYLOAD"))
	}))
	defer srv.Close()

	for _, by := range everyPackage {
		t.Run(by, func(t *testing.T) {
			asPackaged(t, by)
			fetched.Store(false)
			info := model.UpdateInfo{
				UpdateAvailable: true, LatestVersion: "0.5.0", ReleaseURL: "https://example.com/rel",
				AssetName: "gwatch-agent-" + runtime.GOOS + "-" + runtime.GOARCH, AssetURL: srv.URL + "/dl/agent",
			}
			err := applyUpdate(context.Background(), config{}, info)
			if !errors.Is(err, errPackaged) {
				t.Fatalf("applyUpdate on a %s build = %v, want a refusal", by, err)
			}
			if fetched.Load() {
				t.Error("a packaged build downloaded the release; it must stop before that")
			}
			p, _ := currentPackaging()
			if !strings.Contains(err.Error(), p.updateCommand("0.5.0")) {
				t.Errorf("the refusal should say how to update with the package manager: %v", err)
			}
		})
	}
}

// TestPackagedUpdateCommand: `update --check` still answers — a packaged agent
// is not blind to releases, it just does not act on them — and `update`
// refuses with the package manager's command, naming the exact file for the
// packages that are release assets rather than a repository.
func TestPackagedUpdateCommand(t *testing.T) {
	var downloads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases") {
			w.Write([]byte(`[{"tag_name":"agent-v0.5.0","html_url":"https://example.com/agent-v0.5.0","assets":[
				{"name":"gwatch-agent-` + runtime.GOOS + `-` + runtime.GOARCH + `","size":7,"browser_download_url":"` + serverURL(r) + `/dl/agent"}]}]`))
			return
		}
		downloads.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	restore, oldVersion := updateClient, version
	updateClient = func() *update.Client {
		return (&update.Client{APIBase: srv.URL, HTTP: srv.Client()}).ForAgent()
	}
	version = "0.4.0"
	defer func() { updateClient, version = restore, oldVersion }()

	asPackaged(t, "deb")
	cfg := config{repo: "acme/gwatch"}
	if err := runUpdate(cfg, true); err != nil {
		t.Fatalf("update --check on a packaged build = %v; checking must still work", err)
	}
	err := runUpdate(cfg, false)
	if !errors.Is(err, errPackaged) {
		t.Fatalf("update on a packaged build = %v, want a refusal", err)
	}
	wantFile := "gwatch-agent_0.5.0-1_" + debArch(runtime.GOARCH) + ".deb"
	if !strings.Contains(err.Error(), "sudo apt install ./"+wantFile) {
		t.Errorf("the refusal should name the package to install (%s): %v", wantFile, err)
	}
	if !strings.Contains(err.Error(), "https://example.com/agent-v0.5.0") {
		t.Errorf("the refusal should say where the package is: %v", err)
	}
	if n := downloads.Load(); n != 0 {
		t.Errorf("a packaged build made %d download request(s)", n)
	}
}

// TestPackagedBuildHasNoRollback: rollback swaps executables too, and a
// packaged agent never kept a .old to swap back.
func TestPackagedBuildHasNoRollback(t *testing.T) {
	for _, by := range everyPackage {
		asPackaged(t, by)
		if err := runRollback(); !errors.Is(err, errPackaged) {
			t.Errorf("rollback on a %s build = %v, want a refusal", by, err)
		}
	}
}

// TestReleaseBuildIsNotPackaged: the self-updating release is the default,
// and nothing about packaging leaks into it.
func TestReleaseBuildIsNotPackaged(t *testing.T) {
	asPackaged(t, "")
	if isPackaged() {
		t.Fatal("an empty packagedBy must be the self-updating release build")
	}
	if strings.Contains(versionLine(), "package") {
		t.Errorf("version line %q mentions a package on a release build", versionLine())
	}
	if userAgent() != "gwatch-agent/"+version {
		t.Errorf("user agent %q on a release build", userAgent())
	}
	if packagedUsage() != "" {
		t.Error("a release build's usage text must not describe a package")
	}
}

// TestPackagedVersionLine: `version` says what installed it, and still starts
// with the version, which is what the update path's proof-it-runs check reads.
func TestPackagedVersionLine(t *testing.T) {
	asPackaged(t, "rpm")
	line := versionLine()
	if !strings.HasPrefix(line, "gwatch-agent "+version+" (") || !strings.Contains(line, "rpm package") {
		t.Errorf("version line = %q", line)
	}
	if !strings.Contains(userAgent(), "(rpm)") {
		t.Errorf("user agent = %q, want it to name the package", userAgent())
	}
}

// TestPackagedAssetsAreInvisibleToTheUpdater is the release-family guarantee
// again, for the new names packaging adds to an agent release. The updater
// matches gwatch-agent-<os>-<arch>[.exe] exactly; none of the packages may
// ever satisfy that, or a release build would install a .deb over itself.
func TestPackagedAssetsAreInvisibleToTheUpdater(t *testing.T) {
	packaged := []string{
		"gwatch-agent_0.5.0-1_amd64.deb", "gwatch-agent_0.5.0-1_arm64.deb", "gwatch-agent_0.5.0-1_armhf.deb",
		"gwatch-agent-0.5.0-1.x86_64.rpm", "gwatch-agent-0.5.0-1.aarch64.rpm", "gwatch-agent-0.5.0-1.armv7hl.rpm",
		"gwatch-agent-homebrew-0.5.0-darwin-arm64.tar.gz", "gwatch-agent-homebrew-0.5.0-darwin-amd64.tar.gz",
		"gwatch-agent-homebrew-0.5.0-linux-amd64.tar.gz", "gwatch-agent-homebrew-0.5.0-linux-arm64.tar.gz",
		"gwatch-agent-winget-setup-0.5.0.exe", "gwatch-agent-winget-manifests-0.5.0.zip",
		"gwatch-agent-packages-0.5.0.sha256",
	}
	release := func(names []string) string {
		var assets []string
		for _, n := range names {
			assets = append(assets, `{"name":"`+n+`","size":9,"browser_download_url":"https://example.com/dl/`+n+`"}`)
		}
		return `[{"tag_name":"agent-v0.5.0","html_url":"https://example.com/agent","assets":[` + strings.Join(assets, ",") + `]}]`
	}
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()
	restore := updateClient
	updateClient = func() *update.Client {
		return (&update.Client{APIBase: srv.URL, HTTP: srv.Client()}).ForAgent()
	}
	defer func() { updateClient = restore }()
	asPackaged(t, "")

	// Packages alone: there is a newer release, and nothing in it to install.
	body = release(packaged)
	info, err := checkUpdate(context.Background(), config{repo: "acme/gwatch"})
	if err != nil {
		t.Fatal(err)
	}
	if info.AssetURL != "" {
		t.Fatalf("the updater picked %q from a release holding only packages", info.AssetName)
	}

	// Packages beside the real build: the real build, and only it.
	want := "gwatch-agent-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		want += ".exe"
	}
	body = release(append(append([]string{}, packaged...), want))
	info, err = checkUpdate(context.Background(), config{repo: "acme/gwatch"})
	if err != nil {
		t.Fatal(err)
	}
	if info.AssetName != want {
		t.Fatalf("the updater picked %q, want %q", info.AssetName, want)
	}
}

// TestPackagedDataDir: the Linux packages and the container keep the token in
// the one place their service reads it from; Homebrew and winget keep the
// release build's default, because their service runs as the person (or the
// Windows service account) the release build's default already suits.
func TestPackagedDataDir(t *testing.T) {
	t.Setenv("GWATCH_AGENT_TOKEN_FILE", "")
	for by, want := range map[string]string{"deb": linuxPackageDataDir, "rpm": linuxPackageDataDir, "docker": linuxPackageDataDir} {
		asPackaged(t, by)
		if got := agentDataDir(); got != want {
			t.Errorf("%s: data dir = %s, want %s", by, got, want)
		}
		if got := tokenFile(); got != filepath.Join(want, "agent-token") {
			t.Errorf("%s: token file = %s", by, got)
		}
	}
	asPackaged(t, "")
	release := agentDataDir()
	for _, by := range []string{"homebrew", "winget"} {
		asPackaged(t, by)
		if got := agentDataDir(); got != release {
			t.Errorf("%s: data dir = %s, want the release build's %s", by, got, release)
		}
	}
}

// TestPackagedServiceCommands: a package that registered the service itself
// must not have a second one registered beside it, and the refusal has to
// name the command that does the job instead.
func TestPackagedServiceCommands(t *testing.T) {
	for by, want := range map[string]string{
		"deb":      "sudo systemctl stop gwatch-agent",
		"rpm":      "sudo systemctl stop gwatch-agent",
		"homebrew": "brew services stop gwatch-agent",
		"docker":   "docker",
	} {
		asPackaged(t, by)
		p, _ := currentPackaging()
		if !p.ownsService {
			t.Errorf("%s registers its own service", by)
		}
		if err := p.refuseServiceCommand("stop"); !errors.Is(err, errPackaged) || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: stop = %v, want it to point at %q", by, err, want)
		}
	}
	asPackaged(t, "deb")
	p, _ := currentPackaging()
	if err := p.refuseServiceCommand("install"); !strings.Contains(err.Error(), "gwatch-agent pair") {
		t.Errorf("install on a .deb should point at pairing: %v", err)
	}
	// winget's setup program registers the service with `install --code`,
	// the same as the ordinary setup program, so it must keep the command.
	asPackaged(t, "winget")
	if p, _ := currentPackaging(); p.ownsService {
		t.Error("the winget package installs the service through the agent; it must keep install")
	}
}

// TestPairOnAPackageStartsItsService is the whole Linux package setup: one
// `sudo gwatch-agent pair`, after which the token and the server are where
// the unit reads them and the unit has been enabled and started.
func TestPairOnAPackageStartsItsService(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/agents/pair" {
			if ua := r.Header.Get("User-Agent"); !strings.Contains(ua, "(deb)") {
				t.Errorf("pairing User-Agent = %q, want it to say the agent came from a .deb", ua)
			}
			w.Write([]byte(`{"token":"gwa_pkg","agent":{"name":"nas"}}`))
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	asPackaged(t, "deb")
	dataDir := filepath.Join(t.TempDir(), "var-lib-gwatch-agent")
	oldDir, oldCtl, oldUp := linuxPackageDataDir, systemctl, systemdRunning
	var calls []string
	linuxPackageDataDir = dataDir
	systemctl = func(args ...string) error { calls = append(calls, strings.Join(args, " ")); return nil }
	systemdRunning = func() bool { return true }
	t.Cleanup(func() { linuxPackageDataDir, systemctl, systemdRunning = oldDir, oldCtl, oldUp })
	t.Setenv("GWATCH_AGENT_TOKEN_FILE", "")

	cfg := config{server: srv.URL, code: "ABCD-EFGH", insecure: true, name: "nas", interval: time.Minute}
	if err := pair(&cfg); err != nil {
		t.Fatalf("pair: %v", err)
	}
	if strings.Join(calls, "; ") != "enable gwatch-agent.service; restart gwatch-agent.service" {
		t.Errorf("systemctl calls = %q; pairing a package should enable and (re)start its unit", calls)
	}
	if tok, err := readStoredToken(); err != nil || tok != "gwa_pkg" {
		t.Fatalf("stored token = %q, %v", tok, err)
	}

	// What the unit's `gwatch-agent run`, given no arguments, will end up with.
	s, err := readStoredSettings()
	if err != nil {
		t.Fatalf("stored settings: %v", err)
	}
	var svc config
	svc.applyStored(s, false)
	if svc.server != srv.URL || !svc.insecure || svc.name != "nas" {
		t.Errorf("the service would run with server=%q insecure=%v name=%q", svc.server, svc.insecure, svc.name)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(settingsFile())
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Errorf("settings file mode = %04o, want 0600 like the token beside it", mode)
		}
	}
}

// TestPairOnAPackageRefusesBeforeSpendingTheCode: on a package the token has
// exactly one home. Pairing without permission to write there (no sudo) must
// stop before the code is used up, not after.
func TestPairOnAPackageRefusesBeforeSpendingTheCode(t *testing.T) {
	var asked atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Store(true)
		w.Write([]byte(`{"token":"gwa_pkg"}`))
	}))
	defer srv.Close()

	asPackaged(t, "rpm")
	blocked := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := linuxPackageDataDir
	linuxPackageDataDir = filepath.Join(blocked, "gwatch-agent")
	t.Cleanup(func() { linuxPackageDataDir = old })
	t.Setenv("GWATCH_AGENT_TOKEN_FILE", "")

	cfg := config{server: srv.URL, code: "ABCD-EFGH", interval: time.Minute}
	err := pair(&cfg)
	if err == nil || !strings.Contains(err.Error(), "sudo") {
		t.Fatalf("pair without a writable data dir = %v, want advice to use sudo", err)
	}
	if asked.Load() {
		t.Error("the code was sent to GWatch before finding out the token could not be kept")
	}
}

// TestStoredSettingsFillOnlyWhatWasNotSaid: what pairing stored is a default,
// never an override.
func TestStoredSettingsFillOnlyWhatWasNotSaid(t *testing.T) {
	s := storedSettings{Server: "https://gwatch.lan:7230", Insecure: true, Name: "stored"}

	c := config{name: "given"}
	c.applyStored(s, true) // --insecure=false given explicitly
	if c.insecure {
		t.Error("an explicit --insecure=false was overridden by the stored setting")
	}
	if c.name != "given" {
		t.Errorf("name = %q; a given --name must win", c.name)
	}
	if c.server != s.Server {
		t.Errorf("server = %q", c.server)
	}

	// A settings file with no server is no settings file.
	dir := t.TempDir()
	t.Setenv("GWATCH_AGENT_TOKEN_FILE", filepath.Join(dir, "agent-token"))
	b, _ := json.Marshal(storedSettings{Name: "x"})
	os.WriteFile(filepath.Join(dir, "agent-settings.json"), b, 0o600)
	if _, err := readStoredSettings(); err == nil {
		t.Error("stored settings without a server were accepted")
	}
}

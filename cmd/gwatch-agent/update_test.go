package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/update"
)

// Self-update is the one feature here that cannot be fixed from a distance. If
// an agent installs something that does not work, the machine it was watching
// goes quiet and the only repair is to walk to it — on every machine at once,
// since they all take the same release. So the tests below are mostly about
// refusing: what must never be installed, and what must be put back when the
// install turns out badly.

// fakeAgent writes a stand-in executable that behaves the way the real agent
// does for the two questions asked of a new binary: what version are you, and
// can you send a reading.
func fakeAgent(t *testing.T, dir, name, reportedVersion string, onceExit int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in executable is a shell script; the swap itself is exercised on the other platforms")
	}
	path := filepath.Join(dir, name)
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
  version) echo "gwatch-agent %s (test)" ;;
  once)    exit %d ;;
  *)       exit 2 ;;
esac
`, reportedVersion, onceExit)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestInstallDownloadedSwapsAndKeepsTheOldOne is the good path: the new binary
// proves it runs and can report, replaces the old one, and the old one stays
// on disk as the way back.
func TestInstallDownloadedSwapsAndKeepsTheOldOne(t *testing.T) {
	dir := t.TempDir()
	exe := fakeAgent(t, dir, "gwatch-agent", "0.4.0", 0)
	newBin := fakeAgent(t, dir, "downloaded", "0.5.0", 0)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	cfg := config{server: srv.URL, token: "gwa_x", interval: time.Minute}
	info := model.UpdateInfo{LatestVersion: "0.5.0", AssetURL: "https://example.invalid/asset"}
	if err := installDownloaded(context.Background(), cfg, exe, newBin, info); err != nil {
		t.Fatalf("install: %v", err)
	}

	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "0.5.0") {
		t.Error("the new version is not in place")
	}
	old, err := os.ReadFile(exe + ".old")
	if err != nil {
		t.Fatalf("the replaced version must be kept for rollback: %v", err)
	}
	if !strings.Contains(string(old), "0.4.0") {
		t.Error(".old is not the version that was replaced")
	}
}

// TestInstallRefusesABinaryThatIsNotWhatTheReleaseSays catches the download
// that is the wrong build — a different architecture, a truncated file, or an
// asset that is simply not the version the release claims. Nothing may be
// swapped in that case.
func TestInstallRefusesABinaryThatIsNotWhatTheReleaseSays(t *testing.T) {
	dir := t.TempDir()
	exe := fakeAgent(t, dir, "gwatch-agent", "0.4.0", 0)

	for _, tc := range []struct {
		name    string
		newBin  func() string
		wantErr string
	}{
		{
			name:    "reports a different version",
			newBin:  func() string { return fakeAgent(t, dir, "wrong-version", "0.1.0", 0) },
			wantErr: "did not pass its checks",
		},
		{
			name: "will not run at all",
			newBin: func() string {
				p := filepath.Join(dir, "not-a-program")
				os.WriteFile(p, []byte("this is not an executable"), 0o755)
				return p
			},
			wantErr: "did not pass its checks",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := model.UpdateInfo{LatestVersion: "0.5.0"}
			err := installDownloaded(context.Background(), config{}, exe, tc.newBin(), info)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want something containing %q", err, tc.wantErr)
			}
			if !strings.Contains(err.Error(), "nothing was changed") {
				t.Errorf("a refusal must say that nothing was changed: %v", err)
			}
			// And it must be true.
			current, _ := os.ReadFile(exe)
			if !strings.Contains(string(current), "0.4.0") {
				t.Error("the running version was replaced by a binary that failed its checks")
			}
			if _, err := os.Stat(exe + ".old"); err == nil {
				t.Error("nothing should have been moved aside")
			}
		})
	}
}

// TestInstallRefusesABinaryThatCannotReport is the subtler failure: the
// download runs and says the right version, but cannot actually do the job on
// this machine — a build that is missing what it needs, or one this server
// will not accept. Finding that out before the swap is the whole point of
// taking a real reading first.
func TestInstallRefusesABinaryThatCannotReport(t *testing.T) {
	dir := t.TempDir()
	exe := fakeAgent(t, dir, "gwatch-agent", "0.4.0", 0)
	newBin := fakeAgent(t, dir, "cannot-report", "0.5.0", 1)

	cfg := config{server: "https://gwatch.invalid", token: "gwa_x", interval: time.Minute}
	info := model.UpdateInfo{LatestVersion: "0.5.0"}
	err := installDownloaded(context.Background(), cfg, exe, newBin, info)
	if err == nil || !strings.Contains(err.Error(), "could not send a reading") {
		t.Fatalf("error = %v, want a refusal naming the failed reading", err)
	}
	current, _ := os.ReadFile(exe)
	if !strings.Contains(string(current), "0.4.0") {
		t.Fatal("an agent that could not report replaced the one that could")
	}
}

// TestInstallRollsBackWhenTheSwappedBinaryWillNotRun is the last net: the new
// binary passed everything, was moved into place, and is broken there anyway
// (a path it cannot run from, permissions it did not inherit). The previous
// version has to come back by itself, because by this point there is nothing
// on the machine that still works to do it.
func TestInstallRollsBackWhenTheSwappedBinaryWillNotRun(t *testing.T) {
	dir := t.TempDir()
	exe := fakeAgent(t, dir, "gwatch-agent", "0.4.0", 0)

	// A binary that works where it is downloaded and not where it lands: it
	// answers `version` only when invoked under its download name.
	newBin := filepath.Join(dir, "downloaded")
	script := `#!/bin/sh
case "$(basename "$0")" in
  downloaded) [ "$1" = version ] && echo "gwatch-agent 0.5.0" && exit 0 ;;
esac
exit 1
`
	if err := os.WriteFile(newBin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	info := model.UpdateInfo{LatestVersion: "0.5.0", ReleaseURL: "https://example.invalid/rel"}
	err := installDownloaded(context.Background(), config{}, exe, newBin, info)
	if err == nil || !strings.Contains(err.Error(), "previous version was put back") {
		t.Fatalf("error = %v, want a rollback", err)
	}
	current, err := os.ReadFile(exe)
	if err != nil {
		t.Fatalf("after a rollback there must be an agent at %s: %v", exe, err)
	}
	if !strings.Contains(string(current), "0.4.0") {
		t.Fatal("the rollback did not put the working version back")
	}
}

// TestRollbackCommand covers the repair someone runs by hand after an update
// that installed cleanly but behaved badly.
func TestRollbackCommand(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "gwatch-agent")
	if err := os.WriteFile(exe, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := rollback(exe); err == nil || !strings.Contains(err.Error(), "no previous version") {
		t.Fatalf("rollback with nothing to roll back to = %v, want a clear refusal", err)
	}
	if err := os.WriteFile(exe+".old", []byte("previous"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := rollback(exe); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "previous" {
		t.Errorf("after rollback the executable is %q, want the previous one", got)
	}
}

// TestCheckUpdateOnlySeesAgentReleases is the same guarantee as
// update.TestReleaseFamiliesStayApart, asserted from the agent's side: the
// repository holds GWatch's releases too, and an agent must never be offered
// one. Installing a GWatch server over an agent would take out the machine and
// the monitoring of it together.
func TestCheckUpdateOnlySeesAgentReleases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[
			{"tag_name":"v9.9.9","html_url":"https://example.com/server","assets":[
				{"name":"gwatch-` + runtime.GOOS + `-` + runtime.GOARCH + `","size":9,"browser_download_url":"https://example.com/dl/server"}]},
			{"tag_name":"agent-v0.5.0","html_url":"https://example.com/agent","assets":[
				{"name":"gwatch-agent-` + runtime.GOOS + `-` + runtime.GOARCH + `","size":9,"browser_download_url":"https://example.com/dl/agent"}]}]`))
	}))
	defer srv.Close()

	restore := updateClient
	updateClient = func() *update.Client {
		c := (&update.Client{APIBase: srv.URL, HTTP: srv.Client()}).ForAgent()
		return c
	}
	defer func() { updateClient = restore }()

	info, err := checkUpdate(context.Background(), config{repo: "acme/gwatch"})
	if err != nil {
		t.Fatal(err)
	}
	if info.LatestVersion != "0.5.0" {
		t.Fatalf("the agent was offered %q; the 9.9.9 release is GWatch's, not the agent's", info.LatestVersion)
	}
	if !strings.HasPrefix(info.AssetName, update.AgentAssetPrefix) {
		t.Fatalf("the agent was offered asset %q", info.AssetName)
	}
}

// TestUpdateRefusesAnUnsignedRelease is the security property the whole
// feature rests on: the agent installs code, unattended, on every machine it
// runs on. An asset that is not signed by a release key pinned into this
// binary must be refused, whatever the server that offered it says.
func TestUpdateRefusesAnUnsignedRelease(t *testing.T) {
	var served bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases"):
			w.Write([]byte(`[{"tag_name":"agent-v0.5.0","html_url":"https://example.com/a","assets":[
				{"name":"gwatch-agent-` + runtime.GOOS + `-` + runtime.GOARCH + `","size":7,"browser_download_url":"` + serverURL(r) + `/dl/agent"}]}]`))
		case strings.HasSuffix(r.URL.Path, "/dl/agent"):
			served = true
			w.Write([]byte("PAYLOAD"))
		default:
			// No .sig asset is published, which is the case under test.
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	restore := updateClient
	updateClient = func() *update.Client {
		return (&update.Client{APIBase: srv.URL, HTTP: srv.Client()}).ForAgent()
	}
	defer func() { updateClient = restore }()

	dir := t.TempDir()
	exe := filepath.Join(dir, "gwatch-agent")
	if err := os.WriteFile(exe, []byte("current"), 0o755); err != nil {
		t.Fatal(err)
	}

	info, err := checkUpdate(context.Background(), config{repo: "acme/gwatch"})
	if err != nil {
		t.Fatal(err)
	}
	if !info.UpdateAvailable {
		t.Fatal("the fake release should be newer than this build")
	}

	// Downloading it must fail on verification rather than land a binary.
	_, err = updateClient().Download(context.Background(), info, dir)
	if err == nil {
		t.Fatal("an unsigned release was accepted")
	}
	if !served {
		t.Log("the asset was refused before it was fetched, which is also fine")
	}
	if !strings.Contains(err.Error(), "signed") && !strings.Contains(err.Error(), "verified") {
		t.Errorf("refusal %q does not say the problem is the signature", err)
	}
	// Nothing may be left behind in the directory the agent runs from.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "gwatch-agent" {
			t.Errorf("a refused update left %s behind", e.Name())
		}
	}
}

// serverURL reconstructs the test server's own base URL from a request, so the
// release JSON can point the download at it.
func serverURL(r *http.Request) string {
	return "http://" + r.Host
}

// TestApplyUpdateRefusesAReleaseWithNoBuildForThisPlatform: the agent runs on
// more platforms than any one release necessarily carries — linux/arm is the
// Raspberry Pi class of machine people put agents on — and an old release, or
// one that dropped a platform, must be refused in words rather than by
// downloading whatever else is there.
func TestApplyUpdateRefusesAReleaseWithNoBuildForThisPlatform(t *testing.T) {
	err := applyUpdate(context.Background(), config{}, model.UpdateInfo{LatestVersion: "0.5.0"})
	if err == nil || !strings.Contains(err.Error(), "no agent build") {
		t.Fatalf("a release with no build for this platform = %v, want a clear refusal", err)
	}
	if !strings.Contains(err.Error(), runtime.GOOS) {
		t.Errorf("the refusal should name the platform it looked for: %v", err)
	}
}

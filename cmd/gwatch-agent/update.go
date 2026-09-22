// Self-update.
//
// A machine running the agent is, almost by definition, a machine nobody logs
// into. If updating meant visiting it, the agent installed today would be the
// agent running in three years, so the agent keeps itself current: it reads
// the agent releases (agent-v*, its own tags — see docs/RELEASING.md), verifies
// the download against the signing keys pinned into this binary, and replaces
// itself.
//
// GWatch is not involved. It is never asked what version to run, never hands
// over a URL and cannot make an agent install anything — a compromised GWatch
// must not be a way to run code on every machine that reports to it, which is
// the whole reason the agent only ever talks outwards. What GWatch does with
// the version each agent reports is show you which machines are behind.
//
// Nothing is installed that was not signed by a release key this build trusts.
// The check is the same one GWatch's own updater makes, and it is not
// skippable: an unsigned or wrongly signed asset is an error, not a prompt.
//
// Before anything is swapped, the downloaded binary is made to prove itself
// twice on this machine — it must run and report its version, and it must take
// a real reading and get it accepted by the server. Only then does it replace
// the running executable, and the executable it replaces is kept alongside as
// .old so `gwatch-agent rollback` can put it back.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/update"
)

const (
	// defaultRepo publishes the agent releases. It is overridable so that a
	// fork which publishes its own signed builds can point its agents at them.
	defaultRepo = "jxburros/GWatch"

	// updateCheckInterval is how often a running agent looks for a new
	// release. Agent releases are rare and never urgent — the machine is being
	// watched either way — so this is deliberately unhurried.
	updateCheckInterval = 6 * time.Hour

	// updateFirstCheck is how long after starting the first check happens. A
	// machine that has just booted has better things to do, and an agent that
	// checked at startup would turn a power cut into a stampede as every
	// machine on the site came back at once.
	updateFirstCheck = 10 * time.Minute

	// updateJitter is spread over the check interval so that agents installed
	// by the same script on the same afternoon do not all ask GitHub, and then
	// all restart, in the same second.
	updateJitter = 30 * time.Minute

	// exitUpdated is how the agent asks to be restarted into the version it
	// has just installed. It is not zero because a Windows service that exits
	// cleanly is a service that has finished, and the service manager leaves
	// it stopped; a failure exit is restarted, which here is what is wanted.
	// systemd (Restart=always) and launchd restart it either way.
	exitUpdated = 70

	// verifyTimeout bounds each of the two proofs the new binary must give
	// before it is allowed to replace the running one.
	verifyTimeout = 2 * time.Minute
)

// updateClient is the agent's view of the releases: its own tags, its own
// assets, so that it can never be handed a GWatch server build.
//
// It is a variable so that tests can point it at a stand-in for GitHub. What
// it may install is not loosened by that: every path runs through
// update.Download, which verifies the signature against the keys pinned into
// this binary and has no way to be told not to.
var updateClient = func() *update.Client {
	return (&update.Client{}).ForAgent()
}

func (c config) repoOrDefault() string {
	if r := strings.TrimSpace(c.repo); r != "" {
		return r
	}
	return defaultRepo
}

// checkUpdate reports the newest agent release this build could move to.
func checkUpdate(ctx context.Context, cfg config) (model.UpdateInfo, error) {
	repo := cfg.repoOrDefault()
	if !update.ValidRepo(repo) {
		return model.UpdateInfo{}, fmt.Errorf("invalid repository %q (expected owner/name)", repo)
	}
	return updateClient().Check(ctx, repo, version, false)
}

// runUpdate is the `update` command: say what is available, and unless only
// asked to check, install it.
func runUpdate(cfg config, checkOnly bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	info, err := checkUpdate(ctx, cfg)
	if err != nil {
		return err
	}
	if !info.UpdateAvailable {
		fmt.Printf("gwatch-agent %s is the newest release.\n", version)
		return nil
	}
	fmt.Printf("gwatch-agent %s is available (this machine runs %s).\n", info.LatestVersion, version)
	if checkOnly {
		fmt.Printf("Run `gwatch-agent update` to install it, or see %s\n", info.ReleaseURL)
		return nil
	}
	if err := applyUpdate(ctx, cfg, info); err != nil {
		return err
	}
	fmt.Printf("Installed gwatch-agent %s. The previous version is kept alongside it; `gwatch-agent rollback` puts it back.\n", info.LatestVersion)
	if svc, err := agentService(cfg, "run"); err == nil {
		if _, serr := svc.Status(); serr == nil {
			fmt.Println("Restart the service to run it: gwatch-agent restart")
		}
	}
	return nil
}

// applyUpdate downloads, verifies and installs a release.
//
// The order matters more than anything else in this file. Everything that can
// be found out about the new binary is found out *before* the running one is
// touched, because the machine this happens on is usually one nobody can walk
// over to.
func applyUpdate(ctx context.Context, cfg config, info model.UpdateInfo) error {
	if info.AssetURL == "" {
		return fmt.Errorf("release %s has no agent build for %s (%w)", info.LatestVersion, platform(), update.ErrNoAsset)
	}
	exe, err := update.Executable()
	if err != nil {
		return fmt.Errorf("find the running executable: %w", err)
	}
	if !update.DirWritable(exe) {
		return fmt.Errorf("cannot write to %s, so the agent cannot replace itself there; run this as an administrator (Windows) or with sudo", filepath.Dir(exe))
	}

	downloaded, err := updateClient().Download(ctx, info, filepath.Dir(exe))
	if err != nil {
		return err
	}
	defer os.Remove(downloaded) // a no-op once it has been moved into place
	return installDownloaded(ctx, cfg, exe, downloaded, info)
}

// installDownloaded is everything that happens to a verified download: the two
// proofs, the swap, and the way back if the swap turns out badly.
func installDownloaded(ctx context.Context, cfg config, exe, downloaded string, info model.UpdateInfo) error {
	// Proof one: it runs here, and it is what it claims to be. A build for the
	// wrong architecture, or a truncated one, fails at this line rather than
	// after it has replaced a working agent.
	if err := verifyRuns(ctx, downloaded, info.LatestVersion); err != nil {
		return fmt.Errorf("the downloaded agent did not pass its checks, so nothing was changed: %w", err)
	}

	// Proof two: it can do the job. It takes a real reading on this machine
	// and gets it accepted by this server with this token, which is every part
	// of the agent that matters, exercised before the swap.
	if cfg.token != "" && cfg.server != "" {
		if err := verifyReports(ctx, downloaded, cfg); err != nil {
			return fmt.Errorf("the downloaded agent could not send a reading, so nothing was changed: %w", err)
		}
	}

	if err := update.Swap(exe, downloaded); err != nil {
		return err
	}

	// And once more in its final place, where the path, and on Windows the
	// permissions it inherits, are not the ones it was verified under.
	if err := verifyRuns(ctx, exe, info.LatestVersion); err != nil {
		if rbErr := rollback(exe); rbErr != nil {
			return fmt.Errorf("the installed agent does not run (%w) and could not be rolled back (%v); reinstall it from %s", err, rbErr, info.ReleaseURL)
		}
		return fmt.Errorf("the installed agent does not run, so the previous version was put back: %w", err)
	}
	return nil
}

// verifyRuns checks that a binary executes on this machine and reports the
// version it is supposed to be.
func verifyRuns(ctx context.Context, path, want string) error {
	ctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("it would not run: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if want != "" && !strings.Contains(string(out), want) {
		return fmt.Errorf("it reports %q, but the release says %s", strings.TrimSpace(string(out)), want)
	}
	return nil
}

// verifyReports checks that a binary can take a reading on this machine and
// have it accepted by the server this agent reports to.
//
// The token goes over the environment rather than the command line: a process
// list is readable by everyone on the machine, and while an agent token can do
// only one thing, there is no reason to put it somewhere it can be read.
func verifyReports(ctx context.Context, path string, cfg config) error {
	ctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()
	args := []string{"once"}
	if cfg.insecure {
		args = append(args, "--insecure")
	}
	if n := strings.TrimSpace(cfg.name); n != "" {
		args = append(args, "--name", n)
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(),
		"GWATCH_SERVER="+cfg.server,
		"GWATCH_AGENT_TOKEN="+cfg.token,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// rollback puts the previous executable back. It is what .old is for, and it
// is the one repair that has to work without a network or a download.
func rollback(exe string) error {
	old := exe + ".old"
	if _, err := os.Stat(old); err != nil {
		return fmt.Errorf("there is no previous version kept at %s", old)
	}
	if err := os.Remove(exe); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove the current executable: %w", err)
	}
	if err := os.Rename(old, exe); err != nil {
		return fmt.Errorf("restore %s: %w", old, err)
	}
	return nil
}

// runRollback is the `rollback` command.
func runRollback() error {
	exe, err := update.Executable()
	if err != nil {
		return err
	}
	if err := rollback(exe); err != nil {
		return err
	}
	fmt.Printf("Put the previous version back at %s.\n", exe)
	out, err := exec.Command(exe, "version").CombinedOutput()
	if err == nil {
		fmt.Printf("Now running: %s", out)
	}
	fmt.Println("Restart the service to run it: gwatch-agent restart")
	return nil
}

// autoUpdate looks for a new release every so often and installs it, then asks
// to be restarted by returning. It returns only when an update has been
// installed or ctx is done; the reporting loop watches the same context, so
// readings carry on the whole time.
//
// A failed check is not worth reporting anywhere: the next one is the retry,
// and an agent that cannot reach GitHub is not a problem on the machine it is
// watching.
func autoUpdate(ctx context.Context, cfg config, updated func()) {
	timer := time.NewTimer(jittered(updateFirstCheck, updateJitter))
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if done := autoUpdateOnce(ctx, cfg); done {
			updated()
			return
		}
		timer.Reset(jittered(updateCheckInterval, updateJitter))
	}
}

// autoUpdateOnce performs one check-and-install, reporting whether an update
// was installed and a restart is now wanted.
func autoUpdateOnce(ctx context.Context, cfg config) bool {
	info, err := checkUpdate(ctx, cfg)
	if err != nil {
		log.Printf("update check failed (this changes nothing; the next check is in about %s): %v", updateCheckInterval, err)
		return false
	}
	if !info.UpdateAvailable {
		return false
	}
	log.Printf("gwatch-agent %s is available; installing it", info.LatestVersion)
	if err := applyUpdate(ctx, cfg, info); err != nil {
		if ctx.Err() != nil {
			return false
		}
		log.Printf("update to %s not installed: %v", info.LatestVersion, err)
		return false
	}
	log.Printf("installed gwatch-agent %s; restarting to run it", info.LatestVersion)
	return true
}

// jittered spreads a wait over a window, so that a fleet installed together
// does not act together. The wait is never shortened below half the base, so
// jitter cannot turn a six-hourly check into a frequent one.
func jittered(base, window time.Duration) time.Duration {
	if window <= 0 {
		return base
	}
	half := window / 2
	d := base - half + time.Duration(rand.Int63n(int64(window)+1))
	if min := base / 2; d < min {
		return min
	}
	return d
}

func platform() string { return runtime.GOOS + "/" + runtime.GOARCH }

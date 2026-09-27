// Packaged builds.
//
// The agent ships two ways. The release binaries (gwatch-agent-<os>-<arch>)
// and the Windows setup program update themselves, which is what a machine
// nobody logs into needs. The packages — .deb, .rpm, the Homebrew formula,
// the winget manifest and the container image — are installed by a package
// manager, and a package manager believes it owns the files it installed. An
// agent that replaced /usr/bin/gwatch-agent underneath apt would leave dpkg
// recording one binary while another runs, `debsums` reporting tampering, the
// next package upgrade silently reverting the agent's own update, and the
// `.old` the swap leaves behind as a file no package owns. The same is true of
// a Homebrew keg, of a winget install record, and of an image layer that the
// next `docker pull` replaces anyway.
//
// So a packaged build does not replace itself, and that is decided when the
// binary is built rather than by anything on the machine: CI builds the
// packaged binaries with -ldflags "-X main.packagedBy=deb" (or rpm, homebrew,
// winget, docker), and a binary built that way turns automatic updates off,
// refuses `update` and `rollback` with the package manager's own command, and
// never reaches the code that swaps an executable. A flag in a unit file would
// say the same thing, but it could be edited, forgotten in one of five package
// formats, or overridden by an environment variable; a build that cannot swap
// itself cannot be talked into it. See docs/AGENT-DECISIONS.md, entry 10.
//
// Nothing here loosens what an update is allowed to be. A packaged agent still
// reports that a newer release exists (`update --check` asks the same signed
// release feed as everyone else), and GWatch still cannot make any agent,
// packaged or not, install anything.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// packagedBy names the package manager a build was made for, set at build
// time with -ldflags "-X main.packagedBy=deb". It is empty for the release
// binaries, which keep themselves up to date. Any non-empty value turns
// self-replacement off, including one this file does not recognise: a typo in
// a packaging job must fail towards "the package manager owns this", never
// towards an agent that swaps a file a package manager thinks it owns.
var packagedBy = ""

// linuxPackageDataDir is where a Linux package keeps the token pairing
// obtains. The release build keeps it under the home directory of whoever
// paired the machine, which is fine when the same account then installs the
// service; a package's service is a fixed systemd unit running as root, so the
// token has to be somewhere that does not depend on how sudo was configured
// to treat $HOME. It is the unit's StateDirectory.
//
// It is a variable only so that tests can point it somewhere writable.
var linuxPackageDataDir = "/var/lib/gwatch-agent"

// packaging describes a packaged build.
type packaging struct {
	// by is the value packagedBy was built with, lower-cased.
	by string
	// source says how this copy was installed, in words: "a .deb package".
	source string
	// manager is who owns the binary, for messages: "apt (dpkg)".
	manager string
	// ownsService is true when the package registers and runs the service
	// itself, so the agent's own install/uninstall/start/stop would create a
	// second, competing registration.
	ownsService bool
	// dataDir, when set, replaces the default directory for the stored token
	// and settings.
	dataDir string
	// systemd is true for the Linux packages, whose unit pairing starts.
	systemd bool
}

// currentPackaging reports whether this is a packaged build, and which.
func currentPackaging() (packaging, bool) {
	by := strings.ToLower(strings.TrimSpace(packagedBy))
	switch by {
	case "":
		return packaging{}, false
	case "deb":
		return packaging{by: by, source: "a .deb package", manager: "apt (dpkg)", ownsService: true, dataDir: linuxPackageDataDir, systemd: true}, true
	case "rpm":
		return packaging{by: by, source: "an .rpm package", manager: "dnf (rpm)", ownsService: true, dataDir: linuxPackageDataDir, systemd: true}, true
	case "homebrew":
		return packaging{by: by, source: "Homebrew", manager: "Homebrew", ownsService: true}, true
	case "winget":
		// The winget package is the Windows setup program built around a
		// packaged binary, and that setup program registers the service by
		// running `gwatch-agent install --code`, exactly as the ordinary one
		// does — so the agent keeps its own service commands here.
		return packaging{by: by, source: "winget", manager: "winget"}, true
	case "docker":
		return packaging{by: by, source: "the container image", manager: "the container image", ownsService: true, dataDir: linuxPackageDataDir}, true
	}
	return packaging{by: by, source: fmt.Sprintf("a %q package", by), manager: "the package manager that installed it"}, true
}

// isPackaged is currentPackaging for callers that only need the yes or no.
func isPackaged() bool {
	_, ok := currentPackaging()
	return ok
}

// updateCommand is what someone types to move a packaged agent to latest
// (which may be empty when it is not known yet). It is a command wherever
// there is one, because "use your package manager" is not an answer to
// "how do I update this".
//
// The .deb and .rpm are release assets, not an apt or dnf repository, so for
// them the answer is to install the newer file; `apt upgrade` would not know
// there was anything to upgrade to.
func (p packaging) updateCommand(latest string) string {
	v := latest
	if v == "" {
		v = "<version>"
	}
	switch p.by {
	case "deb":
		f := fmt.Sprintf("gwatch-agent_%s-1_%s.deb", v, debArch(runtime.GOARCH))
		return fmt.Sprintf("download %s from the agent release and run: sudo apt install ./%s", f, f)
	case "rpm":
		f := fmt.Sprintf("gwatch-agent-%s-1.%s.rpm", v, rpmArch(runtime.GOARCH))
		return fmt.Sprintf("download %s from the agent release and run: sudo dnf install ./%s", f, f)
	case "homebrew":
		return "brew upgrade gwatch-agent"
	case "winget":
		return "winget upgrade GWatch.Agent"
	case "docker":
		return fmt.Sprintf("docker pull ghcr.io/jxburros/gwatch-agent:%s, then recreate the container from it", v)
	}
	return "update it the same way it was installed"
}

// serviceCommand is the package's own way of doing what the agent's service
// commands do, for a package that registered the service itself.
func (p packaging) serviceCommand(cmd string) string {
	switch p.by {
	case "deb", "rpm":
		switch cmd {
		case "install":
			return "the package already installed the gwatch-agent systemd unit; pair this machine with `sudo gwatch-agent pair --server URL --code XXXX-XXXX` and it starts reporting"
		case "uninstall":
			return "remove the package instead: " + map[string]string{"deb": "sudo apt remove gwatch-agent", "rpm": "sudo dnf remove gwatch-agent"}[p.by]
		}
		return fmt.Sprintf("sudo systemctl %s gwatch-agent", cmd)
	case "homebrew":
		switch cmd {
		case "install":
			return "pair this machine with `gwatch-agent pair --server URL --code XXXX-XXXX`, then run it in the background with `brew services start gwatch-agent`"
		case "uninstall":
			return "brew services stop gwatch-agent, then brew uninstall gwatch-agent"
		case "status":
			return "brew services info gwatch-agent"
		}
		return fmt.Sprintf("brew services %s gwatch-agent", cmd)
	case "docker":
		return "the container is the service: start, stop and remove it with docker (or docker compose); see docs/HARDWARE.md#in-a-container"
	}
	return "use the service manager that installed it"
}

// errPackaged marks a refusal that exists because a package manager owns
// this copy of the agent.
var errPackaged = errors.New("installed by a package manager")

// refuseUpdate is what `update` and every other path to a swap say on a
// packaged build.
func (p packaging) refuseUpdate(latest, releaseURL string) error {
	msg := fmt.Sprintf("this agent was installed from %s, so %s owns the binary and the agent will not replace it; to update, %s",
		p.source, p.manager, p.updateCommand(latest))
	if releaseURL != "" && (p.by == "deb" || p.by == "rpm") {
		msg += " (" + releaseURL + ")"
	}
	return fmt.Errorf("%w: %s", errPackaged, msg)
}

// refuseRollback is rollback's version: there is no .old to put back, because
// a packaged agent never made one, and going back a version is the package
// manager's job for the same reason going forward is.
func (p packaging) refuseRollback() error {
	how := "install the earlier version the same way this one was installed"
	switch p.by {
	case "deb":
		how = fmt.Sprintf("install the earlier package: sudo apt install ./gwatch-agent_<version>-1_%s.deb", debArch(runtime.GOARCH))
	case "rpm":
		how = "sudo dnf downgrade gwatch-agent, or install the earlier .rpm from the agent releases"
	case "homebrew":
		how = "Homebrew keeps only the newest formula, so brew uninstall gwatch-agent and install the earlier agent release by hand (docs/HARDWARE.md)"
	case "winget":
		how = "winget install GWatch.Agent --version <version> --force"
	case "docker":
		how = "recreate the container from the earlier image, ghcr.io/jxburros/gwatch-agent:<version>"
	}
	return fmt.Errorf("%w: this agent was installed from %s, which keeps no previous version beside it; to go back, %s",
		errPackaged, p.source, how)
}

// refuseServiceCommand explains why a service command is not the agent's to
// run on a package that registered the service itself.
func (p packaging) refuseServiceCommand(cmd string) error {
	return fmt.Errorf("%w: this agent was installed from %s, which manages its service; %s",
		errPackaged, p.source, p.serviceCommand(cmd))
}

// describe is the suffix `version` prints, so a packaged binary says so when
// asked and a support conversation starts from the right place.
func (p packaging) describe() string {
	return p.by + " package"
}

// debArch and rpmArch map Go's architecture names to the ones the package
// file names carry (nfpm's own mapping; see packaging/nfpm/nfpm.yaml).
func debArch(goarch string) string {
	if goarch == "arm" {
		return "armhf"
	}
	return goarch
}

func rpmArch(goarch string) string {
	switch goarch {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	case "arm":
		return "armv7hl"
	}
	return goarch
}

// checkDataDirWritable is asked before a packaged build spends a pairing code.
// On a package the token has exactly one place the service will look for it,
// so a token that cannot be written there is a machine that will never report
// — better to say "run it with sudo" before the code is used up than after.
// The release build does not ask: its token can live wherever the person who
// paired it can write, and pair() already hands the token over on screen when
// it cannot be saved.
func checkDataDirWritable(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("cannot create %s to keep this machine's token in (%v); run it with sudo", dir, err)
	}
	f, err := os.CreateTemp(dir, ".write-test-*")
	if err != nil {
		return fmt.Errorf("cannot write to %s, where the service looks for this machine's token (%v); run it with sudo", dir, err)
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return nil
}

// systemctl runs systemctl. It is a variable so that tests of pairing on a
// packaged build do not reach for the real service manager.
var systemctl = func(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// systemdRunning is systemd's own test for "booted with systemd" (what
// sd_booted(3) checks). A package installed into a container or a chroot has
// a unit file and nothing to start it with.
var systemdRunning = func() bool {
	_, err := os.Stat("/run/systemd/system")
	return err == nil
}

// afterPair is what a packaged build does once this machine holds a token:
// start the service the package installed, or say how to.
//
// For the Linux packages it enables and (re)starts the systemd unit, so that
// `sudo gwatch-agent pair --server … --code …` is the whole setup — the same
// one command `gwatch-agent install --code` is for the release build.
// Restart rather than start, so that pairing a machine again moves a running
// agent onto the new token. The unit is only touched when this token is the
// one it will read: a token saved somewhere else by GWATCH_AGENT_TOKEN_FILE
// is not the unit's business.
func (p packaging) afterPair(tokenPath string) {
	switch {
	case p.systemd:
		if filepath.Dir(tokenPath) != p.dataDir {
			fmt.Printf("The token was saved to %s rather than %s, where the gwatch-agent service looks for it; start the service with GWATCH_AGENT_TOKEN_FILE set to match, or pair again without it.\n", tokenPath, p.dataDir)
			return
		}
		if !systemdRunning() {
			fmt.Println("systemd is not running here, so the gwatch-agent service was not started; run `gwatch-agent run` under whatever supervises services on this machine.")
			return
		}
		if err := systemctl("enable", "gwatch-agent.service"); err != nil {
			fmt.Fprintf(os.Stderr, "warning: %v\n", err)
		}
		if err := systemctl("restart", "gwatch-agent.service"); err != nil {
			fmt.Fprintf(os.Stderr, "warning: the gwatch-agent service did not start: %v\nStart it with: sudo systemctl enable --now gwatch-agent\n", err)
			return
		}
		fmt.Println("Started the gwatch-agent service; it starts with this machine from now on.")
	case p.by == "homebrew":
		fmt.Println("Run it in the background, starting at login: brew services start gwatch-agent")
	case p.by == "docker":
		fmt.Println("Start the agent's container with the same volume, and it reports with this token from now on.")
	}
}

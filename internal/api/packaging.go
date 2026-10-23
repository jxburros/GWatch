package api

import (
	"errors"
	"fmt"
	"runtime"
)

// Packaged builds of the server.
//
// GWatch is built with -ldflags "-X main.packagedBy=<deb|rpm|docker|homebrew|
// winget>" for every package (packaging/build-server.sh, the Dockerfile), and
// main hands that to Updater.PackagedBy. A package manager believes it owns
// the files it installed: GWatch swapping /usr/bin/gwatch underneath dpkg
// would leave apt recording one binary while another runs, and the next
// package upgrade silently undoing GWatch's own. So a packaged build reports
// that a newer release exists, like any other, but never installs it; the
// update screen says which package manager owns it and what to run instead
// (UpdateStatus.PackagedBy, PackageManager, PackageCommand).
//
// The agent makes the same decision in cmd/gwatch-agent/packaging.go.

// ErrPackaged marks an update refused because a package manager owns this
// copy of GWatch.
var ErrPackaged = errors.New("installed by a package manager")

// packageArch is the architecture the package file names are built from. A
// variable so tests can ask for the others.
var packageArch = runtime.GOARCH

// packageManager names who owns a packaged build, for messages.
func packageManager(by string) string {
	switch by {
	case "deb":
		return "apt"
	case "rpm":
		return "dnf"
	case "docker":
		return "the container image"
	case "homebrew":
		return "Homebrew"
	case "winget":
		return "winget"
	}
	return "the package manager that installed it"
}

// packageUpdateCommand is what someone types to move a packaged GWatch to
// latest (which may be empty when no check has run yet).
//
// The .deb and .rpm are release assets, not an apt or dnf repository (that is
// a follow-up: docs/INSTALL-LINUX.md), so for them the answer is to install
// the newer file; `apt upgrade` would not know there was anything to upgrade
// to.
func packageUpdateCommand(by, latest, goarch string) string {
	v := latest
	if v == "" {
		v = "<version>"
	}
	switch by {
	case "deb":
		f := fmt.Sprintf("gwatch_%s-1_%s.deb", v, debArch(goarch))
		return fmt.Sprintf("download %s from the GWatch release and run: sudo apt install ./%s", f, f)
	case "rpm":
		f := fmt.Sprintf("gwatch-%s-1.%s.rpm", v, rpmArch(goarch))
		return fmt.Sprintf("download %s from the GWatch release and run: sudo dnf install ./%s", f, f)
	case "docker":
		return "docker compose pull && docker compose up -d"
	case "homebrew":
		return "brew upgrade gwatch"
	case "winget":
		return "winget upgrade GWatch.GWatch"
	}
	return "update it the same way it was installed"
}

// packagedRefusal is Apply's answer on a packaged build.
func packagedRefusal(by, latest string) error {
	return fmt.Errorf("%w: this copy of GWatch was installed by %s, which owns the executable, so GWatch will not replace it; to update, %s",
		ErrPackaged, packageManager(by), packageUpdateCommand(by, latest, packageArch))
}

// latestKnown is the newest version the last check found, or "".
func (u *Updater) latestKnown() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.status.Last == nil {
		return ""
	}
	return u.status.Last.LatestVersion
}

// debArch and rpmArch map Go's architecture names to the ones nfpm gives the
// package files (packaging/nfpm/gwatch.yaml); GOARM=7 is armhf / armv7hl.
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

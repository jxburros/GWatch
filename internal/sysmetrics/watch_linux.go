//go:build linux

package sysmetrics

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"

	"github.com/jxburros/GWatch/internal/model"
)

// Services on Linux are systemd units, read with systemctl rather than over
// D-Bus: systemctl is on every systemd machine, talks to systemd over its own
// private socket when it runs as root, and keeps a D-Bus client out of an
// agent that otherwise needs nothing. A machine without systemd (Alpine with
// OpenRC, most containers) has no systemctl, and watching a service there
// reports that instead of calling everything stopped.
//
// Processes come from /proc, which under a host root is the host's.

var errNoSystemctl = errors.New("systemctl is not installed, so services cannot be read on this machine (watching programs still works)")

var errHostRootServices = errors.New("services cannot be read through a host root: systemctl in the container would answer for the container (watching programs still works)")

// runSystemctl runs systemctl with a timeout. It is a variable so a test can
// answer for a machine with whatever units it likes.
var runSystemctl = func(ctx context.Context, args ...string) ([]byte, error) {
	if mountRoot != "" {
		return nil, errHostRootServices
	}
	path, err := exec.LookPath("systemctl")
	if err != nil {
		return nil, errNoSystemctl
	}
	ctx, cancel := context.WithTimeout(ctx, watchTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), "SYSTEMD_COLORS=0", "SYSTEMD_PAGER=", "LC_ALL=C")
	return cmd.Output()
}

// serviceStates asks systemd about the watched units in one call. systemctl
// show answers for a unit it has never heard of too (LoadState=not-found),
// so one block comes back per name asked, in order, whatever exists.
func serviceStates(ctx context.Context, names []string) ([]model.HostService, error) {
	args := []string{"show", "--no-pager", "-p", "Id,LoadState,ActiveState,SubState,Description", "--"}
	for _, n := range names {
		args = append(args, systemdUnitName(n))
	}
	out, err := runSystemctl(ctx, args...)
	if err != nil {
		return nil, err
	}
	return systemdServices(names, parseSystemctlShow(string(out)))
}

// listServices lists every service unit systemd has loaded, running or not.
// A unit file that is installed but disabled and never started is not
// loaded and so not listed; its name can still be typed.
func listServices(ctx context.Context) ([]model.HostService, error) {
	out, err := runSystemctl(ctx, "list-units", "--type=service", "--all", "--no-legend", "--plain", "--no-pager")
	if err != nil {
		return nil, err
	}
	return parseSystemctlListUnits(string(out)), nil
}

// processNames is the executable name of every process in /proc, kernel
// threads left out. A process that exits while it is being read is simply
// skipped.
func processNames(context.Context) ([]string, error) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		dir := procRoot + "/" + e.Name()
		comm, err := os.ReadFile(dir + "/comm")
		if err != nil {
			continue
		}
		cmdline, err := os.ReadFile(dir + "/cmdline")
		if err != nil {
			continue
		}
		if name := procName(string(comm), string(cmdline)); name != "" {
			out = append(out, name)
		}
	}
	return out, nil
}

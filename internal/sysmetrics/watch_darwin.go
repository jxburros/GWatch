//go:build darwin

package sysmetrics

import (
	"context"
	"os/exec"

	"github.com/jxburros/GWatch/internal/model"
)

// Services on macOS are launchd jobs, listed by `launchctl list`: run as root
// (as an installed agent is) it lists the system's daemons, run as a user it
// lists that user's agents. Processes are listed by ps, which macOS lets any
// account run over every process.

// runWatchCommand runs launchctl or ps with a timeout. It is a variable so a
// test can answer for it.
var runWatchCommand = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, watchTimeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

func listServices(ctx context.Context) ([]model.HostService, error) {
	out, err := runWatchCommand(ctx, "/bin/launchctl", "list")
	if err != nil {
		return nil, err
	}
	return parseLaunchctlList(string(out)), nil
}

func serviceStates(ctx context.Context, names []string) ([]model.HostService, error) {
	all, err := listServices(ctx)
	if err != nil {
		return nil, err
	}
	return pickServices(all, names, launchdMatch), nil
}

// processNames asks ps for the executable name (-c) of every process (-ax),
// with no header (comm=).
func processNames(ctx context.Context) ([]string, error) {
	out, err := runWatchCommand(ctx, "/bin/ps", "-axco", "comm=")
	if err != nil {
		return nil, err
	}
	return parsePsComm(string(out)), nil
}

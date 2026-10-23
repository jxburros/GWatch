//go:build !linux

package serviceinstall

// Everywhere but Linux the service is installed by kardianos/service as it
// always was (Windows service, launchd daemon). These stand-ins let main.go be
// written once; SystemdAvailable being false means none of the others is
// ever called.

import (
	"errors"
	"io"
)

var errNoSystemd = errors.New("the systemd service installation is only available on Linux")

// SystemdAvailable is always false off Linux.
func SystemdAvailable() bool { return false }

// SystemdServiceName is never consulted off Linux.
func SystemdServiceName() string { return "GWatch" }

// SystemdInstall mirrors the Linux type.
type SystemdInstall struct {
	DataDir    string
	DataDirSet bool
	Listen     string
	ListenSet  bool
	Env        map[string]string
	NoStart    bool
	Prepare    func(dataDir string) error
	Out        io.Writer
}

// SystemdResult mirrors the Linux type.
type SystemdResult struct {
	DataDir  string
	Listen   string
	Started  bool
	Migrated []string
}

// SystemdUninstall mirrors the Linux type.
type SystemdUninstall struct {
	Purge   bool
	Yes     bool
	DataDir string
	In      io.Reader
	Out     io.Writer
}

// InstallSystemd is not available off Linux.
func InstallSystemd(SystemdInstall) (SystemdResult, error) { return SystemdResult{}, errNoSystemd }

// UninstallSystemd is not available off Linux.
func UninstallSystemd(SystemdUninstall) error { return errNoSystemd }

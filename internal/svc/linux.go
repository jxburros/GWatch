package svc

// The three Linux service managers: systemd, which is nearly every Linux
// machine; OpenRC, which is Alpine and a few others; and a System V init
// script for whatever is left, a container with no init among them. They are
// the three the kardianos library chose between for these programs (it also
// knew upstart, which no supported distribution still runs). Which one a
// machine has is decided once, in service_linux.go.
//
// None of this file is Linux-only Go: the paths are joined onto root, and the
// commands go through a runner, so the tests (linux_test.go) run the real code
// against a temporary directory and a scripted systemctl on any platform.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// fsRoot is a filesystem prefix: "/" in production, a temporary directory
// in the tests.
type fsRoot string

func (r fsRoot) path(p string) string { return filepath.Join(string(r), filepath.FromSlash(p)) }

func (r fsRoot) exists(p string) bool {
	_, err := os.Lstat(r.path(p))
	return err == nil
}

// create writes a file that must not already exist. An install over an
// existing one is refused, as it always was, rather than quietly replacing a
// unit someone may have edited.
func (r fsRoot) create(p string, data string, mode fs.FileMode) error {
	full := r.path(p)
	if _, err := os.Lstat(full); err == nil {
		return fmt.Errorf("the service is already installed (%s exists)", p)
	}
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, err = f.WriteString(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		// The umask may have taken bits off; the scripts must be executable.
		err = os.Chmod(full, mode)
	}
	if err != nil {
		os.Remove(full)
	}
	return err
}

// remove deletes a file, and says ErrNotInstalled when it was not there.
func (r fsRoot) remove(p string) error {
	err := os.Remove(r.path(p))
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNotInstalled
	}
	return err
}

// ---- systemd ----

type systemdManager struct {
	root fsRoot
	r    runner
}

func (m systemdManager) unitPath(c Config) string { return "/etc/systemd/system/" + unitName(c) }

func unitName(c Config) string { return c.Name + ".service" }

func (m systemdManager) install(c Config, exe string) error {
	unit, err := SystemdUnit(c, exe)
	if err != nil {
		return err
	}
	if err := m.root.create(m.unitPath(c), unit, 0644); err != nil {
		return err
	}
	if _, _, err := m.r.run("systemctl", "daemon-reload"); err != nil {
		return err
	}
	_, _, err = m.r.run("systemctl", "enable", unitName(c))
	return err
}

func (m systemdManager) uninstall(c Config) error {
	if !m.root.exists(m.unitPath(c)) {
		return ErrNotInstalled
	}
	// A unit that was never enabled, or that someone disabled by hand,
	// cannot be disabled again; removing it is what matters.
	_, _, _ = m.r.run("systemctl", "disable", unitName(c))
	if err := m.root.remove(m.unitPath(c)); err != nil {
		return err
	}
	_, _, err := m.r.run("systemctl", "daemon-reload")
	return err
}

func (m systemdManager) start(c Config) error {
	_, _, err := m.r.run("systemctl", "start", unitName(c))
	return err
}

func (m systemdManager) stop(c Config) error {
	_, _, err := m.r.run("systemctl", "stop", unitName(c))
	return err
}

func (m systemdManager) restart(c Config) error {
	_, _, err := m.r.run("systemctl", "restart", unitName(c))
	return err
}

// status reads `systemctl is-active`, whose answer is one word on standard
// output and a non-zero exit status for anything but active. "inactive" is
// also what it says about a unit that does not exist, so that case asks
// whether the unit file is there.
func (m systemdManager) status(c Config) (Status, error) {
	out, _, err := m.r.run("systemctl", "is-active", unitName(c))
	state := strings.TrimSpace(out)
	switch state {
	case "active", "activating", "reloading", "deactivating":
		return StatusRunning, nil
	case "inactive":
		lf, _, _ := m.r.run("systemctl", "list-unit-files", "--type=service", "--no-legend", unitName(c))
		if strings.Contains(lf, unitName(c)) || m.root.exists(m.unitPath(c)) {
			return StatusStopped, nil
		}
		return StatusUnknown, ErrNotInstalled
	case "failed":
		return StatusUnknown, errors.New("the service is in the failed state (see: journalctl -u " + unitName(c) + ")")
	case "":
		if err != nil {
			return StatusUnknown, err
		}
	}
	return StatusUnknown, ErrNotInstalled
}

func (m systemdManager) run(s *Service) error { return runForeground(s) }

// ---- OpenRC ----

type openrcManager struct {
	root fsRoot
	r    runner
}

func initScriptPath(c Config) string { return "/etc/init.d/" + c.Name }

func (m openrcManager) install(c Config, exe string) error {
	if err := m.root.create(initScriptPath(c), OpenRCScript(c, exe), 0755); err != nil {
		return err
	}
	_, _, err := m.r.run("rc-update", "add", c.Name, "default")
	return err
}

func (m openrcManager) uninstall(c Config) error {
	if !m.root.exists(initScriptPath(c)) {
		return ErrNotInstalled
	}
	_, _, _ = m.r.run("rc-update", "del", c.Name)
	return m.root.remove(initScriptPath(c))
}

func (m openrcManager) start(c Config) error {
	_, _, err := m.r.run("rc-service", c.Name, "start")
	return err
}

func (m openrcManager) stop(c Config) error {
	_, _, err := m.r.run("rc-service", c.Name, "stop")
	return err
}

func (m openrcManager) restart(c Config) error {
	_, _, err := m.r.run("rc-service", c.Name, "restart")
	return err
}

// status: rc-service exits 0 for a started service, 3 for a stopped one
// (ESRCH) and something else — usually 1 or 2 — when it does not know it.
func (m openrcManager) status(c Config) (Status, error) {
	if !m.root.exists(initScriptPath(c)) {
		return StatusUnknown, ErrNotInstalled
	}
	_, code, err := m.r.run("rc-service", c.Name, "status")
	switch {
	case err == nil:
		return StatusRunning, nil
	case code == 3:
		return StatusStopped, nil
	}
	return StatusUnknown, err
}

func (m openrcManager) run(s *Service) error { return runForeground(s) }

// ---- System V ----

type sysvManager struct {
	root fsRoot
	r    runner
}

// sysvLinks are the runlevel links an install makes, so that the script is
// started at boot and stopped at shutdown on a system that still reads them.
func sysvLinks(c Config) []string {
	var links []string
	for _, l := range []string{"2", "3", "4", "5"} {
		links = append(links, "/etc/rc"+l+".d/S50"+c.Name)
	}
	for _, l := range []string{"0", "1", "6"} {
		links = append(links, "/etc/rc"+l+".d/K02"+c.Name)
	}
	return links
}

func (m sysvManager) install(c Config, exe string) error {
	if err := m.root.create(initScriptPath(c), SysVScript(c, exe), 0755); err != nil {
		return err
	}
	for _, l := range sysvLinks(c) {
		// A directory that is not there is a runlevel this machine does not
		// have; the link is a convenience, not a requirement.
		if _, err := os.Stat(filepath.Dir(m.root.path(l))); err == nil {
			_ = os.Symlink(initScriptPath(c), m.root.path(l))
		}
	}
	return nil
}

func (m sysvManager) uninstall(c Config) error {
	for _, l := range sysvLinks(c) {
		if target, err := os.Readlink(m.root.path(l)); err == nil && target == initScriptPath(c) {
			os.Remove(m.root.path(l))
		}
	}
	return m.root.remove(initScriptPath(c))
}

func (m sysvManager) script(c Config, action string) (int, error) {
	if !m.root.exists(initScriptPath(c)) {
		return 0, ErrNotInstalled
	}
	_, code, err := m.r.run(m.root.path(initScriptPath(c)), action)
	return code, err
}

func (m sysvManager) start(c Config) error {
	_, err := m.script(c, "start")
	return err
}

func (m sysvManager) stop(c Config) error {
	_, err := m.script(c, "stop")
	return err
}

func (m sysvManager) restart(c Config) error {
	_, err := m.script(c, "restart")
	return err
}

func (m sysvManager) status(c Config) (Status, error) {
	code, err := m.script(c, "status")
	switch {
	case errors.Is(err, ErrNotInstalled):
		return StatusUnknown, err
	case err == nil:
		return StatusRunning, nil
	case code == 3:
		return StatusStopped, nil
	}
	return StatusUnknown, err
}

func (m sysvManager) run(s *Service) error { return runForeground(s) }

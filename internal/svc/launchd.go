package svc

// macOS: a launch daemon, /Library/LaunchDaemons/<name>.plist, controlled
// with launchctl's bootstrap, bootout, kickstart and print subcommands in the
// system domain. These replaced load, unload and list in macOS 10.10 and are
// the ones that give a reliable exit status; the property list itself is the
// same file, with the same label and log paths, that earlier releases wrote,
// so a daemon they installed is controlled and removed by this code as it is.
//
// Like the Linux managers this file is ordinary Go with its paths under a
// root and its commands behind a runner, so launchd_test.go runs it anywhere.

import "regexp"

type launchdManager struct {
	root fsRoot
	r    runner
}

func plistPath(c Config) string { return "/Library/LaunchDaemons/" + c.Name + ".plist" }

func launchdTarget(c Config) string { return "system/" + c.Name }

// install writes the property list and leaves the daemon unloaded, as the
// other managers leave theirs stopped: Start loads it, and RunAtLoad then
// starts it. launchd refuses a property list that anyone but root can write,
// hence the mode.
func (m launchdManager) install(c Config, exe string) error {
	return m.root.create(plistPath(c), LaunchdPlist(c, exe), 0644)
}

func (m launchdManager) uninstall(c Config) error {
	if !m.root.exists(plistPath(c)) {
		return ErrNotInstalled
	}
	if loaded, _ := m.loaded(c); loaded {
		_, _, _ = m.r.run("launchctl", "bootout", launchdTarget(c))
	}
	return m.root.remove(plistPath(c))
}

// loaded asks launchd whether it has the job, and returns what it printed
// about it. `launchctl print` exits non-zero (113, "could not find service")
// for a job it does not have.
func (m launchdManager) loaded(c Config) (bool, string) {
	out, _, err := m.r.run("launchctl", "print", launchdTarget(c))
	return err == nil, out
}

// start loads the daemon if it is not loaded, which starts it, and kicks it
// if it is loaded but not running (it may be waiting out launchd's throttle
// after exiting).
func (m launchdManager) start(c Config) error {
	if !m.root.exists(plistPath(c)) {
		return ErrNotInstalled
	}
	loaded, out := m.loaded(c)
	if !loaded {
		_, _, err := m.r.run("launchctl", "bootstrap", "system", m.root.path(plistPath(c)))
		return err
	}
	if launchdRunning(out) {
		return nil
	}
	_, _, err := m.r.run("launchctl", "kickstart", launchdTarget(c))
	return err
}

// stop unloads the daemon. Ending the process alone would not do: KeepAlive
// would have launchd start it again straight away. Stopping a daemon that is
// not loaded is not an error; it is already what was asked for.
func (m launchdManager) stop(c Config) error {
	if !m.root.exists(plistPath(c)) {
		return ErrNotInstalled
	}
	if loaded, _ := m.loaded(c); !loaded {
		return nil
	}
	_, _, err := m.r.run("launchctl", "bootout", launchdTarget(c))
	return err
}

// restart kills and restarts a loaded daemon in one step (kickstart -k), and
// loads one that is not.
func (m launchdManager) restart(c Config) error {
	if !m.root.exists(plistPath(c)) {
		return ErrNotInstalled
	}
	if loaded, _ := m.loaded(c); !loaded {
		_, _, err := m.r.run("launchctl", "bootstrap", "system", m.root.path(plistPath(c)))
		return err
	}
	_, _, err := m.r.run("launchctl", "kickstart", "-k", launchdTarget(c))
	return err
}

func (m launchdManager) status(c Config) (Status, error) {
	if !m.root.exists(plistPath(c)) {
		return StatusUnknown, ErrNotInstalled
	}
	if loaded, out := m.loaded(c); loaded && launchdRunning(out) {
		return StatusRunning, nil
	}
	return StatusStopped, nil
}

func (m launchdManager) run(s *Service) error { return runForeground(s) }

var (
	launchdStateRe = regexp.MustCompile(`(?m)^\s*state = (\S+)`)
	launchdPIDRe   = regexp.MustCompile(`(?m)^\s*pid = [0-9]+`)
)

// launchdRunning reads `launchctl print` output: a running job says
// "state = running" and has a pid line; one that is loaded but not running
// says "state = not running" (or "waiting") and has none.
func launchdRunning(out string) bool {
	if m := launchdStateRe.FindStringSubmatch(out); m != nil {
		return m[1] == "running"
	}
	return launchdPIDRe.MatchString(out)
}

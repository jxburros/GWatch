// Package svc hosts gwatch and gwatch-agent as a background service: it
// registers them with the platform's service manager (the Windows Service
// Control Manager, launchd on macOS, systemd — or, failing that, OpenRC or a
// System V init script — on Linux), starts, stops and asks after them, and runs
// them under that manager once it starts them.
//
// It replaces github.com/kardianos/service (#46), and on purpose does the same
// things that library did for the two programs, with the same names and the
// same files, so that a machine installed by an earlier release is upgraded
// and uninstalled by this one without noticing the difference. The parts it
// did not use — user services, custom loggers, upstart, the BSDs — are not
// here. What the platform-specific code would otherwise do by hand (render a
// unit, a property list or an init script, decide what a status answer means)
// is written as pure functions over the configuration, and every command the
// Linux and macOS code runs goes through a small runner interface, so that the
// sequences can be tested without a service manager on the test machine.
package svc

import (
	"errors"
	"fmt"
	"os"
	"time"
)

// ErrNotInstalled is the error Status returns when the service manager does
// not know the service at all, as opposed to knowing it and having it stopped.
// Callers test for it with errors.Is to tell the person to install first.
var ErrNotInstalled = errors.New("the service is not installed")

// Status is what the service manager says about an installed service.
type Status int

const (
	// StatusUnknown goes with an error: the question could not be answered.
	StatusUnknown Status = iota
	// StatusRunning covers a service that is starting as well as one that
	// has started: either way it needs no start.
	StatusRunning
	// StatusStopped covers a service that is stopping as well as stopped.
	StatusStopped
)

// String is the word the status commands print.
func (s Status) String() string {
	switch s {
	case StatusRunning:
		return "running"
	case StatusStopped:
		return "stopped"
	}
	return "unknown"
}

// Config describes the service to the service manager. Name is the only
// field every call needs; the rest matter when installing.
type Config struct {
	// Name is the service's name to the service manager: the Windows service
	// name, the launchd label, the systemd unit without its ".service", the
	// OpenRC or System V script in /etc/init.d.
	Name string
	// DisplayName is the name Windows shows in the Services console, and the
	// name the Linux init scripts give the service.
	DisplayName string
	// Description is shown in the Services console and written into the
	// systemd unit and init scripts.
	Description string
	// Executable is the program the service manager starts. Empty means this
	// one, wherever it is.
	Executable string
	// Arguments follow the program on the service's command line.
	Arguments []string
	// RestartDelay is how long Windows waits before restarting a service
	// that stopped without being asked to. Zero means five seconds, which
	// is what both programs have always asked for; systemd and launchd have
	// their own settings for this, in the unit and the property list.
	RestartDelay time.Duration
	// SystemdUnit is a text/template for the systemd unit (see SystemdUnit
	// for the fields it may use). Empty means a plain unit that restarts the
	// service when it fails.
	SystemdUnit string
	// EnvFile, if set, is an environment file the OpenRC and System V
	// scripts read before starting the service, as the systemd unit does with
	// EnvironmentFile=. It is optional: a missing file is not an error.
	EnvFile string
}

// Program is what runs inside the service. Start must not block: it starts
// the work and returns, and Stop ends it, waiting a reasonable time for it to
// finish. The *Service is the one running the program; it may be nil when the
// program calls its own Stop.
type Program interface {
	Start(s *Service) error
	Stop(s *Service) error
}

// manager is one platform's service manager. Each method gets the whole
// configuration and is free to ignore most of it.
type manager interface {
	install(c Config, exe string) error
	uninstall(c Config) error
	start(c Config) error
	stop(c Config) error
	restart(c Config) error
	status(c Config) (Status, error)
	// run runs the program until the service manager or a signal stops it.
	run(s *Service) error
}

// Service is a configured service: something to install, control or run.
type Service struct {
	cfg  Config
	prog Program
	m    manager
}

// New describes a service. prog may be nil for a caller that will only
// install, control or ask after the service, never Run it.
func New(prog Program, c Config) (*Service, error) {
	if c.Name == "" {
		return nil, errors.New("svc: the service needs a name")
	}
	if c.RestartDelay == 0 {
		c.RestartDelay = 5 * time.Second
	}
	m, err := platformManager()
	if err != nil {
		return nil, err
	}
	return &Service{cfg: c, prog: prog, m: m}, nil
}

// String names the service in messages.
func (s *Service) String() string {
	if s.cfg.DisplayName != "" {
		return s.cfg.DisplayName
	}
	return s.cfg.Name
}

// Name is the service's name to the service manager.
func (s *Service) Name() string { return s.cfg.Name }

// Install registers the service. It does not start it. Installing a service
// that is already registered is an error, as it always was: an upgrade
// replaces the program file and restarts the service, it does not install
// it a second time.
func (s *Service) Install() error {
	exe := s.cfg.Executable
	if exe == "" {
		var err error
		if exe, err = os.Executable(); err != nil {
			return err
		}
	}
	return s.m.install(s.cfg, exe)
}

// Uninstall removes the service's registration. Callers stop it first.
func (s *Service) Uninstall() error { return s.m.uninstall(s.cfg) }

// Start asks the service manager to start the service.
func (s *Service) Start() error { return s.m.start(s.cfg) }

// Stop asks the service manager to stop the service, and waits for it to.
func (s *Service) Stop() error { return s.m.stop(s.cfg) }

// Restart stops the service and starts it again.
func (s *Service) Restart() error { return s.m.restart(s.cfg) }

// Status asks the service manager after the service. A service it does not
// know returns ErrNotInstalled.
func (s *Service) Status() (Status, error) { return s.m.status(s.cfg) }

// Run runs the program: under the service manager when it started this
// process, and otherwise in the foreground until Ctrl+C or SIGTERM.
func (s *Service) Run() error {
	if s.prog == nil {
		return errors.New("svc: no program to run")
	}
	return s.m.run(s)
}

// ControlActions are the actions Control takes, in the order the command
// lines list them.
var ControlActions = []string{"start", "stop", "restart", "install", "uninstall"}

// Control performs one of ControlActions by name, for the start, stop and
// restart commands and the agent's settings page.
func (s *Service) Control(action string) error {
	var err error
	switch action {
	case "start":
		err = s.Start()
	case "stop":
		err = s.Stop()
	case "restart":
		err = s.Restart()
	case "install":
		err = s.Install()
	case "uninstall":
		err = s.Uninstall()
	default:
		return fmt.Errorf("unknown service action %q", action)
	}
	if err != nil {
		return fmt.Errorf("failed to %s %s: %w", action, s, err)
	}
	return nil
}

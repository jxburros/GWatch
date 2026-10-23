//go:build !linux && !darwin && !windows

package svc

import (
	"errors"
	"runtime"
)

// platformManager: GWatch is built for Windows, macOS and Linux only. On any
// other system either program still runs in the foreground; it just cannot
// install itself as a service.
func platformManager() (manager, error) { return unsupported{}, nil }

var errUnsupported = errors.New("installing a service is not supported on " + runtime.GOOS + "; run the program from your own init system instead")

type unsupported struct{}

func (unsupported) install(Config, string) error  { return errUnsupported }
func (unsupported) uninstall(Config) error        { return errUnsupported }
func (unsupported) start(Config) error            { return errUnsupported }
func (unsupported) stop(Config) error             { return errUnsupported }
func (unsupported) restart(Config) error          { return errUnsupported }
func (unsupported) status(Config) (Status, error) { return StatusUnknown, errUnsupported }
func (unsupported) run(s *Service) error          { return runForeground(s) }

// Interactive is always true where there is no service manager to start us.
func Interactive() bool { return true }

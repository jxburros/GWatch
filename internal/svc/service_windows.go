//go:build windows

package svc

// Windows: a service registered with the Service Control Manager. The
// registration, control and status calls go through x/sys/windows/svc/mgr and
// the service's own life through x/sys/windows/svc, which is what the
// kardianos library did underneath; the settings are the ones it was given —
// own process, started automatically at boot, restarted five seconds after it
// fails — so a service an earlier release registered is indistinguishable
// from one this release registers.

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"time"

	"golang.org/x/sys/windows"
	winsvc "golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

func platformManager() (manager, error) { return windowsManager{}, nil }

var (
	interactiveOnce sync.Once
	interactive     bool
)

// Interactive reports whether this process is something other than a service
// the Service Control Manager started. When Windows cannot say, the answer is
// yes: a program that wrongly thinks it is in a terminal still works, where
// one that wrongly waits for the Service Control Manager hangs.
func Interactive() bool {
	interactiveOnce.Do(func() {
		isService, err := winsvc.IsWindowsService()
		interactive = err != nil || !isService
	})
	return interactive
}

type windowsManager struct{}

// recoveryActions are what the Service Control Manager does when the service
// ends without having been asked to: restart it after the delay, every time.
// Windows repeats the last action for failures beyond the list, so one would
// do; three make the Recovery tab of the service's properties read "Restart
// the Service" on every line instead of only the first. The failure count is
// reset after resetPeriod seconds without one.
func recoveryActions(delay time.Duration) ([]mgr.RecoveryAction, uint32) {
	a := mgr.RecoveryAction{Type: mgr.ServiceRestart, Delay: delay}
	return []mgr.RecoveryAction{a, a, a}, 10
}

// serviceConfig is the registration for c, apart from the program and its
// arguments, which CreateService takes separately.
func serviceConfig(c Config) mgr.Config {
	return mgr.Config{
		ServiceType:  windows.SERVICE_WIN32_OWN_PROCESS,
		StartType:    mgr.StartAutomatic,
		ErrorControl: mgr.ErrorNormal,
		DisplayName:  c.DisplayName,
		Description:  c.Description,
	}
}

func (windowsManager) install(c Config, exe string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	if s, err := m.OpenService(c.Name); err == nil {
		s.Close()
		return fmt.Errorf("service %s already exists", c.Name)
	}
	s, err := m.CreateService(c.Name, exe, serviceConfig(c), c.Arguments...)
	if err != nil {
		return err
	}
	defer s.Close()
	actions, reset := recoveryActions(c.RestartDelay)
	if err := s.SetRecoveryActions(actions, reset); err != nil {
		// A service that will not come back after a crash is not what was
		// asked for; leave nothing half-registered.
		s.Delete()
		return fmt.Errorf("set recovery actions: %w", err)
	}
	return nil
}

func (windowsManager) uninstall(c Config) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(c.Name)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return ErrNotInstalled
		}
		return err
	}
	defer s.Close()
	if err := s.Delete(); err != nil {
		return err
	}
	// Earlier releases registered an event log source under the service's
	// name, which nothing ever wrote to. Take it away with the service; a
	// machine that never had one has nothing to remove.
	_ = eventlog.Remove(c.Name)
	return nil
}

// openLowPriv opens the service with only the rights the control and status
// commands need, which an ordinary user has for querying: the agent's settings
// page runs unelevated and still shows whether the service is running.
func openLowPriv(name string) (*mgr.Mgr, *mgr.Service, error) {
	h, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT|windows.SC_MANAGER_ENUMERATE_SERVICE)
	if err != nil {
		return nil, nil, err
	}
	m := &mgr.Mgr{Handle: h}
	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		m.Disconnect()
		return nil, nil, err
	}
	sh, err := windows.OpenService(h, n, windows.SERVICE_QUERY_CONFIG|windows.SERVICE_QUERY_STATUS|windows.SERVICE_START|windows.SERVICE_STOP)
	if err != nil {
		m.Disconnect()
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return nil, nil, ErrNotInstalled
		}
		return nil, nil, err
	}
	return m, &mgr.Service{Handle: sh, Name: name}, nil
}

func (windowsManager) start(c Config) error {
	m, s, err := openLowPriv(c.Name)
	if err != nil {
		return err
	}
	defer m.Disconnect()
	defer s.Close()
	if err := s.Start(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
		return err
	}
	return nil
}

func (windowsManager) stop(c Config) error {
	m, s, err := openLowPriv(c.Name)
	if err != nil {
		return err
	}
	defer m.Disconnect()
	defer s.Close()
	return stopWait(s)
}

func (windowsManager) restart(c Config) error {
	m, s, err := openLowPriv(c.Name)
	if err != nil {
		return err
	}
	defer m.Disconnect()
	defer s.Close()
	if err := stopWait(s); err != nil {
		return err
	}
	return s.Start()
}

// stopTimeout bounds the wait for a stop. The programs' own Stop gives the
// work 30 seconds to finish, and the Service Control Manager is patient for
// as long as the service keeps reporting that it is stopping.
const stopTimeout = 60 * time.Second

// stopWait asks the service to stop and waits until it has, so that a
// restart, or an uninstall followed by a new install, does not trip over the
// old process. A service that is already stopped is already what was asked.
func stopWait(s *mgr.Service) error {
	st, err := s.Control(winsvc.Stop)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
			return nil
		}
		return err
	}
	deadline := time.Now().Add(stopTimeout)
	for st.State != winsvc.Stopped {
		if time.Now().After(deadline) {
			return fmt.Errorf("the service did not stop within %s", stopTimeout)
		}
		time.Sleep(250 * time.Millisecond)
		if st, err = s.Query(); err != nil {
			return err
		}
	}
	return nil
}

func (windowsManager) status(c Config) (Status, error) {
	m, s, err := openLowPriv(c.Name)
	if err != nil {
		return StatusUnknown, err
	}
	defer m.Disconnect()
	defer s.Close()
	st, err := s.Query()
	if err != nil {
		return StatusUnknown, err
	}
	return stateStatus(st.State)
}

// stateStatus folds the Service Control Manager's seven states into the
// three the commands report: anything on its way up counts as running, and
// anything paused or on its way down as stopped.
func stateStatus(st winsvc.State) (Status, error) {
	switch st {
	case winsvc.StartPending, winsvc.Running:
		return StatusRunning, nil
	case winsvc.Stopped, winsvc.StopPending, winsvc.Paused, winsvc.PausePending, winsvc.ContinuePending:
		return StatusStopped, nil
	}
	return StatusUnknown, fmt.Errorf("unknown service state %d", st)
}

func (windowsManager) run(s *Service) error {
	if Interactive() {
		// Started from a terminal: run until Ctrl+C. (SIGTERM does not
		// exist for a Windows console program.)
		if err := s.prog.Start(s); err != nil {
			return err
		}
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt)
		<-sig
		signal.Stop(sig)
		return s.prog.Stop(s)
	}
	h := &handler{s: s}
	runErr := winsvc.Run(s.cfg.Name, h)
	if err := h.getErr(); err != nil {
		return err
	}
	return runErr
}

// handler is the service's side of the conversation with the Service Control
// Manager: it reports the service starting and running, answers its status
// questions, and stops the program when asked to or when Windows shuts down.
type handler struct {
	s *Service

	// err is from the program's Start or Stop, for Run to return. Execute
	// runs on a thread the Service Control Manager starts, hence the lock.
	mu  sync.Mutex
	err error
}

func (h *handler) setErr(err error) {
	h.mu.Lock()
	h.err = err
	h.mu.Unlock()
}

func (h *handler) getErr() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.err
}

func (h *handler) Execute(_ []string, r <-chan winsvc.ChangeRequest, changes chan<- winsvc.Status) (bool, uint32) {
	const accepts = winsvc.AcceptStop | winsvc.AcceptShutdown
	changes <- winsvc.Status{State: winsvc.StartPending}
	if err := h.s.prog.Start(h.s); err != nil {
		h.setErr(err)
		return true, 1
	}
	changes <- winsvc.Status{State: winsvc.Running, Accepts: accepts}
	for c := range r {
		switch c.Cmd {
		case winsvc.Interrogate:
			changes <- c.CurrentStatus
		case winsvc.Stop, winsvc.Shutdown:
			changes <- winsvc.Status{State: winsvc.StopPending}
			if err := h.s.prog.Stop(h.s); err != nil {
				h.setErr(err)
				return true, 2
			}
			return false, 0
		}
	}
	return false, 0
}

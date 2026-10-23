package svc

import (
	"errors"
	"strings"
	"testing"
	"time"
)

type recordingManager struct {
	calls []string
	fail  error
}

func (m *recordingManager) note(s string) error { m.calls = append(m.calls, s); return m.fail }
func (m *recordingManager) install(c Config, exe string) error {
	return m.note("install " + c.Name + " " + exe + " " + c.RestartDelay.String())
}
func (m *recordingManager) uninstall(Config) error { return m.note("uninstall") }
func (m *recordingManager) start(Config) error     { return m.note("start") }
func (m *recordingManager) stop(Config) error      { return m.note("stop") }
func (m *recordingManager) restart(Config) error   { return m.note("restart") }
func (m *recordingManager) status(Config) (Status, error) {
	return StatusStopped, m.note("status")
}
func (m *recordingManager) run(*Service) error { return m.note("run") }

func TestNewDefaults(t *testing.T) {
	if _, err := New(nil, Config{}); err == nil {
		t.Error("a service without a name was accepted")
	}
	s, err := New(nil, Config{Name: "GWatch"})
	if err != nil {
		t.Fatal(err)
	}
	if s.cfg.RestartDelay != 5*time.Second {
		t.Errorf("restart delay %v, want the 5s both programs ask for", s.cfg.RestartDelay)
	}
	if s.String() != "GWatch" || s.Name() != "GWatch" {
		t.Errorf("names %q %q", s, s.Name())
	}
	if err := s.Run(); err == nil {
		t.Error("Run with no program did not fail")
	}
}

func TestControl(t *testing.T) {
	m := &recordingManager{}
	s := &Service{cfg: Config{Name: "GWatchAgent", DisplayName: "GWatch Hardware Agent", Executable: "/x/agent", RestartDelay: 5 * time.Second}, m: m}
	for _, a := range ControlActions {
		if err := s.Control(a); err != nil {
			t.Fatal(err)
		}
	}
	want := "install GWatchAgent /x/agent 5s"
	if got := strings.Join(m.calls, ","); got != "start,stop,restart,"+want+",uninstall" {
		t.Errorf("calls %s", got)
	}
	if err := s.Control("reload"); err == nil {
		t.Error("an unknown action was accepted")
	}
	m.fail = ErrNotInstalled
	err := s.Control("start")
	if !errors.Is(err, ErrNotInstalled) || !strings.Contains(err.Error(), "failed to start GWatch Hardware Agent") {
		t.Errorf("error %v", err)
	}
}

func TestStatusString(t *testing.T) {
	if StatusRunning.String() != "running" || StatusStopped.String() != "stopped" || StatusUnknown.String() != "unknown" {
		t.Error("status words changed; the status commands and the agent's settings page print them")
	}
}

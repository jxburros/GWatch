//go:build windows

package svc

import (
	"testing"
	"time"

	"golang.org/x/sys/windows"
	winsvc "golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

func TestStateStatus(t *testing.T) {
	for st, want := range map[winsvc.State]Status{
		winsvc.StartPending:    StatusRunning,
		winsvc.Running:         StatusRunning,
		winsvc.StopPending:     StatusStopped,
		winsvc.Stopped:         StatusStopped,
		winsvc.Paused:          StatusStopped,
		winsvc.PausePending:    StatusStopped,
		winsvc.ContinuePending: StatusStopped,
	} {
		if got, err := stateStatus(st); got != want || err != nil {
			t.Errorf("state %d: %v, %v; want %v", st, got, err, want)
		}
	}
	if _, err := stateStatus(99); err == nil {
		t.Error("an unknown state was not an error")
	}
}

func TestServiceRegistration(t *testing.T) {
	c := serviceConfig(Config{Name: "GWatchAgent", DisplayName: "GWatch Hardware Agent", Description: "d"})
	if c.StartType != mgr.StartAutomatic || c.ServiceType != windows.SERVICE_WIN32_OWN_PROCESS || c.DisplayName != "GWatch Hardware Agent" || c.Description != "d" {
		t.Errorf("registration %+v", c)
	}
	actions, reset := recoveryActions(5 * time.Second)
	if len(actions) == 0 || reset == 0 {
		t.Fatalf("actions %v, reset %d", actions, reset)
	}
	for _, a := range actions {
		if a.Type != mgr.ServiceRestart || a.Delay != 5*time.Second {
			t.Errorf("action %+v, want restart after 5s", a)
		}
	}
}

// What the running agent tells the control panel.
//
// The panel (ui.go) is a separate, short-lived process run by whoever is sitting
// at the machine, so it cannot ask the service anything. Instead the service
// writes a small status file after each report and each check-in, and the panel
// reads it. It holds nothing secret: no token, no readings, only how things are
// going and what this machine could report.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/permissions"
)

// agentStatus is the status file.
type agentStatus struct {
	Version     string                 `json:"version"`
	PID         int                    `json:"pid"`
	StartedAt   time.Time              `json:"startedAt"`
	UpdatedAt   time.Time              `json:"updatedAt"`
	Server      string                 `json:"server"`
	Interval    string                 `json:"interval"`
	LastReport  *time.Time             `json:"lastReport,omitempty"`
	ReportOK    bool                   `json:"reportOk"`
	ReportError string                 `json:"reportError,omitempty"`
	Failures    int                    `json:"failures,omitempty"`
	Held        int                    `json:"held,omitempty"`
	LastCheckIn *time.Time             `json:"lastCheckIn,omitempty"`
	CheckInErr  string                 `json:"checkInError,omitempty"`
	LatestAgent string                 `json:"latestAgent,omitempty"`
	Collecting  model.CollectSelection `json:"collecting"`
	FromGWatch  model.CollectSelection `json:"fromGwatch"`
	Inventory   *model.Inventory       `json:"inventory,omitempty"`
}

func statusFile() string { return filepath.Join(filepath.Dir(configFile()), "agent-status.json") }

func (rt *agentRuntime) noteReport(err error, failures int) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.lastReport = time.Now()
	rt.lastReportOK = err == nil
	rt.lastReportErr = ""
	if err != nil {
		rt.lastReportErr = err.Error()
	}
	rt.failures = failures
}

func (rt *agentRuntime) noteCheckIn(err error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.lastCheckIn = time.Now()
	rt.lastCheckInErr = ""
	if err != nil {
		rt.lastCheckInErr = err.Error()
	}
}

// writeStatus saves the status file. held is how many readings are waiting to be
// delivered; negative leaves the last figure alone. Failing to write it is not
// worth mentioning: the panel just shows less.
func (rt *agentRuntime) writeStatus(cfg config, held int) {
	rt.mu.Lock()
	if held >= 0 {
		rt.held = held
	}
	st := agentStatus{
		Version:     version,
		PID:         os.Getpid(),
		StartedAt:   rt.startedAt,
		UpdatedAt:   time.Now(),
		Server:      cfg.baseURL(),
		Interval:    formatDuration(cfg.interval),
		ReportOK:    rt.lastReportOK,
		ReportError: rt.lastReportErr,
		Failures:    rt.failures,
		Held:        rt.held,
		CheckInErr:  rt.lastCheckInErr,
		LatestAgent: rt.latestSeen,
		Collecting:  rt.ceiling.Effective(rt.selection),
		FromGWatch:  rt.selection,
	}
	if !rt.lastReport.IsZero() {
		t := rt.lastReport
		st.LastReport = &t
	}
	if !rt.lastCheckIn.IsZero() {
		t := rt.lastCheckIn
		st.LastCheckIn = &t
	}
	if rt.haveAll {
		inv := rt.inventoryAll
		st.Inventory = &inv
	}
	rt.mu.Unlock()

	path := statusFile()
	if err := permissions.EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return
	}
	// World-readable on purpose: it holds nothing secret, and the panel is run
	// by whoever is at the keyboard, not by the account the service runs as.
	// On Windows the mode bits mean nothing, so the file is given an ACL that
	// says the same thing (status_share_windows.go), for the tray icon.
	shareStatusFile(tmp)
	_ = os.Rename(tmp, path)
}

func readAgentStatus() (agentStatus, error) {
	var st agentStatus
	b, err := os.ReadFile(statusFile())
	if err != nil {
		return st, err
	}
	err = json.Unmarshal(b, &st)
	return st, err
}

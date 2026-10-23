package main

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unsafe"
)

// The Win32 structures the tray and the elevation prompt hand to Windows must
// be exactly the size the C headers give them on 64-bit Windows (amd64 and
// arm64 alike), or the call fails, or reads past the end of the Go value.
func TestWin32StructuresHaveTheirCSizes(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("sizes below are for 64-bit Windows")
	}
	for name, got := range map[string]uintptr{
		"NOTIFYICONDATAW":   unsafe.Sizeof(notifyIconData{}),
		"WNDCLASSEXW":       unsafe.Sizeof(wndClassEx{}),
		"MSG":               unsafe.Sizeof(msg{}),
		"SHELLEXECUTEINFOW": unsafe.Sizeof(shellExecuteInfo{}),
	} {
		want := map[string]uintptr{"NOTIFYICONDATAW": 976, "WNDCLASSEXW": 80, "MSG": 48, "SHELLEXECUTEINFOW": 112}[name]
		if got != want {
			t.Errorf("%s is %d bytes, want %d", name, got, want)
		}
	}
	var n notifyIconData
	if off := unsafe.Offsetof(n.szTip); off != 40 {
		t.Errorf("szTip at %d, want 40", off)
	}
	if off := unsafe.Offsetof(n.guidItem); off != 952 {
		t.Errorf("guidItem at %d, want 952", off)
	}
	var s shellExecuteInfo
	if off := unsafe.Offsetof(s.hProcess); off != 104 {
		t.Errorf("hProcess at %d, want 104", off)
	}
}

func TestTheTraySaysWhatThePanelWould(t *testing.T) {
	now := time.Now()
	recent := now.Add(-30 * time.Second)
	ok := agentStatus{UpdatedAt: recent, Interval: "1m", LastReport: &recent, ReportOK: true, Server: "https://g"}
	failed := ok
	failed.ReportOK, failed.ReportError = false, "connection refused"
	stale := ok
	stale.UpdatedAt = now.Add(-time.Hour)
	for _, c := range []struct {
		svc   string
		st    agentStatus
		err   error
		level trayLevel
		tip   string
	}{
		{"running", ok, nil, levelOK, "reporting"},
		{"running", failed, nil, levelBad, "cannot reach"},
		{"running", stale, nil, levelWarn, "no news"},
		{"stopped", ok, nil, levelBad, "stopped"},
		{"not installed", agentStatus{}, errors.New("no file"), levelWarn, "not installed"},
		{"running", agentStatus{}, errors.New("access denied"), levelOK, "running"},
	} {
		got := describeTrayState(c.svc, c.st, c.err, now)
		if got.level != c.level || !strings.Contains(got.tip, c.tip) || len([]rune(got.tip)) > 127 {
			t.Errorf("%s/%v: %+v", c.svc, c.err, got)
		}
	}
}

package api

import (
	"context"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// The timeline's summary of a weekly window names the time it runs at, not
// the moment it was saved (StartAt, which the API fills with now).
func TestMaintenanceSummaryNamesTheWindowsOwnStartTime(t *testing.T) {
	m := model.MaintenanceWindow{Weekdays: []int{1}, StartTime: "02:00", TimeZone: "America/New_York", DurationMinutes: 90,
		StartAt: time.Date(2026, 10, 24, 17, 41, 0, 0, time.UTC)}
	want := "Weekly on 1 day(s) at 02:00 America/New_York for 90 minutes."
	if got := maintenanceSummary(m); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A window whose node id is 0 lost its node; it is refused rather than saved
// as a window over every node.
func TestMaintenanceWithoutItsNodeIsRefused(t *testing.T) {
	ts, srv := newTestServer(t)
	zero := int64(0)
	start := time.Now().Add(time.Hour)
	body := model.MaintenanceWindow{Name: "Patch", NodeID: &zero, Enabled: true, StartAt: start, EndAt: start.Add(time.Hour)}
	if code := call(t, ts, "POST", "/api/maintenance", body, nil); code != 400 {
		t.Fatalf("nodeId 0: got %d, want 400", code)
	}
	if ws, _ := srv.Store.ListMaintenance(context.Background()); len(ws) != 0 {
		t.Fatalf("a window was saved: %+v", ws)
	}
}

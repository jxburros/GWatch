package api

import (
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

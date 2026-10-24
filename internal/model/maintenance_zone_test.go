package model

import (
	"testing"
	"time"
)

func TestWeeklyMaintenanceUsesItsZoneAcrossDST(t *testing.T) {
	w := MaintenanceWindow{Enabled: true, Weekdays: []int{0}, StartTime: "22:00", TimeZone: "America/New_York", DurationMinutes: 60}
	for _, tc := range []struct {
		at     string
		active bool
	}{
		{"2026-07-06T02:30:00Z", true}, {"2026-07-05T02:30:00Z", false},
		{"2026-12-07T03:30:00Z", true}, {"2026-12-07T02:30:00Z", false},
		{"2026-07-06T03:00:00Z", false},
		{"2026-03-09T02:30:00Z", true}, {"2026-11-02T03:30:00Z", true},
	} {
		at, err := time.Parse(time.RFC3339, tc.at)
		if err != nil {
			t.Fatal(err)
		}
		for _, zone := range []string{"UTC", "Asia/Tokyo", "America/Los_Angeles"} {
			loc, err := time.LoadLocation(zone)
			if err != nil {
				t.Fatal(err)
			}
			if got := w.Active(at.In(loc)); got != tc.active {
				t.Errorf("%s viewed in %s: %v", tc.at, zone, got)
			}
		}
	}
	w.Weekdays = []int{6}
	w.DurationMinutes = 48 * 60
	monday, _ := time.Parse(time.RFC3339, "2026-07-07T00:00:00Z")
	if !w.Active(monday) {
		t.Fatal("Saturday's 48-hour window cut short on Monday")
	}
}

// A weekly window at a wall time the clocks skip on the night they go
// forward covers the hour after the jump, not the hour before it.
func TestWeeklyMaintenanceInTheSkippedHourStartsAfterTheJump(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	// 2026-03-08: New York goes from 02:00 EST straight to 03:00 EDT.
	w := MaintenanceWindow{Enabled: true, Weekdays: []int{0}, StartTime: "02:00", TimeZone: "America/New_York", DurationMinutes: 60}
	for _, tc := range []struct {
		at     time.Time
		active bool
	}{
		{time.Date(2026, 3, 8, 1, 30, 0, 0, ny), false},
		{time.Date(2026, 3, 8, 3, 0, 0, 0, ny), true},
		{time.Date(2026, 3, 8, 3, 45, 0, 0, ny), true},
		{time.Date(2026, 3, 8, 4, 0, 0, 0, ny), false},
	} {
		if got := w.Active(tc.at); got != tc.active {
			t.Errorf("%s: active %v, want %v", tc.at, got, tc.active)
		}
	}
}

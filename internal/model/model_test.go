package model

import (
	"encoding/json"
	"testing"
	"time"
)

func TestCheckTypeValid(t *testing.T) {
	for _, k := range AllCheckTypes {
		if !k.Valid() {
			t.Errorf("%s should be valid", k)
		}
		if k.Label() == "" || k.Label() == string(k) {
			t.Errorf("%s has no human label", k)
		}
	}
	for _, bad := range []CheckType{"", "smtp", "PING", "telnet"} {
		if bad.Valid() {
			t.Errorf("%q should not be valid", bad)
		}
	}
	if got := CheckType("telnet").Label(); got != "telnet" {
		t.Errorf("unknown label = %q, want passthrough", got)
	}
}

func TestStatusSeverityOrder(t *testing.T) {
	// Worst-to-best. The engine relies on this order when rolling check
	// statuses up into a node status.
	order := []Status{StatusDown, StatusDegraded, StatusUnknown, StatusMaintenance, StatusUp, StatusPaused}
	for i := 1; i < len(order); i++ {
		if order[i-1].Severity() <= order[i].Severity() {
			t.Fatalf("%s (%d) should outrank %s (%d)", order[i-1], order[i-1].Severity(), order[i], order[i].Severity())
		}
	}
	if got := Status("bogus").Severity(); got != StatusUnknown.Severity() {
		t.Errorf("unknown status severity = %d, want %d", got, StatusUnknown.Severity())
	}
}

func TestWorst(t *testing.T) {
	cases := []struct{ a, b, want Status }{
		{StatusUp, StatusDown, StatusDown},
		{StatusDown, StatusUp, StatusDown},
		{StatusUp, StatusDegraded, StatusDegraded},
		{StatusPaused, StatusMaintenance, StatusMaintenance},
		{StatusUp, StatusUp, StatusUp},
		{StatusDown, StatusDegraded, StatusDown},
		{StatusUnknown, StatusUp, StatusUnknown},
	}
	for _, c := range cases {
		if got := Worst(c.a, c.b); got != c.want {
			t.Errorf("Worst(%s, %s) = %s, want %s", c.a, c.b, got, c.want)
		}
	}
}

func TestCheckTarget(t *testing.T) {
	cases := []struct {
		name     string
		target   string
		nodeHost string
		want     string
	}{
		{"check target wins", "10.0.0.1", "node.example", "10.0.0.1"},
		{"falls back to node host", "", "node.example", "node.example"},
		{"blank target falls back", "   ", "node.example", "node.example"},
		{"both trimmed", " 10.0.0.1 ", " node.example ", "10.0.0.1"},
		{"node host trimmed", "", " node.example ", "node.example"},
		{"nothing set", "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			chk := Check{Config: CheckConfig{Target: c.target}}
			if got := chk.Target(c.nodeHost); got != c.want {
				t.Errorf("Target(%q) = %q, want %q", c.nodeHost, got, c.want)
			}
		})
	}
}

func TestMaintenanceWindowActiveAbsolute(t *testing.T) {
	start := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	w := MaintenanceWindow{Enabled: true, StartAt: start, EndAt: start.Add(2 * time.Hour)}

	if w.Active(start.Add(-time.Minute)) {
		t.Error("active before start")
	}
	if !w.Active(start) {
		t.Error("start is inclusive")
	}
	if !w.Active(start.Add(90 * time.Minute)) {
		t.Error("inside window")
	}
	if w.Active(start.Add(2 * time.Hour)) {
		t.Error("end is exclusive")
	}

	w.Enabled = false
	if w.Active(start.Add(time.Hour)) {
		t.Error("disabled window must never be active")
	}
}

func TestMaintenanceWindowActiveRecurring(t *testing.T) {
	// Recurring Tuesday/Thursday 22:00 for 3 hours, so it crosses midnight.
	w := MaintenanceWindow{
		Enabled:         true,
		StartAt:         time.Date(2026, 3, 3, 22, 0, 0, 0, time.UTC), // a Tuesday
		Weekdays:        []int{int(time.Tuesday), int(time.Thursday)},
		DurationMinutes: 180,
	}
	at := func(day, hour, min int) time.Time {
		return time.Date(2026, 3, day, hour, min, 0, 0, time.UTC)
	}
	cases := []struct {
		name string
		when time.Time
		want bool
	}{
		{"tuesday inside", at(10, 22, 30), true}, // Tue 2026-03-10
		{"tuesday at start", at(10, 22, 0), true},
		{"tuesday before start", at(10, 21, 59), false},
		{"wednesday spillover", at(11, 0, 30), true}, // still inside Tuesday's window
		{"wednesday after end", at(11, 1, 30), false},
		{"wednesday evening", at(11, 22, 30), false}, // Wednesday is not a listed weekday
		{"thursday inside", at(12, 23, 0), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := w.Active(c.when); got != c.want {
				t.Errorf("Active(%s, %s) = %v, want %v", c.when.Weekday(), c.when.Format("15:04"), got, c.want)
			}
		})
	}
}

func TestMaintenanceWindowRecurringDefaultDuration(t *testing.T) {
	// DurationMinutes unset falls back to one hour.
	w := MaintenanceWindow{
		Enabled:  true,
		StartAt:  time.Date(2026, 3, 3, 9, 0, 0, 0, time.UTC),
		Weekdays: []int{int(time.Tuesday)},
	}
	if !w.Active(time.Date(2026, 3, 10, 9, 59, 0, 0, time.UTC)) {
		t.Error("want active 59 minutes in")
	}
	if w.Active(time.Date(2026, 3, 10, 10, 0, 0, 0, time.UTC)) {
		t.Error("want inactive after one hour")
	}
}

func TestDefaultSettings(t *testing.T) {
	s := DefaultSettings()
	if s.General.MinIntervalSecs <= 0 || s.General.DefaultIntervalSecs < s.General.MinIntervalSecs {
		t.Errorf("interval defaults inconsistent: %+v", s.General)
	}
	if s.General.DefaultTimeoutSecs <= 0 || s.General.MaxConcurrentChecks <= 0 {
		t.Errorf("bad general defaults: %+v", s.General)
	}
	if s.Alerts.Enabled {
		t.Error("alerts must be off until the user configures SMTP")
	}
	if s.Alerts.Recipients == nil {
		t.Error("recipients should be an empty slice so it marshals as [] not null")
	}
	if s.Alerts.FailureThreshold < 1 || s.Alerts.CooldownMinutes <= 0 || s.Alerts.CertWarnDays <= 0 {
		t.Errorf("bad alert defaults: %+v", s.Alerts)
	}
	if s.Alerts.SMTP.Port != 587 || s.Alerts.SMTP.Security != "starttls" {
		t.Errorf("bad SMTP defaults: %+v", s.Alerts.SMTP)
	}
	if s.Retention.RawDays <= 0 || s.Retention.FiveMinDays < s.Retention.RawDays || s.Retention.HourlyDays < s.Retention.FiveMinDays {
		t.Errorf("retention tiers should widen: %+v", s.Retention)
	}
	if s.Retention.DailyDays != 0 {
		t.Errorf("daily rollups should be kept forever, got %d", s.Retention.DailyDays)
	}
}

// The JSON tags are the contract with web/app.js, so a rename must break a test.
func TestNodeJSONWireFormat(t *testing.T) {
	parent := int64(7)
	n := Node{ID: 1, Name: "NAS", Host: "10.0.0.5", Importance: ImportanceHigh, Enabled: true, DependsOnNode: &parent}
	b, err := json.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"id", "name", "host", "group", "tags", "notes", "importance", "enabled", "dependsOnNodeId", "template", "createdAt", "updatedAt", "checks"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("Node JSON missing %q: %s", key, b)
		}
	}
	if raw["dependsOnNodeId"] != float64(7) {
		t.Errorf("dependsOnNodeId = %v, want 7", raw["dependsOnNodeId"])
	}
}

func TestResultJSONOmitsEmptyDiagnostics(t *testing.T) {
	lat := 12.5
	r := Result{CheckID: 3, Success: true, Status: StatusUp, LatencyMS: &lat}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	// Always present: the UI reads these unconditionally.
	for _, key := range []string{"checkId", "ts", "success", "status", "message", "latencyMs", "details"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("Result JSON missing %q: %s", key, b)
		}
	}
	// Omitted when unset so results stay small in the history payloads.
	for _, key := range []string{"error", "minMs", "maxMs", "jitterMs", "lossPct", "warnings"} {
		if _, ok := raw[key]; ok {
			t.Errorf("Result JSON should omit empty %q: %s", key, b)
		}
	}
	if raw["latencyMs"] != 12.5 {
		t.Errorf("latencyMs = %v, want 12.5", raw["latencyMs"])
	}
}

func TestResultRoundTrip(t *testing.T) {
	lat, loss := 9.5, 25.0
	found := true
	in := Result{
		ID: 4, CheckID: 5, Timestamp: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
		Success: false, Status: StatusDegraded, Message: "slow", Error: "timeout",
		LatencyMS: &lat, LossPct: &loss, Attempts: 2, Warnings: []string{"latency"},
		Details: ResultDetails{StatusCode: 200, KeywordFound: &found, ResolvedValues: []string{"1.2.3.4"}},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Result
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Status != in.Status || out.Error != in.Error || out.Attempts != in.Attempts {
		t.Errorf("round trip lost scalars: %+v", out)
	}
	if out.LatencyMS == nil || *out.LatencyMS != lat || out.LossPct == nil || *out.LossPct != loss {
		t.Errorf("round trip lost metrics: %+v", out)
	}
	if out.Details.KeywordFound == nil || !*out.Details.KeywordFound {
		t.Errorf("round trip lost keywordFound: %+v", out.Details)
	}
	if len(out.Details.ResolvedValues) != 1 || out.Details.ResolvedValues[0] != "1.2.3.4" {
		t.Errorf("round trip lost resolvedValues: %+v", out.Details)
	}
	if !out.Timestamp.Equal(in.Timestamp) {
		t.Errorf("round trip lost timestamp: %s", out.Timestamp)
	}
}

func TestAlertOverrideNilMeansInherit(t *testing.T) {
	var o AlertOverride
	b, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "{}" {
		t.Errorf("empty override = %s, want {} so globals are inherited", b)
	}
	yes := true
	o.Enabled = &yes
	b, _ = json.Marshal(o)
	if string(b) != `{"enabled":true}` {
		t.Errorf("override = %s", b)
	}
}

package reports

import (
	"context"
	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store/storetest"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"
)

func TestCalendarReportPeriods(t *testing.T) {
	old := time.Local
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	time.Local = loc
	t.Cleanup(func() { time.Local = old })
	from, to := Period(time.Date(2026, 3, 9, 10, 0, 0, 0, loc), "weekly")
	if from.Hour() != 0 || to.Hour() != 0 || to.Sub(from) != 167*time.Hour {
		t.Fatalf("DST week %v to %v", from, to)
	}
	from, to = Period(time.Date(2026, 4, 5, 10, 0, 0, 0, loc), "monthly")
	if from.Day() != 1 || from.Month() != 3 || to.Day() != 1 || to.Month() != 4 {
		t.Fatalf("month %v to %v", from, to)
	}
}
func TestReportAvailabilityScopeAndEscaping(t *testing.T) {
	s := storetest.Open(t)
	ctx := context.Background()
	n, err := s.CreateNode(ctx, model.Node{Name: "<script>alert(1)</script>", Groups: []string{"Office"}, Enabled: true, Checks: []model.Check{{Name: "HTTP", Type: model.CheckHTTP, Enabled: true}}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for i, status := range []model.Status{model.StatusDown, model.StatusUp} {
		_, err := s.RecordResult(ctx, model.Result{CheckID: n.Checks[0].ID, Timestamp: now.Add(time.Duration(i-2) * time.Minute), Status: status, Success: status == model.StatusUp}, model.CheckState{CheckID: n.Checks[0].ID, Status: status})
		if err != nil {
			t.Fatal(err)
		}
	}
	html, err := Generate(ctx, s, model.ReportDefinition{Groups: []string{"Office"}, TargetAvailability: 99.9}, now.Add(-time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, "50.000%") || !strings.Contains(html, "Below target") || strings.Contains(html, "<script>") || !strings.Contains(html, "&lt;script&gt;") {
		t.Fatal("wrong availability or unescaped node name")
	}
	html, err = Generate(ctx, s, model.ReportDefinition{Groups: []string{"Elsewhere"}}, now.Add(-time.Hour), now)
	if err != nil || !strings.Contains(html, "No nodes in this scope") {
		t.Fatal("scope ignored")
	}
}

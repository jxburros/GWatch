package store

import (
	"context"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/jxburros/GWatch/internal/model"
)

func TestReportSummaryDoesNotDoubleCountOverlappingRetention(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	n, err := s.CreateNode(ctx, model.Node{Name: "Report", Enabled: true, Checks: []model.Check{{Type: model.CheckPing, Name: "Ping", Enabled: true}}})
	if err != nil {
		t.Fatal(err)
	}
	id := n.Checks[0].ID
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(20 * time.Minute)
	for _, r := range []model.Result{{CheckID: id, Timestamp: start.Add(10 * time.Minute), Success: true}, {CheckID: id, Timestamp: start.Add(15 * time.Minute), Success: false}, {CheckID: id, Timestamp: end, Success: false}} {
		if _, err := s.InsertResult(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.InsertRollupsBatch(ctx, []model.Rollup{{CheckID: id, BucketSecs: Bucket5m, BucketStart: start, Count: 3, SuccessCount: 2, FailCount: 1}, {CheckID: id, BucketSecs: Bucket5m, BucketStart: start.Add(10 * time.Minute), Count: 99, SuccessCount: 99}, {CheckID: id, BucketSecs: Bucket1h, BucketStart: start, Count: 999, SuccessCount: 999}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReportSummary(ctx, id, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if got.Count != 5 || got.Failures != 2 || got.Availability != 60 {
		t.Fatalf("double counting or end-boundary sample: %+v", got)
	}
}

func TestReportSummaryIncludesFullDSTDay(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = old })
	s := openTest(t)
	ctx := context.Background()
	n, err := s.CreateNode(ctx, model.Node{Name: "DST", Checks: []model.Check{{Type: model.CheckPing, Name: "Ping"}}})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 3, 8, 0, 0, 0, 0, loc)
	end := start.AddDate(0, 0, 1)
	if err := s.InsertRollupsBatch(ctx, []model.Rollup{{CheckID: n.Checks[0].ID, BucketSecs: Bucket1d, BucketStart: start, Count: 23, SuccessCount: 23}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReportSummary(ctx, n.Checks[0].ID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if got.Count != 23 {
		t.Fatalf("23-hour day excluded: %+v", got)
	}
}

package store

import (
	"context"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/jxburros/GWatch/internal/model"
)

func TestDailyRollupsUseLocalDaysAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = old })
	for _, tc := range []struct {
		name  string
		day   time.Time
		hours int
	}{{"spring", time.Date(2026, 3, 8, 0, 0, 0, 0, loc), 23}, {"fall", time.Date(2026, 11, 1, 0, 0, 0, 0, loc), 25}} {
		t.Run(tc.name, func(t *testing.T) {
			s := openTest(t)
			ctx := context.Background()
			n, err := s.CreateNode(ctx, model.Node{Name: "DST", Host: "localhost", Enabled: true, Checks: []model.Check{{Name: "Ping", Type: model.CheckPing, Enabled: true}}})
			if err != nil {
				t.Fatal(err)
			}
			id := n.Checks[0].ID
			var rows []model.Rollup
			for hour := 0; hour < tc.hours; hour++ {
				rows = append(rows, model.Rollup{CheckID: id, BucketSecs: Bucket1h, BucketStart: tc.day.Add(time.Duration(hour) * time.Hour), Count: 1, SuccessCount: 1, Availability: 100})
			}
			legacyStart := tc.day.UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
			legacyCount := int(tc.day.AddDate(0, 0, 1).Sub(legacyStart) / time.Hour)
			rows = append(rows, model.Rollup{CheckID: id, BucketSecs: Bucket1d, BucketStart: legacyStart, Count: legacyCount, SuccessCount: legacyCount, Availability: 100})
			if err := s.InsertRollupsBatch(ctx, rows); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if _, err := s.RollupUp(ctx, Bucket1h, Bucket1d, tc.day, tc.day.AddDate(0, 0, 1)); err != nil {
					t.Fatal(err)
				}
			}
			got, err := s.RollupsBetween(ctx, id, Bucket1d, tc.day.Add(-time.Second), tc.day.AddDate(0, 0, 1))
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].Count != tc.hours || !got[0].BucketStart.Equal(tc.day) {
				t.Fatalf("wrong daily buckets: %+v", got)
			}
		})
	}
}

func TestRetentionFragmentsDoNotOverwriteArchivedRollups(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	n, err := s.CreateNode(ctx, model.Node{Name: "Retention", Checks: []model.Check{{Name: "Ping", Type: model.CheckPing}}})
	if err != nil {
		t.Fatal(err)
	}
	id := n.Checks[0].ID
	day := localDay(time.Now().AddDate(0, 0, -60))
	// Complete historical hourly/daily summaries coexist with only the last
	// five minutes of the old fine tier. Rebuilding must preserve both counts
	// and failure/latency information in the archived tiers.
	latency := 12.0
	rows := []model.Rollup{
		{CheckID: id, BucketSecs: Bucket1d, BucketStart: day, Count: 288, SuccessCount: 280, FailCount: 8, AvgMS: &latency, Availability: 100.0 * 280 / 288},
		{CheckID: id, BucketSecs: Bucket1h, BucketStart: day.Add(23 * time.Hour), Count: 12, SuccessCount: 10, FailCount: 2, AvgMS: &latency, Availability: 100.0 * 10 / 12},
		{CheckID: id, BucketSecs: Bucket5m, BucketStart: day.Add(23*time.Hour + 55*time.Minute), Count: 1, SuccessCount: 1, Availability: 100},
	}
	if err := s.InsertRollupsBatch(ctx, rows); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]int{{Bucket5m, Bucket1h}, {Bucket1h, Bucket1d}, {Bucket5m, Bucket1d}} {
		if _, err := s.RollupUp(ctx, pair[0], pair[1], day, day.AddDate(0, 0, 1)); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ size, count, fail int }{{Bucket1h, 12, 2}, {Bucket1d, 288, 8}} {
		got, err := s.RollupsBetween(ctx, id, tc.size, day, day.AddDate(0, 0, 1))
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Count != tc.count || got[0].FailCount != tc.fail || got[0].AvgMS == nil || *got[0].AvgMS != latency {
			t.Fatalf("retained %d tier overwritten: %+v", tc.size, got)
		}
	}
}

func TestRawRetentionFragmentKeepsCompleteFiveMinuteBucket(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	n, err := s.CreateNode(ctx, model.Node{Name: "Raw retention", Checks: []model.Check{{Name: "Ping", Type: model.CheckPing}}})
	if err != nil {
		t.Fatal(err)
	}
	id := n.Checks[0].ID
	start := time.Unix(1800000000, 0).UTC()
	if err := s.InsertRollupsBatch(ctx, []model.Rollup{{CheckID: id, BucketSecs: Bucket5m, BucketStart: start, Count: 5, SuccessCount: 3, FailCount: 2, Availability: 60}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertResult(ctx, model.Result{CheckID: id, Timestamp: start.Add(4 * time.Minute), Success: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RollupFromRaw(ctx, start, start.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, err := s.RollupsBetween(ctx, id, Bucket5m, start, start.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Count != 5 || got[0].FailCount != 2 {
		t.Fatalf("raw fragment overwrote history: %+v", got)
	}
}

func TestLegacyDailyRetentionBoundaryPreservesSamples(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = old })
	s := openTest(t)
	ctx := context.Background()
	n, err := s.CreateNode(ctx, model.Node{Name: "Legacy", Checks: []model.Check{{Name: "Ping", Type: model.CheckPing}}})
	if err != nil {
		t.Fatal(err)
	}
	id := n.Checks[0].ID
	legacy := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	latency := 12.0
	rows := []model.Rollup{
		{CheckID: id, BucketSecs: Bucket1d, BucketStart: legacy, Count: 24, SuccessCount: 22, FailCount: 2, AvgMS: &latency, Availability: 100.0 * 22 / 24},
		{CheckID: id, BucketSecs: Bucket1h, BucketStart: legacy.Add(23 * time.Hour), Count: 1, SuccessCount: 1, Availability: 100},
		// This new sample is outside the retained UTC archive and must still
		// contribute to the local day without duplicating the archived sample.
		{CheckID: id, BucketSecs: Bucket1h, BucketStart: legacy.Add(24 * time.Hour), Count: 1, SuccessCount: 1, Availability: 100},
	}
	if err := s.InsertRollupsBatch(ctx, rows); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := s.RollupUp(ctx, Bucket1h, Bucket1d, legacy.Add(-24*time.Hour), legacy.Add(48*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.RollupsBetween(ctx, id, Bucket1d, legacy.Add(-24*time.Hour), legacy.Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	total, failures := 0, 0
	preserved := false
	for _, row := range got {
		total += row.Count
		failures += row.FailCount
		if row.BucketStart.Equal(legacy) {
			preserved = row.Count == 24 && row.AvgMS != nil && *row.AvgMS == latency
		}
	}
	if !preserved || total != 25 || failures != 2 {
		t.Fatalf("legacy archive lost or counted twice: %+v", got)
	}
}

func TestLegacyDailyCompleteSourceAtRangeStart(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = old })
	s := openTest(t)
	ctx := context.Background()
	n, err := s.CreateNode(ctx, model.Node{Name: "Range", Checks: []model.Check{{Name: "Ping", Type: model.CheckPing}}})
	if err != nil {
		t.Fatal(err)
	}
	id := n.Checks[0].ID
	legacy := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	// The old UTC bucket begins before the requested local day, but its
	// only sample is inside that day and can be completely rebuilt.
	rows := []model.Rollup{
		{CheckID: id, BucketSecs: Bucket1d, BucketStart: legacy, Count: 1, SuccessCount: 1, Availability: 100},
		{CheckID: id, BucketSecs: Bucket1h, BucketStart: legacy.Add(6 * time.Hour), Count: 1, SuccessCount: 1, Availability: 100},
	}
	if err := s.InsertRollupsBatch(ctx, rows); err != nil {
		t.Fatal(err)
	}
	from := legacy.Add(5 * time.Hour)
	if _, err := s.RollupUp(ctx, Bucket1h, Bucket1d, from, from.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	got, err := s.RollupsBetween(ctx, id, Bucket1d, legacy, legacy.Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Count != 1 || !got[0].BucketStart.Equal(from) {
		t.Fatalf("legacy range-start samples duplicated or lost: %+v", got)
	}
}

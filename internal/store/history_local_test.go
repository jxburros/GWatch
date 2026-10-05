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
			rows = append(rows, model.Rollup{CheckID: id, BucketSecs: Bucket1d, BucketStart: tc.day.Add(19 * time.Hour).UTC().Truncate(24 * time.Hour), Count: 999, SuccessCount: 999, Availability: 100})
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

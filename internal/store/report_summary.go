package store

import (
	"context"
	"github.com/jxburros/GWatch/internal/model"
	"time"
)

// ReportSummary combines retained raw readings with progressively coarser
// older buckets. No sample is counted twice and partial retained buckets are
// excluded rather than presenting measurements outside the requested range.
func (s *Store) ReportSummary(ctx context.Context, id int64, from, to time.Time) (model.HistorySummary, error) {
	points := []model.HistoryPoint{}
	cutoff := to
	raw, err := s.ResultsBetween(ctx, id, from, to)
	if err != nil {
		return model.HistorySummary{}, err
	}
	for _, r := range raw {
		if !r.Timestamp.Before(to) {
			continue
		}
		p := model.HistoryPoint{Count: 1, Timestamp: r.Timestamp}
		if r.Success {
			p.Availability = 100
			p.AvgMS = r.LatencyMS
			p.MinMS = r.LatencyMS
			p.MaxMS = r.LatencyMS
		} else {
			p.Failures = 1
		}
		points = append(points, p)
		if r.Timestamp.Before(cutoff) {
			cutoff = r.Timestamp
		}
	}
	for _, size := range []int{Bucket5m, Bucket1h, Bucket1d} {
		rows, err := s.RollupsBetween(ctx, id, size, from, cutoff)
		if err != nil {
			return model.HistorySummary{}, err
		}
		next := cutoff
		for _, r := range rows {
			end := r.BucketStart.Add(time.Duration(size) * time.Second)
			if size == Bucket1d {
				if localDay(r.BucketStart).Equal(r.BucketStart) {
					end = r.BucketStart.In(time.Local).AddDate(0, 0, 1)
				}
			}
			if end.After(cutoff) {
				continue
			}
			points = append(points, model.HistoryPoint{Timestamp: r.BucketStart, Count: r.Count, Failures: r.FailCount, Availability: r.Availability, AvgMS: r.AvgMS, MinMS: r.MinMS, MaxMS: r.MaxMS})
			if r.BucketStart.Before(next) {
				next = r.BucketStart
			}
		}
		cutoff = next
	}
	return summarize(points), nil
}

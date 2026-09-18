package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// Bucket sizes in seconds.
const (
	Bucket5m = 300
	Bucket1h = 3600
	Bucket1d = 86400
)

// RollupFromRaw (re)computes 5-minute buckets from raw results whose
// timestamp is in [from, to). Buckets are upserted so the call is idempotent.
func (s *Store) RollupFromRaw(ctx context.Context, from, to time.Time) (int64, error) {
	fromB := from.Unix() - from.Unix()%Bucket5m
	toB := to.Unix() - to.Unix()%Bucket5m + Bucket5m
	res, err := s.Exec(ctx, `
		INSERT INTO rollups(check_id, bucket_seconds, bucket_start, count, success_count, fail_count, min_ms, max_ms, avg_ms, avg_jitter_ms, avg_loss_pct, availability)
		SELECT check_id, ?, (ts/1000) - ((ts/1000) % ?) AS b,
		       COUNT(*), SUM(success), COUNT(*) - SUM(success),
		       MIN(CASE WHEN success = 1 THEN COALESCE(min_ms, latency_ms) END),
		       MAX(CASE WHEN success = 1 THEN COALESCE(max_ms, latency_ms) END),
		       AVG(CASE WHEN success = 1 THEN latency_ms END),
		       AVG(jitter_ms), AVG(loss_pct),
		       100.0 * SUM(success) / COUNT(*)
		FROM results WHERE ts >= ? AND ts < ?
		GROUP BY check_id, b
		ON CONFLICT(check_id, bucket_seconds, bucket_start) DO UPDATE SET
		  count=excluded.count, success_count=excluded.success_count, fail_count=excluded.fail_count,
		  min_ms=excluded.min_ms, max_ms=excluded.max_ms, avg_ms=excluded.avg_ms, avg_jitter_ms=excluded.avg_jitter_ms,
		  avg_loss_pct=excluded.avg_loss_pct, availability=excluded.availability`,
		Bucket5m, Bucket5m, fromB*1000, toB*1000)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// RollupUp aggregates smaller buckets into larger ones for [from, to).
func (s *Store) RollupUp(ctx context.Context, srcBucket, dstBucket int, from, to time.Time) (int64, error) {
	fromB := from.Unix() - from.Unix()%int64(dstBucket)
	toB := to.Unix() - to.Unix()%int64(dstBucket) + int64(dstBucket)
	res, err := s.Exec(ctx, `
		INSERT INTO rollups(check_id, bucket_seconds, bucket_start, count, success_count, fail_count, min_ms, max_ms, avg_ms, avg_jitter_ms, avg_loss_pct, availability)
		SELECT check_id, ?, bucket_start - (bucket_start % ?) AS b,
		       SUM(count), SUM(success_count), SUM(fail_count),
		       MIN(min_ms), MAX(max_ms),
		       CASE WHEN SUM(CASE WHEN avg_ms IS NOT NULL THEN success_count ELSE 0 END) > 0
		            THEN SUM(avg_ms * success_count) / SUM(CASE WHEN avg_ms IS NOT NULL THEN success_count ELSE 0 END) END,
		       CASE WHEN SUM(CASE WHEN avg_jitter_ms IS NOT NULL THEN count ELSE 0 END) > 0
		            THEN SUM(avg_jitter_ms * count) / SUM(CASE WHEN avg_jitter_ms IS NOT NULL THEN count ELSE 0 END) END,
		       CASE WHEN SUM(CASE WHEN avg_loss_pct IS NOT NULL THEN count ELSE 0 END) > 0
		            THEN SUM(avg_loss_pct * count) / SUM(CASE WHEN avg_loss_pct IS NOT NULL THEN count ELSE 0 END) END,
		       100.0 * SUM(success_count) / SUM(count)
		FROM rollups WHERE bucket_seconds = ? AND bucket_start >= ? AND bucket_start < ?
		GROUP BY check_id, b
		ON CONFLICT(check_id, bucket_seconds, bucket_start) DO UPDATE SET
		  count=excluded.count, success_count=excluded.success_count, fail_count=excluded.fail_count,
		  min_ms=excluded.min_ms, max_ms=excluded.max_ms, avg_ms=excluded.avg_ms, avg_jitter_ms=excluded.avg_jitter_ms,
		  avg_loss_pct=excluded.avg_loss_pct, availability=excluded.availability`,
		dstBucket, dstBucket, srcBucket, fromB, toB)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// DeleteResultsBefore removes raw results older than t.
func (s *Store) DeleteResultsBefore(ctx context.Context, t time.Time) (int64, error) {
	res, err := s.Exec(ctx, "DELETE FROM results WHERE ts < ?", t.UnixMilli())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// DeleteRollupsBefore removes buckets of a size older than t.
func (s *Store) DeleteRollupsBefore(ctx context.Context, bucket int, t time.Time) (int64, error) {
	res, err := s.Exec(ctx, "DELETE FROM rollups WHERE bucket_seconds = ? AND bucket_start < ?", bucket, t.Unix())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// DeleteEventsBefore removes timeline entries older than t.
func (s *Store) DeleteEventsBefore(ctx context.Context, t time.Time) (int64, error) {
	res, err := s.Exec(ctx, "DELETE FROM events WHERE ts < ?", t.UnixMilli())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// OldestResult returns the timestamp of the oldest raw result.
func (s *Store) OldestResult(ctx context.Context) (*time.Time, error) {
	var ts sql.NullInt64
	if err := s.reader.QueryRowContext(ctx, "SELECT MIN(ts) FROM results").Scan(&ts); err != nil {
		return nil, err
	}
	if !ts.Valid {
		return nil, nil
	}
	t := time.UnixMilli(ts.Int64).Local()
	return &t, nil
}

// Counts returns row counts used by Monitor Health.
func (s *Store) Counts(ctx context.Context) (raw, r5m, r1h, r1d, events int64, err error) {
	q := func(query string, args ...any) (int64, error) {
		var n int64
		err := s.reader.QueryRowContext(ctx, query, args...).Scan(&n)
		return n, err
	}
	if raw, err = q("SELECT COUNT(*) FROM results"); err != nil {
		return
	}
	if r5m, err = q("SELECT COUNT(*) FROM rollups WHERE bucket_seconds = ?", Bucket5m); err != nil {
		return
	}
	if r1h, err = q("SELECT COUNT(*) FROM rollups WHERE bucket_seconds = ?", Bucket1h); err != nil {
		return
	}
	if r1d, err = q("SELECT COUNT(*) FROM rollups WHERE bucket_seconds = ?", Bucket1d); err != nil {
		return
	}
	events, err = q("SELECT COUNT(*) FROM events")
	return
}

// RollupsBetween returns buckets of a size for a check in [from, to).
func (s *Store) RollupsBetween(ctx context.Context, checkID int64, bucket int, from, to time.Time) ([]model.Rollup, error) {
	rows, err := s.reader.QueryContext(ctx, `SELECT check_id, bucket_seconds, bucket_start, count, success_count, fail_count, min_ms, max_ms, avg_ms, avg_jitter_ms, avg_loss_pct, availability
		FROM rollups WHERE check_id = ? AND bucket_seconds = ? AND bucket_start >= ? AND bucket_start < ? ORDER BY bucket_start`, checkID, bucket, from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Rollup{}
	for rows.Next() {
		var r model.Rollup
		var start int64
		var min, max, avg, jit, loss sql.NullFloat64
		if err := rows.Scan(&r.CheckID, &r.BucketSecs, &start, &r.Count, &r.SuccessCount, &r.FailCount, &min, &max, &avg, &jit, &loss, &r.Availability); err != nil {
			return nil, err
		}
		r.BucketStart = time.Unix(start, 0).Local()
		r.MinMS, r.MaxMS, r.AvgMS, r.AvgJitterMS, r.AvgLossPct = floatPtr(min), floatPtr(max), floatPtr(avg), floatPtr(jit), floatPtr(loss)
		out = append(out, r)
	}
	return out, rows.Err()
}

// RangeSpec describes a chart time range.
type RangeSpec struct {
	Name     string
	Duration time.Duration
	Bucket   int // 0 = raw
}

// ParseRange maps a range name to its spec.
func ParseRange(name string) (RangeSpec, error) {
	switch name {
	case "1h":
		return RangeSpec{"1h", time.Hour, 0}, nil
	case "24h", "":
		return RangeSpec{"24h", 24 * time.Hour, 0}, nil
	case "7d":
		return RangeSpec{"7d", 7 * 24 * time.Hour, Bucket5m}, nil
	case "30d":
		return RangeSpec{"30d", 30 * 24 * time.Hour, Bucket1h}, nil
	case "1y":
		return RangeSpec{"1y", 365 * 24 * time.Hour, Bucket1d}, nil
	}
	return RangeSpec{}, fmt.Errorf("unsupported range %q (use 1h, 24h, 7d, 30d, 1y)", name)
}

// History builds a chart series for a check over a range, choosing raw
// results or rollups depending on the range and on what is still retained.
func (s *Store) History(ctx context.Context, check model.Check, nodeName string, rng RangeSpec, now time.Time) (model.HistorySeries, error) {
	series := model.HistorySeries{
		CheckID:   check.ID,
		CheckName: check.Name,
		NodeName:  nodeName,
		CheckType: check.Type,
		Range:     rng.Name,
		From:      now.Add(-rng.Duration),
		To:        now,
		Points:    []model.HistoryPoint{},
	}
	bucket := rng.Bucket
	if bucket == 0 {
		// Raw results are preferred for short ranges. When retention has
		// already trimmed the raw data covering this window, the 5-minute
		// rollups reach further back than the raw rows, so use them instead.
		var minRaw, minRollup sql.NullInt64
		_ = s.reader.QueryRowContext(ctx, "SELECT MIN(ts)/1000 FROM results WHERE check_id = ? AND ts >= ?", check.ID, series.From.UnixMilli()).Scan(&minRaw)
		_ = s.reader.QueryRowContext(ctx, "SELECT MIN(bucket_start) FROM rollups WHERE check_id = ? AND bucket_seconds = ? AND bucket_start >= ?", check.ID, Bucket5m, series.From.Unix()).Scan(&minRollup)
		if minRollup.Valid && (!minRaw.Valid || minRollup.Int64+Bucket5m*2 < minRaw.Int64) {
			bucket = Bucket5m
		}
	}
	if bucket == 0 {
		series.Source = "raw"
		results, err := s.ResultsBetween(ctx, check.ID, series.From, now)
		if err != nil {
			return series, err
		}
		for _, r := range results {
			p := model.HistoryPoint{Timestamp: r.Timestamp, Count: 1, Availability: 100}
			if r.Success {
				p.AvgMS, p.MinMS, p.MaxMS = r.LatencyMS, r.MinMS, r.MaxMS
				if p.MinMS == nil {
					p.MinMS = r.LatencyMS
				}
				if p.MaxMS == nil {
					p.MaxMS = r.LatencyMS
				}
			} else {
				p.Failures = 1
				p.Availability = 0
			}
			p.JitterMS, p.LossPct = r.JitterMS, r.LossPct
			series.Points = append(series.Points, p)
		}
	} else {
		series.BucketSecs = bucket
		switch bucket {
		case Bucket5m:
			series.Source = "5m"
		case Bucket1h:
			series.Source = "1h"
		default:
			series.Source = "1d"
		}
		rollups, err := s.RollupsBetween(ctx, check.ID, bucket, series.From, now)
		if err != nil {
			return series, err
		}
		for _, r := range rollups {
			series.Points = append(series.Points, model.HistoryPoint{
				Timestamp: r.BucketStart, AvgMS: r.AvgMS, MinMS: r.MinMS, MaxMS: r.MaxMS, JitterMS: r.AvgJitterMS, LossPct: r.AvgLossPct,
				Availability: r.Availability, Count: r.Count, Failures: r.FailCount,
			})
		}
	}
	series.Summary = summarize(series.Points)
	return series, nil
}

func summarize(points []model.HistoryPoint) model.HistorySummary {
	var sum model.HistorySummary
	var latSum float64
	var latN int
	for _, p := range points {
		sum.Count += p.Count
		sum.Failures += p.Failures
		if p.AvgMS != nil {
			w := p.Count - p.Failures
			if w <= 0 {
				w = 1
			}
			latSum += *p.AvgMS * float64(w)
			latN += w
			if sum.MinMS == nil || (p.MinMS != nil && *p.MinMS < *sum.MinMS) {
				v := *p.AvgMS
				if p.MinMS != nil {
					v = *p.MinMS
				}
				sum.MinMS = &v
			}
			if sum.MaxMS == nil || (p.MaxMS != nil && *p.MaxMS > *sum.MaxMS) {
				v := *p.AvgMS
				if p.MaxMS != nil {
					v = *p.MaxMS
				}
				sum.MaxMS = &v
			}
		}
	}
	if latN > 0 {
		v := latSum / float64(latN)
		sum.AvgMS = &v
	}
	if sum.Count > 0 {
		sum.Availability = 100 * float64(sum.Count-sum.Failures) / float64(sum.Count)
	}
	return sum
}

// ---- bulk export / import (backups) ----

// AllResults streams every raw result to fn in id order.
func (s *Store) AllResults(ctx context.Context, fn func(model.Result) error) error {
	rows, err := s.reader.QueryContext(ctx, "SELECT "+resultCols+" FROM results ORDER BY id")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		r, err := scanResult(rows)
		if err != nil {
			return err
		}
		if err := fn(r); err != nil {
			return err
		}
	}
	return rows.Err()
}

// AllRollups streams every rollup to fn.
func (s *Store) AllRollups(ctx context.Context, fn func(model.Rollup) error) error {
	rows, err := s.reader.QueryContext(ctx, `SELECT check_id, bucket_seconds, bucket_start, count, success_count, fail_count, min_ms, max_ms, avg_ms, avg_jitter_ms, avg_loss_pct, availability FROM rollups ORDER BY check_id, bucket_seconds, bucket_start`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var r model.Rollup
		var start int64
		var min, max, avg, jit, loss sql.NullFloat64
		if err := rows.Scan(&r.CheckID, &r.BucketSecs, &start, &r.Count, &r.SuccessCount, &r.FailCount, &min, &max, &avg, &jit, &loss, &r.Availability); err != nil {
			return err
		}
		r.BucketStart = time.Unix(start, 0).UTC()
		r.MinMS, r.MaxMS, r.AvgMS, r.AvgJitterMS, r.AvgLossPct = floatPtr(min), floatPtr(max), floatPtr(avg), floatPtr(jit), floatPtr(loss)
		if err := fn(r); err != nil {
			return err
		}
	}
	return rows.Err()
}

// AllEvents streams every event to fn.
func (s *Store) AllEvents(ctx context.Context, fn func(model.Event) error) error {
	rows, err := s.reader.QueryContext(ctx, "SELECT "+eventCols+" FROM events ORDER BY id")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return err
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	return rows.Err()
}

// InsertRollupsBatch upserts rollups in one transaction.
func (s *Store) InsertRollupsBatch(ctx context.Context, rollups []model.Rollup) error {
	return s.WriteTx(ctx, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, `INSERT INTO rollups(check_id, bucket_seconds, bucket_start, count, success_count, fail_count, min_ms, max_ms, avg_ms, avg_jitter_ms, avg_loss_pct, availability)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(check_id, bucket_seconds, bucket_start) DO UPDATE SET count=excluded.count, success_count=excluded.success_count, fail_count=excluded.fail_count,
			min_ms=excluded.min_ms, max_ms=excluded.max_ms, avg_ms=excluded.avg_ms, avg_jitter_ms=excluded.avg_jitter_ms, avg_loss_pct=excluded.avg_loss_pct, availability=excluded.availability`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, r := range rollups {
			if _, err := stmt.ExecContext(ctx, r.CheckID, r.BucketSecs, r.BucketStart.Unix(), r.Count, r.SuccessCount, r.FailCount, nullFloat(r.MinMS), nullFloat(r.MaxMS), nullFloat(r.AvgMS), nullFloat(r.AvgJitterMS), nullFloat(r.AvgLossPct), r.Availability); err != nil {
				return err
			}
		}
		return nil
	})
}

// InsertResultsBatch inserts raw results in one transaction (ids are reassigned).
func (s *Store) InsertResultsBatch(ctx context.Context, results []model.Result) error {
	return s.WriteTx(ctx, func(tx *sql.Tx) error {
		for _, r := range results {
			if _, err := insertResultTx(ctx, tx, r); err != nil {
				return err
			}
		}
		return nil
	})
}

// InsertEventsBatch inserts events in one transaction (ids are reassigned).
func (s *Store) InsertEventsBatch(ctx context.Context, events []model.Event) error {
	return s.WriteTx(ctx, func(tx *sql.Tx) error {
		for _, e := range events {
			var meta any
			if len(e.Meta) > 0 {
				meta = string(e.Meta)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO events(ts, type, node_id, check_id, node_name, check_name, title, detail, meta) VALUES (?,?,?,?,?,?,?,?,?)`,
				e.Timestamp.UnixMilli(), string(e.Type), nullInt64(e.NodeID), nullInt64(e.CheckID), e.NodeName, e.CheckName, e.Title, e.Detail, meta); err != nil {
				return err
			}
		}
		return nil
	})
}

// ClearAll wipes every table (used before a restore).
func (s *Store) ClearAll(ctx context.Context, includeHistory bool) error {
	return s.WriteTx(ctx, func(tx *sql.Tx) error {
		tables := []string{"maintenance_windows", "dashboards", "check_state", "checks", "nodes", "settings"}
		if includeHistory {
			tables = append([]string{"results", "rollups", "events"}, tables...)
		}
		for _, t := range tables {
			if _, err := tx.ExecContext(ctx, "DELETE FROM "+t); err != nil {
				return err
			}
		}
		return nil
	})
}

// ClearHistory wipes results, rollups and events only.
func (s *Store) ClearHistory(ctx context.Context) error {
	return s.WriteTx(ctx, func(tx *sql.Tx) error {
		for _, t := range []string{"results", "rollups", "events"} {
			if _, err := tx.ExecContext(ctx, "DELETE FROM "+t); err != nil {
				return err
			}
		}
		return nil
	})
}

// CreateNodeWithID inserts a node keeping its original id (restore).
func (s *Store) CreateNodeWithID(ctx context.Context, n model.Node) error {
	if n.Tags == nil {
		n.Tags = []string{}
	}
	return s.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO nodes(id, name, host, group_name, tags, notes, importance, enabled, depends_on_node_id, template, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
			n.ID, n.Name, n.Host, n.Group, jsonString(n.Tags), n.Notes, string(n.Importance), boolInt(n.Enabled), nil, n.Template, fmtTime(n.CreatedAt), fmtTime(n.UpdatedAt)); err != nil {
			return err
		}
		for _, c := range n.Checks {
			var alerts any
			if c.Alerts != nil {
				alerts = jsonString(c.Alerts)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO checks(id, node_id, type, name, enabled, interval_seconds, timeout_seconds, retries, failure_threshold, config, alerts, sort_order, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				c.ID, n.ID, string(c.Type), c.Name, boolInt(c.Enabled), c.IntervalSeconds, c.TimeoutSeconds, c.Retries, c.FailureThreshold, jsonString(c.Config), alerts, c.SortOrder, fmtTime(c.CreatedAt), fmtTime(c.UpdatedAt)); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO check_state(check_id, status) VALUES (?, 'unknown')`, c.ID); err != nil {
				return err
			}
		}
		return nil
	})
}

// SetDependencies applies depends_on relations after all nodes exist (restore).
func (s *Store) SetDependencies(ctx context.Context, deps map[int64]int64) error {
	return s.WriteTx(ctx, func(tx *sql.Tx) error {
		for id, parent := range deps {
			if _, err := tx.ExecContext(ctx, "UPDATE nodes SET depends_on_node_id = ? WHERE id = ?", parent, id); err != nil {
				return err
			}
		}
		return nil
	})
}

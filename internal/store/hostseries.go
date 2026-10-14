package store

// What a machine's hardware check recorded, read back as the machine.
//
// A machine's readings are stored the way every other check's are: one result
// per run, in results, with the per-metric values in its metrics column and the
// whole snapshot in its details. There is no table of readings of their own, so
// there is nothing for retention, backups and restores to treat differently.
// These two reads put the machine back together from those results for the
// places that want a machine rather than a check: the node's hardware panel and
// its charts.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// LatestHostFromResults returns the newest hardware snapshot any of the checks
// recorded. It is what an agent's last reading is after a restart, when the
// copy GWatch keeps in memory has gone.
//
// A check that cannot reach its machine records a failure with no snapshot, so
// the newest result is not always the one to read; this finds the newest that
// has one.
func (s *Store) LatestHostFromResults(ctx context.Context, checkIDs []int64) (model.HostMetrics, bool, error) {
	var best model.HostMetrics
	found := false
	for _, id := range checkIDs {
		var details string
		err := s.queryRow(ctx, `SELECT details FROM results WHERE check_id = ? AND details LIKE ? ORDER BY id DESC LIMIT 1`,
			id, `%"host":{%`).Scan(&details)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return model.HostMetrics{}, false, err
		}
		var d model.ResultDetails
		if json.Unmarshal([]byte(details), &d) != nil || d.Host == nil {
			continue
		}
		if !found || d.Host.Timestamp.After(best.Timestamp) {
			best, found = *d.Host, true
		}
	}
	return best, found, nil
}

// hostSeriesFields is how many numeric series a HostSample carries.
const hostSeriesFields = 9

type hostBucket struct {
	n      int
	tsSum  int64
	sum    [hostSeriesFields]float64
	counts [hostSeriesFields]int
}

// HostSeries reads the numeric series for a machine over a range from the
// results of the checks that watch it, averaged into at most `buckets` points.
//
// It streams: a hardware result carries a whole snapshot, and a year of them is
// far more than should be held in memory to draw a chart of a few hundred
// points, so each row's small metrics column is folded into its bucket and let
// go. Averaging rather than sampling is what keeps a spike visible. A series a
// result does not carry stays missing from the bucket rather than being
// averaged in as a zero.
func (s *Store) HostSeries(ctx context.Context, checkIDs []int64, from, to time.Time, buckets int) ([]model.HostSample, error) {
	if buckets <= 0 {
		buckets = 600
	}
	span := to.Sub(from)
	if span <= 0 {
		return []model.HostSample{}, nil
	}
	acc := make([]hostBucket, buckets)
	for _, id := range checkIDs {
		rows, err := s.query(ctx, `SELECT ts, metrics FROM results WHERE check_id = ? AND ts >= ? AND ts <= ? AND metrics <> '' ORDER BY ts ASC`,
			id, from.UnixMilli(), to.UnixMilli())
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var ms int64
			var raw string
			if err := rows.Scan(&ms, &raw); err != nil {
				rows.Close()
				return nil, err
			}
			var m map[string]float64
			if json.Unmarshal([]byte(raw), &m) != nil || len(m) == 0 {
				continue
			}
			idx := int(float64(time.UnixMilli(ms).Sub(from)) / float64(span) * float64(buckets))
			if idx >= buckets {
				idx = buckets - 1
			}
			if idx < 0 {
				idx = 0
			}
			b := &acc[idx]
			b.n++
			b.tsSum += ms
			for i, v := range hostSeriesValues(m) {
				if v != nil {
					b.sum[i] += *v
					b.counts[i]++
				}
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}

	out := make([]model.HostSample, 0, buckets)
	for _, b := range acc {
		if b.n == 0 {
			continue
		}
		sample := model.HostSample{Timestamp: time.UnixMilli(b.tsSum / int64(b.n))}
		dst := []**float64{&sample.CPUPct, &sample.MemPct, &sample.SwapPct, &sample.DiskPct, &sample.LoadPerCore,
			&sample.NetRxBytesSec, &sample.NetTxBytesSec, &sample.DiskReadBytes, &sample.DiskWriteBytes}
		for i, p := range dst {
			if b.counts[i] > 0 {
				v := b.sum[i] / float64(b.counts[i])
				*p = &v
			}
		}
		out = append(out, sample)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp.Before(out[j].Timestamp) })
	return out, nil
}

// hostSeriesValues reduces a hardware result's metrics to the nine series a
// machine's charts draw, in the order of HostSample's fields: processor,
// memory, swap, the fullest disk, load per core, and the totals of network and
// disk throughput. A nil is a series the result does not have.
func hostSeriesValues(m map[string]float64) [hostSeriesFields]*float64 {
	var out [hostSeriesFields]*float64
	pick := func(key string) *float64 {
		if v, ok := m[key]; ok {
			return &v
		}
		return nil
	}
	out[0] = pick(model.MetricCPU)
	out[1] = pick(model.MetricMemory)
	out[2] = pick(model.MetricSwap)
	out[4] = pick(model.MetricLoad)

	var fullest *float64
	var rx, tx, rd, wr *float64
	add := func(dst **float64, v float64) {
		if *dst == nil {
			z := 0.0
			*dst = &z
		}
		**dst += v
	}
	for key, v := range m {
		family, instance := model.SplitMetricKey(key)
		switch family {
		case model.MetricDisk:
			if fullest == nil || v > *fullest {
				x := v
				fullest = &x
			}
		case model.MetricNet:
			switch {
			case strings.HasSuffix(instance, ".rx"):
				add(&rx, v)
			case strings.HasSuffix(instance, ".tx"):
				add(&tx, v)
			}
		case model.MetricDiskIO:
			switch {
			case strings.HasSuffix(instance, ".read"):
				add(&rd, v)
			case strings.HasSuffix(instance, ".write"):
				add(&wr, v)
			}
		}
	}
	out[3], out[5], out[6], out[7], out[8] = fullest, rx, tx, rd, wr
	return out
}

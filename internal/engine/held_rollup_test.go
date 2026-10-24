package engine

import (
	"context"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
)

// A result a collector or an agent held through an outage is stored hours
// in the past. The next routine retention pass must roll it up, or the 3-
// and 7-day charts (read from 5-minute buckets) never show it.
func TestHeldResultsReachTheRollups(t *testing.T) {
	e, st, _, _ := setup(t)
	ctx := context.Background()
	node, agent := collectedSite(t, st)
	_, hwCheck, hwAgent := pushedMachine(t, st, 1)
	if err := e.ReloadConfig(ctx); err != nil {
		t.Fatal(err)
	}
	ping := node.Checks[0].ID
	then := time.Now().Add(-5 * time.Hour)
	r := collectorResult(ping, false, then)
	r.Held = true
	if _, err := e.IngestCollectorResults(ctx, agent, model.CollectorBatch{Results: []model.CollectorResult{r}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.IngestHostReading(ctx, hwAgent, machineReading("nas.local", 10, 40, then), ""); err != nil {
		t.Fatal(err)
	}
	if err := e.runRetention(ctx, false, false); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{ping, hwCheck.ID} {
		rows, err := st.RollupsBetween(ctx, id, store.Bucket5m, then.Add(-10*time.Minute), then.Add(10*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].Count != 1 {
			t.Errorf("check %d: the held result is missing from the 5-minute rollups: %+v", id, rows)
		}
	}
}

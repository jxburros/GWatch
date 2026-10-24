package engine

import (
	"context"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/checks"
	"github.com/jxburros/GWatch/internal/model"
)

// A check stored without a timeout gets 10 s per attempt from the checks
// package; the engine's bound on the whole run must leave room for every
// retry rather than cutting it off after 15 s.
func TestRunBoundAllowsDefaultAttemptTimeout(t *testing.T) {
	e, st, _, _ := setup(t)
	ctx := context.Background()
	n, err := st.CreateNode(ctx, model.Node{Name: "Site", Host: "example.com", Enabled: true, Checks: []model.Check{{Type: model.CheckTCP, Name: "TCP", Enabled: true, IntervalSeconds: 60, Retries: 3}}})
	if err != nil {
		t.Fatal(err)
	}
	var left time.Duration
	e.opts.Run = func(rctx context.Context, c model.Check, _ checks.Options) model.Result {
		d, _ := rctx.Deadline()
		left = time.Until(d)
		return model.Result{Timestamp: time.Now(), Success: true, Status: model.StatusUp}
	}
	if err := e.ReloadConfig(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.RunNow(ctx, n.Checks[0].ID); err != nil {
		t.Fatal(err)
	}
	if left < 40*time.Second {
		t.Fatalf("run bounded to %s; four 10 s attempts need at least 40 s", left)
	}
}

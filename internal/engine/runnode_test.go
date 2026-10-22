package engine

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/checks"
	"github.com/jxburros/GWatch/internal/model"
)

// manyCheckNode stores a node with count ping checks and loads it into the
// engine with MaxConcurrentChecks set to limit.
func manyCheckNode(t *testing.T, e *Engine, count, limit int) model.Node {
	t.Helper()
	ctx := context.Background()
	settings, err := e.store.LoadSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings.General.MaxConcurrentChecks = limit
	if err := e.store.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	cs := make([]model.Check, 0, count)
	for i := 0; i < count; i++ {
		cs = append(cs, model.Check{Name: fmt.Sprintf("Ping %d", i), Type: model.CheckPing, Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 1})
	}
	n, err := e.store.CreateNode(ctx, model.Node{Name: "Wide", Host: "localhost", Enabled: true, Checks: cs})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.ReloadConfig(ctx); err != nil {
		t.Fatal(err)
	}
	e.ctx = context.Background()
	t.Cleanup(e.wg.Wait)
	return n
}

// A manual node run shares the scheduler's semaphore, so a node with many
// checks never has more than MaxConcurrentChecks of them running at once.
func TestRunNodeNowHonoursMaxConcurrentChecks(t *testing.T) {
	e, _, _, _ := setup(t)
	const limit = 3
	n := manyCheckNode(t, e, 20, limit)

	var active, peak atomic.Int32
	e.opts.Run = func(_ context.Context, _ model.Check, _ checks.Options) model.Result {
		now := active.Add(1)
		for {
			p := peak.Load()
			if now <= p || peak.CompareAndSwap(p, now) {
				break
			}
		}
		time.Sleep(15 * time.Millisecond)
		active.Add(-1)
		return model.Result{Success: true, Status: model.StatusUp}
	}
	results, err := e.RunNodeNow(context.Background(), n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != len(n.Checks) {
		t.Fatalf("ran %d of %d checks", len(results), len(n.Checks))
	}
	if got := peak.Load(); got > limit {
		t.Fatalf("a manual node run had %d checks in flight, above the limit of %d", got, limit)
	}
}

// A run waiting for a slot gives up when its caller does, rather than queuing
// the rest of the node behind a scheduler that is already full.
func TestRunNodeNowStopsWaitingWhenCancelled(t *testing.T) {
	e, _, net, _ := setup(t)
	n := manyCheckNode(t, e, 5, 2)
	// Every slot is taken, as by a busy scheduler.
	e.sem <- struct{}{}
	e.sem <- struct{}{}
	defer func() { <-e.sem; <-e.sem }()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan []model.Result, 1)
	go func() {
		r, _ := e.RunNodeNow(ctx, n.ID)
		done <- r
	}()
	select {
	case r := <-done:
		if len(r) != 0 {
			t.Fatalf("no slot was free, yet %d check(s) ran", len(r))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RunNodeNow kept waiting for a slot after its context ended")
	}
	net.mu.Lock()
	defer net.mu.Unlock()
	if len(net.runs) != 0 {
		t.Fatalf("checks ran without a slot: %v", net.runs)
	}
}

// RunNodeChecks leaves out what its filter refuses.
func TestRunNodeChecksAppliesFilter(t *testing.T) {
	e, _, _, _ := setup(t)
	n := manyCheckNode(t, e, 4, 8)
	skip := n.Checks[1].ID
	results, err := e.RunNodeChecks(context.Background(), n.ID, func(c model.Check) bool { return c.ID != skip })
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	for _, r := range results {
		if r.CheckID == skip {
			t.Fatal("the filtered check ran")
		}
	}
}

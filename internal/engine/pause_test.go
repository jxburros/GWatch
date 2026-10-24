package engine

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/checks"
	"github.com/jxburros/GWatch/internal/model"
)

// #148: a restore pauses the engine so nothing it does on its own writes to
// the database, or runs a check against half-restored configuration, while
// the restore runs. Pause has to wait for a check already running, refuse new
// work until Resume, and never leave the engine paused when it gives up.
func TestPauseWaitsForRunningWorkAndRefusesNewWork(t *testing.T) {
	e, st, _, _ := setup(t)
	ctx := context.Background()
	started := make(chan struct{}, 16)
	release := make(chan struct{})
	var runs atomic.Int32
	e.opts.Run = func(ctx context.Context, c model.Check, opts checks.Options) model.Result {
		runs.Add(1)
		started <- struct{}{}
		<-release
		return model.Result{Timestamp: time.Now(), Success: true, Status: model.StatusUp, Message: "ok", Attempts: 1}
	}
	n, err := st.CreateNode(ctx, model.Node{Name: "Box", Host: "10.0.0.9", Enabled: true,
		Checks: []model.Check{{Type: model.CheckPing, Name: "Ping", Enabled: true, IntervalSeconds: 3600, TimeoutSeconds: 5}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		e.Stop()
	})
	// The scheduler staggers a new check's first run by up to two seconds.
	select {
	case <-started:
	case <-time.After(6 * time.Second):
		t.Fatal("the check never started")
	}

	paused := make(chan error, 1)
	go func() { paused <- e.Pause(ctx) }()
	select {
	case err := <-paused:
		t.Fatalf("Pause returned (%v) while a check was still running", err)
	case <-time.After(200 * time.Millisecond):
	}
	if !e.Paused() {
		t.Fatal("the gate should be shut to new work while Pause waits")
	}
	close(release)
	if err := <-paused; err != nil {
		t.Fatalf("Pause: %v", err)
	}
	beforeRuns := runs.Load()

	// Paused: no requested run, no retention pass, no second pause, and the
	// scheduler does not start a check that is due.
	if _, err := e.RunNow(ctx, n.Checks[0].ID); !errors.Is(err, ErrPaused) {
		t.Fatalf("RunNow while paused: %v, want ErrPaused", err)
	}
	if _, err := e.RunNodeNow(ctx, n.ID); !errors.Is(err, ErrPaused) {
		t.Fatalf("RunNodeNow while paused: %v, want ErrPaused", err)
	}
	if err := e.RunRetention(ctx, true); !errors.Is(err, ErrPaused) {
		t.Fatalf("RunRetention while paused: %v, want ErrPaused", err)
	}
	if err := e.Pause(ctx); !errors.Is(err, ErrAlreadyPaused) {
		t.Fatalf("a second Pause: %v, want ErrAlreadyPaused", err)
	}
	e.mu.Lock()
	past := time.Now().Add(-time.Minute)
	e.states[n.Checks[0].ID].NextRunAt = &past
	e.mu.Unlock()
	time.Sleep(1500 * time.Millisecond) // more than one scheduler tick
	if got := runs.Load(); got != beforeRuns {
		t.Fatalf("a check ran while paused (%d runs, was %d)", got, beforeRuns)
	}

	// Resumed: the due check runs on the next tick, and a requested run works.
	e.Resume()
	if e.Paused() {
		t.Fatal("still paused after Resume")
	}
	waitFor(t, func() bool { return runs.Load() > beforeRuns })
	if _, err := e.RunNow(ctx, n.Checks[0].ID); err != nil {
		t.Fatalf("RunNow after Resume: %v", err)
	}
}

func TestPauseGivesUpWithoutStayingPaused(t *testing.T) {
	e, _, _, _ := setup(t)
	// Something holding the gate that will not finish in time.
	if !e.enterWork() {
		t.Fatal("enterWork on a fresh engine")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := e.Pause(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Pause: %v, want the context's deadline", err)
	}
	if e.Paused() {
		t.Fatal("a Pause that gave up left the engine paused")
	}
	e.leaveWork()
	if err := e.Pause(context.Background()); err != nil {
		t.Fatalf("Pause once idle: %v", err)
	}
	e.Resume()
	e.Resume() // resuming twice is harmless
	if e.Paused() {
		t.Fatal("still paused")
	}
}

// A restore keeps check ids but empties their stored states; once the engine
// discards what it held, a restored check does not inherit the old check's
// status or active alert.
func TestDiscardStatesForgetsTheOldChecksState(t *testing.T) {
	e, st, _, _ := setup(t)
	ctx := context.Background()
	n, err := st.CreateNode(ctx, model.Node{Name: "Old", Host: "example.com", Enabled: true, Checks: []model.Check{{Type: model.CheckTCP, Name: "TCP", Enabled: true, IntervalSeconds: 60}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.ReloadConfig(ctx); err != nil {
		t.Fatal(err)
	}
	id := n.Checks[0].ID
	e.mu.Lock()
	e.states[id].Status = model.StatusDown
	e.states[id].AlertActive = true
	e.mu.Unlock()

	e.DiscardStates()
	if err := e.ReloadConfig(ctx); err != nil {
		t.Fatal(err)
	}
	got := e.States()[id]
	if got.AlertActive || got.Status == model.StatusDown {
		t.Fatalf("the check kept the state the engine held before: %+v", got)
	}
}

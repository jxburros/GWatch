package engine

import (
	"context"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/checks"
	"github.com/jxburros/GWatch/internal/model"
)

// A result whose down decision waits on a dependency probe must not, once the
// probe is back, overwrite what another result for the same check (a pushed
// hardware reading, say) committed in the meantime.
func TestProbeDoesNotOverwriteConcurrentCommit(t *testing.T) {
	e, st, _, _ := setup(t)
	parent := regressionNode(t, e)
	child, err := st.CreateNode(context.Background(), model.Node{Name: "child", Host: "localhost", Enabled: true, DependsOnNode: &parent.ID, Checks: []model.Check{{Name: "ping", Type: model.CheckPing, Enabled: true, FailureThreshold: 2, IntervalSeconds: 60}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.ReloadConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	e.opts.Run = func(_ context.Context, c model.Check, _ checks.Options) model.Result {
		if c.ID == parent.Checks[0].ID {
			close(entered)
			<-release
		}
		return model.Result{Timestamp: time.Now(), Success: true, Status: model.StatusUp}
	}
	c, n := child.Checks[0], child
	fail := func(at time.Time) model.Result {
		return model.Result{CheckID: c.ID, Timestamp: at, Status: model.StatusDown, Message: "refused"}
	}
	base := time.Now()
	if _, err := e.process(context.Background(), c, n, fail(base), 0); err != nil {
		t.Fatal(err)
	}
	// The second failure reaches the threshold and probes the parent.
	done := make(chan error, 1)
	go func() { _, err := e.process(context.Background(), c, n, fail(base.Add(time.Second)), 0); done <- err }()
	<-entered
	ok := model.Result{CheckID: c.ID, Timestamp: base.Add(2 * time.Second), Success: true, Status: model.StatusUp, Message: "ok"}
	if _, err := e.process(context.Background(), c, n, ok, 0); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	state, _ := e.State(c.ID)
	if state.LastSuccessAt == nil || state.ConsecutiveFailures != 1 || state.Status == model.StatusDown {
		t.Fatalf("probe result overwrote the concurrent success: %+v", state)
	}
}

package engine

import (
	"context"
	"errors"
	"sync"
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

// Rule evaluations that overlap must store where a rule stands in the order
// they decided it, so the saved state (read back after a restart) agrees with
// the one in memory.
func TestConcurrentRuleEvaluationsPersistTheLastDecision(t *testing.T) {
	f := newRuleFixture(t)
	id := f.checks[0]
	rule := f.saveRule(t, model.Rule{Name: "Down", Enabled: true, Join: model.RuleJoinAny,
		Conditions: []model.RuleCondition{checkCond(id, model.StatusDown)}})
	for round := 0; round < 20; round++ {
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(down bool) {
				defer wg.Done()
				f.e.mu.Lock()
				f.e.states[id].Status = map[bool]model.Status{true: model.StatusDown, false: model.StatusUp}[down]
				f.e.mu.Unlock()
				f.e.evaluateRules(context.Background(), &id)
			}(i%2 == 0)
		}
		wg.Wait()
		stored, err := f.st.RuleStates(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if mem := f.e.RuleStates()[rule.ID]; stored[rule.ID].Met != mem.Met {
			t.Fatalf("round %d: stored met=%v, in memory met=%v", round, stored[rule.ID].Met, mem.Met)
		}
	}
	f.e.wg.Wait()
}

// A pushed reading that could not be recorded is not answered as accepted:
// the agent drops a reading GWatch accepted, and this one is in no history.
// Sent again, it is recorded.
func TestAPushedReadingThatIsNotRecordedIsAnError(t *testing.T) {
	e, st, _, _ := setup(t)
	ctx := context.Background()
	_, check, agent := pushedMachine(t, st, 2)
	if err := e.ReloadConfig(ctx); err != nil {
		t.Fatal(err)
	}
	save := e.recordResult
	e.recordResult = func(context.Context, model.Result, model.CheckState) (model.Result, error) {
		return model.Result{}, errors.New("database is locked")
	}
	reading := machineReading("nas.local", 10, 40, time.Now())
	if _, err := e.IngestHostReading(ctx, agent, reading, ""); err == nil {
		t.Fatal("a reading that was not recorded was reported as accepted")
	}
	e.recordResult = save
	if _, err := e.IngestHostReading(ctx, agent, reading, ""); err != nil {
		t.Fatal(err)
	}
	if rs := resultsOf(t, st, check.ID); len(rs) != 1 {
		t.Fatalf("the reading sent again should be recorded once, got %d", len(rs))
	}
	e.wg.Wait()
}

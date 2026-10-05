package engine

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/checks"
	"github.com/jxburros/GWatch/internal/mailer"
	"github.com/jxburros/GWatch/internal/model"
)

func regressionNode(t *testing.T, e *Engine) model.Node {
	t.Helper()
	n, err := e.store.CreateNode(context.Background(), model.Node{Name: "Test", Host: "localhost", Enabled: true, Checks: []model.Check{{Name: "Ping", Type: model.CheckPing, Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 1, FailureThreshold: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.ReloadConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.ctx = context.Background()
	t.Cleanup(e.wg.Wait)
	return n
}

func TestWarningDoesNotDelayDown(t *testing.T) {
	e, _, _, mb := setup(t)
	n := regressionNode(t, e)
	c := n.Checks[0]
	e.settings.Alerts.NotifyWarnings = true
	_, err := e.process(context.Background(), c, n, model.Result{CheckID: c.ID, Timestamp: time.Now(), Success: true, Status: model.StatusDegraded, Warnings: []string{"slow"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	e.wg.Wait()
	_, err = e.process(context.Background(), c, n, model.Result{CheckID: c.ID, Timestamp: time.Now().Add(time.Minute), Status: model.StatusDown}, 0)
	if err != nil {
		t.Fatal(err)
	}
	e.wg.Wait()
	if mb.count() != 2 {
		t.Fatalf("warning then down: %v", mb.subjects())
	}
	state, _ := e.State(c.ID)
	if state.LastWarnAt == nil || state.LastAlertAt == nil {
		t.Fatalf("separate clocks missing: %+v", state)
	}
}

func TestFailedPersistenceDoesNotCommitAlertState(t *testing.T) {
	e, _, _, mb := setup(t)
	n := regressionNode(t, e)
	c := n.Checks[0]
	save := e.recordResult
	e.recordResult = func(context.Context, model.Result, model.CheckState) (model.Result, error) {
		return model.Result{}, errors.New("disk full")
	}
	r := model.Result{CheckID: c.ID, Timestamp: time.Now(), Status: model.StatusDown}
	if _, err := e.process(context.Background(), c, n, r, 0); err == nil {
		t.Fatal("expected store failure")
	}
	state, _ := e.State(c.ID)
	if state.AlertActive || state.ConsecutiveFailures != 0 || mb.count() != 0 {
		t.Fatalf("uncommitted state: %+v", state)
	}
	e.recordResult = save
	if _, err := e.process(context.Background(), c, n, r, 0); err != nil {
		t.Fatal(err)
	}
	e.wg.Wait()
	if mb.count() != 1 {
		t.Fatal("outage notification was lost")
	}
}

func TestPausedInFlightResultIsDiscarded(t *testing.T) {
	e, _, _, mb := setup(t)
	n := regressionNode(t, e)
	c := n.Checks[0]
	e.mu.Lock()
	paused := c
	paused.Enabled = false
	e.checks[c.ID] = paused
	e.states[c.ID].Status = model.StatusPaused
	e.mu.Unlock()
	if _, err := e.process(context.Background(), c, n, model.Result{CheckID: c.ID, Timestamp: time.Now(), Status: model.StatusDown}, 0); err != nil {
		t.Fatal(err)
	}
	state, _ := e.State(c.ID)
	if state.Status != model.StatusPaused || state.ConsecutiveFailures != 0 || mb.count() != 0 {
		t.Fatalf("paused state overwritten: %+v", state)
	}
}

func TestOverlappingRunsShareResult(t *testing.T) {
	e, _, _, _ := setup(t)
	n := regressionNode(t, e)
	id := n.Checks[0].ID
	started, release := make(chan struct{}), make(chan struct{})
	var runs atomic.Int32
	e.opts.Run = func(context.Context, model.Check, checks.Options) model.Result {
		runs.Add(1)
		close(started)
		<-release
		return model.Result{Status: model.StatusDown}
	}
	done := make(chan error, 2)
	go func() { _, err := e.RunNow(context.Background(), id); done <- err }()
	<-started
	waiterCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.RunNow(waiterCtx, id); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter cancellation: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if runs.Load() != 1 {
		t.Fatal("duplicate run")
	}
	state, _ := e.State(id)
	if state.ConsecutiveFailures != 1 {
		t.Fatalf("duplicate failure: %+v", state)
	}
}

func TestParentApplicationFailureDoesNotSuppress(t *testing.T) {
	e, _, _, _ := setup(t)
	n := regressionNode(t, e)
	p := model.Node{ID: 100, Enabled: true, Checks: []model.Check{{ID: 101, Type: model.CheckHTTP, Enabled: true}, {ID: 102, Type: model.CheckPing, Enabled: true}}}
	n.DependsOnNode = &p.ID
	e.nodes[p.ID] = p
	e.states[101] = &model.CheckState{Status: model.StatusDown, ConsecutiveFailures: 3}
	e.states[102] = &model.CheckState{Status: model.StatusUp}
	if c, _ := e.parentDownLocked(n); c != nil {
		t.Fatal("HTTP failure suppressed child")
	}
	e.states[102].Status = model.StatusDown
	if c, _ := e.parentDownLocked(n); c == nil || c.ID != 102 {
		t.Fatal("reachability failure did not suppress child")
	}
}

func TestTestCheckHasHardwareMonitor(t *testing.T) {
	e, _, _, _ := setup(t)
	e.opts.Run = func(_ context.Context, _ model.Check, o checks.Options) model.Result {
		if o.Hosts == nil {
			t.Error("hardware monitor missing")
		}
		return model.Result{Success: true}
	}
	e.TestCheck(context.Background(), model.Check{Type: model.CheckSystem}, "localhost")
}

func TestSchedulerPrioritizesOldestDue(t *testing.T) {
	e, _, _, _ := setup(t)
	n := regressionNode(t, e)
	id := n.Checks[0].ID
	newer, older := time.Now().Add(-time.Minute), time.Now().Add(-3*time.Minute)
	e.states[id].NextRunAt = &newer
	c := n.Checks[0]
	c.ID = id + 100
	e.checks[c.ID] = c
	e.states[c.ID] = &model.CheckState{CheckID: c.ID, NextRunAt: &older}
	e.sem = make(chan struct{}, 1)
	e.lastTick = time.Now()
	ran, release := make(chan int64, 1), make(chan struct{})
	e.opts.Run = func(_ context.Context, c model.Check, _ checks.Options) model.Result {
		ran <- c.ID
		<-release
		return model.Result{Success: true}
	}
	e.tick(time.Now())
	got := <-ran
	close(release)
	e.wg.Wait()
	if got != c.ID {
		t.Fatalf("scheduled %d ahead of overdue %d", got, c.ID)
	}
}

func TestSilenceExpiryReevaluatesRules(t *testing.T) {
	f := newRuleFixture(t)
	f.e.ctx = context.Background()
	hs := newHookServer(t)
	rule := f.saveRule(t, model.Rule{Name: "outage", Enabled: true, Join: model.RuleJoinAll, Conditions: []model.RuleCondition{checkCond(f.checks[0], model.StatusDown)}, Actions: []model.Action{hookAction(hs)}})
	if _, err := f.e.Silence(context.Background(), f.checks[0], time.Hour); err != nil {
		t.Fatal(err)
	}
	f.setDown(t, f.checks[0], true)
	if f.e.RuleStates()[rule.ID].Met {
		t.Fatal("silenced rule fired")
	}
	f.e.mu.Lock()
	past := time.Now().Add(-time.Minute)
	f.e.states[f.checks[0]].SilencedUntil = &past
	for _, s := range f.e.states {
		s.NextRunAt = nil
	}
	f.e.lastTick = time.Now()
	f.e.mu.Unlock()
	f.e.tick(time.Now())
	f.e.wg.Wait()
	if !f.e.RuleStates()[rule.ID].Met || hs.count() != 1 {
		t.Fatal("expired silence did not fire rule")
	}
}

func TestFailedSMTPRetriesWithoutFalseRecovery(t *testing.T) {
	e, _, net, mb := setup(t)
	n := regressionNode(t, e)
	id := n.Checks[0].ID
	e.opts.Send = func(context.Context, model.SMTPSettings, mailer.Message) error { return errors.New("SMTP unavailable") }
	net.down[id] = true
	if _, err := e.RunNow(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	e.wg.Wait()
	state, _ := e.State(id)
	if state.AlertActive || state.LastAlertAt != nil {
		t.Fatalf("failed delivery consumed alert: %+v", state)
	}
	e.opts.Send = mb.send
	net.down[id] = false
	if _, err := e.RunNow(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	e.wg.Wait()
	if mb.count() != 0 {
		t.Fatal("recovery sent for failed down notification")
	}
	net.down[id] = true
	if _, err := e.RunNow(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	e.wg.Wait()
	if mb.count() != 1 {
		t.Fatal("down alert did not retry after SMTP recovery")
	}
}

func TestParentProbeWaitsForInFlightReachability(t *testing.T) {
	e, st, _, mb := setup(t)
	parent := regressionNode(t, e)
	child, err := st.CreateNode(context.Background(), model.Node{Name: "child", Host: "localhost", Enabled: true, DependsOnNode: &parent.ID, Checks: []model.Check{{Name: "ping", Type: model.CheckPing, Enabled: true, FailureThreshold: 1, IntervalSeconds: 60}}})
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
		return model.Result{Status: model.StatusDown}
	}
	done := make(chan error, 2)
	go func() { _, err := e.RunNow(context.Background(), parent.Checks[0].ID); done <- err }()
	<-entered
	go func() { _, err := e.RunNow(context.Background(), child.Checks[0].ID); done <- err }()
	// The child cannot complete while its parent's result remains unknown.
	select {
	case err := <-done:
		t.Fatalf("returned before parent probe: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	e.wg.Wait()
	state, _ := e.State(child.Checks[0].ID)
	if state.SuppressReason != "dependency" || mb.count() != 1 {
		t.Fatalf("dependency race: %+v, mails %d", state, mb.count())
	}
}

func TestFailedSilenceWriteDoesNotMuteInMemory(t *testing.T) {
	e, _, _, _ := setup(t)
	n := regressionNode(t, e)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Silence(ctx, n.Checks[0].ID, time.Hour); err == nil {
		t.Fatal("expected persistence error")
	}
	state, _ := e.State(n.Checks[0].ID)
	if state.SilencedUntil != nil {
		t.Fatal("failed silence write still muted check")
	}
}

func TestAcknowledgedNodeIncidentSuppressesFurtherDownAlerts(t *testing.T) {
	e, st, net, mb := setup(t)
	ctx := context.Background()
	n, err := st.CreateNode(ctx, model.Node{Name: "Multi", Host: "localhost", Enabled: true, Checks: []model.Check{{Name: "Ping", Type: model.CheckPing, Enabled: true, FailureThreshold: 1}, {Name: "TCP", Type: model.CheckTCP, Enabled: true, FailureThreshold: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.ReloadConfig(ctx); err != nil {
		t.Fatal(err)
	}
	net.down[n.Checks[0].ID] = true
	if _, err = e.RunNow(ctx, n.Checks[0].ID); err != nil {
		t.Fatal(err)
	}
	e.wg.Wait()
	incs, err := st.ListIncidents(ctx, "all")
	if err != nil || len(incs) != 1 {
		t.Fatalf("incidents: %v %+v", err, incs)
	}
	if _, err = st.UpdateIncident(ctx, incs[0].ID, "acknowledge", "tester", ""); err != nil {
		t.Fatal(err)
	}
	net.down[n.Checks[1].ID] = true
	if _, err = e.RunNow(ctx, n.Checks[1].ID); err != nil {
		t.Fatal(err)
	}
	e.wg.Wait()
	state, _ := e.State(n.Checks[1].ID)
	if state.SuppressReason != "acknowledged" || mb.count() != 1 {
		t.Fatalf("acknowledged incident re-alerted: %+v mails=%d", state, mb.count())
	}
}

package engine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/logging"
	"github.com/jxburros/GWatch/internal/mailer"
	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
)

// hookServer counts the HTTP requests a rule's action makes and keeps the
// bodies, so a test can see what fired and with which placeholders.
type hookServer struct {
	*httptest.Server
	mu     sync.Mutex
	bodies []map[string]string
}

func newHookServer(t *testing.T) *hookServer {
	t.Helper()
	hs := &hookServer{}
	hs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]string
		_ = json.Unmarshal(b, &m)
		hs.mu.Lock()
		hs.bodies = append(hs.bodies, m)
		hs.mu.Unlock()
		io.WriteString(w, "ok")
	}))
	t.Cleanup(hs.Close)
	return hs
}

func (hs *hookServer) count() int {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	return len(hs.bodies)
}

func (hs *hookServer) body(i int) map[string]string {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	return hs.bodies[i]
}

// hookAction posts the rule placeholders as JSON to the hook server.
func hookAction(hs *hookServer) model.Action {
	return model.Action{Type: model.ActionHTTP, Method: "POST", URL: hs.URL, Body: `{"event":"{{event}}","name":"{{rule.name}}","met":"{{rule.met}}","status":"{{status}}","message":"{{message}}","conditions":"{{rule.conditions}}"}`}
}

// ruleFixture is three DNS servers, each a node with one check, the way the
// "two of three DNS servers down" recipe has them.
type ruleFixture struct {
	e      *Engine
	st     *store.Store
	net    *fakeNet
	nodes  []model.Node
	checks []int64
}

func newRuleFixture(t *testing.T) *ruleFixture {
	t.Helper()
	e, st, net, _ := setup(t)
	ctx := context.Background()
	f := &ruleFixture{e: e, st: st, net: net}
	for _, name := range []string{"Pi-hole", "Router", "Cloud DNS"} {
		n, err := st.CreateNode(ctx, model.Node{Name: name, Host: name + ".lan", Enabled: true, Checks: []model.Check{{Type: model.CheckDNS, Name: "DNS", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5}}})
		if err != nil {
			t.Fatal(err)
		}
		f.nodes = append(f.nodes, n)
		f.checks = append(f.checks, n.Checks[0].ID)
	}
	return f
}

// setDown takes a check down (two failing runs, the failure threshold in
// setup) or brings it back up (one good run).
func (f *ruleFixture) setDown(t *testing.T, checkID int64, down bool) {
	t.Helper()
	ctx := context.Background()
	f.net.mu.Lock()
	f.net.down[checkID] = down
	delete(f.net.custom, checkID)
	f.net.mu.Unlock()
	runs := 1
	if down {
		runs = 2
	}
	for i := 0; i < runs; i++ {
		if _, err := f.e.RunNow(ctx, checkID); err != nil {
			t.Fatal(err)
		}
	}
	want := model.StatusUp
	if down {
		want = model.StatusDown
	}
	if s, _ := f.e.State(checkID); s.Status != want {
		t.Fatalf("check %d: expected %s, got %s", checkID, want, s.Status)
	}
}

// setDegraded makes a check report a degraded (but successful) result.
func (f *ruleFixture) setDegraded(t *testing.T, checkID int64) {
	t.Helper()
	f.net.mu.Lock()
	if f.net.custom == nil {
		f.net.custom = map[int64]func() model.Result{}
	}
	f.net.custom[checkID] = func() model.Result {
		lat := 900.0
		return model.Result{Timestamp: time.Now(), Success: true, Status: model.StatusDegraded, Message: "slow", LatencyMS: &lat, Attempts: 1, Warnings: []string{"slow"}}
	}
	f.net.mu.Unlock()
	if _, err := f.e.RunNow(context.Background(), checkID); err != nil {
		t.Fatal(err)
	}
	if s, _ := f.e.State(checkID); s.Status != model.StatusDegraded {
		t.Fatalf("check %d: expected degraded, got %s", checkID, s.Status)
	}
}

func (f *ruleFixture) saveRule(t *testing.T, r model.Rule) model.Rule {
	t.Helper()
	saved, err := f.st.SaveRule(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.e.ReloadConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
	return saved
}

func checkCond(id int64, status model.Status) model.RuleCondition {
	return model.RuleCondition{Kind: model.RuleConditionStatus, CheckID: &id, Status: status}
}

func nodeCond(id int64, status model.Status) model.RuleCondition {
	return model.RuleCondition{Kind: model.RuleConditionStatus, NodeID: &id, Status: status}
}

func ruleEvents(t *testing.T, st *store.Store, typ model.EventType) []model.Event {
	t.Helper()
	evs, err := st.ListEvents(context.Background(), store.EventFilter{Limit: 100, Types: []model.EventType{typ}})
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

func TestRuleAtLeastFiresOnceAndClears(t *testing.T) {
	f := newRuleFixture(t)
	hs := newHookServer(t)
	ctx := context.Background()
	if err := f.e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer f.e.Stop()
	for _, id := range f.checks {
		f.setDown(t, id, false)
	}
	rule := f.saveRule(t, model.Rule{Name: "Two of three DNS servers down", Enabled: true, Join: model.RuleJoinAtLeast, AtLeast: 2,
		Conditions: []model.RuleCondition{checkCond(f.checks[0], model.StatusDown), checkCond(f.checks[1], model.StatusDown), checkCond(f.checks[2], model.StatusDown)},
		Actions:    []model.Action{hookAction(hs)}, NotifyCleared: true})

	// One down: not met, nothing fires.
	f.setDown(t, f.checks[0], true)
	time.Sleep(100 * time.Millisecond)
	if hs.count() != 0 || f.e.RuleStates()[rule.ID].Met {
		t.Fatalf("one of three should not fire: hits=%d state=%+v", hs.count(), f.e.RuleStates()[rule.ID])
	}
	// Two down: met, fires once with the generated message.
	f.setDown(t, f.checks[1], true)
	waitFor(t, func() bool { return hs.count() == 1 })
	b := hs.body(0)
	if b["event"] != "rule_fired" || b["name"] != rule.Name || b["met"] != "2" || b["status"] != "met" || b["conditions"] != "Pi-hole › DNS is down, Router › DNS is down" {
		t.Fatalf("placeholders: %+v", b)
	}
	if b["message"] != "2 of 3 conditions met — Pi-hole › DNS is down, Router › DNS is down" {
		t.Fatalf("message: %q", b["message"])
	}
	st := f.e.RuleStates()[rule.ID]
	if !st.Met || st.Since == nil || st.LastFiredAt == nil {
		t.Fatalf("state after firing: %+v", st)
	}
	// Three down: still met, no second firing.
	f.setDown(t, f.checks[2], true)
	time.Sleep(100 * time.Millisecond)
	if hs.count() != 1 {
		t.Fatalf("a rule fires once per episode, got %d", hs.count())
	}
	waitFor(t, func() bool { return len(ruleEvents(t, f.st, model.EventRuleFired)) == 1 })
	ev := ruleEvents(t, f.st, model.EventRuleFired)[0]
	if ev.Title != "Rule fired: "+rule.Name || ev.NodeID != nil || ev.CheckID != nil {
		t.Fatalf("event: %+v", ev)
	}
	// Back to one down: cleared, and NotifyCleared runs the actions again.
	f.setDown(t, f.checks[0], false)
	f.setDown(t, f.checks[1], false)
	waitFor(t, func() bool { return hs.count() == 2 })
	if b := hs.body(1); b["event"] != "rule_cleared" || b["status"] != "cleared" || b["met"] != "1" {
		t.Fatalf("cleared placeholders: %+v", b)
	}
	waitFor(t, func() bool { return len(ruleEvents(t, f.st, model.EventRuleCleared)) == 1 })
	if st := f.e.RuleStates()[rule.ID]; st.Met {
		t.Fatalf("state after clearing: %+v", st)
	}
	// Stored too.
	stored, _ := f.st.RuleStates(ctx)
	if stored[rule.ID].Met || stored[rule.ID].LastFiredAt == nil {
		t.Fatalf("stored state: %+v", stored[rule.ID])
	}
}

func TestRuleAllAnyAndNodeConditions(t *testing.T) {
	f := newRuleFixture(t)
	hs := newHookServer(t)
	ctx := context.Background()
	if err := f.e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer f.e.Stop()
	for _, id := range f.checks {
		f.setDown(t, id, false)
	}
	all := f.saveRule(t, model.Rule{Name: "All", Enabled: true, Join: model.RuleJoinAll,
		Conditions: []model.RuleCondition{checkCond(f.checks[0], model.StatusDown), checkCond(f.checks[1], model.StatusDown)}, Actions: []model.Action{hookAction(hs)}})
	// "degraded" on a node means degraded or worse on any of its checks.
	anyRule := f.saveRule(t, model.Rule{Name: "Any", Enabled: true, Join: model.RuleJoinAny,
		Conditions: []model.RuleCondition{nodeCond(f.nodes[2].ID, model.StatusDegraded), checkCond(f.checks[1], model.StatusDown)}, Actions: []model.Action{hookAction(hs)}})
	disabled := f.saveRule(t, model.Rule{Name: "Off", Enabled: false, Join: model.RuleJoinAny,
		Conditions: []model.RuleCondition{checkCond(f.checks[0], model.StatusDown)}, Actions: []model.Action{hookAction(hs)}})

	f.setDown(t, f.checks[0], true)
	time.Sleep(100 * time.Millisecond)
	if hs.count() != 0 {
		t.Fatalf("\"all\" with one of two down should not fire (and a disabled rule never does): %d", hs.count())
	}
	f.setDegraded(t, f.checks[2])
	waitFor(t, func() bool { return hs.count() == 1 })
	if b := hs.body(0); b["name"] != "Any" || b["conditions"] != "Cloud DNS › DNS is degraded" {
		t.Fatalf("any/node condition: %+v", b)
	}
	f.setDown(t, f.checks[1], true)
	waitFor(t, func() bool { return hs.count() == 2 })
	if b := hs.body(1); b["name"] != "All" || b["met"] != "2" {
		t.Fatalf("all: %+v", b)
	}
	// Degraded → down on the node still satisfies "degraded"; nothing new fires.
	f.setDown(t, f.checks[2], true)
	time.Sleep(100 * time.Millisecond)
	if hs.count() != 2 {
		t.Fatalf("degraded-or-worse should stay met: %d", hs.count())
	}
	states := f.e.RuleStates()
	if !states[all.ID].Met || !states[anyRule.ID].Met || states[disabled.ID].Met {
		t.Fatalf("states: %+v", states)
	}
}

func TestRuleCooldownAndSilence(t *testing.T) {
	f := newRuleFixture(t)
	hs := newHookServer(t)
	ctx := context.Background()
	if err := f.e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer f.e.Stop()
	f.setDown(t, f.checks[0], false)
	rule := f.saveRule(t, model.Rule{Name: "Pi-hole down", Enabled: true, Join: model.RuleJoinAll, CooldownMinutes: 60, NotifyCleared: true,
		Conditions: []model.RuleCondition{checkCond(f.checks[0], model.StatusDown)}, Actions: []model.Action{hookAction(hs)}})

	f.setDown(t, f.checks[0], true)
	waitFor(t, func() bool { return hs.count() == 1 })
	f.setDown(t, f.checks[0], false)
	waitFor(t, func() bool { return hs.count() == 2 }) // the clearing
	// Flap: met again inside the cooldown — the state and the event are
	// kept, the actions are held back, and so is the clearing that follows.
	f.setDown(t, f.checks[0], true)
	waitFor(t, func() bool { return len(ruleEvents(t, f.st, model.EventRuleFired)) == 2 })
	if !f.e.RuleStates()[rule.ID].Met {
		t.Fatal("state should be met even inside the cooldown")
	}
	f.setDown(t, f.checks[0], false)
	waitFor(t, func() bool { return len(ruleEvents(t, f.st, model.EventRuleCleared)) == 2 })
	time.Sleep(100 * time.Millisecond)
	if hs.count() != 2 {
		t.Fatalf("cooldown should hold the actions back: %d", hs.count())
	}
	evs := ruleEvents(t, f.st, model.EventRuleFired)
	held := evs[0]
	if held.Timestamp.Before(evs[1].Timestamp) {
		held = evs[1]
	}
	if !strings.Contains(held.Detail, "held back") {
		t.Fatalf("the held-back firing should say so: %q", held.Detail)
	}

	// A silenced check does not count: silence it, take it down, nothing;
	// unsilence with it still down and the rule is met.
	quiet := f.saveRule(t, model.Rule{Name: "Router down", Enabled: true, Join: model.RuleJoinAll,
		Conditions: []model.RuleCondition{checkCond(f.checks[1], model.StatusDown)}, Actions: []model.Action{hookAction(hs)}})
	f.setDown(t, f.checks[1], false)
	if _, err := f.e.Silence(ctx, f.checks[1], time.Hour); err != nil {
		t.Fatal(err)
	}
	f.setDown(t, f.checks[1], true)
	time.Sleep(100 * time.Millisecond)
	if f.e.RuleStates()[quiet.ID].Met {
		t.Fatal("a silenced check must not count towards a rule")
	}
	if _, err := f.e.Silence(ctx, f.checks[1], 0); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return f.e.RuleStates()[quiet.ID].Met })
}

// A restart reads the rule states back: a rule that fired before the stop
// is still met afterwards and does not fire again for the same outage.
func TestRuleStateSurvivesRestart(t *testing.T) {
	f := newRuleFixture(t)
	hs := newHookServer(t)
	ctx := context.Background()
	if err := f.e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	f.setDown(t, f.checks[0], false)
	rule := f.saveRule(t, model.Rule{Name: "Pi-hole down", Enabled: true, Join: model.RuleJoinAll,
		Conditions: []model.RuleCondition{checkCond(f.checks[0], model.StatusDown)}, Actions: []model.Action{hookAction(hs)}})
	f.setDown(t, f.checks[0], true)
	waitFor(t, func() bool { return hs.count() == 1 })
	f.e.Stop()

	log, _ := logging.New("", nil)
	e2 := New(f.st, log, Options{Version: "test", Run: f.net.run, Send: func(context.Context, model.SMTPSettings, mailer.Message) error { return nil }})
	if err := e2.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer e2.Stop()
	st := e2.RuleStates()[rule.ID]
	if !st.Met || st.LastFiredAt == nil {
		t.Fatalf("state not reloaded: %+v", st)
	}
	// Another failing run changes nothing; recovery clears it.
	if _, err := e2.RunNow(ctx, f.checks[0]); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if hs.count() != 1 {
		t.Fatalf("a restart must not fire the rule again: %d", hs.count())
	}
	f.net.mu.Lock()
	f.net.down[f.checks[0]] = false
	f.net.mu.Unlock()
	if _, err := e2.RunNow(ctx, f.checks[0]); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !e2.RuleStates()[rule.ID].Met })
	waitFor(t, func() bool { return len(ruleEvents(t, f.st, model.EventRuleCleared)) == 1 })
}

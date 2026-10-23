package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
)

// collectedSite is a node at a remote site, checked from a collector: a ping
// and an SNMP check the collector runs, and a script check that stays here.
func collectedSite(t *testing.T, st *store.Store) (model.Node, model.Agent) {
	t.Helper()
	ctx := context.Background()
	agent, err := st.CreateAgent(ctx, "office-pi", nil, "gwa_c", fmt.Sprintf("hash-c-%d", machines.Add(1)), "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetAgentCollector(ctx, agent.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordAgentCheckIn(ctx, agent.ID, nil, true); err != nil {
		t.Fatal(err)
	}
	agent, _ = st.GetAgent(ctx, agent.ID)
	id := agent.ID
	node, err := st.CreateNode(ctx, model.Node{Name: "Branch router", Host: "192.168.50.1", Enabled: true, CollectorID: &id, Checks: []model.Check{
		{Type: model.CheckPing, Name: "Ping", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5},
		{Type: model.CheckSNMP, Name: "SNMP", Enabled: true, IntervalSeconds: 120, TimeoutSeconds: 5,
			Config: model.CheckConfig{SNMPCommunity: "s3cret", SNMPOIDs: []model.SNMPOID{{OID: "1.3.6.1.2.1.1.3.0", Name: "uptime"}}}},
		{Type: model.CheckCustom, Name: "Script", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5, Config: model.CheckConfig{Command: "true"}},
		{Type: model.CheckTCP, Name: "Disabled TCP", Enabled: false, IntervalSeconds: 60, TimeoutSeconds: 5, Config: model.CheckConfig{Port: 22}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return node, agent
}

func collectorResult(checkID int64, ok bool, at time.Time) model.CollectorResult {
	lat := 4.0
	r := model.Result{CheckID: checkID, Timestamp: at, Success: ok, Status: model.StatusUp, Message: "ok", LatencyMS: &lat, Attempts: 1}
	if !ok {
		r.Status, r.Message, r.Error, r.LatencyMS = model.StatusDown, "100% packet loss", "timeout", nil
	}
	return model.CollectorResult{Result: r}
}

func eventsOfType(t *testing.T, st *store.Store, ty model.EventType) []model.Event {
	t.Helper()
	evs, err := st.ListEvents(context.Background(), store.EventFilter{Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	var out []model.Event
	for _, e := range evs {
		if e.Type == ty {
			out = append(out, e)
		}
	}
	return out
}

// The collector is handed exactly the network checks of its nodes — not the
// script beside them, not a disabled check — with the credentials they need.
func TestCollectorAssignmentHoldsOnlyItsNetworkChecks(t *testing.T) {
	e, st, _, _ := setup(t)
	ctx := context.Background()
	node, agent := collectedSite(t, st)
	if err := e.ReloadConfig(ctx); err != nil {
		t.Fatal(err)
	}
	a := e.CollectorAssignment(agent.ID)
	if len(a.Checks) != 2 {
		t.Fatalf("want ping and SNMP, got %+v", a.Checks)
	}
	if a.Checks[0].ID != node.Checks[0].ID || a.Checks[0].Target != "192.168.50.1" {
		t.Errorf("the ping should come first, aimed at the node's host: %+v", a.Checks[0])
	}
	if a.Checks[1].Config.SNMPCommunity != "s3cret" {
		t.Error("the SNMP check needs its community to run")
	}
	for _, c := range a.Checks {
		if c.Type == model.CheckCustom || c.Config.Command != "" {
			t.Fatalf("a script reached a collector: %+v", c)
		}
	}
	if other := e.CollectorAssignment(agent.ID + 1000); len(other.Checks) != 0 {
		t.Fatal("another agent was handed this collector's checks")
	}
}

// The checks a collector runs are not run here — not on the schedule, not by
// Run now — and what the collector sends goes through the ordinary
// processing: down after the threshold, one alert, recovery.
func TestCollectedChecksAreJudgedFromWhatTheCollectorSends(t *testing.T) {
	e, st, net, mb := setup(t)
	ctx := context.Background()
	node, agent := collectedSite(t, st)
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	ping, script := node.Checks[0].ID, node.Checks[2].ID

	if _, err := e.RunNow(ctx, ping); !errors.Is(err, ErrCollected) {
		t.Fatalf("Run now on a collected check: %v", err)
	}
	waitFor(t, func() bool {
		net.mu.Lock()
		defer net.mu.Unlock()
		return net.runs[script] > 0
	})
	net.mu.Lock()
	ran := net.runs[ping]
	net.mu.Unlock()
	if ran != 0 {
		t.Fatalf("the server ran a check its collector owns %d time(s)", ran)
	}

	send := func(ok bool) model.CollectorBatchReply {
		t.Helper()
		reply, err := e.IngestCollectorResults(ctx, agent, model.CollectorBatch{Results: []model.CollectorResult{collectorResult(ping, ok, time.Now())}})
		if err != nil {
			t.Fatal(err)
		}
		return reply
	}
	if r := send(true); r.Accepted != 1 {
		t.Fatalf("reply %+v", r)
	}
	if s, _ := e.State(ping); s.Status != model.StatusUp {
		t.Fatalf("after a good result: %s", s.Status)
	}
	send(false)
	send(false)
	s, _ := e.State(ping)
	if s.Status != model.StatusDown || !s.AlertActive {
		t.Fatalf("after two failures: %+v", s)
	}
	waitFor(t, func() bool { return mb.count() == 1 })
	send(true)
	if s, _ := e.State(ping); s.Status != model.StatusUp {
		t.Fatalf("after recovery: %s", s.Status)
	}
	if got := len(resultsOf(t, st, ping)); got != 4 {
		t.Fatalf("want 4 results in the history, got %d", got)
	}
}

// A collector can only write into checks it was given.
func TestCollectorCannotWriteIntoOtherChecks(t *testing.T) {
	e, st, _, _ := setup(t)
	ctx := context.Background()
	node, agent := collectedSite(t, st)
	local, err := st.CreateNode(ctx, model.Node{Name: "Here", Host: "10.0.0.1", Enabled: true, Checks: []model.Check{
		{Type: model.CheckPing, Name: "Ping", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.ReloadConfig(ctx); err != nil {
		t.Fatal(err)
	}
	impostor := agent
	impostor.ID += 1000
	batch := model.CollectorBatch{Results: []model.CollectorResult{
		collectorResult(local.Checks[0].ID, false, time.Now()), // a check this server runs
		collectorResult(node.Checks[2].ID, false, time.Now()),  // the script on its own node
		collectorResult(node.Checks[3].ID, false, time.Now()),  // a disabled check
		collectorResult(999999, false, time.Now()),             // no such check
	}}
	reply, err := e.IngestCollectorResults(ctx, agent, batch)
	if err != nil {
		t.Fatal(err)
	}
	if reply.Accepted != 0 || reply.Rejected != 4 {
		t.Fatalf("reply %+v", reply)
	}
	reply, _ = e.IngestCollectorResults(ctx, impostor, model.CollectorBatch{Results: []model.CollectorResult{collectorResult(node.Checks[0].ID, false, time.Now())}})
	if reply.Accepted != 0 {
		t.Fatal("another agent wrote into this collector's check")
	}
	if n := len(resultsOf(t, st, local.Checks[0].ID)) + len(resultsOf(t, st, node.Checks[0].ID)); n != 0 {
		t.Fatalf("%d result(s) were recorded", n)
	}
}

// A result the collector held through an outage goes into the history at its
// own time and judges nothing.
func TestHeldCollectorResultsOnlyFillHistory(t *testing.T) {
	e, st, _, mb := setup(t)
	ctx := context.Background()
	node, agent := collectedSite(t, st)
	if err := e.ReloadConfig(ctx); err != nil {
		t.Fatal(err)
	}
	ping := node.Checks[0].ID
	var batch model.CollectorBatch
	for i := 30; i > 0; i-- {
		r := collectorResult(ping, false, time.Now().Add(-time.Duration(i)*time.Minute))
		r.Held = true
		batch.Results = append(batch.Results, r)
	}
	tooOld := collectorResult(ping, false, time.Now().Add(-72*time.Hour))
	tooOld.Held = true
	batch.Results = append(batch.Results, tooOld)
	reply, err := e.IngestCollectorResults(ctx, agent, batch)
	if err != nil {
		t.Fatal(err)
	}
	if reply.Accepted != 30 || reply.Rejected != 1 {
		t.Fatalf("reply %+v", reply)
	}
	if got := len(resultsOf(t, st, ping)); got != 30 {
		t.Fatalf("history has %d results, want 30", got)
	}
	if s, _ := e.State(ping); s.Status != model.StatusUnknown || s.ConsecutiveFailures != 0 {
		t.Fatalf("held results changed the check's state: %+v", s)
	}
	time.Sleep(50 * time.Millisecond)
	if mb.count() != 0 {
		t.Fatal("held results sent an alert")
	}
}

// A collector that goes quiet makes its checks unknown and raises one event
// and one alert — not one per check — and when it is back, a check that was
// down before is down still, without a second down alert.
func TestQuietCollectorRaisesOneAlertNotOnePerCheck(t *testing.T) {
	e, st, _, mb := setup(t)
	ctx := context.Background()
	node, agent := collectedSite(t, st)
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	ping, snmp := node.Checks[0].ID, node.Checks[1].ID
	send := func(id int64, ok bool) {
		t.Helper()
		if _, err := e.IngestCollectorResults(ctx, agent, model.CollectorBatch{Results: []model.CollectorResult{collectorResult(id, ok, time.Now())}}); err != nil {
			t.Fatal(err)
		}
	}
	send(ping, false)
	send(ping, false)
	send(snmp, true)
	waitFor(t, func() bool { return mb.count() == 1 }) // the ping's own down alert

	quiet := func(after time.Duration) {
		e.mu.Lock()
		evs, mails := e.watchCollectorsLocked(time.Now().Add(after))
		e.mu.Unlock()
		for _, ev := range evs {
			e.recordEvent(ev)
		}
		for _, m := range mails {
			e.sendMail(m)
		}
	}
	quiet(time.Minute) // within the grace: nothing
	if got := eventsOfType(t, st, model.EventCollectorDown); len(got) != 0 {
		t.Fatal("called unreachable inside its grace period")
	}
	quiet(10 * time.Minute)
	quiet(20 * time.Minute) // still quiet: no second event
	downs := eventsOfType(t, st, model.EventCollectorDown)
	if len(downs) != 1 || !strings.Contains(downs[0].Detail, "2 check(s)") {
		t.Fatalf("want one unreachable event naming both checks, got %+v", downs)
	}
	waitFor(t, func() bool { return mb.count() == 2 })
	if subj := mb.subjects()[1]; !strings.Contains(subj, "collector") && !strings.Contains(subj, "Collector") {
		t.Errorf("the alert should be about the collector: %q", subj)
	}
	for _, id := range []int64{ping, snmp} {
		s, _ := e.State(id)
		if s.Status != model.StatusUnknown || s.SuppressReason != suppressCollector || !strings.Contains(s.LastMessage, "office-pi") {
			t.Fatalf("check %d while its collector is quiet: %+v", id, s)
		}
	}
	if st, _ := e.CollectorState(agent); st != model.CollectorUnreachable {
		t.Fatalf("collector state %q", st)
	}

	// Back: the ping is still failing. It is down again, as it was, and no
	// second down alert goes out for an outage already alerted.
	send(ping, false)
	if s, _ := e.State(ping); s.Status != model.StatusDown || s.SuppressReason != "" {
		t.Fatalf("the ping after the collector came back: %+v", s)
	}
	if got := eventsOfType(t, st, model.EventCollectorUp); len(got) != 1 {
		t.Fatalf("want one 'back' event, got %d", len(got))
	}
	if et := eventTypes(t, st, ping); et[model.EventDown] != 1 {
		t.Fatalf("the ping went down %d times; it went down once", et[model.EventDown])
	}
	time.Sleep(50 * time.Millisecond)
	// Two so far plus the collector's recovery notice (recovery notifications
	// are on by default); the ping itself sent nothing new.
	for _, s := range mb.subjects()[2:] {
		if strings.Contains(s, "Ping") {
			t.Fatalf("the ping alerted again: %v", mb.subjects())
		}
	}
	send(snmp, true)
	if s, _ := e.State(snmp); s.Status != model.StatusUp {
		t.Fatalf("SNMP after the collector came back: %+v", s)
	}
}

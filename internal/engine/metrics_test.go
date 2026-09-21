package engine

import (
	"context"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
)

// hardwareResult is what a hardware check reports when the given metrics are
// over their warning level: every metric listed, each with its own verdict,
// the check's status the worst of them.
func hardwareResult(warn ...string) model.Result {
	over := map[string]bool{}
	for _, k := range warn {
		over[k] = true
	}
	r := model.Result{Timestamp: time.Now(), Success: true, Status: model.StatusUp, Message: "CPU 12%, memory 40%", Attempts: 1, Metrics: map[string]float64{}}
	for _, m := range []struct{ key, label string }{{"cpu", "Processor use"}, {"memory", "Memory use"}, {"disk:/srv", "Disk /srv"}} {
		mr := model.MetricResult{Key: m.key, Label: m.label, Value: 40, Unit: "%", Status: model.StatusUp}
		if over[m.key] {
			mr.Value, mr.Status, mr.Reason = 91, model.StatusDegraded, m.label+" is 91%, at or above the 90% warning threshold"
			r.Warnings = append(r.Warnings, mr.Reason)
			r.Status = model.StatusDegraded
		}
		r.Metrics[m.key] = mr.Value
		r.Details.MetricResults = append(r.Details.MetricResults, mr)
	}
	return r
}

// #60: each of a hardware check's metrics has its own warning that begins and
// clears on its own timeline. Two metrics over their lines are two warning
// events, and one of them coming back clears only itself.
func TestMetricWarningsAreTrackedSeparately(t *testing.T) {
	e, st, net, mb := setup(t)
	ctx := context.Background()
	n, err := st.CreateNode(ctx, model.Node{Name: "NAS", Host: "nas.local", Enabled: true, Checks: []model.Check{{Type: model.CheckSystem, Name: "Hardware", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5, Config: model.SystemDefaults()}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	cid := n.Checks[0].ID

	var next model.Result
	net.mu.Lock()
	net.custom = map[int64]func() model.Result{cid: func() model.Result { return next }}
	net.mu.Unlock()
	setNext := func(r model.Result) {
		net.mu.Lock()
		next = r
		net.mu.Unlock()
	}

	setNext(hardwareResult())
	if _, err := e.RunNow(ctx, cid); err != nil {
		t.Fatal(err)
	}
	if s, _ := e.State(cid); s.Status != model.StatusUp || len(s.MetricStatus) != 0 {
		t.Fatalf("healthy: %+v", s)
	}

	// Two metrics cross their warning level in the same run.
	setNext(hardwareResult("memory", "disk:/srv"))
	e.RunNow(ctx, cid)
	s, _ := e.State(cid)
	if s.Status != model.StatusDegraded {
		t.Fatalf("status = %s, want degraded", s.Status)
	}
	if s.MetricStatus["memory"] != model.StatusDegraded || s.MetricStatus["disk:/srv"] != model.StatusDegraded || len(s.MetricStatus) != 2 {
		t.Fatalf("metric status = %v", s.MetricStatus)
	}
	if s.WarningActive {
		t.Error("a check judged per metric does not also carry the check-level warning flag")
	}
	warnings := metricEvents(t, st, cid, model.EventWarning)
	if len(warnings) != 2 || !warnings["memory"] || !warnings["disk:/srv"] {
		t.Fatalf("warning events by metric = %v, want memory and disk:/srv", warnings)
	}
	// One warning email for everything that began this run, naming both.
	waitFor(t, func() bool { return mb.count() == 1 })

	// Memory comes back; the disk stays over. Only memory's warning clears,
	// and no new warning is raised for the disk.
	setNext(hardwareResult("disk:/srv"))
	e.RunNow(ctx, cid)
	s, _ = e.State(cid)
	if s.Status != model.StatusDegraded || len(s.MetricStatus) != 1 || s.MetricStatus["disk:/srv"] != model.StatusDegraded {
		t.Fatalf("after memory recovered: %+v", s)
	}
	cleared := metricEvents(t, st, cid, model.EventWarningCleared)
	if len(cleared) != 1 || !cleared["memory"] {
		t.Fatalf("cleared events by metric = %v, want memory only", cleared)
	}
	if again := metricEvents(t, st, cid, model.EventWarning); len(again) != 2 {
		t.Fatalf("a still-open warning must not be raised again: %v", again)
	}

	// Everything back to normal: the disk clears, the check is up.
	setNext(hardwareResult())
	e.RunNow(ctx, cid)
	s, _ = e.State(cid)
	if s.Status != model.StatusUp || len(s.MetricStatus) != 0 {
		t.Fatalf("after full recovery: %+v", s)
	}
	cleared = metricEvents(t, st, cid, model.EventWarningCleared)
	if len(cleared) != 2 || !cleared["disk:/srv"] {
		t.Fatalf("cleared events by metric = %v, want memory and disk:/srv", cleared)
	}
	time.Sleep(50 * time.Millisecond)
	if mb.count() != 1 {
		t.Errorf("clearing sends no mail, got %d", mb.count())
	}
}

// metricEvents returns which metrics have an event of the given type.
func metricEvents(t *testing.T, st *store.Store, cid int64, typ model.EventType) map[string]bool {
	t.Helper()
	evs, err := st.ListEvents(context.Background(), store.EventFilter{Limit: 500, CheckID: &cid, Types: []model.EventType{typ}})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, e := range evs {
		if e.Type == typ {
			out[e.Metric] = true
		}
	}
	return out
}

// The trigger side of #60: a metric_over condition fires on one metric's
// value, and the placeholders name the metric an event is about.
func TestMetricTriggerConditionAndVars(t *testing.T) {
	r := hardwareResult("disk:/srv")
	c := model.Check{ID: 3, Type: model.CheckSystem, Name: "Hardware"}
	n := model.Node{ID: 1, Name: "NAS"}
	tc := triggerContext{check: c, node: n, result: r, state: model.CheckState{Status: model.StatusDegraded}, events: []model.Event{
		{Type: model.EventWarning, Metric: "disk:/srv", Title: "Disk /srv warning"},
	}}

	over := model.Trigger{On: []string{"metric_over"}, Metric: "disk:/srv", MetricOver: 90}
	if met := conditionsMet(over, tc); len(met) != 1 || met[0] != "metric_over" {
		t.Errorf("91 > 90 should meet metric_over, got %v", met)
	}
	over.MetricOver = 95
	if met := conditionsMet(over, tc); len(met) != 0 {
		t.Errorf("91 is not over 95, got %v", met)
	}
	over.Metric = "nonesuch"
	over.MetricOver = 0
	if met := conditionsMet(over, tc); len(met) != 0 {
		t.Errorf("an unmeasured metric never fires, got %v", met)
	}
	// A metric this check does not measure at all: nothing to compare.
	if met := conditionsMet(model.Trigger{On: []string{"metric_over"}, MetricOver: 1}, tc); len(met) != 0 {
		t.Errorf("no metric named means off, got %v", met)
	}
	// The generic "degraded" condition is met by a metric-scoped warning.
	if met := conditionsMet(model.Trigger{On: []string{"degraded"}}, tc); len(met) != 1 {
		t.Errorf("a metric warning is a degraded event, got %v", met)
	}

	vars := TriggerVars(n, c, r, tc.state, "degraded")
	if vars["metrics.cpu"] != "40" || vars["metrics.disk__srv"] != "91" {
		t.Errorf("flattened metrics: cpu=%q disk=%q", vars["metrics.cpu"], vars["metrics.disk__srv"])
	}
	if vars["metric"] != "" {
		t.Errorf("no firing metric until withMetric: %q", vars["metric"])
	}
	withMetric(vars, r, firingMetric(model.Trigger{On: []string{"degraded"}}, tc, "degraded"))
	if vars["metric"] != "disk:/srv" || vars["metric.label"] != "Disk /srv" || vars["metric.value"] != "91" || vars["metric.status"] != "degraded" {
		t.Errorf("firing metric vars: %q %q %q %q", vars["metric"], vars["metric.label"], vars["metric.value"], vars["metric.status"])
	}
	// metric_over names its own metric, whatever the events say.
	vars = TriggerVars(n, c, r, tc.state, "metric_over")
	withMetric(vars, r, firingMetric(model.Trigger{Metric: "cpu"}, tc, "metric_over"))
	if vars["metric"] != "cpu" || vars["metric.value"] != "40" || vars["metric.status"] != "up" {
		t.Errorf("metric_over vars: %q %q %q", vars["metric"], vars["metric.value"], vars["metric.status"])
	}
	if MetricVarName("net:eth0.rx") != "net_eth0.rx" || MetricVarName("disk:/srv") != "disk__srv" {
		t.Errorf("var names: %q %q", MetricVarName("net:eth0.rx"), MetricVarName("disk:/srv"))
	}
}

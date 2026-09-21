package store

import (
	"context"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// #60: an event about one metric of a check names it, a check's state keeps
// each metric's last verdict, and a trigger can watch one — all of which must
// survive the round trip through the database.
func TestMetricFieldsRoundTrip(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	n, err := s.CreateNode(ctx, model.Node{Name: "NAS", Host: "nas.local", Enabled: true, Checks: []model.Check{{
		Type: model.CheckSystem, Name: "Hardware", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5, Config: model.SystemDefaults(),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	cid := n.Checks[0].ID

	// Events, through both insert paths.
	ev, err := s.InsertEvent(ctx, model.Event{Type: model.EventWarning, CheckID: &cid, NodeID: &n.ID, Title: "Disk /srv warning", Detail: "Disk /srv is 88%", Metric: "disk:/srv"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.InsertEventsBatch(ctx, []model.Event{{Timestamp: time.Now(), Type: model.EventWarningCleared, CheckID: &cid, Title: "Memory back to normal", Metric: "memory"}}); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListEvents(ctx, EventFilter{CheckID: &cid, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	got := map[model.EventType]string{}
	for _, e := range list {
		got[e.Type] = e.Metric
	}
	if got[model.EventWarning] != "disk:/srv" || got[model.EventWarningCleared] != "memory" {
		t.Fatalf("event metrics = %v (inserted id %d)", got, ev.ID)
	}
	// A check-level event has none, and comes back with none.
	plain, _ := s.InsertEvent(ctx, model.Event{Type: model.EventDown, CheckID: &cid, Title: "Down"})
	list, _ = s.ListEvents(ctx, EventFilter{CheckID: &cid, Limit: 10})
	for _, e := range list {
		if e.ID == plain.ID && e.Metric != "" {
			t.Errorf("a check-level event should carry no metric, got %q", e.Metric)
		}
	}

	// Check state: the per-metric verdicts.
	st := model.CheckState{CheckID: cid, Status: model.StatusDegraded, MetricStatus: map[string]model.Status{"disk:/srv": model.StatusDegraded, "memory": model.StatusDown}}
	if err := s.SaveState(ctx, st); err != nil {
		t.Fatal(err)
	}
	back, err := s.GetState(ctx, cid)
	if err != nil {
		t.Fatal(err)
	}
	if back.MetricStatus["disk:/srv"] != model.StatusDegraded || back.MetricStatus["memory"] != model.StatusDown || len(back.MetricStatus) != 2 {
		t.Fatalf("metric status = %v", back.MetricStatus)
	}
	all, _ := s.ListStates(ctx)
	if all[cid].MetricStatus["memory"] != model.StatusDown {
		t.Errorf("ListStates should carry the map too: %v", all[cid].MetricStatus)
	}
	// Clearing it clears the column rather than leaving the old map behind.
	st.MetricStatus = nil
	if err := s.SaveState(ctx, st); err != nil {
		t.Fatal(err)
	}
	if back, _ = s.GetState(ctx, cid); len(back.MetricStatus) != 0 {
		t.Errorf("cleared metric status came back as %v", back.MetricStatus)
	}

	// Triggers: the metric_over condition's key and level.
	tr, err := s.SaveTrigger(ctx, model.Trigger{NodeID: n.ID, Name: "Disk hot", Enabled: true, On: []string{"metric_over"}, Metric: "disk:/srv", MetricOver: 90, Action: model.Action{Type: model.ActionHTTP, URL: "https://example.com/hook"}})
	if err != nil {
		t.Fatal(err)
	}
	tr2, err := s.GetTrigger(ctx, tr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if tr2.Metric != "disk:/srv" || tr2.MetricOver != 90 {
		t.Fatalf("trigger metric = %q over %v", tr2.Metric, tr2.MetricOver)
	}
	tr2.MetricOver = 95
	if _, err := s.SaveTrigger(ctx, tr2); err != nil {
		t.Fatal(err)
	}
	if tr3, _ := s.GetTrigger(ctx, tr.ID); tr3.MetricOver != 95 {
		t.Errorf("update lost the level: %v", tr3.MetricOver)
	}
}

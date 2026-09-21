package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// A rule round-trips with its conditions and actions intact, its state is
// a separate row that survives a re-save and goes with the rule when it is
// deleted, and ClearAll takes both tables with it.
func TestRulesCRUDAndState(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	n, err := s.CreateNode(ctx, model.Node{Name: "Pi-hole", Host: "192.168.1.2", Enabled: true,
		Checks: []model.Check{{Type: model.CheckDNS, Name: "DNS", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5}}})
	if err != nil {
		t.Fatal(err)
	}
	checkID := n.Checks[0].ID
	r, err := s.SaveRule(ctx, model.Rule{Name: "Two DNS servers down", Enabled: true, Join: model.RuleJoinAtLeast, AtLeast: 2,
		Conditions: []model.RuleCondition{
			{Kind: model.RuleConditionStatus, CheckID: &checkID, Status: model.StatusDown},
			{Kind: model.RuleConditionStatus, NodeID: &n.ID, Status: model.StatusDegraded},
		},
		Actions:         []model.Action{{Type: model.ActionHTTP, URL: "https://example.com/hook", Method: "POST", Body: "{{message}}"}, {Type: model.ActionNtfy, Topic: "gwatch"}},
		CooldownMinutes: 15, NotifyCleared: true})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if r.ID == 0 || r.CreatedAt.IsZero() {
		t.Fatalf("id or timestamps not assigned: %+v", r)
	}
	got, err := s.GetRule(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Two DNS servers down" || !got.Enabled || got.Join != model.RuleJoinAtLeast || got.AtLeast != 2 || got.CooldownMinutes != 15 || !got.NotifyCleared {
		t.Fatalf("fields: %+v", got)
	}
	if len(got.Conditions) != 2 || got.Conditions[0].CheckID == nil || *got.Conditions[0].CheckID != checkID || got.Conditions[0].NodeID != nil ||
		got.Conditions[1].NodeID == nil || *got.Conditions[1].NodeID != n.ID || got.Conditions[1].Status != model.StatusDegraded {
		t.Fatalf("conditions: %+v", got.Conditions)
	}
	if len(got.Actions) != 2 || got.Actions[0].Type != model.ActionHTTP || got.Actions[0].Body != "{{message}}" || got.Actions[1].Type != model.ActionNtfy {
		t.Fatalf("actions: %+v", got.Actions)
	}

	// State: absent until put, then upserted.
	states, err := s.RuleStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 0 {
		t.Fatalf("expected no state yet, got %+v", states)
	}
	since := time.Now().Add(-time.Minute).Round(time.Millisecond)
	if err := s.PutRuleState(ctx, model.RuleState{RuleID: r.ID, Met: true, Since: &since}); err != nil {
		t.Fatal(err)
	}
	fired := time.Now().Round(time.Millisecond)
	if err := s.PutRuleState(ctx, model.RuleState{RuleID: r.ID, Met: true, Since: &since, LastFiredAt: &fired}); err != nil {
		t.Fatal(err)
	}
	states, _ = s.RuleStates(ctx)
	st, ok := states[r.ID]
	if !ok || !st.Met || st.Since == nil || !st.Since.Equal(since) || st.LastFiredAt == nil || !st.LastFiredAt.Equal(fired) {
		t.Fatalf("state: %+v (ok=%v)", st, ok)
	}

	// Editing the rule leaves the state alone; the list is by name.
	got.Name = "DNS servers down"
	got.Enabled = false
	got.Join = model.RuleJoinAny
	if _, err := s.SaveRule(ctx, got); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveRule(ctx, model.Rule{Name: "Another", Join: model.RuleJoinAll, Conditions: []model.RuleCondition{{Kind: "status", NodeID: &n.ID, Status: model.StatusDown}}, Actions: []model.Action{{Type: model.ActionSlack, WebhookURL: "https://hooks.slack.com/x"}}}); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "Another" || list[1].Name != "DNS servers down" || list[1].Enabled || list[1].Join != model.RuleJoinAny {
		t.Fatalf("list: %+v", list)
	}
	states, _ = s.RuleStates(ctx)
	if !states[r.ID].Met {
		t.Fatalf("state lost on re-save: %+v", states)
	}

	// Deleting takes the state with it; deleting again is not found.
	if err := s.DeleteRule(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteRule(ctx, r.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	if _, err := s.GetRule(ctx, r.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete: %v", err)
	}
	states, _ = s.RuleStates(ctx)
	if _, ok := states[r.ID]; ok {
		t.Fatalf("state survived the delete: %+v", states)
	}

	if err := s.ClearAll(ctx, false); err != nil {
		t.Fatal(err)
	}
	list, _ = s.ListRules(ctx)
	if len(list) != 0 {
		t.Fatalf("rules survived ClearAll: %+v", list)
	}
}

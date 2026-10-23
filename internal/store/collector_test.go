package store

import (
	"context"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// A node's "check from" and an agent's collector settings are kept, changed,
// cleared, carried through a restore and through an agent export.
func TestCollectorSettingsPersist(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	agent, err := s.CreateAgent(ctx, "branch", nil, "gwa_b", "hash-collector-1", "test")
	if err != nil {
		t.Fatal(err)
	}
	id := agent.ID
	n, err := s.CreateNode(ctx, model.Node{Name: "Router", Host: "10.1.0.1", Enabled: true, CollectorID: &id})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetNode(ctx, n.ID)
	if err != nil || got.CollectorID == nil || *got.CollectorID != id {
		t.Fatalf("collector not kept: %v %+v", err, got.CollectorID)
	}
	got.CollectorID = nil
	if _, _, err := s.UpdateNode(ctx, got); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.GetNode(ctx, n.ID); again.CollectorID != nil {
		t.Fatal("clearing the collector did not stick")
	}
	got.CollectorID = &id
	if err := s.RestoreNodes(ctx, []model.Node{got}, false); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.GetNode(ctx, n.ID); again.CollectorID == nil || *again.CollectorID != id {
		t.Fatal("a restore lost the collector")
	}

	if err := s.SetAgentCollector(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordAgentCheckIn(ctx, id, nil, true); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := s.TouchCollector(ctx, id, now); err != nil {
		t.Fatal(err)
	}
	a, _ := s.GetAgent(ctx, id)
	if !a.Collector || !a.CollectorAllowed || a.CollectorSeenAt == nil {
		t.Fatalf("agent collector fields: %+v", a)
	}
	recs, err := s.ExportAgents(ctx)
	if err != nil || len(recs) != 1 || !recs[0].Collector {
		t.Fatalf("export lost the collector switch: %v %+v", err, recs)
	}
	if err := s.SetAgentCollector(ctx, 99999, true); err != ErrNotFound {
		t.Fatalf("a missing agent: %v", err)
	}
}

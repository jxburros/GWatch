package store

import (
	"context"
	"github.com/jxburros/GWatch/internal/model"
	"testing"
	"time"
)

func TestIncidentExportExceedsDisplayLimit(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	n, err := s.CreateNode(ctx, model.Node{Name: "history", Host: "localhost"})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	raw := jsonString(model.Incident{NodeID: n.ID, State: "resolved", OpenedAt: at, ResolvedAt: &at})
	err = s.writeTx(ctx, func(tx *wtx) error {
		for i := 0; i < 5001; i++ {
			if _, err := tx.insertID(ctx, "INSERT INTO incidents(node_id,active_node_id,state,opened_at,document) VALUES(?,?,?,?,?)", n.ID, nil, "resolved", fmtTime(at), raw); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	display, err := s.ListIncidents(ctx, "all")
	if err != nil || len(display) != 5000 {
		t.Fatalf("display rows %d %v", len(display), err)
	}
	exported, err := s.ExportIncidents(ctx)
	if err != nil || len(exported) != 5001 {
		t.Fatalf("export lost incident rows: %d %v", len(exported), err)
	}
}

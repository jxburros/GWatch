package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// Acknowledging an incident that is already over conflicts with its state; it
// is not a server error. A missing incident is a 404.
func TestAcknowledgingAResolvedIncidentIsAConflict(t *testing.T) {
	ts, srv := newTestServer(t)
	ctx := context.Background()
	n, err := srv.Store.CreateNode(ctx, model.Node{Name: "router", Host: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	opened := time.Now().Add(-time.Hour)
	resolved := opened.Add(time.Minute)
	if err := srv.Store.RestoreIncidents(ctx, []model.Incident{{NodeID: n.ID, State: "resolved", OpenedAt: opened, ResolvedAt: &resolved}}); err != nil {
		t.Fatal(err)
	}
	list, err := srv.Store.ListIncidents(ctx, "all")
	if err != nil || len(list) != 1 {
		t.Fatalf("incidents: %v %d", err, len(list))
	}
	id := itoa(list[0].ID)

	if code, body := callBody(t, ts, "POST", "/api/incidents/"+id+"/acknowledge", map[string]any{}); code != http.StatusConflict {
		t.Fatalf("acknowledging a resolved incident: %d %s", code, body)
	}
	if code, body := callBody(t, ts, "POST", "/api/incidents/"+id+"/note", map[string]any{"note": "post-mortem filed"}); code != http.StatusOK {
		t.Fatalf("a note on a resolved incident should still be taken: %d %s", code, body)
	}
	if code, _ := callBody(t, ts, "POST", "/api/incidents/"+itoa(list[0].ID+1000)+"/resolve", map[string]any{}); code != http.StatusNotFound {
		t.Fatalf("a missing incident should be a 404, got %d", code)
	}
}

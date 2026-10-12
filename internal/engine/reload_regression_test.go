package engine

import (
	"context"
	"testing"

	"github.com/jxburros/GWatch/internal/model"
)

func TestReloadAfterClientDisconnect(t *testing.T) {
	e, st, _, _ := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	n, err := st.CreateNode(ctx, model.Node{Name: "Saved before disconnect", Enabled: true, Checks: []model.Check{{Name: "Ping", Type: model.CheckPing, Enabled: true}}})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := e.ReloadConfig(ctx); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.nodes[n.ID].Name != n.Name {
		t.Fatal("committed node was not loaded")
	}
}

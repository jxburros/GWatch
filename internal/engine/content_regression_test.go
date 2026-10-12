package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jxburros/GWatch/internal/checks"
	"github.com/jxburros/GWatch/internal/model"
)

func TestEmptyContentBaselineSurvivesEngineReload(t *testing.T) {
	e, st, _, _ := setup(t)
	value := "v1"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if value != "" {
			w.Header().Set("X-Version", value)
		}
		w.Write([]byte("ok"))
	}))
	defer server.Close()
	ctx := context.Background()
	n, err := st.CreateNode(ctx, model.Node{Name: "watched", Enabled: true, Checks: []model.Check{{Name: "version", Type: model.CheckHTTP, Enabled: true, TimeoutSeconds: 5, Config: model.CheckConfig{Target: server.URL, ContentWatch: "header", ContentHeader: "X-Version"}}}})
	if err != nil {
		t.Fatal(err)
	}
	e.opts.Run = checks.Run
	if err = e.ReloadConfig(ctx); err != nil {
		t.Fatal(err)
	}
	id := n.Checks[0].ID
	for _, v := range []string{"v1", "", "", ""} {
		value = v
		if _, err = e.RunNow(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if got := eventTypes(t, st, id)[model.EventContentChanged]; got != 1 {
		t.Fatalf("content events: %d", got)
	}
	// A fresh engine has to load the empty baseline from the database.
	next := New(st, e.log, e.opts)
	if err = next.ReloadConfig(ctx); err != nil {
		t.Fatal(err)
	}
	if r, err := next.RunNow(ctx, id); err != nil || r.Details.ContentChanged {
		t.Fatalf("empty after restart: %+v %v", r, err)
	}
	value = "v2"
	if r, err := next.RunNow(ctx, id); err != nil || !r.Details.ContentChanged {
		t.Fatalf("reappeared value: %+v %v", r, err)
	}
	if got := eventTypes(t, st, id)[model.EventContentChanged]; got != 2 {
		t.Fatalf("content events after reappearance: %d", got)
	}
}

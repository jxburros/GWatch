package api

import (
	"context"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/jxburros/GWatch/internal/engine"
	"github.com/jxburros/GWatch/internal/logging"
	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store/storetest"
)

// Exercise the actual hardware runner through the editor's Test endpoint.
// Seeding the monitor keeps the result independent of host load and sampler
// timing; the engine remains stopped so no background collection can replace it.
func TestLocalHardwareCheckThroughAPI(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	log, err := logging.New("", nil)
	if err != nil {
		t.Fatal(err)
	}
	eng := engine.New(st, log, engine.Options{})
	metrics := reading("local-hardware-fixture", 12)
	metrics.Key = model.HostKeyLocal
	if err := eng.Hosts().Record(ctx, metrics); err != nil {
		t.Fatal(err)
	}
	srv := &Server{Engine: eng, Store: st, Log: log, DataDir: t.TempDir(), Web: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html>app</html>")}}}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	cfg := model.SystemDefaults()
	cfg.HostSource = model.HostSourceLocal
	check := model.Check{Type: model.CheckSystem, Name: "Hardware", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5, Config: cfg}
	var result model.Result
	if code := call(t, ts, "POST", "/api/checks/test", map[string]any{"check": check, "nodeHost": "localhost"}, &result); code != 200 {
		t.Fatalf("test route returned HTTP %d", code)
	}
	if !result.Success || result.Status != model.StatusUp || result.Error != "" {
		t.Fatalf("local hardware Test failed: %+v", result)
	}
	if result.Details.Host == nil || result.Details.Host.Hostname != metrics.Hostname || result.Metrics["cpu"] != 12 || result.Metrics["memory"] != 30 {
		t.Fatalf("Test did not evaluate the monitor's reading: %+v", result)
	}
	raw, _, _, _, _, err := st.Counts(ctx)
	if err != nil || raw != 0 {
		t.Fatalf("testing an unsaved check recorded results: count=%d err=%v", raw, err)
	}
}

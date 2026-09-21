package api

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// createHardwareNode saves a node with one hardware check and returns it as
// the API sees it.
func createHardwareNode(t *testing.T, ts *httptest.Server, cfg model.CheckConfig) nodeDoc {
	t.Helper()
	node := model.Node{Name: "NAS", Host: "nas.local", Enabled: true, Importance: model.ImportanceNormal, Checks: []model.Check{{
		Type: model.CheckSystem, Name: "Hardware", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5, Config: cfg,
	}}}
	var created nodeDoc
	if code := call(t, ts, "POST", "/api/nodes", node, &created); code != 201 {
		t.Fatalf("create = %d", code)
	}
	return created
}

// #60: a hardware check's metrics are served by /api/history like any other
// named metric — the four every machine has from the configuration, and a
// disk or interface from whatever the check last reported.
func TestHistoryServesHardwareMetrics(t *testing.T) {
	ts, srv := newTestServer(t)
	ctx := context.Background()
	created := createHardwareNode(t, ts, model.SystemDefaults())
	checkID := created.Checks[0].ID

	// Before any result the four singletons are measured, a disk is not.
	var series model.HistorySeries
	if code := call(t, ts, "GET", fmt.Sprintf("/api/history?checkId=%d&range=24h&metric=cpu", checkID), nil, &series); code != 200 {
		t.Fatalf("cpu before results = %d", code)
	}
	if series.Metric != "cpu" || series.MetricUnit != "%" || len(series.Points) != 0 {
		t.Fatalf("series = %+v", series)
	}
	if code := call(t, ts, "GET", fmt.Sprintf("/api/history?checkId=%d&metric=disk:/srv", checkID), nil, nil); code != 400 {
		t.Fatalf("an unreported disk = %d, want 400", code)
	}

	if _, err := srv.Store.InsertResult(ctx, model.Result{
		CheckID: checkID, Timestamp: time.Now().Add(-time.Minute), Success: true, Status: model.StatusUp,
		Metrics: map[string]float64{"cpu": 12, "memory": 40, "disk:/srv": 88, "net:eth0.rx": 1500},
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if code := call(t, ts, "GET", fmt.Sprintf("/api/history?checkId=%d&range=24h&metric=cpu", checkID), nil, &series); code != 200 {
		t.Fatalf("cpu = %d", code)
	}
	if len(series.Points) != 1 || series.Points[0].Value == nil || *series.Points[0].Value != 12 {
		t.Fatalf("cpu points = %+v", series.Points)
	}
	// A key the newest result carried counts as measured, with its unit
	// worked out from the key.
	if code := call(t, ts, "GET", fmt.Sprintf("/api/history?checkId=%d&range=24h&metric=disk:/srv", checkID), nil, &series); code != 200 {
		t.Fatalf("disk = %d", code)
	}
	if series.MetricUnit != "%" || *series.Points[0].Value != 88 {
		t.Fatalf("disk series = %+v", series)
	}
	if code := call(t, ts, "GET", fmt.Sprintf("/api/history?checkId=%d&range=24h&metric=net:eth0.rx", checkID), nil, &series); code != 200 || series.MetricUnit != "B/s" {
		t.Fatalf("net = %d %q", code, series.MetricUnit)
	}
	// A name nothing measured is still refused.
	if code := call(t, ts, "GET", fmt.Sprintf("/api/history?checkId=%d&metric=gpu", checkID), nil, nil); code != 400 {
		t.Fatalf("unmeasured = %d, want 400", code)
	}
	// So is a metric another check reported: the gate is per check.
	other := createHardwareNode(t, ts, model.SystemDefaults())
	if code := call(t, ts, "GET", fmt.Sprintf("/api/history?checkId=%d&metric=disk:/srv", other.Checks[0].ID), nil, nil); code != 400 {
		t.Fatalf("another check's disk = %d, want 400", code)
	}
	// The CSV export goes through the same gate.
	if code := call(t, ts, "GET", fmt.Sprintf("/api/export/history.csv?checkId=%d&range=24h&metric=disk:/srv", checkID), nil, nil); code != 200 {
		t.Fatalf("csv = %d", code)
	}
}

// A hardware check saved with the flat threshold fields of an older
// configuration comes back on the per-metric list, and one saved with none
// gets the defaults there.
func TestSavingAHardwareCheckNormalisesItsThresholds(t *testing.T) {
	ts, _ := newTestServer(t)

	legacy := createHardwareNode(t, ts, model.CheckConfig{HostSource: model.HostSourceLocal, CPUWarnPct: 80, DiskWarnPct: 70, DiskCritPct: 90})
	cfg := legacy.Checks[0].Config
	if cfg.CPUWarnPct != 0 || cfg.DiskWarnPct != 0 || cfg.DiskCritPct != 0 {
		t.Errorf("flat fields should be cleared on save: %+v", cfg)
	}
	byKey := map[string]model.MetricThreshold{}
	for _, mt := range cfg.MetricThresholds {
		byKey[mt.Metric] = mt
	}
	if *byKey["cpu"].Warn != 80 || *byKey["disk"].Warn != 70 || *byKey["disk"].Crit != 90 || *byKey["inodes"].Crit != 90 || len(byKey) != 3 {
		t.Errorf("converted list = %+v", cfg.MetricThresholds)
	}
	// The list is stored sorted families first, so it reads the same twice.
	if cfg.MetricThresholds[0].Metric != "cpu" || cfg.MetricThresholds[1].Metric != "disk" || cfg.MetricThresholds[2].Metric != "inodes" {
		t.Errorf("order = %v", cfg.MetricThresholds)
	}

	bare := createHardwareNode(t, ts, model.CheckConfig{HostSource: model.HostSourceLocal})
	if got := len(bare.Checks[0].Config.MetricThresholds); got != len(model.SystemDefaults().MetricThresholds) {
		t.Errorf("a check saved without thresholds gets the defaults, got %d entries", got)
	}

	// The list is validated on save, with the same rules as the flat fields.
	bad := model.Node{Name: "Bad", Host: "x", Enabled: true, Checks: []model.Check{{
		Type: model.CheckSystem, Name: "Hardware", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5,
		Config: model.CheckConfig{MetricThresholds: []model.MetricThreshold{{Metric: "memory", Warn: model.Float(90), Crit: model.Float(50)}}},
	}}}
	if code := call(t, ts, "POST", "/api/nodes", bad, nil); code != 400 {
		t.Errorf("critical below warning = %d, want 400", code)
	}
}

// A bulk edit sets one metric's thresholds across many hardware checks,
// merging by key so the rest of each check's list is left alone — and it
// leaves every other check type untouched.
func TestBulkEditMergesMetricThresholds(t *testing.T) {
	ts, _ := newTestServer(t)
	a := createHardwareNode(t, ts, model.SystemDefaults())
	b := createHardwareNode(t, ts, model.SystemDefaults())
	// A ping check in the same selection: the key means nothing to it.
	var ping nodeDoc
	call(t, ts, "POST", "/api/nodes", map[string]any{"name": "Router", "host": "10.0.0.1", "enabled": true,
		"checks": []map[string]any{{"type": "ping", "name": "Ping", "enabled": true, "intervalSeconds": 60, "timeoutSeconds": 5, "config": map[string]any{"pingCount": 4}}}}, &ping)

	var res bulkResponse
	body := map[string]any{
		"nodeIds": []int64{a.ID, b.ID, ping.ID},
		"check": map[string]any{"config": map[string]any{
			"metricThresholds": []map[string]any{{"metric": "disk", "warn": 90, "crit": 98}, {"metric": "disk:/srv", "warn": 60}},
		}},
	}
	if code := call(t, ts, "PATCH", "/api/nodes/bulk", body, &res); code != 200 {
		t.Fatalf("bulk = %d", code)
	}
	if res.Checks != 3 {
		t.Errorf("checks touched = %d, want 3 (the ping check is in the selection, if unchanged by this key)", res.Checks)
	}
	for _, id := range []int64{a.ID, b.ID} {
		var n nodeDoc
		call(t, ts, "GET", fmt.Sprintf("/api/nodes/%d", id), nil, &n)
		byKey := map[string]model.MetricThreshold{}
		for _, mt := range n.Checks[0].Config.MetricThresholds {
			byKey[mt.Metric] = mt
		}
		if *byKey["disk"].Warn != 90 || *byKey["disk"].Crit != 98 {
			t.Errorf("node %d disk = %+v", id, byKey["disk"])
		}
		if *byKey["disk:/srv"].Warn != 60 || byKey["disk:/srv"].Crit != nil {
			t.Errorf("node %d disk:/srv = %+v", id, byKey["disk:/srv"])
		}
		// The memory pair the patch never mentioned is as the defaults left it.
		if *byKey["memory"].Warn != 90 || *byKey["memory"].Crit != 97 {
			t.Errorf("node %d memory should be untouched: %+v", id, byKey["memory"])
		}
	}
	var r nodeDoc
	call(t, ts, "GET", fmt.Sprintf("/api/nodes/%d", ping.ID), nil, &r)
	if len(r.Checks[0].Config.MetricThresholds) != 0 {
		t.Errorf("a ping check gained hardware thresholds: %+v", r.Checks[0].Config.MetricThresholds)
	}

	// The same validation as the editor: a critical level under its warning
	// is refused and nothing is written.
	body["check"] = map[string]any{"config": map[string]any{"metricThresholds": []map[string]any{{"metric": "memory", "warn": 90, "crit": 50}}}}
	if code := call(t, ts, "PATCH", "/api/nodes/bulk", body, nil); code != 400 {
		t.Errorf("bad thresholds = %d, want 400", code)
	}
}

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jxburros/GWatch/internal/checks"
	"github.com/jxburros/GWatch/internal/engine"
	"github.com/jxburros/GWatch/internal/logging"
	"github.com/jxburros/GWatch/internal/mailer"
	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
	"github.com/jxburros/GWatch/internal/update"
)

func newTestServer(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log, _ := logging.New("", nil)
	run := func(ctx context.Context, c model.Check, o checks.Options) model.Result {
		lat := 5.0
		return model.Result{Timestamp: time.Now(), Success: true, Status: model.StatusUp, Message: "ok", LatencyMS: &lat, Attempts: 1, Details: model.ResultDetails{StatusCode: 200}}
	}
	send := func(ctx context.Context, s model.SMTPSettings, m mailer.Message) error { return nil }
	eng := engine.New(st, log, engine.Options{Version: "test", Run: run, Send: send})
	if err := eng.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Stop)
	web := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html>app</html>")},
		"app.js":     &fstest.MapFile{Data: []byte("//js")},
		"wall.html":  &fstest.MapFile{Data: []byte("<html>wall</html>")},
	}
	srv := &Server{Engine: eng, Store: st, Log: log, Web: web, BackupDir: filepath.Join(dir, "backups"), Version: "test", Updater: &Updater{Client: &update.Client{}, Version: "test", Log: log}}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, srv
}

func call(t *testing.T, ts *httptest.Server, method, path string, body any, out any) int {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, ts.URL+path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if out != nil && resp.StatusCode < 300 {
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("%s %s: bad json %q: %v", method, path, data, err)
		}
	}
	if resp.StatusCode >= 300 {
		t.Logf("%s %s -> %d %s", method, path, resp.StatusCode, data)
	}
	return resp.StatusCode
}

func TestNodeLifecycleAndHistory(t *testing.T) {
	ts, _ := newTestServer(t)
	var tmpl []model.NodeTemplate
	if code := call(t, ts, "GET", "/api/templates", nil, &tmpl); code != 200 || len(tmpl) < 4 {
		t.Fatalf("templates: %d %d", code, len(tmpl))
	}
	node := map[string]any{"name": "Router", "host": "192.168.1.1", "group": "Home Network", "tags": []string{"core", "core", " "}, "enabled": true, "importance": "critical",
		"checks": []map[string]any{{"type": "ping", "name": "Ping", "enabled": true, "intervalSeconds": 60, "timeoutSeconds": 5}, {"type": "tcp", "name": "TCP 80", "enabled": true, "intervalSeconds": 60, "config": map[string]any{"port": 80}}}}
	var created nodeDoc
	if code := call(t, ts, "POST", "/api/nodes", node, &created); code != 201 {
		t.Fatalf("create: %d", code)
	}
	if created.ID == 0 || len(created.Checks) != 2 || len(created.Tags) != 1 || created.Status != model.StatusUnknown {
		t.Fatalf("created: %+v", created)
	}
	// validation: bad interval
	bad := map[string]any{"name": "X", "host": "h", "enabled": true, "checks": []map[string]any{{"type": "ping", "intervalSeconds": 3}}}
	if code := call(t, ts, "POST", "/api/nodes", bad, nil); code != 400 {
		t.Fatalf("expected 400 for tiny interval, got %d", code)
	}
	// run now
	var results []model.Result
	if code := call(t, ts, "POST", fmt.Sprintf("/api/nodes/%d/run", created.ID), nil, &results); code != 200 || len(results) != 2 {
		t.Fatalf("run node: %d %d", code, len(results))
	}
	var got nodeDoc
	call(t, ts, "GET", fmt.Sprintf("/api/nodes/%d", created.ID), nil, &got)
	if got.Status != model.StatusUp || len(got.LastResults) != 2 {
		t.Fatalf("get after run: %+v", got.Status)
	}
	// history
	var series model.HistorySeries
	if code := call(t, ts, "GET", fmt.Sprintf("/api/history?checkId=%d&range=24h", created.Checks[0].ID), nil, &series); code != 200 || len(series.Points) != 1 || series.Source != "raw" {
		t.Fatalf("history: %d %+v", code, series)
	}
	if code := call(t, ts, "GET", "/api/history?checkId=1&range=2h", nil, nil); code != 400 {
		t.Fatalf("expected 400 for bad range, got %d", code)
	}
	// update: rename, drop tcp check
	got.Name = "Gateway"
	got.Checks = got.Checks[:1]
	var updated nodeDoc
	if code := call(t, ts, "PUT", fmt.Sprintf("/api/nodes/%d", created.ID), got.Node, &updated); code != 200 || updated.Name != "Gateway" || len(updated.Checks) != 1 {
		t.Fatalf("update: %d %+v", code, updated)
	}
	// dependency loop rejected
	var child nodeDoc
	call(t, ts, "POST", "/api/nodes", map[string]any{"name": "Plex", "host": "plex", "enabled": true, "dependsOnNodeId": created.ID, "checks": []map[string]any{}}, &child)
	updated.DependsOnNode = &child.ID
	if code := call(t, ts, "PUT", fmt.Sprintf("/api/nodes/%d", created.ID), updated.Node, nil); code != 400 {
		t.Fatalf("expected loop rejection, got %d", code)
	}
	// overview & groups
	var ov engine.Overview
	call(t, ts, "GET", "/api/overview", nil, &ov)
	if ov.Summary.Total != 2 || len(ov.Groups) != 2 {
		t.Fatalf("overview: %+v", ov.Summary)
	}
	var groups struct {
		Groups []struct{ Name string } `json:"groups"`
	}
	call(t, ts, "GET", "/api/groups", nil, &groups)
	if len(groups.Groups) != 1 || groups.Groups[0].Name != "Home Network" {
		t.Fatalf("groups: %+v", groups)
	}
	// events include config changes
	var events []model.Event
	call(t, ts, "GET", "/api/events?type=config_changed", nil, &events)
	if len(events) < 3 {
		t.Fatalf("expected config events, got %d", len(events))
	}
	// silence + enable
	var st model.CheckState
	if code := call(t, ts, "POST", fmt.Sprintf("/api/checks/%d/silence", updated.Checks[0].ID), map[string]int{"minutes": 60}, &st); code != 200 || st.SilencedUntil == nil {
		t.Fatalf("silence: %d %+v", code, st)
	}
	if code := call(t, ts, "POST", fmt.Sprintf("/api/nodes/%d/enable", created.ID), map[string]bool{"enabled": false}, &got); code != 200 || got.Status != model.StatusPaused {
		t.Fatalf("disable: %d %s", code, got.Status)
	}
	// duplicate, delete
	var dup nodeDoc
	if code := call(t, ts, "POST", fmt.Sprintf("/api/nodes/%d/duplicate", created.ID), nil, &dup); code != 201 || !strings.HasSuffix(dup.Name, "(copy)") {
		t.Fatalf("duplicate: %d %+v", code, dup)
	}
	if code := call(t, ts, "DELETE", fmt.Sprintf("/api/nodes/%d", dup.ID), nil, nil); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	// test check (unsaved)
	var tr model.Result
	if code := call(t, ts, "POST", "/api/checks/test", map[string]any{"check": map[string]any{"type": "http", "config": map[string]any{"target": "https://example.com"}}, "nodeHost": ""}, &tr); code != 200 || !tr.Success {
		t.Fatalf("test check: %d %+v", code, tr)
	}
	// CSV export
	resp, err := http.Get(ts.URL + fmt.Sprintf("/api/export/history.csv?checkId=%d&range=24h", updated.Checks[0].ID))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(string(body), "timestamp,avg_ms") || !strings.Contains(resp.Header.Get("Content-Type"), "text/csv") {
		t.Fatalf("csv: %d %q", resp.StatusCode, body)
	}
	// static app shell + fallback
	for _, p := range []string{"/", "/nodes/5", "/app.js"} {
		resp, _ := http.Get(ts.URL + p)
		if resp.StatusCode != 200 {
			t.Fatalf("static %s: %d", p, resp.StatusCode)
		}
		resp.Body.Close()
	}
	resp, _ = http.Get(ts.URL + "/api/nope")
	if resp.StatusCode != 404 {
		t.Fatalf("unknown api: %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestSettingsDashboardsMaintenanceBackups(t *testing.T) {
	ts, srv := newTestServer(t)
	var settings model.Settings
	call(t, ts, "GET", "/api/settings", nil, &settings)
	settings.Alerts.SMTP = model.SMTPSettings{Host: "smtp.example.com", Port: 587, Username: "u", Password: "secret", From: "gw@example.com", Security: "starttls"}
	settings.Alerts.Recipients = []string{"me@example.com"}
	settings.Alerts.Enabled = true
	var saved model.Settings
	if code := call(t, ts, "PUT", "/api/settings", settings, &saved); code != 200 || saved.Alerts.SMTP.Password != passwordMask {
		t.Fatalf("put settings: %d %+v", code, saved.Alerts.SMTP)
	}
	// masked password round-trips without clobbering the stored one
	saved.General.InstanceName = "Home"
	call(t, ts, "PUT", "/api/settings", saved, &saved)
	if srv.Engine.Settings().Alerts.SMTP.Password != "secret" || srv.Engine.Settings().General.InstanceName != "Home" {
		t.Fatalf("password clobbered: %+v", srv.Engine.Settings().Alerts.SMTP)
	}
	bad := saved
	bad.Alerts.Recipients = []string{"nope"}
	if code := call(t, ts, "PUT", "/api/settings", bad, nil); code != 400 {
		t.Fatalf("expected 400 for bad recipient, got %d", code)
	}
	// The ping method is one of three words. An empty one is what every
	// settings document written before the setting existed looks like, so it
	// becomes the default instead of an error.
	badPing := saved
	badPing.General.PingMethod = "telepathy"
	if code := call(t, ts, "PUT", "/api/settings", badPing, nil); code != 400 {
		t.Fatalf("expected 400 for an unknown ping method, got %d", code)
	}
	emptyPing := saved
	emptyPing.General.PingMethod = ""
	var back model.Settings
	if code := call(t, ts, "PUT", "/api/settings", emptyPing, &back); code != 200 || back.General.PingMethod != model.PingMethodAuto {
		t.Fatalf("empty ping method: %d %q", code, back.General.PingMethod)
	}
	okPing := saved
	okPing.General.PingMethod = model.PingMethodSystem
	if code := call(t, ts, "PUT", "/api/settings", okPing, &back); code != 200 || back.General.PingMethod != model.PingMethodSystem {
		t.Fatalf("system ping method: %d %q", code, back.General.PingMethod)
	}
	if code := call(t, ts, "POST", "/api/settings/test-email", map[string]string{}, nil); code != 200 {
		t.Fatalf("test email: %d", code)
	}
	var rs model.RetentionStatus
	if code := call(t, ts, "POST", "/api/retention/run", nil, &rs); code != 200 || len(rs.Plan) == 0 {
		t.Fatalf("retention: %d %+v", code, rs)
	}

	// dashboards
	var dash model.Dashboard
	if code := call(t, ts, "POST", "/api/dashboards", map[string]any{"name": "Internet", "widgets": []map[string]any{{"type": "summary", "width": 9, "height": 0}}}, &dash); code != 200 || dash.Widgets[0].Width != 4 || dash.Widgets[0].Height != 1 {
		t.Fatalf("dashboard: %d %+v", code, dash)
	}
	dash.Name = "WAN"
	call(t, ts, "PUT", fmt.Sprintf("/api/dashboards/%d", dash.ID), dash, &dash)
	var dashes []model.Dashboard
	call(t, ts, "GET", "/api/dashboards", nil, &dashes)
	if len(dashes) != 1 || dashes[0].Name != "WAN" {
		t.Fatalf("dashboards: %+v", dashes)
	}
	if code := call(t, ts, "DELETE", fmt.Sprintf("/api/dashboards/%d", dash.ID), nil, nil); code != 200 {
		t.Fatalf("delete dashboard: %d", code)
	}

	// maintenance
	var mw maintenanceDoc
	body := map[string]any{"name": "Reboot", "group": "Servers", "enabled": true, "startAt": time.Now().Add(-time.Minute).Format(time.RFC3339), "endAt": time.Now().Add(time.Hour).Format(time.RFC3339)}
	if code := call(t, ts, "POST", "/api/maintenance", body, &mw); code != 200 || !mw.Active {
		t.Fatalf("maintenance: %d %+v", code, mw)
	}
	body["endAt"] = body["startAt"]
	if code := call(t, ts, "PUT", fmt.Sprintf("/api/maintenance/%d", mw.ID), body, nil); code != 400 {
		t.Fatalf("expected 400 for end before start, got %d", code)
	}
	weekly := map[string]any{"name": "Weekly", "enabled": true, "weekdays": []int{0, 6}, "startAt": time.Now().Format(time.RFC3339), "durationMinutes": 30}
	if code := call(t, ts, "POST", "/api/maintenance", weekly, &mw); code != 200 {
		t.Fatalf("weekly maintenance: %d", code)
	}
	var list []maintenanceDoc
	call(t, ts, "GET", "/api/maintenance", nil, &list)
	if len(list) != 2 {
		t.Fatalf("maintenance list: %d", len(list))
	}
	call(t, ts, "DELETE", fmt.Sprintf("/api/maintenance/%d", mw.ID), nil, nil)

	// backups
	call(t, ts, "POST", "/api/nodes", map[string]any{"name": "Site", "host": "example.com", "enabled": true, "checks": []map[string]any{{"type": "http", "intervalSeconds": 60}}}, nil)
	if code := call(t, ts, "POST", "/api/backups", map[string]any{"password": "", "includeHistory": true}, nil); code != 400 {
		t.Fatalf("expected password required, got %d", code)
	}
	var info model.BackupInfo
	if code := call(t, ts, "POST", "/api/backups", map[string]any{"password": "pw", "includeHistory": true}, &info); code != 201 {
		t.Fatalf("backup: %d", code)
	}
	var listing struct {
		Backups []model.BackupInfo `json:"backups"`
		Status  model.BackupStatus `json:"status"`
	}
	call(t, ts, "GET", "/api/backups", nil, &listing)
	if len(listing.Backups) != 1 || !listing.Status.LastBackupOK {
		t.Fatalf("backups: %+v", listing)
	}
	resp, _ := http.Get(ts.URL + "/api/backups/" + info.FileName + "/download")
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || int64(len(data)) != info.SizeBytes {
		t.Fatalf("download: %d %d", resp.StatusCode, len(data))
	}
	if code := call(t, ts, "GET", "/api/backups/../x/download", nil, nil); code == 200 {
		t.Fatal("path traversal must be rejected")
	}
	// wrong password on restore leaves data intact
	if code := call(t, ts, "POST", "/api/backups/restore-existing", map[string]any{"fileName": info.FileName, "password": "wrong", "includeHistory": true}, nil); code != 400 {
		t.Fatalf("expected 400 for wrong password, got %d", code)
	}
	// delete the node, restore, expect it back
	var nodes []nodeDoc
	call(t, ts, "GET", "/api/nodes", nil, &nodes)
	for _, n := range nodes {
		call(t, ts, "DELETE", fmt.Sprintf("/api/nodes/%d", n.ID), nil, nil)
	}
	call(t, ts, "GET", "/api/nodes", nil, &nodes)
	if len(nodes) != 0 {
		t.Fatalf("nodes should be gone: %d", len(nodes))
	}
	// restore via upload
	var buf bytes.Buffer
	mp := multipart.NewWriter(&buf)
	fw, _ := mp.CreateFormFile("file", info.FileName)
	fw.Write(data)
	mp.WriteField("password", "pw")
	mp.WriteField("includeHistory", "true")
	mp.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/backups/restore", &buf)
	req.Header.Set("Content-Type", mp.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	rb, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("restore upload: %d %s", resp.StatusCode, rb)
	}
	call(t, ts, "GET", "/api/nodes", nil, &nodes)
	if len(nodes) != 1 || nodes[0].Name != "Site" {
		t.Fatalf("restored nodes: %+v", nodes)
	}
	if srv.Engine.Settings().General.InstanceName != "Home" {
		t.Fatal("settings not restored")
	}
	var health model.Health
	call(t, ts, "GET", "/api/health", nil, &health)
	if health.Backup.LastRestoreAt == nil || health.Backup.LastBackupFile != info.FileName || !health.SMTPConfigured {
		t.Fatalf("health: %+v", health.Backup)
	}
	if code := call(t, ts, "DELETE", "/api/backups/"+info.FileName, nil, nil); code != 200 {
		t.Fatalf("delete backup: %d", code)
	}
	if _, err := os.Stat(filepath.Join(srv.BackupDir, info.FileName)); !os.IsNotExist(err) {
		t.Fatal("backup file should be deleted")
	}
	var logs struct {
		Lines []string `json:"lines"`
	}
	call(t, ts, "GET", "/api/logs", nil, &logs)
	if len(logs.Lines) == 0 {
		t.Fatal("expected log lines")
	}
}

func TestStreamEmitsUpdates(t *testing.T) {
	ts, _ := newTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("content type: %s", resp.Header.Get("Content-Type"))
	}
	go func() {
		resp, err := http.Post(ts.URL+"/api/nodes", "application/json", strings.NewReader(`{"name":"N","host":"h","enabled":true,"checks":[]}`))
		if err == nil {
			resp.Body.Close()
		}
	}()
	buf := make([]byte, 4096)
	var got string
	for !strings.Contains(got, "event: update") {
		n, err := resp.Body.Read(buf)
		if err != nil {
			t.Fatalf("stream read: %v (got %q)", err, got)
		}
		got += string(buf[:n])
	}
	if !strings.Contains(got, `"kind":"config"`) && !strings.Contains(got, `"kind":"event"`) {
		t.Fatalf("unexpected stream payload: %q", got)
	}
}

// The security headers are the browser's half of the bargain, so they go on
// every response — the app shell, the API and each static asset alike. A
// header only some responses carry is a header an attacker aims at the rest.
func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	ts, _ := newTestServer(t)
	head := func(path string) http.Header {
		t.Helper()
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp.Header
	}

	for _, path := range []string{"/", "/api/health", "/app.js", "/api/no-such-endpoint", "/#/nodes"} {
		h := head(path)
		csp := h.Get("Content-Security-Policy")
		if csp == "" {
			t.Errorf("%s: no Content-Security-Policy", path)
			continue
		}
		for _, want := range []string{"default-src 'self'", "script-src 'self'", "object-src 'none'", "base-uri 'none'", "form-action 'self'", "frame-ancestors 'none'"} {
			if !strings.Contains(csp, want) {
				t.Errorf("%s: policy is missing %q: %s", path, want, csp)
			}
		}
		if got := h.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options = %q", path, got)
		}
		if got := h.Get("X-Frame-Options"); got != "DENY" {
			t.Errorf("%s: X-Frame-Options = %q", path, got)
		}
		if got := h.Get("Referrer-Policy"); got != "same-origin" {
			t.Errorf("%s: Referrer-Policy = %q", path, got)
		}
	}

	// The wallboard is the one page meant to be embedded — a screen in a Home
	// Assistant dashboard is a real use, and the page is read-only and gated
	// on its own share token, so there is nothing for clickjacking to steal.
	// It says so with frame-ancestors, and stays silent on X-Frame-Options,
	// which has no way to express "anyone".
	for _, path := range []string{"/wall", "/wall/", "/wall.html", "/wall?id=2&token=abc"} {
		h := head(path)
		csp := h.Get("Content-Security-Policy")
		if !strings.Contains(csp, "frame-ancestors *") {
			t.Errorf("%s: the wallboard should be framable: %s", path, csp)
		}
		if got := h.Get("X-Frame-Options"); got != "" {
			t.Errorf("%s: X-Frame-Options must not contradict frame-ancestors, got %q", path, got)
		}
		// Everything else about the policy is the same as anywhere else.
		if !strings.Contains(csp, "script-src 'self'") || h.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: the rest of the policy should be unchanged: %s", path, csp)
		}
	}

	// A path that merely starts with the same letters is not the wallboard.
	for _, path := range []string{"/wallboards", "/api/wallboards", "/wall.html.bak"} {
		if csp := head(path).Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
			t.Errorf("%s: only the wallboard page itself may be framed: %s", path, csp)
		}
	}
}

// Nodes can be in several groups. The API takes the new list, still takes the
// old single group from a client that has not been updated, and always answers
// with both so neither kind of client has to guess.
func TestNodeGroupsOverTheAPI(t *testing.T) {
	ts, _ := newTestServer(t)

	var multi nodeDoc
	body := map[string]any{"name": "NAS", "host": "nas.local", "enabled": true,
		"groups": []string{"Servers", " Storage ", "servers"}, "checks": []map[string]any{}}
	if code := call(t, ts, "POST", "/api/nodes", body, &multi); code != 201 {
		t.Fatalf("create with groups: %d", code)
	}
	if len(multi.Groups) != 2 || multi.Groups[0] != "Servers" || multi.Groups[1] != "Storage" {
		t.Fatalf("groups not normalised: %+v", multi.Groups)
	}
	if multi.Group != "Servers" {
		t.Fatalf("group alias = %q, want the first group", multi.Group)
	}

	var legacy nodeDoc
	old := map[string]any{"name": "Printer", "host": "printer", "enabled": true, "group": "Office", "checks": []map[string]any{}}
	if code := call(t, ts, "POST", "/api/nodes", old, &legacy); code != 201 {
		t.Fatalf("create with the legacy group: %d", code)
	}
	if len(legacy.Groups) != 1 || legacy.Groups[0] != "Office" || legacy.Group != "Office" {
		t.Fatalf("a client sending only group should get a one-group node: %+v", legacy)
	}

	// The list carries both fields, and groups is a list even when empty.
	var raw []map[string]any
	if code := call(t, ts, "GET", "/api/nodes", nil, &raw); code != 200 || len(raw) != 2 {
		t.Fatalf("list nodes: %d %d", code, len(raw))
	}
	for _, n := range raw {
		if _, ok := n["groups"].([]any); !ok {
			t.Fatalf("groups should always be a list: %v", n["groups"])
		}
		if _, ok := n["group"].(string); !ok {
			t.Fatalf("the deprecated group alias should still be sent: %v", n["group"])
		}
	}

	// /api/groups counts a node once per group it is in.
	var groups struct {
		Groups []struct {
			Name  string `json:"name"`
			Count int    `json:"count"`
		} `json:"groups"`
	}
	if code := call(t, ts, "GET", "/api/groups", nil, &groups); code != 200 || len(groups.Groups) != 3 {
		t.Fatalf("groups: %d %+v", code, groups.Groups)
	}

	// And the overview tallies it in each of them: three group rows for two
	// nodes, which is why the group totals can exceed the node count.
	var ov struct {
		Summary struct {
			Total int `json:"total"`
		} `json:"summary"`
		Groups []struct {
			Name  string `json:"name"`
			Total int    `json:"total"`
		} `json:"groups"`
	}
	if code := call(t, ts, "GET", "/api/overview", nil, &ov); code != 200 {
		t.Fatalf("overview: %d", code)
	}
	tally := map[string]int{}
	for _, g := range ov.Groups {
		tally[g.Name] = g.Total
	}
	if tally["Servers"] != 1 || tally["Storage"] != 1 || tally["Office"] != 1 {
		t.Fatalf("per-group tally = %v", tally)
	}
	if ov.Summary.Total != 2 {
		t.Fatalf("summary total = %d, want the number of nodes", ov.Summary.Total)
	}

	// An update replaces the whole list.
	multi.Groups = []string{"Storage"}
	var updated nodeDoc
	if code := call(t, ts, "PUT", fmt.Sprintf("/api/nodes/%d", multi.ID), multi.Node, &updated); code != 200 {
		t.Fatalf("update: %d", code)
	}
	if len(updated.Groups) != 1 || updated.Groups[0] != "Storage" || updated.Group != "Storage" {
		t.Fatalf("update should replace the group list: %+v", updated)
	}
}

// A check's credentials are not monitoring data: reading a node hands back a
// mask, and saving the node back with the field blank keeps what is stored.
func TestCheckSecretsAreMaskedAndKept(t *testing.T) {
	ts, srv := newTestServer(t)
	ctx := context.Background()

	node := model.Node{Name: "Gateway", Host: "192.168.1.1", Enabled: true, Importance: model.ImportanceNormal, Checks: []model.Check{{
		Type: model.CheckSNMP, Name: "SNMP", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5,
		Config: model.CheckConfig{
			SNMPVersion: "2c", SNMPPort: 161, SNMPCommunity: "n0t-public",
			SNMPOIDs: []model.SNMPOID{{OID: "1.3.6.1.2.1.1.3.0", Name: "Uptime", Kind: "gauge", Scale: 0.01, Unit: "s"}},
		},
	}}}
	var created nodeDoc
	if code := call(t, ts, "POST", "/api/nodes", node, &created); code != 201 {
		t.Fatalf("create = %d", code)
	}
	if got := created.Checks[0].Config.SNMPCommunity; got != passwordMask {
		t.Fatalf("community echoed as %q, want the mask", got)
	}

	var fetched nodeDoc
	if code := call(t, ts, "GET", fmt.Sprintf("/api/nodes/%d", created.ID), nil, &fetched); code != 200 {
		t.Fatalf("get = %d", code)
	}
	if got := fetched.Checks[0].Config.SNMPCommunity; got != passwordMask {
		t.Fatalf("reading a node handed out %q", got)
	}

	// Saving it back the way the editor does — with the credential left
	// blank — must not wipe the stored one.
	back := fetched.Node
	back.Checks[0].Config.SNMPCommunity = ""
	var saved nodeDoc
	if code := call(t, ts, "PUT", fmt.Sprintf("/api/nodes/%d", created.ID), back, &saved); code != 200 {
		t.Fatalf("update = %d", code)
	}
	stored, err := srv.Store.GetCheck(ctx, created.Checks[0].ID)
	if err != nil {
		t.Fatalf("get check: %v", err)
	}
	if stored.Config.SNMPCommunity != "n0t-public" {
		t.Fatalf("stored community = %q, want it kept", stored.Config.SNMPCommunity)
	}

	// The overview is readable by viewers and read-only API keys, so it must
	// not hand the credential out either.
	var ov engine.Overview
	if code := call(t, ts, "GET", "/api/overview", nil, &ov); code != 200 {
		t.Fatalf("overview = %d", code)
	}
	for _, nv := range ov.Nodes {
		for _, c := range nv.Node.Checks {
			if c.Config.SNMPCommunity == "n0t-public" {
				t.Fatal("the overview handed out a community string")
			}
		}
		for _, cv := range nv.Checks {
			if cv.Check.Config.SNMPCommunity == "n0t-public" {
				t.Fatal("the overview's check views handed out a community string")
			}
		}
	}
}

// An SNMP check's per-OID readings are charted like any other metric.
func TestHistoryServesANamedMetric(t *testing.T) {
	ts, srv := newTestServer(t)
	ctx := context.Background()

	node := model.Node{Name: "Gateway", Host: "192.168.1.1", Enabled: true, Importance: model.ImportanceNormal, Checks: []model.Check{{
		Type: model.CheckSNMP, Name: "SNMP", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5,
		Config: model.CheckConfig{
			SNMPVersion: "2c", SNMPPort: 161, SNMPCommunity: "public",
			SNMPOIDs: []model.SNMPOID{{OID: "1.3.6.1.2.1.2.2.1.10.1", Name: "WAN in", Kind: "counter", Scale: 8, Unit: "bit/s"}},
		},
	}}}
	var created nodeDoc
	if code := call(t, ts, "POST", "/api/nodes", node, &created); code != 201 {
		t.Fatalf("create = %d", code)
	}
	checkID := created.Checks[0].ID
	if _, err := srv.Store.InsertResult(ctx, model.Result{
		CheckID: checkID, Timestamp: time.Now().Add(-time.Minute), Success: true, Status: model.StatusUp,
		Metrics: map[string]float64{"WAN in": 1600},
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var series model.HistorySeries
	if code := call(t, ts, "GET", fmt.Sprintf("/api/history?checkId=%d&range=24h&metric=WAN+in", checkID), nil, &series); code != 200 {
		t.Fatalf("history = %d", code)
	}
	if series.Metric != "WAN in" || series.MetricUnit != "bit/s" {
		t.Fatalf("series = %+v, want it to describe the metric", series)
	}
	if len(series.Points) != 1 || series.Points[0].Value == nil || *series.Points[0].Value != 1600 {
		t.Fatalf("points = %+v, want the stored reading", series.Points)
	}

	// A name the check does not measure is refused rather than served empty,
	// so /api/history cannot be used to fish for names in stored results.
	if code := call(t, ts, "GET", fmt.Sprintf("/api/history?checkId=%d&metric=whatever", checkID), nil, nil); code != 400 {
		t.Fatalf("unknown metric = %d, want 400", code)
	}
}

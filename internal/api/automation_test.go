package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/update"
)

func TestTriggersEndpointsAndHooks(t *testing.T) {
	ts, srv := newTestServer(t)
	var hits int32
	var lastBody atomic.Value
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		b, _ := io.ReadAll(r.Body)
		lastBody.Store(string(b))
		io.WriteString(w, "ok")
	}))
	defer hook.Close()

	var node nodeDoc
	if code := call(t, ts, "POST", "/api/nodes", map[string]any{"name": "Router", "host": "192.168.1.1", "enabled": true, "checks": []map[string]any{{"type": "ping", "name": "Ping", "enabled": true, "intervalSeconds": 60}}}, &node); code != 201 {
		t.Fatalf("create node: %d", code)
	}
	// validation
	if code := call(t, ts, "POST", "/api/triggers", map[string]any{"nodeId": node.ID, "name": "x", "on": []string{"down"}, "action": map[string]any{"type": "http"}}, nil); code != 400 {
		t.Fatalf("expected 400 for missing url, got %d", code)
	}
	if code := call(t, ts, "POST", "/api/triggers", map[string]any{"nodeId": node.ID, "name": "x", "on": []string{"whatever"}, "action": map[string]any{"type": "http", "url": hook.URL}}, nil); code != 400 {
		t.Fatalf("expected 400 for bad condition, got %d", code)
	}
	var trig model.Trigger
	body := map[string]any{"nodeId": node.ID, "name": "Notify", "enabled": true, "on": []string{"any_success", "down"}, "action": map[string]any{"type": "http", "url": hook.URL + "/{{node.name}}", "body": `{"node":"{{node.name}}","status":"{{status}}","event":"{{event}}"}`}}
	if code := call(t, ts, "POST", "/api/triggers", body, &trig); code != 200 || trig.ID == 0 || len(trig.On) != 2 {
		t.Fatalf("create trigger: %d %+v", code, trig)
	}
	var list []model.Trigger
	call(t, ts, "GET", fmt.Sprintf("/api/triggers?nodeId=%d", node.ID), nil, &list)
	if len(list) != 1 {
		t.Fatalf("list triggers: %d", len(list))
	}
	// running the node's checks fires the any_success condition
	call(t, ts, "POST", fmt.Sprintf("/api/nodes/%d/run", node.ID), nil, nil)
	deadline := time.Now().Add(5 * time.Second)
	for atomic.LoadInt32(&hits) == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if atomic.LoadInt32(&hits) == 0 {
		t.Fatal("trigger did not fire")
	}
	if b, _ := lastBody.Load().(string); !strings.Contains(b, `"node":"Router"`) || !strings.Contains(b, `"event":"any_success"`) {
		t.Fatalf("webhook body: %s", b)
	}
	// the run is recorded on the trigger and in the timeline
	deadline = time.Now().Add(5 * time.Second)
	for {
		call(t, ts, "GET", fmt.Sprintf("/api/triggers?nodeId=%d", node.ID), nil, &list)
		if len(list) == 1 && list[0].RunCount >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("trigger run not recorded: %+v", list)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if list[0].LastStatus != "ok" {
		t.Fatalf("trigger status: %+v", list[0])
	}
	var events []model.Event
	call(t, ts, "GET", "/api/events?type=trigger_fired", nil, &events)
	if len(events) == 0 || !strings.Contains(events[0].Title, "Notify") {
		t.Fatalf("trigger events: %+v", events)
	}
	// manual run + test action
	var res model.ActionResult
	if code := call(t, ts, "POST", fmt.Sprintf("/api/triggers/%d/run", trig.ID), nil, &res); code != 200 || !res.OK {
		t.Fatalf("run trigger: %d %+v", code, res)
	}
	if code := call(t, ts, "POST", "/api/actions/test", map[string]any{"action": map[string]any{"type": "http", "url": hook.URL}, "nodeId": node.ID}, &res); code != 200 || !res.OK {
		t.Fatalf("test action: %d %+v", code, res)
	}
	// update + delete
	trig.Enabled = false
	var updated model.Trigger
	if code := call(t, ts, "PUT", fmt.Sprintf("/api/triggers/%d", trig.ID), trig, &updated); code != 200 || updated.Enabled {
		t.Fatalf("update trigger: %d %+v", code, updated)
	}
	if code := call(t, ts, "DELETE", fmt.Sprintf("/api/triggers/%d", trig.ID), nil, nil); code != 200 {
		t.Fatalf("delete trigger: %d", code)
	}

	// endpoints
	var ep model.Endpoint
	epBody := map[string]any{"name": "Router rebooted", "slug": "Router Rebooted!", "enabled": true, "method": "POST", "token": "s3cret-token", "action": map[string]any{"type": "run_node", "nodeId": node.ID}}
	if code := call(t, ts, "POST", "/api/endpoints", epBody, &ep); code != 200 || ep.Slug != "router-rebooted" {
		t.Fatalf("create endpoint: %d %+v", code, ep)
	}
	if code := call(t, ts, "POST", "/api/endpoints", epBody, nil); code != 400 {
		t.Fatalf("duplicate slug should be rejected, got %d", code)
	}
	if code := call(t, ts, "GET", "/hook/router-rebooted?token=s3cret-token", nil, nil); code != 405 {
		t.Fatalf("expected 405 for wrong method, got %d", code)
	}
	if code := call(t, ts, "POST", "/hook/router-rebooted", map[string]any{}, nil); code != 401 {
		t.Fatalf("expected 401 without token, got %d", code)
	}
	req, _ := http.NewRequest("POST", ts.URL+"/hook/router-rebooted", strings.NewReader("hello"))
	req.Header.Set("X-GWatch-Token", "s3cret-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(data), `"ok":true`) {
		t.Fatalf("hook: %d %s", resp.StatusCode, data)
	}
	if code := call(t, ts, "GET", "/hook/nope", nil, nil); code != 404 {
		t.Fatalf("unknown hook: %d", code)
	}
	var eps []model.Endpoint
	call(t, ts, "GET", "/api/endpoints", nil, &eps)
	if len(eps) != 1 || eps[0].CallCount != 1 || eps[0].LastStatus != "ok" {
		t.Fatalf("endpoints: %+v", eps)
	}
	if code := call(t, ts, "POST", fmt.Sprintf("/api/endpoints/%d/run", ep.ID), nil, &res); code != 200 || !res.OK {
		t.Fatalf("run endpoint: %d %+v", code, res)
	}
	if code := call(t, ts, "DELETE", fmt.Sprintf("/api/endpoints/%d", ep.ID), nil, nil); code != 200 {
		t.Fatalf("delete endpoint: %d", code)
	}
	// config export carries automation
	var meta map[string]any
	call(t, ts, "GET", "/api/automation/meta", nil, &meta)
	if meta["conditions"] == nil || meta["interpreters"] == nil {
		t.Fatalf("meta: %+v", meta)
	}
	_ = srv
}

// callBody is call() that also returns the response body, so error messages can
// be asserted on (call only decodes successful responses).
func callBody(t *testing.T, ts *httptest.Server, method, path string, body any) (int, string) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = strings.NewReader(string(b))
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
	return resp.StatusCode, string(data)
}

func TestEndpointTokenRequired(t *testing.T) {
	ts, srv := newTestServer(t)
	action := map[string]any{"type": "http", "url": "http://127.0.0.1:1/never"}

	// no token and no acknowledgement: refused
	code, msg := callBody(t, ts, "POST", "/api/endpoints", map[string]any{"name": "Open", "slug": "open", "enabled": true, "action": action})
	if code != 400 || !strings.Contains(msg, "token is required") {
		t.Fatalf("expected 400 without a token, got %d %s", code, msg)
	}
	// a short token is refused too
	if code := call(t, ts, "POST", "/api/endpoints", map[string]any{"name": "Short", "slug": "short", "enabled": true, "token": "abc", "action": action}, nil); code != 400 {
		t.Fatalf("expected 400 for a short token, got %d", code)
	}
	// acknowledged: saved, and callable without a token
	var ep model.Endpoint
	if code := call(t, ts, "POST", "/api/endpoints", map[string]any{"name": "Open", "slug": "open", "enabled": true, "allowNoToken": true, "action": map[string]any{"type": "run_node", "nodeId": 0}}, nil); code != 400 {
		t.Fatalf("expected the action to still be validated, got %d", code)
	}
	if code := call(t, ts, "POST", "/api/endpoints", map[string]any{"name": "Open", "slug": "open", "enabled": true, "allowNoToken": true, "action": map[string]any{"type": "http", "url": "http://127.0.0.1:1/never"}}, &ep); code != 200 || !ep.AllowNoToken || ep.Token != "" {
		t.Fatalf("create acknowledged endpoint: %d %+v", code, ep)
	}
	if code := call(t, ts, "POST", "/hook/open", map[string]any{}, nil); code != 502 {
		// 502 = the action itself failed (nothing listens on port 1), which means
		// the request got past the token check.
		t.Fatalf("expected the hook to run without a token, got %d", code)
	}
	// supplying a token clears the acknowledgement
	var saved model.Endpoint
	if code := call(t, ts, "PUT", fmt.Sprintf("/api/endpoints/%d", ep.ID), map[string]any{"name": "Open", "slug": "open", "enabled": true, "allowNoToken": true, "token": "long-enough-token", "action": map[string]any{"type": "http", "url": "http://127.0.0.1:1/never"}}, &saved); code != 200 || saved.AllowNoToken || saved.Token != "long-enough-token" {
		t.Fatalf("token should win over allowNoToken: %d %+v", code, saved)
	}
	if code := call(t, ts, "POST", "/hook/open", map[string]any{}, nil); code != 401 {
		t.Fatalf("expected 401 once a token is set, got %d", code)
	}

	// an endpoint that lost its token (written straight to the store, as an old
	// install would have it) is refused and reported by the meta endpoint
	if _, err := srv.Store.SaveEndpoint(context.Background(), model.Endpoint{Name: "Legacy", Slug: "legacy", Enabled: true, Method: "ANY",
		Action: model.Action{Type: model.ActionHTTP, URL: "http://127.0.0.1:1/never"}}); err != nil {
		t.Fatal(err)
	}
	if code, msg := callBody(t, ts, "POST", "/hook/legacy", map[string]any{}); code != 401 || !strings.Contains(msg, "no token configured") {
		t.Fatalf("expected 401 for the tokenless endpoint, got %d %s", code, msg)
	}
	var meta struct {
		Tokenless []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
			Slug string `json:"slug"`
		} `json:"tokenlessEndpoints"`
	}
	call(t, ts, "GET", "/api/automation/meta", nil, &meta)
	if len(meta.Tokenless) != 1 || meta.Tokenless[0].Slug != "legacy" {
		t.Fatalf("tokenlessEndpoints: %+v", meta.Tokenless)
	}
}

func TestChartsStatusEventsAndAccessPassword(t *testing.T) {
	ts, srv := newTestServer(t)
	var charts []model.SavedChart
	if code := call(t, ts, "PUT", "/api/charts", []map[string]any{{"name": "Latency", "config": map[string]any{"metric": "avg", "range": "24h"}}, {"name": ""}}, &charts); code != 200 || len(charts) != 2 || charts[0].ID == "" || charts[1].Name != "Chart 2" {
		t.Fatalf("put charts: %d %+v", code, charts)
	}
	call(t, ts, "GET", "/api/charts", nil, &charts)
	if len(charts) != 2 {
		t.Fatalf("get charts: %+v", charts)
	}

	var status statusDoc
	if code := call(t, ts, "GET", "/api/status", nil, &status); code != 200 || !status.ServiceOK {
		t.Fatalf("status: %d %+v", code, status)
	}
	var net model.NetworkInfo
	if code := call(t, ts, "GET", "/api/network", nil, &net); code != 200 {
		t.Fatalf("network: %d", code)
	}

	// events: text search, until, exact type
	call(t, ts, "POST", "/api/events/note", map[string]any{"text": "Rebooted the NAS"}, nil)
	call(t, ts, "POST", "/api/events/note", map[string]any{"text": "Replaced a cable"}, nil)
	var events []model.Event
	call(t, ts, "GET", "/api/events?q=nas", nil, &events)
	if len(events) != 1 || !strings.Contains(events[0].Detail, "NAS") {
		t.Fatalf("search: %+v", events)
	}
	call(t, ts, "GET", "/api/events?until=2000-01-01", nil, &events)
	if len(events) != 0 {
		t.Fatalf("until filter: %d", len(events))
	}
	call(t, ts, "GET", "/api/events?type=note&since=2000-01-01T00:00", nil, &events)
	if len(events) != 2 {
		t.Fatalf("since filter: %d", len(events))
	}
	resp, _ := http.Get(ts.URL + "/api/export/events.csv?q=cable")
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), "Replaced a cable") || strings.Contains(string(b), "NAS") {
		t.Fatalf("events csv: %s", b)
	}
	resp, _ = http.Get(ts.URL + "/api/export/logs.txt")
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), "text/plain") || len(b) == 0 {
		t.Fatalf("logs export: %d %q", resp.StatusCode, b)
	}

	// appearance + remote access settings
	var settings model.Settings
	call(t, ts, "GET", "/api/settings", nil, &settings)
	settings.General.Theme = "light"
	settings.General.AccentColor = "#00AAFF"
	settings.General.RemoteAccess = true
	settings.General.AccessPassword = "letmein"
	var saved model.Settings
	if code := call(t, ts, "PUT", "/api/settings", settings, &saved); code != 200 || saved.General.Theme != "light" || saved.General.AccentColor != "#00aaff" || saved.General.AccessPassword != passwordMask {
		t.Fatalf("appearance settings: %d %+v", code, saved.General)
	}
	if srv.Engine.Settings().General.AccessPassword != "letmein" {
		t.Fatal("password not stored")
	}
	saved.General.AccentColor = "blue"
	if code := call(t, ts, "PUT", "/api/settings", saved, nil); code != 400 {
		t.Fatalf("expected 400 for bad colour, got %d", code)
	}
	// the masked password round-trips
	saved.General.AccentColor = "#00aaff"
	call(t, ts, "PUT", "/api/settings", saved, &saved)
	if srv.Engine.Settings().General.AccessPassword != "letmein" {
		t.Fatal("password clobbered by mask")
	}

	// loopback clients are never challenged (httptest connects via 127.0.0.1)
	if code := call(t, ts, "GET", "/api/health", nil, nil); code != 200 {
		t.Fatalf("loopback should pass: %d", code)
	}
	// a non-loopback client must authenticate for anything but the public
	// liveness and sign-in routes
	h := srv.Handler()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/nodes", nil)
	req.RemoteAddr = "192.168.1.20:5555"
	h.ServeHTTP(rr, req)
	if rr.Code != 401 || rr.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("expected 401 for LAN client, got %d", rr.Code)
	}
	rr = httptest.NewRecorder()
	req.SetBasicAuth("anyone", "letmein")
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("expected 200 with password, got %d", rr.Code)
	}
	// /api/health stays reachable without credentials, but only as liveness.
	rr = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/health", nil)
	req.RemoteAddr = "192.168.1.20:5555"
	h.ServeHTTP(rr, req)
	if rr.Code != 200 || strings.Contains(rr.Body.String(), "databasePath") {
		t.Fatalf("public health should be minimal: %d %s", rr.Code, rr.Body.String())
	}
	rr = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/hook/none", nil)
	req.RemoteAddr = "192.168.1.20:5555"
	h.ServeHTTP(rr, req)
	if rr.Code != 404 {
		t.Fatalf("hooks bypass basic auth (token protected instead), got %d", rr.Code)
	}
	rr = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.9:1"
	h.ServeHTTP(rr, req)
	if rr.Code != 401 {
		t.Fatalf("UI should be protected too, got %d", rr.Code)
	}
}

func TestUpdateEndpoints(t *testing.T) {
	ts, srv := newTestServer(t)
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases") {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `[{"tag_name":"v9.9.9","html_url":"https://example.com","assets":[]},
				{"tag_name":"v10.0.0-beta1","prerelease":true,"html_url":"https://example.com/beta","assets":[]}]`)
			return
		}
		w.WriteHeader(404)
	}))
	defer gh.Close()
	srv.Updater.Client.APIBase = gh.URL
	srv.Updater.Client.HTTP = gh.Client()
	var info model.UpdateInfo
	if code := call(t, ts, "POST", "/api/update/check", nil, &info); code != 200 || info.LatestVersion != "9.9.9" || !info.UpdateAvailable {
		t.Fatalf("check: %d %+v", code, info)
	}
	var st struct {
		Status model.UpdateStatus `json:"status"`
		Repo   string             `json:"repo"`
	}
	call(t, ts, "GET", "/api/update/status", nil, &st)
	if st.Status.Last == nil || st.Repo != "jxburros/GWatch" {
		t.Fatalf("status: %+v", st)
	}
	// no asset for this platform → apply fails cleanly
	resp, _ := http.Post(ts.URL+"/api/update/apply", "application/json", strings.NewReader("{}"))
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var out map[string]any
	_ = json.Unmarshal(data, &out)
	if resp.StatusCode != 502 || !strings.Contains(fmt.Sprint(out["error"]), "no executable") {
		t.Fatalf("apply: %d %s", resp.StatusCode, data)
	}
	var events []model.Event
	call(t, ts, "GET", "/api/events?type=update", nil, &events)
	if len(events) < 2 {
		t.Fatalf("update events: %+v", events)
	}

	// The catalogue lists both releases, the pre-release flagged as such, and
	// the stable check does not land on it.
	var cat struct {
		Releases []model.Release `json:"releases"`
	}
	if code := call(t, ts, "GET", "/api/update/releases", nil, &cat); code != 200 || len(cat.Releases) != 2 {
		t.Fatalf("releases: %d %+v", code, cat.Releases)
	}
	if cat.Releases[0].Version != "10.0.0-beta1" || !cat.Releases[0].Prerelease || cat.Releases[1].Version != "9.9.9" {
		t.Fatalf("catalogue: %+v", cat.Releases)
	}
	if info.Prerelease || info.LatestVersion != "9.9.9" {
		t.Fatalf("a stable check should not offer the pre-release: %+v", info)
	}

	// Naming a version that was never published is refused, and named
	// against the release list rather than turned into a download URL.
	resp, _ = http.Post(ts.URL+"/api/update/apply", "application/json", strings.NewReader(`{"version":"4.2.0"}`))
	data, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 502 || !strings.Contains(string(data), "no release 4.2.0") {
		t.Fatalf("unknown version: %d %s", resp.StatusCode, data)
	}
	_ = context.Background()
}

// TestUpdaterBackgroundChecks covers the parts of the updater that no HTTP
// request drives: whether a periodic check is due, and what the status says
// about when the next one is.
func TestUpdaterBackgroundChecks(t *testing.T) {
	var hits int
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases") {
			hits++
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `[{"tag_name":"v9.9.9","html_url":"https://example.com","assets":[]}]`)
			return
		}
		w.WriteHeader(404)
	}))
	defer gh.Close()

	prefs := model.UpdateSettings{CheckAutomatically: false, CheckIntervalHours: 24}
	u := &Updater{
		Client:  &update.Client{APIBase: gh.URL, HTTP: gh.Client()},
		Version: "1.0.0",
		Prefs:   func() model.UpdateSettings { return prefs },
		Repo:    func() string { return "acme/gwatch" },
	}

	// Automatic checks off: nothing is contacted, and the status says so.
	u.checkIfDue(context.Background())
	if hits != 0 {
		t.Fatalf("a check ran with automatic checks off (%d requests)", hits)
	}
	if st := u.Status(); st.AutoCheck || st.NextCheckAt != nil || st.LastCheckAt != nil {
		t.Fatalf("status with checks off: %+v", st)
	}

	// Switched on: the first tick checks, and the next one is scheduled an
	// interval later.
	prefs.CheckAutomatically = true
	u.checkIfDue(context.Background())
	if hits != 1 {
		t.Fatalf("expected one check, got %d", hits)
	}
	st := u.Status()
	if !st.AutoCheck || st.LastCheckAt == nil || st.NextCheckAt == nil {
		t.Fatalf("status after check: %+v", st)
	}
	if got := st.NextCheckAt.Sub(*st.LastCheckAt); got != 24*time.Hour {
		t.Fatalf("next check should be an interval later, got %s", got)
	}
	if st.Last == nil || st.Last.LatestVersion != "9.9.9" || !st.Last.UpdateAvailable {
		t.Fatalf("last check: %+v", st.Last)
	}

	// Not due yet: the interval has not elapsed, so GitHub is left alone.
	u.checkIfDue(context.Background())
	if hits != 1 {
		t.Fatalf("a check ran before it was due (%d requests)", hits)
	}

	// Due again once the interval has passed.
	u.mu.Lock()
	u.lastCheck = time.Now().Add(-25 * time.Hour)
	u.mu.Unlock()
	u.checkIfDue(context.Background())
	if hits != 2 {
		t.Fatalf("expected a second check once due, got %d", hits)
	}

	// PromptOnOpen only reaches the interface when checking is on at all:
	// GWatch cannot offer an update it never looks for.
	prefs.PromptOnOpen = true
	if st := u.Status(); !st.PromptOnOpen {
		t.Fatal("prompt should be on when checks are on")
	}
	prefs.CheckAutomatically = false
	if st := u.Status(); st.PromptOnOpen {
		t.Fatal("prompt should be off when automatic checks are off")
	}
}

// A /hook/ token is the only thing standing between the outside world and
// whatever the endpoint does, so guessing at one has to cost the guesser the
// same per-IP failure budget as guessing at a password.
func TestHookTokenAttemptsAreRateLimited(t *testing.T) {
	ts, srv := newTestServer(t)
	action := map[string]any{"type": "http", "url": "http://127.0.0.1:1/never"}
	var ep model.Endpoint
	if code := call(t, ts, "POST", "/api/endpoints", map[string]any{
		"name": "Reboot", "slug": "reboot", "enabled": true, "method": "ANY",
		"token": "the-real-token", "action": action,
	}, &ep); code != 200 {
		t.Fatalf("create endpoint: %d", code)
	}

	// hook calls one /hook/ URL and reports the status and any Retry-After.
	hook := func(path, token string) (int, string) {
		t.Helper()
		req, err := http.NewRequest("POST", ts.URL+path, strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("X-GWatch-Token", token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		return resp.StatusCode, resp.Header.Get("Retry-After")
	}

	// 1. Wrong tokens run out of attempts, and the 429 says when to come back.
	limited := false
	for i := 0; i < failureLimit+2; i++ {
		status, retry := hook("/hook/reboot", "not-the-token")
		if status == http.StatusTooManyRequests {
			if retry == "" {
				t.Error("a 429 must carry Retry-After so a caller knows how long to wait")
			}
			limited = true
			break
		}
		if status != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401, got %d", i, status)
		}
	}
	if !limited {
		t.Fatalf("guessing a hook token should run out within %d tries", failureLimit+2)
	}

	// 2. A slug nobody recognises answers 404 — a mistyped URL has to stay
	//    diagnosable — but it is charged for all the same, so the slug space
	//    cannot be swept for free. The address is already out of budget here,
	//    which is how we can tell the unknown slug went through the limiter.
	if status, _ := hook("/hook/no-such-endpoint", ""); status != http.StatusTooManyRequests {
		t.Errorf("an unknown slug must consume the budget too, got %d", status)
	}

	// 3. The right token gets in and hands the budget straight back, so an
	//    endpoint called on a schedule is never throttled by its own traffic.
	srv.failLimiter.Reset("127.0.0.1")
	for i := 0; i < failureLimit*3; i++ {
		// 502 = the action ran and nothing was listening on port 1, which
		// means the token was accepted.
		if status, _ := hook("/hook/reboot", "the-real-token"); status != http.StatusBadGateway {
			t.Fatalf("call %d with the right token: %d", i, status)
		}
	}
	// With the budget intact, an unknown slug is a plain 404 again.
	if status, _ := hook("/hook/no-such-endpoint", ""); status != http.StatusNotFound {
		t.Fatalf("an unknown slug on a full budget should be a 404, got %d", status)
	}
}

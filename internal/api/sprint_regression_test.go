package api

import (
	"context"
	"fmt"
	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/update"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpaqueOriginCannotPerformBodilessActions(t *testing.T) {
	ts, _ := newTestServer(t)
	for _, origin := range []string{"null", "data:text/html,x", ":bad", "http://"} {
		code, _, _ := as(t, ts, creds{Origin: origin}, "POST", "/api/retention/run", nil, nil)
		if code != 403 {
			t.Fatalf("origin %q: %d", origin, code)
		}
	}
	r := httptest.NewRequest("POST", "http://localhost/api/retention/run", nil)
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	if !crossSite(r) {
		t.Fatal("fetch metadata ignored")
	}
}

func TestSecretMasksRoundTripAndShareableExport(t *testing.T) {
	ts, srv := newTestServer(t)
	ctx := context.Background()
	n, err := srv.Store.CreateNode(ctx, model.Node{Name: "private", Enabled: true, Checks: []model.Check{{Name: "probe", Type: model.CheckHTTP, Enabled: true, Config: model.CheckConfig{Target: "https://example.com", Headers: map[string]string{"Authorization": "HEADERSECRET"}, Body: "BODYSECRET", Env: map[string]string{"KEY": "ENVSECRET"}, SNMPCommunity: "COMMUNITYSECRET"}}}})
	if err != nil {
		t.Fatal(err)
	}
	settings := srv.Engine.Settings()
	settings.General.AccessPassword = "ACCESSSECRET"
	if err := srv.Store.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if _, err = srv.Store.SaveTrigger(ctx, model.Trigger{NodeID: n.ID, Name: "notify", Action: model.Action{Type: model.ActionSlack, WebhookURL: "https://example.com/WEBHOOKSECRET", Token: "ACTIONSECRET"}}); err != nil {
		t.Fatal(err)
	}
	var exported map[string]any
	code, body, _ := as(t, ts, creds{}, "GET", "/api/export/config.json", nil, &exported)
	if code != 200 {
		t.Fatalf("export %d %s", code, body)
	}
	for _, secret := range []string{"HEADERSECRET", "BODYSECRET", "ENVSECRET", "COMMUNITYSECRET", "ACCESSSECRET", "WEBHOOKSECRET", "ACTIONSECRET"} {
		if strings.Contains(body, secret) {
			t.Fatalf("export leaks %s", secret)
		}
	}
	masked := maskNodeChecks(n)
	if masked.Checks[0].Config.Headers["Authorization"] != passwordMask || n.Checks[0].Config.Headers["Authorization"] != "HEADERSECRET" {
		t.Fatal("mask mutated original or missed map")
	}
	restoreCheckSecrets(&masked, n)
	if masked.Checks[0].Config.Body != "BODYSECRET" || masked.Checks[0].Config.Headers["Authorization"] != "HEADERSECRET" || masked.Checks[0].Config.Env["KEY"] != "ENVSECRET" {
		t.Fatal("masked edit lost credentials")
	}
}

func TestBulkExplicitSelectionExcludesUntickedChecks(t *testing.T) {
	ts, _ := newTestServer(t)
	a, _ := makeBulkNodes(t, ts)
	var result bulkResponse
	code := call(t, ts, "PATCH", "/api/nodes/bulk", map[string]any{"nodeIds": []int64{a.ID}, "checkIds": []int64{a.Checks[0].ID}, "node": map[string]any{"importance": "high"}, "check": map[string]any{"intervalSeconds": 123}}, &result)
	if code != 200 || result.Checks != 1 || intervalOf(t, ts, a.Checks[1].ID) != 60 {
		t.Fatalf("unticked changed: %d %+v", code, result)
	}
	code = call(t, ts, "PATCH", "/api/nodes/bulk", map[string]any{"nodeIds": []int64{a.ID}, "checkIds": []int64{}, "check": map[string]any{"intervalSeconds": 222}}, nil)
	if code != 400 || intervalOf(t, ts, a.Checks[0].ID) != 123 {
		t.Fatal("empty explicit selection broadened")
	}
}

func TestAgentUpdateOptOutMakesNoNetworkContact(t *testing.T) {
	calls := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.Write([]byte("[]")) }))
	defer remote.Close()
	u := Updater{Client: &update.Client{APIBase: remote.URL, HTTP: remote.Client()}, Prefs: func() model.UpdateSettings { return model.UpdateSettings{CheckAutomatically: false} }}
	if got := u.AgentLatest(context.Background()); got.Version != "" || !got.CheckedAt.IsZero() || calls != 0 {
		t.Fatalf("contacted GitHub while disabled: %+v", got)
	}
}

func TestErrorsAndStaticFilesDoNotLeak(t *testing.T) {
	ts, srv := newTestServer(t)
	w := httptest.NewRecorder()
	srv.fail(w, fmt.Errorf("private SQL password and path"))
	if strings.Contains(w.Body.String(), "private SQL") || w.Header().Get("X-Request-ID") == "" {
		t.Fatal("error leaked or no correlation ID")
	}
	res, err := http.Get(ts.URL + "/app.js")
	if err != nil {
		t.Fatal(err)
	}
	tag := res.Header.Get("ETag")
	res.Body.Close()
	if tag == "" {
		t.Fatal("no validator")
	}
	req, _ := http.NewRequest("GET", ts.URL+"/app.js", nil)
	req.Header.Set("If-None-Match", tag)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 304 {
		t.Fatalf("validator status %d", res.StatusCode)
	}
}

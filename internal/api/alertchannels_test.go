package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/secrets"
)

func TestAlertChannelSettingsAreSealedMaskedAndRestored(t *testing.T) {
	ts, srv := newTestServer(t)
	ctx := context.Background()
	var settings model.Settings
	call(t, ts, "GET", "/api/settings", nil, &settings)
	if settings.Alerts.Channels == nil {
		t.Fatal("channels should come back as an empty list, not null")
	}
	// Alerts can be enabled with a channel and no e-mail at all.
	settings.Alerts.Enabled = true
	settings.Alerts.Recipients = nil
	settings.Alerts.Channels = []model.AlertChannel{
		{Name: " Pager ", Enabled: true, MinSeverity: "down", Groups: []string{" Servers ", "servers", ""}, Action: model.Action{Type: model.ActionPagerDuty, RoutingKey: "R0UTING-KEY"}},
		{Name: "Chat", Enabled: true, Action: model.Action{Type: model.ActionDiscord, WebhookURL: "https://discord.com/api/webhooks/1/s3cret"}},
	}
	var saved model.Settings
	if code := call(t, ts, "PUT", "/api/settings", settings, &saved); code != 200 {
		t.Fatalf("put: %d", code)
	}
	if len(saved.Alerts.Channels) != 2 {
		t.Fatalf("channels = %+v", saved.Alerts.Channels)
	}
	pager, chat := saved.Alerts.Channels[0], saved.Alerts.Channels[1]
	if pager.ID == "" || chat.ID == "" || pager.ID == chat.ID || pager.Name != "Pager" || chat.MinSeverity != model.AlertSeverityWarning {
		t.Fatalf("normalised channels = %+v", saved.Alerts.Channels)
	}
	if len(pager.Groups) != 1 || pager.Groups[0] != "Servers" {
		t.Fatalf("groups = %v", pager.Groups)
	}
	if pager.Action.RoutingKey != passwordMask || chat.Action.WebhookURL != passwordMask {
		t.Fatalf("secrets not masked: %+v", saved.Alerts.Channels)
	}
	live := srv.Engine.Settings().Alerts.Channels
	if live[0].Action.RoutingKey != "R0UTING-KEY" || live[1].Action.WebhookURL != "https://discord.com/api/webhooks/1/s3cret" {
		t.Fatalf("engine should hold the real credentials: %+v", live)
	}

	// At rest the credentials are sealed.
	var raw model.Settings
	if err := srv.Store.GetSetting(ctx, "settings", &raw); err != nil {
		t.Fatal(err)
	}
	if !secrets.IsSealed(raw.Alerts.Channels[0].Action.RoutingKey) || !secrets.IsSealed(raw.Alerts.Channels[1].Action.WebhookURL) {
		t.Fatalf("channel secrets stored in cleartext: %+v", raw.Alerts.Channels)
	}

	// Saving the masked document back keeps them; renaming one keeps it too;
	// changing a secret takes the new one.
	saved.Alerts.Channels[0].Name = "On-call"
	saved.Alerts.Channels[1].Action.WebhookURL = "https://discord.com/api/webhooks/2/new"
	if code := call(t, ts, "PUT", "/api/settings", saved, &saved); code != 200 {
		t.Fatalf("re-put: %d", code)
	}
	live = srv.Engine.Settings().Alerts.Channels
	if live[0].Name != "On-call" || live[0].Action.RoutingKey != "R0UTING-KEY" || live[0].ID != pager.ID || live[1].Action.WebhookURL != "https://discord.com/api/webhooks/2/new" {
		t.Fatalf("after re-save: %+v", live)
	}

	// A mask sent for a channel the server never issued restores nothing.
	forged := saved
	forged.Alerts.Channels = []model.AlertChannel{{ID: "ch-unknown", Enabled: true, Action: model.Action{Type: model.ActionPagerDuty, RoutingKey: passwordMask}}}
	if code := call(t, ts, "PUT", "/api/settings", forged, nil); code != 400 {
		t.Fatalf("an unknown channel's mask should not resolve to a credential: %d", code)
	}

	// Only notification types are channels.
	bad := saved
	bad.Alerts.Channels = []model.AlertChannel{{Enabled: true, Action: model.Action{Type: model.ActionScript, Code: "rm -rf /"}}}
	if code := call(t, ts, "PUT", "/api/settings", bad, nil); code != 400 {
		t.Fatalf("a script channel should be refused: %d", code)
	}
	bad.Alerts.Channels = []model.AlertChannel{{Enabled: true, MinSeverity: "info", Action: model.Action{Type: model.ActionPagerDuty, RoutingKey: "k"}}}
	if code := call(t, ts, "PUT", "/api/settings", bad, nil); code != 400 {
		t.Fatalf("an unknown severity should be refused: %d", code)
	}
	// Enabled with neither recipients nor an enabled channel is refused.
	none := saved
	none.Alerts.Channels = nil
	if code := call(t, ts, "PUT", "/api/settings", none, nil); code != 400 {
		t.Fatalf("alerts with nowhere to go should be refused: %d", code)
	}

	// The config export leaves the credentials out.
	resp, err := http.Get(ts.URL + "/api/export/config.json")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), "On-call") {
		t.Fatalf("export: %d", resp.StatusCode)
	}
	if strings.Contains(string(b), "R0UTING-KEY") || strings.Contains(string(b), "webhooks/2/new") {
		t.Fatal("config export carries alert channel credentials")
	}
}

func TestTestAlertChannelUsesStoredSecrets(t *testing.T) {
	ts, _ := newTestServer(t)
	var mu sync.Mutex
	var auth []string
	push := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auth = append(auth, r.Header.Get("Authorization"))
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer push.Close()
	var settings model.Settings
	call(t, ts, "GET", "/api/settings", nil, &settings)
	settings.Alerts.Channels = []model.AlertChannel{{Name: "Phone", Enabled: true, Action: model.Action{Type: model.ActionNtfy, Server: push.URL, Topic: "t", Token: "tk-secret"}}}
	var saved model.Settings
	if code := call(t, ts, "PUT", "/api/settings", settings, &saved); code != 200 {
		t.Fatalf("put: %d", code)
	}
	var res model.ActionResult
	if code := call(t, ts, "POST", "/api/settings/test-channel", map[string]any{"channel": saved.Alerts.Channels[0]}, &res); code != 200 || !res.OK {
		t.Fatalf("test: %d %+v", code, res)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(auth) != 1 || auth[0] != "Bearer tk-secret" {
		t.Fatalf("the test should run with the stored token, got %v", auth)
	}
	if code := call(t, ts, "POST", "/api/settings/test-channel", map[string]any{"channel": model.AlertChannel{Action: model.Action{Type: model.ActionGit, Repo: "/", GitArgs: "status"}}}, nil); code != 400 {
		t.Fatalf("a git action is not a channel: %d", code)
	}
}

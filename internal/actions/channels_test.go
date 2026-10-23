package actions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jxburros/GWatch/internal/model"
)

const (
	testTelegramToken = "123456:ABC-def_ghi"
	testTwilioSID     = "ACXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"
)

func TestValidateChannels(t *testing.T) {
	bad := []model.Action{
		{Type: model.ActionTelegram},
		{Type: model.ActionTelegram, Token: "not-a-token", ChatID: "123"},
		{Type: model.ActionTelegram, Token: testTelegramToken},
		{Type: model.ActionTelegram, Token: testTelegramToken, ChatID: "chat 1"},
		{Type: model.ActionDiscord},
		{Type: model.ActionDiscord, WebhookURL: "http://discord.com/api/webhooks/1/x"},
		{Type: model.ActionDiscord, WebhookURL: "https://{{query.host}}/api/webhooks/1/x"},
		{Type: model.ActionGotify, Token: "t"},
		{Type: model.ActionGotify, Server: "https://gotify.example.com"},
		{Type: model.ActionGotify, Server: "https://{{body}}", Token: "t"},
		{Type: model.ActionGotify, Server: "https://gotify.example.com", Token: "t", Priority: "11"},
		{Type: model.ActionMatrix, Token: "t", RoomID: "!a:b"},
		{Type: model.ActionMatrix, Server: "https://matrix.example.org", RoomID: "!a:b"},
		{Type: model.ActionMatrix, Server: "https://matrix.example.org", Token: "t", RoomID: "#alias:b"},
		{Type: model.ActionTwilio, Token: "t", From: "+15550001111", To: "+15550002222"},
		{Type: model.ActionTwilio, AccountSID: "AC123", Token: "t", From: "+15550001111", To: "+15550002222"},
		{Type: model.ActionTwilio, AccountSID: testTwilioSID, From: "+15550001111", To: "+15550002222"},
		{Type: model.ActionTwilio, AccountSID: testTwilioSID, Token: "t", To: "+15550002222"},
		{Type: model.ActionTwilio, AccountSID: testTwilioSID, Token: "t", From: "+15550001111"},
		{Type: model.ActionTwilio, AccountSID: testTwilioSID, Token: "t", From: "+15550001111", To: "555-0100"},
		{Type: model.ActionPagerDuty},
		{Type: model.ActionPagerDuty, RoutingKey: "k", EventAction: "acknowledge"},
		{Type: model.ActionPagerDuty, RoutingKey: "k", Severity: "huge"},
		{Type: model.ActionOpsgenie},
		{Type: model.ActionOpsgenie, Token: "k", Region: "apac"},
		{Type: model.ActionOpsgenie, Token: "k", Priority: "P9"},
		{Type: model.ActionOpsgenie, Token: "k", EventAction: "snooze"},
	}
	for i, a := range bad {
		if err := Validate(a); err == nil {
			t.Errorf("case %d: expected error for %+v", i, a)
		}
	}
	good := []model.Action{
		{Type: model.ActionTelegram, Token: testTelegramToken, ChatID: "-1001234567890"},
		{Type: model.ActionTelegram, Token: testTelegramToken, ChatID: "@gwatch_alerts"},
		{Type: model.ActionDiscord, WebhookURL: "https://discord.com/api/webhooks/1/x"},
		{Type: model.ActionGotify, Server: "http://gotify.lan", Token: "t", Priority: "8"},
		{Type: model.ActionMatrix, Server: "https://matrix.example.org", Token: "t", RoomID: "!abc:example.org"},
		{Type: model.ActionTwilio, AccountSID: testTwilioSID, Token: "t", From: "+15550001111", To: "+15550002222, +447700900123"},
		{Type: model.ActionTwilio, AccountSID: testTwilioSID, Token: "t", From: "MG0123", To: "+15550002222"},
		{Type: model.ActionPagerDuty, RoutingKey: "k"},
		{Type: model.ActionPagerDuty, RoutingKey: "k", EventAction: "resolve", Severity: "warning"},
		{Type: model.ActionOpsgenie, Token: "k", Region: "eu", EventAction: "close", Priority: "p2"},
	}
	for i, a := range good {
		if err := Validate(a); err != nil {
			t.Errorf("case %d: unexpected error for %+v: %v", i, a, err)
		}
	}
}

func TestIsNotifyCoversEveryChannel(t *testing.T) {
	for _, typ := range []model.ActionType{model.ActionSlack, model.ActionTeams, model.ActionNtfy, model.ActionPushover, model.ActionTelegram, model.ActionDiscord, model.ActionGotify, model.ActionMatrix, model.ActionTwilio, model.ActionPagerDuty, model.ActionOpsgenie} {
		if !typ.Valid() || !typ.IsNotify() {
			t.Errorf("%s: valid=%v notify=%v", typ, typ.Valid(), typ.IsNotify())
		}
	}
	for _, typ := range []model.ActionType{model.ActionHTTP, model.ActionGit, model.ActionScript, model.ActionRunNode} {
		if typ.IsNotify() {
			t.Errorf("%s should not be a notification channel", typ)
		}
	}
}

// swap points one of the pinned provider endpoints at a test server for the
// duration of a test.
func swap(t *testing.T, v *string, to string) {
	orig := *v
	*v = to
	t.Cleanup(func() { *v = orig })
}

func TestRunTelegram(t *testing.T) {
	var path string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		b, _ := readAll(r)
		_ = json.Unmarshal(b, &body)
		w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	defer srv.Close()
	swap(t, &telegramAPIForTest, srv.URL)

	r := &Runner{}
	a := model.Action{Type: model.ActionTelegram, Token: testTelegramToken, ChatID: "-100123", Title: "{{node.name}}", Message: `it's "down" <b>`}
	res := r.Run(context.Background(), a, Vars{"node.name": "Router"})
	if !res.OK {
		t.Fatalf("expected OK, got %+v", res)
	}
	if path != "/bot"+testTelegramToken+"/sendMessage" {
		t.Errorf("path = %q", path)
	}
	if body["chat_id"] != "-100123" || body["text"] != "Router\nit's \"down\" <b>" {
		t.Errorf("body = %v", body)
	}
	if _, ok := body["parse_mode"]; ok {
		t.Error("parse_mode must not be set: a stray * or < would make Telegram reject the alert")
	}
}

func TestRunTelegramNotOKAndTokenScrubbed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":false,"description":"chat not found"}`))
	}))
	defer srv.Close()
	swap(t, &telegramAPIForTest, srv.URL)
	r := &Runner{}
	res := r.Run(context.Background(), model.Action{Type: model.ActionTelegram, Token: testTelegramToken, ChatID: "1"}, Vars{})
	if res.OK || !strings.Contains(res.Error, "chat not found") {
		t.Fatalf("expected a failure naming Telegram's reason, got %+v", res)
	}

	// A transport error quotes the request URL, which carries the token.
	swap(t, &telegramAPIForTest, "http://127.0.0.1:1")
	res = r.Run(context.Background(), model.Action{Type: model.ActionTelegram, Token: testTelegramToken, ChatID: "1"}, Vars{})
	if res.OK || res.Error == "" {
		t.Fatalf("expected a connection failure, got %+v", res)
	}
	if strings.Contains(res.Error+res.Output, testTelegramToken) {
		t.Fatalf("bot token leaked into the result: %q", res.Error)
	}
}

func TestRunDiscord(t *testing.T) {
	var body map[string]any
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := readAll(r)
		_ = json.Unmarshal(b, &body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	r := &Runner{}
	a := model.Action{Type: model.ActionDiscord, WebhookURL: srv.URL + "/api/webhooks/1/x", Title: "T", Message: "@everyone {{message}}", IgnoreTLSErrors: true}
	res := r.Run(context.Background(), a, Vars{"message": strings.Repeat("x", 3000)})
	if !res.OK {
		t.Fatalf("expected OK, got %+v", res)
	}
	content, _ := body["content"].(string)
	if !strings.HasPrefix(content, "**T**\n@everyone ") || len([]rune(content)) != 2000 {
		t.Errorf("content = %.40q (len %d)", content, len([]rune(content)))
	}
	am, _ := body["allowed_mentions"].(map[string]any)
	if p, ok := am["parse"].([]any); !ok || len(p) != 0 {
		t.Errorf("allowed_mentions = %v; @everyone must not ping", body["allowed_mentions"])
	}
}

func TestRunGotify(t *testing.T) {
	var path, key, query string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, key, query = r.URL.Path, r.Header.Get("X-Gotify-Key"), r.URL.RawQuery
		b, _ := readAll(r)
		_ = json.Unmarshal(b, &body)
		w.Write([]byte(`{"id":1}`))
	}))
	defer srv.Close()
	r := &Runner{}
	a := model.Action{Type: model.ActionGotify, Server: srv.URL + "/", Token: "app-token", Priority: "8", Message: "{{message}}"}
	res := r.Run(context.Background(), a, Vars{"message": "down"})
	if !res.OK {
		t.Fatalf("expected OK, got %+v", res)
	}
	if path != "/message" || key != "app-token" || query != "" {
		t.Errorf("path=%q key=%q query=%q (the token must go in the header, not the URL)", path, key, query)
	}
	if body["message"] != "down" || body["priority"] != float64(8) {
		t.Errorf("body = %v", body)
	}
}

func TestGotifyKeyIsNotForwardedAcrossRedirects(t *testing.T) {
	var leaked string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("X-Gotify-Key")
		w.WriteHeader(200)
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/stolen", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	r := &Runner{}
	r.Run(context.Background(), model.Action{Type: model.ActionGotify, Server: srv.URL, Token: "app-token"}, Vars{})
	if leaked != "" {
		t.Fatalf("Gotify token forwarded to the redirect target: %q", leaked)
	}
}

func TestRunMatrix(t *testing.T) {
	var method, rawPath, auth string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, rawPath, auth = r.Method, r.URL.EscapedPath(), r.Header.Get("Authorization")
		b, _ := readAll(r)
		_ = json.Unmarshal(b, &body)
		w.Write([]byte(`{"event_id":"$abc"}`))
	}))
	defer srv.Close()
	r := &Runner{}
	a := model.Action{Type: model.ActionMatrix, Server: srv.URL, Token: "syt_token", RoomID: "!room:example.org", Title: "T", Message: "M"}
	res := r.Run(context.Background(), a, Vars{})
	if !res.OK {
		t.Fatalf("expected OK, got %+v", res)
	}
	if method != http.MethodPut || !strings.HasPrefix(rawPath, "/_matrix/client/v3/rooms/%21room:example.org/send/m.room.message/gwatch-") {
		t.Errorf("method=%s path=%s", method, rawPath)
	}
	if auth != "Bearer syt_token" || body["msgtype"] != "m.text" || body["body"] != "T\nM" {
		t.Errorf("auth=%q body=%v", auth, body)
	}
}

func TestRunTwilio(t *testing.T) {
	var forms []string
	var users []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/2010-04-01/Accounts/"+testTwilioSID+"/Messages.json" {
			t.Errorf("path = %q", r.URL.Path)
		}
		u, p, _ := r.BasicAuth()
		users = append(users, u+":"+p)
		_ = r.ParseForm()
		forms = append(forms, r.Form.Encode())
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"sid":"SM1","status":"queued"}`))
	}))
	defer srv.Close()
	swap(t, &twilioAPIForTest, srv.URL)
	r := &Runner{}
	a := model.Action{Type: model.ActionTwilio, AccountSID: testTwilioSID, Token: "secret", From: "+15550001111", To: "+15550002222, +447700900123", Message: "{{node.name}} down"}
	res := r.Run(context.Background(), a, Vars{"node.name": "NAS"})
	if !res.OK {
		t.Fatalf("expected OK, got %+v", res)
	}
	if len(forms) != 2 || !strings.Contains(forms[0], "To=%2B15550002222") || !strings.Contains(forms[1], "To=%2B447700900123") || !strings.Contains(forms[0], "Body=NAS+down") || !strings.Contains(forms[0], "From=%2B15550001111") {
		t.Errorf("forms = %v", forms)
	}
	if users[0] != testTwilioSID+":secret" {
		t.Errorf("basic auth = %q", users[0])
	}
}

func TestRunTwilioPartialFailure(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		_ = r.ParseForm()
		if r.Form.Get("MessagingServiceSid") != "MG42" {
			t.Errorf("a sender starting MG should be sent as MessagingServiceSid: %v", r.Form)
		}
		if n == 2 {
			w.WriteHeader(400)
			w.Write([]byte(`{"code":21211,"message":"invalid To"}`))
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	swap(t, &twilioAPIForTest, srv.URL)
	r := &Runner{}
	res := r.Run(context.Background(), model.Action{Type: model.ActionTwilio, AccountSID: testTwilioSID, Token: "s", From: "MG42", To: "+15550002222 +15550003333"}, Vars{})
	if res.OK || !strings.Contains(res.Error, "+15550003333") {
		t.Fatalf("expected a failure naming the number that missed out, got %+v", res)
	}
}

func TestRunPagerDutyTriggerAndAutoResolve(t *testing.T) {
	var got []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := readAll(r)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		got = append(got, m)
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"status":"success","message":"Event processed","dedup_key":"x"}`))
	}))
	defer srv.Close()
	swap(t, &pagerDutyURLForTest, srv.URL)
	r := &Runner{}
	a := model.Action{Type: model.ActionPagerDuty, RoutingKey: "rk"}
	vars := Vars{"event": "down", "node.id": "4", "check.id": "9", "node.name": "NAS", "node.host": "10.0.0.5", "check.name": "Ping", "message": "timeout"}
	if res := r.Run(context.Background(), a, vars); !res.OK {
		t.Fatalf("trigger: %+v", res)
	}
	vars["event"] = "recovered"
	if res := r.Run(context.Background(), a, vars); !res.OK {
		t.Fatalf("resolve: %+v", res)
	}
	if len(got) != 2 {
		t.Fatalf("got %d events", len(got))
	}
	trig, res := got[0], got[1]
	if trig["event_action"] != "trigger" || trig["routing_key"] != "rk" || trig["dedup_key"] != "gwatch-node-4-check-9" {
		t.Errorf("trigger = %v", trig)
	}
	p, _ := trig["payload"].(map[string]any)
	if p["severity"] != "critical" || p["source"] != "10.0.0.5" || p["component"] != "Ping" || p["summary"] == "" {
		t.Errorf("payload = %v", p)
	}
	if res["event_action"] != "resolve" || res["dedup_key"] != trig["dedup_key"] {
		t.Errorf("resolve = %v (must reuse the trigger's dedup_key)", res)
	}
	if _, ok := res["payload"]; ok {
		t.Error("a resolve carries no payload")
	}

	// A warning files under its own key with warning severity.
	got = nil
	vars["event"] = "degraded"
	r.Run(context.Background(), a, vars)
	if got[0]["dedup_key"] != "gwatch-node-4-check-9-warning" || got[0]["payload"].(map[string]any)["severity"] != "warning" {
		t.Errorf("warning = %v", got[0])
	}
}

func TestRunPagerDutyRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"status":"invalid event","message":"Event object is invalid"}`))
	}))
	defer srv.Close()
	swap(t, &pagerDutyURLForTest, srv.URL)
	res := (&Runner{}).Run(context.Background(), model.Action{Type: model.ActionPagerDuty, RoutingKey: "rk", DedupKey: "custom-{{rule.id}}"}, Vars{"rule.id": "3"})
	if res.OK || res.StatusCode != 400 || !strings.Contains(res.Output, "custom-3") {
		t.Fatalf("expected a 400 failure using the custom key, got %+v", res)
	}
}

func TestRunOpsgenieCreateAndClose(t *testing.T) {
	type call struct {
		path, query, auth string
		body              map[string]any
	}
	var calls []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := readAll(r)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		calls = append(calls, call{r.URL.EscapedPath(), r.URL.RawQuery, r.Header.Get("Authorization"), m})
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"result":"Request will be processed","requestId":"r1"}`))
	}))
	defer srv.Close()
	swap(t, &opsgenieEUAPIForTest, srv.URL)
	r := &Runner{}
	a := model.Action{Type: model.ActionOpsgenie, Token: "gk", Region: "eu", Priority: "p1", Tags: "gwatch, lan"}
	vars := Vars{"event": "rule_fired", "rule.id": "7", "node.name": "DNS quorum", "status": "met", "message": "2 of 3 conditions met"}
	if res := r.Run(context.Background(), a, vars); !res.OK {
		t.Fatalf("create: %+v", res)
	}
	vars["event"] = "rule_cleared"
	if res := r.Run(context.Background(), a, vars); !res.OK {
		t.Fatalf("close: %+v", res)
	}
	c, cl := calls[0], calls[1]
	if c.path != "/v2/alerts" || c.auth != "GenieKey gk" || c.body["alias"] != "gwatch-rule-7" || c.body["priority"] != "P1" || c.body["entity"] != "DNS quorum" {
		t.Errorf("create = %+v", c)
	}
	if tags, _ := c.body["tags"].([]any); len(tags) != 2 {
		t.Errorf("tags = %v", c.body["tags"])
	}
	if cl.path != "/v2/alerts/gwatch-rule-7/close" || cl.query != "identifierType=alias" || cl.auth != "GenieKey gk" {
		t.Errorf("close = %+v", cl)
	}
}

func TestOpsgenieAliasIsOnePathSegment(t *testing.T) {
	var raw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw = r.URL.EscapedPath()
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	swap(t, &opsgenieUSAPIForTest, srv.URL)
	(&Runner{}).Run(context.Background(), model.Action{Type: model.ActionOpsgenie, Token: "gk", EventAction: "close", DedupKey: "{{message}}"}, Vars{"message": "../../v1/x?y"})
	if !strings.HasPrefix(raw, "/v2/alerts/..%2F..%2Fv1%2Fx%3Fy/close") {
		t.Fatalf("alias escaped wrongly: %q", raw)
	}
}

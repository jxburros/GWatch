package actions

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jxburros/GWatch/internal/model"
)

func readAll(r *http.Request) ([]byte, error) { return io.ReadAll(r.Body) }

func TestValidateNotify(t *testing.T) {
	bad := []model.Action{
		{Type: model.ActionSlack},
		{Type: model.ActionSlack, WebhookURL: "http://hooks.slack.com/x"},
		{Type: model.ActionTeams},
		{Type: model.ActionTeams, WebhookURL: "http://example.com/webhook"},
		{Type: model.ActionNtfy},
		{Type: model.ActionNtfy, Topic: "bad topic!"},
		{Type: model.ActionNtfy, Topic: "ok", Server: "ftp://ntfy.sh"},
		{Type: model.ActionPushover},
		{Type: model.ActionPushover, Token: "t"},
		{Type: model.ActionPushover, UserKey: "u"},
	}
	for i, a := range bad {
		if err := Validate(a); err == nil {
			t.Errorf("case %d: expected error for %+v", i, a)
		}
	}
	good := []model.Action{
		{Type: model.ActionSlack, WebhookURL: "https://hooks.slack.com/services/x"},
		{Type: model.ActionSlack, WebhookURL: "{{query.webhook}}"},
		{Type: model.ActionTeams, WebhookURL: "https://example.webhook.office.com/x"},
		{Type: model.ActionNtfy, Topic: "gwatch-alerts"},
		{Type: model.ActionNtfy, Topic: "gwatch-alerts", Server: "https://ntfy.example.com"},
		{Type: model.ActionPushover, Token: "tok", UserKey: "user"},
	}
	for i, a := range good {
		if err := Validate(a); err != nil {
			t.Errorf("case %d: unexpected error for %+v: %v", i, a, err)
		}
	}
}

func TestRunSlack(t *testing.T) {
	var gotBody string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := readAll(r)
		gotBody = string(b)
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content-type = %q", r.Header.Get("Content-Type"))
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	r := &Runner{}
	a := model.Action{Type: model.ActionSlack, WebhookURL: srv.URL, Message: `it's "quoted"` + "\nand a newline", IgnoreTLSErrors: true}
	res := r.Run(context.Background(), a, Vars{})
	if !res.OK {
		t.Fatalf("expected OK, got %+v", res)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(gotBody), &payload); err != nil {
		t.Fatalf("slack body is not valid JSON: %v (%s)", err, gotBody)
	}
	if payload["text"] != `it's "quoted"`+"\nand a newline" {
		t.Errorf("text = %q", payload["text"])
	}
}

func TestRunSlackFailure(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("boom"))
	}))
	defer srv.Close()

	r := &Runner{}
	res := r.Run(context.Background(), model.Action{Type: model.ActionSlack, WebhookURL: srv.URL, Message: "hi", IgnoreTLSErrors: true}, Vars{})
	if res.OK {
		t.Fatal("expected failure on non-2xx")
	}
	if res.StatusCode != 500 {
		t.Errorf("statusCode = %d", res.StatusCode)
	}
}

func TestRunTeams(t *testing.T) {
	var payload map[string]any
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := readAll(r)
		if err := json.Unmarshal(b, &payload); err != nil {
			t.Fatalf("teams body is not valid JSON: %v (%s)", err, b)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	r := &Runner{}
	a := model.Action{Type: model.ActionTeams, WebhookURL: srv.URL, Title: "{{node.name}} is {{status}}", Message: `msg with "quotes" and {{missing}}`, IgnoreTLSErrors: true}
	vars := Vars{"node.name": "Router", "status": "down"}
	res := r.Run(context.Background(), a, vars)
	if !res.OK {
		t.Fatalf("expected OK, got %+v", res)
	}
	if payload["type"] != "message" {
		t.Errorf("type = %v", payload["type"])
	}
	raw, _ := json.Marshal(payload)
	body := string(raw)
	if !strings.Contains(body, "Router is down") {
		t.Errorf("card missing expanded title: %s", body)
	}
	if !strings.Contains(body, `msg with \"quotes\" and`) {
		t.Errorf("card missing expanded message: %s", body)
	}
	if !strings.Contains(body, "application/vnd.microsoft.card.adaptive") {
		t.Errorf("card missing adaptive content type: %s", body)
	}
}

func TestRunNtfy(t *testing.T) {
	var gotPath, gotBody, gotTitle, gotPriority, gotTags, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := readAll(r)
		gotBody = string(b)
		gotTitle = r.Header.Get("Title")
		gotPriority = r.Header.Get("Priority")
		gotTags = r.Header.Get("Tags")
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	r := &Runner{}
	a := model.Action{Type: model.ActionNtfy, Server: srv.URL, Topic: "gwatch-alerts", Title: "{{node.name}}", Message: "{{message}}", Priority: "urgent", Tags: "warning,down", Token: "tk123"}
	vars := Vars{"node.name": "Router", "message": "down"}
	res := r.Run(context.Background(), a, vars)
	if !res.OK {
		t.Fatalf("expected OK, got %+v", res)
	}
	if gotPath != "/gwatch-alerts" {
		t.Errorf("path = %q", gotPath)
	}
	if gotBody != "down" {
		t.Errorf("body = %q", gotBody)
	}
	if gotTitle != "Router" {
		t.Errorf("title header = %q", gotTitle)
	}
	if gotPriority != "urgent" {
		t.Errorf("priority header = %q", gotPriority)
	}
	if gotTags != "warning,down" {
		t.Errorf("tags header = %q", gotTags)
	}
	if gotAuth != "Bearer tk123" {
		t.Errorf("authorization header = %q", gotAuth)
	}
}

func TestRunNtfyDefaultServer(t *testing.T) {
	r := &Runner{}
	res := r.Run(context.Background(), model.Action{Type: model.ActionNtfy, Topic: "x"}, Vars{})
	// no real network access in tests: expect it to at least attempt
	// https://ntfy.sh/x and fail with a network error, not a validation error.
	if res.Error == "" {
		t.Skip("network reachable; nothing to assert about the default server")
	}
	if strings.Contains(res.Error, "must start with") {
		t.Errorf("unexpected validation-style error for default server: %s", res.Error)
	}
}

func TestRunPushover(t *testing.T) {
	var gotForm string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		gotForm = r.Form.Encode()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"status":1,"request":"abc"}`))
	}))
	defer srv.Close()

	r := &Runner{}
	orig := pushoverURLForTest
	defer func() { pushoverURLForTest = orig }()
	pushoverURLForTest = srv.URL

	a := model.Action{Type: model.ActionPushover, Token: "tok", UserKey: "user", Title: "{{node.name}}", Message: "{{message}}", Priority: "1"}
	vars := Vars{"node.name": "Router", "message": "down"}
	res := r.Run(context.Background(), a, vars)
	if !res.OK {
		t.Fatalf("expected OK, got %+v", res)
	}
	if !strings.Contains(gotForm, "token=tok") || !strings.Contains(gotForm, "user=user") || !strings.Contains(gotForm, "title=Router") || !strings.Contains(gotForm, "message=down") || !strings.Contains(gotForm, "priority=1") {
		t.Errorf("form = %q", gotForm)
	}
}

func TestRunPushoverNonSuccessStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{"status":0,"errors":["invalid token"]}`))
	}))
	defer srv.Close()

	r := &Runner{}
	orig := pushoverURLForTest
	defer func() { pushoverURLForTest = orig }()
	pushoverURLForTest = srv.URL

	res := r.Run(context.Background(), model.Action{Type: model.ActionPushover, Token: "bad", UserKey: "user"}, Vars{})
	if res.OK {
		t.Fatal("expected failure on status != 1")
	}
}

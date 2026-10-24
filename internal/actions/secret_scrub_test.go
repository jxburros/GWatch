package actions

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jxburros/GWatch/internal/model"
)

// A webhook URL is the credential of a Slack, Teams or Discord channel, and an
// HTTP action's URL often carries one. A transport error quotes the request
// URL, and the result's first line becomes a timeline entry that viewers and
// read-only API keys can read, so the secret part must not survive into it.
func TestResultDoesNotCarryWebhookSecret(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := srv.URL
	srv.Close() // nothing listens there now: the request fails in the transport

	const secret = "s3cr3tWebhookPart"
	for _, a := range []model.Action{
		{Type: model.ActionSlack, WebhookURL: addr + "/services/T0/B0/" + secret},
		{Type: model.ActionDiscord, WebhookURL: addr + "/api/webhooks/1/" + secret + "{{node.name}}"},
		{Type: model.ActionHTTP, URL: addr + "/hook?token=" + secret, Method: "POST", Body: "x"},
	} {
		res := (&Runner{}).Run(context.Background(), a, Vars{"node.name": "nas"})
		if res.OK {
			t.Fatalf("%s: expected a failure", a.Type)
		}
		if strings.Contains(res.Error, secret) || strings.Contains(res.Output, secret) {
			t.Errorf("%s: secret in result: error %q output %q", a.Type, res.Error, res.Output)
		}
		if !strings.Contains(res.Error, strings.TrimPrefix(addr, "https://")) {
			t.Errorf("%s: the host should still be named: %q", a.Type, res.Error)
		}
	}

	// A successful HTTP action reports what it called; the query that holds
	// the token is left out of that too.
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ok.Close()
	res := (&Runner{}).Run(context.Background(), model.Action{Type: model.ActionHTTP, URL: ok.URL + "/hook?token=" + secret}, nil)
	if !res.OK || strings.Contains(res.Output, secret) {
		t.Errorf("ok=%v output %q", res.OK, res.Output)
	}
}

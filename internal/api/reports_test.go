package api

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

// The groups and tags of an on-demand report are typed by hand as a list, so
// "Office, Lab" has to mean the group "Lab" and not " Lab", which no node is in.
func TestGenerateReportTrimsGroupsAndTags(t *testing.T) {
	ts, _ := newTestServer(t)
	if code := call(t, ts, "POST", "/api/nodes", map[string]any{
		"name": "lab-switch", "host": "127.0.0.1", "groups": []string{"Lab"}, "tags": []string{"core"},
	}, nil); code != 201 {
		t.Fatalf("create node: %d", code)
	}
	now := time.Now()
	q := url.Values{
		"from":   {now.Add(-time.Hour).Format(time.RFC3339)},
		"to":     {now.Format(time.RFC3339)},
		"groups": {"Office, Lab"},
		"tags":   {"edge, core"},
	}
	code, body := callBody(t, ts, "GET", "/api/reports/generate?"+q.Encode(), nil)
	if code != 200 {
		t.Fatalf("generate: %d %s", code, body)
	}
	if !strings.Contains(body, "lab-switch") {
		t.Fatalf("a space after the comma should not drop the node from the report: %s", body)
	}
}

package mailer

import (
	"strings"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// #99: a grouped alert is one e-mail whose subject names the node and how
// many checks went down, and whose body still carries every check's own
// message and details, in both the text and the HTML part, escaped.
func TestBuildGroupedAlertEmail(t *testing.T) {
	when := time.Date(2026, 10, 23, 9, 30, 0, 0, time.UTC)
	items := []AlertItem{
		{CheckName: "HTTP", Status: model.StatusDown, Message: "connection refused", Details: map[string]string{"Target": "http://web:80", "Error": "dial tcp: refused"}},
		{CheckName: "JSON <API>", Status: model.StatusDown, Message: "timeout", Details: map[string]string{"Target": "http://web/api"}},
		{CheckName: "TCP 443", Status: model.StatusDown, Message: "no route", Details: map[string]string{"Empty": " "}},
	}
	m := BuildGroupedAlertEmail(KindDown, "Home", "Web server", items, when)
	if m.Subject != "[Home] DOWN: Web server — 3 checks down" {
		t.Fatalf("subject = %q", m.Subject)
	}
	for _, want := range []string{"Web server: 3 checks down", "== HTTP ==", "connection refused", "Error: dial tcp: refused", "== JSON <API> ==", "timeout", "== TCP 443 ==", "no route", "Sent by Home"} {
		if !strings.Contains(m.TextBody, want) {
			t.Errorf("text body lacks %q:\n%s", want, m.TextBody)
		}
	}
	if strings.Contains(m.TextBody, "Empty") {
		t.Error("a blank detail should be left out")
	}
	for _, want := range []string{"JSON &lt;API&gt;", "http://web/api", "dial tcp: refused", "Web server: 3 checks down"} {
		if !strings.Contains(m.HTMLBody, want) {
			t.Errorf("html body lacks %q", want)
		}
	}
	if strings.Contains(m.HTMLBody, "JSON <API>") {
		t.Error("the html body must escape check names")
	}

	rec := BuildGroupedAlertEmail(KindRecovered, "", "NAS", items[:2], when)
	if rec.Subject != "[GWatch] RECOVERED: NAS — 2 checks recovered" {
		t.Fatalf("recovery subject = %q", rec.Subject)
	}
	if got := GroupSummary(KindWarning, items[:2]); got != "2 checks need attention: HTTP, JSON <API>" {
		t.Fatalf("summary = %q", got)
	}
}

// The digest lists every warning with its node, and says how many were left
// out when it hit its bound.
func TestBuildWarningDigestEmail(t *testing.T) {
	from := time.Date(2026, 10, 23, 8, 0, 0, 0, time.UTC)
	items := []AlertItem{
		{NodeName: "NAS", CheckName: "Disk", Status: model.StatusDegraded, Message: "disk /srv 91%", When: from.Add(5 * time.Minute)},
		{NodeName: "Router", CheckName: "Ping", Status: model.StatusDegraded, Message: "packet loss 20%", When: from.Add(40 * time.Minute)},
	}
	m := BuildWarningDigestEmail("Home", model.WarningDigestHourly, items, 3, from, from.Add(time.Hour))
	if m.Subject != "[Home] Hourly warning digest: 5 warnings" {
		t.Fatalf("subject = %q", m.Subject)
	}
	for _, want := range []string{"== NAS — Disk ==", "disk /srv 91%", "== Router — Ping ==", "packet loss 20%", "3 earlier warnings were left out"} {
		if !strings.Contains(m.TextBody, want) {
			t.Errorf("text body lacks %q:\n%s", want, m.TextBody)
		}
	}
	if !strings.Contains(m.HTMLBody, "NAS — Disk") || !strings.Contains(m.HTMLBody, "3 earlier warnings") {
		t.Error("html body lacks the items or the left-out note")
	}
	one := BuildWarningDigestEmail("", model.WarningDigestDaily, items[:1], 0, from, from.Add(24*time.Hour))
	if one.Subject != "[GWatch] Daily warning digest: 1 warning" {
		t.Fatalf("subject = %q", one.Subject)
	}
}

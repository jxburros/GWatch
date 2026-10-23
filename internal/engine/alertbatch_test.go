package engine

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/mailer"
	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
)

// groupedSetup is setup with alert grouping switched on: window "seconds"
// that each last unit, so a window closes in milliseconds. It returns the
// engine already started.
func groupedSetup(t *testing.T, window int, unit time.Duration) (*Engine, *store.Store, *fakeNet, *mailbox) {
	t.Helper()
	old := groupWindowUnit
	groupWindowUnit = unit
	t.Cleanup(func() { groupWindowUnit = old })
	e, st, net, mb := setup(t)
	ctx := context.Background()
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Stop)
	withChannels(t, e, nil, func(s *model.Settings) { s.Alerts.GroupWindowSeconds = window })
	return e, st, net, mb
}

func makeNode(t *testing.T, e *Engine, st *store.Store, name string, checkNames ...string) []int64 {
	t.Helper()
	ctx := context.Background()
	n := model.Node{Name: name, Host: strings.ToLower(name) + ".lan", Enabled: true}
	for _, c := range checkNames {
		n.Checks = append(n.Checks, model.Check{Type: model.CheckTCP, Name: c, Enabled: true, IntervalSeconds: 3600, TimeoutSeconds: 5, Config: model.CheckConfig{Port: 80}})
	}
	created, err := st.CreateNode(ctx, n)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.ReloadConfig(ctx); err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 0, len(created.Checks))
	for _, c := range created.Checks {
		ids = append(ids, c.ID)
		if _, err := e.RunNow(ctx, c.ID); err != nil { // a first, healthy result
			t.Fatal(err)
		}
	}
	return ids
}

// takeDown fails a check until it is down (the threshold is 2).
func takeDown(t *testing.T, e *Engine, net *fakeNet, ids ...int64) {
	t.Helper()
	for _, id := range ids {
		setDown(net, id, true)
		for i := 0; i < 2; i++ {
			if _, err := e.RunNow(context.Background(), id); err != nil {
				t.Fatal(err)
			}
		}
		if s, _ := e.State(id); s.Status != model.StatusDown {
			t.Fatalf("check %d is %s, want down", id, s.Status)
		}
	}
}

func bringUp(t *testing.T, e *Engine, net *fakeNet, ids ...int64) {
	t.Helper()
	for _, id := range ids {
		setDown(net, id, false)
		if _, err := e.RunNow(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}
}

// #99: one web server stopping used to send an e-mail for each of its
// checks. Grouped, it sends one that lists them all, and one when they come
// back; another node's outage at the same time gets an e-mail of its own.
func TestAlertGroupingOneMailPerNode(t *testing.T) {
	e, st, net, mb := groupedSetup(t, 50, 10*time.Millisecond)
	web := makeNode(t, e, st, "Web", "HTTP", "JSON", "TCP")
	nas := makeNode(t, e, st, "NAS", "SMB", "Ping")

	takeDown(t, e, net, web...)
	takeDown(t, e, net, nas...)
	waitFor(t, func() bool { return mb.count() == 2 })
	time.Sleep(200 * time.Millisecond)
	subjects := mb.subjects()
	if len(subjects) != 2 {
		t.Fatalf("subjects = %v", subjects)
	}
	got := strings.Join(subjects, " | ")
	if !strings.Contains(got, "DOWN: Web — 3 checks down") || !strings.Contains(got, "DOWN: NAS — 2 checks down") {
		t.Fatalf("subjects = %v", subjects)
	}
	mb.mu.Lock()
	for _, m := range mb.sent {
		if strings.Contains(m.Subject, "Web") {
			for _, c := range []string{"== HTTP ==", "== JSON ==", "== TCP ==", "connection refused"} {
				if !strings.Contains(m.TextBody, c) {
					t.Errorf("grouped e-mail lacks %q:\n%s", c, m.TextBody)
				}
			}
		}
	}
	mb.mu.Unlock()
	// Every check's own state and timeline is as if it had been alerted
	// alone.
	for _, id := range append(append([]int64{}, web...), nas...) {
		s, _ := e.State(id)
		if !s.AlertActive || s.LastAlertAt == nil {
			t.Fatalf("check %d: AlertActive=%v LastAlertAt=%v", id, s.AlertActive, s.LastAlertAt)
		}
		if n := eventTypes(t, st, id)[model.EventAlertSent]; n != 1 {
			t.Fatalf("check %d: %d alert_sent events, want 1", id, n)
		}
	}

	bringUp(t, e, net, web...)
	waitFor(t, func() bool { return mb.count() == 3 })
	time.Sleep(200 * time.Millisecond)
	if s := mb.subjects(); len(s) != 3 || !strings.Contains(s[2], "RECOVERED: Web — 3 checks recovered") {
		t.Fatalf("subjects = %v", s)
	}
}

// A window that closes with one alert in it sends that alert as an ordinary
// e-mail, and an alert after the window has closed opens a new one.
func TestAlertGroupWindowExpires(t *testing.T) {
	e, st, net, mb := groupedSetup(t, 5, 10*time.Millisecond)
	ids := makeNode(t, e, st, "Web", "HTTP", "TCP")
	takeDown(t, e, net, ids[0])
	waitFor(t, func() bool { return mb.count() == 1 })
	takeDown(t, e, net, ids[1])
	waitFor(t, func() bool { return mb.count() == 2 })
	s := mb.subjects()
	if !strings.HasSuffix(s[0], "DOWN: Web — HTTP") || !strings.HasSuffix(s[1], "DOWN: Web — TCP") {
		t.Fatalf("subjects = %v", s)
	}
}

// A check that recovers while its down alert is still waiting in the window
// is never reported at all, neither down nor recovered; the rest of its
// group still goes out; and the cooldown is not consumed by the alert that
// was never sent. The window does not hold up the check's results either.
func TestAlertGroupRecoveryCancelsUnsentDown(t *testing.T) {
	e, st, net, mb := groupedSetup(t, 100, 10*time.Millisecond)
	ids := makeNode(t, e, st, "Web", "HTTP", "TCP")
	start := time.Now()
	takeDown(t, e, net, ids...)
	bringUp(t, e, net, ids[0])
	if took := time.Since(start); took > 900*time.Millisecond {
		t.Fatalf("processing waited on the grouping window: %s", took)
	}
	waitFor(t, func() bool { return mb.count() == 1 })
	time.Sleep(300 * time.Millisecond)
	if s := mb.subjects(); len(s) != 1 || !strings.HasSuffix(s[0], "DOWN: Web — TCP") {
		t.Fatalf("subjects = %v", s)
	}
	s, _ := e.State(ids[0])
	if s.AlertActive || s.LastAlertAt != nil {
		t.Fatalf("recovered check: AlertActive=%v LastAlertAt=%v", s.AlertActive, s.LastAlertAt)
	}
	types := eventTypes(t, st, ids[0])
	if types[model.EventAlertSuppressed] != 1 || types[model.EventAlertSent] != 0 {
		t.Fatalf("events: %+v", types)
	}
	// With the cooldown untouched, its next outage is alerted.
	takeDown(t, e, net, ids[0])
	waitFor(t, func() bool { return mb.count() == 2 })
}

// A grouped e-mail that fails re-arms every check in it, so their next
// failing results alert again.
func TestAlertGroupFailureRearmsAll(t *testing.T) {
	e, st, net, mb := groupedSetup(t, 50, 10*time.Millisecond)
	var mu sync.Mutex
	fail := true
	e.opts.Send = func(ctx context.Context, s model.SMTPSettings, m mailer.Message) error {
		mu.Lock()
		defer mu.Unlock()
		if fail {
			return errors.New("554 rejected")
		}
		return mb.send(ctx, s, m)
	}
	ids := makeNode(t, e, st, "Web", "HTTP", "JSON", "TCP")
	takeDown(t, e, net, ids...)
	waitFor(t, func() bool {
		for _, id := range ids {
			if s, _ := e.State(id); s.AlertActive || s.LastAlertAt != nil || eventTypes(t, st, id)[model.EventAlertFailed] != 1 {
				return false
			}
		}
		return true
	})
	mu.Lock()
	fail = false
	mu.Unlock()
	for _, id := range ids {
		if _, err := e.RunNow(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, func() bool { return mb.count() == 1 })
	if s := mb.subjects(); !strings.Contains(s[0], "3 checks down") {
		t.Fatalf("subjects = %v", s)
	}
	for _, id := range ids {
		if s, _ := e.State(id); !s.AlertActive {
			t.Fatalf("check %d not alerted after the retry", id)
		}
	}
}

// Stop sends what is waiting in a window at once, rather than waiting the
// window out or losing it.
func TestStopFlushesAlertGroups(t *testing.T) {
	e, st, net, mb := groupedSetup(t, 300, time.Second)
	ids := makeNode(t, e, st, "Web", "HTTP", "TCP")
	takeDown(t, e, net, ids...)
	if mb.count() != 0 {
		t.Fatal("the window should still be open")
	}
	start := time.Now()
	e.Stop()
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("Stop took %s", took)
	}
	if s := mb.subjects(); len(s) != 1 || !strings.Contains(s[0], "2 checks down") {
		t.Fatalf("subjects = %v", s)
	}
}

// A grouped alert is one message per chat channel, with the summary as its
// {{message}}.
func TestAlertGroupOneChannelMessage(t *testing.T) {
	e, st, net, mb := groupedSetup(t, 50, 10*time.Millisecond)
	box := &pushbox{}
	srv := httptest.NewServer(http.HandlerFunc(box.handler))
	defer srv.Close()
	withChannels(t, e, []model.AlertChannel{{ID: "ch-1", Name: "Phone", Enabled: true, MinSeverity: model.AlertSeverityWarning,
		Action: model.Action{Type: model.ActionNtfy, Server: srv.URL, Topic: "gwatch", Title: "{{alert.kind}} {{alert.count}}"}}},
		func(s *model.Settings) { s.Alerts.GroupWindowSeconds = 50 })
	ids := makeNode(t, e, st, "Web", "HTTP", "TCP")
	takeDown(t, e, net, ids...)
	waitFor(t, func() bool { return mb.count() == 1 && box.count() == 1 })
	time.Sleep(200 * time.Millisecond)
	if got := box.all(); len(got) != 1 || got[0] != "down 2|Web is down: 2 checks down: HTTP, TCP" {
		t.Fatalf("channel messages = %q", got)
	}
	for _, id := range ids {
		if n := eventTypes(t, st, id)[model.EventAlertSent]; n != 2 { // the e-mail and the channel
			t.Fatalf("check %d: %d alert_sent events", id, n)
		}
	}
}

func setDegradedResult(net *fakeNet, id int64, warning string) {
	net.mu.Lock()
	defer net.mu.Unlock()
	if net.custom == nil {
		net.custom = map[int64]func() model.Result{}
	}
	net.custom[id] = func() model.Result {
		lat := 900.0
		return model.Result{Timestamp: time.Now(), Success: true, Status: model.StatusDegraded, Message: warning, LatencyMS: &lat, Attempts: 1, Warnings: []string{warning}}
	}
}

// #99: with the hourly digest, warnings are not e-mailed one by one but
// collected (in the database) and sent as one summary on the hour, while a
// down alert still goes out at once. A digest that fails is kept and tried
// again later.
func TestWarningDigest(t *testing.T) {
	e, st, net, mb := groupedSetup(t, 0, time.Millisecond)
	withChannels(t, e, nil, func(s *model.Settings) { s.Alerts.WarningDigest = model.WarningDigestHourly })
	ctx := context.Background()
	ids := makeNode(t, e, st, "NAS", "Disk", "Ping", "SMB")
	setDegradedResult(net, ids[0], "disk /srv 91%")
	setDegradedResult(net, ids[1], "latency 900 ms")
	for _, id := range ids[:2] {
		if _, err := e.RunNow(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	takeDown(t, e, net, ids[2])
	waitFor(t, func() bool { return mb.count() == 1 })
	time.Sleep(100 * time.Millisecond)
	if s := mb.subjects(); len(s) != 1 || !strings.HasSuffix(s[0], "DOWN: NAS — SMB") {
		t.Fatalf("only the down alert should have been sent: %v", s)
	}
	now := time.Now()
	if err := e.runDigest(ctx, now); err != nil || mb.count() != 1 {
		t.Fatalf("the digest is not due yet: err=%v mails=%d", err, mb.count())
	}

	var mu sync.Mutex
	fail := true
	e.opts.Send = func(ctx context.Context, s model.SMTPSettings, m mailer.Message) error {
		mu.Lock()
		defer mu.Unlock()
		if fail {
			return errors.New("421 try later")
		}
		return mb.send(ctx, s, m)
	}
	due := now.Add(2 * time.Hour)
	if err := e.runDigest(ctx, due); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	fail = false
	mu.Unlock()
	if err := e.runDigest(ctx, due.Add(time.Minute)); err != nil || mb.count() != 1 {
		t.Fatalf("a failed digest waits before it is retried: err=%v mails=%d", err, mb.count())
	}
	if err := e.runDigest(ctx, due.Add(digestRetryAfter+time.Minute)); err != nil {
		t.Fatal(err)
	}
	if mb.count() != 2 {
		t.Fatalf("mails = %v", mb.subjects())
	}
	mb.mu.Lock()
	digest := mb.sent[1]
	mb.mu.Unlock()
	if !strings.Contains(digest.Subject, "Hourly warning digest: 2 warnings") || !strings.Contains(digest.TextBody, "disk /srv 91%") || !strings.Contains(digest.TextBody, "== NAS — Ping ==") {
		t.Fatalf("digest = %q\n%s", digest.Subject, digest.TextBody)
	}
	if n := eventTypes(t, st, ids[0])[model.EventAlertSent]; n != 1 {
		t.Fatalf("the digested warning's timeline has %d alert_sent events", n)
	}
	// Sent warnings are gone; the next run has nothing to send.
	if err := e.runDigest(ctx, due.Add(3*time.Hour)); err != nil || mb.count() != 2 {
		t.Fatalf("err=%v mails=%d", err, mb.count())
	}
}

func TestDigestDue(t *testing.T) {
	loc := time.Local
	first := time.Date(2026, 10, 23, 9, 20, 0, 0, loc)
	cases := []struct {
		mode string
		now  time.Time
		want bool
	}{
		{model.WarningDigestHourly, time.Date(2026, 10, 23, 9, 59, 0, 0, loc), false},
		{model.WarningDigestHourly, time.Date(2026, 10, 23, 10, 0, 0, 0, loc), true},
		{model.WarningDigestDaily, time.Date(2026, 10, 24, 7, 59, 0, 0, loc), false},
		{model.WarningDigestDaily, time.Date(2026, 10, 24, 8, 0, 0, 0, loc), true},
		{model.WarningDigestOff, first, true}, // switched off with warnings left: send them
	}
	for _, c := range cases {
		if got := digestDue(c.mode, first, c.now); got != c.want {
			t.Errorf("digestDue(%s, %s) = %v", c.mode, c.now, got)
		}
	}
	early := time.Date(2026, 10, 23, 6, 0, 0, 0, loc)
	if !digestDue(model.WarningDigestDaily, early, time.Date(2026, 10, 23, 8, 0, 0, 0, loc)) {
		t.Error("a warning before 08:00 belongs in that morning's digest")
	}
}

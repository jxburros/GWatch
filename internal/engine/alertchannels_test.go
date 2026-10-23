package engine

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// pushbox is an ntfy-shaped server that records the title and body of every
// message posted to it.
type pushbox struct {
	mu    sync.Mutex
	msgs  []string
	paths []string
	fail  bool
}

func (p *pushbox) handler(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail {
		w.WriteHeader(500)
		return
	}
	p.msgs = append(p.msgs, r.Header.Get("Title")+"|"+string(b))
	p.paths = append(p.paths, r.URL.Path)
	w.WriteHeader(200)
}

func (p *pushbox) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.msgs)
}

func (p *pushbox) all() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.msgs...)
}

func withChannels(t *testing.T, e *Engine, chs []model.AlertChannel, mutate func(*model.Settings)) {
	t.Helper()
	ctx := context.Background()
	s := e.Settings()
	s.Alerts.Channels = chs
	if mutate != nil {
		mutate(&s)
	}
	if err := e.store.SaveSettings(ctx, s); err != nil {
		t.Fatal(err)
	}
	if err := e.ReloadConfig(ctx); err != nil {
		t.Fatal(err)
	}
}

func setDown(n *fakeNet, id int64, down bool) {
	n.mu.Lock()
	n.down[id] = down
	n.mu.Unlock()
}

// The "also notify" channels go out at exactly the points the alert e-mail
// does: once on down, once on recovery, and not at all while the cooldown,
// a silence or maintenance holds the e-mail back.
func TestAlertChannelsFollowTheEmail(t *testing.T) {
	e, st, net, mb := setup(t)
	box := &pushbox{}
	srv := httptest.NewServer(http.HandlerFunc(box.handler))
	defer srv.Close()
	ctx := context.Background()
	n, err := st.CreateNode(ctx, model.Node{Name: "NAS", Host: "10.0.0.5", Enabled: true, Checks: []model.Check{{Type: model.CheckPing, Name: "Ping", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	withChannels(t, e, []model.AlertChannel{{ID: "ch-1", Name: "Phone", Enabled: true, MinSeverity: model.AlertSeverityWarning,
		Action: model.Action{Type: model.ActionNtfy, Server: srv.URL, Topic: "gwatch", Title: "{{alert.kind}}"}}}, nil)
	cid := n.Checks[0].ID
	e.RunNow(ctx, cid)

	setDown(net, cid, true)
	e.RunNow(ctx, cid)
	e.RunNow(ctx, cid)
	waitFor(t, func() bool { return mb.count() == 1 && box.count() == 1 })
	if got := box.all()[0]; got != "down|NAS is down: connection refused" {
		t.Fatalf("down message = %q", got)
	}
	e.RunNow(ctx, cid) // still down: nothing new
	setDown(net, cid, false)
	e.RunNow(ctx, cid)
	waitFor(t, func() bool { return mb.count() == 2 && box.count() == 2 })
	if got := box.all()[1]; got != "recovered|NAS is up: ok" {
		t.Fatalf("recovery message = %q", got)
	}

	// A second outage inside the cooldown: the e-mail is held back, and so
	// is the channel.
	setDown(net, cid, true)
	e.RunNow(ctx, cid)
	e.RunNow(ctx, cid)
	time.Sleep(100 * time.Millisecond)
	if mb.count() != 2 || box.count() != 2 {
		t.Fatalf("cooldown should hold back both: mail=%d channel=%d", mb.count(), box.count())
	}
	types := eventTypes(t, st, cid)
	if types[model.EventAlertSent] != 4 { // two e-mails, two channel sends
		t.Fatalf("events: %+v", types)
	}
}

func TestAlertChannelSilenceAndSeverityAndGroups(t *testing.T) {
	e, st, net, mb := setup(t)
	warnBox, downBox, otherBox := &pushbox{}, &pushbox{}, &pushbox{}
	srvW := httptest.NewServer(http.HandlerFunc(warnBox.handler))
	defer srvW.Close()
	srvD := httptest.NewServer(http.HandlerFunc(downBox.handler))
	defer srvD.Close()
	srvO := httptest.NewServer(http.HandlerFunc(otherBox.handler))
	defer srvO.Close()
	ctx := context.Background()
	n, err := st.CreateNode(ctx, model.Node{Name: "Web", Host: "web.lan", Group: "Servers", Enabled: true, Checks: []model.Check{{Type: model.CheckPing, Name: "Ping", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	withChannels(t, e, []model.AlertChannel{
		{ID: "w", Name: "Everything", Enabled: true, MinSeverity: model.AlertSeverityWarning, Action: model.Action{Type: model.ActionNtfy, Server: srvW.URL, Topic: "w"}},
		{ID: "d", Name: "Critical only", Enabled: true, MinSeverity: model.AlertSeverityDown, Groups: []string{"servers"}, Action: model.Action{Type: model.ActionNtfy, Server: srvD.URL, Topic: "d"}},
		{ID: "o", Name: "Other group", Enabled: true, Groups: []string{"Cameras"}, Action: model.Action{Type: model.ActionNtfy, Server: srvO.URL, Topic: "o"}},
	}, func(s *model.Settings) { s.Alerts.NotifyWarnings = true; s.Alerts.CooldownMinutes = 0 })
	cid := n.Checks[0].ID
	e.RunNow(ctx, cid)

	// A degraded result is a warning: only the "warning" channel gets it.
	lat := 900.0
	net.mu.Lock()
	net.custom = map[int64]func() model.Result{cid: func() model.Result {
		return model.Result{Timestamp: time.Now(), Success: true, Status: model.StatusDegraded, Message: "slow", Warnings: []string{"latency 900 ms"}, LatencyMS: &lat}
	}}
	net.mu.Unlock()
	e.RunNow(ctx, cid)
	waitFor(t, func() bool { return warnBox.count() == 1 })
	net.mu.Lock()
	net.custom = nil
	net.mu.Unlock()

	// Silenced: nothing goes anywhere.
	if _, err := e.Silence(ctx, cid, time.Hour); err != nil {
		t.Fatal(err)
	}
	setDown(net, cid, true)
	e.RunNow(ctx, cid)
	e.RunNow(ctx, cid)
	time.Sleep(100 * time.Millisecond)
	if downBox.count() != 0 || warnBox.count() != 1 {
		t.Fatalf("silence should hold the channels back: warn=%d down=%d", warnBox.count(), downBox.count())
	}
	mails := mb.count()
	// Unsilenced: the next failing result sends the down alert to both the
	// warning and the down channel, never to the other group's.
	if _, err := e.Silence(ctx, cid, 0); err != nil {
		t.Fatal(err)
	}
	e.RunNow(ctx, cid)
	waitFor(t, func() bool { return downBox.count() == 1 && warnBox.count() == 2 && mb.count() == mails+1 })
	time.Sleep(50 * time.Millisecond)
	if otherBox.count() != 0 {
		t.Fatalf("a channel for another group got %v", otherBox.all())
	}
}

// With no e-mail recipients, the channels alone carry the alert; and when
// every route fails, the alert is re-armed exactly as a failed e-mail is.
func TestAlertChannelsWithoutEmailAndFailure(t *testing.T) {
	e, st, net, mb := setup(t)
	box := &pushbox{fail: true}
	srv := httptest.NewServer(http.HandlerFunc(box.handler))
	defer srv.Close()
	ctx := context.Background()
	n, err := st.CreateNode(ctx, model.Node{Name: "Cam", Host: "cam.lan", Enabled: true, Checks: []model.Check{{Type: model.CheckPing, Name: "Ping", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	withChannels(t, e, []model.AlertChannel{{ID: "x", Name: "Push", Enabled: true, Action: model.Action{Type: model.ActionNtfy, Server: srv.URL, Topic: "x"}}},
		func(s *model.Settings) { s.Alerts.Recipients = []string{} })
	cid := n.Checks[0].ID
	e.RunNow(ctx, cid)
	setDown(net, cid, true)
	e.RunNow(ctx, cid)
	e.RunNow(ctx, cid)
	waitFor(t, func() bool {
		s, _ := e.State(cid)
		return eventTypes(t, st, cid)[model.EventAlertFailed] == 1 && !s.AlertActive
	})
	if mb.count() != 0 {
		t.Fatal("no e-mail without recipients")
	}
	// The channel works again: the next failing result delivers it.
	box.mu.Lock()
	box.fail = false
	box.mu.Unlock()
	e.RunNow(ctx, cid)
	waitFor(t, func() bool { return box.count() == 1 })
	waitFor(t, func() bool { s, _ := e.State(cid); return s.AlertActive })
}

func TestAlertChannelsForIncidentOnlyRecovery(t *testing.T) {
	n := model.Node{Name: "x", Group: "Servers"}
	chs := []model.AlertChannel{
		{Enabled: true, Action: model.Action{Type: model.ActionSlack}},
		{Enabled: true, MinSeverity: model.AlertSeverityDown, Action: model.Action{Type: model.ActionPagerDuty}},
		{Enabled: false, Action: model.Action{Type: model.ActionOpsgenie}},
	}
	if !hasIncidentChannel(chs, n) {
		t.Fatal("the PagerDuty channel should count")
	}
	got := alertChannelsFor(chs, mailTask{kind: "recovered", node: n, channelsOnly: true})
	if len(got) != 1 || got[0].Action.Type != model.ActionPagerDuty {
		t.Fatalf("channels-only recovery went to %+v", got)
	}
	if got := alertChannelsFor(chs, mailTask{kind: "warning", node: n}); len(got) != 1 || got[0].Action.Type != model.ActionSlack {
		t.Fatalf("warning went to %+v", got)
	}
	if hasIncidentChannel(chs[:1], n) {
		t.Fatal("Slack is not an incident channel")
	}
}

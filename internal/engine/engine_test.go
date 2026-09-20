package engine

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/checks"
	"github.com/jxburros/GWatch/internal/logging"
	"github.com/jxburros/GWatch/internal/mailer"
	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
)

type fakeNet struct {
	mu   sync.Mutex
	down map[int64]bool // check id -> failing
	runs map[int64]int
}

func (f *fakeNet) run(ctx context.Context, c model.Check, opts checks.Options) model.Result {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs[c.ID]++
	lat := 10.0
	if f.down[c.ID] {
		return model.Result{Timestamp: time.Now(), Success: false, Status: model.StatusDown, Message: "connection refused", Error: "dial tcp: connection refused", Attempts: 1}
	}
	return model.Result{Timestamp: time.Now(), Success: true, Status: model.StatusUp, Message: "ok", LatencyMS: &lat, Attempts: 1}
}

type mailbox struct {
	mu   sync.Mutex
	sent []mailer.Message
}

func (m *mailbox) send(ctx context.Context, s model.SMTPSettings, msg mailer.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return nil
}

func (m *mailbox) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

func (m *mailbox) subjects() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []string{}
	for _, s := range m.sent {
		out = append(out, s.Subject)
	}
	return out
}

func setup(t *testing.T) (*Engine, *store.Store, *fakeNet, *mailbox) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log, _ := logging.New("", nil)
	settings := model.DefaultSettings()
	settings.Alerts.Enabled = true
	settings.Alerts.Recipients = []string{"me@example.com"}
	settings.Alerts.SMTP = model.SMTPSettings{Host: "smtp.example.com", Port: 587, From: "gwatch@example.com", Security: "starttls"}
	settings.Alerts.FailureThreshold = 2
	settings.Alerts.CooldownMinutes = 60
	if err := st.SaveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	net := &fakeNet{down: map[int64]bool{}, runs: map[int64]int{}}
	mb := &mailbox{}
	e := New(st, log, Options{Version: "test", Run: net.run, Send: mb.send})
	return e, st, net, mb
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func eventTypes(t *testing.T, st *store.Store, checkID int64) map[model.EventType]int {
	t.Helper()
	evs, err := st.ListEvents(context.Background(), store.EventFilter{Limit: 500, CheckID: &checkID})
	if err != nil {
		t.Fatal(err)
	}
	out := map[model.EventType]int{}
	for _, e := range evs {
		out[e.Type]++
	}
	return out
}

func TestDownAlertRecoveryAndCooldown(t *testing.T) {
	e, st, net, mb := setup(t)
	ctx := context.Background()
	n, err := st.CreateNode(ctx, model.Node{Name: "Site", Host: "example.com", Enabled: true, Checks: []model.Check{{Type: model.CheckHTTP, Name: "HTTP", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	cid := n.Checks[0].ID

	r, err := e.RunNow(ctx, cid)
	if err != nil || !r.Success {
		t.Fatalf("first run: %v %+v", err, r)
	}
	if s, _ := e.State(cid); s.Status != model.StatusUp {
		t.Fatalf("expected up, got %s", s.Status)
	}
	net.mu.Lock()
	net.down[cid] = true
	net.mu.Unlock()

	// first failure: below threshold, no alert
	e.RunNow(ctx, cid)
	if s, _ := e.State(cid); s.Status != model.StatusUp || s.ConsecutiveFailures != 1 {
		t.Fatalf("after 1 failure: %+v", s)
	}
	if mb.count() != 0 {
		t.Fatal("no mail expected yet")
	}
	// second failure: down + email
	e.RunNow(ctx, cid)
	s, _ := e.State(cid)
	if s.Status != model.StatusDown || !s.AlertActive {
		t.Fatalf("after 2 failures: %+v", s)
	}
	waitFor(t, func() bool { return mb.count() == 1 })
	// third failure: still down, no additional email
	e.RunNow(ctx, cid)
	time.Sleep(50 * time.Millisecond)
	if mb.count() != 1 {
		t.Fatalf("expected exactly 1 mail, got %d", mb.count())
	}
	// recovery
	net.mu.Lock()
	net.down[cid] = false
	net.mu.Unlock()
	e.RunNow(ctx, cid)
	s, _ = e.State(cid)
	if s.Status != model.StatusUp || s.AlertActive {
		t.Fatalf("after recovery: %+v", s)
	}
	waitFor(t, func() bool { return mb.count() == 2 })
	subs := mb.subjects()
	if subs[0] == "" || subs[1] == "" || subs[0] == subs[1] {
		t.Fatalf("subjects: %v", subs)
	}
	// second outage within cooldown -> suppressed
	net.mu.Lock()
	net.down[cid] = true
	net.mu.Unlock()
	e.RunNow(ctx, cid)
	e.RunNow(ctx, cid)
	s, _ = e.State(cid)
	if s.Status != model.StatusDown || s.AlertActive || !s.AlertSuppressed || s.SuppressReason != "cooldown" {
		t.Fatalf("expected cooldown suppression: %+v", s)
	}
	time.Sleep(50 * time.Millisecond)
	if mb.count() != 2 {
		t.Fatalf("cooldown should suppress mail, got %d", mb.count())
	}
	types := eventTypes(t, st, cid)
	if types[model.EventDown] != 2 || types[model.EventRecovered] != 1 || types[model.EventAlertSent] != 2 || types[model.EventAlertSuppressed] != 1 {
		t.Fatalf("events: %+v", types)
	}
	results, _ := st.RecentResults(ctx, cid, 100)
	if len(results) != 7 {
		t.Fatalf("expected 7 recorded results, got %d", len(results))
	}
}

func TestDependencySuppression(t *testing.T) {
	e, st, net, mb := setup(t)
	ctx := context.Background()
	gw, err := st.CreateNode(ctx, model.Node{Name: "Gateway", Host: "192.168.1.1", Enabled: true, Checks: []model.Check{{Type: model.CheckPing, Name: "Ping", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5}}})
	if err != nil {
		t.Fatal(err)
	}
	plex, err := st.CreateNode(ctx, model.Node{Name: "Plex", Host: "192.168.1.10", Enabled: true, DependsOnNode: &gw.ID, Checks: []model.Check{{Type: model.CheckTCP, Name: "TCP 32400", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5, Config: model.CheckConfig{Port: 32400}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	gwc, plexc := gw.Checks[0].ID, plex.Checks[0].ID
	e.RunNow(ctx, gwc)
	e.RunNow(ctx, plexc)

	// Everything fails (gateway is down). Plex is evaluated first: the engine
	// must probe the gateway and attribute the failure to it.
	net.mu.Lock()
	net.down[gwc] = true
	net.down[plexc] = true
	net.mu.Unlock()
	e.RunNow(ctx, plexc)
	e.RunNow(ctx, plexc)
	time.Sleep(100 * time.Millisecond)
	ps, _ := e.State(plexc)
	if ps.Status != model.StatusDown || ps.AlertActive || ps.SuppressReason != "dependency" {
		t.Fatalf("plex should be suppressed after the probe found the gateway failing: %+v", ps)
	}
	if mb.count() != 0 {
		t.Fatalf("no mail expected before the gateway reaches its threshold: %v", mb.subjects())
	}
	// The gateway's next scheduled run reaches its threshold: one email, for the gateway.
	e.RunNow(ctx, gwc)
	waitFor(t, func() bool { return mb.count() >= 1 })
	time.Sleep(100 * time.Millisecond)
	gs, _ := e.State(gwc)
	ps, _ = e.State(plexc)
	if gs.Status != model.StatusDown || !gs.AlertActive {
		t.Fatalf("gateway should be down and alerted: %+v", gs)
	}
	if ps.Status != model.StatusDown || ps.AlertActive || ps.SuppressReason != "dependency" || ps.AffectedByCheckID == nil || *ps.AffectedByCheckID != gwc || ps.AffectedByNodeName != "Gateway" {
		t.Fatalf("plex should be suppressed by dependency: %+v", ps)
	}
	if mb.count() != 1 {
		t.Fatalf("expected exactly one (gateway) email, got %d: %v", mb.count(), mb.subjects())
	}
	types := eventTypes(t, st, plexc)
	if types[model.EventAffectedByParent] != 1 || types[model.EventAlertSuppressed] != 1 {
		t.Fatalf("plex events: %+v", types)
	}
	ov, err := e.Overview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var plexView *NodeView
	for i := range ov.Nodes {
		if ov.Nodes[i].Node.ID == plex.ID {
			plexView = &ov.Nodes[i]
		}
	}
	if plexView == nil || plexView.AffectedBy != "Gateway" || ov.Summary.Down != 2 {
		t.Fatalf("overview: %+v", ov.Summary)
	}

	// Gateway recovers but Plex stays down: Plex now deserves its own alert.
	net.mu.Lock()
	net.down[gwc] = false
	net.mu.Unlock()
	e.RunNow(ctx, gwc)
	waitFor(t, func() bool { return mb.count() == 2 }) // gateway recovery mail
	e.RunNow(ctx, plexc)
	waitFor(t, func() bool { return mb.count() == 3 }) // plex down mail
	ps, _ = e.State(plexc)
	if !ps.AlertActive || ps.AffectedByCheckID != nil {
		t.Fatalf("plex should now be alerted on its own: %+v", ps)
	}
}

func TestMaintenanceSuppressesAndRecordsEvents(t *testing.T) {
	e, st, net, mb := setup(t)
	ctx := context.Background()
	n, err := st.CreateNode(ctx, model.Node{Name: "NAS", Host: "nas", Group: "Servers", Enabled: true, Checks: []model.Check{{Type: model.CheckPing, Name: "Ping", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveMaintenance(ctx, model.MaintenanceWindow{Name: "Weekly reboot", Group: "Servers", Enabled: true, StartAt: time.Now().Add(-time.Minute), EndAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	cid := n.Checks[0].ID
	net.mu.Lock()
	net.down[cid] = true
	net.mu.Unlock()
	e.RunNow(ctx, cid)
	e.RunNow(ctx, cid)
	s, _ := e.State(cid)
	if s.Status != model.StatusDown || s.SuppressReason != "maintenance" {
		t.Fatalf("expected maintenance suppression: %+v", s)
	}
	time.Sleep(50 * time.Millisecond)
	if mb.count() != 0 {
		t.Fatal("no mail during maintenance")
	}
	status, inMW := e.NodeStatus(n)
	if status != model.StatusMaintenance || !inMW {
		t.Fatalf("node status: %s %v", status, inMW)
	}
	evs, _ := st.ListEvents(ctx, store.EventFilter{Limit: 100, Types: []model.EventType{model.EventMaintenanceBegan}})
	if len(evs) != 1 {
		t.Fatalf("expected maintenance_began event, got %d", len(evs))
	}
	// Silence works too and is reflected in state
	if _, err := e.Silence(ctx, cid, 0); err != nil {
		t.Fatal(err)
	}
	h := e.Health(ctx)
	if !h.SchedulerRunning || h.ChecksEnabled != 1 || h.LastCheckAt == nil {
		t.Fatalf("health: %+v", h)
	}
}

func TestSchedulerRunsDueChecks(t *testing.T) {
	e, st, net, _ := setup(t)
	ctx := context.Background()
	n, err := st.CreateNode(ctx, model.Node{Name: "Site", Host: "example.com", Enabled: true, Checks: []model.Check{{Type: model.CheckHTTP, Name: "HTTP", Enabled: true, IntervalSeconds: 10, TimeoutSeconds: 5}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	cid := n.Checks[0].ID
	waitFor(t, func() bool {
		net.mu.Lock()
		defer net.mu.Unlock()
		return net.runs[cid] >= 1
	})
	s, _ := e.State(cid)
	if s.NextRunAt == nil || s.NextRunAt.Sub(*s.LastRunAt) < 9*time.Second {
		t.Fatalf("next run not scheduled: %+v", s)
	}
	if err := e.RunRetention(ctx, true); err != nil {
		t.Fatal(err)
	}
	rs := e.RetentionStatus(ctx)
	if rs.RawRows < 1 || rs.RollupRows5m < 1 || len(rs.Plan) == 0 {
		t.Fatalf("retention status: %+v", rs)
	}
}

// A maintenance window still names one group. A node that has that group among
// its own is covered by it, whether the group is its first or its last.
func TestMaintenanceMatchesAnyOfANodesGroups(t *testing.T) {
	e, st, net, _ := setup(t)
	ctx := context.Background()
	n, err := st.CreateNode(ctx, model.Node{Name: "NAS", Host: "nas", Groups: []string{"Storage", "Servers"}, Enabled: true,
		Checks: []model.Check{{Type: model.CheckPing, Name: "Ping", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5}}})
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateNode(ctx, model.Node{Name: "Printer", Host: "printer", Groups: []string{"Office"}, Enabled: true,
		Checks: []model.Check{{Type: model.CheckPing, Name: "Ping", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveMaintenance(ctx, model.MaintenanceWindow{Name: "Weekly reboot", Group: "Servers", Enabled: true,
		StartAt: time.Now().Add(-time.Minute), EndAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()

	cid := n.Checks[0].ID
	net.mu.Lock()
	net.down[cid] = true
	net.down[other.Checks[0].ID] = true
	net.mu.Unlock()
	// Two runs: one failure is not yet a failure, by the default threshold.
	e.RunNow(ctx, cid)
	e.RunNow(ctx, cid)
	s, _ := e.State(cid)
	if s.SuppressReason != "maintenance" {
		t.Fatalf("a window on the node's second group should still cover it: %+v", s)
	}
	e.RunNow(ctx, other.Checks[0].ID)
	e.RunNow(ctx, other.Checks[0].ID)
	if s, _ := e.State(other.Checks[0].ID); s.SuppressReason == "maintenance" {
		t.Fatalf("a node outside the window's group should not be covered: %+v", s)
	}

	// The overview counts the node in each of its groups, so the two group
	// tallies add up to more than the number of nodes.
	ov, err := e.Overview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, g := range ov.Groups {
		seen[g.Name] = g.Total
	}
	if seen["Storage"] != 1 || seen["Servers"] != 1 || seen["Office"] != 1 {
		t.Fatalf("each group should count the nodes in it: %v", seen)
	}
	if ov.Summary.Total != 2 {
		t.Fatalf("the node tally itself should not be inflated: %d", ov.Summary.Total)
	}
}

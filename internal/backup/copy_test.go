package backup

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/auth"
	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
	"github.com/jxburros/GWatch/internal/store/storetest"
)

// seedFull fills a store with one of everything a full backup carries, and
// returns the counts a copy or restore should reproduce.
func seedFull(t *testing.T, ctx context.Context, st *store.Store) (users, keys, agents, boards, samples, results, events int) {
	t.Helper()
	gw, err := st.CreateNode(ctx, model.Node{Name: "Gateway", Host: "192.168.1.1", Group: "Home Network", Enabled: true,
		Checks: []model.Check{{Type: model.CheckPing, Name: "Ping", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5}}})
	if err != nil {
		t.Fatal(err)
	}
	nas, err := st.CreateNode(ctx, model.Node{Name: "NAS", Host: "nas.lan", Group: "Storage", Enabled: true, DependsOnNode: &gw.ID,
		Checks: []model.Check{{Type: model.CheckSystem, Name: "Hardware", Enabled: true, IntervalSeconds: 60, Config: model.CheckConfig{MetricsToken: "t0ken"}}}})
	if err != nil {
		t.Fatal(err)
	}
	lat := 3.5
	for i := 0; i < 25; i++ {
		if _, err := st.InsertResult(ctx, model.Result{CheckID: gw.Checks[0].ID, Timestamp: time.Now().Add(-time.Duration(i) * time.Minute), Success: i%5 != 0, Status: model.StatusUp, LatencyMS: &lat, Attempts: 1}); err != nil {
			t.Fatal(err)
		}
		results++
	}
	if _, err := st.RollupFromRaw(ctx, time.Now().Add(-time.Hour), time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"down", "up", "note"} {
		if _, err := st.InsertEvent(ctx, model.Event{Type: model.EventDown, Title: title, NodeID: &gw.ID, Actor: "test"}); err != nil {
			t.Fatal(err)
		}
		events++
	}
	if _, err := st.SaveDashboard(ctx, model.Dashboard{Name: "Overview", Widgets: []model.Widget{{Type: "summary", Width: 4, Height: 1}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveTrigger(ctx, model.Trigger{NodeID: gw.ID, Name: "Reboot", On: []string{"down"}, Action: model.Action{Type: model.ActionScript, Command: "true"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveEndpoint(ctx, model.Endpoint{Name: "Hook", Slug: "hook", Enabled: true, Method: "POST", Action: model.Action{Type: model.ActionScript, Command: "true"}}); err != nil {
		t.Fatal(err)
	}
	gwCheck := gw.Checks[0].ID
	if _, err := st.SaveRule(ctx, model.Rule{Name: "Gateway and NAS down", Enabled: true, Join: model.RuleJoinAll,
		Conditions: []model.RuleCondition{{Kind: model.RuleConditionStatus, CheckID: &gwCheck, Status: model.StatusDown}, {Kind: model.RuleConditionStatus, NodeID: &nas.ID, Status: model.StatusDegraded}},
		Actions:    []model.Action{{Type: model.ActionHTTP, URL: "https://example.com/hook"}}}); err != nil {
		t.Fatal(err)
	}
	b, err := st.SaveWallboard(ctx, model.Wallboard{Name: "Lobby", Panels: []model.WallPanel{{Type: "status_list"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetWallboardShare(ctx, b.ID, true, "wall-token-1"); err != nil {
		t.Fatal(err)
	}
	boards = 1
	hash, _ := auth.HashPassword("pass-w0rd")
	for _, u := range []string{"admin", "Viewer"} {
		if _, err := st.CreateUser(ctx, u, hash, auth.RoleAdmin); err != nil {
			t.Fatal(err)
		}
		users++
	}
	if _, err := st.CreateAPIKey(ctx, "Grafana", "gw_abc", auth.HashToken("gw_abcdef"), auth.ScopeRead, "admin"); err != nil {
		t.Fatal(err)
	}
	keys = 1
	ag, err := st.CreateAgent(ctx, "nas agent", &nas.ID, "gwa_x", auth.HashToken("gwa_xyz"), "admin")
	if err != nil {
		t.Fatal(err)
	}
	agents = 1
	cpu := 42.0
	for i := 0; i < 7; i++ {
		if err := st.SaveHostSample(ctx, model.HostSample{Key: model.AgentHostKey(ag.ID), Timestamp: time.Now().Add(-time.Duration(i) * time.Minute), CPUPct: &cpu}); err != nil {
			t.Fatal(err)
		}
		samples++
	}
	settings, _ := st.LoadSettings(ctx)
	settings.Alerts.Recipients = []string{"me@example.com"}
	settings.Alerts.SMTP.Password = "smtp-secret"
	if err := st.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	return
}

// TestCopyMovesEverything is "gwatch migrate-db" without the command line:
// a SQLite file copied into a fresh store on whatever backend the tests run
// against (so under GWATCH_TEST_DB=postgres it is SQLite → PostgreSQL), and
// every count and identity checked on the other side.
func TestCopyMovesEverything(t *testing.T) {
	ctx := context.Background()
	src, err := store.Open(filepath.Join(t.TempDir(), "gwatch.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	users, keys, agents, boards, samples, results, events := seedFull(t, ctx, src)

	dst := storetest.Open(t)
	if empty, err := dst.IsEmpty(ctx); err != nil || !empty {
		t.Fatalf("fresh target should be empty: %v %v", empty, err)
	}
	stages := map[string]int{}
	sum, err := Copy(ctx, src, dst, func(stage string, n int) { stages[stage] = n })
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	if sum.Nodes != 2 || sum.Checks != 2 || sum.Results != results || sum.Events != events || sum.Rollups == 0 ||
		sum.Users != users || sum.APIKeys != keys || sum.Agents != agents || sum.Wallboards != boards || sum.HostSamples != samples {
		t.Fatalf("summary: %+v", sum)
	}
	if stages["results"] != results || stages["hardware readings"] != samples {
		t.Fatalf("progress was not reported: %v", stages)
	}
	if empty, _ := dst.IsEmpty(ctx); empty {
		t.Fatal("target still reports empty")
	}

	// Identities that other things point at survived.
	srcNodes, _ := src.ListNodes(ctx)
	dstNodes, _ := dst.ListNodes(ctx)
	if len(dstNodes) != 2 || dstNodes[0].ID != srcNodes[0].ID || dstNodes[1].Checks[0].ID != srcNodes[1].Checks[0].ID {
		t.Fatalf("node/check ids changed: %+v vs %+v", srcNodes, dstNodes)
	}
	if dstNodes[1].DependsOnNode == nil || *dstNodes[1].DependsOnNode != srcNodes[0].ID {
		t.Fatalf("dependency lost: %+v", dstNodes[1])
	}
	if dstNodes[1].Checks[0].Config.MetricsToken != "t0ken" {
		t.Fatalf("check secret did not survive: %+v", dstNodes[1].Checks[0].Config)
	}
	if _, hash, err := dst.GetUserByName(ctx, "viewer"); err != nil || auth.VerifyPassword(hash, "pass-w0rd") != nil {
		t.Fatalf("user did not arrive with a working password: %v", err)
	}
	if _, err := dst.LookupAPIKey(ctx, auth.HashToken("gw_abcdef")); err != nil {
		t.Fatalf("API key did not arrive: %v", err)
	}
	ag, err := dst.AgentByTokenHash(ctx, auth.HashToken("gwa_xyz"))
	if err != nil {
		t.Fatalf("agent did not arrive: %v", err)
	}
	if got, err := dst.HostSamples(ctx, model.AgentHostKey(ag.ID), time.Now().Add(-time.Hour), time.Now(), 0); err != nil || len(got) != samples {
		t.Fatalf("readings did not follow their agent's id: %d %v", len(got), err)
	}
	if b, err := dst.WallboardByShareToken(ctx, "wall-token-1"); err != nil || b.Name != "Lobby" {
		t.Fatalf("wallboard share token did not arrive: %v", err)
	}
	s2, _ := dst.LoadSettings(ctx)
	if s2.Alerts.SMTP.Password != "smtp-secret" || len(s2.Alerts.Recipients) != 1 {
		t.Fatalf("settings: %+v", s2.Alerts)
	}
	// A rule's conditions still point at the right check and node, since
	// those kept their ids.
	rules, _ := dst.ListRules(ctx)
	if len(rules) != 1 || len(rules[0].Conditions) != 2 || rules[0].Conditions[0].CheckID == nil || *rules[0].Conditions[0].CheckID != srcNodes[0].Checks[0].ID ||
		rules[0].Conditions[1].NodeID == nil || *rules[0].Conditions[1].NodeID != srcNodes[1].ID || len(rules[0].Actions) != 1 {
		t.Fatalf("rules did not arrive intact: %+v", rules)
	}
	// And the target keeps working afterwards: a new node gets an id after
	// the copied ones rather than colliding with them.
	n, err := dst.CreateNode(ctx, model.Node{Name: "New", Enabled: true, Checks: []model.Check{{Type: model.CheckPing, Name: "Ping", Enabled: true, IntervalSeconds: 60}}})
	if err != nil || n.ID <= srcNodes[1].ID {
		t.Fatalf("create after copy: %v %+v", err, n)
	}

	// ClearEverything leaves the target as empty as it started.
	if err := dst.ClearEverything(ctx); err != nil {
		t.Fatal(err)
	}
	if empty, err := dst.IsEmpty(ctx); err != nil || !empty {
		t.Fatalf("after ClearEverything: empty=%v %v", empty, err)
	}
}

// A format-2 archive carries accounts, keys, agents, wallboards and
// readings, and a restore merges the first three rather than replacing
// them: the account doing the restoring keeps its password.
func TestBackupCarriesAccountsAndAgents(t *testing.T) {
	ctx := context.Background()
	src := storetest.Open(t)
	users, keys, agents, boards, samples, _, _ := seedFull(t, ctx, src)
	dir := t.TempDir()
	info, err := Create(ctx, src, dir, "pw", true, "test")
	if err != nil {
		t.Fatal(err)
	}
	m, err := Inspect(filepath.Join(dir, info.FileName), "pw")
	if err != nil || m.Format != Format || m.Users != users || m.APIKeys != keys || m.Agents != agents || m.Wallboards != boards || m.HostSamples != samples {
		t.Fatalf("manifest: %v %+v", err, m)
	}

	dst := storetest.Open(t)
	// The administrator on the new machine already exists, with another
	// password, and must still be able to sign in afterwards.
	otherHash, _ := auth.HashPassword("other")
	if _, err := dst.CreateUser(ctx, "ADMIN", otherHash, auth.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	sum, err := Restore(ctx, dst, filepath.Join(dir, info.FileName), "pw", true)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if sum.Users != users-1 || sum.APIKeys != keys || sum.Agents != agents || sum.Wallboards != boards || sum.HostSamples != samples {
		t.Fatalf("summary: %+v", sum)
	}
	if _, hash, err := dst.GetUserByName(ctx, "admin"); err != nil || auth.VerifyPassword(hash, "other") != nil {
		t.Fatalf("the existing administrator's password was overwritten: %v", err)
	}
	if _, hash, err := dst.GetUserByName(ctx, "viewer"); err != nil || auth.VerifyPassword(hash, "pass-w0rd") != nil {
		t.Fatalf("the archived account did not arrive: %v", err)
	}
	// Restoring the same archive again changes nothing.
	sum2, err := Restore(ctx, dst, filepath.Join(dir, info.FileName), "pw", false)
	if err != nil || sum2.Users != 0 || sum2.APIKeys != 0 || sum2.Agents != 0 || sum2.Wallboards != boards {
		t.Fatalf("second restore: %v %+v", err, sum2)
	}
	list, _ := dst.ListWallboards(ctx)
	if len(list) != boards {
		t.Fatalf("wallboards duplicated: %d", len(list))
	}
}

package backup

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jxburros/GWatch/internal/auth"
	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
	"github.com/jxburros/GWatch/internal/store/storetest"
)

// #148: the configuration part of a restore is one transaction. A failure at
// any point inside it must leave the database exactly as it was, on whichever
// backend the tests run against, and a failure in the history import that
// follows must be reported as such, with the configuration restored.

// targetStore is a store with a configuration of its own, different from
// archiveOf's in every part a restore touches.
func targetStore(t *testing.T, ctx context.Context) *store.Store {
	t.Helper()
	st := storetest.Open(t)
	n, err := st.CreateNode(ctx, model.Node{Name: "Keep me", Host: "10.9.9.9", Enabled: true,
		Checks: []model.Check{{Type: model.CheckTCP, Name: "SSH", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5, Config: model.CheckConfig{Port: 22}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveTrigger(ctx, model.Trigger{NodeID: n.ID, Name: "Mine", On: []string{"down"}, Action: model.Action{Type: model.ActionHTTP, Method: "POST", URL: "https://example.com/"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUser(ctx, "owner", "$argon2id$z", auth.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	settings := model.DefaultSettings()
	settings.General.InstanceName = "Target"
	if err := st.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	return st
}

// snapshot is everything a restore could change, as JSON, for comparison.
func snapshot(t *testing.T, ctx context.Context, st *store.Store) string {
	t.Helper()
	cfg, err := ExportConfig(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	var results, rollups, events int
	_ = st.AllResults(ctx, func(model.Result) error { results++; return nil })
	_ = st.AllRollups(ctx, func(model.Rollup) error { rollups++; return nil })
	_ = st.AllEvents(ctx, func(model.Event) error { events++; return nil })
	b, err := json.Marshal(struct {
		Cfg                      Config
		Results, Rollups, Events int
	}{cfg, results, rollups, events})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func injectFault(t *testing.T, at string) error {
	t.Helper()
	boom := errors.New("injected failure at " + at)
	restoreFault = func(step string) error {
		if step == at {
			return boom
		}
		return nil
	}
	t.Cleanup(func() { restoreFault = nil })
	return boom
}

func TestRestoreRollsBackAFailurePartWay(t *testing.T) {
	ctx := context.Background()
	src := storetest.Open(t)
	seedFull(t, ctx, src)
	dir := t.TempDir()
	info, err := Create(ctx, src, dir, "pw", true, "test")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, info.FileName)
	// After the nodes (and, with history, the emptied history tables), after
	// the triggers, and at the very end, once the accounts are in.
	for _, step := range []string{"nodes", "triggers", "accounts"} {
		t.Run(step, func(t *testing.T) {
			st := targetStore(t, ctx)
			before := snapshot(t, ctx, st)
			boom := injectFault(t, step)
			sum, err := RestoreWith(ctx, st, path, "pw", RestoreOptions{IncludeHistory: true, RecreateAccounts: true})
			if !errors.Is(err, boom) {
				t.Fatalf("restore: %v, want the injected failure", err)
			}
			var partial *HistoryError
			if errors.As(err, &partial) {
				t.Fatal("a failure in the configuration was reported as a history failure")
			}
			if sum.Nodes != 0 || sum.Users != 0 || sum.Results != 0 {
				t.Fatalf("the summary counts a rolled-back restore: %+v", sum)
			}
			if after := snapshot(t, ctx, st); after != before {
				t.Fatalf("a failed restore changed the database\nbefore: %s\nafter:  %s", before, after)
			}
			// And the store still takes writes: the transaction is gone.
			restoreFault = nil
			if _, err := RestoreWith(ctx, st, path, "pw", RestoreOptions{IncludeHistory: true, RecreateAccounts: true}); err != nil {
				t.Fatalf("the same restore without the fault: %v", err)
			}
			nodes, _ := st.ListNodes(ctx)
			if len(nodes) != 2 {
				t.Fatalf("after a clean restore: %d nodes, want the archive's 2", len(nodes))
			}
		})
	}
}

func TestRestoreReportsAHistoryFailureWithTheConfigurationRestored(t *testing.T) {
	ctx := context.Background()
	src := storetest.Open(t)
	_, _, _, _, results, _ := seedFull(t, ctx, src)
	dir := t.TempDir()
	info, err := Create(ctx, src, dir, "pw", true, "test")
	if err != nil {
		t.Fatal(err)
	}
	st := targetStore(t, ctx)
	boom := injectFault(t, "results")
	sum, err := RestoreWith(ctx, st, filepath.Join(dir, info.FileName), "pw", RestoreOptions{IncludeHistory: true, RecreateAccounts: true})
	var partial *HistoryError
	if !errors.As(err, &partial) || !errors.Is(err, boom) {
		t.Fatalf("restore: %v, want a HistoryError wrapping the injected failure", err)
	}
	if !strings.Contains(err.Error(), "configuration was restored") {
		t.Fatalf("the error should say the configuration was restored: %v", err)
	}
	// The one batch of results went in before the failure; nothing after.
	if sum.Nodes != 2 || sum.Results != results || sum.Rollups != 0 || sum.Events != 0 {
		t.Fatalf("summary: %+v (want 2 nodes, %d results, nothing after)", sum, results)
	}
	nodes, _ := st.ListNodes(ctx)
	if len(nodes) != 2 || (nodes[0].Name != "Gateway" && nodes[1].Name != "Gateway") {
		t.Fatalf("the configuration should be the archive's: %+v", nodes)
	}
	settings, _ := st.LoadSettings(ctx)
	if settings.General.InstanceName == "Target" {
		t.Fatal("the settings should be the archive's")
	}
}

func TestRestoreValidateRefusesBeforeAnythingIsWritten(t *testing.T) {
	ctx := context.Background()
	src := storetest.Open(t)
	seedFull(t, ctx, src)
	dir := t.TempDir()
	info, err := Create(ctx, src, dir, "pw", true, "test")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, info.FileName)
	st := targetStore(t, ctx)
	before := snapshot(t, ctx, st)
	_, err = RestoreWith(ctx, st, path, "pw", RestoreOptions{IncludeHistory: true, Validate: func(c *Config) error {
		return &InvalidError{Problems: []string{"trigger \"Reboot\": the script code is empty", "endpoint \"Hook\": a token is required"}}
	}})
	var invalid *InvalidError
	if !errors.As(err, &invalid) || len(invalid.Problems) != 2 || !strings.Contains(err.Error(), "2 problems") {
		t.Fatalf("restore: %v, want the InvalidError", err)
	}
	if after := snapshot(t, ctx, st); after != before {
		t.Fatal("a refused restore changed the database")
	}

	// What Validate tidies is what is written.
	if _, err := RestoreWith(ctx, st, path, "pw", RestoreOptions{Validate: func(c *Config) error {
		for i := range c.Nodes {
			c.Nodes[i].Name = strings.ToUpper(c.Nodes[i].Name)
		}
		return nil
	}}); err != nil {
		t.Fatal(err)
	}
	nodes, _ := st.ListNodes(ctx)
	for _, n := range nodes {
		if n.Name != strings.ToUpper(n.Name) {
			t.Fatalf("Validate's changes were not restored: %q", n.Name)
		}
	}
}

func TestPlanListsWhatMattersForSecurity(t *testing.T) {
	ctx := context.Background()
	src := storetest.Open(t)
	seedFull(t, ctx, src)
	if _, err := src.SaveEndpoint(ctx, model.Endpoint{Name: "Open door", Slug: "open-door", Enabled: true, Method: "POST", AllowNoToken: true,
		Action: model.Action{Type: model.ActionGit, Repo: "/srv/site", GitArgs: "pull --ff-only"}}); err != nil {
		t.Fatal(err)
	}
	settings, _ := src.LoadSettings(ctx)
	settings.General.RemoteAccess = true
	settings.General.AccessPassword = "letmein"
	settings.General.RequireLoginLocally = true
	if err := src.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	info, err := Create(ctx, src, dir, "pw", true, "test")
	if err != nil {
		t.Fatal(err)
	}
	m, cfg, err := Read(filepath.Join(dir, info.FileName), "pw")
	if err != nil {
		t.Fatal(err)
	}

	// The target has one of the archive's two accounts, and none of its keys
	// or agents.
	st := storetest.Open(t)
	if _, err := st.CreateUser(ctx, "admin", "$argon2id$x", auth.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, ctx, st)
	runsCode := func(c model.Check) bool { return c.Type == model.CheckSystem }
	p, err := Plan(ctx, st, m, cfg, runsCode)
	if err != nil {
		t.Fatal(err)
	}
	if after := snapshot(t, ctx, st); after != before {
		t.Fatal("Plan wrote to the database")
	}
	if p.Counts.Nodes != 2 || p.Counts.Checks != 2 || p.Counts.Triggers != 1 || p.Counts.Endpoints != 2 || p.Counts.Users != 2 || p.Counts.APIKeys != 1 || p.Counts.Agents != 1 || !p.Counts.History || p.Counts.Results == 0 {
		t.Fatalf("counts: %+v", p.Counts)
	}
	if len(p.Accounts) != 1 || p.Accounts[0].Username != "Viewer" || !p.Accounts[0].Admin || p.AccountsRecreatedAnyway {
		t.Fatalf("accounts: %+v (anyway %v), want Viewer as a missing administrator", p.Accounts, p.AccountsRecreatedAnyway)
	}
	if len(p.Agents) != 1 || p.Agents[0] != "nas agent" {
		t.Fatalf("agents: %v", p.Agents)
	}
	if len(p.APIKeys) != 1 || p.APIKeys[0].Name != "Grafana" || p.APIKeys[0].Scope != "read" {
		t.Fatalf("API keys: %+v", p.APIKeys)
	}
	kinds := map[string]string{}
	for _, c := range p.Commands {
		kinds[c.Kind+" "+c.Name] = c.Detail
	}
	if kinds["trigger Gateway › Reboot"] == "" || kinds["endpoint Hook (/hook/hook)"] == "" || kinds["endpoint Open door (/hook/open-door)"] != "git pull --ff-only" || kinds["check NAS › Hardware"] == "" {
		t.Fatalf("commands: %+v", p.Commands)
	}
	if len(p.TokenlessEndpoints) != 1 || p.TokenlessEndpoints[0] != "open-door" {
		t.Fatalf("tokenless endpoints: %v", p.TokenlessEndpoints)
	}
	joined := strings.Join(p.AccessChanges, " | ")
	for _, want := range []string{"LAN access is turned on", "access password for other devices is set", "Signing in on this computer is turned on"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("access changes %q lack %q", joined, want)
		}
	}
	if len(p.Problems) != 0 {
		t.Fatalf("Plan does not validate; problems: %v", p.Problems)
	}

	// On a server with no accounts at all, they would be re-created anyway.
	empty := storetest.Open(t)
	p, err = Plan(ctx, empty, m, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !p.AccountsRecreatedAnyway || len(p.Accounts) != 2 {
		t.Fatalf("on an empty server: %+v (anyway %v)", p.Accounts, p.AccountsRecreatedAnyway)
	}
}

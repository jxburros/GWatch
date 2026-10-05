package store

import (
	"context"
	"github.com/jxburros/GWatch/internal/model"
	"strings"
	"testing"
	"time"
)

func TestAllCheckAndActionCredentialsSealed(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	n, err := s.CreateNode(ctx, model.Node{Name: "secret test", Enabled: true, Checks: []model.Check{{Name: "HTTP", Type: model.CheckHTTP, Enabled: true, Config: model.CheckConfig{Body: "BODYSECRET", Headers: map[string]string{"Authorization": "HEADERSECRET"}, Env: map[string]string{"TOKEN": "ENVSECRET"}, SNMPCommunity: "COMMUNITYSECRET"}}}})
	if err != nil {
		t.Fatal(err)
	}
	var raw string
	if err = s.queryRow(ctx, "SELECT config FROM checks WHERE id=?", n.Checks[0].ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"BODYSECRET", "HEADERSECRET", "ENVSECRET", "COMMUNITYSECRET"} {
		if strings.Contains(raw, secret) {
			t.Fatalf("plaintext check secret: %s", secret)
		}
	}
	got, err := s.GetCheck(ctx, n.Checks[0].ID)
	if err != nil || got.Config.Headers["Authorization"] != "HEADERSECRET" || got.Config.Body != "BODYSECRET" {
		t.Fatalf("decryption: %+v %v", got.Config, err)
	}
	a := model.Action{Type: model.ActionHTTP, URL: "https://secret.example/HOOKSECRET", Token: "ACTIONSECRET", Headers: map[string]string{"Authorization": "ACTIONHEADERSECRET"}}
	tr, err := s.SaveTrigger(ctx, model.Trigger{NodeID: n.ID, Name: "trigger", Action: a})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.queryRow(ctx, "SELECT action FROM triggers WHERE id=?", tr.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "SECRET") {
		t.Fatal("plaintext action secret")
	}
	e, err := s.SaveEndpoint(ctx, model.Endpoint{Name: "endpoint", Slug: "secret-test", Token: "ENDPOINTSECRET", Action: a})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.queryRow(ctx, "SELECT token FROM endpoints WHERE id=?", e.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "ENDPOINTSECRET") {
		t.Fatal("plaintext endpoint token")
	}
	r, err := s.SaveRule(ctx, model.Rule{Name: "rule", Join: "all", Actions: []model.Action{a}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.queryRow(ctx, "SELECT actions FROM rules WHERE id=?", r.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "SECRET") {
		t.Fatal("plaintext rule secret")
	}
	// Legacy rows are sealed on upgrade without changing what runners receive.
	if _, err = s.exec(ctx, "UPDATE endpoints SET token=? WHERE id=?", "LEGACYSECRET", e.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.migrateActionSecrets(ctx); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.GetEndpoint(ctx, e.ID)
	if err != nil || loaded.Token != "LEGACYSECRET" || loaded.Action.Token != "ACTIONSECRET" {
		t.Fatalf("migration roundtrip: %v", err)
	}
}

func TestSQLLexingAndPairingRetention(t *testing.T) {
	q := `SELECT "col?", '?', $$?$$, $tag$?$tag$, ? -- ?
/* ? */ WHERE x=?`
	want := `SELECT "col?", '?', $$?$$, $tag$?$tag$, $1 -- ?
/* ? */ WHERE x=$2`
	if got := numberPlaceholders(q); got != want {
		t.Fatalf("got %s", got)
	}
	if got := splitStatements("-- comment; still comment\nCREATE TABLE x(v TEXT DEFAULT ';--'); /* ; */ SELECT 1;"); len(got) != 2 {
		t.Fatalf("statements: %#v", got)
	}
	s := openTest(t)
	ctx := context.Background()
	now := time.Now()
	for _, age := range []time.Duration{-time.Hour, time.Hour} {
		_, err := s.exec(ctx, "INSERT INTO agent_pairings(name,code_hash,created_at,expires_at) VALUES(?,?,?,?)", age.String(), age.String(), fmtTime(now), fmtTime(now.Add(age)))
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PruneExpiredPairings(now); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.queryRow(ctx, "SELECT COUNT(*) FROM agent_pairings").Scan(&n); err != nil || n != 1 {
		t.Fatalf("retained %d %v", n, err)
	}
}

func TestIncidentGroupsRecoveryAndAttribution(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	n, err := s.CreateNode(ctx, model.Node{Name: "router", Enabled: true, Checks: []model.Check{{Name: "ping", Type: model.CheckPing, Enabled: true}, {Name: "http", Type: model.CheckHTTP, Enabled: true}}})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour)
	record := func(c model.Check, status model.Status) {
		t.Helper()
		at = at.Add(time.Minute)
		_, err := s.RecordResult(ctx, model.Result{CheckID: c.ID, Timestamp: at, Status: status, Success: status == model.StatusUp}, model.CheckState{CheckID: c.ID, Status: status})
		if err != nil {
			t.Fatal(err)
		}
	}
	record(n.Checks[0], model.StatusDown)
	record(n.Checks[1], model.StatusDown)
	list, err := s.ListIncidents(ctx, "active")
	if err != nil || len(list) != 1 || len(list[0].CheckIDs) != 2 {
		t.Fatalf("grouping: %+v %v", list, err)
	}
	id := list[0].ID
	v, err := s.UpdateIncident(ctx, id, "acknowledge", "alex", "Investigating")
	if err != nil || v.AcknowledgedBy != "alex" || len(v.Notes) != 1 {
		t.Fatalf("ack: %+v %v", v, err)
	}
	if yes, err := s.NodeIncidentAcknowledged(ctx, n.ID); !yes || err != nil {
		t.Fatal("ack not visible")
	}
	record(n.Checks[0], model.StatusUp)
	list, _ = s.ListIncidents(ctx, "active")
	if len(list) != 1 {
		t.Fatal("closed before all checks recovered")
	}
	record(n.Checks[1], model.StatusUp)
	v, err = s.GetIncident(ctx, id)
	if err != nil || v.State != "resolved" || v.ResolvedBy != "recovery" || v.TimeToResolveSeconds == nil {
		t.Fatalf("recovery: %+v %v", v, err)
	}
	record(n.Checks[0], model.StatusDown)
	list, _ = s.ListIncidents(ctx, "active")
	if len(list) != 1 || list[0].ID == id {
		t.Fatal("new outage did not open a new incident")
	}
}

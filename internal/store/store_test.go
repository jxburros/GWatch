package store

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/secrets"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func f(v float64) *float64 { return &v }

func TestNodeCRUDAndChecks(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	n, err := s.CreateNode(ctx, model.Node{Name: "Router", Host: "192.168.1.1", Group: "Home Network", Tags: []string{"core"}, Enabled: true,
		Checks: []model.Check{{Type: model.CheckPing, Name: "Ping", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5}}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if n.ID == 0 || len(n.Checks) != 1 || n.Checks[0].ID == 0 {
		t.Fatalf("ids not assigned: %+v", n)
	}
	got, err := s.GetNode(ctx, n.ID)
	if err != nil || got.Name != "Router" || len(got.Checks) != 1 {
		t.Fatalf("get: %v %+v", err, got)
	}
	st, err := s.GetState(ctx, n.Checks[0].ID)
	if err != nil || st.Status != model.StatusUnknown {
		t.Fatalf("state: %v %+v", err, st)
	}
	// update: modify existing check, add one, expect none deleted
	got.Checks[0].Name = "Ping (renamed)"
	got.Checks = append(got.Checks, model.Check{Type: model.CheckTCP, Name: "TCP 80", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5, Config: model.CheckConfig{Port: 80}})
	upd, deleted, err := s.UpdateNode(ctx, got)
	if err != nil || len(deleted) != 0 || len(upd.Checks) != 2 || upd.Checks[0].Name != "Ping (renamed)" {
		t.Fatalf("update: %v deleted=%v %+v", err, deleted, upd)
	}
	// remove first check
	upd.Checks = upd.Checks[1:]
	upd2, deleted, err := s.UpdateNode(ctx, upd)
	if err != nil || len(deleted) != 1 || len(upd2.Checks) != 1 || upd2.Checks[0].Type != model.CheckTCP {
		t.Fatalf("update2: %v deleted=%v %+v", err, deleted, upd2)
	}
	groups, tags, err := s.GroupCounts(ctx)
	if err != nil || groups["Home Network"] != 1 || tags["core"] != 1 {
		t.Fatalf("groups: %v %v %v", err, groups, tags)
	}
	if err := s.DeleteNode(ctx, n.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetNode(ctx, n.ID); err != ErrNotFound {
		t.Fatalf("expected not found, got %v", err)
	}
	checks, _ := s.ListChecks(ctx)
	if len(checks) != 0 {
		t.Fatalf("checks should cascade: %+v", checks)
	}
}

func TestResultsRollupsHistory(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	n, err := s.CreateNode(ctx, model.Node{Name: "Site", Host: "example.com", Enabled: true, Checks: []model.Check{{Type: model.CheckHTTP, Name: "HTTP", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5}}})
	if err != nil {
		t.Fatal(err)
	}
	cid := n.Checks[0].ID
	now := time.Now().Truncate(time.Minute)
	// 3 hours of results every minute, one failure per hour
	for i := 0; i < 180; i++ {
		ts := now.Add(-time.Duration(180-i) * time.Minute)
		r := model.Result{CheckID: cid, Timestamp: ts, Success: i%60 != 5, Status: model.StatusUp, LatencyMS: f(float64(100 + i%10)), Attempts: 1}
		if !r.Success {
			r.Status = model.StatusDown
			r.LatencyMS = nil
		}
		if _, err := s.InsertResult(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.RollupFromRaw(ctx, now.Add(-4*time.Hour), now); err != nil {
		t.Fatalf("rollup raw: %v", err)
	}
	if _, err := s.RollupUp(ctx, Bucket5m, Bucket1h, now.Add(-4*time.Hour), now); err != nil {
		t.Fatalf("rollup 1h: %v", err)
	}
	if _, err := s.RollupUp(ctx, Bucket1h, Bucket1d, now.Add(-4*time.Hour), now); err != nil {
		t.Fatalf("rollup 1d: %v", err)
	}
	raw, r5, r1h, r1d, _, err := s.Counts(ctx)
	if err != nil || raw != 180 || r5 < 36 || r5 > 37 || r1h < 3 || r1h > 4 || r1d < 1 || r1d > 2 {
		t.Fatalf("counts: %v raw=%d 5m=%d 1h=%d 1d=%d", err, raw, r5, r1h, r1d)
	}
	rs, _ := s.RollupsBetween(ctx, cid, Bucket1h, now.Add(-4*time.Hour), now)
	var total, fails int
	for _, r := range rs {
		total += r.Count
		fails += r.FailCount
		if r.Count > 0 && r.AvgMS == nil && r.SuccessCount > 0 {
			t.Fatalf("avg missing: %+v", r)
		}
	}
	if total != 180 || fails != 3 {
		t.Fatalf("hourly totals: %d %d", total, fails)
	}
	rng, _ := ParseRange("24h")
	h, err := s.History(ctx, n.Checks[0], n.Name, rng, now)
	if err != nil || h.Source != "raw" || len(h.Points) != 180 || h.Summary.Failures != 3 || h.Summary.AvgMS == nil {
		t.Fatalf("history raw: %v %s %d %+v", err, h.Source, len(h.Points), h.Summary)
	}
	rng7, _ := ParseRange("7d")
	h7, err := s.History(ctx, n.Checks[0], n.Name, rng7, now)
	if err != nil || h7.Source != "5m" || len(h7.Points) < 36 {
		t.Fatalf("history 5m: %v %s %d", err, h7.Source, len(h7.Points))
	}
	// retention: delete raw older than 1h, then 24h history should fall back to 5m rollups
	if _, err := s.DeleteResultsBefore(ctx, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	h24, err := s.History(ctx, n.Checks[0], n.Name, rng, now)
	if err != nil || h24.Source != "5m" {
		t.Fatalf("history fallback: %v %s %d", err, h24.Source, len(h24.Points))
	}
	last, _ := s.LastResults(ctx)
	if _, ok := last[cid]; !ok {
		t.Fatal("last result missing")
	}
}

// TestHistoryIncludesAResultFromThisMillisecond pins the upper bound of a raw
// history window. Result timestamps are stored truncated to the millisecond,
// so a result recorded in the same millisecond the chart is drawn in lands
// exactly on "now" — running a check by hand and looking at its history is
// precisely that case, and it used to come back empty.
func TestHistoryIncludesAResultFromThisMillisecond(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	n, err := s.CreateNode(ctx, model.Node{Name: "Site", Host: "example.com", Enabled: true, Checks: []model.Check{{Type: model.CheckHTTP, Name: "HTTP", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5}}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err := s.InsertResult(ctx, model.Result{CheckID: n.Checks[0].ID, Timestamp: now, Success: true, Status: model.StatusUp, LatencyMS: f(12), Attempts: 1}); err != nil {
		t.Fatal(err)
	}
	rng, _ := ParseRange("24h")
	// Truncated so the stored timestamp and the window's end are the same
	// millisecond however long the lines above took.
	h, err := s.History(ctx, n.Checks[0], n.Name, rng, now.Truncate(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Points) != 1 {
		t.Fatalf("a result from this millisecond should be in the window, got %d points", len(h.Points))
	}
}

func TestEventsSettingsDashboardsMaintenance(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := s.InsertEvent(ctx, model.Event{Type: model.EventDown, Title: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	evs, err := s.ListEvents(ctx, EventFilter{Limit: 3})
	if err != nil || len(evs) != 3 || evs[0].ID != 5 {
		t.Fatalf("events: %v %+v", err, evs)
	}
	evs, _ = s.ListEvents(ctx, EventFilter{Limit: 3, BeforeID: 3})
	if len(evs) != 2 {
		t.Fatalf("before: %+v", evs)
	}
	st, err := s.LoadSettings(ctx)
	if err != nil || st.Retention.RawDays != 30 {
		t.Fatalf("settings default: %v %+v", err, st)
	}
	st.Alerts.Recipients = []string{"a@b.c"}
	if err := s.SaveSettings(ctx, st); err != nil {
		t.Fatal(err)
	}
	st2, _ := s.LoadSettings(ctx)
	if len(st2.Alerts.Recipients) != 1 {
		t.Fatalf("settings roundtrip: %+v", st2)
	}
	d, err := s.SaveDashboard(ctx, model.Dashboard{Name: "Overview", Widgets: []model.Widget{{Type: "summary", Width: 4, Height: 1}}})
	if err != nil || d.ID == 0 || d.Widgets[0].ID == "" {
		t.Fatalf("dashboard: %v %+v", err, d)
	}
	ds, _ := s.ListDashboards(ctx)
	if len(ds) != 1 {
		t.Fatalf("dashboards: %+v", ds)
	}
	m, err := s.SaveMaintenance(ctx, model.MaintenanceWindow{Name: "Reboot", Enabled: true, StartAt: time.Now().Add(-time.Hour), EndAt: time.Now().Add(time.Hour)})
	if err != nil || m.ID == 0 {
		t.Fatalf("maintenance: %v", err)
	}
	ms, _ := s.ListMaintenance(ctx)
	if len(ms) != 1 || !ms[0].Active(time.Now()) {
		t.Fatalf("maintenance active: %+v", ms)
	}
}

// rawSettingsRow returns the settings row exactly as it sits on disk.
func rawSettingsRow(t *testing.T, s *Store) string {
	t.Helper()
	var raw string
	if err := s.Reader().QueryRow("SELECT value FROM settings WHERE key = 'settings'").Scan(&raw); err != nil {
		t.Fatalf("read raw settings: %v", err)
	}
	return raw
}

func TestSettingsSecretsSealedAtRest(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	st, err := s.LoadSettings(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	st.Alerts.SMTP.Password = "smtp-sup3r-secret"
	st.General.AccessPassword = "lan-acc3ss-secret"
	if err := s.SaveSettings(ctx, st); err != nil {
		t.Fatalf("save: %v", err)
	}

	raw := rawSettingsRow(t, s)
	if strings.Contains(raw, "smtp-sup3r-secret") || strings.Contains(raw, "lan-acc3ss-secret") {
		t.Fatalf("passwords stored in cleartext: %s", raw)
	}
	if strings.Count(raw, secrets.Prefix) != 2 {
		t.Fatalf("expected two sealed values in row: %s", raw)
	}

	got, err := s.LoadSettings(ctx)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Alerts.SMTP.Password != "smtp-sup3r-secret" || got.General.AccessPassword != "lan-acc3ss-secret" {
		t.Fatalf("round trip lost secrets: %+v", got)
	}
	if err := s.SecretsHealthy(); err != nil {
		t.Fatalf("SecretsHealthy: %v", err)
	}
}

func TestSettingsEmptySecretsStayEmpty(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	st, _ := s.LoadSettings(ctx)
	if err := s.SaveSettings(ctx, st); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, _ := s.LoadSettings(ctx)
	if got.Alerts.SMTP.Password != "" || got.General.AccessPassword != "" {
		t.Fatalf("empty secrets became non-empty: %+v", got)
	}
}

func TestPlaintextSettingsMigratedOnOpen(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	ctx := context.Background()

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	st, _ := s.LoadSettings(ctx)
	st.Alerts.SMTP.Password = "legacy-smtp"
	st.General.AccessPassword = "legacy-access"
	// Bypass SaveSettings to simulate a database written before encryption.
	if err := s.PutSetting(ctx, "settings", st); err != nil {
		t.Fatalf("put: %v", err)
	}
	if raw := rawSettingsRow(t, s); !strings.Contains(raw, "legacy-smtp") {
		t.Fatalf("precondition: plaintext not written: %s", raw)
	}
	s.Close()

	s2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	raw := rawSettingsRow(t, s2)
	if strings.Contains(raw, "legacy-smtp") || strings.Contains(raw, "legacy-access") {
		t.Fatalf("plaintext survived migration: %s", raw)
	}
	if strings.Count(raw, secrets.Prefix) != 2 {
		t.Fatalf("expected sealed values after migration: %s", raw)
	}
	got, err := s2.LoadSettings(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Alerts.SMTP.Password != "legacy-smtp" || got.General.AccessPassword != "legacy-access" {
		t.Fatalf("migration lost values: %+v", got)
	}
}

func TestWrongKeyFileYieldsEmptySecrets(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	ctx := context.Background()

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	st, _ := s.LoadSettings(ctx)
	st.Alerts.SMTP.Password = "smtp-secret"
	st.General.AccessPassword = "access-secret"
	st.Alerts.Recipients = []string{"a@b.c"}
	if err := s.SaveSettings(ctx, st); err != nil {
		t.Fatalf("save: %v", err)
	}
	s.Close()

	// Replace the key file: the rest of the settings must still load.
	if err := os.Remove(filepath.Join(dir, KeyFileName)); err != nil {
		t.Fatalf("remove key: %v", err)
	}
	s2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("reopen with new key: %v", err)
	}
	defer s2.Close()
	got, err := s2.LoadSettings(ctx)
	if err != nil {
		t.Fatalf("load must not fail on undecryptable secrets: %v", err)
	}
	if got.Alerts.SMTP.Password != "" || got.General.AccessPassword != "" {
		t.Fatalf("secrets should be empty with a different key: %+v", got)
	}
	if len(got.Alerts.Recipients) != 1 {
		t.Fatalf("non-secret settings lost: %+v", got)
	}
	if s2.SecretsHealthy() == nil {
		t.Fatalf("SecretsHealthy should report the decryption failure")
	}
}

func TestKeyFileCreatedNextToDatabase(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	fi, err := os.Stat(filepath.Join(dir, KeyFileName))
	if err != nil {
		t.Fatalf("key file: %v", err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file perms = %v, want 0600", fi.Mode().Perm())
	}
}

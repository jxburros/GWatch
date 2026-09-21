package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// Schema additions for automation. Applied by migrate() after the base schema.
const automationSchema = `
CREATE TABLE IF NOT EXISTS triggers (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 1,
  conditions TEXT NOT NULL DEFAULT '[]',
  check_id INTEGER,
  latency_over_ms REAL NOT NULL DEFAULT 0,
  action TEXT NOT NULL DEFAULT '{}',
  cooldown_minutes INTEGER NOT NULL DEFAULT 0,
  last_run_at TEXT,
  last_status TEXT NOT NULL DEFAULT '',
  last_output TEXT NOT NULL DEFAULT '',
  run_count INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_triggers_node ON triggers(node_id);

CREATE TABLE IF NOT EXISTS endpoints (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  slug TEXT NOT NULL UNIQUE,
  description TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 1,
  method TEXT NOT NULL DEFAULT 'ANY',
  token TEXT NOT NULL DEFAULT '',
  allow_no_token INTEGER NOT NULL DEFAULT 0,
  action TEXT NOT NULL DEFAULT '{}',
  last_called_at TEXT,
  last_status TEXT NOT NULL DEFAULT '',
  last_output TEXT NOT NULL DEFAULT '',
  call_count INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

-- Notification rules (#31): conditions across nodes and checks, joined by
-- all / any / at_least, with a list of actions. join_kind because JOIN is a
-- keyword everywhere.
CREATE TABLE IF NOT EXISTS rules (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  join_kind TEXT NOT NULL DEFAULT 'all',
  at_least INTEGER NOT NULL DEFAULT 1,
  conditions TEXT NOT NULL DEFAULT '[]',
  actions TEXT NOT NULL DEFAULT '[]',
  cooldown_minutes INTEGER NOT NULL DEFAULT 0,
  notify_cleared INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS rule_state (
  rule_id INTEGER PRIMARY KEY REFERENCES rules(id) ON DELETE CASCADE,
  met INTEGER NOT NULL DEFAULT 0,
  since TEXT,
  last_fired_at TEXT
);
`

// ---- triggers ----

const triggerCols = `id, node_id, name, description, enabled, conditions, check_id, latency_over_ms, metric, metric_over, action, cooldown_minutes, last_run_at, last_status, last_output, run_count, created_at, updated_at`

func scanTrigger(sc interface{ Scan(...any) error }) (model.Trigger, error) {
	var t model.Trigger
	var enabled int
	var conds, action, created, updated string
	var check sql.NullInt64
	var lastRun sql.NullString
	if err := sc.Scan(&t.ID, &t.NodeID, &t.Name, &t.Description, &enabled, &conds, &check, &t.LatencyOverMS, &t.Metric, &t.MetricOver, &action, &t.CooldownMinutes, &lastRun, &t.LastStatus, &t.LastOutput, &t.RunCount, &created, &updated); err != nil {
		return t, err
	}
	t.Enabled = enabled == 1
	_ = json.Unmarshal([]byte(conds), &t.On)
	if t.On == nil {
		t.On = []string{}
	}
	_ = json.Unmarshal([]byte(action), &t.Action)
	t.CheckID = int64Ptr(check)
	t.LastRunAt = parseTime(lastRun)
	t.CreatedAt, t.UpdatedAt = mustTime(created), mustTime(updated)
	return t, nil
}

// ListTriggers returns every trigger, optionally limited to one node.
func (s *Store) ListTriggers(ctx context.Context, nodeID *int64) ([]model.Trigger, error) {
	q := "SELECT " + triggerCols + " FROM triggers"
	var args []any
	if nodeID != nil {
		q += " WHERE node_id = ?"
		args = append(args, *nodeID)
	}
	q += " ORDER BY node_id, id"
	rows, err := s.query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Trigger{}
	for rows.Next() {
		t, err := scanTrigger(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetTrigger returns one trigger.
func (s *Store) GetTrigger(ctx context.Context, id int64) (model.Trigger, error) {
	row := s.queryRow(ctx, "SELECT "+triggerCols+" FROM triggers WHERE id = ?", id)
	t, err := scanTrigger(row)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

// SaveTrigger inserts (ID == 0) or updates a trigger. Run statistics are
// kept from the stored row on update.
func (s *Store) SaveTrigger(ctx context.Context, t model.Trigger) (model.Trigger, error) {
	now := time.Now()
	if t.On == nil {
		t.On = []string{}
	}
	t.UpdatedAt = now
	if t.ID == 0 {
		t.CreatedAt = now
		newID, err := s.insertID(ctx, `INSERT INTO triggers(node_id, name, description, enabled, conditions, check_id, latency_over_ms, metric, metric_over, action, cooldown_minutes, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			t.NodeID, t.Name, t.Description, boolInt(t.Enabled), jsonString(t.On), nullInt64(t.CheckID), t.LatencyOverMS, t.Metric, t.MetricOver, jsonString(t.Action), t.CooldownMinutes, fmtTime(now), fmtTime(now))
		if err != nil {
			return t, err
		}
		t.ID = newID
		return t, nil
	}
	res, err := s.exec(ctx, `UPDATE triggers SET node_id=?, name=?, description=?, enabled=?, conditions=?, check_id=?, latency_over_ms=?, metric=?, metric_over=?, action=?, cooldown_minutes=?, updated_at=? WHERE id=?`,
		t.NodeID, t.Name, t.Description, boolInt(t.Enabled), jsonString(t.On), nullInt64(t.CheckID), t.LatencyOverMS, t.Metric, t.MetricOver, jsonString(t.Action), t.CooldownMinutes, fmtTime(now), t.ID)
	if err != nil {
		return t, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return t, ErrNotFound
	}
	return s.GetTrigger(ctx, t.ID)
}

// RecordTriggerRun stores the outcome of a trigger execution.
func (s *Store) RecordTriggerRun(ctx context.Context, id int64, at time.Time, ok bool, output string) error {
	status := "failed"
	if ok {
		status = "ok"
	}
	_, err := s.exec(ctx, `UPDATE triggers SET last_run_at=?, last_status=?, last_output=?, run_count=run_count+1 WHERE id=?`, fmtTime(at), status, clip(output, 4000), id)
	return err
}

// DeleteTrigger removes a trigger.
func (s *Store) DeleteTrigger(ctx context.Context, id int64) error {
	res, err := s.exec(ctx, "DELETE FROM triggers WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- endpoints ----

const endpointCols = `id, name, slug, description, enabled, method, token, allow_no_token, action, last_called_at, last_status, last_output, call_count, created_at, updated_at`

func scanEndpoint(sc interface{ Scan(...any) error }) (model.Endpoint, error) {
	var e model.Endpoint
	var enabled, allowNoToken int
	var action, created, updated string
	var lastCalled sql.NullString
	if err := sc.Scan(&e.ID, &e.Name, &e.Slug, &e.Description, &enabled, &e.Method, &e.Token, &allowNoToken, &action, &lastCalled, &e.LastStatus, &e.LastOutput, &e.CallCount, &created, &updated); err != nil {
		return e, err
	}
	e.Enabled = enabled == 1
	e.AllowNoToken = allowNoToken == 1
	_ = json.Unmarshal([]byte(action), &e.Action)
	e.LastCalledAt = parseTime(lastCalled)
	e.CreatedAt, e.UpdatedAt = mustTime(created), mustTime(updated)
	return e, nil
}

// ListEndpoints returns every custom endpoint.
func (s *Store) ListEndpoints(ctx context.Context) ([]model.Endpoint, error) {
	rows, err := s.query(ctx, "SELECT "+endpointCols+" FROM endpoints ORDER BY "+s.d.ci("name")+", id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Endpoint{}
	for rows.Next() {
		e, err := scanEndpoint(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetEndpoint returns one endpoint by id.
func (s *Store) GetEndpoint(ctx context.Context, id int64) (model.Endpoint, error) {
	row := s.queryRow(ctx, "SELECT "+endpointCols+" FROM endpoints WHERE id = ?", id)
	e, err := scanEndpoint(row)
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	return e, err
}

// GetEndpointBySlug returns one endpoint by its URL slug.
func (s *Store) GetEndpointBySlug(ctx context.Context, slug string) (model.Endpoint, error) {
	row := s.queryRow(ctx, "SELECT "+endpointCols+" FROM endpoints WHERE slug = ?", slug)
	e, err := scanEndpoint(row)
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	return e, err
}

// SaveEndpoint inserts (ID == 0) or updates an endpoint.
func (s *Store) SaveEndpoint(ctx context.Context, e model.Endpoint) (model.Endpoint, error) {
	now := time.Now()
	e.UpdatedAt = now
	if e.ID == 0 {
		e.CreatedAt = now
		newID, err := s.insertID(ctx, `INSERT INTO endpoints(name, slug, description, enabled, method, token, allow_no_token, action, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)`,
			e.Name, e.Slug, e.Description, boolInt(e.Enabled), e.Method, e.Token, boolInt(e.AllowNoToken), jsonString(e.Action), fmtTime(now), fmtTime(now))
		if err != nil {
			if s.d.isUniqueViolation(err) {
				return e, fmt.Errorf("an endpoint with the slug %q already exists", e.Slug)
			}
			return e, err
		}
		e.ID = newID
		return e, nil
	}
	res, err := s.exec(ctx, `UPDATE endpoints SET name=?, slug=?, description=?, enabled=?, method=?, token=?, allow_no_token=?, action=?, updated_at=? WHERE id=?`,
		e.Name, e.Slug, e.Description, boolInt(e.Enabled), e.Method, e.Token, boolInt(e.AllowNoToken), jsonString(e.Action), fmtTime(now), e.ID)
	if err != nil {
		if s.d.isUniqueViolation(err) {
			return e, fmt.Errorf("an endpoint with the slug %q already exists", e.Slug)
		}
		return e, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return e, ErrNotFound
	}
	return s.GetEndpoint(ctx, e.ID)
}

// RecordEndpointCall stores the outcome of an endpoint invocation.
func (s *Store) RecordEndpointCall(ctx context.Context, id int64, at time.Time, ok bool, output string) error {
	status := "failed"
	if ok {
		status = "ok"
	}
	_, err := s.exec(ctx, `UPDATE endpoints SET last_called_at=?, last_status=?, last_output=?, call_count=call_count+1 WHERE id=?`, fmtTime(at), status, clip(output, 4000), id)
	return err
}

// DeleteEndpoint removes an endpoint.
func (s *Store) DeleteEndpoint(ctx context.Context, id int64) error {
	res, err := s.exec(ctx, "DELETE FROM endpoints WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- rules ----

const ruleCols = `id, name, enabled, join_kind, at_least, conditions, actions, cooldown_minutes, notify_cleared, created_at, updated_at`

func scanRule(sc interface{ Scan(...any) error }) (model.Rule, error) {
	var r model.Rule
	var enabled, notifyCleared int
	var conds, acts, created, updated string
	if err := sc.Scan(&r.ID, &r.Name, &enabled, &r.Join, &r.AtLeast, &conds, &acts, &r.CooldownMinutes, &notifyCleared, &created, &updated); err != nil {
		return r, err
	}
	r.Enabled = enabled == 1
	r.NotifyCleared = notifyCleared == 1
	_ = json.Unmarshal([]byte(conds), &r.Conditions)
	if r.Conditions == nil {
		r.Conditions = []model.RuleCondition{}
	}
	_ = json.Unmarshal([]byte(acts), &r.Actions)
	if r.Actions == nil {
		r.Actions = []model.Action{}
	}
	r.CreatedAt, r.UpdatedAt = mustTime(created), mustTime(updated)
	return r, nil
}

// ListRules returns every notification rule, by name.
func (s *Store) ListRules(ctx context.Context) ([]model.Rule, error) {
	rows, err := s.query(ctx, "SELECT "+ruleCols+" FROM rules ORDER BY "+s.d.ci("name")+", id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Rule{}
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetRule returns one rule.
func (s *Store) GetRule(ctx context.Context, id int64) (model.Rule, error) {
	row := s.queryRow(ctx, "SELECT "+ruleCols+" FROM rules WHERE id = ?", id)
	r, err := scanRule(row)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

// SaveRule inserts (ID == 0) or updates a rule. Its state (RuleState) is a
// separate row and is left alone.
func (s *Store) SaveRule(ctx context.Context, r model.Rule) (model.Rule, error) {
	now := time.Now()
	if r.Conditions == nil {
		r.Conditions = []model.RuleCondition{}
	}
	if r.Actions == nil {
		r.Actions = []model.Action{}
	}
	if r.Join == "" {
		r.Join = model.RuleJoinAll
	}
	r.UpdatedAt = now
	if r.ID == 0 {
		r.CreatedAt = now
		newID, err := s.insertID(ctx, `INSERT INTO rules(name, enabled, join_kind, at_least, conditions, actions, cooldown_minutes, notify_cleared, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)`,
			r.Name, boolInt(r.Enabled), r.Join, r.AtLeast, jsonString(r.Conditions), jsonString(r.Actions), r.CooldownMinutes, boolInt(r.NotifyCleared), fmtTime(now), fmtTime(now))
		if err != nil {
			return r, err
		}
		r.ID = newID
		return r, nil
	}
	res, err := s.exec(ctx, `UPDATE rules SET name=?, enabled=?, join_kind=?, at_least=?, conditions=?, actions=?, cooldown_minutes=?, notify_cleared=?, updated_at=? WHERE id=?`,
		r.Name, boolInt(r.Enabled), r.Join, r.AtLeast, jsonString(r.Conditions), jsonString(r.Actions), r.CooldownMinutes, boolInt(r.NotifyCleared), fmtTime(now), r.ID)
	if err != nil {
		return r, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return r, ErrNotFound
	}
	return s.GetRule(ctx, r.ID)
}

// DeleteRule removes a rule and its state.
func (s *Store) DeleteRule(ctx context.Context, id int64) error {
	return s.writeTx(ctx, func(tx *wtx) error {
		// The state row first: not every backend cascades here.
		if _, err := tx.exec(ctx, "DELETE FROM rule_state WHERE rule_id = ?", id); err != nil {
			return err
		}
		res, err := tx.exec(ctx, "DELETE FROM rules WHERE id = ?", id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

var ruleStateCols = []string{"rule_id", "met", "since", "last_fired_at"}

// RuleStates returns the stored state of every rule that has one, by rule id.
func (s *Store) RuleStates(ctx context.Context) (map[int64]model.RuleState, error) {
	rows, err := s.query(ctx, "SELECT rule_id, met, since, last_fired_at FROM rule_state")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]model.RuleState{}
	for rows.Next() {
		var st model.RuleState
		var met int
		var since, fired sql.NullString
		if err := rows.Scan(&st.RuleID, &met, &since, &fired); err != nil {
			return nil, err
		}
		st.Met = met == 1
		st.Since = parseTime(since)
		st.LastFiredAt = parseTime(fired)
		out[st.RuleID] = st
	}
	return out, rows.Err()
}

// PutRuleState upserts the state of a rule.
func (s *Store) PutRuleState(ctx context.Context, st model.RuleState) error {
	_, err := s.exec(ctx, insertValues("rule_state", ruleStateCols)+" "+s.d.upsertClause([]string{"rule_id"}, ruleStateCols),
		st.RuleID, boolInt(st.Met), fmtTimePtr(st.Since), fmtTimePtr(st.LastFiredAt))
	return err
}

// ---- saved charts ----

// ListSavedCharts returns the chart configurations kept on the Charts page.
func (s *Store) ListSavedCharts(ctx context.Context) ([]model.SavedChart, error) {
	var out []model.SavedChart
	err := s.GetSetting(ctx, "savedCharts", &out)
	if errors.Is(err, ErrNotFound) {
		return []model.SavedChart{}, nil
	}
	if out == nil {
		out = []model.SavedChart{}
	}
	return out, err
}

// SaveSavedCharts replaces the saved chart list.
func (s *Store) SaveSavedCharts(ctx context.Context, charts []model.SavedChart) error {
	if charts == nil {
		charts = []model.SavedChart{}
	}
	return s.PutSetting(ctx, "savedCharts", charts)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

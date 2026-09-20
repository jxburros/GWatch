package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/secrets"
)

const nodeCols = `id, name, host, group_name, tags, notes, importance, enabled, depends_on_node_id, template, created_at, updated_at`

func scanNode(sc interface{ Scan(...any) error }) (model.Node, error) {
	var n model.Node
	var tags, created, updated string
	var enabled int
	var dep sql.NullInt64
	if err := sc.Scan(&n.ID, &n.Name, &n.Host, &n.Group, &tags, &n.Notes, &n.Importance, &enabled, &dep, &n.Template, &created, &updated); err != nil {
		return n, err
	}
	n.Enabled = enabled == 1
	n.DependsOnNode = int64Ptr(dep)
	n.CreatedAt = mustTime(created)
	n.UpdatedAt = mustTime(updated)
	_ = json.Unmarshal([]byte(tags), &n.Tags)
	if n.Tags == nil {
		n.Tags = []string{}
	}
	n.Checks = []model.Check{}
	return n, nil
}

// ListNodes returns all nodes with their checks, ordered by name.
func (s *Store) ListNodes(ctx context.Context) ([]model.Node, error) {
	rows, err := s.reader.QueryContext(ctx, "SELECT "+nodeCols+" FROM nodes ORDER BY name COLLATE NOCASE")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var nodes []model.Node
	index := map[int64]int{}
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		index[n.ID] = len(nodes)
		nodes = append(nodes, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	checks, err := s.ListChecks(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range checks {
		if i, ok := index[c.NodeID]; ok {
			nodes[i].Checks = append(nodes[i].Checks, c)
		}
	}
	if nodes == nil {
		nodes = []model.Node{}
	}
	return nodes, nil
}

// GetNode returns a node with its checks.
func (s *Store) GetNode(ctx context.Context, id int64) (model.Node, error) {
	row := s.reader.QueryRowContext(ctx, "SELECT "+nodeCols+" FROM nodes WHERE id = ?", id)
	n, err := scanNode(row)
	if errors.Is(err, sql.ErrNoRows) {
		return n, ErrNotFound
	}
	if err != nil {
		return n, err
	}
	checks, err := s.ListChecksForNode(ctx, id)
	if err != nil {
		return n, err
	}
	n.Checks = checks
	return n, nil
}

// CreateNode inserts a node and its checks.
func (s *Store) CreateNode(ctx context.Context, n model.Node) (model.Node, error) {
	now := time.Now()
	n.CreatedAt, n.UpdatedAt = now, now
	if n.Tags == nil {
		n.Tags = []string{}
	}
	if n.Importance == "" {
		n.Importance = model.ImportanceNormal
	}
	err := s.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO nodes(name, host, group_name, tags, notes, importance, enabled, depends_on_node_id, template, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			n.Name, n.Host, n.Group, jsonString(n.Tags), n.Notes, string(n.Importance), boolInt(n.Enabled), nullInt64(n.DependsOnNode), n.Template, fmtTime(now), fmtTime(now))
		if err != nil {
			return err
		}
		n.ID, _ = res.LastInsertId()
		for i := range n.Checks {
			n.Checks[i].NodeID = n.ID
			n.Checks[i].ID = 0
			if n.Checks[i].SortOrder == 0 {
				n.Checks[i].SortOrder = i
			}
			c, err := insertCheck(ctx, tx, n.Checks[i])
			if err != nil {
				return err
			}
			n.Checks[i] = c
		}
		return nil
	})
	return n, err
}

// UpdateNode updates a node and reconciles its checks: checks with an ID are
// updated, checks without an ID are inserted, and checks that are no longer
// present are deleted. It returns the ids of deleted checks.
func (s *Store) UpdateNode(ctx context.Context, n model.Node) (model.Node, []int64, error) {
	now := time.Now()
	n.UpdatedAt = now
	if n.Tags == nil {
		n.Tags = []string{}
	}
	if n.Importance == "" {
		n.Importance = model.ImportanceNormal
	}
	if n.DependsOnNode != nil && *n.DependsOnNode == n.ID {
		n.DependsOnNode = nil
	}
	var deleted []int64
	err := s.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE nodes SET name=?, host=?, group_name=?, tags=?, notes=?, importance=?, enabled=?, depends_on_node_id=?, template=?, updated_at=? WHERE id=?`,
			n.Name, n.Host, n.Group, jsonString(n.Tags), n.Notes, string(n.Importance), boolInt(n.Enabled), nullInt64(n.DependsOnNode), n.Template, fmtTime(now), n.ID)
		if err != nil {
			return err
		}
		if affected, _ := res.RowsAffected(); affected == 0 {
			return ErrNotFound
		}
		existing := map[int64]bool{}
		rows, err := tx.QueryContext(ctx, "SELECT id FROM checks WHERE node_id = ?", n.ID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			existing[id] = true
		}
		rows.Close()
		keep := map[int64]bool{}
		for i := range n.Checks {
			c := n.Checks[i]
			c.NodeID = n.ID
			c.SortOrder = i
			if c.ID != 0 && existing[c.ID] {
				c, err = updateCheck(ctx, tx, c)
			} else {
				c.ID = 0
				c, err = insertCheck(ctx, tx, c)
			}
			if err != nil {
				return err
			}
			keep[c.ID] = true
			n.Checks[i] = c
		}
		for id := range existing {
			if !keep[id] {
				if _, err := tx.ExecContext(ctx, "DELETE FROM checks WHERE id = ?", id); err != nil {
					return err
				}
				deleted = append(deleted, id)
			}
		}
		return nil
	})
	return n, deleted, err
}

// SetNodeEnabled toggles a node.
func (s *Store) SetNodeEnabled(ctx context.Context, id int64, enabled bool) error {
	res, err := s.Exec(ctx, "UPDATE nodes SET enabled=?, updated_at=? WHERE id=?", boolInt(enabled), fmtTime(time.Now()), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteNode removes a node, its checks, results, rollups and state.
func (s *Store) DeleteNode(ctx context.Context, id int64) error {
	res, err := s.Exec(ctx, "DELETE FROM nodes WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// GroupCounts returns groups and tags with node counts.
func (s *Store) GroupCounts(ctx context.Context) (groups map[string]int, tags map[string]int, err error) {
	nodes, err := s.ListNodes(ctx)
	if err != nil {
		return nil, nil, err
	}
	groups = map[string]int{}
	tags = map[string]int{}
	for _, n := range nodes {
		if g := strings.TrimSpace(n.Group); g != "" {
			groups[g]++
		}
		for _, t := range n.Tags {
			if t = strings.TrimSpace(t); t != "" {
				tags[t]++
			}
		}
	}
	return groups, tags, nil
}

// ---- checks ----

const checkCols = `id, node_id, type, name, enabled, interval_seconds, timeout_seconds, retries, failure_threshold, config, alerts, sort_order, created_at, updated_at`

func scanCheck(sc interface{ Scan(...any) error }) (model.Check, error) {
	var c model.Check
	var enabled int
	var cfg, created, updated string
	var alerts sql.NullString
	if err := sc.Scan(&c.ID, &c.NodeID, &c.Type, &c.Name, &enabled, &c.IntervalSeconds, &c.TimeoutSeconds, &c.Retries, &c.FailureThreshold, &cfg, &alerts, &c.SortOrder, &created, &updated); err != nil {
		return c, err
	}
	c.Enabled = enabled == 1
	_ = json.Unmarshal([]byte(cfg), &c.Config)
	if alerts.Valid && alerts.String != "" && alerts.String != "null" {
		var a model.AlertOverride
		if err := json.Unmarshal([]byte(alerts.String), &a); err == nil {
			c.Alerts = &a
		}
	}
	c.CreatedAt = mustTime(created)
	c.UpdatedAt = mustTime(updated)
	return c, nil
}

func insertCheck(ctx context.Context, tx *sql.Tx, c model.Check) (model.Check, error) {
	now := time.Now()
	c.CreatedAt, c.UpdatedAt = now, now
	var alerts any
	if c.Alerts != nil {
		alerts = jsonString(c.Alerts)
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO checks(node_id, type, name, enabled, interval_seconds, timeout_seconds, retries, failure_threshold, config, alerts, sort_order, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.NodeID, string(c.Type), c.Name, boolInt(c.Enabled), c.IntervalSeconds, c.TimeoutSeconds, c.Retries, c.FailureThreshold, jsonString(c.Config), alerts, c.SortOrder, fmtTime(now), fmtTime(now))
	if err != nil {
		return c, err
	}
	c.ID, _ = res.LastInsertId()
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO check_state(check_id, status) VALUES (?, 'unknown')`, c.ID)
	return c, err
}

func updateCheck(ctx context.Context, tx *sql.Tx, c model.Check) (model.Check, error) {
	now := time.Now()
	c.UpdatedAt = now
	var alerts any
	if c.Alerts != nil {
		alerts = jsonString(c.Alerts)
	}
	_, err := tx.ExecContext(ctx, `UPDATE checks SET node_id=?, type=?, name=?, enabled=?, interval_seconds=?, timeout_seconds=?, retries=?, failure_threshold=?, config=?, alerts=?, sort_order=?, updated_at=? WHERE id=?`,
		c.NodeID, string(c.Type), c.Name, boolInt(c.Enabled), c.IntervalSeconds, c.TimeoutSeconds, c.Retries, c.FailureThreshold, jsonString(c.Config), alerts, c.SortOrder, fmtTime(now), c.ID)
	if err != nil {
		return c, err
	}
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO check_state(check_id, status) VALUES (?, 'unknown')`, c.ID)
	return c, err
}

// ListChecks returns all checks ordered by node and sort order.
func (s *Store) ListChecks(ctx context.Context) ([]model.Check, error) {
	rows, err := s.reader.QueryContext(ctx, "SELECT "+checkCols+" FROM checks ORDER BY node_id, sort_order, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Check
	for rows.Next() {
		c, err := scanCheck(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if out == nil {
		out = []model.Check{}
	}
	return out, rows.Err()
}

// ListChecksForNode returns the checks belonging to a node.
func (s *Store) ListChecksForNode(ctx context.Context, nodeID int64) ([]model.Check, error) {
	rows, err := s.reader.QueryContext(ctx, "SELECT "+checkCols+" FROM checks WHERE node_id = ? ORDER BY sort_order, id", nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Check{}
	for rows.Next() {
		c, err := scanCheck(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetCheck returns one check.
func (s *Store) GetCheck(ctx context.Context, id int64) (model.Check, error) {
	row := s.reader.QueryRowContext(ctx, "SELECT "+checkCols+" FROM checks WHERE id = ?", id)
	c, err := scanCheck(row)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

// SetCheckEnabled toggles a check.
func (s *Store) SetCheckEnabled(ctx context.Context, id int64, enabled bool) error {
	res, err := s.Exec(ctx, "UPDATE checks SET enabled=?, updated_at=? WHERE id=?", boolInt(enabled), fmtTime(time.Now()), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- check state ----

const stateCols = `check_id, status, consecutive_failures, last_run_at, last_success_at, last_change_at, next_run_at, last_message, last_latency_ms, alert_active, alert_suppressed, suppress_reason, last_alert_at, silenced_until, affected_by_check_id, warning_active, cert_warning_active, last_content_hash, last_content_value`

func scanState(sc interface{ Scan(...any) error }) (model.CheckState, error) {
	var st model.CheckState
	var lastRun, lastSuccess, lastChange, nextRun, lastAlert, silenced sql.NullString
	var lat sql.NullFloat64
	var affected sql.NullInt64
	var alertActive, alertSuppressed, warn, certWarn int
	if err := sc.Scan(&st.CheckID, &st.Status, &st.ConsecutiveFailures, &lastRun, &lastSuccess, &lastChange, &nextRun, &st.LastMessage, &lat, &alertActive, &alertSuppressed, &st.SuppressReason, &lastAlert, &silenced, &affected, &warn, &certWarn, &st.LastContentHash, &st.LastContentValue); err != nil {
		return st, err
	}
	st.LastRunAt = parseTime(lastRun)
	st.LastSuccessAt = parseTime(lastSuccess)
	st.LastChangeAt = parseTime(lastChange)
	st.NextRunAt = parseTime(nextRun)
	st.LastAlertAt = parseTime(lastAlert)
	st.SilencedUntil = parseTime(silenced)
	st.LastLatencyMS = floatPtr(lat)
	st.AffectedByCheckID = int64Ptr(affected)
	st.AlertActive = alertActive == 1
	st.AlertSuppressed = alertSuppressed == 1
	st.WarningActive = warn == 1
	st.CertWarningActive = certWarn == 1
	return st, nil
}

// ListStates returns the live state of every check.
func (s *Store) ListStates(ctx context.Context) (map[int64]model.CheckState, error) {
	rows, err := s.reader.QueryContext(ctx, "SELECT "+stateCols+" FROM check_state")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]model.CheckState{}
	for rows.Next() {
		st, err := scanState(rows)
		if err != nil {
			return nil, err
		}
		out[st.CheckID] = st
	}
	return out, rows.Err()
}

// GetState returns the state of one check.
func (s *Store) GetState(ctx context.Context, checkID int64) (model.CheckState, error) {
	row := s.reader.QueryRowContext(ctx, "SELECT "+stateCols+" FROM check_state WHERE check_id = ?", checkID)
	st, err := scanState(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.CheckState{CheckID: checkID, Status: model.StatusUnknown}, nil
	}
	return st, err
}

// SaveState upserts the state of a check.
func (s *Store) SaveState(ctx context.Context, st model.CheckState) error {
	return s.WriteTx(ctx, func(tx *sql.Tx) error {
		return saveStateTx(ctx, tx, st)
	})
}

func saveStateTx(ctx context.Context, tx *sql.Tx, st model.CheckState) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO check_state(`+stateCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(check_id) DO UPDATE SET status=excluded.status, consecutive_failures=excluded.consecutive_failures, last_run_at=excluded.last_run_at,
		last_success_at=excluded.last_success_at, last_change_at=excluded.last_change_at, next_run_at=excluded.next_run_at, last_message=excluded.last_message,
		last_latency_ms=excluded.last_latency_ms, alert_active=excluded.alert_active, alert_suppressed=excluded.alert_suppressed, suppress_reason=excluded.suppress_reason,
		last_alert_at=excluded.last_alert_at, silenced_until=excluded.silenced_until, affected_by_check_id=excluded.affected_by_check_id, warning_active=excluded.warning_active,
		cert_warning_active=excluded.cert_warning_active, last_content_hash=excluded.last_content_hash, last_content_value=excluded.last_content_value`,
		st.CheckID, string(st.Status), st.ConsecutiveFailures, fmtTimePtr(st.LastRunAt), fmtTimePtr(st.LastSuccessAt), fmtTimePtr(st.LastChangeAt), fmtTimePtr(st.NextRunAt),
		st.LastMessage, nullFloat(st.LastLatencyMS), boolInt(st.AlertActive), boolInt(st.AlertSuppressed), st.SuppressReason, fmtTimePtr(st.LastAlertAt), fmtTimePtr(st.SilencedUntil),
		nullInt64(st.AffectedByCheckID), boolInt(st.WarningActive), boolInt(st.CertWarningActive), st.LastContentHash, st.LastContentValue)
	return err
}

// ---- results ----

const resultCols = `id, check_id, ts, success, status, message, error, latency_ms, min_ms, max_ms, jitter_ms, stddev_ms, loss_pct, attempts, details, warnings`

func scanResult(sc interface{ Scan(...any) error }) (model.Result, error) {
	var r model.Result
	var ts int64
	var success int
	var lat, min, max, jit, sd, loss sql.NullFloat64
	var details, warnings string
	if err := sc.Scan(&r.ID, &r.CheckID, &ts, &success, &r.Status, &r.Message, &r.Error, &lat, &min, &max, &jit, &sd, &loss, &r.Attempts, &details, &warnings); err != nil {
		return r, err
	}
	r.Timestamp = time.UnixMilli(ts).Local()
	r.Success = success == 1
	r.LatencyMS, r.MinMS, r.MaxMS, r.JitterMS, r.LossPct = floatPtr(lat), floatPtr(min), floatPtr(max), floatPtr(jit), floatPtr(loss)
	r.StdDevMS = floatPtr(sd)
	_ = json.Unmarshal([]byte(details), &r.Details)
	_ = json.Unmarshal([]byte(warnings), &r.Warnings)
	return r, nil
}

// InsertResult stores a raw result and returns it with the id set.
func (s *Store) InsertResult(ctx context.Context, r model.Result) (model.Result, error) {
	err := s.WriteTx(ctx, func(tx *sql.Tx) error {
		var err error
		r, err = insertResultTx(ctx, tx, r)
		return err
	})
	return r, err
}

func insertResultTx(ctx context.Context, tx *sql.Tx, r model.Result) (model.Result, error) {
	if r.Timestamp.IsZero() {
		r.Timestamp = time.Now()
	}
	if r.Warnings == nil {
		r.Warnings = []string{}
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO results(check_id, ts, success, status, message, error, latency_ms, min_ms, max_ms, jitter_ms, stddev_ms, loss_pct, attempts, details, warnings)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.CheckID, r.Timestamp.UnixMilli(), boolInt(r.Success), string(r.Status), r.Message, r.Error, nullFloat(r.LatencyMS), nullFloat(r.MinMS), nullFloat(r.MaxMS), nullFloat(r.JitterMS), nullFloat(r.StdDevMS), nullFloat(r.LossPct), r.Attempts, jsonString(r.Details), jsonString(r.Warnings))
	if err != nil {
		return r, err
	}
	r.ID, _ = res.LastInsertId()
	return r, nil
}

// RecordResult stores a result and its updated state atomically.
func (s *Store) RecordResult(ctx context.Context, r model.Result, st model.CheckState) (model.Result, error) {
	err := s.WriteTx(ctx, func(tx *sql.Tx) error {
		var err error
		r, err = insertResultTx(ctx, tx, r)
		if err != nil {
			return err
		}
		return saveStateTx(ctx, tx, st)
	})
	return r, err
}

// RecentResults returns the newest results of a check.
func (s *Store) RecentResults(ctx context.Context, checkID int64, limit int) ([]model.Result, error) {
	if limit <= 0 || limit > 5000 {
		limit = 50
	}
	rows, err := s.reader.QueryContext(ctx, "SELECT "+resultCols+" FROM results WHERE check_id = ? ORDER BY ts DESC LIMIT ?", checkID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Result{}
	for rows.Next() {
		r, err := scanResult(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LastResults returns the latest result for every check.
func (s *Store) LastResults(ctx context.Context) (map[int64]model.Result, error) {
	rows, err := s.reader.QueryContext(ctx, "SELECT "+resultCols+" FROM results WHERE id IN (SELECT MAX(id) FROM results GROUP BY check_id)")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]model.Result{}
	for rows.Next() {
		r, err := scanResult(rows)
		if err != nil {
			return nil, err
		}
		out[r.CheckID] = r
	}
	return out, rows.Err()
}

// ResultsBetween returns raw results of a check in [from, to] ascending.
//
// The upper bound is inclusive, as it is for host samples. Timestamps are
// stored truncated to the millisecond, so an exclusive bound would drop a
// result recorded in the same millisecond the caller asks in — a chart drawn
// immediately after a check ran would miss the very result that prompted it.
func (s *Store) ResultsBetween(ctx context.Context, checkID int64, from, to time.Time) ([]model.Result, error) {
	rows, err := s.reader.QueryContext(ctx, "SELECT "+resultCols+" FROM results WHERE check_id = ? AND ts >= ? AND ts <= ? ORDER BY ts ASC", checkID, from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Result{}
	for rows.Next() {
		r, err := scanResult(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---- events ----

const eventCols = `id, ts, type, node_id, check_id, node_name, check_name, title, detail, meta, actor`

func scanEvent(sc interface{ Scan(...any) error }) (model.Event, error) {
	var e model.Event
	var ts int64
	var node, check sql.NullInt64
	var meta sql.NullString
	if err := sc.Scan(&e.ID, &ts, &e.Type, &node, &check, &e.NodeName, &e.CheckName, &e.Title, &e.Detail, &meta, &e.Actor); err != nil {
		return e, err
	}
	e.Timestamp = time.UnixMilli(ts).Local()
	e.NodeID, e.CheckID = int64Ptr(node), int64Ptr(check)
	if meta.Valid && meta.String != "" {
		e.Meta = json.RawMessage(meta.String)
	}
	return e, nil
}

// InsertEvent appends to the timeline.
func (s *Store) InsertEvent(ctx context.Context, e model.Event) (model.Event, error) {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now()
	}
	var meta any
	if len(e.Meta) > 0 {
		meta = string(e.Meta)
	}
	res, err := s.Exec(ctx, `INSERT INTO events(ts, type, node_id, check_id, node_name, check_name, title, detail, meta, actor) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		e.Timestamp.UnixMilli(), string(e.Type), nullInt64(e.NodeID), nullInt64(e.CheckID), e.NodeName, e.CheckName, e.Title, e.Detail, meta, e.Actor)
	if err != nil {
		return e, err
	}
	e.ID, _ = res.LastInsertId()
	return e, nil
}

// EventFilter narrows ListEvents.
type EventFilter struct {
	Limit    int
	BeforeID int64
	NodeID   *int64
	CheckID  *int64
	Types    []model.EventType
	Since    *time.Time
	Until    *time.Time
	Query    string // case-insensitive substring of title, detail, node or check name
}

// ListEvents returns timeline entries newest first.
func (s *Store) ListEvents(ctx context.Context, f EventFilter) ([]model.Event, error) {
	if f.Limit <= 0 || f.Limit > 5000 {
		f.Limit = 100
	}
	var where []string
	var args []any
	if f.BeforeID > 0 {
		where = append(where, "id < ?")
		args = append(args, f.BeforeID)
	}
	if f.NodeID != nil {
		where = append(where, "node_id = ?")
		args = append(args, *f.NodeID)
	}
	if f.CheckID != nil {
		where = append(where, "check_id = ?")
		args = append(args, *f.CheckID)
	}
	if len(f.Types) > 0 {
		where = append(where, "type IN ("+placeholders(len(f.Types))+")")
		for _, t := range f.Types {
			args = append(args, string(t))
		}
	}
	if f.Since != nil {
		where = append(where, "ts >= ?")
		args = append(args, f.Since.UnixMilli())
	}
	if f.Until != nil {
		where = append(where, "ts < ?")
		args = append(args, f.Until.UnixMilli())
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		like := "%" + strings.ToLower(q) + "%"
		where = append(where, "(lower(title) LIKE ? OR lower(detail) LIKE ? OR lower(node_name) LIKE ? OR lower(check_name) LIKE ? OR lower(type) LIKE ?)")
		args = append(args, like, like, like, like, like)
	}
	q := "SELECT " + eventCols + " FROM events"
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY id DESC LIMIT ?"
	args = append(args, f.Limit)
	rows, err := s.reader.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Event{}
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- maintenance windows ----

const mwCols = `id, name, node_id, group_name, enabled, start_at, end_at, weekdays, duration_minutes, notes, created_at`

func scanMW(sc interface{ Scan(...any) error }) (model.MaintenanceWindow, error) {
	var m model.MaintenanceWindow
	var node sql.NullInt64
	var enabled int
	var start, end, weekdays, created string
	if err := sc.Scan(&m.ID, &m.Name, &node, &m.Group, &enabled, &start, &end, &weekdays, &m.DurationMinutes, &m.Notes, &created); err != nil {
		return m, err
	}
	m.NodeID = int64Ptr(node)
	m.Enabled = enabled == 1
	m.StartAt, m.EndAt, m.CreatedAt = mustTime(start), mustTime(end), mustTime(created)
	_ = json.Unmarshal([]byte(weekdays), &m.Weekdays)
	if m.Weekdays == nil {
		m.Weekdays = []int{}
	}
	return m, nil
}

// ListMaintenance returns all windows.
func (s *Store) ListMaintenance(ctx context.Context) ([]model.MaintenanceWindow, error) {
	rows, err := s.reader.QueryContext(ctx, "SELECT "+mwCols+" FROM maintenance_windows ORDER BY start_at DESC, id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.MaintenanceWindow{}
	for rows.Next() {
		m, err := scanMW(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SaveMaintenance inserts (ID == 0) or updates a window.
func (s *Store) SaveMaintenance(ctx context.Context, m model.MaintenanceWindow) (model.MaintenanceWindow, error) {
	if m.Weekdays == nil {
		m.Weekdays = []int{}
	}
	if m.ID == 0 {
		m.CreatedAt = time.Now()
		res, err := s.Exec(ctx, `INSERT INTO maintenance_windows(name, node_id, group_name, enabled, start_at, end_at, weekdays, duration_minutes, notes, created_at) VALUES (?,?,?,?,?,?,?,?,?,?)`,
			m.Name, nullInt64(m.NodeID), m.Group, boolInt(m.Enabled), fmtTime(m.StartAt), fmtTime(m.EndAt), jsonString(m.Weekdays), m.DurationMinutes, m.Notes, fmtTime(m.CreatedAt))
		if err != nil {
			return m, err
		}
		m.ID, _ = res.LastInsertId()
		return m, nil
	}
	res, err := s.Exec(ctx, `UPDATE maintenance_windows SET name=?, node_id=?, group_name=?, enabled=?, start_at=?, end_at=?, weekdays=?, duration_minutes=?, notes=? WHERE id=?`,
		m.Name, nullInt64(m.NodeID), m.Group, boolInt(m.Enabled), fmtTime(m.StartAt), fmtTime(m.EndAt), jsonString(m.Weekdays), m.DurationMinutes, m.Notes, m.ID)
	if err != nil {
		return m, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return m, ErrNotFound
	}
	return m, nil
}

// DeleteMaintenance removes a window.
func (s *Store) DeleteMaintenance(ctx context.Context, id int64) error {
	res, err := s.Exec(ctx, "DELETE FROM maintenance_windows WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- dashboards ----

const dashCols = `id, name, sort_order, widgets, created_at, updated_at`

func scanDash(sc interface{ Scan(...any) error }) (model.Dashboard, error) {
	var d model.Dashboard
	var widgets, created, updated string
	if err := sc.Scan(&d.ID, &d.Name, &d.SortOrder, &widgets, &created, &updated); err != nil {
		return d, err
	}
	_ = json.Unmarshal([]byte(widgets), &d.Widgets)
	if d.Widgets == nil {
		d.Widgets = []model.Widget{}
	}
	d.CreatedAt, d.UpdatedAt = mustTime(created), mustTime(updated)
	return d, nil
}

// ListDashboards returns all dashboards.
func (s *Store) ListDashboards(ctx context.Context) ([]model.Dashboard, error) {
	rows, err := s.reader.QueryContext(ctx, "SELECT "+dashCols+" FROM dashboards ORDER BY sort_order, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Dashboard{}
	for rows.Next() {
		d, err := scanDash(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetDashboard returns one dashboard.
func (s *Store) GetDashboard(ctx context.Context, id int64) (model.Dashboard, error) {
	row := s.reader.QueryRowContext(ctx, "SELECT "+dashCols+" FROM dashboards WHERE id = ?", id)
	d, err := scanDash(row)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	return d, err
}

// SaveDashboard inserts (ID == 0) or updates a dashboard.
func (s *Store) SaveDashboard(ctx context.Context, d model.Dashboard) (model.Dashboard, error) {
	now := time.Now()
	if d.Widgets == nil {
		d.Widgets = []model.Widget{}
	}
	for i := range d.Widgets {
		if d.Widgets[i].ID == "" {
			d.Widgets[i].ID = fmt.Sprintf("w%d%d", now.UnixNano()%1000000, i)
		}
		if len(d.Widgets[i].Config) == 0 {
			d.Widgets[i].Config = json.RawMessage("{}")
		}
	}
	d.UpdatedAt = now
	if d.ID == 0 {
		d.CreatedAt = now
		res, err := s.Exec(ctx, `INSERT INTO dashboards(name, sort_order, widgets, created_at, updated_at) VALUES (?,?,?,?,?)`, d.Name, d.SortOrder, jsonString(d.Widgets), fmtTime(now), fmtTime(now))
		if err != nil {
			return d, err
		}
		d.ID, _ = res.LastInsertId()
		return d, nil
	}
	res, err := s.Exec(ctx, `UPDATE dashboards SET name=?, sort_order=?, widgets=?, updated_at=? WHERE id=?`, d.Name, d.SortOrder, jsonString(d.Widgets), fmtTime(now), d.ID)
	if err != nil {
		return d, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return d, ErrNotFound
	}
	return d, nil
}

// DeleteDashboard removes a dashboard.
func (s *Store) DeleteDashboard(ctx context.Context, id int64) error {
	res, err := s.Exec(ctx, "DELETE FROM dashboards WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- settings / key-value ----

// GetSetting reads a JSON value into out. Returns ErrNotFound when unset.
func (s *Store) GetSetting(ctx context.Context, key string, out any) error {
	var raw string
	err := s.reader.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(raw), out)
}

// PutSetting stores a JSON value.
func (s *Store) PutSetting(ctx context.Context, key string, v any) error {
	_, err := s.Exec(ctx, `INSERT INTO settings(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, jsonString(v))
	return err
}

// sealSettings encrypts the secret fields of a settings document in place so
// they never reach disk in cleartext.
func (s *Store) sealSettings(st *model.Settings) error {
	pw, err := s.secrets.Seal(st.Alerts.SMTP.Password)
	if err != nil {
		return fmt.Errorf("seal smtp password: %w", err)
	}
	st.Alerts.SMTP.Password = pw
	ap, err := s.secrets.Seal(st.General.AccessPassword)
	if err != nil {
		return fmt.Errorf("seal access password: %w", err)
	}
	st.General.AccessPassword = ap
	bp, err := s.secrets.Seal(st.Backups.Password)
	if err != nil {
		return fmt.Errorf("seal backup password: %w", err)
	}
	st.Backups.Password = bp
	return nil
}

// openSettings decrypts the secret fields of a settings document in place.
// A value that cannot be decrypted (wrong or missing key file) is emptied
// rather than failing the whole load; the reason is recorded and reported by
// SecretsHealthy.
func (s *Store) openSettings(st *model.Settings) {
	var firstErr error
	open := func(field *string, what string) {
		v, err := s.secrets.Open(*field)
		if err != nil {
			*field = ""
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", what, err)
			}
			return
		}
		*field = v
	}
	open(&st.Alerts.SMTP.Password, "smtp password")
	open(&st.General.AccessPassword, "access password")
	open(&st.Backups.Password, "backup password")
	s.setSecretsErr(firstErr)
}

// migrateSecrets re-saves the settings row when it still holds plaintext
// secrets, so an upgraded install stops keeping cleartext passwords on disk
// without the user having to touch anything.
func (s *Store) migrateSecrets(ctx context.Context) error {
	st := model.DefaultSettings()
	err := s.GetSetting(ctx, "settings", &st)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		// A settings row we cannot parse is not this migration's problem.
		return nil
	}
	plaintext := func(v string) bool { return v != "" && !secrets.IsSealed(v) }
	if !plaintext(st.Alerts.SMTP.Password) && !plaintext(st.General.AccessPassword) && !plaintext(st.Backups.Password) {
		return nil
	}
	if err := s.sealSettings(&st); err != nil {
		return err
	}
	return s.PutSetting(ctx, "settings", st)
}

// LoadSettings returns the settings document, filling in defaults. Secret
// fields are returned decrypted, exactly as callers stored them.
func (s *Store) LoadSettings(ctx context.Context) (model.Settings, error) {
	st := model.DefaultSettings()
	err := s.GetSetting(ctx, "settings", &st)
	if err == nil {
		s.openSettings(&st)
	}
	if errors.Is(err, ErrNotFound) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	def := model.DefaultSettings()
	if st.General.DefaultIntervalSecs <= 0 {
		st.General.DefaultIntervalSecs = def.General.DefaultIntervalSecs
	}
	if st.General.DefaultTimeoutSecs <= 0 {
		st.General.DefaultTimeoutSecs = def.General.DefaultTimeoutSecs
	}
	if st.General.MaxConcurrentChecks <= 0 {
		st.General.MaxConcurrentChecks = def.General.MaxConcurrentChecks
	}
	if st.General.MinIntervalSecs <= 0 {
		st.General.MinIntervalSecs = def.General.MinIntervalSecs
	}
	if st.General.WallboardRefreshSecs <= 0 {
		st.General.WallboardRefreshSecs = def.General.WallboardRefreshSecs
	}
	if st.Alerts.FailureThreshold <= 0 {
		st.Alerts.FailureThreshold = def.Alerts.FailureThreshold
	}
	if st.Alerts.CooldownMinutes < 0 {
		st.Alerts.CooldownMinutes = 0
	}
	if st.Alerts.CertWarnDays <= 0 {
		st.Alerts.CertWarnDays = def.Alerts.CertWarnDays
	}
	if st.Alerts.Recipients == nil {
		st.Alerts.Recipients = []string{}
	}
	if st.Alerts.SMTP.Security == "" {
		st.Alerts.SMTP.Security = "starttls"
	}
	if st.Retention.RawDays <= 0 {
		st.Retention.RawDays = def.Retention.RawDays
	}
	if st.Retention.HostDays <= 0 {
		st.Retention.HostDays = def.Retention.HostDays
	}
	if st.General.AccentColor == "" {
		st.General.AccentColor = def.General.AccentColor
	}
	if st.General.Theme == "" {
		st.General.Theme = def.General.Theme
	}
	if st.General.UpdateRepo == "" {
		st.General.UpdateRepo = def.General.UpdateRepo
	}
	// Installs older than the header indicators have no rules stored, and
	// normalising on the way out seeds them the defaults without anybody
	// having to open the settings page first.
	st.Indicators = model.NormalizeIndicators(st.Indicators)
	return st, nil
}

// SaveSettings stores the settings document. Secret fields (SMTP password,
// LAN access password) are encrypted before they are written.
func (s *Store) SaveSettings(ctx context.Context, st model.Settings) error {
	if err := s.sealSettings(&st); err != nil {
		return err
	}
	return s.PutSetting(ctx, "settings", st)
}

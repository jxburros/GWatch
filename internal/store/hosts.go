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
)

// Schema for hardware health: the machines registered to report in, and the
// readings themselves. Applied by migrate() after the base schema.
//
// Readings are keyed by a host key ("local", "agent:<id>", "url:<check id>")
// rather than by a check, because one machine's readings are worth keeping
// whether or not a check happens to be pointed at it, and because two checks
// may watch different aspects of the same machine.
const hostSchema = `
CREATE TABLE IF NOT EXISTS agents (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  node_id INTEGER REFERENCES nodes(id) ON DELETE SET NULL,
  token_hash TEXT NOT NULL UNIQUE,
  prefix TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 1,
  created_by TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  revoked_at TEXT,
  last_seen_at TEXT,
  last_addr TEXT NOT NULL DEFAULT '',
  last_version TEXT NOT NULL DEFAULT '',
  hostname TEXT NOT NULL DEFAULT '',
  os TEXT NOT NULL DEFAULT '',
  arch TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS host_samples (
  host_key TEXT NOT NULL,
  ts INTEGER NOT NULL,                  -- unix milliseconds
  cpu_pct REAL,
  mem_pct REAL,
  swap_pct REAL,
  disk_pct REAL,
  load_per_core REAL,
  net_rx_bps REAL,
  net_tx_bps REAL,
  disk_read_bps REAL,
  disk_write_bps REAL,
  metrics TEXT NOT NULL DEFAULT '{}',
  PRIMARY KEY (host_key, ts)
);
CREATE INDEX IF NOT EXISTS idx_host_samples_ts ON host_samples(ts);
`

const agentCols = `id, name, node_id, prefix, enabled, created_by, created_at, revoked_at, last_seen_at, last_addr, last_version, hostname, os, arch`

func scanAgent(sc interface{ Scan(...any) error }) (model.Agent, error) {
	var a model.Agent
	var nodeID sql.NullInt64
	var createdAt string
	var revokedAt, lastSeenAt sql.NullString
	err := sc.Scan(&a.ID, &a.Name, &nodeID, &a.Prefix, &a.Enabled, &a.CreatedBy, &createdAt,
		&revokedAt, &lastSeenAt, &a.LastAddr, &a.LastVersion, &a.Hostname, &a.OS, &a.Arch)
	if err != nil {
		return model.Agent{}, err
	}
	if nodeID.Valid {
		id := nodeID.Int64
		a.NodeID = &id
	}
	if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
		a.CreatedAt = t.Local()
	}
	a.RevokedAt = parseTime(revokedAt)
	a.LastSeenAt = parseTime(lastSeenAt)
	return a, nil
}

// CreateAgent registers a machine that may push its readings. Only the token's
// hash is stored: the token itself is shown once, at creation, and GWatch
// cannot recover it afterwards.
func (s *Store) CreateAgent(ctx context.Context, name string, nodeID *int64, prefix, tokenHash, createdBy string) (model.Agent, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return model.Agent{}, errors.New("a name is required")
	}
	if tokenHash == "" {
		return model.Agent{}, errors.New("a token is required")
	}
	res, err := s.Exec(ctx, `INSERT INTO agents(name, node_id, token_hash, prefix, enabled, created_by, created_at)
		VALUES (?,?,?,?,1,?,?)`, name, nodeID, tokenHash, prefix, createdBy, fmtTime(time.Now()))
	if err != nil {
		if isUniqueViolation(err) {
			return model.Agent{}, ErrDuplicate
		}
		return model.Agent{}, err
	}
	id, _ := res.LastInsertId()
	return s.GetAgent(ctx, id)
}

// GetAgent returns one registered machine.
func (s *Store) GetAgent(ctx context.Context, id int64) (model.Agent, error) {
	row := s.reader.QueryRowContext(ctx, `SELECT `+agentCols+` FROM agents WHERE id = ?`, id)
	a, err := scanAgent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Agent{}, ErrNotFound
	}
	return a, err
}

// ListAgents returns every registered machine, newest first.
func (s *Store) ListAgents(ctx context.Context) ([]model.Agent, error) {
	rows, err := s.reader.QueryContext(ctx, `SELECT `+agentCols+` FROM agents ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Agent{}
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AgentByTokenHash resolves a presented token. A revoked or disabled agent is
// reported as not found, so a stolen token stops working the moment it is
// revoked rather than at the next restart.
func (s *Store) AgentByTokenHash(ctx context.Context, tokenHash string) (model.Agent, error) {
	row := s.reader.QueryRowContext(ctx,
		`SELECT `+agentCols+` FROM agents WHERE token_hash = ? AND revoked_at IS NULL AND enabled = 1`, tokenHash)
	a, err := scanAgent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Agent{}, ErrNotFound
	}
	return a, err
}

// UpdateAgent changes the display name, the node the readings belong to and
// whether the agent is accepted. The token is never changed: reissuing one
// means revoking this agent and registering a new one.
func (s *Store) UpdateAgent(ctx context.Context, id int64, name string, nodeID *int64, enabled bool) (model.Agent, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return model.Agent{}, errors.New("a name is required")
	}
	res, err := s.Exec(ctx, `UPDATE agents SET name = ?, node_id = ?, enabled = ? WHERE id = ?`,
		name, nodeID, enabled, id)
	if err != nil {
		return model.Agent{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return model.Agent{}, ErrNotFound
	}
	return s.GetAgent(ctx, id)
}

// RevokeAgent stops accepting readings from a machine. The row is kept so the
// audit trail still explains where past readings came from.
func (s *Store) RevokeAgent(ctx context.Context, id int64) error {
	res, err := s.Exec(ctx, `UPDATE agents SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`,
		fmtTime(time.Now()), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Either there is no such agent or it was already revoked; tell the
		// two apart so the caller can report the first as an error.
		if _, err := s.GetAgent(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// DeleteAgent removes a machine and every reading it ever sent.
func (s *Store) DeleteAgent(ctx context.Context, id int64) error {
	key := model.AgentHostKey(id)
	return s.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM host_samples WHERE host_key = ?`, key); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM agents WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// TouchAgent records that a machine reported in, along with what it says it
// is. The identity columns come from the agent's own reading, so they describe
// the machine holding the token rather than anything GWatch went and looked up.
func (s *Store) TouchAgent(ctx context.Context, id int64, addr, version, hostname, os, arch string) error {
	_, err := s.Exec(ctx, `UPDATE agents SET last_seen_at = ?, last_addr = ?, last_version = ?,
		hostname = ?, os = ?, arch = ? WHERE id = ?`,
		fmtTime(time.Now()), addr, version, hostname, os, arch, id)
	return err
}

// ---- readings ----

// SaveHostSample stores one reading. A reading that arrives with the same
// timestamp as one already stored replaces it, so a retried push cannot
// produce two points a chart would draw as a spike.
func (s *Store) SaveHostSample(ctx context.Context, sample model.HostSample) error {
	if strings.TrimSpace(sample.Key) == "" {
		return errors.New("a host key is required")
	}
	if sample.Timestamp.IsZero() {
		sample.Timestamp = time.Now()
	}
	sample.Metrics.Key = sample.Key
	blob, err := json.Marshal(sample.Metrics)
	if err != nil {
		return fmt.Errorf("encode metrics: %w", err)
	}
	_, err = s.Exec(ctx, `INSERT INTO host_samples
		(host_key, ts, cpu_pct, mem_pct, swap_pct, disk_pct, load_per_core, net_rx_bps, net_tx_bps, disk_read_bps, disk_write_bps, metrics)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(host_key, ts) DO UPDATE SET
			cpu_pct = excluded.cpu_pct, mem_pct = excluded.mem_pct, swap_pct = excluded.swap_pct,
			disk_pct = excluded.disk_pct, load_per_core = excluded.load_per_core,
			net_rx_bps = excluded.net_rx_bps, net_tx_bps = excluded.net_tx_bps,
			disk_read_bps = excluded.disk_read_bps, disk_write_bps = excluded.disk_write_bps,
			metrics = excluded.metrics`,
		sample.Key, sample.Timestamp.UnixMilli(),
		sample.CPUPct, sample.MemPct, sample.SwapPct, sample.DiskPct, sample.LoadPerCore,
		sample.NetRxBytesSec, sample.NetTxBytesSec, sample.DiskReadBytes, sample.DiskWriteBytes,
		string(blob))
	return err
}

// LatestHostSample returns the newest reading for a machine.
func (s *Store) LatestHostSample(ctx context.Context, key string) (model.HostSample, error) {
	row := s.reader.QueryRowContext(ctx, `SELECT `+hostSampleCols+`
		FROM host_samples WHERE host_key = ? ORDER BY ts DESC LIMIT 1`, key)
	sample, err := scanHostSample(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.HostSample{}, ErrNotFound
	}
	return sample, err
}

// LatestHostSamples returns the newest reading for every machine that has one,
// keyed by host key. It is one query rather than one per machine so the
// hardware list stays a single round trip however many agents there are.
func (s *Store) LatestHostSamples(ctx context.Context) (map[string]model.HostSample, error) {
	rows, err := s.reader.QueryContext(ctx, `SELECT `+hostSampleCols+` FROM host_samples
		WHERE (host_key, ts) IN (SELECT host_key, MAX(ts) FROM host_samples GROUP BY host_key)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]model.HostSample{}
	for rows.Next() {
		sample, err := scanHostSample(rows)
		if err != nil {
			return nil, err
		}
		out[sample.Key] = sample
	}
	return out, rows.Err()
}

// HostSamples returns the readings for a machine between two times, oldest
// first. The full snapshot is left out: a chart wants the numeric series, and
// decoding several thousand snapshots to draw a line is pure waste.
func (s *Store) HostSamples(ctx context.Context, key string, from, to time.Time, limit int) ([]model.HostSample, error) {
	if limit <= 0 || limit > 20000 {
		limit = 20000
	}
	rows, err := s.reader.QueryContext(ctx, `SELECT host_key, ts, cpu_pct, mem_pct, swap_pct, disk_pct,
		load_per_core, net_rx_bps, net_tx_bps, disk_read_bps, disk_write_bps
		FROM host_samples WHERE host_key = ? AND ts >= ? AND ts <= ? ORDER BY ts LIMIT ?`,
		key, from.UnixMilli(), to.UnixMilli(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.HostSample{}
	for rows.Next() {
		var sample model.HostSample
		var ms int64
		if err := rows.Scan(&sample.Key, &ms, &sample.CPUPct, &sample.MemPct, &sample.SwapPct,
			&sample.DiskPct, &sample.LoadPerCore, &sample.NetRxBytesSec, &sample.NetTxBytesSec,
			&sample.DiskReadBytes, &sample.DiskWriteBytes); err != nil {
			return nil, err
		}
		sample.Timestamp = time.UnixMilli(ms)
		out = append(out, sample)
	}
	return out, rows.Err()
}

// HostKeys lists every machine that has at least one stored reading. It
// includes keys whose agent has since been deleted, which is how the retention
// sweep finds readings to clear out.
func (s *Store) HostKeys(ctx context.Context) ([]string, error) {
	rows, err := s.reader.QueryContext(ctx, `SELECT DISTINCT host_key FROM host_samples ORDER BY host_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		out = append(out, key)
	}
	return out, rows.Err()
}

// PruneHostSamples deletes readings older than cutoff and reports how many
// rows went.
func (s *Store) PruneHostSamples(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.Exec(ctx, `DELETE FROM host_samples WHERE ts < ?`, cutoff.UnixMilli())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// DeleteHostSamples removes every reading for one machine.
func (s *Store) DeleteHostSamples(ctx context.Context, key string) error {
	_, err := s.Exec(ctx, `DELETE FROM host_samples WHERE host_key = ?`, key)
	return err
}

const hostSampleCols = `host_key, ts, cpu_pct, mem_pct, swap_pct, disk_pct, load_per_core, net_rx_bps, net_tx_bps, disk_read_bps, disk_write_bps, metrics`

func scanHostSample(sc interface{ Scan(...any) error }) (model.HostSample, error) {
	var sample model.HostSample
	var ms int64
	var blob string
	err := sc.Scan(&sample.Key, &ms, &sample.CPUPct, &sample.MemPct, &sample.SwapPct, &sample.DiskPct,
		&sample.LoadPerCore, &sample.NetRxBytesSec, &sample.NetTxBytesSec,
		&sample.DiskReadBytes, &sample.DiskWriteBytes, &blob)
	if err != nil {
		return model.HostSample{}, err
	}
	sample.Timestamp = time.UnixMilli(ms)
	if blob != "" {
		// A snapshot that cannot be decoded (a row written by a newer build,
		// say) still has usable numeric columns, so the row is kept rather
		// than failing the whole query.
		_ = json.Unmarshal([]byte(blob), &sample.Metrics)
	}
	sample.Metrics.Key = sample.Key
	return sample, nil
}

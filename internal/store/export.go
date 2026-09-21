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

// This file is the store's side of moving a whole installation: what the
// backup package exports and restores beyond nodes and history, and what
// "gwatch migrate-db" copies from the SQLite file into a server database.
// The records carry the stored credential digests (a password hash, a token
// hash), never a secret in the clear: that is all the database holds too.

// UserRecord is an account with its stored password hash.
type UserRecord struct {
	model.User
	PasswordHash string `json:"passwordHash"`
}

// APIKeyRecord is an API key with the digest it is looked up by.
type APIKeyRecord struct {
	model.APIKey
	KeyHash string `json:"keyHash"`
}

// AgentRecord is a registered machine with its token digest.
type AgentRecord struct {
	model.Agent
	TokenHash string `json:"tokenHash"`
}

// ExportUsers returns every account with its password hash.
func (s *Store) ExportUsers(ctx context.Context) ([]UserRecord, error) {
	rows, err := s.query(ctx, "SELECT "+userCols+", password_hash FROM users ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UserRecord{}
	for rows.Next() {
		var r UserRecord
		var created, updated string
		var lastLogin sql.NullString
		if err := rows.Scan(&r.ID, &r.Username, &r.Role, &created, &updated, &lastLogin, &r.PasswordHash); err != nil {
			return nil, err
		}
		r.CreatedAt, r.UpdatedAt = mustTime(created), mustTime(updated)
		r.LastLoginAt = parseTime(lastLogin)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ImportUser adds an account exported by ExportUsers, keeping its id when
// that id is free. An account with the same user name is already there is
// left exactly as it is, and false is returned: a restore must never
// overwrite the password of the administrator doing the restoring.
func (s *Store) ImportUser(ctx context.Context, r UserRecord) (bool, error) {
	if _, _, err := s.GetUserByName(ctx, r.Username); err == nil {
		return false, nil
	} else if !errors.Is(err, ErrNotFound) {
		return false, err
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now()
	}
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = r.CreatedAt
	}
	return true, s.insertWithID(ctx, "users", r.ID,
		[]string{"username", "password_hash", "role", "created_at", "updated_at", "last_login_at"},
		r.Username, r.PasswordHash, r.Role, fmtTime(r.CreatedAt), fmtTime(r.UpdatedAt), fmtTimePtr(r.LastLoginAt))
}

// ExportAPIKeys returns every key, revoked ones included, with its digest.
func (s *Store) ExportAPIKeys(ctx context.Context) ([]APIKeyRecord, error) {
	rows, err := s.query(ctx, "SELECT "+apiKeyCols+", key_hash FROM api_keys ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIKeyRecord{}
	for rows.Next() {
		var r APIKeyRecord
		var created string
		var lastUsed, revoked sql.NullString
		if err := rows.Scan(&r.ID, &r.Name, &r.Prefix, &r.Scope, &r.CreatedBy, &created, &lastUsed, &revoked, &r.KeyHash); err != nil {
			return nil, err
		}
		r.CreatedAt = mustTime(created)
		r.LastUsedAt, r.RevokedAt = parseTime(lastUsed), parseTime(revoked)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ImportAPIKey adds a key exported by ExportAPIKeys. A key with the same
// digest is already there is left alone and false is returned.
func (s *Store) ImportAPIKey(ctx context.Context, r APIKeyRecord) (bool, error) {
	var n int
	if err := s.queryRow(ctx, "SELECT COUNT(*) FROM api_keys WHERE key_hash = ?", r.KeyHash).Scan(&n); err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now()
	}
	return true, s.insertWithID(ctx, "api_keys", r.ID,
		[]string{"name", "prefix", "key_hash", "scope", "created_by", "created_at", "last_used_at", "revoked_at"},
		r.Name, r.Prefix, r.KeyHash, r.Scope, r.CreatedBy, fmtTime(r.CreatedAt), fmtTimePtr(r.LastUsedAt), fmtTimePtr(r.RevokedAt))
}

// ExportAgents returns every registered machine with its token digest.
func (s *Store) ExportAgents(ctx context.Context) ([]AgentRecord, error) {
	rows, err := s.query(ctx, "SELECT "+agentCols+", token_hash FROM agents ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AgentRecord{}
	for rows.Next() {
		var r AgentRecord
		var nodeID sql.NullInt64
		var enabled int
		var created string
		var revoked, lastSeen sql.NullString
		if err := rows.Scan(&r.ID, &r.Name, &nodeID, &r.Prefix, &enabled, &r.CreatedBy, &created,
			&revoked, &lastSeen, &r.LastAddr, &r.LastVersion, &r.Hostname, &r.OS, &r.Arch, &r.TokenHash); err != nil {
			return nil, err
		}
		r.NodeID = int64Ptr(nodeID)
		r.Enabled = enabled == 1
		r.CreatedAt = mustTime(created)
		r.RevokedAt, r.LastSeenAt = parseTime(revoked), parseTime(lastSeen)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ImportAgent adds a machine exported by ExportAgents, keeping its id: the
// readings it sent are keyed "agent:<id>", so a new id would orphan them. An
// agent with the same token digest is left alone and false is returned.
func (s *Store) ImportAgent(ctx context.Context, r AgentRecord) (bool, error) {
	var n int
	if err := s.queryRow(ctx, "SELECT COUNT(*) FROM agents WHERE token_hash = ?", r.TokenHash).Scan(&n); err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now()
	}
	return true, s.insertWithID(ctx, "agents", r.ID,
		[]string{"name", "node_id", "token_hash", "prefix", "enabled", "created_by", "created_at", "revoked_at", "last_seen_at", "last_addr", "last_version", "hostname", "os", "arch"},
		r.Name, nullInt64(r.NodeID), r.TokenHash, r.Prefix, boolInt(r.Enabled), r.CreatedBy, fmtTime(r.CreatedAt), fmtTimePtr(r.RevokedAt), fmtTimePtr(r.LastSeenAt),
		r.LastAddr, r.LastVersion, r.Hostname, r.OS, r.Arch)
}

// ReplaceWallboards removes every wallboard and writes the given ones in
// their place, ids and share tokens included: a projected address typed
// into a display has to keep working after a restore.
func (s *Store) ReplaceWallboards(ctx context.Context, boards []model.Wallboard) error {
	return s.writeTx(ctx, func(tx *wtx) error {
		if _, err := tx.exec(ctx, "DELETE FROM wallboards"); err != nil {
			return err
		}
		for _, b := range boards {
			b.Layout.Normalize()
			if b.Panels == nil {
				b.Panels = []model.WallPanel{}
			}
			if b.CreatedAt.IsZero() {
				b.CreatedAt = time.Now()
			}
			if b.UpdatedAt.IsZero() {
				b.UpdatedAt = b.CreatedAt
			}
			cols := []string{"id", "name", "sort_order", "layout", "panels", "share_enabled", "share_token", "created_at", "updated_at"}
			if _, err := tx.exec(ctx, insertValues("wallboards", cols),
				b.ID, b.Name, b.SortOrder, jsonString(b.Layout), jsonString(b.Panels), boolInt(b.Share.Enabled), b.Share.Token, fmtTime(b.CreatedAt), fmtTime(b.UpdatedAt)); err != nil {
				return fmt.Errorf("wallboard %q: %w", b.Name, err)
			}
		}
		return s.d.syncSequence(ctx, tx, "wallboards")
	})
}

// insertWithID inserts a row into a table with a generated id column,
// keeping the id the row had if it is given and still free, and letting the
// database pick one otherwise.
func (s *Store) insertWithID(ctx context.Context, table string, id int64, cols []string, args ...any) error {
	return s.writeTx(ctx, func(tx *wtx) error {
		if id > 0 {
			var n int
			if err := tx.queryRow(ctx, "SELECT COUNT(*) FROM "+table+" WHERE id = ?", id).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				if _, err := tx.exec(ctx, insertValues(table, append([]string{"id"}, cols...)), append([]any{id}, args...)...); err != nil {
					return err
				}
				return s.d.syncSequence(ctx, tx, table)
			}
		}
		_, err := tx.exec(ctx, insertValues(table, cols), args...)
		return err
	})
}

// AllHostSamples streams every hardware reading to fn, oldest first per
// machine.
func (s *Store) AllHostSamples(ctx context.Context, fn func(model.HostSample) error) error {
	rows, err := s.query(ctx, "SELECT "+hostSampleCols+" FROM host_samples ORDER BY host_key, ts")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		sample, err := scanHostSample(rows)
		if err != nil {
			return err
		}
		if err := fn(sample); err != nil {
			return err
		}
	}
	return rows.Err()
}

// InsertHostSamplesBatch writes readings in one transaction, replacing any
// with the same machine and timestamp.
func (s *Store) InsertHostSamplesBatch(ctx context.Context, samples []model.HostSample) error {
	q := insertValues("host_samples", hostSampleColList) + " " + s.d.upsertClause([]string{"host_key", "ts"}, hostSampleColList)
	return s.writeTx(ctx, func(tx *wtx) error {
		for _, sample := range samples {
			if sample.Key == "" {
				continue
			}
			sample.Metrics.Key = sample.Key
			blob, err := json.Marshal(sample.Metrics)
			if err != nil {
				return err
			}
			if _, err := tx.exec(ctx, q, sample.Key, sample.Timestamp.UnixMilli(),
				sample.CPUPct, sample.MemPct, sample.SwapPct, sample.DiskPct, sample.LoadPerCore,
				sample.NetRxBytesSec, sample.NetTxBytesSec, sample.DiskReadBytes, sample.DiskWriteBytes, string(blob)); err != nil {
				return err
			}
		}
		return nil
	})
}

// IsEmpty reports whether the database holds no GWatch data at all: no
// nodes, settings, accounts, keys, agents, boards, events or results. It is
// what "gwatch migrate-db" checks before copying into a target.
func (s *Store) IsEmpty(ctx context.Context) (bool, error) {
	for _, table := range []string{"nodes", "settings", "users", "api_keys", "agents", "wallboards", "dashboards", "events", "results", "host_samples"} {
		var n int
		if err := s.queryRow(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
			return false, fmt.Errorf("count %s: %w", table, err)
		}
		if n > 0 {
			return false, nil
		}
	}
	return true, nil
}

// ClearEverything empties every table except schema_version, for
// "gwatch migrate-db --replace". Unlike ClearAll, which a restore uses and
// which leaves accounts, keys, agents and readings alone, this leaves
// nothing behind.
func (s *Store) ClearEverything(ctx context.Context) error {
	return s.writeTx(ctx, func(tx *wtx) error {
		// Children before parents: MySQL will not delete a row another one
		// still points at unless the constraint cascades, and not every one
		// of these does.
		for _, t := range []string{"results", "rollups", "events", "host_samples", "agent_pairings", "agents", "sessions", "api_keys", "users",
			"rule_state", "rules", "triggers", "endpoints", "maintenance_windows", "dashboards", "wallboards", "check_state", "checks", "nodes", "settings"} {
			if _, err := tx.exec(ctx, "DELETE FROM "+t); err != nil {
				return fmt.Errorf("clear %s: %w", t, err)
			}
		}
		return nil
	})
}

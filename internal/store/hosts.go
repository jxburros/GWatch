package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/auth"
	"github.com/jxburros/GWatch/internal/model"
)

// Schema for the machines registered to report in, and the invitations to enrol
// them. Applied by migrate() after the base schema.
//
// A machine's readings are not stored here. They used to be, in a table of their
// own keyed by host; a machine is a node with a hardware check, and what that
// check measures is recorded like what any other check measures — in results,
// with its retention, its history and its backups (see hostseries.go).
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

-- Invitations to enrol a machine. A row survives being redeemed, revoked or
-- expiring: it is what lets the Hardware page say where an agent came from,
-- and keeping it is also what makes "already used" a state the redemption
-- query can see rather than an absence it has to guess at.
CREATE TABLE IF NOT EXISTS agent_pairings (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  node_id INTEGER REFERENCES nodes(id) ON DELETE SET NULL,
  code_hash TEXT NOT NULL UNIQUE,
  created_by TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  redeemed_at TEXT,
  revoked_at TEXT,
  agent_id INTEGER REFERENCES agents(id) ON DELETE SET NULL,
  redeemed_addr TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_agent_pairings_expires ON agent_pairings(expires_at);
`

const agentCols = `id, name, node_id, prefix, enabled, created_by, created_at, revoked_at, last_seen_at, last_addr, last_version, hostname, os, arch`

// agentSelectCols is agentCols plus what the interface needs and a backup does
// not: whether a token can be read back, whether a rotation is waiting, and
// the settings GWatch holds for the machine. The sealed tokens themselves are
// never selected here; only RevealAgentToken reads them.
const agentSelectCols = agentCols + `,
	CASE WHEN token_sealed = '' THEN 0 ELSE 1 END, CASE WHEN pending_token_hash = '' THEN 0 ELSE 1 END,
	pending_since, collect, inventory, checked_in_at, collector, collector_allowed, collector_seen_at`

func scanAgent(sc interface{ Scan(...any) error }) (model.Agent, error) {
	return scanAgentWith(sc)
}

// scanAgentWith is scanAgent for a query that selects further columns after
// agentSelectCols; extra receives them.
func scanAgentWith(sc interface{ Scan(...any) error }, extra ...any) (model.Agent, error) {
	var a model.Agent
	var nodeID sql.NullInt64
	var createdAt string
	var revokedAt, lastSeenAt, pendingSince, checkedInAt, collectorSeen sql.NullString
	var viewable, pending, collector, collectorAllowed int
	var collect, inventory string
	dest := append([]any{&a.ID, &a.Name, &nodeID, &a.Prefix, &a.Enabled, &a.CreatedBy, &createdAt,
		&revokedAt, &lastSeenAt, &a.LastAddr, &a.LastVersion, &a.Hostname, &a.OS, &a.Arch,
		&viewable, &pending, &pendingSince, &collect, &inventory, &checkedInAt,
		&collector, &collectorAllowed, &collectorSeen}, extra...)
	err := sc.Scan(dest...)
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
	a.TokenViewable = viewable == 1
	a.RotationPending = pending == 1
	a.RotationSince = parseTime(pendingSince)
	a.CheckedInAt = parseTime(checkedInAt)
	a.Collector = collector == 1
	a.CollectorAllowed = collectorAllowed == 1
	a.CollectorSeenAt = parseTime(collectorSeen)
	if collect != "" {
		_ = json.Unmarshal([]byte(collect), &a.Collect)
	}
	if inventory != "" {
		var inv model.Inventory
		if json.Unmarshal([]byte(inventory), &inv) == nil {
			a.Inventory = &inv
		}
	}
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
	id, err := s.insertID(ctx, `INSERT INTO agents(name, node_id, token_hash, prefix, enabled, created_by, created_at)
		VALUES (?,?,?,?,1,?,?)`, name, nodeID, tokenHash, prefix, createdBy, fmtTime(time.Now()))
	if err != nil {
		if s.d.isUniqueViolation(err) {
			return model.Agent{}, ErrDuplicate
		}
		return model.Agent{}, err
	}
	return s.GetAgent(ctx, id)
}

// GetAgent returns one registered machine.
func (s *Store) GetAgent(ctx context.Context, id int64) (model.Agent, error) {
	row := s.queryRow(ctx, `SELECT `+agentSelectCols+` FROM agents WHERE id = ?`, id)
	a, err := scanAgent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Agent{}, ErrNotFound
	}
	return a, err
}

// ListAgents returns every registered machine, newest first.
func (s *Store) ListAgents(ctx context.Context) ([]model.Agent, error) {
	rows, err := s.query(ctx, `SELECT `+agentSelectCols+` FROM agents ORDER BY id DESC`)
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
	row := s.queryRow(ctx,
		`SELECT `+agentSelectCols+` FROM agents WHERE token_hash = ? AND revoked_at IS NULL AND enabled = 1`, tokenHash)
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
	res, err := s.exec(ctx, `UPDATE agents SET name = ?, node_id = ?, enabled = ? WHERE id = ?`,
		name, nodeID, boolInt(enabled), id)
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
	res, err := s.exec(ctx, `UPDATE agents SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`,
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

// DeleteAgent removes a machine's registration: its token, and the record of how
// it was enrolled. The node it reported for and everything its hardware check
// recorded stay, since that is the node's history and is deleted with the node.
func (s *Store) DeleteAgent(ctx context.Context, id int64) error {
	return s.writeTx(ctx, func(tx *wtx) error {
		res, err := tx.exec(ctx, `DELETE FROM agents WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// AgentByAnyTokenHash resolves a presented token against both the agent's
// current token and a replacement waiting to be picked up, and says which one
// it was. A revoked or disabled agent is not found, whichever token is used.
func (s *Store) AgentByAnyTokenHash(ctx context.Context, tokenHash string) (agent model.Agent, pending bool, err error) {
	row := s.queryRow(ctx,
		`SELECT `+agentSelectCols+`, CASE WHEN token_hash = ? THEN 0 ELSE 1 END FROM agents
		 WHERE (token_hash = ? OR (pending_token_hash <> '' AND pending_token_hash = ?)) AND revoked_at IS NULL AND enabled = 1`,
		tokenHash, tokenHash, tokenHash)
	var which int
	a, err := scanAgentWith(row, &which)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Agent{}, false, ErrNotFound
	}
	return a, which == 1, err
}

// SetAgentToken keeps a copy of the agent's token, sealed with the server's own
// key, so that an administrator can read it back and compare it with the one in
// the machine's configuration file. The digest it is authenticated by is stored
// separately and does not depend on this.
func (s *Store) SetAgentToken(ctx context.Context, id int64, token string) error {
	sealed, err := s.sealSecret(token)
	if err != nil {
		return err
	}
	res, err := s.exec(ctx, `UPDATE agents SET token_sealed = ? WHERE id = ?`, sealed, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AgentTokens is what an administrator may be shown of an agent's credentials.
// Either is empty when GWatch holds no readable copy.
type AgentTokens struct {
	Current string
	Pending string
}

// RevealAgentToken opens the sealed copies of an agent's tokens. A token that
// was stored before copies were kept, or that came back from a backup, is
// simply not there to open: the answer is empty, not an error.
func (s *Store) RevealAgentToken(ctx context.Context, id int64) (AgentTokens, error) {
	var current, pending string
	err := s.queryRow(ctx, `SELECT token_sealed, pending_token_sealed FROM agents WHERE id = ?`, id).Scan(&current, &pending)
	if errors.Is(err, sql.ErrNoRows) {
		return AgentTokens{}, ErrNotFound
	}
	if err != nil {
		return AgentTokens{}, err
	}
	var out AgentTokens
	if current != "" {
		if out.Current, err = s.secrets.Open(current); err != nil {
			return AgentTokens{}, fmt.Errorf("open the stored token: %w", err)
		}
	}
	if pending != "" {
		if out.Pending, err = s.secrets.Open(pending); err != nil {
			return AgentTokens{}, fmt.Errorf("open the stored replacement token: %w", err)
		}
	}
	return out, nil
}

// StartAgentRotation records a replacement token for a machine. With retireOld
// false the old token keeps working until the new one is first used, which is
// what lets the agent pick it up by itself and switch over without a gap. With
// retireOld true the old token stops working at once and the new one is the
// only one, for the case where the old one may have leaked.
func (s *Store) StartAgentRotation(ctx context.Context, id int64, token, prefix string, retireOld bool) error {
	sealed, err := s.sealSecret(token)
	if err != nil {
		return err
	}
	hash := auth.HashToken(token)
	var res sql.Result
	if retireOld {
		res, err = s.exec(ctx, `UPDATE agents SET token_hash = ?, token_sealed = ?, prefix = ?,
			pending_token_hash = '', pending_token_sealed = '', pending_since = NULL WHERE id = ?`,
			hash, sealed, prefix, id)
	} else {
		res, err = s.exec(ctx, `UPDATE agents SET pending_token_hash = ?, pending_token_sealed = ?, pending_since = ?
			WHERE id = ?`, hash, sealed, fmtTime(time.Now()), id)
	}
	if err != nil {
		if s.d.isUniqueViolation(err) {
			return ErrDuplicate
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CancelAgentRotation drops a replacement token that has not been used.
func (s *Store) CancelAgentRotation(ctx context.Context, id int64) error {
	_, err := s.exec(ctx, `UPDATE agents SET pending_token_hash = '', pending_token_sealed = '', pending_since = NULL WHERE id = ?`, id)
	return err
}

// PromoteAgentToken makes the replacement the agent's token, which is what the
// first request made with it does. The old token stops working in the same
// statement; the condition on the digest means only the request that actually
// presented the replacement can do this.
func (s *Store) PromoteAgentToken(ctx context.Context, id int64, pendingHash, prefix string) error {
	_, err := s.exec(ctx, `UPDATE agents SET token_hash = pending_token_hash, token_sealed = pending_token_sealed,
		prefix = ?, pending_token_hash = '', pending_token_sealed = '', pending_since = NULL
		WHERE id = ? AND pending_token_hash = ? AND pending_token_hash <> ''`, prefix, id, pendingHash)
	return err
}

// collectColumn is a selection as it is stored: empty for "nothing chosen".
func collectColumn(sel model.CollectSelection) string {
	sel = sel.Normalize()
	if sel.IsZero() {
		return ""
	}
	return jsonString(sel)
}

// SetAgentCollect stores the selection an administrator made for a machine.
func (s *Store) SetAgentCollect(ctx context.Context, id int64, sel model.CollectSelection) error {
	res, err := s.exec(ctx, `UPDATE agents SET collect = ? WHERE id = ?`, collectColumn(sel), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RecordAgentCheckIn notes that a machine asked for its settings, and keeps
// what it said it could collect and whether its own configuration allows it to
// act as a remote collector. A check-in that carries no inventory leaves the
// last one in place.
func (s *Store) RecordAgentCheckIn(ctx context.Context, id int64, inv *model.Inventory, collectorAllowed bool) error {
	if inv == nil {
		_, err := s.exec(ctx, `UPDATE agents SET checked_in_at = ?, collector_allowed = ? WHERE id = ?`, fmtTime(time.Now()), boolInt(collectorAllowed), id)
		return err
	}
	_, err := s.exec(ctx, `UPDATE agents SET checked_in_at = ?, inventory = ?, collector_allowed = ? WHERE id = ?`,
		fmtTime(time.Now()), jsonString(inv.Normalize()), boolInt(collectorAllowed), id)
	return err
}

// SetAgentCollector stores an administrator's half of the agreement that
// makes a machine a remote collector. The machine's half is in its own
// agent.json and arrives with each check-in.
func (s *Store) SetAgentCollector(ctx context.Context, id int64, on bool) error {
	res, err := s.exec(ctx, `UPDATE agents SET collector = ? WHERE id = ?`, boolInt(on), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchCollector records that a collector sent results (or a heartbeat). It
// is kept apart from last_seen_at, which is the machine's hardware reading:
// an agent can be reporting its own health and have stopped collecting, and
// the agent card says which.
func (s *Store) TouchCollector(ctx context.Context, id int64, at time.Time) error {
	_, err := s.exec(ctx, `UPDATE agents SET collector_seen_at = ? WHERE id = ?`, fmtTime(at), id)
	return err
}

// TouchAgent records that a machine reported in, along with what it says it
// is. The identity columns come from the agent's own reading, so they describe
// the machine holding the token rather than anything GWatch went and looked up.
func (s *Store) TouchAgent(ctx context.Context, id int64, addr, version, hostname, os, arch string) error {
	_, err := s.exec(ctx, `UPDATE agents SET last_seen_at = ?, last_addr = ?, last_version = ?,
		hostname = ?, os = ?, arch = ? WHERE id = ?`,
		fmtTime(time.Now()), addr, version, hostname, os, arch, id)
	return err
}

// SetAgentIdentity records what a machine says it is. It is used at enrolment,
// where the machine describes itself before it has sent a reading, so unlike
// TouchAgent it deliberately leaves last_seen_at alone: being enrolled is not
// the same as having reported in, and the Hardware page draws that distinction.
func (s *Store) SetAgentIdentity(ctx context.Context, id int64, hostname, os, arch string) error {
	_, err := s.exec(ctx, `UPDATE agents SET hostname = ?, os = ?, arch = ? WHERE id = ?`,
		hostname, os, arch, id)
	return err
}

// ---- pairing codes ----

const pairingCols = `id, name, node_id, created_by, created_at, expires_at, redeemed_at, revoked_at, agent_id, redeemed_addr`

func scanPairing(sc interface{ Scan(...any) error }) (model.PairingCode, error) {
	var p model.PairingCode
	var nodeID, agentID sql.NullInt64
	var createdAt, expiresAt string
	var redeemedAt, revokedAt sql.NullString
	err := sc.Scan(&p.ID, &p.Name, &nodeID, &p.CreatedBy, &createdAt, &expiresAt,
		&redeemedAt, &revokedAt, &agentID, &p.RedeemedAddr)
	if err != nil {
		return model.PairingCode{}, err
	}
	p.NodeID = int64Ptr(nodeID)
	p.AgentID = int64Ptr(agentID)
	p.CreatedAt = mustTime(createdAt)
	p.ExpiresAt = mustTime(expiresAt)
	p.RedeemedAt = parseTime(redeemedAt)
	p.RevokedAt = parseTime(revokedAt)
	return p, nil
}

// scanPairingWithHash reads a row that carries the stored digest as well, for
// the one caller that has to check the digest rather than trust the lookup.
func scanPairingWithHash(sc interface{ Scan(...any) error }) (model.PairingCode, string, error) {
	var p model.PairingCode
	var nodeID, agentID sql.NullInt64
	var createdAt, expiresAt, hash string
	var redeemedAt, revokedAt sql.NullString
	err := sc.Scan(&p.ID, &p.Name, &nodeID, &p.CreatedBy, &createdAt, &expiresAt,
		&redeemedAt, &revokedAt, &agentID, &p.RedeemedAddr, &hash)
	if err != nil {
		return model.PairingCode{}, "", err
	}
	p.NodeID = int64Ptr(nodeID)
	p.AgentID = int64Ptr(agentID)
	p.CreatedAt = mustTime(createdAt)
	p.ExpiresAt = mustTime(expiresAt)
	p.RedeemedAt = parseTime(redeemedAt)
	p.RevokedAt = parseTime(revokedAt)
	return p, hash, nil
}

// SetPairingCollect keeps the readings an administrator chose while adding a
// machine, so that the agent starts with them when the code is redeemed.
func (s *Store) SetPairingCollect(ctx context.Context, id int64, sel model.CollectSelection) error {
	_, err := s.exec(ctx, `UPDATE agent_pairings SET collect = ? WHERE id = ?`, collectColumn(sel), id)
	return err
}

// PairingCollect returns what SetPairingCollect kept; zero means nothing chosen.
func (s *Store) PairingCollect(ctx context.Context, id int64) (model.CollectSelection, error) {
	var raw string
	err := s.queryRow(ctx, `SELECT collect FROM agent_pairings WHERE id = ?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return model.CollectSelection{}, ErrNotFound
	}
	if err != nil {
		return model.CollectSelection{}, err
	}
	var sel model.CollectSelection
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &sel)
	}
	return sel, nil
}

// CreatePairingCode records an invitation to enrol one machine. Only the hash
// of the code is stored, exactly as an agent token's is: the code is shown
// once, when it is minted, and a lost one is replaced rather than recovered.
func (s *Store) CreatePairingCode(ctx context.Context, name string, nodeID *int64, codeHash, createdBy string, expiresAt time.Time) (model.PairingCode, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return model.PairingCode{}, errors.New("a name is required")
	}
	if codeHash == "" {
		return model.PairingCode{}, errors.New("a code is required")
	}
	id, err := s.insertID(ctx, `INSERT INTO agent_pairings(name, node_id, code_hash, created_by, created_at, expires_at)
		VALUES (?,?,?,?,?,?)`, name, nodeID, codeHash, createdBy, fmtTime(time.Now()), fmtTime(expiresAt))
	if err != nil {
		if s.d.isUniqueViolation(err) {
			return model.PairingCode{}, ErrDuplicate
		}
		return model.PairingCode{}, err
	}
	return s.GetPairingCode(ctx, id)
}

// GetPairingCode returns one invitation.
func (s *Store) GetPairingCode(ctx context.Context, id int64) (model.PairingCode, error) {
	row := s.queryRow(ctx, `SELECT `+pairingCols+` FROM agent_pairings WHERE id = ?`, id)
	p, err := scanPairing(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.PairingCode{}, ErrNotFound
	}
	return p, err
}

// ListPairingCodes returns recent invitations, newest first. Codes that have
// been used or have run out are kept in the list rather than swept away: an
// administrator looking at the Hardware page wants to see that the machine
// they invited five minutes ago did in fact turn up.
func (s *Store) ListPairingCodes(ctx context.Context, limit int) ([]model.PairingCode, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.query(ctx,
		`SELECT `+pairingCols+` FROM agent_pairings ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.PairingCode{}
	for rows.Next() {
		p, err := scanPairing(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// RedeemPairingCode claims an invitation for the machine presenting it and
// reports what it was for.
//
// The claim is the whole point of this being one method rather than a lookup
// followed by an update the caller makes. Reading the row, deciding it is
// still good and marking it used all happen inside a single write
// transaction on the one writer connection, so two machines racing with the
// same code cannot both be told yes: the second finds redeemed_at already set
// and is turned away like any other spent code.
//
// Every way of failing — no such code, expired, already redeemed, cancelled —
// comes back as ErrNotFound, because the caller must not be able to tell them
// apart either. Which one it was is recorded in the audit trail, where only an
// administrator can read it.
//
// The stored digest is returned with the row so the caller can compare it
// against the one it computed in constant time. The comparison SQLite already
// did to find the row is not constant time, which does not matter for a digest
// of a high-entropy code, but the credential the caller ends up trusting
// should still be one it checked itself.
func (s *Store) RedeemPairingCode(ctx context.Context, codeHash string, now time.Time, addr string) (model.PairingCode, string, error) {
	var out model.PairingCode
	var stored string
	err := s.writeTx(ctx, func(tx *wtx) error {
		row := tx.queryRow(ctx, `SELECT `+pairingCols+`, code_hash FROM agent_pairings WHERE code_hash = ?`, codeHash)
		p, hash, err := scanPairingWithHash(row)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if !p.Pending(now) {
			return ErrNotFound
		}
		stored = hash
		// The guard on redeemed_at is belt and braces: the transaction above
		// already serialises the race, and this makes the statement itself
		// refuse to claim a code twice however it is reached.
		res, err := tx.exec(ctx,
			`UPDATE agent_pairings SET redeemed_at = ?, redeemed_addr = ? WHERE id = ? AND redeemed_at IS NULL`,
			fmtTime(now), addr, p.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		redeemed := now
		p.RedeemedAt = &redeemed
		p.RedeemedAddr = addr
		out = p
		return nil
	})
	if err != nil {
		return model.PairingCode{}, "", err
	}
	return out, stored, nil
}

// AttachPairingAgent notes which machine a redeemed invitation produced. It is
// a separate step because the agent does not exist until the code has been
// claimed, and claiming it is what must not be allowed to happen twice.
func (s *Store) AttachPairingAgent(ctx context.Context, id, agentID int64) error {
	_, err := s.exec(ctx, `UPDATE agent_pairings SET agent_id = ? WHERE id = ?`, agentID, id)
	return err
}

// RevokePairingCode cancels an invitation that has not been used. A code that
// was already redeemed, cancelled or expired is left exactly as it is, so
// cancelling cannot rewrite the record of an enrolment that already happened.
func (s *Store) RevokePairingCode(ctx context.Context, id int64) error {
	res, err := s.exec(ctx,
		`UPDATE agent_pairings SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL AND redeemed_at IS NULL`,
		fmtTime(time.Now()), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err := s.GetPairingCode(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

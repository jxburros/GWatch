package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/auth"
	"github.com/jxburros/GWatch/internal/model"
)

// ErrDuplicate is returned when a unique constraint rejects a write, for
// example a second account with the same user name.
var ErrDuplicate = errors.New("already exists")

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}

// ---- users ----

const userCols = `id, username, role, created_at, updated_at, last_login_at`

func scanUser(sc interface{ Scan(...any) error }) (model.User, error) {
	var u model.User
	var created, updated string
	var lastLogin sql.NullString
	if err := sc.Scan(&u.ID, &u.Username, &u.Role, &created, &updated, &lastLogin); err != nil {
		return u, err
	}
	u.CreatedAt, u.UpdatedAt = mustTime(created), mustTime(updated)
	u.LastLoginAt = parseTime(lastLogin)
	return u, nil
}

// CreateUser inserts an account. passwordHash must already be encoded by
// auth.HashPassword; the store never sees a plaintext password.
func (s *Store) CreateUser(ctx context.Context, username, passwordHash string, role auth.Role) (model.User, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return model.User{}, errors.New("a user name is required")
	}
	now := fmtTime(time.Now())
	id, err := s.insertID(ctx, `INSERT INTO users(username, password_hash, role, created_at, updated_at) VALUES (?,?,?,?,?)`,
		username, passwordHash, string(role), now, now)
	if err != nil {
		if isUniqueViolation(err) {
			return model.User{}, fmt.Errorf("a user named %q %w", username, ErrDuplicate)
		}
		return model.User{}, err
	}
	return s.GetUser(ctx, id)
}

// GetUser returns one account by id.
func (s *Store) GetUser(ctx context.Context, id int64) (model.User, error) {
	u, err := scanUser(s.queryRow(ctx, "SELECT "+userCols+" FROM users WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

// GetUserByName returns one account by user name (case-insensitive) together
// with its stored password hash.
func (s *Store) GetUserByName(ctx context.Context, username string) (model.User, string, error) {
	row := s.queryRow(ctx, "SELECT "+userCols+", password_hash FROM users WHERE username = ? COLLATE NOCASE", strings.TrimSpace(username))
	var u model.User
	var created, updated, hash string
	var lastLogin sql.NullString
	err := row.Scan(&u.ID, &u.Username, &u.Role, &created, &updated, &lastLogin, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return u, "", ErrNotFound
	}
	if err != nil {
		return u, "", err
	}
	u.CreatedAt, u.UpdatedAt = mustTime(created), mustTime(updated)
	u.LastLoginAt = parseTime(lastLogin)
	return u, hash, nil
}

// ListUsers returns every account, ordered by user name.
func (s *Store) ListUsers(ctx context.Context) ([]model.User, error) {
	rows, err := s.query(ctx, "SELECT "+userCols+" FROM users ORDER BY username COLLATE NOCASE")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// CountUsers returns how many accounts exist.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.queryRow(ctx, "SELECT COUNT(*) FROM users").Scan(&n)
	return n, err
}

// CountAdmins returns how many accounts hold the admin role.
func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.queryRow(ctx, "SELECT COUNT(*) FROM users WHERE role = ?", string(auth.RoleAdmin)).Scan(&n)
	return n, err
}

// UpdateUserRole changes an account's role.
func (s *Store) UpdateUserRole(ctx context.Context, id int64, role auth.Role) error {
	res, err := s.exec(ctx, "UPDATE users SET role = ?, updated_at = ? WHERE id = ?", string(role), fmtTime(time.Now()), id)
	return affected(res, err)
}

// SetUserPassword replaces an account's password hash and signs the account
// out everywhere: an old session must not survive a password change.
func (s *Store) SetUserPassword(ctx context.Context, id int64, passwordHash string) error {
	res, err := s.exec(ctx, "UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?", passwordHash, fmtTime(time.Now()), id)
	if err := affected(res, err); err != nil {
		return err
	}
	return s.DeleteUserSessions(ctx, id)
}

// TouchUserLogin records a successful sign-in.
func (s *Store) TouchUserLogin(ctx context.Context, id int64) error {
	_, err := s.exec(ctx, "UPDATE users SET last_login_at = ? WHERE id = ?", fmtTime(time.Now()), id)
	return err
}

// DeleteUser removes an account and its sessions.
func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	res, err := s.exec(ctx, "DELETE FROM users WHERE id = ?", id)
	return affected(res, err)
}

func affected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- sessions ----

// CreateSession stores a browser session for a user. tokenHash must be
// auth.HashToken of the cookie value; the raw token is never stored.
func (s *Store) CreateSession(ctx context.Context, tokenHash string, userID int64, remote string, expires time.Time) error {
	now := fmtTime(time.Now())
	_, err := s.exec(ctx, `INSERT INTO sessions(token_hash, user_id, created_at, expires_at, last_seen_at, remote) VALUES (?,?,?,?,?,?)`,
		tokenHash, userID, now, fmtTime(expires), now, remote)
	return err
}

// GetSession resolves a session token hash to its account. Expired sessions
// are deleted and reported as ErrNotFound. Every successful lookup slides the
// expiry forward by extend and refreshes last_seen_at.
func (s *Store) GetSession(ctx context.Context, tokenHash string, extend time.Duration) (model.User, error) {
	row := s.queryRow(ctx, "SELECT user_id, expires_at FROM sessions WHERE token_hash = ?", tokenHash)
	var userID int64
	var expires string
	if err := row.Scan(&userID, &expires); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.User{}, ErrNotFound
		}
		return model.User{}, err
	}
	if exp := mustTime(expires); exp.Before(time.Now()) {
		_, _ = s.exec(ctx, "DELETE FROM sessions WHERE token_hash = ?", tokenHash)
		return model.User{}, ErrNotFound
	}
	u, err := s.GetUser(ctx, userID)
	if err != nil {
		return u, err
	}
	now := time.Now()
	if extend > 0 {
		_, _ = s.exec(ctx, "UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE token_hash = ?", fmtTime(now), fmtTime(now.Add(extend)), tokenHash)
	} else {
		_, _ = s.exec(ctx, "UPDATE sessions SET last_seen_at = ? WHERE token_hash = ?", fmtTime(now), tokenHash)
	}
	return u, nil
}

// DeleteSession removes one session (sign out).
func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.exec(ctx, "DELETE FROM sessions WHERE token_hash = ?", tokenHash)
	return err
}

// DeleteUserSessions signs an account out of every browser.
func (s *Store) DeleteUserSessions(ctx context.Context, userID int64) error {
	_, err := s.exec(ctx, "DELETE FROM sessions WHERE user_id = ?", userID)
	return err
}

// PurgeExpiredSessions drops sessions that are past their expiry and reports
// how many rows were removed.
func (s *Store) PurgeExpiredSessions(ctx context.Context) (int64, error) {
	res, err := s.exec(ctx, "DELETE FROM sessions WHERE expires_at < ?", fmtTime(time.Now()))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// CountSessions returns how many sessions are stored (used by tests).
func (s *Store) CountSessions(ctx context.Context) (int, error) {
	var n int
	err := s.queryRow(ctx, "SELECT COUNT(*) FROM sessions").Scan(&n)
	return n, err
}

// ---- API keys ----

const apiKeyCols = `id, name, prefix, scope, created_by, created_at, last_used_at, revoked_at`

func scanAPIKey(sc interface{ Scan(...any) error }) (model.APIKey, error) {
	var k model.APIKey
	var created string
	var lastUsed, revoked sql.NullString
	if err := sc.Scan(&k.ID, &k.Name, &k.Prefix, &k.Scope, &k.CreatedBy, &created, &lastUsed, &revoked); err != nil {
		return k, err
	}
	k.CreatedAt = mustTime(created)
	k.LastUsedAt, k.RevokedAt = parseTime(lastUsed), parseTime(revoked)
	return k, nil
}

// CreateAPIKey stores a minted key. keyHash must be auth.HashToken of the key;
// the key itself is shown to the caller once and never stored.
func (s *Store) CreateAPIKey(ctx context.Context, name, prefix, keyHash string, scope auth.Scope, createdBy string) (model.APIKey, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return model.APIKey{}, errors.New("a name is required")
	}
	id, err := s.insertID(ctx, `INSERT INTO api_keys(name, prefix, key_hash, scope, created_by, created_at) VALUES (?,?,?,?,?,?)`,
		name, prefix, keyHash, string(scope), createdBy, fmtTime(time.Now()))
	if err != nil {
		if isUniqueViolation(err) {
			return model.APIKey{}, ErrDuplicate
		}
		return model.APIKey{}, err
	}
	return s.GetAPIKey(ctx, id)
}

// GetAPIKey returns one key by id.
func (s *Store) GetAPIKey(ctx context.Context, id int64) (model.APIKey, error) {
	k, err := scanAPIKey(s.queryRow(ctx, "SELECT "+apiKeyCols+" FROM api_keys WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return k, ErrNotFound
	}
	return k, err
}

// ListAPIKeys returns every key, newest first. Revoked keys are included so
// the settings page can show what was withdrawn and when.
func (s *Store) ListAPIKeys(ctx context.Context) ([]model.APIKey, error) {
	rows, err := s.query(ctx, "SELECT "+apiKeyCols+" FROM api_keys ORDER BY revoked_at IS NOT NULL, id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.APIKey{}
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// LookupAPIKey resolves a key hash. A revoked key is reported as ErrNotFound
// so callers cannot tell a withdrawn key from one that never existed.
func (s *Store) LookupAPIKey(ctx context.Context, keyHash string) (model.APIKey, error) {
	k, err := scanAPIKey(s.queryRow(ctx, "SELECT "+apiKeyCols+" FROM api_keys WHERE key_hash = ?", keyHash))
	if errors.Is(err, sql.ErrNoRows) {
		return k, ErrNotFound
	}
	if err != nil {
		return k, err
	}
	if k.Revoked() {
		return model.APIKey{}, ErrNotFound
	}
	return k, nil
}

// RevokeAPIKey marks a key as withdrawn. Revoking is preferred over deleting
// so the audit trail keeps the name of the key that was in use.
func (s *Store) RevokeAPIKey(ctx context.Context, id int64) error {
	res, err := s.exec(ctx, "UPDATE api_keys SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL", fmtTime(time.Now()), id)
	return affected(res, err)
}

// TouchAPIKey records that a key was just used. The write is skipped when the
// stored timestamp is already recent, so a busy integration does not turn
// every read into a database write.
func (s *Store) TouchAPIKey(ctx context.Context, id int64) error {
	now := time.Now()
	_, err := s.exec(ctx, "UPDATE api_keys SET last_used_at = ? WHERE id = ? AND (last_used_at IS NULL OR last_used_at < ?)",
		fmtTime(now), id, fmtTime(now.Add(-time.Minute)))
	return err
}

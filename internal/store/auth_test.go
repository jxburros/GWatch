package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/auth"
	"github.com/jxburros/GWatch/internal/model"
)

func TestUserCRUD(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	if n, err := s.CountUsers(ctx); err != nil || n != 0 {
		t.Fatalf("fresh database: %d %v", n, err)
	}
	hash, err := auth.HashPassword("hunter2hunter2")
	if err != nil {
		t.Fatal(err)
	}
	pat, err := s.CreateUser(ctx, "Pat", hash, auth.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if pat.ID == 0 || pat.Username != "Pat" || pat.Role != "admin" || pat.LastLoginAt != nil {
		t.Fatalf("created: %+v", pat)
	}
	// User names are unique, case-insensitively.
	if _, err := s.CreateUser(ctx, "pat", hash, auth.RoleViewer); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate user name: %v", err)
	}
	if _, err := s.CreateUser(ctx, "  ", hash, auth.RoleViewer); err == nil {
		t.Fatal("blank user name must be rejected")
	}
	sam, err := s.CreateUser(ctx, "sam", hash, auth.RoleViewer)
	if err != nil {
		t.Fatal(err)
	}

	// Lookup by name is case-insensitive and returns the hash.
	got, storedHash, err := s.GetUserByName(ctx, "PAT")
	if err != nil || got.ID != pat.ID || storedHash != hash {
		t.Fatalf("by name: %+v %v", got, err)
	}
	if _, _, err := s.GetUserByName(ctx, "nobody"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing user: %v", err)
	}

	users, err := s.ListUsers(ctx)
	if err != nil || len(users) != 2 || users[0].Username != "Pat" {
		t.Fatalf("list: %+v %v", users, err)
	}
	if n, _ := s.CountAdmins(ctx); n != 1 {
		t.Fatalf("admins: %d", n)
	}

	if err := s.UpdateUserRole(ctx, sam.ID, auth.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountAdmins(ctx); n != 2 {
		t.Fatalf("admins after promote: %d", n)
	}
	if err := s.UpdateUserRole(ctx, 9999, auth.RoleAdmin); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing user: %v", err)
	}

	if err := s.TouchUserLogin(ctx, pat.ID); err != nil {
		t.Fatal(err)
	}
	if u, _ := s.GetUser(ctx, pat.ID); u.LastLoginAt == nil {
		t.Fatal("last login not recorded")
	}

	if err := s.DeleteUser(ctx, sam.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUser(ctx, sam.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
	if _, err := s.GetUser(ctx, sam.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get deleted: %v", err)
	}
}

func TestSessions(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	hash, _ := auth.HashPassword("hunter2hunter2")
	u, err := s.CreateUser(ctx, "pat", hash, auth.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}

	tok, _ := auth.NewSessionToken()
	th := auth.HashToken(tok)
	if err := s.CreateSession(ctx, th, u.ID, "10.0.0.5:1234", time.Now().Add(auth.SessionLifetime)); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSession(ctx, th, auth.SessionLifetime)
	if err != nil || got.User.ID != u.ID || got.User.Role != string(auth.RoleAdmin) {
		t.Fatalf("get session: %+v %v", got, err)
	}
	if _, err := s.GetSession(ctx, auth.HashToken("nope"), auth.SessionLifetime); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown session: %v", err)
	}

	// An expired session is refused and cleaned up on the way out.
	old, _ := auth.NewSessionToken()
	oh := auth.HashToken(old)
	if err := s.CreateSession(ctx, oh, u.ID, "", time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSession(ctx, oh, auth.SessionLifetime); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired session: %v", err)
	}
	if n, _ := s.CountSessions(ctx); n != 1 {
		t.Fatalf("expired session should be deleted, %d left", n)
	}

	// PurgeExpiredSessions clears anything past its expiry.
	exp, _ := auth.NewSessionToken()
	_ = s.CreateSession(ctx, auth.HashToken(exp), u.ID, "", time.Now().Add(-time.Minute))
	n, err := s.PurgeExpiredSessions(ctx)
	if err != nil || n != 1 {
		t.Fatalf("purge: %d %v", n, err)
	}

	// Changing the password signs every browser out.
	newHash, _ := auth.HashPassword("something else entirely")
	if err := s.SetUserPassword(ctx, u.ID, newHash); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSession(ctx, th, auth.SessionLifetime); !errors.Is(err, ErrNotFound) {
		t.Fatalf("password change must end sessions: %v", err)
	}
	if _, stored, _ := s.GetUserByName(ctx, "pat"); stored != newHash {
		t.Fatal("password hash not updated")
	}

	// Deleting the account cascades to its sessions.
	tok2, _ := auth.NewSessionToken()
	_ = s.CreateSession(ctx, auth.HashToken(tok2), u.ID, "", time.Now().Add(time.Hour))
	if err := s.DeleteUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountSessions(ctx); n != 0 {
		t.Fatalf("sessions should cascade, %d left", n)
	}
}

// sessionTimes reads a session's stored expiry and last-seen time back.
func sessionTimes(t *testing.T, s *Store, tokenHash string) (expires, lastSeen time.Time) {
	t.Helper()
	var e, l string
	if err := s.queryRow(context.Background(), "SELECT expires_at, last_seen_at FROM sessions WHERE token_hash = ?", tokenHash).Scan(&e, &l); err != nil {
		t.Fatal(err)
	}
	return mustTime(e), mustTime(l)
}

// A session in use slides its expiry forward — but only once a renewal is
// due, so that an ordinary request is not a database write, and it says when
// it did so the caller can re-issue the cookie with the same expiry (#70).
func TestSessionRenewal(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	hash, _ := auth.HashPassword("hunter2hunter2")
	u, err := s.CreateUser(ctx, "pat", hash, auth.RoleViewer)
	if err != nil {
		t.Fatal(err)
	}
	near := func(a, b time.Time) bool { d := a.Sub(b); return d > -time.Minute && d < time.Minute }

	// Freshly signed in: nothing to renew, and nothing written.
	fresh := auth.HashToken("fresh")
	freshExp := time.Now().Add(auth.SessionLifetime)
	if err := s.CreateSession(ctx, fresh, u.ID, "", freshExp); err != nil {
		t.Fatal(err)
	}
	_, seenBefore := sessionTimes(t, s, fresh)
	got, err := s.GetSession(ctx, fresh, auth.SessionLifetime)
	if err != nil || got.Renewed || !near(got.ExpiresAt, freshExp) {
		t.Fatalf("fresh session: %+v %v", got, err)
	}
	if exp, seen := sessionTimes(t, s, fresh); !near(exp, freshExp) || !seen.Equal(seenBefore) {
		t.Fatalf("a fresh session should not be written to: expires %v, last seen %v (was %v)", exp, seen, seenBefore)
	}

	// Used again once the renewal interval has passed: slid forward to a
	// full lifetime from now, in the row and in what the caller is told.
	due := auth.HashToken("due")
	if err := s.CreateSession(ctx, due, u.ID, "", time.Now().Add(auth.SessionLifetime-2*auth.SessionRenewInterval)); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetSession(ctx, due, auth.SessionLifetime)
	want := time.Now().Add(auth.SessionLifetime)
	if err != nil || !got.Renewed || !near(got.ExpiresAt, want) || got.User.ID != u.ID {
		t.Fatalf("due session: %+v %v", got, err)
	}
	if exp, _ := sessionTimes(t, s, due); !near(exp, want) {
		t.Fatalf("the stored expiry should have slid to %v, is %v", want, exp)
	}
	// …and the very next request finds nothing more to do.
	if again, err := s.GetSession(ctx, due, auth.SessionLifetime); err != nil || again.Renewed || !again.ExpiresAt.Equal(got.ExpiresAt) {
		t.Fatalf("second lookup should not renew again: %+v %v", again, err)
	}

	// last_seen_at is kept roughly current without a write per request.
	if _, err := s.exec(ctx, "UPDATE sessions SET last_seen_at = ? WHERE token_hash = ?", fmtTime(time.Now().Add(-time.Hour)), fresh); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSession(ctx, fresh, auth.SessionLifetime); err != nil {
		t.Fatal(err)
	}
	if _, seen := sessionTimes(t, s, fresh); !near(seen, time.Now()) {
		t.Fatalf("a stale last_seen_at should be refreshed, is %v", seen)
	}
}

// A database that cannot answer is not the same as a session that does not
// exist: only the second may sign a browser out (#70).
func TestSessionLookupErrorIsNotNotFound(t *testing.T) {
	cfg := testConfig(t)
	s, err := OpenDSN(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	_, err = s.GetSession(context.Background(), auth.HashToken("anything"), auth.SessionLifetime)
	if err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("a failed lookup must be reported as an error of its own, got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	live := openTest(t)
	if _, err := live.GetSession(ctx, auth.HashToken("anything"), auth.SessionLifetime); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("a cancelled lookup must not read as \"no such session\", got %v", err)
	}
}

func TestAPIKeys(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	key, prefix, err := auth.NewAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	k, err := s.CreateAPIKey(ctx, "Home Assistant", prefix, auth.HashToken(key), auth.ScopeRead, "pat (admin)")
	if err != nil {
		t.Fatal(err)
	}
	if k.ID == 0 || k.Prefix != prefix || k.Scope != "read" || k.CreatedBy != "pat (admin)" || k.Revoked() {
		t.Fatalf("created: %+v", k)
	}
	if _, err := s.CreateAPIKey(ctx, " ", prefix, auth.HashToken("x"), auth.ScopeRead, ""); err == nil {
		t.Fatal("blank name must be rejected")
	}
	// The same key cannot be stored twice.
	if _, err := s.CreateAPIKey(ctx, "dup", prefix, auth.HashToken(key), auth.ScopeRead, ""); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate hash: %v", err)
	}

	got, err := s.LookupAPIKey(ctx, auth.HashToken(key))
	if err != nil || got.ID != k.ID {
		t.Fatalf("lookup: %+v %v", got, err)
	}
	if _, err := s.LookupAPIKey(ctx, auth.HashToken("gw_nonsense")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown key: %v", err)
	}

	if err := s.TouchAPIKey(ctx, k.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetAPIKey(ctx, k.ID); got.LastUsedAt == nil {
		t.Fatal("last used not recorded")
	}

	// A revoked key is invisible to lookup but still listed.
	if err := s.RevokeAPIKey(ctx, k.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LookupAPIKey(ctx, auth.HashToken(key)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked key must not resolve: %v", err)
	}
	if err := s.RevokeAPIKey(ctx, k.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoking twice: %v", err)
	}
	list, err := s.ListAPIKeys(ctx)
	if err != nil || len(list) != 1 || !list[0].Revoked() {
		t.Fatalf("list: %+v %v", list, err)
	}
}

// TestMigrateAddsColumns builds a database with the pre-2.x column set and
// checks that opening it adds the columns migrate() is responsible for.
func TestMigrateAddsColumns(t *testing.T) {
	cfg := testConfig(t)
	db, d := rawDB(t, cfg)
	old := `
CREATE TABLE events (id INTEGER PRIMARY KEY AUTOINCREMENT, ts INTEGER NOT NULL, type TEXT NOT NULL,
  node_id INTEGER, check_id INTEGER, node_name TEXT NOT NULL DEFAULT '', check_name TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL, detail TEXT NOT NULL DEFAULT '', meta TEXT);
CREATE TABLE endpoints (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, slug TEXT NOT NULL UNIQUE,
  description TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 1, method TEXT NOT NULL DEFAULT 'POST',
  token TEXT NOT NULL DEFAULT '', action TEXT NOT NULL DEFAULT '{}', last_called_at TEXT, last_status TEXT NOT NULL DEFAULT '',
  last_output TEXT NOT NULL DEFAULT '', call_count INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
`
	rawSchema(t, db, d, old)
	if _, err := db.Exec("INSERT INTO events(ts, type, title) VALUES (1, 'note', 'older event')"); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	s, err := OpenDSN(ctx, cfg)
	if err != nil {
		t.Fatalf("open older database: %v", err)
	}
	defer s.Close()
	for _, want := range []struct{ table, column string }{{"events", "actor"}, {"endpoints", "allow_no_token"}} {
		cols, err := s.tableColumns(ctx, want.table)
		if err != nil {
			t.Fatal(err)
		}
		if !cols[want.column] {
			t.Fatalf("%s.%s was not added by migrate()", want.table, want.column)
		}
	}
	// The pre-existing row survives and reads back with an empty actor.
	evs, err := s.ListEvents(ctx, EventFilter{Limit: 10})
	if err != nil || len(evs) != 1 || evs[0].Actor != "" {
		t.Fatalf("existing rows: %+v %v", evs, err)
	}
	// And a new event round-trips its actor.
	if _, err := s.InsertEvent(ctx, model.Event{Type: model.EventAuth, Title: "Signed in", Actor: "pat (admin)"}); err != nil {
		t.Fatal(err)
	}
	evs, _ = s.ListEvents(ctx, EventFilter{Limit: 10, Types: []model.EventType{model.EventAuth}})
	if len(evs) != 1 || evs[0].Actor != "pat (admin)" {
		t.Fatalf("actor round trip: %+v", evs)
	}
	// Opening again is a no-op (the columns are already there).
	s.Close()
	s2, err := OpenDSN(ctx, cfg)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	s2.Close()
}

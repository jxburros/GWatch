package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
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
	if err != nil || got.ID != u.ID {
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
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	old := `
CREATE TABLE events (id INTEGER PRIMARY KEY AUTOINCREMENT, ts INTEGER NOT NULL, type TEXT NOT NULL,
  node_id INTEGER, check_id INTEGER, node_name TEXT NOT NULL DEFAULT '', check_name TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL, detail TEXT NOT NULL DEFAULT '', meta TEXT);
CREATE TABLE endpoints (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, slug TEXT NOT NULL UNIQUE,
  description TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 1, method TEXT NOT NULL DEFAULT 'POST',
  token TEXT NOT NULL DEFAULT '', action TEXT NOT NULL DEFAULT '{}', last_called_at TEXT, last_status TEXT NOT NULL DEFAULT '',
  last_output TEXT NOT NULL DEFAULT '', call_count INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
INSERT INTO events(ts, type, title) VALUES (1, 'note', 'older event');
`
	if _, err := db.Exec(old); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("open older database: %v", err)
	}
	defer s.Close()
	ctx := context.Background()
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
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	s2.Close()
}

package api

import (
	"context"
	"strings"
	"testing"

	"github.com/jxburros/GWatch/internal/model"
)

// storedHash reads the password hash an account currently has, straight from
// the store, so the tests can see which algorithm made it.
func storedHash(t *testing.T, srv *Server, name string) string {
	t.Helper()
	_, h, err := srv.Store.GetUserByName(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// TestPasswordHashingSetting covers #49 end to end through the API: the
// setting defaults to Argon2id, an unknown value is refused, PBKDF2 can be
// selected, new accounts then get PBKDF2 hashes, and an existing Argon2id
// account is moved to PBKDF2 the next time it signs in — without losing the
// session it just signed in with — and back again when the setting is
// switched back.
func TestPasswordHashingSetting(t *testing.T) {
	ts, srv := newTestServer(t)
	admin := bootstrapAdmin(t, ts, "pat", "correct horse battery")
	if h := storedHash(t, srv, "pat"); !strings.HasPrefix(h, "$argon2id$") {
		t.Fatalf("default hash: %s", h)
	}

	var settings model.Settings
	if code, body, _ := as(t, ts, creds{Cookie: admin}, "GET", "/api/settings", nil, &settings); code != 200 {
		t.Fatalf("get settings: %d %s", code, body)
	}
	if settings.General.PasswordHashing != "argon2id" {
		t.Fatalf("default passwordHashing = %q", settings.General.PasswordHashing)
	}

	bad := settings
	bad.General.PasswordHashing = "md5"
	if code, _, _ := as(t, ts, creds{Cookie: admin}, "PUT", "/api/settings", bad, nil); code != 400 {
		t.Fatalf("an unknown algorithm must be refused, got %d", code)
	}
	// Empty is what older settings documents hold; it reads as the default.
	empty := settings
	empty.General.PasswordHashing = ""
	var back model.Settings
	if code, body, _ := as(t, ts, creds{Cookie: admin}, "PUT", "/api/settings", empty, &back); code != 200 || back.General.PasswordHashing != "argon2id" {
		t.Fatalf("empty algorithm: %d %s %q", code, body, back.General.PasswordHashing)
	}

	settings.General.PasswordHashing = "pbkdf2-sha256"
	if code, body, _ := as(t, ts, creds{Cookie: admin}, "PUT", "/api/settings", settings, &back); code != 200 || back.General.PasswordHashing != "pbkdf2-sha256" {
		t.Fatalf("select pbkdf2: %d %s", code, body)
	}

	// A new account is hashed with the selected algorithm.
	if code, body, _ := as(t, ts, creds{Cookie: admin}, "POST", "/api/users", map[string]string{"username": "sam", "password": "another long password", "role": "viewer"}, nil); code != 201 {
		t.Fatalf("create user: %d %s", code, body)
	}
	if h := storedHash(t, srv, "sam"); !strings.HasPrefix(h, "$pbkdf2-sha256$i=600000$") {
		t.Fatalf("new account hash: %s", h)
	}

	// The existing Argon2id account still signs in, and is rehashed on the
	// way. The administrator's earlier session survives: rehashing is not a
	// password change.
	pat := login(t, ts, "pat", "correct horse battery")
	if h := storedHash(t, srv, "pat"); !strings.HasPrefix(h, "$pbkdf2-sha256$") {
		t.Fatalf("argon2id account was not rehashed on sign-in: %s", h)
	}
	for _, c := range []string{admin, pat} {
		if code, _, _ := as(t, ts, creds{Cookie: c}, "GET", "/api/settings", nil, nil); code != 200 {
			t.Fatal("rehashing on sign-in must not end any session")
		}
	}
	// And the rehashed account keeps signing in with the same password.
	login(t, ts, "pat", "correct horse battery")

	// Switching back moves accounts back to Argon2id the same way.
	settings.General.PasswordHashing = "argon2id"
	if code, body, _ := as(t, ts, creds{Cookie: admin}, "PUT", "/api/settings", settings, nil); code != 200 {
		t.Fatalf("select argon2id: %d %s", code, body)
	}
	login(t, ts, "sam", "another long password")
	if h := storedHash(t, srv, "sam"); !strings.HasPrefix(h, "$argon2id$") {
		t.Fatalf("pbkdf2 account was not moved back on sign-in: %s", h)
	}
}

// TestRehashDoesNotUndoAPasswordChange exercises the guard in
// ReplacePasswordHash: a rehash computed from a hash that has since been
// replaced must not be written over the newer one.
func TestRehashDoesNotUndoAPasswordChange(t *testing.T) {
	ts, srv := newTestServer(t)
	bootstrapAdmin(t, ts, "pat", "correct horse battery")
	ctx := context.Background()
	u, oldHash, err := srv.Store.GetUserByName(ctx, "pat")
	if err != nil {
		t.Fatal(err)
	}
	newer, err := srv.hashPassword("a completely new password")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Store.SetUserPassword(ctx, u.ID, newer); err != nil {
		t.Fatal(err)
	}
	if err := srv.Store.ReplacePasswordHash(ctx, u.ID, oldHash, "$pbkdf2-sha256$stale"); err == nil {
		t.Fatal("a rehash of a replaced hash must not apply")
	}
	if h := storedHash(t, srv, "pat"); h != newer {
		t.Fatalf("the newer password hash was overwritten: %s", h)
	}
}

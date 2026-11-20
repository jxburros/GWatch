package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/auth"
	"github.com/jxburros/GWatch/internal/dbconfig"
	"github.com/jxburros/GWatch/internal/store"
)

func TestUserCommandsAddAndResetPassword(t *testing.T) {
	dir := t.TempDir()
	run := func(args []string, password string) (string, error) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		err := runUserCommand(args, strings.NewReader(password+"\n"), &stdout, &stderr)
		return stdout.String(), err
	}
	out, err := run([]string{"add", "--data-dir", dir, "admin"}, "initial-password")
	if err != nil || !strings.Contains(out, "Created admin account") {
		t.Fatalf("add first account: output %q, error %v", out, err)
	}
	out, err = run([]string{"add", "--data-dir", dir, "--role", "viewer", "reader"}, "reader-password")
	if err != nil || !strings.Contains(out, "Created viewer account") {
		t.Fatalf("add viewer: output %q, error %v", out, err)
	}

	cfg, _, err := dbconfig.Resolve(dir, dbconfig.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.OpenDSN(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	admin, oldHash, err := db.GetUserByName(context.Background(), "admin")
	if err != nil || auth.VerifyPassword(oldHash, "initial-password") != nil {
		t.Fatalf("initial password not stored: %v", err)
	}
	reader, _, err := db.GetUserByName(context.Background(), "reader")
	if err != nil || reader.Role != string(auth.RoleViewer) {
		t.Fatalf("second account role: %+v, %v", reader, err)
	}
	token, err := auth.NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	tokenHash := auth.HashToken(token)
	if err := db.CreateSession(context.Background(), tokenHash, admin.ID, "", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	out, err = run([]string{"reset-password", "--data-dir", dir, "admin"}, "replacement-password")
	if err != nil || !strings.Contains(out, "all of its browser sessions have been signed out") {
		t.Fatalf("reset password: output %q, error %v", out, err)
	}
	_, newHash, err := db.GetUserByName(context.Background(), "admin")
	if err != nil || newHash == oldHash || auth.VerifyPassword(newHash, "replacement-password") != nil {
		t.Fatalf("replacement password not stored: %v", err)
	}
	if _, err := db.GetSession(context.Background(), tokenHash, time.Hour); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("password reset left a live session: %v", err)
	}
}

func TestUserCommandRejectsShortPasswordAndInvalidRole(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	err := runUserCommand([]string{"add", "--data-dir", dir, "admin"}, strings.NewReader("short\n"), &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "at least 8 characters") {
		t.Fatalf("short password error = %v", err)
	}
	err = runUserCommand([]string{"add", "--data-dir", dir, "--role", "owner", "admin"}, strings.NewReader("valid-password\n"), &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "unknown role") {
		t.Fatalf("invalid role error = %v", err)
	}
}

func TestValidateCLIUsername(t *testing.T) {
	for _, name := range []string{"", "a", strings.Repeat("a", 65), "a\x00b"} {
		if err := validateCLIUsername(name); err == nil {
			t.Errorf("validateCLIUsername(%q) succeeded", name)
		}
	}
	for _, name := range []string{"admin", "two words", "équipe"} {
		if err := validateCLIUsername(name); err != nil {
			t.Errorf("validateCLIUsername(%q): %v", name, err)
		}
	}
}

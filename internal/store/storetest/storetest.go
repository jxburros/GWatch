// Package storetest opens stores for other packages' tests, on whichever
// database backend the environment asks for. It is the counterpart of the
// store package's own test helpers and reads the same variables:
//
//	GWATCH_TEST_DB        sqlite (default), postgres or mysql
//	GWATCH_TEST_PG_DSN    connection string for postgres
//	GWATCH_TEST_MYSQL_DSN connection string for mysql
//
// On a server each store gets a schema (PostgreSQL) or database (MySQL) of
// its own, created for the test and dropped after it, so the engine, API and
// backup suites can run against a shared server without seeing each other.
// The store package cannot import this (it would be a cycle), so it keeps a
// copy of the same few lines in testdb_test.go.
package storetest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/jxburros/GWatch/internal/store"
)

// Backend names the database the tests are running against.
func Backend() string {
	b := os.Getenv("GWATCH_TEST_DB")
	if b == "" {
		return "sqlite"
	}
	return b
}

// Config returns a fresh, isolated database for a test to open. The
// secrets key file lives in a temp dir of the test's own. It can be called
// more than once in a test when the test needs two stores.
func Config(t testing.TB) store.DBConfig {
	t.Helper()
	dir := t.TempDir()
	var cfg store.DBConfig
	switch Backend() {
	case "sqlite":
		return store.DBConfig{Driver: "sqlite", Path: filepath.Join(dir, "test.db"), KeyFile: filepath.Join(dir, store.KeyFileName)}
	case "postgres":
		dsn := os.Getenv("GWATCH_TEST_PG_DSN")
		if dsn == "" {
			t.Fatal("GWATCH_TEST_DB=postgres needs GWATCH_TEST_PG_DSN")
		}
		cfg = store.DBConfig{Driver: "postgres", DSN: dsn}
	case "mysql":
		dsn := os.Getenv("GWATCH_TEST_MYSQL_DSN")
		if dsn == "" {
			t.Fatal("GWATCH_TEST_DB=mysql needs GWATCH_TEST_MYSQL_DSN")
		}
		cfg = store.DBConfig{Driver: "mysql", DSN: dsn}
	default:
		t.Fatalf("GWATCH_TEST_DB=%q: use sqlite, postgres or mysql", Backend())
	}
	cfg.KeyFile = filepath.Join(dir, store.KeyFileName)
	var b [6]byte
	_, _ = rand.Read(b[:])
	isolated, cleanup, err := store.CreateIsolated(context.Background(), cfg, "test_"+hex.EncodeToString(b[:]))
	if err != nil {
		t.Fatalf("create isolated database: %v", err)
	}
	t.Cleanup(func() {
		if err := cleanup(); err != nil {
			t.Errorf("drop isolated database: %v", err)
		}
	})
	return isolated
}

// Open opens a fresh store for a test and closes it when the test ends.
func Open(t testing.TB) *store.Store {
	t.Helper()
	s, err := store.OpenDSN(context.Background(), Config(t))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

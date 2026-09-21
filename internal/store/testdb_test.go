package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// The store tests run against SQLite by default and against a PostgreSQL or
// MySQL server when asked to, which is how CI proves the three dialects
// agree (.github/workflows/ci.yml):
//
//	GWATCH_TEST_DB=postgres GWATCH_TEST_PG_DSN=postgres://gwatch:gwatch@localhost:5432/gwatch?sslmode=disable go test ./internal/store/...
//	GWATCH_TEST_DB=mysql    GWATCH_TEST_MYSQL_DSN='gwatch:gwatch@tcp(127.0.0.1:3306)/gwatch'                    go test ./internal/store/...
//
// Every test gets a schema (PostgreSQL) or database (MySQL) of its own,
// created before the test and dropped after it, so tests neither see each
// other's rows nor have to run one at a time. internal/store/storetest
// offers the same to the other packages' tests.

// testConfig returns a fresh, isolated database for one test to open. The
// secrets key file lives in the test's temp dir for every backend.
func testConfig(t *testing.T) DBConfig {
	t.Helper()
	dir := t.TempDir()
	var cfg DBConfig
	switch backend := os.Getenv("GWATCH_TEST_DB"); backend {
	case "", "sqlite":
		return DBConfig{Driver: "sqlite", Path: filepath.Join(dir, "test.db"), KeyFile: filepath.Join(dir, KeyFileName)}
	case "postgres":
		dsn := os.Getenv("GWATCH_TEST_PG_DSN")
		if dsn == "" {
			t.Fatal("GWATCH_TEST_DB=postgres needs GWATCH_TEST_PG_DSN")
		}
		cfg = DBConfig{Driver: "postgres", DSN: dsn}
	case "mysql":
		dsn := os.Getenv("GWATCH_TEST_MYSQL_DSN")
		if dsn == "" {
			t.Fatal("GWATCH_TEST_DB=mysql needs GWATCH_TEST_MYSQL_DSN")
		}
		cfg = DBConfig{Driver: "mysql", DSN: dsn}
	default:
		t.Fatalf("GWATCH_TEST_DB=%q: use sqlite, postgres or mysql", backend)
	}
	cfg.KeyFile = filepath.Join(dir, KeyFileName)
	var b [6]byte
	_, _ = rand.Read(b[:])
	isolated, cleanup, err := CreateIsolated(context.Background(), cfg, "test_"+hex.EncodeToString(b[:]))
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

// openTest opens a fresh store for one test.
func openTest(t *testing.T) *Store {
	t.Helper()
	return openCfg(t, testConfig(t))
}

// openCfg opens cfg and closes it when the test ends. Tests that close and
// reopen a database call OpenDSN on the same cfg themselves.
func openCfg(t *testing.T, cfg DBConfig) *Store {
	t.Helper()
	s, err := OpenDSN(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// isFileBacked says whether the backend under test is SQLite. Some tests
// are about the database file itself (the pre-migration copy, the bytes on
// disk a refused open must not touch) and skip on a server.
func isFileBacked() bool {
	b := os.Getenv("GWATCH_TEST_DB")
	return b == "" || b == "sqlite"
}

func requireFileBacked(t *testing.T, why string) {
	t.Helper()
	if !isFileBacked() {
		t.Skipf("%s: only under SQLite", why)
	}
}

// rawDB opens cfg without going through the store, for tests that build a
// database by hand the way an older GWatch would have left it. Statements
// written in SQLite's form are translated with the dialect, as the store
// itself would.
func rawDB(t *testing.T, cfg DBConfig) (*sql.DB, dialect) {
	t.Helper()
	cfg = cfg.Normalized()
	d, err := dialectFor(cfg.Driver)
	if err != nil {
		t.Fatal(err)
	}
	db, err := openPool(cfg, true)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db, d
}

// rawSchema applies SQLite-form DDL to a raw database, translated for the
// backend under test.
func rawSchema(t *testing.T, db *sql.DB, d dialect, ddl ...string) {
	t.Helper()
	var stmts []string
	for _, block := range ddl {
		stmts = append(stmts, splitStatements(block)...)
	}
	for _, stmt := range d.schema(stmts) {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("apply schema: %v\n%s", err, stmt)
		}
	}
}

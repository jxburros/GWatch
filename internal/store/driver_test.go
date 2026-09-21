package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// TestSQLiteFileURI pins the URI shape for the paths GWatch actually sees:
// a Windows drive path (which must not grow a "///" authority — see the
// comment on sqliteFileURI), a Unix absolute path, a relative one, and
// the characters SQLite's URI parser would otherwise treat as syntax. The
// inputs are already in forward-slash form, so this runs the same on every
// platform.
func TestSQLiteFileURI(t *testing.T) {
	cases := map[string]string{
		"C:/ProgramData/GWatch/gwatch.db":        "file:C:/ProgramData/GWatch/gwatch.db",
		"C:/Program Files/GWatch/gwatch.db":      "file:C:/Program Files/GWatch/gwatch.db",
		"/var/lib/gwatch/gwatch.db":              "file:/var/lib/gwatch/gwatch.db",
		"data/gwatch.db":                         "file:data/gwatch.db",
		"/srv/100%/gwatch.db":                    "file:/srv/100%25/gwatch.db",
		"/srv/what?/gwatch.db":                   "file:/srv/what%3F/gwatch.db",
		"/srv/site#2/gwatch.db":                  "file:/srv/site%232/gwatch.db",
		"/srv/tricky %3F already/gwatch.db":      "file:/srv/tricky %253F already/gwatch.db",
		"C:/Users/Ünïcödé/AppData/gwatch.db":     "file:C:/Users/Ünïcödé/AppData/gwatch.db",
		"/home/someone/.local/share/gwatch/a.db": "file:/home/someone/.local/share/gwatch/a.db",
	}
	for in, want := range cases {
		if got := sqliteFileURI(in); got != want {
			t.Errorf("sqliteFileURI(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestDataSourceNameApplies opens a database with each pool's connection
// string under whichever driver this build compiled in and checks that the
// pragmas the store relies on actually took effect. This is what the three
// driver_*.go files have to agree on, so it runs under every build tag.
func TestDataSourceNameApplies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dsn.db")
	for _, writer := range []bool{true, false} {
		dsn := dataSourceName(path, writer)
		db, err := sql.Open(driverName, dsn)
		if err != nil {
			t.Fatalf("open %q: %v", dsn, err)
		}
		var mode string
		if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
			t.Fatalf("journal_mode (writer=%v): %v", writer, err)
		}
		if mode != "wal" {
			t.Errorf("writer=%v: journal_mode = %q, want wal", writer, mode)
		}
		var fk, busy, sync int
		if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
			t.Errorf("writer=%v: foreign_keys = %d, %v; want 1", writer, fk, err)
		}
		if err := db.QueryRow("PRAGMA busy_timeout").Scan(&busy); err != nil || busy != 5000 {
			t.Errorf("writer=%v: busy_timeout = %d, %v; want 5000", writer, busy, err)
		}
		// synchronous=NORMAL reads back as 1.
		if err := db.QueryRow("PRAGMA synchronous").Scan(&sync); err != nil || sync != 1 {
			t.Errorf("writer=%v: synchronous = %d, %v; want 1 (NORMAL)", writer, sync, err)
		}
		if _, err := db.Exec("CREATE TABLE IF NOT EXISTS probe (id INTEGER PRIMARY KEY)"); err != nil {
			t.Errorf("writer=%v: write: %v", writer, err)
		}
		db.Close()
	}
}

// TestDriverLabel makes sure the label the UI shows is set and that the
// driver name the constants point at is actually registered with
// database/sql in this build.
func TestDriverLabel(t *testing.T) {
	s := &Store{d: sqliteDialect{}}
	if s.Driver() == "" {
		t.Fatal("empty driver label")
	}
	found := false
	for _, name := range sql.Drivers() {
		if name == driverName {
			found = true
		}
	}
	if !found {
		t.Fatalf("driver %q is not registered; have %v", driverName, sql.Drivers())
	}
}

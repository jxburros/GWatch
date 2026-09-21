//go:build sqlite_cgo

package store

import (
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

// Opt-in with -tags sqlite_cgo: github.com/mattn/go-sqlite3, the C SQLite
// library linked through cgo. Needs CGO_ENABLED=1 and a C compiler, so it is
// never used for the release binaries (which are cross-compiled with cgo
// off); it is here for people building from source who want it.
const (
	driverName  = "sqlite3"
	driverLabel = "mattn/go-sqlite3 (cgo)"
)

// dataSourceName builds the connection string for the writer or reader pool.
// mattn spells its options as individual keys rather than "_pragma=". It has
// no key for temp_store, so that one pragma is left at SQLite's default here
// (temporary tables and indexes may spill to disk instead of staying in
// memory) — the store never creates any, so nothing depends on it. The writer
// pool asks for BEGIN IMMEDIATE transactions, same reason as under ncruces.
func dataSourceName(path string, writer bool) string {
	dsn := sqliteFileURI(filepath.ToSlash(path)) + "?_busy_timeout=5000&_journal_mode=WAL&_synchronous=NORMAL&_foreign_keys=on"
	if writer {
		dsn += "&_txlock=immediate"
	}
	return dsn
}

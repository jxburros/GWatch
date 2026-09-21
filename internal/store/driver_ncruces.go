//go:build sqlite_ncruces && !sqlite_cgo

package store

import (
	"path/filepath"

	// The driver registers itself as "sqlite3"; the embed package carries the
	// SQLite Wasm binary it runs. Without the second import the driver
	// compiles but cannot open anything.
	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

// Opt-in with -tags sqlite_ncruces: github.com/ncruces/go-sqlite3, SQLite
// compiled to WebAssembly and run through wazero. Pure Go, but the Wasm
// runtime only JIT-compiles on amd64 and arm64; see docs/INSTALL.md.
const (
	driverName  = "sqlite3"
	driverLabel = "ncruces/go-sqlite3"
)

// dataSourceName builds the connection string for the writer or reader pool.
// ncruces takes the same "_pragma=" options as modernc. The writer pool also
// asks for BEGIN IMMEDIATE transactions so a write transaction takes the
// database lock up front instead of failing with SQLITE_BUSY when it tries
// to upgrade a read lock later.
func dataSourceName(path string, writer bool) string {
	dsn := sqliteFileURI(filepath.ToSlash(path)) + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_pragma=temp_store(MEMORY)"
	if writer {
		dsn += "&_txlock=immediate"
	}
	return dsn
}

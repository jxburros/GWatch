//go:build !sqlite_ncruces && !sqlite_cgo

package store

import (
	"fmt"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// The default: modernc.org/sqlite, SQLite transpiled to Go. No cgo, no C
// toolchain, builds for every target the release cross-compiles to.
const (
	driverName  = "sqlite"
	driverLabel = "modernc.org/sqlite"
)

// dataSourceName builds the connection string for the writer or reader pool.
// modernc applies "_pragma=" options on every new connection. Both pools get
// the same string: the writer is a single connection and serialises itself,
// so there is nothing to ask the driver for.
func dataSourceName(path string, writer bool) string {
	return fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_pragma=temp_store(MEMORY)", filepath.ToSlash(path))
}

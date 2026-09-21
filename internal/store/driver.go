package store

import "strings"

// GWatch talks to SQLite through database/sql, and which SQLite driver sits
// behind that is chosen at build time with a build tag:
//
//	(no tag)              modernc.org/sqlite             pure Go, the default and what releases ship
//	-tags sqlite_ncruces  github.com/ncruces/go-sqlite3  pure Go, SQLite compiled to Wasm
//	-tags sqlite_cgo      github.com/mattn/go-sqlite3    the C library, needs cgo and a C toolchain
//
// Exactly one of driver_modernc.go, driver_ncruces.go and driver_cgo.go is
// compiled in, and each defines the same three names: driverName (what
// sql.Open wants), driverLabel (what people see in the UI) and
// dataSourceName (the connection string for one of the two pools). Nothing
// else in the package should know which driver it is running on.
//
// Two places in store.go lean on the underlying SQLite library rather than the
// Go driver, and behave the same under all three: tableColumns reads PRAGMA
// table_info, and addMissingColumns treats an error containing "duplicate
// column" as "already there" — that text is SQLite's own message, so every
// driver reports it the same way.

// Driver names the SQLite driver this build was compiled with, for the
// Settings pages and the health payload.
func (s *Store) Driver() string { return driverLabel }

// sqliteFileURI turns a database path into the "file:" URI SQLite's URI
// parser expects, ready for query parameters to be appended.
//
// path is in forward-slash form (filepath.ToSlash). The path is written
// straight after "file:" with no "//" authority, so a Windows drive path
// comes out as "file:C:/ProgramData/GWatch/gwatch.db" rather than
// "file:///C:/...": SQLite's parser copies whatever follows the authority as
// the file name verbatim, and only the C library's own Windows VFS strips the
// leading "/" from "/C:/...". The pure Go drivers do not run that VFS, so the
// drive-letter form is the one that opens the right file everywhere. A Unix
// absolute path becomes "file:/var/lib/gwatch/gwatch.db", which is equally
// valid. The three characters the parser gives meaning to inside a path are
// percent-escaped; the parser decodes them again.
func sqliteFileURI(path string) string {
	var b strings.Builder
	b.WriteString("file:")
	for i := 0; i < len(path); i++ {
		switch c := path[i]; c {
		case '%':
			b.WriteString("%25")
		case '?':
			b.WriteString("%3F")
		case '#':
			b.WriteString("%23")
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

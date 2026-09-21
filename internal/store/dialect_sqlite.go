package store

import (
	"context"
	"database/sql"
	"os"
	"strings"
)

// sqliteDialect is the database GWatch was written against, so most of it is
// the identity: the schema constants are SQLite DDL as they stand, and the
// queries already use its placeholders and upsert syntax.
type sqliteDialect struct{}

func (sqliteDialect) name() string  { return "sqlite" }
func (sqliteDialect) label() string { return driverLabel }

func (sqliteDialect) rebind(q string) string { return q }

func (sqliteDialect) schema(stmts []string) []string { return stmts }
func (sqliteDialect) ddl(stmt string) string         { return stmt }

func (sqliteDialect) upsertClause(keys, cols []string) string { return excludedUpsert(keys, cols) }

func (sqliteDialect) insertIgnore(table string, cols []string) string {
	return "INSERT OR IGNORE" + strings.TrimPrefix(insertValues(table, cols), "INSERT")
}

func (sqliteDialect) insertID(ctx context.Context, ex execer, q string, args ...any) (int64, error) {
	return lastInsertIDExec(ctx, ex, q, args...)
}

func (sqliteDialect) ci(col string) string   { return col + " COLLATE NOCASE" }
func (sqliteDialect) ciEq(col string) string { return col + " = ? COLLATE NOCASE" }

func (sqliteDialect) intDiv(a, b string) string    { return "(" + a + "/" + b + ")" }
func (sqliteDialect) castInt(expr string) string   { return "CAST(" + expr + " AS INTEGER)" }
func (sqliteDialect) jsonArray(expr string) string { return "json_array(" + expr + ")" }

func (sqliteDialect) hasTable(ctx context.Context, s *Store, table string) (bool, error) {
	var n int
	err := s.queryRow(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name = ?", table).Scan(&n)
	return n > 0, err
}

func (sqliteDialect) tableColumns(ctx context.Context, s *Store, table string) (map[string]bool, error) {
	// PRAGMA takes no bound parameters; table names here are compile-time
	// constants from addedColumns, never user input. table_info is answered
	// by SQLite itself, so its shape is the same under every driver.
	rows, err := s.query(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return nil, err
		}
		have[name] = true
	}
	return have, rows.Err()
}

// syncSequence is a no-op: AUTOINCREMENT keeps sqlite_sequence ahead of any
// explicit id that was inserted.
func (sqliteDialect) syncSequence(ctx context.Context, tx *wtx, table string) error { return nil }

// The messages matched here are SQLite's own, so all three SQLite drivers
// (see driver.go) report them with the same text.
func (sqliteDialect) isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}
func (sqliteDialect) isDuplicateColumn(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate column")
}
func (sqliteDialect) isDuplicateIndex(err error) bool {
	return err != nil && strings.Contains(err.Error(), "already exists")
}

func (sqliteDialect) checkpoint(ctx context.Context, s *Store) error {
	_, err := s.exec(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	return err
}

func (sqliteDialect) vacuum(ctx context.Context, s *Store) error {
	_, err := s.exec(ctx, "VACUUM")
	return err
}

// sizeBytes is the database file plus its WAL.
func (sqliteDialect) sizeBytes(ctx context.Context, s *Store) int64 {
	var total int64
	for _, p := range []string{s.cfg.Path, s.cfg.Path + "-wal"} {
		if fi, err := os.Stat(p); err == nil {
			total += fi.Size()
		}
	}
	return total
}

func (sqliteDialect) serializeWrites() bool { return true }
func (sqliteDialect) fileBacked() bool      { return true }

package store

import (
	"context"
	"errors"
	"strings"

	"github.com/go-sql-driver/mysql"
)

// mysqlDialect talks to MySQL 8 or MariaDB through go-sql-driver/mysql. Its
// SQL is closest to SQLite's on the surface (? placeholders, LastInsertId)
// and furthest away in the schema:
//
//   - a TEXT column cannot be a primary key, be UNIQUE or carry an index
//     without a prefix length, so every indexed text column becomes
//     VARCHAR(255); the values kept there (hashes, slugs, user names,
//     RFC 3339 timestamps) are all far shorter;
//   - MySQL 8 refuses a plain literal DEFAULT on a TEXT column, so those are
//     written as expression defaults, DEFAULT ('…'), which MariaDB takes too;
//   - an inline "REFERENCES t(id)" on a column is parsed and silently
//     ignored, so each one is turned into a table-level FOREIGN KEY;
//   - CREATE INDEX has no IF NOT EXISTS in MySQL 8, so the duplicate error is
//     ignored instead;
//   - the connection runs with ANSI_QUOTES so the "groups" and "key" columns
//     can be quoted the same way as everywhere else.
//
// Text is compared with the table's case-insensitive collation, which is
// what stands in for COLLATE NOCASE.
type mysqlDialect struct{}

func (mysqlDialect) name() string  { return "mysql" }
func (mysqlDialect) label() string { return "MySQL/MariaDB (go-sql-driver/mysql)" }

func (mysqlDialect) rebind(q string) string { return q }

// mysqlTableSuffix pins the engine and character set so the schema does not
// depend on the server's defaults.
const mysqlTableSuffix = " ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci"

func (d mysqlDialect) schema(stmts []string) []string {
	indexed := indexedColumns(stmts)
	var out []string
	for _, stmt := range stmts {
		upper := strings.ToUpper(strings.TrimSpace(stmt))
		if strings.HasPrefix(upper, "CREATE INDEX") || strings.HasPrefix(upper, "CREATE UNIQUE INDEX") {
			out = append(out, strings.Replace(stmt, "IF NOT EXISTS ", "", 1))
			continue
		}
		t, ok := parseCreateTable(stmt)
		if !ok {
			out = append(out, stmt)
			continue
		}
		var fks []string
		for i, def := range t.defs {
			if isConstraint(def) {
				continue
			}
			name, typ, rest := splitColumn(def)
			rest = strings.Replace(rest, "UNIQUE COLLATE NOCASE", "UNIQUE", 1)
			var ref *reference
			rest, ref = takeReference(name, rest)
			if ref != nil {
				fks = append(fks, ref.constraint())
			}
			t.defs[i] = strings.TrimSpace(name + " " + d.columnType(typ, rest, indexed[t.name+"."+name]))
		}
		t.defs = append(t.defs, fks...)
		out = append(out, t.String(mysqlTableSuffix))
	}
	return out
}

// columnType maps a SQLite column type (and the clauses after it) to the
// MySQL form. indexed says whether the column takes part in a key or index.
func (mysqlDialect) columnType(typ, rest string, indexed bool) string {
	upperRest := strings.ToUpper(rest)
	switch typ {
	case "INTEGER":
		if strings.HasPrefix(upperRest, "PRIMARY KEY AUTOINCREMENT") {
			return "BIGINT AUTO_INCREMENT PRIMARY KEY" + rest[len("PRIMARY KEY AUTOINCREMENT"):]
		}
		return "BIGINT " + rest
	case "REAL":
		return "DOUBLE " + rest
	case "TEXT":
		if indexed {
			return "VARCHAR(255) " + rest
		}
		// MEDIUMTEXT rather than TEXT: a check's configuration or a
		// wallboard's panels are JSON documents, and TEXT stops at 64 KB.
		if i := strings.Index(upperRest, "DEFAULT '"); i >= 0 {
			end := strings.Index(rest[i+len("DEFAULT '"):], "'")
			lit := rest[i+len("DEFAULT ") : i+len("DEFAULT '")+end+1]
			rest = rest[:i] + "DEFAULT (" + lit + ")" + rest[i+len("DEFAULT ")+len(lit):]
		}
		return "MEDIUMTEXT " + rest
	}
	return typ + " " + rest
}

func (d mysqlDialect) ddl(stmt string) string {
	fields := strings.Fields(stmt)
	for i, f := range fields {
		if strings.EqualFold(f, "COLUMN") && i+2 < len(fields) {
			head := strings.Join(fields[:i+2], " ")
			typ := strings.ToUpper(fields[i+2])
			rest := strings.Join(fields[i+3:], " ")
			return strings.TrimSpace(head + " " + d.columnType(typ, rest, false))
		}
	}
	return stmt
}

func (mysqlDialect) upsertClause(keys, cols []string) string {
	set := nonKeys(keys, cols)
	if len(set) == 0 {
		// Nothing to update: assigning a key column to itself makes the
		// statement a no-op on conflict rather than an error.
		return "ON DUPLICATE KEY UPDATE " + keys[0] + "=" + keys[0]
	}
	parts := make([]string, len(set))
	for i, c := range set {
		parts[i] = c + "=VALUES(" + c + ")"
	}
	return "ON DUPLICATE KEY UPDATE " + strings.Join(parts, ", ")
}

func (mysqlDialect) insertIgnore(table string, cols []string) string {
	return "INSERT IGNORE" + strings.TrimPrefix(insertValues(table, cols), "INSERT")
}

func (mysqlDialect) insertID(ctx context.Context, ex execer, q string, args ...any) (int64, error) {
	return lastInsertIDExec(ctx, ex, q, args...)
}

// Comparisons and sorts are case-insensitive already under the table
// collation.
func (mysqlDialect) ci(col string) string   { return col }
func (mysqlDialect) ciEq(col string) string { return col + " = ?" }

// "/" on integers gives a decimal in MySQL; DIV is the integer division.
func (mysqlDialect) intDiv(a, b string) string    { return "(" + a + " DIV " + b + ")" }
func (mysqlDialect) castInt(expr string) string   { return "CAST(" + expr + " AS SIGNED)" }
func (mysqlDialect) jsonArray(expr string) string { return "JSON_ARRAY(" + expr + ")" }

func (mysqlDialect) hasTable(ctx context.Context, s *Store, table string) (bool, error) {
	var n int
	err := s.queryRow(ctx, "SELECT count(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?", table).Scan(&n)
	return n > 0, err
}

func (mysqlDialect) tableColumns(ctx context.Context, s *Store, table string) (map[string]bool, error) {
	return informationSchemaColumns(ctx, s, "DATABASE()", table)
}

// syncSequence is a no-op: AUTO_INCREMENT moves past any explicit id on its
// own.
func (mysqlDialect) syncSequence(ctx context.Context, tx *wtx, table string) error { return nil }

func mysqlNumber(err error) uint16 {
	var myErr *mysql.MySQLError
	if errors.As(err, &myErr) {
		return myErr.Number
	}
	return 0
}

func (mysqlDialect) isUniqueViolation(err error) bool { return mysqlNumber(err) == 1062 }
func (mysqlDialect) isDuplicateColumn(err error) bool { return mysqlNumber(err) == 1060 }
func (mysqlDialect) isDuplicateIndex(err error) bool  { return mysqlNumber(err) == 1061 }

func (mysqlDialect) checkpoint(ctx context.Context, s *Store) error { return nil }
func (mysqlDialect) vacuum(ctx context.Context, s *Store) error     { return nil }

// sizeBytes adds up data and index pages of the tables in the database.
func (mysqlDialect) sizeBytes(ctx context.Context, s *Store) int64 {
	var n int64
	_ = s.queryRow(ctx, "SELECT COALESCE(SUM(data_length + index_length), 0) FROM information_schema.tables WHERE table_schema = DATABASE()").Scan(&n)
	return n
}

// serializeWrites: see the PostgreSQL dialect for why this is true.
func (mysqlDialect) serializeWrites() bool { return true }
func (mysqlDialect) fileBacked() bool      { return false }

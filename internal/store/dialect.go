package store

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// GWatch keeps its data in SQLite by default and can be pointed at a
// PostgreSQL or MySQL/MariaDB server instead (docs/DATABASE.md). The three
// speak slightly different SQL, and a dialect is the small set of things the
// store has to ask about the database it is talking to. It is deliberately
// not a query builder: every statement in the package is still written out
// by hand, in SQLite's form, and passes through rebind on its way out. The
// dialect only steps in where the three genuinely disagree — placeholders,
// upserts, generated ids, case-insensitive text, schema DDL and the
// catalogue queries that inspect it.
//
// The schema itself is written once, in store.go, automation.go and
// hosts.go, in SQLite's form; each dialect's schema method translates those
// statements rather than keeping a second copy of them.
type dialect interface {
	// name is the value DBConfig.Driver takes: "sqlite", "postgres" or
	// "mysql".
	name() string
	// label is what the Settings pages show for the driver in use.
	label() string

	// rebind rewrites the ? placeholders of a query into whatever the
	// driver wants. It is the identity for SQLite and MySQL.
	rebind(q string) string

	// schema translates the SQLite-form DDL statements into this
	// database's own, in the order they should run.
	schema(stmts []string) []string
	// ddl translates one statement outside the schema (an ALTER TABLE from
	// addedColumns).
	ddl(stmt string) string

	// upsertClause is the ON CONFLICT / ON DUPLICATE KEY tail of an INSERT
	// into table whose conflict target is keys and whose other columns are
	// cols; every column that is not a key is overwritten from the new row.
	upsertClause(keys, cols []string) string
	// insertIgnore is an INSERT into table that does nothing when a row
	// with the same key already exists.
	insertIgnore(table string, cols []string) string
	// insertID runs an INSERT into a table with a generated id column and
	// returns the id the new row got. q is already rebound.
	insertID(ctx context.Context, ex execer, q string, args ...any) (int64, error)

	// ci is col compared or sorted without regard to case: in ORDER BY, or
	// as one side of an equality. ciEq is the whole "col = ?" test.
	ci(col string) string
	ciEq(col string) string

	// intDiv divides two integer expressions, discarding the remainder.
	intDiv(a, b string) string
	// castInt casts an expression (usually a placeholder in a SELECT list,
	// whose type the server cannot otherwise infer) to a 64-bit integer.
	castInt(expr string) string
	// jsonArray is a one-element JSON array containing expr, as text.
	jsonArray(expr string) string

	// hasTable and tableColumns read the catalogue.
	hasTable(ctx context.Context, s *Store, table string) (bool, error)
	tableColumns(ctx context.Context, s *Store, table string) (map[string]bool, error)
	// syncSequence brings the id generator of table up to date after rows
	// were inserted with explicit ids, so the next generated id does not
	// collide with one of them. A no-op where the database does this itself.
	syncSequence(ctx context.Context, tx *wtx, table string) error

	// Error classification, since each driver reports these differently.
	isUniqueViolation(err error) bool
	isDuplicateColumn(err error) bool
	isDuplicateIndex(err error) bool

	// checkpoint flushes whatever the database keeps outside its main
	// storage (the WAL, for SQLite) and is a no-op elsewhere.
	checkpoint(ctx context.Context, s *Store) error
	// vacuum reclaims space after large deletions where that is something a
	// client can ask for.
	vacuum(ctx context.Context, s *Store) error
	// sizeBytes reports how much storage the database takes.
	sizeBytes(ctx context.Context, s *Store) int64
	// serializeWrites says whether the store should take every write
	// through its single-connection writer and mutex. True for every
	// dialect today — see the comment on Store.wmu.
	serializeWrites() bool
	// fileBacked reports whether the database is a file GWatch can copy
	// before a migration (SQLite) or a server it cannot.
	fileBacked() bool
}

// dialectFor returns the dialect for a DBConfig.Driver value.
func dialectFor(driver string) (dialect, error) {
	switch driver {
	case "", "sqlite", "sqlite3":
		return sqliteDialect{}, nil
	case "postgres", "postgresql", "pgx":
		return postgresDialect{}, nil
	case "mysql", "mariadb":
		return mysqlDialect{}, nil
	}
	return nil, fmt.Errorf("unknown database driver %q (use sqlite, postgres or mysql)", driver)
}

// ---- shared helpers ----

// rebindCache remembers rewritten queries: the store's statements are
// compile-time constants, so the same few hundred strings come through again
// and again.
var rebindCache sync.Map

// numberPlaceholders replaces each ? outside a quoted string with $1, $2, …
func numberPlaceholders(q string) string {
	if !strings.Contains(q, "?") {
		return q
	}
	if v, ok := rebindCache.Load(q); ok {
		return v.(string)
	}
	var b strings.Builder
	b.Grow(len(q) + 16)
	n := 0
	inQuote := false
	for i := 0; i < len(q); i++ {
		c := q[i]
		switch {
		case c == '\'':
			inQuote = !inQuote
			b.WriteByte(c)
		case c == '?' && !inQuote:
			n++
			b.WriteByte('$')
			b.WriteString(fmt.Sprint(n))
		default:
			b.WriteByte(c)
		}
	}
	out := b.String()
	rebindCache.Store(q, out)
	return out
}

// insertValues is the "INSERT INTO t(a, b) VALUES (?,?)" every dialect
// shares.
func insertValues(table string, cols []string) string {
	return "INSERT INTO " + table + "(" + strings.Join(cols, ", ") + ") VALUES (" + placeholders(len(cols)) + ")"
}

// nonKeys returns the columns of cols that are not in keys, in order.
func nonKeys(keys, cols []string) []string {
	isKey := map[string]bool{}
	for _, k := range keys {
		isKey[k] = true
	}
	var out []string
	for _, c := range cols {
		if !isKey[c] {
			out = append(out, c)
		}
	}
	return out
}

// excludedUpsert is the ON CONFLICT form SQLite and PostgreSQL share.
func excludedUpsert(keys, cols []string) string {
	set := nonKeys(keys, cols)
	if len(set) == 0 {
		return "ON CONFLICT(" + strings.Join(keys, ", ") + ") DO NOTHING"
	}
	parts := make([]string, len(set))
	for i, c := range set {
		parts[i] = c + "=excluded." + c
	}
	return "ON CONFLICT(" + strings.Join(keys, ", ") + ") DO UPDATE SET " + strings.Join(parts, ", ")
}

// lastInsertIDExec is the insertID every driver with LastInsertId uses.
func lastInsertIDExec(ctx context.Context, ex execer, q string, args ...any) (int64, error) {
	res, err := ex.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// schemaStatements splits the three DDL constants into single statements,
// with comment lines removed, so a dialect can translate and run them one at
// a time.
func schemaStatements() []string {
	var out []string
	for _, block := range []string{schema, automationSchema, hostSchema} {
		out = append(out, splitStatements(block)...)
	}
	return out
}

// splitStatements splits a block of DDL on semicolons, dropping comments
// and empty pieces.
func splitStatements(block string) []string {
	var out []string
	for _, stmt := range strings.Split(block, ";") {
		stmt = strings.TrimSpace(stripSQLComments(stmt))
		if stmt == "" {
			continue
		}
		out = append(out, stmt)
	}
	return out
}

// stripSQLComments removes "-- …" comments, whole-line and trailing.
func stripSQLComments(s string) string {
	lines := strings.Split(s, "\n")
	out := lines[:0]
	for _, l := range lines {
		if i := strings.Index(l, "--"); i >= 0 {
			l = l[:i]
		}
		if strings.TrimSpace(l) == "" {
			continue
		}
		out = append(out, strings.TrimRight(l, " \t"))
	}
	return strings.Join(out, "\n")
}

// createTable is a CREATE TABLE statement taken apart into the pieces a
// dialect rewrites: the table name and each column or constraint definition.
type createTable struct {
	name string
	defs []string
}

// parseCreateTable takes apart a "CREATE TABLE IF NOT EXISTS name (…)"
// statement. ok is false for anything else.
func parseCreateTable(stmt string) (createTable, bool) {
	trimmed := strings.TrimSpace(stmt)
	upper := strings.ToUpper(trimmed)
	if !strings.HasPrefix(upper, "CREATE TABLE") {
		return createTable{}, false
	}
	open := strings.Index(trimmed, "(")
	close := strings.LastIndex(trimmed, ")")
	if open < 0 || close < open {
		return createTable{}, false
	}
	head := strings.Fields(trimmed[:open])
	name := head[len(head)-1]
	body := trimmed[open+1 : close]
	// Split on commas at nesting depth zero: "REFERENCES nodes(id)" and
	// "PRIMARY KEY (host_key, ts)" carry commas of their own.
	var defs []string
	depth := 0
	start := 0
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				defs = append(defs, strings.TrimSpace(body[start:i]))
				start = i + 1
			}
		}
	}
	if last := strings.TrimSpace(body[start:]); last != "" {
		defs = append(defs, last)
	}
	return createTable{name: name, defs: defs}, true
}

// String reassembles the statement with an optional table suffix (MySQL's
// ENGINE and charset clause).
func (t createTable) String(suffix string) string {
	return "CREATE TABLE IF NOT EXISTS " + t.name + " (\n  " + strings.Join(t.defs, ",\n  ") + "\n)" + suffix
}

// isConstraint reports whether a definition is a table constraint rather
// than a column.
func isConstraint(def string) bool {
	upper := strings.ToUpper(def)
	return strings.HasPrefix(upper, "PRIMARY KEY") || strings.HasPrefix(upper, "UNIQUE") || strings.HasPrefix(upper, "FOREIGN KEY") || strings.HasPrefix(upper, "CONSTRAINT")
}

// splitColumn takes a column definition apart into its name, its SQLite
// type and everything after the type.
func splitColumn(def string) (name, typ, rest string) {
	fields := strings.Fields(def)
	name = fields[0]
	if len(fields) > 1 {
		typ = strings.ToUpper(fields[1])
	}
	if len(fields) > 2 {
		rest = strings.Join(fields[2:], " ")
	}
	return name, typ, rest
}

// indexedColumns collects, from the CREATE INDEX statements and the table
// constraints in stmts, every table.column that has an index or key on it,
// so a dialect can pick a type those columns may be indexed with.
func indexedColumns(stmts []string) map[string]bool {
	out := map[string]bool{}
	for _, stmt := range stmts {
		upper := strings.ToUpper(strings.TrimSpace(stmt))
		if strings.HasPrefix(upper, "CREATE INDEX") || strings.HasPrefix(upper, "CREATE UNIQUE INDEX") {
			// … ON table(col, col)
			on := strings.Index(upper, " ON ")
			open := strings.Index(stmt[on:], "(")
			close := strings.LastIndex(stmt, ")")
			table := strings.TrimSpace(stmt[on+4 : on+open])
			for _, col := range strings.Split(stmt[on+open+1:close], ",") {
				out[table+"."+strings.TrimSpace(col)] = true
			}
			continue
		}
		t, ok := parseCreateTable(stmt)
		if !ok {
			continue
		}
		for _, def := range t.defs {
			if isConstraint(def) {
				open, close := strings.Index(def, "("), strings.LastIndex(def, ")")
				if open >= 0 && close > open {
					for _, col := range strings.Split(def[open+1:close], ",") {
						out[t.name+"."+strings.TrimSpace(col)] = true
					}
				}
				continue
			}
			name, _, rest := splitColumn(def)
			if r := strings.ToUpper(rest); strings.Contains(r, "PRIMARY KEY") || strings.Contains(r, "UNIQUE") {
				out[t.name+"."+name] = true
			}
		}
	}
	return out
}

// reference is an inline "REFERENCES table(col) [ON DELETE action]" pulled
// off a column definition.
type reference struct {
	col, target, action string
}

// takeReference removes an inline REFERENCES clause from rest and returns
// it, for dialects that want it as a table constraint instead.
func takeReference(col, rest string) (string, *reference) {
	upper := strings.ToUpper(rest)
	i := strings.Index(upper, "REFERENCES ")
	if i < 0 {
		return rest, nil
	}
	clause := rest[i:]
	before := strings.TrimSpace(rest[:i])
	// The clause runs to the end of the definition: nothing follows a
	// REFERENCES in the schema.
	fields := strings.Fields(clause)
	ref := &reference{col: col, target: fields[1]}
	if j := strings.Index(strings.ToUpper(clause), "ON DELETE "); j >= 0 {
		ref.action = strings.TrimSpace(clause[j:])
	}
	return before, ref
}

func (r reference) constraint() string {
	s := "FOREIGN KEY (" + r.col + ") REFERENCES " + r.target
	if r.action != "" {
		s += " " + r.action
	}
	return s
}

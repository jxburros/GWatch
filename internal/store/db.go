package store

import (
	"context"
	"database/sql"
)

// Every statement the store runs goes through the handful of methods in this
// file, so there is exactly one place where a query is rewritten for the
// database it is about to be sent to (see dialect.go). Nothing else in the
// package talks to s.reader, s.writer or a *sql.Tx directly.
//
// The same funnel is what lets InTx (below) run any of the store's ordinary
// methods inside one caller-owned transaction: on a store bound to a
// transaction, each of these sends its statement to that transaction rather
// than to a pool.

// query runs a read on the reader pool, or inside the transaction a store
// handed out by InTx is bound to.
func (s *Store) query(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if s.tx != nil {
		return s.tx.query(ctx, q, args...)
	}
	return s.reader.QueryContext(ctx, s.d.rebind(q), args...)
}

// queryRow runs a single-row read on the reader pool, or inside the bound
// transaction.
func (s *Store) queryRow(ctx context.Context, q string, args ...any) *sql.Row {
	if s.tx != nil {
		return s.tx.queryRow(ctx, q, args...)
	}
	return s.reader.QueryRowContext(ctx, s.d.rebind(q), args...)
}

// exec runs a single write statement on the writer, serialised with every
// other write, or inside the bound transaction, which already holds the
// writer (and the write mutex) for its whole span.
func (s *Store) exec(ctx context.Context, q string, args ...any) (sql.Result, error) {
	if s.tx != nil {
		return s.tx.exec(ctx, q, args...)
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return s.writer.ExecContext(ctx, s.d.rebind(q), args...)
}

// insertID runs an INSERT into a table whose primary key is a generated id
// and returns the id the row was given.
func (s *Store) insertID(ctx context.Context, q string, args ...any) (int64, error) {
	if s.tx != nil {
		return s.tx.insertID(ctx, q, args...)
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return s.d.insertID(ctx, s.writer, s.d.rebind(q), args...)
}

// wtx is a write transaction in progress. It wraps *sql.Tx so that the
// statements run inside it pass through the same funnel as everything else.
type wtx struct {
	tx *sql.Tx
	s  *Store
}

func (t *wtx) exec(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return t.tx.ExecContext(ctx, t.s.d.rebind(q), args...)
}

func (t *wtx) query(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return t.tx.QueryContext(ctx, t.s.d.rebind(q), args...)
}

func (t *wtx) queryRow(ctx context.Context, q string, args ...any) *sql.Row {
	return t.tx.QueryRowContext(ctx, t.s.d.rebind(q), args...)
}

func (t *wtx) prepare(ctx context.Context, q string) (*sql.Stmt, error) {
	return t.tx.PrepareContext(ctx, t.s.d.rebind(q))
}

func (t *wtx) insertID(ctx context.Context, q string, args ...any) (int64, error) {
	return t.s.d.insertID(ctx, t.tx, t.s.d.rebind(q), args...)
}

// execer is what a dialect's insertID needs from a *sql.DB or a *sql.Tx.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// writeTx runs fn inside a serialised write transaction. On a store bound to
// a transaction by InTx, fn runs inside that transaction instead: there is no
// nesting and no savepoint, so an error from fn is simply passed up, and the
// InTx that owns the transaction rolls the whole of it back.
func (s *Store) writeTx(ctx context.Context, fn func(tx *wtx) error) error {
	if s.tx != nil {
		return fn(s.tx)
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// A panic inside fn must not leave the transaction open on the single
	// writer connection, which would wedge every later write.
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := fn(&wtx{tx: tx, s: s}); err != nil {
		return err
	}
	committed = true
	return tx.Commit()
}

// InTx runs fn with a store whose every read and write goes through one write
// transaction, and commits that transaction when fn returns nil. An error
// from fn (or a panic in it) rolls back everything fn wrote, on SQLite,
// PostgreSQL and MySQL alike.
//
// The point is that the ordinary methods (SaveTrigger, ImportUser,
// RestoreNodes and the rest) need no transaction-aware twins: called on the
// store fn is given, they send their statements to the transaction, and a
// method that would open a transaction of its own (writeTx) joins this one
// instead. A restore (internal/backup) writes the whole of an archive's
// configuration this way, so a failure part-way through leaves the database
// exactly as it was rather than half-restored.
//
// Reads on the bound store go through the transaction too, so fn sees what
// it has written so far; everyone else (the reader pool) sees the data as it
// was until the commit. The transaction holds the writer for as long as fn
// runs, so every other write in the process waits for it: fn should do
// nothing slow but database work, and must never write through the outer
// store s, which would wait for the transaction fn itself holds. A method
// whose failure is meant to be tolerated (one that tries a statement and
// falls back on an error) must not be called inside fn either: on PostgreSQL
// a failed statement aborts the whole transaction. None of the methods a
// restore uses is like that. A store already bound to a transaction runs fn
// on that same transaction.
//
// The bound store is only valid inside fn.
func (s *Store) InTx(ctx context.Context, fn func(tx *Store) error) error {
	if s.tx != nil {
		return fn(s)
	}
	return s.writeTx(ctx, func(tx *wtx) error {
		bound := &Store{cfg: s.cfg, d: s.d, writer: s.writer, reader: s.reader, secrets: s.secrets, lastMigration: s.lastMigration, tx: tx, root: s}
		return fn(bound)
	})
}

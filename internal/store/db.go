package store

import (
	"context"
	"database/sql"
)

// Every statement the store runs goes through the handful of methods in this
// file, so there is exactly one place where a query is rewritten for the
// database it is about to be sent to (see dialect.go). Nothing else in the
// package talks to s.reader, s.writer or a *sql.Tx directly.

// query runs a read on the reader pool.
func (s *Store) query(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return s.reader.QueryContext(ctx, s.d.rebind(q), args...)
}

// queryRow runs a single-row read on the reader pool.
func (s *Store) queryRow(ctx context.Context, q string, args ...any) *sql.Row {
	return s.reader.QueryRowContext(ctx, s.d.rebind(q), args...)
}

// exec runs a single write statement on the writer, serialised with every
// other write.
func (s *Store) exec(ctx context.Context, q string, args ...any) (sql.Result, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return s.writer.ExecContext(ctx, s.d.rebind(q), args...)
}

// insertID runs an INSERT into a table whose primary key is a generated id
// and returns the id the row was given.
func (s *Store) insertID(ctx context.Context, q string, args ...any) (int64, error) {
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

// writeTx runs fn inside a serialised write transaction.
func (s *Store) writeTx(ctx context.Context, fn func(tx *wtx) error) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(&wtx{tx: tx, s: s}); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Package store is the SQLite persistence layer. It uses two connection pools:
// a single-connection writer (so writes are naturally queued and serialised)
// and a small reader pool. The database runs in WAL mode so readers never
// block the background writer.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jxburros/GWatch/internal/secrets"

	_ "modernc.org/sqlite"
)

// KeyFileName is the name of the machine-local secrets key file kept next to
// the database file.
const KeyFileName = "gwatch.key"

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// Store wraps the SQLite database.
type Store struct {
	path    string
	writer  *sql.DB
	reader  *sql.DB
	wmu     sync.Mutex
	secrets *secrets.Box

	secmu      sync.RWMutex
	secretsErr error
}

// Open opens (creating if needed) the database at path and applies the schema.
// Secrets stored in the settings row are encrypted with the key file
// "gwatch.key" kept alongside the database, which is created if missing.
func Open(path string) (*Store, error) {
	return OpenWithKeyFile(path, filepath.Join(filepath.Dir(path), KeyFileName))
}

// OpenWithKeyFile is Open with an explicit path for the secrets key file.
func OpenWithKeyFile(path, keyFile string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_pragma=temp_store(MEMORY)", filepath.ToSlash(path))

	writer, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	writer.SetMaxOpenConns(1)
	writer.SetMaxIdleConns(1)
	writer.SetConnMaxLifetime(0)

	reader, err := sql.Open("sqlite", dsn)
	if err != nil {
		writer.Close()
		return nil, err
	}
	reader.SetMaxOpenConns(4)
	reader.SetMaxIdleConns(4)
	reader.SetConnMaxLifetime(0)

	box, err := secrets.Load(keyFile)
	if err != nil {
		writer.Close()
		reader.Close()
		return nil, fmt.Errorf("load secrets key: %w", err)
	}

	s := &Store{path: path, writer: writer, reader: reader, secrets: box}
	if err := s.migrate(); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.migrateSecrets(context.Background()); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// SecretsHealthy reports the last secrets problem seen while loading settings
// (for example a key file that cannot decrypt the stored values). It returns
// nil while everything decrypts cleanly.
func (s *Store) SecretsHealthy() error {
	s.secmu.RLock()
	defer s.secmu.RUnlock()
	return s.secretsErr
}

func (s *Store) setSecretsErr(err error) {
	s.secmu.Lock()
	s.secretsErr = err
	s.secmu.Unlock()
}

// Path returns the database file path.
func (s *Store) Path() string { return s.path }

// Close closes both pools.
func (s *Store) Close() error {
	err1 := s.reader.Close()
	err2 := s.writer.Close()
	if err1 != nil {
		return err1
	}
	return err2
}

// SizeBytes returns the size of the database file (plus WAL) on disk.
func (s *Store) SizeBytes() int64 {
	var total int64
	for _, p := range []string{s.path, s.path + "-wal"} {
		if fi, err := os.Stat(p); err == nil {
			total += fi.Size()
		}
	}
	return total
}

// Checkpoint forces a WAL checkpoint so the main file reflects all writes.
func (s *Store) Checkpoint(ctx context.Context) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err := s.writer.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	return err
}

// Vacuum reclaims space after large deletions.
func (s *Store) Vacuum(ctx context.Context) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err := s.writer.ExecContext(ctx, "VACUUM")
	return err
}

// WriteTx runs fn inside a serialised write transaction.
func (s *Store) WriteTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Exec runs a single write statement.
func (s *Store) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return s.writer.ExecContext(ctx, query, args...)
}

// Reader exposes the read pool for ad-hoc queries.
func (s *Store) Reader() *sql.DB { return s.reader }

const schema = `
CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL);

CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS nodes (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  host TEXT NOT NULL DEFAULT '',
  group_name TEXT NOT NULL DEFAULT '',
  tags TEXT NOT NULL DEFAULT '[]',
  notes TEXT NOT NULL DEFAULT '',
  importance TEXT NOT NULL DEFAULT 'normal',
  enabled INTEGER NOT NULL DEFAULT 1,
  depends_on_node_id INTEGER REFERENCES nodes(id) ON DELETE SET NULL,
  template TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS checks (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  type TEXT NOT NULL,
  name TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  interval_seconds INTEGER NOT NULL DEFAULT 60,
  timeout_seconds INTEGER NOT NULL DEFAULT 10,
  retries INTEGER NOT NULL DEFAULT 1,
  failure_threshold INTEGER NOT NULL DEFAULT 0,
  config TEXT NOT NULL DEFAULT '{}',
  alerts TEXT,
  sort_order INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_checks_node ON checks(node_id);

CREATE TABLE IF NOT EXISTS check_state (
  check_id INTEGER PRIMARY KEY REFERENCES checks(id) ON DELETE CASCADE,
  status TEXT NOT NULL DEFAULT 'unknown',
  consecutive_failures INTEGER NOT NULL DEFAULT 0,
  last_run_at TEXT,
  last_success_at TEXT,
  last_change_at TEXT,
  next_run_at TEXT,
  last_message TEXT NOT NULL DEFAULT '',
  last_latency_ms REAL,
  alert_active INTEGER NOT NULL DEFAULT 0,
  alert_suppressed INTEGER NOT NULL DEFAULT 0,
  suppress_reason TEXT NOT NULL DEFAULT '',
  last_alert_at TEXT,
  silenced_until TEXT,
  affected_by_check_id INTEGER,
  warning_active INTEGER NOT NULL DEFAULT 0,
  cert_warning_active INTEGER NOT NULL DEFAULT 0,
  last_content_hash TEXT NOT NULL DEFAULT '',
  last_content_value TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS results (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  check_id INTEGER NOT NULL REFERENCES checks(id) ON DELETE CASCADE,
  ts INTEGER NOT NULL,             -- unix milliseconds
  success INTEGER NOT NULL,
  status TEXT NOT NULL,
  message TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '',
  latency_ms REAL,
  min_ms REAL,
  max_ms REAL,
  jitter_ms REAL,
  loss_pct REAL,
  attempts INTEGER NOT NULL DEFAULT 1,
  details TEXT NOT NULL DEFAULT '{}',
  warnings TEXT NOT NULL DEFAULT '[]'
);
CREATE INDEX IF NOT EXISTS idx_results_check_ts ON results(check_id, ts);
CREATE INDEX IF NOT EXISTS idx_results_ts ON results(ts);

CREATE TABLE IF NOT EXISTS rollups (
  check_id INTEGER NOT NULL REFERENCES checks(id) ON DELETE CASCADE,
  bucket_seconds INTEGER NOT NULL,
  bucket_start INTEGER NOT NULL,   -- unix seconds
  count INTEGER NOT NULL,
  success_count INTEGER NOT NULL,
  fail_count INTEGER NOT NULL,
  min_ms REAL,
  max_ms REAL,
  avg_ms REAL,
  avg_jitter_ms REAL,
  avg_loss_pct REAL,
  availability REAL NOT NULL,
  PRIMARY KEY (check_id, bucket_seconds, bucket_start)
);
CREATE INDEX IF NOT EXISTS idx_rollups_bucket ON rollups(bucket_seconds, bucket_start);

CREATE TABLE IF NOT EXISTS events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts INTEGER NOT NULL,
  type TEXT NOT NULL,
  node_id INTEGER,
  check_id INTEGER,
  node_name TEXT NOT NULL DEFAULT '',
  check_name TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL,
  detail TEXT NOT NULL DEFAULT '',
  meta TEXT
);
CREATE INDEX IF NOT EXISTS idx_events_ts ON events(ts);
CREATE INDEX IF NOT EXISTS idx_events_node ON events(node_id, ts);
CREATE INDEX IF NOT EXISTS idx_events_check ON events(check_id, ts);

CREATE TABLE IF NOT EXISTS maintenance_windows (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  node_id INTEGER REFERENCES nodes(id) ON DELETE CASCADE,
  group_name TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 1,
  start_at TEXT NOT NULL,
  end_at TEXT NOT NULL,
  weekdays TEXT NOT NULL DEFAULT '[]',
  duration_minutes INTEGER NOT NULL DEFAULT 0,
  notes TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS dashboards (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  sort_order INTEGER NOT NULL DEFAULT 0,
  widgets TEXT NOT NULL DEFAULT '[]',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
`

func (s *Store) migrate() error {
	ctx := context.Background()
	return s.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, schema); err != nil {
			return fmt.Errorf("apply schema: %w", err)
		}
		if _, err := tx.ExecContext(ctx, automationSchema); err != nil {
			return fmt.Errorf("apply automation schema: %w", err)
		}
		var version int
		err := tx.QueryRowContext(ctx, "SELECT version FROM schema_version LIMIT 1").Scan(&version)
		if errors.Is(err, sql.ErrNoRows) {
			_, err = tx.ExecContext(ctx, "INSERT INTO schema_version(version) VALUES (1)")
		}
		return err
	})
}

// ---- helpers ----

func fmtTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func fmtTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return fmtTime(*t)
}

func parseTime(s sql.NullString) *time.Time {
	if !s.Valid || s.String == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, s.String)
	if err != nil {
		return nil
	}
	t = t.Local()
	return &t
}

func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t.Local()
}

func jsonString(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

func nullFloat(f *float64) any {
	if f == nil {
		return nil
	}
	return *f
}

func floatPtr(n sql.NullFloat64) *float64 {
	if !n.Valid {
		return nil
	}
	v := n.Float64
	return &v
}

func nullInt64(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func int64Ptr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?,", n-1) + "?"
}

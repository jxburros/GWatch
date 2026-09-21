// Package store is the persistence layer. It keeps GWatch's data in an
// embedded SQLite file by default, or in a PostgreSQL or MySQL/MariaDB
// server when an administrator asks for one (docs/DATABASE.md, dialect.go).
// It uses two connection pools: a single-connection writer (so writes are
// naturally queued and serialised) and a small reader pool. Under SQLite the
// database runs in WAL mode so readers never block the background writer.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jxburros/GWatch/internal/secrets"
)

// KeyFileName is the name of the machine-local secrets key file kept in the
// data directory (next to the database file, when the database is a file).
const KeyFileName = "gwatch.key"

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// Store wraps the database.
type Store struct {
	cfg    DBConfig
	d      dialect
	writer *sql.DB
	reader *sql.DB
	// wmu serialises writes. The single-connection writer pool would queue
	// them anyway; the mutex makes the ordering explicit and lets a write
	// transaction hold the writer for its whole span. It is taken for every
	// dialect (dialect.serializeWrites), servers included.
	wmu     sync.Mutex
	secrets *secrets.Box

	secmu      sync.RWMutex
	secretsErr error

	lastMigration *MigrationReport
}

// Open opens (creating if needed) the SQLite database at path and applies
// the schema. Secrets stored in the settings row are encrypted with the key
// file "gwatch.key" kept alongside the database, which is created if missing.
// For a server database, see OpenDSN.
func Open(path string) (*Store, error) {
	return OpenWithKeyFile(path, filepath.Join(filepath.Dir(path), KeyFileName))
}

// OpenWithKeyFile is Open with an explicit path for the secrets key file.
func OpenWithKeyFile(path, keyFile string) (*Store, error) {
	return OpenDSN(context.Background(), DBConfig{Driver: "sqlite", Path: path, KeyFile: keyFile})
}

func loadSecrets(keyFile string) (*secrets.Box, error) {
	box, err := secrets.Load(keyFile)
	if err != nil {
		return nil, fmt.Errorf("load secrets key: %w", err)
	}
	return box, nil
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

// Path describes where the data is: the database file path under SQLite,
// and a password-free "postgres://user@host:port/db" for a server.
func (s *Store) Path() string { return s.cfg.Describe() }

// Config returns the connection settings the store was opened with, with
// the password redacted.
func (s *Store) Config() DBConfig { return s.cfg.Redacted() }

// Backend names the database in use: "sqlite", "postgres" or "mysql".
func (s *Store) Backend() string { return s.d.name() }

// Driver names the database driver this store runs on, for the Settings
// pages and the health payload: which SQLite driver the build was compiled
// with, or the server driver.
func (s *Store) Driver() string { return s.d.label() }

// Close closes both pools.
func (s *Store) Close() error {
	err1 := s.reader.Close()
	err2 := s.writer.Close()
	if err1 != nil {
		return err1
	}
	return err2
}

// SizeBytes returns how much storage the database takes: the file plus its
// WAL under SQLite, the tables' data and indexes on a server.
func (s *Store) SizeBytes() int64 {
	return s.d.sizeBytes(context.Background(), s)
}

// Checkpoint forces a WAL checkpoint so the main file reflects all writes.
// It is a no-op on a server database, which has no file to bring up to date.
func (s *Store) Checkpoint(ctx context.Context) error {
	return s.d.checkpoint(ctx, s)
}

// Vacuum reclaims space after large deletions. A no-op on a server database,
// whose own maintenance takes care of that.
func (s *Store) Vacuum(ctx context.Context) error {
	return s.d.vacuum(ctx, s)
}

const schema = `
CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL);

CREATE TABLE IF NOT EXISTS settings (
  "key" TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS nodes (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  host TEXT NOT NULL DEFAULT '',
  group_name TEXT NOT NULL DEFAULT '',
  "groups" TEXT NOT NULL DEFAULT '[]',
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
  stddev_ms REAL,
  loss_pct REAL,
  attempts INTEGER NOT NULL DEFAULT 1,
  details TEXT NOT NULL DEFAULT '{}',
  warnings TEXT NOT NULL DEFAULT '[]',
  metrics TEXT NOT NULL DEFAULT ''
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

-- A wallboard is a dashboard's cousin for a screen across the room. The share
-- token is stored as it is rather than hashed: an address typed into a display
-- with no keyboard has to be readable again later. It is worth one read-only
-- board, and clearing it is what takes the address back.
CREATE TABLE IF NOT EXISTS wallboards (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  sort_order INTEGER NOT NULL DEFAULT 0,
  layout TEXT NOT NULL DEFAULT '{}',
  panels TEXT NOT NULL DEFAULT '[]',
  share_enabled INTEGER NOT NULL DEFAULT 0,
  share_token TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_wallboards_share ON wallboards(share_token);

CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT NOT NULL UNIQUE COLLATE NOCASE,
  password_hash TEXT NOT NULL,
  role TEXT NOT NULL DEFAULT 'viewer',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  last_login_at TEXT
);

CREATE TABLE IF NOT EXISTS sessions (
  token_hash TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL,
  remote TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);

CREATE TABLE IF NOT EXISTS api_keys (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  prefix TEXT NOT NULL DEFAULT '',
  key_hash TEXT NOT NULL UNIQUE,
  scope TEXT NOT NULL DEFAULT 'read',
  created_by TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  last_used_at TEXT,
  revoked_at TEXT
);
`

// addedColumns lists columns added to tables that older databases created
// before the column existed. CREATE TABLE IF NOT EXISTS leaves such a table
// alone, so each one is added with ALTER TABLE when the catalogue shows it
// missing. The DDL is written in SQLite's form and translated for the other
// databases like the schema itself (dialect.ddl). Adding an entry here is the way to extend an existing table.
//
// This runs before the numbered migrations below, on every open, regardless
// of schema_version: it predates the version-tracked mechanism and a step may
// depend on a column it adds existing.
var addedColumns = []struct{ table, column, ddl string }{
	{"endpoints", "allow_no_token", "ALTER TABLE endpoints ADD COLUMN allow_no_token INTEGER NOT NULL DEFAULT 0"},
	{"events", "actor", "ALTER TABLE events ADD COLUMN actor TEXT NOT NULL DEFAULT ''"},
	{"results", "stddev_ms", "ALTER TABLE results ADD COLUMN stddev_ms REAL"},
	// "groups" is quoted everywhere it is used: it is a keyword in SQLite's
	// window-frame syntax, and a bare one reads badly even where it parses.
	{"nodes", "groups", `ALTER TABLE nodes ADD COLUMN "groups" TEXT NOT NULL DEFAULT '[]'`},
	// The named metrics a check measured beyond its latency, as a JSON object
	// of name -> number. An SNMP check writes one entry per OID here; every
	// other check leaves it empty.
	{"results", "metrics", "ALTER TABLE results ADD COLUMN metrics TEXT NOT NULL DEFAULT ''"},
	// #60: a hardware check's metrics are tracked one by one. An event about
	// one of them names it; the check's state remembers each one's last
	// verdict (a JSON object of key -> status); a trigger can watch one.
	{"events", "metric", "ALTER TABLE events ADD COLUMN metric TEXT NOT NULL DEFAULT ''"},
	{"check_state", "metric_status", "ALTER TABLE check_state ADD COLUMN metric_status TEXT NOT NULL DEFAULT ''"},
	{"triggers", "metric", "ALTER TABLE triggers ADD COLUMN metric TEXT NOT NULL DEFAULT ''"},
	{"triggers", "metric_over", "ALTER TABLE triggers ADD COLUMN metric_over REAL NOT NULL DEFAULT 0"},
}

// currentSchemaVersion is the schema_version this build expects. Every
// database ever shipped before this mechanism existed is version 1, which is
// why the first numbered migration below is version 2.
//
// To add a data migration: bump this constant by one, then append a
// {version, name, run} entry to migrations for the new version. run does
// whatever the migration needs inside the *sql.Tx it is given (including
// schema DDL, though additive columns usually belong in addedColumns
// instead); migrate() takes care of backing up the file first, running the
// step in its own transaction, and recording the new version once it
// commits.
const currentSchemaVersion = 3

// migration is one numbered step that brings the database from version-1 to
// version. Steps run in order, oldest first, each in its own transaction.
type migration struct {
	version int
	name    string
	run     func(ctx context.Context, tx *wtx) error
}

// migrations must stay sorted by version, ascending, with no gaps from 2 up
// to currentSchemaVersion: runMigrations walks it in order and stops once the
// database is current.
var migrations = []migration{
	{
		version: 2,
		name:    "backfill missing check_state rows",
		run:     migrateBackfillCheckState,
	},
	{
		version: 3,
		name:    "backfill node groups from group_name",
		run:     migrateBackfillNodeGroups,
	},
}

// migrateBackfillCheckState gives every check a check_state row. Every
// current code path that inserts a check inserts its state alongside it
// (CreateNode, UpdateNode, CreateNodeWithID), but that has not always been
// true, and a check without a state row makes GetState and anything that
// joins on it misbehave. This also serves as the migration mechanism's proof
// of life: it is real, it is idempotent, and it is safe to run on any
// database whether or not the gap it closes actually applies.
func migrateBackfillCheckState(ctx context.Context, tx *wtx) error {
	_, err := tx.exec(ctx, `
		INSERT INTO check_state(check_id, status)
		SELECT id, 'unknown' FROM checks
		WHERE id NOT IN (SELECT check_id FROM check_state)`)
	return err
}

// migrateBackfillNodeGroups fills the groups column for rows written before a
// node could belong to more than one group. addedColumns gives those rows an
// empty list; the group they were actually in is still in group_name, so each
// one becomes a one-group node. Rows that already have a list are left alone,
// which is what makes the step safe to run twice.
func migrateBackfillNodeGroups(ctx context.Context, tx *wtx) error {
	_, err := tx.exec(ctx, `
		UPDATE nodes
		SET "groups" = `+tx.s.d.jsonArray("group_name")+`
		WHERE trim(coalesce(group_name, '')) != ''
		  AND ("groups" IS NULL OR trim("groups") = '' OR "groups" = '[]')`)
	return err
}

// MigrationReport summarises what Open did to bring an older database up to
// date. It is nil when the database was freshly created or was already at
// currentSchemaVersion, since neither case touches or backs up anything.
type MigrationReport struct {
	FromVersion   int
	ToVersion     int
	BackupPath    string   // the gwatch.db.before-vN copy made before migrating, if any
	BackupSkipped string   // why no copy was made (a server database), if none was
	Applied       []string // migration names, in the order they ran
}

// LastMigration returns the report from the migration Open ran, or nil if
// none was needed.
func (s *Store) LastMigration() *MigrationReport { return s.lastMigration }

// SchemaVersion returns the schema_version currently recorded in the
// database.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	return s.storedSchemaVersion(ctx)
}

func (s *Store) migrate() error {
	ctx := context.Background()

	// Read-only, and first: a database from a newer GWatch must be refused
	// without writing a single byte to it, so an operator who tried the new
	// build and rolled back to this one finds the file exactly as they left
	// it.
	stored, err := s.storedSchemaVersion(ctx)
	if err != nil {
		return err
	}
	if stored > currentSchemaVersion {
		return fmt.Errorf("this database (schema version %d) was written by a newer GWatch than this build (schema version %d) — upgrade GWatch, or restore a backup taken with this version", stored, currentSchemaVersion)
	}
	fresh := stored == 0

	if !fresh && stored < currentSchemaVersion {
		s.lastMigration = &MigrationReport{FromVersion: stored, ToVersion: currentSchemaVersion}
		if s.d.fileBacked() {
			backupPath, err := s.backupBeforeMigration(currentSchemaVersion)
			if err != nil {
				return fmt.Errorf("back up database before migrating: %w", err)
			}
			s.lastMigration.BackupPath = backupPath
		} else {
			// A server database is not a file GWatch can copy. The report
			// says so, and docs/DATABASE.md asks for a server-side backup
			// (pg_dump, mysqldump) before upgrading.
			s.lastMigration.BackupSkipped = fmt.Sprintf("no pre-migration copy was made: the database is on a %s server, which GWatch cannot copy — take a server-side backup before upgrading", s.d.name())
		}
	}

	if err := s.applySchema(ctx); err != nil {
		return err
	}
	if fresh {
		if _, err := s.exec(ctx, "INSERT INTO schema_version(version) VALUES (?)", currentSchemaVersion); err != nil {
			return fmt.Errorf("record schema version: %w", err)
		}
	}

	if err := s.addMissingColumns(ctx); err != nil {
		return err
	}

	if fresh || stored == currentSchemaVersion {
		return nil
	}
	return s.runMigrations(ctx, stored)
}

// storedSchemaVersion reads the schema_version row without writing anything,
// so it is safe to call before deciding whether the database may be opened
// at all. It returns 0 for a database that has no schema_version table or
// row yet, which migrate() treats as fresh.
func (s *Store) storedSchemaVersion(ctx context.Context) (int, error) {
	have, err := s.d.hasTable(ctx, s, "schema_version")
	if err != nil {
		return 0, fmt.Errorf("check schema_version table: %w", err)
	}
	if !have {
		return 0, nil
	}
	var version int
	err = s.queryRow(ctx, "SELECT version FROM schema_version LIMIT 1").Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read schema_version: %w", err)
	}
	return version, nil
}

// runMigrations applies every step after stored, in order, each in its own
// transaction that commits the step's data changes together with the new
// version number: a step that fails partway leaves schema_version exactly
// where it started, not somewhere in between.
func (s *Store) runMigrations(ctx context.Context, stored int) error {
	for _, m := range migrations {
		if m.version <= stored {
			continue
		}
		if err := s.writeTx(ctx, func(tx *wtx) error {
			if err := m.run(ctx, tx); err != nil {
				return fmt.Errorf("migration %d (%s): %w", m.version, m.name, err)
			}
			if _, err := tx.exec(ctx, "UPDATE schema_version SET version = ?", m.version); err != nil {
				return fmt.Errorf("record schema version %d: %w", m.version, err)
			}
			return nil
		}); err != nil {
			return err
		}
		stored = m.version
		if s.lastMigration != nil {
			s.lastMigration.Applied = append(s.lastMigration.Applied, m.name)
		}
	}
	return nil
}

// backupBeforeMigration checkpoints the WAL and copies the main database
// file to a sibling gwatch.db.before-vN, so a migration that turns out to be
// wrong can be undone by restoring that copy. The store keeps writes on a
// single-connection writer pool and reads on a separate pool, so a plain file
// copy after a checkpoint is a consistent snapshot without needing
// VACUUM INTO or taking the database offline.
func (s *Store) backupBeforeMigration(targetVersion int) (string, error) {
	if err := s.Checkpoint(context.Background()); err != nil {
		return "", fmt.Errorf("checkpoint: %w", err)
	}
	dest := fmt.Sprintf("%s.before-v%d", s.cfg.Path, targetVersion)
	if _, err := os.Stat(dest); err == nil {
		// A backup from an earlier attempt is already there; do not clobber
		// it, keep both.
		dest = fmt.Sprintf("%s.%s", dest, time.Now().UTC().Format("20060102-150405"))
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("stat %s: %w", dest, err)
	}
	if err := copyFile(s.cfg.Path, dest); err != nil {
		return "", err
	}
	return dest, nil
}

// copyFile copies src to dst by way of a temporary file and a rename, so a
// reader never sees a partially written backup.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create %s: %w", tmp, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return fmt.Errorf("copy to %s: %w", tmp, err)
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(tmp)
		return fmt.Errorf("sync %s: %w", tmp, err)
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename %s to %s: %w", tmp, dst, err)
	}
	return nil
}

func (s *Store) addMissingColumns(ctx context.Context) error {
	cache := map[string]map[string]bool{}
	for _, c := range addedColumns {
		have, ok := cache[c.table]
		if !ok {
			cols, err := s.tableColumns(ctx, c.table)
			if err != nil {
				return fmt.Errorf("inspect %s: %w", c.table, err)
			}
			cache[c.table] = cols
			have = cols
		}
		if len(have) == 0 || have[c.column] {
			continue // the table does not exist, or the column is already there
		}
		if _, err := s.exec(ctx, s.d.ddl(c.ddl)); err != nil && !s.d.isDuplicateColumn(err) {
			return fmt.Errorf("add %s.%s: %w", c.table, c.column, err)
		}
		have[c.column] = true
	}
	return nil
}

// tableColumns lists the columns of table, or nothing if the table does not
// exist.
func (s *Store) tableColumns(ctx context.Context, table string) (map[string]bool, error) {
	return s.d.tableColumns(ctx, s, table)
}

// applySchema runs the CREATE TABLE / CREATE INDEX statements, translated
// for the database in use, one at a time and outside any transaction: MySQL
// commits DDL implicitly anyway, and every statement is an IF NOT EXISTS
// (or treated as one) so running them again is harmless.
func (s *Store) applySchema(ctx context.Context) error {
	for _, stmt := range s.d.schema(schemaStatements()) {
		if _, err := s.exec(ctx, stmt); err != nil {
			upper := strings.ToUpper(stmt)
			if strings.HasPrefix(upper, "CREATE") && strings.Contains(upper, "INDEX") && s.d.isDuplicateIndex(err) {
				continue
			}
			return fmt.Errorf("apply schema: %w\n%s", err, stmt)
		}
	}
	return nil
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

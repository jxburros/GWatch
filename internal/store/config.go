package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// DBConfig says which database GWatch keeps its data in and how to reach it.
// The zero value, or Driver "sqlite" with a Path, is the embedded SQLite file
// every install starts with. Driver "postgres" or "mysql" points at a server
// instead; see docs/DATABASE.md for how each field is used.
//
// This is also the shape of database.json in the data directory, which is
// how a server backend survives a restart (the Windows service is started
// with --data-dir alone). The password in that file is sealed with the same
// key file as the secrets in the settings table.
type DBConfig struct {
	// Driver is "sqlite" (the default), "postgres" or "mysql". "mariadb" is
	// accepted as a spelling of "mysql".
	Driver string `json:"driver"`
	// Path is the SQLite database file. Ignored by the server drivers.
	Path string `json:"path,omitempty"`
	// DSN, when set, is a complete connection string in the driver's own
	// syntax (a postgres:// URL or key=value list; user:pass@tcp(host)/db
	// for MySQL) and takes the place of Host, Port, User, Password, Database
	// and SSLMode. Schema and Database still apply on top of it.
	DSN      string `json:"dsn,omitempty"`
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
	// Database is the database name on the server.
	Database string `json:"database,omitempty"`
	// Schema is the PostgreSQL schema to keep the tables in (the default is
	// the user's search path, normally "public"). Unused by MySQL, where a
	// database is a schema.
	Schema string `json:"schema,omitempty"`
	// SSLMode is PostgreSQL's sslmode (disable, prefer, require, verify-ca,
	// verify-full; default prefer) and is mapped onto the MySQL driver's
	// tls setting: disable → off, prefer → preferred, require → encrypted
	// without verifying the server, verify-ca/verify-full → verified.
	SSLMode string `json:"sslMode,omitempty"`

	// KeyFile is where the secrets key lives. It is not part of the
	// connection and is not stored in database.json: Open derives it from
	// the data directory.
	KeyFile string `json:"-"`
}

// Default ports.
const (
	DefaultPostgresPort = 5432
	DefaultMySQLPort    = 3306
)

// Normalized returns the config with the driver spelled canonically and the
// defaults filled in.
func (c DBConfig) Normalized() DBConfig {
	switch strings.ToLower(strings.TrimSpace(c.Driver)) {
	case "", "sqlite", "sqlite3":
		c.Driver = "sqlite"
	case "postgres", "postgresql", "pgx":
		c.Driver = "postgres"
		if c.Port == 0 {
			c.Port = DefaultPostgresPort
		}
		if c.Host == "" && c.DSN == "" {
			c.Host = "localhost"
		}
		if c.SSLMode == "" {
			c.SSLMode = "prefer"
		}
	case "mysql", "mariadb":
		c.Driver = "mysql"
		if c.Port == 0 {
			c.Port = DefaultMySQLPort
		}
		if c.Host == "" && c.DSN == "" {
			c.Host = "localhost"
		}
		if c.SSLMode == "" {
			c.SSLMode = "prefer"
		}
	default:
		c.Driver = strings.ToLower(strings.TrimSpace(c.Driver))
	}
	return c
}

// IsServer reports whether the config points at a database server rather
// than the embedded file.
func (c DBConfig) IsServer() bool {
	d := c.Normalized().Driver
	return d == "postgres" || d == "mysql"
}

// Validate reports the first thing wrong with the config, in words an
// administrator can act on.
func (c DBConfig) Validate() error {
	c = c.Normalized()
	switch c.Driver {
	case "sqlite":
		if c.Path == "" {
			return errors.New("a database file path is required for SQLite")
		}
		return nil
	case "postgres", "mysql":
		if c.DSN != "" {
			return nil
		}
		if c.Host == "" {
			return errors.New("a database host is required")
		}
		if c.Port <= 0 || c.Port > 65535 {
			return fmt.Errorf("port %d is out of range", c.Port)
		}
		if c.Database == "" {
			return errors.New("a database name is required")
		}
		if c.User == "" {
			return errors.New("a database user is required")
		}
		switch c.SSLMode {
		case "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
		default:
			return fmt.Errorf("unknown TLS mode %q (use disable, prefer, require, verify-ca or verify-full)", c.SSLMode)
		}
		return nil
	}
	return fmt.Errorf("unknown database driver %q (use sqlite, postgres or mysql)", c.Driver)
}

// Describe is a one-line, password-free description of where the data is:
// the file path for SQLite, "postgres://user@host:5432/db" for a server.
func (c DBConfig) Describe() string {
	c = c.Normalized()
	switch c.Driver {
	case "sqlite":
		return c.Path
	case "postgres", "mysql":
		if c.DSN != "" && c.Host == "" {
			return c.Driver + " (connection string)" + c.schemaSuffix()
		}
		host := c.Host
		if c.Port != 0 {
			host = net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
		}
		s := c.Driver + "://"
		if c.User != "" {
			s += c.User + "@"
		}
		return s + host + "/" + c.Database + c.schemaSuffix()
	}
	return c.Driver
}

func (c DBConfig) schemaSuffix() string {
	if c.Driver == "postgres" && c.Schema != "" {
		return " (schema " + c.Schema + ")"
	}
	return ""
}

// Redacted returns the config with the password and any connection string
// (which may carry one) replaced, for showing or logging.
func (c DBConfig) Redacted() DBConfig {
	if c.Password != "" {
		c.Password = "********"
	}
	if c.DSN != "" {
		c.DSN = "********"
	}
	return c
}

// OpenDSN opens the database cfg describes, creating the schema if needed,
// and applies any pending migrations. It is Open for every backend: Open
// itself is the SQLite convenience.
//
// cfg.KeyFile is required for a server backend; for SQLite it defaults to
// KeyFileName next to the database file.
func OpenDSN(ctx context.Context, cfg DBConfig) (*Store, error) {
	cfg = cfg.Normalized()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	d, err := dialectFor(cfg.Driver)
	if err != nil {
		return nil, err
	}
	if cfg.KeyFile == "" {
		if cfg.Driver != "sqlite" {
			return nil, errors.New("a secrets key file path is required for a server database")
		}
		cfg.KeyFile = filepath.Join(filepath.Dir(cfg.Path), KeyFileName)
	}
	if cfg.Driver == "sqlite" {
		if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o755); err != nil {
			return nil, fmt.Errorf("create data dir: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(cfg.KeyFile), 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	writer, err := openPool(cfg, true)
	if err != nil {
		return nil, err
	}
	// One writer connection: the store serialises writes itself (see wmu),
	// and a second connection would only sit idle behind the mutex.
	writer.SetMaxOpenConns(1)
	writer.SetMaxIdleConns(1)
	reader, err := openPool(cfg, false)
	if err != nil {
		writer.Close()
		return nil, err
	}
	reader.SetMaxOpenConns(4)
	reader.SetMaxIdleConns(4)
	if cfg.Driver == "sqlite" {
		writer.SetConnMaxLifetime(0)
		reader.SetConnMaxLifetime(0)
	} else {
		// A server may drop idle connections; recycling them quietly is
		// kinder than finding out on the next query.
		for _, db := range []*sql.DB{writer, reader} {
			db.SetConnMaxLifetime(time.Hour)
			db.SetConnMaxIdleTime(5 * time.Minute)
		}
	}

	s := &Store{cfg: cfg, d: d, writer: writer, reader: reader}
	if err := writer.PingContext(ctx); err != nil {
		s.Close()
		return nil, fmt.Errorf("connect to %s: %w", cfg.Describe(), err)
	}
	if cfg.Driver == "postgres" && cfg.Schema != "" {
		// Best effort: the schema is normally created by the administrator
		// along with the database and user (docs/DATABASE.md), and a user
		// without CREATE on the database gets a clearer error from the
		// CREATE TABLE that follows than from this.
		_, _ = writer.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS "`+strings.ReplaceAll(cfg.Schema, `"`, `""`)+`"`)
	}

	s.secrets, err = loadSecrets(cfg.KeyFile)
	if err != nil {
		s.Close()
		return nil, err
	}
	if err := s.migrate(); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.migrateSecrets(ctx); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.migrateCheckSecrets(ctx); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// openPool opens one of the two connection pools. writer says which, for the
// SQLite drivers that want the writer's transactions to take the lock up
// front.
func openPool(cfg DBConfig, writer bool) (*sql.DB, error) {
	switch cfg.Driver {
	case "sqlite":
		// Which SQLite driver this is, and what its connection string looks
		// like, is decided at build time — see driver.go.
		return sql.Open(driverName, dataSourceName(cfg.Path, writer))
	case "postgres":
		cc, err := cfg.pgxConfig()
		if err != nil {
			return nil, err
		}
		return stdlib.OpenDB(*cc), nil
	case "mysql":
		mc, err := cfg.mysqlConfig()
		if err != nil {
			return nil, err
		}
		conn, err := mysql.NewConnector(mc)
		if err != nil {
			return nil, err
		}
		return sql.OpenDB(conn), nil
	}
	return nil, fmt.Errorf("unknown database driver %q", cfg.Driver)
}

// pgxConfig builds the pgx connection config: from DSN when given,
// otherwise from the individual fields, with the schema applied as the
// connection's search_path either way.
func (c DBConfig) pgxConfig() (*pgx.ConnConfig, error) {
	dsn := c.DSN
	if dsn == "" {
		q := url.Values{}
		q.Set("sslmode", c.SSLMode)
		u := url.URL{
			Scheme:   "postgres",
			User:     url.UserPassword(c.User, c.Password),
			Host:     net.JoinHostPort(c.Host, strconv.Itoa(c.Port)),
			Path:     "/" + c.Database,
			RawQuery: q.Encode(),
		}
		dsn = u.String()
	}
	cc, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres connection: %w", err)
	}
	if c.DSN != "" && c.Database != "" {
		cc.Database = c.Database
	}
	if cc.RuntimeParams == nil {
		cc.RuntimeParams = map[string]string{}
	}
	cc.RuntimeParams["application_name"] = "gwatch"
	if c.Schema != "" {
		cc.RuntimeParams["search_path"] = c.Schema
	}
	return cc, nil
}

// mysqlConfig builds the go-sql-driver config the same way.
func (c DBConfig) mysqlConfig() (*mysql.Config, error) {
	var mc *mysql.Config
	if c.DSN != "" {
		var err error
		if mc, err = mysql.ParseDSN(c.DSN); err != nil {
			return nil, fmt.Errorf("mysql connection: %w", err)
		}
		if c.Database != "" {
			mc.DBName = c.Database
		}
	} else {
		mc = mysql.NewConfig()
		mc.User = c.User
		mc.Passwd = c.Password
		mc.Net = "tcp"
		mc.Addr = net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
		mc.DBName = c.Database
		switch c.SSLMode {
		case "disable":
			mc.TLSConfig = "false"
		case "", "prefer", "allow":
			mc.TLSConfig = "preferred"
		case "require":
			mc.TLSConfig = "skip-verify"
		case "verify-ca", "verify-full":
			mc.TLSConfig = "true"
		}
	}
	// RowsAffected must count matched rows, not changed ones: the store
	// treats "0 rows" from an UPDATE as "no such row", and MySQL's default
	// would report 0 for an update that happened to change nothing.
	mc.ClientFoundRows = true
	mc.MultiStatements = false
	if mc.Params == nil {
		mc.Params = map[string]string{}
	}
	// ANSI_QUOTES makes "groups" and "key" identifiers, as they are in
	// SQLite and PostgreSQL. Appending keeps the server's own strict-mode
	// settings in place.
	mc.Params["sql_mode"] = "CONCAT(@@sql_mode, ',ANSI_QUOTES')"
	return mc, nil
}

// ConnectionInfo is what TestConnection learns about a server.
type ConnectionInfo struct {
	Driver        string `json:"driver"`
	ServerVersion string `json:"serverVersion"`
	Database      string `json:"database"`
}

// TestConnection connects with cfg, runs a trivial query and reports the
// server's version. Nothing is created or written, so it is safe to call
// from the Settings page against a database GWatch is not using yet.
func TestConnection(ctx context.Context, cfg DBConfig) (ConnectionInfo, error) {
	cfg = cfg.Normalized()
	if err := cfg.Validate(); err != nil {
		return ConnectionInfo{}, err
	}
	db, err := openPool(cfg, false)
	if err != nil {
		return ConnectionInfo{}, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	info := ConnectionInfo{Driver: cfg.Driver, Database: cfg.Describe()}
	var q string
	switch cfg.Driver {
	case "sqlite":
		q = "SELECT 'SQLite ' || sqlite_version()"
	default:
		q = "SELECT version()"
	}
	if err := db.QueryRowContext(ctx, q).Scan(&info.ServerVersion); err != nil {
		return info, fmt.Errorf("connect to %s: %w", cfg.Describe(), err)
	}
	return info, nil
}

// CreateIsolated gives cfg a database of its own to work in: a schema on
// PostgreSQL, a database on MySQL, both named name and created now. The
// returned function drops it again. For SQLite, which is a file per store
// anyway, it returns cfg unchanged.
//
// This is what the test suites use to run every test against a fresh,
// empty schema on a shared server; the connecting user needs CREATE on the
// server for it to work.
func CreateIsolated(ctx context.Context, cfg DBConfig, name string) (DBConfig, func() error, error) {
	cfg = cfg.Normalized()
	noop := func() error { return nil }
	if !cfg.IsServer() {
		return cfg, noop, nil
	}
	if !isPlainIdent(name) {
		return cfg, noop, fmt.Errorf("isolated database name %q must be letters, digits and underscores", name)
	}
	admin := cfg
	admin.Schema = ""
	db, err := openPool(admin, true)
	if err != nil {
		return cfg, noop, err
	}
	db.SetMaxOpenConns(1)
	var create, drop string
	switch cfg.Driver {
	case "postgres":
		create, drop = `CREATE SCHEMA "`+name+`"`, `DROP SCHEMA "`+name+`" CASCADE`
		cfg.Schema = name
	case "mysql":
		create, drop = "CREATE DATABASE `"+name+"` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci", "DROP DATABASE `"+name+"`"
		cfg.Database = name
	}
	if _, err := db.ExecContext(ctx, create); err != nil {
		db.Close()
		return cfg, noop, fmt.Errorf("create %s: %w", name, err)
	}
	cleanup := func() error {
		defer db.Close()
		_, err := db.ExecContext(context.Background(), drop)
		return err
	}
	return cfg, cleanup, nil
}

func isPlainIdent(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

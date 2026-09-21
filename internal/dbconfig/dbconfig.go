// Package dbconfig decides which database GWatch opens. The answer comes from
// four places, and the later ones override the earlier:
//
//  1. the default: the SQLite file gwatch.db in the data directory;
//  2. database.json in the data directory, written by Settings › Database or
//     by "gwatch install" with --db-* flags, which is how a server backend
//     survives a restart (the service is started with --data-dir alone);
//  3. the GWATCH_DB_* environment variables;
//  4. the --db-* command-line flags.
//
// The password in database.json is sealed with the data directory's
// gwatch.key, the same key that protects secrets in the settings table, so
// a copy of the file on its own does not give the password away.
package dbconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jxburros/GWatch/internal/secrets"
	"github.com/jxburros/GWatch/internal/store"
)

// FileName is the persisted connection configuration in the data directory.
const FileName = "database.json"

// DBFileName is the SQLite database file in the data directory.
const DBFileName = "gwatch.db"

// Path returns where database.json lives for a data directory.
func Path(dataDir string) string { return filepath.Join(dataDir, FileName) }

// Default is the zero-configuration answer: SQLite in the data directory.
func Default(dataDir string) store.DBConfig {
	return store.DBConfig{Driver: "sqlite", Path: filepath.Join(dataDir, DBFileName), KeyFile: filepath.Join(dataDir, store.KeyFileName)}
}

// Complete fills in what a config leaves to the data directory: the SQLite
// file path when none is given, and the secrets key file, which always
// lives there.
func Complete(dataDir string, cfg store.DBConfig) store.DBConfig {
	cfg = cfg.Normalized()
	if cfg.Driver == "sqlite" && cfg.Path == "" {
		cfg.Path = filepath.Join(dataDir, DBFileName)
	}
	cfg.KeyFile = filepath.Join(dataDir, store.KeyFileName)
	return cfg
}

// Load reads database.json from dataDir and opens the sealed password. A
// missing file is not an error: it returns the SQLite default and false.
func Load(dataDir string) (cfg store.DBConfig, found bool, err error) {
	raw, err := os.ReadFile(Path(dataDir))
	if errors.Is(err, os.ErrNotExist) {
		return Default(dataDir), false, nil
	}
	if err != nil {
		return store.DBConfig{}, false, fmt.Errorf("read %s: %w", Path(dataDir), err)
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return store.DBConfig{}, true, fmt.Errorf("parse %s: %w", Path(dataDir), err)
	}
	cfg = Complete(dataDir, cfg)
	for _, field := range []*string{&cfg.Password, &cfg.DSN} {
		if !secrets.IsSealed(*field) {
			continue
		}
		box, err := secrets.Load(cfg.KeyFile)
		if err != nil {
			return store.DBConfig{}, true, err
		}
		v, err := box.Open(*field)
		if err != nil {
			return store.DBConfig{}, true, fmt.Errorf("%s: the database password cannot be decrypted with %s: %w", Path(dataDir), cfg.KeyFile, err)
		}
		*field = v
	}
	return cfg, true, nil
}

// Save writes cfg to database.json in dataDir, sealing the password (and a
// connection string, which may carry one) with the data directory's key
// file, creating the key if this is a fresh install. The file is written
// beside its final name and renamed into place, and is readable by the
// owner only.
func Save(dataDir string, cfg store.DBConfig) error {
	cfg = cfg.Normalized()
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	box, err := secrets.Load(filepath.Join(dataDir, store.KeyFileName))
	if err != nil {
		return err
	}
	for _, field := range []*string{&cfg.Password, &cfg.DSN} {
		if *field == "" || secrets.IsSealed(*field) {
			continue
		}
		if *field, err = box.Seal(*field); err != nil {
			return fmt.Errorf("seal database password: %w", err)
		}
	}
	// What the file stores is the connection, not where the key lives or
	// (for SQLite) the default file path, both of which follow the data
	// directory.
	cfg.KeyFile = ""
	if cfg.Driver == "sqlite" && cfg.Path == filepath.Join(dataDir, DBFileName) {
		cfg.Path = ""
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	path := Path(dataDir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename %s: %w", tmp, err)
	}
	return nil
}

// Remove deletes database.json, taking the install back to the SQLite
// default at the next start. A missing file is fine.
func Remove(dataDir string) error {
	err := os.Remove(Path(dataDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Overrides are connection fields given on the command line or in the
// environment. An empty field means "not given" and leaves the underlying
// value alone; Port is a string so that "not given" and 0 stay distinct.
type Overrides struct {
	Driver, DSN, Path, Host, Port, User, Password, Database, Schema, SSLMode string
}

// Empty reports whether nothing was given.
func (o Overrides) Empty() bool { return o == Overrides{} }

// Apply lays the overrides over cfg.
func (o Overrides) Apply(cfg store.DBConfig) (store.DBConfig, error) {
	set := func(dst *string, v string) {
		if v != "" {
			*dst = v
		}
	}
	set(&cfg.Driver, o.Driver)
	set(&cfg.DSN, o.DSN)
	set(&cfg.Path, o.Path)
	set(&cfg.Host, o.Host)
	set(&cfg.User, o.User)
	set(&cfg.Password, o.Password)
	set(&cfg.Database, o.Database)
	set(&cfg.Schema, o.Schema)
	set(&cfg.SSLMode, o.SSLMode)
	if o.Port != "" {
		n, err := strconv.Atoi(strings.TrimSpace(o.Port))
		if err != nil || n <= 0 || n > 65535 {
			return cfg, fmt.Errorf("database port %q is not a port number", o.Port)
		}
		cfg.Port = n
	}
	return cfg, nil
}

// EnvVars are the environment variables FromEnv reads, in Overrides' field
// order. GWATCH_DB_PASSWORD is the recommended way to pass a password from a
// service manager or container, since a --db-password flag shows up in the
// process list.
var EnvVars = []string{"GWATCH_DB_DRIVER", "GWATCH_DB_DSN", "GWATCH_DB_PATH", "GWATCH_DB_HOST", "GWATCH_DB_PORT", "GWATCH_DB_USER", "GWATCH_DB_PASSWORD", "GWATCH_DB_NAME", "GWATCH_DB_SCHEMA", "GWATCH_DB_SSLMODE"}

// FromEnv reads the GWATCH_DB_* variables.
func FromEnv() Overrides {
	get := func(k string) string { return strings.TrimSpace(os.Getenv(k)) }
	return Overrides{
		Driver: get("GWATCH_DB_DRIVER"), DSN: get("GWATCH_DB_DSN"), Path: get("GWATCH_DB_PATH"),
		Host: get("GWATCH_DB_HOST"), Port: get("GWATCH_DB_PORT"), User: get("GWATCH_DB_USER"),
		Password: get("GWATCH_DB_PASSWORD"), Database: get("GWATCH_DB_NAME"), Schema: get("GWATCH_DB_SCHEMA"),
		SSLMode: get("GWATCH_DB_SSLMODE"),
	}
}

// Resolve is the whole precedence in one call: database.json under the
// environment under the flags, completed for the data directory. It also
// reports whether database.json was found, so the caller can say where the
// answer came from.
func Resolve(dataDir string, flags Overrides) (cfg store.DBConfig, fromFile bool, err error) {
	cfg, fromFile, err = Load(dataDir)
	if err != nil {
		return cfg, fromFile, err
	}
	if cfg, err = FromEnv().Apply(cfg); err != nil {
		return cfg, fromFile, err
	}
	if cfg, err = flags.Apply(cfg); err != nil {
		return cfg, fromFile, err
	}
	cfg = Complete(dataDir, cfg)
	return cfg, fromFile, cfg.Validate()
}

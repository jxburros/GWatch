package dbconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jxburros/GWatch/internal/secrets"
	"github.com/jxburros/GWatch/internal/store"
)

func TestDefaultIsSQLiteInDataDir(t *testing.T) {
	dir := t.TempDir()
	cfg, found, err := Load(dir)
	if err != nil || found {
		t.Fatalf("no file: %v found=%v", err, found)
	}
	if cfg.Driver != "sqlite" || cfg.Path != filepath.Join(dir, DBFileName) || cfg.KeyFile != filepath.Join(dir, store.KeyFileName) {
		t.Fatalf("default = %+v", cfg)
	}
}

// Saving seals the password with the data directory's key; loading opens
// it again. The file itself never holds the password in the clear.
func TestSaveSealsPasswordAndLoadOpensIt(t *testing.T) {
	dir := t.TempDir()
	in := store.DBConfig{Driver: "postgres", Host: "db.lan", Port: 5432, User: "gwatch", Password: "s3cret", Database: "gwatch", Schema: "gw", SSLMode: "require"}
	if err := Save(dir, in); err != nil {
		t.Fatalf("save: %v", err)
	}
	raw, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "s3cret") || !strings.Contains(string(raw), secrets.Prefix) {
		t.Fatalf("password not sealed on disk: %s", raw)
	}
	if strings.Contains(string(raw), "keyFile") || strings.Contains(string(raw), "KeyFile") {
		t.Fatalf("the key file path must not be stored: %s", raw)
	}
	if fi, _ := os.Stat(Path(dir)); fi.Mode().Perm()&0o077 != 0 && os.Getenv("GOOS") != "windows" && fi.Mode().Perm() != 0o600 {
		t.Logf("database.json mode %v (0600 expected on Unix)", fi.Mode().Perm())
	}
	out, found, err := Load(dir)
	if err != nil || !found {
		t.Fatalf("load: %v found=%v", err, found)
	}
	in.KeyFile = filepath.Join(dir, store.KeyFileName)
	if out != in {
		t.Fatalf("round trip:\n got %+v\nwant %+v", out, in)
	}
	// Saving SQLite removes nothing by itself but stores no path, and
	// Remove takes the install back to the default.
	if err := Save(dir, store.DBConfig{Driver: "sqlite", Path: filepath.Join(dir, DBFileName)}); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(Path(dir))
	if strings.Contains(string(raw), "path") {
		t.Fatalf("the default SQLite path should not be written down: %s", raw)
	}
	if err := Remove(dir); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := Load(dir); found {
		t.Fatal("file still there after Remove")
	}
	if err := Remove(dir); err != nil {
		t.Fatalf("removing twice: %v", err)
	}
}

// Flags beat the environment, which beats database.json, which beats the
// default — field by field.
func TestResolvePrecedence(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, store.DBConfig{Driver: "postgres", Host: "file-host", Port: 5433, User: "file-user", Password: "file-pw", Database: "file-db"}); err != nil {
		t.Fatal(err)
	}
	for _, v := range EnvVars {
		t.Setenv(v, "")
	}
	t.Setenv("GWATCH_DB_HOST", "env-host")
	t.Setenv("GWATCH_DB_PASSWORD", "env-pw")

	cfg, fromFile, err := Resolve(dir, Overrides{User: "flag-user", Port: "5434"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !fromFile || cfg.Driver != "postgres" || cfg.Host != "env-host" || cfg.Password != "env-pw" || cfg.User != "flag-user" || cfg.Port != 5434 || cfg.Database != "file-db" {
		t.Fatalf("resolved = %+v (fromFile=%v)", cfg, fromFile)
	}
	if cfg.KeyFile != filepath.Join(dir, store.KeyFileName) {
		t.Fatalf("key file = %q", cfg.KeyFile)
	}

	// A bad port is refused rather than silently ignored.
	if _, _, err := Resolve(dir, Overrides{Port: "lots"}); err == nil {
		t.Fatal("expected an error for a non-numeric port")
	}
	// Without the file, the environment alone can name a server.
	t.Setenv("GWATCH_DB_DRIVER", "mysql")
	t.Setenv("GWATCH_DB_USER", "u")
	t.Setenv("GWATCH_DB_NAME", "d")
	cfg, fromFile, err = Resolve(t.TempDir(), Overrides{})
	if err != nil || fromFile || cfg.Driver != "mysql" || cfg.Port != store.DefaultMySQLPort || cfg.Host != "env-host" {
		t.Fatalf("env only: %+v fromFile=%v %v", cfg, fromFile, err)
	}
	// And with nothing at all, SQLite.
	for _, v := range EnvVars {
		t.Setenv(v, "")
	}
	empty := t.TempDir()
	cfg, fromFile, err = Resolve(empty, Overrides{})
	if err != nil || fromFile || cfg.Driver != "sqlite" || cfg.Path != filepath.Join(empty, DBFileName) {
		t.Fatalf("default: %+v fromFile=%v %v", cfg, fromFile, err)
	}
}

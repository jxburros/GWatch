package store

import (
	"testing"
)

// A server config that names no TLS mode verifies the certificate when the
// server is across a network, and keeps prefer only for one on this machine.
func TestServerTLSDefaultsToVerifiedOffHost(t *testing.T) {
	for _, tc := range []struct {
		driver, host, want string
	}{
		{"postgres", "db.example.lan", "verify-full"},
		{"postgres", "10.0.0.5", "verify-full"},
		{"postgres", "2001:db8::1", "verify-full"},
		{"mysql", "db.example.lan", "verify-full"},
		{"mariadb", "192.168.1.20", "verify-full"},
		{"postgres", "", "prefer"}, // filled in as localhost
		{"postgres", "localhost", "prefer"},
		{"postgres", "127.0.0.1", "prefer"},
		{"postgres", "::1", "prefer"},
		{"postgres", "/var/run/postgresql", "prefer"},
		{"mysql", "localhost", "prefer"},
		{"mysql", "127.0.0.2", "prefer"},
	} {
		got := DBConfig{Driver: tc.driver, Host: tc.host}.Normalized().SSLMode
		if got != tc.want {
			t.Errorf("%s at %q: sslMode %q, want %q", tc.driver, tc.host, got, tc.want)
		}
	}
}

// A mode set explicitly is honoured as given, weak or not, so a config that
// already says prefer, require or disable keeps working.
func TestExplicitTLSModeIsKept(t *testing.T) {
	for _, mode := range []string{"disable", "allow", "prefer", "require", "verify-ca", "verify-full"} {
		c := DBConfig{Driver: "postgres", Host: "db.example.lan", User: "u", Database: "d", SSLMode: mode}.Normalized()
		if c.SSLMode != mode {
			t.Errorf("explicit %q became %q", mode, c.SSLMode)
		}
		if err := c.Validate(); err != nil {
			t.Errorf("explicit %q rejected: %v", mode, err)
		}
	}
}

// The default reaches the drivers: pgx is asked for verify-full, and the MySQL
// driver for a verified connection rather than "preferred".
func TestRemoteDefaultReachesTheDrivers(t *testing.T) {
	c := DBConfig{Driver: "postgres", Host: "db.example.lan", User: "u", Password: "p", Database: "d"}.Normalized()
	cc, err := c.pgxConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cc.TLSConfig == nil || cc.TLSConfig.InsecureSkipVerify || cc.TLSConfig.ServerName != "db.example.lan" {
		t.Fatalf("postgres TLS not verified: %+v", cc.TLSConfig)
	}
	if len(cc.Fallbacks) != 0 {
		t.Fatalf("postgres keeps %d plaintext fallbacks", len(cc.Fallbacks))
	}

	m := DBConfig{Driver: "mysql", Host: "db.example.lan", User: "u", Password: "p", Database: "d"}.Normalized()
	mc, err := m.mysqlConfig()
	if err != nil {
		t.Fatal(err)
	}
	if mc.TLSConfig != "true" {
		t.Fatalf("mysql tls = %q, want true", mc.TLSConfig)
	}
}

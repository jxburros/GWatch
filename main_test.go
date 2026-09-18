package main

import (
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestValidateListenAcceptsLoopback(t *testing.T) {
	ok := []string{
		"127.0.0.1:8080",
		"localhost:8080",
		"LocalHost:9000",
		"127.5.6.7:1",
		"[::1]:8080",
		":8080", // empty host binds every interface, but the server still only serves locally
	}
	for _, addr := range ok {
		if err := validateListen(addr); err != nil {
			t.Errorf("validateListen(%q) = %v, want nil", addr, err)
		}
	}
}

func TestValidateListenRejectsRemote(t *testing.T) {
	// GWatch is local-only; binding to a routable address would expose the
	// unauthenticated API to the network.
	bad := []string{
		"0.0.0.0:8080",
		"192.168.1.10:8080",
		"10.0.0.1:8080",
		"[::]:8080",
		"example.com:8080",
	}
	for _, addr := range bad {
		err := validateListen(addr)
		if err == nil {
			t.Errorf("validateListen(%q) = nil, want a local-only error", addr)
			continue
		}
		if !strings.Contains(err.Error(), "local-only") {
			t.Errorf("validateListen(%q) = %v, want a local-only error", addr, err)
		}
	}
}

func TestValidateListenRejectsMalformed(t *testing.T) {
	for _, addr := range []string{"", "127.0.0.1", "not an address"} {
		err := validateListen(addr)
		if err == nil || !strings.Contains(err.Error(), "host:port") {
			t.Errorf("validateListen(%q) = %v, want a host:port error", addr, err)
		}
	}
}

func TestBrowserHost(t *testing.T) {
	cases := []struct{ in, want string }{
		{"127.0.0.1:8080", "127.0.0.1:8080"},
		{"0.0.0.0:8080", "127.0.0.1:8080"},
		{":8080", "127.0.0.1:8080"},
		{"[::]:8080", "127.0.0.1:8080"},
		{"localhost:9000", "localhost:9000"},
		{"garbage", "garbage"}, // unparseable input is passed through unchanged
	}
	for _, c := range cases {
		if got := browserHost(c.in); got != c.want {
			t.Errorf("browserHost(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestEnvOr(t *testing.T) {
	const key = "GWATCH_TEST_ENVOR"
	if got := envOr(key, "fallback"); got != "fallback" {
		t.Errorf("unset = %q, want fallback", got)
	}
	t.Setenv(key, "  ")
	if got := envOr(key, "fallback"); got != "fallback" {
		t.Errorf("blank = %q, want fallback", got)
	}
	t.Setenv(key, "  value  ")
	if got := envOr(key, "fallback"); got != "value" {
		t.Errorf("set = %q, want trimmed value", got)
	}
}

func TestDefaultDataDir(t *testing.T) {
	dir := defaultDataDir()
	if dir == "" || !filepath.IsAbs(dir) && dir != "data" {
		t.Errorf("defaultDataDir() = %q, want an absolute path or the %q fallback", dir, "data")
	}
	if runtime.GOOS == "windows" {
		t.Setenv("ProgramData", `C:\PD`)
		if got := defaultDataDir(); got != filepath.Join(`C:\PD`, "GWatch") {
			t.Errorf("defaultDataDir() = %q", got)
		}
		return
	}
	t.Setenv("XDG_DATA_HOME", "/xdg")
	if got := defaultDataDir(); got != filepath.Join("/xdg", "gwatch") {
		t.Errorf("defaultDataDir() with XDG_DATA_HOME = %q", got)
	}
}

// The web UI is compiled into the binary, so a missing or renamed asset is a
// build-time bug that only shows up as a blank page at runtime.
func TestEmbeddedWebAssets(t *testing.T) {
	for _, name := range []string{
		"web/index.html",
		"web/app.js",
		"web/app.css",
		"web/api.js",
		"web/components.js",
		"web/views/dashboard.js",
		"web/views/nodes.js",
		"web/views/incidents.js",
		"web/views/settings.js",
		"web/views/wallboard.js",
	} {
		b, err := fs.ReadFile(webFiles, name)
		if err != nil {
			t.Errorf("embedded %s: %v", name, err)
			continue
		}
		if len(b) == 0 {
			t.Errorf("embedded %s is empty", name)
		}
	}
	if b, err := fs.ReadFile(webFiles, "web/index.html"); err == nil && !strings.Contains(string(b), "<html") {
		t.Errorf("web/index.html does not look like a page: %.80q", b)
	}
}

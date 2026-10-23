//go:build linux

package svc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStatName(t *testing.T) {
	for in, want := range map[string]string{
		"1 (systemd) S 0 1 1 0 -1":        "systemd",
		"812 (my (odd) prog) S 1 812 812": "my (odd) prog",
		"garbage":                         "",
	} {
		if got := statName(in); got != want {
			t.Errorf("statName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInContainer(t *testing.T) {
	dir := t.TempDir()
	for name, tc := range map[string]struct {
		text string
		want bool
	}{
		"docker":  {"12:cpuset:/docker/0123abcd\n0::/\n", true},
		"lxc":     {"0::/lxc.payload.c1\n", true},
		"host":    {"0::/init.scope\n", false},
		"missing": {"", false},
	} {
		p := filepath.Join(dir, name)
		if name != "missing" {
			if err := os.WriteFile(p, []byte(tc.text), 0644); err != nil {
				t.Fatal(err)
			}
		}
		if got := inContainer(p); got != tc.want {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
}

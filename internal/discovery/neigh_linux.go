//go:build linux

package discovery

import (
	"os"
	"path/filepath"
	"strings"
)

// arpTable and fsRoot are the paths read on Linux; the tests move them.
var (
	arpTable = "/proc/net/arp"
	fsRoot   = "/"
)

func neighbourTable() []neighbour {
	b, err := os.ReadFile(arpTable)
	if err != nil {
		return nil
	}
	return parseProcNetARP(string(b))
}

// containerKind reports whether GWatch runs in a container, by the markers
// the runtimes leave on purpose (Docker's /.dockerenv, Podman's
// /run/.containerenv, systemd's /run/systemd/container) or, failing those,
// the cgroup of pid 1. It is "" on an ordinary machine.
func containerKind() string {
	if _, err := os.Stat(filepath.Join(fsRoot, ".dockerenv")); err == nil {
		return "Docker"
	}
	if _, err := os.Stat(filepath.Join(fsRoot, "run", ".containerenv")); err == nil {
		return "Podman"
	}
	if b, err := os.ReadFile(filepath.Join(fsRoot, "run", "systemd", "container")); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return s
		}
	}
	if b, err := os.ReadFile(filepath.Join(fsRoot, "proc", "1", "cgroup")); err == nil {
		s := string(b)
		switch {
		case strings.Contains(s, "kubepods"):
			return "Kubernetes"
		case strings.Contains(s, "docker"):
			return "Docker"
		case strings.Contains(s, "libpod"):
			return "Podman"
		}
	}
	return ""
}

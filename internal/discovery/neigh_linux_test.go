//go:build linux

package discovery

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNeighbourTableAndContainerOnLinux(t *testing.T) {
	dir := t.TempDir()
	arp := filepath.Join(dir, "arp")
	if err := os.WriteFile(arp, []byte(procNetARP), 0o644); err != nil {
		t.Fatal(err)
	}
	oldARP, oldRoot := arpTable, fsRoot
	t.Cleanup(func() { arpTable, fsRoot = oldARP, oldRoot })
	arpTable, fsRoot = arp, dir

	if got := neighbourTable(); len(got) != 3 {
		t.Errorf("neighbour table: %+v", got)
	}
	if k := containerKind(); k != "" {
		t.Errorf("an empty root is no container, got %q", k)
	}
	if err := os.WriteFile(filepath.Join(dir, ".dockerenv"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if k := containerKind(); k != "Docker" {
		t.Errorf("container kind = %q", k)
	}
	n := containerNotes([]Responder{{IP: "192.168.1.1"}})
	if len(n) != 1 || !strings.Contains(n[0], "Docker") {
		t.Errorf("notes: %v", n)
	}
}

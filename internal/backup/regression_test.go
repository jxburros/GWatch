package backup

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestScheduledPrunePreservesManualAndLegacyArchives(t *testing.T) {
	dir := t.TempDir()
	manual := []string{"gwatch-backup-20200101-000000-full.gwbackup", "gwatch-backup-20200102-000000.gwbackup"}
	for _, name := range append(manual, "gwatch-auto-20200101-000000.gwbackup", "gwatch-auto-20200102-000000.gwbackup") {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("archive"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := PruneScheduled(dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 {
		t.Fatalf("removed %v", removed)
	}
	for _, name := range manual {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("manual backup deleted: %s", name)
		}
	}
}

func TestEncryptedArchiveRejectsChunkManipulation(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewEncryptWriter(&buf, "password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write(bytes.Repeat([]byte("x"), 2*chunkSize)); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	header := len(magic) + 1 + saltSize
	boundary := header + 4 + 12 + int(binary.BigEndian.Uint32(data[header:]))
	secondEnd := boundary + 4 + 12 + int(binary.BigEndian.Uint32(data[boundary:]))
	variants := map[string][]byte{
		"truncated-at-boundary": append([]byte{}, data[:boundary]...),
		"dropped-chunk":         append(append([]byte{}, data[:header]...), data[boundary:]...),
		"reordered-chunks":      append(append(append(append([]byte{}, data[:header]...), data[boundary:secondEnd]...), data[header:boundary]...), data[secondEnd:]...),
	}
	for name, damaged := range variants {
		t.Run(name, func(t *testing.T) {
			r, err := NewDecryptReader(bytes.NewReader(damaged), "password")
			if err == nil {
				_, err = io.ReadAll(r)
			}
			if err == nil {
				t.Fatal("tampered archive accepted")
			}
		})
	}
}

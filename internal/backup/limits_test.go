package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store/storetest"
)

// craft writes an encrypted archive holding the given entries verbatim.
func craft(t *testing.T, entries map[string][]byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "crafted.gwbackup")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := NewEncryptWriter(f, "pw")
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(enc)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func lower[T any](t *testing.T, p *T, v T) {
	old := *p
	*p = v
	t.Cleanup(func() { *p = old })
}

func resultLines(n int) []byte {
	var b bytes.Buffer
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `{"checkId":1,"nodeId":1,"ok":true,"timestamp":"2024-01-01T00:00:%02dZ"}`+"\n", i%60)
	}
	return b.Bytes()
}

// TestRestoreRejectsOversizedArchivesBeforeWriting covers the limits a
// restore enforces on the archive and its contents: each case must fail with
// ErrArchiveTooLarge and leave the database exactly as it was.
func TestRestoreRejectsOversizedArchivesBeforeWriting(t *testing.T) {
	cfg := mustJSON(t, Config{Nodes: []model.Node{{ID: 99, Name: "from archive", Enabled: true}}})
	cases := map[string]struct {
		setup   func(t *testing.T)
		entries func(t *testing.T) map[string][]byte
	}{
		"archive-file": {
			setup: func(t *testing.T) { lower(t, &maxArchiveBytes, 64) },
			entries: func(t *testing.T) map[string][]byte {
				return map[string][]byte{"manifest.json": mustJSON(t, Manifest{Format: Format}), "config.json": cfg}
			},
		},
		"config-json-bomb": {
			setup: func(t *testing.T) { lower(t, &maxConfigBytes, 4<<10) },
			entries: func(t *testing.T) map[string][]byte {
				// Highly compressible: a few hundred bytes in the ZIP.
				big := `{"nodes":[],"settings":{},"pad":"` + strings.Repeat("A", 1<<20) + `"}`
				return map[string][]byte{"manifest.json": mustJSON(t, Manifest{Format: Format}), "config.json": []byte(big)}
			},
		},
		"too-many-entries": {
			entries: func(t *testing.T) map[string][]byte {
				e := map[string][]byte{"manifest.json": mustJSON(t, Manifest{Format: Format}), "config.json": cfg}
				for i := 0; i <= maxEntries; i++ {
					e[fmt.Sprintf("junk-%d", i)] = []byte("x")
				}
				return e
			},
		},
		"manifest-claims-too-much": {
			entries: func(t *testing.T) map[string][]byte {
				return map[string][]byte{"manifest.json": mustJSON(t, Manifest{Format: Format, IncludeHistory: true, Results: maxHistoryRecords + 1}), "config.json": cfg, "results.jsonl": resultLines(1)}
			},
		},
		"more-history-than-claimed": {
			entries: func(t *testing.T) map[string][]byte {
				return map[string][]byte{"manifest.json": mustJSON(t, Manifest{Format: Format, IncludeHistory: true, Results: 2}), "config.json": cfg, "results.jsonl": resultLines(10)}
			},
		},
		"history-bomb": {
			setup: func(t *testing.T) { lower(t, &maxHistoryBytes, 8<<10) },
			entries: func(t *testing.T) map[string][]byte {
				return map[string][]byte{"manifest.json": mustJSON(t, Manifest{Format: Format, IncludeHistory: true, Results: 5000}), "config.json": cfg, "results.jsonl": resultLines(5000)}
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if tc.setup != nil {
				tc.setup(t)
			}
			ctx := context.Background()
			st := storetest.Open(t)
			n, err := st.CreateNode(ctx, model.Node{Name: "keep me", Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			path := craft(t, tc.entries(t))
			if _, err := Restore(ctx, st, path, "pw", true); !errors.Is(err, ErrArchiveTooLarge) {
				t.Fatalf("restore error = %v, want ErrArchiveTooLarge", err)
			}
			nodes, err := st.ListNodes(ctx)
			if err != nil || len(nodes) != 1 || nodes[0].ID != n.ID || nodes[0].Name != n.Name {
				t.Fatalf("database changed: %+v %v", nodes, err)
			}
		})
	}
}

func TestCapReader(t *testing.T) {
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(&capReader{r: strings.NewReader("12345"), n: 5}); err != nil {
		t.Fatalf("exact-size source rejected: %v", err)
	}
	if _, err := buf.ReadFrom(&capReader{r: strings.NewReader("123456"), n: 5}); !errors.Is(err, ErrArchiveTooLarge) {
		t.Fatalf("oversized source: %v", err)
	}
}

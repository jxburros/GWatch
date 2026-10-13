package backup

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store/storetest"
)

func TestRestorePreservesUnselectedHistory(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		archiveHistory, restoreHistory bool
	}{
		{"config archive", false, false}, {"config archive with history requested", false, true},
		{"full archive with history unselected", true, false}, {"full restore", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st := storetest.Open(t)
			seedFull(t, ctx, st)
			nodes, err := st.ListNodes(ctx)
			if err != nil {
				t.Fatal(err)
			}
			n := nodes[0]
			if err := st.RestoreIncidents(ctx, []model.Incident{{NodeID: n.ID, State: "open", OpenedAt: time.Now(), Notes: nil}}); err != nil {
				t.Fatal(err)
			}
			before, err := st.ExportIncidents(ctx)
			if err != nil {
				t.Fatal(err)
			}
			agents, err := st.ExportAgents(ctx)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			info, err := Create(ctx, st, dir, "pw", tc.archiveHistory, "test")
			if err != nil {
				t.Fatal(err)
			}
			// New readings after the backup must survive a config-only restore, while
			// an explicit full restore must replace them with the archived readings.
			if _, err := st.InsertResult(ctx, model.Result{CheckID: n.Checks[0].ID, Timestamp: time.Now(), Status: model.StatusUp, Success: true}); err != nil {
				t.Fatal(err)
			}
			if _, err := st.InsertEvent(ctx, model.Event{Type: model.EventDown, Title: "after backup"}); err != nil {
				t.Fatal(err)
			}
			results := 0
			if err := st.AllResults(ctx, func(model.Result) error { results++; return nil }); err != nil {
				t.Fatal(err)
			}
			events := 0
			if err := st.AllEvents(ctx, func(model.Event) error { events++; return nil }); err != nil {
				t.Fatal(err)
			}
			var rollups []model.Rollup
			if err := st.AllRollups(ctx, func(r model.Rollup) error { rollups = append(rollups, r); return nil }); err != nil {
				t.Fatal(err)
			}
			n.Name = "changed"
			n.Checks = append(n.Checks, model.Check{Type: model.CheckTCP, Name: "new check", Enabled: true, IntervalSeconds: 60})
			updated, _, err := st.UpdateNode(ctx, n)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.InsertResult(ctx, model.Result{CheckID: updated.Checks[1].ID, Timestamp: time.Now(), Status: model.StatusUp, Success: true}); err != nil {
				t.Fatal(err)
			}
			if _, err := st.CreateNode(ctx, model.Node{Name: "new node", Enabled: true}); err != nil {
				t.Fatal(err)
			}
			// Repeat restores must not duplicate incidents or disconnect an agent.
			for i := 0; i < 2; i++ {
				sum, err := Restore(ctx, st, filepath.Join(dir, info.FileName), "pw", tc.restoreHistory)
				if err != nil {
					t.Fatal(err)
				}
				full := tc.archiveHistory && tc.restoreHistory
				if sum.History != full {
					t.Fatalf("history = %v", sum.History)
				}
				got, err := st.ListNodes(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, nodes) {
					t.Fatalf("configuration not restored: %+v", got)
				}
				gotResults, gotEvents := 0, 0
				if err := st.AllResults(ctx, func(model.Result) error { gotResults++; return nil }); err != nil {
					t.Fatal(err)
				}
				if err := st.AllEvents(ctx, func(model.Event) error { gotEvents++; return nil }); err != nil {
					t.Fatal(err)
				}
				wantResults, wantEvents := results, events
				if full {
					wantResults--
					wantEvents--
				}
				if gotResults != wantResults || gotEvents != wantEvents {
					t.Fatalf("history results/events = %d/%d, want %d/%d", gotResults, gotEvents, wantResults, wantEvents)
				}
				var gotRollups []model.Rollup
				if err := st.AllRollups(ctx, func(r model.Rollup) error { gotRollups = append(gotRollups, r); return nil }); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(gotRollups, rollups) {
					t.Fatal("rollups changed")
				}
				incidents, err := st.ExportIncidents(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if len(incidents) != len(before) || incidents[0].NodeID != before[0].NodeID || incidents[0].State != before[0].State {
					t.Fatalf("incidents = %+v", incidents)
				}
				gotAgents, err := st.ExportAgents(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(gotAgents, agents) {
					t.Fatalf("agents changed: %+v", gotAgents)
				}
			}
		})
	}
}

func TestRestoreRejectsUnsupportedFormatBeforeWriting(t *testing.T) {
	for _, format := range []int{0, Format + 1} {
		t.Run(fmt.Sprint(format), func(t *testing.T) {
			ctx := context.Background()
			st := storetest.Open(t)
			n, err := st.CreateNode(ctx, model.Node{Name: "keep me", Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "future.gwbackup")
			f, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			enc, err := NewEncryptWriter(f, "pw")
			if err != nil {
				t.Fatal(err)
			}
			zw := zip.NewWriter(enc)
			for name, value := range map[string]any{"manifest.json": Manifest{Format: format}, "config.json": Config{}} {
				w, err := zw.Create(name)
				if err != nil {
					t.Fatal(err)
				}
				if err := json.NewEncoder(w).Encode(value); err != nil {
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
			if _, err := Inspect(path, "pw"); err == nil {
				t.Fatal("inspect accepted unsupported format")
			}
			if _, err := Restore(ctx, st, path, "pw", true); err == nil {
				t.Fatal("restore accepted unsupported format")
			}
			if got, err := st.GetNode(ctx, n.ID); err != nil || got.Name != n.Name {
				t.Fatalf("database changed: %+v %v", got, err)
			}
		})
	}
}

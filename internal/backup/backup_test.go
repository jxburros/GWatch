package backup

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store/storetest"
)

func TestEncryptRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewEncryptWriter(&buf, "secret")
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("0123456789abcdef"), 200000) // > 2 chunks
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := NewDecryptReader(bytes.NewReader(buf.Bytes()), "secret")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: %d vs %d", len(got), len(payload))
	}
	r, err = NewDecryptReader(bytes.NewReader(buf.Bytes()), "wrong")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(r); err != ErrBadPassword {
		t.Fatalf("expected bad password, got %v", err)
	}
	// truncated
	r, _ = NewDecryptReader(bytes.NewReader(buf.Bytes()[:len(buf.Bytes())-10]), "secret")
	if _, err := io.ReadAll(r); err == nil {
		t.Fatal("expected truncation error")
	}
}

func TestBackupRestore(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st := storetest.Open(t)
	gw, err := st.CreateNode(ctx, model.Node{Name: "Gateway", Host: "192.168.1.1", Group: "Home Network", Enabled: true, Checks: []model.Check{{Type: model.CheckPing, Name: "Ping", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5}}})
	if err != nil {
		t.Fatal(err)
	}
	plex, err := st.CreateNode(ctx, model.Node{Name: "Plex", Host: "192.168.1.10", Group: "Media", Enabled: true, DependsOnNode: &gw.ID, Checks: []model.Check{{Type: model.CheckTCP, Name: "TCP 32400", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5, Config: model.CheckConfig{Port: 32400}}}})
	if err != nil {
		t.Fatal(err)
	}
	lat := 12.5
	for i := 0; i < 10; i++ {
		if _, err := st.InsertResult(ctx, model.Result{CheckID: gw.Checks[0].ID, Timestamp: time.Now().Add(-time.Duration(i) * time.Minute), Success: true, Status: model.StatusUp, LatencyMS: &lat, Attempts: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.RollupFromRaw(ctx, time.Now().Add(-time.Hour), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertEvent(ctx, model.Event{Type: model.EventDown, Title: "x", NodeID: &plex.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveDashboard(ctx, model.Dashboard{Name: "Overview", Widgets: []model.Widget{{Type: "summary", Width: 4, Height: 1}}}); err != nil {
		t.Fatal(err)
	}
	settings, _ := st.LoadSettings(ctx)
	settings.Alerts.Recipients = []string{"me@example.com"}
	_ = st.SaveSettings(ctx, settings)

	bdir := filepath.Join(dir, "backups")
	info, err := Create(ctx, st, bdir, "pw", true, "test")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if info.SizeBytes == 0 || !ValidFileName(info.FileName) {
		t.Fatalf("bad info %+v", info)
	}
	list, _ := List(bdir)
	if len(list) != 1 {
		t.Fatalf("list: %+v", list)
	}
	m, err := Inspect(filepath.Join(bdir, info.FileName), "pw")
	if err != nil || m.Nodes != 2 || m.Checks != 2 || m.Results != 10 || m.Events != 1 {
		t.Fatalf("inspect: %v %+v", err, m)
	}
	if _, err := Inspect(filepath.Join(bdir, info.FileName), "nope"); err == nil {
		t.Fatal("expected wrong password error")
	}
	st.Close()

	// restore into a fresh database
	st2 := storetest.Open(t)
	sum, err := Restore(ctx, st2, filepath.Join(bdir, info.FileName), "pw", true)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if sum.Nodes != 2 || sum.Checks != 2 || sum.Results != 10 || sum.Events != 1 || sum.Rollups == 0 {
		t.Fatalf("summary: %+v", sum)
	}
	nodes, _ := st2.ListNodes(ctx)
	if len(nodes) != 2 {
		t.Fatalf("nodes: %+v", nodes)
	}
	var restoredPlex model.Node
	for _, n := range nodes {
		if n.Name == "Plex" {
			restoredPlex = n
		}
	}
	if restoredPlex.DependsOnNode == nil || *restoredPlex.DependsOnNode != gw.ID || restoredPlex.Checks[0].ID != plex.Checks[0].ID {
		t.Fatalf("ids/deps not preserved: %+v", restoredPlex)
	}
	res, _ := st2.RecentResults(ctx, gw.Checks[0].ID, 100)
	if len(res) != 10 {
		t.Fatalf("results: %d", len(res))
	}
	s2, _ := st2.LoadSettings(ctx)
	if len(s2.Alerts.Recipients) != 1 {
		t.Fatalf("settings not restored: %+v", s2)
	}
	ds, _ := st2.ListDashboards(ctx)
	if len(ds) != 1 {
		t.Fatalf("dashboards: %+v", ds)
	}
	// config-only restore keeps ids too
	st3 := storetest.Open(t)
	sum3, err := Restore(ctx, st3, filepath.Join(bdir, info.FileName), "pw", false)
	if err != nil || sum3.Results != 0 || sum3.Nodes != 2 {
		t.Fatalf("config-only restore: %v %+v", err, sum3)
	}
}

func TestPrune(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st := storetest.Open(t)
	bdir := filepath.Join(dir, "backups")

	// Nothing to prune yet.
	if removed, err := Prune(bdir, 3); err != nil || len(removed) != 0 {
		t.Fatalf("prune empty dir: %v %+v", err, removed)
	}

	// Backup archive names have second resolution, so create each under a
	// unique name (renaming right after Create) to avoid collisions, and
	// give each a distinct mtime so List/Prune ordering is deterministic.
	var names []string
	for i := 0; i < 5; i++ {
		info, err := Create(ctx, st, bdir, "pw", false, "test")
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		src := filepath.Join(bdir, info.FileName)
		name := fmt.Sprintf("gwatch-backup-prune-%d%s", i, Extension)
		dst := filepath.Join(bdir, name)
		if src != dst {
			if err := os.Rename(src, dst); err != nil {
				t.Fatal(err)
			}
		}
		names = append(names, name)
		mt := time.Now().Add(time.Duration(i) * time.Second)
		_ = os.Chtimes(dst, mt, mt)
	}
	list, err := List(bdir)
	if err != nil || len(list) != 5 {
		t.Fatalf("list before prune: %v %+v", err, list)
	}
	// Keep the 2 newest (last created, highest mtime).
	removed, err := Prune(bdir, 2)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if len(removed) != 3 {
		t.Fatalf("expected 3 removed, got %+v", removed)
	}
	list, err = List(bdir)
	if err != nil || len(list) != 2 {
		t.Fatalf("list after prune: %v %+v", err, list)
	}
	// The two newest (by mtime, i.e. the last two created) must remain.
	remaining := map[string]bool{list[0].FileName: true, list[1].FileName: true}
	for _, want := range names[3:] {
		if !remaining[want] {
			t.Fatalf("expected %s to survive prune, remaining=%+v", want, remaining)
		}
	}
	// keep <= 0 removes nothing.
	if removed, err := Prune(bdir, 0); err != nil || len(removed) != 0 {
		t.Fatalf("prune keep=0: %v %+v", err, removed)
	}
}

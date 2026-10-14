package backup

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
	"github.com/jxburros/GWatch/internal/store/storetest"
)

// seedLinks makes a router with two nodes plugged into it, one with a style of
// its own for its link and one without.
func seedLinks(t *testing.T, ctx context.Context, st *store.Store) (styled, plain model.Node) {
	t.Helper()
	router, err := st.CreateNode(ctx, model.Node{Name: "Router", Host: "10.0.0.1", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	styled, err = st.CreateNode(ctx, model.Node{Name: "Switch", Host: "10.0.0.2", Enabled: true, DependsOnNode: &router.ID,
		LinkStyle: &model.LinkStyle{Color: "#3b82f6", Width: 3.5, Dash: model.LinkDashDashDot}})
	if err != nil {
		t.Fatal(err)
	}
	plain, err = st.CreateNode(ctx, model.Node{Name: "NAS", Host: "10.0.0.3", Enabled: true, DependsOnNode: &router.ID})
	if err != nil {
		t.Fatal(err)
	}
	return styled, plain
}

func checkLinks(t *testing.T, ctx context.Context, st *store.Store, styled, plain model.Node) {
	t.Helper()
	for _, c := range []struct {
		n    model.Node
		want *model.LinkStyle
	}{
		{styled, &model.LinkStyle{Color: "#3b82f6", Width: 3.5, Dash: model.LinkDashDashDot}},
		{plain, nil},
	} {
		got, err := st.GetNode(ctx, c.n.ID)
		if err != nil {
			t.Fatalf("node %s: %v", c.n.Name, err)
		}
		if !got.LinkStyle.Equal(c.want) {
			t.Fatalf("%s link style = %+v, want %+v", c.n.Name, got.LinkStyle, c.want)
		}
		if got.DependsOnNode == nil {
			t.Fatalf("%s lost its dependency", c.n.Name)
		}
	}
}

func TestBackupAndRestoreKeepLinkStyle(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	styled, plain := seedLinks(t, ctx, st)

	dir := filepath.Join(t.TempDir(), "backups")
	info, err := Create(ctx, st, dir, "pw", false, "test")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	st.Close()

	fresh := storetest.Open(t)
	if _, err := Restore(ctx, fresh, filepath.Join(dir, info.FileName), "pw", false); err != nil {
		t.Fatalf("restore: %v", err)
	}
	checkLinks(t, ctx, fresh, styled, plain)

	// Restoring over a database that already has the nodes, with a style
	// changed since, puts the style from the backup back.
	got, _ := fresh.GetNode(ctx, styled.ID)
	got.LinkStyle = &model.LinkStyle{Dash: model.LinkDashDotted}
	if _, _, err := fresh.UpdateNode(ctx, got); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(ctx, fresh, filepath.Join(dir, info.FileName), "pw", false); err != nil {
		t.Fatalf("restore over: %v", err)
	}
	checkLinks(t, ctx, fresh, styled, plain)
}

func TestCopyBetweenDatabasesKeepsLinkStyle(t *testing.T) {
	ctx := context.Background()
	src, err := store.Open(filepath.Join(t.TempDir(), "gwatch.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	styled, plain := seedLinks(t, ctx, src)

	dst := storetest.Open(t)
	if _, err := Copy(ctx, src, dst, nil); err != nil {
		t.Fatalf("copy: %v", err)
	}
	checkLinks(t, ctx, dst, styled, plain)
}

// A backup is a file anybody can edit, and a restore must neither fail on a
// bad link style nor let it into the database.
func TestRestoreDropsALinkStyleThatIsNotAllowed(t *testing.T) {
	ctx := context.Background()
	dst := storetest.Open(t)
	cfg := Config{Nodes: []model.Node{
		{ID: 1, Name: "Router", Enabled: true},
		{ID: 2, Name: "Cam", Enabled: true, DependsOnNode: ptr(int64(1)), LinkStyle: &model.LinkStyle{Color: "url(#x)", Width: 99, Dash: "wavy"}},
	}, Settings: model.DefaultSettings()}
	var sum Summary
	if err := restoreConfig(ctx, dst, cfg, &sum, false); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got, err := dst.GetNode(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !got.LinkStyle.Equal(&model.LinkStyle{Width: model.MaxLinkWidth}) {
		t.Fatalf("link style = %+v, want only the clamped width", got.LinkStyle)
	}
}

func ptr[T any](v T) *T { return &v }

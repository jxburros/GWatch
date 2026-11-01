package backup

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
	"github.com/jxburros/GWatch/internal/store/storetest"
)

// The network map's hand arrangement is configuration like any other: a
// backup carries it and a restore puts it back, against the same node ids.
func TestBackupAndRestoreKeepTheMapArrangement(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	styled, plain := seedLinks(t, ctx, st)
	want := model.MapLayout{
		Positions: map[string]model.MapPosition{strconv.FormatInt(styled.ID, 10): {X: 4.5, Y: -2, Pinned: true}},
		Collapsed: []int64{plain.ID},
	}
	if err := st.PutSetting(ctx, model.MapLayoutSettingKey, want); err != nil {
		t.Fatal(err)
	}

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
	var got model.MapLayout
	if err := fresh.GetSetting(ctx, model.MapLayoutSettingKey, &got); err != nil {
		t.Fatalf("the arrangement was not restored: %v", err)
	}
	key := strconv.FormatInt(styled.ID, 10)
	if got.Positions[key] != want.Positions[key] || len(got.Collapsed) != 1 || got.Collapsed[0] != plain.ID {
		t.Fatalf("restored arrangement = %+v, want %+v", got, want)
	}
}

// A backup made before there was an arrangement restores to none.
func TestRestoreWithoutAMapArrangement(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	seedLinks(t, ctx, st)
	cfg, err := ExportConfig(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MapLayout != nil {
		t.Fatalf("nothing arranged, yet the export has %+v", cfg.MapLayout)
	}
	dir := filepath.Join(t.TempDir(), "backups")
	info, err := Create(ctx, st, dir, "pw", false, "test")
	if err != nil {
		t.Fatal(err)
	}
	fresh := storetest.Open(t)
	if _, err := Restore(ctx, fresh, filepath.Join(dir, info.FileName), "pw", false); err != nil {
		t.Fatal(err)
	}
	var got model.MapLayout
	if err := fresh.GetSetting(ctx, model.MapLayoutSettingKey, &got); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("an arrangement appeared from nowhere: %+v (%v)", got, err)
	}
}

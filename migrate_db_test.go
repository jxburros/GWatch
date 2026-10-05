package main

import (
	"bytes"
	"context"
	"github.com/jxburros/GWatch/internal/dbconfig"
	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
	"github.com/jxburros/GWatch/internal/store/storetest"
	"os"
	"strings"
	"testing"
)

func TestMigrateDBRejectsMissingSourceAndSQLiteTarget(t *testing.T) {
	dir := t.TempDir()
	if err := migrateDB(context.Background(), config{dataDir: dir}); err == nil {
		t.Fatal("missing source accepted")
	}
	src, err := store.OpenDSN(context.Background(), dbconfig.Default(dir))
	if err != nil {
		t.Fatal(err)
	}
	src.Close()
	if err := migrateDB(context.Background(), config{dataDir: dir}); err == nil || !strings.Contains(err.Error(), "name the server") {
		t.Fatal(err)
	}
}
func TestMigrateDBReplaceAndSaveRules(t *testing.T) {
	if storetest.Backend() == "sqlite" {
		t.Skip("requires PostgreSQL or MySQL integration backend")
	}
	ctx := context.Background()
	dir := t.TempDir()
	source, err := store.OpenDSN(ctx, dbconfig.Default(dir))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.CreateNode(ctx, model.Node{Name: "source node", Host: "localhost"}); err != nil {
		t.Fatal(err)
	}
	source.Close()
	targetCfg := storetest.Config(t)
	target, err := store.OpenDSN(ctx, targetCfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = target.CreateNode(ctx, model.Node{Name: "existing target", Host: "localhost"}); err != nil {
		t.Fatal(err)
	}
	target.Close()
	cfg := config{dataDir: dir, db: dbconfig.Overrides{Driver: targetCfg.Driver, DSN: targetCfg.DSN, Schema: targetCfg.Schema, Database: targetCfg.Database}}
	if err = migrateDB(ctx, cfg); err == nil || !strings.Contains(err.Error(), "--replace") {
		t.Fatalf("nonempty target: %v", err)
	}
	if _, err = os.Stat(dbconfig.Path(dir)); !os.IsNotExist(err) {
		t.Fatal("failed copy saved database.json")
	}
	cfg.replace = true
	if err = migrateDB(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	saved, found, err := dbconfig.Load(dir)
	if err != nil || !found || saved.Driver != targetCfg.Driver {
		t.Fatalf("saved target: %+v %v", saved, err)
	}
	target, err = store.OpenDSN(ctx, saved)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := target.ListNodes(ctx)
	target.Close()
	if err != nil || len(nodes) != 1 || nodes[0].Name != "source node" {
		t.Fatalf("replace result: %+v %v", nodes, err)
	}
	before, err := os.ReadFile(dbconfig.Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	cfg.db = dbconfig.Overrides{}
	if err = migrateDB(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(dbconfig.Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("unmodified file configuration was unnecessarily rewritten")
	}
}

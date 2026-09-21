package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jxburros/GWatch/internal/backup"
	"github.com/jxburros/GWatch/internal/dbconfig"
	"github.com/jxburros/GWatch/internal/store"
)

// migrateDB is "gwatch migrate-db": it copies the SQLite database in the
// data directory into the PostgreSQL or MySQL database named by the --db-*
// flags, the GWATCH_DB_* variables or database.json, and then saves that
// target as database.json so the next start uses it. The service must be
// stopped first; the copy goes through the backup package's export and
// restore path in memory, so what arrives is exactly what a full backup
// restored onto the server would have been.
func migrateDB(ctx context.Context, cfg config) error {
	src := dbconfig.Default(cfg.dataDir)
	if _, err := os.Stat(src.Path); err != nil {
		return fmt.Errorf("no SQLite database to copy at %s (is --data-dir right?)", src.Path)
	}
	target, fromFile, err := dbconfig.Resolve(cfg.dataDir, cfg.db)
	if err != nil {
		return err
	}
	if !target.IsServer() {
		if fromFile {
			return errors.New("database.json names SQLite; give --db-driver postgres or mysql and the connection flags to name a server to copy into")
		}
		return errors.New("name the server to copy into with --db-driver postgres|mysql, --db-host, --db-name, --db-user and GWATCH_DB_PASSWORD (or --db-dsn), or save it first under Settings › Database")
	}

	fmt.Printf("Copying %s into %s\n", src.Path, target.Describe())
	if info, err := store.TestConnection(ctx, target); err != nil {
		return err
	} else {
		fmt.Printf("Connected: %s\n", info.ServerVersion)
	}

	from, err := store.OpenDSN(ctx, src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src.Path, err)
	}
	defer from.Close()
	to, err := store.OpenDSN(ctx, target)
	if err != nil {
		return fmt.Errorf("open %s: %w", target.Describe(), err)
	}
	defer to.Close()

	empty, err := to.IsEmpty(ctx)
	if err != nil {
		return err
	}
	if !empty {
		if !cfg.replace {
			return fmt.Errorf("%s already holds GWatch data; run again with --replace to empty it first, or point at an empty database", target.Describe())
		}
		fmt.Println("Emptying the target database (--replace)…")
		if err := to.ClearEverything(ctx); err != nil {
			return fmt.Errorf("empty target: %w", err)
		}
	}

	last := ""
	sum, err := backup.Copy(ctx, from, to, func(stage string, n int) {
		if stage != last {
			if last != "" {
				fmt.Println()
			}
			last = stage
		}
		fmt.Printf("\r  %-18s %d", stage, n)
	})
	if last != "" {
		fmt.Println()
	}
	if err != nil {
		return fmt.Errorf("copy failed, nothing was changed in %s: %w", src.Path, err)
	}

	fmt.Printf("Done: %d nodes, %d checks, %d results, %d rollups, %d events, %d hardware readings, %d wallboards, %d users, %d API keys, %d agents.\n",
		sum.Nodes, sum.Checks, sum.Results, sum.Rollups, sum.Events, sum.HostSamples, sum.Wallboards, sum.Users, sum.APIKeys, sum.Agents)

	if !fromFile || !cfg.db.Empty() || !dbconfig.FromEnv().Empty() {
		if err := dbconfig.Save(cfg.dataDir, target); err != nil {
			return fmt.Errorf("the copy succeeded but the database settings could not be saved: %w", err)
		}
		fmt.Printf("Saved the connection to %s.\n", dbconfig.Path(cfg.dataDir))
	}
	fmt.Println(strings.TrimSpace(`
GWatch will use the new database the next time it starts (gwatch restart, or start the
service again). The SQLite file was left in place as a fallback: remove database.json to
go back to it.`))
	return nil
}

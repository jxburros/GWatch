package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/kardianos/service"

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

	// The docs have always said to stop the service first; now it is checked.
	// A copy taken while the service writes is not a copy of anything.
	if where, running := gwatchRunning(ctx, cfg); running {
		return fmt.Errorf("GWatch is running (%s); stop it first (gwatch stop, or stop the service), then run migrate-db again", where)
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
		return fmt.Errorf("copy failed, nothing was changed in %s: %w\n%s may now be partly filled; once the problem is fixed, run gwatch migrate-db --replace to empty it and copy again", src.Path, err, target.Describe())
	}

	fmt.Printf("Done: %d nodes, %d checks, %d results, %d rollups, %d events, %d wallboards, %d users, %d API keys, %d agents.\n",
		sum.Nodes, sum.Checks, sum.Results, sum.Rollups, sum.Events, sum.Wallboards, sum.Users, sum.APIKeys, sum.Agents)

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

// gwatchRunning says whether a GWatch is running against this data: the
// installed service reports itself running, or something answering as GWatch
// is listening on the address this one would use.
func gwatchRunning(ctx context.Context, cfg config) (string, bool) {
	if svc, err := service.New(&program{cfg: cfg}, &service.Config{Name: serviceName}); err == nil {
		if st, err := svc.Status(); err == nil && st == service.StatusRunning {
			return "the " + serviceName + " service is running", true
		}
	}
	host, port, err := net.SplitHostPort(cfg.listen)
	if err != nil {
		return "", false
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	addr := net.JoinHostPort(host, port)
	client := &http.Client{
		Timeout: 2 * time.Second,
		// Only asking whether something is there, and sending nothing: a
		// self-signed certificate is what a GWatch on HTTPS has.
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec
	}
	for _, scheme := range []string{"http", "https"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, scheme+"://"+addr+"/api/health", nil)
		if err != nil {
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		var h struct {
			SchedulerRunning *bool `json:"schedulerRunning"`
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&h)
		resp.Body.Close()
		if err == nil && h.SchedulerRunning != nil {
			return "it answers on " + addr, true
		}
	}
	return "", false
}

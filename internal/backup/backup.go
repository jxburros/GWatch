// Package backup creates and restores password-encrypted backup archives
// containing the configuration and, optionally, the performance history.
package backup

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
)

// Extension of backup archives.
const Extension = ".gwbackup"

// Manifest describes an archive.
type Manifest struct {
	Format         int       `json:"format"`
	AppVersion     string    `json:"appVersion"`
	CreatedAt      time.Time `json:"createdAt"`
	IncludeHistory bool      `json:"includeHistory"`
	Nodes          int       `json:"nodes"`
	Checks         int       `json:"checks"`
	Results        int       `json:"results"`
	Rollups        int       `json:"rollups"`
	Events         int       `json:"events"`
}

// Config is the configuration part of an archive.
type Config struct {
	Settings    model.Settings            `json:"settings"`
	Nodes       []model.Node              `json:"nodes"`
	Dashboards  []model.Dashboard         `json:"dashboards"`
	Maintenance []model.MaintenanceWindow `json:"maintenance"`
}

// Summary reports what a restore imported.
type Summary struct {
	Nodes    int      `json:"nodes"`
	Checks   int      `json:"checks"`
	Results  int      `json:"results"`
	Rollups  int      `json:"rollups"`
	Events   int      `json:"events"`
	History  bool     `json:"history"`
	Manifest Manifest `json:"manifest"`
}

var safeName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// ValidFileName reports whether name is a plain backup file name.
func ValidFileName(name string) bool {
	return safeName.MatchString(name) && strings.HasSuffix(name, Extension) && !strings.Contains(name, "..")
}

// ExportConfig collects the configuration from the store.
func ExportConfig(ctx context.Context, st *store.Store) (Config, error) {
	var cfg Config
	var err error
	if cfg.Settings, err = st.LoadSettings(ctx); err != nil {
		return cfg, err
	}
	if cfg.Nodes, err = st.ListNodes(ctx); err != nil {
		return cfg, err
	}
	if cfg.Dashboards, err = st.ListDashboards(ctx); err != nil {
		return cfg, err
	}
	if cfg.Maintenance, err = st.ListMaintenance(ctx); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// Create writes an encrypted archive into dir and returns its metadata.
func Create(ctx context.Context, st *store.Store, dir, password string, includeHistory bool, appVersion string) (model.BackupInfo, error) {
	if strings.TrimSpace(password) == "" {
		return model.BackupInfo{}, errors.New("a password is required to encrypt the backup")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return model.BackupInfo{}, err
	}
	now := time.Now()
	name := fmt.Sprintf("gwatch-backup-%s%s", now.Format("20060102-150405"), Extension)
	if includeHistory {
		name = fmt.Sprintf("gwatch-backup-%s-full%s", now.Format("20060102-150405"), Extension)
	}
	path := filepath.Join(dir, name)
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return model.BackupInfo{}, err
	}
	cleanup := func() { f.Close(); os.Remove(tmp) }

	if err := writeArchive(ctx, st, f, password, includeHistory, appVersion, now); err != nil {
		cleanup()
		return model.BackupInfo{}, err
	}
	if err := f.Sync(); err != nil {
		cleanup()
		return model.BackupInfo{}, err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return model.BackupInfo{}, err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return model.BackupInfo{}, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return model.BackupInfo{}, err
	}
	return model.BackupInfo{FileName: name, CreatedAt: now, SizeBytes: fi.Size(), IncludeHistory: includeHistory, Encrypted: true}, nil
}

func writeArchive(ctx context.Context, st *store.Store, w io.Writer, password string, includeHistory bool, appVersion string, now time.Time) error {
	bw := bufio.NewWriterSize(w, 1<<16)
	enc, err := NewEncryptWriter(bw, password)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(enc)
	cfg, err := ExportConfig(ctx, st)
	if err != nil {
		return err
	}
	manifest := Manifest{Format: 1, AppVersion: appVersion, CreatedAt: now, IncludeHistory: includeHistory, Nodes: len(cfg.Nodes)}
	for _, n := range cfg.Nodes {
		manifest.Checks += len(n.Checks)
	}
	if err := writeJSON(zw, "config.json", cfg); err != nil {
		return err
	}
	if includeHistory {
		rw, err := zw.Create("results.jsonl")
		if err != nil {
			return err
		}
		enc := json.NewEncoder(rw)
		if err := st.AllResults(ctx, func(r model.Result) error { manifest.Results++; return enc.Encode(r) }); err != nil {
			return err
		}
		rw, err = zw.Create("rollups.jsonl")
		if err != nil {
			return err
		}
		enc = json.NewEncoder(rw)
		if err := st.AllRollups(ctx, func(r model.Rollup) error { manifest.Rollups++; return enc.Encode(r) }); err != nil {
			return err
		}
		rw, err = zw.Create("events.jsonl")
		if err != nil {
			return err
		}
		enc = json.NewEncoder(rw)
		if err := st.AllEvents(ctx, func(e model.Event) error { manifest.Events++; return enc.Encode(e) }); err != nil {
			return err
		}
	}
	if err := writeJSON(zw, "manifest.json", manifest); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return bw.Flush()
}

func writeJSON(zw *zip.Writer, name string, v any) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// List returns the archives in dir, newest first.
func List(dir string) ([]model.BackupInfo, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []model.BackupInfo{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []model.BackupInfo{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), Extension) {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, model.BackupInfo{FileName: e.Name(), CreatedAt: fi.ModTime(), SizeBytes: fi.Size(), IncludeHistory: strings.Contains(e.Name(), "-full"), Encrypted: true})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// Open decrypts an archive to a temporary file and returns a zip reader.
func Open(path, password string) (*zip.ReadCloser, func(), error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	dec, err := NewDecryptReader(bufio.NewReaderSize(f, 1<<16), password)
	if err != nil {
		return nil, nil, err
	}
	tmp, err := os.CreateTemp("", "gwatch-restore-*.zip")
	if err != nil {
		return nil, nil, err
	}
	if _, err := io.Copy(tmp, dec); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return nil, nil, err
	}
	tmp.Close()
	zr, err := zip.OpenReader(tmp.Name())
	if err != nil {
		os.Remove(tmp.Name())
		return nil, nil, fmt.Errorf("backup archive is not readable: %w", err)
	}
	cleanup := func() { zr.Close(); os.Remove(tmp.Name()) }
	return zr, cleanup, nil
}

// Inspect returns the manifest of an archive.
func Inspect(path, password string) (Manifest, error) {
	zr, cleanup, err := Open(path, password)
	if err != nil {
		return Manifest{}, err
	}
	defer cleanup()
	var m Manifest
	if err := readJSON(&zr.Reader, "manifest.json", &m); err != nil {
		return m, err
	}
	return m, nil
}

func readJSON(zr *zip.Reader, name string, v any) error {
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer rc.Close()
			return json.NewDecoder(rc).Decode(v)
		}
	}
	return fmt.Errorf("backup is missing %s", name)
}

func hasFile(zr *zip.Reader, name string) bool {
	for _, f := range zr.File {
		if f.Name == name {
			return true
		}
	}
	return false
}

// Restore replaces the configuration (and optionally the history) with the
// contents of the archive. Node and check ids are preserved so history rows
// keep pointing at the right checks.
func Restore(ctx context.Context, st *store.Store, path, password string, includeHistory bool) (Summary, error) {
	zr, cleanup, err := Open(path, password)
	if err != nil {
		return Summary{}, err
	}
	defer cleanup()
	var sum Summary
	if err := readJSON(&zr.Reader, "manifest.json", &sum.Manifest); err != nil {
		return sum, err
	}
	var cfg Config
	if err := readJSON(&zr.Reader, "config.json", &cfg); err != nil {
		return sum, err
	}
	includeHistory = includeHistory && sum.Manifest.IncludeHistory && hasFile(&zr.Reader, "results.jsonl")
	sum.History = includeHistory

	if err := st.ClearAll(ctx, true); err != nil {
		return sum, err
	}
	if err := st.SaveSettings(ctx, cfg.Settings); err != nil {
		return sum, err
	}
	deps := map[int64]int64{}
	for _, n := range cfg.Nodes {
		if n.DependsOnNode != nil {
			deps[n.ID] = *n.DependsOnNode
		}
		if err := st.CreateNodeWithID(ctx, n); err != nil {
			return sum, fmt.Errorf("restore node %q: %w", n.Name, err)
		}
		sum.Nodes++
		sum.Checks += len(n.Checks)
	}
	if err := st.SetDependencies(ctx, deps); err != nil {
		return sum, err
	}
	for _, d := range cfg.Dashboards {
		d.ID = 0
		if _, err := st.SaveDashboard(ctx, d); err != nil {
			return sum, err
		}
	}
	for _, m := range cfg.Maintenance {
		m.ID = 0
		if _, err := st.SaveMaintenance(ctx, m); err != nil {
			return sum, err
		}
	}
	if includeHistory {
		if err := importLines(&zr.Reader, "results.jsonl", 2000, func(lines [][]byte) error {
			batch := make([]model.Result, 0, len(lines))
			for _, l := range lines {
				var r model.Result
				if err := json.Unmarshal(l, &r); err == nil {
					batch = append(batch, r)
				}
			}
			sum.Results += len(batch)
			return st.InsertResultsBatch(ctx, batch)
		}); err != nil {
			return sum, err
		}
		if err := importLines(&zr.Reader, "rollups.jsonl", 2000, func(lines [][]byte) error {
			batch := make([]model.Rollup, 0, len(lines))
			for _, l := range lines {
				var r model.Rollup
				if err := json.Unmarshal(l, &r); err == nil {
					batch = append(batch, r)
				}
			}
			sum.Rollups += len(batch)
			return st.InsertRollupsBatch(ctx, batch)
		}); err != nil {
			return sum, err
		}
		if err := importLines(&zr.Reader, "events.jsonl", 2000, func(lines [][]byte) error {
			batch := make([]model.Event, 0, len(lines))
			for _, l := range lines {
				var e model.Event
				if err := json.Unmarshal(l, &e); err == nil {
					batch = append(batch, e)
				}
			}
			sum.Events += len(batch)
			return st.InsertEventsBatch(ctx, batch)
		}); err != nil {
			return sum, err
		}
	}
	return sum, nil
}

func importLines(zr *zip.Reader, name string, batchSize int, fn func([][]byte) error) error {
	if !hasFile(zr, name) {
		return nil
	}
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		sc := bufio.NewScanner(rc)
		sc.Buffer(make([]byte, 1<<20), 16<<20)
		var batch [][]byte
		flush := func() error {
			if len(batch) == 0 {
				return nil
			}
			err := fn(batch)
			batch = nil
			return err
		}
		for sc.Scan() {
			line := append([]byte{}, sc.Bytes()...)
			if len(strings.TrimSpace(string(line))) == 0 {
				continue
			}
			batch = append(batch, line)
			if len(batch) >= batchSize {
				if err := flush(); err != nil {
					return err
				}
			}
		}
		if err := sc.Err(); err != nil {
			return err
		}
		return flush()
	}
	return nil
}

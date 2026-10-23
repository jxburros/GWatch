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

// Format is the archive format this build writes. Format 1 carried the
// configuration and history only; format 2 (#34) added wallboards, user
// accounts, API keys and hardware agents to config.json, and hardware
// readings to the history. Restore reads both.
const Format = 2

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
	Wallboards     int       `json:"wallboards,omitempty"`
	Users          int       `json:"users,omitempty"`
	APIKeys        int       `json:"apiKeys,omitempty"`
	Agents         int       `json:"agents,omitempty"`
}

// Config is the configuration part of an archive.
//
// The account, key and agent records carry the digests the database keeps
// (a password hash, a token hash), never a credential in the clear; the
// archive as a whole is encrypted with the backup password. A format-1
// archive has none of the last four fields, and Restore leaves what they
// describe untouched when they are absent.
type Config struct {
	Incidents   []model.Incident          `json:"incidents,omitempty"`
	Reports     []model.ReportDefinition  `json:"reports,omitempty"`
	Settings    model.Settings            `json:"settings"`
	Nodes       []model.Node              `json:"nodes"`
	Dashboards  []model.Dashboard         `json:"dashboards"`
	Maintenance []model.MaintenanceWindow `json:"maintenance"`
	Triggers    []model.Trigger           `json:"triggers,omitempty"`
	Endpoints   []model.Endpoint          `json:"endpoints,omitempty"`
	Rules       []model.Rule              `json:"rules,omitempty"` // notification rules (#31); absent from archives written before them
	Charts      []model.SavedChart        `json:"charts,omitempty"`
	Wallboards  []model.Wallboard         `json:"wallboards,omitempty"`
	Users       []store.UserRecord        `json:"users,omitempty"`
	APIKeys     []store.APIKeyRecord      `json:"apiKeys,omitempty"`
	Agents      []store.AgentRecord       `json:"agents,omitempty"`
}

// Summary reports what a restore (or a migrate-db copy) imported.
type Summary struct {
	Nodes      int      `json:"nodes"`
	Checks     int      `json:"checks"`
	Results    int      `json:"results"`
	Rollups    int      `json:"rollups"`
	Events     int      `json:"events"`
	Wallboards int      `json:"wallboards"`
	Users      int      `json:"users"`
	APIKeys    int      `json:"apiKeys"`
	Agents     int      `json:"agents"`
	History    bool     `json:"history"`
	Manifest   Manifest `json:"manifest"`
	// SkippedUsers and SkippedAgents are the accounts and machines in the
	// archive that are not on this server and were not re-created (see
	// RestoreOptions.RecreateAccounts).
	SkippedUsers  []string `json:"skippedUsers,omitempty"`
	SkippedAgents []string `json:"skippedAgents,omitempty"`
}

// RestoreOptions says what a restore brings back beyond the configuration.
type RestoreOptions struct {
	// IncludeHistory restores results, rollups and events when the archive
	// has them.
	IncludeHistory bool
	// RecreateAccounts re-creates the user accounts and agents in the
	// archive that are not on this server. Without it they are left out and
	// listed in the summary: users can only be deleted, not revoked, so an
	// older backup would otherwise bring a deleted administrator back with
	// their old password, and a purged agent's token would work again.
	RecreateAccounts bool
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
	if cfg.Incidents, err = st.ExportIncidents(ctx); err != nil {
		return cfg, err
	}
	if err = st.GetSetting(ctx, "reports", &cfg.Reports); err != nil && !errors.Is(err, store.ErrNotFound) {
		return cfg, err
	}
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
	if cfg.Triggers, err = st.ListTriggers(ctx, nil); err != nil {
		return cfg, err
	}
	if cfg.Endpoints, err = st.ListEndpoints(ctx); err != nil {
		return cfg, err
	}
	if cfg.Rules, err = st.ListRules(ctx); err != nil {
		return cfg, err
	}
	if cfg.Charts, err = st.ListSavedCharts(ctx); err != nil {
		return cfg, err
	}
	if cfg.Wallboards, err = st.ListWallboards(ctx); err != nil {
		return cfg, err
	}
	if cfg.Users, err = st.ExportUsers(ctx); err != nil {
		return cfg, err
	}
	if cfg.APIKeys, err = st.ExportAPIKeys(ctx); err != nil {
		return cfg, err
	}
	if cfg.Agents, err = st.ExportAgents(ctx); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// Create writes an encrypted archive into dir and returns its metadata.
func Create(ctx context.Context, st *store.Store, dir, password string, includeHistory bool, appVersion string) (model.BackupInfo, error) {
	return create(ctx, st, dir, password, includeHistory, appVersion, "gwatch-backup-")
}

// CreateScheduled marks archives eligible for automatic retention. Older archives remain manual.
func CreateScheduled(ctx context.Context, st *store.Store, dir, password string, includeHistory bool, appVersion string) (model.BackupInfo, error) {
	return create(ctx, st, dir, password, includeHistory, appVersion, "gwatch-auto-")
}

func create(ctx context.Context, st *store.Store, dir, password string, includeHistory bool, appVersion, prefix string) (model.BackupInfo, error) {
	if strings.TrimSpace(password) == "" {
		return model.BackupInfo{}, errors.New("a password is required to encrypt the backup")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return model.BackupInfo{}, err
	}
	now := time.Now()
	name := fmt.Sprintf("%s%s%s", prefix, now.Format("20060102-150405.000000000"), Extension)
	if includeHistory {
		name = fmt.Sprintf("%s%s-full%s", prefix, now.Format("20060102-150405.000000000"), Extension)
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
	manifest := Manifest{Format: Format, AppVersion: appVersion, CreatedAt: now, IncludeHistory: includeHistory, Nodes: len(cfg.Nodes),
		Wallboards: len(cfg.Wallboards), Users: len(cfg.Users), APIKeys: len(cfg.APIKeys), Agents: len(cfg.Agents)}
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

// Prune keeps the keep newest archives in dir (by creation time, newest
// first, same order as List) and removes the rest. It returns the file names
// that were removed. keep <= 0 removes nothing.
func Prune(dir string, keep int) ([]string, error) {
	return prune(dir, keep, false)
}

// PruneScheduled only removes archives explicitly created by the scheduler.
func PruneScheduled(dir string, keep int) ([]string, error) {
	return prune(dir, keep, true)
}

func prune(dir string, keep int, scheduledOnly bool) ([]string, error) {
	if keep <= 0 {
		return nil, nil
	}
	list, err := List(dir)
	if err != nil {
		return nil, err
	}
	if scheduledOnly {
		auto := list[:0]
		for _, item := range list {
			if strings.HasPrefix(item.FileName, "gwatch-auto-") {
				auto = append(auto, item)
			}
		}
		list = auto
	}
	if len(list) <= keep {
		return nil, nil
	}
	var removed []string
	for _, b := range list[keep:] {
		if err := os.Remove(filepath.Join(dir, b.FileName)); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return removed, err
		}
		removed = append(removed, b.FileName)
	}
	return removed, nil
}

// Open decrypts an archive to a temporary file and returns a zip reader. An
// archive larger than MaxArchiveBytes, or whose ZIP directory is out of
// bounds (see checkEntries), is refused with ErrArchiveTooLarge.
func Open(path, password string) (*zip.ReadCloser, func(), error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil {
		return nil, nil, err
	} else if fi.Size() > maxArchiveBytes {
		return nil, nil, ErrArchiveTooLarge
	}
	dec, err := NewDecryptReader(bufio.NewReaderSize(f, 1<<16), password)
	if err != nil {
		return nil, nil, err
	}
	tmp, err := os.CreateTemp("", "gwatch-restore-*.zip")
	if err != nil {
		return nil, nil, err
	}
	// The plaintext is never longer than the ciphertext; the cap is a
	// backstop should the file grow while it is read.
	if _, err := io.Copy(tmp, &capReader{r: dec, n: maxArchiveBytes}); err != nil {
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
	if err := checkEntries(&zr.Reader); err != nil {
		cleanup()
		return nil, nil, err
	}
	return zr, cleanup, nil
}

// Inspect returns the manifest of an archive.
func Inspect(path, password string) (Manifest, error) {
	zr, cleanup, err := Open(path, password)
	if err != nil {
		return Manifest{}, err
	}
	defer cleanup()
	return readManifest(&zr.Reader)
}

// readManifest reads and checks manifest.json.
func readManifest(zr *zip.Reader) (Manifest, error) {
	var m Manifest
	if err := readJSON(zr, "manifest.json", maxManifestBytes, &m); err != nil {
		return m, err
	}
	if m.Format < 1 || m.Format > Format {
		return m, fmt.Errorf("unsupported backup format %d (supported: 1–%d)", m.Format, Format)
	}
	if err := checkManifest(m); err != nil {
		return m, err
	}
	return m, nil
}

// readJSON decodes entry name, reading at most max decompressed bytes.
func readJSON(zr *zip.Reader, name string, max int64, v any) error {
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer rc.Close()
			if err := json.NewDecoder(&capReader{r: rc, n: max}).Decode(v); err != nil {
				if errors.Is(err, ErrArchiveTooLarge) {
					return fmt.Errorf("%w: %s", ErrArchiveTooLarge, name)
				}
				return err
			}
			return nil
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
// contents of the archive, re-creating every account in it. Node and check ids
// are preserved so history rows keep pointing at the right checks.
func Restore(ctx context.Context, st *store.Store, path, password string, includeHistory bool) (Summary, error) {
	return RestoreWith(ctx, st, path, password, RestoreOptions{IncludeHistory: includeHistory, RecreateAccounts: true})
}

// RestoreWith is Restore with the choices spelled out.
func RestoreWith(ctx context.Context, st *store.Store, path, password string, opts RestoreOptions) (Summary, error) {
	includeHistory := opts.IncludeHistory
	zr, cleanup, err := Open(path, password)
	if err != nil {
		return Summary{}, err
	}
	defer cleanup()
	var sum Summary
	if sum.Manifest, err = readManifest(&zr.Reader); err != nil {
		return sum, err
	}
	var cfg Config
	if err := readJSON(&zr.Reader, "config.json", maxConfigBytes, &cfg); err != nil {
		return sum, err
	}
	includeHistory = includeHistory && sum.Manifest.IncludeHistory && hasFile(&zr.Reader, "results.jsonl")
	sum.History = includeHistory
	// Everything that can make the import fail for size is checked before
	// the first write, so an oversized archive never leaves a half-restored
	// installation behind.
	if includeHistory {
		if err := preflightHistory(&zr.Reader, sum.Manifest); err != nil {
			return sum, err
		}
	}

	if err := restoreConfig(ctx, st, cfg, &sum, includeHistory, opts.RecreateAccounts); err != nil {
		return sum, err
	}
	if includeHistory {
		budget := maxHistoryBytes
		if err := importLines(&zr.Reader, "results.jsonl", sum.Manifest, &budget, func(lines [][]byte) error {
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
		if err := importLines(&zr.Reader, "rollups.jsonl", sum.Manifest, &budget, func(lines [][]byte) error {
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
		if err := importLines(&zr.Reader, "events.jsonl", sum.Manifest, &budget, func(lines [][]byte) error {
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
		// Archives from before hardware readings became results carry a
		// hostsamples.jsonl. It is not read: every reading in it was also the
		// result of a hardware check, which results.jsonl has.
	}
	return sum, nil
}

// batchSize is how many history rows go into one transaction on import.
const batchSize = 2000

// restoreConfig writes the configuration part of an archive into a store
// by updating matching nodes/checks in place and removing absent IDs. Node and check
// ids are kept; everything else gets fresh ids except wallboards, whose ids
// are in projected addresses, and accounts, keys and agents, which keep
// theirs when free.
//
// Accounts, keys and agents are merged rather than replaced: one already
// present (same user name, same digest) is left alone. A restore must never
// overwrite the password of the administrator running it, and a key or
// agent that was deliberately revoked here must not come back to life.
// Wallboards are present in the archive as a whole or not at all, so they
// are replaced when the archive has them and untouched otherwise (a
// format-1 archive).
func restoreConfig(ctx context.Context, st *store.Store, cfg Config, sum *Summary, includeHistory, recreateAccounts bool) error {
	if err := st.RestoreNodes(ctx, cfg.Nodes, includeHistory); err != nil {
		return err
	}
	if err := st.SaveSettings(ctx, cfg.Settings); err != nil {
		return err
	}
	deps := map[int64]int64{}
	for _, n := range cfg.Nodes {
		if n.DependsOnNode != nil {
			deps[n.ID] = *n.DependsOnNode
		}
		sum.Nodes++
		sum.Checks += len(n.Checks)
	}
	if includeHistory {
		if err := st.RestoreIncidents(ctx, cfg.Incidents); err != nil {
			return err
		}
	}
	if err := st.PutSetting(ctx, "reports", cfg.Reports); err != nil {
		return err
	}
	if err := st.SetDependencies(ctx, deps); err != nil {
		return err
	}
	for _, d := range cfg.Dashboards {
		d.ID = 0
		if _, err := st.SaveDashboard(ctx, d); err != nil {
			return err
		}
	}
	for _, m := range cfg.Maintenance {
		m.ID = 0
		if _, err := st.SaveMaintenance(ctx, m); err != nil {
			return err
		}
	}
	for _, t := range cfg.Triggers {
		t.ID = 0
		if _, err := st.SaveTrigger(ctx, t); err != nil {
			return err
		}
	}
	for _, e := range cfg.Endpoints {
		e.ID = 0
		if _, err := st.SaveEndpoint(ctx, e); err != nil {
			return err
		}
	}
	// Rules name nodes and checks by id, and those keep their ids above.
	// A rule starts over as not-met: its state is about the moment, not
	// the configuration.
	for _, r := range cfg.Rules {
		r.ID = 0
		if _, err := st.SaveRule(ctx, r); err != nil {
			return err
		}
	}
	if len(cfg.Charts) > 0 {
		if err := st.SaveSavedCharts(ctx, cfg.Charts); err != nil {
			return err
		}
	}
	if cfg.Wallboards != nil {
		if err := st.ReplaceWallboards(ctx, cfg.Wallboards); err != nil {
			return err
		}
		sum.Wallboards = len(cfg.Wallboards)
	}
	// Agents before users and keys only by convention; none depends on
	// another. An agent's node_id points at a node restored above.
	for _, a := range cfg.Agents {
		if !recreateAccounts {
			known, err := st.AgentTokenKnown(ctx, a.TokenHash)
			if err != nil {
				return fmt.Errorf("restore agent %q: %w", a.Name, err)
			}
			if !known {
				sum.SkippedAgents = append(sum.SkippedAgents, a.Name)
				continue
			}
		}
		added, err := st.ImportAgent(ctx, a)
		if err != nil {
			return fmt.Errorf("restore agent %q: %w", a.Name, err)
		}
		if added {
			sum.Agents++
		}
	}
	for _, u := range cfg.Users {
		if !recreateAccounts {
			if _, _, err := st.GetUserByName(ctx, u.Username); errors.Is(err, store.ErrNotFound) {
				sum.SkippedUsers = append(sum.SkippedUsers, u.Username)
				continue
			} else if err != nil {
				return fmt.Errorf("restore user %q: %w", u.Username, err)
			}
		}
		added, err := st.ImportUser(ctx, u)
		if err != nil {
			return fmt.Errorf("restore user %q: %w", u.Username, err)
		}
		if added {
			sum.Users++
		}
	}
	for _, k := range cfg.APIKeys {
		added, err := st.ImportAPIKey(ctx, k)
		if err != nil {
			return fmt.Errorf("restore API key %q: %w", k.Name, err)
		}
		if added {
			sum.APIKeys++
		}
	}
	return nil
}

// Copy moves everything from src into dst without going through an archive
// on disk: the configuration first, then the history streamed in batches.
// It is what "gwatch migrate-db" runs to take an installation from the
// SQLite file to a server database. dst is expected to be empty; the caller
// checks (store.IsEmpty) and empties it (store.ClearEverything) if asked
// to. progress, if not nil, is told each stage's name and running count.
func Copy(ctx context.Context, src, dst *store.Store, progress func(stage string, n int)) (Summary, error) {
	report := func(stage string, n int) {
		if progress != nil {
			progress(stage, n)
		}
	}
	var sum Summary
	cfg, err := ExportConfig(ctx, src)
	if err != nil {
		return sum, fmt.Errorf("read configuration: %w", err)
	}
	if err := restoreConfig(ctx, dst, cfg, &sum, true, true); err != nil {
		return sum, fmt.Errorf("write configuration: %w", err)
	}
	report("configuration", sum.Nodes)

	results := make([]model.Result, 0, batchSize)
	flushResults := func() error {
		if len(results) == 0 {
			return nil
		}
		if err := dst.InsertResultsBatch(ctx, results); err != nil {
			return err
		}
		sum.Results += len(results)
		results = results[:0]
		report("results", sum.Results)
		return nil
	}
	if err := src.AllResults(ctx, func(r model.Result) error {
		results = append(results, r)
		if len(results) >= batchSize {
			return flushResults()
		}
		return nil
	}); err != nil {
		return sum, fmt.Errorf("copy results: %w", err)
	}
	if err := flushResults(); err != nil {
		return sum, fmt.Errorf("copy results: %w", err)
	}

	rollups := make([]model.Rollup, 0, batchSize)
	flushRollups := func() error {
		if len(rollups) == 0 {
			return nil
		}
		if err := dst.InsertRollupsBatch(ctx, rollups); err != nil {
			return err
		}
		sum.Rollups += len(rollups)
		rollups = rollups[:0]
		report("rollups", sum.Rollups)
		return nil
	}
	if err := src.AllRollups(ctx, func(r model.Rollup) error {
		rollups = append(rollups, r)
		if len(rollups) >= batchSize {
			return flushRollups()
		}
		return nil
	}); err != nil {
		return sum, fmt.Errorf("copy rollups: %w", err)
	}
	if err := flushRollups(); err != nil {
		return sum, fmt.Errorf("copy rollups: %w", err)
	}

	events := make([]model.Event, 0, batchSize)
	flushEvents := func() error {
		if len(events) == 0 {
			return nil
		}
		if err := dst.InsertEventsBatch(ctx, events); err != nil {
			return err
		}
		sum.Events += len(events)
		events = events[:0]
		report("events", sum.Events)
		return nil
	}
	if err := src.AllEvents(ctx, func(e model.Event) error {
		events = append(events, e)
		if len(events) >= batchSize {
			return flushEvents()
		}
		return nil
	}); err != nil {
		return sum, fmt.Errorf("copy events: %w", err)
	}
	if err := flushEvents(); err != nil {
		return sum, fmt.Errorf("copy events: %w", err)
	}

	sum.History = true
	return sum, nil
}

// importLines feeds the lines of entry name to fn in batches of batchSize,
// within the shared decompressed budget and the record count m claims.
func importLines(zr *zip.Reader, name string, m Manifest, budget *int64, fn func([][]byte) error) error {
	var batch [][]byte
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := fn(batch)
		batch = nil
		return err
	}
	if err := scanLines(zr, name, budget, claimed(m, name), func(line []byte) error {
		batch = append(batch, append([]byte{}, line...))
		if len(batch) >= batchSize {
			return flush()
		}
		return nil
	}); err != nil {
		return err
	}
	return flush()
}

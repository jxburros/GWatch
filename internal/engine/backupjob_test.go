package engine

import (
	"context"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/backup"
	"github.com/jxburros/GWatch/internal/logging"
	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
	"github.com/jxburros/GWatch/internal/store/storetest"
)

func newTestEngine(t *testing.T, dataDir string) (*Engine, *store.Store) {
	t.Helper()
	st := storetest.Open(t)
	log, _ := logging.New("", nil)
	e := New(st, log, Options{Version: "test", DataDir: dataDir})
	e.ctx = context.Background()
	return e, st
}

// TestScheduledBackupSkippedWithoutDataDir ensures the job does nothing when
// no data directory is configured (as in most tests / some run modes), and
// nothing when scheduled backups are disabled.
func TestScheduledBackupSkippedWhenNotConfigured(t *testing.T) {
	e, st := newTestEngine(t, "")
	settings := model.DefaultSettings()
	settings.Backups.Enabled = true
	settings.Backups.Password = "pw"
	if err := st.SaveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	if err := e.ReloadConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.maybeRunScheduledBackup(time.Now())
	if e.BackupStatus().LastBackupAt != nil {
		t.Fatalf("expected no backup without a data dir, got %+v", e.BackupStatus())
	}

	// Disabled: even with a data dir, nothing should run.
	dataDir := t.TempDir()
	e2, st2 := newTestEngine(t, dataDir)
	settings2 := model.DefaultSettings()
	settings2.Backups.Enabled = false
	settings2.Backups.Password = "pw"
	if err := st2.SaveSettings(context.Background(), settings2); err != nil {
		t.Fatal(err)
	}
	if err := e2.ReloadConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
	e2.maybeRunScheduledBackup(time.Now())
	if e2.BackupStatus().LastBackupAt != nil {
		t.Fatalf("expected no backup when disabled, got %+v", e2.BackupStatus())
	}
}

// TestScheduledBackupRunsWhenDue drives maybeRunScheduledBackup directly
// (rather than waiting for the real hourly/minute ticker) to verify it
// creates an archive, records status/events and prunes old archives.
func TestScheduledBackupRunsWhenDue(t *testing.T) {
	dataDir := t.TempDir()
	e, st := newTestEngine(t, dataDir)
	ctx := context.Background()
	if _, err := st.CreateNode(ctx, model.Node{Name: "Gateway", Host: "192.168.1.1", Enabled: true, Checks: []model.Check{{Type: model.CheckPing, Name: "Ping", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5}}}); err != nil {
		t.Fatal(err)
	}
	settings := model.DefaultSettings()
	settings.Backups.Enabled = true
	settings.Backups.Password = "s3cret"
	settings.Backups.IntervalHours = 1
	settings.Backups.Keep = 2
	settings.Backups.IncludeHistory = false
	if err := st.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if err := e.ReloadConfig(ctx); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	e.maybeRunScheduledBackup(now)
	status := e.BackupStatus()
	if status.LastBackupAt == nil || !status.LastBackupOK {
		t.Fatalf("expected a successful scheduled backup, got %+v", status)
	}
	list, err := backup.List(e.backupDir())
	if err != nil || len(list) != 1 {
		t.Fatalf("expected 1 archive after first run: %v %+v", err, list)
	}

	// Not due yet: running again immediately should not create a second
	// archive because the interval has not elapsed.
	e.maybeRunScheduledBackup(now.Add(5 * time.Minute))
	list, err = backup.List(e.backupDir())
	if err != nil || len(list) != 1 {
		t.Fatalf("expected still 1 archive before interval elapses: %v %+v", err, list)
	}

	// Due again after the configured interval: a second archive is created
	// and, since keep=2, both remain. Archive file names have 1-second
	// resolution, so sleep briefly to guarantee a distinct file name.
	time.Sleep(1100 * time.Millisecond)
	e.maybeRunScheduledBackup(now.Add(2 * time.Hour))
	list, err = backup.List(e.backupDir())
	if err != nil || len(list) != 2 {
		t.Fatalf("expected 2 archives after second run: %v %+v", err, list)
	}

	// A third run, further out, must prune down to keep=2.
	time.Sleep(1100 * time.Millisecond)
	e.maybeRunScheduledBackup(now.Add(4 * time.Hour))
	list, err = backup.List(e.backupDir())
	if err != nil || len(list) != 2 {
		t.Fatalf("expected pruning to keep 2 archives: %v %+v", err, list)
	}

	next := e.NextBackupAt()
	if next == nil {
		t.Fatal("expected NextBackupAt to report a time once enabled and a backup has run")
	}
}

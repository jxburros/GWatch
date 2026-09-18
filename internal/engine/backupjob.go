package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/jxburros/GWatch/internal/backup"
	"github.com/jxburros/GWatch/internal/model"
)

// backupDir returns the directory backups are stored in, or "" when DataDir
// is not configured (as in most tests, which then skip the scheduled job).
func (e *Engine) backupDir() string {
	if e.opts.DataDir == "" {
		return ""
	}
	return filepath.Join(e.opts.DataDir, "backups")
}

// RunBackup checkpoints the database, writes an encrypted archive into dir,
// prunes old archives beyond keep (when keep > 0) and records the outcome in
// the backup status and the event timeline. It is the single code path used
// both by the manual "create backup" API call and the scheduled job, so the
// two stay in sync.
func (e *Engine) RunBackup(ctx context.Context, dir, password string, includeHistory bool, keep int, reason string) (model.BackupInfo, error) {
	_ = e.store.Checkpoint(ctx)
	info, err := backup.Create(ctx, e.store, dir, password, includeHistory, e.opts.Version)
	status := e.BackupStatus()
	now := time.Now()
	status.LastBackupAt = &now
	if err != nil {
		status.LastBackupOK = false
		status.LastError = err.Error()
		e.SetBackupStatus(ctx, status)
		e.RecordEvent(model.Event{Type: model.EventBackup, Title: reason + " failed", Detail: err.Error()})
		return model.BackupInfo{}, err
	}
	status.LastBackupOK = true
	status.LastError = ""
	status.LastBackupFile = info.FileName
	e.SetBackupStatus(ctx, status)
	e.RecordEvent(model.Event{Type: model.EventBackup, Title: reason + " created", Detail: fmt.Sprintf("%s (%s%s)", info.FileName, humanBytes(info.SizeBytes), map[bool]string{true: ", with history", false: ", configuration only"}[info.IncludeHistory])})
	if keep > 0 {
		if removed, perr := backup.Prune(dir, keep); perr == nil && len(removed) > 0 {
			e.RecordEvent(model.Event{Type: model.EventBackup, Title: "Old backups pruned", Detail: fmt.Sprintf("Removed %d archive(s) beyond the configured %d to keep.", len(removed), keep)})
		}
	}
	return info, nil
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// NextBackupAt reports when the scheduled backup job will next run, or nil
// when scheduled backups are disabled or no backup has run yet.
func (e *Engine) NextBackupAt() *time.Time {
	s := e.Settings().Backups
	if !s.Enabled || s.Password == "" {
		return nil
	}
	interval := s.IntervalHours
	if interval <= 0 {
		interval = 24
	}
	last := e.BackupStatus().LastBackupAt
	var next time.Time
	if last == nil {
		next = time.Now()
	} else {
		next = last.Add(time.Duration(interval) * time.Hour)
	}
	return &next
}

// backupLoop periodically checks whether a scheduled backup is due and runs
// it. It ticks every minute so an interval configured in hours still fires
// close to on time without busy-waiting.
func (e *Engine) backupLoop() {
	defer e.wg.Done()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-e.ctx.Done():
			return
		case now := <-ticker.C:
			e.maybeRunScheduledBackup(now)
		}
	}
}

// maybeRunScheduledBackup runs a scheduled backup when one is enabled,
// configured with a password, has a valid data directory and is due. It is
// exposed (unexported but package-testable) so tests can call it directly
// instead of waiting on the ticker.
func (e *Engine) maybeRunScheduledBackup(now time.Time) {
	s := e.Settings().Backups
	if !s.Enabled || s.Password == "" {
		return
	}
	dir := e.backupDir()
	if dir == "" {
		return
	}
	interval := s.IntervalHours
	if interval <= 0 {
		interval = 24
	}
	last := e.BackupStatus().LastBackupAt
	if last != nil && now.Sub(*last) < time.Duration(interval)*time.Hour {
		return
	}
	keep := s.Keep
	if keep <= 0 {
		keep = 7
	}
	if _, err := e.RunBackup(e.ctx, dir, s.Password, s.IncludeHistory, keep, "Scheduled backup"); err != nil {
		e.RecordError("scheduled backup", err)
	}
}

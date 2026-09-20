package api

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/backup"
	"github.com/jxburros/GWatch/internal/engine"
	"github.com/jxburros/GWatch/internal/mailer"
	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/update"
)

const passwordMask = "********"

// ---- maintenance ----

type maintenanceDoc struct {
	model.MaintenanceWindow
	Active bool `json:"active"`
}

func (s *Server) handleListMaintenance(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListMaintenance(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	now := time.Now()
	out := make([]maintenanceDoc, 0, len(list))
	for _, m := range list {
		out = append(out, maintenanceDoc{m, m.Active(now)})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSaveMaintenance(w http.ResponseWriter, r *http.Request) {
	var m model.MaintenanceWindow
	if err := decodeJSON(r, &m); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if r.Method == http.MethodPut {
		id, err := pathID(r, "id")
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		m.ID = id
	} else {
		m.ID = 0
	}
	m.Name = strings.TrimSpace(m.Name)
	if m.Name == "" {
		m.Name = "Maintenance"
	}
	if len(m.Weekdays) == 0 {
		if m.StartAt.IsZero() || m.EndAt.IsZero() || !m.EndAt.After(m.StartAt) {
			writeError(w, http.StatusBadRequest, "end time must be after start time")
			return
		}
	} else {
		for _, d := range m.Weekdays {
			if d < 0 || d > 6 {
				writeError(w, http.StatusBadRequest, "weekdays must be 0 (Sunday) to 6 (Saturday)")
				return
			}
		}
		if m.DurationMinutes <= 0 {
			writeError(w, http.StatusBadRequest, "duration is required for a recurring window")
			return
		}
		if m.StartAt.IsZero() {
			m.StartAt = time.Now()
		}
		if m.EndAt.IsZero() {
			m.EndAt = m.StartAt.AddDate(10, 0, 0)
		}
	}
	if m.NodeID != nil && *m.NodeID <= 0 {
		m.NodeID = nil
	}
	if m.NodeID != nil {
		if _, err := s.Store.GetNode(r.Context(), *m.NodeID); err != nil {
			writeError(w, http.StatusBadRequest, "the selected node does not exist")
			return
		}
		m.Group = ""
	}
	saved, err := s.Store.SaveMaintenance(r.Context(), m)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.configChanged(r.Context(), nil, "Maintenance window saved: "+saved.Name, maintenanceSummary(saved))
	writeJSON(w, http.StatusOK, maintenanceDoc{saved, saved.Active(time.Now())})
}

func maintenanceSummary(m model.MaintenanceWindow) string {
	if len(m.Weekdays) > 0 {
		return fmt.Sprintf("Weekly on %d day(s) at %s for %d minutes.", len(m.Weekdays), m.StartAt.Local().Format("15:04"), m.DurationMinutes)
	}
	return fmt.Sprintf("From %s to %s.", m.StartAt.Local().Format("2006-01-02 15:04"), m.EndAt.Local().Format("2006-01-02 15:04"))
}

func (s *Server) handleDeleteMaintenance(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Store.DeleteMaintenance(r.Context(), id); err != nil {
		s.fail(w, err)
		return
	}
	s.configChanged(r.Context(), nil, "Maintenance window deleted", "")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- dashboards ----

func (s *Server) handleListDashboards(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListDashboards(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleGetDashboard(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	d, err := s.Store.GetDashboard(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleSaveDashboard(w http.ResponseWriter, r *http.Request) {
	var d model.Dashboard
	if err := decodeJSON(r, &d); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if r.Method == http.MethodPut {
		id, err := pathID(r, "id")
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		d.ID = id
	} else {
		d.ID = 0
	}
	d.Name = strings.TrimSpace(d.Name)
	if d.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	for i := range d.Widgets {
		wd := &d.Widgets[i]
		if wd.Width < 1 {
			wd.Width = 2
		}
		if wd.Width > 4 {
			wd.Width = 4
		}
		if wd.Height < 1 {
			wd.Height = 1
		}
		if wd.Height > 6 {
			wd.Height = 6
		}
		if wd.X != nil && (*wd.X < 0 || *wd.X+wd.Width > 4) {
			x := 4 - wd.Width
			if *wd.X >= 0 && *wd.X < x {
				x = *wd.X
			}
			wd.X = &x
		}
		if wd.Y != nil && *wd.Y < 0 {
			y := 0
			wd.Y = &y
		}
		if wd.Type == "" {
			writeError(w, http.StatusBadRequest, "every widget needs a type")
			return
		}
	}
	saved, err := s.Store.SaveDashboard(r.Context(), d)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleDeleteDashboard(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Store.DeleteDashboard(r.Context(), id); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- settings ----

func maskSettings(st model.Settings) model.Settings {
	if st.Alerts.SMTP.Password != "" {
		st.Alerts.SMTP.Password = passwordMask
	}
	if st.General.AccessPassword != "" {
		st.General.AccessPassword = passwordMask
	}
	if st.Backups.Password != "" {
		st.Backups.Password = passwordMask
	}
	return st
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, maskSettings(s.Engine.Settings()))
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	current := s.Engine.Settings()
	var st model.Settings
	if err := decodeJSON(r, &st); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if st.Alerts.SMTP.Password == passwordMask {
		st.Alerts.SMTP.Password = current.Alerts.SMTP.Password
	}
	if st.General.AccessPassword == passwordMask {
		st.General.AccessPassword = current.General.AccessPassword
	}
	if st.Backups.Password == passwordMask {
		st.Backups.Password = current.Backups.Password
	}
	def := model.DefaultSettings()
	g := &st.General
	if strings.TrimSpace(g.InstanceName) == "" {
		g.InstanceName = def.General.InstanceName
	}
	if g.RequireLoginLocally && !current.General.RequireLoginLocally {
		// Turning this on with no administrator to sign in as would lock the
		// owner out of their own monitor.
		n, err := s.Store.CountAdmins(r.Context())
		if err != nil {
			s.fail(w, err)
			return
		}
		if n == 0 {
			writeError(w, http.StatusBadRequest, "create an administrator account before requiring a sign-in on this computer")
			return
		}
	}
	if g.DefaultIntervalSecs < 10 || g.DefaultIntervalSecs > 86400 {
		writeError(w, http.StatusBadRequest, "default interval must be between 10 seconds and 1 day")
		return
	}
	if g.DefaultTimeoutSecs < 1 || g.DefaultTimeoutSecs > 300 {
		writeError(w, http.StatusBadRequest, "default timeout must be between 1 and 300 seconds")
		return
	}
	if g.MaxConcurrentChecks < 1 || g.MaxConcurrentChecks > 64 {
		writeError(w, http.StatusBadRequest, "max concurrent checks must be between 1 and 64")
		return
	}
	if g.MinIntervalSecs < 5 {
		g.MinIntervalSecs = 5
	}
	if g.WallboardRefreshSecs < 5 {
		g.WallboardRefreshSecs = def.General.WallboardRefreshSecs
	}
	if g.LatencyWarnMS < 0 || g.PacketLossWarnPct < 0 || g.PacketLossWarnPct > 100 {
		writeError(w, http.StatusBadRequest, "warning thresholds must be positive (packet loss up to 100%)")
		return
	}
	switch g.Theme {
	case "dark", "light", "system":
	default:
		g.Theme = "dark"
	}
	g.AccentColor = strings.ToLower(strings.TrimSpace(g.AccentColor))
	if g.AccentColor == "" {
		g.AccentColor = def.General.AccentColor
	}
	if !hexColor.MatchString(g.AccentColor) {
		writeError(w, http.StatusBadRequest, "accent colour must be a hex value like #43c9c0")
		return
	}
	g.AccessPassword = strings.TrimSpace(g.AccessPassword)
	g.UpdateRepo = strings.TrimSpace(g.UpdateRepo)
	if g.UpdateRepo == "" {
		g.UpdateRepo = def.General.UpdateRepo
	}
	if !update.ValidRepo(g.UpdateRepo) {
		writeError(w, http.StatusBadRequest, "update repository must look like owner/name")
		return
	}
	a := &st.Alerts
	if a.FailureThreshold < 1 {
		a.FailureThreshold = def.Alerts.FailureThreshold
	}
	if a.CooldownMinutes < 0 {
		a.CooldownMinutes = 0
	}
	if a.CertWarnDays < 1 {
		a.CertWarnDays = def.Alerts.CertWarnDays
	}
	recips := make([]string, 0, len(a.Recipients))
	for _, rcp := range a.Recipients {
		rcp = strings.TrimSpace(rcp)
		if rcp == "" {
			continue
		}
		if !strings.Contains(rcp, "@") {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("%q is not an email address", rcp))
			return
		}
		recips = append(recips, rcp)
	}
	a.Recipients = recips
	if a.SMTP.Security == "" {
		a.SMTP.Security = "starttls"
	}
	if a.SMTP.Port <= 0 {
		a.SMTP.Port = 587
	}
	if a.Enabled {
		if err := mailer.Validate(a.SMTP); err != nil {
			writeError(w, http.StatusBadRequest, "alerts cannot be enabled: "+err.Error())
			return
		}
		if len(a.Recipients) == 0 {
			writeError(w, http.StatusBadRequest, "alerts cannot be enabled without at least one recipient")
			return
		}
	}
	rt := &st.Retention
	if rt.RawDays < 1 || rt.RawDays > 3650 {
		writeError(w, http.StatusBadRequest, "raw results must be kept between 1 and 3650 days")
		return
	}
	if rt.HostDays < 1 || rt.HostDays > 3650 {
		writeError(w, http.StatusBadRequest, "hardware readings must be kept between 1 and 3650 days")
		return
	}
	for _, v := range []int{rt.FiveMinDays, rt.HourlyDays, rt.DailyDays, rt.EventDays} {
		if v < 0 || v > 36500 {
			writeError(w, http.StatusBadRequest, "retention days must be between 0 (forever) and 36500")
			return
		}
	}
	bk := &st.Backups
	if bk.IntervalHours <= 0 {
		bk.IntervalHours = def.Backups.IntervalHours
	}
	if bk.IntervalHours < 1 || bk.IntervalHours > 720 {
		writeError(w, http.StatusBadRequest, "backup interval must be between 1 and 720 hours")
		return
	}
	if bk.Keep <= 0 {
		bk.Keep = def.Backups.Keep
	}
	if bk.Keep < 1 || bk.Keep > 365 {
		writeError(w, http.StatusBadRequest, "the number of backups to keep must be between 1 and 365")
		return
	}
	if bk.Enabled && strings.TrimSpace(bk.Password) == "" {
		writeError(w, http.StatusBadRequest, "scheduled backups cannot be enabled without a password")
		return
	}
	up := &st.Updates
	if up.CheckIntervalHours <= 0 {
		up.CheckIntervalHours = def.Updates.CheckIntervalHours
	}
	if up.CheckIntervalHours < 1 || up.CheckIntervalHours > 720 {
		writeError(w, http.StatusBadRequest, "the update check interval must be between 1 and 720 hours")
		return
	}
	// An unknown condition or colour would simply never light up in the
	// browser, which reads as a bug rather than as a setting that was not
	// taken, so it is refused here with the reason.
	if err := model.ValidateIndicators(st.Indicators); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	st.Indicators = model.NormalizeIndicators(st.Indicators)
	if err := s.Store.SaveSettings(r.Context(), st); err != nil {
		s.fail(w, err)
		return
	}
	s.configChanged(r.Context(), nil, "Settings updated", describeSettingsChange(current, st))
	writeJSON(w, http.StatusOK, maskSettings(s.Engine.Settings()))
}

func describeSettingsChange(before, after model.Settings) string {
	var parts []string
	if before.Alerts.Enabled != after.Alerts.Enabled {
		parts = append(parts, "email alerts "+map[bool]string{true: "enabled", false: "disabled"}[after.Alerts.Enabled])
	}
	if before.Alerts.SMTP.Host != after.Alerts.SMTP.Host || before.Alerts.SMTP.From != after.Alerts.SMTP.From {
		parts = append(parts, "SMTP settings changed")
	}
	if strings.Join(before.Alerts.Recipients, ",") != strings.Join(after.Alerts.Recipients, ",") {
		parts = append(parts, "recipients changed")
	}
	if before.Retention != after.Retention {
		parts = append(parts, "retention policy changed")
	}
	if before.General.RemoteAccess != after.General.RemoteAccess {
		parts = append(parts, "remote access "+map[bool]string{true: "enabled", false: "disabled"}[after.General.RemoteAccess])
	}
	if before.General.AccessPassword != after.General.AccessPassword {
		parts = append(parts, "access password changed")
	}
	if before.General.RequireLoginLocally != after.General.RequireLoginLocally {
		parts = append(parts, "sign-in on this computer "+map[bool]string{true: "required", false: "no longer required"}[after.General.RequireLoginLocally])
	}
	if before.General.Theme != after.General.Theme || before.General.AccentColor != after.General.AccentColor {
		parts = append(parts, "appearance changed")
	}
	if before.General != after.General {
		parts = append(parts, "general settings changed")
	}
	// Automatic update checks are the one thing GWatch does that reaches
	// outside the network on its own, so switching them is worth a line of
	// its own rather than "settings changed".
	if before.Updates.CheckAutomatically != after.Updates.CheckAutomatically {
		parts = append(parts, "automatic update checks "+map[bool]string{true: "enabled", false: "disabled"}[after.Updates.CheckAutomatically])
	}
	if before.Updates.IncludePrerelease != after.Updates.IncludePrerelease {
		parts = append(parts, "pre-release updates "+map[bool]string{true: "offered", false: "no longer offered"}[after.Updates.IncludePrerelease])
	}
	if before.Updates != after.Updates {
		parts = append(parts, "update settings changed")
	}
	if !sameIndicators(before.Indicators, after.Indicators) {
		parts = append(parts, "header indicators changed")
	}
	if len(parts) == 0 {
		return "No effective change."
	}
	return strings.Join(parts, "; ")
}

func (s *Server) handleTestEmail(w http.ResponseWriter, r *http.Request) {
	var body struct {
		To string `json:"to"`
	}
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	var to []string
	if strings.TrimSpace(body.To) != "" {
		to = []string{strings.TrimSpace(body.To)}
	}
	if err := s.Engine.SendTestEmail(r.Context(), to); err != nil {
		writeError(w, http.StatusBadGateway, "Test email failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Test email sent. Check your inbox (and spam folder)."})
}

func (s *Server) handleRetentionStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Engine.RetentionStatus(r.Context()))
}

func (s *Server) handleRetentionRun(w http.ResponseWriter, r *http.Request) {
	if err := s.Engine.RunRetention(r.Context(), true); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.Engine.RetentionStatus(r.Context()))
}

// ---- backups ----

func (s *Server) handleListBackups(w http.ResponseWriter, r *http.Request) {
	list, err := backup.List(s.BackupDir)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"backups": list, "dir": s.BackupDir, "status": s.Engine.BackupStatus(), "nextScheduledAt": s.Engine.NextBackupAt()})
}

func (s *Server) handleCreateBackup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password       string `json:"password"`
		IncludeHistory bool   `json:"includeHistory"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(body.Password) == "" {
		writeError(w, http.StatusBadRequest, "a password is required; backups are always encrypted")
		return
	}
	info, err := s.Engine.RunBackup(r.Context(), s.BackupDir, body.Password, body.IncludeHistory, 0, "Backup")
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, info)
}

func (s *Server) backupPath(name string) (string, bool) {
	if !backup.ValidFileName(name) {
		return "", false
	}
	return filepath.Join(s.BackupDir, name), true
}

func (s *Server) handleDownloadBackup(w http.ResponseWriter, r *http.Request) {
	p, ok := s.backupPath(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid backup name")
		return
	}
	f, err := os.Open(p)
	if err != nil {
		writeError(w, http.StatusNotFound, "backup not found")
		return
	}
	defer f.Close()
	fi, _ := f.Stat()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(p)))
	http.ServeContent(w, r, filepath.Base(p), fi.ModTime(), f)
}

func (s *Server) handleDeleteBackup(w http.ResponseWriter, r *http.Request) {
	p, ok := s.backupPath(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid backup name")
		return
	}
	if err := os.Remove(p); err != nil {
		writeError(w, http.StatusNotFound, "backup not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) restore(w http.ResponseWriter, r *http.Request, path, password string, includeHistory bool) {
	if strings.TrimSpace(password) == "" {
		writeError(w, http.StatusBadRequest, "password is required")
		return
	}
	// Verify the archive before touching the database.
	if _, err := backup.Inspect(path, password); err != nil {
		s.fail(w, err)
		return
	}
	sum, err := backup.Restore(r.Context(), s.Store, path, password, includeHistory)
	if err != nil {
		s.recordEvent(r.Context(), model.Event{Type: model.EventRestore, Title: "Restore failed", Detail: err.Error()})
		s.fail(w, err)
		return
	}
	if err := s.Engine.ReloadConfig(r.Context()); err != nil {
		s.fail(w, err)
		return
	}
	status := s.Engine.BackupStatus()
	now := time.Now()
	status.LastRestoreAt = &now
	s.Engine.SetBackupStatus(r.Context(), status)
	s.recordEvent(r.Context(), model.Event{Type: model.EventRestore, Title: "Backup restored", Detail: fmt.Sprintf("%d node(s), %d check(s)%s restored from %s.", sum.Nodes, sum.Checks, map[bool]string{true: fmt.Sprintf(", %d results, %d events", sum.Results, sum.Events), false: ""}[sum.History], filepath.Base(path))})
	if err := s.Engine.RunRetention(r.Context(), true); err != nil {
		s.Log.Errorf("retention after restore: %v", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "nodes": sum.Nodes, "checks": sum.Checks, "results": sum.Results, "rollups": sum.Rollups, "events": sum.Events, "history": sum.History})
}

func (s *Server) handleRestoreExisting(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FileName       string `json:"fileName"`
		Password       string `json:"password"`
		IncludeHistory bool   `json:"includeHistory"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	p, ok := s.backupPath(body.FileName)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid backup name")
		return
	}
	if _, err := os.Stat(p); err != nil {
		writeError(w, http.StatusNotFound, "backup not found")
		return
	}
	s.restore(w, r, p, body.Password, body.IncludeHistory)
}

func (s *Server) handleRestoreUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid upload: "+err.Error())
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "a backup file is required")
		return
	}
	defer file.Close()
	tmp, err := os.CreateTemp("", "gwatch-upload-*"+backup.Extension)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, file); err != nil {
		tmp.Close()
		s.fail(w, err)
		return
	}
	tmp.Close()
	includeHistory := strings.EqualFold(r.FormValue("includeHistory"), "true")
	s.restore(w, r, tmp.Name(), r.FormValue("password"), includeHistory)
}

// ---- export ----

func csvWriter(w http.ResponseWriter, name string) *csv.Writer {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	return csv.NewWriter(w)
}

func fmtFloat(f *float64) string {
	if f == nil {
		return ""
	}
	return strconv.FormatFloat(*f, 'f', 2, 64)
}

func (s *Server) handleExportHistory(w http.ResponseWriter, r *http.Request) {
	id := queryInt64Ptr(r, "checkId")
	if id == nil {
		writeError(w, http.StatusBadRequest, "checkId is required")
		return
	}
	series, err := s.historyFor(r.Context(), *id, r.URL.Query().Get("range"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cw := csvWriter(w, fmt.Sprintf("gwatch-%s-%s-%s.csv", slug(series.NodeName), slug(series.CheckName), series.Range))
	_ = cw.Write([]string{"timestamp", "avg_ms", "min_ms", "max_ms", "jitter_ms", "loss_pct", "availability_pct", "count", "failures"})
	for _, p := range series.Points {
		_ = cw.Write([]string{p.Timestamp.Format(time.RFC3339), fmtFloat(p.AvgMS), fmtFloat(p.MinMS), fmtFloat(p.MaxMS), fmtFloat(p.JitterMS), fmtFloat(p.LossPct), strconv.FormatFloat(p.Availability, 'f', 2, 64), strconv.Itoa(p.Count), strconv.Itoa(p.Failures)})
	}
	cw.Flush()
}

func (s *Server) handleExportResults(w http.ResponseWriter, r *http.Request) {
	id := queryInt64Ptr(r, "checkId")
	if id == nil {
		writeError(w, http.StatusBadRequest, "checkId is required")
		return
	}
	results, err := s.Store.RecentResults(r.Context(), *id, queryInt(r, "limit", 5000))
	if err != nil {
		s.fail(w, err)
		return
	}
	cw := csvWriter(w, fmt.Sprintf("gwatch-results-%d.csv", *id))
	_ = cw.Write([]string{"timestamp", "success", "status", "message", "error", "latency_ms", "min_ms", "max_ms", "jitter_ms", "loss_pct", "http_status", "final_url", "attempts"})
	for _, res := range results {
		code := ""
		if res.Details.StatusCode != 0 {
			code = strconv.Itoa(res.Details.StatusCode)
		}
		_ = cw.Write([]string{res.Timestamp.Format(time.RFC3339), strconv.FormatBool(res.Success), string(res.Status), res.Message, res.Error, fmtFloat(res.LatencyMS), fmtFloat(res.MinMS), fmtFloat(res.MaxMS), fmtFloat(res.JitterMS), fmtFloat(res.LossPct), code, res.Details.FinalURL, strconv.Itoa(res.Attempts)})
	}
	cw.Flush()
}

func (s *Server) handleExportEvents(w http.ResponseWriter, r *http.Request) {
	events, err := s.Store.ListEvents(r.Context(), s.eventFilterFromQuery(r, 5000))
	if err != nil {
		s.fail(w, err)
		return
	}
	cw := csvWriter(w, "gwatch-events.csv")
	_ = cw.Write([]string{"timestamp", "type", "node", "check", "title", "detail", "node_id", "check_id"})
	for _, e := range events {
		nid, cid := "", ""
		if e.NodeID != nil {
			nid = strconv.FormatInt(*e.NodeID, 10)
		}
		if e.CheckID != nil {
			cid = strconv.FormatInt(*e.CheckID, 10)
		}
		_ = cw.Write([]string{e.Timestamp.Format(time.RFC3339), string(e.Type), e.NodeName, e.CheckName, e.Title, e.Detail, nid, cid})
	}
	cw.Flush()
}

func (s *Server) handleExportConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := backup.ExportConfig(r.Context(), s.Store)
	if err != nil {
		s.fail(w, err)
		return
	}
	cfg.Settings.Alerts.SMTP.Password = ""
	cfg.Settings.Backups.Password = ""
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="gwatch-config.json"`)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(cfg)
}

func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ', r == '-', r == '_', r == '.':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "export"
	}
	return out
}

// ---- logs / stream ----

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"lines": s.Log.Recent(queryInt(r, "limit", 200)), "file": s.Log.Path()})
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "event: hello\ndata: {}\n\n")
	flusher.Flush()
	ch := s.Engine.Subscribe()
	defer s.Engine.Unsubscribe(ch)
	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		case u := <-ch:
			b, _ := json.Marshal(u)
			fmt.Fprintf(w, "event: update\ndata: %s\n\n", b)
			flusher.Flush()
		}
	}
}

var _ = engine.Update{}

// sameIndicators compares two indicator lists. IndicatorRule is comparable
// all the way down, so this is == over the slice rather than reflection.
func sameIndicators(a, b []model.IndicatorRule) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

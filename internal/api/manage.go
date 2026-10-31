package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/auth"
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
		writeDecodeError(w, err)
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
		if m.StartTime != "" || m.TimeZone != "" {
			if len(m.StartTime) != 5 {
				writeError(w, http.StatusBadRequest, "startTime must be HH:MM")
				return
			}
			if _, err := time.Parse("15:04", m.StartTime); err != nil {
				writeError(w, http.StatusBadRequest, "startTime must be HH:MM")
				return
			}
			if m.TimeZone == "" || m.TimeZone == "Local" {
				writeError(w, http.StatusBadRequest, "timeZone must be an explicit IANA time zone")
				return
			}
			if _, err := time.LoadLocation(m.TimeZone); err != nil {
				writeError(w, http.StatusBadRequest, "unknown timeZone")
				return
			}
		}
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
	// A node id that is not a node is a narrow scope that lost its node, not
	// a request for every node; widening it silently would silence them all.
	if m.NodeID != nil && *m.NodeID <= 0 {
		writeError(w, http.StatusBadRequest, "nodeId must be a node's id, or null for a window over all nodes")
		return
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
		// A window with its own start time runs at that time in its own
		// zone; StartAt is only when it was saved.
		if m.StartTime != "" {
			return fmt.Sprintf("Weekly on %d day(s) at %s %s for %d minutes.", len(m.Weekdays), m.StartTime, m.TimeZone, m.DurationMinutes)
		}
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
		writeDecodeError(w, err)
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
		// A widget's configuration is the interface's to read, but a network
		// map's line colour ends up in a drawing, so it is held to the same
		// rule as the colour of a link.
		if wd.Type == "network_map" {
			cfg, err := model.CleanMapLineConfig(wd.Config)
			if err != nil {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("network map widget: %v", err))
				return
			}
			wd.Config = cfg
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
	st.Alerts.Channels = maskAlertChannels(st.Alerts.Channels)
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
		writeDecodeError(w, err)
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
	if err := s.validateSettings(&st, current, func() (int, error) { return s.Store.CountAdmins(r.Context()) }); err != nil {
		var bad settingsError
		if errors.As(err, &bad) {
			writeError(w, http.StatusBadRequest, bad.Error())
			return
		}
		s.fail(w, err)
		return
	}
	if err := s.Store.SaveSettings(r.Context(), st); err != nil {
		s.fail(w, err)
		return
	}
	s.configChanged(r.Context(), nil, "Settings updated", describeSettingsChange(current, st))
	writeJSON(w, http.StatusOK, maskSettings(s.Engine.Settings()))
}

// settingsError is a settings document refused by validateSettings, which a
// save answers with 400.
type settingsError string

func (e settingsError) Error() string { return string(e) }

func badSettings(msg string) error { return settingsError(msg) }

// validateSettings validates and tidies a settings document the way
// PUT /api/settings does, against the settings in force (current), which
// decide what counts as switching something on. countAdmins says how many
// administrators there will be once the document applies; turning on a
// sign-in on this computer is refused when the answer is none. A document
// that does not validate is refused with a settingsError; any other error
// is the database's. A restore holds an archive's settings to the same
// rules (restore_check.go).
func (s *Server) validateSettings(st *model.Settings, current model.Settings, countAdmins func() (int, error)) error {
	def := model.DefaultSettings()
	g := &st.General
	if g.ListenPort < 0 || g.ListenPort > 65535 {
		return badSettings("listen port must be between 1 and 65535 (or zero for the startup default)")
	}
	if strings.TrimSpace(g.InstanceName) == "" {
		g.InstanceName = def.General.InstanceName
	}
	if g.RequireLoginLocally && !current.General.RequireLoginLocally {
		// Turning this on with no administrator to sign in as would lock the
		// owner out of their own monitor.
		n, err := countAdmins()
		if err != nil {
			return err
		}
		if n == 0 {
			return badSettings("create an administrator account before requiring a sign-in on this computer")
		}
	}
	// The hashing algorithm (#49). Empty is how every settings document saved
	// before the option existed looks, so it means the Argon2id default;
	// anything else that is not a known name is refused rather than quietly
	// replaced, because it decides how passwords and backups are protected.
	alg, ok := auth.ParseAlgorithm(g.PasswordHashing)
	if !ok {
		return badSettings(fmt.Sprintf(`password hashing must be %q or %q`, auth.Argon2id, auth.PBKDF2))
	}
	g.PasswordHashing = string(alg)
	if g.DefaultIntervalSecs < 10 || g.DefaultIntervalSecs > 86400 {
		return badSettings("default interval must be between 10 seconds and 1 day")
	}
	if g.DefaultTimeoutSecs < 1 || g.DefaultTimeoutSecs > 300 {
		return badSettings("default timeout must be between 1 and 300 seconds")
	}
	if g.MaxConcurrentChecks < 1 || g.MaxConcurrentChecks > 64 {
		return badSettings("max concurrent checks must be between 1 and 64")
	}
	if g.MinIntervalSecs < 5 {
		g.MinIntervalSecs = 5
	}
	if g.WallboardRefreshSecs < 5 {
		g.WallboardRefreshSecs = def.General.WallboardRefreshSecs
	}
	if g.LatencyWarnMS < 0 || g.PacketLossWarnPct < 0 || g.PacketLossWarnPct > 100 {
		return badSettings("warning thresholds must be positive (packet loss up to 100%)")
	}
	// An empty ping method is how every settings document written before this
	// setting existed looks, so it means the default rather than an error.
	g.PingMethod = strings.TrimSpace(g.PingMethod)
	if g.PingMethod == "" {
		g.PingMethod = def.General.PingMethod
	}
	if !model.ValidPingMethod(g.PingMethod) {
		return badSettings(`ping method must be "auto", "builtin" or "system"`)
	}
	// Zero is how a settings document from before the ping count existed
	// looks, so it means the default rather than an error.
	if g.PingCount == 0 {
		g.PingCount = model.DefaultPingCount
	}
	if g.PingCount < 1 || g.PingCount > model.MaxPingCount {
		return badSettings(fmt.Sprintf("pings per check must be between 1 and %d", model.MaxPingCount))
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
		return badSettings("accent colour must be a hex value like #43c9c0")
	}
	g.AccessPassword = strings.TrimSpace(g.AccessPassword)
	g.UpdateRepo = strings.TrimSpace(g.UpdateRepo)
	if g.UpdateRepo == "" {
		g.UpdateRepo = def.General.UpdateRepo
	}
	if !update.ValidRepo(g.UpdateRepo) {
		return badSettings("update repository must look like owner/name")
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
	if a.GroupWindowSeconds < 0 || a.GroupWindowSeconds > model.MaxAlertGroupWindowSeconds {
		return badSettings(fmt.Sprintf("the alert grouping window must be between 0 (off) and %d seconds", model.MaxAlertGroupWindowSeconds))
	}
	if !model.ValidWarningDigest(a.WarningDigest) {
		return badSettings("the warning digest must be off, hourly or daily")
	}
	if a.WarningDigest == "" {
		a.WarningDigest = model.WarningDigestOff
	}
	recips := make([]string, 0, len(a.Recipients))
	for _, rcp := range a.Recipients {
		rcp = strings.TrimSpace(rcp)
		if rcp == "" {
			continue
		}
		if !strings.Contains(rcp, "@") {
			return badSettings(fmt.Sprintf("%q is not an email address", rcp))
		}
		recips = append(recips, rcp)
	}
	a.Recipients = recips
	channels, err := s.normalizeAlertChannels(a.Channels, current.Alerts.Channels)
	if err != nil {
		return badSettings(err.Error())
	}
	a.Channels = channels
	if a.SMTP.Security == "" {
		a.SMTP.Security = "starttls"
	}
	if a.SMTP.Port <= 0 {
		a.SMTP.Port = 587
	}
	// Alerts can go out by e-mail, through the "also notify" channels, or
	// both. Whichever is configured has to work: e-mail recipients need SMTP
	// settings that validate, and with no recipients at least one channel
	// has to be switched on, or enabling alerts would send nothing at all.
	if a.Enabled {
		if len(a.Recipients) == 0 && enabledAlertChannels(a.Channels) == 0 {
			return badSettings("alerts cannot be enabled without at least one recipient or notification channel")
		}
		if len(a.Recipients) > 0 {
			if err := mailer.Validate(a.SMTP); err != nil {
				return badSettings("alerts cannot be enabled: " + err.Error())
			}
		}
	}
	rt := &st.Retention
	if rt.RawDays < 1 || rt.RawDays > 3650 {
		return badSettings("raw results must be kept between 1 and 3650 days")
	}
	if rt.HostDays < 1 || rt.HostDays > 3650 {
		return badSettings("hardware check results must be kept between 1 and 3650 days")
	}
	for _, v := range []int{rt.FiveMinDays, rt.HourlyDays, rt.DailyDays, rt.EventDays} {
		if v < 0 || v > 36500 {
			return badSettings("retention days must be between 0 (forever) and 36500")
		}
	}
	bk := &st.Backups
	if bk.IntervalHours <= 0 {
		bk.IntervalHours = def.Backups.IntervalHours
	}
	if bk.IntervalHours < 1 || bk.IntervalHours > 720 {
		return badSettings("backup interval must be between 1 and 720 hours")
	}
	if bk.Keep <= 0 {
		bk.Keep = def.Backups.Keep
	}
	if bk.Keep < 1 || bk.Keep > 365 {
		return badSettings("the number of backups to keep must be between 1 and 365")
	}
	if bk.Enabled && strings.TrimSpace(bk.Password) == "" {
		return badSettings("scheduled backups cannot be enabled without a password")
	}
	up := &st.Updates
	if up.CheckIntervalHours <= 0 {
		up.CheckIntervalHours = def.Updates.CheckIntervalHours
	}
	if up.CheckIntervalHours < 1 || up.CheckIntervalHours > 720 {
		return badSettings("the update check interval must be between 1 and 720 hours")
	}
	// An unknown condition or colour would simply never light up in the
	// browser, which reads as a bug rather than as a setting that was not
	// taken, so it is refused here with the reason.
	if err := model.ValidateIndicators(st.Indicators); err != nil {
		return badSettings(err.Error())
	}
	st.Indicators = model.NormalizeIndicators(st.Indicators)
	return nil
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
	if alertChannelsSummary(before.Alerts.Channels) != alertChannelsSummary(after.Alerts.Channels) {
		parts = append(parts, fmt.Sprintf("alert channels changed (%d enabled)", enabledAlertChannels(after.Alerts.Channels)))
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
	// How passwords and backups are protected is a security decision, so it
	// gets a line of its own in the audit trail.
	if before.General.PasswordHashing != after.General.PasswordHashing {
		parts = append(parts, "password hashing set to "+after.General.PasswordHashing)
	}
	if before.General.Theme != after.General.Theme || before.General.AccentColor != after.General.AccentColor {
		parts = append(parts, "appearance changed")
	}
	// How a ping is sent decides whether GWatch runs an external program, so
	// it gets a line rather than disappearing into "general settings changed".
	if before.General.PingMethod != after.General.PingMethod {
		parts = append(parts, "ping method set to "+after.General.PingMethod)
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
			writeDecodeError(w, err)
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
		writeDecodeError(w, err)
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

// restoreTimeout bounds a restore, which carries on if the browser that asked
// for it goes away: stopping a restore half-way is how a database ends up
// emptied, so the request's own cancellation is not passed on.
const restoreTimeout = 2 * time.Hour

func (s *Server) restore(w http.ResponseWriter, r *http.Request, path, password string, includeHistory, recreateAccounts bool) {
	if strings.TrimSpace(password) == "" {
		writeError(w, http.StatusBadRequest, "password is required")
		return
	}
	// Accounts missing from this server are only re-created when asked, or
	// when there are none at all (a restore onto a fresh installation).
	if !recreateAccounts {
		if n, err := s.Store.CountUsers(r.Context()); err == nil && n == 0 {
			recreateAccounts = true
		}
	}
	// Read and validate the archive before touching the database, or taking
	// the safety backup: an archive that is unreadable, too large or not
	// valid (checkArchive) is refused here with nothing changed.
	_, cfg, err := backup.Read(path, password)
	if err == nil {
		err = s.checkArchive(r.Context(), &cfg, recreateAccounts)
	}
	if err != nil {
		s.writeArchiveError(w, err)
		return
	}
	// One restore at a time: while another holds the engine paused, this one
	// is refused before it takes a safety backup. (Pause below is what
	// actually enforces it; this only answers sooner.)
	if s.Engine.Paused() {
		writeError(w, http.StatusConflict, "another restore is in progress; wait for it to finish")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), restoreTimeout)
	defer cancel()

	// A safety net first: everything as it is now, history included, under
	// the same password, so a restore that was a mistake (or that fails
	// part-way) can itself be undone from Settings › Backups. Monitoring
	// carries on while it is written; it is paused only for the restore.
	safety := ""
	var safetyEvent model.Event
	if s.BackupDir != "" {
		info, err := s.Engine.RunBackup(ctx, s.BackupDir, password, true, 0, "Pre-restore backup")
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not take a backup of the current data before restoring, so nothing was changed: "+err.Error())
			return
		}
		safety = info.FileName
		safetyEvent = engine.BackupCreatedEvent("Pre-restore backup", info, time.Now())
	}

	// Pause the engine for the restore (engine/pause.go): no check runs, and
	// nothing it does on its own writes, until the restored configuration is
	// loaded. Whatever happens below, the engine reloads its configuration
	// and resumes before this returns.
	if err := s.Engine.Pause(ctx); err != nil {
		if errors.Is(err, engine.ErrAlreadyPaused) {
			writeError(w, http.StatusConflict, "another restore is in progress; wait for it to finish")
			return
		}
		s.fail(w, err)
		return
	}
	resumed := false
	var reloadErr error
	resume := func() {
		if resumed {
			return
		}
		resumed = true
		reloadErr = s.Engine.ReloadConfig(ctx)
		s.Engine.Resume()
	}
	defer resume()

	sum, err := backup.RestoreWith(ctx, s.Store, path, password, backup.RestoreOptions{IncludeHistory: includeHistory, RecreateAccounts: recreateAccounts,
		// Checked again on the copy actually restored, which is the one that
		// counts should the file have changed since it was read above.
		Validate: func(c *backup.Config) error { return s.checkArchive(ctx, c, recreateAccounts) }})
	var partial *backup.HistoryError
	if err == nil || errors.As(err, &partial) {
		// The restored checks start from their stored (emptied) states, not
		// from what the engine held for whatever had the same id before.
		s.Engine.DiscardStates()
	}
	resume()
	if err != nil && !errors.As(err, &partial) {
		// Refused, or failed and rolled back: the configuration is as it was.
		var invalid *backup.InvalidError
		if errors.As(err, &invalid) || errors.Is(err, backup.ErrArchiveTooLarge) {
			s.writeArchiveError(w, err)
			return
		}
		detail := err.Error() + " Nothing was changed."
		if safety != "" {
			detail += fmt.Sprintf(" The data as it was before is also in %s.", safety)
		}
		s.recordEvent(ctx, model.Event{Type: model.EventRestore, Title: "Restore failed", Detail: detail})
		if safety != "" {
			writeError(w, http.StatusInternalServerError, "restore failed: "+detail)
			return
		}
		s.fail(w, err)
		return
	}
	// The configuration was restored (and with partial, the history only
	// in part).
	if reloadErr != nil {
		s.Log.Errorf("reload after restore: %v", reloadErr)
	}
	status := s.Engine.BackupStatus()
	now := time.Now()
	status.LastRestoreAt = &now
	s.Engine.SetBackupStatus(ctx, status)
	detail := fmt.Sprintf("%d node(s), %d check(s)%s restored from %s.", sum.Nodes, sum.Checks, map[bool]string{true: fmt.Sprintf(", %d results, %d events", sum.Results, sum.Events), false: ""}[sum.History], filepath.Base(path))
	if len(sum.SkippedUsers) > 0 {
		detail += fmt.Sprintf(" Not re-created, as they are no longer on this server: %d account(s) (%s).", len(sum.SkippedUsers), strings.Join(sum.SkippedUsers, ", "))
	}
	if len(sum.SkippedAgents) > 0 {
		detail += fmt.Sprintf(" Not re-created: %d agent(s) (%s).", len(sum.SkippedAgents), strings.Join(sum.SkippedAgents, ", "))
	}
	title := "Backup restored"
	if partial != nil {
		title = "Backup restored without all of its history"
		detail = fmt.Sprintf("The configuration was restored (%d node(s), %d check(s)), but importing the history stopped part-way after %d results, %d rollups and %d events: %v.",
			sum.Nodes, sum.Checks, sum.Results, sum.Rollups, sum.Events, partial.Err)
	}
	if safety != "" {
		detail += " The data as it was before is in " + safety + "."
		// Restoring the history replaced every event, the safety backup's
		// "created" among them; put it back, at the time it was taken.
		if sum.History {
			s.Engine.RecordEvent(safetyEvent)
		}
	}
	s.recordEvent(ctx, model.Event{Type: model.EventRestore, Title: title, Detail: detail})
	if err := s.Engine.RunRetention(ctx, true); err != nil {
		s.Log.Errorf("retention after restore: %v", err)
	}
	if partial != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "restore incomplete: " + detail, "configRestored": true,
			"nodes": sum.Nodes, "checks": sum.Checks, "results": sum.Results, "rollups": sum.Rollups, "events": sum.Events, "history": sum.History,
			"skippedUsers": nonNil(sum.SkippedUsers), "skippedAgents": nonNil(sum.SkippedAgents), "safetyBackup": safety})
		return
	}
	if reloadErr != nil {
		s.fail(w, reloadErr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "nodes": sum.Nodes, "checks": sum.Checks, "results": sum.Results, "rollups": sum.Rollups, "events": sum.Events, "history": sum.History,
		"skippedUsers": nonNil(sum.SkippedUsers), "skippedAgents": nonNil(sum.SkippedAgents), "safetyBackup": safety})
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (s *Server) handleRestoreExisting(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FileName        string `json:"fileName"`
		Password        string `json:"password"`
		IncludeHistory  bool   `json:"includeHistory"`
		RestoreAccounts bool   `json:"restoreAccounts"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeDecodeError(w, err)
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
	s.restore(w, r, p, body.Password, body.IncludeHistory, body.RestoreAccounts)
}

// restoreUploadLimit caps a restore upload: the largest archive a restore
// accepts plus room for the multipart framing and the password and
// includeHistory fields beside it. A variable so tests can lower it.
var restoreUploadLimit = backup.MaxArchiveBytes + 1<<20

func (s *Server) handleRestoreUpload(w http.ResponseWriter, r *http.Request) {
	path, done, ok := s.receiveBackupUpload(w, r)
	if !ok {
		return
	}
	defer done()
	includeHistory := strings.EqualFold(r.FormValue("includeHistory"), "true")
	recreateAccounts := strings.EqualFold(r.FormValue("restoreAccounts"), "true")
	s.restore(w, r, path, r.FormValue("password"), includeHistory, recreateAccounts)
}

// ---- export ----

func csvWriter(w http.ResponseWriter, name string) *csv.Writer {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	// A byte-order mark first: without it Excel opens a UTF-8 file as the
	// system code page, and every em dash, accent and › in a node name or
	// message comes out garbled. Other spreadsheets ignore it.
	_, _ = io.WriteString(w, "\uFEFF")
	return csv.NewWriter(w)
}

// csvText makes a text cell safe to open in a spreadsheet. encoding/csv keeps
// the file's structure intact, but Excel, LibreOffice and Sheets read a cell
// that starts with =, +, -, @, a tab or a carriage return as a formula — and
// node names, check names, event notes, result messages and URLs are text
// that monitored devices and API keys can choose. A leading apostrophe makes
// the spreadsheet show the cell as text. Only text columns go through here;
// numbers GWatch formats itself (a negative latency, say) are left alone.
func csvText(v string) string {
	if v == "" {
		return v
	}
	switch v[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + v
	}
	return v
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
	end, err := historyEnd(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	series, err := s.historyFor(r.Context(), *id, r.URL.Query().Get("range"), r.URL.Query().Get("metric"), end)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cw := csvWriter(w, fmt.Sprintf("gwatch-%s-%s-%s.csv", slug(series.NodeName), slug(series.CheckName), series.Range))
	if series.Metric != "" {
		// A named metric is in its own unit, not milliseconds: its values go
		// under "value", with the unit beside them.
		_ = cw.Write([]string{"timestamp", "value", "unit", "availability_pct"})
		for _, p := range series.Points {
			value := ""
			if p.Value != nil {
				value = strconv.FormatFloat(*p.Value, 'f', -1, 64)
			}
			_ = cw.Write([]string{p.Timestamp.Format(time.RFC3339), value, csvText(series.MetricUnit), strconv.FormatFloat(p.Availability, 'f', 2, 64)})
		}
		cw.Flush()
		return
	}
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
	_ = cw.Write([]string{"timestamp", "success", "status", "message", "error", "latency_ms", "min_ms", "max_ms", "jitter_ms", "stddev_ms", "loss_pct", "http_status", "final_url", "attempts"})
	for _, res := range results {
		code := ""
		if res.Details.StatusCode != 0 {
			code = strconv.Itoa(res.Details.StatusCode)
		}
		_ = cw.Write([]string{res.Timestamp.Format(time.RFC3339), strconv.FormatBool(res.Success), csvText(string(res.Status)), csvText(res.Message), csvText(res.Error), fmtFloat(res.LatencyMS), fmtFloat(res.MinMS), fmtFloat(res.MaxMS), fmtFloat(res.JitterMS), fmtFloat(res.StdDevMS), fmtFloat(res.LossPct), code, csvText(res.Details.FinalURL), strconv.Itoa(res.Attempts)})
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
		_ = cw.Write([]string{e.Timestamp.Format(time.RFC3339), csvText(string(e.Type)), csvText(e.NodeName), csvText(e.CheckName), csvText(e.Title), csvText(e.Detail), nid, cid})
	}
	cw.Flush()
}

func (s *Server) handleExportConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := backup.ExportConfig(r.Context(), s.Store)
	if err != nil {
		s.fail(w, err)
		return
	}
	cfg.Incidents = nil
	for i := range cfg.Reports {
		cfg.Reports[i].Recipients = nil
		cfg.Reports[i].Enabled = false
	}
	cfg.Settings.General.AccessPassword = ""
	cfg.Users, cfg.APIKeys, cfg.Agents = nil, nil, nil
	for i := range cfg.Triggers {
		_ = cfg.Triggers[i].Action.TransformSecrets(blankSecret)
	}
	for i := range cfg.Rules {
		for j := range cfg.Rules[i].Actions {
			_ = cfg.Rules[i].Actions[j].TransformSecrets(blankSecret)
		}
	}
	for i := range cfg.Endpoints {
		cfg.Endpoints[i].Token = ""
		_ = cfg.Endpoints[i].Action.TransformSecrets(blankSecret)
	}
	for i := range cfg.Wallboards {
		cfg.Wallboards[i].Share.Token = ""
	}
	cfg.Settings.Alerts.SMTP.Password = ""
	cfg.Settings.Alerts.Channels = blankAlertChannels(cfg.Settings.Alerts.Channels)
	cfg.Settings.Backups.Password = ""
	// A configuration export is meant to be readable and shareable, so the
	// checks' credentials come out empty for the same reason the passwords do.
	for i := range cfg.Nodes {
		for j := range cfg.Nodes[i].Checks {
			_ = cfg.Nodes[i].Checks[j].Config.TransformSecrets(blankSecret)
		}
	}
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
	// Subscribe before saying hello: a client that acts on "hello" (reloads,
	// saves something) must not miss the update its own action broadcasts in
	// the gap before the subscription exists.
	ch := s.Engine.Subscribe()
	defer s.Engine.Unsubscribe(ch)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "event: hello\ndata: {}\n\n")
	flusher.Flush()
	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()
	done := s.streams()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-done:
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

func (s *Server) streams() chan struct{} {
	s.streamsOnce.Do(func() { s.streamsDone = make(chan struct{}) })
	return s.streamsDone
}

// CloseStreams ends every open /api/stream response, now and from then on.
// http.Server.Shutdown waits for responses to finish and a stream never does,
// so without this one open browser tab held every stop up for the whole
// shutdown timeout. Register it with http.Server.RegisterOnShutdown.
func (s *Server) CloseStreams() {
	ch := s.streams()
	s.streamsClose.Do(func() { close(ch) })
}

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

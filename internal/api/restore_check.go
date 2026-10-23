package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/jxburros/GWatch/internal/auth"
	"github.com/jxburros/GWatch/internal/backup"
	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/reports"
	"github.com/jxburros/GWatch/internal/store"
)

// A restore writes an archive's configuration straight into the database,
// below the API. Before #148 that meant an archive was trusted completely:
// anyone who could hand an administrator a backup file and its password could
// add administrator accounts, triggers that run scripts the API would refuse,
// endpoints callable without a token, and settings no settings page would
// save. Now the archive's configuration goes through the same validation the
// API applies to each of those things when they are saved one at a time
// (checkArchive below), and a restore of an archive that fails any of it is
// refused with the full list of problems, before anything is written. And
// before a restore is applied the browser asks for a preview (POST
// /api/backups/inspect), which lists what the archive would add or change
// where it matters for security, so an administrator confirms knowing what
// they are letting in.

// checkArchive validates, and tidies in place, an archive's configuration the
// way the API validates each part of it on a save: settings, nodes and their
// checks, triggers, custom endpoints, notification rules, scheduled reports,
// network-map widgets, accounts, API keys and agents. recreateAccounts says
// whether the archive's missing accounts will be re-created, which decides how
// many administrators there will be afterwards. Every problem found is
// collected; an archive with any is refused with a *backup.InvalidError.
// Another error is the database's.
func (s *Server) checkArchive(ctx context.Context, cfg *backup.Config, recreateAccounts bool) error {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	// Accounts first: the settings check needs to know whether there will be
	// an administrator to sign in as.
	admins, err := s.Store.CountAdmins(ctx)
	if err != nil {
		return err
	}
	seenUser := map[string]bool{}
	for i, u := range cfg.Users {
		label := fmt.Sprintf("account %d", i+1)
		if u.Username != "" {
			label = fmt.Sprintf("account %q", u.Username)
		}
		if err := validateUsername(u.Username); err != nil {
			add("%s: %v", label, err)
		}
		key := strings.ToLower(strings.TrimSpace(u.Username))
		if seenUser[key] {
			add("%s appears more than once", label)
		}
		seenUser[key] = true
		if !auth.Role(u.Role).Valid() {
			add("%s: unknown role %q", label, u.Role)
		}
		if strings.TrimSpace(u.PasswordHash) == "" {
			add("%s has no password hash", label)
		}
		if recreateAccounts && auth.Role(u.Role) == auth.RoleAdmin {
			if _, _, err := s.Store.GetUserByName(ctx, u.Username); errors.Is(err, store.ErrNotFound) {
				admins++
			} else if err != nil {
				return err
			}
		}
	}
	for i, k := range cfg.APIKeys {
		if strings.TrimSpace(k.KeyHash) == "" {
			add("API key %d (%q) has no key digest", i+1, k.Name)
		}
		if sc := auth.Scope(k.Scope); sc != auth.ScopeRead && sc != auth.ScopeReadWrite {
			add("API key %q: unknown scope %q", k.Name, k.Scope)
		}
	}

	// Settings, against the settings in force: switching LAN sign-in on is
	// judged against what is on now. The alert channels are the exception:
	// they are compared with the archive's own, so that their ids (which the
	// notification history names) are kept rather than issued afresh.
	current := s.Engine.Settings()
	current.Alerts.Channels = cfg.Settings.Alerts.Channels
	if err := s.validateSettings(&cfg.Settings, current, func() (int, error) { return admins, nil }); err != nil {
		var bad settingsError
		if !errors.As(err, &bad) {
			return err
		}
		add("settings: %v", bad)
	}

	// Nodes and checks, under the archive's settings, with the ids a restore
	// keeps. The one relaxation is the minimum check interval: it is held to
	// the absolute floor rather than to the archive's own minimum, because
	// raising the minimum in Settings › General leaves the checks already
	// faster than it alone, and a backup of such a server must still
	// restore. The minimum is overload protection, not a matter of trust.
	nodeSettings := cfg.Settings
	nodeSettings.General.MinIntervalSecs = 5
	nodeByID := map[int64]*model.Node{}
	checkIDs := map[int64]bool{}
	for i := range cfg.Nodes {
		n := &cfg.Nodes[i]
		label := fmt.Sprintf("node %q", n.Name)
		if err := normalizeNodeWith(n, nodeSettings); err != nil {
			add("%s: %v", label, err)
		}
		if n.ID <= 0 || nodeByID[n.ID] != nil {
			add("%s: invalid or duplicate node id %d", label, n.ID)
		} else {
			nodeByID[n.ID] = n
		}
		for _, c := range n.Checks {
			if c.ID <= 0 || checkIDs[c.ID] {
				add("%s: check %q has an invalid or duplicate id %d", label, c.Name, c.ID)
			}
			checkIDs[c.ID] = true
		}
	}
	for _, n := range cfg.Nodes {
		if n.DependsOnNode != nil && nodeByID[*n.DependsOnNode] == nil {
			add("node %q depends on a node that is not in the backup", n.Name)
		}
	}

	for i := range cfg.Triggers {
		t := &cfg.Triggers[i]
		if err := s.normalizeTriggerFor(t, nodeByID[t.NodeID]); err != nil {
			add("trigger %q: %v", t.Name, err)
		}
	}
	slugs := map[string]bool{}
	for i := range cfg.Endpoints {
		e := &cfg.Endpoints[i]
		if err := s.normalizeEndpoint(e); err != nil {
			add("endpoint %q: %v", e.Name, err)
			continue
		}
		if slugs[e.Slug] {
			add("endpoint %q: another endpoint already has the URL name %q", e.Name, e.Slug)
		}
		slugs[e.Slug] = true
	}
	for i := range cfg.Rules {
		r := &cfg.Rules[i]
		if err := s.normalizeRuleIn(r, cfg.Nodes); err != nil {
			add("notification rule %q: %v", r.Name, err)
		}
	}
	if err := reports.Validate(cfg.Reports); err != nil {
		add("scheduled reports: %v", err)
	}
	for i := range cfg.Dashboards {
		d := &cfg.Dashboards[i]
		for j := range d.Widgets {
			wd := &d.Widgets[j]
			if wd.Type != "network_map" {
				continue
			}
			clean, err := model.CleanMapLineConfig(wd.Config)
			if err != nil {
				add("dashboard %q: network map widget: %v", d.Name, err)
				continue
			}
			wd.Config = clean
		}
	}
	for _, a := range cfg.Agents {
		if strings.TrimSpace(a.TokenHash) == "" {
			add("agent %q has no token digest", a.Name)
		}
		if a.NodeID != nil && nodeByID[*a.NodeID] == nil {
			add("agent %q is linked to a node that is not in the backup", a.Name)
		}
	}

	if len(problems) > 0 {
		return &backup.InvalidError{Problems: problems}
	}
	return nil
}

// writeArchiveError answers a restore or preview refused for the archive's
// content (400 with the list of problems, 413 for one too large), or reports
// any other error the usual way.
func (s *Server) writeArchiveError(w http.ResponseWriter, err error) {
	var invalid *backup.InvalidError
	switch {
	case errors.As(err, &invalid):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": invalid.Error(), "problems": invalid.Problems})
	case errors.Is(err, backup.ErrArchiveTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, err.Error())
	default:
		s.fail(w, err)
	}
}

// preview works out what restoring the archive at path would do, with the
// archive's validation problems, if any, listed rather than refused: the
// preview is where the administrator gets to read them.
func (s *Server) preview(ctx context.Context, path, password string, recreateAccounts bool) (backup.Preview, error) {
	m, cfg, err := backup.Read(path, password)
	if err != nil {
		return backup.Preview{}, err
	}
	p, err := backup.Plan(ctx, s.Store, m, cfg, checkRunsCode)
	if err != nil {
		return p, err
	}
	if !recreateAccounts && p.AccountsRecreatedAnyway {
		recreateAccounts = true
	}
	if err := s.checkArchive(ctx, &cfg, recreateAccounts); err != nil {
		var invalid *backup.InvalidError
		if !errors.As(err, &invalid) {
			return p, err
		}
		p.Problems = invalid.Problems
	}
	return p, nil
}

// handleInspectUpload is POST /api/backups/inspect: the same multipart form
// as a restore (file, password, restoreAccounts), answered with the preview
// of what restoring it would do. Nothing is changed.
func (s *Server) handleInspectUpload(w http.ResponseWriter, r *http.Request) {
	path, done, ok := s.receiveBackupUpload(w, r)
	if !ok {
		return
	}
	defer done()
	s.writePreview(w, r, path, r.FormValue("password"), strings.EqualFold(r.FormValue("restoreAccounts"), "true"))
}

// handleInspectExisting is POST /api/backups/inspect-existing: the preview
// for an archive already in the backup folder, body {fileName, password,
// restoreAccounts}.
func (s *Server) handleInspectExisting(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FileName        string `json:"fileName"`
		Password        string `json:"password"`
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
	s.writePreview(w, r, p, body.Password, body.RestoreAccounts)
}

func (s *Server) writePreview(w http.ResponseWriter, r *http.Request, path, password string, recreateAccounts bool) {
	if strings.TrimSpace(password) == "" {
		writeError(w, http.StatusBadRequest, "password is required")
		return
	}
	p, err := s.preview(r.Context(), path, password, recreateAccounts)
	if err != nil {
		s.writeArchiveError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// receiveBackupUpload stores the "file" of a multipart restore or preview
// request in a temporary file, within restoreUploadLimit, and returns its
// path and a function that removes it. When it returns false it has already
// answered the request.
func (s *Server) receiveBackupUpload(w http.ResponseWriter, r *http.Request) (string, func(), bool) {
	// ParseMultipartForm's argument only decides what is kept in memory
	// rather than spooled to disk; the body itself is capped here.
	r.Body = http.MaxBytesReader(w, r.Body, restoreUploadLimit)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("backup file is larger than the %d MiB limit", backup.MaxArchiveBytes>>20))
			return "", nil, false
		}
		writeError(w, http.StatusBadRequest, "invalid upload: "+err.Error())
		return "", nil, false
	}
	form := r.MultipartForm
	file, _, err := r.FormFile("file")
	if err != nil {
		form.RemoveAll()
		writeError(w, http.StatusBadRequest, "a backup file is required")
		return "", nil, false
	}
	defer file.Close()
	tmp, err := os.CreateTemp("", "gwatch-upload-*"+backup.Extension)
	if err != nil {
		form.RemoveAll()
		s.fail(w, err)
		return "", nil, false
	}
	done := func() { os.Remove(tmp.Name()); form.RemoveAll() }
	if _, err := io.Copy(tmp, file); err != nil {
		tmp.Close()
		done()
		s.fail(w, err)
		return "", nil, false
	}
	tmp.Close()
	return tmp.Name(), done, true
}

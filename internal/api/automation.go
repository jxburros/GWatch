package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jxburros/GWatch/internal/actions"
	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
	"github.com/jxburros/GWatch/internal/update"
)

// ---- triggers ----

func (s *Server) normalizeAction(a *model.Action) error {
	a.Method = strings.ToUpper(strings.TrimSpace(a.Method))
	a.URL = strings.TrimSpace(a.URL)
	a.Repo = strings.TrimSpace(a.Repo)
	a.GitArgs = strings.TrimSpace(a.GitArgs)
	a.Interpreter = strings.TrimSpace(a.Interpreter)
	a.Command = strings.TrimSpace(a.Command)
	a.WorkDir = strings.TrimSpace(a.WorkDir)
	if a.Type == model.ActionScript && a.Interpreter == "" {
		a.Interpreter = actions.DefaultInterpreter()
	}
	if a.Headers != nil {
		clean := map[string]string{}
		for k, v := range a.Headers {
			if k = strings.TrimSpace(k); k != "" {
				clean[k] = v
			}
		}
		a.Headers = clean
	}
	if a.TimeoutSeconds < 0 {
		a.TimeoutSeconds = 0
	}
	return actions.Validate(*a)
}

func (s *Server) normalizeTrigger(ctx context.Context, t *model.Trigger) error {
	t.Name = strings.TrimSpace(t.Name)
	if t.Name == "" {
		t.Name = "Trigger"
	}
	if t.NodeID <= 0 {
		return fmt.Errorf("a node is required")
	}
	n, err := s.Store.GetNode(ctx, t.NodeID)
	if err != nil {
		return fmt.Errorf("the node does not exist")
	}
	if t.CheckID != nil {
		found := false
		for _, c := range n.Checks {
			if c.ID == *t.CheckID {
				found = true
			}
		}
		if !found || *t.CheckID <= 0 {
			t.CheckID = nil
		}
	}
	valid := map[string]bool{}
	for _, c := range model.TriggerConditions {
		valid[c] = true
	}
	conds := make([]string, 0, len(t.On))
	seen := map[string]bool{}
	for _, c := range t.On {
		c = strings.TrimSpace(c)
		if !valid[c] {
			return fmt.Errorf("unknown condition %q", c)
		}
		if !seen[c] {
			seen[c] = true
			conds = append(conds, c)
		}
	}
	if len(conds) == 0 {
		return fmt.Errorf("pick at least one condition")
	}
	t.On = conds
	if t.CooldownMinutes < 0 {
		t.CooldownMinutes = 0
	}
	if t.LatencyOverMS < 0 {
		t.LatencyOverMS = 0
	}
	return s.normalizeAction(&t.Action)
}

func (s *Server) handleListTriggers(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListTriggers(r.Context(), queryInt64Ptr(r, "nodeId"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleSaveTrigger(w http.ResponseWriter, r *http.Request) {
	var t model.Trigger
	if err := decodeJSON(r, &t); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if r.Method == http.MethodPut {
		id, err := pathID(r, "id")
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		t.ID = id
	} else {
		t.ID = 0
	}
	if err := s.normalizeTrigger(r.Context(), &t); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, err := s.Store.SaveTrigger(r.Context(), t)
	if err != nil {
		s.fail(w, err)
		return
	}
	n, _ := s.Store.GetNode(r.Context(), saved.NodeID)
	s.configChanged(r.Context(), &n, fmt.Sprintf("Trigger saved: %s", saved.Name), fmt.Sprintf("On %s → %s action.", strings.Join(saved.On, ", "), saved.Action.Type))
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleDeleteTrigger(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	t, err := s.Store.GetTrigger(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Store.DeleteTrigger(r.Context(), id); err != nil {
		s.fail(w, err)
		return
	}
	n, _ := s.Store.GetNode(r.Context(), t.NodeID)
	s.configChanged(r.Context(), &n, "Trigger deleted: "+t.Name, "")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleRunTrigger(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := s.Engine.RunTrigger(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleTestAction(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action model.Action `json:"action"`
		NodeID *int64       `json:"nodeId"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.normalizeAction(&body.Action); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.Engine.TestAction(r.Context(), body.Action, body.NodeID))
}

// tokenlessEndpoint identifies a custom endpoint that anyone who can reach the
// port may call. The Settings > Automation tab warns about them.
type tokenlessEndpoint struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

func (s *Server) tokenlessEndpoints(ctx context.Context) []tokenlessEndpoint {
	out := []tokenlessEndpoint{}
	list, err := s.Store.ListEndpoints(ctx)
	if err != nil {
		return out
	}
	for _, e := range list {
		if strings.TrimSpace(e.Token) == "" {
			out = append(out, tokenlessEndpoint{ID: e.ID, Name: e.Name, Slug: e.Slug})
		}
	}
	return out
}

func (s *Server) handleAutomationMeta(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"tokenlessEndpoints": s.tokenlessEndpoints(r.Context()),
		"minTokenLength":     minEndpointToken,
		"conditions":         model.TriggerConditions,
		"interpreters":       actions.Interpreters(),
		"defaultInterpreter": actions.DefaultInterpreter(),
		"placeholders":       []string{"event", "node.id", "node.name", "node.host", "node.group", "node.tags", "check.id", "check.name", "check.type", "target", "status", "prev_status", "message", "error", "success", "latencyMs", "lossPct", "statusCode", "failures", "ts", "instance", "trigger.name", "body", "query.<name>", "method", "remote"},
	})
}

// ---- endpoints ----

// minEndpointToken is the shortest token a custom endpoint may be saved with.
const minEndpointToken = 8

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

func slugify(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ', r == '.', r == '/':
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func (s *Server) handleListEndpoints(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListEndpoints(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleSaveEndpoint(w http.ResponseWriter, r *http.Request) {
	var e model.Endpoint
	if err := decodeJSON(r, &e); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if r.Method == http.MethodPut {
		id, err := pathID(r, "id")
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		e.ID = id
	} else {
		e.ID = 0
	}
	e.Name = strings.TrimSpace(e.Name)
	if e.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if strings.TrimSpace(e.Slug) == "" {
		e.Slug = slugify(e.Name)
	} else {
		e.Slug = slugify(e.Slug)
	}
	if !slugPattern.MatchString(e.Slug) {
		writeError(w, http.StatusBadRequest, "the URL name may only contain lowercase letters, digits, - and _")
		return
	}
	e.Method = strings.ToUpper(strings.TrimSpace(e.Method))
	switch e.Method {
	case "", "ANY":
		e.Method = "ANY"
	case "GET", "POST", "PUT", "DELETE":
	default:
		writeError(w, http.StatusBadRequest, "method must be GET, POST, PUT, DELETE or ANY")
		return
	}
	e.Token = strings.TrimSpace(e.Token)
	if e.Token != "" {
		// A token and "allow calls without a token" are mutually exclusive; the
		// token wins so an acknowledgement cannot linger on a protected endpoint.
		e.AllowNoToken = false
		if len(e.Token) < minEndpointToken {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("the token must be at least %d characters", minEndpointToken))
			return
		}
	} else if !e.AllowNoToken {
		writeError(w, http.StatusBadRequest, "a token is required; generate one or explicitly allow calls without a token")
		return
	}
	if err := s.normalizeAction(&e.Action); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, err := s.Store.SaveEndpoint(r.Context(), e)
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.fail(w, err)
		return
	}
	s.configChanged(r.Context(), nil, "Endpoint saved: "+saved.Name, fmt.Sprintf("/hook/%s runs a %s action.", saved.Slug, saved.Action.Type))
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleDeleteEndpoint(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	e, err := s.Store.GetEndpoint(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Store.DeleteEndpoint(r.Context(), id); err != nil {
		s.fail(w, err)
		return
	}
	s.configChanged(r.Context(), nil, "Endpoint deleted: "+e.Name, "")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleRunEndpoint(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	e, err := s.Store.GetEndpoint(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.Engine.RunEndpoint(r.Context(), e, actions.Vars{"method": "MANUAL", "remote": "ui"}))
}

// handleHook serves /hook/{slug}: the public face of a custom endpoint.
func (s *Server) handleHook(w http.ResponseWriter, r *http.Request) {
	slug := strings.ToLower(strings.Trim(r.PathValue("slug"), "/"))
	e, err := s.Store.GetEndpointBySlug(r.Context(), slug)
	if err != nil || !e.Enabled {
		writeError(w, http.StatusNotFound, "no such endpoint")
		return
	}
	if e.Method != "ANY" && e.Method != r.Method {
		writeError(w, http.StatusMethodNotAllowed, "this endpoint accepts "+e.Method)
		return
	}
	// /hook/ is exempt from the LAN access password, so the endpoint's own token
	// is the only thing protecting it. An endpoint whose token went missing is
	// refused unless its owner explicitly allowed anonymous calls.
	if e.Token == "" && !e.AllowNoToken {
		writeError(w, http.StatusUnauthorized, "this endpoint has no token configured")
		return
	}
	if e.Token != "" {
		got := r.Header.Get("X-GWatch-Token")
		if got == "" {
			got = r.URL.Query().Get("token")
		}
		if got == "" {
			if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
				got = strings.TrimPrefix(auth, "Bearer ")
			}
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(e.Token)) != 1 {
			writeError(w, http.StatusUnauthorized, "invalid token")
			return
		}
	}
	vars := actions.Vars{"method": r.Method, "remote": r.RemoteAddr}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	vars["body"] = string(body)
	for k, v := range r.URL.Query() {
		if k != "token" && len(v) > 0 {
			vars["query."+k] = v[0]
		}
	}
	res := s.Engine.RunEndpoint(r.Context(), e, vars)
	status := http.StatusOK
	if !res.OK {
		status = http.StatusBadGateway
	}
	writeJSON(w, status, res)
}

// ---- saved charts ----

func (s *Server) handleListCharts(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListSavedCharts(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handlePutCharts(w http.ResponseWriter, r *http.Request) {
	var list []model.SavedChart
	if err := decodeJSON(r, &list); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	now := time.Now()
	for i := range list {
		list[i].Name = strings.TrimSpace(list[i].Name)
		if list[i].Name == "" {
			list[i].Name = fmt.Sprintf("Chart %d", i+1)
		}
		if list[i].ID == "" {
			list[i].ID = fmt.Sprintf("c%d%d", now.UnixNano()%1000000, i)
		}
		if len(list[i].Config) == 0 {
			list[i].Config = json.RawMessage("{}")
		}
		list[i].UpdatedAt = now
	}
	if err := s.Store.SaveSavedCharts(r.Context(), list); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// ---- status dots for the header ----

type statusDoc struct {
	Down         int      `json:"down"`
	Degraded     int      `json:"degraded"`
	Unknown      int      `json:"unknown"`
	Up           int      `json:"up"`
	Total        int      `json:"total"`
	CertWarnings int      `json:"certWarnings"`
	Maintenance  int      `json:"maintenance"`
	ServiceOK    bool     `json:"serviceOk"`
	ServiceIssue []string `json:"serviceIssues"`
	Attention    int      `json:"attention"`
	GeneratedAt  string   `json:"generatedAt"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	ov, err := s.Engine.Overview(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	hl := s.Engine.Health(r.Context())
	doc := statusDoc{Down: ov.Summary.Down, Degraded: ov.Summary.Degraded, Unknown: ov.Summary.Unknown, Up: ov.Summary.Up, Total: ov.Summary.Total, CertWarnings: len(ov.CertWarnings), Maintenance: ov.Summary.Maintenance, Attention: len(ov.Attention), GeneratedAt: ov.GeneratedAt.Format(time.RFC3339), ServiceIssue: []string{}}
	if !hl.ServiceRunning {
		doc.ServiceIssue = append(doc.ServiceIssue, "service stopped")
	}
	if !hl.SchedulerRunning {
		doc.ServiceIssue = append(doc.ServiceIssue, "scheduler stopped")
	}
	if hl.LastCheckAt != nil && hl.ChecksEnabled > 0 && time.Since(*hl.LastCheckAt) > 10*time.Minute {
		doc.ServiceIssue = append(doc.ServiceIssue, "no checks recently")
	}
	if hl.Retention.LastError != "" {
		doc.ServiceIssue = append(doc.ServiceIssue, "retention error")
	}
	if hl.Backup.LastBackupAt != nil && !hl.Backup.LastBackupOK {
		doc.ServiceIssue = append(doc.ServiceIssue, "backup failed")
	}
	if hl.LastAlertError != "" {
		doc.ServiceIssue = append(doc.ServiceIssue, "alert email failed")
	}
	doc.ServiceOK = len(doc.ServiceIssue) == 0
	writeJSON(w, http.StatusOK, doc)
}

// ---- network / remote access ----

// NetworkFunc reports how the server is currently bound.
type NetworkFunc func() model.NetworkInfo

func (s *Server) handleNetwork(w http.ResponseWriter, r *http.Request) {
	if s.Network == nil {
		writeJSON(w, http.StatusOK, model.NetworkInfo{LANURLs: []string{}})
		return
	}
	writeJSON(w, http.StatusOK, s.Network())
}

// LANAddresses lists the non-loopback IPv4 (and global IPv6) addresses of this machine.
func LANAddresses() []string {
	var out []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() {
				continue
			}
			if ip4 := ip.To4(); ip4 != nil {
				out = append(out, ip4.String())
			} else if ip.IsGlobalUnicast() {
				out = append(out, "["+ip.String()+"]")
			}
		}
	}
	return out
}

// ---- updates ----

// Updater performs release checks and applies updates.
type Updater struct {
	Client  *update.Client
	Version string
	Restart func() // asked to restart the process after a successful swap
	Log     interface{ Printf(string, ...any) }

	mu     sync.Mutex
	status model.UpdateStatus
}

func (u *Updater) exe() string {
	if p, err := update.Executable(); err == nil {
		return p
	}
	return ""
}

// Status returns a copy of the updater state.
func (u *Updater) Status() model.UpdateStatus {
	u.mu.Lock()
	defer u.mu.Unlock()
	st := u.status
	st.Executable = u.exe()
	st.CanApply = st.Executable != "" && update.DirWritable(st.Executable)
	return st
}

// Check queries GitHub for the latest release.
func (u *Updater) Check(ctx context.Context, repo string) (model.UpdateInfo, error) {
	info, err := u.Client.Check(ctx, repo, u.Version)
	if err != nil {
		info.Error = err.Error()
	}
	u.mu.Lock()
	cp := info
	u.status.Last = &cp
	u.mu.Unlock()
	return info, err
}

// Apply downloads the latest asset, swaps the executable and requests a restart.
func (u *Updater) Apply(ctx context.Context, repo string) (model.UpdateInfo, error) {
	u.mu.Lock()
	if u.status.Applying {
		u.mu.Unlock()
		return model.UpdateInfo{}, fmt.Errorf("an update is already being applied")
	}
	u.status.Applying = true
	u.status.LastError = ""
	u.mu.Unlock()
	done := func(err error) {
		u.mu.Lock()
		u.status.Applying = false
		if err != nil {
			u.status.LastError = err.Error()
		}
		u.mu.Unlock()
	}
	info, err := u.Check(ctx, repo)
	if err != nil {
		done(err)
		return info, err
	}
	if !info.UpdateAvailable {
		err := fmt.Errorf("already up to date (%s)", info.LatestVersion)
		done(err)
		return info, err
	}
	if info.AssetURL == "" {
		done(update.ErrNoAsset)
		return info, update.ErrNoAsset
	}
	exe := u.exe()
	if exe == "" {
		err := fmt.Errorf("cannot determine the executable path")
		done(err)
		return info, err
	}
	if !update.DirWritable(exe) {
		err := fmt.Errorf("the executable directory is not writable (%s); run the update as an administrator or update by re-running the installer", exe)
		done(err)
		return info, err
	}
	newPath, err := u.Client.Download(ctx, info, filepathDir(exe))
	if err != nil {
		done(err)
		return info, err
	}
	if err := update.Swap(exe, newPath); err != nil {
		os.Remove(newPath)
		done(err)
		return info, err
	}
	now := time.Now()
	u.mu.Lock()
	u.status.Applying = false
	u.status.Applied = true
	u.status.LastApplyAt = &now
	u.status.Restarting = u.Restart != nil
	u.mu.Unlock()
	if u.Log != nil {
		u.Log.Printf("update: installed %s (%s) at %s; restarting", info.LatestVersion, info.AssetName, exe)
	}
	if u.Restart != nil {
		go func() {
			time.Sleep(1500 * time.Millisecond)
			u.Restart()
		}()
	}
	return info, nil
}

func filepathDir(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[:i]
	}
	return "."
}

func (s *Server) updateRepo() string {
	repo := strings.TrimSpace(s.Engine.Settings().General.UpdateRepo)
	if repo == "" {
		repo = model.DefaultSettings().General.UpdateRepo
	}
	return repo
}

func (s *Server) handleUpdateStatus(w http.ResponseWriter, r *http.Request) {
	if s.Updater == nil {
		writeJSON(w, http.StatusOK, model.UpdateStatus{})
		return
	}
	st := s.Updater.Status()
	writeJSON(w, http.StatusOK, map[string]any{"status": st, "repo": s.updateRepo(), "version": s.Version})
}

func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if s.Updater == nil {
		writeError(w, http.StatusServiceUnavailable, "updates are not available in this build")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	info, err := s.Updater.Check(ctx, s.updateRepo())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "info": info})
		return
	}
	s.Engine.RecordEvent(model.Event{Type: model.EventUpdate, Title: "Checked for updates", Detail: fmt.Sprintf("Current %s, latest release %s%s.", info.CurrentVersion, info.LatestVersion, map[bool]string{true: " — update available", false: ""}[info.UpdateAvailable])})
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	if s.Updater == nil {
		writeError(w, http.StatusServiceUnavailable, "updates are not available in this build")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	info, err := s.Updater.Apply(ctx, s.updateRepo())
	if err != nil {
		s.Engine.RecordEvent(model.Event{Type: model.EventUpdate, Title: "Update failed", Detail: err.Error()})
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "info": info})
		return
	}
	s.Engine.RecordEvent(model.Event{Type: model.EventUpdate, Title: "Update installed: " + info.LatestVersion, Detail: fmt.Sprintf("Downloaded %s from %s. The service restarts to finish the update.", info.AssetName, info.Repo)})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "info": info, "restarting": s.Updater.Status().Restarting})
}

// ---- audit exports ----

func (s *Server) handleExportLogs(w http.ResponseWriter, r *http.Request) {
	lines := s.Log.Recent(queryInt(r, "limit", 1000))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "gwatch-"+time.Now().Format("20060102-150405")+".log"))
	for _, l := range lines {
		_, _ = io.WriteString(w, l+"\n")
	}
}

func (s *Server) eventFilterFromQuery(r *http.Request, def int) store.EventFilter {
	f := store.EventFilter{Limit: queryInt(r, "limit", def), BeforeID: int64(queryInt(r, "before", 0)), NodeID: queryInt64Ptr(r, "nodeId"), CheckID: queryInt64Ptr(r, "checkId"), Query: strings.TrimSpace(r.URL.Query().Get("q"))}
	counterparts := map[model.EventType]model.EventType{
		model.EventWarning:          model.EventWarningCleared,
		model.EventCertWarning:      model.EventCertWarningCleared,
		model.EventSilenced:         model.EventUnsilenced,
		model.EventMaintenanceBegan: model.EventMaintenanceEnded,
		model.EventDown:             model.EventRecovered,
		model.EventAlertSent:        model.EventAlertFailed,
		model.EventServiceStarted:   model.EventServiceStopped,
	}
	for _, t := range r.URL.Query()["type"] {
		for _, part := range strings.Split(t, ",") {
			if part = strings.TrimSpace(part); part != "" {
				et := model.EventType(part)
				f.Types = append(f.Types, et)
				if c, ok := counterparts[et]; ok && r.URL.Query().Get("exact") == "" {
					f.Types = append(f.Types, c)
				}
			}
		}
	}
	if since := r.URL.Query().Get("since"); since != "" {
		if t, err := parseTimeParam(since); err == nil {
			f.Since = &t
		}
	}
	if until := r.URL.Query().Get("until"); until != "" {
		if t, err := parseTimeParam(until); err == nil {
			f.Until = &t
		}
	}
	return f
}

func parseTimeParam(v string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, v, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("bad time %q", v)
}

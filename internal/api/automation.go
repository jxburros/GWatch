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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jxburros/GWatch/internal/actions"
	"github.com/jxburros/GWatch/internal/auth"
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
	// metric_over watches one named metric; without a name there is nothing
	// to compare, so the condition is refused rather than never firing.
	t.Metric = strings.TrimSpace(t.Metric)
	if seen["metric_over"] && t.Metric == "" {
		return fmt.Errorf("the metric_over condition needs the metric to watch, e.g. cpu or disk:/srv")
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
		writeDecodeError(w, err)
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
		writeDecodeError(w, err)
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
		"placeholders":       []string{"event", "node.id", "node.name", "node.host", "node.group", "node.tags", "check.id", "check.name", "check.type", "target", "status", "prev_status", "message", "error", "success", "latencyMs", "lossPct", "statusCode", "failures", "ts", "instance", "trigger.name", "metric", "metric.label", "metric.value", "metric.status", "metrics.<key>", "body", "query.<name>", "method", "remote"},
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

// endpointDoc is an endpoint as the API returns it. The token is the only
// thing guarding a /hook/ URL, so it is replaced by a flag for anyone who is
// not an administrator; hasToken still lets the UI show whether one is set.
type endpointDoc struct {
	model.Endpoint
	HasToken bool `json:"hasToken"`
}

func endpointDocs(list []model.Endpoint, redact bool) []endpointDoc {
	out := make([]endpointDoc, 0, len(list))
	for _, e := range list {
		d := endpointDoc{Endpoint: e, HasToken: e.Token != ""}
		if redact {
			d.Token = ""
		}
		out = append(out, d)
	}
	return out
}

func (s *Server) handleListEndpoints(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListEndpoints(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, endpointDocs(list, !auth.FromContext(r.Context()).IsAdmin()))
}

func (s *Server) handleSaveEndpoint(w http.ResponseWriter, r *http.Request) {
	var e model.Endpoint
	if err := decodeJSON(r, &e); err != nil {
		writeDecodeError(w, err)
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
	// An endpoint's token is a credential, and this route is reached with
	// nothing else, so every attempt that does not produce a working one is
	// charged to the same per-IP failure budget as a wrong password. That
	// includes a slug nobody recognises: the 404 is kept because it is what
	// makes a mistyped URL diagnosable, but paying for it stops the slug space
	// from being enumerated for free.
	ip := s.clientIP(r)
	if allowed, wait := s.failLimiter.Allow(ip); !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds()+0.999)))
		writeError(w, http.StatusTooManyRequests, "too many failed attempts; try again shortly")
		return
	}
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
			s.auditAuthFailure(r.Context(), "Endpoint token rejected",
				fmt.Sprintf("The wrong token was presented for /hook/%s from %s.", e.Slug, ip), ip)
			writeError(w, http.StatusUnauthorized, "invalid token")
			return
		}
	}
	// The caller got in, so give its budget back: an endpoint called on a
	// schedule must never talk itself into a 429.
	s.failLimiter.Reset(ip)
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
		writeDecodeError(w, err)
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

	// Prefs and Repo report the current update settings and the repository to
	// check. They are functions rather than values because settings change
	// while the service runs.
	Prefs func() model.UpdateSettings
	Repo  func() string

	mu        sync.Mutex
	status    model.UpdateStatus
	lastCheck time.Time

	// The newest hardware agent release, cached. GWatch looks this up so it
	// can tell you which machines are running an old agent; it does not act
	// on it, and cannot — an agent updates itself and is never told to by
	// GWatch (docs/HARDWARE.md#keeping-agents-up-to-date).
	agentMu      sync.Mutex
	agentLatest  model.AgentRelease
	agentFetched time.Time
}

// agentCacheFor is how long the newest-agent-release answer is reused. Agent
// releases are rare, and this is only used to decide whether to draw a badge,
// so asking GitHub more often than this would be noise for no gain.
//
// agentRetryFor is the same thing after a failed lookup: short enough that a
// blip clears by itself, long enough that a page someone keeps refreshing —
// or a Hardware tab left open — does not turn into a stream of requests to
// GitHub while it is down.
const (
	agentCacheFor = 6 * time.Hour
	agentRetryFor = 10 * time.Minute
)

// prefs returns the update settings, falling back to the defaults when no
// accessor is wired (tests, and any build that constructs an Updater bare).
func (u *Updater) prefs() model.UpdateSettings {
	if u.Prefs == nil {
		return model.DefaultSettings().Updates
	}
	p := u.Prefs()
	if p.CheckIntervalHours <= 0 {
		p.CheckIntervalHours = model.DefaultSettings().Updates.CheckIntervalHours
	}
	return p
}

func (u *Updater) exe() string {
	if p, err := update.Executable(); err == nil {
		return p
	}
	return ""
}

// Status returns a copy of the updater state.
func (u *Updater) Status() model.UpdateStatus {
	p := u.prefs()
	u.mu.Lock()
	defer u.mu.Unlock()
	st := u.status
	st.Executable = u.exe()
	st.CanApply = st.Executable != "" && update.DirWritable(st.Executable)
	st.AutoCheck = p.CheckAutomatically
	st.PromptOnOpen = p.CheckAutomatically && p.PromptOnOpen
	if !u.lastCheck.IsZero() {
		last := u.lastCheck
		st.LastCheckAt = &last
		if p.CheckAutomatically {
			next := last.Add(time.Duration(p.CheckIntervalHours) * time.Hour)
			st.NextCheckAt = &next
		}
	}
	return st
}

// Check queries GitHub for the newest release this build could move to.
func (u *Updater) Check(ctx context.Context, repo string) (model.UpdateInfo, error) {
	info, err := u.Client.Check(ctx, repo, u.Version, u.prefs().IncludePrerelease)
	if err != nil {
		info.Error = err.Error()
	}
	u.mu.Lock()
	cp := info
	u.status.Last = &cp
	u.lastCheck = time.Now()
	u.mu.Unlock()
	return info, err
}

// AgentLatest reports the newest release of the hardware agent, cached.
//
// This is read-only interest: it exists so the interface can mark machines
// whose agent is behind. Agents take their own updates, verified against keys
// pinned into the agent binary, and nothing here can make one install
// anything — deliberately, since a GWatch that had been got into must not
// become a way onto every machine that reports to it.
func (u *Updater) AgentLatest(ctx context.Context) model.AgentRelease {
	u.agentMu.Lock()
	if !u.agentFetched.IsZero() {
		ttl := agentCacheFor
		if u.agentLatest.Version == "" {
			ttl = agentRetryFor
		}
		if time.Since(u.agentFetched) < ttl {
			cached := u.agentLatest
			u.agentMu.Unlock()
			return cached
		}
	}
	u.agentMu.Unlock()

	repo := model.DefaultSettings().General.UpdateRepo
	if u.Repo != nil {
		if r := strings.TrimSpace(u.Repo()); r != "" {
			repo = r
		}
	}
	out := model.AgentRelease{CheckedAt: time.Now()}
	// The agent's releases are its own: its tags, its assets. Asking as the
	// agent rather than as GWatch is what stops a GWatch release being read as
	// an agent one, and the two version independently.
	client := u.Client
	if client == nil {
		client = &update.Client{}
	}
	rels, err := client.ForAgent().Releases(ctx, repo, "")
	if err != nil {
		out.Error = err.Error()
	} else {
		for _, r := range rels {
			if r.Prerelease {
				continue
			}
			out.Version, out.URL = r.Version, r.URL
			break
		}
	}

	u.agentMu.Lock()
	// A failed lookup must not evict a good answer: a machine is not "up to
	// date" because GitHub was unreachable for a minute.
	if out.Version != "" || u.agentLatest.Version == "" {
		u.agentLatest, u.agentFetched = out, time.Now()
	} else {
		out = u.agentLatest
	}
	u.agentMu.Unlock()
	return out
}

// Releases lists what this build could move to, newest first. Pre-releases are
// included and flagged: the interface decides whether to show them, and says
// what they are when it does.
func (u *Updater) Releases(ctx context.Context, repo string) ([]model.Release, error) {
	return u.Client.Releases(ctx, repo, u.Version)
}

// Run checks for updates in the background: once shortly after the service
// starts, then every CheckIntervalHours. It returns when ctx is cancelled.
// Nothing is contacted while CheckAutomatically is off, and turning it back on
// is picked up on the next tick rather than needing a restart.
func (u *Updater) Run(ctx context.Context) {
	// A moment's grace so the first check does not compete with everything
	// else a starting service is doing.
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
		u.checkIfDue(ctx)
	}
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			u.checkIfDue(ctx)
		}
	}
}

// checkIfDue runs a background check when automatic checks are on and the
// interval has elapsed. A failure is kept in the status rather than retried:
// the next tick is the retry.
func (u *Updater) checkIfDue(ctx context.Context) {
	p := u.prefs()
	if !p.CheckAutomatically {
		return
	}
	u.mu.Lock()
	last := u.lastCheck
	u.mu.Unlock()
	if !last.IsZero() && time.Since(last) < time.Duration(p.CheckIntervalHours)*time.Hour {
		return
	}
	repo := "jxburros/GWatch"
	if u.Repo != nil {
		repo = u.Repo()
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	info, err := u.Check(ctx, repo)
	if err != nil {
		if u.Log != nil {
			u.Log.Printf("update: check failed: %v", err)
		}
		return
	}
	if info.UpdateAvailable && u.Log != nil {
		u.Log.Printf("update: %s is available (running %s)", info.LatestVersion, info.CurrentVersion)
	}
}

// target resolves what Apply should install. It records the check it performs
// in the status, so asking to install also refreshes what the interface shows.
func (u *Updater) target(ctx context.Context, repo, version string) (model.UpdateInfo, error) {
	version = strings.TrimSpace(version)
	if version == "" {
		info, err := u.Check(ctx, repo)
		if err != nil {
			return info, err
		}
		if !info.UpdateAvailable {
			return info, fmt.Errorf("already up to date (%s)", info.LatestVersion)
		}
		return info, nil
	}
	rels, err := u.Releases(ctx, repo)
	if err != nil {
		return model.UpdateInfo{Repo: repo, CurrentVersion: u.Version, CheckedAt: time.Now()}, err
	}
	rel := update.Find(rels, version)
	if rel == nil {
		return model.UpdateInfo{Repo: repo, CurrentVersion: u.Version, CheckedAt: time.Now()},
			fmt.Errorf("no release %s was published for %s", version, repo)
	}
	if rel.Running {
		return model.UpdateInfo{Repo: repo, CurrentVersion: u.Version, CheckedAt: time.Now()},
			fmt.Errorf("%s is the version already running", rel.Version)
	}
	if !rel.Newer {
		// Swapping in an older executable is not an update: the database has
		// already been migrated by the running version and an older build may
		// not understand it. Downloading the release by hand stays possible.
		return model.UpdateInfo{Repo: repo, CurrentVersion: u.Version, CheckedAt: time.Now()},
			fmt.Errorf("%s is older than the running version (%s); GWatch does not install an earlier version over a later one", rel.Version, u.Version)
	}
	if !rel.Installable {
		return model.UpdateInfo{Repo: repo, CurrentVersion: u.Version, CheckedAt: time.Now()}, update.ErrNoAsset
	}
	info := model.UpdateInfo{
		Repo: repo, CurrentVersion: u.Version, CheckedAt: time.Now(), CurrentIsDev: update.IsDev(u.Version),
		LatestVersion: rel.Version, ReleaseURL: rel.URL, ReleaseNotes: rel.Notes, PublishedAt: rel.PublishedAt,
		AssetName: rel.AssetName, AssetURL: rel.AssetURL, AssetSize: rel.AssetSize,
		Prerelease: rel.Prerelease, UpdateAvailable: true,
	}
	u.mu.Lock()
	cp := info
	u.status.Last = &cp
	u.lastCheck = time.Now()
	u.mu.Unlock()
	return info, nil
}

// Apply downloads a release asset, swaps the executable and requests a
// restart. An empty version means the newest release this build could move to,
// honouring the pre-release setting; a version names one release to install,
// which may be an older one than the newest available or a pre-release the
// user has chosen to accept.
//
// The version is resolved against the release list fetched here, never used to
// build a download URL: what is downloaded is always an asset GitHub itself
// lists for that release, and it is verified against the pinned signing keys
// like any other.
func (u *Updater) Apply(ctx context.Context, repo, version string) (model.UpdateInfo, error) {
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
	info, err := u.target(ctx, repo, version)
	if err != nil {
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
	s.recordEvent(r.Context(), model.Event{Type: model.EventUpdate, Title: "Checked for updates", Detail: fmt.Sprintf("Current %s, latest release %s%s.", info.CurrentVersion, info.LatestVersion, map[bool]string{true: " — update available", false: ""}[info.UpdateAvailable])})
	writeJSON(w, http.StatusOK, info)
}

// handleUpdateReleases lists the releases this build could move to. The
// pre-release ones are listed too, flagged as such: what the interface offers
// is decided there, with the warning next to it.
func (s *Server) handleUpdateReleases(w http.ResponseWriter, r *http.Request) {
	if s.Updater == nil {
		writeError(w, http.StatusServiceUnavailable, "updates are not available in this build")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	rels, err := s.Updater.Releases(ctx, s.updateRepo())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "releases": []model.Release{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"releases": rels, "repo": s.updateRepo(), "version": s.Version})
}

func (s *Server) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	if s.Updater == nil {
		writeError(w, http.StatusServiceUnavailable, "updates are not available in this build")
		return
	}
	// An empty version installs the newest release on offer; naming one
	// installs that release instead, which is how a user takes an update that
	// is not the most recent, or a pre-release.
	var body struct {
		Version string `json:"version"`
	}
	if r.ContentLength > 0 {
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeError(w, err)
			return
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	info, err := s.Updater.Apply(ctx, s.updateRepo(), body.Version)
	if err != nil {
		s.recordEvent(r.Context(), model.Event{Type: model.EventUpdate, Title: "Update failed", Detail: err.Error()})
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "info": info})
		return
	}
	s.recordEvent(r.Context(), model.Event{Type: model.EventUpdate, Title: "Update installed: " + info.LatestVersion, Detail: fmt.Sprintf("Downloaded %s from %s. The service restarts to finish the update.", info.AssetName, info.Repo)})
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
		model.EventRuleFired:        model.EventRuleCleared,
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

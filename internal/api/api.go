// Package api serves the localhost-only JSON API and the embedded web UI.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jxburros/GWatch/internal/auth"
	"github.com/jxburros/GWatch/internal/backup"
	"github.com/jxburros/GWatch/internal/checks"
	"github.com/jxburros/GWatch/internal/discovery"
	"github.com/jxburros/GWatch/internal/engine"
	"github.com/jxburros/GWatch/internal/logging"
	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
)

// Server holds the dependencies of the HTTP handlers.
type Server struct {
	Engine    *engine.Engine
	Store     *store.Store
	Log       *logging.Logger
	Web       fs.FS
	BackupDir string
	Version   string
	// Updater performs GitHub release checks and self-updates (optional).
	Updater *Updater
	// Network reports how the server is bound (optional).
	Network NetworkFunc

	// failLimiter counts failed credential attempts per client IP; apiLimiter
	// is the general ceiling on API-key traffic from off this machine. Both are
	// created by Handler().
	failLimiter *auth.Limiter
	apiLimiter  *auth.Limiter

	// discoveryJobs holds the subnet sweep in flight and the last one that
	// finished. It lives here rather than in the store because a sweep is a
	// question about the network as it is right now: an answer that survived a
	// restart would be an answer about a network that has moved on.
	discoveryJobs *discovery.Registry
	discoveryOnce sync.Once

	// routes records every pattern Handler() registered, so the authorization
	// tests can prove the router and the policy table describe the same surface.
	routes []routeSpec

	// remoteAddrOverride replaces r.RemoteAddr when set. It exists so tests can
	// exercise non-loopback behaviour over a loopback httptest connection, and
	// is never set outside tests.
	remoteAddrOverride func(*http.Request) string
}

// Handler builds the router.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.routes = nil
	if s.failLimiter == nil {
		s.failLimiter = auth.NewLimiter(failureLimit, failureWindow)
	}
	if s.apiLimiter == nil {
		s.apiLimiter = auth.NewLimiter(remoteRequestLimit, remoteRequestWindow)
	}
	s.discovery()

	s.route(mux, "GET /api/health", s.handleHealth)
	s.route(mux, "GET /api/status", s.handleStatus)
	s.route(mux, "GET /api/network", s.handleNetwork)
	s.route(mux, "GET /api/version", s.handleVersion)
	s.route(mux, "GET /api/overview", s.handleOverview)
	s.route(mux, "GET /api/wallboard", s.handleWallboard)
	s.route(mux, "GET /api/stream", s.handleStream)

	s.route(mux, "GET /api/nodes", s.handleListNodes)
	s.route(mux, "POST /api/nodes", s.handleCreateNode)
	s.route(mux, "GET /api/nodes/{id}", s.handleGetNode)
	s.route(mux, "PUT /api/nodes/{id}", s.handleUpdateNode)
	s.route(mux, "DELETE /api/nodes/{id}", s.handleDeleteNode)
	s.route(mux, "POST /api/nodes/{id}/enable", s.handleEnableNode)
	s.route(mux, "POST /api/nodes/{id}/duplicate", s.handleDuplicateNode)
	s.route(mux, "POST /api/nodes/{id}/run", s.handleRunNode)
	s.route(mux, "POST /api/nodes/{id}/silence", s.handleSilenceNode)
	s.route(mux, "GET /api/templates", s.handleTemplates)
	s.route(mux, "GET /api/groups", s.handleGroups)

	s.route(mux, "GET /api/discovery", s.handleLatestDiscovery)
	s.route(mux, "POST /api/discovery", s.handleStartDiscovery)
	s.route(mux, "GET /api/discovery/{id}", s.handleGetDiscovery)
	s.route(mux, "POST /api/discovery/{id}/cancel", s.handleCancelDiscovery)
	s.route(mux, "POST /api/discovery/{id}/add", s.handleAddFromDiscovery)

	s.route(mux, "POST /api/checks/test", s.handleTestCheck)
	s.route(mux, "POST /api/checks/{id}/run", s.handleRunCheck)
	s.route(mux, "POST /api/checks/{id}/enable", s.handleEnableCheck)
	s.route(mux, "POST /api/checks/{id}/silence", s.handleSilenceCheck)
	s.route(mux, "GET /api/checks/{id}/results", s.handleCheckResults)
	s.route(mux, "GET /api/checks/{id}/state", s.handleCheckState)

	s.route(mux, "GET /api/history", s.handleHistory)
	s.route(mux, "GET /api/history/multi", s.handleHistoryMulti)

	s.route(mux, "GET /api/events", s.handleEvents)
	s.route(mux, "POST /api/events/note", s.handleNote)

	s.route(mux, "GET /api/maintenance", s.handleListMaintenance)
	s.route(mux, "POST /api/maintenance", s.handleSaveMaintenance)
	s.route(mux, "PUT /api/maintenance/{id}", s.handleSaveMaintenance)
	s.route(mux, "DELETE /api/maintenance/{id}", s.handleDeleteMaintenance)

	s.route(mux, "GET /api/dashboards", s.handleListDashboards)
	s.route(mux, "POST /api/dashboards", s.handleSaveDashboard)
	s.route(mux, "GET /api/dashboards/{id}", s.handleGetDashboard)
	s.route(mux, "PUT /api/dashboards/{id}", s.handleSaveDashboard)
	s.route(mux, "DELETE /api/dashboards/{id}", s.handleDeleteDashboard)

	s.route(mux, "GET /api/wallboards", s.handleListWallboards)
	s.route(mux, "POST /api/wallboards", s.handleSaveWallboard)
	s.route(mux, "GET /api/wallboards/{id}", s.handleGetWallboard)
	s.route(mux, "PUT /api/wallboards/{id}", s.handleSaveWallboard)
	s.route(mux, "DELETE /api/wallboards/{id}", s.handleDeleteWallboard)
	s.route(mux, "POST /api/wallboards/{id}/share", s.handleShareWallboard)
	// The projected view: reachable with a shared board's token and nothing
	// else. See handleWallboardView.
	s.route(mux, "GET /api/wallboards/{id}/view", s.handleWallboardView)

	s.route(mux, "GET /api/settings", s.handleGetSettings)
	s.route(mux, "PUT /api/settings", s.handlePutSettings)
	s.route(mux, "POST /api/settings/test-email", s.handleTestEmail)
	s.route(mux, "GET /api/retention/status", s.handleRetentionStatus)
	s.route(mux, "POST /api/retention/run", s.handleRetentionRun)

	s.route(mux, "GET /api/backups", s.handleListBackups)
	s.route(mux, "POST /api/backups", s.handleCreateBackup)
	s.route(mux, "GET /api/backups/{name}/download", s.handleDownloadBackup)
	s.route(mux, "DELETE /api/backups/{name}", s.handleDeleteBackup)
	s.route(mux, "POST /api/backups/restore", s.handleRestoreUpload)
	s.route(mux, "POST /api/backups/restore-existing", s.handleRestoreExisting)

	s.route(mux, "GET /api/export/history.csv", s.handleExportHistory)
	s.route(mux, "GET /api/export/results.csv", s.handleExportResults)
	s.route(mux, "GET /api/export/events.csv", s.handleExportEvents)
	s.route(mux, "GET /api/export/config.json", s.handleExportConfig)

	s.route(mux, "GET /api/export/logs.txt", s.handleExportLogs)

	s.route(mux, "GET /api/triggers", s.handleListTriggers)
	s.route(mux, "POST /api/triggers", s.handleSaveTrigger)
	s.route(mux, "PUT /api/triggers/{id}", s.handleSaveTrigger)
	s.route(mux, "DELETE /api/triggers/{id}", s.handleDeleteTrigger)
	s.route(mux, "POST /api/triggers/{id}/run", s.handleRunTrigger)
	s.route(mux, "POST /api/actions/test", s.handleTestAction)
	s.route(mux, "GET /api/automation/meta", s.handleAutomationMeta)

	s.route(mux, "GET /api/endpoints", s.handleListEndpoints)
	s.route(mux, "POST /api/endpoints", s.handleSaveEndpoint)
	s.route(mux, "PUT /api/endpoints/{id}", s.handleSaveEndpoint)
	s.route(mux, "DELETE /api/endpoints/{id}", s.handleDeleteEndpoint)
	s.route(mux, "POST /api/endpoints/{id}/run", s.handleRunEndpoint)
	s.route(mux, "/hook/{slug}", s.handleHook)
	s.route(mux, "/hook/{slug}/{rest...}", s.handleHook)

	s.route(mux, "GET /api/hosts", s.handleListHosts)
	s.route(mux, "GET /api/hosts/{key}", s.handleGetHost)
	s.route(mux, "GET /api/hosts/{key}/history", s.handleHostHistory)

	s.route(mux, "GET /api/agents", s.handleListAgents)
	s.route(mux, "POST /api/agents", s.handleCreateAgent)
	s.route(mux, "PUT /api/agents/{id}", s.handleUpdateAgent)
	s.route(mux, "DELETE /api/agents/{id}", s.handleRevokeAgent)

	s.route(mux, "GET /api/agents/pairings", s.handleListPairings)
	s.route(mux, "POST /api/agents/pairings", s.handleCreatePairing)
	s.route(mux, "DELETE /api/agents/pairings/{id}", s.handleRevokePairing)
	// Redeeming a pairing code is the one agent route that has to be reachable
	// with no credential at all: a credential is what it hands out. See
	// handlePair for what stands in for one.
	s.route(mux, "POST /api/agents/pair", s.handlePair)

	// Agents authenticate with their own token and are handled outside the
	// authorization table; see handleIngestMetrics for why.
	s.route(mux, "/ingest/metrics", s.handleIngestMetrics)

	s.route(mux, "GET /api/charts", s.handleListCharts)
	s.route(mux, "PUT /api/charts", s.handlePutCharts)

	s.route(mux, "GET /api/update/status", s.handleUpdateStatus)
	s.route(mux, "POST /api/update/check", s.handleUpdateCheck)
	s.route(mux, "GET /api/update/releases", s.handleUpdateReleases)
	s.route(mux, "POST /api/update/apply", s.handleUpdateApply)

	s.route(mux, "GET /api/logs", s.handleLogs)

	s.route(mux, "GET /api/me", s.handleMe)
	s.route(mux, "GET /api/auth/setup", s.handleAuthSetup)
	s.route(mux, "POST /api/auth/login", s.handleLogin)
	s.route(mux, "POST /api/auth/logout", s.handleLogout)
	s.route(mux, "POST /api/auth/change-password", s.handleChangePassword)

	s.route(mux, "GET /api/users", s.handleListUsers)
	s.route(mux, "POST /api/users", s.handleCreateUser)
	s.route(mux, "PUT /api/users/{id}", s.handleUpdateUser)
	s.route(mux, "DELETE /api/users/{id}", s.handleDeleteUser)

	s.route(mux, "GET /api/apikeys", s.handleListAPIKeys)
	s.route(mux, "POST /api/apikeys", s.handleCreateAPIKey)
	s.route(mux, "DELETE /api/apikeys/{id}", s.handleRevokeAPIKey)

	s.route(mux, "/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "unknown API endpoint")
	})

	if s.Web != nil {
		mux.Handle("/", s.staticHandler())
	}
	return securityHeaders(versionAlias(noCache(s.accessControl(mux))))
}

func isLoopbackRemote(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// contentSecurityPolicy is the policy every response carries. GWatch serves
// its own bundle and talks to nothing but itself, so each source list is
// 'self' and the few exceptions are named one at a time:
//
//   - img-src also allows data: for the one inline SVG in app.css (the select
//     arrow) and blob: for a chart exported as a PNG.
//   - style-src is plain 'self': the markup carries no style attributes (the
//     sidebar's stagger index moved into app.css for exactly this reason),
//     and the interface sets styles through the CSSOM, which the policy does
//     not govern.
//   - script-src needs no hash or nonce: what used to be inline in index.html
//     now lives in boot.js and entry.js.
//
// frame-ancestors is filled in per request by securityHeaders.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self'; " +
	"img-src 'self' data: blob:; " +
	"font-src 'self'; " +
	"connect-src 'self'; " +
	"base-uri 'none'; " +
	"object-src 'none'; " +
	"form-action 'self'; " +
	"frame-ancestors "

// securityHeaders puts the response headers a browser needs in order to hold
// GWatch to its own origin on every response, static assets included.
//
// The wallboard is the one page allowed into someone else's frame. It is
// read-only, it is reached with a board's own share token rather than with
// whatever credential the viewer happens to hold, and putting one in a Home
// Assistant dashboard is a thing people actually do. Framing it therefore
// costs nothing that clickjacking could take. Every other page — the
// application, where a click does change something — refuses to be framed at
// all, and says so twice: X-Frame-Options for browsers that predate
// frame-ancestors, and frame-ancestors for the rest.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		if isWallboardPage(r.URL.Path) {
			h.Set("Content-Security-Policy", contentSecurityPolicy+"*")
		} else {
			h.Set("Content-Security-Policy", contentSecurityPolicy+"'none'")
			h.Set("X-Frame-Options", "DENY")
		}
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// isWallboardPage reports whether a path addresses the projected wallboard.
// It recognises the same spellings staticHandler does, and is applied to the
// path as it arrived, before that rewrite.
func isWallboardPage(p string) bool {
	p = path.Clean(p)
	return p == "/wall" || p == "/wall.html"
}

func noCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) staticHandler() http.Handler {
	files := http.FS(s.Web)
	fileServer := http.FileServer(files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := path.Clean(r.URL.Path)
		if p == "/" || p == "/index.html" {
			w.Header().Set("Cache-Control", "no-cache")
			r.URL.Path = "/"
			fileServer.ServeHTTP(w, r)
			return
		}
		// /wall is the address a display is pointed at. It is its own page
		// rather than the application shell: the shell would send a browser
		// that cannot sign in to the sign-in screen, which is exactly what a
		// screen with no keyboard cannot do anything about.
		if p == "/wall" || p == "/wall/" {
			w.Header().Set("Cache-Control", "no-cache")
			r.URL.Path = "/wall.html"
			fileServer.ServeHTTP(w, r)
			return
		}
		if f, err := s.Web.Open(strings.TrimPrefix(p, "/")); err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		// Unknown path: serve the app shell (routes are hash based).
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, backup.ErrBadPassword):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		s.Log.Errorf("api: %v", err)
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// decodeError is a body that could not be read, carrying the status it should
// be answered with. A malformed document is a 400 as it always was; a body in
// some other format entirely is a 415, and only the error knows which of the
// two happened. Handlers pass it to writeDecodeError rather than deciding.
type decodeError struct {
	status int
	msg    string
}

func (e *decodeError) Error() string { return e.msg }

// writeDecodeError answers a request whose body could not be read.
func writeDecodeError(w http.ResponseWriter, err error) {
	var de *decodeError
	if errors.As(err, &de) {
		writeError(w, de.status, de.msg)
		return
	}
	writeError(w, http.StatusBadRequest, err.Error())
}

// requireJSONBody refuses a body that is not announced as JSON.
//
// Without this, a form post from another website — which a browser will send
// cross-origin with no preflight, because text/plain, form-urlencoded and
// multipart are "simple" content types — is indistinguishable from the UI's
// own fetch() once it reaches a handler. Demanding a JSON content type puts
// every write behind a preflight the other site cannot pass. An empty body
// with no content type is still allowed: a handler that treats a missing body
// as "nothing to change" is not being asked to parse anything.
func requireJSONBody(r *http.Request) error {
	ct := strings.TrimSpace(r.Header.Get("Content-Type"))
	if ct == "" {
		if r.ContentLength == 0 {
			return nil
		}
		return &decodeError{http.StatusUnsupportedMediaType, "this endpoint needs a Content-Type of application/json"}
	}
	mediaType, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return &decodeError{http.StatusUnsupportedMediaType, "unreadable Content-Type; this endpoint needs application/json"}
	}
	mediaType = strings.ToLower(mediaType)
	if mediaType == "application/json" || strings.HasSuffix(mediaType, "+json") {
		return nil
	}
	return &decodeError{http.StatusUnsupportedMediaType, fmt.Sprintf("this endpoint needs a Content-Type of application/json, not %s", mediaType)}
}

func decodeJSON(r *http.Request, v any) error {
	if err := requireJSONBody(r); err != nil {
		return err
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 4<<20))
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}

// decodeNode decodes a node body. "enabled" defaults to true for the node
// and for every check when the field is omitted (JSON would otherwise make
// them false).
func decodeNode(r *http.Request) (model.Node, error) {
	// This one reads the body itself rather than going through decodeJSON, so
	// it has to insist on the content type itself too.
	if err := requireJSONBody(r); err != nil {
		return model.Node{}, err
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		return model.Node{}, fmt.Errorf("invalid JSON body: %w", err)
	}
	var n model.Node
	if err := json.Unmarshal(raw, &n); err != nil {
		return n, fmt.Errorf("invalid JSON body: %w", err)
	}
	var probe struct {
		Enabled *bool `json:"enabled"`
		Checks  []struct {
			Enabled *bool `json:"enabled"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(raw, &probe); err == nil {
		if probe.Enabled == nil {
			n.Enabled = true
		}
		for i := range probe.Checks {
			if i < len(n.Checks) && probe.Checks[i].Enabled == nil {
				n.Checks[i].Enabled = true
			}
		}
	}
	return n, nil
}

func pathID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid id")
	}
	return id, nil
}

func queryInt(r *http.Request, name string, def int) int {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func queryInt64Ptr(r *http.Request, name string) *int64 {
	v := r.URL.Query().Get(name)
	if v == "" {
		return nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return nil
	}
	return &n
}

// ---- health / overview ----

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	h := s.Engine.Health(r.Context())
	if !auth.FromContext(r.Context()).Authenticated() {
		// This route is public so that an uptime probe can reach it, so a
		// caller with no identity gets liveness only. The full document names
		// the database path, the data directory, recent internal errors and
		// the backup state — none of it anyone's business before signing in.
		writeJSON(w, http.StatusOK, map[string]any{
			"serviceRunning":   h.ServiceRunning,
			"schedulerRunning": h.SchedulerRunning,
			"now":              h.Now,
		})
		return
	}
	writeJSON(w, http.StatusOK, h)
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	doc := map[string]any{"version": s.Version, "apiVersion": APIVersion}
	if auth.FromContext(r.Context()).Authenticated() {
		doc["platform"] = s.Engine.Health(r.Context()).Platform
	}
	writeJSON(w, http.StatusOK, doc)
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	ov, err := s.Engine.Overview(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ov)
}

type wallboardDoc struct {
	engine.Overview
	Health model.Health          `json:"health"`
	Trends []model.HistorySeries `json:"trends"`
}

func (s *Server) handleWallboard(w http.ResponseWriter, r *http.Request) {
	ov, err := s.Engine.Overview(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	doc := wallboardDoc{Overview: ov, Health: s.Engine.Health(r.Context()), Trends: []model.HistorySeries{}}
	rng, _ := store.ParseRange("24h")
	for _, c := range autoChecks(ov, 6) {
		series, err := s.Store.History(r.Context(), c.check, c.node.Name, rng, time.Now())
		if err == nil {
			doc.Trends = append(doc.Trends, series)
		}
	}
	writeJSON(w, http.StatusOK, doc)
}

type autoCandidate struct {
	check model.Check
	node  model.Node
	score int
}

// autoChecks picks up to limit checks worth charting when the user has not
// chosen any: critical/high importance nodes first, then ping and HTTP checks.
func autoChecks(ov engine.Overview, limit int) []autoCandidate {
	var cands []autoCandidate
	for _, nv := range ov.Nodes {
		if !nv.Node.Enabled {
			continue
		}
		for _, cv := range nv.Checks {
			if !cv.Check.Enabled {
				continue
			}
			score := 0
			switch nv.Node.Importance {
			case model.ImportanceCritical:
				score += 40
			case model.ImportanceHigh:
				score += 20
			}
			switch cv.Check.Type {
			case model.CheckPing:
				score += 10
			case model.CheckHTTP:
				score += 8
			case model.CheckTCP, model.CheckDNS:
				score += 4
			}
			cands = append(cands, autoCandidate{cv.Check, nv.Node, score})
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score > cands[j].score
		}
		return strings.ToLower(cands[i].node.Name) < strings.ToLower(cands[j].node.Name)
	})
	if len(cands) > limit {
		cands = cands[:limit]
	}
	return cands
}

// ---- nodes ----

type nodeDoc struct {
	model.Node
	Status        model.Status               `json:"status"`
	StateByCheck  map[int64]model.CheckState `json:"stateByCheck"`
	LastResults   map[int64]model.Result     `json:"lastResults,omitempty"`
	InMaintenance bool                       `json:"inMaintenance"`
}

func (s *Server) decorateNode(n model.Node, states map[int64]model.CheckState, last map[int64]model.Result) nodeDoc {
	doc := nodeDoc{Node: n, StateByCheck: map[int64]model.CheckState{}}
	for _, c := range n.Checks {
		if st, ok := states[c.ID]; ok {
			doc.StateByCheck[c.ID] = st
		} else {
			doc.StateByCheck[c.ID] = model.CheckState{CheckID: c.ID, Status: model.StatusUnknown}
		}
	}
	if last != nil {
		doc.LastResults = map[int64]model.Result{}
		for _, c := range n.Checks {
			if r, ok := last[c.ID]; ok {
				doc.LastResults[c.ID] = r
			}
		}
	}
	doc.Status, doc.InMaintenance = s.Engine.NodeStatus(n)
	return doc
}

func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.Store.ListNodes(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	states := s.Engine.States()
	out := make([]nodeDoc, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, s.decorateNode(n, states, nil))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetNode(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	n, err := s.Store.GetNode(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	last, err := s.Store.LastResults(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.decorateNode(n, s.Engine.States(), last))
}

// normalizeNode validates and fills defaults on a node and its checks.
func (s *Server) normalizeNode(n *model.Node) error {
	settings := s.Engine.Settings()
	n.Name = strings.TrimSpace(n.Name)
	n.Host = strings.TrimSpace(n.Host)
	n.Group = strings.TrimSpace(n.Group)
	if n.Name == "" {
		return fmt.Errorf("name is required")
	}
	if n.Importance == "" {
		n.Importance = model.ImportanceNormal
	}
	switch n.Importance {
	case model.ImportanceLow, model.ImportanceNormal, model.ImportanceHigh, model.ImportanceCritical:
	default:
		return fmt.Errorf("invalid importance %q", n.Importance)
	}
	tags := make([]string, 0, len(n.Tags))
	seen := map[string]bool{}
	for _, t := range n.Tags {
		t = strings.TrimSpace(t)
		if t != "" && !seen[strings.ToLower(t)] {
			seen[strings.ToLower(t)] = true
			tags = append(tags, t)
		}
	}
	n.Tags = tags
	if n.DependsOnNode != nil && (*n.DependsOnNode <= 0 || *n.DependsOnNode == n.ID) {
		n.DependsOnNode = nil
	}
	if n.Checks == nil {
		n.Checks = []model.Check{}
	}
	for i := range n.Checks {
		c := &n.Checks[i]
		c.Name = strings.TrimSpace(c.Name)
		if c.Name == "" {
			c.Name = c.Type.Label()
		}
		if !c.Type.Valid() {
			return fmt.Errorf("check %q: unsupported type %q", c.Name, c.Type)
		}
		if c.IntervalSeconds <= 0 {
			c.IntervalSeconds = settings.General.DefaultIntervalSecs
		}
		if c.IntervalSeconds < settings.General.MinIntervalSecs {
			return fmt.Errorf("check %q: interval must be at least %d seconds (see Settings › General to change the minimum)", c.Name, settings.General.MinIntervalSecs)
		}
		if c.TimeoutSeconds <= 0 {
			c.TimeoutSeconds = settings.General.DefaultTimeoutSecs
		}
		if c.Retries < 0 {
			c.Retries = 0
		}
		if c.Type == model.CheckSystem {
			if c.Config.HostSource == "" {
				c.Config.HostSource = model.HostSourceLocal
			}
			// A hardware check with no thresholds would watch a machine and
			// never say anything, so a new one starts with the defaults
			// written into it rather than applied invisibly at run time.
			if !c.Config.HasSystemThresholds() {
				d := model.SystemDefaults()
				c.Config.CPUWarnPct, c.Config.CPUCritPct = d.CPUWarnPct, d.CPUCritPct
				c.Config.MemWarnPct, c.Config.MemCritPct = d.MemWarnPct, d.MemCritPct
				c.Config.SwapWarnPct = d.SwapWarnPct
				c.Config.DiskWarnPct, c.Config.DiskCritPct = d.DiskWarnPct, d.DiskCritPct
				c.Config.LoadWarnPerCore = d.LoadWarnPerCore
			}
		}
		if c.FailureThreshold < 0 {
			c.FailureThreshold = 0
		}
		if err := checks.Validate(*c, n.Host); err != nil {
			return fmt.Errorf("check %q: %w", c.Name, err)
		}
	}
	return nil
}

func (s *Server) handleCreateNode(w http.ResponseWriter, r *http.Request) {
	n, err := decodeNode(r)
	if err != nil {
		writeDecodeError(w, err)
		return
	}
	n.ID = 0
	if err := s.normalizeNode(&n); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if n.DependsOnNode != nil {
		if _, err := s.Store.GetNode(r.Context(), *n.DependsOnNode); err != nil {
			writeError(w, http.StatusBadRequest, "the selected dependency node does not exist")
			return
		}
	}
	created, err := s.Store.CreateNode(r.Context(), n)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.configChanged(r.Context(), &created, fmt.Sprintf("Added node %s", created.Name), fmt.Sprintf("%d check(s): %s", len(created.Checks), checkNames(created.Checks)))
	writeJSON(w, http.StatusCreated, s.decorateNode(created, s.Engine.States(), nil))
}

func checkNames(cs []model.Check) string {
	names := make([]string, 0, len(cs))
	for _, c := range cs {
		names = append(names, c.Name)
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

func (s *Server) configChanged(ctx context.Context, n *model.Node, title, detail string) {
	if err := s.Engine.ReloadConfig(ctx); err != nil {
		s.Log.Errorf("reload config: %v", err)
	}
	ev := model.Event{Type: model.EventConfigChanged, Title: title, Detail: detail}
	if n != nil {
		ev.NodeID = &n.ID
		ev.NodeName = n.Name
	}
	s.recordEvent(ctx, ev)
}

func (s *Server) handleUpdateNode(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	existing, err := s.Store.GetNode(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	n, err := decodeNode(r)
	if err != nil {
		writeDecodeError(w, err)
		return
	}
	n.ID = id
	if err := s.normalizeNode(&n); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if n.DependsOnNode != nil {
		if err := s.checkDependencyCycle(r.Context(), id, *n.DependsOnNode); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	updated, deleted, err := s.Store.UpdateNode(r.Context(), n)
	if err != nil {
		s.fail(w, err)
		return
	}
	detail := describeNodeChange(existing, updated, len(deleted))
	s.configChanged(r.Context(), &updated, fmt.Sprintf("Updated node %s", updated.Name), detail)
	writeJSON(w, http.StatusOK, s.decorateNode(updated, s.Engine.States(), nil))
}

func describeNodeChange(before, after model.Node, deletedChecks int) string {
	var parts []string
	if before.Name != after.Name {
		parts = append(parts, fmt.Sprintf("renamed from %s", before.Name))
	}
	if before.Host != after.Host {
		parts = append(parts, fmt.Sprintf("host %s → %s", before.Host, after.Host))
	}
	if before.Group != after.Group {
		parts = append(parts, fmt.Sprintf("group %q → %q", before.Group, after.Group))
	}
	if before.Enabled != after.Enabled {
		parts = append(parts, map[bool]string{true: "enabled", false: "disabled"}[after.Enabled])
	}
	if (before.DependsOnNode == nil) != (after.DependsOnNode == nil) || (before.DependsOnNode != nil && after.DependsOnNode != nil && *before.DependsOnNode != *after.DependsOnNode) {
		parts = append(parts, "dependency changed")
	}
	added := len(after.Checks) - (len(before.Checks) - deletedChecks)
	if added > 0 {
		parts = append(parts, fmt.Sprintf("%d check(s) added", added))
	}
	if deletedChecks > 0 {
		parts = append(parts, fmt.Sprintf("%d check(s) removed", deletedChecks))
	}
	if len(parts) == 0 {
		return "Check configuration updated: " + checkNames(after.Checks)
	}
	return strings.Join(parts, "; ")
}

func (s *Server) checkDependencyCycle(ctx context.Context, nodeID, parentID int64) error {
	nodes, err := s.Store.ListNodes(ctx)
	if err != nil {
		return err
	}
	byID := map[int64]model.Node{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	if _, ok := byID[parentID]; !ok {
		return fmt.Errorf("the selected dependency node does not exist")
	}
	visited := map[int64]bool{nodeID: true}
	cur := parentID
	for {
		if visited[cur] {
			return fmt.Errorf("dependency would create a loop")
		}
		visited[cur] = true
		p := byID[cur]
		if p.DependsOnNode == nil {
			return nil
		}
		cur = *p.DependsOnNode
	}
}

func (s *Server) handleDeleteNode(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	n, err := s.Store.GetNode(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Store.DeleteNode(r.Context(), id); err != nil {
		s.fail(w, err)
		return
	}
	s.configChanged(r.Context(), nil, fmt.Sprintf("Deleted node %s", n.Name), fmt.Sprintf("Removed %d check(s) and their history.", len(n.Checks)))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleEnableNode(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeDecodeError(w, err)
		return
	}
	if err := s.Store.SetNodeEnabled(r.Context(), id, body.Enabled); err != nil {
		s.fail(w, err)
		return
	}
	n, err := s.Store.GetNode(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.configChanged(r.Context(), &n, fmt.Sprintf("%s %s", map[bool]string{true: "Enabled", false: "Disabled"}[body.Enabled], n.Name), "Monitoring "+map[bool]string{true: "resumed", false: "paused; configuration kept"}[body.Enabled]+".")
	writeJSON(w, http.StatusOK, s.decorateNode(n, s.Engine.States(), nil))
}

func (s *Server) handleDuplicateNode(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	n, err := s.Store.GetNode(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	n.ID = 0
	n.Name = n.Name + " (copy)"
	n.Enabled = false
	for i := range n.Checks {
		n.Checks[i].ID = 0
	}
	created, err := s.Store.CreateNode(r.Context(), n)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.configChanged(r.Context(), &created, fmt.Sprintf("Duplicated node as %s", created.Name), "The copy is disabled until you enable it.")
	writeJSON(w, http.StatusCreated, s.decorateNode(created, s.Engine.States(), nil))
}

func (s *Server) handleRunNode(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	results, err := s.Engine.RunNodeNow(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, results)
}

// handleSilenceNode silences (or unsilences) every check of a node.
func (s *Server) handleSilenceNode(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var body struct {
		Minutes int `json:"minutes"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeDecodeError(w, err)
		return
	}
	n, err := s.Store.GetNode(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	states := map[int64]model.CheckState{}
	for _, c := range n.Checks {
		st, err := s.Engine.Silence(r.Context(), c.ID, time.Duration(body.Minutes)*time.Minute)
		if err != nil {
			s.fail(w, err)
			return
		}
		states[c.ID] = st
	}
	writeJSON(w, http.StatusOK, s.decorateNode(n, states, nil))
}

func (s *Server) handleTemplates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, checks.Templates())
}

func (s *Server) handleGroups(w http.ResponseWriter, r *http.Request) {
	groups, tags, err := s.Store.GroupCounts(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	type entry struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	toList := func(m map[string]int) []entry {
		out := make([]entry, 0, len(m))
		for k, v := range m {
			out = append(out, entry{k, v})
		}
		for i := 0; i < len(out); i++ {
			for j := i + 1; j < len(out); j++ {
				if strings.ToLower(out[j].Name) < strings.ToLower(out[i].Name) {
					out[i], out[j] = out[j], out[i]
				}
			}
		}
		return out
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": toList(groups), "tags": toList(tags)})
}

// ---- checks ----

func (s *Server) handleTestCheck(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Check    model.Check `json:"check"`
		NodeHost string      `json:"nodeHost"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeDecodeError(w, err)
		return
	}
	settings := s.Engine.Settings()
	if body.Check.TimeoutSeconds <= 0 {
		body.Check.TimeoutSeconds = settings.General.DefaultTimeoutSecs
	}
	if body.Check.IntervalSeconds <= 0 {
		body.Check.IntervalSeconds = settings.General.DefaultIntervalSecs
	}
	if !body.Check.Type.Valid() {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unsupported check type %q", body.Check.Type))
		return
	}
	if err := checks.Validate(body.Check, body.NodeHost); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.Engine.TestCheck(r.Context(), body.Check, body.NodeHost))
}

func (s *Server) handleRunCheck(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := s.Store.GetCheck(r.Context(), id); err != nil {
		s.fail(w, err)
		return
	}
	res, err := s.Engine.RunNow(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleEnableCheck(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeDecodeError(w, err)
		return
	}
	if err := s.Store.SetCheckEnabled(r.Context(), id, body.Enabled); err != nil {
		s.fail(w, err)
		return
	}
	c, err := s.Store.GetCheck(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	n, _ := s.Store.GetNode(r.Context(), c.NodeID)
	s.configChanged(r.Context(), &n, fmt.Sprintf("%s check %s on %s", map[bool]string{true: "Enabled", false: "Disabled"}[body.Enabled], c.Name, n.Name), "")
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) handleSilenceCheck(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var body struct {
		Minutes int `json:"minutes"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeDecodeError(w, err)
		return
	}
	st, err := s.Engine.Silence(r.Context(), id, time.Duration(body.Minutes)*time.Minute)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleCheckResults(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := s.Store.RecentResults(r.Context(), id, queryInt(r, "limit", 50))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleCheckState(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	st, ok := s.Engine.State(id)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// ---- history ----

func (s *Server) historyFor(ctx context.Context, checkID int64, rangeName string) (model.HistorySeries, error) {
	rng, err := store.ParseRange(rangeName)
	if err != nil {
		return model.HistorySeries{}, err
	}
	c, err := s.Store.GetCheck(ctx, checkID)
	if err != nil {
		return model.HistorySeries{}, err
	}
	n, err := s.Store.GetNode(ctx, c.NodeID)
	if err != nil {
		return model.HistorySeries{}, err
	}
	return s.Store.History(ctx, c, n.Name, rng, time.Now())
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	ids := r.URL.Query()["checkId"]
	if len(ids) == 0 {
		writeError(w, http.StatusBadRequest, "checkId is required")
		return
	}
	if len(ids) > 1 {
		s.handleHistoryMulti(w, r)
		return
	}
	id, err := strconv.ParseInt(ids[0], 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid checkId")
		return
	}
	series, err := s.historyFor(r.Context(), id, r.URL.Query().Get("range"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.fail(w, err)
		} else {
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, series)
}

func (s *Server) handleHistoryMulti(w http.ResponseWriter, r *http.Request) {
	out := []model.HistorySeries{}
	ids := r.URL.Query()["checkId"]
	if len(ids) == 0 && r.URL.Query().Get("auto") != "" {
		ov, err := s.Engine.Overview(r.Context())
		if err != nil {
			s.fail(w, err)
			return
		}
		for _, c := range autoChecks(ov, 4) {
			ids = append(ids, strconv.FormatInt(c.check.ID, 10))
		}
	}
	for _, raw := range ids {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			continue
		}
		series, err := s.historyFor(r.Context(), id, r.URL.Query().Get("range"))
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		out = append(out, series)
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- events ----

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	events, err := s.Store.ListEvents(r.Context(), s.eventFilterFromQuery(r, 100))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, events)
}

func (s *Server) handleNote(w http.ResponseWriter, r *http.Request) {
	var body struct {
		NodeID *int64 `json:"nodeId"`
		Text   string `json:"text"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeDecodeError(w, err)
		return
	}
	body.Text = strings.TrimSpace(body.Text)
	if body.Text == "" {
		writeError(w, http.StatusBadRequest, "text is required")
		return
	}
	ev := model.Event{Type: model.EventNote, Title: "Note", Detail: body.Text}
	if body.NodeID != nil {
		if n, err := s.Store.GetNode(r.Context(), *body.NodeID); err == nil {
			ev.NodeID = &n.ID
			ev.NodeName = n.Name
			ev.Title = "Note on " + n.Name
		}
	}
	writeJSON(w, http.StatusCreated, s.recordEvent(r.Context(), ev))
}

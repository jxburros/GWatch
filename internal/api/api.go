// Package api serves the localhost-only JSON API and the embedded web UI.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/backup"
	"github.com/jxburros/GWatch/internal/checks"
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
}

// Handler builds the router.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/version", s.handleVersion)
	mux.HandleFunc("GET /api/overview", s.handleOverview)
	mux.HandleFunc("GET /api/wallboard", s.handleWallboard)
	mux.HandleFunc("GET /api/stream", s.handleStream)

	mux.HandleFunc("GET /api/nodes", s.handleListNodes)
	mux.HandleFunc("POST /api/nodes", s.handleCreateNode)
	mux.HandleFunc("GET /api/nodes/{id}", s.handleGetNode)
	mux.HandleFunc("PUT /api/nodes/{id}", s.handleUpdateNode)
	mux.HandleFunc("DELETE /api/nodes/{id}", s.handleDeleteNode)
	mux.HandleFunc("POST /api/nodes/{id}/enable", s.handleEnableNode)
	mux.HandleFunc("POST /api/nodes/{id}/duplicate", s.handleDuplicateNode)
	mux.HandleFunc("POST /api/nodes/{id}/run", s.handleRunNode)
	mux.HandleFunc("GET /api/templates", s.handleTemplates)
	mux.HandleFunc("GET /api/groups", s.handleGroups)

	mux.HandleFunc("POST /api/checks/test", s.handleTestCheck)
	mux.HandleFunc("POST /api/checks/{id}/run", s.handleRunCheck)
	mux.HandleFunc("POST /api/checks/{id}/enable", s.handleEnableCheck)
	mux.HandleFunc("POST /api/checks/{id}/silence", s.handleSilenceCheck)
	mux.HandleFunc("GET /api/checks/{id}/results", s.handleCheckResults)
	mux.HandleFunc("GET /api/checks/{id}/state", s.handleCheckState)

	mux.HandleFunc("GET /api/history", s.handleHistory)
	mux.HandleFunc("GET /api/history/multi", s.handleHistoryMulti)

	mux.HandleFunc("GET /api/events", s.handleEvents)
	mux.HandleFunc("POST /api/events/note", s.handleNote)

	mux.HandleFunc("GET /api/maintenance", s.handleListMaintenance)
	mux.HandleFunc("POST /api/maintenance", s.handleSaveMaintenance)
	mux.HandleFunc("PUT /api/maintenance/{id}", s.handleSaveMaintenance)
	mux.HandleFunc("DELETE /api/maintenance/{id}", s.handleDeleteMaintenance)

	mux.HandleFunc("GET /api/dashboards", s.handleListDashboards)
	mux.HandleFunc("POST /api/dashboards", s.handleSaveDashboard)
	mux.HandleFunc("GET /api/dashboards/{id}", s.handleGetDashboard)
	mux.HandleFunc("PUT /api/dashboards/{id}", s.handleSaveDashboard)
	mux.HandleFunc("DELETE /api/dashboards/{id}", s.handleDeleteDashboard)

	mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	mux.HandleFunc("PUT /api/settings", s.handlePutSettings)
	mux.HandleFunc("POST /api/settings/test-email", s.handleTestEmail)
	mux.HandleFunc("GET /api/retention/status", s.handleRetentionStatus)
	mux.HandleFunc("POST /api/retention/run", s.handleRetentionRun)

	mux.HandleFunc("GET /api/backups", s.handleListBackups)
	mux.HandleFunc("POST /api/backups", s.handleCreateBackup)
	mux.HandleFunc("GET /api/backups/{name}/download", s.handleDownloadBackup)
	mux.HandleFunc("DELETE /api/backups/{name}", s.handleDeleteBackup)
	mux.HandleFunc("POST /api/backups/restore", s.handleRestoreUpload)
	mux.HandleFunc("POST /api/backups/restore-existing", s.handleRestoreExisting)

	mux.HandleFunc("GET /api/export/history.csv", s.handleExportHistory)
	mux.HandleFunc("GET /api/export/results.csv", s.handleExportResults)
	mux.HandleFunc("GET /api/export/events.csv", s.handleExportEvents)
	mux.HandleFunc("GET /api/export/config.json", s.handleExportConfig)

	mux.HandleFunc("GET /api/logs", s.handleLogs)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "unknown API endpoint")
	})

	if s.Web != nil {
		mux.Handle("/", s.staticHandler())
	}
	return noCache(mux)
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

func decodeJSON(r *http.Request, v any) error {
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
	writeJSON(w, http.StatusOK, s.Engine.Health(r.Context()))
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	h := s.Engine.Health(r.Context())
	writeJSON(w, http.StatusOK, map[string]string{"version": s.Version, "platform": h.Platform})
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
	// Pick up to 6 checks: critical/high importance first, then ping/http checks.
	type cand struct {
		check model.Check
		node  model.Node
		score int
	}
	var cands []cand
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
			cands = append(cands, cand{cv.Check, nv.Node, score})
		}
	}
	for i := 0; i < len(cands); i++ {
		for j := i + 1; j < len(cands); j++ {
			if cands[j].score > cands[i].score || (cands[j].score == cands[i].score && cands[j].node.Name < cands[i].node.Name) {
				cands[i], cands[j] = cands[j], cands[i]
			}
		}
	}
	rng, _ := store.ParseRange("24h")
	for i, c := range cands {
		if i >= 6 {
			break
		}
		series, err := s.Store.History(r.Context(), c.check, c.node.Name, rng, time.Now())
		if err == nil {
			doc.Trends = append(doc.Trends, series)
		}
	}
	writeJSON(w, http.StatusOK, doc)
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
		writeError(w, http.StatusBadRequest, err.Error())
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
	s.Engine.RecordEvent(ev)
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
		writeError(w, http.StatusBadRequest, err.Error())
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
		writeError(w, http.StatusBadRequest, err.Error())
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
		writeError(w, http.StatusBadRequest, err.Error())
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
		writeError(w, http.StatusBadRequest, err.Error())
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
		writeError(w, http.StatusBadRequest, err.Error())
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
	for _, raw := range r.URL.Query()["checkId"] {
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
	f := store.EventFilter{Limit: queryInt(r, "limit", 100), BeforeID: int64(queryInt(r, "before", 0)), NodeID: queryInt64Ptr(r, "nodeId"), CheckID: queryInt64Ptr(r, "checkId")}
	for _, t := range r.URL.Query()["type"] {
		for _, part := range strings.Split(t, ",") {
			if part = strings.TrimSpace(part); part != "" {
				f.Types = append(f.Types, model.EventType(part))
			}
		}
	}
	if since := r.URL.Query().Get("since"); since != "" {
		if t, err := time.Parse(time.RFC3339, since); err == nil {
			f.Since = &t
		}
	}
	events, err := s.Store.ListEvents(r.Context(), f)
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
		writeError(w, http.StatusBadRequest, err.Error())
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
	writeJSON(w, http.StatusCreated, s.Engine.RecordEvent(ev))
}

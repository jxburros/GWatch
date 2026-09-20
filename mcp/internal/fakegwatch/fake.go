// Package fakegwatch is a stand-in GWatch for the MCP server's tests.
//
// It answers the endpoints the tools use and, more importantly, it mirrors the
// authorization policy the real service enforces in internal/api/policy.go: a
// request without a known key is 401, a read key on a write route is 403 with
// GWatch's own wording, and the routes that are denied to every key whatever
// its scope stay denied. That is what makes the "a read-only key can query but
// every write is rejected server-side" test in ROADMAP 3.2 mean something —
// the refusal comes from the server, not from the tool layer.
package fakegwatch

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Scope values, matching GWatch's API key scopes.
const (
	ScopeRead      = "read"
	ScopeReadWrite = "readwrite"
)

// ReadOnlyMessage is the exact text GWatch answers a read key on a write route
// with (internal/api/policy.go, denialMessage).
const ReadOnlyMessage = "This API key is read-only."

// Server is a fake GWatch instance.
type Server struct {
	*httptest.Server

	mu     sync.Mutex
	keys   map[string]key // secret -> identity
	nodes  map[int64]*node
	nextID int64
	notes  []map[string]any

	// Requests records every request the client made, so a test can assert on
	// the prefix and headers the MCP server is supposed to send.
	Requests []Request
}

// Request is one recorded call.
type Request struct {
	Method    string
	Path      string
	Query     string
	UserAgent string
	APIKey    string
	Status    int
}

type key struct {
	name  string
	scope string
}

type node struct {
	ID     int64    `json:"id"`
	Name   string   `json:"name"`
	Host   string   `json:"host"`
	Groups []string `json:"groups"`
	// Group is the deprecated single-group alias GWatch still sends and
	// accepts; groups decides when both are given, exactly as it does there.
	Group      string           `json:"group"`
	Tags       []string         `json:"tags"`
	Notes      string           `json:"notes"`
	Importance string           `json:"importance"`
	Enabled    bool             `json:"enabled"`
	Checks     []map[string]any `json:"checks"`
	Silenced   int              `json:"-"`
}

// New starts a fake GWatch. Register keys with AddKey before using it.
func New() *Server {
	s := &Server{keys: map[string]key{}, nodes: map[int64]*node{}, nextID: 1}
	s.seed()
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// AddKey registers an API key secret with a scope.
func (s *Server) AddKey(secret, name, scope string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys[secret] = key{name: name, scope: scope}
}

// Recorded returns a copy of the recorded requests.
func (s *Server) Recorded() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.Requests...)
}

func (s *Server) seed() {
	// The router is in two groups, so the tests have a node whose second
	// group is the one a filter names.
	s.nodes[1] = &node{
		ID: 1, Name: "Router", Host: "192.168.1.1", Groups: []string{"Home Network", "Critical"},
		Tags: []string{"infra"}, Importance: "critical", Enabled: true,
		Checks: []map[string]any{
			{"id": 11, "nodeId": 1, "type": "ping", "name": "Ping", "enabled": true, "intervalSeconds": 60, "timeoutSeconds": 5, "config": map[string]any{}},
		},
	}
	s.nodes[2] = &node{
		ID: 2, Name: "Website", Host: "https://example.com", Groups: []string{"Public"},
		Tags: []string{"web", "external"}, Importance: "high", Enabled: true,
		Checks: []map[string]any{
			{"id": 21, "nodeId": 2, "type": "http", "name": "Homepage", "enabled": true, "intervalSeconds": 120, "timeoutSeconds": 10, "config": map[string]any{"target": "https://example.com"}},
		},
	}
	for _, n := range s.nodes {
		n.syncGroups()
	}
	s.nextID = 3
}

// syncGroups mirrors what GWatch does with the two fields: a node given only
// the old single group is read as being in that one group, and group always
// comes back out as the first of the list.
func (n *node) syncGroups() {
	if len(n.Groups) == 0 && strings.TrimSpace(n.Group) != "" {
		n.Groups = []string{n.Group}
	}
	if n.Groups == nil {
		n.Groups = []string{}
	}
	n.Group = ""
	if len(n.Groups) > 0 {
		n.Group = n.Groups[0]
	}
}

// ---- policy --------------------------------------------------------------

// writeRoutes are the routes a readwrite key may call and a read key may not.
// The patterns use {} for an id segment, as GWatch's own table does.
var writeRoutes = map[string]bool{
	"POST /nodes":              true,
	"PUT /nodes/{}":            true,
	"DELETE /nodes/{}":         true,
	"POST /nodes/{}/enable":    true,
	"POST /nodes/{}/run":       true,
	"POST /nodes/{}/silence":   true,
	"POST /nodes/{}/duplicate": true,
	"POST /checks/test":        true,
	"POST /checks/{}/run":      true,
	"POST /events/note":        true,
}

// deniedRoutes are refused to every API key whatever its scope.
var deniedPrefixes = []string{
	"/settings", "/backups", "/logs", "/triggers", "/endpoints", "/users",
	"/apikeys", "/update", "/network", "/automation", "/retention",
	"/export/config.json", "/export/logs.txt", "/actions",
}

// pattern reduces a concrete path to the shape used in writeRoutes: numeric
// segments become {}.
func pattern(path string) string {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	for i, s := range segs {
		if _, err := strconv.ParseInt(s, 10, 64); err == nil {
			segs[i] = "{}"
		}
	}
	return "/" + strings.Join(segs, "/")
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	rec := Request{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery,
		UserAgent: r.Header.Get("User-Agent"), APIKey: r.Header.Get("X-API-Key")}
	rw := &recorder{ResponseWriter: w, status: 200}
	defer func() {
		rec.Status = rw.status
		s.mu.Lock()
		s.Requests = append(s.Requests, rec)
		s.mu.Unlock()
	}()

	// Everything is served under the versioned prefix; the unversioned alias
	// is not modelled because the MCP server must always use /api/v1.
	if !strings.HasPrefix(r.URL.Path, "/api/v1/") {
		fail(rw, http.StatusNotFound, "not found")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")

	secret := r.Header.Get("X-API-Key")
	if secret == "" {
		if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer ") {
			secret = strings.TrimPrefix(a, "Bearer ")
		}
	}
	s.mu.Lock()
	k, known := s.keys[secret]
	s.mu.Unlock()
	if !known {
		fail(rw, http.StatusUnauthorized, "sign in required")
		return
	}

	for _, p := range deniedPrefixes {
		if path == p || strings.HasPrefix(path, p+"/") {
			fail(rw, http.StatusForbidden,
				fmt.Sprintf("API keys cannot use %s; sign in as an administrator in the web interface.", r.URL.Path))
			return
		}
	}
	if writeRoutes[r.Method+" "+pattern(path)] && k.scope != ScopeReadWrite {
		fail(rw, http.StatusForbidden, ReadOnlyMessage)
		return
	}

	s.route(rw, r, path, k)
}

func (s *Server) route(w http.ResponseWriter, r *http.Request, path string, k key) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch {
	case r.Method == http.MethodGet && path == "/me":
		ok(w, map[string]any{
			"kind": "apikey", "name": k.name, "scope": k.scope,
			"isAdmin": false, "canWrite": k.scope == ScopeReadWrite, "signedIn": true,
		})
	case r.Method == http.MethodGet && path == "/health":
		ok(w, map[string]any{
			"version": "0.4.1", "serviceMode": "console", "serviceRunning": true,
			"schedulerRunning": true, "startedAt": time.Now().Add(-time.Hour), "uptimeSeconds": 3600,
			"now": time.Now(), "checksTotal": 2, "checksEnabled": 2, "checksRunning": 0,
			"databaseBytes": 4096, "alertsEnabled": false, "smtpConfigured": false,
			"platform": "linux/amd64", "recentErrors": []any{},
		})
	case r.Method == http.MethodGet && path == "/overview":
		s.overview(w)
	case r.Method == http.MethodGet && path == "/nodes":
		ok(w, s.nodeList())
	case r.Method == http.MethodGet && path == "/groups":
		ok(w, map[string]any{
			"groups": []map[string]any{{"name": "Critical", "count": 1}, {"name": "Home Network", "count": 1}, {"name": "Public", "count": 1}},
			"tags":   []map[string]any{{"name": "infra", "count": 1}, {"name": "web", "count": 1}},
		})
	case r.Method == http.MethodGet && path == "/templates":
		ok(w, []map[string]any{{"id": "website", "name": "Website", "description": "HTTP + cert", "node": map[string]any{}, "checks": []any{}}})
	case r.Method == http.MethodGet && path == "/history/multi":
		s.history(w, r)
	case r.Method == http.MethodGet && path == "/events":
		s.events(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/checks/") && strings.HasSuffix(path, "/results"):
		s.results(w, r, path)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/nodes/"):
		s.getNode(w, path)
	case r.Method == http.MethodPost && path == "/nodes":
		s.createNode(w, r)
	case r.Method == http.MethodPut && strings.HasPrefix(path, "/nodes/"):
		s.updateNode(w, r, path)
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/nodes/"):
		s.deleteNode(w, path)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/enable"):
		s.enableNode(w, r, path)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/silence"):
		s.silenceNode(w, r, path)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/run"):
		s.runNode(w, path)
	case r.Method == http.MethodPost && path == "/events/note":
		s.addNote(w, r)
	case r.Method == http.MethodPost && path == "/checks/test":
		ok(w, map[string]any{"checkId": 0, "ts": time.Now(), "success": true, "status": "up",
			"message": "reachable in 3 ms", "latencyMs": 3.0, "attempts": 1, "details": map[string]any{}})
	default:
		fail(w, http.StatusNotFound, "not found")
	}
}

// ---- handlers ------------------------------------------------------------

func (s *Server) nodeList() []map[string]any {
	out := make([]map[string]any, 0, len(s.nodes))
	for _, id := range s.ids() {
		out = append(out, s.nodeDoc(s.nodes[id], true))
	}
	return out
}

func (s *Server) ids() []int64 {
	ids := make([]int64, 0, len(s.nodes))
	for id := range s.nodes {
		ids = append(ids, id)
	}
	for i := range ids {
		for j := i + 1; j < len(ids); j++ {
			if ids[j] < ids[i] {
				ids[i], ids[j] = ids[j], ids[i]
			}
		}
	}
	return ids
}

// nodeDoc renders a node the way the API does: the stored record plus live
// state. Node 2 is down so the tests have something interesting to summarise.
func (s *Server) nodeDoc(n *node, withState bool) map[string]any {
	status := "up"
	msg := "reachable in 3 ms"
	if n.ID == 2 {
		status, msg = "down", "connection refused"
	}
	doc := map[string]any{
		"id": n.ID, "name": n.Name, "host": n.Host, "groups": n.Groups, "group": n.Group,
		"tags": n.Tags, "notes": n.Notes, "importance": n.Importance,
		"enabled": n.Enabled, "dependsOnNodeId": nil, "checks": n.Checks,
	}
	if !withState {
		return doc
	}
	state := map[string]any{}
	for _, c := range n.Checks {
		id := fmt.Sprintf("%v", asInt(c["id"]))
		state[id] = map[string]any{
			"checkId": asInt(c["id"]), "status": status, "consecutiveFailures": 0,
			"lastMessage": msg, "lastRunAt": time.Now(), "lastLatencyMs": 3.0,
		}
	}
	doc["status"] = status
	doc["stateByCheck"] = state
	doc["inMaintenance"] = false
	return doc
}

func (s *Server) overview(w http.ResponseWriter) {
	ok(w, map[string]any{
		"summary": map[string]any{"up": 1, "degraded": 0, "down": 1, "unknown": 0, "paused": 0, "maintenance": 0, "total": 2},
		"groups": []map[string]any{
			{"name": "Home Network", "status": "up", "up": 1, "total": 1},
			{"name": "Public", "status": "down", "down": 1, "total": 1},
		},
		"incidents": []map[string]any{
			{"id": 90, "ts": time.Now(), "type": "down", "nodeId": 2, "checkId": 21,
				"nodeName": "Website", "checkName": "Homepage", "title": "Website is down", "detail": "connection refused"},
		},
		"certWarnings": []any{},
		"attention": []map[string]any{
			{"nodeId": 2, "nodeName": "Website", "checkId": 21, "checkName": "Homepage",
				"status": "down", "message": "connection refused", "since": time.Now().Add(-10 * time.Minute)},
		},
		"maintenance": []any{},
		"generatedAt": time.Now(),
	})
}

// history returns 500 points per requested check, which is well over the
// tool's cap, so the striding is actually exercised.
func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	rng := r.URL.Query().Get("range")
	if rng == "" {
		rng = "24h"
	}
	var out []map[string]any
	for _, raw := range r.URL.Query()["checkId"] {
		id, _ := strconv.ParseInt(raw, 10, 64)
		points := make([]map[string]any, 0, 500)
		base := time.Now().Add(-24 * time.Hour)
		for i := 0; i < 500; i++ {
			avg := float64(i % 40)
			points = append(points, map[string]any{
				"ts": base.Add(time.Duration(i) * time.Minute), "avgMs": avg, "minMs": avg, "maxMs": avg,
				"jitterMs": nil, "lossPct": nil, "availability": 100.0, "count": 1, "failures": 0,
			})
		}
		out = append(out, map[string]any{
			"checkId": id, "checkName": "Ping", "nodeName": "Router", "checkType": "ping",
			"range": rng, "source": "raw", "bucketSeconds": 0,
			"from": base, "to": time.Now(), "points": points,
			"summary": map[string]any{"availability": 99.5, "avgMs": 12.0, "minMs": 1.0, "maxMs": 40.0, "count": 500, "failures": 2},
		})
	}
	ok(w, out)
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 100
	if v, err := strconv.Atoi(q.Get("limit")); err == nil && v > 0 {
		limit = v
	}
	all := []map[string]any{
		{"id": 90, "ts": time.Now(), "type": "down", "nodeId": 2, "checkId": 21, "nodeName": "Website",
			"checkName": "Homepage", "title": "Website is down", "detail": "connection refused"},
		{"id": 89, "ts": time.Now().Add(-time.Hour), "type": "recovered", "nodeId": 1, "checkId": 11,
			"nodeName": "Router", "checkName": "Ping", "title": "Router recovered", "detail": ""},
		{"id": 88, "ts": time.Now().Add(-2 * time.Hour), "type": "note", "nodeId": nil, "checkId": nil,
			"title": "note", "detail": "rebooted the switch", "actor": "api key MCP (read-write)"},
	}
	if t := q.Get("type"); t != "" {
		var keep []map[string]any
		for _, e := range all {
			if e["type"] == t {
				keep = append(keep, e)
			}
		}
		all = keep
	}
	if len(all) > limit {
		all = all[:limit]
	}
	if all == nil {
		all = []map[string]any{}
	}
	ok(w, all)
}

func (s *Server) results(w http.ResponseWriter, r *http.Request, path string) {
	id, _ := strconv.ParseInt(strings.Split(strings.Trim(path, "/"), "/")[1], 10, 64)
	limit := 50
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = v
	}
	out := make([]map[string]any, 0, limit)
	for i := 0; i < limit && i < 5; i++ {
		out = append(out, map[string]any{
			"id": 1000 + i, "checkId": id, "ts": time.Now().Add(-time.Duration(i) * time.Minute),
			"success": i != 0, "status": map[bool]string{true: "down", false: "up"}[i == 0],
			"message": "reachable", "latencyMs": 4.5, "attempts": 1, "details": map[string]any{},
		})
	}
	ok(w, out)
}

func (s *Server) getNode(w http.ResponseWriter, path string) {
	n := s.byPath(path)
	if n == nil {
		fail(w, http.StatusNotFound, "node not found")
		return
	}
	doc := s.nodeDoc(n, true)
	doc["lastResults"] = map[string]any{}
	ok(w, doc)
}

func (s *Server) createNode(w http.ResponseWriter, r *http.Request) {
	var in node
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.Host) == "" {
		fail(w, http.StatusBadRequest, "name and host are required")
		return
	}
	in.syncGroups()
	in.ID = s.nextID
	s.nextID++
	for i := range in.Checks {
		in.Checks[i]["id"] = float64(in.ID*100 + int64(i))
		in.Checks[i]["nodeId"] = float64(in.ID)
	}
	s.nodes[in.ID] = &in
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-GWatch-API-Version", "1")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(s.nodeDoc(&in, false))
}

func (s *Server) updateNode(w http.ResponseWriter, r *http.Request, path string) {
	n := s.byPath(path)
	if n == nil {
		fail(w, http.StatusNotFound, "node not found")
		return
	}
	var in node
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	in.syncGroups()
	in.ID = n.ID
	s.nodes[n.ID] = &in
	ok(w, s.nodeDoc(&in, true))
}

func (s *Server) deleteNode(w http.ResponseWriter, path string) {
	n := s.byPath(path)
	if n == nil {
		fail(w, http.StatusNotFound, "node not found")
		return
	}
	delete(s.nodes, n.ID)
	ok(w, map[string]any{"ok": true})
}

func (s *Server) enableNode(w http.ResponseWriter, r *http.Request, path string) {
	n := s.byPath(path)
	if n == nil {
		fail(w, http.StatusNotFound, "node not found")
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	n.Enabled = body.Enabled
	ok(w, s.nodeDoc(n, true))
}

func (s *Server) silenceNode(w http.ResponseWriter, r *http.Request, path string) {
	n := s.byPath(path)
	if n == nil {
		fail(w, http.StatusNotFound, "node not found")
		return
	}
	var body struct {
		Minutes int `json:"minutes"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	n.Silenced = body.Minutes
	ok(w, s.nodeDoc(n, true))
}

func (s *Server) runNode(w http.ResponseWriter, path string) {
	n := s.byPath(path)
	if n == nil {
		fail(w, http.StatusNotFound, "node not found")
		return
	}
	out := make([]map[string]any, 0, len(n.Checks))
	for _, c := range n.Checks {
		out = append(out, map[string]any{
			"id": 2000, "checkId": asInt(c["id"]), "ts": time.Now(), "success": true,
			"status": "up", "message": "reachable in 3 ms", "latencyMs": 3.0, "attempts": 1,
			"details": map[string]any{},
		})
	}
	ok(w, out)
}

func (s *Server) addNote(w http.ResponseWriter, r *http.Request) {
	var body struct {
		NodeID *int64 `json:"nodeId"`
		Text   string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Text) == "" {
		fail(w, http.StatusBadRequest, "text is required")
		return
	}
	ev := map[string]any{"id": 500 + len(s.notes), "ts": time.Now(), "type": "note",
		"nodeId": body.NodeID, "checkId": nil, "title": "note", "detail": body.Text}
	s.notes = append(s.notes, ev)
	ok(w, ev)
}

// ---- plumbing ------------------------------------------------------------

func (s *Server) byPath(path string) *node {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	if len(segs) < 2 {
		return nil
	}
	id, err := strconv.ParseInt(segs[1], 10, 64)
	if err != nil {
		return nil
	}
	return s.nodes[id]
}

func asInt(v any) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return 0
}

type recorder struct {
	http.ResponseWriter
	status int
}

func (r *recorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func ok(w http.ResponseWriter, v any) { writeJSON(w, v) }

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-GWatch-API-Version", "1")
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

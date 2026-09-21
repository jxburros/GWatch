package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/jxburros/GWatch/internal/auth"
)

// level is the minimum standing a principal needs for a route.
type level int

const (
	// levelPublic needs no identity at all.
	levelPublic level = iota
	// levelViewer needs any authenticated identity; it marks read-only routes.
	levelViewer
	// levelUser needs a signed-in user account (self-service routes).
	levelUser
	// levelAdmin needs the admin role.
	levelAdmin
)

func (l level) String() string {
	switch l {
	case levelPublic:
		return "public"
	case levelViewer:
		return "viewer"
	case levelUser:
		return "user"
	default:
		return "admin"
	}
}

// keyMode says what an API key may do on a route. This is the boundary an
// external agent (the MCP companion of ROADMAP 3.2) runs into, so it is stated
// per route rather than derived from the HTTP method.
type keyMode int

const (
	// keyDeny: API keys are refused whatever their scope. Administration,
	// automation execution, backups, updates and the service log live here.
	keyDeny keyMode = iota
	// keyRead: any valid key may call this route.
	keyRead
	// keyWrite: only a readwrite key may call this route.
	keyWrite
)

// routePolicy is one row of the authorization table.
type routePolicy struct {
	Method  string // "" matches any method
	Pattern string // the same pattern the mux is registered with
	Level   level
	Key     keyMode
}

// policies is the complete authorization table. It is an explicit list on
// purpose: "GET is safe" heuristics quietly grant read access to whatever is
// added next, and several GET routes here (settings, backups, config export,
// the service log) are not safe to hand out.
//
// Anything not listed falls through to admin-only with API keys denied, so a
// route added without a policy fails closed rather than open.
var policies = []routePolicy{
	// ---- public ----
	{"GET", "/api/health", levelPublic, keyRead},
	{"GET", "/api/version", levelPublic, keyRead},
	{"GET", "/api/me", levelPublic, keyRead},
	{"GET", "/api/auth/setup", levelPublic, keyRead},
	// A projected wallboard: public only in that it takes no credential here.
	// The handler refuses every caller that does not present the token of a
	// board whose sharing its administrator switched on.
	{"GET", "/api/wallboards/{id}/view", levelPublic, keyRead},
	{"POST", "/api/auth/login", levelPublic, keyDeny},
	{"POST", "/api/auth/logout", levelPublic, keyDeny},

	// ---- self-service ----
	{"POST", "/api/auth/change-password", levelUser, keyDeny},

	// ---- read: monitoring data ----
	{"GET", "/api/status", levelViewer, keyRead},
	{"GET", "/api/overview", levelViewer, keyRead},
	{"GET", "/api/wallboard", levelViewer, keyRead},
	{"GET", "/api/stream", levelViewer, keyRead},
	{"GET", "/api/nodes", levelViewer, keyRead},
	{"GET", "/api/nodes/{id}", levelViewer, keyRead},
	{"GET", "/api/templates", levelViewer, keyRead},
	{"GET", "/api/groups", levelViewer, keyRead},
	{"GET", "/api/checks/{id}/results", levelViewer, keyRead},
	{"GET", "/api/checks/{id}/state", levelViewer, keyRead},
	{"GET", "/api/history", levelViewer, keyRead},
	{"GET", "/api/history/multi", levelViewer, keyRead},
	{"GET", "/api/events", levelViewer, keyRead},
	{"GET", "/api/maintenance", levelViewer, keyRead},
	{"GET", "/api/dashboards", levelViewer, keyRead},
	{"GET", "/api/dashboards/{id}", levelViewer, keyRead},
	{"GET", "/api/wallboards", levelViewer, keyRead},
	{"GET", "/api/wallboards/{id}", levelViewer, keyRead},
	{"GET", "/api/charts", levelViewer, keyRead},
	{"GET", "/api/hosts", levelViewer, keyRead},
	{"GET", "/api/hosts/{key}", levelViewer, keyRead},
	{"GET", "/api/hosts/{key}/history", levelViewer, keyRead},
	{"GET", "/api/export/history.csv", levelViewer, keyRead},
	{"GET", "/api/export/results.csv", levelViewer, keyRead},
	{"GET", "/api/export/events.csv", levelViewer, keyRead},

	// ---- read, but never through an API key ----
	// The service log and the automation inventory describe the machine GWatch
	// runs on rather than the network it watches; a viewer in the browser may
	// read them (ROADMAP 2.1 counts the log as part of the audit trail), an
	// integration key may not.
	{"GET", "/api/logs", levelViewer, keyDeny},
	{"GET", "/api/export/logs.txt", levelViewer, keyDeny},
	{"GET", "/api/triggers", levelViewer, keyDeny},
	{"GET", "/api/rules", levelViewer, keyDeny},
	{"GET", "/api/rules/{id}", levelViewer, keyDeny},
	{"GET", "/api/endpoints", levelViewer, keyDeny},
	{"GET", "/api/automation/meta", levelViewer, keyDeny},
	{"GET", "/api/retention/status", levelViewer, keyDeny},
	{"GET", "/api/update/status", levelViewer, keyDeny},

	// ---- monitoring writes: a readwrite key may do these ----
	{"POST", "/api/nodes", levelAdmin, keyWrite},
	{"PUT", "/api/nodes/{id}", levelAdmin, keyWrite},
	// One patch across many nodes and checks. It changes nothing a PUT could
	// not, so it sits at the same standing as the single-node write.
	{"PATCH", "/api/nodes/bulk", levelAdmin, keyWrite},
	{"DELETE", "/api/nodes/{id}", levelAdmin, keyWrite},
	{"POST", "/api/nodes/{id}/enable", levelAdmin, keyWrite},
	{"POST", "/api/nodes/{id}/duplicate", levelAdmin, keyWrite},
	{"POST", "/api/nodes/{id}/run", levelAdmin, keyWrite},
	{"POST", "/api/nodes/{id}/silence", levelAdmin, keyWrite},
	{"POST", "/api/checks/test", levelAdmin, keyWrite},
	{"POST", "/api/checks/{id}/run", levelAdmin, keyWrite},
	{"POST", "/api/checks/{id}/enable", levelAdmin, keyWrite},
	{"POST", "/api/checks/{id}/silence", levelAdmin, keyWrite},
	{"POST", "/api/events/note", levelAdmin, keyWrite},
	{"POST", "/api/maintenance", levelAdmin, keyWrite},
	{"PUT", "/api/maintenance/{id}", levelAdmin, keyWrite},
	{"DELETE", "/api/maintenance/{id}", levelAdmin, keyWrite},
	{"POST", "/api/dashboards", levelAdmin, keyWrite},
	{"PUT", "/api/dashboards/{id}", levelAdmin, keyWrite},
	{"DELETE", "/api/dashboards/{id}", levelAdmin, keyWrite},
	{"POST", "/api/wallboards", levelAdmin, keyWrite},
	{"PUT", "/api/wallboards/{id}", levelAdmin, keyWrite},
	{"DELETE", "/api/wallboards/{id}", levelAdmin, keyWrite},
	// Handing out an address that needs no sign-in is an administrator's
	// decision and never an integration's, however wide its key.
	{"POST", "/api/wallboards/{id}/share", levelAdmin, keyDeny},
	{"PUT", "/api/charts", levelAdmin, keyWrite},

	// ---- administration: never through an API key ----
	// Walking a device takes a credential and an address and makes GWatch
	// talk to whatever is there. That is a probe, and it belongs to the
	// administrator in front of the machine rather than to any integration,
	// however wide its key.
	{"POST", "/api/snmp/walk", levelAdmin, keyDeny},
	{"GET", "/api/network", levelAdmin, keyDeny},
	// Discovery pings a few thousand addresses and then creates monitors from
	// what answered. Both halves are an administrator's decision, and reading
	// a sweep's results is a map of the network, so even the GETs are held to
	// the same standing as the sweep itself.
	{"GET", "/api/discovery", levelAdmin, keyDeny},
	{"POST", "/api/discovery", levelAdmin, keyDeny},
	{"GET", "/api/discovery/{id}", levelAdmin, keyDeny},
	{"POST", "/api/discovery/{id}/cancel", levelAdmin, keyDeny},
	{"POST", "/api/discovery/{id}/add", levelAdmin, keyDeny},
	{"GET", "/api/settings", levelAdmin, keyDeny},
	{"PUT", "/api/settings", levelAdmin, keyDeny},
	{"POST", "/api/settings/test-email", levelAdmin, keyDeny},
	// Which database GWatch keeps its data in (database.json). Reading it
	// names a server; writing it moves the data. An integration key has no
	// business with either.
	{"GET", "/api/database", levelAdmin, keyDeny},
	{"PUT", "/api/database", levelAdmin, keyDeny},
	{"POST", "/api/database/test", levelAdmin, keyDeny},
	{"POST", "/api/retention/run", levelAdmin, keyDeny},
	{"GET", "/api/backups", levelAdmin, keyDeny},
	// The agent skill is set-up material for the administrator configuring an
	// assistant, not monitoring data, so an assistant's own key cannot read it.
	{"GET", "/api/mcp/status", levelAdmin, keyDeny},
	{"GET", "/api/mcp/skill", levelAdmin, keyDeny},
	{"POST", "/api/backups", levelAdmin, keyDeny},
	{"GET", "/api/backups/{name}/download", levelAdmin, keyDeny},
	{"DELETE", "/api/backups/{name}", levelAdmin, keyDeny},
	{"POST", "/api/backups/restore", levelAdmin, keyDeny},
	{"POST", "/api/backups/restore-existing", levelAdmin, keyDeny},
	{"GET", "/api/export/config.json", levelAdmin, keyDeny},
	{"POST", "/api/triggers", levelAdmin, keyDeny},
	{"PUT", "/api/triggers/{id}", levelAdmin, keyDeny},
	{"DELETE", "/api/triggers/{id}", levelAdmin, keyDeny},
	{"POST", "/api/triggers/{id}/run", levelAdmin, keyDeny},
	{"POST", "/api/rules", levelAdmin, keyDeny},
	{"PUT", "/api/rules/{id}", levelAdmin, keyDeny},
	{"DELETE", "/api/rules/{id}", levelAdmin, keyDeny},
	{"POST", "/api/rules/{id}/test", levelAdmin, keyDeny},
	{"POST", "/api/actions/test", levelAdmin, keyDeny},
	{"POST", "/api/endpoints", levelAdmin, keyDeny},
	{"PUT", "/api/endpoints/{id}", levelAdmin, keyDeny},
	{"DELETE", "/api/endpoints/{id}", levelAdmin, keyDeny},
	{"POST", "/api/endpoints/{id}/run", levelAdmin, keyDeny},
	{"GET", "/api/update/releases", levelAdmin, keyDeny},
	{"POST", "/api/update/check", levelAdmin, keyDeny},
	{"POST", "/api/update/apply", levelAdmin, keyDeny},
	{"GET", "/api/users", levelAdmin, keyDeny},
	{"POST", "/api/users", levelAdmin, keyDeny},
	{"PUT", "/api/users/{id}", levelAdmin, keyDeny},
	{"DELETE", "/api/users/{id}", levelAdmin, keyDeny},
	// Registering a machine mints a credential, so it sits with the other
	// credential routes: administrators in the browser only, never an API key.
	{"GET", "/api/agents", levelAdmin, keyDeny},
	{"POST", "/api/agents", levelAdmin, keyDeny},
	{"PUT", "/api/agents/{id}", levelAdmin, keyDeny},
	{"DELETE", "/api/agents/{id}", levelAdmin, keyDeny},
	{"GET", "/api/agents/pairings", levelAdmin, keyDeny},
	{"POST", "/api/agents/pairings", levelAdmin, keyDeny},
	{"DELETE", "/api/agents/pairings/{id}", levelAdmin, keyDeny},
	// Redeeming a pairing code is public because it cannot be anything else:
	// the machine typing the code has no credential yet, and getting one is
	// the point of the call. The code itself is the credential — one machine,
	// one use, fifteen minutes, cancellable — and wrong ones are counted
	// against the same per-IP failure budget as wrong passwords.
	{"POST", "/api/agents/pair", levelPublic, keyDeny},

	{"GET", "/api/apikeys", levelAdmin, keyDeny},
	{"POST", "/api/apikeys", levelAdmin, keyDeny},
	{"DELETE", "/api/apikeys/{id}", levelAdmin, keyDeny},

	// Unknown /api/… paths: admin-only, so a probe from a viewer or a key
	// cannot map the surface. The handler behind it answers 404.
	{"", "/api/", levelAdmin, keyDeny},
}

// fallbackPolicy is used for any request the table does not match. It is the
// strictest policy there is.
var fallbackPolicy = routePolicy{Level: levelAdmin, Key: keyDeny}

// policyFor returns the policy for a method and path. The most specific
// matching pattern wins (literal segments beat wildcards, and a longer pattern
// beats a shorter one), mirroring how net/http's mux picks a handler.
func policyFor(method, path string) routePolicy {
	best := fallbackPolicy
	bestScore := -1
	for _, p := range policies {
		if p.Method != "" && p.Method != method {
			continue
		}
		score, ok := matchPattern(p.Pattern, path)
		if !ok {
			continue
		}
		if p.Method != "" {
			score += 1000 // a method-specific rule beats a catch-all
		}
		if score > bestScore {
			best, bestScore = p, score
		}
	}
	return best
}

// matchPattern reports whether path matches a mux-style pattern and how
// specific the match is. Supported forms are literal segments, "{name}" for a
// single segment and a trailing "{name...}" for the rest of the path. A
// pattern ending in "/" matches that prefix.
func matchPattern(pattern, path string) (score int, ok bool) {
	if pattern == path {
		return 10000, true
	}
	if strings.HasSuffix(pattern, "/") && !strings.Contains(pattern, "{") {
		if strings.HasPrefix(path, pattern) {
			return len(pattern), true
		}
		return 0, false
	}
	// A trailing slash is a segment of its own to net/http's mux: /api/nodes/
	// does not reach "GET /api/nodes", it falls through to the /api/ catch-all
	// and 404s. Matching it here would authorize against a route that is never
	// going to run — harmless today, but the kind of drift this table exists
	// to avoid. A "{rest...}" pattern is the exception: it swallows the slash.
	if strings.HasSuffix(path, "/") && !strings.Contains(pattern, "...}") {
		return 0, false
	}
	pp := strings.Split(strings.Trim(pattern, "/"), "/")
	sp := strings.Split(strings.Trim(path, "/"), "/")
	for i, seg := range pp {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "...}") {
			return score, len(sp) >= i // the rest of the path, possibly empty
		}
		if i >= len(sp) {
			return 0, false
		}
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			score++ // a wildcard segment counts for less than a literal one
			continue
		}
		if seg != sp[i] {
			return 0, false
		}
		score += 10
	}
	if len(sp) != len(pp) {
		return 0, false
	}
	return score, true
}

// routeSpec is a route as it was registered on the mux. Handler() records
// every registration so the tests can prove the policy table and the router
// describe the same surface.
type routeSpec struct {
	Method  string
	Pattern string
}

func (r routeSpec) String() string {
	if r.Method == "" {
		return r.Pattern
	}
	return r.Method + " " + r.Pattern
}

// route registers a handler on the mux and records it.
func (s *Server) route(mux *http.ServeMux, pattern string, fn http.HandlerFunc) {
	method, p, found := strings.Cut(pattern, " ")
	if !found {
		method, p = "", pattern
	}
	s.routes = append(s.routes, routeSpec{Method: method, Pattern: p})
	mux.HandleFunc(pattern, fn)
}

// Routes returns the routes registered by the most recent Handler() call.
func (s *Server) Routes() []routeSpec { return s.routes }

// denialMessage explains a 403 in words the person reading it can act on.
func denialMessage(p auth.Principal, pol routePolicy, path string) string {
	if p.Kind == auth.KindAPIKey {
		switch pol.Key {
		case keyDeny:
			return fmt.Sprintf("API keys cannot use %s; sign in as an administrator in the web interface.", path)
		case keyWrite:
			return "This API key is read-only."
		}
	}
	if pol.Level == levelUser {
		return "This action needs a signed-in user account."
	}
	return fmt.Sprintf("This action needs an administrator account (%s).", p.Describe())
}

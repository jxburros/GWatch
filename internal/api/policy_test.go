package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The authorization table has to be consulted before the router runs, so
// policyFor and matchPattern reimplement net/http's pattern matching. Two
// implementations of the same rules drift, and drift here means authorizing a
// request against one route while another one serves it. This test puts the
// two side by side: the same paths go to policyFor and to a real
// http.ServeMux registered with the very patterns Handler() registers, and
// the answers have to line up.
//
// Where they cannot line up — the mux redirects instead of serving, or it
// matches on the escaped path while policyFor sees the decoded one — the test
// still holds the policy to being no *more* permissive than the route that
// would eventually run. That direction is the one that matters: a policy that
// is stricter than the route costs a caller an error message, a policy that is
// laxer than the route costs the owner their monitor.
func TestPolicyMatcherAgreesWithServeMux(t *testing.T) {
	_, srv := newTestServer(t)

	// The mux is built from the recorded registrations rather than from a
	// second hand-written list, so the two cannot be brought out of step by
	// adding a route. /hook/ and /ingest/ carry their own tokens and are
	// answered before the policy table is consulted at all (see
	// accessControl), so they are left out of both sides.
	mux := http.NewServeMux()
	patterns := 0
	for _, r := range srv.Routes() {
		if !strings.HasPrefix(r.Pattern, "/api/") {
			continue
		}
		mux.Handle(r.String(), http.NotFoundHandler())
		patterns++
	}
	if patterns < 60 {
		t.Fatalf("only %d /api patterns registered; is Handler() still routing through s.route?", patterns)
	}

	cases := []struct {
		method, path string
		why          string
	}{
		// The ordinary shapes, so a wholesale mismatch cannot hide behind the
		// interesting ones.
		{"GET", "/api/health", "a public literal"},
		{"GET", "/api/settings", "an admin literal"},
		{"PUT", "/api/settings", "same path, a different method"},
		{"GET", "/api/nodes", "a collection"},
		{"POST", "/api/nodes", "the same collection, written to"},
		{"GET", "/api/nodes/42", "a wildcard segment"},
		{"DELETE", "/api/nodes/42", "a wildcard segment, written to"},
		{"POST", "/api/nodes/42/silence", "a wildcard followed by a literal"},
		{"GET", "/api/history/multi", "a literal that extends another literal"},
		{"GET", "/api/backups/daily.gwb/download", "a wildcard holding a filename"},
		{"GET", "/api/export/history.csv", "a dotted literal"},
		{"GET", "/api/wallboards/7/view", "a wildcard between two literals"},

		// Method mismatches: the mux falls to the /api/ catch-all, and so must
		// the table, or a route would be authorized as its read-only twin.
		{"POST", "/api/health", "a public route with the wrong method"},
		{"DELETE", "/api/settings", "an admin route with a method it has not got"},
		{"PATCH", "/api/nodes/42", "a method nothing registers"},
		{"GET", "/api/nodes/42/silence", "a write route read instead"},

		// Nothing registered at all.
		{"GET", "/api/brand-new-thing", "an unknown route"},
		{"POST", "/api/nodes/42/brand-new-action", "an unknown action on a known node"},
		{"GET", "/api/history/multi/extra", "one segment too many"},
		{"GET", "/api/nodes/42/1/2/3", "several segments too many"},
		{"GET", "/api/", "the catch-all itself"},

		// Trailing slashes. To the mux a trailing slash is a segment of its
		// own, so none of these reaches the route it looks like.
		{"GET", "/api/health/", "a public literal with a trailing slash"},
		{"GET", "/api/settings/", "an admin literal with a trailing slash"},
		{"GET", "/api/nodes/", "a collection with a trailing slash"},
		{"GET", "/api/nodes/42/", "a wildcard with a trailing slash"},

		// Case. Paths are matched literally, so a different case is a
		// different — and unregistered — route.
		{"GET", "/api/SETTINGS", "mixed case"},
		{"GET", "/api/Health", "mixed case on a public route"},

		// Percent-encoding that decodes to an ordinary character: both sides
		// end up looking at the same segments.
		{"GET", "/api/%73ettings", "an escaped letter in a literal segment"},
		{"GET", "/api/nodes/%34%32", "an escaped wildcard value"},

		// The versioned alias. versionAlias rewrites the path before either
		// the router or the table sees it, which is exactly what the harness
		// below reproduces.
		{"GET", "/api/v1/health", "the alias of a public route"},
		{"GET", "/api/v1/nodes/42", "the alias of a wildcard route"},
		{"POST", "/api/v1/settings", "the alias with a wrong method"},
		{"GET", "/api/v1/", "the alias of the catch-all"},

		// Paths the mux cleans and redirects rather than serves.
		{"GET", "//api/settings", "a doubled leading slash"},
		{"GET", "/api//settings", "a doubled slash in the middle"},
		{"GET", "/api/./settings", "a dot segment"},
		{"GET", "/api/nodes/../settings", "a dot-dot segment climbing to another route"},
		{"GET", "/api/nodes/../../api/health", "a dot-dot segment climbing out and back"},
		{"GET", "/api", "the catch-all without its slash"},

		// %2F: the mux matches on the escaped path, so this stays one segment
		// to it and becomes two to anything reading URL.Path.
		{"GET", "/api/backups/a%2Fb/download", "an escaped slash inside a wildcard"},
		{"GET", "/api/hosts/a%2Fb/history", "an escaped slash inside a viewer route"},
	}

	for _, c := range cases {
		muxPattern, served := resolveThroughMux(t, mux, c.method, c.path)
		polPattern, pol := resolveThroughPolicy(t, c.method, c.path)

		if served && muxPattern == polPattern {
			continue // the two agree; nothing to explain
		}

		// They differ. The only acceptable reason is that the route the mux
		// found will not actually run this request (it redirects first), or
		// that the two are looking at different spellings of the path. Either
		// way the policy must not be the laxer of the two.
		muxPol := policyOfPattern(muxPattern)
		if !atLeastAsStrict(pol, muxPol) {
			t.Errorf("%s %s (%s): policy says %q (%v, %v) but the router serves %q (%v, %v) — the policy is the laxer of the two",
				c.method, c.path, c.why, polPattern, pol.Level, pol.Key, muxPattern, muxPol.Level, muxPol.Key)
			continue
		}
		if served {
			// A served request whose patterns disagree is only excusable when
			// the router and URL.Path see different segments, which in
			// practice means an escaped slash.
			if !strings.Contains(strings.ToLower(c.path), "%2f") {
				t.Errorf("%s %s (%s): policy resolved %q but the router serves %q, and nothing about the path explains the difference",
					c.method, c.path, c.why, polPattern, muxPattern)
			}
		}
	}
}

// resolveThroughMux reports which pattern the mux picks for a request and
// whether that pattern will actually serve it. A path the mux cleans is
// answered with a redirect, so the pattern it names is the one the *next*
// request would reach, not this one.
func resolveThroughMux(t *testing.T, mux *http.ServeMux, method, path string) (pattern string, served bool) {
	t.Helper()
	var got string
	var redirected bool
	versionAlias(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, p := mux.Handler(r)
		got = p
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		// Any redirect means this request is not served by the pattern named:
		// the mux answered 301 for a cleaned path up to Go 1.25 and 307 from
		// Go 1.26, and the difference is not one this test cares about.
		redirected = rec.Code >= 300 && rec.Code <= 399
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, path, nil))
	return got, !redirected
}

// resolveThroughPolicy answers the same question from the authorization table,
// through the same alias rewriting the server applies.
func resolveThroughPolicy(t *testing.T, method, path string) (pattern string, pol routePolicy) {
	t.Helper()
	versionAlias(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pol = policyFor(r.Method, r.URL.Path)
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, path, nil))
	if pol.Pattern == "" {
		return "", pol // nothing matched; the fallback applies
	}
	if pol.Method == "" {
		return pol.Pattern, pol
	}
	return pol.Method + " " + pol.Pattern, pol
}

// policyOfPattern returns the table row for a pattern as the mux spells it.
// A pattern with no row falls to the same closed default policyFor uses.
func policyOfPattern(pattern string) routePolicy {
	method, p, found := strings.Cut(pattern, " ")
	if !found {
		method, p = "", pattern
	}
	for _, row := range policies {
		if row.Method == method && row.Pattern == p {
			return row
		}
	}
	return fallbackPolicy
}

// atLeastAsStrict reports whether a is no more permissive than b: it demands
// at least as much standing and allows an API key at most as much.
func atLeastAsStrict(a, b routePolicy) bool { return a.Level >= b.Level && a.Key <= b.Key }

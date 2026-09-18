package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jxburros/GWatch/internal/auth"
	"github.com/jxburros/GWatch/internal/model"
)

// ---- helpers ----

// creds describes how one request identifies itself.
type creds struct {
	Cookie   string // session cookie value
	APIKey   string // sent as Authorization: Bearer
	Basic    string // legacy access password
	Remote   string // override the client address (e.g. a LAN client)
	KeyInHdr bool   // send the key in X-API-Key instead of Authorization
}

// as performs a request with the given credentials and returns status, body
// and headers. It uses the raw http client so cookies are never shared
// implicitly between callers.
func as(t *testing.T, ts *httptest.Server, c creds, method, path string, body any, out any) (int, string, http.Header) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, ts.URL+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Cookie != "" {
		req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: c.Cookie})
	}
	if c.APIKey != "" {
		if c.KeyInHdr {
			req.Header.Set("X-API-Key", c.APIKey)
		} else {
			req.Header.Set("Authorization", "Bearer "+c.APIKey)
		}
	}
	if c.Basic != "" {
		req.SetBasicAuth("gwatch", c.Basic)
	}
	if c.Remote != "" {
		req.Header.Set("X-Test-Remote", c.Remote)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if out != nil && resp.StatusCode < 300 {
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("%s %s: bad json %q: %v", method, path, data, err)
		}
	}
	return resp.StatusCode, string(data), resp.Header
}

// remoteHook lets a test pretend a loopback connection came from elsewhere.
// The header is only honoured because the test sets the hook: the production
// server reads r.RemoteAddr and nothing else.
func allowTestRemote(srv *Server) {
	srv.remoteAddrOverride = func(r *http.Request) string {
		return r.Header.Get("X-Test-Remote")
	}
}

// bootstrapAdmin creates the first admin over loopback and signs in.
func bootstrapAdmin(t *testing.T, ts *httptest.Server, name, pw string) string {
	t.Helper()
	if code, body, _ := as(t, ts, creds{}, "POST", "/api/users", map[string]string{"username": name, "password": pw, "role": "admin"}, nil); code != 201 {
		t.Fatalf("create admin: %d %s", code, body)
	}
	return login(t, ts, name, pw)
}

func login(t *testing.T, ts *httptest.Server, name, pw string) string {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.URL+"/api/auth/login", strings.NewReader(fmt.Sprintf(`{"username":%q,"password":%q}`, name, pw)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("login %s: %d %s", name, resp.StatusCode, b)
	}
	for _, c := range resp.Cookies() {
		if c.Name == auth.SessionCookie && c.Value != "" {
			if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
				t.Fatalf("session cookie flags: %+v", c)
			}
			if c.Secure {
				t.Fatal("Secure must not be set on a plain-HTTP connection")
			}
			return c.Value
		}
	}
	t.Fatal("no session cookie returned")
	return ""
}

// mintKey creates an API key as the given admin and returns the secret.
func mintKey(t *testing.T, ts *httptest.Server, admin, name, scope string) string {
	t.Helper()
	var out struct {
		Key    string       `json:"key"`
		APIKey model.APIKey `json:"apiKey"`
	}
	code, body, _ := as(t, ts, creds{Cookie: admin}, "POST", "/api/apikeys", map[string]string{"name": name, "scope": scope}, &out)
	if code != 201 || out.Key == "" {
		t.Fatalf("mint key: %d %s", code, body)
	}
	if out.APIKey.Scope != scope || out.APIKey.Prefix != out.Key[:auth.KeyPrefixLen] {
		t.Fatalf("minted key: %+v", out.APIKey)
	}
	return out.Key
}

func newNode(name string) map[string]any {
	return map[string]any{"name": name, "host": "example.com", "enabled": true, "checks": []map[string]any{}}
}

// ---- 1. a fresh install stays usable with no setup ----

func TestFreshInstallLoopbackIsAdminRemoteIsNot(t *testing.T) {
	ts, srv := newTestServer(t)
	allowTestRemote(srv)

	var me model.Principal
	if code, body, hdr := as(t, ts, creds{}, "GET", "/api/me", nil, &me); code != 200 {
		t.Fatalf("me: %d %s", code, body)
	} else if hdr.Get("X-GWatch-API-Version") != "1" {
		t.Fatalf("version header: %q", hdr.Get("X-GWatch-API-Version"))
	}
	if me.Kind != "local" || !me.IsAdmin || me.SignedIn {
		t.Fatalf("loopback principal: %+v", me)
	}
	// Admin work needs no sign-in at all on this computer.
	if code, body, _ := as(t, ts, creds{}, "POST", "/api/nodes", newNode("Router"), nil); code != 201 {
		t.Fatalf("loopback create node: %d %s", code, body)
	}
	if code, _, _ := as(t, ts, creds{}, "GET", "/api/settings", nil, nil); code != 200 {
		t.Fatal("loopback should read settings")
	}

	// A client from elsewhere on the network is anonymous and gets 401.
	for _, path := range []string{"/api/nodes", "/api/overview", "/api/settings", "/api/users"} {
		code, body, hdr := as(t, ts, creds{Remote: "192.168.1.50:4444"}, "GET", path, nil, nil)
		if code != 401 || !strings.Contains(body, "sign in required") {
			t.Fatalf("%s from LAN: %d %s", path, code, body)
		}
		// No access password and no accounts: nothing to challenge with.
		if hdr.Get("WWW-Authenticate") != "" {
			t.Fatalf("%s: unexpected basic-auth challenge", path)
		}
	}
	// but the public routes stay reachable
	for _, path := range []string{"/api/health", "/api/version", "/api/me", "/api/auth/setup"} {
		if code, body, _ := as(t, ts, creds{Remote: "192.168.1.50:4444"}, "GET", path, nil, nil); code != 200 {
			t.Fatalf("public %s: %d %s", path, code, body)
		}
	}
	var setup struct {
		UsersConfigured   bool `json:"usersConfigured"`
		LoginRequired     bool `json:"loginRequired"`
		AccessPasswordSet bool `json:"accessPasswordSet"`
	}
	as(t, ts, creds{Remote: "192.168.1.50:4444"}, "GET", "/api/auth/setup", nil, &setup)
	if setup.UsersConfigured || !setup.LoginRequired || setup.AccessPasswordSet {
		t.Fatalf("setup doc: %+v", setup)
	}
}

// A page someone merely visits must not be able to make their browser change
// things on 127.0.0.1, where no credential is needed.
func TestLocalPrincipalRejectsCrossSiteWrites(t *testing.T) {
	ts, _ := newTestServer(t)
	post := func(origin string) (int, string) {
		b, _ := json.Marshal(newNode("Router"))
		req, _ := http.NewRequest("POST", ts.URL+"/api/nodes", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}

	if code, body := post("https://evil.example.com"); code != 403 || !strings.Contains(body, "another website") {
		t.Fatalf("cross-site write: %d %s", code, body)
	}
	// Same origin is fine, and so is a client that sends no Origin at all
	// (curl, a script) — those are not browsers being driven by a third party.
	if code, body := post(ts.URL); code != 201 {
		t.Fatalf("same-origin write: %d %s", code, body)
	}
	if code, body := post(""); code != 201 {
		t.Fatalf("write with no Origin: %d %s", code, body)
	}
	// Reads are unaffected.
	req, _ := http.NewRequest("GET", ts.URL+"/api/nodes", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("cross-site read: %d", resp.StatusCode)
	}
}

// ---- 2. the viewer role ----

func TestViewerCanReadAndCannotWrite(t *testing.T) {
	ts, srv := newTestServer(t)
	allowTestRemote(srv)
	admin := bootstrapAdmin(t, ts, "pat", "correct horse battery")

	// The first account is forced to admin even when "viewer" is asked for.
	var users []model.User
	as(t, ts, creds{Cookie: admin}, "GET", "/api/users", nil, &users)
	if len(users) != 1 || users[0].Role != "admin" {
		t.Fatalf("first account: %+v", users)
	}

	if code, body, _ := as(t, ts, creds{Cookie: admin}, "POST", "/api/users", map[string]string{"username": "sam", "password": "viewer password", "role": "viewer"}, nil); code != 201 {
		t.Fatalf("create viewer: %d %s", code, body)
	}
	viewer := login(t, ts, "sam", "viewer password")

	var me model.Principal
	as(t, ts, creds{Cookie: viewer}, "GET", "/api/me", nil, &me)
	if me.Kind != "user" || me.Name != "sam" || me.Role != "viewer" || me.IsAdmin || me.CanWrite || !me.SignedIn {
		t.Fatalf("viewer principal: %+v", me)
	}

	// A node to look at, created by the admin.
	var node nodeDoc
	as(t, ts, creds{Cookie: admin}, "POST", "/api/nodes", newNode("Router"), &node)

	// Reads a viewer is entitled to, from off this machine.
	remote := creds{Cookie: viewer, Remote: "192.168.1.60:5000"}
	for _, path := range []string{
		"/api/overview", "/api/status", "/api/wallboard", "/api/nodes",
		fmt.Sprintf("/api/nodes/%d", node.ID), "/api/events", "/api/history/multi?auto=1&range=24h",
		"/api/dashboards", "/api/charts", "/api/maintenance", "/api/groups", "/api/templates",
		"/api/logs", "/api/triggers", "/api/endpoints", "/api/export/events.csv",
	} {
		if code, body, _ := as(t, ts, remote, "GET", path, nil, nil); code != 200 {
			t.Errorf("viewer GET %s: %d %s", path, code, body)
		}
	}

	// Writes and administration are refused, with a message that says why.
	denied := []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/nodes", newNode("Sneaky")},
		{"PUT", fmt.Sprintf("/api/nodes/%d", node.ID), newNode("Renamed")},
		{"DELETE", fmt.Sprintf("/api/nodes/%d", node.ID), nil},
		{"POST", fmt.Sprintf("/api/nodes/%d/run", node.ID), nil},
		{"POST", fmt.Sprintf("/api/nodes/%d/silence", node.ID), map[string]int{"minutes": 5}},
		{"PUT", "/api/settings", map[string]any{}},
		{"GET", "/api/settings", nil},
		{"GET", "/api/backups", nil},
		{"POST", "/api/backups", map[string]any{"password": "pw"}},
		{"POST", "/api/triggers/1/run", nil},
		{"POST", "/api/endpoints/1/run", nil},
		{"POST", "/api/actions/test", map[string]any{}},
		{"GET", "/api/export/config.json", nil},
		{"GET", "/api/users", nil},
		{"POST", "/api/users", map[string]string{"username": "mole", "password": "password123", "role": "admin"}},
		{"GET", "/api/apikeys", nil},
		{"POST", "/api/apikeys", map[string]string{"name": "k"}},
		{"POST", "/api/update/check", nil},
		{"POST", "/api/retention/run", nil},
		{"POST", "/api/events/note", map[string]string{"text": "hi"}},
	}
	for _, d := range denied {
		code, body, _ := as(t, ts, creds{Cookie: viewer}, d.method, d.path, d.body, nil)
		if code != 403 {
			t.Errorf("viewer %s %s: expected 403, got %d %s", d.method, d.path, code, body)
			continue
		}
		if !strings.Contains(body, "administrator account") || !strings.Contains(body, "viewer") || !strings.Contains(body, "sam") {
			t.Errorf("viewer %s %s: unhelpful message %s", d.method, d.path, body)
		}
	}

	// The node survived every attempt.
	var nodes []nodeDoc
	as(t, ts, creds{Cookie: admin}, "GET", "/api/nodes", nil, &nodes)
	if len(nodes) != 1 || nodes[0].Name != "Router" {
		t.Fatalf("viewer changed data: %+v", nodes)
	}
}

func TestEndpointTokenIsRedactedForNonAdmins(t *testing.T) {
	ts, srv := newTestServer(t)
	allowTestRemote(srv)
	admin := bootstrapAdmin(t, ts, "pat", "correct horse battery")
	if code, body, _ := as(t, ts, creds{Cookie: admin}, "POST", "/api/endpoints",
		map[string]any{"name": "Deploy", "slug": "deploy", "enabled": true, "method": "POST", "token": "supersecrettoken", "action": map[string]any{"type": "http", "url": "https://example.com/x", "method": "POST"}}, nil); code != 200 {
		t.Fatalf("create endpoint: %d %s", code, body)
	}
	as(t, ts, creds{Cookie: admin}, "POST", "/api/users", map[string]string{"username": "sam", "password": "viewer password", "role": "viewer"}, nil)
	viewer := login(t, ts, "sam", "viewer password")

	var list []endpointDoc
	if code, body, _ := as(t, ts, creds{Cookie: viewer}, "GET", "/api/endpoints", nil, &list); code != 200 || len(list) != 1 {
		t.Fatalf("viewer endpoints: %d %s", code, body)
	}
	if list[0].Token != "" || !list[0].HasToken {
		t.Fatalf("token must be redacted for a viewer: %+v", list[0])
	}
	var adminList []endpointDoc
	as(t, ts, creds{Cookie: admin}, "GET", "/api/endpoints", nil, &adminList)
	if adminList[0].Token != "supersecrettoken" || !adminList[0].HasToken {
		t.Fatalf("admin must see the token: %+v", adminList[0])
	}
}

// ---- 3. API key scopes ----

// writeRoutes is every route a readwrite key is meant to reach, and denyRoutes
// is every route no key may reach whatever its scope. Both are used by the
// read-only test (which expects 403 on all of them) and the readwrite test.
func writeRoutes(nodeID, checkID int64) []struct {
	Method, Path string
	Body         any
} {
	return []struct {
		Method, Path string
		Body         any
	}{
		{"POST", "/api/nodes", newNode("From a key")},
		{"POST", fmt.Sprintf("/api/nodes/%d/enable", nodeID), map[string]bool{"enabled": true}},
		{"POST", fmt.Sprintf("/api/nodes/%d/run", nodeID), nil},
		{"POST", fmt.Sprintf("/api/nodes/%d/silence", nodeID), map[string]int{"minutes": 5}},
		{"POST", fmt.Sprintf("/api/nodes/%d/duplicate", nodeID), nil},
		{"POST", fmt.Sprintf("/api/checks/%d/run", checkID), nil},
		{"POST", fmt.Sprintf("/api/checks/%d/enable", checkID), map[string]bool{"enabled": true}},
		{"POST", fmt.Sprintf("/api/checks/%d/silence", checkID), map[string]int{"minutes": 5}},
		{"POST", "/api/checks/test", map[string]any{"check": map[string]any{"type": "http", "config": map[string]any{"target": "https://example.com"}}}},
		// The rename drops the node's checks, so it comes after the check routes.
		{"PUT", fmt.Sprintf("/api/nodes/%d", nodeID), newNode("Renamed by a key")},
		{"POST", "/api/events/note", map[string]string{"text": "from a key"}},
		{"POST", "/api/maintenance", map[string]any{"name": "W", "enabled": true, "startAt": "2030-01-01T00:00:00Z", "endAt": "2030-01-01T01:00:00Z"}},
		{"POST", "/api/dashboards", map[string]any{"name": "D", "widgets": []any{}}},
		{"PUT", "/api/charts", []any{}},
	}
}

var denyRoutes = []struct {
	Method, Path string
	Body         any
}{
	{"GET", "/api/settings", nil},
	{"PUT", "/api/settings", map[string]any{}},
	{"POST", "/api/settings/test-email", map[string]any{}},
	{"GET", "/api/network", nil},
	{"GET", "/api/backups", nil},
	{"POST", "/api/backups", map[string]any{"password": "pw"}},
	{"POST", "/api/backups/restore-existing", map[string]any{"fileName": "x", "password": "p"}},
	{"GET", "/api/export/config.json", nil},
	{"GET", "/api/logs", nil},
	{"GET", "/api/export/logs.txt", nil},
	{"GET", "/api/triggers", nil},
	{"POST", "/api/triggers", map[string]any{}},
	{"POST", "/api/triggers/1/run", nil},
	{"GET", "/api/endpoints", nil},
	{"POST", "/api/endpoints", map[string]any{}},
	{"POST", "/api/endpoints/1/run", nil},
	{"POST", "/api/actions/test", map[string]any{}},
	{"GET", "/api/automation/meta", nil},
	{"GET", "/api/update/status", nil},
	{"POST", "/api/update/check", nil},
	{"POST", "/api/update/apply", nil},
	{"GET", "/api/retention/status", nil},
	{"POST", "/api/retention/run", nil},
	{"GET", "/api/users", nil},
	{"POST", "/api/users", map[string]string{"username": "mole", "password": "password123"}},
	{"DELETE", "/api/users/1", nil},
	{"GET", "/api/apikeys", nil},
	{"POST", "/api/apikeys", map[string]string{"name": "second"}},
	{"DELETE", "/api/apikeys/1", nil},
	{"POST", "/api/auth/change-password", map[string]string{"current": "a", "new": "b"}},
}

func TestReadOnlyAPIKey(t *testing.T) {
	ts, srv := newTestServer(t)
	allowTestRemote(srv)
	admin := bootstrapAdmin(t, ts, "pat", "correct horse battery")
	var node nodeDoc
	as(t, ts, creds{Cookie: admin}, "POST", "/api/nodes",
		map[string]any{"name": "Router", "host": "example.com", "enabled": true, "checks": []map[string]any{{"type": "ping", "intervalSeconds": 60}}}, &node)

	key := mintKey(t, ts, admin, "Dashboard on my phone", "read")
	remote := creds{APIKey: key, Remote: "203.0.113.7:9000"}

	var me model.Principal
	as(t, ts, remote, "GET", "/api/me", nil, &me)
	if me.Kind != "apikey" || me.IsAdmin || me.CanWrite || me.Scope != "read" || me.Name != "Dashboard on my phone" {
		t.Fatalf("key principal: %+v", me)
	}

	// Reading the monitoring data is the whole point of the key.
	for _, path := range []string{
		"/api/overview", "/api/status", "/api/wallboard", "/api/nodes",
		fmt.Sprintf("/api/nodes/%d", node.ID), "/api/events", "/api/dashboards", "/api/charts",
		fmt.Sprintf("/api/history?checkId=%d&range=24h", node.Checks[0].ID),
		fmt.Sprintf("/api/checks/%d/results", node.Checks[0].ID),
		"/api/export/history.csv?checkId=1&range=24h", "/api/health", "/api/version",
	} {
		if code, body, _ := as(t, ts, remote, "GET", path, nil, nil); code != 200 {
			t.Errorf("read key GET %s: %d %s", path, code, body)
		}
	}
	// The key also works in X-API-Key.
	if code, _, _ := as(t, ts, creds{APIKey: key, KeyInHdr: true}, "GET", "/api/overview", nil, nil); code != 200 {
		t.Error("X-API-Key header should work too")
	}

	// Every write is refused, and the message says the key is read-only.
	for _, d := range writeRoutes(node.ID, node.Checks[0].ID) {
		code, body, _ := as(t, ts, remote, d.Method, d.Path, d.Body, nil)
		if code != 403 {
			t.Errorf("read key %s %s: expected 403, got %d %s", d.Method, d.Path, code, body)
			continue
		}
		if !strings.Contains(body, "read-only") {
			t.Errorf("read key %s %s: unhelpful message %s", d.Method, d.Path, body)
		}
	}
	// And so is everything on the deny list, scope or no scope.
	for _, d := range denyRoutes {
		code, body, _ := as(t, ts, remote, d.Method, d.Path, d.Body, nil)
		if code != 403 {
			t.Errorf("read key %s %s: expected 403, got %d %s", d.Method, d.Path, code, body)
		} else if !strings.Contains(body, "API keys cannot") {
			t.Errorf("read key %s %s: unhelpful message %s", d.Method, d.Path, body)
		}
	}
	if len(denyRoutes) < 20 {
		t.Fatalf("the deny list should cover the whole admin surface, got %d routes", len(denyRoutes))
	}
}

func TestReadWriteAPIKey(t *testing.T) {
	ts, srv := newTestServer(t)
	allowTestRemote(srv)
	admin := bootstrapAdmin(t, ts, "pat", "correct horse battery")
	var node nodeDoc
	as(t, ts, creds{Cookie: admin}, "POST", "/api/nodes",
		map[string]any{"name": "Router", "host": "example.com", "enabled": true, "checks": []map[string]any{{"type": "ping", "intervalSeconds": 60}}}, &node)

	key := mintKey(t, ts, admin, "Home Assistant", "readwrite")
	c := creds{APIKey: key, Remote: "203.0.113.8:9000"}

	var me model.Principal
	as(t, ts, c, "GET", "/api/me", nil, &me)
	if me.Kind != "apikey" || me.IsAdmin || !me.CanWrite || me.Scope != "readwrite" {
		t.Fatalf("readwrite key principal: %+v", me)
	}

	for _, d := range writeRoutes(node.ID, node.Checks[0].ID) {
		if code, body, _ := as(t, ts, c, d.Method, d.Path, d.Body, nil); code >= 400 {
			t.Errorf("readwrite key %s %s: %d %s", d.Method, d.Path, code, body)
		}
	}
	// Deleting a node works too (done last so the fixtures above survive).
	var created nodeDoc
	as(t, ts, c, "POST", "/api/nodes", newNode("Temporary"), &created)
	if code, body, _ := as(t, ts, c, "DELETE", fmt.Sprintf("/api/nodes/%d", created.ID), nil, nil); code != 200 {
		t.Fatalf("readwrite key delete: %d %s", code, body)
	}

	// Administration stays out of reach whatever the scope.
	for _, d := range denyRoutes {
		code, body, _ := as(t, ts, c, d.Method, d.Path, d.Body, nil)
		if code != 403 || !strings.Contains(body, "API keys cannot") {
			t.Errorf("readwrite key %s %s: expected a 403 refusal, got %d %s", d.Method, d.Path, code, body)
		}
	}
}

// ---- 4. bad credentials and rate limiting ----

func TestRevokedAndUnknownKeysAreRejected(t *testing.T) {
	ts, srv := newTestServer(t)
	allowTestRemote(srv)
	admin := bootstrapAdmin(t, ts, "pat", "correct horse battery")
	key := mintKey(t, ts, admin, "Temporary", "read")

	if code, _, _ := as(t, ts, creds{APIKey: key}, "GET", "/api/overview", nil, nil); code != 200 {
		t.Fatal("the key should work before it is revoked")
	}
	var keys []model.APIKey
	as(t, ts, creds{Cookie: admin}, "GET", "/api/apikeys", nil, &keys)
	if len(keys) != 1 || keys[0].LastUsedAt == nil {
		t.Fatalf("key listing: %+v", keys)
	}
	if code, body, _ := as(t, ts, creds{Cookie: admin}, "DELETE", fmt.Sprintf("/api/apikeys/%d", keys[0].ID), nil, nil); code != 200 {
		t.Fatalf("revoke: %d %s", code, body)
	}
	if code, body, _ := as(t, ts, creds{APIKey: key, Remote: "203.0.113.9:1"}, "GET", "/api/overview", nil, nil); code != 401 {
		t.Fatalf("revoked key: %d %s", code, body)
	}
	if code, _, _ := as(t, ts, creds{APIKey: "gw_thiskeyneverexistedatallreally", Remote: "203.0.113.10:1"}, "GET", "/api/overview", nil, nil); code != 401 {
		t.Fatalf("unknown key: %d", code)
	}
	// The revoked key is still listed, so the audit trail keeps its name.
	as(t, ts, creds{Cookie: admin}, "GET", "/api/apikeys", nil, &keys)
	if len(keys) != 1 || keys[0].RevokedAt == nil {
		t.Fatalf("revoked key should stay listed: %+v", keys)
	}
}

func TestLoginRateLimit(t *testing.T) {
	ts, srv := newTestServer(t)
	allowTestRemote(srv)
	bootstrapAdmin(t, ts, "pat", "correct horse battery")

	var got429 bool
	for i := 0; i < failureLimit+3; i++ {
		code, body, hdr := as(t, ts, creds{Remote: "198.51.100.5:1234"}, "POST", "/api/auth/login", map[string]string{"username": "pat", "password": "nope"}, nil)
		switch code {
		case 401:
			if strings.Contains(body, "no such user") {
				t.Fatal("the refusal must not say whether the account exists")
			}
		case 429:
			got429 = true
			if hdr.Get("Retry-After") == "" {
				t.Fatal("429 should carry Retry-After")
			}
		default:
			t.Fatalf("attempt %d: %d %s", i, code, body)
		}
	}
	if !got429 {
		t.Fatalf("expected a 429 within %d attempts", failureLimit+3)
	}
	// A different address is not affected by someone else's failures.
	if code, _, _ := as(t, ts, creds{Remote: "198.51.100.6:1234"}, "POST", "/api/auth/login", map[string]string{"username": "pat", "password": "nope"}, nil); code != 401 {
		t.Fatalf("another client should still get a plain 401, got %d", code)
	}
	// The failures are in the audit log.
	var events []model.Event
	as(t, ts, creds{}, "GET", "/api/events?type=auth", nil, &events)
	var failures int
	for _, e := range events {
		if strings.Contains(e.Title, "Sign-in failed") && strings.Contains(e.Actor, "198.51.100.5") {
			failures++
		}
	}
	if failures == 0 {
		t.Fatal("failed sign-ins should be audited with the client address")
	}
}

func TestSessionLifecycle(t *testing.T) {
	ts, srv := newTestServer(t)
	allowTestRemote(srv)
	admin := bootstrapAdmin(t, ts, "pat", "correct horse battery")

	if code, _, _ := as(t, ts, creds{Cookie: admin, Remote: "192.168.1.9:1"}, "GET", "/api/settings", nil, nil); code != 200 {
		t.Fatal("a signed-in admin should reach settings from anywhere")
	}
	if code, _, _ := as(t, ts, creds{Cookie: admin}, "POST", "/api/auth/logout", nil, nil); code != 200 {
		t.Fatal("logout")
	}
	if code, _, _ := as(t, ts, creds{Cookie: admin, Remote: "192.168.1.9:1"}, "GET", "/api/settings", nil, nil); code != 401 {
		t.Fatal("the session should be gone after signing out")
	}
	// A forged cookie is simply not a session.
	if code, _, _ := as(t, ts, creds{Cookie: "not-a-real-token", Remote: "192.168.1.9:1"}, "GET", "/api/settings", nil, nil); code != 401 {
		t.Fatal("a forged cookie must not authenticate")
	}

	// Changing your own password works and ends the session.
	admin = login(t, ts, "pat", "correct horse battery")
	if code, body, _ := as(t, ts, creds{Cookie: admin}, "POST", "/api/auth/change-password", map[string]string{"current": "wrong", "new": "a longer password"}, nil); code != 401 {
		t.Fatalf("wrong current password: %d %s", code, body)
	}
	if code, body, _ := as(t, ts, creds{Cookie: admin}, "POST", "/api/auth/change-password", map[string]string{"current": "correct horse battery", "new": "short"}, nil); code != 400 {
		t.Fatalf("short new password: %d %s", code, body)
	}
	if code, body, _ := as(t, ts, creds{Cookie: admin}, "POST", "/api/auth/change-password", map[string]string{"current": "correct horse battery", "new": "a much longer password"}, nil); code != 200 {
		t.Fatalf("change password: %d %s", code, body)
	}
	if code, _, _ := as(t, ts, creds{Cookie: admin, Remote: "192.168.1.9:1"}, "GET", "/api/settings", nil, nil); code != 401 {
		t.Fatal("changing the password must end the session")
	}
	login(t, ts, "pat", "a much longer password")
}

// ---- 5. last-admin protection and required local sign-in ----

func TestLastAdminCannotBeRemoved(t *testing.T) {
	ts, srv := newTestServer(t)
	allowTestRemote(srv)
	admin := bootstrapAdmin(t, ts, "pat", "correct horse battery")
	var users []model.User
	as(t, ts, creds{Cookie: admin}, "GET", "/api/users", nil, &users)
	patID := users[0].ID

	for _, attempt := range []struct {
		method string
		body   any
	}{
		{"DELETE", nil},
		{"PUT", map[string]string{"role": "viewer"}},
	} {
		code, body, _ := as(t, ts, creds{Cookie: admin}, attempt.method, fmt.Sprintf("/api/users/%d", patID), attempt.body, nil)
		if code != 400 || !strings.Contains(body, "only administrator") {
			t.Fatalf("%s last admin: %d %s", attempt.method, code, body)
		}
	}

	// With a second admin, both operations become possible.
	as(t, ts, creds{Cookie: admin}, "POST", "/api/users", map[string]string{"username": "sam", "password": "another password", "role": "admin"}, nil)
	if code, body, _ := as(t, ts, creds{Cookie: admin}, "PUT", fmt.Sprintf("/api/users/%d", patID), map[string]string{"role": "viewer"}, nil); code != 200 {
		t.Fatalf("demote with a second admin present: %d %s", code, body)
	}
	// Demoting ends the account's sessions, so the old cookie is now useless.
	if code, _, _ := as(t, ts, creds{Cookie: admin, Remote: "192.168.1.9:1"}, "GET", "/api/users", nil, nil); code != 403 && code != 401 {
		t.Fatalf("a demoted admin must lose admin access, got %d", code)
	}
	sam := login(t, ts, "sam", "another password")
	if code, body, _ := as(t, ts, creds{Cookie: sam}, "DELETE", fmt.Sprintf("/api/users/%d", patID), nil, nil); code != 200 {
		t.Fatalf("delete a viewer: %d %s", code, body)
	}
}

func TestRequireLoginLocally(t *testing.T) {
	ts, srv := newTestServer(t)
	allowTestRemote(srv)

	// With no accounts, the setting cannot lock anyone out.
	st := srv.Engine.Settings()
	st.General.RequireLoginLocally = true
	if err := srv.Store.SaveSettings(t.Context(), st); err != nil {
		t.Fatal(err)
	}
	if err := srv.Engine.ReloadConfig(t.Context()); err != nil {
		t.Fatal(err)
	}
	if code, body, _ := as(t, ts, creds{}, "GET", "/api/settings", nil, nil); code != 200 {
		t.Fatalf("with no accounts, loopback must stay admin: %d %s", code, body)
	}

	// Once an account exists it takes effect, and loopback has to sign in.
	admin := bootstrapAdmin(t, ts, "pat", "correct horse battery")
	if code, body, _ := as(t, ts, creds{}, "GET", "/api/settings", nil, nil); code != 401 {
		t.Fatalf("loopback should now need to sign in: %d %s", code, body)
	}
	var me model.Principal
	as(t, ts, creds{}, "GET", "/api/me", nil, &me)
	if me.Kind != "" || me.IsAdmin {
		t.Fatalf("loopback principal with local login required: %+v", me)
	}
	if code, _, _ := as(t, ts, creds{Cookie: admin}, "GET", "/api/settings", nil, nil); code != 200 {
		t.Fatal("signing in should restore access")
	}
}

// ---- 6. versioning and the policy table ----

func TestVersionedPrefixAndHeader(t *testing.T) {
	ts, _ := newTestServer(t)
	var v struct {
		Version    string `json:"version"`
		APIVersion int    `json:"apiVersion"`
	}
	code, _, hdr := as(t, ts, creds{}, "GET", "/api/v1/version", nil, &v)
	if code != 200 || v.APIVersion != APIVersion || v.Version != "test" {
		t.Fatalf("/api/v1/version: %d %+v", code, v)
	}
	if hdr.Get("X-GWatch-API-Version") != "1" {
		t.Fatalf("version header on the alias: %q", hdr.Get("X-GWatch-API-Version"))
	}
	// A path with parameters works through the alias too.
	var node nodeDoc
	if code, body, _ := as(t, ts, creds{}, "POST", "/api/v1/nodes", newNode("Router"), &node); code != 201 {
		t.Fatalf("/api/v1/nodes: %d %s", code, body)
	}
	if code, _, _ := as(t, ts, creds{}, "GET", fmt.Sprintf("/api/v1/nodes/%d", node.ID), nil, nil); code != 200 {
		t.Fatal("/api/v1/nodes/{id}")
	}
	// The alias is authorized identically, not waved through.
	ts2, srv2 := newTestServer(t)
	allowTestRemote(srv2)
	if code, body, _ := as(t, ts2, creds{Remote: "192.168.1.30:1"}, "GET", "/api/v1/nodes", nil, nil); code != 401 {
		t.Fatalf("the alias must be authorized too: %d %s", code, body)
	}
	// Unknown paths under either prefix are 404 for an administrator.
	for _, p := range []string{"/api/nope", "/api/v1/nope"} {
		if code, _, _ := as(t, ts, creds{}, "GET", p, nil, nil); code != 404 {
			t.Errorf("%s: %d", p, code)
		}
	}
}

func TestEveryRouteHasAPolicyAndEveryPolicyHasARoute(t *testing.T) {
	_, srv := newTestServer(t)
	routes := srv.Routes()
	if len(routes) < 60 {
		t.Fatalf("only %d routes recorded; is Handler() still registering through s.route?", len(routes))
	}

	// 1. Every registered /api route is named by the policy table. A route
	//    added without a policy still fails closed (admin-only), but silently:
	//    this assertion is what makes the omission visible.
	declared := map[string]bool{}
	for _, p := range policies {
		declared[p.Method+" "+p.Pattern] = true
	}
	for _, r := range routes {
		if !strings.HasPrefix(r.Pattern, "/api/") {
			// /hook/… carries its own endpoint token and /ingest/… its own
			// agent token; neither goes through the policy table.
			continue
		}
		if !declared[r.Method+" "+r.Pattern] {
			t.Errorf("route %s has no entry in the policy table", r)
		}
	}

	// 2. And no policy names a route that does not exist, so the table cannot
	//    quietly keep granting access to something that was renamed.
	registered := map[string]bool{}
	for _, r := range routes {
		registered[r.Method+" "+r.Pattern] = true
	}
	for _, p := range policies {
		if !registered[p.Method+" "+p.Pattern] {
			t.Errorf("policy %s %s does not match any registered route", p.Method, p.Pattern)
		}
	}

	// 3. An unknown route falls to admin-only with API keys denied.
	for _, path := range []string{"/api/brand-new-thing", "/api/nodes/1/brand-new-action", "/something-else"} {
		pol := policyFor("POST", path)
		if pol.Level != levelAdmin || pol.Key != keyDeny {
			t.Errorf("unmatched %s resolved to %v/%v, must fail closed", path, pol.Level, pol.Key)
		}
	}

	// 4. Specific patterns win over the /api/ catch-all and over each other.
	for _, c := range []struct {
		method, path string
		want         level
	}{
		{"GET", "/api/nodes", levelViewer},
		{"POST", "/api/nodes", levelAdmin},
		{"GET", "/api/nodes/42", levelViewer},
		{"DELETE", "/api/nodes/42", levelAdmin},
		{"GET", "/api/health", levelPublic},
		{"GET", "/api/settings", levelAdmin},
		{"GET", "/api/history/multi", levelViewer},
		{"GET", "/api/backups/x.gwb/download", levelAdmin},
	} {
		if got := policyFor(c.method, c.path).Level; got != c.want {
			t.Errorf("policyFor(%s %s) = %v, want %v", c.method, c.path, got, c.want)
		}
	}
}

// ---- 7. audit attribution ----

func TestEventsRecordTheActor(t *testing.T) {
	ts, srv := newTestServer(t)
	allowTestRemote(srv)
	admin := bootstrapAdmin(t, ts, "pat", "correct horse battery")
	key := mintKey(t, ts, admin, "Home Assistant", "readwrite")

	as(t, ts, creds{Cookie: admin}, "POST", "/api/nodes", newNode("By the admin"), nil)
	as(t, ts, creds{APIKey: key}, "POST", "/api/nodes", newNode("By the key"), nil)
	as(t, ts, creds{}, "POST", "/api/nodes", newNode("By this computer"), nil)

	var events []model.Event
	as(t, ts, creds{Cookie: admin}, "GET", "/api/events?type=config_changed", nil, &events)
	want := map[string]string{
		"Added node By the admin":     "pat (admin)",
		"Added node By the key":       "api key Home Assistant (read-write)",
		"Added node By this computer": "local",
	}
	for _, e := range events {
		if actor, ok := want[e.Title]; ok {
			if e.Actor != actor {
				t.Errorf("%q recorded actor %q, want %q", e.Title, e.Actor, actor)
			}
			delete(want, e.Title)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing events: %v", want)
	}

	// Account and key management is audited as well.
	var authEvents []model.Event
	as(t, ts, creds{Cookie: admin}, "GET", "/api/events?type=auth", nil, &authEvents)
	seen := map[string]bool{}
	for _, e := range authEvents {
		seen[e.Title] = true
	}
	for _, title := range []string{"Account created: pat", "Signed in: pat", "API key created: Home Assistant"} {
		if !seen[title] {
			t.Errorf("expected an audit entry %q; got %v", title, seen)
		}
	}

	// Engine-originated events stay unattributed.
	ev := srv.Engine.RecordEvent(model.Event{Type: model.EventNote, Title: "From the scheduler"})
	if ev.Actor != "" {
		t.Fatalf("engine events must not claim an actor: %q", ev.Actor)
	}
}

func TestLegacyAccessPasswordStillAuthenticates(t *testing.T) {
	ts, srv := newTestServer(t)
	allowTestRemote(srv)
	st := srv.Engine.Settings()
	st.General.AccessPassword = "letmein"
	if err := srv.Store.SaveSettings(t.Context(), st); err != nil {
		t.Fatal(err)
	}
	if err := srv.Engine.ReloadConfig(t.Context()); err != nil {
		t.Fatal(err)
	}

	remote := "192.168.1.77:2222"
	if code, body, hdr := as(t, ts, creds{Remote: remote}, "GET", "/api/nodes", nil, nil); code != 401 || hdr.Get("WWW-Authenticate") == "" {
		t.Fatalf("expected a basic-auth challenge: %d %s", code, body)
	}
	if code, body, _ := as(t, ts, creds{Remote: remote, Basic: "letmein"}, "GET", "/api/nodes", nil, nil); code != 200 {
		t.Fatalf("correct password: %d %s", code, body)
	}
	// The password is an administrator, as it always was.
	if code, body, _ := as(t, ts, creds{Remote: remote, Basic: "letmein"}, "POST", "/api/nodes", newNode("From the LAN"), nil); code != 201 {
		t.Fatalf("password principal should be admin: %d %s", code, body)
	}
	if code, _, _ := as(t, ts, creds{Remote: remote, Basic: "wrong"}, "GET", "/api/nodes", nil, nil); code != 401 {
		t.Fatal("wrong password must be refused")
	}

	// Once accounts exist, the browser is not challenged any more: it gets the
	// sign-in screen instead. The password itself keeps working for scripts.
	bootstrapAdmin(t, ts, "pat", "correct horse battery")
	if _, _, hdr := as(t, ts, creds{Remote: remote}, "GET", "/api/nodes", nil, nil); hdr.Get("WWW-Authenticate") != "" {
		t.Fatal("no basic-auth challenge once accounts exist")
	}
	if code, _, _ := as(t, ts, creds{Remote: remote, Basic: "letmein"}, "GET", "/api/nodes", nil, nil); code != 200 {
		t.Fatal("the access password should keep working for existing scripts")
	}
}

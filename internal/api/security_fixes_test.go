package api

import (
	"strings"
	"testing"

	"github.com/jxburros/GWatch/internal/model"
)

// #86: a loopback request whose Host header names an external domain (as it
// does after DNS rebinding) must not be handed the no-sign-in local-admin
// principal, even though its Origin matches its Host.
func TestRebindingHostIsNotLocalAdmin(t *testing.T) {
	ts, _ := newTestServer(t)

	// Control: the same request with a loopback Host is the local admin and
	// may mint a key on a fresh install.
	if code, body, _ := as(t, ts, creds{}, "POST", "/api/apikeys",
		map[string]string{"name": "ok", "scope": "read"}, nil); code != 201 {
		t.Fatalf("loopback Host should be local admin: %d %s", code, body)
	}

	// Attack: Host (and Origin) say evil.example. The socket is still loopback,
	// but the Host is not this machine, so the request is anonymous and the
	// admin route refuses it.
	code, _, _ := as(t, ts, creds{Host: "evil.example:7230", Origin: "http://evil.example:7230"},
		"POST", "/api/apikeys", map[string]string{"name": "pwn", "scope": "readwrite"}, nil)
	if code == 201 {
		t.Fatalf("a rebound Host must not reach the admin API, got %d", code)
	}
	if code != 401 && code != 403 {
		t.Fatalf("expected 401/403 for a rebound Host, got %d", code)
	}
}

// #85: a read-write API key must not be able to create, edit or test a check
// that runs a command on the host, even though it may write ordinary checks.
func TestAPIKeyCannotCreateOrTestCodeCheck(t *testing.T) {
	ts, _ := newTestServer(t)
	admin := bootstrapAdmin(t, ts, "admin", "correct horse battery")
	key := mintKey(t, ts, admin, "integration", "readwrite")

	custom := map[string]any{"name": "Box", "host": "127.0.0.1", "enabled": true, "importance": "normal",
		"checks": []map[string]any{{"type": "custom", "name": "run", "enabled": true, "intervalSeconds": 60, "timeoutSeconds": 5,
			"config": map[string]any{"command": "echo message=up"}}}}

	// The key is refused the custom check on create…
	if code, _, _ := as(t, ts, creds{APIKey: key}, "POST", "/api/nodes", custom, nil); code != 403 {
		t.Fatalf("a key creating a custom check should be 403, got %d", code)
	}
	// …and on test.
	testBody := map[string]any{"nodeHost": "127.0.0.1", "check": map[string]any{"type": "custom", "name": "run",
		"intervalSeconds": 60, "timeoutSeconds": 5, "config": map[string]any{"command": "echo message=up"}}}
	if code, _, _ := as(t, ts, creds{APIKey: key}, "POST", "/api/checks/test", testBody, nil); code != 403 {
		t.Fatalf("a key testing a custom check should be 403, got %d", code)
	}

	// An ordinary check is still fine through the same key, so the gate is not
	// a blanket block on writing checks.
	ping := map[string]any{"name": "Gateway", "host": "127.0.0.1", "enabled": true, "importance": "normal",
		"checks": []map[string]any{{"type": "ping", "name": "Ping", "enabled": true, "intervalSeconds": 60, "timeoutSeconds": 5}}}
	if code, body, _ := as(t, ts, creds{APIKey: key}, "POST", "/api/nodes", ping, nil); code != 201 {
		t.Fatalf("a key writing a ping check should still work: %d %s", code, body)
	}

	// An administrator in the browser may create the custom check.
	if code, body, _ := as(t, ts, creds{Cookie: admin}, "POST", "/api/nodes", custom, nil); code != 201 {
		t.Fatalf("an admin creating a custom check should work: %d %s", code, body)
	}
}

// #87: a shared wallboard is read without signing in, so its view must be
// masked like every other read path — a community string must never appear.
func TestSharedWallboardViewMasksSecrets(t *testing.T) {
	ts, srv := newTestServer(t)
	allowTestRemote(srv)

	node := map[string]any{"name": "Switch", "host": "192.168.1.2", "enabled": true, "importance": "normal",
		"checks": []map[string]any{{"type": "snmp", "name": "SNMP", "enabled": true, "intervalSeconds": 60, "timeoutSeconds": 5,
			"config": map[string]any{"snmpVersion": "2c", "snmpPort": 161, "snmpCommunity": "s3cretCommunity",
				"snmpOids": []map[string]any{{"oid": "1.3.6.1.2.1.1.3.0", "name": "Uptime", "kind": "gauge"}}}}}}
	if code := call(t, ts, "POST", "/api/nodes", node, nil); code != 201 {
		t.Fatalf("create node: %d", code)
	}
	var b model.Wallboard
	call(t, ts, "POST", "/api/wallboards", map[string]any{"name": "Lobby"}, &b)
	var shared model.Wallboard
	if code := call(t, ts, "POST", "/api/wallboards/"+itoa(b.ID)+"/share", map[string]any{"enabled": true}, &shared); code != 200 {
		t.Fatalf("share: %d", code)
	}
	_, body := onTheNetwork(t, ts, "/api/wallboards/"+itoa(b.ID)+"/view?token="+shared.Share.Token)
	if strings.Contains(body, "s3cretCommunity") {
		t.Fatalf("the shared wallboard view leaked the community string:\n%s", body)
	}
	if !strings.Contains(body, passwordMask) {
		t.Fatalf("the community should come back masked, not stripped silently:\n%s", body)
	}
}

// #142: a read-only API key (the MCP companion's default) must not read a
// check's request headers, body or environment back — these routinely carry
// credentials. A person at the browser still sees them, so the editor works.
func TestAPIKeyReadStripsHeaderSecrets(t *testing.T) {
	ts, _ := newTestServer(t)
	admin := bootstrapAdmin(t, ts, "admin", "correct horse battery")
	key := mintKey(t, ts, admin, "assistant", "read")

	node := map[string]any{"name": "API", "host": "127.0.0.1", "enabled": true, "importance": "normal",
		"checks": []map[string]any{{"type": "http", "name": "Probe", "enabled": true, "intervalSeconds": 60, "timeoutSeconds": 5,
			"config": map[string]any{"target": "https://127.0.0.1/", "headers": map[string]string{"Authorization": "Bearer HDRSECRET"}}}}}
	if code, body, _ := as(t, ts, creds{Cookie: admin}, "POST", "/api/nodes", node, nil); code != 201 {
		t.Fatalf("create node: %d %s", code, body)
	}

	// The key cannot read the header back.
	if _, body, _ := as(t, ts, creds{APIKey: key}, "GET", "/api/nodes", nil, nil); strings.Contains(body, "HDRSECRET") {
		t.Fatalf("a read-only key read a check header back:\n%s", body)
	}
	if _, body, _ := as(t, ts, creds{APIKey: key}, "GET", "/api/overview", nil, nil); strings.Contains(body, "HDRSECRET") {
		t.Fatalf("the overview leaked a check header to a key:\n%s", body)
	}

	// Browser administrators also receive a mask; saving it preserves the stored value.
	if _, body, _ := as(t, ts, creds{Cookie: admin}, "GET", "/api/nodes", nil, nil); strings.Contains(body, "HDRSECRET") || !strings.Contains(body, passwordMask) {
		t.Fatalf("the admin editor must receive a header mask:\n%s", body)
	}
}

// #89: the failed-attempt budget is per credential kind, so spending down the
// sign-in budget must not block an API key, and a key's success must not hand
// the sign-in budget back to a password guesser.
func TestRateLimitIsPerCredentialKind(t *testing.T) {
	ts, _ := newTestServer(t)
	admin := bootstrapAdmin(t, ts, "admin", "correct horse battery")
	key := mintKey(t, ts, admin, "integration", "read")

	// Exhaust the sign-in budget with wrong passwords for the real account.
	limited := false
	for i := 0; i < failureLimit+3; i++ {
		code, _, _ := as(t, ts, creds{}, "POST", "/api/auth/login",
			map[string]string{"username": "admin", "password": "wrong"}, nil)
		if code == 429 {
			limited = true
			break
		}
		if code != 401 {
			t.Fatalf("attempt %d: expected 401, got %d", i, code)
		}
	}
	if !limited {
		t.Fatal("sign-in guessing should run out of budget")
	}

	// The API key, a different credential kind, is unaffected.
	if code, body, _ := as(t, ts, creds{APIKey: key}, "GET", "/api/overview", nil, nil); code != 200 {
		t.Fatalf("a valid key must work while sign-in is throttled: %d %s", code, body)
	}

	// That key success reset only the key bucket; sign-in is still throttled.
	if code, _, _ := as(t, ts, creds{}, "POST", "/api/auth/login",
		map[string]string{"username": "admin", "password": "wrong"}, nil); code != 429 {
		t.Fatalf("a key success must not refill the sign-in budget, got %d", code)
	}
}

// Guards against a copy/paste error in the limiter keys: each kind must hash to
// a distinct bucket for the same address.
func TestLimiterKeysAreDistinct(t *testing.T) {
	kinds := []string{limiterLogin, limiterAPIKey, limiterPassword, limiterHook, limiterPair, limiterIngest}
	seen := map[string]string{}
	for _, k := range kinds {
		key := limiterKey(k, "127.0.0.1")
		if prev, ok := seen[key]; ok {
			t.Fatalf("limiter kinds %q and %q share a bucket (%q)", prev, k, key)
		}
		seen[key] = k
	}
	if got := limiterIPKey("2001:db8::1"); !strings.HasSuffix(got, "/64") {
		t.Fatalf("IPv6 should be keyed on its /64, got %q", got)
	}
	if a, b := limiterIPKey("2001:db8::1"), limiterIPKey("2001:db8::ffff"); a != b {
		t.Fatalf("addresses in one /64 should share a bucket: %q vs %q", a, b)
	}
}

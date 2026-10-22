package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/auth"
	"github.com/jxburros/GWatch/internal/model"
)

// countVerifications replaces verifyPassword for one test with a wrapper that
// counts the calls, tracks how many run at once, and holds each for hold.
func countVerifications(t *testing.T, hold time.Duration) (calls, peak *atomic.Int64) {
	t.Helper()
	calls, peak = new(atomic.Int64), new(atomic.Int64)
	var active atomic.Int64
	real := verifyPassword
	verifyPassword = func(hash, pw string) error {
		calls.Add(1)
		n := active.Add(1)
		defer active.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(hold)
		return real(hash, pw)
	}
	t.Cleanup(func() { verifyPassword = real })
	return calls, peak
}

// Finding 7: once the budget for the current password is spent, a wrong
// current password is refused before any Argon2 work is done.
func TestChangePasswordLimitedBeforeVerify(t *testing.T) {
	ts, srv := newTestServer(t)
	allowTestRemote(srv)
	freezeLimiters(srv)
	bootstrapAdmin(t, ts, "pat", "correct horse battery")
	viewer := createAndLogin(t, ts, srv, "vic", "viewer password")

	calls, _ := countVerifications(t, 0)
	wrong := map[string]string{"current": "wrong", "new": "a much longer password"}
	limited := false
	for i := 0; i < failureLimit+3; i++ {
		code, body, hdr := as(t, ts, creds{Cookie: viewer, Remote: "198.51.100.20:1"}, "POST", "/api/auth/change-password", wrong, nil)
		if code == http.StatusTooManyRequests {
			if hdr.Get("Retry-After") == "" {
				t.Fatal("429 should carry Retry-After")
			}
			limited = true
			break
		}
		if code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d %s", i, code, body)
		}
	}
	if !limited {
		t.Fatal("wrong current passwords should run out of budget")
	}
	before := calls.Load()
	if before != failureLimit {
		t.Fatalf("%d verifications before the limit, want %d", before, failureLimit)
	}
	for i := 0; i < 5; i++ {
		if code, _, _ := as(t, ts, creds{Cookie: viewer, Remote: "198.51.100.20:1"}, "POST", "/api/auth/change-password", wrong, nil); code != http.StatusTooManyRequests {
			t.Fatalf("blocked attempt %d: %d", i, code)
		}
	}
	if got := calls.Load(); got != before {
		t.Fatalf("a blocked attempt still ran Argon2: %d verifications, want %d", got, before)
	}
	// The budget is the account's own sign-in budget: signing in as vic from
	// that address is held back too, while another account is not.
	if code, _, _ := as(t, ts, creds{Remote: "198.51.100.20:1"}, "POST", "/api/auth/login", map[string]string{"username": "vic", "password": "viewer password"}, nil); code != http.StatusTooManyRequests {
		t.Fatalf("sign-in as the throttled account: %d", code)
	}
	if code, _, _ := as(t, ts, creds{Remote: "198.51.100.20:1"}, "POST", "/api/auth/login", map[string]string{"username": "pat", "password": "correct horse battery"}, nil); code != http.StatusOK {
		t.Fatalf("sign-in as another account: %d", code)
	}
}

// Finding 7: password changes share login's verification slots, so however
// many arrive at once no more than cap(loginSlots) Argon2 checks run together.
func TestChangePasswordSharesVerificationSlots(t *testing.T) {
	ts, srv := newTestServer(t)
	allowTestRemote(srv)
	bootstrapAdmin(t, ts, "pat", "correct horse battery")
	viewer := createAndLogin(t, ts, srv, "vic", "viewer password")

	_, peak := countVerifications(t, 100*time.Millisecond)
	var wg sync.WaitGroup
	var busy atomic.Int64
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var code int
			if i%2 == 0 {
				code, _, _ = as(t, ts, creds{Cookie: viewer, Remote: fmt.Sprintf("198.51.100.%d:1", 30+i)}, "POST", "/api/auth/change-password", map[string]string{"current": "wrong", "new": "a much longer password"}, nil)
			} else {
				code, _, _ = as(t, ts, creds{Remote: fmt.Sprintf("198.51.100.%d:1", 30+i)}, "POST", "/api/auth/login", map[string]string{"username": "pat", "password": "wrong"}, nil)
			}
			switch code {
			case http.StatusTooManyRequests:
				busy.Add(1)
			case http.StatusUnauthorized:
			default:
				t.Errorf("request %d: %d", i, code)
			}
		}(i)
	}
	wg.Wait()
	if p := peak.Load(); p > int64(cap(srv.loginSlots)) {
		t.Fatalf("%d password checks ran at once; the cap is %d", p, cap(srv.loginSlots))
	}
	if busy.Load() == 0 {
		t.Fatal("eight simultaneous checks against two slots should turn some away")
	}
	if len(srv.loginSlots) != 0 {
		t.Fatal("verification slot leaked")
	}
}

// Finding 15: a good sign-in clears only its own account's failures. Guesses
// at pat alternated with sam's own successful sign-ins still run out.
func TestLoginSuccessDoesNotResetOtherAccounts(t *testing.T) {
	ts, srv := newTestServer(t)
	allowTestRemote(srv)
	freezeLimiters(srv)
	bootstrapAdmin(t, ts, "pat", "correct horse battery")
	createAndLogin(t, ts, srv, "sam", "sam's own password")

	from := creds{Remote: "198.51.100.40:1"}
	guess := map[string]string{"username": "pat", "password": "guess"}
	own := map[string]string{"username": "sam", "password": "sam's own password"}
	limited := false
	for i := 0; i < failureLimit+3; i++ {
		code, body, _ := as(t, ts, from, "POST", "/api/auth/login", guess, nil)
		if code == http.StatusTooManyRequests {
			limited = true
			break
		}
		if code != http.StatusUnauthorized {
			t.Fatalf("guess %d: %d %s", i, code, body)
		}
		if code, body, _ := as(t, ts, from, "POST", "/api/auth/login", own, nil); code != http.StatusOK {
			t.Fatalf("sam's sign-in %d: %d %s", i, code, body)
		}
	}
	if !limited {
		t.Fatal("sam's successful sign-ins must not keep handing back pat's budget")
	}
	// The failures against pat stay counted after one more success by sam.
	as(t, ts, from, "POST", "/api/auth/login", own, nil)
	if code, _, _ := as(t, ts, from, "POST", "/api/auth/login", guess, nil); code != http.StatusTooManyRequests {
		t.Fatalf("pat's budget came back after sam signed in: %d", code)
	}
	// pat's own failures from one address do not lock pat out everywhere.
	if code, _, _ := as(t, ts, creds{Remote: "198.51.100.41:1"}, "POST", "/api/auth/login", map[string]string{"username": "pat", "password": "correct horse battery"}, nil); code != http.StatusOK {
		t.Fatalf("pat from elsewhere: %d", code)
	}
	// And a correct sign-in clears pat's own budget at that address.
	srv.failLimiter.Reset(loginTargetKey("pat", "198.51.100.40"))
	if code, _, _ := as(t, ts, from, "POST", "/api/auth/login", map[string]string{"username": "PAT", "password": "correct horse battery"}, nil); code != http.StatusOK {
		t.Fatalf("pat after the reset: %d", code)
	}
}

// Finding 15: spraying guesses across many account names from one client
// meets the client-wide ceiling, which a successful sign-in does not clear.
func TestLoginSprayHitsSourceCeiling(t *testing.T) {
	ts, srv := newTestServer(t)
	allowTestRemote(srv)
	freezeLimiters(srv)
	bootstrapAdmin(t, ts, "pat", "correct horse battery")
	countVerifications(t, 0) // nothing to hold; keeps the real check

	from := creds{Remote: "198.51.100.50:1"}
	limitedAt := -1
	for i := 0; i < loginSourceLimit+5; i++ {
		if i == failureLimit {
			// A good sign-in midway changes nothing about the spray's budget.
			if code, _, _ := as(t, ts, from, "POST", "/api/auth/login", map[string]string{"username": "pat", "password": "correct horse battery"}, nil); code != http.StatusOK {
				t.Fatalf("pat's sign-in: %d", code)
			}
		}
		code, body, _ := as(t, ts, from, "POST", "/api/auth/login", map[string]string{"username": fmt.Sprintf("user%d", i), "password": "guess"}, nil)
		if code == http.StatusTooManyRequests {
			limitedAt = i
			break
		}
		if code != http.StatusUnauthorized {
			t.Fatalf("spray %d: %d %s", i, code, body)
		}
	}
	if limitedAt != loginSourceLimit {
		t.Fatalf("spray limited after %d names, want %d", limitedAt, loginSourceLimit)
	}
	// Another client is unaffected.
	if code, _, _ := as(t, ts, creds{Remote: "198.51.100.51:1"}, "POST", "/api/auth/login", map[string]string{"username": "user0", "password": "guess"}, nil); code != http.StatusUnauthorized {
		t.Fatalf("another client: %d", code)
	}
}

// Finding 16: behind a configured trusted proxy, two origins have budgets of
// their own; without the setting, or from a direct client, the forwarding
// header is ignored.
func TestTrustedProxyGivesOriginsTheirOwnBudget(t *testing.T) {
	ts, srv := newTestServer(t)
	allowTestRemote(srv)
	freezeLimiters(srv)
	bootstrapAdmin(t, ts, "pat", "correct horse battery")
	srv.TrustedProxies = mustProxies(t, "127.0.0.1")

	exhaust := func(c creds) {
		t.Helper()
		for i := 0; i < failureLimit; i++ {
			if code, body, _ := as(t, ts, c, "POST", "/api/auth/login", map[string]string{"username": "pat", "password": "nope"}, nil); code != http.StatusUnauthorized {
				t.Fatalf("attempt %d: %d %s", i, code, body)
			}
		}
		if code, _, _ := as(t, ts, c, "POST", "/api/auth/login", map[string]string{"username": "pat", "password": "nope"}, nil); code != http.StatusTooManyRequests {
			t.Fatalf("budget not spent: %d", code)
		}
	}

	// An attacker behind the proxy spends their own budget only.
	exhaust(creds{Remote: "127.0.0.1:5000", Proxied: "203.0.113.7"})
	if code, body, _ := as(t, ts, creds{Remote: "127.0.0.1:5001", Proxied: "203.0.113.8"}, "POST", "/api/auth/login", map[string]string{"username": "pat", "password": "correct horse battery"}, nil); code != http.StatusOK {
		t.Fatalf("a second origin behind the proxy was locked out: %d %s", code, body)
	}
	// Changing the header's left side does not buy a fresh budget: the proxy
	// appends the address it saw, and only that rightmost hop is believed.
	if code, _, _ := as(t, ts, creds{Remote: "127.0.0.1:5000", Proxied: "192.0.2.1, 203.0.113.7"}, "POST", "/api/auth/login", map[string]string{"username": "pat", "password": "nope"}, nil); code != http.StatusTooManyRequests {
		t.Fatalf("a client-written hop bought a fresh budget: %d", code)
	}

	// A direct client cannot pick an identity: its header is ignored, and
	// varying it never escapes the budget of its real address.
	direct := "198.51.100.60:1"
	for i := 0; i < failureLimit; i++ {
		as(t, ts, creds{Remote: direct, Proxied: fmt.Sprintf("203.0.113.%d", 100+i)}, "POST", "/api/auth/login", map[string]string{"username": "pat", "password": "nope"}, nil)
	}
	if code, _, _ := as(t, ts, creds{Remote: direct, Proxied: "203.0.113.250"}, "POST", "/api/auth/login", map[string]string{"username": "pat", "password": "nope"}, nil); code != http.StatusTooManyRequests {
		t.Fatalf("a direct client escaped its budget with X-Forwarded-For: %d", code)
	}
	// Nor can it borrow the budget of an origin behind the proxy.
	if code, _, _ := as(t, ts, creds{Remote: "198.51.100.61:1", Proxied: "203.0.113.7"}, "POST", "/api/auth/login", map[string]string{"username": "pat", "password": "correct horse battery"}, nil); code != http.StatusOK {
		t.Fatalf("a direct client was charged to the proxied origin it named: %d", code)
	}

	// The audit log names the origin, not the proxy.
	var events []model.Event
	as(t, ts, creds{}, "GET", "/api/events?type=auth", nil, &events)
	found := false
	for _, e := range events {
		if strings.Contains(e.Title, "Sign-in failed") && strings.Contains(e.Actor, "203.0.113.7") {
			found = true
		}
	}
	if !found {
		t.Fatal("failed sign-ins through the proxy should be audited with the origin's address")
	}
}

// Without --trusted-proxies, every client behind a proxy is the proxy, as
// before: the header is not believed from anyone.
func TestForwardedHeaderIgnoredByDefault(t *testing.T) {
	srv := &Server{}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:4000"
	r.Header.Set("X-Forwarded-For", "203.0.113.7")
	r.Header.Set("X-Real-Ip", "203.0.113.8")
	if got := srv.clientIP(r); got != "127.0.0.1" {
		t.Fatalf("clientIP = %q, want the socket peer", got)
	}
}

func TestClientIPFromTrustedProxy(t *testing.T) {
	srv := &Server{TrustedProxies: mustProxies(t, "127.0.0.1, ::1, 10.0.0.0/8")}
	for _, tc := range []struct {
		peer, xff, realIP, want string
	}{
		{"127.0.0.1:1", "203.0.113.7", "", "203.0.113.7"},
		{"[::1]:1", "203.0.113.7", "", "203.0.113.7"},
		// Rightmost untrusted hop; trusted hops to its right are skipped.
		{"127.0.0.1:1", "192.0.2.1, 203.0.113.7, 10.1.2.3", "", "203.0.113.7"},
		// Ports, brackets and IPv4-mapped forms name the same client.
		{"127.0.0.1:1", "203.0.113.7:5555", "", "203.0.113.7"},
		{"127.0.0.1:1", "[2001:db8::7]:5555", "", "2001:db8::7"},
		{"127.0.0.1:1", "::ffff:203.0.113.7", "", "203.0.113.7"},
		// X-Real-IP is the fallback when X-Forwarded-For is absent.
		{"127.0.0.1:1", "", "203.0.113.9", "203.0.113.9"},
		// Every hop trusted: the request began at a proxy.
		{"127.0.0.1:1", "10.0.0.5", "", "10.0.0.5"},
		// A chain that cannot be read, or no header, falls back to the peer.
		{"127.0.0.1:1", "203.0.113.7, not-an-ip", "", "127.0.0.1"},
		{"127.0.0.1:1", "", "", "127.0.0.1"},
		// A peer that is not a trusted proxy is never believed.
		{"198.51.100.1:1", "203.0.113.7", "203.0.113.8", "198.51.100.1"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tc.peer
		if tc.xff != "" {
			r.Header.Set("X-Forwarded-For", tc.xff)
		}
		if tc.realIP != "" {
			r.Header.Set("X-Real-Ip", tc.realIP)
		}
		if got := srv.clientIP(r); got != tc.want {
			t.Errorf("peer %s xff %q real %q: got %q, want %q", tc.peer, tc.xff, tc.realIP, got, tc.want)
		}
	}
}

// A forwarded request is never the local administrator, even from a trusted
// proxy on this machine and even when the client it names is loopback.
func TestTrustedProxyNeverLocal(t *testing.T) {
	srv := &Server{TrustedProxies: mustProxies(t, "127.0.0.1")}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:1"
	r.Header.Set("X-Forwarded-For", "127.0.0.1")
	if srv.isLocalClient(r) {
		t.Fatal("a forwarded request was treated as local")
	}
}

func TestParseTrustedProxies(t *testing.T) {
	got, err := ParseTrustedProxies(" 127.0.0.1, ::1  10.0.0.0/8,::ffff:192.168.0.0/112 ")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"127.0.0.1/32", "::1/128", "10.0.0.0/8", "192.168.0.0/16"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i].String() != want[i] {
			t.Fatalf("entry %d: %s, want %s", i, got[i], want[i])
		}
	}
	if got, err := ParseTrustedProxies(""); err != nil || len(got) != 0 {
		t.Fatalf("empty: %v %v", got, err)
	}
	for _, bad := range []string{"localhost", "10.0.0.0/33", "fe80::1%eth0", "1.2.3"} {
		if _, err := ParseTrustedProxies(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

// Finding 18: two administrators deleted at once through the API cannot both
// go; one request is refused with the last-administrator message.
func TestLastAdminRaceThroughAPI(t *testing.T) {
	ts, srv := newTestServer(t)
	allowTestRemote(srv)
	admin := bootstrapAdmin(t, ts, "pat", "correct horse battery")
	as(t, ts, creds{Cookie: admin}, "POST", "/api/users", map[string]string{"username": "sam", "password": "another password", "role": "admin"}, nil)
	var users []model.User
	as(t, ts, creds{Cookie: admin}, "GET", "/api/users", nil, &users)
	if len(users) != 2 {
		t.Fatalf("users: %+v", users)
	}
	codes := make([]int, 2)
	bodies := make([]string, 2)
	var wg sync.WaitGroup
	for i, u := range users {
		wg.Add(1)
		go func(i int, id int64) {
			defer wg.Done()
			codes[i], bodies[i], _ = as(t, ts, creds{}, "DELETE", fmt.Sprintf("/api/users/%d", id), nil, nil)
		}(i, u.ID)
	}
	wg.Wait()
	ok, refused := 0, 0
	for i, c := range codes {
		switch {
		case c == http.StatusOK:
			ok++
		case c == http.StatusBadRequest && strings.Contains(bodies[i], "only administrator"):
			refused++
		default:
			t.Fatalf("delete %d: %d %s", i, c, bodies[i])
		}
	}
	if ok != 1 || refused != 1 {
		t.Fatalf("%d deleted, %d refused; want one of each", ok, refused)
	}
}

// freezeLimiters swaps the sign-in limiters for ones with the same budgets
// over an hour instead of a minute, so the few seconds of real Argon2 a test
// spends do not refill a token and the counts it asserts are exact.
func freezeLimiters(srv *Server) {
	srv.failLimiter.Close()
	srv.loginSourceLimiter.Close()
	srv.failLimiter = auth.NewLimiter(failureLimit, time.Hour)
	srv.loginSourceLimiter = auth.NewLimiter(loginSourceLimit, time.Hour)
}

func mustProxies(t *testing.T, spec string) []netip.Prefix {
	t.Helper()
	p, err := ParseTrustedProxies(spec)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// createAndLogin adds an account through the API (as this computer's local
// administrator) and signs it in.
func createAndLogin(t *testing.T, ts *httptest.Server, srv *Server, name, pw string) string {
	t.Helper()
	role := "viewer"
	if code, body, _ := as(t, ts, creds{}, "POST", "/api/users", map[string]string{"username": name, "password": pw, "role": role}, nil); code != http.StatusCreated {
		t.Fatalf("create %s: %d %s", name, code, body)
	}
	return login(t, ts, name, pw)
}

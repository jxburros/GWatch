package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/auth"
	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
)

// APIVersion is the current version of the JSON API. Every /api response
// carries it in X-GWatch-API-Version, and /api/v1/… is its stable prefix.
const APIVersion = 1

// apiVersionPrefix is the versioned alias of /api.
const apiVersionPrefix = "/api/v1/"

const (
	// failureLimit is how many *failed* credential attempts one client IP may
	// make per failureWindow before it is asked to wait.
	failureLimit  = 10
	failureWindow = time.Minute
	// loginSourceLimit is how many sign-in attempts one client may make per
	// failureWindow across every account name together. failureLimit applies
	// to each (account name, client) pair and is cleared when that pair signs
	// in; this wider ceiling is never cleared by a success, so neither a
	// spray across many names nor a guesser's own good sign-ins between
	// guesses gets past it.
	loginSourceLimit = 30
	// remoteRequestLimit is the general per-IP ceiling for API-key traffic from
	// off this machine (ROADMAP 2.5 "rate limiting").
	remoteRequestLimit  = 300
	remoteRequestWindow = time.Minute
)

// ---- principal resolution ----

// clientIP is the address the limiters and the audit log key on. By default
// it is the socket peer, never a forwarded header: GWatch may sit behind a
// reverse proxy the user controls, but trusting X-Forwarded-For by default
// would let any caller invent its own identity and dodge the rate limits.
// When the peer is one of the configured TrustedProxies, the client that proxy
// names in its forwarding headers is used instead (see forwardedClient), so
// that everyone behind one proxy no longer shares one budget.
func (s *Server) clientIP(r *http.Request) string {
	addr := s.remoteAddr(r)
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if s.trustedProxy(host) {
		if client, ok := s.forwardedClient(r); ok {
			return client
		}
	}
	return host
}

// remoteAddr returns the request's remote address. Tests may substitute one
// through the unexported hook so that non-loopback behaviour can be exercised;
// production always reads r.RemoteAddr.
func (s *Server) remoteAddr(r *http.Request) string {
	if s.remoteAddrOverride != nil {
		if a := s.remoteAddrOverride(r); a != "" {
			return a
		}
	}
	return r.RemoteAddr
}

func (s *Server) isLoopback(r *http.Request) bool { return isLoopbackRemote(s.remoteAddr(r)) }

// Credential kinds for the failed-attempt limiter. Each kind keeps its own
// per-client budget so that a success of one kind — an agent reporting in, a
// read-only key, a tokenless hook — can no longer clear the failures another
// kind (sign-in, the access password, a pairing code) is accumulating. Before
// this, one shared bucket let anyone holding any valid credential reset the
// budget between guesses and brute-force the rest (#89).
const (
	limiterLogin    = "login"
	limiterAPIKey   = "apikey"
	limiterPassword = "password"
	limiterHook     = "hook"
	limiterPair     = "pair"
	limiterIngest   = "ingest"
)

// limiterKey namespaces a rate-limit bucket by credential kind and normalizes
// the client address, so an IPv6 client cannot draw a fresh budget from every
// address in a routed /64 (#89).
func limiterKey(kind, ip string) string {
	return kind + "|" + limiterIPKey(ip)
}

func limiterIPKey(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return ip
	}
	if v4 := parsed.To4(); v4 != nil {
		return v4.String()
	}
	return parsed.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

// loginTargetKey is the failed-attempt bucket for one account name tried from
// one client. Sign-in failures used to share a bucket per client, which any
// successful sign-in cleared: someone holding one account could alternate a
// guess at another account with a good sign-in of their own and never run out
// of guesses. Keyed by the name as well, a success clears only the budget of
// the account that signed in. The name is folded the way the store matches it
// (case-insensitively) and capped at the longest name an account can have,
// so a spray of long names cannot inflate the limiter's memory.
func loginTargetKey(username, ip string) string {
	name := strings.ToLower(strings.TrimSpace(username))
	if len(name) > 64 {
		name = name[:64]
	}
	// The address has no "|" in it, so putting the free-form name last keeps
	// every (name, address) pair a distinct key.
	return limiterLogin + "|" + limiterIPKey(ip) + "|" + name
}

// loginSourceKey is the bucket for every sign-in attempt from one client,
// whatever account name it tries; see loginSourceLimit.
func loginSourceKey(ip string) string {
	return limiterLogin + "|" + limiterIPKey(ip)
}

// admitPasswordCheck spends one attempt from both sign-in budgets before an
// account password is verified: the (account name, client) budget and the
// client-wide ceiling. It reports whether the check may go ahead and, if not,
// how long the client should wait. A refused attempt costs nothing, so the
// first budget's token is handed back when the second refuses.
func (s *Server) admitPasswordCheck(username, ip string) (bool, time.Duration) {
	target := loginTargetKey(username, ip)
	if ok, wait := s.failLimiter.Allow(target); !ok {
		return false, wait
	}
	if ok, wait := s.loginSourceLimiter.Allow(loginSourceKey(ip)); !ok {
		s.failLimiter.Refund(target)
		return false, wait
	}
	return true, 0
}

// passwordCheckPassed clears the (account name, client) budget after a correct
// password and hands back the attempt the client-wide ceiling was charged. It
// does not clear that ceiling: the failures before this success still count.
func (s *Server) passwordCheckPassed(username, ip string) {
	s.failLimiter.Reset(loginTargetKey(username, ip))
	s.loginSourceLimiter.Refund(loginSourceKey(ip))
}

// passwordCheckNotRun hands both tokens back when an admitted attempt never
// reached the password check (every verification slot was busy).
func (s *Server) passwordCheckNotRun(username, ip string) {
	s.failLimiter.Refund(loginTargetKey(username, ip))
	s.loginSourceLimiter.Refund(loginSourceKey(ip))
}

// verifyPassword is auth.VerifyPassword. It is a variable only so the tests
// can count and time the 64 MiB Argon2 checks the handlers start.
var verifyPassword = auth.VerifyPassword

// acquirePasswordSlot takes one of the loginSlots that bound how many Argon2
// verifications run at once, across sign-in and password changes alike. It
// never waits: when every slot is busy it answers 429 itself and returns
// false. On true the caller must call the returned release.
func (s *Server) acquirePasswordSlot(w http.ResponseWriter, busy string) (release func(), ok bool) {
	select {
	case s.loginSlots <- struct{}{}:
		return func() { <-s.loginSlots }, true
	default:
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusTooManyRequests, busy)
		return nil, false
	}
}

// presentedAPIKey returns the API key a request carries, if any.
func presentedAPIKey(r *http.Request) string {
	if k := strings.TrimSpace(r.Header.Get("X-API-Key")); k != "" {
		return k
	}
	if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(a, "Bearer "))
	}
	return ""
}

// authError is a credential failure that must be answered with a status other
// than "anonymous".
type authError struct {
	status int
	msg    string
	retry  time.Duration
}

func (e *authError) Error() string { return e.msg }

// resolvePrincipal works out who is calling, in a fixed order of precedence:
// API key, session cookie, legacy access password, trusted local client. The
// first credential a request presents decides: a session that is not
// recognised falls through to the ones below it, but one that could not be
// checked at all ends the resolution with a 503 (see lookupFailed). w is only
// written to when a session's cookie is renewed.
func (s *Server) resolvePrincipal(w http.ResponseWriter, r *http.Request) (auth.Principal, *authError) {
	ctx := r.Context()
	ip := s.clientIP(r)
	general := s.Engine.Settings().General

	// 1. API key.
	if raw := presentedAPIKey(r); raw != "" && auth.LooksLikeAPIKey(raw) {
		if ok, wait := s.failLimiter.Allow(limiterKey(limiterAPIKey, ip)); !ok {
			return auth.Anonymous, &authError{http.StatusTooManyRequests, "too many failed attempts; try again shortly", wait}
		}
		k, err := s.Store.LookupAPIKey(ctx, auth.HashToken(raw))
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			// The database did not answer. That is no verdict on the key, so
			// it is neither audited as a rejection nor answered with a 401.
			return auth.Anonymous, s.lookupFailed(ctx, "look up api key", err)
		}
		if err != nil {
			s.auditAuthFailure(ctx, "API key rejected", "An unknown or revoked API key was presented.", ip)
			return auth.Anonymous, &authError{http.StatusUnauthorized, "invalid API key", 0}
		}
		s.failLimiter.Reset(limiterKey(limiterAPIKey, ip))
		if !s.isLoopback(r) {
			if ok, wait := s.apiLimiter.Allow(ip); !ok {
				return auth.Anonymous, &authError{http.StatusTooManyRequests, "too many requests; slow down", wait}
			}
		}
		if err := s.Store.TouchAPIKey(ctx, k.ID); err != nil {
			s.Log.Errorf("touch api key: %v", err)
		}
		scope := auth.ParseScope(k.Scope)
		role := auth.RoleViewer
		if scope == auth.ScopeReadWrite {
			role = auth.RoleAdmin // only meaningful via CanWrite; IsAdmin stays false for keys
		}
		return auth.Principal{Kind: auth.KindAPIKey, Name: k.Name, Role: role, Scope: scope, KeyID: k.ID}, nil
	}

	// 2. Session cookie.
	if c, err := r.Cookie(auth.SessionCookie); err == nil && c.Value != "" {
		sess, err := s.Store.GetSession(ctx, auth.HashToken(c.Value), auth.SessionLifetime)
		switch {
		case err == nil:
			if sess.Renewed {
				// The session's expiry just slid forward; the cookie has to
				// follow it, or the browser drops the cookie on the date it
				// was first given however busy the session has been since.
				http.SetCookie(w, s.sessionCookie(r, c.Value, sess.ExpiresAt))
			}
			u := sess.User
			// The role is read from the account on every request, so a role
			// change applies to the sessions an account already holds at once.
			return auth.Principal{Kind: auth.KindUser, Name: u.Username, Role: auth.ParseRole(u.Role), UserID: u.ID}, nil
		case !errors.Is(err, store.ErrNotFound):
			// The database could not answer — busy, a dropped connection to
			// a server database. Falling through here would treat a browser
			// that is signed in as signed out: a 401, which takes the web
			// interface to the sign-in screen, or — on this computer — the
			// administrator principal below, whatever the account's role is.
			// Neither is true, so say what is: try again shortly.
			return auth.Anonymous, s.lookupFailed(ctx, "look up session", err)
		}
		// A stale or forged cookie is not an error by itself; fall through and
		// let the browser be treated as signed out.
	}

	// 3. Legacy LAN access password over HTTP basic auth. Kept working so that
	// installs that only ever set a password keep working after an upgrade.
	if pw := general.AccessPassword; pw != "" {
		if _, got, ok := r.BasicAuth(); ok {
			if allowed, wait := s.failLimiter.Allow(limiterKey(limiterPassword, ip)); !allowed {
				return auth.Anonymous, &authError{http.StatusTooManyRequests, "too many failed attempts; try again shortly", wait}
			}
			if subtle.ConstantTimeCompare([]byte(got), []byte(pw)) == 1 {
				s.failLimiter.Reset(limiterKey(limiterPassword, ip))
				return auth.Principal{Kind: auth.KindPassword, Name: "access password", Role: auth.RoleAdmin}, nil
			}
			s.auditAuthFailure(ctx, "Access password rejected", "A client supplied the wrong access password.", ip)
			return auth.Anonymous, &authError{http.StatusUnauthorized, "incorrect password", 0}
		}
	}

	// 4. A client on this computer. This is what keeps a fresh install and
	// every existing single-user install working with no setup at all. Once
	// accounts exist the owner can turn "require sign-in on this computer" on,
	// and then even loopback has to sign in.
	if s.isLocalClient(r) && s.hostHeaderLocal(r) {
		if !general.RequireLoginLocally {
			return auth.Principal{Kind: auth.KindLocal, Name: "this computer", Role: auth.RoleAdmin}, nil
		}
		n, err := s.Store.CountUsers(ctx)
		if err != nil {
			return auth.Anonymous, s.lookupFailed(ctx, "count users", err)
		}
		if n == 0 {
			// "Require sign-in" with no account to sign in to would be a
			// lockout, so it does not take effect until an account exists.
			return auth.Principal{Kind: auth.KindLocal, Name: "this computer", Role: auth.RoleAdmin}, nil
		}
	}

	return auth.Anonymous, nil
}

// lookupFailed is the answer when the database could not be asked who a
// request is. It is a 503 with a short Retry-After — something to try again,
// never a sign-out.
func (s *Server) lookupFailed(ctx context.Context, what string, err error) *authError {
	if ctx.Err() == nil { // a client that went away is not worth a log line
		s.Log.Errorf("%s: %v", what, err)
	}
	return &authError{http.StatusServiceUnavailable, "GWatch could not check who you are just now; try again in a moment", 2 * time.Second}
}

// forwardingHeaders are the headers a reverse proxy adds to say it is passing
// a request on for someone else. A browser never sends any of them itself.
var forwardingHeaders = []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-Ip"}

// isLocalClient reports whether a request comes from someone at this computer,
// which is what earns the no-sign-in administrator principal. A loopback peer
// is not enough on its own: a reverse proxy running alongside GWatch — the
// setup REMOTE-ACCESS.md describes, and what `tailscale serve` does — connects
// from 127.0.0.1 on behalf of whoever reached the proxy. Without this check
// every such visitor was an administrator with no sign-in at all, and a
// signed-in viewer became one the moment their session was not recognised.
// A proxied request says so in the headers it adds, and is then judged like
// any other remote client. Here the mere presence of a header is enough to
// refuse, whoever sent it and whatever TrustedProxies says: a forwarded request
// is never a local one, and a header can only ever take this principal away.
func (s *Server) isLocalClient(r *http.Request) bool {
	if !s.isLoopback(r) {
		return false
	}
	for _, h := range forwardingHeaders {
		if r.Header.Get(h) != "" {
			return false
		}
	}
	return true
}

// hostHeaderLocal reports whether the request's Host header names this machine
// rather than some external domain. A loopback peer earns the no-sign-in
// administrator principal, so the one lever a remote attacker keeps is the Host
// header: with DNS rebinding a page on evil.example can point the browser at
// 127.0.0.1 while Host and Origin both read "evil.example", which the Origin
// check alone cannot catch. An attacker-controlled domain never appears in the
// allowed set, so requiring it closes the rebinding path (#86). Loopback
// literals are always accepted — they are how a person actually reaches GWatch
// on this machine — and /wall and /hook do not pass through here.
func (s *Server) hostHeaderLocal(r *http.Request) bool {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.Trim(host, "[]"))
	if host == "" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() {
			return true
		}
		return s.localHostSet()[host]
	}
	if host == "localhost" {
		return true
	}
	return s.localHostSet()[host]
}

// localHostSet is the set of non-loopback names and addresses that still name
// this machine: its hostname (full and short), its own interface addresses and
// the host it is configured to listen on. It is built once — these do not
// change while the process runs — and consulted by hostHeaderLocal.
func (s *Server) localHostSet() map[string]bool {
	s.localHostsOnce.Do(func() {
		set := map[string]bool{"localhost": true}
		add := func(v string) {
			if v = strings.ToLower(strings.TrimSpace(v)); v != "" {
				set[v] = true
			}
		}
		if h, err := os.Hostname(); err == nil {
			add(h)
			if short, _, ok := strings.Cut(h, "."); ok {
				add(short)
			}
		}
		if addrs, err := net.InterfaceAddrs(); err == nil {
			for _, a := range addrs {
				if ipnet, ok := a.(*net.IPNet); ok {
					add(ipnet.IP.String())
				}
			}
		}
		if s.Network != nil {
			ni := s.Network()
			if h, _, err := net.SplitHostPort(ni.ListenAddress); err == nil && h != "0.0.0.0" && h != "::" {
				add(h)
			}
			add(ni.Hostname)
		}
		s.localHosts = set
	})
	return s.localHosts
}

// ---- middleware ----

// accessControl resolves a principal for every request and then authorizes it
// against the route policy table. It replaces the earlier "non-loopback needs
// the access password" check, which that flow is now one branch of.
func (s *Server) accessControl(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Custom endpoints carry their own token and are called by devices that
		// have no account; they are never subject to the sign-in. The hardware
		// ingest route is the same shape: an agent token identifies one
		// machine and permits one thing, submitting that machine's readings.
		if strings.HasPrefix(r.URL.Path, "/hook/") || strings.HasPrefix(r.URL.Path, "/ingest/") {
			next.ServeHTTP(w, r)
			return
		}
		isAPI := strings.HasPrefix(r.URL.Path, "/api/")
		if isAPI {
			w.Header().Set("X-GWatch-API-Version", strconv.Itoa(APIVersion))
		}

		p, aerr := s.resolvePrincipal(w, r)
		if aerr != nil && aerr.status == http.StatusServiceUnavailable && !isAPI {
			// The shell and its assets are what an anonymous browser gets
			// anyway, so a moment's trouble reading the database is no reason
			// to fail a page load over; the app's own /api/me asks again.
			p, aerr = auth.Anonymous, nil
		}
		if aerr != nil {
			if aerr.retry > 0 {
				w.Header().Set("Retry-After", strconv.Itoa(int(aerr.retry.Seconds()+0.999)))
			}
			s.deny(w, r, aerr.status, aerr.msg, isAPI)
			return
		}
		r = r.WithContext(auth.WithPrincipal(r.Context(), p))

		if !isAPI {
			// The UI shell is normally public: it is a static bundle that shows
			// a sign-in screen when the API says one is needed, and serving it
			// without credentials is what makes that screen reachable. An
			// install that still uses only the old access password has no
			// sign-in screen to show, so there the shell keeps its basic-auth
			// challenge exactly as before.
			if !p.Authenticated() && s.legacyPasswordOnly(r.Context()) {
				w.Header().Set("WWW-Authenticate", `Basic realm="GWatch", charset="UTF-8"`)
				s.deny(w, r, http.StatusUnauthorized, "password required", false)
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		// Cookies and cached basic auth accompany same-site requests from sibling
		// ports, so session users need the same Origin check as local clients.
		// API keys are explicit credentials and may cross a rewriting proxy.
		if r.Method != http.MethodGet && r.Method != http.MethodHead && ((p.Kind == auth.KindLocal || p.Kind == auth.KindPassword || p.Kind == auth.KindUser) && crossSite(r) || r.Header.Get("Sec-Fetch-Site") == "cross-site" || r.Header.Get("Sec-Fetch-Site") == "same-site" || r.Header.Get("Origin") == "null") {
			s.deny(w, r, http.StatusForbidden, "this request came from another website; open GWatch directly to make changes", true)
			return
		}

		pol := policyFor(r.Method, r.URL.Path)
		if err := authorize(p, pol, r.URL.Path); err != nil {
			if !p.Authenticated() {
				// Only offer basic auth where it is the configured mechanism,
				// so browsers do not pop a native dialog for account holders.
				if s.legacyPasswordOnly(r.Context()) {
					w.Header().Set("WWW-Authenticate", `Basic realm="GWatch", charset="UTF-8"`)
				}
				s.deny(w, r, http.StatusUnauthorized, "sign in required", true)
				return
			}
			s.deny(w, r, http.StatusForbidden, err.Error(), true)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// crossSite reports whether the request carries an Origin naming somewhere
// other than the host it was sent to. A request with no Origin at all (curl,
// a script, any non-browser client) is not cross-site.
func crossSite(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return true
	}
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return true // an Origin we cannot read is not one we can trust
	}
	return !strings.EqualFold(u.Host, r.Host)
}

// legacyPasswordOnly reports whether the install still authenticates purely
// with the old access password (no accounts created yet).
func (s *Server) legacyPasswordOnly(ctx context.Context) bool {
	if s.Engine.Settings().General.AccessPassword == "" {
		return false
	}
	n, err := s.Store.CountUsers(ctx)
	return err == nil && n == 0
}

// authorize applies one policy row to one principal.
func authorize(p auth.Principal, pol routePolicy, path string) error {
	if pol.Level == levelPublic {
		return nil
	}
	if !p.Authenticated() {
		return errors.New("sign in required")
	}
	if p.Kind == auth.KindAPIKey {
		// An API key is judged only by the route's key mode. It never inherits
		// the admin path below, whatever role was attached to it.
		switch pol.Key {
		case keyRead:
			return nil
		case keyWrite:
			if p.Scope == auth.ScopeReadWrite {
				return nil
			}
		}
		return errors.New(denialMessage(p, pol, path))
	}
	switch pol.Level {
	case levelViewer, levelUser:
		if pol.Level == levelUser && p.Kind != auth.KindUser {
			return errors.New(denialMessage(p, pol, path))
		}
		return nil
	default: // levelAdmin
		if p.IsAdmin() {
			return nil
		}
		return errors.New(denialMessage(p, pol, path))
	}
}

func (s *Server) deny(w http.ResponseWriter, r *http.Request, status int, msg string, isAPI bool) {
	if isAPI {
		writeError(w, status, msg)
		return
	}
	http.Error(w, "GWatch: "+msg, status)
}

// versionAlias lets every route be reached under /api/v1/… as well as /api/….
// The prefix is rewritten before the router and the policy table see it, so
// the two prefixes cannot drift apart.
func versionAlias(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, apiVersionPrefix) {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/api/" + strings.TrimPrefix(r.URL.Path, apiVersionPrefix)
			if r2.URL.RawPath != "" {
				r2.URL.RawPath = "/api/" + strings.TrimPrefix(r2.URL.RawPath, apiVersionPrefix)
			}
			r = r2
		} else if r.URL.Path == strings.TrimSuffix(apiVersionPrefix, "/") {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/api/"
			r = r2
		}
		next.ServeHTTP(w, r)
	})
}

// ---- audit helpers ----

// recordEvent records an event, attributing it to whoever made the request.
// Engine-originated events (check results, the scheduler) go through
// Engine.RecordEvent directly and stay unattributed.
func (s *Server) recordEvent(ctx context.Context, ev model.Event) model.Event {
	if ev.Actor == "" {
		ev.Actor = auth.FromContext(ctx).Label()
	}
	return s.Engine.RecordEvent(ev)
}

// auditAuth records a sign-in, sign-out or account change.
func (s *Server) auditAuth(ctx context.Context, title, detail string) {
	s.recordEvent(ctx, model.Event{Type: model.EventAuth, Title: title, Detail: detail})
}

// auditAuthFailure records a rejected credential together with the address it
// came from. It runs before a principal exists, so the actor is the address.
func (s *Server) auditAuthFailure(ctx context.Context, title, detail, ip string) {
	s.Engine.RecordEvent(model.Event{Type: model.EventAuth, Title: title, Detail: detail, Actor: "from " + ip})
}

// ---- auth endpoints ----

func (s *Server) principalDoc(ctx context.Context, p auth.Principal) model.Principal {
	st := s.Engine.Settings()
	g := st.General
	return model.Principal{
		Kind:        string(p.Kind),
		Name:        p.Name,
		Role:        string(p.Role),
		Scope:       string(p.Scope),
		UserID:      p.UserID,
		IsAdmin:     p.IsAdmin(),
		CanWrite:    p.CanWrite(),
		SignedIn:    p.Kind == auth.KindUser,
		Theme:       g.Theme,
		AccentColor: g.AccentColor,
		Indicators:  model.NormalizeIndicators(st.Indicators),
	}
}

// handleMe reports the identity behind the current request. It is public so
// that a signed-out browser can learn it is signed out (and pick up the theme)
// without a 401 round trip.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.principalDoc(r.Context(), auth.FromContext(r.Context())))
}

// handleAuthSetup tells the web interface what kind of sign-in this install
// uses, and whether this particular client needs one.
func (s *Server) handleAuthSetup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	n, err := s.Store.CountUsers(ctx)
	if err != nil {
		s.fail(w, err)
		return
	}
	g := s.Engine.Settings().General
	p := auth.FromContext(ctx)
	writeJSON(w, http.StatusOK, map[string]any{
		"usersConfigured":   n > 0,
		"loginRequired":     !p.Authenticated(),
		"accessPasswordSet": g.AccessPassword != "",
		"localLoginForced":  g.RequireLoginLocally,
		"apiVersion":        APIVersion,
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeDecodeError(w, err)
		return
	}
	ip := s.clientIP(r)
	username := strings.TrimSpace(body.Username)
	if ok, wait := s.admitPasswordCheck(username, ip); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds()+0.999)))
		writeError(w, http.StatusTooManyRequests, "too many sign-in attempts; try again shortly")
		return
	}
	// Bound Argon2 memory across all clients, including unknown usernames.
	release, ok := s.acquirePasswordSlot(w, "sign-in is busy; try again shortly")
	if !ok {
		s.passwordCheckNotRun(username, ip)
		return
	}
	defer release()
	u, hash, err := s.Store.GetUserByName(ctx, username)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		// The database did not answer: that is not a wrong password, so it
		// is neither audited nor reported as one.
		s.fail(w, err)
		return
	}
	if err != nil {
		// No such user: spend the same argon2 time a real account would, so
		// that an unknown name cannot be told from a wrong password by how
		// quickly it is rejected (#91).
		auth.SpendVerifyTime(body.Password)
	}
	if err != nil || verifyPassword(hash, body.Password) != nil {
		s.auditAuthFailure(ctx, "Sign-in failed", fmt.Sprintf("Wrong user name or password for %q.", username), ip)
		// One message for both cases: a different answer for "no such user"
		// would let anyone enumerate the account names.
		writeError(w, http.StatusUnauthorized, "incorrect user name or password")
		return
	}
	s.passwordCheckPassed(username, ip)

	token, err := auth.NewSessionToken()
	if err != nil {
		s.fail(w, err)
		return
	}
	expires := time.Now().Add(auth.SessionLifetime)
	if err := s.Store.CreateSession(ctx, auth.HashToken(token), u.ID, s.remoteAddr(r), expires); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Store.TouchUserLogin(ctx, u.ID); err != nil {
		s.Log.Errorf("record login: %v", err)
	}
	// Sign-ins are rare and bounded, which makes this the natural moment to
	// clear out sessions that have run out without needing a background job.
	if _, err := s.Store.PurgeExpiredSessions(ctx); err != nil {
		s.Log.Errorf("purge sessions: %v", err)
	}
	http.SetCookie(w, s.sessionCookie(r, token, expires))

	p := auth.Principal{Kind: auth.KindUser, Name: u.Username, Role: auth.ParseRole(u.Role), UserID: u.ID}
	s.recordEvent(auth.WithPrincipal(ctx, p), model.Event{Type: model.EventAuth, Title: "Signed in: " + u.Username, Detail: "From " + s.remoteAddr(r) + "."})
	writeJSON(w, http.StatusOK, s.principalDoc(ctx, p))
}

func (s *Server) sessionCookie(r *http.Request, token string, expires time.Time) *http.Cookie {
	c := &http.Cookie{
		Name:     auth.SessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  expires,
		// Secure is only set when GWatch itself is the end of the HTTPS
		// connection: it is normally served over plain HTTP on the LAN, and a
		// Secure cookie would never be sent back there. Behind a reverse
		// proxy that terminates TLS, GWatch sees plain HTTP and the cookie is
		// not marked Secure, deliberately without trusting X-Forwarded-Proto:
		// cookies are shared across schemes and ports on one host name, and a
		// Secure cookie set through https://host would be withheld from
		// http://host:7230 — which could then never replace it, since a
		// browser refuses to overwrite a Secure cookie from a plain-HTTP page,
		// so signing in there would appear to work and then bounce straight
		// back to the sign-in screen. The login and every renewal go through
		// here, so the cookie keeps the same attributes for its whole life.
		Secure: r.TLS != nil || s.Engine.Settings().General.BehindHTTPSProxy,
	}
	if token == "" {
		c.MaxAge = -1
	}
	return c
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if c, err := r.Cookie(auth.SessionCookie); err == nil && c.Value != "" {
		if err := s.Store.DeleteSession(ctx, auth.HashToken(c.Value)); err != nil {
			s.Log.Errorf("delete session: %v", err)
		}
	}
	if p := auth.FromContext(ctx); p.Kind == auth.KindUser {
		s.auditAuth(ctx, "Signed out: "+p.Name, "")
	}
	http.SetCookie(w, s.sessionCookie(r, "", time.Unix(0, 0)))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := auth.FromContext(ctx)
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeDecodeError(w, err)
		return
	}
	// Checking the current password is a sign-in by another name, so it spends
	// from the same budgets and takes one of the same verification slots, and
	// both are settled before any Argon2 work is done: a signed-in account
	// sending wrong passwords in parallel could otherwise start one 64 MiB
	// check per request, however many it had already got wrong.
	ip := s.clientIP(r)
	if ok, wait := s.admitPasswordCheck(p.Name, ip); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds()+0.999)))
		writeError(w, http.StatusTooManyRequests, "too many attempts; try again shortly")
		return
	}
	release, ok := s.acquirePasswordSlot(w, "GWatch is busy checking passwords; try again shortly")
	if !ok {
		s.passwordCheckNotRun(p.Name, ip)
		return
	}
	// Held until the new password is hashed too, which costs the same again.
	defer release()
	u, hash, err := s.Store.GetUserByName(ctx, p.Name)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := verifyPassword(hash, body.Current); err != nil {
		writeError(w, http.StatusUnauthorized, "the current password is not correct")
		return
	}
	s.passwordCheckPassed(p.Name, ip)
	if err := auth.ValidatePassword(body.New); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	newHash, err := auth.HashPassword(body.New)
	if err != nil {
		s.fail(w, err)
		return
	}
	// SetUserPassword also ends every session, including this one, so the
	// browser is asked to sign in again with the new password.
	if err := s.Store.SetUserPassword(ctx, u.ID, newHash); err != nil {
		s.fail(w, err)
		return
	}
	s.auditAuth(ctx, "Password changed: "+u.Username, "Every signed-in browser for this account was signed out.")
	http.SetCookie(w, s.sessionCookie(r, "", time.Unix(0, 0)))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- users ----

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.Store.ListUsers(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeDecodeError(w, err)
		return
	}
	if err := validateUsername(body.Username); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := auth.ValidatePassword(body.Password); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	role := auth.ParseRole(body.Role)
	n, err := s.Store.CountUsers(ctx)
	if err != nil {
		s.fail(w, err)
		return
	}
	if n == 0 {
		// The very first account has to be an administrator, otherwise the
		// install would have accounts but nobody able to manage them.
		role = auth.RoleAdmin
	}
	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		s.fail(w, err)
		return
	}
	u, err := s.Store.CreateUser(ctx, strings.TrimSpace(body.Username), hash, role)
	if err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		s.fail(w, err)
		return
	}
	detail := "Role: " + u.Role + "."
	if n == 0 {
		detail = "First account on this install; created as an administrator."
	}
	s.auditAuth(ctx, "Account created: "+u.Username, detail)
	writeJSON(w, http.StatusCreated, u)
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	u, err := s.Store.GetUser(ctx, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	var body struct {
		Role     *string `json:"role"`
		Password *string `json:"password"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeDecodeError(w, err)
		return
	}
	var changes []string
	if body.Role != nil {
		role := auth.ParseRole(*body.Role)
		if role != auth.ParseRole(u.Role) {
			// The store refuses to demote the last administrator, in the same
			// transaction as the change (see lastAdminError).
			if err := s.Store.UpdateUserRole(ctx, id, role); err != nil {
				if errors.Is(err, store.ErrLastAdmin) {
					writeError(w, http.StatusBadRequest, lastAdminError(u, "change the role of"))
					return
				}
				s.fail(w, err)
				return
			}
			// The sessions the account already holds are left alone. They
			// never carried a role of their own: every request reads it from
			// the account (see resolvePrincipal), so a demoted account loses
			// its standing on its very next request and a promoted one gains
			// it, and the web interface restyles itself when it next asks who
			// it is. Ending the sessions as well used to sign the person out
			// in the middle of whatever they were doing, for no gain.
			changes = append(changes, "role set to "+string(role))
		}
	}
	if body.Password != nil {
		if err := auth.ValidatePassword(*body.Password); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		hash, err := auth.HashPassword(*body.Password)
		if err != nil {
			s.fail(w, err)
			return
		}
		if err := s.Store.SetUserPassword(ctx, id, hash); err != nil {
			s.fail(w, err)
			return
		}
		changes = append(changes, "password reset")
	}
	updated, err := s.Store.GetUser(ctx, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if len(changes) > 0 {
		s.auditAuth(ctx, "Account updated: "+updated.Username, strings.Join(changes, ", ")+".")
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	u, err := s.Store.GetUser(ctx, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Store.DeleteUser(ctx, id); err != nil {
		if errors.Is(err, store.ErrLastAdmin) {
			writeError(w, http.StatusBadRequest, lastAdminError(u, "delete"))
			return
		}
		s.fail(w, err)
		return
	}
	s.auditAuth(ctx, "Account deleted: "+u.Username, "")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// lastAdminError explains the refusal of a change that would leave the
// install with no administrator — including an admin demoting themselves.
// The check itself lives in the store (UpdateUserRole, DeleteUser), inside the
// transaction that makes the change: counting first and writing afterwards let
// two requests removing two different administrators at once both pass.
func lastAdminError(u model.User, verb string) string {
	return fmt.Sprintf("you cannot %s %s: it is the only administrator account, and GWatch would have no one able to manage it", verb, u.Username)
}

func validateUsername(name string) error {
	name = strings.TrimSpace(name)
	if len(name) < 2 {
		return errors.New("the user name must be at least 2 characters")
	}
	if len(name) > 64 {
		return errors.New("the user name is too long")
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return errors.New("the user name contains a control character")
		}
	}
	return nil
}

// ---- API keys ----

func (s *Server) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.Store.ListAPIKeys(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, keys)
}

func (s *Server) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body struct {
		Name  string `json:"name"`
		Scope string `json:"scope"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeDecodeError(w, err)
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "give the key a name so you can recognise it later")
		return
	}
	if len(name) > 100 {
		writeError(w, http.StatusBadRequest, "the name is too long")
		return
	}
	// An omitted scope means read-only: granting write has to be deliberate.
	scope := auth.ParseScope(body.Scope)
	key, prefix, err := auth.NewAPIKey()
	if err != nil {
		s.fail(w, err)
		return
	}
	created, err := s.Store.CreateAPIKey(ctx, name, prefix, auth.HashToken(key), scope, auth.FromContext(ctx).Label())
	if err != nil {
		s.fail(w, err)
		return
	}
	s.auditAuth(ctx, "API key created: "+created.Name, "Scope: "+created.Scope+".")
	// The key itself is returned exactly once; only its digest is stored.
	writeJSON(w, http.StatusCreated, map[string]any{"key": key, "apiKey": created})
}

func (s *Server) handleRevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	k, err := s.Store.GetAPIKey(ctx, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Store.RevokeAPIKey(ctx, id); err != nil {
		s.fail(w, err)
		return
	}
	s.auditAuth(ctx, "API key revoked: "+k.Name, "It can no longer be used.")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

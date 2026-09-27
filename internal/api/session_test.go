package api

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/auth"
	"github.com/jxburros/GWatch/internal/model"
)

// Session and role consistency (#70): people were signed out when nothing
// had signed them out, and shown as viewers (or administrators) when their
// account said otherwise. These tests pin down the server's half of the fix;
// tests/web/identity.test.mjs covers the web interface's half.

// setAccessPassword turns the legacy LAN access password on.
func setAccessPassword(t *testing.T, srv *Server, pw string) {
	t.Helper()
	st := srv.Engine.Settings()
	st.General.AccessPassword = pw
	if err := srv.Store.SaveSettings(t.Context(), st); err != nil {
		t.Fatal(err)
	}
	if err := srv.Engine.ReloadConfig(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// Every way of calling GWatch is reported by /api/me with the standing it
// actually has, and the web interface styles itself from exactly these
// fields — so they have to agree with what the policy then allows.
func TestPrincipalReportsItsActualStanding(t *testing.T) {
	ts, srv := newTestServer(t)
	allowTestRemote(srv)
	admin := bootstrapAdmin(t, ts, "pat", "correct horse battery")
	if code, body, _ := as(t, ts, creds{Cookie: admin}, "POST", "/api/users", map[string]string{"username": "sam", "password": "viewer password", "role": "viewer"}, nil); code != 201 {
		t.Fatalf("create viewer: %d %s", code, body)
	}
	viewer := login(t, ts, "sam", "viewer password")
	readKey := mintKey(t, ts, admin, "Widget", "read")
	writeKey := mintKey(t, ts, admin, "Automation", "readwrite")
	setAccessPassword(t, srv, "letmein")

	const lan = "192.168.1.60:5000"
	for _, tc := range []struct {
		name     string
		c        creds
		kind     string
		role     string
		admin    bool
		write    bool
		signedIn bool
	}{
		{"this computer", creds{}, "local", "admin", true, true, false},
		{"admin from the LAN", creds{Cookie: admin, Remote: lan}, "user", "admin", true, true, true},
		{"admin on this computer", creds{Cookie: admin}, "user", "admin", true, true, true},
		// A signed-in account is itself, even on the machine GWatch runs on:
		// the session is resolved before the loopback shortcut.
		{"viewer on this computer", creds{Cookie: viewer}, "user", "viewer", false, false, true},
		{"viewer from the LAN", creds{Cookie: viewer, Remote: lan}, "user", "viewer", false, false, true},
		{"anonymous from the LAN", creds{Remote: lan}, "", "", false, false, false},
		{"stale cookie from the LAN", creds{Cookie: "not-a-session", Remote: lan}, "", "", false, false, false},
		{"read-only key", creds{APIKey: readKey, Remote: lan}, "apikey", "viewer", false, false, false},
		{"read-write key", creds{APIKey: writeKey, Remote: lan}, "apikey", "admin", false, true, false},
		{"access password", creds{Basic: "letmein", Remote: lan}, "password", "admin", true, true, false},
		// Through a reverse proxy on this machine the peer is 127.0.0.1, but
		// the visitor is not at this computer.
		{"anonymous through a local proxy", creds{Proxied: "203.0.113.9"}, "", "", false, false, false},
		{"viewer through a local proxy", creds{Cookie: viewer, Proxied: "203.0.113.9"}, "user", "viewer", false, false, true},
	} {
		var me model.Principal
		if code, body, _ := as(t, ts, tc.c, "GET", "/api/me", nil, &me); code != 200 {
			t.Fatalf("%s: /api/me %d %s", tc.name, code, body)
		}
		if me.Kind != tc.kind || me.Role != tc.role || me.IsAdmin != tc.admin || me.CanWrite != tc.write || me.SignedIn != tc.signedIn {
			t.Errorf("%s: got %+v", tc.name, me)
		}
		// What /api/me claims is what the policy grants: the admin-only
		// settings are open to exactly the principals reported as admins.
		want := 403
		switch {
		case tc.kind == "":
			want = 401
		case tc.admin:
			want = 200
		}
		if code, _, _ := as(t, ts, tc.c, "GET", "/api/settings", nil, nil); code != want {
			t.Errorf("%s: settings %d, want %d", tc.name, code, want)
		}
	}
}

// A role change applies to the sessions an account already holds, on its next
// request, without signing it out.
func TestRoleChangeKeepsTheSession(t *testing.T) {
	ts, srv := newTestServer(t)
	allowTestRemote(srv)
	admin := bootstrapAdmin(t, ts, "pat", "correct horse battery")
	var sam model.User
	if code, body, _ := as(t, ts, creds{Cookie: admin}, "POST", "/api/users", map[string]string{"username": "sam", "password": "viewer password", "role": "viewer"}, &sam); code != 201 {
		t.Fatalf("create viewer: %d %s", code, body)
	}
	remote := creds{Cookie: login(t, ts, "sam", "viewer password"), Remote: "192.168.1.61:1"}

	for _, step := range []struct {
		role  string
		admin bool
	}{{"admin", true}, {"viewer", false}} {
		if code, body, _ := as(t, ts, creds{Cookie: admin}, "PUT", fmt.Sprintf("/api/users/%d", sam.ID), map[string]string{"role": step.role}, nil); code != 200 {
			t.Fatalf("set role %s: %d %s", step.role, code, body)
		}
		var me model.Principal
		if code, _, _ := as(t, ts, remote, "GET", "/api/me", nil, &me); code != 200 || !me.SignedIn || me.IsAdmin != step.admin || me.Role != step.role {
			t.Fatalf("after setting %s the same session should report it: %d %+v", step.role, code, me)
		}
	}
	// A password reset, by contrast, still ends every session.
	if code, body, _ := as(t, ts, creds{Cookie: admin}, "PUT", fmt.Sprintf("/api/users/%d", sam.ID), map[string]string{"password": "a brand new password"}, nil); code != 200 {
		t.Fatalf("reset password: %d %s", code, body)
	}
	var me model.Principal
	if as(t, ts, remote, "GET", "/api/me", nil, &me); me.SignedIn {
		t.Fatal("a password reset must end the account's sessions")
	}
}

// A database that cannot say whether a session is valid must not be taken to
// mean it is not: that 401 is what sent people to the sign-in screen, and on
// this computer falling through would hand a signed-in viewer the
// administrator principal.
func TestSessionLookupFailureIsNotASignOut(t *testing.T) {
	ts, srv := newTestServer(t)
	allowTestRemote(srv)
	session := bootstrapAdmin(t, ts, "pat", "correct horse battery")

	if err := srv.Store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []creds{
		{Cookie: session, Remote: "192.168.1.62:1"},
		{Cookie: session}, // on this computer: not the loopback principal either
	} {
		for _, path := range []string{"/api/me", "/api/nodes", "/api/settings"} {
			code, body, hdr := as(t, ts, c, "GET", path, nil, nil)
			if code != http.StatusServiceUnavailable || hdr.Get("Retry-After") == "" {
				t.Fatalf("%s with the database unavailable (remote %q): %d %s, Retry-After %q", path, c.Remote, code, body, hdr.Get("Retry-After"))
			}
		}
	}
	// The shell itself is still served, so a reload is not an error page.
	if code, body, _ := as(t, ts, creds{Cookie: session, Remote: "192.168.1.62:1"}, "GET", "/", nil, nil); code != 200 {
		t.Fatalf("shell with the database unavailable: %d %s", code, body)
	}
}

// The session slides forward while it is used, and the browser's cookie
// slides with it — otherwise the cookie dies on the date it was first issued
// however active the session has been since.
func TestSessionCookieFollowsRenewal(t *testing.T) {
	ts, srv := newTestServer(t)
	allowTestRemote(srv)
	fresh := bootstrapAdmin(t, ts, "pat", "correct horse battery")
	remote := "192.168.1.63:1"

	sessionCookie := func(hdr http.Header) *http.Cookie {
		for _, c := range (&http.Response{Header: hdr}).Cookies() {
			if c.Name == auth.SessionCookie {
				return c
			}
		}
		return nil
	}

	// A session signed in a moment ago has nothing to renew: no Set-Cookie
	// on ordinary requests.
	if _, _, hdr := as(t, ts, creds{Cookie: fresh, Remote: remote}, "GET", "/api/nodes", nil, nil); sessionCookie(hdr) != nil {
		t.Fatal("a fresh session should not re-issue its cookie on every request")
	}

	// One whose renewal is due gets the same token back with a new expiry.
	var users []model.User
	as(t, ts, creds{Cookie: fresh}, "GET", "/api/users", nil, &users)
	token, _ := auth.NewSessionToken()
	if err := srv.Store.CreateSession(t.Context(), auth.HashToken(token), users[0].ID, "", time.Now().Add(auth.SessionLifetime-2*auth.SessionRenewInterval)); err != nil {
		t.Fatal(err)
	}
	code, body, hdr := as(t, ts, creds{Cookie: token, Remote: remote}, "GET", "/api/nodes", nil, nil)
	if code != 200 {
		t.Fatalf("renewing request: %d %s", code, body)
	}
	c := sessionCookie(hdr)
	if c == nil {
		t.Fatal("a renewed session must re-issue its cookie")
	}
	if c.Value != token || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Secure {
		t.Fatalf("renewed cookie: %+v", c)
	}
	want := time.Now().Add(auth.SessionLifetime)
	if d := c.Expires.Sub(want); d < -time.Minute || d > time.Minute {
		t.Fatalf("renewed cookie expires %v, want about %v", c.Expires, want)
	}
	// Renewed once, it stays quiet until the next renewal is due.
	if _, _, hdr := as(t, ts, creds{Cookie: token, Remote: remote}, "GET", "/api/nodes", nil, nil); sessionCookie(hdr) != nil {
		t.Fatal("the cookie should be re-issued once per renewal, not on every request")
	}
}

// A reverse proxy running alongside GWatch connects from 127.0.0.1 for
// visitors who are not at this computer; they must not get the no-sign-in
// administrator principal.
func TestProxiedLoopbackIsNotThisComputer(t *testing.T) {
	ts, _ := newTestServer(t)

	// With no accounts yet, a proxied visitor is anonymous — the same as a
	// visitor from the LAN — while the machine itself stays administrator.
	if code, body, _ := as(t, ts, creds{Proxied: "203.0.113.10"}, "POST", "/api/nodes", newNode("Nope"), nil); code != 401 {
		t.Fatalf("proxied visitor on a fresh install: %d %s", code, body)
	}
	if code, body, _ := as(t, ts, creds{}, "POST", "/api/nodes", newNode("Router"), nil); code != 201 {
		t.Fatalf("this computer: %d %s", code, body)
	}

	// Signing in through the proxy works as it does from anywhere else.
	admin := bootstrapAdmin(t, ts, "pat", "correct horse battery")
	if code, body, _ := as(t, ts, creds{Cookie: admin, Proxied: "203.0.113.10"}, "GET", "/api/settings", nil, nil); code != 200 {
		t.Fatalf("signed in through the proxy: %d %s", code, body)
	}
	var setup struct {
		LoginRequired bool `json:"loginRequired"`
	}
	as(t, ts, creds{Proxied: "203.0.113.10"}, "GET", "/api/auth/setup", nil, &setup)
	if !setup.LoginRequired {
		t.Fatal("a proxied visitor should be shown the sign-in screen")
	}
}

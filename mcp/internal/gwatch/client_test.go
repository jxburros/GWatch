package gwatch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A redirect to another origin must not take the API key with it: net/http
// forwards custom headers such as X-API-Key across hosts.
func TestRedirectToAnotherOriginIsRefused(t *testing.T) {
	var leaked string
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("X-API-Key")
		_, _ = w.Write([]byte(`{"kind":"apikey"}`))
	}))
	defer elsewhere.Close()
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+r.URL.Path, http.StatusFound)
	}))
	defer gw.Close()

	c := New(gw.URL, "gw_secret", "test", 5*time.Second)
	_, err := c.Me(context.Background())
	if err == nil || !strings.Contains(err.Error(), "refusing to follow a redirect") {
		t.Fatalf("Me through a cross-origin redirect: err = %v, want a refusal", err)
	}
	if leaked != "" {
		t.Fatalf("the API key reached the other origin: %q", leaked)
	}
}

func TestRedirectOnTheSameOriginIsFollowed(t *testing.T) {
	var got string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/me" {
			http.Redirect(w, r, srv.URL+"/api/v1/me/", http.StatusFound)
			return
		}
		got = r.Header.Get("X-API-Key")
		_, _ = w.Write([]byte(`{"kind":"apikey","scope":"read"}`))
	}))
	defer srv.Close()

	me, err := New(srv.URL, "gw_secret", "test", 5*time.Second).Me(context.Background())
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if me.Scope != "read" || got != "gw_secret" {
		t.Fatalf("scope %q, key %q after a same-origin redirect", me.Scope, got)
	}
}

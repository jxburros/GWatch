package checks

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jxburros/GWatch/internal/model"
)

// recorder is a server a redirect points at, keeping what it was sent.
type recorder struct {
	mu      sync.Mutex
	headers http.Header
	body    string
}

func (rc *recorder) handler(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	rc.mu.Lock()
	rc.headers, rc.body = r.Header.Clone(), string(b)
	rc.mu.Unlock()
}

// A check's headers and body are stored as credentials. Go carries custom
// headers, and a 307's body, to whatever host a redirect names; a monitored
// service redirecting elsewhere must not hand them on.
func TestHTTPCheckKeepsCredentialsOnItsOwnHost(t *testing.T) {
	other := &recorder{}
	elsewhere := httptest.NewServer(http.HandlerFunc(other.handler))
	defer elsewhere.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/same" {
			if r.Header.Get("X-Api-Key") != "k3y" {
				w.WriteHeader(http.StatusForbidden)
			}
			return
		}
		if r.URL.Path == "/hop" {
			http.Redirect(w, r, "/same", http.StatusTemporaryRedirect)
			return
		}
		http.Redirect(w, r, elsewhere.URL+"/landing", http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	// The origin is reached by name and the other server by address, so the
	// redirect crosses hosts.
	base := strings.Replace(origin.URL, "127.0.0.1", "localhost", 1)

	cfg := model.CheckConfig{Method: "POST", Body: "password=hunter2", Headers: map[string]string{"X-Api-Key": "k3y", "Authorization": "Bearer t0k"}}
	res := run(t, httpCheck(model.CheckHTTP, base+"/start", cfg), Options{})
	if !res.Success {
		t.Fatalf("check failed: %s", res.Error)
	}
	other.mu.Lock()
	defer other.mu.Unlock()
	if other.headers == nil {
		t.Fatal("the redirect was not followed")
	}
	if other.headers.Get("X-Api-Key") != "" || other.headers.Get("Authorization") != "" || strings.Contains(other.body, "hunter2") {
		t.Errorf("credentials followed the redirect: headers %v body %q", other.headers, other.body)
	}

	// On the same host they still go along.
	res = run(t, httpCheck(model.CheckHTTP, base+"/hop", cfg), Options{})
	if !res.Success {
		t.Errorf("same-host redirect lost the header: %s", res.Error)
	}
}

// Go keeps Authorization across a drop from https to http on the same host,
// which would send a metrics token in the clear.
func TestMetricsTokenDoesNotFollowADowngrade(t *testing.T) {
	plain := &recorder{}
	insecure := httptest.NewServer(http.HandlerFunc(plain.handler))
	defer insecure.Close()
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, insecure.URL+"/metrics", http.StatusFound)
	}))
	defer secure.Close()

	cfg := model.CheckConfig{HostSource: model.HostSourceURL, MetricsURL: secure.URL, MetricsToken: "m3tricsT0ken", IgnoreTLSErrors: true}
	_ = Run(context.Background(), systemCheck(cfg), Options{})
	plain.mu.Lock()
	defer plain.mu.Unlock()
	if plain.headers == nil {
		t.Fatal("the redirect was not followed")
	}
	if got := plain.headers.Get("Authorization"); got != "" {
		t.Errorf("token sent over plain http: %q", got)
	}
}

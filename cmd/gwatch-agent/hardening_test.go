package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestServiceControlsNeedNoCredentials(t *testing.T) {
	for _, cmd := range []string{"status", "uninstall", "start", "stop", "restart"} {
		if err := (config{}).validate(cmd); err != nil {
			t.Errorf("%s: %v", cmd, err)
		}
	}
}
func TestServeRejectsEmptyAndLimitsGuesses(t *testing.T) {
	for _, token := range []string{"", "separate-serve-secret"} {
		handler := (&program{cfg: config{token: token}}).metricsHandler()
		for i := 0; i < 12; i++ {
			r := httptest.NewRequest("GET", "/metrics", nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			want := http.StatusUnauthorized
			if i >= 10 {
				want = http.StatusTooManyRequests
			}
			if w.Code != want {
				t.Fatalf("attempt %d: %d, want %d", i, w.Code, want)
			}
		}
	}
}
func TestPinnedTLSRejectsChangedServerKey(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer server.Close()
	sum := sha256.Sum256(server.Certificate().RawSubjectPublicKeyInfo)
	client := newClient(config{certPin: hex.EncodeToString(sum[:])})
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	client = newClient(config{certPin: "0000000000000000000000000000000000000000000000000000000000000000"})
	if resp, err = client.Get(server.URL); err == nil {
		resp.Body.Close()
		t.Fatal("changed pin accepted")
	}
}

func TestServeLockoutAppliesBeforeSecretComparison(t *testing.T) {
	l := newServeLimiter()
	now := time.Now()
	want := []byte("secret")
	for i := 0; i < 10; i++ {
		if status := l.authenticate("192.0.2.1:1234", []byte("wrong"), want, now); status != 401 {
			t.Fatal(status)
		}
	}
	if status := l.authenticate("192.0.2.1:1234", want, want, now); status != 429 {
		t.Fatal("correct guess bypassed lockout")
	}
	if status := l.authenticate("192.0.2.2:1234", want, want, now); status != 0 {
		t.Fatal("unrelated peer blocked")
	}
	if status := l.authenticate("192.0.2.1:1234", want, want, now.Add(time.Minute)); status != 0 {
		t.Fatal("lockout did not expire")
	}
}

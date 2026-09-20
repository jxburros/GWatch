package checks

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

func httpCheck(t model.CheckType, target string, cfg model.CheckConfig) model.Check {
	cfg.Target = target
	return model.Check{ID: 1, Type: t, Name: "t", Enabled: true, TimeoutSeconds: 5, Config: cfg}
}

func run(t *testing.T, c model.Check, opts Options) model.Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return Run(ctx, c, opts)
}

func TestParseStatusExpr(t *testing.T) {
	cases := []struct {
		expr    string
		ok      []int
		bad     []int
		str     string
		wantErr bool
	}{
		{expr: "", ok: []int{200, 301, 399}, bad: []int{199, 400, 503}, str: "200-399"},
		{expr: "200", ok: []int{200}, bad: []int{201, 404}, str: "200"},
		{expr: "200-299", ok: []int{200, 250, 299}, bad: []int{300}, str: "200-299"},
		{expr: "200,301,302", ok: []int{200, 301, 302}, bad: []int{300, 303}, str: "200,301,302"},
		{expr: "2xx", ok: []int{200, 299}, bad: []int{300}, str: "2xx"},
		{expr: "2xx, 404", ok: []int{204, 404}, bad: []int{500}, str: "2xx,404"},
		{expr: "abc", wantErr: true},
		{expr: "600", wantErr: true},
		{expr: "300-200", wantErr: true},
		{expr: "20-", wantErr: true},
	}
	for _, tc := range cases {
		m, err := ParseStatusExpr(tc.expr)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%q: expected error", tc.expr)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unexpected error %v", tc.expr, err)
			continue
		}
		if m.String() != tc.str {
			t.Errorf("%q: String() = %q, want %q", tc.expr, m.String(), tc.str)
		}
		for _, c := range tc.ok {
			if !m.Matches(c) {
				t.Errorf("%q should match %d", tc.expr, c)
			}
		}
		for _, c := range tc.bad {
			if m.Matches(c) {
				t.Errorf("%q should not match %d", tc.expr, c)
			}
		}
	}
}

func TestHTTPSuccessAndTimings(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test") != "yes" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("ETag", "v1")
		_, _ = w.Write([]byte("<html><body>Welcome home</body></html>"))
	}))
	defer srv.Close()

	res := run(t, httpCheck(model.CheckHTTP, srv.URL, model.CheckConfig{Headers: map[string]string{"X-Test": "yes"}}), Options{})
	if !res.Success || res.Status != model.StatusUp {
		t.Fatalf("expected up, got %+v", res)
	}
	if res.Details.StatusCode != 200 {
		t.Errorf("status code = %d", res.Details.StatusCode)
	}
	if res.LatencyMS == nil || res.Details.TotalMs == nil || res.Details.ConnectMs == nil || res.Details.FirstByteMs == nil {
		t.Errorf("timing fields missing: %+v", res.Details)
	}
	if res.Details.TLSMs != nil {
		t.Errorf("plain http should not report TLS time")
	}
	if res.Details.FinalURL != srv.URL {
		t.Errorf("FinalURL = %q", res.Details.FinalURL)
	}
	if res.Attempts != 1 || res.Timestamp.IsZero() {
		t.Errorf("attempts/timestamp wrong: %d %v", res.Attempts, res.Timestamp)
	}
	if !strings.HasPrefix(res.Message, "HTTP 200 in ") {
		t.Errorf("message = %q", res.Message)
	}
	if res.Details.ContentLength == 0 {
		t.Errorf("content length not recorded")
	}
}

func TestHTTPUnexpectedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	res := run(t, httpCheck(model.CheckHTTP, srv.URL, model.CheckConfig{}), Options{})
	if res.Success || res.Status != model.StatusDown {
		t.Fatalf("expected down, got %+v", res)
	}
	if !res.Details.UnexpectedCode || res.Details.StatusCode != 503 {
		t.Errorf("details = %+v", res.Details)
	}
	if res.Message != "Unexpected HTTP 503 (expected 200-399)" {
		t.Errorf("message = %q", res.Message)
	}
	// An explicit expectation of 503 turns it into a success.
	res = run(t, httpCheck(model.CheckHTTP, srv.URL, model.CheckConfig{ExpectedStatus: "503"}), Options{})
	if !res.Success {
		t.Errorf("expected success with explicit 503, got %q", res.Message)
	}
}

func TestHTTPRedirects(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/b", http.StatusFound) })
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/final", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/final", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("done")) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	res := run(t, httpCheck(model.CheckHTTP, srv.URL+"/a", model.CheckConfig{}), Options{})
	if !res.Success {
		t.Fatalf("expected success: %+v", res)
	}
	if res.Details.Redirects != 2 {
		t.Errorf("redirects = %d, want 2", res.Details.Redirects)
	}
	if res.Details.FinalURL != srv.URL+"/final" {
		t.Errorf("FinalURL = %q", res.Details.FinalURL)
	}

	no := false
	res = run(t, httpCheck(model.CheckHTTP, srv.URL+"/a", model.CheckConfig{FollowRedirects: &no, ExpectedStatus: "302"}), Options{})
	if !res.Success || res.Details.StatusCode != 302 || res.Details.Redirects != 0 || res.Details.FinalURL != srv.URL+"/a" {
		t.Errorf("no-follow result = %+v", res)
	}
}

func TestHTTPSSelfSignedCertificate(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer srv.Close()

	res := run(t, httpCheck(model.CheckHTTP, srv.URL, model.CheckConfig{}), Options{})
	if res.Success || res.Status != model.StatusDown {
		t.Fatalf("self-signed cert should fail: %+v", res)
	}
	if !strings.HasPrefix(res.Error, "TLS: ") || !strings.Contains(res.Error, "x509") {
		t.Errorf("error = %q", res.Error)
	}
	if res.Details.Cert == nil || res.Details.Cert.Valid || res.Details.Cert.Error == "" {
		t.Errorf("expected probed cert details with Valid=false, got %+v", res.Details.Cert)
	}

	res = run(t, httpCheck(model.CheckHTTP, srv.URL, model.CheckConfig{IgnoreTLSErrors: true}), Options{})
	if !res.Success {
		t.Fatalf("with IgnoreTLSErrors the request should succeed: %+v", res)
	}
	c := res.Details.Cert
	if c == nil {
		t.Fatalf("cert details missing")
	}
	if c.Valid || c.Error == "" || c.NotAfter.IsZero() || c.Subject == "" {
		t.Errorf("cert = %+v", c)
	}
	if res.Details.TLSMs == nil {
		t.Errorf("TLS timing missing")
	}
	if res.Status != model.StatusUp {
		t.Errorf("status = %s warnings=%v", res.Status, res.Warnings)
	}

	// A huge warn window turns the far-future test certificate into a warning.
	res = run(t, httpCheck(model.CheckHTTP, srv.URL, model.CheckConfig{IgnoreTLSErrors: true, CertWarnDays: 100000}), Options{})
	if res.Status != model.StatusDegraded || len(res.Warnings) == 0 || !strings.HasPrefix(res.Warnings[0], "Certificate expires in ") {
		t.Errorf("expected expiry warning, got status=%s warnings=%v", res.Status, res.Warnings)
	}

	// CertCheck=false suppresses certificate evaluation.
	off := false
	res = run(t, httpCheck(model.CheckHTTP, srv.URL, model.CheckConfig{IgnoreTLSErrors: true, CertCheck: &off, CertWarnDays: 100000}), Options{})
	if res.Details.Cert != nil || res.Status != model.StatusUp {
		t.Errorf("CertCheck=false should skip cert: %+v", res)
	}
}

// TestNetErrorLabel covers the wording on every platform, including the
// Winsock numbers that only a Windows machine produces. Windows reports
// different, localized text for these conditions, so matching the message
// instead of the error number would leave Windows users reading raw
// "No connection could be made because..." strings.
func TestNetErrorLabel(t *testing.T) {
	cases := []struct {
		name  string
		errno syscall.Errno
		want  string
	}{
		{"posix refused", syscall.ECONNREFUSED, "connection refused"},
		{"posix host unreachable", syscall.EHOSTUNREACH, "no route to host"},
		{"posix network unreachable", syscall.ENETUNREACH, "network is unreachable"},
		{"posix reset", syscall.ECONNRESET, "connection reset by peer"},
		{"winsock refused", wsaeConnRefused, "connection refused"},
		{"winsock host unreachable", wsaeHostUnreach, "no route to host"},
		{"winsock network unreachable", wsaeNetUnreach, "network is unreachable"},
		{"winsock reset", wsaeConnReset, "connection reset by peer"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := netErrorLabel(c.errno); got != c.want {
				t.Errorf("netErrorLabel(%v) = %q, want %q", c.errno, got, c.want)
			}
			// The errno is normally buried under *net.OpError and
			// *os.SyscallError, so unwrapping must work too.
			wrapped := &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", c.errno)}
			if got := describeNetError(wrapped, time.Second); got != c.want {
				t.Errorf("describeNetError(wrapped %v) = %q, want %q", c.errno, got, c.want)
			}
		})
	}

	if got := netErrorLabel(errors.New("boom")); got != "" {
		t.Errorf("non-syscall error = %q, want empty", got)
	}
	if got := netErrorLabel(syscall.Errno(0)); got != "" {
		t.Errorf("zero errno = %q, want empty", got)
	}
}

func TestHTTPErrors(t *testing.T) {
	// Connection refused.
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	_ = l.Close()
	res := run(t, httpCheck(model.CheckHTTP, "http://"+addr, model.CheckConfig{}), Options{})
	if res.Success || !strings.Contains(res.Error, "connection refused") {
		t.Errorf("expected connection refused, got %+v", res)
	}

	// Timeout.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(3 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	c := httpCheck(model.CheckHTTP, srv.URL, model.CheckConfig{})
	c.TimeoutSeconds = 1
	res = run(t, c, Options{})
	if res.Success || res.Error != "timed out after 1s" {
		t.Errorf("expected timeout, got %+v", res)
	}

	// Bad URL / DNS.
	res = run(t, httpCheck(model.CheckHTTP, "ftp://example", model.CheckConfig{}), Options{})
	if res.Success || !strings.Contains(res.Error, "scheme") {
		t.Errorf("expected scheme error, got %+v", res)
	}
}

func TestHTTPRetries(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) == 1 {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	c := httpCheck(model.CheckHTTP, srv.URL, model.CheckConfig{})
	c.Retries = 2
	res := run(t, c, Options{})
	if !res.Success || res.Attempts != 2 {
		t.Errorf("expected success on attempt 2, got success=%v attempts=%d", res.Success, res.Attempts)
	}
}

func TestKeywordCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<h1>Welcome to Plex</h1>"))
	}))
	defer srv.Close()

	res := run(t, httpCheck(model.CheckKeyword, srv.URL, model.CheckConfig{Keyword: "Welcome"}), Options{})
	if !res.Success || res.Message != `Keyword "Welcome" found` || res.Details.KeywordFound == nil || !*res.Details.KeywordFound {
		t.Errorf("found case: %+v", res)
	}
	res = run(t, httpCheck(model.CheckKeyword, srv.URL, model.CheckConfig{Keyword: "Error"}), Options{})
	if res.Success || res.Message != `Keyword "Error" not found` || res.Status != model.StatusDown {
		t.Errorf("missing case: %+v", res)
	}
	res = run(t, httpCheck(model.CheckKeyword, srv.URL, model.CheckConfig{Keyword: "Error", KeywordAbsent: true}), Options{})
	if !res.Success {
		t.Errorf("absent case should pass: %+v", res)
	}
	res = run(t, httpCheck(model.CheckKeyword, srv.URL, model.CheckConfig{Keyword: "Welcome", KeywordAbsent: true}), Options{})
	if res.Success {
		t.Errorf("absent case should fail when present: %+v", res)
	}
	res = run(t, httpCheck(model.CheckKeyword, srv.URL, model.CheckConfig{}), Options{})
	if res.Success || !strings.Contains(res.Error, "keyword") {
		t.Errorf("missing keyword config should fail: %+v", res)
	}
}

func TestJSONPath(t *testing.T) {
	doc := map[string]any{
		"status": "ok",
		"count":  float64(3),
		"flag":   true,
		"data": map[string]any{
			"items": []any{
				map[string]any{"name": "first", "n": float64(1)},
				map[string]any{"name": "second"},
			},
		},
	}
	cases := []struct {
		path   string
		want   string
		exists bool
	}{
		{"", `{"count":3,"data":{"items":[{"n":1,"name":"first"},{"name":"second"}]},"flag":true,"status":"ok"}`, true},
		{"status", "ok", true},
		{"$.status", "ok", true},
		{"count", "3", true},
		{"flag", "true", true},
		{"data.items[0].name", "first", true},
		{"data.items[1]", `{"name":"second"}`, true},
		{"data.items[-1].name", "second", true},
		{"data.items[5].name", "", false},
		{"data.items.name", "", false},
		{"missing", "", false},
		{"data.items[0].n", "1", true},
	}
	for _, tc := range cases {
		v, ok := resolveJSONPath(doc, tc.path)
		if ok != tc.exists {
			t.Errorf("%q: exists=%v want %v", tc.path, ok, tc.exists)
			continue
		}
		if ok && jsonValueString(v) != tc.want {
			t.Errorf("%q: got %q want %q", tc.path, jsonValueString(v), tc.want)
		}
	}
	if !jsonValueEquals("1", "1.0") || !jsonValueEquals("ok", "ok") || jsonValueEquals("ok", "nope") || !jsonValueEquals("ok", `"ok"`) {
		t.Errorf("jsonValueEquals mismatch")
	}
}

func TestJSONCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","data":{"items":[{"name":"first","n":1.0}]}}`))
	}))
	defer srv.Close()

	res := run(t, httpCheck(model.CheckJSON, srv.URL, model.CheckConfig{JSONPath: "status", JSONExpected: "ok"}), Options{})
	if !res.Success || res.Message != `json "status" = "ok"` || res.Details.JSONValue != "ok" || res.Details.JSONMatched == nil || !*res.Details.JSONMatched {
		t.Errorf("expected match: %+v", res)
	}
	res = run(t, httpCheck(model.CheckJSON, srv.URL, model.CheckConfig{JSONPath: "data.items[0].n", JSONExpected: "1"}), Options{})
	if !res.Success {
		t.Errorf("numeric equality: %+v", res)
	}
	res = run(t, httpCheck(model.CheckJSON, srv.URL, model.CheckConfig{JSONPath: "status", JSONExpected: "down"}), Options{})
	if res.Success || res.Status != model.StatusDown || !strings.Contains(res.Message, `(expected "down")`) {
		t.Errorf("mismatch: %+v", res)
	}
	res = run(t, httpCheck(model.CheckJSON, srv.URL, model.CheckConfig{JSONPath: "data.items[0].name"}), Options{})
	if !res.Success || res.Details.JSONValue != "first" {
		t.Errorf("exists only: %+v", res)
	}
	res = run(t, httpCheck(model.CheckJSON, srv.URL, model.CheckConfig{JSONPath: "nope.x"}), Options{})
	if res.Success || !strings.Contains(res.Message, "not found") {
		t.Errorf("missing path: %+v", res)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("<html>")) }))
	defer bad.Close()
	res = run(t, httpCheck(model.CheckJSON, bad.URL, model.CheckConfig{JSONPath: "status"}), Options{})
	if res.Success || !strings.Contains(res.Message, "not valid JSON") {
		t.Errorf("invalid json: %+v", res)
	}
}

func TestContentWatch(t *testing.T) {
	var body atomic.Value
	body.Store("<html><!-- c1 --><script>var t=1</script>Hello   World</html>")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", "v1")
		_, _ = w.Write([]byte(body.Load().(string)))
	}))
	defer srv.Close()

	first := run(t, httpCheck(model.CheckHTTP, srv.URL, model.CheckConfig{ContentWatch: "hash"}), Options{})
	if first.Details.ContentHash == "" || first.Details.ContentChanged || first.Status != model.StatusUp {
		t.Fatalf("first run: %+v", first)
	}
	// Changes in comments, scripts, whitespace and case do not count.
	body.Store("<HTML><!-- c2 --><script>var t=2</script>hello world</HTML>")
	same := run(t, httpCheck(model.CheckHTTP, srv.URL, model.CheckConfig{ContentWatch: "hash"}), Options{PreviousHash: first.Details.ContentHash})
	if same.Details.ContentHash != first.Details.ContentHash || same.Details.ContentChanged || same.Status != model.StatusUp {
		t.Errorf("normalised body should hash the same: %+v", same)
	}
	body.Store("<html>Hacked?</html>")
	changed := run(t, httpCheck(model.CheckHTTP, srv.URL, model.CheckConfig{ContentWatch: "hash"}), Options{PreviousHash: first.Details.ContentHash})
	if !changed.Details.ContentChanged || changed.Status != model.StatusDegraded || !changed.Success {
		t.Errorf("expected content changed warning: %+v", changed)
	}
	if len(changed.Warnings) != 1 || changed.Warnings[0] != "Response content changed" {
		t.Errorf("warnings = %v", changed.Warnings)
	}

	// Header mode.
	h := run(t, httpCheck(model.CheckHTTP, srv.URL, model.CheckConfig{ContentWatch: "header", ContentHeader: "ETag"}), Options{PreviousValue: "v0"})
	if h.Details.ContentValue != "v1" || !h.Details.ContentChanged || h.Status != model.StatusDegraded {
		t.Errorf("header mode: %+v", h)
	}
	h = run(t, httpCheck(model.CheckHTTP, srv.URL, model.CheckConfig{ContentWatch: "header", ContentHeader: "ETag"}), Options{PreviousValue: "v1"})
	if h.Details.ContentChanged || h.Status != model.StatusUp {
		t.Errorf("header unchanged: %+v", h)
	}
	// Redirect mode.
	r := run(t, httpCheck(model.CheckHTTP, srv.URL, model.CheckConfig{ContentWatch: "redirect"}), Options{})
	if r.Details.ContentValue != srv.URL {
		t.Errorf("redirect mode value = %q", r.Details.ContentValue)
	}
	// Keyword mode.
	k := run(t, httpCheck(model.CheckHTTP, srv.URL, model.CheckConfig{ContentWatch: "keyword", Keyword: "Hacked"}), Options{PreviousValue: "missing"})
	if k.Details.ContentValue != "found" || !k.Details.ContentChanged {
		t.Errorf("keyword mode: %+v", k)
	}
}

func TestLatencyWarning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(30 * time.Millisecond)
	}))
	defer srv.Close()
	res := run(t, httpCheck(model.CheckHTTP, srv.URL, model.CheckConfig{}), Options{LatencyWarnMS: 5})
	if res.Status != model.StatusDegraded || len(res.Warnings) == 0 {
		t.Errorf("expected latency warning: %+v", res)
	}
	res = run(t, httpCheck(model.CheckHTTP, srv.URL, model.CheckConfig{LatencyWarnMS: 60000}), Options{LatencyWarnMS: 5})
	if res.Status != model.StatusUp {
		t.Errorf("check threshold should override global: %+v", res)
	}
}

func TestCertCheck(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	host, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "https://"))

	res := run(t, httpCheck(model.CheckCert, srv.URL, model.CheckConfig{}), Options{})
	if res.Success || res.Status != model.StatusDown || !strings.HasPrefix(res.Message, "Certificate invalid: ") {
		t.Fatalf("self-signed should be down: %+v", res)
	}
	if res.Details.Cert == nil || res.Details.Cert.Valid || res.Details.Cert.DaysRemaining <= 0 || res.Details.Cert.Serial == "" {
		t.Errorf("cert details: %+v", res.Details.Cert)
	}
	if res.LatencyMS == nil || res.Details.TLSMs == nil {
		t.Errorf("handshake timing missing")
	}

	res = run(t, httpCheck(model.CheckCert, host, model.CheckConfig{Port: atoi(port), IgnoreTLSErrors: true}), Options{})
	if !res.Success || res.Status != model.StatusUp || !strings.HasPrefix(res.Message, "Not verified, expires in ") {
		t.Errorf("ignore TLS errors: %+v", res)
	}
	res = run(t, httpCheck(model.CheckCert, host+":"+port, model.CheckConfig{IgnoreTLSErrors: true, CertWarnDays: 100000}), Options{})
	if res.Status != model.StatusDegraded || len(res.Warnings) == 0 {
		t.Errorf("expiry warning: %+v", res)
	}

	// Closed port.
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	closed := l.Addr().String()
	_ = l.Close()
	res = run(t, httpCheck(model.CheckCert, closed, model.CheckConfig{}), Options{})
	if res.Success || !strings.Contains(res.Error, "connection refused") {
		t.Errorf("closed port: %+v", res)
	}
}

func TestTCPCheck(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	host, port, _ := net.SplitHostPort(l.Addr().String())

	res := run(t, httpCheck(model.CheckTCP, host, model.CheckConfig{Port: atoi(port)}), Options{})
	if !res.Success || res.Status != model.StatusUp || res.LatencyMS == nil || res.Details.RemoteAddr != l.Addr().String() {
		t.Errorf("open port: %+v", res)
	}
	if !strings.HasPrefix(res.Message, "Connected to "+l.Addr().String()+" in ") {
		t.Errorf("message = %q", res.Message)
	}
	// host:port in target, no Port configured.
	res = run(t, httpCheck(model.CheckTCP, l.Addr().String(), model.CheckConfig{}), Options{})
	if !res.Success {
		t.Errorf("host:port target: %+v", res)
	}
	// Node host fallback.
	res = run(t, model.Check{Type: model.CheckTCP, Config: model.CheckConfig{Port: atoi(port)}}, Options{NodeHost: host})
	if !res.Success {
		t.Errorf("node host fallback: %+v", res)
	}

	closedL, _ := net.Listen("tcp", "127.0.0.1:0")
	closed := closedL.Addr().String()
	_ = closedL.Close()
	res = run(t, httpCheck(model.CheckTCP, closed, model.CheckConfig{}), Options{})
	if res.Success || res.Status != model.StatusDown || !strings.HasPrefix(res.Message, "Connection refused") {
		t.Errorf("closed port: %+v", res)
	}
	res = run(t, httpCheck(model.CheckTCP, host, model.CheckConfig{}), Options{})
	if res.Success || !strings.Contains(res.Error, "port") {
		t.Errorf("missing port: %+v", res)
	}
}

// fakeDNS answers A queries with 1.2.3.4 for names under "good." and
// NXDOMAIN for everything else. AAAA queries get an empty NOERROR answer.
func fakeDNS(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	go func() {
		buf := make([]byte, 1500)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			q := buf[:n]
			if len(q) < 12 {
				continue
			}
			// Parse the question name.
			i := 12
			var labels []string
			for i < len(q) && q[i] != 0 {
				l := int(q[i])
				if i+1+l > len(q) {
					break
				}
				labels = append(labels, string(q[i+1:i+1+l]))
				i += 1 + l
			}
			i++ // zero terminator
			if i+4 > len(q) {
				continue
			}
			qtype := binary.BigEndian.Uint16(q[i:])
			qend := i + 4
			name := strings.ToLower(strings.Join(labels, "."))

			resp := make([]byte, 0, 512)
			resp = append(resp, q[0], q[1]) // ID
			flags := uint16(0x8180)         // QR, RD, RA
			ancount := uint16(0)
			var answer []byte
			if strings.HasSuffix(name, "good.test") {
				if qtype == 1 {
					ancount = 1
					answer = []byte{0xC0, 0x0C, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 1, 2, 3, 4}
				}
			} else {
				flags |= 3 // NXDOMAIN
			}
			resp = binary.BigEndian.AppendUint16(resp, flags)
			resp = binary.BigEndian.AppendUint16(resp, 1)       // QDCOUNT
			resp = binary.BigEndian.AppendUint16(resp, ancount) // ANCOUNT
			resp = binary.BigEndian.AppendUint16(resp, 0)
			resp = binary.BigEndian.AppendUint16(resp, 0)
			resp = append(resp, q[12:qend]...)
			resp = append(resp, answer...)
			_, _ = pc.WriteTo(resp, addr)
		}
	}()
	return pc.LocalAddr().String()
}

func TestDNSCheck(t *testing.T) {
	server := fakeDNS(t)

	res := run(t, httpCheck(model.CheckDNS, "host.good.test", model.CheckConfig{DNSServer: server}), Options{})
	if !res.Success || res.Status != model.StatusUp {
		t.Fatalf("expected success: %+v", res)
	}
	if len(res.Details.ResolvedValues) != 1 || res.Details.ResolvedValues[0] != "1.2.3.4" || res.Details.Resolver != server || res.LatencyMS == nil {
		t.Errorf("details: %+v", res.Details)
	}
	if !strings.HasPrefix(res.Message, "Resolved to 1.2.3.4 in ") {
		t.Errorf("message = %q", res.Message)
	}

	res = run(t, httpCheck(model.CheckDNS, "host.good.test", model.CheckConfig{DNSServer: server, ExpectedIPs: []string{"1.2.3.4"}}), Options{})
	if !res.Success || res.Details.ExpectedMatch == nil || !*res.Details.ExpectedMatch {
		t.Errorf("expected match: %+v", res)
	}
	res = run(t, httpCheck(model.CheckDNS, "host.good.test", model.CheckConfig{DNSServer: server, ExpectedIPs: []string{"5.6.7.8"}}), Options{})
	if res.Success || res.Message != "Resolved to 1.2.3.4 but expected 5.6.7.8" {
		t.Errorf("expected mismatch: %+v", res)
	}

	c := httpCheck(model.CheckDNS, "nope.bad.test", model.CheckConfig{DNSServer: server})
	c.TimeoutSeconds = 3
	res = run(t, c, Options{})
	if res.Success || res.Status != model.StatusDown || !strings.HasPrefix(res.Error, "DNS lookup failed") {
		t.Errorf("unresolvable: %+v", res)
	}

	// localhost through the system resolver (hosts file).
	res = run(t, httpCheck(model.CheckDNS, "localhost", model.CheckConfig{}), Options{})
	if !res.Success || res.Details.Resolver != "system" || len(res.Details.ResolvedValues) == 0 {
		t.Errorf("localhost: %+v", res)
	}
	// A URL target is reduced to its host.
	res = run(t, httpCheck(model.CheckDNS, "https://www.good.test:8443/path", model.CheckConfig{DNSServer: server}), Options{})
	if !res.Success {
		t.Errorf("url target: %+v", res)
	}
}

func TestPingCheck(t *testing.T) {
	orig := pingFunc
	defer func() { pingFunc = orig }()

	ms := func(v float64) time.Duration { return time.Duration(v * float64(time.Millisecond)) }
	pingFunc = func(ctx context.Context, host string, count int, timeout time.Duration) (pingResult, error) {
		if host != "192.168.1.1" {
			t.Errorf("host = %q", host)
		}
		if count != 4 {
			t.Errorf("count = %d", count)
		}
		return pingResult{Sent: 4, Received: 4, RTTs: []time.Duration{ms(10), ms(12), ms(11), ms(15)}}, nil
	}
	res := run(t, httpCheck(model.CheckPing, "192.168.1.1", model.CheckConfig{}), Options{})
	if !res.Success || res.Status != model.StatusUp {
		t.Fatalf("expected up: %+v", res)
	}
	if *res.LatencyMS != 12 || *res.MinMS != 10 || *res.MaxMS != 15 || *res.LossPct != 0 {
		t.Errorf("stats: avg=%v min=%v max=%v loss=%v", *res.LatencyMS, *res.MinMS, *res.MaxMS, *res.LossPct)
	}
	// |12-10| + |11-12| + |15-11| = 2+1+4 = 7 / 3 = 2.3
	if *res.JitterMS != 2.3 {
		t.Errorf("jitter = %v", *res.JitterMS)
	}
	// Population stddev around the mean of 12: (4+0+1+9)/4 = 3.5, sqrt = 1.87.
	if *res.StdDevMS != 1.9 {
		t.Errorf("stddev = %v", *res.StdDevMS)
	}
	if res.Message != "4/4 replies, avg 12 ms" || res.Details.PacketsSent != 4 || len(res.Details.RTTs) != 4 {
		t.Errorf("message/details: %q %+v", res.Message, res.Details)
	}

	pingFunc = func(ctx context.Context, host string, count int, timeout time.Duration) (pingResult, error) {
		return pingResult{Sent: 4, Received: 3, RTTs: []time.Duration{ms(10), ms(10), ms(10)}}, nil
	}
	res = run(t, httpCheck(model.CheckPing, "192.168.1.1", model.CheckConfig{}), Options{PacketLossWarnPct: 20})
	if res.Status != model.StatusDegraded || *res.LossPct != 25 || len(res.Warnings) != 1 {
		t.Errorf("loss warning: %+v", res)
	}
	if res.Message != "3/4 replies, avg 10 ms (25% loss)" {
		t.Errorf("message = %q", res.Message)
	}
	res = run(t, httpCheck(model.CheckPing, "192.168.1.1", model.CheckConfig{}), Options{PacketLossWarnPct: 50})
	if res.Status != model.StatusUp {
		t.Errorf("loss below threshold should be up: %+v", res)
	}
	res = run(t, httpCheck(model.CheckPing, "192.168.1.1", model.CheckConfig{LatencyWarnMS: 5}), Options{})
	if res.Status != model.StatusDegraded {
		t.Errorf("latency warning: %+v", res)
	}

	pingFunc = func(ctx context.Context, host string, count int, timeout time.Duration) (pingResult, error) {
		return pingResult{Sent: 4, Received: 0}, nil
	}
	c := httpCheck(model.CheckPing, "192.168.1.1", model.CheckConfig{})
	c.Retries = 1
	res = run(t, c, Options{})
	if res.Success || res.Status != model.StatusDown || res.Message != "no reply (100% loss)" || *res.LossPct != 100 || res.Attempts != 2 {
		t.Errorf("no reply: %+v", res)
	}
	if res.JitterMS != nil || res.LatencyMS != nil {
		t.Errorf("no RTT stats expected without replies")
	}

	pingFunc = func(ctx context.Context, host string, count int, timeout time.Duration) (pingResult, error) {
		return pingResult{}, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	res = run(t, httpCheck(model.CheckPing, "192.168.1.1", model.CheckConfig{}), Options{})
	if res.Success || !strings.HasPrefix(res.Error, "DNS lookup failed") {
		t.Errorf("dns failure: %+v", res)
	}
	pingFunc = func(ctx context.Context, host string, count int, timeout time.Duration) (pingResult, error) {
		return pingResult{}, errors.New("operation not permitted")
	}
	res = run(t, httpCheck(model.CheckPing, "192.168.1.1", model.CheckConfig{}), Options{})
	if res.Success || res.Error != "ping failed: operation not permitted" {
		t.Errorf("generic failure: %+v", res)
	}
}

func TestParsePingOutput(t *testing.T) {
	linux := `PING 8.8.8.8 (8.8.8.8) 56(84) bytes of data.
64 bytes from 8.8.8.8: icmp_seq=1 ttl=117 time=12.3 ms
64 bytes from 8.8.8.8: icmp_seq=2 ttl=117 time=11.9 ms
64 bytes from 8.8.8.8: icmp_seq=4 ttl=117 time=13.0 ms

--- 8.8.8.8 ping statistics ---
4 packets transmitted, 3 received, 25% packet loss, time 3004ms
rtt min/avg/max/mdev = 11.900/12.400/13.000/0.454 ms`
	pr := parsePingOutput(linux)
	if pr.Sent != 4 || pr.Received != 3 || len(pr.RTTs) != 3 || pr.RTTs[0] != 12300*time.Microsecond {
		t.Errorf("linux: %+v", pr)
	}
	windows := `Pinging 8.8.8.8 with 32 bytes of data:
Reply from 8.8.8.8: bytes=32 time=14ms TTL=117
Reply from 8.8.8.8: bytes=32 time<1ms TTL=117
Request timed out.
Reply from 8.8.8.8: bytes=32 time=15ms TTL=117

Ping statistics for 8.8.8.8:
    Packets: Sent = 4, Received = 3, Lost = 1 (25% loss),
Approximate round trip times in milli-seconds:
    Minimum = 14ms, Maximum = 15ms, Average = 14ms`
	pr = parsePingOutput(windows)
	if pr.Sent != 4 || pr.Received != 3 || len(pr.RTTs) != 3 || pr.RTTs[1] != time.Millisecond {
		t.Errorf("windows: %+v", pr)
	}
	pr = parsePingOutput("ping: unknown host nope")
	if pr.Sent != 0 || pr.Received != 0 || len(pr.RTTs) != 0 {
		t.Errorf("garbage: %+v", pr)
	}
}

func TestValidate(t *testing.T) {
	yes := true
	_ = yes
	cases := []struct {
		name    string
		check   model.Check
		host    string
		wantErr string
	}{
		{"unsupported type", model.Check{Type: "snmp"}, "h", "unsupported check type"},
		{"missing target", model.Check{Type: model.CheckPing}, "", "target"},
		{"node host fallback ok", model.Check{Type: model.CheckPing}, "192.168.1.1", ""},
		{"interval too small", model.Check{Type: model.CheckPing, IntervalSeconds: 5}, "h", "interval"},
		{"timeout too large", model.Check{Type: model.CheckPing, TimeoutSeconds: 999}, "h", "timeout"},
		{"retries too many", model.Check{Type: model.CheckPing, Retries: 50}, "h", "retries"},
		{"ping count", model.Check{Type: model.CheckPing, Config: model.CheckConfig{PingCount: 50}}, "h", "ping count"},
		{"http ok", model.Check{Type: model.CheckHTTP, Config: model.CheckConfig{Target: "example.com/path"}}, "", ""},
		{"http bad scheme", model.Check{Type: model.CheckHTTP, Config: model.CheckConfig{Target: "ftp://example.com"}}, "", "scheme"},
		{"http bad status", model.Check{Type: model.CheckHTTP, Config: model.CheckConfig{Target: "example.com", ExpectedStatus: "abc"}}, "", "status"},
		{"http bad method", model.Check{Type: model.CheckHTTP, Config: model.CheckConfig{Target: "example.com", Method: "GE T"}}, "", "method"},
		{"http bad watch", model.Check{Type: model.CheckHTTP, Config: model.CheckConfig{Target: "example.com", ContentWatch: "magic"}}, "", "content watch"},
		{"http header watch needs header", model.Check{Type: model.CheckHTTP, Config: model.CheckConfig{Target: "example.com", ContentWatch: "header"}}, "", "header"},
		{"keyword needs keyword", model.Check{Type: model.CheckKeyword, Config: model.CheckConfig{Target: "example.com"}}, "", "keyword"},
		{"json ok", model.Check{Type: model.CheckJSON, Config: model.CheckConfig{Target: "example.com", JSONPath: "a.b[0]"}}, "", ""},
		{"tcp needs port", model.Check{Type: model.CheckTCP}, "192.168.1.10", "port"},
		{"tcp port in target", model.Check{Type: model.CheckTCP}, "192.168.1.10:32400", ""},
		{"tcp bad port", model.Check{Type: model.CheckTCP, Config: model.CheckConfig{Port: 70000}}, "h", "port"},
		{"cert ok default port", model.Check{Type: model.CheckCert}, "example.com", ""},
		{"dns ok", model.Check{Type: model.CheckDNS, Config: model.CheckConfig{RecordType: "mx", DNSServer: "1.1.1.1"}}, "example.com", ""},
		{"dns bad type", model.Check{Type: model.CheckDNS, Config: model.CheckConfig{RecordType: "SRV"}}, "example.com", "record type"},
		{"dns bad server", model.Check{Type: model.CheckDNS, Config: model.CheckConfig{DNSServer: "1.1.1.1:99999"}}, "example.com", "DNS server"},
		{"negative loss", model.Check{Type: model.CheckPing, Config: model.CheckConfig{PacketLossWarnPct: 120}}, "h", "packet loss"},
	}
	for _, tc := range cases {
		err := Validate(tc.check, tc.host)
		if tc.wantErr == "" {
			if err != nil {
				t.Errorf("%s: unexpected error %v", tc.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.wantErr)) {
			t.Errorf("%s: err = %v, want containing %q", tc.name, err, tc.wantErr)
		}
	}
}

func TestTemplates(t *testing.T) {
	tpls := Templates()
	if len(tpls) != 9 {
		t.Fatalf("got %d templates", len(tpls))
	}
	// A template whose check reads a registered machine cannot name one until
	// the person picks it in the editor, exactly as the others cannot know a
	// host. Listing it here keeps that exception deliberate rather than
	// letting any template quietly ship unvalidatable.
	needsAChoice := map[string]string{"agent-machine": "which registered machine"}
	seen := map[string]bool{}
	for _, tpl := range tpls {
		if tpl.ID == "" || tpl.Name == "" || tpl.Description == "" || len(tpl.Checks) == 0 {
			t.Errorf("template %+v incomplete", tpl)
		}
		if seen[tpl.ID] {
			t.Errorf("duplicate template id %q", tpl.ID)
		}
		seen[tpl.ID] = true
		for _, c := range tpl.Checks {
			err := Validate(c, "192.168.1.10")
			if want, ok := needsAChoice[tpl.ID]; ok {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Errorf("template %s check %s: want an error asking for %q, got %v", tpl.ID, c.Name, want, err)
				}
			} else if err != nil {
				t.Errorf("template %s check %s: %v", tpl.ID, c.Name, err)
			}
			if c.IntervalSeconds != 60 || c.TimeoutSeconds != 10 || c.Retries != 1 || c.FailureThreshold != 2 || !c.Enabled {
				t.Errorf("template %s check %s has unexpected defaults: %+v", tpl.ID, c.Name, c)
			}
		}
	}
}

func TestRunNeverPanics(t *testing.T) {
	res := run(t, model.Check{Type: model.CheckHTTP}, Options{})
	if res.Success || res.Status != model.StatusDown || res.Error == "" {
		t.Errorf("empty check: %+v", res)
	}
	res = run(t, model.Check{Type: "bogus", Config: model.CheckConfig{Target: "x"}}, Options{})
	if res.Success || !strings.Contains(res.Error, "unsupported") {
		t.Errorf("bogus type: %+v", res)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res = Run(ctx, httpCheck(model.CheckHTTP, "http://127.0.0.1:9", model.CheckConfig{}), Options{})
	if res.Success || res.Attempts != 1 {
		t.Errorf("cancelled ctx: %+v", res)
	}
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jxburros/GWatch/internal/model"
)

type panel struct {
	t      *testing.T
	ts     *httptest.Server
	ui     *uiServer
	secret string
	dir    string
}

func newPanel(t *testing.T) *panel {
	t.Helper()
	dir := isolate(t)
	writeFile(t, filepath.Join(dir, "agent.json"), `{"server":"https://s:7230","token":"`+oldToken+`","name":"nas","interval":"60s"}`)
	cfg, problems, _ := baseConfig()
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	ui := newUIServer(cfg, "127.0.0.1:0", "s3cret")
	ts := httptest.NewServer(ui.handler())
	t.Cleanup(ts.Close)
	ui.addr = ts.Listener.Addr().String()
	ui.control = func(string) error { return nil }
	ui.state = func() serviceState { return serviceState{Managed: true, State: "running"} }
	ui.testReading = func(context.Context, config) error { return nil }
	ui.checkUpdate = func(context.Context, config) (model.UpdateInfo, error) { return model.UpdateInfo{}, nil }
	return &panel{t: t, ts: ts, ui: ui, secret: "s3cret", dir: dir}
}

type opts struct {
	cookie bool
	header bool
	origin string
	host   string
}

func (p *panel) do(method, path string, body any, o opts) (*http.Response, []byte) {
	p.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, p.ts.URL+path, rdr)
	if o.cookie {
		req.AddCookie(&http.Cookie{Name: uiCookie, Value: p.secret})
	}
	if o.header {
		req.Header.Set(uiHeader, "1")
	}
	if o.origin != "" {
		req.Header.Set("Origin", o.origin)
	}
	if o.host != "" {
		req.Host = o.host
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		p.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func (p *panel) ok(method, path string, body any) []byte {
	p.t.Helper()
	resp, b := p.do(method, path, body, opts{cookie: true, header: true})
	if resp.StatusCode != 200 {
		p.t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, b)
	}
	return b
}

func TestNothingIsServedWithoutTheSecret(t *testing.T) {
	p := newPanel(t)
	for _, path := range []string{"/", "/api/state", "/panel.js", "/fonts/barlow-400.woff2"} {
		if resp, _ := p.do("GET", path, nil, opts{}); resp.StatusCode != 403 {
			t.Errorf("GET %s without the cookie: %d", path, resp.StatusCode)
		}
	}
	if resp, _ := p.do("GET", "/?k=wrong", nil, opts{}); resp.StatusCode != 403 {
		t.Errorf("a wrong secret: %d", resp.StatusCode)
	}
	resp, _ := p.do("GET", "/?k=s3cret", nil, opts{})
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/" {
		t.Fatalf("the secret should be traded for a cookie and dropped from the address: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == uiCookie {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Value != "s3cret" {
		t.Errorf("cookie %+v", cookie)
	}
}

func TestAHostNameThatIsNotLoopbackIsRefused(t *testing.T) {
	p := newPanel(t)
	_, port, _ := strings.Cut(p.ui.addr, ":")
	for _, host := range []string{"evil.example:" + port, "evil.example", "192.168.1.5:" + port, "127.0.0.1:1"} {
		if resp, _ := p.do("GET", "/api/state", nil, opts{cookie: true, host: host}); resp.StatusCode != 403 {
			t.Errorf("Host %q: %d (a rebound DNS name must not reach the panel)", host, resp.StatusCode)
		}
	}
	for _, host := range []string{"127.0.0.1:" + port, "localhost:" + port} {
		if resp, _ := p.do("GET", "/api/state", nil, opts{cookie: true, host: host}); resp.StatusCode != 200 {
			t.Errorf("Host %q: %d", host, resp.StatusCode)
		}
	}
}

func TestChangesNeedTheHeaderAndAMatchingOrigin(t *testing.T) {
	p := newPanel(t)
	body := map[string]any{"name": "x"}
	if resp, _ := p.do("PUT", "/api/settings", body, opts{cookie: true}); resp.StatusCode != 403 {
		t.Errorf("no header: %d", resp.StatusCode)
	}
	if resp, _ := p.do("PUT", "/api/settings", body, opts{cookie: true, header: true, origin: "https://evil.example"}); resp.StatusCode != 403 {
		t.Errorf("foreign origin: %d", resp.StatusCode)
	}
	if resp, _ := p.do("PUT", "/api/settings", body, opts{cookie: true, header: true, origin: "http://" + p.ui.addr}); resp.StatusCode != 200 {
		t.Errorf("own origin: %d", resp.StatusCode)
	}
	if resp, _ := p.do("POST", "/api/token", nil, opts{header: true}); resp.StatusCode != 403 {
		t.Errorf("no cookie: %d", resp.StatusCode)
	}
}

func TestThePageIsServedWithAStrictPolicy(t *testing.T) {
	p := newPanel(t)
	resp, body := p.do("GET", "/", nil, opts{cookie: true})
	if resp.StatusCode != 200 || !strings.Contains(string(body), "GWatch Agent") {
		t.Fatalf("index: %d", resp.StatusCode)
	}
	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'self'") || strings.Contains(csp, "unsafe") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP %q", csp)
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Error("the panel must not be cached")
	}
	for _, path := range []string{"/panel.js", "/panel.css", "/agent-logo.png", "/fonts/barlow-400.woff2", "/fonts/kode-mono.woff2"} {
		if r, b := p.do("GET", path, nil, opts{cookie: true}); r.StatusCode != 200 || len(b) == 0 {
			t.Errorf("%s: %d", path, r.StatusCode)
		}
	}
	// Inline script or style would be refused by the policy above; make sure the
	// page does not depend on any.
	if strings.Contains(string(body), "<style") || strings.Contains(strings.ToLower(string(body)), " onclick=") {
		t.Error("the page uses inline style or script, which its own policy forbids")
	}
}

func TestStateNeverCarriesTheToken(t *testing.T) {
	p := newPanel(t)
	b := p.ok("GET", "/api/state", nil)
	if strings.Contains(string(b), oldToken) {
		t.Fatalf("the token is in the state: %s", b)
	}
	var st struct {
		Settings uiSettings `json:"settings"`
		TokenSet bool       `json:"tokenSet"`
		File     struct {
			Path     string `json:"path"`
			Writable bool   `json:"writable"`
		} `json:"file"`
		FileText string `json:"fileText"`
	}
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatal(err)
	}
	if st.Settings.Server != "https://s:7230" || st.Settings.Name != "nas" || !st.TokenSet || !st.File.Writable || !strings.Contains(st.FileText, `"nas"`) || !strings.Contains(st.FileText, tokenMask) {
		t.Errorf("state %+v", st)
	}
	var tok struct{ Token string }
	_ = json.Unmarshal(p.ok("POST", "/api/token", nil), &tok)
	if tok.Token != oldToken {
		t.Errorf("token %q", tok.Token)
	}
}

func TestSavingSettingsWritesTheFileAndRefusesNonsense(t *testing.T) {
	p := newPanel(t)
	p.ok("PUT", "/api/settings", map[string]any{
		"interval": "30s", "name": "renamed", "autoUpdate": false,
		"collect": map[string]any{"metrics": []string{"cpu"}, "interfaces": []string{"eth0"}},
	})
	c, problems, _ := baseConfig()
	if len(problems) > 0 || c.interval.String() != "30s" || c.name != "renamed" || c.autoUpdate || len(c.collect.Interfaces) != 1 {
		t.Fatalf("saved: %+v problems %v", c, problems)
	}
	if c.token != oldToken || c.server != "https://s:7230" {
		t.Errorf("saving settings must leave the token and server alone: %+v", c)
	}

	resp, b := p.do("PUT", "/api/settings", map[string]any{"interval": "1s"}, opts{cookie: true, header: true})
	if resp.StatusCode != 400 || !strings.Contains(string(b), "interval") {
		t.Errorf("an interval that is too short: %d %s", resp.StatusCode, b)
	}
	resp, b = p.do("PUT", "/api/settings", map[string]any{"server": "https://"}, opts{cookie: true, header: true})
	if resp.StatusCode != 400 {
		t.Errorf("a server with no host: %d %s", resp.StatusCode, b)
	}
	if c, _, _ := baseConfig(); c.interval.String() != "30s" {
		t.Error("a refused save changed the file")
	}
}

func TestTheFileEditorChecksWhatItSaves(t *testing.T) {
	p := newPanel(t)
	put := func(text string) (int, string) {
		resp, b := p.do("PUT", "/api/file", map[string]string{"text": text}, opts{cookie: true, header: true})
		return resp.StatusCode, string(b)
	}
	if code, b := put("{\n  \"server\": \n}"); code != 400 || !strings.Contains(b, "line") {
		t.Errorf("bad JSON: %d %s", code, b)
	}
	if code, b := put(`{"serverr": "x"}`); code != 400 || !strings.Contains(b, "serverr") {
		t.Errorf("unknown key: %d %s", code, b)
	}
	if code, _ := put(`[1,2]`); code != 400 {
		t.Errorf("not an object: %d", code)
	}
	if code, b := put(`{"server":"https://new:7230","token":"` + newToken + `","interval":"2m"}`); code != 200 {
		t.Fatalf("a good file: %d %s", code, b)
	}
	c, _, _ := baseConfig()
	if c.server != "https://new:7230" || c.token != newToken || c.interval.String() != "2m0s" {
		t.Errorf("saved %+v", c)
	}
}

func TestServiceControlExplainsWhatItCannotDo(t *testing.T) {
	p := newPanel(t)
	var did []string
	p.ui.control = func(a string) error { did = append(did, a); return nil }
	for _, a := range []string{"start", "stop", "restart"} {
		p.ok("POST", "/api/service/"+a, nil)
	}
	if strings.Join(did, ",") != "start,stop,restart" {
		t.Errorf("did %v", did)
	}
	if resp, _ := p.do("POST", "/api/service/destroy", nil, opts{cookie: true, header: true}); resp.StatusCode != 400 {
		t.Errorf("an unknown action: %d", resp.StatusCode)
	}
	p.ui.control = func(string) error { return errors.New("Failed to restart: Interactive authentication required.") }
	resp, b := p.do("POST", "/api/service/restart", nil, opts{cookie: true, header: true})
	if resp.StatusCode != 403 || !strings.Contains(string(b), "sudo gwatch-agent ui") {
		t.Errorf("a permission failure should say how to get permission: %d %s", resp.StatusCode, b)
	}
}

func TestTestReadingReportsTheOutcomeEitherWay(t *testing.T) {
	p := newPanel(t)
	var out struct {
		OK      bool   `json:"ok"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(p.ok("POST", "/api/test", nil), &out)
	if !out.OK {
		t.Errorf("%+v", out)
	}
	p.ui.testReading = func(context.Context, config) error { return errors.New("the server rejected this token") }
	_ = json.Unmarshal(p.ok("POST", "/api/test", nil), &out)
	if out.OK || !strings.Contains(out.Message, "rejected") {
		t.Errorf("%+v", out)
	}
}

func TestOnlyLoopbackAddressesAreServed(t *testing.T) {
	cfg := defaultConfig()
	for _, addr := range []string{"0.0.0.0:0", "192.168.1.5:9000", ":0", "example.com:80"} {
		if err := runUI(cfg, addr, false); err == nil || !strings.Contains(err.Error(), "own address") {
			t.Errorf("%q: %v", addr, err)
		}
	}
}

func TestAReadOnlyFileSaysSo(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	p := newPanel(t)
	if err := os.Chmod(p.dir, 0o500); err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() { os.Chmod(p.dir, 0o700) })
	resp, b := p.do("PUT", "/api/settings", map[string]any{"name": "x"}, opts{cookie: true, header: true})
	if resp.StatusCode != 403 || !strings.Contains(string(b), "sudo") {
		t.Errorf("%d %s", resp.StatusCode, b)
	}
}

// The editor is shown a mask where the token is. Saving the file with the mask
// untouched must keep the token, and typing over the mask must replace it.
func TestTheEditorNeverNeedsToSeeTheToken(t *testing.T) {
	p := newPanel(t)
	var st struct {
		FileText string `json:"fileText"`
	}
	_ = json.Unmarshal(p.ok("GET", "/api/state", nil), &st)
	if strings.Contains(st.FileText, oldToken) || !strings.Contains(st.FileText, tokenMask) {
		t.Fatalf("editor text:\n%s", st.FileText)
	}
	edited := strings.Replace(st.FileText, `"nas"`, `"edited"`, 1)
	p.ok("PUT", "/api/file", map[string]string{"text": edited})
	c, _, _ := baseConfig()
	if c.name != "edited" || c.token != oldToken {
		t.Errorf("saving with the mask in place changed the token: %+v", c)
	}
	replaced := strings.Replace(edited, tokenMask, newToken, 1)
	p.ok("PUT", "/api/file", map[string]string{"text": replaced})
	if c, _, _ := baseConfig(); c.token != newToken {
		t.Errorf("typing over the mask should set the token: %s", c.token)
	}
}

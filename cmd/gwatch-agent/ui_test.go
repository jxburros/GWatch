package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

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
	ui := newUIServer(cfg, "127.0.0.1:0", "s3cret", "c0de")
	ts := httptest.NewServer(ui.handler())
	t.Cleanup(ts.Close)
	ui.addr = ts.Listener.Addr().String()
	ui.control = func(string) error { return nil }
	ui.state = func() serviceState { return serviceState{Managed: true, State: "running"} }
	ui.testReading = func(context.Context, config) error { return nil }
	ui.checkUpdate = func(context.Context, config) (model.UpdateInfo, error) { return model.UpdateInfo{}, nil }
	ui.installUpdate = func(context.Context, config, model.UpdateInfo) error {
		t.Error("installUpdate was not expected")
		return nil
	}
	ui.pair = func(*config) error { t.Error("pair was not expected"); return nil }
	ui.elevate = nil // whatever desktop the tests run on, never a real prompt
	oldJournal := journalTail
	journalTail = func(string, int) ([]string, error) { return nil, os.ErrNotExist }
	t.Cleanup(func() { journalTail = oldJournal })
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
	if resp, _ := p.do("GET", "/?k=s3cret", nil, opts{}); resp.StatusCode != 403 {
		t.Errorf("the session secret is not an address: %d", resp.StatusCode)
	}
	resp, _ := p.do("GET", "/?k=c0de", nil, opts{})
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

func TestTheAddressWorksOnce(t *testing.T) {
	p := newPanel(t)
	redeemed := 0
	p.ui.onRedeemed = func() { redeemed++ }
	if resp, _ := p.do("GET", "/?k=c0de", nil, opts{}); resp.StatusCode != 303 {
		t.Fatalf("first visit: %d", resp.StatusCode)
	}
	if resp, _ := p.do("GET", "/?k=c0de", nil, opts{}); resp.StatusCode != 403 {
		t.Errorf("the same address a second time: %d", resp.StatusCode)
	}
	if redeemed != 1 {
		t.Errorf("onRedeemed ran %d times", redeemed)
	}
	// The cookie it bought still works.
	p.ok("GET", "/api/state", nil)

	q := newPanel(t)
	q.ui.codeExpires = time.Now().Add(-time.Second)
	if resp, _ := q.do("GET", "/?k=c0de", nil, opts{}); resp.StatusCode != 403 {
		t.Errorf("an address past its time: %d", resp.StatusCode)
	}
}

func TestTheBrowserIsNeverHandedTheCode(t *testing.T) {
	link := "http://127.0.0.1:1234/?k=c0dec0dec0de"
	page, cleanup, err := writeLaunchPage(link)
	if err != nil {
		t.Fatal(err)
	}
	for _, arg := range browserCommand(page).Args {
		if strings.Contains(arg, "c0dec0dec0de") {
			t.Errorf("the opener's command line carries the code: %q", arg)
		}
	}
	b, err := os.ReadFile(page)
	if err != nil || !strings.Contains(string(b), link) {
		t.Fatalf("the page should lead to the panel: %v %s", err, b)
	}
	if runtime.GOOS != "windows" {
		for _, path := range []string{page, filepath.Dir(page)} {
			if fi, err := os.Stat(path); err != nil {
				t.Error(err)
			} else if fi.Mode().Perm()&0o077 != 0 {
				t.Errorf("%s should be private to this account: %v", path, fi.Mode())
			}
		}
	}
	cleanup()
	if _, err := os.Stat(filepath.Dir(page)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the page should be gone after cleanup: %v", err)
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

// What the panel says when this account may not change the file or the service
// does not depend on the platform's file modes, so it is tested with the error
// each platform gives rather than by revoking permissions the platform may not
// enforce.
func TestARefusedChangeSaysHowToGetAccess(t *testing.T) {
	denials := map[string]error{
		"unix":    &fs.PathError{Op: "open", Path: "/etc/gwatch-agent/agent.json", Err: syscall.EACCES},
		"wrapped": fmt.Errorf("create /etc/gwatch-agent: %w", os.ErrPermission),
		"windows": errors.New("open C:\\ProgramData\\GWatch\\agent.json: Access is denied."),
	}
	for name, denial := range denials {
		t.Run(name, func(t *testing.T) {
			p := newPanel(t)
			p.ui.save = func(*configDoc) error { return denial }
			resp, b := p.do("PUT", "/api/settings", map[string]any{"name": "x"}, opts{cookie: true, header: true})
			if resp.StatusCode != 403 || !strings.Contains(string(b), "sudo") {
				t.Errorf("saving: %d %s", resp.StatusCode, b)
			}
			p.ui.control = func(string) error { return denial }
			resp, b = p.do("POST", "/api/service/restart", nil, opts{cookie: true, header: true})
			if resp.StatusCode != 403 || !strings.Contains(string(b), "sudo") {
				t.Errorf("service control: %d %s", resp.StatusCode, b)
			}
		})
	}
	p := newPanel(t)
	p.ui.save = func(*configDoc) error { return errors.New("disk full") }
	resp, b := p.do("PUT", "/api/settings", map[string]any{"name": "x"}, opts{cookie: true, header: true})
	if resp.StatusCode != 500 || strings.Contains(string(b), "sudo") {
		t.Errorf("a failure that is not about permission should not suggest sudo: %d %s", resp.StatusCode, b)
	}
}

// The same thing against the real file system, where the platform enforces file
// modes. A directory the owner has made read-only does not do: saving puts its
// own directory right, as the owner may. What the account cannot do is create a
// directory inside one it may not write to, which is the position of a person
// opening the panel over a configuration that belongs to someone else. Root,
// Windows and some other platforms are not stopped by file modes, and then there
// is nothing here to observe.
func TestAnUnwritableLocationSaysSo(t *testing.T) {
	p := newPanel(t)
	locked := filepath.Join(p.dir, "locked")
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o700) })
	if err := os.Mkdir(filepath.Join(locked, "probe"), 0o700); err == nil {
		t.Skip("this account can write into a read-only directory here")
	}
	p.ui.cfg.configPath = filepath.Join(locked, "agent-dir", "agent.json")
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

func TestAPanelOverAFileItCannotReadSaysHowToGetAccess(t *testing.T) {
	dir := isolate(t)
	path := filepath.Join(dir, "agent.json")
	writeFile(t, path, `{"server":"https://s:7230"}`)
	if err := os.Chmod(path, 0o000); err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() { os.Chmod(path, 0o600) })
	if f, err := os.Open(path); err == nil {
		f.Close()
		t.Skip("this account can read a file with no permissions here (root, or a platform that does not enforce modes)")
	}
	cfg := defaultConfig()
	cfg.configPath = path
	// No administrator prompt here, whatever desktop the tests run on: this
	// is the answer where there is none (over SSH, say).
	t.Setenv("SSH_CONNECTION", "192.0.2.1 22 192.0.2.2 22")
	err := runUI(cfg, "127.0.0.1:0", false)
	if err == nil || !strings.Contains(err.Error(), "sudo gwatch-agent ui") {
		t.Fatalf("want advice to use sudo, got %v", err)
	}
	// A file that is not there yet is not an access problem: the panel is also
	// how a machine gets set up.
	if err := checkConfigReadable(filepath.Join(dir, "not-yet.json")); err != nil {
		t.Errorf("a missing file should be fine: %v", err)
	}
}

// ---- issue #64: log, diagnostics, update now, pairing again ----

const otherToken = "gwa_someothertoken0000000000000"

func TestTheNewActionsAreGuardedLikeTheRest(t *testing.T) {
	p := newPanel(t)
	_, port, _ := strings.Cut(p.ui.addr, ":")
	for _, path := range []string{"/api/log", "/api/diagnostics"} {
		if resp, _ := p.do("GET", path, nil, opts{}); resp.StatusCode != 403 {
			t.Errorf("GET %s without the cookie: %d", path, resp.StatusCode)
		}
		if resp, _ := p.do("GET", path, nil, opts{cookie: true, host: "evil.example:" + port}); resp.StatusCode != 403 {
			t.Errorf("GET %s under a rebound name: %d", path, resp.StatusCode)
		}
	}
	pairBody := map[string]any{"server": "https://s:7230", "code": "ABCD-2345"}
	for _, path := range []string{"/api/update-now", "/api/pair"} {
		for name, o := range map[string]opts{
			"no cookie":      {header: true},
			"no header":      {cookie: true},
			"foreign origin": {cookie: true, header: true, origin: "https://evil.example"},
			"rebound name":   {cookie: true, header: true, host: "evil.example:" + port},
		} {
			if resp, _ := p.do("POST", path, pairBody, o); resp.StatusCode != 403 {
				t.Errorf("POST %s with %s: %d", path, name, resp.StatusCode)
			}
		}
	}
	// And a GET cannot reach either of them.
	for _, path := range []string{"/api/update-now", "/api/pair"} {
		if resp, _ := p.do("GET", path, nil, opts{cookie: true}); resp.StatusCode == 200 {
			t.Errorf("GET %s should not do anything", path)
		}
	}
}

func TestTheLogIsShownWithoutTheToken(t *testing.T) {
	p := newPanel(t)
	var lines []string
	for i := 0; i < 80; i++ {
		lines = append(lines, fmt.Sprintf("2026/10/23 10:00:%02d report %d", i%60, i))
	}
	lines = append(lines, "note: token "+oldToken+" in use", "a rotated one: "+otherToken)
	writeFile(t, logFile(), strings.Join(lines, "\n")+"\n")

	var got recentLog
	json.Unmarshal(p.ok("GET", "/api/log", nil), &got)
	if len(got.Lines) != logTailLines || got.Source != logFile() {
		t.Fatalf("want the last %d lines of %s, got %d from %s", logTailLines, logFile(), len(got.Lines), got.Source)
	}
	text := strings.Join(got.Lines, "\n")
	if strings.Contains(text, oldToken) || strings.Contains(text, otherToken) || !strings.Contains(text, "token removed") {
		t.Errorf("the token reached the panel:\n%s", text)
	}
	if !strings.Contains(got.Lines[len(got.Lines)-3], "report 79") {
		t.Errorf("the lines should be the newest, in order: %q", got.Lines[len(got.Lines)-3:])
	}

	// No log yet: a sentence, not an empty box.
	os.Remove(logFile())
	got = recentLog{}
	json.Unmarshal(p.ok("GET", "/api/log", nil), &got)
	if len(got.Lines) != 0 || got.Note == "" {
		t.Errorf("no log: %+v", got)
	}
}

func TestTheServiceLogRollsOverAndNeverKeepsAToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.log")
	cl, err := openCappedLog(path)
	if err != nil {
		t.Fatal(err)
	}
	line := []byte("report failed with token " + oldToken + strings.Repeat(".", 1000) + "\n")
	for i := 0; i < 2*maxLogFile/len(line); i++ {
		cl.Write(line)
	}
	cl.Close()
	cur, _ := os.ReadFile(path)
	old, _ := os.ReadFile(path + ".1")
	if len(old) == 0 || len(cur) == 0 || len(cur) > maxLogFile || len(old) > maxLogFile {
		t.Errorf("rollover: current %d bytes, previous %d", len(cur), len(old))
	}
	if bytes.Contains(cur, []byte(oldToken)) || bytes.Contains(old, []byte(oldToken)) {
		t.Error("the token was written to the log file")
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
			t.Errorf("the log is %v; it should be the owner's alone", st.Mode().Perm())
		}
	}
}

func TestDiagnosticsAreEverythingButTheToken(t *testing.T) {
	p := newPanel(t)
	writeFile(t, logFile(), "2026/10/23 report failed: the server rejected "+oldToken+"\n")
	var out map[string]string
	json.Unmarshal(p.ok("GET", "/api/diagnostics", nil), &out)
	text := out["text"]
	for _, want := range []string{version, runtime.GOOS + "/" + runtime.GOARCH, "https://s:7230", `"name": "nas"`, "Service:      running", "report failed", "token removed"} {
		if !strings.Contains(text, want) {
			t.Errorf("diagnostics lack %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, oldToken) {
		t.Errorf("diagnostics carry the token:\n%s", text)
	}
}

func TestUpdateNowTakesTheUpdatersPathAndRestarts(t *testing.T) {
	p := newPanel(t)
	// Up to date: nothing is installed.
	resp, b := p.do("POST", "/api/update-now", nil, opts{cookie: true, header: true})
	if resp.StatusCode != 200 || !strings.Contains(string(b), "newest release") {
		t.Fatalf("up to date: %d %s", resp.StatusCode, b)
	}

	info := model.UpdateInfo{UpdateAvailable: true, LatestVersion: "9.9.9"}
	p.ui.checkUpdate = func(context.Context, config) (model.UpdateInfo, error) { return info, nil }
	var installed []string
	p.ui.installUpdate = func(_ context.Context, c config, i model.UpdateInfo) error {
		installed = append(installed, i.LatestVersion+" for "+c.server)
		return nil
	}
	var did []string
	p.ui.control = func(a string) error { did = append(did, a); return nil }
	var out map[string]any
	json.Unmarshal(p.ok("POST", "/api/update-now", nil), &out)
	if out["ok"] != true || strings.Join(installed, ",") != "9.9.9 for https://s:7230" || strings.Join(did, ",") != "restart" {
		t.Errorf("installed %v, did %v, answer %v", installed, did, out["message"])
	}

	// A location this account cannot write: say how to get the rights.
	p.ui.installUpdate = func(context.Context, config, model.UpdateInfo) error {
		return fmt.Errorf("cannot replace it: %w", os.ErrPermission)
	}
	if resp, b := p.do("POST", "/api/update-now", nil, opts{cookie: true, header: true}); resp.StatusCode != 403 || !strings.Contains(string(b), "sudo gwatch-agent ui") {
		t.Errorf("unwritable: %d %s", resp.StatusCode, b)
	}

	// A packaged agent belongs to its package manager (entry 10), and the
	// panel never gets as far as installing anything.
	asPackaged(t, "winget")
	p.ui.installUpdate = func(context.Context, config, model.UpdateInfo) error {
		t.Error("a packaged agent must not install an update")
		return nil
	}
	if resp, b := p.do("POST", "/api/update-now", nil, opts{cookie: true, header: true}); resp.StatusCode != 409 || !strings.Contains(string(b), "winget upgrade") {
		t.Errorf("packaged: %d %s", resp.StatusCode, b)
	}
	var st map[string]any
	json.Unmarshal(p.ok("GET", "/api/state", nil), &st)
	if u, _ := st["updates"].(map[string]any); u == nil || u["canUpdate"] != false || u["manager"] != "winget" {
		t.Errorf("the state should say who updates a packaged agent: %v", st["updates"])
	}
}

func TestPairingAgainRunsPairAndRestartsTheService(t *testing.T) {
	p := newPanel(t)
	writeFile(t, filepath.Join(p.dir, "agent.json"), `{"server":"https://s:7230","token":"`+oldToken+`","certPin":"abcd","name":"nas"}`)
	var seen config
	p.ui.pair = func(c *config) error {
		seen = *c
		c.token, c.pairedAs, c.pairedNode = otherToken, "nas-2", 42
		return nil
	}
	var did []string
	p.ui.control = func(a string) error { did = append(did, a); return nil }

	if resp, _ := p.do("POST", "/api/pair", map[string]any{"server": "https://s:7230"}, opts{cookie: true, header: true}); resp.StatusCode != 400 {
		t.Errorf("no code: %d", resp.StatusCode)
	}

	var out map[string]any
	json.Unmarshal(p.ok("POST", "/api/pair", map[string]any{"server": "https://other:7230", "code": "abcd 2345", "name": "nas-2", "insecure": true}), &out)
	if seen.server != "https://other:7230" || seen.code != "abcd 2345" || seen.name != "nas-2" || !seen.insecure {
		t.Errorf("pair was given %+v", seen)
	}
	if seen.certPin != "" || seen.token != "" {
		t.Error("a different server must start with no pinned certificate and no token")
	}
	if out["ok"] != true || !strings.Contains(fmt.Sprint(out["message"]), `"nas-2"`) || out["link"] != "https://other:7230/#/nodes/42" {
		t.Errorf("answer %v / %v", out["message"], out["link"])
	}
	if strings.Join(did, ",") != "restart" {
		t.Errorf("the service should be restarted onto the new token: %v", did)
	}

	// A failure is reported, not hidden.
	p.ui.pair = func(*config) error {
		return errors.New("pairing failed: The code presented is unknown, expired, cancelled or already used.")
	}
	out = nil
	json.Unmarshal(p.ok("POST", "/api/pair", map[string]any{"server": "https://s:7230", "code": "ABCD2345"}), &out)
	if out["ok"] != false || !strings.Contains(fmt.Sprint(out["message"]), "already used") {
		t.Errorf("failure: %v", out)
	}
}

func TestServiceControlAsksTheSystemForRights(t *testing.T) {
	p := newPanel(t)
	p.ui.control = func(string) error { return errors.New("Access is denied.") }
	var asked []string
	p.ui.elevate = func(a string) error { asked = append(asked, a); return nil }
	p.ok("POST", "/api/service/restart", nil)
	if strings.Join(asked, ",") != "restart" {
		t.Errorf("asked %v", asked)
	}
	// Saying no to the prompt is an answer: the usual advice, not an error.
	p.ui.elevate = func(string) error { return errElevationDeclined }
	if resp, b := p.do("POST", "/api/service/stop", nil, opts{cookie: true, header: true}); resp.StatusCode != 403 || !strings.Contains(string(b), "sudo gwatch-agent ui") {
		t.Errorf("declined: %d %s", resp.StatusCode, b)
	}
}

func TestTheHandedOverPageIsNewAndGoesWithTheDirectory(t *testing.T) {
	dir := t.TempDir()
	page, cleanup, err := writeHandoffPage(dir, "http://127.0.0.1:1/?k=c0de")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(page); !strings.Contains(string(b), "k=c0de") {
		t.Errorf("page %s", b)
	}
	// Never written over something already there.
	if _, _, err := writeHandoffPage(dir, "http://127.0.0.1:1/?k=other"); err == nil {
		t.Error("a second page should not replace the first")
	}
	cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("the directory should be gone once the page has been used")
	}
	if _, _, err := writeHandoffPage(filepath.Join(t.TempDir(), "missing"), "x"); err == nil {
		t.Error("a directory that is not there is refused")
	}
}

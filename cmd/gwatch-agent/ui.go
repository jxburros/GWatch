// The agent's control panel: `gwatch-agent ui`.
//
// A small web page, served on this machine's loopback address, for someone who
// would rather not edit a file in a terminal: what the agent is doing, its
// settings, what it collects (with checkboxes for the interfaces and drives this
// machine actually has), its token, and start/stop/restart.
//
// It is deliberately not part of the service. The service runs as root or
// SYSTEM, and a page hosted by it would let any user on the machine change what
// a root process does. The panel is a short-lived process run by whoever is at
// the keyboard, with that person's rights: it can edit the configuration file
// only if they could edit it themselves, and it can control the service only
// if the operating system lets them. On a machine where the configuration is
// root's, `sudo gwatch-agent ui` is the way in, and the same checks apply.
//
// The agent stays pure Go (no window toolkit, no cgo): the panel is ordinary
// HTML served from the binary and shown by the browser already on the machine.
//
// What guards it, since it can change settings and show the token:
//   - it listens only on a loopback address, and refuses any other host name in
//     the request (so a web page cannot reach it by rebinding a DNS name);
//   - it is opened with a random secret in the address, which becomes a cookie
//     and is then dropped from the address bar; nothing is served without it;
//   - anything that changes something needs a header a web page on another
//     origin cannot set, and an Origin that matches;
//   - it exits after half an hour of nobody using it.
package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/kardianos/service"

	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/web"
)

//go:embed ui
var uiAssets embed.FS

const (
	uiCookie      = "gwatch_agent_panel"
	uiHeader      = "X-GWatch-Panel"
	uiIdleTimeout = 30 * time.Minute
	maxUIBody     = 256 << 10
)

// runUI serves the panel until interrupted or idle.
func runUI(cfg config, addr string, openBrowser bool) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("--ui-listen must be host:port, e.g. 127.0.0.1:0")
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("the control panel only listens on this machine's own address (127.0.0.1, ::1 or localhost), not %q", host)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer ln.Close()

	secret, err := randomHex(24)
	if err != nil {
		return err
	}
	srv := newUIServer(cfg, ln.Addr().String(), secret)
	httpSrv := &http.Server{
		Handler:           srv.handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      3 * time.Minute, // a service restart can take a while
		IdleTimeout:       60 * time.Second,
	}

	link := "http://" + ln.Addr().String() + "/?k=" + secret
	fmt.Printf("GWatch agent control panel: %s\n", link)
	fmt.Println("It runs with your account's rights, only on this machine, and stops by itself after half an hour of not being used. Press Ctrl-C to stop it now.")
	if openBrowser {
		openInBrowser(link)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if time.Since(time.Unix(srv.lastUsed.Load(), 0)) > uiIdleTimeout {
					fmt.Println("Nobody has used the panel for a while; closing it.")
					stop()
					return
				}
			}
		}
	}()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdown)
	}()
	if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// openInBrowser asks the desktop to show a page. Failing is fine: the address
// has been printed.
func openInBrowser(link string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", link)
	case "darwin":
		cmd = exec.Command("open", link)
	default:
		cmd = exec.Command("xdg-open", link)
	}
	if cmd.Start() == nil {
		go func() { _ = cmd.Wait() }()
	}
}

// ---- the server ----

type uiServer struct {
	cfg      config
	addr     string
	secret   string
	lastUsed atomic.Int64
	// Seams for tests: how the service is controlled and asked after.
	control func(action string) error
	state   func() serviceState
	// Reading and updates, so a test need not reach the network.
	testReading func(ctx context.Context, c config) error
	checkUpdate func(ctx context.Context, c config) (model.UpdateInfo, error)
}

type serviceState struct {
	// Managed is false when a package manager owns the service: the panel then
	// says what to run instead of offering buttons that would be refused.
	Managed bool   `json:"managed"`
	Manager string `json:"manager,omitempty"`
	State   string `json:"state"`
	Hint    string `json:"hint,omitempty"`
	Error   string `json:"error,omitempty"`
}

func newUIServer(cfg config, addr, secret string) *uiServer {
	s := &uiServer{cfg: cfg, addr: addr, secret: secret}
	s.lastUsed.Store(time.Now().Unix())
	s.control = s.controlService
	s.state = s.serviceState
	s.testReading = func(ctx context.Context, c config) error {
		c2 := c
		ctx, cancel := context.WithTimeout(ctx, 2*requestTimeout)
		defer cancel()
		metrics, err := collect(ctx, c2, newCollector(c2))
		if err != nil {
			return fmt.Errorf("cannot read this computer's hardware: %w", err)
		}
		return send(ctx, newClient(c2), c2, newRuntime(c2).shape(metrics))
	}
	s.checkUpdate = checkUpdate
	return s
}

func (s *uiServer) handler() http.Handler {
	mux := http.NewServeMux()
	assets, _ := fs.Sub(uiAssets, "ui")
	static := http.FileServer(http.FS(assets))
	mux.Handle("GET /fonts/", http.FileServer(http.FS(web.Assets)))
	mux.Handle("GET /agent-logo.png", http.FileServer(http.FS(web.Assets)))
	mux.Handle("GET /", static)

	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("PUT /api/settings", s.handleSaveSettings)
	mux.HandleFunc("PUT /api/file", s.handleSaveFile)
	mux.HandleFunc("POST /api/token", s.handleToken)
	mux.HandleFunc("POST /api/service/{action}", s.handleService)
	mux.HandleFunc("POST /api/test", s.handleTest)
	mux.HandleFunc("POST /api/update-check", s.handleUpdateCheck)
	return s.guard(mux)
}

// guard is everything between the network and the panel.
func (s *uiServer) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")

		// The name in the request has to be one of this machine's loopback
		// names with the port the panel is on. A page that has taken over a DNS
		// name to point at this address would arrive with its own name here.
		if !s.hostOK(r.Host) {
			http.Error(w, "this panel is only reachable as "+s.addr, http.StatusForbidden)
			return
		}
		s.lastUsed.Store(time.Now().Unix())

		// The first visit carries the secret in the address. Trade it for a
		// cookie and take it out of the address bar and the history.
		if k := r.URL.Query().Get("k"); k != "" {
			if !secretEqual(k, s.secret) {
				http.Error(w, "that address is not valid; run `gwatch-agent ui` again for a fresh one", http.StatusForbidden)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: uiCookie, Value: s.secret, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		c, err := r.Cookie(uiCookie)
		if err != nil || !secretEqual(c.Value, s.secret) {
			http.Error(w, "open the address `gwatch-agent ui` printed", http.StatusForbidden)
			return
		}

		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			// A web page on another origin can send a cookie-bearing request
			// here but cannot set this header without being allowed to by a
			// CORS preflight, which nothing here answers.
			if r.Header.Get(uiHeader) != "1" {
				http.Error(w, "missing panel header", http.StatusForbidden)
				return
			}
			if o := r.Header.Get("Origin"); o != "" {
				u, err := url.Parse(o)
				if err != nil || !s.hostOK(u.Host) {
					http.Error(w, "unexpected origin", http.StatusForbidden)
					return
				}
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxUIBody)
		}
		next.ServeHTTP(w, r)
	})
}

func secretEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// hostOK accepts the address the panel is listening on, and the same port under
// the other names for the loopback address.
func (s *uiServer) hostOK(h string) bool {
	_, port, err := net.SplitHostPort(s.addr)
	if err != nil {
		return false
	}
	name, p, err := net.SplitHostPort(h)
	if err != nil || p != port {
		return false
	}
	if name == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(name, "[]"))
	return ip != nil && ip.IsLoopback()
}

func writeUIJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeUIError(w http.ResponseWriter, status int, msg string) {
	writeUIJSON(w, status, map[string]string{"error": msg})
}

// ---- state ----

// uiSettings is the editable settings as the panel shows them. The token is
// never in it; the panel asks for the token on its own.
type uiSettings struct {
	Server     string                 `json:"server"`
	Name       string                 `json:"name"`
	Interval   string                 `json:"interval"`
	CheckIn    string                 `json:"checkIn"`
	Buffer     string                 `json:"buffer"`
	AutoUpdate bool                   `json:"autoUpdate"`
	Insecure   bool                   `json:"insecure"`
	CertPin    string                 `json:"certPin"`
	Collect    model.CollectSelection `json:"collect"`
}

func (s *uiServer) loadConfig() (config, []string, []string) {
	configPathOverride = s.cfg.configPath
	c, problems, warnings := baseConfig()
	return c, problems, warnings
}

func (s *uiServer) buildState() map[string]any {
	c, problems, warnings := s.loadConfig()
	st := map[string]any{
		"version":  version,
		"platform": runtime.GOOS + "/" + runtime.GOARCH,
		"file": map[string]any{
			"path":     c.configPath,
			"exists":   c.fileExists,
			"writable": configWritable(c.configPath),
			"problems": problems,
			"warnings": warnings,
		},
		"settings": uiSettings{
			Server: c.server, Name: c.name, Interval: formatDuration(c.interval), CheckIn: formatDuration(c.checkIn),
			Buffer: formatDuration(c.buffer), AutoUpdate: c.autoUpdate, Insecure: c.insecure, CertPin: c.certPin,
			Collect: c.collect,
		},
		"tokenSet":     c.token != "",
		"tokenPreview": describeToken(c.token, false),
		"tokenSource":  c.sources["token"],
		"defaults": map[string]any{
			"interval": formatDuration(defaultInterval), "checkIn": formatDuration(defaultCheckIn), "buffer": formatDuration(defaultBuffer),
		},
		"service":  s.state(),
		"fileText": s.fileText(),
		"families": []map[string]string{
			{"key": model.MetricCPU, "label": "Processor use"},
			{"key": model.MetricLoad, "label": "Load average"},
			{"key": model.MetricMemory, "label": "Memory"},
			{"key": model.MetricSwap, "label": "Swap"},
			{"key": model.MetricDisk, "label": "Disk space"},
			{"key": model.MetricInodes, "label": "Inodes (needs disk space)"},
			{"key": model.MetricNet, "label": "Network throughput"},
			{"key": model.MetricDiskIO, "label": "Disk throughput"},
		},
	}
	if status, err := readAgentStatus(); err == nil {
		st["status"] = status
		st["statusAgeSeconds"] = int(time.Since(status.UpdatedAt).Seconds())
	}
	return st
}

func (s *uiServer) handleState(w http.ResponseWriter, r *http.Request) {
	writeUIJSON(w, http.StatusOK, s.buildState())
}

// configWritable asks the operating system rather than guessing from modes: can
// this account put a file where the configuration lives?
func configWritable(path string) bool {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false
	}
	f, err := os.CreateTemp(dir, ".write-test-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

const elevateHint = "this account is not allowed to do that; run `sudo gwatch-agent ui` (on Windows, from an Administrator terminal) to make changes from here"

func isPermissionError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrPermission) {
		return true
	}
	m := strings.ToLower(err.Error())
	for _, frag := range []string{"permission denied", "access is denied", "interactive authentication required", "operation not permitted", "not permitted"} {
		if strings.Contains(m, frag) {
			return true
		}
	}
	return false
}

// ---- saving ----

// uiSettingsPatch is what the panel sends. A pointer means "leave it alone".
type uiSettingsPatch struct {
	Server     *string                 `json:"server"`
	Name       *string                 `json:"name"`
	Interval   *string                 `json:"interval"`
	CheckIn    *string                 `json:"checkIn"`
	Buffer     *string                 `json:"buffer"`
	AutoUpdate *bool                   `json:"autoUpdate"`
	Insecure   *bool                   `json:"insecure"`
	CertPin    *string                 `json:"certPin"`
	Collect    *model.CollectSelection `json:"collect"`
}

func (s *uiServer) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	var p uiSettingsPatch
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeUIError(w, http.StatusBadRequest, "that is not a settings object")
		return
	}
	path := s.cfg.configPath
	doc, err := loadConfigDoc(path)
	if err != nil {
		writeUIError(w, http.StatusConflict, err.Error()+" — fix it in the file editor below")
		return
	}
	setText := func(key string, v *string) {
		if v != nil {
			doc.set(key, strings.TrimSpace(*v))
		}
	}
	setText("server", p.Server)
	setText("name", p.Name)
	setText("certPin", p.CertPin)
	for key, v := range map[string]*string{"interval": p.Interval, "checkIn": p.CheckIn, "buffer": p.Buffer} {
		if v != nil {
			doc.set(key, strings.TrimSpace(*v))
		}
	}
	if p.AutoUpdate != nil {
		doc.set("autoUpdate", *p.AutoUpdate)
	}
	if p.Insecure != nil {
		doc.set("insecure", *p.Insecure)
	}
	if p.Collect != nil {
		doc.set("collect", p.Collect.Normalize())
	}
	if !s.persist(w, doc) {
		return
	}
	st := s.buildState()
	st["saved"] = true
	writeUIJSON(w, http.StatusOK, st)
}

// persist validates a document and writes it, answering the request itself when
// it cannot.
func (s *uiServer) persist(w http.ResponseWriter, doc *configDoc) bool {
	if _, problems := doc.settings(); len(problems) > 0 {
		writeUIJSON(w, http.StatusBadRequest, map[string]any{"error": "the settings are not valid", "problems": problems})
		return false
	}
	if srv, _ := doc.settings(); srv.Server != "" {
		if u, err := url.Parse(strings.TrimRight(ensureScheme(srv.Server), "/")); err != nil || u.Host == "" {
			writeUIJSON(w, http.StatusBadRequest, map[string]any{"error": "the settings are not valid", "problems": []string{"server: write it like https://gwatch.lan:7230"}})
			return false
		}
	}
	if err := doc.save(); err != nil {
		if isPermissionError(err) {
			writeUIError(w, http.StatusForbidden, elevateHint)
			return false
		}
		writeUIError(w, http.StatusInternalServerError, err.Error())
		return false
	}
	return true
}

func ensureScheme(server string) string {
	if !strings.Contains(server, "://") {
		return "https://" + server
	}
	return server
}

// handleSaveFile replaces the whole file with text from the editor, after
// checking that it is what the agent will accept.
func (s *uiServer) handleSaveFile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeUIError(w, http.StatusBadRequest, "that is not a file")
		return
	}
	doc := &configDoc{path: s.cfg.configPath, raw: map[string]json.RawMessage{}}
	if err := json.Unmarshal([]byte(body.Text), &doc.raw); err != nil || doc.raw == nil {
		msg := "that is not a JSON object"
		if err != nil {
			msg = "that is not valid JSON: " + jsonErrorWhere([]byte(body.Text), err)
		}
		writeUIError(w, http.StatusBadRequest, msg)
		return
	}
	// The editor was shown a mask where the token is; saving it unchanged keeps
	// the token the file already has.
	if raw, ok := doc.raw["token"]; ok {
		var tok string
		if json.Unmarshal(raw, &tok) == nil && tok == tokenMask {
			if current, err := loadConfigDoc(s.cfg.configPath); err == nil && current.has("token") {
				doc.raw["token"] = current.raw["token"]
			} else {
				delete(doc.raw, "token")
			}
		}
	}
	if unknown := doc.unknownKeys(); len(unknown) > 0 {
		writeUIJSON(w, http.StatusBadRequest, map[string]any{"error": "the settings are not valid",
			"problems": []string{fmt.Sprintf("%q is not a setting this agent knows; check the spelling", unknown[0])}})
		return
	}
	if !s.persist(w, doc) {
		return
	}
	st := s.buildState()
	st["saved"] = true
	writeUIJSON(w, http.StatusOK, st)
}

// handleToken shows the token. It is a POST so that it is never fetched by
// accident (a prefetch, a link checker) and so it needs the panel header.
func (s *uiServer) handleToken(w http.ResponseWriter, r *http.Request) {
	c, _, _ := s.loadConfig()
	writeUIJSON(w, http.StatusOK, map[string]string{"token": c.token})
}

// tokenMask stands in for the token in the file editor. The editor is part of
// the page that loads without anyone asking to see the token, so the real one
// is not in it; leaving the mask in place on save keeps the token as it is, and
// typing over it replaces it.
const tokenMask = "(unchanged: use Show token above to see it)"

// fileText is the configuration file for the editor, with the token masked.
func (s *uiServer) fileText() string {
	doc, err := loadConfigDoc(s.cfg.configPath)
	if err != nil {
		// A file that cannot be parsed is shown as it is: that is the one case
		// where the editor is how it gets fixed. It may be hiding a token, but
		// it is the person's own file, in the person's own browser.
		b, _ := os.ReadFile(s.cfg.configPath)
		return string(b)
	}
	if !doc.exists {
		return ""
	}
	if fsx, _ := doc.settings(); fsx.Token != "" {
		doc.set("token", tokenMask)
	}
	return string(doc.bytes())
}

// ---- the service ----

func (s *uiServer) serviceHandle() (service.Service, error) {
	return newService(&program{cfg: s.cfg, mode: "status"}, s.cfg, "status")
}

func (s *uiServer) serviceState() serviceState {
	if p, ok := currentPackaging(); ok && p.ownsService {
		return serviceState{Managed: false, Manager: p.manager, State: "managed by " + p.manager, Hint: p.serviceCommand("restart")}
	}
	svc, err := s.serviceHandle()
	if err != nil {
		return serviceState{Managed: true, State: "unknown", Error: err.Error()}
	}
	st, err := svc.Status()
	if err != nil {
		if errors.Is(err, service.ErrNotInstalled) {
			return serviceState{Managed: true, State: "not installed", Hint: "Install it with `gwatch-agent install` (from an Administrator terminal on Windows)."}
		}
		return serviceState{Managed: true, State: "unknown", Error: err.Error()}
	}
	state := map[service.Status]string{service.StatusRunning: "running", service.StatusStopped: "stopped"}[st]
	if state == "" {
		state = "unknown"
	}
	return serviceState{Managed: true, State: state}
}

func (s *uiServer) controlService(action string) error {
	svc, err := s.serviceHandle()
	if err != nil {
		return err
	}
	return service.Control(svc, action)
}

func (s *uiServer) handleService(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	switch action {
	case "start", "stop", "restart":
	default:
		writeUIError(w, http.StatusBadRequest, "unknown action")
		return
	}
	if p, ok := currentPackaging(); ok && p.ownsService {
		writeUIError(w, http.StatusConflict, p.refuseServiceCommand(action).Error())
		return
	}
	if err := s.control(action); err != nil {
		if isPermissionError(err) {
			writeUIError(w, http.StatusForbidden, elevateHint)
			return
		}
		writeUIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Give the service a moment to settle so the answer reflects it.
	time.Sleep(700 * time.Millisecond)
	st := s.buildState()
	st["did"] = action
	writeUIJSON(w, http.StatusOK, st)
}

func (s *uiServer) handleTest(w http.ResponseWriter, r *http.Request) {
	c, problems, _ := s.loadConfig()
	if len(problems) > 0 {
		writeUIError(w, http.StatusConflict, "the settings file has problems: "+strings.Join(problems, "; "))
		return
	}
	if err := c.validate("once"); err != nil {
		writeUIError(w, http.StatusConflict, err.Error())
		return
	}
	if err := s.testReading(r.Context(), c); err != nil {
		writeUIJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		return
	}
	writeUIJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "GWatch accepted a reading from this machine."})
}

func (s *uiServer) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	c, _, _ := s.loadConfig()
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	info, err := s.checkUpdate(ctx, c)
	if err != nil {
		writeUIJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		return
	}
	msg := fmt.Sprintf("This agent (%s) is up to date.", version)
	if info.UpdateAvailable {
		msg = fmt.Sprintf("Agent %s is available. A running agent installs it by itself; to do it now, run `gwatch-agent update`.", info.LatestVersion)
		if p, ok := currentPackaging(); ok {
			msg = fmt.Sprintf("Agent %s is available. %s keeps this copy up to date: %s.", info.LatestVersion, p.manager, p.updateCommand(""))
		}
	}
	writeUIJSON(w, http.StatusOK, map[string]any{"ok": true, "message": msg, "latest": info.LatestVersion, "available": info.UpdateAvailable})
}

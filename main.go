// GWatch is a local-only home network and service monitor. It runs as a
// Windows background service (or a plain console process on any OS), keeps
// its data in an embedded SQLite database and serves its web interface on
// localhost.
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kardianos/service"

	"github.com/jxburros/GWatch/internal/api"
	"github.com/jxburros/GWatch/internal/engine"
	"github.com/jxburros/GWatch/internal/logging"
	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
	"github.com/jxburros/GWatch/internal/update"
)

//go:embed web
var webFiles embed.FS

// version is set at build time with -ldflags "-X main.version=1.2.3".
var version = "dev"

const (
	serviceName    = "GWatch"
	serviceDisplay = "GWatch Network Monitor"
	serviceDesc    = "Monitoring of home network devices, servers and websites. Serves its web interface on http://127.0.0.1:7230 (or on the LAN when remote access is enabled)."
)

// restartExitCode tells a service manager that the process wants to be
// restarted (the recovery action configured at install time restarts it).
const restartExitCode = 3

type config struct {
	dataDir string
	listen  string
}

func usage() {
	fmt.Fprintf(os.Stderr, `GWatch %s — local network & service monitor

Usage:
  gwatch [run] [--data-dir DIR] [--listen 127.0.0.1:7230]   run in the foreground (or as the service when started by Windows)
                                                             use --listen 0.0.0.0:7230 (or Settings › Network) to allow other devices
  gwatch install [--data-dir DIR] [--listen ADDR]            install and start the background service (run as Administrator on Windows)
  gwatch uninstall                                           stop and remove the background service
  gwatch start | stop | restart | status                     control the installed service
  gwatch open                                                open the web interface in your browser
  gwatch version

Environment: GWATCH_DATA_DIR, GWATCH_LISTEN override the defaults.
`, version)
}

func main() {
	args := os.Args[1:]
	cmd := "run"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd = args[0]
		args = args[1:]
	}
	fs := flag.NewFlagSet("gwatch", flag.ContinueOnError)
	fs.Usage = usage
	cfg := config{}
	fs.StringVar(&cfg.dataDir, "data-dir", envOr("GWATCH_DATA_DIR", defaultDataDir()), "directory for the database, logs and backups")
	fs.StringVar(&cfg.listen, "listen", envOr("GWATCH_LISTEN", "127.0.0.1:7230"), "address to serve the web interface on (127.0.0.1:7230 = this computer only, 0.0.0.0:7230 = whole network)")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	switch cmd {
	case "version", "-v", "--version":
		fmt.Printf("gwatch %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
		return
	case "help", "-h", "--help":
		usage()
		return
	case "open":
		openBrowser("http://" + browserHost(cfg.listen))
		return
	}

	if err := validateListen(cfg.listen); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}

	prg := &program{cfg: cfg}
	svcConfig := &service.Config{
		Name:        serviceName,
		DisplayName: serviceDisplay,
		Description: serviceDesc,
		Arguments:   []string{"run", "--data-dir", cfg.dataDir, "--listen", cfg.listen},
		Option:      service.KeyValue{"StartType": "automatic", "OnFailure": "restart", "OnFailureDelayDuration": "5s"},
	}
	svc, err := service.New(prg, svcConfig)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	prg.svc = svc

	switch cmd {
	case "run":
		if err := svc.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "install":
		if err := os.MkdirAll(cfg.dataDir, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "error: cannot create data directory:", err)
			os.Exit(1)
		}
		if err := svc.Install(); err != nil {
			fmt.Fprintln(os.Stderr, "error: install failed:", err, "(on Windows run this from an Administrator prompt)")
			os.Exit(1)
		}
		fmt.Printf("Installed service %q (data in %s).\n", serviceName, cfg.dataDir)
		if err := svc.Start(); err != nil {
			fmt.Fprintln(os.Stderr, "warning: service installed but could not be started:", err)
			os.Exit(1)
		}
		fmt.Printf("Service started. Open http://%s in your browser (or run: gwatch open).\n", browserHost(cfg.listen))
	case "uninstall":
		_ = svc.Stop()
		if err := svc.Uninstall(); err != nil {
			fmt.Fprintln(os.Stderr, "error: uninstall failed:", err)
			os.Exit(1)
		}
		fmt.Printf("Service %q removed. Data in %s was kept.\n", serviceName, cfg.dataDir)
	case "start", "stop", "restart":
		if err := service.Control(svc, cmd); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Printf("Service %s: done.\n", cmd)
	case "status":
		st, err := svc.Status()
		if err != nil {
			fmt.Println("Service status: not installed or unavailable:", err)
			return
		}
		names := map[service.Status]string{service.StatusRunning: "running", service.StatusStopped: "stopped", service.StatusUnknown: "unknown"}
		fmt.Printf("Service status: %s\n", names[st])
	default:
		usage()
		os.Exit(2)
	}
}

// program implements service.Interface.
type program struct {
	cfg    config
	svc    service.Service
	cancel context.CancelFunc
	done   chan struct{}

	restartMu sync.Mutex
	restart   bool // set when the process should come back up after stopping
	helper    bool // a detached "gwatch restart" helper is doing the restart
	mode      string
}

// requestRestart shuts the application down and arranges for it to start
// again (used after a self-update).
func (p *program) requestRestart() {
	p.restartMu.Lock()
	if p.restart {
		p.restartMu.Unlock()
		return
	}
	p.restart = true
	p.restartMu.Unlock()
	if p.mode == "service" && runtime.GOOS == "windows" {
		// A detached copy of the (new) executable asks the service manager
		// to stop and start us; the SCM waits for our graceful shutdown.
		if exe, err := update.Executable(); err == nil {
			cmd := exec.Command(exe, "restart")
			if err := cmd.Start(); err == nil {
				p.restartMu.Lock()
				p.helper = true
				p.restartMu.Unlock()
				go cmd.Wait()
				return
			}
		}
	}
	if p.cancel != nil {
		p.cancel()
	}
}

// afterStop completes a requested restart once the application has shut down.
func (p *program) afterStop() {
	p.restartMu.Lock()
	restart, helper := p.restart, p.helper
	p.restartMu.Unlock()
	if !restart || helper {
		return
	}
	if p.mode == "service" {
		// Leave it to the service manager (recovery action / Restart=always).
		os.Exit(restartExitCode)
	}
	exe, err := update.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "restart: cannot find executable:", err)
		os.Exit(restartExitCode)
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), "GWATCH_RESTARTED=1")
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "restart failed:", err)
		os.Exit(restartExitCode)
	}
	fmt.Println("GWatch restarted as a new process.")
	os.Exit(0)
}

func (p *program) Start(s service.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.done = make(chan struct{})
	mode := "console"
	if !service.Interactive() && (runtime.GOOS == "windows" || os.Getenv("INVOCATION_ID") != "") {
		mode = "service"
	}
	p.mode = mode
	go func() {
		defer close(p.done)
		if err := runApp(ctx, p.cfg, mode, p.requestRestart); err != nil {
			fmt.Fprintln(os.Stderr, "fatal:", err)
			if mode == "console" {
				os.Exit(1)
			}
			return
		}
		p.afterStop()
		if mode == "console" && service.Interactive() {
			// Ctrl+C or a restart request in console mode: leave the
			// process instead of waiting for a service manager.
			os.Exit(0)
		}
	}()
	return nil
}

func (p *program) Stop(s service.Service) error {
	if p.cancel != nil {
		p.cancel()
	}
	select {
	case <-p.done:
	case <-time.After(30 * time.Second):
	}
	return nil
}

// runApp opens the database, starts the engine and serves the API until ctx
// is cancelled or an interrupt arrives. requestRestart is invoked after a
// successful self-update.
func runApp(ctx context.Context, cfg config, mode string, requestRestart func()) error {
	if err := os.MkdirAll(cfg.dataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir %s: %w", cfg.dataDir, err)
	}
	var stdout *os.File
	if mode == "console" {
		stdout = os.Stdout
	}
	log, err := logging.New(filepath.Join(cfg.dataDir, "logs"), stdoutWriter(stdout))
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}
	defer log.Close()
	log.Printf("GWatch %s starting (%s mode), data dir %s", version, mode, cfg.dataDir)

	st, err := store.Open(filepath.Join(cfg.dataDir, "gwatch.db"))
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer st.Close()
	if err := ensureDefaults(ctx, st); err != nil {
		return err
	}

	eng := engine.New(st, log, engine.Options{Version: version, ServiceMode: mode, ListenAddr: cfg.listen, DataDir: cfg.dataDir})
	if err := eng.Start(ctx); err != nil {
		return fmt.Errorf("start engine: %w", err)
	}

	webFS, err := fs.Sub(webFiles, "web")
	if err != nil {
		return err
	}
	lm := &listenManager{base: cfg.listen, log: log}
	updater := &api.Updater{
		Client: &update.Client{}, Version: version, Restart: requestRestart, Log: log,
		Prefs: func() model.UpdateSettings { return eng.Settings().Updates },
		Repo:  func() string { return eng.Settings().General.UpdateRepo },
	}
	// Checks for new releases run in the background, and stop with the
	// service. Whether they run at all is a setting (Settings › Updates).
	go updater.Run(ctx)
	srv := &api.Server{Engine: eng, Store: st, Log: log, Web: webFS, BackupDir: filepath.Join(cfg.dataDir, "backups"), Version: version,
		Updater: updater,
		Network: func() model.NetworkInfo { return lm.info(eng.Settings().General) },
	}
	httpServer := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute, // large backup uploads
		IdleTimeout:       2 * time.Minute,
	}
	lm.server = httpServer

	errCh := make(chan error, 1)
	if err := lm.apply(eng.Settings().General, errCh); err != nil {
		eng.Stop()
		return err
	}
	if mode == "console" {
		fmt.Printf("GWatch %s is running. Open http://%s — press Ctrl+C to stop.\n", version, browserHost(lm.current()))
	}

	// Re-bind when the remote access setting changes.
	updates := eng.Subscribe()
	defer eng.Unsubscribe(updates)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case sig := <-sigCh:
			log.Printf("received %s, shutting down", sig)
			break loop
		case err := <-errCh:
			if err != nil && !errors.Is(err, http.ErrServerClosed) && !lm.closing(err) {
				eng.Stop()
				return fmt.Errorf("http server: %w", err)
			}
		case u := <-updates:
			if u.Kind == "config" {
				if err := lm.apply(eng.Settings().General, errCh); err != nil {
					log.Errorf("listen: %v", err)
					eng.RecordError("network listen", err)
				}
			}
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
	eng.Stop()
	_ = st.Checkpoint(context.Background())
	log.Printf("GWatch stopped")
	return nil
}

// listenManager owns the TCP listener and can move it between the loopback
// address and all interfaces when the remote-access setting changes.
type listenManager struct {
	base   string // address from the command line
	log    *logging.Logger
	server *http.Server

	mu       sync.Mutex
	ln       net.Listener
	addr     string // effective address
	expected map[net.Listener]bool
	lastErr  string
}

// effective returns the address to bind for the given settings.
func effectiveListen(base string, g model.GeneralSettings) string {
	host, port, err := net.SplitHostPort(base)
	if err != nil {
		return base
	}
	if g.RemoteAccess && isLoopbackHost(host) {
		return net.JoinHostPort("", port)
	}
	return base
}

func (m *listenManager) current() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.addr
}

// closing reports whether err came from a listener we deliberately closed.
func (m *listenManager) closing(err error) bool {
	return errors.Is(err, net.ErrClosed) || strings.Contains(err.Error(), "use of closed network connection")
}

// apply (re)binds the listener when the effective address changed.
func (m *listenManager) apply(g model.GeneralSettings, errCh chan error) error {
	want := effectiveListen(m.base, g)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ln != nil && m.addr == want {
		return nil
	}
	old := m.ln
	if old != nil {
		_ = old.Close()
		m.ln = nil
		// Give the OS a moment to release the port before rebinding on all interfaces.
		time.Sleep(150 * time.Millisecond)
	}
	ln, err := listenWithRetry(want)
	if err != nil {
		m.lastErr = err.Error()
		if old != nil {
			// Fall back to the previous address so the UI stays reachable.
			if prev, err2 := listenWithRetry(m.addr); err2 == nil {
				m.ln = prev
				go func() { errCh <- m.server.Serve(prev) }()
			}
		}
		return fmt.Errorf("listen on %s: %w (is another copy of GWatch already running?)", want, err)
	}
	m.lastErr = ""
	m.ln = ln
	m.addr = want
	m.log.Printf("web interface available at http://%s%s", browserHost(want), map[bool]string{true: " (reachable from other devices)", false: " (this computer only)"}[!isLoopbackHost(hostOf(want))])
	go func() { errCh <- m.server.Serve(ln) }()
	return nil
}

// listenWithRetry binds addr, retrying for a while when the port is still
// held by a previous instance (self-update restart).
func listenWithRetry(addr string) (net.Listener, error) {
	deadline := time.Now().Add(2 * time.Second)
	if os.Getenv("GWATCH_RESTARTED") != "" {
		deadline = time.Now().Add(30 * time.Second)
	}
	for {
		ln, err := net.Listen("tcp", addr)
		if err == nil || time.Now().After(deadline) {
			return ln, err
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func (m *listenManager) info(g model.GeneralSettings) model.NetworkInfo {
	m.mu.Lock()
	addr, lastErr := m.addr, m.lastErr
	m.mu.Unlock()
	host, portStr, _ := net.SplitHostPort(addr)
	port := 0
	fmt.Sscanf(portStr, "%d", &port)
	remote := !isLoopbackHost(host)
	info := model.NetworkInfo{ListenAddress: addr, RemoteAccess: remote, PasswordSet: g.AccessPassword != "", Port: port, LocalURL: "http://" + browserHost(addr), LANURLs: []string{}, RestartNeeded: lastErr != ""}
	if hn, err := os.Hostname(); err == nil {
		info.Hostname = hn
	}
	if remote {
		for _, ip := range api.LANAddresses() {
			info.LANURLs = append(info.LANURLs, fmt.Sprintf("http://%s:%d", ip, port))
		}
		if info.Hostname != "" {
			info.LANURLs = append(info.LANURLs, fmt.Sprintf("http://%s:%d", strings.ToLower(info.Hostname), port))
		}
	}
	return info
}

func hostOf(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

// isLoopbackHost reports whether host only binds this computer.
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func stdoutWriter(f *os.File) *os.File {
	if f == nil {
		return nil
	}
	return f
}

// ensureDefaults creates the first dashboard on a fresh database.
func ensureDefaults(ctx context.Context, st *store.Store) error {
	dashboards, err := st.ListDashboards(ctx)
	if err != nil {
		return err
	}
	if len(dashboards) > 0 {
		return nil
	}
	_, err = st.SaveDashboard(ctx, model.Dashboard{Name: "Overview", Widgets: []model.Widget{
		{ID: "w1", Type: "summary", Title: "Overall health", Width: 4, Height: 1},
		{ID: "w2", Type: "attention", Title: "Needs attention", Width: 2, Height: 2},
		{ID: "w3", Type: "groups", Title: "Groups", Width: 2, Height: 2},
		{ID: "w4", Type: "status_list", Title: "All nodes", Width: 2, Height: 2},
		{ID: "w5", Type: "incidents", Title: "Recent incidents", Width: 2, Height: 2},
		{ID: "w6", Type: "latency_chart", Title: "Latency (24h)", Width: 4, Height: 2},
		{ID: "w7", Type: "cert_warnings", Title: "Certificate warnings", Width: 2, Height: 1},
		{ID: "w8", Type: "monitor_health", Title: "Monitor health", Width: 2, Height: 1},
	}})
	return err
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func defaultDataDir() string {
	if runtime.GOOS == "windows" {
		if pd := os.Getenv("ProgramData"); pd != "" {
			return filepath.Join(pd, "GWatch")
		}
		return `C:\ProgramData\GWatch`
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "gwatch")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".local", "share", "gwatch")
	}
	return "data"
}

// validateListen checks the listen address is well formed. Any host is
// accepted: 127.0.0.1 keeps the interface private to this computer, while
// 0.0.0.0 (or Settings › Network › remote access) opens it to the LAN.
func validateListen(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid listen address %q (expected host:port)", addr)
	}
	if port == "" {
		return fmt.Errorf("invalid listen address %q (a port is required)", addr)
	}
	if host == "" || strings.EqualFold(host, "localhost") {
		return nil
	}
	if ip := net.ParseIP(host); ip == nil {
		return fmt.Errorf("invalid listen address %q (the host must be an IP address such as 127.0.0.1 or 0.0.0.0)", addr)
	}
	return nil
}

func browserHost(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		fmt.Println("Open this address in your browser:", url)
	}
}

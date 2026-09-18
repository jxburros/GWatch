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
	"syscall"
	"time"

	"github.com/kardianos/service"

	"github.com/jxburros/GWatch/internal/api"
	"github.com/jxburros/GWatch/internal/engine"
	"github.com/jxburros/GWatch/internal/logging"
	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
)

//go:embed web
var webFiles embed.FS

// version is set at build time with -ldflags "-X main.version=1.2.3".
var version = "dev"

const (
	serviceName    = "GWatch"
	serviceDisplay = "GWatch Network Monitor"
	serviceDesc    = "Local-only monitoring of home network devices, servers and websites. Serves its web interface on http://127.0.0.1:8080."
)

type config struct {
	dataDir string
	listen  string
}

func usage() {
	fmt.Fprintf(os.Stderr, `GWatch %s — local network & service monitor

Usage:
  gwatch [run] [--data-dir DIR] [--listen 127.0.0.1:8080]   run in the foreground (or as the service when started by Windows)
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
	fs.StringVar(&cfg.listen, "listen", envOr("GWATCH_LISTEN", "127.0.0.1:8080"), "localhost address to serve the web interface on")
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
}

func (p *program) Start(s service.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.done = make(chan struct{})
	mode := "console"
	if !service.Interactive() && (runtime.GOOS == "windows" || os.Getenv("INVOCATION_ID") != "") {
		mode = "service"
	}
	go func() {
		defer close(p.done)
		if err := runApp(ctx, p.cfg, mode); err != nil {
			fmt.Fprintln(os.Stderr, "fatal:", err)
			if mode == "console" {
				os.Exit(1)
			}
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
// is cancelled or an interrupt arrives.
func runApp(ctx context.Context, cfg config, mode string) error {
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
	srv := &api.Server{Engine: eng, Store: st, Log: log, Web: webFS, BackupDir: filepath.Join(cfg.dataDir, "backups"), Version: version}
	httpServer := &http.Server{
		Addr:              cfg.listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute, // large backup uploads
		IdleTimeout:       2 * time.Minute,
	}
	ln, err := net.Listen("tcp", cfg.listen)
	if err != nil {
		eng.Stop()
		return fmt.Errorf("listen on %s: %w (is another copy of GWatch already running?)", cfg.listen, err)
	}
	log.Printf("web interface available at http://%s", browserHost(cfg.listen))
	if mode == "console" {
		fmt.Printf("GWatch %s is running. Open http://%s — press Ctrl+C to stop.\n", version, browserHost(cfg.listen))
	}

	errCh := make(chan error, 1)
	go func() { errCh <- httpServer.Serve(ln) }()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	select {
	case <-ctx.Done():
	case sig := <-sigCh:
		log.Printf("received %s, shutting down", sig)
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			eng.Stop()
			return fmt.Errorf("http server: %w", err)
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

// validateListen enforces the local-only rule: the interface may only bind
// to a loopback address.
func validateListen(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid listen address %q (expected host:port)", addr)
	}
	if host == "" || strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("GWatch is local-only: the listen address must be a loopback address such as 127.0.0.1:8080")
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

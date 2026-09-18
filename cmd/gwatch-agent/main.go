// gwatch-agent reports one machine's hardware health to a GWatch server.
//
// It is deliberately one-directional. The agent dials out to GWatch, hands
// over a reading and hangs up; GWatch never connects back, is given no
// credential for this machine, and cannot ask the agent to do anything. The
// token the agent carries is good for exactly one thing on the GWatch side —
// submitting this machine's readings — so a machine running the agent has not
// been opened up to the monitor, it has only been made willing to talk.
//
// Serve mode is the opposite trade and is off unless asked for: the agent
// listens and GWatch (or anything else with the token) reads from it. That
// needs an open port on this machine, which is why push is the default.
//
//	gwatch-agent run --server https://gwatch.lan:8080 --token gwa_…
//	gwatch-agent install --server … --token …     install as a background service
//	gwatch-agent once --server … --token …        send one reading and exit
//	gwatch-agent serve --listen 0.0.0.0:9713 --token …
package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kardianos/service"

	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/sysmetrics"
)

// version is set at build time with -ldflags "-X main.version=1.2.3".
var version = "dev"

const (
	serviceName    = "GWatchAgent"
	serviceDisplay = "GWatch Hardware Agent"
	serviceDesc    = "Reports this computer's processor, memory, disk and network health to a GWatch server. It only sends readings out; nothing can reach this computer through it."
)

const (
	defaultInterval = time.Minute
	minInterval     = 10 * time.Second
	defaultListen   = "0.0.0.0:9713"
	// requestTimeout bounds one report. It is well under the smallest useful
	// interval so a hung server cannot stall the next reading.
	requestTimeout = 20 * time.Second
	// maxBackoff caps the wait between failed reports. The agent keeps trying
	// forever: a server that is down comes back, and the machine it is
	// reporting on is usually not the one that needs attention.
	maxBackoff = 10 * time.Minute
)

type config struct {
	server   string
	token    string
	interval time.Duration
	listen   string
	insecure bool
	name     string
}

func usage() {
	fmt.Fprintf(os.Stderr, `gwatch-agent %s — reports this computer's hardware health to GWatch

The agent only ever sends data out. GWatch is never given a way in.

Usage:
  gwatch-agent run --server URL --token TOKEN [--interval 60s]
        send a reading every interval until stopped
  gwatch-agent once --server URL --token TOKEN
        send a single reading and exit (useful for testing the token)
  gwatch-agent print
        print this computer's reading as JSON and exit; sends nothing
  gwatch-agent serve --token TOKEN [--listen %s]
        expose readings for GWatch to read, instead of pushing them.
        This opens a port on this computer; prefer run unless you need it.
  gwatch-agent install --server URL --token TOKEN [--interval 60s]
        install and start the background service (run as Administrator on Windows)
  gwatch-agent uninstall | start | stop | restart | status
  gwatch-agent version

Get a token from GWatch: Settings › Hardware › Register a machine. It is
shown once, and it can do nothing but submit this machine's readings.

Environment: GWATCH_SERVER, GWATCH_AGENT_TOKEN, GWATCH_AGENT_INTERVAL,
GWATCH_AGENT_LISTEN override the defaults.
`, version, defaultListen)
}

func main() {
	args := os.Args[1:]
	cmd := "run"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd = args[0]
		args = args[1:]
	}

	fs := flag.NewFlagSet("gwatch-agent", flag.ContinueOnError)
	fs.Usage = usage
	cfg := config{}
	fs.StringVar(&cfg.server, "server", os.Getenv("GWATCH_SERVER"), "base URL of the GWatch server, e.g. https://gwatch.lan:8080")
	fs.StringVar(&cfg.token, "token", os.Getenv("GWATCH_AGENT_TOKEN"), "agent token from Settings › Hardware")
	fs.DurationVar(&cfg.interval, "interval", envDuration("GWATCH_AGENT_INTERVAL", defaultInterval), "how often to send a reading")
	fs.StringVar(&cfg.listen, "listen", envOr("GWATCH_AGENT_LISTEN", defaultListen), "serve mode: address to listen on")
	fs.BoolVar(&cfg.insecure, "insecure", false, "accept an untrusted TLS certificate from the server (use only with a self-signed certificate you recognise)")
	fs.StringVar(&cfg.name, "name", "", "override the hostname reported to GWatch")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	switch cmd {
	case "version", "-v", "--version":
		fmt.Printf("gwatch-agent %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
		return
	case "help", "-h", "--help":
		usage()
		return
	case "print":
		if err := printReading(cfg); err != nil {
			fatal(err)
		}
		return
	case "once":
		if err := runOnce(cfg); err != nil {
			fatal(err)
		}
		fmt.Println("Reading accepted.")
		return
	}

	if err := cfg.validate(cmd); err != nil {
		fatal(err)
	}

	prg := &program{cfg: cfg, mode: cmd}
	svc, err := service.New(prg, &service.Config{
		Name:        serviceName,
		DisplayName: serviceDisplay,
		Description: serviceDesc,
		Arguments:   cfg.serviceArgs(cmd),
		Option:      service.KeyValue{"StartType": "automatic", "OnFailure": "restart", "OnFailureDelayDuration": "5s"},
	})
	if err != nil {
		fatal(err)
	}

	switch cmd {
	case "run", "serve":
		if err := svc.Run(); err != nil {
			fatal(err)
		}
	case "install":
		if err := svc.Install(); err != nil {
			fatal(fmt.Errorf("install failed: %w (on Windows run this from an Administrator prompt)", err))
		}
		fmt.Printf("Installed service %q.\n", serviceName)
		if err := svc.Start(); err != nil {
			fatal(fmt.Errorf("service installed but could not be started: %w", err))
		}
		fmt.Println("Service started. This computer will appear under Hardware in GWatch within a minute.")
	case "uninstall":
		_ = svc.Stop()
		if err := svc.Uninstall(); err != nil {
			fatal(fmt.Errorf("uninstall failed: %w", err))
		}
		fmt.Printf("Service %q removed.\n", serviceName)
	case "start", "stop", "restart":
		if err := service.Control(svc, cmd); err != nil {
			fatal(err)
		}
		fmt.Printf("Service %s: done.\n", cmd)
	case "status":
		st, err := svc.Status()
		if err != nil {
			fmt.Println("Service status: not installed or unavailable:", err)
			return
		}
		fmt.Printf("Service status: %s\n", map[service.Status]string{
			service.StatusRunning: "running",
			service.StatusStopped: "stopped",
			service.StatusUnknown: "unknown",
		}[st])
	default:
		usage()
		os.Exit(2)
	}
}

// validate reports a configuration the agent cannot run with, in the words
// someone setting it up would use.
func (c config) validate(cmd string) error {
	if c.token == "" {
		return errors.New("a token is required (GWatch: Settings › Hardware › Register a machine)")
	}
	if cmd == "serve" {
		if _, _, err := net.SplitHostPort(c.listen); err != nil {
			return fmt.Errorf("--listen must be host:port, e.g. %s", defaultListen)
		}
		return nil
	}
	if c.server == "" {
		return errors.New("a server URL is required, e.g. --server https://gwatch.lan:8080")
	}
	if c.interval < minInterval {
		return fmt.Errorf("--interval must be at least %s", minInterval)
	}
	return nil
}

// serviceArgs reconstructs the command line the service manager should use.
// The token is on it, so the service registration is as sensitive as the
// token itself — which is why it can only submit readings.
func (c config) serviceArgs(cmd string) []string {
	mode := "run"
	if cmd == "serve" {
		mode = "serve"
	}
	args := []string{mode, "--token", c.token}
	if mode == "serve" {
		args = append(args, "--listen", c.listen)
	} else {
		args = append(args, "--server", c.server, "--interval", c.interval.String())
	}
	if c.insecure {
		args = append(args, "--insecure")
	}
	if c.name != "" {
		args = append(args, "--name", c.name)
	}
	return args
}

// ingestURL is where readings are posted.
func (c config) ingestURL() string {
	base := strings.TrimRight(strings.TrimSpace(c.server), "/")
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	return base + "/ingest/metrics"
}

// ---- the reporting loop ----

type program struct {
	cfg  config
	mode string

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func (p *program) Start(service.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.mu.Lock()
	p.cancel = cancel
	p.done = make(chan struct{})
	p.mu.Unlock()
	go p.run(ctx)
	return nil
}

func (p *program) Stop(service.Service) error {
	p.mu.Lock()
	cancel, done := p.cancel, p.done
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
		}
	}
	return nil
}

func (p *program) run(ctx context.Context) {
	defer close(p.done)
	// Ctrl-C has to work when this is a foreground process; the service
	// manager calls Stop instead and this signal handler never fires.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	go func() {
		select {
		case <-sig:
			p.Stop(nil)
		case <-ctx.Done():
		}
	}()

	if p.mode == "serve" {
		p.serve(ctx)
		return
	}
	p.report(ctx)
}

// report sends a reading every interval, backing off when the server cannot
// be reached and recovering as soon as it can.
func (p *program) report(ctx context.Context) {
	collector := newCollector(p.cfg)
	client := newClient(p.cfg)
	log.Printf("gwatch-agent %s reporting to %s every %s", version, p.cfg.server, p.cfg.interval)

	backoff := time.Duration(0)
	failures := 0
	for {
		wait := p.cfg.interval
		metrics, err := collect(ctx, p.cfg, collector)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("cannot read this computer's hardware: %v", err)
		} else if err := send(ctx, client, p.cfg, metrics); err != nil {
			if ctx.Err() != nil {
				return
			}
			failures++
			backoff = nextBackoff(backoff, p.cfg.interval)
			wait = backoff
			// A rejected token will not start working on its own, so say so
			// plainly rather than burying it in a retry count.
			log.Printf("report failed (attempt %d, retrying in %s): %v", failures, wait, err)
		} else {
			if failures > 0 {
				log.Printf("reporting again after %d failed attempt(s)", failures)
			}
			failures, backoff = 0, 0
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// nextBackoff doubles the wait up to maxBackoff, starting from the interval.
func nextBackoff(current, interval time.Duration) time.Duration {
	if current <= 0 {
		return interval
	}
	next := current * 2
	if next > maxBackoff {
		return maxBackoff
	}
	return next
}

// send posts one reading.
func send(ctx context.Context, client *http.Client, cfg config, metrics model.HostMetrics) error {
	body, err := json.Marshal(metrics)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.ingestURL(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.token)
	req.Header.Set("User-Agent", "gwatch-agent/"+version)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// Drain a little of the body so the connection can be reused, and so a
	// rejection can be quoted back to whoever is reading the log.
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	if resp.StatusCode/100 == 2 {
		return nil
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("the server rejected this token; register the machine again in GWatch (HTTP %d: %s)",
			resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	return fmt.Errorf("the server answered HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
}

func runOnce(cfg config) error {
	if err := cfg.validate("once"); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*requestTimeout)
	defer cancel()
	metrics, err := collect(ctx, cfg, newCollector(cfg))
	if err != nil {
		return fmt.Errorf("cannot read this computer's hardware: %w", err)
	}
	return send(ctx, newClient(cfg), cfg, metrics)
}

// printReading shows what this machine would report. It contacts nothing, so
// it is the way to see exactly what an agent would send before installing one.
func printReading(cfg config) error {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	metrics, err := collect(ctx, cfg, newCollector(cfg))
	if err != nil {
		return fmt.Errorf("cannot read this computer's hardware: %w", err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(metrics)
}

func newCollector(cfg config) *sysmetrics.Collector {
	c := sysmetrics.NewCollector(model.HostKeyLocal)
	c.AgentVersion = version
	return c
}

// collect takes a reading and applies the one thing the agent overrides: the
// name this machine reports itself under, for the case where the system
// hostname is not what anyone calls it.
func collect(ctx context.Context, cfg config, collector *sysmetrics.Collector) (model.HostMetrics, error) {
	metrics, err := collector.Collect(ctx)
	if err != nil {
		return metrics, err
	}
	if name := strings.TrimSpace(cfg.name); name != "" {
		metrics.Hostname = name
	}
	return metrics, nil
}

// ---- serve mode ----

// serve exposes the reading for GWatch to fetch. Unlike push, this opens a
// port on this machine, so the token is checked in constant time and nothing
// but GET /metrics exists to be reached.
func (p *program) serve(ctx context.Context) {
	collector := newCollector(p.cfg)
	want := []byte(p.cfg.token)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="gwatch-agent"`)
			http.Error(w, "a token is required", http.StatusUnauthorized)
			return
		}
		metrics, err := collect(r.Context(), p.cfg, collector)
		if err != nil {
			http.Error(w, "cannot read this computer's hardware", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(metrics)
	})

	srv := &http.Server{
		Addr:              p.cfg.listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()

	log.Printf("gwatch-agent %s serving readings on http://%s/metrics", version, p.cfg.listen)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("cannot listen on %s: %v", p.cfg.listen, err)
	}
}

// ---- helpers ----

func newClient(cfg config) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if cfg.insecure {
		transport.TLSClientConfig = insecureTLS()
	}
	return &http.Client{Transport: transport, Timeout: requestTimeout}
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return def
	}
	return d
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

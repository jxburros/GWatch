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
// A machine is enrolled either with a token copied from GWatch or with a short
// pairing code typed into it. The code is the same trade in a friendlier
// shape: it is worth one enrolment, for a few minutes, and what it buys is the
// token above, which is then kept on this machine and used from then on.
//
//	gwatch-agent pair --server https://gwatch.lan:8080 --code XXXX-XXXX
//	gwatch-agent run --server https://gwatch.lan:8080 --token gwa_…
//	gwatch-agent install --server … --token …     install as a background service
//	gwatch-agent install --server … --code …      pair, then install the service
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
	"os/exec"
	"os/signal"
	"path/filepath"
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
	code     string
	interval time.Duration
	listen   string
	insecure bool
	name     string
}

func usage() {
	fmt.Fprintf(os.Stderr, `gwatch-agent %s — reports this computer's hardware health to GWatch

The agent only ever sends data out. GWatch is never given a way in.

Usage:
  gwatch-agent pair --server URL --code XXXX-XXXX [--name NAME]
        exchange a pairing code for this machine's own token, save it and
        send one reading to prove it works
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
  gwatch-agent install --server URL --code XXXX-XXXX [--name NAME] [--interval 60s]
        install and start the background service (run as Administrator on
        Windows). With --code the machine is paired first.
  gwatch-agent uninstall | start | stop | restart | status
  gwatch-agent version

Get a pairing code or a token from GWatch, under Hardware. A code is short
enough to type off the screen and is good for one machine for a few minutes;
a token is the long gwa_… string, shown once. Either way, what this machine
ends up holding can do one thing only: submit this machine's readings.

A token obtained with a pairing code is saved to
  %s
readable only by the account that paired the machine. run, once and serve use
it when --token is not given.

Environment: GWATCH_SERVER, GWATCH_AGENT_TOKEN, GWATCH_AGENT_CODE,
GWATCH_AGENT_INTERVAL, GWATCH_AGENT_LISTEN, GWATCH_AGENT_TOKEN_FILE override
the defaults.
`, version, defaultListen, tokenFile())
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
	fs.StringVar(&cfg.code, "code", os.Getenv("GWATCH_AGENT_CODE"), "pairing code shown in GWatch (Hardware › Pair a machine), e.g. XXXX-XXXX")
	fs.DurationVar(&cfg.interval, "interval", envDuration("GWATCH_AGENT_INTERVAL", defaultInterval), "how often to send a reading")
	fs.StringVar(&cfg.listen, "listen", envOr("GWATCH_AGENT_LISTEN", defaultListen), "serve mode: address to listen on")
	fs.BoolVar(&cfg.insecure, "insecure", false, "accept an untrusted TLS certificate from the server (use only with a self-signed certificate you recognise)")
	fs.StringVar(&cfg.name, "name", "", "override the hostname reported to GWatch")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	// Naming both is a mistake worth stopping rather than guessing at: the two
	// would enrol this machine twice, or use a token the person did not mean.
	if cfg.token != "" && cfg.code != "" {
		fatal(errors.New("give either --token or --code, not both: a code is exchanged for a token, so naming both leaves it unclear which machine you meant to enrol"))
	}
	// A machine paired earlier already has its token on disk, so the commands
	// that need one should not have to be told it again.
	if cfg.token == "" && cfg.code == "" {
		if stored, err := readStoredToken(); err == nil {
			cfg.token = stored
		}
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
	case "pair":
		if err := pair(&cfg); err != nil {
			fatal(err)
		}
		return
	}

	// install --code does the pairing first, so that everything after this
	// point is the ordinary token install and there is only one of it.
	if cmd == "install" && cfg.code != "" {
		if err := pair(&cfg); err != nil {
			fatal(err)
		}
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
		return fmt.Errorf("a token is required: pair this machine with %s, or pass --token (GWatch: Hardware › Pair a machine)",
			"gwatch-agent pair --server URL --code XXXX-XXXX")
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

// baseURL is the server as given, tidied up: a bare host is assumed to be
// HTTPS, because the one thing that must never be silently downgraded is the
// connection carrying a credential.
func (c config) baseURL() string {
	base := strings.TrimRight(strings.TrimSpace(c.server), "/")
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	return base
}

// ingestURL is where readings are posted.
func (c config) ingestURL() string { return c.baseURL() + "/ingest/metrics" }

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

// ---- pairing ----

// pairURL is where a pairing code is exchanged for this machine's own token.
func (c config) pairURL() string { return c.baseURL() + "/api/agents/pair" }

// pair exchanges the code for a token, keeps the token, and proves it works by
// sending one reading. On success cfg.token holds the new token, so an install
// started with --code carries straight on as a token install.
func pair(cfg *config) error {
	if strings.TrimSpace(cfg.server) == "" {
		return errors.New("a server URL is required, e.g. --server https://gwatch.lan:8080")
	}
	code := tidyCode(cfg.code)
	if code == "" {
		return errors.New("a pairing code is required, e.g. --code XXXX-XXXX (GWatch: Hardware › Pair a machine)")
	}

	hostname, _ := os.Hostname()
	if n := strings.TrimSpace(cfg.name); n != "" {
		hostname = n
	}
	body, err := json.Marshal(map[string]string{
		"code": code, "hostname": hostname, "os": runtime.GOOS, "arch": runtime.GOARCH, "version": version,
	})
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.pairURL(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "gwatch-agent/"+version)

	resp, err := newClient(*cfg).Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach GWatch at %s: %w", cfg.baseURL(), err)
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("pairing failed: %s", serverMessage(resp.StatusCode, payload))
	}

	var out struct {
		Token string `json:"token"`
		Agent struct {
			Name string `json:"name"`
		} `json:"agent"`
	}
	if err := json.Unmarshal(payload, &out); err != nil || out.Token == "" {
		return fmt.Errorf("GWatch accepted the code but did not return a token; pair the machine again")
	}

	path, err := saveToken(out.Token)
	if err != nil {
		// The token is real and the code is spent, so losing it here would
		// mean going back for another code. Print it rather than swallow it.
		fmt.Fprintf(os.Stderr, "warning: could not save the token to %s: %v\n", tokenFile(), err)
		fmt.Fprintf(os.Stderr, "the token is %s — keep it somewhere only this machine can read\n", out.Token)
	}
	cfg.token = out.Token
	cfg.code = ""

	name := out.Agent.Name
	if name == "" {
		name = hostname
	}
	fmt.Printf("Paired with GWatch as %q.\n", name)
	if path != "" {
		fmt.Printf("Token saved to %s (readable only by this account).\n", path)
	}

	// Sending a reading straight away is the difference between "the code was
	// accepted" and "this works": it exercises the token, the URL and the TLS
	// settings, here, while the person who typed the code is still watching.
	if err := runOnce(*cfg); err != nil {
		return fmt.Errorf("paired, but the first reading did not go through: %w", err)
	}
	fmt.Println("First reading accepted. This computer now appears under Hardware in GWatch.")
	return nil
}

// tidyCode cleans up a code as somebody typed it — spaces, stray dashes, lower
// case. GWatch normalises it again and is the one that decides whether it is a
// code at all; this is only so that a pasted "  abcd efgh " reaches it intact.
func tidyCode(s string) string {
	var sb strings.Builder
	for _, r := range strings.ToUpper(s) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// serverMessage turns a rejection into the sentence GWatch wrote, falling back
// to the raw body when the answer is not one of ours.
func serverMessage(status int, body []byte) string {
	var out struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err == nil && out.Error != "" {
		return out.Error
	}
	return fmt.Sprintf("the server answered HTTP %d: %s", status, strings.TrimSpace(string(body)))
}

// ---- the stored token ----

// tokenFile is where a token obtained by pairing is kept, so that the service
// installed afterwards — which runs with no arguments a person typed — can
// find it, and so that pairing a machine twice is never necessary.
//
// The directory is the agent's half of where GWatch itself keeps its data:
// %ProgramData%\GWatch on Windows, $XDG_DATA_HOME/gwatch or
// ~/.local/share/gwatch elsewhere. Anyone looking for GWatch's files on a
// machine looks there first, which is the whole argument for it.
func tokenFile() string {
	if p := strings.TrimSpace(os.Getenv("GWATCH_AGENT_TOKEN_FILE")); p != "" {
		return p
	}
	return filepath.Join(agentDataDir(), "agent-token")
}

func agentDataDir() string {
	if runtime.GOOS == "windows" {
		if pd := os.Getenv("ProgramData"); pd != "" {
			return filepath.Join(pd, "GWatch")
		}
		return `C:\ProgramData\GWatch`
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); xdg != "" {
		return filepath.Join(xdg, "gwatch")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".local", "share", "gwatch")
	}
	return "."
}

// saveToken writes the token where run and once will find it, and reports
// where that was.
//
// The file is created 0600 and the directory 0700, so on Linux and macOS only
// the account that paired the machine — root, for the usual sudo install — can
// read it. Windows has no mode bits, and a file under %ProgramData%\GWatch
// would otherwise inherit an ACL every user of that computer can read, so the
// inherited entries are replaced with SYSTEM and the administrators group:
// SYSTEM because that is what the installed service runs as, administrators
// because that is who installed it.
//
// Tightening the ACL is best effort. If it does not work the token is still
// written, because the failure to protect it matters far less than it looks:
// an agent token submits one machine's readings and can do nothing else at
// all, and the same token is already on the service's command line where the
// service manager will show it to anyone who asks.
func saveToken(token string) (string, error) {
	path := tokenFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(token + "\n"); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	// A file left over from an earlier pairing keeps whatever mode it had, so
	// the permissions are set again rather than assumed.
	if err := os.Chmod(path, 0o600); err != nil && runtime.GOOS != "windows" {
		return "", err
	}
	restrictWindowsACL(path)
	return path, nil
}

// restrictWindowsACL takes the inherited permissions off the token file and
// leaves SYSTEM and the administrators group. It does nothing anywhere else,
// where the mode bits have already said the same thing.
func restrictWindowsACL(path string) {
	if runtime.GOOS != "windows" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// The well-known SIDs are used rather than the group names, which are
	// translated on a localised Windows and would not match.
	cmd := exec.CommandContext(ctx, "icacls", path, "/inheritance:r",
		"/grant:r", "*S-1-5-18:(R,W)", "/grant:r", "*S-1-5-32-544:(R,W)")
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not restrict permissions on %s: %v: %s\n",
			path, err, strings.TrimSpace(string(out)))
	}
}

// readStoredToken returns the token saved by a previous pairing. A missing
// file is not an error worth reporting: it only means this machine was set up
// with --token, or has not been set up at all, and the caller says so better.
func readStoredToken() (string, error) {
	b, err := os.ReadFile(tokenFile())
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(b))
	if token == "" {
		return "", errors.New("the stored token file is empty")
	}
	return token, nil
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

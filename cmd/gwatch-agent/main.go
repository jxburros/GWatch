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
//	gwatch-agent pair --server https://gwatch.lan:7230 --code XXXX-XXXX
//	gwatch-agent run --server https://gwatch.lan:7230 --token gwa_…
//	gwatch-agent install --server … --token …     install as a background service
//	gwatch-agent install --server … --code …      pair, then install the service
//	gwatch-agent once --server … --token …        send one reading and exit
//	gwatch-agent serve --listen 0.0.0.0:9713 --token …
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kardianos/service"

	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/permissions"
	"github.com/jxburros/GWatch/internal/serviceinstall"
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
	defaultListen   = "127.0.0.1:9713"
	// requestTimeout bounds one report. It is well under the smallest useful
	// interval so a hung server cannot stall the next reading.
	requestTimeout = 20 * time.Second
	// maxBackoff caps the wait between failed reports. The agent keeps trying
	// forever: a server that is down comes back, and the machine it is
	// reporting on is usually not the one that needs attention.
	maxBackoff = 10 * time.Minute
)

type config struct {
	server     string
	token      string
	code       string
	interval   time.Duration
	listen     string
	insecure   bool
	certPin    string
	name       string
	repo       string
	autoUpdate bool
	// hostRoot is where the host's root filesystem is mounted when the agent
	// runs in a container and is watching the machine around it rather than
	// the container (docs/HARDWARE.md#in-a-container). Empty everywhere else.
	hostRoot string
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
  gwatch-agent update [--check]
        install the newest signed agent release (--check only reports one).
        A running agent does this by itself; see below.
  gwatch-agent rollback
        put back the version the last update replaced
  gwatch-agent version

Get a pairing code or a token from GWatch, under Hardware. A code is short
enough to type off the screen and is good for one machine for a few minutes;
a token is the long gwa_… string, shown once. Either way, what this machine
ends up holding can do one thing only: submit this machine's readings.

A token obtained with a pairing code is saved to
  %s
readable only by the account that paired the machine. run and once use
it when --token is not given.

Keeping itself up to date: a running agent checks for a new agent release
every few hours, installs it if the download is signed by a release key this
build trusts, and restarts into it. The machines an agent runs on are rarely
machines anyone logs into, which is the argument for doing this by default;
--auto-update=false, or GWATCH_AGENT_AUTO_UPDATE=off, turns it off and leaves
gwatch-agent update to be run by hand. GWatch is not involved either way: it
is never asked what version to run and cannot make this machine install
anything.%s

Environment: GWATCH_SERVER, GWATCH_AGENT_TOKEN, GWATCH_AGENT_CODE,
GWATCH_AGENT_INTERVAL, GWATCH_AGENT_LISTEN, GWATCH_AGENT_TOKEN_FILE,
GWATCH_AGENT_AUTO_UPDATE, GWATCH_AGENT_REPO, GWATCH_HOST_ROOT override the
defaults.
`, version, defaultListen, tokenFile(), packagedUsage())
}

// packagedUsage is the paragraph a packaged build adds to the usage text, so
// that the description of self-update above is not the last word on a binary
// that does not do it.
func packagedUsage() string {
	p, ok := currentPackaging()
	if !ok {
		return ""
	}
	return fmt.Sprintf(`

This copy was installed from %s, and %s keeps it up to date: it
does not replace itself, whatever the settings above say. To update it,
%s.`, p.source, p.manager, p.updateCommand(""))
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
	fs.StringVar(&cfg.server, "server", os.Getenv("GWATCH_SERVER"), "base URL of the GWatch server, e.g. https://gwatch.lan:7230")
	fs.StringVar(&cfg.token, "token", os.Getenv("GWATCH_AGENT_TOKEN"), "agent token from Settings › Hardware")
	fs.StringVar(&cfg.code, "code", os.Getenv("GWATCH_AGENT_CODE"), "pairing code shown in GWatch (Hardware › Pair a machine), e.g. XXXX-XXXX")
	fs.DurationVar(&cfg.interval, "interval", envDuration("GWATCH_AGENT_INTERVAL", defaultInterval), "how often to send a reading")
	fs.StringVar(&cfg.listen, "listen", envOr("GWATCH_AGENT_LISTEN", defaultListen), "serve mode: address to listen on")
	fs.StringVar(&cfg.certPin, "cert-pin", os.Getenv("GWATCH_AGENT_CERT_PIN"), "pinned SHA-256 server public key")
	fs.BoolVar(&cfg.insecure, "insecure", false, "accept an untrusted TLS certificate from the server (use only with a self-signed certificate you recognise)")
	fs.StringVar(&cfg.name, "name", "", "override the hostname reported to GWatch")
	fs.StringVar(&cfg.repo, "repo", envOr("GWATCH_AGENT_REPO", defaultRepo), "GitHub repository the agent takes its own updates from")
	fs.BoolVar(&cfg.autoUpdate, "auto-update", envBool("GWATCH_AGENT_AUTO_UPDATE", true), "keep this agent up to date from signed agent releases")
	fs.StringVar(&cfg.hostRoot, "host-root", os.Getenv("GWATCH_HOST_ROOT"), "in a container: where the host's / is mounted (e.g. /host), to report the host rather than the container")
	var noStart bool
	fs.BoolVar(&noStart, "no-start", false, "install without starting")
	var updateCheckOnly bool
	fs.BoolVar(&updateCheckOnly, "check", false, "update: report whether a newer agent exists without installing it")
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
	if cfg.token == "" && cfg.code == "" && cmd != "serve" {
		if stored, err := readStoredToken(); err == nil {
			cfg.token = stored
		}
	}
	// The same goes for where to send readings. Pairing keeps the server it
	// paired with beside the token, so that a service started with no
	// arguments at all — the systemd unit a package installs, a Homebrew
	// service — reports to it. Anything given on the command line or in the
	// environment wins; the stored settings only fill in what was not said.
	if strings.TrimSpace(cfg.server) == "" {
		if s, err := readStoredSettings(); err == nil {
			insecureGiven := false
			fs.Visit(func(f *flag.Flag) {
				if f.Name == "insecure" {
					insecureGiven = true
				}
			})
			cfg.applyStored(s, insecureGiven)
		}
	}

	// A packaged build hands the service to the package that installed it.
	// The agent's own service commands would register a second service beside
	// the package's (a GWatchAgent unit next to gwatch-agent.service, both
	// reporting), so they say what to use instead.
	if p, ok := currentPackaging(); ok && p.ownsService {
		switch cmd {
		case "install", "uninstall", "start", "stop", "restart", "status":
			fatal(p.refuseServiceCommand(cmd))
		}
	}

	// Watching the host from inside a container is decided before any reading
	// is taken, and a host root that is not there is an error rather than a
	// warning: an agent that quietly fell back to reading the container would
	// report a machine that does not exist under the name of one that does.
	// Only the commands that take a reading ask; `update --check`, `rollback`
	// or `version` in the same container have no reason to fail over it.
	switch cmd {
	case "run", "serve", "once", "print", "pair", "install":
		if root := strings.TrimSpace(cfg.hostRoot); root != "" {
			if err := sysmetrics.SetHostRoot(root); err != nil {
				fatal(fmt.Errorf("%w (in a container, mount the host with -v /:%s:ro; to report the container itself instead, set GWATCH_HOST_ROOT to nothing)", err, root))
			}
		}
	}

	switch cmd {
	case "version", "-v", "--version":
		fmt.Println(versionLine())
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
	case "update":
		if err := runUpdate(cfg, updateCheckOnly); err != nil {
			fatal(err)
		}
		return
	case "rollback":
		if err := runRollback(); err != nil {
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
		// A distinct status for "not set up yet", so that a service manager
		// can tell it from a crash: the packaged systemd unit does not restart
		// on it (RestartPreventExitStatus=78), which keeps a machine that has
		// been installed but not yet paired from logging the same complaint
		// every few seconds until someone gets round to it.
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(exitNotConfigured)
	}

	prg := &program{cfg: cfg, mode: cmd}
	svc, err := newService(prg, cfg, cmd)
	if err != nil {
		fatal(err)
	}

	switch cmd {
	case "run", "serve":
		if err := svc.Run(); err != nil {
			fatal(err)
		}
	case "install":
		if _, err := saveToken(cfg.token); err != nil {
			fatal(err)
		}
		if err := saveStoredSettings(storedSettings{Server: cfg.baseURL(), Insecure: cfg.insecure, CertPin: cfg.certPin, Name: cfg.name}); err != nil {
			fatal(err)
		}
		if err := svc.Install(); err != nil {
			fatal(fmt.Errorf("install failed: %w (on Windows run this from an Administrator prompt)", err))
		}
		fmt.Printf("Installed service %q.\n", serviceName)
		if noStart {
			return
		}
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

// exitNotConfigured is EX_CONFIG from sysexits.h: the agent has no token or no
// server, which no amount of restarting will fix.
const exitNotConfigured = 78

// versionLine is what `version` prints. The update path checks that a new
// binary's version line contains the version the release claims, so the
// version itself always comes first.
func versionLine() string {
	line := fmt.Sprintf("gwatch-agent %s (%s/%s", version, runtime.GOOS, runtime.GOARCH)
	if p, ok := currentPackaging(); ok {
		line += "; " + p.describe()
	}
	return line + ")"
}

// userAgent identifies this build to GWatch. A packaged build says which
// package, which is how GWatch's logs can tell how an agent was installed
// without a field of its own in the reading.
func userAgent() string {
	if p, ok := currentPackaging(); ok {
		return "gwatch-agent/" + version + " (" + p.by + ")"
	}
	return "gwatch-agent/" + version
}

// validate reports a configuration the agent cannot run with, in the words
// someone setting it up would use.
func (c config) validate(cmd string) error {
	switch cmd {
	case "uninstall", "status", "start", "stop", "restart":
		return nil
	}
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
		return errors.New("a server URL is required, e.g. --server https://gwatch.lan:7230")
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
	if c.certPin != "" {
		args = append(args, "--cert-pin", c.certPin)
	}
	if c.insecure {
		args = append(args, "--insecure")
	}
	if c.name != "" {
		args = append(args, "--name", c.name)
	}
	// The service runs with no arguments a person typed, so anything turned
	// off here has to be turned off on its command line too, or the installed
	// service would quietly go back to the default.
	if !c.autoUpdate {
		args = append(args, "--auto-update=false")
	}
	if r := strings.TrimSpace(c.repo); r != "" && r != defaultRepo {
		args = append(args, "--repo", r)
	}
	if r := strings.TrimSpace(c.hostRoot); r != "" {
		args = append(args, "--host-root", r)
	}
	return args
}

// newService describes the background service to the platform's service
// manager. It is here rather than inline so that the commands which only need
// to ask after the service — update, for one — describe it the same way.
func newService(prg *program, cfg config, cmd string) (service.Service, error) {
	exe := ""
	if cmd == "install" {
		var err error
		exe, err = serviceinstall.Executable("gwatch-agent")
		if err != nil {
			return nil, err
		}
	}
	return service.New(prg, &service.Config{
		Executable:  exe,
		Name:        serviceName,
		DisplayName: serviceDisplay,
		Description: serviceDesc,
		Arguments:   cfg.serviceArgs(cmd),
		Option:      service.KeyValue{"StartType": "automatic", "OnFailure": "restart", "OnFailureDelayDuration": "5s", "SystemdScript": strings.ReplaceAll(serviceinstall.SystemdScript, "/etc/gwatch/gwatch.env", "/etc/gwatch-agent/agent.env")},
	})
}

// agentService is newService for a caller that has no program to run: asking
// whether the service exists, and nothing else.
func agentService(cfg config, cmd string) (service.Service, error) {
	return newService(&program{cfg: cfg, mode: cmd}, cfg, cmd)
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

	// Keeping itself current is only the reporting agent's job. serve mode is
	// driven by GWatch asking for readings, and an agent that restarted itself
	// mid-request would be a worse neighbour than an old one.
	if p.mode == "serve" {
		p.serve(ctx)
		return
	}
	if pk, ok := currentPackaging(); ok {
		// Said once, at start, because it is the answer to "why is this
		// machine behind?" and the log is where someone will look for it.
		log.Printf("automatic updates are off: this agent was installed from %s, which keeps it up to date (%s)", pk.source, pk.updateCommand(""))
	} else if p.cfg.autoUpdate {
		go autoUpdate(ctx, p.cfg, p.restartForUpdate)
	}
	p.report(ctx)
}

// restartForUpdate ends this process so that the service manager starts the
// version that has just been installed in its place.
//
// Exiting is the whole mechanism, and it is deliberately the whole mechanism:
// the alternative is for a service to restart itself, which means a service
// asking its own service manager to stop it while it is running, and that is
// fragile on every platform in a different way. Reporting is stopped first, so
// nothing is half-sent, and the exit status is non-zero because a Windows
// service that exits cleanly is left stopped (see exitUpdated).
func (p *program) restartForUpdate() {
	_ = p.Stop(nil)
	log.Printf("restarting into the newly installed agent")
	os.Exit(exitUpdated)
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
		// A little spread on each wait: twenty machines set up by the same
		// script would otherwise report on the same second of every minute
		// for as long as they run, turning a fleet into a spike the server
		// sees and the network feels.
		wait := jittered(p.cfg.interval, p.cfg.interval/10)
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
	req.Header.Set("User-Agent", userAgent())

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
		return errors.New("a server URL is required, e.g. --server https://gwatch.lan:7230")
	}
	code := tidyCode(cfg.code)
	if code == "" {
		return errors.New("a pairing code is required, e.g. --code XXXX-XXXX (GWatch: Hardware › Pair a machine)")
	}
	pk, packaged := currentPackaging()
	if packaged && pk.dataDir != "" && filepath.Dir(tokenFile()) == pk.dataDir {
		if err := checkDataDirWritable(pk.dataDir); err != nil {
			return err
		}
	}

	// The collector's idea of the name rather than the kernel's: in a
	// container watching its host, the name worth pairing under is the host's.
	hostname := sysmetrics.Hostname()
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
	req.Header.Set("User-Agent", userAgent())

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

	if cfg.insecure && resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		sum := sha256.Sum256(resp.TLS.PeerCertificates[0].RawSubjectPublicKeyInfo)
		cfg.certPin = hex.EncodeToString(sum[:])
		cfg.insecure = false
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
	// Where to report goes beside the token, so that a service given no
	// arguments knows it too. Losing it is not worth failing over — the token
	// is the part that cannot be had again — but it is worth saying, since a
	// service started without --server would then refuse to run.
	if path != "" {
		if err := saveStoredSettings(storedSettings{Server: cfg.baseURL(), Insecure: cfg.insecure, CertPin: cfg.certPin, Name: strings.TrimSpace(cfg.name)}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not save the server address beside the token (%v); give the service --server %s\n", err, cfg.baseURL())
		}
	}

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
	if packaged && path != "" {
		pk.afterPair(path)
	}
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
	// A package that runs the agent as a fixed service keeps its data in one
	// fixed place (see linuxPackageDataDir), whoever paired the machine.
	if p, ok := currentPackaging(); ok && p.dataDir != "" {
		return p.dataDir
	}
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
	if err := permissions.EnsurePrivateDir(filepath.Dir(path)); err != nil {
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
	if err := permissions.EnsurePrivateFile(path); err != nil {
		fmt.Fprintf(os.Stderr, "cannot protect %s: %v\n", path, err)
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

// ---- the stored settings ----

// storedSettings is what pairing learned besides the token: where the server
// is, and the two things about talking to it that were decided when the
// machine was paired. It lets `gwatch-agent run` with no arguments — which is
// how a package's service runs it — report to the server the machine was
// paired with, instead of refusing for want of a --server.
//
// None of it is secret, but it sits beside the token with the same
// permissions, because it is the other half of what the token is for.
type storedSettings struct {
	CertPin  string `json:"certPin,omitempty"`
	Server   string `json:"server"`
	Insecure bool   `json:"insecure,omitempty"`
	Name     string `json:"name,omitempty"`
}

// settingsFile lives beside the token file, wherever that is.
func settingsFile() string {
	return filepath.Join(filepath.Dir(tokenFile()), "agent-settings.json")
}

func saveStoredSettings(s storedSettings) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	path := settingsFile()
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil && runtime.GOOS != "windows" {
		return err
	}
	restrictWindowsACL(path)
	return nil
}

func readStoredSettings() (storedSettings, error) {
	var s storedSettings
	b, err := os.ReadFile(settingsFile())
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("%s: %w", settingsFile(), err)
	}
	if strings.TrimSpace(s.Server) == "" {
		return s, errors.New("the stored settings name no server")
	}
	return s, nil
}

// applyStored fills in what the command line and environment left unsaid.
// It is only called when no server was given, so the stored server is the
// one in use; --insecure is taken from the store only in that case and only
// when it was not given explicitly, because "this server's certificate is
// self-signed" was a statement about that server and nothing else.
func (c *config) applyStored(s storedSettings, insecureGiven bool) {
	c.server = s.Server
	if c.certPin == "" {
		c.certPin = s.CertPin
	}
	if !insecureGiven {
		c.insecure = s.Insecure
	}
	if strings.TrimSpace(c.name) == "" {
		c.name = s.Name
	}
}

// ---- serve mode ----

// serve exposes the reading for GWatch to fetch. Unlike push, this opens a
// port on this machine, so the token is checked in constant time and nothing
// but GET /metrics exists to be reached.
func (p *program) serve(ctx context.Context) {
	srv := &http.Server{
		Addr:              p.cfg.listen,
		Handler:           p.metricsHandler(),
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

// metricsHandler is everything serve mode exposes: one route, behind the
// token. It is separate from serve so that what is reachable can be tested
// without opening a port.
func (p *program) metricsHandler() http.Handler {
	collector := newCollector(p.cfg)
	want := []byte(p.cfg.token)
	limiter := newServeLimiter()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if status := limiter.authenticate(r.RemoteAddr, []byte(got), want, time.Now()); status != 0 {
			if status == http.StatusTooManyRequests {
				w.Header().Set("Retry-After", "60")
				http.Error(w, "too many failed attempts", status)
				return
			}
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
	return mux
}

// ---- helpers ----

func newClient(cfg config) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if cfg.certPin != "" {
		transport.TLSClientConfig = pinnedTLS(cfg.certPin)
	} else if cfg.insecure {
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

// envBool reads an on/off environment variable. Anything unrecognised leaves
// the default alone: a misspelt value must never quietly turn off something
// like automatic updates.
func envBool(key string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
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

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
//	gwatch-agent run                              report using agent.json
//	gwatch-agent install --server … --token …     install as a background service
//	gwatch-agent install --server … --code …      pair, then install the service
//	gwatch-agent once --server … --token …        send one reading and exit
//	GWATCH_AGENT_TOKEN=… gwatch-agent serve --listen 0.0.0.0:9713
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

	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/permissions"
	"github.com/jxburros/GWatch/internal/serviceinstall"
	"github.com/jxburros/GWatch/internal/svc"
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
	// maxBackoff caps the wait between attempts to deliver while GWatch cannot
	// be reached. The agent keeps trying forever: a server that is down comes
	// back, and the machine it is reporting on is usually not the one that needs
	// attention. The cap is short because readings are being held for the
	// moment it does: an agent that waited ten minutes between attempts would
	// leave GWatch looking at a machine up to ten minutes behind for no reason
	// but politeness, and a refused connection costs the server nothing.
	maxBackoff = 2 * time.Minute
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
	// checkIn is how often the agent asks GWatch for its settings, and buffer
	// how long it holds readings while GWatch cannot be reached.
	checkIn time.Duration
	buffer  time.Duration
	// collect is the machine's own limit on what it will report: a list that is
	// empty means no limit, and GWatch narrows within whatever is set here.
	collect model.CollectSelection
	// collector is this machine's half of the agreement that makes it a remote
	// collector: it may run the network checks GWatch assigns it (collector.go).
	// Off unless agent.json (or GWATCH_AGENT_COLLECTOR) says so.
	collector bool
	// hostRoot is where the host's root filesystem is mounted when the agent
	// runs in a container and is watching the machine around it rather than
	// the container (docs/HARDWARE.md#in-a-container). Empty everywhere else.
	hostRoot string

	// Where the settings came from (configfile.go). sources maps a setting to
	// the layer that decided it, tokenSource is where the token in use came
	// from, and fileServer is the server the file named, which is what
	// insecure/certPin there were a statement about.
	configPath  string
	fileExists  bool
	fileServer  string
	tokenSource string
	sources     map[string]string

	// pairedAs and pairedNode are what GWatch called this machine and the node
	// it belongs to, once pair() has succeeded (probe.go, ui_actions.go).
	pairedAs   string
	pairedNode int64
}

func usage() {
	fmt.Fprintf(os.Stderr, `gwatch-agent %s — reports this computer's hardware health to GWatch

The agent only ever sends data out. GWatch is never given a way in.

Usage:
  gwatch-agent pair --server URL --code XXXX-XXXX [--name NAME]
        exchange a pairing code for this machine's own token, save it in the
        configuration file and send one reading to prove it works
  gwatch-agent run
        send a reading every interval until stopped, using the settings in the
        configuration file (flags and environment variables override them)
  gwatch-agent once
        send a single reading and exit (useful for testing the settings)
  gwatch-agent print
        print the reading this computer would send as JSON and exit; sends nothing
  gwatch-agent ui
        open the control panel: status, settings, what is collected and the
        token, in a browser. It runs as you, only on this machine, and closes by
        itself when it is not being used.
  gwatch-agent probe --server URL [--insecure]
        check that URL is a GWatch this computer can reach, without pairing
  gwatch-agent tray [start-at-login|stop-at-login]
        Windows: show the agent's state in the notification area
  gwatch-agent config [show|path|validate] [--show-token]
        show every setting and where it came from, print the configuration
        file's location, or check the file for mistakes
  GWATCH_AGENT_TOKEN=TOKEN gwatch-agent serve [--listen %s]
        expose readings for GWatch to read, instead of pushing them, to
        whoever presents TOKEN. This opens a port on this computer; prefer run
        unless you need it.
  gwatch-agent install [--server URL] [--token TOKEN | --code XXXX-XXXX] [--interval 60s]
        install and start the background service (run as Administrator on
        Windows). With --code the machine is paired first. The service is
        registered with no settings of its own: it reads the configuration file.
  gwatch-agent uninstall | start | stop | restart | status
  gwatch-agent update [--check]
        install the newest signed agent release (--check only reports one).
        A running agent does this by itself; see below.
  gwatch-agent rollback
        put back the version the last update replaced
  gwatch-agent version

Settings live in one plain-text JSON file:
  %s
Open it, change a setting, restart the agent. It holds this machine's token,
so it is readable only by the account that paired the machine. Settings given
as flags or environment variables win over the file; "gwatch-agent config show"
says which is in force. Pair this machine from GWatch (Add node › GWatch agent)
with a short code, or put a token from GWatch's node page in the file.

Whoever holds the token can do one thing: submit this machine's readings. Every
so often the agent also asks GWatch for a small, fixed set of things — which
readings to send (only ever fewer than this machine allows), when to ask again,
and a replacement token while an administrator is rotating this one. GWatch can
not tell it to do anything else.

Keeping itself up to date: a running agent checks for a new agent release
every few hours, installs it if the download is signed by a release key this
build trusts, and restarts into it. The machines an agent runs on are rarely
machines anyone logs into, which is the argument for doing this by default;
--auto-update=false, or "autoUpdate": false in the file, turns it off and leaves
gwatch-agent update to be run by hand. GWatch is not involved either way: it
is never asked what version to run and cannot make this machine install
anything.%s

Environment: GWATCH_SERVER, GWATCH_AGENT_TOKEN, GWATCH_AGENT_CODE,
GWATCH_AGENT_INTERVAL, GWATCH_AGENT_LISTEN, GWATCH_AGENT_CONFIG,
GWATCH_AGENT_TOKEN_FILE (moves the whole data directory), GWATCH_AGENT_AUTO_UPDATE,
GWATCH_AGENT_REPO, GWATCH_HOST_ROOT override the file. A token is best kept in
the file or GWATCH_AGENT_TOKEN: any user of this computer can read a command
line, so run and serve move a --token off theirs as they start.
`, version, defaultListen, configFile(), packagedUsage())
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
	if cmd == "run" || cmd == "serve" {
		args = keepTokenOffCommandLine(cmd, args)
	}
	// `config show` and friends: the word after config is a subcommand, and
	// flag parsing would stop at it.
	configSub := ""
	if cmd == "config" && len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		configSub, args = args[0], args[1:]
	}

	// Where the configuration file is has to be known before the flags are
	// declared, because the file supplies their defaults.
	configPathOverride = configPathFromArgs(args)
	base, problems, warnings := baseConfig()

	fs := flag.NewFlagSet("gwatch-agent", flag.ContinueOnError)
	fs.Usage = usage
	cfg := base
	fs.StringVar(&configPathOverride, "config", configPathOverride, "the configuration file (default: agent.json beside the stored token)")
	fs.StringVar(&cfg.server, "server", base.server, "base URL of the GWatch server, e.g. https://gwatch.lan:7230")
	fs.StringVar(&cfg.token, "token", base.token, "this machine's agent token (normally kept in the configuration file)")
	fs.StringVar(&cfg.code, "code", base.code, "pairing code shown in GWatch (Add node › GWatch agent), e.g. XXXX-XXXX")
	fs.DurationVar(&cfg.interval, "interval", base.interval, "how often to send a reading")
	fs.StringVar(&cfg.listen, "listen", base.listen, "serve mode: address to listen on")
	fs.StringVar(&cfg.certPin, "cert-pin", base.certPin, "pinned SHA-256 server public key")
	fs.BoolVar(&cfg.insecure, "insecure", base.insecure, "accept an untrusted TLS certificate from the server (use only with a self-signed certificate you recognise)")
	fs.StringVar(&cfg.name, "name", base.name, "override the hostname reported to GWatch")
	fs.StringVar(&cfg.repo, "repo", base.repo, "GitHub repository the agent takes its own updates from")
	fs.BoolVar(&cfg.autoUpdate, "auto-update", base.autoUpdate, "keep this agent up to date from signed agent releases")
	fs.StringVar(&cfg.hostRoot, "host-root", base.hostRoot, "in a container: where the host's / is mounted (e.g. /host), to report the host rather than the container")
	var noStart, showToken bool
	fs.BoolVar(&noStart, "no-start", false, "install without starting")
	fs.BoolVar(&showToken, "show-token", false, "config show: print the token in full")
	var updateCheckOnly bool
	fs.BoolVar(&updateCheckOnly, "check", false, "update: report whether a newer agent exists without installing it")
	var uiAddr string
	fs.StringVar(&uiAddr, "ui-listen", "127.0.0.1:0", "ui: loopback address for the control panel (port 0 picks a free one)")
	var uiNoBrowser bool
	fs.BoolVar(&uiNoBrowser, "no-browser", false, "ui: print the address instead of opening a browser")
	fs.StringVar(&uiHandoffDir, "ui-handoff", "", "ui: used when the panel restarts itself with elevation (elevate.go)")
	fs.StringVar(&resultFile, "result-file", "", "probe, install: also write the outcome to this file (for the setup program)")
	resultTarget = &cfg
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	configPathOverride = strings.TrimSpace(configPathOverride)
	cfg.configPath = configFile()

	// Anything given on the command line decided its setting.
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	for flagName, key := range map[string]string{
		"server": "server", "token": "token", "code": "code", "interval": "interval", "listen": "listen",
		"cert-pin": "certPin", "insecure": "insecure", "name": "name", "repo": "repo", "auto-update": "autoUpdate",
		"host-root": "hostRoot",
	} {
		if given[flagName] {
			cfg.sources[key] = srcFlag
		}
	}
	cfg.tokenSource = cfg.sources["token"]

	cfg.dropServerBoundTrust(given["insecure"], given["cert-pin"])

	// A token that is only the one the file holds yields to a pairing code
	// (re-pairing this machine); one that was given explicitly, alongside a code,
	// is a mistake worth stopping rather than guessing at.
	if cfg.token != "" && cfg.code != "" {
		if cfg.tokenSource == srcFlag || cfg.tokenSource == srcEnv {
			fatal(errors.New("give either --token or --code, not both: a code is exchanged for a token, so naming both leaves it unclear which machine you meant to enrol"))
		}
		cfg.token, cfg.tokenSource = "", srcDefault
	}
	// Serve mode's token is the one GWatch presents to read from this machine,
	// not the one this machine presents to push; the stored token is the latter.
	if cmd == "serve" && (cfg.tokenSource == srcFile || cfg.tokenSource == srcLegacy) {
		cfg.token, cfg.tokenSource = "", srcDefault
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

	switch cmd {
	case "version", "-v", "--version":
		fmt.Println(versionLine())
		return
	case "help", "-h", "--help":
		usage()
		return
	case "config":
		if err := runConfigCommand(configSub, cfg, problems, warnings, showToken); err != nil {
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
	case "probe":
		if err := runProbe(cfg); err != nil {
			fatal(err)
		}
		return
	case "tray":
		if err := runTray(cfg, fs.Args()); err != nil {
			fatal(err)
		}
		return
	}

	// A configuration file that cannot be read is not something to guess
	// around. Running on defaults would report a machine to the wrong place, or
	// under the wrong name, without anyone knowing the file was ignored.
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, "error:", p)
		}
		fmt.Fprintf(os.Stderr, "fix the file, or move it aside; `%s config validate` checks it\n", filepath.Base(os.Args[0]))
		os.Exit(exitNotConfigured)
	}
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}

	// Watching the host from inside a container is decided before any reading
	// is taken, and a host root that is not there is an error rather than a
	// warning: an agent that quietly fell back to reading the container would
	// report a machine that does not exist under the name of one that does.
	// Only the commands that take a reading ask; `update --check`, `rollback`
	// or `version` in the same container have no reason to fail over it.
	switch cmd {
	case "run", "serve", "once", "print", "pair", "install", "ui":
		if root := strings.TrimSpace(cfg.hostRoot); root != "" {
			if err := sysmetrics.SetHostRoot(root); err != nil {
				fatal(fmt.Errorf("%w (in a container, mount the host with -v /:%s:ro; to report the container itself instead, set GWATCH_HOST_ROOT to nothing)", err, root))
			}
		}
	}

	switch cmd {
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
	case "ui":
		if err := runUI(cfg, uiAddr, !uiNoBrowser); err != nil {
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

	// An agent paired by an older version gets its configuration file now, so
	// there is one place to edit; and one that is about to be taken up with a
	// replacement token has to be able to write it.
	if cmd == "run" {
		if migrateLegacy(cfg) {
			cfg.fileExists = true
			if cfg.tokenSource == srcLegacy {
				cfg.tokenSource = srcFile
			}
		}
	}

	prg := &program{cfg: cfg, mode: cmd}
	service, err := newService(prg, cfg, cmd)
	if err != nil {
		fatal(err)
	}

	switch cmd {
	case "run", "serve":
		if err := service.Run(); err != nil {
			fatal(err)
		}
	case "install":
		// Everything the service needs is written to the configuration file,
		// and the service is registered with nothing but `run`: the token is
		// not left on a command line any user of the machine can read, and
		// changing a setting later is an edit and a restart, not a reinstall.
		if err := persistConfig(cfg); err != nil {
			fatal(err)
		}
		if err := service.Install(); err != nil {
			fatal(fmt.Errorf("install failed: %w (on Windows run this from an Administrator prompt)", err))
		}
		fmt.Printf("Installed service %q. Its settings are in %s.\n", serviceName, cfg.configPathOrDefault())
		if noStart {
			return
		}
		if err := service.Start(); err != nil {
			fatal(fmt.Errorf("service installed but could not be started: %w", err))
		}
		fmt.Println("Service started. This computer will appear in GWatch within a minute.")
		noteInstallResult(nil, "running")
	case "uninstall":
		_ = service.Stop()
		if err := service.Uninstall(); err != nil {
			fatal(fmt.Errorf("uninstall failed: %w", err))
		}
		fmt.Printf("Service %q removed.\n", serviceName)
	case "start", "stop", "restart":
		if err := service.Control(cmd); err != nil {
			fatal(err)
		}
		fmt.Printf("Service %s: done.\n", cmd)
	case "status":
		st, err := service.Status()
		if err != nil {
			fmt.Println("Service status: not installed or unavailable:", err)
			return
		}
		fmt.Printf("Service status: %s\n", st)
	default:
		usage()
		os.Exit(2)
	}
}

// dropServerBoundTrust forgets the file's insecure and certPin when the agent
// has been pointed at a different server for this run. Both were decisions about
// the server the file names; pointing the agent somewhere else must not carry
// them along, or "accept any certificate" would follow a credential to a server
// it was never about. Anything given explicitly this run is kept.
func (c *config) dropServerBoundTrust(insecureGiven, pinGiven bool) {
	if c.fileServer == "" || strings.TrimRight(c.server, "/") == strings.TrimRight(c.fileServer, "/") {
		return
	}
	if !insecureGiven && c.sources["insecure"] != srcEnv {
		c.insecure = false
	}
	if !pinGiven && c.sources["certPin"] != srcEnv {
		c.certPin = ""
	}
}

// configPathFromArgs finds --config before flags are parsed, since the file it
// names supplies the other flags' defaults.
func configPathFromArgs(args []string) string {
	for i, a := range args {
		switch {
		case a == "--config" || a == "-config":
			if i+1 < len(args) {
				return strings.TrimSpace(args[i+1])
			}
		case strings.HasPrefix(a, "--config="):
			return strings.TrimSpace(strings.TrimPrefix(a, "--config="))
		case strings.HasPrefix(a, "-config="):
			return strings.TrimSpace(strings.TrimPrefix(a, "-config="))
		}
	}
	return ""
}

// tokenEnv is the environment variable that carries the token, and the one a
// token given on the command line is moved into.
const tokenEnv = "GWATCH_AGENT_TOKEN"

// keepTokenOffCommandLine takes a --token out of the command line of the
// modes that keep running. Any user of the machine can read a process's command
// line for as long as it runs, and the token is this machine's credential (or,
// for serve, the one that reads it). Where the operating system can replace
// the process in place, the agent starts itself again with the token in its
// environment, which only its own account can read, and nothing else changes:
// same process, same settings, the token still winning over the file. Where it
// cannot, it says so and carries on, since a service installed by an older
// agent was registered with the token as an argument and must keep working.
func keepTokenOffCommandLine(cmd string, args []string) []string {
	token, rest, found := splitTokenArg(args)
	if !found || strings.TrimSpace(token) == "" {
		return args
	}
	advice := fmt.Sprintf("keep the token in %s, or give it in %s", configFile(), tokenEnv)
	if cmd == "serve" {
		advice = "give serve's token in " + tokenEnv
	}
	exe, err := os.Executable()
	if err == nil {
		argv, env := reexecArgs(os.Args[0], cmd, rest, token, os.Environ())
		fmt.Fprintf(os.Stderr, "note: the token given with --token has been taken off the command line, where other users of this computer could read it; to avoid this step, %s\n", advice)
		err = reexec(exe, argv, env)
	}
	fmt.Fprintf(os.Stderr, "warning: --token leaves the token on the command line, where other users of this computer may be able to read it (%v); %s\n", err, advice)
	return args
}

// splitTokenArg finds every --token on a command line (the last one is the one
// that counts, as with any flag) and returns its value and the command line
// without them. Nothing after "--" is a flag.
func splitTokenArg(args []string) (token string, rest []string, found bool) {
	rest = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		switch {
		case a == "--token" || a == "-token":
			found = true
			if i+1 < len(args) {
				token = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--token="):
			found, token = true, strings.TrimPrefix(a, "--token=")
		case strings.HasPrefix(a, "-token="):
			found, token = true, strings.TrimPrefix(a, "-token=")
		default:
			rest = append(rest, a)
		}
	}
	return token, rest, found
}

// reexecArgs is the command line and environment the agent starts itself again
// with: the same command and flags less the token, which moves to tokenEnv.
func reexecArgs(argv0, cmd string, rest []string, token string, environ []string) ([]string, []string) {
	argv := append([]string{argv0, cmd}, rest...)
	env := make([]string, 0, len(environ)+1)
	for _, kv := range environ {
		if !strings.HasPrefix(kv, tokenEnv+"=") {
			env = append(env, kv)
		}
	}
	return argv, append(env, tokenEnv+"="+token)
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
		return fmt.Errorf("a token is required: pair this machine with %s, or put one in %s (GWatch: Add node › GWatch agent)",
			"gwatch-agent pair --server URL --code XXXX-XXXX", c.configPathOrDefault())
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

// serviceArgs is the command line the service manager is given. It is nearly
// empty on purpose: every setting is in the configuration file, so the service
// reads them when it starts and changing one is an edit and a restart. The only
// thing that has to travel on the command line is where the file is, and only
// when it is not in its usual place. (A service installed by an older agent
// still carries its settings as arguments, and they still win; `config show`
// says so when they do.)
func (c config) serviceArgs(cmd string) []string {
	mode := "run"
	if cmd == "serve" {
		mode = "serve"
	}
	args := []string{mode}
	if override := strings.TrimSpace(configPathOverride); override != "" {
		args = append(args, "--config", override)
	}
	return args
}

// agentEnvFile is the optional environment file the Linux service reads
// before it starts, for settings given as GWATCH_AGENT_* variables.
const agentEnvFile = "/etc/gwatch-agent/agent.env"

// newService describes the background service to the platform's service
// manager. It is here rather than inline so that the commands which only need
// to ask after the service — update, for one — describe it the same way.
//
// Windows restarts the service five seconds after it fails (svc's default
// RestartDelay); the systemd unit says the same with RestartSec=, and launchd
// restarts it whenever it exits. On a Linux machine without systemd the
// OpenRC or System V script reads the same environment file the unit does.
func newService(prg *program, cfg config, cmd string) (*svc.Service, error) {
	exe := ""
	if cmd == "install" {
		var err error
		exe, err = serviceinstall.Executable("gwatch-agent")
		if err != nil {
			return nil, err
		}
	}
	return svc.New(prg, svc.Config{
		Executable:  exe,
		Name:        serviceName,
		DisplayName: serviceDisplay,
		Description: serviceDesc,
		Arguments:   cfg.serviceArgs(cmd),
		SystemdUnit: strings.ReplaceAll(serviceinstall.SystemdScript, "/etc/gwatch/gwatch.env", agentEnvFile),
		EnvFile:     agentEnvFile,
	})
}

// agentService is newService for a caller that has no program to run: asking
// whether the service exists, and nothing else.
func agentService(cfg config, cmd string) (*svc.Service, error) {
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

func (p *program) Start(*svc.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.mu.Lock()
	p.cancel = cancel
	p.done = make(chan struct{})
	p.mu.Unlock()
	go p.run(ctx)
	return nil
}

func (p *program) Stop(*svc.Service) error {
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

	if p.cfg.configPath != "" {
		keepConfigBackup(p.cfg.configPath)
	}
	defer startServiceLog()() // agentlog.go: a copy of the log the control panel can show

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
// be reached and recovering as soon as it can. Readings that could not be
// delivered are held, oldest first, and sent when it can, so the last few hours
// are not thrown away just because the network was down for them; they are kept
// in a small bounded file beside agent.json while they wait, so a restart or a
// power cut during the outage does not throw them away either (spool.go).
func (p *program) report(ctx context.Context) {
	collector := newCollector(p.cfg)
	client := newClient(p.cfg)
	rt := newRuntime(p.cfg)
	queue := openReadingQueue(p.cfg, queueFile())
	// Whatever is still held when the agent stops is on disk for the next run.
	defer queue.sync()
	// The collector's sender and the check-in run beside this loop. Stopping
	// waits for them too: the sender writes the results it holds to disk as
	// it leaves, and a check-in may be saving a replacement token, and the
	// service manager ends the process as soon as this returns.
	var side sync.WaitGroup
	defer side.Wait()
	log.Printf("gwatch-agent %s reporting to %s every %s (settings: %s)", version, p.cfg.server, p.cfg.interval, p.cfg.configPathOrDefault())
	if ov := overrides(p.cfg); len(ov) > 0 {
		for _, o := range ov {
			log.Printf("note: %s", o)
		}
	}
	if p.cfg.collector {
		col := newCollectorRunner(ctx, p.cfg, rt, client)
		rt.mu.Lock()
		rt.collector = col
		rt.mu.Unlock()
		log.Print(describeCollector(p.cfg))
		side.Add(1)
		go func() { defer side.Done(); col.sendLoop(ctx) }()
	}
	rt.writeStatus(p.cfg, queue.len())
	side.Add(1)
	go func() { defer side.Done(); p.checkInLoop(ctx, rt, client) }()

	// Taking readings and delivering them are two clocks. Readings are taken
	// every interval, always, so that what is held through an outage is a
	// reading per interval and not a reading per attempt; delivery is tried at
	// the same pace while all is well and backs off while it is not.
	backoff := time.Duration(0)
	failures := 0
	var nextAttempt time.Time
	for {
		// A little spread on each wait: twenty machines set up by the same
		// script would otherwise report on the same second of every minute
		// for as long as they run, turning a fleet into a spike the server
		// sees and the network feels.
		wait := jittered(p.cfg.interval, p.cfg.interval/10)
		rt.watch(collector)
		metrics, err := takeReading(ctx, p.cfg, collector)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("cannot read this computer's hardware: %v", err)
			rt.noteReport(err, failures)
		} else {
			queue.add(rt.shape(metrics))
			// While a failed delivery is being waited out the reading is simply
			// held; the status keeps saying what the last attempt came to.
			if !time.Now().Before(nextAttempt) {
				sent, err := queue.flush(ctx, func(m model.HostMetrics) error {
					c := p.cfg
					c.token = rt.currentToken()
					return send(ctx, client, c, m)
				})
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					failures++
					backoff = nextBackoff(backoff, p.cfg.interval)
					nextAttempt = time.Now().Add(backoff)
					// A rejected token will not start working on its own, so say so
					// plainly rather than burying it in a retry count.
					log.Printf("report failed (attempt %d, trying again in %s, %d reading(s) held): %v", failures, backoff, queue.len(), err)
					rt.noteReport(err, failures)
				} else {
					if failures > 0 {
						log.Printf("reporting again after %d failed attempt(s); %d reading(s) delivered", failures, sent)
					}
					failures, backoff, nextAttempt = 0, 0, time.Time{}
					rt.noteReport(nil, 0)
				}
			}
		}
		queue.sync()
		rt.writeStatus(p.cfg, queue.len())

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

// send posts one reading. A failure says whether sending the same reading again
// later can help: an unreachable server, a busy one and a rejected token can all
// mend; a reading GWatch calls malformed will not.
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
	if isHeld(cfg, metrics, time.Now()) {
		req.Header.Set(heldHeader, "1")
	}

	resp, err := client.Do(req)
	if err != nil {
		return &sendFailure{err: err, retry: true}
	}
	defer resp.Body.Close()
	// Drain a little of the body so the connection can be reused, and so a
	// rejection can be quoted back to whoever is reading the log.
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	if resp.StatusCode/100 == 2 {
		return nil
	}
	retry := resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests ||
		resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusUnauthorized
	if resp.StatusCode == http.StatusUnauthorized {
		return &sendFailure{retry: retry, err: fmt.Errorf("the server rejected this token; check it against the one GWatch shows for this machine, or pair it again (HTTP %d: %s)",
			resp.StatusCode, strings.TrimSpace(string(snippet)))}
	}
	return &sendFailure{retry: retry, err: fmt.Errorf("the server answered HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))}
}

// heldHeader marks a reading that is being delivered late, after it was held
// through an outage. GWatch keeps the time such a reading was taken, and refuses
// one whose time it cannot believe rather than filing it as current: a reading
// from yesterday stamped "now" would be judged as if the machine were in that
// state now. A live reading carries no mark, so a machine whose clock is simply
// wrong still has its readings filed, at the time they arrive.
const heldHeader = "X-GWatch-Held"

// isHeld is whether a reading is being sent late: taken more than an interval
// ago by this machine's own clock, which is the clock that took it, so the
// answer does not depend on whether this clock agrees with GWatch's.
func isHeld(cfg config, m model.HostMetrics, now time.Time) bool {
	interval := cfg.interval
	if interval <= 0 {
		interval = time.Minute
	}
	return !m.Timestamp.IsZero() && now.Sub(m.Timestamp) > interval
}

func runOnce(cfg config) error {
	if err := cfg.validate("once"); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*requestTimeout)
	defer cancel()
	rt, collector := newRuntime(cfg), newCollector(cfg)
	rt.watch(collector)
	metrics, err := collect(ctx, cfg, collector)
	if err != nil {
		return fmt.Errorf("cannot read this computer's hardware: %w", err)
	}
	return send(ctx, newClient(cfg), cfg, rt.shape(metrics))
}

// printReading shows what this machine would report. It contacts nothing, so
// it is the way to see exactly what an agent would send before installing one.
func printReading(cfg config) error {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	rt, collector := newRuntime(cfg), newCollector(cfg)
	rt.watch(collector)
	metrics, err := collect(ctx, cfg, collector)
	if err != nil {
		return fmt.Errorf("cannot read this computer's hardware: %w", err)
	}
	metrics = rt.shape(metrics)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(metrics)
}

func newCollector(cfg config) *sysmetrics.Collector {
	c := sysmetrics.NewCollector(model.HostKeyLocal)
	c.AgentVersion = version
	// The services and programs named in this machine's own file. The
	// reporting loop narrows this to GWatch's choice before each reading
	// (agentRuntime.watch); serve mode, which GWatch never chooses for,
	// watches exactly these.
	c.SetWatch(cfg.collect.WatchedServices(), cfg.collect.WatchedProcesses())
	return c
}

// takeReading is how the reporting loop takes each reading. It is collect;
// tests replace it so that what they time is the loop, not this machine's
// hardware.
var takeReading = collect

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
		return errors.New("a pairing code is required, e.g. --code XXXX-XXXX (GWatch: Add node › GWatch agent)")
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
			Name   string `json:"name"`
			NodeID *int64 `json:"nodeId"`
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
	cfg.token = out.Token
	cfg.tokenSource = srcFile
	cfg.code = ""
	cfg.configPath = configFile()
	if cfg.sources == nil {
		cfg.sources = map[string]string{}
	}
	if name := strings.TrimSpace(cfg.name); name != "" {
		cfg.sources["name"] = srcFlag
	}
	// The token and the server it belongs to go into the configuration file,
	// so a service started with no arguments — a package's systemd unit, a
	// Homebrew service — reports to the server this machine was paired with.
	// The code is spent whatever happens next, so a failure to keep the token
	// prints it rather than losing it.
	path := cfg.configPath
	if err := persistConfig(*cfg); err != nil {
		path = ""
		fmt.Fprintf(os.Stderr, "warning: could not save the token to %s: %v\n", cfg.configPath, err)
		fmt.Fprintf(os.Stderr, "the token is %s — keep it somewhere only this machine can read\n", out.Token)
	}

	name := out.Agent.Name
	if name == "" {
		name = hostname
	}
	fmt.Printf("Paired with GWatch as %q.\n", name)
	cfg.pairedAs = name
	if out.Agent.NodeID != nil {
		cfg.pairedNode = *out.Agent.NodeID
	}
	fmt.Printf("See it in GWatch: %s\n", machineLink(*cfg))
	if path != "" {
		fmt.Printf("Token and settings saved to %s (readable only by this account); edit that file to change them.\n", path)
	}

	// Sending a reading straight away is the difference between "the code was
	// accepted" and "this works": it exercises the token, the URL and the TLS
	// settings, here, while the person who typed the code is still watching.
	if err := runOnce(*cfg); err != nil {
		return fmt.Errorf("paired, but the first reading did not go through: %w", err)
	}
	fmt.Println("First reading accepted. This computer now appears in GWatch.")
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

// ---- where the agent keeps its files ----

// tokenFile is where an older agent kept its token, and now only what decides
// the data directory: the configuration file lives beside it. GWATCH_AGENT_TOKEN_FILE
// moves the whole directory, which is how tests and unusual installs point the
// agent somewhere else.
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

// restrictWindowsACL takes the inherited permissions off a file holding a
// credential and leaves SYSTEM and the administrators group. It does nothing
// anywhere else, where the mode bits have already said the same thing.
//
// Tightening the ACL is best effort. If it does not work the file is still
// written, because the failure to protect it matters far less than it looks:
// an agent token submits one machine's readings and can do nothing else at all.
func restrictWindowsACL(path string) {
	if err := permissions.EnsurePrivateFile(path); err != nil {
		fmt.Fprintf(os.Stderr, "cannot protect %s: %v\n", path, err)
	}
}

// readStoredToken returns the token this machine holds: the configuration
// file's, or failing that the one an older agent saved. A missing file is not
// an error worth reporting: it only means this machine was set up with
// --token, or has not been set up at all, and the caller says so better.
func readStoredToken() (string, error) {
	doc, err := loadConfigDoc(configFile())
	if err != nil {
		return "", err
	}
	if fsx, _ := doc.settings(); fsx.Token != "" {
		return fsx.Token, nil
	}
	return readLegacyToken()
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

	// The address to listen on may not exist yet: a machine coming back from
	// a power cut starts its services before the network has handed it its
	// address. Giving up here would leave a running service that serves
	// nothing until someone restarts it, so the agent keeps trying instead.
	backoff := time.Duration(0)
	for {
		ln, err := listen("tcp", p.cfg.listen)
		if err == nil {
			log.Printf("gwatch-agent %s serving readings on http://%s/metrics", version, p.cfg.listen)
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("stopped serving on %s: %v", p.cfg.listen, err)
			}
			return
		}
		backoff = min(max(2*backoff, listenRetry), time.Minute)
		log.Printf("cannot listen on %s yet (trying again in %s): %v", p.cfg.listen, backoff, err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
	}
}

// listen is net.Listen; tests replace it.
var listen = net.Listen

// listenRetry is the first wait before serve mode tries its address again.
var listenRetry = 5 * time.Second

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

func fatal(err error) {
	noteInstallResult(err, "")
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

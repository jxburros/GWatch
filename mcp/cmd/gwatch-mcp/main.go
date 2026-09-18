// Command gwatch-mcp is a Model Context Protocol server for GWatch.
//
// It runs separately from the gwatch binary and talks to it only over the
// documented JSON API with an API key (ROADMAP 3.1), so the monitoring service
// keeps its own attack surface and release cycle. Nothing here is on by
// default: the server refuses to start without an API key, and it registers
// write tools only when it is started with --allow-write *and* the key it was
// given has the readwrite scope (ROADMAP 3.2).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/jxburros/GWatch/mcp/internal/gwatch"
	"github.com/jxburros/GWatch/mcp/internal/mcpserver"
	"github.com/jxburros/GWatch/mcp/internal/tools"
)

// version is stamped at build time with -ldflags "-X main.version=…". The MCP
// companion versions independently of gwatch itself.
var version = "dev"

const defaultURL = "http://127.0.0.1:8080"

// howToGetAKey is printed whenever the server cannot start for want of a key.
// It is the one piece of setup that cannot be automated from here.
const howToGetAKey = `Create one in GWatch: Settings › Users & access › API keys › New key.
Choose the "read" scope unless this assistant is meant to change your monitoring
configuration; the key is shown once, so copy it then.

Then either pass it with --api-key, or set GWATCH_API_KEY in the environment (in
an MCP client that is the "env" block of the server's entry in its config file).`

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "gwatch-mcp: "+err.Error())
		os.Exit(1)
	}
}

type options struct {
	url        string
	apiKey     string
	allowWrite bool
	timeout    time.Duration
}

func run(argv []string, stdout, stderr io.Writer) error {
	// Sub-commands come before the flags so `gwatch-mcp version` and
	// `gwatch-mcp check` read the way a person expects.
	cmd := ""
	if len(argv) > 0 && !strings.HasPrefix(argv[0], "-") {
		cmd, argv = argv[0], argv[1:]
	}

	fs := flag.NewFlagSet("gwatch-mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var opt options
	fs.StringVar(&opt.url, "url", envOr("GWATCH_URL", defaultURL),
		"base URL of the GWatch instance (env GWATCH_URL)")
	fs.StringVar(&opt.apiKey, "api-key", os.Getenv("GWATCH_API_KEY"),
		"GWatch API key, gw_… (env GWATCH_API_KEY); required")
	fs.BoolVar(&opt.allowWrite, "allow-write", envBool("GWATCH_MCP_ALLOW_WRITE"),
		"register the write tools as well (env GWATCH_MCP_ALLOW_WRITE=1); off by default, and still refused by GWatch unless the key's scope is readwrite")
	fs.DurationVar(&opt.timeout, "timeout", envDuration("GWATCH_MCP_TIMEOUT", 30*time.Second),
		"timeout for each request to GWatch (env GWATCH_MCP_TIMEOUT)")
	fs.Usage = func() { usage(stderr, fs) }
	if err := fs.Parse(argv); err != nil {
		return err
	}
	if rest := fs.Args(); len(rest) > 0 && cmd == "" {
		cmd = rest[0]
	}

	switch cmd {
	case "version":
		fmt.Fprintf(stdout, "gwatch-mcp %s (%s/%s, built with %s)\n", version, runtime.GOOS, runtime.GOARCH, runtime.Version())
		return nil
	case "help", "-h", "--help":
		usage(stdout, fs)
		return nil
	case "", "serve", "check":
	default:
		usage(stderr, fs)
		return fmt.Errorf("unknown command %q", cmd)
	}

	if strings.TrimSpace(opt.apiKey) == "" {
		return fmt.Errorf("no API key.\n\n%s", howToGetAKey)
	}
	client := gwatch.New(opt.url, opt.apiKey, "gwatch-mcp/"+version, opt.timeout)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cmd == "check" {
		return check(ctx, stdout, client, opt)
	}
	return serve(ctx, stderr, client, opt)
}

// check is the setup aid: it connects, says who GWatch thinks the key is, and
// states plainly whether the write tools will be registered and why not.
func check(ctx context.Context, out io.Writer, client *gwatch.Client, opt options) error {
	fmt.Fprintf(out, "gwatch-mcp %s\n", version)
	fmt.Fprintf(out, "GWatch:      %s%s\n", client.BaseURL, gwatch.APIPrefix)
	me, err := client.Me(ctx)
	if err != nil {
		fmt.Fprintf(out, "Connection:  FAILED\n")
		return err
	}
	fmt.Fprintf(out, "Connection:  ok\n")
	fmt.Fprintf(out, "Principal:   %s\n", me.Describe())
	fmt.Fprintf(out, "Scope:       %s\n", orDash(me.Scope))

	allow, why := writeAllowed(opt.allowWrite, me)
	set := tools.New(client, allow)
	if allow {
		fmt.Fprintf(out, "Write tools: ENABLED — this assistant can create, change and delete nodes and checks.\n")
	} else {
		fmt.Fprintf(out, "Write tools: disabled (%s)\n", why)
	}
	read, write := 0, 0
	for _, t := range set.Tools() {
		if t.Write {
			write++
		} else {
			read++
		}
	}
	fmt.Fprintf(out, "Tools:       %d read, %d write\n", read, write)

	if me.Kind != "apikey" {
		fmt.Fprintf(out, "\nNote: GWatch identified this connection as %q rather than an API key. "+
			"That usually means the request came from the machine GWatch runs on, where a local client is an "+
			"administrator without credentials. The key is still sent, but the permissions you see are not the "+
			"key's — test from another machine to see what a remote client gets.\n", me.Kind)
	}
	return nil
}

// writeAllowed decides whether the write tools are registered, and explains a
// "no". Both conditions have to hold: the operator asked for it on the command
// line, and GWatch says the key is allowed to write. Registering them anyway
// would only produce tools that always answer 403.
func writeAllowed(flagSet bool, me gwatch.Me) (bool, string) {
	switch {
	case !flagSet && me.Scope != "readwrite":
		return false, "--allow-write was not given, and the key's scope is not readwrite"
	case !flagSet:
		return false, "--allow-write was not given"
	case me.Scope == "readwrite":
		return true, ""
	case me.Scope != "":
		return false, fmt.Sprintf("--allow-write was given, but GWatch reports this key's scope as %q — mint a readwrite key in Settings › Users & access", me.Scope)
	default:
		return false, "--allow-write was given, but GWatch does not report a readwrite scope for this credential"
	}
}

func serve(ctx context.Context, logw io.Writer, client *gwatch.Client, opt options) error {
	// Ask GWatch who we are before serving anything: it is the only way to
	// know the key's scope, and failing here rather than at the first tool call
	// gives the person a message they can act on.
	me, err := client.Me(ctx)
	if err != nil {
		var apiErr *gwatch.Error
		if errors.As(err, &apiErr) && apiErr.Status == 401 {
			return fmt.Errorf("%v\n\n%s", err, howToGetAKey)
		}
		return err
	}
	allow, why := writeAllowed(opt.allowWrite, me)
	set := tools.New(client, allow)

	// Diagnostics go to stderr: stdout carries the JSON-RPC stream.
	mode := "read-only"
	if allow {
		mode = "read/write"
	}
	fmt.Fprintf(logw, "gwatch-mcp %s serving %d tools (%s) against %s as %s\n",
		version, len(set.Tools()), mode, client.BaseURL, me.Describe())
	if !allow && why != "" {
		fmt.Fprintf(logw, "gwatch-mcp: write tools not registered: %s\n", why)
	}

	srv := mcpserver.New(set, "gwatch", version)
	if err := mcpserver.RunStdio(ctx, srv); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func usage(w io.Writer, fs *flag.FlagSet) {
	fmt.Fprintf(w, `gwatch-mcp %s — a Model Context Protocol server for GWatch.

Usage:
  gwatch-mcp [flags]          serve MCP over stdio (the default)
  gwatch-mcp check [flags]    connect, print who GWatch thinks you are and whether write tools are on
  gwatch-mcp version          print the version

Flags:
`, version)
	fs.SetOutput(w)
	fs.PrintDefaults()
	fmt.Fprintf(w, "\n%s\n", howToGetAKey)
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envBool(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
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

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

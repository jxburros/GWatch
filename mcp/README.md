# gwatch-mcp — GWatch for AI assistants

`gwatch-mcp` is a [Model Context Protocol](https://modelcontextprotocol.io) server that lets an
AI assistant — Claude Desktop, Claude Code, or any other MCP client — read what GWatch is
monitoring and, if you explicitly allow it, manage nodes and checks.

It is a **separate program from `gwatch`**, in its own Go module with its own version. It never
imports GWatch's code; it talks to a running GWatch over the documented JSON API
([`docs/API.md`](../docs/API.md)) with an API key, over `/api/v1/…`. That is deliberate: the
service that watches your network keeps its own attack surface and its own release cycle, and
nothing here is on unless you turn it on.

> **In the app:** GWatch's **Settings › AI & MCP** page walks through the same setup with your
> own address filled in, mints the read-only key, and offers the **agent skill** — a short
> [`SKILL.md`](../skill/SKILL.md) that teaches an assistant how to use these tools well (start with
> the overview, how to drill into a problem, how to be careful with writes). The skill is versioned
> on its own ([`skill/VERSION`](../skill/VERSION)); the page notes when the copy you downloaded has
> fallen behind.

---

## Trust model — read this before `--allow-write`

An AI assistant that can create and delete checks is a materially bigger trust boundary than a
person clicking through the web interface. So there are three separate gates, and all three have
to be open before an assistant can change anything:

1. **The API key's scope.** Keys are minted in GWatch as `read` or `readwrite`. Default to `read`.
2. **`--allow-write` on this server.** Off unless you pass it (or set `GWATCH_MCP_ALLOW_WRITE=1`).
   Without it the write tools are not registered at all, so the model never sees them in
   `tools/list`.
3. **GWatch itself.** This is the one that actually matters. GWatch's route policy
   (`internal/api/policy.go`) refuses a write from a `read` key with **HTTP 403 — "This API key is
   read-only."**, whatever this server advertises. Hiding the tools is a convenience for the model;
   the server-side refusal is the boundary. `gwatch-mcp` surfaces that 403 as a tool error and does
   not retry it with anything else.

On top of that, **GWatch denies a few things to every API key whatever its scope**: settings,
backups, the service log, config export, the network info, automation triggers, custom endpoints,
software updates, user accounts and API keys themselves. There are deliberately **no tools** for
any of that — a tool for it could only ever return 403. An assistant can help you run your
monitoring; it cannot reconfigure the machine your monitor runs on, grant itself more access, or
read your SMTP password.

`gwatch_delete_node` destroys a node's checks, results and history, so it additionally requires
`confirm: true` in its arguments and refuses without it before any request is sent.

---

## Install

**Download a binary.** Every GWatch release publishes `gwatch-mcp-<os>-<arch>` next to the
`gwatch-<os>-<arch>` binaries, with a `.sha256` and an ed25519 `.sig` beside each. Grab the one for
your platform, rename it to `gwatch-mcp` (`.exe` on Windows) and put it somewhere on your `PATH`.

**Or build it with Go** (1.26 or newer):

```sh
go install github.com/jxburros/GWatch/mcp/cmd/gwatch-mcp@latest
```

The main package lives at `mcp/cmd/gwatch-mcp` rather than at the module root so that `go install`
produces a binary called `gwatch-mcp`; installing the module root would name the binary `mcp`.

`gwatch-mcp` is released with its own **nested-module tags**, `mcp/v<version>` (not the root
module's `v<version>`), which is what makes `@latest` above resolve at all — Go's module
resolution needs a tag prefixed with the module's own subdirectory to find a version of code that
lives under `mcp/` rather than at the repository root. Its binaries still ship inside the *core*
GitHub release, cross-compiled from whatever `mcp/VERSION` says at the time; see
[`docs/RELEASING.md`](../docs/RELEASING.md#releasing-the-mcp-companion) for the full release
process.

**Or from a checkout:**

```sh
cd mcp && go build -o gwatch-mcp ./cmd/gwatch-mcp
# or, from the repository root:
make mcp-build          # writes dist/gwatch-mcp
```

## Create an API key

In GWatch: **Settings › Users & access › API keys › New key**. Give it a name you will recognise
("Claude Desktop"), choose the **`read`** scope, and copy the `gw_…` key — it is shown once and
only a digest is stored. Pick `readwrite` only if you actually want the assistant to change your
monitoring, and see the trust model above first.

## Check the setup

```sh
gwatch-mcp check --url http://127.0.0.1:7230 --api-key gw_…
```

```
gwatch-mcp 0.1.0
GWatch:      http://127.0.0.1:7230/api/v1
Connection:  ok
Principal:   API key "Claude Desktop" (scope read)
Scope:       read
Write tools: disabled (--allow-write was not given, and the key's scope is not readwrite)
Tools:       9 read, 0 write
```

Run it from the machine the assistant will run on, not from the GWatch host: a client on the
GWatch machine is an administrator without credentials, so a check there tells you less than one
from elsewhere. `check` says so when it notices.

## Configure your MCP client

The server speaks MCP over **stdio**, so a client just needs the command and the environment.

**Claude Desktop** — `claude_desktop_config.json`
(macOS: `~/Library/Application Support/Claude/`, Windows: `%APPDATA%\Claude\`):

```json
{
  "mcpServers": {
    "gwatch": {
      "command": "/usr/local/bin/gwatch-mcp",
      "env": {
        "GWATCH_URL": "http://127.0.0.1:7230",
        "GWATCH_API_KEY": "gw_your_read_key_here"
      }
    }
  }
}
```

**Claude Code** — `claude mcp add`:

```sh
claude mcp add gwatch /usr/local/bin/gwatch-mcp \
  -e GWATCH_URL=http://127.0.0.1:7230 \
  -e GWATCH_API_KEY=gw_your_read_key_here
```

**Any other MCP client** — run `gwatch-mcp` as a stdio server with those two environment
variables, or with `--url` and `--api-key` as arguments. Putting the key in `env` rather than in
`args` keeps it out of the process list.

**To allow writes**, add the flag *and* use a `readwrite` key:

```json
{
  "mcpServers": {
    "gwatch": {
      "command": "/usr/local/bin/gwatch-mcp",
      "args": ["--allow-write"],
      "env": {
        "GWATCH_URL": "http://127.0.0.1:7230",
        "GWATCH_API_KEY": "gw_your_readwrite_key_here"
      }
    }
  }
}
```

Reaching a GWatch that is not on this machine is covered in
[`docs/REMOTE-ACCESS.md`](../docs/REMOTE-ACCESS.md) — use HTTPS if the key crosses a network you
do not control.

## Configuration

| Flag | Environment | Default | Meaning |
| --- | --- | --- | --- |
| `--url` | `GWATCH_URL` | `http://127.0.0.1:7230` | Base URL of the GWatch instance. |
| `--api-key` | `GWATCH_API_KEY` | — | **Required.** The `gw_…` key. The server refuses to start without one. |
| `--allow-write` | `GWATCH_MCP_ALLOW_WRITE=1` | off | Register the write tools. Still refused by GWatch unless the key is `readwrite`. |
| `--timeout` | `GWATCH_MCP_TIMEOUT` | `30s` | Timeout for each request to GWatch. |

Commands: `gwatch-mcp` (serve over stdio), `gwatch-mcp check`, `gwatch-mcp version`.

Every request carries `X-API-Key`, `User-Agent: gwatch-mcp/<version>` and goes to `/api/v1/…`.
Diagnostics go to stderr; stdout is the JSON-RPC stream.

## Tools

Each tool returns a one-line human summary followed by compact JSON. Errors carry the HTTP status
and GWatch's own `error` message.

### Read (always available)

| Tool | What it does |
| --- | --- |
| `gwatch_overview` | The whole picture: up/degraded/down tally, per-group status, what needs attention, open incidents, expiring certificates. Start here. |
| `gwatch_list_nodes` | The monitored nodes with their status and a line per check. Optional `group`, `tag`, `status` and `q` filters; a node may be in several groups and `group` matches any of them. |
| `gwatch_get_node` | One node in full: stored configuration, every check's config, and live state. |
| `gwatch_check_results` | The most recent individual runs of one check (`checkId`, `limit`). |
| `gwatch_history` | Availability and latency over `1h`/`24h`/`7d`/`30d`/`1y` for one or several checks, downsampled to ~200 points per series by even striding. |
| `gwatch_events` | The incident timeline, filtered by `nodeId`, `checkId`, `type`, `q`, `since`, `until`, `limit`. |
| `gwatch_health` | The health of the GWatch service itself — scheduler, last check, database size, recent internal errors. |
| `gwatch_templates` | The node templates GWatch ships, with the checks each creates. |
| `gwatch_groups` | The groups and tags in use, with counts. A node in several groups is counted in each. |

### Write (only with `--allow-write` and a `readwrite` key)

| Tool | What it does |
| --- | --- |
| `gwatch_create_node` | Create a node and its checks (`name`, `host`, `groups[]`, `tags`, `importance`, `checks[]`). `group` is still accepted as a one-group shorthand. |
| `gwatch_update_node` | Change a node. Only the fields given change; giving `checks` replaces the whole check list. |
| `gwatch_delete_node` | Delete a node and its history. Requires `confirm: true`. |
| `gwatch_set_node_enabled` | Pause or resume a node's checks. |
| `gwatch_run_node` | Run every enabled check of a node now; results are recorded and alerted on as usual. |
| `gwatch_silence_node` | Silence alerts for a node for N minutes (0 unsilences). Checks keep running. |
| `gwatch_add_note` | Add a note to the event timeline, optionally attached to a node. |
| `gwatch_test_check` | Run a check configuration once without saving it. Nothing is recorded, nothing is alerted. |

### Not available, on purpose

No tools for settings, backups, the service log, config export, automation triggers, custom
endpoints, software updates, user accounts or API keys. GWatch refuses all of those to every API
key whatever its scope, so there is nothing for a tool to do there.

## Developing

```sh
cd mcp
gofmt -l .
go vet ./...
go test ./...
```

Everything is tested against a fake GWatch (`internal/fakegwatch`) that mirrors the real route
policy, including the 403 a read key gets on a write. There is also an end-to-end test against a
real instance, skipped unless you ask for it:

```sh
GWATCH_E2E_URL=http://127.0.0.1:7230 GWATCH_E2E_KEY=gw_… go test ./internal/e2e -v
```

The layering is worth keeping: `internal/gwatch` is the HTTP client, `internal/tools` is the tool
layer and knows nothing about MCP, and `internal/mcpserver` is the only package that touches the
protocol. Adding a tool means adding it to `internal/tools`; the transport does not change.

**On the `github.com/modelcontextprotocol/go-sdk` version**: `mcp/go.mod` requires whatever
Go the SDK release it uses requires (1.25 today), and CI builds it with the root module's
toolchain (Go 1.26) under `GOTOOLCHAIN=local`, so an SDK whose `go` directive outruns that
fails the build outright rather than quietly downloading a toolchain. The SDK is bumped when
CI's `govulncheck` reports a reachable fix in it, or when a tool here needs something newer; a
bump deserves its own change with the e2e and stdio tests re-run. See also the comment at the
top of `internal/mcpserver/server.go`.

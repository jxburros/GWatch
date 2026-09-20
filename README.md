<img src="web/logo.svg" alt="" width="84" align="right">

# GWatch

[![CI](https://github.com/jxburros/GWatch/actions/workflows/ci.yml/badge.svg)](https://github.com/jxburros/GWatch/actions/workflows/ci.yml)

GWatch is a calm monitor for a home network and its services. It runs as a Windows
background service (or a plain console program on Linux/macOS), checks your router,
servers, websites and APIs on a schedule, keeps long-term history in an embedded SQLite
database, sends email alerts that do not spam, can run webhooks / git commands / scripts
when something changes, and shows everything in a compact dark or light web interface
served on `http://127.0.0.1:8080` (optionally to the rest of your LAN).

No cloud account, no AI features, never exposed to the internet by itself. The product
brief that defines the scope lives in [`local-network-monitoring-product-brief.md`](local-network-monitoring-product-brief.md).

> **GWatch is in beta (0.2.1).** It works and it looks after your data, but the
> interface and the JSON API can still change between releases, and bugs are likelier
> now than they will be at 1.0 — which is reserved for the first public, stable
> release. [`CHANGELOG.md`](CHANGELOG.md) lists what is in this version and the gaps it
> ships with. Please [report anything that looks wrong](https://github.com/jxburros/GWatch/issues).

## What it does

- **Check types**: Ping (latency, min/max, jitter, packet loss), HTTP/S (status expectations,
  redirects, DNS/connect/TLS/first-byte timing, final URL), HTTPS certificate (issuer,
  expiry, days remaining, validity), TCP port, DNS (with expected values), Keyword, JSON,
  and Custom script (run your own command and parse a simple status/metric contract from
  its output — see "Custom checks" below), and Hardware health (see below).
- **Hardware health** of the computer GWatch runs on and of any other machine you install
  the agent on: processor, memory and swap, filesystem space, network throughput and disk
  throughput, with warning and critical thresholds so "the disk is filling" and "the disk
  is full" are different events. A machine is a node like any other — pairing one creates
  its node, and its readings and their history are shown there rather than in a section of
  their own. The agent connects outwards to GWatch and hands over
  a reading; GWatch never connects back and holds no credential for the machine, and the
  agent's token can do exactly one thing — submit that one machine's readings. A machine
  that goes quiet is reported as down, which is the whole point. See
  [`docs/HARDWARE.md`](docs/HARDWARE.md).
- **Nodes** group several checks (a Plex node with Ping + TCP 32400 + HTTP). Templates
  prefill sensible defaults for a website, home server, router, API endpoint, TCP service,
  DNS name, this computer's hardware or a machine running the agent. Everything stays editable. Nodes carry groups, tags, notes, importance,
  enable/disable, duplicate, and a dependency ("Plex depends on Gateway").
- **Run now / test** any check and inspect the full result (timings, status code, final
  URL, certificate details, resolved addresses, packets).
- **Alerts by email** after N consecutive failures, on recovery, for warnings (latency,
  packet loss, certificate expiry, response/content change), with a cooldown, per-check
  overrides, manual silencing, maintenance windows (one-off or weekly) and
  dependency-aware suppression: when the gateway is down you get one email, and the
  downstream nodes are recorded as "affected by Gateway" instead of mailing you 20 times.
- **Incident timeline**: down, recovered, warnings, certificate warnings, alerts sent /
  suppressed, silenced, maintenance began/ended, configuration changes, dependency context,
  service start/stop, detected sleep/offline gaps, backups, restores and your own notes.
- **History at 1 hour, 24 hours, 1 week, 1 month and 1 year** with automatic rollups:
  every raw result for 30 days, then 5-minute, hourly and daily summaries (all configurable).
  The database never grows without bound and the retention page explains exactly what is
  kept, rolled up and deleted.
- **Dashboards**: as many as you like, each with its own widgets (health summary, group
  cards, status list, charts, incidents, certificate warnings, needs-attention list,
  monitor health, filtered table). Widgets are dragged by their handle and resized from
  their edges on a 4-column grid; the layout is saved per dashboard.
- **Charts tab**: explore any metric (latency avg/min/max, jitter, packet loss,
  availability) for any checks over any range, with line / area / step / bar / point styles,
  smoothing, thresholds, fixed axes, per-line colours and one-chart-per-check. Save named
  charts, export PNG/CSV, or pin a chart to a dashboard.
- **Audit tab**: the complete event log with text search, type / node / time filters and
  CSV export; the service log with level filter and download; one page for every export.
- **Automation**: per-node **triggers** run an action when the node goes down, recovers,
  becomes degraded, changes status, exceeds a latency, and so on — an HTTP request
  (webhook), a native **Slack**, **Microsoft Teams**, **ntfy** or **Pushover**
  notification, a git command in a repository, custom code (sh, bash, PowerShell, cmd,
  Python, Node or any command) or "run another node's checks now". **Custom endpoints**
  expose the same actions at `/hook/<name>` (optionally token-protected) so a router, a
  CI job or Home Assistant can poke GWatch. Placeholders such as `{{node.name}}`,
  `{{status}}` and `{{message}}` are expanded; every run is recorded with its output.
  See [`docs/RECIPES.md`](docs/RECIPES.md) for ready-made trigger recipes (Home Assistant,
  Discord/Slack, ntfy/Pushover, Docker restarts, git pulls, custom inbound hooks).
- **Wallboards**: full-screen status views for a spare monitor, television or tablet.
  Make as many as you like and arrange each one from its own panels — a headline, the
  counts, a clock, what needs attention, groups, a grid of nodes, trend charts,
  certificates, maintenance, or a line of text — with its own columns, theme, type size
  and refresh. A board can be **projected**: switch it on and GWatch gives you an
  address any browser on your network can open, so a screen with no keyboard shows it
  without ever signing in. That address is worth that one read-only board and nothing
  else, it is shown only to an administrator, and it can be changed or withdrawn at any
  time.
- **Appearance**: dark, light or system theme and a user-chosen accent colour.
- **Remote access**: opt in to serving the interface on the whole LAN.
- **Accounts and API keys**: user accounts with two roles — administrator and viewer —
  and API keys scoped read-only or read-write.
- **Updates**: GWatch watches the project's GitHub releases — on a schedule and when
  you open the interface, or only when you press the button, as you prefer — and says so
  beside Settings when a new version is waiting. Install the newest, or pick an earlier
  release, or opt into pre-releases at your own risk. The new executable replaces the
  running one in place (the previous one is kept as `.old`); the service restarts itself.
  An update is only installed if its ed25519 signature verifies against a release key
  pinned into the running binary — an unsigned, differently signed or altered download
  is refused, never installed with a warning. A build with no key pinned (the default
  for forks and local builds) still reports new releases but will not install them.
- **Exports**: chart as PNG, history and results as CSV, events as CSV, service log,
  configuration as JSON.
- **Backups**: one-click, password-encrypted (Argon2id + AES-256-GCM) archives of the
  configuration and optionally the whole history; restore on a new computer in one step.
  Scheduled automatic backups (Settings › Backups) run unattended on a configurable
  interval and keep only the newest N archives. See
  [`docs/RESTORE.md`](docs/RESTORE.md) for the full restore-to-a-new-machine procedure.
- **Monitor health**: service status, scheduler, last/next check, sleep/offline gaps,
  database size, retention status, last backup, recent internal errors and logs.

## Install on Windows

**Easiest: the setup program.** Download `gwatch-setup-<version>.exe` from the
[latest release](https://github.com/jxburros/GWatch/releases/latest), run it, and follow
the wizard — it asks for a port and whether to allow other devices on your network, then
installs the `GWatch` service, starts it, and adds a Start Menu shortcut. No PowerShell
or command line needed. Full walkthrough, including upgrading and uninstalling:
[`docs/INSTALL.md`](docs/INSTALL.md).

The same release carries a second setup program, `gwatch-agent-setup-<version>.exe`. That
one does not go on this machine — it goes on the *other* machines you want GWatch to
report the health of. It asks for the server's address and a pairing code you generate in
GWatch under **Nodes › Pair a machine**, and nothing else; see
[`docs/HARDWARE.md`](docs/HARDWARE.md).

**Advanced: the PowerShell scripts.** The setup program wraps these same steps; use them
directly if you'd rather build from source, script an unattended rollout, or skip
running a downloaded `.exe`.

1. Build (or download) `gwatch.exe`. With Go 1.24+ installed:
   ```powershell
   powershell -ExecutionPolicy Bypass -File scripts\build.ps1
   ```
2. Install the service from an **Administrator** PowerShell:
   ```powershell
   powershell -ExecutionPolicy Bypass -File scripts\install.ps1 -Exe .\dist\gwatch.exe
   ```
   This copies the program to `C:\Program Files\GWatch`, registers the `GWatch` service
   (automatic start, restarts on failure), starts it, and adds a Start-menu shortcut that
   opens the interface. Data lives in `C:\ProgramData\GWatch` (`gwatch.db`, `logs\`, `backups\`).
3. Open <http://127.0.0.1:8080> (or run `gwatch open`).

**Updating**: build the new `gwatch.exe` and run `scripts\install.ps1` again. It stops the
service, replaces the executable and starts the service. Your data is untouched.

**Removing**: `scripts\uninstall.ps1` (add `-RemoveData` to also delete the database).

You can also manage the service by hand: `gwatch install`, `gwatch uninstall`,
`gwatch start|stop|restart|status`.

## Run without installing (any OS)

```bash
go run . run --data-dir ./data
```

Then open <http://127.0.0.1:8080>. Monitoring only runs while this process runs.

Options: `--data-dir DIR` (or `GWATCH_DATA_DIR`), `--listen 127.0.0.1:8080` (or
`GWATCH_LISTEN`). The default binds this computer only. Use `--listen 0.0.0.0:8080`, or
turn on **Settings › Network access**, to reach the interface from other devices on your
network (the listener is rebound live, no restart needed); create an account under
**Settings › Users & access** so other devices have to sign in.

## Accounts and API keys

A browser on the computer GWatch runs on is an administrator without signing in, so a
fresh install needs no setup at all. Once you want to reach GWatch from elsewhere,
create accounts under **Settings › Users & access**: an **administrator** can change
anything, a **viewer** can see dashboards, charts, history, incidents and the audit log
and is refused — with an explanation, not a silent failure — on every change. Every
change is recorded in the audit log with the name of whoever made it. Turning on
"Require sign-in on this computer too" makes even a local browser sign in.

The same page mints **API keys** for scripts and home-automation systems, scoped
`read` or `readwrite`. A key is shown once and stored only as a fingerprint. No key of
either scope can reach settings, backups, updates, accounts, the service log or
anything that runs a trigger or an endpoint on the machine.

The older shared access password still works, so nothing breaks on upgrade, but
accounts replace it: they give each person their own password, a role, and a name in
the audit log.

Reaching GWatch from outside your own network — a read-only credential plus Tailscale
or a reverse proxy with TLS, and why you should never port-forward the raw HTTP port —
is covered in [`docs/REMOTE-ACCESS.md`](docs/REMOTE-ACCESS.md).

### Custom checks

The **Custom script** check type lets you monitor anything GWatch doesn't have a
built-in check for: run your own command on the check's schedule and let GWatch parse a
small status/metric contract from its output. No shell is used on any platform — the
command line is split on whitespace (quote with `'` or `"` to keep a value together);
use `sh -c '...'` (or `cmd /C ...` on Windows) explicitly if you need pipes, globbing or
`$VAR` expansion. The target (the node host, or the check's own target override) is
passed as the `GWATCH_TARGET` environment variable and substituted for a literal
`{{target}}` in any single argument that contains it.

Exit code 0 means up, 2 means degraded, anything else means down. Stdout may also contain
`key=value` lines: `status=up|degraded|down` overrides the exit code, `message=...` is
shown as the result, `latency_ms=<number>` feeds the charts, and `error=...` sets the
error text. Everything else the command prints (stdout and stderr, up to 8 KiB) is kept
and shown alongside the result. The check is killed and reported "timed out" if it runs
past its configured timeout.

**The command runs on this machine with the permissions of the GWatch service itself.**
Only administrators you trust should be able to create or edit a custom check — anyone
who can do so can run arbitrary code as GWatch.

## AI assistants (MCP)

`gwatch-mcp` is an optional, separate program that lets an AI assistant (Claude Desktop,
Claude Code, or any Model Context Protocol client) read what GWatch is monitoring and —
only if you explicitly allow it — manage nodes and checks. It ships and versions on its
own, never imports GWatch's code, and talks to a running GWatch over the JSON API with an
API key, so the monitoring service itself is unchanged and nothing is on by default.

It is read-only unless you both start it with `--allow-write` and give it a `readwrite`
key, and GWatch enforces that boundary itself: a `read` key gets a 403 on every write
whatever the assistant tries. Settings, backups, the service log, triggers, endpoints,
accounts and API keys are off-limits to any API key, so there are no tools for them.

Install, configuration snippets for each client, the trust model and the full tool list are
in [`mcp/README.md`](mcp/README.md).

## Notes for home networks

- HTTP, TCP, DNS and ping checks may target private addresses such as `192.168.1.1`; that is
  the point of the product. The web interface stays on localhost unless you open it.
- Triggers and endpoints run on the computer that runs GWatch with its permissions. Give
  endpoints a token and create an administrator account before enabling remote access.
- ICMP ping uses a raw socket on Windows (fine under the service account). On Linux it
  tries unprivileged ping sockets, then a raw socket, then the system `ping` command.
- Email: any SMTP provider works (STARTTLS on 587, implicit TLS on 465, or none). Use the
  "Send test email" button in Settings › Alerts before enabling alerts.
- Secrets at rest: account passwords are stored as argon2id hashes, and session tokens
  and API keys only as sha256 digests — none of them can be read back out of the
  database. The SMTP password and the legacy access password are encrypted in `gwatch.db`
  with the key file `gwatch.key`, created next to the database on first run (mode 0600).
  Keep the two together: moving the database to another machine without the key file means
  those two passwords have to be entered again; everything else still loads. Backups do not
  need the key file — they carry the settings in the clear inside an archive that is already
  encrypted with your backup password.
- Sleep/hibernate: when the computer was asleep the timeline records a "monitoring paused"
  gap so a quiet period is never mistaken for a healthy one.

## Development

```bash
make test                         # go vet + unit and integration tests
make ci                           # everything CI runs: gofmt, vet, go mod tidy, race tests
make cover                        # race tests plus a per-function coverage report
make windows                      # cross-compile dist/gwatch.exe from Linux/macOS
make agent                        # build dist/gwatch-agent for this platform
make agent-all                    # build it for Windows, Linux and macOS, amd64/arm64/arm
```

The test suite needs no external network: HTTP, TLS and DNS checks are exercised against
local `httptest` servers and a fake in-process DNS resolver, and ping output is parsed
from fixtures, so the tests are deterministic on a CI runner.

### Continuous integration

[`.github/workflows/ci.yml`](.github/workflows/ci.yml) runs on every push and pull
request as a single job on a Windows runner (Linux runners are switched off for now):

| Step | What it guards |
|---|---|
| lint | `gofmt`, `go vet`, `go mod tidy` is a no-op, `go mod verify` |
| build + test | `go build ./...` and the whole test suite on Windows, the platform GWatch installs as a service on |
| web assets | `node --check` on every file under `web/` — the UI is embedded with `//go:embed`, so the Go compiler never sees a syntax error there |
| PowerShell | Parses `scripts/*.ps1`, since those scripts are the Windows install path |
| installers | Compiles `scripts/installer/gwatch.iss` and `gwatch-agent.iss` with Inno Setup into `gwatch-setup-*.exe` and `gwatch-agent-setup-*.exe` |
| artefact | Uploads `gwatch-windows-amd64.exe` and both setup programs |

Pushing a tag such as `v0.1.0` additionally runs the `release` job, which cross-compiles
`gwatch-<os>-<arch>[.exe]` for Windows, Linux and macOS, writes a `.sha256` checksum and
an ed25519 `.sig` signature next to each, and publishes a GitHub release. Those asset
names are what **Settings › Updates** looks for.

The signature is what the updater actually trusts: it verifies `<asset>.sig` against the
public keys pinned in [`internal/update/release_keys.txt`](internal/update/release_keys.txt)
and refuses to install anything else. Releasing therefore needs a one-time setup — generate
the key pair with `make keygen` (`go run ./cmd/gwatch-sign keygen`), paste the printed
`ed25519:…` line into `release_keys.txt`, keep `release.key` offline and store its base64
seed as the `GWATCH_SIGNING_KEY` repository secret. The release job fails if that secret is
missing, or if its public key is not listed in `release_keys.txt` (which would ship binaries
that reject their own updates). Full steps: [`docs/RELEASING.md`](docs/RELEASING.md).

Layout:

| Path | Purpose |
|---|---|
| `main.go` | CLI, Windows service wrapper (kardianos/service), HTTP server bound to localhost |
| `internal/model` | Shared data types and JSON wire format |
| `internal/store` | SQLite (modernc, pure Go) schema, single queued writer, rollups, history queries |
| `internal/checks` | Check runners: ping, http, cert, tcp, dns, keyword, json; node templates |
| `internal/engine` | Scheduler, result processing, alert rules, dependencies, maintenance, retention, health, triggers |
| `internal/actions` | Automation actions: HTTP requests, git commands, custom scripts, run-node |
| `internal/update` | GitHub release check, download, checksum, signature verification and executable swap |
| `cmd/gwatch-sign` | Maintainer CLI: generate the release signing key, sign and verify release assets |
| `internal/mailer` | SMTP delivery and alert email rendering |
| `internal/backup` | Encrypted backup archives and restore |
| `internal/api` | JSON API (see `docs/API.md`) and static UI serving |
| `web/` | The browser interface (vanilla HTML/CSS/JS, no build step, embedded into the binary; open with `?mock=1` for an in-browser demo backend) |
| `web/fonts/` | Barlow and Kode Mono, latin subsets, self-hosted so the UI still requests nothing from the internet ([SIL OFL 1.1](web/fonts/OFL.txt)) |
| `scripts/` | Windows build / install / uninstall PowerShell scripts |
| `VERSION` | The version every build reports; a release is the tag `v<VERSION>` |
| `scripts/installer/` | The two Inno Setup scripts, their shared branding and the wizard artwork |
| `cmd/gwatch-rsrc/` | Builds the `.syso` resource objects that put the GWatch icon inside the Windows executables (`make rsrc`) |

## License, terms and privacy

GWatch is source-available under the [GWatch Community License](LICENSE): free to use,
modify, and distribute, including commercially for deployment, support, and
customization work — but the unmodified software itself may not be resold or offered
as a paid hosted service. See [TRADEMARKS.md](TRADEMARKS.md) for the separate policy
on using the GWatch name and logo.

**Attribution**: forks and derivatives must keep the GWatch name, logo, and copyright
line visible.

The two typefaces shipped in `web/fonts/` — Barlow and Kode Mono — are third-party and
carry their own licence, the [SIL Open Font License 1.1](web/fonts/OFL.txt), which
travels with them.

Three plain-language documents sit alongside the licence. They are written for
transparency rather than by a lawyer, and they say so at the top:

| Document | What it covers |
|---|---|
| [`docs/TERMS.md`](docs/TERMS.md) | Terms of use — acceptable use, no warranty, limitation of liability |
| [`docs/PRIVACY.md`](docs/PRIVACY.md) | What GWatch stores, where, and the fact that none of it goes anywhere |
| [`docs/DISCLAIMER.md`](docs/DISCLAIMER.md) | The security choices that are yours to make, and what happens if you make them badly |

The setup program shows the licence and a digest of all three before it installs
anything, and drops a copy in the install directory.

## Credits

GWatch is developed by **Jeffrey Guntly** and **Garrett Guntly**, and published by
**JX Holdings, LLC**.

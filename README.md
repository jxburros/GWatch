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

## What it does

- **Check types**: Ping (latency, min/max, jitter, packet loss), HTTP/S (status expectations,
  redirects, DNS/connect/TLS/first-byte timing, final URL), HTTPS certificate (issuer,
  expiry, days remaining, validity), TCP port, DNS (with expected values), Keyword, JSON,
  and Custom script (run your own command and parse a simple status/metric contract from
  its output — see "Custom checks" below).
- **Nodes** group several checks (a Plex node with Ping + TCP 32400 + HTTP). Templates
  prefill sensible defaults for a website, home server, router, API endpoint, TCP service
  or DNS name. Everything stays editable. Nodes carry groups, tags, notes, importance,
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
- **Wallboard**: a read-only full-screen status view for a spare monitor or tablet.
- **Appearance**: dark, light or system theme and a user-chosen accent colour.
- **Remote access**: opt in to serving the interface on the whole LAN, with an optional
  access password for other devices (this computer is never challenged).
- **Updates**: check GitHub releases from Settings › Updates and install the new
  executable in place (the previous one is kept as `.old`); the service restarts itself.
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

**Advanced: the PowerShell scripts.** The setup program wraps these same steps; use them
directly if you'd rather build from source, script an unattended rollout, or skip
running a downloaded `.exe`.

1. Build (or download) `gwatch.exe`. With Go 1.24+ installed:
   ```powershell
   powershell -ExecutionPolicy Bypass -File scripts\build.ps1 -Version 1.0.0
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
network (the listener is rebound live, no restart needed); set an access password there
so other devices have to authenticate.

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

## Notes for home networks

- HTTP, TCP, DNS and ping checks may target private addresses such as `192.168.1.1`; that is
  the point of the product. The web interface stays on localhost unless you open it.
- Triggers and endpoints run on the computer that runs GWatch with its permissions. Give
  endpoints a token and set an access password before enabling remote access.
- ICMP ping uses a raw socket on Windows (fine under the service account). On Linux it
  tries unprivileged ping sockets, then a raw socket, then the system `ping` command.
- Email: any SMTP provider works (STARTTLS on 587, implicit TLS on 465, or none). Use the
  "Send test email" button in Settings › Alerts before enabling alerts.
- Secrets at rest: the SMTP password and the access password are encrypted in `gwatch.db`
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
| installer | Compiles `scripts/installer/gwatch.iss` with Inno Setup into `gwatch-setup-*.exe` |
| artefact | Uploads `gwatch-windows-amd64.exe` and `gwatch-setup-*.exe` |

Pushing a tag such as `v1.2.0` additionally runs the `release` job, which cross-compiles
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
| `scripts/` | Windows build / install / uninstall PowerShell scripts |

## License

GWatch is source-available under the [GWatch Community License](LICENSE): free to use,
modify, and distribute, including commercially for deployment, support, and
customization work — but the unmodified software itself may not be resold or offered
as a paid hosted service. See [TRADEMARKS.md](TRADEMARKS.md) for the separate policy
on using the GWatch name and logo.

**Attribution**: forks and derivatives must keep the GWatch name, logo, and copyright
line visible.

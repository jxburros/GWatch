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
  expiry, days remaining, validity), TCP port, DNS (with expected values), Keyword, JSON.
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
  (webhook), a git command in a repository, custom code (sh, bash, PowerShell, cmd,
  Python, Node or any command) or "run another node's checks now". **Custom endpoints**
  expose the same actions at `/hook/<name>` (optionally token-protected) so a router, a
  CI job or Home Assistant can poke GWatch. Placeholders such as `{{node.name}}`,
  `{{status}}` and `{{message}}` are expanded; every run is recorded with its output.
- **Wallboard**: a read-only full-screen status view for a spare monitor or tablet.
- **Appearance**: dark, light or system theme and a user-chosen accent colour.
- **Remote access**: opt in to serving the interface on the whole LAN, with an optional
  access password for other devices (this computer is never challenged).
- **Updates**: check GitHub releases from Settings › Updates and install the new
  executable in place (the previous one is kept as `.old`); the service restarts itself.
- **Exports**: chart as PNG, history and results as CSV, events as CSV, service log,
  configuration as JSON.
- **Backups**: one-click, password-encrypted (Argon2id + AES-256-GCM) archives of the
  configuration and optionally the whole history; restore on a new computer in one step.
- **Monitor health**: service status, scheduler, last/next check, sleep/offline gaps,
  database size, retention status, last backup, recent internal errors and logs.

## Install on Windows

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

## Notes for home networks

- HTTP, TCP, DNS and ping checks may target private addresses such as `192.168.1.1`; that is
  the point of the product. The web interface stays on localhost unless you open it.
- Triggers and endpoints run on the computer that runs GWatch with its permissions. Give
  endpoints a token and set an access password before enabling remote access.
- ICMP ping uses a raw socket on Windows (fine under the service account). On Linux it
  tries unprivileged ping sockets, then a raw socket, then the system `ping` command.
- Email: any SMTP provider works (STARTTLS on 587, implicit TLS on 465, or none). Use the
  "Send test email" button in Settings › Alerts before enabling alerts.
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
| artefact | Uploads `gwatch-windows-amd64.exe` |

Pushing a tag such as `v1.2.0` additionally runs the `release` job, which cross-compiles
`gwatch-<os>-<arch>[.exe]` for Windows, Linux and macOS with `.sha256` checksums and
publishes a GitHub release. Those asset names are what **Settings › Updates** looks for.

Layout:

| Path | Purpose |
|---|---|
| `main.go` | CLI, Windows service wrapper (kardianos/service), HTTP server bound to localhost |
| `internal/model` | Shared data types and JSON wire format |
| `internal/store` | SQLite (modernc, pure Go) schema, single queued writer, rollups, history queries |
| `internal/checks` | Check runners: ping, http, cert, tcp, dns, keyword, json; node templates |
| `internal/engine` | Scheduler, result processing, alert rules, dependencies, maintenance, retention, health, triggers |
| `internal/actions` | Automation actions: HTTP requests, git commands, custom scripts, run-node |
| `internal/update` | GitHub release check, download, checksum and executable swap |
| `internal/mailer` | SMTP delivery and alert email rendering |
| `internal/backup` | Encrypted backup archives and restore |
| `internal/api` | JSON API (see `docs/API.md`) and static UI serving |
| `web/` | The browser interface (vanilla HTML/CSS/JS, no build step, embedded into the binary; open with `?mock=1` for an in-browser demo backend) |
| `scripts/` | Windows build / install / uninstall PowerShell scripts |

## License

MIT, see [LICENSE](LICENSE).

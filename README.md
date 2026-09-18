# GWatch

GWatch is a calm, local-only monitor for a home network and its services. It runs as a
Windows background service (or a plain console program on Linux/macOS), checks your
router, servers, websites and APIs on a schedule, keeps long-term history in an embedded
SQLite database, sends email alerts that do not spam, and shows everything in a dark,
minimal web interface served on `http://127.0.0.1:8080`.

No cloud account, no public exposure, no AI features. The product brief that defines the
scope lives in [`local-network-monitoring-product-brief.md`](local-network-monitoring-product-brief.md).

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
  cards, status list, latency / response-time / packet-loss / uptime charts, incidents,
  certificate warnings, needs-attention list, monitor health, filtered table).
- **Wallboard**: a read-only full-screen status view for a spare monitor or tablet.
- **Exports**: chart as PNG, history and results as CSV, incidents as CSV, configuration as JSON.
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
`GWATCH_LISTEN`). The listen address must be a loopback address: the interface is never
reachable from other computers.

## Notes for home networks

- HTTP, TCP, DNS and ping checks may target private addresses such as `192.168.1.1`; that is
  the point of the product. Only the web interface is restricted to localhost.
- ICMP ping uses a raw socket on Windows (fine under the service account). On Linux it
  tries unprivileged ping sockets, then a raw socket, then the system `ping` command.
- Email: any SMTP provider works (STARTTLS on 587, implicit TLS on 465, or none). Use the
  "Send test email" button in Settings › Alerts before enabling alerts.
- Sleep/hibernate: when the computer was asleep the timeline records a "monitoring paused"
  gap so a quiet period is never mistaken for a healthy one.

## Development

```bash
go vet ./... && go test ./...      # unit + integration tests (no external network needed)
make windows                      # cross-compile dist/gwatch.exe from Linux/macOS
```

Layout:

| Path | Purpose |
|---|---|
| `main.go` | CLI, Windows service wrapper (kardianos/service), HTTP server bound to localhost |
| `internal/model` | Shared data types and JSON wire format |
| `internal/store` | SQLite (modernc, pure Go) schema, single queued writer, rollups, history queries |
| `internal/checks` | Check runners: ping, http, cert, tcp, dns, keyword, json; node templates |
| `internal/engine` | Scheduler, result processing, alert rules, dependencies, maintenance, retention, health |
| `internal/mailer` | SMTP delivery and alert email rendering |
| `internal/backup` | Encrypted backup archives and restore |
| `internal/api` | JSON API (see `docs/API.md`) and static UI serving |
| `web/` | The browser interface (vanilla HTML/CSS/JS, no build step, embedded into the binary) |
| `scripts/` | Windows build / install / uninstall PowerShell scripts |

## License

MIT, see [LICENSE](LICENSE).

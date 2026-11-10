# Features

A running checklist of capabilities GWatch is believed to implement, as of 0.8.10 (gwatch-agent 0.8.7, gwatch-mcp 0.1.2). Keep entries small and concrete so the list can support audits and QA. A listed feature counts as implemented for inventory purposes whether or not it has been tested; the checkbox is a checklist marker, not a test result. Seeded from `README.md` and `docs/USER-GUIDE.md`.

## Running GWatch

- [ ] Runs as a Windows service, a systemd service (OpenRC/SysV fallbacks), a launchd service, or a container
- [ ] `gwatch run`, `install`, `uninstall`, `start`/`stop`/`restart`, `status`, `open`, `update`, `migrate-db`, `version`
- [ ] Windows setup program asks for the port and LAN access
- [ ] `.deb`/`.rpm` server packages with a hardened unit running as the `gwatch` user
- [ ] Multi-arch container image and Compose file
- [ ] Listens on loopback by default; LAN access opt-in with sign-in
- [ ] Signed self-update, verified before install
- [ ] Monitor health page with the service log

## Nodes and checks

- [ ] Nodes with several checks, groups/tags, notes, importance, enable/disable, duplicate
- [ ] Node templates (website, home server, router, API endpoint and others)
- [ ] Ping check (latency, jitter, packet loss; ICMP or system ping)
- [ ] HTTP/S check (expected codes, redirects, final URL, timings)
- [ ] HTTPS certificate check with expiry thresholds
- [ ] TCP port check
- [ ] DNS check with expected answer
- [ ] Keyword check
- [ ] JSON check, with recorded values
- [ ] Response/content change detection
- [ ] Custom script check
- [ ] Hardware health check (via the agent or this machine)
- [ ] SNMP check with walk-to-choose and named ports
- [ ] Matter device check through python-matter-server
- [ ] Per-check interval, timeout, retries and failure threshold
- [ ] Run now with the result inspector
- [ ] Dependencies between nodes
- [ ] Discovery: ping sweep, names, open ports, suggested templates
- [ ] Bulk edit across many nodes

## Alerts, rules and automation

- [ ] Email alerts with test email
- [ ] Slack, Teams, Discord, Telegram, Matrix, ntfy, Gotify, Pushover, Twilio SMS, PagerDuty, Opsgenie
- [ ] Consecutive-failure thresholds, cooldowns, recovery notifications
- [ ] Dependency-aware suppression
- [ ] Silences and maintenance windows
- [ ] Per-node alert grouping and warning digest
- [ ] Cross-node notification rules (all / any / at least N)
- [ ] Triggers with HTTP, notify, git, script and run-node actions
- [ ] Inbound custom endpoints with tokens

## Seeing what happened

- [ ] Multiple customisable dashboards with widgets
- [ ] Charts at 1h–1y with overlays of earlier periods and "view as table"
- [ ] Network map: status colours, outage reach, clusters, arranging by hand, what-if, Present mode, list view
- [ ] Incidents with acknowledge and resolve
- [ ] Event timeline with notes
- [ ] Availability reports (printable)
- [ ] Audit log with actor
- [ ] Wallboards, including token links for a spare screen
- [ ] CSV export of history and incidents; chart images
- [ ] History rollups and retention

## Accounts and access

- [ ] Administrator and viewer accounts
- [ ] Local-administrator trust on this computer, with "Require sign-in on this computer too"
- [ ] Read-only and read-write API keys; `/api/v1`
- [ ] Rate limiting and `--trusted-proxies`
- [ ] Secrets sealed at rest with `gwatch.key`

## Data

- [ ] SQLite by default; PostgreSQL or MySQL/MariaDB as options
- [ ] `gwatch migrate-db` from SQLite to a server
- [ ] Encrypted one-click and scheduled backups, restore on a new machine

## Agent (`gwatch-agent`)

- [ ] Pairing by code; Windows installer, Linux packages, Homebrew, winget, install script, container
- [ ] CPU, memory, filesystems, network, disk throughput; temperatures on Linux
- [ ] Docker container and Hyper-V guest readings
- [ ] Self-update from signed agent releases (unless package-managed)
- [ ] Local control panel
- [ ] Opt-in `serve` mode
- [ ] Remote collectors: network checks run from a second site

## AI assistants

- [ ] Separate `gwatch-mcp` server with read tools; write tools behind `--allow-write` and a read-write key
- [ ] Settings › AI & MCP set-up page and downloadable agent skill

## Interface

- [ ] Dark and light themes
- [ ] Keyboard and screen-reader support; axe-checked routes
- [ ] First-run tour, help and tips
- [ ] Demo mode with `?mock=1`

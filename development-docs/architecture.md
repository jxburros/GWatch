# GWatch — Architecture

> **For AI Agents and contributors:** this is the source of truth for GWatch's *current* structure. It describes what exists, not what is planned (`productRoadmap.md` holds plans). For the full file-by-file layout and the `make` targets, see `docs/development/DEVELOPMENT.md`; this document explains how the pieces fit together and which boundaries must hold.

<!--
Set up on 2026-11-10 from the code at 0.8.10 (gwatch-agent 0.8.7, gwatch-mcp 0.1.2),
docs/development/DEVELOPMENT.md, docs/API.md, docs/PRIVACY.md, docs/DATABASE.md and
docs/development/AGENT-DECISIONS.md.
-->

---

## 1. Architecture Summary

GWatch is a **single Go binary** (`gwatch`) that runs as a background service and serves a **dependency-free browser interface** embedded in it. Inside one process:

- a **scheduler and engine** run checks against nodes, judge results, apply dependencies, maintenance and alert rules, and fire notifications and triggers;
- a **store** writes results through a single queued writer into SQLite (default) or the person's own PostgreSQL or MySQL/MariaDB, rolling history up and pruning it;
- a **JSON API** (`/api/…`, versioned as `/api/v1/…`) with one route policy serves the web interface, API-key clients and server-sent events.

Two **separately versioned companions** talk to it over HTTP: `gwatch-agent` (hardware readings and, optionally, remote network checks, always over connections the agent opens, except opt-in `serve` mode) and `gwatch-mcp` (an MCP server that uses the public API with an API key).

---

## 2. System Responsibilities

- Run eleven check types on per-check schedules and record results.
- Evaluate status, thresholds, consecutive failures, dependencies, maintenance windows, silences and cross-node rules; open and close incidents; record events and an audit log.
- Deliver notifications (email and twelve channels in total, plus webhook actions) with grouping, digests and cooldowns.
- Run automation: triggers and inbound endpoints that call webhooks, notify, run `git` or run scripts.
- Keep history with rollups and retention; export CSV, charts and availability reports.
- Serve dashboards, charts, the network map, wallboards and settings through the web interface.
- Manage accounts, sessions, API keys and the local-administrator trust model.
- Back up and restore (encrypted archives), and update itself from signed releases.
- Report its own health.

---

## 3. System Boundaries

### This Repo Owns

- `gwatch` (root module), `gwatch-agent` (`cmd/gwatch-agent`), the `gwatch-mcp` module (`mcp/`), the agent skill (`skill/`), the maintainer tools (`cmd/gwatch-sign`, `cmd/gwatch-seal-secret`, `cmd/gwatch-rsrc`), installers and packaging, and the documentation.

### External Systems Own

- The monitored devices and services; SMTP servers and notification services; the Matter server (python-matter-server) that GWatch connects to for Matter checks; GitHub Releases (update source); the shared JX Holdings release infrastructure used by the release workflows; the person's PostgreSQL/MySQL server when chosen.

### User Configuration Owns

- The data directory and everything in it; the listen address and LAN exposure; accounts, keys, nodes, checks, channels and automation; whether the agent, remote collectors and the MCP companion are used at all.

### Generated Artifacts Own

- `web/mock-templates.js` and `tests/contract/api-shapes.json` (generated from Go; regenerate rather than hand-edit), the `.syso` resource files (`make rsrc`), release assets and their `.sha256`/`.sig` files.

### Human Maintainers Own

- The release signing key (kept offline) and the `GWATCH_SIGNING_KEY` secret, the pinned public keys in `internal/update/release_keys.txt`, release dispatch, the licence and trademark policy.

---

## 4. Directory and File Map

| Path | Role |
|---|---|
| `main.go`, `migrate_db.go`, `packaging.go`, `service_systemd.go` | CLI (`run`, `install`, `uninstall`, `start`/`stop`/`restart`, `status`, `open`, `update`, `migrate-db`, `version`), HTTP server, service wiring |
| `internal/model` | Shared data types and the JSON wire format |
| `internal/engine` | Scheduler, result processing, alerting, dependencies, maintenance, rules, triggers, retention, backups job, reports, collectors |
| `internal/checks` | Check runners and node templates |
| `internal/store`, `internal/dbconfig` | Schema, migrations, single writer, rollups, queries; which database to open |
| `internal/api` | HTTP handlers, route policy (`policy.go`), authentication (`auth.go`), SSE, static UI serving |
| `internal/auth`, `internal/secrets`, `internal/permissions`, `internal/selfconn` | Roles/sessions/keys/password hashing; sealing secrets with `gwatch.key`; file permissions; recognising GWatch's own loopback connections |
| `internal/actions`, `internal/mailer` | Automation actions and notification channels; SMTP and alert email |
| `internal/backup`, `internal/update` | Encrypted backup/restore; signed self-update |
| `internal/hostmon`, `internal/sysmetrics` | Latest hardware reading per machine; per-platform collectors (Docker, Hyper-V) shared with the agent |
| `internal/discovery`, `internal/matter`, `internal/reports` | Subnet sweeps; Matter client; availability reports |
| `internal/svc`, `internal/serviceinstall`, `internal/subprocess`, `internal/logging` | Service hosting (SCM, launchd, systemd/OpenRC/SysV); protected install copy and hardened systemd unit; process running; rotated log |
| `web/` | The interface: plain HTML/CSS/JS, `//go:embed`ed; `web/views/` per screen; `web/mock.js` demo backend |
| `cmd/gwatch-agent` | The agent (own `VERSION`, own releases, local control panel) |
| `mcp/` | `gwatch-mcp`, its own Go module and `VERSION` |
| `skill/` | The agent skill served by `GET /api/mcp/skill`, own `VERSION` |
| `tests/web`, `tests/e2e`, `tests/contract` | jsdom suite, Playwright + axe, API shape contract |
| `scripts/`, `packaging/`, `Dockerfile` | Windows scripts and Inno Setup installers, `.deb`/`.rpm`/Homebrew/winget, container image |
| `.github/workflows/`, `.release/`, `release.json` | CI, release, release validation; release manifest and notes |
| `docs/`, `docs/development/` | User documentation; developer how-tos |
| `development-docs/` | Governance documents (this file and its siblings) |
| `brand/`, `marketing/` | Logos and marketing assets (slated to move out, #274) |

---

## 5. Major Subsystems

### Engine (`internal/engine`)

**Responsibility:** decides when each check runs, interprets results and turns them into status, incidents, events, notifications and trigger runs.
**Key behaviour:** dependency suppression checks the parent before blaming a child; suppressed observations are still stored; rules combine status conditions across nodes and run existing trigger actions. The engine holds a global lock while processing; the result write currently happens under it (#210).
**Boundaries:** does not talk HTTP to clients; persists through the store; sends through `actions`/`mailer`.

### Checks (`internal/checks`)

**Responsibility:** one runner per check type: `ping`, `http`, `cert`, `tcp`, `dns`, `keyword`, `json`, `custom` (script), `system` (hardware), `snmp`, `matter`. Templates live here too. The same network runners are compiled into the agent for remote collectors.

### Store (`internal/store`)

**Responsibility:** all persistence through a dialect layer (SQLite via modernc.org by default; `-tags sqlite_ncruces` / `sqlite_cgo` alternatives; PostgreSQL via pgx; MySQL/MariaDB). A single queued writer serialises writes (also on PostgreSQL/MySQL today, #212). Raw results are rolled up into coarser summaries and pruned according to retention settings.

### API and policy (`internal/api`)

**Responsibility:** every request is resolved to a principal — local administrator (loopback client, unless "Require sign-in on this computer too" is on, and never for GWatch's own outbound loopback connections), a signed-in account (admin or viewer), or an API key (`read`/`readwrite`) — and checked against the route policy. The web interface uses the same API; `docs/API.md` documents it. Server-sent events stream changes to open pages.

### Web interface (`web/`)

**Responsibility:** single-page app in vanilla JavaScript with per-screen modules in `web/views/`, SVG/canvas charts, the network map (`netmap.js`, `netmap-tools.js`) and wallboards (`wall.html`). `?mock=1` runs it against `web/mock.js`.

### Agent (`cmd/gwatch-agent`)

**Responsibility:** posts one machine's hardware readings (CPU, memory, filesystems, network, disk throughput, Linux temperatures, Docker containers, Hyper-V guests) to GWatch with a submit-only token obtained by redeeming a pairing code; optionally runs remote collector checks for nodes assigned to it. Updates itself from its own signed releases unless installed by a package manager. Decisions: `docs/development/AGENT-DECISIONS.md`.

### MCP companion (`mcp/`)

**Responsibility:** exposes read tools and, only with a `readwrite` key **and** `--allow-write`, write tools over MCP, calling `/api/v1`. Never imports GWatch code.

### Update and backup (`internal/update`, `internal/backup`)

**Responsibility:** check GitHub Releases, download, verify SHA-256 and the ed25519 signature against the pinned keys, then swap the executable; create and restore password-encrypted archives (Argon2id + AES-GCM), optionally with history.

---

## 6. Execution Flow / Runtime Flow

1. The service starts (`gwatch run`, or the service manager), resolves the data directory and database (§9), opens the store, runs migrations, loads `gwatch.key`.
2. The engine starts its scheduler, retention, backup, digest and collector jobs.
3. The HTTP server listens on `127.0.0.1:7230` by default and serves the embedded interface and the API.
4. Each due check runs; the result is processed (status, thresholds, dependencies, maintenance), written, and may open/close incidents, send notifications and fire triggers and rules; SSE notifies open pages.
5. Agents push readings or collector results; `serve`-mode agents are polled.

### Failure Points

- A persistent store write failure currently causes checks to re-run at about 1 Hz with no backoff (#211).
- A failed update verification, restore or migration stops rather than continuing.

---

## 7. Data Flow

- **Inputs:** check results from runners and remote collectors; agent readings; user configuration through the API; inbound endpoint calls; Matter server events.
- **Outputs:** stored history, incidents, events and audit entries; notifications; trigger actions (HTTP, git, scripts); exports (CSV, chart images, reports); backups.
- **Rules:** monitoring content (HTTP bodies, DNS answers, SNMP values, names) is untrusted when it reaches placeholders, emails, logs or the MCP companion. Nothing is sent anywhere the person did not configure, other than the update check to GitHub Releases.

---

## 8. State, Storage, and Persistence

The data directory (Windows `%ProgramData%\GWatch`; Linux `$XDG_DATA_HOME/gwatch` or `~/.local/share/gwatch`, `/var/lib/gwatch` for the packaged service; macOS `~/.local/share/gwatch`) holds `gwatch.db`, `gwatch.key` (mode 0600), `database.json` when a server database is chosen, `logs/` and `backups/`.

- Secrets in the settings table and in `database.json` are sealed with `gwatch.key`; losing the key loses those secrets (backups carry what is needed to restore).
- Discovery results and the newest hardware readings live in memory only.
- Schema migrations are applied at start-up on every dialect; `gwatch migrate-db` copies a SQLite install into PostgreSQL/MySQL.
- Map options are per-browser `localStorage`; the shared map arrangement is stored server-side.

---

## 9. Configuration Model

- **Process configuration:** `--data-dir`, `--listen`, `--trusted-proxies` and `--db-*` flags override `GWATCH_DATA_DIR`, `GWATCH_LISTEN`, `GWATCH_TRUSTED_PROXIES` and `GWATCH_DB_*`, which override defaults. Installed services read an environment file written by `gwatch install`.
- **Database choice:** default SQLite → `database.json` → `GWATCH_DB_*` → `--db-*` (later wins; `internal/dbconfig`).
- **Everything else** (nodes, checks, channels, retention, users, keys) is stored configuration, changed through the API.

---

## 10. External Services and Integrations

SMTP; Slack, Teams, Discord, Telegram, Matrix, ntfy, Gotify, Pushover, Twilio SMS, PagerDuty, Opsgenie; webhook actions; python-matter-server; GitHub Releases for updates. Each outbound integration exists only when configured. There is no inbound integration other than the API, custom endpoints (`/hook/<name>`, token-protected) and agent submissions.

---

## 11. AI Architecture and Provider Boundaries

GWatch contains no AI provider code. The MCP companion is a separate process using API keys; GWatch's route policy is the enforcement boundary (a `read` key is refused writes with HTTP 403 whatever the companion advertises, and every key is denied settings, backups, logs, automation, rules, discovery, pairing, updates, users and keys). `skill/SKILL.md` tells assistants to treat monitoring content as untrusted data.

---

## 12. Security, Privacy, and Secrets Boundaries

- Loopback-only by default; LAN access must be enabled and then requires sign-in. GWatch is not designed to be exposed to the internet; remote access is via the person's VPN or reverse proxy (`docs/REMOTE-ACCESS.md`), with `--trusted-proxies` for forwarded client addresses.
- The local administrator: a loopback client is the administrator with no sign-in unless "Require sign-in on this computer too" is on. GWatch's own outbound loopback connections never receive this shortcut (`internal/selfconn`).
- Scripts (custom checks, script actions, endpoints) can only be created by a signed-in administrator, never by an API key. Untrusted placeholder input in scripts requires an explicit acknowledgement.
- Passwords are hashed; API keys and sessions are stored as digests; secrets are sealed at rest; secrets are masked in API responses and scrubbed from logs.
- Updates and agent updates are verified against pinned ed25519 keys before swapping; there is no silent-skip path.
- On Windows the service normally runs as LocalSystem; on Linux as an unprivileged `gwatch` user in a hardened systemd unit with only `CAP_NET_RAW`.

---

## 13. Extension Points and Override Mechanisms

Custom script checks, triggers and actions with placeholders, inbound custom endpoints, the JSON API with scoped keys, notification rules, templates, and the MCP companion. There is deliberately no plugin system.

---

## 14. Generated Artifacts and Outputs

- Exports: CSV history and incidents, chart images, availability reports, config export (administrator only).
- Backups: `.gwbackup` encrypted archives.
- Release assets: per-platform binaries with `.sha256` and `.sig`, Windows setup programs, `.deb`/`.rpm`, multi-arch container images (`ghcr.io/jxburros/gwatch`), agent packages for Homebrew and winget.
- Generated source: see §3.

---

## 15. Build, Runtime, and Deployment Architecture

- **Local development:** Go 1.26 (newest patch), `go run . run --data-dir ./data`; Node only for web tests.
- **Build:** `CGO_ENABLED=0` static builds with `-X main.version` from `VERSION`; the Windows installers are built by Inno Setup on Windows.
- **Test / validation:** see `developmentManifesto.md` §10 and `.github/workflows/ci.yml` (Windows, Linux, macOS, Docker, packaging and service-lifecycle jobs).
- **Release:** `release.json` is the manifest; `.release/config.json` maps each app to the paths that require a version bump. The Release workflow is dispatched by hand from `main` (dry run first), builds, signs and publishes only apps ahead of their published version. Tags: `v<VERSION>` (gwatch), `agent-v<version>`, `mcp/v<version>`.
- **Runtime:** a Windows service, a systemd unit (OpenRC/SysV fallbacks), a launchd daemon, or a container.

---

## 16. Error Handling and Failure Modes

GWatch exposes its own health (Monitor health: service, scheduler, last check, database size, retention, backups, recent errors and the service log). Errors returned by the API are plain-language messages without secrets. Verification, restore and migration failures stop the operation.

---

## 17. Architectural Invariants

- One binary for the server; the interface is embedded and dependency-free.
- All authorisation goes through the route policy; API keys never reach scripts, settings, backups, logs, users or keys.
- All writes go through the store's single writer; schema changes are migrations valid on all three dialects.
- Agent connections are agent-initiated except opt-in `serve` mode; the agent never executes commands from GWatch.
- `gwatch-mcp` uses only the public API.
- Nothing is sent off the machine unless configured; the interface loads no third-party resources.
- Updates are refused unless the signature verifies against a pinned key.
- SNMP stays a check (numeric OIDs, polling only); discovery stays a person-started, capped sweep with in-memory results.

---

## 18. Known Tradeoffs and Constraints

- PostgreSQL/MySQL inherit SQLite's single-writer model (#212).
- The result write is held under the engine lock (#210).
- Windows setup programs are not Authenticode-signed (#120).
- A Docker install needs `docker exec` to create the first administrator (#271).
- Re-running the Windows setup program keeps the first install's port and LAN setting (#275).
- macOS agent readings lack CPU utilisation and temperatures; Windows lacks temperatures (#224).

---

## 19. When to Update This Document

Update this document when a change adds, removes or reorganises a subsystem; changes data flow, storage, migrations or retention; adds or removes an external integration; changes authentication, authorisation, secrets or the trust model; changes build, packaging, release or runtime architecture; changes configuration sources or precedence; or changes the API, CLI or agent protocol.

Usually do not update it for typo fixes, copy changes, styling, localised bug fixes, helpers inside a module, tests, or minor dependency bumps.

When updating: describe what now exists, keep it aligned with the code, preserve accurate information, remove stale information, and do not turn this file into a changelog, roadmap or file inventory (`docs/development/DEVELOPMENT.md` keeps the full layout). If unsure, record the uncertainty rather than inventing details.

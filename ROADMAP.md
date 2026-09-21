# GWatch Public Release Roadmap

**Positioning**: robust enough for a person to trust with their home network, basic
enough to stay simple, wide open to customize — the gap between Uptime Kuma and
Zabbix, not a Zabbix competitor. Work top-to-bottom: trust and stability ship before
reach and convenience. Don't start a "Next" item before its "Now" prerequisites are
done, and don't let scope creep pull any item toward the non-goals at the bottom.

---

## Status (September 2026)

Every phase below has been implemented on the public-release branch. What each item
became, and what still needs a human:

| Item | Where it lives | Follow-up |
|---|---|---|
| 1.1 endpoint tokens | `internal/api/automation.go`, Settings › Automation banner | — |
| 1.2 script placeholders | `internal/actions` (`expandScriptCode`, `buildGitArgs`, `allowUntrustedInput`) | `cmd` scripts get a sanitized literal, not a reference (cmd re-parses `%VAR%`); documented |
| 1.3 signed updates | `internal/update/sign.go`, `cmd/gwatch-sign`, `docs/RELEASING.md` | **Generate the release key, paste the public key into `internal/update/release_keys.txt`, add the `GWATCH_SIGNING_KEY` secret.** Until then self-update reports releases but refuses to install them (fail closed) |
| 1.4 secrets at rest | `internal/secrets`, `gwatch.key` next to `gwatch.db` | — |
| 2.1 admin/viewer accounts | `internal/auth`, `internal/api/policy.go`, Settings › Users & access, audit `actor` | — |
| 2.2 API keys, `/api/v1`, custom checks | `internal/api/auth.go`, `internal/checks/custom.go` | — |
| 2.3 Slack / Teams / ntfy / Pushover | `internal/actions/notify.go` | — |
| 2.4 scheduled backups, restore path | `internal/engine/backupjob.go`, `docs/RESTORE.md` | — |
| 2.5 read-only remote access | viewer role + read-only keys, `docs/REMOTE-ACCESS.md` | Decision: bring your own VPN/reverse proxy; GWatch never becomes internet-facing |
| 3.1 / 3.2 MCP server | separate module `mcp/` (`gwatch-mcp`), read-only by default; Settings › AI & MCP, `skill/` | Resolved: the tag scheme is `mcp/vX.Y.Z` (`docs/RELEASING.md`), and the in-app set-up page plus the versioned agent skill (#56) cover the "how do I set it up" gap |
| 3.3 trigger recipes | `docs/RECIPES.md` | — |
| 4.1 Windows installer | `scripts/installer/gwatch.iss`, `docs/INSTALL.md` | Not yet compiled on a Windows machine; run the CI job once and test install/upgrade/uninstall by hand |

## Phase 1 — Now: security hardening (blocking; do before any public release)

### 1.1 Require auth by default on custom endpoints
- **Problem**: `/hook/<name>` endpoints are exempt from the LAN access-password check
  (`internal/api/api.go:151`), and `handleHook` only checks a token `if e.Token != ""`
  (`internal/api/automation.go:322-330`). An endpoint created without a token is fully
  open to anyone who can reach the port.
- **Fix**: generate a random token by default when a custom endpoint is created (don't
  allow an empty token unless the user explicitly opts out with a clear warning in the
  UI). Add a settings-page banner/lint listing any existing tokenless endpoints.
- **Done when**: no endpoint can be saved without a token unless the user has
  explicitly acknowledged a warning dialog; existing installs get a one-time migration
  notice.

### 1.2 Escape/sandbox placeholder expansion into scripts
- **Problem**: `Expand()` (`internal/actions/actions.go:57-68`) substitutes
  `{{placeholder}}` values as raw text into script code (`:405-454`) before it's
  executed via `sh`/`bash`/`python`/`powershell`. `handleHook` feeds the entire raw
  POST body and every query parameter into `vars["body"]` / `vars["query.*"]`
  (`internal/api/automation.go:314-350`), and `TriggerVars`
  (`internal/engine/automation.go:112-149`) can carry content from monitored devices
  (HTTP responses, DNS answers). Any action that references `{{body}}`, `{{message}}`,
  or `{{node.name}}` inside script code is a command-injection vector for anything that
  can reach the node or the hook.
- **Fix options** (pick one, or layer both):
  1. Shell-escape/quote placeholder values when they're substituted into script text
     for the shell-based interpreters (`sh`, `bash`, `cmd`, `powershell`).
  2. Require an explicit "this action can execute untrusted input" checkbox before
     saving any script action whose code references `{{body}}`, `{{message}}`, or
     `{{query.*}}`, with the risk spelled out in plain language.
- **Done when**: a test exists that feeds shell metacharacters (`` ` ``, `$()`, `;`,
  `|`) through a placeholder into a script action and asserts they can't execute
  arbitrary commands, or that the UI blocks/warns before the action can be saved.

### 1.3 Real signature verification for self-update
- **Problem**: `Download()` in `internal/update/update.go:220-268` verifies a SHA-256
  checksum fetched from the same GitHub release as the binary, and silently skips
  verification if the `.sha256` sidecar is missing or unreachable (condition at
  `:257`). This guards against transit corruption, not a compromised or spoofed
  release.
- **Fix**: sign releases (e.g. `minisign` or GPG detached signature) with a key kept
  outside the release pipeline, embed/pin the public key in the binary, and refuse to
  install an update whose signature doesn't verify — no silent-skip path.
- **Done when**: an update with a missing or invalid signature is rejected, not
  installed-with-a-warning.

### 1.4 Stop storing secrets in plaintext
- **Problem**: SMTP password and LAN access password are stored unencrypted in the
  live SQLite settings row (`internal/model/model.go:430,469`; masked only in API
  responses via `internal/api/manage.go:229-255`). Backups already protect this data
  with Argon2id/AES-GCM; the live DB doesn't.
- **Fix**: encrypt these fields at rest (e.g. derive a machine-local key, or reuse the
  backup-crypto primitives in `internal/backup/crypto.go` for a single-value envelope).
- **Done when**: `gwatch.db` opened directly no longer reveals the SMTP or access
  password in cleartext.

---

## Phase 2 — Next: foundations for going public

### 2.1 Minimal multi-user (two tiers only)
- Add `admin` and `viewer` roles — not full RBAC. A viewer can see dashboards, charts,
  history, incidents, audit log; cannot edit nodes/checks/triggers/settings, run
  actions, or manage backups.
- Every login/action gets an attributed identity in the audit log (currently
  presumably single-actor).
- **Done when**: a viewer-role account can be created, logs in, and is blocked (with a
  clear message, not a silent 403) from every admin-only action.

### 2.2 API as a first-class surface
- Version the JSON API (`internal/api`, documented in `docs/API.md`) so breaking
  changes don't silently break integrations.
- Introduce API keys distinct from the UI access password, each scoped read-only or
  read-write, creatable/revocable from Settings.
- Add a scriptable "custom check" type: user supplies a command/script, GWatch runs it
  on schedule and parses a simple status/metric contract from its output — lets power
  users monitor anything without a new built-in check type per request.
- **Done when**: an API key can be minted with a read-only scope and used to query
  history/incidents but rejected on any write endpoint.

### 2.3 A few more notification channels
- Add Slack, Microsoft Teams, and one generic push service (ntfy or Pushover) as
  action/trigger targets alongside the existing email and webhook/script actions.
  Don't chase parity with Uptime Kuma's ~90 integrations — three or four covers most of
  this audience.
- **Done when**: each new channel has a "send test message" button, same pattern as
  the existing SMTP test-email flow.

### 2.4 Business-continuity backup workflow
- Add scheduled/automatic backups (currently one-click manual only, per
  `internal/backup`), configurable interval and retention count.
- Write and test a documented restore-to-a-new-machine path end to end (not just the
  existing restore code path in isolation).
- **Done when**: a scheduled backup runs unattended and a restore onto a fresh install
  reproduces the original configuration and (optionally) history.

### 2.5 Read-only remote access for off-network devices
- Let a user view already-monitored data (dashboards, current status, charts,
  incidents) from a phone or other device outside the LAN, without exposing
  config/write access or trigger/automation execution.
- This is the first time GWatch data is designed to leave the LAN, so scope it tightly:
  its own dedicated token/API-key type (reuse the scoping from 2.2), rate limiting, and
  explicitly no access to triggers, custom endpoints, backups, or settings.
- **Decided: no relay, no tunnel helper.** GWatch does not become an internet-facing
  service. The user brings their own remote access — Tailscale/WireGuard, or a reverse
  proxy with TLS — and points it at GWatch with a read-only API key or a viewer
  account. A service that runs commands, holds an SMTP password and can restore from an
  uploaded archive should not be published to the open internet, and shipping a tunnel
  would make doing so by accident easy. See [`docs/REMOTE-ACCESS.md`](docs/REMOTE-ACCESS.md).
- **Done when**: a read-only token can view dashboards/history from outside the LAN and
  is rejected on every write/trigger/settings endpoint.

---

## Phase 3 — Then: AI-controllable, not AI-integrated

Goal: let a user's own AI platform of choice add/manage nodes and checks and read
monitoring data, and let GWatch push simple triggers into other apps — without adding
AI features to GWatch itself, and without changing the core "no AI, local-only"
promise.

### 3.1 Standalone MCP server (separate from the core binary)
- Build it as its own package/binary that talks to GWatch's existing JSON API (from
  2.2), not embedded in `gwatch` itself. Keeps the trusted monitoring service's attack
  surface and release cycle unchanged; this piece can ship and version independently.
- **Done when**: the MCP server can be installed/run separately from `gwatch` and
  requires its own explicit opt-in (not on by default).

### 3.2 Scoped read/write MCP tools
- Expose read tools (list nodes/checks, query history, incidents, monitor health) and
  write tools (create/edit/delete nodes and checks) as distinct MCP capabilities, each
  gated by the read-only vs read-write API key scopes from 2.2.
- Treat "an external AI agent can create/delete checks" as a materially bigger trust
  boundary than a human clicking through the UI — default new API keys used by the MCP
  server to read-only, require an explicit step to grant write.
- **Done when**: an MCP client with a read-only key can query data but every write tool
  call is rejected server-side (not just hidden client-side).

### 3.3 Outbound trigger documentation (mostly already built)
- The existing triggers/webhook system (`internal/actions`, `internal/engine`) already
  covers "send a simple trigger for alerts in other apps." This phase is primarily
  documentation and a few ready-made recipes (e.g. "send a GWatch down-event to Home
  Assistant," "post to a generic webhook consumer"), not new engineering.
- **Done when**: docs/API.md or a new docs page has at least 2-3 copy-pasteable trigger
  recipes for common external tools.

---

## Phase 4 — Later: deployment polish

### 4.1 Desktop installer
- Build a proper installer (Windows first, matching the current
  `scripts/install.ps1` flow; consider macOS/Linux packages too) — but only once
  Phases 1-3 have stabilized. An installer locks in an interface (what gets installed,
  what ports/services get set up, the first-run flow), so it should wrap a settled
  feature set rather than get redone every time a new capability lands.
- The existing PowerShell scripts are a reasonable interim path and don't block
  anything above — no urgency here.
- **Done when**: a non-technical user can install, configure network access, and reach
  the web UI without touching PowerShell or the CLI.

---

## Open decisions (don't resolve casually)

- **Licensing/support model**: decided. GWatch is source-available (see `LICENSE`
  and `TRADEMARKS.md`), close to open source but with two restrictions: nobody may
  charge for unaltered copies of the software (including offering it as a paid
  hosted service without a Significant Modification), and nobody may remove or
  obscure credit to GWatch, JX Holdings, and the original developers. Consequences
  for this roadmap: there are no paid tiers for the API keys (2.2) or the MCP
  companion (Phase 3) — everything ships free. The only commercial restriction is
  reselling unmodified GWatch or offering it as a paid hosted service without
  significant modification; deployment, support, and customization services remain
  fair game. Attribution is mandatory in every copy and derivative.

## Explicit non-goals (keep scope from drifting toward Zabbix/PRTG)

Do not add: SNMP, distributed/multi-site monitoring, a plugin marketplace, or full RBAC
(beyond the two-tier admin/viewer split in 2.1). Each would pull GWatch toward
enterprise-NMS territory and away from the "basic but yours to shape" niche that's
actually working.

**Host metrics were on this list and came off it.** The reasoning that put them here was
that agents are what turns a monitor into an NMS: a fleet to deploy, a protocol to
version, a config management problem. The reasoning that took them off is that the
question "is the machine in trouble?" is the one immediately after "does it answer?", and
a home server that still answers a ping while its disk fills is exactly the failure this
tool exists to catch. What keeps it from being the thin end of the wedge is the shape of
what shipped, and that shape is the constraint to hold:

- **One binary, one direction, one permission.** `gwatch-agent` posts a reading and hangs
  up. GWatch never connects to it and holds no credential for it; the agent's token can
  submit that one machine's readings and nothing else. There is no remote execution, no
  remote configuration and no fleet management — and none should be added.
- **No metric language.** Readings are a fixed struct: processor, memory, filesystem,
  network, disk throughput. Not a metrics pipeline, not arbitrary series, not something
  with a query language. A new reading is a field, reviewed like any other field.
- **Nothing to deploy.** Registering a machine is one screen and one command. If keeping
  agents working ever needs config management, the feature has grown wrong.

Temperatures and SMART were considered and left out: both are patchy across vendors on
Windows, and SMART needs elevated privileges on the watched machine. Revisit only if
someone actually wants them, and only within the constraints above.

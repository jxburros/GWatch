# GWatch localhost API

All endpoints are served by the local service on `http://127.0.0.1:7230` (configurable) and
return JSON unless noted. Errors are `{"error": "message"}` with a 4xx/5xx status.
Timestamps are RFC 3339 strings. Field names match `internal/model/model.go`.

Static UI: `GET /` serves `web/index.html`; `/app.js`, `/app.css` etc. are served from `web/`.

## Versioning

`/api/v1/…` is the stable prefix. Every route documented here is served under both
`/api/…` and `/api/v1/…`; the unprefixed form is an alias of the current version and
follows it. Breaking changes bump the prefix (`/api/v2/…`) and the old prefix keeps
working for at least one release, so integrations should use `/api/v1/…`.

Every API response carries `X-GWatch-API-Version: 1`, and `GET /api/version` returns
`{"version": "0.5.0", "platform": "windows/amd64", "apiVersion": 1}` (`platform` is
omitted for callers who have not identified themselves).

## Platform notes

- **Ping** builds its own ICMP echo requests; there is no ping library underneath it.
  On Linux and macOS it tries an unprivileged ICMP socket first and then a raw one; on
  Windows it goes straight to a raw socket, which the service account may open. If none
  of those is permitted it falls back to shelling out to the system `ping` command —
  whichever works, it works without any setup on your part. `general.pingMethod`
  (`auto`, the default, `builtin` or `system`) chooses between that order, the built-in
  sender alone and the system command alone, and a check may override it with
  `config.pingMethod` (the same three values, or `""` to follow the setting).
- **Email alerts** work with any SMTP provider: STARTTLS on port 587, implicit TLS on
  465, or no encryption at all. Use **Settings › Alerts › Send test email** before
  relying on it — that exercises the exact configuration a real alert would use.

## Authentication and roles

Every request resolves to a **principal**, in this order of precedence:

| Kind | How it identifies itself | Standing |
| --- | --- | --- |
| `apikey` | `Authorization: Bearer gw_…` or `X-API-Key: gw_…` | its scope, never administrator |
| `user` | the `gwatch_session` cookie from `POST /api/auth/login` | its role, `admin` or `viewer` |
| `password` | HTTP basic auth with the legacy access password (any user name) | administrator |
| `local` | a client on the computer GWatch runs on | administrator |
| anonymous | nothing | nothing but the public routes |

A browser on the computer GWatch runs on is an administrator without signing in. That
is what keeps a fresh install, and every install that predates accounts, working with
no setup. Turn on `general.requireLoginLocally` once accounts exist and even loopback
has to sign in. The legacy access password keeps working for existing scripts.

A loopback request that carries a reverse proxy's forwarding headers (`Forwarded`,
`X-Forwarded-For`, `X-Forwarded-Host`, `X-Forwarded-Proto` or `X-Real-IP`) is not
`local`: it came through a proxy on this machine on behalf of someone elsewhere, and is
judged like any other remote client — whether or not the proxy is listed in
`--trusted-proxies`, and whatever address it forwards. A signed-in account is always itself, even on the
computer GWatch runs on — the cookie is resolved before the loopback shortcut.

If the database cannot be asked whether a session cookie or API key is valid, the
request is answered `503` with a short `Retry-After` rather than being treated as
signed out; try again.

`GET /api/me` reports the current principal:

```json
{"kind":"user","name":"pat","role":"admin","isAdmin":true,"canWrite":true,"signedIn":true,
 "theme":"dark","accentColor":"#43c9c0",
 "indicators":[{"id":"nodes-down","name":"Nodes down","enabled":true,"colour":"red",
                "condition":{"kind":"nodesInStatus","status":"down","minCount":1}}]}
```

`theme`, `accentColor` and `indicators` come from settings, which a viewer may not
read. They ride along here so that every account is styled the same and evaluates the
same header indicators. `indicators` is the effective list: normalised, with ids and
counts filled in, and seeded with the defaults when none are stored.

**Roles.** `admin` may change anything. `viewer` may read the overview, status,
wallboard, stream, nodes and checks, history, events, maintenance, dashboards, wallboards,
charts,
groups, templates, the CSV exports of history/results/events, the service log, and the
list of triggers and endpoints — with the endpoint `token` field blanked and a
`hasToken` boolean added. Everything else answers 403.

**API keys** are minted in Settings › Users & access (`POST /api/apikeys`) and shown
once. Only a sha256 digest and the first 12 characters are stored. A key is never an
administrator, whatever its scope:

- `read` — the viewer read list above, minus the service log, triggers, endpoints,
  automation metadata, retention status and update status. A check's request
  headers, request body, custom-check environment and agentless metrics URL are
  blanked for a key, since they routinely carry credentials; the SNMP and metrics
  secrets come back masked, the same as for a browser.
- `readwrite` — the same, plus creating, updating, deleting, enabling, running and
  silencing nodes and checks, notes, maintenance windows, dashboards, wallboards and
  charts. A key may lay a wallboard out; it may not project one. It may **not**
  create, edit or test a `custom` check, or a `system` check that reads an
  agentless URL: both run code or carry a stored token to a chosen address, so an
  administrator must create them in the web interface (403, not 401). The same goes
  for a saved one: a key may not run it, switch it (or a node holding a switched-on
  one) on, duplicate a node holding one, or touch one in a bulk edit — switching one
  off is still allowed. `POST /api/nodes/{id}/run` through a key skips such checks and
  runs the rest; a key cannot run a check or node that is switched off (409).
  A key also may not point a saved check at a new destination — the node host, the
  check's target (address, scheme or port), SNMP port, method, `Host` header, type,
  redirect or TLS setting, DNS server, or hardware source — while leaving any of its
  credentials (body, masked headers or environment values, SNMP secrets, metrics
  token) blank or masked to be kept: that would send a secret the key cannot read to
  an address it chose. Such a `PUT /api/nodes/{id}` is refused with 403; send every
  credential afresh, or have an administrator retarget the check in the browser.

**Denied to every key, whatever its scope** (403, not 401):
`GET|PUT /api/settings`, `POST /api/settings/test-email`, `GET|PUT /api/database`,
`POST /api/database/test`, `GET /api/network`, everything under `/api/backups`, `GET /api/export/config.json`, `GET /api/logs`,
`GET /api/export/logs.txt`, everything under `/api/triggers` (including `GET`),
everything under `/api/endpoints` (including `GET`), `POST /api/actions/test`,
`GET /api/automation/meta`, `GET /api/retention/status`, `POST /api/retention/run`,
`GET|POST /api/update/…`, `GET /api/mcp/status`, `GET /api/mcp/skill`, everything under
`/api/users` and `/api/apikeys`, and `POST /api/auth/change-password`.

That list is deliberate and tested: a key is for reading a monitor from elsewhere, not
for administering the machine it runs on. It is the same boundary `gwatch-mcp`, the Model
Context Protocol companion in [`../mcp/`](../mcp/README.md), runs into.

**Status codes.** `401` with `{"error":"sign in required"}` means no valid credential
was presented; `WWW-Authenticate: Basic` is only sent when the install still uses the
old access password and has no accounts. `403` means the credential is valid but not
entitled, and the message says why — for example `This API key is read-only.` or
`This action needs an administrator account (you are signed in as viewer "pat").`

**Cross-site writes.** The `local` and `password` principals are both granted by
something the browser supplies on its own — nothing at all on loopback, and cached basic
auth credentials for the access password — so a non-GET request from either one carrying
an `Origin` header that names a different host is refused with 403. A request with no
`Origin` (curl, a script) is unaffected, and so are `user` and `apikey` — the session
cookie is `SameSite=Lax` and is not sent on a cross-site write in the first place, and a
key is not something a cross-site page can obtain.

**Request bodies.** A request that carries a body must declare
`Content-Type: application/json` (parameters such as `; charset=utf-8` are fine, and a
`…+json` suffix is accepted). `text/plain`, `application/x-www-form-urlencoded` and
`multipart/form-data` are refused with `415` — those are the content types a browser will
send cross-origin without a preflight, so insisting on JSON puts every write behind one.
A malformed JSON document is still a `400`. The multipart upload at
`POST /api/backups/restore` is the one exception, and takes a file rather than a body.

**Rate limits.** Failed credentials (API key, access password, agent token and
custom-endpoint token) are limited to 10 per minute per client IP; API-key requests from
off this machine are limited to 300 per minute per client IP. Password checks — sign-in
and the current password of `POST /api/auth/change-password` — are limited to 10
attempts per minute per account name and client IP, which a correct password clears for
that account only, and to 30 attempts per minute per client IP across all account names,
which a correct password does not clear. They are refused before the password is checked,
and at most two run at once; when both are busy the answer is `429` with `Retry-After: 1`.
All of these answer `429` with `Retry-After` in seconds. The client IP is the connecting
address, or, when that address is listed in `--trusted-proxies`, the client the proxy
names in `X-Forwarded-For` / `X-Real-IP` (see [REMOTE-ACCESS.md](REMOTE-ACCESS.md#rate-limiting)). A `/hook/` call that presents the right token gives its budget
back, so an endpoint called on a schedule is never throttled by its own traffic; an
unknown slug still answers `404`, but it is charged for, so slugs cannot be enumerated.

**Response headers.** Every response, the static UI included, carries:

| Header | Value |
| --- | --- |
| `Content-Security-Policy` | `default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; font-src 'self'; connect-src 'self'; base-uri 'none'; object-src 'none'; form-action 'self'; frame-ancestors 'none'` |
| `X-Content-Type-Options` | `nosniff` |
| `X-Frame-Options` | `DENY` |
| `Referrer-Policy` | `same-origin` |

GWatch serves its own bundle and talks to nothing but itself, so every source is `'self'`.
The wallboard page (`/wall`, `/wall.html`) is the one exception: it is read-only and gated
on its board's share token, and putting one in a dashboard elsewhere is a real use, so it
is sent `frame-ancestors *` and no `X-Frame-Options`. Everything else refuses to be framed.

**Public routes**, reachable without any credential: `GET /api/health` (liveness only —
`{"serviceRunning","schedulerRunning","now"}` — until the caller identifies itself),
`GET /api/version`, `GET /api/me`, `GET /api/auth/setup`, `POST /api/auth/login`,
`POST /api/auth/logout`, the static UI (including `/wall`), and the routes that
take a credential of their own in place of the sign-in:
`GET /api/wallboards/{id}/view` (the token of a board whose sharing is switched on;
every other caller is refused), `POST /api/agents/pair` (a one-use pairing code, with
wrong codes counted against the same per-IP budget as wrong passwords), `/ingest/metrics`
and `/ingest/checkin` (an agent token), and `/hook/…` (each endpoint's own token).

Anything not listed in the policy table is admin-only with API keys denied, so a new
route is closed until it is opened on purpose.

### Sign-in and accounts

- `GET /api/auth/setup` → `{ "usersConfigured": bool, "loginRequired": bool, "accessPasswordSet": bool, "localLoginForced": bool, "apiVersion": 1 }`. `loginRequired` is about *this* client.
- `POST /api/auth/login` body `{ "username", "password" }` → the principal, and sets `gwatch_session` (HttpOnly, SameSite=Lax, 30-day sliding expiry, `Secure` only when GWatch itself terminates HTTPS). Rate limited. While the session is in use its expiry slides forward about once an hour, and the response that slides it re-issues the cookie with the new expiry, so a session in regular use does not run out.
- `POST /api/auth/logout` → `{ok:true}` and clears the cookie.
- `POST /api/auth/change-password` body `{ "current", "new" }` → changes your own password and signs every browser of that account out. Any signed-in user; never an API key.
- `GET /api/users` → `[User]`. `POST /api/users` body `{ "username", "password", "role": "admin"|"viewer" }` → `User` (201). The first account created is always an administrator, and on an install with no accounts a `local` or `password` principal may create it.
- `PUT /api/users/{id}` body `{ "role"?, "password"? }` → `User`. A new password ends that account's sessions; a new role applies to them from their next request, without signing them out.
- `DELETE /api/users/{id}` → `{ok:true}`. Deleting or demoting the last administrator is refused with 400; the check is made in the same database transaction as the change, so two simultaneous requests removing the last two administrators cannot both succeed.
- `GET /api/apikeys` → `[APIKey]` (revoked keys included, so the trail keeps their names).
- `POST /api/apikeys` body `{ "name", "scope": "read"|"readwrite" }` → `{ "key": "gw_…", "apiKey": APIKey }` (201). An omitted scope means `read`. **The key is returned once and cannot be shown again.**
- `DELETE /api/apikeys/{id}` → `{ok:true}`, revoking it immediately.

Reaching GWatch from outside your own network is covered in
[`REMOTE-ACCESS.md`](REMOTE-ACCESS.md). `gwatch-mcp`, the optional Model Context Protocol
companion that lets an AI assistant use this API with a key, is documented in
[`../mcp/README.md`](../mcp/README.md).

## Health & overview

- `GET /api/health` → `model.Health` (service mode, scheduler, last/next check, db size, retention status, backup status, recent internal errors, alert config state). Also carries the on-disk paths: `dataDir` (the data directory), `databasePath` (`<dataDir>/gwatch.db`, or a password-free `postgres://user@host:port/db` when GWatch is on a server — [`DATABASE.md`](DATABASE.md)), `keyPath` (`<dataDir>/gwatch.key`) and `backupDir` (`<dataDir>/backups`) — see [`INSTALL.md`](INSTALL.md#where-your-data-lives) — and `databaseDriver`, the database driver in use: the SQLite driver the binary was built with (see [`INSTALL.md`](INSTALL.md#choosing-the-sqlite-driver)), or the PostgreSQL/MySQL one.
- `GET /api/overview` → 
  ```json
  {
    "summary": {"up":0,"degraded":0,"down":0,"unknown":0,"paused":0,"maintenance":0,"total":0},
    "nodes": [ {"node": Node (with checks), "status": "up", "checks": [ {"check": Check, "state": CheckState, "lastResult": Result|null} ], "inMaintenance": false, "affectedBy": "Gateway"|"" } ],
    "groups": [ {"name":"Home Network","status":"up","up":3,"degraded":0,"down":0,"unknown":0,"paused":0,"maintenance":0,"total":3} ],   // a node counts in every group it is in, so these totals can exceed summary.total; a node with no group at all is counted under "Ungrouped"
    "incidents": [ Event ... ]      // open down/degraded conditions as most recent related events (max 20)
    "certWarnings": [ {"nodeId":1,"nodeName":"Site","checkId":3,"checkName":"HTTPS","daysRemaining":9,"notAfter":"...","subject":"..."} ],
    "attention": [ {"nodeId":1,"nodeName":"...","checkId":2,"checkName":"...","status":"down","message":"...","since":"...","affectedBy":""} ],
    "maintenance": [ MaintenanceWindow ... ]  // currently active windows
    "generatedAt": "..."
  }
  ```
- `GET /api/wallboard` → same shape as overview plus `"health": Health` and `"trends": [HistorySeries...]` for up to 6 most important checks over 24h. This is the automatic board; a configured one is read from `/api/wallboards/{id}/view` (see Wallboards).
- `GET /api/status` → header summary: `{ "down", "degraded", "unknown", "up", "total", "certWarnings", "maintenance", "attention", "serviceOk", "serviceIssues": [..] }`.
  This one document is also what the header indicators are evaluated against, in the browser (see Indicators).
- `GET /api/network` → `NetworkInfo`: effective listen address, whether other devices can reach it, LAN URLs, whether a password is set.

## Nodes and checks

**Groups.** A node belongs to zero or more groups, carried as `"groups": ["Home Network", "Critical"]` — always a list, never `null`. `"group"` is a **deprecated** alias kept for one release: on a read it is the first entry of `groups` (`""` when there are none), and on a write it is used only when `groups` is absent or empty, in which case the node ends up in that one group. Group names are trimmed, blanks are dropped, names that differ only in case are folded onto the first spelling given, and the list is capped at 16. Everything that filters or groups by a group — maintenance windows, dashboard and wallboard panels, the node list — matches **any** of a node's groups.

- `GET /api/nodes` → `[Node]` each with `checks`, plus live state: each node object also carries `"status"`, `"stateByCheck": {checkId: CheckState}`, `"inMaintenance"`.
- `POST /api/nodes` body: `Node` (with `checks`, ids omitted) → created `Node`.
- `GET /api/nodes/{id}` → `Node` with checks, `stateByCheck`, `lastResults: {checkId: Result}`, `"status"`.
- `PUT /api/nodes/{id}` body: full `Node` with `checks`. Checks with an `id` are updated, without are created, missing ones are deleted. → updated `Node`.
- `DELETE /api/nodes/{id}` → `{ "ok": true }`.
- `POST /api/nodes/{id}/enable` body `{ "enabled": true|false }` → Node.
- `POST /api/nodes/{id}/duplicate` → new Node (name suffixed " (copy)", disabled).
- `POST /api/nodes/{id}/run` → runs all enabled checks of the node now → `[Result]`. They share the scheduler's `maxConcurrentChecks` limit, so a large node runs a few at a time. A node holds at most 500 checks (400 above that).
- `POST /api/nodes/{id}/silence` body `{ "minutes": 60 }` (0 = unsilence) → silences every check of the node → Node.
- `PATCH /api/nodes/bulk` — one change across many nodes and checks. See **Bulk edit** below.
- `GET /api/templates` → `[NodeTemplate]` (website, home-server, router — shown as "Network device", ping, api, tcp-service, this-computer, agent-machine, dns).
- `GET /api/groups` → `{ "groups": [{"name":"...","count":3}], "tags": [{"name":"...","count":2}], "checkGroups": [{"name":"...","count":4}] }`. `checkGroups` counts checks by their own groups (`config.groups` on a check, up to 10 names of at most 64 characters, case-insensitive). A node in several groups is counted once in each of them, so the counts can add up to more than the number of nodes.

**Credentials inside a check's config** — `metricsToken` (hardware health via a metrics
URL) and `snmpCommunity`, `snmpAuthPass`, `snmpPrivPass` (SNMP) — follow the same
convention as the SMTP password in settings. They are sealed with the machine-local key
file (`gwatch.key`, see [the store](../internal/store)) before they reach the database,
they come back from every read (`/api/nodes`, `/api/nodes/{id}`, `/api/overview`,
`/api/wallboard`) as `********` rather than in the clear, and on a `PUT /api/nodes/{id}`
a field that arrives blank **or** as `********` keeps the stored value. To change one,
send the new value; `/api/export/config.json` omits them entirely. `POST
/api/checks/test` fills them in from the stored check when the body carries a check `id`,
so testing a saved check from the editor does not need the editor to hold the secret.
Request `headers` are *not* treated this way: they are free-form and are stored and
echoed as written, so an `Authorization` header on an HTTP check is stored in the clear.

- `POST /api/checks/test` body: `{ "check": Check, "nodeHost": "..." }` → `Result` (not recorded; for validating unsaved config).
- The `custom` check type (`Check.type == "custom"`) runs a user-supplied command on the
  check's schedule instead of one of the built-in check types. Its config
  (`Check.config`): `command` (required — a command line, split on whitespace honouring
  single/double quotes; **no shell is used**, so pipes, globbing and `$VAR` expansion do
  nothing unless the command itself is `sh -c '...'` or, on Windows, `cmd /C ...`),
  `workDir` (optional working directory), `env` (optional map of extra environment
  variables, names matching `^[A-Za-z_][A-Za-z0-9_]*$`), plus the usual `target` (passed
  to the command as the `GWATCH_TARGET` environment variable, and substituted for the
  literal `{{target}}` inside any single argument that contains it — a target with spaces
  stays one argument, it is never re-split). The check's `timeoutSeconds` bounds each
  attempt; the process is killed and the result reported `"timed out"` if it runs over.
  **The command runs on the machine hosting GWatch with the service's own permissions —
  only trusted administrators should be able to create or edit a custom check.**

  Exit code 0 → up, 2 → degraded, anything else → down. Stdout may additionally contain
  lines of the form `key=value` (case-insensitive keys), one per line:
  - `status=up|degraded|down` — overrides the exit-code-derived status.
  - `message=...` — shown as `Result.message`.
  - `latency_ms=<number>` — sets `Result.latencyMs` so it appears on charts.
  - `error=...` — sets `Result.error` (used as the message too, when `message=` is absent
    and the check failed).

  Every other line of stdout and stderr (i.e. not recognised as one of the control lines
  above) is combined and kept, capped at 8 KiB, as `Result.details.output`.
- The `snmp` check type (`Check.type == "snmp"`) reads one or more OIDs off a network
  device — a router, a switch, an access point — in a single GET per run (chunked at 20
  OIDs), and turns each reading into its own metric with its own thresholds. See
  [SNMP.md](SNMP.md) for enabling SNMP on a device and choosing OIDs. Its config
  (`Check.config`):

  | field | meaning |
  | --- | --- |
  | `snmpVersion` | `"2c"` (default) or `"3"` |
  | `snmpPort` | UDP port, default 161 |
  | `snmpCommunity` | v2c community string, default `public` |
  | `snmpUser` | v3 security name (required for v3) |
  | `snmpAuthProto` | `""` (noAuth), `MD5`, `SHA`, `SHA224`, `SHA256`, `SHA384`, `SHA512` |
  | `snmpAuthPass` | required when `snmpAuthProto` is set |
  | `snmpPrivProto` | `""` (no encryption), `DES`, `AES`, `AES192`, `AES256`, `AES192C`, `AES256C` |
  | `snmpPrivPass` | required when `snmpPrivProto` is set; privacy also requires authentication |
  | `snmpOids` | up to 64 readings, see below |

  Each entry of `snmpOids` is `{ oid, name, kind, scale, unit, warnAbove, critAbove,
  warnBelow, critBelow }`. `oid` is dotted numeric, with or without a leading dot (MIB
  names are refused — GWatch ships no MIB files). `name` identifies the metric in
  results and charts, so names must be non-empty and unique within the check. `kind` is
  `"gauge"` (default — the value means something as it stands) or `"counter"` (a total
  that only climbs, evaluated as its per-second rate of change between consecutive runs,
  wrap-aware for 32- and 64-bit counters). `scale` multiplies the value or rate before
  it is compared and stored (`8` turns octets per second into bits per second, `0.01`
  turns TimeTicks into seconds); 0 and 1 both mean "as read". The four thresholds are
  optional and compared **strictly**: crossing a `warn` marks the check degraded,
  crossing a `crit` marks it down. Setting `critAbove` and `critBelow` to the same number
  means "must be exactly this", which is how an interface's operational status is
  expressed. Validation requires `warnAbove < critAbove` and `warnBelow > critBelow`.

  A run reports every OID in `Result.details.snmp`, an array of
  `{ oid, name, raw, value, rate, unit }`. `raw` is the value as the device sent it;
  `value` is the scaled number for a gauge; `rate` is the scaled per-second rate for a
  counter. A value that is not a number (`sysDescr`, a MAC address) is reported as `raw`
  only and never thresholded, and a counter has neither `rate` nor a verdict on the
  first run after the service starts, because there is nothing to compare against. An
  OID the device does not implement makes the check down and is named in the message.
  Every OID that produced a number also appears in `Result.metrics` (see below).
  A device that does not answer makes the check down with the usual network wording;
  note that SNMP v2c answers a *wrong community* with silence, so a wrong community and
  an unreachable device produce the same timeout — the message says so.
- The `matter` check type (`Check.type == "matter"`) reads Matter devices through a
  python-matter-server sidecar over its WebSocket API, one `read_attribute` per run for
  every reading. See [MATTER.md](MATTER.md) for the sidecar, commissioning and the
  conversions. Its config:

  | field | meaning |
  | --- | --- |
  | `matterServer` | the sidecar's WebSocket URL, `ws://` or `wss://`; blank means the node host on `ws://<host>:5580/ws`, and `host[:port]` alone is accepted |
  | `matterNodeId` | the node id the sidecar gave the device (required) |
  | `matterReadings` | 1–64 readings, see below |

  Each entry of `matterReadings` is `{ endpoint, kind, cluster, attribute, scale, name,
  unit, warnAbove, critAbove, warnBelow, critBelow }`. `kind` is one of the standard
  readings — `temperature` (°C), `humidity` (%), `onoff` (1 on / 0 off), `contact`
  (1 open / 0 shut), `occupancy` (1 occupied), `illuminance` (lux), `battery` (%) —
  whose cluster, attribute and conversion are fixed (and `cluster`, `attribute` and
  `scale` are cleared on save), or `attribute`, which reads `cluster`/`attribute`
  (decimal ids, cluster required) as a number times `scale`. A standard reading saved
  without a `name` or `unit` gets the default ones (`Temperature`, `°C`, …). Names must
  be non-empty and unique within the check; thresholds work exactly as an SNMP
  reading's.

  A run reports every reading in `Result.details.matter`, an array of
  `{ name, endpoint, path, raw, unit, value }` — `path` is `endpoint/cluster/attribute`,
  `raw` is the reading in words (`21.37`, `open`, `on`), `value` the number it was
  judged and charted as — and the sidecar it asked in `Result.details.matterServer`.
  Every reading with a number is in `Result.metrics`. An attribute the endpoint does not
  have, an unavailable or unknown node and an unreachable sidecar make the check down; a
  sensor reporting `null` makes it degraded. `latencyMs` is the live read's round trip.
- `POST /api/checks/{id}/run` → `Result` (recorded and processed through alerting).
- `POST /api/checks/{id}/enable` body `{ "enabled": bool }` → Check, masked (and for an API key stripped) the same way as in `GET /api/nodes/{id}`.
- `POST /api/checks/{id}/silence` body `{ "minutes": 60 }` (0 = unsilence) → CheckState.
- `GET /api/checks/{id}/results?limit=50` → `[Result]` newest first.
- `GET /api/checks/{id}/state` → CheckState.

### Node and Check objects

A `Node` is `{id, name, host, groups, group, tags, notes, importance, enabled,
dependsOnNodeId, linkStyle, template, createdAt, updatedAt, checks}`. `host` is the default target
its checks inherit when their own `config.target` is empty; `importance` is `low`,
`normal`, `high` or `critical`; `dependsOnNodeId` is the parent used for
dependency-aware alert suppression; `template` records which template the node was
created from and is informational only. `group` is the deprecated single-group alias
described above.

`linkStyle` is how the line from this node to its parent (`dependsOnNodeId`) is
drawn on the network map, or `null` for "like the map". It is
`{ "color": "#3b82f6", "width": 3, "dash": "dashed" }`, and every field is
optional and left out when it is not set. `color` is `#rgb`, `#rrggbb` or
`#rrggbbaa` and nothing else (it is returned in lower case); `width` is the
thickness in the map's drawing units, 1 to 8 (a number outside that range is
clamped, one decimal is kept); `dash` is `solid`, `dashed`, `dotted` or
`dash-dot`. A colour or dash that is not one of these is a 400 on create, update
and bulk edit. The style belongs to the link, so a node with no
`dependsOnNodeId` has none (it is dropped when the dependency is cleared, and
when the parent is deleted) and one pointed at a different parent keeps it.
It is part of the node everywhere a node goes: the overview, the configuration
export, backups and restores, and `POST /api/nodes/{id}/duplicate`.

A `Check` is `{id, nodeId, type, name, enabled, intervalSeconds, timeoutSeconds,
retries, failureThreshold, config, alerts, sortOrder, createdAt, updatedAt}`.
`intervalSeconds` is bounded below by `general.minIntervalSeconds` (10 by default) and
by a hard floor of 10 seconds that no setting lifts; `timeoutSeconds` bounds one
attempt; `retries` is how many immediate retries happen inside a single run before it
counts as a failure; and `failureThreshold` is how many consecutive failed runs make
the check `down` (`0` follows `alerts.failureThreshold`).
`alerts` is an optional per-check `AlertOverride`; `sortOrder` is the position in the
node's list.

### Check config by type

Every type reads `config.target`, which overrides the node's `host`: a hostname or IP
for `ping`, `tcp`, `dns` and `cert`, a URL for `http`, `keyword` and `json` (the scheme
is optional and `https` is assumed). `latencyWarnMs` and `packetLossWarnPct` are the
warning thresholds on any type that measures them (`0` means off, and an unset value
falls back to `general.latencyWarnMs` / `general.packetLossWarnPct`).

| type | config fields |
| --- | --- |
| `ping` | `pingCount` (packets per run, default 4, max 20), `pingMethod` (`""` follows `general.pingMethod`, else `auto`/`builtin`/`system`) |
| `http` | `method` (default `GET`), `expectedStatus` (`"200"`, `"200-299"`, `"200,301,302"`; default `"200-399"`), `followRedirects` (default `true`), `headers`, `body`, `ignoreTlsErrors`, `certCheck` (also judge certificate expiry — default `true` on https), `certWarnDays`, `contentWatch`, `contentHeader` |
| `keyword` | everything `http` takes, plus `keyword` (the text that must be present) and `keywordAbsent` (`true` inverts it: the text must **not** be present) |
| `json` | everything `http` takes, plus `jsonPath` (dotted, e.g. `status` or `data.items[0].name`), `jsonExpected` (empty means "the path must merely exist") and the recording fields `jsonRecord`, `jsonMetric`, `jsonUnit`, `jsonWarnAbove`, `jsonCritAbove`, `jsonWarnBelow`, `jsonCritBelow` — see [Named metrics](#named-metrics) |
| `cert` | `port` (default 443), `certWarnDays` (warn this many days before expiry, default 14) |
| `tcp` | `port` (required) |
| `dns` | `recordType` (`A` — the default, which resolves A and AAAA — or `CNAME`, `MX`, `TXT`), `expectedIps` (optional list; every resolved value must be in it) and `dnsServer` (optional resolver `host[:port]`; the system resolver otherwise) |
| `custom` | `command`, `workDir`, `env` — see the output contract above |
| `system` | `hostSource`, `agentId`, `metricsUrl`, `metricsToken`, `metricThresholds`, `diskMounts`, `staleAfterSeconds` — see [Hardware check metrics](#hardware-check-metrics) |
| `snmp` | the `snmp*` fields in the table above |
| `matter` | `matterServer`, `matterNodeId`, `matterReadings` — see the `matter` table above |

`contentWatch` turns an `http`, `keyword` or `json` check into a change watch as well:
`""` (off), `"hash"` (the body's digest), `"header"` (the header named by
`contentHeader`, e.g. `ETag` or `Last-Modified`), `"redirect"` (the final URL), or
`"keyword"` / `"json"` (the text or value the check already extracts). A change records
a `content_changed` event and marks the check degraded.

A `system` check's `hostSource` says which machine it reads: `local` (the computer
GWatch runs on), `agent` (the newest reading pushed by the registered machine named by
`agentId`) or `url` (scraped from `metricsUrl`, with `metricsToken` as the bearer
token). `staleAfterSeconds` is how old a reading may be before the check calls the
machine down; `0` means three times the check's interval. See
[`HARDWARE.md`](HARDWARE.md).

The flat hardware thresholds `cpuWarnPct`, `cpuCritPct`, `memWarnPct`, `memCritPct`,
`swapWarnPct`, `diskWarnPct`, `diskCritPct`, `loadWarnPerCore` and `loadCritPerCore`
are **deprecated**. They are still read, so an older configuration keeps working, and
are converted into `metricThresholds` the first time the check is saved; nothing writes
them any more.

A `Result` is one observation: `{id, checkId, ts, success, status, message, error,
latencyMs, minMs, maxMs, jitterMs, stddevMs, lossPct, details, attempts, warnings}`.
`latencyMs` is the primary metric of the check type (average RTT for ping, total
request time for HTTP, connect time for TCP, resolve time for DNS) and is what the
charts plot. `minMs`, `maxMs`, `jitterMs` and `stddevMs` are filled in when a check
takes several samples in one run — today that means ping. `jitterMs` is the mean
difference between consecutive packets; `stddevMs` is the population standard
deviation of all the packets in the run, the figure `ping` prints beside min/avg/max.
`lossPct` is the percentage of packets that did not come back. `details` carries the
type-specific diagnostics: for ping, `packetsSent`, `packetsReceived` and `rtts` (the
per-packet round-trip times in milliseconds).

### Walking an SNMP device

- `POST /api/snmp/walk` body:
  `{ "host", "version", "port", "community", "user", "authProto", "authPass",
  "privProto", "privPass", "oid", "max", "checkId" }` →
  `{ "rows": [{ "oid", "type", "value", "name", "kind" }], "truncated": bool, "max": 500 }`.

  `oid` is the subtree to walk, default `1.3.6.1.2.1` (mib-2); walking from the root of
  the whole tree would drag in vendor subtrees thousands of rows deep. `max` is capped at
  500 and `truncated` says the ceiling was hit. The whole exchange is bounded at 15
  seconds. `checkId` names a saved check whose stored credentials fill in the blank ones,
  the same arrangement as `POST /api/checks/test`.

  `name` is a suggestion from a small table of standard-MIB OIDs (`Port 3 in`,
  `Uptime`…), empty for an OID GWatch does not recognise, which is most of a device's
  tree. `kind` is `"counter"` for the Counter32/Counter64 types and `"gauge"` otherwise,
  so a ticked row arrives in the editor set up the right way.

  **Administrator only, and refused to API keys of every scope.** The call takes a
  credential and an address of the caller's choosing, makes GWatch talk to whatever is
  there, and reports what came back — that is a probe, and it belongs to the person in
  front of the machine rather than to an integration.

### Matter devices and commissioning

- `POST /api/matter/nodes` body `{ "server", "host" }` → `{ "server", "serverInfo",
  "nodes": [{ "nodeId", "available", "isBridge", "vendor", "product", "label",
  "endpoints": [{ "id", "label", "deviceTypes", "clusters", "readings": [{ "kind",
  "name", "unit", "cluster", "attribute", "current" }] }] }], "standards": [...] }`.
  Asks the sidecar at `server` (or `host` on port 5580) for every commissioned node
  (`get_nodes`) and describes it from the sidecar's cache, with the standard readings
  each endpoint offers and their current values. 502 when the sidecar cannot be reached.
- `POST /api/matter/commission` body `{ "server", "host", "code", "networkOnly" }` →
  `{ "server", "node": {…as above} }`. Relays a pairing code — an 11- or 21-digit manual
  code (dashes and spaces are stripped) or `MT:` QR text — to `commission_with_code`.
  `networkOnly` defaults to `true` (multi-admin, or a device already on the network);
  `false` needs Bluetooth on the sidecar and is refused up front when it has none. Waits
  up to three minutes, and records a `config_changed` event either way.

  **Administrator only, and refused to API keys of every scope**, like the SNMP walk:
  both make GWatch connect to an address of the caller's choosing, and commissioning
  adds a device to a Matter controller. GWatch stores no Matter credential; the fabric
  stays in the sidecar's storage.

## Discovery (find devices on the network)

A discovery run pings a range of addresses, asks whatever answered for its name and
tries a short list of TCP ports on it, and suggests a node template per responder. Every
route here is **administrator-only and refused for API keys**, reads included: starting
one is a decision about the network, and a run's results are a map of it.

One run at a time per install. A second `POST /api/discovery` while one is going is
answered **409**; the registry is in memory, so a restart forgets the last run.

- `POST /api/discovery` body `{ "ranges": ["192.168.1.0/24", "192.168.1.10-50", "10.0.0.5"], "ports": [22,80,443] }`
  → **202** with the `DiscoveryJob` as it stands. Leaving `ports` out uses the default
  set — 22, 80, 443, 445, 3389, 8080, 8443, 9100, 32400 and 1883 (161/SNMP is not probed:
  it is UDP, where silence and a dropped packet are the same answer) — and sending it
  empty (`"ports": []`) probes nothing at all, describing each responder by its name and
  round trip alone. At most 24 ports. Ranges are IPv4 CIDR
  blocks, dashed ranges written out in full or abbreviated to a last octet, or single
  addresses; several may share a line, separated by commas or semicolons, and `#` starts
  a comment. Network and broadcast addresses are skipped. **400** for IPv6, for anything
  unparseable, and for more than 4096 addresses in one run — each with a message to show
  the person who typed it.
- `GET /api/discovery` → the most recent run, so a modal that was closed mid-sweep can
  reattach. **404** when nothing has been run yet.
- `GET /api/discovery/{id}` → that run. **404** once it has been superseded.
- `POST /api/discovery/{id}/cancel` → the run, now `"cancelled"`. Whatever had already
  answered is kept. Cancelling a finished run is not an error.
- `POST /api/discovery/{id}/add` body
  `{ "items": [{"ip": "192.168.1.20", "name": "NAS", "template": "home-server"}], "group": "Home", "groups": ["Home"] }`
  → **201** `{ "created": [Node...], "skipped": [{"ip": "...", "reason": "..."}] }`.
  `name` defaults to the responder's reverse-DNS name and then to its address; `template`
  defaults to the suggested one and then to `ping`. Nodes are built from
  `GET /api/templates` exactly as the node editor builds them, with `host` set to the
  address. An address already used as some node's `host` is skipped rather than
  duplicated, as is one the run never saw. `group` and `groups` are the same thing; the
  first entry of `groups` is used when `group` is absent.

`DiscoveryJob`:

```json
{
  "id": "6f1c…",
  "ranges": ["192.168.1.0/24"],
  "ports": [22, 80, 443],
  "state": "running | done | cancelled | failed",
  "total": 254,
  "scanned": 118,
  "responders": 9,
  "results": [
    {
      "ip": "192.168.1.20",
      "hostname": "nas.lan",
      "rttMs": 1.4,
      "openPorts": [22, 80],
      "template": "home-server",
      "note": "SSH and a web interface — looks like a server or NAS.",
      "mac": "00:11:32:aa:bb:cc",
      "vendor": "Synology",
      "noPing": false
    }
  ],
  "notes": [],
  "error": "",
  "startedAt": "...",
  "finishedAt": "..."
}
```

After the pings, the server reads its own IPv4 neighbour (ARP) table — `/proc/net/arp` on
Linux, `GetIpNetTable` on Windows; not yet on macOS. Every address in the swept ranges with
a resolved entry is a responder, so a device that answered ARP but not the ping is found
too, with `noPing: true` and `rttMs` 0 (its note warns that a ping check would read it as
down). `mac` is the address in that table, lower-case and colon-separated, and `vendor` the
maker of the network card from the IEEE registry embedded in GWatch, or
`"Private (randomised) address"` for a locally administered MAC such as a phone's; both are
absent for a device behind a router, which never has an entry of its own. The vendor also
sharpens the suggested template's note — and a web-only device whose maker makes printers,
cameras or smart-home kit is not suggested as a router. `notes` explain the results: today,
that GWatch runs in a container whose neighbour table shows no LAN devices (bridge
networking). IPv6 neighbours (NDP) are not read.

Progress is also pushed over `/api/stream` (see Server-sent events), and each run leaves
one entry of type `discovery` in the timeline — "Discovery scanned 254 addresses in
192.168.1.0/24: 17 responded" — as does each add.

### Bulk edit

`PATCH /api/nodes/bulk` applies one change to many nodes and checks in a single
transaction, with one engine reload and one entry in the event timeline. It is
admin-only; a read-write API key may call it, a read-only key gets 403.

```json
{
  "nodeIds":  [1, 2, 3],
  "checkIds": [17],
  "node":  { "addTags": ["critical"] },
  "check": { "intervalSeconds": 120 },
  "checkFilter": { "types": ["ping", "tcp"] }
}
```

**Semantics.** The `node` patch applies to every node in `nodeIds`. The `check`
patch applies to every check in `checkIds` **plus** every check of every node in
`nodeIds`. `checkFilter.types` narrows that whole set, listed check ids
included, so what a caller selects and what it changes are the same set. Both
patches are partial: only the fields actually present in the JSON are changed,
so `"enabled": false` and an absent `enabled` are different requests. An
unknown field anywhere in the body is a 400 naming it — a misspelled key means
the change would silently not happen.

**Node fields.** `groups` (replaces the whole list), `addGroups`,
`removeGroups`, `tags` (replaces), `addTags`, `removeTags`, `importance`,
`enabled`, `dependsOnNodeId` (a node id, or `null` to clear it), `linkStyle`
(an object as above to set the style of the node's link to its parent, or `null`
to reset it; the network map's side panel uses this). Group and tag
names are trimmed, folded case-insensitively onto the spelling already in use
and capped at 16 groups, exactly as `PUT /api/nodes/{id}` does; removals match
ignoring case. A dependency that would form a loop, or point a node at itself,
is refused.

**Check fields.** `intervalSeconds`, `timeoutSeconds`, `retries`,
`failureThreshold`, `enabled`, `alerts` and `config`.

`alerts` is the whole per-check `AlertOverride`: an object **replaces** it (so a
field the object leaves out goes back to following the global setting), and an
explicit `null` clears it.

`config` is a whitelist of keys whose meaning does not depend on the check type:
`latencyWarnMs`, `packetLossWarnPct`, `pingMethod`, `certWarnDays`, plus
`metricThresholds`, which is merged by metric key onto every `system` check in the
selection and skipped on every other type (see
[Hardware check metrics](#hardware-check-metrics)). Any other key is a 400 naming it
and listing the ones that would have worked.

**Check type changes are not supported.** A check's type decides what its
configuration means, so changing it in bulk would leave every check it touched
pointing at settings that mean nothing for the new type. Change a type one
check at a time, in the editor.

**Validation** is the same as the single-node path: the interval minimum from
Settings › General, and `checks.Validate` on the merged check. Nothing is
written unless everything validates, and the store writes it all in one
transaction — a row that has gone missing since the request was built fails the
whole batch.

**Errors.** 400 when nothing is selected, when nothing would change, when the
type filter leaves no checks, for an unknown field or config key, for a value
the single-node path would also refuse, and for a node or check id that no
longer exists.

**Response.**

```json
{ "nodes": 5, "checks": 14, "changes": ["interval → 120 s", "tags +critical"] }
```

`nodes` and `checks` are how many rows were written; `changes` is the same list
of phrases the audit entry is built from ("Bulk edit: interval → 120 s on 14
checks; tags +critical on 5 nodes").

## History (charts)

- `GET /api/history?checkId=ID&range=1h|24h|3d|7d|30d|1y` → `HistorySeries`.
  Optional `end=` (Unix seconds, Unix milliseconds or RFC 3339) ends the window at
  that moment instead of now — a timestacked chart reads yesterday's 24 hours with
  `range=24h&end=<now − 1 day>`. An `end` in the future is read as now; one that
  cannot be parsed is a 400. `/api/history/multi` and `/api/export/history.csv`
  take it too.
  Multiple: `GET /api/history?checkId=1&checkId=2&range=24h` → `[HistorySeries]` (always an array when more than one id, single object for one id... to keep it simple the UI should use `GET /api/history/multi?checkId=..&checkId=..&range=` → `[HistorySeries]`).
- `GET /api/history/multi?checkId=1&checkId=2&range=24h` → `[HistorySeries]`. With `auto=1` and no `checkId`, the service picks up to 4 important checks (critical/high nodes, ping and HTTP first).
- Point spacing by range: 1h/24h → raw results (or 5-minute rollups if raw is gone), 3d/7d → 5-minute rollups, 30d → hourly, 1y → daily.

### Named metrics

A check may measure things beyond the latency every check reports. Those go into
`Result.metrics`, a JSON object of name → number stored alongside the result. Two check
types write it:

- An **SNMP** check writes one entry per OID that produced a number, keyed by the OID's
  `name` and holding the value (gauge) or rate (counter) **after** `scale`.
- A **json** check with `jsonRecord: true` writes one entry, keyed by `jsonMetric`
  (default `"value"`), holding the number the `jsonPath` pointed at. A JSON number and
  a string holding one (`"42.5"`) both count; anything else — text, a boolean, an
  object — is not a metric and is kept only as text in `Result.details.jsonValue`,
  which is how a string is recorded. The recording sits beside `jsonExpected`, which
  keeps its meaning: with both set the check is down on a mismatch *and* the value is
  still stored. Its config fields:

  | field | meaning |
  | --- | --- |
  | `jsonRecord` | `true` to keep the value on every run (requires `jsonPath`) |
  | `jsonMetric` | the metric's name in results, charts and `metric=`; at most 64 characters, default `value` |
  | `jsonUnit` | shown beside the value, e.g. `°C`, `%`, `ms`; optional |
  | `jsonWarnAbove`, `jsonCritAbove`, `jsonWarnBelow`, `jsonCritBelow` | optional thresholds, compared **strictly** like an OID's: crossing a `warn` marks the check degraded, crossing a `crit` marks it down. Validation requires `jsonWarnAbove < jsonCritAbove` and `jsonWarnBelow > jsonCritBelow`. Text is never thresholded. |

  The result's message names the recording (`json "temp" = 42.5; recorded value = 42.5
  °C`) and, when a threshold is crossed, says which one.

- A **system** (hardware health) check writes one entry per reading the machine
  reported, keyed as described under [Hardware check metrics](#hardware-check-metrics):
  `cpu`, `memory`, `swap`, `load`, `disk:<mount>`, `inodes:<mount>`,
  `net:<iface>.rx`, `net:<iface>.tx`, `diskio:<dev>.read`, `diskio:<dev>.write`,
  `diskio:<dev>.busy`. Each also has its own verdict in
  `Result.details.metricResults`.

Every other check type leaves `metrics` absent.

- `GET /api/history?checkId=ID&range=…&metric=<name>` → `HistorySeries` for that metric.
  For a hardware check the four singleton keys are always accepted, and so is any
  key the check's **newest result** carried (`disk:/srv` once the machine has
  reported that filesystem); `metricUnit` is worked out from the key (`%`, `B/s`,
  or empty for load).
  The series carries `metric` and `metricUnit`, each point carries `value`, and `avgMs`,
  `minMs` and `maxMs` carry the same number so that a chart drawn from a series' latency
  fields plots a named metric unchanged. `GET /api/export/history.csv` takes `metric=`
  too.
- A name the check is not configured to measure is a `400`, rather than an empty series.
- **Limit:** a named-metric series is always read from raw results. The rollup tables
  have a column per built-in metric (latency, jitter, loss) and nowhere to put one a
  check invented, so `metric=` reaches back only as far as raw history is retained — 30
  days by default, whatever Settings › Retention says otherwise. Ranges beyond that
  return the part of the window raw results still cover. Availability, latency and the
  uptime bars for an SNMP or json check are rolled up normally; only the named readings
  are limited this way.

## Hardware health

- `GET /api/hosts` → `[HostSummary]`: this computer first, then every registered
  machine, then any machine known only from a scraped endpoint. Each carries its newest
  reading in `metrics`, a `status` and a `stale` flag. That status means only "are
  readings still arriving" — whether a figure is *too high* is a hardware check's
  business, because only a check knows the thresholds somebody chose.
- `GET /api/hosts/{key}` → one `HostSummary`. The key is `local`, `agent:<id>` or
  `url:<check id>`.
- `GET /api/hosts/{key}/history?range=1h|24h|7d|30d|1y` →
  `{ key, range, from, to, samples }`. A sample is the numeric series only
  (`cpuPct`, `memPct`, `swapPct`, `diskPct`, `loadPerCore`, `netRxBytesPerSec`,
  `netTxBytesPerSec`, `diskReadBytesPerSec`, `diskWriteBytesPerSec`), not the whole
  snapshot. Readings are averaged into at most 600 buckets; a metric no reading in a
  bucket carried stays absent rather than becoming zero.

### Hardware check metrics

A `system` check reads a whole machine in one run, but judges, records and reports
each reading as a metric of its own (#60).

**Keys.** `cpu`, `memory`, `swap` and `load` (load average per core) are the readings
a machine has one of. The rest are `family:instance`: `disk:/srv` and `inodes:/srv`
per filesystem; `net:eth0.rx` and `net:eth0.tx` per interface (bytes/s received and
sent); `diskio:sda.read`, `diskio:sda.write` (bytes/s) and `diskio:sda.busy` (%) per
block device. Units follow the family: `%` for everything but `load` (no unit) and
the throughput keys (`B/s`).

**Per-metric verdicts.** Every run puts one `MetricResult` per reading in
`Result.details.metricResults`:

```json
{ "key": "disk:/srv", "label": "Disk /srv", "value": 88, "unit": "%", "status": "degraded",
  "reason": "Disk /srv is 88%, at or above the 85% warning threshold" }
```

`status` is that metric's own verdict — `up`, `degraded` (at or past its warning
level) or `down` (at or past its critical level). The check's `status` is the worst
of them; `warnings` carries the reasons of the degraded ones and `error` those of the
critical ones, as before. A reading the machine did not report (no swap, no load on
Windows) is simply absent. `Result.metrics` holds the same values keyed the same way,
which is what `/api/history?metric=` serves. A stale reading (see `staleAfterSeconds`)
fails the whole check and carries no metric results.

**Thresholds.** `config.metricThresholds` is a list:

```json
"metricThresholds": [
  { "metric": "cpu",       "warn": 90 },
  { "metric": "memory",    "warn": 90, "crit": 97 },
  { "metric": "disk",      "warn": 85, "crit": 95 },
  { "metric": "disk:/srv", "warn": 95 },
  { "metric": "net:eth0.rx", "warn": 1000, "crit": 100, "below": true }
]
```

| field | meaning |
| --- | --- |
| `metric` | a family — `cpu`, `memory`, `swap`, `load`, `disk`, `inodes`, `net`, `diskio` — or an instance key. An instance entry replaces its family's for that instance alone; `net:eth0` and `diskio:sda` cover both directions of that interface or device, `net:eth0.rx` one of them. `cpu`, `memory`, `swap` and `load` take no instance. |
| `warn`, `crit` | the levels; a missing level is off. Crossing `warn` makes the metric (and so the check) degraded, crossing `crit` makes it down. On an "above" threshold a level of `0` is also off, so an older configuration that said "0 turns it off" still means that. |
| `below` | `true` for a reading where less is worse: the levels are then crossed at or *below* the number, and `crit` must be at or below `warn`. |

Validation: a percentage family's levels are 0–100, a rate's are non-negative,
`crit` is at or above `warn` (at or below with `below`), no two entries name the same
key, and the family must be one of the eight. A family that is listed with no levels,
or not listed at all, is still measured and charted; it just never alerts. A check
saved with an empty list gets `SystemDefaults` (cpu 90; memory 90/97; swap 50; disk
and inodes 85/95; load 2).

The flat fields that preceded the list — `cpuWarnPct`, `cpuCritPct`, `memWarnPct`,
`memCritPct`, `swapWarnPct`, `diskWarnPct`, `diskCritPct`, `loadWarnPerCore`,
`loadCritPerCore` — are **deprecated**. They are still read (with `0` meaning off, and
inodes following the disk pair when `diskCritPct` is set) when `metricThresholds` is
empty, and a check saved with them is converted to the list and stored without them.
`diskMounts` and `staleAfterSeconds` are unchanged.

**Incidents and state.** A metric crossing its warning or critical level writes a
`warning` event with `metric` set to its key (title `Disk /srv warning` or
`Disk /srv critical`, detail the reason); it coming back writes `warning_cleared`
with the same `metric`. Each metric's timeline is independent of the others'. A
metric the machine stops reporting has its open warning cleared. `CheckState.metricStatus`
is `{ "<key>": "degraded"|"down" }` for every metric currently outside its thresholds
(absent when all are within them). The check-level `down`/`recovered` events, the
down alert and its cooldown are unchanged; `warningActive` stays `false` on a check
judged per metric.

**Bulk edit.** `config.metricThresholds` in a `PATCH /api/nodes/bulk` check patch is
merged by key: each entry replaces the check's entry for that key and the rest of the
list is kept. It applies only to `system` checks; other checks in the selection are
left alone. The same validation applies.

Registering a machine mints a credential, so those routes sit with the other credential
routes — an administrator in the browser, never an API key:

- `GET /api/agents` → `[Agent]`. The token is never returned; only `prefix` is.
- `POST /api/agents` body `{ "name": "Living room NAS", "nodeId": null|id }` →
  `{ "token": "gwa_…", "agent": Agent }`. **The token is returned exactly once.** Only its
  sha256 digest is stored, so it cannot be shown again.
- `PUT /api/agents/{id}` body `{ "name": …, "nodeId": …, "enabled": bool }` → `Agent`.
- `DELETE /api/agents/{id}` → 204. Revokes the token and keeps the machine's past
  readings. `?purge=1` deletes the registration (token, how it was enrolled); the node, and the history its hardware check recorded, stay with the node.
- `POST /api/agents/{id}/token` → `{ "agent": Agent, "token": "gwa_…", "pending": "gwa_…"|"", "viewable": bool }`.
  Reads the agent's token back, so it can be compared with the one in the machine's
  `agent.json`. A token minted by this version is also kept sealed with the server's own key
  (`gwatch.key`), the way every other secret GWatch holds is; `pending` is the replacement
  waiting during a rotation. `viewable: false` means GWatch only has a digest (an agent
  registered before copies were kept, or restored from a backup — a backup never carries the
  readable copy), and a rotation gets a readable one. It is a `POST`, it writes to the audit
  trail, and like the rest of the credential routes it is for an administrator in the
  browser, never an API key. `Agent` carries `tokenViewable`, `rotationPending`,
  `rotationSince`, `collect`, `inventory` and `checkedInAt`; never a token.
- `POST /api/agents/{id}/rotate` body `{ "retireOld": bool }` → `{ "token": "gwa_…", "retiredOld": bool, "agent": Agent }`.
  Issues a replacement token. By default the old token keeps working until the new one is
  first used; an agent holding the old one is handed the new one when it next checks in
  (below), writes it to its file and switches over, and its first request with the new token
  retires the old (both are accepted until then, so there is no gap; an agent that never
  takes the new one up — its token comes from an environment variable or a flag — keeps
  using the old one until the rotation is cancelled or replaced). `retireOld: true` is for a token that may have leaked: the old token stops
  working at once, nothing is delivered over the network, and the new token has to be put on
  the machine by hand. `409` for a revoked agent.
- `POST /api/agents/{id}/rotate/cancel` → 204. Drops a replacement nobody has used.
- `PUT /api/agents/{id}/collect` body `CollectSelection` → `Agent`. Which readings the
  administrator wants from the machine: `{ "metrics": ["cpu","net"], "interfaces": ["eth0"],
  "disks": [], "mounts": [] }`. An empty list is "automatic" — every kind of reading, every
  disk and filesystem, and the interfaces that are not obviously virtual. The agent applies
  it the next time it checks in, always inside the limit set in its own configuration file:
  GWatch can narrow what a machine reports, never widen it. `400` for an unknown metric
  family (`cpu`, `memory`, `swap`, `load`, `disk`, `inodes`, `net`, `diskio`).
- `GET /api/agents/latest` → `{ "version", "url", "checkedAt", "error"? }`, the newest
  non-pre-release `gwatch-agent` release in the update repository, which the interface
  uses to mark machines running an older agent. It reads the agent's own releases
  (its own tags), not GWatch's; the answer is cached for six hours (ten minutes after a failed
  lookup, and a failed lookup never replaces a good answer). When `updates.checkAutomatically`
  is off, or the build has no updater, it returns an empty release
  and makes no network request. Administrator only, never an API key.

### Pairing codes

A pairing code is a short, human-readable stand-in for an agent token: eight
characters from a Crockford-style base32 alphabet with the confusable ones removed
(no `I`, `L`, `O`, `U`, `0` or `1`), shown as `XXXX-XXXX`. It exists so that enrolling
a machine means typing eight characters into an installer prompt rather than copying a
forty-character `gwa_…` token between computers. Thirty symbols over eight places is
about 39 bits, and a code lives for fifteen minutes, is good for exactly one machine,
and can be cancelled in the meantime.

- `GET /api/agents/pairings` → `[PairingCode]`, newest first. The code itself is never
  returned; the row carries `name`, `nodeId`, `createdAt`, `expiresAt`, and whichever
  of `redeemedAt`, `revokedAt`, `agentId` and `redeemedAddr` apply.
- `POST /api/agents/pairings` body `{ "name": "Living room NAS", "nodeId": null|id, "collect": CollectSelection }` →
  `{ "code": "XXXX-XXXX", "pairing": PairingCode }`. **The code is returned exactly
  once.** Only its sha256 digest is stored, exactly as an agent token's is.
- `GET /api/agents/pairings/{id}` → `{ "state", "pairing": PairingCode, "agent"?: Agent }`.
  How one code has got on: `state` is `pending`, `paired` (redeemed; `agent` is the
  machine it enrolled), `reporting` (that machine's first reading has arrived),
  `expired` or `cancelled`. The pairing dialog polls this to confirm the pairing as
  it happens. Neither the code nor the agent's token is ever in the answer.
- `DELETE /api/agents/pairings/{id}` → 204. Cancels an unused code. A code that was
  already redeemed is left as it is, so cancelling cannot rewrite an enrolment that
  already happened.

### Redeeming a pairing code

`POST /api/agents/pair` body
`{ "code": "XXXX-XXXX", "hostname": …, "os": …, "arch": …, "version": … }` →
`201 { "token": "gwa_…", "agent": Agent, "intervalSeconds": … }`.

This is the one route under `/api/` that takes no credential, and it cannot take one:
handing out a credential is what it is for. What stands in for one is the code — short
lived, single use and cancellable — plus the same per-IP failure budget a wrong password
or a wrong agent token is counted against, so guessing runs out of attempts long before
it runs out of codes. The code is compared in constant time, redemption is atomic (two
machines racing with one code enrol exactly one), and every attempt is written to the
audit trail.

An unknown code, a mistyped one, an expired one, a cancelled one and one that has
already been used all answer `401` with the same message. Telling them apart would make
the endpoint an oracle. Which one it actually was is recorded in the event log, where
only an administrator can read it. Codes are read case-insensitively and dashes,
underscores and whitespace are ignored wherever they land, so `a2b3 c4d5` and
`A2B3-C4D5` are the same code.

On the machine being enrolled this is `gwatch-agent pair --server URL --code XXXX-XXXX`,
or `gwatch-agent install --server URL --code XXXX-XXXX` to pair and install the service
in one go. The token that comes back is saved, with the server's address, in the agent's
configuration file, `agent.json`, with owner-only permissions: in `%ProgramData%\GWatch` on
Windows and `$XDG_DATA_HOME/gwatch` (or `~/.local/share/gwatch`) elsewhere —
`GWATCH_AGENT_CONFIG` or `--config` names the file and `GWATCH_AGENT_TOKEN_FILE` moves the
whole directory. `run` and `once` read it when `--token` is not given. The file is
created 0600 under a 0700 directory; on Windows the inherited ACL is replaced with
SYSTEM and the administrators group. (Earlier agents kept the token in `agent-token`
and the server in `agent-settings.json`; an agent that finds those moves them into
`agent.json` the first time it starts.)

### Submitting a reading

`POST /ingest/metrics` with `Authorization: Bearer gwa_…` and a `HostMetrics` body.
Answers `202` with `{ accepted, ts, intervalSeconds }`; `ts` is the time the reading was
filed at.

GWatch keeps the reading's own `ts` — when the machine took it — so readings an agent held
through an outage are filed when they were taken. A reading that arrives after a newer one
is added to the history at its own time without being judged as current, and one GWatch
already has a result for at about that time is ignored, so a replay does no harm. An
agent marks a reading it is delivering late with `X-GWatch-Held: 1`. A held reading with
no `ts`, one more than five minutes ahead of GWatch's clock, or one more than 48 hours old
is refused with `422` (the agent drops it rather than retrying). An unmarked reading is
taken to be live: if its `ts` is missing or implausible it is filed at the time it
arrived, so a machine with a wrong clock still reports.

This route is deliberately outside the authorization table above. It is reached with an
agent token and nothing else; the token names exactly one machine, the reading is filed
under that machine whatever the payload claims, and the route can do nothing further. An
agent therefore never holds a credential that could read or change anything in GWatch —
which is the point of installing one on a machine you would rather not hand over. An
agent token is refused by every `/api/…` route, and a wrong one counts against the same
per-IP failure budget as a wrong password.

### Asking for settings

`POST /ingest/checkin` with `Authorization: Bearer gwa_…` and an optional body
`{ "version": "0.7.0", "inventory": { "interfaces": [{ "name", "detail", "auto" }], "disks": […], "mounts": […] } }`.
Answers `200` with an `AgentSettings`:

```json
{ "checkInSeconds": 900,
  "collect": { "metrics": [], "interfaces": ["eth0"], "disks": [], "mounts": [] },
  "latestAgent": "0.7.1",
  "token": "gwa_…" }
```

That is the **whole** of what GWatch can tell an agent, and it is a closed set on purpose:
how long to wait before asking again, the readings the administrator chose, the newest
agent release as a hint, and — only while a rotation is waiting, and only to a caller that
holds the token being replaced — the replacement token. There is no field for a URL to
fetch, a command to run, a file to write or a version to install. The agent applies each
part on its own terms: the selection only ever narrows what its own file allows, the
interval is clamped to between one minute and a day, a token is taken up only if the
agent's file owns it, and `latestAgent` changes nothing (an agent still fetches and
verifies releases itself, from the release feed). The agent sends an inventory of what it
could report so that GWatch can offer checkboxes for a disk or interface it has not been
told about; only what the machine's own limit allows is listed. Like `/ingest/metrics` the
route is reached with an agent token and nothing else, shares its failure budget, and is
outside the authorization table. [`AGENT-DECISIONS.md`](AGENT-DECISIONS.md#12-an-agent-asks-gwatch-for-a-small-fixed-set-of-things)
says why this does not turn GWatch into a control channel.

Rates in a `HostMetrics` (`usagePct`, anything `…PerSec`) are derived by the reporting
machine from two of its own consecutive readings, never by GWatch subtracting across the
network: only the machine itself sees an unbroken counter series. They are omitted when
there is nothing to compare against, which is not the same as a rate of zero. A timestamp
more than five minutes ahead of GWatch, or more than 48 hours behind it, is replaced with
arrival time. See [`HARDWARE.md`](HARDWARE.md).

## Events / incidents

- `GET /api/events?limit=100&before=ID&nodeId=&checkId=&type=&q=&since=&until=` → `[Event]` newest first. A `type` filter also includes its counterpart (down+recovered, warning+warning_cleared, cert_warning+cert_warning_cleared, silenced+unsilenced, maintenance_began+maintenance_ended, alert_sent+alert_failed, rule_fired+rule_cleared) unless `exact=1`. `q` is a case-insensitive search over title, detail, node and check name; `since`/`until` accept RFC 3339, `2006-01-02T15:04` or `2006-01-02`.
- `POST /api/events/note` body `{ "nodeId": null|id, "text": "rebooted router" }` → Event (timeline annotation).

Every `Event` carries an optional `actor` naming who caused it — `"local"`, `"password"`,
`"pat (admin)"`, `"api key Home Assistant (read-write)"`, or `"from 198.51.100.5"` for a
rejected credential. It is absent on events the monitoring engine produces by itself
(check results, the scheduler, alerts). Sign-ins, sign-outs, failed sign-ins and every
account or API-key change are recorded with the `auth` event type.

An event about one of a check's metrics rather than the check as a whole — a hardware
check's `warning` / `warning_cleared` for `disk:/srv` — carries the metric's key in
`metric` (see [Hardware check metrics](#hardware-check-metrics)). It is absent on
every other event.

### Event types

`Event.type` is one of:

| type | recorded when |
| --- | --- |
| `down` | a check crossed its failure threshold |
| `recovered` | a check that was down answered again |
| `warning` | a check went degraded — a threshold crossed, high latency, packet loss. Carries `metric` when it is one reading rather than the whole check |
| `warning_cleared` | the degraded condition went away |
| `cert_warning` | a TLS certificate is expiring, or is not valid |
| `cert_warning_cleared` | the certificate is valid and no longer close to expiry |
| `content_changed` | a `contentWatch` saw the response differ from the previous observation |
| `alert_sent` | an alert email went out |
| `alert_suppressed` | an alert was not sent — cooldown, silencing, maintenance, or a parent node being down |
| `alert_failed` | an alert email could not be delivered |
| `silenced` / `unsilenced` | a check or node was silenced for a while, or released early |
| `maintenance_began` / `maintenance_ended` | a maintenance window opened or closed |
| `config_changed` | nodes, checks, settings, the database configuration or anything else in the configuration was changed |
| `affected_by_parent` | a check was not alerted on because the node it depends on is down |
| `service_started` / `service_stopped` | the monitoring service came up or shut down |
| `monitor_gap` | the monitoring computer was asleep or offline, so there is a hole in the history |
| `internal_error` | GWatch itself hit an error worth surfacing |
| `backup` / `restore` | a backup was written, or an archive restored |
| `retention` | a rollup + cleanup pass ran |
| `note` | a human annotation, from `POST /api/events/note` |
| `trigger_fired` | an automation trigger ran its action |
| `endpoint_called` | a custom inbound endpoint was invoked |
| `update` | an application update was checked for or applied — and the agent-skill download |
| `auth` | a sign-in, sign-out, failed sign-in, or an account or API-key change |
| `discovery` | a subnet was swept, or nodes were added from a sweep |
| `rule_fired` / `rule_cleared` | a notification rule's conditions came together, or came apart again |

## Maintenance windows

- `GET /api/maintenance` → `[MaintenanceWindow]` (each with extra `"active": bool`).
- `POST /api/maintenance` body MaintenanceWindow → created.
- `PUT /api/maintenance/{id}` → updated. `DELETE /api/maintenance/{id}` → `{ok:true}`.

Weekly maintenance windows accept `weekdays` (0 = Sunday), `startTime` (`HH:MM`),
`timeZone` (an explicit IANA name such as `America/New_York`) and
`durationMinutes`. The recurrence follows that zone's daylight saving rules,
regardless of the server or browser zone. Existing windows without these fields
retain server-local scheduling until edited; verify their day/time when updating.
Multi-day durations remain active across all covered dates.

Automatic triggers respect maintenance windows and silences. A `runNode` action
has a minimum cooldown of one minute, including older saved triggers with zero
cooldown, to stop self-triggering and mutually-triggering loops. Explicit manual
trigger runs remain available.

## Dashboards

- `GET /api/dashboards` → `[Dashboard]`. `GET /api/dashboards/{id}` → one `Dashboard`.
- `POST /api/dashboards` body `{name, widgets}` → Dashboard. `PUT /api/dashboards/{id}`, `DELETE /api/dashboards/{id}`.
- A default "Overview" dashboard is created on first run.

Each widget carries its grid position: `x` (0..3), `y` (row), `width` (1..4) and `height` (1..6). Widgets without `x`/`y` are placed automatically.

Widget types (`Widget.type`) and their `config`:
| type | config | description |
|---|---|---|
| `chart` | the Charts-tab config: `{ "checkIds": [], "metric": "avg|min|max|jitter|loss|availability", "range": "24h", "style": "line|area|step|bars|scatter", "smooth", "points", "lineWidth", "shadeFailures", "legend", "grid", "yMin", "yMax", "threshold", "split", "uptime", "colors": {checkId: "#hex"}, "timestack": null | { "period": "1h|24h|3d|7d|30d", "layers": 2–8 } }` | fully configurable chart; with `timestack` the first series is laid over itself, one layer per earlier period |
| `summary` | `{}` | overall health counts (up/degraded/down/unknown) |
| `groups` | `{ "groups": ["Home Network", ...] }` (empty = all) | group status cards |
| `status_list` | `{ "group": "", "tag": "", "nodeIds": [], "sort": "" }` | node/check status list, filtered (`group` matches any of a node's groups); `sort` is `""` (as the overview lists them), `status`, `name`, `name-desc`, `host` or `importance` |
| `network_map` | `{ "orientation": "down|right|radial", "labels": "name|name-host|none", "size": "small|medium|large", "edges": "curved|straight|elbow", "group": "", "tag": "", "onlyLinked": false, "colorEdges": true, "showLegend": true, "lineColor": "", "lineWidth": 1.5, "lineStyle": "solid|dashed|dotted|dash-dot" }` | the dependency map: every node under the node it depends on (`radial` is the clusters layout: the nodes that depend on a node branch off it in whatever direction has room, with longer lines to bigger branches). `lineColor` (`#rgb`, `#rrggbb` or `#rrggbbaa`; empty = the theme's), `lineWidth` (1–8) and `lineStyle` are how a line looks unless the node at its lower end has a `linkStyle` of its own; saving a dashboard with a colour or style that is not allowed is a 400, and a width out of range is clamped |
| `latency_chart` | `{ "checkIds": [1,2], "range": "24h", "metric": "avg" }` | line chart of latency/response time |
| `response_chart` | same as latency_chart (HTTP checks) | response time |
| `loss_chart` | `{ "checkIds": [], "range": "24h" }` | packet loss % |
| `uptime_chart` | `{ "checkIds": [], "range": "7d" }` | availability % per bucket |
| `incidents` | `{ "limit": 10 }` | recent incidents & recoveries |
| `cert_warnings` | `{}` | certificate expiry warnings |
| `attention` | `{}` | needs-attention list |
| `monitor_health` | `{}` | service health |
| `table` | `{ "group": "", "tag": "", "sort": "" }` | filtered node table (`sort` as for `status_list`) |

## Wallboards

A wallboard is a screen-sized view for a spare display. It is a separate object
from a dashboard with its own panel catalogue: a dashboard is read at a desk, a
wallboard from across a room.

- `GET /api/wallboards` → `[Wallboard]`. `GET /api/wallboards/{id}` → one.
- `POST /api/wallboards` body `{name, layout?, panels?}` → Wallboard. Created with
  no panels, it starts as the default arrangement.
- `PUT /api/wallboards/{id}` body `{name, sortOrder, layout, panels}` → updated.
  The share settings are **not** changed here.
- `DELETE /api/wallboards/{id}` → `{ok:true}`.
- `POST /api/wallboards/{id}/share` body `{ "enabled": true, "rotate": false }` →
  Wallboard. Switching projection on mints the board's address; `rotate` replaces
  it; switching off clears it. Administrator only, and never through an API key.
- `GET /api/wallboards/{id}/view?token=…` → the board and the data its panels
  draw, in one document. This is what a display polls. It is built on the server
  from the board's panels, and holds nothing they do not show:
  - `"wallboard"`: the Wallboard (share token redacted), `"generatedAt"`, `"serverNow"`.
  - `"summary"` (the overview's node counts): only with a `headline` or `counts`
    panel, or the footer (`hideChrome` false).
  - `"health"`: `{ serviceRunning, schedulerRunning, lastCheckAt, checksEnabled }`,
    not the Health document; only with a `health` panel or the footer.
  - `"nodes"`: `[{ "node": { id, name, group, groups, tags }, "status" }]`, only the
    nodes some `nodes` panel selects by its `group`, `tag` and `nodeIds`.
  - `"attention"`, `"groups"`: as in the overview, only with that panel.
  - `"certWarnings"`: `[{ nodeName, checkName, daysRemaining, notAfter }]`, and
    `"maintenance"`: `[{ name, group, nodeId }]`, only with that panel.
  - `"trends"`: `[HistorySeries...]` for the `trends` panels.

  A list no panel needs is `[]`; `summary` and `health` are left out. No check
  configuration, host, incident or Health detail is ever included.

`layout`: `{ "columns": 4..24, "theme": "signal|contrast|midnight|daylight",
"scale": 0.6..2, "refreshSeconds": 5..3600, "hideChrome": false }`. A panel's
`width` is counted in `columns`; `height` is 1..4 grid rows. Values outside these
ranges are clamped rather than refused.

Panel types (`WallPanel.type`) and their `config`:
| type | config | description |
|---|---|---|
| `headline` | `{}` | the one sentence that matters, with a lit status circle |
| `counts` | `{}` | up / degraded / down / unknown as large numerals |
| `clock` | `{ "seconds": false }` | time and date |
| `attention` | `{ "limit": 6 }` | what is down or degraded now, worst first |
| `groups` | `{}` | one tile per group, worst status wins |
| `nodes` | `{ "group": "", "tag": "", "nodeIds": [], "limit": 24 }` | a grid of nodes and their status (`group` matches any of a node's groups) |
| `trends` | `{ "range": "24h", "limit": 4, "checkIds": [] }` | charts; no `checkIds` means GWatch picks |
| `certs` | `{}` | certificates expiring soon or invalid |
| `maintenance` | `{}` | maintenance windows in force |
| `health` | `{}` | whether GWatch itself is running and checking |
| `message` | `{ "text": "" }` | a fixed line of text |

### Projecting a board

`GET /api/wallboards/{id}/view` is the only route that returns monitoring data
without a credential, and only for a board whose administrator switched
projection on, and only with that board's own token. The token is stored as it
is rather than hashed, because the address has to be readable again later — it
is typed into a display that has no keyboard. It is worth one read-only board
— exactly the data that board draws — and nothing else, it is shown only to an administrator (`share.token` comes back
`{"enabled":true,"redacted":true}` for a viewer, and is never included in the
view document), and switching projection off or rotating the token revokes it.
Every change is written to the audit trail.

The display itself is pointed at `/wall?id=<id>&token=<token>`, a page of its
own rather than the application: a screen with no keyboard must never be sent to
a sign-in form. It accepts `&mode=light` and `&accent=<hex>` as well.

## Saved charts

- `GET /api/charts` → `[SavedChart]` (`{ id, name, config, updatedAt }`, config as for the `chart` widget).
- `PUT /api/charts` body `[SavedChart]` → replaces the whole list.

## Automation

See [`docs/RECIPES.md`](RECIPES.md) for copy-pasteable trigger/endpoint recipes (Home Assistant, Discord/Slack, ntfy/Pushover, Docker restarts, git pulls, custom inbound hooks).

- `GET /api/automation/meta` → conditions, interpreters, default interpreter, placeholder names, `minTokenLength` and `tokenlessEndpoints` (`[{id, name, slug}]` — endpoints anyone who can reach the port may call).
- `GET /api/triggers?nodeId=` → `[Trigger]`. `POST /api/triggers`, `PUT /api/triggers/{id}`, `DELETE /api/triggers/{id}`.
  A trigger: `{ nodeId, name, description, enabled, on: ["down","recovered","degraded","warning_cleared","cert_warning","content_changed","affected_by_parent","status_change","any_failure","any_success","latency_over","metric_over"], checkId: null|id, latencyOverMs, metric, metricOver, cooldownMinutes, action }` plus run statistics (`lastRunAt`, `lastStatus`, `lastOutput`, `runCount`).
  `metric_over` fires on a run whose `Result.metrics[metric]` is above `metricOver` — a hardware check's `disk:/srv` or `cpu`, an SNMP check's OID name, a json check's recorded value — regardless of the check's own thresholds; saving it without `metric` is a 400. A hardware check's per-metric `warning` / `warning_cleared` events satisfy `degraded` / `warning_cleared` like any other.
- `POST /api/triggers/{id}/run` → `ActionResult` (runs it now with the node's current state).
- `POST /api/actions/test` body `{ "action": Action, "nodeId": null|id }` → `ActionResult` (nothing recorded).
- `GET /api/endpoints` → `[Endpoint]`. `POST /api/endpoints`, `PUT /api/endpoints/{id}`, `DELETE /api/endpoints/{id}`, `POST /api/endpoints/{id}/run`.
  An endpoint: `{ name, slug, description, enabled, method: "ANY|GET|POST|PUT|DELETE", token, allowNoToken, action }`.
  A token is **required**: saving with an empty `token` is rejected with 400 unless `allowNoToken` is `true`, the explicit acknowledgement that anyone who can reach the port may run the action. A token must be at least 8 characters, and supplying one forces `allowNoToken` back to `false`.
- `ANY /hook/{slug}` → runs the endpoint's action and answers `ActionResult` (200, or 502 when the action failed). The token is passed as `?token=`, `X-GWatch-Token` or `Authorization: Bearer`; a wrong token is 401, and 429 with `Retry-After` once the per-IP failure budget runs out. An endpoint with no stored token is refused with 401 unless `allowNoToken` is set. `/hook/` URLs are not covered by the LAN access password, so the token is their only protection. The request body and query parameters are available to the action as `{{body}}` and `{{query.<name>}}`.

An `Action` is `{ "type": "http|slack|teams|ntfy|pushover|git|script|run_node", "timeoutSeconds", ... }`:
| type | fields |
|---|---|
| `http` | `method` (auto: POST with body, else GET), `url`, `headers`, `body`, `expectedStatus` (default 200-399), `ignoreTlsErrors` |
| `slack` | `webhookUrl` (https://, a Slack incoming webhook), `message` |
| `teams` | `webhookUrl` (https://, a Teams/Power Automate webhook), `title`, `message` |
| `ntfy` | `server` (default `https://ntfy.sh`; http:// or https://), `topic`, `title`, `message`, `priority` (`min\|low\|default\|high\|urgent` or `1`-`5`), `tags`, `token` (optional bearer token) |
| `pushover` | `token` (application token), `userKey`, `title`, `message`, `priority` (`-2`..`2`) |
| `git` | `repo` (working directory), `gitArgs` (everything after `git`) |
| `script` | `interpreter` (`sh`, `bash`, `powershell`, `cmd`, `python`, `node`, `custom`), `command` (for custom; `{{file}}` is the script path), `code`, `workDir`, `allowUntrustedInput` |
| `run_node` | `nodeId` |

A URL is where a request — and for ntfy its bearer token — is sent, so the `url` of an `http` action, the `webhookUrl` of `slack` and `teams`, and the ntfy `server` must spell out their scheme and host (and port, and any user info): a placeholder there is refused with 400 when the action is saved. Placeholders may appear in the path, query and fragment, where each value is escaped as data (path-escaped in the path, query-escaped in the query) and so cannot add a segment, a parameter or another host. The ntfy `topic` is one escaped path segment under `server`. ntfy's `Authorization` header is dropped if the server redirects to another scheme, host or port.

`slack`, `teams`, `ntfy` and `pushover` all default `title` to `"GWatch {{instance}}"` and `message` to `"{{node.name}} is {{status}}: {{message}}"` when left blank; both fields are JSON-safe (or form/header-safe) no matter what characters `{{message}}` expands to, since the payload is built with `encoding/json` (or form-encoding for Pushover, headers for ntfy) instead of string concatenation.

String fields may contain `{{placeholders}}`: `node.name`, `node.host`, `node.group` (the node's first group), `node.groups` (all of them, comma-separated), `check.name`, `check.type`, `target`, `status`, `prev_status`, `message`, `error`, `success`, `latencyMs`, `lossPct`, `statusCode`, `failures`, `event`, `ts`, `instance`, `metric`, `metric.label`, `metric.value`, `metric.status` (the metric a metric-scoped event or a `metric_over` condition fired on; empty otherwise), `metrics.<key>` (every named metric of the run — a key's characters other than letters, digits, dots and dashes become `_`, so `disk:/srv` is `{{metrics.disk__srv}}`), `body`, `query.<name>`. Scripts also receive them as `GWATCH_*` environment variables (`node.name` → `GWATCH_NODE_NAME`).

A placeholder value can be anything an HTTP caller or a monitored device sent, so inside the **code of a script action** it is never spliced in as raw text. It is replaced by something the interpreter cannot re-parse as code:

| interpreter | `{{node.name}}` becomes |
|---|---|
| `sh`, `bash` | `"${GWATCH_NODE_NAME}"` — a double-quoted expansion, so metacharacters in the value are never re-parsed (write it unquoted; your own quotes around it only add word splitting) |
| `powershell` | `${env:GWATCH_NODE_NAME}` |
| `python` | a Python string literal |
| `node` | a JavaScript string literal |
| `cmd` | a sanitized literal: `cmd.exe` re-parses its own expansions, so `& \| < > ^ % ! "` are dropped from the value and newlines become spaces |

Unknown names become an empty literal. Elsewhere — URL, body, headers, git arguments, `repo`, `workDir` — placeholders expand to the plain value, because those are data rather than code; header values lose CR and LF, and git arguments are split **before** expansion so a value can never add an argument (a value that would turn a plain argument into an option is refused).

With `interpreter: "custom"` the language is whatever `command` runs, so GWatch has no quoting rule to apply: a `code` containing any `{{placeholder}}` is rejected with 400 unless `allowUntrustedInput` is `true`, which opts into raw expansion. Reading the `GWATCH_*` environment variables instead works in every interpreter and needs no acknowledgement.
`ActionResult` is `{ ok, output, error, statusCode, startedAt, durationMs }`.

## Rules

A **rule** is a notification that looks at several checks at once, where a trigger looks at one node: "two of my three DNS servers are down", "the gateway and the switch are both unreachable". Rules sit beside the per-node alerts, triggers and dependency-aware suppression and change none of them (Settings › Rules; recipe 9 in [`docs/RECIPES.md`](RECIPES.md)).

- `GET /api/rules` → `[Rule]`, each with its `state`. `GET /api/rules/{id}` → one. Viewers may read them in the browser; no API key may (they carry webhook URLs and tokens, like triggers).
- `POST /api/rules`, `PUT /api/rules/{id}`, `DELETE /api/rules/{id}` — administrators. Each is recorded in the audit timeline as `config_changed` ("Rule saved: …", "Rule deleted: …").
- `POST /api/rules/{id}/test` → `[ActionResult]`, one per action: runs the actions once with sample values (`event` = `test`), recording nothing.

A rule:

```json
{ "id": 3, "name": "Two of three DNS servers down", "enabled": true,
  "join": "at_least", "atLeast": 2,
  "conditions": [
    { "kind": "status", "checkId": 12, "status": "down" },
    { "kind": "status", "checkId": 27, "status": "down" },
    { "kind": "status", "nodeId": 4, "status": "degraded" } ],
  "actions": [ { "type": "pushover", "token": "…", "userKey": "…" } ],
  "cooldownMinutes": 30, "notifyCleared": true,
  "state": { "ruleId": 3, "met": false, "since": "2026-09-20T07:12:00Z", "lastFiredAt": "2026-09-19T22:40:11Z" },
  "createdAt": "…", "updatedAt": "…" }
```

| field | meaning |
|---|---|
| `join` | `all` (every condition), `any` (at least one) or `at_least` (at least `atLeast` of them; `1 ≤ atLeast ≤ len(conditions)`) |
| `conditions[].kind` | always `status` today; the field exists so another kind can be added later without reshaping stored rules |
| `conditions[].checkId` / `nodeId` | exactly one of the two. A check condition holds while that check is at the status; a node condition holds while **any** of the node's enabled checks is |
| `conditions[].status` | `down`, or `degraded` — which means **degraded or worse**, so a check that went from degraded to down still satisfies it |
| `actions` | one or more `Action`s (the table above), all run when the rule fires |
| `cooldownMinutes` | shortest time between two runs of the actions (0 = none) |
| `notifyCleared` | also run the actions, with `event` = `rule_cleared`, when the conditions come apart |
| `state` | read-only: `met`, `since` (when `met` last changed), `lastFiredAt` (when the actions last ran) |

Validation (400 with the reason): a name, at least one condition and one action, a known join, a count in range for `at_least`, every condition naming an existing node or check (and only one of the two), each action valid the way a trigger's is.

**Semantics.**
- The engine re-evaluates the rules that mention a check whenever that check's status changes, when a check is silenced or unsilenced, when a maintenance window opens or closes, and on every configuration reload. A rule saved while its conditions already hold fires right away; one that no longer holds after an edit clears. On start-up the stored state is trusted, so a restart neither fires a rule again nor forgets that it is waiting to clear.
- A rule fires **once** on the way from not-met to met: it runs its actions, records a `rule_fired` event (no node or check; the title is the rule's name and the detail says which conditions held and what ran) and stays met until the conditions come apart, when it records `rule_cleared` and, with `notifyCleared`, runs the actions again. Both events carry the same `Detail` sentence, e.g. `2 of 3 conditions met — Pi-hole › DNS is down, Router › DNS is down.`
- **Cooldown:** a firing inside `cooldownMinutes` of the last run still changes the state and records `rule_fired`, with the actions held back (the detail says so); the clearing that follows such a firing is quiet too, even with `notifyCleared`.
- **Maintenance and silence:** a check under an active maintenance window or silenced does not count towards any rule while that lasts, and counts again afterwards. A disabled check or node never counts. A disabled rule is put back to not-met without firing anything.
- Rules are exported and restored with the backup configuration (`rules` in `config.json`; an older archive without it restores as before). A restored rule starts not-met.

**Placeholders** for a rule's actions, beside the ones every action gets: `rule.id`, `rule.name`, `rule.join`, `rule.needed`, `rule.total`, `rule.met` (how many conditions hold), `rule.conditions` (`Pi-hole › DNS is down, Router › DNS is down`), `rule.summary` (`Rule 'Two of three DNS servers down' fired: 2 of 3 conditions met — …`), `message` (the sentence after the colon), `status` (`met` or `cleared`), `event` (`rule_fired`, `rule_cleared` or `test`), `ts`, `instance`. `node.name` carries the rule's name and `check.name`, `node.host`, `target` are empty, so the default Slack, Teams, ntfy and Pushover text ("{{node.name}} is {{status}}: {{message}}") reads as a sentence without a rule-specific template.

**Deliberately not included yet:** hold timers ("only when the conditions have held for N minutes") and metric conditions ("disk above 90 %"). The model is shaped for them — `kind` on a condition — but neither is built.

## Updates

- `GET /api/update/status` → `{ "status": UpdateStatus, "repo": "owner/name", "version": "..." }`. `UpdateStatus` carries `lastCheckAt`, `nextCheckAt` (null when automatic checks are off), `autoCheck` and `promptOnOpen` alongside the last `UpdateInfo`. It is the one update endpoint a viewer may read, so the interface can show that an update is waiting.
- `GET /api/update/releases` → `{ "releases": [Release], "repo": "owner/name", "version": "..." }`, newest first, up to 50 releases back. Each `Release` is `{version, tag, name, prerelease, notes, url, publishedAt, assetName, assetUrl, assetSize, installable, newer, running}`. Drafts are never listed; pre-releases are, flagged with `prerelease` so the caller decides whether to offer them. `installable` is false when the release carries no executable for this platform.
- `POST /api/update/check` → `UpdateInfo` for the newest release this build could move to (502 with `{error, info}` when GitHub cannot be reached or there is no release). Pre-releases are considered only when `updates.includePrerelease` is set. On a build with no release signing key pinned, the check still reports the release and `info.error` explains that it cannot be installed.
- `POST /api/update/apply` body `{ "version": "0.3.0" }` (optional) → downloads the platform asset (`gwatch-<os>-<arch>[.exe]`), **verifies its signature**, swaps the executable and restarts the service → `{ ok, info, restarting }`. With no body, or an empty `version`, it installs the newest release on offer; naming a version installs that one instead, which is how a user takes an update that is not the most recent, or a pre-release.

A named version is resolved against the release list fetched from GitHub, never turned into a download URL: what is downloaded is always an asset GitHub itself lists for that release. A version that was never published is refused (`no release X was published for owner/name`), as is one older than the running build (`GWatch does not install an earlier version over a later one` — the database has already been migrated by the running version).

Checking happens on its own as well as on request: once shortly after the service starts, then every `updates.checkIntervalHours`, while `updates.checkAutomatically` is on. Nothing else about the installation is sent — a check asks GitHub for the list of releases and nothing more.

A signature is mandatory. Each asset is published with a sibling `<asset>.sig` in the `gwatch-sig-v1` format — an ed25519 signature over the SHA-256 digest of the asset — and the downloaded file is only kept if that signature verifies against a public key pinned into the running binary (`internal/update/release_keys.txt`, or the `-X …/internal/update.releaseKeys=ed25519:…` build override). Otherwise the download is deleted and apply fails with:

| Situation | `error` |
|---|---|
| this build pins no key | `this build has no release signing key, updates are disabled (see docs)` |
| no `.sig` asset in the release | `the release is not signed` |
| signature is malformed or from another key | `signature verification failed` |

The `<asset>.sha256` sidecar is still checked when the release publishes one (`checksum mismatch` aborts the update), but it is only a transit-corruption guard and never substitutes for the signature. See [`RELEASING.md`](RELEASING.md).

## AI assistants (MCP)

The MCP companion itself ([`../mcp/README.md`](../mcp/README.md)) uses the endpoints above with an
API key. These two serve the **Settings › AI & MCP** page and are for the administrator setting an
assistant up, not for the assistant: both are admin-only and refuse every API key.

- `GET /api/mcp/status` → `{ "skillVersion": "1.0.0", "lastDownloadedVersion": "1.0.0", "lastDownloadedAt": "RFC3339 or null", "lastDownloadedBy": "pat (admin)", "updateAvailable": false }`. `skillVersion` is read from the embedded `skill/VERSION`. The `lastDownloaded*` fields describe the most recent download from this install (absent/null before the first one). `updateAvailable` is true only when a download has happened and the embedded skill's version differs from the one downloaded — never having downloaded it is not an update.
- `GET /api/mcp/skill` → a zip of the `skill/` folder as `gwatch-skill-<version>.zip` (`application/zip`, `Content-Disposition: attachment`), unpacking to `gwatch/SKILL.md`, `gwatch/README.md` and `gwatch/VERSION`. `?format=md` returns `SKILL.md` alone as `text/markdown`. Each successful download records `{version, at, by}` under the `mcpSkillDownload` setting and adds an `update` event ("Agent skill downloaded: <version>") attributed to the caller.

## Settings

- `GET /api/settings` → `Settings` (SMTP password, access password and the scheduled-backup password are returned masked as `"********"` when set).
- `PUT /api/settings` body `Settings` → saved Settings (a masked password keeps the stored one). `general.theme` is `dark|light|system`, `general.accentColor` a hex colour, `general.remoteAccess` rebinds the listener to all interfaces live, `general.accessPassword` is the legacy shared password for other devices (user accounts replace it), `general.requireLoginLocally` makes a browser on this computer sign in too — it is refused while no administrator account exists, and ignored until one does, `general.updateRepo` is the GitHub repository checked for releases, `general.pingMethod` is `auto|builtin|system` and is refused if it is anything else (an empty value is read as `auto`, which is what settings saved before it existed contain), `general.passwordHashing` is `argon2id|pbkdf2-sha256` and is refused if it is anything else (an empty value is read as `argon2id`). `backups` configures scheduled automatic backups (see below); it cannot be saved with `enabled: true` and no password. `updates` is `{checkAutomatically, checkIntervalHours, includePrerelease, promptOnOpen}`: `checkAutomatically` governs every check GWatch makes on its own (the periodic one and the one made when the interface is opened), `checkIntervalHours` must be 1-720, `includePrerelease` lets a pre-release be offered as an update, and `promptOnOpen` offers a waiting update in a dialog when the interface is opened. `indicators` configures the status circles in the header (see below).
- All three passwords are stored encrypted in the database with the local `gwatch.key` file; the API request and response bodies are unchanged.
- `POST /api/settings/test-email` body `{ "to": "optional@override" }` → `{ "ok": true, "message": "..." }` or error.
- `GET /api/retention/status` → `RetentionStatus`. `POST /api/retention/run` → runs rollup+cleanup now → RetentionStatus.

`Settings` is `{ general, alerts, retention, backups, updates, indicators }`.

**`general`**

| field | default | meaning |
| --- | --- | --- |
| `instanceName` | `GWatch` | the name shown in the header and in alert emails |
| `defaultIntervalSeconds` | `60` | the interval a new check is created with |
| `defaultTimeoutSeconds` | `10` | the per-attempt timeout a new check is created with |
| `maxConcurrentChecks` | `8` | how many checks the scheduler runs at once |
| `minIntervalSeconds` | `10` | the floor every check's interval is validated against (overload protection). Values under 5 are raised to 5 on save, and a check is refused below 10 seconds regardless |
| `wallboardRefreshSeconds` | `15` | how often a wallboard redraws |
| `latencyWarnMs` | `0` (off) | the global latency warning threshold a check inherits |
| `packetLossWarnPct` | `0` (off) | the global packet-loss warning threshold a check inherits |
| `pingMethod` | `auto` | `auto`, `builtin` (GWatch's own ICMP sender) or `system` (the platform `ping`). Anything else is a `400`; an empty value, which is what settings saved before this existed contain, reads as `auto` |
| `theme` | `dark` | `dark`, `light` or `system` |
| `accentColor` | `#43c9c0` | hex colour used for the accent |
| `remoteAccess` | `false` | rebinds the listener to every interface, live |
| `accessPassword` | — | the legacy shared password for other devices (user accounts replace it) |
| `updateRepo` | `jxburros/GWatch` | the GitHub `owner/repo` checked for releases |
| `requireLoginLocally` | `false` | makes a browser on this computer sign in too. Refused while no administrator account exists, and ignored until one does |
| `passwordHashing` | `argon2id` | how new account password hashes and new backup encryption keys are derived: `argon2id` (recommended) or `pbkdf2-sha256` (PBKDF2-HMAC-SHA256, 600,000 iterations, from the Go standard library only). Anything else is a `400`; an empty value reads as `argon2id`. Existing hashes of either kind keep verifying; each account is rehashed with the selected algorithm on its next successful sign-in, without ending its sessions. PBKDF2 is weaker than Argon2id against GPU guessing at equal cost — see [`DISCLAIMER.md`](DISCLAIMER.md#password-hashing-argon2id-or-pbkdf2) |

**`alerts`**

| field | default | meaning |
| --- | --- | --- |
| `enabled` | `false` | whether alert email is sent at all |
| `recipients` | `[]` | where alerts go |
| `failureThreshold` | `2` | consecutive failed runs before a check is alerted on (a check's own `failureThreshold` overrides it) |
| `cooldownMinutes` | `60` | the minimum gap between two alerts about the same check |
| `notifyRecovery` | `true` | also mail when a check recovers |
| `notifyWarnings` | `true` | also mail on a degraded condition |
| `certWarnDays` | `14` | how many days before expiry a certificate starts warning |
| `smtp` | port `587`, `starttls` | `{ host, port, username, password, from, security }`; `security` is `starttls`, `tls` or `none` |

**`retention`** — all in days; `0` means "keep forever".

| field | default | what it bounds |
| --- | --- | --- |
| `rawDays` | `30` | individual results (and so any named-metric series — see [Named metrics](#named-metrics)) |
| `fiveMinDays` | `180` | the 5-minute rollups |
| `hourlyDays` | `730` | the hourly rollups |
| `dailyDays` | `0` | the daily rollups |
| `eventDays` | `730` | the incident/event timeline |
| `hostDays` | `90` | the results of hardware checks, which are whole snapshots of a machine rather than one number (`rawDays` bounds every other check's) |

### Database

Which database GWatch keeps its data in ([`DATABASE.md`](DATABASE.md)). This is not part of
`Settings`: it has to be known before the database is open, so it lives in `database.json`
in the data directory and a change takes effect at the next start. The running process is
never switched over by these routes.

- `GET /api/database` → `{ "active": { "driver": "sqlite|postgres|mysql", "label", "description", "schemaVersion", "sizeBytes" }, "saved": DBConfig, "source": "default|file", "file": "<dataDir>/database.json", "restartRequired": bool }`. `active` is the database this process is using; `saved` is what the next start will use, with `password` (and `dsn`) masked as `"********"` when set; `restartRequired` is true when the two differ.
- `POST /api/database/test` body `DBConfig` → `{ "ok": true, "driver", "serverVersion", "database", "message" }`, or `400` for an incomplete config and `502` when the connection fails. Opens a connection with the given details and runs `SELECT version()`; writes nothing. A masked password means "the saved one".
- `PUT /api/database` body `DBConfig` → the same document as `GET`, after validating, testing the connection (a server that cannot be reached is not saved) and writing `database.json`. `driver: "sqlite"` removes the file instead. Recorded as a `config_changed` event.

`DBConfig` is `{ "driver", "host", "port", "user", "password", "database", "schema", "sslMode", "dsn" }` — the fields of `database.json`.

### Indicators

`settings.indicators` is a list of rules behind the status circles under the page
title in the web interface. Each rule is:

```json
{"id":"nodes-down","name":"Nodes down","enabled":true,"colour":"red",
 "condition":{"kind":"nodesInStatus","status":"down","minCount":1}}
```

- `colour` is `yellow`, `orange` or `red`. Green and blue are not rule colours:
  green is what the header shows when no rule fires, and blue when there is nothing
  to report on yet — no nodes at all, or no node that has produced a result.
- `condition.kind` is one of:
  - `nodesInStatus` — counts nodes in `condition.status`, which is `down`,
    `degraded`, `unknown` (no result yet) or `maintenance`. `up` is refused: an
    indicator that fires when all is well would be a second green.
  - `certWarnings` — counts certificates that are expiring or invalid.
  - `attention` — counts the checks that are down or degraded right now.
  - `serviceHealth` — fires when the monitor itself is unwell (stopped scheduler,
    retention error, failed backup, bounced alert email). It takes no count.
- `condition.minCount` is how many it takes to fire; it defaults to 1.

The vocabulary is closed and short because the rules are evaluated in the browser
against `GET /api/status`, so every condition has to be answerable from that one
summary. An unknown `kind`, an unknown `colour` or an unknown `status` is refused
with `400` and a message naming the rule. A missing `id` is assigned on save, a
duplicate id is replaced, and an empty list is replaced by the defaults — which is
also how a `PUT` asks for the defaults back. The defaults are: red for any node
down and for the monitor being unwell, orange for any node degraded and for a
certificate expiring, yellow for any node still waiting for a first result and for
any node in maintenance.

The effective list is served to every account through `GET /api/me`, because a
viewer may not read settings but sees the same header.

## Backups

Manual, one-click backups always work regardless of the settings below. Scheduled,
unattended backups are configured through `Settings.backups`
(`{ enabled, intervalHours, keep, includeHistory, password }`, saved via
`PUT /api/settings`): `intervalHours` (1-720) is how often a backup is made,
`keep` (1-365) is how many archives are retained (older ones are deleted
automatically after each scheduled run), and `password` is required to enable it —
backups are always encrypted. The password is masked in `GET /api/settings` and
stripped from `GET /api/export/config.json` like the SMTP password.

- `GET /api/backups` → `{ "backups": [BackupInfo], "dir": "...", "status": BackupStatus, "nextScheduledAt": "RFC3339 or null" }`. `nextScheduledAt` is `null` when scheduled backups are disabled or not configured with a password.
- `POST /api/backups` body `{ "password": "...", "includeHistory": true }` → BackupInfo. (Manual backups are never pruned.)
- `GET /api/backups/{fileName}/download` → file (`application/octet-stream`).
- `DELETE /api/backups/{fileName}` → `{ok:true}`.
- `POST /api/backups/restore` multipart form: `file`, `password`, `includeHistory` ("true"/"false"), `restoreAccounts` ("true"/"false", default false: accounts and agents no longer on this server are left out and listed) → `{ "ok": true, "nodes": n, "checks": n, "results": n, "skippedUsers": [...], "skippedAgents": [...], "safetyBackup": "file name" }`. A full backup of the current data is taken first under the same password; if it cannot be, nothing is restored. A body over 2 GiB (plus 1 MiB for the form) is refused with 413, as is an archive over any of the limits in [`RESTORE.md`](RESTORE.md#size-limits); either way nothing is changed.
- `POST /api/backups/restore-existing` body `{ "fileName": "...", "password": "...", "includeHistory": bool, "restoreAccounts": bool }` → same.

See [`RESTORE.md`](RESTORE.md) for the end-to-end restore-to-a-new-machine procedure.

## Export

- `GET /api/export/history.csv?checkId=ID&range=30d` → CSV: `timestamp,avg_ms,min_ms,max_ms,jitter_ms,loss_pct,availability_pct,count,failures`.
- `GET /api/export/results.csv?checkId=ID&limit=5000` → raw results CSV: `timestamp,success,status,message,error,latency_ms,min_ms,max_ms,jitter_ms,stddev_ms,loss_pct,http_status,final_url,attempts`.
- `GET /api/export/events.csv?nodeId=&limit=5000&type=&q=&since=&until=` → events CSV (same filters as `/api/events`).
- Every CSV is UTF-8 and starts with a byte-order mark, so Excel shows accents and dashes as written; strip the leading `\uFEFF` if a tool of yours does not expect it.
- In the CSV exports, a text cell (names, titles, notes, messages, errors, URLs) that
  begins with `=`, `+`, `-`, `@`, a tab or a carriage return is written with a leading
  `'`, so a spreadsheet shows it as text rather than evaluating it as a formula. Numeric
  columns are written as they are. Use `/api/events` or `/api/checks/{id}/results` for
  the raw values.
- `GET /api/export/logs.txt?limit=1000` → the recent service log as text.
- `GET /api/export/config.json` → settings, nodes and checks, dashboards, maintenance
  windows, triggers, rules, endpoints, saved charts, wallboards and report definitions as
  JSON, meant to be readable and shareable. Secrets come out empty: the access, SMTP and
  backup passwords; every check credential (metrics token, SNMP community and passphrases,
  request body, header and environment values); every action credential (tokens, user
  keys, webhook and request URLs, bodies, headers); endpoint tokens and wallboard share
  tokens. Accounts, API keys, agents and incidents are left out, and reports lose their
  recipients and are switched off. Administrator only, never an API key. It is not a
  backup: use an encrypted backup for a complete recoverable copy.

## Logs

- `GET /api/logs?limit=200` → `{ "lines": ["...", ...], "file": "path" }`.
- `GET /api/version` → `{ "version": "...", "platform": "windows/amd64", "apiVersion": 1 }` (see Versioning). `platform` is included only for an authenticated caller; an anonymous one gets `version` and `apiVersion` alone.

## Server-sent events

- `GET /api/stream` (text/event-stream) emits `event: update` with `data: {"kind":"result"|"state"|"event"|"config"|"health"|"host"|"maintenance"|"trigger"|"endpoint"|"discovery"|"rule","checkId":..,"nodeId":..}` whenever something changes. The UI uses it to refresh without polling; falling back to polling every 15s is fine. A `"host"` update means a new hardware reading arrived, and a `"rule"` update that a notification rule changed or fired; neither carries a `checkId` or `nodeId`.
- A `"discovery"` update carries the sweep's counters instead of a check or node, a few times a second while one is running and once more when it stops: `{"kind":"discovery","discovery":{"id":"6f1c…","state":"running","scanned":118,"total":254,"responders":9}}`. The results themselves are read from `GET /api/discovery/{id}`.


## Incidents and availability reports (0.6)

- `GET /api/incidents?state=active|all|open|acknowledged|resolved` returns grouped node
  incidents (default active). `GET /api/incidents/{id}` returns `{incident, events}`.
- `POST /api/incidents/{id}/acknowledge`, `/resolve`, or `/note` takes `{ "note": "..." }`.
  Admins and read-write keys may update; viewers/read keys may read. Notes are limited
  to 8,000 bytes. Incidents expose `openedAt`, acknowledgment/resolution times and actors,
  `checkIds`, notes, `durationSeconds`, and acknowledgment/resolution elapsed seconds.
- `GET /api/reports` and `PUT /api/reports` are administrator-only. PUT replaces the list
  of definitions: `id` (unique string), `name`, `enabled`, `groups`, `tags`, `period`
  (`weekly` or `monthly`), `recipients`, `targetAvailability` (0 disables the target),
  and `includeLatencyCharts`.
  Calendar periods follow the service time zone. Schedules begin with the next complete
  period after creation and retry failed delivery no more often than hourly.
- `GET /api/reports/generate?from=<RFC3339>&to=<RFC3339>&groups=<comma-separated>&tags=<comma-separated>&target=99.9`
  downloads self-contained printable HTML for up to 366 days. Viewers/read keys may
  generate a report. Add `charts=1` for group latency charts. Node availability
  aggregates observed samples across its checks.
- `GET /api/agents/latest` returns an empty release without network access when
  automatic update checks are disabled. When enabled it caches the agent release lookup.

Check secrets have one shared model definition for masking and encryption. Headers and
custom environment values are masked by key; returning `********` preserves a stored
value. Blank map values explicitly clear that value. Scalar credentials retain the
existing blank-means-keep convention. Action credentials are hidden from viewers.
Shareable config exports omit passwords, accounts, key/agent records, tokens, check and
action credentials, incident history and report recipient lists. Use an encrypted backup
for a complete recoverable copy.

Explicit `checkIds`, including `[]`, are authoritative for bulk check edits. `nodeIds`
expand to all checks only when `checkIds` is absent. This prevents unticked checks from
being silently changed while node-level settings are edited.

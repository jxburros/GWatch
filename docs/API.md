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
`{"version": "0.4.1", "platform": "windows/amd64", "apiVersion": 1}` (`platform` is
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
  automation metadata, retention status and update status.
- `readwrite` — the same, plus creating, updating, deleting, enabling, running and
  silencing nodes and checks, notes, maintenance windows, dashboards, wallboards and
  charts. A key may lay a wallboard out; it may not project one.

**Denied to every key, whatever its scope** (403, not 401):
`GET|PUT /api/settings`, `POST /api/settings/test-email`, `GET /api/network`,
everything under `/api/backups`, `GET /api/export/config.json`, `GET /api/logs`,
`GET /api/export/logs.txt`, everything under `/api/triggers` (including `GET`),
everything under `/api/endpoints` (including `GET`), `POST /api/actions/test`,
`GET /api/automation/meta`, `GET /api/retention/status`, `POST /api/retention/run`,
`GET|POST /api/update/…`, everything under `/api/users` and `/api/apikeys`, and
`POST /api/auth/change-password`.

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

**Rate limits.** Failed credentials (sign-in, API key, access password, agent token and
custom-endpoint token) are limited to 10 per minute per client IP; API-key requests from
off this machine are limited to 300 per minute per client IP. Both answer `429` with
`Retry-After` in seconds. A `/hook/` call that presents the right token gives its budget
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
`POST /api/auth/logout`, the static UI, and `/hook/…`, which is guarded by each
endpoint's own token rather than by the sign-in.

Anything not listed in the policy table is admin-only with API keys denied, so a new
route is closed until it is opened on purpose.

### Sign-in and accounts

- `GET /api/auth/setup` → `{ "usersConfigured": bool, "loginRequired": bool, "accessPasswordSet": bool, "localLoginForced": bool, "apiVersion": 1 }`. `loginRequired` is about *this* client.
- `POST /api/auth/login` body `{ "username", "password" }` → the principal, and sets `gwatch_session` (HttpOnly, SameSite=Lax, 30-day sliding expiry, `Secure` only over HTTPS). Rate limited.
- `POST /api/auth/logout` → `{ok:true}` and clears the cookie.
- `POST /api/auth/change-password` body `{ "current", "new" }` → changes your own password and signs every browser of that account out. Any signed-in user; never an API key.
- `GET /api/users` → `[User]`. `POST /api/users` body `{ "username", "password", "role": "admin"|"viewer" }` → `User` (201). The first account created is always an administrator, and on an install with no accounts a `local` or `password` principal may create it.
- `PUT /api/users/{id}` body `{ "role"?, "password"? }` → `User`. Changing either ends that account's sessions.
- `DELETE /api/users/{id}` → `{ok:true}`. Deleting or demoting the last administrator is refused with 400.
- `GET /api/apikeys` → `[APIKey]` (revoked keys included, so the trail keeps their names).
- `POST /api/apikeys` body `{ "name", "scope": "read"|"readwrite" }` → `{ "key": "gw_…", "apiKey": APIKey }` (201). An omitted scope means `read`. **The key is returned once and cannot be shown again.**
- `DELETE /api/apikeys/{id}` → `{ok:true}`, revoking it immediately.

Reaching GWatch from outside your own network is covered in
[`REMOTE-ACCESS.md`](REMOTE-ACCESS.md). `gwatch-mcp`, the optional Model Context Protocol
companion that lets an AI assistant use this API with a key, is documented in
[`../mcp/README.md`](../mcp/README.md).

## Health & overview

- `GET /api/health` → `model.Health` (service mode, scheduler, last/next check, db size, retention status, backup status, recent internal errors, alert config state). Also carries the on-disk paths: `dataDir` (the data directory), `databasePath` (`<dataDir>/gwatch.db`), `keyPath` (`<dataDir>/gwatch.key`) and `backupDir` (`<dataDir>/backups`) — see [`INSTALL.md`](INSTALL.md#where-your-data-lives).
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
- `POST /api/nodes/{id}/run` → runs all enabled checks of the node now → `[Result]`.
- `POST /api/nodes/{id}/silence` body `{ "minutes": 60 }` (0 = unsilence) → silences every check of the node → Node.
- `PATCH /api/nodes/bulk` — one change across many nodes and checks. See **Bulk edit** below.
- `GET /api/templates` → `[NodeTemplate]` (website, home-server, router — shown as "Network device", ping, api, tcp-service, this-computer, agent-machine, dns).
- `GET /api/groups` → `{ "groups": [{"name":"...","count":3}], "tags": [{"name":"...","count":2}] }`. A node in several groups is counted once in each of them, so the counts can add up to more than the number of nodes.

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
- `POST /api/checks/{id}/run` → `Result` (recorded and processed through alerting).
- `POST /api/checks/{id}/enable` body `{ "enabled": bool }` → Check.
- `POST /api/checks/{id}/silence` body `{ "minutes": 60 }` (0 = unsilence) → CheckState.
- `GET /api/checks/{id}/results?limit=50` → `[Result]` newest first.
- `GET /api/checks/{id}/state` → CheckState.

A ping check's config is `pingCount` (packets per run, default 4, maximum 20) and
`pingMethod` (`""` to follow `general.pingMethod`, or `auto`/`builtin`/`system` to
override it), plus the usual `target`, `latencyWarnMs` and `packetLossWarnPct`.

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
      "note": "SSH and a web interface — looks like a server or NAS."
    }
  ],
  "error": "",
  "startedAt": "...",
  "finishedAt": "..."
}
```

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
`enabled`, `dependsOnNodeId` (a node id, or `null` to clear it). Group and tag
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
`latencyWarnMs`, `packetLossWarnPct`, `pingMethod`, `certWarnDays`. Any other
key is a 400 naming it and listing the ones that would have worked.

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

- `GET /api/history?checkId=ID&range=1h|24h|7d|30d|1y` → `HistorySeries`.
  Multiple: `GET /api/history?checkId=1&checkId=2&range=24h` → `[HistorySeries]` (always an array when more than one id, single object for one id... to keep it simple the UI should use `GET /api/history/multi?checkId=..&checkId=..&range=` → `[HistorySeries]`).
- `GET /api/history/multi?checkId=1&checkId=2&range=24h` → `[HistorySeries]`. With `auto=1` and no `checkId`, the service picks up to 4 important checks (critical/high nodes, ping and HTTP first).
- Point spacing by range: 1h/24h → raw results (or 5-minute rollups if raw is gone), 7d → 5-minute rollups, 30d → hourly, 1y → daily.

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

Every other check type leaves `metrics` absent.

- `GET /api/history?checkId=ID&range=…&metric=<name>` → `HistorySeries` for that metric.
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

Registering a machine mints a credential, so those routes sit with the other credential
routes — an administrator in the browser, never an API key:

- `GET /api/agents` → `[Agent]`. The token is never returned; only `prefix` is.
- `POST /api/agents` body `{ "name": "Living room NAS", "nodeId": null|id }` →
  `{ "token": "gwa_…", "agent": Agent }`. **The token is returned exactly once.** Only its
  sha256 digest is stored, so it cannot be shown again.
- `PUT /api/agents/{id}` body `{ "name": …, "nodeId": …, "enabled": bool }` → `Agent`.
- `DELETE /api/agents/{id}` → 204. Revokes the token and keeps the machine's past
  readings. `?purge=1` deletes the machine and every reading it ever sent.

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
- `POST /api/agents/pairings` body `{ "name": "Living room NAS", "nodeId": null|id }` →
  `{ "code": "XXXX-XXXX", "pairing": PairingCode }`. **The code is returned exactly
  once.** Only its sha256 digest is stored, exactly as an agent token's is.
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
in one go. The token that comes back is saved with owner-only permissions to
`%ProgramData%\GWatch\agent-token` on Windows and `$XDG_DATA_HOME/gwatch/agent-token`
(or `~/.local/share/gwatch/agent-token`) elsewhere — `GWATCH_AGENT_TOKEN_FILE` overrides
it — and `run`, `once` and `serve` use it when `--token` is not given. The file is
created 0600 under a 0700 directory; on Windows the inherited ACL is replaced with
SYSTEM and the administrators group.

### Submitting a reading

`POST /ingest/metrics` with `Authorization: Bearer gwa_…` and a `HostMetrics` body.
Answers `202` with `{ accepted, ts, intervalSeconds }`.

This route is deliberately outside the authorization table above. It is reached with an
agent token and nothing else; the token names exactly one machine, the reading is filed
under that machine whatever the payload claims, and the route can do nothing further. An
agent therefore never holds a credential that could read or change anything in GWatch —
which is the point of installing one on a machine you would rather not hand over. An
agent token is refused by every `/api/…` route, and a wrong one counts against the same
per-IP failure budget as a wrong password.

Rates in a `HostMetrics` (`usagePct`, anything `…PerSec`) are derived by the reporting
machine from two of its own consecutive readings, never by GWatch subtracting across the
network: only the machine itself sees an unbroken counter series. They are omitted when
there is nothing to compare against, which is not the same as a rate of zero. A timestamp
more than five minutes ahead of GWatch, or more than 48 hours behind it, is replaced with
arrival time. See [`HARDWARE.md`](HARDWARE.md).

## Events / incidents

- `GET /api/events?limit=100&before=ID&nodeId=&checkId=&type=&q=&since=&until=` → `[Event]` newest first. A `type` filter also includes its counterpart (down+recovered, warning+warning_cleared, cert_warning+cert_warning_cleared, silenced+unsilenced, maintenance_began+maintenance_ended, alert_sent+alert_failed) unless `exact=1`. `q` is a case-insensitive search over title, detail, node and check name; `since`/`until` accept RFC 3339, `2006-01-02T15:04` or `2006-01-02`.
- `POST /api/events/note` body `{ "nodeId": null|id, "text": "rebooted router" }` → Event (timeline annotation).

Every `Event` carries an optional `actor` naming who caused it — `"local"`, `"password"`,
`"pat (admin)"`, `"api key Home Assistant (read-write)"`, or `"from 198.51.100.5"` for a
rejected credential. It is absent on events the monitoring engine produces by itself
(check results, the scheduler, alerts). Sign-ins, sign-outs, failed sign-ins and every
account or API-key change are recorded with the `auth` event type.

## Maintenance windows

- `GET /api/maintenance` → `[MaintenanceWindow]` (each with extra `"active": bool`).
- `POST /api/maintenance` body MaintenanceWindow → created.
- `PUT /api/maintenance/{id}` → updated. `DELETE /api/maintenance/{id}` → `{ok:true}`.

## Dashboards

- `GET /api/dashboards` → `[Dashboard]`.
- `POST /api/dashboards` body `{name, widgets}` → Dashboard. `PUT /api/dashboards/{id}`, `DELETE /api/dashboards/{id}`.
- A default "Overview" dashboard is created on first run.

Each widget carries its grid position: `x` (0..3), `y` (row), `width` (1..4) and `height` (1..6). Widgets without `x`/`y` are placed automatically.

Widget types (`Widget.type`) and their `config`:
| type | config | description |
|---|---|---|
| `chart` | the Charts-tab config: `{ "checkIds": [], "metric": "avg|min|max|jitter|loss|availability", "range": "24h", "style": "line|area|step|bars|scatter", "smooth", "points", "lineWidth", "shadeFailures", "legend", "grid", "yMin", "yMax", "threshold", "split", "uptime", "colors": {checkId: "#hex"} }` | fully configurable chart |
| `summary` | `{}` | overall health counts (up/degraded/down/unknown) |
| `groups` | `{ "groups": ["Home Network", ...] }` (empty = all) | group status cards |
| `status_list` | `{ "group": "", "tag": "", "nodeIds": [] }` | node/check status list, filtered (`group` matches any of a node's groups) |
| `latency_chart` | `{ "checkIds": [1,2], "range": "24h", "metric": "avg" }` | line chart of latency/response time |
| `response_chart` | same as latency_chart (HTTP checks) | response time |
| `loss_chart` | `{ "checkIds": [], "range": "24h" }` | packet loss % |
| `uptime_chart` | `{ "checkIds": [], "range": "7d" }` | availability % per bucket |
| `incidents` | `{ "limit": 10 }` | recent incidents & recoveries |
| `cert_warnings` | `{}` | certificate expiry warnings |
| `attention` | `{}` | needs-attention list |
| `monitor_health` | `{}` | service health |
| `table` | `{ "group": "", "tag": "" }` | filtered node table |

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
- `GET /api/wallboards/{id}/view?token=…` → the board and its data in one
  document: the overview fields, plus `"wallboard": Wallboard`, `"health": Health`,
  `"trends": [HistorySeries...]` and `"serverNow"`. This is what a display polls.

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
and nothing else, it is shown only to an administrator (`share.token` comes back
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
  A trigger: `{ nodeId, name, description, enabled, on: ["down","recovered","degraded","warning_cleared","cert_warning","content_changed","affected_by_parent","status_change","any_failure","any_success","latency_over"], checkId: null|id, latencyOverMs, cooldownMinutes, action }` plus run statistics (`lastRunAt`, `lastStatus`, `lastOutput`, `runCount`).
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
| `ntfy` | `server` (default `https://ntfy.sh`), `topic`, `title`, `message`, `priority` (`min\|low\|default\|high\|urgent` or `1`-`5`), `tags`, `token` (optional bearer token) |
| `pushover` | `token` (application token), `userKey`, `title`, `message`, `priority` (`-2`..`2`) |
| `git` | `repo` (working directory), `gitArgs` (everything after `git`) |
| `script` | `interpreter` (`sh`, `bash`, `powershell`, `cmd`, `python`, `node`, `custom`), `command` (for custom; `{{file}}` is the script path), `code`, `workDir`, `allowUntrustedInput` |
| `run_node` | `nodeId` |

`slack`, `teams`, `ntfy` and `pushover` all default `title` to `"GWatch {{instance}}"` and `message` to `"{{node.name}} is {{status}}: {{message}}"` when left blank; both fields are JSON-safe (or form/header-safe) no matter what characters `{{message}}` expands to, since the payload is built with `encoding/json` (or form-encoding for Pushover, headers for ntfy) instead of string concatenation.

String fields may contain `{{placeholders}}`: `node.name`, `node.host`, `node.group` (the node's first group), `node.groups` (all of them, comma-separated), `check.name`, `check.type`, `target`, `status`, `prev_status`, `message`, `error`, `success`, `latencyMs`, `lossPct`, `statusCode`, `failures`, `event`, `ts`, `instance`, `body`, `query.<name>`. Scripts also receive them as `GWATCH_*` environment variables (`node.name` → `GWATCH_NODE_NAME`).

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

## Settings

- `GET /api/settings` → `Settings` (SMTP password, access password and the scheduled-backup password are returned masked as `"********"` when set).
- `PUT /api/settings` body `Settings` → saved Settings (a masked password keeps the stored one). `general.theme` is `dark|light|system`, `general.accentColor` a hex colour, `general.remoteAccess` rebinds the listener to all interfaces live, `general.accessPassword` is the legacy shared password for other devices (user accounts replace it), `general.requireLoginLocally` makes a browser on this computer sign in too — it is refused while no administrator account exists, and ignored until one does, `general.updateRepo` is the GitHub repository checked for releases, `general.pingMethod` is `auto|builtin|system` and is refused if it is anything else (an empty value is read as `auto`, which is what settings saved before it existed contain). `backups` configures scheduled automatic backups (see below); it cannot be saved with `enabled: true` and no password. `updates` is `{checkAutomatically, checkIntervalHours, includePrerelease, promptOnOpen}`: `checkAutomatically` governs every check GWatch makes on its own (the periodic one and the one made when the interface is opened), `checkIntervalHours` must be 1-720, `includePrerelease` lets a pre-release be offered as an update, and `promptOnOpen` offers a waiting update in a dialog when the interface is opened. `indicators` configures the status circles in the header (see below).
- All three passwords are stored encrypted in the database with the local `gwatch.key` file; the API request and response bodies are unchanged.
- `POST /api/settings/test-email` body `{ "to": "optional@override" }` → `{ "ok": true, "message": "..." }` or error.
- `GET /api/retention/status` → `RetentionStatus`. `POST /api/retention/run` → runs rollup+cleanup now → RetentionStatus.

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
- `POST /api/backups/restore` multipart form: `file`, `password`, `includeHistory` ("true"/"false") → `{ "ok": true, "nodes": n, "checks": n, "results": n }`.
- `POST /api/backups/restore-existing` body `{ "fileName": "...", "password": "...", "includeHistory": bool }` → same.

See [`RESTORE.md`](RESTORE.md) for the end-to-end restore-to-a-new-machine procedure.

## Export

- `GET /api/export/history.csv?checkId=ID&range=30d` → CSV: `timestamp,avg_ms,min_ms,max_ms,jitter_ms,loss_pct,availability_pct,count,failures`.
- `GET /api/export/results.csv?checkId=ID&limit=5000` → raw results CSV: `timestamp,success,status,message,error,latency_ms,min_ms,max_ms,jitter_ms,stddev_ms,loss_pct,http_status,final_url,attempts`.
- `GET /api/export/events.csv?nodeId=&limit=5000&type=&q=&since=&until=` → events CSV (same filters as `/api/events`).
- `GET /api/export/logs.txt?limit=1000` → the recent service log as text.
- `GET /api/export/config.json` → nodes+checks+dashboards+maintenance+triggers+endpoints+saved charts as JSON (no passwords).

## Logs

- `GET /api/logs?limit=200` → `{ "lines": ["...", ...], "file": "path" }`.
- `GET /api/version` → `{ "version": "...", "platform": "windows/amd64", "apiVersion": 1 }` (see Versioning).

## Server-sent events

- `GET /api/stream` (text/event-stream) emits `event: update` with `data: {"kind":"result"|"state"|"event"|"config"|"health"|"maintenance"|"trigger"|"endpoint"|"discovery","checkId":..,"nodeId":..}` whenever something changes. The UI uses it to refresh without polling; falling back to polling every 15s is fine.
- A `"discovery"` update carries the sweep's counters instead of a check or node, a few times a second while one is running and once more when it stops: `{"kind":"discovery","discovery":{"id":"6f1c…","state":"running","scanned":118,"total":254,"responders":9}}`. The results themselves are read from `GET /api/discovery/{id}`.

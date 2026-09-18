# GWatch localhost API

All endpoints are served by the local service on `http://127.0.0.1:8080` (configurable) and
return JSON unless noted. Errors are `{"error": "message"}` with a 4xx/5xx status.
Timestamps are RFC 3339 strings. Field names match `internal/model/model.go`.

When an access password is set (Settings › Network access) every request from a
non-loopback client must carry HTTP basic auth (any user name, that password). Requests
from this computer and calls to `/hook/…` are exempt; hooks use their own token.

Static UI: `GET /` serves `web/index.html`; `/app.js`, `/app.css` etc. are served from `web/`.

## Health & overview

- `GET /api/health` → `model.Health` (service mode, scheduler, last/next check, db size, retention status, backup status, recent internal errors, alert config state).
- `GET /api/overview` → 
  ```json
  {
    "summary": {"up":0,"degraded":0,"down":0,"unknown":0,"paused":0,"maintenance":0,"total":0},
    "nodes": [ {"node": Node (with checks), "status": "up", "checks": [ {"check": Check, "state": CheckState, "lastResult": Result|null} ], "inMaintenance": false, "affectedBy": "Gateway"|"" } ],
    "groups": [ {"name":"Home Network","status":"up","up":3,"degraded":0,"down":0,"unknown":0,"paused":0,"maintenance":0,"total":3} ],
    "incidents": [ Event ... ]      // open down/degraded conditions as most recent related events (max 20)
    "certWarnings": [ {"nodeId":1,"nodeName":"Site","checkId":3,"checkName":"HTTPS","daysRemaining":9,"notAfter":"...","subject":"..."} ],
    "attention": [ {"nodeId":1,"nodeName":"...","checkId":2,"checkName":"...","status":"down","message":"...","since":"...","affectedBy":""} ],
    "maintenance": [ MaintenanceWindow ... ]  // currently active windows
    "generatedAt": "..."
  }
  ```
- `GET /api/wallboard` → same shape as overview plus `"health": Health` and `"trends": [HistorySeries...]` for up to 6 most important checks over 24h.
- `GET /api/status` → header summary: `{ "down", "degraded", "unknown", "up", "total", "certWarnings", "maintenance", "attention", "serviceOk", "serviceIssues": [..] }`.
- `GET /api/network` → `NetworkInfo`: effective listen address, whether other devices can reach it, LAN URLs, whether a password is set.

## Nodes and checks

- `GET /api/nodes` → `[Node]` each with `checks`, plus live state: each node object also carries `"status"`, `"stateByCheck": {checkId: CheckState}`, `"inMaintenance"`.
- `POST /api/nodes` body: `Node` (with `checks`, ids omitted) → created `Node`.
- `GET /api/nodes/{id}` → `Node` with checks, `stateByCheck`, `lastResults: {checkId: Result}`, `"status"`.
- `PUT /api/nodes/{id}` body: full `Node` with `checks`. Checks with an `id` are updated, without are created, missing ones are deleted. → updated `Node`.
- `DELETE /api/nodes/{id}` → `{ "ok": true }`.
- `POST /api/nodes/{id}/enable` body `{ "enabled": true|false }` → Node.
- `POST /api/nodes/{id}/duplicate` → new Node (name suffixed " (copy)", disabled).
- `POST /api/nodes/{id}/run` → runs all enabled checks of the node now → `[Result]`.
- `POST /api/nodes/{id}/silence` body `{ "minutes": 60 }` (0 = unsilence) → silences every check of the node → Node.
- `GET /api/templates` → `[NodeTemplate]` (website, home-server, router, api-endpoint, tcp-service, dns).
- `GET /api/groups` → `{ "groups": [{"name":"...","count":3}], "tags": [{"name":"...","count":2}] }`.

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
- `POST /api/checks/{id}/run` → `Result` (recorded and processed through alerting).
- `POST /api/checks/{id}/enable` body `{ "enabled": bool }` → Check.
- `POST /api/checks/{id}/silence` body `{ "minutes": 60 }` (0 = unsilence) → CheckState.
- `GET /api/checks/{id}/results?limit=50` → `[Result]` newest first.
- `GET /api/checks/{id}/state` → CheckState.

## History (charts)

- `GET /api/history?checkId=ID&range=1h|24h|7d|30d|1y` → `HistorySeries`.
  Multiple: `GET /api/history?checkId=1&checkId=2&range=24h` → `[HistorySeries]` (always an array when more than one id, single object for one id... to keep it simple the UI should use `GET /api/history/multi?checkId=..&checkId=..&range=` → `[HistorySeries]`).
- `GET /api/history/multi?checkId=1&checkId=2&range=24h` → `[HistorySeries]`. With `auto=1` and no `checkId`, the service picks up to 4 important checks (critical/high nodes, ping and HTTP first).
- Point spacing by range: 1h/24h → raw results (or 5-minute rollups if raw is gone), 7d → 5-minute rollups, 30d → hourly, 1y → daily.

## Events / incidents

- `GET /api/events?limit=100&before=ID&nodeId=&checkId=&type=&q=&since=&until=` → `[Event]` newest first. A `type` filter also includes its counterpart (down+recovered, warning+warning_cleared, cert_warning+cert_warning_cleared, silenced+unsilenced, maintenance_began+maintenance_ended, alert_sent+alert_failed) unless `exact=1`. `q` is a case-insensitive search over title, detail, node and check name; `since`/`until` accept RFC 3339, `2006-01-02T15:04` or `2006-01-02`.
- `POST /api/events/note` body `{ "nodeId": null|id, "text": "rebooted router" }` → Event (timeline annotation).

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
| `status_list` | `{ "group": "", "tag": "", "nodeIds": [] }` | node/check status list, filtered |
| `latency_chart` | `{ "checkIds": [1,2], "range": "24h", "metric": "avg" }` | line chart of latency/response time |
| `response_chart` | same as latency_chart (HTTP checks) | response time |
| `loss_chart` | `{ "checkIds": [], "range": "24h" }` | packet loss % |
| `uptime_chart` | `{ "checkIds": [], "range": "7d" }` | availability % per bucket |
| `incidents` | `{ "limit": 10 }` | recent incidents & recoveries |
| `cert_warnings` | `{}` | certificate expiry warnings |
| `attention` | `{}` | needs-attention list |
| `monitor_health` | `{}` | service health |
| `table` | `{ "group": "", "tag": "" }` | filtered node table |

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
- `ANY /hook/{slug}` → runs the endpoint's action and answers `ActionResult` (200, or 502 when the action failed). The token is passed as `?token=`, `X-GWatch-Token` or `Authorization: Bearer`; a wrong token is 401. An endpoint with no stored token is refused with 401 unless `allowNoToken` is set. `/hook/` URLs are not covered by the LAN access password, so the token is their only protection. The request body and query parameters are available to the action as `{{body}}` and `{{query.<name>}}`.

An `Action` is `{ "type": "http|git|script|run_node", "timeoutSeconds", ... }`:
| type | fields |
|---|---|
| `http` | `method` (auto: POST with body, else GET), `url`, `headers`, `body`, `expectedStatus` (default 200-399), `ignoreTlsErrors` |
| `git` | `repo` (working directory), `gitArgs` (everything after `git`) |
| `script` | `interpreter` (`sh`, `bash`, `powershell`, `cmd`, `python`, `node`, `custom`), `command` (for custom; `{{file}}` is the script path), `code`, `workDir`, `allowUntrustedInput` |
| `run_node` | `nodeId` |

String fields may contain `{{placeholders}}`: `node.name`, `node.host`, `node.group`, `check.name`, `check.type`, `target`, `status`, `prev_status`, `message`, `error`, `success`, `latencyMs`, `lossPct`, `statusCode`, `failures`, `event`, `ts`, `instance`, `body`, `query.<name>`. Scripts also receive them as `GWATCH_*` environment variables (`node.name` → `GWATCH_NODE_NAME`).

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

- `GET /api/update/status` → `{ "status": UpdateStatus, "repo": "owner/name", "version": "..." }`.
- `POST /api/update/check` → `UpdateInfo` from the repository's latest GitHub release (502 with `{error, info}` when GitHub cannot be reached or there is no release). On a build with no release signing key pinned, the check still reports the release and `info.error` explains that it cannot be installed.
- `POST /api/update/apply` → downloads the platform asset (`gwatch-<os>-<arch>[.exe]`), **verifies its signature**, swaps the executable and restarts the service → `{ ok, info, restarting }`.

A signature is mandatory. Each asset is published with a sibling `<asset>.sig` in the `gwatch-sig-v1` format — an ed25519 signature over the SHA-256 digest of the asset — and the downloaded file is only kept if that signature verifies against a public key pinned into the running binary (`internal/update/release_keys.txt`, or the `-X …/internal/update.releaseKeys=ed25519:…` build override). Otherwise the download is deleted and apply fails with:

| Situation | `error` |
|---|---|
| this build pins no key | `this build has no release signing key, updates are disabled (see docs)` |
| no `.sig` asset in the release | `the release is not signed` |
| signature is malformed or from another key | `signature verification failed` |

The `<asset>.sha256` sidecar is still checked when the release publishes one (`checksum mismatch` aborts the update), but it is only a transit-corruption guard and never substitutes for the signature. See [`RELEASING.md`](RELEASING.md).

## Settings

- `GET /api/settings` → `Settings` (SMTP password, access password and the scheduled-backup password are returned masked as `"********"` when set).
- `PUT /api/settings` body `Settings` → saved Settings (a masked password keeps the stored one). `general.theme` is `dark|light|system`, `general.accentColor` a hex colour, `general.remoteAccess` rebinds the listener to all interfaces live, `general.accessPassword` enables basic auth for other devices, `general.updateRepo` is the GitHub repository checked for releases. `backups` configures scheduled automatic backups (see below); it cannot be saved with `enabled: true` and no password.
- All three passwords are stored encrypted in the database with the local `gwatch.key` file; the API request and response bodies are unchanged.
- `POST /api/settings/test-email` body `{ "to": "optional@override" }` → `{ "ok": true, "message": "..." }` or error.
- `GET /api/retention/status` → `RetentionStatus`. `POST /api/retention/run` → runs rollup+cleanup now → RetentionStatus.

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
- `GET /api/export/results.csv?checkId=ID&limit=5000` → raw results CSV.
- `GET /api/export/events.csv?nodeId=&limit=5000&type=&q=&since=&until=` → events CSV (same filters as `/api/events`).
- `GET /api/export/logs.txt?limit=1000` → the recent service log as text.
- `GET /api/export/config.json` → nodes+checks+dashboards+maintenance+triggers+endpoints+saved charts as JSON (no passwords).

## Logs

- `GET /api/logs?limit=200` → `{ "lines": ["...", ...], "file": "path" }`.
- `GET /api/version` → `{ "version": "...", "platform": "windows/amd64" }`.

## Server-sent events

- `GET /api/stream` (text/event-stream) emits `event: update` with `data: {"kind":"result"|"state"|"event"|"config"|"health"|"maintenance"|"trigger"|"endpoint","checkId":..,"nodeId":..}` whenever something changes. The UI uses it to refresh without polling; falling back to polling every 15s is fine.

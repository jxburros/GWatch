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

- `GET /api/automation/meta` → conditions, interpreters, default interpreter and placeholder names.
- `GET /api/triggers?nodeId=` → `[Trigger]`. `POST /api/triggers`, `PUT /api/triggers/{id}`, `DELETE /api/triggers/{id}`.
  A trigger: `{ nodeId, name, description, enabled, on: ["down","recovered","degraded","warning_cleared","cert_warning","content_changed","affected_by_parent","status_change","any_failure","any_success","latency_over"], checkId: null|id, latencyOverMs, cooldownMinutes, action }` plus run statistics (`lastRunAt`, `lastStatus`, `lastOutput`, `runCount`).
- `POST /api/triggers/{id}/run` → `ActionResult` (runs it now with the node's current state).
- `POST /api/actions/test` body `{ "action": Action, "nodeId": null|id }` → `ActionResult` (nothing recorded).
- `GET /api/endpoints` → `[Endpoint]`. `POST /api/endpoints`, `PUT /api/endpoints/{id}`, `DELETE /api/endpoints/{id}`, `POST /api/endpoints/{id}/run`.
  An endpoint: `{ name, slug, description, enabled, method: "ANY|GET|POST|PUT|DELETE", token, action }`.
- `ANY /hook/{slug}` → runs the endpoint's action and answers `ActionResult` (200, or 502 when the action failed). The token, when set, is passed as `?token=`, `X-GWatch-Token` or `Authorization: Bearer`. The request body and query parameters are available to the action as `{{body}}` and `{{query.<name>}}`.

An `Action` is `{ "type": "http|git|script|run_node", "timeoutSeconds", ... }`:
| type | fields |
|---|---|
| `http` | `method` (auto: POST with body, else GET), `url`, `headers`, `body`, `expectedStatus` (default 200-399), `ignoreTlsErrors` |
| `git` | `repo` (working directory), `gitArgs` (everything after `git`) |
| `script` | `interpreter` (`sh`, `bash`, `powershell`, `cmd`, `python`, `node`, `custom`), `command` (for custom; `{{file}}` is the script path), `code`, `workDir` |
| `run_node` | `nodeId` |

String fields may contain `{{placeholders}}`: `node.name`, `node.host`, `node.group`, `check.name`, `check.type`, `target`, `status`, `prev_status`, `message`, `error`, `success`, `latencyMs`, `lossPct`, `statusCode`, `failures`, `event`, `ts`, `instance`, `body`, `query.<name>`. Scripts also receive them as `GWATCH_*` environment variables.
`ActionResult` is `{ ok, output, error, statusCode, startedAt, durationMs }`.

## Updates

- `GET /api/update/status` → `{ "status": UpdateStatus, "repo": "owner/name", "version": "..." }`.
- `POST /api/update/check` → `UpdateInfo` from the repository's latest GitHub release (502 with `{error, info}` when GitHub cannot be reached or there is no release).
- `POST /api/update/apply` → downloads the platform asset (`gwatch-<os>-<arch>[.exe]`, verified against `<asset>.sha256` when published), swaps the executable and restarts the service → `{ ok, info, restarting }`.

## Settings

- `GET /api/settings` → `Settings` (SMTP password and access password are returned masked as `"********"` when set).
- `PUT /api/settings` body `Settings` → saved Settings (a masked password keeps the stored one). `general.theme` is `dark|light|system`, `general.accentColor` a hex colour, `general.remoteAccess` rebinds the listener to all interfaces live, `general.accessPassword` enables basic auth for other devices, `general.updateRepo` is the GitHub repository checked for releases.
- `POST /api/settings/test-email` body `{ "to": "optional@override" }` → `{ "ok": true, "message": "..." }` or error.
- `GET /api/retention/status` → `RetentionStatus`. `POST /api/retention/run` → runs rollup+cleanup now → RetentionStatus.

## Backups

- `GET /api/backups` → `{ "backups": [BackupInfo], "dir": "...", "status": BackupStatus }`.
- `POST /api/backups` body `{ "password": "...", "includeHistory": true }` → BackupInfo.
- `GET /api/backups/{fileName}/download` → file (`application/octet-stream`).
- `DELETE /api/backups/{fileName}` → `{ok:true}`.
- `POST /api/backups/restore` multipart form: `file`, `password`, `includeHistory` ("true"/"false") → `{ "ok": true, "nodes": n, "checks": n, "results": n }`.
- `POST /api/backups/restore-existing` body `{ "fileName": "...", "password": "...", "includeHistory": bool }` → same.

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

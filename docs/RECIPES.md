# Trigger & endpoint recipes

Copy-pasteable automation setups for GWatch. Every recipe uses fields exactly as
they appear in the trigger/endpoint editor (Automation tab) and in the JSON API
(`internal/model/model.go`, `docs/API.md`). See `docs/API.md` → **Automation**
for the full field reference; this page is worked examples.

## Placeholders

String fields on an action (`url`, `headers`, `body`, `gitArgs`, `code`, …) may
contain `{{placeholder}}`. Unknown names expand to an empty string. Available
placeholders, from `internal/engine/automation.go` (`TriggerVars`) and
`internal/api/automation.go` (`handleHook`):

| Placeholder | Meaning |
|---|---|
| `{{node.name}}`, `{{node.host}}`, `{{node.group}}`, `{{node.tags}}`, `{{node.notes}}` | the node the trigger fired on |
| `{{check.name}}`, `{{check.type}}`, `{{check.target}}` | the check that fired it |
| `{{target}}` | the check's effective target (host/URL/port) |
| `{{status}}`, `{{prev_status}}` | new and previous status (`up`, `degraded`, `down`, `unknown`) |
| `{{message}}`, `{{error}}` | the result's message / error text |
| `{{success}}` | `true`/`false` for the run that fired the trigger |
| `{{latencyMs}}`, `{{lossPct}}`, `{{statusCode}}` | numeric fields, empty string when not applicable |
| `{{failures}}` | consecutive failures so far |
| `{{event}}` | the condition that fired it, e.g. `down`, `recovered`, `test`, `manual` |
| `{{ts}}` | RFC 3339 timestamp of the result |
| `{{instance}}` | this GWatch instance's name (Settings › General) |
| `{{trigger.name}}` | the trigger's own name |
| `{{body}}` | **endpoints only**: the raw request body posted to `/hook/{slug}` |
| `{{query.<name>}}` | **endpoints only**: a query-string parameter, e.g. `{{query.reason}}` |
| `{{method}}`, `{{remote}}` | **endpoints only**: HTTP method and caller address (or `MANUAL`/`ui` for a manual run) |

Scripts and git commands also get every placeholder as an environment variable
`GWATCH_*` (dots and dashes become underscores, e.g. `node.name` →
`GWATCH_NODE_NAME`), via `actions.Env()`. Use those instead of `{{…}}` inside
shell scripts to avoid quoting problems.

**Important — JSON bodies are not escaped.** `Expand()` does plain text
substitution: if `{{message}}` happens to contain a `"` or newline, the JSON
body becomes invalid and the webhook call fails at the receiving end. Keep
free-text placeholders (`message`, `error`) out of JSON string values, or put
them in fields unlikely to contain quotes (`node.name`, `status`, `check.name`,
`ts`). If you need the exact, safely-encoded message, use the **generic
webhook + script** pattern (recipe 2) and build the JSON with `jq`/`python`/
`node` inside the script, where you control escaping.

## 1. Home Assistant webhook trigger

Send a GWatch down/recovered event into Home Assistant to trigger any HA
automation (turn on a light, send a mobile notification, run a scene).

In Home Assistant, no explicit webhook needs to be created ahead of time —
any automation with a **Webhook** trigger auto-registers its ID. Create an
automation with:
- Trigger: **Webhook**, webhook ID `gwatch-alert` (pick your own, keep it
  unguessable since HA webhooks are unauthenticated by default).
- Condition (optional): `{{ trigger.json.status == 'down' }}`.
- Action: whatever you want (notify, turn on a siren, etc). The posted JSON
  is available as `trigger.json.*` in the automation.

GWatch trigger:
- **Conditions**: `Goes down`, `Recovers` (the `down`/`recovered` checkboxes).
- **Action type**: `HTTP request`.
- **Method**: `POST` (leave "Auto" — a body is present, so it becomes POST).
- **URL**: `https://ha.local:8123/api/webhook/gwatch-alert`
- **Headers**: none required.
- **Body**:
  ```json
  {"node": "{{node.name}}", "check": "{{check.name}}", "status": "{{status}}", "event": "{{event}}", "target": "{{target}}", "ts": "{{ts}}"}
  ```
- **Expected status**: leave as default (`200-399`).

What GWatch sends:
```bash
curl -X POST 'https://ha.local:8123/api/webhook/gwatch-alert' \
  -H 'Content-Type: application/json' \
  -d '{"node": "Plex Server", "check": "Ping", "status": "down", "event": "down", "target": "192.168.1.20", "ts": "2026-09-18T14:03:11Z"}'
```

If your HA instance uses a self-signed certificate, tick **Ignore TLS
certificate errors** on the action rather than disabling HTTPS.

## 2. Generic webhook consumer (n8n, Node-RED, any HTTP endpoint)

A safe, general-purpose envelope for tools that parse JSON webhooks
(n8n "Webhook" node, Node-RED `http in`, Zapier/Make catch-hooks, your own
receiver).

- **Action type**: `HTTP request`.
- **Method**: `POST`.
- **URL**: `https://n8n.example.com/webhook/gwatch` (n8n) or your endpoint.
- **Headers**: `Content-Type: application/json` (set automatically for a
  JSON-looking body, but fine to set explicitly).
- **Body**:
  ```json
  {
    "instance": "{{instance}}",
    "event": "{{event}}",
    "node": {"name": "{{node.name}}", "host": "{{node.host}}", "group": "{{node.group}}"},
    "check": {"name": "{{check.name}}", "type": "{{check.type}}", "target": "{{target}}"},
    "status": "{{status}}",
    "prevStatus": "{{prev_status}}",
    "latencyMs": "{{latencyMs}}",
    "lossPct": "{{lossPct}}",
    "statusCode": "{{statusCode}}",
    "failures": "{{failures}}",
    "ts": "{{ts}}"
  }
  ```
  Note `message`/`error` are deliberately left out — see the escaping warning
  above. If your consumer needs them, add them last and accept that a stray
  quote in a check's error text will break the payload; or switch to the
  script pattern below.

What GWatch sends (example for a `down` event):
```bash
curl -X POST 'https://n8n.example.com/webhook/gwatch' \
  -H 'Content-Type: application/json' \
  -d '{"instance":"home-gwatch","event":"down","node":{"name":"Router","host":"192.168.1.1","group":"Home Network"},"check":{"name":"Ping","type":"ping","target":"192.168.1.1"},"status":"down","prevStatus":"up","latencyMs":"","lossPct":"100","statusCode":"","failures":"3","ts":"2026-09-18T14:03:11Z"}'
```

### Script pattern for properly-escaped JSON

When you need `message`/`error` verbatim and safely encoded, use a **Custom
code** action instead of `HTTP request` and build the request yourself, e.g.
with `python` (interpreter `python`):

```python
import json, os, urllib.request

payload = {
    "node": os.environ.get("GWATCH_NODE_NAME", ""),
    "status": os.environ.get("GWATCH_STATUS", ""),
    "message": os.environ.get("GWATCH_MESSAGE", ""),
    "error": os.environ.get("GWATCH_ERROR", ""),
}
req = urllib.request.Request(
    "https://n8n.example.com/webhook/gwatch",
    data=json.dumps(payload).encode(),
    headers={"Content-Type": "application/json"},
    method="POST",
)
urllib.request.urlopen(req, timeout=10)
```

This reads the same values via the `GWATCH_*` environment variables
(`actions.Env()`), so `json.dumps` escapes them correctly no matter what
characters they contain.

## 3. Discord and Slack chat notifications

### Discord

- **Action type**: `HTTP request`.
- **Method**: `POST`.
- **URL**: your Discord webhook URL, e.g.
  `https://discord.com/api/webhooks/123456789012345678/AbCdEf...`
- **Body**:
  ```json
  {"content": "**{{node.name}}** — {{check.name}} is now **{{status}}**\n{{target}}"}
  ```
  Keep `content` short and avoid embedding `{{message}}` directly (quotes
  break the JSON) — if you want the message too, use a bulleted plain-text
  form without surrounding quotes at risk, e.g. append `\n{{message}}` only
  once you've confirmed your checks don't emit quote characters, or switch to
  the script pattern in recipe 2.
- Discord expects `200`/`204`; the default expected status (`200-399`) covers
  that.

Discord embed variant:
```json
{"embeds": [{"title": "{{node.name}} — {{check.name}}", "description": "{{status}} ({{event}})", "color": 15158332, "fields": [{"name": "Target", "value": "{{target}}"}, {"name": "Latency", "value": "{{latencyMs}} ms"}]}]}
```

### Slack (incoming webhook)

- **Action type**: `HTTP request`.
- **Method**: `POST`.
- **URL**: `https://hooks.slack.com/services/T000/B000/XXXXXXXXXXXXXXXXXXXXXXXX`
- **Body**:
  ```json
  {"text": "*{{node.name}}* — {{check.name}} is now *{{status}}* ({{target}})"}
  ```

Both send with the standard `curl`:
```bash
curl -X POST 'https://hooks.slack.com/services/T000/B000/XXXX' \
  -H 'Content-Type: application/json' \
  -d '{"text": "*Router* — Ping is now *down* (192.168.1.1)"}'
```

## 4. Push notifications: ntfy and Pushover

### ntfy.sh (or self-hosted ntfy)

ntfy takes the message as the raw POST body and reads title/priority/tags
from headers, not JSON — set them in the **Request headers** grid rather than
in a JSON `body`.

- **Action type**: `HTTP request`.
- **Method**: `POST`.
- **URL**: `https://ntfy.sh/gwatch-home-alerts` (your topic).
- **Headers**:
  - `Title`: `{{node.name}} is {{status}}`
  - `Priority`: `high` (use `urgent` for down, `default` for recovered — you
    can make two triggers, one per condition, with different priorities)
  - `Tags`: `warning,{{event}}`
- **Body**: `{{check.name}} on {{target}} — {{message}}` (plain text, so
  quotes in `{{message}}` are harmless here)

```bash
curl -X POST 'https://ntfy.sh/gwatch-home-alerts' \
  -H 'Title: Plex Server is down' -H 'Priority: urgent' -H 'Tags: warning,down' \
  -d 'Ping on 192.168.1.20 — Request timeout'
```

### Pushover

- **Action type**: `HTTP request`.
- **Method**: `POST`.
- **URL**: `https://api.pushover.net/1/messages.json`
- **Headers**: `Content-Type: application/x-www-form-urlencoded` — but GWatch
  always sends the literal `body` text, so build a URL-encoded body by hand
  (Pushover also accepts JSON with `Content-Type: application/json`, which is
  simpler here):
- **Body** (JSON):
  ```json
  {"token": "YOUR_APP_TOKEN", "user": "YOUR_USER_KEY", "title": "{{node.name}} is {{status}}", "message": "{{check.name}} on {{target}}", "priority": 0}
  ```

```bash
curl -X POST 'https://api.pushover.net/1/messages.json' \
  -H 'Content-Type: application/json' \
  -d '{"token":"YOUR_APP_TOKEN","user":"YOUR_USER_KEY","title":"Plex Server is down","message":"Ping on 192.168.1.20","priority":0}'
```

## 5. Recovery actions: restart a container, chain another node's checks

### Restart a Docker container locally when a node goes down

- **Trigger conditions**: `Goes down` only (not `recovered`, or it will also
  fire on the way back up).
- **Cooldown**: set e.g. `15` minutes so a flapping check doesn't restart the
  container in a loop.
- **Action type**: `Custom code`, interpreter `sh` (or `bash`).
- **Code**:
  ```sh
  docker restart "$GWATCH_NODE_NAME_CONTAINER" || docker restart plex
  ```
  In practice, hardcode the container name (it rarely matches the GWatch node
  name exactly) — put it directly in the script rather than deriving it from
  a placeholder:
  ```sh
  docker restart plex
  ```
- **Timeout**: bump to `60` seconds if the container is slow to stop.

### Restart a container over SSH

- **Action type**: `Custom code`, interpreter `sh`.
- **Code**:
  ```sh
  ssh -o BatchMode=yes -o ConnectTimeout=5 gwatch@192.168.1.20 'docker restart plex'
  ```
  This runs as the GWatch service account, so that account needs a working
  SSH key (`~/.ssh/id_ed25519` with no passphrase, or an ssh-agent reachable
  from the service) and the key must be in the target's `authorized_keys`.

### Chained recovery: run another node's checks right after a fix

Useful when node B depends on node A (e.g. a reverse proxy in front of an
app) and you want to re-verify B immediately after remediating A, instead of
waiting for the next scheduled cycle.

- Trigger on node A: `Goes down` → `Custom code` that restarts A's service,
  **then** a second trigger on node A: `Recovers` → **Action type**
  `Run a node now`, **Node**: pick node B. This re-runs every enabled check
  of B as soon as A comes back, so B's status reflects reality without
  waiting for its own schedule.
- Equivalently, from a custom endpoint or script, `POST /api/nodes/{id}/run`
  achieves the same "run now" for a node on demand.

## 6. Inbound: a custom endpoint called by a router or CI job

Expose `/hook/router-rebooted` so an external script (a router's boot script,
a CI pipeline, a cron job) can tell GWatch to immediately re-check a node,
without waiting for the schedule.

Create an **Endpoint** (Automation tab → Endpoints):
- **Name**: `Router rebooted`
- **URL name (slug)**: `router-rebooted` → served at `/hook/router-rebooted`
- **Accepts**: `POST` (or `ANY` if the caller can't guarantee the verb)
- **Token**: generate one (the UI has a "generate" button) — say
  `s3cr3t-token-value`. Callers must send it as `?token=`, an
  `X-GWatch-Token` header, or `Authorization: Bearer …`.
- **Action type**: `Run a node now`, **Node**: your router node.

Call it from the router/CI job:
```bash
curl -X POST 'http://gwatch.local:8080/hook/router-rebooted' \
  -H 'X-GWatch-Token: s3cr3t-token-value'
```
or with the token in the URL:
```bash
curl -X POST 'http://gwatch.local:8080/hook/router-rebooted?token=s3cr3t-token-value'
```

The response is the `ActionResult` JSON (`{ "ok": true, "output": "Ran the
checks of node 12.", ... }`), status `200` on success or `502` if the action
itself failed (not if the check results were down — that's still `200`, only
the *action* failing gives `502`).

If the caller needs to pass extra context, it is available to the action's
placeholders as `{{body}}` (raw request body) and `{{query.<name>}}` (query
string params), e.g.:
```bash
curl -X POST 'http://gwatch.local:8080/hook/router-rebooted?token=s3cr3t-token-value&reason=firmware-update' \
  -d 'rebooted by cron at 03:00'
```
would make `{{query.reason}}` = `firmware-update` and `{{body}}` = `rebooted
by cron at 03:00` available if the endpoint's action were an `HTTP request`
or `Custom code` action instead of `Run a node now`.

## 7. Git: pull a config repository when a deploy finishes

Trigger a `git pull` in a local checkout when an external deploy job notifies
GWatch, so a config-management repo on the monitoring box stays in sync.

Create an endpoint:
- **Name**: `Deploy finished`
- **URL name (slug)**: `deploy-finished`
- **Accepts**: `POST`
- **Token**: set one, same as recipe 6.
- **Action type**: `Git command`.
- **Repository directory**: `/opt/gwatch-config` (an existing local clone;
  GWatch does not clone it for you, and this must be a directory that
  already exists and is a valid git working tree).
- **Git arguments**: `pull --ff-only`

Call it from your deploy pipeline's last step:
```bash
curl -X POST 'http://gwatch.local:8080/hook/deploy-finished' \
  -H 'X-GWatch-Token: s3cr3t-token-value'
```

The command runs as `git pull --ff-only` inside `/opt/gwatch-config`, using
the GWatch service account's own git credentials/SSH agent (there's no
credential injection — configure the remote as you would for any
non-interactive `git pull` on that account, e.g. a deploy key with no
passphrase, and `GIT_TERMINAL_PROMPT=0` is already set so a missing
credential fails fast instead of hanging).

You can equally use a *trigger* instead of an endpoint here — e.g. run
`git pull` in a docs repo whenever a specific node (your CI runner) recovers.

## Testing a recipe

Before saving, or any time after, use **Test this action** in the trigger or
endpoint editor. It calls `POST /api/actions/test`:
```bash
curl -X POST 'http://127.0.0.1:8080/api/actions/test' \
  -H 'Content-Type: application/json' \
  -d '{"action": {"type": "http", "url": "https://ntfy.sh/gwatch-home-alerts", "body": "test from GWatch"}, "nodeId": null}'
```
This runs the action once with sample placeholder values (or, if `nodeId` is
given, the real current state of that node's first check) and returns an
`ActionResult` — **nothing is recorded**: no event, no `lastRunAt`, no
`runCount` change. It is safe to test destructive-looking actions (git,
scripts) as many times as you like, but remember the action itself still
actually runs (a `script` action really executes, a `git` action really runs
git) — only the bookkeeping is skipped.

An already-saved trigger can also be fired for real with
`POST /api/triggers/{id}/run` (uses the node's current state, and *is*
recorded), and an endpoint can be run manually from the UI, which posts
`{"method": "MANUAL", "remote": "ui"}` as the endpoint's `{{method}}`/
`{{remote}}` placeholders.

## Security

- **Endpoint tokens** (`Endpoint.token`) are the only access control on
  `/hook/{slug}` — set one for any endpoint reachable outside this computer,
  and treat it like a password (long, random, not reused). Without a token,
  anyone who can reach the URL can run the action.
- **Access password** (Settings › Network access → `general.accessPassword`)
  gates the rest of the API and UI for non-loopback clients (HTTP basic auth,
  any username). It does **not** apply to `/hook/{slug}` calls, which use
  their own token instead — this lets an unauthenticated router or CI job
  call a specific hook without knowing the UI password.
- Triggers and endpoints run **on the computer running GWatch, with its
  process's permissions** — a `script` or `git` action is exactly as
  powerful as a shell on that machine. Give the service account only the
  access it needs (e.g. a scoped SSH deploy key, a `docker` group membership
  if it must restart containers), not broad admin rights.
- Outbound webhook URLs and bodies can carry secrets (tokens, API keys) in
  plain text in the trigger/endpoint config; anyone with UI/API access can
  read them back via `GET /api/triggers` or `GET /api/endpoints` (they are
  not masked). Restrict who can reach the GWatch UI accordingly.
- `GET /api/export/config.json` deliberately omits passwords but **does**
  include trigger/endpoint actions (and therefore any secrets embedded in
  their URLs/headers/bodies) — treat exported config files the same way you
  treat the live config.

<img src="../web/logo.svg" alt="" width="72" align="right">

# GWatch User Guide

Everything the interface can do, screen by screen — including the corners most
people never find. If you want the JSON API instead, that is
[`API.md`](API.md); if you want to install GWatch, start at
[`INSTALL.md`](INSTALL.md) or [`DOCKER.md`](DOCKER.md).

GWatch also has a **Help** page built in, at **Help** in the sidebar. That page
is a short reference you can search while you work. This guide is the long
version: it goes through every screen in order and stops on the settings and
options the Help page only mentions in passing.

---

## Contents

**Getting going**
1. [Your first half-hour](#1-your-first-half-hour)
2. [Finding your way around](#2-finding-your-way-around)

**The things you watch**
3. [Nodes and checks](#3-nodes-and-checks)
4. [The ten check types](#4-the-ten-check-types)
5. [Discovery: adding a whole network at once](#5-discovery-adding-a-whole-network-at-once)
6. [Bulk edit: changing many things at once](#6-bulk-edit-changing-many-things-at-once)

**Reading what happened**
7. [Dashboards](#7-dashboards)
8. [Charts](#8-charts)
9. [Incidents and Audit](#9-incidents-and-audit)

**Being told about it**
10. [Email alerts](#10-email-alerts)
11. [Notification rules](#11-notification-rules)
12. [Automation: triggers and endpoints](#12-automation-triggers-and-endpoints)

**The rest of the surface**
13. [Hardware health and the agent](#13-hardware-health-and-the-agent)
14. [Wallboards](#14-wallboards)
15. [Settings, tab by tab](#15-settings-tab-by-tab)
16. [Accounts, API keys and remote access](#16-accounts-api-keys-and-remote-access)
17. [Backups, restore and moving machine](#17-backups-restore-and-moving-machine)
18. [Keyboard and accessibility](#18-keyboard-and-accessibility)
19. [Lesser-known corners](#19-lesser-known-corners)
20. [When something is wrong](#20-when-something-is-wrong)

---

## 1. Your first half-hour

GWatch runs as a Windows background service — or as a plain console program on
Linux and macOS, in which case monitoring happens only while that process runs
— and serves its interface at `http://127.0.0.1:7230`. Open that on the
computer it runs on and you are
already an administrator — GWatch treats a request from the machine itself as
coming from the person sitting at it, so there is no password to get past on a
fresh install.

A sensible order for the first half-hour:

| Step | Where | Why first |
|---|---|---|
| 1. Create an administrator account | **Settings › Users & access** | Until you do, "this computer" is the only identity there is, and nothing in the audit log has a name against it |
| 2. Add your router as the first node | **Nodes › Add node**, template *Network device* | Almost everything else depends on it, so it is the node other nodes will point at |
| 3. Add the two or three things you actually notice when they break | **Nodes › Add node** | Resist adding forty. A monitor you ignore is worse than none |
| 4. Set the router as the parent of the rest | each node's **Depends on** | One outage then reads as one outage, not a dozen |
| 5. Set up email and send the test | **Settings › Alerts** | An alerting system you have never seen deliver a message is a guess |
| 6. Take a backup | **Settings › Backups** | One encrypted file, and you can rebuild anywhere |

If you would rather be shown than told, the **guided tour** runs automatically
on a fresh install and can be replayed any time from **Help › Guided tour and
tips**. The same page turns the small contextual **tips** on and off, and
resets the ones you have already dismissed.

> **Just looking?** Add `?mock=1` to the address — `http://127.0.0.1:7230/?mock=1`
> — and GWatch runs against a fake in-browser backend with invented data. You
> can click everything, including destructive buttons, and nothing real is
> touched. It is how the interface is developed and it is the safest way to
> explore before you commit to a layout.

---

## 2. Finding your way around

### The layout

- **The sidebar** on the left is in two parts. *Monitor* holds **Dashboard**,
  **Nodes**, **Charts** and **Incidents**; *System* holds **Audit** and
  **Help**. Along its foot sit the signed-in account, **Sign out**,
  **Wallboards**, the service-health line and **Pin sidebar**. The sidebar
  collapses to icons to give the content more room; the pin keeps it open.
- **The top bar** carries the page title, the **status indicators** under it,
  and on the right the **Settings** button — Settings is reached from here, not
  from the sidebar. An **Update** badge appears beside it when a newer release
  is waiting, and only for an administrator, who is the only one who could
  install it.
- **The page bar** below that holds the current page's own controls, and is
  simply absent on pages that have none.
- **The service-health line**, at the foot of the sidebar, is about GWatch
  itself rather than about what it is watching. It links to **Settings ›
  Monitor health**.
- **A red banner** across the top means the browser cannot reach the GWatch
  service at all — a different problem from anything being down, and it has its
  own **Retry**.

### The status indicators

Those small coloured circles under the page title are not decoration and they
are not fixed. Each one is a rule you can configure in **Settings ›
Indicators**, and each one is a link that takes you to whatever it is
complaining about:

| Colour | Means |
|---|---|
| **Green** | Nothing is firing. GWatch works this out itself; it is not a rule you can add |
| **Blue** | Nothing to report on yet — no nodes, or none that has produced a result |
| **Red** | Needs looking at now |
| **Orange** | Worth knowing about |
| **Yellow** | For information |

Several firing at once stack side by side, reddest first. The conditions a rule
can test are deliberately few — nodes in a given status, certificates expiring,
checks needing attention, or the monitor itself being unwell — because they are
evaluated in your browser against one summary document, so anything needing a
second request every few seconds is not on the menu.

### Statuses

Every check, and every node, is in one of six states:

| Status | Meaning |
|---|---|
| **Up** | Answering, within its thresholds |
| **Degraded** | Answering badly — slow, lossy, a certificate near expiry, content changed, a metric over its warning threshold |
| **Down** | Not answering, or past its critical threshold, for more consecutive runs than its failure threshold allows |
| **Unknown** | No result yet. Normal on a young install |
| **Maintenance** | Inside a maintenance window |
| **Paused** | Disabled by you |

A node shows the worst status among its checks.

### Appearance

**Settings › Appearance** is one of the three tabs a viewer account can reach.
It holds the **theme** (Dark, Light, or follow the operating system), the
**accent colour**, and the **density**: *Compact*, the default, tightens the
rows so a long node list scrolls easily, and *Breathing room* restores the
roomier spacing GWatch used to ship with.

---

## 3. Nodes and checks

### The idea

A **node** is one thing you care about — a device, a server, a site. It holds
one or more **checks**, which are the individual questions asked of it on a
schedule. Keeping them together means a machine that falls over goes down once,
not three times.

Each node carries:

- **Host** — the default target its checks inherit. A check can override it.
- **Groups** — a node can be in several. Everything that filters by group
  matches *any* of them, and a name that differs only in case is folded onto
  the first spelling you used. Maximum 16.
- **Tags** — free-form labels for filtering.
- **Importance** — *low*, *normal*, *high* or *critical*. Critical and high
  nodes are listed first on a wallboard.
- **Depends on** — a parent node. While the parent is down, alerts for this
  node are suppressed and recorded as "affected by" instead. This is the single
  most useful field on the page and the one most often left empty.
- **Notes** — free text, for the thing you will not remember in six months.
- **Enabled** — a disabled node keeps its configuration and its history and
  stops being checked.

### Templates

**Add node** starts from a template, which prefills sensible checks and
thresholds. There are nine:

| Template | Checks it creates |
|---|---|
| **Website** | *Website loads* (HTTP, expects 200-399), *Certificate* (port 443, warn 14 days), *Name resolves* (DNS, A) |
| **Home server** | *Reachable (ping)*, *SSH port 22* (TCP), *Web interface* (HTTP, TLS errors ignored) |
| **Network device** | *Reachable (ping)*, *Internet name lookup* (DNS for `google.com`), *Admin page port 80* (TCP) |
| **Ping only** | *Reachable (ping)* — the quickest way to add a printer or a camera |
| **API endpoint** | *API responds* (HTTP, expects 200-299), *Status value* (JSON, `status` must equal `ok`) |
| **TCP service** | *Port accepts connections* — port 32400, i.e. Plex, ready to change |
| **DNS only** | *Name resolves* (DNS, A) |
| **This computer** | *Hardware health* for the machine GWatch runs on |
| **Machine with an agent** | *Hardware health* fed by an agent elsewhere |

Every one of those is a starting point, not a cage: rename them, delete the
ones you do not want, add others.

The template is recorded on the node but is only informational afterwards —
changing what a node does is a matter of editing its checks, not its template.

### The node list

**Nodes** is a searchable, filterable list. The search box matches a node's
name, host, groups, tags **and its check names**. Above it sit chip filters for
status, group and tag, with live counts.

> **The filters travel in the address bar.** Filter the list down to what you
> want and the URL changes with it, so you can bookmark "everything critical
> that is down" or paste it to someone else and have them see the same view.

The list is sectioned by group, worst status first inside each section. With a
group filter on, a node in several groups is listed under the group you
filtered to rather than its first one.

Each row's **Actions** menu holds *Open*, *Edit*, *Run all checks now*,
*Duplicate* and *Delete node*. The page's own buttons are **Add node**,
**Discover** and **Bulk edit**.

### The node page

Opening a node shows its checks, each with its current state, its recent
results and its charts.

- **Run now** on a check, or **Run all now** for the node, runs it immediately
  rather than waiting for the schedule. The result is recorded and processed
  through alerting exactly as a scheduled run would be.
- **Silence** stops alerts for a while without disabling the check: 1 hour, 8
  hours or 24 hours, and *Unsilence* to end it early. The check keeps running
  and keeps recording; you simply stop being told.
- **Enable / Disable** a single check, or the whole node.
- **More actions** holds *Edit checks*, *Duplicate*, *Delete node* and three
  CSV exports — **history**, **results** and **events** — scoped to this node.

### The result inspector

Clicking a result row opens the **inspector**, which shows everything the check
recorded rather than just its verdict. What appears depends on the type:

- **Ping** — packets sent and received, per-packet round-trip times, min, max,
  average, jitter and standard deviation.
- **HTTP/S** — the DNS, connect, TLS and first-byte timings separately, the
  status code, the final URL after redirects, and the certificate.
- **Certificate** — issuer, subject, validity dates and days remaining.
- **DNS** — the records that came back.
- **SNMP** — every OID: the raw value as the device sent it, the scaled value
  or per-second rate, and its unit.
- **Custom script** — the combined stdout and stderr the script produced, up to
  8 KiB.
- **Hardware** — each metric with its own verdict against its own threshold.

This is the screen to reach for when a check is failing and the one-line
message is not enough.

---

## 4. The ten check types

Every type shares a schedule (**interval**, **timeout**, **retries**,
**failures before down**) and takes an optional **target** that overrides the
node's host.

> **Interval, timeout, retries, threshold** are four different things and are
> often confused. *Interval* is how often the check runs. *Timeout* is how long
> one attempt may take. *Retries* are immediate second tries inside a single
> run. *Failures before down* is how many consecutive **runs** must fail before
> the check is called down and alerts fire — so `0` here follows the global
> setting, and `1` means the first bad run is an outage.

### Ping

Reachability and latency over ICMP.

- **Packets per run** — default 4, maximum 20.
- **Warn when slower than** (ms) and **warn when packet loss exceeds** (%) mark
  the check degraded rather than down. `0` turns either off.
- **Ping method** — *Auto*, *Built-in sender*, or *System ping command*.
  Built-in opens a raw ICMP socket itself; system runs the operating system's
  `ping` and parses it. Auto picks. Set it per check, or globally in
  **Settings › General**. If ping fails on Linux or in Docker with a permission
  error, this is the setting to change (or grant `NET_RAW`).

### HTTP/S

- **Method**, **request headers**, **request body**.
- **Expected status** — `200`, a range like `200-299`, or a list like
  `200,301,302`. The default is `200-399`.
- **Follow redirects** — on by default. Turn it off to assert on the redirect
  itself.
- **Ignore TLS certificate errors** — for self-signed certificates.
- **Also check the HTTPS certificate** — on by default for `https` targets, so
  one check covers both reachability and expiry.
- **Warn before expiry** (days) for that certificate check.

> **Request headers are stored as written.** Unlike the SNMP and agent
> credentials, they are not encrypted and not masked, so an `Authorization`
> header on an HTTP check sits readable in the database. Use a token you are
> willing to store that way.

### HTTPS certificate

Issuer, validity and days remaining for a `host:port` — port 443 by default.
**Warn before expiry** defaults to 14 days. Use this when you want expiry
watched on its own schedule rather than as a rider on an HTTP check.

### TCP port

Can a connection be opened. **Port** is required. The honest test for SSH, a
database, or Plex on 32400.

### DNS

- **Record type** — *A / AAAA* (the default, which resolves both), *CNAME*,
  *MX* or *TXT*.
- **Expected values** — optional. Every resolved value must be in the list.
- **DNS server** — optional `host[:port]`. Leave it blank for the system
  resolver. Filling it in is how you check one particular resolver rather than
  "does this name resolve here", which is what makes a "is Pi-hole still
  answering?" check possible.

### Keyword

Everything HTTP/S takes, plus:

- **Keyword** — the text that must be present.
- **Text must be absent** — inverts it, so the check alerts when the text
  *appears*. Good for a maintenance banner or an error string.

### JSON

Everything HTTP/S takes, plus:

- **JSON path** — dotted, e.g. `status` or `data.items[0].name`.
- **Expected value** — leave it blank and the check merely asserts the path
  exists.
- **Record this value** — the lesser-known half of this check type. Tick it and
  the number at that path is **stored with every run**, charted on the node page
  with its unit, and exported per metric as CSV. Give it a **metric name** and a
  **unit**, and optionally warning and critical thresholds above or below. A
  value that is not a number is kept as text in the result details instead.
  This turns any JSON API into a time series — a thermometer, a queue depth, a
  Pi-hole's block percentage.

### Custom script

Runs a command of yours on the check's schedule.

- **Command line** — split on whitespace, honouring quotes. **No shell is
  used**, so pipes, globbing and `$VAR` do nothing unless the command itself is
  `sh -c '…'` or `cmd /C …`.
- **Working directory** and **environment variables** are optional.
- The target is passed as the `GWATCH_TARGET` environment variable and
  substituted for a literal `{{target}}` inside any one argument.

The contract is the exit code, plus optional `key=value` lines on stdout:

```
exit 0 → up      exit 2 → degraded      anything else → down

status=up|degraded|down     overrides the exit code
message=…                   shown as the result message
latency_ms=<number>         plotted on the charts
error=…                     recorded as the error
```

Everything else the script prints is kept as the result's output, capped at
8 KiB.

> **This runs on the GWatch machine with the service's permissions.** Only
> trusted administrators should be able to create or edit a custom check.

### Hardware health

Covered in full under [Hardware health and the agent](#13-hardware-health-and-the-agent).

- **Read hardware from** — *This computer*, a paired **machine**, or **a
  metrics endpoint** GWatch reads over HTTP.
- **Watch only these mount points** — leave empty for every filesystem.
- **Report down after no reading for** — `0` means three times the interval.
- **Thresholds** are per metric, as a family (`disk`) or one instance
  (`disk:/srv`).

### SNMP

Reads OIDs straight off a router, switch or access point.

- **SNMP version** — *2c* with a community string, or *3* with a user,
  authentication (MD5 / SHA / SHA224 / SHA256 / SHA384 / SHA512) and optional
  encryption (DES / AES / AES192 / AES256 / AES192C / AES256C).
- **Presets…** fills in the standard MIB readings: uptime, interface traffic in
  and out (32- and 64-bit), errors in and out, link up/down, processor load,
  device name and description.
- Each reading has a **name**, a **kind** (*gauge* for a value that means
  something as it stands, *counter* for a total that only climbs and is charted
  as its per-second rate), a **scale** (`8` turns octets per second into bits
  per second), a **unit**, and its own four thresholds.

OIDs must be dotted numeric — GWatch ships no MIB files, so MIB *names* are
refused. Up to 64 readings per check, fetched in one request per run.

> **"Walk this device"** in the editor lists what the device actually exposes
> so you can tick the readings you want rather than guessing OIDs. It is the
> fastest way to set up an unfamiliar switch.
>
> **A wrong community string looks exactly like an unreachable device.** SNMP
> v2c answers a bad community with silence, so both produce a timeout. The
> result message says so, but it cannot tell them apart for you.

See [`SNMP.md`](SNMP.md) for enabling SNMP on the device end.

---

## 5. Discovery: adding a whole network at once

**Nodes › Discover** pings a range of addresses, looks up the name of whatever
answers, tries a few ports on it, and offers the lot as a list you can tick.

- **Ranges to scan** — one per line. CIDR (`192.168.1.0/24`), a dash range
  (`192.168.1.10-50`) or a single address (`10.0.0.5`) all work, and you can
  mix them.
- **Ports to try on whatever answers** — what it probes to guess what each
  device is.
- **Put them in a group** — everything you add lands in that group.

Each responder comes back with its address, its name, its round-trip time, its
open ports and a **suggested template**. Tick the ones you want and add them in
one go. Progress is live, and a sweep can be cancelled while it runs.

Nothing is installed and no `nmap` is involved — it is ICMP plus a few TCP
connects.

> **Inside Docker with the default bridge network, discovery sees Docker's
> own subnet, not your LAN.** Host or macvlan networking is what makes it
> useful there. See [`DOCKER.md`](DOCKER.md).

---

## 6. Bulk edit: changing many things at once

**Nodes › Bulk edit** changes one setting across as many nodes and checks as
you tick. You build a list of changes, see exactly what will happen written out
in words, and confirm once.

**Check fields**

| Group | Fields |
|---|---|
| Schedule | Check interval, timeout, retries, failures before down, checks enabled |
| Thresholds | Warn when slower than, warn when packet loss exceeds, certificate warning, **hardware threshold**, ping method |
| Alerts | Send alerts, alert cooldown, notify on recovery, notify on warnings, alert recipients, **clear all alert overrides** |

**Node fields**

Add to groups · remove from groups · replace groups with · add tags · remove
tags · replace tags with · importance · nodes enabled · depends on.

Notes on the ones that trip people up:

- The alert overrides are **tri-state**: *yes*, *no*, or *follow the global
  setting*. "Clear all alert overrides" puts the selection back on the global
  settings entirely.
- **Hardware threshold** sets the warning and critical pair for one metric —
  a family such as `disk`, or one instance such as `disk:/srv` — across every
  hardware check in the selection, and leaves their other thresholds alone.
- **Add / remove** groups and tags are additive; **replace … with** overwrites
  the whole list, and leaving it empty clears it.

**A check's type is the one thing bulk edit will not change**, because the type
decides what the rest of its configuration means. Change a type one check at a
time in the editor.

Nothing is written unless every change validates, and it all goes in one
transaction — so a partial bulk edit is not a state you can end up in.

---

## 7. Dashboards

**Dashboard** is the desk view: a grid of widgets you arrange. You can have
more than one — the tabs along the top switch between them, and a default
"Overview" dashboard is created on first run.

There are thirteen widgets:

| Widget | Shows |
|---|---|
| **Overall health** | The one-line summary: how many of everything are up |
| **Group status** | One tile per group, coloured by its worst check |
| **Status list** | A list of nodes and their current status |
| **Needs attention** | Everything down or degraded right now, worst first |
| **Recent incidents** | The latest entries from the timeline |
| **Certificate warnings** | Certificates expiring or already invalid |
| **Monitor health** | Whether GWatch itself is running and checking |
| **Node table** | A denser table of nodes |
| **Chart** | A saved chart of your own |
| **Latency chart** | Latency over time |
| **Response-time chart** | Response time over time |
| **Packet-loss chart** | Loss over time |
| **Uptime** | Availability as a bar |

**Arranging them.** Drag a widget by its grip to move it, and its corner to
resize. From the keyboard, focus the grip and use the **arrow keys** to move it
one cell at a time, or **Shift + arrows** to resize — the new position is read
out as it changes, so this works with a screen reader.

---

## 8. Charts

**Charts** is where a chart you want to keep gets saved rather than rebuilt.
Pick checks and a range, then:

- **Save**, and **Save as a copy** for a variant.
- **Rename** and **Delete chart**.
- **Pin to a dashboard** — the saved chart becomes a *Chart* widget there.
- **Export chart as PNG** for a report or a message.

Every chart in GWatch, here and elsewhere, has two things worth knowing about:

- **Left and right arrow keys** step the tooltip through its points when the
  chart has focus.
- **"View as table"** gives the same figures as a table, which is both the
  accessible alternative and the fastest way to read an exact value off a busy
  line.

**Ranges and resolution.** 1h and 24h are drawn from raw results, 7d from
5-minute rollups, 30d from hourly and 1y from daily. A **named metric** series
— an SNMP reading, a recorded JSON value, a hardware metric — is always read
from raw results, because the rollup tables have columns for latency, jitter
and loss and nowhere to put a metric a check invented. So those series reach
back only as far as raw history is kept, 30 days by default.

---

## 9. Incidents and Audit

**Incidents** is the timeline: outages, recoveries, warnings, certificate
warnings, alerts sent and suppressed, silences, maintenance windows,
configuration changes, service starts and stops, sleep gaps, backups,
discovery sweeps, rules firing, and your own notes. Filter it by node and by
type.

> **Filtering by a type includes its counterpart.** Asking for `down` gives
> you the recoveries too, `silenced` gives you the unsilences, and so on — the
> pairs stay together so an incident reads as a story rather than as half of
> one.

**Add note** writes a human annotation into the timeline, either against a node
or on its own. This is how "rebooted the router" ends up next to the outage it
explains.

**Audit** is the same log with the full apparatus, in three tabs:

| Tab | What it is |
|---|---|
| **Event log** | Full-text search across title, detail, node and check name, plus date range, node and type filters |
| **Service log** | The service's own log file — what GWatch printed, not what it observed |
| **Exports** | CSV of history, results and events, and the service log as text |

Every event carries an **actor** naming who caused it — `local`, `pat (admin)`,
`api key Home Assistant (read-write)`, or `from 198.51.100.5` for a rejected
credential. Events the monitoring engine produces by itself have no actor, so a
blank actor column means "nobody did this, it just happened".

---

## 10. Email alerts

**Settings › Alerts** holds the global email configuration.

- **Send email alerts** — the master switch.
- **Recipients** — where they go.
- **Failures before down** — consecutive failed runs before a check alerts.
- **Cooldown** — the minimum gap between two alerts about the same check, so a
  flapping check cannot fill your inbox. 60 minutes by default.
- **Send a recovery email when a check becomes healthy again.**
- **Also email for warnings** — latency, packet loss, expiring certificates,
  content changes.
- **Certificate warning** — how many days before expiry counts as a warning.
- **SMTP** — host, port, security (*STARTTLS* on 587, *TLS/SSL* on 465, or
  *none*), from address, user name and password.

**Send a test message** is not optional in spirit. An alerting setup you have
never watched deliver is a guess.

### The four ways an alert gets narrowed

1. **Per-check overrides** — any check can override the global alert settings,
   including its own recipients.
2. **Silencing** — a node or check muted for 1, 8 or 24 hours from its page.
3. **Maintenance windows** — **Settings › Maintenance**, either one-off (start
   and end) or weekly recurring (days, start time, duration), scoped to all
   nodes, one group or one node.
4. **Dependencies** — while a parent node is down, its children do not alert
   separately; they are recorded as "affected by".

Every alert *not* sent because of one of these is written to the timeline as
**alert suppressed**, with the reason. If you think GWatch is too quiet, that
filter on the Incidents page is the first place to look.

---

## 11. Notification rules

**Settings › Rules** answers the question per-node alerts cannot: *"tell me
when two of my three DNS servers are down."* Per-node alerts look at one check
at a time; a rule looks across the lot.

A rule is:

- **Conditions** — a list, each one a check, or any check on a node, being
  **down** or **degraded or worse**.
- **Met when** — *all conditions*, *any condition*, or *at least N*, with N as
  a number you set.
- **Actions** — the same actions triggers use (below).
- **Cooldown** — minutes, `0` for none.
- **Also notify when the rule clears** — runs the actions again on the way back.

A rule fires **once** when its conditions come together, records `rule_fired`
in the timeline, and stays met until they come apart, when it records
`rule_cleared`. It is re-evaluated on every status change and whenever
maintenance or silencing changes, and it remembers its state across restarts.

**Run the actions once with sample values** tests a rule without waiting for
the real thing to happen.

Per-node alerts, triggers and dependency suppression are untouched by rules —
they sit beside them rather than replacing them. Hold timers ("only when this
has lasted ten minutes") and metric conditions ("disk above 90%") are
deliberately not in this first version.

---

## 12. Automation: triggers and endpoints

**Settings › Automation** is everything that is not email.

### Triggers

A trigger watches one node and runs an action. **Run when this node…**:

| Condition | Fires when |
|---|---|
| Goes down | The node's check crosses its failure threshold |
| Recovers | It answers again |
| Becomes degraded | A warning threshold is crossed |
| Warning cleared | That warning goes away |
| Certificate warning | A certificate is expiring or invalid |
| Response changed | A content watch saw the page differ |
| Affected by dependency | Its parent is down |
| Any status change | Any of the above transitions |
| Every failed run | Each failure, not just the threshold crossing |
| Every successful run | Each success |
| Latency above… | A run slower than the **latency threshold** you set |
| A metric above… | A named metric over the value you set — a hardware metric like `disk:/srv`, an SNMP OID, a recorded JSON value — regardless of the check's own thresholds |

**Only for** narrows a trigger to one check on the node instead of any of them,
and **Cooldown** stops it running constantly.

### Actions

Eight kinds:

| Action | Fields |
|---|---|
| **HTTP request** | URL, method (*auto* sends POST when there is a body), headers, body, expected status, timeout, ignore TLS errors |
| **Slack** | Webhook URL |
| **Microsoft Teams** | Webhook URL |
| **ntfy** | Server, topic, title, message, tags, priority (*min* to *urgent*) |
| **Pushover** | Application token, user key, title, message, priority (*lowest (-2)* to *emergency (2)*) |
| **Git command** | Repository directory, git command and arguments |
| **Custom code** | Interpreter (*sh*, *bash*, *PowerShell*, *cmd.exe*, *Python*, *Node.js*, or *Custom command…* where `{{file}}` is the script's path), the code, and a working directory |
| **Run a node now** | Which node's checks to run |

Every channel has a **test** button, the same pattern as the SMTP test.

### Placeholders

String fields accept `{{placeholders}}`:

`event` · `node.id` · `node.name` · `node.host` · `node.group` · `node.groups` ·
`node.tags` · `check.id` · `check.name` · `check.type` · `target` · `status` ·
`prev_status` · `message` · `error` · `success` · `latencyMs` · `lossPct` ·
`statusCode` · `failures` · `ts` · `instance` · `trigger.name` · `metric` ·
`metric.label` · `metric.value` · `metric.status` · `metrics.<key>` · `body` ·
`query.<name>` · `method` · `remote`

The editor lists them under **"Placeholders you can use"**, and clicking one
copies it. The last four are only meaningful on a custom endpoint: `body` and
`query.<name>` carry what the caller sent, and `method` and `remote` say how
and from where.

A metric key's awkward characters become underscores, so `disk:/srv` is
`{{metrics.disk__srv}}`. Scripts also receive all of them as `GWATCH_*`
environment variables — `node.name` becomes `GWATCH_NODE_NAME`.

### Custom endpoints

An endpoint is a trigger with the direction reversed: it exposes an action at
`/hook/<name>` so something else can poke GWatch. A router, a CI job, a
smart-home hub or a cron line can all call one.

- **URL name** decides the address.
- **Accepts** — which HTTP method, or any.
- **Access token** — required by default, and you should leave it that way.
  "Allow calls without a token" is offered, labelled *not recommended*, and
  GWatch puts a banner on the settings page listing any tokenless endpoints you
  have.

The whole request body arrives as `{{body}}` and query parameters as
`{{query.<name>}}`; `{{method}}` and `{{remote}}` say how the call was made and
where from. A **script** action also receives the raw request body on
**stdin**, which is the tidiest way to handle a JSON payload — pipe it straight
into `jq` or `json.load(sys.stdin)` instead of interpolating it.

> **Triggers and endpoints run on the GWatch machine with the service's
> permissions**, so treat who can create one as seriously as who can log in.
>
> Placeholders inside **script code** are handled for you: GWatch does not
> paste the value in as raw text, it substitutes a *safe reference* to it —
> `"${GWATCH_MESSAGE}"` in sh and bash, `${env:GWATCH_MESSAGE}` in PowerShell,
> a quoted string literal in Python and Node. A value can contain anything the
> sender chose, so it is never allowed to be read as code.
>
> The one exception is the **Custom command…** interpreter, where GWatch cannot
> know the language and so cannot know how to quote. There it *refuses* to save
> code containing placeholders until you tick the box acknowledging that
> `{{body}}`, `{{message}}` and `{{query.*}}` can contain anything the sender
> chooses. The `GWATCH_*` environment variables are available to every script
> and are the clearest thing to use.

Ready-made recipes for Home Assistant, Slack, Discord, ntfy, Pushover, Docker
and git are in [`RECIPES.md`](RECIPES.md).

---

## 13. Hardware health and the agent

GWatch reads its own computer's hardware with no setup at all. Any *other*
machine needs the small `gwatch-agent` binary.

### What is measured

Processor · memory · swap · load per core · each filesystem's space and inodes
· each network interface's traffic in and out · each disk's read, write and
busy time.

**Every reading stands on its own.** A machine is one hardware check, but each
of those readings has its own value, status, threshold, chart, incident and
trigger variable. Thresholds are set per **family** (`disk`, `cpu`, `memory`)
or per **instance** (`disk:/srv`), so the one filesystem that runs close to
full does not have to drag the others' thresholds with it.

Rates — anything per-second, and percentages derived from counters — are worked
out by the reporting machine from two of its own consecutive readings, never by
GWatch subtracting across the network. When there is nothing to compare
against they are simply absent, which is not the same as zero.

### Pairing a machine

The friendly path: **Nodes › Add node** with the *Machine with an agent*
template, or **Settings › Hardware › Pair a machine**. GWatch shows a short
**pairing code** — deliberately short enough to type off the screen, good for
one machine for a few minutes. On the other machine:

```sh
gwatch-agent pair --server http://<gwatch>:7230 --code XXXX-XXXX --name "Media server"
```

That exchanges the code for the machine's own token, saves it, and sends one
reading to prove it works. Then install it as a service:

```sh
gwatch-agent install --server http://<gwatch>:7230 --code XXXX-XXXX --name "Media server"
```

### The agent's other commands

| Command | Does |
|---|---|
| `gwatch-agent print` | Prints this machine's reading as JSON and **contacts nothing**. Run this first if you want to see exactly what would be sent |
| `gwatch-agent once --server … --token …` | Sends a single reading and exits — the quickest way to prove a token works |
| `gwatch-agent run --server … --token … [--interval 60s]` | Sends a reading every interval until stopped |
| `gwatch-agent serve --token …` | The inverse: exposes readings for GWatch to *read*, instead of pushing them. This opens a port on that machine, so prefer `run` unless you need it |
| `gwatch-agent install / uninstall / start / stop / restart / status` | The background service |

`GWATCH_SERVER`, `GWATCH_AGENT_TOKEN`, `GWATCH_AGENT_CODE`,
`GWATCH_AGENT_INTERVAL`, `GWATCH_AGENT_LISTEN` and `GWATCH_AGENT_TOKEN_FILE`
set the same things from the environment.

### The trust model

The agent **only ever sends data out**. GWatch never connects back to it, holds
no credential for it, and cannot ask it to do anything. The token that machine
holds can do exactly one thing: submit that machine's readings. There is no
remote execution, no remote configuration and no fleet management.

A machine that goes quiet is reported as **down** — which is the point of
monitoring it. **Report down after no reading for** sets how quiet is too quiet.

Full reference, including what each platform can and cannot measure:
[`HARDWARE.md`](HARDWARE.md).

---

## 14. Wallboards

A **wallboard** is what a spare screen shows. It is a cousin of a dashboard
rather than the same thing: a dashboard is read at a desk by someone who came
looking for an answer, a wallboard is read across a room by someone who did
not. So it has its own panels and its own look.

You can have as many as you like. Eleven panels:

| Panel | Shows |
|---|---|
| **Headline** | The one sentence that matters, with a lit circle beside it |
| **Counts** | Up, degraded, down and unknown as large numerals |
| **Clock** | The time and date, optionally with seconds |
| **Needs attention** | Everything down or degraded right now, worst first |
| **Groups** | One tile per group, coloured by its worst check |
| **Nodes** | A grid of nodes, optionally filtered to one group or tag |
| **Trends** | Latency or availability for the checks you pick — leave them all unticked and GWatch picks what matters most |
| **Certificates** | Expiring soon or already invalid |
| **Maintenance** | Windows in force right now |
| **Service** | Whether GWatch itself is running and checking |
| **Message** | A fixed line of text for whoever walks past |

Each panel has a title, a width and a height in grid cells. The board itself
has **columns** (fewer on a portrait screen), **refresh every** (seconds — the
board reloads itself, nobody has to), **type size** (for a screen further away
than the one you laid it out on), a **footer strip**, and a **theme**:

| Theme | For |
|---|---|
| **Signal** | GWatch's own graphite, lit by the accent |
| **High contrast** | Black field, heavy type — a bright room or a far wall |
| **Midnight** | Deep blue, dimmed — a screen someone sleeps near |
| **Daylight** | Light field — a screen by a window |

### Putting one on a screen

Switch **projection** on for a board and GWatch gives you an address of the
form `http://<gwatch>:7230/wall?id=<id>&token=<token>`. Type that into the
browser on the television, tablet or old laptop, and it shows the board and
keeps itself up to date. **That screen never signs in and never has to** — the
token can read that one board and nothing else.

> Treat a no-sign-in address like a key to that board. It is shown only to an
> administrator, works only while projection is on, and **Change address**
> takes the old one back immediately. Do not forward it to the internet.

---

## 15. Settings, tab by tab

Seventeen tabs. Three of them — *Appearance*, *Monitor health* and *About* — are
visible to viewer accounts; the rest are administrator-only.

| Tab | What lives there |
|---|---|
| **General** | Instance name (shown in the wallboard and in alert emails), default interval and timeout for new checks, how many checks run at once, the minimum interval allowed, wallboard refresh, the global latency and packet-loss warnings, and the ping method |
| **Appearance** | Theme, accent colour, density, and the switch for contextual tips |
| **Indicators** | The rules behind the coloured circles in the header |
| **Users & access** | Accounts, roles, API keys, and the legacy shared access password |
| **Network access** | Whether other devices on your LAN may reach the interface, and the addresses they would use |
| **Alerts** | Email and SMTP, as in [section 10](#10-email-alerts) |
| **Automation** | Triggers and custom endpoints, as in [section 12](#12-automation-triggers-and-endpoints) |
| **Rules** | Notification rules, as in [section 11](#11-notification-rules) |
| **Hardware** | Registered machines, pairing codes, agent tokens, revoking and purging |
| **AI & MCP** | Setting up the optional MCP companion, and the downloadable agent skill |
| **Retention** | How long each resolution of history is kept, current storage, and where your files are |
| **Maintenance** | One-off and weekly recurring maintenance windows |
| **Backups** | Manual and scheduled encrypted backups, and restore |
| **Database** | SQLite, or your own PostgreSQL / MySQL server |
| **Updates** | Checking for and applying new releases |
| **Monitor health** | Whether GWatch itself is well |
| **About** | Version, licence, and the terms, privacy and disclaimer documents |

A few worth calling out:

### Retention

Recent data stays detailed; older data is summarised so the database never
grows without limit. `0` means keep forever.

| Setting | Default |
|---|---|
| Keep every result for | 30 days |
| Keep 5-minute summaries for | 180 days |
| Keep hourly summaries for | 730 days |
| Keep daily summaries for | forever |
| Keep events for | 730 days |
| Keep hardware readings for | 90 days |

The tab writes out in plain words what your numbers actually mean, shows the
current row counts, and has **Run retention now**. It also shows **where your
files are** — data directory, database, driver, key file and backups folder —
which is the answer to "where is my data" and is repeated on the Backups tab
for the same reason.

### Monitor health

GWatch watches itself: whether the service and the scheduler are running, the
last retention run, the last backup, recent internal errors and bounced alert
mail. The service-health line in the header links here, and the
**serviceHealth** indicator fires from it.

### AI & MCP

An optional, separate companion that lets an AI assistant *read* your
monitoring — and, only if you deliberately give it a read-write key, manage it.
The tab walks through making a read-only API key, installing `gwatch-mcp`,
copying a client configuration with this install's address already in it, and
checking it works. It also offers a downloadable **agent skill** that teaches
an assistant how to use GWatch well, and quietly notes when a newer one has
shipped. Nothing here is on by default. See [`mcp/README.md`](../mcp/README.md).

---

## 16. Accounts, API keys and remote access

### Roles

| Role | Can |
|---|---|
| **Administrator** | Everything |
| **Viewer** | See dashboards, charts, history, incidents and the audit log. Refused — with an explanation, not a silent failure — on every change |

GWatch will not let you remove or demote the last administrator. Passwords are
stored as argon2id hashes; session tokens and API keys only as sha256 digests.
None can be read back out of the database.

### API keys

For things that are not browsers, scoped **read-only** or **read-write**.
Either scope is *always* refused on settings, backups and restores, updates,
accounts, keys, the configuration export, the service log, and anything that
runs a trigger or an endpoint — so a read-write key is not an administrator.

Revoking is immediate, and the audit log keeps the key's name so you can still
read back what it did.

### The legacy shared access password

One password, no user name. It still works so nothing breaks on upgrade, but it
gives everyone the same powers and no name in the log. Leave it empty once you
have accounts.

### Reaching GWatch from elsewhere

By default the interface answers on this computer only. **Settings › Network
access** opens it to the rest of your LAN and lists the addresses a phone or
tablet can use — a firewall on the machine may still need to allow the port.
Starting the service with `--listen 0.0.0.0:7230` does the same thing.

**Require sign-in on this computer too** closes the loopback shortcut, so even
a browser on the GWatch machine has to sign in. It is refused while no
administrator account exists and ignored until one does, so it cannot lock you
out of a fresh install.

> **GWatch never becomes an internet-facing service on its own.** There is no
> hosted relay and no tunnel helper, and this is a decision rather than a gap.
> To reach it from outside, bring your own way in — a VPN such as Tailscale or
> WireGuard, or a reverse proxy with TLS you control — and use a viewer account
> or a read-only API key. **Never port-forward the HTTP port**: plain HTTP puts
> your password on the wire in the clear. [`REMOTE-ACCESS.md`](REMOTE-ACCESS.md)
> has the full argument and the recommended setups.

---

## 17. Backups, restore and moving machine

A backup is a single password-encrypted archive (Argon2id + AES-256-GCM) of
your configuration, optionally including the whole history and event timeline.

- **Manual backups** always work, whatever the settings below say, and are
  never pruned automatically.
- **Scheduled backups** run unattended on an interval (1–720 hours), keep the
  newest few (1–365), and **cannot be enabled without a password** — backups
  are always encrypted.
- Archives can be downloaded, deleted, and restored either by uploading a file
  or by picking one GWatch already has.

A backup does **not** need the `gwatch.key` file that sits beside the database.
It carries the settings in the clear inside an archive that is already
encrypted with your password — which is what makes restoring onto a different
machine work at all.

Restoring onto a fresh install is the supported way to move GWatch to another
computer or come back from a reinstall. The archive carries nodes, checks,
dashboards, wallboards, maintenance windows, triggers, endpoints, accounts, API
keys, registered agents and hardware readings. Step by step, with what does and
does not come across: [`RESTORE.md`](RESTORE.md).

### Updates

**Settings › Updates** checks GitHub for new releases, and can check
periodically (1–720 hours), include pre-releases, and offer a waiting update in
a dialog when you open the interface. Turning automatic checking off means
off: no periodic check, no check when the interface opens, no prompt. This is
the only part of GWatch that reaches the internet on its own.

Every release is signed, and an update whose signature does not verify is
**refused, not installed with a warning**.

---

## 18. Keyboard and accessibility

GWatch has **no single-key global shortcuts** — nothing happens because you
leant on a key while reading a dashboard. What it has is ordinary keyboard
behaviour that works everywhere:

| Key | Does |
|---|---|
| **Tab** from the top of the page | Reveals "Skip to content", which jumps past the sidebar |
| **Esc** | Closes the open dialog, menu or tip |
| **Tab** / **Shift+Tab** in a dialog | Cycles inside it. Focus cannot wander behind the dialog and returns where it started when it closes |
| **Enter** in a form | Submits it. In the Audit search box, runs the search |
| **Enter** or **Space** on a result row | Opens that result's detail |
| **Tab** in the script editor | Inserts two spaces, so code stays indentable. To leave the box, press **Esc** then **Tab** |
| **↑** / **↓** in a menu | Move between items; **Home** and **End** jump to the ends |
| **←** / **→** on a chart | Step the tooltip through its points |
| **←** / **→** on the wallboard tabs | Switch between boards |
| **Arrow keys** on a widget's grip | Move it one cell; with **Shift**, resize it |
| **Enter** / **Esc** in the tour | Next step, and skip |

Dialogs name themselves by their heading and make the page behind them inert.
Form errors are tied to their fields. Every chart offers **"View as table"**.
The focus ring survives forced-colours mode. An automated accessibility scan
runs over every page, in both themes, on every change to the code.

---

## 19. Lesser-known corners

A short list of things that exist but are easy to never find.

- **`?mock=1`** runs the whole interface against an in-browser fake backend.
  Nothing real is touched. Ideal for trying a wallboard layout, or for seeing
  what a feature looks like with data before you have any.
- **Node list filters live in the URL.** Bookmark a filtered view, or send it
  to someone.
- **"Record this value" on a JSON check** turns any API into a chart.
- **"Walk this device" on an SNMP check** lists what the device exposes, so you
  never have to guess an OID.
- **Per-check alert recipients.** A check can mail someone the global settings
  never mention.
- **Alert suppressed events.** Every alert you did *not* get is in the
  timeline, with the reason.
- **Notes on the timeline** put "rebooted the router" next to the outage.
- **Duplicate** on a node copies it, suffixed "(copy)" and disabled, so you can
  edit before it starts running.
- **Export chart as PNG**, and the three per-node CSV exports under **More
  actions**.
- **`gwatch-agent print`** shows exactly what a machine would send, and
  contacts nothing.
- **`gwatch-agent serve`** inverts the agent: GWatch reads from it, rather than
  it pushing. Use it only when pushing is not possible.
- **Custom endpoints** let something else poke GWatch at `/hook/<name>`.
- **`gwatch open`** opens the interface from a terminal; **`gwatch status`**
  says whether the service is running.
- **The data-paths block** on Retention and Backups answers "where is my data".
- **The status indicators are links.** Clicking one takes you to whatever it is
  complaining about, rather than leaving you to go and find it.
- **Placeholders are click-to-copy** in the trigger and endpoint editors.
- **An endpoint's script gets the request body on stdin**, so a JSON payload
  can go straight into `jq` without being interpolated.
- **"Run the actions once with sample values"** tests a notification rule
  without waiting for the real thing.
- **Change address** on a projected wallboard revokes the old link instantly.
- **`gwatch migrate-db`** copies a whole SQLite install onto a PostgreSQL or
  MySQL server in one command — see [`DATABASE.md`](DATABASE.md).

---

## 20. When something is wrong

| Symptom | Look at |
|---|---|
| **Ping checks fail with a permission error** | **Settings › General › Ping method** — switch to *System ping command*. In Docker, add `--cap-add NET_RAW`. See [`DOCKER.md`](DOCKER.md) |
| **Everything went down at once** | The parent node. Then **Incidents** filtered to *affected by parent* — dependency suppression may be doing its job |
| **No alerts arriving** | **Settings › Alerts**, then send the test. Then **Incidents › Alerts suppressed** for cooldowns, silences and maintenance windows |
| **Too many alerts** | Raise **failures before down**, raise the **cooldown**, or set a dependency so one outage reports once |
| **An SNMP check times out** | A wrong community string looks identical to an unreachable device. Re-check the community, then that SNMP is enabled on the device — [`SNMP.md`](SNMP.md) |
| **A machine's hardware readings stopped** | The agent. `gwatch-agent status` on that machine, then `gwatch-agent once` to test the token |
| **Discovery only finds Docker's network** | Bridge networking. Use host or macvlan — [`DOCKER.md`](DOCKER.md) |
| **A gap in the charts** | **Incidents › Monitoring gaps** — the computer was asleep or offline |
| **A chart is empty beyond 30 days** | Named-metric series are read from raw history only. **Settings › Retention** |
| **Locked out after enabling remote access** | The loopback shortcut still applies unless you also turned on "require sign-in on this computer too" |
| **GWatch itself seems unwell** | **Settings › Monitor health**, then **Audit › Service log** |

---

## Where to go next

| Document | Covers |
|---|---|
| [`API.md`](API.md) | The JSON API: every endpoint, authentication, roles |
| [`INSTALL.md`](INSTALL.md) | Installing, upgrading and uninstalling on Windows |
| [`DOCKER.md`](DOCKER.md) | Running in a container |
| [`DATABASE.md`](DATABASE.md) | PostgreSQL or MySQL instead of SQLite |
| [`HARDWARE.md`](HARDWARE.md) | Hardware health and the agent in full |
| [`SNMP.md`](SNMP.md) | Enabling SNMP on a device, choosing OIDs |
| [`RECIPES.md`](RECIPES.md) | Ready-made trigger and endpoint recipes |
| [`REMOTE-ACCESS.md`](REMOTE-ACCESS.md) | Reaching GWatch from outside, safely |
| [`RESTORE.md`](RESTORE.md) | Moving to a new machine |
| [`PRIVACY.md`](PRIVACY.md) | What is stored, where, and what never leaves |

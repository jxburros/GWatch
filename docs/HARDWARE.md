# Hardware health

GWatch watches whether things on your network answer. Hardware health answers
the next question: *is the machine itself in trouble?* A server that still
answers a ping while its disk is three hours from full is up, and it is also
about to ruin your evening.

Three kinds of machine can be read, and they differ in who connects to whom.

| Source | Who connects | What GWatch needs on that machine |
| --- | --- | --- |
| **This computer** | nobody | nothing — it is already reading itself |
| **A registered machine** | the machine → GWatch | `gwatch-agent`, and a token |
| **A metrics endpoint** | GWatch → the machine | `gwatch-agent serve`, a token, and an open port |

The middle row is the one to use unless you have a reason not to, and the rest
of this document explains why.

## This computer

Nothing to set up. GWatch reads the machine it runs on every minute and keeps
the readings alongside everything else, so there is a history whether or not you
ever add a check.

A machine is not a separate kind of thing in GWatch: it is a node with a
hardware check, and its readings are shown on that node. That is why there is no
hardware section in the sidebar — the machines of your network are in **Nodes**,
with everything else you watch.

To be *told* when something is wrong, add a check: **Nodes › Add node**, pick
the **This computer** template, or add a **Hardware health** check to any node
you already have. The thresholds it starts with are 90% processor, 90/97%
memory, 85/95% disk and 50% swap; change them to whatever "wrong" means for
this machine.

## Another machine, with the agent

`gwatch-agent` is a single file with no dependencies. It reads the machine it
runs on and posts the reading to GWatch every minute.

1. In GWatch: **Nodes › Pair a machine**. Give it a name. GWatch creates the
   node the machine will be watched as, carrying a hardware check that is
   completed and switched on the moment the machine actually pairs. (**Settings
   › Hardware › Register a machine** does the same thing with a token instead of
   a code, for a rollout you are scripting.)
2. Copy the install command it shows you. It already has this server's address
   and the new code or token in it.
3. Run it on the machine you want to watch:

   ```
   gwatch-agent install --server https://gwatch.lan:7230 --token gwa_…
   ```

   On Windows, from an Administrator prompt. The agent installs itself as a
   background service and starts reporting immediately.

4. Its readings appear on its node within a minute, and the hardware check
   there starts alerting on the thresholds it was created with. Change them to
   whatever "wrong" means for that machine.

Before installing anything, `gwatch-agent print` shows exactly what would be
sent, and contacts nothing. `gwatch-agent once --server … --token …` sends a
single reading and exits, which is the quickest way to prove a token works.

### What this does and does not give GWatch

The agent connects outwards and hangs up. Specifically:

- **GWatch never connects to the machine.** It has no address for it beyond
  whatever the reading arrived from, no credential for it, and no way to ask it
  for anything. Nothing needs to be opened on that machine or forwarded to it.
- **The token can do exactly one thing**: submit that one machine's readings.
  It is not an API key — it cannot read your nodes, your settings, or any other
  machine's readings, and every other route in GWatch refuses it.
- **A reading is filed under the machine that sent it**, whatever the payload
  claims. A token cannot write another machine's history.
- **Losing a token is small.** Someone holding it could send made-up readings
  for that one machine. Revoke it in **Settings › Hardware** and it stops
  working on the next request; past readings are kept so the record still says
  where they came from.

The install command contains the token, so it ends up in the service
registration on that machine and in your shell history. That is the same
exposure as the token itself, which is why the token is worth so little.

### Over the internet

The agent posts over whatever URL you give it, so put GWatch behind HTTPS if
the agent is not on your own network — a plain `http://` server hands the
reading, and the token, to anything on the path. See
[`REMOTE-ACCESS.md`](REMOTE-ACCESS.md).

With a self-signed certificate, add `--insecure`. The agent still uses TLS; it
just stops checking the certificate, which means someone able to intercept the
connection can read the readings and feed GWatch false ones. Nothing more,
since the token grants nothing else.

## A metrics endpoint, read by GWatch

If you would rather GWatch did the asking — because the machine cannot reach
GWatch, or because you already collect metrics centrally — run the agent in
serve mode:

```
gwatch-agent serve --token <any secret you choose> --listen 0.0.0.0:9713
```

Then add a **Hardware health** check with its source set to **A metrics
endpoint**, the URL `http://that-machine:9713/metrics`, and the same token.

This is the opposite trade: it opens a port on the machine being watched, and
whoever holds the token can read its hardware readings. `GET /metrics` is the
only route there is, and the token is compared in constant time, but a port is
a port. That is why pushing is the default.

The endpoint returns the reading as JSON, so anything that can serve the same
shape works here too — `gwatch-agent print` shows the format.

## What is measured

| | Linux | Windows | macOS |
| --- | --- | --- | --- |
| Processor utilisation | yes | yes | **no** — see below |
| Load average | yes | n/a | yes |
| Memory and swap | yes | yes | yes |
| Filesystem space and inodes | yes | space only | yes |
| Network throughput and errors | yes | yes | yes |
| Disk read/write throughput | yes | yes (needs administrator) | **no** |

macOS has no way to read processor utilisation without a native extension, so
the agent reports **load average per core** instead and says so in the
reading's warnings. The hardware check falls back to it, so a macOS machine is
still watched — set a load threshold rather than a processor one.

Windows disk throughput needs the process to be able to open
`\\.\PhysicalDriveN`, which the service account can and an ordinary user
usually cannot. Everything else works either way.

Anything a platform cannot report is recorded as a warning on the reading and
shown on the machine's page. It never fails the check on its own: knowing the
disk is full is useful even when the network counters are missing.

### Memory, honestly

"Used" excludes reclaimable cache. On Linux that means it is derived from
`MemAvailable`, so a machine with 30 GB of page cache does not read as full —
because it isn't. On macOS it is wired + active + compressed, which is what
Activity Monitor calls memory used.

### Disk space, honestly

Used and free are what `df` reports, which is not quite what `total` suggests:
free space excludes the root reservation, so the percentage reflects what an
ordinary process can still write rather than what physically remains.

## Checks, warnings and alerts

A hardware check crossing a **warning** threshold makes it degraded; crossing
the **critical** one makes it down. They are different events, so "the disk is
filling" and "the disk is full" do not arrive as the same alert twice. Alerts,
cooldowns, silencing and maintenance windows all work exactly as they do for
any other check.

### One check, many metrics

A machine is a node, and its hardware is one check on that node — not one
check per metric. That is deliberate: a machine either is or is not the thing
being watched, so it gets one row in **Nodes**, one status, and one place to
set "report down after no reading for". Splitting it into a processor check, a
memory check, a disk check and so on would multiply that bookkeeping without
buying anything, since every reading already arrives as a single snapshot of
the whole machine.

What is not shared is anything else. The check collects everything in one
go, but from there on **each metric is its own thing**:

- **Its own key.** `cpu`, `memory`, `swap` and `load` for the readings a
  machine has one of; `disk:/srv`, `inodes:/srv`, `net:eth0.rx`,
  `net:eth0.tx`, `diskio:sda.read`, `diskio:sda.write`, `diskio:sda.busy` for
  the ones it may have several of. The key is how the metric is named
  everywhere: in the last result, in charts, in triggers, in the API.
- **Its own verdict.** Every run judges every metric against the threshold
  that governs it and records the verdict (`up`, `degraded` or `down`) with
  the reading. The check's status is the worst of them — a disk at its
  warning level is a degraded check, a disk at its critical level is a down
  check — but *Inspect last result › Metrics* shows the whole table, so the
  memory that is fine is not hidden behind the disk that is not.
- **Its own thresholds.** The check carries a list of warning/critical pairs,
  one per metric family (Processor, Memory, Swap, Load per core, Disk space,
  Inodes, Network throughput, Disk I/O), edited as boxed groups in the check
  editor. Below them, *One disk, interface or device* adds an entry for a
  single instance — `disk:/srv`, `net:eth0` — which replaces the family's
  pair for that instance alone. A media disk that lives at 90% can have its
  own line while every other disk keeps the default; an entry with both boxes
  blank turns alerting off for that one instance. Leave a box blank to turn
  that level off; a family with nothing set is still measured and charted,
  just never alerted on.
- **Its own incidents.** A metric crossing its warning level opens a warning
  on the timeline naming that metric — *Disk /srv warning* — and it closes
  when *that* metric comes back, whatever the others are doing. Two disks
  over their lines are two incidents; the memory clearing does not close the
  disk's. The alert email lists every metric that is not within its
  thresholds, one row each. Going down (a critical level, or a machine that
  stops reporting) and recovering stay per check, as does the alert cooldown.
- **Its own chart.** The node page charts the hardware check's metrics by
  family — processor, memory and swap on one percentage axis, one line per
  filesystem, one per interface direction — from the check's own results, so
  the charted line is the very number the thresholds judged. Each line has a
  CSV export, and `GET /api/history?checkId=…&metric=disk:/srv` serves it.
  The machine panel above still draws the reading-level history charts; its
  meters take their colour from the check's verdicts once a check is watching
  the machine.
- **Its own trigger.** The `metric_over` condition fires when one named
  metric is above a number of the trigger's own; the `{{metric}}`,
  `{{metric.label}}`, `{{metric.value}}` and `{{metric.status}}`
  placeholders name the metric a warning is about, and `{{metrics.<key>}}`
  carries every reading — see [`RECIPES.md`](RECIPES.md#placeholders).
- **Its own bulk edit.** *Bulk edit › Hardware threshold* sets one metric's
  pair across every selected hardware check and leaves their other
  thresholds alone.

One check does not mean one number; it means one place to configure all of
them and one place to see whether the machine, as a whole, is fine.

**A machine that stops reporting is down.** That is the whole point of an agent
that pushes: silence is the signal. By default a check reports down after three
missed intervals (at least a minute); change it under "Report down after no
reading for". This is the one verdict that is the check's rather than any
metric's.

Use "Watch only these mount points" to name the filesystems you care about. A
named mount that is not currently mounted is ignored rather than failed: a
removable volume that is unplugged is not a hardware fault — and a warning it
had open is closed, since there is nothing left to warn about.

Checks saved before the per-metric list existed carried one flat pair per
family (`cpuWarnPct`, `diskCritPct` and so on). They keep working unchanged
— the flat fields are read with the same meaning — and are moved onto the
list the next time the check is saved.

## Storage and retention

Readings are stored per machine, not per check, so a machine's history is kept
whether or not a check happens to be pointed at it, and two checks can watch
different aspects of the same machine without doubling the data.

They are kept for 90 days by default — less than check results, because each
one is a whole snapshot rather than a single number. Change it under
**Settings › Retention**. Deleting a registered machine deletes its readings
with it; revoking only stops the token.

## Keeping agents up to date

A machine running the agent is usually a machine nobody logs into. So the agent
keeps itself current: every few hours it looks at the agent releases, and if
there is a newer one it installs it and restarts into it. **You install an agent
by hand once.**

It is deliberately the agent, and not GWatch, that decides this:

- The agent checks GitHub directly and verifies the download against signing
  keys built into the agent itself. Nothing unsigned, or signed by anyone else,
  is installed — there is no setting that relaxes that.
- **GWatch is not involved.** It is never asked what version a machine should
  run, is never given a way to push anything, and cannot make an agent install
  anything. A GWatch that someone else got into must not become a way onto every
  machine that reports to it, which is the same reason the agent only ever talks
  outwards.
- What GWatch does with the version each agent reports is *show* it to you, so
  you can see which machines are behind.

Before an agent replaces itself it makes the download prove twice over that it
works on that machine — that it runs and is the version it claims, and that it
can take a real reading the server accepts. Only then is anything swapped, and
the version it replaces is kept next to it:

```
gwatch-agent update           # take the newest release now
gwatch-agent update --check   # say whether there is one, install nothing
gwatch-agent rollback         # put back the version the last update replaced
```

`rollback` needs no network and no download: the previous executable is on the
machine. It is the repair for an agent that installed cleanly and then behaved
badly — and worth knowing before you need it, because by then the machine may
not be reporting.

To turn automatic updates off, install with `--auto-update=false` (or set
`GWATCH_AGENT_AUTO_UPDATE=off`) and run `gwatch-agent update` yourself. An agent
that cannot reach GitHub carries on reporting exactly as before; a failed update
check is not an error on the machine being watched.

Agent releases are tagged `agent-v…` and are separate from GWatch's own — see
[`RELEASING.md`](RELEASING.md#releasing-the-agent). An agent is never offered a
GWatch build, or the other way round.

Why it is built this way, including the option deliberately not taken —
GWatch pushing updates to agents — is in
[`AGENT-DECISIONS.md`](AGENT-DECISIONS.md).

## Building the agent

The agent is built from this repository for every platform it supports:

```
make agent-all      # dist/gwatch-agent-<os>-<arch>
make agent          # just this platform
```

Its version comes from `cmd/gwatch-agent/VERSION`, not the project's `VERSION`
file. It imports only the collector, the shared data types and the release
verifier — no database, no web interface, no GWatch credentials.

## Troubleshooting

**The machine never appears.** Run `gwatch-agent once --server … --token …` on
it by hand. A rejected token says so plainly; a connection failure names what
went wrong reaching the server.

**"the server rejected this token".** The token was revoked, the machine was
deleted, or it was mistyped. Register the machine again and use the new token.

**Readings arrive but figures are missing.** Check the machine's page for the
warnings the collector recorded — they name what this platform could not read.

**A machine shows as "not reporting".** The agent stopped, lost its route to
GWatch, or the service is not running. `gwatch-agent status` on that machine
says whether the service is up; its log says why it is not reporting.

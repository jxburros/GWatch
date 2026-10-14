<img src="../web/agent-logo.png" alt="" width="72" align="right">

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

Nothing to install. GWatch can read the machine it runs on by itself.

A machine is not a separate kind of thing in GWatch: it is a node with a
hardware check, and what the check measures is recorded like what any other
check measures — one result per run, in the same place, with the same charts,
retention and backups as a ping or a website. That is why there is no hardware
section in the sidebar or in Settings: the machines of your network are in
**Nodes**, with everything else you watch.

So there is a history of this computer once something is watching it. Add a
check: **Nodes › Add node**, pick the **This computer** template, or add a
**Hardware health** check to any node you already have. The thresholds it
starts with are 90% processor, 90/97% memory, 85/95% disk and 50% swap; change
them to whatever "wrong" means for this machine. (GWatch keeps this computer's
newest reading in memory so that a check always has something fresh to judge,
but nothing is stored until a check records it. Machines whose readings no check
watches have only that newest reading.)

How long the results are kept is **Settings › Retention › Keep hardware check
results for** (90 days by default). A hardware result is a whole snapshot of the
machine, so it has a retention of its own; every other check follows the
setting for individual results.

## Another machine, with the agent

`gwatch-agent` is a single file with no dependencies. It reads the machine it
runs on and posts the reading to GWatch every minute.

1. In GWatch: **Nodes › Add node › A machine with the GWatch agent**. Give it a
   name, and untick any kind of reading you do not want (everything is on to
   begin with, and it can be changed later). GWatch creates the node the machine
   will be watched as, carrying a hardware check that is completed and switched
   on the moment the machine actually pairs, and shows an eight-character
   **pairing code** with the command to run.
2. Install the agent on the machine you want to watch (below, or the Windows
   installer) and run the command GWatch showed:

   ```
   gwatch-agent install --server https://gwatch.lan:7230 --code XXXX-XXXX
   ```

   On Windows, from an Administrator prompt. The agent swaps the code for its
   own token, installs itself as a background service and starts reporting.
   The dialog in GWatch tells you when the machine has paired and again when its
   first reading has arrived.
3. Its readings appear on its node within a minute, and the hardware check
   there starts alerting on the thresholds it was created with. Change them to
   whatever "wrong" means for that machine.

A machine is not a separate thing in GWatch: it is a node, and everything about
its agent — the token, what it collects, whether it is up to date — is on that
node, in the **GWatch agent** card (administrators only). There is no hardware
section under Settings.

Before installing anything, `gwatch-agent print` shows exactly what would be
sent, and contacts nothing. `gwatch-agent once` sends a single reading and
exits, which is the quickest way to prove the settings work.

`gwatch-agent pair --server … --code …` on its own (without `install`) keeps
the token *and* the server address on the machine, so afterwards
`gwatch-agent run` needs no arguments at all. That is how the packages below
run it. (For a rollout you are scripting, `POST /api/agents` registers a machine
with a token instead of a code — see [`API.md`](API.md#agents).)

### The agent's configuration file

Everything about how a machine talks to GWatch is in one plain-text file,
`agent.json`, that you can open and edit:

| Where | Path |
| --- | --- |
| Windows | `%ProgramData%\GWatch\agent.json` |
| Linux, macOS | `~/.local/share/gwatch/agent.json` (`$XDG_DATA_HOME/gwatch/agent.json`) |
| `.deb`, `.rpm` and the container | `/var/lib/gwatch-agent/agent.json` |

`gwatch-agent config path` prints it. It is created when the machine is paired
or the service installed, with every setting in it so you can see what there is
to change:

```json
{
  "server": "https://gwatch.lan:7230",
  "token": "gwa_…",
  "name": "Living room NAS",
  "interval": "1m",
  "checkIn": "15m",
  "buffer": "6h",
  "autoUpdate": true,
  "insecure": false,
  "collect": {}
}
```

| Setting | Meaning |
| --- | --- |
| `server` | The address of your GWatch, with the port. |
| `token` | This machine's credential. It must match the one GWatch shows on the node. |
| `name` | What the machine calls itself in GWatch. Empty uses the computer's own name. |
| `interval` | How often a reading is sent (at least `10s`). |
| `checkIn` | How often the agent asks GWatch for its settings (see below). `1m` to `24h`. |
| `buffer` | How long readings are held in memory while GWatch cannot be reached, then sent oldest first. `0s` keeps nothing; at most `48h`. |
| `autoUpdate` | Whether the agent installs new signed releases by itself. |
| `insecure`, `certPin` | Accept a self-signed certificate, or pin the server's public key. These are about the server named above, and are dropped if the agent is pointed at a different one for a run. |
| `collect` | The machine's own limit on what it will report (below). |
| `listen`, `repo`, `hostRoot` | Serve mode's address, the repository updates are taken from, and the container host mount. Rarely needed. |

Change a value, then restart the agent (`gwatch-agent restart`, or restart the
machine) and it applies. Durations are written like `"90s"`, `"15m"`, `"6h"`.

**Precedence**, lowest to highest: built-in defaults, this file, environment
variables (`GWATCH_SERVER`, `GWATCH_AGENT_TOKEN`, …), command-line flags. Run
`gwatch-agent config show` to see every setting with the layer that decided it —
the answer to "I edited the file and nothing changed" — and
`gwatch-agent config validate` to check the file: a misspelt key is named, a bad
value is explained, and a JSON mistake is reported with its line. An agent
refuses to run on a file it cannot read rather than guess. The file holds the
token, so it is readable only by the account that paired the machine.

The installed service is registered with *no* settings of its own — just `run` —
so changing a setting is an edit and a restart, never a reinstall, and the token
is not left in a service definition any user of the machine can read. (A service
installed by an older agent still carries its settings on its command line, and
those still win over the file; `config show` says so. Run `gwatch-agent
uninstall` then `install` to move onto the file.)

Agents paired by an older version are migrated: the first time one starts it
writes `agent.json` from the old `agent-token` and `agent-settings.json`, which
are left where they were so a rollback still finds them.

### The token

The token is what ties a machine to its node. It is shown on the node
(**Show token**) so you can compare it with the `token` in the machine's
`agent.json`; GWatch keeps a copy sealed with its own key (`gwatch.key`) the same
way it keeps every other secret. An agent registered before that, or restored
from a backup, only has a digest in GWatch and its token cannot be shown until
it is rotated.

**Rotating** a token is on the same card, in two forms that answer two
different worries:

- **Rotate** — GWatch makes a new token and the old one keeps working until the
  new one is first used. The agent collects the new token the next time it asks
  for its settings, writes it into `agent.json` and switches over; its first
  request with the new token retires the old one. Nothing to copy and no gap in
  readings. It only works for an agent whose token lives in `agent.json`: one
  whose token comes from an environment variable or a flag would be given the old
  token back at its next restart, so it leaves the file alone and logs that you
  need to update it where the token comes from.
- **Replace now** — for a token that may have leaked. The old token stops
  working at once and nothing is sent to the machine; you put the new token in
  `agent.json` yourself and restart the agent. (The ordinary rotation hands the
  new token to whoever holds the old one, which is exactly what you do not want
  if the old one is in the wrong hands.)

### What an agent collects

A machine has far more to report than anyone wants to read — every bridge,
tunnel and container interface sits beside the one network card that matters.
What leaves a machine is chosen in two places, and the machine's owner always has
the last word:

- **On the machine**, `collect` in `agent.json` (or the control panel) is a
  *limit*: `{ "metrics": ["cpu", "memory", "disk", "net"], "interfaces": ["eth0"], "disks": [], "mounts": ["/", "/data"] }`.
  A list that is empty means "no limit". `metrics` are the kinds of reading
  (`cpu`, `load`, `memory`, `swap`, `disk`, `inodes`, `net`, `diskio`).
- **In GWatch**, the **What it collects** section of the node's agent card
  chooses *within* that limit, from checkboxes for the interfaces, disks and
  filesystems the machine says it has. It can narrow what a machine reports and
  never widen it. The choice reaches the agent the next time it asks for its
  settings, and the same choices can be made while adding a machine.

Where both sides name something, only what both allow is sent; where only one
does, that is used; where neither does it is automatic. Automatic means every
kind of reading, every disk and filesystem, and every interface that is not
obviously virtual — bridges, container veth pairs, tunnels, Windows pseudo
adapters and the like are left out until someone ticks them. A reading that is
not collected is not sent, so it disappears from the checks, charts and alerts
built on it. (Filesystem space and inodes travel together: choosing inodes keeps
the whole filesystem entry.)

### Asking GWatch for settings

The agent only ever dials out, and that has not changed. Every so often
(`checkIn`, 15 minutes by default) it also asks GWatch a small, fixed set of
questions and applies the answers itself: which readings you chose, when to ask
again, the newest agent release (a hint only — releases are still fetched and
verified from the release feed), and a replacement token while you are rotating
this one. It sends back an inventory of what it could collect, which is what the
checkboxes are drawn from. GWatch's answer has no field for a URL, a command, a
file or a version to install, so it cannot make the machine do anything it did
not choose to; the full list is in [`API.md`](API.md#asking-for-settings) and the
reasoning in [`AGENT-DECISIONS.md`](AGENT-DECISIONS.md#12-an-agent-asks-gwatch-for-a-small-fixed-set-of-things).

### When GWatch is unreachable

The agent keeps nothing on the machine beyond its configuration — but it does not
throw away the last few hours because the network was down for them. A reading
that cannot be delivered is held in memory (up to `buffer`, six hours by
default) and sent, oldest first, as soon as GWatch answers. History therefore
has a gap no longer than the outage, and often none. Held readings are not
written to disk: a restart during an outage loses them.

### The control panel

`gwatch-agent ui` opens a small page in your browser, in GWatch's own style, for
someone who would rather not edit a file: whether the agent is reporting and when
it last spoke to GWatch, every setting above in a form, the collection limit as
checkboxes for the interfaces and drives this machine actually has, the token
(**Show**, to compare it with the node), a test reading, an update check, and
start / stop / restart for the service. The file itself is there too, as an
editor that refuses a file the agent could not run with.

It is not part of the service. The service runs as root or SYSTEM, and a page it
hosted would let any user on the machine change what a root process does. The
panel is a short-lived process that runs as *you*, only on this machine's
loopback address, and closes itself after half an hour of not being used: it can
edit the configuration only if you could edit it yourself, and control the
service only if the operating system lets you. Where the files belong to root
(the Linux packages, the Windows service), run it as `sudo gwatch-agent ui` or
from an Administrator terminal; otherwise it opens read-only and says so.

What protects it: it listens on a loopback address and refuses any other host
name in the request, so a web page cannot reach it by rebinding a DNS name; it is
opened with a random secret in the address that becomes a cookie and is then
dropped from the address bar; anything that changes something needs a header a
page on another origin cannot set and a matching `Origin`; the token is never in
the page until you press **Show**. The service writes a small status file for it
to read, which holds no token and no readings.

### Installing from a package manager

The agent is also packaged for apt/dnf, Homebrew, winget and Docker. A packaged
agent is the same program with one difference: **the package manager keeps it
up to date, and the agent never replaces itself** — `gwatch-agent update` tells
you the package manager's command instead ([why](AGENT-DECISIONS.md#10-a-packaged-agent-belongs-to-its-package-manager-it-does-not-update-itself)).
That suits machines you already manage with packages. For a machine nobody logs
into, the plain binary above, which updates itself, is still the better fit.

> **Availability.** The packages are published by release jobs that are newer
> than the agent releases already out, so they exist from the first `agent-v…`
> release cut after them. `brew install` also needs the `jxburros/homebrew-tap`
> repository and `winget install` the listing in winget's catalogue, both set up
> once by the maintainers ([RELEASING.md](RELEASING.md#the-agent-packages)).
> Until then the commands below have nothing to install; check the latest agent
> release for `.deb`, `.rpm` and `gwatch-agent-winget-setup-*` files.

### Linux packages

A `.deb` and an `.rpm` for amd64, arm64 and armhf (32-bit Raspberry Pi OS)
are attached to each agent release, next to the binaries. They install
`/usr/bin/gwatch-agent`, a `gwatch-agent` systemd unit, and
`/etc/gwatch-agent/agent.env`; the unit is enabled but not started, because
there is nothing to report with yet. Then pair the machine, once:

```
sudo apt install ./gwatch-agent_<version>-1_amd64.deb      # or: sudo dnf install ./gwatch-agent-<version>-1.x86_64.rpm
sudo gwatch-agent pair --server https://gwatch.lan:7230 --code XXXX-XXXX
```

`pair` keeps the token and the server in `/var/lib/gwatch-agent` (root only)
and starts the service; that is the whole setup. To set a machine up from a
script with a token instead of a code, put `GWATCH_SERVER` and
`GWATCH_AGENT_TOKEN` in `/etc/gwatch-agent/agent.env` and
`sudo systemctl restart gwatch-agent`. Manage it with `systemctl` and
`journalctl -u gwatch-agent`; the agent's own `install`, `start`, `stop` and
`status` point there rather than registering a second service.

The service runs as root, heavily sandboxed: it can read the whole machine but
write only its own `/var/lib/gwatch-agent`, and its only capability is the one
for reaching mount points only root can enter. An unpaired agent exits with
status 78, which the unit does not restart.

These are release files, not an apt or dnf repository, so `apt upgrade` does
not know about new ones: to update, install the next release's package the
same way. `sudo apt purge gwatch-agent` also deletes the stored token; a plain
remove keeps it, so reinstalling does not mean pairing again.

### macOS: Homebrew

```
brew install jxburros/tap/gwatch-agent
gwatch-agent pair --server https://gwatch.lan:7230 --code XXXX-XXXX
brew services start gwatch-agent
```

Pair and start the service as the same user — the token is kept in that
user's `~/.local/share/gwatch`. `brew upgrade gwatch-agent` updates it. The
formula works on Homebrew for Linux too.

### Windows: winget

```
winget install GWatch.Agent --override "/VERYSILENT /SUPPRESSMSGBOXES /SERVER=https://gwatch.lan:7230 /CODE=XXXX-XXXX"
```

This is the same setup program as the download, run silently with the server
and code it would otherwise ask for (the switches are listed in
[`INSTALL.md`](INSTALL.md#installing-the-agent-on-other-machines)). A plain
`winget install GWatch.Agent` installs the files without pairing; finish from
an Administrator prompt with
`"%ProgramFiles%\GWatch Agent\gwatch-agent.exe" install --server … --code …`.
`winget upgrade GWatch.Agent` updates it and keeps the pairing.

### In a container

`ghcr.io/jxburros/gwatch-agent` watches the **Docker host** it runs on — not
the container, and not the other containers. It needs the host's root mounted
read-only at `/host` and the host's network:

```yaml
services:
  gwatch-agent:
    image: ghcr.io/jxburros/gwatch-agent:latest
    network_mode: host
    volumes:
      - /:/host:ro,rslave
      - gwatch-agent:/var/lib/gwatch-agent
    environment:
      GWATCH_SERVER: https://gwatch.lan:7230
    restart: unless-stopped
volumes:
  gwatch-agent:
```

Pair once, then start it:

```
docker compose run --rm gwatch-agent pair --code XXXX-XXXX
docker compose up -d
```

(or set `GWATCH_AGENT_TOKEN` in `environment` instead of pairing). With `/host`
mounted, the agent reads the host's mount table, filesystems, name and
distribution through it; the processor, memory and disk counters are the
host's anyway, and `network_mode: host` makes the interfaces the host's too.
Without the mount it refuses to start rather than report the container as
though it were the host. `rslave` lets filesystems mounted after the container
started show up; drop it if Docker refuses it on your host. No `--privileged`
or `--pid=host` is needed.

It runs as an unprivileged user. A filesystem mounted under a directory only
root can enter cannot be measured that way and shows as a warning on the
reading; add `user: "0"` if that one matters. It is updated by pulling a newer
image, never by itself. Why this works, and what it cannot see, is
[`AGENT-DECISIONS.md`](AGENT-DECISIONS.md#11-the-container-image-watches-the-docker-host-through-the-hosts-own-root).

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
  for that one machine. Revoke it on the machine's node, or **Replace now** to
  give it a new one, and the old one stops working on the next request; past
  readings are kept so the record still says where they came from.

The token lives in `agent.json`, readable only by the account that paired the
machine. It is no longer left on the service's command line, where any user of
the machine could read it from the service manager. A pairing code, which is what
you type, is worth almost nothing once used.

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

An agent installed from a package ([above](#installing-from-a-package-manager))
is the exception: its package manager owns the binary, so it never updates
itself, whatever these settings say. `gwatch-agent update --check` still says
whether a newer release exists; `gwatch-agent update` answers with the package
manager's command (`brew upgrade gwatch-agent`, `winget upgrade GWatch.Agent`,
the next `.deb` or `.rpm`, or `docker pull`), and `gwatch-agent version` says
which package it came from.

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
make agent-packages # the .deb and .rpm, into dist/packages
make agent-image    # the container image, for this platform
```

The packaged builds differ from these by one linker flag,
`-X main.packagedBy=…`, set by `packaging/build-agent.sh`; everything under
`packaging/` is how each package is put together.

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

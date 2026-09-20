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

**A machine that stops reporting is down.** That is the whole point of an agent
that pushes: silence is the signal. By default a check reports down after three
missed intervals (at least a minute); change it under "Report down after no
reading for".

Set a threshold to 0 to ignore that reading entirely — a check that only
watches disk space is a perfectly good check.

Use "Watch only these mount points" to name the filesystems you care about. A
named mount that is not currently mounted is ignored rather than failed: a
removable volume that is unplugged is not a hardware fault.

## Storage and retention

Readings are stored per machine, not per check, so a machine's history is kept
whether or not a check happens to be pointed at it, and two checks can watch
different aspects of the same machine without doubling the data.

They are kept for 90 days by default — less than check results, because each
one is a whole snapshot rather than a single number. Change it under
**Settings › Retention**. Deleting a registered machine deletes its readings
with it; revoking only stops the token.

## Building the agent

The agent is built from this repository for every platform it supports:

```
make agent-all      # dist/gwatch-agent-<os>-<arch>
make agent          # just this platform
```

It imports only the collector and the shared data types — no database, no web
interface, no GWatch credentials.

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

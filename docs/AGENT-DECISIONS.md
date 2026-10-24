# Agent: decisions on record

Why the hardware agent is built the way it is. These were settled deliberately,
and several of them close off options that look obvious from the outside — so
they are written down, with the reasoning, rather than left to be rediscovered
or quietly reversed.

Dated 2026-09-21; entries 10 and 11 added 2026-09-27; entries 12 to 15 and the update to 6 added 2026-10-14; further updates to 6 (the log, elevation, the Windows tray) 2026-10-23; entry 16 (remote collectors) and its row in 12 added 2026-10-23. Each entry says what was
decided, what was rejected, and what would have to change for the decision to
be worth revisiting.

---

## 1. An agent updates itself. GWatch never updates an agent.

**Decided.** The agent checks its own release feed, verifies the download
against ed25519 keys built into the agent binary, and installs it. Automatic
updates are **on by default**.

**Rejected: GWatch pushing updates to agents.** It is the obvious feature —
one screen, a fleet, a button — and it is the one shape this must never take.
The agent's entire security story is that it talks outwards and nothing can
reach in: GWatch is given no credential for the machine and no way to ask it to
do anything. A push channel would invert that and make every agent a remote
code execution target for anything that got into GWatch, which is a far softer
target than the machines it watches.

**Also rejected, for now: GWatch as a hint about what to *run*** — the agent
asking GWatch which version it *should* run, then still fetching and verifying
independently. This is defensible (the worst a compromised GWatch could do is pin
agents to an older *signed* version) and it would allow coordinating a fleet from
one screen. It is not built because nothing yet needs it. (Since entry 12 the
agent does ask GWatch a few other small questions, and one of the answers is the
newest release *number*; that is only said in the agent's log, and changes
nothing about what it installs.)

**On by default** follows from the goal: "you install an agent by hand once"
only holds if updating is the default rather than something to discover. The
cost is that agents install signed code unattended, which is stated plainly in
`docs/HARDWARE.md` and can be turned off per machine.

**Revisit if:** someone needs staged rollouts across a fleet — that is the case
for the "GWatch as a hint" posture, and the point at which its trade becomes
worth making.

## 2. GWatch's role is visibility, not control.

GWatch already stores the version each agent reports. It now shows it: a count
above the machines table, a mark on each row and on the machine's own panel.

There is no "update this machine" button, and its absence is the feature. The
wording says so too — "agents take it by themselves; there is nothing to do
here" — because an unexplained mark reads as a chore.

## 3. Verify before the swap, not after.

An agent proves a download twice **before** replacing anything: it must run on
that machine and report the version the release claims, and it must take a real
reading that the server accepts. Only then is the running binary replaced, and
what it replaces is kept alongside as `.old`.

**Known gap, accepted deliberately.** There is no automatic rollback *after*
the restart. A binary that passes both proofs and then fails only as a service
needs a watchdog to notice, and a watchdog is a second thing on the machine
that can itself fail — on a machine nobody logs into, that is a worse trade
than the risk it covers. The mitigations are the pre-swap proofs, an immediate
rollback if the swapped binary will not run at all, and `gwatch-agent rollback`
needing neither network nor download.

The practical consequence: **an agent release is the one release worth being
slow about.** Tag it, let it reach your own machines, look at them.

**Revisit if:** an agent release ever does reach machines broken. The watchdog
becomes worth its cost the first time this actually happens.

## 4. The agent versions and releases on its own.

`cmd/gwatch-agent/VERSION`, tags `agent-v…`, its own GitHub release.

The agent's version used to be whatever GWatch tag built it. That coupling ran
both ways and both ways were bad: an agent fix needed a GWatch release, and a
GWatch release restarted every agent on the site. A thing that updates itself
also needs releases it can be pointed at that carry nothing else.

**The two trains must never cross.** GWatch reads `v…` tags and
`gwatch-<os>-<arch>` assets; the agent reads `agent-v…` and
`gwatch-agent-<os>-<arch>`. Neither can be handed the other's build — an agent
that installed a GWatch server over itself would take out the machine's
monitoring and the means of repairing it in one move. The separation is by tag
prefix *and* exact asset name, and it is tested from both sides
(`TestReleaseFamiliesStayApart`, `TestCheckUpdateOnlySeesAgentReleases`).
Anything else added to a release has to keep that true.

**Consequence accepted:** the agent is a separate download from a separate
release, which is one more thing to explain in `docs/INSTALL.md`. Worth it.

## 5. Tests came first.

Written before the self-update, not after. Self-update is the only feature here
that cannot be fixed remotely: a bad one is unrecoverable on every machine at
once. The tests are mostly about refusing — what must never be installed, and
what must be put back when an install goes badly.

## 6. The agent gets a local control panel, not a tray icon.

**Decided, and built** (issue #64): `gwatch-agent ui` serves a small page on the
loopback interface, styled like GWatch, for someone who does not want a
terminal. What it is collecting and sending, its settings, its token, and
start/stop/restart.

**It is a separate, short-lived process run as the person at the keyboard, not
a page hosted by the service.** The first version of this decision said the
agent "serves" the panel, which is the obvious reading and the wrong one: the
service runs as root or SYSTEM, and a page it hosted — however well guarded —
would be a way for any user on the machine, or any web page that reaches
loopback, to change what a root process does, including pointing it at a
different server with the token in hand. As its own process the panel has
exactly the rights of whoever started it: it can edit `agent.json` only if they
could edit it themselves, and control the service only if the operating system
lets them. On a machine whose files belong to root that means `sudo
gwatch-agent ui`, which is the right friction, and it opens read-only otherwise.
It stops itself after half an hour of not being used.

**How it commands the service:** by asking the service manager to start, stop
or restart it and letting the OS say no. The alternative — a local control
socket the service listens on — is smoother and is the first inbound path on a
machine whose whole point is that it has none. Not worth it until the prompts
prove annoying.

**How it knows what the service is doing:** the service writes a small status
file (last report, last check-in, readings held, what the machine could
collect) after each report; the panel reads it. It holds no token and no
readings, and it is the only thing the two processes share besides the
configuration file. (Since 2026-10-23 the service also keeps its own log there,
`agent.log`, so the panel can show the end of it: owner-only, capped at 1 MiB
plus one older file, tokens removed as it is written. It is the one thing the
agent now writes besides its configuration and the status file, and it is
there because a Windows service's log otherwise goes nowhere at all.)

**Asking for rights (2026-10-23).** Opened from a Start menu, an application
menu or the tray, `sudo` is no answer, so the panel uses the desktop's own
elevation prompt (UAC, polkit's `pkexec`, the macOS administrator dialog) in two
places only: a service button the service manager refused, and opening the
panel over settings this account cannot read. In the second case the elevated
copy serves the panel and the unelevated one opens the browser: the elevated
copy writes the one-time page into a private folder the unelevated copy made
and passed by path, so the code is still never on a command line and no
browser runs as root. Declining the prompt leaves things as they were, with the
same "use sudo" advice.

**What guards it:** loopback only; a Host check against DNS rebinding; a random
one-time code in the address, traded for a separate cookie and dropped from the
address bar, and never put on a command line (the browser is opened on a private
temporary page that leads on to the panel, since another user can read the
opener's command line and a panel run under sudo is worth reading);
a custom header and an Origin check on anything that changes something; a
strict content-security policy; the token absent from the page until someone
presses Show (the file editor is shown a mask where it is).

**Why a loopback page and not a native window:** the agent is a seven-platform,
pure-Go, `CGO_ENABLED=0` cross-compile from one Linux runner. Fyne and Wails
drag in cgo and break that for every target. A served page stays pure Go, works
the same on all three platforms, and reuses the interface GWatch already has —
its fonts and mark are embedded from `web/`, not copied.

**The tray is deferred** (issue #53), and dropping it from this round removed
the single worst constraint on the plan: the cgo requirement, the per-desktop
Linux tray mess (GNOME needs an extension), the login-autostart registration in
the installer, and a second binary to sign and release. A tray is passive where
a panel is active, but GWatch already covers that — an agent that stops
reporting makes its machine unhealthy on the dashboard and raises the alert.

**Revisit if:** the panel ships and people want it glanceable. By then the tray
is a thin shim over an interface that exists, buildable on the platforms where
it is easy.

**Revisited for Windows (2026-10-23, #53).** `gwatch-agent tray` is that shim,
on the one platform where it is easy: pure Go over user32 and shell32, no cgo,
no second binary, off by default, started at sign-in only if the setup
program's box is ticked (a per-user Run value the uninstaller removes). It
shows the service's state and the status file and opens the panel; it can do
nothing the panel cannot. To let it read the status file unelevated, the
service gives that one file an access list readable by signed-in users; the
folder, the configuration and the token stay the administrators'. macOS and
Linux stay deferred for the reasons above, which have not changed.

## 7. Network throughput will be shown in bits, with bytes alongside.

**Decided** (not yet built — issue #67). Network is conventionally bits per
second, decimal: a saturated gigabit link should read as something a person can
compare to the "1 Gbps" on their router, not as `119 MB/s`. Disks and memory
stay bytes.

Both units, in two senses — the byte figure alongside the bit one where there
is room, *and* one setting to flip which is primary. One setting, not a knob
per surface.

Storage, the API and the JSON stay in `B/s`. The conversion happens at the
display and parse edges only, so there is no migration and no API break.

Two things travel with it: `bytes()` divides by 1024 and labels the result
`KB`/`MB`, which is wrong either way (`KiB`, or divide by 1000); and network
thresholds are typed in raw bytes per second, so alerting at 800 Mbps means
entering `100000000`. That last one is the part that is genuinely "raw", and
the fix is a unit-aware input that accepts `800 Mbps`, `100 MB/s` or a bare
number — bare keeping today's meaning, so no existing configuration changes.

## 8. Pairing is confirmed where the person is looking.

**Decided** (not yet built — issue #69). The agent's own output is already
fine. The gap is the browser: the pairing dialog counts down and says "nothing
is enrolled until the code is typed in", and then never changes when the
machine actually enrols. The fix is to poll while the dialog is open and turn
it into a success state naming the machine. The Windows installer has the same
silence and gets the same treatment.

## 9. `--insecure` stays as it is.

Certificate pinning was proposed — the pairing dialog knows the server's
certificate and could print `--pin sha256:…`, which is the same convenience
without disabling verification permanently on a token-bearing client — and was
**declined**. Recorded so it is not re-proposed as though it were an oversight.

## 10. A packaged agent belongs to its package manager. It does not update itself.

**Decided** (issue #77). The agent now also ships as a `.deb`, an `.rpm`, a
Homebrew formula, a winget package and a container image. The binary in each of
them is built with `-ldflags "-X main.packagedBy=<deb|rpm|homebrew|winget|docker>"`
(`packaging/build-agent.sh`), and a binary built that way:

- never starts the automatic-update loop, whatever `--auto-update` or
  `GWATCH_AGENT_AUTO_UPDATE` say, and logs once at start that its package
  manager keeps it current;
- answers `gwatch-agent update` with the package manager's own command
  (`brew upgrade gwatch-agent`, `winget upgrade GWatch.Agent`, the exact `.deb`
  or `.rpm` to install, `docker pull …`) and exits non-zero, having downloaded
  nothing;
- still answers `gwatch-agent update --check`, from the same release feed as
  every other agent — a packaged agent is not blind to releases, it just does
  not act on them;
- refuses `rollback`, and — where the package registers the service itself
  (`.deb`, `.rpm`, Homebrew, the container) — refuses `install`, `uninstall`,
  `start`, `stop`, `restart` and `status`, pointing at `systemctl`,
  `brew services` or `docker` instead of registering a second service beside
  the package's;
- says what it is in `gwatch-agent version` (`…; deb package)`) and in the
  `User-Agent` it sends GWatch (`gwatch-agent/0.5.0 (deb)`).

The refusal is at the top of `applyUpdate`, the one function every install
path goes through, as well as in the command and the loop, so it holds if
either of those is ever changed. Any non-empty `packagedBy` counts, including
one the agent does not recognise: a typo in a packaging job fails towards "the
package manager owns this". The release binaries (`gwatch-agent-<os>-<arch>`)
and the ordinary Windows setup program are unchanged and keep updating
themselves, which is still the right default for a machine nobody logs into
(entry 1).

**Rejected: keep self-update and treat the package as a bootstrapper.** A
package manager believes it owns what it installed, and an agent that replaced
`/usr/bin/gwatch-agent` under apt would make that belief false in ways that
surface later and elsewhere: `dpkg` records one binary while another runs,
`debsums` reports the file as modified, the next package upgrade silently
reverts the agent's own update, and the `.old` the swap leaves is a file no
package owns. Homebrew's keg is a versioned directory an agent would be
rewriting from inside; a winget install record would name a version that is
no longer there; an agent inside an image replaces a file the next
`docker pull` throws away. The packaged systemd unit also makes `/usr`
read-only to the agent (`ProtectSystem=strict`), so on the Linux packages a
self-update could not work even if it were attempted.

**Rejected: turn it off with `--auto-update=false` in the unit file only.**
It says the same thing, but a flag can be edited, overridden by an environment
variable, or forgotten in one of five packaging formats; a binary that cannot
swap itself cannot be talked into it. The unit's `/usr` being read-only is kept
as a second, independent reason, not as the mechanism.

**Security is unchanged by any of this.** Packaging never loosens what an
update may be: a packaged binary carries the same pinned keys, installs nothing
itself, and GWatch still cannot make any agent install anything. The packaging
CI jobs are deliberately *not* given `GWATCH_SIGNING_KEY` — the packages'
integrity is the sha256 their package manager pins, not the ed25519 chain — so
a compromised packaging tool could publish a bad package, which its manager's
checks and a person can catch, but could not sign an update that every
self-updating agent would install on its own.

**Consequence accepted: the `.deb` and `.rpm` do not update unattended.** They
are release assets, not an apt or dnf repository, so nothing tells apt there is
a newer one; updating means installing the next file, and GWatch's "behind"
mark (entry 2) is how you notice. For a Linux machine nobody logs into, the
release binary with `gwatch-agent install` is still the better choice, and
`docs/HARDWARE.md` says so. Homebrew and winget do have an update channel, but
it runs when someone runs it (or schedules `brew upgrade` / `winget upgrade
--all`).

**Revisit if:** a signed apt/dnf repository is set up — then
`unattended-upgrades` and `dnf-automatic` give the Linux packages unattended
updates through the package manager, and the gap above closes — or if packaged
agents are found to fall behind in practice.

## 11. The container image watches the Docker host, through the host's own root.

**Decided** (issue #77). `ghcr.io/jxburros/gwatch-agent` is for watching the
machine a Docker host *is*, from a container on it. It was worth checking
whether that is coherent before shipping it, because the collector does not use
gopsutil (so the `HOST_PROC` convention does not apply) — it reads procfs and
`statfs` itself, and a container changes some of what those return and not the
rest:

| What | Source | From inside a container |
| --- | --- | --- |
| Processor, load, memory, swap, uptime, disk I/O | `/proc/stat`, `loadavg`, `meminfo`, `uptime`, `diskstats` | the **host's** — not namespaced |
| Filesystems | `/proc/mounts` + `statfs` | the **container's** overlay and volumes |
| Network interfaces | `/proc/net/dev` + netlink | the **container's** veth, unless `--network=host` |
| Distribution, hostname | `/etc/os-release`, `/etc/hostname` | the **image's** Alpine and a random ID |

So the image runs with the host's `/` mounted read-only at `/host` and
`GWATCH_HOST_ROOT=/host` (also `--host-root`), which moves the collector's
`/proc`, `/sys` and `/etc` under it and reads the mount table from
`/host/proc/1/mounts` — the host init's, since `/proc/mounts` is a link to the
*reader's* mount namespace — measuring each filesystem through `/host` and
reporting it under the host's own path (`internal/sysmetrics.SetHostRoot`).
With `--network=host` as well, every figure is the host's; CI checks this by
running the image on its runner and comparing the name and distribution.

It needs neither `--pid=host` nor `--privileged`: `/proc/1/mounts` of the
host's procfs is world-readable, whereas `/proc/1/root` would have needed
`CAP_SYS_PTRACE`. It runs as an unprivileged user, because everything it reads
is world-readable; a filesystem mounted under a directory only root can enter
is the one thing it then cannot measure, and it says so in the reading's
warnings (`--user 0` fixes it).

If `GWATCH_HOST_ROOT` is set and the host is not mounted there, the agent
refuses to start. Falling back to the container's own view would report a
machine that does not exist under the name of one that does.

**Rejected: an image for watching the container itself.** That is a different
product (cAdvisor's), and the readings above show why the agent's model does
not fit it: half of them would be the host's anyway.

**Revisit if:** someone needs the Docker *containers* on a host watched, rather
than the host — that is per-container cgroup accounting, a new collector rather
than a new mount.

## 12. An agent asks GWatch for a small, fixed set of things.

**Decided** (2026-10-14). Every so often an agent POSTs a check-in
(`/ingest/checkin`, authenticated by its token like a reading) and applies the
answer. The answer is a closed struct, `model.AgentSettings`, and this is all of
it:

| Field | What the agent does with it |
| --- | --- |
| `checkInSeconds` | Waits that long before asking again, clamped to one minute to one day; its own `checkIn` setting wins if it has one. |
| `collect` | Narrows which readings it sends, *inside* the limit in its own file. Nothing here can widen what the machine's owner allows. |
| `latestAgent` | Says in its log that a newer agent exists. It changes nothing: the agent still looks for releases on its own schedule, from the release feed, and verifies what it downloads. |
| `token` | Present only while an administrator is rotating this machine's token, and only to a caller that authenticated with the token being replaced. Taken up only if the agent's own file is where its token lives. |
| `collector` | Added by entry 16. Present only when an administrator has made this agent a remote collector *and* the check-in said the machine's own `agent.json` allows it: definitions of built-in network checks to run. Validated by the agent and ignored altogether by one whose file does not allow collecting. |

There is no field for a URL, a command, a script, a file to write or a version to
install, and a reply that carries anything else is read as the fields above and
nothing more. Adding such a field would change what an agent is; if it ever
seems worth doing it should be argued here first.

**Why this is not the push channel entry 1 rejected.** Entry 1's worry is a
GWatch that has been got into becoming a way onto every machine that reports to
it. The worst a compromised GWatch can do with this is: ask the agent to send
*fewer* readings; make it check in more or less often; and, during a rotation it
started, hand over a token the agent then uses to talk to that same GWatch.
None of that executes anything, and none of it can reach past what the machine's
own limit allows. The agent still opens every connection; GWatch still has no
address for it and no credential to it.

**Why not leave collection to the agent's file alone?** Because the person
watching a machine is usually not the person at it. "I plugged in a drive and
want it monitored" should be a checkbox where the drive's node is, not an SSH
session. The machine's owner keeps the last word through the limit in `agent.json`.

**Token rotation rides on it, with a deliberate exception.** The ordinary
rotation delivers the replacement to whoever holds the old token, which is
exactly wrong if the old token leaked; so there is a second form, *replace now*,
that retires the old token at once and delivers nothing over the network.

**Rejected: piggybacking on every reading.** Cheaper, but it would make "GWatch
may answer an agent" a property of the one route every agent hits every minute,
and it would couple the reading's interval to the settings'. A separate, rare
check-in keeps the reading path as dull as it was.

**Rejected: GWatch holding no copy of the token at all** (the original design:
shown once, digest only). It makes the one thing people need to do — compare the
token on a machine with the one GWatch expects — impossible. The copy is sealed
with the server's key like every other secret GWatch holds; a backup does not
carry it, so a restore never turns a backup file into a bag of readable
credentials. An agent registered before this has only a digest and gets a
readable token by being rotated.

**Revisit if:** a second kind of answer is wanted. Add it to `AgentSettings`
with the reasoning here, rather than reaching for a generic "directives" list.

## 13. The agent's settings are one editable file, and the service has none.

**Decided** (2026-10-14). `agent.json` holds the server, the token, the name,
the intervals, auto-update, certificate trust and the collection limit. The
service is registered with `run` and nothing else.

Before this, settings were flags baked into the service at install time, the
token among them. Changing one meant reinstalling; getting at the install screens
meant reinstalling; and the token sat on a command line any user of the machine
could read from the service manager.

**Precedence is defaults < file < environment < flags**, the usual order, and
`gwatch-agent config show` prints each setting with its layer, because the cost
of a layered system is "I edited the file and nothing changed". A service
installed by an older agent keeps its baked-in flags, which therefore still win;
the answer is to reinstall once, and `config show` says so.

**A file it cannot read stops the agent** (exit 78, which the packaged unit does
not restart on) rather than running on defaults: reporting a machine under the
wrong name to the wrong place without anyone knowing the file was ignored is
worse than not reporting. The service commands that read no setting
(`uninstall`, `stop`, `start`, `restart`, `status`) only warn about it, so a
broken file never stands between someone and removing the agent. An unknown key is a warning, not a refusal — a typo is
worth a line in the log, not a dead agent — and `config validate` fails on it.

**JSON, not XML.** The agent already wrote JSON, the standard library reads it,
and nobody needs a second parser for a dozen keys.

**`insecure` and `certPin` belong to the server in the file.** Pointing the
agent at a different server for one run drops them, or "accept any certificate"
would follow the token to a server it was never a decision about.

**A rotation is written back only to the file.** A token that comes from the
environment or a flag comes straight back at the next restart, after GWatch has
retired the copy the file now holds; so such an agent leaves the file alone and
says where the new token has to go.

**Rejected: live reload.** An edit applies on restart, which is what was asked
for and needs no watcher, signal or Windows equivalent of either.

**Held readings are kept in a small bounded file.** Up to `buffer` (six hours by
default) of readings are held while GWatch is unreachable and sent oldest first,
so an outage leaves a gap no longer than itself. *Revised* (issue #74): they were
memory only at first, on the grounds that the agent should store nothing beyond
its configuration; but the outages worth surviving are exactly the ones that
come with a power cut or a reboot, and a buffer that forgets everything then is
not much of one. So they are now kept in `agent-queue.jsonl` beside `agent.json`,
under three limits — the `buffer` age, 4,000 readings, about 8 MB — with the
oldest dropped first, because the watched machine must never be filled up by its
own agent. Nothing is written while GWatch answers; a held reading is appended
(one small write per interval through an outage) and the file is rewritten
atomically only when readings leave its front. GWatch accepts readings up to 48
hours old, which is also the ceiling on `buffer`. A late reading is marked
(`X-GWatch-Held`) and GWatch refuses one whose time is implausible rather than
filing it at arrival, which is what it still does for a live reading from a
machine with a wrong clock: yesterday's reading stamped "now" would be judged as
the machine's current state.

## 14. Automatic collection leaves out what looks like scaffolding.

**Decided** (2026-10-14). With nothing chosen, an agent reports every kind of
reading, every disk and filesystem, and every network interface that is up or
has carried traffic *and* does not look virtual: container veth pairs, bridges,
tunnels, Windows pseudo and Teredo/ISATAP adapters, hypervisor switches. Ninety
percent of an interface list on a Docker host or a Windows machine is these, and
none of it is what anyone sat down to watch.

They are **offered but not ticked** in the inventory, marked "hidden by default",
so one tick brings a WireGuard tunnel or a bridge that matters back. The test is
on the name, and it errs towards hiding; the cost of a miss is one checkbox, the
cost of the other mistake is a hundred lines of noise.

The selection is applied to a finished reading rather than steering the platform
collectors, because rates are derived from consecutive readings: a collector that
skipped an interface for a while would hand back a nonsense rate when it was
switched back on.

## 15. A machine's readings are its check's results, not a table of their own.

**Decided** (2026-10-14). A machine is a node with a hardware check, and what
that check measures is stored the way any check's measurements are: a row in
`results`, with its metrics by key and the whole snapshot in its details. The
separate `host_samples` table is gone.

**What was wrong.** Hardware was stored twice. Every run of a hardware check
already recorded a result carrying every metric and the full snapshot; the
agent's reading was *also* written, again in full, to a table keyed by machine
rather than by check, with its own retention, its own API, its own export and
restore code and its own charts. A second copy of the same history at the same
resolution, in a shape nothing else shares — the opposite of "a machine is just
a check".

**How it works now.**

- An agent's reading is judged **when it arrives** (`Engine.IngestHostReading`).
  A current reading goes through the check's whole machinery — state, incidents,
  alerts — as if the check had run and taken it. A reading the agent held back
  through an outage (entry 13) is added to the check's history at the time it
  was taken and does nothing else: the machine being back is the news, and an
  alert for a disk that was full yesterday is noise.
- The check's **schedule** is kept for one job: noticing that readings have
  stopped. While a fresh reading exists, a due run would only judge the same
  reading twice, so it is skipped; once readings stop, the run happens and
  fails with "no hardware reading for …", exactly as before.
- Results are recorded at about the check's interval (80% of it, to tolerate
  jitter), whatever the machine's own: one that reports every ten seconds does
  not turn a once-a-minute check into six results a minute.
- GWatch's newest reading of each machine is held in memory. After a restart it
  is read back out of the check's latest result that carries a snapshot.
- Retention: hardware checks keep their results for the **hardware** setting
  (`hostDays`, 90 days) and every other check for `rawDays`. A hardware result
  is a whole snapshot, which is why it has a retention of its own.
- The charts on a node read the series back out of those results, averaged into
  buckets as they are streamed so a year of them is never held in memory.

**What this costs, accepted.**

- **A machine no hardware check watches keeps only its newest reading, in
  memory.** An agent registered onto a node that never had a hardware check, or
  this computer's own readings when no "This computer" check exists, used to
  leave a history in the readings table. Now there is nowhere for it to go, and
  those readings are dropped by the migration. Adding a machine from Add node
  makes its check, and attaching one to an existing node adds one if there is
  none.
- **Deleting an agent no longer deletes readings**, because they are not the
  agent's: they are the node's history and go with the node.
- **A named metric is only as old as raw results.** A year-long chart of a
  hardware metric reaches back as far as `hostDays`, not a year; per-metric
  rollups would fix that for every named metric (SNMP and JSON included) and
  are the right next step, not a part of this one.
- **Old archives** carry a `hostsamples.jsonl`. It is not read: every reading in
  it was also a hardware check's result, which `results.jsonl` has.

**The migration** (schema version 4) is a `DROP TABLE`. SQLite databases are
copied aside before any numbered migration runs; for PostgreSQL and MySQL, take
the server-side backup `docs/DATABASE.md` already asks for.

**Rejected: keeping the table as a cache.** It would be the same second copy
under a kinder name, and the question to answer is "where is this machine's
history", which should have one answer.

**Rejected: running the check on its schedule *and* on arrival.** Two results a
minute for the same reading, or a rule for choosing between them.

**Rejected: judging only on the schedule** (the smallest change). A reading the
agent held back would never reach the history, which would make the held-back
readings of entry 13 a feature that keeps them in memory for nothing.

## 16. An agent may run GWatch's network checks from where it is, if both sides agree.

**Decided** (2026-10-23, issue #123). A paired agent can be a **remote
collector**: it runs the ping, TCP, HTTP (and keyword, JSON and certificate),
DNS and SNMP checks of the nodes an administrator points at it, on their
intervals, and posts the results to `/ingest/results`, where they go through
the engine's ordinary processing. It is how one GWatch watches a second site
it cannot reach. `docs/COLLECTORS.md` is the user's account; this is why it is
shaped the way it is.

This is the "second kind of answer" entry 12 said to argue for here first,
and it is the closest anything has come to entry 1's push channel, so the
argument is the whole point:

**Both sides must agree, and either can stop it.** An administrator switches
it on for the agent in GWatch; the machine's owner sets `"collector": true` in
its `agent.json`. The agent says at each check-in whether its file allows it,
and GWatch hands out checks only when both are true. An agent whose file does
not allow it has no runner at all and ignores anything a reply says about
checks. Entry 12's rule that the machine's owner has the last word on what
leaves the machine extends to what the machine is asked to do.

**What can be asked is data from a closed list, never code.** A definition is
an id, one of eight built-in check types, a target and the settings that type
reads (`model.CheckConfig.ForCollector`, an allow-list, with a test that fails
for any new field until someone decides which side it is on). The agent
checks each one again (`model.CollectorCheck.Validate`: a collectable type,
a target that is an address and nothing a shell or a file system would read,
sane numbers) and the runner it calls (`checks.RunCollected`) refuses any
other type a third time. A **custom script** check is not on the list and
never will be: running GWatch's commands on another machine is the remote
execution entry 1 rejects, and it would make every collector a way onto its
network for anything that got into GWatch.

**The worst a compromised GWatch can do with it** is make a machine that has
opted in open network connections it describes — ping an address, connect to
a port, make an HTTP request with headers it chooses, ask a DNS server, read
SNMP — on intervals of ten seconds or more, at most 500 checks, eight at a
time, and receive the results. That is a real capability: from inside the
site's network it is a scanner of that network, and an HTTP check can reach
an internal web interface. It is the same capability as the feature, which is
why it is opt-in on the machine and not merely in GWatch, and why the docs tell
an owner to choose the collector machine with that in mind. It still cannot
run anything on the collector, read its files, change its configuration or
reach it between check-ins.

**The agent still opens every connection.** The checks arrive as part of the
answer to the agent's own check-in (more often for a collector — every two
minutes — so a moved node starts soon); results leave on a route of their
own, authenticated by the same token, that can do one thing: file results for
the checks this agent is assigned *now*. A result for any other check is
refused one by one, so a stolen collector token cannot write into the history
of a node it was not given.

**Credentials go to the one collector that needs them, and stay in memory.**
An SNMP community and HTTP headers are part of a definition, because the check
cannot run without them. They are sent only to the collector a node is
assigned to, only once both switches are on, only over the agent's TLS
connection, and the agent never writes them to disk (after a restart it runs
nothing until its first check-in). Results are spooled through an outage like
readings (entry 13, the same bounded file discipline, generic over what it
holds) because they carry nothing secret.

**A quiet collector is one incident, not N.** If a collector sends nothing —
results or a once-a-minute heartbeat — for three of its shortest interval
(at least three minutes), its checks become `unknown` with the reason, and one
`collector_unreachable` event and one alert are raised for it, the reasoning of
a parent dependency applied to whatever does the checking. A check that was
down before the silence is down again afterwards without a second alert.

**Rejected: a separate collector binary.** Another thing to sign, release,
package and pair; the agent already has pairing, a submit-only token, signed
self-update, packages, a spool and a service. The cost of reusing it is that
the agent now links the check runners, including the script runner's code,
which it can never call (the runner refuses the type before dispatch).

**Rejected: GWatch connecting to the collector** (a pull model, a tunnel, a
websocket held open from GWatch's side). Each needs the collector to accept
something inbound, which is the line entry 1 draws.

**Rejected: letting a collector run custom scripts "for administrators who
trust their sites".** The opt-in on the machine would make it defensible on
paper, and in practice it turns a compromised GWatch into code execution on
every opted-in machine. If it is ever wanted, it should be a script the
machine's owner puts on the machine and names in its own file — something
GWatch triggers, never something GWatch supplies — and argued here first.

**Rejected: storing the definitions on the collector** so it could run through
a restart while GWatch is unreachable. It would put credentials on disk at
every site; a restart during an outage costs that outage's results for the
moments until the next successful check-in, which is cheap by comparison.

**Revisit if:** collectors need to run while cut off from GWatch across
restarts (then the definitions want sealing with a key that never leaves the
machine), or someone needs a collector for a check type not on the list — add
the type to `CollectorCheckTypes` with its reasoning, never a generic escape
hatch.

---

## Corrections to earlier assumptions

Kept because they changed the shape of work still to come:

- **The Windows agent installer already supports silent installation.**
  `/VERYSILENT /SERVER=… /CODE=… /NAME=…` works today
  (`scripts/installer/gwatch-agent.iss`, `docs/INSTALL.md`). It had been listed
  as missing, and #63 is smaller than it looked: what is actually missing is
  verification before the wizard finishes, a result page that says what
  happened, and a non-Windows install story.
- **Revoking an agent already exists** (`DELETE /api/agents/{id}`, admin only).
  What does not exist is *rotation* — re-keying a machine without pairing it
  again — which is a smaller job than "revocation and rotation" implied.

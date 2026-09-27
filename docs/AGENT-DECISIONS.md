# Agent: decisions on record

Why the hardware agent is built the way it is. These were settled deliberately,
and several of them close off options that look obvious from the outside — so
they are written down, with the reasoning, rather than left to be rediscovered
or quietly reversed.

Dated 2026-09-21; entries 10 and 11 added 2026-09-27. Each entry says what was
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

**Also rejected, for now: GWatch as a hint** — the agent asking GWatch which
version it *should* run, then still fetching and verifying independently. This
is defensible (the worst a compromised GWatch could do is pin agents to an
older *signed* version) and it would allow coordinating a fleet from one
screen. It is not built because nothing yet needs it.

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

**Decided** (not yet built — issue #64): a small local page, served on the
loopback interface by the agent and styled like GWatch, for someone who does
not want a terminal. Start/stop, what it is collecting and sending, update and
the automatic-update setting, and enough to troubleshoot with.

**How it commands the service:** by shelling out to `gwatch-agent start/stop`
and letting the OS ask for elevation. The alternative — a local control socket
the service listens on — is smoother and is the first inbound path on a machine
whose whole point is that it has none. Not worth it until the prompts prove
annoying.

**Why a loopback page and not a native window:** the agent is a seven-platform,
pure-Go, `CGO_ENABLED=0` cross-compile from one Linux runner. Fyne and Wails
drag in cgo and break that for every target. A served page stays pure Go, works
the same on all three platforms, and reuses the interface GWatch already has.

**The tray is deferred** (issue #53), and dropping it from this round removed
the single worst constraint on the plan: the cgo requirement, the per-desktop
Linux tray mess (GNOME needs an extension), the login-autostart registration in
the installer, and a second binary to sign and release. A tray is passive where
a panel is active, but GWatch already covers that — an agent that stops
reporting makes its machine unhealthy on the dashboard and raises the alert.
The panel needs a front door instead: a start menu entry, a `.desktop` file,
and `gwatch-agent ui`.

**Revisit if:** the panel ships and people want it glanceable. By then the tray
is a thin shim over an interface that exists, buildable on the platforms where
it is easy.

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

# Changelog

Versions follow [semantic versioning](https://semver.org). GWatch is pre-1.0,
which is deliberate and carries its usual meaning: it works and it looks after
your data, but the interface and the JSON API can still change between releases.
1.0.0 is reserved for the first public, stable release.

The version a build reports comes from the [`VERSION`](VERSION) file, and a
release is published as `v<VERSION>` by the Release workflow, dispatched from
`main`. It refuses to publish a tag that disagrees with the file — see
[`docs/RELEASING.md`](docs/RELEASING.md).

## 0.8.4 (not yet released)

GWatch 0.8.4 and gwatch-agent 0.8.3 ship together: the agent reads the containers
and virtual machines below.

### Added

- **Watching containers and virtual machines.** A machine can now be told to watch
  named Docker (or Podman) containers and named Hyper-V guests, chosen where its
  services and programs are: on the agent card's **What it collects**, in
  `agent.json` (`"containers"`, `"vms"`), in the agent's control panel, or on the
  hardware panel of the computer GWatch runs on — each offering the machine's own
  list to pick from. Each container becomes readings of the hardware check:
  `docker:<name>` (running, not healthy, or stopped), `docker-restarts:<name>`
  (restarts in the last hour, which catches a crash loop), and processor and memory
  use against its own limit; each guest `vm:<name>` (running, paused or saved, or
  off) with its processor and memory use. New hardware checks are critical when a
  watched container stops or a guest is off, warn while one is unhealthy,
  restarting, paused or saved, warn at three restarts in an hour, and judge a
  container's memory like a machine's; older checks get the levels by adding them
  in their thresholds. Docker is read over its Unix socket with `GET` requests
  only; Hyper-V through WMI on the Windows host, with `Get-VM` as a fallback.
  Nothing is asked of either until a name is chosen. The access this needs — the
  Docker socket, the "Hyper-V Administrators" group — and what it implies is in
  [`docs/HARDWARE.md`](docs/HARDWARE.md#containers-and-virtual-machines), and running
  GWatch's own container with the socket in [`docs/DOCKER.md`](docs/DOCKER.md).

- **Node colours.** A node can have a colour of its own — in the node editor,
  on the network map's side panel, or for many at once in Bulk edit. It fills the
  node's box on the network map and its name in the node list, with the text in
  black or white, whichever reads on it; the status keeps its stripe, dot and
  border. It is shown nowhere else (`color` on a node in the API).
- **Pings per check.** *Settings › General › Pings per check* (1–20, 4 unless
  changed) is how many echo requests a ping check sends when it does not set
  its own; *Make every ping check use this* hands existing checks back to it,
  and Bulk edit can set or clear a check's own count (`general.pingCount`,
  bulk `config.pingCount`).
- **Discovery sets the check interval and dependency.** Beside *Put them in a
  group*, the results now offer *Check every* and *Depends on*, applied to every
  node added (`intervalSeconds` and `dependsOnNodeId` on
  `POST /api/discovery/{id}/add`).

### Changed

- **Network map clusters are rings.** In the *Clusters* layout every node is the
  middle of a ring of what depends on it, and each of those with dependents of
  its own starts a ring of its own, further out on a longer line — the router in
  the middle, its switches round it, each switch with its machines round it.
  The side facing a node's parent is left open so the line in stays clear, and a
  long run of machines goes on two or more rings. Each ring is worked out the
  way it is drawn, so boxes no longer need nudging apart afterwards.
- **Charts open blank.** Opening Charts no longer opens the first saved chart
  or plots anything until something is ticked; a saved chart is opened from the
  list.
- **Ping checks follow a setting for how many pings they send.** New ping checks,
  and those from the templates, no longer store a count of their own (it was
  always 4) and send what *Settings › General › Pings per check* says.

### Fixed

- **Network map line thickness.** A thickness typed into the line controls is
  drawn straight away rather than once the field is left, and lines are drawn in
  pixels on the screen: a large map fitted into its frame, or one on a dashboard
  widget, used to thin every line to a hairline whatever thickness was chosen.
  A link's style is drawn as it is changed and saved just after.

## 0.8.3 (not yet released)

GWatch only; gwatch-agent stays at 0.8.2.

### Changed

- **Add node picker.** The agent machine is offered once, as **A machine with the
  GWatch agent** (which also hands out the pairing code), instead of twice. The
  **Discover devices** tile is gone: **Discover** is already on the Nodes toolbar,
  beside **Add node**.

## 0.8.2 (not yet released)

0.8.1 was never published, so everything under it below ships in 0.8.2. Agent
0.8.2 is released alongside.

### Changed

- **Test robustness.** The test database helper tolerates Windows holding a closed
  database file a little longer, and a remote-collector test waits for the agent to
  let go of delivered results; the 0.8.1 release run stopped on those two tests.
  No change to GWatch or the agent themselves.

## 0.8.1 — 2026-10-24 (not published)

### Fixed

- **Tighter access and input handling.** Saving a wallboard no longer hands its share
  token back to a non-administrator, and a missing board looks the same as an unshared
  one to an anonymous caller; agent inventories are left out of the hardware list for
  non-administrators; moving a check to another collector counts as retargeting it, so
  an API key cannot send its stored credentials somewhere new that way. A ping or DNS
  target starting with `-` is refused, Python script placeholders can no longer break out
  of single-quoted or f-string literals, `$` is escaped in systemd `ExecStart=` lines, the
  SQLite path is escaped in its connection string, and the timeline search treats `%`
  and `_` literally. Pre-migration database copies and backup archives are written
  owner-only. `gwatch install` refuses a system directory as the data directory, and on
  Windows the program a service install puts in Program Files can be run by ordinary
  users. A correct `/hook/` token gives back the attempt it was charged instead of
  clearing the whole failure budget.
- **Checks.** An AAAA check (set through the API) resolves IPv6 addresses only; a JSON
  content watch no longer reads a non-JSON body as `null`; a check with no saved
  timeout is bounded by the default one; a check moved to a collector or deleted just
  as it fell due is no longer left marked as running and never scheduled again; and a
  collector's incident without a node gets a PagerDuty/Opsgenie key of its own.
- **API.** Acknowledging a resolved incident is a `409`, not a `500`;
  `PUT /api/agents/{id}` without `nodeId` keeps the machine on its node instead of
  detaching it; on-demand reports trim their groups and tags, build their window from
  local days rather than UTC, and count an open incident as lasting until now; a restore
  keeps agent references pointing at the same machine.
- **Agent.** The trial reading before a self-update uses the agent's own configuration
  file; serve mode stays inside the machine's own collection limit; after a self-update
  where no service manager would restart it, the agent restarts itself in place; the
  newest agent version seen at check-in is recorded safely under concurrent check-ins.
- **Web interface.** A viewer's own theme and accent are no longer reset; the Charts
  page's **New chart** works and a refused save says so; **Export chart as PNG**
  downloads instead of throwing; a live update for the old range can no longer redraw
  a node's History card after another range is picked; a discovery started just before
  its dialog closed stops polling; incident actions keep their button after a failure
  and the node's Timeline link opens that node's events; inspecting a hardware reading
  that carries only the one-minute load no longer breaks the node page; a refused
  sign-in is styled as an error; and the local sign-in switch and the password hashing
  choice go back to what is saved when a save is refused.
- **Documentation**: the README names the current version and the release process as the
  dispatched workflow runs it, `docs/API.md` lists every route an API key is refused,
  packaging docs point at screens and jobs that exist, a Markdown link checker
  (`scripts/ci/check-md-links.py`) guards relative links and anchors, and the user guide,
  API, install, restore, Docker, privacy, collectors and MCP pages were checked against
  the code again (settings fields, placeholders, where secrets are kept, CLI paths).
- **gwatch-mcp 0.1.2.** It no longer follows a redirect to another host or scheme,
  which would have handed the API key to that address; and `gwatch_update_node` keeps
  the settings of an existing check the caller left out (enabled, interval, timeout,
  retries, alert override) instead of resetting them to a new check's defaults.
- **Secrets stay where they belong.** A webhook URL or channel token in a failed
  action's error no longer reaches the timeline; an HTTP check's headers, body and
  metrics token are not sent on to a redirect that leaves the configured host or drops
  to plain HTTP; a collector's oversized result details are clipped.
- **Concurrency and shutdown.** A dependency probe no longer overwrites a newer state
  committed to the same check meanwhile; notification-rule decisions are saved in the
  order they are made; an open browser tab no longer holds up a service stop; a git
  action that times out cannot be held open by an ssh child; a pushed hardware reading
  that fails to record is answered with an error so the agent keeps it; and a restore
  no longer carries the old checks' status and active alerts over to the restored ones.
- **Numbers and times.** Readings an agent or collector held through an outage reach
  the long-range rollups; a check with no samples shows no availability instead of a
  red 0%; uptime bars weight their buckets by sample count; chart hour ticks, timestack
  day layers, the hourly warning digest and a weekly maintenance window starting in the
  skipped hour all keep to local time across daylight-saving changes; the named-metric
  history CSV has `value,unit` columns; a scheduled report's subject names its last day.
- **Command line and installs.** `--version` and `--help` work for both programs and a
  misspelt agent command says so; a second `install` is refused before it spends a
  pairing code or overwrites the running program; re-running `sudo gwatch install
  --db-*` under systemd works; Linux disk sizes use the fragment size (right on NFS) and
  inode counts can no longer wrap; system ping parses translated Windows output; SNMP v3
  refusals name the setting to check; `GWATCH_AGENT_INTERVAL` accepts plain seconds;
  two agent control panels no longer share one cookie.
- **Web interface, second pass.** Editing a wallboard panel keeps its node list; a
  second save from the same settings tab is no longer silently dropped; an automatic
  backup password needs its confirmation; a maintenance window can no longer widen to
  every node when its group or node is gone (the server refuses a node id of 0 too);
  renamed header and environment rows can be removed; a refused dashboard change is
  undone on screen; and the audit log's **Load more** keeps the filter last applied.

## 0.8.0 — 2026-10-23

### Changed

- **GWatch and the agent host their services with their own code** (#46) instead of
  kardianos/service: the Windows Service Control Manager, launchd and systemd (with
  OpenRC and SysV fallbacks), which now also read the env file on non-systemd Linux.
- **Web UI maintenance** (#118): shared helpers keep focus through live refreshes, the
  shared modules are type-checked with `tsc`, a contract test keeps the mock API in step
  with the real one, and the Settings, node editor and mock files are split into modules.

### Fixed

- **A backup taken while a machine was being paired restores again.** The node a
  pairing code creates waits with a switched-off hardware check and no machine;
  restore refused it ("choose which registered machine this check reads"), so the
  server's own backup could not be restored. A switched-off agent check may now wait
  for its machine; one that is switched on must still name it.
- Restoring a backup with its history no longer loses the "Pre-restore backup
  created" event: it is recorded again, at the time the safety backup was taken.
- On Windows, a Python script action prefers the `py` launcher and a real
  `python.exe` over the Microsoft Store's "Python was not found" alias.
- `gwatch install` on Linux says how to fix a `/usr/local/bin` that someone other
  than root can write to.
- **The agent comes back after a power cut** (#170). A power cut while `agent.json` was
  being written could leave it empty, zeroed or cut off, and the agent then refused to
  start (exit 78, which the packaged service never restarts). Every save now also keeps
  `agent.json.bak`, both written atomically, and a damaged file falls back to the copy
  with a warning. Serve mode keeps trying to listen when its address is not there yet,
  as on a machine that boots before DHCP answers, instead of serving nothing.
- **Garbled text in CSV exports and ntfy titles** (#178). CSV exports start with a
  byte-order mark so Excel reads them as UTF-8, and ntfy titles and tags that are not
  plain ASCII are sent as RFC 2047 encoded words.
- **Engine and hardware correctness** (#161): Windows network counters are 64-bit
  (GetIfTable2Ex), so fast links no longer wrap every interval; deleting a node or check
  takes it out of the rules that named it (an emptied rule is switched off); a system
  ping stopped before its summary no longer reports phantom loss; sleep detection uses
  the wall clock; a hung network mount no longer blocks the hardware sampler.
- **Filters stay in the address** (#113). The Nodes list and the incident event feed
  write their filters into the URL, the event feed's Export CSV follows the filters
  (type included), and a slow answer for an earlier filter can no longer land last.
- **Dashboards stop re-downloading history on every live update, and share one event
  stream** (#114).
- **Bounded shutdown** (#105). Stopping gives in-flight work 15 s, cancels it, and
  returns within 20 s, inside the Windows SCM and systemd stop timeouts.
  `gwatch migrate-db` refuses to copy while GWatch is running, and a failed copy says
  to re-run with `--replace`.
- **Safer restores** (#148). A restore first takes a full backup of what it replaces,
  runs to the end even if the browser goes away, leaves out accounts and agents deleted
  since the backup unless asked to re-create them, and keeps agents linked to their nodes.
- **Startup no longer stalls result writes** (#107). The rollup backfill runs in
  day-sized chunks and, after the first, only catches up from where the last one got to.
- **Alert e-mails** (#99): at most three are sent at once, and a failed one is retried
  once after 15 s before the alert is re-armed.
- **The network map's clusters layout** no longer puts nodes on perfect rings: each
  node's dependents branch off it in whatever direction has room, with longer lines to
  bigger branches, and nothing overlaps.
- **Documentation accuracy** (#165): API routes, the config export, Settings tabs,
  retention, privacy (what is encrypted, what GWatch contacts), ROADMAP and the brief.
### Added

- **Linux: `gwatch install` creates a proper systemd service** (#125). The program goes
  to `/usr/local/bin/gwatch`, runs as a `gwatch` system user with data in
  `/var/lib/gwatch` and settings in `/etc/gwatch/gwatch.env`, under a hardened
  `gwatch.service` with `CAP_NET_RAW` (and `CAP_NET_BIND_SERVICE` only for a port below
  1024). Re-running it updates and reconfigures; an existing `GWatch.service` install is
  migrated; `gwatch uninstall --purge` removes everything after a confirmation. Checks
  and script actions no longer run as root — see `docs/INSTALL-LINUX.md` for granting
  more. Updating from Settings › Updates is not available on these installs yet: re-run
  the installer.
- **Ping says how it works and why it fell back** (#131): the startup log, Settings ›
  Monitor health and discovery name the ICMP method in use and the reason (for example
  `ping_group_range`); the Linux docs cover all three ways to allow ping.
- **Server `.deb` and `.rpm` packages** for amd64, arm64 and armhf (#126), with a
  hardened unit, a `gwatch` user and an update path that steps aside for apt/dnf; a CI
  job installs, upgrades and purges them, and another exercises `gwatch install` under
  systemd end to end (#135).
- **Linux readings** (#132): temperatures (hwmon and thermal zones), pressure stall
  (PSI), memory and CPU against cgroup v2 limits inside a container, ZFS pools with
  health, and `/` on overlay.
- **Discovery reads the neighbour table and names makers** (#133): devices that ignore
  ping are found through ARP, and results show the MAC address and its vendor (IEEE
  registry, embedded); Windows reads `GetIpNetTable`.
- **The agent keeps held readings through a restart** (#74), in a bounded spool beside
  its configuration, and marks late readings so GWatch files them at the time they were
  taken. Token rotation (#75) is now covered end to end by tests.
- **More alert channels** (#122): Telegram, Discord, Gotify, Matrix, Twilio SMS,
  PagerDuty and Opsgenie actions, and an "Also notify" list in Settings › Alerts that
  goes out with the alert e-mails under the same suppression, with per-channel minimum
  severity and groups. Secrets are sealed and masked.
- **Opt-in PBKDF2 password hashing** (#49) from the standard library, for accounts and
  backup encryption; Argon2id stays the default and accounts move over as they sign in.
- **Matter devices** (#45): a `matter` check reads on/off, temperature, humidity,
  contact, occupancy, illuminance and battery from a python-matter-server sidecar, with
  a device picker and commissioning by pairing code. See `docs/MATTER.md`.
- **The agent's control panel** (#64) shows the recent log, can update now, re-pair and
  copy diagnostics, asks the OS for elevation when it needs to, and has Start menu and
  desktop entries. The Windows setup program tests the connection before installing and
  ends on a real result page (#63). An optional Windows notification-area icon (#53).
- **Remote collectors** (#123): a paired agent can run the network checks (ping, TCP,
  DNS, SNMP, HTTP) of nodes at another site and send the results back, when an admin
  turns it on in GWatch and the machine allows it in its own `agent.json`. A collector
  that goes quiet raises one alert, not one per check. See `docs/COLLECTORS.md`.
- **Watch services and programs** (#62): `service:<name>` and `process:<name>` readings
  on Linux, Windows and macOS, critical when stopped by default, picked from the
  machine's own list.
- **Grouped alerts and a warning digest** (#99): optionally, a node's alerts within a
  short window go out as one e-mail ("Web — 3 checks down"), and warnings can be sent as
  an hourly or daily digest.
- **Restores are atomic, validated and previewed** (#148): the configuration is restored
  in one transaction with monitoring paused, the archive goes through the API's own
  validation, and Settings › Backups shows what a restore will add and change first.
- **Authenticode signing for the Windows programs and setup programs** (#120), switched
  on once an Azure Artifact Signing account is configured; releases stay unsigned, with
  a warning, until then.
- **Check groups across nodes** (#36), as a prototype: put checks from different nodes
  in groups of their own and filter, chart and bulk-edit them as a set.
- **Charts treat a node as one item** (#184): the picker lists checks under their node,
  and a chart can be split one per node.
- **Disk activity on macOS** (#185). The agent and the local hardware check read disk
  throughput, operations and busy % on macOS from the I/O Registry, as on Linux and
  Windows.
- **Discovery describes responders 16 at a time** (#110), so a busy /24 is described in
  seconds rather than over a minute.

## 0.7.2 — 2026-10-22

The first published release since 0.6.2: 0.7.0 and 0.7.1 were never published, so
everything under them below ships in 0.7.2. Agent 0.7.2 is released alongside.

### Changed

- **Release publishing.** The shared release tooling is pinned to a version that finds
  a newly created draft release in the release list; the 0.7.1 run stopped there,
  before anything was published. No change to GWatch or the agent themselves.

## 0.7.1 — 2026-10-22 (not published)

Security fixes on top of 0.7.0.

### Security

- **Update signatures name the release they are for.** Each `.sig` now also carries
  `release-sig`, an ed25519 signature over the asset's name, the release version and
  its digest. GWatch, the agent and the install scripts check it, so a genuinely signed
  binary can no longer be republished as a newer release or as another product. The
  updater checks the signature's release before downloading the executable and stops a
  download at the size the release declares (512 MiB at most). The agent's own version
  proof is now an exact comparison. Copies released before this keep verifying the
  digest-only `sig` line, which is still written so they can install this release.
- **API keys keep their limits on stored checks.** A read-write key can no longer
  retarget a check whose credentials would be filled back in from storage (403), get a
  check's decrypted credentials back from `POST /api/checks/{id}/enable` (responses are
  now masked), or run, switch on, duplicate or bulk-enable checks that run a command or
  carry a credential to a URL. Running a disabled check or node with a key answers 409.
- **Manual node runs share the check concurrency limit**, and a node holds at most 500
  checks.
- **Sign-in limits are per account and source.** A successful sign-in resets only the
  failures against that account from that address; a per-address ceiling (30 a minute)
  that a success never clears stops spraying. Changing a password now checks the limits
  and takes a slot of the sign-in concurrency limit before any password hashing.
  `--trusted-proxies` / `GWATCH_TRUSTED_PROXIES` (empty by default) lets a reverse proxy
  you name forward the client address the limits use; see
  [`docs/REMOTE-ACCESS.md`](docs/REMOTE-ACCESS.md).
- **The last administrator cannot be removed by two requests at once.** The check and
  the change now happen in one database transaction.
- **CSV exports neutralise spreadsheet formulas**: a text cell starting with `=`, `+`,
  `-`, `@`, a tab or a carriage return is prefixed with `'`.
- **The agent keeps secrets off command lines.** `run` and `serve` started with
  `--token` move it into `GWATCH_AGENT_TOKEN` and restart themselves in place without
  it (Windows warns instead); the control panel's address carries a one-time code that
  expires in five minutes and is no longer handed to the browser opener's arguments.
- **`gwatch-sign keygen -force`** writes the key as a new 0600 file and refuses to
  write through a link.

- **A remote database connection verifies the server by default.** A PostgreSQL or
  MySQL/MariaDB configuration that leaves `sslMode` empty now gets `verify-full`
  (PostgreSQL) or a verified TLS connection (MySQL) when the host is not this machine.
  `prefer` could be downgraded to plaintext or answered by an impostor by anyone on
  the network path. A server on `localhost`, a loopback address or a Unix socket keeps
  `prefer`, and a mode set explicitly — including `prefer`, `require` or `disable` — is
  honoured as before. See *Upgrade notes*.
- **Backup restore is bounded.** An upload larger than 2 GiB is refused with 413 before
  it is stored; the decrypted archive, the number of entries, `config.json` (256 MiB),
  the decompressed history (32 GiB in all) and its row counts are all limited, and an
  archive over any limit is refused before anything in the database is changed.
- **Notification URLs cannot be redirected by a hook caller.** Slack and Teams webhook
  URLs and the ntfy server now take placeholders only in the path and query, escaped as
  data, as HTTP actions already did; a placeholder in the scheme, user info, host or
  port is refused when the action is saved and when it runs. ntfy's bearer token is no
  longer sent on to a redirect that changes scheme, host or port.
- **A shared wallboard returns only what it draws.** The view behind a board's projected
  address is now built on the server from the board's panels: the nodes a *Nodes*
  panel selects (name, groups, tags and status only), the lists the other panels show,
  and the service's running state for the *Service* panel and the footer. It no longer
  carries the whole overview (every node with its checks), incidents, or the Health
  document's paths, database details and recent errors.

### Upgrade notes

- **Release tooling.** `gwatch-sign sign` and `verify` require `-version`, the release
  version the assets are published in; `make sign`/`verify-release` pass `VERSION`.
- **Server databases with no `sslMode`.** An install that reaches a remote database
  through `GWATCH_DB_*` environment variables, `--db-*` flags or a hand-written
  `database.json` without `sslMode` will now insist on a certificate that verifies
  against the system trust store and matches the host name. If the server has none,
  the service will not start: add the server's CA to the trust store, or set
  `sslMode` (`GWATCH_DB_SSLMODE`, `--db-sslmode`) to the mode you used before,
  `prefer`, explicitly. A `database.json` saved by the settings page or
  `gwatch migrate-db` already names its mode and is unaffected.
- **Notification actions with a placeholder in the host.** A Slack, Teams or ntfy action
  whose URL or server took its host from a placeholder (`{{query.webhook}}`, say) now
  fails when it runs and must be edited to name the host. Placeholders later in the
  URL keep working, but a value is escaped as one path segment, so `/` in it arrives
  as `%2F`.

## 0.7.0 — 2026-10-14 (not published)

### Added

- **Adding a machine is part of Add node.** *Nodes › Add node › A machine with the
  GWatch agent*: a name, what to monitor, and an eight-character pairing code with
  the command to run (its server address editable, for a machine that reaches GWatch
  differently from your browser). The *Pair a machine* button and the *Settings ›
  Hardware* section are gone, and an old link to either lands on Nodes. Choosing an
  existing node to add the machine to now gives that node a hardware check for it.
- **The agent card, on the machine's node.** Administrators see the agent that
  reports for a node in the node itself: identity and a quiet note when a newer agent
  exists, **the token** (shown on request, so it can be compared with the one in the
  machine's file), **rotation**, **what it collects**, and revoke / delete. GWatch now
  keeps a copy of a token sealed with its own key (a backup does not carry it; an agent
  registered before this has only a digest and gets a readable token by being
  rotated).
- **Token rotation**, in two forms. *Rotate* issues a new token while the old one keeps
  working; the agent collects it the next time it asks for its settings, writes it into
  its file and switches over, and its first use retires the old one. *Replace now* is
  for a token that may have leaked: the old one stops at once and nothing is sent to the
  machine.
- **Choose what an agent reports.** Checkboxes for the kinds of reading and, once the
  machine has said what it has, for its network interfaces, disks and filesystems —
  while adding the machine and on its node afterwards. Automatic leaves out virtual,
  bridge, container, tunnel and Windows pseudo interfaces, which are offered but not
  ticked. The choice narrows what the agent sends inside a limit set in its own file;
  GWatch can never widen it.
- **The agent asks GWatch for a small, fixed set of things** every so often
  (`POST /ingest/checkin`): which readings were chosen, when to ask again, the newest
  agent release (a hint only) and, during a rotation, the replacement token. It has no
  field for a URL, a command, a file or a version to install, and the agent still opens
  every connection. See [`docs/AGENT-DECISIONS.md`](docs/AGENT-DECISIONS.md), entry 12.
- **The agent keeps one editable file.** `agent.json` holds the server, token, name,
  intervals, auto-update, certificate trust and a collection limit; the service is
  registered with no settings of its own, so a change is an edit and a restart rather
  than a reinstall, and the token is no longer left on a service's command line.
  `gwatch-agent config show` prints each setting with the layer that decided it
  (defaults, file, environment, flags) and `config validate` checks the file. Agents
  paired by an earlier version move their token and settings into it on first start.
- **The agent holds readings through an outage** (up to `buffer`, six hours by default,
  in memory) and sends them oldest first when GWatch is back, so a gap in the network
  leaves a gap in the history no longer than itself.
- **A control panel for the agent: `gwatch-agent ui`.** A page on the machine's own
  loopback address, in GWatch's style: whether the agent is reporting, every setting,
  the collection limit as checkboxes for the interfaces and drives the machine really
  has, the token, a test reading, an update check, start / stop / restart, and the
  file itself. It runs as the person who opened it rather than inside the root/SYSTEM
  service, so it can change only what that person could; see entry 6 of
  [`docs/AGENT-DECISIONS.md`](docs/AGENT-DECISIONS.md).

- **Network map: clusters, and lines you can style.**
  - **Clusters layout.** The *Radial* layout, which put every node on rings
    round one centre and read as one giant circle, is replaced by **Clusters**:
    a node with things plugged into it sits in the middle of a ring of them, a
    switch among its machines, the switches that hang off a router in a ring
    round the router, circles around circles. One line joins each node to its
    parent. Rings are sized from the leaves up so that nothing overlaps and a
    line is always there to see; children go round in name order, and a node
    with a single child puts it straight out beyond itself so a chain does not
    balloon. Several trees ring the biggest one; nodes with no dependency are
    kept in rows underneath. A map saved as *radial* keeps working and now
    draws as clusters. The top-to-bottom and left-to-right layouts are
    unchanged.
  - **Line colour, thickness and style.** **Customise** gains **Line style**
    (colour, thickness 1–8, solid, dashed, dotted or dash-and-dot) for every line
    on the map, in the page and in a dashboard's *Network map* widget alike.
  - **Per-link style.** Choose a node on the map and **Link to *its parent***
    sets that one line's colour, thickness and style (with **Reset**); the node
    editor has the same **Link style** group beside **Depends on**, and
    `PATCH /api/nodes/bulk` takes a `linkStyle`. A link's own style beats the
    map's, which beats the theme's. It is kept on the node at the lower end of
    the link (`Node.linkStyle`, in a new `nodes.link_style` column added to older
    databases when they are opened), so every map shows it, and it travels with
    the node in backups, restores, the database copy and the configuration
    export.
  - **An outage still shows.** Lines out of a node that is down stay red whatever
    colour was chosen — though they take the chosen thickness and dashes — and
    the legend says in words what red means, so neither colour nor dash style is
    the only cue. **Export as SVG** carries each line's colour, thickness and
    dashes.
  - Colours are `#rgb`, `#rrggbb` or `#rrggbbaa` and nothing else, checked in
    the service (a bad colour or style is a 400; a width is clamped) and again
    in the page before anything is drawn; a restored backup or an older database
    cannot carry anything else in either.

### Changed

- **A machine's hardware readings are its check's results.** They used to be stored
  twice — once as every hardware check's result, and again, in full, in a table of
  readings of their own with its own retention, API, backups and charts. That second
  copy is gone (schema version 4 drops it; SQLite databases are copied aside first, and
  PostgreSQL and MySQL want the server-side backup `docs/DATABASE.md` asks for). An
  agent's reading is judged when it arrives: a current one goes through the check's
  state, incident and alert machinery; one the agent held back through an outage joins
  the history at the time it was taken and alerts nobody. The check's schedule is kept
  to notice that readings have stopped. A node's hardware charts and the hardware list
  are served from the results, and **Settings › Retention › Keep hardware check results
  for** now bounds hardware checks' results (every other check follows the setting for
  individual results). See entry 15 of [`docs/AGENT-DECISIONS.md`](docs/AGENT-DECISIONS.md).
  - **Behaviour that changes:** a machine no hardware check watches keeps only its
    newest reading, in memory — the migration drops what the old table held for such
    machines; deleting an agent removes its registration but leaves the node and its
    history; a year-long chart of a hardware metric reaches back as far as the hardware
    retention (90 days by default); and an archive from before this carries a list of
    hardware readings that restore ignores, since every reading in it was also a check
    result.
- The *Radial* layout is now called **Clusters (radial)** in the layout list.
- A link's style is dropped when the node stops depending on anything and when
  its parent is deleted, so a colour chosen for one link does not turn up on
  another.

### Versions and upgrading

- GWatch is **0.7.0** and GWatch Agent is **0.7.0**. The agent needs 0.7.0 to use
  most of what is above (its file, token rotation, check-ins, held readings and the
  control panel); a 0.6.x agent keeps reporting to a 0.7.0 server,
  but it never asks for settings, so it ignores a rotation and the readings chosen
  for it until it updates.
- The database moves to schema version 4 the first time 0.7.0 opens it. A SQLite
  database is copied aside first; PostgreSQL and MySQL want the server-side backup
  [`docs/DATABASE.md`](docs/DATABASE.md) asks for before any upgrade. Downgrading to
  0.6.x afterwards is not supported: the table 0.6.x keeps hardware readings in is gone.

## 0.6.2 — 2026-10-13

### Fixed

- Configuration-only restores preserve results and rollups for matching check
  IDs, incidents for matching nodes, and the event timeline (#140). Restores
  update nodes and checks in place; IDs absent from the archive are removed
  along with their associated history. Both confirmation dialogs explain this.
- Restoring a backup no longer disconnects existing agents from nodes retained
  in the restored configuration. Unsupported archive formats are refused before
  any database changes (the agent-link and format-validation parts of #148).

### Versions and scope

- GWatch is **0.6.2**. GWatch Agent remains **0.6.1**.
- Whole-restore atomicity, automatic safety backups, and deleted-account/agent
  resurrection remain tracked in #148; this patch does not resolve those items.

## 0.6.1 — 2026-10-11

### Fixed

- Weekly maintenance windows carry an explicit IANA time zone and local start
  time, follow daylight saving changes, and remain active across multi-day
  durations (#156; multi-day portion of #161). Existing windows keep their
  server-local schedule until edited; confirm their weekday and time when editing.
- Automatic triggers respect maintenance and silences. Run-node actions have a
  one-minute minimum cooldown, including previously saved triggers, preventing
  immediate self-triggering and mutual loops (#98).
- Configuration reloads are serialized and finish after a saving browser
  disconnects. Empty watched HTTP headers/JSON values become a persisted
  baseline, so a disappearance produces one content-change event (#101, #147).
- Latest-result reads use an index lookup per check instead of scanning retained
  history on every dashboard or overview request (#108).
- Wallboard layout inputs and chart canvases survive live refreshes, preserving
  unsaved edits and keyboard focus, including timestacked charts (#158).
- SNMP community entry is readable text. Saved credentials remain protected,
  clearly marked as stored, and preserved when left unchanged (#177).
- Session writes check the Origin header even without Fetch Metadata. Sign-in
  password checks have a shared concurrency limit to bound Argon2 memory use
  across clients (#90, #91).
- Node script placeholders escape template-literal interpolation and backticks
  so a monitored value cannot turn into executable JavaScript (#144).
- Server and agent prereleases are flagged in GitHub, cannot move Docker's
  `latest` tag, and are excluded from stable updates even if GitHub's prerelease
  flag is missing (#152).
- The local-administrator trust model, Windows LocalSystem implications,
  less-privileged service setup and decision to retain the local-login default
  are documented (#18).

### Versions and validation

- GWatch and GWatch Agent are **0.6.1**. The agent receives the shared updater's
  prerelease filtering safeguard; this release does not claim to resolve the
  reported Windows recovery issue (#170).
- Confirmed existing DNS-rebinding and retention-fragment regression coverage
  for #86 and #150. Both fixes were already present before this release.
- Added Go, browser-DOM and Chromium regression coverage for the fixes above.

## 0.6.0 — 2026-10-05

### Added and fixed

- Grouped incidents with acknowledgment, resolution, attributed notes and timing,
  plus an Events tab retaining the raw audit timeline. Acknowledgment stops repeat
  notifications until recovery; a node incident closes when all enabled checks recover.
- Weekly/monthly availability reports by groups or tags, optional availability
  targets, incident durations, slowest checks and expiring certificates. Reports
  arrive as HTML email with a printable HTML attachment; generate any range on demand.
- Warning cooldowns no longer delay down alerts. Database or SMTP failures allow
  retries; dependency probes wait for in-flight parents; silence expiry re-evaluates
  rules. Scheduler fairness and overdue-check health metrics prevent silent starvation.
- Subprocess trees are terminated on timeout and captured output is bounded.
  Pausing/running a check concurrently cannot corrupt its state. Hardware Test works;
  SNMP resets no longer produce huge false rates. Scheduled pruning keeps manual backups.
- Linux/macOS verified installers, ARMv7 server artifacts and image, journal logging,
  configurable listening port and UI restart. Agent installation uses a protected
  executable location, Windows updates retain the executable extension, lifecycle
  commands work without a token, and serve mode has separate credentials and TLS support.
- Keyboard focus, theme accessibility, chart labels, empty states, wallboard fit/retry,
  bulk selections, community-password feedback, onboarding/update dismissal and hidden
  tab work are corrected. Static assets have validators and directory listings are disabled.
- Real-server browser and MCP smoke tests, full Linux race coverage, additional database
  engine/host tests, collector parser tests, staticcheck, pinned release tooling and
  scheduled vulnerability checks. Publishing waits for signed release success.

### Upgrade notes

- GWatch and GWatch Agent are independently versioned **0.6.0**. The bundled MCP
  companion is **0.1.1**, with agent skill **1.0.1**.
- Treat credentials present in old shared wallboard URLs/config exports as exposed:
  rotate affected credentials and wallboard share links after upgrading.
- Signing jobs use the protected `release` Environment and accept only commits on
  main. The maintainer migration workflow moves the signing key as encrypted data;
  remove the repository secret after confirming the Environment copy.
- Daily rollups now use the service's local calendar and DST boundaries. Existing
  days are rebuilt where source detail remains. Historical daily-only UTC buckets
  retain their original boundaries; hourly-only archives have hour-level precision
  in time zones whose offsets include partial hours.
- Availability is based on observed samples. Missing samples are not treated as up;
  historical incidents before this upgrade are not reconstructed from old event logs.
- For a TLS-terminating reverse proxy, explicitly enable `behindHTTPSProxy` to mark
  session cookies Secure. Enable it only when clients actually use HTTPS.

### Security

- **A read-write API key can no longer run commands on the host.** Creating,
  editing or testing a `custom` check — or a `system` check that reads an
  agentless URL — now requires an administrator signed in to the web interface,
  and is refused to every API key with 403. This closes a path by which a
  read-write key (and so the MCP companion, if run with `--allow-write`) could
  execute arbitrary commands as the service account ([#85]).
- **DNS rebinding can no longer reach the local administrator.** A loopback
  request is granted the no-sign-in administrator principal only when its `Host`
  header names this machine (a loopback address, `localhost`, or the machine's
  own hostname or addresses). A page on an external domain rebound to `127.0.0.1`
  no longer counts as local ([#86]).
- **A shared wallboard no longer leaks check credentials.** The projected
  (token) wallboard view is now masked like every other read path, so a board's
  share URL cannot hand out SNMP communities, metrics tokens or check headers
  ([#87]).
- **Read-only API keys no longer read check header/body/environment secrets.**
  A check's request headers (e.g. `Authorization`), request body, custom-check
  environment and agentless metrics URL are stripped from responses to API keys,
  which are the MCP companion's default. Browser responses mask these values and preserve them on save. Check headers,
  bodies, environments, notification credentials and endpoint tokens are sealed
  at rest; upgrades migrate existing plaintext values ([#142], [#88]).
- **The brute-force limiter now keeps a separate budget per credential kind.**
  A success of one kind (an agent reporting in, a valid key, a hook) can no
  longer clear the failed-attempt budget another kind (sign-in, the access
  password, a pairing code) is accumulating, and IPv6 clients are keyed on their
  /64 ([#89]).
- **Sign-in timing no longer reveals which user names exist.** A sign-in for an
  unknown name now spends the same argon2 work as one for a real account ([#91]).

[#85]: https://github.com/jxburros/GWatch/issues/85
[#86]: https://github.com/jxburros/GWatch/issues/86
[#87]: https://github.com/jxburros/GWatch/issues/87
[#88]: https://github.com/jxburros/GWatch/issues/88
[#89]: https://github.com/jxburros/GWatch/issues/89
[#91]: https://github.com/jxburros/GWatch/issues/91
[#142]: https://github.com/jxburros/GWatch/issues/142

## 0.5.0

Four requests from the field, and SNMP made a first-class way in. The agent is
unchanged and stays at **gwatch-agent 0.5.0**.

### Added

- **Network map.** A new page, **Network map** in the sidebar, draws the
  dependencies between nodes: each node sits under the node it *depends on*,
  so a firewall or a gateway is at the top of everything an outage there would
  silence. Nodes are coloured by status, and the lines out of a node that is
  down are drawn red and dashed. Choose a node to see what is upstream of it
  and what depends on it, and to change its dependency in place (a choice that
  would make a loop is not offered). **Customise** lays the map out top to
  bottom, left to right or radially; labels nodes by name, by name and address
  or not at all; sizes them small, medium or large; draws lines curved,
  straight or right-angled; narrows it to a group or tag, keeping the chain a
  filtered node depends on, faded; and can hide nodes with no dependency. The
  choices are remembered per browser. Zoom with the wheel or +/−, pan by
  dragging or with the arrow keys, 0 to fit; export as SVG; "View as list"
  gives the same tree as text. **Pin to a dashboard** adds it as the new
  **Network map** widget, whose editor offers the same options. A node's page
  links to it with **Show on the network map**.
- **Timestacked charts.** The **Timestack** button on the Charts page (and the
  same option in a chart widget's editor) lays one metric over itself — the
  last 1 hour, 24 hours, 3 days, 7 days or 30 days over the periods before
  it, 2 to 8 layers — on one time axis: "Last 24 hours", "Yesterday",
  "2 days earlier"… with the current layer drawn thickest and each layer's
  average in a tile. Saved charts and dashboard widgets keep it; the range
  chips pick the stacked period while it is on. Behind it, the history API
  takes an `end=` (Unix seconds or milliseconds, or RFC 3339) to read a window
  that ends in the past, and a `3d` range.
- **SNMP, easier to find and to set up.**
  - A new **SNMP device** template: a ping and an SNMP check on v2c reading
    uptime and the device name, ready to test.
  - **Test connection** in the SNMP check reads the device's name, description
    and uptime with the settings as typed, and says *Connected to …* or lists
    what to check when nothing answers.
  - **Pick interfaces** lists the device's ports by name, alias and
    description with whether each link is up, and turns the ticked ones into
    link-state, traffic in/out (64-bit counters by default) and optionally
    error readings — no SNMP index to look up.
  - A short how-to at the top of every SNMP check, a node page prompt
    (**Add SNMP readings**) on anything that looks like network gear, the
    same item in the node's menu, SNMP moved up the check-type picker, and a
    new Help topic, *SNMP devices*.
- **Sorting node lists.** The Nodes page has a **Sort** control — status
  (worst first, as before), name A–Z or Z–A, address or importance — and can
  show **one list** instead of group sections. Names sort as a person reads
  them (case ignored, "switch 9" before "switch 10"). Both choices are
  remembered, and `?sort=` works in a link. The *Status list* and *Node table*
  widgets gained a **Sort by** option.
- Help has new topics for the network map and SNMP devices, and the charts
  topic covers timestacking.

### Fixed

- **Dragging a dashboard widget sent it pages down** until the page was
  reloaded. The drop row was worked out from the CSS row height read as a bare
  number — `11.375rem` taken as 11.375 pixels — so a drop a few hundred pixels
  down landed a dozen or more rows further, and the widget stayed pinned there
  while everything else closed up. The grid's own row height in pixels is
  used now, a page that scrolls mid-drag no longer throws the drop off, and a
  widget dropped into empty space floats up under the others.

## 0.4.0

The 2026-09-27 sprint: everything labelled `sprint-plan` in the tracker. The
hardware agent is released alongside it, on its own train, as
**gwatch-agent 0.5.0** (see [below](#gwatch-agent-050)).

### Added

- **Chart any metric, not just latency** (#68). The Charts page and dashboard
  chart widgets have a **Metrics** picker: each node › check lists every metric
  it can be charted by — latency (average, minimum, maximum), jitter and packet
  loss for a ping check, response time for the rest, availability for all, and
  every metric the check measures: a machine's processor, memory, swap, load,
  each disk and its inodes, each network interface received and sent, disk
  read, write and busy; an SNMP check's OIDs; a JSON check's recorded value.
  It filters as you type when there are many checks, counts what is ticked and
  clears in one go. Leaving it empty still lets GWatch pick the most important
  checks. Line colours are set per series, and CSV export gives one file per
  series in that series' own values. Saved charts and widgets from earlier
  versions open exactly as before; a new chart still records the old fields,
  so an older GWatch reading it falls back to latency rather than failing.
- **Several metrics, nodes and units on one chart** (#73). Any mix of series can
  share a chart: the first unit ticked is read off the left-hand axis and a
  second unit off a right-hand axis with its own scale. A third unit gets a
  chart of its own underneath — a note says so — rather than being squashed
  onto a rescaled axis. The tooltip, the keyboard walk, the screen-reader
  summary, "View as table", the stat tiles, the legend and the PNG export all
  give each series in its own unit, and label which axis it is on when units
  are mixed.
- **Pairing confirms itself** (#69). The dialog that shows a pairing code now
  watches it, through a new `GET /api/agents/pairings/{id}`: the moment the
  machine redeems the code it says the machine has paired, and when its first
  reading arrives it says the machine is reporting — each with a notification
  and an **Open its node** button. The agent's Windows installer says whether
  pairing actually worked on its last page, rather than always announcing that
  the machine is reporting.
- **An opt-in tutorial, and Help for everything added lately** (#44). Help has
  new topics for charts, AI assistants (MCP) and the database and Docker
  options, and its hardware, incidents, accounts, remote-access,
  troubleshooting and keyboard topics now cover pairing and its confirmation,
  per-metric readings, notification rules, recorded JSON values, agent
  self-update and packages, sessions and proxied sign-in. Every topic opens
  directly at `#/help?topic=<id>`. The words live in one module
  (`web/help-content.js`) shared by Help and the new **tutorial**: eleven
  steps across the real pages, each ringing one control, that start only from
  **Start the tutorial** in Help, never start or resume by themselves, pause on
  Esc or when you go elsewhere, and resume from the step you reached. Two new
  tips cover Settings › Rules and Settings › AI & MCP. `docs/USER-GUIDE.md`
  has a "Help, the tutorial and tips" section.
- **The agent has its own logo** (#79) — the "G" in a deerstalker. It is the
  icon of `gwatch-agent.exe` and of the agent's installer and uninstaller, the
  artwork of that installer's wizard, and appears in GWatch wherever the agent
  is introduced: the pairing dialog, Settings › Hardware, the onboarding step
  about other machines, Help and `docs/HARDWARE.md`. The installer artwork and
  the icon are generated reproducibly by `scripts/installer/make-assets.py`.
- **Agent packages** (#77): `.deb` and `.rpm`, a Homebrew formula, a winget
  package and a container image — see gwatch-agent 0.5.0 below.

### Changed

- **Throughput reads as a rate** (#67): B/s, kB/s, MB/s, GB/s on chart axes,
  tooltips, tables and stat tiles, the node page's network and disk charts,
  the machine panel and the result inspector, with nice round axis ticks in the
  scaled unit. Rates step in 1000s, as alert emails already wrote them (the
  machine panel and inspector used to step in 1024s); sizes on disk and in
  memory still step in 1024s. SNMP `bit/s` readings chart as kbit/s, Mbit/s…
- **Both installers are light-themed only** (#66): a pale page, a white
  header and fields, dark text and the teal rule under the header, with the
  wizard artwork redrawn on light. The dark artwork and palette are gone, and
  the wizard names no dark or system-following style.
- **Visitors through a reverse proxy on the GWatch machine sign in** (#70). A
  proxy running alongside GWatch connects from `127.0.0.1`, which used to earn
  every visitor through it the no-sign-in administrator standing reserved for
  someone at the machine. A loopback request carrying a forwarding header
  (`Forwarded`, `X-Forwarded-For`/`-Host`/`-Proto`, `X-Real-IP`) is now treated
  as the remote client it is. Caddy and `tailscale serve` add one already;
  nginx needs `proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;` —
  see `docs/REMOTE-ACCESS.md`. **If you reach GWatch through such a proxy
  without an account, create one before upgrading.**
- Changing an account's role no longer signs it out; the new role applies to
  its existing sessions on their next request and the page redraws itself for
  it. Changing a password still signs it out everywhere.

### Fixed

- **Random sign-outs and a stray "viewer" look** (#70):
  - A database hiccup while checking a session (SQLite busy, a dropped
    connection to a database server) was treated as "not signed in", and the
    401 sent the browser to the sign-in screen. It is now a 503 with
    `Retry-After`; only an unknown or expired session signs a browser out. A
    database error during sign-in is no longer reported as a wrong password.
  - The session slid forward in the database with use, but the browser's cookie
    kept the expiry it got at sign-in, so it vanished 30 days after sign-in
    however active the session had been. The cookie is now renewed with the
    session (at most hourly), and the session is written at most once a minute
    rather than on every request.
  - A single stray 401 no longer bounces the browser: it asks `/api/me` first.
    If `/api/me` could not be reached when the page opened, the whole session
    was styled as a viewer until reload; identity is now retried, re-read after
    a 403, a reconnect or a return to the tab, never downgraded by a failed
    fetch, and the page redraws itself when it changes.
- **The page grew a scrollbar for a few seconds at a time** (#71). The header's
  decorative sweep slid a box past the right-hand edge for the last fifth of
  every nine-second cycle; it is now a gradient that moves inside the header.
  The empty state's turning reticle is clipped too, so it cannot put a
  scrollbar on a narrow widget. A new browser test steps every running
  animation through its cycle and fails if the page ever gets wider than the
  window.
- **Empty values showed as "null"** (#72). The hardware check's editor printed
  a bare "null" under its thresholds; a select given no value now shows its
  empty option; a missing list is an empty one; the machine panel no longer
  says "load undefined". A new browser test makes the mock answer the way the
  Go service does — empty lists and optional values as `null` — and reads every
  page and every node's page and editor for "null", "undefined" or "NaN".

### gwatch-agent 0.5.0

Released separately, by tagging `agent-v0.5.0`; agents on automatic updates
pick it up by themselves.

- **Packages** (#77). The agent is now also published as a `.deb` and `.rpm`
  (amd64, arm64, armhf), a Homebrew formula (`jxburros/tap/gwatch-agent`), a
  winget package (`GWatch.Agent`) and a container image
  (`ghcr.io/jxburros/gwatch-agent`), each built by its own job on the
  `agent-v*` tag, with the packages built and exercised on every pull request.
- **A packaged agent belongs to its package manager.** Packages are built with
  the package's name baked in: such a build never updates or rolls itself
  back, `gwatch-agent update` prints the package manager's command instead
  (`update --check` still reports a newer release), and where the package
  registers the service itself the agent's own service commands point at
  `systemctl`, `brew services` or `docker`. Release binaries still update
  themselves, and no package is ever named like a self-update asset. The
  reasoning is entry 10 of `docs/AGENT-DECISIONS.md`.
- `gwatch-agent pair` saves the server address (and `--insecure`, `--name`)
  beside the token, so `gwatch-agent run` needs no arguments afterwards; with
  the Linux packages, `sudo gwatch-agent pair --server … --code …` also enables
  and starts the service.
- **Watching a Docker host from a container**: `--host-root` /
  `GWATCH_HOST_ROOT` reads the host's filesystems, name and distribution
  through the host's root mounted into the container.
- An agent with no token or server exits with status 78, which the packaged
  service unit does not restart on, so an unpaired machine does not log the
  same error forever.
- `gwatch-agent version` and the User-Agent the agent sends say which package
  a build came from.
- The agent's own icon (#79).

### Before the first packaged agent release

The Homebrew tap needs a `jxburros/homebrew-tap` repository and a
`HOMEBREW_TAP_TOKEN` secret, and the winget submission a `WINGET_TOKEN`
secret; without them those two jobs skip with a notice and keep the formula
and manifests as workflow artifacts. The container package has to be made
public once, after its first push. `docs/RELEASING.md` has the details.

## 0.3.1

### Added

- **The agent keeps itself up to date** (#52). `gwatch-agent` now has its own
  version ([`cmd/gwatch-agent/VERSION`](cmd/gwatch-agent/VERSION)), its own
  release tags (`agent-v…`) and its own GitHub release, and a running agent
  installs newer ones by itself. You install an agent by hand once. The
  machines agents run on are rarely machines anyone logs into, which is the
  argument both for the self-update and for the split: an agent fix no longer
  needs a GWatch release, and a GWatch release no longer restarts every agent.

  It is the agent, not GWatch, that decides this. The agent reads the release
  feed itself and verifies every download against signing keys built into the
  agent binary; **GWatch is never asked what version a machine should run and
  has no way to make an agent install anything**, because a GWatch that
  someone had got into must not become a way onto every machine reporting to
  it. Before replacing itself an agent makes the download prove it runs on
  that machine *and* that it can send a reading the server accepts; the binary
  it replaces is kept beside it, so `gwatch-agent rollback` is a repair that
  needs no network. New commands: `gwatch-agent update [--check]` and
  `gwatch-agent rollback`; `--auto-update=false` (or
  `GWATCH_AGENT_AUTO_UPDATE=off`) turns the automatic half off.
  See [`docs/HARDWARE.md`](docs/HARDWARE.md#keeping-agents-up-to-date) and
  [`docs/RELEASING.md`](docs/RELEASING.md#releasing-the-agent).
- **GWatch shows which machines are behind.** Settings › Hardware counts the
  machines running an older agent than the newest release and marks them in
  the table, and a machine's hardware panel marks its own. It is a label and
  nothing more — there is no button, because there is deliberately no
  mechanism. `GET /api/agents/latest` reports the newest agent release
  (cached; admin only).

### Changed

- Agent releases are no longer part of a GWatch release. `gwatch-agent-*`
  binaries and `gwatch-agent-setup-<version>.exe` now come from the agent's
  own `agent-v…` release rather than from `v…`. An agent and a GWatch
  installation can never be offered each other's build: the two are separated
  by tag prefix and by exact asset name, and that separation is tested.
- Agents spread their reporting over the interval rather than all reporting on
  the same second, and spread their update checks over a window. A fleet set
  up by one script no longer acts in lockstep.

## 0.3.0

The 2026-09-21 sprint: everything labelled `sprint-plan` in the tracker.

### Added

- **Hardware metrics stand on their own** (#60). A machine is still one
  hardware check, but every reading it takes — processor, memory, swap, load,
  each disk and its inodes, each network interface's traffic, each disk's
  throughput — now has its own value, status, threshold, chart, incident and
  trigger variable. Thresholds are a list (`metricThresholds`) with a family
  entry such as `disk` and optional instance entries such as `disk:/srv`;
  the old flat fields are converted the first time a check is saved. Warnings
  and their clearing are recorded per metric, alert mails list every metric
  that is not up, `/api/history?metric=disk:/srv` charts one series, the
  node page groups the charts by family, triggers gain a `metric_over`
  condition and `{{metric}}`/`{{metrics.<key>}}` placeholders, and bulk edit
  can set a threshold across many machines. `docs/HARDWARE.md` has the new
  "One check, many metrics".
- **Notification rules across nodes** (#31, first version). Settings › Rules
  holds rules such as "tell me when two of my three DNS servers are down": a
  list of status conditions on any checks or whole nodes (down, or degraded
  meaning degraded-or-worse), joined by all, any or at-least-N, with the same
  actions triggers use, a cooldown and an optional notice when the rule
  clears. Rules are re-evaluated on every status change and when maintenance
  or silencing changes, fire once per crossing, record `rule_fired` and
  `rule_cleared` in the timeline, and keep their state across restarts.
  Per-node alerts and dependency-aware suppression are untouched. Hold timers
  and metric conditions are deliberately left for later.
- **JSON checks record the value they read** (#55). Tick "Record this value"
  on a JSON check and the number at its path is stored with every run, charted
  on the node page with its unit, exported per metric as CSV, and optionally
  held to warning and critical thresholds above or below. A value that is not
  a number is kept as text in the result details, as before. `docs/RECIPES.md`
  shows it against a Pi-hole.
- **Settings › AI & MCP** (#56) explains the MCP companion, walks through
  setting it up (a read-only API key, `go install`, a copyable client
  configuration with this install's address filled in, `gwatch-mcp check`),
  lists what an assistant can and cannot do, and offers a downloadable
  **agent skill** that teaches an assistant how to use GWatch well. The skill
  is versioned on its own (`skill/VERSION`); GWatch remembers who last
  downloaded it and when, and quietly notes on that card when a newer one has
  shipped.
- **Your own PostgreSQL or MySQL/MariaDB server can hold the database**
  (#34). SQLite stays the zero-configuration default and every release is
  tested against it first; an administrator who already runs a database
  server can point GWatch at it with the `--db-*` flags, `GWATCH_DB_*`
  variables or **Settings › Database**, which tests the connection and saves
  `database.json` beside `gwatch.db` for the next restart. `gwatch
  migrate-db` copies an existing SQLite install across in one go, backups and
  restores are the same encrypted archive whichever database they came from,
  and CI runs the store, API, engine and backup suites against PostgreSQL 16
  and MySQL 8 as well as SQLite. `docs/DATABASE.md` covers choosing,
  configuring and moving. The backup format is now version 2 and carries
  accounts, API keys, agents, wallboards and hardware readings; older archives
  still restore.
- **A Docker image** (#51): `ghcr.io/jxburros/gwatch`, published for
  linux/amd64 and linux/arm64 on every release, with a Compose file and
  `docs/DOCKER.md` covering the `/data` volume, the first administrator
  account, ping capabilities, discovery under bridge networking, hardware
  readings (the container's, not the host's) and upgrading by pulling.
- **The SQLite driver is a build-time choice** (#48). `modernc.org/sqlite`
  stays the automatic default and is what releases ship; `-tags
  sqlite_ncruces` or `-tags sqlite_cgo` swap in `ncruces/go-sqlite3` or
  `mattn/go-sqlite3` for people building from source. CI runs the store under
  all three, and Settings and `/api/health` say which one a build uses.
- **An accessibility pass across the interface** (#38). Dialogs name
  themselves by their heading, make the page behind them inert and hand focus
  back where it came from; menus, wallboard tabs and the dashboard grid work
  from the keyboard (arrows move a widget, Shift+arrows resize it); every
  chart canvas can be stepped through with the arrow keys and offers a "View
  as table" alternative; form errors are tied to their fields; the focus ring
  survives forced-colours mode; and the skip link, which the hash router had
  quietly broken, works again. Playwright runs axe over every route in both
  themes in CI so it stays that way.
- **A user guide** (`docs/USER-GUIDE.md`). The repository documented the API,
  installation and operations but never the interface itself. Twenty sections
  covering every screen and setting in order: the first half-hour, the layout,
  nodes and checks, a reference for all ten check types and their options,
  discovery, bulk edit, dashboards, charts, incidents and audit, alerts and the
  four ways they get narrowed, notification rules, automation, hardware and the
  agent, wallboards, all seventeen settings tabs, accounts and remote access,
  backups, the keyboard contract, the corners that are easy to never find, and
  troubleshooting. Linked from the README and from the in-app Help page.

### Changed

- **The interface is responsive, not a phone app** (#54). The one piece of
  code written for a finger — a touch handler on charts — is gone, along with
  the comments that described narrow layouts as phone layouts. Narrow windows
  and tablets still read fine; GWatch is built for a desk.
- **The documentation was audited against the code.** `docs/API.md` gained
  reference material it never had — the Node and Check objects, every check
  type's configuration fields, a table of all 31 event types, and the
  `settings.general`/`alerts`/`retention` fields with their defaults — and its
  stream, version and dashboard entries were corrected. `docs/RELEASING.md`
  had the wrong CI job count and a repository layout missing ten packages.
  `ROADMAP.md` still listed SNMP as a non-goal after it shipped in 0.2.2.
  `docs/DATABASE.md` gained `GWATCH_DB_PATH`.

## 0.2.2

The 2026-09-19 sprint: everything labelled `sprint-plan` in the tracker.

### Added

- **SNMP checks for routers, switches and access points** (#39). A check
  reads any list of OIDs on a schedule, with warning and critical thresholds
  per OID; counters are charted as a per-second rate (bits per second with a
  scale of 8), presets cover the standard MIBs (sysUpTime, IF-MIB traffic,
  errors and link state, HOST-RESOURCES load), and "Walk this device" lists
  what a device exposes so you can tick the readings you want. SNMP
  credentials and agent metrics tokens are now encrypted at rest and masked by
  the API. See `docs/SNMP.md`.
- **Discovery** (#42). From the Nodes page, sweep one or more IP ranges and
  see every device that answers with its name, round trip and open ports, then
  bulk-add the ones you pick as nodes with a template suggested for each.
  Administrator only, pure Go, no `nmap`.
- **Bulk edit** (#27). Nodes › Bulk edit, and `PATCH /api/nodes/bulk` behind
  it, applies one change — interval, timeout, retries, failure threshold,
  enabled, alert overrides, latency, packet-loss and certificate thresholds,
  ping method, groups, tags, importance, dependency — across as many nodes and
  checks as you tick, in one transaction with one entry in the timeline.
- **Nodes can belong to more than one group** (#35). Nodes carry a `groups`
  list; filters, maintenance windows, dashboard and wallboard group panels
  match any of a node's groups. The single `group` field stays for one release
  as a deprecated alias for the first group.
- **Status indicators in the header** (#26). A row of orbs under the page
  title: blue when nothing is connected, green when all is clear, and one per
  firing rule in yellow, orange or red. The rules are yours to set under
  Settings › Indicators and come seeded with sensible defaults.
- **Compact lists by default, with a "Breathing room" mode** (#32). Node
  lists, check rows and settings lists take less space; Settings › Appearance
  restores the old spacing.
- **A "Ping only" template** (#41), and the "Router" template is now
  "Network device" (#40) — a switch or access point is the same thing to
  monitoring rules.
- **Ping reports the standard deviation of each run** (#30) alongside min,
  max, average, jitter and loss, on the check card, in the inspector and in the
  results CSV. The packet count is editable per check.
- **A ping method setting** (#47). Settings › General › Ping chooses between
  the automatic socket order, the built-in sender alone and the system `ping`
  command, with a per-check override. GWatch now sends its own ICMP echo
  requests; the third-party ping library is gone.
- **Settings shows where your data lives** (#33): the data directory,
  `gwatch.db`, `gwatch.key` and the backups folder, on the Retention and
  Backups tabs. It is never a temp folder on any platform; `docs/INSTALL.md`
  says where it is on each.
- **A test suite for the web interface** (#21): the shared formatting, chart
  and DOM helpers and every view's happy path against the mock API, run by
  `npm test` and in CI.
- **CI runs on Linux and macOS as well as Windows** (#17), with the race
  detector on the concurrency-heavy packages and `govulncheck` on both modules.
  Building GWatch now needs Go 1.26: the scan found standard-library fixes that
  never reached the 1.24 line, so the module moved to a supported toolchain.

### Changed

- **The default port is 7230** (#25), no longer 8080, so a fresh install does
  not collide with the next development server. Existing installs are
  unaffected: the service registers its `--listen` address explicitly.
- **Hardware checks show each metric's thresholds prominently** (#29). A
  machine is still one node with one hardware check; every metric in it has
  its own warning and critical pair, now grouped and labelled in the editor
  with the defaults visible. `docs/HARDWARE.md` explains why it is one check.
- **The dark theme's grey text is brighter** (#37), and every text and
  background pair in both themes now clears WCAG contrast; a test keeps it so.
- **The README is a short overview** (#22) that links to the documents in
  `docs/`, which now carry what it used to repeat. The mock demo interface
  (`?mock=1`) wears an undismissable "Mock data" banner.
- **The MCP companion is released with its own `mcp/vX.Y.Z` tags** (#11), so
  `go install .../mcp/cmd/gwatch-mcp@latest` resolves. `docs/RELEASING.md`
  explains when to bump `mcp/VERSION`.

### Fixed

- **macOS reported zero total memory.** The collector read `hw.memsize`
  through a call that trims a trailing NUL byte, which every memory size
  below 2^56 ends in, so the eight-byte value came back seven bytes long and
  was refused. The first macOS CI run (#17) caught it; the value is now read
  raw.
- **The page no longer flashes when live data arrives** (#28). The nodes
  list, the dashboard widgets and the node history are updated in place
  instead of being rebuilt, and chart canvases paint their own themed
  background.
- **Custom endpoint tokens can no longer be guessed without limit** (#15).
  A wrong `/hook/` token counts against the same per-IP failure budget as a
  wrong password and answers 429 with `Retry-After` once it runs out; an
  unknown slug is charged too. A correct token restores the budget.
- **The cross-site write guard covers the legacy access password** (#16),
  and every request carrying a body must declare `Content-Type:
  application/json`; form-shaped bodies are refused with 415.
- **Every response carries security headers** (#19): a
  `Content-Security-Policy` of `'self'`, `X-Content-Type-Options: nosniff`,
  `X-Frame-Options: DENY` and `Referrer-Policy: same-origin`. The projected
  wallboard is the one page that may be embedded in another site's frame.
- **The database's schema version is enforced** (#20). A database written by
  a newer GWatch is refused instead of silently opened; an older one gets a
  copy taken beside it before it is migrated, and numbered data migrations
  now have a home.
- **The authorization table's path matcher agrees with the router** (#22).
  A trailing slash is its own segment, as it is to `http.ServeMux`, and a
  test holds the two to the same answer across encoded, doubled, dotted and
  aliased paths.

## 0.2.1

### Added

- **Updates are yours to drive.** Settings › Updates now lists every release
  the project has published, and installs the one you pick: the newest, or an
  earlier one you would rather have. Pre-releases are shown when you ask for
  them, marked as what they are and installed at your own risk. Whatever you
  choose is downloaded, checked against the pinned signing key and installed
  the same way.
- **GWatch looks for updates by itself.** A check runs shortly after the
  service starts and then once a day (1-720 hours, your choice), and again
  when you open the interface. When something is waiting, an indicator appears
  beside Settings and takes you to Updates; if you would rather be asked
  outright, GWatch offers the update in a dialog as you arrive, which you can
  take, put off, or skip for that version. All of it switches off in one
  place, and off means off: no periodic check, no check on open, no prompt. A
  check asks GitHub for the list of releases and tells it nothing about you or
  what you monitor.

### Fixed

- **A chart no longer hides the result that was just recorded.** Result
  timestamps are stored to the millisecond and a raw history window excluded
  its own upper bound, so a check run by hand and looked at in that same
  millisecond charted as empty. The window now includes `now`, as the host
  sample history already did.

## 0.2.0 — second beta

### Changed

- **Hardware is no longer a section of its own.** A machine is a node like any
  other: pairing or registering one creates the node it is watched as, with a
  hardware check that is completed and switched on as soon as the machine
  reports, and its readings and history are shown on that node. Links to the
  old hardware pages follow the machine to its node.
- **The header carries the page name and nothing else.** Page subtitles are
  gone; Settings has moved to the upper right, where it belongs to the
  application rather than to any page; and the live counts and each page's own
  controls have moved to a bar beneath the header.
- **Every size is relative.** The interface is stated in `rem` against a root
  that grows with the viewport, so it scales with the screen instead of being
  pinned to one laptop's pixels. The left rail scrolls and tightens rather than
  clipping an icon on a short window.

### Added

- **Wallboards you configure, and can put on a screen.** As many boards as you
  like, each arranged from its own panels — headline, counts, clock, attention,
  groups, a node grid, trends, certificates, maintenance, service health or a
  line of text — with its own columns, theme, type size and refresh, and its
  own visual identity rather than the application's.
- **Projecting a wallboard.** Switch projection on for a board and GWatch gives
  you an address any browser on your network can open. That screen never signs
  in and can read that one board and nothing else; the address is shown only to
  an administrator, is never echoed back to the display, and switching
  projection off or changing it revokes the old one at once. Every change is in
  the audit trail. See [`docs/API.md`](docs/API.md#wallboards).
- **An inverted mark for dark backgrounds** (`web/logo-dark.svg`), used in the
  interface's dark theme and on the installers' header strip.
- **The setup programs wear the application's skin**: graphite field, a teal
  accent rule under the header, and the monospaced face in the fields that hold
  a port, an address or a pairing code.
- **Releases are signed, and updates install themselves.** A release signing
  key is pinned into the binary
  ([`internal/update/release_keys.txt`](internal/update/release_keys.txt)), so
  Settings › Updates can verify a download against it before replacing the
  running copy. 0.1.0 shipped without a key and could only tell you a newer
  version existed.

## 0.1.0 — first beta

The first released version. Everything below is new, so this entry describes
what GWatch is rather than what changed.

### Monitoring

- Check types: Ping, HTTP/S, HTTPS certificate expiry, TCP port, DNS, Keyword,
  JSON, Custom script and System (a machine's processor, memory, disk and
  throughput).
- A scheduler with per-node intervals, immediate retries inside a run, and a
  consecutive-failure threshold before a check is called down — so one dropped
  packet is not an incident.
- Incidents with acknowledgement, suppression and maintenance windows.
- Long-term history in an embedded SQLite database, with configurable retention.
- Automation: triggers that run webhooks, commands and scripts on a state
  change, with Slack, Teams, ntfy and Pushover actions built in.
- Email alerts through your own SMTP server, with a cooldown so an outage does
  not become an inbox full of mail.

### Hardware agents

- `gwatch-agent`, a separate one-directional reporter for the other machines on
  your network. It sends readings out and is given no way in.
- Pairing codes: an eight-character code, good for one machine for fifteen
  minutes, single-use and cancellable, exchanged for a token that can do exactly
  one thing — submit that machine's readings.
- Built for Windows, Linux (including 32-bit ARM) and macOS.

### Interface

- A web interface on `127.0.0.1:8080`, optionally on the LAN, with dark and
  light themes and a user-chosen accent.
- Dashboard, node detail, charts, incidents, hardware, audit log and a
  wallboard view.
- A first-run guided tour, a searchable Help page, and opt-in contextual tips.

### Access and data

- Accounts with roles, or a single access password; API keys for scripts.
- An audit log of configuration changes and sign-ins.
- Encrypted backups on a schedule, and a documented restore-to-a-new-machine
  procedure.
- In-app updates from GitHub releases, installed only when the download's
  ed25519 signature verifies against a key pinned into the running binary.

### Installing

- Two Windows setup programs: one for GWatch, one for the agent. Both carry the
  licence and a digest of the terms, register their service, and are branded to
  match the app.
- PowerShell scripts for an unattended or built-from-source install.

### Known gaps at 0.1.0

- Neither setup program is Authenticode-signed, so Windows SmartScreen warns
  about an unknown publisher.
- Automation endpoint tokens and trigger action payloads are stored unencrypted
  in the database; anyone who can read `gwatch.db` can read them. The SMTP,
  access and backup passwords are encrypted. See
  [`docs/PRIVACY.md`](docs/PRIVACY.md).
- Re-running a setup program over an existing install keeps the original port
  and LAN settings; changing them means uninstalling first.

### Release infrastructure migration — 2026-10-20

- Adopt the unified release manifest, version-advance validation, manual main-only planning, complete artifact staging, and verified draft publisher. Preserve local packaging, existing updater trust, and distribution identities.
- Validation: shared engine tests, workflow lint, and prepared manifest/version checks. Native/hosted consumer release dry runs and publication remain unverified until the migration is merged and credentials/settings are checked.

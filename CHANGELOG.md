# Changelog

Versions follow [semantic versioning](https://semver.org). GWatch is pre-1.0,
which is deliberate and carries its usual meaning: it works and it looks after
your data, but the interface and the JSON API can still change between releases.
1.0.0 is reserved for the first public, stable release.

The version a build reports comes from the [`VERSION`](VERSION) file, and a
release is cut by tagging `v<VERSION>`. CI refuses to publish a tag that
disagrees with the file — see [`docs/RELEASING.md`](docs/RELEASING.md).

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

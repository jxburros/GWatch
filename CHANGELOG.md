# Changelog

Versions follow [semantic versioning](https://semver.org). GWatch is pre-1.0,
which is deliberate and carries its usual meaning: it works and it looks after
your data, but the interface and the JSON API can still change between releases.
1.0.0 is reserved for the first public, stable release.

The version a build reports comes from the [`VERSION`](VERSION) file, and a
release is cut by tagging `v<VERSION>`. CI refuses to publish a tag that
disagrees with the file — see [`docs/RELEASING.md`](docs/RELEASING.md).

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

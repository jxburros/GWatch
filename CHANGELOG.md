# Changelog

Versions follow [semantic versioning](https://semver.org). GWatch is pre-1.0,
which is deliberate and carries its usual meaning: it works and it looks after
your data, but the interface and the JSON API can still change between releases.
1.0.0 is reserved for the first public, stable release.

The version a build reports comes from the [`VERSION`](VERSION) file, and a
release is cut by tagging `v<VERSION>`. CI refuses to publish a tag that
disagrees with the file — see [`docs/RELEASING.md`](docs/RELEASING.md).

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

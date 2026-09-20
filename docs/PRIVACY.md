# Privacy policy

This page describes what GWatch does with data. The whole answer fits in one
sentence: it keeps everything on your own computer and sends nothing to JX
Holdings, LLC.

> **Plain-language document, not legal advice.** Like the rest of GWatch's
> documentation, this is written in ordinary English rather than legalese. It
> has not been reviewed by a lawyer and it is not legal advice. If you run
> GWatch somewhere that a privacy regime applies to — a workplace, a client
> network, anywhere covered by the GDPR, the UK GDPR, the CCPA or similar — get
> your own advice about your obligations. Ours are simple, because we hold
> nothing.

## We collect nothing, because there is nowhere for it to go

GWatch is local-first and self-hosted. There is no GWatch cloud, no vendor
backend, no account, no licence server, and no activation. JX Holdings, LLC
operates no service that GWatch talks to and therefore receives no data about
you, your network, your checks, your alerts, or the fact that you installed it
at all.

This is not a promise about how carefully we handle your data. It is the
absence of the data. We could not produce a record of your monitoring if
someone subpoenaed us for one, because no such record was ever created outside
your machine.

Concretely:

- **No telemetry.** No usage analytics, no crash reporting, no "anonymous
  statistics", no install ping, no heartbeat. There is no analytics SDK in the
  binary and no tracker in the web interface.
- **No accounts.** Nothing to register, nothing to sign in to except the
  accounts you create inside your own copy.
- **No third-party resources in the interface.** The fonts (Barlow and Kode
  Mono) are shipped inside the binary and served from your own machine, so
  loading the UI does not fetch anything from the internet — no CDN, no font
  service, no referrer leaving your browser.

## What GWatch stores, and where

Everything lives in one directory on the computer running GWatch:

| Platform | Default location |
| --- | --- |
| Windows | `%ProgramData%\GWatch` (normally `C:\ProgramData\GWatch`) |
| Linux | `$XDG_DATA_HOME/gwatch`, otherwise `~/.local/share/gwatch` |
| macOS | `~/.local/share/gwatch` |

Override it with `--data-dir` or the `GWATCH_DATA_DIR` environment variable.
The directory holds:

- `gwatch.db` — an embedded SQLite database, the single home for everything
  below;
- `gwatch.key` — the key file used to encrypt secrets stored in that database,
  created on first run with mode `0600`;
- `logs/` — the service log;
- `backups/` — encrypted backup archives, if you make any.

Inside the database:

- **Nodes and checks** — the hosts, addresses, URLs and names you monitor,
  their groups, tags, notes and dependencies.
- **Check results and history** — every raw result for the retention period you
  set, then 5-minute, hourly and daily rollups. Latency, packet loss, status
  codes, resolved addresses, certificate issuers and expiry dates, final URLs,
  response snippets for keyword and JSON checks, and the output of custom
  script checks.
- **Hardware readings** — processor, memory, swap, filesystem, network and disk
  throughput for this computer and for any machine you install the agent on,
  with the machine's hostname, operating system and architecture.
- **Incidents and the audit log** — outages, recoveries, warnings, maintenance
  windows, configuration changes, backups and restores, service start and stop,
  sleep gaps, and your own notes. Sign-ins and failed sign-in attempts are
  recorded with the IP address they came from, and every change carries the name
  of whoever made it.
- **User accounts** — usernames, roles, and passwords stored only as argon2id
  hashes. A hash cannot be turned back into a password.
- **Sessions** — stored as sha256 digests of the session token, with the client
  address the session was last seen from.
- **API keys** — stored only as a sha256 digest plus a short display prefix. A
  key is shown to you once, at creation, and never again.
- **Agent tokens** — likewise stored only as a hash plus a prefix. See
  [`HARDWARE.md`](HARDWARE.md) for why an agent token is worth very little even
  if it leaks.
- **Settings**, including your SMTP host, username and password, the legacy
  shared access password, and your scheduled-backup password. Those three
  secrets are encrypted in the database with the `gwatch.key` key file; the rest
  of the settings are stored as-is.
- **Automation** — your triggers and custom endpoints, including their action
  configuration and each endpoint's token. These are **stored in the clear**: a
  Slack or Teams webhook URL, an ntfy or Pushover credential, or an API token
  you paste into a webhook action sits readable in `gwatch.db`. Protect the file
  accordingly — see [`DISCLAIMER.md`](DISCLAIMER.md).

None of this is transmitted anywhere by GWatch itself. Exports (CSV, PNG, JSON
configuration, the service log) and backup archives are files GWatch hands to
you; where they go afterwards is entirely your decision.

## What GWatch sends over the network

Only what you configure, and only to destinations you chose.

1. **The checks you create.** ICMP echo requests, TCP connections, HTTP and
   HTTPS requests, DNS queries and TLS handshakes to the hosts you told GWatch
   to watch. This is the product doing its job. The corollary is in
   [`TERMS.md`](TERMS.md): only point it at things you are allowed to probe.
2. **Alert email**, if you configure it — sent through your own SMTP server,
   with your credentials, to the addresses you nominate. GWatch has no mail
   relay of its own, so if you do not configure SMTP, no email is ever sent.
3. **Update checks.** A check makes a request to `api.github.com` for the
   GWatch repository's releases, and installing an update downloads the asset
   from GitHub. Nothing about your installation is sent with that request
   beyond what any HTTPS request necessarily reveals — your IP address and the
   fact that something asked about GWatch releases. GitHub's handling of that
   request is covered by GitHub's own privacy policy.

   GWatch checks on its own as well as when you press **Check now**: shortly
   after the service starts, once every 24 hours after that (or whatever
   interval you set), and when you open the web interface. This is the only
   thing GWatch does that reaches outside your network without you asking, and
   it is switchable: turn off **Check for updates automatically** in
   Settings › Updates and GWatch contacts nobody until you press the button.
   Nothing is installed without you choosing to install it, whether or not
   automatic checks are on.
4. **Automation you set up.** Triggers and custom endpoints send exactly what
   you told them to send, to the webhook URL, Slack or Teams workspace, ntfy
   server or Pushover account you configured, or run the command you wrote on
   the local machine. If you point a trigger at a third-party notification
   service, that service sees whatever the message contains, and their privacy
   policy applies to it — not ours.
5. **Agent traffic**, if you use `gwatch-agent`. The agent connects outwards to
   *your* GWatch server and posts that one machine's hardware reading. It is
   one-directional: GWatch never connects back to the agent and holds no
   credential for the machine. (Serve mode reverses this and is off unless you
   ask for it.) Nothing goes to any third party.

There is no sixth item. If you disconnect the machine from the internet and
monitor only your LAN, GWatch is fully functional and makes no outbound
connection beyond your own network.

## You are the data controller

Because the data never leaves your machine, you hold it and you decide about
it. In data-protection terms, you are the controller for everything GWatch
stores, and JX Holdings, LLC is not a processor for you — we have no access to
the data and no role in handling it.

If you are running GWatch in a context covered by the GDPR, the UK GDPR, the
CCPA/CPRA, or a comparable regime, note that some of what GWatch records can be
personal data in your hands:

- IP addresses, in check results, sign-in records, session rows and agent
  records;
- usernames and account activity in the audit log;
- hostnames and machine details of devices belonging to identifiable people;
- whatever ends up in your own notes, trigger payloads and custom script output.

Your obligations — lawful basis, notice to the people involved, retention
limits, access and deletion requests, security of the store — are yours. GWatch
gives you the tools rather than the answers: configurable retention with
automatic rollup and deletion, per-node deletion, a full audit log with CSV
export, and encrypted backups. What you do with them is your call.

Deleting data is straightforward: delete the node or check to remove its
history, lower the retention settings to shorten what is kept, or delete the
data directory to remove everything. Uninstalling on Windows offers to delete
`C:\ProgramData\GWatch` for you.

## Children

GWatch is a network administration tool, not a consumer service, and is not
directed at children. It has no user-facing sign-up and collects nothing about
anyone.

## Changes to this policy

The current version is always the one in the repository at
[`docs/PRIVACY.md`](https://github.com/jxburros/GWatch/blob/main/docs/PRIVACY.md);
its history is the git history of that file. If GWatch ever gained a feature
that sent data anywhere new, that change would be described here and in the
release notes before it shipped — and it would still be something you had to
switch on.

## Contact

Questions about this policy: JX Holdings, LLC, through the project at
<https://github.com/jxburros/GWatch>. Please do not send us your monitoring
data; we do not want it and have nowhere to put it.

## Related

- [`TERMS.md`](TERMS.md) — the terms you accept by using GWatch
- [`DISCLAIMER.md`](DISCLAIMER.md) — security choices and who carries the risk
- [`REMOTE-ACCESS.md`](REMOTE-ACCESS.md) — reaching GWatch from outside your
  network without exposing it
- [`LICENSE`](../LICENSE) — what you may do with the code

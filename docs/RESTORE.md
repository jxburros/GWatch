# Restoring GWatch on a new machine

This is the supported path for moving GWatch from one computer to another (or
recovering after a full reinstall): create a backup on the old machine, install
GWatch fresh on the new one, and restore the archive there. It works whether the
backup was made by hand (Settings › Backups › Create a backup) or automatically
(Settings › Backups › Automatic backups — see [`API.md`](API.md#backups) for the
scheduling options).

## 1. Get a backup archive

On the machine you are moving away from:

1. Open **Settings › Backups**.
2. Under **Create a backup**, choose a password (or reuse the one configured for
   automatic backups) and tick **Include performance history and events** if you
   want the new machine to keep the existing charts and incident timeline, not
   just the configuration.
3. Click **Create backup**, then **Download** the `.gwbackup` file it produced and
   copy it to the new machine (USB drive, network share, cloud storage — GWatch
   itself never uploads it anywhere).

If automatic backups are enabled, any archive listed under **Backups on this
computer** already satisfies step 2-3; download the newest one instead.

## 2. Install GWatch on the new machine

Follow the normal install steps for the platform: [`docs/INSTALL.md`](INSTALL.md)
for Windows, or build/run the binary directly on Linux/macOS (see the main
[`README.md`](../README.md#install)). Start the service once so it creates its data
directory (`gwatch.db`, `logs/`, `backups/`) and finishes first-run setup, then
open the web interface.

## 3. Restore the archive

### Via the UI

1. Open **Settings › Backups** on the new machine.
2. Under **Restore from a file**, choose the `.gwbackup` file you copied over,
   enter its password, and decide whether to also restore history and events
   (only available if the archive was created with history included).
3. Click **Restore from file**. GWatch reads the archive and shows what it
   holds, and lists everything in it that matters for security (see
   [Before it is applied: the preview](#before-it-is-applied-the-preview)).
   Nothing has changed yet.
4. Read the list, and click **Restore** to go ahead. GWatch replaces its
   configuration with the archive's nodes, checks, dashboards, saved charts,
   maintenance windows, triggers, custom endpoints and settings, then reloads
   the scheduler.

An archive already in **Backups on this computer** is restored the same way from
its **Restore** button: the password first, then the preview, then the restore.

### Via the API

Preview first (optional, and changes nothing):

```sh
curl -sS -X POST http://127.0.0.1:7230/api/backups/inspect \
  -F "file=@gwatch-backup-20260101-020000-full.gwbackup" \
  -F "password=your-backup-password"
```

Then restore:

```sh
curl -sS -X POST http://127.0.0.1:7230/api/backups/restore \
  -F "file=@gwatch-backup-20260101-020000-full.gwbackup" \
  -F "password=your-backup-password" \
  -F "includeHistory=true"
```

From `127.0.0.1` no credentials are needed (unless sign-in on this computer is
required). From elsewhere, send an administrator's: a signed-in administrator's
`gwatch_session` cookie, or the legacy access password as basic auth
(`-u any:password`). API keys are refused on every backup route. A successful response looks like:

```json
{ "ok": true, "nodes": 12, "checks": 31, "results": 481203, "rollups": 9120, "events": 214, "history": true,
  "skippedUsers": [], "skippedAgents": [], "safetyBackup": "gwatch-backup-20261023-101500-full.gwbackup" }
```

Before it changes anything, a restore takes a full backup (history included) of what
is there now, encrypted with the same password, and names it in `safetyBackup` and in
the event timeline. If the restore was a mistake, restore that file to go back. If that
backup cannot be written (a full disk, say), nothing is restored. A restore carries on
to the end even if the browser that asked for it is closed.

## Before it is applied: the preview

A backup is only as trustworthy as whoever had the file and its password. Before a
restore is applied, the restore dialog (or `POST /api/backups/inspect`, see
[`API.md`](API.md#backups)) shows what the archive holds (nodes, checks, triggers,
endpoints, rules, dashboards, wallboards, accounts, API keys, agents, and how much
history) and, under **Check these before you restore**, everything in it that matters
for security:

- **Accounts** in the archive that are not on this machine, with administrators
  marked, and whether they will be added (when you tick **Re-create accounts and
  agents…**, or on a machine with no accounts yet) or left out. The same for
  **agents**.
- **API keys** this machine does not have. These are always added; a key revoked
  here is still known here, so it does not come back.
- Everything that **runs a command on this computer**: script and git actions of
  triggers, custom endpoints and notification rules, and checks that run code
  (custom checks, hardware checks that read a URL).
- **Endpoints anybody can call without a token.**
- Changes to **who can reach GWatch and how they sign in**: LAN access turned on or
  off, the access password for other devices set, replaced or removed, signing in on
  this computer, "behind an HTTPS proxy", the listening port, password hashing. (The
  trusted reverse proxies are a command-line setting, `--trusted-proxies`, and are
  never in a backup.)

## What a restore checks

The archive's configuration goes through the same checks GWatch applies when each part
of it is saved by hand: the settings as **Settings** saves them, every node and check as
the node editor does (with one relaxation: a check faster than the archive's own minimum
interval is accepted down to the 5-second floor, since raising the minimum leaves older
checks as they were), triggers, custom endpoints and notification rules with their
actions (a script with an unknown interpreter, or placeholders spliced into a custom
interpreter's code without the acknowledgement, is refused), scheduled reports,
network-map widgets, and the accounts, API keys and agents (a known role and scope, a
password hash or token digest, links only to nodes in the archive).

If anything fails, the restore is refused before anything is written, or a safety
backup taken, with every problem listed (`400`, `{"error": "...", "problems": [...]}`),
and the preview lists the same problems so you see them before you try.

## If a restore fails

The **configuration** is restored as one database transaction, on SQLite, PostgreSQL and
MySQL alike: every table it touches (nodes, checks, settings, dashboards, maintenance
windows, triggers, endpoints, rules, charts, wallboards, accounts, keys, agents, and,
with history, the emptying of the old history) is written together or not at all. A
failure anywhere in it leaves the database exactly as it was, and the error says
**Nothing was changed**.

The **history** (results, rollups, events) cannot sensibly go in that transaction: it
can be hundreds of millions of rows, and holding one transaction open for all of them
would block every other write for as long as the import takes and grow the database's
journal to the size of the history. It is imported afterwards, in batches. If that
import fails part-way, the configuration is restored and in force, the history holds
what was imported before the failure, and the response is a `500` with
`"configRestored": true` and the counts imported; the event timeline records "Backup
restored without all of its history". Restoring the archive again, or the safety backup,
puts it right.

While a restore runs, **monitoring is paused**: no check runs, and no results, rollups,
retention passes, scheduled backups or reports are written, so nothing is recorded
against configuration that is half replaced. Monitoring resumes, with the restored
configuration loaded, as soon as the restore ends, whether it succeeded or not. A second
restore started while one is running is refused (`409`).

## What a restore brings over

- All **nodes** and their **checks**, with the same internal IDs, so any
  restored history lines up correctly (dependencies between nodes are
  preserved too).
- **Dashboards**, **saved charts** and **wallboards** (with their projection
  addresses, so a display already showing a board keeps working).
- **Maintenance windows**, **triggers** and **custom endpoints** (automation).
- **User accounts** (with their password hashes), **API keys** and **hardware agents**,
  from supported backup format 2 archives. These are *merged*:
  an account, key or agent already on the new machine is left exactly as it is, so a
  restore never overwrites the password of the administrator doing the restoring, and
  never resurrects a key that was deliberately revoked here. An account or agent in the
  archive that is **not** on this machine — one deleted since the backup was made — is
  left out and listed (`skippedUsers`, `skippedAgents`) unless you tick **Re-create
  accounts and agents that have been deleted since this backup** (`restoreAccounts:
  true`), so an older backup cannot bring back a removed administrator's password or a
  purged machine's token. On a machine with no accounts at all, as when moving to a new
  computer, they are re-created without asking. An agent that is still here keeps its
  link to its node. An archive from an older version has none of these and leaves them
  untouched.
- Optionally, the **history**, which includes what each hardware check recorded: a machine's readings are its check's results, kept like any other check's. (An archive from before that carries a separate list of hardware readings; it is not read, because every reading in it was also a result of a hardware check.)
- **Settings**: general, alerts (including SMTP configuration), retention
  policy, network access, appearance, and the automatic-backup schedule.
- Optionally, **history**: raw results, rollups (5-minute/hourly/daily) and the
  event/incident timeline, when the archive included it and you chose to
  restore it. When history is not restored (including configuration-only
  archives), existing results and rollups for matching check IDs, incidents for
  matching nodes, and the event timeline are kept. Nodes and checks absent from
  the archive are removed, together with their associated history. Existing
  agents keep their links to nodes that remain in the restored configuration.
  Unsupported archive formats are rejected before any database changes.

## Size limits

A restore reads only archives within these limits, and checks all of them before it
changes anything in the database, so an archive that is too large is refused whole
rather than half-restored:

| What | Limit |
| --- | --- |
| The archive file (an upload, or one in the backup folder) | 2 GiB |
| Entries in the archive | 64 |
| `manifest.json` / `config.json`, decompressed | 1 MiB / 256 MiB |
| All history files together, decompressed | 32 GiB |
| Results, rollups or events | 200 million each, and no more than the archive's manifest says it holds |

A backup GWatch wrote is far inside them: the archive is compressed, and two gigabytes of
it is many months of history for hundreds of checks. An upload over the limit is
answered **413** before it is stored. If an archive of your own is refused for its
history, restore it without history (configuration only); if the file itself is over
the limit, shorten retention on the old machine and take a new backup, or move the data
with `gwatch migrate-db` ([`DATABASE.md`](DATABASE.md)), which copies database to
database without an archive.

## What a restore does not bring over

- **The `gwatch.key` file.** It is the key that encrypts the secrets stored in
  the old machine's database, and it belongs to that installation. The archive
  does not need it: it carries those secrets itself, inside its own password
  encryption, and the new machine seals them again with its own `gwatch.key`.
  (The keys that verify signed release updates are built into the program, so
  there is nothing about updates to carry over either.)
- The **access password** for remote/LAN access is restored as part of
  settings (it travels with the config), but SMTP and automatic-backup
  passwords are only ever stored encrypted/hashed as GWatch normally does —
  restoring them onto a new machine works the same as restoring any other
  setting.
- **Browser sessions and pairing codes.** Everyone signs in once more on the new
  machine, and a machine still waiting to be enrolled needs a new code. On a machine
  with no accounts yet, a browser on that machine is an administrator, so you can
  always get in after a restore.
- **`database.json`**, if the old machine kept its data on a PostgreSQL or MySQL
  server ([`DATABASE.md`](DATABASE.md)). Which database the new machine uses is its
  own decision; a backup restores onto any of them.
- Log files under `logs/` are local operational logs and are not part of a
  backup archive.
- Anything you changed on the new machine *before* restoring is discarded —
  restore replaces the whole configuration, not a merge.

## Verifying the restore

After restoring:

1. Check **Settings › Monitor health** — service running, scheduler running,
   database size roughly matching what you expect.
2. Check the **Dashboard** and **Nodes** pages — the same nodes/groups should appear with the
   status you expect once the first check cycle completes.
3. If you restored history, open a node's **Charts** and confirm data extends
   back further than "just now".
4. Open **Audit › Event log** and confirm a "Backup restored" event was
   recorded with the expected node/check counts, and (if you restored history)
   the counts match what you saw in step 1 of the archive's own creation.
5. If alerts are enabled, send a test email from **Settings › Alerts** to
   confirm SMTP settings came across correctly.
6. If automatic backups were enabled, confirm **Settings › Backups** shows
   "Next scheduled backup" so the new machine keeps making its own backups
   going forward.

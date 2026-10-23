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
3. Click **Restore from a file** and confirm. GWatch clears its current
   configuration, imports the archive's nodes, checks, dashboards, saved
   charts, maintenance windows, triggers, custom endpoints and settings, then
   reloads the scheduler.

### Via the API

```sh
curl -sS -X POST http://127.0.0.1:7230/api/backups/restore \
  -F "file=@gwatch-backup-20260101-020000-full.gwbackup" \
  -F "password=your-backup-password" \
  -F "includeHistory=true"
```

Add basic-auth credentials (`-u user:pass` or an `Authorization` header) if the
new machine's remote-access password is set and you are not calling from
`127.0.0.1`. A successful response looks like:

```json
{ "ok": true, "nodes": 12, "checks": 31, "results": 481203, "rollups": 9120, "events": 214, "history": true,
  "skippedUsers": [], "skippedAgents": [], "safetyBackup": "gwatch-backup-20261023-101500-full.gwbackup" }
```

Before it changes anything, a restore takes a full backup (history included) of what
is there now, encrypted with the same password, and names it in `safetyBackup` and in
the event timeline. If the restore was a mistake, or fails part-way, restore that file
to go back. If that backup cannot be written (a full disk, say), nothing is restored.
A restore carries on to the end even if the browser that asked for it is closed.

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

- **The `gwatch.key` file and update-signing configuration**, if present on the
  old machine (used to verify signed release updates). This is deliberately
  outside the backup archive — it is tied to the machine's installation, not to
  the monitoring configuration, and copying signing material between machines
  is a decision to make explicitly, not something a configuration restore
  should do silently. If the new machine needs to verify signed updates the
  same way, set that up separately.
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
2. Check the **Overview** page — the same nodes/groups should appear with the
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

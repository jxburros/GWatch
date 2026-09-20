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

Follow the normal install steps for the platform (see the main
[`README.md`](../README.md#install-on-windows) for Windows, or build/run the
binary directly on Linux/macOS). Start the service once so it creates its data
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
{ "ok": true, "nodes": 12, "checks": 31, "results": 481203, "rollups": 9120, "events": 214, "history": true }
```

## What a restore brings over

- All **nodes** and their **checks**, with the same internal IDs, so any
  restored history lines up correctly (dependencies between nodes are
  preserved too).
- **Dashboards** and **saved charts**.
- **Maintenance windows**, **triggers** and **custom endpoints** (automation).
- **Settings**: general, alerts (including SMTP configuration), retention
  policy, network access, appearance, and the automatic-backup schedule.
- Optionally, **history**: raw results, rollups (5-minute/hourly/daily) and the
  event/incident timeline, when the archive included it and you chose to
  restore it.

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
- **User accounts, sessions and API keys.** They are not in the archive and a
  restore leaves whatever the new machine already has alone. That cuts both
  ways on purpose: restoring a backup never costs you your sign-in, and it
  never resurrects an account or a key you deliberately removed. On a machine
  with no accounts yet, a browser on that machine is an administrator, so you
  can always get in after a restore and create them again under
  **Settings › Users & access**.
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

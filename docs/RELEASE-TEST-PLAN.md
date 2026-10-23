# Release test plan — the release after 0.7.2

This is everything to check by hand before releasing the work on
`claude/friendly-volta-u5cmy2`. CI covers a lot (see the end), but CI has never
touched a real Windows service, a Mac, a real systemd machine you care about, a
real Matter device, a second site, or a database that real people have been
filling for months. Those are what this list is for.

Tick the boxes as you go. Each section says how risky it is:

- 🔴 **Blocker** — a failure here can lose data, lock someone out or leave a
  machine unmonitored. Do not release until it passes.
- 🟠 **Should** — a failure is bad but contained to one feature. Fix it or ship
  the feature switched off and say so in the release notes.
- 🧪 **Experimental** — new, and never run against the real thing. Test it, and if
  it misbehaves, call it "preview" in the release notes rather than holding the
  whole release.

---

## 0. How to run this release safely

- [ ] **Release a release candidate first.** Tag `v0.8.0-rc.1` (and
      `agent-v0.8.0-rc.1`). A tag with a prerelease suffix publishes a GitHub
      *prerelease*, does not move Docker's `latest`, and the updater on stable
      installs skips it (docs/RELEASING.md, "Release candidates"). Install the
      RC on your test machines and work through this list against it — that
      way you are testing the real signed, packaged artefacts, not a local build.
- [ ] **Bump the versions.** `VERSION` and `cmd/gwatch-agent/VERSION` are both
      still `0.7.2`. Both programs changed a lot, so this is `0.8.0` for both
      (the server and the agent version separately; see "The two release trains
      must never cross" in docs/RELEASING.md). Move the CHANGELOG's
      **Unreleased** section under the new version heading and date it.
- [ ] **Merge to `main` through a pull request** and let the whole CI run go
      green on it (Linux, Windows, macOS, Postgres, MySQL, the e2e suite, the
      packaging jobs, the `linux-service` job, the Authenticode dry run). Several
      of those jobs are new and have **never run on GitHub yet** — expect to fix
      one or two.
- [ ] **Take a backup of every real install before upgrading it** (Settings ›
      Backups › Create backup, with history), and copy the archive somewhere off
      the machine. Note the password.
- [ ] **Write down how to roll back** before you need it (section 16).

### Machines you need

| Machine | Used for |
| --- | --- |
| A copy of your current real install (0.7.2), or a machine restored from its backup | Upgrade testing — the most important part |
| Windows 10/11 (a real one, not just CI) | Server installer, agent installer, service, tray, code signing check |
| A Mac (Apple Silicon is fine) | Server and agent as launchd services, disk activity |
| A Linux machine with systemd (Ubuntu/Debian VM is ideal) | `gwatch install`, `.deb` package, agent package, hardened service |
| A second Linux box or VM (or a Pi) | Agent, remote collector, Linux readings |
| Docker | Container image, discovery-in-container note, cgroup limits |
| *Optional:* Fedora/RHEL VM | `.rpm` packages |
| *Optional:* Alpine VM | OpenRC fallback |
| *Optional:* a PostgreSQL or MySQL server | Upgrade and restore on a server database, if anyone uses one |

---

## 1. 🔴 Upgrading an existing 0.7.2 install

Real users already have data. This is the single most important section.

- [ ] **Your own data survives the upgrade.** On a copy of a real install
      (or restore your real backup onto a fresh 0.7.2 first), install the new
      version over it. Then check:
  - [ ] every node, check, group, tag, dashboard, wallboard, rule, trigger,
        endpoint and maintenance window is still there;
  - [ ] history charts still show the old data at 24h, 7d, 30d and 1y;
  - [ ] the event timeline and incidents are intact;
  - [ ] you can sign in with your existing account (password hashes are still
        Argon2id and must keep working);
  - [ ] API keys still work (try one with `curl`);
  - [ ] alert e-mail settings still send (Settings › Alerts › send a test).
- [ ] **The database schema upgrades cleanly.** This release adds columns
      (`nodes.collector_id`; `agents.collector`, `collector_allowed`,
      `collector_seen_at`) and new settings. Watch the log on first start for
      errors. If anyone runs GWatch on **PostgreSQL or MySQL**, repeat the
      upgrade on that backend (`make test-postgres` / `make test-mysql` only
      covers fresh test databases).
- [ ] **The first start after upgrading is not slow.** The rollup backfill now
      runs in chunks and remembers how far it got (#107). With months of
      history, results should keep arriving during the first minutes after
      start, and a second restart should not repeat the backfill.
- [ ] **0.7.2 agents keep reporting to the new server** before they update
      themselves. Leave one old agent running, upgrade only the server, and
      confirm its machine stays healthy.
- [ ] **New agents work with the upgraded server** (pair a fresh one).
- [ ] **Self-update from 0.7.2 works on each platform you support.** From a
      0.7.2 install, Settings › Updates should offer the RC (only with
      "include pre-releases" switched on) or, once released, the stable
      version; apply it and confirm the service comes back on the new version
      by itself. The update signatures now bind the release (`release-sig`), so
      this is the real proof that old installs can take the new release.
- [ ] **Agents self-update from 0.7.2** (`gwatch-agent update`, or wait for the
      automatic update) and come back reporting.

## 2. 🔴 Fresh installs, per platform

### Windows (server)
- [ ] Run `gwatch-setup-*.exe` on a clean machine. Install, open the URL, create
      the first account, add a node.
- [ ] SmartScreen will warn "unknown publisher" unless code signing is set up
      (#120, section 13). Expected for now — note it in the release notes.
- [ ] Run the setup program again over the top (upgrade path) and confirm
      nothing is lost.
- [ ] Uninstall from Settings › Apps and confirm the service is gone.

### Linux with systemd — `gwatch install` (🧪 new in #125)
The service now runs as a dedicated `gwatch` user under a hardened unit.
None of this has run on a real machine yet.
- [ ] `sudo ./gwatch-linux-amd64 install --listen 0.0.0.0:7230` on a clean VM.
  - [ ] `/usr/local/bin/gwatch` exists, `systemctl status gwatch` is active,
        `ps -o user= -C gwatch` says `gwatch`.
  - [ ] Data is in `/var/lib/gwatch` (owned by `gwatch`, mode 700); settings in
        `/etc/gwatch/gwatch.env` (mode 600).
  - [ ] The web UI is reachable from another machine on the LAN.
- [ ] Create the first account (docs/INSTALL-LINUX.md shows the `curl` to
      `127.0.0.1:7230/api/users`).
- [ ] **Ping works without root**: add a ping check to your router; it goes up.
      Settings › Monitor health › Ping says which method it uses (#131).
- [ ] **Re-running install is safe**: run the same `install` again — it updates
      and restarts, keeps your data and settings.
- [ ] **Reconfigure**: `install --listen 0.0.0.0:8080` changes the port.
      Try a port below 1024 (e.g. 80) and confirm it can bind.
- [ ] **Migration from an old install**: on a machine where 0.7.2 was installed
      with `sudo gwatch install` (unit `GWatch.service`, data under
      `/root/.local/share/gwatch`), run the new `install`. The old unit is
      removed, the data moves to `/var/lib/gwatch`, and everything is still
      there afterwards. **Take a backup first** — this moves your database.
- [ ] **Custom checks and script actions now run as `gwatch`, not root.**
      If you (or users) have custom checks or script actions that need root,
      Docker, or files in a home directory, they will now fail. Test one, and
      check the docs' drop-in instructions (`systemctl edit gwatch`) work.
- [ ] **Updating**: Settings › Updates cannot install updates on these installs
      any more (the service can't write `/usr/local/bin`). Confirm the page
      says to re-run the installer, and that re-running `install.sh` with the
      new binary updates it.
- [ ] `sudo gwatch uninstall` keeps data; `sudo gwatch uninstall --purge`
      asks for confirmation and then removes the unit, program, data,
      `/etc/gwatch` and the `gwatch` user.
- [ ] Reboot the VM: the service starts by itself.

### Linux `.deb` / `.rpm` packages (🧪 new in #126)
- [ ] `sudo apt install ./gwatch_*_amd64.deb` on Ubuntu/Debian: it prints the URL
      and how to create the first account; the service is running as `gwatch`.
- [ ] Settings › Updates says updates come from the package manager and does
      not offer to install one.
- [ ] Install a newer `.deb` over it: data and edits to `/etc/gwatch/gwatch.env`
      survive.
- [ ] `sudo apt remove gwatch` keeps `/var/lib/gwatch`; `sudo apt purge gwatch`
      removes it and the user.
- [ ] *Optional:* the same with the `.rpm` on Fedora, and the armhf `.deb` on a
      32-bit Pi OS.
- [ ] `gwatch install` refuses to run on a packaged build (it would create a
      second service).

### macOS (server)
- [ ] `sudo gwatch install`, then work through docs/RELEASING.md
      "Service hosting: manual checks" steps 1–10 (section 4 below).

### Docker
- [ ] `docker compose up` with the documented compose file; create the first
      account with the `docker exec` command from docs/DOCKER.md.
- [ ] Hardware readings inside the container: memory and CPU are now measured
      against the container's limits (#132). Set `mem_limit` and confirm the
      percentage is of the limit, not the host.
- [ ] The root filesystem `/` is now reported (overlay).
- [ ] Discovery in bridge networking shows the note explaining why MAC
      addresses are missing.

## 3. 🔴 The agent (gwatch-agent 0.8.0)

### Install and pair
- [ ] **Windows installer** (🧪 #63): the Connect page's **Test connection**
      button — try a correct address, a wrong one, `http` instead of `https`,
      and a self-signed certificate with and without the box ticked. The final
      page should say "Paired as *name*, service running" with a link that
      opens the machine in GWatch; with a wrong code it should show the actual
      error. **Check the layout at 125% and 150% display scaling** — the new
      button's position has never been seen on a real screen.
- [ ] Silent install still works:
      `gwatch-agent-setup-*.exe /VERYSILENT /SERVER=… /CODE=… /NAME=…`.
- [ ] **Linux packages**: `sudo apt install ./gwatch-agent_*.deb` then
      `sudo gwatch-agent pair --server … --code …`.
- [ ] **macOS**: Homebrew or the install script, then pair.
- [ ] `gwatch-agent probe --server https://your-gwatch:7230` prints one clear
      line and the right exit code for a good and a bad address.

### Power cut and reboot (🔴 #170, the issue a user actually hit)
- [ ] **Pull the power** (or hard-reset the VM) on a machine running the agent,
      a few times, ideally while it is running a check-in. It must come back
      reporting by itself every time.
- [ ] Simulate the damaged-file case: stop the agent, empty `agent.json`
      (`: > agent.json`), start it. It should run from `agent.json.bak` and log
      a warning; the machine stays healthy in GWatch.
- [ ] **Serve mode** (`gwatch-agent serve`) on a machine whose address comes
      from DHCP: reboot it; it must start serving once the network is up.

### Readings held through an outage (#74)
- [ ] Stop the GWatch server for 15 minutes while an agent runs; start it again.
      The gap in that machine's charts fills in, at the right times.
- [ ] Same, but **restart the agent's machine during the outage** — held
      readings survive (`agent-queue.jsonl`).
- [ ] The spool file stays small and disappears once delivered.

### Token rotation (#75)
- [ ] On a node's agent card: **Rotate token…**. Within one check-in
      (≤ 15 min) the agent picks it up, keeps reporting, and the old token stops
      working. Try **Replace now** too.

### Agent control panel (🧪 #64)
Run `gwatch-agent ui` on each OS (and from the Start menu / application menu).
- [ ] It opens in the browser without a terminal.
- [ ] **Recent log** shows lines; no token appears anywhere in it.
- [ ] **Copy diagnostics** — paste it somewhere and check there is no token.
- [ ] **Update now** on an older agent: it updates and restarts. On a packaged
      (apt/Homebrew) agent it should refuse and show the package command.
- [ ] **Re-pair** with a new code.
- [ ] **Start/Stop/Restart** as a normal user: the OS should ask for an
      administrator password (UAC on Windows, a polkit prompt on Linux, the
      admin dialog on macOS). Cancelling the prompt should give a clear message.
- [ ] The Settings card's **Save** button now actually saves (it was broken).

### Windows tray icon (🧪 #53)
- [ ] Tick "Show the agent in the notification area" in the installer. Sign out
      and back in: the icon appears, the tooltip shows the status, and it turns
      to a warning/error icon when you stop the service.
- [ ] Menu: Open control panel, Agent status, Quit.
- [ ] Restart Explorer (Task Manager) — the icon comes back.
- [ ] Uninstall the agent — the icon goes and doesn't come back at next sign-in.
- [ ] Watch for a console window flashing at sign-in (known possible issue).

### Hardware readings
- [ ] **macOS disk activity** (🧪 #185): a Mac now reports disk read/write and
      busy %. Copy a big file and watch it move.
- [ ] **Windows network rates** above ~600 Mbit/s are right (64-bit counters,
      #161) — copy a large file over gigabit and compare with Task Manager.
- [ ] **Linux** (🧪 #132): temperatures appear (and look sane), PSI readings,
      ZFS pools as one entry each with health (if you have ZFS).
- [ ] **A dead network share** mounted on a Linux machine (unplug the NAS)
      does not freeze the readings (#161).

### Services and programs (🧪 #62)
- [ ] On each OS, open the agent card › What it collects › Services to watch;
      the machine's list appears as suggestions. Watch `ssh`/`sshd` (Linux),
      `Spooler` (Windows), something from `launchctl list` (macOS).
- [ ] Stop the service: the check goes critical; start it: back up.
- [ ] Watch a program by name (`firefox`, `chrome.exe`): count goes 0 → n.
- [ ] The same for GWatch's own computer (its hardware panel).

## 4. 🔴 Service hosting on Windows and macOS (🧪 #46)

kardianos/service was replaced with our own code. It has **never run** on the
Windows Service Control Manager or launchd. Do the 10 steps in
docs/RELEASING.md › "Service hosting: manual checks" on Windows (admin prompt)
and on a Mac (`sudo`), once for `gwatch` and once for `gwatch-agent`:

- [ ] 1 Fresh install · 2 Status (also as a non-admin) · 3 Stop/start/restart
- [ ] 4 Crash recovery (kill the process; back within ~10 s)
- [ ] 5 Reboot (runs without anyone signing in)
- [ ] 6 Self-update · 7 Install twice is refused · 8 Upgrade over a 0.7.2 install
- [ ] 9 Uninstall (data kept) · 10 Foreground `run` with Ctrl+C
- [ ] *Optional:* Alpine (OpenRC): `install` writes `/etc/init.d/GWatch` and it
      reads `/etc/gwatch/gwatch.env`.

## 5. 🔴 Backups and restore (#148, #49)

- [ ] **Preview**: Settings › Backups › restore a file. Before anything happens
      you see a summary (accounts that would be added, commands, endpoints
      without tokens, access changes). Cancel — nothing changed.
- [ ] **Safety backup**: restore an older backup. A new archive appears in the
      backup list and the timeline says "Pre-restore backup created"; restoring
      *that* archive puts you back exactly where you were.
- [ ] **Deleted accounts stay deleted**: delete a user, restore a backup taken
      before — the user is not back unless you tick "Re-create accounts…".
- [ ] **A restore onto a fresh install** (moving to a new computer) brings
      everything back, accounts included.
- [ ] **Monitoring pauses and resumes**: during a long restore (with history)
      checks don't run; afterwards they resume and alerts still work.
- [ ] **Close the browser mid-restore** of a big archive — it finishes anyway.
- [ ] **A 0.7.2 backup restores on the new version**, and a new backup made
      with the default (Argon2id) still restores on 0.7.2 (format unchanged).
- [ ] **PBKDF2** (🧪 #49): switch Settings › Users & access › Password hashing to
      PBKDF2. Sign out and in (account is rehashed; you stay signed in
      elsewhere). Make a backup — it must restore on the *new* version.
      ⚠️ A PBKDF2 backup **cannot** be restored by 0.7.2 or older; say so in the
      release notes. Switch back to Argon2id and sign in again.
- [ ] `gwatch migrate-db` refuses while GWatch is running (only if anyone uses
      PostgreSQL/MySQL).

## 6. 🟠 Alerts (#99, #122, #178)

Use a test node you can break on purpose (a VM, or a TCP check on a port you
open and close).
- [ ] **E-mail alerts** still go out for down, recovery and warning.
- [ ] **Grouping** (Settings › Alerts › group within 30 s): stop a machine with
      several checks — **one** e-mail lists them; recovery is one e-mail too.
      Bring it back up within the window — no e-mail at all.
- [ ] **Warning digest** hourly: warnings arrive as one summary on the hour.
- [ ] **Each alert channel you plan to advertise** (🧪 #122), via Settings ›
      Alerts › Also notify › Test: Telegram, Discord, Gotify, Matrix, Twilio SMS,
      PagerDuty, Opsgenie, Teams (Workflows), Slack, ntfy, Pushover.
      ⚠️ Testing PagerDuty/Opsgenie opens a real `gwatch-test` incident — resolve
      it by hand.
- [ ] **PagerDuty/Opsgenie resolve on recovery** — break a check, see the
      incident open, fix it, see it resolve.
- [ ] **Maintenance and silence suppress channels too** — put the node in
      maintenance, break it, nothing is sent anywhere.
- [ ] **Accents and dashes** in a node name (e.g. "Café — rack") arrive intact
      in e-mail and ntfy (#178).

## 7. 🧪 Remote collectors (#123)

Brand new and the biggest change. Needs two networks (a second VM on another
subnet or a friend's/office network over a VPN).
- [ ] On the remote agent's machine set `"collector": true` in `agent.json` and
      restart it; in GWatch turn collecting on on its agent card. Both are needed —
      confirm that with only one, nothing is sent.
- [ ] Create a node on the remote network, set **Check from** to that agent, add
      ping, TCP and HTTP checks. They go up with results arriving from the agent.
- [ ] An SNMP check with a community string, and an HTTP check with a header,
      work from the collector.
- [ ] Break a remote device: the alert fires as normal.
- [ ] **Stop the collector's agent**: after a few minutes its checks go
      "unknown" and you get **one** "collector unreachable" alert, not one per
      check. Start it: a "collector back" event, held results fill the gap.
- [ ] Try to give a collector a custom/script check — it must stay on the server.
- [ ] A restore while a collector is sending: results wait and arrive afterwards.

## 8. 🧪 Matter (#45)

Only tested against a fake server built from the sidecar's source code.
- [ ] Run python-matter-server with the Docker Compose snippet in
      docs/MATTER.md; commission one real device (multi-admin from your
      existing hub, or a pairing code).
- [ ] Add a Matter check, **Load devices**, pick a reading (e.g. a plug's on/off,
      a sensor's temperature). Values match the device's own app.
- [ ] Unplug the device: the check goes down. Stop the sidecar: down with a
      clear message.
- [ ] If anything is off, ship Matter labelled "preview".

## 9. 🟠 Discovery (#110, #131, #133)

- [ ] Sweep your home /24. It finishes noticeably faster than before.
- [ ] Phones/IoT devices that ignore ping now show up (from the ARP table) with a
      MAC address and maker; randomised phone MACs say "private".
- [ ] Add a device from the results; the suggested template is sensible.
- [ ] On Windows, the MAC/vendor column is filled too.

## 10. 🟠 Web interface

- [ ] **Every Settings tab opens** (the Settings page was split into 16 files
      that load on demand, #118): click through all of them, change and save
      something on each, reload.
- [ ] **Network map › Clusters** with your real network: readable, nothing
      overlapping, no line through another box. Try Large size and the
      dashboard widget version.
- [ ] **Charts**: the picker groups checks under their node; "One chart per node"
      works (#184).
- [ ] **Check groups** (prototype, #36): add a group to two checks on different
      nodes; filter the Nodes list and bulk edit by it.
- [ ] **Filters in the address** (#113): filter Nodes and Incidents › Events,
      reload, use Back — the filter stays; Export CSV matches what is shown.
- [ ] **CSV export opens correctly in Excel** with accented names (#178).
- [ ] **Dashboards and wallboards** with live updates for 10+ minutes: no
      flicker, focus stays where you clicked, the browser's network tab isn't
      re-downloading 30-day history every second (#114).
- [ ] A **viewer** (read-only) account sees no secrets anywhere (alert channel
      tokens, check credentials) and can't reach admin pages.
- [ ] Phone-sized screen: the main pages are usable.

## 11. 🟠 Engine behaviour

- [ ] **Stopping is quick** (#105): `stop`/`restart` the service while a long
      script action runs; it stops within ~20 s.
- [ ] **Laptop sleep** (#161): put a Mac/Linux laptop running GWatch to sleep
      for an hour; a "Monitoring paused" event appears on wake.
- [ ] **Deleting a node used by a rule** (#161): the rule is updated (or switched
      off if empty) and the change shows in the event timeline.
- [ ] **Leave it running for a few days** on the RC with real nodes before the
      final release, and watch Settings › Monitor health for internal errors.

## 12. 🟠 Security spot-checks

- [ ] A **read-only API key** cannot reach the new admin endpoints: Matter
      commission/nodes, alert-channel test, collector switch, backup inspect.
      (`curl -H "Authorization: Bearer <key>" -X POST …` → 403.)
- [ ] The agent panel (`gwatch-agent ui`) refuses requests without its cookie
      (open its URL in a private window — nothing loads).
- [ ] Config export (Audit › Configuration › Download JSON, i.e.
      `/api/export/config.json`) contains no passwords or tokens — search the file.
- [ ] `/ingest/results` refuses a token from an agent that isn't a collector.

## 13. 🟠 Release mechanics

- [ ] All expected files are on the release: server binaries (incl. linux/arm),
      agent binaries, both Windows setup programs, `.sha256` and `.sig` for each,
      the six server packages and the agent packages, the MCP companion.
- [ ] `make verify-release` (or `gwatch-sign verify`) passes on the downloads.
- [ ] Docker image `ghcr.io/jxburros/gwatch:<version>` pulls and runs; `latest`
      only moves on the stable tag.
- [ ] winget manifest (if you publish it) points at the right installer.
- [ ] **Code signing (#120)** is *not* set up yet, so the release summary shows
      "Windows binaries are not Authenticode-signed". That is expected. When you
      do set it up (docs/RELEASING.md › Authenticode signing), right-click the
      setup program › Properties › Digital Signatures should show JX Holdings.

## 14. Release notes — things to tell users

- [ ] **Linux `gwatch install`** now runs GWatch as a `gwatch` user, not root;
      custom checks/scripts that needed root need a drop-in; updates are by
      re-running the installer.
- [ ] **PBKDF2 backups** can't be restored by older versions.
- [ ] **Experimental**: remote collectors, Matter, the Windows tray, check
      groups (prototype).
- [ ] **Windows installers are unsigned** (SmartScreen warning) until signing is
      set up.
- [ ] **New alert channels** — PagerDuty/Opsgenie "test" opens a real incident.

## 15. What CI already covers (so you don't have to)

Every push runs: Go tests on Linux/Windows/macOS (plus PostgreSQL and MySQL),
`go vet` and the race detector, the 351 web unit tests, the type check, the 183
browser (Playwright) tests against the mock API, the mock-vs-API contract test,
the server and agent package builds with install/upgrade/purge on a systemd
runner, the full `gwatch install` lifecycle on systemd, the Inno Setup
installer compile, the Authenticode dry run with a throwaway certificate, and
the check that signing happens before hashing. If any of those are red on the
release PR, fix them first — but green CI does **not** replace sections 1–8.

## 16. If something goes wrong after release

- **A user's install broke on upgrade**: they restore the backup they took
  (or the automatic pre-restore/scheduled one) on the previous version.
  Previous binaries stay on the GitHub releases page.
- **Rolling back the server binary**: `gwatch update --rollback` restores the
  previous executable kept by the updater (for installs that self-update;
  a Linux `gwatch install` or package install rolls back by installing the
  previous version's binary or package).
- **A bad release**: publish a fixed patch release quickly rather than deleting
  one — clients that already updated need something newer to move to.
- **Linux `gwatch install` data move**: if the migration from `/root/...` went
  wrong, the original directory is only moved, never deleted — look in
  `/var/lib/gwatch` and restore from the backup taken in section 0.

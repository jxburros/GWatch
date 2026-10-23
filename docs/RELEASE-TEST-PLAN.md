# Release test plan — Windows

Everything to check by hand on Windows before releasing the work on the
`claude/friendly-volta-u5cmy2` branch. It starts with how to build and install
that branch from the ZIP GitHub offers, so you can test it before any release
exists.

CI already builds and unit-tests all of this on Windows on every push (see the
end), but CI has never installed a real Windows service, clicked through a setup
program, pulled the power on a PC, or run for days with real devices. That is
what this list is for.

Each section says how risky it is:

- 🔴 **Blocker** — a failure can lose data, lock someone out or leave a machine
  unmonitored. Don't release until it passes.
- 🟠 **Should** — bad, but contained to one feature. Fix it, or ship with the
  feature off and say so in the release notes.
- 🧪 **Experimental** — new and never run against the real thing. If it
  misbehaves, call it "preview" in the release notes rather than holding the
  release.

---

## Part 1 — Build and install the branch from GitHub's ZIP

### What you need

- [ ] **Windows 10 or 11, 64-bit**, with an account that can run things as
      Administrator. A spare PC or a virtual machine (Hyper-V, VirtualBox) is best,
      because some tests pull the power or uninstall things. A VM also gives you
      snapshots to go back to.
- [ ] **A second Windows PC or VM** for the agent and remote-collector tests (the
      agent can share the server's PC, but a separate machine is more realistic).
- [ ] **Go 1.26 or newer** (the build needs it — `go.mod` says `go 1.26.0`):
      ```powershell
      winget install GoLang.Go
      ```
      Close and reopen PowerShell, then check: `go version` must say `go1.26` or later.
- [ ] **Inno Setup 6** (builds the two setup programs):
      ```powershell
      winget install JRSoftware.InnoSetup
      ```
- Nothing else: no Git, no Node.js, no C compiler. The web interface is built into
  `gwatch.exe`.

### Download the branch

1. Open <https://github.com/jxburros/GWatch/tree/claude/friendly-volta-u5cmy2>
   (you need to be signed in to GitHub if the repository is private).
2. Click **Code ▸ Download ZIP**. (Direct link:
   `https://github.com/jxburros/GWatch/archive/refs/heads/claude/friendly-volta-u5cmy2.zip`)
3. Extract it somewhere short and **not** synced by OneDrive, for example
   `C:\src\`. You get a folder called `GWatch-claude-friendly-volta-u5cmy2`.
   Rename it to `C:\src\GWatch` to keep paths short.
4. Files extracted from a download are marked "from the internet". Clear that so
   Windows doesn't block the build scripts. In PowerShell:
   ```powershell
   Get-ChildItem -Recurse C:\src\GWatch | Unblock-File
   ```

### Build the setup programs

In a normal (not Administrator) PowerShell window:

```powershell
cd C:\src\GWatch
powershell -ExecutionPolicy Bypass -File scripts\build-installer.ps1 -Version 0.8.0-test -AgentVersion 0.8.0-test
```

- `-Version 0.8.0-test` stamps the build so you can tell it apart from the
  released 0.7.2 everywhere (Settings › About, `gwatch version`, the agent card).
  Without it the build calls itself `0.7.2`, the same as the release, which makes
  upgrade testing confusing.
- It takes a few minutes the first time (Go downloads its dependencies).
- When it finishes, `C:\src\GWatch\dist\` holds:
  - `gwatch-setup-0.8.0-test.exe` — the server's setup program
  - `gwatch-agent-setup-0.8.0-test.exe` — the agent's setup program
  - `gwatch.exe` and `gwatch-agent.exe` — the bare programs
- [ ] The build finished without errors and both setup programs exist.

*Optional:* run the automated tests on your machine too (≈5–10 minutes):
```powershell
go test ./...
```

### About testing a ZIP build

- These builds are **not signed** (neither Authenticode nor the update signature
  of a real release), so Windows SmartScreen may not behave the way it will for
  the real download, and **Settings › Updates will say you are up to date** — there
  is no published release newer than `0.8.0-test`. Self-update is tested later, in
  Part 3, against a release candidate.
- Everything else behaves exactly as the release will.

### Install the server from the ZIP build

- [ ] Run `dist\gwatch-setup-0.8.0-test.exe`. Choose the port (7230) and tick
      **Allow other devices on my network to open GWatch** if you want to test from a
      phone or another PC (it adds a Windows Firewall rule for your local subnet).
- [ ] At the end it opens <http://127.0.0.1:7230>. On a fresh install, a browser on
      the same PC is the administrator; create your first account under
      **Settings › Users & access** if you want to sign in from other devices.
- [ ] **Settings › About** shows `0.8.0-test`.

*Alternative without Inno Setup:* build only the program and install it as a
service with the scripts, from an **Administrator** PowerShell:
```powershell
powershell -ExecutionPolicy Bypass -File scripts\build.ps1 -Version 0.8.0-test
powershell -ExecutionPolicy Bypass -File scripts\install.ps1 -Exe .\dist\gwatch.exe -Listen 0.0.0.0:7230
```
(`scripts\uninstall.ps1` removes it; add `-RemoveData` to delete the database too.)

### Install the agent from the ZIP build

- [ ] In GWatch: **Nodes › Add node › A machine with the GWatch agent** — note the
      server address and the pairing code it shows.
- [ ] On the second PC, copy over and run `gwatch-agent-setup-0.8.0-test.exe`.
      On the Connect page enter the server address **with `http://`** (for example
      `http://192.168.1.20:7230` — the agent assumes `https://` if you leave it
      off), the pairing code and a name.

Both programs keep their data in `C:\ProgramData\GWatch` (the server's
`gwatch.db`, `logs\`, `backups\`; the agent's `agent.json`, `agent.log` and so on),
so they can share a PC.

---

## Part 2 — What to test

### 1. 🔴 Upgrading an existing 0.7.2 install

People already have months of data. This is the most important section.

- [ ] Start from the real **0.7.2**: download `gwatch-setup-0.7.2.exe` and
      `gwatch-agent-setup-0.7.2.exe` from the
      [Releases page](https://github.com/jxburros/GWatch/releases) (or use a copy
      of your own real install). Set it up the way a user would: a dozen nodes,
      a few weeks of history if you can (or restore a backup of your real install
      onto it), a dashboard, a rule, a maintenance window, an alert e-mail, an API
      key, a paired agent.
- [ ] **Make a backup first** (Settings › Backups › Create backup, with history)
      and copy it off the PC. Note the password.
- [ ] Run **`gwatch-setup-0.8.0-test.exe` over it** (don't uninstall first). Then:
  - [ ] every node, check, group, tag, dashboard, wallboard, rule, trigger,
        endpoint and maintenance window is still there;
  - [ ] history charts still show the old data at 24h, 7d, 30d and 1y;
  - [ ] the incident timeline is intact;
  - [ ] you can still sign in with your existing account;
  - [ ] your API key still works:
        `curl.exe -H "Authorization: Bearer <key>" http://127.0.0.1:7230/api/nodes`;
  - [ ] Settings › Alerts › send a test e-mail still arrives;
  - [ ] `C:\ProgramData\GWatch\logs\` shows no errors from the first start.
- [ ] **The first start is not slow.** With months of history, results should
      keep arriving during the first minutes (the startup catch-up now runs in
      small pieces), and a second restart should not repeat it.
- [ ] **The old 0.7.2 agent keeps reporting** to the upgraded server. Then
      upgrade the agent by running `gwatch-agent-setup-0.8.0-test.exe` over it —
      it keeps its pairing and keeps reporting.
- [ ] **A 0.7.2 backup restores** on the new version (Settings › Backups).

### 2. 🔴 The server as a Windows service (🧪 new service code)

The code that registers and runs GWatch as a Windows service was replaced in this
release and has **never run on a real Windows machine**. From an **Administrator**
PowerShell in `C:\Program Files\GWatch`:

- [ ] `sc.exe qc GWatch` shows `AUTO_START`, `WIN32_OWN_PROCESS`, the display
      name and `"C:\Program Files\GWatch\gwatch.exe" run …`.
- [ ] `sc.exe qfailure GWatch` shows `RESTART -- Delay = 5000 milliseconds`.
- [ ] `.\gwatch.exe status` says `running` — and also from a **non-Administrator**
      prompt.
- [ ] `.\gwatch.exe stop` → `status` says `stopped`; `.\gwatch.exe start` brings it
      back; `.\gwatch.exe restart` gives it a new process ID (Task Manager ›
      Details).
- [ ] Stopping an already stopped service, or starting a running one, succeeds
      quietly.
- [ ] **Crash recovery**: `taskkill /F /IM gwatch.exe` — it is running again
      within about 10 seconds.
- [ ] **Reboot**: after restarting the PC, GWatch is running before anyone signs in
      (open it from another device, or check right after signing in).
- [ ] **Stopping is quick**: start a long script action (a trigger running
      `powershell -Command Start-Sleep 300`), then `.\gwatch.exe stop` — it stops
      within about 20 seconds.
- [ ] **Install twice is refused**: `.\gwatch.exe install` while installed says it
      already exists and leaves the service alone.
- [ ] **Uninstall** from Windows Settings › Apps › GWatch: the service is gone
      (`sc.exe query GWatch` says it doesn't exist), the firewall rule is gone, and
      `C:\ProgramData\GWatch` (your data) is kept.
- [ ] **Reinstall** over the kept data: everything is back.
- [ ] **Foreground run**: stop the service, run `.\gwatch.exe run` in a terminal,
      open the UI, then Ctrl+C — it stops cleanly. Start the service again.

### 3. 🔴 The agent on Windows

#### The setup program (🧪 new)
- [ ] **Test connection** on the Connect page: try a correct address, a wrong one,
      `https://` against a server that only does `http://`, and a wrong port. Each
      gives a clear one-line answer before anything is installed.
- [ ] Click **Next** without testing — it tests for you; on a failure it asks
      "Install anyway?".
- [ ] The **last page** says "Paired as *name*, service running" and the "Open this
      machine in GWatch" box opens the right node. With a wrong or used pairing
      code it shows the real error instead.
- [ ] **Look at the pages at 100%, 125% and 150% display scaling** (Settings ›
      Display) — the new button and labels have never been seen on a real screen.
- [ ] **Silent install** on a third run:
      `gwatch-agent-setup-0.8.0-test.exe /VERYSILENT /SERVER=http://<server>:7230 /CODE=<code> /NAME=Test-PC`
- [ ] `"C:\Program Files\GWatch Agent\gwatch-agent.exe" probe --server http://<server>:7230`
      prints one line and exits 0; with a wrong address it exits non-zero.

#### The agent service (🧪 new service code)
From an Administrator prompt in `C:\Program Files\GWatch Agent`, repeat the
service checks from section 2 for `GWatchAgent`: `sc.exe qc GWatchAgent`,
`sc.exe qfailure GWatchAgent`, `.\gwatch-agent.exe status|stop|start|restart`,
`taskkill /F /IM gwatch-agent.exe` (back within ~10 s), and a reboot.
- [ ] All of the above pass for the agent.

#### Power cuts (🔴 the bug a user reported)
Do this in a VM (Hyper-V "Turn off", VirtualBox "Power off") or on a spare PC by
holding the power button.
- [ ] Cut the power on the agent's PC **five times**, at random moments. After
      each boot, the machine is reporting again in GWatch without you touching it.
- [ ] **Damaged settings file**: from an Administrator PowerShell
      ```powershell
      & "C:\Program Files\GWatch Agent\gwatch-agent.exe" stop
      Clear-Content C:\ProgramData\GWatch\agent.json
      & "C:\Program Files\GWatch Agent\gwatch-agent.exe" start
      ```
      It keeps reporting (it falls back to `agent.json.bak`) and `agent.log` has a
      warning saying so.
- [ ] Cut the power on the **server's** PC too: it comes back by itself and its
      database is fine (open a few charts).

#### When the server is down
- [ ] Stop the server for 15 minutes while the agent runs, then start it again.
      The agent machine's charts fill in the gap at the right times.
- [ ] Same again, but **restart the agent's PC** during the outage — the held
      readings still arrive (they are kept in
      `C:\ProgramData\GWatch\agent-queue.jsonl`, which disappears once delivered).

#### Token rotation
- [ ] On the node's agent card: **Rotate token…**. Within 15 minutes the agent picks
      up the new token and keeps reporting; the old token stops working. Try
      **Replace now** as well.

#### The agent control panel (🧪 new)
Open **Start ▸ GWatch Agent control panel** as a normal (non-admin) user.
- [ ] It opens in the browser.
- [ ] **Recent log** shows lines, and **no token** appears anywhere in it.
- [ ] **Copy diagnostics** — paste into Notepad and search for the token: not there.
- [ ] **Start / Stop / Restart** pops up a **UAC prompt**; after you approve, it
      works. Cancelling UAC gives a clear message.
- [ ] **Re-pair** with a fresh code from GWatch — it works and links to the node.
- [ ] **Update now** says it is up to date (expected for a ZIP build; re-test with
      the release candidate in Part 3).
- [ ] Change a setting on the **Settings** card and press **Save** — it actually
      saves now (this button was broken).

#### Tray icon (🧪 new, optional feature)
- [ ] Reinstall the agent with **"Show the agent in the notification area"** ticked
      on the last page. The icon appears; sign out and in — it's still there.
- [ ] The tooltip shows the status; stop the agent service → the icon changes to a
      warning or error.
- [ ] Right-click: **Open control panel**, **Agent status**, **Quit** all work.
- [ ] Restart Windows Explorer (Task Manager › Windows Explorer › Restart) — the
      icon comes back.
- [ ] Watch for a black console window flashing at sign-in (a known possibility).
- [ ] Uninstall the agent — the icon disappears and doesn't come back at the next
      sign-in.

#### Readings from a Windows machine
- [ ] **Network rate on gigabit**: copy a large file over the network and compare the
      agent's network reading with Task Manager › Performance › Ethernet. They
      should roughly agree even above 600 Mbit/s (this used to read wrong).
- [ ] **Disk activity and busy %** move while you copy a large file.
- [ ] **Services to watch** (🧪 new): on the node's agent card › What it collects,
      start typing `Spooler` — Windows' own services are suggested. Watch it, then
      `Stop-Service Spooler` (Administrator) → the check goes critical;
      `Start-Service Spooler` → back up.
- [ ] **Programs to watch**: watch `notepad`, open and close Notepad — the count
      goes 1 → 0 and the check goes critical at 0.
- [ ] The same two lists work for the **server's own PC** (its hardware panel).

### 4. 🔴 Backups and restore

- [ ] **Preview**: Settings › Backups › restore an archive. Before anything happens
      you see what it will do (accounts to add, commands, endpoints without tokens,
      access changes). **Cancel** — nothing changed.
- [ ] **Safety backup**: restore an older backup for real. A new archive appears in
      the list and the timeline says "Pre-restore backup created". Restore *that*
      one — you're back exactly where you started.
- [ ] **Deleted accounts stay deleted**: delete a user, restore a backup from before
      — the user is not back unless you tick "Re-create accounts…".
- [ ] **New PC**: on a clean VM, install the ZIP build and restore your backup —
      everything comes back, accounts included.
- [ ] **Close the browser** in the middle of restoring a large backup — it finishes
      anyway (check the timeline afterwards).
- [ ] **Monitoring resumes** after a restore: checks run, a broken check still
      alerts.
- [ ] **PBKDF2** (🧪): Settings › Users & access › Password hashing → PBKDF2. Sign
      out and in again (still works). Make a backup and restore it — fine.
      ⚠️ A PBKDF2 backup **cannot be restored by 0.7.2** (release-notes item). Switch
      back to Argon2id and sign in again.

### 5. 🟠 Alerts

Use a node you can break on purpose (a TCP check on a port you can open and
close, or the second PC).
- [ ] E-mail alerts arrive for down, recovery and warning.
- [ ] **Grouping**: Settings › Alerts › group alerts within 30 seconds. Break
      something with several checks → **one** e-mail listing them; recovery → one
      e-mail. Break it and fix it within 30 s → **no** e-mail.
- [ ] **Warning digest** hourly: warnings come as one summary on the hour.
- [ ] **Each alert channel you'll advertise** (🧪 new ones): Settings › Alerts ›
      Also notify › add one › **Test** — Telegram, Discord, Gotify, Matrix, Twilio
      SMS, PagerDuty, Opsgenie, Teams (Workflows), plus the existing Slack, ntfy,
      Pushover. ⚠️ Testing PagerDuty/Opsgenie opens a real `gwatch-test` incident
      you must resolve by hand.
- [ ] **PagerDuty/Opsgenie close the incident** when the check recovers.
- [ ] **Maintenance silences channels too**: put the node in maintenance, break it,
      nothing is sent anywhere.
- [ ] **Accents and dashes**: name a node `Café — rack`; e-mail and ntfy show it
      exactly.

### 6. 🧪 Remote collectors

Brand new and the biggest change. Use the second PC as the collector.
- [ ] On the second PC, edit `C:\ProgramData\GWatch\agent.json` (Notepad as
      Administrator), add `"collector": true`, and restart the agent
      (`gwatch-agent.exe restart`). In GWatch, open that machine's agent card and
      turn on **Use this machine as a remote collector**. Check that with only one of the two switches
      on, nothing happens.
- [ ] Create a node for a device only the second PC needs to reach (or any device —
      it still proves the path), set **Check from** to that agent, and add ping,
      TCP and HTTP checks. They go up, with results coming from the agent.
- [ ] An SNMP check (your router) and an HTTP check with a custom header work from
      the collector.
- [ ] Break the device: the alert fires as usual.
- [ ] **Turn the collector PC off**: after a few minutes its checks go "unknown" and
      you get **one** "collector unreachable" alert, not one per check. Turn it
      on: a "collector back" event, and the gap fills in.
- [ ] A custom (script) check on that node still runs on the server, not the
      collector.

### 7. 🧪 Matter

Matter needs the python-matter-server sidecar, which needs proper LAN multicast
and IPv6 — that generally **does not work under Docker Desktop on Windows**. Run
the sidecar on a Linux box, a Raspberry Pi or alongside Home Assistant
(docs/MATTER.md), and point GWatch on Windows at it. If you have no such setup,
skip this section and label Matter "preview" in the release notes.
- [ ] Add a Matter check, enter the sidecar's address, **Load devices**, pick a
      reading. The value matches the device's own app.
- [ ] Unplug the device → the check goes down. Stop the sidecar → down with a clear
      message.

### 8. 🟠 Discovery

- [ ] Nodes › Discover on your home network. It finishes noticeably faster than in
      0.7.2.
- [ ] Phones and smart-home devices that ignore ping now appear, with a **MAC
      address and maker**; a phone's randomised address says "private".
- [ ] Add a device from the results; the suggested template makes sense.
- [ ] Settings › Monitor health › **Ping** says which ping method Windows is using.

### 9. 🟠 The web interface

- [ ] **Every Settings tab opens** (Settings was split into separate files): click
      through all 16, change and save something on each, reload the page.
- [ ] **Network map › Layout: Clusters** with your real network: readable, nodes
      branch off their parents, nothing overlaps, no line runs through another box.
      Try each node size, and the dashboard's network-map widget.
- [ ] **Charts**: the picker lists checks under their node; **Charts: One chart per
      node** puts a node's checks together.
- [ ] **Check groups** (prototype): give two checks on different nodes the same
      check group; filter the Nodes list and bulk edit by it.
- [ ] **Filters stay put**: filter Nodes and Incidents › Events, reload, press Back
      — the filter is still there; **Export CSV** matches what's on screen.
- [ ] **CSV opens correctly in Excel**, with accented names intact (double-click the
      file).
- [ ] **Leave a dashboard and a wallboard open for an hour**: no flicker, focus
      stays where you clicked, and Edge/Chrome's Task Manager doesn't show the tab
      growing without limit.
- [ ] A **read-only (viewer) account** sees no secrets anywhere (alert-channel
      tokens, check passwords) and can't open admin pages.
- [ ] **Edge and Chrome** both work; also try your phone if you enabled LAN access.

### 10. 🟠 Engine behaviour

- [ ] **Laptop sleep**: on a laptop running GWatch, sleep it for an hour; on waking,
      a "Monitoring paused" event appears.
- [ ] **Deleting a node a rule uses**: the rule is updated (or switched off if it
      had nothing left) and the change shows in the event timeline.
- [ ] **Soak test**: leave it running for **several days** with real nodes, the
      agent and a collector. Check Settings › Monitor health for internal errors,
      and that `C:\ProgramData\GWatch` isn't growing unexpectedly.

### 11. 🟠 Security spot-checks

- [ ] A **read-only API key** is refused (403) by the new admin endpoints, e.g.
      `curl.exe -X POST -H "Authorization: Bearer <key>" http://127.0.0.1:7230/api/settings/test-channel`.
- [ ] The agent control panel's address doesn't work in another browser profile or
      an InPrivate window (it needs its cookie).
- [ ] **Audit › Configuration › Download JSON** contains no passwords or tokens —
      search the file.
- [ ] The firewall rule added by the server's setup allows only your local subnet
      (`wf.msc` › Inbound Rules › GWatch › Scope).

---

## Part 3 — The release itself

Once Parts 1–2 pass:

- [ ] **Set the versions**: `VERSION` and `cmd/gwatch-agent/VERSION` to `0.8.0`
      (both programs changed); move the CHANGELOG's **Unreleased** section under
      `0.8.0` with the date.
- [ ] **Merge to `main` through a pull request** and let CI go green. Several CI
      jobs are new and have never run on GitHub — expect to fix one or two.
- [ ] **Publish a release candidate first**: tag `v0.8.0-rc.1` and
      `agent-v0.8.0-rc.1`. That publishes a GitHub *prerelease*; installs on the
      stable channel don't pick it up.
- [ ] **Test self-update against the RC** (this can't be tested with a ZIP build):
  - [ ] on a 0.7.2 server, switch on Settings › Updates › **Show pre-releases**,
        apply the update — the service comes back on the new version by itself;
  - [ ] a 0.7.2 agent updates with `gwatch-agent.exe update` (or the control
        panel's **Update now**) and keeps reporting;
  - [ ] `gwatch.exe update --rollback` (Administrator) brings the previous version
        back.
- [ ] **Download the RC's setup programs from GitHub** on a clean VM and install
      them: this is the first time you'll see what users see, including the
      **SmartScreen "unknown publisher" warning** (expected until code signing is
      set up — docs/RELEASING.md › Authenticode signing).
- [ ] All files are on the RC's release page: both setup programs, the `.exe`
      programs, `.sha256` and `.sig` files.
- [ ] Then tag `v0.8.0` and `agent-v0.8.0` for the real release.

### Things to put in the release notes

- [ ] **Windows installers are unsigned** — SmartScreen will warn until code
      signing is set up.
- [ ] **PBKDF2 backups** can't be restored by 0.7.2 or older.
- [ ] **Experimental**: remote collectors, Matter, the tray icon, check groups.
- [ ] Testing a **PagerDuty/Opsgenie** channel opens a real incident.

### If something goes wrong after release

- A user's install broke on upgrade: they install the previous version's setup
  program from the Releases page and restore the backup made before upgrading
  (or the automatic "Pre-restore" / scheduled one in `C:\ProgramData\GWatch\backups`).
- Server binary only: `gwatch.exe update --rollback` from an Administrator prompt.
- Publish a fixed patch release rather than deleting a bad one — installs that
  already updated need something newer to move to.

---

## What CI already covers on Windows

Every push builds and tests on a Windows runner: all Go tests (including the
Windows service registration settings and the tray's Win32 structures), both
Inno Setup installers compiled, the PowerShell scripts parsed and linted, an
Authenticode signing dry run with a throwaway certificate, and the 351 web unit
tests and 183 browser tests (on Linux, against the same web interface). Green CI
means the code is consistent — it does **not** replace the hands-on tests above.

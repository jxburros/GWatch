# Installing GWatch on Windows

> **This is a beta.** GWatch works and keeps your data safely, but features and the
> layout can still change between releases. See
> [`CHANGELOG.md`](../CHANGELOG.md) for what this version includes and what it does
> not.

This is a plain-language walkthrough for installing GWatch with the setup program — no
PowerShell or command line needed. If you're comfortable with PowerShell, or need to
automate the install, see [Advanced: the PowerShell scripts](#advanced-the-powershell-scripts)
below instead. Not on Windows? On a Linux server, a NAS or a Raspberry Pi the
container image is the easy path — see [`DOCKER.md`](DOCKER.md).

## 1. Download the setup program

Get `gwatch-setup-<version>.exe` from the
[latest GWatch release](https://github.com/jxburros/GWatch/releases/latest) on GitHub
and save it anywhere (your Downloads folder is fine).

Windows may show a "Windows protected your PC" SmartScreen warning because the
installer isn't Authenticode-signed yet (see [Known limitations](#known-limitations)
below). Click **More info**, then **Run anyway**, if you trust the download.

## 2. Run the installer

Double-click `gwatch-setup-<version>.exe`. Windows will ask for administrator
permission — GWatch installs itself as a background Windows service, which needs it.
Click **Yes**.

Then it's an ordinary "Next, Next, Finish" wizard:

1. **Welcome page** — click **Next**.
2. **Licence and terms** — the GWatch Community License, followed by a short plain-text
   digest of the [terms of use](TERMS.md), the [privacy policy](PRIVACY.md) and the
   [security disclaimer](DISCLAIMER.md). Choose **I accept the agreement** to continue. A
   copy is installed as `LICENSE.txt` next to the program, and there is a **Licence and
   terms** entry in the Start Menu folder, so you can read it again later.
3. **Destination folder** — where the `gwatch.exe` program file goes (default:
   `C:\Program Files\GWatch`). Leave the default unless you have a reason to change it.
4. **Network access page** — this is the one page worth reading:
   - **Port** — which network port GWatch listens on. `7230` (the default) is fine
     unless something else on your computer already uses it.
   - **"Allow other devices on my network to open GWatch"** — leave this unchecked if
     you only want to use GWatch from this computer. Check it if you want to check on
     your network from a phone, tablet or another computer on the same Wi-Fi/LAN. When
     checked, the installer also opens the chosen port in Windows Firewall so other
     devices can reach it.
   - A note reminds you that GWatch has **no password by default**. If you check the
     "allow other devices" box, plan to set one in step 3 of *Using GWatch* below before
     you consider it safe to leave switched on.
5. **Additional icons** — optionally add a desktop shortcut, in addition to the Start
   Menu one that's always created.
6. **Ready to Install** — click **Install**. This copies the program, registers GWatch
   as a Windows service (so it starts automatically with your computer), and starts it.
7. **Finish** — leave "Open GWatch in your browser" checked and click **Finish** to see
   it running right away.

## 3. Using GWatch

The first time the interface opens it offers a short guided setup — six screens covering
what GWatch is, setting a password, adding your first node, hardware agents and alerts.
It takes under a minute and you can skip it; **Help** in the sidebar restarts it whenever
you like, and the same page has an opt-in setting for small contextual tips that point
things out as you go. Tips are off until you turn them on.

- The web interface opens at an address like `http://127.0.0.1:7230` (or
  `http://<your-computer-name>:7230` if you allowed LAN access).
- A **GWatch Monitor** shortcut in your Start Menu (and on the desktop, if you chose
  that option) reopens the interface any time.
- **If you allowed other devices on your network to connect**: go to
  **Settings › Users & access** in the web interface and set an access password (or
  create individual user accounts) right away. Without one, anyone who can reach that
  port on your network can see and change everything.

## Installing the agent on other machines

`gwatch-setup-<version>.exe` installs the monitor. The reporting agent is a separate
download, from its own release: look for the newest release tagged `agent-v…` on the
[releases page](https://github.com/jxburros/GWatch/releases) and take
`gwatch-agent-setup-<version>.exe` from it. It installs the small reporting agent on a
machine you want GWatch to watch — a file server, a spare laptop, the desktop in the
other room. Run it **on that machine**, not on the one running GWatch.

The agent has its own version and its own releases because it keeps itself up to date:
you install it once on a machine and it takes later agent releases by itself, so its
version does not follow GWatch's. See [`HARDWARE.md`](HARDWARE.md#keeping-agents-up-to-date).

Before you start, get a pairing code:

1. In GWatch, open **Hardware** and choose **Pair a machine**.
2. Give the machine a name. GWatch shows an eight-character code like `ABCD-2345`.
3. The code is good for that one machine and expires after about fifteen minutes. If it
   lapses, generate another — they are free.

Then, on the other machine, run `gwatch-agent-setup-<version>.exe` and fill in the
**Connect to GWatch** page: the server's address (for example
`http://gwatch.lan:7230`), the pairing code, and optionally a name. The installer
exchanges the code for a credential that can do exactly one thing — submit that
machine's readings — and registers the `GWatchAgent` service. The machine should appear
on its own node within a minute.

Nothing is opened up on the machine running the agent: it dials out to GWatch and hangs
up, and GWatch is given no way back in. To install across several machines at once:

```powershell
gwatch-agent-setup-0.1.0.exe /VERYSILENT /SERVER=http://gwatch.lan:7230 /CODE=ABCD-2345
```

Each machine needs its own code. See [`HARDWARE.md`](HARDWARE.md) for what the agent
reports and how to read it.

## Where your data lives

GWatch keeps its database, logs and backups in `C:\ProgramData\GWatch` — a separate
folder from the program files, so upgrading or reinstalling never touches your
monitoring history, nodes, checks or settings. `ProgramData` is a hidden folder by
default; type the path directly into File Explorer's address bar to open it.

That data directory is always a normal, permanent folder on this computer's disk —
never a temp directory that the OS can clear on reboot or under disk pressure. The
Windows installer and `scripts/install.ps1` both point the service at
`C:\ProgramData\GWatch`; running GWatch directly (any OS, see the
[README](../README.md#run-without-installing-any-os)) picks a per-platform default
unless you override it:

| Platform | Default data directory |
| --- | --- |
| Windows | `%ProgramData%\GWatch` (normally `C:\ProgramData\GWatch`) |
| Linux | `$XDG_DATA_HOME/gwatch`, otherwise `~/.local/share/gwatch` |
| macOS | `~/.local/share/gwatch` |

Override it with `--data-dir DIR` on the command line or the `GWATCH_DATA_DIR`
environment variable, on any platform. Inside that directory:

- `gwatch.db` — the SQLite database;
- `gwatch.key` — the key that encrypts secrets stored in the database;
- `database.json` — only present when GWatch has been pointed at a PostgreSQL or
  MySQL server instead of `gwatch.db`; see [`DATABASE.md`](DATABASE.md);
- `logs/` — the service log;
- `backups/` — encrypted backup archives, if you make any.

See [`PRIVACY.md`](PRIVACY.md#what-gwatch-stores-and-where) for what's stored inside
`gwatch.db` and why.

## Using your own database server

`gwatch.db` is the default and needs nothing from you. If you would rather GWatch kept
its data on a PostgreSQL or MySQL/MariaDB server you already run, **Settings › Database**
points it there (the change takes effect after a restart of the service), and
`gwatch migrate-db` copies what is in `gwatch.db` across. [`DATABASE.md`](DATABASE.md)
walks through it, including creating the database and user on the server.

## Choosing the SQLite driver

`gwatch.db` is an ordinary SQLite database whichever way GWatch was built, and the
release downloads need nothing from you here — skip this section unless you build
from source and have a reason to swap the library that reads and writes that file.

The SQLite driver is chosen when the program is compiled, with a Go build tag; there
is no setting, flag or environment variable for it at run time. The choices are:

| Build | Driver | Notes |
| --- | --- | --- |
| `go build .` (no tag) | [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) | The default. Pure Go, no C toolchain. What every release is built with. |
| `go build -tags sqlite_ncruces .` | [ncruces/go-sqlite3](https://pkg.go.dev/github.com/ncruces/go-sqlite3) | Pure Go: SQLite compiled to WebAssembly, run by an embedded Wasm runtime. That runtime only compiles to native code on amd64 and arm64; on other CPUs (32-bit ARM, 386, RISC-V …) it falls back to a much slower interpreter. See the project's [support matrix](https://github.com/ncruces/go-sqlite3/wiki/Support-matrix) for the platform-by-platform limits before choosing it. |
| `CGO_ENABLED=1 go build -tags sqlite_cgo .` | [mattn/go-sqlite3](https://pkg.go.dev/github.com/mattn/go-sqlite3) | The C SQLite library linked through cgo. Needs a C compiler (gcc/clang, or MinGW on Windows) and cannot be cross-compiled the way the releases are, so it is never used for a release build. |

`make build-ncruces` and `make build-cgo` run the two alternative builds; `make build`
is the default. The build tags are exclusive — pass at most one.

Switching drivers does not change the database file: all three write the same
format, so a `gwatch.db` made under one opens under another. To see which driver a
running copy was built with, open **Settings › Retention** or **Settings › Backups**
and look at the **Database driver** line beside the data directory paths, or read the
`databaseDriver` field of `GET /api/health`. (The PostgreSQL and MySQL drivers are
not a build choice: every binary has them, and `database.json` picks one at run time.)

## Upgrading

Download the newer `gwatch-setup-<version>.exe` and run it the same way. The installer
detects the existing GWatch service, stops it, replaces the program file, and starts it
again. Your data in `C:\ProgramData\GWatch` is untouched. (GWatch can also check for and
install updates itself from **Settings › Updates** — see the main
[README](../README.md#what-it-does).)

> If you want to change the port or the "allow other devices" setting on an upgrade,
> uninstall first (see below), then run the newer setup program as a fresh install so
> the new settings take effect — running the setup program straight over an existing
> install keeps the port/network settings from the first install.

## Uninstalling

Open **Settings › Apps** in Windows, find **GWatch**, and choose **Uninstall** (or use
"Add or Remove Programs"). This stops and removes the Windows service and the firewall
rule the installer added, then asks whether to also delete your data in
`C:\ProgramData\GWatch`. Choose **No** if you might reinstall later and want to keep
your history and settings; choose **Yes** to remove everything.

## Known limitations

- Neither installer is Authenticode-signed yet, hence the SmartScreen warning in step 1.
  Signing is tracked as a follow-up — see
  [`docs/RELEASING.md`](RELEASING.md#the-installer-artefact).
- The in-app updater (**Settings › Updates**) does not download or install the
  setup program itself; it manages the `gwatch.exe` program file directly, in place. Both
  work fine, and either one applies as an upgrade path.

## Advanced: the PowerShell scripts

The installer wraps the same steps the [`scripts/install.ps1`](../scripts/install.ps1)
and [`scripts/uninstall.ps1`](../scripts/uninstall.ps1) PowerShell scripts perform, and
either path is fully supported. Use the scripts directly if you want to script an
unattended install across several machines, prefer not to run a downloaded `.exe`, or
want to build `gwatch.exe` from source yourself.

1. Build (or download) `gwatch.exe`. With Go 1.26+ installed:
   ```powershell
   powershell -ExecutionPolicy Bypass -File scripts\build.ps1
   ```
2. Install the service from an **Administrator** PowerShell:
   ```powershell
   powershell -ExecutionPolicy Bypass -File scripts\install.ps1 -Exe .\dist\gwatch.exe
   ```
   This copies the program to `C:\Program Files\GWatch`, registers the `GWatch` service
   (automatic start, restarts on failure), starts it, and adds a Start-menu shortcut that
   opens the interface. Data lives in `C:\ProgramData\GWatch` (`gwatch.db`, `logs\`, `backups\`).
   Add `-Listen 0.0.0.0:7230` to serve the interface to the whole network from the start
   (it can also be switched on later in Settings › Network access).
3. Open <http://127.0.0.1:7230> (or run `gwatch open`).

**Updating**: build the new `gwatch.exe` and run `scripts\install.ps1` again. It stops the
service, replaces the executable and starts the service. Your data is untouched.

**Removing**: `scripts\uninstall.ps1` (add `-RemoveData` to also delete the database).

You can also manage the service by hand: `gwatch install`, `gwatch uninstall`,
`gwatch start|stop|restart|status`.

# Building the Windows setup programs

Two Inno Setup 6 scripts live here, and they share their branding, their licence
page and their wizard artwork:

| Script | Produces | Installs |
| --- | --- | --- |
| `gwatch.iss` | `gwatch-setup-<version>.exe` | GWatch itself — the monitor, its database and its web interface, as the `GWatch` Windows service |
| `gwatch-agent.iss` | `gwatch-agent-setup-<version>.exe` | `gwatch-agent` on a machine you want GWatch to watch, as the `GWatchAgent` service |

Neither script compiles Go code. Each wraps an executable that already exists, so
build the executable first — or use the wrapper script below, which does both.

## Build it in one command

From a Windows machine with [Go 1.24+](https://go.dev/dl/) and
[Inno Setup 6](https://jrsoftware.org/isdl.php) installed:

```powershell
powershell -ExecutionPolicy Bypass -File scripts\build-installer.ps1 -Version 1.0.0
```

That compiles both executables, compiles both setup programs and copies the
results into `dist\`. Add `-Which Monitor` or `-Which Agent` to build only one,
and `-Iscc <path>` if Inno Setup is somewhere the script does not look.

Install Inno Setup with either of:

```powershell
winget install JRSoftware.InnoSetup
choco install innosetup --no-progress -y
```

## Build it by hand

```powershell
# 1. Build the executables
powershell -ExecutionPolicy Bypass -File scripts\build.ps1 -Version 1.0.0

# 2. Compile a setup program from one of them
iscc /DAppVersion=1.0.0 /DExePath=..\..\dist\gwatch.exe gwatch.iss
```

Run step 2 from PowerShell, not Git Bash: Git Bash rewrites `/DAppVersion=…` as a
Windows path (MSYS path conversion) and ISCC then reads it as a second script
name. `ISCC.exe` is not on `PATH` after a default install; it lands in
`C:\Program Files (x86)\Inno Setup 6`.

Output goes to `scripts\installer\Output\`. `AppVersion` and `ExePath` both have
fallback defaults (`0.0.0`, and the matching executable under `..\..\dist\`), so
plain `iscc gwatch.iss` works for a quick local test build.

## Not building it at all

CI compiles both setup programs on every push and pull request. Open the run on
the **Actions** tab and download the **`gwatch-windows-installer`** artefact.
Tagging `v1.2.3` attaches both to the GitHub release — see
[`docs/RELEASING.md`](../../docs/RELEASING.md).

## What the monitor's installer does

Installs to `{autopf}\GWatch` (`C:\Program Files\GWatch` on a 64-bit machine),
shows the licence and terms, asks for a port and whether to allow LAN access on a
custom wizard page, registers and starts the `GWatch` service with data in
`%ProgramData%\GWatch`, and adds Start Menu entries. A Windows Firewall rule is
added only when LAN access is asked for. The uninstaller stops and removes the
service, removes the firewall rule, and asks (default: no) whether to delete the
data directory.

```powershell
gwatch-setup-1.0.0.exe /VERYSILENT /PORT=8080 /LAN=1
```

`/PORT` defaults to `8080` and `/LAN` to `0`.

## What the agent's installer does

Installs to `{autopf}\GWatch Agent`, asks for the GWatch server's address and a
pairing code, and runs `gwatch-agent install --code` to exchange the code for
this machine's own submit-only token before registering the `GWatchAgent`
service. Get a pairing code from GWatch under **Hardware › Pair a machine**; one
is good for a single machine and expires after about fifteen minutes.

Pairing runs in `CurStepChanged` rather than `[Run]` so a mistyped, expired or
already-used code produces a real error message instead of a service that never
reports.

```powershell
gwatch-agent-setup-1.0.0.exe /VERYSILENT /SERVER=http://gwatch.lan:8080 /CODE=ABCD-2345 /NAME=nas
```

`/INSECURE=1` accepts a self-signed certificate on the server.

## Branding assets

`brand.iss` holds the publisher, developers and URLs that both scripts share.
`assets\` holds the artwork:

| File | Used as | Notes |
| --- | --- | --- |
| `logo-master.png` | source artwork | 1254×1254 RGBA, transparent background. Everything else here is derived from it. |
| `gwatch.ico` | `SetupIconFile`, shortcut icon | 16/24/32/48/64/128 as 32-bit BMP entries, 256 as PNG |
| `wizard-large.bmp`, `wizard-large-2x.bmp` | `WizardImageFile` | 164×314 and 328×628, the welcome and finish panels |
| `wizard-small.bmp`, `wizard-small-2x.bmp` | `WizardSmallImageFile` | 55×58 and 110×116, the inner-page header badge |

The large panel is deliberately the app's own skin — graphite `#0f1114`, the teal
accent, square corners, a hairline grid, and status dots in the up/warn/down
colours from `web/app.css`. The small badge is white instead, because Inno draws
it on the inner pages' white header strip and the mark's navy needs the contrast.

The vector form of the same mark is [`web/logo.svg`](../../web/logo.svg), which is
what the web interface and the favicon use. Change one and change the other.

### The executables' own icon

`gwatch.ico` is also compiled into `gwatch.exe` and `gwatch-agent.exe`, so they
carry their icon in Explorer, the task bar and Alt-Tab rather than falling back to
the generic Windows program icon. A Go binary gets one only if a COFF resource
object is linked in, which is what the committed `rsrc_windows_amd64.syso` /
`rsrc_windows_arm64.syso` files in the repository root and in `cmd/gwatch-agent/`
are. The `_windows_<arch>` suffixes are ordinary Go build constraints, so Linux and
macOS builds ignore them.

They are generated from `gwatch.ico` by [`cmd/gwatch-rsrc`](../../cmd/gwatch-rsrc),
and regenerated with:

```sh
make rsrc
```

That is deterministic — running it when the icon has not changed produces no diff —
so it only needs running after editing `gwatch.ico`. Version information is
deliberately *not* embedded: the version is a build-time `-ldflags` value, and a
committed object would pin it to whatever it was when the object was generated.

## Known limitations

- Neither setup program is Authenticode-signed, so Windows SmartScreen shows an
  "unknown publisher" warning. See
  [`docs/RELEASING.md`](../../docs/RELEASING.md#the-installer-artefact).
- On upgrade, if the port or LAN setting changes, the already-registered `GWatch`
  service keeps its original `--data-dir`/`--listen` arguments: the installer
  calls `gwatch.exe start`, not a fresh `gwatch.exe install`, when the service
  already exists. Uninstall first, or use `gwatch uninstall` followed by
  `gwatch install --listen <new-addr>`, to change those after the first install.

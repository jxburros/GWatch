# Building the Windows installer

`gwatch.iss` is an [Inno Setup 6](https://jrsoftware.org/isinfo.php) script that
packages an already-built `gwatch.exe` into `gwatch-setup-<version>.exe`. It does not
compile Go code — build the executable first.

## Prerequisites

- [Inno Setup 6](https://jrsoftware.org/isdl.php) installed (provides `iscc.exe` /
  `ISCC.exe`, normally under `C:\Program Files (x86)\Inno Setup 6`). On a CI runner or
  via Chocolatey: `choco install innosetup --no-progress -y`.
- A built `gwatch.exe` (see [`../build.ps1`](../build.ps1) or the cross-compile step in
  [`.github/workflows/ci.yml`](../../.github/workflows/ci.yml)).

## Build locally

From a Windows machine, in this directory:

```powershell
# 1. Build gwatch.exe (adjust the version to taste)
powershell -ExecutionPolicy Bypass -File ..\build.ps1 -Version 1.0.0

# 2. Compile the installer from it
iscc /DAppVersion=1.0.0 /DExePath=..\..\dist\gwatch.exe gwatch.iss
```

This writes `scripts\installer\Output\gwatch-setup-1.0.0.exe`.

`AppVersion` and `ExePath` both have fallback defaults in the script (`0.0.0` and
`..\..\dist\gwatch.exe`), so `iscc gwatch.iss` alone also works for a quick local test
build as long as `dist\gwatch.exe` exists.

## What it does

See the comments at the top of `gwatch.iss` for the full behaviour. In short: it
installs to `{autopf}\GWatch` (`C:\Program Files\GWatch` on a 64-bit machine), asks for
a port and whether to allow LAN access on a custom wizard page, registers/starts the
`GWatch` Windows service with data in `%ProgramData%\GWatch`, adds a Start Menu shortcut
that runs `gwatch open`, and optionally adds a Windows Firewall rule when LAN access is
requested. The uninstaller stops and removes the service, removes the firewall rule, and
asks (default: no) whether to also delete the data directory.

## Silent installs

```powershell
gwatch-setup-1.0.0.exe /VERYSILENT /PORT=8080 /LAN=1
```

`/PORT` defaults to `8080`, `/LAN` defaults to `0` (LAN access off, firewall rule not
added). Both are read by the `[Code]` section via `{param:PORT|8080}` /
`{param:LAN|0}`.

## Known limitations / follow-ups

- No custom installer icon is bundled. `web/logo.svg` is an SVG and Inno Setup needs a
  `.ico`; adding one means adding a binary asset to the repo, which is out of scope for
  this change. A future pass can generate `gwatch.ico` from the logo and set
  `SetupIconFile` / `UninstallDisplayIcon` accordingly.
- The installer exe itself is not Authenticode-signed (see
  [`docs/RELEASING.md`](../../docs/RELEASING.md)) — Windows SmartScreen will show an
  "unknown publisher" warning until that's added.
- On upgrade, if the port or LAN setting changes, the already-registered Windows service
  keeps its original `--data-dir`/`--listen` arguments (the installer calls `gwatch.exe
  start`, not a fresh `gwatch.exe install`, when the service already exists — see the
  comments in `gwatch.iss`). Re-run the installer's uninstaller first, or use
  `gwatch uninstall` followed by `gwatch install --listen <new-addr>`, to change those
  settings after the first install.

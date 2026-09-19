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
powershell -ExecutionPolicy Bypass -File scripts\build-installer.ps1
```

That compiles both executables, compiles both setup programs and copies the
results into `dist\`. The version comes from the repository's
[`VERSION`](../../VERSION) file unless `-Version` overrides it. Add
`-Which Monitor` or `-Which Agent` to build only one, and `-Iscc <path>` if Inno
Setup is somewhere the script does not look.

Install Inno Setup with either of:

```powershell
winget install JRSoftware.InnoSetup
choco install innosetup --no-progress -y
```

## Build it by hand

```powershell
# 1. Build the executables
powershell -ExecutionPolicy Bypass -File scripts\build.ps1

# 2. Compile a setup program from one of them
iscc /DAppVersion=0.1.0 /DExePath=..\..\dist\gwatch.exe gwatch.iss
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
Tagging `v0.1.0` attaches both to the GitHub release — see
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
gwatch-setup-0.1.0.exe /VERYSILENT /PORT=8080 /LAN=1
```

`/PORT` defaults to `8080` and `/LAN` to `0`.

## What the agent's installer does

Installs to `{autopf}\GWatch Agent`, asks for the GWatch server's address and a
pairing code, and runs `gwatch-agent install --code` to exchange the code for
this machine's own submit-only token before registering the `GWatchAgent`
service. Get a pairing code from GWatch under **Nodes › Pair a machine**; one
is good for a single machine and expires after about fifteen minutes.

Pairing runs in `CurStepChanged` rather than `[Run]` so a mistyped, expired or
already-used code produces a real error message instead of a service that never
reports.

```powershell
gwatch-agent-setup-0.1.0.exe /VERYSILENT /SERVER=http://gwatch.lan:8080 /CODE=ABCD-2345 /NAME=nas
```

`/INSECURE=1` accepts a self-signed certificate on the server.

## Branding assets

`brand.iss` holds the publisher, developers and URLs that both scripts share.
`style.iss` holds the wizard's skin: both scripts include it as the first line
of their `[Code]` section, and it repaints the wizard in the application's own
“Signal” palette — graphite field, teal accent rule under the header, the
monospaced face in the fields that hold machine text. It is written against
Inno's control classes rather than against named fields, and the whole of it
runs inside `try`, so a future Inno Setup that renames something leaves a plain
wizard rather than an error box.

`assets\` holds the artwork:

| File | Used as | Notes |
| --- | --- | --- |
| `logo-master.png` | source artwork | 1254×1254 RGBA, transparent background. Everything else here is derived from it. |
| `gwatch.ico` | `SetupIconFile`, shortcut icon | 16/24/32/48/64/128 as 32-bit BMP entries, 256 as PNG |
| `wizard-large.bmp`, `wizard-large-2x.bmp` | `WizardImageFile` | 164×314 and 328×628, the welcome and finish panels |
| `wizard-small.bmp`, `wizard-small-2x.bmp` | source for the dark badge | 55×58 and 110×116, the mark on white |
| `wizard-small-dark.bmp`, `wizard-small-dark-2x.bmp` | `WizardSmallImageFile` | the same badge inverted for the graphite header |

The dark badge is derived from the light one by exchanging its black and white,
exactly as [`web/logo-dark.svg`](../../web/logo-dark.svg) does for the vector
mark: a pixel whose chroma leans blue or is neutral — the navy ring, the white
sclera, and every antialiased blend between them — is remapped along the
navy-to-white axis onto `#f2f4f6`-to-`#16181d`, and a pixel whose chroma leans
red is left alone, because the gold iris is the mark's colour rather than its
contrast. Redo it that way if the light badge ever changes.

The large panel is deliberately the app's own skin — graphite `#0f1114`, the teal
accent, square corners, a hairline grid, and status dots in the up/warn/down
colours from `web/app.css`. The small badge is the inverted mark, because Inno draws
it on the inner pages' header strip, which `style.iss` paints graphite.

The vector form of the same mark is [`web/logo.svg`](../../web/logo.svg), with
[`web/logo-dark.svg`](../../web/logo-dark.svg) for a dark field; the web
interface and the favicon use whichever the theme calls for. Change one and
change the others.

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

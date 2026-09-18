# Installing GWatch on Windows

This is a plain-language walkthrough for installing GWatch with the setup program — no
PowerShell or command line needed. If you're comfortable with PowerShell, or need to
automate the install, see [Advanced: the PowerShell scripts](#advanced-the-powershell-scripts)
below instead.

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
2. **Destination folder** — where the `gwatch.exe` program file goes (default:
   `C:\Program Files\GWatch`). Leave the default unless you have a reason to change it.
3. **Network Access page** — this is the one page worth reading:
   - **Port** — which network port GWatch listens on. `8080` (the default) is fine
     unless something else on your computer already uses it.
   - **"Allow other devices on my network to open GWatch"** — leave this unchecked if
     you only want to use GWatch from this computer. Check it if you want to check on
     your network from a phone, tablet or another computer on the same Wi-Fi/LAN. When
     checked, the installer also opens the chosen port in Windows Firewall so other
     devices can reach it.
   - A note reminds you that GWatch has **no password by default**. If you check the
     "allow other devices" box, plan to set one in step 5 below before you consider it
     safe to leave switched on.
4. **Additional icons** — optionally add a desktop shortcut, in addition to the Start
   Menu one that's always created.
5. **Ready to Install** — click **Install**. This copies the program, registers GWatch
   as a Windows service (so it starts automatically with your computer), and starts it.
6. **Finish** — leave "Open GWatch in your browser" checked and click **Finish** to see
   it running right away.

## 3. Using GWatch

- The web interface opens at an address like `http://127.0.0.1:8080` (or
  `http://<your-computer-name>:8080` if you allowed LAN access).
- A **GWatch Monitor** shortcut in your Start Menu (and on the desktop, if you chose
  that option) reopens the interface any time.
- **If you allowed other devices on your network to connect**: go to
  **Settings › Users & access** in the web interface and set an access password (or
  create individual user accounts) right away. Without one, anyone who can reach that
  port on your network can see and change everything.

## Where your data lives

GWatch keeps its database, logs and backups in `C:\ProgramData\GWatch` — a separate
folder from the program files, so upgrading or reinstalling never touches your
monitoring history, nodes, checks or settings. `ProgramData` is a hidden folder by
default; type the path directly into File Explorer's address bar to open it.

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

- The installer isn't Authenticode-signed yet, hence the SmartScreen warning in step 1.
  Signing is tracked as a follow-up — see
  [`docs/RELEASING.md`](RELEASING.md#the-installer-artefact).
- The in-app updater (**Settings › Updates**) does not download or install the
  installer itself; it manages the `gwatch.exe` program file directly, in place. Both
  work fine, and either one applies as an upgrade path.

## Advanced: the PowerShell scripts

The installer wraps the same steps the [`scripts/install.ps1`](../scripts/install.ps1)
and [`scripts/uninstall.ps1`](../scripts/uninstall.ps1) PowerShell scripts perform, and
either path is fully supported. Use the scripts directly if you want to script an
unattended install across several machines, prefer not to run a downloaded `.exe`, or
want to build `gwatch.exe` from source yourself. See the
[README's "Install on Windows" section](../README.md#install-on-windows) for the exact
commands.

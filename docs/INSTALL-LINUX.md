# Installing GWatch on Linux

Install the `.deb` or `.rpm` package where you can: it runs GWatch as its own unprivileged user in a hardened systemd unit, and your package manager owns the updates. The signed standalone binary works on any systemd distribution. Use [Docker](DOCKER.md) when you already manage containers.

## From a .deb or .rpm package

Each GWatch release carries `gwatch_<version>-1_{amd64,arm64,armhf}.deb` and `gwatch-<version>-1.{x86_64,aarch64,armv7hl}.rpm`, with their SHA-256 sums in `gwatch-linux-packages-<version>.sha256`. Download the one for your machine (armhf/armv7hl is 32-bit Raspberry Pi OS), check it against the sums file, then:

```sh
sudo apt install ./gwatch_<version>-1_amd64.deb      # or: sudo dnf install ./gwatch-<version>-1.x86_64.rpm
```

The package installs `/usr/bin/gwatch`, the `gwatch` systemd unit and `/etc/gwatch/gwatch.env` (root-only, kept across upgrades), creates the system user `gwatch`, and enables and starts the service on `http://127.0.0.1:7230`. The installer prints the address and the command that creates the first administrator (see below).

The service runs as `gwatch`, not root, and can write only `/var/lib/gwatch` (database, key, logs, backups). Its single capability is `CAP_NET_RAW`, for the built-in ping. The unit also sets `NoNewPrivileges`, `ProtectSystem=strict`, a system-call filter and the rest of systemd's sandboxing; `systemd-analyze security gwatch` rates it about 1.7 ("OK"). Command actions and custom-script checks run inside the same sandbox. To let them write elsewhere, or to listen on a port below 1024, add a drop-in with `sudo systemctl edit gwatch` (examples are in the unit file's header) instead of editing the unit.

```sh
sudo systemctl status gwatch
sudo journalctl -u gwatch -f
```

Settings go in `/etc/gwatch/gwatch.env` (`GWATCH_LISTEN`, `GWATCH_DB_*`, `GWATCH_TRUSTED_PROXIES`); restart with `sudo systemctl restart gwatch`. `gwatch install` and `gwatch uninstall` refuse to run on a packaged copy, because the package manages the service.

**Updates.** A packaged GWatch reports new releases in Settings > Updates but never replaces itself: the page shows "Updated by apt" (or dnf) and the command to run. Because the packages are release files, not an apt or dnf repository, you update by installing the next release's package the same way; `apt upgrade` does not see it yet. Upgrading restarts the service on the new version and keeps your data and your edits to `gwatch.env`.

**Removal.** `sudo apt remove gwatch` (or `sudo dnf remove gwatch`) stops and removes the service and keeps `/var/lib/gwatch` and the `gwatch` user, so reinstalling picks up where you left off. `sudo apt purge gwatch` also deletes `/var/lib/gwatch`, `/etc/gwatch` and the `gwatch` user; export a backup first. rpm has no purge: remove those by hand.

Switching from `gwatch install` to the package: run `sudo /usr/local/libexec/gwatch/gwatch uninstall` first and keep `/var/lib/gwatch`. The package's service takes the directory over, and systemd hands its ownership to the `gwatch` user on first start. If dpkg asks about `/etc/gwatch/gwatch.env`, keep your version.

**Not yet: an apt/dnf repository.** A signed repository would let `apt upgrade` and `dnf upgrade` keep GWatch current unattended. It needs hosting and a repository signing key held outside this repository (for example a Cloudsmith or packagecloud account, or a GitHub Pages repository signed in a protected release job). That is a planned follow-up and is not part of the release workflow today.

## Verified installation

Install curl, jq and OpenSSL 3 from your distribution, then run:

```sh
curl -fsSL https://github.com/jxburros/GWatch/releases/latest/download/install.sh | sudo sh
```

The installer checks SHA-256 and an Ed25519 signature against its embedded release key before executing the download. An absent or invalid signature stops installation. Options include `--version`, `--listen 0.0.0.0:7230`, `--no-start` and `--uninstall`. Download the script first to review it.

For a manually verified binary:

```sh
chmod +x gwatch-linux-amd64
sudo ./gwatch-linux-amd64 install --data-dir /var/lib/gwatch --listen 127.0.0.1:7230
```

Installation copies the executable into `/usr/local/libexec/gwatch/gwatch`, a protected root-owned directory, and registers the `GWatch` service. A privileged service never points at your Downloads directory. The service runs as root; enable command actions only for trusted administrators. Private data lives in `/var/lib/gwatch`. The systemd unit reads `/etc/gwatch/gwatch.env` on restart; this file is created with mode 0600. File logs are also sent to the journal:

```sh
sudo systemctl status GWatch
sudo journalctl -u GWatch -f
sudo /usr/local/libexec/gwatch/gwatch restart
```

## First administrator on a headless server

Keep the initial listener on loopback. Open an SSH tunnel from your desktop:

```sh
ssh -L 7230:127.0.0.1:7230 user@server
```

Open `http://127.0.0.1:7230`, create an administrator in Settings > Accounts, and enable the local sign-in requirement. Alternatively, create the first administrator from a shell on the server:

```sh
# A private admin.json contains username, password, and role set to admin.
curl --fail -H 'Content-Type: application/json' --data-binary @admin.json http://127.0.0.1:7230/api/users
```

Delete the credential file afterward. Existing installations require an authenticated administrator to create accounts.

## Network and ping

Settings > Network changes the port live; zero retains the startup default. If binding fails, the old listener is restored and a restart action is offered. To change the startup address, edit `GWATCH_LISTEN` in `/etc/gwatch/gwatch.env`, then restart. On headless machines `gwatch open` prints the URL.

For LAN access, permit the selected port through your firewall:

```sh
sudo ufw allow 7230/tcp
# or
sudo firewall-cmd --add-port=7230/tcp --permanent
sudo firewall-cmd --reload
```

Use a source-subnet restriction when only your LAN should connect. Settings shows a command for detected ufw/firewalld installations. Use HTTPS through a reverse proxy for remote credentials.

Unprivileged deployments can permit ICMP through `net.ipv4.ping_group_range`, use the distribution's `ping` command, or grant `CAP_NET_RAW` to a dedicated service. Root installations already have permission. See [Docker networking](DOCKER.md) for container capabilities.

## Updates and removal

Use Settings > Updates for signed updates. Under systemd the service restarts after five seconds. After a healthy start, the old executable becomes `.rollback`; restore it with `gwatch update --rollback`, then restart. Package-managed builds refuse self-update; use the package manager instead.

```sh
sudo /usr/local/libexec/gwatch/gwatch uninstall
```

Uninstall keeps the executable, environment and data. To purge, export a backup first, then explicitly remove `/usr/local/libexec/gwatch`, `/etc/gwatch` and `/var/lib/gwatch` after checking their contents.

## Raspberry Pi

Use `linux-arm64` on 64-bit Raspberry Pi OS and `linux-arm` on ARMv7 systems. Original ARMv6 Pi/Zero devices are unsupported. Images include `linux/arm/v7`. Release binaries use modernc SQLite. Start with a small inventory and measure process memory before expanding: capacity depends on check concurrency and history, and no fixed memory benchmark is claimed. Prefer an SSD for sustained database writes, shorten retention on small machines and consider [PostgreSQL](DATABASE.md) for larger inventories.

## Agent on Linux or macOS

Download `install-agent.sh` from the desired `agent-v...` release, inspect it, then run:

```sh
sudo sh install-agent.sh --server https://gwatch.example --code ABCD-2345
```

The script detects Linux/macOS and amd64/arm64/ARMv7, verifies the release signature, and installs a systemd/launchd service. OpenSSL 3 must be on PATH, including on macOS. Prefer [Homebrew or Linux packages](HARDWARE.md) for package-managed updates. CLI pairing with explicit `--insecure` trusts the first TLS key once, saves its SHA-256 pin and rejects later key changes. Serve mode requires a separate token, defaults to loopback and limits failed authentication attempts.

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
sudo ./gwatch-linux-amd64 install --listen 127.0.0.1:7230
```

## What `gwatch install` sets up

On a machine that runs systemd, `sudo gwatch install`:

- copies the program to `/usr/local/bin/gwatch`, owned by root, so the service never runs a file in your Downloads directory or one it could replace itself. Running install from a temporary directory is fine: it is the copy that is used.
- creates a `gwatch` system user and group (`useradd --system`; BusyBox `adduser -S` on Alpine) with no login shell. The service runs as that user, not as root.
- keeps its data in `/var/lib/gwatch` (mode 0700, owned by `gwatch`), unless you pass `--data-dir`. A data directory must be outside `/home` and `/root`, which the service is not allowed to see.
- writes its settings to `/etc/gwatch/gwatch.env` (mode 0600, readable by root only, which is who reads it when the service starts): `GWATCH_DATA_DIR`, `GWATCH_LISTEN`, and `GWATCH_TRUSTED_PROXIES` when given. Lines you add yourself, such as `GWATCH_DB_PASSWORD`, are kept.
- writes `/etc/systemd/system/gwatch.service`, enables it and starts it.

Install is safe to run again. Re-running it updates the program and the unit and restarts the service; `--listen` or `--data-dir` change those settings, and leaving them out keeps what the last install chose. `sudo gwatch install --listen 0.0.0.0:7230` is how to open the interface to the LAN from the command line.

Machines that do not run systemd (OpenRC, a container with no init) get the generic service the earlier releases installed, run as root.

### Upgrading from an earlier release

Releases before this one installed a unit called `GWatch.service` that ran as root, kept the program in `/usr/local/libexec/gwatch` and, when install was run with `sudo` and no `--data-dir`, kept the data in `/root/.local/share/gwatch`. Running `sudo gwatch install` with the new release moves all of that over: it stops and removes `GWatch.service`, moves a data directory under `/root` to `/var/lib/gwatch` (it refuses, and says why, if `/var/lib/gwatch` already holds files or is on a different filesystem), hands the data directory to the `gwatch` user, and removes the old program from `/usr/local/libexec/gwatch`. Take a backup first (Settings > Backups).

### The unit

The unit is hardened along the lines of the [hardware agent's](../packaging/nfpm/gwatch-agent.service): `NoNewPrivileges=yes`, `ProtectSystem=strict` with `ReadWritePaths=` the data directory, `ProtectHome=yes`, `PrivateTmp=yes`, `PrivateDevices=yes`, `UMask=0077`, the kernel, clock, hostname and control-group protections, and `RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX AF_NETLINK` (IPv4 and IPv6 cover ICMP and UDP; netlink is how the interface list is read). It restarts five seconds after a failure (`Restart=on-failure`), but not after exit status 78, which GWatch uses for settings that are wrong — a malformed `GWATCH_LISTEN`, unreadable database settings — so a mistake shows once in the journal rather than every five seconds.

The service holds one capability, `CAP_NET_RAW`, as an ambient capability, so that ping works (see [Network and ping](#network-and-ping)). When the listen address is a port below 1024, install adds `CAP_NET_BIND_SERVICE` as well. Changing the port to one below 1024 later from Settings > Network does not; run `sudo gwatch install --listen 0.0.0.0:443` (or whichever) instead.

Logs go to the journal as well as to the log files in the data directory:

```sh
sudo systemctl status gwatch
sudo journalctl -u gwatch -f
sudo systemctl restart gwatch     # or: sudo gwatch restart
```

Do not edit `gwatch.service` itself: install rewrites it. Put changes in a drop-in, which install leaves alone:

```sh
sudo systemctl edit gwatch
```

### What custom checks and script actions can do

Custom checks and script actions run as the `gwatch` user, inside the same sandbox as the service. Under earlier releases they ran as root. Concretely, a command run by GWatch now:

- can write only to the data directory (and its own private `/tmp`); the rest of the filesystem is read-only to it, and `/home` and `/root` are invisible.
- cannot gain privileges: `sudo`, and set-user-ID programs generally, do not work under `NoNewPrivileges=yes`.
- cannot reach devices in `/dev` beyond the basic pseudo-devices, and cannot use a Docker socket it has no group for.

To give it more, add a drop-in with `sudo systemctl edit gwatch` and restart. Grant the narrowest thing that works. Some examples:

```ini
[Service]
# Let checks run `docker ps` and the like (membership of docker is root-equivalent).
SupplementaryGroups=docker

# Let a script action write to a directory outside the data directory.
ReadWritePaths=/srv/reports

# Let a check read something under a home directory.
ProtectHome=read-only
```

A command that genuinely needs root (restarting another service, say) needs both a sudoers rule and the sandbox loosened for it. Allow exactly the command, for the `gwatch` user, in `/etc/sudoers.d/gwatch`:

```text
gwatch ALL=(root) NOPASSWD: /usr/bin/systemctl restart nginx.service
```

and, because sudo is a set-user-ID program, a drop-in with `NoNewPrivileges=no`, `RestrictSUIDSGID=no`, `CapabilityBoundingSet=` emptied (an empty assignment resets it) and `PrivateDevices=no`. That gives up a good part of the sandbox for every command the service runs, which is why it is not the default.

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

Settings > Network changes the port live; zero retains the startup default. If binding fails, the old listener is restored and a restart action is offered. To change the startup address, run `sudo gwatch install --listen ADDRESS`, or edit `GWATCH_LISTEN` in `/etc/gwatch/gwatch.env` and restart. On headless machines `gwatch open` prints the URL.

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

The service cannot replace its own program: `/usr/local/bin` is root's, and `ProtectSystem=strict` makes it read-only to the service in any case. Settings > Updates therefore tells you when a release is out but says it cannot install it from there. To update, run the installer again, which verifies the release and keeps your settings:

```sh
curl -fsSL https://github.com/jxburros/GWatch/releases/latest/download/install.sh | sudo sh
```

or, with a binary you have verified yourself, `sudo ./gwatch-linux-amd64 install`. Package-managed builds refuse self-update; use the package manager instead.

To remove the service but keep the data, the settings and the program:

```sh
sudo gwatch uninstall
```

To remove everything install created — the data directory (database, encryption key, logs and backups), `/etc/gwatch`, the `gwatch` user and group, and `/usr/local/bin/gwatch` — export a backup first, then:

```sh
sudo gwatch uninstall --purge          # lists what it will delete and asks you to type yes
sudo gwatch uninstall --purge --yes    # for scripts: does not ask
```

`--purge` refuses to delete a data directory that is a system directory or that does not look like GWatch's (no `gwatch.db`, `gwatch.key` or `database.json` in it); remove such a directory by hand if you are sure.

## Raspberry Pi

Use `linux-arm64` on 64-bit Raspberry Pi OS and `linux-arm` on ARMv7 systems. Original ARMv6 Pi/Zero devices are unsupported. Images include `linux/arm/v7`. Release binaries use modernc SQLite. Start with a small inventory and measure process memory before expanding: capacity depends on check concurrency and history, and no fixed memory benchmark is claimed. Prefer an SSD for sustained database writes, shorten retention on small machines and consider [PostgreSQL](DATABASE.md) for larger inventories.

## Agent on Linux or macOS

Download `install-agent.sh` from the desired `agent-v...` release, inspect it, then run:

```sh
sudo sh install-agent.sh --server https://gwatch.example --code ABCD-2345
```

The script detects Linux/macOS and amd64/arm64/ARMv7, verifies the release signature, and installs a systemd/launchd service. OpenSSL 3 must be on PATH, including on macOS. Prefer [Homebrew or Linux packages](HARDWARE.md) for package-managed updates. CLI pairing with explicit `--insecure` trusts the first TLS key once, saves its SHA-256 pin and rejects later key changes. Serve mode requires a separate token, defaults to loopback and limits failed authentication attempts.

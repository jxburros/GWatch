# Installing GWatch on Linux

Use the signed standalone binary for a systemd service. Use [Docker](DOCKER.md) when you already manage containers. The server does not yet publish deb/rpm packages; those packages are available for the [hardware agent](HARDWARE.md).

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

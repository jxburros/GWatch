# Running GWatch in Docker

GWatch is a single static binary, so the container image is small: the binary,
Alpine, CA certificates, the `ping` command and time-zone data. It runs as a
non-root user, keeps everything it knows in one directory, and reports its own
health to Docker. This page covers how to run it, the two things a container
changes about how GWatch behaves (who counts as an administrator, and what
"this computer" means), and how upgrades and backups work when the program
lives in an image.

If you are on Windows, the [setup program](INSTALL.md) is the simpler path.

## Quick start

```bash
docker run -d --name gwatch \
  -p 7230:7230 \
  -v gwatch-data:/data \
  --cap-add NET_RAW \
  --restart unless-stopped \
  ghcr.io/jxburros/gwatch:latest
```

Or with Compose, using the [`docker-compose.yml`](../docker-compose.yml) at the
repository root:

```bash
docker compose up -d
```

Then open `http://<the docker host>:7230` and create the administrator account
(next section). `docker logs gwatch` shows the same log the program writes to
`/data/logs/`.

## First run: create an administrator account

GWatch treats a request from the loopback address (127.0.0.1) as coming from
the person sitting at the computer it runs on, and gives it administrator access
with no password. That is how a fresh install on Windows works: you open the
page on the same machine and you are in.

In a container with the default (bridge) network, nothing you do from the host
arrives over loopback. Your browser's request is forwarded through Docker's
network and reaches GWatch from another address, so it lands on the sign-in
screen — and on a fresh install there is no account to sign in with yet. The
screen tells you to "open it on the computer it runs on", which in a container
means from inside the container. Create the first account with one command:

```bash
docker exec gwatch wget -q -O - \
  --header 'Content-Type: application/json' \
  --post-data '{"username":"admin","password":"choose-a-good-one","role":"admin"}' \
  http://127.0.0.1:7230/api/users
```

The first account is always an administrator, and the password has to be at
least 8 characters. Sign in with it in your browser, then go to
**Settings → Users & access** to add anyone else. The password went through
your shell as a command-line argument, so if that bothers you change it right
away from the same page (or put the JSON in a file and use `--post-file`
instead).

Once one account exists the loopback shortcut only matters for whatever else
runs inside the container, which is nothing — unless you use host networking,
covered below.

## The `/data` volume

`GWATCH_DATA_DIR` is `/data` in the image, and everything GWatch keeps lives
there:

| Path | What it is |
|---|---|
| `gwatch.db` (plus `-wal`, `-shm`) | The SQLite database: nodes, checks, history, users, settings |
| `gwatch.key` | The key that encrypts the secrets inside the database (SMTP passwords, SNMP communities and the like) |
| `logs/` | The program's own log, rotated by size |
| `backups/` | Backup archives made from Settings → Backups |

Keep `gwatch.db` and `gwatch.key` together. A database copied without its key
opens, but every stored secret in it is unreadable and has to be re-entered.
Back the volume up as a unit, or better, use GWatch's own encrypted backups
(below).

The examples use a **named volume** (`gwatch-data`), which Docker owns and sets
up with the right permissions. A **bind mount** (`-v /srv/gwatch:/data`) works
too, but the directory has to be writable by the user the container runs as:
uid and gid `7230`.

```bash
sudo mkdir -p /srv/gwatch && sudo chown 7230:7230 /srv/gwatch
```

## Listening address and remote access

The image sets `GWATCH_LISTEN=0.0.0.0:7230`, so GWatch listens on every
interface inside the container. It has to: the default of `127.0.0.1:7230`
would be reachable only from inside the container and Docker's `-p 7230:7230`
would have nothing to forward to. Who can actually reach the port is decided
by Docker (`-p 127.0.0.1:7230:7230` publishes it to the host alone,
`-p 7230:7230` to the whole network) and by your firewall, not by GWatch.

This means the **Settings → Network access → Allow access from other devices on
my network** switch does nothing in a container. It exists to move a
loopback-only listener onto the network; a listener already on `0.0.0.0` has
nowhere further to go. Leave it as it is.

Everything in [`REMOTE-ACCESS.md`](REMOTE-ACCESS.md) about reaching GWatch from
outside your network still applies unchanged — a reverse proxy with TLS in
front of the published port is the usual arrangement.

## Ping

GWatch's built-in ping tries an unprivileged ICMP socket first and a raw socket
second. Inside a container, as a non-root user, both need help. Pick one:

- **`--cap-add NET_RAW`** (what the quick start and the Compose file do). Lets
  the process open a raw ICMP socket. It is a small, well-understood
  capability and the least surprising option.
- **`--sysctl net.ipv4.ping_group_range="0 2147483647"`** instead of the
  capability. Lets any group use unprivileged ICMP sockets, so GWatch's first
  attempt succeeds and no raw socket is needed.
- **Neither.** Set **Settings → General → Ping method** to `system`. GWatch
  then runs the `ping` command, which the image ships (`iputils`) with the
  capability it needs. Individual nodes can override the method in their
  check settings too.

Without any of the three, ping checks fail with "no permission to open an ICMP
socket" and say so in the check's status.

## Discovery and the container network

Subnet discovery pings a range of addresses and looks at what answers. Under
Docker's default bridge network the container is on Docker's own private
subnet (`172.17.0.0/16` or similar), so a scan finds Docker's gateway and other
containers, not your router, printers and NAS. Ordinary checks are not affected:
a check against `192.168.1.1` reaches it fine through Docker's NAT, and that is
what most people want.

If you want discovery to see the real LAN there are two ways, both with a
cost:

- **`--network host`** puts the container straight on the host's network stack.
  Discovery works, `-p` is no longer needed (or honoured), and GWatch takes port
  7230 on the host directly. The catch is the loopback rule from the first-run
  section: anything on the host that connects to `127.0.0.1:7230` — any local
  process, any user with a shell on that machine — is now an administrator with
  no password. Turn on **Settings → Users & access → Require sign-in on this
  computer too** if you use host networking on a machine other people can log
  in to. The Compose file has a commented-out block for this.
- **A macvlan network** gives the container its own address on your LAN, so
  discovery sees the LAN and there is no loopback shortcut from the host. It
  takes more setting up (`docker network create -d macvlan ...` with your
  subnet and gateway, and a host cannot talk to its own macvlan containers
  without extra configuration). Docker's macvlan documentation covers it.

## Hardware readings are the container's

The **This computer** readings under Settings → Hardware, and any "this
computer" metrics you monitor, describe the container: its share of memory,
the disk the volume is on, the container's network interfaces. They are not
wrong, but they are not the host either.

To watch the host, run `gwatch-agent` on the host itself, as you would for any
other machine, and point it at the container's published port. The agent is a
separate download from the [releases page](https://github.com/jxburros/GWatch/releases/latest)
and [`HARDWARE.md`](HARDWARE.md) walks through registering it.

## Upgrading

The in-app updater (Settings → Updates) still checks for new releases and tells
you when there is one, but it cannot install it: the binary sits in a read-only
image directory and the updater declines to run when it cannot replace the
executable. Upgrade by pulling the new image instead:

```bash
docker compose pull && docker compose up -d
# or, without Compose:
docker pull ghcr.io/jxburros/gwatch:latest
docker stop gwatch && docker rm gwatch
docker run -d --name gwatch ... ghcr.io/jxburros/gwatch:latest   # same flags as before
```

Your data is in the volume, not the container, so removing the old container
loses nothing. Database schema changes are applied on start, the same as any
other upgrade. Going *back* to an older image after a newer one has run against
the database is not supported — take a backup first if you want that option.

## Backups and moving to another machine

GWatch's own backups (Settings → Backups) are password-encrypted archives that
include the secrets key, so one archive is a complete copy that restores on any
machine — container or not. Make one, download it from the same page, and keep
it somewhere other than the volume. [`RESTORE.md`](RESTORE.md) covers restoring
one onto a fresh install; a fresh container with a new empty volume counts.

If you would rather copy the volume itself, copy the whole `/data` directory
while the container is stopped, so the `-wal` file is folded into the database
and `gwatch.key` comes along.

## Image tags and platforms

Images are published to `ghcr.io/jxburros/gwatch` by the release workflow,
built for `linux/amd64` and `linux/arm64` (Raspberry Pi 4 and 5, Apple silicon
Docker Desktop, most ARM servers):

| Tag | Meaning |
|---|---|
| `0.2.2` | Exactly that release. Pin this if you want upgrades to be a decision |
| `0.2` | The newest patch release of that minor version |
| `latest` | The newest release |

`docker run --rm ghcr.io/jxburros/gwatch:latest version` prints the version the
image carries. To build your own from a checkout, `make docker` produces
`gwatch:<VERSION>` for your machine's architecture; the `Dockerfile` takes a
`VERSION` build argument and otherwise reads the `VERSION` file.

## Environment variables

| Variable | Default in the image | Purpose |
|---|---|---|
| `GWATCH_DATA_DIR` | `/data` | Where the database, key, logs and backups live. Change the volume, not this |
| `GWATCH_LISTEN` | `0.0.0.0:7230` | Address and port inside the container. Change the `-p` mapping rather than this |
| `TZ` | unset (UTC) | Time zone for schedules, maintenance windows and log timestamps, e.g. `Europe/London` |

<img src="web/logo.svg" alt="" width="84" align="right">

# GWatch

[![CI](https://github.com/jxburros/GWatch/actions/workflows/ci.yml/badge.svg)](https://github.com/jxburros/GWatch/actions/workflows/ci.yml)

GWatch is a calm monitor for a home network and its services. It runs as a Windows
background service (or a plain console program on Linux/macOS), checks your router,
servers, websites and APIs on a schedule, keeps long-term history in an embedded SQLite
database, sends email alerts that do not spam, can run webhooks / git commands / scripts
when something changes, and shows everything in a compact dark or light web interface
served on `http://127.0.0.1:7230` (optionally to the rest of your LAN).

No cloud account, no AI features, never exposed to the internet by itself. The product
brief that defines the scope lives in [`local-network-monitoring-product-brief.md`](local-network-monitoring-product-brief.md).

> **GWatch is in beta (0.2.2).** It works and it looks after your data, but the
> interface and the JSON API can still change between releases, and bugs are likelier
> now than they will be at 1.0 — which is reserved for the first public, stable
> release. [`CHANGELOG.md`](CHANGELOG.md) lists what is in this version and the gaps it
> ships with. Please [report anything that looks wrong](https://github.com/jxburros/GWatch/issues).

## What's new in 0.2.2

Since 0.2.1: SNMP checks for routers, switches and access points; subnet discovery;
bulk edit across nodes and checks; nodes can belong to more than one group; status
indicators in the header; compact lists by default with a "Breathing room" mode; a
"Ping only" template; ping's own ICMP sender with a method setting and standard
deviation reporting; the default port moved to 7230; hardware check thresholds shown
per metric; brighter dark-theme text with WCAG contrast everywhere; a shorter README
with the detail moved into `docs/`; several security fixes (rate-limited endpoint
tokens, enforced `Content-Type`, security headers on every response, enforced database
schema versioning); and CI now runs on Linux and macOS as well as Windows. Full detail
in [`CHANGELOG.md`](CHANGELOG.md).

## What it does

- **Check types**: Ping, HTTP/S, HTTPS certificate, TCP port, DNS, Keyword, JSON, Custom
  script (run your own command), and Hardware health (this computer, or any machine you
  install the small agent on — processor, memory, disk, network).
- **Nodes** group several checks, with templates, groups, tags, dependencies and importance.
- **Discovery**: ping a subnet, see what answers with its name and open ports, and add the
  devices you tick as nodes — with a template suggested for each. No nmap, nothing to install.
- **Bulk edit** changes one setting — interval, timeout, thresholds, groups, tags, importance,
  enabled, alert overrides — across as many nodes and checks as you tick, in one go.
- **Alerts by email** after N consecutive failures, on recovery, or for warnings, with
  cooldowns, silencing, maintenance windows and dependency-aware suppression.
- **Incident timeline**, **dashboards**, a **Charts** tab and an **Audit** tab with full
  history — retained and rolled up automatically so the database never grows without bound.
- **Automation**: triggers and custom inbound endpoints run webhooks, Slack/Teams/ntfy/
  Pushover notifications, git commands or scripts when something changes.
- **Wallboards** for a spare monitor or tablet, with an optional no-sign-in projected view.
  The interface is responsive, so a narrow window or a tablet can read it, but it is built
  for a desk: GWatch is not a phone app and is not designed to run on phone hardware.
- **Accounts and API keys**, scoped read-only or read-write, plus opt-in **remote access**.
- **Self-updating**, with every release cryptographically signed and verified before install.
- **Backups**: one-click, password-encrypted, restorable on a new machine in one step.

## Install

**Windows** — download `gwatch-setup-<version>.exe` from the
[latest release](https://github.com/jxburros/GWatch/releases/latest) and run it: it asks for
a port and whether to allow other devices on your network, then installs and starts the
`GWatch` service. Full walkthrough (including the PowerShell-only path, upgrading and
uninstalling): [`docs/INSTALL.md`](docs/INSTALL.md).

**Docker** — `docker run -d --name gwatch -p 7230:7230 -v gwatch-data:/data --cap-add NET_RAW ghcr.io/jxburros/gwatch:latest`,
then open `http://<the docker host>:7230` and create the first administrator account.
Multi-arch (`linux/amd64`, `linux/arm64`), a `docker-compose.yml` in the repository root,
and what changes inside a container (ping, discovery, hardware readings, upgrades):
[`docs/DOCKER.md`](docs/DOCKER.md).

**Linux/macOS** — run it straight from source (Go 1.26+):

```bash
go run . run --data-dir ./data
```

Then open <http://127.0.0.1:7230> — monitoring runs only while this process runs. See
`gwatch --help` (or `--listen`/`GWATCH_LISTEN` to change the address) for the rest of the flags.

## Read more

| Document | What it covers |
|---|---|
| [`docs/API.md`](docs/API.md) | The JSON API: authentication and roles, every endpoint, custom checks, platform notes |
| [`docs/INSTALL.md`](docs/INSTALL.md) | The Windows installer walkthrough, the PowerShell scripts, upgrading, uninstalling |
| [`docs/DOCKER.md`](docs/DOCKER.md) | Running GWatch in a container: the first account, the `/data` volume, ping and discovery inside Docker, upgrading by pulling |
| [`docs/HARDWARE.md`](docs/HARDWARE.md) | Hardware health, the agent, its trust model, and what each platform can measure |
| [`docs/SNMP.md`](docs/SNMP.md) | SNMP checks: enabling SNMP on a router or switch, choosing OIDs, interface traffic in bits per second |
| [`docs/RECIPES.md`](docs/RECIPES.md) | Ready-made trigger/endpoint recipes: Home Assistant, Slack/Discord, ntfy/Pushover, Docker, git |
| [`docs/REMOTE-ACCESS.md`](docs/REMOTE-ACCESS.md) | Reaching GWatch from outside your network safely — accounts, API keys, Tailscale, a TLS proxy |
| [`docs/RESTORE.md`](docs/RESTORE.md) | Moving GWatch to a new machine: backup, restore, and what does and doesn't come across |
| [`docs/RELEASING.md`](docs/RELEASING.md) | Building and testing locally, CI, repository layout, and cutting a signed release |
| [`docs/PRIVACY.md`](docs/PRIVACY.md) | What GWatch stores, where, and the fact that none of it goes anywhere |
| [`docs/TERMS.md`](docs/TERMS.md) | Terms of use — acceptable use, no warranty, limitation of liability |
| [`docs/DISCLAIMER.md`](docs/DISCLAIMER.md) | The security choices that are yours to make, and what happens if you make them badly |
| [`mcp/README.md`](mcp/README.md) | `gwatch-mcp` — an optional MCP server so an AI assistant can read (and, if you allow it, manage) your monitoring |
| [`CHANGELOG.md`](CHANGELOG.md) | What shipped in each release |
| [`ROADMAP.md`](ROADMAP.md) | What's planned and roughly when |

## License, trademarks and credits

GWatch is source-available under the [GWatch Community License](LICENSE): free to use,
modify, and distribute, including commercially for deployment, support, and
customization work — but the unmodified software itself may not be resold or offered
as a paid hosted service. See [`TRADEMARKS.md`](TRADEMARKS.md) for the separate policy
on using the GWatch name and logo; forks and derivatives must keep the GWatch name, logo,
and copyright line visible. The setup program shows the licence and a digest of the
terms, privacy and disclaimer documents above before it installs anything.

GWatch is developed by **Jeffrey Guntly** and **Garrett Guntly**, and published by
**JX Holdings, LLC**.

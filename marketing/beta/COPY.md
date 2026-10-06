# GWatch Beta: ready-to-use copy

Text to go with the images in this kit. Every claim here comes from GWatch 0.6.0's README,
CHANGELOG and `docs/`. If a fact changes, update it here and in `build.py` (see
[README.md](README.md#facts-in-the-copy)).

Links used throughout:

- Download: <https://github.com/jxburros/GWatch/releases/latest>
- Feedback (bugs, rough edges, feature ideas): <https://github.com/jxburros/GWatch/issues>
- Docs: <https://github.com/jxburros/GWatch/blob/main/docs/USER-GUIDE.md>

---

## Taglines

- **Your home network, watched calmly.** (main line)
- Know the moment something breaks. Not before.
- Calm is a feature.
- One outage. One alert.
- Robust enough to trust. Simple enough to enjoy.
- Run it early. Shape 1.0.

## One-liner (about 25 words)

GWatch is a calm, local monitor for your home network. It watches your router, servers, websites
and services, keeps the history, and alerts you only when something actually needs you.

## Short description (about 60 words)

GWatch watches everything on your home network: your router, NAS, Pi-hole, Home Assistant,
websites, APIs and certificates. It runs on your own Windows PC, Linux or macOS machine, Docker host
or Raspberry Pi, keeps years of history, draws a live map of what depends on what, and sends
alerts that don't nag. No cloud account, and nothing leaves your network. The beta is open now.

---

## Announcement post (blog, forum, Reddit, GitHub Discussions)

**Title:** The GWatch Beta is open: calm monitoring for your home network

Most of us find out the network is down when someone yells from the other room. GWatch is built
so you hear first, and so you only hear when it matters.

**What GWatch is**

GWatch is a monitor for a home network and its services. Install it on a PC, a server, a NAS or a
Raspberry Pi and it checks your devices and services on a schedule, keeps long-term history, and
shows everything in a quiet dark or light web interface.

- **It checks everything.** Ping, HTTP/S, HTTPS certificates, TCP ports, DNS, keywords, JSON
  values, SNMP readings from routers and switches, hardware health (CPU, memory, disks and
  network, from this machine or any machine running the small GWatch Agent) and your own scripts.
- **It doesn't nag.** Alerts fire after N failures, on recovery or for warnings, with cooldowns,
  silences, maintenance windows and dependency-aware suppression. If your gateway goes down you
  get one alert about the gateway, not eleven about everything behind it.
- **It shows the whole picture.** The network map draws every device under the one it depends on
  and traces an outage's reach in red. Dashboards, charts that lay today over last week, an
  incident timeline and weekly or monthly availability reports cover the rest.
- **It acts for you.** It can notify by email, Slack, Teams, ntfy or Pushover, or call webhooks,
  git commands and scripts when something changes.
- **It stays home.** No cloud account. Nothing is phoned home, and GWatch never exposes itself to
  the internet. Every release is cryptographically signed and verified before it installs, and
  backups are password-encrypted.
- **It runs where you do.** A Windows service with a point-and-click installer, a Linux or macOS
  program, a multi-arch Docker image, or a Raspberry Pi. SQLite is built in, or you can use your
  own PostgreSQL or MySQL/MariaDB server.

It's also keyboard- and screen-reader-accessible throughout, with a "View as table" alternative
for every chart.

**Why a beta?**

GWatch 0.6.0 works and looks after your data. Before 1.0, the first public and stable release, we
want it running on as many real networks as possible. The interface and the API can still change,
and we want your input while it can.

**How to join**

1. **Install 0.6.0** from <https://github.com/jxburros/GWatch/releases/latest>: the Windows
   installer, the Docker image (`ghcr.io/jxburros/gwatch`) or a Linux/macOS build.
2. **Point it at your real network**: your router, NAS, Pi-hole, websites, APIs, whatever you run.
3. **Tell us everything** at <https://github.com/jxburros/GWatch/issues>: bugs, the screen that
   confused you, the setting you couldn't find, and the feature that would make you recommend it.

GWatch updates itself from signed releases, so beta testers get each fix as it ships.

Thank you for helping make 1.0 great.

---

## Social posts

**X / Bluesky / Mastodon** (with `social/square-1080x1080.png` or `motion/teaser.mp4`)

> Your home network, watched calmly. 🟢🟡🔴
>
> GWatch checks your router, servers, sites and services, keeps the history, and only speaks up
> when it matters. Local, private, free to use.
>
> The beta is open. Run it early and help shape 1.0 → github.com/jxburros/GWatch

**Short version (under 200 characters)**

> GWatch Beta is open: calm, local monitoring for your whole home network. No cloud account, no
> alert storms. Try it and tell us what to build next → github.com/jxburros/GWatch

**LinkedIn** (with the carousel PDF as a document post)

> We're opening the GWatch beta.
>
> GWatch is a calm monitor for home and small networks. It runs on your own hardware (a Windows
> service, Linux, macOS, Docker or a Raspberry Pi) and watches routers, servers, websites, APIs,
> certificates and hardware health.
>
> What makes it different: alerts with manners (cooldowns, maintenance windows and
> dependency-aware suppression), a live network map that shows what an outage really takes down,
> and nothing that leaves your network.
>
> 0.6.0 is solid, and we want it on real networks before 1.0. If you run a homelab, look after
> a family network or support a small office, we'd love your bug reports, rough edges and
> feature requests.
>
> Join here: github.com/jxburros/GWatch

**Story** (with `social/story-1080x1920.png`; link sticker → releases page)

> Now in beta. Tap to try GWatch.

**Instagram / LinkedIn carousel caption** (with `carousel/`)

> Meet GWatch: one calm place for everything on your network. Swipe to see the dashboard, the
> network map, and how to join the beta. 🔗 github.com/jxburros/GWatch

**Hashtags (pick 2–4):** #homelab #selfhosted #networkmonitoring #monitoring

---

## Email invitation (with `social/email-header-1200x400.png`)

**Subject:** You're invited to the GWatch Beta

**Preview text:** Calm, local monitoring for your home network. Help shape 1.0.

> Hi there,
>
> We'd like to invite you to the GWatch Beta.
>
> GWatch is a calm monitor for your home network and its services. It checks your router,
> servers, websites and APIs on a schedule, keeps the history, draws a live map of what depends
> on what, and alerts you only when something actually needs you. It runs on your own hardware,
> and nothing leaves your network.
>
> **Joining takes about ten minutes:**
>
> 1. Download 0.6.0: https://github.com/jxburros/GWatch/releases/latest
> 2. Point it at your real network.
> 3. Tell us what breaks, what confuses you and what you wish it did:
>    https://github.com/jxburros/GWatch/issues
>
> Beta builds are signed and update themselves, and backups take one click, so trying it is
> low-risk.
>
> Thank you for helping us make 1.0 great.
>
> The GWatch team

---

## Beta FAQ

**Is it free?** Yes. GWatch is free to use, modify and run, including at work, under the
[GWatch Community License](../../LICENSE). The source is available on GitHub.

**Is the beta safe to run on my real network?** 0.6.0 works and looks after your data. Out of the
box it only watches: it acts on something only through triggers you set up yourself, and it never
exposes itself to the internet. The
interface and the JSON API may still change before 1.0, and bugs are likelier now than they will
be later. That's why we want your reports.

**What does it run on?** Windows (as a background service, with an installer), Linux, macOS,
Docker (`linux/amd64`, `linux/arm64`, `linux/arm/v7`) and 64-bit or ARMv7 Raspberry Pi.

**Does it need the internet or an account?** No. There's no cloud account, and your monitoring data
stays on your machine (see [`docs/PRIVACY.md`](../../docs/PRIVACY.md)). GWatch only needs the
internet for the things you ask it to check, the notifications you configure and its signed updates.

**How do updates work?** GWatch updates itself from signed releases and verifies each one
before installing, so beta testers get fixes as they ship.

**What feedback is most useful?** Anything that looks wrong, steps that took longer than they
should, a check type or integration you're missing, and the feature that would make you recommend
GWatch to a friend. File it at <https://github.com/jxburros/GWatch/issues>; screenshots help.

**What happens at 1.0?** 1.0 is the first public, stable release. Your install keeps updating
through it; the beta notices in the interface turn themselves off.

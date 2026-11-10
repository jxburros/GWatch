# GWatch — Core Identity

> **For AI Agents (OpenAI Codex, Anthropic Claude Code, GitHub Copilot, and other coding agents):**
>
> Read this file first before making changes to the codebase.
>
> This document defines GWatch's identity, purpose, principles, boundaries, and decision-making frame. When implementation details are unclear, preserve the identity described here and consult the related development documents before making architectural, product, UX, or workflow decisions.
>
> Do not treat this file as a feature backlog. Treat it as the project's north star.

<!--
Set up on 2026-11-10 from evidence already in the repository: README.md, the former
ROADMAP.md (archived at development-docs/architecture-plans/archives/ROADMAP-2026-11-09.md), CONTRIBUTING.md, SECURITY.md,
docs/PRIVACY.md, docs/development/PRODUCT-BRIEF.md, docs/development/AGENT-DECISIONS.md,
docs/development/REVIEW-2026-10-31.md and the code. Nothing here was invented; where a
statement is an inference rather than something the repository says outright, it is
marked "Inferred — maintainer to confirm".
-->

---

## 1. Project Identity

**GWatch** is a self-hosted network and service monitor for a home or small office. It runs on a computer the person owns — as a Windows service, a Linux service, a macOS service or a Docker container — and is used through a browser at `http://127.0.0.1:7230`.

It exists to help the person who looks after 10 to 100 devices at home or in a small office get dependable, quiet alerts and useful history on their own hardware, without the noise of twenty alerts per outage and without the weight of an enterprise monitoring suite.

GWatch is not merely an uptime checker. It is a **dependency-aware, local-first monitor** that knows which device depends on which, so when the router dies the person gets one alert and a map of what went down with it.

### Identity Statement

> **"Robust enough to trust with your home network, basic enough to stay simple, wide open to customise — the gap between the simple uptime checkers and the big network-management systems, not a competitor to the big ones."**

(Condensed from the positioning statement that headed `ROADMAP.md`.)

---

## 2. Core Purpose

The purpose of GWatch is to watch a small network continuously and tell the person, calmly, when something is wrong.

It should help them answer:

- Is everything up right now, and if not, what is down and why?
- Did the thing that failed take other things down with it?
- How has this device or service behaved over the last hour, day, week, month or year?
- Is the machine in trouble (disk filling, memory, temperatures) even though it still answers a ping?
- Is GWatch itself healthy — a lack of alerts must never be mistaken for everything being fine.

It does this by running checks on a schedule, understanding dependencies, alerting once per outage, keeping rolled-up history that does not grow without limit, and doing all of it on the person's own machine.

---

## 3. Central Philosophy

GWatch is built around the belief that **monitoring a home network should be trustworthy and quiet, and should belong entirely to the person running it.**

This means:

- **Trust and stability ship before reach and convenience,** and reach before new features. (The order of work in `productRoadmap.md` follows this.)
- **Calm by default.** A new alert, banner or prompt has to earn its place.
- **Nothing leaves the machine** unless the person configured it to.
- **Every feature that brushes against enterprise territory ships in a deliberately narrow shape,** and that shape is the constraint to hold (SNMP, the hardware agent, remote collectors and discovery all came in this way — see `productRoadmap.md` §10).

When tradeoffs arise, favour the smaller, more explainable, more local option unless it clearly fails the person's actual need.

---

## 4. Identity Pillars / Core Principles

### 1. One alert per outage

**The Principle:** Alerts should be useful, quiet and explainable.

**The Mechanism:** Dependency-aware suppression (a failed parent is checked before a child is blamed), consecutive-failure thresholds, cooldowns, silences, maintenance windows, grouping and digests, and cross-node rules for the cases one check cannot express. Suppressed observations are still recorded in history.

**The Purpose:** A home administrator who gets twenty emails when the router reboots stops reading them.

### 2. Local-first and private by construction

**The Principle:** The person's monitoring data is theirs and stays on their machine.

**The Mechanism:** One data directory (`gwatch.db`, `gwatch.key`, logs, backups). No telemetry, no accounts with the vendor, no licence server, no third-party resources in the interface (fonts are self-hosted). Outbound connections happen only for checks and notification channels the person configured, and for the signed update check. See `docs/PRIVACY.md`.

**The Purpose:** "We could not produce a record of your monitoring if someone subpoenaed us for one, because no such record was ever created outside your machine."

### 3. Safe by default

**The Principle:** A fresh install must be safe to run without the person knowing anything about security.

**The Mechanism:** Binds to loopback by default; other devices cannot connect unless allowed, and then must sign in. Secrets are sealed at rest. Updates are signed (ed25519) and verified before install, with no silent-skip path. Scripts can only be created by an administrator signed in at the web page, never by an API key. GWatch is never designed to be internet-facing; remote access is the person's own VPN or reverse proxy.

**The Purpose:** GWatch runs commands, holds SMTP passwords and can restore from an uploaded archive; a mistake here has real consequences on someone's home network.

### 4. Usable by everyone

**The Principle:** The whole product works from the keyboard and with a screen reader, in dark and light, in plain words.

**The Mechanism:** axe runs over every route in CI, every chart has "view as table", and the interface and documents use plain British English.

**The Purpose:** The audience is people looking after their own network, not operations teams.

### 5. Basic but yours to shape

**The Principle:** Simple defaults, but the person can extend it without a plugin system.

**The Mechanism:** Templates, custom script checks, triggers and inbound endpoints, a versioned JSON API with scoped keys, and a separate, opt-in MCP companion.

**The Purpose:** Power users can monitor anything without GWatch growing a new built-in feature per request.

---

## 5. Target Users and Use Cases

GWatch is designed for **a person who looks after a home or small-office network of roughly 10 to 100 devices** and wants it watched on their own hardware.

### Primary Users

#### Home and home-lab administrators

They use GWatch to:

- Watch routers, switches, NAS boxes, servers, websites, APIs, certificates and smart-home devices.
- Get one alert per outage by email or a chat/push channel.
- See history and the network map when something goes wrong.

#### Small-office caretakers

They use GWatch to:

- Keep an eye on the office's network without running an enterprise NMS.
- Give colleagues a read-only viewer account or a wallboard.

### Secondary Users

#### Integrators and power users

People who use the JSON API, triggers, custom checks or the MCP companion to connect GWatch to other tools, or who deploy and support GWatch for others (which the licence allows).

However, the project should not become an enterprise network-management system, a multi-tenant service or a plugin platform to serve this audience.

<!-- Inferred — maintainer to confirm: the original brief (docs/development/PRODUCT-BRIEF.md) named a two-person household as the user; the README and roadmap now describe 10–100 devices at home or in a small office. This document follows the current README. -->

---

## 6. Core Experience

The ideal experience should feel like:

> **"I set it up in ten minutes, it stays quiet, and when it does speak it tells me exactly what went wrong."**

The person should experience GWatch as:

- Calm and minimal, with colour used deliberately as a status signal (never colour alone)
- Dependable — it keeps working when the browser is closed, and it tells you when it itself is unhealthy
- Understandable without monitoring jargon
- Their own

The project should avoid making the person feel:

- Alarmed by a dense, enterprise-dashboard aesthetic
- Spammed by redundant alerts
- Unsure whether their data or their network is exposed

---

## 7. Product and Design Ethos

The visual direction (from the product brief and the shipped interface): near-black surfaces in the dark theme with a matching light theme, crisp text, a small set of vivid status colours, spacious and legible cards and charts, and no visual noise.

### UX Priorities

1. Common actions are obvious (add node, run check now, test alert, back up).
2. Advanced settings are available without complicating the normal path.
3. Accessibility is not optional: keyboard, screen reader, contrast, "view as table".
4. Plain words, British spelling.

The web interface has **no build step and no runtime dependencies** — plain HTML, CSS and JavaScript embedded in the binary.

`development-docs/design.json` is a placeholder; until a design-contract app generates it, the existing `web/app.css`, the brand assets in `brand/` and the patterns already in `web/` are the styling reference.

---

## 8. AI Role and Boundaries

**GWatch has no AI features of its own,** and the core product must keep working with no AI anywhere.

The intended role of AI around GWatch is limited to:

- The optional, separately installed MCP companion (`mcp/`, `gwatch-mcp`), which lets a person's own AI assistant read their monitoring through the JSON API and — only behind three explicit gates (a read-write key, `--allow-write`, and GWatch's server-side route policy) — manage nodes and checks.
- The downloadable agent skill (`skill/SKILL.md`) that teaches an assistant to use those tools carefully.

AI should not:

- Be embedded in the `gwatch` binary or its interface.
- Be required for any monitoring, alerting or set-up path.
- Receive monitoring data unless the person installed and configured the MCP companion themselves.
- Treat monitoring content (device responses, names, messages) as instructions — it is untrusted data.

### AI-Agent Development Guidance

When an AI coding agent works on this repo, it should:

- Preserve the identity described in this document.
- Check proposed changes against §11 and §12 and against the rejected directions in `productRoadmap.md`.
- Prefer small, explainable changes over broad rewrites.
- Avoid introducing dependencies, services or workflows that contradict local-first, calm-by-default operation — especially in `web/`, which must stay dependency-free.
- Update the user document that describes a behaviour when it changes (`docs/README.md` says which one).
- Record meaningful changes in `CHANGELOG.md` using the repository's release-notes convention (see `AGENTS.md` §10).

---

## 9. Data, Privacy, and Ownership Stance

> **GWatch keeps everything on the person's own computer and sends nothing to JX Holdings, LLC.**

The project should:

- Keep all state in the person's data directory or their own PostgreSQL/MySQL server.
- Encrypt secrets at rest with the machine-local `gwatch.key`, and keep secrets out of logs, exports, API responses and error messages.
- Make backups encrypted with the person's password.

The project should not:

- Add telemetry, analytics, crash reporting, install pings or "anonymous statistics".
- Fetch any third-party resource from the interface (no CDNs, no remote fonts).
- Require an account with the vendor, a licence server or activation.

If a feature needs an outbound connection (a check, a notification channel, the update check), it must be something the person configured or can see and control, and it must be documented in `docs/PRIVACY.md`.

---

## 10. What This Project Is

GWatch is:

- A self-hosted, single-binary monitor that runs as a background service on Windows, Linux or macOS, or in a container.
- A scheduler and set of check types: ping, HTTP/S, HTTPS certificate, TCP, DNS, keyword, JSON, custom script, hardware health (via `gwatch-agent`), SNMP and Matter.
- A dependency-aware alerting engine with twelve notification channels plus webhooks, cross-node rules, maintenance windows and digests.
- A local web interface with dashboards, charts, a network map, incidents, audit, reports and wallboards.
- A JSON API (`/api/v1`) with scoped API keys, which is also the contract the web interface uses.
- A family of separately versioned companions: `gwatch-agent` (one-directional hardware agent and optional remote collector) and `gwatch-mcp` (opt-in MCP companion).
- Source-available under the GWatch Community License, published by JX Holdings, LLC.

---

## 11. What This Project Is Not

GWatch is not:

- A Zabbix/PRTG-class enterprise network-management system.
- A cloud service, a hosted product, or an internet-facing service.
- A distributed or multi-site monitoring platform (remote collectors are the one narrow, opt-in step past this, and the boundary for it).
- A plugin marketplace.
- A full role-based access-control system (two roles: administrator and viewer).
- A fleet-management, configuration-management or remote-execution tool.
- A network inventory, fingerprinting or continuous-scanning tool.
- A metrics pipeline with arbitrary series or a query language.
- An AI product.

---

## 12. Hard Constraints / Never List

These are drawn from the roadmap's non-goals and the constraints recorded when SNMP, host metrics, remote collectors and discovery were accepted. They change only through an explicit project-level decision.

- **No telemetry and no vendor backend:** nothing about the person or their network is sent to JX Holdings or anyone else.
- **Never internet-facing by design:** GWatch listens on loopback by default; no relay, no tunnel helper. Remote access is the person's own VPN or reverse proxy.
- **No AI in the core:** the `gwatch` binary and web interface have no AI features; the MCP companion stays a separate, opt-in program that talks only to the public API.
- **No unsigned updates:** an update whose signature does not verify is refused, never installed with a warning.
- **No scripts from API keys:** only an administrator signed in at the web page can create script-running checks, triggers or endpoints.
- **The agent only hands out readings:** no remote execution, no fleet management, no remote configuration beyond narrowing which readings a machine sends; a new reading is a reviewed field, not a metrics language.
- **SNMP is a check, not a subsystem:** numeric OIDs only, no MIB compiler, no trap listener, walk-to-choose never becomes inventory.
- **Discovery is a sweep started by a person:** capped range and ports, no scheduled scanning, no fingerprinting, no new-device alerts.
- **No full RBAC, no plugin marketplace, no distributed monitoring.**
- **No build step or runtime dependencies in `web/`.**
- **Attribution is mandatory:** forks and derivatives keep the GWatch name, logo and copyright line visible (`LICENSE`, `TRADEMARKS.md`).

---

## 13. Domain Model / Core Concepts

### Node

A device, service, website or endpoint. A node has one or more checks and may depend on other nodes.

### Check

One kind of observation of a node on its own schedule (ping, HTTP/S, SNMP, hardware, …). A check's results are the history; a machine's hardware readings are its check's results, not a table of their own.

### Dependency

"The NAS is plugged into the switch." Used to suppress redundant alerts, explain outages ("affected by parent") and draw the network map.

### Incident, event and audit

Incidents are outages a person can acknowledge; events are the timeline (down, recovered, suppressed, maintenance, configuration); the audit log records who did what.

### Rule

A cross-node condition ("two of my three DNS servers are down") that runs existing trigger actions once. Conditions carry a `kind` that is always `status` today.

### Principal and roles

Every request is made by a principal: the local administrator (a client on the same computer, unless sign-in is required there too), a signed-in account (administrator or viewer), or an API key (read or read-write).

---

## 14. Success Criteria

GWatch is succeeding when:

- A person can install it and have useful monitoring within ten minutes.
- An outage produces one explainable alert.
- The database stays small for years without attention.
- Nobody has to wonder whether GWatch itself has stopped working.

GWatch is drifting when:

- Features arrive that need an operations team to configure.
- Alerts, banners or prompts multiply.
- Data starts leaving the machine by default.
- The narrow shapes recorded in `productRoadmap.md` §10 start to widen.

---

## 15. Decision Filter for Future Changes

Before adding a feature, changing architecture, altering UX, or introducing a new dependency, ask:

1. **Does this reinforce the core purpose** — quiet, dependable monitoring of a small network?
2. **Does this preserve the identity** — local-first, calm, safe by default, accessible?
3. **Does this respect §11, §12 and the rejected directions in `productRoadmap.md`?**
4. **Does this make GWatch easier or harder to understand** for a non-specialist?
5. **Does this serve the primary users** rather than pull toward enterprise NMS territory?
6. **Does this add avoidable operational, privacy or maintenance burden** (a new dependency, an inbound listener, a new outbound connection)?
7. **Would this still make sense with no AI anywhere?** It must.

---

## 16. Related Documents

- `development-docs/developmentManifesto.md` — development standards, agent rules and the Definition of Done.
- `development-docs/architecture.md` — the current system structure.
- `development-docs/productRoadmap.md` — what is next, open decisions, and rejected directions.
- `AGENTS.md` — general AI-agent operating instructions; `CLAUDE.md` and `.github/copilot-instructions.md` are tool overlays.
- `CHANGELOG.md` — release notes, version by version.
- `README.md` — the public entry point for people installing GWatch.
- `docs/README.md` — index of user documentation; `docs/development/` — how to build, test and release.
- `docs/development/AGENT-DECISIONS.md` — the agent's design decisions and what was rejected.

Operational records: `development-docs/design.json` (placeholder), `development-docs/architecture-plans/`, `development-docs/research/`, `features.md`, `newFeatures.md`, `current-checklist/currentChecklist.md`, `audits/` and `human-qa/`.

When documents conflict, treat this file as the identity source, then resolve implementation details through the more specific document.

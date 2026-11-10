# GWatch — Product Roadmap

> **For AI Agents and contributors:** this is GWatch's version-level direction: what is being worked on before 1.0, what comes after, what is deliberately deferred, and what GWatch has decided not to become. It is not a task tracker — each line points to a GitHub issue, and the issue is where the detail and discussion live. Check §10 (rejected directions and narrow shapes) before proposing any new capability.

<!--
Rebuilt on 2026-11-10 from the former ROADMAP.md (November 2026), every open issue on
jxburros/GWatch at that date, docs/development/REVIEW-2026-10-31.md and the "not yet"
notes in docs/. Each open issue was checked against the code at 0.8.10; the one found
complete (#194, release blockers) was closed. The original public-release plan, all of
it shipped, moved to architecture-plans/archives/original-public-release-plan.md.
Version numbers for the stages below have not been assigned by the maintainers.
-->

---

## Roadmap Status Legend

| Status | Meaning |
|---|---|
| **Current** | Being worked on now, in priority order |
| **Next** | Planned after the current stage |
| **Candidate** | Wanted, not yet scheduled |
| **Deferred** | Deliberately postponed, not rejected; has a revisit condition |
| **Rejected** | Decided against; do not implement without a new project-level decision |

---

## 1. Roadmap Purpose

This roadmap tells contributors and agents what GWatch is building toward, in what order, and what it will not become. Granular tasks live in [GitHub issues](https://github.com/jxburros/GWatch/issues); completed work lives in `CHANGELOG.md`; current structure lives in `architecture.md`.

---

## 2. Product Direction Summary

**Positioning:** robust enough for a person to trust with their home network, basic enough to stay simple, wide open to customise — the gap between the simple uptime checkers (Uptime Kuma and the like) and Zabbix/PRTG, not a competitor to the latter.

**Order of work:** trust before reach, reach before new features. Scope creep must not pull any item toward the rejected directions in §10.

**Destination:** 1.0.0, reserved for the first public, stable release (`CHANGELOG.md`).

---

## 3. Current Product State

### Current Version / Baseline

GWatch 0.8.10 (beta), gwatch-agent 0.8.7, gwatch-mcp 0.1.2, agent skill 1.0.2. The newest published release is 0.8.8; 0.8.9 and 0.8.10 are not published yet.

### Current Capabilities

Everything in the original public-release plan has shipped, along with much that was never on it: eleven check types (ping, HTTP/S, certificate, TCP, DNS, keyword, JSON, custom script, hardware via the agent, SNMP, Matter); dependency-aware alerting with twelve channels, rules, maintenance windows and digests; dashboards, charts, the network map (clusters, arranging by hand, what-if, presenting), incidents, audit, reports and wallboards; discovery and bulk edit; administrator/viewer accounts and scoped API keys; encrypted backups; signed self-update; SQLite, PostgreSQL or MySQL; the Windows installer, Linux packages, macOS service and Docker image; the hardware agent with Docker and Hyper-V readings and remote collectors; and the opt-in MCP companion. `features.md` is the granular inventory.

### Known Gaps in the Current Release

- **Unsigned Windows installers:** SmartScreen warns about an unknown publisher (#120). Release assets are signed with GWatch's own key; that is a different thing from Authenticode.
- **Re-running the Windows setup program keeps the first install's port and LAN setting** (#275).
- **Docker's first account** needs `docker exec` (#271).
- **No apt/dnf repository,** so `.deb`/`.rpm` installs do not update unattended (#223).
- **Agent readings differ by platform:** macOS has no processor-use reading or temperatures, Windows no temperatures (#224).
- **The phone layout is not usable yet** (#254–#258).

---

## 4. Current Version Focus

### Version Goal

Make GWatch trustworthy and easy to adopt for a public 1.0: close every known security finding, make the first five minutes work on every platform and on a phone, and settle the licence.

### Why This Version Matters

Two full reviews (31 October, of 0.8.5, and 9 November, of 0.8.9) found no critical or high issues but a long tail of low-severity hardening, packaging and first-run friction. A monitor people trust with their network has to clear that tail before it asks a wider audience to install it.

### Version Scope

Stages 1 and 2 below are the current focus, worked top to bottom. Stages 3–5 are next, in that order. Tracking issue for the October review: #199.

---

## 5. Current Version Development Items

### 5.1 Stage 1 — Trust: before anything else  **Current**

| What | Issue |
|---|---|
| Require sign-in on this computer once an account exists | #202 |
| A command to create the first administrator or reset a lost password | #288 |
| Harden the systemd unit that `gwatch-agent install` writes | #200 |
| Remove or lock the signing-key migration workflow; pin a second, offline update key; take signing out of the release commit's CI context | #247, #248, #207 |
| Remaining low-severity security findings: hook token in the query string, raw placeholders in script `WorkDir` and git `Repo`, forwarding headers without `--trusted-proxies`, viewers reading script code and the log, backup password floor, legacy LAN password notice | #203, #204, #205, #206, #208, #209 |
| Remaining findings from the 0.8.9 review: rate and concurrency on successful endpoint calls, wallboard token guessing, authorising on the routed pattern, event-stream write deadline and subscriber cap | #249, #250, #251, #252 |
| Supply chain: Dependabot for Go, npm and Docker; base images pinned by digest; SBOM and CodeQL | #253 |
| Build with a patched Go toolchain from source (`toolchain` directive) | #201 |
| Sign the Windows setup programs and binaries (Authenticode); the pipeline exists, the signing identity and a first signed release do not | #120 |
| Releases that match the version numbers; a lighter release pipeline | #222 |
| Tag the MCP module so `go install` gets the current version | #272 |
| Decide the licence and trademark policy, and the launch plan, before going public; have the policy documents reviewed | #10, #286 |

**Completion standard:** every issue above closed; no known security finding open; the published release matches `VERSION`.

### 5.2 Stage 2 — The first five minutes  **Current**

| What | Issue |
|---|---|
| A usable phone layout: the header, the page title, Help and Settings, the map, the node filters | #254, #255, #256, #257, #258 |
| Docker: a first administrator without `docker exec` | #271 |
| A new node gets a ping check, plus the other small UX fixes from the review | #220 |
| The tour ends with a first step | #263 |
| Discover prefills the server's own subnet even when GWatch listens only on this computer | #266 |
| Agent: a bare server address must not become `https://`; help text and errors must suggest `http://` | #287 |
| Small correctness fixes visible on first use: the dev-build update nag, the blank-chart copy contradiction, consistency fixes (time format, validation focus, long node lists) | #217, #216, #267 |
| Moderate accessibility notes (page-bar landmark, heading order, `/wall` heading) | #219 |
| An apt/dnf repository so packages update themselves | #223 |
| Windows setup: change the port and LAN setting on a re-run; smoke-test silent installs in CI | #275 |

**Completion standard:** a new person can install on Windows, Linux, macOS or Docker, create an account, and get a first result and alert without reading documentation or using a shell inside a container; every page works on a phone.

---

## 6. Current Version Completion Criteria (1.0)

1.0 ships when stages 1 and 2 are complete, the licence decision is made and written into `LICENSE`, `TRADEMARKS.md`, `CONTRIBUTING.md` and the policy documents, the JSON API is declared stable, and `docs/development/RELEASE-TEST-PLAN.md` passes on every supported platform.

<!-- Inferred — maintainer to confirm: whether stages 3–5 (or parts of them, such as the status page) are required for 1.0 or follow it. The former ROADMAP.md said only "trust comes before reach, and reach before new features". -->

---

## 7. Planned Future Versions

### Stage 3 — What people expect from a monitor in this class  **Next**

| What | Issue |
|---|---|
| A public status page | #226 |
| Two-factor sign-in (TOTP), then single sign-on (OIDC) | #276, #277 |
| A generic webhook as a first-class channel, then Apprise; Home Assistant in both directions | #227, #285 |
| Acknowledge or silence from the notification; an installable web app | #279 |
| A push (heartbeat) check type | #280 |
| A Prometheus metrics endpoint | #278 |
| Import from Uptime Kuma; configuration as a file (export and apply) | #281, #282 |
| Proxmox guests, as Docker and Hyper-V are watched today | #283 |
| Rule hold timers and metric conditions (see §9) | #284 |
| MCP tools for incidents and maintenance windows | #273 |
| Global search, a command palette and keyboard shortcuts | #259 |

**Why this comes next:** these are the features every peer in this class has and GWatch lacks. Each must still fit §10 — for example, a status page is opt-in and reached through the person's own reverse proxy; GWatch itself does not become internet-facing.

### Stage 4 — The network map  **Next**

Tracked together in #230.

| What | Issue |
|---|---|
| Nodes with more than one parent | #231 |
| Links that carry their own data | #232 |
| Topology discovered automatically (LLDP/CDP, SNMP bridge and ARP tables) — must stay a person-started read, not inventory | #233 |
| Device-type icons | #234 |
| Edit dependencies directly on the map | #235 |
| Saved views | #236 |
| Bends in lines | #237 |
| A view-only presentation link for people who are not signed in | #238 |
| More ways to make the map fit your network | #239 |
| Nearer term: undo after a drag; node colour that cannot be mistaken for status; very large maps; Esc ends a simulation and Present mode; Present mode and folded clusters | #260, #261, #262, #264, #265 |
| Map layout API: a lighter read and a guard against overwrites; restore validates the layout | #269, #268 |

### Stage 5 — Underneath  **Next**

| What | Issue |
|---|---|
| Take the database write out from under the engine's lock; back off on persistent write failure | #210, #211 |
| Real concurrency on PostgreSQL and MySQL | #212 |
| Smaller view functions; strict type checking across the web code | #215 |
| Backend structure (split `api.go`, use-cases out of handlers, no package-level SNMP state, unified migrations, structured logging); clock-injected, parallel tests | #214, #213 |
| Extend the Go↔JS mock contract to omitted keys and request bodies | #221 |
| History import reports skipped rows | #270 |
| The agent on macOS and Windows: the readings it lacks | #224 |
| Marketing and brand assets out of the main repository | #274 |

---

## 8. Later Ideas / Future Candidates

Recorded in the documentation but not yet tracked by an issue. **Candidate** — not approved scope.

- **An official Home Assistant add-on and an Unraid template** for the container image, "once the image has had some use" (former roadmap §4.2; `docs/DOCKER.md`). Related to, but distinct from, #285.
- **Neighbour-table (ARP) names in discovery on macOS** — "not yet on macOS" (`docs/API.md` → Discovery).

### Conditions for Promotion

A candidate is promoted when a maintainer opens an issue for it and places it in a stage, and it passes `coreIdentity.md` §15 and §10 below.

---

## 9. Deferred Work

- **Rule hold timers and metric conditions** (#284). The cross-node rules decision (#31) shipped the smaller version on purpose; the condition model carries a `kind` that is always `status` today so either can be added without reshaping stored rules. Revisit when someone has a rule that flaps or a threshold that is not a check's own. Now scheduled in Stage 3.
- **SMART readings** in the agent. Left out because SMART is patchy across vendors on Windows and needs elevated privileges on the watched machine. Revisit only if someone actually wants it, and only within the agent's constraints (§10).
- **Temperatures on Windows and macOS** — came in on Linux only (#132); the rest is #224.

---

## 10. Rejected / Out-of-Scope Directions

**Rejected.** Each would pull GWatch toward enterprise-NMS territory and away from the "basic but yours to shape" niche:

- **Distributed or multi-site monitoring platforms** — a second GWatch, federation, a central server for many sites. Remote collectors (#123) are the one narrow, opt-in step past this, and the boundary for it (below).
- **A plugin marketplace** or plugin system.
- **Full role-based access control** beyond the administrator/viewer split.
- **A relay, tunnel helper or anything that makes GWatch internet-facing by itself.** Remote access is the person's own VPN or reverse proxy (`docs/REMOTE-ACCESS.md`).
- **AI features inside GWatch.** The MCP companion stays separate and opt-in.
- **Paid tiers.** Everything ships free; the licence restricts only reselling unmodified GWatch or offering it as a paid hosted service.

### Narrow shapes that must hold

Several features that were once non-goals shipped in a deliberately narrow form. The shape is the constraint; widening it is a project-level decision.

**SNMP (#39).** A check, not a subsystem: a list of numeric OIDs polled on the check's own schedule, each a named metric through the same scheduler, alerting, charting and retention as every other check. No MIB files or MIB compiler, no trap listener. "Walk this device" is an editor aid, capped and one device at a time — never inventory.

**Host metrics and the agent.** One binary, one direction, one permission: in its usual mode `gwatch-agent` posts a reading and hangs up; GWatch never connects to it and holds no credential for it. Opt-in `gwatch-agent serve` lets GWatch poll it with a bearer token. Either way the agent only hands out readings: no remote execution, no fleet management, and the only remote configuration is narrowing which readings a machine sends, inside the limit in the machine's own file. No metric language: readings are a fixed struct, and a new reading is a reviewed field. Nothing to deploy: registering a machine is one screen and one command. Containers and virtual machines are readings, not a kind of node.

**Remote collectors (#123).** With both the machine's owner and the administrator opting in, an agent runs the built-in network checks (ping, TCP, HTTP and relatives, DNS, SNMP) of nodes pointed at it and posts the results — data from a closed list, never a script, over connections the agent opens. Not a second GWatch, a fleet or remote execution. Reasoning: `docs/development/AGENT-DECISIONS.md` entry 16.

**Network discovery (#42).** A sweep started by a person, then forgotten: at most 4,096 addresses, at most 24 TCP ports (ten by default), administrator only, one run at a time, results in memory until the next run or a restart, nothing added until ticked. No scheduled or continuous scanning, no inventory, no fingerprinting, no `nmap`, no SNMP probing during a sweep, no new-device alerts. Automatic topology discovery (#233) must fit within this.

---

## 11. Version Planning Notes

### Sequencing Rules

- Trust before reach, reach before new features.
- Security findings are never deferred behind features.
- An installer or package locks in an interface; change it only for a settled feature set.

### Product Risks

- **Licence:** the October review found the current source-available licence blocks distribution listings and community channels and reads as a risk to the self-hosted audience (`docs/development/REVIEW-2026-10-31.md` §7). Unresolved: #10, #286.
- **Version drift:** version numbers have run ahead of published releases (#222).

---

## 12. Open Product Questions

- **Licence and contribution terms** (#10, #286): keep the GWatch Community License, or move to an OSI licence such as AGPL-3 plus the existing trademark policy? Contribution terms are not written down yet (`CONTRIBUTING.md`).
- **What 1.0 requires** beyond stages 1 and 2 (see the note in §6).

### Decision Notes

- **Licensing/support model (decided earlier, now reopened by #10 and #286):** source-available; nobody may charge for unaltered copies (including a paid hosted service without significant modification) or remove credit to GWatch, JX Holdings and the original developers. No paid tiers for API keys or the MCP companion; deployment, support and customisation services remain allowed.
- **Notification rules across nodes (#31), decided as the smaller version:** status conditions across nodes and checks joined by all / any / at least N, running existing trigger actions once with a cooldown and an optional clear notification. Per-node alerts, triggers and dependency suppression are untouched. Hold timers and metric conditions deferred, not rejected (§9).
- **Remote access (2.5):** no relay, no tunnel helper; bring your own VPN or reverse proxy.
- **MCP tags:** `mcp/vX.Y.Z`.

---

## 13. AI-Coding-Agent Roadmap Guidance

AI agents should:

- Check whether requested work belongs to the current stage, and prefer current-stage work.
- Treat §8 candidates as unapproved scope.
- Check §10 before proposing anything that touches SNMP, the agent, collectors, discovery, remote access, roles or plugins.
- Use the issue for the detail; do not turn this file into a task list.

AI agents should not:

- Mark a stage complete, or move an issue between stages, without a maintainer's decision.
- Close issues, add roadmap items or remove deferred or rejected items without explicit direction.
- Use this roadmap to override `coreIdentity.md`, the manifesto or the current architecture.

If a requested change conflicts with this roadmap, identify the conflict and ask for direction.

---

## 14. Roadmap Maintenance Rules

Update this file when a stage begins or completes, product direction changes, a capability is added to or removed from the plan, a candidate is promoted, deferred or rejected, sequencing changes, or an open question is resolved.

Do not update it for ordinary implementation progress, individual bug fixes, refactors, copy changes or dependency updates; closing an issue that is listed here is recorded by GitHub and `CHANGELOG.md`, and the line can be removed when the stage is reviewed.

| Change type | Update this file? | Better location |
|---|---:|---|
| Stage scope or direction changes | Yes | here |
| Completed tasks and bug fixes | No | `CHANGELOG.md`, the issue |
| Architecture changes | Only if direction changes | `architecture.md` |
| Setup or usage changes | No | `README.md`, `docs/` |
| Identity or non-goal changes | Yes, with `coreIdentity.md` | both |

Review this roadmap at the start and end of each stage, before 1.0, and after each full review.

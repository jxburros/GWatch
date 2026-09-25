# GWatch Support Plan

> **DRAFT — Schedule A to the GWatch Commercial License and Support Agreement.**
> Fees are left blank on purpose; they go in the Order Form. Quote-block notes
> are drafting notes for JX Holdings; delete them before sending.

Version: `[date]`

This plan describes the support JX Holdings, LLC provides to customers with a
GWatch Commercial License and Support Agreement. Capitalized terms have the
meanings given in that Agreement.

## Plans at a glance

| | **Standard** | **Priority** |
| --- | --- | --- |
| Coverage hours | Business hours | Business hours |
| Channels | Email, private issue tracker | Email, private issue tracker, scheduled video calls |
| Authorized Contacts | 2 | 5 |
| Included support hours per year | 24 | 80 |
| Severity 1 first response | 1 business day | 4 business hours |
| Named support engineer | — | Yes |
| Quarterly check-in call | — | Yes |
| Advance notice of breaking changes | Release notes | Release notes plus direct notice before release |
| Upgrade assistance | How-to guidance | Guided upgrade session for each minor Release |
| Annual fee | See Order Form | See Order Form |

**Business hours** are 9:00–17:00 US Central Time, Monday to Friday, excluding
US federal holidays. Requests received outside business hours are treated as
received at the start of the next business day.

## Severity levels

Customer proposes a severity when opening a request. JX Holdings may adjust it
reasonably, and will say why.

| Severity | Meaning |
| --- | --- |
| **1 — Critical** | GWatch is down, or has stopped running checks or sending alerts, in production, with no workaround. Or: a suspected security vulnerability in GWatch. |
| **2 — High** | A major function (a check type, alerting, triggers, the web interface, backups, updates) is failing or badly degraded, and a workaround is impractical. |
| **3 — Normal** | A function is partly failing or behaves incorrectly, and a reasonable workaround exists. |
| **4 — Low** | A how-to question, a documentation issue, a cosmetic problem or a feature request. |

## Response targets

The target is the time to a first substantive response from a person: an
acknowledgment that includes an initial assessment or a specific request for
information. Targets are measured in business hours. They are targets, not
guaranteed resolution times.

| Severity | Standard | Priority |
| --- | --- | --- |
| 1 — Critical | 8 business hours (1 business day) | 4 business hours |
| 2 — High | 16 business hours (2 business days) | 8 business hours (1 business day) |
| 3 — Normal | 3 business days | 2 business days |
| 4 — Low | 5 business days | 3 business days |

For Severity 1, once it is acknowledged, JX Holdings works on it continuously
during business hours until a fix or workaround is available. It posts an
update at least once per business day.

**After-hours emergencies.** Not included in either plan. JX Holdings may help
with a Severity 1 outside business hours on a best-effort basis if someone is
available. That time is billed at `[1.5x]` the additional-hours rate and does
not count against included hours.

> Drafting note: before you offer any after-hours or 24/7 coverage, be sure the
> two of you can actually answer a phone at 3 a.m. on a holiday weekend.
> "Best-effort, billed" is the honest version.

## How to open a request

Authorized Contacts open requests by email at `[support@...]` or in the
private tracker at `[URL]`. Include:

1. the GWatch version (`gwatch version`) and how it was installed (Windows
   service, Linux/macOS console, Docker image);
2. the database backend (SQLite, PostgreSQL, MySQL/MariaDB) and its version;
3. what happened, what you expected, and when it started;
4. steps to reproduce, if known; and
5. relevant log excerpts or screenshots, **with passwords, API keys and SMTP
   credentials removed**.

A request from someone who isn't an Authorized Contact will be routed to one
before work starts.

## Included support hours

- Time spent on a request (investigation, replies, calls, remote sessions
  and preparing patches) counts against the year's included hours, in
  15-minute increments.
- **Time spent fixing a confirmed defect in GWatch itself does not count.**
  Customers don't pay twice for our bugs.
- Unused hours do not roll over to the next Term year.
- JX Holdings will tell Customer when 75% of included hours are used. After
  they run out, further time is billed at the additional-hours rate in the
  Order Form, with Customer's approval first.

## What's covered

- Installation, upgrade and configuration questions for Supported Releases on
  supported platforms.
- Diagnosing unexpected behaviour, and determining whether it's a GWatch
  defect.
- Defect fixes and workarounds for confirmed GWatch defects, normally
  delivered in a public Release, with an interim patch build if needed.
- Help restoring from a GWatch backup, and moving between database backends
  with `gwatch migrate-db`.
- Security advisories: Customer is notified directly when a Release fixes a
  security issue, before or at the time of public disclosure.
- Clarifying the Documentation.

## What's not covered

These are available as Professional Services under a Statement of Work:

- Writing custom check scripts, webhooks, triggers or integrations.
- Designing Customer's monitoring strategy, or configuring checks for
  Customer's specific devices beyond how-to guidance.
- Customer Modifications, and forks or builds of GWatch not produced by JX
  Holdings.
- Problems in Customer's network, hosts, reverse proxies, TLS, email
  servers, database servers or third-party services, beyond confirming that
  GWatch isn't the cause.
- New features. Feature requests are welcome and are considered for the
  roadmap, but a request is not a commitment to build it.
- Releases that are no longer supported (below).

## Supported Releases and platforms

- **Supported Releases:** the latest published Release, plus the previous
  minor Release for `[90]` days after a new minor Release comes out. For a
  problem on an older Release, the first step may be to upgrade.
- **Platforms:** the platforms and install methods described in
  `docs/INSTALL.md` for the Supported Release: the Windows service, the
  console program on Linux and macOS, and the official Docker image for
  `linux/amd64` and `linux/arm64`.
- **Databases:** embedded SQLite, and PostgreSQL and MySQL/MariaDB versions
  that `docs/DATABASE.md` lists as supported.
- GWatch is pre-1.0. The web interface, JSON API and data formats may change
  between minor Releases. Priority customers get direct notice of breaking
  changes before a Release ships.

## Escalation

If Customer is unhappy with how a request is going, an Authorized Contact
can escalate it by email to `[escalation contact]`. A principal of JX
Holdings will respond within one business day.

## Service credits `[optional]`

If JX Holdings misses the Severity 1 response target more than `[twice]` in a
calendar quarter, Customer may request a credit of `[5]%` of that quarter's
share of the annual Support Plan fee for each additional miss, up to `[15]%`
per quarter. The request must be made within 30 days after the quarter ends.
Credits apply to the next invoice, have no cash value, and are Customer's only
remedy for missed response targets.

> Drafting note: a small, capped credit reassures buyers without much risk.
> Leave it out if you'd rather not measure yourselves this precisely yet.

## Add-ons

Priced in the Order Form:

- **Onboarding package:** a fixed-scope engagement to install GWatch, set up
  accounts, alerting and backups, import or discover the first devices, and
  run a walkthrough session. Delivered within `[30]` days of the Effective
  Date.
- **Additional support hours:** billed hourly beyond the included hours.
- **Attribution Relief:** see Section 2.4 of the Agreement.
- **Professional Services:** custom development, integrations and on-site
  work, at the Order Form rate under an SOW.

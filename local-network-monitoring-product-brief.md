# Local Network Monitoring Application — Product Brief

## Document purpose

This document records the full agreed shape of a personal server and network monitoring application. It is written to be consumed by a future AI agent, designer, developer, or reviewer. It preserves the requested features, scope decisions, design direction, technical constraints, and suggested implementation approach without intentionally collapsing them into a shorter product summary.

## Product at a glance

Build a simple, easy-to-use, local-only monitoring application for a household of two people. It will primarily run on Windows, continuously monitor configured devices and services in the background, retain useful performance history, send email alerts, and provide all normal interaction through a browser-based local web interface.

The product should feel calming and minimal while using bold contrast: strong blacks and whites, dark theme, carefully limited but vivid status colors, and no visually noisy enterprise-dashboard aesthetic.

The product has no AI features.

## Core product statement

The application is an always-on, local Windows network and service monitor that provides dependable availability and performance monitoring, clear historical charts, controlled alerts, customizable local dashboards, easy node configuration, and long-term data retention that does not grow without limit.

It is not intended to become an enterprise monitoring platform, a cloud service, a public status-page service, or an AI-driven diagnosis product.

## Intended users

- Primary user: the requester.
- Secondary user: the requester's husband.
- Expected number of users: likely two people only.
- The application does not need organizational tenancy, teams, user roles, complex permissions, or collaborative workflows in its initial scope.

## Platform, installation, and runtime requirements

### Windows

- The application should ideally run on Windows.
- It should be easy to install.
- It should be easy to update.
- It should be appropriate for use on a personal Windows machine.

### Background operation

- Monitoring must continue when the normal interface is not open.
- Closing the browser or interface must not stop scheduled checks, history collection, retention work, backup work, or alerts.
- The monitoring engine should run as a Windows background service.
- The service should start automatically with Windows.
- Service upgrades should safely stop the old service, install the updated version, and restart the service.
- The application should make service health visible to the user.

### Local-only operation

- The application is local only.
- All normal operation should occur through a web interface.
- The web interface should be hosted by the local service and available only on the same computer, for example through a `localhost` address.
- The application should bind to localhost only and should not be reachable from other computers on the LAN or the public internet.
- The product should not require a cloud account, cloud database, hosted backend, or remote access service.
- Monitoring external websites and sending email naturally require outbound network connections, but monitoring data and the application itself remain local.

### Interface delivery

- The normal user experience is a local web interface opened in a browser.
- An installed application launcher may open the local interface in the default browser.
- A desktop/webview shell may be used for a more app-like launcher experience, but it must not be required for the monitoring service to run.

## Visual and interaction direction

### Design character

- Simple and easy to use.
- Calming and minimal.
- Boldly colorful and contrasting.
- Strong blacks and whites.
- Dark theme.
- Avoid the dense, alarming, visually busy style common in enterprise monitoring products.
- Use color as a deliberate status signal, not as decoration.

### Suggested visual system

- Near-black or charcoal page surfaces.
- Crisp white or near-white text with sufficient contrast.
- Electric green for healthy/operational status.
- Coral or red for outage/error status.
- Warm yellow for degraded/warning status.
- Vivid blue or purple for neutral navigation, selection, and informational emphasis.
- Do not communicate state with color alone; accompanying text/icons must remain clear.
- Keep dashboard cards, charts, and controls spacious and legible.

### Interaction principles

- Common actions must be obvious: add node, edit node, disable node, run check now, inspect last result, test alert settings, create dashboard, restore backup.
- The user should not have to understand monitoring jargon to add a basic website, router, home server, or API endpoint.
- Advanced settings should be available without making the normal path feel complicated.
- Helpful preset templates should populate sensible defaults.

## Primary information architecture

The primary navigation has three main areas. A read-only wallboard is a display mode, not a required fourth primary management area.

1. Dashboard
2. Nodes
3. Settings
4. Wallboard / status-screen mode (read only)

## Dashboard area

### Multiple dashboards

- The user can create multiple dashboards.
- Each dashboard can be named.
- Each dashboard can be customized independently.
- Each dashboard can arrange widgets.
- Dashboards can focus on different groups or use cases, such as Home Network, Servers, Media, Internet, Critical, or a troubleshooting view.

### Dashboard widgets

Potential widgets include all of the following:

- Overall health summary: healthy, degraded, down, unknown.
- Group status cards.
- Node/check status list.
- Latency chart.
- Response-time chart.
- Packet-loss chart.
- Uptime chart.
- Recent incidents and recoveries.
- Certificate-expiry warnings.
- Needs-attention list.
- Monitor-service health.
- Filtered table for a selected group or tag.

### Dashboard time ranges

Relevant charts should support the following requested time ranges:

- 1 hour.
- 24 hours.
- 1 week.
- 1 month.
- 1 year.

### Dashboard grouping

- Devices and services can be grouped with groups and/or tags.
- Examples: Home Lab, Home Network, Servers, Media, Internet, Critical.
- Grouping may be configured in Nodes and used for dashboard filters, widgets, wallboard layouts, maintenance windows, and alert dependency relationships.

## Nodes area

### Node definition

A node represents a device, service, website, endpoint, router, NAS, server, application, or API. A node may have multiple checks.

Examples:

- A router node can have Ping, DNS, and TCP port checks.
- A Plex Server node can have Ping, TCP port 32400, and HTTP/S checks.
- A public website node can have HTTP/S, HTTPS certificate, keyword, and DNS checks.
- An API endpoint node can have HTTP/S and JSON assertion checks.

### Node management requirements

- Show a list of all monitored things.
- Make it easy to add nodes.
- Make it easy to remove nodes.
- Make it easy to edit nodes.
- Make it easy to configure nodes and attached checks.
- Support enabling and disabling nodes/checks without deleting their configuration.
- Support searching and filtering the list.
- Support groups/tags.
- Support notes.
- Support an optional importance/criticality level.
- Support duplicating a node or check when useful.

### Node templates

Provide simple templates that prefill sensible configuration values:

- Website.
- Home server.
- Router.
- API endpoint.

Templates are a convenience layer. Users must still be able to modify each generated setting.

### Per-check configuration

Each attached check should support settings appropriate to its type, including where relevant:

- Check interval/frequency.
- Timeout.
- Retry count.
- Consecutive-failure threshold before alerting.
- Enabled/disabled state.
- Alert settings or overrides.
- Node/group association.
- Dependency relationship.
- Maintenance/quiet-window behavior.

### Requested check frequency

- The user needs to define how frequently Ping and HTTP/S checks run.
- Example frequencies explicitly requested: 1 minute, 5 minutes, and similar intervals.
- Frequency should be configurable per check, not only globally.
- The application may provide sane defaults and protect against accidental overload from extremely aggressive schedules.

### Run now / test configuration

Every monitor/check must provide a Run now action.

The result view should show, where applicable:

- Timestamp.
- Success/failure state.
- Error reason.
- DNS timing.
- Connection timing.
- TLS timing.
- HTTP request timing.
- Final URL after redirects.
- HTTP response status code.
- Certificate details for HTTPS.

This feature is intended to make node configuration easier to validate and debug before waiting for the next scheduled run.

## Check types

The application should support the following monitor/check types.

### Ping / ICMP checks

Purpose: determine reachability and network latency.

Required measurements:

- Ping response time in milliseconds.
- Average latency.
- Minimum latency.
- Maximum latency.
- Jitter.
- Packet loss when measurable.

### HTTP/S checks

Purpose: check a webpage, web application, or HTTP API endpoint.

Required capabilities:

- Make HTTP and HTTPS requests.
- Record response status code, such as 200, 404, 503, and other codes.
- Record response time.
- Support configurable expected status code(s).
- Support a single expected code or a set/range of allowed codes.
- Support following redirects.
- Show the final URL after redirects.
- Surface request, connection, TLS, timeout, and response errors clearly.

### HTTP/S response-code change monitoring

- The user may need to know when a returned HTTP/S code changes, for example when a normally healthy service stops returning its expected response.
- A check should be able to alert when its returned status differs from its configured expected result or allowed set.
- A response-code change should be described as an unexpected response/status change, not automatically described as a confirmed compromise or hack.

### HTTP/S response/content change monitoring

- The user expressed possible interest in detecting when an HTTP/S return changes because a site may have been hacked or modified.
- This should be an optional advanced rule rather than a default behavior.
- Supported comparison targets may include selected response headers, a keyword, a JSON value, a selected page region/text snippet, a normalized content hash, or a final redirect destination.
- Raw page bodies should not be stored by default simply to enable change detection.
- The configuration must make it clear that dynamic timestamps, ads, rotating content, login pages, and similar expected changes can cause false positives.
- The UI should label findings as response/content changed, not as a definitive security conclusion.

### HTTPS certificate checks

HTTPS checks should expose certificate information and monitoring:

- Certificate validity.
- Certificate issuer.
- Certificate expiration date.
- Days remaining before expiration.
- Alerts before expiration at configurable thresholds.
- Errors when certificate validation fails.

### TCP port checks

Purpose: determine whether a TCP endpoint accepts a connection without requiring a full application-protocol monitor.

Examples explicitly relevant to the product:

- Port 22.
- Port 80.
- Port 443.
- Port 32400.

### DNS checks

Purpose: determine whether a hostname resolves correctly.

Capabilities:

- Verify that a hostname resolves.
- Optionally verify that the returned DNS record/IP matches an expected value.
- Report DNS errors and timing where possible.

### Keyword checks

Purpose: verify that an HTTP/S response contains, or does not contain, expected text.

### JSON checks

Purpose: verify that an API response contains an expected value or path.

## Alerting

### Email alerts

- Email alerts are requested.
- Settings should allow configuration of an SMTP sender/provider.
- Provide a test-email action.
- The initial product should prioritize email rather than integrating many notification providers.
- The architecture may allow one simple optional alternative alert channel later, such as a webhook or phone-friendly service, but this is not required for the initial product.

### Alert behavior

Alerts should be useful, quiet, and explainable.

Each monitor/check should be able to participate in rules such as:

- Alert after a configured number of consecutive failures.
- Alert for high latency.
- Alert for packet loss.
- Alert for an unexpected HTTP status code.
- Alert for an HTTP/S response or content change.
- Alert for HTTPS certificate expiration or validity problems.
- Alert for a DNS expectation failure.
- Alert when a service misses a scheduled/expected result, if a heartbeat-style check is added later.
- Send a recovery notification when the check becomes healthy again.
- Suppress repeated alerts for a configurable cooldown period.
- Apply global defaults with optional per-check overrides.

### Dependency-aware alerting

Nodes/checks must support dependencies.

Example required behavior:

- If the router or internet gateway is down, downstream nodes may also fail.
- The app should avoid sending 20 separate downstream “site unavailable” emails in this situation.
- Dependent failures should be recorded/displayed as affected by the parent outage.
- The dashboard and incident views should make the relationship understandable, for example: “Plex unavailable because Gateway is down.”
- Dependency-aware suppression should suppress redundant notifications without hiding the underlying observations from history.

### Maintenance windows and quiet periods

- The user can create maintenance windows/quiet hours for a node or group.
- Planned reboot, update, or maintenance activity should not create unwanted alerts.
- Maintenance should still be recorded in the timeline/history.
- The relevant monitor/group can be visibly marked as in maintenance.
- The system should record when maintenance begins and ends.

## Incident timeline and event history

The application must maintain a compact incident/event timeline because event history is often more useful than a chart during troubleshooting.

Record the following event types:

- A monitor/check went down.
- A monitor/check recovered.
- An alert was sent.
- An alert was suppressed.
- A monitor/check was manually silenced.
- A maintenance window began.
- A maintenance window ended.
- A relevant configuration change occurred.
- A certificate warning began.
- A certificate warning cleared.
- Dependency suppression/affected-by-parent context when relevant.

Potential later enhancement:

- Allow a user to annotate the timeline with notes such as “rebooted router,” “ISP outage,” or “updated Plex.”

## Historical data and long-term retention

### Required history

The user needs performance history over:

- 1 hour.
- 24 hours.
- 1 week.
- 1 month.
- 1 year.

### Storage goal

History must remain useful for long periods without allowing the database to grow indefinitely, crash, or become unsustainable.

### Retention and rollups

The design should retain granular data at first and progressively aggregate it over time. This resembles the requested behavior in Nagios XI: newer data is detailed; older data is trimmed or compressed into less granular summaries such as hourly data.

Suggested configurable defaults:

| Data age | Detail retained |
| --- | --- |
| First 30 days | Every raw check result |
| 1–6 months | 5-minute summaries |
| 6–24 months | Hourly summaries |
| Long-term | Daily summaries, or automatic deletion according to configuration |

Rollups should preserve meaningful measurements, including:

- Check/sample count.
- Successful-check count.
- Failed-check count.
- Minimum response time.
- Maximum response time.
- Average response time.
- Jitter statistics.
- Packet-loss information.
- Availability/uptime percentage.

Retention intervals must be configurable. The product should clearly tell the user what will be kept, rolled up, and deleted.

### Database direction

- Use a local embedded database rather than requiring the user to install and maintain a database server.
- SQLite is the recommended initial database because it is a portable local file and fits a two-user, local-only application.
- Use disciplined background writes, a single queued writer, and appropriate transaction handling.
- Use write-ahead logging (WAL) or equivalent safe configuration for responsive read access while background checks write results.
- Retention and aggregation must run as managed background jobs.
- Monitor health should show database size and retention/rollup status.

## Reports and export

### Requested reports

- Reports to export are primarily line charts of monitored performance metrics.

### Export capabilities

- Export line charts as an image or other appropriate chart artifact.
- Export historical data as CSV.
- Export incident history as CSV where useful.
- Avoid making elaborate report builders an initial requirement.
- PDF export may be a later convenience, but it is not required for the first release.

## Backup and restore

The application must make it easy to move to a replacement Windows computer without recreating every monitor manually.

Required capabilities:

- One-click local backup.
- Backup of configuration.
- Optional backup of performance history and incidents.
- Password-encrypted backup archives.
- Restore from a backup archive.
- Clear success/failure feedback.
- Last-backup timestamp and result visible in monitor health/settings.
- Local storage only; no required cloud backup destination.

## Monitor health / self-observability

The application must expose its own health. A lack of alerts must not be mistaken for evidence that every monitored system is healthy.

Show the following in a Monitor Health view and/or dashboard widget:

- Whether the background Windows service is running.
- When a check last completed successfully.
- The next scheduled check.
- Whether the monitoring computer was asleep, rebooting, or offline.
- Database size.
- Retention and rollup status.
- Last backup time.
- Backup success/failure state.
- Recent internal errors.
- Useful diagnostic log access appropriate for a local personal app.

## Read-only wallboard/status-screen mode

The product should provide a local, read-only wallboard/status-screen mode suitable for a spare monitor, tablet, or permanently open browser tab.

The wallboard should show at a glance:

- Overall health.
- Group status.
- Current incidents.
- Key availability/latency trends.
- Certificate warnings.

Wallboard scope limits:

- It stays local.
- It is not a public status page.
- It does not require remote access.
- It does not require building a public-facing status-page product.

## Suggested technical shape

This is an architectural direction, not an irreversible implementation mandate.

```text
Windows background monitoring service
  ├─ Scheduler: runs configured checks at configured intervals
  ├─ Check runners: Ping, HTTP/S, HTTPS certificate, TCP, DNS, keyword, JSON
  ├─ Result processor: measures, normalizes, evaluates results
  ├─ Alert evaluator: thresholds, consecutive failures, dependencies, cooldowns
  ├─ Email sender: SMTP configuration and delivery
  ├─ Incident/event recorder
  ├─ Retention and aggregation jobs
  ├─ Backup/restore manager
  ├─ Local embedded database
  └─ Localhost-only web API

Local browser interface
  ├─ Dashboard: multiple widget dashboards
  ├─ Nodes: list, templates, configuration, Run now, result details
  ├─ Settings: general, alerts, retention, backups, updates, health
  └─ Wallboard: read-only local display mode
```

## Suggested implementation priorities

### Foundation / first milestone

1. Windows installation and background service.
2. Localhost-only web interface and local database.
3. Node list and easy add/edit/delete/enable/disable workflows.
4. Ping checks.
5. HTTP/S checks with redirects, expected status codes, and timing.
6. Basic dark interface.
7. Raw result storage plus data-rollup/retention foundation.
8. Basic dashboard and 24-hour/7-day/30-day charts.
9. Email failure/recovery alerts.
10. Service/database/last-check health visibility.

### Strong initial release additions

1. HTTPS certificate validity/expiry monitoring.
2. TCP port checks.
3. DNS checks.
4. Keyword and JSON checks.
5. Dependency-aware alerting.
6. Maintenance windows.
7. Incident timeline.
8. Node templates.
9. Run now and detailed result inspection.
10. Backup, restore, CSV export, and chart export.
11. Multiple customizable dashboards.
12. Local read-only wallboard.

### Later refinements, not required for the initial scope

- HTTP/S content normalization and change detection refinements.
- Timeline annotations/notes.
- A limited second notification channel, such as a webhook or phone-friendly service.
- More advanced dashboard widget customization.
- Optional heartbeat/push monitors for backup jobs or scripts.
- PDF export convenience.

## Explicit non-goals and deferred scope

The following are deliberately not required for the initial product because they would make the app more complex without supporting the central personal-monitoring goal:

- AI assistance.
- AI analysis.
- Any AI feature in the application.
- Cloud accounts.
- Cloud synchronization.
- Remote/public access.
- Public status pages.
- User roles.
- Organizations/teams/tenancy.
- Enterprise permission systems.
- SNMP monitoring.
- Hardware-sensor monitoring.
- Docker/container management.
- Full CPU/RAM/disk monitoring agents.
- Network discovery/scanning.
- Ticketing systems.
- Elaborate workflow automation.
- Large catalogs of alert-provider integrations.
- SMS integrations as an initial feature.

## Quality bar

The app should optimize for the following outcome:

> A polished, always-on Windows monitor for a personal home network and services: strong monitoring basics, clear current status, long-term performance history, alert behavior that does not spam, easy local dashboards, easy backup, and no unnecessary enterprise machinery.

## Completion criteria for the intended first product

The first product is on target when both intended users can:

1. Install it on Windows without separately administering a server database.
2. Start it once and trust monitoring to continue when the browser is closed.
3. Open a local browser interface and see Dashboard, Nodes, and Settings.
4. Add a router, home server, website, API endpoint, or TCP service through understandable forms/templates.
5. Configure 1-minute, 5-minute, or other check intervals.
6. Monitor Ping, HTTP/S, HTTPS certificates, TCP ports, DNS, keyword responses, and JSON expectations.
7. See current status and detailed last-result troubleshooting information.
8. Create multiple dashboards with useful status and chart widgets.
9. Group nodes and use those groups in dashboards and maintenance windows.
10. See history at 1-hour, 24-hour, 1-week, 1-month, and 1-year scales.
11. Trust that older data has been safely aggregated instead of growing endlessly.
12. Receive an email when a meaningful failure occurs and one when it recovers.
13. Avoid cascades of redundant alerts when a parent dependency such as the router/gateway is down.
14. See a concise incident timeline with outages, recoveries, maintenance, suppression, and configuration events.
15. Export line-chart/report data and CSV history.
16. Back up and restore all configuration and, when chosen, history.
17. Check that the monitor service, scheduler, storage, and backups themselves are healthy.
18. Put a local read-only wallboard on a spare display if desired.
19. Use the application without encountering cloud requirements, public exposure, or AI features.

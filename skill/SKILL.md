---
name: gwatch
description: How to use the GWatch MCP tools (gwatch_*) to answer questions about a home or small-office network monitor — what is up, what is down, why, and since when — and, only when the tools for it exist, to make careful changes to what is monitored. Use whenever a gwatch_* tool is available.
version: 1.0.0
---

# Working with GWatch

GWatch is a self-hosted network monitor for a home or small office. It runs checks (ping, TCP,
HTTP, DNS, SNMP, hardware readings) against **nodes** — a router, a NAS, a website, a printer — on
a schedule, keeps the results, and raises incidents when something stops answering. You reach it
through the `gwatch_*` tools of the `gwatch-mcp` server. The person you are helping is usually the
one who set it up: treat them as the administrator of a small network, not as an operations team.

## Start with the overview

Whenever the question is "how is everything" or you have no context yet, call `gwatch_overview`
first. It returns the up/degraded/down/unknown tally, status per group, the checks that need
attention, the open incidents and any certificates about to expire. Most questions end there.
Summarise it in two or three sentences before offering detail: what is down, what is degraded,
and whether anything is expiring soon. If everything is up, say so plainly.

## Finding a node

- `gwatch_list_nodes` lists every node with its status and one line per check. Narrow it with
  `group`, `tag`, `status` or `q` (substring of name or host). A node may belong to several groups.
- `gwatch_groups` tells you which groups and tags exist, so you can filter instead of guessing.
- `gwatch_get_node` with an `id` from the list gives the node in full: configuration, every check
  and the live state of each. Use it when a person asks about one device by name.

When the person names something loosely ("the NAS", "the living-room TV"), list nodes with `q`
first and confirm which one they mean if more than one matches.

## Drilling into a problem

1. `gwatch_get_node` to see which check is failing and what its last message and error say. The
   message usually already names the cause: "connection refused", "no such host", "timeout".
2. `gwatch_check_results` for that check (`checkId`, newest first) to see whether it is failing
   every run or only sometimes, and what the latency looked like before it broke.
3. `gwatch_history` for the trend. `range` accepts exactly `1h`, `24h`, `7d`, `30d` or `1y`
   (default `24h`). It returns availability, average/min/max latency and a downsampled series.
   Pass several `checkIds` to compare — for example the router against the device behind it.
4. `gwatch_events` for the narrative: outages, recoveries, warnings, notes and configuration
   changes, newest first. Filter with `nodeId`, `type`, `q`, `since`/`until` (RFC 3339,
   `2006-01-02T15:04` or `2006-01-02`). A `type` of `down` also returns the matching `recovered`
   events unless `exact` is true, which is what you want for "when did it go down and come back".

For "was it slow last night", use `gwatch_history` with `24h` and read the latency series; for
"has this been happening for a while", use `7d` or `30d` and look at availability.

## Reading the statuses

- **up** — the last check succeeded.
- **degraded** — answering, but with a warning: high latency, packet loss, an expiring
  certificate or changed page content. Not an outage; say what the warning is.
- **down** — the last check failed after its retries. This is the one that matters.
- **unknown** — no result yet: a new node, or the service has just started.
- **paused** — disabled by the person on purpose. Do not report it as a problem.
- **maintenance** — inside a maintenance window. Expected; alerts are held.

An `alert_suppressed` or `affected_by_parent` event means GWatch decided not to alert about a
node because the thing it depends on (typically the router or the internet link) was already
down. Report the parent as the problem and the children as consequences, not as separate
outages. Several nodes going down in the same minute almost always means the upstream device or
the link, so check the router or gateway node first.

`gwatch_health` is about the monitor itself — whether the scheduler is running, when it last ran
a check, recent internal errors. Use it when results look stale or the person doubts the monitor
rather than the network. A `monitor_gap` event means the monitoring computer was asleep or off,
not that the network was.

## Changing things: only if the tools exist, and carefully

The write tools (`gwatch_create_node`, `gwatch_update_node`, `gwatch_delete_node`,
`gwatch_set_node_enabled`, `gwatch_run_node`, `gwatch_silence_node`, `gwatch_add_note`,
`gwatch_test_check`) are only registered when the server was started with `--allow-write`
**and** GWatch itself only accepts them from an API key with the `readwrite` scope. Never assume
they exist: check your tool list. If they are missing, say that this connection is read-only and
that the person can enable writes in GWatch under Settings › AI & MCP if they want to.

A tool error of **HTTP 403 "This API key is read-only"** means the key GWatch was given cannot
write, whatever the server advertised. Do not retry with different arguments or another tool: the
refusal is deliberate and comes from GWatch. Tell the person what you tried and why it was refused.

Settings, backups, user accounts, API keys, automation triggers, custom endpoints, software
updates and the service log are never reachable through an API key. There are no tools for them.
If asked, explain that those are done in the GWatch web interface by an administrator.

When the write tools are present:

- **Prefer the reversible action.** Silence (`gwatch_silence_node`, in minutes) rather than
  disable; disable (`gwatch_set_node_enabled`) rather than delete. Deleting a node destroys its
  checks, results and history.
- **`gwatch_delete_node` requires `confirm: true`** and refuses without it. Only pass it after the
  person has confirmed the deletion of that specific node by name in this conversation.
- **Test before you create.** Run `gwatch_test_check` with the check configuration first; it runs
  once, records nothing and shows whether the target answers. Then `gwatch_create_node`. Look at
  `gwatch_templates` for the checks GWatch would normally give that kind of device.
- **`gwatch_update_node` changes only the fields you pass.** Omit `checks` to leave the checks
  alone; passing a `checks` list replaces all of them.
- **`gwatch_run_node` runs the real checks** and the results are recorded and alerted on as
  usual. Use it to confirm a fix, not as a way to poll.
- **Leave a trail.** After a change made on the person's behalf, `gwatch_add_note` with what was
  done and why, attached to the node. The note appears in GWatch's event timeline.
- Say what you are about to change before you change it, and report the result afterwards.

## How to report what you find

- Lead with the answer: "Your NAS has been down since 02:14; everything else is up."
- Give times as GWatch reports them and durations in plain words ("about three hours"), not raw
  timestamps.
- Name devices by their GWatch node name, with the host in parentheses when it helps.
- Distinguish "the device is off or unreachable" (ping fails) from "the device is on but the
  service is not answering" (ping works, TCP or HTTP fails). That is usually the most useful
  thing you can tell a home-network administrator.
- Suggest the next physical step when it is obvious: power-cycle the device, check the cable, look
  at the router first if several things went down together.
- Do not speculate about causes the data does not support. "The check timed out" is a fact;
  "the disk failed" is a guess. Say which is which.
- Keep it short. The person can ask for more.

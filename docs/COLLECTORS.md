# Remote collectors

GWatch checks things from the machine it runs on. A second office, a parent's
house or a client's site is on a network that machine cannot reach, and until
now the answer was a second GWatch there. A **remote collector** is the other
answer: a machine at that site already running the [GWatch agent](HARDWARE.md#another-machine-with-the-agent)
also runs some of GWatch's checks from where it is, and sends the results back
over the connection it already makes.

The site's devices then appear in your GWatch like any other node — with their
history, incidents, alerts, notification rules, maintenance windows and
silences all working as they do for a node checked from here — and nothing at
the site is opened to the outside.

## What a collector runs

Only GWatch's built-in network checks:

| Runs on a collector | Stays on this server |
| --- | --- |
| Ping, TCP port, HTTP/S, Keyword, JSON, HTTPS certificate, DNS, SNMP | Custom scripts, Hardware health, Matter |

A script never runs on a collector, and cannot be made to: running GWatch's
commands on another machine is exactly the remote execution the agent was built
never to allow (see [the decisions](AGENT-DECISIONS.md#16-an-agent-may-run-gwatchs-network-checks-from-where-it-is-if-both-sides-agree)).
A hardware check reads the machine an agent is already reporting, and a Matter
check needs its sidecar on GWatch's own network.

A node checked from a collector can still have checks of the other kinds; they
simply go on running here.

## Setting one up

It takes **two switches, one on each side**, and neither does anything alone.

1. **On the machine.** In the agent's `agent.json` (`gwatch-agent config path`
   says where), set

   ```json
   "collector": true
   ```

   and restart the agent (`gwatch-agent restart`). Or set
   `GWATCH_AGENT_COLLECTOR=true` in its environment, which is the easier way in
   a container. Without this the agent ignores anything GWatch says about
   checks, and GWatch sends it none.

2. **In GWatch.** Open the machine's node, and in the agent card's **Remote
   collector** section switch on **Use this machine as a remote collector**.
   The card then says how things stand: *waiting for the machine* (step 1 is
   not done yet, or the agent has not checked in since), *no checks yet*,
   *running N checks*, or *unreachable*.

3. **Point nodes at it.** Edit a node at that site and set **Check from** to the
   collector. Save; the collector picks the change up at its next check-in,
   which for a collector is every two minutes, and starts running the node's
   network checks on their own intervals. Set **Check from** back to *This
   server* to move them home.

The **Test this check** button in the editor still tests from this server.
**Run now** on a collected check is refused, since this server may not be able
to reach the device at all; the collector runs it on its schedule.

## What is sent to the collector

The collector is given, for each check it runs: the check's id, type, name,
interval, timeout and retries; its target, resolved (the check's own target or
the node's host); and the settings that check type reads — expected status,
keyword, JSON path, DNS record type and so on.

**That includes the credentials some checks carry**: an SNMP community string
or v3 passwords, and the HTTP headers of an HTTP, Keyword or JSON check, which
often hold an API key or an `Authorization` header. GWatch keeps these sealed on
disk and never shows them back in its own interface, but a collector has to
have them to run the check, so:

- they are sent **only to the collector a node is assigned to**, and only once
  both switches above are on — an agent that has not opted in is sent no checks
  at all;
- they travel **only over the agent's own connection to GWatch**, inside the
  answer to its check-in. Run GWatch behind HTTPS (see
  [`REMOTE-ACCESS.md`](REMOTE-ACCESS.md)) if a collector reaches it over the
  internet: over plain `http://` the credentials cross the network in the clear;
- the agent keeps them **in memory only**. It writes nothing it was handed to
  disk, so after a restart it runs nothing until its first check-in, a few
  seconds later;
- anyone with administrator rights on the collector machine can read the
  agent's memory, and so these credentials. Choose the collector accordingly,
  and prefer read-only SNMP communities and narrowly-scoped API keys for checks
  it runs.

Nothing else of GWatch's goes to it: no other node, no settings, no other
check's credentials, and no command, script or file.

## When the collector goes quiet

A collector sends its results every few seconds, and an empty "still here"
batch at least once a minute. If GWatch hears nothing from it for three of its
shortest check interval (and never less than three minutes):

- every check it runs becomes **unknown** — nobody knows whether those devices
  are up — and says why ("Waiting for the remote collector office-pi, which has
  sent nothing since 14:02");
- **one** `collector_unreachable` event, and **one** alert through your usual
  alert e-mail and channels, are raised for the collector, rather than a down
  alert for every device behind it. This is the same reasoning as a node's
  *Depends on*, applied to the thing doing the checking;
- the machine's own node, if it has a hardware check, goes down separately when
  its readings stop, which is its own alert.

When results arrive again a `collector_back` event is recorded (and a recovery
notice sent, if you get those), and each check takes up its own state from its
next result. A check that was down before the collector went quiet is simply
still down — it does not alert a second time for the same outage.

If collecting is turned off in GWatch, the machine stops allowing it, or the
agent is revoked, the node's checks go unknown in the same way, and the message
on them says which.

## When GWatch is unreachable from the site

The collector goes on running its checks, and holds the results the way the
agent holds readings: in `agent-results.jsonl` beside `agent.json`, bounded by
the same `buffer` setting (six hours by default), 4,000 results and about 8 MB,
oldest dropped first, so a restart or a power cut during the outage does not
lose them. When GWatch answers again they are sent, oldest first, and filed at
the time they were taken. Results delivered late fill in the history only: they
do not raise alerts for something that has already happened.

## Questions

**Can GWatch reach the collector machine now?** No. The collector still only
dials out. GWatch has no address for it, no credential for it, and no way to
make it do anything except, at its next check-in, hand it a list of network
checks — which it runs only because its own `agent.json` says it may.

**Does a collector need an inbound port?** No.

**Can one agent be a collector and report its own hardware?** Yes; it does both,
over the same connection and with the same token.

**How many checks can one collector run?** Up to 500, eight at a time.

**What does it look like in the API?** `collectorId` on a node, the collector
fields on an agent, `PUT /api/agents/{id}/collector`, and `/ingest/results` —
see [`API.md`](API.md#submitting-check-results-remote-collectors).

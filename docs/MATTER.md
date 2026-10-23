# Matter devices

GWatch can read Matter sensors, plugs, lights and bridges — a room's temperature
and humidity, whether a plug is on, whether a door is open, whether anyone is in
the hall, how bright it is, how much battery a sensor has left — and chart and
alert on each reading the way it does an SNMP reading. It does this through
**python-matter-server**, the Matter controller the Home Assistant project
maintains, running beside GWatch as a sidecar:

```
GWatch ──WebSocket──▶ python-matter-server ──Matter (IPv6, Wi-Fi/Ethernet/Thread)──▶ devices and bridges
       ws://host:5580/ws        (holds the fabric)
```

GWatch is not a Matter controller and does not join the Matter fabric itself.
Commissioning, the fabric's root certificate and every device's operational
credentials are created and kept by the sidecar, in its own storage directory.
GWatch stores only the sidecar's WebSocket address on each Matter check — there
is no Matter credential in GWatch's database or backups.

- [Setting up the sidecar](#setting-up-the-sidecar)
- [Adding devices (commissioning)](#adding-devices-commissioning)
- [The Matter check](#the-matter-check)
- [What the sidecar supports, and what it does not](#what-the-sidecar-supports-and-what-it-does-not)
- [Security](#security)
- [The WebSocket API GWatch uses](#the-websocket-api-gwatch-uses)
- [GWatch's own endpoints](#gwatchs-own-endpoints)

## Setting up the sidecar

python-matter-server needs **host networking**: Matter finds devices with mDNS
and talks to them over IPv6 link-local addresses, neither of which crosses a
Docker bridge network. Run it on a Linux machine (a Raspberry Pi, a NUC, the box
GWatch runs on) — the upstream project advises against NAS operating systems and
other special-purpose hosts, whose network stacks it has not been tuned for.

A Compose file with GWatch and the sidecar together:

```yaml
services:
  gwatch:
    image: ghcr.io/jxburros/gwatch:latest
    container_name: gwatch
    restart: unless-stopped
    ports:
      - "7230:7230"
    volumes:
      - gwatch-data:/data
    cap_add:
      - NET_RAW

  matter-server:
    image: ghcr.io/home-assistant-libs/python-matter-server:stable
    container_name: matter-server
    restart: unless-stopped
    # Required: mDNS and IPv6 link-local traffic do not cross a bridge network.
    network_mode: host
    security_opt:
      # Needed for Bluetooth commissioning over D-Bus.
      - apparmor:unconfined
    volumes:
      # The fabric — its certificates and every device's credentials — lives
      # here. Back this directory up; without it every device must be
      # commissioned again.
      - ./matter-data:/data
      # Only for adding brand-new devices over Bluetooth:
      # - /run/dbus:/run/dbus:ro
    # The default command line, plus Bluetooth if you mounted D-Bus above.
    # Pass the defaults as well whenever you change it:
    # command: --storage-path /data --paa-root-cert-dir /data/credentials --bluetooth-adapter 0

volumes:
  gwatch-data:
```

The GWatch service is the repository's own `docker-compose.yml`; see
[DOCKER.md](DOCKER.md) for its options. With the sidecar on host
networking, GWatch reaches it at `ws://<that machine's address>:5580/ws` — from a
GWatch container on a bridge network that is the host's LAN address, not
`localhost`.

**Which image.** The python-matter-server project announced that 8.1.2 is its
final release and that development has moved to
[matterjs-server](https://github.com/matter-js/matterjs-server), which is meant
to replace it for Home Assistant's Matter integration and so to serve the same
WebSocket API. Upstream's newer documentation publishes the image as
`ghcr.io/matter-js/python-matter-server:stable`. GWatch is written and tested
against the python-matter-server API described
[below](#the-websocket-api-gwatch-uses); any server that keeps that API — the
commands, the answer shapes and the `server_info` greeting it sends on connect
(schema version 11 at the time of writing), which is how GWatch recognises one —
should work, but only python-matter-server's has been checked against.

**Already running Home Assistant?** Its *Matter Server* add-on is the same
server. GWatch can share it: point the check at
`ws://homeassistant.local:5580/ws`. On Home Assistant OS the add-on's port has to
be exposed in the add-on's *Network* settings first; it is not reachable from
other machines by default. Devices commissioned there show up in GWatch's
**Load devices** list without being added again.

## Adding devices (commissioning)

A device has to be on the sidecar's fabric before GWatch can read it. In the
Matter check's form, open **Add a device to the Matter server** and paste a
pairing code:

- **A device already in another app** (Apple Home, Google Home, Alexa,
  SmartThings, Home Assistant): Matter's *multi-admin* lets it join a second
  controller and stay in the first. In that app, open the device and choose
  *Turn on pairing mode*, *Linked Matter apps & services* or *Share with other
  apps*; it shows a fresh 11-digit code, valid for a few minutes. Leave
  **Already on the network** ticked.
- **A brand-new device**: use the code printed on it or its box — the 11-digit
  manual code (dashes are fine) or the QR code's text starting `MT:`. A device
  already on Ethernet or Wi-Fi joins with **Already on the network** ticked. One
  that still needs to be told the Wi-Fi password, or that is fresh out of the box
  and only listens over Bluetooth, needs the sidecar to have a Bluetooth adapter
  and the D-Bus mount, and Wi-Fi or Thread credentials set on the sidecar
  beforehand (`set_wifi_credentials` / `set_thread_dataset`, below) — usually
  simpler from Home Assistant or the sidecar's own dashboard than from GWatch.

Commissioning takes from a few seconds to a couple of minutes (a bridge with
many devices takes longest, because the sidecar interviews every endpoint).
GWatch waits up to three minutes and then says the device may still appear.
Every attempt, successful or not, is recorded in the event log.

## The Matter check

Add a node from the **Matter device** template, with the host the sidecar runs
on as the node's host, or add a **Matter** check to any node. The template also
creates a *Matter server port 5580* TCP check: when the sidecar is down, that
check says so plainly, rather than every Matter reading failing at once with
nothing to tell them apart.

In the Matter check:

1. **Matter server** — the sidecar's WebSocket address. Leave it blank to use
   the node host on the default port and path, `ws://<host>:5580/ws`.
   `host:port` on its own is accepted too.
2. **Load devices** lists every node the sidecar has commissioned, by vendor,
   product and label. Pick one: its endpoints are listed with the device types
   they report and the standard readings they offer, with the value each reads
   right now. A **bridge** is one node whose bridged devices are endpoints, each
   with its own label, so one Matter check can read a whole bridge — up to 64
   readings.
3. Tick the readings to watch and **Add readings**. A reading from a bridged
   device is named after it (*Living room sensor temperature*); a battery
   reading starts with *Warn < 20 %* and *Crit < 10 %*.

### The standard readings

| Reading | Cluster | Attribute | What GWatch records |
| --- | --- | --- | --- |
| Temperature | TemperatureMeasurement `0x0402` | MeasuredValue `0x0000` | value / 100, in °C |
| Relative humidity | RelativeHumidityMeasurement `0x0405` | MeasuredValue `0x0000` | value / 100, in % |
| On / off | OnOff `0x0006` | OnOff `0x0000` | 1 on, 0 off |
| Contact | BooleanState `0x0045` | StateValue `0x0000` | 1 **open**, 0 shut (StateValue is *true* when the contact is made, i.e. shut) |
| Occupancy | OccupancySensing `0x0406` | Occupancy `0x0000`, bit 0 | 1 occupied, 0 clear |
| Illuminance | IlluminanceMeasurement `0x0400` | MeasuredValue `0x0000` | lux = 10^((value − 1) / 10000); 0 means too dark to measure |
| Battery | PowerSource `0x002F` | BatPercentRemaining `0x000C` | value / 2, in % (the attribute counts half-percents) |
| Other attribute | any | any | the number as read, multiplied by **Scale**; a boolean as 1/0; text is shown and never thresholded |

Each reading has a **name** (unique in the check — it is the metric's name in
charts and in `/api/history?metric=`), a **unit** and the same four thresholds
as an SNMP reading: crossing **Warn** marks the check degraded, crossing **Crit**
marks it down, and the comparisons are strict. Because states are 1 and 0, a
state becomes an alert with a threshold: **Crit > 0** on a contact is "down while
the door is open"; **Crit < 1** on a plug is "down while it is off".

### What a run does, and what each outcome means

Each run opens a WebSocket to the sidecar, waits for its `server_info` greeting,
sends one `read_attribute` for all the check's attribute paths and closes the
connection. The read is **live** — the sidecar asks the device rather than
answering from its cache — so the charted latency is GWatch → sidecar → device
and back, which is where a struggling Thread mesh shows up first.

| Outcome | Check status | Message |
| --- | --- | --- |
| Every reading answered, none past a threshold | up | *5 readings read from node 4 in 38 ms · Living room temperature 21.4 °C, …* |
| A reading past a Warn threshold | degraded | names the reading, its value and the threshold |
| A sensor reports *null* (no reading right now) | degraded | *… has no value right now* |
| A reading past a Crit threshold | down | names the reading, its value and the threshold |
| The attribute does not exist on that endpoint | down | *… is not available on node 4* — a wrong endpoint or a cluster the device lacks |
| The node is offline (`NodeNotReady`/`NodeNotResolving`) | down | *Matter node 4 is unavailable …* |
| No such node on this sidecar | down | *there is no node 4 on the Matter server at …* |
| The sidecar is not running or not reachable | down | *could not reach the Matter server at ws://… — is python-matter-server running there?* |
| Something answered on the port but never greeted | down | *the Matter server at … did not answer within 10s …* |

The check's retries, failure threshold, alerting, maintenance windows,
dependencies and triggers all work as for any other check. A reading that
produced a number is stored in `Result.metrics` and charted on the node page
under *Matter readings*.

## What the sidecar supports, and what it does not

GWatch can read whatever the sidecar can reach. In practice:

- **Wi-Fi and Ethernet devices** work as soon as they are commissioned, provided
  the sidecar's machine is on the same network segment (Matter uses mDNS and
  IPv6 link-local addressing; neither crosses a router or a VLAN boundary without
  an mDNS reflector and IPv6 routing).
- **Thread devices** need a **Thread border router** on the same network as the
  sidecar: an Apple TV 4K, a HomePod mini, a Nest Hub (2nd gen) or Nest Wifi Pro,
  an Echo 4th gen, or an OpenThread border router (Home Assistant's *OpenThread
  Border Router* add-on with a Thread radio). Neither GWatch nor the sidecar is
  one. The sidecar's host also needs to accept IPv6 router advertisements for
  the Thread prefix (on Linux, `accept_ra` and `accept_ra_rt_info_max_plen` set
  on the interface); see the sidecar's own
  [OS requirements](https://github.com/home-assistant-libs/python-matter-server/blob/main/docs/os_requirements.md).
- **Bridges** (Hue, Aqara, IKEA DIRIGERA, SwitchBot, a Home Assistant Matter
  bridge) appear as one node with an endpoint per bridged device. Their devices'
  own radio (Zigbee, a proprietary protocol) is the bridge's business; GWatch
  reads the Matter view of them like any other endpoint.
- **Bluetooth commissioning** of a brand-new device needs an adapter on the
  sidecar's host and the D-Bus mount. Adding an already-installed device through
  multi-admin needs neither.
- GWatch only **reads**. It sends no commands and writes no attributes, so it
  cannot switch a plug or unlock a door, whatever a check is configured to do.
  It never sends `start_listening` either, so it does not hold a subscription
  open on the sidecar between runs.

## Security

- **The sidecar has no authentication.** Anyone who can open
  `ws://host:5580/ws` can list, commission, remove and *control* every device on
  its fabric. Keep the port off the internet and off untrusted network
  segments: start it with `--listen-address` bound to the interface GWatch uses,
  or firewall port 5580 to GWatch's address. GWatch's own access controls stop at
  GWatch; they cannot protect a port someone can reach directly.
- **GWatch keeps no Matter secret.** The fabric credentials stay in the
  sidecar's storage directory (`/data` above), which is what needs backing up
  and protecting. A Matter check's configuration is just the address, the node
  id and the readings.
- **Probing and commissioning are administrator-only.** `POST /api/matter/nodes`
  and `POST /api/matter/commission` need the admin role and are refused to API
  keys of every scope, like the SNMP walk: they make GWatch connect to an address
  of the caller's choosing, and commissioning adds a device to a controller.
  Saving a Matter check follows the usual node-editing rules.
- `wss://` is supported for a sidecar behind a TLS proxy; the certificate is
  verified against the system's trust store.

## The WebSocket API GWatch uses

This is the part of python-matter-server's API GWatch depends on, from its
[WebSocket documentation](https://github.com/home-assistant-libs/python-matter-server/blob/main/docs/websockets_api.md)
and its source (`matter_server/common/models.py`,
`matter_server/server/client_handler.py`,
`matter_server/server/device_controller.py`). Every message is one JSON text
frame.

**On connect** the server sends its `server_info`, unprompted and without a
`message_id`:

```json
{ "fabric_id": 1, "compressed_fabric_id": 1234567890123456789,
  "schema_version": 11, "min_supported_schema_version": 9,
  "sdk_version": "2024.11.4", "wifi_credentials_set": false,
  "thread_credentials_set": true, "bluetooth_enabled": false }
```

**A command** is `{ "message_id": "<any string>", "command": "<name>", "args": { … } }`.
The answer carries the same `message_id`, and is either a success
`{ "message_id": "1", "result": … }` or an error
`{ "message_id": "1", "error_code": 3, "details": "Node 4 is not (yet) available." }`.
Commands are answered as they finish, not necessarily in order, and after
`start_listening` the server also pushes events (`{ "event": "attribute_updated",
"data": … }`) — GWatch sends one command at a time and skips anything whose
`message_id` is not the one it is waiting for.

| Command | Args | Result |
| --- | --- | --- |
| `get_nodes` | — (`only_available`: optional bool) | a list of nodes (below) |
| `get_node` | `node_id` | one node; `NodeNotExists` (5) if there is none |
| `read_attribute` | `node_id`, `attribute_path` — `"endpoint/cluster/attribute"` with decimal ids, or a list of them; `*` is a wildcard for cluster or attribute | `{ "1/1026/0": 2137, … }`, read live from the device; `NodeNotReady` (3) when the node is not available **or does not exist** |
| `commission_with_code` | `code` (`MT:…` QR text or a manual pairing code), `network_only` (bool, default false) | the new node; `NodeCommissionFailed` (1) on failure, and immediately when `network_only` is false and the server has no Bluetooth |
| `start_listening` | — | a dump of every node, then a stream of events on the same connection |
| `server_info` | — | the same object as the greeting |
| `set_wifi_credentials` | `ssid`, `credentials` | — (needed before commissioning a Wi-Fi device over Bluetooth) |
| `set_thread_dataset` | `dataset` (hex TLVs from the border router) | — (needed before commissioning a Thread device over Bluetooth) |
| `open_commissioning_window` | `node_id` | a pairing code to share the device with another controller |
| `ping_node`, `interview_node`, `remove_node`, `device_command`, `write_attribute`, … | | not used by GWatch |

**A node** (`MatterNodeData`):

```json
{ "node_id": 4, "date_commissioned": "2024-05-01T10:02:11", "last_interview": "2024-05-01T10:02:40",
  "interview_version": 6, "available": true, "is_bridge": true,
  "attributes": {
    "0/40/1": "Aqara", "0/40/3": "Hub M2", "0/40/5": "Hall bridge",
    "0/29/0": [{ "0": 22, "1": 1 }],
    "2/29/0": [{ "0": 770, "1": 1 }, { "0": 19, "1": 1 }],
    "2/57/5": "Living room sensor",
    "2/1026/0": 2137, "2/1029/0": 4720, "2/47/12": 162
  } }
```

`attributes` is the server's cache of every attribute, keyed by path. GWatch
reads names from Basic Information (`0x0028`: VendorName 1, ProductName 3,
NodeLabel 5) on endpoint 0 and, on a bridge, Bridged Device Basic Information
(`0x0039`) on each bridged endpoint; device types from the Descriptor cluster's
DeviceTypeList (`0x001D`/0, structs with their field ids as keys); and the
standard readings from the paths in the table above.

**Error codes** (`matter_server/common/errors.py`): 0 unknown, 1
NodeCommissionFailed, 2 NodeInterviewFailed, 3 NodeNotReady, 4 NodeNotResolving,
5 NodeNotExists, 6 VersionMismatch, 7 SDKStackError, 8 InvalidArguments, 9
InvalidCommand. Because `read_attribute` answers 3 for a node that was never
commissioned as well as for one that is offline, GWatch follows a 3 with
`get_node` to tell them apart. A server old enough to accept only a single path
answers a list with 8, and GWatch then reads the paths one at a time.

GWatch's client (`internal/matter`) is a small RFC 6455 implementation of its
own — text and binary messages, fragmentation, ping/pong and the closing
handshake, no extensions — rather than a new dependency; messages are capped at
32 MiB. `internal/matter/mattertest` is a fake sidecar that the tests run the
client, the check and the API against.

## GWatch's own endpoints

Both need the admin role and are refused to API keys of every scope.

- `POST /api/matter/nodes` body `{ "server": "ws://…", "host": "…" }` — `server`,
  or `host` on port 5580 when `server` is blank — runs `get_nodes` and answers
  `{ "server", "serverInfo", "nodes": [NodeSummary], "standards": [...] }`, where a
  `NodeSummary` is `{ nodeId, available, isBridge, vendor, product, label,
  endpoints: [{ id, label, deviceTypes, clusters, readings: [{ kind, name, unit,
  cluster, attribute, current }] }] }`. Bounded at 20 seconds; 502 when the
  sidecar cannot be reached.
- `POST /api/matter/commission` body `{ "server", "host", "code", "networkOnly" }`
  — `networkOnly` defaults to `true` — validates the code (an 11- or 21-digit
  manual code, separators stripped, or `MT:` QR text), runs
  `commission_with_code` and answers `{ "server", "node": NodeSummary }`. Bounded
  at three minutes. Recorded in the event log either way.

The check's configuration fields are in [API.md](API.md#node-and-check-objects).

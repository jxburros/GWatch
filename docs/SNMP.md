# SNMP checks

A ping tells you a switch answers. SNMP tells you which of its ports is
saturated, which one is throwing errors, and how long it has been up since the
power cut you did not notice. Routers, switches, access points, managed PDUs,
printers and most UPSes speak it, and they will tell you what they know if you
ask — no agent to install, nothing to run on the device.

GWatch's **SNMP** check reads a list of OIDs from one device on a schedule.
Each OID becomes a metric of its own, with its own unit, its own thresholds and
its own chart.

## Enabling SNMP on the device

SNMP is off, or read-write, or set to `public`, depending on who made the box.
Three rules, whatever the make:

1. **Enable SNMP v2c read-only**, or v3 if the device offers it and you can be
   bothered — see below for when that is worth it.
2. **Change the community string** from `public` to something else, and make it
   read-only. The community is the whole of v2c's security.
3. **Restrict it to your monitoring machine's address** if the device has that
   setting, which most managed switches and all decent routers do.

Where the setting lives:

| Device | Where to look |
| --- | --- |
| MikroTik (RouterOS) | *IP › SNMP* — enable, then *Communities* for the read-only community and its allowed address |
| Ubiquiti UniFi | *Settings › System › Advanced › SNMP* — v2c or v3, community/user set there |
| OPNsense / pfSense | *Services › SNMP* — enable, set the community, bind to the LAN interface |
| TP-Link / Netgear / Zyxel managed switches | usually *System › SNMP* or *Maintenance › SNMP*: enable the agent, then add a read-only community |
| Synology / QNAP | *Control Panel › Terminal & SNMP* — tick SNMP, choose v2c or v3 |
| Most printers | the embedded web page, under *Network › SNMP* |

Then, in GWatch: **Nodes › the device › Edit › Add check › SNMP**. Fill in the
community, and press **Test this check** before saving. If the test says
*no response*, read the last section of this page.

### v2c or v3

Use **v2c** on a network you control. Its community string travels in the clear,
which matters exactly as much as anything else on your LAN travelling in the
clear.

Use **v3** when the traffic crosses something you do not control, or when the
device is shared. v3 needs a user name, an authentication protocol and password
(prefer `SHA256` or better), and optionally an encryption protocol and password
(`AES` upwards). Encryption requires authentication — GWatch refuses the
combination that pretends otherwise.

Either way, GWatch seals the community string and the v3 passwords with the
machine-local key file before they reach the database, never shows them again
(the editor displays `********` and an empty box), and never writes them into a
configuration export. Leaving the box blank when you save keeps what is stored.

## Choosing OIDs

An OID is a dotted number identifying one value: `1.3.6.1.2.1.1.3.0` is "how
long has this device been up". GWatch reads numeric OIDs only — it ships no MIB
files, so `sysUpTime.0` means nothing to it.

The editor's **Presets** dropdown covers what almost everyone wants, all of it
from the standard MIBs that every SNMP device implements:

| Preset | OID | Kind |
| --- | --- | --- |
| Uptime — `sysUpTime` | `1.3.6.1.2.1.1.3.0` | gauge, scale `0.01` → seconds |
| Description — `sysDescr` | `1.3.6.1.2.1.1.1.0` | text |
| Device name — `sysName` | `1.3.6.1.2.1.1.5.0` | text |
| Link up/down — `ifOperStatus` | `1.3.6.1.2.1.2.2.1.8.N` | gauge, `1` = up |
| Traffic in / out — `ifInOctets` / `ifOutOctets` | `1.3.6.1.2.1.2.2.1.10.N` / `.16.N` | counter, scale `8` → bit/s |
| Traffic in / out, 64-bit — `ifHCInOctets` / `ifHCOutOctets` | `1.3.6.1.2.1.31.1.1.1.6.N` / `.10.N` | counter, scale `8` → bit/s |
| Errors in / out — `ifInErrors` / `ifOutErrors` | `1.3.6.1.2.1.2.2.1.14.N` / `.20.N` | counter |
| Processor load — `hrProcessorLoad` | `1.3.6.1.2.1.25.3.3.1.2.N` | gauge, % |

Vendor MIBs (temperature on a Cisco, PoE draw on a UniFi switch) are not
offered, because a preset that only works on one make is worse than none. Add
those as rows by hand from the vendor's MIB documentation.

### Gauges and counters

A **gauge** already means something: a percentage, a temperature, a link state.
GWatch compares the number it read.

A **counter** only ever climbs — an interface's octet counter reaches a few
billion and wraps back to zero — so the number itself is meaningless and the
*rate* is what you want. GWatch subtracts the previous reading from this one,
divides by the seconds between them, and charts that. Wrapping is unwound at
the counter's own width, so a 32-bit counter going from 4,294,967,196 to 100 is
200 octets, not a leap backwards.

Two consequences worth knowing:

- **The first run after GWatch starts has no rate** for any counter, so no
  reading and no verdict for those rows. The second run onwards is normal.
- **A long interval smooths a counter out.** A check running every five minutes
  reports the average rate over five minutes, which will never show a 20-second
  spike. Use 60 seconds for traffic you actually want to watch.

### `N`: which port is which?

`N` in the interface OIDs is SNMP's own index for a port, and it very often is
not the number printed on the case. The editor asks for it when you pick one of
those presets.

To find out which is which, press **Walk this device** in the check's editor.
GWatch reads the device's `1.3.6.1.2.1` subtree and shows what came back: the
OID, a suggested name, the SNMP type and the current value. Tick the rows you
want and they become readings, already set as gauges or counters according to
their type — the scale, the unit and the thresholds are then yours to set. The
port names (`ether1-wan`, `sfp-uplink`) are in the walk too, under `ifDescr`
and `ifAlias`, which is how you tell which index is which.

The walk stops at 500 rows and 15 seconds, and it is administrator-only: it
points GWatch at an address with a credential and reports what answered, so it
is not something an API key can do.

The same thing from a terminal, if you would rather:

```
# On Linux or macOS, from the net-snmp package:
snmpwalk -v2c -c <community> 192.168.1.3 1.3.6.1.2.1.2.2.1.2     # ifDescr
snmpwalk -v2c -c <community> 192.168.1.3 1.3.6.1.2.1.31.1.1.1.18 # ifAlias
```

The last number of each line is the index; the value beside it is the port's
name. On a router the WAN port is usually `1` or `2`, but "usually" is why you
walk it.

## The recipe: interface traffic in bits per second

The one most people come for. For port `N` on a device you have already added:

1. **Add check › SNMP**, set the community.
2. **Presets › Traffic in, 64-bit — `ifHCInOctets`**, answer `N`. Repeat for
   **Traffic out**.
3. Leave the kind as **counter**, the scale as **8** and the unit as **bit/s** —
   the preset sets all three. The scale is what turns octets into bits; drop it
   to 1 if you would rather read bytes per second.
4. Set the interval to **60 seconds**.
5. Optionally set **Warn >** to about 80% of the link's capacity in bit/s
   (`800000000` for a saturated gigabit port) so a link that is full says so.

Prefer the 64-bit `ifHC…` counters wherever the device offers them: a 32-bit
octet counter wraps every 34 seconds on a saturated gigabit link, and a check
that runs every 60 seconds cannot tell one wrap from two.

Two more that are worth having on any switch:

- **Errors in** on the uplink, with **Warn >** `0` — an error rate above zero
  means a cable, an SFP or a duplex mismatch, and it is the check that finds
  the fault everyone else blames on the internet.
- **Link up/down** on the uplink. The preset sets both `Crit >` and `Crit <` to
  `1`, which together mean "must be exactly 1": anything else — 2 (down), 7
  (lower layer down) — marks the check down.

## What you get back

The check's own latency is the round trip of the SNMP exchange, charted like
any other check's. Each OID is charted separately on the node's page, and can be
exported as CSV.

Per-OID history is read from **raw results only**. GWatch's rollup tables have
columns for latency, jitter and packet loss and nowhere to put a metric a check
invented, so the per-OID charts reach back as far as raw history is kept — 30
days by default, whatever *Settings › Retention* says otherwise. The check's
availability and latency roll up normally and go back as far as everything else.

A reading that is text rather than a number — `sysDescr`, a name, a MAC address
— is shown as the device sent it and never compared against a threshold.

## When it does not answer

**SNMP v2c answers a wrong community with silence.** There is no "access
denied": the device simply does not reply, exactly as it would if it were
switched off. So a timeout means one of:

- SNMP is not enabled on the device, or not on this interface;
- the community string is wrong, or is set to allow only some other address;
- UDP 161 is blocked between here and there;
- the device really is unreachable — which the node's ping check will confirm in
  a way that this one cannot.

Check them in that order. From the GWatch machine, `snmpget -v2c -c <community>
<host> 1.3.6.1.2.1.1.1.0` distinguishes the first three from the fourth in one
command.

An OID reported as **not available on this device** is a different fault: the
device answered, but it does not implement that OID — usually an interface index
that does not exist. Walk the device and correct the index.

SNMP v3 does report authentication failures, and the message says so.

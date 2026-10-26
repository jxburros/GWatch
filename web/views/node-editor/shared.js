// The node editor: what more than one kind of check's fields use.
//
// Part of node-editor.js, split out for #118: the code is the
// view's own, moved here unchanged inside create(shell), which hands it the
// view's shared state and helpers.

// The hardware thresholds a new check starts with, one entry per metric
// family. They mirror model.SystemDefaults on the server, which is what a
// check saved without thresholds is given; keeping them here too means the
// editor shows the numbers the check will use rather than a row of blanks.
export const SYSTEM_DEFAULTS = {
  metricThresholds: [
    { metric: 'cpu', warn: 90 }, { metric: 'memory', warn: 90, crit: 97 }, { metric: 'swap', warn: 50 },
    { metric: 'disk', warn: 85, crit: 95 }, { metric: 'inodes', warn: 85, crit: 95 }, { metric: 'load', warn: 2 },
    { metric: 'pool', warn: 1, crit: 2 },
    { metric: 'service', crit: 0, below: true }, { metric: 'process', crit: 0, below: true },
    { metric: 'docker', warn: 1, crit: 0, below: true }, { metric: 'docker-restarts', warn: 3 },
    { metric: 'docker-memory', warn: 90, crit: 97 }, { metric: 'vm', warn: 1, crit: 0, below: true },
  ],
};

// The metric families a hardware check reports, in the order the editor shows
// them. `unit` is what a threshold is measured in; `instances` says the family
// is keyed per disk, interface or device, so an override can name one.
// Mirrors model.MetricFamilies and model.SystemMetricUnit on the server.
export const METRIC_FAMILIES = [
  { key: 'cpu', label: 'Processor', unit: '%' },
  { key: 'memory', label: 'Memory', unit: '%' },
  { key: 'swap', label: 'Swap', unit: '%', note: 'Heavy swapping is a sign worth a warning; leave the critical level blank unless swap running out is itself an outage here.' },
  { key: 'load', label: 'Load per core', unit: '', step: 0.1, note: 'Processor demand divided by the number of cores, so it means the same thing on a 2-core box and a 64-core one. On macOS, where processor utilisation is not readable, this is what the check watches instead.' },
  { key: 'disk', label: 'Disk space', unit: '%', instances: 'a mount point, e.g. /srv or C:' },
  { key: 'inodes', label: 'Inodes', unit: '%', instances: 'a mount point, e.g. /srv', note: 'A filesystem can run out of inodes with space to spare, and the failure looks identical to a full disk.' },
  { key: 'net', label: 'Network throughput', unit: 'B/s', step: 1, instances: 'an interface, e.g. eth0 (or eth0.rx for one direction)', note: 'Bytes per second, received or sent, per interface. Blank means throughput is charted but never alerted on.' },
  { key: 'diskio', label: 'Disk I/O', unit: 'B/s', step: 1, instances: 'a device, e.g. sda (or sda.read, sda.write; sda.busy is a percentage)', note: 'Bytes per second read or written, per device. Blank means it is charted but never alerted on.' },
  { key: 'temp', label: 'Temperature', unit: '°C', instances: 'a sensor, e.g. coretemp/Package id 0 or nvme/Composite', note: 'Linux only: every hardware sensor and thermal zone, each its own reading. Blank means charted but never alerted on — what is hot depends on the part, so set it for the sensors you know.' },
  { key: 'psi', label: 'Pressure stall', unit: '%', instances: 'a resource, e.g. memory (or memory.avg10, memory.avg60)', note: 'Linux only: the share of time work was stalled waiting for the processor, memory or disks, averaged over 10 and 60 seconds. Sustained memory or io pressure is a machine that is short of it.' },
  { key: 'pool', label: 'ZFS pool health', unit: '', instances: 'a pool, e.g. tank', note: 'Linux with ZFS only: 0 is healthy, 1 is degraded (a disk has dropped out), 2 is faulted or unavailable.' },
  // Services and processes are "less is worse": their levels are crossed at
  // or below the number, so critical 0 means "stopped" or "none running".
  { key: 'service', label: 'Services', unit: '', below: true, instances: 'a service, e.g. nginx', note: 'Only the services this machine is told to watch (its hardware settings, or the agent’s own panel). 1 is running, 0 is stopped or not installed; levels are crossed at or below the number, so critical 0 is “critical when stopped”.' },
  { key: 'process', label: 'Processes', unit: '', below: true, instances: 'a program, e.g. plex', note: 'Only the programs this machine is told to watch. The reading is how many copies are running; levels are crossed at or below the number, so critical 0 is “critical when none is running”.' },
  // Containers and virtual machines: a state each (also "less is worse"),
  // and the figures the Docker daemon or the hypervisor has for it.
  { key: 'docker', label: 'Containers', unit: '', below: true, instances: 'a container, e.g. web', note: 'Only the Docker or Podman containers this machine is told to watch. 2 is running, 1 is unhealthy, paused or restarting, 0 is stopped or gone; levels are crossed at or below the number, so warning 1 and critical 0 is “warn when not healthy, critical when stopped”.' },
  { key: 'docker-restarts', label: 'Container restarts', unit: '', step: 1, instances: 'a container, e.g. web', note: 'How many times a watched container’s restart policy restarted it in the last hour. A container stuck in a crash loop can look running whenever it is sampled; this is what catches it.' },
  { key: 'docker-cpu', label: 'Container processor', unit: '%', instances: 'a container, e.g. web', note: 'A watched container’s processor use, as a share of its own limit (docker run --cpus), or of the whole machine when it has none.' },
  { key: 'docker-memory', label: 'Container memory', unit: '%', instances: 'a container, e.g. web', note: 'A watched container’s memory use, as a share of its own limit (docker run --memory), or of the whole machine when it has none. At its limit the kernel kills it.' },
  { key: 'vm', label: 'Virtual machines', unit: '', below: true, instances: 'a guest, e.g. dc01', note: 'Only the guests this machine’s hypervisor (Hyper-V) is told to watch. 2 is running, 1 is paused, saved or changing state, 0 is off or gone. For a guest that is off on purpose, add an entry for it below with both levels blank.' },
  { key: 'vm-cpu', label: 'Virtual machine processor', unit: '%', instances: 'a guest, e.g. dc01', note: 'How busy a watched guest’s virtual processors are, across all of them.' },
  { key: 'vm-memory', label: 'Virtual machine memory', unit: '%', instances: 'a guest, e.g. dc01', note: 'How much of the memory the host assigned a watched guest it is using, when the guest’s integration services report it.' },
];

// legacyMetricThresholds converts the flat per-family fields a hardware check
// had before the list existed, with the same rules as the server's
// EffectiveMetricThresholds: 0 is off, and inodes followed the disk pair.
export function legacyMetricThresholds(cfg) {
  const level = (v) => (Number(v) > 0 ? Number(v) : undefined);
  const out = [];
  const add = (metric, warn, crit) => { const t = { metric, warn: level(warn), crit: level(crit) }; if (t.warn != null || t.crit != null) out.push(t); };
  add('cpu', cfg.cpuWarnPct, cfg.cpuCritPct);
  add('memory', cfg.memWarnPct, cfg.memCritPct);
  add('swap', cfg.swapWarnPct, 0);
  add('load', cfg.loadWarnPerCore, cfg.loadCritPerCore);
  add('disk', cfg.diskWarnPct, cfg.diskCritPct);
  if (Number(cfg.diskCritPct) > 0) add('inodes', cfg.diskWarnPct, cfg.diskCritPct);
  return out;
}

/*
 * Well-known OIDs from the standard MIBs, offered as presets so that setting
 * up a router does not start with a MIB browser. Everything here is from
 * SNMPv2-MIB, IF-MIB or HOST-RESOURCES-MIB, which every device that speaks
 * SNMP at all implements; vendor MIBs are deliberately absent, because a
 * preset that only works on one make would be worse than no preset.
 *
 * "{N}" in an OID is an interface or processor index, which the editor asks
 * for when the preset is chosen: SNMP numbers the ports and there is no way
 * to know from here which number is the one the reader means. Walking the
 * device (docs/SNMP.md) is how that number is found.
 */
export const SNMP_PRESETS = [
  { group: 'System', items: [
    { label: 'Uptime — sysUpTime', oid: '1.3.6.1.2.1.1.3.0', name: 'Uptime', kind: 'gauge', scale: 0.01, unit: 's' },
    { label: 'Description — sysDescr', oid: '1.3.6.1.2.1.1.1.0', name: 'Description', kind: 'gauge' },
    { label: 'Device name — sysName', oid: '1.3.6.1.2.1.1.5.0', name: 'Device name', kind: 'gauge' },
  ] },
  { group: 'Interface (asks for the port number)', items: [
    { label: 'Link up/down — ifOperStatus', oid: '1.3.6.1.2.1.2.2.1.8.{N}', name: 'Port {N} link', kind: 'gauge', critBelow: 1, critAbove: 1 },
    { label: 'Traffic in — ifInOctets (bit/s)', oid: '1.3.6.1.2.1.2.2.1.10.{N}', name: 'Port {N} in', kind: 'counter', scale: 8, unit: 'bit/s' },
    { label: 'Traffic out — ifOutOctets (bit/s)', oid: '1.3.6.1.2.1.2.2.1.16.{N}', name: 'Port {N} out', kind: 'counter', scale: 8, unit: 'bit/s' },
    { label: 'Traffic in, 64-bit — ifHCInOctets (bit/s)', oid: '1.3.6.1.2.1.31.1.1.1.6.{N}', name: 'Port {N} in', kind: 'counter', scale: 8, unit: 'bit/s' },
    { label: 'Traffic out, 64-bit — ifHCOutOctets (bit/s)', oid: '1.3.6.1.2.1.31.1.1.1.10.{N}', name: 'Port {N} out', kind: 'counter', scale: 8, unit: 'bit/s' },
    { label: 'Errors in — ifInErrors', oid: '1.3.6.1.2.1.2.2.1.14.{N}', name: 'Port {N} errors in', kind: 'counter', unit: '/s', warnAbove: 0 },
    { label: 'Errors out — ifOutErrors', oid: '1.3.6.1.2.1.2.2.1.20.{N}', name: 'Port {N} errors out', kind: 'counter', unit: '/s', warnAbove: 0 },
  ] },
  { group: 'Processor (asks for the processor number)', items: [
    { label: 'Processor load — hrProcessorLoad', oid: '1.3.6.1.2.1.25.3.3.1.2.{N}', name: 'Processor {N}', kind: 'gauge', unit: '%', warnAbove: 85, critAbove: 95 },
  ] },
];

/** A blank OID row, or one filled in from a preset with its index applied. */
export function oidRow(preset, index) {
  const row = { oid: '', name: '', kind: 'gauge', scale: 1, unit: '' };
  if (!preset) return row;
  const n = String(index ?? 1);
  row.oid = preset.oid.replace('{N}', n);
  row.name = preset.name.replace('{N}', n);
  row.kind = preset.kind || 'gauge';
  row.scale = preset.scale ?? 1;
  row.unit = preset.unit || '';
  for (const k of SNMP_THRESHOLD_KEYS) if (preset[k] != null) row[k] = preset[k];
  return row;
}

export const SNMP_THRESHOLD_KEYS = ['warnAbove', 'critAbove', 'warnBelow', 'critBelow'];

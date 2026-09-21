// Node editor: create / edit a node and its checks, with inline check testing.

import { api } from '../api.js';
import { h, icon, clear, replace, field, textInput, numberInput, textarea, selectInput, checkbox, toggle, chipInput, toast, confirmDialog, promptDialog, openModal, emptyState, skeleton, CHECK_TYPES, checkTypeLabel, uid, busy } from '../components.js';
import { nodeGroups } from '../fmt.js';
import { resultInspector } from './inspector.js';
// The interval choices, the three states of an alert override, the importance
// levels and the ping methods are shared with the bulk editor, so both screens
// offer the same words and the same values. See web/views/node-fields.js.
import { INTERVALS, intervalOptions, TRI, IMPORTANCE_OPTIONS, PING_METHOD_OPTIONS, ALERT_OVERRIDE_FIELDS } from './node-fields.js';

function defaultCheck(type, settings) {
  const g = settings?.general || {};
  const base = { _key: uid('c'), type, name: checkTypeLabel(type), enabled: true, intervalSeconds: g.defaultIntervalSeconds || 60, timeoutSeconds: g.defaultTimeoutSeconds || 10, retries: 1, failureThreshold: 0, config: {}, alerts: null };
  switch (type) {
    case 'ping': base.config = { pingCount: 4 }; break;
    case 'http': base.config = { method: 'GET', expectedStatus: '200-399', followRedirects: true, certCheck: true, certWarnDays: settings?.alerts?.certWarnDays || 14 }; base.name = 'HTTP/S'; break;
    case 'cert': base.config = { port: 443, certWarnDays: settings?.alerts?.certWarnDays || 14 }; base.name = 'Certificate'; break;
    case 'tcp': base.config = { port: 443 }; base.name = 'TCP port'; break;
    case 'dns': base.config = { recordType: 'A' }; break;
    case 'keyword': base.config = { method: 'GET', keyword: '', followRedirects: true }; break;
    case 'json': base.config = { method: 'GET', jsonPath: '', jsonExpected: '', jsonRecord: false, jsonMetric: '', jsonUnit: '' }; break;
    case 'custom': base.config = { command: '', workDir: '', env: {} }; base.name = 'Custom script'; break;
    case 'system': base.config = { metricThresholds: SYSTEM_DEFAULTS.metricThresholds.map((t) => ({ ...t })), hostSource: 'local' }; base.name = 'Hardware health'; break;
    // A new SNMP check starts where every device is the same: v2c on 161 with
    // the community every one of them ships with, reading the one OID every
    // one of them answers. It is a working check before anything is typed.
    case 'snmp': base.config = { snmpVersion: '2c', snmpPort: 161, snmpCommunity: 'public', snmpOids: [oidRow(SNMP_PRESETS[0].items[0])] }; base.name = 'SNMP'; break;
  }
  return base;
}

function cleanCheck(c) {
  const out = { ...c };
  delete out._key; delete out._test;
  out.intervalSeconds = Number(out.intervalSeconds) || 60;
  out.timeoutSeconds = Number(out.timeoutSeconds) || 10;
  out.retries = Number(out.retries) || 0;
  out.failureThreshold = Number(out.failureThreshold) || 0;
  const cfg = { ...(out.config || {}) };
  for (const k of Object.keys(cfg)) {
    const v = cfg[k];
    if (v === '' || v == null || (Array.isArray(v) && !v.length) || (typeof v === 'object' && !Array.isArray(v) && !Object.keys(v).length)) delete cfg[k];
  }
  for (const k of ['pingCount', 'certWarnDays', 'port']) if (cfg[k] != null) cfg[k] = Number(cfg[k]);
  for (const k of ['latencyWarnMs', 'packetLossWarnPct']) if (cfg[k] != null) cfg[k] = Number(cfg[k]);
  if (out.type === 'system') {
    // The editor writes the per-metric list; the flat fields it replaced are
    // dropped so the server never sees the two disagree.
    cfg.metricThresholds = cleanMetricThresholds(cfg.metricThresholds);
    for (const k of LEGACY_THRESHOLD_KEYS) delete cfg[k];
  }
  if (cfg.snmpPort != null) cfg.snmpPort = Number(cfg.snmpPort);
  if (cfg.snmpOids) cfg.snmpOids = cfg.snmpOids.map(cleanOidRow).filter((o) => o.oid || o.name);
  // A json check that is not recording keeps none of the recording fields,
  // and a threshold that is unset has to be absent rather than zero, for the
  // same reason as an OID's (see cleanOidRow).
  if (!cfg.jsonRecord) for (const k of ['jsonRecord', 'jsonMetric', 'jsonUnit', ...JSON_THRESHOLD_KEYS]) delete cfg[k];
  for (const k of JSON_THRESHOLD_KEYS) {
    if (cfg[k] === '' || cfg[k] == null || isNaN(Number(cfg[k]))) delete cfg[k];
    else cfg[k] = Number(cfg[k]);
  }
  out.config = cfg;
  if (out.alerts) {
    const a = {};
    for (const k of ['enabled', 'notifyRecovery', 'notifyWarnings']) if (out.alerts[k] != null) a[k] = out.alerts[k];
    if (out.alerts.cooldownMinutes != null && out.alerts.cooldownMinutes !== '') a.cooldownMinutes = Number(out.alerts.cooldownMinutes);
    if (out.alerts.recipients?.length) a.recipients = out.alerts.recipients;
    out.alerts = Object.keys(a).length ? a : null;
  }
  return out;
}

// The hardware thresholds a new check starts with, one entry per metric
// family. They mirror model.SystemDefaults on the server, which is what a
// check saved without thresholds is given; keeping them here too means the
// editor shows the numbers the check will use rather than a row of blanks.
const SYSTEM_DEFAULTS = {
  metricThresholds: [
    { metric: 'cpu', warn: 90 }, { metric: 'memory', warn: 90, crit: 97 }, { metric: 'swap', warn: 50 },
    { metric: 'disk', warn: 85, crit: 95 }, { metric: 'inodes', warn: 85, crit: 95 }, { metric: 'load', warn: 2 },
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

// metricThresholdErrors mirrors model.ValidateMetricThresholds: the same
// rules, so the editor refuses what the server would.
export function metricThresholdErrors(list) {
  const seen = new Set();
  const families = new Map(METRIC_FAMILIES.map((f) => [f.key, f]));
  for (const t of list || []) {
    const key = (t.metric || '').trim();
    if (!key) return 'A threshold must name the metric it applies to.';
    const [family, instance] = key.includes(':') ? [key.slice(0, key.indexOf(':')), key.slice(key.indexOf(':') + 1)] : [key, ''];
    const f = families.get(family);
    if (!f) return `"${key}" is not a hardware metric — use one of ${METRIC_FAMILIES.map((x) => x.key).join(', ')}.`;
    if (instance && !f.instances) return `${f.label} is one reading for the whole machine, so "${key}" cannot name an instance.`;
    if (seen.has(key)) return `Two thresholds both apply to "${key}".`;
    seen.add(key);
    const pct = f.unit === '%' || (family === 'diskio' && instance.endsWith('.busy'));
    const label = instance ? `${f.label} ${instance}` : f.label;
    for (const [name, v] of [['warning', t.warn], ['critical', t.crit]]) {
      if (v == null || v === '') continue;
      if (isNaN(Number(v))) return `The ${label} ${name} threshold must be a number.`;
      if (pct && (v < 0 || v > 100)) return `The ${label} ${name} threshold must be between 0 and 100.`;
      if (v < 0) return `The ${label} ${name} threshold cannot be negative.`;
    }
    if (t.warn != null && t.warn !== '' && t.crit != null && t.crit !== '' && Number(t.warn) > 0 && Number(t.crit) > 0) {
      if (!t.below && Number(t.crit) < Number(t.warn)) return `The ${label} critical threshold must be at or above its warning threshold.`;
      if (t.below && Number(t.crit) > Number(t.warn)) return `The ${label} critical threshold must be at or below its warning threshold when less is worse.`;
    }
  }
  return null;
}

// cleanMetricThreshold drops blank levels rather than sending zeros, so an
// empty box means "off" on the server too. An entry with no level and no
// instance is not worth keeping at all.
function cleanMetricThresholds(list) {
  const out = [];
  for (const t of list || []) {
    const metric = (t.metric || '').trim();
    if (!metric) continue;
    const row = { metric };
    for (const k of ['warn', 'crit']) if (t[k] !== '' && t[k] != null && !isNaN(Number(t[k]))) row[k] = Number(t[k]);
    if (t.below) row.below = true;
    // An instance entry with no level is still meaningful: it switches the
    // family's thresholds off for that one disk or interface.
    if (row.warn == null && row.crit == null && !metric.includes(':')) continue;
    out.push(row);
  }
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
const SNMP_PRESETS = [
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

const SNMP_AUTH_PROTOCOLS = ['', 'MD5', 'SHA', 'SHA224', 'SHA256', 'SHA384', 'SHA512'];
const SNMP_PRIV_PROTOCOLS = ['', 'DES', 'AES', 'AES192', 'AES256', 'AES192C', 'AES256C'];

/** A blank OID row, or one filled in from a preset with its index applied. */
function oidRow(preset, index) {
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

const SNMP_THRESHOLD_KEYS = ['warnAbove', 'critAbove', 'warnBelow', 'critBelow'];

// The same four thresholds, as a json check names them on its config, each
// with the wording the SNMP table uses for its column heading.
const JSON_THRESHOLD_FIELDS = [['jsonWarnAbove', 'Warn >'], ['jsonCritAbove', 'Crit >'], ['jsonWarnBelow', 'Warn <'], ['jsonCritBelow', 'Crit <']];
const JSON_THRESHOLD_KEYS = JSON_THRESHOLD_FIELDS.map(([k]) => k);

// jsonRecordErrors mirrors internal/checks.validateJSONRecord: a recording
// json check needs a path, a short name and thresholds that escalate in the
// right order. Like snmpErrors, this is a courtesy; the server is the rule.
function jsonRecordErrors(cfg) {
  const e = {};
  if (!cfg.jsonRecord) return e;
  if (!(cfg.jsonPath || '').trim()) e.jsonPath = 'Recording a value needs a JSON path to read it from.';
  if ((cfg.jsonMetric || '').trim().length > 64) e.jsonMetric = 'Keep the metric name to 64 characters or fewer.';
  const num = (k) => (cfg[k] === '' || cfg[k] == null || isNaN(Number(cfg[k])) ? null : Number(cfg[k]));
  const [wa, ca, wb, cb] = JSON_THRESHOLD_KEYS.map(num);
  if (wa != null && ca != null && wa >= ca) e.jsonCritAbove = 'The critical "above" threshold must be above the warning one.';
  if (wb != null && cb != null && wb <= cb) e.jsonCritBelow = 'The critical "below" threshold must be below the warning one.';
  return e;
}

// snmpErrors mirrors internal/checks.validateSNMPCheck so that the editor can
// say what is wrong beside the field rather than after a round trip. The
// server refuses the same things again: this is a courtesy, not the rule.
function snmpErrors(cfg) {
  const e = {};
  const port = Number(cfg.snmpPort);
  if (cfg.snmpPort != null && cfg.snmpPort !== '' && (!port || port < 1 || port > 65535)) e.snmpPort = 'Port must be 1–65535.';
  if ((cfg.snmpVersion || '2c') === '3') {
    if (!(cfg.snmpUser || '').trim()) e.snmpUser = 'SNMP v3 needs a user name.';
    if (cfg.snmpAuthProto && !cfg.snmpAuthPass) e.snmpAuthPass = 'Enter the authentication password.';
    if (cfg.snmpPrivProto && !cfg.snmpAuthProto) e.snmpPrivProto = 'Encryption needs authentication as well.';
    if (cfg.snmpPrivProto && !cfg.snmpPrivPass) e.snmpPrivPass = 'Enter the encryption password.';
  }
  const rows = cfg.snmpOids || [];
  if (!rows.length) { e.snmpOids = 'Add at least one reading.'; return e; }
  if (rows.length > 64) { e.snmpOids = 'An SNMP check can read at most 64 OIDs. Split the rest into a second check.'; return e; }
  const names = new Set();
  for (const o of rows) {
    const oid = (o.oid || '').trim().replace(/^\./, '');
    if (!/^\d+(\.\d+)+$/.test(oid)) { e.snmpOids = `"${o.oid || '(blank)'}" is not a numeric OID — it should look like 1.3.6.1.2.1.1.3.0.`; return e; }
    const name = (o.name || '').trim().toLowerCase();
    if (!name) { e.snmpOids = `The reading ${o.oid} needs a name.`; return e; }
    if (names.has(name)) { e.snmpOids = `Two readings are both called "${o.name}" — names identify the metric in charts, so they must differ.`; return e; }
    names.add(name);
    const num = (k) => (o[k] === '' || o[k] == null || isNaN(Number(o[k])) ? null : Number(o[k]));
    const [wa, ca, wb, cb] = ['warnAbove', 'critAbove', 'warnBelow', 'critBelow'].map(num);
    if (wa != null && ca != null && wa >= ca) { e.snmpOids = `${o.name}: the critical "above" threshold must be above the warning one.`; return e; }
    if (wb != null && cb != null && wb <= cb) { e.snmpOids = `${o.name}: the critical "below" threshold must be below the warning one.`; return e; }
  }
  return e;
}

// An unset threshold has to be absent rather than zero: zero is a threshold a
// reader might genuinely mean (an error counter that should never move).
function cleanOidRow(o) {
  const row = { oid: (o.oid || '').trim(), name: (o.name || '').trim(), kind: o.kind === 'counter' ? 'counter' : 'gauge' };
  const scale = Number(o.scale);
  row.scale = isFinite(scale) && scale > 0 ? scale : 1;
  if ((o.unit || '').trim()) row.unit = o.unit.trim();
  for (const k of SNMP_THRESHOLD_KEYS) {
    if (o[k] === '' || o[k] == null || isNaN(Number(o[k]))) continue;
    row[k] = Number(o[k]);
  }
  return row;
}

// The flat threshold fields a hardware check had before the per-metric list.
// A check that still carries them is converted when it is loaded (see
// legacyMetricThresholds) and saved without them.
const LEGACY_THRESHOLD_KEYS = ['cpuWarnPct', 'cpuCritPct', 'memWarnPct', 'memCritPct', 'swapWarnPct', 'diskWarnPct', 'diskCritPct', 'loadWarnPerCore', 'loadCritPerCore'];

// adoptMetricThresholds gives a draft's hardware checks the list form: a check
// loaded with only the flat fields is converted with the server's rules, so
// the editor shows the thresholds the check has actually been using.
function adoptMetricThresholds(checks) {
  for (const c of checks || []) {
    if (c.type !== 'system') continue;
    c.config = c.config || {};
    if (!c.config.metricThresholds?.length) c.config.metricThresholds = legacyMetricThresholds(c.config);
    c.config.metricThresholds = c.config.metricThresholds.map((t) => ({ ...t }));
  }
}

export async function mount(root, ctx) {
  const isNew = !ctx.params.id;
  const state = { draft: null, nodes: [], groups: { groups: [], tags: [] }, settings: null, errors: {}, destroyed: false, saving: false };
  root.append(skeleton({ lines: 6 }));

  // Load supporting data
  const [nodes, groups, settings] = await Promise.all([api.get('/api/nodes').catch(() => []), api.get('/api/groups').catch(() => ({ groups: [], tags: [] })), api.get('/api/settings').catch(() => null)]);
  state.nodes = nodes || []; state.groups = groups || { groups: [], tags: [] }; state.settings = settings;

  if (isNew) {
    const tplId = ctx.query.get('template');
    let tpl = null;
    if (tplId) { try { const list = await api.get('/api/templates'); tpl = (list || []).find((t) => t.id === tplId) || null; } catch { tpl = null; } }
    state.draft = tpl
      ? { ...tpl.node, id: undefined, template: tpl.id, tags: [...(tpl.node.tags || [])], enabled: true, importance: tpl.node.importance || 'normal', checks: (tpl.checks || []).map((c) => ({ ...c, _key: uid('c'), id: undefined, config: { ...(c.config || {}) }, alerts: c.alerts ? { ...c.alerts } : null })) }
      : { name: '', host: '', groups: [], tags: [], notes: '', importance: 'normal', enabled: true, dependsOnNodeId: null, template: '', checks: [] };
    if (tpl && (!state.draft.name || state.draft.name === tpl.name)) state.draft.name = '';
  } else {
    let node;
    try { node = await api.get(`/api/nodes/${ctx.params.id}`); } catch (e) {
      replace(root, h('div', { class: 'card' }, emptyState({ icon: 'alert', title: 'Could not load this node', text: e.message, actions: h('a', { class: 'btn', href: '#/nodes' }, 'Back to nodes') })));
      return {};
    }
    state.draft = { ...node, tags: [...(node.tags || [])], checks: (node.checks || []).map((c) => ({ ...c, _key: uid('c'), config: { ...(c.config || {}) }, alerts: c.alerts ? { ...c.alerts } : null })) };
    delete state.draft.stateByCheck; delete state.draft.lastResults; delete state.draft.status; delete state.draft.inMaintenance;
  }
  if (state.destroyed) return {};
  adoptMetricThresholds(state.draft.checks);
  const d = state.draft;

  ctx.setTitle(isNew ? 'Add node' : `Edit ${d.name}`, {
    actions: [h('a', { class: 'btn', href: isNew ? '#/nodes' : `#/nodes/${d.id}` }, 'Cancel'), h('button', { class: 'btn btn-primary', type: 'button', onclick: save }, icon('save'), isNew ? 'Create node' : 'Save changes')],
  });

  /* ---------- Node fields ---------- */
  const nameInput = textInput({ value: d.name, placeholder: 'e.g. Living room router', oninput: () => { d.name = nameInput.value; } });
  const hostInput = textInput({ value: d.host, placeholder: 'e.g. 192.168.1.1, nas.local or https://example.com', oninput: () => { d.host = hostInput.value; } });
  // A node can be in several groups, so the group field is the same chip
  // editor as tags, suggesting the groups already in use.
  const groupsInput = chipInput({ values: nodeGroups(d), placeholder: 'Add a group and press Enter', suggestions: state.groups.groups.map((g) => g.name), onChange: (v) => { d.groups = v; } });
  const tagsInput = chipInput({ values: d.tags || [], placeholder: 'Add a tag and press Enter', suggestions: state.groups.tags.map((t) => t.name), onChange: (v) => { d.tags = v; } });
  const importanceSel = selectInput({ options: IMPORTANCE_OPTIONS, value: d.importance || 'normal', onchange: () => { d.importance = importanceSel.value; } });
  const notesInput = textarea({ value: d.notes || '', placeholder: 'Anything useful: where it lives, how to reboot it, who owns it…', oninput: () => { d.notes = notesInput.value; } });
  const enabledToggle = toggle({ label: 'Enabled — run its checks on schedule', checked: d.enabled !== false, onChange: (v) => { d.enabled = v; } });
  const dependsSel = selectInput({ options: [{ value: '', label: 'None' }, ...state.nodes.filter((n) => String(n.id) !== String(d.id)).map((n) => ({ value: n.id, label: `${n.name} (${n.host})` }))], value: d.dependsOnNodeId ?? '', onchange: () => { d.dependsOnNodeId = dependsSel.value ? Number(dependsSel.value) : null; } });

  const nameField = field({ label: 'Name', input: nameInput });
  const hostField = field({ label: 'Host / target', input: hostInput, help: 'Hostname, IP address or URL. Individual checks can override it.' });
  const nodeCard = h('section', { class: 'card', 'aria-label': 'Node' },
    h('h2', { style: { marginBottom: '18px' } }, 'Node'),
    h('div', { class: 'form-grid' },
      nameField, hostField,
      field({ label: 'Groups', input: groupsInput, help: 'A node can be in more than one. Groups drive dashboard filters, maintenance windows and the wallboard.' }),
      field({ label: 'Tags', input: tagsInput }),
      field({ label: 'Importance', input: importanceSel, help: 'Critical and high nodes are listed first on the wallboard.' }),
      field({ label: 'Depends on', input: dependsSel, help: 'Alerts for this node are suppressed while the parent is down, and its failures are shown as "affected by" the parent.' }),
      field({ label: 'Notes', input: notesInput, cls: 'span-2' }),
      h('div', { class: 'span-2' }, enabledToggle),
    ));

  /* ---------- Checks ---------- */
  const checksList = h('div', { class: 'stack' });
  const checksCard = h('section', { 'aria-label': 'Checks' },
    h('div', { class: 'row-between', style: { marginBottom: '14px' } }, h('h2', null, 'Checks'), h('button', { class: 'btn', type: 'button', onclick: addCheck }, icon('plus'), 'Add check')),
    checksList);

  function renderChecks() {
    clear(checksList);
    if (!d.checks.length) {
      checksList.append(h('div', { class: 'card' }, emptyState({ icon: 'activity', title: 'No checks yet', text: 'A node needs at least one check to be monitored. Ping is a good start for devices; HTTP/S for websites and APIs.', compact: true, actions: h('button', { class: 'btn btn-primary btn-sm', type: 'button', onclick: addCheck }, icon('plus'), 'Add check') })));
      return;
    }
    d.checks.forEach((c, i) => checksList.append(checkCard(c, i)));
  }

  function addCheck() {
    const picker = h('div', { class: 'type-picker', role: 'list' });
    const m = openModal({ title: 'Add a check', wide: true, body: [h('p', null, 'What do you want to know about this node?'), picker] });
    for (const t of CHECK_TYPES) picker.append(h('button', { type: 'button', role: 'listitem', onclick: () => { d.checks.push(defaultCheck(t.value, state.settings)); m.close(); renderChecks(); setTimeout(() => { const cards = checksList.querySelectorAll('.editor-check'); const last = cards[cards.length - 1]; last?.scrollIntoView({ behavior: 'smooth', block: 'start' }); last?.querySelector('input')?.focus(); }, 30); } }, h('b', null, t.label), h('span', null, t.desc)));
  }

  function checkCard(c, index) {
    const cfg = c.config;
    const card = h('section', { class: 'card editor-check', 'aria-label': `${c.name} check` });
    const err = state.errors.checks?.[c._key] || {};
    const nameIn = textInput({ value: c.name, placeholder: checkTypeLabel(c.type), 'aria-label': 'Check name', oninput: () => { c.name = nameIn.value; } });
    const head = h('div', { class: 'editor-check-head' },
      h('span', { class: 'type-badge' }, checkTypeLabel(c.type)),
      h('div', { class: 'name-field' }, nameIn, err.name ? h('div', { class: 'error small', style: { color: 'var(--down)' } }, err.name) : null),
      toggle({ label: 'Enabled', checked: c.enabled !== false, onChange: (v) => { c.enabled = v; } }),
      h('div', { class: 'head-actions' },
        h('button', { class: 'btn btn-sm', type: 'button', onclick: (e) => testCheck(c, e.currentTarget, card) }, icon('play'), 'Test this check'),
        h('button', { class: 'btn btn-sm icon-btn', type: 'button', 'aria-label': 'Duplicate check', title: 'Duplicate', onclick: () => { const copy = { ...c, _key: uid('c'), id: undefined, name: `${c.name} (copy)`, config: { ...c.config }, alerts: c.alerts ? { ...c.alerts } : null }; d.checks.splice(index + 1, 0, copy); renderChecks(); } }, icon('copy')),
        h('button', { class: 'btn btn-sm icon-btn btn-danger', type: 'button', 'aria-label': 'Remove check', title: 'Remove', onclick: async () => { if (c.id) { const ok = await confirmDialog({ title: `Remove "${c.name}"?`, message: 'Its history will be deleted when you save.', confirmLabel: 'Remove', danger: true }); if (!ok) return; } d.checks.splice(index, 1); renderChecks(); } }, icon('trash')),
      ));
    card.append(head);

    // Schedule row
    const custom = !INTERVALS.includes(Number(c.intervalSeconds));
    const customIn = numberInput({ value: c.intervalSeconds, min: state.settings?.general?.minIntervalSeconds || 10, step: 1, 'aria-label': 'Custom interval in seconds', hidden: !custom, oninput: () => { c.intervalSeconds = Number(customIn.value); } });
    const intervalSel = selectInput({ options: [...intervalOptions(), { value: 'custom', label: 'Custom…' }], value: custom ? 'custom' : c.intervalSeconds, onchange: () => { if (intervalSel.value === 'custom') { customIn.hidden = false; customIn.focus(); } else { customIn.hidden = true; c.intervalSeconds = Number(intervalSel.value); } } });
    const timeoutIn = numberInput({ value: c.timeoutSeconds, min: 1, max: 120, oninput: () => { c.timeoutSeconds = Number(timeoutIn.value); } });
    const retriesIn = numberInput({ value: c.retries ?? 0, min: 0, max: 10, oninput: () => { c.retries = Number(retriesIn.value); } });
    const thresholdIn = numberInput({ value: c.failureThreshold || '', min: 0, max: 50, placeholder: `Default (${state.settings?.alerts?.failureThreshold || 2})`, oninput: () => { c.failureThreshold = Number(thresholdIn.value) || 0; } });
    card.append(h('div', { class: 'form-grid-4' },
      field({ label: 'Interval', input: h('div', { class: 'stack-sm', style: { gap: '6px' } }, intervalSel, customIn), error: err.intervalSeconds }),
      field({ label: 'Timeout', input: h('div', { class: 'input-with-unit' }, timeoutIn, h('span', { class: 'unit' }, 'seconds')), error: err.timeoutSeconds }),
      field({ label: 'Retries', input: retriesIn, help: 'Immediate retries inside one run before counting a failure.' }),
      field({ label: 'Failures before down', input: thresholdIn, help: 'Consecutive failed runs before the check is marked down and alerts fire.' }),
    ));

    // Type-specific
    card.append(h('div', { class: 'section-title', style: { marginTop: '22px' } }, `${checkTypeLabel(c.type)} settings`));
    card.append(typeFields(c, err));

    // Alert overrides
    card.append(alertOverrides(c));

    // Test result area
    const testArea = h('div', { class: 'test-area' });
    if (c._test) testArea.append(h('div', { style: { marginTop: '16px' } }, h('div', { class: 'section-title' }, 'Test result'), resultInspector(c._test, c, { compact: true })));
    card.append(testArea);
    card._testArea = testArea;
    return card;
  }

  function targetField(c, err, label, placeholder, help) {
    const input = textInput({ value: c.config.target || '', placeholder: placeholder || (d.host ? `Uses node host (${d.host})` : 'Uses node host'), oninput: () => { c.config.target = input.value; } });
    return field({ label: label || 'Target override', input, help: help || 'Leave blank to use the node host.', error: err.target });
  }
  const warnFields = (c, { loss = false } = {}) => {
    const lat = numberInput({ value: c.config.latencyWarnMs || '', min: 0, placeholder: state.settings?.general?.latencyWarnMs ? `Default (${state.settings.general.latencyWarnMs} ms)` : 'Off', oninput: () => { c.config.latencyWarnMs = Number(lat.value) || 0; } });
    const out = [field({ label: 'Warn when slower than', input: h('div', { class: 'input-with-unit' }, lat, h('span', { class: 'unit' }, 'ms')), help: 'Marks the check degraded (not down).' })];
    if (loss) {
      const l = numberInput({ value: c.config.packetLossWarnPct || '', min: 0, max: 100, placeholder: state.settings?.general?.packetLossWarnPct ? `Default (${state.settings.general.packetLossWarnPct}%)` : 'Off', oninput: () => { c.config.packetLossWarnPct = Number(l.value) || 0; } });
      out.push(field({ label: 'Warn when packet loss exceeds', input: h('div', { class: 'input-with-unit' }, l, h('span', { class: 'unit' }, '%')) }));
    }
    return out;
  };

  function httpCommon(c, err) {
    const cfg = c.config;
    const method = selectInput({ options: ['GET', 'HEAD', 'POST', 'PUT', 'DELETE', 'OPTIONS'], value: cfg.method || 'GET', onchange: () => { cfg.method = method.value; } });
    const expected = textInput({ value: cfg.expectedStatus || '', placeholder: '200-399', oninput: () => { cfg.expectedStatus = expected.value; } });
    const follow = checkbox({ label: 'Follow redirects', checked: cfg.followRedirects !== false, onChange: (v) => { cfg.followRedirects = v; } });
    const ignoreTls = checkbox({ label: 'Ignore TLS certificate errors (self-signed)', checked: !!cfg.ignoreTlsErrors, onChange: (v) => { cfg.ignoreTlsErrors = v; } });
    const body = textarea({ value: cfg.body || '', placeholder: 'Optional request body (for POST / PUT)', rows: 2, oninput: () => { cfg.body = body.value; } });
    // headers
    const headersWrap = h('div', { class: 'kv-rows' });
    const renderHeaders = () => {
      clear(headersWrap);
      const entries = Object.entries(cfg.headers || {});
      entries.forEach(([k, v]) => {
        const kIn = textInput({ value: k, placeholder: 'Header', 'aria-label': 'Header name' });
        const vIn = textInput({ value: v, placeholder: 'Value', 'aria-label': 'Header value' });
        const commit = () => { const next = {}; headersWrap.querySelectorAll('.kv-row').forEach((r) => { const [a, b] = r.querySelectorAll('input'); if (a.value.trim()) next[a.value.trim()] = b.value; }); cfg.headers = next; };
        kIn.addEventListener('change', commit); vIn.addEventListener('input', commit);
        headersWrap.append(h('div', { class: 'kv-row' }, kIn, vIn, h('button', { class: 'btn btn-sm icon-btn', type: 'button', 'aria-label': 'Remove header', onclick: () => { const nh = { ...cfg.headers }; delete nh[k]; cfg.headers = nh; renderHeaders(); } }, icon('x'))));
      });
      headersWrap.append(h('div', null, h('button', { class: 'btn btn-sm', type: 'button', onclick: () => { cfg.headers = { ...(cfg.headers || {}), [`Header-${entries.length + 1}`]: '' }; renderHeaders(); } }, icon('plus'), 'Add header')));
    };
    renderHeaders();
    return h('div', { class: 'stack-sm' },
      h('div', { class: 'form-grid' },
        targetField(c, err, 'URL', d.host ? `Uses node host (${d.host})` : 'https://example.com/health', 'Full URL. Leave blank to use the node host (https assumed).'),
        field({ label: 'Method', input: method }),
        field({ label: 'Expected status', input: expected, help: 'A code, a range or a list: 200 · 200-299 · 200,301,302. Anything else is an unexpected response.' }),
        field({ label: 'Options', input: h('div', { class: 'stack-sm', style: { gap: '8px', paddingTop: '6px' } }, follow, ignoreTls) }),
        field({ label: 'Request headers', input: headersWrap, cls: 'span-2' }),
        field({ label: 'Request body', input: body, cls: 'span-2' }),
      ));
  }

  function certFields(c, { withToggle }) {
    const cfg = c.config;
    const days = numberInput({ value: cfg.certWarnDays || '', min: 1, max: 365, placeholder: `Default (${state.settings?.alerts?.certWarnDays || 14})`, oninput: () => { cfg.certWarnDays = Number(days.value) || 0; } });
    const items = [];
    if (withToggle) items.push(field({ label: 'Certificate', input: h('div', { style: { paddingTop: '6px' } }, checkbox({ label: 'Also check the HTTPS certificate', checked: cfg.certCheck !== false, onChange: (v) => { cfg.certCheck = v; } })) }));
    items.push(field({ label: 'Warn before expiry', input: h('div', { class: 'input-with-unit' }, days, h('span', { class: 'unit' }, 'days')) }));
    return items;
  }

  function contentWatch(c) {
    const cfg = c.config;
    const mode = selectInput({ options: [
      { value: '', label: 'Off (recommended)' }, { value: 'hash', label: 'Normalised page content (hash)' }, { value: 'header', label: 'A response header' }, { value: 'redirect', label: 'Final redirect destination' }, { value: 'keyword', label: 'Presence of a keyword' }, { value: 'json', label: 'A JSON value' },
    ], value: cfg.contentWatch || '', onchange: () => { cfg.contentWatch = mode.value; headerRow.hidden = mode.value !== 'header'; kwRow.hidden = mode.value !== 'keyword'; jsonRow.hidden = mode.value !== 'json'; } });
    const headerIn = textInput({ value: cfg.contentHeader || '', placeholder: 'e.g. ETag, Last-Modified, Server', oninput: () => { cfg.contentHeader = headerIn.value; } });
    const headerRow = field({ label: 'Header to watch', input: headerIn }); headerRow.hidden = (cfg.contentWatch || '') !== 'header';
    const kwIn = textInput({ value: cfg.keyword || '', placeholder: 'Text that should stay present', oninput: () => { cfg.keyword = kwIn.value; } });
    const kwRow = field({ label: 'Keyword to watch', input: kwIn }); kwRow.hidden = (cfg.contentWatch || '') !== 'keyword';
    const jpIn = textInput({ value: cfg.jsonPath || '', placeholder: 'e.g. version or data.items[0].name', oninput: () => { cfg.jsonPath = jpIn.value; } });
    const jsonRow = field({ label: 'JSON path to watch', input: jpIn }); jsonRow.hidden = (cfg.contentWatch || '') !== 'json';
    return h('details', { class: 'collapsible', open: !!cfg.contentWatch },
      h('summary', null, icon('chevronRight'), 'Advanced: watch for response / content changes'),
      h('div', { class: 'stack-sm', style: { paddingTop: '8px' } },
        h('div', { class: 'banner banner-warn' }, icon('alert'), h('div', null,
          h('b', null, 'Expect false positives on dynamic pages. '),
          'Timestamps, adverts, rotating content, session tokens and login pages change on every request. A detected change means the response is different from last time — it is ', h('b', null, 'not'), ' a security conclusion. Raw page bodies are never stored; only a hash or the watched value.')),
        h('div', { class: 'form-grid' }, field({ label: 'Compare', input: mode, help: 'When the watched value differs from the previous run the check is marked degraded with "response changed".' }), headerRow, kwRow, jsonRow),
      ));
  }

  // jsonRecordFields is the recording half of a json check (#55): a switch
  // that reveals the metric name, its unit and the same four thresholds an
  // SNMP reading has. The value is charted on the node page under that name,
  // so the fields say what the chart will say.
  function jsonRecordFields(c, err) {
    const cfg = c.config;
    const details = h('div', { class: 'stack-sm', style: { paddingTop: '8px' } });
    details.hidden = !cfg.jsonRecord;
    const record = checkbox({ label: 'Record this value', checked: !!cfg.jsonRecord, onChange: (v) => { cfg.jsonRecord = v; details.hidden = !v; } });
    const name = textInput({ value: cfg.jsonMetric || '', placeholder: 'value', maxlength: 64, oninput: () => { cfg.jsonMetric = name.value; } });
    const unit = textInput({ value: cfg.jsonUnit || '', placeholder: 'e.g. °C, %, ms', style: { minWidth: '64px' }, oninput: () => { cfg.jsonUnit = unit.value; } });
    const thresholds = JSON_THRESHOLD_FIELDS.map(([k, label]) => {
      const input = numberInput({ value: cfg[k] ?? '', step: 'any', placeholder: 'off', 'aria-label': `${label} threshold`, oninput: () => { cfg[k] = input.value; } });
      return field({ label, input, error: err[k] });
    });
    details.append(
      h('div', { class: 'form-grid' },
        field({ label: 'Metric name', input: name, error: err.jsonMetric, help: 'Names the value in charts and in /api/history?metric=. Leave blank for "value".' }),
        field({ label: 'Unit', input: unit, help: 'Shown beside the value. Optional.' })),
      h('div', { class: 'form-grid-4' }, ...thresholds),
      h('p', { class: 'note' }, 'Thresholds are strict: crossing a ', h('b', null, 'Warn'), ' value marks the check degraded, crossing a ', h('b', null, 'Crit'), ' value marks it down. Leave one blank to turn it off. A value that is text rather than a number is kept in the last result and never thresholded.'));
    return h('div', { class: 'stack-sm' },
      h('div', { style: { paddingTop: '6px' } }, record),
      h('div', { class: 'help' }, 'Keeps the number the path points at on every run, so it can be charted over time and given thresholds — a temperature, a queue length, a count of blocked queries.'),
      details);
  }

  function typeFields(c, err) {
    const cfg = c.config;
    switch (c.type) {
      case 'ping': {
        const count = numberInput({ value: cfg.pingCount || 4, min: 1, max: 20, oninput: () => { cfg.pingCount = Number(count.value); } });
        // An empty value means "whatever Settings › General says", which is
        // what cleanCheck drops from the config before it is saved.
        const method = selectInput({
          options: PING_METHOD_OPTIONS,
          value: cfg.pingMethod || '',
          onchange: () => { cfg.pingMethod = method.value; },
        });
        return h('div', { class: 'form-grid-3' }, targetField(c, err, 'Host override'),
          field({ label: 'Packets per run', input: count, error: err.pingCount, help: 'Packets per run, default 4, max 20. More packets give a steadier average and a more meaningful jitter and standard deviation.' }),
          field({ label: 'Ping method', input: method, help: 'Built-in sends the echo requests itself; system ping runs the operating system\'s ping command.' }),
          ...warnFields(c, { loss: true }));
      }
      case 'http':
        return h('div', { class: 'stack' }, httpCommon(c, err), h('div', { class: 'form-grid-3' }, ...certFields(c, { withToggle: true }), ...warnFields(c)), contentWatch(c));
      case 'keyword': {
        const kw = textInput({ value: cfg.keyword || '', placeholder: 'Text to look for', oninput: () => { cfg.keyword = kw.value; } });
        const absent = checkbox({ label: 'Text must be absent (alert when it appears)', checked: !!cfg.keywordAbsent, onChange: (v) => { cfg.keywordAbsent = v; } });
        return h('div', { class: 'stack' }, h('div', { class: 'form-grid' }, field({ label: 'Keyword', input: kw, error: err.keyword, help: 'Case-sensitive text that must appear in the response body.' }), field({ label: 'Mode', input: h('div', { style: { paddingTop: '6px' } }, absent) })), httpCommon(c, err), h('div', { class: 'form-grid-3' }, ...warnFields(c)));
      }
      case 'json': {
        const path = textInput({ value: cfg.jsonPath || '', placeholder: 'e.g. status or data.items[0].name', oninput: () => { cfg.jsonPath = path.value; } });
        const exp = textInput({ value: cfg.jsonExpected || '', placeholder: 'Leave blank to only require the path to exist', oninput: () => { cfg.jsonExpected = exp.value; } });
        return h('div', { class: 'stack' }, h('div', { class: 'form-grid' }, field({ label: 'JSON path', input: path, error: err.jsonPath, help: 'Dotted path into the response. Arrays use [index].' }), field({ label: 'Expected value', input: exp })), jsonRecordFields(c, err), httpCommon(c, err), h('div', { class: 'form-grid-3' }, ...warnFields(c)));
      }
      case 'cert': {
        const port = numberInput({ value: cfg.port || 443, min: 1, max: 65535, oninput: () => { cfg.port = Number(port.value); } });
        return h('div', { class: 'form-grid-3' }, targetField(c, err, 'Host override'), field({ label: 'Port', input: port, error: err.port }), ...certFields(c, { withToggle: false }));
      }
      case 'tcp': {
        const port = numberInput({ value: cfg.port || '', min: 1, max: 65535, placeholder: 'e.g. 22, 80, 443, 32400', oninput: () => { cfg.port = Number(port.value); } });
        return h('div', { class: 'form-grid-3' }, targetField(c, err, 'Host override'), field({ label: 'Port', input: port, error: err.port }), ...warnFields(c));
      }
      case 'dns': {
        const rt = selectInput({ options: [{ value: 'A', label: 'A / AAAA (addresses)' }, { value: 'CNAME', label: 'CNAME' }, { value: 'MX', label: 'MX' }, { value: 'TXT', label: 'TXT' }], value: cfg.recordType || 'A', onchange: () => { cfg.recordType = rt.value; } });
        const expected = chipInput({ values: cfg.expectedIps || [], placeholder: 'Add an expected value', onChange: (v) => { cfg.expectedIps = v; } });
        const server = textInput({ value: cfg.dnsServer || '', placeholder: 'System resolver', oninput: () => { cfg.dnsServer = server.value; } });
        return h('div', { class: 'form-grid' }, targetField(c, err, 'Hostname override', d.host ? `Uses node host (${d.host})` : 'example.com'), field({ label: 'Record type', input: rt }), field({ label: 'Expected values', input: expected, help: 'Optional. Every resolved value must be in this list.' }), field({ label: 'DNS server', input: server, help: 'Optional resolver host[:port], e.g. 192.168.1.2 or 1.1.1.1:53.' }), ...warnFields(c));
      }
      case 'custom': {
        const cmd = textarea({ value: cfg.command || '', class: 'code', rows: 2, placeholder: "e.g. /usr/local/bin/check-disk.sh {{target}}", oninput: () => { cfg.command = cmd.value; } });
        const workdir = textInput({ value: cfg.workDir || '', placeholder: "Optional — defaults to the service's own working directory", oninput: () => { cfg.workDir = workdir.value; } });
        const envWrap = h('div', { class: 'kv-rows' });
        const renderEnv = () => {
          clear(envWrap);
          const entries = Object.entries(cfg.env || {});
          entries.forEach(([k, v]) => {
            const kIn = textInput({ value: k, placeholder: 'NAME', class: 'mono', 'aria-label': 'Environment variable name' });
            const vIn = textInput({ value: v, placeholder: 'value', 'aria-label': 'Environment variable value' });
            const commit = () => { const next = {}; envWrap.querySelectorAll('.kv-row').forEach((r) => { const [a, b] = r.querySelectorAll('input'); if (a.value.trim()) next[a.value.trim()] = b.value; }); cfg.env = next; };
            kIn.addEventListener('change', commit); vIn.addEventListener('input', commit);
            envWrap.append(h('div', { class: 'kv-row' }, kIn, vIn, h('button', { class: 'btn btn-sm icon-btn', type: 'button', 'aria-label': 'Remove variable', onclick: () => { const nv = { ...cfg.env }; delete nv[k]; cfg.env = nv; renderEnv(); } }, icon('x'))));
          });
          envWrap.append(h('div', null, h('button', { class: 'btn btn-sm', type: 'button', onclick: () => { cfg.env = { ...(cfg.env || {}), [`VAR_${entries.length + 1}`]: '' }; renderEnv(); } }, icon('plus'), 'Add variable')));
        };
        renderEnv();
        return h('div', { class: 'stack' },
          h('div', { class: 'banner banner-warn' }, icon('alert'), h('div', null,
            h('b', null, 'Runs on this machine. '), "The command executes with the GWatch service's own permissions. Only trusted administrators should be able to create or edit a custom check.")),
          h('div', { class: 'form-grid' },
            targetField(c, err, 'Target', 'Passed as {{target}} and GWATCH_TARGET', 'Optional. Passed to the command as the GWATCH_TARGET environment variable, and substituted for {{target}} in any argument that contains it.'),
            field({ label: 'Command', input: cmd, cls: 'span-2', error: err.command, help: "No shell is used: the command line is split on whitespace (quote with ' or \" to keep a value together, e.g. a path with spaces). Use sh -c '...' (or cmd /C ... on Windows) explicitly if you need pipes, globbing or environment expansion." }),
            field({ label: 'Working directory', input: workdir }),
            field({ label: 'Environment variables', input: envWrap, cls: 'span-2' }),
          ),
          h('details', { class: 'collapsible' },
            h('summary', null, icon('chevronRight'), 'Output contract'),
            h('div', { class: 'stack-sm', style: { paddingTop: '8px' } },
              h('p', { class: 'note' }, 'Exit code 0 = up, 2 = degraded, anything else = down. The check is killed and reported "timed out" if it runs past the timeout above.'),
              h('p', { class: 'note' }, 'Stdout may contain lines of the form ', h('code', null, 'key=value'), ' (case-insensitive keys): ', h('code', null, 'status=up|degraded|down'), ' overrides the exit code, ', h('code', null, 'message=...'), ' is shown as the result message, ', h('code', null, 'latency_ms=<number>'), ' sets the charted latency, and ', h('code', null, 'error=...'), ' sets the error text. Any other output (stdout and stderr, up to 8 KiB) is kept and shown with the result.'),
            )),
        );
      }
      case 'system': return systemFields(c, err);
      case 'snmp': return snmpFields(c, err);
      default: return h('p', { class: 'note' }, 'No settings for this type.');
    }
  }

  /* ---------- SNMP ---------- */

  function snmpFields(c, err) {
    const cfg = c.config;
    if (!Array.isArray(cfg.snmpOids)) cfg.snmpOids = [];

    const version = selectInput({
      options: [{ value: '2c', label: 'Version 2c — a community string' }, { value: '3', label: 'Version 3 — a user with authentication' }],
      value: cfg.snmpVersion || '2c',
      onchange: () => { cfg.snmpVersion = version.value; renderCreds(); },
    });
    const port = numberInput({ value: cfg.snmpPort || 161, min: 1, max: 65535, oninput: () => { cfg.snmpPort = Number(port.value) || 161; } });

    // A stored credential never comes back from the server: what arrives is
    // the same mask the settings screen uses. The box therefore starts empty
    // and the mask stays in the draft, so leaving the box alone keeps what is
    // stored and typing in it replaces that.
    const secretInput = (key) => {
      const stored = !!cfg[key];
      const input = textInput({
        type: 'password', value: '', autocomplete: 'off',
        placeholder: stored ? 'Leave blank to keep the stored value' : '',
        oninput: () => { cfg[key] = input.value; },
      });
      return { input, help: stored ? 'A value is stored — leave this blank to keep it.' : 'Nothing stored yet.' };
    };

    const community = secretInput('snmpCommunity');
    const communityRow = h('div', { class: 'form-grid' },
      field({ label: 'Community string', input: community.input, error: err.snmpCommunity, help: `${community.help} Most devices ship with "public", which is read-only.` }));

    const user = textInput({ value: cfg.snmpUser || '', placeholder: 'e.g. monitor', oninput: () => { cfg.snmpUser = user.value; } });
    const authProto = selectInput({
      options: SNMP_AUTH_PROTOCOLS.map((p) => ({ value: p, label: p || 'None (noAuthNoPriv)' })),
      value: cfg.snmpAuthProto || '', onchange: () => { cfg.snmpAuthProto = authProto.value; },
    });
    const privProto = selectInput({
      options: SNMP_PRIV_PROTOCOLS.map((p) => ({ value: p, label: p || 'None (no encryption)' })),
      value: cfg.snmpPrivProto || '', onchange: () => { cfg.snmpPrivProto = privProto.value; },
    });
    const authPass = secretInput('snmpAuthPass');
    const privPass = secretInput('snmpPrivPass');
    const v3Row = h('div', { class: 'form-grid' },
      field({ label: 'User', input: user, error: err.snmpUser }),
      field({ label: 'Authentication', input: authProto, help: 'SHA256 or better where the device offers it.' }),
      field({ label: 'Authentication password', input: authPass.input, error: err.snmpAuthPass, help: authPass.help }),
      field({ label: 'Encryption', input: privProto, help: 'Encryption needs authentication as well.' }),
      field({ label: 'Encryption password', input: privPass.input, error: err.snmpPrivPass, help: privPass.help }));

    const credsWrap = h('div');
    function renderCreds() {
      clear(credsWrap);
      credsWrap.append((cfg.snmpVersion || '2c') === '3' ? v3Row : communityRow);
    }
    renderCreds();

    /* ---- the readings table ---- */
    const rowsWrap = h('div', { class: 'table-wrap' });
    const small = (props) => numberInput({ ...props, style: { minWidth: '72px' } });

    function renderRows() {
      clear(rowsWrap);
      if (!cfg.snmpOids.length) {
        rowsWrap.append(h('p', { class: 'note' }, 'No readings yet. Add one from Presets, or add a blank row and paste an OID into it.'));
        return;
      }
      const table = h('table', { class: 'table' }, h('thead', null, h('tr', null,
        h('th', null, 'OID'), h('th', null, 'Name'), h('th', null, 'Kind'), h('th', { class: 'num' }, 'Scale'),
        h('th', null, 'Unit'), h('th', { class: 'num' }, 'Warn >'), h('th', { class: 'num' }, 'Crit >'),
        h('th', { class: 'num' }, 'Warn <'), h('th', { class: 'num' }, 'Crit <'), h('th', null, ''))));
      const tb = h('tbody');
      cfg.snmpOids.forEach((o, i) => {
        const oid = textInput({ value: o.oid || '', class: 'mono', placeholder: '1.3.6.1.2.1.1.3.0', 'aria-label': 'OID', oninput: () => { o.oid = oid.value; } });
        const name = textInput({ value: o.name || '', placeholder: 'Uptime', 'aria-label': 'Reading name', oninput: () => { o.name = name.value; } });
        const kind = selectInput({ options: [{ value: 'gauge', label: 'Gauge' }, { value: 'counter', label: 'Counter' }], value: o.kind || 'gauge', onchange: () => { o.kind = kind.value; } });
        const scale = small({ value: o.scale ?? 1, step: 'any', 'aria-label': 'Scale', oninput: () => { o.scale = scale.value; } });
        const unit = textInput({ value: o.unit || '', placeholder: '—', 'aria-label': 'Unit', style: { minWidth: '64px' }, oninput: () => { o.unit = unit.value; } });
        const th = SNMP_THRESHOLD_KEYS.map((k) => {
          const input = small({ value: o[k] ?? '', step: 'any', placeholder: 'off', 'aria-label': `${k} threshold`, oninput: () => { o[k] = input.value; } });
          return h('td', { class: 'num' }, input);
        });
        tb.append(h('tr', null,
          h('td', null, oid), h('td', null, name), h('td', null, kind), h('td', { class: 'num' }, scale), h('td', null, unit), ...th,
          h('td', null, h('button', { class: 'btn btn-sm icon-btn btn-danger', type: 'button', 'aria-label': `Remove ${o.name || 'reading'}`, title: 'Remove', onclick: () => { cfg.snmpOids.splice(i, 1); renderRows(); } }, icon('x')))));
      });
      table.append(tb);
      rowsWrap.append(table);
    }
    renderRows();

    const presets = selectInput({
      options: [{ value: '', label: 'Presets…' }],
      onchange: async () => {
        const [gi, ii] = presets.value.split(':');
        presets.value = '';
        const preset = SNMP_PRESETS[Number(gi)]?.items[Number(ii)];
        if (!preset) return;
        let index = 1;
        if (preset.oid.includes('{N}')) {
          const answer = await promptDialog({
            title: 'Which one?',
            label: preset.oid.startsWith('1.3.6.1.2.1.25') ? 'Processor number' : 'Interface index',
            value: '1',
            confirmLabel: 'Add reading',
            message: 'SNMP numbers ports and processors itself, so the number here is the device’s, not the label on its case. The SNMP guide shows how to walk a device to find out which is which.',
          });
          if (answer == null || answer === '') return;
          index = Number(answer) || 1;
        }
        cfg.snmpOids.push(oidRow(preset, index));
        renderRows();
      },
    });
    for (const [gi, group] of SNMP_PRESETS.entries()) {
      const og = h('optgroup', { label: group.group });
      group.items.forEach((item, ii) => og.append(h('option', { value: `${gi}:${ii}` }, item.label)));
      presets.append(og);
    }

    return h('div', { class: 'stack' },
      h('div', { class: 'form-grid' },
        targetField(c, err, 'Device override', d.host ? `Uses node host (${d.host})` : '192.168.1.1', 'The router, switch or access point to read. Leave blank to use the node host.'),
        field({ label: 'SNMP version', input: version }),
        field({ label: 'Port', input: port, error: err.snmpPort, help: 'UDP 161 unless the device was changed.' })),
      credsWrap,
      h('div', { class: 'row-between', style: { marginTop: '4px' } },
        h('div', { class: 'section-title', style: { marginBottom: 0 } }, 'Readings'),
        h('div', { class: 'btn-group' },
          presets,
          h('button', { class: 'btn btn-sm', type: 'button', onclick: (e) => walkDevice(c, e.currentTarget, renderRows) }, icon('search'), 'Walk this device'),
          h('button', { class: 'btn btn-sm', type: 'button', onclick: () => { cfg.snmpOids.push(oidRow()); renderRows(); } }, icon('plus'), 'Add a reading'))),
      h('p', { class: 'note' }, 'A ', h('b', null, 'gauge'), ' is a value that already means something — a percentage, a temperature, a link state. A ', h('b', null, 'counter'), ' only ever climbs, so GWatch charts how fast it climbs: an interface’s octet counter with a scale of 8 becomes bits per second. The first run after a restart has nothing to compare against, so a counter has no reading (and no verdict) until the second one.'),
      h('p', { class: 'note' }, 'Thresholds are strict: crossing a ', h('b', null, 'Warn'), ' value marks the check degraded, crossing a ', h('b', null, 'Crit'), ' value marks it down. Setting both ', h('b', null, 'Crit >'), ' and ', h('b', null, 'Crit <'), ' to the same number means "must be exactly this", which is how a link-state reading is expressed. A reading that is text rather than a number is shown as it arrived and never thresholded.'),
      err.snmpOids ? h('div', { class: 'error small', style: { color: 'var(--down)' } }, err.snmpOids) : null,
      rowsWrap,
    );
  }

  // walkDevice asks the device what it can tell us and shows the answer as a
  // list to tick. It is the only honest way to find an interface's SNMP
  // index, which is very often not the number printed on the case.
  async function walkDevice(c, btn, onAdded) {
    const cfg = c.config;
    const host = (cfg.target || d.host || '').trim();
    if (!host) { toast('Enter the device’s host on the node (or a target on this check) first.', { kind: 'error' }); return; }
    const done = busy(btn, 'Walking…');
    let answer;
    try {
      answer = await api.post('/api/snmp/walk', {
        host,
        checkId: c.id || 0,
        version: cfg.snmpVersion || '2c',
        port: Number(cfg.snmpPort) || 161,
        community: cfg.snmpCommunity || '',
        user: cfg.snmpUser || '',
        authProto: cfg.snmpAuthProto || '',
        authPass: cfg.snmpAuthPass || '',
        privProto: cfg.snmpPrivProto || '',
        privPass: cfg.snmpPrivPass || '',
        oid: '1.3.6.1.2.1',
      });
    } catch (e) { toast(`Could not read ${host}: ${e.message}`, { kind: 'error' }); done(); return; }
    done();

    const rows = answer?.rows || [];
    if (!rows.length) { toast(`${host} answered, but reported nothing under 1.3.6.1.2.1.`, { kind: 'error' }); return; }

    const already = new Set(cfg.snmpOids.map((o) => (o.oid || '').replace(/^\./, '')));
    const boxes = new Map();
    const table = h('table', { class: 'table' }, h('thead', null, h('tr', null,
      h('th', null, ''), h('th', null, 'OID'), h('th', null, 'Suggested name'), h('th', null, 'Type'), h('th', null, 'Value'))));
    const tb = h('tbody');
    for (const row of rows) {
      const have = already.has(row.oid);
      const box = h('input', { type: 'checkbox', disabled: have, 'aria-label': `Add ${row.name || row.oid}` });
      boxes.set(box, row);
      tb.append(h('tr', null,
        h('td', null, box),
        h('td', { class: 'mono' }, row.oid),
        h('td', null, row.name || h('span', { class: 'dim' }, '—'), have ? h('span', { class: 'tag' }, 'already added') : null),
        h('td', { class: 'dim' }, row.type),
        h('td', { class: 'mono truncate', style: { maxWidth: '260px' } }, row.value)));
    }
    table.append(tb);

    const filter = textInput({ placeholder: 'Filter by OID, name or value', 'aria-label': 'Filter readings', oninput: () => {
      const q = filter.value.trim().toLowerCase();
      for (const tr of tb.children) tr.hidden = q ? !tr.textContent.toLowerCase().includes(q) : false;
    } });

    const add = h('button', { class: 'btn btn-primary', type: 'button', onclick: () => {
      let added = 0;
      for (const [box, row] of boxes) {
        if (!box.checked) continue;
        const name = uniqueReadingName(cfg, row.name || row.oid);
        cfg.snmpOids.push({ oid: row.oid, name, kind: row.kind === 'counter' ? 'counter' : 'gauge', scale: 1, unit: '' });
        added++;
      }
      m.close();
      if (added) { onAdded(); toast(`Added ${added} reading${added === 1 ? '' : 's'}. Set the scale, unit and thresholds on each.`, { kind: 'success' }); }
    } }, 'Add ticked readings');

    const m = openModal({
      title: `What ${host} can tell us`,
      wide: true,
      body: h('div', { class: 'stack-sm' },
        h('p', { class: 'note' }, `${rows.length} reading${rows.length === 1 ? '' : 's'} under 1.3.6.1.2.1`, answer.truncated ? ` (stopped at the first ${answer.max} — narrow it down on the device if you need more)` : '', '. The last number of an interface row is its SNMP index, which is what the presets ask for. Ticked rows arrive as gauges or counters according to their type; the scale, unit and thresholds are yours to set.'),
        filter,
        h('div', { class: 'table-wrap', style: { maxHeight: '48vh', overflow: 'auto' } }, table)),
      footer: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, 'Cancel'), add],
    });
  }

  // Names identify a metric in charts, so a walked row cannot quietly take a
  // name another reading already has.
  function uniqueReadingName(cfg, base) {
    const taken = new Set((cfg.snmpOids || []).map((o) => (o.name || '').toLowerCase()));
    if (!taken.has(base.toLowerCase())) return base;
    for (let i = 2; i < 100; i++) {
      const candidate = `${base} (${i})`;
      if (!taken.has(candidate.toLowerCase())) return candidate;
    }
    return `${base} ${Date.now()}`;
  }

  /* ---------- Hardware health ---------- */

  function systemFields(c, err) {
    const cfg = c.config;
    const wrap = h('div', { class: 'stack' });

    const source = selectInput({
      options: [
        { value: 'local', label: 'This computer — the machine GWatch runs on' },
        { value: 'agent', label: 'A registered machine — it pushes its readings to GWatch' },
        { value: 'url', label: 'A metrics endpoint — GWatch reads it' },
      ],
      value: cfg.hostSource || 'local',
      onchange: () => { cfg.hostSource = source.value; renderSource(); },
    });

    const agentSel = selectInput({
      options: [{ value: '', label: 'Loading machines…' }],
      value: String(cfg.agentId || ''),
      onchange: () => { cfg.agentId = Number(agentSel.value) || 0; },
    });
    const agentRow = field({
      label: 'Machine', input: agentSel, error: err.agentId,
      help: 'Register machines under Settings › Hardware. The agent on that machine connects out to GWatch; GWatch never connects to it.',
    });
    loadAgents(agentSel, cfg);

    const urlIn = textInput({
      value: cfg.metricsUrl || '', placeholder: 'https://nas.lan:9713/metrics',
      oninput: () => { cfg.metricsUrl = urlIn.value; },
    });
    const tokenIn = textInput({
      type: 'password', value: cfg.metricsToken || '', placeholder: 'Bearer token for that endpoint', autocomplete: 'off',
      oninput: () => { cfg.metricsToken = tokenIn.value; },
    });
    const urlRow = h('div', { class: 'form-grid' },
      field({ label: 'Metrics URL', input: urlIn, error: err.metricsUrl, help: 'Where gwatch-agent is listening, when it is run with `serve` instead of pushing.' }),
      field({ label: 'Token', input: tokenIn, help: 'Sent as an Authorization header. It is stored in this check\u2019s configuration.' }));

    const sourceWrap = h('div');
    function renderSource() {
      clear(sourceWrap);
      const mode = cfg.hostSource || 'local';
      if (mode === 'agent') sourceWrap.append(agentRow);
      else if (mode === 'url') sourceWrap.append(urlRow);
      else sourceWrap.append(h('p', { class: 'note' }, 'Nothing to configure: GWatch already reads this computer every minute.'));
    }
    renderSource();

    const stale = numberInput({
      value: cfg.staleAfterSeconds || '', min: 0,
      placeholder: `Default (3 \u00d7 the interval, at least 60 s)`,
      oninput: () => { cfg.staleAfterSeconds = Number(stale.value) || 0; },
    });

    const mounts = chipInput({
      values: cfg.diskMounts || [], placeholder: 'e.g. / or C:',
      onChange: (v) => { cfg.diskMounts = v; },
    });

    if (!Array.isArray(cfg.metricThresholds)) cfg.metricThresholds = legacyMetricThresholds(cfg);
    const thresholdsEl = h('div', { class: 'stack-sm', 'aria-label': 'Hardware thresholds' });
    const renderThresholds = () => {
      clear(thresholdsEl);
      for (const f of METRIC_FAMILIES) {
        thresholdsEl.append(thresholdGroup(f, cfg, f.key === 'disk'
          ? field({ label: 'Watch only these mount points', input: mounts, help: 'Leave empty to watch every filesystem.' })
          : null));
      }
      thresholdsEl.append(instanceOverrides(cfg, renderThresholds));
    };
    renderThresholds();

    wrap.append(
      h('div', { class: 'form-grid' },
        field({ label: 'Read hardware from', input: source }),
        field({ label: 'Report down after no reading for', input: stale, help: 'A machine that stops reporting is the signal an agent exists to give.' })),
      sourceWrap,
      h('div', { class: 'section-title', style: { marginTop: '4px' } }, 'Warning and critical thresholds'),
      h('p', { class: 'note' }, 'One check, one line of history per metric, and each metric has its own verdict: crossing ', h('b', null, 'warning'), ' marks that metric — and so the check — ', h('b', null, 'degraded'), '; crossing ', h('b', null, 'critical'), ' marks it ', h('b', null, 'down'), '. A warning on the disk and a warning on memory are two incidents, not one. Leave a box blank to turn that level off.'),
      err.metricThresholds ? h('div', { class: 'error', role: 'alert' }, err.metricThresholds) : null,
      thresholdsEl,
    );
    return wrap;
  }

  // thresholdGroup is one metric family's warning/critical pair, boxed and
  // labelled so the two numbers that escalate together read as a unit rather
  // than as two rows in an unrelated grid, with the shipped default visible
  // in each field. `extra` adds a further field to the box, used for Disk's
  // mount-point filter.
  function thresholdGroup(f, cfg, extra) {
    const entry = familyEntry(cfg, f.key);
    const def = SYSTEM_DEFAULTS.metricThresholds.find((t) => t.metric === f.key) || {};
    return h('div', { class: 'threshold-group', 'data-metric': f.key, style: { border: '1px solid var(--line)', padding: '10px 12px 12px' } },
      h('div', { class: 'section-title', style: { marginBottom: '8px' } }, f.label),
      h('div', { class: 'form-grid' },
        levelField(entry, 'warn', 'Warning', f, def.warn),
        levelField(entry, 'crit', 'Critical', f, def.crit)),
      f.note ? h('p', { class: 'note', style: { marginTop: '6px' } }, f.note) : null,
      extra ? h('div', { style: { marginTop: '10px' } }, extra) : null);
  }

  // familyEntry finds, or creates, the list entry for a family, so the
  // family's boxes always have somewhere to write. A created entry with no
  // level is dropped again on save (see cleanMetricThresholds).
  function familyEntry(cfg, key) {
    let entry = cfg.metricThresholds.find((t) => t.metric === key);
    if (!entry) { entry = { metric: key }; cfg.metricThresholds.push(entry); }
    return entry;
  }

  function levelField(entry, level, label, f, def) {
    const unit = f.key === 'diskio' && (entry.metric || '').endsWith('.busy') ? '%' : f.unit;
    const input = numberInput({
      value: entry[level] ?? '', min: 0, max: unit === '%' ? 100 : undefined, step: f.step || 1,
      placeholder: def != null ? `Default ${def}${unit}` : 'Off',
      'aria-label': `${f.label}${entry.metric.includes(':') ? ' ' + entry.metric.slice(entry.metric.indexOf(':') + 1) : ''} ${label.toLowerCase()} threshold`,
      oninput: () => { entry[level] = input.value === '' ? undefined : Number(input.value); },
    });
    return field({ label, input: unit ? h('div', { class: 'input-with-unit' }, input, h('span', { class: 'unit' }, unit)) : input });
  }

  // instanceOverrides lists the thresholds that name one disk, interface or
  // device, each overriding its family's pair for that instance alone, and
  // offers a row for adding another.
  function instanceOverrides(cfg, rerender) {
    const rows = cfg.metricThresholds.filter((t) => (t.metric || '').includes(':'));
    const box = h('div', { class: 'stack-sm', style: { border: '1px solid var(--line)', padding: '10px 12px 12px' } },
      h('div', { class: 'section-title', style: { marginBottom: '4px' } }, 'One disk, interface or device'),
      h('p', { class: 'note' }, 'An entry here replaces the family’s pair above for that one instance: a media disk that is always 90% full, an uplink that is meant to run hot. Both boxes blank means that instance is never alerted on.'));
    for (const t of rows) {
      const family = METRIC_FAMILIES.find((f) => f.key === t.metric.slice(0, t.metric.indexOf(':'))) || { key: '', label: t.metric, unit: '' };
      box.append(h('div', { class: 'threshold-group threshold-instance', 'data-metric': t.metric },
        h('div', { class: 'row-between' },
          h('div', { class: 'small' }, h('b', null, family.label), ' ', h('code', null, t.metric.slice(t.metric.indexOf(':') + 1))),
          h('button', { class: 'btn btn-sm icon-btn btn-danger', type: 'button', 'aria-label': `Remove the ${t.metric} threshold`, title: 'Remove', onclick: () => {
            cfg.metricThresholds.splice(cfg.metricThresholds.indexOf(t), 1); rerender();
          } }, icon('trash'))),
        h('div', { class: 'form-grid' },
          levelField(t, 'warn', 'Warning', family),
          levelField(t, 'crit', 'Critical', family))));
    }
    const familySel = selectInput({ options: METRIC_FAMILIES.filter((f) => f.instances).map((f) => ({ value: f.key, label: f.label })), 'aria-label': 'Family of the instance threshold' });
    const instanceIn = textInput({ placeholder: 'e.g. /srv, eth0, sda', 'aria-label': 'Which disk, interface or device' });
    const addBtn = h('button', { class: 'btn btn-sm', type: 'button', onclick: () => {
      const instance = instanceIn.value.trim();
      if (!instance) { toast('Name the disk, interface or device first.', { kind: 'error' }); return; }
      const key = `${familySel.value}:${instance}`;
      if (cfg.metricThresholds.some((t) => t.metric === key)) { toast(`There is already a threshold for ${key}.`, { kind: 'error' }); return; }
      cfg.metricThresholds.push({ metric: key });
      rerender();
    } }, icon('plus'), 'Add a threshold for one disk / interface');
    box.append(h('div', { class: 'row', style: { gap: '8px', flexWrap: 'wrap', alignItems: 'end' } },
      field({ label: 'Family', input: familySel }),
      field({ label: 'Instance', input: instanceIn, help: METRIC_FAMILIES.find((f) => f.key === familySel.value)?.instances }),
      addBtn));
    return box;
  }

  // The machine list comes from the server, so a check cannot be pointed at a
  // machine that was never registered.
  async function loadAgents(select, cfg) {
    let agents = [];
    try { agents = await api.get('/api/agents'); } catch { agents = null; }
    clear(select);
    if (agents === null) {
      select.append(h('option', { value: '' }, 'Could not load the machine list'));
      return;
    }
    const usable = agents.filter((a) => !a.revokedAt);
    if (!usable.length) {
      select.append(h('option', { value: '' }, 'No machines registered yet — add one under Settings \u203a Hardware'));
      return;
    }
    select.append(h('option', { value: '' }, 'Choose a machine\u2026'));
    for (const a of usable) {
      select.append(h('option', { value: String(a.id) }, a.lastSeenAt ? `${a.name} (${a.hostname || 'reporting'})` : `${a.name} (not reporting yet)`));
    }
    select.value = String(cfg.agentId || '');
  }

  function alertOverrides(c) {
    const a = c.alerts || (c.alerts = {});
    const tri = (key) => selectInput({ options: TRI, value: a[key] == null ? '' : String(a[key]), onchange: (e) => { a[key] = e.target.value === '' ? null : e.target.value === 'true'; } });
    const cooldown = numberInput({ value: a.cooldownMinutes ?? '', min: 0, placeholder: `Default (${state.settings?.alerts?.cooldownMinutes ?? 60} min)`, oninput: () => { a.cooldownMinutes = cooldown.value === '' ? null : Number(cooldown.value); } });
    const recipients = chipInput({ values: a.recipients || [], placeholder: 'Add an email and press Enter', validate: (v) => /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(v), onChange: (v) => { a.recipients = v; } });
    const hasOverride = a.enabled != null || a.cooldownMinutes != null || a.notifyRecovery != null || a.notifyWarnings != null || a.recipients?.length;
    return h('details', { class: 'collapsible', open: !!hasOverride, style: { marginTop: '12px' } },
      h('summary', null, icon('chevronRight'), 'Alert overrides for this check', hasOverride ? h('span', { class: 'tag' }, 'customised') : null),
      // The fields, their labels and their order come from the shared table,
      // so the bulk editor's alert rows and this card cannot disagree.
      h('div', { class: 'form-grid-4', style: { paddingTop: '8px' } },
        ...ALERT_OVERRIDE_FIELDS.map((f) => {
          if (f.kind === 'tri-state') return field({ label: f.label, input: tri(f.key) });
          if (f.kind === 'chips') return field({ label: f.label, input: recipients, help: f.help, cls: 'span-2' });
          return field({ label: f.label, input: h('div', { class: 'input-with-unit' }, cooldown, h('span', { class: 'unit' }, f.unit)) });
        }),
      ));
  }

  /* ---------- Test check ---------- */
  async function testCheck(c, btn, card) {
    const done = busy(btn, 'Testing…');
    try {
      const result = await api.post('/api/checks/test', { check: cleanCheck(c), nodeHost: d.host });
      c._test = result;
      replace(card._testArea, h('div', { style: { marginTop: '16px' } }, h('div', { class: 'section-title' }, 'Test result'), resultInspector(result, c, { compact: true })));
      toast(result.success ? 'Test succeeded' : `Test failed: ${result.message || result.error || ''}`, { kind: result.success ? 'success' : 'error' });
    } catch (e) { toast(`Test failed: ${e.message}`, { kind: 'error' }); }
    done();
  }

  /* ---------- Validation / save ---------- */
  function validate() {
    const errors = { checks: {} };
    let count = 0;
    if (!d.name.trim()) { errors.name = 'Give the node a name.'; count++; }
    // A hardware check names a machine rather than an address, so a node made
    // only of those needs no host at all.
    const needsHost = d.checks.filter((c) => c.type !== 'system');
    const allHaveTarget = needsHost.length && needsHost.every((c) => (c.config.target || '').trim());
    if (needsHost.length && !d.host.trim() && !allHaveTarget) { errors.host = 'Enter a host, IP or URL (or set a target on every check).'; count++; }
    const minInt = state.settings?.general?.minIntervalSeconds || 10;
    for (const c of d.checks) {
      const e = {};
      if (!(c.name || '').trim()) e.name = 'Name this check.';
      if (!c.intervalSeconds || c.intervalSeconds < minInt) e.intervalSeconds = `At least ${minInt} seconds.`;
      if (!c.timeoutSeconds || c.timeoutSeconds < 1) e.timeoutSeconds = 'At least 1 second.';
      if (c.type === 'tcp' && (!c.config.port || c.config.port < 1 || c.config.port > 65535)) e.port = 'Port is required (1–65535).';
      if (c.type === 'cert' && c.config.port && (c.config.port < 1 || c.config.port > 65535)) e.port = 'Port must be 1–65535.';
      if (c.type === 'keyword' && !(c.config.keyword || '').trim()) e.keyword = 'Enter the text to look for.';
      if (c.type === 'json' && !(c.config.jsonPath || '').trim()) e.jsonPath = 'Enter a JSON path.';
      if (c.type === 'json') Object.assign(e, jsonRecordErrors(c.config));
      if (c.type === 'custom' && !(c.config.command || '').trim()) e.command = 'Enter a command to run.';
      if (c.type === 'system') {
        if (c.config.hostSource === 'agent' && !c.config.agentId) e.agentId = 'Choose which registered machine this check reads.';
        if (c.config.hostSource === 'url' && !(c.config.metricsUrl || '').trim()) e.metricsUrl = 'Enter the metrics URL to read.';
        const thresholdErr = metricThresholdErrors(c.config.metricThresholds);
        if (thresholdErr) e.metricThresholds = thresholdErr;
      }
      if (c.type === 'snmp') Object.assign(e, snmpErrors(c.config));
      if (['http', 'keyword', 'json'].includes(c.type) && c.config.target && !/^(https?:\/\/)?[^\s/]+/.test(c.config.target.trim())) e.target = 'Enter a valid URL.';
      if (Object.keys(e).length) { errors.checks[c._key] = e; count += Object.keys(e).length; }
    }
    state.errors = errors;
    nameField.setError(errors.name); hostField.setError(errors.host);
    return count;
  }

  async function save() {
    if (state.saving) return;
    const n = validate();
    renderChecks();
    if (n) { toast(`Please fix ${n} problem${n === 1 ? '' : 's'} before saving.`, { kind: 'error' }); root.querySelector('.has-error input, .error')?.scrollIntoView({ behavior: 'smooth', block: 'center' }); return; }
    if (!d.checks.length) { const ok = await confirmDialog({ title: 'Save without checks?', message: 'This node will not be monitored until you add a check.', confirmLabel: 'Save anyway' }); if (!ok) return; }
    state.saving = true;
    const groups = nodeGroups(d).map((g) => g.trim()).filter(Boolean);
    // group goes out as well as groups: it is the deprecated alias, and the
    // server derives it from the list anyway.
    const payload = { ...d, name: d.name.trim(), host: d.host.trim(), groups, group: groups[0] || '', checks: d.checks.map((c, i) => ({ ...cleanCheck(c), sortOrder: i })) };
    try {
      const saved = isNew ? await api.post('/api/nodes', payload) : await api.put(`/api/nodes/${d.id}`, payload);
      toast(isNew ? `${saved.name} added` : 'Changes saved', { kind: 'success' });
      ctx.navigate(`/nodes/${saved.id}`);
    } catch (e) { toast(e.message, { kind: 'error' }); state.saving = false; }
  }

  renderChecks();
  clear(root);
  root.append(h('div', { class: 'editor' }, nodeCard, checksCard,
    h('div', { class: 'sticky-actions' }, h('div', { class: 'card' },
      h('span', { class: 'muted small' }, isNew ? 'The node starts monitoring as soon as you create it.' : 'Changes apply immediately after saving.'),
      h('div', { class: 'btn-group' }, h('a', { class: 'btn', href: isNew ? '#/nodes' : `#/nodes/${d.id}` }, 'Cancel'), h('button', { class: 'btn btn-primary', type: 'button', onclick: save }, icon('save'), isNew ? 'Create node' : 'Save changes'))))));
  setTimeout(() => nameInput.focus(), 30);

  return { destroy() { state.destroyed = true; } };
}

// Node editor: create / edit a node and its checks, with inline check testing.

import { api } from '../api.js';
import { scrollBehavior, h, icon, clear, replace, field, textInput, numberInput, textarea, selectInput, checkbox, toggle, chipInput, toast, confirmDialog, openModal, emptyState, skeleton, CHECK_TYPES, checkTypeLabel, uid, busy } from '../components.js';
import { nodeGroups } from '../fmt.js';
import { resultInspector } from './inspector.js';
import { linkStyleEditor } from '../netmap.js';
import { INTERVALS, intervalOptions, TRI, IMPORTANCE_OPTIONS, PING_METHOD_OPTIONS, ALERT_OVERRIDE_FIELDS } from './node-fields.js';
import { METRIC_FAMILIES, SNMP_PRESETS, SNMP_THRESHOLD_KEYS, SYSTEM_DEFAULTS, legacyMetricThresholds, oidRow } from './node-editor/shared.js';
import { create as createSnmp } from './node-editor/snmp.js';
import { create as createMatter } from './node-editor/matter.js';
import { create as createSystem } from './node-editor/system.js';
export { METRIC_FAMILIES, legacyMetricThresholds } from './node-editor/shared.js';

// The interval choices, the three states of an alert override, the importance
// levels and the ping methods are shared with the bulk editor, so both screens
// offer the same words and the same values. See web/views/node-fields.js.

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
    // A Matter check cannot read anything until a device is picked, so it
    // starts empty and the form's Load devices button is the next step.
    case 'matter': base.config = { matterServer: '', matterNodeId: 0, matterReadings: [] }; base.name = 'Matter'; break;
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
  if (cfg.matterNodeId != null) cfg.matterNodeId = Number(cfg.matterNodeId) || 0;
  if (!cfg.matterNodeId) delete cfg.matterNodeId;
  if (cfg.matterServer != null) cfg.matterServer = String(cfg.matterServer).trim();
  if (!cfg.matterServer) delete cfg.matterServer;
  if (cfg.matterReadings) cfg.matterReadings = cfg.matterReadings.map(cleanMatterRow);
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

// matterErrors mirrors internal/checks.validateMatterCheck, beside the
// fields rather than after a round trip; the server is still the rule.
function matterErrors(cfg, host) {
  const e = {};
  const server = (cfg.matterServer || '').trim();
  if (server && !/^wss?:\/\/[^\s/]+/i.test(server)) e.matterServer = 'The address starts with ws:// (or wss://), e.g. ws://192.168.1.20:5580/ws.';
  if (!server && !(host || '').trim()) e.matterServer = 'Enter the Matter server address, or give the node the host it runs on.';
  if (!Number(cfg.matterNodeId)) e.matterNodeId = 'Choose the device with Load devices, or enter its node id.';
  const rows = cfg.matterReadings || [];
  if (!rows.length) { e.matterReadings = 'Add at least one reading.'; return e; }
  if (rows.length > 64) { e.matterReadings = 'A Matter check takes at most 64 readings. Split the rest into a second check.'; return e; }
  const names = new Set();
  for (const r of rows) {
    const name = (r.name || '').trim().toLowerCase();
    if (!name) { e.matterReadings = `The reading on endpoint ${r.endpoint} needs a name.`; return e; }
    if (names.has(name)) { e.matterReadings = `Two readings are both called "${r.name}" — names identify the metric in charts, so they must differ.`; return e; }
    names.add(name);
    if (r.kind === 'attribute' && !Number(r.cluster)) { e.matterReadings = `${r.name}: an "Other attribute" reading needs a cluster id.`; return e; }
    const num = (k) => (r[k] === '' || r[k] == null || isNaN(Number(r[k])) ? null : Number(r[k]));
    const [wa, ca, wb, cb] = SNMP_THRESHOLD_KEYS.map(num);
    if (wa != null && ca != null && wa >= ca) { e.matterReadings = `${r.name}: the critical "above" threshold must be above the warning one.`; return e; }
    if (wb != null && cb != null && wb <= cb) { e.matterReadings = `${r.name}: the critical "below" threshold must be below the warning one.`; return e; }
  }
  return e;
}

// cleanMatterRow gives a reading the types the server expects. A standard
// reading's cluster, attribute and scale are implied by its kind, so they are
// not sent; an unset threshold is absent rather than zero, as for an OID.
function cleanMatterRow(r) {
  const row = { endpoint: Math.max(0, Math.floor(Number(r.endpoint) || 0)), kind: r.kind || 'temperature', name: (r.name || '').trim() };
  if ((r.unit || '').trim()) row.unit = r.unit.trim();
  if (row.kind === 'attribute') {
    row.cluster = Math.max(0, Math.floor(Number(r.cluster) || 0));
    row.attribute = Math.max(0, Math.floor(Number(r.attribute) || 0));
    const scale = Number(r.scale);
    row.scale = isFinite(scale) && scale > 0 ? scale : 1;
  }
  for (const k of SNMP_THRESHOLD_KEYS) {
    if (r[k] === '' || r[k] == null || isNaN(Number(r[k]))) continue;
    row[k] = Number(r[k]);
  }
  return row;
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
  const state = { draft: null, nodes: [], agents: [], groups: { groups: [], tags: [] }, settings: null, errors: {}, destroyed: false, saving: false };
  root.append(skeleton({ lines: 6 }));

  // Load supporting data. The agents are for "Check from"; the list is an
  // administrator's, and anyone else simply sees "This server".
  const [nodes, groups, settings, agents] = await Promise.all([api.get('/api/nodes').catch(() => []), api.get('/api/groups').catch(() => ({ groups: [], tags: [] })), api.get('/api/settings').catch(() => null), api.get('/api/agents').catch(() => [])]);
  state.nodes = nodes || []; state.groups = groups || { groups: [], tags: [] }; state.settings = settings; state.agents = agents || [];

  if (isNew) {
    const tplId = ctx.query.get('template');
    let tpl = null;
    if (tplId) { try { const list = await api.get('/api/templates'); tpl = (list || []).find((t) => t.id === tplId) || null; } catch { tpl = null; } }
    state.draft = tpl
      ? { ...tpl.node, id: undefined, template: tpl.id, tags: [...(tpl.node.tags || [])], enabled: true, importance: tpl.node.importance || 'normal', checks: (tpl.checks || []).map((c) => ({ ...c, _key: uid('c'), id: undefined, config: { ...(c.config || {}) }, alerts: c.alerts ? { ...c.alerts } : null })) }
      : { name: '', host: '', groups: [], tags: [], notes: '', importance: 'normal', enabled: true, dependsOnNodeId: null, collectorId: null, template: '', checks: [] };
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
  const dependsSel = selectInput({ options: [{ value: '', label: 'None' }, ...state.nodes.filter((n) => String(n.id) !== String(d.id)).map((n) => ({ value: n.id, label: `${n.name} (${n.host})` }))], value: d.dependsOnNodeId ?? '', onchange: () => { d.dependsOnNodeId = dependsSel.value ? Number(dependsSel.value) : null; syncLinkStyle(); } });
  // How the line to that parent looks on the network maps. It is the style of
  // the link, so it is only there while there is one.
  const linkStyle = linkStyleEditor({ value: d.linkStyle, onChange: (v) => { d.linkStyle = v; } });
  const linkStyleField = h('div', { class: 'span-2' }, linkStyle);
  function syncLinkStyle() {
    const parent = state.nodes.find((n) => String(n.id) === String(d.dependsOnNodeId));
    linkStyleField.hidden = !parent;
    if (!parent) d.linkStyle = null;
    linkStyle.setParentName(parent?.name || '');
  }
  syncLinkStyle();

  // Where the node's network checks run from: this server, or a paired agent
  // acting as a remote collector (#123). Script, hardware and Matter checks
  // run here whatever this says. An agent whose collecting is not switched on
  // is still offered (so the node can be set up first) and says so.
  const collectorOptions = [{ value: '', label: 'This server' }, ...state.agents
    .filter((a) => !a.revokedAt || String(a.id) === String(d.collectorId))
    .map((a) => ({ value: a.id, label: `${a.name} (remote collector${a.collector ? (a.collectorAllowed ? '' : ' — not yet allowed on the machine') : ' — collecting is off'})` }))];
  if (d.collectorId && !collectorOptions.some((o) => String(o.value) === String(d.collectorId))) collectorOptions.push({ value: d.collectorId, label: `Agent ${d.collectorId}` });
  const collectorNote = h('div', { class: 'note small', 'data-collector-note': '' });
  const collectorSel = selectInput({ options: collectorOptions, value: d.collectorId ?? '', onchange: () => { d.collectorId = collectorSel.value ? Number(collectorSel.value) : null; syncCollector(); } });
  function syncCollector() {
    const a = state.agents.find((x) => String(x.id) === String(d.collectorId));
    collectorNote.textContent = !d.collectorId ? ''
      : !a ? 'This agent is not in the list GWatch can see.'
        : !a.collector ? `Collecting is off for ${a.name}: turn it on in its agent card, or these checks stay unknown.`
          : !a.collectorAllowed ? `${a.name} has not allowed collecting in its own agent.json ("collector": true) yet; until it does, these checks stay unknown.`
            : `Ping, TCP, HTTP, DNS and SNMP checks run from ${a.name}; other checks still run here. "Test this check" tests from this server.`;
  }
  syncCollector();

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
      linkStyleField,
      field({ label: 'Check from', input: h('div', { class: 'stack-sm' }, collectorSel, collectorNote), help: 'Where this node’s network checks run: this server, or a paired agent at another site acting as a remote collector.' }),
      field({ label: 'Notes', input: notesInput, cls: 'span-2' }),
      h('div', { class: 'span-2' }, enabledToggle),
    ));

  /* ---------- Checks ---------- */
  const checksList = h('div', { class: 'stack' });
  const checksCard = h('section', { 'aria-label': 'Checks' },
    h('div', { class: 'row-between u-mb-14' }, h('h2', null, 'Checks'), h('button', { class: 'btn', type: 'button', onclick: addCheck }, icon('plus'), 'Add check')),
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
    for (const t of CHECK_TYPES) picker.append(h('button', { type: 'button', role: 'listitem', onclick: () => { d.checks.push(defaultCheck(t.value, state.settings)); m.close(); renderChecks(); setTimeout(() => { const cards = checksList.querySelectorAll('.editor-check'); const last = cards[cards.length - 1]; last?.scrollIntoView({ behavior: scrollBehavior(), block: 'start' }); last?.querySelector('input')?.focus(); }, 30); } }, h('b', null, t.label), h('span', null, t.desc)));
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
        h('button', { class: 'btn btn-sm icon-btn', type: 'button', 'aria-label': 'Duplicate check', title: 'Duplicate', onclick: () => { const copy = { ...c, _key: uid('c'), id: undefined, name: `${c.name} (copy)`, config: JSON.parse(JSON.stringify(c.config), (key, value) => value === '********' ? '' : value), alerts: c.alerts ? { ...c.alerts } : null }; d.checks.splice(index + 1, 0, copy); renderChecks(); } }, icon('copy')),
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
      field({ label: 'Interval', input: h('div', { class: 'stack-sm u-gap-6' }, intervalSel, customIn), error: err.intervalSeconds }),
      field({ label: 'Timeout', input: h('div', { class: 'input-with-unit' }, timeoutIn, h('span', { class: 'unit' }, 'seconds')), error: err.timeoutSeconds }),
      field({ label: 'Retries', input: retriesIn, help: 'Immediate retries inside one run before counting a failure.' }),
      field({ label: 'Failures before down', input: thresholdIn, help: 'Consecutive failed runs before the check is marked down and alerts fire.' }),
    ));

    // Type-specific
    card.append(h('div', { class: 'section-title', style: { marginTop: '22px' } }, `${checkTypeLabel(c.type)} settings`));
    card.append(typeFields(c, err));

    // Check groups (#36): folded away unless the check already has some, so
    // the common case — a check that is in no group of its own — looks as
    // it always has.
    const checkGroupsIn = chipInput({ values: c.config.groups || [], placeholder: 'Add a check group and press Enter', suggestions: (state.groups.checkGroups || []).map((g) => g.name), onChange: (v) => { c.config.groups = v; } });
    card.append(h('details', { class: 'collapsible u-mt-12', open: !!(c.config.groups || []).length },
      h('summary', null, icon('chevronRight'), 'Check groups', (c.config.groups || []).length ? h('span', { class: 'sub' }, ` · ${c.config.groups.join(', ')}`) : null),
      field({ label: 'Groups for this check', input: checkGroupsIn, help: 'Optional, and separate from the node’s groups: put checks from different nodes together (“HTTPS certificates”, “Plex ports”) to filter, chart and bulk-edit them as a set.' })));

    // Alert overrides
    card.append(alertOverrides(c));

    // Test result area
    const testArea = h('div', { class: 'test-area' });
    if (c._test) testArea.append(h('div', { class: 'u-mt-16' }, h('div', { class: 'section-title' }, 'Test result'), resultInspector(c._test, c, { compact: true })));
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
        // Removing takes the row away and reads the rest back, rather than
        // deleting `k`: the name may have been edited since this row was drawn.
        const row = h('div', { class: 'kv-row' }, kIn, vIn, h('button', { class: 'btn btn-sm icon-btn', type: 'button', 'aria-label': 'Remove header', onclick: () => { row.remove(); commit(); renderHeaders(); } }, icon('x')));
        headersWrap.append(row);
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
    if (withToggle) items.push(field({ label: 'Certificate', input: h('div', { class: 'u-pt-6' }, checkbox({ label: 'Also check the HTTPS certificate', checked: cfg.certCheck !== false, onChange: (v) => { cfg.certCheck = v; } })) }));
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
      h('div', { class: 'stack-sm u-pt-8' },
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
    const details = h('div', { class: 'stack-sm u-pt-8' });
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
      h('div', { class: 'u-pt-6' }, record),
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
        return h('div', { class: 'stack' }, h('div', { class: 'form-grid' }, field({ label: 'Keyword', input: kw, error: err.keyword, help: 'Case-sensitive text that must appear in the response body.' }), field({ label: 'Mode', input: h('div', { class: 'u-pt-6' }, absent) })), httpCommon(c, err), h('div', { class: 'form-grid-3' }, ...warnFields(c)));
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
            h('div', { class: 'stack-sm u-pt-8' },
              h('p', { class: 'note' }, 'Exit code 0 = up, 2 = degraded, anything else = down. The check is killed and reported "timed out" if it runs past the timeout above.'),
              h('p', { class: 'note' }, 'Stdout may contain lines of the form ', h('code', null, 'key=value'), ' (case-insensitive keys): ', h('code', null, 'status=up|degraded|down'), ' overrides the exit code, ', h('code', null, 'message=...'), ' is shown as the result message, ', h('code', null, 'latency_ms=<number>'), ' sets the charted latency, and ', h('code', null, 'error=...'), ' sets the error text. Any other output (stdout and stderr, up to 8 KiB) is kept and shown with the result.'),
            )),
        );
      }
      case 'system': return systemFields(c, err);
      case 'snmp': return snmpFields(c, err);
      case 'matter': return matterFields(c, err);
      default: return h('p', { class: 'note' }, 'No settings for this type.');
    }
  }

  function alertOverrides(c) {
    const a = c.alerts || (c.alerts = {});
    const tri = (key) => selectInput({ options: TRI, value: a[key] == null ? '' : String(a[key]), onchange: (e) => { a[key] = e.target.value === '' ? null : e.target.value === 'true'; } });
    const cooldown = numberInput({ value: a.cooldownMinutes ?? '', min: 0, placeholder: `Default (${state.settings?.alerts?.cooldownMinutes ?? 60} min)`, oninput: () => { a.cooldownMinutes = cooldown.value === '' ? null : Number(cooldown.value); } });
    const recipients = chipInput({ values: a.recipients || [], placeholder: 'Add an email and press Enter', validate: (v) => /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(v), onChange: (v) => { a.recipients = v; } });
    const hasOverride = a.enabled != null || a.cooldownMinutes != null || a.notifyRecovery != null || a.notifyWarnings != null || a.recipients?.length;
    return h('details', { class: 'collapsible u-mt-12', open: !!hasOverride },
      h('summary', null, icon('chevronRight'), 'Alert overrides for this check', hasOverride ? h('span', { class: 'tag' }, 'customised') : null),
      // The fields, their labels and their order come from the shared table,
      // so the bulk editor's alert rows and this card cannot disagree.
      h('div', { class: 'form-grid-4 u-pt-8' },
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
      replace(card._testArea, h('div', { class: 'u-mt-16' }, h('div', { class: 'section-title' }, 'Test result'), resultInspector(result, c, { compact: true })));
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
    // A Matter check with its own server address does not need one either.
    const needsHost = d.checks.filter((c) => c.type !== 'system' && !(c.type === 'matter' && (c.config.matterServer || '').trim()));
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
      if (c.type === 'matter') Object.assign(e, matterErrors(c.config, d.host));
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
    if (n) { toast(`Please fix ${n} problem${n === 1 ? '' : 's'} before saving.`, { kind: 'error' }); root.querySelector('.has-error input, .error')?.scrollIntoView({ behavior: scrollBehavior(), block: 'center' }); return; }
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

  // The parts of this view (#118) are created with the bindings they read.
  const shell = { agents, d, nodes, targetField };
  const { snmpFields } = Object.assign(shell, createSnmp(shell));
  const { matterFields } = Object.assign(shell, createMatter(shell));
  const { systemFields } = Object.assign(shell, createSystem(shell));
  // ?add=snmp (from a node's page, or the network device template's prompt)
  // opens the editor with an SNMP check already added and in view.
  const addType = ctx.query.get('add');
  let focusAdded = null;
  if (addType && CHECK_TYPES.some((t) => t.value === addType) && !(addType === 'snmp' && d.checks.some((c) => c.type === 'snmp'))) {
    const c = defaultCheck(addType, state.settings);
    d.checks.push(c); focusAdded = c._key;
  }
  renderChecks();
  clear(root);
  root.append(h('div', { class: 'editor' }, nodeCard, checksCard,
    h('div', { class: 'sticky-actions' }, h('div', { class: 'card' },
      h('span', { class: 'muted small' }, isNew ? 'The node starts monitoring as soon as you create it.' : 'Changes apply immediately after saving.'),
      h('div', { class: 'btn-group' }, h('a', { class: 'btn', href: isNew ? '#/nodes' : `#/nodes/${d.id}` }, 'Cancel'), h('button', { class: 'btn btn-primary', type: 'button', onclick: save }, icon('save'), isNew ? 'Create node' : 'Save changes'))))));
  setTimeout(() => {
    const cards = focusAdded ? checksList.querySelectorAll('.editor-check') : [];
    const card = cards[cards.length - 1];
    if (card) { card.scrollIntoView({ block: 'start' }); card.querySelector('input')?.focus(); } else nameInput.focus();
  }, 30);

  return { destroy() { state.destroyed = true; } };
}


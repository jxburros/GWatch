// Node editor: create / edit a node and its checks, with inline check testing.

import { api } from '../api.js';
import { h, icon, clear, replace, field, textInput, numberInput, textarea, selectInput, checkbox, toggle, chipInput, toast, confirmDialog, openModal, emptyState, skeleton, CHECK_TYPES, checkTypeLabel, uid, busy } from '../components.js';
import { interval as fmtInterval, nodeGroups } from '../fmt.js';
import { resultInspector } from './inspector.js';

const INTERVALS = [30, 60, 120, 300, 600, 900, 1800, 3600];
const TRI = [{ value: '', label: 'Use global default' }, { value: 'true', label: 'Yes' }, { value: 'false', label: 'No' }];

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
    case 'json': base.config = { method: 'GET', jsonPath: '', jsonExpected: '' }; break;
    case 'custom': base.config = { command: '', workDir: '', env: {} }; base.name = 'Custom script'; break;
    case 'system': base.config = { ...SYSTEM_DEFAULTS, hostSource: 'local' }; base.name = 'Hardware health'; break;
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

// The hardware thresholds a new check starts with. They mirror
// model.SystemDefaults on the server, which is what a check saved without
// thresholds is given; keeping them here too means the editor shows the
// numbers the check will use rather than a row of zeros.
const SYSTEM_DEFAULTS = {
  cpuWarnPct: 90, memWarnPct: 90, memCritPct: 97, swapWarnPct: 50,
  diskWarnPct: 85, diskCritPct: 95, loadWarnPerCore: 2,
};

// Warning/critical pairs, for the rule that a critical threshold cannot sit
// below the warning it is supposed to escalate.
const THRESHOLD_PAIRS = [
  ['cpuWarnPct', 'cpuCritPct', 'processor'],
  ['memWarnPct', 'memCritPct', 'memory'],
  ['diskWarnPct', 'diskCritPct', 'disk'],
  ['loadWarnPerCore', 'loadCritPerCore', 'load per core'],
];

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
  const importanceSel = selectInput({ options: [{ value: 'low', label: 'Low' }, { value: 'normal', label: 'Normal' }, { value: 'high', label: 'High' }, { value: 'critical', label: 'Critical' }], value: d.importance || 'normal', onchange: () => { d.importance = importanceSel.value; } });
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
    const intervalSel = selectInput({ options: [...INTERVALS.map((s) => ({ value: s, label: `Every ${fmtInterval(s)}` })), { value: 'custom', label: 'Custom…' }], value: custom ? 'custom' : c.intervalSeconds, onchange: () => { if (intervalSel.value === 'custom') { customIn.hidden = false; customIn.focus(); } else { customIn.hidden = true; c.intervalSeconds = Number(intervalSel.value); } } });
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

  function typeFields(c, err) {
    const cfg = c.config;
    switch (c.type) {
      case 'ping': {
        const count = numberInput({ value: cfg.pingCount || 4, min: 1, max: 20, oninput: () => { cfg.pingCount = Number(count.value); } });
        return h('div', { class: 'form-grid-3' }, targetField(c, err, 'Host override'), field({ label: 'Packets per run', input: count }), ...warnFields(c, { loss: true }));
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
        return h('div', { class: 'stack' }, h('div', { class: 'form-grid' }, field({ label: 'JSON path', input: path, error: err.jsonPath, help: 'Dotted path into the response. Arrays use [index].' }), field({ label: 'Expected value', input: exp })), httpCommon(c, err), h('div', { class: 'form-grid-3' }, ...warnFields(c)));
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
      default: return h('p', { class: 'note' }, 'No settings for this type.');
    }
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

    wrap.append(
      h('div', { class: 'form-grid' },
        field({ label: 'Read hardware from', input: source }),
        field({ label: 'Report down after no reading for', input: stale, help: 'A machine that stops reporting is the signal an agent exists to give.' })),
      sourceWrap,
      h('div', { class: 'section-title', style: { marginTop: '4px' } }, 'Warning and critical thresholds'),
      h('p', { class: 'note' }, 'One check, one history line per metric, each with its own pair of thresholds below: crossing ', h('b', null, 'warning'), ' marks the check ', h('b', null, 'degraded'), '; crossing ', h('b', null, 'critical'), ' marks it ', h('b', null, 'down'), '. Filled in below at the defaults GWatch ships with — leave a threshold at 0 to turn it off.'),
      h('div', { class: 'stack-sm' },
        thresholdGroup('Processor', cfg, err, 'cpuWarnPct', 'cpuCritPct'),
        thresholdGroup('Memory', cfg, err, 'memWarnPct', 'memCritPct'),
        thresholdGroup('Swap', cfg, err, 'swapWarnPct', null,
          'Swap has a warning only — heavy swapping can degrade this check, but never marks it down by itself.'),
        thresholdGroup('Disk', cfg, err, 'diskWarnPct', 'diskCritPct', null,
          field({ label: 'Watch only these mount points', input: mounts, help: 'Leave empty to watch every filesystem.' }))),
      h('details', { class: 'collapsible' },
        h('summary', null, icon('chevronRight'), 'Load average (used when processor use cannot be read)'),
        h('div', { class: 'stack-sm', style: { paddingTop: '8px' } },
          h('p', { class: 'note' }, 'Load per core is processor demand divided by the number of cores, so it means the same thing on a 2-core box and a 64-core one. On macOS, where processor utilisation is not readable without a native extension, this is what the check watches instead of processor use above.'),
          thresholdGroup('Load per core', cfg, err, 'loadWarnPerCore', 'loadCritPerCore', null, null, { unit: '', step: 0.1 }))),
    );
    return wrap;
  }

  // thresholdGroup is one metric's warning/critical pair, boxed and labelled
  // so the two numbers that escalate together read as a unit rather than as
  // two rows in an unrelated grid, with the shipped default visible in each
  // field even when it is 0 (off). `critKey` is null for a metric that only
  // ever warns (see Swap, above); `extra` adds a further field to the box,
  // used for Disk's mount-point filter.
  function thresholdGroup(label, cfg, err, warnKey, critKey, note, extra, { unit = '%', step = 1 } = {}) {
    const fields = [pctField(cfg, warnKey, 'Warning', null, { unit, step })];
    if (critKey) fields.push(pctField(cfg, critKey, 'Critical', err[critKey], { unit, step }));
    return h('div', { style: { border: '1px solid var(--line)', padding: '10px 12px 12px' } },
      h('div', { class: 'section-title', style: { marginBottom: '8px' } }, label),
      h('div', { class: 'form-grid' }, ...fields),
      note ? h('p', { class: 'note', style: { marginTop: '6px' } }, note) : null,
      extra ? h('div', { style: { marginTop: '10px' } }, extra) : null);
  }

  function pctField(cfg, key, label, error, { unit = '%', step = 1 } = {}) {
    const def = SYSTEM_DEFAULTS[key];
    const input = numberInput({
      value: cfg[key] ?? '', min: 0, max: unit === '%' ? 100 : undefined, step,
      placeholder: def != null ? `Default (${def}${unit})` : 'Off',
      oninput: () => { cfg[key] = Number(input.value) || 0; },
    });
    return field({ label, input: unit ? h('div', { class: 'input-with-unit' }, input, h('span', { class: 'unit' }, unit)) : input, error });
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
      h('div', { class: 'form-grid-4', style: { paddingTop: '8px' } },
        field({ label: 'Send alerts', input: tri('enabled') }),
        field({ label: 'Cooldown', input: h('div', { class: 'input-with-unit' }, cooldown, h('span', { class: 'unit' }, 'min')) }),
        field({ label: 'Notify on recovery', input: tri('notifyRecovery') }),
        field({ label: 'Notify on warnings', input: tri('notifyWarnings') }),
        field({ label: 'Recipients', input: recipients, help: 'Replaces the global recipients for this check when set.', cls: 'span-2' }),
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
      if (c.type === 'custom' && !(c.config.command || '').trim()) e.command = 'Enter a command to run.';
      if (c.type === 'system') {
        if (c.config.hostSource === 'agent' && !c.config.agentId) e.agentId = 'Choose which registered machine this check reads.';
        if (c.config.hostSource === 'url' && !(c.config.metricsUrl || '').trim()) e.metricsUrl = 'Enter the metrics URL to read.';
        for (const [warnKey, critKey, label] of THRESHOLD_PAIRS) {
          const warn = Number(c.config[warnKey]) || 0;
          const crit = Number(c.config[critKey]) || 0;
          if (warn > 0 && crit > 0 && crit < warn) e[critKey] = `The ${label} critical threshold must be at or above its warning threshold.`;
        }
      }
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

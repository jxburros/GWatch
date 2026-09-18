// Node editor: create / edit a node and its checks, with inline check testing.

import { api } from '../api.js';
import { h, icon, clear, replace, field, textInput, numberInput, textarea, selectInput, checkbox, toggle, chipInput, toast, confirmDialog, openModal, emptyState, skeleton, CHECK_TYPES, checkTypeLabel, uid, busy } from '../components.js';
import { interval as fmtInterval } from '../fmt.js';
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
      : { name: '', host: '', group: '', tags: [], notes: '', importance: 'normal', enabled: true, dependsOnNodeId: null, template: '', checks: [] };
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
  const groupInput = textInput({ value: d.group || '', placeholder: 'e.g. Home Network', list: 'group-list', oninput: () => { d.group = groupInput.value; } });
  const groupList = h('datalist', { id: 'group-list' }, state.groups.groups.map((g) => h('option', { value: g.name })));
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
      field({ label: 'Group', input: h('div', null, groupInput, groupList), help: 'Used for dashboard filters, maintenance windows and the wallboard.' }),
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
      default: return h('p', { class: 'note' }, 'No settings for this type.');
    }
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
    const allHaveTarget = d.checks.length && d.checks.every((c) => (c.config.target || '').trim());
    if (!d.host.trim() && !allHaveTarget) { errors.host = 'Enter a host, IP or URL (or set a target on every check).'; count++; }
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
    const payload = { ...d, name: d.name.trim(), host: d.host.trim(), group: (d.group || '').trim(), checks: d.checks.map((c, i) => ({ ...cleanCheck(c), sortOrder: i })) };
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

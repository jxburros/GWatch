// Dashboard: switchable dashboards made of widgets on a 4-column grid.

import { api, getHistoryMulti, getHistoryAuto, qs } from '../api.js';
import { h, icon, clear, replace, statusPill, statusGlyph, checkChip, toast, confirmDialog, promptDialog, openModal, menuButton, showMenu, field, textInput, numberInput, selectInput, checkbox, checkMultiSelect, emptyState, skeleton, eventRow, rangeChips, statusMeta, uid } from '../components.js';
import { LineChart, toSeries, uptimeBar, uptimeLegend, SERIES_COLORS } from '../charts.js';
import { relTime, ms as fmtMs, pct, bytes, plural, dateShort, rangeLabel, duration } from '../fmt.js';

export const WIDGET_TYPES = [
  { type: 'summary', label: 'Overall health', desc: 'Big up / degraded / down / unknown numerals with a headline.', w: 2, h: 1, config: {} },
  { type: 'groups', label: 'Group status', desc: 'One card per group with its worst status.', w: 2, h: 1, config: { groups: [] } },
  { type: 'status_list', label: 'Status list', desc: 'Nodes and their checks, optionally filtered by group, tag or node.', w: 2, h: 2, config: { group: '', tag: '', nodeIds: [] } },
  { type: 'latency_chart', label: 'Latency chart', desc: 'Ping / connect latency over time for selected checks.', w: 2, h: 2, config: { checkIds: [], range: '24h', metric: 'avg' } },
  { type: 'response_chart', label: 'Response-time chart', desc: 'HTTP response time over time for selected checks.', w: 2, h: 2, config: { checkIds: [], range: '24h', metric: 'avg' } },
  { type: 'loss_chart', label: 'Packet-loss chart', desc: 'Packet loss % over time for ping checks.', w: 2, h: 2, config: { checkIds: [], range: '24h' } },
  { type: 'uptime_chart', label: 'Uptime', desc: 'Availability per period as a coloured bar, per check.', w: 2, h: 2, config: { checkIds: [], range: '7d' } },
  { type: 'incidents', label: 'Recent incidents', desc: 'The latest outages and recoveries.', w: 2, h: 2, config: { limit: 10 } },
  { type: 'cert_warnings', label: 'Certificate warnings', desc: 'Certificates that expire soon or are invalid.', w: 1, h: 1, config: {} },
  { type: 'attention', label: 'Needs attention', desc: 'Everything that is currently down or degraded, with dependency context.', w: 2, h: 1, config: {} },
  { type: 'monitor_health', label: 'Monitor health', desc: 'Is the GWatch service itself running and checking?', w: 1, h: 1, config: {} },
  { type: 'table', label: 'Node table', desc: 'A table of nodes for a group or tag.', w: 4, h: 2, config: { group: '', tag: '' } },
];
const CHART_TYPES = new Set(['latency_chart', 'response_chart', 'loss_chart']);

export function widgetConfig(w) {
  let c = w.config;
  if (typeof c === 'string') { try { c = JSON.parse(c); } catch { c = {}; } }
  return c && typeof c === 'object' ? c : {};
}
function widgetMeta(type) { return WIDGET_TYPES.find((x) => x.type === type) || { type, label: type, desc: '', w: 2, h: 1, config: {} }; }

export async function mount(root, ctx) {
  const state = {
    dashboards: [], current: null, overview: null, health: null, nodes: [], groups: { groups: [], tags: [] },
    editing: false, draft: null, chartHosts: new Map(), charts: new Map(), historyCache: new Map(), destroyed: false,
  };
  const now = () => Date.now();

  const grid = h('div', { class: 'dash-grid' });
  const tabs = h('div', { class: 'dash-tabs', role: 'tablist', 'aria-label': 'Dashboards' });
  root.append(tabs, grid);

  /* ---------- Data ---------- */
  async function loadAll() {
    const [dashboards, overview, health, nodes, groups] = await Promise.all([
      api.get('/api/dashboards'), api.get('/api/overview'), api.get('/api/health').catch(() => null), api.get('/api/nodes').catch(() => []), api.get('/api/groups').catch(() => ({ groups: [], tags: [] })),
    ]);
    state.dashboards = dashboards || [];
    state.overview = overview; state.health = health; state.nodes = nodes || []; state.groups = groups || { groups: [], tags: [] };
    pickCurrent(ctx.params.id);
  }
  function pickCurrent(id) {
    const list = state.dashboards;
    state.current = (id && list.find((d) => String(d.id) === String(id))) || list[0] || null;
  }
  async function refreshData() {
    const [overview, health] = await Promise.all([api.get('/api/overview'), api.get('/api/health').catch(() => state.health)]);
    if (state.destroyed) return;
    state.overview = overview; state.health = health;
    state.historyCache.clear();
    renderWidgets();
  }

  /* ---------- Title / actions ---------- */
  function setTitle() {
    const d = state.current;
    const actions = [];
    if (state.editing) {
      actions.push(
        h('button', { class: 'btn', type: 'button', onclick: () => addWidget() }, icon('plus'), 'Add widget'),
        h('button', { class: 'btn btn-ghost', type: 'button', onclick: () => cancelEdit() }, 'Cancel'),
        h('button', { class: 'btn btn-primary', type: 'button', onclick: () => saveEdit() }, icon('check'), 'Done'),
      );
    } else if (d) {
      actions.push(
        h('button', { class: 'btn', type: 'button', onclick: () => addWidget() }, icon('plus'), 'Add widget'),
        h('button', { class: 'btn btn-primary', type: 'button', onclick: () => startEdit() }, icon('edit'), 'Edit layout'),
        menuButton(() => [
          { label: 'Rename dashboard', icon: 'edit', onClick: renameDashboard },
          { label: 'New dashboard', icon: 'plus', onClick: newDashboard },
          { sep: true },
          { label: 'Delete dashboard', icon: 'trash', danger: true, onClick: deleteDashboard, disabled: state.dashboards.length <= 1 },
        ], { label: 'Dashboard options' }),
      );
    } else {
      actions.push(h('button', { class: 'btn btn-primary', type: 'button', onclick: newDashboard }, icon('plus'), 'New dashboard'));
    }
    ctx.setTitle(d ? d.name : 'Dashboard', { subtitle: state.editing ? 'Editing layout — use the arrows to reorder, then press Done' : null, actions });
  }

  function renderTabs() {
    clear(tabs);
    for (const d of state.dashboards) {
      const active = state.current && d.id === state.current.id;
      tabs.append(h('a', { class: `chip ${active ? 'active' : ''}`, role: 'tab', 'aria-selected': active ? 'true' : 'false', href: `#/dashboard/${d.id}` }, d.name));
    }
    tabs.append(h('button', { class: 'chip', type: 'button', title: 'New dashboard', onclick: newDashboard }, icon('plus'), 'New'));
  }

  /* ---------- Dashboard CRUD ---------- */
  async function newDashboard() {
    const name = await promptDialog({ title: 'New dashboard', label: 'Name', placeholder: 'e.g. Servers, Internet, Critical', confirmLabel: 'Create' });
    if (!name) return;
    try {
      const created = await api.post('/api/dashboards', { name: name.trim(), widgets: [{ id: uid('w'), type: 'summary', title: 'Overall health', width: 2, height: 1, config: {} }, { id: uid('w'), type: 'attention', title: 'Needs attention', width: 2, height: 1, config: {} }] });
      toast(`Dashboard "${created.name}" created`, { kind: 'success' });
      state.dashboards.push(created);
      ctx.navigate(`/dashboard/${created.id}`);
    } catch (e) { toast(e.message, { kind: 'error' }); }
  }
  async function renameDashboard() {
    const d = state.current; if (!d) return;
    const name = await promptDialog({ title: 'Rename dashboard', label: 'Name', value: d.name });
    if (!name || name.trim() === d.name) return;
    try {
      const saved = await api.put(`/api/dashboards/${d.id}`, { ...d, name: name.trim() });
      Object.assign(d, saved); setTitle(); renderTabs(); toast('Dashboard renamed', { kind: 'success' });
    } catch (e) { toast(e.message, { kind: 'error' }); }
  }
  async function deleteDashboard() {
    const d = state.current; if (!d) return;
    const ok = await confirmDialog({ title: `Delete "${d.name}"?`, message: 'The dashboard layout will be removed. Your nodes and history are not affected.', confirmLabel: 'Delete', danger: true });
    if (!ok) return;
    try {
      await api.del(`/api/dashboards/${d.id}`);
      state.dashboards = state.dashboards.filter((x) => x.id !== d.id);
      toast('Dashboard deleted', { kind: 'success' });
      ctx.navigate(state.dashboards[0] ? `/dashboard/${state.dashboards[0].id}` : '/dashboard');
      if (!state.dashboards[0]) { state.current = null; render(); }
    } catch (e) { toast(e.message, { kind: 'error' }); }
  }
  async function persistWidgets(widgets, { silent = false } = {}) {
    const d = state.current; if (!d) return;
    try {
      const saved = await api.put(`/api/dashboards/${d.id}`, { ...d, widgets });
      Object.assign(d, saved);
      if (!silent) toast('Layout saved', { kind: 'success' });
    } catch (e) { toast(e.message, { kind: 'error' }); throw e; }
  }

  /* ---------- Edit mode ---------- */
  function widgetsList() { return state.editing ? state.draft : (state.current?.widgets || []); }
  function startEdit() { state.editing = true; state.draft = (state.current?.widgets || []).map((w) => ({ ...w, config: { ...widgetConfig(w) } })); setTitle(); renderWidgets(); }
  function cancelEdit() { state.editing = false; state.draft = null; setTitle(); renderWidgets(); }
  async function saveEdit() {
    const widgets = state.draft;
    state.editing = false; state.draft = null;
    setTitle();
    await persistWidgets(widgets).catch(() => {});
    renderWidgets();
  }
  function moveWidget(i, dir) {
    const list = widgetsList(); const j = i + dir;
    if (j < 0 || j >= list.length) return;
    [list[i], list[j]] = [list[j], list[i]];
    if (!state.editing) persistWidgets(list, { silent: true }).catch(() => {});
    renderWidgets();
  }
  async function removeWidget(i) {
    const list = widgetsList();
    const w = list[i];
    if (!state.editing) {
      const ok = await confirmDialog({ title: `Remove "${w.title || widgetMeta(w.type).label}"?`, confirmLabel: 'Remove', danger: true });
      if (!ok) return;
    }
    list.splice(i, 1);
    disposeChart(w.id);
    if (!state.editing) await persistWidgets(list, { silent: true }).catch(() => {});
    renderWidgets();
  }
  async function editWidget(i) {
    const list = widgetsList();
    const edited = await openWidgetEditor(list[i], state);
    if (!edited) return;
    list[i] = edited;
    disposeChart(edited.id);
    if (!state.editing) await persistWidgets(list, { silent: true }).catch(() => {});
    renderWidgets();
  }
  async function addWidget() {
    if (!state.current) { await newDashboard(); return; }
    const created = await openWidgetEditor(null, state);
    if (!created) return;
    const list = state.editing ? state.draft : [...(state.current.widgets || [])];
    list.push(created);
    if (!state.editing) { await persistWidgets(list, { silent: true }).catch(() => {}); }
    renderWidgets();
    toast('Widget added', { kind: 'success' });
  }
  async function setWidgetRange(w, range) {
    const cfg = widgetConfig(w);
    cfg.range = range; w.config = cfg;
    disposeChart(w.id);
    if (!state.editing) await persistWidgets(widgetsList(), { silent: true }).catch(() => {});
    renderWidgets();
  }

  /* ---------- Rendering ---------- */
  function render() {
    setTitle();
    renderTabs();
    renderWidgets();
  }

  function renderWidgets() {
    clear(grid);
    if (!state.current) {
      grid.append(h('div', { class: 'card', style: { gridColumn: 'span 4' } }, emptyState({ icon: 'grid', title: 'No dashboards yet', text: 'Create a dashboard and add widgets for the groups you care about.', actions: h('button', { class: 'btn btn-primary', type: 'button', onclick: newDashboard }, icon('plus'), 'New dashboard') })));
      return;
    }
    const list = widgetsList();
    if (!list.length) {
      grid.append(h('div', { class: 'card', style: { gridColumn: 'span 4' } }, emptyState({ icon: 'grid', title: 'This dashboard is empty', text: 'Add an overall health summary, a status list or a latency chart to get started.', actions: h('button', { class: 'btn btn-primary', type: 'button', onclick: addWidget }, icon('plus'), 'Add widget') })));
      return;
    }
    list.forEach((w, i) => grid.append(renderWidget(w, i, list.length)));
  }

  function renderWidget(w, i, n) {
    const meta = widgetMeta(w.type);
    const cfg = widgetConfig(w);
    const width = Math.min(4, Math.max(1, w.width || meta.w)); const height = Math.min(3, Math.max(1, w.height || meta.h));
    const card = h('section', { class: `card widget ${state.editing ? 'editing' : ''}`, style: { gridColumn: `span ${width}`, gridRow: `span ${height}` }, 'data-w': width, 'data-h': height, 'aria-label': w.title || meta.label });
    const head = h('div', { class: 'widget-head' }, h('h2', { class: 'card-title' }, w.title || meta.label));
    const actions = h('div', { class: 'widget-edit-bar' });
    if (state.editing) {
      actions.append(
        h('button', { class: 'btn btn-sm icon-btn', type: 'button', 'aria-label': 'Move up', disabled: i === 0, onclick: () => moveWidget(i, -1) }, icon('arrowUp')),
        h('button', { class: 'btn btn-sm icon-btn', type: 'button', 'aria-label': 'Move down', disabled: i === n - 1, onclick: () => moveWidget(i, 1) }, icon('arrowDown')),
        h('button', { class: 'btn btn-sm icon-btn', type: 'button', 'aria-label': 'Edit widget', onclick: () => editWidget(i) }, icon('edit')),
        h('button', { class: 'btn btn-sm icon-btn btn-danger', type: 'button', 'aria-label': 'Remove widget', onclick: () => removeWidget(i) }, icon('x')),
      );
    } else if (CHART_TYPES.has(w.type) || w.type === 'uptime_chart') {
      actions.append(rangeChips(cfg.range || '24h', (r) => setWidgetRange(w, r)));
      actions.append(menuButton(() => chartMenu(w, i), { label: 'Chart options', small: true }));
    } else {
      actions.append(menuButton(() => [
        { label: 'Edit widget', icon: 'edit', onClick: () => editWidget(i) },
        { label: 'Move up', icon: 'arrowUp', onClick: () => moveWidget(i, -1), disabled: i === 0 },
        { label: 'Move down', icon: 'arrowDown', onClick: () => moveWidget(i, 1), disabled: i === n - 1 },
        { sep: true },
        { label: 'Remove', icon: 'trash', danger: true, onClick: () => removeWidget(i) },
      ], { label: 'Widget options', small: true }));
    }
    head.append(actions);
    const body = h('div', { class: 'widget-body' });
    card.append(head, body);
    try { renderWidgetBody(w, cfg, body, i); } catch (e) { console.error(e); body.append(h('div', { class: 'note' }, 'Could not render this widget.')); }
    return card;
  }

  function chartMenu(w, i) {
    const cfg = widgetConfig(w);
    const chart = state.charts.get(w.id);
    const ids = cfg.checkIds || [];
    const csvItems = ids.map((id) => {
      const c = findCheck(id);
      return { label: `Export CSV — ${c ? `${c.node.name} › ${c.check.name}` : `check ${id}`}`, icon: 'download', href: `/api/export/history.csv${qs({ checkId: id, range: cfg.range || '24h' })}`, download: `history-${id}-${cfg.range || '24h'}.csv` };
    });
    return [
      chart ? { label: 'Export chart as image', icon: 'image', onClick: () => chart.exportPNG(`${(w.title || w.type).replace(/[^\w-]+/g, '-').toLowerCase()}-${cfg.range || '24h'}.png`) } : null,
      ...csvItems,
      { sep: true },
      { label: 'Edit widget', icon: 'edit', onClick: () => editWidget(i) },
      { label: 'Move up', icon: 'arrowUp', onClick: () => moveWidget(i, -1), disabled: i === 0 },
      { label: 'Move down', icon: 'arrowDown', onClick: () => moveWidget(i, 1), disabled: i === widgetsList().length - 1 },
      { sep: true },
      { label: 'Remove', icon: 'trash', danger: true, onClick: () => removeWidget(i) },
    ];
  }

  function findCheck(id) {
    for (const n of state.nodes) for (const c of n.checks || []) if (String(c.id) === String(id)) return { node: n, check: c };
    return null;
  }

  function nodesFiltered(cfg) {
    const ov = state.overview?.nodes || [];
    return ov.filter((entry) => {
      const n = entry.node;
      if (cfg.group && n.group !== cfg.group) return false;
      if (cfg.tag && !(n.tags || []).includes(cfg.tag)) return false;
      if (cfg.nodeIds?.length && !cfg.nodeIds.map(Number).includes(Number(n.id))) return false;
      return true;
    });
  }

  function renderWidgetBody(w, cfg, body, index) {
    const ov = state.overview;
    if (!ov) { body.append(skeleton({ lines: 3 })); return; }
    switch (w.type) {
      case 'summary': return renderSummary(body, ov);
      case 'groups': return renderGroups(body, ov, cfg);
      case 'status_list': return renderStatusList(body, cfg);
      case 'latency_chart':
      case 'response_chart': return renderChart(w, cfg, body, { unit: 'ms', metric: cfg.metric || 'avg' });
      case 'loss_chart': return renderChart(w, cfg, body, { unit: '%', metric: 'loss', yMin: 0, yMax: 100 });
      case 'uptime_chart': return renderUptime(w, cfg, body);
      case 'incidents': return renderIncidents(body, ov, cfg);
      case 'cert_warnings': return renderCerts(body, ov);
      case 'attention': return renderAttention(body, ov);
      case 'monitor_health': return renderMonitorHealth(body);
      case 'table': return renderTable(body, cfg);
      default: body.append(h('div', { class: 'note' }, `Unknown widget type "${w.type}".`));
    }
  }

  function renderSummary(body, ov) {
    const s = ov.summary || {};
    const active = (s.up || 0) + (s.degraded || 0) + (s.down || 0) + (s.unknown || 0);
    let headline, ic, cls;
    if (s.down > 0) { headline = `${plural(s.down + (s.degraded || 0), 'node')} need${s.down + (s.degraded || 0) === 1 ? 's' : ''} attention`; ic = 'x'; cls = 'text-down'; }
    else if (s.degraded > 0) { headline = `${plural(s.degraded, 'node')} need${s.degraded === 1 ? 's' : ''} attention`; ic = 'alert'; cls = 'text-degraded'; }
    else if (active === 0) { headline = 'Nothing is being monitored yet'; ic = 'question'; cls = 'text-unknown'; }
    else if (s.up === 0 && s.unknown > 0) { headline = 'Waiting for first results'; ic = 'clock'; cls = 'text-unknown'; }
    else { headline = `All ${s.up} healthy`; ic = 'check'; cls = 'text-up'; }
    body.append(h('div', { class: `summary-headline ${cls}` }, icon(ic), h('span', { style: { color: 'var(--text)' } }, headline)));
    const counts = h('div', { class: 'summary-counts' });
    for (const [key, label] of [['up', 'Up'], ['degraded', 'Degraded'], ['down', 'Down'], ['unknown', 'Unknown']]) {
      const n = s[key] || 0; const m = statusMeta(key);
      counts.append(h('div', { class: `summary-count ${n === 0 ? 'zero' : ''}` }, h('div', { class: `n ${n > 0 ? 'text-' + key : ''}` }, String(n)), h('div', { class: 'l' }, icon(m.icon), label)));
    }
    body.append(counts);
    const foot = [];
    if (s.maintenance) foot.push(h('span', null, statusGlyph('maintenance', { text: false }), ` ${plural(s.maintenance, 'node')} in maintenance`));
    if (s.paused) foot.push(h('span', null, statusGlyph('paused', { text: false }), ` ${s.paused} paused`));
    foot.push(h('span', { class: 'dim', style: { marginLeft: 'auto' } }, `${s.total || 0} total · updated ${relTime(ov.generatedAt, now())}`));
    body.append(h('div', { class: 'summary-foot' }, foot));
  }

  function renderGroups(body, ov, cfg) {
    let groups = ov.groups || [];
    if (cfg.groups?.length) groups = groups.filter((g) => cfg.groups.includes(g.name));
    if (!groups.length) { body.append(emptyState({ icon: 'layers', title: 'No groups', text: 'Give your nodes a group to see status cards here.', compact: true })); return; }
    const wrap = h('div', { class: 'group-cards' });
    for (const g of groups) {
      const parts = [];
      if (g.up) parts.push(`${g.up} up`);
      if (g.degraded) parts.push(`${g.degraded} degraded`);
      if (g.down) parts.push(`${g.down} down`);
      if (g.unknown) parts.push(`${g.unknown} unknown`);
      if (g.maintenance) parts.push(`${g.maintenance} maintenance`);
      if (g.paused) parts.push(`${g.paused} paused`);
      wrap.append(h('a', { class: 'group-card', href: `#/nodes?group=${encodeURIComponent(g.name)}` },
        h('div', { class: 'row-between' }, h('span', { class: 'g-name' }, g.name), statusPill(g.status)),
        h('div', { class: 'g-count' }, parts.join(' · ') || `${g.total} nodes`)));
    }
    body.append(wrap);
  }

  function renderStatusList(body, cfg) {
    const rows = nodesFiltered(cfg);
    if (!rows.length) { body.append(emptyState({ icon: 'server', title: 'No nodes match', text: state.nodes.length ? 'Adjust the widget filter to include some nodes.' : 'Add your router, a website or a home server to see status here.', compact: true, actions: state.nodes.length ? null : h('a', { class: 'btn btn-primary btn-sm', href: '#/nodes' }, 'Go to Nodes') })); return; }
    const list = h('div', { class: 'status-rows' });
    for (const r of rows) {
      const n = r.node;
      const chips = h('div', { class: 'check-chips' });
      for (const c of r.checks || []) chips.append(checkChip(c.check, c.state));
      list.append(h('div', { class: 'status-row' },
        statusPill(r.status),
        h('div', { class: 'name' }, h('a', { href: `#/nodes/${n.id}` }, n.name), r.affectedBy ? h('span', { class: 'sub affected-note' }, icon('link'), `affected by ${r.affectedBy}`) : h('span', { class: 'sub' }, n.host)),
        chips));
    }
    body.append(list);
  }

  function renderIncidents(body, ov, cfg) {
    const limit = cfg.limit || 10;
    const items = (ov.incidents || []).slice(0, limit);
    if (!items.length) { body.append(h('div', { class: 'all-good' }, icon('check'), h('strong', null, 'No open incidents'), h('span', null, 'Everything has been quiet.'))); return; }
    const list = h('div', { class: 'event-rows' });
    for (const ev of items) list.append(eventRow(ev, { now: now() }));
    body.append(list, h('div', { style: { marginTop: '10px' } }, h('a', { class: 'small', href: '#/incidents' }, 'Open the incident timeline →')));
  }

  function renderCerts(body, ov) {
    const items = ov.certWarnings || [];
    if (!items.length) { body.append(h('div', { class: 'all-good' }, icon('shield'), h('strong', null, 'No certificate warnings'), h('span', null, 'All monitored certificates are valid.'))); return; }
    const list = h('div', { class: 'cert-rows' });
    for (const c of items) {
      const cls = c.daysRemaining <= 0 ? 'text-down' : c.daysRemaining <= 7 ? 'text-down' : 'text-degraded';
      list.append(h('div', { class: 'cert-row' },
        h('div', { class: `days ${cls}` }, c.daysRemaining <= 0 ? 'now' : String(c.daysRemaining), h('small', null, c.daysRemaining <= 0 ? 'expired' : 'days left')),
        h('div', { class: 'grow', style: { minWidth: 0, flex: 1 } }, h('div', { class: 'strong' }, h('a', { href: `#/nodes/${c.nodeId}`, style: { color: 'inherit' } }, c.nodeName), ` › ${c.checkName}`), h('div', { class: 'small muted truncate' }, `${c.subject || ''}${c.notAfter ? ' · expires ' + dateShort(c.notAfter) : ''}`)),
      ));
    }
    body.append(list);
  }

  function renderAttention(body, ov) {
    const items = ov.attention || [];
    if (!items.length) { body.append(h('div', { class: 'all-good' }, icon('check'), h('strong', null, 'Nothing needs attention'), h('span', null, 'All monitored nodes are healthy.'))); return; }
    const list = h('div', null);
    for (const a of items) {
      list.append(h('div', { class: 'attention-row' },
        statusPill(a.status),
        h('div', { class: 'a-body' },
          h('div', { class: 'a-title' }, h('a', { href: `#/nodes/${a.nodeId}`, style: { color: 'inherit' } }, a.nodeName), ` › ${a.checkName}`),
          h('div', { class: 'a-msg' }, a.message || ''),
          a.affectedBy ? h('div', { class: 'affected-note' }, icon('link'), `${a.nodeName} unavailable because ${a.affectedBy} is down`) : null),
        h('div', { class: 'a-since', title: a.since ? new Date(a.since).toLocaleString() : '' }, a.since ? (relTime(a.since, now()) === 'just now' ? 'since just now' : `since ${relTime(a.since, now()).replace(' ago', '')} ago`) : ''),
      ));
    }
    body.append(list);
  }

  function renderMonitorHealth(body) {
    const hl = state.health;
    if (!hl) { body.append(h('div', { class: 'note' }, 'Service health unavailable.')); return; }
    const okGlyph = (ok, yes, no) => h('span', { class: `status-glyph ${ok ? 'text-up' : 'text-down'}` }, icon(ok ? 'check' : 'x'), ok ? yes : no);
    const grid = h('div', { class: 'health-grid' },
      h('div', { class: 'health-item' }, h('div', { class: 'hv' }, okGlyph(hl.serviceRunning, 'Running', 'Stopped')), h('div', { class: 'hl' }, `Service (${hl.serviceMode || '—'})`)),
      h('div', { class: 'health-item' }, h('div', { class: 'hv' }, okGlyph(hl.schedulerRunning, 'Running', 'Stopped')), h('div', { class: 'hl' }, 'Scheduler')),
      h('div', { class: 'health-item' }, h('div', { class: 'hv' }, relTime(hl.lastCheckAt, now())), h('div', { class: 'hl' }, 'Last check')),
      h('div', { class: 'health-item' }, h('div', { class: 'hv' }, hl.nextCheckAt ? relTime(hl.nextCheckAt, now()) : '—'), h('div', { class: 'hl' }, 'Next check')),
      h('div', { class: 'health-item' }, h('div', { class: 'hv' }, bytes(hl.databaseBytes)), h('div', { class: 'hl' }, 'Database')),
      h('div', { class: 'health-item' }, h('div', { class: 'hv' }, hl.backup?.lastBackupAt ? h('span', { class: `status-glyph ${hl.backup.lastBackupOk ? 'text-up' : 'text-down'}` }, icon(hl.backup.lastBackupOk ? 'check' : 'x'), relTime(hl.backup.lastBackupAt, now())) : h('span', { class: 'muted' }, 'never')), h('div', { class: 'hl' }, 'Last backup')),
    );
    body.append(grid);
    if (hl.lastGap && Date.now() - new Date(hl.lastGap.to).getTime() < 86400e3) body.append(h('div', { class: 'note', style: { marginTop: '10px' } }, icon('moon'), ` Monitoring gap of ${duration(hl.lastGap.seconds)} ended ${relTime(hl.lastGap.to, now())}`));
    body.append(h('div', { style: { marginTop: 'auto', paddingTop: '10px' } }, h('a', { class: 'small', href: '#/settings/health' }, 'Open Monitor Health →')));
  }

  function renderTable(body, cfg) {
    const rows = nodesFiltered(cfg);
    if (!rows.length) { body.append(emptyState({ icon: 'server', title: 'No nodes match', text: 'Adjust the group or tag filter for this table.', compact: true })); return; }
    const table = h('table', { class: 'table' }, h('thead', null, h('tr', null, h('th', null, 'Node'), h('th', null, 'Status'), h('th', null, 'Host'), h('th', null, 'Group'), h('th', null, 'Checks'), h('th', null, 'Last checked'))));
    const tb = h('tbody');
    for (const r of rows) {
      const n = r.node;
      const last = (r.checks || []).map((c) => c.state?.lastRunAt).filter(Boolean).sort().pop();
      const chips = h('div', { class: 'check-chips' });
      for (const c of r.checks || []) chips.append(checkChip(c.check, c.state));
      tb.append(h('tr', null,
        h('td', null, h('a', { href: `#/nodes/${n.id}`, class: 'strong', style: { color: 'inherit' } }, n.name), r.affectedBy ? h('div', { class: 'affected-note' }, icon('link'), `affected by ${r.affectedBy}`) : null),
        h('td', null, statusPill(r.status)),
        h('td', { class: 'mono' }, n.host),
        h('td', null, n.group || h('span', { class: 'dim' }, '—')),
        h('td', null, chips),
        h('td', { class: 'muted nowrap' }, last ? relTime(last, now()) : '—')));
    }
    table.append(tb);
    body.append(h('div', { class: 'table-wrap' }, table));
  }

  /* ---------- Charts ---------- */
  function disposeChart(id) {
    const c = state.charts.get(id);
    if (c) { c.destroy(); state.charts.delete(id); }
    state.chartHosts.delete(id);
  }
  async function fetchHistory(ids, range) {
    const key = `${ids.join(',')}|${range}`;
    if (!state.historyCache.has(key)) state.historyCache.set(key, ids.length ? getHistoryMulti(ids, range) : getHistoryAuto(range));
    return state.historyCache.get(key);
  }

  function renderChart(w, cfg, body, { unit, metric, yMin, yMax }) {
    // No explicit selection: the service picks the most important checks
    // (critical/high nodes, ping and HTTP first) so a fresh dashboard shows data.
    const ids = (cfg.checkIds || []).map(Number).filter(Boolean);
    let host = state.chartHosts.get(w.id);
    if (!host) {
      host = h('div', { class: 'chart-host', style: { flex: '1', minHeight: '0', display: 'flex', flexDirection: 'column' } });
      state.chartHosts.set(w.id, host);
      const chart = new LineChart(host, { unit, yMin: yMin ?? null, yMax: yMax ?? null, title: w.title || widgetMeta(w.type).label, ariaLabel: `${w.title || widgetMeta(w.type).label} chart` });
      state.charts.set(w.id, chart);
    }
    body.append(host);
    const chart = state.charts.get(w.id);
    const range = cfg.range || '24h';
    fetchHistory(ids, range).then((series) => {
      if (state.destroyed || !state.charts.has(w.id)) return;
      const list = Array.isArray(series) ? series : [series];
      if (!list.length) { replace(body, emptyState({ icon: 'activity', title: 'Nothing to chart yet', text: 'Add a node with a ping or HTTP check, or edit this widget to pick checks.', compact: true })); state.charts.delete(w.id); state.chartHosts.delete(w.id); return; }
      const from = list[0]?.from, to = list[0]?.to;
      chart.setData({ series: list.map((hs, i) => toSeries(hs, metric, SERIES_COLORS[i % SERIES_COLORS.length])), from, to, bucketSeconds: list[0]?.bucketSeconds || 0 });
    }).catch((e) => { console.warn(e); body.append(h('div', { class: 'note' }, 'Could not load history: ' + e.message)); });
  }

  function renderUptime(w, cfg, body) {
    const ids = (cfg.checkIds || []).map(Number).filter(Boolean);
    const range = cfg.range || '7d';
    const list = h('div', null, h('div', { class: 'widget-loading' }, 'Loading…'));
    body.append(list);
    fetchHistory(ids, range).then((series) => {
      if (state.destroyed) return;
      clear(list);
      for (const hs of (Array.isArray(series) ? series : [series])) {
        const avail = hs.summary?.availability;
        const cls = avail == null ? '' : avail >= 99.9 ? 'text-up' : avail >= 95 ? 'text-degraded' : 'text-down';
        list.append(h('div', { class: 'uptime-row' },
          h('div', { class: 'uptime-name truncate' }, h('a', { href: `#/nodes/${findCheck(hs.checkId)?.node.id ?? ''}`, style: { color: 'inherit' } }, hs.nodeName || ''), h('div', { class: 'sub' }, hs.checkName)),
          uptimeBar(hs.points, { bucketSeconds: hs.bucketSeconds, from: hs.from, to: hs.to }),
          h('div', { class: `uptime-pct ${cls}` }, pct(avail, 2))));
      }
      list.append(uptimeLegend());
    }).catch((e) => replace(list, h('div', { class: 'note' }, 'Could not load history: ' + e.message)));
  }

  /* ---------- Boot ---------- */
  try {
    await loadAll();
  } catch (e) {
    replace(root, h('div', { class: 'card' }, emptyState({ icon: 'alert', title: 'Could not load the dashboard', text: e.message })));
    return { refresh: () => {}, destroy: () => {} };
  }
  if (ctx.params.id && !state.dashboards.some((d) => String(d.id) === ctx.params.id)) ctx.navigate('/dashboard');
  render();

  return {
    refresh: refreshData,
    async update(params) {
      if (state.editing) return false;
      pickCurrent(params.id);
      state.charts.forEach((c) => c.destroy()); state.charts.clear(); state.chartHosts.clear(); state.historyCache.clear();
      render();
      return true;
    },
    destroy() {
      state.destroyed = true;
      state.charts.forEach((c) => c.destroy());
      state.charts.clear();
    },
  };
}

/* ---------- Widget editor modal ---------- */

export function openWidgetEditor(existing, state) {
  return new Promise((resolve) => {
    const isNew = !existing;
    const w = existing ? { ...existing, config: { ...widgetConfig(existing) } } : { id: uid('w'), type: 'summary', title: '', width: 2, height: 1, config: {} };
    let result = null;
    const nodes = state.nodes || [];
    const groups = state.groups?.groups || [];
    const tags = state.groups?.tags || [];

    const picker = h('div', { class: 'widget-picker', role: 'radiogroup', 'aria-label': 'Widget type' });
    const titleInput = textInput({ value: w.title || '', placeholder: widgetMeta(w.type).label });
    const widthSel = selectInput({ options: [1, 2, 3, 4].map((n) => ({ value: n, label: `${n} column${n > 1 ? 's' : ''}` })), value: w.width || 2 });
    const heightSel = selectInput({ options: [1, 2, 3].map((n) => ({ value: n, label: `${n} row${n > 1 ? 's' : ''}` })), value: w.height || 1 });
    const cfgArea = h('div', { class: 'stack-sm' });
    let cfgControls = {};

    function renderPicker() {
      clear(picker);
      for (const t of WIDGET_TYPES) {
        picker.append(h('button', { type: 'button', class: `wp ${t.type === w.type ? 'active' : ''}`, role: 'radio', 'aria-checked': t.type === w.type ? 'true' : 'false', onclick: () => { if (w.type !== t.type) { w.type = t.type; w.config = { ...t.config }; if (isNew) { widthSel.value = String(t.w); heightSel.value = String(t.h); } titleInput.placeholder = t.label; renderPicker(); renderCfg(); } } },
          h('b', null, t.label), h('span', null, t.desc)));
      }
    }
    function renderCfg() {
      clear(cfgArea); cfgControls = {};
      const cfg = w.config;
      const groupSel = () => selectInput({ options: [{ value: '', label: 'All groups' }, ...groups.map((g) => ({ value: g.name, label: g.name }))], value: cfg.group || '' });
      const tagSel = () => selectInput({ options: [{ value: '', label: 'Any tag' }, ...tags.map((t) => ({ value: t.name, label: t.name }))], value: cfg.tag || '' });
      const rangeSel = (def) => selectInput({ options: ['1h', '24h', '7d', '30d', '1y'].map((r) => ({ value: r, label: rangeLabel(r) })), value: cfg.range || def });
      switch (w.type) {
        case 'groups': {
          const list = h('div', { class: 'check-list' });
          const sel = new Set(cfg.groups || []);
          for (const g of groups) list.append(checkbox({ label: `${g.name} (${g.count})`, checked: sel.has(g.name), onChange: (v) => { if (v) sel.add(g.name); else sel.delete(g.name); } }));
          if (!groups.length) list.append(h('div', { class: 'note' }, 'No groups yet — all groups will be shown.'));
          cfgControls.groups = () => [...sel];
          cfgArea.append(field({ label: 'Groups to show (none selected = all)', input: list }));
          break;
        }
        case 'status_list': case 'table': {
          const g = groupSel(); const t = tagSel();
          cfgControls.group = () => g.value; cfgControls.tag = () => t.value;
          cfgArea.append(h('div', { class: 'form-grid' }, field({ label: 'Group', input: g }), field({ label: 'Tag', input: t })));
          if (w.type === 'status_list') {
            const sel = new Set((cfg.nodeIds || []).map(Number));
            const list = h('div', { class: 'check-list' });
            for (const n of nodes) list.append(checkbox({ label: n.name, checked: sel.has(Number(n.id)), onChange: (v) => { if (v) sel.add(Number(n.id)); else sel.delete(Number(n.id)); } }));
            cfgControls.nodeIds = () => [...sel];
            cfgArea.append(field({ label: 'Specific nodes (optional)', input: list, help: 'Leave all unticked to show every node that matches the group / tag.' }));
          }
          break;
        }
        case 'latency_chart': case 'response_chart': case 'loss_chart': case 'uptime_chart': {
          const filter = w.type === 'loss_chart' ? (c) => c.type === 'ping' : w.type === 'response_chart' ? (c) => ['http', 'keyword', 'json'].includes(c.type) : null;
          const ms = checkMultiSelect(nodes, cfg.checkIds || [], { filterType: filter });
          cfgControls.checkIds = () => ms.value;
          const r = rangeSel(w.type === 'uptime_chart' ? '7d' : '24h');
          cfgControls.range = () => r.value;
          const row = h('div', { class: 'form-grid' }, field({ label: 'Time range', input: r }));
          if (w.type === 'latency_chart' || w.type === 'response_chart') {
            const m = selectInput({ options: [{ value: 'avg', label: 'Average' }, { value: 'min', label: 'Minimum' }, { value: 'max', label: 'Maximum' }, { value: 'jitter', label: 'Jitter' }], value: cfg.metric || 'avg' });
            cfgControls.metric = () => m.value;
            row.append(field({ label: 'Metric', input: m }));
          }
          cfgArea.append(row, field({ label: 'Checks', input: ms, help: w.type === 'loss_chart' ? 'Only ping checks report packet loss.' : 'Pick one or more checks. Each becomes a line.' }));
          break;
        }
        case 'incidents': {
          const n = numberInput({ value: cfg.limit || 10, min: 1, max: 50 });
          cfgControls.limit = () => Number(n.value) || 10;
          cfgArea.append(field({ label: 'How many to show', input: n }));
          break;
        }
        default:
          cfgArea.append(h('p', { class: 'note' }, 'This widget has no options.'));
      }
    }
    renderPicker(); renderCfg();

    const form = h('form', { class: 'stack', onsubmit: (e) => { e.preventDefault(); submit(); } },
      field({ label: 'Widget type', input: picker }),
      h('div', { class: 'form-grid-3' }, field({ label: 'Title', input: titleInput, help: 'Leave blank to use the default title.' }), field({ label: 'Width', input: widthSel }), field({ label: 'Height', input: heightSel })),
      h('div', null, h('div', { class: 'section-title' }, 'Options'), cfgArea),
    );
    function submit() {
      const cfg = {};
      for (const [k, get] of Object.entries(cfgControls)) cfg[k] = get();
      result = { id: w.id, type: w.type, title: titleInput.value.trim(), width: Number(widthSel.value), height: Number(heightSel.value), config: cfg };
      m.close();
    }
    const m = openModal({
      title: isNew ? 'Add widget' : 'Edit widget', wide: true, body: form,
      footer: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, 'Cancel'), h('button', { class: 'btn btn-primary', type: 'button', onclick: submit }, isNew ? 'Add widget' : 'Save widget')],
      onClose: () => resolve(result),
    });
  });
}

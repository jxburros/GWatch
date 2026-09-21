// Dashboard: switchable dashboards made of widgets on a 4-column grid.
// Widgets are dragged by their handle and resized from their edges; the
// layout (x, y, width, height per widget) is saved with the dashboard.

import { api, getHistoryMulti, getHistoryAuto, qs } from '../api.js';
import { h, icon, clear, replace, statusPill, statusSpine, statusWord, statusGlyph, checkChip, statusOrb, toast, confirmDialog, promptDialog, openModal, menuButton, field, textInput, numberInput, selectInput, checkbox, emptyState, skeleton, eventRow, rangeChips, statusMeta, uid } from '../components.js';
import { relTime, bytes, plural, dateShort, duration, pct, nodeGroups, inGroup } from '../fmt.js';
import { chartConfigEditor, renderConfiguredChart, normalizeChartConfig } from '../chart-config.js';
// charts.js is already in the graph by way of chart-config.js, so naming these
// here costs nothing and spares the availability widget an await it does not
// need — the widget must be able to fill itself in one synchronous step.
import { uptimeBar, uptimeLegend } from '../charts.js';

export const COLS = 4;
export const WIDGET_TYPES = [
  { type: 'summary', label: 'Overall health', desc: 'Big up / degraded / down / unknown numerals with a headline.', w: 2, h: 2, config: {} },
  { type: 'groups', label: 'Group status', desc: 'One card per group with its worst status.', w: 2, h: 1, config: { groups: [] } },
  { type: 'status_list', label: 'Status list', desc: 'Nodes and their checks, optionally filtered by group, tag or node.', w: 2, h: 2, config: { group: '', tag: '', nodeIds: [] } },
  { type: 'chart', label: 'Chart', desc: 'Any metric for any checks, styled the way you like (same options as the Charts tab).', w: 2, h: 2, config: { metric: 'avg', range: '24h', style: 'area' } },
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
const LEGACY_CHARTS = { latency_chart: { metric: 'avg', unit: 'ms' }, response_chart: { metric: 'avg', unit: 'ms' }, loss_chart: { metric: 'loss', unit: '%' } };
const CHART_LIKE = new Set(['chart', 'latency_chart', 'response_chart', 'loss_chart', 'uptime_chart']);

export function widgetConfig(w) {
  let c = w.config;
  if (typeof c === 'string') { try { c = JSON.parse(c); } catch { c = {}; } }
  return c && typeof c === 'object' ? c : {};
}
function widgetMeta(type) { return WIDGET_TYPES.find((x) => x.type === type) || { type, label: type, desc: '', w: 2, h: 1, config: {} }; }

/* ---------- Layout helpers (pure) ---------- */
export function collides(a, b) { return a !== b && a.x < b.x + b.w && a.x + a.w > b.x && a.y < b.y + b.h && a.y + a.h > b.y; }

/** Normalise widget geometry into {x,y,w,h} items; auto-places widgets without a position. */
export function toLayout(widgets) {
  const items = widgets.map((w) => {
    const meta = widgetMeta(w.type);
    const wd = Math.min(COLS, Math.max(1, Number(w.width) || meta.w));
    const ht = Math.min(6, Math.max(1, Number(w.height) || meta.h));
    const x = w.x == null ? null : Math.min(COLS - wd, Math.max(0, Number(w.x)));
    const y = w.y == null ? null : Math.max(0, Number(w.y));
    return { id: w.id, x, y, w: wd, h: ht };
  });
  const placed = items.filter((i) => i.x != null && i.y != null);
  for (const it of items) {
    if (it.x != null && it.y != null) continue;
    // first free slot scanning rows then columns
    outer: for (let y = 0; ; y++) {
      for (let x = 0; x + it.w <= COLS; x++) {
        const test = { ...it, x, y };
        if (!placed.some((p) => collides(test, p))) { it.x = x; it.y = y; placed.push(it); break outer; }
      }
    }
  }
  return compact(items, null);
}

/** Gravity: every item (except pinned) floats up as far as it can, in reading order. */
export function compact(items, pinned) {
  const done = pinned ? [pinned] : [];
  const order = items.filter((i) => i !== pinned).sort((a, b) => a.y - b.y || a.x - b.x);
  for (const it of order) {
    let y = 0;
    while (done.some((p) => collides({ ...it, y }, p))) y++;
    it.y = y;
    done.push(it);
  }
  return items;
}

/** Place `moved` at its new position and push overlapping widgets down, then compact. */
export function resolve(items, moved) {
  moved.x = Math.min(COLS - moved.w, Math.max(0, moved.x));
  moved.y = Math.max(0, moved.y);
  let changed = true; let guard = 0;
  while (changed && guard++ < 200) {
    changed = false;
    for (const it of items.filter((i) => i !== moved).sort((a, b) => a.y - b.y)) {
      if (collides(it, moved)) { it.y = moved.y + moved.h; changed = true; }
    }
    // pushed items may now overlap each other
    const order = items.filter((i) => i !== moved).sort((a, b) => a.y - b.y || a.x - b.x);
    for (let i = 0; i < order.length; i++) for (let j = 0; j < i; j++) if (collides(order[i], order[j])) { order[i].y = order[j].y + order[j].h; changed = true; }
  }
  return compact(items, moved);
}

export async function mount(root, ctx) {
  const state = {
    dashboards: [], current: null, overview: null, health: null, nodes: [], groups: { groups: [], tags: [] },
    chartViews: new Map(), historyCache: new Map(), uptimeLists: new Map(), destroyed: false, layout: [], interacting: false,
  };
  const now = () => Date.now();
  // Widget id -> its card on the grid, so a live update can find a widget
  // again instead of building a new one.
  const widgetEls = new Map();

  const grid = h('div', { class: 'dash-grid' });
  // The dashboard chips are links to addresses (#/dashboard/2), so they are
  // navigation with a current page rather than tabs — which also lets the
  // "New" button sit beside them without pretending to be one.
  const tabs = h('nav', { class: 'dash-tabs', 'aria-label': 'Dashboards' });
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
    state.layout = state.current ? toLayout(state.current.widgets || []) : [];
  }
  async function refreshData() {
    if (state.interacting) return;
    const [overview, health] = await Promise.all([api.get('/api/overview'), api.get('/api/health').catch(() => state.health)]);
    if (state.destroyed || state.interacting) return;
    state.overview = overview; state.health = health;
    state.historyCache.clear();
    refreshWidgets();
  }

  /* ---------- Title / actions ---------- */
  function setTitle() {
    const d = state.current;
    const actions = [];
    if (d) {
      actions.push(
        h('button', { class: 'btn btn-primary admin-only', type: 'button', onclick: () => addWidget() }, icon('plus'), 'Add widget'),
        menuButton(() => [
          { label: 'Rename dashboard', icon: 'edit', onClick: renameDashboard, adminOnly: true },
          { label: 'New dashboard', icon: 'plus', onClick: newDashboard, adminOnly: true },
          { label: 'Tidy layout', icon: 'layout', onClick: tidy, adminOnly: true },
          { sep: true, adminOnly: true },
          { label: 'Delete dashboard', icon: 'trash', danger: true, onClick: deleteDashboard, disabled: state.dashboards.length <= 1, adminOnly: true },
        ], { label: 'Dashboard options', cls: 'admin-only' }),
      );
    } else {
      actions.push(h('button', { class: 'btn btn-primary admin-only', type: 'button', onclick: newDashboard }, icon('plus'), 'New dashboard'));
    }
    // A viewer cannot save a layout, so the hint about dragging is misleading.
    ctx.setTitle(d ? d.name : 'Dashboard', { actions });
  }

  function renderTabs() {
    clear(tabs);
    for (const d of state.dashboards) {
      const active = state.current && d.id === state.current.id;
      tabs.append(h('a', { class: `chip ${active ? 'active' : ''}`, 'aria-current': active ? 'page' : null, href: `#/dashboard/${d.id}` }, d.name));
    }
    tabs.append(h('button', { class: 'chip admin-only', type: 'button', 'aria-label': 'New dashboard', title: 'New dashboard', onclick: newDashboard }, icon('plus'), 'New'));
  }

  /* ---------- Dashboard CRUD ---------- */
  async function newDashboard() {
    const name = await promptDialog({ title: 'New dashboard', label: 'Name', placeholder: 'e.g. Servers, Internet, Critical', confirmLabel: 'Create' });
    if (!name) return;
    try {
      const created = await api.post('/api/dashboards', { name: name.trim(), widgets: [{ id: uid('w'), type: 'summary', title: 'Overall health', x: 0, y: 0, width: 2, height: 1, config: {} }, { id: uid('w'), type: 'attention', title: 'Needs attention', x: 2, y: 0, width: 2, height: 1, config: {} }] });
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
  /** Save the current widget list (with layout) to the server. */
  async function persist({ silent = true } = {}) {
    const d = state.current; if (!d) return;
    const widgets = (d.widgets || []).map((w) => { const l = state.layout.find((x) => x.id === w.id); return l ? { ...w, x: l.x, y: l.y, width: l.w, height: l.h } : w; });
    try {
      const saved = await api.put(`/api/dashboards/${d.id}`, { ...d, widgets });
      Object.assign(d, saved);
      state.layout = toLayout(d.widgets || []);
      if (!silent) toast('Layout saved', { kind: 'success' });
    } catch (e) { toast(e.message, { kind: 'error' }); throw e; }
  }
  function tidy() { state.layout = compact(state.layout.map((l) => ({ ...l })), null); persist().catch(() => {}); renderWidgets(); }

  /* ---------- Widget CRUD ---------- */
  function widgetsList() { return state.current?.widgets || []; }
  async function removeWidget(w) {
    const ok = await confirmDialog({ title: `Remove "${w.title || widgetMeta(w.type).label}"?`, confirmLabel: 'Remove', danger: true });
    if (!ok) return;
    state.current.widgets = widgetsList().filter((x) => x.id !== w.id);
    state.layout = compact(state.layout.filter((l) => l.id !== w.id), null);
    disposeChart(w.id);
    await persist().catch(() => {});
    renderWidgets();
  }
  async function editWidget(w) {
    const edited = await openWidgetEditor(w, state);
    if (!edited) return;
    const list = widgetsList();
    const i = list.findIndex((x) => x.id === w.id);
    list[i] = edited;
    const l = state.layout.find((x) => x.id === w.id);
    if (l && (l.w !== edited.width || l.h !== edited.height)) { l.w = Math.min(COLS, edited.width); l.h = edited.height; l.x = Math.min(l.x, COLS - l.w); resolve(state.layout, l); }
    disposeChart(edited.id);
    await persist().catch(() => {});
    renderWidgets();
  }
  async function addWidget() {
    if (!state.current) { await newDashboard(); return; }
    const created = await openWidgetEditor(null, state);
    if (!created) return;
    widgetsList().push(created);
    state.layout = toLayout(widgetsList().map((w) => (w.id === created.id ? { ...w, x: null, y: null } : { ...w, ...(state.layout.find((l) => l.id === w.id) ? { x: state.layout.find((l) => l.id === w.id).x, y: state.layout.find((l) => l.id === w.id).y } : {}) })));
    await persist().catch(() => {});
    renderWidgets();
    toast('Widget added', { kind: 'success' });
  }
  async function setWidgetRange(w, range) {
    const cfg = widgetConfig(w);
    cfg.range = range; w.config = cfg;
    disposeChart(w.id);
    await persist().catch(() => {});
    renderWidgets();
  }

  /* ---------- Rendering ---------- */
  function render() { setTitle(); renderTabs(); renderWidgets(); }

  /* A live update must not throw the grid away. Rebuilding it re-creates every
     widget, and a chart built from scratch shows nothing at all until its
     history request comes back — that gap is what flashed. So an update
     refreshes each widget where it stands: the cheap ones by writing their
     body again, which is synchronous and therefore invisible, and the charts
     by asking the view they already have to reload, which swaps its contents
     only once the new data is in hand. Anything structural — a widget added,
     removed or moved — still goes through a full render. */
  function refreshWidgets() {
    if (state.interacting) return;
    const list = state.current ? widgetsList() : [];
    if (!list.length || list.length !== widgetEls.size || list.some((w) => !widgetEls.has(w.id))) { renderWidgets(); return; }
    for (const w of list) {
      const card = widgetEls.get(w.id);
      if (!card || !card.isConnected) { renderWidgets(); return; }
      const view = state.chartViews.get(w.id);
      if (view) { view.refresh(); continue; }
      if (w.type === 'uptime_chart') { fillUptime(state.uptimeLists.get(w.id), w); continue; }
      clear(card._body);
      replace(card._actions, card._actionKids);
      try { renderWidgetBody(w, widgetConfig(w), card._body, card._actions); } catch (e) { console.error(e); card._body.append(h('div', { class: 'note' }, 'Could not render this widget.')); }
    }
  }

  function renderWidgets() {
    if (state.interacting) return;
    widgetEls.clear();
    state.uptimeLists.clear();
    clear(grid);
    if (!state.current) {
      grid.append(h('div', { class: 'card', style: { gridColumn: 'span 4' } }, emptyState({ icon: 'grid', title: 'No dashboards yet', text: 'Create a dashboard and add widgets for the groups you care about.', actions: h('button', { class: 'btn btn-primary admin-only', type: 'button', onclick: newDashboard }, icon('plus'), 'New dashboard') })));
      return;
    }
    const list = widgetsList();
    if (!list.length) {
      grid.append(h('div', { class: 'card', style: { gridColumn: 'span 4' } }, emptyState({ icon: 'grid', title: 'This dashboard is empty', text: 'Add an overall health summary, a status list or a chart to get started.', actions: h('button', { class: 'btn btn-primary admin-only', type: 'button', onclick: addWidget }, icon('plus'), 'Add widget') })));
      return;
    }
    const ordered = [...state.layout].sort((a, b) => a.y - b.y || a.x - b.x);
    for (const l of ordered) {
      const w = list.find((x) => x.id === l.id);
      if (!w) continue;
      const card = renderWidget(w, l);
      widgetEls.set(w.id, card);
      grid.append(card);
    }
  }

  function applyGeometry(el, l) {
    el.style.gridColumn = `${l.x + 1} / span ${l.w}`;
    el.style.gridRow = `${l.y + 1} / span ${l.h}`;
    el.dataset.w = l.w; el.dataset.h = l.h;
  }

  function renderWidget(w, l) {
    const meta = widgetMeta(w.type);
    const cfg = widgetConfig(w);
    const card = h('section', { class: 'card widget', 'aria-label': w.title || meta.label });
    applyGeometry(card, l);
    const dragHandle = h('button', { class: 'widget-drag admin-only', type: 'button', 'aria-label': `Move ${w.title || meta.label}`, title: 'Drag to move' }, icon('grip'));
    const head = h('div', { class: 'widget-head' }, h('h2', { class: 'card-title' }, dragHandle, h('span', { class: 'truncate', title: w.title || meta.label }, w.title || meta.label)));
    const actions = h('div', { class: 'widget-edit-bar' });
    if (CHART_LIKE.has(w.type)) {
      actions.append(rangeChips(cfg.range || (w.type === 'uptime_chart' ? '7d' : '24h'), (r) => setWidgetRange(w, r)));
      actions.append(menuButton(() => chartMenu(w), { label: 'Chart options', small: true, cls: 'admin-only' }));
    } else {
      actions.append(menuButton(() => [
        { label: 'Edit widget', icon: 'edit', onClick: () => editWidget(w) },
        { sep: true },
        { label: 'Remove', icon: 'trash', danger: true, onClick: () => removeWidget(w) },
      ], { label: 'Widget options', small: true, cls: 'admin-only' }));
    }
    head.append(actions);
    const body = h('div', { class: 'widget-body' });
    card.append(head, body);
    // Kept on the card so a live update can rewrite just this widget's body
    // rather than the whole grid — see refreshWidgets(). The action bar is
    // noted as it stands now, before the body is rendered, because a body may
    // add a control of its own to it (Monitor health does) and a re-render
    // would otherwise add a second one.
    card._body = body; card._actions = actions; card._actionKids = [...actions.children];
    try { renderWidgetBody(w, cfg, body, actions); } catch (e) { console.error(e); body.append(h('div', { class: 'note' }, 'Could not render this widget.')); }
    // resize handles
    for (const dir of ['e', 's', 'se']) {
      const hnd = h('div', { class: `rs rs-${dir} admin-only`, title: 'Drag to resize', 'aria-hidden': 'true' });
      hnd.addEventListener('pointerdown', (e) => startResize(e, w, card, dir));
      card.append(hnd);
    }
    dragHandle.addEventListener('pointerdown', (e) => startDrag(e, w, card));
    return card;
  }

  /* ---------- Drag & resize ---------- */
  function cellSize() {
    const rect = grid.getBoundingClientRect();
    const cs = getComputedStyle(grid);
    const gap = parseFloat(cs.columnGap) || 1;
    const padding = parseFloat(cs.paddingLeft) || 0;
    const cellW = (rect.width - padding * 2 - gap * (COLS - 1)) / COLS;
    const rowH = parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--row-h')) || 168;
    return { rect, gap, padding, cellW, rowH };
  }
  function layoutOf(w) { return state.layout.find((l) => l.id === w.id); }

  function startDrag(e, w, card) {
    if (e.button !== 0 || window.innerWidth < 900) return;
    e.preventDefault();
    const l = layoutOf(w); if (!l) return;
    const { rect, gap, padding, cellW, rowH } = cellSize();
    const cardRect = card.getBoundingClientRect();
    const offX = e.clientX - cardRect.left, offY = e.clientY - cardRect.top;
    const placeholder = h('div', { class: 'widget-placeholder' });
    applyGeometry(placeholder, l);
    const ghost = { ...l };
    let moved = false;
    state.interacting = true; grid.classList.add('interacting');
    const onMove = (ev) => {
      if (!moved) {
        moved = true;
        grid.insertBefore(placeholder, card);
        card.classList.add('dragging');
        card.style.setProperty('--drag-w', `${cardRect.width}px`); card.style.setProperty('--drag-h', `${cardRect.height}px`);
        card.style.gridColumn = ''; card.style.gridRow = '';
      }
      const gx = ev.clientX - rect.left - padding - offX, gy = ev.clientY - rect.top - padding - offY + grid.scrollTop;
      card.style.left = `${gx}px`; card.style.top = `${gy}px`;
      const nx = Math.round(gx / (cellW + gap)), ny = Math.round(gy / (rowH + gap));
      const tx = Math.min(COLS - ghost.w, Math.max(0, nx)), ty = Math.max(0, ny);
      if (tx !== ghost.x || ty !== ghost.y) {
        ghost.x = tx; ghost.y = ty;
        // preview the layout with the ghost pinned
        const preview = state.layout.map((it) => (it.id === l.id ? ghost : { ...it }));
        resolve(preview, ghost);
        for (const it of preview) { if (it.id === l.id) continue; const el = grid.querySelector(`[data-wid="${it.id}"]`); if (el) applyGeometry(el, it); }
        applyGeometry(placeholder, ghost);
      }
    };
    const onUp = () => {
      window.removeEventListener('pointermove', onMove); window.removeEventListener('pointerup', onUp); window.removeEventListener('pointercancel', onUp);
      state.interacting = false; grid.classList.remove('interacting');
      card.classList.remove('dragging'); card.style.left = ''; card.style.top = '';
      placeholder.remove();
      if (moved && (ghost.x !== l.x || ghost.y !== l.y)) {
        l.x = ghost.x; l.y = ghost.y;
        resolve(state.layout, l);
        persist().catch(() => {});
      }
      renderWidgets();
    };
    window.addEventListener('pointermove', onMove); window.addEventListener('pointerup', onUp); window.addEventListener('pointercancel', onUp);
  }

  function startResize(e, w, card, dir) {
    if (e.button !== 0 || window.innerWidth < 900) return;
    e.preventDefault(); e.stopPropagation();
    const l = layoutOf(w); if (!l) return;
    const { gap, cellW, rowH } = cellSize();
    const startX = e.clientX, startY = e.clientY;
    const start = { w: l.w, h: l.h };
    const ghost = { ...l };
    state.interacting = true; grid.classList.add('interacting'); card.classList.add('resizing');
    const onMove = (ev) => {
      let nw = start.w, nh = start.h;
      if (dir === 'e' || dir === 'se') nw = Math.round((start.w * (cellW + gap) + (ev.clientX - startX)) / (cellW + gap));
      if (dir === 's' || dir === 'se') nh = Math.round((start.h * (rowH + gap) + (ev.clientY - startY)) / (rowH + gap));
      nw = Math.min(COLS - l.x, Math.max(1, nw)); nh = Math.min(6, Math.max(1, nh));
      if (nw !== ghost.w || nh !== ghost.h) {
        ghost.w = nw; ghost.h = nh;
        const preview = state.layout.map((it) => (it.id === l.id ? ghost : { ...it }));
        resolve(preview, ghost);
        for (const it of preview) { const el = it.id === l.id ? card : grid.querySelector(`[data-wid="${it.id}"]`); if (el) applyGeometry(el, it); }
        card.querySelectorAll('canvas').length && window.dispatchEvent(new Event('resize'));
      }
    };
    const onUp = () => {
      window.removeEventListener('pointermove', onMove); window.removeEventListener('pointerup', onUp); window.removeEventListener('pointercancel', onUp);
      state.interacting = false; grid.classList.remove('interacting'); card.classList.remove('resizing');
      if (ghost.w !== l.w || ghost.h !== l.h) {
        l.w = ghost.w; l.h = ghost.h;
        const wd = widgetsList().find((x) => x.id === w.id); if (wd) { wd.width = l.w; wd.height = l.h; }
        resolve(state.layout, l);
        disposeChart(w.id);
        persist().catch(() => {});
      }
      renderWidgets();
    };
    window.addEventListener('pointermove', onMove); window.addEventListener('pointerup', onUp); window.addEventListener('pointercancel', onUp);
  }

  function chartMenu(w) {
    const cfg = widgetConfig(w);
    const view = state.chartViews.get(w.id);
    const ids = (cfg.checkIds || []).map(Number).filter(Boolean);
    const csvItems = ids.map((id) => {
      const c = findCheck(id);
      return { label: `Export CSV — ${c ? `${c.node.name} › ${c.check.name}` : `check ${id}`}`, icon: 'download', href: `/api/export/history.csv${qs({ checkId: id, range: cfg.range || '24h' })}`, download: `history-${id}-${cfg.range || '24h'}.csv` };
    });
    return [
      view ? { label: 'Export chart as image', icon: 'image', onClick: () => view.exportPNG(`${(w.title || w.type).replace(/[^\w-]+/g, '-').toLowerCase()}-${cfg.range || '24h'}.png`) } : null,
      ...csvItems,
      { label: 'Open in Charts', icon: 'chart', onClick: () => openInCharts(w) },
      { sep: true },
      { label: 'Edit widget', icon: 'edit', onClick: () => editWidget(w) },
      { sep: true },
      { label: 'Remove', icon: 'trash', danger: true, onClick: () => removeWidget(w) },
    ];
  }
  async function openInCharts(w) {
    const cfg = chartConfigFor(w);
    try {
      const saved = await api.get('/api/charts');
      const entry = { id: uid('chart'), name: w.title || widgetMeta(w.type).label, config: cfg };
      const list = await api.put('/api/charts', [...(saved || []), entry]);
      const created = list[list.length - 1];
      ctx.navigate(`/charts/${created.id}`);
    } catch (e) { toast(e.message, { kind: 'error' }); }
  }
  function chartConfigFor(w) {
    const cfg = widgetConfig(w);
    if (w.type === 'chart') return normalizeChartConfig(cfg);
    const legacy = LEGACY_CHARTS[w.type] || {};
    return normalizeChartConfig({ checkIds: cfg.checkIds || [], range: cfg.range || '24h', metric: w.type === 'loss_chart' ? 'loss' : (cfg.metric || legacy.metric || 'avg'), style: 'area' });
  }

  function findCheck(id) {
    for (const n of state.nodes) for (const c of n.checks || []) if (String(c.id) === String(id)) return { node: n, check: c };
    return null;
  }

  function nodesFiltered(cfg) {
    const ov = state.overview?.nodes || [];
    return ov.filter((entry) => {
      const n = entry.node;
      if (cfg.group && !inGroup(n, cfg.group)) return false;
      if (cfg.tag && !(n.tags || []).includes(cfg.tag)) return false;
      if (cfg.nodeIds?.length && !cfg.nodeIds.map(Number).includes(Number(n.id))) return false;
      return true;
    });
  }

  function renderWidgetBody(w, cfg, body, actions) {
    const ov = state.overview;
    body.closest('.widget').dataset.wid = w.id;
    if (!ov) { body.append(skeleton({ lines: 3 })); return; }
    switch (w.type) {
      case 'summary': return renderSummary(body, ov);
      case 'groups': return renderGroups(body, ov, cfg);
      case 'status_list': return renderStatusList(body, cfg);
      case 'chart':
      case 'latency_chart':
      case 'response_chart':
      case 'loss_chart': return renderChart(w, body);
      case 'uptime_chart': return renderUptime(w, body);
      case 'incidents': return renderIncidents(body, ov, cfg);
      case 'cert_warnings': return renderCerts(body, ov);
      case 'attention': return renderAttention(body, ov);
      case 'monitor_health': return renderMonitorHealth(body, actions);
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
      const cls = n > 0 ? `text-${key}` : '';
      counts.append(h('div', { class: `summary-count ${n === 0 ? 'zero' : ''}` },
        h('div', { class: `n ${cls}` }, String(n)),
        h('div', { class: `rule ${cls}` }),
        h('div', { class: 'l' }, icon(m.icon), label)));
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
      const gm = statusMeta(g.status);
      wrap.append(h('a', { class: 'group-card', href: `#/nodes?group=${encodeURIComponent(g.name)}` },
        statusSpine(g.status, { key: `group:${g.name}` }),
        h('div', { class: 'g-head' }, h('span', { class: 'g-name' }, g.name), h('span', { class: 'sr-only' }, gm.label)),
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
        statusSpine(r.status, { key: `node:${n.id}` }),
        statusWord(r.status),
        h('div', { class: 'name' }, h('a', { href: `#/nodes/${n.id}` }, n.name), r.affectedBy ? h('span', { class: 'sub affected-note' }, icon('link'), `affected by ${r.affectedBy}`) : h('span', { class: 'sub' }, n.host || '')),
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
    body.append(list, h('div', { style: { marginTop: '8px' } }, h('a', { class: 'small', href: '#/incidents' }, 'Open the incident timeline →')));
  }

  function renderCerts(body, ov) {
    const items = ov.certWarnings || [];
    if (!items.length) { body.append(h('div', { class: 'all-good' }, icon('shield'), h('strong', null, 'No certificate warnings'), h('span', null, 'All monitored certificates are valid.'))); return; }
    const list = h('div', { class: 'cert-rows' });
    for (const c of items) {
      const cls = c.daysRemaining <= 7 ? 'text-down' : 'text-degraded';
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
        statusSpine(a.status, { key: `att:${a.nodeId}:${a.checkName}` }),
        statusWord(a.status),
        h('div', { class: 'a-body' },
          h('div', { class: 'a-title' }, h('a', { href: `#/nodes/${a.nodeId}`, style: { color: 'inherit' } }, a.nodeName), ` › ${a.checkName}`),
          h('div', { class: 'a-msg' }, a.message || ''),
          a.affectedBy ? h('div', { class: 'affected-note' }, icon('link'), `${a.nodeName} unavailable because ${a.affectedBy} is down`) : null),
        h('div', { class: 'a-since', title: a.since ? new Date(a.since).toLocaleString() : '' }, a.since ? (relTime(a.since, now()) === 'just now' ? 'since just now' : `since ${relTime(a.since, now()).replace(' ago', '')} ago`) : ''),
      ));
    }
    body.append(list);
  }

  function renderMonitorHealth(body, actions) {
    const hl = state.health;
    if (!hl) { body.append(h('div', { class: 'note' }, 'Service health unavailable.')); return; }
    // The way out to the full health page is a panel action, so it sits in the
    // band with the widget's other controls. That leaves the whole body to the
    // readouts, which is the only way six of them clear the fold in a widget
    // one row tall.
    if (actions) actions.prepend(h('a', { class: 'btn btn-sm btn-ghost', href: '#/settings/health', title: 'Open Monitor health' }, 'Open', icon('arrowRight')));
    const okGlyph = (ok, yes, no) => h('span', { class: `status-glyph ${ok ? 'text-up' : 'text-down'}` }, icon(ok ? 'check' : 'x'), ok ? yes : no);
    const gridEl = h('div', { class: 'health-grid' },
      h('div', { class: 'health-item' }, h('div', { class: 'hv' }, okGlyph(hl.serviceRunning, 'Running', 'Stopped')), h('div', { class: 'hl', title: `Service (${hl.serviceMode || '\u2014'})` }, `Service (${hl.serviceMode || '\u2014'})`)),
      h('div', { class: 'health-item' }, h('div', { class: 'hv' }, okGlyph(hl.schedulerRunning, 'Running', 'Stopped')), h('div', { class: 'hl' }, 'Scheduler')),
      h('div', { class: 'health-item' }, h('div', { class: 'hv' }, relTime(hl.lastCheckAt, now())), h('div', { class: 'hl' }, 'Last check')),
      h('div', { class: 'health-item' }, h('div', { class: 'hv' }, hl.nextCheckAt ? relTime(hl.nextCheckAt, now()) : '\u2014'), h('div', { class: 'hl' }, 'Next check')),
      h('div', { class: 'health-item' }, h('div', { class: 'hv' }, bytes(hl.databaseBytes)), h('div', { class: 'hl' }, 'Database')),
      h('div', { class: 'health-item' }, h('div', { class: 'hv' }, hl.backup?.lastBackupAt ? h('span', { class: `status-glyph ${hl.backup.lastBackupOk ? 'text-up' : 'text-down'}` }, icon(hl.backup.lastBackupOk ? 'check' : 'x'), relTime(hl.backup.lastBackupAt, now())) : h('span', { class: 'muted' }, 'never')), h('div', { class: 'hl' }, 'Last backup')),
    );
    body.append(gridEl);
    // A recent monitoring gap is a state of the panel, so it is flagged in the
    // band rather than set as a paragraph under the readouts, where it would be
    // the thing pushed out of a panel one row tall.
    if (hl.lastGap && Date.now() - new Date(hl.lastGap.to).getTime() < 86400e3) {
      const flag = h('span', { class: 'tag tag-warn', title: `Monitoring gap of ${duration(hl.lastGap.seconds)} ended ${relTime(hl.lastGap.to, now())}` },
        icon('moon'), `gap ${duration(hl.lastGap.seconds)}`);
      if (actions) actions.prepend(flag); else body.append(h('div', { class: 'note' }, flag));
    }
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
        h('td', { class: 'mono' }, n.host || ''),
        h('td', null, nodeGroups(n).join(', ') || h('span', { class: 'dim' }, '—')),
        h('td', null, chips),
        h('td', { class: 'muted nowrap' }, last ? relTime(last, now()) : '—')));
    }
    table.append(tb);
    body.append(h('div', { class: 'table-wrap' }, table));
  }

  /* ---------- Charts ---------- */
  function disposeChart(id) {
    const v = state.chartViews.get(id);
    if (v) { v.destroy(); state.chartViews.delete(id); }
  }
  function fetchHistory(ids, range) {
    const key = `${ids.join(',')}|${range}`;
    if (!state.historyCache.has(key)) state.historyCache.set(key, ids.length ? getHistoryMulti(ids, range) : getHistoryAuto(range));
    return state.historyCache.get(key);
  }
  function renderChart(w, body) {
    disposeChart(w.id);
    const host = h('div', { style: { flex: '1', minHeight: '0', display: 'flex', flexDirection: 'column' } });
    body.append(host);
    const cfg = chartConfigFor(w);
    // widget space is limited: legend only when the widget is tall enough
    const l = layoutOf(w);
    if (l && l.h < 2) cfg.legend = false;
    const view = renderConfiguredChart(host, cfg, { title: w.title || widgetMeta(w.type).label, fetch: fetchHistory, fill: true });
    state.chartViews.set(w.id, view);
  }

  function renderUptime(w, body) {
    const list = h('div', null, h('div', { class: 'widget-loading' }, 'Loading…'));
    body.append(list);
    state.uptimeLists.set(w.id, list);
    fillUptime(list, w);
  }

  /** Fill — or re-fill — an availability widget. The rows are built first and
   *  put in place in a single step, so a refresh never empties the widget
   *  while it waits for history to come back. */
  function fillUptime(list, w) {
    if (!list) return;
    const cfg = widgetConfig(w);
    const ids = (cfg.checkIds || []).map(Number).filter(Boolean);
    const range = cfg.range || '7d';
    fetchHistory(ids, range).then((series) => {
      if (state.destroyed || !list.isConnected) return;
      const arr = Array.isArray(series) ? series : [series];
      if (!arr.length) { replace(list, emptyState({ icon: 'activity', title: 'Nothing to show yet', compact: true })); return; }
      const rows = [];
      for (const hs of arr) {
        const avail = hs.summary?.availability;
        const cls = avail == null ? '' : avail >= 99.9 ? 'text-up' : avail >= 95 ? 'text-degraded' : 'text-down';
        rows.push(h('div', { class: 'uptime-row' },
          h('div', { class: 'uptime-name truncate' }, h('a', { href: `#/nodes/${findCheck(hs.checkId)?.node.id ?? ''}`, style: { color: 'inherit' } }, hs.nodeName || ''), h('div', { class: 'sub' }, hs.checkName)),
          uptimeBar(hs.points, { bucketSeconds: hs.bucketSeconds, from: hs.from, to: hs.to, label: `${hs.nodeName || ''} › ${hs.checkName}` }),
          h('div', { class: `uptime-pct ${cls}` }, pct(avail, 2))));
      }
      rows.push(uptimeLegend());
      replace(list, rows);
    }).catch((e) => { if (list.isConnected) replace(list, h('div', { class: 'note' }, 'Could not load history: ' + e.message)); });
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
    themeChanged() { state.chartViews.forEach((v) => v.refresh()); },
    async update(params) {
      if (state.interacting) return false;
      pickCurrent(params.id);
      state.chartViews.forEach((v) => v.destroy()); state.chartViews.clear(); state.historyCache.clear();
      render();
      return true;
    },
    destroy() {
      state.destroyed = true;
      state.chartViews.forEach((v) => v.destroy());
      state.chartViews.clear();
    },
  };
}

/* ---------- Widget editor modal ---------- */

export function openWidgetEditor(existing, state) {
  return new Promise((resolve) => {
    const isNew = !existing;
    // A new widget starts at whatever size its type asks for, the same as
    // picking that type in the list does.
    const firstType = WIDGET_TYPES[0];
    const w = existing ? { ...existing, config: { ...widgetConfig(existing) } } : { id: uid('w'), type: firstType.type, title: '', width: firstType.w, height: firstType.h, config: { ...firstType.config } };
    let result = null;
    const nodes = state.nodes || [];
    const groups = state.groups?.groups || [];
    const tags = state.groups?.tags || [];

    const picker = h('div', { class: 'widget-picker', role: 'radiogroup', 'aria-label': 'Widget type' });
    const titleInput = textInput({ value: w.title || '', placeholder: widgetMeta(w.type).label });
    const widthSel = selectInput({ options: [1, 2, 3, 4].map((n) => ({ value: n, label: `${n} column${n > 1 ? 's' : ''}` })), value: w.width || 2 });
    const heightSel = selectInput({ options: [1, 2, 3, 4, 5, 6].map((n) => ({ value: n, label: `${n} row${n > 1 ? 's' : ''}` })), value: w.height || 1 });
    const cfgArea = h('div', { class: 'stack-sm' });
    let cfgControls = {};
    let chartEditor = null;

    function renderPicker() {
      clear(picker);
      for (const t of WIDGET_TYPES) {
        picker.append(h('button', { type: 'button', class: `wp ${t.type === w.type ? 'active' : ''}`, role: 'radio', 'aria-checked': t.type === w.type ? 'true' : 'false', onclick: () => { if (w.type !== t.type) { w.type = t.type; w.config = { ...t.config }; if (isNew) { widthSel.value = String(t.w); heightSel.value = String(t.h); } titleInput.placeholder = t.label; renderPicker(); renderCfg(); } } },
          h('b', null, t.label), h('span', null, t.desc)));
      }
    }
    function renderCfg() {
      clear(cfgArea); cfgControls = {}; chartEditor = null;
      const cfg = w.config;
      const groupSel = () => selectInput({ options: [{ value: '', label: 'All groups' }, ...groups.map((g) => ({ value: g.name, label: g.name }))], value: cfg.group || '' });
      const tagSel = () => selectInput({ options: [{ value: '', label: 'Any tag' }, ...tags.map((t) => ({ value: t.name, label: t.name }))], value: cfg.tag || '' });
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
        case 'chart': case 'latency_chart': case 'response_chart': case 'loss_chart': {
          const seed = w.type === 'chart' ? cfg : { checkIds: cfg.checkIds || [], range: cfg.range || '24h', metric: w.type === 'loss_chart' ? 'loss' : (cfg.metric || 'avg'), style: 'area' };
          chartEditor = chartConfigEditor(seed, { nodes, compact: true });
          cfgArea.append(chartEditor);
          if (w.type !== 'chart') cfgArea.append(h('p', { class: 'note' }, 'Saving converts this widget to the general "Chart" type with the options above.'));
          break;
        }
        case 'uptime_chart': {
          const sel = new Set((cfg.checkIds || []).map(Number));
          const list = h('div', { class: 'check-list' });
          for (const n of nodes) for (const c of n.checks || []) list.append(checkbox({ label: `${n.name} › ${c.name}`, checked: sel.has(Number(c.id)), onChange: (v) => { if (v) sel.add(Number(c.id)); else sel.delete(Number(c.id)); } }));
          cfgControls.checkIds = () => [...sel];
          const r = selectInput({ options: ['1h', '24h', '7d', '30d', '1y'], value: cfg.range || '7d' });
          cfgControls.range = () => r.value;
          cfgArea.append(field({ label: 'Time range', input: r }), field({ label: 'Checks', input: list, help: 'Leave all unticked to let GWatch pick important checks.' }));
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
      let cfg = {};
      let type = w.type;
      if (chartEditor) { cfg = chartEditor.value; type = 'chart'; }
      else for (const [k, get] of Object.entries(cfgControls)) cfg[k] = get();
      result = { id: w.id, type, title: titleInput.value.trim(), x: existing?.x ?? null, y: existing?.y ?? null, width: Number(widthSel.value), height: Number(heightSel.value), config: cfg };
      m.close();
    }
    const m = openModal({
      title: isNew ? 'Add widget' : 'Edit widget', wide: true, body: form,
      footer: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, 'Cancel'), h('button', { class: 'btn btn-primary', type: 'button', onclick: submit }, isNew ? 'Add widget' : 'Save widget')],
      onClose: () => resolve(result),
    });
  });
}

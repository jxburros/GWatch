// Charts: a dedicated page for exploring history. Configure metric, checks,
// range and appearance, save named charts, export them and pin them to a
// dashboard as widgets.

import { api } from '../api.js';
import { h, icon, clear, replace, toast, confirmDialog, promptDialog, menuButton, emptyState, skeleton, rangeChips, busy, uid } from '../components.js';
import { chartConfigEditor, renderConfiguredChart, normalizeChartConfig, describeChartConfig, chartMetricSummary, cachedHistoryFetch, chartCsvItems } from '../chart-config.js';
import { debounce } from '../api.js';
import { TIMESTACK_PERIODS } from '../chart-config.js';

// Turning timestack on starts from the period closest to the chart's range.
const RANGE_TO_STACK = { '1h': '1h', '24h': '24h', '7d': '7d', '30d': '30d', '1y': '30d' };

export async function mount(root, ctx) {
  const state = { nodes: [], saved: [], currentId: ctx.params.id || null, cfg: normalizeChartConfig({}), dirty: false, view: null, destroyed: false, cache: new Map(), editor: null };

  const side = h('aside', { class: 'charts-side' });
  const main = h('div', { class: 'charts-main' });
  root.append(h('div', { class: 'charts-layout' }, side, main));

  async function loadAll() {
    const [nodes, saved] = await Promise.all([api.get('/api/nodes').catch(() => []), api.get('/api/charts').catch(() => [])]);
    state.nodes = nodes || []; state.saved = saved || [];
    pickCurrent();
  }
  function pickCurrent() {
    const found = state.currentId && state.saved.find((c) => c.id === state.currentId);
    if (found) { state.cfg = normalizeChartConfig(parseCfg(found.config)); state.dirty = false; }
    else if (!state.currentId && state.saved.length && !state.dirty) { state.currentId = state.saved[0].id; state.cfg = normalizeChartConfig(parseCfg(state.saved[0].config)); }
    else if (state.currentId && !found) { state.currentId = null; }
  }
  function parseCfg(c) { if (typeof c === 'string') { try { return JSON.parse(c); } catch { return {}; } } return c || {}; }
  function current() { return state.saved.find((c) => c.id === state.currentId) || null; }

  async function persist() {
    try { state.saved = await api.put('/api/charts', state.saved); } catch (e) { toast(e.message, { kind: 'error' }); throw e; }
  }
  // Latency history and each named metric are cached by what was asked for,
  // so flipping between styles or colours never refetches.
  const fetchHistory = cachedHistoryFetch(state.cache);

  /* ---------- Title / actions ---------- */
  function setTitle() {
    const c = current();
    const actions = [
      h('button', { class: 'btn', type: 'button', onclick: newChart }, icon('plus'), 'New chart'),
      h('button', { class: `btn ${state.dirty || !c ? 'btn-primary' : ''}`, type: 'button', onclick: save }, icon('save'), c ? 'Save' : 'Save as…'),
      menuButton(() => [
        c ? { label: 'Save as a copy', icon: 'copy', onClick: saveAs } : null,
        c ? { label: 'Rename', icon: 'edit', onClick: rename } : null,
        { label: 'Pin to a dashboard', icon: 'grid', onClick: pinToDashboard },
        { sep: true },
        { label: 'Export chart as PNG', icon: 'image', onClick: () => state.view?.exportPNG(`${slug(c?.name || 'chart')}-${state.cfg.range}.png`) },
        ...exportCsvItems(),
        { sep: true },
        c ? { label: 'Delete chart', icon: 'trash', danger: true, onClick: remove } : null,
      ], { label: 'Chart options' }),
    ];
    ctx.setTitle(c ? c.name : 'Charts', { actions });
  }
  function exportCsvItems() {
    return chartCsvItems(state.cfg, { nodes: state.nodes, series: state.lastSeries });
  }
  const slug = (s) => String(s).toLowerCase().replace(/[^\w]+/g, '-');

  /* ---------- Saved charts sidebar ---------- */
  function renderSide() {
    clear(side);
    const list = h('div', { class: 'saved-list' });
    if (!state.saved.length) list.append(h('div', { class: 'note', style: { padding: '8px' } }, 'No saved charts yet. Configure one below and press Save.'));
    for (const c of state.saved) {
      list.append(h('button', { type: 'button', class: `saved-item ${c.id === state.currentId ? 'active' : ''}`, onclick: () => ctx.navigate(`/charts/${c.id}`) },
        icon('chart'), h('span', { style: { minWidth: 0, flex: 1 } }, h('div', { class: 'truncate' }, c.name), h('div', { class: 'sub truncate' }, describeChartConfig(parseCfg(c.config))))));
    }
    side.append(h('section', { class: 'card saved-card' }, h('div', { class: 'card-title' }, 'Saved charts'), list));
    state.editor = chartConfigEditor(state.cfg, { nodes: state.nodes, onChange: onEdit });
    side.append(h('details', { class: 'card chart-config-fold', open: true }, h('summary', { class: 'card-title' }, 'Configure'), state.editor));
  }
  const rerender = debounce(() => renderMain(), 250);
  function onEdit(cfg) {
    const before = JSON.stringify(state.cfg);
    state.cfg = cfg;
    if (JSON.stringify(cfg) !== before) { state.dirty = true; setTitle(); rerender(); }
  }

  /* ---------- Main chart ---------- */
  function renderMain() {
    if (state.destroyed) return;
    state.view?.destroy();
    clear(main);
    const c = current();
    const host = h('div');
    const card = h('section', { class: 'card chart-card' },
      h('div', { class: 'card-head' }, h('h2', null, icon('chart'), c ? c.name : 'Untitled chart', h('span', { class: 'tag' }, chartMetricSummary(state.cfg))),
        h('div', { class: 'card-actions' },
          // A timestacked chart's chips choose the period stacked, not a range.
          state.cfg.timestack
            ? rangeChips(state.cfg.timestack.period, (p) => { state.cfg.timestack = { ...state.cfg.timestack, period: p }; state.dirty = true; renderSide(); setTitle(); renderMain(); }, { ranges: TIMESTACK_PERIODS.map((p) => p.value) })
            : rangeChips(state.cfg.range, (r) => { state.cfg.range = r; state.dirty = true; renderSide(); setTitle(); renderMain(); }),
          h('button', { type: 'button', class: `chip ${state.cfg.timestack ? 'active' : ''}`, 'aria-pressed': state.cfg.timestack ? 'true' : 'false', title: 'Overlay this metric against itself: the last period over the ones before it', onclick: toggleStack }, icon('layers'), 'Timestack'))),
      host);
    main.append(card);
    state.view = renderConfiguredChart(host, state.cfg, { title: c ? c.name : 'Chart', fetch: fetchHistory, onData: (list) => { state.lastSeries = list; } });
    main.append(h('p', { class: 'note' }, 'Tip: hover the chart for values, press ', h('b', null, 'Timestack'), ' to lay this metric over its own earlier days, weeks or months, and use ', h('b', null, 'Pin to a dashboard'), ' to keep this exact chart on a dashboard.'));
  }

  function toggleStack() {
    state.cfg = normalizeChartConfig({ ...state.cfg, timestack: state.cfg.timestack ? null : { period: RANGE_TO_STACK[state.cfg.range] || '24h', layers: 4 } });
    state.dirty = true; renderSide(); setTitle(); renderMain();
  }

  /* ---------- CRUD ---------- */
  async function newChart() {
    state.currentId = null; state.cfg = normalizeChartConfig({}); state.dirty = true;
    ctx.navigate('/charts'); render();
  }
  async function save() {
    const c = current();
    if (!c) { await saveAs(); return; }
    c.config = state.cfg; await persist(); state.dirty = false; toast('Chart saved', { kind: 'success' }); render();
  }
  async function saveAs() {
    const name = await promptDialog({ title: 'Save chart', label: 'Name', value: current()?.name ? `${current().name} copy` : '', placeholder: 'e.g. Gateway latency, All websites 7d' });
    if (!name) return;
    const entry = { id: uid('chart'), name: name.trim(), config: state.cfg };
    state.saved.push(entry);
    await persist();
    const saved = state.saved.find((x) => x.name === entry.name) || state.saved[state.saved.length - 1];
    state.currentId = saved.id; state.dirty = false;
    toast('Chart saved', { kind: 'success' });
    ctx.navigate(`/charts/${saved.id}`); render();
  }
  async function rename() {
    const c = current(); if (!c) return;
    const name = await promptDialog({ title: 'Rename chart', label: 'Name', value: c.name });
    if (!name || name.trim() === c.name) return;
    c.name = name.trim(); await persist(); toast('Chart renamed', { kind: 'success' }); render();
  }
  async function remove() {
    const c = current(); if (!c) return;
    if (!await confirmDialog({ title: `Delete "${c.name}"?`, confirmLabel: 'Delete', danger: true })) return;
    state.saved = state.saved.filter((x) => x.id !== c.id);
    await persist(); state.currentId = null; state.dirty = false; toast('Chart deleted', { kind: 'success' });
    ctx.navigate('/charts'); render();
  }
  async function pinToDashboard() {
    let dashboards = [];
    try { dashboards = await api.get('/api/dashboards'); } catch (e) { toast(e.message, { kind: 'error' }); return; }
    if (!dashboards.length) { toast('Create a dashboard first', { kind: 'error' }); return; }
    const sel = h('select', null, dashboards.map((d) => h('option', { value: d.id }, d.name)));
    const ok = await confirmDialog({ title: 'Pin chart to a dashboard', confirmLabel: 'Pin', body: h('div', { class: 'field' }, h('label', null, 'Dashboard'), sel) });
    if (!ok) return;
    const d = dashboards.find((x) => String(x.id) === sel.value);
    const widgets = [...(d.widgets || []), { id: uid('w'), type: 'chart', title: current()?.name || '', width: 2, height: 2, config: state.cfg }];
    try { await api.put(`/api/dashboards/${d.id}`, { ...d, widgets }); toast(`Pinned to ${d.name}`, { kind: 'success', action: { label: 'Open', onClick: () => ctx.navigate(`/dashboard/${d.id}`) } }); } catch (e) { toast(e.message, { kind: 'error' }); }
  }

  function render() { setTitle(); renderSide(); renderMain(); }

  try { await loadAll(); } catch (e) { replace(root, h('div', { class: 'card' }, emptyState({ icon: 'alert', title: 'Could not load charts', text: e.message }))); return { destroy() {} }; }
  render();

  return {
    refresh() { state.cache.clear(); state.view?.refresh(); },
    themeChanged() { renderMain(); },
    async update(params) {
      if (params.id !== state.currentId) { state.currentId = params.id || null; state.dirty = false; pickCurrent(); if (!params.id) state.cfg = state.cfg; render(); }
      return true;
    },
    destroy() { state.destroyed = true; state.view?.destroy(); },
  };
}

export { skeleton, busy };

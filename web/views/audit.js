// Audit: everything GWatch recorded — the full event log with filters and
// search, the service log, and one place to export logs, history and config.

import { api, qs } from '../api.js';
import { h, icon, clear, replace, eventIcon, eventMeta, EVENT_META, toast, field, textInput, selectInput, checkbox, emptyState, skeleton, busy } from '../components.js';
import { dateTime, relTime, timeShort, toLocalInput, fromLocalInput, num } from '../fmt.js';

const TABS = [{ id: 'events', label: 'Event log' }, { id: 'log', label: 'Service log' }, { id: 'exports', label: 'Exports' }];
const PAGE = 200;
const TYPE_OPTIONS = [{ value: '', label: 'All event types' }, ...Object.entries(EVENT_META).map(([value, m]) => ({ value, label: m.label }))];

export async function mount(root, ctx) {
  const state = { tab: TABS.some((t) => t.id === ctx.params.tab) ? ctx.params.tab : 'events', nodes: [], destroyed: false, refresh: null };
  const tabs = h('div', { class: 'tabs', role: 'tablist' });
  const panel = h('div');
  root.append(tabs, panel);

  function renderTabs() {
    clear(tabs);
    for (const t of TABS) tabs.append(h('a', { class: `tab ${t.id === state.tab ? 'active' : ''}`, role: 'tab', 'aria-selected': t.id === state.tab ? 'true' : 'false', href: `#/audit/${t.id}` }, t.label));
  }

  async function loadNodes() { try { state.nodes = await api.get('/api/nodes'); } catch { state.nodes = []; } }

  async function renderTab() {
    renderTabs();
    ctx.setTitle('Audit', { subtitle: { events: 'Every recorded event, searchable and exportable', log: 'What the service itself is doing', exports: 'Download history, events, logs and configuration' }[state.tab] });
    replace(panel, skeleton({ lines: 5 }));
    state.refresh = null;
    try {
      const el = await ({ events: tabEvents, log: tabLog, exports: tabExports })[state.tab]();
      if (!state.destroyed) replace(panel, el);
    } catch (e) { replace(panel, h('div', { class: 'card' }, emptyState({ icon: 'alert', title: 'Could not load', text: e.message }))); }
  }

  /* ---------- Event log ---------- */
  async function tabEvents() {
    const f = { q: ctx.query.get('q') || '', type: ctx.query.get('type') || '', nodeId: ctx.query.get('nodeId') || '', since: '', until: '' };
    const events = [];
    let hasMore = true;
    const q = h('input', { type: 'search', value: f.q, placeholder: 'Search title, detail, node or check…', 'aria-label': 'Search events' });
    const type = selectInput({ options: TYPE_OPTIONS, value: f.type });
    const node = selectInput({ options: [{ value: '', label: 'All nodes' }, ...state.nodes.map((n) => ({ value: n.id, label: n.name }))], value: f.nodeId });
    const since = h('input', { type: 'datetime-local', value: '' });
    const until = h('input', { type: 'datetime-local', value: '' });
    const applyBtn = h('button', { class: 'btn btn-primary', type: 'button', onclick: () => reload() }, icon('filter'), 'Apply');
    const exportBtn = h('a', { class: 'btn', download: 'gwatch-events.csv', href: '#' }, icon('download'), 'Export CSV');
    const listEl = h('div');
    const foot = h('div', { class: 'row-between', style: { marginTop: '10px' } });
    const countEl = h('span', { class: 'note' });
    const params = () => ({ q: q.value.trim(), type: type.value, nodeId: node.value, since: fromLocalInput(since.value), until: fromLocalInput(until.value) });
    const syncExport = () => { exportBtn.href = `/api/export/events.csv${qs({ ...params(), limit: 5000 })}`; };
    for (const el of [q, type, node, since, until]) { el.addEventListener('change', syncExport); }
    q.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); reload(); } });
    type.addEventListener('change', () => reload()); node.addEventListener('change', () => reload());
    syncExport();

    async function fetchPage(before) { return api.get(`/api/events${qs({ ...params(), limit: PAGE, before })}`); }
    async function reload() {
      events.length = 0; hasMore = true;
      replace(listEl, skeleton({ lines: 6 }));
      try { const rows = await fetchPage(null); if (state.destroyed) return; events.push(...(rows || [])); hasMore = events.length >= PAGE; render(); }
      catch (e) { replace(listEl, h('div', { class: 'card' }, emptyState({ icon: 'alert', title: 'Could not load events', text: e.message }))); }
    }
    async function loadMore(btn) {
      const done = busy(btn, 'Loading…');
      try { const rows = await fetchPage(events[events.length - 1]?.id); events.push(...(rows || [])); hasMore = (rows || []).length >= PAGE; render(); } catch (e) { toast(e.message, { kind: 'error' }); done(); }
    }
    function render() {
      clear(listEl); clear(foot);
      if (!events.length) { listEl.append(h('div', { class: 'card' }, emptyState({ icon: 'audit', title: 'No events match', text: 'Try a different search, type, node or time window.', compact: true }))); return; }
      const table = h('table', { class: 'table audit-table' }, h('thead', null, h('tr', null, h('th', null, 'Time'), h('th', null, 'Type'), h('th', null, 'Node › check'), h('th', null, 'Event'), h('th', null, 'Who'))));
      const tb = h('tbody');
      for (const ev of events) {
        const m = eventMeta(ev.type);
        tb.append(h('tr', null,
          h('td', { class: 'mono nowrap', title: dateTime(ev.ts) }, dateTime(ev.ts, { seconds: true }), h('div', { class: 'dim tiny' }, relTime(ev.ts))),
          h('td', null, h('div', { class: 'row', style: { gap: '6px', flexWrap: 'nowrap' } }, eventIcon(ev.type), h('span', { class: 'ev-type' }, m.label))),
          h('td', null, ev.nodeName ? h('a', { href: ev.nodeId ? `#/nodes/${ev.nodeId}` : null }, ev.nodeName, ev.checkName ? ` › ${ev.checkName}` : '') : h('span', { class: 'dim' }, '—')),
          h('td', null, h('div', { class: 'strong' }, ev.title || m.label), ev.detail ? h('div', { class: 'ev-detail' }, ev.detail) : null),
          // Empty for anything the monitoring engine did on its own.
          h('td', { class: 'nowrap' }, ev.actor
            ? h('span', { class: 'row', style: { gap: '5px', flexWrap: 'nowrap' } }, icon('user'), ev.actor)
            : h('span', { class: 'dim' }, '—')),
        ));
      }
      table.append(tb);
      listEl.append(h('div', { class: 'card', style: { padding: '0 10px' } }, h('div', { class: 'table-wrap' }, table)));
      countEl.textContent = `${num(events.length)} event${events.length === 1 ? '' : 's'} shown${hasMore ? ' — there are more' : ''}`;
      foot.append(countEl);
      if (hasMore) { const b = h('button', { class: 'btn', type: 'button', onclick: () => loadMore(b) }, 'Load more'); foot.append(b); }
    }
    await reload();
    state.refresh = async () => {
      try { const rows = await fetchPage(null); if (state.destroyed) return; const seen = new Set(events.map((e) => e.id)); const fresh = (rows || []).filter((e) => !seen.has(e.id)); if (fresh.length) { events.unshift(...fresh); render(); } } catch { /* ignore */ }
    };
    return h('div', null,
      h('section', { class: 'filter-bar', 'aria-label': 'Filter the audit log' }, h('div', { class: 'audit-toolbar' }, field({ label: 'Search', input: q }), field({ label: 'Type', input: type }), field({ label: 'Node', input: node }), field({ label: 'From', input: since }), field({ label: 'Until', input: until }), h('div', { class: 'btn-group' }, applyBtn, exportBtn))),
      listEl, foot);
  }

  /* ---------- Service log ---------- */
  async function tabLog() {
    const box = h('pre', { class: 'log-box', tabindex: 0, 'aria-label': 'Log output' });
    const limit = selectInput({ options: [100, 200, 500, 1000].map((n) => ({ value: n, label: `Last ${n} lines` })), value: 200 });
    const level = selectInput({ options: [{ value: '', label: 'All levels' }, { value: 'warn', label: 'Warnings and errors' }, { value: 'error', label: 'Errors only' }], value: '' });
    const search = h('input', { type: 'search', placeholder: 'Filter lines…', 'aria-label': 'Filter log lines' });
    const follow = checkbox({ label: 'Follow', checked: true });
    const fileEl = h('div', { class: 'mono small muted' });
    let lines = [];
    const render = () => {
      clear(box);
      const needle = search.value.trim().toLowerCase();
      let shown = 0;
      for (const line of lines) {
        const isErr = /\b(ERROR|error|panic)\b/.test(line); const isWarn = /\b(WARN|warning)\b/i.test(line);
        if (level.value === 'error' && !isErr) continue;
        if (level.value === 'warn' && !isErr && !isWarn) continue;
        if (needle && !line.toLowerCase().includes(needle)) continue;
        box.append(h('span', { class: isErr ? 'lvl-error' : isWarn ? 'lvl-warn' : '' }, line + '\n'));
        shown++;
      }
      if (!shown) box.textContent = lines.length ? '(no lines match the filter)' : '(log is empty)';
      if (follow.input.checked) box.scrollTop = box.scrollHeight;
    };
    const load = async () => {
      const data = await api.get(`/api/logs${qs({ limit: limit.value })}`);
      if (state.destroyed) return;
      lines = data.lines || [];
      fileEl.textContent = data.file ? `File: ${data.file}` : 'File logging is off; showing the in-memory buffer.';
      render();
    };
    limit.addEventListener('change', load);
    level.addEventListener('change', render);
    search.addEventListener('input', render);
    const refreshBtn = h('button', { class: 'btn btn-sm', type: 'button', onclick: load }, icon('refresh'), 'Refresh');
    const dl = h('a', { class: 'btn btn-sm', href: '/api/export/logs.txt?limit=1000', download: 'gwatch.log' }, icon('download'), 'Download');
    await load();
    state.refresh = load;
    return h('section', { class: 'card' },
      h('div', { class: 'log-toolbar' }, h('div', { class: 'search' }, search), level, limit, follow, refreshBtn, dl),
      box, h('div', { style: { marginTop: '8px' } }, fileEl));
  }

  /* ---------- Exports ---------- */
  async function tabExports() {
    const checks = [];
    for (const n of state.nodes) for (const c of n.checks || []) checks.push({ value: c.id, label: `${n.name} › ${c.name}` });
    const histCheck = selectInput({ options: checks.length ? checks : [{ value: '', label: 'No checks yet' }], value: checks[0]?.value ?? '' });
    const histRange = selectInput({ options: ['1h', '24h', '7d', '30d', '1y'], value: '30d' });
    const histBtn = h('a', { class: 'btn btn-primary', download: 'history.csv' }, icon('download'), 'Download history CSV');
    const resCheck = selectInput({ options: checks.length ? checks : [{ value: '', label: 'No checks yet' }], value: checks[0]?.value ?? '' });
    const resLimit = selectInput({ options: [500, 1000, 5000].map((n) => ({ value: n, label: `Last ${n} results` })), value: 5000 });
    const resBtn = h('a', { class: 'btn btn-primary', download: 'results.csv' }, icon('download'), 'Download results CSV');
    const evNode = selectInput({ options: [{ value: '', label: 'All nodes' }, ...state.nodes.map((n) => ({ value: n.id, label: n.name }))], value: '' });
    const evSince = h('input', { type: 'datetime-local', value: toLocalInput(new Date(Date.now() - 30 * 86400e3)) });
    const evBtn = h('a', { class: 'btn btn-primary', download: 'events.csv' }, icon('download'), 'Download events CSV');
    const sync = () => {
      histBtn.href = `/api/export/history.csv${qs({ checkId: histCheck.value, range: histRange.value })}`;
      resBtn.href = `/api/export/results.csv${qs({ checkId: resCheck.value, limit: resLimit.value })}`;
      evBtn.href = `/api/export/events.csv${qs({ nodeId: evNode.value, since: fromLocalInput(evSince.value), limit: 5000 })}`;
      histBtn.classList.toggle('btn-disabled', !histCheck.value); resBtn.classList.toggle('btn-disabled', !resCheck.value);
    };
    for (const el of [histCheck, histRange, resCheck, resLimit, evNode, evSince]) el.addEventListener('change', sync);
    sync();
    const card = (title, ic, desc, ...rest) => h('div', { class: 'export-card' }, h('h3', null, icon(ic), title), h('p', null, desc), ...rest);
    return h('div', { class: 'export-grid' },
      card('Chart history', 'chart', 'Aggregated points (average / min / max / loss / availability) for one check over a time range.', h('div', { class: 'form-grid' }, field({ label: 'Check', input: histCheck }), field({ label: 'Range', input: histRange })), h('div', null, histBtn)),
      card('Raw results', 'activity', 'Every individual check run with message, timings, status code and final URL.', h('div', { class: 'form-grid' }, field({ label: 'Check', input: resCheck }), field({ label: 'How many', input: resLimit })), h('div', null, resBtn)),
      card('Events', 'audit', 'The event log as CSV: outages, recoveries, alerts, maintenance, configuration changes, triggers, notes.', h('div', { class: 'form-grid' }, field({ label: 'Node', input: evNode }), field({ label: 'Since', input: evSince })), h('div', null, evBtn)),
      card('Service log', 'terminal', 'The last 1000 lines the service wrote (start/stop, errors, trigger runs).', h('div', null, h('a', { class: 'btn btn-primary', href: '/api/export/logs.txt?limit=1000', download: 'gwatch.log' }, icon('download'), 'Download log'))),
      card('Configuration', 'file', 'Nodes, checks, dashboards, maintenance windows, triggers and endpoints as JSON (no SMTP password). For a restorable copy use an encrypted backup.', h('div', { class: 'btn-group' }, h('a', { class: 'btn btn-primary', href: '/api/export/config.json', download: 'gwatch-config.json' }, icon('download'), 'Download JSON'), h('a', { class: 'btn', href: '#/settings/backups' }, icon('save'), 'Backups'))),
    );
  }

  await loadNodes();
  await renderTab();
  return {
    refresh: () => state.refresh && state.refresh(),
    async update(params) { const tab = TABS.some((t) => t.id === params.tab) ? params.tab : 'events'; if (tab !== state.tab) { state.tab = tab; await renderTab(); } return true; },
    destroy() { state.destroyed = true; },
  };
}

export { timeShort, textInput };

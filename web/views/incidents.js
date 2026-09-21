// Incident timeline: every event grouped by day with filters and notes.

import { api, qs } from '../api.js';
import { h, icon, clear, replace, eventIcon, eventMeta, EVENT_META, toast, openModal, field, selectInput, textarea, emptyState, skeleton } from '../components.js';
import { dayHeading, dayKey, timeShort, relTime, metricLabel } from '../fmt.js';

const TYPE_GROUPS = [
  { value: '', label: 'All events' },
  { value: 'down', label: 'Went down' },
  { value: 'recovered', label: 'Recovered' },
  { value: 'warning', label: 'Warnings' },
  { value: 'cert_warning', label: 'Certificate warnings' },
  { value: 'content_changed', label: 'Response changed' },
  { value: 'alert_sent', label: 'Alerts sent' },
  { value: 'alert_suppressed', label: 'Alerts suppressed' },
  { value: 'alert_failed', label: 'Alerts failed' },
  { value: 'silenced', label: 'Silenced' },
  { value: 'maintenance_began', label: 'Maintenance began' },
  { value: 'maintenance_ended', label: 'Maintenance ended' },
  { value: 'affected_by_parent', label: 'Affected by parent' },
  { value: 'config_changed', label: 'Configuration changes' },
  { value: 'service_started', label: 'Service started' },
  { value: 'service_stopped', label: 'Service stopped' },
  { value: 'monitor_gap', label: 'Monitoring gaps' },
  { value: 'internal_error', label: 'Internal errors' },
  { value: 'backup', label: 'Backups' },
  { value: 'restore', label: 'Restores' },
  { value: 'retention', label: 'Retention' },
  { value: 'note', label: 'Notes' },
];
const PAGE = 100;

export async function mount(root, ctx) {
  const state = { events: [], nodes: [], type: ctx.query.get('type') || '', nodeId: ctx.query.get('nodeId') || '', hasMore: true, loading: false, destroyed: false };

  const typeSel = selectInput({ options: TYPE_GROUPS, value: state.type, 'aria-label': 'Event type', onchange: () => { state.type = typeSel.value; reload(); } });
  const nodeSel = selectInput({ options: [{ value: '', label: 'All nodes' }], value: '', 'aria-label': 'Node', onchange: () => { state.nodeId = nodeSel.value; reload(); } });
  const toolbar = h('section', { class: 'filter-bar', 'aria-label': 'Filter events' }, h('div', { class: 'toolbar' }, h('div', { style: { minWidth: '220px' } }, typeSel), h('div', { style: { minWidth: '220px' } }, nodeSel)));
  const listEl = h('div', null, skeleton({ lines: 5 }));
  const moreWrap = h('div', { style: { display: 'flex', justifyContent: 'center', marginTop: '8px' } });
  root.append(toolbar, listEl, moreWrap);

  ctx.setTitle('Incidents', {
    actions: [
      h('a', { class: 'btn', href: '#/audit/events' }, icon('audit'), 'Full audit log'),
      h('a', { class: 'btn', href: `/api/export/events.csv${qs({ nodeId: state.nodeId || null, limit: 5000 })}`, download: 'events.csv' }, icon('download'), 'Export CSV'),
      h('button', { class: 'btn btn-primary', type: 'button', onclick: addNote }, icon('note'), 'Add note'),
    ],
  });

  async function loadNodes() {
    try {
      state.nodes = await api.get('/api/nodes');
      clear(nodeSel);
      nodeSel.append(h('option', { value: '' }, 'All nodes'));
      for (const n of state.nodes) nodeSel.append(h('option', { value: n.id }, n.name));
      nodeSel.value = state.nodeId;
    } catch { /* keep the "all nodes" option */ }
  }

  async function fetchPage(before) {
    return api.get(`/api/events${qs({ limit: PAGE, before, type: state.type, nodeId: state.nodeId })}`);
  }
  async function reload() {
    state.events = []; state.hasMore = true;
    replace(listEl, skeleton({ lines: 5 }));
    try {
      const rows = await fetchPage(null);
      if (state.destroyed) return;
      state.events = rows || [];
      state.hasMore = state.events.length >= PAGE;
      render();
    } catch (e) { replace(listEl, h('div', { class: 'card' }, emptyState({ icon: 'alert', title: 'Could not load events', text: e.message }))); }
  }
  async function loadMore(btn) {
    if (!state.events.length) return;
    btn.disabled = true; btn.textContent = 'Loading…';
    try {
      const last = state.events[state.events.length - 1];
      const rows = await fetchPage(last.id);
      state.events.push(...(rows || []));
      state.hasMore = (rows || []).length >= PAGE;
      render();
    } catch (e) { toast(e.message, { kind: 'error' }); btn.disabled = false; btn.textContent = 'Load more'; }
  }
  async function refresh() {
    // Pull the newest page and merge unseen events at the top.
    try {
      const rows = await fetchPage(null);
      if (state.destroyed) return;
      const seen = new Set(state.events.map((e) => e.id));
      const fresh = (rows || []).filter((e) => !seen.has(e.id));
      if (fresh.length) { state.events = [...fresh, ...state.events]; render(); }
    } catch { /* ignore transient errors */ }
  }

  function render() {
    clear(listEl); clear(moreWrap);
    if (!state.events.length) {
      listEl.append(h('div', { class: 'card' }, emptyState({ icon: 'activity', title: 'No events yet', text: state.type || state.nodeId ? 'Nothing matches these filters.' : 'Outages, recoveries, alerts and maintenance will appear here as they happen.' })));
      return;
    }
    const days = new Map();
    for (const ev of state.events) { const k = dayKey(ev.ts); if (!days.has(k)) days.set(k, []); days.get(k).push(ev); }
    for (const [, evs] of days) {
      const sec = h('section', { class: 'timeline-day' }, h('h2', null, dayHeading(evs[0].ts)));
      const tl = h('div', { class: 'timeline' });
      for (const ev of evs) tl.append(row(ev));
      sec.append(tl);
      listEl.append(sec);
    }
    if (state.hasMore) {
      const btn = h('button', { class: 'btn', type: 'button', onclick: () => loadMore(btn) }, 'Load more');
      moreWrap.append(btn);
    }
  }

  function row(ev) {
    const m = eventMeta(ev.type);
    const detail = [];
    if (ev.detail) detail.push(ev.detail);
    let reason = null;
    try { const meta = ev.meta ? (typeof ev.meta === 'string' ? JSON.parse(ev.meta) : ev.meta) : null; if (meta?.reason) reason = meta.reason; } catch { /* ignore */ }
    if (ev.type === 'alert_suppressed' && reason && !(ev.detail || '').toLowerCase().includes(String(reason).toLowerCase())) detail.push(`suppressed — ${reason}`);
    return h('article', { class: 'event-row' },
      eventIcon(ev.type),
      h('div', { class: 'ev-body' },
        h('div', { class: 'row', style: { gap: '8px' } }, h('span', { class: 'ev-type' }, m.label), ev.nodeName ? h('a', { class: 'ev-node', href: `#/nodes/${ev.nodeId}` }, ev.nodeName, ev.checkName ? ` › ${ev.checkName}` : '') : null,
          // The metric an event is about, when it is about one (a hardware
          // check's disk rather than the check as a whole).
          ev.metric ? h('span', { class: 'tag ev-metric', title: ev.metric }, metricLabel(ev.metric)) : null),
        h('div', { class: 'ev-title' }, ev.title || m.label),
        detail.length ? h('div', { class: 'ev-detail' }, detail.join(' · ')) : null),
      h('div', { class: 'ev-time', title: new Date(ev.ts).toLocaleString() }, timeShort(ev.ts, { seconds: true }), h('div', { class: 'dim' }, relTime(ev.ts))));
  }

  async function addNote() {
    const text = textarea({ placeholder: 'e.g. Rebooted the router, ISP outage reported, updated Plex…', rows: 3 });
    const nodeOpt = selectInput({ options: [{ value: '', label: 'General (no node)' }, ...state.nodes.map((n) => ({ value: n.id, label: n.name }))], value: state.nodeId || '' });
    const form = h('form', { class: 'stack-sm', onsubmit: (e) => { e.preventDefault(); submit(); } }, field({ label: 'Note', input: text }), field({ label: 'Related node', input: nodeOpt }));
    const m = openModal({ title: 'Add a note to the timeline', body: form, footer: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, 'Cancel'), h('button', { class: 'btn btn-primary', type: 'button', onclick: () => submit() }, 'Add note')] });
    async function submit() {
      if (!text.value.trim()) { text.focus(); return; }
      try {
        await api.post('/api/events/note', { nodeId: nodeOpt.value ? Number(nodeOpt.value) : null, text: text.value.trim() });
        m.close(); toast('Note added', { kind: 'success' }); reload();
      } catch (e) { toast(e.message, { kind: 'error' }); }
    }
    setTimeout(() => text.focus(), 50);
  }

  await Promise.all([loadNodes(), reload()]);
  return { refresh, destroy() { state.destroyed = true; } };
}

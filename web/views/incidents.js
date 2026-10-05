// Incident lifecycle, with the raw event feed kept as a separate view.
import { api, qs } from '../api.js';
import { h, replace, selectInput, textarea, field, toast, openModal, emptyState, eventRow, preserveFocus } from '../components.js';
import { relTime, duration, dateTime } from '../fmt.js';
import * as eventsView from './incident-events.js';
import * as reportsView from './reports.js';

export async function mount(root, ctx) {
  const tab = ctx.query.get('tab') || (ctx.query.has('type') ? 'events' : 'active');
  const tabs = h('nav', { class: 'tabs', 'aria-label': 'Incident sections' },
    ...[['active', 'Incidents'], ['events', 'Events'], ['reports', 'Reports']].map(([id, name]) => h('a', { href: `#/incidents?tab=${id}`, class: tab === id ? 'tab active' : 'tab', 'aria-current': tab === id ? 'page' : null }, name)));
  const body = h('div', { class: 'stack' }); root.append(tabs, body);
  if (tab === 'events') return eventsView.mount(body, ctx);
  if (tab === 'reports') return reportsView.mount(body, ctx);
  ctx.setTitle('Incidents');
  let destroyed = false, request = 0;
  const filter = selectInput({ 'aria-label': 'Incident state', value: 'active', options: ['active', 'all', 'open', 'acknowledged', 'resolved'].map((value) => ({ value, label: value[0].toUpperCase() + value.slice(1) })), onchange: () => load() });
  const list = h('div', { class: 'stack' }); body.append(h('div', { class: 'toolbar' }, filter), list);
  async function load() {
    const token = ++request;
    try {
      const rows = await api.get(`/api/incidents${qs({ state: filter.value })}`);
      if (destroyed || token !== request) return;
      const restore = preserveFocus(list);
      replace(list, ...(rows || []).map((i) => h('article', { class: 'card' },
        h('div', { class: 'row-between' }, h('h2', null, h('a', { href: `#/nodes/${i.nodeId}` }, i.nodeName)), h('span', { class: 'pill' }, i.state)),
        h('p', { class: 'note' }, `Opened ${relTime(i.openedAt)} · ${duration(i.durationSeconds)} · ${(i.checkIds || []).length} checks`),
        i.acknowledgedAt ? h('p', { class: 'note' }, `Acknowledged by ${i.acknowledgedBy || 'operator'} ${relTime(i.acknowledgedAt)}`) : null,
        h('button', { class: 'btn', type: 'button', 'data-focus-key': `incident:${i.id}`, onclick: () => detail(i.id) }, 'Timeline and notes'))));
      if (!rows?.length) list.append(emptyState({ icon: 'check', title: 'No incidents match', text: filter.value === 'active' ? 'There are no open or acknowledged outages.' : 'Try another incident state.' }));
      restore();
    } catch (e) { if (!destroyed && token === request) replace(list, emptyState({ icon: 'alert', title: 'Could not load incidents', text: e.message })); }
  }
  async function detail(id) {
    try {
      const { incident: i, events } = await api.get(`/api/incidents/${id}`);
      if (destroyed) return;
      const note = textarea({ rows: 3, placeholder: 'What happened or what you changed…' });
      const contents = h('div', { class: 'stack' },
        h('p', null, `${i.state} · opened ${dateTime(i.openedAt)} · duration ${duration(i.durationSeconds)}`),
        i.acknowledgedAt ? h('p', { class: 'note' }, `Acknowledged by ${i.acknowledgedBy || 'operator'} at ${dateTime(i.acknowledgedAt)}`) : null,
        i.resolvedAt ? h('p', { class: 'note' }, `Resolved by ${i.resolvedBy || 'monitor'} at ${dateTime(i.resolvedAt)}`) : null,
        i.timeToAcknowledgeSeconds != null ? h('p', { class: 'note' }, `Time to acknowledge: ${duration(i.timeToAcknowledgeSeconds)}`) : null,
        i.timeToResolveSeconds != null ? h('p', { class: 'note' }, `Time to resolve: ${duration(i.timeToResolveSeconds)}`) : null,
        h('h3', null, 'Notes'), ...(i.notes || []).map((n) => h('p', null, h('b', null, `${n.actor || 'Operator'} · ${dateTime(n.at)}: `), n.text)),
        h('h3', null, 'Timeline'), ...(events || []).map((e) => eventRow(e)),
        ctx.me?.canWrite ? field({ label: 'Note', input: note }) : null);
      const action = (name, label) => h('button', { type: 'button', class: 'btn', onclick: async (ev) => {
        if (name === 'note' && !note.value.trim()) { note.focus(); return; }
        ev.currentTarget.disabled = true;
        try { await api.post(`/api/incidents/${id}/${name}`, { note: note.value.trim() }); modal.close(); await load(); toast('Incident updated', { kind: 'success' }); }
        catch (e) { ev.currentTarget.disabled = false; toast(e.message, { kind: 'error' }); }
      } }, label);
      const modal = openModal({ title: `${i.nodeName} — incident`, body: contents, footer: ctx.me?.canWrite ? [i.state === 'open' ? action('acknowledge', 'Acknowledge') : null, i.state !== 'resolved' ? action('resolve', 'Resolve') : null, action('note', 'Add note')] : [] });
    } catch (e) { toast(e.message, { kind: 'error' }); }
  }
  await load();
  return { refresh: load, destroy() { destroyed = true; request++; } };
}

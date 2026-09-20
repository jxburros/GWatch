// Nodes list: searchable, filterable list of everything being monitored.

import { api } from '../api.js';
import { h, icon, clear, replace, statusSpine, statusWord, checkChip, importanceBadge, tagList, toggle, menuButton, toast, confirmDialog, openModal, emptyState, skeleton } from '../components.js';
import { relTime } from '../fmt.js';
import { pairMachine } from './machines.js';

const STATUS_ORDER = ['down', 'degraded', 'unknown', 'maintenance', 'up', 'paused'];

export async function mount(root, ctx) {
  const state = { nodes: [], groups: { groups: [], tags: [] }, templates: null, q: ctx.query.get('q') || '', group: ctx.query.get('group') || '', status: ctx.query.get('status') || '', tag: ctx.query.get('tag') || '', destroyed: false };

  // A machine is a node too, so pairing one starts from here rather than from
  // a hardware section of its own.
  ctx.setTitle('Nodes', {
    actions: [
      h('button', { class: 'btn admin-only', type: 'button', onclick: () => pairMachine(load) }, icon('cpu'), 'Pair a machine'),
      h('button', { class: 'btn btn-primary admin-only', type: 'button', onclick: () => openTemplatePicker(state, ctx) }, icon('plus'), 'Add node'),
    ],
  });

  const searchInput = h('input', { type: 'search', placeholder: 'Search name, host, group or tag…', value: state.q, 'aria-label': 'Search nodes', oninput: () => { state.q = searchInput.value; renderList(); } });
  const countEl = h('span', { class: 'filter-count' });
  const toolbar = h('div', { class: 'toolbar' }, h('div', { class: 'search' }, icon('search'), searchInput));
  const filters = h('div', { class: 'filters' });
  // Search and filters belong to the same control: one panel above the list,
  // wearing the same label band as every other panel. The match count rides in
  // the band, where it reads as a readout of the filters rather than a control.
  const band = h('div', { class: 'card-head' }, h('h2', { class: 'card-title' }, 'Filters'), countEl);
  const controls = h('section', { class: 'filter-bar', 'aria-label': 'Filter nodes' }, band, toolbar, filters);
  const listEl = h('div', { class: 'node-list' }, skeleton({ lines: 4 }));
  root.append(controls, listEl);

  async function load() {
    const [nodes, groups] = await Promise.all([api.get('/api/nodes'), api.get('/api/groups').catch(() => ({ groups: [], tags: [] }))]);
    if (state.destroyed) return;
    state.nodes = nodes || []; state.groups = groups || { groups: [], tags: [] };
    renderFilters(); renderList();
  }

  function chipRow(label, items, current, onPick) {
    if (!items.length) return null;
    const row = h('div', { class: 'chip-row', role: 'group', 'aria-label': label }, h('span', { class: 'chip-label' }, label));
    row.append(h('button', { type: 'button', class: `chip ${!current ? 'active' : ''}`, 'aria-pressed': !current ? 'true' : 'false', onclick: () => onPick('') }, 'All'));
    for (const it of items) row.append(h('button', { type: 'button', class: `chip ${current === it.value ? 'active' : ''}`, 'aria-pressed': current === it.value ? 'true' : 'false', onclick: () => onPick(current === it.value ? '' : it.value) }, it.label));
    return row;
  }

  function renderFilters() {
    clear(filters);
    const counts = {};
    for (const n of state.nodes) counts[n.status || 'unknown'] = (counts[n.status || 'unknown'] || 0) + 1;
    filters.append(
      chipRow('Group', state.groups.groups.map((g) => ({ value: g.name, label: `${g.name} (${g.count})` })), state.group, (v) => { state.group = v; renderFilters(); renderList(); }),
      chipRow('Status', STATUS_ORDER.filter((s) => counts[s]).map((s) => ({ value: s, label: `${s[0].toUpperCase()}${s.slice(1)} (${counts[s]})` })), state.status, (v) => { state.status = v; renderFilters(); renderList(); }),
      chipRow('Tag', state.groups.tags.map((t) => ({ value: t.name, label: t.name })), state.tag, (v) => { state.tag = v; renderFilters(); renderList(); }),
    );
  }

  function matches(n) {
    if (state.group && n.group !== state.group) return false;
    if (state.status && (n.status || 'unknown') !== state.status) return false;
    if (state.tag && !(n.tags || []).includes(state.tag)) return false;
    if (state.q) {
      const q = state.q.toLowerCase();
      const hay = [n.name, n.host, n.group, ...(n.tags || []), ...(n.checks || []).map((c) => c.name)].filter((x) => x != null).join(' ').toLowerCase();
      if (!hay.includes(q)) return false;
    }
    return true;
  }

  function renderList() {
    clear(listEl);
    if (!state.nodes.length) {
      countEl.textContent = '';
      listEl.append(h('div', { class: 'card' }, emptyState({ icon: 'server', title: 'No nodes yet', text: 'Add your router, a website or a home server. Templates fill in sensible checks for you.', actions: h('button', { class: 'btn btn-primary admin-only', type: 'button', onclick: () => openTemplatePicker(state, ctx) }, icon('plus'), 'Add your first node') })));
      return;
    }
    const rows = state.nodes.filter(matches);
    countEl.textContent = rows.length === state.nodes.length
      ? `${state.nodes.length} node${state.nodes.length === 1 ? '' : 's'}`
      : `${rows.length} of ${state.nodes.length} nodes`;
    if (!rows.length) {
      listEl.append(h('div', { class: 'card' }, emptyState({ icon: 'search', title: 'No nodes match', text: 'Try a different search or clear the filters.', compact: true, actions: h('button', { class: 'btn btn-sm', type: 'button', onclick: () => { state.q = ''; state.group = ''; state.status = ''; state.tag = ''; searchInput.value = ''; renderFilters(); renderList(); } }, 'Clear filters') })));
      return;
    }
    // group sections
    const byGroup = new Map();
    for (const n of rows) { const g = n.group || 'Ungrouped'; if (!byGroup.has(g)) byGroup.set(g, []); byGroup.get(g).push(n); }
    const groupNames = [...byGroup.keys()].sort((a, b) => (a === 'Ungrouped') - (b === 'Ungrouped') || a.localeCompare(b));
    for (const g of groupNames) {
      const nodes = byGroup.get(g).sort((a, b) => STATUS_ORDER.indexOf(a.status || 'unknown') - STATUS_ORDER.indexOf(b.status || 'unknown') || a.name.localeCompare(b.name));
      const section = h('section', { class: 'node-group', 'aria-label': g });
      if (groupNames.length > 1 || g !== 'Ungrouped') section.append(h('div', { class: 'node-group-head' }, h('h2', null, g), h('span', { class: 'count' }, `${nodes.length} node${nodes.length === 1 ? '' : 's'}`)));
      const list = h('div', { class: 'node-rows' });
      for (const n of nodes) list.append(nodeRow(n));
      section.append(list);
      listEl.append(section);
    }
  }

  function nodeRow(n) {
    const states = n.stateByCheck || {};
    const chips = h('div', { class: 'check-chips' });
    for (const c of n.checks || []) chips.append(checkChip(c, states[c.id], { href: `#/nodes/${n.id}` }));
    if (!(n.checks || []).length) chips.append(h('span', { class: 'dim small' }, 'No checks'));
    const last = Object.values(states).map((s) => s?.lastRunAt).filter(Boolean).sort().pop();
    // Left visible for a viewer — whether a node is paused is worth seeing —
    // but not operable, since the server would refuse the change anyway.
    const enabledToggle = toggle({ checked: n.enabled !== false, ariaLabel: `${n.name} enabled`, disabled: !ctx.me?.isAdmin, onChange: (v) => setEnabled(n, v) });
    const row = h('article', { class: `node-row ${n.enabled === false ? 'disabled' : ''}`, 'aria-label': n.name },
      statusSpine(n.status || 'unknown', { key: `node:${n.id}` }),
      h('div', null, statusWord(n.status || 'unknown'), n.inMaintenance && n.status !== 'maintenance' ? h('div', { class: 'tiny text-maintenance', style: { marginTop: '4px' } }, 'in maintenance') : null),
      h('div', { class: 'n-name' }, h('a', { href: `#/nodes/${n.id}` }, n.name || 'Unnamed node'), n.host ? h('span', { class: 'n-host' }, n.host) : h('span', { class: 'n-host dim' }, 'targets set per check'), tagList(n.tags || [])),
      chips,
      h('div', { class: 'n-meta' }, h('span', null, last ? `Checked ${relTime(last)}` : 'Not checked yet'), importanceBadge(n.importance)),
      h('div', { class: 'n-actions' }, enabledToggle, menuButton(() => rowMenu(n), { label: `Actions for ${n.name}` })),
    );
    return row;
  }

  function rowMenu(n) {
    return [
      { label: 'Open', icon: 'external', href: `#/nodes/${n.id}` },
      { label: 'Edit', icon: 'edit', href: `#/nodes/${n.id}/edit`, adminOnly: true },
      { label: 'Run all checks now', icon: 'play', onClick: () => runNow(n), adminOnly: true },
      { label: 'Duplicate', icon: 'copy', onClick: () => duplicate(n), adminOnly: true },
      { label: n.enabled === false ? 'Enable' : 'Disable', icon: 'power', onClick: () => setEnabled(n, n.enabled === false), adminOnly: true },
      { sep: true, adminOnly: true },
      { label: 'Delete', icon: 'trash', danger: true, onClick: () => remove(n), adminOnly: true },
    ];
  }

  async function runNow(n) {
    try { const results = await api.post(`/api/nodes/${n.id}/run`); toast(`Ran ${results?.length ?? 0} check${results?.length === 1 ? '' : 's'} for ${n.name}`, { kind: 'success' }); await load(); } catch (e) { toast(e.message, { kind: 'error' }); }
  }
  async function duplicate(n) {
    try { const copy = await api.post(`/api/nodes/${n.id}/duplicate`); toast(`Created "${copy.name}" (disabled until you enable it)`, { kind: 'success', action: { label: 'Edit', onClick: () => ctx.navigate(`/nodes/${copy.id}/edit`) } }); await load(); } catch (e) { toast(e.message, { kind: 'error' }); }
  }
  async function setEnabled(n, enabled) {
    try { await api.post(`/api/nodes/${n.id}/enable`, { enabled }); toast(`${n.name} ${enabled ? 'enabled' : 'disabled'}`, { kind: 'success' }); await load(); } catch (e) { toast(e.message, { kind: 'error' }); await load(); }
  }
  async function remove(n) {
    const ok = await confirmDialog({ title: `Delete ${n.name}?`, message: `This removes the node, its ${(n.checks || []).length} check(s) and all of their history. This cannot be undone.`, confirmLabel: 'Delete node', danger: true });
    if (!ok) return;
    try { await api.del(`/api/nodes/${n.id}`); toast(`${n.name} deleted`, { kind: 'success' }); await load(); } catch (e) { toast(e.message, { kind: 'error' }); }
  }

  try { await load(); } catch (e) { replace(listEl, h('div', { class: 'card' }, emptyState({ icon: 'alert', title: 'Could not load nodes', text: e.message }))); }

  return { refresh: load, destroy() { state.destroyed = true; } };
}

const TEMPLATE_ICONS = { website: 'globe', 'home-server': 'server', router: 'router', ping: 'activity', 'api-endpoint': 'api', 'tcp-service': 'link', dns: 'hash', blank: 'file' };

export async function openTemplatePicker(state, ctx) {
  const body = h('div', { class: 'stack-sm' }, h('p', null, 'Pick a starting point. Every setting can be changed afterwards.'), skeleton({ lines: 3 }));
  const m = openModal({ title: 'Add a node', wide: true, body });
  let templates = state?.templates;
  try { if (!templates) templates = await api.get('/api/templates'); if (state) state.templates = templates; } catch (e) { templates = []; }
  const grid = h('div', { class: 'template-grid' });
  for (const t of templates || []) {
    grid.append(h('a', { class: 'template-card', href: `#/nodes/new?template=${encodeURIComponent(t.id)}`, onclick: () => m.close() },
      h('span', { class: 't-icon' }, icon(TEMPLATE_ICONS[t.id] || t.icon || 'server')),
      h('span', null, h('b', null, t.name), h('span', null, t.description || ''))));
  }
  grid.append(h('a', { class: 'template-card', href: '#/nodes/new', onclick: () => m.close() },
    h('span', { class: 't-icon' }, icon('file')),
    h('span', null, h('b', null, 'Blank'), h('span', null, 'Start from an empty node and add the checks you want.'))));
  replace(body, h('p', null, 'Pick a starting point. Every setting can be changed afterwards.'), grid);
}

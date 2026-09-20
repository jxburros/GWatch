// Nodes list: searchable, filterable list of everything being monitored.

import { api } from '../api.js';
import { h, icon, clear, replace, statusSpine, statusWord, checkChip, importanceBadge, tagList, toggle, menuButton, toast, confirmDialog, openModal, emptyState, skeleton } from '../components.js';
import { relTime } from '../fmt.js';
import { pairMachine } from './hardware.js';

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

  // A live update lands every few seconds. Emptying the list and building it
  // again would throw away every element on the page each time — which loses
  // the focus ring, the row the pointer is over and any open menu, and hands
  // the browser the whole list to lay out and paint in one go. So rows and
  // group sections are kept between renders, keyed by node id and group name,
  // and only what actually changed is written back.
  const rowEls = new Map();      // node id -> <article class="node-row">
  const sectionEls = new Map();  // group name -> { el, head, rows }

  function renderList() {
    if (!state.nodes.length) {
      countEl.textContent = '';
      clear(listEl);
      listEl.append(h('div', { class: 'card' }, emptyState({ icon: 'server', title: 'No nodes yet', text: 'Add your router, a website or a home server. Templates fill in sensible checks for you.', actions: h('button', { class: 'btn btn-primary admin-only', type: 'button', onclick: () => openTemplatePicker(state, ctx) }, icon('plus'), 'Add your first node') })));
      return;
    }
    const rows = state.nodes.filter(matches);
    countEl.textContent = rows.length === state.nodes.length
      ? `${state.nodes.length} node${state.nodes.length === 1 ? '' : 's'}`
      : `${rows.length} of ${state.nodes.length} nodes`;
    if (!rows.length) {
      clear(listEl);
      listEl.append(h('div', { class: 'card' }, emptyState({ icon: 'search', title: 'No nodes match', text: 'Try a different search or clear the filters.', compact: true, actions: h('button', { class: 'btn btn-sm', type: 'button', onclick: () => { state.q = ''; state.group = ''; state.status = ''; state.tag = ''; searchInput.value = ''; renderFilters(); renderList(); } }, 'Clear filters') })));
      return;
    }
    // group sections
    const byGroup = new Map();
    for (const n of rows) { const g = n.group || 'Ungrouped'; if (!byGroup.has(g)) byGroup.set(g, []); byGroup.get(g).push(n); }
    const groupNames = [...byGroup.keys()].sort((a, b) => (a === 'Ungrouped') - (b === 'Ungrouped') || a.localeCompare(b));

    // Drop what the new data no longer has. A section taken out takes its rows
    // with it, but their entries are pruned here too so the cache cannot grow.
    for (const [name, sec] of sectionEls) if (!byGroup.has(name)) { sec.el.remove(); sectionEls.delete(name); }
    const live = new Set(rows.map((n) => String(n.id)));
    for (const [id, row] of rowEls) if (!live.has(id)) { row.remove(); rowEls.delete(id); }

    const keep = new Set();
    let prevSection = null;
    for (const g of groupNames) {
      const nodes = byGroup.get(g).sort((a, b) => STATUS_ORDER.indexOf(a.status || 'unknown') - STATUS_ORDER.indexOf(b.status || 'unknown') || a.name.localeCompare(b.name));
      let sec = sectionEls.get(g);
      if (!sec) {
        const el = h('section', { class: 'node-group', 'aria-label': g });
        const head = h('div', { class: 'node-group-head' }, h('h2', null, g), h('span', { class: 'count' }));
        const list = h('div', { class: 'node-rows' });
        el.append(list);
        sec = { el, head, rows: list };
        sectionEls.set(g, sec);
      }
      keep.add(sec.el);
      // Whether a section shows its heading depends on the filters, so it can
      // come and go on a section that is otherwise unchanged.
      const wantHead = groupNames.length > 1 || g !== 'Ungrouped';
      if (wantHead) {
        sec.head.querySelector('.count').textContent = `${nodes.length} node${nodes.length === 1 ? '' : 's'}`;
        if (sec.head.parentNode !== sec.el) sec.el.insertBefore(sec.head, sec.rows);
      } else if (sec.head.parentNode) sec.head.remove();

      if (sec.el.previousElementSibling !== prevSection || sec.el.parentNode !== listEl) {
        listEl.insertBefore(sec.el, prevSection ? prevSection.nextSibling : listEl.firstChild);
      }
      prevSection = sec.el;

      let prevRow = null;
      for (const n of nodes) {
        const id = String(n.id);
        let row = rowEls.get(id);
        if (!row) { row = makeRow(n); rowEls.set(id, row); }
        updateRow(row, n);
        if (row.previousElementSibling !== prevRow || row.parentNode !== sec.rows) {
          sec.rows.insertBefore(row, prevRow ? prevRow.nextSibling : sec.rows.firstChild);
        }
        prevRow = row;
      }
    }
    // Anything else in the list is left over from a skeleton or an empty state.
    for (const child of [...listEl.children]) if (!keep.has(child)) child.remove();
  }

  /** Build a row's fixed frame. The controls that carry listeners are made
   *  once and read `row._node`, so a row reused across updates can never end
   *  up with a second listener on the same switch or menu. */
  function makeRow(n) {
    const row = h('article', { class: 'node-row' });
    const statusCell = h('div', { class: 'n-status' });
    const link = h('a');
    const host = h('span');
    const tags = h('span');
    const nameCell = h('div', { class: 'n-name' }, link, host, tags);
    const chips = h('div', { class: 'check-chips' });
    const meta = h('div', { class: 'n-meta' }, h('span'));
    // Left visible for a viewer — whether a node is paused is worth seeing —
    // but not operable, since the server would refuse the change anyway.
    const enabledToggle = toggle({ checked: n.enabled !== false, disabled: !ctx.me?.isAdmin, onChange: (v) => setEnabled(row._node, v) });
    const menu = menuButton(() => rowMenu(row._node), { label: 'Actions' });
    const spine = statusSpine('unknown');
    row.append(spine, statusCell, nameCell, chips, meta, h('div', { class: 'n-actions' }, enabledToggle, menu));
    row._parts = { spine, statusCell, link, host, tags, chips, meta, toggle: enabledToggle, menu };
    return row;
  }

  /** Write a node's current state into a row that is already on the page. */
  function updateRow(row, n) {
    row._node = n;
    const p = row._parts;
    const status = n.status || 'unknown';
    const states = n.stateByCheck || {};
    const name = n.name || 'Unnamed node';

    const cls = `node-row ${n.enabled === false ? 'disabled' : ''}`;
    if (row.className !== cls) row.className = cls;
    if (row.getAttribute('aria-label') !== name) row.setAttribute('aria-label', name);

    // The spine flashes when a node changes status, and that flash is decided
    // inside `statusSpine` from the key. Replacing the element is what arms it,
    // and the spine carries no listeners, so replacing it costs nothing.
    const spine = statusSpine(status, { key: `node:${n.id}` });
    p.spine.replaceWith(spine);
    p.spine = spine;

    // The rest of the row is made of small composites with no listeners on
    // them, so each is simply built again — but only when the values behind it
    // moved. Most updates touch one check on one node, and the rows either
    // side of it are then left completely alone.
    const sig = row._sig || (row._sig = {});
    const statusSig = `${status}|${n.inMaintenance ? 1 : 0}`;
    if (sig.status !== statusSig) {
      sig.status = statusSig;
      replace(p.statusCell, statusWord(status), n.inMaintenance && status !== 'maintenance' ? h('div', { class: 'tiny text-maintenance', style: { marginTop: '4px' } }, 'in maintenance') : null);
    }

    const href = `#/nodes/${n.id}`;
    if (p.link.getAttribute('href') !== href) p.link.setAttribute('href', href);
    if (p.link.textContent !== name) p.link.textContent = name;
    const hostCls = n.host ? 'n-host' : 'n-host dim';
    const hostText = n.host || 'targets set per check';
    if (p.host.className !== hostCls) p.host.className = hostCls;
    if (p.host.textContent !== hostText) p.host.textContent = hostText;
    const tagSig = (n.tags || []).join('\u0000');
    if (sig.tags !== tagSig) {
      sig.tags = tagSig;
      const tags = tagList(n.tags || []);
      p.tags.replaceWith(tags);
      p.tags = tags;
    }

    const checks = n.checks || [];
    const chipSig = checks.map((c) => `${c.id}:${c.name}:${c.enabled}:${states[c.id]?.status}:${states[c.id]?.lastLatencyMs}:${states[c.id]?.lastMessage}`).join('\u0000');
    if (sig.chips !== chipSig) {
      sig.chips = chipSig;
      replace(p.chips, checks.map((c) => checkChip(c, states[c.id], { href })));
      if (!checks.length) p.chips.append(h('span', { class: 'dim small' }, 'No checks'));
    }

    const last = Object.values(states).map((s) => s?.lastRunAt).filter(Boolean).sort().pop();
    // "Checked 2 minutes ago" ages on its own, so the rendered words decide.
    const metaText = last ? `Checked ${relTime(last)}` : 'Not checked yet';
    const metaSig = `${metaText}|${n.importance || ''}`;
    if (sig.meta !== metaSig) {
      sig.meta = metaSig;
      replace(p.meta, h('span', null, metaText), importanceBadge(n.importance));
    }

    p.toggle.input.checked = n.enabled !== false;
    p.toggle.input.disabled = !ctx.me?.isAdmin;
    p.toggle.input.setAttribute('aria-label', `${name} enabled`);
    p.menu.setAttribute('aria-label', `Actions for ${name}`);
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

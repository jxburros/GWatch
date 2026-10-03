// Network map: the dependencies between nodes, drawn. Every node with a
// "Depends on" hangs under the node it names, so a firewall or a gateway sits
// at the top of everything it would take down with it. The map can be laid
// out three ways and styled, its options are remembered in this browser, a
// node's dependency can be changed from beside it, and the map as configured
// can be pinned to a dashboard as a widget.

import { api } from '../api.js';
import { h, append, icon, clear, replace, toast, confirmDialog, menuButton, emptyState, skeleton, statusPill, field, selectInput, uid } from '../components.js';
import { plural, compareNames } from '../fmt.js';
import { renderNetworkMap, mapConfigEditor, normalizeMapConfig, buildForest } from '../netmap.js';

const STORE_KEY = 'gw.netmap';
function savedConfig() { try { return normalizeMapConfig(JSON.parse(localStorage.getItem(STORE_KEY) || '{}')); } catch { return normalizeMapConfig({}); } }
function saveConfig(cfg) { try { localStorage.setItem(STORE_KEY, JSON.stringify(cfg)); } catch { /* private window: forgotten on reload */ } }

export async function mount(root, ctx) {
  const state = { overview: null, groups: null, cfg: savedConfig(), selected: ctx.query.get('node') || null, map: null, destroyed: false };

  ctx.setTitle('Network map', {
    actions: [
      h('button', { class: 'btn admin-only', type: 'button', onclick: pinToDashboard }, icon('grid'), 'Pin to a dashboard'),
      menuButton(() => [
        { label: 'Fit the whole map', icon: 'layout', onClick: () => state.map?.fit() },
        { label: 'Export as SVG', icon: 'download', onClick: exportSvg },
        { sep: true },
        { label: 'Reset the map options', icon: 'refresh', onClick: () => { state.cfg = normalizeMapConfig({}); saveConfig(state.cfg); renderSide(); state.map?.update(entries(), state.cfg); } },
      ], { label: 'Map options' }),
    ],
  });

  const mapCard = h('section', { class: 'card netmap-card' });
  const side = h('aside', { class: 'netmap-side' });
  root.append(h('div', { class: 'netmap-layout' }, mapCard, side));
  mapCard.append(skeleton({ lines: 6 }));

  async function load() {
    const [overview, groups] = await Promise.all([api.get('/api/overview'), api.get('/api/groups').catch(() => ({ groups: [], tags: [] }))]);
    if (state.destroyed) return;
    state.overview = overview; state.groups = groups;
  }
  const entries = () => state.overview?.nodes || [];
  const entryOf = (id) => entries().find((e) => String(e.node.id) === String(id));

  function renderMap() {
    clear(mapCard);
    const list = entries();
    const linked = list.filter((e) => e.node.dependsOnNodeId != null).length;
    mapCard.append(h('div', { class: 'card-head' },
      h('h2', { class: 'card-title' }, icon('network'), 'Dependencies'),
      h('span', { class: 'muted' }, `${plural(list.length, 'node')} · ${plural(linked, 'dependency', 'dependencies')}`)));
    const host = h('div', { class: 'netmap-host' });
    mapCard.append(host);
    if (list.length && !linked) {
      mapCard.insertBefore(h('div', { class: 'note netmap-hint' }, icon('info'),
        h('span', null, 'No node depends on another yet. Choose a node on the map (or open it in Nodes) and set what it ', h('b', null, 'Depends on'), ' — the router everything sits behind, the switch a camera is plugged into. While a parent is down, alerts for everything under it are held back.')), host);
    }
    state.map = renderNetworkMap(host, list, state.cfg, { interactive: true, selectedId: state.selected, onSelect: (n) => { state.selected = String(n.id); renderSelected(); }, title: 'Network map' });
  }

  /* ---------- Side panel: the selected node, then the options ---------- */
  const selectedCard = h('section', { class: 'card' });
  const optionsCard = h('section', { class: 'card' });
  side.append(selectedCard, optionsCard);

  function renderSide() {
    clear(optionsCard);
    optionsCard.append(h('div', { class: 'card-title' }, 'Customise'),
      mapConfigEditor(state.cfg, { groups: state.groups, onChange: (cfg) => { state.cfg = cfg; saveConfig(cfg); state.map?.update(entries(), cfg); } }));
    renderSelected();
  }

  function renderSelected() {
    clear(selectedCard);
    const e = state.selected && entryOf(state.selected);
    if (!e) {
      selectedCard.append(h('div', { class: 'card-title' }, 'Node'), h('p', { class: 'note' }, 'Choose a node on the map to see what it depends on, what depends on it and to change its dependency.'));
      return;
    }
    const n = e.node;
    const all = entries();
    const forest = buildForest(all.map((x) => x.node));
    const children = (forest.children.get(String(n.id)) || []).map((id) => entryOf(id)?.node).filter(Boolean);
    // Everything below, however deep: what an outage here would silence.
    const below = [];
    const walk = (id) => { for (const k of forest.children.get(id) || []) { below.push(k); walk(k); } };
    walk(String(n.id));
    const parent = n.dependsOnNodeId != null ? entryOf(n.dependsOnNodeId)?.node : null;
    const chain = [];
    for (let p = parent, seen = new Set(); p && !seen.has(p.id); p = p.dependsOnNodeId != null ? entryOf(p.dependsOnNodeId)?.node : null) { seen.add(p.id); chain.push(p); }

    append(selectedCard, [
      h('div', { class: 'card-title row-between' }, h('span', { class: 'truncate' }, n.name), statusPill(n.enabled === false ? 'paused' : e.status)),
      h('div', { class: 'muted mono', style: { marginBottom: '8px' } }, n.host || ''),
      e.affectedBy ? h('p', { class: 'note affected-note' }, icon('link'), `Affected by ${e.affectedBy}`) : null]);

    // Which node in a chain could this one depend on: anything except itself
    // and the nodes below it, which would make a loop.
    const blocked = new Set([String(n.id), ...below]);
    const options = all.map((x) => x.node).filter((x) => !blocked.has(String(x.id))).sort((a, b) => compareNames(a.name, b.name));
    const sel = selectInput({ options: [{ value: '', label: 'Nothing (a root of the map)' }, ...options.map((x) => ({ value: x.id, label: `${x.name}${x.host ? ` (${x.host})` : ''}` }))], value: n.dependsOnNodeId ?? '', disabled: !ctx.me?.isAdmin });
    const saveBtn = h('button', { type: 'button', class: 'btn btn-primary btn-sm admin-only', onclick: () => setParent(n, sel.value ? Number(sel.value) : null) }, 'Save');
    append(selectedCard, [
      field({ label: 'Depends on', input: h('div', { class: 'row netmap-parent', style: { gap: '6px', flexWrap: 'nowrap' } }, sel, saveBtn), help: 'While the node it depends on is down, this node’s failures are put down to it and its alerts are held back.' }),
      chain.length ? h('div', { class: 'netmap-facts' }, h('span', { class: 'section-title' }, 'Upstream'), h('div', null, chain.map((p, i) => [i ? ' › ' : '', h('a', { href: '#', onclick: (ev) => { ev.preventDefault(); select(p.id); } }, p.name)]))) : null,
      h('div', { class: 'netmap-facts' }, h('span', { class: 'section-title' }, 'Depends on this'),
        children.length ? h('div', null, children.sort((a, b) => compareNames(a.name, b.name)).map((c, i) => [i ? ', ' : '', h('a', { href: '#', onclick: (ev) => { ev.preventDefault(); select(c.id); } }, c.name)])) : h('div', { class: 'muted' }, 'Nothing')),
      below.length ? h('p', { class: 'note' }, `If ${n.name} goes down, alerts for ${plural(below.length, 'node')} below it are held back.`) : null,
      h('div', { class: 'row', style: { gap: '6px', marginTop: '10px' } },
        h('a', { class: 'btn btn-sm', href: `#/nodes/${n.id}` }, icon('external'), 'Open'),
        h('a', { class: 'btn btn-sm admin-only', href: `#/nodes/${n.id}/edit` }, icon('edit'), 'Edit')),
    ]);
  }
  function select(id) { state.selected = String(id); state.map?.select(id); renderSelected(); }

  async function setParent(n, parentId) {
    if ((n.dependsOnNodeId ?? null) === parentId) { toast('Nothing changed'); return; }
    try {
      await api.patch('/api/nodes/bulk', { nodeIds: [n.id], node: { dependsOnNodeId: parentId } });
      toast(parentId ? `${n.name} now depends on ${entryOf(parentId)?.node.name || 'that node'}` : `${n.name} no longer depends on anything`, { kind: 'success' });
      await load(); state.map?.update(entries(), state.cfg); renderSelected();
    } catch (err) { toast(err.message, { kind: 'error' }); }
  }

  async function pinToDashboard() {
    let dashboards = [];
    try { dashboards = await api.get('/api/dashboards'); } catch (e) { toast(e.message, { kind: 'error' }); return; }
    if (!dashboards.length) { toast('Create a dashboard first', { kind: 'error' }); return; }
    const sel = h('select', null, dashboards.map((d) => h('option', { value: d.id }, d.name)));
    const ok = await confirmDialog({ title: 'Pin the network map to a dashboard', confirmLabel: 'Pin', body: h('div', { class: 'stack-sm' }, h('div', { class: 'field' }, h('label', null, 'Dashboard'), sel), h('p', { class: 'note' }, 'The widget keeps the layout, labels and filters chosen here; edit the widget to change them.')) });
    if (!ok) return;
    const d = dashboards.find((x) => String(x.id) === sel.value);
    const widgets = [...(d.widgets || []), { id: uid('w'), type: 'network_map', title: 'Network map', width: 4, height: 3, config: state.cfg }];
    try { await api.put(`/api/dashboards/${d.id}`, { ...d, widgets }); toast(`Pinned to ${d.name}`, { kind: 'success', action: { label: 'Open', onClick: () => ctx.navigate(`/dashboard/${d.id}`) } }); } catch (e) { toast(e.message, { kind: 'error' }); }
  }

  function exportSvg() {
    const el = state.map?.svg();
    if (!el) { toast('Nothing to export yet'); return; }
    // The drawing's colours come from the page's stylesheet, so the export
    // carries the ones in use now inline.
    const copy = el.cloneNode(true);
    const cs = getComputedStyle(el);
    copy.setAttribute('xmlns', 'http://www.w3.org/2000/svg');
    const pairs = [...el.querySelectorAll('*')].map((x, i) => [x, copy.querySelectorAll('*')[i]]);
    for (const [src, dst] of pairs) {
      const s = getComputedStyle(src);
      for (const prop of ['fill', 'stroke', 'stroke-width', 'stroke-dasharray', 'opacity', 'font-family', 'font-weight']) {
        const v = s.getPropertyValue(prop); if (v) dst.style.setProperty(prop, v);
      }
    }
    copy.style.background = cs.backgroundColor;
    const blob = new Blob([new XMLSerializer().serializeToString(copy)], { type: 'image/svg+xml' });
    const a = h('a', { href: URL.createObjectURL(blob), download: 'gwatch-network-map.svg' });
    document.body.append(a); a.click(); a.remove();
    setTimeout(() => URL.revokeObjectURL(a.href), 1000);
  }

  try { await load(); } catch (e) { replace(root, h('div', { class: 'card' }, emptyState({ icon: 'alert', title: 'Could not load the network map', text: e.message }))); return { destroy() {} }; }
  renderMap(); renderSide();

  return {
    async refresh() {
      await load();
      if (state.destroyed) return;
      state.map?.update(entries(), state.cfg);
      // The side panel holds a form; only redraw it when nothing in it has focus.
      if (!selectedCard.contains(document.activeElement)) renderSelected();
    },
    themeChanged() { state.map?.update(entries(), state.cfg); },
    destroy() { state.destroyed = true; state.map?.destroy(); },
  };
}


// Network map: the dependencies between nodes, drawn. Every node with a
// "Depends on" hangs off the node it names, so a firewall or a gateway sits
// in the middle of everything it would take down with it.
//
// The clusters layout can be arranged by hand — drag a node and it is pinned
// there, carrying what hangs off it — and tidied up again; clusters can be
// folded away. An administrator's arrangement is kept on the service and is
// the one every map shows; anyone else may move things about in their own tab.
// Choosing a node shows what it depends on and what depends on it, and a
// what-if simulation shows what an outage or maintenance there would cut off.
// The map's look is remembered in this browser, and the map as configured can
// be pinned to a dashboard or shown full screen (#/map/present).

import { api } from '../api.js';
import { h, append, icon, clear, replace, toast, confirmDialog, menuButton, showMenu, emptyState, skeleton, statusPill, field, selectInput, uid } from '../components.js';
import { plural, compareNames } from '../fmt.js';
import {
  renderNetworkMap, mapConfigEditor, linkStyleEditor, nodeColourControl, normalizeMapConfig, buildForest,
  descendantsOf, ancestorsOf, biggestRisks, tidyUnpinned, toggleFolded, unpin,
} from '../netmap.js';
import { arrangementStore, createSimulation, nodeFinder, exportMapSvg, exportMapPng, layoutLocked, setLayoutLocked } from '../netmap-tools.js';

const STORE_KEY = 'gw.netmap';
export function savedConfig() { try { return normalizeMapConfig(JSON.parse(localStorage.getItem(STORE_KEY) || '{}')); } catch { return normalizeMapConfig({}); } }
function saveConfig(cfg) { try { localStorage.setItem(STORE_KEY, JSON.stringify(cfg)); } catch { /* private window: forgotten on reload */ } }

export async function mount(root, ctx) {
  const state = { overview: null, groups: null, cfg: savedConfig(), selected: ctx.query.get('node') || null, map: null, destroyed: false };
  const isAdmin = () => !!ctx.me?.isAdmin;
  const store = arrangementStore({ canSave: isAdmin });
  const mayArrange = () => !layoutLocked();

  ctx.setTitle('Network map', {
    actions: [
      h('a', { class: 'btn', href: '#/map/present', title: 'Show the map full screen and read-only, for an audience' }, icon('tv'), 'Present'),
      h('button', { class: 'btn admin-only', type: 'button', onclick: addToDashboard }, icon('grid'), 'Add to a dashboard'),
      menuButton(() => [
        { label: 'Fit the whole map', icon: 'layout', onClick: () => state.map?.fit() },
        { label: 'Export as SVG', icon: 'download', onClick: () => exportMapSvg(state.map?.svg()) },
        { label: 'Export as PNG (what is in view)', icon: 'image', onClick: () => exportMapPng(state.map?.svg()) },
      ], { label: 'More map actions' }),
    ],
  });

  const mapCard = h('section', { class: 'card netmap-card' });
  const side = h('aside', { class: 'netmap-side', 'aria-label': 'Network map details and options' });
  root.append(h('div', { class: 'netmap-layout' }, mapCard, side));
  mapCard.append(skeleton({ lines: 6 }));

  async function load() {
    const [overview, groups] = await Promise.all([api.get('/api/overview'), api.get('/api/groups').catch(() => ({ groups: [], tags: [] }))]);
    if (state.destroyed) return;
    state.overview = overview; state.groups = groups;
  }
  const entries = () => state.overview?.nodes || [];
  const entryOf = (id) => entries().find((e) => String(e.node.id) === String(id));
  const nameOf = (id) => entryOf(id)?.node.name || 'that node';
  const fullForest = () => state.map?.view()?.all || buildForest(entries().map((e) => e.node));
  const clusters = () => state.cfg.orientation === 'radial';

  const sim = createSimulation({
    names: nameOf,
    forest: fullForest,
    onChange: (s) => { state.map?.setSim(s); renderSelected(); },
  });

  /* ---------- The map, with its own toolbar ---------- */
  // Everything that acts on the map as a whole sits in its head: how many
  // nodes, finding one, and (in clusters) arranging. What acts on one node is
  // in the side panel; how the map looks is the side panel's other tab.
  function arrangeMenu() {
    const btn = h('button', { type: 'button', class: 'btn btn-sm', 'aria-haspopup': 'menu', 'aria-expanded': 'false', title: 'Tidy up, or lock the layout' }, icon('layers'), 'Arrange');
    btn.addEventListener('click', (ev) => {
      ev.stopPropagation();
      showMenu(btn, [
        { label: 'Tidy up the unpinned nodes', icon: 'layers', onClick: tidyUnpinnedNodes },
        { label: 'Tidy up everything (forget every place)', icon: 'refresh', onClick: tidyEverything },
        { sep: true },
        { label: layoutLocked() ? 'Unlock the layout' : 'Lock the layout', icon: 'lock', onClick: toggleLock },
      ]);
    });
    return btn;
  }

  function howTo() {
    if (!clusters()) return 'To arrange nodes by hand, choose the Clusters layout under Customise.';
    if (!mayArrange()) return null;
    return `Drag a node to arrange the map — it is pinned where you drop it and what hangs off it comes along (Shift moves it alone).${isAdmin() ? '' : ' Your arrangement stays in this tab.'} Click empty space or press Esc to let go of the chosen node.`;
  }

  function renderMap() {
    clear(mapCard);
    const list = entries();
    const linked = list.filter((e) => e.node.dependsOnNodeId != null).length;
    const locked = clusters() && layoutLocked();
    mapCard.append(h('div', { class: 'card-head netmap-head' },
      h('h2', { class: 'card-title' }, icon('network'), 'Dependencies'),
      h('span', { class: 'muted netmap-count' }, `${plural(list.length, 'node')} · ${plural(linked, 'dependency', 'dependencies')}`),
      h('div', { class: 'netmap-toolbar' },
        nodeFinder(entries, (id) => select(id, { focus: true })),
        locked ? h('button', { type: 'button', class: 'btn btn-sm netmap-locked', 'aria-pressed': 'true', title: 'The layout is locked in this browser: click to unlock it', onclick: toggleLock }, icon('lock'), 'Locked') : null,
        clusters() ? arrangeMenu() : null)));
    mapCard.append(sim.el);
    const host = h('div', { class: 'netmap-host' });
    mapCard.append(host);
    if (list.length && !linked) {
      mapCard.insertBefore(h('div', { class: 'note netmap-hint' }, icon('info'),
        h('span', null, 'No node depends on another yet. Choose a node on the map (or open it in Nodes) and set what it ', h('b', null, 'Depends on'), ' — the router everything sits behind, the switch a camera is plugged into. While a parent is down, alerts for everything under it are held back.')), host);
    }
    state.map = renderNetworkMap(host, list, state.cfg, {
      interactive: true, selectedId: state.selected, title: 'Network map',
      arranged: store.value, canArrange: mayArrange(), sim: sim.state,
      onSelect: (n) => { state.selected = n ? String(n.id) : null; showTab('node'); renderSelected(); if (n) revealPanel(); },
      onArrange: (a) => { store.change(a); renderSelected(); },
    });
    const tip = howTo();
    if (tip) mapCard.append(h('p', { class: 'netmap-howto' }, tip));
  }

  // On a narrow screen the side panel is under the map; choosing a node
  // scrolls it into view, or nothing would seem to happen.
  function revealPanel() {
    if (typeof matchMedia !== 'function' || !matchMedia('(max-width: 68.75rem)').matches) return;
    const r = side.getBoundingClientRect();
    if (r.top > window.innerHeight - 80) side.scrollIntoView({ behavior: 'smooth', block: 'start' });
  }

  /* ---------- Side panel: the chosen node, or the map's options ---------- */
  const tabIds = { node: uid('netmap-tab'), options: uid('netmap-tab') };
  const panelIds = { node: uid('netmap-panel'), options: uid('netmap-panel') };
  const tabNode = h('button', { type: 'button', role: 'tab', class: 'tab', id: tabIds.node, 'aria-controls': panelIds.node, onclick: () => showTab('node') }, 'Node');
  const tabOptions = h('button', { type: 'button', role: 'tab', class: 'tab', id: tabIds.options, 'aria-controls': panelIds.options, onclick: () => showTab('options') }, icon('sliders'), 'Customise');
  const tablist = h('div', { class: 'tabs netmap-tabs', role: 'tablist', 'aria-label': 'Side panel' }, tabNode, tabOptions);
  tablist.addEventListener('keydown', (ev) => {
    if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(ev.key)) return;
    ev.preventDefault();
    const next = (ev.key === 'Home') ? 'node' : (ev.key === 'End') ? 'options' : (tabNode.getAttribute('aria-selected') === 'true' ? 'options' : 'node');
    showTab(next); (next === 'node' ? tabNode : tabOptions).focus();
  });
  const selectedCard = h('section', { class: 'card netmap-inspect', role: 'tabpanel', id: panelIds.node, 'aria-labelledby': tabIds.node });
  const optionsCard = h('section', { class: 'card netmap-options-panel', role: 'tabpanel', id: panelIds.options, 'aria-labelledby': tabIds.options });
  side.append(h('div', { class: 'netmap-tabbed' }, tablist, selectedCard, optionsCard));
  function showTab(which) {
    const node = which !== 'options';
    tabNode.setAttribute('aria-selected', node ? 'true' : 'false'); tabNode.tabIndex = node ? 0 : -1;
    tabOptions.setAttribute('aria-selected', node ? 'false' : 'true'); tabOptions.tabIndex = node ? -1 : 0;
    selectedCard.hidden = !node; optionsCard.hidden = node;
  }
  showTab('node');

  function resetOptions() {
    state.cfg = normalizeMapConfig({}); saveConfig(state.cfg);
    renderOptions(); state.map?.setCanArrange(mayArrange()); renderMap();
    toast('The map is back to its default look.');
  }

  function renderOptions() {
    clear(optionsCard);
    optionsCard.append(
      mapConfigEditor(state.cfg, { groups: state.groups, onChange: (cfg) => {
        const layoutChanged = cfg.orientation !== state.cfg.orientation;
        state.cfg = cfg; saveConfig(cfg); state.map?.update(entries(), cfg);
        if (layoutChanged) { state.map?.setCanArrange(mayArrange()); renderMap(); }
      } }),
      h('div', { class: 'netmap-options-foot' },
        h('span', { class: 'help' }, 'Remembered in this browser.'),
        h('button', { type: 'button', class: 'btn btn-sm', onclick: resetOptions }, icon('refresh'), 'Reset the map options')));
  }

  function renderSide() {
    renderOptions();
    renderSelected();
  }

  function risksList() {
    const f = buildForest(entries().map((e) => e.node));
    const risks = biggestRisks(f, null, 6);
    if (!risks.length) return null;
    return h('div', { class: 'netmap-risks-block' },
      h('div', { class: 'section-title' }, 'Biggest risks'),
      h('p', { class: 'help' }, 'The nodes whose loss takes the most with it.'),
      h('ol', { class: 'netmap-risks' }, risks.map((r) => h('li', null,
        h('a', { href: '#', onclick: (ev) => { ev.preventDefault(); select(r.id, { focus: true }); } }, r.node.name),
        h('span', { class: 'muted' }, ` ${plural(r.below, 'node')} below`)))));
  }

  function renderSelected() {
    clear(selectedCard);
    const e = state.selected && entryOf(state.selected);
    if (!e) {
      selectedCard.append(
        h('p', { class: 'netmap-empty-hint' }, 'Choose a node on the map, or find one by name, to see what it depends on and what depends on it — and to try what happens if it fails.'),
        risksList());
      return;
    }
    const n = e.node;
    const id = String(n.id);
    const all = entries();
    const forest = buildForest(all.map((x) => x.node));
    const children = (forest.children.get(id) || []).map((k) => entryOf(k)?.node).filter(Boolean).sort((a, b) => compareNames(a.name, b.name));
    // Everything below, however deep: what an outage here would silence.
    const below = descendantsOf(forest, id);
    const chain = ancestorsOf(forest, id).map((k) => entryOf(k)?.node).filter(Boolean);
    const parent = chain[0] || null;
    const v = state.map?.view();
    const drawnState = v?.states.get(id);
    const simKind = sim.kindOf(id);
    const pinned = !!store.value.positions?.[id]?.pinned;
    const folded = (store.value.collapsed || []).map(String).includes(id);
    const cause = drawnState?.cause;
    const causeName = cause ? entryOf(cause.by)?.node.name : '';

    const link = (node) => h('a', { href: '#', onclick: (ev) => { ev.preventDefault(); select(node.id, { focus: true }); } }, node.name);
    append(selectedCard, [
      h('div', { class: 'netmap-node-head' },
        h('h2', { class: 'netmap-node-name' }, n.name),
        statusPill(drawnState?.status || (n.enabled === false ? 'paused' : e.status))),
      n.host ? h('div', { class: 'muted mono netmap-node-host' }, n.host) : null,
      simKind ? h('p', { class: 'note' }, icon('zap'), simKind === 'down' ? ' Failing in the simulation.' : ' In maintenance in the simulation.') : null,
      cause ? h('p', { class: 'note affected-note' }, icon('link'), cause.kind === 'maintenance' ? ` Quiet while ${causeName} is in maintenance` : ` Cut off while ${causeName} is down`, cause.sim ? ' (simulated)' : '')
        : e.affectedBy ? h('p', { class: 'note affected-note' }, icon('link'), ` Affected by ${e.affectedBy}`) : null,
      h('div', { class: 'netmap-facts' }, h('span', { class: 'section-title' }, 'Depends on'),
        chain.length ? h('div', null, chain.map((p, i) => [i ? ' › ' : '', link(p)])) : h('div', { class: 'muted' }, 'Nothing: it is a root of the map')),
      h('div', { class: 'netmap-facts' }, h('span', { class: 'section-title' }, 'Depends on this'),
        children.length ? h('div', null, children.slice(0, 12).map((c, i) => [i ? ', ' : '', link(c)]), children.length > 12 ? ` and ${children.length - 12} more` : '') : h('div', { class: 'muted' }, 'Nothing')),
      below.length ? h('p', { class: 'netmap-impact' }, `If ${n.name} goes down, ${plural(below.length, 'node')} ${below.length === 1 ? 'is' : 'are'} cut off and their alerts held back.`) : null,
      // What if: a simulation, which changes nothing real.
      h('div', { class: 'netmap-group' },
        h('div', { class: 'section-title' }, 'What if…'),
        h('div', { class: 'netmap-actions' },
          simKind
            ? h('button', { type: 'button', class: 'btn btn-sm', onclick: () => sim.remove(id) }, icon('x'), 'Take out of the simulation')
            : [h('button', { type: 'button', class: 'btn btn-sm', onclick: () => sim.add(id, 'down') }, icon('zap'), 'Simulate a failure'),
              h('button', { type: 'button', class: 'btn btn-sm', onclick: () => sim.add(id, 'maintenance') }, icon('wrench'), 'Simulate maintenance')])),
      // On the map: folding, pinning, and the way out to the node itself.
      h('div', { class: 'netmap-group' },
        h('div', { class: 'section-title' }, 'On the map'),
        h('div', { class: 'netmap-actions' },
          below.length ? h('button', { type: 'button', class: 'btn btn-sm', 'aria-pressed': folded ? 'true' : 'false', onclick: () => setArranged(toggleFolded(store.value, id)) }, icon('layers'), folded ? `Unfold its ${plural(below.length, 'node')}` : 'Fold its cluster') : null,
          pinned ? h('button', { type: 'button', class: 'btn btn-sm', title: 'Let the layout place it again, next to what it depends on', onclick: () => setArranged(unpin(forest, store.value, id)) }, icon('pin'), 'Unpin') : null,
          h('a', { class: 'btn btn-sm', href: `#/nodes/${n.id}` }, icon('external'), 'Open the node'))),
    ]);

    // Changing it: what it depends on, its colour and the look of its line.
    if (!isAdmin()) return;
    const blocked = new Set([id, ...below]);
    const options = all.map((x) => x.node).filter((x) => !blocked.has(String(x.id))).sort((a, b) => compareNames(a.name, b.name));
    const sel = selectInput({ options: [{ value: '', label: 'Nothing (a root of the map)' }, ...options.map((x) => ({ value: x.id, label: `${x.name}${x.host ? ` (${x.host})` : ''}` }))], value: n.dependsOnNodeId ?? '' });
    const saveBtn = h('button', { type: 'button', class: 'btn btn-primary btn-sm', onclick: () => setParent(n, sel.value ? Number(sel.value) : null) }, 'Save');
    // The line to the parent has a look of its own, kept on this node and
    // seen on every map; what it leaves unset follows the map's.
    const linkEditor = parent ? linkStyleEditor({
      parentName: parent.name, value: n.linkStyle,
      defaults: { color: state.cfg.lineColor, width: state.cfg.lineWidth, dash: state.cfg.lineStyle },
      onChange: (style) => setLinkStyle(n, style),
    }) : null;
    const colour = nodeColourControl({ value: n.color || '', label: 'Node colour', onChange: (v) => setColour(n, v) });
    const details = h('details', { class: 'netmap-edit' },
      h('summary', null, icon('edit'), 'Edit its dependency and colours'),
      field({ label: 'Depends on', input: h('div', { class: 'row netmap-parent u-gap-6 u-nowrap-flex' }, sel, saveBtn), help: 'While the node it depends on is down, this node’s failures are put down to it and its alerts are held back.' }),
      field({ label: 'Colour', input: colour, help: 'Fills this node’s box on the map and its row in the node list.' }),
      linkEditor,
      h('a', { class: 'btn btn-sm', href: `#/nodes/${n.id}/edit` }, icon('edit'), 'Edit everything else about it'));
    details.open = selectedCard.__editOpen ?? false;
    details.addEventListener('toggle', () => { selectedCard.__editOpen = details.open; });
    selectedCard.append(details);
  }

  function select(id, { focus = false } = {}) {
    state.selected = String(id);
    if (focus) state.map?.focus(id); else state.map?.select(id);
    showTab('node');
    renderSelected();
  }

  /* ---------- Arranging ---------- */
  function setArranged(next, { quiet = false } = {}) {
    state.map?.setArranged(next);
    const saved = store.change(next, { quiet });
    renderSelected();
    return saved;
  }

  function tidyUnpinnedNodes() {
    const v = state.map?.view();
    if (!v?.layout.radial) { toast('Tidying up is for the Clusters layout: choose it under Customise.'); return; }
    const before = store.value;
    setArranged(tidyUnpinned(v.forest, v.layout.pos, v.layout.cell, before, { node: { w: v.boxW, h: v.boxH }, spacing: v.spacing }));
    toast('Tidied up: the nodes nobody pinned are back next to what they depend on.', { kind: 'success', action: { label: 'Undo', onClick: () => setArranged(before, { quiet: true }) } });
  }

  async function tidyEverything() {
    const pinnedCount = Object.values(store.value.positions || {}).filter((p) => p.pinned).length;
    if (pinnedCount) {
      const ok = await confirmDialog({ title: 'Tidy up everything?', message: `This forgets where ${plural(pinnedCount, 'pinned node')} ${pinnedCount === 1 ? 'was' : 'were'} put${isAdmin() ? ', for everyone' : ''}, and lays the whole map out again. Folded clusters stay folded.`, confirmLabel: 'Tidy up everything' });
      if (!ok) return;
    }
    const before = store.value;
    setArranged({ ...before, positions: {} });
    toast('The whole map is laid out afresh.', { kind: 'success', action: { label: 'Undo', onClick: () => setArranged(before, { quiet: true }) } });
  }

  function toggleLock() {
    setLayoutLocked(!layoutLocked());
    state.map?.setCanArrange(mayArrange());
    renderMap();
    state.map?.select(state.selected);
    toast(layoutLocked() ? 'The layout is locked in this browser: nodes cannot be dragged.' : 'The layout is unlocked: drag nodes to arrange them.');
  }

  /* ---------- Changing a node ---------- */
  async function setParent(n, parentId) {
    if ((n.dependsOnNodeId ?? null) === parentId) { toast('Nothing changed'); return; }
    try {
      await api.patch('/api/nodes/bulk', { nodeIds: [n.id], node: { dependsOnNodeId: parentId } });
      toast(parentId ? `${n.name} now depends on ${nameOf(parentId)}` : `${n.name} no longer depends on anything`, { kind: 'success' });
      // A place it was left in next to its old parent means nothing now.
      const p = store.value.positions?.[String(n.id)];
      if (p && !p.pinned) { const next = { ...store.value, positions: { ...store.value.positions } }; delete next.positions[String(n.id)]; store.change(next, { quiet: true }); state.map?.setArranged(next); }
      await load(); state.map?.update(entries(), state.cfg); renderSelected();
    } catch (err) { toast(err.message, { kind: 'error' }); }
  }

  // A node's colour is saved as it is picked; the map redraws from what came
  // back, and a refusal puts the panel back as it was.
  async function setColour(n, color) {
    try {
      await api.patch('/api/nodes/bulk', { nodeIds: [n.id], node: { color } });
      await load();
      if (!state.destroyed) state.map?.update(entries(), state.cfg);
    } catch (err) {
      toast(err.message, { kind: 'error' });
      if (!state.destroyed) { await load().catch(() => {}); renderSelected(); }
    }
  }

  // A link's style is drawn the moment it is changed and saved just after:
  // a run of changes (a thickness stepped up one notch at a time) is one
  // request carrying the last of them, and saves go one after another so the
  // last change is the one that stays. The map then redraws from what came
  // back; a failure puts the panel back as it was saved.
  let linkSaving = Promise.resolve();
  let linkPending = null;
  // A change still waiting to be saved is kept on top of whatever was just
  // loaded, so the map never flicks back to an older style in between.
  function keepPending() {
    const e = linkPending && entryOf(linkPending.id);
    if (e) e.node.linkStyle = linkPending.style;
  }
  function setLinkStyle(n, style) {
    const e = entryOf(n.id);
    if (e) { e.node.linkStyle = style; state.map?.update(entries(), state.cfg); }
    const queued = !!linkPending;
    linkPending = { id: n.id, style };
    if (queued) return linkSaving;
    linkSaving = linkSaving.then(async () => {
      await new Promise((resolve) => setTimeout(resolve, 200));
      const { id, style: latest } = linkPending;
      linkPending = null;
      try {
        await api.patch('/api/nodes/bulk', { nodeIds: [id], node: { linkStyle: latest } });
        await load();
        keepPending();
        if (!state.destroyed) state.map?.update(entries(), state.cfg);
      } catch (err) {
        toast(err.message, { kind: 'error' });
        if (!state.destroyed) { await load().catch(() => {}); renderSelected(); }
      }
    });
    return linkSaving;
  }

  async function addToDashboard() {
    let dashboards = [];
    try { dashboards = await api.get('/api/dashboards'); } catch (e) { toast(e.message, { kind: 'error' }); return; }
    if (!dashboards.length) { toast('Create a dashboard first', { kind: 'error' }); return; }
    const sel = h('select', null, dashboards.map((d) => h('option', { value: d.id }, d.name)));
    const ok = await confirmDialog({ title: 'Add the network map to a dashboard', confirmLabel: 'Add', body: h('div', { class: 'stack-sm' }, field({ label: 'Dashboard', input: sel }), h('p', { class: 'note' }, 'The widget keeps the layout, labels and filters chosen here, and draws the map as it is arranged; edit the widget to change them.')) });
    if (!ok) return;
    const d = dashboards.find((x) => String(x.id) === sel.value);
    const widgets = [...(d.widgets || []), { id: uid('w'), type: 'network_map', title: 'Network map', width: 4, height: 3, config: state.cfg }];
    try { await api.put(`/api/dashboards/${d.id}`, { ...d, widgets }); toast(`Added to ${d.name}`, { kind: 'success', action: { label: 'Open', onClick: () => ctx.navigate(`/dashboard/${d.id}`) } }); } catch (e) { toast(e.message, { kind: 'error' }); }
  }

  try { await Promise.all([load(), store.load()]); } catch (e) { replace(root, h('div', { class: 'card' }, emptyState({ icon: 'alert', title: 'Could not load the network map', text: e.message }))); return { destroy() {} }; }
  if (state.destroyed) return { destroy() {} };
  renderMap(); renderSide();
  if (state.selected && entryOf(state.selected)) state.map?.focus(state.selected);

  return {
    async refresh() {
      await load();
      if (state.destroyed) return;
      keepPending();
      state.map?.update(entries(), state.cfg);
      // The side panel holds a form; only redraw it when nothing in it has focus.
      if (!selectedCard.contains(document.activeElement)) renderSelected();
    },
    themeChanged() { state.map?.update(entries(), state.cfg); },
    destroy() { state.destroyed = true; sim.destroy(); state.map?.destroy(); },
  };
}

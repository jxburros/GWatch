// The network map, presented: full screen, nothing that edits anything, big
// enough to read across a room. It shows the map as it is arranged and
// styled, frozen at the moment it was opened (so a real alert cannot change
// it mid-sentence) unless switched to live, optionally without addresses.
// Choosing a node shows what depends on it, and the what-if simulation shows
// what a failure or maintenance there would cut off — nothing real changes.

import { api } from '../api.js';
import { h, icon, toast, statusPill } from '../components.js';
import { plural, compareNames } from '../fmt.js';
import { renderNetworkMap, buildForest, descendantsOf, ancestorsOf, biggestRisks } from '../netmap.js';
import { arrangementStore, createSimulation, nodeFinder, exportMapPng } from '../netmap-tools.js';
import { savedConfig } from './network-map.js';

export async function mount(root, ctx) {
  const state = { overview: null, live: false, frozenAt: null, hideHosts: false, selected: null, map: null, destroyed: false };
  // Shown as set up on the map page, a size up, with the legend and lines
  // heavy enough to see from the back of the room.
  const base = savedConfig();
  const cfg = { ...base, size: 'large', showLegend: true, lineWidth: Math.max(base.lineWidth, 3) };
  const store = arrangementStore({ canSave: () => false });
  const entries = () => state.overview?.nodes || [];
  const entryOf = (id) => entries().find((e) => String(e.node.id) === String(id));
  const nameOf = (id) => entryOf(id)?.node.name || 'that node';

  const wrap = h('div', { class: 'netmap-present' });
  const bar = h('header', { class: 'netmap-present-bar' });
  const stageHost = h('div', { class: 'netmap-present-map' });
  const card = h('aside', { class: 'netmap-present-card card', 'aria-live': 'polite' });
  const risks = h('aside', { class: 'netmap-present-risks card' });
  card.hidden = true; risks.hidden = true;
  wrap.append(bar, stageHost, card, risks);
  root.append(wrap);

  const sim = createSimulation({
    names: nameOf,
    forest: () => state.map?.view()?.all || buildForest(entries().map((e) => e.node)),
    onChange: (s) => { state.map?.setSim(s); renderCard(); },
  });
  sim.el.classList.add('netmap-present-sim');
  wrap.append(sim.el);

  async function load() {
    state.overview = await api.get('/api/overview');
    state.frozenAt = new Date();
  }

  const fmtTime = (d) => d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  // A button of the bar: an icon and a label; on a narrow screen the label is
  // hidden and stays the button's name (and its tooltip).
  const tool = (iconName, label, attrs = {}) => h(attrs.href ? 'a' : 'button', { type: attrs.href ? null : 'button', class: 'btn btn-sm', 'aria-label': label, title: label, ...attrs }, icon(iconName), h('span', { class: 'btn-label' }, label));
  function renderBar() {
    const liveBtn = tool(state.live ? 'activity' : 'pause', state.live ? 'Live' : `Frozen at ${fmtTime(state.frozenAt || new Date())}`, { 'aria-pressed': state.live ? 'true' : 'false', onclick: async () => {
      state.live = !state.live;
      if (state.live) { await load().catch((e) => toast(e.message, { kind: 'error' })); state.map?.update(entries(), cfg); }
      renderBar();
    } });
    const hostsBtn = tool('eye', state.hideHosts ? 'Addresses hidden' : 'Hide addresses', { 'aria-pressed': state.hideHosts ? 'true' : 'false', onclick: () => {
      state.hideHosts = !state.hideHosts; state.map?.setHideHosts(state.hideHosts); renderBar(); renderCard();
    } });
    bar.replaceChildren(
      h('div', { class: 'netmap-present-title' }, icon('network'), h('h1', null, 'Network map'), h('span', { class: 'muted' }, `${plural(entries().length, 'node')}`)),
      nodeFinder(entries, (id) => choose(id, true)),
      h('div', { class: 'netmap-present-tools' },
        liveBtn, hostsBtn,
        tool('alert', 'Biggest risks', { 'aria-pressed': risks.hidden ? 'false' : 'true', onclick: () => { risks.hidden = !risks.hidden; renderRisks(); renderBar(); } }),
        tool('image', 'Save as PNG', { onclick: () => exportMapPng(state.map?.svg()) }),
        document.fullscreenEnabled ? tool('maximize', 'Full screen', { onclick: () => (document.fullscreenElement ? document.exitFullscreen() : wrap.requestFullscreen?.()) }) : null,
        tool('x', 'Leave', { href: '#/map' })));
  }

  function renderRisks() {
    if (risks.hidden) return;
    const f = buildForest(entries().map((e) => e.node));
    const list = biggestRisks(f, null, 8);
    risks.replaceChildren(h('div', { class: 'card-title' }, 'Biggest risks'),
      h('p', { class: 'help' }, 'The nodes whose loss takes the most with it. Choose one, then simulate its failure.'),
      h('ol', { class: 'netmap-risks' }, list.map((r) => h('li', null,
        h('a', { href: '#', onclick: (ev) => { ev.preventDefault(); choose(r.id, true); } }, r.node.name),
        h('span', { class: 'muted' }, ` ${plural(r.below, 'node')} below`)))));
  }

  function choose(id, focus = false) {
    state.selected = id != null ? String(id) : null;
    if (focus && state.selected) state.map?.focus(state.selected); else state.map?.select(state.selected);
    renderCard();
  }

  function renderCard() {
    const e = state.selected && entryOf(state.selected);
    card.hidden = !e;
    if (!e) { card.replaceChildren(); return; }
    const n = e.node, id = String(n.id);
    const f = buildForest(entries().map((x) => x.node));
    const below = descendantsOf(f, id);
    const kids = (f.children.get(id) || []).map((k) => entryOf(k)?.node).filter(Boolean).sort((a, b) => compareNames(a.name, b.name));
    const chain = ancestorsOf(f, id).map((k) => entryOf(k)?.node).filter(Boolean);
    const st = state.map?.view()?.states.get(id);
    const simKind = sim.kindOf(id);
    card.replaceChildren(
      h('div', { class: 'card-title row-between' }, h('span', { class: 'truncate' }, n.name), statusPill(st?.status || e.status)),
      !state.hideHosts && n.host ? h('div', { class: 'muted mono' }, n.host) : null,
      h('p', null, chain.length ? `Depends on ${chain.map((p) => p.name).join(' › ')}.` : 'Depends on nothing: a root of the map.'),
      h('p', null, kids.length ? `${plural(kids.length, 'node')} depend${kids.length === 1 ? 's' : ''} on it directly, ${plural(below.length, 'node')} in all.` : 'Nothing depends on it.'),
      h('div', { class: 'netmap-actions' },
        simKind ? h('button', { type: 'button', class: 'btn btn-sm', onclick: () => sim.remove(id) }, icon('x'), 'Take out of the simulation')
          : [h('button', { type: 'button', class: 'btn btn-sm btn-primary', onclick: () => sim.add(id, 'down') }, icon('zap'), 'Simulate a failure'),
            h('button', { type: 'button', class: 'btn btn-sm', onclick: () => sim.add(id, 'maintenance') }, icon('wrench'), 'Simulate maintenance')],
        h('button', { type: 'button', class: 'btn btn-sm', onclick: () => choose(null) }, 'Close')));
  }

  try { await Promise.all([load(), store.load()]); } catch (e) {
    root.append(h('div', { class: 'card' }, h('p', null, `Could not load the network map: ${e.message}`), h('a', { class: 'btn', href: '#/map' }, 'Back to the map')));
    return { destroy() {} };
  }
  if (state.destroyed) return { destroy() {} };
  renderBar();
  state.map = renderNetworkMap(stageHost, entries(), cfg, {
    interactive: true, title: 'Network map', arranged: store.value, canArrange: false,
    onSelect: (n) => choose(n ? n.id : null),
  });

  return {
    // Frozen unless switched to live: a presentation does not move under the presenter.
    async refresh() {
      if (!state.live || state.destroyed) return;
      await load();
      state.map?.update(entries(), cfg);
      renderBar();
      renderCard();
    },
    themeChanged() { state.map?.update(entries(), cfg); },
    destroy() { state.destroyed = true; sim.destroy(); state.map?.destroy(); if (document.fullscreenElement) document.exitFullscreen?.().catch?.(() => {}); },
  };
}

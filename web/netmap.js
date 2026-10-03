// Network map: every node drawn under the node it depends on.
//
// A node's "Depends on" is what dependency-aware alerting reads — while a
// parent is down, its children's failures are put down to it and their
// alerts held back. This module turns those links into a picture: a forest
// with the upstream devices (the firewall, the gateway, a core switch) at the
// root and everything that hangs off them below. It is shared by the Network
// map page and the dashboard's network map widget, so one configured on the
// page can be pinned to a dashboard unchanged.
//
// The layout is a plain tidy tree, worked out here with no library: leaves
// take consecutive slots, each parent sits centred over its children, and the
// depth is the distance from the root. Nodes with no dependency either way
// are set apart in a block of their own, so a long row of unrelated devices
// does not stretch the trees out of shape. The arithmetic is pure and
// exported (buildForest, layoutMap) so it can be tested without a browser.

import { h, icon, statusMeta, field, selectInput, checkbox } from './components.js';
import { compareNames, inGroup, plural } from './fmt.js';

export const MAP_ORIENTATIONS = [
  { value: 'down', label: 'Top to bottom' },
  { value: 'right', label: 'Left to right' },
  { value: 'radial', label: 'Radial' },
];
export const MAP_LABELS = [
  { value: 'name', label: 'Name' },
  { value: 'name-host', label: 'Name and address' },
  { value: 'none', label: 'No labels (status only)' },
];
export const MAP_SIZES = [
  { value: 'small', label: 'Small' },
  { value: 'medium', label: 'Medium' },
  { value: 'large', label: 'Large' },
];
export const MAP_EDGES = [
  { value: 'curved', label: 'Curved' },
  { value: 'straight', label: 'Straight' },
  { value: 'elbow', label: 'Right-angled' },
];

/** A complete map configuration with its defaults filled in. */
export function normalizeMapConfig(cfg = {}) {
  const c = cfg && typeof cfg === 'object' ? cfg : {};
  const pick = (list, v, fb) => (list.some((o) => o.value === v) ? v : fb);
  return {
    orientation: pick(MAP_ORIENTATIONS, c.orientation, 'down'),
    labels: pick(MAP_LABELS, c.labels, 'name'),
    size: pick(MAP_SIZES, c.size, 'medium'),
    edges: pick(MAP_EDGES, c.edges, 'curved'),
    group: typeof c.group === 'string' ? c.group : '',
    tag: typeof c.tag === 'string' ? c.tag : '',
    onlyLinked: !!c.onlyLinked,
    // Lines out of a parent that is down are drawn in the down colour, so
    // the reach of an outage can be followed by eye.
    colorEdges: c.colorEdges !== false,
    showLegend: c.showLegend !== false,
  };
}

/**
 * The dependency forest of a set of nodes: `children` maps a node id to its
 * children (sorted by name), `parentOf` a node id to its parent's id, and
 * `roots` the nodes nothing they depend on is in the set (sorted by name).
 * A parent that is not in the set leaves its child a root. A cycle — which
 * the service refuses, but data from elsewhere could carry — is broken at
 * the first node of it met, which becomes a root; `cycles` lists those ids.
 */
export function buildForest(nodes) {
  const byId = new Map();
  for (const n of nodes || []) if (n && n.id != null) byId.set(String(n.id), n);
  const parentOf = new Map();
  for (const [id, n] of byId) {
    const p = n.dependsOnNodeId;
    if (p != null && String(p) !== id && byId.has(String(p))) parentOf.set(id, String(p));
  }
  // Break cycles: follow each node's chain upwards; meeting a node twice
  // means a loop, and the link out of the node where it closes is dropped.
  const cycles = [];
  for (const id of byId.keys()) {
    const seen = new Set([id]);
    let cur = id;
    while (parentOf.has(cur)) {
      const up = parentOf.get(cur);
      if (seen.has(up)) { parentOf.delete(cur); cycles.push(cur); break; }
      seen.add(up); cur = up;
    }
  }
  const byName = (a, b) => compareNames(byId.get(a)?.name, byId.get(b)?.name) || compareNames(a, b);
  const children = new Map();
  for (const id of byId.keys()) children.set(id, []);
  for (const [id, p] of parentOf) children.get(p).push(id);
  for (const list of children.values()) list.sort(byName);
  const roots = [...byId.keys()].filter((id) => !parentOf.has(id)).sort(byName);
  return { byId, parentOf, children, roots, cycles };
}

/** How many nodes hang below `id`, itself included. */
function subtreeSize(forest, id) {
  let n = 1;
  for (const c of forest.children.get(id) || []) n += subtreeSize(forest, c);
  return n;
}

/**
 * Positions for every node, in slots and levels rather than pixels:
 * { pos: Map id → { slot, level }, slots, levels, linked: Set, isolated: [] }.
 * For a tree layout `slot` runs across and `level` down from the roots; for
 * a radial one `slot` is the angle (in slots of the full circle) and `level`
 * the ring. Trees come first, largest first; nodes with no link at all are
 * packed into rows of their own after them, or left out with `onlyLinked`.
 */
export function layoutMap(forest, { orientation = 'down', onlyLinked = false, isolatedPerRow = 0 } = {}) {
  const pos = new Map();
  const linked = new Set();
  for (const [id, p] of forest.parentOf) { linked.add(id); linked.add(p); }
  const trees = forest.roots.filter((r) => linked.has(r))
    .sort((a, b) => subtreeSize(forest, b) - subtreeSize(forest, a) || compareNames(forest.byId.get(a)?.name, forest.byId.get(b)?.name));
  const isolated = forest.roots.filter((r) => !linked.has(r));

  let next = 0;
  let deepest = trees.length ? 0 : -1;
  const place = (id, level) => {
    deepest = Math.max(deepest, level);
    const kids = forest.children.get(id) || [];
    if (!kids.length) { pos.set(id, { slot: next++, level }); return; }
    for (const k of kids) place(k, level + 1);
    const first = pos.get(kids[0]).slot, last = pos.get(kids[kids.length - 1]).slot;
    pos.set(id, { slot: (first + last) / 2, level });
  };
  trees.forEach((r, i) => { if (i) next += 0.5; place(r, 0); });
  let slots = next;
  let levels = deepest + 1;

  if (orientation === 'radial') {
    // Everything around one centre: a single tree's root sits in the middle,
    // several trees (and the unlinked nodes) take the first ring.
    const one = trees.length === 1 && (!isolated.length || onlyLinked);
    if (!onlyLinked) for (const id of isolated) pos.set(id, { slot: next++, level: 0 });
    slots = Math.max(1, next);
    for (const p of pos.values()) p.level += one ? 0 : 1;
    if (one) pos.set(trees[0], { slot: 0, level: 0 });
    levels = Math.max(0, ...[...pos.values()].map((p) => p.level)) + 1;
    return { pos, slots, levels, linked, isolated: onlyLinked ? [] : isolated, radial: true };
  }

  if (!onlyLinked && isolated.length) {
    // A block under (or beside) the trees, as wide as they are.
    const perRow = Math.max(1, isolatedPerRow || Math.max(4, Math.ceil(slots) || Math.ceil(Math.sqrt(isolated.length * 2))));
    const top = levels + (levels ? 0.2 : 0);
    isolated.forEach((id, i) => pos.set(id, { slot: i % perRow, level: top + Math.floor(i / perRow) }));
    slots = Math.max(slots, Math.min(perRow, isolated.length));
    levels = top + Math.ceil(isolated.length / perRow);
  }
  return { pos, slots: Math.max(1, slots), levels: Math.max(1, levels), linked, isolated: onlyLinked ? [] : isolated, radial: false };
}

/**
 * Which overview entries a map draws: those matching the group and tag
 * filters, plus every node upstream of one of them (drawn faded), so the
 * chain a filtered node depends on is never cut off.
 */
export function mapEntries(entries, cfg) {
  const c = normalizeMapConfig(cfg);
  const all = (entries || []).filter((e) => e && e.node);
  const byId = new Map(all.map((e) => [String(e.node.id), e]));
  const match = (n) => (!c.group || inGroup(n, c.group)) && (!c.tag || (n.tags || []).includes(c.tag));
  const out = new Map();
  for (const e of all) {
    if (!match(e.node)) continue;
    out.set(String(e.node.id), { ...e, context: false });
    let p = e.node.dependsOnNodeId; const seen = new Set();
    while (p != null && byId.has(String(p)) && !seen.has(String(p))) {
      seen.add(String(p));
      const up = byId.get(String(p));
      if (!out.has(String(p))) out.set(String(p), { ...up, context: !match(up.node) });
      p = up.node.dependsOnNodeId;
    }
  }
  return [...out.values()];
}

/* ---------- Drawing ---------- */

const SVG_NS = 'http://www.w3.org/2000/svg';
function svg(tag, attrs = {}, ...children) {
  const el = document.createElementNS(SVG_NS, tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v == null || v === false) continue;
    if (k === 'class') el.setAttribute('class', v);
    else if (k.startsWith('on') && typeof v === 'function') el.addEventListener(k.slice(2).toLowerCase(), v);
    else el.setAttribute(k, String(v));
  }
  for (const ch of children.flat()) {
    if (ch == null || ch === false) continue;
    el.append(ch instanceof Node ? ch : document.createTextNode(String(ch)));
  }
  return el;
}

const SIZES = {
  small: { w: 112, hName: 26, hHost: 38, gapX: 18, gapY: 46, font: 11 },
  medium: { w: 148, hName: 32, hHost: 46, gapX: 24, gapY: 58, font: 12.5 },
  large: { w: 188, hName: 40, hHost: 56, gapX: 30, gapY: 72, font: 14 },
};
const BARE = { small: 16, medium: 20, large: 26 };

/** Shortens text to fit about `chars` characters. */
function fit(text, chars) {
  const s = String(text ?? '');
  return s.length > chars ? `${s.slice(0, Math.max(1, chars - 1))}…` : s;
}

/**
 * Draws a map into `host`. `entries` are overview node entries
 * ({ node, status, affectedBy }). Options: `interactive` adds zooming and
 * panning (wheel, drag, the +/− buttons and the keyboard) and `onSelect(node)`
 * is called when a node is chosen; without it a node is a link to its page.
 * Returns { update(entries, cfg?), fit(), destroy(), svg }.
 */
export function renderNetworkMap(host, entries, cfg, { interactive = false, onSelect = null, selectedId = null, title = 'Network map' } = {}) {
  let c = normalizeMapConfig(cfg);
  let data = entries || [];
  let selected = selectedId != null ? String(selectedId) : null;
  const wrap = h('div', { class: `netmap${interactive ? ' netmap-interactive' : ''}` });
  const stage = h('div', { class: 'netmap-stage' });
  const legend = h('div', { class: 'netmap-legend' });
  const listDetails = h('details', { class: 'netmap-list chart-data' }, h('summary', null, 'View as list'));
  wrap.append(stage, legend, listDetails);
  host.append(wrap);

  // The visible window onto the drawing, in drawing units.
  let box = null; let full = null;
  let root = null;
  const setBox = (b) => { box = b; root?.setAttribute('viewBox', `${b.x} ${b.y} ${b.w} ${b.h}`); };

  function draw() {
    stage.replaceChildren();
    const shown = mapEntries(data, c);
    const forest = buildForest(shown.map((e) => e.node));
    const layout = layoutMap(forest, { orientation: c.orientation, onlyLinked: c.onlyLinked });
    const entryOf = new Map(shown.map((e) => [String(e.node.id), e]));
    const S = SIZES[c.size];
    const bare = c.labels === 'none';
    const boxH = bare ? BARE[c.size] : c.labels === 'name-host' ? S.hHost : S.hName;
    const boxW = bare ? BARE[c.size] : S.w;
    const chars = Math.floor((boxW - 26) / (S.font * 0.56));

    if (!layout.pos.size) {
      stage.append(h('div', { class: 'netmap-empty' },
        icon('network'),
        h('strong', null, data.length ? 'Nothing to map with these options' : 'No nodes yet'),
        h('span', null, data.length
          ? (c.onlyLinked ? 'None of the nodes shown depends on another. Set a node’s “Depends on” to draw a link.' : 'No node matches the group or tag chosen.')
          : 'Add nodes, then give each one the node it depends on.')));
      legend.replaceChildren(); listDetails.hidden = true;
      return;
    }
    listDetails.hidden = false;

    // Pixel centres for every node.
    const centre = new Map();
    let cx0 = 0, cy0 = 0;
    if (layout.radial) {
      const ringGap = Math.max(boxH, bare ? 0 : 0) + S.gapY + (bare ? 10 : 30);
      const circumference = layout.slots * (boxW + S.gapX);
      const r1 = Math.max(ringGap, circumference / (2 * Math.PI));
      for (const [id, p] of layout.pos) {
        if (p.level === 0) { centre.set(id, { x: 0, y: 0 }); continue; }
        const a = (p.slot / layout.slots) * Math.PI * 2 - Math.PI / 2;
        const r = r1 + (p.level - 1) * ringGap;
        centre.set(id, { x: Math.cos(a) * r, y: Math.sin(a) * r });
      }
    } else {
      const stepAcross = (c.orientation === 'right' ? boxH : boxW) + S.gapX;
      const stepDown = (c.orientation === 'right' ? boxW + S.gapY * 1.4 : boxH + S.gapY);
      for (const [id, p] of layout.pos) {
        const across = p.slot * stepAcross, down = p.level * stepDown;
        centre.set(id, c.orientation === 'right' ? { x: down, y: across } : { x: across, y: down });
      }
    }
    const xs = [...centre.values()].map((p) => p.x), ys = [...centre.values()].map((p) => p.y);
    const pad = 18;
    full = { x: Math.min(...xs) - boxW / 2 - pad, y: Math.min(...ys) - boxH / 2 - pad, w: Math.max(...xs) - Math.min(...xs) + boxW + pad * 2, h: Math.max(...ys) - Math.min(...ys) + boxH + pad * 2 };
    cx0 = full.x; cy0 = full.y;

    root = svg('svg', {
      class: 'netmap-svg', role: 'group', 'aria-label': `${title}: ${plural(layout.pos.size, 'node')}, ${plural(forest.parentOf.size, 'dependency', 'dependencies')}`,
      viewBox: `${cx0} ${cy0} ${full.w} ${full.h}`, preserveAspectRatio: 'xMidYMid meet',
    });
    const edgesG = svg('g', { class: 'netmap-edges', 'aria-hidden': 'true' });
    const nodesG = svg('g', { class: 'netmap-nodes' });
    root.append(edgesG, nodesG);

    // Edges: from the parent's near side to the child's.
    for (const [id, p] of forest.parentOf) {
      const a = centre.get(p), b = centre.get(id);
      if (!a || !b) continue;
      const parentStatus = entryOf.get(p)?.status || 'unknown';
      const cut = c.colorEdges && (parentStatus === 'down');
      let d;
      if (layout.radial || c.edges === 'straight') {
        d = `M${a.x},${a.y} L${b.x},${b.y}`;
      } else if (c.orientation === 'right') {
        const x1 = a.x + boxW / 2, x2 = b.x - boxW / 2, mx = (x1 + x2) / 2;
        d = c.edges === 'elbow' ? `M${x1},${a.y} H${mx} V${b.y} H${x2}` : `M${x1},${a.y} C${mx},${a.y} ${mx},${b.y} ${x2},${b.y}`;
      } else {
        const y1 = a.y + boxH / 2, y2 = b.y - boxH / 2, my = (y1 + y2) / 2;
        d = c.edges === 'elbow' ? `M${a.x},${y1} V${my} H${b.x} V${y2}` : `M${a.x},${y1} C${a.x},${my} ${b.x},${my} ${b.x},${y2}`;
      }
      const ctx = entryOf.get(id)?.context || entryOf.get(p)?.context;
      edgesG.append(svg('path', { d, class: `netmap-edge${cut ? ' netmap-edge-down' : ''}${ctx ? ' netmap-context' : ''}`, 'data-from': p, 'data-to': id }));
    }

    // Nodes.
    for (const [id] of layout.pos) {
      const e = entryOf.get(id); if (!e) continue;
      const n = e.node; const p = centre.get(id);
      const status = n.enabled === false ? 'paused' : (e.status || 'unknown');
      const m = statusMeta(status);
      const kids = forest.children.get(id)?.length || 0;
      const parent = forest.parentOf.get(id);
      const parentName = parent ? entryOf.get(parent)?.node.name : '';
      const desc = `${n.name}, ${m.label}${n.host ? `, ${n.host}` : ''}${parentName ? `, depends on ${parentName}` : ''}${kids ? `, ${plural(kids, 'node')} depend${kids === 1 ? 's' : ''} on it` : ''}${e.affectedBy ? `, affected by ${e.affectedBy}` : ''}`;
      const g = svg('g', { class: `netmap-node status-${status in { up: 1, down: 1, degraded: 1, paused: 1, maintenance: 1 } ? status : 'unknown'}${e.context ? ' netmap-context' : ''}${selected === id ? ' selected' : ''}`, transform: `translate(${p.x},${p.y})`, 'data-node': id });
      const title = svg('title', {}, desc);
      if (bare) {
        g.append(title, svg('circle', { r: boxW / 2, class: 'netmap-dot' }));
      } else {
        g.append(title,
          svg('rect', { x: -boxW / 2, y: -boxH / 2, width: boxW, height: boxH, rx: 3, class: 'netmap-box' }),
          svg('rect', { x: -boxW / 2, y: -boxH / 2, width: 4, height: boxH, class: 'netmap-stripe' }),
          svg('circle', { cx: -boxW / 2 + 14, cy: c.labels === 'name-host' ? -boxH / 2 + S.hName / 2 : 0, r: S.font * 0.36, class: 'netmap-dot' }),
          svg('text', { x: -boxW / 2 + 24, y: c.labels === 'name-host' ? -boxH / 2 + S.hName / 2 : 0, 'dominant-baseline': 'central', class: 'netmap-name', 'font-size': S.font }, fit(n.name, chars)));
        if (c.labels === 'name-host') g.append(svg('text', { x: -boxW / 2 + 24, y: -boxH / 2 + S.hName / 2 + S.font * 1.15, 'dominant-baseline': 'central', class: 'netmap-host', 'font-size': S.font * 0.84 }, fit(n.host, chars + 2)));
        if (kids) g.append(svg('text', { x: boxW / 2 - 6, y: -boxH / 2 + 9, 'text-anchor': 'end', class: 'netmap-kids', 'font-size': S.font * 0.72 }, `▾${kids}`));
      }
      // A link to the node's page, or a button that selects it.
      const target = onSelect
        ? svg('a', { href: `#/nodes/${n.id}`, role: 'button', 'aria-label': desc, 'aria-pressed': selected === id ? 'true' : 'false', onclick: (ev) => { ev.preventDefault(); selected = id; onSelect(n, e); highlight(); } })
        : svg('a', { href: `#/nodes/${n.id}`, 'aria-label': desc });
      target.append(g);
      nodesG.append(target);
    }
    stage.append(root);
    if (interactive && box && box.w && box.h) setBox(box); else box = { ...full };

    // Legend and the text equivalent.
    const counts = {};
    for (const [id] of layout.pos) { const e = entryOf.get(id); const s = e?.node.enabled === false ? 'paused' : (e?.status || 'unknown'); counts[s] = (counts[s] || 0) + 1; }
    legend.replaceChildren();
    if (c.showLegend) {
      for (const s of ['up', 'degraded', 'down', 'maintenance', 'paused', 'unknown']) {
        if (!counts[s]) continue;
        legend.append(h('span', { class: 'netmap-key' }, h('span', { class: `orb orb-sm orb-${s}`, 'aria-hidden': 'true' }), `${statusMeta(s).label} ${counts[s]}`));
      }
      legend.append(h('span', { class: 'netmap-key muted' }, c.orientation === 'right' ? 'Each node sits to the right of the node it depends on' : layout.radial ? 'Each node sits outside the node it depends on' : 'Each node sits under the node it depends on'));
      if (forest.cycles.length) legend.append(h('span', { class: 'netmap-key text-degraded' }, icon('alert'), `${plural(forest.cycles.length, 'dependency loop')} broken to draw this`));
    }
    renderList(forest, entryOf, layout);
  }

  function renderList(forest, entryOf, layout) {
    const item = (id) => {
      const e = entryOf.get(id); if (!e) return null;
      const status = e.node.enabled === false ? 'paused' : (e.status || 'unknown');
      const kids = (forest.children.get(id) || []).map(item).filter(Boolean);
      return h('li', null, h('span', { class: `orb orb-sm orb-${status}`, 'aria-hidden': 'true' }), ' ', h('a', { href: `#/nodes/${e.node.id}` }, e.node.name), h('span', { class: 'muted' }, ` — ${statusMeta(status).label}`), kids.length ? h('ul', null, kids) : null);
    };
    const roots = forest.roots.filter((r) => layout.pos.has(r));
    const list = h('ul', { class: 'netmap-tree' }, roots.map(item));
    listDetails.replaceChildren(listDetails.firstChild, list);
  }

  function highlight() {
    for (const g of stage.querySelectorAll('.netmap-node')) {
      const on = g.getAttribute('data-node') === selected;
      g.classList.toggle('selected', on);
      g.parentNode?.setAttribute('aria-pressed', on ? 'true' : 'false');
    }
  }

  /* ---------- Zoom and pan (the page only) ---------- */
  const zoomBy = (f, cx, cy) => {
    if (!box || !full) return;
    const w = Math.min(full.w * 4, Math.max(full.w / 8, box.w * f));
    const k = w / box.w;
    const px = cx ?? box.x + box.w / 2, py = cy ?? box.y + box.h / 2;
    setBox({ x: px - (px - box.x) * k, y: py - (py - box.y) * k, w, h: box.h * k });
  };
  const fitAll = () => { if (full) setBox({ ...full }); };
  let off = () => {};
  if (interactive) {
    const toDrawing = (ev) => {
      const r = root.getBoundingClientRect();
      // preserveAspectRatio "meet": the drawing is scaled by the smaller ratio and centred.
      const s = Math.min(r.width / box.w, r.height / box.h);
      const ox = (r.width - box.w * s) / 2, oy = (r.height - box.h * s) / 2;
      return { x: box.x + (ev.clientX - r.left - ox) / s, y: box.y + (ev.clientY - r.top - oy) / s, s };
    };
    const onWheel = (ev) => { if (!root) return; ev.preventDefault(); const p = toDrawing(ev); zoomBy(ev.deltaY > 0 ? 1.15 : 1 / 1.15, p.x, p.y); };
    let drag = null;
    const onDown = (ev) => {
      if (ev.button !== 0 || !root || ev.target.closest('a')) return;
      drag = { x: ev.clientX, y: ev.clientY, box: { ...box }, s: toDrawing(ev).s };
      stage.setPointerCapture?.(ev.pointerId); stage.classList.add('panning');
    };
    const onMove = (ev) => { if (!drag) return; setBox({ ...drag.box, x: drag.box.x - (ev.clientX - drag.x) / drag.s, y: drag.box.y - (ev.clientY - drag.y) / drag.s }); };
    const onUp = () => { drag = null; stage.classList.remove('panning'); };
    const onKey = (ev) => {
      if (ev.target.closest('a')) return;
      const step = box ? box.w * 0.1 : 0;
      const k = { '+': () => zoomBy(1 / 1.25), '=': () => zoomBy(1 / 1.25), '-': () => zoomBy(1.25), '0': fitAll,
        ArrowLeft: () => setBox({ ...box, x: box.x - step }), ArrowRight: () => setBox({ ...box, x: box.x + step }),
        ArrowUp: () => setBox({ ...box, y: box.y - step }), ArrowDown: () => setBox({ ...box, y: box.y + step }) }[ev.key];
      if (k && box) { ev.preventDefault(); k(); }
    };
    stage.tabIndex = 0;
    stage.setAttribute('aria-label', 'Network map. Plus and minus zoom, arrow keys pan, 0 fits the whole map.');
    stage.addEventListener('wheel', onWheel, { passive: false });
    stage.addEventListener('pointerdown', onDown);
    stage.addEventListener('pointermove', onMove);
    stage.addEventListener('pointerup', onUp);
    stage.addEventListener('pointercancel', onUp);
    stage.addEventListener('keydown', onKey);
    const tools = h('div', { class: 'netmap-tools' },
      h('button', { type: 'button', class: 'icon-btn', 'aria-label': 'Zoom in', title: 'Zoom in', onclick: () => zoomBy(1 / 1.25) }, icon('plus')),
      h('button', { type: 'button', class: 'icon-btn', 'aria-label': 'Zoom out', title: 'Zoom out', onclick: () => zoomBy(1.25) }, icon('minus')),
      h('button', { type: 'button', class: 'icon-btn', 'aria-label': 'Fit the whole map', title: 'Fit', onclick: fitAll }, icon('maximize')));
    wrap.insertBefore(tools, stage);
    off = () => { stage.removeEventListener('wheel', onWheel); };
  }

  draw();
  return {
    svg: () => root,
    update(next, nextCfg) {
      data = next || [];
      if (nextCfg) { const before = JSON.stringify(c); c = normalizeMapConfig(nextCfg); if (JSON.stringify(c) !== before) box = null; }
      draw();
    },
    select(id) { selected = id != null ? String(id) : null; highlight(); },
    fit: fitAll,
    destroy() { off(); wrap.remove(); },
  };
}

/** The group and tag choices a map can be filtered by, from /api/groups. */
export function mapFilterOptions(groups) {
  return {
    groups: [{ value: '', label: 'All groups' }, ...((groups?.groups) || []).map((g) => ({ value: g.name, label: g.name }))],
    tags: [{ value: '', label: 'Any tag' }, ...((groups?.tags) || []).map((t) => ({ value: t.name, label: t.name }))],
  };
}

/**
 * The map's options as a form — shared by the Network map page and the
 * dashboard widget editor. `.value` is the configuration the controls hold;
 * `onChange(cfg)` fires on every change.
 */
export function mapConfigEditor(cfg, { groups = null, onChange } = {}) {
  const c = normalizeMapConfig(cfg);
  const emit = () => onChange && onChange(wrap.value);
  const opts = mapFilterOptions(groups);
  const orientation = selectInput({ options: MAP_ORIENTATIONS, value: c.orientation, onchange: emit });
  const labels = selectInput({ options: MAP_LABELS, value: c.labels, onchange: emit });
  const size = selectInput({ options: MAP_SIZES, value: c.size, onchange: emit });
  const edges = selectInput({ options: MAP_EDGES, value: c.edges, onchange: emit });
  const group = selectInput({ options: opts.groups, value: c.group, onchange: emit });
  const tag = selectInput({ options: opts.tags, value: c.tag, onchange: emit });
  const onlyLinked = checkbox({ label: 'Only nodes with a dependency', checked: c.onlyLinked, onChange: emit });
  const colorEdges = checkbox({ label: 'Colour the links out of a node that is down', checked: c.colorEdges, onChange: emit });
  const showLegend = checkbox({ label: 'Show the legend', checked: c.showLegend, onChange: emit });
  const wrap = h('div', { class: 'stack-sm netmap-options' },
    h('div', { class: 'form-grid' },
      field({ label: 'Layout', input: orientation }),
      field({ label: 'Labels', input: labels }),
      field({ label: 'Node size', input: size }),
      field({ label: 'Lines', input: edges }),
      field({ label: 'Group', input: group, help: 'The nodes a filtered node depends on stay on the map, faded.' }),
      field({ label: 'Tag', input: tag })),
    h('div', { class: 'stack-sm', style: { gap: '6px' } }, onlyLinked, colorEdges, showLegend));
  Object.defineProperty(wrap, 'value', { get: () => normalizeMapConfig({
    orientation: orientation.value, labels: labels.value, size: size.value, edges: edges.value, group: group.value, tag: tag.value,
    onlyLinked: onlyLinked.input.checked, colorEdges: colorEdges.input.checked, showLegend: showLegend.input.checked,
  }) });
  return wrap;
}


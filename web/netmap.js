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
// The layouts are worked out here with no library. The tree ones are a plain
// tidy tree: leaves take consecutive slots, each parent sits centred over its
// children, and the depth is the distance from the root. The clusters one
// (stored as "radial", which older saved maps already say) draws a node with
// things plugged into it at the centre of a ring of them, and a ring that
// holds a node with a ring of its own as one more circle around a circle.
// Nodes with no dependency either way are set apart in a block of their own,
// so a long row of unrelated devices does not stretch the trees out of shape.
// The arithmetic is pure and exported (buildForest, layoutMap) so it can be
// tested without a browser.
//
// How a line looks — its colour, thickness and dashes — is decided in one
// place, edgeLook: the link's own style (kept on the node at its lower end),
// else the map's, else the theme's. Both sets of values are validated here
// before they reach the drawing, whatever sent them.

import { h, icon, statusMeta, field, selectInput, checkbox, numberInput } from './components.js';
import { compareNames, inGroup, plural } from './fmt.js';

export const MAP_ORIENTATIONS = [
  { value: 'down', label: 'Top to bottom' },
  { value: 'right', label: 'Left to right' },
  { value: 'radial', label: 'Clusters (radial)' },
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
export const MAP_LINE_STYLES = [
  { value: 'solid', label: 'Solid' },
  { value: 'dashed', label: 'Dashed' },
  { value: 'dotted', label: 'Dotted' },
  { value: 'dash-dot', label: 'Dash and dot' },
];
/** The thickness a line may take, in drawing units; 1.5 is what it always was. */
export const LINE_WIDTH = { min: 1, max: 8, step: 0.5, default: 1.5 };

/* ---------- Line styles ---------- */

// The only colour forms a line may have. A colour ends up in an SVG style
// attribute, so it is matched against this exact shape and never "cleaned":
// a name, a function or a url() is not a colour here. The service applies the
// same rule to a link's colour (model.NormalizeLinkColor).
const HEX_COLOR = /^#(?:[0-9a-f]{3}|[0-9a-f]{6}|[0-9a-f]{8})$/i;

/** The colour in lower case, or '' when it is not #rgb, #rrggbb or #rrggbbaa. */
export function normalizeLineColor(v) {
  const s = typeof v === 'string' ? v.trim() : '';
  return HEX_COLOR.test(s) ? s.toLowerCase() : '';
}

/** A thickness pulled into range and rounded to a tenth; `fallback` when it is not a positive number. */
export function normalizeLineWidth(v, fallback = 0) {
  if (v == null || typeof v === 'boolean' || (typeof v === 'string' && !v.trim())) return fallback;
  const n = Number(v);
  if (!Number.isFinite(n) || n <= 0) return fallback;
  return Math.round(Math.min(LINE_WIDTH.max, Math.max(LINE_WIDTH.min, n)) * 10) / 10;
}

/** One of the line styles, or '' for anything else. */
export function normalizeLineDash(v) {
  const s = typeof v === 'string' ? v.trim().toLowerCase() : '';
  return MAP_LINE_STYLES.some((o) => o.value === s) ? s : '';
}

/**
 * A link's own style — { color, width, dash } — checked field by field: what
 * is not valid is dropped, so an unset field (colour '', width 0, dash '')
 * follows the map. null when it sets nothing at all.
 */
export function normalizeLinkStyle(raw) {
  if (!raw || typeof raw !== 'object') return null;
  const color = normalizeLineColor(raw.color), width = normalizeLineWidth(raw.width, 0), dash = normalizeLineDash(raw.dash);
  return color || width || dash ? { color, width, dash } : null;
}

/**
 * The stroke-dasharray for a line style at a thickness, with the line cap it
 * needs. The pattern grows with the line, so a thick dotted line is still
 * dotted and not a smear; dots are zero-length dashes with round caps.
 */
export function lineDash(style, width = LINE_WIDTH.default) {
  const w = Math.max(LINE_WIDTH.min, Number(width) || LINE_WIDTH.default);
  const r = (n) => Math.round(n * 10) / 10;
  switch (style) {
    case 'dashed': return { array: `${r(w * 3 + 3)} ${r(w * 2 + 2)}`, cap: 'butt' };
    case 'dotted': return { array: `0.01 ${r(w * 2.5 + 1)}`, cap: 'round' };
    case 'dash-dot': return { array: `${r(w * 3 + 3)} ${r(w * 1.5 + 3)} 0.01 ${r(w * 1.5 + 3)}`, cap: 'round' };
    default: return { array: 'none', cap: 'butt' };
  }
}

/**
 * How one line is drawn: its own style first, then the map's, then the
 * theme's (a colour of '' leaves the stylesheet to choose). `cut` is a line
 * out of a node that is down, drawn in the down colour whatever colour was
 * chosen — an outage must never be hidden by a pretty map — but still in the
 * thickness and dashes it was given. A dash of '' means nobody chose one, so
 * the stylesheet's own (a down line is dashed unless told otherwise) stands.
 * `cfg` is a normalised map configuration.
 */
export function edgeLook(cfg, linkStyle, cut = false) {
  const own = normalizeLinkStyle(linkStyle) || { color: '', width: 0, dash: '' };
  return {
    color: cut ? '' : own.color || cfg.lineColor,
    width: own.width || cfg.lineWidth,
    dash: own.dash || (cfg.lineStyle !== 'solid' ? cfg.lineStyle : ''),
    cut: !!cut,
  };
}

/** The inline style for an edgeLook. Inline so it outranks the stylesheet and travels with an exported SVG. */
export function edgeStyle(look) {
  const parts = [`stroke-width:${look.width}`];
  if (look.color) parts.push(`stroke:${look.color}`);
  if (look.dash) {
    const d = lineDash(look.dash, look.width);
    parts.push(`stroke-dasharray:${d.array}`, `stroke-linecap:${d.cap}`);
  }
  return parts.join(';');
}

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
    // The look of every line that has no style of its own. A colour of ''
    // is the theme's.
    lineColor: normalizeLineColor(c.lineColor),
    lineWidth: normalizeLineWidth(c.lineWidth, LINE_WIDTH.default),
    lineStyle: normalizeLineDash(c.lineStyle) || 'solid',
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
 * Positions for every node. For the tree layouts they are in slots and levels
 * rather than pixels:
 * { pos: Map id → { slot, level }, slots, levels, linked: Set, isolated: [], radial: false },
 * where `slot` runs across and `level` down from the roots. Trees come first,
 * largest first; nodes with no link at all are packed into rows of their own
 * after them, or left out with `onlyLinked`.
 *
 * The clusters layout (orientation "radial") needs to know how big a node is
 * to keep its rings clear of one another, so it works in pixels: pass
 * `node: { w, h }` (the box a node is drawn in), `spacing` (the least space
 * between two neighbouring boxes) and `reach` (the shortest a line from a node
 * to the ring around it may be). It returns
 * { pos: Map id → { x, y, level, ring }, rings: [{ id, x, y, r }], bounds,
 *   levels, linked, isolated, radial: true }
 * with `x`, `y` the centre of the node's box (a single tree's root is at 0,0),
 * `ring` the radius of the ring of its children (0 for a leaf) and `bounds`
 * the box that holds every node's box.
 */
export function layoutMap(forest, { orientation = 'down', onlyLinked = false, isolatedPerRow = 0, node = null, spacing = 24, reach = 58 } = {}) {
  const pos = new Map();
  const linked = new Set();
  for (const [id, p] of forest.parentOf) { linked.add(id); linked.add(p); }
  const trees = forest.roots.filter((r) => linked.has(r))
    .sort((a, b) => subtreeSize(forest, b) - subtreeSize(forest, a) || compareNames(forest.byId.get(a)?.name, forest.byId.get(b)?.name));
  const isolated = forest.roots.filter((r) => !linked.has(r));

  if (orientation === 'radial') {
    return layoutClusters(forest, trees, isolated, linked, { onlyLinked, isolatedPerRow, node: node || { w: 148, h: 32 }, spacing, reach });
  }

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
 * The clusters layout. A node with children is the centre of a ring and its
 * children are spaced evenly around it; a child with children of its own is
 * the centre of a ring of its own, and the whole of that sits on the ring of
 * its parent — circles around circles.
 *
 * Every cluster is measured first, from the leaves up, as a circle that holds
 * the node's box and everything below it. A ring is then given the radius
 * that keeps three things true: its children's circles clear the box of the
 * node in the middle (by `reach`, so there is always a line to see), clear
 * one another all the way round, and are all inside the circle the cluster
 * itself will be measured as. Two circles that do not overlap cannot hold two
 * boxes that do, so no box overlaps another.
 *
 * Children keep the order of their names. Round a node that has a parent they
 * start half a step away from the line to it, so the line runs through a gap
 * and never through a child; a node with one child puts it straight out
 * beyond itself, so a chain of switches reads as a chain and its circle is
 * not a ring around nothing (which would double in size with every link).
 *
 * Several trees: the biggest is laid out as above, the others sit on a ring
 * around it the same way. Nodes with no link at all are a block of rows under
 * everything, as in the tree layouts — a ring would suggest they hang off
 * something.
 */
function layoutClusters(forest, trees, isolated, linked, { onlyLinked, isolatedPerRow, node, spacing, reach: wantReach }) {
  // A line shorter than the space kept round a box would be all box.
  const reach = Math.max(wantReach, spacing / 2);
  const half = Math.hypot(node.w / 2, node.h / 2); // a box fits in a circle this big
  const own = half + spacing / 2; // ... with half the space to its neighbours all round
  const info = new Map();
  const pos = new Map();
  const rings = [];
  let deepest = trees.length ? 0 : -1;

  // Where the first child goes round a node with nothing above it: one child
  // to the right, two left and right, more starting at the top.
  const startAngle = (n) => (n === 1 ? 0 : n === 2 ? Math.PI : -Math.PI / 2);
  // The distance at which `n` circles, the two biggest of radii a and b, sit
  // evenly round a point without touching: neighbours are the closest pair, and
  // the two biggest are the worst neighbours there could be.
  const ringFor = (n, a, b, gap) => (n < 2 ? 0 : (a + b + gap) / (2 * Math.sin(Math.PI / n)));

  const measure = (id) => {
    const kids = forest.children.get(id) || [];
    const ms = kids.map(measure);
    let m;
    if (!ms.length) {
      m = { R: own, off: 0, rho: 0 };
    } else {
      // R is the radius of the circle that holds the cluster, centred `off`
      // beyond the node along the way out. A ring is centred on its node
      // (off 0); a node with one child is held by the smaller circle round
      // the two of them. A child's circle reaches R - off back towards the
      // ring's centre and R + off away from it.
      let back = 0, ext = 0, big1 = 0, big2 = 0;
      for (const c of ms) {
        back = Math.max(back, c.R - c.off); ext = Math.max(ext, c.R + c.off);
        if (c.R > big1) { big2 = big1; big1 = c.R; } else if (c.R > big2) big2 = c.R;
      }
      const rho = Math.max(half + back + reach, ringFor(ms.length, big1, big2, 0));
      if (ms.length === 1) {
        const far = rho + ms[0].off;
        m = { R: (own + far + ms[0].R) / 2, off: (far + ms[0].R - own) / 2, rho };
      } else {
        m = { R: Math.max(own, rho + ext), off: 0, rho };
      }
    }
    info.set(id, m);
    return m;
  };

  const put = (id, x, y, level, outward) => {
    deepest = Math.max(deepest, level);
    const m = info.get(id);
    const kids = forest.children.get(id) || [];
    pos.set(id, { x, y, level, ring: m.rho });
    if (!kids.length) return;
    rings.push({ id, x, y, r: m.rho });
    kids.forEach((k, j) => {
      // Round the root they start where startAngle says; round anything else
      // half a step on from the way back to the parent.
      const a = outward == null
        ? startAngle(kids.length) + (2 * Math.PI * j) / kids.length
        : outward + Math.PI + ((2 * j + 1) * Math.PI) / kids.length;
      put(k, x + m.rho * Math.cos(a), y + m.rho * Math.sin(a), level + 1, a);
    });
  };

  if (trees.length) {
    const ms = trees.map(measure);
    // Trees are separated by more than the nodes in one: they are separate things.
    const gap = spacing * 2;
    put(trees[0], 0, 0, 0, null);
    if (trees.length > 1) {
      // A root with one child puts its cluster's centre out to its right.
      const centre = { x: ms[0].off, y: 0 };
      const radii = ms.slice(1).map((m) => m.R).sort((a, b) => b - a);
      const n = radii.length;
      const dist = Math.max(ms[0].R + radii[0] + gap, ringFor(n, radii[0], radii[1] || 0, gap));
      for (let i = 1; i < trees.length; i++) {
        const a = startAngle(n) + (2 * Math.PI * (i - 1)) / n;
        put(trees[i], centre.x + dist * Math.cos(a) - ms[i].off, centre.y + dist * Math.sin(a), 0, null);
      }
    }
  }

  const extent = () => {
    let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
    for (const p of pos.values()) {
      minX = Math.min(minX, p.x - node.w / 2); maxX = Math.max(maxX, p.x + node.w / 2);
      minY = Math.min(minY, p.y - node.h / 2); maxY = Math.max(maxY, p.y + node.h / 2);
    }
    return pos.size ? { minX, minY, maxX, maxY } : { minX: 0, minY: 0, maxX: 0, maxY: 0 };
  };

  if (!onlyLinked && isolated.length) {
    // Rows, centred under the trees and about as wide as they are (or as
    // near a landscape rectangle as the count allows when there are none).
    const cellW = node.w + spacing, cellH = node.h + spacing;
    const n = isolated.length;
    const b = extent();
    const landscape = Math.ceil(Math.sqrt((n * 1.6 * cellH) / cellW));
    const cols = Math.max(1, Math.min(n, isolatedPerRow > 0 ? isolatedPerRow
      : Math.max(landscape, Math.min(landscape * 2, Math.floor((b.maxX - b.minX + spacing) / cellW)))));
    const rowsN = Math.ceil(n / cols);
    const cx = pos.size ? (b.minX + b.maxX) / 2 : 0;
    const top = pos.size ? b.maxY + reach : -(rowsN * cellH - spacing) / 2;
    isolated.forEach((id, i) => {
      const row = Math.floor(i / cols), inRow = Math.min(cols, n - row * cols);
      pos.set(id, { x: cx + ((i % cols) - (inRow - 1) / 2) * cellW, y: top + node.h / 2 + row * cellH, level: 0, ring: 0 });
    });
  }
  return { pos, rings, bounds: extent(), levels: Math.max(pos.size ? 1 : 0, deepest + 1), linked, isolated: onlyLinked ? [] : isolated, radial: true };
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
 * The path of a line in the clusters layout: from the edge of the parent's
 * box to the edge of the child's, along the line between their centres. The
 * line stops at the boxes rather than running under them so a dash pattern
 * begins where the line does, and a faded box does not show it through.
 * `curved` bows it a little, always the same way round, which keeps a ring of
 * lines from looking ragged.
 */
function clusterEdge(a, b, w, h, round, curved) {
  const dx = b.x - a.x, dy = b.y - a.y;
  const len = Math.hypot(dx, dy);
  if (!len) return `M${a.x},${a.y}`;
  const ux = dx / len, uy = dy / len;
  // How far from a box's centre its edge is, along the line.
  const t = round ? w / 2 : Math.min(ux ? w / 2 / Math.abs(ux) : Infinity, uy ? h / 2 / Math.abs(uy) : Infinity);
  const f = (n) => Math.round(n * 10) / 10;
  if (t * 2 >= len) return `M${f(a.x)},${f(a.y)} L${f(b.x)},${f(b.y)}`;
  const s = { x: a.x + ux * t, y: a.y + uy * t }, e = { x: b.x - ux * t, y: b.y - uy * t };
  if (!curved) return `M${f(s.x)},${f(s.y)} L${f(e.x)},${f(e.y)}`;
  const bow = (len - 2 * t) * 0.12;
  return `M${f(s.x)},${f(s.y)} Q${f((s.x + e.x) / 2 - uy * bow)},${f((s.y + e.y) / 2 + ux * bow)} ${f(e.x)},${f(e.y)}`;
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
    const entryOf = new Map(shown.map((e) => [String(e.node.id), e]));
    const S = SIZES[c.size];
    const bare = c.labels === 'none';
    const boxH = bare ? BARE[c.size] : c.labels === 'name-host' ? S.hHost : S.hName;
    const boxW = bare ? BARE[c.size] : S.w;
    const chars = Math.floor((boxW - 26) / (S.font * 0.56));
    const layout = layoutMap(forest, { orientation: c.orientation, onlyLinked: c.onlyLinked, node: { w: boxW, h: boxH }, spacing: S.gapX, reach: Math.round(S.gapY * 0.7) });

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
      // The clusters layout is already in pixels.
      for (const [id, p] of layout.pos) centre.set(id, { x: p.x, y: p.y });
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
    let cutCount = 0;
    for (const [id, p] of forest.parentOf) {
      const a = centre.get(p), b = centre.get(id);
      if (!a || !b) continue;
      const parentStatus = entryOf.get(p)?.status || 'unknown';
      const cut = c.colorEdges && (parentStatus === 'down');
      if (cut) cutCount++;
      let d;
      if (layout.radial) {
        d = clusterEdge(a, b, boxW, boxH, bare, c.edges === 'curved');
      } else if (c.edges === 'straight') {
        d = `M${a.x},${a.y} L${b.x},${b.y}`;
      } else if (c.orientation === 'right') {
        const x1 = a.x + boxW / 2, x2 = b.x - boxW / 2, mx = (x1 + x2) / 2;
        d = c.edges === 'elbow' ? `M${x1},${a.y} H${mx} V${b.y} H${x2}` : `M${x1},${a.y} C${mx},${a.y} ${mx},${b.y} ${x2},${b.y}`;
      } else {
        const y1 = a.y + boxH / 2, y2 = b.y - boxH / 2, my = (y1 + y2) / 2;
        d = c.edges === 'elbow' ? `M${a.x},${y1} V${my} H${b.x} V${y2}` : `M${a.x},${y1} C${a.x},${my} ${b.x},${my} ${b.x},${y2}`;
      }
      const ctx = entryOf.get(id)?.context || entryOf.get(p)?.context;
      // The look of the line: its own style (kept on the node at its lower
      // end), else the map's, else the theme's — with an outage always red.
      const look = edgeLook(c, entryOf.get(id)?.node.linkStyle, cut);
      edgesG.append(svg('path', { d, class: `netmap-edge${cut ? ' netmap-edge-down' : ''}${ctx ? ' netmap-context' : ''}`, style: edgeStyle(look), 'data-from': p, 'data-to': id }));
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
      legend.append(h('span', { class: 'netmap-key muted' }, c.orientation === 'right' ? 'Each node sits to the right of the node it depends on' : layout.radial ? 'Each node sits in a ring round the node it depends on' : 'Each node sits under the node it depends on'));
      // Lines can be any colour and dash by now, so what a red one means is
      // said in words — colour and dashes alone must not be the whole cue.
      if (cutCount) legend.append(h('span', { class: 'netmap-key' }, h('span', { class: 'netmap-linekey', 'aria-hidden': 'true' }), `Red lines lead out of a node that is down (${plural(cutCount, 'link')})`));
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
 * Colour, thickness and style controls for a line — the map's own, a single
 * link's, the node editor's. `.el` is the controls, `.value` what they hold
 * as { color, width, dash } and `onChange(value)` fires when a person changes
 * one. A colour of '' is the theme's.
 *
 * With `inherit` (a link's own style) an empty colour, thickness or style
 * means "follow the map", and `defaults` ({ color, width, dash }) says what
 * the map has, so the empty thickness can show it. Without, thickness and
 * style always have a value.
 */
export function lineStyleControls({ value = {}, inherit = false, defaults = {}, disabled = false, label = 'Line', onChange } = {}) {
  let color = normalizeLineColor(value.color);
  let width = normalizeLineWidth(value.width, inherit ? 0 : LINE_WIDTH.default);
  let dash = normalizeLineDash(value.dash) || (inherit ? '' : 'solid');
  const unset = inherit ? 'Same as the map' : 'Theme colour';
  // A colour input holds #rrggbb only; a stored #rgb or #rrggbbaa is shown
  // as that and left exactly as it was unless the person picks another.
  const hex6 = (c) => (c.length === 4 ? `#${[...c.slice(1)].map((x) => x + x).join('')}` : c.slice(0, 7));
  const emit = () => onChange && onChange(api.value);

  const picker = h('input', { type: 'color', value: hex6(color || normalizeLineColor(defaults.color) || '#8b949e'), 'aria-label': `${label} colour`, disabled });
  const shown = h('span', { class: 'netmap-colour-text mono muted' }, color || unset);
  const useDefault = h('button', { type: 'button', class: 'btn btn-sm', disabled: disabled || !color, 'aria-label': `Use the ${inherit ? 'map’s' : 'theme'} colour for the ${label.toLowerCase()}`, onclick: () => { color = ''; paint(); emit(); } }, 'Default');
  const widthIn = numberInput({ min: LINE_WIDTH.min, max: LINE_WIDTH.max, step: LINE_WIDTH.step, value: width || '', placeholder: inherit ? String(defaults.width || LINE_WIDTH.default) : '', 'aria-label': `${label} thickness in pixels`, disabled });
  const styleOpts = MAP_LINE_STYLES.map((o) => (inherit && o.value === defaults.dash ? { ...o, label: `${o.label} (map)` } : o));
  const dashSel = selectInput({ options: inherit ? [{ value: '', label: 'Like the map' }, ...styleOpts] : MAP_LINE_STYLES, value: dash, 'aria-label': `${label} style`, disabled });

  function paint() {
    shown.textContent = color || unset;
    if (color) picker.value = hex6(color);
    useDefault.disabled = disabled || !color;
    widthIn.value = width || '';
    dashSel.value = dash;
  }
  picker.addEventListener('change', () => { color = normalizeLineColor(picker.value); paint(); emit(); });
  widthIn.addEventListener('change', () => { width = normalizeLineWidth(widthIn.value, inherit ? 0 : LINE_WIDTH.default); paint(); emit(); });
  dashSel.addEventListener('change', () => { dash = normalizeLineDash(dashSel.value) || (inherit ? '' : 'solid'); emit(); });

  const api = {
    el: h('div', { class: 'netmap-linestyle' },
      field({ label: 'Colour', input: h('div', { class: 'netmap-colour' }, picker, shown, useDefault) }),
      field({ label: 'Thickness', input: h('div', { class: 'input-with-unit' }, widthIn, h('span', { class: 'unit' }, 'px')) }),
      field({ label: 'Style', input: dashSel })),
    get value() { return { color, width, dash }; },
    /** Back to the defaults, as a person's own change. */
    reset() { color = ''; width = inherit ? 0 : LINE_WIDTH.default; dash = inherit ? '' : 'solid'; paint(); emit(); },
  };
  return api;
}

/**
 * The style of one link, as a group of controls: the line from a node to the
 * node it depends on, `parentName`. `value` is the link's style (or null),
 * `defaults` what the map draws a line as, and `onChange(style)` gets the
 * style as it should be kept — null when it sets nothing. The Reset button
 * clears the lot, so the link follows the map again.
 */
export function linkStyleEditor({ parentName = '', value = null, defaults = {}, disabled = false, onChange } = {}) {
  const controls = lineStyleControls({ value: value || {}, inherit: true, defaults, disabled, label: 'Link', onChange: (v) => onChange && onChange(normalizeLinkStyle(v)) });
  const legend = h('legend', null, parentName ? `Link to ${parentName}` : 'Link style');
  const reset = h('button', { type: 'button', class: 'btn btn-sm admin-only', 'aria-label': 'Reset the style of this link', onclick: () => controls.reset() }, icon('refresh'), 'Reset');
  const wrap = h('fieldset', { class: 'netmap-linegroup' }, legend, controls.el, h('div', { class: 'row-between' }, h('span', { class: 'help' }, 'Applies on every map. An outage still shows in red.'), reset));
  wrap.setParentName = (name) => { legend.textContent = name ? `Link to ${name}` : 'Link style'; };
  Object.defineProperty(wrap, 'value', { get: () => normalizeLinkStyle(controls.value) });
  return wrap;
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
  // How every line looks unless a link has a style of its own.
  const lines = lineStyleControls({ value: { color: c.lineColor, width: c.lineWidth, dash: c.lineStyle }, label: 'Map line', onChange: emit });
  const resetLines = h('button', { type: 'button', class: 'btn btn-sm', onclick: () => lines.reset() }, icon('refresh'), 'Reset the lines');
  const wrap = h('div', { class: 'stack-sm netmap-options' },
    h('div', { class: 'form-grid' },
      field({ label: 'Layout', input: orientation, help: 'Clusters put each node in the middle of a ring of the nodes that depend on it.' }),
      field({ label: 'Labels', input: labels }),
      field({ label: 'Node size', input: size }),
      field({ label: 'Line shape', input: edges, help: 'Clusters draw straight or gently curved lines.' }),
      field({ label: 'Group', input: group, help: 'The nodes a filtered node depends on stay on the map, faded.' }),
      field({ label: 'Tag', input: tag })),
    h('fieldset', { class: 'netmap-linegroup' },
      h('legend', null, 'Line style'),
      lines.el,
      h('div', { class: 'row-between' }, h('span', { class: 'help' }, 'A link can have its own colour, thickness and style: choose a node on the map.'), resetLines)),
    h('div', { class: 'stack-sm', style: { gap: '6px' } }, onlyLinked, colorEdges, showLegend));
  Object.defineProperty(wrap, 'value', { get: () => normalizeMapConfig({
    orientation: orientation.value, labels: labels.value, size: size.value, edges: edges.value, group: group.value, tag: tag.value,
    onlyLinked: onlyLinked.input.checked, colorEdges: colorEdges.input.checked, showLegend: showLegend.input.checked,
    lineColor: lines.value.color, lineWidth: lines.value.width, lineStyle: lines.value.dash,
  }) });
  return wrap;
}

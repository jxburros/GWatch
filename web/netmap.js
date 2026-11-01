// @ts-check
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
// (stored as "radial", which older saved maps already say) puts the things
// plugged into a node in a ring round it, and each of those with things of
// its own in the middle of its own ring, further out on a longer line.
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
  { value: 'straight', label: 'Straight' },
  { value: 'elbow', label: 'Right-angled' },
  { value: 'curved', label: 'Curved' },
];
/** What fills a node's box: its own colour with a status light beside it, one or the other. */
export const MAP_NODE_FILLS = [
  { value: 'both', label: 'Colour and status' },
  { value: 'colour', label: 'Chosen colour only' },
  { value: 'status', label: 'Status only' },
];
/** What colours a line: the colours chosen for it (red where an outage reaches), only those, or status. */
export const MAP_LINE_COLOURS = [
  { value: 'outage', label: 'Chosen colours, red where an outage reaches' },
  { value: 'chosen', label: 'Chosen colours only' },
  { value: 'status', label: 'Status of the node each line leads to' },
];
export const MAP_LINE_STYLES = [
  { value: 'solid', label: 'Solid' },
  { value: 'dashed', label: 'Dashed' },
  { value: 'dotted', label: 'Dotted' },
  { value: 'dash-dot', label: 'Dash and dot' },
];
/**
 * The thickness a line may take. It is screen pixels at the least: however far
 * out a big map is zoomed, a line is never drawn thinner than this; zoomed in,
 * it grows with the boxes, up to three times as thick.
 */
export const LINE_WIDTH = { min: 1, max: 8, step: 0.5, default: 2 };
/** How much thicker than its own width a line may grow when zoomed in. */
export const LINE_ZOOM_MAX = 3;

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

/* ---------- A node's own colour ---------- */

/** A node's own colour (#rrggbb and the other hex forms, lower case), or ''. */
export function nodeColour(node) {
  return normalizeLineColor(node?.color);
}

/** The colour as #rrggbb, dropping any alpha and widening #rgb. */
function hex6(c) {
  const s = normalizeLineColor(c);
  if (!s) return '';
  return s.length === 4 ? `#${[...s.slice(1)].map((x) => x + x).join('')}` : s.slice(0, 7);
}

/** WCAG relative luminance of a hex colour. */
function luminance(c) {
  const v = hex6(c);
  const ch = [1, 3, 5].map((i) => parseInt(v.slice(i, i + 2), 16) / 255).map((x) => (x <= 0.03928 ? x / 12.92 : ((x + 0.055) / 1.055) ** 2.4));
  return 0.2126 * ch[0] + 0.7152 * ch[1] + 0.0722 * ch[2];
}

/**
 * The text colour that reads on a background of colour `bg`: near-black or
 * white, whichever has the higher contrast. A node's colour is anyone's
 * choice, so its label must never be left to the theme's text colour.
 */
export function textOn(bg) {
  if (!hex6(bg)) return '';
  const l = luminance(bg);
  return (l + 0.05) / 0.05 >= 1.05 / (l + 0.05) ? '#111418' : '#ffffff';
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
 * theme's (a colour of '' leaves the stylesheet to choose). `state` says what
 * an outage or the status is doing to the line: true or 'cut' (out of a node
 * that is down), 'affected' (further below one), 'quiet' (below a node in
 * maintenance), or a status colour ('st-up', 'st-down', …). Any of them is
 * drawn in its own colour whatever colour was chosen — an outage must never be
 * hidden by a pretty map — but still in the thickness and dashes it was
 * given. `cfg` is a normalised map configuration.
 *
 * @param {any} cfg
 * @param {any} linkStyle
 * @param {boolean | string} [state]
 */
export function edgeLook(cfg, linkStyle, state = false) {
  const own = normalizeLinkStyle(linkStyle) || { color: '', width: 0, dash: '' };
  const st = state === true ? 'cut' : (state || '');
  return {
    color: st ? '' : own.color || cfg.lineColor,
    width: own.width || cfg.lineWidth,
    dash: own.dash || (cfg.lineStyle !== 'solid' ? cfg.lineStyle : ''),
    cut: st === 'cut',
    state: st,
  };
}

/**
 * The inline style for an edgeLook. Inline so it outranks the stylesheet and
 * travels with an exported SVG. `scale` is drawing units per screen pixel at
 * the zoom shown (see strokeScale), so the thickness chosen is screen pixels.
 */
export function edgeStyle(look, scale = 1) {
  const r = (n) => Math.round(n * 1000) / 1000;
  const parts = [`stroke-width:${r(look.width * scale)}`];
  if (look.color) parts.push(`stroke:${look.color}`);
  if (look.dash) {
    const d = lineDash(look.dash, look.width);
    parts.push(`stroke-dasharray:${d.array === 'none' ? 'none' : d.array.split(' ').map((x) => r(Number(x) * scale)).join(' ')}`, `stroke-linecap:${d.cap}`);
  }
  return parts.join(';');
}

/**
 * Drawing units per screen pixel for a line at a zoom of `pxPerUnit` screen
 * pixels per drawing unit. A line is its thickness in screen pixels when the
 * map is shown smaller than life (a big map fitted to the screen), and grows
 * with the drawing when it is zoomed in, up to LINE_ZOOM_MAX times.
 */
export function strokeScale(pxPerUnit) {
  const s = Number(pxPerUnit);
  if (!Number.isFinite(s) || s <= 0) return 1;
  return Math.min(LINE_ZOOM_MAX, Math.max(1, s)) / s;
}

/** A complete map configuration with its defaults filled in. */
/** @param {any} [cfg] */
export function normalizeMapConfig(cfg = {}) {
  const c = cfg && typeof cfg === 'object' ? cfg : {};
  const pick = (list, v, fb) => (list.some((o) => o.value === v) ? v : fb);
  // Lines out of a parent that is down are drawn in the down colour, so the
  // reach of an outage can be followed by eye — unless whoever set the map up
  // chose otherwise. Older maps said so with colorEdges.
  const lineColour = pick(MAP_LINE_COLOURS, c.lineColour, c.colorEdges === false ? 'chosen' : 'outage');
  return {
    // Clusters, the layout most asked for, unless another was chosen.
    orientation: pick(MAP_ORIENTATIONS, c.orientation, 'radial'),
    labels: pick(MAP_LABELS, c.labels, 'name'),
    size: pick(MAP_SIZES, c.size, 'medium'),
    edges: pick(MAP_EDGES, c.edges, 'straight'),
    group: typeof c.group === 'string' ? c.group : '',
    tag: typeof c.tag === 'string' ? c.tag : '',
    onlyLinked: !!c.onlyLinked,
    nodeFill: pick(MAP_NODE_FILLS, c.nodeFill, 'both'),
    lineColour,
    colorEdges: lineColour !== 'chosen',
    // Choosing a node lights up what it depends on and what depends on it.
    highlightPath: c.highlightPath !== false,
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
 * to keep boxes and lines clear of one another, so it works in pixels: pass
 * `node: { w, h }` (the box a node is drawn in), `spacing` (the least space
 * between two neighbouring boxes) and `reach` (the shortest a line between
 * two boxes may be). It returns
 * { pos: Map id → { x, y, level, ring }, rings: [], bounds, levels, linked,
 *   isolated, radial: true }
 * with `x`, `y` the centre of the node's box (the biggest tree's root is at
 * 0,0), `ring` always 0 and `rings` always empty (kept for callers that read
 * them; nothing is drawn on a ring any more), and `bounds` the box that holds
 * every node's box.
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
 * The clusters layout: every node is the middle of a ring of the things that
 * depend on it, and every one of those that has dependents of its own is the
 * middle of a ring of its own, further out — a firewall with its switches
 * round it, each switch with its machines round it, the way someone would
 * sketch a network on a whiteboard.
 *
 * It is worked out in "cell space", where a node's box with the spacing
 * round it is one cell, 1 across and 1/CLUSTER_STRETCH tall. Name boxes are
 * several times wider than they are tall; in cell space they are nearly
 * square, so a ring of them is tight round its node however the ring turns,
 * and the picture is only stretched back to boxes at the end (which makes the
 * rings ovals, wider than tall, the shape a row of name labels wants).
 * Stretching never brings two cells closer or a line nearer a box, so what
 * holds in cell space holds on the page.
 *
 * In cell space every node is a circle of radius R0 round its box, and every
 * cluster a circle that holds all of it. A cluster is laid out once, from the
 * leaves up, in a frame of its own (the way out from the parent along +x),
 * and only turned and moved into place afterwards, so the whole layout is a
 * single pass — no nudging, and the same nodes always give the same picture:
 *
 *  1. The children with no dependents of their own (leaves) go on a ring
 *     round the node, or on two or three rings, alternately, when there are
 *     many of them; a line to an outer leaf passes between inner ones.
 *  2. A child with dependents (a branch) goes further out, far enough that
 *     its whole cluster's circle clears the rings, in a slice of the turn as
 *     wide as that circle needs. Its line in runs through a gap in the rings.
 *  3. The ring is the smallest at which the leaves' places and the branches'
 *     slices fit round the node: the whole turn round a root (the first child
 *     at the top, a lone one to the right), and round anything else the turn
 *     less a gap facing its parent, so the line in stays clear. A node with
 *     only a few children keeps them on its far side.
 *
 * Several trees are laid out one by one; the biggest keeps its root at 0,0
 * and the others are set down round it in the nearest free space. Nodes with
 * no link at all are a block of rows under everything, as in the tree
 * layouts.
 */
const CLUSTER_STRETCH = 1.6;
function layoutClusters(forest, trees, isolated, linked, { onlyLinked, isolatedPerRow, node, spacing, reach: wantReach }) {
  const reach = Math.max(wantReach, spacing / 2);
  const cw = node.w + spacing, ch = (node.h + spacing) * CLUSTER_STRETCH;
  // A cell's circle, and a box's (the least room a line passing by needs).
  const R0 = Math.hypot(0.5, 0.5 / CLUSTER_STRETCH);
  const RL = Math.hypot(node.w / 2 / cw, node.h / 2 / ch) + 0.02;
  const GAP = 0.12; // between a ring and the clusters beyond it
  // Between two neighbouring clusters: clear space, so each reads as a group
  // of its own and nothing looks nearer another cluster's node than its own.
  const SIBLING_GAP = 1.1;
  // The least distance, in cells, from a node to a child in any direction,
  // so the line between their boxes is at least `reach` long on the page.
  let rMin = 2 * R0;
  // Sampled round the turn, and exactly at the box's corners, where the
  // edge's distance from the centre is longest.
  const corner = Math.atan2(node.h * cw, node.w * ch);
  for (const t of [...Array.from({ length: 180 }, (_, i) => (i / 180) * Math.PI), corner, Math.PI - corner]) {
    const dx = Math.cos(t) * cw, dy = Math.sin(t) * ch, len = Math.hypot(dx, dy);
    const edge = Math.min(node.w / 2 / Math.max(Math.abs(dx / len), 1e-9), node.h / 2 / Math.max(Math.abs(dy / len), 1e-9));
    rMin = Math.max(rMin, (reach + 2 * edge) / len + 1e-6);
  }
  const TURN = 2 * Math.PI;
  const BACK_GAP = 0.4 * Math.PI;
  const asin = (x) => Math.asin(Math.min(1, x));

  // One node's cluster in its own frame: { kids: [{ id, angle, dist, branch }],
  // c, R } — the children's places, and the circle that holds it all, centred
  // on (c, 0).
  const shapes = new Map();
  const shape = (id, isRoot) => {
    const key = `${id}:${isRoot ? 1 : 0}`;
    if (shapes.has(key)) return shapes.get(key);
    const kids = forest.children.get(id) || [];
    let out;
    if (!kids.length) out = { kids: [], c: 0, R: R0 };
    else {
      const sub = new Map();
      for (const k of kids) if ((forest.children.get(k) || []).length) sub.set(k, shape(k, false));
      const leaves = kids.filter((k) => !sub.has(k));
      const branches = kids.filter((k) => sub.has(k));
      // Round the ring: the leaves by name, the branches dealt in among them evenly.
      const order = [];
      {
        const n = kids.length;
        const at = new Set(branches.map((_, i) => Math.min(n - 1, Math.floor(((i + 0.5) * n) / branches.length))));
        let li = 0, bi = 0;
        for (let i = 0; i < n; i++) order.push(at.has(i) && bi < branches.length ? branches[bi++] : (li < leaves.length ? leaves[li++] : branches[bi++]));
      }
      const m = leaves.length;
      const arc = isRoot ? TURN : TURN - BACK_GAP;
      // Everything that depends on the ring radius r (and the number of rings).
      const plan = (r, rings) => {
        const outer = m ? r + (rings - 1) * 2 * R0 : 0;
        const phi = rings === 1 ? 2 * asin(R0 / r) : Math.max(asin(RL / r), (2 / rings) * asin(R0 / r));
        const width = new Map();
        const dist = new Map();
        let total = m * phi + (isRoot && rings > 1 && m > 1 ? 2 * asin(R0 / r) : 0);
        for (const b of branches) {
          const s = sub.get(b);
          // Far enough that its circle clears the rings (and this node's box).
          const d = Math.max(r, (m ? outer + R0 : R0) + GAP + s.R - s.c, rMin);
          const w = Math.max(2 * asin((s.R + SIBLING_GAP / 2) / (d + s.c)), m ? 2 * asin(RL / r) + phi / 2 : 0);
          dist.set(b, d); width.set(b, w); total += w;
        }
        return { r, rings, outer, phi, width, dist, total };
      };
      // The smallest ring that fits, for one, two or three rings of leaves;
      // whichever reaches out least is used.
      let best = null;
      for (let rings = 1; rings <= Math.min(3, Math.max(1, m)); rings++) {
        let lo = rMin, hi = rMin;
        while (plan(hi, rings).total > arc) { lo = hi; hi *= 1.5; }
        if (hi > lo) for (let i = 0; i < 30; i++) { const mid = (lo + hi) / 2; if (plan(mid, rings).total > arc) lo = mid; else hi = mid; }
        const p = plan(hi, rings);
        let reachOut = m ? p.outer + R0 : 0;
        for (const b of branches) reachOut = Math.max(reachOut, p.dist.get(b) + sub.get(b).c + sub.get(b).R);
        if (!best || reachOut < best.reachOut - 1e-9) best = { ...p, reachOut };
      }
      // Spread them: round a root over the whole turn; round anything else
      // over what they need, or a little more when there are only a few.
      const used = isRoot ? arc : Math.min(arc, Math.max(best.total, kids.length * Math.PI / 4));
      const scale = best.total > 0 ? used / best.total : 1;
      const widthOf = (k) => (sub.has(k) ? best.width.get(k) : best.phi) * scale;
      let cursor;
      if (isRoot) cursor = kids.length === 1 ? -widthOf(order[0]) / 2 : -Math.PI / 2 - widthOf(order[0]) / 2;
      else cursor = -used / 2;
      const placed = [];
      let li = 0;
      for (const k of order) {
        const w = widthOf(k);
        const angle = cursor + w / 2;
        cursor += w;
        if (sub.has(k)) placed.push({ id: k, angle, dist: best.dist.get(k), branch: true });
        else placed.push({ id: k, angle, dist: best.r + (li++ % best.rings) * 2 * R0, branch: false });
      }
      // The circle that holds it all, centred on the axis: the centre that
      // needs the smallest radius (the farthest reach is convex in it).
      const disks = [{ x: 0, y: 0, r: R0 }, ...placed.map((p) => {
        const s = sub.get(p.id);
        const d = p.dist + (s ? s.c : 0);
        return { x: d * Math.cos(p.angle), y: d * Math.sin(p.angle), r: s ? s.R : R0 };
      })];
      const need = (c) => disks.reduce((a, q) => Math.max(a, Math.hypot(q.x - c, q.y) + q.r), 0);
      let lo = -best.reachOut, hi = best.reachOut;
      for (let i = 0; i < 60; i++) { const a = lo + (hi - lo) / 3, b = hi - (hi - lo) / 3; if (need(a) <= need(b)) hi = b; else lo = a; }
      const c = isRoot ? 0 : (lo + hi) / 2;
      out = { kids: placed, c, R: need(c) };
    }
    shapes.set(key, out);
    return out;
  };

  const pos = new Map();
  let deepest = trees.length ? 0 : -1;
  const layoutTree = (root) => {
    const at = new Map();
    const put = (id, isRoot, x, y, rot, lvl) => {
      at.set(id, { x: x * cw, y: y * ch, level: lvl });
      deepest = Math.max(deepest, lvl);
      for (const k of shape(id, isRoot).kids) {
        const a = rot + k.angle;
        put(k.id, false, x + k.dist * Math.cos(a), y + k.dist * Math.sin(a), a, lvl + 1);
      }
    };
    put(root, true, 0, 0, 0, 0);
    return at;
  };

  // Lay out every tree, the biggest first, and set each later one down in the
  // nearest free space round what is already there.
  const placedBoxes = [];
  const clear = (b) => placedBoxes.every((o) => b.maxX + spacing * 2 <= o.minX || o.maxX + spacing * 2 <= b.minX || b.maxY + spacing * 2 <= o.minY || o.maxY + spacing * 2 <= b.minY);
  for (const [ti, root] of trees.entries()) {
    const at = layoutTree(root);
    let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
    for (const p of at.values()) {
      minX = Math.min(minX, p.x - node.w / 2); maxX = Math.max(maxX, p.x + node.w / 2);
      minY = Math.min(minY, p.y - node.h / 2); maxY = Math.max(maxY, p.y + node.h / 2);
    }
    let ox = 0, oy = 0;
    if (ti > 0) {
      // A spiral out from the middle of the first tree; the first spot where
      // this tree's box is clear of every other is where it goes.
      const b0 = placedBoxes[0];
      const cx = (b0.minX + b0.maxX) / 2, cy = (b0.minY + b0.maxY) / 2;
      const w = maxX - minX, hgt = maxY - minY;
      const step = Math.max(R0 * cw, 20);
      for (let i = 0; i < 20000; i++) {
        const ang = i * 0.5, rad = step * Math.sqrt(i) * 1.2;
        const x = cx + rad * Math.cos(ang) - (minX + w / 2), y = cy + rad * Math.sin(ang) - (minY + hgt / 2);
        if (clear({ minX: minX + x, maxX: maxX + x, minY: minY + y, maxY: maxY + y })) { ox = x; oy = y; break; }
      }
    }
    placedBoxes.push({ minX: minX + ox, maxX: maxX + ox, minY: minY + oy, maxY: maxY + oy });
    for (const [id, p] of at) pos.set(id, { x: p.x + ox, y: p.y + oy, level: p.level, ring: 0 });
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
  return { pos, rings: [], bounds: extent(), levels: Math.max(pos.size ? 1 : 0, deepest + 1), linked, isolated: onlyLinked ? [] : isolated, radial: true, cell: { w: cw, h: ch } };
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

/* ---------- Status, outage reach and what-if ---------- */

const STATUSES = { up: 1, down: 1, degraded: 1, paused: 1, maintenance: 1, unknown: 1 };
const SEVERITY = { down: 5, degraded: 4, unknown: 3, maintenance: 2, paused: 1, up: 0 };

/** Everything below `id` in a forest, nearest first. */
export function descendantsOf(forest, id) {
  const out = [];
  const queue = [...(forest.children.get(String(id)) || [])];
  for (let i = 0; i < queue.length; i++) { out.push(queue[i]); queue.push(...(forest.children.get(queue[i]) || [])); }
  return out;
}

/** The chain above `id`, nearest first. */
export function ancestorsOf(forest, id) {
  const out = [];
  for (let p = forest.parentOf.get(String(id)); p != null && !out.includes(p); p = forest.parentOf.get(p)) out.push(p);
  return out;
}

/**
 * The nodes whose loss would take the most with them: every node with
 * anything below it, by how much (most first, then by name), with what is
 * below it and how much of that is up now.
 */
export function biggestRisks(forest, entryOf, limit = 8) {
  const out = [];
  for (const id of forest.byId.keys()) {
    const below = descendantsOf(forest, id);
    if (!below.length) continue;
    out.push({ id, node: forest.byId.get(id), below: below.length, direct: (forest.children.get(id) || []).length });
  }
  out.sort((a, b) => b.below - a.below || compareNames(a.node?.name, b.node?.name));
  return out.slice(0, limit);
}

/**
 * What each node on a map is showing. Returns Map id → { status, sim, cause },
 * where `status` is its own (paused when switched off), or what a what-if
 * makes it, and `cause` — when something above it is down or in maintenance —
 * is { kind: 'down' | 'maintenance', by, depth, sim }: the nearest such node
 * and how many links away it is.
 *
 * `sim` is a what-if: { failed: Map id → 'down' | 'maintenance', depth }.
 * The failed nodes take that status; what hangs below them is cut off, but
 * only `depth` links down is shown yet (a simulation spreads one ring at a
 * time). Nothing real is changed: it is only how the map is drawn.
 */
export function mapStates(forest, entryOf, sim = null) {
  const out = new Map();
  const failed = sim?.failed instanceof Map ? sim.failed : new Map(Object.entries(sim?.failed || {}));
  const reveal = sim && Number.isFinite(sim.depth) ? sim.depth : Infinity;
  const visit = (id, inherited) => {
    const e = entryOf.get(id);
    const own = e ? (e.node.enabled === false ? 'paused' : (STATUSES[e.status] ? e.status : 'unknown')) : 'unknown';
    const simmed = failed.get(id);
    const status = simmed === 'down' || simmed === 'maintenance' ? simmed : own;
    // A real outage reaches only what the service says it reaches: a node
    // failing while something it depends on is down is "affected by" it
    // (its alerts are held back); one whose own checks pass is not cut off,
    // and neither is anything it carries. Maintenance above a node that is
    // failing too makes it quiet. A what-if reaches everything below, but
    // shows only as far as it has spread so far.
    let cause = null;
    if (inherited?.sim) cause = inherited.depth <= reveal ? inherited : null;
    else if (inherited?.kind === 'down') cause = e?.affectedBy ? inherited : null;
    else if (inherited && own !== 'up' && own !== 'paused') cause = inherited;
    out.set(id, { status, own, sim: !!simmed, cause });
    const passOn = inherited?.sim ? inherited : cause;
    const mine = status === 'down' || status === 'maintenance'
      ? { kind: status, by: id, depth: 1, sim: !!simmed }
      : passOn ? { ...passOn, depth: passOn.depth + 1 } : null;
    for (const k of forest.children.get(id) || []) visit(k, mine);
  };
  for (const r of forest.roots) visit(r, null);
  return out;
}

/**
 * What an outage does to the line from `parent` into `id`: 'cut' out of a node
 * that is down, 'quiet' out of one in maintenance, 'affected' or 'quiet'
 * further into what they cut off, or '' when nothing is. A simulated failure
 * shows on its lines once the simulation has spread to them.
 */
export function edgeState(states, parent, id, sim = null) {
  const p = states.get(String(parent));
  const reveal = sim && Number.isFinite(sim.depth) ? sim.depth : Infinity;
  if (p && (p.status === 'down' || p.status === 'maintenance') && (!p.sim || reveal >= 1)) return p.status === 'down' ? 'cut' : 'quiet';
  const c = states.get(String(id))?.cause;
  if (!c || c.depth < 2) return '';
  return c.kind === 'maintenance' ? 'quiet' : 'affected';
}

/* ---------- Arranged positions ---------- */

/**
 * Where every node is drawn in the clusters layout once it has been arranged
 * by hand. `auto` is the layout's own places (page units), `cell` its cell
 * size, and `arranged` the stored arrangement: positions in cells, each
 * pinned (put there by someone) or not (kept where a tidy-up left it).
 *
 * A node with a stored place is drawn there. One without follows the node it
 * depends on, at the place the layout gave it next to that node — so moving a
 * switch carries its devices with it — and the whole group is turned to face
 * the way the switch now hangs from its own parent, so its ring still opens
 * away from the line in. Returns Map id → { x, y, pinned, stored }.
 */
export function arrangePositions(forest, auto, cell, arranged) {
  const stored = arranged?.positions || {};
  const out = new Map();
  const turn = new Map();
  const toCells = (p) => ({ x: p.x / cell.w, y: p.y / cell.h });
  const angle = (a, b) => Math.atan2(b.y - a.y, b.x - a.x);
  const place = (id) => {
    const p = forest.parentOf.get(id);
    const s = stored[id];
    const base = auto.get(id) || { x: 0, y: 0 };
    if (s && Number.isFinite(s.x) && Number.isFinite(s.y)) {
      out.set(id, { x: s.x * cell.w, y: s.y * cell.h, pinned: !!s.pinned, stored: true });
      // How far round this node now hangs from its parent, compared with
      // where the layout hung it: its own group turns by as much.
      if (p != null && out.has(p) && auto.has(p)) {
        const was = angle(toCells(auto.get(p)), toCells(base));
        const now = angle(toCells(out.get(p)), toCells(out.get(id)));
        turn.set(id, now - was);
      } else turn.set(id, 0);
    } else if (p != null && out.has(p) && auto.has(p)) {
      const t = turn.get(p) || 0;
      const pa = toCells(auto.get(p)), me = toCells(base), at = toCells(out.get(p));
      const dx = me.x - pa.x, dy = me.y - pa.y;
      const x = at.x + dx * Math.cos(t) - dy * Math.sin(t), y = at.y + dx * Math.sin(t) + dy * Math.cos(t);
      out.set(id, { x: x * cell.w, y: y * cell.h, pinned: false, stored: false });
      turn.set(id, t);
    } else {
      out.set(id, { x: base.x, y: base.y, pinned: false, stored: false });
      turn.set(id, 0);
    }
    for (const k of forest.children.get(id) || []) place(k);
  };
  for (const r of forest.roots) if (auto.has(r)) place(r);
  // Anything the layout placed that is not under a root it placed (the
  // unlinked block) keeps its place, or its stored one.
  for (const [id, p] of auto) {
    if (out.has(id)) continue;
    const s = stored[id];
    out.set(id, s ? { x: s.x * cell.w, y: s.y * cell.h, pinned: !!s.pinned, stored: true } : { x: p.x, y: p.y, pinned: false, stored: false });
  }
  return out;
}

/** The nodes that move with `id` when it is dragged: those below it with no place of their own, down to the next that has one. */
export function carriedBy(forest, id, arranged) {
  const stored = arranged?.positions || {};
  const out = [];
  const walk = (x) => { for (const k of forest.children.get(x) || []) { if (stored[k]?.pinned) continue; out.push(k); walk(k); } };
  walk(String(id));
  return out;
}

/**
 * "Tidy up unpinned": the nodes nobody placed go back to where the layout puts
 * them next to their parents, and each group that then sits on top of
 * something is turned round the node it hangs from until it is clear (or as
 * clear as it can be); pinned nodes do not move. Returns the new arrangement:
 * the pins, and the places of the groups that had to turn.
 */
export function tidyUnpinned(forest, auto, cell, arranged, { node, spacing }) {
  const pins = {};
  for (const [id, p] of Object.entries(arranged?.positions || {})) if (p.pinned) pins[id] = { ...p };
  const base = { ...(arranged || {}), positions: pins };
  const at = arrangePositions(forest, auto, cell, base);
  const kept = {};
  const clash = (a, b) => Math.abs(a.x - b.x) < node.w + spacing && Math.abs(a.y - b.y) < node.h + spacing;
  // Pinned nodes from the top down: each one's group, turned as little as clears it.
  const order = [];
  const walk = (id) => { order.push(id); for (const k of forest.children.get(id) || []) walk(k); };
  for (const r of forest.roots) walk(r);
  const settled = new Set();
  for (const id of order) {
    if (!pins[id]) continue;
    const group = carriedBy(forest, id, base);
    if (!group.length) continue;
    const inGroup = new Set([id, ...group]);
    const others = [...at].filter(([k]) => !inGroup.has(k) && (settled.has(k) || !forest.parentOf.has(k) || pins[k] || ![...inGroup].some((g) => g === forest.parentOf.get(k)))).map(([, p]) => p);
    const anchor = at.get(id);
    const ac = { x: anchor.x / cell.w, y: anchor.y / cell.h };
    const turned = (t) => group.map((k) => {
      const p = at.get(k), dx = p.x / cell.w - ac.x, dy = p.y / cell.h - ac.y;
      return [k, { x: (ac.x + dx * Math.cos(t) - dy * Math.sin(t)) * cell.w, y: (ac.y + dx * Math.sin(t) + dy * Math.cos(t)) * cell.h }];
    });
    let best = null;
    for (let i = 0; i <= 24; i++) {
      const t = (i % 2 ? 1 : -1) * Math.ceil(i / 2) * (Math.PI / 12);
      const placed = turned(t);
      let n = 0;
      for (const [, p] of placed) for (const o of others) if (clash(p, o)) n++;
      if (!best || n < best.n) best = { n, t, placed };
      if (!n) break;
    }
    for (const [k, p] of best.placed) {
      at.set(k, { ...at.get(k), x: p.x, y: p.y });
      settled.add(k);
      if (best.t) kept[k] = { x: Math.round((p.x / cell.w) * 10) / 10, y: Math.round((p.y / cell.h) * 10) / 10 };
    }
    settled.add(id);
  }
  return { ...(arranged || {}), positions: { ...kept, ...pins } };
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
 * The path of a straight line between two boxes: from the edge of one to the
 * edge of the other, along the line between their centres. The line stops at
 * the boxes rather than running under them so a dash pattern begins where the
 * line does, and a faded box does not show it through.
 */
function boxEdge(a, b, w, h, round) {
  const dx = b.x - a.x, dy = b.y - a.y;
  const len = Math.hypot(dx, dy);
  const f = (n) => Math.round(n * 10) / 10;
  if (!len) return `M${f(a.x)},${f(a.y)}`;
  const ux = dx / len, uy = dy / len;
  // How far from a box's centre its edge is, along the line.
  const t = round ? w / 2 : Math.min(ux ? w / 2 / Math.abs(ux) : Infinity, uy ? h / 2 / Math.abs(uy) : Infinity);
  if (t * 2 >= len) return `M${f(a.x)},${f(a.y)} L${f(b.x)},${f(b.y)}`;
  return `M${f(a.x + ux * t)},${f(a.y + uy * t)} L${f(b.x - ux * t)},${f(b.y - uy * t)}`;
}

/**
 * Draws a map into `host`. `entries` are overview node entries
 * ({ node, status, affectedBy }). Options:
 *
 * - `interactive`: zooming and panning (wheel, drag, the +/− buttons and the
 *   keyboard); `onSelect(node | null)` is called when a node is chosen (or the
 *   empty map is clicked); without it a node is a link to its page.
 * - `arranged`: the hand arrangement ({ positions, collapsed }) the clusters
 *   layout is drawn with. `canArrange` lets nodes be dragged and clusters
 *   folded; each change is handed to `onArrange(arranged, { saved })` —
 *   whoever drew the map decides whether to keep it.
 * - `sim`: a what-if (see mapStates), `hideHosts`: no addresses anywhere.
 *
 * Returns { update(entries, cfg?), select(id), focus(id), fit(), setArranged(a),
 * setSim(sim), setHideHosts(b), setCanArrange(b), view(), destroy(), svg }.
 */
export function renderNetworkMap(host, entries, cfg, {
  interactive = false, onSelect = null, selectedId = null, title = 'Network map',
  arranged = null, canArrange = false, onArrange = null, sim = null, hideHosts = false,
} = {}) {
  let c = normalizeMapConfig(cfg);
  let data = entries || [];
  let selected = selectedId != null ? String(selectedId) : null;
  let layoutState = cloneArranged(arranged);
  let simState = sim;
  let noHosts = !!hideHosts;
  let mayArrange = !!canArrange;
  let dragging = null;
  let pendingDraw = false;
  const wrap = h('div', { class: `netmap${interactive ? ' netmap-interactive' : ''}` });
  const stage = h('div', { class: 'netmap-stage' });
  const legend = h('div', { class: 'netmap-legend' });
  const listDetails = h('details', { class: 'netmap-list chart-data' }, h('summary', null, 'View as list'));
  wrap.append(stage, legend, listDetails);
  host.append(wrap);

  // The visible window onto the drawing, in drawing units.
  let box = null; let full = null;
  let root = null;
  // What the last drawing was made from, for dragging and highlighting.
  let drawn = null;

  /** Screen pixels per drawing unit at the window shown. */
  const pxPerUnit = () => {
    const r = root?.getBoundingClientRect?.();
    if (!r || !r.width || !r.height || !box) return 0;
    return Math.min(r.width / box.w, r.height / box.h);
  };
  // Lines are given their thickness in screen pixels (see strokeScale), so
  // they are worked out again whenever the zoom or the stage size changes.
  let lastScale = -1;
  const applyStrokes = (force = false) => {
    if (!root) return;
    const k = strokeScale(pxPerUnit());
    if (!force && Math.abs(k - lastScale) < 1e-6) return;
    lastScale = k;
    root.style.setProperty('--netmap-k', String(k));
    for (const el of root.querySelectorAll('.netmap-edge')) el.setAttribute('style', edgeStyle(el.__look, k));
  };
  const setBox = (b) => { box = b; root?.setAttribute('viewBox', `${b.x} ${b.y} ${b.w} ${b.h}`); applyStrokes(); };

  function draw() {
    if (dragging) { pendingDraw = true; return; }
    // Where the chosen node was, so a redraw that moves things (a cluster
    // folded, a tidy-up) keeps it where it was on the screen.
    const anchorId = selected;
    const anchorWas = anchorId && drawn?.centre.get(anchorId) ? { ...drawn.centre.get(anchorId) } : null;
    stage.querySelector('.netmap-svg')?.remove();
    stage.querySelector('.netmap-empty')?.remove();
    const shown = mapEntries(data, c);
    const all = buildForest(shown.map((e) => e.node));
    const entryOf = new Map(shown.map((e) => [String(e.node.id), e]));
    const states = mapStates(all, entryOf, simState);
    // Folded clusters: what is below them is not drawn, and they say how much
    // is folded into them and the worst of it.
    const collapsed = new Set((layoutState.collapsed || []).map(String).filter((id) => (all.children.get(id) || []).length));
    const hidden = new Set();
    for (const id of collapsed) if (!hidden.has(id)) for (const d of descendantsOf(all, id)) hidden.add(d);
    const visible = shown.filter((e) => !hidden.has(String(e.node.id)));
    const forest = buildForest(visible.map((e) => e.node));
    const S = SIZES[c.size];
    const bare = c.labels === 'none';
    const boxH = bare ? BARE[c.size] : c.labels === 'name-host' && !noHosts ? S.hHost : S.hName;
    const boxW = bare ? BARE[c.size] : S.w;
    const layout = layoutMap(forest, { orientation: c.orientation, onlyLinked: c.onlyLinked, node: { w: boxW, h: boxH }, spacing: S.gapX, reach: Math.round(S.gapY * 0.7) });

    if (!layout.pos.size) {
      drawn = null; root = null;
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
    let placed = null;
    if (layout.radial) {
      placed = arrangePositions(forest, layout.pos, layout.cell, layoutState);
      for (const [id, p] of placed) centre.set(id, { x: p.x, y: p.y });
    } else {
      const stepAcross = (c.orientation === 'right' ? boxH : boxW) + S.gapX;
      const stepDown = (c.orientation === 'right' ? boxW + S.gapY * 1.4 : boxH + S.gapY);
      for (const [id, p] of layout.pos) {
        const across = p.slot * stepAcross, down = p.level * stepDown;
        centre.set(id, c.orientation === 'right' ? { x: down, y: across } : { x: across, y: down });
      }
    }
    const pad = 18;
    const extent = () => {
      const xs = [...centre.values()].map((p) => p.x), ys = [...centre.values()].map((p) => p.y);
      return { x: Math.min(...xs) - boxW / 2 - pad, y: Math.min(...ys) - boxH / 2 - pad, w: Math.max(...xs) - Math.min(...xs) + boxW + pad * 2, h: Math.max(...ys) - Math.min(...ys) + boxH + pad * 2 };
    };
    full = extent();

    root = svg('svg', {
      class: `netmap-svg fill-${c.nodeFill}${selected && c.highlightPath ? ' has-path' : ''}`, role: 'group',
      'aria-label': `${title}: ${plural(layout.pos.size, 'node')}, ${plural(forest.parentOf.size, 'dependency', 'dependencies')}${hidden.size ? `, ${plural(hidden.size, 'node')} folded away` : ''}`,
      viewBox: `${full.x} ${full.y} ${full.w} ${full.h}`, preserveAspectRatio: 'xMidYMid meet',
    });
    const edgesG = svg('g', { class: 'netmap-edges', 'aria-hidden': 'true' });
    const nodesG = svg('g', { class: 'netmap-nodes' });
    root.append(edgesG, nodesG);
    const edgeEls = new Map(); // child id → path
    const nodeEls = new Map(); // id → g

    const pathFor = (p, id) => {
      const a = centre.get(p), b = centre.get(id);
      if (layout.radial || c.edges === 'straight') return boxEdge(a, b, boxW, boxH, bare);
      if (c.orientation === 'right') {
        const x1 = a.x + boxW / 2, x2 = b.x - boxW / 2, mx = (x1 + x2) / 2;
        return c.edges === 'elbow' ? `M${x1},${a.y} H${mx} V${b.y} H${x2}` : `M${x1},${a.y} C${mx},${a.y} ${mx},${b.y} ${x2},${b.y}`;
      }
      const y1 = a.y + boxH / 2, y2 = b.y - boxH / 2, my = (y1 + y2) / 2;
      return c.edges === 'elbow' ? `M${a.x},${y1} V${my} H${b.x} V${y2}` : `M${a.x},${y1} C${a.x},${my} ${b.x},${my} ${b.x},${y2}`;
    };

    // Edges: from the parent's near side to the child's.
    const lineCounts = { cut: 0, affected: 0, quiet: 0 };
    for (const [id, p] of forest.parentOf) {
      if (!centre.has(p) || !centre.has(id)) continue;
      const outage = edgeState(states, p, id, simState);
      if (outage) lineCounts[outage]++;
      const childStatus = states.get(id)?.status || 'unknown';
      // What colours the line, as the map was set up: an outage over the
      // colour chosen (the default), the colour chosen only, or the status of
      // the node it leads to.
      const state = c.lineColour === 'status' ? (outage || `st-${childStatus}`) : c.lineColour === 'outage' ? outage : '';
      const ctx = entryOf.get(id)?.context || entryOf.get(p)?.context;
      const sim = states.get(id)?.cause?.sim;
      let look = edgeLook(c, entryOf.get(id)?.node.linkStyle, state);
      // Further into an outage, and into maintenance, the line is dashed
      // unless it has dashes of its own: colour must not be the only cue.
      if ((state === 'affected' || state === 'quiet') && !look.dash) look = { ...look, dash: 'dashed' };
      const el = svg('path', { d: pathFor(p, id), class: `netmap-edge${state ? ` netmap-edge-${state}` : ''}${state === 'cut' ? ' netmap-edge-down' : ''}${sim ? ' netmap-edge-sim' : ''}${ctx ? ' netmap-context' : ''}`, 'data-from': p, 'data-to': id });
      el.__look = look;
      el.setAttribute('style', edgeStyle(look, 1));
      edgesG.append(el);
      edgeEls.set(id, el);
    }

    // Nodes.
    const statusCounts = {};
    const showPod = c.nodeFill === 'both';
    const showStatus = c.nodeFill !== 'colour';
    for (const [id] of layout.pos) {
      const e = entryOf.get(id); if (!e) continue;
      const n = e.node; const p = centre.get(id);
      const st = states.get(id) || { status: 'unknown', cause: null };
      const status = st.status;
      statusCounts[status] = (statusCounts[status] || 0) + 1;
      const m = statusMeta(status);
      const kids = all.children.get(id)?.length || 0;
      const isFolded = collapsed.has(id);
      const folded = isFolded ? descendantsOf(all, id) : [];
      const worst = folded.reduce((w, k) => {
        const s = states.get(k)?.status || 'unknown';
        return SEVERITY[s] > SEVERITY[w] ? s : w;
      }, 'up');
      const parent = all.parentOf.get(id);
      const parentName = parent ? entryOf.get(parent)?.node.name : '';
      const cause = st.cause;
      const causeName = cause ? entryOf.get(cause.by)?.node.name : '';
      const causeText = cause ? (cause.kind === 'maintenance' ? `quiet while ${causeName} is in maintenance` : `cut off while ${causeName} is down`) + (cause.sim ? ' (simulated)' : '') : '';
      const desc = `${n.name}, ${m.label}${st.sim ? ' (simulated)' : ''}${n.host && !noHosts ? `, ${n.host}` : ''}${parentName ? `, depends on ${parentName}` : ''}${kids ? `, ${plural(kids, 'node')} depend${kids === 1 ? 's' : ''} on it` : ''}${isFolded ? `, ${plural(folded.length, 'node')} folded into it` : ''}${causeText ? `, ${causeText}` : ''}${!cause && e.affectedBy ? `, affected by ${e.affectedBy}` : ''}${placed?.get(id)?.pinned ? ', pinned' : ''}`;
      const cls = ['netmap-node', `status-${STATUSES[status] ? status : 'unknown'}`];
      if (e.context) cls.push('netmap-context');
      if (selected === id) cls.push('selected');
      if (st.sim) cls.push('sim-failed');
      if (cause) cls.push(cause.kind === 'maintenance' ? 'is-quiet' : 'is-cut-off');
      if (cause?.sim) cls.push('is-sim');
      if (placed?.get(id)?.pinned) cls.push('is-pinned');
      if (isFolded) cls.push('is-folded');
      const g = svg('g', { class: cls.join(' '), transform: `translate(${p.x},${p.y})`, 'data-node': id });
      const titleEl = svg('title', {}, desc);
      // The node's own colour fills its box (unless the map shows status
      // only), with its text in whichever of black or white reads on it. The
      // status has a small box of its own at the side with a light in it.
      const own = c.nodeFill !== 'status' ? nodeColour(n) : '';
      const ink = own ? textOn(own) : '';
      if (own) g.classList.add('netmap-coloured');
      if (bare) {
        g.append(titleEl, svg('circle', { r: boxW / 2, class: 'netmap-dot', style: own ? `fill:${own}` : null }));
        if (showPod && own) g.append(svg('circle', { r: boxW / 2 + 2.5, class: 'netmap-dot-ring' }));
      } else {
        const rowY = c.labels === 'name-host' && !noHosts ? -boxH / 2 + S.hName / 2 : 0;
        g.append(titleEl, svg('rect', { x: -boxW / 2, y: -boxH / 2, width: boxW, height: boxH, rx: 3, class: 'netmap-box', style: own ? `fill:${own}` : null }));
        let textX;
        if (showPod) {
          // The status box: as tall as the node, square, with a light.
          const pod = boxH;
          const lx = -boxW / 2 + pod / 2;
          const r = Math.max(3, Math.min(pod * 0.2, S.font * 0.42));
          g.append(
            svg('rect', { x: -boxW / 2, y: -boxH / 2, width: pod, height: boxH, rx: 3, class: 'netmap-pod' }),
            svg('circle', { cx: lx, cy: 0, r: r * 2, class: 'netmap-glow' }),
            svg('circle', { cx: lx, cy: 0, r, class: 'netmap-light' }));
          textX = -boxW / 2 + pod + 6;
        } else if (showStatus) {
          g.append(
            svg('rect', { x: -boxW / 2, y: -boxH / 2, width: 4, height: boxH, class: 'netmap-stripe' }),
            svg('circle', { cx: -boxW / 2 + 14, cy: rowY, r: S.font * 0.36, class: 'netmap-dot' }));
          textX = -boxW / 2 + 24;
        } else textX = -boxW / 2 + 9;
        const chars = Math.floor((boxW / 2 - textX - (kids ? 18 : 6)) / (S.font * 0.56));
        g.append(svg('text', { x: textX, y: rowY, 'dominant-baseline': 'central', class: 'netmap-name', 'font-size': S.font, style: ink ? `fill:${ink}` : null }, fit(n.name, chars)));
        if (c.labels === 'name-host' && !noHosts) g.append(svg('text', { x: textX, y: rowY + S.font * 1.15, 'dominant-baseline': 'central', class: 'netmap-host', 'font-size': S.font * 0.84, style: ink ? `fill:${ink};opacity:0.85` : null }, fit(n.host, chars + 2)));
        if (isFolded) {
          // What is folded in: how many, in the colour of the worst of them.
          const label = `+${folded.length}`;
          const cw = S.font * 0.62 * label.length + 10;
          g.append(svg('g', { class: `netmap-fold status-${worst}`, transform: `translate(${boxW / 2 - cw / 2 - 3},${-boxH / 2})` },
            svg('rect', { x: -cw / 2, y: -7, width: cw, height: 14, rx: 7, class: 'netmap-fold-chip' }),
            svg('text', { x: 0, y: 0, 'text-anchor': 'middle', 'dominant-baseline': 'central', class: 'netmap-fold-text', 'font-size': S.font * 0.72 }, label)));
        } else if (kids) g.append(svg('text', { x: boxW / 2 - 6, y: -boxH / 2 + 9, 'text-anchor': 'end', class: 'netmap-kids', 'font-size': S.font * 0.72, style: ink ? `fill:${ink};opacity:0.8` : null }, `▾${kids}`));
        if (placed?.get(id)?.pinned) g.append(svg('circle', { cx: boxW / 2 - 1, cy: boxH / 2 - 1, r: 3.2, class: 'netmap-pin' }));
      }
      // A link to the node's page, or a button that selects it.
      const target = onSelect
        ? svg('a', { href: `#/nodes/${n.id}`, role: 'button', 'aria-label': desc, 'aria-pressed': selected === id ? 'true' : 'false', onclick: (ev) => { ev.preventDefault(); if (wrap.__suppressClick) { wrap.__suppressClick = false; return; } selected = id; onSelect(n, e); highlight(); } })
        : svg('a', { href: `#/nodes/${n.id}`, 'aria-label': desc });
      target.append(g);
      nodesG.append(target);
      nodeEls.set(id, g);
    }
    stage.append(root);
    drawn = { forest, all, entryOf, states, centre, placed, layout, edgeEls, nodeEls, boxW, boxH, bare, pathFor, collapsed, hidden, spacing: S.gapX };
    const anchorNow = anchorWas && centre.get(anchorId);
    if (interactive && box && box.w && box.h) setBox(anchorNow ? { ...box, x: box.x + anchorNow.x - anchorWas.x, y: box.y + anchorNow.y - anchorWas.y } : box);
    else setBox({ ...full });
    applyStrokes(true);
    highlight();

    // Legend and the text equivalent.
    legend.replaceChildren();
    if (c.showLegend) {
      for (const s of ['up', 'degraded', 'down', 'maintenance', 'paused', 'unknown']) {
        if (!statusCounts[s]) continue;
        legend.append(h('span', { class: 'netmap-key' }, h('span', { class: `orb orb-sm orb-${s}`, 'aria-hidden': 'true' }), `${statusMeta(s).label} ${statusCounts[s]}`));
      }
      legend.append(h('span', { class: 'netmap-key muted' }, c.orientation === 'right' ? 'Each node sits to the right of the node it depends on' : layout.radial ? 'Each node sits in a ring round the node it depends on' : 'Each node sits under the node it depends on'));
      // Lines can be any colour and dash by now, so what a coloured one means
      // is said in words — colour and dashes alone must not be the whole cue.
      if (c.lineColour !== 'chosen') {
        if (lineCounts.cut) legend.append(h('span', { class: 'netmap-key' }, h('span', { class: 'netmap-linekey', 'aria-hidden': 'true' }), `Red lines lead out of a node that is down (${plural(lineCounts.cut, 'link')})`));
        if (lineCounts.affected) legend.append(h('span', { class: 'netmap-key' }, h('span', { class: 'netmap-linekey netmap-linekey-affected', 'aria-hidden': 'true' }), `Faded red lines lead further into what it cuts off (${plural(lineCounts.affected, 'link')})`));
        if (lineCounts.quiet) legend.append(h('span', { class: 'netmap-key' }, h('span', { class: 'netmap-linekey netmap-linekey-quiet', 'aria-hidden': 'true' }), `Blue lines lead into what is quiet for maintenance (${plural(lineCounts.quiet, 'link')})`));
      }
      if (hidden.size) legend.append(h('span', { class: 'netmap-key muted' }, `${plural(hidden.size, 'node')} folded into ${plural(collapsed.size, 'cluster')}`));
      if (all.cycles.length) legend.append(h('span', { class: 'netmap-key text-degraded' }, icon('alert'), `${plural(all.cycles.length, 'dependency loop')} broken to draw this`));
    }
    renderList(all, entryOf, states, layout, hidden);
  }

  function renderList(forest, entryOf, states, layout, hidden) {
    const item = (id) => {
      const e = entryOf.get(id); if (!e) return null;
      const status = states.get(id)?.status || 'unknown';
      const kids = (forest.children.get(id) || []).map(item).filter(Boolean);
      return h('li', null, h('span', { class: `orb orb-sm orb-${status}`, 'aria-hidden': 'true' }), ' ', h('a', { href: `#/nodes/${e.node.id}` }, e.node.name), h('span', { class: 'muted' }, ` — ${statusMeta(status).label}`), kids.length ? h('ul', null, kids) : null);
    };
    const roots = forest.roots.filter((r) => layout.pos.has(r) || hidden.has(r));
    const list = h('ul', { class: 'netmap-tree' }, roots.map(item));
    listDetails.replaceChildren(listDetails.firstChild, list);
  }

  // The chosen node, and — unless the map was set not to — the chain it
  // hangs from and everything below it, with the rest faded.
  function highlight() {
    if (!drawn) return;
    const on = new Set();
    if (selected && c.highlightPath && drawn.forest.byId.has(selected)) {
      on.add(selected);
      for (const a of ancestorsOf(drawn.forest, selected)) on.add(a);
      for (const d of descendantsOf(drawn.forest, selected)) on.add(d);
    }
    root?.classList.toggle('has-path', on.size > 0);
    for (const [id, g] of drawn.nodeEls) {
      g.classList.toggle('selected', id === selected);
      g.classList.toggle('on-path', on.has(id));
      g.parentNode?.setAttribute('aria-pressed', id === selected ? 'true' : 'false');
    }
    for (const [id, el] of drawn.edgeEls) el.classList.toggle('on-path', on.has(id) && on.has(drawn.forest.parentOf.get(id)));
  }

  /* ---------- Zoom and pan (the page only) ---------- */
  // How far in and out the map goes: out to a quarter of the whole map, in to
  // where a box is four times its size.
  const zoomBy = (f, cx, cy) => {
    if (!box || !full) return;
    const s = pxPerUnit() || 1;
    const minW = box.w * s / 4; // 4 screen px per unit at the most
    const w = Math.min(full.w * 4, Math.max(Math.min(minW, full.w), box.w * f));
    const k = w / box.w;
    const px = cx ?? box.x + box.w / 2, py = cy ?? box.y + box.h / 2;
    setBox({ x: px - (px - box.x) * k, y: py - (py - box.y) * k, w, h: box.h * k });
  };
  const fitAll = () => { if (full) setBox({ ...full }); };
  // Centres a node and zooms in until its box is about life size.
  const focusOn = (id) => {
    const p = drawn?.centre.get(String(id));
    if (!p || !box) return;
    const s = pxPerUnit() || 1;
    const k = Math.min(1, s / 1.1); // the window shrinks until 1.1 px per unit
    const w = box.w * k, hh = box.h * k;
    setBox({ x: p.x - w / 2, y: p.y - hh / 2, w, h: hh });
  };
  let off = () => {};
  const toDrawing = (ev) => {
    const r = root.getBoundingClientRect();
    // preserveAspectRatio "meet": the drawing is scaled by the smaller ratio and centred.
    const s = Math.min(r.width / box.w, r.height / box.h);
    const ox = (r.width - box.w * s) / 2, oy = (r.height - box.h * s) / 2;
    return { x: box.x + (ev.clientX - r.left - ox) / s, y: box.y + (ev.clientY - r.top - oy) / s, s };
  };
  if (interactive) {
    const onWheel = (ev) => { if (!root) return; ev.preventDefault(); const p = toDrawing(ev); zoomBy(ev.deltaY > 0 ? 1.15 : 1 / 1.15, p.x, p.y); };
    let pan = null;
    const onDown = (ev) => {
      if (ev.button !== 0 || !root) return;
      const nodeLink = ev.target.closest?.('.netmap-nodes a');
      if (nodeLink) {
        // A node: dragged (when the map may be arranged) or chosen.
        const id = nodeLink.querySelector('.netmap-node')?.getAttribute('data-node');
        if (id && mayArrange && drawn?.layout.radial) {
          const start = toDrawing(ev);
          dragging = { id, start, moved: false, alone: ev.shiftKey || ev.altKey, pointer: ev.pointerId };
        }
        return;
      }
      pan = { x: ev.clientX, y: ev.clientY, box: { ...box }, s: toDrawing(ev).s, moved: false };
      stage.setPointerCapture?.(ev.pointerId); stage.classList.add('panning');
    };
    const onMove = (ev) => {
      if (dragging) { dragMove(ev); return; }
      if (!pan) return;
      if (Math.hypot(ev.clientX - pan.x, ev.clientY - pan.y) > 3) pan.moved = true;
      setBox({ ...pan.box, x: pan.box.x - (ev.clientX - pan.x) / pan.s, y: pan.box.y - (ev.clientY - pan.y) / pan.s });
    };
    const onUp = (ev) => {
      if (dragging) { dragEnd(); return; }
      // A click on the empty map lets go of the chosen node.
      if (pan && !pan.moved && selected && onSelect) { selected = null; highlight(); onSelect(null, null); }
      pan = null; stage.classList.remove('panning');
    };
    const onKey = (ev) => {
      if (ev.target.closest('a')) {
        if (ev.key === 'Escape' && selected && onSelect) { selected = null; highlight(); onSelect(null, null); }
        return;
      }
      const step = box ? box.w * 0.1 : 0;
      const k = { '+': () => zoomBy(1 / 1.25), '=': () => zoomBy(1 / 1.25), '-': () => zoomBy(1.25), '0': fitAll,
        ArrowLeft: () => setBox({ ...box, x: box.x - step }), ArrowRight: () => setBox({ ...box, x: box.x + step }),
        ArrowUp: () => setBox({ ...box, y: box.y - step }), ArrowDown: () => setBox({ ...box, y: box.y + step }),
        Escape: () => { if (selected && onSelect) { selected = null; highlight(); onSelect(null, null); } } }[ev.key];
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
  // The stage changing size changes how many pixels a drawing unit is.
  const resize = typeof ResizeObserver === 'function' ? new ResizeObserver(() => applyStrokes()) : null;
  resize?.observe(stage);

  /* ---------- Dragging a node ---------- */
  // The node moves with the pointer, and so does everything it carries (the
  // nodes below it with no place of their own) unless Shift or Alt is held,
  // which moves it alone. Letting go pins it there.
  function dragMove(ev) {
    const d = dragging;
    if (!d || !drawn) return;
    const p = toDrawing(ev);
    const dx = p.x - d.start.x, dy = p.y - d.start.y;
    if (!d.moved) {
      if (Math.hypot(dx, dy) * p.s < 4) return;
      d.moved = true;
      stage.setPointerCapture?.(d.pointer);
      stage.classList.add('dragging');
      d.group = d.alone ? [] : carriedBy(drawn.forest, d.id, layoutState);
      d.from = new Map([d.id, ...d.group].map((id) => [id, { ...drawn.centre.get(id) }]));
    }
    for (const [id, from] of d.from) {
      const at = { x: from.x + dx, y: from.y + dy };
      drawn.centre.set(id, at);
      drawn.nodeEls.get(id)?.setAttribute('transform', `translate(${at.x},${at.y})`);
    }
    // Lines into and out of anything that moved.
    for (const id of d.from.keys()) {
      const e = drawn.edgeEls.get(id);
      const parent = drawn.forest.parentOf.get(id);
      if (e && parent != null) e.setAttribute('d', drawn.pathFor(parent, id));
      for (const k of drawn.forest.children.get(id) || []) { const ce = drawn.edgeEls.get(k); if (ce) ce.setAttribute('d', drawn.pathFor(id, k)); }
    }
  }
  function dragEnd() {
    const d = dragging;
    dragging = null;
    stage.classList.remove('dragging');
    if (!d?.moved || !drawn) { if (pendingDraw) { pendingDraw = false; draw(); } return; }
    wrap.__suppressClick = true;
    setTimeout(() => { wrap.__suppressClick = false; }, 0);
    const cell = drawn.layout.cell;
    const positions = { ...(layoutState.positions || {}) };
    const at = drawn.centre.get(d.id);
    positions[d.id] = { x: Math.round((at.x / cell.w) * 10) / 10, y: Math.round((at.y / cell.h) * 10) / 10, pinned: true };
    if (d.alone) {
      // Moved alone: what it would have carried stays where it is.
      for (const k of carriedBy(drawn.forest, d.id, layoutState)) {
        const q = drawn.centre.get(k);
        if (q) positions[k] = { x: Math.round((q.x / cell.w) * 10) / 10, y: Math.round((q.y / cell.h) * 10) / 10 };
      }
    } else {
      // Carried: the group forgets any tidied places, and swings round to
      // face away from the line in.
      for (const k of carriedBy(drawn.forest, d.id, layoutState)) if (positions[k] && !positions[k].pinned) delete positions[k];
    }
    layoutState = { ...layoutState, positions };
    pendingDraw = false;
    draw();
    onArrange?.(cloneArranged(layoutState), { moved: d.id });
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
    focus(id) { selected = id != null ? String(id) : selected; highlight(); focusOn(id); },
    fit: fitAll,
    setArranged(next) { layoutState = cloneArranged(next); draw(); },
    setSim(next) { simState = next; draw(); },
    setHideHosts(on) { noHosts = !!on; draw(); },
    setCanArrange(on) { mayArrange = !!on; },
    /** What the last drawing was made from: the forests, the states, where every node is, and the layout. */
    view: () => drawn,
    destroy() { off(); resize?.disconnect(); wrap.remove(); },
  };
}

/** A copy of an arrangement, always with positions and collapsed. */
function cloneArranged(a) {
  const src = a && typeof a === 'object' ? a : {};
  return { positions: { ...(src.positions || {}) }, collapsed: [...(src.collapsed || [])] };
}

/**
 * The arrangement with a cluster folded (or unfolded): `id` is added to or
 * removed from `collapsed`.
 */
export function toggleFolded(arranged, id, fold) {
  const a = cloneArranged(arranged);
  const n = Number(id);
  const has = a.collapsed.map(Number).includes(n);
  const want = fold ?? !has;
  a.collapsed = want ? (has ? a.collapsed : [...a.collapsed, n]) : a.collapsed.filter((x) => Number(x) !== n);
  return a;
}

/** The arrangement with `id` unpinned: it goes back next to its parent, taking what it carried with it. */
export function unpin(forest, arranged, id) {
  const a = cloneArranged(arranged);
  const key = String(id);
  const group = carriedBy(forest, key, a);
  delete a.positions[key];
  for (const k of group) if (a.positions[k] && !a.positions[k].pinned) delete a.positions[k];
  return a;
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
 *
 * @typedef {{ color?: string, width?: number, dash?: string }} LineStyle
 * @param {{ value?: LineStyle, inherit?: boolean, defaults?: LineStyle, disabled?: boolean, label?: string, onChange?: (value: LineStyle) => void }} [opts]
 */
export function lineStyleControls({ value = {}, inherit = false, defaults = {}, disabled = false, label = 'Line', onChange } = {}) {
  let color = normalizeLineColor(value.color);
  let width = normalizeLineWidth(value.width, inherit ? 0 : LINE_WIDTH.default);
  let dash = normalizeLineDash(value.dash) || (inherit ? '' : 'solid');
  const unset = inherit ? 'Same as the map' : 'Theme colour';
  // A colour input holds #rrggbb only; a stored #rgb or #rrggbbaa is shown
  // as that and left exactly as it was unless the person picks another.
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
  // The thickness applies as it is typed or stepped, not only once the field
  // is left; a half-typed value ("2.") waits for the rest of it. Leaving the
  // field puts what it holds back into range.
  widthIn.addEventListener('input', () => {
    // An empty field mid-edit is only "follow the map" for a link; the map's
    // own thickness waits for a number.
    if (widthIn.value === '' ? !inherit : !Number.isFinite(Number(widthIn.value))) return;
    const next = normalizeLineWidth(widthIn.value, inherit ? 0 : LINE_WIDTH.default);
    if (next !== width) { width = next; emit(); }
  });
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
 * A node's own colour, as a control: a colour picker, the colour in words
 * and a None button. `.value` is the colour ('' for none) and
 * `onChange(value)` fires when a person changes it. Used on the network map's
 * side panel, in the node editor and in the bulk editor.
 *
 * @param {{ value?: string, disabled?: boolean, label?: string, onChange?: (value: string) => void }} [opts]
 */
export function nodeColourControl({ value = '', disabled = false, label = 'Node colour', onChange } = {}) {
  let colour = normalizeLineColor(value);
  const picker = h('input', { type: 'color', value: hex6(colour) || '#3b82f6', 'aria-label': label, disabled });
  const shown = h('span', { class: 'netmap-colour-text mono muted' });
  const none = h('button', { type: 'button', class: 'btn btn-sm', 'aria-label': `No ${label.toLowerCase()}`, onclick: () => { colour = ''; paint(); onChange && onChange(colour); } }, 'None');
  function paint() {
    shown.textContent = colour || 'None';
    if (colour) picker.value = hex6(colour);
    none.disabled = disabled || !colour;
  }
  picker.addEventListener('input', () => { colour = normalizeLineColor(picker.value); paint(); });
  picker.addEventListener('change', () => { colour = normalizeLineColor(picker.value); paint(); onChange && onChange(colour); });
  paint();
  const el = h('div', { class: 'netmap-colour node-colour' }, picker, shown, none);
  Object.defineProperty(el, 'value', { get: () => colour });
  return el;
}

/**
 * The style of one link, as a group of controls: the line from a node to the
 * node it depends on, `parentName`. `value` is the link's style (or null),
 * `defaults` what the map draws a line as, and `onChange(style)` gets the
 * style as it should be kept — null when it sets nothing. The Reset button
 * clears the lot, so the link follows the map again.
 *
 * @param {{ parentName?: string, value?: LineStyle | null, defaults?: LineStyle, disabled?: boolean, onChange?: (style: LineStyle | null) => void }} [opts]
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
 *
 * @param {any} cfg
 * @param {{ groups?: any, onChange?: (cfg: any) => void }} [opts]
 */
export function mapConfigEditor(cfg, { groups = null, onChange } = {}) {
  const c = normalizeMapConfig(cfg);
  const emit = () => onChange && onChange(wrap.value);
  const opts = mapFilterOptions(groups);
  const orientation = selectInput({ options: MAP_ORIENTATIONS, value: c.orientation, onchange: emit });
  const labels = selectInput({ options: MAP_LABELS, value: c.labels, onchange: emit });
  const size = selectInput({ options: MAP_SIZES, value: c.size, onchange: emit });
  const edges = selectInput({ options: MAP_EDGES, value: c.edges, onchange: emit });
  const nodeFill = selectInput({ options: MAP_NODE_FILLS, value: c.nodeFill, onchange: emit });
  const lineColour = selectInput({ options: MAP_LINE_COLOURS, value: c.lineColour, onchange: emit });
  const group = selectInput({ options: opts.groups, value: c.group, onchange: emit });
  const tag = selectInput({ options: opts.tags, value: c.tag, onchange: emit });
  const onlyLinked = checkbox({ label: 'Only nodes with a dependency', checked: c.onlyLinked, onChange: emit });
  const highlightPath = checkbox({ label: 'Choosing a node lights up what it depends on and what depends on it', checked: c.highlightPath, onChange: emit });
  const showLegend = checkbox({ label: 'Show the legend', checked: c.showLegend, onChange: emit });
  // How every line looks unless a link has a style of its own.
  const lines = lineStyleControls({ value: { color: c.lineColor, width: c.lineWidth, dash: c.lineStyle }, label: 'Map line', onChange: emit });
  const resetLines = h('button', { type: 'button', class: 'btn btn-sm', onclick: () => lines.reset() }, icon('refresh'), 'Reset the lines');
  const wrap = h('div', { class: 'stack-sm netmap-options' },
    h('div', { class: 'form-grid' },
      field({ label: 'Layout', input: orientation, help: 'Clusters put each node in the middle of a ring of what depends on it, and can be arranged by dragging.' }),
      field({ label: 'Labels', input: labels }),
      field({ label: 'Node size', input: size }),
      field({ label: 'Line shape', input: edges, help: 'Clusters always draw straight lines.' }),
      field({ label: 'Node colour shows', input: nodeFill, help: 'Both: the node’s own colour, with its status as a light beside it.' }),
      field({ label: 'Line colour shows', input: lineColour }),
      field({ label: 'Group', input: group, help: 'The nodes a filtered node depends on stay on the map, faded.' }),
      field({ label: 'Tag', input: tag })),
    h('fieldset', { class: 'netmap-linegroup' },
      h('legend', null, 'Line style'),
      lines.el,
      h('div', { class: 'row-between' }, h('span', { class: 'help' }, 'A link can have its own colour, thickness and style: choose a node on the map. Thickness is screen pixels at the least, and grows as you zoom in.'), resetLines)),
    h('div', { class: 'stack-sm u-gap-6' }, onlyLinked, highlightPath, showLegend));
  Object.defineProperty(wrap, 'value', { get: () => normalizeMapConfig({
    orientation: orientation.value, labels: labels.value, size: size.value, edges: edges.value, group: group.value, tag: tag.value,
    nodeFill: nodeFill.value, lineColour: lineColour.value,
    onlyLinked: onlyLinked.input.checked, highlightPath: highlightPath.input.checked, showLegend: showLegend.input.checked,
    lineColor: lines.value.color, lineWidth: lines.value.width, lineStyle: lines.value.dash,
  }) });
  return wrap;
}

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
/** @param {any} [cfg] */
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
 * It is worked out from the leaves up, deterministically (the same nodes give
 * the same picture every time, whatever order they arrive in):
 *
 *  1. Each node's cluster is laid out in a frame of its own, the way out from
 *     its parent pointing right. The children that have none of their own sit
 *     close, on one ring round the node (a long run of them on two or more
 *     rings, alternately, so a switch with forty machines is not one huge
 *     circle).
 *     A child with a cluster of its own sits further out, on a line long
 *     enough that its whole cluster clears the ring, in a slice of the turn as
 *     wide as that cluster needs; the slices and the ring places are dealt out
 *     in turn so the bigger clusters are spread round evenly. Round the root
 *     they take the whole turn, the first at the top (a lone child to the
 *     right); round any other node they leave a gap facing the node's parent,
 *     so the line in stays clear, and a node with only a few children keeps
 *     them on its far side.
 *  2. The clusters are put together: each child's cluster is turned to face
 *     the way its slice points and set down at the end of its line.
 *  3. Rules that are then made to hold exactly: no two boxes closer than
 *     `spacing`, no line shorter than `reach` between the boxes it joins,
 *     and no line running through a box it does not join. If a few rounds
 *     of nudging cannot settle it, the picture is spread out a little and
 *     tried again, which always ends. The rings are built to keep these, so
 *     this rarely has anything to do.
 *
 * Several trees are laid out one by one; the biggest keeps its root at 0,0
 * and the others are set down round it in the nearest free space. Nodes with
 * no link at all are a block of rows under everything, as in the tree
 * layouts.
 */
function layoutClusters(forest, trees, isolated, linked, { onlyLinked, isolatedPerRow, node, spacing, reach: wantReach }) {
  // A line shorter than the space kept round a box would be all box.
  const reach = Math.max(wantReach, spacing / 2);
  const half = Math.hypot(node.w / 2, node.h / 2); // a box fits in a circle this big
  const own = half + spacing / 2; // ... with half the space to its neighbours all round
  const pos = new Map();
  let deepest = trees.length ? 0 : -1;
  // How far a box's border is from its centre, along a direction.
  const edge = (cos, sin) => Math.min(node.w / 2 / Math.max(Math.abs(cos), 1e-9), node.h / 2 / Math.max(Math.abs(sin), 1e-9));
  // Whether two boxes at these centres are at least `spacing` apart.
  const apart = (a, b) => {
    const gx = Math.abs(a.x - b.x) - node.w, gy = Math.abs(a.y - b.y) - node.h;
    return (gx >= 0 && gy >= 0 ? Math.hypot(gx, gy) : Math.max(gx, gy)) >= spacing;
  };
  const TURN = 2 * Math.PI;
  // How far apart two rings of leaves are, along a direction: a box's width
  // and the spacing out to the sides, its height and the spacing up and down.
  const ringStep = (a) => 1.1 * Math.min((node.w + spacing) / Math.max(Math.abs(Math.cos(a)), 1e-9), (node.h + spacing) / Math.max(Math.abs(Math.sin(a)), 1e-9));
  const maxStep = 1.1 * (node.w + spacing);
  // The gap left facing a node's parent, and the least slice a ring place gets.
  const BACK_GAP = 0.4 * Math.PI;
  const MIN_SLOT = Math.PI / 36;

  /**
   * One node's cluster in its own frame: the node at 0,0, the way out from
   * its parent along +x. That frame is turned by `rot` on the page, and the
   * rings are worked out as they will be drawn — boxes are always level, so
   * whether two of them clear each other depends on which way the ring is
   * turned. Returns how far the cluster reaches behind the node (`back`),
   * ahead of it (`fwd`) and to either side (`side`), boxes included, and with
   * `final` the positions of everything in it (with the depth below the node).
   * The children's clusters are measured first (once each, unturned) to share
   * out the turn, then laid out for good facing the way they ended up.
   */
  const measured = new Map();
  const measure = (id) => {
    if (!measured.has(id)) measured.set(id, cluster(id, false, 0, false));
    return measured.get(id);
  };
  const cluster = (id, isRoot, rot, final) => {
    const pts = new Map([[id, { x: 0, y: 0, lvl: 0 }]]);
    const kids = forest.children.get(id) || [];
    if (!kids.length) return { pts, back: own, side: own, fwd: own };
    const sub = new Map();
    for (const k of kids) if ((forest.children.get(k) || []).length) sub.set(k, measure(k));
    const leaves = kids.filter((k) => !sub.has(k));
    const branches = kids.filter((k) => sub.has(k));
    // The order round the ring: the leaves by name, with the branches dealt
    // in among them evenly.
    const order = [];
    {
      let li = 0;
      const n = kids.length;
      const at = new Set(branches.map((_, i) => Math.min(n - 1, Math.floor(((i + 0.5) * n) / branches.length))));
      let bi = 0;
      for (let i = 0; i < n; i++) order.push(at.has(i) && bi < branches.length ? branches[bi++] : (li < leaves.length ? leaves[li++] : branches[bi++]));
    }
    const m = leaves.length;
    // Which ring the i-th leaf is on when there are `rings` of them: in turn,
    // except that round a whole turn the few left over after the last full
    // round go on rings 1, 2, … so none is next to a leaf on its own ring.
    const ringOf = (i, rings) => {
      const rem = m % rings;
      return isRoot && rem && i >= m - rem ? 1 + (i - (m - rem)) : i % rings;
    };
    const minLine = (a) => 2 * edge(Math.cos(a + rot), Math.sin(a + rot)) + reach; // centre to centre, along a

    let ring = 0, stagger = 0, layers = 1, angle = new Map(), dist = new Map(), arc = TURN;
    let outer = own; // how far the ring reaches out from the node, boxes included
    let push = 1; // how much further out the branches are put when the slices do not fit
    for (let pass = 0; pass < 6; pass++) {
      // Each branch as far out as clears the ring, and the slice it needs there.
      const need = new Map();
      for (const b of branches) {
        const c = sub.get(b);
        const d = Math.max(node.w + reach, outer + spacing + c.back) * push;
        dist.set(b, d);
        need.set(b, 2 * Math.atan2(c.side + spacing / 2, Math.max(own, d - c.back)));
      }
      const needB = [...need.values()].reduce((a, b) => a + b, 0);
      arc = isRoot ? TURN : Math.min(TURN - BACK_GAP, Math.max(kids.length * Math.PI / 3, needB + m * Math.PI / 8));
      if (needB + m * MIN_SLOT > arc && pass < 5) { push *= 1.25; continue; }
      const slot = m ? Math.max(MIN_SLOT, (arc - needB) / m) : 0;
      // Where each one sits: one after another round the arc. Boxes are
      // wide, so two leaves side by side at the top or bottom of a ring need
      // far more room between them than two stacked at its sides; a leaf's
      // share of the turn follows how much it needs where it sits (worked
      // out from where an even share would put it).
      const place = (width) => {
        const total = order.reduce((a, k) => a + width(k), 0);
        const scale = total > arc ? arc / total : 1;
        let cursor = isRoot ? (kids.length === 1 ? -width(order[0]) * scale / 2 : -Math.PI / 2 - (width(order[0]) * scale) / 2) : -Math.min(arc, total * scale) / 2;
        const out = new Map();
        for (const k of order) { const w = width(k) * scale; out.set(k, cursor + w / 2); cursor += w; }
        return out;
      };
      angle = place((k) => (sub.has(k) ? Math.max(need.get(k), slot) : slot));
      if (m > 3) {
        const roomAt = (a) => Math.min((node.w + spacing) / Math.max(Math.abs(Math.sin(a)), 1e-9), (node.h + spacing) / Math.max(Math.abs(Math.cos(a)), 1e-9));
        const mean = leaves.reduce((t, k) => t + roomAt(angle.get(k) + rot), 0) / m;
        const first = angle;
        angle = place((k) => (sub.has(k) ? Math.max(need.get(k), slot) : Math.max(MIN_SLOT / 2, (slot * roomAt(first.get(k) + rot)) / mean)));
      }
      if (!m) break;
      // The ring: the smallest radius at which every leaf clears the node by
      // the reach and its neighbours by the spacing. A long run of leaves is
      // also tried on several rings, alternately, and whichever reaches out
      // least is used.
      const leafAngles = leaves.map((k) => angle.get(k));
      const fits = (r, st, rings) => {
        const at = leafAngles.map((a, i) => { const g = a + rot, rr = r + ringOf(i, rings) * st * ringStep(g); return { x: rr * Math.cos(g), y: rr * Math.sin(g) }; });
        const middle = { x: 0, y: 0 };
        for (let i = 0; i < at.length; i++) {
          if (!apart(at[i], middle)) return false;
          for (let j = i + 1; j < at.length; j++) if (!apart(at[i], at[j])) return false;
        }
        return true;
      };
      const smallest = (st, rings) => {
        let r = Math.max(...leafAngles.map(minLine));
        for (let i = 0; i < 400 && !fits(r, st, rings); i++) r *= 1.04;
        return r;
      };
      let r = smallest(0, 1), st = 0, rings = 1;
      // A long run of leaves: up to three rings past 8, four past 40.
      const step = 1;
      for (let k = 2; k <= (m > 40 ? 4 : m > 8 ? 3 : 1); k++) {
        // Round a whole turn the last leaf is next to the first, so the
        // rings have to alternate across that join as well.
        if (isRoot && k === 2 && m % 2) continue;
        const rk = smallest(step, k);
        if (rk + (k - 1) * maxStep < r + (rings - 1) * st * maxStep) { r = rk; st = step; rings = k; }
      }
      const changed = Math.abs(r - ring) > 0.5 || st !== stagger || rings !== layers;
      ring = r; stagger = st; layers = rings; outer = ring + (layers - 1) * stagger * maxStep + own;
      if (!changed && pass > 0) break;
    }

    leaves.forEach((k, i) => {
      const a = angle.get(k), r = ring + ringOf(i, layers) * stagger * ringStep(a + rot);
      pts.set(k, { x: r * Math.cos(a), y: r * Math.sin(a), lvl: 1 });
    });
    let back = own, side = own, fwd = own;
    for (const p of pts.values()) { back = Math.max(back, -p.x + own); fwd = Math.max(fwd, p.x + own); side = Math.max(side, Math.abs(p.y) + own); }
    for (const b of branches) {
      const a = angle.get(b), d = dist.get(b), c = Math.cos(a), sn = Math.sin(a);
      const ox = d * c, oy = d * sn;
      if (final) {
        for (const [k, p] of cluster(b, false, rot + a, true).pts) pts.set(k, { x: ox + p.x * c - p.y * sn, y: oy + p.x * sn + p.y * c, lvl: p.lvl + 1 });
      } else {
        // Measuring only: the child's cluster as the box it was measured in.
        const f = sub.get(b);
        for (const [u, v] of [[-f.back, -f.side], [-f.back, f.side], [f.fwd, -f.side], [f.fwd, f.side]]) {
          const x = ox + u * c - v * sn, y = oy + u * sn + v * c;
          back = Math.max(back, -x); fwd = Math.max(fwd, x); side = Math.max(side, Math.abs(y));
        }
      }
    }
    return { pts, back, side, fwd };
  };

  const layoutTree = (root) => {
    const at = new Map();
    const level = new Map();
    for (const [id, p] of cluster(root, true, 0, true).pts) {
      at.set(id, { x: p.x, y: p.y });
      level.set(id, p.lvl);
      deepest = Math.max(deepest, p.lvl);
    }
    const ids = [...at.keys()];
    const edges = [];
    for (const id of ids) for (const k of forest.children.get(id) || []) edges.push([k, id]);
    const subtree = (id) => { const out = [id]; for (let i = 0; i < out.length; i++) out.push(...(forest.children.get(out[i]) || [])); return out; };
    settle(at, ids, edges, subtree, 40);
    // The root back at the origin.
    const r0 = at.get(root);
    const ox = r0.x, oy = r0.y;
    for (const p of at.values()) { p.x -= ox; p.y -= oy; }
    return { at, level };
  };

  // Nudges until no two boxes are too close, no line is too short and no line
  // crosses a box it does not join; if that will not settle, spreads the
  // whole picture out from its middle a little and tries again.
  const settle = (at, ids, edges, subtree, attempts) => {
    const margin = Math.max(2, spacing / 4);
    for (let attempt = 0; attempt < attempts; attempt++) {
      let clean = false;
      for (let round = 0; round < 40 && !clean; round++) {
        clean = true;
        for (let i = 0; i < ids.length; i++) {
          for (let j = i + 1; j < ids.length; j++) {
            const a = at.get(ids[i]), b = at.get(ids[j]);
            let dx = b.x - a.x, dy = b.y - a.y;
            if (!dx && !dy) dx = 1;
            const gx = Math.abs(dx) - node.w, gy = Math.abs(dy) - node.h;
            const far = gx >= 0 && gy >= 0 ? Math.hypot(gx, gy) : Math.max(gx, gy);
            if (far >= spacing - 1e-9) continue;
            clean = false;
            // The least move that clears it: across, or up and down.
            const needX = node.w + spacing - Math.abs(dx), needY = node.h + spacing - Math.abs(dy);
            if (gx >= 0 && gy >= 0) {
              const d = Math.hypot(gx, gy) || 1, push = (spacing - d) / 2 + 0.5;
              a.x -= (gx / d) * push * Math.sign(dx); a.y -= (gy / d) * push * Math.sign(dy);
              b.x += (gx / d) * push * Math.sign(dx); b.y += (gy / d) * push * Math.sign(dy);
            } else if (needX * node.h < needY * node.w) {
              const push = needX / 2 + 0.5; a.x -= Math.sign(dx) * push; b.x += Math.sign(dx) * push;
            } else {
              const push = needY / 2 + 0.5; a.y -= Math.sign(dy || 1) * push; b.y += Math.sign(dy || 1) * push;
            }
          }
        }
        for (const [k, p] of edges) {
          const a = at.get(p), b = at.get(k);
          const dx = b.x - a.x, dy = b.y - a.y, d = Math.hypot(dx, dy) || 1;
          const line = d - 2 * edge(dx / d, dy / d);
          if (line >= reach - 1e-9) continue;
          clean = false;
          const out = reach - line + 0.5;
          for (const id of subtree(k)) { const q = at.get(id); q.x += (dx / d) * out; q.y += (dy / d) * out; }
        }
        for (const [k, p] of edges) {
          const a = at.get(p), b = at.get(k);
          for (const id of ids) {
            if (id === k || id === p) continue;
            const o = at.get(id);
            if (!segmentHitsBox(a, b, o, node.w / 2 + margin, node.h / 2 + margin)) continue;
            clean = false;
            // Off the line, the short way.
            const vx = b.x - a.x, vy = b.y - a.y, len2 = vx * vx + vy * vy || 1;
            const t = Math.max(0, Math.min(1, ((o.x - a.x) * vx + (o.y - a.y) * vy) / len2));
            let nx = o.x - (a.x + vx * t), ny = o.y - (a.y + vy * t);
            let nl = Math.hypot(nx, ny);
            if (nl < 1e-6) { nx = -vy; ny = vx; nl = Math.hypot(nx, ny) || 1; }
            const step = node.h / 2 + margin + 1;
            o.x += (nx / nl) * step; o.y += (ny / nl) * step;
          }
        }
      }
      if (clean) return true;
      if (attempt === attempts - 1) return false;
      // Spread out from the middle and try again.
      let cx = 0, cy = 0;
      for (const id of ids) { cx += at.get(id).x; cy += at.get(id).y; }
      cx /= ids.length; cy /= ids.length;
      for (const id of ids) { const q = at.get(id); q.x = cx + (q.x - cx) * 1.15; q.y = cy + (q.y - cy) * 1.15; }
    }
    return false;
  };

  // Lay out every tree, the biggest first, and set each later one down in the
  // nearest free space round what is already there.
  const placedBoxes = [];
  const clear = (b) => placedBoxes.every((o) => b.maxX + spacing * 2 <= o.minX || o.maxX + spacing * 2 <= b.minX || b.maxY + spacing * 2 <= o.minY || o.maxY + spacing * 2 <= b.minY);
  for (const [ti, root] of trees.entries()) {
    const { at, level } = layoutTree(root);
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
      const step = Math.max(own, 20);
      for (let i = 0; i < 20000; i++) {
        const ang = i * 0.5, rad = step * Math.sqrt(i) * 1.2;
        const x = cx + rad * Math.cos(ang) - (minX + w / 2), y = cy + rad * Math.sin(ang) - (minY + hgt / 2);
        if (clear({ minX: minX + x, maxX: maxX + x, minY: minY + y, maxY: maxY + y })) { ox = x; oy = y; break; }
      }
    }
    placedBoxes.push({ minX: minX + ox, maxX: maxX + ox, minY: minY + oy, maxY: maxY + oy });
    for (const [id, p] of at) pos.set(id, { x: p.x + ox, y: p.y + oy, level: level.get(id), ring: 0 });
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
  return { pos, rings: [], bounds: extent(), levels: Math.max(pos.size ? 1 : 0, deepest + 1), linked, isolated: onlyLinked ? [] : isolated, radial: true };
}

/** Whether the segment a→b passes through the box of half-size hw×hh centred on o. */
function segmentHitsBox(a, b, o, hw, hh) {
  // Liang–Barsky clipping against the box.
  let t0 = 0, t1 = 1;
  const dx = b.x - a.x, dy = b.y - a.y;
  const clip = (p, q) => {
    if (Math.abs(p) < 1e-12) return q >= 0;
    const r = q / p;
    if (p < 0) { if (r > t1) return false; if (r > t0) t0 = r; } else { if (r < t0) return false; if (r < t1) t1 = r; }
    return true;
  };
  return clip(-dx, a.x - (o.x - hw)) && clip(dx, (o.x + hw) - a.x) && clip(-dy, a.y - (o.y - hh)) && clip(dy, (o.y + hh) - a.y) && t0 < t1;
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
      // The node's own colour fills its box, with its text in whichever of
      // black or white reads on it. The status keeps the stripe, the dot and
      // the border, so a coloured node that goes down still shows it.
      const own = nodeColour(n);
      const ink = own ? textOn(own) : '';
      if (own) g.classList.add('netmap-coloured');
      if (bare) {
        g.append(title, svg('circle', { r: boxW / 2, class: 'netmap-dot' }));
      } else {
        g.append(title,
          svg('rect', { x: -boxW / 2, y: -boxH / 2, width: boxW, height: boxH, rx: 3, class: 'netmap-box', style: own ? `fill:${own}` : null }),
          svg('rect', { x: -boxW / 2, y: -boxH / 2, width: 4, height: boxH, class: 'netmap-stripe' }),
          svg('circle', { cx: -boxW / 2 + 14, cy: c.labels === 'name-host' ? -boxH / 2 + S.hName / 2 : 0, r: S.font * 0.36, class: 'netmap-dot' }),
          svg('text', { x: -boxW / 2 + 24, y: c.labels === 'name-host' ? -boxH / 2 + S.hName / 2 : 0, 'dominant-baseline': 'central', class: 'netmap-name', 'font-size': S.font, style: ink ? `fill:${ink}` : null }, fit(n.name, chars)));
        if (c.labels === 'name-host') g.append(svg('text', { x: -boxW / 2 + 24, y: -boxH / 2 + S.hName / 2 + S.font * 1.15, 'dominant-baseline': 'central', class: 'netmap-host', 'font-size': S.font * 0.84, style: ink ? `fill:${ink};opacity:0.85` : null }, fit(n.host, chars + 2)));
        if (kids) g.append(svg('text', { x: boxW / 2 - 6, y: -boxH / 2 + 9, 'text-anchor': 'end', class: 'netmap-kids', 'font-size': S.font * 0.72, style: ink ? `fill:${ink};opacity:0.8` : null }, `▾${kids}`));
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
      field({ label: 'Layout', input: orientation, help: 'Clusters put each node in the middle of a ring of what depends on it; a switch with things of its own starts a ring of its own.' }),
      field({ label: 'Labels', input: labels }),
      field({ label: 'Node size', input: size }),
      field({ label: 'Line shape', input: edges, help: 'Clusters draw straight or gently curved lines.' }),
      field({ label: 'Group', input: group, help: 'The nodes a filtered node depends on stay on the map, faded.' }),
      field({ label: 'Tag', input: tag })),
    h('fieldset', { class: 'netmap-linegroup' },
      h('legend', null, 'Line style'),
      lines.el,
      h('div', { class: 'row-between' }, h('span', { class: 'help' }, 'A link can have its own colour, thickness and style: choose a node on the map.'), resetLines)),
    h('div', { class: 'stack-sm u-gap-6' }, onlyLinked, colorEdges, showLegend));
  Object.defineProperty(wrap, 'value', { get: () => normalizeMapConfig({
    orientation: orientation.value, labels: labels.value, size: size.value, edges: edges.value, group: group.value, tag: tag.value,
    onlyLinked: onlyLinked.input.checked, colorEdges: colorEdges.input.checked, showLegend: showLegend.input.checked,
    lineColor: lines.value.color, lineWidth: lines.value.width, lineStyle: lines.value.dash,
  }) });
  return wrap;
}

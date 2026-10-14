// The network map (web/netmap.js): the dependency forest, its layout and
// which nodes a filtered map shows are pure and tested directly; the drawing
// is checked for the parts a person relies on — a node per node, a line per
// dependency, the down colour on lines out of a node that is down, and the
// colour, thickness and dashes of a line coming from the link's own style,
// else the map's, else the theme's.

import { freshRoot, window } from './dom.mjs';
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  buildForest, layoutMap, mapEntries, normalizeMapConfig, renderNetworkMap, mapConfigEditor, linkStyleEditor, lineStyleControls,
  MAP_ORIENTATIONS, MAP_LINE_STYLES, LINE_WIDTH, normalizeLineColor, normalizeLineWidth, normalizeLineDash, normalizeLinkStyle, lineDash, edgeLook, edgeStyle,
} from '../../web/netmap.js';

const node = (id, name, parent = null, extra = {}) => ({ id, name, host: `10.0.0.${id}`, dependsOnNodeId: parent, groups: [], tags: [], ...extra });
const NODES = [
  node(1, 'Firewall'),
  node(2, 'Core switch', 1),
  node(3, 'NAS', 2),
  node(4, 'Camera', 2),
  node(5, 'Wi-Fi', 1),
  node(6, 'Website'),
];

test('buildForest finds roots and children, sorted by name', () => {
  const f = buildForest(NODES);
  assert.deepEqual(f.roots, ['1', '6']);
  assert.deepEqual(f.children.get('1'), ['2', '5']);
  assert.deepEqual(f.children.get('2'), ['4', '3'], 'Camera before NAS');
  assert.equal(f.parentOf.get('3'), '2');
  assert.deepEqual(f.cycles, []);
});

test('buildForest breaks a dependency loop instead of looping', () => {
  const f = buildForest([node(1, 'A', 2), node(2, 'B', 1)]);
  assert.equal(f.cycles.length, 1);
  assert.equal(f.roots.length, 1);
  assert.equal(f.parentOf.size, 1);
});

test('a parent outside the set leaves its child a root', () => {
  const f = buildForest([node(3, 'NAS', 2)]);
  assert.deepEqual(f.roots, ['3']);
});

test('layoutMap centres parents over their children and sets unlinked nodes apart', () => {
  const f = buildForest(NODES);
  const l = layoutMap(f);
  const p = (id) => l.pos.get(String(id));
  assert.equal(p(1).level, 0);
  assert.equal(p(2).level, 1);
  assert.equal(p(3).level, 2);
  assert.equal(p(2).slot, (p(4).slot + p(3).slot) / 2, 'a parent sits over the middle of its children');
  assert.ok(p(6).level > p(3).level, 'the unlinked Website goes in a block under the trees');
  assert.deepEqual(l.isolated, ['6']);
  const linkedOnly = layoutMap(f, { onlyLinked: true });
  assert.equal(linkedOnly.pos.has('6'), false);

  assert.equal(l.radial, false);
  assert.equal(l.pos.get('1').x, undefined, 'the tree layouts stay in slots and levels');
});

test('mapEntries keeps the chain upstream of a filtered node, marked as context', () => {
  const entries = NODES.map((n) => ({ node: { ...n, groups: n.id === 3 ? ['Storage'] : [] }, status: 'up' }));
  const shown = mapEntries(entries, { group: 'Storage' });
  const ids = shown.map((e) => e.node.id).sort();
  assert.deepEqual(ids, [1, 2, 3]);
  assert.equal(shown.find((e) => e.node.id === 3).context, false);
  assert.equal(shown.find((e) => e.node.id === 1).context, true);
});

test('normalizeMapConfig falls back to defaults for anything unknown', () => {
  const c = normalizeMapConfig({ orientation: 'sideways', labels: 'name-host', size: 'huge' });
  assert.equal(c.orientation, 'down');
  assert.equal(c.labels, 'name-host');
  assert.equal(c.size, 'medium');
  assert.equal(c.colorEdges, true);
});

test('renderNetworkMap draws every node and dependency, and colours an outage', () => {
  const root = freshRoot();
  const entries = NODES.map((n) => ({ node: n, status: n.id === 2 ? 'down' : 'up' }));
  const map = renderNetworkMap(root, entries, {}, { title: 'Test map' });
  assert.equal(root.querySelectorAll('.netmap-node').length, 6);
  assert.equal(root.querySelectorAll('.netmap-edge').length, 4);
  assert.equal(root.querySelectorAll('.netmap-edge-down').length, 2, 'the two lines out of the switch that is down');
  assert.ok(root.querySelector('.netmap-node.status-down'));
  assert.ok(root.querySelector('a[href="#/nodes/3"]'), 'a node links to its page');
  assert.match(root.querySelector('.netmap-list').textContent, /Firewall.*Core switch.*Camera/s, 'the list view nests the tree');
  map.update(entries, { onlyLinked: true });
  assert.equal(root.querySelectorAll('.netmap-node').length, 5);
  map.destroy();
  assert.equal(root.querySelector('.netmap'), null);
});

/* ---------- The clusters layout ---------- */

// The size a node is drawn at (a medium map's), the room kept between
// neighbours, and the shortest line a ring may have.
const BOX = { w: 148, h: 32 };
const SPACING = 24;
const REACH = 40;
const clusters = (nodes, opts = {}) => {
  const f = buildForest(nodes);
  return { f, l: layoutMap(f, { orientation: 'radial', node: BOX, spacing: SPACING, reach: REACH, ...opts }) };
};
const at = (l, id) => l.pos.get(String(id));
const distance = (a, b) => Math.hypot(a.x - b.x, a.y - b.y);

/** Every pair of boxes that overlap, or are less than `gap` apart (the shortest way from one box to the other). */
function overlaps(l, gap = 0) {
  const ids = [...l.pos.keys()], bad = [];
  for (let i = 0; i < ids.length; i++) {
    for (let j = i + 1; j < ids.length; j++) {
      const a = l.pos.get(ids[i]), b = l.pos.get(ids[j]);
      const gx = Math.abs(a.x - b.x) - BOX.w, gy = Math.abs(a.y - b.y) - BOX.h;
      if ((gx < 0 && gy < 0) || Math.hypot(Math.max(0, gx), Math.max(0, gy)) < gap) bad.push([ids[i], ids[j]]);
    }
  }
  return bad;
}

/** Every line that runs through the box of a node it does not join. */
function linesThroughBoxes(f, l) {
  const bad = [];
  for (const [id, parent] of f.parentOf) {
    const a = at(l, parent), b = at(l, id);
    for (const [other, o] of l.pos) {
      if (other === id || other === parent) continue;
      for (let t = 0.01; t < 1; t += 0.01) {
        const x = a.x + (b.x - a.x) * t, y = a.y + (b.y - a.y) * t;
        if (Math.abs(x - o.x) < BOX.w / 2 - 1 && Math.abs(y - o.y) < BOX.h / 2 - 1) { bad.push([parent, id, other]); break; }
      }
    }
  }
  return bad;
}

// A home network: a router with a Pi-hole, a media server and a switch, and
// five things plugged into the switch.
const HOME = [
  node(1, 'Router'), node(2, 'Switch', 1), node(3, 'Pi-hole', 1), node(4, 'Plex', 1),
  node(5, 'NAS', 2), node(6, 'Camera', 2), node(7, 'Printer', 2), node(8, 'Home Assistant', 2), node(9, 'Backup server', 2),
];
// An office: a core with four switches and what hangs off them.
const OFFICE = [node(1, 'Core')];
{
  let id = 2;
  for (let s = 1; s <= 4; s++) {
    const sw = id++;
    OFFICE.push(node(sw, `Switch ${s}`, 1));
    for (let d = 1; d <= [8, 3, 12, 5][s - 1]; d++) OFFICE.push(node(id++, `Device ${s}.${d}`, sw));
  }
}

test('clusters: a node with children is the centre of an evenly spaced ring of them', () => {
  for (const nodes of [HOME, OFFICE, NODES.slice(0, 5)]) {
    const { f, l } = clusters(nodes);
    for (const [id, kids] of f.children) {
      if (kids.length < 2) continue;
      const centre = at(l, id);
      assert.ok(centre.ring > 0, `${id} has a ring`);
      const ring = kids.map((k) => at(l, k));
      for (const k of ring) assert.ok(Math.abs(distance(centre, k) - centre.ring) < 1e-6, 'every child is one ring radius away');
      // Neighbouring children are the same angle apart, all the way round.
      const angles = ring.map((k) => Math.atan2(k.y - centre.y, k.x - centre.x)).sort((a, b) => a - b);
      const step = (2 * Math.PI) / kids.length;
      for (let i = 0; i < angles.length; i++) {
        const gap = (angles[(i + 1) % angles.length] - angles[i] + 2 * Math.PI) % (2 * Math.PI) || 2 * Math.PI;
        assert.ok(Math.abs(gap - step) < 1e-6, `children of ${id} are ${step} apart, not ${gap}`);
      }
      // ... so the parent is at the centre of them.
      assert.ok(Math.abs(ring.reduce((s, k) => s + k.x, 0) / kids.length - centre.x) < 1e-6);
      assert.ok(Math.abs(ring.reduce((s, k) => s + k.y, 0) / kids.length - centre.y) < 1e-6);
    }
  }
});

test('clusters: a single tree has its root in the middle, and rings sit on rings', () => {
  const { f, l } = clusters(HOME);
  assert.deepEqual([at(l, 1).x, at(l, 1).y, at(l, 1).level], [0, 0, 0]);
  assert.equal(l.radial, true);
  // The switch is on the router's ring and is the centre of a ring of its own,
  // whose members are one level further out.
  const router = at(l, 1), sw = at(l, 2);
  assert.ok(Math.abs(distance(router, sw) - router.ring) < 1e-6);
  assert.ok(sw.ring > 0 && sw.level === 1);
  for (const k of f.children.get('2')) {
    assert.equal(at(l, k).level, 2);
    assert.ok(Math.abs(distance(sw, at(l, k)) - sw.ring) < 1e-6);
    assert.ok(distance(router, at(l, k)) > 0);
  }
  // The leaves have no ring.
  assert.equal(at(l, 5).ring, 0);
  // The line to the parent runs through a gap in the node's own ring, not
  // through one of its children.
  const toParent = Math.atan2(router.y - sw.y, router.x - sw.x);
  for (const k of f.children.get('2')) {
    const c = at(l, k);
    const away = Math.abs(((Math.atan2(c.y - sw.y, c.x - sw.x) - toParent + 3 * Math.PI) % (2 * Math.PI)) - Math.PI);
    assert.ok(away > 0.1, 'no child sits on the line back to the parent');
  }
  assert.equal(l.levels, 3);
});

test('clusters: no two boxes overlap and no line runs through a box it does not join', () => {
  const chain = Array.from({ length: 9 }, (_, i) => node(i + 1, `Hop ${i}`, i ? i : null));
  const star = [node(1, 'Hub'), ...Array.from({ length: 40 }, (_, i) => node(i + 2, `Leaf ${String(i).padStart(2, '0')}`, 1))];
  const lopsided = [node(1, 'Core'), node(2, 'Big', 1), node(3, 'Tiny A', 1), node(4, 'Tiny B', 1), ...Array.from({ length: 15 }, (_, i) => node(i + 5, `Big ${i}`, 2))];
  for (const [name, nodes] of Object.entries({ HOME, OFFICE, chain, star, lopsided, NODES })) {
    const { f, l } = clusters(nodes);
    assert.deepEqual(overlaps(l, SPACING - 1e-6), [], `${name}: boxes keep their distance`);
    assert.deepEqual(linesThroughBoxes(f, l), [], `${name}: lines stay clear of other boxes`);
    assert.equal(l.pos.size, nodes.length, `${name}: every node is placed`);
    for (const p of l.pos.values()) assert.ok(Number.isFinite(p.x) && Number.isFinite(p.y));
    // bounds hold every box
    for (const p of l.pos.values()) assert.ok(p.x - BOX.w / 2 >= l.bounds.minX - 1e-6 && p.x + BOX.w / 2 <= l.bounds.maxX + 1e-6 && p.y - BOX.h / 2 >= l.bounds.minY - 1e-6 && p.y + BOX.h / 2 <= l.bounds.maxY + 1e-6);
  }
});

test('clusters: random forests never overlap, and keep the spacing asked for', () => {
  let seed = 11;
  const rnd = () => { seed = (seed * 1664525 + 1013904223) % 4294967296; return seed / 4294967296; };
  for (let round = 0; round < 60; round++) {
    const n = 2 + Math.floor(rnd() * 45);
    const nodes = [];
    for (let i = 1; i <= n; i++) nodes.push(node(i, `N${String(Math.floor(rnd() * 500)).padStart(3, '0')}-${i}`, i > 1 && rnd() < 0.85 ? 1 + Math.floor(rnd() * (i - 1)) : null));
    const spacing = 10 + Math.floor(rnd() * 30);
    const { l } = clusters(nodes, { spacing });
    assert.equal(l.pos.size, n);
    assert.deepEqual(overlaps(l, spacing - 1e-6), [], `round ${round}: boxes ${spacing} apart`);
  }
});

test('clusters: the same nodes in any order give the same picture', () => {
  const flat = (l) => [...l.pos].sort(([x], [y]) => x.localeCompare(y)).map(([id, p]) => [id, p.x, p.y, p.level, p.ring]);
  const a = clusters(OFFICE).l;
  const shuffled = [...OFFICE].sort((x, y) => ((x.id * 7919) % 13) - ((y.id * 7919) % 13) || x.id - y.id);
  assert.notDeepEqual(shuffled.map((n) => n.id), OFFICE.map((n) => n.id), 'the input really is in another order');
  assert.deepEqual(flat(clusters(shuffled).l), flat(a));
  assert.deepEqual(flat(clusters(OFFICE).l), flat(a), 'and the same twice');
  assert.deepEqual(clusters(shuffled).l.bounds, a.bounds);
});

test('clusters: children go round in the order of their names, from the top, clockwise', () => {
  const four = [node(1, 'Hub'), node(2, 'Delta', 1), node(3, 'Alpha', 1), node(4, 'Charlie', 1), node(5, 'Bravo', 1)];
  const l = clusters(four).l, hub = at(l, 1);
  const at90 = (id) => { const p = at(l, id); return [Math.round(p.x - hub.x), Math.round(p.y - hub.y)]; };
  assert.deepEqual(at90(3), [0, -Math.round(hub.ring)], 'Alpha is straight up');
  assert.deepEqual(at90(5), [Math.round(hub.ring), 0], 'Bravo is to its right');
  assert.deepEqual(at90(4), [0, Math.round(hub.ring)], 'Charlie is below');
  assert.deepEqual(at90(2), [-Math.round(hub.ring), 0], 'Delta is on the left');
  // One child goes to the right, two go left and right.
  const one = clusters([node(1, 'A'), node(2, 'B', 1)]).l;
  assert.ok(at(one, 2).x > 0 && Math.abs(at(one, 2).y) < 1e-9);
  const two = clusters([node(1, 'A'), node(2, 'B', 1), node(3, 'C', 1)]).l;
  assert.ok(at(two, 2).x < 0 && at(two, 3).x > 0 && Math.abs(at(two, 2).y) < 1e-6);
});

test('clusters: several trees ring the biggest one, and the unlinked nodes sit in rows under all of them', () => {
  const nodes = [...HOME, node(20, 'Cloud'), node(21, 'S3', 20), node(22, 'API', 20), node(23, 'CDN', 20),
    node(30, 'Alone 1'), node(31, 'Alone 2'), node(32, 'Alone 3'), node(33, 'Alone 4')];
  const { f, l } = clusters(nodes);
  assert.deepEqual([at(l, 1).x, at(l, 1).y], [0, 0], 'the biggest tree has its root in the middle');
  assert.ok(distance(at(l, 1), at(l, 20)) > at(l, 1).ring, 'the other tree is outside the first one\'s rings');
  assert.deepEqual(overlaps(l, SPACING - 1e-6), []);
  assert.deepEqual(linesThroughBoxes(f, l), []);
  assert.deepEqual(l.isolated, ['30', '31', '32', '33']);
  const lowestLinked = Math.max(...[...l.pos].filter(([id]) => l.linked.has(id)).map(([, p]) => p.y));
  for (const id of l.isolated) assert.ok(at(l, id).y - BOX.h / 2 > lowestLinked + BOX.h / 2, `${id} is under every linked node`);
  // Rows are centred under the trees.
  const linkedBox = [...l.pos].filter(([id]) => l.linked.has(id)).map(([, p]) => p.x);
  const mid = (Math.min(...linkedBox) + Math.max(...linkedBox)) / 2;
  const xs = l.isolated.map((id) => at(l, id).x);
  assert.ok(Math.abs((Math.min(...xs) + Math.max(...xs)) / 2 - mid) < BOX.w, 'the block is centred on the trees');

  // Three trees: they go round the biggest one, not in a line.
  const three = [...HOME, node(20, 'Cloud'), node(21, 'S3', 20), node(40, 'Lab'), node(41, 'Bench', 40)];
  const t = clusters(three).l;
  assert.deepEqual(overlaps(t, SPACING - 1e-6), []);
  assert.ok(at(t, 20).x < 0 && at(t, 40).x > 0, 'two more trees go left and right of the first');
});

test('clusters: onlyLinked leaves the unlinked nodes out, and unlinked nodes alone get a block of their own', () => {
  const nodes = [...HOME, node(30, 'Alone')];
  assert.equal(clusters(nodes, { onlyLinked: true }).l.pos.has('30'), false);
  assert.deepEqual(clusters(nodes, { onlyLinked: true }).l.isolated, []);
  assert.equal(clusters(nodes).l.pos.has('30'), true);
  const none = clusters([node(1, 'A'), node(2, 'B'), node(3, 'C'), node(4, 'D'), node(5, 'E')]);
  assert.equal(none.l.pos.size, 5);
  assert.deepEqual(overlaps(none.l, SPACING - 1e-6), []);
  assert.ok(Math.abs((none.l.bounds.minX + none.l.bounds.maxX) / 2) < 1e-6 && Math.abs((none.l.bounds.minY + none.l.bounds.maxY) / 2) < 1e-6, 'the block is centred on the origin');
  assert.equal(clusters([node(1, 'A'), node(2, 'B')], { onlyLinked: true }).l.pos.size, 0);
  assert.equal(clusters([]).l.pos.size, 0);
});

test('clusters: a dependency loop is broken and every node is still drawn', () => {
  const { f, l } = clusters([node(1, 'A', 3), node(2, 'B', 1), node(3, 'C', 2), node(4, 'D', 2)]);
  assert.equal(f.cycles.length, 1);
  assert.equal(l.pos.size, 4);
  assert.deepEqual(overlaps(l, SPACING - 1e-6), []);
  const self = clusters([node(1, 'A', 1)]).l;
  assert.equal(self.pos.size, 1);
});

test('clusters: a big fan-out makes a big ring rather than overlapping boxes', () => {
  const kids = 150;
  const nodes = [node(1, 'Hub'), ...Array.from({ length: kids }, (_, i) => node(i + 2, `Leaf ${String(i).padStart(3, '0')}`, 1))];
  const started = Date.now();
  const { l } = clusters(nodes);
  assert.ok(Date.now() - started < 1500, 'laid out quickly');
  assert.deepEqual(overlaps(l, SPACING - 1e-6), []);
  const hub = at(l, 1);
  // The ring is big enough that neighbouring children (a box's width apart at the very least) fit round it.
  assert.ok(hub.ring * 2 * Math.sin(Math.PI / kids) >= Math.hypot(BOX.w, BOX.h) + SPACING - 1e-6, 'the circumference holds the boxes');
  for (let i = 2; i < kids + 2; i++) assert.ok(Math.abs(distance(hub, at(l, i)) - hub.ring) < 1e-6);
});

test('clusters: a chain of nodes does not double in size with every link', () => {
  const chain = (n) => Array.from({ length: n }, (_, i) => node(i + 1, `Hop ${String(i).padStart(2, '0')}`, i ? i : null));
  const w = (n) => { const b = clusters(chain(n)).l.bounds; return b.maxX - b.minX; };
  assert.ok(w(12) < 12 * 300, `12 hops are ${w(12)} wide`);
  assert.ok(w(12) < w(6) * 2.5, 'twice the hops is about twice the width');
  const b12 = clusters(chain(12)).l.bounds;
  assert.ok(Math.abs(b12.maxY - b12.minY - BOX.h) < 1e-6, 'and it runs in a straight line');
});

test('clusters: nodes are kept apart by the spacing asked for, and a line is never shorter than the reach', () => {
  const tight = clusters(HOME, { spacing: 10, reach: 20 }).l, loose = clusters(HOME, { spacing: 40, reach: 90 }).l;
  assert.ok(loose.bounds.maxX - loose.bounds.minX > tight.bounds.maxX - tight.bounds.minX);
  assert.deepEqual(overlaps(tight, 10 - 1e-6), []);
  const f = buildForest(HOME);
  for (const [l, reach] of [[tight, 20], [loose, 90]]) {
    for (const [child, parent] of f.parentOf) {
      const p = at(l, parent), q = at(l, child), d = distance(p, q);
      // how far from a box's centre its edge is, along the line to the other
      const edge = Math.min(BOX.w / 2 / Math.max(Math.abs(q.x - p.x) / d, 1e-9), BOX.h / 2 / Math.max(Math.abs(q.y - p.y) / d, 1e-9));
      assert.ok(d - 2 * edge >= reach - 1e-6, `the line from ${parent} to ${child} is ${d - 2 * edge} long, not ${reach}`);
    }
  }
  // Called without sizes, it still works (a default node box).
  const bare = layoutMap(buildForest(HOME), { orientation: 'radial' });
  assert.equal(bare.pos.size, HOME.length);
});

test('the radial orientation is still the stored value, now labelled as clusters', () => {
  assert.equal(normalizeMapConfig({ orientation: 'radial' }).orientation, 'radial');
  const o = MAP_ORIENTATIONS.find((x) => x.value === 'radial');
  assert.match(o.label, /Clusters/);
  assert.deepEqual(MAP_ORIENTATIONS.map((x) => x.value), ['down', 'right', 'radial']);
});

/* ---------- Line styles ---------- */

const HOSTILE = ['red', 'rgb(1,2,3)', 'url(#a)', '#fff;fill:red', '#fff" onload="x', 'var(--down)', 'javascript:alert(1)', '#ggg', '#12', '#12345', '#1234567', '#123456789', '3b82f6', ' ', '', null, undefined, 7, {}, ['#fff']];

test('line colours are #rgb, #rrggbb or #rrggbbaa and nothing else', () => {
  assert.equal(normalizeLineColor('#FFF'), '#fff');
  assert.equal(normalizeLineColor(' #3B82F6 '), '#3b82f6');
  assert.equal(normalizeLineColor('#3b82f680'), '#3b82f680');
  for (const bad of HOSTILE) assert.equal(normalizeLineColor(bad), '', `${JSON.stringify(bad)} is not a colour`);
});

test('line widths are clamped, and what is not a number is not a width', () => {
  assert.equal(LINE_WIDTH.default, 1.5);
  assert.equal(normalizeLineWidth(99, 1.5), LINE_WIDTH.max);
  assert.equal(normalizeLineWidth(0.2, 1.5), LINE_WIDTH.min);
  assert.equal(normalizeLineWidth('3', 1.5), 3);
  assert.equal(normalizeLineWidth(2.26, 1.5), 2.3);
  for (const bad of ['', ' ', 'thick', '3px', null, undefined, NaN, Infinity, -2, 0, true, {}]) assert.equal(normalizeLineWidth(bad, 0), 0, `${JSON.stringify(bad)} is no width`);
  assert.equal(normalizeLineWidth('thick', 1.5), 1.5, 'the fallback is what is used');
  assert.equal(normalizeLineDash('Dotted'), 'dotted');
  for (const bad of ['wavy', '5 4', 'solid;fill:red', '', null, 3]) assert.equal(normalizeLineDash(bad), '');
  assert.deepEqual(MAP_LINE_STYLES.map((o) => o.value), ['solid', 'dashed', 'dotted', 'dash-dot']);
});

test('a map configuration has line defaults, and refuses a bad colour, width or style', () => {
  const d = normalizeMapConfig({});
  assert.deepEqual([d.lineColor, d.lineWidth, d.lineStyle], ['', 1.5, 'solid']);
  const c = normalizeMapConfig({ lineColor: '#E5A50A', lineWidth: 4, lineStyle: 'dash-dot' });
  assert.deepEqual([c.lineColor, c.lineWidth, c.lineStyle], ['#e5a50a', 4, 'dash-dot']);
  const bad = normalizeMapConfig({ lineColor: 'url(#x)', lineWidth: 'wide', lineStyle: 'wavy' });
  assert.deepEqual([bad.lineColor, bad.lineWidth, bad.lineStyle], ['', 1.5, 'solid']);
  assert.equal(normalizeMapConfig({ lineWidth: 500 }).lineWidth, 8);
  assert.equal(normalizeMapConfig({ lineWidth: -1 }).lineWidth, 1.5);
  // An older saved configuration, with none of it, still loads as it was.
  const old = normalizeMapConfig({ orientation: 'radial', labels: 'name-host', size: 'large', edges: 'straight', onlyLinked: true });
  assert.equal(old.orientation, 'radial');
  assert.equal(old.lineWidth, 1.5);
});

test('a link style keeps what is valid and is null when it sets nothing', () => {
  assert.deepEqual(normalizeLinkStyle({ color: '#ABC', width: 3, dash: 'dotted' }), { color: '#abc', width: 3, dash: 'dotted' });
  assert.deepEqual(normalizeLinkStyle({ color: 'red', width: 'x', dash: 'dashed' }), { color: '', width: 0, dash: 'dashed' });
  for (const none of [null, undefined, {}, { color: 'red', width: 0, dash: 'wavy' }, 'dashed', 4, []]) assert.equal(normalizeLinkStyle(none), null);
});

test('a line is drawn in its own style, else the map\'s, else the theme\'s', () => {
  const plain = normalizeMapConfig({});
  const styled = normalizeMapConfig({ lineColor: '#112233', lineWidth: 3, lineStyle: 'dashed' });
  // theme: nothing is set, so the stylesheet decides colour and dashes
  assert.deepEqual(edgeLook(plain, null), { color: '', width: 1.5, dash: '', cut: false });
  // map: its colour, thickness and style
  assert.deepEqual(edgeLook(styled, null), { color: '#112233', width: 3, dash: 'dashed', cut: false });
  // link: each field wins over the map's, and a field it leaves out follows the map
  assert.deepEqual(edgeLook(styled, { color: '#ff0000', width: 5, dash: 'dotted' }), { color: '#ff0000', width: 5, dash: 'dotted', cut: false });
  assert.deepEqual(edgeLook(styled, { dash: 'solid' }), { color: '#112233', width: 3, dash: 'solid', cut: false });
  assert.deepEqual(edgeLook(styled, { width: 2 }), { color: '#112233', width: 2, dash: 'dashed', cut: false });
  assert.deepEqual(edgeLook(plain, { color: '#fff' }), { color: '#fff', width: 1.5, dash: '', cut: false });
  // junk in a link's style is no style at all
  assert.deepEqual(edgeLook(styled, { color: 'red', width: 'x', dash: 'wavy' }), edgeLook(styled, null));
});

test('an outage keeps its colour whatever colour a line was given, but takes its width and dashes', () => {
  const styled = normalizeMapConfig({ lineColor: '#112233', lineWidth: 3, lineStyle: 'dotted' });
  const own = { color: '#00ff00', width: 6, dash: 'dashed' };
  assert.equal(edgeLook(styled, own, true).color, '', 'no colour of its own: the stylesheet draws it in the down colour');
  assert.equal(edgeLook(styled, null, true).color, '');
  assert.equal(edgeLook(styled, own, true).width, 6);
  assert.equal(edgeLook(styled, own, true).dash, 'dashed');
  assert.equal(edgeLook(styled, null, true).width, 3);
  assert.equal(edgeLook(styled, null, true).dash, 'dotted');
  assert.equal(edgeStyle(edgeLook(styled, own, true)).includes('stroke:'), false, 'nothing in the style that could override the down colour');
  // With nothing chosen the stylesheet's dashes for a down line stand, since
  // colour alone should not be what marks it; an explicit solid is obeyed.
  assert.equal(edgeStyle(edgeLook(normalizeMapConfig({}), null, true)).includes('dasharray'), false);
  assert.match(edgeStyle(edgeLook(normalizeMapConfig({}), { dash: 'solid' }, true)), /stroke-dasharray:none/);
});

test('every line style has a dash pattern that grows with the thickness', () => {
  assert.equal(lineDash('solid', 2).array, 'none');
  for (const style of ['dashed', 'dotted', 'dash-dot']) {
    const thin = lineDash(style, 1), thick = lineDash(style, 6);
    assert.notEqual(thin.array, 'none');
    assert.notEqual(thin.array, thick.array, `${style} scales with the line`);
    const sum = (a) => a.split(' ').reduce((s, n) => s + Number(n), 0);
    assert.ok(sum(thick.array) > sum(thin.array));
    for (const n of thin.array.split(' ')) assert.ok(Number.isFinite(Number(n)));
  }
  assert.equal(lineDash('dotted', 2).cap, 'round');
  assert.equal(lineDash('dashed', 2).cap, 'butt');
  assert.equal(lineDash('dash-dot', 2).array.split(' ').length, 4, 'a dash, a gap, a dot and a gap');
  assert.notEqual(lineDash('dashed', 2).array, lineDash('dotted', 2).array);
  assert.notEqual(lineDash('dashed', 2).array, lineDash('dash-dot', 2).array);
});

test('what reaches a style attribute is only ever numbers and a hex colour', () => {
  const hostile = { color: '#fff;fill:red', width: '1;stroke:red', dash: 'dashed;x:y' };
  for (const cfg of [normalizeMapConfig({ lineColor: '"><script>', lineWidth: '3;x', lineStyle: 'a;b' }), normalizeMapConfig({})]) {
    const s = edgeStyle(edgeLook(cfg, hostile));
    assert.match(s, /^stroke-width:\d+(\.\d)?$/, s);
  }
  const ok = edgeStyle(edgeLook(normalizeMapConfig({ lineColor: '#ABCDEF', lineWidth: 2.5, lineStyle: 'dash-dot' }), null));
  assert.match(ok, /^stroke-width:2\.5;stroke:#abcdef;stroke-dasharray:[\d. ]+;stroke-linecap:round$/);
});

/* ---------- Drawing with line styles and in clusters ---------- */

const styleOf = (el) => el.getAttribute('style') || '';
const edgeOf = (root, from, to) => root.querySelector(`.netmap-edge[data-from="${from}"][data-to="${to}"]`);

test('renderNetworkMap draws every line in the map\'s style, and a link\'s own over it', () => {
  const root = freshRoot();
  const entries = NODES.map((n) => ({ node: n.id === 4 ? { ...n, linkStyle: { color: '#ff8800', width: 5, dash: 'dotted' } } : n, status: 'up' }));
  const map = renderNetworkMap(root, entries, { lineColor: '#112233', lineWidth: 3, lineStyle: 'dashed' }, { title: 'Styled' });
  const edges = [...root.querySelectorAll('.netmap-edge')];
  assert.equal(edges.length, 4);
  for (const e of edges) assert.ok(e.getAttribute('class').startsWith('netmap-edge'), 'still the stylesheet\'s class');
  assert.match(styleOf(edgeOf(root, 1, 2)), /stroke:#112233/);
  assert.match(styleOf(edgeOf(root, 1, 2)), /stroke-width:3(;|$)/);
  assert.match(styleOf(edgeOf(root, 1, 2)), /stroke-dasharray:\d/);
  assert.equal(styleOf(edgeOf(root, 1, 2)), styleOf(edgeOf(root, 2, 3)), 'every link without a style of its own looks the same');
  const own = styleOf(edgeOf(root, 2, 4));
  assert.match(own, /stroke:#ff8800/);
  assert.match(own, /stroke-width:5(;|$)/);
  assert.match(own, /stroke-dasharray:0\.01 /, 'dotted');
  assert.match(own, /stroke-linecap:round/);
  // Changing the map's style redraws the lines, and the link keeps its own.
  map.update(entries, { lineColor: '#abcdef', lineWidth: 2, lineStyle: 'solid' });
  assert.match(styleOf(edgeOf(root, 1, 2)), /stroke:#abcdef/);
  assert.doesNotMatch(styleOf(edgeOf(root, 1, 2)), /dasharray/, 'solid leaves the stylesheet alone');
  assert.match(styleOf(edgeOf(root, 2, 4)), /stroke:#ff8800/);
  map.destroy();
});

test('an unstyled map draws its lines as it always did', () => {
  const root = freshRoot();
  renderNetworkMap(root, NODES.map((n) => ({ node: n, status: 'up' })), {}, {});
  for (const e of root.querySelectorAll('.netmap-edge')) assert.equal(styleOf(e), 'stroke-width:1.5');
});

test('an outage shows in the down colour on a custom coloured line, and the legend says what red means', () => {
  const root = freshRoot();
  const entries = NODES.map((n) => ({ node: { ...n, linkStyle: { color: '#00ff00', width: 4, dash: 'dotted' } }, status: n.id === 2 ? 'down' : 'up' }));
  renderNetworkMap(root, entries, { lineColor: '#00ffff', lineWidth: 2 }, {});
  for (const to of [3, 4]) {
    const e = edgeOf(root, 2, to);
    assert.ok(e.classList.contains('netmap-edge-down'));
    assert.doesNotMatch(styleOf(e), /stroke:/, 'no colour that could hide the outage');
    assert.match(styleOf(e), /stroke-width:4/);
    assert.match(styleOf(e), /stroke-dasharray:0\.01 /);
  }
  assert.match(styleOf(edgeOf(root, 1, 2)), /stroke:#00ff00/, 'the line into the node that is down is not cut');
  const legend = root.querySelector('.netmap-legend').textContent;
  assert.match(legend, /Red lines lead out of a node that is down \(2 links\)/, 'colour and dashes are not the only cue');
  // Not drawn when there is no outage.
  const calm = freshRoot();
  renderNetworkMap(calm, NODES.map((n) => ({ node: n, status: 'up' })), {}, {});
  assert.doesNotMatch(calm.querySelector('.netmap-legend').textContent, /Red lines/);
});

test('renderNetworkMap in clusters: a node per node, a line per dependency, nothing overlapping', () => {
  const root = freshRoot();
  const entries = HOME.concat([node(50, 'Website')]).map((n) => ({ node: n, status: n.id === 2 ? 'down' : 'up' }));
  const map = renderNetworkMap(root, entries, { orientation: 'radial' }, { title: 'Clusters' });
  assert.equal(root.querySelectorAll('.netmap-node').length, 10);
  assert.equal(root.querySelectorAll('.netmap-edge').length, 8);
  assert.equal(root.querySelectorAll('.netmap-edge-down').length, 5, 'the five lines out of the switch that is down');
  const centres = [...root.querySelectorAll('.netmap-node')].map((g) => g.getAttribute('transform').match(/translate\(([-\d.e]+),([-\d.e]+)\)/).slice(1).map(Number));
  for (const c of centres) assert.ok(c.every(Number.isFinite));
  for (let i = 0; i < centres.length; i++) for (let j = i + 1; j < centres.length; j++) {
    assert.ok(Math.abs(centres[i][0] - centres[j][0]) >= 148 || Math.abs(centres[i][1] - centres[j][1]) >= 32, 'boxes do not overlap on the page');
  }
  assert.match(root.querySelector('.netmap-legend').textContent, /ring round the node it depends on/);
  // Lines end at the boxes rather than running under them; curved bows, straight does not.
  const d = edgeOf(root, 1, 3).getAttribute('d');
  assert.match(d, /^M[-\d.e]+,[-\d.e]+ Q/, 'curved: a gentle bend');
  map.update(entries, { orientation: 'radial', edges: 'straight' });
  assert.match(edgeOf(root, 1, 3).getAttribute('d'), /^M[-\d.e]+,[-\d.e]+ L/);
  map.update(entries, { orientation: 'radial', edges: 'elbow' });
  assert.match(edgeOf(root, 1, 3).getAttribute('d'), / L/, 'right angles make no sense round a ring: a straight line');
  // onlyLinked still hides the unlinked, labels none still draws dots, and a click selects.
  map.update(entries, { orientation: 'radial', onlyLinked: true });
  assert.equal(root.querySelectorAll('.netmap-node').length, 9);
  map.update(entries, { orientation: 'radial', labels: 'none', size: 'small' });
  assert.equal(root.querySelectorAll('.netmap-dot').length, 10);
  assert.match(root.querySelector('.netmap-list').textContent, /Router.*Pi-hole/s);
  map.destroy();
});

test('clusters and the tree layouts draw the same nodes the same way', () => {
  for (const orientation of ['down', 'right', 'radial']) {
    const root = freshRoot();
    const map = renderNetworkMap(root, NODES.map((n) => ({ node: n, status: 'up' })), { orientation }, {});
    assert.equal(root.querySelectorAll('.netmap-node').length, 6, orientation);
    assert.equal(root.querySelectorAll('.netmap-edge').length, 4, orientation);
    assert.ok(root.querySelector('svg').getAttribute('viewBox').split(' ').every((v) => Number.isFinite(Number(v))), orientation);
    map.destroy();
  }
});

/* ---------- The controls ---------- */

const change = (el) => el.dispatchEvent(new window.Event('change', { bubbles: true }));
const colourOf = (root) => root.querySelector('input[type="color"]');
const widthOf = (root) => root.querySelector('input[type="number"]');
const styleSelectOf = (root) => [...root.querySelectorAll('select')].find((s) => [...s.options].some((o) => o.value === 'dash-dot'));
const buttonNamed = (root, text) => [...root.querySelectorAll('button')].find((b) => b.textContent.trim() === text);

test('the map options carry the line colour, thickness and style, and reset them', () => {
  const seen = [];
  const ed = mapConfigEditor({ lineColor: '#ABCDEF', lineWidth: 2, lineStyle: 'dotted' }, { onChange: (c) => seen.push(c) });
  assert.deepEqual([ed.value.lineColor, ed.value.lineWidth, ed.value.lineStyle], ['#abcdef', 2, 'dotted']);
  assert.equal(colourOf(ed).value, '#abcdef');
  assert.equal(widthOf(ed).value, '2');
  assert.equal(styleSelectOf(ed).value, 'dotted');
  assert.deepEqual([...styleSelectOf(ed).options].map((o) => o.value), ['solid', 'dashed', 'dotted', 'dash-dot'], 'no empty option: a map always has a style');

  colourOf(ed).value = '#112233'; change(colourOf(ed));
  assert.equal(seen.at(-1).lineColor, '#112233');
  widthOf(ed).value = '99'; change(widthOf(ed));
  assert.equal(seen.at(-1).lineWidth, 8, 'clamped');
  assert.equal(widthOf(ed).value, '8', 'and shown clamped');
  widthOf(ed).value = 'abc'; change(widthOf(ed));
  assert.equal(seen.at(-1).lineWidth, 1.5, 'junk is the default');
  styleSelectOf(ed).value = 'dash-dot'; change(styleSelectOf(ed));
  assert.equal(seen.at(-1).lineStyle, 'dash-dot');

  // Default puts the colour back to the theme's and nothing else.
  buttonNamed(ed, 'Default').click();
  assert.deepEqual([seen.at(-1).lineColor, seen.at(-1).lineStyle], ['', 'dash-dot']);
  assert.ok(buttonNamed(ed, 'Default').disabled, 'nothing to reset once it is the theme\'s');
  assert.match(ed.textContent, /Theme colour/);
  buttonNamed(ed, 'Reset the lines').click();
  assert.deepEqual([seen.at(-1).lineColor, seen.at(-1).lineWidth, seen.at(-1).lineStyle], ['', 1.5, 'solid']);

  // Everything has a label for a screen reader, and keyboard reach (native controls).
  for (const control of ed.querySelectorAll('input, select')) {
    const label = control.getAttribute('aria-label') || (control.id && ed.querySelector(`label[for="${control.id}"]`)?.textContent) || control.closest('label')?.textContent;
    assert.ok(label && label.trim(), `${control.outerHTML.slice(0, 60)} has a label`);
  }
  assert.ok(ed.querySelector('fieldset > legend').textContent.includes('Line style'));
});

test('the map options without a colour still show the theme\'s default', () => {
  const ed = mapConfigEditor({});
  assert.deepEqual([ed.value.lineColor, ed.value.lineWidth, ed.value.lineStyle], ['', 1.5, 'solid']);
  assert.match(ed.textContent, /Theme colour/);
  assert.ok(buttonNamed(ed, 'Default').disabled);
});

test('a link\'s style controls follow the map until a field is chosen, and Reset hands it all back', () => {
  const calls = [];
  const ed = linkStyleEditor({ parentName: 'Gateway', value: null, defaults: { color: '', width: 2, dash: 'dashed' }, onChange: (s) => calls.push(s) });
  assert.equal(ed.querySelector('legend').textContent, 'Link to Gateway');
  assert.equal(ed.value, null);
  assert.equal(widthOf(ed).value, '');
  assert.equal(widthOf(ed).getAttribute('placeholder'), '2', 'the map\'s thickness shows through');
  assert.deepEqual([...styleSelectOf(ed).options].map((o) => o.value), ['', 'solid', 'dashed', 'dotted', 'dash-dot']);
  assert.equal(styleSelectOf(ed).options[0].textContent, 'Like the map');
  assert.equal([...styleSelectOf(ed).options].find((o) => o.value === 'dashed').textContent, 'Dashed (map)');
  assert.match(ed.textContent, /Same as the map/);

  widthOf(ed).value = '3'; change(widthOf(ed));
  assert.deepEqual(calls.at(-1), { color: '', width: 3, dash: '' }, 'only what was chosen is set');
  colourOf(ed).value = '#3b82f6'; change(colourOf(ed));
  assert.deepEqual(calls.at(-1), { color: '#3b82f6', width: 3, dash: '' });
  styleSelectOf(ed).value = 'solid'; change(styleSelectOf(ed));
  assert.deepEqual(calls.at(-1), { color: '#3b82f6', width: 3, dash: 'solid' }, 'solid is a choice: it overrides a dashed map');
  assert.deepEqual(ed.value, { color: '#3b82f6', width: 3, dash: 'solid' });
  widthOf(ed).value = '0.2'; change(widthOf(ed));
  assert.equal(calls.at(-1).width, 1, 'too thin is the thinnest');
  widthOf(ed).value = ''; change(widthOf(ed));
  assert.equal(calls.at(-1).width, 0, 'blank follows the map again');

  buttonNamed(ed, 'Default').click();
  assert.equal(calls.at(-1).color, '');
  buttonNamed(ed, 'Reset').click();
  assert.equal(calls.at(-1), null, 'a link with no choices has no style');
  assert.equal(ed.value, null);
  assert.equal(widthOf(ed).value, '');
  assert.equal(styleSelectOf(ed).value, '');
  ed.setParentName('Switch');
  assert.equal(ed.querySelector('legend').textContent, 'Link to Switch');
  ed.setParentName('');
  assert.equal(ed.querySelector('legend').textContent, 'Link style');
});

test('a stored #rgb or #rrggbbaa colour is shown, and kept unless another is picked', () => {
  const calls = [];
  const c = lineStyleControls({ value: { color: '#f80' }, inherit: true, onChange: (v) => calls.push(v) });
  assert.equal(colourOf(c.el).value, '#ff8800');
  assert.match(c.el.textContent, /#f80/);
  widthOf(c.el).value = '2'; change(widthOf(c.el));
  assert.equal(calls.at(-1).color, '#f80', 'changing something else leaves the stored colour exactly as it was');
  const rgba = lineStyleControls({ value: { color: '#11223380' }, inherit: true });
  assert.equal(colourOf(rgba.el).value, '#112233');
  assert.equal(rgba.value.color, '#11223380');
  const off = lineStyleControls({ value: { color: '#f80' }, inherit: true, disabled: true });
  for (const el of off.el.querySelectorAll('input, select, button')) assert.ok(el.disabled);
  // A link whose style says something untrustworthy shows nothing of it.
  const junk = lineStyleControls({ value: { color: 'red;x', width: 'x', dash: 'wavy' }, inherit: true });
  assert.deepEqual(junk.value, { color: '', width: 0, dash: '' });
});

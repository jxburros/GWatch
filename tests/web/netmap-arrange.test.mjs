// The network map's newer working parts, which are pure and tested directly:
// how thick a line is at a zoom, what an outage (real or simulated) reaches,
// where a node is drawn once the map has been arranged by hand, tidying up,
// folding a cluster away, and the biggest risks; and the drawing's node look
// — its own colour with a status light beside it, one or the other.

import { freshRoot } from './dom.mjs';
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  buildForest, layoutMap, renderNetworkMap, strokeScale, edgeStyle, edgeLook, normalizeMapConfig, LINE_WIDTH, LINE_ZOOM_MAX,
  mapStates, edgeState, descendantsOf, ancestorsOf, biggestRisks, arrangePositions, carriedBy, tidyUnpinned, toggleFolded, unpin,
} from '../../web/netmap.js';
import { bigNetwork } from './big-network.mjs';

const node = (id, name, parent = null, extra = {}) => ({ id, name, host: `10.0.0.${id}`, dependsOnNodeId: parent, groups: [], tags: [], ...extra });
// A router, a switch with three devices, and a Pi-hole.
const HOME = [node(1, 'Router'), node(2, 'Switch', 1), node(3, 'Pi-hole', 1), node(4, 'NAS', 2), node(5, 'Camera', 2), node(6, 'Printer', 2)];
const entriesOf = (nodes, status = {}, extra = {}) => nodes.map((n) => ({ node: n, status: status[n.id] || 'up', ...(extra[n.id] || {}) }));
const byId = (entries) => new Map(entries.map((e) => [String(e.node.id), e]));

/* ---------- Line thickness ---------- */

test('a line is its thickness in screen pixels however far out the map is, and grows when zoomed in, up to three times', () => {
  // A 250-node map fitted to the screen: a tenth of a pixel per unit. The
  // line is drawn ten units wide, so it is still its thickness on screen.
  assert.equal(strokeScale(0.1), 10);
  assert.equal(strokeScale(1), 1, 'life size: units are pixels');
  assert.equal(strokeScale(2) * 2, 2, 'twice life size: twice as thick on screen');
  assert.equal(strokeScale(10) * 10, LINE_ZOOM_MAX, 'and no more than three times');
  assert.equal(strokeScale(0), 1, 'a map not on screen yet is drawn as is');
  assert.equal(strokeScale(NaN), 1);
  // The thickness and the dashes scale together, so a dashed line stays dashed.
  const look = edgeLook(normalizeMapConfig({ lineWidth: 3, lineStyle: 'dashed' }), null);
  assert.equal(edgeStyle(look, 1), 'stroke-width:3;stroke-dasharray:12 8;stroke-linecap:butt');
  assert.equal(edgeStyle(look, 10), 'stroke-width:30;stroke-dasharray:120 80;stroke-linecap:butt');
  assert.equal(LINE_WIDTH.default, 2);
});

test('the drawn lines take their thickness from the zoom (in jsdom, which has no layout, life size)', () => {
  const root = freshRoot();
  renderNetworkMap(root, entriesOf(HOME), { lineWidth: 4 }, { interactive: true });
  for (const e of root.querySelectorAll('.netmap-edge')) assert.match(e.getAttribute('style'), /^stroke-width:4/);
  assert.equal(root.querySelector('.netmap-svg').style.getPropertyValue('--netmap-k'), '1');
});

/* ---------- Outages, real and simulated ---------- */

test('a real outage reaches what the service says it affects, and no further than what is failing', () => {
  const f = buildForest(HOME);
  // The switch is down; the NAS is failing because of it, the camera is fine.
  const e = byId(entriesOf(HOME, { 2: 'down', 4: 'down', 5: 'up' }, { 4: { affectedBy: 'Switch' } }));
  const s = mapStates(f, e);
  assert.equal(s.get('2').status, 'down');
  assert.deepEqual(s.get('4').cause, { kind: 'down', by: '2', depth: 1, sim: false });
  assert.equal(s.get('5').cause, null, 'the camera answers: it is not cut off');
  assert.equal(edgeState(s, 2, 4), 'cut', 'every line out of a node that is down is cut');
  assert.equal(edgeState(s, 2, 5), 'cut');
  assert.equal(edgeState(s, 1, 2), '', 'the line into the node that is down is not');
  // A paused node is drawn paused.
  const paused = mapStates(f, byId(entriesOf([...HOME.slice(0, 5), { ...HOME[5], enabled: false }])));
  assert.equal(paused.get('6').status, 'paused');
});

test('a simulated failure cuts off everything below it, one ring at a time, and changes nothing else', () => {
  const nodes = [...HOME, node(7, 'Backup', 4)];
  const f = buildForest(nodes);
  const e = byId(entriesOf(nodes));
  const start = mapStates(f, e, { failed: new Map([['2', 'down']]), depth: 0 });
  assert.equal(start.get('2').status, 'down');
  assert.equal(start.get('2').sim, true);
  assert.equal(start.get('4').cause, null, 'not spread yet');
  assert.equal(edgeState(start, 2, 4, { depth: 0 }), '');
  const one = mapStates(f, e, { failed: new Map([['2', 'down']]), depth: 1 });
  assert.equal(one.get('4').cause.depth, 1);
  assert.equal(one.get('7').cause, null, 'two links down: not yet');
  assert.equal(edgeState(one, 2, 4, { depth: 1 }), 'cut');
  const all = mapStates(f, e, { failed: { 2: 'down' } });
  for (const id of ['4', '5', '6', '7']) assert.equal(all.get(id).cause?.by, '2', `${id} is cut off by the switch`);
  assert.equal(edgeState(all, 4, 7), 'affected', 'further in, affected');
  assert.equal(all.get('3').cause, null, 'the Pi-hole hangs off the router, not the switch');
  assert.equal(e.get('2').status, 'up', 'the entries are untouched');
  // Maintenance makes what is below it quiet.
  const maint = mapStates(f, e, { failed: { 2: 'maintenance' } });
  assert.equal(maint.get('4').cause.kind, 'maintenance');
  assert.equal(edgeState(maint, 2, 4), 'quiet');
  assert.equal(edgeState(maint, 4, 7), 'quiet');
});

test('descendants, ancestors and the biggest risks', () => {
  const f = buildForest(bigNetwork(250));
  assert.equal(descendantsOf(f, 1).length, 244, 'everything linked hangs off the gateway');
  assert.deepEqual(ancestorsOf(f, 4).map(Number), [2, 1], 'a switch: the core, then the gateway');
  const risks = biggestRisks(f, null, 4);
  assert.deepEqual(risks.map((r) => r.node.name).slice(0, 2), ['Gateway', 'Core switch']);
  assert.ok(risks.every((r, i) => !i || risks[i - 1].below >= r.below), 'most first');
  assert.deepEqual(biggestRisks(buildForest([node(1, 'Alone')])), []);
});

/* ---------- Arranging by hand ---------- */

const CELL_BOX = { w: 148, h: 32 };
function laidOut(nodes) {
  const f = buildForest(nodes);
  const l = layoutMap(f, { orientation: 'radial', node: CELL_BOX, spacing: 24, reach: 40 });
  return { f, l };
}

test('a pinned node is drawn where it was put, and what it carries comes with it, turned to face away from the line in', () => {
  const { f, l } = laidOut(HOME);
  const none = arrangePositions(f, l.pos, l.cell, null);
  for (const [id, p] of l.pos) assert.deepEqual([none.get(id).x, none.get(id).y], [p.x, p.y], `${id}: unarranged is as laid out`);

  // Put the switch somewhere else: it is pinned there, its devices keep their
  // distances from it, and its ring still opens away from the router.
  const at = { x: 30, y: -20 };
  const arranged = { positions: { 2: { ...at, pinned: true } } };
  const p = arrangePositions(f, l.pos, l.cell, arranged);
  assert.deepEqual([p.get('2').x, p.get('2').y], [at.x * l.cell.w, at.y * l.cell.h]);
  assert.equal(p.get('2').pinned, true);
  const cells = (q) => ({ x: q.x / l.cell.w, y: q.y / l.cell.h });
  const dist = (a, b) => Math.hypot(cells(a).x - cells(b).x, cells(a).y - cells(b).y);
  for (const k of ['4', '5', '6']) {
    assert.equal(p.get(k).pinned, false);
    assert.ok(Math.abs(dist(p.get(k), p.get('2')) - dist(l.pos.get(k), l.pos.get('2'))) < 1e-9, `${k} keeps its distance from the switch`);
  }
  const angle = (a, b) => Math.atan2(cells(b).y - cells(a).y, cells(b).x - cells(a).x);
  const turn = (q) => angle(q.get('1'), q.get('2'));
  const before = angle(l.pos.get('2'), l.pos.get('4')) - turn(l.pos);
  const after = angle(p.get('2'), p.get('4')) - turn(p);
  assert.ok(Math.abs(Math.atan2(Math.sin(after - before), Math.cos(after - before))) < 1e-9, 'the NAS sits the same way round, seen from the line in');
  assert.equal(p.get('3').x, l.pos.get('3').x, 'the Pi-hole did not move');

  assert.deepEqual(carriedBy(f, 2, arranged).sort(), ['4', '5', '6']);
  assert.deepEqual(carriedBy(f, 1, { positions: { 2: { x: 0, y: 0, pinned: true } } }).sort(), ['3'], 'a pinned node is not carried by its parent');
});

test('unpinning puts a node back next to its parent, and folding is a toggle', () => {
  const { f } = laidOut(HOME);
  const a = { positions: { 2: { x: 5, y: 5, pinned: true }, 4: { x: 9, y: 9 }, 3: { x: 1, y: 1, pinned: true } }, collapsed: [] };
  const u = unpin(f, a, 2);
  assert.deepEqual(Object.keys(u.positions).sort(), ['3'], 'the switch and the place kept for what it carried are gone; another pin stays');
  assert.deepEqual(a.positions[2], { x: 5, y: 5, pinned: true }, 'the arrangement given is not changed');
  const folded = toggleFolded(a, 2);
  assert.deepEqual(folded.collapsed, [2]);
  assert.deepEqual(toggleFolded(folded, '2').collapsed, []);
  assert.deepEqual(toggleFolded(folded, 2, true).collapsed, [2], 'fold an already folded cluster: still folded');
});

test('tidying up forgets the places nobody pinned, keeps the pins, and turns a group off whatever it lands on', () => {
  const { f, l } = laidOut(HOME);
  // The switch pinned right on top of the Pi-hole's place: its devices would
  // land on the router's cluster unless turned.
  const pinAt = { x: l.pos.get('3').x / l.cell.w + 1.2, y: l.pos.get('3').y / l.cell.h };
  const before = { positions: { 2: { ...pinAt, pinned: true }, 5: { x: 99, y: 99 } }, collapsed: [4] };
  const after = tidyUnpinned(f, l.pos, l.cell, before, { node: CELL_BOX, spacing: 24 });
  assert.deepEqual(after.positions[2], { ...pinAt, pinned: true }, 'the pin stays');
  assert.ok(!after.positions[5] || after.positions[5].x !== 99, 'the stale place is gone');
  assert.deepEqual(after.collapsed, [4], 'folding is not touched');
  const drawn = arrangePositions(f, l.pos, l.cell, after);
  const clash = (a, b) => Math.abs(a.x - b.x) < CELL_BOX.w && Math.abs(a.y - b.y) < CELL_BOX.h;
  for (const k of ['4', '5', '6']) for (const o of ['1', '3']) assert.ok(!clash(drawn.get(k), drawn.get(o)), `${k} is clear of ${o}`);
});

/* ---------- The drawing ---------- */

const pod = (root, id) => root.querySelector(`.netmap-node[data-node="${id}"] .netmap-pod`);
const boxFill = (root, id) => root.querySelector(`.netmap-node[data-node="${id}"] .netmap-box`).getAttribute('style') || '';

test('a node shows its own colour with a status light beside it, or only one of them, as chosen', () => {
  const nodes = HOME.map((n) => (n.id === 2 ? { ...n, color: '#1f4e8c' } : n));
  const entries = entriesOf(nodes, { 2: 'down' });
  const root = freshRoot();
  const map = renderNetworkMap(root, entries, {}, {});
  assert.ok(root.querySelector('.netmap-svg').classList.contains('fill-both'));
  assert.ok(pod(root, 2), 'a status box');
  assert.ok(root.querySelector('.netmap-node[data-node="2"] .netmap-light'), 'with a light in it');
  assert.ok(root.querySelector('.netmap-node[data-node="2"]').classList.contains('status-down'));
  assert.match(boxFill(root, 2), /fill:#1f4e8c/, 'and the node\'s own colour fills the box');
  assert.match(root.querySelector('.netmap-node[data-node="2"] .netmap-name').getAttribute('style'), /fill:#ffffff/, 'white text on dark blue');

  map.update(entries, { nodeFill: 'colour' });
  assert.ok(root.querySelector('.netmap-svg').classList.contains('fill-colour'));
  assert.equal(pod(root, 2), null, 'no status shown at all');
  assert.equal(root.querySelector('.netmap-node[data-node="2"] .netmap-dot'), null);
  assert.match(boxFill(root, 2), /fill:#1f4e8c/);

  map.update(entries, { nodeFill: 'status' });
  assert.equal(pod(root, 2), null);
  assert.equal(boxFill(root, 2), '', 'the box is left to the stylesheet, which tints it by status');
  assert.ok(root.querySelector('.netmap-node[data-node="2"] .netmap-dot'), 'a status dot');
});

test('a folded cluster hides what is in it and says how much, in the colour of the worst of it', () => {
  const root = freshRoot();
  const entries = entriesOf(HOME, { 5: 'down' });
  const map = renderNetworkMap(root, entries, {}, { arranged: { collapsed: [2] } });
  assert.equal(root.querySelectorAll('.netmap-node').length, 3, 'router, switch and Pi-hole');
  const fold = root.querySelector('.netmap-node[data-node="2"] .netmap-fold');
  assert.equal(fold.textContent, '+3');
  assert.ok(fold.classList.contains('status-down'), 'the camera in it is down');
  assert.match(root.querySelector('.netmap-svg').getAttribute('aria-label'), /3 nodes folded away/);
  assert.match(root.querySelector('a[aria-label^="Switch,"]').getAttribute('aria-label'), /3 nodes folded into it/);
  assert.match(root.querySelector('.netmap-legend').textContent, /3 nodes folded into 1 cluster/);
  assert.match(root.querySelector('.netmap-list').textContent, /Camera/, 'the list still has everything');
  map.setArranged({ collapsed: [] });
  assert.equal(root.querySelectorAll('.netmap-node').length, 6);
});

test('choosing a node lights up what it hangs from and what hangs from it, unless the map is set not to', () => {
  const root = freshRoot();
  const entries = entriesOf(HOME);
  let chosen = 'unset';
  const map = renderNetworkMap(root, entries, {}, { interactive: true, onSelect: (n) => { chosen = n ? n.id : null; } });
  root.querySelector('a[aria-label^="Switch,"]').dispatchEvent(new window.MouseEvent('click', { bubbles: true, cancelable: true }));
  assert.equal(chosen, 2);
  const svgEl = root.querySelector('.netmap-svg');
  assert.ok(svgEl.classList.contains('has-path'));
  const on = [...root.querySelectorAll('.netmap-node.on-path')].map((g) => g.getAttribute('data-node')).sort();
  assert.deepEqual(on, ['1', '2', '4', '5', '6'], 'the router above, the switch and its devices; not the Pi-hole');
  assert.ok(root.querySelector('.netmap-edge[data-from="2"][data-to="4"]').classList.contains('on-path'));
  assert.ok(!root.querySelector('.netmap-edge[data-from="1"][data-to="3"]').classList.contains('on-path'));
  map.update(entries, { highlightPath: false });
  assert.ok(!root.querySelector('.netmap-svg').classList.contains('has-path'));
});

test('a simulation draws on the map and says so to a screen reader', () => {
  const root = freshRoot();
  const map = renderNetworkMap(root, entriesOf(HOME), {}, {});
  map.setSim({ failed: new Map([['2', 'down']]) });
  const sw = root.querySelector('.netmap-node[data-node="2"]');
  assert.ok(sw.classList.contains('sim-failed') && sw.classList.contains('status-down'));
  assert.match(root.querySelector('a[aria-label^="Switch,"]').getAttribute('aria-label'), /Down \(simulated\)/);
  assert.match(root.querySelector('a[aria-label^="NAS,"]').getAttribute('aria-label'), /cut off while Switch is down \(simulated\)/);
  assert.ok(root.querySelector('.netmap-edge[data-from="2"][data-to="4"]').classList.contains('netmap-edge-sim'));
  map.setSim(null);
  assert.equal(root.querySelectorAll('.sim-failed').length, 0);
});

test('addresses can be left off everywhere, for showing the map to people', () => {
  const root = freshRoot();
  const map = renderNetworkMap(root, entriesOf(HOME), { labels: 'name-host' }, {});
  assert.ok(root.querySelector('.netmap-host'));
  assert.match(root.querySelector('a[aria-label^="NAS,"]').getAttribute('aria-label'), /10\.0\.0\.4/);
  map.setHideHosts(true);
  assert.equal(root.querySelector('.netmap-host'), null);
  assert.doesNotMatch(root.querySelector('a[aria-label^="NAS,"]').getAttribute('aria-label'), /10\.0\.0\.4/);
});

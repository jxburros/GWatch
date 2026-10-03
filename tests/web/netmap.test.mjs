// The network map (web/netmap.js): the dependency forest, its layout and
// which nodes a filtered map shows are pure and tested directly; the drawing
// is checked for the parts a person relies on — a node per node, a line per
// dependency, the down colour on lines out of a node that is down.

import { freshRoot } from './dom.mjs';
import test from 'node:test';
import assert from 'node:assert/strict';
import { buildForest, layoutMap, mapEntries, normalizeMapConfig, renderNetworkMap } from '../../web/netmap.js';

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

  const radial = layoutMap(buildForest(NODES.slice(0, 5)), { orientation: 'radial' });
  assert.equal(radial.pos.get('1').level, 0, 'one tree: its root is the centre');
  assert.equal(radial.pos.get('2').level, 1);
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

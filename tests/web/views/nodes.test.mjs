// Smoke test for web/views/nodes.js: does the node list render one row per
// mock node, with the right count in the filter band, against the mock API.

import '../dom.mjs';
import '../../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as nodesView from '../../../web/views/nodes.js';
import { mountView } from '../view-harness.mjs';

test('nodes view lists every mock node and titles the page', async (t) => {
  const { root, ctx } = await mountView(nodesView, undefined, t);
  assert.equal(ctx.setTitleCalls.length, 1);
  assert.equal(ctx.setTitleCalls[0].title, 'Nodes');

  const mockNodes = window.__gwatchMock.nodes;
  const rows = root.querySelectorAll('.node-row');
  assert.equal(rows.length, mockNodes.length);

  const names = [...rows].map((r) => r.querySelector('.n-name a').textContent);
  for (const n of mockNodes) assert.ok(names.includes(n.name), `expected a row for ${n.name}`);

  const count = root.querySelector('.filter-count').textContent;
  assert.equal(count, `${mockNodes.length} nodes`);
});

test('the group filter matches any of a node\'s groups, and the others are chips on the row', async (t) => {
  // The NAS is in two groups in the mock data. Filtering on its second one
  // must find it, and the row must say which other groups it is in.
  const nas = window.__gwatchMock.nodes.find((n) => n.name === 'NAS');
  assert.ok(nas.groups.length > 1, 'the mock NAS should be in more than one group');
  const second = nas.groups[1];

  const { root } = await mountView(nodesView, { query: new URLSearchParams(`group=${second}`) }, t);
  const rows = [...root.querySelectorAll('.node-row')];
  const names = rows.map((r) => r.querySelector('.n-name a').textContent);
  assert.ok(names.includes(nas.name), `filtering on ${second} should find the NAS`);
  for (const other of window.__gwatchMock.nodes) {
    if (!(other.groups || []).includes(second)) assert.ok(!names.includes(other.name), `${other.name} is not in ${second}`);
  }

  // It is listed once, under the group that was filtered for, with its other
  // groups shown as chips beside its name.
  assert.equal(names.filter((n) => n === nas.name).length, 1);
  const section = root.querySelector(`.node-group[aria-label="${second}"]`);
  assert.ok(section, `expected a section headed ${second}`);
  const row = rows.find((r) => r.querySelector('.n-name a').textContent === nas.name);
  const chips = [...row.querySelectorAll('.n-groups .tag-group')].map((c) => c.textContent);
  assert.deepEqual(chips, nas.groups.filter((g) => g !== second));
});

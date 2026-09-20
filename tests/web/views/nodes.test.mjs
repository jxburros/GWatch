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

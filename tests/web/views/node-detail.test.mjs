// web/views/node-detail.js shows one node's header, its checks and its
// event/history sections. Picks a real node id out of the mock data rather
// than hard-coding one, so this does not depend on web/mock.js's id
// numbering.

import '../dom.mjs';
import '../../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as nodeDetailView from '../../../web/views/node-detail.js';
import { mountView, settle } from '../view-harness.mjs';

test('node detail view renders the node\'s header and its checks', async (t) => {
  const gateway = window.__gwatchMock.nodes.find((n) => n.name === 'Gateway');
  const { root, ctx } = await mountView(nodeDetailView, { params: { id: String(gateway.id) } }, t);
  assert.equal(ctx.setTitleCalls[0].title, 'Gateway');

  const cards = root.querySelectorAll('.check-card');
  assert.equal(cards.length, gateway.checks.length);
  const cardNames = [...cards].map((c) => c.getAttribute('aria-label'));
  for (const c of gateway.checks) assert.ok(cardNames.includes(c.name));

  await settle();
});

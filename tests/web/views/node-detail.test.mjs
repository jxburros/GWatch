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

// #30: a ping run measures several packets, and the card reports the spread of
// them — not just the average. Which node carries the ping check is the mock's
// business, so this finds one rather than naming it.
test('a ping check card reports the run\'s spread and packet count', async (t) => {
  const { nodes, states } = window.__gwatchMock;
  // A ping check that is down has no round-trip times to spread, so this picks
  // one whose last run succeeded.
  const node = nodes.find((n) => (n.checks || []).some((c) => c.type === 'ping' && states[c.id]?.status === 'up'));
  const ping = node.checks.find((c) => c.type === 'ping');
  const { root } = await mountView(nodeDetailView, { params: { id: String(node.id) } }, t);

  const card = [...root.querySelectorAll('.check-card')].find((c) => c.getAttribute('aria-label') === ping.name);
  assert.ok(card, 'the ping check has a card');
  const labels = [...card.querySelectorAll('.stat-label')].map((e) => e.textContent);
  for (const want of ['Avg RTT', 'Min RTT', 'Max RTT', 'Jitter', 'Std deviation', 'Packet loss', 'Packets received']) {
    assert.ok(labels.includes(want), `${want} is on the card (got ${labels.join(', ')})`);
  }

  await settle();
});

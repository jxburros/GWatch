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

test('node detail shows an snmp check\'s readings once the result is inspected', async (t) => {
  const node = window.__gwatchMock.nodes.find((n) => n.checks.some((c) => c.type === 'snmp'));
  const check = node.checks.find((c) => c.type === 'snmp');
  const { root } = await mountView(nodeDetailView, { params: { id: String(node.id) } }, t);

  const card = [...root.querySelectorAll('.check-card')].find((c) => c.getAttribute('aria-label') === check.name);
  assert.ok(card, 'the snmp check has a card');
  const inspect = [...card.querySelectorAll('button')].find((b) => b.textContent.includes('Inspect last result'));
  inspect.click();
  await settle();

  const table = [...root.querySelectorAll('.inspector table.table')].find((tb) => tb.textContent.includes('Verdict'));
  assert.ok(table, 'the inspector shows a readings table');
  const text = table.textContent;
  for (const o of check.config.snmpOids) {
    assert.ok(text.includes(o.name), `${o.name} is listed`);
    assert.ok(text.includes(o.oid), `${o.oid} is listed`);
  }
  // The text reading is shown as it arrived and carries no verdict.
  assert.ok(text.includes('Reported as text'), 'a non-numeric reading says so');

  await settle();
});

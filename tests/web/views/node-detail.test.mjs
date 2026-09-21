// web/views/node-detail.js shows one node's header, its checks and its
// event/history sections. Picks a real node id out of the mock data rather
// than hard-coding one, so this does not depend on web/mock.js's id
// numbering.

import '../dom.mjs';
import '../../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as nodeDetailView from '../../../web/views/node-detail.js';
import { mountView, settle, waitFor } from '../view-harness.mjs';

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

// #55: a json check that records its value is charted on the node page under
// its metric name, with a CSV link, exactly as an SNMP reading is.
test('node detail charts a json check\'s recorded value', async (t) => {
  const node = window.__gwatchMock.nodes.find((n) => n.checks.some((c) => c.type === 'json' && c.config.jsonRecord));
  const check = node.checks.find((c) => c.type === 'json' && c.config.jsonRecord);
  const { root } = await mountView(nodeDetailView, { params: { id: String(node.id) } }, t);

  const history = root.querySelector('section[aria-label="History"]');
  const section = await waitFor(() => [...history.querySelectorAll('.section-title')].find((el) => el.textContent === `${check.name} — recorded value`)?.parentElement);
  assert.ok(section.textContent.includes(`${check.config.jsonMetric} (${check.config.jsonUnit})`), 'the metric is named with its unit');
  assert.ok(section.querySelector('canvas'), 'and drawn as a chart');
  const csv = section.querySelector('a[download]');
  assert.ok(csv, 'with a CSV link');
  assert.ok(csv.getAttribute('href').includes(`metric=${encodeURIComponent(check.config.jsonMetric)}`), 'that asks for the metric by name');

  await settle();
});

// #60: a hardware check's metrics are charted from its own results, grouped
// by family — one axis for the percentages, one line per filesystem — and
// each line can be exported by its key.
test('node detail charts a hardware check\'s metrics by family', async (t) => {
  const node = window.__gwatchMock.nodes.find((n) => n.checks.some((c) => c.type === 'system'));
  const check = node.checks.find((c) => c.type === 'system');
  const { root } = await mountView(nodeDetailView, { params: { id: String(node.id) } }, t);

  const history = root.querySelector('section[aria-label="History"]');
  const section = await waitFor(() => [...history.querySelectorAll('.section-title')].find((el) => el.textContent === `${check.name} — hardware metrics`)?.parentElement);
  const groups = [...section.querySelectorAll('[data-group]')].map((g) => g.dataset.group);
  assert.ok(groups.includes('usage'), 'processor, memory and swap share one chart');
  assert.ok(groups.includes('disk'), 'the filesystems have a chart');
  assert.ok(groups.includes('net'), 'so do the interfaces');
  assert.ok(groups.includes('diskbusy'), 'disk busy % is kept off the bytes/s axis');
  assert.ok(section.querySelector('[data-group="disk"] canvas'), 'and they are drawn');
  assert.ok(section.querySelector('[data-group="disk"] button, [data-group="disk"] a[download]'), 'the disk chart has an export control');
  assert.ok(section.querySelector('[data-group="usage"]').textContent.includes('Processor, memory and swap (%)'), 'the group is named with its unit');

  // The hardware check is not in the latency chart: it measures a machine,
  // not a round trip — but the node's other checks still are.
  assert.ok([...history.querySelectorAll('.section-title')].some((el) => el.textContent === 'Latency / response time'));
  await settle();
});

test('node detail shows a hardware check\'s per-metric verdicts in the inspector', async (t) => {
  const node = window.__gwatchMock.nodes.find((n) => n.checks.some((c) => c.type === 'system'));
  const check = node.checks.find((c) => c.type === 'system');
  const { root } = await mountView(nodeDetailView, { params: { id: String(node.id) } }, t);

  const card = root.querySelector(`section.check-card[aria-label="${check.name}"]`);
  assert.ok(card, 'the hardware check has a card');
  card.querySelector('button[aria-expanded]').click();
  await settle();
  const table = await waitFor(() => root.querySelector(`section.check-card[aria-label="${check.name}"] table.metric-results`));
  const rows = [...table.querySelectorAll('tbody tr')];
  assert.ok(rows.length >= 6, `one row per metric, got ${rows.length}`);
  const media = rows.find((r) => r.dataset.metric === 'disk:/srv/media');
  assert.ok(media, 'the media disk has a row');
  assert.ok(media.textContent.includes('warning'), 'and its own verdict');
  assert.ok(rows.find((r) => r.dataset.metric === 'cpu').textContent.includes('within thresholds'), 'while the processor is fine');
});

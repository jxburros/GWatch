// The Matter check (#45): its form in the node editor — the readings table,
// "Load devices" and commissioning against the mock Matter server — and its
// readings in the node page's inspector and charts.

import '../dom.mjs';
import '../../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as nodeEditorView from '../../../web/views/node-editor.js';
import * as nodeDetailView from '../../../web/views/node-detail.js';
import { namedMetrics } from '../../../web/chart-config.js';
import { MATTER_READING_KINDS, matterKind } from '../../../web/views/node-fields.js';
import { CHECK_TYPES } from '../../../web/components.js';
import { mountView, settle, waitFor } from '../view-harness.mjs';

Element.prototype.scrollIntoView = Element.prototype.scrollIntoView || function scrollIntoView() {};

const matterNode = () => window.__gwatchMock.nodes.find((n) => n.checks.some((c) => c.type === 'matter'));
const matterCheck = () => matterNode().checks.find((c) => c.type === 'matter');
const cardOf = (root, check) => [...root.querySelectorAll('.editor-check')].find((c) => c.getAttribute('aria-label') === `${check.name} check`);
const button = (el, text) => [...el.querySelectorAll('button')].find((b) => b.textContent.includes(text));

test('Matter is a check type, with the standard readings the server knows', () => {
  assert.ok(CHECK_TYPES.some((t) => t.value === 'matter'), 'Matter is offered in the type picker');
  // The same seven standard kinds as internal/matter.Standards, plus a plain
  // attribute.
  assert.deepEqual(MATTER_READING_KINDS.map((k) => k.value), ['temperature', 'humidity', 'onoff', 'contact', 'occupancy', 'illuminance', 'battery', 'attribute']);
  assert.equal(matterKind('battery').defaults.critBelow, 10);
});

test('node editor renders a Matter check\'s server, node and readings', async (t) => {
  const node = matterNode(), check = matterCheck();
  const { root } = await mountView(nodeEditorView, { params: { id: String(node.id) } }, t);
  const card = cardOf(root, check);
  assert.ok(card, 'the matter check has a card of its own');

  const server = [...card.querySelectorAll('.field')].find((f) => f.textContent.startsWith('Matter server')).querySelector('input');
  assert.equal(server.value, '', 'no address of its own');
  assert.match(server.placeholder, /Uses node host \(ws:\/\/homeassistant\.local:5580\/ws\)/);
  const nodeId = [...card.querySelectorAll('.field')].find((f) => f.textContent.startsWith('Node id')).querySelector('input');
  assert.equal(nodeId.value, String(check.config.matterNodeId));

  const rows = card.querySelectorAll('table.table tbody tr');
  assert.equal(rows.length, check.config.matterReadings.length, 'one row per reading');
  const first = rows[0];
  assert.equal(first.querySelector('select').value, 'temperature');
  // A standard reading's cluster and attribute are implied, so those boxes
  // are disabled.
  const [, cluster, attribute] = first.querySelectorAll('input[type="number"]');
  assert.ok(cluster.disabled && attribute.disabled, 'cluster and attribute are implied by the kind');
  assert.ok(button(card, 'Load devices'), 'a Load devices button');
  assert.ok(card.textContent.includes('Add a device to the Matter server'), 'the commissioning panel');
});

test('Load devices lists the server\'s nodes and adds the ticked readings', async (t) => {
  const node = matterNode(), check = matterCheck();
  const { root } = await mountView(nodeEditorView, { params: { id: String(node.id) } }, t);
  const card = cardOf(root, check);
  const before = card.querySelectorAll('table.table tbody tr').length;

  button(card, 'Load devices').click();
  const modal = await waitFor(() => document.querySelector('.modal'));
  assert.ok(card.textContent.includes('Connected to ws://homeassistant.local:5580/ws'), 'the server the node host stands in for answered');
  const select = modal.querySelector('select');
  assert.equal(select.value, String(check.config.matterNodeId), 'the check\'s own node is preselected');
  const rows = [...modal.querySelectorAll('tbody tr')];
  // Readings already on the check are not offered twice.
  const temp = rows.find((r) => r.textContent.includes('Living room sensor') && r.textContent.includes('Temperature'));
  assert.ok(temp.querySelector('input').disabled, 'temperature is already added');
  assert.ok(temp.textContent.includes('21.4 °C'), 'the current value is shown');

  const motion = rows.find((r) => r.textContent.includes('Hall motion') && r.textContent.includes('Occupancy'));
  motion.querySelector('input').checked = true;
  button(modal, 'Add readings').click();
  const after = card.querySelectorAll('table.table tbody tr');
  assert.equal(after.length, before + 1);
  const added = after[after.length - 1];
  assert.equal(added.querySelector('select').value, 'occupancy');
  assert.equal(added.querySelector('input[type="number"]').value, '5', 'on the endpoint it was listed under');
  const name = [...added.querySelectorAll('input[type="text"]')][0];
  assert.equal(name.value, 'Hall motion occupied', 'named after the bridged device');
});

test('picking a different node replaces the readings and sets the node id', async (t) => {
  const node = matterNode(), check = matterCheck();
  const { root } = await mountView(nodeEditorView, { params: { id: String(node.id) } }, t);
  const card = cardOf(root, check);
  button(card, 'Load devices').click();
  const modal = await waitFor(() => document.querySelector('.modal'));
  const select = modal.querySelector('select');
  select.value = '7';
  select.dispatchEvent(new window.Event('change'));
  assert.ok(modal.textContent.includes('replaces the ones it has'), 'the switch is announced');
  const plug = [...modal.querySelectorAll('tbody tr')].find((r) => r.textContent.includes('On / off'));
  plug.querySelector('input').checked = true;
  button(modal, 'Add readings').click();

  const rows = card.querySelectorAll('table.table tbody tr');
  assert.equal(rows.length, 1);
  assert.equal(rows[0].querySelector('select').value, 'onoff');
  const nodeId = [...card.querySelectorAll('.field')].find((f) => f.textContent.startsWith('Node id')).querySelector('input');
  assert.equal(nodeId.value, '7');
});

test('a Matter server that is not running is explained under the button', async (t) => {
  const node = matterNode(), check = matterCheck();
  const { root } = await mountView(nodeEditorView, { params: { id: String(node.id) } }, t);
  const card = cardOf(root, check);
  const server = [...card.querySelectorAll('.field')].find((f) => f.textContent.startsWith('Matter server')).querySelector('input');
  server.value = 'ws://down.lan:5580/ws';
  server.dispatchEvent(new window.Event('input'));
  button(card, 'Load devices').click();
  await waitFor(() => card.querySelector('.snmp-test-fail'));
  assert.ok(card.textContent.includes('Could not load devices'));
  assert.ok(card.textContent.includes('connection refused'));
});

test('commissioning a device relays the pairing code and reports the new node', async (t) => {
  const node = matterNode(), check = matterCheck();
  const { root } = await mountView(nodeEditorView, { params: { id: String(node.id) } }, t);
  const card = cardOf(root, check);
  const panel = [...card.querySelectorAll('details')].find((d) => d.textContent.includes('Add a device to the Matter server'));
  const code = panel.querySelector('input[aria-label="Pairing code"]');
  code.value = 'not a code';
  button(panel, 'Add device').click();
  await waitFor(() => panel.querySelector('.snmp-test-fail'));
  assert.ok(panel.textContent.includes('not a Matter pairing code'));

  code.value = '3497-011-2332';
  button(panel, 'Add device').click();
  await waitFor(() => panel.querySelector('.snmp-test-ok'));
  assert.match(panel.textContent, /Added node \d+ — Nanoleaf Essentials Bulb/);
  assert.equal(code.value, '', 'the code is not left on the page');
  await settle();
  document.querySelector('.modal-backdrop')?.remove();
});

test('the editor refuses a Matter check with no node or duplicate names before saving', async (t) => {
  const node = matterNode(), check = matterCheck();
  const { root } = await mountView(nodeEditorView, { params: { id: String(node.id) } }, t);
  const card = cardOf(root, check);
  const names = [...card.querySelectorAll('table.table tbody tr')].map((r) => r.querySelectorAll('input[type="text"]')[0]);
  names[1].value = names[0].value;
  names[1].dispatchEvent(new window.Event('input'));
  button(root, 'Save changes').click();
  await settle();
  const again = cardOf(root, check);
  assert.ok(again.textContent.includes('both called'), 'the duplicate name is named');
});

test('node detail shows a Matter check\'s readings in the inspector and charts them', async (t) => {
  const node = matterNode(), check = matterCheck();
  const metrics = namedMetrics(check, null);
  assert.deepEqual(metrics.map((m) => m.name), check.config.matterReadings.map((r) => r.name));
  assert.equal(metrics[0].unit, '°C');

  const { root } = await mountView(nodeDetailView, { params: { id: String(node.id) } }, t);
  const card = [...root.querySelectorAll('.check-card')].find((c) => c.getAttribute('aria-label') === check.name);
  assert.ok(card, 'the matter check has a card');
  button(card, 'Inspect last result').click();
  await settle();
  const table = [...root.querySelectorAll('.inspector table.table')].find((tb) => tb.textContent.includes('Path'));
  assert.ok(table, 'the inspector shows the Matter readings table');
  assert.ok(table.textContent.includes('3/69/0'), 'with each attribute path');
  assert.ok(table.textContent.includes('shut'), 'a state as its word');
  assert.ok(root.textContent.includes('ws://homeassistant.local:5580/ws'), 'and the server it was read through');

  const history = root.querySelector('section[aria-label="History"]');
  const section = await waitFor(() => [...history.querySelectorAll('.section-title')].find((el) => el.textContent === `${check.name} — Matter readings`)?.parentElement);
  assert.ok(section.querySelector('canvas'), 'the readings are charted');
  await settle();
});

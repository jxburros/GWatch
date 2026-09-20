// web/views/node-editor.js in edit mode: loading an existing node populates
// the name field and one check card per existing check.

import '../dom.mjs';
import '../../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as nodeEditorView from '../../../web/views/node-editor.js';
import { mountView, settle } from '../view-harness.mjs';

test('node editor (edit mode) loads the node into the form', async (t) => {
  const gateway = window.__gwatchMock.nodes.find((n) => n.name === 'Gateway');
  const { root, ctx } = await mountView(nodeEditorView, { params: { id: String(gateway.id) } }, t);
  assert.equal(ctx.setTitleCalls[0].title, `Edit ${gateway.name}`);
  assert.equal(root.querySelector('.card[aria-label="Node"] input').value, gateway.name);
  assert.equal(root.querySelectorAll('.editor-check').length, gateway.checks.length);
});

test('node editor (new node) starts from a blank draft', async (t) => {
  const { root, ctx } = await mountView(nodeEditorView, { params: {}, query: new URLSearchParams() }, t);
  assert.equal(ctx.setTitleCalls[0].title, 'Add node');
  assert.equal(root.querySelector('.card[aria-label="Node"] input').value, '');
});

test('node editor renders the SNMP settings of an snmp check', async (t) => {
  const node = window.__gwatchMock.nodes.find((n) => n.checks.some((c) => c.type === 'snmp'));
  const check = node.checks.find((c) => c.type === 'snmp');
  const { root } = await mountView(nodeEditorView, { params: { id: String(node.id) } }, t);

  const card = [...root.querySelectorAll('.editor-check')].find((c) => c.getAttribute('aria-label') === `${check.name} check`);
  assert.ok(card, 'the snmp check has a card of its own');

  // The version and port controls, and the v2c community box rather than the
  // v3 user fields.
  const selects = [...card.querySelectorAll('select')].map((s) => s.value);
  assert.ok(selects.includes('2c'), 'the version select shows 2c');
  assert.ok([...card.querySelectorAll('input[type="password"]')].length >= 1, 'a community field is shown');

  // One table row per configured OID, with the OID and its name filled in.
  const rows = card.querySelectorAll('table.table tbody tr');
  assert.equal(rows.length, check.config.snmpOids.length);
  const oids = [...rows].map((r) => r.querySelector('input').value);
  for (const o of check.config.snmpOids) assert.ok(oids.includes(o.oid), `${o.oid} has a row`);

  // The presets dropdown offers the standard-MIB readings.
  const presets = [...card.querySelectorAll('select')].find((s) => s.querySelector('optgroup'));
  assert.ok(presets, 'a presets dropdown is offered');
  const labels = [...presets.querySelectorAll('option')].map((o) => o.textContent);
  assert.ok(labels.some((l) => l.includes('sysUpTime')), 'sysUpTime is among the presets');
  assert.ok(labels.some((l) => l.includes('ifOperStatus')), 'ifOperStatus is among the presets');
});

test('adding an snmp check starts it on v2c with an uptime reading', async (t) => {
  // The editor scrolls the new card into view, which jsdom does not
  // implement; the call is decoration, so a no-op is enough.
  Element.prototype.scrollIntoView = Element.prototype.scrollIntoView || function scrollIntoView() {};

  const { root } = await mountView(nodeEditorView, { params: {}, query: new URLSearchParams() }, t);
  // The type picker opens in a modal; choose SNMP by its label.
  root.querySelector('section[aria-label="Checks"] button').click();
  const picker = document.querySelector('.type-picker');
  const button = [...picker.querySelectorAll('button')].find((b) => b.textContent.startsWith('SNMP'));
  assert.ok(button, 'SNMP is offered as a check type');
  button.click();
  await settle();

  const card = root.querySelector('.editor-check');
  assert.ok(card, 'the new check has a card');
  const rows = card.querySelectorAll('table.table tbody tr');
  assert.equal(rows.length, 1, 'a new snmp check starts with one reading');
  assert.equal(rows[0].querySelector('input').value, '1.3.6.1.2.1.1.3.0');
  assert.ok([...card.querySelectorAll('select')].some((s) => s.value === '2c'), 'and on version 2c');
});

// web/views/node-editor.js in edit mode: loading an existing node populates
// the name field and one check card per existing check.

import '../dom.mjs';
import '../../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as nodeEditorView from '../../../web/views/node-editor.js';
import { mountView } from '../view-harness.mjs';

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

// #47: a ping check may override the global ping method, and #30 gave the
// packet count its help text. Both live in the ping check's own fields.
test('node editor offers the ping count and the ping method override', async (t) => {
  const node = window.__gwatchMock.nodes.find((n) => (n.checks || []).some((c) => c.type === 'ping'));
  const { root } = await mountView(nodeEditorView, { params: { id: String(node.id) } }, t);

  const card = [...root.querySelectorAll('.editor-check')]
    .find((el) => [...el.querySelectorAll('label, .field-label')].some((l) => l.textContent.includes('Packets per run')));
  assert.ok(card, 'the ping check card is on the page');
  assert.match(card.textContent, /default 4, max 20/);

  const select = [...card.querySelectorAll('select')].find((el) => [...el.options].some((o) => o.value === 'system'));
  assert.ok(select, 'the ping method override select is on the card');
  assert.deepEqual([...select.options].map((o) => o.value), ['', 'builtin', 'system']);
  assert.equal(select.value, ''); // no override stored, so it follows the global setting
});

test('the group field is a chip editor carrying every group the node is in', async (t) => {
  const nas = window.__gwatchMock.nodes.find((n) => n.name === 'NAS');
  assert.ok(nas.groups.length > 1, 'the mock NAS should be in more than one group');
  const { root } = await mountView(nodeEditorView, { params: { id: String(nas.id) } }, t);

  const groupField = [...root.querySelectorAll('.field')].find((f) => f.textContent.startsWith('Groups'));
  assert.ok(groupField, 'expected a Groups field');
  const chips = [...groupField.querySelectorAll('.chip-input .chip-item')].map((c) => c.textContent.trim());
  assert.deepEqual(chips, nas.groups);

  // The groups already in use are offered as suggestions.
  const options = [...groupField.querySelectorAll('datalist option')].map((o) => o.getAttribute('value'));
  for (const g of nas.groups) assert.ok(options.includes(g), `expected ${g} among the suggestions`);
});

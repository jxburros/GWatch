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

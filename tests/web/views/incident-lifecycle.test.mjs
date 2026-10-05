import '../dom.mjs';
import '../../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as view from '../../../web/views/incidents.js';
import { mountView, waitFor } from '../view-harness.mjs';
test('incidents default to lifecycle records and acknowledge with a note', async (t) => {
  const { root } = await mountView(view, undefined, t);
  assert.match(root.textContent, /Gateway/);
  [...root.querySelectorAll('button')].find((b) => b.textContent === 'Timeline and notes').click();
  const modal = await waitFor(() => document.querySelector('[role="dialog"]'));
  modal.querySelector('textarea').value = 'Investigating router';
  [...modal.querySelectorAll('button')].find((b) => b.textContent === 'Acknowledge').click();
  await waitFor(() => root.textContent.includes('acknowledged'));
  assert.match(root.textContent, /Acknowledged by local/);
});
test('viewer reports offer generation without admin delivery definitions', async (t) => {
  const { root } = await mountView(view, { isAdmin: false, query: new URLSearchParams('tab=reports') }, t);
  assert.match(root.textContent, /Open printable report/);
  assert.doesNotMatch(root.textContent, /Save schedules|Scheduled reports|forbidden/i);
});

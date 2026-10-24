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

test('a refused incident action says why and gives the button back', async (t) => {
  const { root } = await mountView(view, { query: new URLSearchParams('state=all') }, t);
  [...root.querySelectorAll('button')].find((b) => b.textContent === 'Timeline and notes').click();
  const modal = await waitFor(() => document.querySelector('[role="dialog"]'));
  t.after(() => modal.closest('.modal-backdrop')?.remove());
  const realFetch = window.fetch;
  window.fetch = async (url, init) => (init?.method === 'POST' && String(url).startsWith('/api/incidents/')
    ? new Response(JSON.stringify({ error: 'database is locked' }), { status: 500, headers: { 'Content-Type': 'application/json' } })
    : realFetch(url, init));
  t.after(() => { window.fetch = realFetch; });
  modal.querySelector('textarea').value = 'Still looking';
  const add = [...modal.querySelectorAll('button')].find((b) => b.textContent === 'Add note');
  add.click();
  await waitFor(() => document.querySelector('.toast-error'));
  assert.match(document.querySelector('.toast-error').textContent, /database is locked/);
  assert.equal(add.disabled, false, 'the button can be pressed again');
});

test('the node page\'s Timeline link (?nodeId=) opens the events for that node', async (t) => {
  const { root } = await mountView(view, { query: new URLSearchParams('nodeId=1') }, t);
  assert.ok(root.querySelector('select[aria-label="Event type"]'), 'the Events tab, not the incident list');
});

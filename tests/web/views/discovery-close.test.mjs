// Closing the discovery modal while its Start request is still out must not
// leave a poll running behind it: onClose has already stopped watching, so a
// watch begun after that is never stopped.

import '../dom.mjs';
import '../../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import { openDiscovery } from '../../../web/views/discovery.js';
import { makeCtx, waitFor } from '../view-harness.mjs';

const json = (status, body) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

test('closing the modal during Start leaves no poll running', async (t) => {
  const realFetch = window.fetch;
  let release;
  const held = new Promise((r) => { release = r; });
  window.fetch = async (url, init) => {
    const u = String(url);
    if (u === '/api/discovery' && init?.method === 'POST') {
      await held;
      return json(202, { id: 'run-1', state: 'running', ranges: ['10.0.0.0/30'], scanned: 0, total: 2, responders: 0 });
    }
    if (u === '/api/discovery') return json(404, { error: 'nothing yet' });
    return realFetch(url, init);
  };
  t.after(() => { window.fetch = realFetch; });
  const polls = [];
  const realSet = globalThis.setInterval, realClear = globalThis.clearInterval;
  globalThis.setInterval = (fn, ms, ...rest) => { const id = realSet(fn, ms, ...rest); if (ms === 2000) polls.push(id); return id; };
  globalThis.clearInterval = (id) => { const i = polls.indexOf(id); if (i >= 0) polls.splice(i, 1); return realClear(id); };
  t.after(() => { for (const id of polls) realClear(id); globalThis.setInterval = realSet; globalThis.clearInterval = realClear; });

  await openDiscovery(makeCtx(), () => {});
  const modal = document.querySelector('#modals .modal');
  modal.querySelector('textarea').value = '10.0.0.0/30';
  [...modal.querySelectorAll('button')].find((b) => b.textContent.trim() === 'Start scan').click();
  modal.querySelector('.modal-head button').click(); // closed while the POST is held
  release();
  await new Promise((r) => setTimeout(r, 50));
  await waitFor(() => !document.querySelector('#modals .modal'));
  assert.equal(polls.length, 0, 'no discovery poll outlives the modal');
});

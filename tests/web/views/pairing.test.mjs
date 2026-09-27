// web/views/machines.js pairMachine (#69): once the code has been typed on the
// other machine, the dialog showing it has to say so — first that the machine
// has paired, then that its first reading has arrived — instead of leaving
// whoever typed it to go and look. web/mock.js redeems a code by itself after
// a delay, which the test shortens.

import '../dom.mjs';
import '../../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import { pairMachine, pairingWatch } from '../../../web/views/machines.js';
import { waitFor } from '../view-harness.mjs';

test('the pairing dialog confirms the machine pairing and then reporting', async (t) => {
  pairingWatch.intervalMs = 50;
  Object.assign(window.__gwatchMockPairing, { redeemMs: 300, reportMs: 300 });
  t.after(() => { document.querySelectorAll('.modal-backdrop').forEach((b) => b.remove()); });

  const started = pairMachine();
  const name = await waitFor(() => document.querySelector('.modal input[placeholder^="e.g. Living room"]'));
  name.value = 'Garage Pi';
  const ask = await waitFor(() => [...document.querySelectorAll('.modal-foot .btn')].find((b) => b.textContent.includes('Get a pairing code')));
  ask.click();
  await started;

  const status = await waitFor(() => document.querySelector('.modal [role="status"]'));
  assert.equal(status.textContent, '', 'nothing to confirm before the code is used');

  await waitFor(() => /Paired — Garage Pi/.test(status.textContent), { timeout: 5000 });
  const cancel = [...document.querySelectorAll('.modal-foot .btn')].find((b) => b.textContent.includes('Cancel this code'));
  assert.ok(cancel.hidden, 'a used code cannot be cancelled, so the button goes');

  await waitFor(() => /is paired and reporting/.test(status.textContent), { timeout: 5000 });
  const toasts = [...document.querySelectorAll('.toast')].map((x) => x.textContent);
  assert.ok(toasts.some((x) => x.includes('Garage Pi paired with GWatch')), `expected a paired toast, got ${toasts}`);
  assert.ok(toasts.some((x) => x.includes('Garage Pi is paired and reporting')), `expected a reporting toast, got ${toasts}`);
});

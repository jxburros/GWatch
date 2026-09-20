// Smoke test for web/views/discovery.js: open the modal, sweep the mock's
// network, tick everything that answered and add it — the whole errand, the
// way a person does it.

import '../dom.mjs';
import '../../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import { openDiscovery, guessRange } from '../../../web/views/discovery.js';
import { makeCtx, waitFor } from '../view-harness.mjs';

const modal = () => document.querySelector('#modals .modal');
const buttonNamed = (label) => [...document.querySelectorAll('#modals .modal button')]
  .find((b) => b.textContent.trim() === label);

test('discovery finds the mock devices and adds the ones that are ticked', async (t) => {
  window.__gwatchMockDiscoveryAdds.length = 0;
  let reloaded = 0;
  const ctx = makeCtx();
  const opened = openDiscovery(ctx, () => { reloaded++; });
  t.after(() => { modal()?.querySelector('.modal-head button')?.click(); });

  // The form fills itself in from /api/network and offers the templates.
  await opened;
  const ranges = modal().querySelector('textarea');
  assert.ok(ranges, 'the modal should offer a box to type ranges into');
  ranges.value = '192.168.1.0/24';

  buttonNamed('Start scan').click();

  // The mock run takes about two seconds; the modal shows a progress bar the
  // whole time and then the results.
  await waitFor(() => modal()?.querySelector('[role="progressbar"]'));
  const rows = await waitFor(() => {
    const found = modal()?.querySelectorAll('.table tbody tr');
    return found && found.length ? found : null;
  }, { timeout: 8000 });
  assert.equal(rows.length, 3, 'the mock network has three devices on it');

  // Each row names the device, times it and suggests somewhere to start.
  const first = rows[0];
  assert.equal(first.querySelector('td.mono').textContent, '192.168.1.1');
  assert.equal(first.querySelector('input[type="text"]').value, 'gateway.lan');
  assert.ok(first.querySelector('select'), 'each row offers a template to start from');

  // Nothing is ticked yet, so there is nothing to add.
  assert.ok(buttonNamed('Add selected').disabled, 'adding should wait until something is ticked');

  // "Select all", then add them.
  const selectAll = modal().querySelector('.table thead input[type="checkbox"]');
  selectAll.checked = true;
  selectAll.dispatchEvent(new window.Event('change'));
  const groupInput = [...modal().querySelectorAll('input[type="text"]')].pop();
  groupInput.value = 'Discovered';

  const add = buttonNamed('Add selected');
  assert.ok(!add.disabled, 'adding should be possible once something is ticked');
  add.click();

  await waitFor(() => window.__gwatchMockDiscoveryAdds.length > 0, { timeout: 8000 });
  const sent = window.__gwatchMockDiscoveryAdds[0];
  assert.equal(sent.items.length, 3);
  assert.deepEqual(sent.items.map((i) => i.ip).sort(), ['192.168.1.1', '192.168.1.23', '192.168.1.64']);
  // Both spellings of the group go out, so this works either side of the
  // change that turns a node's group into a list of them.
  assert.equal(sent.group, 'Discovered');
  assert.deepEqual(sent.groups, ['Discovered']);

  // The caller is told to reload, and the modal gets out of the way.
  await waitFor(() => reloaded > 0 && !modal(), { timeout: 8000 });
});

test('reopening picks the last run back up, and a bad range is refused', async (t) => {
  const ctx = makeCtx();
  await openDiscovery(ctx, () => {});
  t.after(() => { modal()?.querySelector('.modal-head button')?.click(); });

  // The modal was closed after the run above; opening it again finds that run
  // rather than an empty form.
  assert.ok(modal().querySelector('.table tbody tr'), 'the last run’s results should still be there');

  buttonNamed('Scan again').click();
  const ranges = await waitFor(() => modal()?.querySelector('textarea'));
  ranges.value = '';
  buttonNamed('Start scan').click();
  const banner = await waitFor(() => modal()?.querySelector('.banner'));
  assert.match(banner.textContent, /at least one range/);
  assert.ok(modal().querySelector('textarea'), 'the form is still there to correct');
});

test('the ranges box is prefilled from the LAN address when there is one', () => {
  assert.equal(guessRange({ lanUrls: ['http://192.168.1.10:7230', 'http://desktop-pc:7230'] }), '192.168.1.0/24');
  assert.equal(guessRange({ lanUrls: ['http://10.1.2.3:7230'] }), '10.1.2.0/24');
  assert.equal(guessRange({ lanUrls: ['http://172.20.5.6:7230'] }), '172.20.5.0/24');
  // A public address is not somebody's home network, and neither is nothing.
  assert.equal(guessRange({ lanUrls: ['http://93.184.215.14:7230'] }), '');
  assert.equal(guessRange({ lanUrls: [] }), '');
  assert.equal(guessRange(null), '');
});

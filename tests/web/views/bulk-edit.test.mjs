// web/views/bulk-edit.js: pick two nodes, say "every check every 2 minutes",
// apply it, and check that the mock's nodes actually moved — the screen's
// promise (its summary sentence) and what it does have to be the same thing.

import '../dom.mjs';
import '../../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as bulkView from '../../../web/views/bulk-edit.js';
import { BULK_FIELDS, bulkField, bulkValue, setPath } from '../../../web/views/node-fields.js';
import { mountView, settle, waitFor } from '../view-harness.mjs';

/** Tick a checkbox the way a person does: set it and fire the change event
 *  the view listens for. */
function tick(box, on = true) {
  box.checked = on;
  box.dispatchEvent(new window.Event('change', { bubbles: true }));
}

/** Answer the confirm dialog the Apply button opens. */
async function confirmDialog() {
  const button = await waitFor(() => [...document.querySelectorAll('.modal-foot .btn')].find((b) => b.textContent.includes('Apply changes')));
  button.click();
}

test('bulk edit applies one interval across two nodes and says so', async (t) => {
  const { root } = await mountView(bulkView, undefined, t);

  const rows = [...root.querySelectorAll('.bulk-node')];
  assert.ok(rows.length >= 2, 'the node list is on the page');

  // Pick the first two nodes. Ticking a node ticks its checks with it.
  const names = rows.slice(0, 2).map((r) => r.querySelector('.bulk-node-name').textContent);
  for (const r of rows.slice(0, 2)) tick(r.querySelector('.bulk-node-head input[type="checkbox"]'));

  const picked = names.map((name) => window.__gwatchMock.nodes.find((n) => n.name === name));
  const affected = picked.flatMap((n) => n.checks);
  assert.ok(affected.length > 0);

  // The change: every selected check, every 2 minutes.
  const paramSel = root.querySelector('.bulk-row select');
  assert.equal(paramSel.value, 'intervalSeconds', 'the interval is the first parameter offered');
  const valueSel = [...root.querySelectorAll('.bulk-row select')][1];
  valueSel.value = '120';
  valueSel.dispatchEvent(new window.Event('change', { bubbles: true }));

  const summary = root.querySelector('.bulk-summary');
  assert.match(summary.textContent, /Set interval to 2 min on \d+ checks/);
  assert.match(summary.textContent, new RegExp(`on ${affected.length} checks`));

  const apply = [...root.querySelectorAll('.btn')].find((b) => b.textContent.includes('Apply changes'));
  assert.ok(apply && !apply.disabled, 'Apply is offered once there is something to apply');
  apply.click();
  await confirmDialog();
  await waitFor(() => affected.every((c) => c.intervalSeconds === 120));

  // Every check of both nodes moved, and nothing else did.
  for (const n of window.__gwatchMock.nodes) {
    const expected = names.includes(n.name) ? 120 : null;
    for (const c of n.checks) {
      if (expected) assert.equal(c.intervalSeconds, expected, `${n.name} / ${c.name}`);
    }
  }

  // And the screen said what happened.
  const toast = await waitFor(() => [...document.querySelectorAll('.toast')].find((el) => /Updated/.test(el.textContent)));
  assert.match(toast.textContent, new RegExp(`${affected.length} checks`));
});

test('a node-level change reports node counts and leaves the checks alone', async (t) => {
  const { root } = await mountView(bulkView, undefined, t);
  const row = root.querySelector('.bulk-node');
  const name = row.querySelector('.bulk-node-name').textContent;
  tick(row.querySelector('.bulk-node-head input[type="checkbox"]'));

  const node = window.__gwatchMock.nodes.find((n) => n.name === name);
  const intervals = node.checks.map((c) => c.intervalSeconds);

  const paramSel = root.querySelector('.bulk-row select');
  paramSel.value = 'addTags';
  paramSel.dispatchEvent(new window.Event('change', { bubbles: true }));

  const chip = root.querySelector('.bulk-row .chip-input input');
  chip.value = 'paged';
  chip.dispatchEvent(new window.Event('change', { bubbles: true }));

  assert.match(root.querySelector('.bulk-summary').textContent, /Add tag paged on 1 node/);

  [...root.querySelectorAll('.btn')].find((b) => b.textContent.includes('Apply changes')).click();
  await confirmDialog();
  await waitFor(() => (node.tags || []).includes('paged'));
  assert.deepEqual(node.checks.map((c) => c.intervalSeconds), intervals, 'a node-only change left the checks alone');
});

test('nothing selected means nothing to apply', async (t) => {
  const { root } = await mountView(bulkView, undefined, t);
  const apply = [...root.querySelectorAll('.btn')].find((b) => b.textContent.includes('Apply changes'));
  assert.equal(apply.disabled, true);
  assert.match(root.querySelector('.bulk-summary').textContent, /Pick some nodes or checks/);
  await settle();
});

// The field table is the contract between this screen and the node editor, so
// it is worth holding to its own shape rather than only through a rendered
// dropdown.
test('every bulk field declares a scope, a patch path and a summary', () => {
  assert.ok(BULK_FIELDS.length > 10);
  for (const f of BULK_FIELDS) {
    assert.ok(['node', 'check'].includes(f.scope), `${f.key} has a scope`);
    assert.ok(f.path && f.label && typeof f.summary === 'function', `${f.key} is complete`);
  }
  // A dotted path builds the nested object the API expects, and two rows
  // under the same parent merge rather than overwrite one another.
  const body = {};
  setPath(body, bulkField('alertsEnabled').path, bulkValue(bulkField('alertsEnabled'), 'false'));
  setPath(body, bulkField('alertsCooldown').path, bulkValue(bulkField('alertsCooldown'), '30'));
  assert.deepEqual(body, { alerts: { enabled: false, cooldownMinutes: 30 } });
});

// #60: a hardware threshold travels as a one-entry list merged by key, with
// blank levels left out so the server reads them as off.
test('the hardware threshold bulk field sends one merged list entry', () => {
  const f = bulkField('metricThresholds');
  assert.equal(f.scope, 'check');
  assert.equal(f.path, 'config.metricThresholds');
  assert.deepEqual(bulkValue(f, { metric: ' disk:/srv ', warn: 90, crit: '' }), [{ metric: 'disk:/srv', warn: 90 }]);
  assert.deepEqual(bulkValue(f, { metric: 'net', warn: '', crit: 5e6 }), [{ metric: 'net', crit: 5e6 }]);
  assert.match(f.summary({ metric: 'disk', warn: 90, crit: '' }), /disk thresholds to warning 90, critical off/);
  const body = {};
  setPath(body, f.path, bulkValue(f, { metric: 'disk', warn: 90, crit: 98 }));
  assert.deepEqual(body, { config: { metricThresholds: [{ metric: 'disk', warn: 90, crit: 98 }] } });
});

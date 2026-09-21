// web/views/rules.js (Settings › Rules, #31): the list shows the mock's two
// rules with their state, the editor opens with the shared action editor,
// the "at least N" count appears only for that join, and an empty name is
// refused inline before anything is sent.

import '../dom.mjs';
import '../../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as settingsView from '../../../web/views/settings.js';
import { mountView, waitFor } from '../view-harness.mjs';
import { joinLabel, conditionLabels } from '../../../web/views/rules.js';

const modal = () => document.querySelector('#modals .modal');
const buttonNamed = (root, label) => [...root.querySelectorAll('button')].find((b) => b.textContent.trim() === label || b.getAttribute('aria-label') === label);

test('rules tab lists the mock rules with their state', async (t) => {
  const { root } = await mountView(settingsView, { params: { tab: 'rules' } }, t);
  const panel = root.querySelector('.settings-panel');
  assert.ok(root.querySelector('.settings-nav a.active')?.textContent === 'Rules');
  const rows = [...panel.querySelectorAll('.rule-row')];
  assert.equal(rows.length, 2, 'two rules from web/mock.js');
  const gatewayRule = rows.find((r) => r.textContent.includes('Gateway and Plex both down'));
  assert.ok(gatewayRule, 'the met rule is listed');
  assert.match(gatewayRule.textContent, /Met since/);
  assert.match(gatewayRule.textContent, /All 2 conditions/);
  assert.match(gatewayRule.textContent, /any check of Gateway is down/);
  assert.match(gatewayRule.textContent, /notifies when cleared/);
  const dnsRule = rows.find((r) => r.textContent.includes('Two of three DNS servers down'));
  assert.match(dnsRule.textContent, /Not met/);
  assert.match(dnsRule.textContent, /last fired/);
  assert.match(dnsRule.textContent, /At least 2 of 3 conditions/);
  assert.match(dnsRule.textContent, /Pi-hole › Resolves via Pi-hole is down/);
  // Every switch and icon-only button carries a name for the axe scan.
  for (const sw of panel.querySelectorAll('input[role="switch"]')) assert.ok(sw.getAttribute('aria-label'), 'switch has a name');
  for (const btn of panel.querySelectorAll('button.icon-btn')) assert.ok(btn.getAttribute('aria-label'), 'icon button has a name');
  assert.ok(buttonNamed(panel, 'Send a test'), 'a test button per rule');
});

test('rule editor: at least N shows its count, labelled selects, inline validation', async (t) => {
  const { root } = await mountView(settingsView, { params: { tab: 'rules' } }, t);
  t.after(() => { modal()?.querySelector('.modal-head button')?.click(); });
  buttonNamed(root, 'New rule').click();
  await waitFor(() => modal());
  const m = modal();
  assert.equal(m.querySelector('.modal-head h2').textContent, 'New rule');

  // One condition row to start with: node, check ("any"), status — each labelled.
  const row = m.querySelector('.rule-cond-row');
  assert.ok(row, 'a first condition row');
  const [nodeSel, checkSel, statusSel] = row.querySelectorAll('select');
  assert.equal(nodeSel.getAttribute('aria-label'), 'Node');
  assert.equal(checkSel.getAttribute('aria-label'), 'Check');
  assert.equal(statusSel.getAttribute('aria-label'), 'Status');
  assert.equal(checkSel.value, '', 'any check by default');
  assert.ok([...checkSel.options].length > 1, 'the node\'s checks are offered');
  assert.deepEqual([...statusSel.options].map((o) => o.value), ['down', 'degraded']);
  // Picking another node refills the check list.
  nodeSel.value = nodeSel.options[nodeSel.options.length - 1].value;
  nodeSel.dispatchEvent(new window.Event('change'));
  assert.equal(checkSel.options[0].textContent, 'Any check of this node');

  // The join select: the count is hidden for "all" and appears for "at least".
  const joinSel = [...m.querySelectorAll('select')].find((s) => [...s.options].some((o) => o.value === 'at_least'));
  const atLeastField = [...m.querySelectorAll('.field')].find((f) => f.textContent.includes('How many'));
  assert.equal(atLeastField.hidden, true);
  joinSel.value = 'at_least';
  joinSel.dispatchEvent(new window.Event('change'));
  assert.equal(atLeastField.hidden, false);
  const atLeast = atLeastField.querySelector('input[type="number"]');
  assert.equal(atLeast.value, '2');

  // The shared action editor is there, once, with the type select.
  assert.equal(m.querySelectorAll('.rule-action').length, 1);
  buttonNamed(m, 'Add action').click();
  assert.equal(m.querySelectorAll('.rule-action').length, 2);
  buttonNamed(m, 'Add condition').click();
  assert.equal(m.querySelectorAll('.rule-cond-row').length, 2);

  // Submit with no name: refused inline, nothing sent, the dialog stays open.
  atLeast.value = '5';
  buttonNamed(m, 'Create rule').click();
  await waitFor(() => m.querySelector('.field.has-error'));
  const errors = [...m.querySelectorAll('.field .error')].map((e) => e.textContent);
  assert.ok(errors.includes('A name is required.'), errors.join(' | '));
  assert.ok(errors.includes('Pick a number between 1 and 2.'), errors.join(' | '));
  assert.ok(modal(), 'the dialog stays open');
  const nameInput = m.querySelector('input[type="text"]');
  assert.equal(nameInput.getAttribute('aria-invalid'), 'true');
});

test('joinLabel and conditionLabels read as sentences', () => {
  const nodes = [{ id: 1, name: 'Pi-hole', checks: [{ id: 10, name: 'DNS' }] }, { id: 2, name: 'NAS', checks: [] }];
  const r = { join: 'at_least', atLeast: 2, conditions: [{ kind: 'status', checkId: 10, status: 'down' }, { kind: 'status', nodeId: 2, status: 'degraded' }, { kind: 'status', checkId: 99, status: 'down' }] };
  assert.equal(joinLabel(r), 'At least 2 of 3 conditions');
  assert.deepEqual(conditionLabels(r, nodes), ['Pi-hole › DNS is down', 'any check of NAS is degraded or worse', 'check 99 is down']);
  assert.equal(joinLabel({ join: 'any', conditions: [{}] }), 'Any of 1 condition');
  assert.equal(joinLabel({ join: 'all', conditions: [{}, {}] }), 'All 2 conditions');
});

// web/views/node-editor.js in edit mode: loading an existing node populates
// the name field and one check card per existing check.

import '../dom.mjs';
import '../../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as nodeEditorView from '../../../web/views/node-editor.js';
import { mountView, settle, waitFor } from '../view-harness.mjs';

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

test('walking a device offers its readings and adds the ticked ones', async (t) => {
  const node = window.__gwatchMock.nodes.find((n) => n.checks.some((c) => c.type === 'snmp'));
  const check = node.checks.find((c) => c.type === 'snmp');
  const { root } = await mountView(nodeEditorView, { params: { id: String(node.id) } }, t);
  const card = [...root.querySelectorAll('.editor-check')].find((c) => c.getAttribute('aria-label') === `${check.name} check`);
  const before = card.querySelectorAll('table.table tbody tr').length;

  const walk = [...card.querySelectorAll('button')].find((b) => b.textContent.includes('Walk this device'));
  assert.ok(walk, 'the editor offers to walk the device');
  walk.click();
  const modal = await waitFor(() => document.querySelector('.modal'));

  const rows = modal.querySelectorAll('tbody tr');
  assert.ok(rows.length > 5, 'the walk lists what the device reported');
  assert.ok(modal.textContent.includes('1.3.6.1.2.1.31.1.1.1.6.1'), 'an interface counter is listed with its index');

  // A row already on the check cannot be added twice.
  const known = [...rows].find((r) => r.textContent.includes(check.config.snmpOids[0].oid));
  assert.ok(known.querySelector('input[type="checkbox"]').disabled, 'a reading already added is not offered again');

  // Tick a counter row and add it: it arrives as a counter.
  const counter = [...rows].find((r) => r.textContent.includes('1.3.6.1.2.1.2.2.1.14.2'));
  counter.querySelector('input[type="checkbox"]').checked = true;
  [...modal.querySelectorAll('button')].find((b) => b.textContent === 'Add ticked readings').click();

  const after = card.querySelectorAll('table.table tbody tr');
  assert.equal(after.length, before + 1, 'the ticked reading was added');
  const added = [...after].find((r) => r.querySelector('input').value === '1.3.6.1.2.1.2.2.1.14.2');
  assert.ok(added, 'with the OID that was ticked');
  assert.equal([...added.querySelectorAll('select')][0].value, 'counter', 'and the kind guessed from its type');
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

// #55: a json check can record the value it reads. The editor shows the
// recording fields only once the switch is on, filled from the check.
test('node editor shows a json check\'s recording fields behind its switch', async (t) => {
  const node = window.__gwatchMock.nodes.find((n) => n.checks.some((c) => c.type === 'json' && c.config.jsonRecord));
  const check = node.checks.find((c) => c.type === 'json' && c.config.jsonRecord);
  const { root } = await mountView(nodeEditorView, { params: { id: String(node.id) } }, t);

  const card = [...root.querySelectorAll('.editor-check')].find((c) => c.getAttribute('aria-label') === `${check.name} check`);
  assert.ok(card, 'the recording json check has a card of its own');
  const record = [...card.querySelectorAll('label.checkbox')].find((l) => l.textContent.includes('Record this value'));
  assert.ok(record, 'the card offers to record the value');
  assert.equal(record.input.checked, true, 'and the switch reflects the stored setting');

  const inputs = [...card.querySelectorAll('input')];
  assert.ok(inputs.some((i) => i.value === check.config.jsonMetric), 'the metric name is filled in');
  assert.ok(inputs.some((i) => i.value === check.config.jsonUnit), 'the unit is filled in');
  // The same four thresholds an SNMP reading has, worded the same way.
  for (const label of ['Warn >', 'Crit >', 'Warn <', 'Crit <']) {
    assert.ok(card.querySelector(`input[aria-label="${label} threshold"]`), `${label} is offered`);
  }
  assert.equal(card.querySelector('input[aria-label="Warn > threshold"]').value, String(check.config.jsonWarnAbove));

  // Switching recording off hides the fields it governs.
  record.input.checked = false;
  record.input.dispatchEvent(new window.Event('change'));
  assert.equal(card.querySelector('input[aria-label="Warn > threshold"]').closest('[hidden]') != null, true, 'the recording fields are hidden once the switch is off');
});

test('a json check that does not record keeps its recording fields folded away', async (t) => {
  const node = window.__gwatchMock.nodes.find((n) => n.checks.some((c) => c.type === 'json' && !c.config.jsonRecord));
  const check = node.checks.find((c) => c.type === 'json' && !c.config.jsonRecord);
  const { root } = await mountView(nodeEditorView, { params: { id: String(node.id) } }, t);
  const card = [...root.querySelectorAll('.editor-check')].find((c) => c.getAttribute('aria-label') === `${check.name} check`);
  const record = [...card.querySelectorAll('label.checkbox')].find((l) => l.textContent.includes('Record this value'));
  assert.ok(record, 'the switch is offered on every json check');
  assert.equal(record.input.checked, false);
  assert.ok(card.querySelector('input[aria-label="Warn > threshold"]').closest('[hidden]'), 'its fields stay hidden until it is switched on');
});

// #60: a hardware check's thresholds are a list, one boxed pair per family,
// with instance overrides below; a check that still carries the flat fields
// of an older configuration is converted the moment it is loaded.
test('node editor renders a hardware check\'s thresholds per metric, with instance overrides', async (t) => {
  const node = window.__gwatchMock.nodes.find((n) => n.checks.some((c) => c.type === 'system'));
  const check = node.checks.find((c) => c.type === 'system');
  const { root } = await mountView(nodeEditorView, { params: { id: String(node.id) } }, t);

  const card = [...root.querySelectorAll('.editor-check')].find((c) => c.getAttribute('aria-label') === `${check.name} check`);
  assert.ok(card, 'the hardware check has a card of its own');
  const groups = [...card.querySelectorAll('.threshold-group')].map((g) => g.dataset.metric);
  for (const family of ['cpu', 'memory', 'swap', 'load', 'disk', 'inodes', 'net', 'diskio']) assert.ok(groups.includes(family), `${family} has a threshold group`);
  assert.ok(groups.includes('disk:/'), 'the configured instance override has its own row');

  // The family boxes show the configured levels; a missing level is blank.
  assert.equal(card.querySelector('[data-metric="disk"] input[aria-label="Disk space warning threshold"]').value, '85');
  assert.equal(card.querySelector('[data-metric="disk"] input[aria-label="Disk space critical threshold"]').value, '95');
  assert.equal(card.querySelector('[data-metric="swap"] input[aria-label="Swap critical threshold"]').value, '', 'an unset level is blank, not zero');

  // Adding an override for another disk gives it a row of its own.
  const instanceIn = card.querySelector('input[aria-label="Which disk, interface or device"]');
  instanceIn.value = '/srv/media';
  instanceIn.closest('.row').querySelector('button').click();
  await settle();
  assert.ok(root.querySelector('[data-metric="disk:/srv/media"]'), 'the new instance override is listed');
  assert.equal(check.config.metricThresholds.filter((x) => x.metric === 'disk:/srv/media').length, 0, 'the mock\'s stored check is untouched until save');
});

test('node editor converts a hardware check\'s legacy flat thresholds to the list', () => {
  const { legacyMetricThresholds, metricThresholdErrors } = nodeEditorView;
  const list = legacyMetricThresholds({ cpuWarnPct: 80, memWarnPct: 90, memCritPct: 97, diskWarnPct: 70, diskCritPct: 90, loadWarnPerCore: 2 });
  const byKey = Object.fromEntries(list.map((x) => [x.metric, x]));
  assert.deepEqual(byKey.cpu, { metric: 'cpu', warn: 80, crit: undefined });
  assert.deepEqual(byKey.disk, { metric: 'disk', warn: 70, crit: 90 });
  assert.deepEqual(byKey.inodes, { metric: 'inodes', warn: 70, crit: 90 }, 'inodes follow the disk pair when the disk critical level is set');
  assert.equal(byKey.swap, undefined, 'a zero level is off and gets no entry');

  // The same rules as the server's ValidateMetricThresholds.
  assert.equal(metricThresholdErrors(list), null);
  assert.match(metricThresholdErrors([{ metric: 'gpu', warn: 1 }]), /not a hardware metric/);
  assert.match(metricThresholdErrors([{ metric: 'cpu:0', warn: 1 }]), /cannot name an instance/);
  assert.match(metricThresholdErrors([{ metric: 'disk', warn: 1 }, { metric: 'disk', warn: 2 }]), /both apply/);
  assert.match(metricThresholdErrors([{ metric: 'disk:/srv', crit: 140 }]), /between 0 and 100/);
  assert.match(metricThresholdErrors([{ metric: 'memory', warn: 90, crit: 50 }]), /at or above its warning/);
  assert.match(metricThresholdErrors([{ metric: 'net:eth0', warn: 10, crit: 50, below: true }]), /at or below its warning/);
  assert.equal(metricThresholdErrors([{ metric: 'net:eth0.rx', warn: 100, crit: 10, below: true }]), null);
});

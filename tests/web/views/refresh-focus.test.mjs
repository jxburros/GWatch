// #118 (and the bug class behind #112): a refresh — a live update landing, or
// the view redrawing itself after a click — must not take the keyboard focus
// off the control it was on. web/components.js has two shared ways to refresh
// a region, renderKeyed (keep the same elements) and refreshRegion (rebuild,
// then put the focus and scroll back); these tests pin both, and the views
// that use them.

import '../dom.mjs';
import '../../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import { h, renderKeyed, refreshRegion, chipRow, chipRows } from '../../../web/components.js';
import * as nodesView from '../../../web/views/nodes.js';
import * as bulkView from '../../../web/views/bulk-edit.js';
import * as incidentsView from '../../../web/views/incidents.js';
import * as dashboardView from '../../../web/views/dashboard.js';
import { mountView, settle, waitFor } from '../view-harness.mjs';

function attach(el) { document.body.append(el); return el; }

test('renderKeyed keeps, updates, adds, removes and reorders by key', () => {
  const list = attach(h('ul'));
  const items = [{ id: 1, t: 'a' }, { id: 2, t: 'b' }, { id: 3, t: 'c' }];
  const opts = {
    key: (i) => String(i.id),
    render: (i) => h('li', null, h('button', { type: 'button' }, i.t)),
    update: (el, i) => { el.firstChild.textContent = i.t; },
  };
  renderKeyed(list, items, opts);
  const [one, two] = list.children;
  two.firstChild.focus();
  renderKeyed(list, [{ id: 4, t: 'd' }, { id: 2, t: 'B' }, null, { id: 1, t: 'a' }], opts);
  assert.deepEqual([...list.children].map((li) => li.textContent), ['d', 'B', 'a']);
  assert.equal(list.children[1], two, 'the kept element is the same node');
  assert.equal(list.children[2], one, 'a moved element is the same node');
  assert.equal(document.activeElement, two.firstChild, 'the focus is still on the kept button');
  // Moving the focused element itself does not lose the focus either.
  renderKeyed(list, [{ id: 2, t: 'B' }, { id: 4, t: 'd' }], opts);
  assert.equal(list.firstElementChild, two);
  assert.equal(document.activeElement, two.firstChild);
  list.remove();
});

test('renderKeyed swaps in an element update returns', () => {
  const list = attach(h('div'));
  const opts = { key: (i) => i, render: (i) => h('p', null, i), update: (el, i) => h('p', { class: 'new' }, i) };
  renderKeyed(list, ['x'], opts);
  renderKeyed(list, ['x'], opts);
  assert.equal(list.children.length, 1);
  assert.equal(list.firstElementChild.className, 'new');
  list.remove();
});

test('refreshRegion rebuilds and puts the focus and the scroll back', () => {
  const region = attach(h('div'));
  const build = (r) => r.append(h('a', { href: '#/one' }, 'One'), h('a', { href: '#/two' }, 'Two'));
  refreshRegion(region, build);
  const before = region.querySelector('a[href="#/two"]');
  before.focus();
  region.scrollTop = 40;
  refreshRegion(region, build);
  const after = region.querySelector('a[href="#/two"]');
  assert.notEqual(after, before, 'the contents were rebuilt');
  assert.equal(document.activeElement, after, 'the focus moved to the rebuilt link');
  assert.equal(region.scrollTop, 40);
  region.remove();
});

test('chipRow updates a row in place: same buttons, new counts, new pressed chip', () => {
  const picks = [];
  const items = [{ value: 'a', label: 'A (1)' }, { value: 'b', label: 'B (2)' }];
  const row = attach(chipRow(null, { label: 'Group', items, current: '', onPick: (v) => picks.push(v) }));
  const b = row.querySelector('[data-focus-key="Group:b"]');
  b.focus();
  chipRow(row, { label: 'Group', items: [{ value: 'a', label: 'A (3)' }, { value: 'b', label: 'B (4)' }], current: 'b', onPick: (v) => picks.push(v) });
  assert.equal(row.querySelector('[data-focus-key="Group:b"]'), b);
  assert.equal(document.activeElement, b);
  assert.equal(b.textContent, 'B (4)');
  assert.equal(b.getAttribute('aria-pressed'), 'true');
  assert.equal(row.querySelector('[data-focus-key="Group:all"]').getAttribute('aria-pressed'), 'false');
  assert.equal(row.querySelector('.chip-label').textContent, 'Group');
  b.click(); // the pressed chip again goes back to All
  row.querySelector('[data-focus-key="Group:a"]').click();
  assert.deepEqual(picks, ['', 'a']);
  assert.equal(chipRow(null, { label: 'Empty', items: [], current: '', onPick() {} }), null);
  row.remove();
});

test('chipRows leaves out an empty row and keeps the others by label', () => {
  const bar = attach(h('div'));
  const rows = (n) => [
    { label: 'Group', items: [{ value: 'g', label: `g (${n})` }], current: '', onPick() {} },
    { label: 'Tag', items: [], current: '', onPick() {} },
  ];
  chipRows(bar, rows(1));
  const group = bar.firstElementChild;
  chipRows(bar, rows(2));
  assert.equal(bar.children.length, 1);
  assert.equal(bar.firstElementChild, group);
  assert.match(group.textContent, /g \(2\)/);
  bar.remove();
});

test('Nodes: picking a filter chip keeps the focus on that chip, and a refresh keeps the chips', async (t) => {
  const { root, instance } = await mountView(nodesView, undefined, t);
  const chip = root.querySelector('[data-focus-key="Status:down"]');
  assert.ok(chip, 'the mock has down nodes');
  chip.focus();
  chip.click();
  assert.equal(document.activeElement, chip, 'the very chip clicked still has the focus');
  assert.equal(chip.getAttribute('aria-pressed'), 'true');
  await instance.refresh();
  assert.equal(root.querySelector('[data-focus-key="Status:down"]'), chip, 'a live refresh kept the element');
  assert.equal(document.activeElement, chip);
});

test('Bulk edit: the filter chips and the check-type chips keep the focus when pressed', async (t) => {
  const { root } = await mountView(bulkView, undefined, t);
  const chip = await waitFor(() => root.querySelector('.filters [data-focus-key^="Group:"]:not([data-focus-key="Group:all"])'));
  chip.focus();
  chip.click();
  assert.equal(document.activeElement, chip);
  assert.equal(chip.getAttribute('aria-pressed'), 'true');

  const typeRow = root.querySelector('[aria-label="Limit to check types"]');
  const typeChip = [...typeRow.querySelectorAll('button')].find((b) => b.textContent !== 'Every type');
  assert.ok(typeChip, 'a check type chip is shown');
  typeChip.focus();
  typeChip.click();
  assert.equal(document.activeElement, typeChip);
  assert.equal(typeChip.getAttribute('aria-pressed'), 'true');
  assert.equal(typeRow.querySelector('.chip-label').textContent, 'Only');
  typeChip.click();
  assert.equal(typeChip.getAttribute('aria-pressed'), 'false');
});

test('Incidents: a refresh keeps the focus on an incident\'s button', async (t) => {
  window.location.hash = '#/incidents';
  const { root, instance } = await mountView(incidentsView, undefined, t);
  const button = await waitFor(() => root.querySelector('[data-focus-key^="incident:"]'));
  const key = button.dataset.focusKey;
  button.focus();
  await instance.refresh();
  assert.equal(document.activeElement.dataset.focusKey, key);
  assert.ok(document.activeElement.isConnected);
});

test('Dashboard: a live refresh keeps the focus inside a widget', async (t) => {
  window.location.hash = '#/dashboard';
  const { root, instance } = await mountView(dashboardView, undefined, t);
  await settle();
  // A link inside a widget that is rewritten (not a chart) on refresh.
  const link = [...root.querySelectorAll('.widget a[href^="#/nodes/"]')].find((a) => !a.closest('.chart-wrap'));
  assert.ok(link, 'a widget lists a node');
  const href = link.getAttribute('href');
  link.focus();
  await instance.refresh();
  await settle();
  assert.notEqual(document.activeElement, link, "the widget body was rebuilt");
  assert.equal(document.activeElement.getAttribute('href'), href);
  assert.ok(root.contains(document.activeElement));
});

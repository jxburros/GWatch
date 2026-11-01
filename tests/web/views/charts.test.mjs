// web/views/charts.js opens on a blank chart; a saved one (the mock seeds two
// — see web/mock.js's savedCharts) is opened from the list beside it, with
// the config editor.

import '../dom.mjs';
import '../../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as chartsView from '../../../web/views/charts.js';
import { mountView, settle, waitFor } from '../view-harness.mjs';

test('charts view opens on a blank chart, not on a saved one', async (t) => {
  const { root, ctx } = await mountView(chartsView, undefined, t);
  assert.equal(ctx.setTitleCalls.at(-1).title, 'Charts');
  assert.equal(root.querySelectorAll('.saved-item').length, 2, 'the saved charts are listed');
  assert.equal(root.querySelectorAll('.saved-item.active').length, 0, 'none of them is open');
  assert.match(root.querySelector('.chart-card').textContent, /A blank chart/);
  assert.equal(root.querySelector('.chart-card canvas'), null, 'nothing is plotted');
  assert.equal(root.querySelectorAll('.metric-picker input:checked').length, 0, 'nothing is ticked');
  await settle();
});

test('a saved chart opens with its own title and series', async (t) => {
  const { root, ctx } = await mountView(chartsView, { params: { id: 'chart-1' } }, t);
  assert.equal(ctx.setTitleCalls.at(-1).title, 'Gateway latency');
  // The chart was saved before series existed and opens as one: its check is
  // open in the picker, with its latency ticked.
  const open = root.querySelector('details.metric-check[open]');
  assert.ok(open, 'the check the chart plots is open in the picker');
  assert.equal(open.querySelector('[data-metric="avg"] input').checked, true);
  await settle();
});

test('a saved chart mixing a latency with a machine’s readings draws two axes (#73)', async (t) => {
  const { root } = await mountView(chartsView, { params: { id: 'chart-2' } }, t);
  const legend = await waitFor(() => root.querySelectorAll('.chart-card .legend-item').length >= 3 && root.querySelectorAll('.chart-card .legend-item'));
  const text = [...legend].map((el) => el.textContent);
  assert.match(text[0], /Latency \(average\)ms · left axis$/);
  assert.match(text[1], /Processor/);
  assert.match(text[1], /% · right axis$/);
  assert.equal(root.querySelector('.chart-card .card-head .tag').textContent, '3 metrics');
});

// "New chart" goes to #/charts, which the router hands back as an update with
// no id; that used to read as a change of chart and open the first saved one.
test('New chart from a saved chart opens a blank chart, not the first saved one', async (t) => {
  const { ctx, instance } = await mountView(chartsView, { params: { id: 'chart-2' } }, t);
  const actions = ctx.setTitleCalls.at(-1).opts.actions;
  actions.find((a) => a.textContent.includes('New chart')).click();
  await settle();
  assert.deepEqual(ctx.navigated.at(-1), '/charts');
  await instance.update({ id: undefined }, new URLSearchParams()); // the hashchange that follows
  assert.equal(ctx.setTitleCalls.at(-1).title, 'Charts', 'an unsaved chart, titled as one');
});

// A refused save must not show the chart as deleted: the list is only
// replaced once the server has taken the new one.
test('a refused delete keeps the chart in the list', async (t) => {
  const { root, ctx } = await mountView(chartsView, { params: { id: 'chart-2' } }, t);
  const realFetch = window.fetch;
  window.fetch = async (url, init) => (init?.method === 'PUT' && String(url) === '/api/charts'
    ? new Response(JSON.stringify({ error: 'disk full' }), { status: 507, headers: { 'Content-Type': 'application/json' } })
    : realFetch(url, init));
  t.after(() => { window.fetch = realFetch; });
  const menu = ctx.setTitleCalls.at(-1).opts.actions.find((a) => a.getAttribute('aria-label') === 'Chart options');
  menu.click();
  [...document.querySelectorAll('.menu-item')].find((b) => b.textContent.includes('Delete chart')).click();
  const dialog = await waitFor(() => document.querySelector('[role="dialog"]'));
  [...dialog.querySelectorAll('button')].find((b) => b.textContent === 'Delete').click();
  await waitFor(() => [...document.querySelectorAll('.toast-error')].find((el) => /disk full/.test(el.textContent)));
  await settle();
  assert.equal(root.querySelectorAll('.saved-item').length, 2);
  assert.notEqual(ctx.setTitleCalls.at(-1).title, 'Charts', 'still on the chart that was not deleted');
});

// The dashboard select in the pin dialog sat beside a <label> that was not
// tied to it, so a screen reader announced an unnamed combo box.
test('the pin-to-dashboard dialog names its dashboard select', async (t) => {
  const { ctx } = await mountView(chartsView, { params: { id: 'chart-2' } }, t);
  ctx.setTitleCalls.at(-1).opts.actions.find((a) => a.getAttribute('aria-label') === 'Chart options').click();
  [...document.querySelectorAll('.menu-item')].find((b) => b.textContent.includes('Add to a dashboard')).click();
  const dialog = await waitFor(() => document.querySelector('[role="dialog"]'));
  const sel = await waitFor(() => dialog.querySelector('select'));
  const label = sel.id && dialog.querySelector(`label[for="${sel.id}"]`);
  assert.ok(label, 'the select has a label');
  assert.equal(label.textContent, 'Dashboard');
  [...dialog.querySelectorAll('button')].find((b) => b.textContent === 'Cancel').click();
});

// #184: a node watched several ways is one item in the picker, with its checks
// inside it, and "One chart per node" puts all of a node's series together.
test('the metric picker groups checks under their node, and charts can split per node', async () => {
  const { metricPicker, normalizeChartConfig } = await import('../../../web/chart-config.js');
  const nodes = [
    { id: 1, name: 'Router', checks: [{ id: 11, name: 'Ping', type: 'ping' }, { id: 12, name: 'SNMP', type: 'snmp' }] },
    { id: 2, name: 'NAS', checks: [{ id: 21, name: 'HTTP', type: 'http' }] },
  ];
  const picker = metricPicker(nodes, []);
  const groups = picker.querySelectorAll('.metric-node');
  assert.equal(groups.length, 2);
  assert.equal(groups[0].querySelectorAll('.metric-check').length, 2, 'both of the router’s checks sit under it');
  const filter = picker.querySelector('input[type="search"]');
  if (filter) {
    filter.value = 'router';
    filter.dispatchEvent(new window.Event('input'));
    assert.equal(groups[1].hidden, true);
    assert.equal(groups[0].hidden, false);
  }
  assert.equal(normalizeChartConfig({ split: true }).splitBy, 'check', 'the older split flag still means per check');
  assert.equal(normalizeChartConfig({ splitBy: 'node' }).splitBy, 'node');
  assert.equal(normalizeChartConfig({ splitBy: 'bogus' }).splitBy, '');
});

test('"One chart per node" draws one chart for each node, headed by its name', async () => {
  const { renderConfiguredChart } = await import('../../../web/chart-config.js');
  const now = Date.now();
  const series = (checkId, nodeId, nodeName, checkName) => ({
    checkId, nodeId, nodeName, checkName, checkType: 'ping', range: '24h', bucketSeconds: 300,
    from: new Date(now - 864e5).toISOString(), to: new Date(now).toISOString(),
    points: [{ t: new Date(now - 6e5).toISOString(), avg: 3, count: 1, failures: 0 }], summary: { availability: 100 },
  });
  const all = [series(11, 1, 'Router', 'Ping'), series(12, 1, 'Router', 'SNMP'), series(21, 2, 'NAS', 'HTTP')];
  const host = document.createElement('div');
  document.body.append(host);
  const view = renderConfiguredChart(host, {
    splitBy: 'node', series: [{ checkId: 11, metric: 'avg' }, { checkId: 12, metric: 'avg' }, { checkId: 21, metric: 'avg' }],
  }, { fetch: async (ids) => all.filter((s) => ids.includes(s.checkId)) });
  await waitFor(() => host.querySelectorAll('.chart-host').length === 2);
  const headings = [...host.querySelectorAll('.chart-host .section-title')].map((e) => e.textContent);
  assert.deepEqual(headings, ['Router', 'NAS']);
  view.destroy?.();
  host.remove();
});

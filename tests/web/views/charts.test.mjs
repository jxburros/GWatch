// web/views/charts.js picks the first saved chart (the mock seeds two — see
// web/mock.js's savedCharts) and renders it, with the saved-charts list and
// the config editor beside it.

import '../dom.mjs';
import '../../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as chartsView from '../../../web/views/charts.js';
import { mountView, settle, waitFor } from '../view-harness.mjs';

test('charts view opens on the first saved chart and titles the page after it', async (t) => {
  const { root, ctx } = await mountView(chartsView, undefined, t);
  assert.equal(ctx.setTitleCalls.at(-1).title, 'Gateway latency');
  assert.equal(root.querySelectorAll('.saved-item').length, 2);
  assert.ok(root.querySelector('.chart-card'), 'expected the main chart card to render');
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

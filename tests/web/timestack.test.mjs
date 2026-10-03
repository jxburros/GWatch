// Timestacked charts (web/chart-config.js): one metric read over several
// windows of the same length, each moved forward onto the current one. The
// fetch is the test's own, so the windows asked for can be checked exactly.

import { freshRoot } from './dom.mjs';
import test from 'node:test';
import assert from 'node:assert/strict';
import { normalizeChartConfig, normalizeTimestack, timestackLabel, loadTimestackSeries, describeChartConfig, renderConfiguredChart } from '../../web/chart-config.js';

const DAY = 86400e3;

test('normalizeTimestack fills defaults, clamps layers and turns off cleanly', () => {
  assert.equal(normalizeTimestack(null), null);
  assert.equal(normalizeTimestack({ enabled: false, period: '7d' }), null);
  assert.deepEqual(normalizeTimestack({}), { period: '24h', layers: 4 });
  assert.deepEqual(normalizeTimestack({ period: '7d', layers: 50 }), { period: '7d', layers: 8 });
  assert.deepEqual(normalizeTimestack({ period: 'bogus', layers: 1 }), { period: '24h', layers: 2 });
  assert.equal(normalizeChartConfig({}).timestack, null, 'an ordinary chart is not a timestack');
  assert.deepEqual(normalizeChartConfig({ timestack: { period: '3d', layers: '3' } }).timestack, { period: '3d', layers: 3 });
});

test('timestack layers are named for how far back they are', () => {
  assert.equal(timestackLabel('24h', 0), 'Last 24 hours');
  assert.equal(timestackLabel('24h', 1), 'Yesterday');
  assert.equal(timestackLabel('24h', 2), '2 days earlier');
  assert.equal(timestackLabel('3d', 1), '3 days earlier');
  assert.equal(timestackLabel('7d', 1), 'Week before');
  assert.equal(timestackLabel('7d', 3), '3 weeks earlier');
  assert.equal(timestackLabel('1h', 1), '1 hour earlier');
  assert.equal(timestackLabel('30d', 2), '60 days earlier');
  assert.match(describeChartConfig({ series: [{ checkId: 1, metric: 'avg' }], timestack: { period: '24h', layers: 3 } }), /timestacked 3 × 24 hours/);
});

test('loadTimestackSeries asks for each earlier window and shifts it onto the current one', async () => {
  const calls = [];
  const now = Date.UTC(2026, 9, 3, 12, 30, 45);
  const fetchFn = async (ids, range, metric, end) => {
    calls.push({ ids, range, metric, end });
    const to = end ?? now;
    return [{ checkId: ids[0], checkName: 'Ping', nodeName: 'Gateway', checkType: 'ping', from: new Date(to - DAY).toISOString(), to: new Date(to).toISOString(), points: [{ ts: new Date(to - 1000).toISOString(), avgMs: 5 }], summary: { avgMs: 5 } }];
  };
  const layers = await loadTimestackSeries({ series: [{ checkId: 7, metric: 'avg' }, { checkId: 9, metric: 'avg' }], timestack: { period: '24h', layers: 3 } }, fetchFn, now);
  assert.equal(layers.length, 3);
  assert.deepEqual(calls.map((c) => c.ids), [[7], [7], [7]], 'only the first series is stacked');
  const minute = Math.floor(now / 60e3) * 60e3;
  assert.deepEqual(calls.map((c) => c.end), [undefined, minute - DAY, minute - 2 * DAY], 'each window ends a period before the last');
  assert.ok(calls.every((c) => c.range === '24h' && c.metric === undefined));
  assert.deepEqual(layers.map((l) => l.shift), [0, DAY, 2 * DAY]);

  // A named metric is fetched by name.
  calls.length = 0;
  await loadTimestackSeries({ series: [{ checkId: 4, metric: 'cpu', named: true }], timestack: { period: '7d', layers: 2 } }, fetchFn, now);
  assert.deepEqual(calls.map((c) => [c.range, c.metric]), [['7d', 'cpu'], ['7d', 'cpu']]);
});

test('a timestacked chart draws one line per layer under the layer names', async () => {
  const root = freshRoot();
  const host = document.createElement('div');
  root.append(host);
  const fetchFn = async (ids, range, metric, end) => {
    const to = end ?? Date.now();
    return [{ checkId: 1, checkName: 'Ping', nodeName: 'Gateway', checkType: 'ping', from: new Date(to - DAY).toISOString(), to: new Date(to).toISOString(), points: [{ ts: new Date(to - 60e3).toISOString(), avgMs: 3 }], summary: { avgMs: 3 } }];
  };
  const view = renderConfiguredChart(host, { series: [{ checkId: 1, metric: 'avg' }], timestack: { period: '24h', layers: 3 } }, { fetch: fetchFn });
  await new Promise((r) => setTimeout(r, 20));
  assert.equal(view.charts.length, 1);
  const names = view.charts[0].data.series.map((s) => s.name);
  assert.deepEqual(names, ['Last 24 hours', 'Yesterday', '2 days earlier']);
  // Yesterday's point was moved forward a day, onto today's axis.
  const [today, yesterday] = view.charts[0].data.series;
  assert.ok(Math.abs(today.points[0].t - yesterday.points[0].t) < 120e3);
  assert.match(host.textContent, /Gateway › Ping/);
  view.destroy();
});

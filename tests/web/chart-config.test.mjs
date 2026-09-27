// web/chart-config.js — the "custom chart" feature. First the pure
// config-shaping helpers (which need dom.mjs only because the import chain
// pulls in web/charts.js; see charts-math.test.mjs), then the series model of
// #68 and #73: any metric of any check, an editor that lists them, and charts
// that put two units on two axes. The rendering tests hand
// renderConfiguredChart a fetch of their own, so no mock service is involved.

import { freshRoot } from './dom.mjs';
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  normalizeChartConfig, describeChartConfig, metricMeta, METRICS,
  normalizeSeries, seriesKey, checkMetricOptions, chartMetricSummary, chartConfigEditor, renderConfiguredChart, loadChartSeries, chartCsvItems,
} from '../../web/chart-config.js';
import { LineChart, groupByUnits } from '../../web/charts.js';

test('normalizeChartConfig fills in every default for an empty config', () => {
  const c = normalizeChartConfig({});
  assert.deepEqual(c.checkIds, []);
  assert.equal(c.metric, 'avg');
  assert.equal(c.range, '24h');
  assert.equal(c.style, 'area');
  assert.equal(c.smooth, false);
  assert.equal(c.lineWidth, 1.75);
  assert.equal(c.shadeFailures, true);
  assert.equal(c.legend, true);
  assert.equal(c.grid, true);
  assert.equal(c.yMin, null);
  assert.equal(c.yMax, null);
  assert.equal(c.threshold, null);
  assert.equal(c.height, 260);
  assert.deepEqual(c.colors, {});
});

test('normalizeChartConfig rejects out-of-range values rather than passing them through', () => {
  const c = normalizeChartConfig({ metric: 'nonsense', range: 'nonsense', style: 'nonsense', lineWidth: 99, height: 5000 });
  assert.equal(c.metric, 'avg');
  assert.equal(c.range, '24h');
  assert.equal(c.style, 'area');
  assert.equal(c.lineWidth, 5); // clamped to the [0.5, 5] range
  assert.equal(c.height, 800); // clamped to the [120, 800] range
});

test('normalizeChartConfig coerces checkIds to positive numbers and drops junk', () => {
  const c = normalizeChartConfig({ checkIds: ['12', 34, 'not-a-number', 0, null] });
  assert.deepEqual(c.checkIds, [12, 34]);
});

test('normalizeChartConfig treats an empty/NaN yMin, yMax or threshold as "auto"/"none"', () => {
  const c = normalizeChartConfig({ yMin: '', yMax: 'nope', threshold: undefined });
  assert.equal(c.yMin, null);
  assert.equal(c.yMax, null);
  assert.equal(c.threshold, null);
  const c2 = normalizeChartConfig({ yMin: '0', yMax: '100', threshold: '40' });
  assert.equal(c2.yMin, 0);
  assert.equal(c2.yMax, 100);
  assert.equal(c2.threshold, 40);
});

test('metricMeta finds a known metric and falls back to the first one otherwise', () => {
  assert.equal(metricMeta('loss').unit, '%');
  assert.equal(metricMeta('bogus'), METRICS[0]);
});

test('describeChartConfig summarises metric, range, checks and style', () => {
  const desc = describeChartConfig({ metric: 'avg', range: '7d', checkIds: [1, 2], style: 'line' });
  assert.match(desc, /7 days/);
  assert.match(desc, /2 checks/);
  assert.match(desc, /line/);
  const auto = describeChartConfig({ checkIds: [] });
  assert.match(auto, /auto checks/);
  const one = describeChartConfig({ checkIds: [1] });
  assert.match(one, /1 check(?!s)/);
});

/* ---------- Series: any metric of any check (#68, #73) ---------- */

test('a chart saved before series opens as one series per check, colours and all', () => {
  const c = normalizeChartConfig({ metric: 'loss', checkIds: ['3', 7], colors: { 3: '#ff0000' } });
  assert.deepEqual(c.series, [{ checkId: 3, metric: 'loss' }, { checkId: 7, metric: 'loss' }]);
  assert.equal(c.colors['3:loss'], '#ff0000', 'the colour follows its series');
  // What an older GWatch reads is still there.
  assert.deepEqual(c.checkIds, [3, 7]);
  assert.equal(c.metric, 'loss');
  // Normalising twice changes nothing.
  assert.deepEqual(normalizeChartConfig(c).series, c.series);
});

test('series mix GWatch’s own measurements with metrics a check names itself', () => {
  const c = normalizeChartConfig({ series: [
    { checkId: 1, metric: 'avg' },
    { checkId: '9', metric: 'net:eth0.rx' },
    { checkId: 9, metric: 'net:eth0.rx', named: true }, // a duplicate
    { checkId: 4, metric: 'avg', named: true }, // an SNMP OID someone called "avg"
    { checkId: 0, metric: 'cpu' }, null, 'junk',
  ] });
  assert.deepEqual(c.series, [
    { checkId: 1, metric: 'avg' },
    { checkId: 9, metric: 'net:eth0.rx', named: true },
    { checkId: 4, metric: 'avg', named: true },
  ]);
  assert.notEqual(seriesKey(c.series[0]), seriesKey({ checkId: 1, metric: 'avg', named: true }));
  assert.deepEqual(c.checkIds, [1, 9, 4]);
  assert.equal(chartMetricSummary(c), '3 metrics');
  assert.match(describeChartConfig(c), /^3 metrics · 24 hours · 3 checks/);
  assert.equal(chartMetricSummary({ series: [{ checkId: 9, metric: 'cpu', named: true }] }), 'Processor');
  assert.deepEqual(normalizeSeries(undefined), []);
});

const machine = {
  id: 20, name: 'NAS', checks: [
    { id: 21, type: 'system', name: 'Hardware health', config: {} },
    { id: 22, type: 'ping', name: 'Ping', config: {} },
  ],
};
const machineResult = {
  metrics: { cpu: 12, memory: 40, 'net:eth0.rx': 1.2e6, 'net:eth0.tx': 3e5 },
  details: { metricResults: [
    { key: 'cpu', label: 'Processor', value: 12, unit: '%' },
    { key: 'memory', label: 'Memory', value: 40, unit: '%' },
    { key: 'net:eth0.rx', label: 'Network eth0 received', value: 1.2e6, unit: 'B/s' },
    { key: 'net:eth0.tx', label: 'Network eth0 sent', value: 3e5, unit: 'B/s' },
  ] },
};

test('checkMetricOptions lists what each kind of check can be charted by', () => {
  const sys = checkMetricOptions(machine.checks[0], machineResult);
  assert.deepEqual(sys.map((o) => o.metric), ['cpu', 'memory', 'net:eth0.rx', 'net:eth0.tx', 'availability']);
  assert.deepEqual(sys.map((o) => o.unit), ['%', '%', 'B/s', 'B/s', '%']);
  assert.ok(sys.slice(0, 4).every((o) => o.named), 'hardware readings are named metrics');
  assert.equal(sys.find((o) => o.metric === 'net:eth0.rx').label, 'Network eth0 received');
  const ping = checkMetricOptions(machine.checks[1]);
  assert.deepEqual(ping.map((o) => o.metric), ['avg', 'min', 'max', 'jitter', 'loss', 'availability']);
  assert.equal(ping[0].label, 'Latency (average)');
  const http = checkMetricOptions({ id: 5, type: 'http', name: 'Web' });
  assert.deepEqual(http.map((o) => o.metric), ['avg', 'min', 'max', 'availability'], 'jitter and loss are a ping’s alone');
  assert.equal(http[0].label, 'Response time (average)');
  const snmp = checkMetricOptions({ id: 6, type: 'snmp', name: 'Switch', config: { snmpOids: [{ name: 'Uplink in', unit: 'bit/s' }, { name: '' }] } });
  assert.deepEqual(snmp.map((o) => o.metric), ['Uplink in', 'avg', 'min', 'max', 'availability']);
});

const settle = () => new Promise((r) => setTimeout(r, 0));

test('the editor lists a hardware check’s metrics, fetching its node the first time it is opened', async () => {
  const root = freshRoot();
  const asked = [];
  const changes = [];
  const editor = chartConfigEditor({ series: [{ checkId: 22, metric: 'avg' }] }, {
    nodes: [machine], onChange: (v) => changes.push(v),
    loadNode: async (id) => { asked.push(id); return { ...machine, lastResults: { 21: machineResult } }; },
  });
  root.append(editor);
  assert.equal(editor.querySelector('fieldset.metric-fieldset > legend').textContent, 'Metrics');
  const [sysCheck, pingCheck] = editor.querySelectorAll('details.metric-check');
  assert.equal(pingCheck.open, true, 'a check with a ticked metric starts open');
  assert.equal(pingCheck.querySelector('[data-metric="avg"] input').checked, true);
  assert.equal(sysCheck.open, false);
  assert.deepEqual(asked, [], 'nothing is fetched until a machine is opened');
  sysCheck.open = true;
  sysCheck.dispatchEvent(new window.Event('toggle'));
  await settle(); await settle();
  assert.deepEqual(asked, [20]);
  const group = sysCheck.querySelector('[role="group"]');
  assert.equal(group.getAttribute('aria-label'), 'Metrics for NAS › Hardware health');
  const labels = [...group.querySelectorAll('label.checkbox')].map((l) => l.textContent);
  assert.deepEqual(labels, ['Processor%', 'Memory%', 'Network eth0 receivedB/s', 'Network eth0 sentB/s', 'Availability%']);
  const rx = group.querySelector('[data-metric="net:eth0.rx"] input');
  rx.checked = true;
  rx.dispatchEvent(new window.Event('change'));
  assert.deepEqual(changes.at(-1).series, [{ checkId: 22, metric: 'avg' }, { checkId: 21, metric: 'net:eth0.rx', named: true }]);
  assert.deepEqual(editor.value.checkIds, [22, 21]);
  // One colour per series, named for its check and its metric.
  const colours = [...editor.querySelectorAll('.series-color input[type="color"]')].map((i) => i.getAttribute('aria-label'));
  assert.deepEqual(colours, ['Colour for NAS › Ping — Latency (average)', 'Colour for NAS › Hardware health — Network eth0 received']);
  // Which metric an automatic chart plots only shows while nothing is ticked.
  const auto = [...editor.querySelectorAll('.field')].find((f) => f.textContent.startsWith('Metric for automatic checks'));
  assert.equal(auto.hidden, true);
});

/* ---------- Mixed units on one chart ---------- */

const T0 = Date.UTC(2026, 0, 1, 12, 0, 0);
const iso = (t) => new Date(t).toISOString();
function hist(checkId, checkType, points, extra = {}) {
  return { checkId, checkName: checkType === 'system' ? 'Hardware health' : 'Ping', nodeName: 'NAS', checkType, from: iso(T0), to: iso(T0 + 120e3), bucketSeconds: 0, summary: { availability: 100, avgMs: extra.avg ?? null }, points, ...extra };
}
const pingHist = hist(22, 'ping', [0, 1, 2].map((i) => ({ ts: iso(T0 + i * 60e3), avgMs: 10 + i, minMs: 9, maxMs: 12, lossPct: 0, availability: 100 })), { avg: 11 });
const named = (metric, unit, vals) => hist(21, 'system', vals.map((v, i) => ({ ts: iso(T0 + i * 60e3), value: v, avgMs: v, availability: 100 })), { metric, metricUnit: unit, avg: vals.reduce((a, b) => a + b, 0) / vals.length });
/** Stands in for /api/history: latency for the ping, two hardware readings,
 *  and a refusal for anything else, as the service refuses a metric a check
 *  does not measure. */
function fakeFetch(calls = []) {
  return async (ids, range, metric) => {
    calls.push([ids.join(','), metric || '']);
    if (!metric) return ids.includes(22) ? [pingHist] : [];
    if (metric === 'cpu') return named('cpu', '%', [10, 20, 30]);
    if (metric === 'net:eth0.rx') return named('net:eth0.rx', 'B/s', [1e6, 2e6, 1.5e6]);
    throw new Error('check 21 does not measure it');
  };
}

test('loadChartSeries asks for latency once and for each named metric on its own', async () => {
  const calls = [];
  const out = await loadChartSeries({ series: [{ checkId: 22, metric: 'avg' }, { checkId: 21, metric: 'cpu', named: true }, { checkId: 22, metric: 'loss' }, { checkId: 21, metric: 'gone', named: true }] }, fakeFetch(calls));
  assert.deepEqual(calls, [['22', ''], ['21', 'cpu'], ['21', 'gone']]);
  assert.deepEqual(out.map((e) => e.spec.metric), ['avg', 'cpu', 'loss']);
  assert.equal(out.missing, 1, 'a metric the check no longer measures is counted, not fatal');
});

test('two units share one chart on a left and a right axis', async (t) => {
  const root = freshRoot();
  const view = renderConfiguredChart(root, { series: [{ checkId: 22, metric: 'avg' }, { checkId: 21, metric: 'net:eth0.rx', named: true }] }, { title: 'Mixed', fetch: fakeFetch() });
  t.after(() => view.destroy());
  await settle(); await settle();
  assert.equal(view.charts.length, 1);
  const chart = view.charts[0];
  assert.deepEqual(chart.data.series.map((s) => [s.unit, s.axis]), [['ms', 0], ['B/s', 1]]);
  const { yt, yt2 } = chart._scales(600, 200);
  assert.equal(yt.unit, 'ms');
  assert.equal(yt2.unit, 'B/s');
  assert.ok(yt2.labels.every((l) => / MB\/s$/.test(l)), `the right axis reads in MB/s: ${yt2.labels}`);
  // Each series is named for its metric too, since the chart mixes them.
  assert.deepEqual(chart.data.series.map((s) => s.name), ['NAS › Ping — Latency (average)', 'NAS › Hardware health — Network eth0 received']);
  const legend = [...root.querySelectorAll('.legend-item')].map((el) => el.textContent);
  assert.match(legend[0], /ms · left axis$/);
  assert.match(legend[1], /B\/s · right axis$/);
  assert.match(chart.summaryEl.textContent, /Network eth0 received \(B\/s, right axis\): 3 samples, lowest 1\.0 MB\/s .* highest 2\.0 MB\/s .* latest 1\.5 MB\/s/);
  // The table writes each column in its own unit.
  const details = root.querySelector('details.chart-data');
  details.open = true;
  details.dispatchEvent(new window.Event('toggle'));
  assert.deepEqual([...details.querySelectorAll('tbody tr')[0].querySelectorAll('td')].map((td) => td.textContent), ['10.0 ms', '1.0 MB/s', '100%']);
  // Stat tiles: each series' average, in its own unit.
  const tiles = [...root.querySelectorAll('.chart-stats .stat-value')].map((el) => el.textContent);
  assert.deepEqual(tiles, ['11.0 ms', '1.5 MB/s']);
});

test('a third unit starts another chart, and says so', async (t) => {
  const root = freshRoot();
  const view = renderConfiguredChart(root, { series: [{ checkId: 22, metric: 'avg' }, { checkId: 21, metric: 'cpu', named: true }, { checkId: 21, metric: 'net:eth0.rx', named: true }] }, { title: 'Three', fetch: fakeFetch() });
  t.after(() => view.destroy());
  await settle(); await settle();
  assert.equal(view.charts.length, 2);
  assert.deepEqual(view.charts.map((c) => c._units), [['ms', '%'], ['B/s']]);
  assert.match(root.querySelector('.chart-split-note').textContent, /3 different units .* drawn as 2 charts/);
  assert.deepEqual(groupByUnits([{ unit: 'a' }, { unit: 'b' }, { unit: 'a' }, { unit: 'c' }]).map((g) => g.units), [['a', 'b'], ['c']]);
});

test('a single-unit LineChart keeps one axis, now in readable rates', (t) => {
  const root = freshRoot();
  const chart = new LineChart(root, { unit: 'B/s' });
  t.after(() => chart.destroy());
  chart.setData({ series: [{ name: 'Download', points: [{ t: T0, v: 250_000 }, { t: T0 + 60e3, v: 750_000 }] }], from: T0, to: T0 + 60e3 });
  const { yt, yt2 } = chart._scales(600, 200);
  assert.equal(yt2, null);
  // One unit, picked by the top tick, and the decimals the step needs.
  assert.deepEqual(yt.labels, ['0.0 MB/s', '0.2 MB/s', '0.4 MB/s', '0.6 MB/s', '0.8 MB/s', '1.0 MB/s']);
  assert.match(chart.summaryEl.textContent, /Download: 2 samples, lowest 250\.0 kB\/s .* highest 750\.0 kB\/s/);
});

test('CSV links export a named metric’s own values', () => {
  const items = chartCsvItems({ series: [{ checkId: 22, metric: 'avg' }, { checkId: 22, metric: 'loss' }, { checkId: 21, metric: 'net:eth0.rx', named: true }] }, { nodes: [machine] });
  assert.equal(items.length, 2, 'one latency export per check');
  assert.match(items[1].href, /checkId=21.*metric=net%3Aeth0\.rx/);
  assert.equal(items[1].label, 'Export CSV — NAS › Hardware health — Network eth0 received');
});

// The text alternatives and keyboard access of web/charts.js (#38): a
// LineChart's canvas is a picture to assistive technology, so the same
// figures must be there as text — a summary the canvas is described by, a
// table under "View as table" — and the tooltip must be reachable with the
// arrow keys. jsdom draws nothing, but the DOM the chart builds around its
// canvas is what these tests are about. The chart's hit-test reads its own
// layout, which is all zeros without a rendering engine, so its
// getBoundingClientRect is given a real-looking width here.

import { freshRoot } from './dom.mjs';
import test from 'node:test';
import assert from 'node:assert/strict';
import { LineChart, uptimeBar } from '../../web/charts.js';

const T0 = Date.UTC(2026, 0, 1, 12, 0, 0);
const series = [
  { name: 'Ping', points: [{ t: T0, v: 10, avail: 100 }, { t: T0 + 60e3, v: 30, avail: 100 }, { t: T0 + 120e3, v: 20, avail: 50 }] },
  { name: 'HTTP', points: [{ t: T0, v: 100 }, { t: T0 + 60e3, v: 200 }, { t: T0 + 120e3, v: null, avail: 0 }] },
];

function sized(chart) {
  const rect = () => ({ left: 0, top: 0, width: 600, height: 200, right: 600, bottom: 200 });
  chart.el.getBoundingClientRect = rect;
  chart.canvas.getBoundingClientRect = rect;
  return chart;
}

test('LineChart describes its canvas by a text summary and offers the data as a table', (t) => {
  const root = freshRoot();
  const chart = new LineChart(root, { unit: 'ms', ariaLabel: 'Latency history' });
  t.after(() => chart.destroy());
  chart.setData({ series, from: T0, to: T0 + 120e3, bucketSeconds: 60 });
  const canvas = root.querySelector('canvas');
  assert.equal(canvas.getAttribute('role'), 'img');
  assert.equal(canvas.getAttribute('tabindex'), '0', 'the canvas is in the tab order');
  const summary = document.getElementById(canvas.getAttribute('aria-describedby'));
  assert.ok(summary, 'aria-describedby resolves');
  assert.match(summary.textContent, /Ping: 3 samples, lowest 10\.0 ms .* highest 30\.0 ms .* latest 20\.0 ms/);
  assert.match(summary.textContent, /HTTP: 2 samples/);

  const details = root.querySelector('details.chart-data');
  assert.ok(details, 'a "View as table" details follows the chart');
  assert.equal(details.querySelector('table'), null, 'the table is not built until it is wanted');
  details.open = true;
  details.dispatchEvent(new window.Event('toggle'));
  const table = details.querySelector('table');
  assert.ok(table);
  assert.deepEqual([...table.querySelectorAll('thead th')].map((th) => th.textContent), ['Time', 'Ping', 'HTTP', 'Availability']);
  const rows = [...table.querySelectorAll('tbody tr')];
  assert.equal(rows.length, 3, 'one row per distinct time');
  assert.deepEqual([...rows[2].querySelectorAll('td')].map((td) => td.textContent), ['20.0 ms', 'failed', '0%']);
  // While open, new data rewrites the table.
  chart.setData({ series: [series[0]], from: T0, to: T0 + 120e3, bucketSeconds: 60 });
  assert.deepEqual([...details.querySelectorAll('thead th')].map((th) => th.textContent), ['Time', 'Ping', 'Availability']);
});

test('LineChart steps the tooltip through the points with the arrow keys and announces each stop', (t) => {
  const root = freshRoot();
  const chart = sized(new LineChart(root, { unit: 'ms' }));
  t.after(() => chart.destroy());
  chart.setData({ series, from: T0, to: T0 + 120e3, bucketSeconds: 60 });
  const canvas = chart.canvas;
  const live = root.querySelector('[aria-live="polite"]');
  const press = (key) => canvas.dispatchEvent(new window.KeyboardEvent('keydown', { key, bubbles: true, cancelable: true }));
  assert.equal(chart.tooltip.hidden, true);
  press('ArrowRight');
  assert.equal(chart.tooltip.hidden, false, 'ArrowRight shows the tooltip at the first point');
  assert.equal(chart.hover.t, T0);
  assert.match(live.textContent, /Ping 10\.0 ms, HTTP 100 ms/);
  press('ArrowRight');
  assert.equal(chart.hover.t, T0 + 60e3);
  press('End');
  assert.equal(chart.hover.t, T0 + 120e3);
  assert.match(live.textContent, /HTTP failed/);
  press('ArrowRight');
  assert.equal(chart.hover.t, T0 + 120e3, 'End is the last stop');
  press('Home');
  assert.equal(chart.hover.t, T0);
  press('ArrowLeft');
  assert.equal(chart.hover.t, T0, 'Home is the first stop');
  press('Escape');
  assert.equal(chart.tooltip.hidden, true, 'Escape hides the tooltip');
  assert.equal(chart.hover, null);
});

test('a wallboard-style chart can leave the table out', (t) => {
  const root = freshRoot();
  const chart = new LineChart(root, { table: false });
  t.after(() => chart.destroy());
  assert.equal(root.querySelector('details'), null);
  assert.ok(root.querySelector('canvas[aria-describedby]'), 'the summary still describes it');
});

test('uptimeBar names itself after what it shows and sums up its periods', () => {
  const points = [
    { ts: new Date(T0).toISOString(), availability: 100 },
    { ts: new Date(T0 + 60e3).toISOString(), availability: 0 },
    { ts: new Date(T0 + 120e3).toISOString(), availability: 50 },
  ];
  const bar = uptimeBar(points, { bucketSeconds: 60, from: T0, to: T0 + 180e3, label: 'Office router › Ping' });
  assert.equal(bar.getAttribute('role'), 'img');
  const name = bar.getAttribute('aria-label');
  assert.match(name, /^Availability per period — Office router › Ping: 1 of 3 periods fully up, 1 partial, 1 down, first trouble /);
  assert.equal(uptimeBar([], { label: 'x' }).getAttribute('aria-label'), 'Availability per period — x: no data');
});

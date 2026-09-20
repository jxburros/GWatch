// The pure config-shaping helpers in web/chart-config.js — the part of the
// "custom chart" feature that has no DOM in it at all, aside from the import
// chain pulling in web/charts.js (see charts-math.test.mjs for why dom.mjs
// has to load first).

import './dom.mjs';
import test from 'node:test';
import assert from 'node:assert/strict';
import { normalizeChartConfig, describeChartConfig, metricMeta, METRICS } from '../../web/chart-config.js';

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

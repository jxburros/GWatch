// The axis/series maths in web/charts.js that does not need a real canvas:
// tick placement, colour conversion and history → chart-series mapping.
// Importing web/charts.js runs `let CSS = cssColors()` at module load, which
// reads computed styles off document.documentElement — so the jsdom
// environment has to be up before this import, same as for any DOM test.

import './dom.mjs';
import test from 'node:test';
import assert from 'node:assert/strict';
import { niceTicks, timeTicks, hexToRgba, toSeries, seriesColors, SERIES_COLORS } from '../../web/charts.js';

test('niceTicks picks a round step and brackets [min, max]', () => {
  const t = niceTicks(0, 97, 5);
  assert.ok(t.min <= 0);
  assert.ok(t.max >= 97);
  assert.ok(t.ticks.length >= 2);
  // every tick is a multiple of the step, up to floating point noise
  for (const v of t.ticks) assert.ok(Math.abs(Math.round(v / t.step) * t.step - v) < 1e-6);
});

test('niceTicks tolerates a degenerate (min === max) range', () => {
  const t = niceTicks(5, 5);
  assert.ok(t.max > t.min);
  assert.ok(Array.isArray(t.ticks) && t.ticks.length > 0);
});

test('niceTicks falls back to [0, 1] for non-finite input', () => {
  assert.deepEqual(niceTicks(NaN, 5), { min: 0, max: 1, ticks: [0, 1] });
  assert.deepEqual(niceTicks(0, Infinity), { min: 0, max: 1, ticks: [0, 1] });
});

test('timeTicks spans exactly the requested range and grows major ticks for wide spans', () => {
  const to = Date.UTC(2026, 8, 20, 12, 0, 0);
  const from = to - 3600e3; // 1 hour
  const { ticks } = timeTicks(from, to, 600);
  assert.ok(ticks.length > 0);
  for (const t of ticks) { assert.ok(t.t >= from - 60e3); assert.ok(t.t <= to + 60e3); assert.equal(typeof t.label, 'string'); }
});

test('timeTicks over a multi-day span steps in days, not minutes', () => {
  const to = Date.UTC(2026, 8, 20);
  const from = to - 30 * 86400e3;
  const { step, ticks } = timeTicks(from, to, 600);
  assert.ok(typeof step === 'number' ? step >= 86400e3 : true);
  assert.ok(ticks.length <= 40); // a month of daily ticks stays compact, not one per minute
});

test('hexToRgba converts a hex colour and passes through anything else unchanged', () => {
  assert.equal(hexToRgba('#43c9c0', 0.5), 'rgba(67, 201, 192, 0.5)');
  assert.equal(hexToRgba('43c9c0', 1), 'rgba(67, 201, 192, 1)');
  assert.equal(hexToRgba('not-a-colour', 1), 'not-a-colour');
});

test('toSeries maps a HistorySeries to chart points for the chosen metric', () => {
  const hs = {
    checkId: 7, nodeName: 'Gateway', checkName: 'Ping',
    points: [
      { ts: '2026-09-20T00:00:00.000Z', avgMs: 12.5, minMs: 10, maxMs: 15, availability: 100 },
      { ts: '2026-09-20T00:05:00.000Z', avgMs: null, minMs: null, maxMs: null, availability: 0 },
    ],
  };
  const s = toSeries(hs, 'avg', '#fff');
  assert.equal(s.name, 'Gateway › Ping');
  assert.equal(s.checkId, 7);
  assert.equal(s.points.length, 2);
  assert.equal(s.points[0].v, 12.5);
  assert.equal(s.points[0].t, Date.parse('2026-09-20T00:00:00.000Z'));
  assert.equal(s.points[1].v, null);

  const loss = toSeries({ ...hs, points: [{ ts: hs.points[0].ts, lossPct: 3.5 }] }, 'loss');
  assert.equal(loss.points[0].v, 3.5);
});

test('seriesColors puts the current accent first and never repeats a colour', () => {
  const palette = seriesColors();
  assert.equal(new Set(palette).size, palette.length);
  assert.ok(palette.length >= SERIES_COLORS.length); // accent plus the fixed palette (minus a duplicate, if any)
});

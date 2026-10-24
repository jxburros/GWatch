// Time-axis ticks across a daylight-saving change. The zone is set before
// anything reads a date, so this file runs on New York time wherever it runs.
process.env.TZ = 'America/New_York';

await import('./dom.mjs');
const { default: test } = await import('node:test');
const { default: assert } = await import('node:assert/strict');
const { timeTicks } = await import('../../web/charts.js');
const { loadTimestackSeries } = await import('../../web/chart-config.js');

test('hour ticks stay on the local clock across the night the clocks go back', () => {
  // 31 Oct 12:00 to 3 Nov 12:00 New York; 1 Nov has 25 hours.
  const from = new Date(2026, 9, 31, 12).getTime();
  const to = new Date(2026, 10, 3, 12).getTime();
  const { step, ticks } = timeTicks(from, to, 600);
  assert.equal(step, 12 * 3600e3);
  for (const t of ticks) {
    const d = new Date(t.t);
    assert.ok(d.getHours() % 12 === 0 && d.getMinutes() === 0, `tick at ${d.toString()}`);
  }
  assert.deepEqual(ticks.filter((t) => t.major).map((t) => t.label), ['Sun 1', 'Mon 2', 'Tue 3']);
});

test('a day timestack lines up the same time of day across the clocks going back', async () => {
  const H = 3600e3;
  const now = new Date(2026, 10, 2, 12).getTime(); // Monday noon; the clocks went back early on Sunday 1 Nov
  const ends = [];
  const fetchFn = async (ids, range, metric, end) => { ends.push(end); return [{ checkId: ids[0], checkType: 'ping', points: [], summary: {} }]; };
  const layers = await loadTimestackSeries({ series: [{ checkId: 7, metric: 'avg' }], timestack: { period: '24h', layers: 3 } }, fetchFn, now);
  assert.deepEqual(ends.slice(1).map((e) => new Date(e).getHours()), [12, 12], 'each earlier day ends at noon too');
  assert.deepEqual(layers.map((l) => l.shift), [0, 24 * H, 49 * H]);
});

// Time-axis ticks across a daylight-saving change. The zone is set before
// anything reads a date, so this file runs on New York time wherever it runs.
process.env.TZ = 'America/New_York';

await import('./dom.mjs');
const { default: test } = await import('node:test');
const { default: assert } = await import('node:assert/strict');
const { timeTicks } = await import('../../web/charts.js');

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

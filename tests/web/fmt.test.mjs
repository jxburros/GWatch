// web/fmt.js is pure — no DOM, no fetch — so it is tested directly, with a
// fixed `now` wherever a function reads the clock, so the assertions do not
// flap with the wall clock.

import test from 'node:test';
import assert from 'node:assert/strict';
import * as fmt from '../../web/fmt.js';

test('relTime formats both directions around "now"', () => {
  const now = Date.UTC(2026, 8, 20, 12, 0, 0);
  assert.equal(fmt.relTime(now - 2000, now), 'just now');
  assert.equal(fmt.relTime(now - 45000, now), '45 s ago');
  assert.equal(fmt.relTime(now - 5 * 60000, now), '5 min ago');
  assert.equal(fmt.relTime(now - 2 * 3600000, now), '2 h ago');
  assert.equal(fmt.relTime(now - 3 * 86400000, now), '3 d ago');
  assert.equal(fmt.relTime(now + 3000, now), 'in a moment');
  assert.equal(fmt.relTime(now + 30000, now), 'in 30 s');
  assert.equal(fmt.relTime(now + 5 * 60000, now), 'in 5 min');
  assert.equal(fmt.relTime(null, now), '—');
});

test('duration formats seconds as the largest sensible unit', () => {
  assert.equal(fmt.duration(0), '0 s');
  assert.equal(fmt.duration(45), '45 s');
  assert.equal(fmt.duration(90), '1 m 30 s');
  assert.equal(fmt.duration(120), '2 min');
  assert.equal(fmt.duration(3661), '1 h 1 m');
  assert.equal(fmt.duration(3600), '1 h');
  assert.equal(fmt.duration(90000), '1 d 1 h');
  assert.equal(fmt.duration(null), '—');
});

test('ms scales from sub-millisecond to seconds', () => {
  assert.equal(fmt.ms(0.4), '0.40 ms');
  assert.equal(fmt.ms(4.2), '4.2 ms');
  assert.equal(fmt.ms(42), '42.0 ms');
  assert.equal(fmt.ms(150), '150 ms');
  assert.equal(fmt.ms(1200), '1.20 s');
  assert.equal(fmt.ms(12000), '12.0 s');
  assert.equal(fmt.ms(null), '—');
});

test('pct rounds to the given precision but keeps 0% and 100% bare', () => {
  assert.equal(fmt.pct(0), '0%');
  assert.equal(fmt.pct(100), '100%');
  assert.equal(fmt.pct(99.949), '99.9%');
  assert.equal(fmt.pct(50, 0), '50%');
});

test('bytes picks a unit and trims decimals above 10 of it', () => {
  assert.equal(fmt.bytes(0), '0 B');
  assert.equal(fmt.bytes(512), '512 B');
  assert.equal(fmt.bytes(2048), '2.0 KB');
  assert.equal(fmt.bytes(1024 * 1024 * 15), '15 MB');
  assert.equal(fmt.bytes(1024 ** 3 * 2.5), '2.5 GB');
});

test('num adds locale thousands separators', () => {
  assert.equal(fmt.num(1234567), (1234567).toLocaleString());
  assert.equal(fmt.num(null), '—');
});

test('dateTime, dateShort and timeShort pad consistently', () => {
  const d = new Date(2026, 0, 5, 9, 4, 7); // 5 Jan 2026, 09:04:07 local
  assert.equal(fmt.dateTime(d), '5 Jan 2026, 09:04:07');
  assert.equal(fmt.dateTime(d, { seconds: false }), '5 Jan 2026, 09:04');
  assert.equal(fmt.dateShort(d), '5 Jan 2026');
  assert.equal(fmt.timeShort(d), '09:04');
  assert.equal(fmt.timeShort(d, { seconds: true }), '09:04:07');
});

test('dayHeading recognises today and yesterday relative to `now`', () => {
  const now = new Date(2026, 8, 20, 15, 0, 0);
  assert.equal(fmt.dayHeading(new Date(2026, 8, 20, 3, 0, 0), now), 'Today');
  assert.equal(fmt.dayHeading(new Date(2026, 8, 19, 23, 0, 0), now), 'Yesterday');
  assert.equal(fmt.dayHeading(new Date(2026, 8, 15, 12, 0, 0), now), 'Tuesday 15 September');
  assert.equal(fmt.dayHeading(new Date(2025, 8, 15, 12, 0, 0), now), 'Monday 15 September 2025');
});

test('dayKey is a zero-padded local ISO date', () => {
  assert.equal(fmt.dayKey(new Date(2026, 0, 5)), '2026-01-05');
  assert.equal(fmt.dayKey(null), '');
});

test('interval reads back the same units the scheduler is configured in', () => {
  assert.equal(fmt.interval(30), '30 s');
  assert.equal(fmt.interval(60), '1 min');
  assert.equal(fmt.interval(300), '5 min');
  assert.equal(fmt.interval(3600), '1 h');
  assert.equal(fmt.interval(7200), '2 h');
});

test('rangeLabel and rangeMs cover every entry in RANGES', () => {
  for (const r of fmt.RANGES) {
    assert.notEqual(fmt.rangeLabel(r), r); // every known range has a human label
    assert.ok(fmt.rangeMs(r) > 0);
  }
  assert.equal(fmt.rangeLabel('bogus'), 'bogus'); // falls back to the raw value
});

test('plural picks singular vs. plural, with an irregular form when given one', () => {
  assert.equal(fmt.plural(1, 'check'), '1 check');
  assert.equal(fmt.plural(2, 'check'), '2 checks');
  assert.equal(fmt.plural(3, 'entry', 'entries'), '3 entries');
});

test('weekdayShort / weekdayLong index into fixed day names', () => {
  assert.equal(fmt.weekdayShort(0), 'Sun');
  assert.equal(fmt.weekdayLong(0), 'Sunday');
  assert.equal(fmt.weekdayShort(9), ''); // out of range is silently empty, not a throw
});

test('toLocalInput / fromLocalInput round-trip a datetime-local value', () => {
  const d = new Date(2026, 5, 1, 8, 30);
  const s = fmt.toLocalInput(d);
  assert.equal(s, '2026-06-01T08:30');
  assert.equal(fmt.fromLocalInput(''), null);
  assert.equal(new Date(fmt.fromLocalInput(s)).getMinutes(), 30);
});

test('retentionSpan reads a day count back as the roundest unit it divides into', () => {
  assert.equal(fmt.retentionSpan(0), 'forever');
  assert.equal(fmt.retentionSpan(60), '2 months');
  assert.equal(fmt.retentionSpan(365), '1 year');
  assert.equal(fmt.retentionSpan(14), '2 weeks');
  assert.equal(fmt.retentionSpan(10), '10 days');
});

test('isBeta is true only for a 0.x version, not a dev or CI build', () => {
  assert.equal(fmt.isBeta('0.4.1'), true);
  assert.equal(fmt.isBeta('v0.9.0'), true);
  assert.equal(fmt.isBeta('1.0.0'), false);
  assert.equal(fmt.isBeta('dev'), false);
  assert.equal(fmt.isBeta('ci-abc1234'), false);
});

test('compareVersions orders releases, and refuses to guess', () => {
  assert.equal(fmt.compareVersions('0.5.0', '0.4.0'), 1);
  assert.equal(fmt.compareVersions('0.4.0', '0.5.0'), -1);
  assert.equal(fmt.compareVersions('0.4.0', '0.4.0'), 0);
  assert.equal(fmt.compareVersions('v1.2.3', '1.2.3'), 0);
  assert.equal(fmt.compareVersions('1.10.0', '1.9.0'), 1, 'parts are numbers, not text');
  assert.equal(fmt.compareVersions('1.2', '1.2.0'), 0, 'missing parts are zero');
  assert.equal(fmt.compareVersions('1.2.0', '1.2.0-rc1'), 1, 'a release beats its own candidate');
  // Anything that is not a version compares equal, so nothing is ever marked
  // out of date on the strength of a string nobody can read.
  assert.equal(fmt.compareVersions('dev', '1.0.0'), 0);
  assert.equal(fmt.compareVersions('', '1.0.0'), 0);
  assert.equal(fmt.compareVersions(null, undefined), 0);
});

test('agentIsBehind only marks a machine when both versions are known', () => {
  assert.equal(fmt.agentIsBehind('0.4.0', '0.5.0'), true);
  assert.equal(fmt.agentIsBehind('0.5.0', '0.5.0'), false);
  assert.equal(fmt.agentIsBehind('0.6.0', '0.5.0'), false, 'a newer agent is not behind');
  assert.equal(fmt.agentIsBehind('', '0.5.0'), false);
  assert.equal(fmt.agentIsBehind('0.4.0', ''), false, 'no release to compare with marks nothing');
  assert.equal(fmt.agentIsBehind('dev', '0.5.0'), false, 'a development build is not out of date');
});

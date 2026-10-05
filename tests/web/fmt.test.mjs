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

// #67: a throughput is written in the largest decimal unit that fits, the
// way the service's alerts write one (checks.bytesPerSecond), never as a raw
// count of bytes.
test('rate writes a throughput in decimal units, as the service does', () => {
  assert.equal(fmt.rate(0), '0 B/s');
  assert.equal(fmt.rate(999), '999 B/s');
  assert.equal(fmt.rate(1000), '1.0 kB/s');
  assert.equal(fmt.rate(12_345), '12.3 kB/s');
  assert.equal(fmt.rate(1_500_000), '1.5 MB/s');
  assert.equal(fmt.rate(2.5e9), '2.5 GB/s');
  assert.equal(fmt.rate(125_000_000, 'bit/s'), '125.0 Mbit/s');
  assert.equal(fmt.rate(null), '—');
  // An alert-style reading agrees with the chart.
  assert.equal(fmt.metricValue(1_500_000, 'B/s'), '1.5 MB/s');
});

test('unitValue writes any reading in its own unit', () => {
  assert.equal(fmt.unitValue(12.34, 'ms'), '12.3 ms');
  assert.equal(fmt.unitValue(1500, 'ms'), '1.50 s');
  assert.equal(fmt.unitValue(42.25, '%'), '42.3%');
  assert.equal(fmt.unitValue(3_200_000, 'B/s'), '3.2 MB/s');
  assert.equal(fmt.unitValue(0.534, ''), '0.53', 'a load average is a bare number');
  assert.equal(fmt.unitValue(21.456, '°C'), '21.5 °C');
  assert.equal(fmt.unitValue(undefined, 'B/s'), '—');
});

test('unitAxis writes every tick on an axis in one scaled unit', () => {
  // Ticks 0 … 2 MB/s in steps of 0.5 MB/s: one unit, one decimal, all the way up.
  const yt = { max: 2e6, step: 5e5 };
  assert.deepEqual([0, 5e5, 1e6, 1.5e6, 2e6].map((v) => fmt.unitAxis(v, 'B/s', yt)), ['0.0 MB/s', '0.5 MB/s', '1.0 MB/s', '1.5 MB/s', '2.0 MB/s']);
  assert.deepEqual([0, 2e5, 4e5].map((v) => fmt.unitAxis(v, 'B/s', { max: 4e5, step: 2e5 })), ['0 kB/s', '200 kB/s', '400 kB/s']);
  assert.equal(fmt.unitAxis(50, '%'), '50%');
  assert.equal(fmt.unitAxis(40, 'ms'), '40 ms');
  assert.equal(fmt.unitAxis(0.5, '', { max: 2, step: 0.5 }), '0.5');
});

test('sortNodes puts nodes in alphabetical, address, importance or status order', () => {
  const list = [
    { name: 'node 10', host: '192.168.1.10', status: 'up', importance: 'low' },
    { name: 'Node 9', host: '192.168.1.9', status: 'down', importance: 'normal' },
    { name: 'alpha', host: '10.0.0.1', status: 'degraded', importance: 'critical' },
  ];
  const names = (key) => fmt.sortNodes(list, key).map((n) => n.name);
  assert.deepEqual(names('name'), ['alpha', 'Node 9', 'node 10'], 'case ignored, numbers in number order');
  assert.deepEqual(names('name-desc'), ['node 10', 'Node 9', 'alpha']);
  assert.deepEqual(names('host'), ['alpha', 'Node 9', 'node 10']);
  assert.deepEqual(names('importance'), ['alpha', 'Node 9', 'node 10']);
  assert.deepEqual(names('status'), ['Node 9', 'alpha', 'node 10'], 'worst first');
  assert.equal(list[0].name, 'node 10', 'the list given is not reordered');
  const wrapped = list.map((n) => ({ node: n, status: n.status }));
  assert.deepEqual(fmt.sortNodes(wrapped, 'name', { nodeOf: (r) => r.node }).map((r) => r.node.name), ['alpha', 'Node 9', 'node 10']);
});

test('sub-millisecond latency ticks have distinct labels', () => {
  assert.deepEqual([0, 0.05, 0.1, 0.15].map((v) => fmt.unitAxis(v, 'ms', { max: 0.15, step: 0.05 })), ['0.00 ms', '0.05 ms', '0.10 ms', '0.15 ms']);
});

// The mock's history series: latency, availability and named metrics over a range.
//
// Part of mock.js, split out for #118: the section is the
// mock's own, moved here unchanged inside install(m), which runs at the
// point it used to and hands it the bindings it reads.

/** @param {Record<string, any>} m the mock's state and helpers */
export function install(m) {
  const { CHECK_PROFILES, DAY, HOUR, checkMetricUnits, findCheck, iso, jsonReading, matterReading, mockReading, rng, snmpReading, systemMetricResults } = m;

  /* ---------- History ---------- */
  // metricHistory serves one of a check's named metrics — an SNMP check's
  // OID, a json check's recorded value — the way the server does: raw points
  // only, with the metric's value repeated in avgMs so the chart helpers can
  // plot it without knowing it is not a latency.
  function metricHistory(checkId, range, metric, end) {
    const base = history(checkId, range, end);
    const { c } = findCheck(checkId);
    const units = checkMetricUnits(c);
    if (!(metric in units)) throw Object.assign(new Error(`check ${checkId} does not measure "${metric}"`), { status: 400 });
    const o = (c.config?.snmpOids || []).find((x) => x.name === metric);
    const mr = (c.config?.matterReadings || []).find((x) => x.name === metric);
    const r = rng(c.id * 977 + metric.length);
    const points = base.points.map((p) => {
      if (p.avgMs == null && c.type !== 'system') return { ...p, avgMs: null, minMs: null, maxMs: null, value: null };
      let v;
      if (c.type === 'system') v = systemMetricResults(c, mockReading('agent:1', 'nas.lan', 29, +new Date(p.ts))).find((m) => m.key === metric)?.value;
      else if (o) { const reading = snmpReading(o, +new Date(p.ts), r); v = reading.value != null ? reading.value : reading.rate; }
      else if (mr) v = matterReading(mr, +new Date(p.ts), r).value;
      else v = jsonReading(c, +new Date(p.ts));
      return { ...p, value: v ?? null, avgMs: v ?? null, minMs: v ?? null, maxMs: v ?? null, jitterMs: null, lossPct: null };
    });
    return { ...base, source: 'raw', bucketSeconds: 0, metric, metricUnit: units[metric], points };
  }

  // `end` (ms) moves the window back, as the service's `end` does for a
  // timestacked chart; absent or in the future, the window ends now.
  function history(checkId, range, end) {
    const found = findCheck(checkId);
    if (!found) throw Object.assign(new Error('check not found'), { status: 404 });
    const { n, c } = found;
    const p = CHECK_PROFILES[c.id];
    const spans = { '1h': HOUR, '24h': DAY, '3d': 3 * DAY, '7d': 7 * DAY, '30d': 30 * DAY, '1y': 365 * DAY };
    const buckets = { '1h': [Math.min(60, c.intervalSeconds), 'raw'], '24h': [Math.max(60, c.intervalSeconds), 'raw'], '3d': [300, '5m'], '7d': [300, '5m'], '30d': [3600, '1h'], '1y': [86400, '1d'] };
    const span = spans[range] || DAY;
    const [bucketSec, source] = buckets[range] || buckets['24h'];
    const real = Date.now();
    const to = end != null && Number.isFinite(end) && end < real ? end : real;
    const from = to - span;
    const r = rng(c.id * 131 + span / 1000 + Math.round((real - to) / HOUR));
    const points = [];
    const step = bucketSec * 1000;
    let sum = 0, cnt = 0, fails = 0, min = Infinity, max = -Infinity;
    const isPaused = !n.enabled || !c.enabled;
    const start = Math.floor(from / step) * step;
    for (let t = start; t <= to; t += step) {
      if (t < from) continue;
      const f = (t - from) / span;
      // Gaps: a sleep gap around 35% and a short gap at 72%
      if ((f > 0.35 && f < 0.365) || (f > 0.72 && f < 0.727)) continue;
      if (isPaused && t > real - 3 * DAY && p.status === 'paused') continue;
      if (p.status === 'unknown') continue;
      // Failures: currently down since downSince; a brief outage at ~20%
      let failedFrac = 0;
      if (p.downSince != null && t >= real - p.downSince) failedFrac = 1;
      else if (f > 0.20 && f < 0.208 && (c.type === 'ping' || c.type === 'http')) failedFrac = source === 'raw' ? 1 : 0.6;
      const count = source === 'raw' ? 1 : Math.max(1, Math.round(step / (c.intervalSeconds * 1000)));
      const failures = Math.round(count * failedFrac);
      const avail = 100 * (1 - failures / count);
      // Latency: base with noise, a dip (spike) at 60–63%, slow daily wave
      let lat = p.base * (1 + (r() - 0.5) * p.noise + 0.08 * Math.sin((t / DAY) * Math.PI * 2));
      if (f > 0.60 && f < 0.63) lat *= 3.2 + r();
      if (f > 0.61 && f < 0.615) lat *= 1.5;
      const ok = failedFrac < 1;
      // A hardware check measures a machine rather than a round trip, so it
      // has no latency to chart.
      const avg = ok && c.type !== 'system' ? +lat.toFixed(2) : null;
      const loss = c.type === 'ping' ? (failedFrac >= 1 ? 100 : (f > 0.60 && f < 0.63 ? +(25 * r()).toFixed(1) : (r() < 0.02 ? 25 : 0))) : null;
      const pt = { ts: iso(t), avgMs: avg, minMs: ok ? +(lat * 0.85).toFixed(2) : null, maxMs: ok ? +(lat * (1.2 + (source === 'raw' ? 0 : r() * 0.5))).toFixed(2) : null, jitterMs: ok ? +(lat * 0.1).toFixed(2) : null, lossPct: loss, availability: +avail.toFixed(2), count, failures };
      points.push(pt);
      if (avg != null) { sum += avg; cnt++; if (avg < min) min = avg; if (avg > max) max = avg; }
      fails += failures;
    }
    const total = points.reduce((a, q) => a + q.count, 0);
    return { checkId: c.id, checkName: c.name, nodeId: n.id, nodeName: n.name, checkType: c.type, range, source, bucketSeconds: source === 'raw' ? 0 : bucketSec, from: iso(from), to: iso(to), points,
      summary: { availability: total ? +((1 - fails / total) * 100).toFixed(3) : 0, avgMs: cnt ? +(sum / cnt).toFixed(2) : null, minMs: cnt ? min : null, maxMs: cnt ? max : null, count: total, failures: fails } };
  }

  return { history, metricHistory };
}

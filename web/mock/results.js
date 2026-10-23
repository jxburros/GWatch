// The mock's live check state and result history: what each check last said and the readings behind it.
//
// Part of mock.js, split out for #118: the section is the
// mock's own, moved here unchanged inside install(m), which runs at the
// point it used to and hands it the bindings it reads.

/** @param {Record<string, any>} m the mock's state and helpers */
export function install(m) {
  const { CHECK_PROFILES, DAY, HOUR, MIN, NOW, ago, ahead, clone, gateway, iso, mockReading, nodeInMaintenance, nodes, rng, seq, site } = m;

  /* ---------- Live state & results ---------- */
  const states = {}; // checkId -> CheckState
  const lastResults = {}; // checkId -> Result
  const silences = {};
  const resultLog = {}; // checkId -> Result[] newest first

  function certInfo(days, host) {
    const subject = host.replace(/^https?:\/\//, '').replace(/[:/].*$/, '');
    return { subject: `CN=${subject}`, issuer: "CN=R11, O=Let's Encrypt, C=US", notBefore: ago((90 - days) * DAY), notAfter: ahead(days * DAY), daysRemaining: days, dnsNames: [subject, subject.replace(/^www\./, '')], serial: '04:AB:19:F2:7C:33:9E:1D', valid: true };
  }

  // snmpReading invents one plausible reading for an OID row: a gauge gets a
  // value, a counter gets a rate, and an OID whose name says it is text gets
  // text and no number at all — which is what the inspector has to cope with.
  function snmpReading(o, t, r) {
    const v = { oid: o.oid, name: o.name, unit: o.unit || '' };
    const wave = (period, amp) => 1 + amp * Math.sin((t / period) * Math.PI * 2);
    if (o.oid === '1.3.6.1.2.1.1.1.0') { v.raw = 'MikroTik CRS310, RouterOS 7.14'; return v; }
    if (o.kind === 'counter') {
      const base = o.name.includes('errors') ? 0.4 : (o.name.includes('out') ? 3.1e6 : 8.4e6);
      v.rate = +(base * wave(6 * HOUR, 0.35) * (0.9 + r() * 0.2)).toFixed(2);
      v.raw = String(Math.round(1.4e11 + t / 100));
      return v;
    }
    if (o.oid === '1.3.6.1.2.1.1.3.0') { v.value = +((NOW - 41 * DAY - t % MIN) / 1000).toFixed(0); v.raw = String(Math.round(v.value * 100)); return v; }
    v.value = 1;
    v.raw = '1';
    return v;
  }

  // matterReading invents one reading of a Matter check at time t, shaped like
  // internal/checks' MatterValue: a gentle daily wave for a measurement, a
  // state as its word and its 1 or 0. The door stays shut, so the demo check
  // is up; the battery drains slowly over the month.
  function matterReading(rd, t, r) {
    const paths = { temperature: '1026/0', humidity: '1029/0', contact: '69/0', onoff: '6/0', occupancy: '1030/0', illuminance: '1024/0', battery: '47/12' };
    const v = { name: rd.name, endpoint: rd.endpoint, path: `${rd.endpoint}/${paths[rd.kind] || `${rd.cluster}/${rd.attribute}`}`, unit: rd.unit || '' };
    const day = Math.sin((t / DAY) * Math.PI * 2);
    switch (rd.kind) {
      case 'temperature': v.value = +(21 + 1.6 * day + r() * 0.2).toFixed(2); break;
      case 'humidity': v.value = +(48 - 6 * day + r() * 0.5).toFixed(2); break;
      case 'illuminance': v.value = Math.max(0, Math.round(300 * day + r() * 20)); break;
      case 'battery': v.value = +(82 - ((t % (30 * DAY)) / DAY) * 0.3).toFixed(1); break;
      case 'contact': v.value = 0; v.raw = 'shut'; return v;
      case 'onoff': v.value = day > 0 ? 0 : 1; v.raw = v.value ? 'on' : 'off'; return v;
      case 'occupancy': v.value = r() > 0.7 ? 1 : 0; v.raw = v.value ? 'occupied' : 'clear'; return v;
      default: v.value = Math.round(r() * 100); break;
    }
    v.raw = String(v.value);
    return v;
  }

  // jsonReading is the number a recording json check reads at time t: a slow
  // daily wave around a third, which is what a Pi-hole's blocked percentage
  // tends to look like. Deterministic in t so a chart's points and the last
  // result agree.
  function jsonReading(check, t) {
    return +(33 + 9 * Math.sin((t / DAY) * Math.PI * 2 + check.id)).toFixed(1);
  }

  // checkMetricUnits mirrors model.Check.MetricUnits: every named metric a
  // check measures, with its unit — its OIDs for SNMP, its recorded value
  // for a json check that records one.
  function checkMetricUnits(c) {
    const units = {};
    if (c.type === 'snmp') for (const o of c.config?.snmpOids || []) if (o.name) units[o.name] = o.unit || '';
    if (c.type === 'matter') for (const rd of c.config?.matterReadings || []) if (rd.name) units[rd.name] = rd.unit || '';
    if (c.type === 'json' && c.config?.jsonRecord) units[(c.config.jsonMetric || '').trim() || 'value'] = c.config.jsonUnit || '';
    // A hardware check measures whatever its newest reading carried, the way
    // the service's checkHasMetric accepts a key from the latest result.
    if (c.type === 'system') for (const m of systemMetricResults(c, mockReading('agent:1', 'nas.lan', 29))) units[m.key] = m.unit;
    return units;
  }

  // systemMetricUnit and systemMetricResults mirror the service's
  // model.SystemMetricUnit and checks.hostMetricResults: one entry per
  // reading, each judged against the threshold that governs its key — the
  // instance's own entry first, then its family's.
  function systemMetricUnit(key) {
    const [family, instance = ''] = key.includes(':') ? [key.slice(0, key.indexOf(':')), key.slice(key.indexOf(':') + 1)] : [key];
    if (['cpu', 'memory', 'swap', 'disk', 'inodes', 'psi'].includes(family)) return '%';
    if (family === 'temp') return '°C';
    if (family === 'pool') return 'state';
    if (family === 'service') return 'running';
    if (family === 'process') return 'count';
    if (family === 'net') return 'B/s';
    if (family === 'diskio') return instance.endsWith('.busy') ? '%' : 'B/s';
    return '';
  }
  function systemMetricResults(check, m) {
    const list = check.config?.metricThresholds || [];
    const find = (k) => list.find((t) => t.metric === k);
    const governing = (key) => {
      const direct = find(key); if (direct) return direct;
      const colon = key.indexOf(':');
      if (colon < 0) return null;
      const family = key.slice(0, colon), instance = key.slice(colon + 1);
      const dot = instance.lastIndexOf('.');
      if (dot > 0 && (family === 'net' || family === 'diskio' || family === 'psi')) { const whole = find(`${family}:${instance.slice(0, dot)}`); if (whole) return whole; }
      return find(family) || null;
    };
    const fmt = (v, unit) => (unit === '%' ? `${Math.round(v)}%` : unit === 'B/s' ? `${Math.round(v / 1000)} kB/s` : unit === '°C' ? `${Math.round(v)} °C` : unit === 'state' ? (v >= 2 ? 'faulted' : v >= 1 ? 'degraded' : 'healthy') : unit === 'running' ? (v >= 1 ? 'running' : 'stopped') : unit === 'count' ? String(Math.round(v)) : v.toFixed(2));
    const out = [];
    const add = (key, label, value) => {
      if (value == null) return;
      const unit = systemMetricUnit(key);
      const r = { key, label, value, unit, status: 'up' };
      const t = governing(key);
      if (t) {
        const past = (lvl) => lvl != null && lvl !== '' && (t.below ? value <= lvl : lvl > 0 && value >= lvl);
        const side = t.below ? 'at or below' : 'at or above';
        if (past(t.crit)) { r.status = 'down'; r.reason = `${label} is ${fmt(value, unit)}, ${side} the ${fmt(t.crit, unit)} critical threshold`; }
        else if (past(t.warn)) { r.status = 'degraded'; r.reason = `${label} is ${fmt(value, unit)}, ${side} the ${fmt(t.warn, unit)} warning threshold`; }
      }
      out.push(r);
    };
    add('cpu', 'Processor use', m.cpu?.usagePct);
    add('load', 'Load per core', m.cpu?.loadPerCore);
    if (m.memory?.totalBytes) add('memory', 'Memory use', m.memory.usedPct);
    add('swap', 'Swap use', m.memory?.swapUsedPct);
    for (const fs of m.filesystems || []) { add(`disk:${fs.mount}`, `Disk ${fs.mount}`, fs.usedPct); add(`inodes:${fs.mount}`, `Inodes on ${fs.mount}`, fs.inodesUsedPct); }
    for (const n of m.interfaces || []) { add(`net:${n.name}.rx`, `Network ${n.name} received`, n.rxBytesPerSec); add(`net:${n.name}.tx`, `Network ${n.name} sent`, n.txBytesPerSec); }
    for (const d of m.disks || []) { add(`diskio:${d.name}.read`, `Disk ${d.name} read`, d.readBytesPerSec); add(`diskio:${d.name}.write`, `Disk ${d.name} write`, d.writeBytesPerSec); add(`diskio:${d.name}.busy`, `Disk ${d.name} busy`, d.busyPct); }
    for (const t of m.temperatures || []) add(`temp:${t.name}`, `Temperature ${t.name}`, t.celsius);
    for (const p of m.pressure || []) { add(`psi:${p.resource}.avg10`, `Pressure on ${p.resource} (10 s)`, p.avg10); add(`psi:${p.resource}.avg60`, `Pressure on ${p.resource} (60 s)`, p.avg60); }
    for (const p of m.pools || []) add(`pool:${p.name}`, `Pool ${p.name}`, p.health === 'ONLINE' ? 0 : p.health === 'DEGRADED' ? 1 : 2);
    for (const s of m.services || []) add(`service:${s.name}`, `Service ${s.name}`, s.running ? 1 : 0);
    for (const p of m.processes || []) add(`process:${p.name}`, `Copies of ${p.name} running`, p.count);
    return out;
  }

  function makeResult(check, node, t, r, { failed, latency, statusOverride } = {}) {
    const p = CHECK_PROFILES[check.id];
    const success = !failed;
    const status = statusOverride || (failed ? 'down' : (p.warn ? 'degraded' : 'up'));
    const res = { id: ++seq.result, checkId: check.id, ts: iso(t), success, status, message: failed ? (p.message || 'Failed') : (p.message && !failed && status !== 'down' ? p.message : ''), latencyMs: failed ? null : latency, details: {}, attempts: failed ? 2 : 1 };
    const target = check.config.target || node.host;
    switch (check.type) {
      case 'ping': {
        const count = check.config.pingCount || 4;
        const rtts = failed ? [] : Array.from({ length: count }, () => +(latency * (0.8 + r() * 0.4)).toFixed(2));
        res.details = { packetsSent: count, packetsReceived: rtts.length, rtts };
        if (!failed) {
          res.minMs = Math.min(...rtts); res.maxMs = Math.max(...rtts);
          res.jitterMs = +(res.maxMs - res.minMs).toFixed(2);
          // Population standard deviation of the packets, the same figure the
          // service computes, so the detail card shows something plausible.
          const mean = rtts.reduce((a2, b2) => a2 + b2, 0) / rtts.length;
          res.stddevMs = +Math.sqrt(rtts.reduce((a2, v) => a2 + (v - mean) ** 2, 0) / rtts.length).toFixed(2);
          res.lossPct = 0;
          res.message = res.message || `${count}/${count} replies, avg ${latency.toFixed(1)} ms`;
        } else { res.lossPct = 100; res.minMs = null; }
        break;
      }
      case 'http': case 'keyword': case 'json': {
        if (!failed) {
          const dns = +(latency * 0.08).toFixed(1), connect = +(latency * 0.12).toFixed(1), tls = target.startsWith('https') ? +(latency * 0.25).toFixed(1) : 0, ttfb = +(latency * 0.85).toFixed(1);
          res.details = { statusCode: 200, finalUrl: target.startsWith('http') ? target : `https://${target}/`, redirects: check.id === site.checks[0].id ? 1 : 0, dnsMs: dns, connectMs: connect, tlsMs: tls, firstByteMs: ttfb, totalMs: +latency.toFixed(1), contentLength: 18422 + Math.floor(r() * 2000) };
          if (check.type === 'keyword') { res.details.keywordFound = true; res.message = res.message || `Keyword found · 200 in ${latency.toFixed(0)} ms`; }
          else if (check.type === 'json' && check.config.jsonRecord) {
            // A recording check keeps the number under its metric name, the
            // way the service writes Result.metrics.
            const name = (check.config.jsonMetric || '').trim() || 'value';
            const v = jsonReading(check, t);
            res.details.jsonValue = String(v); res.details.jsonMatched = true;
            res.metrics = { [name]: v };
            res.message = res.message || `json "${check.config.jsonPath}" = ${v}; recorded ${name} = ${v}${check.config.jsonUnit ? ' ' + check.config.jsonUnit : ''}`;
          }
          else if (check.type === 'json') { res.details.jsonValue = check.config.jsonExpected || 'ok'; res.details.jsonMatched = true; res.message = res.message || `${check.config.jsonPath} = ${check.config.jsonExpected || 'present'} · ${latency.toFixed(0)} ms`; }
          else res.message = res.message || `OK 200 in ${latency.toFixed(0)} ms`;
          if (check.config.contentWatch === 'redirect') { res.details.contentValue = res.details.finalUrl; res.details.contentChanged = false; }
          if (p.cert && (check.config.certCheck !== false) && target.startsWith('https')) { res.details.cert = certInfo(p.cert.days, target); if (p.warn) res.warnings = [p.warn]; }
        } else {
          res.error = p.message || 'connection failed';
          res.details = { finalUrl: target.startsWith('http') ? target : `https://${target}/`, dnsMs: 2.1, connectMs: null };
        }
        break;
      }
      case 'cert': {
        if (!failed) { res.details = { cert: certInfo(p.cert?.days ?? 60, target) }; res.message = res.message || `Valid, expires in ${p.cert?.days ?? 60} days`; if (p.warn) res.warnings = [p.warn]; }
        else res.error = p.message;
        break;
      }
      case 'tcp': {
        if (!failed) { res.details = { remoteAddr: `${node.host.replace(/^https?:\/\//, '')}:${check.config.port}` }; res.message = res.message || `Connected in ${latency.toFixed(1)} ms`; }
        else res.error = p.message;
        break;
      }
      case 'dns': {
        if (!failed) { const vals = check.config.expectedIps?.length ? check.config.expectedIps : ['93.184.215.14', '2606:2800:21f:cb07:6820:80da:af6b:8b2c']; res.details = { resolvedValues: vals, expectedMatch: check.config.expectedIps?.length ? true : null, resolver: check.config.dnsServer || 'system' }; res.message = res.message || `Resolved to ${vals[0]} in ${latency.toFixed(1)} ms`; }
        else { res.error = p.message; res.details = { resolver: check.config.dnsServer || 'system', resolvedValues: [] }; }
        break;
      }
      case 'system': {
        if (failed) { res.error = p.message || 'no hardware reading'; break; }
        // The reading the check evaluated, the metrics it recorded and each
        // one's own verdict — the shape the service's evaluateHost writes.
        const reading = mockReading('agent:1', 'nas.lan', 29, t);
        const rows = systemMetricResults(check, reading);
        res.latencyMs = null;
        res.details = { host: reading, hostAgeSeconds: 12, metricResults: rows };
        res.metrics = Object.fromEntries(rows.map((m) => [m.key, m.value]));
        res.warnings = rows.filter((m) => m.status === 'degraded').map((m) => m.reason);
        const worst = rows.some((m) => m.status === 'down') ? 'down' : rows.some((m) => m.status === 'degraded') ? 'degraded' : 'up';
        res.status = worst === 'down' ? 'down' : worst;
        res.success = worst !== 'down';
        res.message = res.message || `CPU ${Math.round(reading.cpu.usagePct)}%, memory ${Math.round(reading.memory.usedPct)}%`;
        break;
      }
      case 'snmp': {
        if (failed) { res.error = p.message || 'no response'; break; }
        res.details = { snmp: (check.config.snmpOids || []).map((o) => snmpReading(o, t, r)) };
        res.metrics = {};
        for (const v of res.details.snmp) {
          const measured = v.value != null ? v.value : v.rate;
          if (measured != null) res.metrics[v.name] = measured;
        }
        res.message = res.message || `${res.details.snmp.length} readings read in ${latency.toFixed(0)} ms`;
        if (p.warn) res.warnings = [p.warn];
        break;
      }
      case 'matter': {
        if (failed) { res.error = p.message || `Matter node ${check.config.matterNodeId} is unavailable`; break; }
        res.details = { matter: (check.config.matterReadings || []).map((rd) => matterReading(rd, t, r)), matterServer: `ws://${node.host}:5580/ws` };
        res.metrics = Object.fromEntries(res.details.matter.filter((v) => v.value != null).map((v) => [v.name, v.value]));
        res.message = res.message || `${res.details.matter.length} readings read from node ${check.config.matterNodeId} in ${latency.toFixed(0)} ms`;
        break;
      }
    }
    if (res.message === '') res.message = success ? 'OK' : 'Failed';
    return res;
  }

  function initState() {
    for (const n of nodes) {
      n.checks.forEach((c, i) => {
        const p = CHECK_PROFILES[c.id];
        const r = rng(c.id * 7919);
        const interval = c.intervalSeconds * 1000;
        const lastRun = NOW - Math.floor(r() * interval);
        const failed = p.status === 'down';
        const latency = p.base * (1 + (r() - 0.5) * p.noise);
        const st = {
          checkId: c.id, status: !n.enabled || !c.enabled ? 'paused' : p.status, consecutiveFailures: failed ? Math.max(2, Math.floor((p.downSince || 0) / interval)) : 0,
          lastRunAt: iso(lastRun), lastSuccessAt: failed ? ago(p.downSince + interval) : iso(lastRun), lastChangeAt: p.downSince ? ago(p.downSince) : (p.warn ? ago(5 * DAY + 3 * HOUR) : ago(9 * DAY)), nextRunAt: iso(lastRun + interval),
          lastMessage: '', lastLatencyMs: failed ? null : +latency.toFixed(2), alertActive: failed && !p.affectedBy, alertSuppressed: !!p.affectedBy, suppressReason: p.affectedBy ? 'dependency' : '', lastAlertAt: failed && !p.affectedBy ? ago(p.downSince - 90e3) : null, silencedUntil: null,
          affectedByCheckId: p.affectedBy ? gateway.checks[0].id : null, affectedByNodeName: p.affectedBy || '', warningActive: !!p.warn, certWarningActive: !!(p.warn && p.cert), running: false,
        };
        if (p.status === 'unknown') { st.lastRunAt = null; st.lastSuccessAt = null; st.lastChangeAt = null; st.nextRunAt = ahead(20e3); st.lastLatencyMs = null; st.lastMessage = ''; states[c.id] = st; resultLog[c.id] = []; return; }
        if (nodeInMaintenance(n) && n.enabled && c.enabled) st.status = 'maintenance';
        const res = makeResult(c, n, lastRun, r, { failed, latency });
        st.lastMessage = res.message;
        states[c.id] = st; lastResults[c.id] = res;
        // recent results log
        const log = [res];
        for (let k = 1; k < 25; k++) {
          const t = lastRun - k * interval;
          const wasDown = p.downSince != null && (NOW - t) < p.downSince;
          const lat = p.base * (1 + (r() - 0.5) * p.noise);
          log.push(makeResult(c, n, t, r, { failed: wasDown, latency: lat }));
        }
        resultLog[c.id] = log;
      });
    }
  }
  initState();

  function checkStatus(n, c) {
    if (!n.enabled || !c.enabled) return 'paused';
    const st = states[c.id];
    if (!st) return 'unknown';
    if (nodeInMaintenance(n)) return 'maintenance';
    return st.status === 'maintenance' ? 'up' : st.status;
  }
  const SEV = { down: 5, degraded: 4, unknown: 3, maintenance: 2, up: 1, paused: 0 };
  function nodeStatus(n) {
    if (!n.enabled) return 'paused';
    if (nodeInMaintenance(n)) return 'maintenance';
    let worst = 'paused';
    for (const c of n.checks) { const s = checkStatus(n, c); if (SEV[s] > SEV[worst]) worst = s; }
    return n.checks.length ? (worst === 'paused' ? 'paused' : worst) : 'unknown';
  }
  function stateFor(n, c) { const st = states[c.id] ? clone(states[c.id]) : { checkId: c.id, status: 'unknown', consecutiveFailures: 0, lastRunAt: null, lastSuccessAt: null, lastChangeAt: null, nextRunAt: null, lastMessage: '', lastLatencyMs: null, alertActive: false, alertSuppressed: false, lastAlertAt: null, silencedUntil: null, affectedByCheckId: null, warningActive: false, certWarningActive: false, running: false }; st.status = checkStatus(n, c); if (silences[c.id] && silences[c.id] > Date.now()) { st.silencedUntil = iso(silences[c.id]); st.alertSuppressed = true; st.suppressReason = 'silenced'; } return st; }
  function nodeOut(n, { withResults = false } = {}) {
    const out = clone(n);
    out.status = nodeStatus(n);
    out.inMaintenance = nodeInMaintenance(n);
    out.stateByCheck = {};
    for (const c of n.checks) out.stateByCheck[c.id] = stateFor(n, c);
    if (withResults) { out.lastResults = {}; for (const c of n.checks) if (lastResults[c.id]) out.lastResults[c.id] = clone(lastResults[c.id]); }
    return out;
  }
  function findNode(id) { return nodes.find((n) => n.id === Number(id)); }
  function findCheck(id) { for (const n of nodes) for (const c of n.checks) if (c.id === Number(id)) return { n, c }; return null; }

  return { SEV, checkMetricUnits, findCheck, findNode, jsonReading, lastResults, makeResult, matterReading, nodeOut, nodeStatus, resultLog, silences, snmpReading, stateFor, states, systemMetricResults };
}

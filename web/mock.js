// In-browser mock of the GWatch API. Loaded ONLY when the page is opened with
// ?mock=1 — it intercepts fetch() for /api/* and fakes the event stream so the
// interface can be developed and screenshot-tested without the Go service.

(function installMock() {
  const NOW = Date.now();
  const MIN = 60e3, HOUR = 3600e3, DAY = 86400e3;
  const iso = (t) => new Date(t).toISOString();
  const ago = (ms) => iso(NOW - ms);
  const ahead = (ms) => iso(NOW + ms);
  const seq = { node: 20, check: 100, result: 90000, event: 5000, dash: 5, maint: 5, wall: 1, discovery: 0 };
  const clone = (o) => JSON.parse(JSON.stringify(o));
  // A node belongs to as many groups as it likes; group is the deprecated
  // alias for the first of them, which is what an older client reads.
  const groupsOf = (n) => (n.groups && n.groups.length ? n.groups : (n.group ? [n.group] : []));
  const inGroup = (n, g) => groupsOf(n).some((x) => String(x).toLowerCase() === String(g || '').toLowerCase());

  function rng(seed) { let a = seed >>> 0; return () => { a = (a + 0x6D2B79F5) >>> 0; let t = a; t = Math.imul(t ^ (t >>> 15), t | 1); t ^= t + Math.imul(t ^ (t >>> 7), t | 61); return ((t ^ (t >>> 14)) >>> 0) / 4294967296; }; }

  /* ---------- Nodes & checks ---------- */
  const CHECK_PROFILES = {}; // checkId -> { base, noise, status, ... }

  function mkCheck(nodeId, type, name, opts = {}) {
    const id = ++seq.check;
    const c = {
      id, nodeId, type, name, enabled: opts.enabled !== false, intervalSeconds: opts.interval || 60, timeoutSeconds: opts.timeout || 10, retries: 1, failureThreshold: opts.failureThreshold || 0,
      config: opts.config || {}, alerts: opts.alerts || null, sortOrder: opts.sortOrder || 0, createdAt: ago(40 * DAY), updatedAt: ago(3 * DAY),
    };
    CHECK_PROFILES[id] = { base: opts.base ?? 20, noise: opts.noise ?? 0.3, status: opts.status || 'up', message: opts.message, downSince: opts.downSince, warn: opts.warn, cert: opts.cert, affectedBy: opts.affectedBy, loss: opts.loss ?? 0 };
    return c;
  }
  function mkNode(o) {
    const id = ++seq.node;
    // group is the deprecated alias for the first group, exactly as the
    // server sends it.
    const groups = (o.groups || (o.group ? [o.group] : [])).slice(0, 16);
    return { id, name: o.name, host: o.host, groups, group: groups[0] || '', tags: o.tags || [], notes: o.notes || '', importance: o.importance || 'normal', enabled: o.enabled !== false, dependsOnNodeId: o.dependsOnNodeId ?? null, template: o.template || '', createdAt: ago(40 * DAY), updatedAt: ago(2 * DAY), checks: [] };
  }

  const nodes = [];
  const gateway = mkNode({ name: 'Gateway', host: '192.168.1.1', group: 'Home Network', tags: ['critical', 'router'], importance: 'critical', template: 'router', notes: 'ISP fibre router in the hallway cupboard. Power-cycle if unreachable for more than 10 minutes.' });
  gateway.checks = [
    mkCheck(gateway.id, 'ping', 'Ping', { config: { pingCount: 4, latencyWarnMs: 50, packetLossWarnPct: 10 }, base: 1.2, noise: 0.5, status: 'down', message: 'No reply from 192.168.1.1 (4 of 4 packets lost)', downSince: 23 * MIN, loss: 100, interval: 30 }),
    mkCheck(gateway.id, 'dns', 'DNS resolves example.com', { config: { target: 'example.com', dnsServer: '192.168.1.1', recordType: 'A' }, base: 8, noise: 0.4, status: 'down', message: 'DNS lookup timed out after 5 s', downSince: 22 * MIN }),
    mkCheck(gateway.id, 'http', 'Admin page', { config: { target: 'http://192.168.1.1/', expectedStatus: '200,401', followRedirects: true, certCheck: false }, base: 35, noise: 0.4, status: 'down', message: 'Connection refused', downSince: 23 * MIN, interval: 120 }),
  ];
  const plex = mkNode({ name: 'Plex', host: '192.168.1.20', group: 'Media', tags: ['media', 'docker'], importance: 'high', template: 'home-server', dependsOnNodeId: gateway.id, notes: 'Runs in Docker on the media box.' });
  plex.checks = [
    mkCheck(plex.id, 'ping', 'Ping', { config: { pingCount: 4 }, base: 0.9, noise: 0.5, status: 'down', message: 'No reply (4 of 4 packets lost)', downSince: 21 * MIN, loss: 100, affectedBy: 'Gateway', interval: 30 }),
    mkCheck(plex.id, 'tcp', 'Plex port 32400', { config: { port: 32400 }, base: 2.1, noise: 0.4, status: 'down', message: 'dial tcp 192.168.1.20:32400: i/o timeout', downSince: 21 * MIN, affectedBy: 'Gateway' }),
    mkCheck(plex.id, 'http', 'Web app', { config: { target: 'http://192.168.1.20:32400/web/index.html', expectedStatus: '200-399', certCheck: false }, base: 48, noise: 0.5, status: 'down', message: 'Connection timed out after 10 s', downSince: 20 * MIN, affectedBy: 'Gateway', interval: 120 }),
  ];
  const nas = mkNode({ name: 'NAS', host: 'nas.local', groups: ['Servers', 'Storage'], tags: ['storage', 'backup-target'], importance: 'high', template: 'home-server' });
  nas.checks = [
    mkCheck(nas.id, 'ping', 'Ping', { config: { pingCount: 4, latencyWarnMs: 20 }, base: 0.7, noise: 0.4, interval: 60 }),
    mkCheck(nas.id, 'tcp', 'SSH (22)', { config: { port: 22 }, base: 1.8, noise: 0.3 }),
    mkCheck(nas.id, 'http', 'Web UI', { config: { target: 'https://nas.local:5001/', expectedStatus: '200-399', ignoreTlsErrors: true, certCheck: false }, base: 62, noise: 0.35, interval: 300 }),
    // The machine itself, read from its agent (#60): every metric has its
    // own verdict, and the media disk is the one over its line.
    mkCheck(nas.id, 'system', 'Hardware health', {
      config: {
        hostSource: 'agent', agentId: 1,
        metricThresholds: [
          { metric: 'cpu', warn: 90 }, { metric: 'memory', warn: 90, crit: 97 }, { metric: 'swap', warn: 50 },
          { metric: 'disk', warn: 85, crit: 95 }, { metric: 'inodes', warn: 85, crit: 95 }, { metric: 'load', warn: 2 },
          { metric: 'disk:/', warn: 70, crit: 90 },
        ],
      },
      base: 4, noise: 0.2, interval: 60, status: 'degraded',
      warn: 'Disk /srv/media is 87%, at or above the 85% warning threshold',
      message: 'CPU 21%, memory 41%, disk 87% (/srv/media)',
    }),
  ];
  const ha = mkNode({ name: 'Home Assistant', host: 'homeassistant.local', group: 'Servers', tags: ['automation'], importance: 'normal', template: 'home-server' });
  ha.checks = [
    mkCheck(ha.id, 'ping', 'Ping', { config: { pingCount: 4 }, base: 1.4, noise: 0.5 }),
    mkCheck(ha.id, 'http', 'Frontend', { config: { target: 'http://homeassistant.local:8123/', expectedStatus: '200', certCheck: false, latencyWarnMs: 800 }, base: 140, noise: 0.5, interval: 120 }),
    mkCheck(ha.id, 'json', 'API alive', { config: { target: 'http://homeassistant.local:8123/api/', jsonPath: 'message', jsonExpected: 'API running.', headers: { Authorization: 'Bearer ••••••' } }, base: 95, noise: 0.4, interval: 300 }),
  ];
  const site = mkNode({ name: 'Family website', host: 'https://www.example.org', group: 'Internet', tags: ['public', 'wordpress'], importance: 'high', template: 'website', notes: 'Hosted at a shared host. Certificate renews automatically via Let\u2019s Encrypt — check why it is late.' });
  site.checks = [
    mkCheck(site.id, 'http', 'HTTPS', { config: { expectedStatus: '200-399', followRedirects: true, certCheck: true, certWarnDays: 14, latencyWarnMs: 1500, contentWatch: 'redirect' }, base: 420, noise: 0.35, status: 'degraded', message: 'OK 200 in 438 ms · certificate expires in 9 days', warn: 'Certificate expires in 9 days', cert: { days: 9 }, interval: 300 }),
    mkCheck(site.id, 'keyword', 'Homepage text', { config: { keyword: 'Welcome to the family site', followRedirects: true }, base: 445, noise: 0.35, interval: 600 }),
    mkCheck(site.id, 'dns', 'DNS', { config: { expectedIps: ['93.184.215.14'], recordType: 'A' }, base: 24, noise: 0.5, interval: 900 }),
    mkCheck(site.id, 'cert', 'Certificate', { config: { port: 443, certWarnDays: 14 }, base: 210, noise: 0.2, status: 'degraded', message: 'Certificate valid, expires in 9 days', warn: 'Certificate expires in 9 days', cert: { days: 9 }, interval: 3600 }),
  ];
  const weather = mkNode({ name: 'Weather API', host: 'https://api.example.com', group: 'Internet', tags: ['api', 'public'], template: 'api-endpoint' });
  weather.checks = [
    mkCheck(weather.id, 'http', 'Health endpoint', { config: { target: 'https://api.example.com/v1/health', expectedStatus: '200', certCheck: true }, base: 180, noise: 0.4, cert: { days: 61 }, interval: 120 }),
    mkCheck(weather.id, 'json', 'Status is ok', { config: { target: 'https://api.example.com/v1/health', jsonPath: 'status', jsonExpected: 'ok' }, base: 190, noise: 0.4, interval: 300 }),
  ];
  const printer = mkNode({ name: 'Printer', host: '192.168.1.50', group: 'Home Network', tags: ['office'], importance: 'low', enabled: false, notes: 'Only switched on when needed — monitoring paused.' });
  printer.checks = [mkCheck(printer.id, 'ping', 'Ping', { config: { pingCount: 2 }, base: 3, noise: 0.6, status: 'paused', interval: 300 })];
  const pihole = mkNode({ name: 'Pi-hole', host: '192.168.1.2', groups: ['Home Network', 'Servers'], tags: ['dns', 'raspberry-pi'], importance: 'high', template: 'dns' });
  pihole.checks = [
    mkCheck(pihole.id, 'ping', 'Ping', { config: { pingCount: 4, latencyWarnMs: 30 }, base: 0.8, noise: 0.5, interval: 30 }),
    mkCheck(pihole.id, 'dns', 'Resolves via Pi-hole', { config: { target: 'www.example.org', dnsServer: '192.168.1.2', recordType: 'A' }, base: 3.2, noise: 0.5 }),
    mkCheck(pihole.id, 'http', 'Admin UI', { config: { target: 'http://192.168.1.2/admin/', expectedStatus: '200-399', certCheck: false }, base: 28, noise: 0.4, interval: 300 }),
    // A json check that records the number it reads (#55), so the node page
    // charts it the way it charts an SNMP reading.
    mkCheck(pihole.id, 'json', 'Blocked today', { config: { target: 'http://192.168.1.2/admin/api.php?summaryRaw', jsonPath: 'ads_percentage_today', jsonRecord: true, jsonMetric: 'Blocked', jsonUnit: '%', jsonWarnAbove: 60 }, base: 31, noise: 0.4, interval: 300 }),
  ];
  const backupSrv = mkNode({ name: 'Backup server', host: '192.168.1.30', group: 'Servers', tags: ['storage'], template: 'home-server', notes: 'Weekly patching on Sunday nights.' });
  backupSrv.checks = [
    mkCheck(backupSrv.id, 'ping', 'Ping', { config: { pingCount: 4 }, base: 1.1, noise: 0.5 }),
    mkCheck(backupSrv.id, 'tcp', 'SMB (445)', { config: { port: 445 }, base: 2.4, noise: 0.3 }),
  ];
  // A managed switch read over SNMP: the readings, not a round trip, are what
  // the check is for, so it carries the per-OID rows the inspector renders.
  const swtch = mkNode({ name: 'Office switch', host: '192.168.1.3', group: 'Home Network', tags: ['network', 'snmp'], importance: 'high', notes: 'Eight-port managed switch under the desk. Read over SNMP v2c with a read-only community.' });
  swtch.checks = [
    mkCheck(swtch.id, 'ping', 'Ping', { config: { pingCount: 4 }, base: 0.8, noise: 0.3, interval: 60 }),
    mkCheck(swtch.id, 'snmp', 'SNMP readings', {
      interval: 120,
      config: {
        snmpVersion: '2c', snmpPort: 161, snmpCommunity: '********',
        snmpOids: [
          { oid: '1.3.6.1.2.1.1.3.0', name: 'Uptime', kind: 'gauge', scale: 0.01, unit: 's' },
          { oid: '1.3.6.1.2.1.1.1.0', name: 'Description', kind: 'gauge', scale: 1 },
          { oid: '1.3.6.1.2.1.2.2.1.8.1', name: 'Uplink link', kind: 'gauge', scale: 1, critBelow: 1, critAbove: 1 },
          { oid: '1.3.6.1.2.1.31.1.1.1.6.1', name: 'Uplink in', kind: 'counter', scale: 8, unit: 'bit/s' },
          { oid: '1.3.6.1.2.1.31.1.1.1.10.1', name: 'Uplink out', kind: 'counter', scale: 8, unit: 'bit/s' },
          { oid: '1.3.6.1.2.1.2.2.1.14.3', name: 'Port 3 errors in', kind: 'counter', scale: 1, unit: '/s', warnAbove: 0, critAbove: 5 },
        ],
      },
      base: 9, noise: 0.3, status: 'degraded',
      warn: 'Port 3 errors in (1.3.6.1.2.1.2.2.1.14.3) is 0.4 /s, above the warning threshold of 0 /s',
      message: '6 readings read in 9 ms · Uptime 412350 s, Description MikroTik CRS310, Uplink link 1 and 3 more',
    }),
  ];
  const newHost = mkNode({ name: 'Garage camera', host: '192.168.1.71', group: 'Home Network', tags: ['camera'], importance: 'low' });
  newHost.checks = [mkCheck(newHost.id, 'ping', 'Ping', { config: { pingCount: 4 }, base: 5, noise: 0.5, status: 'unknown', message: '' })];
  nodes.push(gateway, plex, nas, ha, site, weather, printer, pihole, backupSrv, swtch, newHost);

  /* ---------- Maintenance ---------- */
  const maintenance = [
    { id: ++seq.maint, name: 'Backup server patching', nodeId: backupSrv.id, group: '', enabled: true, startAt: ago(35 * MIN), endAt: ahead(85 * MIN), weekdays: [], durationMinutes: 0, notes: 'Manual OS updates and reboot.', createdAt: ago(2 * HOUR) },
    { id: ++seq.maint, name: 'Sunday night updates', nodeId: null, group: 'Servers', enabled: true, startAt: iso(new Date(NOW).setHours(3, 0, 0, 0)), endAt: iso(new Date(NOW).setHours(5, 0, 0, 0)), weekdays: [0], durationMinutes: 120, notes: 'Automatic updates + reboots.', createdAt: ago(20 * DAY) },
    { id: ++seq.maint, name: 'ISP line work', nodeId: null, group: '', enabled: false, startAt: ago(6 * DAY), endAt: ago(6 * DAY - 3 * HOUR), weekdays: [], durationMinutes: 0, notes: 'Planned fibre maintenance (done).', createdAt: ago(8 * DAY) },
  ];
  function windowActive(w, t = Date.now()) {
    if (!w.enabled) return false;
    if (!w.weekdays?.length) return t >= +new Date(w.startAt) && t < +new Date(w.endAt);
    const d = new Date(t); const st = new Date(w.startAt);
    for (let back = 0; back <= 1; back++) {
      const day = new Date(d); day.setDate(day.getDate() - back);
      if (!w.weekdays.includes(day.getDay())) continue;
      const ws = new Date(day.getFullYear(), day.getMonth(), day.getDate(), st.getHours(), st.getMinutes()).getTime();
      if (t >= ws && t < ws + (w.durationMinutes || 60) * MIN) return true;
    }
    return false;
  }
  function nodeInMaintenance(n) { return maintenance.some((w) => windowActive(w) && (w.nodeId ? w.nodeId === n.id : w.group ? inGroup(n, w.group) : true)); }

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
    if (['cpu', 'memory', 'swap', 'disk', 'inodes'].includes(family)) return '%';
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
      if (dot > 0 && (family === 'net' || family === 'diskio')) { const whole = find(`${family}:${instance.slice(0, dot)}`); if (whole) return whole; }
      return find(family) || null;
    };
    const fmt = (v, unit) => (unit === '%' ? `${Math.round(v)}%` : unit === 'B/s' ? `${Math.round(v / 1000)} kB/s` : v.toFixed(2));
    const out = [];
    const add = (key, label, value) => {
      if (value == null) return;
      const unit = systemMetricUnit(key);
      const r = { key, label, value, unit, status: 'up' };
      const t = governing(key);
      if (t) {
        const past = (lvl) => lvl != null && lvl !== '' && (t.below ? value <= lvl : lvl > 0 && value >= lvl);
        if (past(t.crit)) { r.status = 'down'; r.reason = `${label} is ${fmt(value, unit)}, at or above the ${fmt(t.crit, unit)} critical threshold`; }
        else if (past(t.warn)) { r.status = 'degraded'; r.reason = `${label} is ${fmt(value, unit)}, at or above the ${fmt(t.warn, unit)} warning threshold`; }
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

  /* ---------- Events ---------- */
  const events = [];
  function ev(msAgo, type, o = {}) {
    const n = o.node; const c = o.check;
    events.push({ id: 0, ts: ago(msAgo), type, nodeId: n ? n.id : null, checkId: c ? c.id : null, nodeName: n ? n.name : undefined, checkName: c ? c.name : undefined, title: o.title || '', detail: o.detail || '', meta: o.meta ? JSON.stringify(o.meta) : undefined });
  }
  ev(3 * DAY + 2 * HOUR, 'service_started', { title: 'GWatch service started', detail: 'Version 0.4.1 · windows/amd64 · listening on 127.0.0.1:7230' });
  ev(3 * DAY + 2 * HOUR + 40 * MIN, 'service_stopped', { title: 'GWatch service stopped', detail: 'Stopped for upgrade to 0.4.1' });
  ev(3 * DAY + 2 * HOUR + 41 * MIN, 'backup', { title: 'Backup created', detail: 'gwatch-2026-09-15-0713.gwbackup · 12.4 MB · configuration + history' });
  ev(2 * DAY + 6 * HOUR, 'monitor_gap', { title: 'Monitoring gap of 45 minutes', detail: 'The computer was asleep or offline from 01:10 to 01:55. No checks ran during this time.' });
  ev(2 * DAY + 3 * HOUR, 'config_changed', { node: ha, title: 'Node updated', detail: 'Added JSON check "API alive"; interval of "Frontend" changed from 60 s to 120 s' });
  ev(2 * DAY + 1 * HOUR, 'internal_error', { title: 'Failed to send email', detail: 'smtp.example.com: 535 authentication failed — check the SMTP password' });
  ev(2 * DAY + 1 * HOUR, 'alert_failed', { node: site, check: site.checks[0], title: 'Alert email could not be sent', detail: 'SMTP authentication failed (retrying next cycle)' });
  ev(2 * DAY, 'config_changed', { title: 'Alert settings changed', detail: 'SMTP password updated; test email sent successfully' });
  ev(1 * DAY + 20 * HOUR, 'warning', { node: ha, check: ha.checks[1], title: 'High response time', detail: 'Frontend answered in 1,240 ms (warning threshold 800 ms)' });
  ev(1 * DAY + 19 * HOUR + 40 * MIN, 'warning_cleared', { node: ha, check: ha.checks[1], title: 'Response time back to normal', detail: '190 ms' });
  ev(1 * DAY + 9 * HOUR, 'note', { node: nas, title: 'Note: replaced failing drive in bay 3', detail: 'RAID rebuild expected to take ~6 hours' });
  ev(1 * DAY + 2 * HOUR, 'retention', { title: 'Retention run finished', detail: 'Rolled up 41,280 raw results into 5-minute buckets · deleted 12,904 rows older than 30 days · 2.1 s' });
  ev(1 * DAY + 1 * HOUR, 'down', { node: weather, check: weather.checks[0], title: 'Health endpoint is down', detail: 'HTTP 503 Service Unavailable (expected 200)' });
  ev(1 * DAY + 1 * HOUR - 2 * MIN, 'alert_sent', { node: weather, check: weather.checks[0], title: 'Alert email sent', detail: 'To jeff@example.com, sam@example.com' });
  ev(1 * DAY + 41 * MIN, 'recovered', { node: weather, check: weather.checks[0], title: 'Health endpoint recovered', detail: 'HTTP 200 in 172 ms after 19 minutes down' });
  ev(1 * DAY + 40 * MIN, 'alert_sent', { node: weather, check: weather.checks[0], title: 'Recovery email sent', detail: 'To jeff@example.com, sam@example.com' });
  ev(1 * DAY + 20 * MIN, 'content_changed', { node: site, check: site.checks[0], title: 'Response changed: final redirect destination', detail: 'https://www.example.org/ → https://www.example.org/home/ — this is a change notice, not a security finding' });
  ev(22 * HOUR, 'silenced', { node: site, check: site.checks[0], title: 'Alerts silenced for 8 hours', detail: 'By user' });
  ev(14 * HOUR, 'unsilenced', { node: site, check: site.checks[0], title: 'Silence ended', detail: '' });
  ev(5 * DAY + 3 * HOUR, 'cert_warning', { node: site, check: site.checks[0], title: 'Certificate expires in 14 days', detail: 'CN=www.example.org, issued by Let\u2019s Encrypt · expires ' + new Date(NOW + 9 * DAY).toDateString() });
  ev(35 * MIN, 'maintenance_began', { node: backupSrv, title: 'Maintenance began: Backup server patching', detail: 'Alerts for Backup server are held until 21:35' });
  ev(23 * MIN + 30e3, 'down', { node: gateway, check: gateway.checks[0], title: 'Ping is down', detail: 'No reply from 192.168.1.1 (4 of 4 packets lost) after 2 consecutive failures' });
  ev(23 * MIN, 'down', { node: gateway, check: gateway.checks[2], title: 'Admin page is down', detail: 'Connection refused' });
  ev(22 * MIN, 'down', { node: gateway, check: gateway.checks[1], title: 'DNS resolves example.com is down', detail: 'DNS lookup timed out after 5 s' });
  ev(21 * MIN + 30e3, 'alert_sent', { node: gateway, check: gateway.checks[0], title: 'Alert email sent', detail: 'To jeff@example.com, sam@example.com' });
  ev(3 * DAY + 2 * HOUR + 5 * MIN, 'rule_cleared', { title: 'Rule cleared: Two of three DNS servers down', detail: 'Only 1 of 3 conditions still met (2 needed) — Gateway › DNS resolves example.com is down.' });
  ev(21 * MIN, 'rule_fired', { title: 'Rule fired: Gateway and Plex both down', detail: '2 of 2 conditions met — Gateway › Ping is down, Plex › Ping is down. Ran pushover.' });
  ev(21 * MIN, 'down', { node: plex, check: plex.checks[0], title: 'Ping is down', detail: 'No reply (4 of 4 packets lost)' });
  ev(21 * MIN, 'affected_by_parent', { node: plex, check: plex.checks[0], title: 'Plex unavailable because Gateway is down', detail: 'Failures on Plex are attributed to the Gateway outage' });
  ev(21 * MIN, 'alert_suppressed', { node: plex, check: plex.checks[0], title: 'Alert suppressed', detail: 'suppressed — Gateway is down', meta: { reason: 'Gateway is down' } });
  ev(21 * MIN - 10e3, 'down', { node: plex, check: plex.checks[1], title: 'Plex port 32400 is down', detail: 'dial tcp 192.168.1.20:32400: i/o timeout' });
  ev(21 * MIN - 10e3, 'alert_suppressed', { node: plex, check: plex.checks[1], title: 'Alert suppressed', detail: 'suppressed — Gateway is down', meta: { reason: 'Gateway is down' } });
  ev(20 * MIN, 'down', { node: plex, check: plex.checks[2], title: 'Web app is down', detail: 'Connection timed out after 10 s' });
  ev(20 * MIN, 'alert_suppressed', { node: plex, check: plex.checks[2], title: 'Alert suppressed', detail: 'suppressed — Gateway is down', meta: { reason: 'Gateway is down' } });
  ev(15 * MIN, 'alert_suppressed', { node: gateway, check: gateway.checks[1], title: 'Alert suppressed', detail: 'suppressed — cooldown (60 min) after the alert sent at ' + new Date(NOW - 21.5 * MIN).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }), meta: { reason: 'cooldown' } });
  ev(9 * DAY, 'maintenance_ended', { title: 'Maintenance ended: Sunday night updates', detail: 'Group Servers' });
  ev(9 * DAY + 2 * HOUR, 'maintenance_began', { title: 'Maintenance began: Sunday night updates', detail: 'Group Servers' });
  ev(6 * DAY, 'restore', { title: 'Restored from backup', detail: 'gwatch-2026-09-10-2210.gwbackup · 9 nodes, 24 checks, 38,110 results' });
  ev(12 * DAY, 'cert_warning_cleared', { node: weather, check: weather.checks[0], title: 'Certificate renewed', detail: 'CN=api.example.com now expires in 90 days' });
  events.sort((a, b) => new Date(b.ts) - new Date(a.ts));
  events.forEach((e, i) => { e.id = 5000 - i; });

  /* ---------- Dashboards ---------- */
  const W = (type, title, width, height, config = {}) => ({ id: `w-${Math.random().toString(36).slice(2, 8)}`, type, title, width, height, config });
  const dashboards = [
    { id: 1, name: 'Overview', sortOrder: 0, createdAt: ago(40 * DAY), updatedAt: ago(2 * DAY), widgets: [
      W('summary', 'Overall health', 2, 1),
      W('attention', 'Needs attention', 2, 1),
      W('latency_chart', 'LAN latency', 2, 2, { checkIds: [gateway.checks[0].id, nas.checks[0].id, pihole.checks[0].id], range: '24h', metric: 'avg' }),
      W('response_chart', 'Website response time', 2, 2, { checkIds: [site.checks[0].id, weather.checks[0].id], range: '24h', metric: 'avg' }),
      W('groups', 'Groups', 2, 1),
      W('cert_warnings', 'Certificates', 1, 1),
      W('monitor_health', 'Monitor health', 1, 1),
      W('status_list', 'All nodes', 2, 2, { group: '', tag: '', nodeIds: [] }),
      W('incidents', 'Recent incidents', 2, 2, { limit: 8 }),
      W('uptime_chart', 'Availability · 7 days', 2, 2, { checkIds: [gateway.checks[0].id, nas.checks[0].id, site.checks[0].id, ha.checks[1].id], range: '7d' }),
      W('loss_chart', 'Packet loss', 2, 2, { checkIds: [gateway.checks[0].id, plex.checks[0].id, pihole.checks[0].id], range: '24h' }),
      W('table', 'Servers', 4, 2, { group: 'Servers', tag: '' }),
    ] },
    { id: 2, name: 'Internet', sortOrder: 1, createdAt: ago(20 * DAY), updatedAt: ago(1 * DAY), widgets: [
      W('summary', 'Internet health', 2, 1),
      W('cert_warnings', 'Certificates', 2, 1),
      W('response_chart', 'Response time · 7 days', 4, 2, { checkIds: [site.checks[0].id, weather.checks[0].id, site.checks[1].id], range: '7d', metric: 'avg' }),
      W('status_list', 'Internet nodes', 2, 2, { group: 'Internet' }),
      W('uptime_chart', 'Availability · 30 days', 2, 2, { checkIds: [site.checks[0].id, weather.checks[0].id], range: '30d' }),
    ] },
  ];

  /* ---------- Settings / health / backups ---------- */
  // The header indicators, as a fresh install has them. The mock network
  // above is deliberately in a state that fires several at once — a down
  // gateway, a degraded website with a late certificate, a camera nobody has
  // checked yet and a server under maintenance — so the stacked header can be
  // seen without having to break anything first.
  const DEFAULT_INDICATORS = [
    { id: 'nodes-down', name: 'Nodes down', enabled: true, colour: 'red', condition: { kind: 'nodesInStatus', status: 'down', minCount: 1 } },
    { id: 'monitor-unwell', name: 'Monitor trouble', enabled: true, colour: 'red', condition: { kind: 'serviceHealth', minCount: 1 } },
    { id: 'nodes-degraded', name: 'Nodes degraded', enabled: true, colour: 'orange', condition: { kind: 'nodesInStatus', status: 'degraded', minCount: 1 } },
    { id: 'certs-expiring', name: 'Certificates expiring', enabled: true, colour: 'orange', condition: { kind: 'certWarnings', minCount: 1 } },
    { id: 'nodes-unknown', name: 'Waiting for first results', enabled: true, colour: 'yellow', condition: { kind: 'nodesInStatus', status: 'unknown', minCount: 1 } },
    { id: 'nodes-maintenance', name: 'In maintenance', enabled: true, colour: 'yellow', condition: { kind: 'nodesInStatus', status: 'maintenance', minCount: 1 } },
  ];
  let settings = {
    general: { instanceName: 'Home monitor', defaultIntervalSeconds: 60, defaultTimeoutSeconds: 10, maxConcurrentChecks: 8, minIntervalSeconds: 10, wallboardRefreshSeconds: 15, latencyWarnMs: 0, packetLossWarnPct: 0, pingMethod: 'auto', theme: 'dark', accentColor: '#43c9c0', remoteAccess: false, accessPassword: '', requireLoginLocally: false, updateRepo: 'jxburros/GWatch' },
    alerts: { enabled: true, recipients: ['jeff@example.com', 'sam@example.com'], failureThreshold: 2, cooldownMinutes: 60, notifyRecovery: true, notifyWarnings: true, certWarnDays: 14, smtp: { host: 'smtp.example.com', port: 587, username: 'gwatch@example.com', password: '********', from: 'GWatch <gwatch@example.com>', security: 'starttls' } },
    retention: { rawDays: 30, fiveMinDays: 180, hourlyDays: 730, dailyDays: 0, eventDays: 730 },
    indicators: clone(DEFAULT_INDICATORS),
  };
  let retention = { lastRunAt: ago(1 * DAY + 2 * HOUR), lastDurationMs: 2140, lastError: '', rawRows: 412_880, rollupRows5m: 96_412, rollupRows1h: 18_207, rollupRows1d: 1_128, eventRows: 1_940, oldestRaw: ago(30 * DAY), deletedLastRun: 12_904, plan: ['Raw results older than 30 days are rolled up into 5-minute buckets and deleted.', '5-minute buckets older than 180 days are rolled up into hourly buckets and deleted.', 'Hourly buckets older than 730 days are rolled up into daily buckets and deleted.', 'Daily buckets are kept forever.', 'Events older than 730 days are deleted.'] };
  let backups = [
    { fileName: 'gwatch-2026-09-15-0713.gwbackup', createdAt: ago(3 * DAY + 2 * HOUR + 41 * MIN), sizeBytes: 12_990_000, includeHistory: true, encrypted: true },
    { fileName: 'gwatch-2026-09-10-2210.gwbackup', createdAt: ago(8 * DAY), sizeBytes: 11_200_000, includeHistory: true, encrypted: true },
    { fileName: 'gwatch-2026-09-01-0900-config.gwbackup', createdAt: ago(17 * DAY), sizeBytes: 38_000, includeHistory: false, encrypted: true },
  ];
  let backupStatus = { lastBackupAt: backups[0].createdAt, lastBackupOk: true, lastBackupFile: backups[0].fileName, lastError: '', lastRestoreAt: ago(6 * DAY) };

  function health() {
    const all = nodes.flatMap((n) => n.checks);
    const runs = all.map((c) => states[c.id]?.lastRunAt).filter(Boolean).sort();
    const nexts = all.map((c) => states[c.id]?.nextRunAt).filter(Boolean).sort();
    return {
      version: '0.4.1', serviceMode: 'service', serviceRunning: true, startedAt: ago(3 * DAY + 2 * HOUR), uptimeSeconds: Math.floor((Date.now() - (NOW - 3 * DAY - 2 * HOUR)) / 1000), now: iso(Date.now()), schedulerRunning: true,
      lastCheckAt: runs[runs.length - 1] || null, lastSuccessAt: runs[runs.length - 1] || null, nextCheckAt: nexts[0] || null,
      checksTotal: all.length, checksEnabled: all.filter((c) => c.enabled && findNode(c.nodeId).enabled).length, checksRunning: 0,
      lastGap: { from: ago(2 * DAY + 6 * HOUR + 45 * MIN), to: ago(2 * DAY + 6 * HOUR), seconds: 2700 },
      databasePath: 'C:\\ProgramData\\GWatch\\gwatch.db', databaseBytes: 48_300_000, databaseDriver: 'modernc.org/sqlite', dataDir: 'C:\\ProgramData\\GWatch', keyPath: 'C:\\ProgramData\\GWatch\\gwatch.key', backupDir: 'C:\\ProgramData\\GWatch\\backups', retention, backup: backupStatus,
      recentErrors: events.filter((e) => e.type === 'internal_error').slice(0, 5), alertsEnabled: settings.alerts.enabled, smtpConfigured: !!settings.alerts.smtp.host, lastAlertAt: ago(21 * MIN + 30e3), lastAlertError: '', listenAddress: '127.0.0.1:7230', platform: 'windows/amd64',
    };
  }

  const templates = [
    { id: 'website', name: 'Website', description: 'HTTPS page load, certificate expiry, DNS and a keyword.', icon: 'globe', node: { name: 'My website', host: 'https://www.example.com', group: 'Internet', tags: ['public'], importance: 'normal', enabled: true }, checks: [
      { type: 'http', name: 'HTTPS', enabled: true, intervalSeconds: 300, timeoutSeconds: 10, retries: 1, failureThreshold: 0, config: { expectedStatus: '200-399', followRedirects: true, certCheck: true, certWarnDays: 14 } },
      { type: 'cert', name: 'Certificate', enabled: true, intervalSeconds: 3600, timeoutSeconds: 10, retries: 0, failureThreshold: 0, config: { port: 443, certWarnDays: 14 } },
      { type: 'dns', name: 'DNS', enabled: true, intervalSeconds: 900, timeoutSeconds: 5, retries: 1, failureThreshold: 0, config: { recordType: 'A' } },
      { type: 'keyword', name: 'Page text', enabled: false, intervalSeconds: 600, timeoutSeconds: 10, retries: 1, failureThreshold: 0, config: { keyword: '' } }] },
    { id: 'home-server', name: 'Home server', description: 'Ping, SSH port and an optional web UI.', icon: 'server', node: { name: 'Home server', host: '192.168.1.10', group: 'Servers', tags: [], importance: 'high', enabled: true }, checks: [
      { type: 'ping', name: 'Ping', enabled: true, intervalSeconds: 60, timeoutSeconds: 5, retries: 1, failureThreshold: 0, config: { pingCount: 4 } },
      { type: 'tcp', name: 'SSH (22)', enabled: true, intervalSeconds: 120, timeoutSeconds: 5, retries: 1, failureThreshold: 0, config: { port: 22 } },
      { type: 'http', name: 'Web UI', enabled: false, intervalSeconds: 300, timeoutSeconds: 10, retries: 1, failureThreshold: 0, config: { expectedStatus: '200-399', certCheck: false } }] },
    { id: 'router', name: 'Router / gateway', description: 'Ping and DNS through the router; other nodes can depend on it.', icon: 'router', node: { name: 'Router', host: '192.168.1.1', group: 'Home Network', tags: ['critical'], importance: 'critical', enabled: true }, checks: [
      { type: 'ping', name: 'Ping', enabled: true, intervalSeconds: 30, timeoutSeconds: 3, retries: 2, failureThreshold: 0, config: { pingCount: 4, latencyWarnMs: 50, packetLossWarnPct: 10 } },
      { type: 'dns', name: 'DNS via router', enabled: true, intervalSeconds: 120, timeoutSeconds: 5, retries: 1, failureThreshold: 0, config: { target: 'example.com', dnsServer: '192.168.1.1' } }] },
    { id: 'api-endpoint', name: 'API endpoint', description: 'HTTP status plus a JSON value assertion.', icon: 'api', node: { name: 'API', host: 'https://api.example.com/health', group: 'Internet', tags: ['api'], importance: 'normal', enabled: true }, checks: [
      { type: 'http', name: 'HTTP', enabled: true, intervalSeconds: 120, timeoutSeconds: 10, retries: 1, failureThreshold: 0, config: { expectedStatus: '200', certCheck: true } },
      { type: 'json', name: 'JSON value', enabled: true, intervalSeconds: 300, timeoutSeconds: 10, retries: 1, failureThreshold: 0, config: { jsonPath: 'status', jsonExpected: 'ok' } }] },
    { id: 'tcp-service', name: 'TCP service', description: 'Just check that a port accepts connections.', icon: 'link', node: { name: 'Service', host: '192.168.1.20', group: '', tags: [], importance: 'normal', enabled: true }, checks: [
      { type: 'tcp', name: 'Port', enabled: true, intervalSeconds: 60, timeoutSeconds: 5, retries: 1, failureThreshold: 0, config: { port: 443 } }] },
    { id: 'dns', name: 'DNS server', description: 'Ping a resolver and make sure it answers queries.', icon: 'hash', node: { name: 'DNS server', host: '192.168.1.2', group: 'Home Network', tags: ['dns'], importance: 'high', enabled: true }, checks: [
      { type: 'ping', name: 'Ping', enabled: true, intervalSeconds: 60, timeoutSeconds: 3, retries: 1, failureThreshold: 0, config: { pingCount: 4 } },
      { type: 'dns', name: 'Resolves', enabled: true, intervalSeconds: 120, timeoutSeconds: 5, retries: 1, failureThreshold: 0, config: { target: 'example.com', dnsServer: '192.168.1.2' } }] },
  ];

  const logLines = (() => {
    const out = [];
    const t0 = NOW - 3 * DAY - 2 * HOUR;
    const fmt = (t) => new Date(t).toISOString().replace('T', ' ').slice(0, 19);
    out.push(`${fmt(t0)} INFO  gwatch 0.4.1 starting (service mode) data=C:\\ProgramData\\GWatch`);
    out.push(`${fmt(t0 + 120)} INFO  database opened gwatch.db (WAL) size=46.1MB`);
    out.push(`${fmt(t0 + 300)} INFO  scheduler started: 24 checks, 22 enabled, max concurrency 8`);
    out.push(`${fmt(t0 + 900)} INFO  http listening on 127.0.0.1:7230`);
    for (let i = 0; i < 60; i++) {
      const t = NOW - (60 - i) * 4 * MIN;
      const c = nodes[i % nodes.length].checks[0];
      out.push(`${fmt(t)} DEBUG check ${c.id} ${nodes[i % nodes.length].name}/${c.name} ok in ${(CHECK_PROFILES[c.id].base * (0.9 + (i % 5) * 0.05)).toFixed(1)}ms`);
      if (i === 20) out.push(`${fmt(t + 5000)} WARN  smtp send failed: 535 authentication failed (will retry)`);
      if (i === 44) out.push(`${fmt(t + 2000)} ERROR check ${gateway.checks[0].id} Gateway/Ping: no reply (4/4 lost) — consecutive failures=2, now DOWN`);
      if (i === 45) out.push(`${fmt(t + 1000)} INFO  alert sent: Gateway/Ping down → jeff@example.com, sam@example.com`);
      if (i === 46) out.push(`${fmt(t + 1500)} INFO  alert suppressed: Plex/Ping down — dependency Gateway is down`);
    }
    return out;
  })();

  /* ---------- History ---------- */
  // metricHistory serves one of a check's named metrics — an SNMP check's
  // OID, a json check's recorded value — the way the server does: raw points
  // only, with the metric's value repeated in avgMs so the chart helpers can
  // plot it without knowing it is not a latency.
  function metricHistory(checkId, range, metric) {
    const base = history(checkId, range);
    const { c } = findCheck(checkId);
    const units = checkMetricUnits(c);
    if (!(metric in units)) throw Object.assign(new Error(`check ${checkId} does not measure "${metric}"`), { status: 400 });
    const o = (c.config?.snmpOids || []).find((x) => x.name === metric);
    const r = rng(c.id * 977 + metric.length);
    const points = base.points.map((p) => {
      if (p.avgMs == null && c.type !== 'system') return { ...p, avgMs: null, minMs: null, maxMs: null, value: null };
      let v;
      if (c.type === 'system') v = systemMetricResults(c, mockReading('agent:1', 'nas.lan', 29, +new Date(p.ts))).find((m) => m.key === metric)?.value;
      else if (o) { const reading = snmpReading(o, +new Date(p.ts), r); v = reading.value != null ? reading.value : reading.rate; }
      else v = jsonReading(c, +new Date(p.ts));
      return { ...p, value: v ?? null, avgMs: v ?? null, minMs: v ?? null, maxMs: v ?? null, jitterMs: null, lossPct: null };
    });
    return { ...base, source: 'raw', bucketSeconds: 0, metric, metricUnit: units[metric], points };
  }

  function history(checkId, range) {
    const found = findCheck(checkId);
    if (!found) throw Object.assign(new Error('check not found'), { status: 404 });
    const { n, c } = found;
    const p = CHECK_PROFILES[c.id];
    const spans = { '1h': HOUR, '24h': DAY, '7d': 7 * DAY, '30d': 30 * DAY, '1y': 365 * DAY };
    const buckets = { '1h': [Math.min(60, c.intervalSeconds), 'raw'], '24h': [Math.max(60, c.intervalSeconds), 'raw'], '7d': [300, '5m'], '30d': [3600, '1h'], '1y': [86400, '1d'] };
    const span = spans[range] || DAY;
    const [bucketSec, source] = buckets[range] || buckets['24h'];
    const to = Date.now();
    const from = to - span;
    const r = rng(c.id * 131 + span / 1000);
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
      if (isPaused && t > to - 3 * DAY && p.status === 'paused') continue;
      if (p.status === 'unknown') continue;
      // Failures: currently down since downSince; a brief outage at ~20%
      let failedFrac = 0;
      if (p.downSince != null && t >= to - p.downSince) failedFrac = 1;
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
    return { checkId: c.id, checkName: c.name, nodeName: n.name, checkType: c.type, range, source, bucketSeconds: source === 'raw' ? 0 : bucketSec, from: iso(from), to: iso(to), points,
      summary: { availability: total ? +((1 - fails / total) * 100).toFixed(3) : 0, avgMs: cnt ? +(sum / cnt).toFixed(2) : null, minMs: cnt ? min : null, maxMs: cnt ? max : null, count: total, failures: fails } };
  }

  /* ---------- Overview ---------- */
  function overview() {
    const summary = { up: 0, degraded: 0, down: 0, unknown: 0, paused: 0, maintenance: 0, total: nodes.length };
    const outNodes = []; const groupsMap = new Map(); const attention = []; const certWarnings = [];
    for (const n of nodes) {
      const status = nodeStatus(n);
      summary[status] = (summary[status] || 0) + 1;
      // A node counts in every group it is in, so the group totals can come
      // to more than the number of nodes.
      for (const name of (groupsOf(n).length ? groupsOf(n) : ['Ungrouped'])) {
        const g = groupsMap.get(name) || { name, status: 'paused', up: 0, degraded: 0, down: 0, unknown: 0, paused: 0, maintenance: 0, total: 0 };
        g[status]++; g.total++; if (SEV[status] > SEV[g.status]) g.status = status; groupsMap.set(g.name, g);
      }
      const affectedBy = n.checks.map((c) => states[c.id]?.affectedByNodeName).find(Boolean) || '';
      const checks = n.checks.map((c) => ({ check: clone(c), state: stateFor(n, c), lastResult: lastResults[c.id] ? clone(lastResults[c.id]) : null }));
      outNodes.push({ node: clone(n), status, checks, inMaintenance: nodeInMaintenance(n), affectedBy });
      for (const c of n.checks) {
        const st = stateFor(n, c);
        if ((st.status === 'down' || st.status === 'degraded') && n.enabled && c.enabled) attention.push({ nodeId: n.id, nodeName: n.name, checkId: c.id, checkName: c.name, status: st.status, message: st.lastMessage, since: st.lastChangeAt, affectedBy: st.affectedByNodeName || '' });
        const cert = lastResults[c.id]?.details?.cert;
        if (cert && cert.daysRemaining <= (c.config.certWarnDays || settings.alerts.certWarnDays) && !certWarnings.some((w) => w.nodeId === n.id && w.subject === cert.subject)) certWarnings.push({ nodeId: n.id, nodeName: n.name, checkId: c.id, checkName: c.name, daysRemaining: cert.daysRemaining, notAfter: cert.notAfter, subject: cert.subject });
      }
    }
    attention.sort((a, b) => SEV[b.status] - SEV[a.status] || new Date(a.since) - new Date(b.since));
    const openIds = new Set(attention.map((a) => a.checkId));
    const incidents = events.filter((e) => ['down', 'recovered', 'warning', 'cert_warning', 'affected_by_parent'].includes(e.type)).filter((e) => e.checkId == null || openIds.has(e.checkId) || e.type === 'recovered').slice(0, 20);
    return { summary, nodes: outNodes, groups: [...groupsMap.values()], incidents, certWarnings, attention, maintenance: maintenance.filter((w) => windowActive(w)).map((w) => ({ ...w, active: true })), generatedAt: iso(Date.now()) };
  }

  function wallboard() {
    const ov = overview();
    const order = { critical: 0, high: 1, normal: 2, low: 3 };
    const picks = nodes.filter((n) => n.enabled).sort((a, b) => order[a.importance] - order[b.importance]).flatMap((n) => n.checks.filter((c) => c.enabled && CHECK_PROFILES[c.id].status !== 'unknown').slice(0, 1)).slice(0, 6);
    return { ...ov, health: health(), trends: picks.map((c) => history(c.id, '24h')) };
  }

  // One configured wallboard, arranged the way a new one arrives from the
  // service, so the editor and the board itself both have something to work on.
  const wallboards = [{
    id: 1,
    name: 'Office screen',
    sortOrder: 0,
    layout: { columns: 12, theme: 'signal', scale: 1, refreshSeconds: 20, hideChrome: false },
    panels: [
      { id: 'p1', type: 'headline', title: '', width: 6, height: 1, config: {} },
      { id: 'p2', type: 'counts', title: '', width: 4, height: 1, config: {} },
      { id: 'p3', type: 'clock', title: '', width: 2, height: 1, config: { seconds: false } },
      { id: 'p4', type: 'attention', title: 'Needs attention', width: 5, height: 2, config: { limit: 6 } },
      { id: 'p5', type: 'trends', title: 'Trends', width: 7, height: 2, config: { range: '24h', limit: 4, checkIds: [] } },
      { id: 'p6', type: 'groups', title: 'Groups', width: 5, height: 1, config: {} },
      { id: 'p7', type: 'certs', title: 'Certificates', width: 4, height: 1, config: {} },
      { id: 'p8', type: 'health', title: 'Service', width: 3, height: 1, config: {} },
    ],
    share: { enabled: false },
    createdAt: iso(Date.now()), updatedAt: iso(Date.now()),
  }];

  /* ---------- Mutations ---------- */
  function addEvent(type, o) { const e = { id: ++seq.event, ts: iso(Date.now()), type, nodeId: o.nodeId ?? null, checkId: o.checkId ?? null, nodeName: o.nodeName, checkName: o.checkName, title: o.title || '', detail: o.detail || '' }; events.unshift(e); return e; }

  function runCheck(c, n) {
    const p = CHECK_PROFILES[c.id];
    const r = rng(Date.now() % 100000);
    const failed = p.status === 'down';
    const latency = p.base * (1 + (r() - 0.5) * p.noise);
    const res = makeResult(c, n, Date.now(), r, { failed, latency });
    lastResults[c.id] = res;
    (resultLog[c.id] = resultLog[c.id] || []).unshift(res);
    const st = states[c.id] || (states[c.id] = { checkId: c.id, status: 'unknown', consecutiveFailures: 0, lastRunAt: null, lastSuccessAt: null, lastChangeAt: null, nextRunAt: null, lastMessage: '', lastLatencyMs: null, alertActive: false, alertSuppressed: false, lastAlertAt: null, silencedUntil: null, affectedByCheckId: null, warningActive: false, certWarningActive: false, running: false });
    st.lastRunAt = res.ts; st.nextRunAt = iso(Date.now() + c.intervalSeconds * 1000); st.lastMessage = res.message; st.lastLatencyMs = res.latencyMs;
    if (failed) st.consecutiveFailures++; else { st.consecutiveFailures = 0; st.lastSuccessAt = res.ts; }
    if (p.status === 'unknown') { p.status = 'up'; st.status = 'up'; st.lastChangeAt = res.ts; }
    return res;
  }

  function testCheck(check, nodeHost) {
    const type = check.type; const r = rng(Date.now() % 7777);
    const fake = { id: 0, nodeId: 0, ...check, config: check.config || {} };
    CHECK_PROFILES[0] = { base: type === 'ping' ? 4 : type === 'tcp' ? 3 : type === 'dns' ? 12 : 230, noise: 0.3, status: 'up', cert: { days: 71 } };
    const target = fake.config.target || nodeHost || '';
    if (!target) { const res = { id: 0, checkId: 0, ts: iso(Date.now()), success: false, status: 'down', message: 'No target', error: 'No host or URL given — enter a host on the node or a target on the check', latencyMs: null, details: {}, attempts: 1 }; return res; }
    const failed = /fail|bad|nowhere/.test(target);
    CHECK_PROFILES[0].message = failed ? 'Could not resolve host' : '';
    const res = makeResult(fake, { host: nodeHost || target }, Date.now(), r, { failed, latency: CHECK_PROFILES[0].base * (0.8 + r() * 0.4) });
    if (failed) res.error = 'lookup ' + target + ': no such host';
    return res;
  }

  /* ---------- Routing ---------- */
  const routes = [];
  const on = (method, re, fn) => routes.push({ method, re, fn });
  const err = (status, message) => Object.assign(new Error(message), { status });

  on('GET', /^\/api\/health$/, () => health());
  on('GET', /^\/api\/version$/, () => ({ version: '0.4.1', platform: 'windows/amd64', apiVersion: 1 }));
  // The mock always plays an administrator on the machine GWatch runs on:
  // there is nothing to sign in to, so the sign-in screen never appears.
  on('GET', /^\/api\/me$/, () => ({ kind: 'local', name: 'this computer', role: 'admin', isAdmin: true, canWrite: true, signedIn: false, theme: settings.general.theme, accentColor: settings.general.accentColor, indicators: clone(settings.indicators || []) }));
  on('GET', /^\/api\/auth\/setup$/, () => ({ usersConfigured: false, loginRequired: false, accessPasswordSet: false, apiVersion: 1 }));
  on('GET', /^\/api\/users$/, () => []);
  on('GET', /^\/api\/apikeys$/, () => []);
  // Settings › AI & MCP. The fixture has a download of an older skill behind
  // it, so the "updated since" note is exercised. The download itself is a
  // real file the service hands out; the mock only answers the status.
  on('GET', /^\/api\/mcp\/status$/, () => ({ skillVersion: '1.1.0', lastDownloadedVersion: '1.0.0', lastDownloadedAt: iso(Date.now() - 9 * DAY), lastDownloadedBy: 'local', updateAvailable: true }));
  on('GET', /^\/api\/mcp\/skill$/, () => ({ __csv: '---\nname: gwatch\nversion: 1.1.0\n---\n# Working with GWatch\n\n(The real service sends the skill; the demo sends this stand-in.)\n' }));
  on('GET', /^\/api\/overview$/, () => overview());
  on('GET', /^\/api\/wallboard$/, () => wallboard());
  on('GET', /^\/api\/wallboards$/, () => clone(wallboards));
  on('GET', /^\/api\/wallboards\/(\d+)$/, (m) => { const b = wallboards.find((x) => x.id === Number(m[1])); if (!b) throw err(404, 'wallboard not found'); return clone(b); });
  on('POST', /^\/api\/wallboards$/, (m, body) => {
    const def = clone(wallboards[0]);
    const b = { id: ++seq.wall, name: body.name || 'Untitled', sortOrder: wallboards.length, layout: body.layout || def.layout, panels: body.panels?.length ? body.panels : def.panels, share: { enabled: false }, createdAt: iso(Date.now()), updatedAt: iso(Date.now()) };
    wallboards.push(b);
    return clone(b);
  });
  on('PUT', /^\/api\/wallboards\/(\d+)$/, (m, body) => { const b = wallboards.find((x) => x.id === Number(m[1])); if (!b) throw err(404, 'wallboard not found'); b.name = body.name ?? b.name; b.layout = body.layout ?? b.layout; b.panels = body.panels ?? b.panels; b.updatedAt = iso(Date.now()); return clone(b); });
  on('DELETE', /^\/api\/wallboards\/(\d+)$/, (m) => { const i = wallboards.findIndex((x) => x.id === Number(m[1])); if (i < 0) throw err(404, 'wallboard not found'); wallboards.splice(i, 1); return { ok: true }; });
  on('POST', /^\/api\/wallboards\/(\d+)\/share$/, (m, body) => {
    const b = wallboards.find((x) => x.id === Number(m[1]));
    if (!b) throw err(404, 'wallboard not found');
    b.share = body.enabled ? { enabled: true, token: (!body.rotate && b.share.token) || 'demotoken' + b.id } : { enabled: false };
    return clone(b);
  });
  on('GET', /^\/api\/wallboards\/(\d+)\/view$/, (m) => {
    const b = wallboards.find((x) => x.id === Number(m[1]));
    if (!b) throw err(404, 'wallboard not found');
    return { ...wallboard(), wallboard: clone(b), serverNow: iso(Date.now()) };
  });
  on('GET', /^\/api\/templates$/, () => templates);
  on('GET', /^\/api\/groups$/, () => {
    const g = new Map(); const t = new Map();
    for (const n of nodes) { for (const name of groupsOf(n)) g.set(name, (g.get(name) || 0) + 1); for (const tag of n.tags || []) t.set(tag, (t.get(tag) || 0) + 1); }
    return { groups: [...g].map(([name, count]) => ({ name, count })).sort((a, b) => a.name.localeCompare(b.name)), tags: [...t].map(([name, count]) => ({ name, count })).sort((a, b) => a.name.localeCompare(b.name)) };
  });
  on('GET', /^\/api\/nodes$/, () => nodes.map((n) => nodeOut(n)));
  on('GET', /^\/api\/nodes\/(\d+)$/, (m) => { const n = findNode(m[1]); if (!n) throw err(404, 'node not found'); return nodeOut(n, { withResults: true }); });
  on('POST', /^\/api\/nodes$/, (m, body) => {
    if (!body?.name) throw err(400, 'name is required');
    const n = mkNode({ ...body, template: body.template || '' });
    n.checks = (body.checks || []).map((c, i) => { const created = mkCheck(n.id, c.type, c.name, { interval: c.intervalSeconds, timeout: c.timeoutSeconds, failureThreshold: c.failureThreshold, config: c.config, alerts: c.alerts, enabled: c.enabled, sortOrder: i, base: c.type === 'ping' ? 3 : 120, status: 'unknown' }); created.retries = c.retries ?? 1; return created; });
    nodes.push(n);
    addEvent('config_changed', { nodeId: n.id, nodeName: n.name, title: 'Node created', detail: `${n.checks.length} checks` });
    return nodeOut(n, { withResults: true });
  });
  on('PUT', /^\/api\/nodes\/(\d+)$/, (m, body) => {
    const n = findNode(m[1]); if (!n) throw err(404, 'node not found');
    const groups = (body.groups && body.groups.length ? body.groups : (body.group ? [body.group] : [])).slice(0, 16);
    Object.assign(n, { name: body.name, host: body.host, groups, group: groups[0] || '', tags: body.tags || [], notes: body.notes || '', importance: body.importance || 'normal', enabled: body.enabled !== false, dependsOnNodeId: body.dependsOnNodeId ?? null, updatedAt: iso(Date.now()) });
    const keep = new Set();
    const next = (body.checks || []).map((c, i) => {
      const existing = c.id ? n.checks.find((x) => x.id === c.id) : null;
      if (existing) { Object.assign(existing, { name: c.name, enabled: c.enabled !== false, intervalSeconds: c.intervalSeconds, timeoutSeconds: c.timeoutSeconds, retries: c.retries ?? 0, failureThreshold: c.failureThreshold || 0, config: c.config || {}, alerts: c.alerts || null, sortOrder: i, updatedAt: iso(Date.now()) }); keep.add(existing.id); return existing; }
      const created = mkCheck(n.id, c.type, c.name, { interval: c.intervalSeconds, timeout: c.timeoutSeconds, failureThreshold: c.failureThreshold, config: c.config, alerts: c.alerts, enabled: c.enabled, sortOrder: i, base: c.type === 'ping' ? 3 : 120, status: 'unknown' });
      keep.add(created.id); return created;
    });
    n.checks = next;
    addEvent('config_changed', { nodeId: n.id, nodeName: n.name, title: 'Node updated', detail: `${n.checks.length} checks` });
    return nodeOut(n, { withResults: true });
  });
  // Bulk edit. It mirrors internal/api/bulk.go closely enough that the screen
  // behaves the same here as against the service: the same selection rules,
  // the same whitelist, the same shape of answer.
  const BULK_CONFIG_KEYS = ['certWarnDays', 'latencyWarnMs', 'metricThresholds', 'packetLossWarnPct', 'pingMethod'];
  on('PATCH', /^\/api\/nodes\/bulk$/, (m, body) => {
    const nodeIds = body?.nodeIds || [];
    const checkIds = body?.checkIds || [];
    if (!nodeIds.length && !checkIds.length) throw err(400, 'choose at least one node or check to change');
    const np = body?.node, cp = body?.check;
    const hasNode = np && Object.keys(np).length, hasCheck = cp && Object.keys(cp).length;
    if (!hasNode && !hasCheck) throw err(400, 'choose at least one setting to change');
    if (hasCheck) for (const k of Object.keys(cp.config || {})) {
      if (!BULK_CONFIG_KEYS.includes(k)) throw err(400, `unknown config key(s) "${k}"; a bulk edit may set ${BULK_CONFIG_KEYS.map((x) => `"${x}"`).join(', ')}`);
    }
    const types = body?.checkFilter?.types || [];
    const wanted = new Set(types);
    const picked = [];
    const seen = new Set();
    const take = (c) => { if (seen.has(c.id) || (wanted.size && !wanted.has(c.type))) return; seen.add(c.id); picked.push(c); };
    for (const id of checkIds) { const f = findCheck(id); if (!f) throw err(404, `check ${id} no longer exists`); take(f.c); }
    const pickedNodes = [];
    for (const id of nodeIds) {
      const n = findNode(id); if (!n) throw err(400, `node ${id} no longer exists`);
      if (!pickedNodes.includes(n)) pickedNodes.push(n);
      for (const c of n.checks) take(c);
    }
    if (hasNode && !pickedNodes.length) throw err(400, 'the node settings have no nodes to apply to: select some nodes as well as checks');
    if (hasCheck && !picked.length) throw err(400, wanted.size ? 'nothing to change: the selection holds no checks of the chosen type(s)' : 'nothing to change: the selected nodes have no checks');
    const changes = [];
    if (hasNode) {
      const fold = (list, drop) => list.filter((x) => !(drop || []).some((d) => String(d).toLowerCase() === String(x).toLowerCase()));
      const dedupe = (list) => { const out = [], seenL = new Set(); for (const v of list) { const k = String(v).trim().toLowerCase(); if (!k || seenL.has(k)) continue; seenL.add(k); out.push(String(v).trim()); } return out; };
      for (const n of pickedNodes) {
        let groups = np.groups ? [...np.groups] : groupsOf(n);
        groups = dedupe(fold(groups, np.removeGroups).concat(np.addGroups || [])).slice(0, 16);
        n.groups = groups; n.group = groups[0] || '';
        let tags = np.tags ? [...np.tags] : (n.tags || []);
        n.tags = dedupe(fold(tags, np.removeTags).concat(np.addTags || []));
        if (np.importance != null) n.importance = np.importance;
        if (np.enabled != null) n.enabled = !!np.enabled;
        if ('dependsOnNodeId' in np) n.dependsOnNodeId = np.dependsOnNodeId || null;
        n.updatedAt = iso(Date.now());
      }
      if (np.groups) changes.push(np.groups.length ? `groups → ${np.groups.join(', ')}` : 'groups cleared');
      if (np.addGroups?.length) changes.push(`groups +${np.addGroups.join(', +')}`);
      if (np.removeGroups?.length) changes.push(`groups −${np.removeGroups.join(', −')}`);
      if (np.tags) changes.push(np.tags.length ? `tags → ${np.tags.join(', ')}` : 'tags cleared');
      if (np.addTags?.length) changes.push(`tags +${np.addTags.join(', +')}`);
      if (np.removeTags?.length) changes.push(`tags −${np.removeTags.join(', −')}`);
      if (np.importance != null) changes.push(`importance → ${np.importance}`);
      if (np.enabled != null) changes.push(np.enabled ? 'enabled' : 'disabled');
      if ('dependsOnNodeId' in np) changes.push(np.dependsOnNodeId ? `depends on → ${findNode(np.dependsOnNodeId)?.name || np.dependsOnNodeId}` : 'dependency cleared');
    }
    if (hasCheck) {
      for (const c of picked) {
        if (cp.intervalSeconds != null) c.intervalSeconds = cp.intervalSeconds;
        if (cp.timeoutSeconds != null) c.timeoutSeconds = cp.timeoutSeconds;
        if (cp.retries != null) c.retries = cp.retries;
        if (cp.failureThreshold != null) c.failureThreshold = cp.failureThreshold;
        if (cp.enabled != null) c.enabled = !!cp.enabled;
        if ('alerts' in cp) c.alerts = cp.alerts ? { ...cp.alerts } : null;
        if (cp.config) {
          const { metricThresholds, ...rest } = cp.config;
          Object.assign(c.config, rest);
          // Merged by metric key, and only onto a hardware check, the way
          // the service does it.
          if (metricThresholds && c.type === 'system') {
            const keep = (c.config.metricThresholds || []).filter((t) => !metricThresholds.some((p) => p.metric === t.metric));
            c.config.metricThresholds = [...keep, ...metricThresholds.map((t) => ({ ...t }))];
          }
        }
        c.updatedAt = iso(Date.now());
        if (cp.intervalSeconds != null && states[c.id]) states[c.id].nextRunAt = iso(Date.now() + c.intervalSeconds * 1000);
      }
      if (cp.intervalSeconds != null) changes.push(`interval → ${cp.intervalSeconds} s`);
      if (cp.timeoutSeconds != null) changes.push(`timeout → ${cp.timeoutSeconds} s`);
      if (cp.retries != null) changes.push(`retries → ${cp.retries}`);
      if (cp.failureThreshold != null) changes.push(cp.failureThreshold === 0 ? 'failures before down → global default' : `failures before down → ${cp.failureThreshold}`);
      if (cp.enabled != null) changes.push(cp.enabled ? 'enabled' : 'disabled');
      if ('alerts' in cp) changes.push(cp.alerts ? 'alert overrides replaced' : 'alert overrides cleared');
      for (const k of BULK_CONFIG_KEYS) {
        if (!cp.config || !(k in cp.config)) continue;
        const v = cp.config[k];
        if (k === 'latencyWarnMs') changes.push(`latency warning → ${v} ms`);
        else if (k === 'packetLossWarnPct') changes.push(`packet loss warning → ${v} %`);
        else if (k === 'certWarnDays') changes.push(`certificate warning → ${v} days`);
        else if (k === 'metricThresholds') for (const t of v) changes.push(`${t.metric} thresholds → warning ${t.warn ?? 'off'}, critical ${t.crit ?? 'off'}`);
        else changes.push(`ping method → ${v || 'global setting'}`);
      }
    }
    const counts = { nodes: hasNode ? pickedNodes.length : 0, checks: hasCheck ? picked.length : 0 };
    addEvent('config_changed', { title: `Bulk edit: ${changes.join('; ')}`, detail: `Applied to ${counts.nodes} node(s) and ${counts.checks} check(s).` });
    return { ...counts, changes };
  });
  on('DELETE', /^\/api\/nodes\/(\d+)$/, (m) => { const i = nodes.findIndex((n) => n.id === Number(m[1])); if (i < 0) throw err(404, 'node not found'); const [n] = nodes.splice(i, 1); addEvent('config_changed', { title: `Node "${n.name}" deleted` }); return { ok: true }; });
  on('POST', /^\/api\/nodes\/(\d+)\/enable$/, (m, body) => { const n = findNode(m[1]); if (!n) throw err(404, 'node not found'); n.enabled = !!body.enabled; addEvent('config_changed', { nodeId: n.id, nodeName: n.name, title: `Node ${n.enabled ? 'enabled' : 'disabled'}` }); return nodeOut(n); });
  on('POST', /^\/api\/nodes\/(\d+)\/duplicate$/, (m) => { const n = findNode(m[1]); if (!n) throw err(404, 'node not found'); const copy = mkNode({ ...n, name: `${n.name} (copy)`, enabled: false }); copy.checks = n.checks.map((c, i) => mkCheck(copy.id, c.type, c.name, { interval: c.intervalSeconds, timeout: c.timeoutSeconds, config: clone(c.config), alerts: c.alerts, enabled: c.enabled, sortOrder: i, base: CHECK_PROFILES[c.id]?.base ?? 20, status: 'unknown' })); nodes.push(copy); return nodeOut(copy); });
  on('POST', /^\/api\/nodes\/(\d+)\/run$/, (m) => { const n = findNode(m[1]); if (!n) throw err(404, 'node not found'); return n.checks.filter((c) => c.enabled).map((c) => runCheck(c, n)); });
  on('POST', /^\/api\/checks\/test$/, (m, body) => { if (!body?.check) throw err(400, 'check is required'); return testCheck(body.check, body.nodeHost); });
  on('POST', /^\/api\/checks\/(\d+)\/run$/, (m) => { const f = findCheck(m[1]); if (!f) throw err(404, 'check not found'); return runCheck(f.c, f.n); });
  on('POST', /^\/api\/checks\/(\d+)\/enable$/, (m, body) => { const f = findCheck(m[1]); if (!f) throw err(404, 'check not found'); f.c.enabled = !!body.enabled; return clone(f.c); });
  on('POST', /^\/api\/checks\/(\d+)\/silence$/, (m, body) => { const f = findCheck(m[1]); if (!f) throw err(404, 'check not found'); const mins = Number(body.minutes) || 0; if (mins > 0) { silences[f.c.id] = Date.now() + mins * MIN; addEvent('silenced', { nodeId: f.n.id, nodeName: f.n.name, checkId: f.c.id, checkName: f.c.name, title: `Alerts silenced for ${mins} minutes` }); } else { delete silences[f.c.id]; addEvent('unsilenced', { nodeId: f.n.id, nodeName: f.n.name, checkId: f.c.id, checkName: f.c.name, title: 'Silence removed' }); } return stateFor(f.n, f.c); });
  on('GET', /^\/api\/checks\/(\d+)\/results$/, (m, body, u) => { const f = findCheck(m[1]); if (!f) throw err(404, 'check not found'); const limit = Number(u.searchParams.get('limit')) || 50; return (resultLog[f.c.id] || []).slice(0, limit); });
  on('GET', /^\/api\/checks\/(\d+)\/state$/, (m) => { const f = findCheck(m[1]); if (!f) throw err(404, 'check not found'); return stateFor(f.n, f.c); });
  on('GET', /^\/api\/history$/, (m, body, u) => { const ids = u.searchParams.getAll('checkId'); const range = u.searchParams.get('range') || '24h'; const metric = u.searchParams.get('metric'); if (ids.length === 1) return metric ? metricHistory(ids[0], range, metric) : history(ids[0], range); return ids.map((id) => history(id, range)); });
  on('GET', /^\/api\/history\/multi$/, (m, body, u) => { const ids = u.searchParams.getAll('checkId'); const range = u.searchParams.get('range') || '24h'; return ids.map((id) => history(id, range)); });

  // "Walk this device": a plausible mib-2 subtree for a four-port switch, so
  // the editor's picker can be seen without a real device on the network.
  on('POST', /^\/api\/snmp\/walk$/, (m, body) => {
    if (!body?.host) throw err(400, 'a host is required');
    const rows = [
      { oid: '1.3.6.1.2.1.1.1.0', type: 'OctetString', value: 'MikroTik CRS310, RouterOS 7.14', name: 'Description', kind: 'gauge' },
      { oid: '1.3.6.1.2.1.1.3.0', type: 'TimeTicks', value: '41235000', name: 'Uptime', kind: 'gauge' },
      { oid: '1.3.6.1.2.1.1.5.0', type: 'OctetString', value: 'office-switch', name: 'Device name', kind: 'gauge' },
    ];
    const ports = ['ether1-wan', 'ether2-office', 'ether3-loft', 'sfp-uplink'];
    ports.forEach((label, i) => {
      const n = i + 1;
      rows.push({ oid: `1.3.6.1.2.1.2.2.1.2.${n}`, type: 'OctetString', value: label, name: `Port ${n} name`, kind: 'gauge' });
      rows.push({ oid: `1.3.6.1.2.1.2.2.1.8.${n}`, type: 'Integer', value: n === 3 ? '2' : '1', name: `Port ${n} link`, kind: 'gauge' });
      rows.push({ oid: `1.3.6.1.2.1.31.1.1.1.6.${n}`, type: 'Counter64', value: String(1.4e11 + n * 7e8), name: `Port ${n} in`, kind: 'counter' });
      rows.push({ oid: `1.3.6.1.2.1.31.1.1.1.10.${n}`, type: 'Counter64', value: String(9.2e10 + n * 3e8), name: `Port ${n} out`, kind: 'counter' });
      rows.push({ oid: `1.3.6.1.2.1.2.2.1.14.${n}`, type: 'Counter32', value: n === 3 ? '1842' : '0', name: `Port ${n} errors in`, kind: 'counter' });
    });
    return { rows, truncated: false, max: 500 };
  });
  on('GET', /^\/api\/events$/, (m, body, u) => {
    const q = (u.searchParams.get('q') || '').toLowerCase();
    const until = u.searchParams.get('until');
    if (q || until) {
      const untilT = until ? +new Date(until) : Infinity;
      const filtered = events.filter((e) => (!q || [e.title, e.detail, e.nodeName, e.checkName, e.type].some((x) => (x || '').toLowerCase().includes(q))) && +new Date(e.ts) < untilT);
      const before = Number(u.searchParams.get('before')) || 0;
      const type = u.searchParams.get('type');
      const nid = u.searchParams.get('nodeId');
      return clone(filtered.filter((e) => (!before || e.id < before) && (!type || e.type === type) && (!nid || e.nodeId === Number(nid))).slice(0, Number(u.searchParams.get('limit')) || 100));
    }
    let list = events;
    const type = u.searchParams.get('type'); const nodeId = u.searchParams.get('nodeId'); const checkId = u.searchParams.get('checkId'); const before = u.searchParams.get('before'); const limit = Number(u.searchParams.get('limit')) || 100;
    if (type) list = list.filter((e) => e.type === type || (type === 'warning' && e.type === 'warning_cleared') || (type === 'cert_warning' && e.type === 'cert_warning_cleared') || (type === 'silenced' && e.type === 'unsilenced'));
    if (nodeId) list = list.filter((e) => String(e.nodeId) === nodeId);
    if (checkId) list = list.filter((e) => String(e.checkId) === checkId);
    if (before) list = list.filter((e) => e.id < Number(before));
    return list.slice(0, limit);
  });
  on('POST', /^\/api\/events\/note$/, (m, body) => { if (!body?.text) throw err(400, 'text is required'); const n = body.nodeId ? findNode(body.nodeId) : null; return addEvent('note', { nodeId: n?.id ?? null, nodeName: n?.name, title: body.text, detail: '' }); });
  on('GET', /^\/api\/maintenance$/, () => maintenance.map((w) => ({ ...w, active: windowActive(w) })));
  on('POST', /^\/api\/maintenance$/, (m, body) => { const w = { ...body, id: ++seq.maint, createdAt: iso(Date.now()) }; delete w.active; maintenance.push(w); return { ...w, active: windowActive(w) }; });
  on('PUT', /^\/api\/maintenance\/(\d+)$/, (m, body) => { const w = maintenance.find((x) => x.id === Number(m[1])); if (!w) throw err(404, 'window not found'); Object.assign(w, body, { id: w.id }); delete w.active; return { ...w, active: windowActive(w) }; });
  on('DELETE', /^\/api\/maintenance\/(\d+)$/, (m) => { const i = maintenance.findIndex((x) => x.id === Number(m[1])); if (i < 0) throw err(404, 'window not found'); maintenance.splice(i, 1); return { ok: true }; });
  on('GET', /^\/api\/dashboards$/, () => clone(dashboards));
  on('POST', /^\/api\/dashboards$/, (m, body) => { const d = { id: ++seq.dash, name: body.name || 'Untitled', sortOrder: dashboards.length, widgets: body.widgets || [], createdAt: iso(Date.now()), updatedAt: iso(Date.now()) }; dashboards.push(d); return clone(d); });
  on('PUT', /^\/api\/dashboards\/(\d+)$/, (m, body) => { const d = dashboards.find((x) => x.id === Number(m[1])); if (!d) throw err(404, 'dashboard not found'); d.name = body.name ?? d.name; d.widgets = body.widgets ?? d.widgets; d.updatedAt = iso(Date.now()); return clone(d); });
  on('DELETE', /^\/api\/dashboards\/(\d+)$/, (m) => { const i = dashboards.findIndex((x) => x.id === Number(m[1])); if (i < 0) throw err(404, 'dashboard not found'); dashboards.splice(i, 1); return { ok: true }; });
  on('GET', /^\/api\/settings$/, () => clone(settings));
  on('PUT', /^\/api\/settings$/, (m, body) => { settings = clone(body); if (settings.alerts?.smtp?.password) settings.alerts.smtp.password = '********'; if (settings.general?.accessPassword) settings.general.accessPassword = '********'; if (!settings.indicators?.length) settings.indicators = clone(DEFAULT_INDICATORS); addEvent('config_changed', { title: 'Settings changed' }); return clone(settings); });
  on('POST', /^\/api\/settings\/test-email$/, (m, body) => { if (!settings.alerts.smtp.host) throw err(400, 'SMTP host is not configured'); return { ok: true, message: `Test email sent to ${body?.to || settings.alerts.recipients.join(', ')} via ${settings.alerts.smtp.host}` }; });
  on('GET', /^\/api\/retention\/status$/, () => retention);
  // Settings › Database: the connection GWatch opens at the next start
  // (database.json), never the settings document. The demo runs on SQLite and
  // remembers what was saved, so the "restart needed" banner can be seen.
  let savedDB = { driver: 'sqlite', path: 'C:\\ProgramData\\GWatch\\gwatch.db' };
  const dbStatus = () => ({
    active: { driver: 'sqlite', label: 'modernc.org/sqlite', description: 'C:\\ProgramData\\GWatch\\gwatch.db', schemaVersion: 3, sizeBytes: 48_300_000 },
    saved: clone(savedDB), source: savedDB.driver === 'sqlite' ? 'default' : 'file', file: 'C:\\ProgramData\\GWatch\\database.json', restartRequired: savedDB.driver !== 'sqlite',
  });
  const checkDB = (body) => {
    if (!body || !['sqlite', 'postgres', 'mysql'].includes(body.driver)) throw err(400, `unknown database driver "${body?.driver}" (use sqlite, postgres or mysql)`);
    if (body.driver !== 'sqlite') { if (!body.host) throw err(400, 'a database host is required'); if (!body.database) throw err(400, 'a database name is required'); if (!body.user) throw err(400, 'a database user is required'); if (/unreachable|nowhere/.test(body.host)) throw err(502, `Connection failed: connect to ${body.driver}://${body.user}@${body.host}:${body.port}/${body.database}: connection refused`); }
  };
  on('GET', /^\/api\/database$/, () => dbStatus());
  on('POST', /^\/api\/database\/test$/, (m, body) => { checkDB(body); const v = body.driver === 'sqlite' ? 'SQLite 3.46.0' : body.driver === 'postgres' ? 'PostgreSQL 16.4 on x86_64-pc-linux-gnu' : '8.4.2 MySQL Community Server'; return { ok: true, driver: body.driver, serverVersion: v, database: body.driver === 'sqlite' ? 'C:\\ProgramData\\GWatch\\gwatch.db' : `${body.driver}://${body.user}@${body.host}:${body.port}/${body.database}`, message: `Connected (${v}).` }; });
  on('PUT', /^\/api\/database$/, (m, body) => { checkDB(body); savedDB = body.driver === 'sqlite' ? { driver: 'sqlite', path: 'C:\\ProgramData\\GWatch\\gwatch.db' } : { ...body, password: body.password ? '********' : '' }; addEvent('config_changed', { title: 'Database connection changed', detail: body.driver === 'sqlite' ? 'Next start uses the SQLite file.' : `Next start uses ${body.driver}://${body.user}@${body.host}:${body.port}/${body.database}.` }); return dbStatus(); });
  on('POST', /^\/api\/retention\/run$/, () => { retention = { ...retention, lastRunAt: iso(Date.now()), lastDurationMs: 1830, deletedLastRun: 1043, rawRows: retention.rawRows - 1043, rollupRows5m: retention.rollupRows5m + 288 }; addEvent('retention', { title: 'Retention run finished', detail: 'Rolled up 1,043 raw results · 1.8 s' }); return retention; });
  on('GET', /^\/api\/backups$/, () => ({ backups: clone(backups), dir: 'C:\\ProgramData\\GWatch\\backups', status: backupStatus }));
  on('POST', /^\/api\/backups$/, (m, body) => { if (!body?.password) throw err(400, 'password is required'); const d = new Date(); const b = { fileName: `gwatch-${d.toISOString().slice(0, 10)}-${String(d.getHours()).padStart(2, '0')}${String(d.getMinutes()).padStart(2, '0')}${body.includeHistory ? '' : '-config'}.gwbackup`, createdAt: iso(Date.now()), sizeBytes: body.includeHistory ? 13_400_000 : 41_000, includeHistory: !!body.includeHistory, encrypted: true }; backups.unshift(b); backupStatus = { lastBackupAt: b.createdAt, lastBackupOk: true, lastBackupFile: b.fileName, lastError: '', lastRestoreAt: backupStatus.lastRestoreAt }; addEvent('backup', { title: 'Backup created', detail: b.fileName }); return b; });
  on('DELETE', /^\/api\/backups\/([^/]+)$/, (m) => { const name = decodeURIComponent(m[1]); const i = backups.findIndex((b) => b.fileName === name); if (i < 0) throw err(404, 'backup not found'); backups.splice(i, 1); return { ok: true }; });
  on('POST', /^\/api\/backups\/restore$/, (m, body) => { if (body instanceof FormData && body.get('password') === 'wrong') throw err(400, 'wrong password or corrupt archive'); backupStatus.lastRestoreAt = iso(Date.now()); addEvent('restore', { title: 'Restored from uploaded backup' }); return { ok: true, nodes: nodes.length, checks: nodes.flatMap((n) => n.checks).length, results: 38110 }; });
  on('POST', /^\/api\/backups\/restore-existing$/, (m, body) => { if (!backups.some((b) => b.fileName === body.fileName)) throw err(404, 'backup not found'); if (body.password === 'wrong') throw err(400, 'wrong password or corrupt archive'); backupStatus.lastRestoreAt = iso(Date.now()); addEvent('restore', { title: 'Restored from backup', detail: body.fileName }); return { ok: true, nodes: nodes.length, checks: nodes.flatMap((n) => n.checks).length, results: body.includeHistory ? 38110 : 0 }; });
  on('GET', /^\/api\/logs$/, (m, body, u) => ({ lines: logLines.slice(-(Number(u.searchParams.get('limit')) || 200)), file: 'C:\\ProgramData\\GWatch\\logs\\gwatch.log' }));
  on('GET', /^\/api\/export\/history\.csv$/, (m, body, u) => { const hs = history(u.searchParams.get('checkId'), u.searchParams.get('range') || '30d'); return { __csv: ['timestamp,avg_ms,min_ms,max_ms,jitter_ms,loss_pct,availability_pct,count,failures', ...hs.points.map((p) => [p.ts, p.avgMs ?? '', p.minMs ?? '', p.maxMs ?? '', p.jitterMs ?? '', p.lossPct ?? '', p.availability, p.count, p.failures].join(','))].join('\n') }; });
  on('GET', /^\/api\/export\/results\.csv$/, (m, body, u) => ({ __csv: ['timestamp,success,status,message,latency_ms', ...(resultLog[u.searchParams.get('checkId')] || []).map((r) => [r.ts, r.success, r.status, JSON.stringify(r.message), r.latencyMs ?? ''].join(','))].join('\n') }));
  on('GET', /^\/api\/export\/events\.csv$/, () => ({ __csv: ['timestamp,type,node,check,title,detail', ...events.map((e) => [e.ts, e.type, e.nodeName || '', e.checkName || '', JSON.stringify(e.title), JSON.stringify(e.detail)].join(','))].join('\n') }));
  on('GET', /^\/api\/export\/config\.json$/, () => ({ nodes: clone(nodes), dashboards: clone(dashboards), maintenance: clone(maintenance) }));

  /* ---------- Added endpoints: status, network, charts, automation, updates ---------- */
  let savedCharts = [{ id: 'chart-1', name: 'Gateway latency', config: { checkIds: [gateway.checks[0].id], metric: 'avg', range: '24h', style: 'area', threshold: 40 }, updatedAt: ago(3 * DAY) }];
  const triggers = [{ id: 1, nodeId: plex.id, name: 'Restart Plex container', description: '', enabled: true, on: ['down'], checkId: null, latencyOverMs: 0, action: { type: 'script', interpreter: 'sh', code: 'docker restart plex' }, cooldownMinutes: 30, lastRunAt: ago(2 * HOUR), lastStatus: 'ok', lastOutput: 'plex', runCount: 3, createdAt: ago(10 * DAY), updatedAt: ago(10 * DAY) },
    { id: 2, nodeId: gateway.id, name: 'Post to Discord', description: 'Outage channel', enabled: true, on: ['down', 'recovered'], checkId: null, latencyOverMs: 0, action: { type: 'http', method: 'POST', url: 'https://discord.com/api/webhooks/…', body: '{"content":"{{node.name}} is {{status}}"}' }, cooldownMinutes: 0, lastRunAt: null, lastStatus: '', lastOutput: '', runCount: 0, createdAt: ago(3 * DAY), updatedAt: ago(3 * DAY) }];
  const endpoints = [{ id: 1, name: 'Router rebooted', slug: 'router-rebooted', description: 'Called by the router after a reboot', enabled: true, method: 'POST', token: 'abc123', action: { type: 'run_node', nodeId: gateway.id }, lastCalledAt: ago(5 * DAY), lastStatus: 'ok', lastOutput: 'Ran the checks of node 21.', callCount: 4, createdAt: ago(20 * DAY), updatedAt: ago(20 * DAY) }];
  let updateStatus = { last: null, applying: false, applied: false, restarting: false, lastApplyAt: null, lastError: '', executable: 'C:\\Program Files\\GWatch\\gwatch.exe', canApply: true };
  on('GET', /^\/api\/status$/, () => { const ov = overview(); return { down: ov.summary.down, degraded: ov.summary.degraded, unknown: ov.summary.unknown, up: ov.summary.up, total: ov.summary.total, certWarnings: ov.certWarnings.length, maintenance: ov.summary.maintenance, serviceOk: true, serviceIssues: [], attention: ov.attention.length, generatedAt: iso(Date.now()) }; });
  on('GET', /^\/api\/network$/, () => ({ listenAddress: settings.general.remoteAccess ? ':7230' : '127.0.0.1:7230', remoteAccess: !!settings.general.remoteAccess, passwordSet: !!settings.general.accessPassword, port: 7230, localUrl: 'http://127.0.0.1:7230', lanUrls: settings.general.remoteAccess ? ['http://192.168.1.10:7230', 'http://desktop-pc:7230'] : [], hostname: 'desktop-pc', restartNeeded: false }));
  /* ---------- Discovery ---------- */
  // A sweep that finds three devices over about two seconds. The progress is
  // derived from the clock rather than from a timer, so the run advances
  // whether the modal is watching the stream or polling — and a test that
  // drives it can simply wait.
  const DISCOVERY_MS = 2000;
  const DISCOVERY_FOUND = [
    { ip: '192.168.1.1', hostname: 'gateway.lan', rttMs: 1.8, openPorts: [80, 443], template: 'router', note: 'Only a web interface answered — looks like a router, switch or access point.' },
    { ip: '192.168.1.23', hostname: 'pi.lan', rttMs: 0.9, openPorts: [22, 80], template: 'home-server', note: 'SSH and a web interface — looks like a server or NAS.' },
    { ip: '192.168.1.64', hostname: '', rttMs: 5.4, openPorts: [9100], template: 'tcp-service', note: 'Port 9100 is open — this looks like a network printer.' },
  ];
  let discoveryJob = null;
  // The last add, so a test can prove the request was made.
  window.__gwatchMockDiscoveryAdds = [];

  function discoveryView() {
    if (!discoveryJob) return null;
    const j = discoveryJob;
    if (j.state === 'running') {
      const elapsed = Date.now() - +new Date(j.startedAt);
      const ratio = Math.min(1, elapsed / DISCOVERY_MS);
      j.scanned = Math.round(j.total * ratio);
      j.responders = Math.floor(DISCOVERY_FOUND.length * ratio);
      if (ratio >= 1) {
        j.state = 'done';
        j.scanned = j.total;
        j.results = clone(DISCOVERY_FOUND);
        j.responders = j.results.length;
        j.finishedAt = iso(Date.now());
        addEvent('discovery', { title: `Discovery scanned ${j.total} addresses in ${j.ranges.join(', ')}: ${j.responders} responded` });
      }
    }
    return clone(j);
  }

  on('GET', /^\/api\/discovery$/, () => { const j = discoveryView(); if (!j) throw err(404, 'no discovery has been run yet'); return j; });
  on('POST', /^\/api\/discovery$/, (m, body) => {
    const current = discoveryView();
    if (current && current.state === 'running') throw err(409, 'a discovery run is already going; wait for it to finish or cancel it first');
    const ranges = (body?.ranges || []).map((s) => String(s).trim()).filter(Boolean);
    if (!ranges.length) throw err(400, 'give at least one range, such as 192.168.1.0/24 or 192.168.1.10-50');
    if (ranges.some((r) => r.includes(':'))) throw err(400, `${ranges[0]} is IPv6; discovery sweeps IPv4 only for now`);
    discoveryJob = {
      // As on the server: no ports field means the defaults, an empty one
      // means probe nothing.
      id: `mock-${++seq.discovery}`, ranges, ports: body?.ports === undefined ? [22, 80, 443, 445, 3389, 8080, 8443, 9100, 32400, 1883] : body.ports,
      state: 'running', total: 254, scanned: 0, responders: 0, results: [], startedAt: iso(Date.now()), finishedAt: null,
    };
    // The real service pushes progress over the stream several times a second
    // while a sweep runs, so the mock does too — otherwise the bar would only
    // move on the modal's two-second poll and the demo would look stuck.
    const ticker = setInterval(() => {
      const j = discoveryView();
      if (!j) { clearInterval(ticker); return; }
      pushUpdate({ kind: 'discovery', discovery: { id: j.id, state: j.state, scanned: j.scanned, total: j.total, responders: j.responders } });
      if (j.state !== 'running') clearInterval(ticker);
    }, 200);
    return clone(discoveryJob);
  });
  on('GET', /^\/api\/discovery\/([^/]+)$/, (m) => { const j = discoveryView(); if (!j || j.id !== m[1]) throw err(404, 'no discovery run with that id'); return j; });
  on('POST', /^\/api\/discovery\/([^/]+)\/cancel$/, (m) => {
    const j = discoveryView();
    if (!j || j.id !== m[1]) throw err(404, 'no discovery run with that id');
    if (discoveryJob.state === 'running') { discoveryJob.state = 'cancelled'; discoveryJob.finishedAt = iso(Date.now()); }
    return clone(discoveryJob);
  });
  on('POST', /^\/api\/discovery\/([^/]+)\/add$/, (m, body) => {
    const j = discoveryView();
    if (!j || j.id !== m[1]) throw err(404, 'no discovery run with that id');
    window.__gwatchMockDiscoveryAdds.push(clone(body || {}));
    const items = body?.items || [];
    if (!items.length) throw err(400, 'choose at least one device to add');
    const created = []; const skipped = [];
    for (const item of items) {
      const found = (j.results || []).find((r) => r.ip === item.ip);
      if (!found) { skipped.push({ ip: item.ip, reason: "this address was not one of the run's responders" }); continue; }
      const existing = nodes.find((n) => (n.host || '').toLowerCase() === String(item.ip).toLowerCase());
      if (existing) { skipped.push({ ip: item.ip, reason: `already monitored as "${existing.name}"` }); continue; }
      const tmpl = templates.find((t) => t.id === (item.template || found.template)) || templates[0];
      const n = mkNode({ name: item.name || found.hostname || item.ip, host: item.ip, group: body.group || tmpl.node.group, tags: [], template: tmpl.id });
      n.checks = tmpl.checks.map((c, i) => mkCheck(n.id, c.type, c.name, { interval: c.intervalSeconds, timeout: c.timeoutSeconds, config: clone(c.config), enabled: c.enabled, sortOrder: i, base: c.type === 'ping' ? 3 : 40, status: 'unknown' }));
      nodes.push(n);
      created.push(nodeOut(n));
    }
    if (created.length) addEvent('discovery', { title: `Added ${created.length} node${created.length === 1 ? '' : 's'} from discovery`, detail: created.map((c) => `${c.name} (${c.host})`).join(', ') });
    return { created, skipped };
  });

  on('GET', /^\/api\/charts$/, () => clone(savedCharts));
  on('PUT', /^\/api\/charts$/, (m, body) => { savedCharts = (body || []).map((c, i) => ({ ...c, id: c.id || `chart-${Date.now()}${i}`, name: c.name || `Chart ${i + 1}`, updatedAt: iso(Date.now()) })); return clone(savedCharts); });
  on('GET', /^\/api\/automation\/meta$/, () => ({ conditions: ['down', 'recovered', 'degraded', 'warning_cleared', 'cert_warning', 'content_changed', 'affected_by_parent', 'status_change', 'any_failure', 'any_success', 'latency_over'], interpreters: ['sh', 'bash', 'powershell', 'cmd', 'python', 'node', 'custom'], defaultInterpreter: 'powershell', placeholders: [] }));
  on('GET', /^\/api\/triggers$/, (m, body, u) => { const nid = u.searchParams.get('nodeId'); return clone(nid ? triggers.filter((t) => t.nodeId === Number(nid)) : triggers); });
  on('POST', /^\/api\/triggers$/, (m, body) => { const t = { ...body, id: triggers.length ? Math.max(...triggers.map((x) => x.id)) + 1 : 1, lastRunAt: null, lastStatus: '', lastOutput: '', runCount: 0, createdAt: iso(Date.now()), updatedAt: iso(Date.now()) }; triggers.push(t); addEvent('config_changed', { title: `Trigger saved: ${t.name}` }); return clone(t); });
  on('PUT', /^\/api\/triggers\/(\d+)$/, (m, body) => { const t = triggers.find((x) => x.id === Number(m[1])); if (!t) throw err(404, 'not found'); Object.assign(t, body, { id: t.id, updatedAt: iso(Date.now()) }); return clone(t); });
  on('DELETE', /^\/api\/triggers\/(\d+)$/, (m) => { const i = triggers.findIndex((x) => x.id === Number(m[1])); if (i < 0) throw err(404, 'not found'); triggers.splice(i, 1); return { ok: true }; });
  // Notification rules (#31): two samples, one met by the Gateway outage.
  const rules = [
    { id: 1, name: 'Gateway and Plex both down', enabled: true, join: 'all', atLeast: 0,
      conditions: [{ kind: 'status', nodeId: gateway.id, status: 'down' }, { kind: 'status', nodeId: plex.id, status: 'down' }],
      actions: [{ type: 'pushover', token: 'a1b2c3', userKey: 'u1234', title: 'GWatch {{instance}}', message: '', priority: '1', timeoutSeconds: 30 }],
      cooldownMinutes: 30, notifyCleared: true, createdAt: ago(12 * DAY), updatedAt: ago(2 * DAY),
      state: { ruleId: 1, met: true, since: ago(21 * MIN), lastFiredAt: ago(21 * MIN) } },
    { id: 2, name: 'Two of three DNS servers down', enabled: true, join: 'at_least', atLeast: 2,
      conditions: [{ kind: 'status', checkId: gateway.checks[1].id, status: 'down' }, { kind: 'status', checkId: pihole.checks[1].id, status: 'down' }, { kind: 'status', nodeId: nas.id, status: 'degraded' }],
      actions: [{ type: 'ntfy', server: '', topic: 'home-dns', title: '', message: '', priority: 'high', timeoutSeconds: 30 }, { type: 'http', method: 'POST', url: 'https://hooks.example.org/dns', body: '{"text":"{{message}}"}', timeoutSeconds: 30 }],
      cooldownMinutes: 0, notifyCleared: false, createdAt: ago(5 * DAY), updatedAt: ago(5 * DAY),
      state: { ruleId: 2, met: false, since: ago(3 * DAY), lastFiredAt: ago(3 * DAY + 2 * HOUR) } },
  ];
  const checkRule = (body) => {
    if (!String(body.name || '').trim()) throw err(400, 'a name is required');
    if (!(body.conditions || []).length) throw err(400, 'add at least one condition');
    if (body.join === 'at_least' && (body.atLeast < 1 || body.atLeast > body.conditions.length)) throw err(400, `"at least" needs a count between 1 and ${body.conditions.length} (the number of conditions)`);
    if (!(body.actions || []).length) throw err(400, 'add at least one action');
  };
  on('GET', /^\/api\/rules$/, () => clone(rules));
  on('GET', /^\/api\/rules\/(\d+)$/, (m) => { const r = rules.find((x) => x.id === Number(m[1])); if (!r) throw err(404, 'not found'); return clone(r); });
  on('POST', /^\/api\/rules$/, (m, body) => { checkRule(body); const r = { ...body, id: rules.length ? Math.max(...rules.map((x) => x.id)) + 1 : 1, createdAt: iso(Date.now()), updatedAt: iso(Date.now()) }; r.state = { ruleId: r.id, met: false, since: null, lastFiredAt: null }; rules.push(r); addEvent('config_changed', { title: `Rule saved: ${r.name}` }); return clone(r); });
  on('PUT', /^\/api\/rules\/(\d+)$/, (m, body) => { const r = rules.find((x) => x.id === Number(m[1])); if (!r) throw err(404, 'not found'); checkRule(body); Object.assign(r, body, { id: r.id, state: r.state, updatedAt: iso(Date.now()) }); addEvent('config_changed', { title: `Rule saved: ${r.name}` }); return clone(r); });
  on('DELETE', /^\/api\/rules\/(\d+)$/, (m) => { const i = rules.findIndex((x) => x.id === Number(m[1])); if (i < 0) throw err(404, 'not found'); const [r] = rules.splice(i, 1); addEvent('config_changed', { title: `Rule deleted: ${r.name}` }); return { ok: true }; });
  on('POST', /^\/api\/rules\/(\d+)\/test$/, (m) => { const r = rules.find((x) => x.id === Number(m[1])); if (!r) throw err(404, 'not found'); return r.actions.map((a) => ({ ok: true, output: `${a.type}: sent (mock)`, startedAt: iso(Date.now()), durationMs: 42 })); });
  on('POST', /^\/api\/triggers\/(\d+)\/run$/, (m) => { const t = triggers.find((x) => x.id === Number(m[1])); if (!t) throw err(404, 'not found'); t.lastRunAt = iso(Date.now()); t.lastStatus = 'ok'; t.runCount++; t.lastOutput = 'mock run'; addEvent('trigger_fired', { nodeId: t.nodeId, nodeName: findNode(t.nodeId)?.name, title: `Trigger ran: ${t.name}`, detail: `${t.action.type} action on manual — mock run` }); return { ok: true, output: 'mock run', startedAt: t.lastRunAt, durationMs: 42 }; });
  on('POST', /^\/api\/actions\/test$/, (m, body) => ({ ok: true, output: `mock: would run a ${body?.action?.type} action`, startedAt: iso(Date.now()), durationMs: 12, statusCode: body?.action?.type === 'http' ? 200 : 0 }));
  on('GET', /^\/api\/endpoints$/, () => clone(endpoints));
  on('POST', /^\/api\/endpoints$/, (m, body) => { const e = { ...body, id: endpoints.length ? Math.max(...endpoints.map((x) => x.id)) + 1 : 1, slug: (body.slug || body.name || 'hook').toLowerCase().replace(/[^a-z0-9_-]+/g, '-'), lastCalledAt: null, lastStatus: '', lastOutput: '', callCount: 0, createdAt: iso(Date.now()), updatedAt: iso(Date.now()) }; if (endpoints.some((x) => x.slug === e.slug)) throw err(400, `an endpoint with the slug "${e.slug}" already exists`); endpoints.push(e); return clone(e); });
  on('PUT', /^\/api\/endpoints\/(\d+)$/, (m, body) => { const e = endpoints.find((x) => x.id === Number(m[1])); if (!e) throw err(404, 'not found'); Object.assign(e, body, { id: e.id, updatedAt: iso(Date.now()) }); return clone(e); });
  on('DELETE', /^\/api\/endpoints\/(\d+)$/, (m) => { const i = endpoints.findIndex((x) => x.id === Number(m[1])); if (i < 0) throw err(404, 'not found'); endpoints.splice(i, 1); return { ok: true }; });
  on('POST', /^\/api\/endpoints\/(\d+)\/run$/, (m) => { const e = endpoints.find((x) => x.id === Number(m[1])); if (!e) throw err(404, 'not found'); e.lastCalledAt = iso(Date.now()); e.lastStatus = 'ok'; e.callCount++; addEvent('endpoint_called', { title: `Endpoint called: ${e.name}` }); return { ok: true, output: 'mock run', startedAt: e.lastCalledAt, durationMs: 8 }; });
  on('GET', /^\/api\/update\/status$/, () => ({ status: clone(updateStatus), repo: settings.general.updateRepo || 'jxburros/GWatch', version: '0.4.1' }));
  on('POST', /^\/api\/update\/check$/, () => { updateStatus.last = { repo: 'jxburros/GWatch', currentVersion: '0.4.1', latestVersion: '0.5.0', updateAvailable: true, currentIsDev: false, releaseUrl: 'https://github.com/jxburros/GWatch/releases', releaseNotes: '- Charts tab\n- Audit tab\n- Triggers and endpoints', publishedAt: ago(2 * DAY), assetName: 'gwatch-windows-amd64.exe', assetUrl: '', assetSize: 12_000_000, checkedAt: iso(Date.now()) }; addEvent('update', { title: 'Checked for updates', detail: 'Current 0.4.1, latest 0.5.0 — update available' }); return clone(updateStatus.last); });
  on('POST', /^\/api\/update\/apply$/, () => { updateStatus.applied = true; updateStatus.restarting = true; updateStatus.lastApplyAt = iso(Date.now()); addEvent('update', { title: 'Update installed: 0.5.0' }); return { ok: true, info: updateStatus.last, restarting: true }; });
  /* ---------- Hardware ---------- */
  // Two machines: the computer "GWatch" is pretending to run on, and one that
  // reports through an agent. Readings are generated from the same clock as
  // everything else so the charts line up with the rest of the mock.
  const agents = [
    { id: 1, name: 'Living room NAS', nodeId: null, prefix: 'gwa_k3m9vq2x', enabled: true, createdBy: 'local', createdAt: ago(21 * DAY), revokedAt: null, lastSeenAt: ago(40e3), lastAddr: '192.168.1.24:52104', lastVersion: '0.5.0', hostname: 'nas.lan', os: 'linux', arch: 'arm64' },
  ];

  function mockReading(key, hostname, seed, at = Date.now()) {
    const r = rng(seed + Math.floor(at / MIN));
    const wave = (period, phase) => 0.5 + 0.5 * Math.sin((at / period) + phase);
    const totalMem = key === 'local' ? 34359738368 : 8589934592;
    const usedMem = Math.round(totalMem * (0.34 + 0.18 * wave(37 * MIN, 1.1)));
    const rootTotal = key === 'local' ? 1000204886016 : 3998639460352;
    const rootUsed = Math.round(rootTotal * (key === 'local' ? 0.41 : 0.87));
    return {
      key, hostname,
      os: key === 'local' ? 'windows' : 'linux',
      arch: key === 'local' ? 'amd64' : 'arm64',
      platform: key === 'local' ? 'Windows 11 Pro 24H2' : 'Debian GNU/Linux 12 (bookworm)',
      kernel: key === 'local' ? '26100' : '6.1.0-18-arm64',
      agentVersion: key === 'local' ? '' : '0.5.0',
      ts: iso(at),
      bootTime: ago(key === 'local' ? 6 * DAY : 63 * DAY),
      uptimeSeconds: (key === 'local' ? 6 * DAY : 63 * DAY) / 1000,
      cpu: {
        cores: key === 'local' ? 12 : 4,
        model: key === 'local' ? 'AMD Ryzen 5 7600X 6-Core Processor' : 'ARM Cortex-A72',
        usagePct: Math.round((8 + 34 * wave(23 * MIN, 0.4) + r() * 6) * 10) / 10,
        userPct: Math.round(18 * wave(23 * MIN, 0.4) * 10) / 10,
        systemPct: Math.round(6 * wave(19 * MIN, 2.2) * 10) / 10,
        ioWaitPct: key === 'local' ? 0 : Math.round(3 * r() * 10) / 10,
        load1: key === 'local' ? null : Math.round((0.4 + 1.6 * wave(29 * MIN, 0.9)) * 100) / 100,
        load5: key === 'local' ? null : 0.72,
        load15: key === 'local' ? null : 0.58,
        loadPerCore: key === 'local' ? null : Math.round((0.1 + 0.4 * wave(29 * MIN, 0.9)) * 100) / 100,
      },
      memory: {
        totalBytes: totalMem, usedBytes: usedMem, availableBytes: totalMem - usedMem,
        usedPct: Math.round((usedMem / totalMem) * 1000) / 10,
        cachedBytes: Math.round(totalMem * 0.22),
        swapTotalBytes: key === 'local' ? 4294967296 : 1073741824,
        swapUsedBytes: key === 'local' ? 402653184 : 0,
        swapUsedPct: key === 'local' ? 9.4 : 0,
      },
      filesystems: key === 'local'
        ? [{ mount: 'C:', device: 'C:', fsType: 'NTFS', totalBytes: rootTotal, usedBytes: rootUsed, freeBytes: rootTotal - rootUsed, usedPct: Math.round((rootUsed / rootTotal) * 1000) / 10 }]
        : [
          { mount: '/', device: '/dev/mmcblk0p2', fsType: 'ext4', totalBytes: 62914560000, usedBytes: 24159191040, freeBytes: 38755368960, usedPct: 38.4 },
          { mount: '/srv/media', device: '/dev/sda1', fsType: 'ext4', totalBytes: rootTotal, usedBytes: rootUsed, freeBytes: rootTotal - rootUsed, usedPct: Math.round((rootUsed / rootTotal) * 1000) / 10 },
        ],
      interfaces: [{
        name: key === 'local' ? 'Ethernet' : 'eth0', up: true, speedMbit: 1000,
        addresses: [key === 'local' ? '192.168.1.10/24' : '192.168.1.24/24'],
        rxBytes: 84 * 1024 * 1024 * 1024, txBytes: 12 * 1024 * 1024 * 1024,
        rxBytesPerSec: Math.round(40e3 + 9e6 * wave(17 * MIN, 0.2)),
        txBytesPerSec: Math.round(18e3 + 1.4e6 * wave(13 * MIN, 1.7)),
        rxErrors: 0, txErrors: 0, rxDropped: 0, txDropped: 0,
      }],
      disks: [{
        name: key === 'local' ? 'PhysicalDrive0' : 'sda',
        readBytes: 4 * 1024 * 1024 * 1024 * 1024, writeBytes: 2 * 1024 * 1024 * 1024 * 1024,
        readBytesPerSec: Math.round(120e3 + 24e6 * wave(11 * MIN, 2.4)),
        writeBytesPerSec: Math.round(90e3 + 6e6 * wave(31 * MIN, 0.7)),
        readOpsPerSec: Math.round(4 + 90 * wave(11 * MIN, 2.4)),
        writeOpsPerSec: Math.round(2 + 40 * wave(31 * MIN, 0.7)),
        busyPct: Math.round(60 * wave(11 * MIN, 2.4) * 10) / 10,
      }],
      warnings: key === 'local' ? [] : [],
    };
  }

  const hostSummaries = () => ([
    { key: 'local', name: settings.general.instanceName || 'This computer', source: 'local', status: 'up', stale: false, metrics: mockReading('local', 'studio-pc', 11) },
    { key: 'agent:1', name: agents[0].name, source: 'agent', agent: clone(agents[0]), nodeId: nas.id, nodeName: nas.name, status: 'up', stale: false, metrics: mockReading('agent:1', 'nas.lan', 29) },
  ]);

  on('GET', /^\/api\/hosts$/, () => hostSummaries());
  on('GET', /^\/api\/hosts\/([^/]+)$/, (m) => {
    const key = decodeURIComponent(m[1]);
    const found = hostSummaries().find((x) => x.key === key);
    if (!found) throw err(404, 'no such machine');
    return found;
  });
  on('GET', /^\/api\/hosts\/([^/]+)\/history$/, (m, body, u) => {
    const key = decodeURIComponent(m[1]);
    const span = { '1h': HOUR, '24h': DAY, '7d': 7 * DAY, '30d': 30 * DAY, '1y': 365 * DAY }[u.searchParams.get('range') || '24h'] || DAY;
    const points = 240;
    const step = span / points;
    const samples = [];
    for (let i = points; i >= 0; i--) {
      const at = NOW - i * step;
      const reading = mockReading(key, key === 'local' ? 'studio-pc' : 'nas.lan', key === 'local' ? 11 : 29, at);
      samples.push({
        key, ts: iso(at),
        cpuPct: reading.cpu.usagePct,
        memPct: reading.memory.usedPct,
        swapPct: reading.memory.swapUsedPct,
        diskPct: Math.max(...reading.filesystems.map((f) => f.usedPct)),
        loadPerCore: reading.cpu.loadPerCore,
        netRxBytesPerSec: reading.interfaces[0].rxBytesPerSec,
        netTxBytesPerSec: reading.interfaces[0].txBytesPerSec,
        diskReadBytesPerSec: reading.disks[0].readBytesPerSec,
        diskWriteBytesPerSec: reading.disks[0].writeBytesPerSec,
      });
    }
    return { key, range: u.searchParams.get('range') || '24h', from: iso(NOW - span), to: iso(NOW), samples };
  });
  on('GET', /^\/api\/agents$/, () => clone(agents));
  // Newer than the 0.5.0 the demo machine reports, so the "an older agent is
  // running" marks are visible in mock mode.
  on('GET', /^\/api\/agents\/latest$/, () => ({ version: '0.6.0', url: 'https://github.com/jxburros/GWatch/releases', checkedAt: iso(NOW - 3600e3) }));
  on('POST', /^\/api\/agents$/, (m, body) => {
    if (!body?.name) throw err(400, 'give the machine a name so you can recognise it later');
    const a = { id: Math.max(0, ...agents.map((x) => x.id)) + 1, name: body.name, nodeId: body.nodeId ?? null, prefix: 'gwa_mockmock', enabled: true, createdBy: 'local', createdAt: iso(Date.now()), revokedAt: null, lastSeenAt: null, lastAddr: '', lastVersion: '', hostname: '', os: '', arch: '' };
    agents.push(a);
    return { token: 'gwa_mockmocktokenqwertyuiopasdfghjklzxcvbnm23', agent: clone(a) };
  });
  on('PUT', /^\/api\/agents\/(\d+)$/, (m, body) => { const a = agents.find((x) => x.id === Number(m[1])); if (!a) throw err(404, 'not found'); Object.assign(a, { name: body.name ?? a.name, nodeId: body.nodeId ?? a.nodeId, enabled: body.enabled ?? a.enabled }); return clone(a); });
  on('DELETE', /^\/api\/agents\/(\d+)$/, (m, body, u) => {
    const i = agents.findIndex((x) => x.id === Number(m[1])); if (i < 0) throw err(404, 'not found');
    if (u.searchParams.get('purge') === '1') agents.splice(i, 1);
    else agents[i].revokedAt = iso(Date.now());
    return { ok: true };
  });

  on('GET', /^\/api\/export\/logs\.txt$/, () => ({ __csv: logLines.join('\n') }));

  const realFetch = window.fetch.bind(window);
  window.fetch = async function mockFetch(input, init = {}) {
    const url = typeof input === 'string' ? input : input.url;
    const u = new URL(url, location.href);
    if (!u.pathname.startsWith('/api/')) return realFetch(input, init);
    const method = (init.method || (typeof input !== 'string' && input.method) || 'GET').toUpperCase();
    await new Promise((res) => setTimeout(res, 60 + Math.random() * 140));
    let body = init.body;
    if (typeof body === 'string') { try { body = JSON.parse(body); } catch { /* keep string */ } }
    for (const r of routes) {
      if (r.method !== method) continue;
      const m = r.re.exec(u.pathname);
      if (!m) continue;
      try {
        const out = r.fn(m, body, u);
        if (out && out.__csv != null) return new Response(out.__csv, { status: 200, headers: { 'Content-Type': 'text/csv' } });
        return new Response(JSON.stringify(out ?? {}), { status: 200, headers: { 'Content-Type': 'application/json' } });
      } catch (e) {
        return new Response(JSON.stringify({ error: e.message }), { status: e.status || 500, headers: { 'Content-Type': 'application/json' } });
      }
    }
    return new Response(JSON.stringify({ error: `mock: no route for ${method} ${u.pathname}` }), { status: 404, headers: { 'Content-Type': 'application/json' } });
  };

  /* ---------- Event stream ---------- */
  // Every open stream, so something outside the tick loop — a discovery sweep,
  // which has news several times a second — can push to all of them.
  const liveStreams = new Set();
  function pushUpdate(data) {
    const e = new MessageEvent('update', { data: JSON.stringify(data) });
    for (const s of liveStreams) s.dispatchEvent(e);
  }

  class MockEventSource extends EventTarget {
    constructor(url) {
      super();
      liveStreams.add(this);
      this.url = url; this.readyState = 0;
      setTimeout(() => { this.readyState = 1; this.dispatchEvent(new Event('open')); if (this.onopen) this.onopen(new Event('open')); }, 50);
      this.timer = setInterval(() => this.tick(), 12000);
    }
    tick() {
      // Run every check whose next run is due; emit one update per run.
      const now = Date.now();
      let emitted = 0;
      for (const n of nodes) {
        if (!n.enabled) continue;
        for (const c of n.checks) {
          const st = states[c.id];
          if (!c.enabled || !st || !st.nextRunAt || +new Date(st.nextRunAt) > now) continue;
          if (CHECK_PROFILES[c.id].status === 'unknown') continue;
          runCheck(c, n);
          emitted++;
        }
      }
      const data = JSON.stringify({ kind: 'result', checkId: null, nodeId: null, count: emitted });
      const e = new MessageEvent('update', { data });
      this.dispatchEvent(e);
    }
    close() { clearInterval(this.timer); liveStreams.delete(this); this.readyState = 2; }
  }
  window.EventSource = MockEventSource;

  window.__gwatchMock = { nodes, events, dashboards, states, overview, history };
  console.info('[GWatch] mock API enabled (?mock=1)');

  /* ---------- Unmistakable "this is fake" banner ---------- */
  // The mock backend ships inside the release binary (it is genuinely useful
  // for support), so anyone who lands on ?mock=1 — by a stray bookmark, a
  // shared link, or poking around — needs to know at a glance that nothing
  // on the screen is their actual network. This cannot be dismissed: there
  // is no close button, and it is reinstalled on every load, on purpose.
  const MOCK_BANNER_TEXT = 'Mock data — this is not your network';
  document.title = '[MOCK] ' + document.title;

  function paintMockBanner() {
    const bar = document.createElement('div');
    bar.id = 'gwatch-mock-banner';
    bar.setAttribute('role', 'status');
    bar.textContent = MOCK_BANNER_TEXT + ' (?mock=1)';
    // Inline via the CSSOM, not a style="" attribute or a <style> block, so
    // this survives a strict Content-Security-Policy. Colours are hard-coded
    // rather than pulled from the app's CSS variables on purpose: the banner
    // must stay legible and obviously "not the app" even if the stylesheet
    // fails to load.
    bar.style.cssText = [
      'position:fixed', 'top:0', 'left:0', 'right:0', 'z-index:2147483647',
      'display:flex', 'align-items:center', 'justify-content:center', 'gap:0.5em',
      'padding:0.5em 1em', 'background:#b45309', 'color:#fff',
      'font:600 13px/1.3 system-ui, -apple-system, "Segoe UI", sans-serif',
      'letter-spacing:0.02em', 'text-align:center',
      'box-shadow:0 1px 6px rgba(0,0,0,0.4)', 'pointer-events:none',
    ].join(';');
    document.body.prepend(bar);
    // Push the app down by the banner's own height so it is never covered.
    const push = () => { document.body.style.paddingTop = bar.offsetHeight + 'px'; };
    push();
    window.addEventListener('resize', push);
  }

  if (document.body) paintMockBanner();
  else document.addEventListener('DOMContentLoaded', paintMockBanner);
})();

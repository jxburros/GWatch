// In-browser mock of the GWatch API. Loaded ONLY when the page is opened with
// ?mock=1 — it intercepts fetch() for /api/* and fakes the event stream so the
// interface can be developed and screenshot-tested without the Go service.

(function installMock() {
  const NOW = Date.now();
  const MIN = 60e3, HOUR = 3600e3, DAY = 86400e3;
  const iso = (t) => new Date(t).toISOString();
  const ago = (ms) => iso(NOW - ms);
  const ahead = (ms) => iso(NOW + ms);
  const seq = { node: 20, check: 100, result: 90000, event: 5000, dash: 5, maint: 5 };
  const clone = (o) => JSON.parse(JSON.stringify(o));

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
    return { id, name: o.name, host: o.host, group: o.group || '', tags: o.tags || [], notes: o.notes || '', importance: o.importance || 'normal', enabled: o.enabled !== false, dependsOnNodeId: o.dependsOnNodeId ?? null, template: o.template || '', createdAt: ago(40 * DAY), updatedAt: ago(2 * DAY), checks: [] };
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
  const nas = mkNode({ name: 'NAS', host: 'nas.local', group: 'Servers', tags: ['storage', 'backup-target'], importance: 'high', template: 'home-server' });
  nas.checks = [
    mkCheck(nas.id, 'ping', 'Ping', { config: { pingCount: 4, latencyWarnMs: 20 }, base: 0.7, noise: 0.4, interval: 60 }),
    mkCheck(nas.id, 'tcp', 'SSH (22)', { config: { port: 22 }, base: 1.8, noise: 0.3 }),
    mkCheck(nas.id, 'http', 'Web UI', { config: { target: 'https://nas.local:5001/', expectedStatus: '200-399', ignoreTlsErrors: true, certCheck: false }, base: 62, noise: 0.35, interval: 300 }),
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
  const pihole = mkNode({ name: 'Pi-hole', host: '192.168.1.2', group: 'Home Network', tags: ['dns', 'raspberry-pi'], importance: 'high', template: 'dns' });
  pihole.checks = [
    mkCheck(pihole.id, 'ping', 'Ping', { config: { pingCount: 4, latencyWarnMs: 30 }, base: 0.8, noise: 0.5, interval: 30 }),
    mkCheck(pihole.id, 'dns', 'Resolves via Pi-hole', { config: { target: 'www.example.org', dnsServer: '192.168.1.2', recordType: 'A' }, base: 3.2, noise: 0.5 }),
    mkCheck(pihole.id, 'http', 'Admin UI', { config: { target: 'http://192.168.1.2/admin/', expectedStatus: '200-399', certCheck: false }, base: 28, noise: 0.4, interval: 300 }),
  ];
  const backupSrv = mkNode({ name: 'Backup server', host: '192.168.1.30', group: 'Servers', tags: ['storage'], template: 'home-server', notes: 'Weekly patching on Sunday nights.' });
  backupSrv.checks = [
    mkCheck(backupSrv.id, 'ping', 'Ping', { config: { pingCount: 4 }, base: 1.1, noise: 0.5 }),
    mkCheck(backupSrv.id, 'tcp', 'SMB (445)', { config: { port: 445 }, base: 2.4, noise: 0.3 }),
  ];
  const newHost = mkNode({ name: 'Garage camera', host: '192.168.1.71', group: 'Home Network', tags: ['camera'], importance: 'low' });
  newHost.checks = [mkCheck(newHost.id, 'ping', 'Ping', { config: { pingCount: 4 }, base: 5, noise: 0.5, status: 'unknown', message: '' })];
  nodes.push(gateway, plex, nas, ha, site, weather, printer, pihole, backupSrv, newHost);

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
  function nodeInMaintenance(n) { return maintenance.some((w) => windowActive(w) && (w.nodeId ? w.nodeId === n.id : w.group ? w.group === n.group : true)); }

  /* ---------- Live state & results ---------- */
  const states = {}; // checkId -> CheckState
  const lastResults = {}; // checkId -> Result
  const silences = {};
  const resultLog = {}; // checkId -> Result[] newest first

  function certInfo(days, host) {
    const subject = host.replace(/^https?:\/\//, '').replace(/[:/].*$/, '');
    return { subject: `CN=${subject}`, issuer: "CN=R11, O=Let's Encrypt, C=US", notBefore: ago((90 - days) * DAY), notAfter: ahead(days * DAY), daysRemaining: days, dnsNames: [subject, subject.replace(/^www\./, '')], serial: '04:AB:19:F2:7C:33:9E:1D', valid: true };
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
        if (!failed) { res.minMs = Math.min(...rtts); res.maxMs = Math.max(...rtts); res.jitterMs = +(res.maxMs - res.minMs).toFixed(2); res.lossPct = 0; res.message = res.message || `${count}/${count} replies, avg ${latency.toFixed(1)} ms`; }
        else { res.lossPct = 100; res.minMs = null; }
        break;
      }
      case 'http': case 'keyword': case 'json': {
        if (!failed) {
          const dns = +(latency * 0.08).toFixed(1), connect = +(latency * 0.12).toFixed(1), tls = target.startsWith('https') ? +(latency * 0.25).toFixed(1) : 0, ttfb = +(latency * 0.85).toFixed(1);
          res.details = { statusCode: 200, finalUrl: target.startsWith('http') ? target : `https://${target}/`, redirects: check.id === site.checks[0].id ? 1 : 0, dnsMs: dns, connectMs: connect, tlsMs: tls, firstByteMs: ttfb, totalMs: +latency.toFixed(1), contentLength: 18422 + Math.floor(r() * 2000) };
          if (check.type === 'keyword') { res.details.keywordFound = true; res.message = res.message || `Keyword found · 200 in ${latency.toFixed(0)} ms`; }
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
  ev(3 * DAY + 2 * HOUR, 'service_started', { title: 'GWatch service started', detail: 'Version 0.4.1 · windows/amd64 · listening on 127.0.0.1:8080' });
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
  let settings = {
    general: { instanceName: 'Home monitor', defaultIntervalSeconds: 60, defaultTimeoutSeconds: 10, maxConcurrentChecks: 8, minIntervalSeconds: 10, wallboardRefreshSeconds: 15, latencyWarnMs: 0, packetLossWarnPct: 0, theme: 'dark', accentColor: '#43c9c0', remoteAccess: false, accessPassword: '', requireLoginLocally: false, updateRepo: 'jxburros/GWatch' },
    alerts: { enabled: true, recipients: ['jeff@example.com', 'sam@example.com'], failureThreshold: 2, cooldownMinutes: 60, notifyRecovery: true, notifyWarnings: true, certWarnDays: 14, smtp: { host: 'smtp.example.com', port: 587, username: 'gwatch@example.com', password: '********', from: 'GWatch <gwatch@example.com>', security: 'starttls' } },
    retention: { rawDays: 30, fiveMinDays: 180, hourlyDays: 730, dailyDays: 0, eventDays: 730 },
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
      databasePath: 'C:\\ProgramData\\GWatch\\gwatch.db', databaseBytes: 48_300_000, dataDir: 'C:\\ProgramData\\GWatch', retention, backup: backupStatus,
      recentErrors: events.filter((e) => e.type === 'internal_error').slice(0, 5), alertsEnabled: settings.alerts.enabled, smtpConfigured: !!settings.alerts.smtp.host, lastAlertAt: ago(21 * MIN + 30e3), lastAlertError: '', listenAddress: '127.0.0.1:8080', platform: 'windows/amd64',
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
    out.push(`${fmt(t0 + 900)} INFO  http listening on 127.0.0.1:8080`);
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
      const avg = ok ? +lat.toFixed(2) : null;
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
      const g = groupsMap.get(n.group || 'Ungrouped') || { name: n.group || 'Ungrouped', status: 'paused', up: 0, degraded: 0, down: 0, unknown: 0, paused: 0, maintenance: 0, total: 0 };
      g[status]++; g.total++; if (SEV[status] > SEV[g.status]) g.status = status; groupsMap.set(g.name, g);
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
  on('GET', /^\/api\/me$/, () => ({ kind: 'local', name: 'this computer', role: 'admin', isAdmin: true, canWrite: true, signedIn: false, theme: settings.general.theme, accentColor: settings.general.accentColor }));
  on('GET', /^\/api\/auth\/setup$/, () => ({ usersConfigured: false, loginRequired: false, accessPasswordSet: false, apiVersion: 1 }));
  on('GET', /^\/api\/users$/, () => []);
  on('GET', /^\/api\/apikeys$/, () => []);
  on('GET', /^\/api\/overview$/, () => overview());
  on('GET', /^\/api\/wallboard$/, () => wallboard());
  on('GET', /^\/api\/templates$/, () => templates);
  on('GET', /^\/api\/groups$/, () => {
    const g = new Map(); const t = new Map();
    for (const n of nodes) { if (n.group) g.set(n.group, (g.get(n.group) || 0) + 1); for (const tag of n.tags || []) t.set(tag, (t.get(tag) || 0) + 1); }
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
    Object.assign(n, { name: body.name, host: body.host, group: body.group || '', tags: body.tags || [], notes: body.notes || '', importance: body.importance || 'normal', enabled: body.enabled !== false, dependsOnNodeId: body.dependsOnNodeId ?? null, updatedAt: iso(Date.now()) });
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
  on('GET', /^\/api\/history$/, (m, body, u) => { const ids = u.searchParams.getAll('checkId'); const range = u.searchParams.get('range') || '24h'; if (ids.length === 1) return history(ids[0], range); return ids.map((id) => history(id, range)); });
  on('GET', /^\/api\/history\/multi$/, (m, body, u) => { const ids = u.searchParams.getAll('checkId'); const range = u.searchParams.get('range') || '24h'; return ids.map((id) => history(id, range)); });
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
  on('PUT', /^\/api\/settings$/, (m, body) => { settings = clone(body); if (settings.alerts?.smtp?.password) settings.alerts.smtp.password = '********'; if (settings.general?.accessPassword) settings.general.accessPassword = '********'; addEvent('config_changed', { title: 'Settings changed' }); return clone(settings); });
  on('POST', /^\/api\/settings\/test-email$/, (m, body) => { if (!settings.alerts.smtp.host) throw err(400, 'SMTP host is not configured'); return { ok: true, message: `Test email sent to ${body?.to || settings.alerts.recipients.join(', ')} via ${settings.alerts.smtp.host}` }; });
  on('GET', /^\/api\/retention\/status$/, () => retention);
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
  on('GET', /^\/api\/network$/, () => ({ listenAddress: settings.general.remoteAccess ? ':8080' : '127.0.0.1:8080', remoteAccess: !!settings.general.remoteAccess, passwordSet: !!settings.general.accessPassword, port: 8080, localUrl: 'http://127.0.0.1:8080', lanUrls: settings.general.remoteAccess ? ['http://192.168.1.10:8080', 'http://desktop-pc:8080'] : [], hostname: 'desktop-pc', restartNeeded: false }));
  on('GET', /^\/api\/charts$/, () => clone(savedCharts));
  on('PUT', /^\/api\/charts$/, (m, body) => { savedCharts = (body || []).map((c, i) => ({ ...c, id: c.id || `chart-${Date.now()}${i}`, name: c.name || `Chart ${i + 1}`, updatedAt: iso(Date.now()) })); return clone(savedCharts); });
  on('GET', /^\/api\/automation\/meta$/, () => ({ conditions: ['down', 'recovered', 'degraded', 'warning_cleared', 'cert_warning', 'content_changed', 'affected_by_parent', 'status_change', 'any_failure', 'any_success', 'latency_over'], interpreters: ['sh', 'bash', 'powershell', 'cmd', 'python', 'node', 'custom'], defaultInterpreter: 'powershell', placeholders: [] }));
  on('GET', /^\/api\/triggers$/, (m, body, u) => { const nid = u.searchParams.get('nodeId'); return clone(nid ? triggers.filter((t) => t.nodeId === Number(nid)) : triggers); });
  on('POST', /^\/api\/triggers$/, (m, body) => { const t = { ...body, id: triggers.length ? Math.max(...triggers.map((x) => x.id)) + 1 : 1, lastRunAt: null, lastStatus: '', lastOutput: '', runCount: 0, createdAt: iso(Date.now()), updatedAt: iso(Date.now()) }; triggers.push(t); addEvent('config_changed', { title: `Trigger saved: ${t.name}` }); return clone(t); });
  on('PUT', /^\/api\/triggers\/(\d+)$/, (m, body) => { const t = triggers.find((x) => x.id === Number(m[1])); if (!t) throw err(404, 'not found'); Object.assign(t, body, { id: t.id, updatedAt: iso(Date.now()) }); return clone(t); });
  on('DELETE', /^\/api\/triggers\/(\d+)$/, (m) => { const i = triggers.findIndex((x) => x.id === Number(m[1])); if (i < 0) throw err(404, 'not found'); triggers.splice(i, 1); return { ok: true }; });
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
  class MockEventSource extends EventTarget {
    constructor(url) {
      super();
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
    close() { clearInterval(this.timer); this.readyState = 2; }
  }
  window.EventSource = MockEventSource;

  window.__gwatchMock = { nodes, events, dashboards, states, overview, history };
  console.info('[GWatch] mock API enabled (?mock=1)');
})();

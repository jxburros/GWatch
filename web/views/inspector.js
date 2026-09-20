// Result inspector: renders one model.Result with all the diagnostics the
// backend can provide (timings, redirects, certificate, ping packets, DNS…).

import { h, icon, statusPill, checkTypeLabel } from '../components.js';
import { ms as fmtMs, pct, dateTime, dateShort, relTime, plural, bytes, duration, num } from '../fmt.js';

function kv(pairs) {
  const dl = h('dl', { class: 'kv' });
  for (const [k, v, opts = {}] of pairs) {
    if (v == null || v === '') continue;
    dl.append(h('dt', null, k), h('dd', { class: opts.mono ? 'mono' : '' }, v));
  }
  return dl;
}

// sumRate totals a per-second rate across rows that reported one, returning
// null when none did — which is not the same as a rate of zero.
function sumRate(rows, key) {
  let total = null;
  for (const r of rows || []) {
    if (r[key] != null) total = (total || 0) + r[key];
  }
  return total;
}

function yesNo(v, yes = 'Yes', no = 'No') {
  if (v == null) return null;
  return v ? h('span', { class: 'status-glyph text-up' }, icon('check'), yes) : h('span', { class: 'status-glyph text-down' }, icon('x'), no);
}

export function timingBar(d) {
  const dns = d.dnsMs || 0, connect = d.connectMs || 0, tls = d.tlsMs || 0;
  const ttfb = d.firstByteMs != null ? Math.max(0, d.firstByteMs - dns - connect - tls) : 0;
  const total = d.totalMs != null ? d.totalMs : dns + connect + tls + ttfb;
  const download = Math.max(0, total - dns - connect - tls - ttfb);
  const parts = [['dns', 'DNS', dns, '#6ea0ff'], ['connect', 'Connect', connect, '#3ec8b8'], ['tls', 'TLS', tls, 'var(--accent)'], ['ttfb', 'First byte', ttfb, '#e879a6'], ['download', 'Download', download, '#ff9f6e']];
  const sum = parts.reduce((a, p) => a + p[2], 0) || 1;
  const bar = h('div', { class: 'timing-bar', role: 'img', 'aria-label': `Timing breakdown, total ${fmtMs(total)}` });
  for (const [cls, , v] of parts) if (v > 0) bar.append(h('span', { class: `seg ${cls}`, style: { width: `${(v / sum) * 100}%` } }));
  const legend = h('div', { class: 'timing-legend' });
  for (const [, label, v, color] of parts) if (v > 0 || label === 'First byte') legend.append(h('span', { class: 'k', style: { '--sw': color } }, `${label} `, h('span', { class: 'v' }, fmtMs(v))));
  legend.append(h('span', { class: 'k', style: { '--sw': 'var(--text)' } }, 'Total ', h('span', { class: 'v' }, fmtMs(total))));
  return h('div', null, bar, legend);
}

/**
 * snmpValueText renders one reading with its unit. Large per-second rates are
 * abbreviated the way a person would say them (1.4 Mbit/s, not 1400000).
 */
export function snmpValueText(v, unit) {
  if (v == null) return null;
  const round = (x) => num(Math.round(x * 100) / 100);
  const abs = Math.abs(v);
  if (unit && abs >= 1000) {
    for (const [factor, prefix] of [[1e9, 'G'], [1e6, 'M'], [1e3, 'k']]) {
      if (abs >= factor) return `${round(v / factor)} ${prefix}${unit}`;
    }
  }
  return unit ? `${round(v)} ${unit}` : round(v);
}

/**
 * snmpVerdict works out whether one reading crossed a threshold, so the table
 * can say so per row rather than only in the result's message. The comparisons
 * match internal/checks.judgeSNMP: strict, critical first.
 */
export function snmpVerdict(value, cfg) {
  if (value == null || !cfg) return { label: 'Not thresholded', cls: 'dim' };
  const n = (k) => (cfg[k] == null ? null : Number(cfg[k]));
  const [wa, ca, wb, cb] = ['warnAbove', 'critAbove', 'warnBelow', 'critBelow'].map(n);
  if (ca != null && value > ca) return { label: `Critical — above ${ca}`, cls: 'text-down' };
  if (cb != null && value < cb) return { label: `Critical — below ${cb}`, cls: 'text-down' };
  if (wa != null && value > wa) return { label: `Warning — above ${wa}`, cls: 'text-degraded' };
  if (wb != null && value < wb) return { label: `Warning — below ${wb}`, cls: 'text-degraded' };
  if (wa == null && ca == null && wb == null && cb == null) return { label: 'No thresholds set', cls: 'dim' };
  return { label: 'Within thresholds', cls: 'text-up' };
}

function snmpTable(rows, check, result) {
  const byName = new Map((check?.config?.snmpOids || []).map((o) => [o.name, o]));
  const table = h('table', { class: 'table' }, h('thead', null, h('tr', null,
    h('th', null, 'Reading'), h('th', null, 'OID'), h('th', { class: 'num' }, 'Value'), h('th', { class: 'num' }, 'Rate'), h('th', null, 'Verdict'))));
  const tb = h('tbody');
  let awaitingSecondSample = false;
  for (const r of rows) {
    const cfg = byName.get(r.name);
    const measured = r.value != null ? r.value : r.rate;
    const verdict = r.value == null && r.rate == null
      ? { label: cfg?.kind === 'counter' ? 'Waiting for a second sample' : 'Reported as text', cls: 'dim' }
      : snmpVerdict(measured, cfg);
    if (cfg?.kind === 'counter' && r.rate == null) awaitingSecondSample = true;
    tb.append(h('tr', null,
      h('td', null, r.name),
      h('td', { class: 'mono dim' }, r.oid),
      h('td', { class: 'num mono' }, r.value != null ? snmpValueText(r.value, r.unit) : (r.rate != null ? '—' : h('span', { class: 'muted' }, r.raw || '—'))),
      h('td', { class: 'num mono' }, r.rate != null ? snmpValueText(r.rate, r.unit) : '—'),
      h('td', { class: verdict.cls }, verdict.label)));
  }
  table.append(tb);
  return h('div', null,
    h('div', { class: 'section-title' }, 'SNMP readings'),
    h('div', { class: 'table-wrap' }, table),
    awaitingSecondSample && result?.success
      ? h('p', { class: 'note' }, 'A counter has no rate until a second sample exists to compare it against, so the first run after GWatch starts leaves those rows blank.')
      : null);
}

export function certBlock(cert) {
  if (!cert) return null;
  const dr = cert.daysRemaining;
  const drCls = dr <= 7 ? 'text-down' : dr <= 30 ? 'text-degraded' : 'text-up';
  return h('div', null,
    h('div', { class: 'section-title' }, 'Certificate'),
    kv([
      ['Valid', cert.valid ? yesNo(true, 'Valid') : h('span', { class: 'status-glyph text-down' }, icon('x'), cert.error || 'Invalid')],
      ['Subject', cert.subject, { mono: true }],
      ['Issuer', cert.issuer],
      ['Expires', h('span', null, dateShort(cert.notAfter), ' ', h('b', { class: drCls }, `(${plural(dr, 'day')} remaining)`))],
      ['Valid from', dateShort(cert.notBefore)],
      ['Names', cert.dnsNames?.length ? cert.dnsNames.join(', ') : null, { mono: true }],
      ['Serial', cert.serial, { mono: true }],
    ]),
  );
}

/**
 * Render a full inspector for a result.
 * @param {object} result model.Result
 * @param {object} check model.Check (for type-specific labels)
 */
export function resultInspector(result, check, { compact = false } = {}) {
  if (!result) return h('div', { class: 'inspector' }, h('p', { class: 'muted' }, 'No result yet. Run the check to see details here.'));
  const d = result.details || {};
  const type = check?.type || '';
  const wrap = h('div', { class: 'inspector' });

  wrap.append(h('div', { class: 'inspector-head' },
    statusPill(result.status || (result.success ? 'up' : 'down')),
    h('strong', null, result.message || (result.success ? 'Succeeded' : 'Failed')),
    h('span', { class: 'when' }, `${dateTime(result.ts)} (${relTime(result.ts)})`),
    result.attempts > 1 ? h('span', { class: 'tag' }, `${result.attempts} attempts`) : null,
  ));
  if (result.error) wrap.append(h('div', { class: 'inspector-error' }, h('strong', null, 'Error: '), result.error));
  if (result.warnings?.length) wrap.append(h('div', { class: 'banner banner-warn' }, icon('alert'), h('div', null, result.warnings.join(' · '))));

  const left = [];
  const right = [];

  // Core metrics
  const metrics = [];
  if (result.latencyMs != null) metrics.push([type === 'ping' ? 'Average RTT' : type === 'dns' ? 'Resolve time' : type === 'tcp' ? 'Connect time' : 'Response time', fmtMs(result.latencyMs), { mono: true }]);
  if (result.minMs != null) metrics.push(['Min', fmtMs(result.minMs), { mono: true }]);
  if (result.maxMs != null) metrics.push(['Max', fmtMs(result.maxMs), { mono: true }]);
  if (result.jitterMs != null) metrics.push(['Jitter', fmtMs(result.jitterMs), { mono: true }]);
  if (result.lossPct != null) metrics.push(['Packet loss', pct(result.lossPct), { mono: true }]);

  // HTTP family
  const isHttp = ['http', 'keyword', 'json'].includes(type) || d.statusCode || d.finalUrl;
  if (isHttp) {
    const http = [
      ['Status code', d.statusCode ? h('span', null, h('b', { class: d.unexpectedCode ? 'text-down' : '' }, String(d.statusCode)), d.unexpectedCode ? h('span', { class: 'muted' }, ' — unexpected response code') : null) : null],
      ['Final URL', d.finalUrl ? h('a', { href: d.finalUrl, target: '_blank', rel: 'noopener' }, d.finalUrl) : null, { mono: true }],
      ['Redirects', d.redirects != null ? String(d.redirects) : null],
      ['Content length', d.contentLength ? `${d.contentLength.toLocaleString()} bytes` : null],
    ];
    if (d.keywordFound != null) http.push(['Keyword', yesNo(d.keywordFound, 'Found', 'Not found')]);
    if (d.jsonValue || d.jsonMatched != null) http.push(['JSON value', h('span', null, h('code', null, d.jsonValue ?? '(missing)'), ' ', d.jsonMatched != null ? yesNo(d.jsonMatched, 'matches', 'does not match') : null)]);
    if (d.contentChanged != null && (check?.config?.contentWatch || d.contentHash || d.contentValue)) {
      http.push(['Content watch', d.contentChanged ? h('span', { class: 'status-glyph text-degraded' }, icon('eye'), 'Response changed since last run') : h('span', { class: 'status-glyph text-up' }, icon('check'), 'Unchanged')]);
      if (d.contentValue) http.push(['Watched value', d.contentValue, { mono: true }]);
      if (d.contentHash) http.push(['Content hash', d.contentHash.slice(0, 16) + '…', { mono: true }]);
    }
    left.push(h('div', null, h('div', { class: 'section-title' }, 'Response'), kv([...metrics, ...http])));
    if (d.dnsMs != null || d.connectMs != null || d.totalMs != null || d.firstByteMs != null) {
      right.push(h('div', null, h('div', { class: 'section-title' }, 'Timing breakdown'), timingBar(d)));
    }
  } else if (metrics.length) {
    left.push(h('div', null, h('div', { class: 'section-title' }, 'Measurements'), kv(metrics)));
  }

  // Ping
  if (type === 'ping' || d.packetsSent) {
    const chips = h('div', { class: 'rtt-chips' });
    (d.rtts || []).forEach((r, i) => chips.append(h('span', { title: `Packet ${i + 1}` }, fmtMs(r))));
    const lost = (d.packetsSent || 0) - (d.packetsReceived || 0);
    for (let i = 0; i < lost; i++) chips.append(h('span', { class: 'lost' }, 'lost'));
    right.push(h('div', null, h('div', { class: 'section-title' }, 'Packets'),
      kv([['Sent / received', `${d.packetsSent ?? 0} / ${d.packetsReceived ?? 0}`, { mono: true }]]),
      d.rtts?.length || lost ? h('div', { style: { marginTop: '10px' } }, chips) : null));
  }

  // DNS
  if (type === 'dns' || d.resolvedValues) {
    right.push(h('div', null, h('div', { class: 'section-title' }, 'Resolution'), kv([
      ['Resolved to', d.resolvedValues?.length ? h('span', null, d.resolvedValues.map((v) => h('code', { style: { marginRight: '8px' } }, v))) : h('span', { class: 'muted' }, 'nothing')],
      ['Matches expected', d.expectedMatch != null ? yesNo(d.expectedMatch) : null],
      ['Resolver', d.resolver || (check?.config?.dnsServer ? check.config.dnsServer : 'system default'), { mono: true }],
      ['Record type', check?.config?.recordType || (type === 'dns' ? 'A / AAAA' : null)],
    ])));
  }

  // TCP
  if (type === 'tcp' || d.remoteAddr) {
    right.push(h('div', null, h('div', { class: 'section-title' }, 'Connection'), kv([['Remote address', d.remoteAddr, { mono: true }], ['Port', check?.config?.port ? String(check.config.port) : null, { mono: true }]])));
  }

  // Certificate
  if (d.cert) right.push(certBlock(d.cert));

  // Hardware health: the reading the check evaluated, so the inspector shows
  // what the machine looked like at the moment the check passed or failed.
  if (d.host) {
    const m = d.host;
    const rate = (v) => (v == null ? null : `${bytes(v)}/s`);
    const rx = sumRate(m.interfaces, 'rxBytesPerSec');
    const tx = sumRate(m.interfaces, 'txBytesPerSec');
    left.push(h('div', null, h('div', { class: 'section-title' }, 'Machine'), kv([
      ['Name', m.hostname],
      ['System', [m.platform, m.kernel].filter(Boolean).join(' · ')],
      ['Architecture', [m.os, m.arch].filter(Boolean).join('/')],
      ['Up for', m.uptimeSeconds ? duration(m.uptimeSeconds) : null],
      ['Reading taken', m.ts ? `${dateTime(m.ts)} (${relTime(m.ts)})` : null],
      ['Age when checked', d.hostAgeSeconds != null ? duration(d.hostAgeSeconds) : null],
      ['Agent version', m.agentVersion],
    ])));
    right.push(h('div', null, h('div', { class: 'section-title' }, 'Readings'), kv([
      ['Processor', m.cpu?.usagePct != null ? `${pct(m.cpu.usagePct, 0)} of ${num(m.cpu.cores)} cores` : null, { mono: true }],
      ['Load (1/5/15)', m.cpu?.load1 != null ? `${m.cpu.load1.toFixed(2)} / ${m.cpu.load5.toFixed(2)} / ${m.cpu.load15.toFixed(2)}` : null, { mono: true }],
      ['Memory', m.memory?.totalBytes ? `${pct(m.memory.usedPct, 0)} — ${bytes(m.memory.usedBytes)} of ${bytes(m.memory.totalBytes)}` : null, { mono: true }],
      ['Swap', m.memory?.swapTotalBytes ? `${pct(m.memory.swapUsedPct, 0)} — ${bytes(m.memory.swapUsedBytes)} of ${bytes(m.memory.swapTotalBytes)}` : null, { mono: true }],
      ['Network', rx != null || tx != null ? `\u2193 ${rate(rx) || '\u2014'}  \u2191 ${rate(tx) || '\u2014'}` : null, { mono: true }],
    ])));
    if (m.filesystems?.length) {
      right.push(h('div', null, h('div', { class: 'section-title' }, 'Disks'),
        kv(m.filesystems.map((fs) => [fs.mount, `${pct(fs.usedPct, 0)} used — ${bytes(fs.freeBytes)} free of ${bytes(fs.totalBytes)}`, { mono: true }]))));
    }
    if (m.warnings?.length) {
      // A gap in the reading is not a hardware problem, but hiding it would
      // leave the reader wondering why a figure is missing.
      left.push(h('div', null, h('div', { class: 'section-title' }, 'Not available on this machine'),
        h('ul', { class: 'note', style: { margin: 0, paddingLeft: '18px' } }, m.warnings.map((w) => h('li', null, w)))));
    }
  }

  // SNMP: every OID the run asked for, with what came back and whether it
  // crossed one of its thresholds. A reading with no number is text the
  // device reported (a description, a name) and is shown as it arrived.
  if (d.snmp?.length) left.push(snmpTable(d.snmp, check, result));

  // Custom script output
  if (d.output) left.push(h('div', null, h('div', { class: 'section-title' }, 'Output'),
    h('pre', { class: 'log-box', style: { maxHeight: '260px', whiteSpace: 'pre-wrap', wordBreak: 'break-word' } }, d.output)));

  const grid = h('div', { class: 'inspector-grid' }, h('div', { class: 'stack' }, left), h('div', { class: 'stack' }, right));
  if (!left.length && !right.length) grid.append(h('p', { class: 'muted' }, 'No further details were recorded for this result.'));
  wrap.append(grid);
  if (!compact) wrap.append(h('div', { class: 'tiny dim' }, `${checkTypeLabel(type)} check · result #${result.id ?? '—'}`));
  return wrap;
}

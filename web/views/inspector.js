// Result inspector: renders one model.Result with all the diagnostics the
// backend can provide (timings, redirects, certificate, ping packets, DNS…).

import { h, icon, statusPill, checkTypeLabel } from '../components.js';
import { ms as fmtMs, pct, dateTime, dateShort, relTime, plural } from '../fmt.js';

function kv(pairs) {
  const dl = h('dl', { class: 'kv' });
  for (const [k, v, opts = {}] of pairs) {
    if (v == null || v === '') continue;
    dl.append(h('dt', null, k), h('dd', { class: opts.mono ? 'mono' : '' }, v));
  }
  return dl;
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

  // Custom script output
  if (d.output) left.push(h('div', null, h('div', { class: 'section-title' }, 'Output'),
    h('pre', { class: 'log-box', style: { maxHeight: '260px', whiteSpace: 'pre-wrap', wordBreak: 'break-word' } }, d.output)));

  const grid = h('div', { class: 'inspector-grid' }, h('div', { class: 'stack' }, left), h('div', { class: 'stack' }, right));
  if (!left.length && !right.length) grid.append(h('p', { class: 'muted' }, 'No further details were recorded for this result.'));
  wrap.append(grid);
  if (!compact) wrap.append(h('div', { class: 'tiny dim' }, `${checkTypeLabel(type)} check · result #${result.id ?? '—'}`));
  return wrap;
}

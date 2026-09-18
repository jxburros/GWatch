// Hardware: the health of the computer GWatch runs on and of every machine
// reporting to it. The list answers "is anything struggling"; the detail page
// answers "what exactly, and since when".

import { api, subscribeUpdates } from '../api.js';
import {
  h, icon, clear, replace, statusPill, emptyState, skeleton, rangeChips, relTimeEl,
} from '../components.js';
import { LineChart, SERIES_COLORS } from '../charts.js';
import { bytes, pct, duration, dateTime, relTime, num } from '../fmt.js';

// Shading for a usage bar. These are display thresholds only — what actually
// raises an alert is the hardware check's own configuration, which the person
// running GWatch chose.
const WARN_AT = 80;
const CRIT_AT = 92;

export async function mount(root, ctx) {
  return ctx.params.key ? mountDetail(root, ctx) : mountList(root, ctx);
}

/* ---------------- The list ---------------- */

async function mountList(root, ctx) {
  const state = { hosts: [], destroyed: false };
  const listEl = h('div', null, skeleton({ lines: 4 }));
  root.append(listEl);

  ctx.setTitle('Hardware', {
    subtitle: 'Processor, memory, disk and throughput for this computer and every machine reporting in',
    actions: [h('a', { class: 'btn', href: '#/settings/hardware' }, icon('cpu'), 'Manage machines')],
  });

  async function load() {
    try {
      const hosts = await api.get('/api/hosts');
      if (state.destroyed) return;
      state.hosts = hosts || [];
      render();
    } catch (e) {
      replace(listEl, h('div', { class: 'card' }, emptyState({ icon: 'alert', title: 'Could not load hardware readings', text: e.message })));
    }
  }

  function render() {
    if (!state.hosts.length) {
      replace(listEl, h('div', { class: 'card' }, emptyState({
        icon: 'cpu',
        title: 'No hardware readings yet',
        text: 'GWatch reads this computer by itself; the first reading appears within a minute of starting. To add another machine, register it under Settings › Hardware and install the agent on it.',
        actions: [h('a', { class: 'btn btn-primary', href: '#/settings/hardware' }, 'Register a machine')],
      })));
      return;
    }
    replace(listEl, h('div', { class: 'host-grid' }, ...state.hosts.map(hostCard)));
  }

  await load();
  const unsub = subscribeUpdates((u) => { if (!u || u.kind === 'host') load(); });
  return {
    destroy() { state.destroyed = true; unsub(); },
    themeChanged() { /* the list draws no canvas */ },
  };
}

function hostCard(host) {
  const m = host.metrics;
  const head = h('div', { class: 'row-between' },
    h('div', { class: 'row', style: { gap: '8px', minWidth: 0 } },
      icon('cpu'),
      h('a', { class: 'host-name', href: `#/hardware/${encodeURIComponent(host.key)}` }, host.name || host.key),
    ),
    statusPill(host.status, { label: statusWording(host) }),
  );

  const body = h('div', { class: 'stack-sm', style: { marginTop: '12px' } });
  if (!m) {
    body.append(h('p', { class: 'note' }, host.source === 'agent'
      ? 'This machine has not reported yet. Install gwatch-agent on it and give it the token you were shown.'
      : 'No reading yet.'));
  } else {
    body.append(
      usageMeter('Processor', cpuValue(m), { detail: cpuDetail(m) }),
      usageMeter('Memory', m.memory?.totalBytes ? m.memory.usedPct : null, {
        detail: m.memory?.totalBytes ? `${bytes(m.memory.usedBytes)} of ${bytes(m.memory.totalBytes)}` : '',
      }),
      ...topFilesystems(m, 2).map((fs) => usageMeter(`Disk ${fs.mount}`, fs.usedPct, {
        detail: `${bytes(fs.freeBytes)} free of ${bytes(fs.totalBytes)}`,
      })),
      h('div', { class: 'host-footnote' },
        h('span', null, throughputSummary(m)),
        h('span', { class: 'muted' }, host.stale ? `last reading ${relTime(m.ts)}` : upFor(m)),
      ),
    );
  }
  return h('article', { class: `card host-card${host.stale ? ' is-stale' : ''}` }, head, body);
}

// statusWording says what the status means for a machine, which is not the
// same thing it means for a check: here it is only about whether readings are
// still arriving.
function statusWording(host) {
  if (host.status === 'paused') return 'not accepted';
  if (host.stale) return 'not reporting';
  if (host.status === 'unknown') return 'waiting';
  return 'reporting';
}

/** Processor load as a percentage, falling back to load per core where a
 *  platform cannot report utilisation (macOS). */
function cpuValue(m) {
  if (m.cpu?.usagePct != null) return m.cpu.usagePct;
  if (m.cpu?.loadPerCore != null) return Math.min(100, m.cpu.loadPerCore * 100);
  return null;
}

function cpuDetail(m) {
  if (m.cpu?.usagePct != null) {
    return m.cpu.cores ? `${m.cpu.cores} cores` : '';
  }
  if (m.cpu?.loadPerCore != null) return `load ${m.cpu.load1?.toFixed(2)} across ${m.cpu.cores} cores`;
  return 'not reported';
}

function topFilesystems(m, limit) {
  return (m.filesystems || [])
    .filter((fs) => fs.totalBytes > 0)
    .slice()
    .sort((a, b) => b.usedPct - a.usedPct)
    .slice(0, limit);
}

function throughputSummary(m) {
  const rx = sumRate(m.interfaces, 'rxBytesPerSec');
  const tx = sumRate(m.interfaces, 'txBytesPerSec');
  if (rx == null && tx == null) return '';
  return `↓ ${rate(rx)}  ↑ ${rate(tx)}`;
}

function sumRate(rows, key) {
  let total = null;
  for (const r of rows || []) {
    if (r[key] != null) total = (total || 0) + r[key];
  }
  return total;
}

function rate(bytesPerSec) {
  return bytesPerSec == null ? '—' : `${bytes(bytesPerSec)}/s`;
}

function upFor(m) {
  return m.uptimeSeconds ? `up ${duration(m.uptimeSeconds)}` : '';
}

/** A labelled usage bar. Shading is by the display thresholds above. */
function usageMeter(label, value, { detail = '' } = {}) {
  const known = value != null && !isNaN(value);
  const level = !known ? '' : value >= CRIT_AT ? ' crit' : value >= WARN_AT ? ' warn' : '';
  return h('div', { class: `meter${level}` },
    h('div', { class: 'meter-head' },
      h('span', { class: 'meter-label' }, label),
      h('span', { class: 'meter-value' }, known ? pct(value, 0) : '—'),
    ),
    h('div', {
      class: 'meter-track', role: 'img',
      'aria-label': `${label}: ${known ? pct(value, 0) : 'not reported'}`,
    }, h('div', { class: 'meter-fill', style: { width: `${known ? Math.max(0, Math.min(100, value)) : 0}%` } })),
    detail ? h('div', { class: 'meter-detail muted' }, detail) : null,
  );
}

/* ---------------- One machine ---------------- */

async function mountDetail(root, ctx) {
  const key = ctx.params.key;
  const state = { host: null, range: '24h', charts: [], destroyed: false };

  const headEl = h('section', { class: 'card' }, skeleton({ lines: 3 }));
  const snapEl = h('section', { class: 'stack' });
  const chartsEl = h('section', { class: 'card', 'aria-label': 'History' });
  root.append(h('div', { class: 'stack' }, headEl, snapEl, chartsEl));

  async function load() {
    try {
      state.host = await api.get(`/api/hosts/${encodeURIComponent(key)}`);
      if (state.destroyed) return;
      renderHead();
      renderSnapshot();
    } catch (e) {
      replace(headEl, emptyState({
        icon: 'alert', title: 'Could not load this machine', text: e.message,
        actions: [h('a', { class: 'btn', href: '#/hardware' }, 'Back to hardware')],
      }));
    }
  }

  function renderHead() {
    const host = state.host;
    const m = host.metrics;
    ctx.setTitle(host.name || key, {
      subtitle: m ? [m.platform, m.arch, m.hostname].filter(Boolean).join(' · ') : 'No reading yet',
      actions: [h('a', { class: 'btn', href: '#/hardware' }, icon('layers'), 'All machines')],
    });
    const rows = [
      ['Status', statusPill(host.status, { label: statusWording(host) })],
      ['Source', sourceWording(host)],
      m?.ts ? ['Last reading', relTimeEl(m.ts)] : null,
      m?.bootTime ? ['Started', `${dateTime(m.bootTime)} (up ${duration(m.uptimeSeconds)})`] : null,
      m?.cpu?.model ? ['Processor', `${m.cpu.model} — ${num(m.cpu.cores)} cores`] : null,
      host.agent?.lastVersion ? ['Agent version', host.agent.lastVersion] : null,
      host.agent?.lastAddr ? ['Reported from', host.agent.lastAddr] : null,
      host.nodeName ? ['Node', h('a', { href: `#/nodes/${host.nodeId}` }, host.nodeName)] : null,
    ].filter(Boolean);

    const dl = h('dl', { class: 'kv' });
    for (const [k, v] of rows) dl.append(h('dt', null, k), h('dd', null, v));
    replace(headEl, dl);

    if (host.warnings?.length) {
      // The collector could not read something. Say so rather than showing a
      // gap the reader has to guess at.
      headEl.append(h('div', { class: 'banner banner-info', style: { marginTop: '12px' } },
        icon('info'),
        h('div', null,
          h('strong', null, 'Some readings are not available on this machine'),
          h('ul', { class: 'note', style: { margin: '4px 0 0', paddingLeft: '18px' } },
            ...host.warnings.map((w) => h('li', null, w))),
        )));
    }
  }

  function renderSnapshot() {
    const m = state.host.metrics;
    clear(snapEl);
    if (!m) {
      snapEl.append(h('div', { class: 'card' }, emptyState({
        icon: 'cpu', title: 'No reading yet',
        text: 'Install gwatch-agent on this machine and give it the token you were shown when you registered it.',
      })));
      return;
    }

    snapEl.append(h('div', { class: 'card' },
      h('h3', null, 'Now'),
      h('div', { class: 'host-meters' },
        usageMeter('Processor', cpuValue(m), { detail: cpuDetail(m) }),
        usageMeter('Memory', m.memory?.totalBytes ? m.memory.usedPct : null, {
          detail: m.memory?.totalBytes ? `${bytes(m.memory.usedBytes)} used, ${bytes(m.memory.availableBytes)} available` : '',
        }),
        m.memory?.swapTotalBytes
          ? usageMeter('Swap', m.memory.swapUsedPct, { detail: `${bytes(m.memory.swapUsedBytes)} of ${bytes(m.memory.swapTotalBytes)}` })
          : null,
      ),
    ));

    if (m.filesystems?.length) {
      snapEl.append(h('div', { class: 'card' },
        h('h3', null, 'Disks'),
        table(['Mount', 'Device', 'Type', 'Used', 'Free', 'Size', ''],
          m.filesystems.map((fs) => [
            fs.mount,
            h('span', { class: 'mono' }, fs.device || '—'),
            fs.fsType || '—',
            pct(fs.usedPct, 0),
            bytes(fs.freeBytes),
            bytes(fs.totalBytes),
            h('div', { class: 'meter meter-inline' + meterLevel(fs.usedPct) },
              h('div', { class: 'meter-track' }, h('div', { class: 'meter-fill', style: { width: `${fs.usedPct}%` } }))),
          ])),
      ));
    }

    if (m.interfaces?.length) {
      snapEl.append(h('div', { class: 'card' },
        h('h3', null, 'Network'),
        table(['Interface', 'Link', 'Download', 'Upload', 'Received', 'Sent', 'Errors'],
          m.interfaces.map((n) => [
            n.name,
            n.up ? (n.speedMbit ? `${num(n.speedMbit)} Mbit/s` : 'up') : h('span', { class: 'muted' }, 'down'),
            rate(n.rxBytesPerSec),
            rate(n.txBytesPerSec),
            bytes(n.rxBytes),
            bytes(n.txBytes),
            n.rxErrors || n.txErrors || n.rxDropped || n.txDropped
              ? `${num((n.rxErrors || 0) + (n.txErrors || 0))} err, ${num((n.rxDropped || 0) + (n.txDropped || 0))} dropped`
              : h('span', { class: 'muted' }, 'none'),
          ])),
      ));
    }

    if (m.disks?.length) {
      snapEl.append(h('div', { class: 'card' },
        h('h3', null, 'Disk throughput'),
        table(['Device', 'Read', 'Write', 'Reads/s', 'Writes/s', 'Busy'],
          m.disks.map((d) => [
            d.name,
            rate(d.readBytesPerSec),
            rate(d.writeBytesPerSec),
            d.readOpsPerSec == null ? '—' : num(Math.round(d.readOpsPerSec)),
            d.writeOpsPerSec == null ? '—' : num(Math.round(d.writeOpsPerSec)),
            d.busyPct == null ? '—' : pct(d.busyPct, 0),
          ])),
      ));
    }
  }

  function meterLevel(v) {
    return v >= CRIT_AT ? ' crit' : v >= WARN_AT ? ' warn' : '';
  }

  function table(headers, rows) {
    return h('div', { class: 'table-wrap' },
      h('table', { class: 'table' },
        h('thead', null, h('tr', null, ...headers.map((t) => h('th', null, t)))),
        h('tbody', null, ...rows.map((cells) => h('tr', null, ...cells.map((c) => h('td', { class: typeof c === 'string' ? 'mono' : null }, c))))),
      ));
  }

  /* ---- history ---- */

  function clearCharts() {
    for (const c of state.charts) c.destroy?.();
    state.charts = [];
  }

  async function loadHistory() {
    clearCharts();
    clear(chartsEl);
    chartsEl.append(h('div', { class: 'card-head' },
      h('h3', null, 'History'),
      rangeChips(state.range, (r) => { state.range = r; loadHistory(); }),
    ));
    const body = h('div', { class: 'stack' }, skeleton({ lines: 4 }));
    chartsEl.append(body);

    let data;
    try {
      data = await api.get(`/api/hosts/${encodeURIComponent(key)}/history?range=${state.range}`);
    } catch (e) {
      replace(body, h('p', { class: 'note' }, `Could not load history: ${e.message}`));
      return;
    }
    if (state.destroyed) return;
    const samples = data.samples || [];
    if (!samples.length) {
      replace(body, h('p', { class: 'note' }, 'No readings in this range yet.'));
      return;
    }
    clear(body);

    const from = +new Date(data.from);
    const to = +new Date(data.to);
    const series = (name, key_, color) => ({
      name, color,
      points: samples.map((s) => ({ t: +new Date(s.ts), v: s[key_] == null ? null : Number(s[key_]) })),
    });
    const hasAny = (key_) => samples.some((s) => s[key_] != null);

    addChart(body, 'Processor and memory', { unit: '%', yMin: 0, yMax: 100, height: 220 }, [
      hasAny('cpuPct') ? series('Processor', 'cpuPct', SERIES_COLORS[0]) : null,
      hasAny('memPct') ? series('Memory', 'memPct', SERIES_COLORS[1]) : null,
      hasAny('swapPct') ? series('Swap', 'swapPct', SERIES_COLORS[2]) : null,
      hasAny('diskPct') ? series('Fullest disk', 'diskPct', SERIES_COLORS[3]) : null,
    ].filter(Boolean), from, to);

    if (hasAny('netRxBytesPerSec') || hasAny('netTxBytesPerSec')) {
      addChart(body, 'Network throughput', { unit: 'B/s', yMin: 0, height: 200 }, [
        series('Download', 'netRxBytesPerSec', SERIES_COLORS[0]),
        series('Upload', 'netTxBytesPerSec', SERIES_COLORS[4]),
      ], from, to);
    }
    if (hasAny('diskReadBytesPerSec') || hasAny('diskWriteBytesPerSec')) {
      addChart(body, 'Disk throughput', { unit: 'B/s', yMin: 0, height: 200 }, [
        series('Read', 'diskReadBytesPerSec', SERIES_COLORS[5]),
        series('Write', 'diskWriteBytesPerSec', SERIES_COLORS[2]),
      ], from, to);
    }
  }

  function addChart(parent, title, opts, series, from, to) {
    const host = h('div');
    const chart = new LineChart(host, { legend: true, alwaysLegend: true, ariaLabel: title, title, ...opts });
    state.charts.push(chart);
    chart.setData({ series, from, to });
    parent.append(h('div', null, h('div', { class: 'section-title' }, title), host));
  }

  await load();
  await loadHistory();
  const unsub = subscribeUpdates((u) => { if (!u || u.kind === 'host') load(); });
  return {
    destroy() { state.destroyed = true; unsub(); clearCharts(); },
    themeChanged() { for (const c of state.charts) c.scheduleDraw?.(); },
    async update(params) {
      if (params.key === key) return true;
      return false;
    },
  };
}

function sourceWording(host) {
  switch (host.source) {
    case 'local': return 'The computer GWatch runs on';
    case 'agent': return 'An agent on this machine sends its readings to GWatch. GWatch never connects to it.';
    case 'url': return 'GWatch reads this machine’s metrics endpoint.';
    default: return host.source || '—';
  }
}

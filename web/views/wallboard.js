// Wallboard: read-only, large-type status screen for a spare display.

import { api } from '../api.js';
import { h, icon, clear, replace, statusPill, statusMeta, emptyState } from '../components.js';
import { relTime, ms as fmtMs, plural, dateShort, timeShort } from '../fmt.js';
import { LineChart, toSeries, seriesColor } from '../charts.js';

const MONTHS_LONG = ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December'];
const DAYS_LONG = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];

export async function mount(root, ctx) {
  const state = { data: null, charts: [], destroyed: false, timer: null, clockTimer: null };
  ctx.setTitle('Wallboard');
  const wall = h('div', { class: 'wall' });
  root.append(wall);

  const clockTime = h('div', { class: 'time' });
  const clockDate = h('div', { class: 'date' });
  const tickClock = () => { const d = new Date(); clockTime.textContent = timeShort(d, { seconds: false }); clockDate.textContent = `${DAYS_LONG[d.getDay()]} ${d.getDate()} ${MONTHS_LONG[d.getMonth()]}`; };
  tickClock();
  state.clockTimer = setInterval(tickClock, 1000);

  async function load() {
    const data = await api.get('/api/wallboard');
    if (state.destroyed) return;
    state.data = data;
    render();
  }

  function render() {
    const d = state.data;
    const s = d.summary || {};
    state.charts.forEach((c) => c.destroy()); state.charts = [];
    clear(wall);

    // Headline
    let headline, sub, color;
    const problems = (s.down || 0) + (s.degraded || 0);
    if (s.down > 0) { headline = `${plural(s.down, 'node')} down`; sub = s.degraded ? `${s.degraded} more degraded` : 'Everything else is healthy'; color = 'var(--down)'; }
    else if (s.degraded > 0) { headline = `${plural(s.degraded, 'node')} degraded`; sub = 'No outages'; color = 'var(--warn)'; }
    else if ((s.total || 0) === 0) { headline = 'Nothing monitored yet'; sub = 'Add nodes to see them here'; color = 'var(--unknown)'; }
    else if (!s.up && s.unknown) { headline = 'Waiting for results'; sub = 'First checks are running'; color = 'var(--unknown)'; }
    else { headline = 'All systems healthy'; sub = `${plural(s.up, 'node')} up${s.maintenance ? ` · ${s.maintenance} in maintenance` : ''}${s.paused ? ` · ${s.paused} paused` : ''}`; color = 'var(--up)'; }

    const counts = h('div', { class: 'wall-counts' });
    for (const [k, label] of [['up', 'Up'], ['degraded', 'Degraded'], ['down', 'Down'], ['unknown', 'Unknown']]) {
      const n = s[k] || 0; const m = statusMeta(k);
      counts.append(h('div', { class: 'wall-count' }, h('div', { class: 'n', style: { color: n ? m.color : 'var(--dim)' } }, String(n)), h('div', { class: 'l' }, icon(m.icon), label)));
    }
    wall.append(h('div', { class: 'wall-top' },
      h('div', { class: 'wall-headline' }, h('span', { class: 'big-dot', style: { background: color } }), h('div', null, h('h1', null, headline), h('div', { class: 'sub' }, sub))),
      counts,
      h('div', { class: 'wall-clock' }, clockTime, clockDate)));

    // Main columns
    const left = h('div', { class: 'wall-col' });
    const right = h('div', { class: 'wall-col' });

    // Incidents / attention
    const att = d.attention || [];
    const attCard = h('section', { class: 'card wall-card' }, h('div', { class: 'card-title' }, 'Needs attention'));
    if (!att.length) attCard.append(h('div', { class: 'wall-all-clear' }, icon('check'), 'No open incidents'));
    else {
      const list = h('div', { class: 'wall-incidents' });
      for (const a of att.slice(0, 6)) {
        list.append(h('div', { class: 'wall-incident' }, statusPill(a.status, { large: true }),
          h('div', { class: 'i-body' }, h('div', { class: 'i-title' }, `${a.nodeName} › ${a.checkName}`), h('div', { class: 'i-sub' }, a.affectedBy ? `Unavailable because ${a.affectedBy} is down` : (a.message || ''))),
          h('div', { class: 'i-since' }, a.since ? `since ${relTime(a.since).replace(' ago', '')}` : '')));
      }
      if (att.length > 6) list.append(h('div', { class: 'muted', style: { padding: '4px 14px' } }, `+ ${att.length - 6} more`));
      attCard.append(list);
    }
    left.append(attCard);

    // Groups
    const groups = d.groups || [];
    if (groups.length) {
      const g = h('div', { class: 'wall-groups' });
      for (const gr of groups) {
        const parts = [];
        if (gr.up) parts.push(`${gr.up} up`); if (gr.degraded) parts.push(`${gr.degraded} degraded`); if (gr.down) parts.push(`${gr.down} down`); if (gr.unknown) parts.push(`${gr.unknown} unknown`); if (gr.maintenance) parts.push(`${gr.maintenance} maint.`); if (gr.paused) parts.push(`${gr.paused} paused`);
        g.append(h('div', { class: 'wall-group' }, h('div', { class: 'row-between' }, h('span', { class: 'g-name' }, gr.name), statusPill(gr.status)), h('div', { class: 'g-sub' }, parts.join(' · '))));
      }
      left.append(h('section', { class: 'card wall-card' }, h('div', { class: 'card-title' }, 'Groups'), g));
    }

    // Certificates + maintenance
    const certs = d.certWarnings || [];
    const maint = d.maintenance || [];
    if (certs.length || maint.length) {
      const c = h('section', { class: 'card wall-card' });
      if (certs.length) {
        c.append(h('div', { class: 'card-title' }, 'Certificate warnings'));
        for (const w of certs) c.append(h('div', { class: 'wall-cert' }, h('div', { class: `days ${w.daysRemaining <= 7 ? 'text-down' : 'text-degraded'}` }, w.daysRemaining <= 0 ? 'now' : `${w.daysRemaining}d`), h('div', null, h('div', { class: 'strong' }, `${w.nodeName} › ${w.checkName}`), h('div', { class: 'muted small' }, `expires ${dateShort(w.notAfter)}`))));
      }
      if (maint.length) {
        c.append(h('div', { class: 'card-title', style: { marginTop: certs.length ? '16px' : 0 } }, 'Active maintenance'));
        for (const m of maint) c.append(h('div', { class: 'row', style: { padding: '6px 0' } }, statusPill('maintenance'), h('span', { class: 'strong' }, m.name), h('span', { class: 'muted' }, m.group ? `group ${m.group}` : m.nodeId ? 'one node' : 'all nodes')));
      }
      state._sideCard = c;
    }

    // Trend charts
    const trends = d.trends || [];
    const chartsWrap = h('div', { class: 'wall-charts' });
    if (!trends.length) chartsWrap.append(h('section', { class: 'card wall-card' }, emptyState({ icon: 'activity', title: 'No trends yet', text: 'Charts appear once checks have some history.', compact: true })));
    for (const hs of trends.slice(0, 6)) {
      const host = h('div', null);
      const isLoss = hs.checkType === 'ping' && (hs.points || []).every((p) => p.avgMs == null) && (hs.points || []).some((p) => p.lossPct != null);
      const card = h('section', { class: 'card wall-chart' },
        h('div', { class: 'wc-head' }, h('span', { class: 'wc-name' }, `${hs.nodeName} › ${hs.checkName}`), h('span', { class: 'wc-val' }, hs.summary?.avgMs != null ? `avg ${fmtMs(hs.summary.avgMs)} · ${hs.summary.availability?.toFixed?.(2) ?? '—'}%` : `${hs.summary?.availability?.toFixed?.(2) ?? '—'}% available`)),
        host);
      chartsWrap.append(card);
      const chart = new LineChart(host, { unit: isLoss ? '%' : 'ms', legend: false, height: 150, minTickPx: 90, ariaLabel: `${hs.checkName} trend` });
      state.charts.push(chart);
      chart.setData({ series: [toSeries(hs, isLoss ? 'loss' : 'avg', seriesColor(0))], from: hs.from, to: hs.to, bucketSeconds: hs.bucketSeconds || 0 });
    }
    right.append(chartsWrap);
    if (state._sideCard) { right.append(state._sideCard); state._sideCard = null; }

    wall.append(h('div', { class: 'wall-main' }, left, right));

    // Footer
    const hl = d.health || {};
    const healthy = hl.serviceRunning !== false && hl.schedulerRunning !== false;
    wall.append(h('div', { class: 'wall-foot' },
      h('div', { class: 'row' }, h('span', { class: `status-glyph ${healthy ? 'text-up' : 'text-down'}` }, icon(healthy ? 'check' : 'x'), healthy ? 'Service healthy' : 'Service issue'), h('span', { class: 'dim' }, '·'), h('span', null, `last check ${relTime(hl.lastCheckAt || d.generatedAt)}`), h('span', { class: 'dim' }, '·'), h('span', null, `${s.total || 0} nodes monitored`)),
      h('a', { href: '#/dashboard' }, icon('logout'), ' Exit wallboard')));
  }

  try { await load(); } catch (e) { replace(wall, h('div', { class: 'card', style: { margin: '40px' } }, emptyState({ icon: 'alert', title: 'Could not load the wallboard', text: e.message, actions: h('a', { class: 'btn', href: '#/dashboard' }, 'Back') }))); }
  state.timer = setInterval(() => load().catch(() => {}), 30000);

  return {
    refresh: () => load().catch(() => {}),
    themeChanged() { if (state.data) render(); },
    destroy() { state.destroyed = true; clearInterval(state.timer); clearInterval(state.clockTimer); state.charts.forEach((c) => c.destroy()); },
  };
}

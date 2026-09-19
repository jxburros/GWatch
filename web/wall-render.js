// The wallboard renderer.
//
// A wallboard is not a dashboard with bigger type. A dashboard is read at a
// desk by someone who came looking for an answer; a wallboard is read across a
// room by someone who did not. So it has its own identity — a dark field, one
// sentence in the largest type on the screen, status carried by lit colour
// rather than by labels, and panels that are bands of information rather than
// cards to click. Nothing here is interactive, because nobody is standing at
// it.
//
// This module draws a board from the document /api/wallboards/{id}/view
// returns. It is shared by the in-application wallboard and by the standalone
// page a projected display opens, so both are the same board.

import { h, icon, clear, statusOrb, statusMeta, statusSpine, statusWord, statusPill, emptyState } from './components.js';
import { LineChart, toSeries, seriesColor } from './charts.js';
import { relTime, ms as fmtMs, plural, dateShort, timeShort } from './fmt.js';

const MONTHS_LONG = ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December'];
const DAYS_LONG = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];

/** The panels the editor offers, and what each one is for. */
export const PANEL_TYPES = [
  { type: 'headline', label: 'Headline', desc: 'The one sentence that matters, with a lit circle beside it.', w: 6, h: 1, config: {} },
  { type: 'counts', label: 'Counts', desc: 'Up, degraded, down and unknown as large numerals.', w: 4, h: 1, config: {} },
  { type: 'clock', label: 'Clock', desc: 'The time and the date.', w: 2, h: 1, config: { seconds: false } },
  { type: 'attention', label: 'Needs attention', desc: 'Everything down or degraded right now, worst first.', w: 5, h: 2, config: { limit: 6 } },
  { type: 'groups', label: 'Groups', desc: 'One tile per group; its worst check decides its colour.', w: 5, h: 1, config: {} },
  { type: 'nodes', label: 'Nodes', desc: 'A grid of nodes and their status, optionally one group or tag.', w: 6, h: 2, config: { group: '', tag: '', limit: 24 } },
  { type: 'trends', label: 'Trends', desc: 'Latency or availability over time for the checks you pick.', w: 7, h: 2, config: { range: '24h', limit: 4, checkIds: [] } },
  { type: 'certs', label: 'Certificates', desc: 'Certificates expiring soon or already invalid.', w: 4, h: 1, config: {} },
  { type: 'maintenance', label: 'Maintenance', desc: 'Maintenance windows in force right now.', w: 4, h: 1, config: {} },
  { type: 'health', label: 'Service', desc: 'Whether GWatch itself is running and checking.', w: 3, h: 1, config: {} },
  { type: 'message', label: 'Message', desc: 'A fixed line of text for whoever walks past.', w: 4, h: 1, config: { text: '' } },
];

export function panelMeta(type) {
  return PANEL_TYPES.find((p) => p.type === type) || { type, label: type, w: 4, h: 1, config: {} };
}

export const WALL_THEMES = [
  { value: 'signal', label: 'Signal', desc: 'The application’s own graphite, lit by the accent.' },
  { value: 'contrast', label: 'High contrast', desc: 'Black field, heavy type. For a bright room or a far wall.' },
  { value: 'midnight', label: 'Midnight', desc: 'Deep blue, dimmed. For a screen somebody sleeps near.' },
  { value: 'daylight', label: 'Daylight', desc: 'Light field. For a screen by a window.' },
];

/**
 * createWall builds a live wallboard inside `root`.
 *
 * `load()` must resolve to the view document. The board owns its own refresh
 * timer and its own clock; the caller owns its lifetime and must call destroy.
 */
export function createWall(root, load, { onError } = {}) {
  const state = { doc: null, charts: [], timer: null, clockTimer: null, destroyed: false, clocks: [] };
  const wall = h('div', { class: 'wall' });
  clear(root);
  root.append(wall);

  function tickClocks() {
    const d = new Date();
    for (const c of state.clocks) {
      c.time.textContent = timeShort(d, { seconds: !!c.seconds });
      c.date.textContent = `${DAYS_LONG[d.getDay()]} ${d.getDate()} ${MONTHS_LONG[d.getMonth()]}`;
    }
  }
  state.clockTimer = setInterval(tickClocks, 1000);

  function clearCharts() {
    for (const c of state.charts) c.destroy?.();
    state.charts = [];
  }

  async function refresh() {
    let doc;
    try {
      doc = await load();
    } catch (e) {
      if (state.destroyed) return;
      if (onError) onError(e);
      else clear(wall).append(emptyState({ icon: 'alert', title: 'Could not load this wallboard', text: e.message }));
      return;
    }
    if (state.destroyed) return;
    state.doc = doc;
    draw();
    rearm();
  }

  function rearm() {
    clearInterval(state.timer);
    const secs = Math.max(5, state.doc?.wallboard?.layout?.refreshSeconds || 20);
    state.timer = setInterval(() => { refresh(); }, secs * 1000);
  }

  function draw() {
    const doc = state.doc;
    const board = doc.wallboard || {};
    const layout = board.layout || {};
    clearCharts();
    state.clocks = [];
    clear(wall);

    wall.dataset.wallTheme = layout.theme || 'signal';
    wall.style.setProperty('--wall-cols', String(layout.columns || 12));
    wall.style.setProperty('--wall-scale', String(layout.scale || 1));

    const grid = h('div', { class: 'wall-grid' });
    const panels = board.panels || [];
    if (!panels.length) {
      grid.append(h('div', { class: 'wall-panel', style: { gridColumn: '1 / -1' } },
        emptyState({ icon: 'monitor', title: 'This wallboard is empty', text: 'Add a panel to it and it will appear here.' })));
    }
    for (const p of panels) {
      const el = renderPanel(p, doc, state);
      if (el) grid.append(el);
    }
    wall.append(grid);

    if (!layout.hideChrome) wall.append(footer(doc, board));
    tickClocks();
  }

  refresh();

  return {
    refresh,
    redraw() { if (state.doc) draw(); },
    destroy() {
      state.destroyed = true;
      clearInterval(state.timer);
      clearInterval(state.clockTimer);
      clearCharts();
    },
  };
}

function footer(doc, board) {
  const hl = doc.health || {};
  const s = doc.summary || {};
  const healthy = hl.serviceRunning !== false && hl.schedulerRunning !== false;
  return h('div', { class: 'wall-foot' },
    h('div', { class: 'row' },
      h('span', { class: `status-glyph ${healthy ? 'text-up' : 'text-down'}` }, icon(healthy ? 'check' : 'x'), healthy ? 'Service healthy' : 'Service issue'),
      h('span', { class: 'dim' }, '·'),
      h('span', null, `last check ${relTime(hl.lastCheckAt || doc.generatedAt)}`),
      h('span', { class: 'dim' }, '·'),
      h('span', null, `${s.total || 0} nodes monitored`)),
    h('span', { class: 'wall-foot-name' }, board.name || 'Wallboard'));
}

/* ---------------- panels ---------------- */

function renderPanel(p, doc, state) {
  const cfg = parseConfig(p.config);
  const body = h('div', { class: 'wall-panel-body' });
  const meta = panelMeta(p.type);
  const el = h('section', {
    class: `wall-panel wall-panel-${p.type}`,
    style: { gridColumn: `span ${p.width || meta.w}`, gridRow: `span ${p.height || meta.h}` },
    'aria-label': p.title || meta.label,
  });
  // A headline, a clock and a message are the thing itself; anything that is a
  // list or a chart wears a label band so the reader knows what they are
  // looking at from across the room.
  const bare = p.type === 'headline' || p.type === 'clock' || p.type === 'counts' || p.type === 'message';
  if (!bare) el.append(h('div', { class: 'wall-panel-head' }, p.title || meta.label));
  el.append(body);

  switch (p.type) {
    case 'headline': headline(body, doc); break;
    case 'counts': counts(body, doc); break;
    case 'clock': clock(body, cfg, state); break;
    case 'attention': attention(body, doc, cfg); break;
    case 'groups': groups(body, doc); break;
    case 'nodes': nodes(body, doc, cfg); break;
    case 'trends': trends(body, doc, cfg, state); break;
    case 'certs': certs(body, doc); break;
    case 'maintenance': maintenance(body, doc); break;
    case 'health': health(body, doc); break;
    case 'message': message(body, cfg); break;
    default: body.append(h('p', { class: 'muted' }, `Unknown panel "${p.type}"`));
  }
  return el;
}

function parseConfig(config) {
  if (!config) return {};
  if (typeof config === 'string') { try { return JSON.parse(config); } catch { return {}; } }
  return config;
}

function headline(body, doc) {
  const s = doc.summary || {};
  let text, sub, tone;
  if (s.down > 0) { text = `${plural(s.down, 'node')} down`; sub = s.degraded ? `${s.degraded} more degraded` : 'Everything else is healthy'; tone = 'down'; }
  else if (s.degraded > 0) { text = `${plural(s.degraded, 'node')} degraded`; sub = 'No outages'; tone = 'degraded'; }
  else if ((s.total || 0) === 0) { text = 'Nothing monitored yet'; sub = 'Add nodes to see them here'; tone = 'unknown'; }
  else if (!s.up && s.unknown) { text = 'Waiting for results'; sub = 'First checks are running'; tone = 'unknown'; }
  else { text = 'All systems healthy'; sub = `${plural(s.up, 'node')} up${s.maintenance ? ` · ${s.maintenance} in maintenance` : ''}${s.paused ? ` · ${s.paused} paused` : ''}`; tone = 'up'; }
  body.append(h('div', { class: 'wall-headline' },
    statusOrb(tone, { size: 'lg', label: text }),
    h('div', null, h('h1', null, text), h('div', { class: 'sub' }, sub))));
}

function counts(body, doc) {
  const s = doc.summary || {};
  const row = h('div', { class: 'wall-counts' });
  for (const [k, label] of [['up', 'Up'], ['degraded', 'Degraded'], ['down', 'Down'], ['unknown', 'Unknown']]) {
    const n = s[k] || 0; const m = statusMeta(k);
    row.append(h('div', { class: 'wall-count' },
      h('div', { class: 'n', style: { color: n ? m.color : 'var(--dim)' } }, String(n)),
      h('div', { class: 'l' }, icon(m.icon), label)));
  }
  body.append(row);
}

function clock(body, cfg, state) {
  const time = h('div', { class: 'time' });
  const date = h('div', { class: 'date' });
  state.clocks.push({ time, date, seconds: !!cfg.seconds });
  body.append(h('div', { class: 'wall-clock' }, time, date));
}

function attention(body, doc, cfg) {
  const att = doc.attention || [];
  const limit = cfg.limit > 0 ? cfg.limit : 6;
  if (!att.length) {
    body.append(h('div', { class: 'wall-all-clear' }, icon('check'), 'No open incidents'));
    return;
  }
  const list = h('div', { class: 'wall-incidents' });
  for (const a of att.slice(0, limit)) {
    list.append(h('div', { class: 'wall-incident' },
      statusSpine(a.status, { key: `wall:${a.nodeId}:${a.checkName}` }),
      statusWord(a.status),
      h('div', { class: 'i-body' },
        h('div', { class: 'i-title' }, `${a.nodeName} › ${a.checkName}`),
        h('div', { class: 'i-sub' }, a.affectedBy ? `Unavailable because ${a.affectedBy} is down` : (a.message || ''))),
      h('div', { class: 'i-since' }, a.since ? `since ${relTime(a.since).replace(' ago', '')}` : '')));
  }
  if (att.length > limit) list.append(h('div', { class: 'muted', style: { padding: '4px 14px' } }, `+ ${att.length - limit} more`));
  body.append(list);
}

function groups(body, doc) {
  const list = doc.groups || [];
  if (!list.length) { body.append(h('p', { class: 'muted' }, 'No groups yet.')); return; }
  const g = h('div', { class: 'wall-groups' });
  for (const gr of list) {
    const parts = [];
    if (gr.up) parts.push(`${gr.up} up`);
    if (gr.degraded) parts.push(`${gr.degraded} degraded`);
    if (gr.down) parts.push(`${gr.down} down`);
    if (gr.unknown) parts.push(`${gr.unknown} unknown`);
    if (gr.maintenance) parts.push(`${gr.maintenance} maint.`);
    if (gr.paused) parts.push(`${gr.paused} paused`);
    g.append(h('div', { class: 'wall-group' },
      h('div', { class: 'g-head' }, statusOrb(gr.status, { size: 'lg' }), h('span', { class: 'g-name' }, gr.name)),
      h('div', { class: 'g-sub' }, parts.join(' · '))));
  }
  body.append(g);
}

function nodes(body, doc, cfg) {
  let list = (doc.nodes || []).map((nv) => ({ ...nv.node, status: nv.status }));
  if (cfg.group) list = list.filter((n) => n.group === cfg.group);
  if (cfg.tag) list = list.filter((n) => (n.tags || []).includes(cfg.tag));
  if (Array.isArray(cfg.nodeIds) && cfg.nodeIds.length) {
    const want = new Set(cfg.nodeIds.map(Number));
    list = list.filter((n) => want.has(n.id));
  }
  // Worst first: on a wall the thing that is wrong has to be at the top.
  const order = ['down', 'degraded', 'unknown', 'maintenance', 'up', 'paused'];
  list.sort((a, b) => order.indexOf(a.status || 'unknown') - order.indexOf(b.status || 'unknown') || a.name.localeCompare(b.name));
  const limit = cfg.limit > 0 ? cfg.limit : 24;
  if (!list.length) { body.append(h('p', { class: 'muted' }, 'No nodes match this panel.')); return; }
  const grid = h('div', { class: 'wall-nodes' });
  for (const n of list.slice(0, limit)) {
    grid.append(h('div', { class: `wall-node s-${n.status || 'unknown'}` },
      statusOrb(n.status || 'unknown'),
      h('span', { class: 'n-name' }, n.name)));
  }
  if (list.length > limit) grid.append(h('div', { class: 'wall-node muted' }, `+ ${list.length - limit} more`));
  body.append(grid);
}

function trends(body, doc, cfg, state) {
  const all = doc.trends || [];
  const want = Array.isArray(cfg.checkIds) && cfg.checkIds.length ? new Set(cfg.checkIds.map(Number)) : null;
  const list = (want ? all.filter((hs) => want.has(hs.checkId)) : all).slice(0, cfg.limit > 0 ? cfg.limit : 4);
  if (!list.length) {
    body.append(emptyState({ icon: 'activity', title: 'No trends yet', text: 'Charts appear once checks have some history.', compact: true }));
    return;
  }
  const wrap = h('div', { class: 'wall-charts' });
  for (const hs of list) {
    const host = h('div', null);
    // A ping check that reports loss but no latency is charted as loss: on a
    // wall an empty latency chart says nothing at all.
    const isLoss = hs.checkType === 'ping' && (hs.points || []).every((p) => p.avgMs == null) && (hs.points || []).some((p) => p.lossPct != null);
    wrap.append(h('section', { class: 'wall-chart' },
      h('div', { class: 'wc-head' },
        h('span', { class: 'wc-name' }, `${hs.nodeName} › ${hs.checkName}`),
        h('span', { class: 'wc-val' }, hs.summary?.avgMs != null
          ? `avg ${fmtMs(hs.summary.avgMs)} · ${hs.summary.availability?.toFixed?.(2) ?? '—'}%`
          : `${hs.summary?.availability?.toFixed?.(2) ?? '—'}% available`)),
      host));
    const chart = new LineChart(host, { unit: isLoss ? '%' : 'ms', legend: false, height: 150, minTickPx: 90, ariaLabel: `${hs.checkName} trend` });
    state.charts.push(chart);
    chart.setData({ series: [toSeries(hs, isLoss ? 'loss' : 'avg', seriesColor(0))], from: hs.from, to: hs.to, bucketSeconds: hs.bucketSeconds || 0 });
  }
  body.append(wrap);
}

function certs(body, doc) {
  const list = doc.certWarnings || [];
  if (!list.length) { body.append(h('p', { class: 'muted' }, 'Every certificate is valid and not expiring soon.')); return; }
  for (const w of list) {
    body.append(h('div', { class: 'wall-cert' },
      h('div', { class: `days ${w.daysRemaining <= 7 ? 'text-down' : 'text-degraded'}` }, w.daysRemaining <= 0 ? 'now' : `${w.daysRemaining}d`),
      h('div', null,
        h('div', { class: 'strong' }, `${w.nodeName} › ${w.checkName}`),
        h('div', { class: 'muted small' }, `expires ${dateShort(w.notAfter)}`))));
  }
}

function maintenance(body, doc) {
  const list = doc.maintenance || [];
  if (!list.length) { body.append(h('p', { class: 'muted' }, 'No maintenance in force.')); return; }
  for (const m of list) {
    body.append(h('div', { class: 'row', style: { padding: '6px 0' } },
      statusPill('maintenance'),
      h('span', { class: 'strong' }, m.name),
      h('span', { class: 'muted' }, m.group ? `group ${m.group}` : m.nodeId ? 'one node' : 'all nodes')));
  }
}

function health(body, doc) {
  const hl = doc.health || {};
  const ok = hl.serviceRunning !== false && hl.schedulerRunning !== false;
  body.append(h('div', { class: 'wall-health' },
    statusOrb(ok ? 'up' : 'down', { size: 'lg' }),
    h('div', null,
      h('div', { class: 'strong' }, ok ? 'Running' : 'Not running'),
      h('div', { class: 'muted' }, hl.checksEnabled != null ? `${hl.checksEnabled} checks scheduled` : ''),
      h('div', { class: 'muted small' }, hl.lastCheckAt ? `last check ${relTime(hl.lastCheckAt)}` : ''))));
}

function message(body, cfg) {
  body.append(h('div', { class: 'wall-message' }, cfg.text || ''));
}

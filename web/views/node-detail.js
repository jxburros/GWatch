// Node detail: header, per-check cards with result inspector, charts, events.

import { api, getHistoryMulti, qs } from '../api.js';
import { h, icon, clear, replace, statusPill, statusGlyph, importanceBadge, tagList, banner, toast, confirmDialog, showMenu, menuButton, emptyState, skeleton, eventRow, rangeChips, checkTypeLabel } from '../components.js';
import { LineChart, toSeries, uptimeBar, uptimeLegend, SERIES_COLORS } from '../charts.js';
import { relTime, ms as fmtMs, pct, dateTime, interval, plural, timeShort } from '../fmt.js';
import { resultInspector } from './inspector.js';
import { openTriggerEditor, triggerRow } from './automation.js';
import { hardwarePanel } from './machines.js';

export async function mount(root, ctx) {
  const id = ctx.params.id;
  const state = { node: null, events: [], triggers: [], nodes: [], range: '24h', charts: [], expanded: new Set(), results: new Map(), destroyed: false, history: null, hardware: new Map(), latChart: null, lossChart: null, uptimeEl: null, historyShape: '' };

  const headEl = h('div');
  const bannersEl = h('div', { class: 'stack-sm', style: { marginBottom: '14px' } });
  const checksEl = h('div', { class: 'stack-joined' });
  // A machine is a node, so its readings belong on the node rather than on a
  // page of their own. One panel per hardware check.
  const hardwareEl = h('div', { class: 'stack' });
  const triggersEl = h('section', { class: 'card', 'aria-label': 'Triggers' });
  const chartsEl = h('section', { class: 'card', 'aria-label': 'History' });
  const eventsEl = h('section', { class: 'card', 'aria-label': 'Events' });
  // The history card's frame is built once and kept for the life of the view.
  // A live update swaps what is inside it, never the card itself, so the
  // charts are never taken off the page while their new data is in flight.
  const historyChips = h('div', { class: 'card-actions' });
  const historyBody = h('div', { class: 'stack' });
  root.append(headEl, bannersEl, h('div', { class: 'stack' }, checksEl, hardwareEl, chartsEl, triggersEl, eventsEl));
  headEl.append(skeleton({ lines: 2 }));

  async function load({ quiet = false } = {}) {
    const [node, events, triggers, nodes] = await Promise.all([api.get(`/api/nodes/${id}`), api.get(`/api/events${qs({ nodeId: id, limit: 30 })}`).catch(() => []), api.get(`/api/triggers?nodeId=${id}`).catch(() => []), quiet && state.nodes.length ? Promise.resolve(state.nodes) : api.get('/api/nodes').catch(() => [])]);
    if (state.destroyed) return;
    state.node = node; state.events = events || []; state.triggers = triggers || []; state.nodes = nodes || [];
    renderHead(); renderBanners(); renderChecks(); renderHardware(); renderTriggers(); renderEvents();
    if (!quiet || !state.history) await loadHistory();
    else await loadHistory();
  }

  /* ---------- Triggers ---------- */
  async function loadTriggers() {
    try { state.triggers = await api.get(`/api/triggers?nodeId=${id}`); } catch { /* keep */ }
    if (!state.destroyed) renderTriggers();
  }
  function renderTriggers() {
    const n = state.node;
    clear(triggersEl);
    triggersEl.append(h('div', { class: 'card-head' },
      h('div', null, h('h2', null, icon('zap'), 'Triggers'), h('p', { class: 'note' }, 'Run a webhook, a git command, custom code or another node\'s checks when this node changes state.')),
      h('button', { class: 'btn btn-sm btn-primary admin-only', type: 'button', onclick: async () => { const saved = await openTriggerEditor(null, { node: n, nodes: state.nodes }); if (saved) loadTriggers(); } }, icon('plus'), 'Add trigger')));
    if (!state.triggers.length) { triggersEl.append(h('p', { class: 'note' }, 'No triggers on this node. Example: when it goes down, POST to a Discord webhook; when it recovers, run "git pull" in your homelab repo.')); return; }
    for (const t of state.triggers) triggersEl.append(triggerRow(t, { node: n, nodes: state.nodes, onChange: loadTriggers }));
  }

  /* ---------- Header ---------- */
  function renderHead() {
    const n = state.node;
    ctx.setTitle(n.name, {
      actions: [
        h('button', { class: 'btn admin-only', type: 'button', onclick: runAll }, icon('play'), 'Run all now'),
        h('a', { class: 'btn btn-primary admin-only', href: `#/nodes/${n.id}/edit` }, icon('edit'), 'Edit'),
        menuButton(() => [
          { label: n.enabled === false ? 'Enable node' : 'Disable node', icon: 'power', onClick: () => setEnabled(n.enabled === false), adminOnly: true },
          { label: 'Duplicate', icon: 'copy', onClick: duplicate, adminOnly: true },
          { sep: true, adminOnly: true },
          { label: 'Silence alerts for 1 hour', icon: 'bellOff', onClick: () => silence(60), adminOnly: true },
          { label: 'Silence alerts for 8 hours', icon: 'bellOff', onClick: () => silence(480), adminOnly: true },
          { label: 'Silence alerts for 24 hours', icon: 'bellOff', onClick: () => silence(1440), adminOnly: true },
          anySilenced() ? { label: 'Unsilence', icon: 'bell', onClick: () => silence(0), adminOnly: true } : null,
          { sep: true },
          { label: 'Export events CSV', icon: 'download', href: `/api/export/events.csv${qs({ nodeId: n.id })}`, download: `events-${n.id}.csv` },
          { label: 'Delete node', icon: 'trash', danger: true, onClick: remove, adminOnly: true },
        ], { label: 'More actions' }),
      ],
    });
    replace(headEl, h('div', { class: 'detail-head' },
      h('div', { class: 'd-title' },
        h('h1', null, statusPill(n.status || 'unknown', { large: true }), n.name),
        h('div', { class: 'd-meta' },
          n.host ? h('span', { class: 'host' }, n.host) : null,
          n.group ? h('span', { class: 'tag tag-group' }, n.group) : null,
          ...(n.tags || []).map((t) => h('span', { class: 'tag' }, t)),
          importanceBadge(n.importance),
          n.template ? h('span', { class: 'dim small' }, `from ${n.template} template`) : null,
          state.triggers.length ? h('a', { class: 'tag', href: '#triggers', onclick: (e) => { e.preventDefault(); triggersEl.scrollIntoView({ behavior: 'smooth' }); } }, icon('zap'), ` ${state.triggers.length} trigger${state.triggers.length === 1 ? '' : 's'}`) : null,
        ),
        n.notes ? h('p', { class: 'muted', style: { maxWidth: '720px', whiteSpace: 'pre-wrap' } }, n.notes) : null,
      ),
    ));
  }

  function anySilenced() {
    const st = state.node?.stateByCheck || {};
    return Object.values(st).some((s) => s.silencedUntil && new Date(s.silencedUntil) > new Date());
  }

  function renderBanners() {
    const n = state.node;
    clear(bannersEl);
    const states = n.stateByCheck || {};
    const affected = Object.values(states).find((s) => s.affectedByNodeName);
    if (affected) bannersEl.append(banner('maint', h('span', null, h('b', null, `${n.name} appears unavailable because ${affected.affectedByNodeName} is down.`), ' Alerts for this node are suppressed until the parent recovers; results are still recorded.'), { icon: 'link' }));
    if (n.inMaintenance || n.status === 'maintenance') bannersEl.append(banner('maint', h('span', null, h('b', null, 'In maintenance.'), ' Alerts are paused during this window. Checks keep running and results are kept.'), { icon: 'wrench' }));
    if (n.enabled === false) bannersEl.append(banner('info', h('span', null, h('b', null, 'This node is disabled.'), ' No checks run until you enable it.'), { icon: 'pause', actions: h('button', { class: 'btn btn-sm', type: 'button', onclick: () => setEnabled(true) }, 'Enable') }));
    const silenced = Object.values(states).filter((s) => s.silencedUntil && new Date(s.silencedUntil) > new Date());
    if (silenced.length) {
      const until = silenced.map((s) => s.silencedUntil).sort().pop();
      bannersEl.append(banner('info', h('span', null, h('b', null, 'Alerts silenced'), ` until ${dateTime(until, { seconds: false })} (${relTime(until)}).`), { icon: 'bellOff', actions: h('button', { class: 'btn btn-sm', type: 'button', onclick: () => silence(0) }, 'Unsilence') }));
    }
    const suppressed = Object.values(states).find((s) => s.alertSuppressed && s.suppressReason && s.suppressReason !== 'dependency' && s.suppressReason !== 'silenced' && s.suppressReason !== 'maintenance');
    if (suppressed) bannersEl.append(banner('info', `Alerts currently suppressed (${suppressed.suppressReason}).`, { icon: 'bellOff' }));
  }

  /* ---------- Checks ---------- */
  function renderChecks() {
    const n = state.node;
    clear(checksEl);
    const checks = n.checks || [];
    if (!checks.length) { checksEl.append(h('div', { class: 'card' }, emptyState({ icon: 'activity', title: 'No checks on this node', text: 'Add a ping, HTTP or TCP check so GWatch can start watching it.', actions: h('a', { class: 'btn btn-primary admin-only', href: `#/nodes/${n.id}/edit` }, 'Add checks') }))); return; }
    for (const c of checks) checksEl.append(checkCard(c));
  }

  function checkCard(c) {
    const n = state.node;
    const st = (n.stateByCheck || {})[c.id] || {};
    const last = (n.lastResults || {})[c.id] || null;
    const status = c.enabled === false ? 'paused' : (st.status || 'unknown');
    const card = h('section', { class: 'card check-card', 'aria-label': c.name });
    const expanded = state.expanded.has(c.id);
    const target = c.config?.target || n.host;
    const runBtn = h('button', { class: 'btn btn-sm', type: 'button', onclick: () => runCheck(c, runBtn) }, icon('play'), 'Run now');
    const detailBtn = h('button', { class: 'btn btn-sm', type: 'button', 'aria-expanded': expanded ? 'true' : 'false', onclick: () => { if (state.expanded.has(c.id)) state.expanded.delete(c.id); else state.expanded.add(c.id); renderChecks(); } }, icon(expanded ? 'chevronDown' : 'chevronRight'), expanded ? 'Hide details' : 'Inspect last result');
    // The band carries the facts that do not change while you read the card:
    // what kind of check this is, how often it runs and what it points at.
    card.append(h('div', { class: 'check-band' },
      h('span', null, checkTypeLabel(c.type)),
      h('span', { class: 'b-meta' },
        h('span', null, 'every ', interval(c.intervalSeconds)),
        c.config?.target ? h('span', { class: 'target', title: c.config.target }, c.config.target) : null)));
    card.append(h('div', { class: 'check-card-head' },
      h('div', { class: 'c-title' },
        h('h3', null, statusPill(status), c.name),
        h('div', { class: 'c-msg' }, st.lastMessage || last?.message || (c.enabled === false ? 'Paused — this check is disabled.' : 'Waiting for the first result.')),
        st.affectedByNodeName ? h('div', { class: 'affected-note' }, icon('link'), `affected by ${st.affectedByNodeName}`) : null,
        st.silencedUntil && new Date(st.silencedUntil) > new Date() ? h('div', { class: 'small muted' }, icon('bellOff'), ` silenced until ${timeShort(st.silencedUntil)}`) : null,
      ),
      h('div', { class: 'btn-group' }, runBtn, detailBtn, menuButton(() => [
        { label: c.enabled === false ? 'Enable check' : 'Disable check', icon: 'power', onClick: () => setCheckEnabled(c, c.enabled === false), adminOnly: true },
        { label: 'Silence 1 hour', icon: 'bellOff', onClick: () => silenceCheck(c, 60), adminOnly: true },
        { label: 'Silence 24 hours', icon: 'bellOff', onClick: () => silenceCheck(c, 1440), adminOnly: true },
        st.silencedUntil && new Date(st.silencedUntil) > new Date() ? { label: 'Unsilence', icon: 'bell', onClick: () => silenceCheck(c, 0), adminOnly: true } : null,
        { sep: true },
        { label: 'Export results CSV', icon: 'download', href: `/api/export/results.csv${qs({ checkId: c.id, limit: 5000 })}`, download: `results-${c.id}.csv` },
        { label: 'Export history CSV', icon: 'download', href: `/api/export/history.csv${qs({ checkId: c.id, range: state.range })}`, download: `history-${c.id}-${state.range}.csv` },
        { label: 'Edit checks', icon: 'edit', href: `#/nodes/${n.id}/edit`, adminOnly: true },
      ], { label: `Options for ${c.name}`, small: true })),
    ));
    const stats = h('div', { class: 'check-stats' },
      stat(st.lastLatencyMs != null ? fmtMs(st.lastLatencyMs) : '—', c.type === 'ping' ? 'Avg RTT' : c.type === 'http' || c.type === 'keyword' || c.type === 'json' ? 'Response' : 'Latency'),
      stat(st.lastRunAt ? relTime(st.lastRunAt) : '—', 'Last run', st.lastRunAt),
      stat(st.nextRunAt && c.enabled !== false ? relTime(st.nextRunAt) : '—', 'Next run', st.nextRunAt),
      stat(String(st.consecutiveFailures ?? 0), 'Consecutive failures', null, st.consecutiveFailures > 0 ? 'text-down' : ''),
      last?.lossPct != null ? stat(pct(last.lossPct), 'Packet loss', null, last.lossPct > 0 ? 'text-degraded' : '') : null,
      last?.details?.cert ? stat(plural(last.details.cert.daysRemaining, 'day'), 'Cert expires in', null, last.details.cert.daysRemaining <= 14 ? 'text-degraded' : '') : null,
      st.lastChangeAt ? stat(relTime(st.lastChangeAt), `${status[0].toUpperCase()}${status.slice(1)} since`, st.lastChangeAt) : null,
    );
    card.append(stats);
    if (expanded) {
      card.append(resultInspector(last, c));
      const recent = h('div', { style: { marginTop: '16px' } }, h('div', { class: 'section-title' }, 'Recent results'), skeleton({ lines: 3 }));
      card.append(recent);
      loadResults(c).then((rows) => { if (!state.destroyed) replace(recent, h('div', { class: 'section-title' }, 'Recent results'), resultsTable(rows, c)); }).catch((e) => replace(recent, h('div', { class: 'note' }, e.message)));
    }
    return card;
  }

  function stat(value, label, ts, cls = '') {
    return h('div', { class: 'stat' }, h('div', { class: `stat-value mono ${cls}`, title: ts ? dateTime(ts) : '' }, value), h('div', { class: 'stat-label' }, label));
  }

  async function loadResults(c) {
    const rows = await api.get(`/api/checks/${c.id}/results?limit=20`);
    state.results.set(c.id, rows || []);
    return rows || [];
  }

  function resultsTable(rows, c) {
    if (!rows.length) return h('p', { class: 'note' }, 'No results recorded yet.');
    const table = h('table', { class: 'table' }, h('thead', null, h('tr', null, h('th', null, 'Time'), h('th', null, 'Result'), h('th', null, 'Message'), h('th', { class: 'num' }, c.type === 'ping' ? 'Avg RTT' : 'Time'), c.type === 'ping' ? h('th', { class: 'num' }, 'Loss') : h('th', { class: 'num' }, 'Code'))));
    const tb = h('tbody');
    for (const r of rows) {
      const tr = h('tr', { style: { cursor: 'pointer' }, tabindex: 0, title: 'Show details' },
        h('td', { class: 'mono nowrap' }, timeShort(r.ts, { seconds: true }), h('span', { class: 'dim' }, ` · ${relTime(r.ts)}`)),
        h('td', null, statusGlyph(r.status || (r.success ? 'up' : 'down'))),
        h('td', { class: 'muted' }, r.message || r.error || ''),
        h('td', { class: 'num' }, fmtMs(r.latencyMs)),
        c.type === 'ping' ? h('td', { class: 'num' }, pct(r.lossPct)) : h('td', { class: 'num' }, r.details?.statusCode ? String(r.details.statusCode) : '—'));
      const open = () => {
        const next = tr.nextElementSibling;
        if (next && next.classList.contains('detail-row')) { next.remove(); return; }
        tr.after(h('tr', { class: 'detail-row' }, h('td', { colspan: 5, style: { padding: '0 0 12px' } }, resultInspector(r, c, { compact: true }))));
      };
      tr.addEventListener('click', open);
      tr.addEventListener('keydown', (e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); open(); } });
      tb.append(tr);
    }
    table.append(tb);
    return h('div', { class: 'table-wrap' }, table);
  }

  // hostKeyFor maps a hardware check to the machine whose readings it reads.
  // It mirrors model.AgentHostKey and model.URLHostKey on the server.
  function hostKeyFor(c) {
    const cfg = c.config || {};
    if (cfg.hostSource === 'agent' && cfg.agentId) return `agent:${cfg.agentId}`;
    if (cfg.hostSource === 'url') return `url:${c.id}`;
    return 'local';
  }

  /* ---------- Hardware ---------- */
  // Panels are kept across reloads and keyed by machine: rebuilding them would
  // throw away their charts and the range the reader had chosen.
  function renderHardware() {
    const systems = (state.node.checks || []).filter((x) => x.type === 'system');
    const wanted = new Map();
    for (const c of systems) {
      wanted.set(hostKeyFor(c), systems.length > 1 ? c.name : 'Hardware');
    }
    for (const [key, panel] of state.hardware) {
      if (!wanted.has(key)) { panel.destroy(); panel.el.remove(); state.hardware.delete(key); }
    }
    for (const [key, name] of wanted) {
      if (state.hardware.has(key)) continue;
      const panel = hardwarePanel(key, { title: name || 'Hardware' });
      state.hardware.set(key, panel);
      hardwareEl.append(panel.el);
    }
  }

  function destroyHardware() {
    for (const panel of state.hardware.values()) panel.destroy();
    state.hardware.clear();
    clear(hardwareEl);
  }

  /* ---------- Charts ---------- */
  async function loadHistory() {
    const n = state.node;
    const checks = (n.checks || []);
    const ids = checks.map((c) => c.id);
    // Build the card's frame the first time and leave it alone afterwards.
    if (!chartsEl.firstChild) {
      chartsEl.append(h('div', { class: 'card-head' }, h('h2', null, 'History'), historyChips), historyBody);
    }
    replace(historyChips, rangeChips(state.range, (r) => { state.range = r; loadHistory(); }));
    if (!ids.length) { clearCharts(); replace(historyBody, h('p', { class: 'note' }, 'Charts appear once this node has checks.')); return; }
    // Only a first load has nothing to show. On every later pass the charts
    // that are already up stay up, at full opacity, until the new data has
    // arrived — a skeleton here is a pale block where a dark chart was, which
    // is what read as a flash on every live update.
    if (!historyBody.firstChild) historyBody.append(skeleton({ height: 220 }));
    let series;
    try { series = await getHistoryMulti(ids, state.range); } catch (e) { clearCharts(); replace(historyBody, h('p', { class: 'note' }, 'Could not load history: ' + e.message)); return; }
    if (state.destroyed) return;
    state.history = series;
    const list = Array.isArray(series) ? series : [series];
    const from = list[0]?.from, to = list[0]?.to, bucket = list[0]?.bucketSeconds || 0;
    const latencySeries = list.filter((hs) => (hs.points || []).some((p) => p.avgMs != null));
    // A hardware check measures a machine rather than a round trip, so it is
    // left out of the latency chart — its readings are in the hardware panel.
    const timed = ids.filter((id) => checks.find((c) => c.id === id)?.type !== 'system');
    const pings = list.filter((hs) => hs.checkType === 'ping');

    const latData = () => ({ series: latencySeries.map((hs, i) => ({ ...toSeries(hs, 'avg', SERIES_COLORS[i % SERIES_COLORS.length]), name: hs.checkName })), from, to, bucketSeconds: bucket });
    const lossData = () => ({ series: pings.map((hs, i) => ({ ...toSeries(hs, 'loss', SERIES_COLORS[i % SERIES_COLORS.length]), name: hs.checkName })), from, to, bucketSeconds: bucket });

    // Which charts the card holds, and with which series. While that is
    // unchanged the existing canvases are handed the new points and redraw
    // themselves; a new canvas would start life blank and unsized, which is
    // the other half of the flash.
    const shape = JSON.stringify([state.range, timed.length > 0, latencySeries.map((hs) => hs.checkName), pings.map((hs) => hs.checkName)]);
    if (shape === state.historyShape && state.uptimeEl && historyBody.contains(state.uptimeEl)) {
      state.latChart?.setData(latData());
      state.lossChart?.setData(lossData());
      replace(state.uptimeEl, ...uptimeContent(list));
      return;
    }

    // The shape did change, so the card is rebuilt — but off-screen and in one
    // go, and only now that the data is in hand.
    clearCharts();
    const built = [];
    if (timed.length) {
      const latHost = h('div', null);
      const latChart = new LineChart(latHost, { unit: 'ms', height: 240, ariaLabel: 'Latency history', title: `${n.name} — latency (${state.range})` });
      state.charts.push(latChart); state.latChart = latChart;
      latChart.setData(latData());
      built.push(chartSection('Latency / response time', latHost, () => latChart.exportPNG(`${slug(n.name)}-latency-${state.range}.png`), timed));
    }
    if (pings.length) {
      const lossHost = h('div', null);
      const lossChart = new LineChart(lossHost, { unit: '%', height: 160, yMin: 0, yMax: 100, ariaLabel: 'Packet loss history', title: `${n.name} — packet loss (${state.range})` });
      state.charts.push(lossChart); state.lossChart = lossChart;
      lossChart.setData(lossData());
      built.push(chartSection('Packet loss', lossHost, () => lossChart.exportPNG(`${slug(n.name)}-loss-${state.range}.png`), pings.map((p) => p.checkId)));
    }
    state.uptimeEl = h('div', null, ...uptimeContent(list));
    built.push(state.uptimeEl);
    replace(historyBody, built);
    state.historyShape = shape;
  }

  /** The availability bars under the charts: cheap, synchronous DOM, so they
   *  are simply written afresh each time. */
  function uptimeContent(list) {
    const out = [h('div', { class: 'section-title' }, `Availability — ${state.range}`)];
    for (const hs of list) {
      const avail = hs.summary?.availability;
      const cls = avail == null ? '' : avail >= 99.9 ? 'text-up' : avail >= 95 ? 'text-degraded' : 'text-down';
      out.push(h('div', { class: 'uptime-row' }, h('div', { class: 'uptime-name' }, hs.checkName, h('div', { class: 'sub' }, `${hs.summary?.count ?? 0} samples · ${hs.summary?.failures ?? 0} failures`)), uptimeBar(hs.points, { bucketSeconds: hs.bucketSeconds, from: hs.from, to: hs.to }), h('div', { class: `uptime-pct ${cls}` }, pct(avail, 2))));
    }
    out.push(uptimeLegend());
    return out;
  }

  function chartSection(title, host, onExportPng, ids) {
    const csvBtn = h('button', { class: 'btn btn-sm', type: 'button', onclick: () => showMenu(csvBtn, ids.map((cid) => { const c = state.node.checks.find((x) => x.id === cid); return { label: `CSV — ${c ? c.name : cid}`, icon: 'download', href: `/api/export/history.csv${qs({ checkId: cid, range: state.range })}`, download: `history-${cid}-${state.range}.csv` }; })) }, icon('download'), 'Export CSV');
    return h('div', null,
      h('div', { class: 'row-between', style: { marginBottom: '8px' } }, h('div', { class: 'section-title', style: { marginBottom: 0 } }, title), h('div', { class: 'btn-group' }, h('button', { class: 'btn btn-sm', type: 'button', onclick: onExportPng }, icon('image'), 'Export PNG'), csvBtn)),
      host);
  }
  function clearCharts() {
    state.charts.forEach((c) => c.destroy()); state.charts = [];
    state.latChart = null; state.lossChart = null; state.uptimeEl = null; state.historyShape = '';
  }
  const slug = (s) => String(s).toLowerCase().replace(/[^\w]+/g, '-');

  /* ---------- Events ---------- */
  function renderEvents() {
    clear(eventsEl);
    eventsEl.append(h('div', { class: 'card-head' }, h('h2', null, 'Events'), h('div', { class: 'btn-group' }, h('a', { class: 'btn btn-sm', href: `#/incidents?nodeId=${id}` }, 'Timeline'), h('a', { class: 'btn btn-sm', href: `#/audit/events?nodeId=${id}` }, icon('audit'), 'Audit log'))));
    if (!state.events.length) { eventsEl.append(h('p', { class: 'note' }, 'No events recorded for this node yet.')); return; }
    const list = h('div', { class: 'event-rows' });
    for (const ev of state.events) list.append(eventRow(ev, { showNode: false }));
    eventsEl.append(list);
  }

  /* ---------- Actions ---------- */
  async function runAll() {
    try { const rs = await api.post(`/api/nodes/${id}/run`); toast(`Ran ${plural(rs?.length ?? 0, 'check')}`, { kind: 'success' }); await load({ quiet: true }); } catch (e) { toast(e.message, { kind: 'error' }); }
  }
  async function runCheck(c, btn) {
    btn.disabled = true;
    try { const r = await api.post(`/api/checks/${c.id}/run`); toast(`${c.name}: ${r.message || (r.success ? 'succeeded' : 'failed')}`, { kind: r.success ? 'success' : 'error' }); state.expanded.add(c.id); await load({ quiet: true }); } catch (e) { toast(e.message, { kind: 'error' }); btn.disabled = false; }
  }
  async function setEnabled(enabled) {
    try { await api.post(`/api/nodes/${id}/enable`, { enabled }); toast(enabled ? 'Node enabled' : 'Node disabled', { kind: 'success' }); await load({ quiet: true }); } catch (e) { toast(e.message, { kind: 'error' }); }
  }
  async function setCheckEnabled(c, enabled) {
    try { await api.post(`/api/checks/${c.id}/enable`, { enabled }); toast(`${c.name} ${enabled ? 'enabled' : 'disabled'}`, { kind: 'success' }); await load({ quiet: true }); } catch (e) { toast(e.message, { kind: 'error' }); }
  }
  async function silence(minutes) {
    const checks = state.node.checks || [];
    try {
      await Promise.all(checks.map((c) => api.post(`/api/checks/${c.id}/silence`, { minutes })));
      toast(minutes ? `Alerts silenced for ${interval(minutes * 60)}` : 'Alerts unsilenced', { kind: 'success' });
      await load({ quiet: true });
    } catch (e) { toast(e.message, { kind: 'error' }); }
  }
  async function silenceCheck(c, minutes) {
    try { await api.post(`/api/checks/${c.id}/silence`, { minutes }); toast(minutes ? `${c.name} silenced for ${interval(minutes * 60)}` : `${c.name} unsilenced`, { kind: 'success' }); await load({ quiet: true }); } catch (e) { toast(e.message, { kind: 'error' }); }
  }
  async function duplicate() {
    try { const copy = await api.post(`/api/nodes/${id}/duplicate`); toast(`Created "${copy.name}"`, { kind: 'success' }); ctx.navigate(`/nodes/${copy.id}/edit`); } catch (e) { toast(e.message, { kind: 'error' }); }
  }
  async function remove() {
    const n = state.node;
    const ok = await confirmDialog({ title: `Delete ${n.name}?`, message: 'The node, its checks and all recorded history will be removed. This cannot be undone.', confirmLabel: 'Delete node', danger: true });
    if (!ok) return;
    try { await api.del(`/api/nodes/${id}`); toast(`${n.name} deleted`, { kind: 'success' }); ctx.navigate('/nodes'); } catch (e) { toast(e.message, { kind: 'error' }); }
  }

  try { await load(); } catch (e) {
    replace(root, h('div', { class: 'card' }, emptyState({ icon: 'alert', title: e.status === 404 ? 'Node not found' : 'Could not load this node', text: e.message, actions: h('a', { class: 'btn', href: '#/nodes' }, 'Back to nodes') })));
    ctx.setTitle('Node');
    return { destroy() { state.destroyed = true; } };
  }

  return {
    refresh: () => load({ quiet: true }),
    themeChanged() { for (const panel of state.hardware.values()) panel.themeChanged(); },
    destroy() { state.destroyed = true; clearCharts(); destroyHardware(); },
  };
}

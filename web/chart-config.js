// Shared "custom chart" configuration: the editor form and the renderer are
// used by the Charts tab and by the dashboard's chart widgets, so a chart
// configured on one can be pinned to the other unchanged.

import { getHistoryMulti, getHistoryAuto } from './api.js';
import { h, icon, clear, replace, field, textInput, numberInput, selectInput, checkbox, checkMultiSelect, emptyState, uid } from './components.js';
import { LineChart, toSeries, uptimeBar, uptimeLegend, seriesColors, CHART_STYLES } from './charts.js';
import { rangeLabel, RANGES, ms as fmtMs, pct } from './fmt.js';

export const METRICS = [
  { value: 'avg', label: 'Latency / response time (average)', unit: 'ms' },
  { value: 'min', label: 'Latency (minimum)', unit: 'ms' },
  { value: 'max', label: 'Latency (maximum)', unit: 'ms' },
  { value: 'jitter', label: 'Jitter', unit: 'ms' },
  { value: 'loss', label: 'Packet loss', unit: '%' },
  { value: 'availability', label: 'Availability', unit: '%' },
];
export function metricMeta(m) { return METRICS.find((x) => x.value === m) || METRICS[0]; }

/** A complete config with defaults filled in. */
export function normalizeChartConfig(cfg = {}) {
  const c = { ...(cfg || {}) };
  c.checkIds = (c.checkIds || []).map(Number).filter(Boolean);
  c.metric = METRICS.some((m) => m.value === c.metric) ? c.metric : 'avg';
  c.range = RANGES.includes(c.range) ? c.range : '24h';
  c.style = CHART_STYLES.some((s) => s.value === c.style) ? c.style : 'area';
  c.smooth = !!c.smooth;
  c.points = !!c.points;
  c.lineWidth = Math.min(5, Math.max(0.5, Number(c.lineWidth) || 1.75));
  c.shadeFailures = c.shadeFailures !== false;
  c.legend = c.legend !== false;
  c.grid = c.grid !== false;
  c.yMin = c.yMin === '' || c.yMin == null || isNaN(Number(c.yMin)) ? null : Number(c.yMin);
  c.yMax = c.yMax === '' || c.yMax == null || isNaN(Number(c.yMax)) ? null : Number(c.yMax);
  c.threshold = c.threshold === '' || c.threshold == null || isNaN(Number(c.threshold)) ? null : Number(c.threshold);
  c.height = Math.min(800, Math.max(120, Number(c.height) || 260));
  c.split = !!c.split;
  c.uptime = !!c.uptime;
  c.colors = c.colors && typeof c.colors === 'object' ? { ...c.colors } : {};
  return c;
}

export function describeChartConfig(cfg) {
  const c = normalizeChartConfig(cfg);
  const m = metricMeta(c.metric);
  const parts = [m.label.split(' (')[0], rangeLabel(c.range), c.checkIds.length ? `${c.checkIds.length} check${c.checkIds.length === 1 ? '' : 's'}` : 'auto checks', c.style];
  return parts.join(' · ');
}

/**
 * Editor form for a chart config. Returns an element with `.value` (the config
 * read from the controls) and `onChange` callbacks fired live.
 */
export function chartConfigEditor(cfg, { nodes = [], onChange, compact = false } = {}) {
  const c = normalizeChartConfig(cfg);
  const emit = () => { onChange && onChange(wrap.value); };
  const metric = selectInput({ options: METRICS, value: c.metric, onchange: () => { updateFilter(); emit(); } });
  const range = selectInput({ options: RANGES.map((r) => ({ value: r, label: rangeLabel(r) })), value: c.range, onchange: emit });
  const style = selectInput({ options: CHART_STYLES, value: c.style, onchange: emit });
  const width = h('input', { type: 'range', min: 0.5, max: 5, step: 0.25, value: c.lineWidth, oninput: emit, 'aria-label': 'Line thickness' });
  const height = numberInput({ value: c.height, min: 120, max: 800, step: 20, oninput: emit });
  const yMin = numberInput({ value: c.yMin ?? '', placeholder: 'auto', oninput: emit });
  const yMax = numberInput({ value: c.yMax ?? '', placeholder: 'auto', oninput: emit });
  const threshold = numberInput({ value: c.threshold ?? '', placeholder: 'none', oninput: emit });
  const smooth = checkbox({ label: 'Smooth curves', checked: c.smooth, onChange: emit });
  const points = checkbox({ label: 'Show data points', checked: c.points, onChange: emit });
  const shade = checkbox({ label: 'Shade failures', checked: c.shadeFailures, onChange: emit });
  const legend = checkbox({ label: 'Show legend', checked: c.legend, onChange: emit });
  const grid = checkbox({ label: 'Show grid lines', checked: c.grid, onChange: emit });
  const split = checkbox({ label: 'One chart per check', checked: c.split, onChange: emit });
  const uptime = checkbox({ label: 'Show availability bars underneath', checked: c.uptime, onChange: emit });
  let filter = null;
  const msWrap = h('div');
  let ms = null;
  const colorsWrap = h('div', { class: 'series-colors' });
  const colors = { ...c.colors };
  function updateFilter() {
    filter = metric.value === 'loss' ? (x) => x.type === 'ping' : null;
    const selected = ms ? ms.value : c.checkIds;
    ms = checkMultiSelect(nodes, selected, { filterType: filter, onChange: () => { renderColors(); emit(); } });
    replace(msWrap, ms);
    renderColors();
  }
  function renderColors() {
    clear(colorsWrap);
    const ids = ms ? ms.value : c.checkIds;
    if (!ids.length) { colorsWrap.append(h('div', { class: 'note' }, 'Colours follow the accent palette. Pick checks to set one per line.')); return; }
    const palette = seriesColors();
    ids.forEach((id, i) => {
      let name = `Check ${id}`;
      for (const n of nodes) for (const ch of n.checks || []) if (Number(ch.id) === Number(id)) name = `${n.name} › ${ch.name}`;
      const input = h('input', { type: 'color', value: colors[id] || palette[i % palette.length], 'aria-label': `Colour for ${name}`, oninput: () => { colors[id] = input.value; emit(); } });
      colorsWrap.append(h('div', { class: 'series-color' }, input, h('span', { class: 'truncate' }, name)));
    });
  }
  updateFilter();
  const wrap = h('div', { class: 'stack-sm' },
    field({ label: 'Metric', input: metric }),
    h('div', { class: 'form-grid' }, field({ label: 'Time range', input: range }), field({ label: 'Style', input: style })),
    field({ label: 'Checks', input: msWrap, help: 'Leave all unticked to let GWatch pick the most important checks.' }),
    field({ label: 'Line colours', input: colorsWrap }),
    h('div', { class: 'form-grid' },
      field({ label: 'Line thickness', input: width }),
      compact ? null : field({ label: 'Height (px)', input: height }),
      field({ label: 'Y axis minimum', input: yMin }),
      field({ label: 'Y axis maximum', input: yMax }),
      field({ label: 'Threshold line', input: threshold, help: 'Draws a dashed warning line at this value.' }),
    ),
    h('div', { class: 'stack-sm', style: { gap: '6px' } }, smooth, points, shade, legend, grid, split, uptime),
  );
  Object.defineProperty(wrap, 'value', { get: () => normalizeChartConfig({
    checkIds: ms.value, metric: metric.value, range: range.value, style: style.value, lineWidth: width.value, height: height.value,
    yMin: yMin.value, yMax: yMax.value, threshold: threshold.value, smooth: smooth.input.checked, points: points.input.checked, shadeFailures: shade.input.checked,
    legend: legend.input.checked, grid: grid.input.checked, split: split.input.checked, uptime: uptime.input.checked, colors,
  }) });
  return wrap;
}

/**
 * Render a configured chart into host. Returns { charts, refresh, destroy, setRange }.
 * fetch(ids, range) may be provided for caching; defaults to the API.
 */
export function renderConfiguredChart(host, cfg, { title = 'Chart', fetch: fetchFn, fill = false, onData } = {}) {
  const c = normalizeChartConfig(cfg);
  const m = metricMeta(c.metric);
  const charts = [];
  const statsEl = h('div', { class: 'chart-stats' });
  const body = h('div', { class: 'chart-body', style: fill ? { flex: '1', minHeight: '0', display: 'flex', flexDirection: 'column' } : null });
  clear(host);
  host.append(body);
  let destroyed = false;
  const load = async () => {
    const fn = fetchFn || ((ids, range) => (ids.length ? getHistoryMulti(ids, range) : getHistoryAuto(range)));
    let list;
    try { list = await fn(c.checkIds, c.range); } catch (e) { replace(body, h('div', { class: 'note' }, 'Could not load history: ' + e.message)); return; }
    if (destroyed) return;
    list = Array.isArray(list) ? list : [list];
    if (c.metric === 'loss') list = list.filter((hs) => hs.checkType === 'ping');
    if (!list.length) { replace(body, emptyState({ icon: 'activity', title: 'Nothing to chart yet', text: 'Pick checks with history, or add a node with a ping or HTTP check.', compact: true })); return; }
    onData && onData(list);
    clear(body);
    charts.forEach((ch) => ch.destroy()); charts.length = 0;
    const palette = seriesColors();
    const from = list[0]?.from, to = list[0]?.to, bucket = list[0]?.bucketSeconds || 0;
    const mkSeries = (hs, i) => ({ ...toSeries(hs, c.metric, c.colors[hs.checkId] || palette[i % palette.length]) });
    const opts = (single) => ({
      unit: m.unit, yMin: c.yMin ?? (c.metric === 'loss' || c.metric === 'availability' ? 0 : null), yMax: c.yMax ?? (c.metric === 'loss' || c.metric === 'availability' ? 100 : null),
      style: c.style, smooth: c.smooth, points: c.points, lineWidth: c.lineWidth, shadeFailures: c.shadeFailures, legend: c.legend && !single, grid: c.grid, threshold: c.threshold,
      height: fill ? null : c.height, title, ariaLabel: `${title} chart`, alwaysLegend: false,
    });
    const groups = c.split ? list.map((hs) => [hs]) : [list];
    groups.forEach((group, gi) => {
      const hostEl = h('div', { class: 'chart-host', style: fill ? { flex: '1', minHeight: '0', display: 'flex', flexDirection: 'column' } : { marginBottom: c.split ? '12px' : '0' } });
      if (c.split) hostEl.append(h('div', { class: 'section-title', style: { marginBottom: '4px' } }, `${group[0].nodeName || ''} › ${group[0].checkName}`));
      body.append(hostEl);
      const chart = new LineChart(hostEl, opts(c.split));
      charts.push(chart);
      chart.setData({ series: group.map((hs, i) => mkSeries(hs, c.split ? gi + i : i)), from, to, bucketSeconds: bucket });
    });
    if (c.uptime) {
      const up = h('div', { style: { marginTop: '8px' } });
      for (const hs of list) {
        const avail = hs.summary?.availability;
        const cls = avail == null ? '' : avail >= 99.9 ? 'text-up' : avail >= 95 ? 'text-degraded' : 'text-down';
        up.append(h('div', { class: 'uptime-row' }, h('div', { class: 'uptime-name truncate' }, hs.nodeName || '', h('div', { class: 'sub' }, hs.checkName)), uptimeBar(hs.points, { bucketSeconds: hs.bucketSeconds, from: hs.from, to: hs.to }), h('div', { class: `uptime-pct ${cls}` }, pct(avail, 2))));
      }
      up.append(uptimeLegend());
      body.append(up);
    }
    if (!fill) {
      clear(statsEl);
      for (const hs of list) {
        const s = hs.summary || {};
        statsEl.append(h('div', { class: 'stat' }, h('div', { class: 'stat-value mono' }, m.unit === '%' ? pct(c.metric === 'loss' ? avgLoss(hs) : s.availability, 2) : fmtMs(c.metric === 'min' ? s.minMs : c.metric === 'max' ? s.maxMs : s.avgMs)), h('div', { class: 'stat-label' }, `${hs.nodeName ? hs.nodeName + ' › ' : ''}${hs.checkName}`)));
      }
      body.append(statsEl);
    }
  };
  load();
  return {
    charts,
    refresh: load,
    destroy() { destroyed = true; charts.forEach((ch) => ch.destroy()); charts.length = 0; },
    exportPNG(name) { return Promise.all(charts.map((ch, i) => ch.exportPNG(charts.length > 1 ? name.replace(/\.png$/, `-${i + 1}.png`) : name))); },
  };
}

function avgLoss(hs) {
  const vals = (hs.points || []).map((p) => p.lossPct).filter((v) => v != null);
  return vals.length ? vals.reduce((a, b) => a + b, 0) / vals.length : null;
}

export function newChartId() { return uid('chart'); }
export { icon, textInput };

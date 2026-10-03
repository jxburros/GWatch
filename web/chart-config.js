// Shared "custom chart" configuration: the editor form and the renderer are
// used by the Charts tab and by the dashboard's chart widgets, so a chart
// configured on one can be pinned to the other unchanged.
//
// A chart is a list of series, each one measurement of one check:
//
//   { series: [{ checkId: 4, metric: 'avg' }, { checkId: 9, metric: 'net:eth0.rx', named: true }], range, style, … }
//
// `metric` is either one of GWatch's own measurements of a check (METRICS
// below: latency, jitter, packet loss, availability — every check has some of
// these) or, with `named: true`, a metric the check measures for itself: a
// hardware check's processor, memory, disks and interfaces, an SNMP check's
// OIDs, the value a json check records (#68). Any mix of them, from any
// checks, can share a chart (#73); units that differ go on a left and a right
// axis, and more than two units are drawn as more than one chart.
//
// Charts saved before series existed carry `{ metric, checkIds }` — one
// latency-family metric across several checks. normalizeChartConfig reads
// that as one series per check, so an old saved chart or dashboard widget
// opens exactly as it was, and no server migration is needed: the service
// stores a chart's config as opaque JSON. The normalized config still writes
// `checkIds` and `metric` alongside `series` (the checks involved, and the
// metric automatic charts use), which is also what an older GWatch reading a
// newer config falls back on.

//
// Timestacked charts (`timestack: { period, layers }`), what Nagios XI calls a
// Timestacked Performance Graph: one metric laid over itself — the last 24
// hours, the 24 before that, the 24 before those — on a single time axis, so
// today can be read against yesterday and last week at a glance.

import { api, getHistoryMulti, getHistoryAuto, getHistoryMetric, qs } from './api.js';
import { h, icon, clear, replace, field, textInput, numberInput, selectInput, checkbox, emptyState, uid } from './components.js';
import { LineChart, toSeries, uptimeBar, uptimeLegend, seriesColors, CHART_STYLES, groupByUnits } from './charts.js';
import { rangeLabel, RANGES, pct, plural, unitValue, metricLabel, metricUnit, metricFamily } from './fmt.js';

export const METRICS = [
  { value: 'avg', label: 'Latency / response time (average)', unit: 'ms' },
  { value: 'min', label: 'Latency (minimum)', unit: 'ms' },
  { value: 'max', label: 'Latency (maximum)', unit: 'ms' },
  { value: 'jitter', label: 'Jitter', unit: 'ms' },
  { value: 'loss', label: 'Packet loss', unit: '%' },
  { value: 'availability', label: 'Availability', unit: '%' },
];
export function metricMeta(m) { return METRICS.find((x) => x.value === m) || METRICS[0]; }
const BUILTIN = new Set(METRICS.map((m) => m.value));

const HOUR_MS = 3600e3, DAY_MS = 86400e3;
/**
 * The windows a timestacked chart can lay over each other. `range` is the
 * history range each layer is read as; `unit`, `per` and `each` name how far
 * back a layer is ("2 days earlier").
 */
export const TIMESTACK_PERIODS = [
  { value: '1h', label: '1 hour', ms: HOUR_MS, unit: 'hour', each: 1 },
  { value: '24h', label: '24 hours (day)', ms: DAY_MS, unit: 'day', each: 1 },
  { value: '3d', label: '3 days', ms: 3 * DAY_MS, unit: 'day', each: 3 },
  { value: '7d', label: '7 days (week)', ms: 7 * DAY_MS, unit: 'week', each: 1 },
  { value: '30d', label: '30 days (month)', ms: 30 * DAY_MS, unit: 'day', each: 30 },
];
export const TIMESTACK_MAX_LAYERS = 8;
export function timestackPeriod(v) { return TIMESTACK_PERIODS.find((p) => p.value === v) || TIMESTACK_PERIODS[1]; }

/** What layer `i` of a timestack is called: "Last 24 hours", "Yesterday",
 *  "2 days earlier", "Week before", "60 days earlier". */
export function timestackLabel(period, i) {
  const p = timestackPeriod(period);
  if (i === 0) return `Last ${p.label.replace(/ \(.*\)$/, '')}`;
  if (p.value === '24h' && i === 1) return 'Yesterday';
  if (p.value === '7d' && i === 1) return 'Week before';
  const n = i * p.each;
  return `${n} ${p.unit}${n === 1 ? '' : 's'} earlier`;
}

/** A timestack setting with its defaults, or null when the chart is not one. */
export function normalizeTimestack(ts) {
  if (!ts || typeof ts !== 'object' || ts.enabled === false) return null;
  const period = TIMESTACK_PERIODS.some((p) => p.value === ts.period) ? ts.period : '24h';
  const layers = Math.min(TIMESTACK_MAX_LAYERS, Math.max(2, Math.round(Number(ts.layers)) || 4));
  return { period, layers };
}

/** True for one of GWatch's own measurements of a check, false for a metric
 *  the check names itself (a hardware reading, an SNMP OID …). */
export function isBuiltinMetric(spec) { return !!spec && !spec.named && BUILTIN.has(spec.metric); }

/** A series' identity: which check, which metric. It keys the colour map and
 *  the editor's selection. A named metric is marked so that an SNMP OID
 *  someone happened to call "avg" is never mistaken for the latency. */
export function seriesKey(spec) { return `${Number(spec.checkId)}:${isBuiltinMetric(spec) ? '' : 'm:'}${spec.metric}`; }

/** A clean list of series: numeric check ids, known shapes, no duplicates. */
export function normalizeSeries(list) {
  const out = [];
  const seen = new Set();
  for (const raw of Array.isArray(list) ? list : []) {
    if (!raw || typeof raw !== 'object') continue;
    const checkId = Number(raw.checkId);
    if (!Number.isFinite(checkId) || checkId <= 0) continue;
    const metric = String(raw.metric ?? '').trim() || 'avg';
    const spec = !raw.named && BUILTIN.has(metric) ? { checkId, metric } : { checkId, metric, named: true };
    const key = seriesKey(spec);
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(spec);
  }
  return out;
}

/** A complete config with defaults filled in. */
export function normalizeChartConfig(cfg = {}) {
  const c = { ...(cfg || {}) };
  // `metric` is now only what an automatic chart (no series picked) plots.
  c.metric = BUILTIN.has(c.metric) ? c.metric : 'avg';
  c.colors = c.colors && typeof c.colors === 'object' ? { ...c.colors } : {};
  if (Array.isArray(c.series)) {
    c.series = normalizeSeries(c.series);
  } else {
    // A chart from before series: one metric across a list of checks. Its
    // colours were keyed by check id; they move to the series they belong to.
    const ids = (c.checkIds || []).map(Number).filter((n) => Number.isFinite(n) && n > 0);
    c.series = normalizeSeries(ids.map((checkId) => ({ checkId, metric: c.metric })));
    for (const s of c.series) if (c.colors[s.checkId] && !c.colors[seriesKey(s)]) c.colors[seriesKey(s)] = c.colors[s.checkId];
  }
  c.checkIds = [...new Set(c.series.map((s) => s.checkId))];
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
  c.timestack = normalizeTimestack(c.timestack);
  return c;
}

/* ---------- What a check can be charted by ---------- */

/**
 * The named metrics a check measures, as [{ name, unit, label, family? }]:
 * one per OID for an SNMP check, the recorded value for a json check that
 * records one (#55), and for a hardware check every reading its latest
 * result carried — a machine's disks, interfaces and devices are only known
 * from what it last reported (#60), and the service accepts any key that
 * result carried. It mirrors model.Check.MetricUnits on the server, which is
 * what decides whether /api/history will serve a name. `last` is the
 * check's latest result, from a node's `lastResults`.
 */
export function namedMetrics(c, last) {
  const cfg = c?.config || {};
  if (c?.type === 'snmp') return (cfg.snmpOids || []).filter((o) => o.name).map((o) => ({ name: o.name, unit: o.unit || '', label: o.name }));
  if (c?.type === 'json' && cfg.jsonRecord) { const name = (cfg.jsonMetric || '').trim() || 'value'; return [{ name, unit: (cfg.jsonUnit || '').trim(), label: name }]; }
  if (c?.type === 'system') {
    const rows = last?.details?.metricResults || [];
    const keys = rows.length ? rows.map((r) => r.key) : Object.keys(last?.metrics || {});
    return keys.map((key) => ({ name: key, unit: metricUnit(key), label: rows.find((r) => r.key === key)?.label || metricLabel(key), family: metricFamily(key).family }));
  }
  return [];
}

/** What GWatch's own measurements are called for a check of this type: a
 *  ping has a latency, everything else a response time. */
function builtinLabel(metric, type) {
  const what = type === 'ping' ? 'Latency' : 'Response time';
  switch (metric) {
    case 'avg': return `${what} (average)`;
    case 'min': return `${what} (minimum)`;
    case 'max': return `${what} (maximum)`;
    default: return metricMeta(metric).label;
  }
}

/**
 * Every metric a check can be charted by, as [{ metric, named?, label, unit }],
 * in the order the editor lists them. A ping has latency, jitter and packet
 * loss; other round trips have a response time; a hardware check measures a
 * machine rather than a round trip, so it has its readings and availability
 * but no latency; an SNMP or recording json check has its own values first.
 */
export function checkMetricOptions(c, last) {
  const own = namedMetrics(c, last).map((m) => ({ metric: m.name, named: true, label: m.label || m.name, unit: m.unit }));
  const builtin = (list) => list.map((metric) => ({ metric, label: builtinLabel(metric, c.type), unit: metricMeta(metric).unit }));
  if (c.type === 'ping') return builtin(['avg', 'min', 'max', 'jitter', 'loss', 'availability']);
  if (c.type === 'system') return [...own, ...builtin(['availability'])];
  return [...own, ...builtin(['avg', 'min', 'max', 'availability'])];
}

/** A series' metric as a person reads it, for a check of `type`. */
export function seriesMetricLabel(spec, type) {
  if (isBuiltinMetric(spec)) return builtinLabel(spec.metric, type);
  return type === 'system' ? metricLabel(spec.metric) : spec.metric;
}

/** One short phrase for what a chart plots: its metric when every series
 *  plots the same one, otherwise how many different metrics it mixes. */
export function chartMetricSummary(cfg) {
  const c = normalizeChartConfig(cfg);
  if (!c.series.length) return metricMeta(c.metric).label.split(' (')[0];
  const kinds = [...new Set(c.series.map((s) => (isBuiltinMetric(s) ? s.metric : `m:${s.metric}`)))];
  if (kinds.length > 1) return `${kinds.length} metrics`;
  const s = c.series[0];
  return isBuiltinMetric(s) ? metricMeta(s.metric).label.split(' (')[0] : metricLabel(s.metric);
}

export function describeChartConfig(cfg) {
  const c = normalizeChartConfig(cfg);
  const checks = c.checkIds.length;
  if (c.timestack) return [chartMetricSummary(c), `timestacked ${c.timestack.layers} × ${rangeLabel(c.timestack.period)}`, c.style].join(' · ');
  const parts = [chartMetricSummary(c), rangeLabel(c.range), checks ? `${checks} check${checks === 1 ? '' : 's'}` : 'auto checks', c.style];
  return parts.join(' · ');
}

/* ---------- Editor ---------- */

/**
 * The metric picker (#68, #73): every node's checks, each a disclosure that
 * lists the metrics that check can be charted by as checkboxes. Any number
 * can be ticked across any checks; the order they are ticked in is the order
 * of the series (so the first unit ticked is the left axis). A hardware
 * check's readings come from its latest result, which /api/nodes does not
 * carry, so the first time one is opened its node is fetched (`loadNode`).
 * `.value` is the list of series.
 */
export function metricPicker(nodes, selected = [], { onChange, loadNode = (id) => api.get(`/api/nodes/${id}`) } = {}) {
  const sel = new Map(normalizeSeries(selected).map((s) => [seriesKey(s), s]));
  const lastByCheck = new Map();
  const remember = (n) => { for (const [id, r] of Object.entries(n?.lastResults || {})) lastByCheck.set(Number(id), r); };
  for (const n of nodes || []) remember(n);
  // Nodes whose full record has been asked for, and those it has come back for.
  const loading = new Map();
  const settled = new Set();
  const fire = () => { updateCount(); onChange && onChange([...sel.values()]); };

  const list = h('div', { class: 'metric-picker' });
  const countEl = h('span', { class: 'metric-picked', 'aria-live': 'polite' });
  const clearBtn = h('button', { type: 'button', class: 'btn btn-sm', onclick: () => { sel.clear(); list.querySelectorAll('input[type="checkbox"]').forEach((cb) => { cb.checked = false; }); badges.forEach((b) => b()); fire(); } }, 'Clear');
  const filter = h('input', { type: 'search', class: 'metric-filter', placeholder: 'Filter by node or check', 'aria-label': 'Filter checks by node or check name', oninput: applyFilter });
  const badges = [];
  let total = 0;

  for (const n of nodes || []) {
    for (const c of n.checks || []) {
      total++;
      const name = `${n.name} › ${c.name}`;
      const opts = h('div', { class: 'metric-options', role: 'group', 'aria-label': `Metrics for ${name}` });
      const badge = h('span', { class: 'metric-count' });
      const det = h('details', { class: 'metric-check', 'data-check': c.id },
        h('summary', null, icon('chevronRight'), h('span', { class: 'truncate', style: { flex: '1', minWidth: '0' } }, name), badge, h('span', { class: 'tag' }, c.type)),
        opts);
      det.dataset.search = name.toLowerCase();
      const picked = () => [...sel.values()].filter((s) => s.checkId === Number(c.id)).length;
      const setBadge = () => { const k = picked(); badge.textContent = k ? `${k} ticked` : ''; };
      badges.push(setBadge);
      setBadge();
      let filled = false;
      const fill = () => {
        filled = true;
        if (c.type === 'system' && !lastByCheck.has(Number(c.id)) && !settled.has(n.id)) {
          if (!loading.has(n.id)) loading.set(n.id, Promise.resolve().then(() => loadNode(n.id)).then(remember, () => {}).then(() => { settled.add(n.id); }));
          replace(opts, h('div', { class: 'note' }, 'Reading this machine’s metrics…'));
          loading.get(n.id).then(fill);
          return;
        }
        const options = checkMetricOptions(c, lastByCheck.get(Number(c.id)));
        // A metric already charted but not in the latest reading (a disk
        // since unplugged) stays listed, so it can still be unticked.
        for (const s of sel.values()) {
          if (s.checkId !== Number(c.id) || options.some((o) => seriesKey({ checkId: c.id, metric: o.metric, named: o.named }) === seriesKey(s))) continue;
          options.push({ metric: s.metric, named: !!s.named, label: seriesMetricLabel(s, c.type), unit: isBuiltinMetric(s) ? metricMeta(s.metric).unit : '' });
        }
        const boxes = options.map((o) => {
          const spec = o.named ? { checkId: Number(c.id), metric: o.metric, named: true } : { checkId: Number(c.id), metric: o.metric };
          const key = seriesKey(spec);
          const cb = checkbox({ label: o.label, checked: sel.has(key), onChange: (v) => { if (v) sel.set(key, spec); else sel.delete(key); setBadge(); fire(); } });
          cb.dataset.metric = o.metric;
          if (o.unit) cb.append(h('span', { class: 'metric-unit' }, o.unit));
          return cb;
        });
        const needsReading = c.type === 'system' && !lastByCheck.has(Number(c.id));
        replace(opts, ...boxes, needsReading ? h('div', { class: 'note' }, 'This machine has not reported any readings yet, so only its availability can be charted.') : null);
      };
      det.addEventListener('toggle', () => { if (det.open && !filled) fill(); });
      if (picked()) { det.open = true; fill(); }
      list.append(det);
    }
  }
  if (!total) list.append(h('div', { class: 'note' }, 'No checks yet.'));

  function applyFilter() {
    const q = filter.value.trim().toLowerCase();
    for (const det of list.querySelectorAll('.metric-check')) det.hidden = !!q && !det.dataset.search.includes(q);
  }
  function updateCount() {
    const k = sel.size;
    countEl.textContent = k ? `${plural(k, 'metric')} ticked` : 'Nothing ticked: GWatch picks the checks';
    clearBtn.disabled = !k;
  }
  updateCount();

  const wrap = h('div', { class: 'stack-sm metric-picker-wrap' },
    total > 6 ? filter : null,
    list,
    h('div', { class: 'row-between' }, countEl, clearBtn));
  Object.defineProperty(wrap, 'value', { get: () => [...sel.values()] });
  return wrap;
}

/**
 * Editor form for a chart config. Returns an element with `.value` (the config
 * read from the controls) and `onChange` callbacks fired live.
 */
export function chartConfigEditor(cfg, { nodes = [], onChange, compact = false, loadNode } = {}) {
  const c = normalizeChartConfig(cfg);
  const emit = () => { onChange && onChange(wrap.value); };
  const autoMetric = selectInput({ options: METRICS, value: c.metric, onchange: emit });
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
  // Timestack: lay the metric over itself, one layer per earlier period.
  const stackOn = checkbox({ label: 'Timestack: overlay this metric against itself over time', checked: !!c.timestack, onChange: () => { syncStack(); emit(); } });
  const stackPeriod = selectInput({ options: TIMESTACK_PERIODS.map((p) => ({ value: p.value, label: p.label })), value: c.timestack?.period || '24h', onchange: emit });
  const stackLayers = numberInput({ value: c.timestack?.layers || 4, min: 2, max: TIMESTACK_MAX_LAYERS, step: 1, oninput: emit });
  const stackFields = h('div', { class: 'form-grid' },
    field({ label: 'Stack each', input: stackPeriod, help: 'The length of one layer: the last 24 hours over the 24 before them, and so on.' }),
    field({ label: 'Layers', input: stackLayers, help: `How many periods to lay over each other (2–${TIMESTACK_MAX_LAYERS}), the current one included.` }));
  const stackNote = h('div', { class: 'help' }, 'A timestacked chart plots the first metric ticked above (or the first check GWatch picks), and replaces the time range.');
  const stackWrap = h('div', { class: 'stack-sm timestack-fields' }, stackFields, stackNote);
  function syncStack() { stackWrap.hidden = !stackOn.input.checked; range.disabled = stackOn.input.checked; }
  syncStack();
  const colorsWrap = h('div', { class: 'series-colors' });
  const colors = { ...c.colors };
  const picker = metricPicker(nodes, c.series, { onChange: () => { renderColors(); syncAuto(); emit(); }, loadNode });
  // Which metric an automatic chart plots only matters while nothing is ticked.
  const autoField = field({ label: 'Metric for automatic checks', input: autoMetric, help: 'Used while no metric is ticked above: GWatch picks the most important checks and charts this for them.' });
  function syncAuto() { autoField.hidden = picker.value.length > 0; }

  function checkName(id) {
    for (const n of nodes) for (const ch of n.checks || []) if (Number(ch.id) === Number(id)) return { name: `${n.name} › ${ch.name}`, type: ch.type };
    return { name: `Check ${id}`, type: '' };
  }
  function renderColors() {
    clear(colorsWrap);
    const list = picker.value;
    if (!list.length) { colorsWrap.append(h('div', { class: 'note' }, 'Colours follow the accent palette. Tick metrics to set one per line.')); return; }
    const palette = seriesColors();
    list.forEach((s, i) => {
      const { name, type } = checkName(s.checkId);
      const label = `${name} — ${seriesMetricLabel(s, type)}`;
      const key = seriesKey(s);
      const input = h('input', { type: 'color', value: colors[key] || colors[s.checkId] || palette[i % palette.length], 'aria-label': `Colour for ${label}`, oninput: () => { colors[key] = input.value; emit(); } });
      colorsWrap.append(h('div', { class: 'series-color' }, input, h('span', { class: 'truncate' }, label)));
    });
  }
  renderColors();
  syncAuto();

  // The picker is a group of groups rather than one control, so it is a
  // fieldset with a legend rather than a label pointing at its first box.
  const pickerField = h('fieldset', { class: 'field metric-fieldset' },
    h('legend', { class: 'field-label' }, 'Metrics'),
    picker,
    h('div', { class: 'help' }, 'Open a check to tick what to chart: latency, packet loss, availability, or a machine’s processor, memory, disks and network. Mix any metrics from any checks. Up to two units share a chart, one on each axis; a third unit starts another chart. Leave everything unticked to let GWatch pick the most important checks.'));
  const wrap = h('div', { class: 'stack-sm' },
    pickerField,
    autoField,
    h('div', { class: 'form-grid' }, field({ label: 'Time range', input: range }), field({ label: 'Style', input: style })),
    h('div', { class: 'stack-sm' }, stackOn, stackWrap),
    field({ label: 'Line colours', input: colorsWrap }),
    h('div', { class: 'form-grid' },
      field({ label: 'Line thickness', input: width }),
      compact ? null : field({ label: 'Height (px)', input: height }),
      field({ label: 'Y axis minimum', input: yMin, help: 'In the unit of the first metric ticked (the left axis).' }),
      field({ label: 'Y axis maximum', input: yMax }),
      field({ label: 'Threshold line', input: threshold, help: 'Draws a dashed warning line at this value on the left axis.' }),
    ),
    h('div', { class: 'stack-sm', style: { gap: '6px' } }, smooth, points, shade, legend, grid, split, uptime),
  );
  Object.defineProperty(wrap, 'value', { get: () => normalizeChartConfig({
    series: picker.value, metric: autoMetric.value, range: range.value, style: style.value, lineWidth: width.value, height: height.value,
    yMin: yMin.value, yMax: yMax.value, threshold: threshold.value, smooth: smooth.input.checked, points: points.input.checked, shadeFailures: shade.input.checked,
    legend: legend.input.checked, grid: grid.input.checked, split: split.input.checked, uptime: uptime.input.checked, colors,
    timestack: stackOn.input.checked ? { period: stackPeriod.value, layers: stackLayers.value } : null,
  }) });
  return wrap;
}

/* ---------- Fetching ---------- */

/**
 * The default history fetch. With no metric it is the latency-family history
 * of the checks (or of the checks GWatch picks, for none); with a metric it
 * is that one named metric of the one check given. A named metric is asked
 * for check by check: /api/history/multi applies one metric to every id and
 * refuses the lot when one check does not measure it.
 */
export function historyFetch(ids, range, metric, end) {
  if (metric) return getHistoryMetric(ids[0], range, metric, end);
  return ids.length ? getHistoryMulti(ids, range, end) : getHistoryAuto(range);
}

/** historyFetch behind a cache (a Map the caller owns and clears on refresh). */
export function cachedHistoryFetch(cache) {
  return (ids, range, metric, end) => {
    const key = `${ids.join(',')}|${range}|${metric || ''}|${end ?? ''}`;
    if (!cache.has(key)) {
      const p = historyFetch(ids, range, metric, end);
      // A failed request is not kept, so the next refresh asks again.
      p.catch(() => { if (cache.get(key) === p) cache.delete(key); });
      cache.set(key, p);
    }
    return cache.get(key);
  };
}

const asList = (x) => (Array.isArray(x) ? x : x ? [x] : []);

/**
 * Fetches every series of a config: the latency-family ones in one request,
 * each named metric in its own. Resolves to [{ spec, hs }] in the config's
 * order, with `.missing` counting series that had nothing to fetch (a check
 * since deleted, a metric it no longer measures).
 */
export async function loadChartSeries(cfg, fetchFn = historyFetch) {
  const c = normalizeChartConfig(cfg);
  if (!c.series.length) {
    let list = asList(await fetchFn([], c.range));
    if (c.metric === 'loss') list = list.filter((hs) => hs.checkType === 'ping');
    const out = list.map((hs) => ({ spec: { checkId: Number(hs.checkId), metric: c.metric }, hs }));
    out.missing = 0;
    return out;
  }
  const latencyIds = [...new Set(c.series.filter(isBuiltinMetric).map((s) => s.checkId))];
  const named = c.series.filter((s) => !isBuiltinMetric(s));
  const [latency, ...namedLists] = await Promise.all([
    latencyIds.length ? fetchFn(latencyIds, c.range).then(asList) : [],
    ...named.map((s) => Promise.resolve().then(() => fetchFn([s.checkId], c.range, s.metric)).then(asList).catch(() => [])),
  ]);
  const byId = new Map(latency.map((hs) => [Number(hs.checkId), hs]));
  const byKey = new Map(named.map((s, i) => [seriesKey(s), namedLists[i][0]]));
  const out = [];
  let missing = 0;
  for (const s of c.series) {
    const hs = isBuiltinMetric(s) ? byId.get(s.checkId) : byKey.get(seriesKey(s));
    // Packet loss is only measured by a ping.
    if (!hs || (s.metric === 'loss' && isBuiltinMetric(s) && hs.checkType !== 'ping')) { missing++; continue; }
    out.push({ spec: s, hs });
  }
  out.missing = missing;
  return out;
}

/**
 * Fetches the layers of a timestacked chart: the same series read over
 * `layers` windows of the period, each ending one period before the last.
 * Resolves to [{ spec, hs, layer, shift }] — `shift` is how far the layer's
 * points move forward to sit on the current window — with `.spec` the one
 * series stacked. `now` is rounded down to the minute, so the earlier layers
 * are asked for the same window on every redraw within it and stay cached.
 */
export async function loadTimestackSeries(cfg, fetchFn = historyFetch, now = Date.now()) {
  const c = normalizeChartConfig(cfg);
  const ts = c.timestack || normalizeTimestack({});
  const period = timestackPeriod(ts.period);
  let spec = c.series[0];
  if (!spec) {
    // Nothing ticked: stack the first check GWatch would have picked.
    const auto = asList(await fetchFn([], ts.period));
    if (!auto.length) { const none = []; none.spec = null; return none; }
    spec = { checkId: Number(auto[0].checkId), metric: c.metric };
  }
  const named = !isBuiltinMetric(spec);
  const end = Math.floor(now / 60e3) * 60e3;
  const lists = await Promise.all(Array.from({ length: ts.layers }, (_, i) => Promise.resolve()
    .then(() => fetchFn([spec.checkId], ts.period, named ? spec.metric : undefined, i === 0 ? undefined : end - i * period.ms))
    .then(asList).catch(() => [])));
  const out = [];
  lists.forEach((list, i) => {
    const hs = list[0];
    if (hs && !(spec.metric === 'loss' && !named && hs.checkType !== 'ping')) out.push({ spec, hs, layer: i, shift: i * period.ms });
  });
  out.spec = spec;
  return out;
}

/** The unit a fetched series is in. */
function entryUnit({ spec, hs }) {
  if (isBuiltinMetric(spec)) return metricMeta(spec.metric).unit;
  if (hs.metricUnit != null) return hs.metricUnit;
  return hs.checkType === 'system' ? metricUnit(spec.metric) : '';
}

/** The average of a series over the range, in its own unit — what the stat
 *  tile under the chart shows. */
function entryFigure({ spec, hs }) {
  const s = hs.summary || {};
  const mean = (key) => { const vals = (hs.points || []).map((p) => p[key]).filter((v) => v != null && isFinite(v)); return vals.length ? vals.reduce((a, b) => a + b, 0) / vals.length : null; };
  if (!isBuiltinMetric(spec)) return s.avgMs ?? mean('value') ?? mean('avgMs');
  switch (spec.metric) {
    case 'min': return s.minMs;
    case 'max': return s.maxMs;
    case 'jitter': return mean('jitterMs');
    case 'loss': return mean('lossPct');
    case 'availability': return s.availability;
    default: return s.avgMs;
  }
}

/** CSV download links for a chart's series: /api/export/history.csv takes
 *  a metric too, so a named metric exports its own values. */
export function chartCsvItems(cfg, { nodes = [], series = null } = {}) {
  const c = normalizeChartConfig(cfg);
  const list = c.series.length ? c.series : (series || []).map((hs) => ({ checkId: Number(hs.checkId), metric: c.metric }));
  const find = (id) => { for (const n of nodes) for (const ch of n.checks || []) if (Number(ch.id) === Number(id)) return { node: n, check: ch }; return null; };
  const seen = new Set();
  const out = [];
  for (const s of list) {
    const named = !isBuiltinMetric(s);
    const key = named ? seriesKey(s) : `${s.checkId}`;
    if (seen.has(key)) continue;
    seen.add(key);
    const f = find(s.checkId);
    const who = f ? `${f.node.name} › ${f.check.name}` : `check ${s.checkId}`;
    out.push({
      label: `Export CSV — ${who}${named ? ` — ${seriesMetricLabel(s, f?.check.type)}` : ''}`,
      icon: 'download',
      href: `/api/export/history.csv${qs({ checkId: s.checkId, range: c.range, metric: named ? s.metric : undefined })}`,
      download: `history-${s.checkId}${named ? `-${String(s.metric).toLowerCase().replace(/[^\w]+/g, '-')}` : ''}-${c.range}.csv`,
    });
    if (out.length >= 8) break;
  }
  return out;
}

/* ---------- Rendering ---------- */

/**
 * Render a configured chart into host. Returns { charts, refresh, destroy, exportPNG }.
 * fetch(ids, range, metric?) may be provided for caching; defaults to historyFetch.
 *
 * Mixed units (#73): the series are grouped by unit, two units to a chart —
 * the first unit on the left axis, the second on the right. A mix of three
 * or more units is split into further charts rather than squeezed onto a
 * third axis or rescaled into percentages nobody asked for, and a note above
 * the charts says so. yMin, yMax and the threshold line are in the unit of
 * the first series, so they apply to every chart whose left axis is in it.
 */
export function renderConfiguredChart(host, cfg, { title = 'Chart', fetch: fetchFn, fill = false, onData } = {}) {
  const c = normalizeChartConfig(cfg);
  const charts = [];
  const statsEl = h('div', { class: 'chart-stats' });
  const body = h('div', { class: 'chart-body', style: fill ? { flex: '1', minHeight: '0', display: 'flex', flexDirection: 'column' } : null });
  clear(host);
  host.append(body);
  let destroyed = false;
  const load = async () => {
    if (c.timestack) { await loadStack(); return; }
    let entries;
    try { entries = await loadChartSeries(c, fetchFn || historyFetch); } catch (e) { replace(body, h('div', { class: 'note' }, 'Could not load history: ' + e.message)); return; }
    if (destroyed) return;
    if (!entries.length) { replace(body, emptyState({ icon: 'activity', title: 'Nothing to chart yet', text: c.series.length ? 'None of the ticked metrics has any history in this range yet.' : 'Pick checks with history, or add a node with a ping or HTTP check.', compact: true })); return; }
    onData && onData(entries.map((e) => e.hs), entries);
    clear(body);
    charts.forEach((ch) => ch.destroy()); charts.length = 0;
    const palette = seriesColors();
    const from = entries[0].hs.from, to = entries[0].hs.to;
    // Raw named-metric points and bucketed latency points on one chart: the
    // bucket width only means something while every series has it.
    const buckets = [...new Set(entries.map((e) => e.hs.bucketSeconds || 0))];
    const bucket = buckets.length === 1 ? buckets[0] : 0;
    // A series is named after its check; the metric is added whenever the
    // chart plots more than one kind of metric, which is when it is needed.
    const mixedMetrics = new Set(entries.map((e) => seriesKey({ ...e.spec, checkId: 0 }))).size > 1;
    const all = entries.map((e, i) => {
      const unit = entryUnit(e);
      const base = e.hs.nodeName ? `${e.hs.nodeName} › ${e.hs.checkName}` : e.hs.checkName || `Check ${e.hs.checkId}`;
      const color = c.colors[seriesKey(e.spec)] || c.colors[e.spec.checkId] || palette[i % palette.length];
      const s = toSeries(e.hs, isBuiltinMetric(e.spec) ? e.spec.metric : 'avg', color);
      return { ...s, unit, name: mixedMetrics ? `${base} — ${seriesMetricLabel(e.spec, e.hs.checkType)}` : base, entry: e };
    });
    const primaryUnit = all[0].unit;
    // Loss and availability are read against the whole 0–100 scale, as they
    // always were; any other percentage (a processor at 4%) is scaled to fit.
    const fullScale = (list) => list.length > 0 && list.every((s) => isBuiltinMetric(s.entry.spec) && (s.entry.spec.metric === 'loss' || s.entry.spec.metric === 'availability'));
    const opts = (group, named) => {
      const left = group.series.filter((s) => s.unit === group.units[0]);
      const right = group.series.filter((s) => s.unit === group.units[1]);
      const own = group.units[0] === primaryUnit;
      return {
        unit: group.units[0],
        yMin: own && c.yMin != null ? c.yMin : fullScale(left) ? 0 : null,
        yMax: own && c.yMax != null ? c.yMax : fullScale(left) ? 100 : null,
        y2Min: fullScale(right) ? 0 : null, y2Max: fullScale(right) ? 100 : null,
        threshold: own ? c.threshold : null,
        style: c.style, smooth: c.smooth, points: c.points, lineWidth: c.lineWidth, shadeFailures: c.shadeFailures, legend: c.legend && named, alwaysLegend: named, grid: c.grid,
        height: fill ? null : c.height, title, ariaLabel: `${title} chart`,
      };
    };
    // One chart per check when asked, then no more than two units a chart.
    const byCheck = [];
    if (c.split) {
      const ids = [...new Set(all.map((s) => Number(s.entry.hs.checkId)))];
      for (const id of ids) byCheck.push(all.filter((s) => Number(s.entry.hs.checkId) === id));
    } else byCheck.push(all);
    const plan = byCheck.flatMap((list) => groupByUnits(list, primaryUnit).map((g) => ({ ...g, check: c.split ? list[0].entry.hs : null })));
    const unitCount = new Set(all.map((s) => s.unit)).size;
    if (plan.length > byCheck.length) {
      body.append(h('p', { class: 'note chart-split-note' }, c.split
        ? `A chart has two axes, so a check measured here in more than two units is drawn as more than one chart (${plan.length} in all).`
        : `These metrics are in ${unitCount} different units and a chart has two axes, so they are drawn as ${plan.length} charts.`));
    }
    plan.forEach((group, gi) => {
      const hostEl = h('div', { class: 'chart-host', style: fill ? { flex: '1', minHeight: '0', display: 'flex', flexDirection: 'column' } : { marginBottom: plan.length > 1 ? '12px' : '0' } });
      if (group.check && (gi === 0 || plan[gi - 1].check !== group.check)) hostEl.append(h('div', { class: 'section-title', style: { marginBottom: '4px' } }, `${group.check.nodeName || ''} › ${group.check.checkName}`));
      body.append(hostEl);
      // A legend whenever a line needs naming: more than one on the chart, or
      // one of several charts that the units split apart. A chart of its own
      // per check is named by its heading instead, unless it mixes metrics.
      const named = group.series.length > 1 || (!c.split && plan.length > 1) || (c.split && mixedMetrics);
      const chart = new LineChart(hostEl, opts(group, named));
      charts.push(chart);
      chart.setData({ series: group.series.map(({ entry, ...s }) => s), from, to, bucketSeconds: bucket });
    });
    if (c.uptime) {
      const up = h('div', { style: { marginTop: '8px' } });
      const seen = new Set();
      for (const { hs } of entries) {
        if (seen.has(Number(hs.checkId))) continue;
        seen.add(Number(hs.checkId));
        const avail = hs.summary?.availability;
        const cls = avail == null ? '' : avail >= 99.9 ? 'text-up' : avail >= 95 ? 'text-degraded' : 'text-down';
        up.append(h('div', { class: 'uptime-row' }, h('div', { class: 'uptime-name truncate' }, hs.nodeName || '', h('div', { class: 'sub' }, hs.checkName)), uptimeBar(hs.points, { bucketSeconds: hs.bucketSeconds, from: hs.from, to: hs.to, label: `${hs.nodeName || ''} › ${hs.checkName}` }), h('div', { class: `uptime-pct ${cls}` }, pct(avail, 2))));
      }
      up.append(uptimeLegend());
      body.append(up);
    }
    if (entries.missing) body.append(h('p', { class: 'note' }, `${plural(entries.missing, 'ticked metric')} ${entries.missing === 1 ? 'has' : 'have'} nothing to show: the check may have been removed, or no longer measures it.`));
    if (!fill) {
      clear(statsEl);
      // One tile per series: its average over the range, in its own unit.
      // Loss and availability keep the two decimals that tell 99.95% from
      // 99.5%; a processor or a disk reads well enough with one.
      all.forEach((s) => {
        const spec = s.entry.spec;
        const fine = isBuiltinMetric(spec) && (spec.metric === 'loss' || spec.metric === 'availability');
        const v = entryFigure(s.entry);
        statsEl.append(h('div', { class: 'stat' }, h('div', { class: 'stat-value mono' }, fine ? pct(v, 2) : unitValue(v, s.unit)), h('div', { class: 'stat-label' }, s.name)));
      });
      body.append(statsEl);
    }
  };
  // A timestacked chart: one LineChart, one line per layer, every layer's
  // points moved forward onto the current window so they share its time axis.
  const loadStack = async () => {
    let layers;
    try { layers = await loadTimestackSeries(c, fetchFn || historyFetch); } catch (e) { replace(body, h('div', { class: 'note' }, 'Could not load history: ' + e.message)); return; }
    if (destroyed) return;
    if (!layers.length || !layers.some((l) => (l.hs.points || []).length)) {
      replace(body, emptyState({ icon: 'activity', title: 'Nothing to stack yet', text: 'This metric has no history in these periods yet.', compact: true }));
      return;
    }
    onData && onData(layers.map((l) => l.hs), layers);
    clear(body);
    charts.forEach((ch) => ch.destroy()); charts.length = 0;
    const ts = c.timestack;
    const spec = layers.spec;
    const palette = seriesColors();
    const first = c.colors[seriesKey(spec)] || c.colors[spec.checkId] || palette[0];
    const others = palette.filter((x) => x.toLowerCase() !== String(first).toLowerCase());
    const unit = entryUnit(layers[0]);
    const lead = layers[0].hs;
    const what = `${lead.nodeName ? `${lead.nodeName} › ` : ''}${lead.checkName || `Check ${lead.checkId}`} — ${seriesMetricLabel(spec, lead.checkType)}`;
    const series = layers.map((l) => {
      const s = toSeries(l.hs, isBuiltinMetric(spec) ? spec.metric : 'avg', l.layer === 0 ? first : others[(l.layer - 1) % others.length]);
      return { ...s, unit, name: timestackLabel(ts.period, l.layer), lineWidth: l.layer === 0 ? c.lineWidth + 0.75 : c.lineWidth, points: s.points.map((p) => ({ ...p, t: p.t + l.shift })) };
    });
    // The current layer decides the window; older ones were moved onto it.
    const base = layers.find((l) => l.layer === 0)?.hs || lead;
    const from = +new Date(base.from), to = +new Date(base.to);
    const buckets = [...new Set(layers.map((l) => l.hs.bucketSeconds || 0))];
    const full = isBuiltinMetric(spec) && (spec.metric === 'loss' || spec.metric === 'availability');
    body.append(h('div', { class: 'section-title timestack-title' }, `${what} · ${ts.layers} × ${rangeLabel(ts.period)}`));
    const hostEl = h('div', { class: 'chart-host', style: fill ? { flex: '1', minHeight: '0', display: 'flex', flexDirection: 'column' } : null });
    body.append(hostEl);
    const chart = new LineChart(hostEl, {
      unit, yMin: c.yMin != null ? c.yMin : full ? 0 : null, yMax: c.yMax != null ? c.yMax : full ? 100 : null, threshold: c.threshold,
      // Failure shading would mix the layers' outages into one band; the
      // lines themselves break where a check failed.
      style: c.style, smooth: c.smooth, points: c.points, lineWidth: c.lineWidth, shadeFailures: false, legend: c.legend, alwaysLegend: true, grid: c.grid,
      height: fill ? null : c.height, title, ariaLabel: `${title} timestacked chart: ${what}, ${ts.layers} periods of ${rangeLabel(ts.period)}`,
    });
    charts.push(chart);
    chart.setData({ series, from, to, bucketSeconds: buckets.length === 1 ? buckets[0] : 0 });
    if (layers.length < ts.layers) body.append(h('p', { class: 'note' }, `${plural(ts.layers - layers.length, 'earlier period')} could not be read.`));
    if (!fill) {
      clear(statsEl);
      // One tile per layer: its average over its own period.
      layers.forEach((l, i) => {
        const v = entryFigure(l);
        statsEl.append(h('div', { class: 'stat' }, h('div', { class: 'stat-value mono' }, full ? pct(v, 2) : unitValue(v, unit)), h('div', { class: 'stat-label' }, series[i].name)));
      });
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

export function newChartId() { return uid('chart'); }
export { icon, textInput };

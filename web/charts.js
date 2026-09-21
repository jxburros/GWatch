// Lightweight canvas charts: multi-series time line chart with gaps,
// failure shading, hover tooltip and PNG export; plus sparkline and uptime bar.

import { ms as fmtMs, pct as fmtPct, timeShort, dateTime } from './fmt.js';
import { h, cssColors, onThemeChange } from './components.js';

export const SERIES_COLORS = ['#43c9c0', '#e879a6', '#ffc542', '#6ea0ff', '#ff9f6e', '#b18cff', '#3ec8b8', '#35e07f'];
/** Series palette with the current accent first. */
export function seriesColors() {
  const c = cssColors();
  const out = [c.accent, ...SERIES_COLORS.filter((x) => x.toLowerCase() !== c.accent.toLowerCase())];
  return out;
}
export function seriesColor(i) { const p = seriesColors(); return p[i % p.length]; }
// Design tokens are read from CSS so charts follow the theme and accent.
let CSS = cssColors();
onThemeChange(() => { CSS = cssColors(); });
export function refreshChartColors() { CSS = cssColors(); }
const FONT = '11.5px "IBM Plex Sans", Inter, "Segoe UI", system-ui, -apple-system, sans-serif';
const MONO = '11.5px "IBM Plex Mono", "JetBrains Mono", "Cascadia Mono", "SF Mono", Consolas, "Liberation Mono", Menlo, monospace';
export const CHART_STYLES = [
  { value: 'line', label: 'Line' }, { value: 'area', label: 'Area' }, { value: 'step', label: 'Step' }, { value: 'bars', label: 'Bars' }, { value: 'scatter', label: 'Points' },
];
const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
const DAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
const MIN = 60e3, HOUR = 3600e3, DAY = 86400e3;

/* ---------- Axis helpers ---------- */

function niceNum(range, round) {
  const exp = Math.floor(Math.log10(range));
  const f = range / Math.pow(10, exp);
  let nf;
  if (round) nf = f < 1.5 ? 1 : f < 3 ? 2 : f < 7 ? 5 : 10;
  else nf = f <= 1 ? 1 : f <= 2 ? 2 : f <= 5 ? 5 : 10;
  return nf * Math.pow(10, exp);
}

export function niceTicks(min, max, maxTicks = 5) {
  if (!isFinite(min) || !isFinite(max)) return { min: 0, max: 1, ticks: [0, 1] };
  if (max === min) { max = min + 1; }
  const range = niceNum(max - min, false);
  const step = niceNum(range / (maxTicks - 1), true);
  const nmin = Math.floor(min / step) * step;
  const nmax = Math.ceil(max / step) * step;
  const ticks = [];
  for (let v = nmin; v <= nmax + step * 0.5; v += step) ticks.push(+v.toFixed(10));
  return { min: nmin, max: nmax, ticks, step };
}

const TIME_STEPS = [MIN, 2 * MIN, 5 * MIN, 10 * MIN, 15 * MIN, 30 * MIN, HOUR, 2 * HOUR, 3 * HOUR, 6 * HOUR, 12 * HOUR, DAY, 2 * DAY, 7 * DAY, 14 * DAY, 'month', '2month', '3month', '6month', 'year'];

function stepMs(step) {
  if (typeof step === 'number') return step;
  return { month: 30 * DAY, '2month': 60 * DAY, '3month': 91 * DAY, '6month': 182 * DAY, year: 365 * DAY }[step];
}

/** Compute nicely aligned time ticks (local time) for [from, to] across `width` px. */
export function timeTicks(from, to, width, minPx = 76) {
  const span = to - from;
  let step = TIME_STEPS[TIME_STEPS.length - 1];
  for (const s of TIME_STEPS) {
    if (span / stepMs(s) * minPx <= width) { step = s; break; }
  }
  const ticks = [];
  const start = new Date(from);
  if (typeof step === 'number' && step < DAY) {
    // align to local midnight + multiples of step
    const midnight = new Date(start.getFullYear(), start.getMonth(), start.getDate()).getTime();
    let t = midnight + Math.ceil((from - midnight) / step) * step;
    for (; t <= to; t += step) ticks.push({ t, major: new Date(t).getHours() === 0 && new Date(t).getMinutes() === 0 });
  } else if (typeof step === 'number') {
    const days = step / DAY;
    let d = new Date(start.getFullYear(), start.getMonth(), start.getDate());
    if (d.getTime() < from) d.setDate(d.getDate() + 1);
    if (days >= 7) { // align weekly ticks to Monday
      while (d.getDay() !== 1) d.setDate(d.getDate() + 1);
    }
    for (; d.getTime() <= to; d.setDate(d.getDate() + days)) ticks.push({ t: d.getTime(), major: d.getDate() === 1 });
  } else {
    const months = { month: 1, '2month': 2, '3month': 3, '6month': 6, year: 12 }[step];
    let d = new Date(start.getFullYear(), start.getMonth(), 1);
    if (d.getTime() < from) d.setMonth(d.getMonth() + 1);
    if (months > 1) while (d.getMonth() % months !== 0) d.setMonth(d.getMonth() + 1);
    for (; d.getTime() <= to; d.setMonth(d.getMonth() + months)) ticks.push({ t: d.getTime(), major: d.getMonth() === 0 });
  }
  const label = (tick) => {
    const d = new Date(tick.t);
    if (typeof step === 'number' && step < DAY) {
      if (tick.major && span > 12 * HOUR) return `${DAYS[d.getDay()]} ${d.getDate()}`;
      return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
    }
    if (typeof step === 'number') {
      if (span <= 8 * DAY) return `${DAYS[d.getDay()]} ${d.getDate()}`;
      return `${d.getDate()} ${MONTHS[d.getMonth()]}`;
    }
    return tick.major ? `${MONTHS[d.getMonth()]} ${d.getFullYear()}` : MONTHS[d.getMonth()];
  };
  return { step, ticks: ticks.map((t) => ({ ...t, label: label(t) })) };
}

function fmtValue(v, unit) {
  if (v == null || isNaN(v)) return '—';
  if (unit === '%') return fmtPct(v, 1);
  if (unit === 'ms') return fmtMs(v);
  return String(Math.round(v * 100) / 100);
}
function fmtAxis(v, unit) {
  if (unit === '%') return `${Math.round(v * 10) / 10}%`;
  if (unit === 'ms') return v >= 1000 ? `${(v / 1000).toFixed(v >= 10000 ? 0 : 1)} s` : `${Math.round(v * 10) / 10} ms`;
  return String(v);
}

/* ---------- LineChart ---------- */

/**
 * new LineChart(container, { unit: 'ms'|'%', height, yMin, yMax, legend, shadeFailures, area,
 *   style: 'line'|'area'|'step'|'bars'|'scatter', smooth, points, lineWidth, threshold, thresholdLabel, grid })
 * chart.setData({ series: [{ name, color, points: [{ t, v, avail, min, max }] }], from, to, bucketSeconds })
 */
export class LineChart {
  constructor(container, opts = {}) {
    this.container = container;
    this.opts = { unit: 'ms', height: null, yMin: null, yMax: null, legend: true, shadeFailures: true, area: true, style: null, smooth: false, points: false, lineWidth: 1.75, threshold: null, thresholdLabel: '', grid: true, minTickPx: 76, ...opts };
    if (this.opts.style == null) this.opts.style = this.opts.area ? 'area' : 'line';
    this._themeOff = onThemeChange(() => { this._surface = null; this.scheduleDraw(); });
    this.el = h('div', { class: 'chart', style: this.opts.height ? { height: `${this.opts.height}px` } : null });
    this.canvas = h('canvas', { role: 'img', 'aria-label': opts.ariaLabel || 'Chart' });
    this.tooltip = h('div', { class: 'chart-tooltip', hidden: true });
    this.emptyEl = h('div', { class: 'chart-empty', hidden: true }, 'No data for this range yet');
    this.el.append(this.canvas, this.tooltip, this.emptyEl);
    if (this.opts.legend) { this.legend = h('div', { class: 'chart-legend' }); }
    container.append(this.el);
    if (this.legend) container.append(this.legend);
    this.ctx = this.canvas.getContext('2d');
    this.data = { series: [], from: 0, to: 0, bucketSeconds: 0 };
    this.hover = null;
    this._onMove = (e) => this._handleMove(e);
    this._onLeave = () => { this.hover = null; this.tooltip.hidden = true; this.draw(); };
    this.canvas.addEventListener('mousemove', this._onMove);
    this.canvas.addEventListener('mouseleave', this._onLeave);
    this._raf = 0;
    // Resolved lazily from the canvas's own computed background, so a chart on
    // a wallboard panel paints that panel's colour rather than a card's.
    this._surface = null;
    this.ro = new ResizeObserver(() => this.scheduleDraw());
    this.ro.observe(this.el);
    this.draw();
  }

  /** The colour the canvas sits on. A canvas bitmap is transparent until it is
   *  painted, and a transparent canvas shows whatever the browser has behind
   *  it — which on a fresh or just-resized bitmap is the page default, not the
   *  theme. Painting this first makes every frame opaque and on-theme. */
  _surfaceColor() {
    if (this._surface) return this._surface;
    let c = '';
    try { c = getComputedStyle(this.canvas).backgroundColor || ''; } catch { c = ''; }
    // An unstyled canvas computes to transparent; fall back to the card token.
    const transparent = !c || c === 'transparent' || /rgba\(\s*0\s*,\s*0\s*,\s*0\s*,\s*0\s*\)/.test(c);
    this._surface = transparent ? CSS.bg : c;
    return this._surface;
  }

  scheduleDraw() {
    if (this._raf) return;
    this._raf = requestAnimationFrame(() => { this._raf = 0; this.draw(); });
  }

  setData({ series = [], from, to, bucketSeconds = 0 } = {}) {
    let f = from != null ? +new Date(from) : Infinity;
    let t = to != null ? +new Date(to) : -Infinity;
    if (from == null || to == null) {
      for (const s of series) for (const p of s.points) { if (p.t < f) f = p.t; if (p.t > t) t = p.t; }
      if (!isFinite(f)) { f = Date.now() - HOUR; t = Date.now(); }
    }
    this.data = {
      series: series.map((s, i) => ({ ...s, color: s.color || SERIES_COLORS[i % SERIES_COLORS.length], points: (s.points || []).slice().sort((a, b) => a.t - b.t) })),
      from: f, to: t, bucketSeconds,
    };
    this._renderLegend();
    this.draw();
  }

  _renderLegend() {
    if (!this.legend) return;
    this.legend.innerHTML = '';
    if (this.data.series.length <= 1 && !this.opts.alwaysLegend) return;
    for (const s of this.data.series) {
      this.legend.append(h('span', { class: 'legend-item' }, h('span', { class: 'legend-swatch', style: { background: s.color } }), s.name));
    }
  }

  /** Change display options (style, smoothing, threshold …) and redraw. */
  setOptions(opts = {}) {
    Object.assign(this.opts, opts);
    if (this.opts.height && this.el) this.el.style.height = `${this.opts.height}px`;
    if (opts.legend != null) { if (opts.legend && !this.legend) { this.legend = h('div', { class: 'chart-legend' }); this.container.append(this.legend); } if (!opts.legend && this.legend) { this.legend.remove(); this.legend = null; } this._renderLegend(); }
    this.scheduleDraw();
  }

  destroy() {
    this.ro.disconnect();
    if (this._themeOff) this._themeOff();
    if (this._raf) cancelAnimationFrame(this._raf);
    this.canvas.removeEventListener('mousemove', this._onMove);
    this.canvas.removeEventListener('mouseleave', this._onLeave);
    this.el.remove();
    if (this.legend) this.legend.remove();
  }

  _layout(w, h) {
    return { left: 52, right: 14, top: 14, bottom: 26, w, h, plotW: w - 52 - 14, plotH: h - 14 - 26 };
  }

  _scales(w, h) {
    const L = this._layout(w, h);
    const { from, to, series } = this.data;
    let vmin = Infinity, vmax = -Infinity;
    for (const s of series) for (const p of s.points) if (p.v != null && isFinite(p.v)) { if (p.v < vmin) vmin = p.v; if (p.v > vmax) vmax = p.v; }
    if (this.opts.threshold != null && isFinite(this.opts.threshold)) { if (this.opts.threshold > vmax) vmax = this.opts.threshold; if (this.opts.threshold < vmin) vmin = this.opts.threshold; }
    if (!isFinite(vmin)) { vmin = 0; vmax = this.opts.unit === '%' ? 100 : 10; }
    let yMin = this.opts.yMin != null ? this.opts.yMin : Math.min(0, vmin);
    let yMax = this.opts.yMax != null ? this.opts.yMax : vmax;
    if (this.opts.yMax == null) yMax = vmax + (vmax - yMin) * 0.12 || 1;
    if (this.opts.unit === '%' && this.opts.yMax == null) yMax = Math.min(100, Math.max(yMax, 1));
    const yt = niceTicks(yMin, yMax, 5);
    if (this.opts.yMax != null) yt.max = this.opts.yMax;
    if (this.opts.yMin != null) yt.min = this.opts.yMin;
    const x = (t) => L.left + ((t - from) / Math.max(1, to - from)) * L.plotW;
    const y = (v) => L.top + (1 - (v - yt.min) / Math.max(1e-9, yt.max - yt.min)) * L.plotH;
    return { L, x, y, yt };
  }

  draw(ctx = this.ctx, exportMode = false) {
    const rect = this.el.getBoundingClientRect();
    const w = Math.max(10, Math.floor(rect.width));
    const h = Math.max(10, Math.floor(this.opts.height || rect.height || 220));
    const dpr = window.devicePixelRatio || 1;
    if (ctx === this.ctx) {
      // Only when the device-pixel size really changed: assigning width or
      // height reallocates the bitmap and wipes it, and an unpainted bitmap is
      // a hole in the page for the rest of the frame.
      const cw = Math.round(w * dpr), ch = Math.round(h * dpr);
      if (this.canvas.width !== cw || this.canvas.height !== ch) {
        this.canvas.width = cw; this.canvas.height = ch;
        this._surface = null;
      }
      if (this.canvas.style.height !== `${h}px`) this.canvas.style.height = `${h}px`;
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      // Fill rather than clear: clearing leaves the bitmap transparent, which
      // is what made the charts flash the page default between redraws.
      ctx.fillStyle = this._surfaceColor();
      ctx.fillRect(0, 0, w, h);
    }
    this._size = { w, h };
    const hasData = this.data.series.some((s) => s.points.some((p) => p.v != null));
    this.emptyEl.hidden = hasData || this.data.series.some((s) => s.points.length);
    this._render(ctx, w, h, exportMode);
  }

  _render(ctx, w, h, exportMode) {
    const { L, x, y, yt } = this._scales(w, h);
    const { from, to, series, bucketSeconds } = this.data;
    if (exportMode) { ctx.fillStyle = CSS.bg; ctx.fillRect(0, 0, w, h); }

    // Failure shading
    if (this.opts.shadeFailures) {
      for (const s of series) {
        const pts = s.points;
        for (let i = 0; i < pts.length; i++) {
          const p = pts[i];
          if (p.avail == null || p.avail >= 100) continue;
          let x0, x1;
          if (bucketSeconds > 0) { x0 = x(p.t); x1 = x(p.t + bucketSeconds * 1000); }
          else {
            const prev = pts[i - 1]?.t ?? p.t - MIN; const next = pts[i + 1]?.t ?? p.t + MIN;
            x0 = x(p.t - (p.t - prev) / 2); x1 = x(p.t + (next - p.t) / 2);
          }
          x0 = Math.max(L.left, x0); x1 = Math.min(L.left + L.plotW, x1);
          if (x1 - x0 < 2) { x0 -= 1; x1 += 1; }
          const alpha = 0.10 + 0.18 * (1 - p.avail / 100);
          ctx.fillStyle = `rgba(255, 92, 92, ${alpha.toFixed(3)})`;
          ctx.fillRect(x0, L.top, x1 - x0, L.plotH);
        }
      }
    }

    // Grid + y labels
    ctx.font = FONT; ctx.textBaseline = 'middle'; ctx.textAlign = 'right';
    ctx.lineWidth = 1;
    for (const v of yt.ticks) {
      if (v < yt.min - 1e-9 || v > yt.max + 1e-9) continue;
      const yy = Math.round(y(v)) + 0.5;
      if (this.opts.grid !== false) { ctx.strokeStyle = CSS.line; ctx.beginPath(); ctx.moveTo(L.left, yy); ctx.lineTo(L.left + L.plotW, yy); ctx.stroke(); }
      ctx.fillStyle = CSS.muted; ctx.fillText(fmtAxis(v, this.opts.unit), L.left - 8, yy);
    }

    // X ticks
    const tt = timeTicks(from, to, L.plotW, this.opts.minTickPx);
    ctx.textAlign = 'center'; ctx.textBaseline = 'top';
    for (const tick of tt.ticks) {
      const xx = Math.round(x(tick.t)) + 0.5;
      ctx.strokeStyle = tick.major ? CSS.lineStrong : CSS.line; ctx.beginPath(); ctx.moveTo(xx, L.top + L.plotH); ctx.lineTo(xx, L.top + L.plotH + 4); ctx.stroke();
      ctx.fillStyle = tick.major ? CSS.text : CSS.muted; ctx.fillText(tick.label, xx, L.top + L.plotH + 8);
    }

    // Series
    ctx.save();
    ctx.beginPath(); ctx.rect(L.left, L.top - 2, L.plotW, L.plotH + 4); ctx.clip();
    const style = this.opts.style || 'area';
    const lw = Math.max(0.5, Number(this.opts.lineWidth) || 1.75);
    for (let si = 0; si < series.length; si++) {
      const s = series[si];
      const pts = s.points;
      if (!pts.length) continue;
      const gapMs = this._gapThreshold(pts, bucketSeconds);
      // Split into runs of consecutive valid points.
      const runs = [];
      let run = []; let lastT = null;
      for (const p of pts) {
        if (p.v == null || (lastT != null && p.t - lastT > gapMs)) { if (run.length) runs.push(run); run = []; }
        if (p.v != null) { run.push(p); lastT = p.t; } else lastT = null;
      }
      if (run.length) runs.push(run);

      if (style === 'bars') {
        const n = series.length;
        for (const p of pts) {
          if (p.v == null) continue;
          let x0, x1;
          if (bucketSeconds > 0) { x0 = x(p.t); x1 = x(p.t + bucketSeconds * 1000); }
          else { const i = pts.indexOf(p); const prev = pts[i - 1]?.t ?? p.t - MIN; const next = pts[i + 1]?.t ?? p.t + MIN; x0 = x(p.t - (p.t - prev) / 2); x1 = x(p.t + (next - p.t) / 2); }
          const bw = Math.max(1, (x1 - x0 - 1) / n);
          const bx = x0 + 0.5 + bw * si;
          ctx.fillStyle = hexToRgba(s.color, 0.85);
          ctx.fillRect(bx, y(p.v), Math.max(1, bw - (n > 1 ? 0.5 : 0)), y(yt.min) - y(p.v));
        }
        continue;
      }
      const tracePath = (r) => {
        if (style === 'step') {
          ctx.moveTo(x(r[0].t), y(r[0].v));
          for (let i = 1; i < r.length; i++) { ctx.lineTo(x(r[i].t), y(r[i - 1].v)); ctx.lineTo(x(r[i].t), y(r[i].v)); }
          if (bucketSeconds > 0) ctx.lineTo(x(r[r.length - 1].t + bucketSeconds * 1000), y(r[r.length - 1].v));
        } else if (this.opts.smooth && r.length > 2) {
          ctx.moveTo(x(r[0].t), y(r[0].v));
          for (let i = 0; i < r.length - 1; i++) {
            const p0 = r[i - 1] || r[i], p1 = r[i], p2 = r[i + 1], p3 = r[i + 2] || p2;
            const c1x = x(p1.t) + (x(p2.t) - x(p0.t)) / 6, c1y = y(p1.v) + (y(p2.v) - y(p0.v)) / 6;
            const c2x = x(p2.t) - (x(p3.t) - x(p1.t)) / 6, c2y = y(p2.v) - (y(p3.v) - y(p1.v)) / 6;
            ctx.bezierCurveTo(c1x, c1y, c2x, c2y, x(p2.t), y(p2.v));
          }
        } else {
          ctx.moveTo(x(r[0].t), y(r[0].v));
          for (let i = 1; i < r.length; i++) ctx.lineTo(x(r[i].t), y(r[i].v));
        }
      };
      if (style === 'area') {
        ctx.fillStyle = hexToRgba(s.color, series.length > 1 ? 0.06 : 0.11);
        for (const r of runs) {
          if (r.length < 2) continue;
          ctx.beginPath(); tracePath(r);
          const endX = style === 'step' && bucketSeconds > 0 ? x(r[r.length - 1].t + bucketSeconds * 1000) : x(r[r.length - 1].t);
          ctx.lineTo(endX, y(yt.min)); ctx.lineTo(x(r[0].t), y(yt.min)); ctx.closePath(); ctx.fill();
        }
      }
      if (style !== 'scatter') {
        ctx.strokeStyle = s.color; ctx.lineWidth = lw; ctx.lineJoin = 'round'; ctx.lineCap = 'round';
        ctx.beginPath();
        for (const r of runs) { if (r.length >= 2) tracePath(r); }
        ctx.stroke();
      }
      // isolated points always get a dot; every point when requested
      ctx.fillStyle = s.color;
      const dotR = style === 'scatter' ? Math.max(1.5, lw + 0.5) : 2;
      for (const r of runs) {
        if (r.length === 1 || style === 'scatter' || this.opts.points) {
          for (const p of r) { ctx.beginPath(); ctx.arc(x(p.t), y(p.v), dotR, 0, Math.PI * 2); ctx.fill(); }
        }
      }
    }
    // Threshold line
    if (this.opts.threshold != null && isFinite(this.opts.threshold)) {
      const ty = Math.round(y(this.opts.threshold)) + 0.5;
      ctx.strokeStyle = CSS.warn; ctx.lineWidth = 1; ctx.setLineDash([5, 4]);
      ctx.beginPath(); ctx.moveTo(L.left, ty); ctx.lineTo(L.left + L.plotW, ty); ctx.stroke(); ctx.setLineDash([]);
      ctx.font = MONO; ctx.textAlign = 'right'; ctx.textBaseline = 'bottom'; ctx.fillStyle = CSS.warn;
      ctx.fillText(this.opts.thresholdLabel || fmtAxis(this.opts.threshold, this.opts.unit), L.left + L.plotW - 4, ty - 2);
    }
    ctx.restore();

    // Axis lines
    ctx.strokeStyle = CSS.lineStrong; ctx.beginPath();
    ctx.moveTo(L.left + 0.5, L.top); ctx.lineTo(L.left + 0.5, L.top + L.plotH + 0.5); ctx.lineTo(L.left + L.plotW, L.top + L.plotH + 0.5); ctx.stroke();

    // Hover
    if (this.hover && !exportMode) {
      const hx = Math.round(x(this.hover.t)) + 0.5;
      ctx.strokeStyle = hexToRgba(CSS.text, 0.35); ctx.setLineDash([3, 3]); ctx.beginPath(); ctx.moveTo(hx, L.top); ctx.lineTo(hx, L.top + L.plotH); ctx.stroke(); ctx.setLineDash([]);
      for (const hp of this.hover.values) {
        if (hp.v == null) continue;
        ctx.fillStyle = hp.color; ctx.beginPath(); ctx.arc(x(hp.t), y(hp.v), 3.5, 0, Math.PI * 2); ctx.fill();
        ctx.strokeStyle = CSS.bg; ctx.lineWidth = 1.5; ctx.stroke();
      }
    }

    if (exportMode && this.opts.title) {
      ctx.font = 'bold 13px "IBM Plex Sans", Inter, "Segoe UI", system-ui, sans-serif'; ctx.textAlign = 'left'; ctx.textBaseline = 'top'; ctx.fillStyle = CSS.text;
      ctx.fillText(this.opts.title, L.left, 2);
    }
  }

  _gapThreshold(pts, bucketSeconds) {
    if (bucketSeconds > 0) return bucketSeconds * 1000 * 2.5;
    // median spacing
    const ds = [];
    for (let i = 1; i < Math.min(pts.length, 400); i++) ds.push(pts[i].t - pts[i - 1].t);
    if (!ds.length) return Infinity;
    ds.sort((a, b) => a - b);
    return Math.max(ds[Math.floor(ds.length / 2)] * 3, 2 * MIN);
  }

  _handleMove(e) {
    const rect = this.canvas.getBoundingClientRect();
    const mx = e.clientX - rect.left;
    const { w, h } = this._size || { w: rect.width, h: rect.height };
    const { L, x } = this._scales(w, h);
    if (mx < L.left || mx > L.left + L.plotW) { this._onLeave(); return; }
    const t = this.data.from + ((mx - L.left) / L.plotW) * (this.data.to - this.data.from);
    // nearest point per series (binary search)
    const values = [];
    let bestT = null; let bestD = Infinity;
    for (const s of this.data.series) {
      const pts = s.points; if (!pts.length) continue;
      let lo = 0, hi = pts.length - 1;
      while (lo < hi) { const mid = (lo + hi) >> 1; if (pts[mid].t < t) lo = mid + 1; else hi = mid; }
      let idx = lo;
      if (idx > 0 && Math.abs(pts[idx - 1].t - t) < Math.abs(pts[idx].t - t)) idx--;
      const p = pts[idx];
      const d = Math.abs(p.t - t);
      const px = Math.abs(x(p.t) - mx);
      if (px > 40) continue;
      values.push({ name: s.name, color: s.color, ...p });
      if (d < bestD) { bestD = d; bestT = p.t; }
    }
    if (!values.length) { this._onLeave(); return; }
    this.hover = { t: bestT, values };
    this._showTooltip(mx, values, bestT);
    this.draw();
  }

  _showTooltip(mx, values, t) {
    const tip = this.tooltip;
    tip.innerHTML = '';
    tip.append(h('div', { class: 'tt-time' }, this.data.to - this.data.from > 2 * DAY ? dateTime(t, { seconds: false }) : timeShort(t, { seconds: this.data.bucketSeconds < 300 })));
    for (const v of values) {
      const row = h('div', { class: 'tt-row' },
        h('span', { style: { display: 'inline-flex', alignItems: 'center', gap: '6px', minWidth: '0' } }, h('span', { class: 'tt-swatch', style: { background: v.color } }), h('span', { class: 'truncate', style: { maxWidth: '180px' } }, v.name)),
        h('span', { class: 'tt-val' }, v.v == null ? (v.avail === 0 ? 'failed' : '—') : fmtValue(v.v, this.opts.unit)),
      );
      tip.append(row);
      if (v.avail != null && v.avail < 100) tip.append(h('div', { class: 'tt-row tt-fail' }, h('span', null, 'Availability'), h('span', { class: 'tt-val' }, fmtPct(v.avail))));
      if (v.min != null && v.max != null && v.v != null && this.opts.unit === 'ms' && (v.min !== v.v || v.max !== v.v)) {
        tip.append(h('div', { class: 'tt-row', style: { color: 'var(--muted)' } }, h('span', null, 'min / max'), h('span', { class: 'tt-val' }, `${fmtMs(v.min)} / ${fmtMs(v.max)}`)));
      }
    }
    tip.hidden = false;
    const tw = tip.offsetWidth;
    const w = this._size.w;
    let left = mx + 14;
    if (left + tw > w - 4) left = mx - tw - 14;
    tip.style.left = `${Math.max(0, left)}px`;
    tip.style.top = '8px';
  }

  /** Export the chart as a PNG download. */
  exportPNG(filename = 'chart.png') {
    const { w, h } = this._size;
    const scale = 2;
    const c = document.createElement('canvas');
    c.width = w * scale; c.height = (h + (this.legend && this.data.series.length > 1 ? 24 : 0)) * scale;
    const ctx = c.getContext('2d');
    ctx.setTransform(scale, 0, 0, scale, 0, 0);
    ctx.fillStyle = CSS.bg; ctx.fillRect(0, 0, c.width, c.height);
    this._render(ctx, w, h, true);
    if (this.legend && this.data.series.length > 1) {
      ctx.font = FONT; ctx.textBaseline = 'middle'; ctx.textAlign = 'left';
      let lx = 52;
      for (const s of this.data.series) {
        ctx.fillStyle = s.color; ctx.fillRect(lx, h + 10, 12, 3);
        ctx.fillStyle = CSS.muted; ctx.fillText(s.name, lx + 18, h + 12);
        lx += 18 + ctx.measureText(s.name).width + 18;
      }
    }
    return new Promise((resolve) => {
      c.toBlob((blob) => {
        if (!blob) { resolve(false); return; }
        const url = URL.createObjectURL(blob);
        const a = h('a', { href: url, download: filename });
        document.body.append(a); a.click(); a.remove();
        setTimeout(() => URL.revokeObjectURL(url), 2000);
        resolve(true);
      }, 'image/png');
    });
  }
}

export function hexToRgba(hex, a) {
  const m = /^#?([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i.exec(hex || '');
  if (!m) return hex;
  return `rgba(${parseInt(m[1], 16)}, ${parseInt(m[2], 16)}, ${parseInt(m[3], 16)}, ${a})`;
}

/* ---------- Series conversion ---------- */

const METRIC_KEY = { avg: 'avgMs', min: 'minMs', max: 'maxMs', jitter: 'jitterMs', loss: 'lossPct', availability: 'availability' };

/** HistorySeries → chart series. metric: avg|min|max|jitter|loss|availability */
export function toSeries(hs, metric = 'avg', color) {
  const key = METRIC_KEY[metric] || 'avgMs';
  return {
    name: hs.nodeName ? `${hs.nodeName} › ${hs.checkName}` : hs.checkName || `Check ${hs.checkId}`,
    color,
    checkId: hs.checkId,
    points: (hs.points || []).map((p) => ({
      t: +new Date(p.ts),
      v: p[key] == null ? null : Number(p[key]),
      avail: p.availability,
      min: p.minMs, max: p.maxMs,
    })),
  };
}

/* ---------- Sparkline ---------- */

export function sparkline(values, { width = 120, height = 28, color = null } = {}) {
  color = color || CSS.accent;
  const canvas = h('canvas', { class: 'sparkline', width, height, 'aria-hidden': 'true' });
  const dpr = window.devicePixelRatio || 1;
  // Same rule as the line chart: size the bitmap only when it is not already
  // the size we want, so a redraw never wipes a bitmap it could have kept.
  if (canvas.width !== Math.round(width * dpr)) canvas.width = Math.round(width * dpr);
  if (canvas.height !== Math.round(height * dpr)) canvas.height = Math.round(height * dpr);
  canvas.style.width = `${width}px`; canvas.style.height = `${height}px`;
  const ctx = canvas.getContext('2d');
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  const vals = values.filter((v) => v != null && isFinite(v));
  if (vals.length < 2) return canvas;
  const min = Math.min(...vals), max = Math.max(...vals);
  const n = values.length;
  const x = (i) => 2 + (i / (n - 1)) * (width - 4);
  const y = (v) => height - 3 - ((v - min) / Math.max(1e-9, max - min)) * (height - 6);
  ctx.strokeStyle = color; ctx.lineWidth = 1.5; ctx.lineJoin = 'round';
  ctx.beginPath();
  let started = false;
  values.forEach((v, i) => {
    if (v == null || !isFinite(v)) { started = false; return; }
    if (!started) { ctx.moveTo(x(i), y(v)); started = true; } else ctx.lineTo(x(i), y(v));
  });
  ctx.stroke();
  return canvas;
}

/* ---------- Uptime bar ---------- */

/**
 * Per-bucket availability bar. points: HistoryPoint[]; segments are merged
 * down to `maxSegments` (worst availability wins within a merged bucket).
 */
export function uptimeBar(points, { maxSegments = 90, bucketSeconds = 0, from, to } = {}) {
  const wrap = h('div', { class: 'uptime-bar', role: 'img', 'aria-label': 'Availability per period' });
  const pts = (points || []).map((p) => ({ t: +new Date(p.ts ?? p.t), avail: p.availability ?? p.avail, count: p.count })).filter((p) => isFinite(p.t)).sort((a, b) => a.t - b.t);
  if (!pts.length) {
    for (let i = 0; i < 30; i++) wrap.append(h('span', { class: 'seg none' }));
    return wrap;
  }
  const start = from != null ? +new Date(from) : pts[0].t;
  const end = to != null ? +new Date(to) : pts[pts.length - 1].t + (bucketSeconds || 60) * 1000;
  const segs = Math.min(maxSegments, Math.max(1, pts.length));
  const segMs = (end - start) / segs;
  const buckets = new Array(segs).fill(null).map(() => ({ min: null, sum: 0, n: 0 }));
  for (const p of pts) {
    let i = Math.floor((p.t - start) / segMs);
    if (i < 0) i = 0; if (i >= segs) i = segs - 1;
    const b = buckets[i];
    if (p.avail == null) continue;
    b.min = b.min == null ? p.avail : Math.min(b.min, p.avail);
    b.sum += p.avail; b.n++;
  }
  buckets.forEach((b, i) => {
    let cls = 'none'; let label = 'No data';
    if (b.n) {
      const avg = b.sum / b.n;
      cls = b.min >= 100 ? 'up' : b.min <= 0 && avg <= 0 ? 'down' : 'partial';
      label = `${fmtPct(avg)} available`;
    }
    const t0 = start + i * segMs;
    wrap.append(h('span', { class: `seg ${cls}`, title: `${dateTime(t0, { seconds: false })} — ${label}` }));
  });
  return wrap;
}

export function uptimeLegend() {
  return h('div', { class: 'uptime-legend' },
    h('span', { class: 'l-up' }, 'Up'), h('span', { class: 'l-partial' }, 'Partial'), h('span', { class: 'l-down' }, 'Down'), h('span', { class: 'l-none' }, 'No data'));
}

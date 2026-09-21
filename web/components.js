// Small DOM toolkit: element builder, icons, status pills, toasts, modals,
// dropdown menus, chip input, form fields, empty states and skeletons.

import { relTime, ms as fmtMs, metricLabel } from './fmt.js';

/* ---------- Element builder ---------- */

export function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  if (attrs && typeof attrs === 'object' && !(attrs instanceof Node) && !Array.isArray(attrs)) {
    for (const [k, v] of Object.entries(attrs)) {
      if (v == null || v === false) continue;
      if (k === 'class') el.className = v;
      else if (k === 'html') el.innerHTML = v;
      else if (k === 'text') el.textContent = v;
      else if (k === 'dataset') Object.assign(el.dataset, v);
      else if (k === 'style' && typeof v === 'object') Object.assign(el.style, v);
      else if (k.startsWith('on') && typeof v === 'function') el.addEventListener(k.slice(2).toLowerCase(), v);
      else if (k === 'value' && ('value' in el)) el.value = v;
      else if (k === 'checked' || k === 'disabled' || k === 'selected' || k === 'readOnly' || k === 'multiple') el[k] = !!v;
      else if (v === true) el.setAttribute(k, '');
      else el.setAttribute(k, String(v));
    }
  } else if (attrs != null) {
    children.unshift(attrs);
  }
  append(el, children);
  return el;
}

export function append(el, children) {
  for (const c of children) {
    if (c == null || c === false || c === true) continue;
    if (Array.isArray(c)) { append(el, c); continue; }
    if (c instanceof Node) el.appendChild(c);
    else {
      const text = typeof c === 'number' ? String(c) : String(c);
      if (text === 'null' || text === 'undefined') continue;
      el.appendChild(document.createTextNode(text));
    }
  }
  return el;
}

/** Text that is safe to interpolate: null/undefined become "". */
export function text(v, fallback = '') { return v == null ? fallback : String(v); }

export function clear(el) { while (el.firstChild) el.removeChild(el.firstChild); return el; }
export function replace(el, ...children) { clear(el); append(el, children); return el; }
export function frag(...children) { const f = document.createDocumentFragment(); append(f, children); return f; }

/* ---------- Icons (inline SVG, stroke based) ---------- */

const ICONS = {
  check: '<path d="M20 6 9 17l-5-5"/>',
  x: '<path d="M18 6 6 18M6 6l12 12"/>',
  alert: '<path d="M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z"/><path d="M12 9v4M12 17h.01"/>',
  question: '<circle cx="12" cy="12" r="10"/><path d="M9.1 9a3 3 0 0 1 5.8 1c0 2-3 3-3 3M12 17h.01"/>',
  pause: '<rect x="6" y="4" width="4" height="16" rx="1"/><rect x="14" y="4" width="4" height="16" rx="1"/>',
  wrench: '<path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"/>',
  dot: '<circle cx="12" cy="12" r="5" fill="currentColor" stroke="none"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
  minus: '<path d="M5 12h14"/>',
  play: '<path d="M6 4l14 8-14 8z" fill="currentColor" stroke="none"/>',
  edit: '<path d="M12 20h9"/><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4z"/>',
  copy: '<rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>',
  trash: '<path d="M3 6h18M8 6V4a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v2m2 0v14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2V6h12z"/>',
  more: '<circle cx="5" cy="12" r="1.6" fill="currentColor" stroke="none"/><circle cx="12" cy="12" r="1.6" fill="currentColor" stroke="none"/><circle cx="19" cy="12" r="1.6" fill="currentColor" stroke="none"/>',
  refresh: '<path d="M21 12a9 9 0 1 1-2.6-6.4"/><path d="M21 3v6h-6"/>',
  download: '<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4M7 10l5 5 5-5M12 15V3"/>',
  upload: '<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4M17 8l-5-5-5 5M12 3v12"/>',
  bell: '<path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9M13.7 21a2 2 0 0 1-3.4 0"/>',
  bellOff: '<path d="M13.7 21a2 2 0 0 1-3.4 0M18.6 13A17.9 17.9 0 0 1 18 8M6.3 6.3A6 6 0 0 0 6 8c0 7-3 9-3 9h14M18 8a6 6 0 0 0-9.3-5M1 1l22 22"/>',
  clock: '<circle cx="12" cy="12" r="10"/><path d="M12 6v6l4 2"/>',
  shield: '<path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/>',
  calendar: '<rect x="3" y="4" width="18" height="18" rx="2"/><path d="M16 2v4M8 2v4M3 10h18"/>',
  settings: '<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z"/>',
  grid: '<rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/>',
  activity: '<path d="M22 12h-4l-3 9L9 3l-3 9H2"/>',
  server: '<rect x="2" y="2" width="20" height="8" rx="2"/><rect x="2" y="14" width="20" height="8" rx="2"/><path d="M6 6h.01M6 18h.01"/>',
  monitor: '<rect x="2" y="3" width="20" height="14" rx="2"/><path d="M8 21h8M12 17v4"/>',
  search: '<circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/>',
  external: '<path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6M15 3h6v6M10 14 21 3"/>',
  arrowUp: '<path d="M12 19V5M5 12l7-7 7 7"/>',
  arrowDown: '<path d="M12 5v14M19 12l-7 7-7-7"/>',
  arrowLeft: '<path d="M19 12H5M12 19l-7-7 7-7"/>',
  arrowRight: '<path d="M5 12h14M12 5l7 7-7 7"/>',
  chevronDown: '<path d="m6 9 6 6 6-6"/>',
  chevronRight: '<path d="m9 18 6-6-6-6"/>',
  note: '<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><path d="M14 2v6h6M16 13H8M16 17H8M10 9H8"/>',
  globe: '<circle cx="12" cy="12" r="10"/><path d="M2 12h20M12 2a15 15 0 0 1 4 10 15 15 0 0 1-4 10 15 15 0 0 1-4-10 15 15 0 0 1 4-10z"/>',
  router: '<rect x="2" y="13" width="20" height="8" rx="2"/><path d="M6 17h.01M10 17h.01M12 9V3M8 6l4-3 4 3"/>',
  api: '<path d="m16 18 6-6-6-6M8 6l-6 6 6 6"/>',
  zap: '<path d="M13 2 3 14h9l-1 8 10-12h-9z"/>',
  moon: '<path d="M21 12.8A9 9 0 1 1 11.2 3 7 7 0 0 0 21 12.8z"/>',
  info: '<circle cx="12" cy="12" r="10"/><path d="M12 16v-4M12 8h.01"/>',
  save: '<path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2z"/><path d="M17 21v-8H7v8M7 3v5h8"/>',
  mail: '<rect x="2" y="4" width="20" height="16" rx="2"/><path d="m22 7-10 6L2 7"/>',
  database: '<ellipse cx="12" cy="5" rx="9" ry="3"/><path d="M21 12c0 1.7-4 3-9 3s-9-1.3-9-3M3 5v14c0 1.7 4 3 9 3s9-1.3 9-3V5"/>',
  heart: '<path d="M20.8 4.6a5.5 5.5 0 0 0-7.8 0L12 5.7l-1-1.1a5.5 5.5 0 0 0-7.8 7.8l1 1L12 21l7.8-7.6 1-1a5.5 5.5 0 0 0 0-7.8z"/>',
  link: '<path d="M10 13a5 5 0 0 0 7.5.5l3-3a5 5 0 0 0-7-7l-1.7 1.7"/><path d="M14 11a5 5 0 0 0-7.5-.5l-3 3a5 5 0 0 0 7 7l1.7-1.7"/>',
  file: '<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><path d="M14 2v6h6"/>',
  image: '<rect x="3" y="3" width="18" height="18" rx="2"/><circle cx="8.5" cy="8.5" r="1.5"/><path d="m21 15-5-5L5 21"/>',
  eye: '<path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/>',
  power: '<path d="M18.4 6.6a9 9 0 1 1-12.8 0M12 2v10"/>',
  layers: '<path d="m12 2 10 5-10 5L2 7z"/><path d="m2 17 10 5 10-5M2 12l10 5 10-5"/>',
  hash: '<path d="M4 9h16M4 15h16M10 3 8 21M16 3l-2 18"/>',
  lock: '<rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>',
  user: '<path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2"/><circle cx="12" cy="7" r="4"/>',
  users: '<path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 0 0-3-3.87M16 3.13a4 4 0 0 1 0 7.75"/>',
  key: '<circle cx="7.5" cy="15.5" r="4.5"/><path d="m10.7 12.3 8.3-8.3 3 3-2 2-2-2-2 2 2 2-3 3z"/>',
  home: '<path d="m3 9 9-7 9 7v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/><path d="M9 22V12h6v10"/>',
  tv: '<rect x="2" y="7" width="20" height="15" rx="2"/><path d="m17 2-5 5-5-5"/>',
  filter: '<path d="M22 3H2l8 9.5V19l4 2v-8.5z"/>',
  logout: '<path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4M16 17l5-5-5-5M21 12H9"/>',
  sun: '<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/>',
  sleep: '<path d="M21 12.8A9 9 0 1 1 11.2 3 7 7 0 0 0 21 12.8z"/>',
  gitCommit: '<circle cx="12" cy="12" r="4"/><path d="M1.05 12H7M17 12h5.95"/>',
  cpu: '<rect x="4" y="4" width="16" height="16" rx="2"/><rect x="9" y="9" width="6" height="6"/><path d="M9 1v3M15 1v3M9 20v3M15 20v3M20 9h3M20 14h3M1 9h3M1 14h3"/>',
  chart: '<path d="M3 3v18h18"/><path d="m7 15 4-5 3 3 6-8"/>',
  audit: '<path d="M8 6h13M8 12h13M8 18h13"/><path d="M3 6h.01M3 12h.01M3 18h.01"/>',
  pin: '<path d="M12 17v5M5 17h14l-2-6V4h1V2H6v2h1v7z"/>',
  grip: '<circle cx="9" cy="6" r="1.4" fill="currentColor" stroke="none"/><circle cx="15" cy="6" r="1.4" fill="currentColor" stroke="none"/><circle cx="9" cy="12" r="1.4" fill="currentColor" stroke="none"/><circle cx="15" cy="12" r="1.4" fill="currentColor" stroke="none"/><circle cx="9" cy="18" r="1.4" fill="currentColor" stroke="none"/><circle cx="15" cy="18" r="1.4" fill="currentColor" stroke="none"/>',
  code: '<path d="m16 18 6-6-6-6M8 6l-6 6 6 6"/>',
  terminal: '<path d="m4 17 6-6-6-6M12 19h8"/>',
  git: '<circle cx="12" cy="6" r="2.5"/><circle cx="6" cy="18" r="2.5"/><circle cx="18" cy="18" r="2.5"/><path d="M12 8.5v3a3 3 0 0 1-3 3H9a3 3 0 0 0-3 1.5M12 8.5v3a3 3 0 0 0 3 3h0a3 3 0 0 1 3 1.5"/>',
  webhook: '<path d="M18 16.98h-5.99c-1.1 0-1.95.94-2.48 1.9A4 4 0 0 1 2 17c.01-.7.2-1.4.57-2"/><path d="m6 17 3.13-5.78c.53-.97.1-2.18-.5-3.1a4 4 0 1 1 6.89-4.06"/><path d="m12 6 3.13 5.73C15.66 12.7 16.9 13 18 13a4 4 0 0 1 0 8"/>',
  palette: '<circle cx="13.5" cy="6.5" r="1.2" fill="currentColor"/><circle cx="17.5" cy="10.5" r="1.2" fill="currentColor"/><circle cx="8.5" cy="7.5" r="1.2" fill="currentColor"/><circle cx="6.5" cy="12.5" r="1.2" fill="currentColor"/><path d="M12 2C6.5 2 2 6.5 2 12s4.5 10 10 10c.9 0 1.5-.7 1.5-1.5 0-.4-.2-.8-.4-1-.3-.3-.4-.6-.4-1 0-.8.7-1.5 1.5-1.5H16c3.3 0 6-2.7 6-6 0-5-4.5-9-10-9z"/>',
  wifi: '<path d="M5 12.6a11 11 0 0 1 14 0M8.5 16a6 6 0 0 1 7 0M2 8.8a15.5 15.5 0 0 1 20 0M12 20h.01"/>',
  // A sweep hand over two rings, for discovery: the magnifying glass already
  // means "search this list", and this means "look out there".
  radar: '<path d="M19.1 4.9a10 10 0 1 1-8.2-2.8"/><path d="M15.5 8.5a5 5 0 1 0 .8 5.8"/><path d="M12 12 20 4"/><circle cx="12" cy="12" r="1.4" fill="currentColor" stroke="none"/>',
  rocket: '<path d="M4.5 16.5c-1.5 1.3-2 5-2 5s3.7-.5 5-2c.7-.8.7-2 0-2.8-.8-.7-2-.7-2.8 0z"/><path d="m12 15-3-3a22 22 0 0 1 2-3.95A12.9 12.9 0 0 1 22 2c0 2.7-.9 7.5-6 11a22 22 0 0 1-4 2z"/><path d="M9 12H4s.5-3 2-4 4 0 4 0M12 15v5s3-.5 4-2 0-4 0-4"/>',
  layout: '<rect x="3" y="3" width="18" height="18" rx="1"/><path d="M3 9h18M9 21V9"/>',
  sliders: '<path d="M4 21v-7M4 10V3M12 21v-9M12 8V3M20 21v-5M20 12V3M1 14h6M9 8h6M17 16h6"/>',
  // A lifebuoy rather than a question mark: the question mark already means
  // "unknown status" everywhere else in this interface.
  help: '<circle cx="12" cy="12" r="9.5"/><circle cx="12" cy="12" r="4"/><path d="m5.3 5.3 3.9 3.9M14.8 14.8l3.9 3.9M18.7 5.3l-3.9 3.9M9.2 14.8l-3.9 3.9"/>',
};

export function icon(name, cls = '') {
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('aria-hidden', 'true');
  svg.setAttribute('class', `icon ${cls}`.trim());
  svg.innerHTML = ICONS[name] || ICONS.dot;
  return svg;
}
export function iconInto(el, name) { el.appendChild(icon(name)); return el; }

/* ---------- Theme / accent ---------- */

const themeListeners = new Set();
export function onThemeChange(fn) { themeListeners.add(fn); return () => themeListeners.delete(fn); }
function notifyTheme() { for (const fn of themeListeners) { try { fn(); } catch (e) { console.error(e); } } }

let systemMedia = null;
/** Apply "dark" | "light" | "system" to the document and remember it locally. */
export function applyTheme(theme) {
  const t = ['dark', 'light', 'system'].includes(theme) ? theme : 'dark';
  try { localStorage.setItem('gw.theme', t); } catch { /* ignore */ }
  const resolve = () => (t === 'system' ? (window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark') : t);
  const before = document.documentElement.getAttribute('data-theme');
  document.documentElement.setAttribute('data-theme', resolve());
  if (systemMedia) { systemMedia.onchange = null; systemMedia = null; }
  if (t === 'system' && window.matchMedia) {
    systemMedia = window.matchMedia('(prefers-color-scheme: light)');
    systemMedia.onchange = () => { document.documentElement.setAttribute('data-theme', resolve()); notifyTheme(); };
  }
  if (before !== document.documentElement.getAttribute('data-theme')) notifyTheme();
}
export function currentTheme() { return document.documentElement.getAttribute('data-theme') || 'dark'; }

/* ---------- Density (#32) ---------- */
// A per-browser preference, like the pinned sidebar: there is no server
// setting for it, so it does not go through /api/settings and does not need
// an admin to change it. boot.js sets the same attribute pre-paint from the
// same localStorage key, so switching it here just keeps the two in step.
export function applyDensity(density) {
  const d = density === 'comfortable' ? 'comfortable' : 'compact';
  document.documentElement.setAttribute('data-density', d);
  try { localStorage.setItem('gw.density', d); } catch { /* ignore */ }
  notifyTheme();
}
export function currentDensity() { return document.documentElement.getAttribute('data-density') === 'comfortable' ? 'comfortable' : 'compact'; }

export function hexToRgb(hex) {
  const m = /^#?([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i.exec(hex || '');
  return m ? [parseInt(m[1], 16), parseInt(m[2], 16), parseInt(m[3], 16)] : null;
}
/** Apply an accent colour (hex) to the document. */
export function applyAccent(hex) {
  const rgb = hexToRgb(hex);
  if (!rgb) return;
  document.documentElement.style.setProperty('--accent-rgb', rgb.join(', '));
  try { localStorage.setItem('gw.accent', hex.toLowerCase()); } catch { /* ignore */ }
  notifyTheme();
}
export const ACCENT_PRESETS = [
  { name: 'Signal', hex: '#43c9c0' }, { name: 'Violet', hex: '#7c6cff' }, { name: 'Blue', hex: '#3b82f6' }, { name: 'Cyan', hex: '#06b6d4' },
  { name: 'Teal', hex: '#14b8a6' }, { name: 'Green', hex: '#22c55e' }, { name: 'Lime', hex: '#a3e635' }, { name: 'Amber', hex: '#f59e0b' },
  { name: 'Orange', hex: '#f97316' }, { name: 'Red', hex: '#ef4444' }, { name: 'Pink', hex: '#ec4899' }, { name: 'Gold', hex: '#d4a017' },
];
/** Read the current values of the design tokens used by canvas charts. */
export function cssColors() {
  const cs = getComputedStyle(document.documentElement);
  const v = (name, fb) => (cs.getPropertyValue(name) || fb).trim();
  const rgb = v('--accent-rgb', '67, 201, 192').split(',').map((x) => Number(x.trim()));
  const hex = '#' + rgb.map((n) => Math.max(0, Math.min(255, n || 0)).toString(16).padStart(2, '0')).join('');
  return {
    bg: v('--card', '#16181d'), line: v('--line', '#262a31'), lineStrong: v('--line-strong', '#333941'), muted: v('--muted', '#9aa3ac'), dim: v('--dim', '#6c757f'), text: v('--text', '#f2f4f6'),
    down: v('--down', '#ff5c5c'), up: v('--up', '#35e07f'), warn: v('--warn', '#ffc542'), accent: hex, accentRgb: rgb,
  };
}

/* ---------- Status ---------- */

export const STATUS = {
  up: { label: 'Up', icon: 'check', cls: 'status-up', color: 'var(--up)' },
  down: { label: 'Down', icon: 'x', cls: 'status-down', color: 'var(--down)' },
  degraded: { label: 'Degraded', icon: 'alert', cls: 'status-degraded', color: 'var(--warn)' },
  unknown: { label: 'Unknown', icon: 'question', cls: 'status-unknown', color: 'var(--unknown)' },
  paused: { label: 'Paused', icon: 'pause', cls: 'status-paused', color: 'var(--paused)' },
  maintenance: { label: 'Maintenance', icon: 'wrench', cls: 'status-maintenance', color: 'var(--maint)' },
};
export function statusMeta(status) { return STATUS[status] || STATUS.unknown; }

/** A status pill is a static label, not a live region: it is re-rendered with
 *  its row rather than updated in place, so it carries no role="status" —
 *  that would make a screen reader announce every pill on every redraw. */
export function statusPill(status, { label, large = false } = {}) {
  const m = statusMeta(status);
  return h('span', { class: `pill ${m.cls} ${large ? 'pill-lg' : ''}` }, icon(m.icon), label || m.label);
}

/* What each spine showed the last time it was drawn, so a row that has changed
   status since the previous render can flash. Keyed by the caller's row id. */
const spineWas = new Map();
function spineChanged(key, status) {
  if (!key) return false;
  const prev = spineWas.get(key);
  spineWas.set(key, status);
  return prev !== undefined && prev !== status;
}

/** The list-row counterpart to the pill: a 3px colour spine in its own grid
 *  column, with the status word set beside it in mono. The spine is decorative
 *  — `statusWord` carries the status as text, so nothing is lost without it.
 *  Pass `key` (a stable row id) and the spine flashes when the status changes. */
export function statusSpine(status, { size = '', key } = {}) {
  const name = status in STATUS ? status : 'unknown';
  const flash = spineChanged(key, name) && !prefersReducedMotion();
  return h('span', { class: `spine spine-${name}${size ? ` spine-${size}` : ''}${flash ? ' flash' : ''}`, 'aria-hidden': 'true' });
}

function prefersReducedMotion() {
  return typeof window.matchMedia === 'function' && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
}

/** The status word beside a spine, in the status colour. Static text, like
 *  the pill: no live-region role. */
export function statusWord(status, { label } = {}) {
  const m = statusMeta(status);
  const key = status in STATUS ? status : 'unknown';
  return h('span', { class: `status-word text-${key}` }, label || m.label);
}

export function statusGlyph(status, { text = true } = {}) {
  const m = statusMeta(status);
  const el = h('span', { class: `status-glyph text-${status in STATUS ? status : 'unknown'}` }, icon(m.icon));
  if (text) el.append(m.label);
  else el.append(h('span', { class: 'sr-only' }, m.label));
  return el;
}

/** A glowing status circle. The halo colour comes from the status class, so it
 *  stays right when the theme changes. `size` is 'sm' | '' | 'lg'. */
export function statusOrb(status, { size = '', label } = {}) {
  const m = statusMeta(status);
  const key = status in STATUS ? status : 'unknown';
  const el = h('span', { class: `orb orb-${key}${size ? ` orb-${size}` : ''}`, title: label || m.label });
  el.append(h('span', { class: 'sr-only' }, label || m.label));
  return el;
}

/** Compact "check chip": glyph + name + latency, for node rows. */
export function checkChip(check, state, { href } = {}) {
  const status = state?.status || (check.enabled === false ? 'paused' : 'unknown');
  const m = statusMeta(status);
  // The last message rides in the tooltip for a pointer, and in hidden text
  // for a screen reader: a title attribute alone is never read out on a span.
  const chip = h(href ? 'a' : 'span', { class: 'check-chip', href, title: `${check.name || 'Check'}: ${m.label}${state?.lastMessage ? ' — ' + state.lastMessage : ''}` },
    h('span', { class: `text-${status}`, style: { display: 'inline-flex' } }, icon(m.icon)),
    h('span', { class: 'sr-only' }, m.label + ' '),
    check.name || 'Check',
    state?.lastMessage ? h('span', { class: 'sr-only' }, ` — ${state.lastMessage}`) : null,
  );
  if (state?.lastLatencyMs != null && status !== 'paused') chip.append(h('span', { class: 'lat' }, fmtMs(state.lastLatencyMs)));
  return chip;
}

export function importanceBadge(importance) {
  if (!importance || importance === 'normal') return null;
  const icons = { low: 'minus', high: 'arrowUp', critical: 'zap' };
  return h('span', { class: `importance importance-${importance}` }, icon(icons[importance] || 'dot'), importance);
}

/* ---------- Toasts ---------- */

export function toast(message, { kind = 'info', timeout = 4500, action } = {}) {
  const root = document.getElementById('toasts');
  if (!root) return;
  const icons = { success: 'check', error: 'alert', info: 'info' };
  const el = h('div', { class: `toast toast-${kind}`, role: kind === 'error' ? 'alert' : 'status' }, icon(icons[kind] || 'info'), h('span', null, message));
  if (action) el.append(h('button', { class: 'btn btn-sm', onclick: () => { action.onClick(); el.remove(); } }, action.label));
  const closeBtn = h('button', { class: 'btn btn-ghost btn-sm icon-btn', 'aria-label': 'Dismiss', onclick: () => el.remove() }, icon('x'));
  if (!action) el.append(closeBtn);
  root.appendChild(el);
  if (timeout > 0) setTimeout(() => el.remove(), timeout);
  return el;
}

/* ---------- Modals ---------- */

const FOCUSABLE = 'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

/** The elements in `scope` a Tab press can land on. Judged by markup (hidden,
 *  disabled, inside a hidden ancestor) rather than by layout, so the answer is
 *  the same in a headless test as in a browser. */
export function focusableIn(scope) {
  return [...scope.querySelectorAll(FOCUSABLE)].filter((x) => !x.hidden && !x.disabled && !x.closest('[hidden]') && x.getAttribute('aria-hidden') !== 'true');
}

// How many modals are open. The page behind them is made inert by the first
// and released by the last, so a dialog opened from a dialog does not free it.
let modalDepth = 0;
function setAppInert(on) {
  const app = document.getElementById('app');
  if (!app) return;
  if (on) { app.setAttribute('inert', ''); app.setAttribute('aria-hidden', 'true'); } else { app.removeAttribute('inert'); app.removeAttribute('aria-hidden'); }
}

/** `describedBy` is the id of the element that explains the dialog (a
 *  confirm's message, say); it becomes the dialog's aria-describedby. */
export function openModal({ title, body, footer, wide = false, onClose, closeOnBackdrop = true, ariaLabel, describedBy }) {
  const root = document.getElementById('modals');
  const prevFocus = document.activeElement;
  // Named by its own heading, so what a screen reader announces is exactly
  // what is written at the top of the box. A caller with a different name in
  // mind passes ariaLabel, which then wins.
  const titleId = uid('modal-title');
  const dialog = h('div', { class: `modal ${wide ? 'modal-wide' : ''}`, role: 'dialog', 'aria-modal': 'true', 'aria-labelledby': ariaLabel ? null : titleId, 'aria-label': ariaLabel || null, 'aria-describedby': describedBy || null });
  const backdrop = h('div', { class: 'modal-backdrop' }, dialog);
  const close = (result) => {
    if (!backdrop.isConnected) return;
    backdrop.remove();
    document.removeEventListener('keydown', onKey);
    // Release the page before focus goes back to it: an inert element cannot
    // take focus, so the order matters.
    if (--modalDepth <= 0) { modalDepth = 0; setAppInert(false); }
    if (onClose) onClose(result);
    // Back to where focus came from; if that element has since gone (a row
    // the dialog deleted, say), to the page content rather than to nowhere.
    const back = prevFocus && prevFocus.isConnected && prevFocus.focus ? prevFocus : document.getElementById('view');
    if (back) { try { back.focus(); } catch { /* ignore */ } }
  };
  const onKey = (e) => {
    // Only the topmost dialog answers the keyboard.
    if (root.lastElementChild !== backdrop) return;
    if (e.key === 'Escape') { e.preventDefault(); close(undefined); }
    if (e.key === 'Tab') {
      const f = focusableIn(dialog);
      if (!f.length) { e.preventDefault(); return; }
      const first = f[0]; const last = f[f.length - 1];
      const inside = dialog.contains(document.activeElement);
      if (e.shiftKey && (document.activeElement === first || !inside)) { e.preventDefault(); last.focus(); }
      else if (!e.shiftKey && (document.activeElement === last || !inside)) { e.preventDefault(); first.focus(); }
    }
  };
  const head = h('div', { class: 'modal-head' }, h('h2', { id: titleId }, title || ''), h('button', { class: 'btn btn-ghost icon-btn', type: 'button', 'aria-label': 'Close', onclick: () => close(undefined) }, icon('x')));
  const bodyEl = h('div', { class: 'modal-body' });
  append(bodyEl, [body]);
  dialog.append(head, bodyEl);
  if (footer) { const f = h('div', { class: 'modal-foot' }); append(f, [footer]); dialog.append(f); }
  backdrop.addEventListener('mousedown', (e) => { if (closeOnBackdrop && e.target === backdrop) close(undefined); });
  document.addEventListener('keydown', onKey);
  root.appendChild(backdrop);
  if (++modalDepth === 1) setAppInert(true);
  requestAnimationFrame(() => {
    if (!backdrop.isConnected) return;
    const first = focusableIn(bodyEl)[0] || focusableIn(dialog)[0];
    if (first) first.focus();
  });
  return { el: dialog, body: bodyEl, close };
}

export function confirmDialog({ title = 'Are you sure?', message, confirmLabel = 'Confirm', cancelLabel = 'Cancel', danger = false, body } = {}) {
  return new Promise((resolve) => {
    let result = false;
    const msgId = message ? uid('modal-desc') : null;
    const m = openModal({
      title,
      describedBy: msgId,
      body: [message ? h('p', { id: msgId }, message) : null, body],
      footer: [
        h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, cancelLabel),
        h('button', { class: `btn ${danger ? 'btn-danger' : 'btn-primary'}`, type: 'button', onclick: () => { result = true; m.close(); } }, confirmLabel),
      ],
      onClose: () => resolve(result),
    });
  });
}

export function promptDialog({ title, message, label = 'Name', value = '', placeholder = '', type = 'text', confirmLabel = 'Save', required = true } = {}) {
  return new Promise((resolve) => {
    let result = null;
    const input = h('input', { type, value, placeholder, id: 'prompt-input', autocomplete: type === 'password' ? 'current-password' : 'off' });
    const form = h('form', { onsubmit: (e) => { e.preventDefault(); if (required && !input.value.trim()) { input.focus(); return; } result = input.value; m.close(); } },
      message ? h('p', { style: { marginBottom: '14px' } }, message) : null,
      field({ label, input }),
    );
    const m = openModal({
      title,
      body: form,
      footer: [
        h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, 'Cancel'),
        h('button', { class: 'btn btn-primary', type: 'button', onclick: () => form.requestSubmit() }, confirmLabel),
      ],
      onClose: () => resolve(result),
    });
  });
}

/* ---------- Dropdown menu ---------- */

let openMenu = null;
export function closeMenus() {
  if (!openMenu) return;
  const menu = openMenu; openMenu = null;
  const anchor = menu._anchor;
  // Focus goes back to the button that opened the menu — but only when the
  // menu had it. A click elsewhere is already moving focus to what was
  // clicked, and must not be pulled back.
  const hadFocus = menu.contains(document.activeElement) || document.activeElement === document.body;
  menu.remove();
  document.removeEventListener('mousedown', onDocDown, true);
  document.removeEventListener('keydown', onDocKey, true);
  if (anchor && anchor.isConnected) {
    if (anchor.hasAttribute('aria-haspopup')) anchor.setAttribute('aria-expanded', 'false');
    anchor.removeAttribute('aria-controls');
    if (hadFocus) { try { anchor.focus(); } catch { /* ignore */ } }
  }
}
function onDocDown(e) { if (openMenu && !openMenu.contains(e.target)) closeMenus(); }
// Escape closes only the menu: stopping the event here keeps a dialog the
// menu was opened from open underneath it.
function onDocKey(e) { if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); closeMenus(); } }

/** Roving tabindex inside an open menu: one item is in the tab order at a
 *  time, arrows move between the enabled ones, Home and End jump to the ends,
 *  and Tab leaves (which closes the menu and carries on from its button). */
function focusMenuItem(menu, item) {
  for (const el of menu.querySelectorAll('.menu-item')) el.tabIndex = el === item ? 0 : -1;
  item.focus();
}
function onMenuKey(e) {
  const menu = e.currentTarget;
  const items = [...menu.querySelectorAll('.menu-item:not([disabled])')];
  if (!items.length) return;
  const i = items.indexOf(document.activeElement);
  let next = null;
  switch (e.key) {
    case 'ArrowDown': next = items[(i + 1) % items.length]; break;
    case 'ArrowUp': next = items[(i - 1 + items.length) % items.length]; break;
    case 'Home': next = items[0]; break;
    case 'End': next = items[items.length - 1]; break;
    case 'Tab': closeMenus(); return;
    default: return;
  }
  e.preventDefault();
  focusMenuItem(menu, next);
}

/** items: [{label, icon, onClick, danger, href, download, sep, adminOnly}]
 *  An `adminOnly` item is hidden from an account without the admin role. The
 *  menu is appended to <body>, so the `body.viewer` rule still reaches it. */
export function showMenu(anchor, items) {
  closeMenus();
  const menu = h('div', { class: 'menu', role: 'menu', id: uid('menu'), onkeydown: onMenuKey });
  for (const it of items) {
    if (!it) continue;
    if (it.sep) { menu.append(h('div', { class: `menu-sep ${it.adminOnly ? 'admin-only' : ''}`, role: 'separator' })); continue; }
    const cls = `menu-item ${it.danger ? 'danger' : ''} ${it.adminOnly ? 'admin-only' : ''}`;
    const el = it.href
      ? h('a', { class: cls, role: 'menuitem', tabindex: -1, href: it.href, download: it.download || null, target: it.target || null, onclick: () => closeMenus() }, it.icon ? icon(it.icon) : null, it.label)
      : h('button', { class: cls, role: 'menuitem', tabindex: -1, type: 'button', disabled: !!it.disabled, onclick: () => { closeMenus(); it.onClick && it.onClick(); } }, it.icon ? icon(it.icon) : null, it.label);
    menu.append(el);
  }
  menu._anchor = anchor;
  document.body.appendChild(menu);
  const r = anchor.getBoundingClientRect();
  const mw = menu.offsetWidth; const mh = menu.offsetHeight;
  let left = r.right - mw; let top = r.bottom + 6;
  if (left < 8) left = 8;
  if (top + mh > window.innerHeight - 8) top = Math.max(8, r.top - mh - 6);
  menu.style.left = `${left}px`; menu.style.top = `${top}px`;
  openMenu = menu;
  if (anchor.hasAttribute('aria-haspopup')) anchor.setAttribute('aria-expanded', 'true');
  anchor.setAttribute('aria-controls', menu.id);
  document.addEventListener('mousedown', onDocDown, true);
  document.addEventListener('keydown', onDocKey, true);
  const first = menu.querySelector('.menu-item:not([disabled])');
  if (first) focusMenuItem(menu, first);
  return menu;
}

export function menuButton(items, { label = 'More actions', small = false, cls = '' } = {}) {
  const btn = h('button', { class: `btn icon-btn ${small ? 'btn-sm' : ''} ${cls}`, type: 'button', 'aria-label': label, 'aria-haspopup': 'menu', 'aria-expanded': 'false' }, icon('more'));
  btn.addEventListener('click', (e) => { e.stopPropagation(); showMenu(btn, typeof items === 'function' ? items() : items); });
  return btn;
}

/* ---------- Form helpers ---------- */

let idSeq = 0;
export function uid(prefix = 'f') { return `${prefix}-${++idSeq}`; }

export function field({ label, input, help, error, id, cls = '' }) {
  // The label and the descriptions point at the element that actually takes
  // the input, not at whatever wraps it: a composite control (chipInput, say)
  // exposes that element as `.input`; a plain wrapper — a number beside its
  // unit, a select beside a field — holds it as its first form control.
  const isControl = (el) => el instanceof Element && el.matches('input, select, textarea, button');
  const control = input?.input instanceof Node ? input.input : (isControl(input) || !(input instanceof Element)) ? input : (input.querySelector('input, select, textarea') || input);
  const fid = id || control?.id || uid();
  if (control && control.id !== fid) control.id = fid;
  const helpId = help ? `${fid}-help` : null;
  const errorId = `${fid}-error`;
  const el = h('div', { class: `field ${cls} ${error ? 'has-error' : ''}` },
    label ? h('label', { for: fid }, label) : null,
    input,
    help ? h('div', { class: 'help', id: helpId }, help) : null,
    error ? h('div', { class: 'error', id: errorId, role: 'alert' }, error) : null,
  );
  // The help text and the error are announced with the control, and an error
  // also marks it invalid — aria-invalid only means something on a form
  // control, so a plain wrapper does not get it.
  const describe = () => {
    if (!(control instanceof Element)) return;
    const hasError = !!el.querySelector('.error');
    const ids = [helpId, hasError ? errorId : null].filter(Boolean);
    if (ids.length) control.setAttribute('aria-describedby', ids.join(' ')); else control.removeAttribute('aria-describedby');
    if (!('validity' in control)) return;
    if (hasError) control.setAttribute('aria-invalid', 'true'); else control.removeAttribute('aria-invalid');
  };
  describe();
  el.setError = (msg) => {
    el.querySelector('.error')?.remove();
    el.classList.toggle('has-error', !!msg);
    if (msg) el.append(h('div', { class: 'error', id: errorId, role: 'alert' }, msg));
    describe();
  };
  return el;
}

export function textInput(attrs = {}) { return h('input', { type: 'text', ...attrs }); }
export function numberInput(attrs = {}) { return h('input', { type: 'number', ...attrs }); }
export function textarea(attrs = {}) { return h('textarea', attrs); }

/** options: [{value, label, disabled}] or ['a','b'] */
export function selectInput({ options, value, ...attrs } = {}) {
  const sel = h('select', attrs);
  for (const o of options || []) {
    const opt = typeof o === 'object' ? o : { value: o, label: o };
    sel.append(h('option', { value: opt.value, disabled: !!opt.disabled }, opt.label ?? opt.value));
  }
  if (value !== undefined) sel.value = String(value);
  return sel;
}

export function checkbox({ label, checked = false, onChange, id, disabled } = {}) {
  const input = h('input', { type: 'checkbox', checked, id: id || uid('cb'), disabled: !!disabled });
  if (onChange) input.addEventListener('change', () => onChange(input.checked));
  const el = h('label', { class: 'checkbox', for: input.id }, input, h('span', null, label));
  el.input = input;
  return el;
}

export function toggle({ label, checked = false, onChange, id, disabled, ariaLabel } = {}) {
  const input = h('input', { type: 'checkbox', role: 'switch', checked, id: id || uid('sw'), disabled: !!disabled, 'aria-label': ariaLabel || null });
  if (onChange) input.addEventListener('change', () => onChange(input.checked));
  const el = h('label', { class: 'switch', for: input.id }, input, h('span', { class: 'track', 'aria-hidden': 'true' }), label ? h('span', null, label) : null);
  el.input = input;
  return el;
}

/** Chip / tag input. `.value` returns an array of strings. */
export function chipInput({ values = [], placeholder = 'Add…', suggestions = [], onChange, validate, id } = {}) {
  let items = [...values];
  const listId = suggestions.length ? uid('dl') : null;
  const input = h('input', { type: 'text', placeholder, id: id || uid('chip'), list: listId, autocomplete: 'off' });
  const wrap = h('div', { class: 'chip-input', onclick: (e) => { if (e.target === wrap) input.focus(); } });
  if (listId) wrap.append(h('datalist', { id: listId }, suggestions.map((s) => h('option', { value: s }))));
  // The chips are a list to a screen reader (display: contents keeps them
  // flowing beside the input), and each add or remove is announced through a
  // polite live region, since the chips themselves are redrawn silently.
  const list = h('span', { class: 'chip-items', role: 'list' });
  const live = h('span', { class: 'sr-only', 'aria-live': 'polite' });
  const announce = (msg) => { live.textContent = ''; live.textContent = msg; };
  const remove = (i) => {
    const [v] = items.splice(i, 1);
    render();
    announce(`${v} removed`);
    // The remove button just went with its chip; focus goes to the box.
    input.focus();
    onChange && onChange(items);
  };
  const render = () => {
    clear(list);
    items.forEach((v, i) => {
      list.append(h('span', { class: 'chip-item', role: 'listitem' }, v, h('button', { type: 'button', 'aria-label': `Remove ${v}`, onclick: () => remove(i) }, icon('x'))));
    });
  };
  const commit = () => {
    const raw = input.value.split(/[,\n;]+/).map((s) => s.trim()).filter(Boolean);
    const added = [];
    for (const v of raw) {
      if (validate && !validate(v)) { input.setCustomValidity('Invalid value'); input.reportValidity(); continue; }
      if (!items.includes(v)) { items.push(v); added.push(v); }
    }
    input.value = '';
    input.setCustomValidity('');
    if (added.length) { render(); announce(`${added.join(', ')} added`); onChange && onChange(items); }
  };
  input.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' || e.key === ',') { e.preventDefault(); commit(); }
    else if (e.key === 'Backspace' && !input.value && items.length) { remove(items.length - 1); }
  });
  input.addEventListener('blur', commit);
  input.addEventListener('change', commit);
  wrap.append(list, input, live);
  render();
  Object.defineProperty(wrap, 'value', { get: () => [...items], set: (v) => { items = [...(v || [])]; render(); } });
  wrap.input = input;
  return wrap;
}

/* ---------- Misc ---------- */

export function emptyState({ icon: ic = 'info', title, text, actions, compact = false } = {}) {
  return h('div', { class: `empty ${compact ? 'empty-compact' : ''}` },
    h('span', { class: 'empty-icon' }, icon(ic)),
    title ? h('h3', null, title) : null,
    text ? h('p', null, text) : null,
    actions ? h('div', { class: 'btn-group' }, actions) : null,
  );
}

export function skeleton({ lines = 3, height } = {}) {
  if (height) return h('div', { class: 'skeleton-block', style: { height: typeof height === 'number' ? `${height}px` : height }, 'aria-busy': 'true' });
  const el = h('div', { class: 'skeleton', 'aria-busy': 'true' });
  for (let i = 0; i < lines; i++) el.append(h('div', { class: 'skeleton-line', style: { width: `${100 - (i * 17) % 45}%` } }));
  return el;
}

export function banner(kind, text, { icon: ic, actions } = {}) {
  const icons = { warn: 'alert', down: 'x', maint: 'wrench', info: 'info', up: 'check' };
  const el = h('div', { class: `banner banner-${kind}` }, icon(ic || icons[kind] || 'info'), h('div', { style: { flex: '1' } }, text));
  if (actions) el.append(h('div', { class: 'btn-group' }, actions));
  return el;
}

export function rangeChips(current, onChange, { ranges = ['1h', '24h', '7d', '30d', '1y'], small = true } = {}) {
  const wrap = h('div', { class: 'range-chips', role: 'group', 'aria-label': 'Time range' });
  for (const r of ranges) {
    const b = h('button', { type: 'button', class: `chip ${r === current ? 'active' : ''}`, 'aria-pressed': r === current ? 'true' : 'false', onclick: () => { if (r !== current) onChange(r); } }, r);
    wrap.append(b);
  }
  return wrap;
}

export function relTimeEl(v, { prefix = '' } = {}) {
  const el = h('span', { title: v ? new Date(v).toLocaleString() : '' }, prefix + relTime(v));
  el.dataset.rel = v || '';
  return el;
}

export function tagList(tags, { group } = {}) {
  const wrap = h('span', { class: 'n-tags' });
  if (group) wrap.append(h('span', { class: 'tag tag-group' }, group));
  for (const t of tags || []) wrap.append(h('span', { class: 'tag' }, t));
  return wrap;
}

export function busy(btn, label) {
  const orig = btn.innerHTML;
  btn.disabled = true;
  if (label) btn.textContent = label;
  return () => { btn.disabled = false; btn.innerHTML = orig; };
}

/* ---------- Event type metadata for timelines ---------- */

export const EVENT_META = {
  down: { label: 'Went down', icon: 'x', cls: 'ev-down' },
  recovered: { label: 'Recovered', icon: 'check', cls: 'ev-up' },
  warning: { label: 'Warning', icon: 'alert', cls: 'ev-warn' },
  warning_cleared: { label: 'Warning cleared', icon: 'check', cls: 'ev-up' },
  cert_warning: { label: 'Certificate warning', icon: 'shield', cls: 'ev-warn' },
  cert_warning_cleared: { label: 'Certificate warning cleared', icon: 'shield', cls: 'ev-up' },
  content_changed: { label: 'Response changed', icon: 'eye', cls: 'ev-warn' },
  alert_sent: { label: 'Alert sent', icon: 'mail', cls: 'ev-info' },
  alert_suppressed: { label: 'Alert suppressed', icon: 'bellOff', cls: 'ev-neutral' },
  alert_failed: { label: 'Alert failed', icon: 'alert', cls: 'ev-down' },
  silenced: { label: 'Silenced', icon: 'bellOff', cls: 'ev-neutral' },
  unsilenced: { label: 'Unsilenced', icon: 'bell', cls: 'ev-neutral' },
  maintenance_began: { label: 'Maintenance began', icon: 'wrench', cls: 'ev-maint' },
  maintenance_ended: { label: 'Maintenance ended', icon: 'wrench', cls: 'ev-maint' },
  config_changed: { label: 'Configuration changed', icon: 'settings', cls: 'ev-neutral' },
  affected_by_parent: { label: 'Affected by parent', icon: 'link', cls: 'ev-maint' },
  service_started: { label: 'Service started', icon: 'power', cls: 'ev-info' },
  service_stopped: { label: 'Service stopped', icon: 'power', cls: 'ev-neutral' },
  monitor_gap: { label: 'Monitoring gap', icon: 'moon', cls: 'ev-warn' },
  internal_error: { label: 'Internal error', icon: 'alert', cls: 'ev-down' },
  backup: { label: 'Backup', icon: 'save', cls: 'ev-info' },
  restore: { label: 'Restore', icon: 'upload', cls: 'ev-info' },
  retention: { label: 'Retention', icon: 'database', cls: 'ev-neutral' },
  note: { label: 'Note', icon: 'note', cls: 'ev-info' },
  trigger_fired: { label: 'Trigger', icon: 'zap', cls: 'ev-info' },
  endpoint_called: { label: 'Endpoint', icon: 'webhook', cls: 'ev-info' },
  update: { label: 'Update', icon: 'rocket', cls: 'ev-info' },
  auth: { label: 'Sign-in & accounts', icon: 'user', cls: 'ev-info' },
  discovery: { label: 'Discovery', icon: 'radar', cls: 'ev-info' },
};
export function eventMeta(type) { return EVENT_META[type] || { label: type, icon: 'info', cls: 'ev-neutral' }; }

export function eventIcon(type) {
  const m = eventMeta(type);
  return h('span', { class: `ev-icon ${m.cls}`, title: m.label }, icon(m.icon));
}

/** Compact event row used in widgets and the node detail page. */
export function eventRow(ev, { showNode = true, now = Date.now() } = {}) {
  const m = eventMeta(ev.type);
  const link = ev.nodeId ? `#/nodes/${ev.nodeId}` : null;
  const title = h('div', { class: 'ev-title' });
  if (showNode && ev.nodeName) title.append(link ? h('a', { href: link, style: { color: 'inherit' } }, ev.nodeName) : ev.nodeName, ev.checkName ? ` › ${ev.checkName}` : '', ' — ');
  title.append(ev.title || m.label);
  // An event about one of a check's metrics says which one, so a disk filling
  // up and the memory on the same check read as the two incidents they are.
  if (ev.metric) title.append(' ', h('span', { class: 'tag ev-metric', title: ev.metric }, metricLabel(ev.metric)));
  // Who caused it. The monitoring engine leaves this empty, so the line only
  // appears for things a person or an integration did.
  const detail = h('div', { class: 'ev-detail' });
  if (ev.detail) detail.append(ev.detail);
  if (ev.actor) detail.append(h('span', { class: 'ev-actor', title: 'Who made this change' }, icon('user'), ev.actor));
  return h('div', { class: 'event-row' },
    eventIcon(ev.type),
    h('div', { class: 'ev-body' }, title, detail.childNodes.length ? detail : null),
    h('div', { class: 'ev-time', title: new Date(ev.ts).toLocaleString() }, relTime(ev.ts, now)),
  );
}

/** Group checks by node for pickers: returns [{node, checks}] */
export function checkOptions(nodes, filterType) {
  const out = [];
  for (const n of nodes || []) {
    const checks = (n.checks || []).filter((c) => !filterType || filterType(c));
    if (checks.length) out.push({ node: n, checks });
  }
  return out;
}

/** Multi-select list of checks with "Node › Check" labels. `.value` → array of ids. */
export function checkMultiSelect(nodes, selected = [], { filterType, onChange } = {}) {
  let sel = new Set((selected || []).map(Number));
  const wrap = h('div', { class: 'check-list', role: 'group', 'aria-label': 'Checks' });
  const groups = checkOptions(nodes, filterType);
  if (!groups.length) wrap.append(h('div', { class: 'note' }, 'No matching checks yet.'));
  for (const g of groups) {
    for (const c of g.checks) {
      const cb = checkbox({ label: `${g.node.name} › ${c.name}`, checked: sel.has(Number(c.id)), onChange: (v) => { if (v) sel.add(Number(c.id)); else sel.delete(Number(c.id)); onChange && onChange([...sel]); } });
      cb.append(h('span', { class: 'tag', style: { marginLeft: 'auto' } }, c.type));
      wrap.append(cb);
    }
  }
  Object.defineProperty(wrap, 'value', { get: () => [...sel] });
  return wrap;
}

export const CHECK_TYPES = [
  { value: 'ping', label: 'Ping', desc: 'Is it reachable, and how fast does it answer? Uses ICMP echo (ping).' },
  { value: 'http', label: 'HTTP/S', desc: 'Load a web page or API URL and check the response code and response time.' },
  { value: 'cert', label: 'HTTPS certificate', desc: 'Check that the TLS certificate is valid and warn before it expires.' },
  { value: 'tcp', label: 'TCP port', desc: 'Can a connection be opened on a port? Good for SSH (22), Plex (32400), databases.' },
  { value: 'dns', label: 'DNS', desc: 'Does the hostname resolve, optionally to the addresses you expect?' },
  { value: 'keyword', label: 'Keyword', desc: 'Load a page and check that some text is present (or absent).' },
  { value: 'json', label: 'JSON', desc: 'Call an API and check that a value at a path matches what you expect.' },
  { value: 'custom', label: 'Custom script', desc: 'Run your own command on schedule and parse its status from the output.' },
  { value: 'system', label: 'Hardware health', desc: 'Processor, memory, disk space and throughput — for this computer, or for a machine running the agent.' },
  { value: 'snmp', label: 'SNMP', desc: 'Read a router, switch or access point directly: interface traffic and errors, processor load, uptime.' },
];
export function checkTypeLabel(t) { return (CHECK_TYPES.find((x) => x.value === t) || { label: t }).label; }

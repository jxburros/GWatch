// Formatting helpers: relative times, durations, milliseconds, percentages, bytes.

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
const DAYS = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];
const DAYS_SHORT = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];

export function toDate(v) {
  if (v == null || v === '') return null;
  if (v instanceof Date) return isNaN(v) ? null : v;
  const d = new Date(v);
  return isNaN(d) ? null : d;
}

function pad(n) { return String(n).padStart(2, '0'); }

/** "12 s ago", "3 min ago", "2 h ago", "5 d ago"; future values become "in 42 s". */
export function relTime(v, now = Date.now()) {
  const d = toDate(v);
  if (!d) return '—';
  const diff = (now - d.getTime()) / 1000;
  const abs = Math.abs(diff);
  const future = diff < 0;
  let s;
  if (abs < 5) s = future ? 'a moment' : 'just now';
  else if (abs < 60) s = `${Math.round(abs)} s`;
  else if (abs < 3600) s = `${Math.round(abs / 60)} min`;
  else if (abs < 86400) { const h = abs / 3600; s = h < 10 ? `${h.toFixed(h % 1 > 0.05 ? 1 : 0)} h` : `${Math.round(h)} h`; }
  else if (abs < 86400 * 30) s = `${Math.round(abs / 86400)} d`;
  else if (abs < 86400 * 365) s = `${Math.round(abs / (86400 * 30))} mo`;
  else s = `${(abs / (86400 * 365)).toFixed(1)} y`;
  if (s === 'just now') return s;
  return future ? `in ${s}` : `${s} ago`;
}

/** Duration in seconds → "45 s", "1 h 12 m", "3 d 2 h". */
export function duration(seconds) {
  if (seconds == null || isNaN(seconds)) return '—';
  seconds = Math.max(0, Math.round(seconds));
  if (seconds < 60) return `${seconds} s`;
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (d > 0) return h > 0 ? `${d} d ${h} h` : `${d} d`;
  if (h > 0) return m > 0 ? `${h} h ${m} m` : `${h} h`;
  const s = seconds % 60;
  return s > 0 && m < 10 ? `${m} m ${s} s` : `${m} min`;
}

/** Milliseconds → "12 ms", "1.24 s". */
export function ms(v, digits) {
  if (v == null || isNaN(v)) return '—';
  const n = Number(v);
  if (n >= 10000) return `${(n / 1000).toFixed(1)} s`;
  if (n >= 1000) return `${(n / 1000).toFixed(2)} s`;
  if (digits != null) return `${n.toFixed(digits)} ms`;
  if (n >= 100) return `${Math.round(n)} ms`;
  if (n >= 10) return `${n.toFixed(1)} ms`;
  return `${n.toFixed(n < 1 ? 2 : 1)} ms`;
}

export function pct(v, digits = 1) {
  if (v == null || isNaN(v)) return '—';
  const n = Number(v);
  if (n === 100 || n === 0) return `${n}%`;
  return `${n.toFixed(digits)}%`;
}

export function bytes(n) {
  if (n == null || isNaN(n)) return '—';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0; let v = Number(n);
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return `${v < 10 && i > 0 ? v.toFixed(1) : Math.round(v)} ${units[i]}`;
}

export function num(n) {
  if (n == null || isNaN(n)) return '—';
  return Number(n).toLocaleString();
}

/** "18 Sep 2026, 14:32:05" */
export function dateTime(v, { seconds = true } = {}) {
  const d = toDate(v);
  if (!d) return '—';
  return `${d.getDate()} ${MONTHS[d.getMonth()]} ${d.getFullYear()}, ${pad(d.getHours())}:${pad(d.getMinutes())}${seconds ? ':' + pad(d.getSeconds()) : ''}`;
}

export function dateShort(v) {
  const d = toDate(v);
  if (!d) return '—';
  return `${d.getDate()} ${MONTHS[d.getMonth()]} ${d.getFullYear()}`;
}

export function timeShort(v, { seconds = false } = {}) {
  const d = toDate(v);
  if (!d) return '—';
  return `${pad(d.getHours())}:${pad(d.getMinutes())}${seconds ? ':' + pad(d.getSeconds()) : ''}`;
}

/** "Today", "Yesterday", "Tuesday 16 September". */
export function dayHeading(v, now = new Date()) {
  const d = toDate(v);
  if (!d) return '—';
  const sameDay = (a, b) => a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
  if (sameDay(d, now)) return 'Today';
  const y = new Date(now); y.setDate(y.getDate() - 1);
  if (sameDay(d, y)) return 'Yesterday';
  const long = ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December'];
  const year = d.getFullYear() === now.getFullYear() ? '' : ` ${d.getFullYear()}`;
  return `${DAYS[d.getDay()]} ${d.getDate()} ${long[d.getMonth()]}${year}`;
}

export function dayKey(v) {
  const d = toDate(v);
  return d ? `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}` : '';
}

/** Interval seconds → "30 s", "1 min", "5 min", "1 h". */
export function interval(seconds) {
  if (seconds == null || isNaN(seconds)) return '—';
  seconds = Number(seconds);
  if (seconds < 60) return `${seconds} s`;
  if (seconds % 3600 === 0) return seconds === 3600 ? '1 h' : `${seconds / 3600} h`;
  if (seconds % 60 === 0) return `${seconds / 60} min`;
  return duration(seconds);
}

export const RANGES = ['1h', '24h', '7d', '30d', '1y'];
export function rangeLabel(r) {
  return { '1h': '1 hour', '24h': '24 hours', '7d': '7 days', '30d': '30 days', '1y': '1 year' }[r] || r;
}
export function rangeMs(r) {
  return { '1h': 3600e3, '24h': 86400e3, '7d': 7 * 86400e3, '30d': 30 * 86400e3, '1y': 365 * 86400e3 }[r] || 86400e3;
}

export function plural(n, singular, pluralForm) {
  return `${n} ${n === 1 ? singular : (pluralForm || singular + 's')}`;
}

export function weekdayShort(i) { return DAYS_SHORT[i] || ''; }
export function weekdayLong(i) { return DAYS[i] || ''; }

/** For <input type="datetime-local"> values (local time, no seconds). */
export function toLocalInput(v) {
  const d = toDate(v);
  if (!d) return '';
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}
export function fromLocalInput(s) {
  if (!s) return null;
  const d = new Date(s);
  return isNaN(d) ? null : d.toISOString();
}

/** Retention days → "30 days", "6 months", "2 years", "forever". */
export function retentionSpan(days) {
  const n = Number(days);
  if (!n || n <= 0) return 'forever';
  if (n % 365 === 0) return plural(n / 365, 'year');
  if (n % 30 === 0 && n >= 60) return plural(n / 30, 'month');
  if (n % 7 === 0 && n >= 14 && n < 60) return plural(n / 7, 'week');
  return plural(n, 'day');
}

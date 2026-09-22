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

/** True while the version number is still pre-1.0, which is what GWatch uses
    to mean "beta". Nothing has to be switched off at 1.0: the first release
    numbered 1.x stops reporting itself as a beta on its own. A dev or CI build
    is not a beta — it is something else entirely, and says so already. */
export function isBeta(version) {
  const v = String(version || '').trim().replace(/^v/, '');
  return /^0\./.test(v);
}

/** The groups a node belongs to. A node carries `groups`, a list; `group` is
    the deprecated alias for the first of them, and is all an older server (or
    a hand-written fixture) sends, so it is read as the one group it names. */
export function nodeGroups(n) {
  if (!n) return [];
  if (Array.isArray(n.groups) && n.groups.length) return n.groups;
  const g = (n.group || '').trim();
  return g ? [g] : [];
}

/** True when the node is in the named group. A filter names one group and a
    node may be in several, so any one of them is a match. */
export function inGroup(n, group) {
  const want = String(group || '').trim().toLowerCase();
  if (!want) return false;
  return nodeGroups(n).some((g) => String(g).trim().toLowerCase() === want);
}

/* ---------- Hardware metric keys ---------- */

// A hardware check's metrics are keyed "cpu", "memory", "swap", "load" for
// the readings a machine has one of, and "family:instance" for the rest:
// "disk:/srv", "inodes:/srv", "net:eth0.rx", "diskio:sda.busy". These mirror
// model.SplitMetricKey and model.SystemMetricUnit on the server.

const METRIC_FAMILY_LABELS = { cpu: 'Processor', memory: 'Memory', swap: 'Swap', load: 'Load per core', disk: 'Disk', inodes: 'Inodes', net: 'Network', diskio: 'Disk I/O' };

/** Splits a metric key into its family and instance ('' for a singleton). */
export function metricFamily(key) {
  const s = String(key || '');
  const i = s.indexOf(':');
  return i < 0 ? { family: s, instance: '' } : { family: s.slice(0, i), instance: s.slice(i + 1) };
}

/** The unit a hardware metric is measured in, from its key. */
export function metricUnit(key) {
  const { family, instance } = metricFamily(key);
  switch (family) {
    case 'cpu': case 'memory': case 'swap': case 'disk': case 'inodes': return '%';
    case 'load': return '';
    case 'net': return 'B/s';
    case 'diskio': return instance.endsWith('.busy') ? '%' : 'B/s';
  }
  return '';
}

/** A metric key as a person reads it: "Disk /srv", "Network eth0 received". */
export function metricLabel(key) {
  const { family, instance } = metricFamily(key);
  const base = METRIC_FAMILY_LABELS[family];
  if (!base) return key;
  if (!instance) return base;
  if (family === 'inodes') return `Inodes on ${instance}`;
  if (family === 'net' || family === 'diskio') {
    const dot = instance.lastIndexOf('.');
    if (dot > 0) {
      const dir = { rx: 'received', tx: 'sent', read: 'read', write: 'write', busy: 'busy' }[instance.slice(dot + 1)];
      if (dir) return `${family === 'net' ? 'Network' : 'Disk'} ${instance.slice(0, dot)} ${dir}`;
    }
  }
  return `${base} ${instance}`;
}

/** A metric reading with its unit, the way an alert would say it. */
export function metricValue(v, unit) {
  if (v == null || isNaN(v)) return '—';
  if (unit === '%') return pct(v, 0);
  if (unit === 'B/s') return `${bytes(v)}/s`;
  return Number(v).toFixed(2);
}

/**
 * Compares two version strings the way the release machinery does: numerically
 * part by part, with a suffix (-rc1) sorting before the release it precedes.
 * Returns -1, 0 or 1, and 0 whenever either side is not a version — an unknown
 * version must never read as "behind", or every agent that has not reported
 * one yet would be marked out of date.
 */
export function compareVersions(a, b) {
  const parse = (v) => {
    const s = String(v ?? '').trim().replace(/^v/, '');
    const m = s.match(/^(\d+(?:\.\d+)*)(.*)$/);
    if (!m) return null;
    return { nums: m[1].split('.').map(Number), suffix: m[2] };
  };
  const x = parse(a); const y = parse(b);
  if (!x || !y) return 0;
  const len = Math.max(x.nums.length, y.nums.length);
  for (let i = 0; i < len; i++) {
    const d = (x.nums[i] || 0) - (y.nums[i] || 0);
    if (d) return d > 0 ? 1 : -1;
  }
  // 1.2.0 is newer than 1.2.0-rc1; two suffixes compare as text.
  if (x.suffix === y.suffix) return 0;
  if (!x.suffix) return 1;
  if (!y.suffix) return -1;
  return x.suffix < y.suffix ? -1 : 1;
}

/**
 * Whether a machine's agent is behind the newest release. Anything unknown —
 * no reported version, no release to compare with — is not behind.
 */
export function agentIsBehind(running, latest) {
  if (!running || !latest) return false;
  return compareVersions(running, latest) < 0;
}

// Thin fetch wrapper for the localhost API plus the live-update stream.

export class ApiError extends Error {
  constructor(message, status, body) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.body = body;
  }
}

export const UNREACHABLE = 'The GWatch service is not responding — is it running?';

// Connection state shared with the shell (error banner).
const connListeners = new Set();
export const connection = { ok: true, lastError: null, lastChange: 0 };
export function onConnection(fn) { connListeners.add(fn); return () => connListeners.delete(fn); }
function setConnection(ok, err) {
  if (connection.ok === ok) return;
  connection.ok = ok;
  connection.lastError = err || null;
  connection.lastChange = Date.now();
  for (const fn of connListeners) { try { fn(ok); } catch (e) { console.error(e); } }
}

// ---- identity ----
// The shell listens here so that a 401 anywhere in the app can take the whole
// page to the sign-in screen, and a 403 can explain itself with the server's
// own wording rather than a generic "forbidden".
const authListeners = new Set();
export function onAuthChallenge(fn) { authListeners.add(fn); return () => authListeners.delete(fn); }
function challenge(reason) { for (const fn of authListeners) { try { fn(reason); } catch (e) { console.error(e); } } }

const denyListeners = new Set();
export function onDenied(fn) { denyListeners.add(fn); return () => denyListeners.delete(fn); }

// Paths that must never trigger the sign-in screen: they are how the screen
// itself works, or they are polled in the background by the shell.
const AUTH_PATHS = ['/api/auth/', '/api/me', '/api/health', '/api/version'];
const isAuthPath = (path) => AUTH_PATHS.some((p) => path.startsWith(p));

/** The principal behind this browser, as /api/me last reported it. */
export let me = { kind: '', isAdmin: false, canWrite: false, signedIn: false };

export async function refreshMe() {
  try { me = await request('GET', '/api/me'); } catch { /* keep the last answer */ }
  return me;
}

export async function request(method, path, body, opts = {}) {
  const init = { method, headers: {} };
  if (body instanceof FormData) init.body = body;
  else if (body !== undefined) { init.headers['Content-Type'] = 'application/json'; init.body = JSON.stringify(body); }
  let res;
  try {
    res = await fetch(path, init);
  } catch (e) {
    setConnection(false, e);
    throw new ApiError(UNREACHABLE, 0);
  }
  setConnection(true);
  if (opts.raw) return res;
  const ct = res.headers.get('content-type') || '';
  let data = null;
  if (res.status !== 204) {
    if (ct.includes('application/json')) {
      try { data = await res.json(); } catch { data = null; }
    } else {
      data = await res.text();
    }
  }
  if (!res.ok) {
    const msg = (data && typeof data === 'object' && data.error) ? data.error : (typeof data === 'string' && data.trim() ? data.trim().slice(0, 300) : `Request failed (${res.status})`);
    if (path.startsWith('/api/') && !isAuthPath(path)) {
      if (res.status === 401) challenge(msg);
      else if (res.status === 403) for (const fn of denyListeners) { try { fn(msg); } catch (e) { console.error(e); } }
    }
    throw new ApiError(msg, res.status, data);
  }
  return data;
}

export const api = {
  get: (path) => request('GET', path),
  post: (path, body) => request('POST', path, body ?? {}),
  put: (path, body) => request('PUT', path, body),
  patch: (path, body) => request('PATCH', path, body),
  del: (path) => request('DELETE', path),
  upload: (path, formData) => request('POST', path, formData),
  me: () => me,
  refreshMe,
};

/** What the sign-in screen needs to know before it draws itself. */
export const getAuthSetup = () => api.get('/api/auth/setup');
export const signIn = (username, password) => api.post('/api/auth/login', { username, password });
export const signOut = () => api.post('/api/auth/logout');

export function qs(params) {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(params || {})) {
    if (v == null || v === '') continue;
    if (Array.isArray(v)) v.forEach((x) => p.append(k, x));
    else p.append(k, v);
  }
  const s = p.toString();
  return s ? `?${s}` : '';
}

// Convenience helpers used by several views.
/** History for the checks the service considers most important (used when a chart widget has no explicit selection). */
export const getHistoryAuto = (range) => api.get(`/api/history/multi?auto=1&range=${encodeURIComponent(range || '24h')}`);

export const getHistoryMulti = (checkIds, range) => {
  const ids = (checkIds || []).filter((x) => x != null);
  if (!ids.length) return Promise.resolve([]);
  return api.get(`/api/history/multi${qs({ checkId: ids, range })}`);
};

/**
 * Subscribe to live updates. Handler receives the parsed update payload
 * (or null for polling ticks). Falls back to polling every 15s when the
 * EventSource fails. Returns an unsubscribe function.
 */
export function subscribeUpdates(handler, { pollMs = 15000 } = {}) {
  let es = null;
  let pollTimer = null;
  let stopped = false;
  let usingPoll = false;

  const startPoll = () => {
    if (pollTimer || stopped) return;
    usingPoll = true;
    pollTimer = setInterval(() => handler(null), pollMs);
  };
  const stopPoll = () => { if (pollTimer) { clearInterval(pollTimer); pollTimer = null; } usingPoll = false; };

  const connect = () => {
    if (stopped || typeof EventSource === 'undefined') { startPoll(); return; }
    try {
      es = new EventSource('/api/stream');
    } catch {
      startPoll();
      return;
    }
    es.addEventListener('open', () => { stopPoll(); setConnection(true); });
    es.addEventListener('update', (ev) => {
      let data = null;
      try { data = JSON.parse(ev.data); } catch { data = null; }
      handler(data || {});
    });
    es.addEventListener('error', () => {
      // EventSource retries by itself; keep polling meanwhile so the UI stays fresh.
      startPoll();
    });
  };
  connect();

  return () => {
    stopped = true;
    stopPoll();
    if (es) { try { es.close(); } catch { /* ignore */ } es = null; }
  };
}

export function debounce(fn, wait = 500) {
  let t = null;
  return (...args) => {
    clearTimeout(t);
    t = setTimeout(() => { t = null; fn(...args); }, wait);
  };
}

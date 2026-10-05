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
// The shell listens here so that a 401 anywhere in the app (once /api/me has
// confirmed it, see confirmSignedOut) can take the whole page to the sign-in
// screen, and a 403 can explain itself with the server's own wording rather
// than a generic "forbidden".
const authListeners = new Set();
export function onAuthChallenge(fn) { authListeners.add(fn); return () => authListeners.delete(fn); }
function challenge(reason) { for (const fn of authListeners) { try { fn(reason); } catch (e) { console.error(e); } } }

const denyListeners = new Set();
export function onDenied(fn) { denyListeners.add(fn); return () => denyListeners.delete(fn); }

// Paths that must never trigger the sign-in screen: they are how the screen
// itself works, or they are polled in the background by the shell.
const AUTH_PATHS = ['/api/auth/', '/api/me', '/api/health', '/api/version'];
const isAuthPath = (path) => AUTH_PATHS.some((p) => path.startsWith(p));

/** The principal behind this browser, as /api/me last reported it. Until the
 *  first answer arrives this is a placeholder with no standing at all, and
 *  identityKnown() says so — which is not the same as "a viewer". */
export let me = { kind: '', isAdmin: false, canWrite: false, signedIn: false };
let meKnown = false;
export const identityKnown = () => meKnown;

// The fields that decide what this browser may do. A change to any of them
// is a change of identity; the theme and indicator rules that travel with
// them in /api/me are not.
const IDENTITY_FIELDS = ['kind', 'name', 'role', 'userId', 'isAdmin', 'canWrite', 'signedIn'];
const sameIdentity = (a, b) => IDENTITY_FIELDS.every((k) => (a?.[k] ?? null) === (b?.[k] ?? null));

// The shell listens here to restyle itself (and rebuild the view on screen,
// which chose what to offer from the identity it was built with) whenever the
// service reports a different standing from the one last known.
const identityListeners = new Set();
export function onIdentity(fn) { identityListeners.add(fn); return () => identityListeners.delete(fn); }

function setMe(next) {
  const prev = me;
  me = next;
  meKnown = true;
  if (sameIdentity(prev, next)) return;
  for (const fn of identityListeners) { try { fn(next, prev); } catch (e) { console.error(e); } }
}

// One /api/me request at a time: a burst of refusals, a reconnect and the
// shell's periodic check landing together all share the same answer.
let meInFlight = null;
function fetchMe() {
  if (!meInFlight) {
    meInFlight = request('GET', '/api/me').then((who) => {
      if (!who || typeof who !== 'object') throw new ApiError('The service sent an unreadable identity.', 0);
      return who;
    }).finally(() => { meInFlight = null; });
  }
  return meInFlight;
}

const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

/**
 * Ask the service who this browser is, retrying `retries` more times (with
 * a doubling delay) if it cannot answer. A failed answer never changes the
 * identity: an administrator whose /api/me happened to fail — the service
 * busy, the network blinking — used to be styled as a viewer until they
 * reloaded, because the placeholder above was taken as the answer. Now the
 * last real answer stands, and identityKnown() stays false until there is one.
 */
export async function refreshMe({ retries = 0, retryDelay = 500 } = {}) {
  for (let attempt = 0; ; attempt++) {
    try {
      setMe(await fetchMe());
      return me;
    } catch {
      if (attempt >= retries) return me; // keep the last answer
      await pause(retryDelay * 2 ** attempt);
    }
  }
}

/**
 * A request was refused with 401. That alone is not proof the browser is
 * signed out — a request that raced a sign-in, or one the service could not
 * judge, can come back 401 while the session is fine — and acting on it takes
 * the whole page to the sign-in screen. So ask /api/me, and only when it too
 * says there is no identity is the sign-in screen called for. If it cannot be
 * asked, nothing is decided on a guess: the next refusal asks again.
 */
async function confirmSignedOut(reason) {
  let who;
  try { who = await fetchMe(); } catch { return; }
  setMe(who);
  if (!who.kind) challenge(reason);
}

// Coming back from an outage is when the identity is most likely to have
// changed behind the page's back (a restart, a session ended meanwhile), and
// when a first answer that failed at start-up can finally be had.
onConnection((ok) => { if (ok) refreshMe({ retries: 2 }); });

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
      if (res.status === 401) confirmSignedOut(msg);
      else if (res.status === 403) {
        // Refused for lack of standing: perhaps the page is working from an
        // identity that is out of date (the account's role was changed), so
        // ask again — the shell restyles itself if the answer differs.
        refreshMe();
        for (const fn of denyListeners) { try { fn(msg); } catch (e) { console.error(e); } }
      }
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

/**
 * One of a check's own measurements rather than its latency — an SNMP check's
 * OIDs. The series' avgMs carries the metric as well as its `value`, so the
 * existing chart helpers plot it unchanged.
 */
export const getHistoryMetric = (checkId, range, metric, end) =>
  api.get(`/api/history${qs({ checkId, range: range || '24h', metric, end: end != null ? Math.round(end) : undefined })}`);

/** `end` (milliseconds) moves the window back from now — a timestacked chart
 *  reads yesterday's 24 hours, the day before's … this way. */
export const getHistoryMulti = (checkIds, range, end) => {
  const ids = (checkIds || []).filter((x) => x != null);
  if (!ids.length) return Promise.resolve([]);
  return api.get(`/api/history/multi${qs({ checkId: ids, range, end: end != null ? Math.round(end) : undefined })}`);
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
    if (pollTimer || stopped || document.hidden) return;
    usingPoll = true;
    pollTimer = setInterval(() => handler(null), pollMs);
  };
  const stopPoll = () => { if (pollTimer) { clearInterval(pollTimer); pollTimer = null; } usingPoll = false; };

  const connect = () => {
    if (document.hidden || stopped) return;
    if (typeof EventSource === 'undefined') { startPoll(); return; }
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
  const visibility = () => {
    stopPoll();
    if (es) { es.close(); es = null; }
    if (!document.hidden && !stopped) { connect(); handler(null); }
  };
  document.addEventListener('visibilitychange', visibility);
  connect();

  return () => {
    document.removeEventListener('visibilitychange', visibility);
    stopped = true;
    stopPoll();
    if (es) { try { es.close(); } catch { /* ignore */ } es = null; }
  };
}

export function debounce(fn, wait = 500, maxWait = Math.max(2000, wait * 4)) {
  let t = null, max = null, latest;
  const run = () => { clearTimeout(t); clearTimeout(max); t = max = null; fn(...latest); };
  return (...args) => {
    latest = args;
    clearTimeout(t);
    if (!max) max = setTimeout(run, maxWait);
    t = setTimeout(run, wait);
  };
}

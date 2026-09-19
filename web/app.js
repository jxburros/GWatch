// GWatch web UI entry: hash router, shell (rail / topbar / status dots),
// theme + accent handling and live updates.

import { api, onConnection, connection, subscribeUpdates, debounce, refreshMe, signOut, getAuthSetup, onAuthChallenge, onDenied } from './api.js';
import { h, icon, clear, toast, closeMenus, replace, applyTheme, applyAccent, onThemeChange } from './components.js';
import { relTime } from './fmt.js';
import { notifyRoute as tipsRoute, closeTip, onboardingDone } from './tips.js';

const routes = [
  { pattern: /^\/dashboard(?:\/(\d+))?$/, view: () => import('./views/dashboard.js'), params: ['id'], nav: 'dashboard' },
  { pattern: /^\/nodes$/, view: () => import('./views/nodes.js'), nav: 'nodes' },
  { pattern: /^\/nodes\/new$/, view: () => import('./views/node-editor.js'), nav: 'nodes' },
  { pattern: /^\/nodes\/(\d+)\/edit$/, view: () => import('./views/node-editor.js'), params: ['id'], nav: 'nodes' },
  { pattern: /^\/nodes\/(\d+)$/, view: () => import('./views/node-detail.js'), params: ['id'], nav: 'nodes' },
  { pattern: /^\/charts(?:\/([\w-]+))?$/, view: () => import('./views/charts.js'), params: ['id'], nav: 'charts' },
  { pattern: /^\/incidents$/, view: () => import('./views/incidents.js'), nav: 'incidents' },
  { pattern: /^\/audit(?:\/([a-z]+))?$/, view: () => import('./views/audit.js'), params: ['tab'], nav: 'audit' },
  { pattern: /^\/settings(?:\/([a-z]+))?$/, view: () => import('./views/settings.js'), params: ['tab'], nav: 'settings' },
  { pattern: /^\/help$/, view: () => import('./views/help.js'), nav: 'help' },
  { pattern: /^\/onboarding$/, view: () => import('./views/onboarding.js'), nav: null },
  { pattern: /^\/wallboards(?:\/(\d+))?$/, view: () => import('./views/wallboards.js'), params: ['id'], nav: 'wallboard' },
  { pattern: /^\/wallboard(?:\/(\d+))?$/, view: () => import('./views/wallboard.js'), params: ['id'], nav: 'wallboard', wallboard: true },
  { pattern: /^\/login$/, view: () => import('./views/login.js'), nav: null, bare: true },
];

const viewRoot = document.getElementById('view');
const titleEl = document.getElementById('page-title');
const actionsEl = document.getElementById('page-actions');
const pageBar = document.getElementById('page-bar');
const banner = document.getElementById('api-banner');
const healthLink = document.getElementById('service-health');
const dotsEl = document.getElementById('status-dots');
const rail = document.getElementById('rail');
const railPin = document.getElementById('rail-pin');

let current = null; // { instance, route, path }
let navToken = 0;

/* Rail icons */
document.querySelectorAll('[data-icon]').forEach((el) => { el.append(icon(el.dataset.icon)); });

/* ---------- Rail pinning ---------- */
function setRailPinned(pinned, persist = true) {
  document.documentElement.classList.toggle('rail-pinned', pinned);
  document.body.classList.toggle('rail-pinned', pinned);
  railPin.setAttribute('aria-pressed', pinned ? 'true' : 'false');
  railPin.title = pinned ? 'Collapse the sidebar to icons' : 'Keep the sidebar open';
  railPin.querySelector('.label').textContent = pinned ? 'Unpin sidebar' : 'Pin sidebar';
  if (persist) { try { localStorage.setItem('gw.railPinned', pinned ? '1' : '0'); } catch { /* ignore */ } }
}
setRailPinned(document.documentElement.classList.contains('rail-pinned'), false);
railPin.addEventListener('click', () => setRailPinned(!document.documentElement.classList.contains('rail-pinned')));

// A mouse click leaves focus on the link or button inside the rail, and a
// focused child keeps the rail expanded even after the pointer has left. Drop
// that focus so the rail collapses as soon as the mouse moves away. Keyboard
// focus is kept: the CSS expands on :focus-visible, which a click does not set.
function dropRailFocus() {
  const active = document.activeElement;
  if (active && active !== document.body && rail.contains(active)) active.blur();
}
rail.addEventListener('pointerup', (e) => {
  if (e.pointerType === 'mouse' && e.target.closest('a, button')) requestAnimationFrame(dropRailFocus);
});
// A click that navigates elsewhere (or a menu opening over the rail) should not
// leave the rail hanging open either.
rail.addEventListener('mouseleave', dropRailFocus);

/* ---------- Theme / accent from settings ---------- */
/** The theme lives in settings, which only an administrator may read, so it is
 *  served alongside the identity in /api/me and every account gets styled. */
async function loadAppearance() {
  try {
    const me = await refreshMe();
    applyTheme(me.theme || 'dark');
    applyAccent(me.accentColor || '#43c9c0');
    applyIdentity(me);
  } catch { /* keep the cached theme */ }
}

/* ---------- Signed-in identity ---------- */
let identity = { kind: '', isAdmin: false, signedIn: false };
const accountEl = document.getElementById('account');

/** Reflect the principal on <body> so CSS can hide what a viewer cannot use,
 *  and fill in the account block at the foot of the rail. */
function applyIdentity(me) {
  identity = me || identity;
  document.body.classList.toggle('viewer', !identity.isAdmin);
  document.body.classList.toggle('signed-in', !!identity.signedIn);
  if (!accountEl) return;
  accountEl.hidden = !identity.signedIn;
  const out = document.getElementById('sign-out');
  if (out) out.hidden = !identity.signedIn;
  if (!identity.signedIn) return;
  accountEl.querySelector('.account-name').textContent = identity.name || 'Signed in';
  accountEl.querySelector('.account-role').textContent = identity.role === 'admin' ? 'Administrator' : 'Viewer';
}

document.getElementById('sign-out')?.addEventListener('click', async () => {
  try { await signOut(); } catch { /* sign out locally anyway */ }
  location.hash = '#/login';
  location.reload();
});
onThemeChange(() => { if (current?.instance?.themeChanged) { try { current.instance.themeChanged(); } catch (e) { console.error(e); } } });

export function parseHash() {
  let hash = location.hash || '#/dashboard';
  if (hash.startsWith('#')) hash = hash.slice(1);
  if (!hash.startsWith('/')) hash = '/' + hash;
  const [path, query = ''] = hash.split('?');
  return { path: path.replace(/\/+$/, '') || '/dashboard', query: new URLSearchParams(query) };
}

export function navigate(path) { location.hash = path.startsWith('#') ? path : `#${path}`; }

const ctxBase = {
  navigate,
  get me() { return identity; },
  /** The header carries the page name and nothing else, so anything a view
   *  offers goes to the page bar underneath it. A `subtitle` is accepted and
   *  dropped: pages no longer explain themselves in the chrome. */
  setTitle(title, { actions } = {}) {
    if (titleEl.textContent !== title) {
      titleEl.classList.remove('title-enter');
      void titleEl.offsetWidth;
      titleEl.classList.add('title-enter');
    }
    titleEl.textContent = title;
    document.title = title === 'Dashboard' ? 'GWatch' : `${title} — GWatch`;
    clear(actionsEl);
    if (actions) replace(actionsEl, actions);
    syncPageBar();
  },
};

async function route() {
  const token = ++navToken;
  closeMenus();
  // A tip points at something in the view that is on its way out, so it goes
  // with it rather than hanging over whatever arrives next.
  closeTip({ seen: false });
  const { path, query } = parseHash();
  let match = null; let r = null;
  for (const candidate of routes) {
    const m = candidate.pattern.exec(path);
    if (m) { match = m; r = candidate; break; }
  }
  if (!r) {
    if (await redirectHardware(path)) return;
    navigate('/dashboard');
    return;
  }

  // A client that has to sign in sees nothing but the sign-in screen.
  if (!r.bare && await loginRequired()) { navigate('/login'); return; }
  document.body.classList.toggle('bare-mode', !!r.bare);
  const params = {};
  (r.params || []).forEach((name, i) => { params[name] = match[i + 1]; });

  // Same view, only params changed? Let the view handle it if it can.
  if (current && current.route === r && current.instance?.update) {
    const handled = await current.instance.update(params, query);
    if (handled) { current.path = path; setNav(r.nav); tipsRoute(path); return; }
  }

  if (current?.instance?.destroy) { try { current.instance.destroy(); } catch (e) { console.error(e); } }
  current = null;
  document.body.classList.toggle('wallboard-mode', !!r.wallboard);
  setNav(r.nav);
  clear(actionsEl);
  syncPageBar();
  clear(viewRoot);
  viewRoot.append(h('div', { class: 'skeleton', style: { maxWidth: '600px' } }, h('div', { class: 'skeleton-line', style: { width: '40%' } }), h('div', { class: 'skeleton-line' }), h('div', { class: 'skeleton-line', style: { width: '70%' } })));

  let mod;
  try { mod = await r.view(); } catch (e) {
    console.error(e);
    replace(viewRoot, h('div', { class: 'card' }, h('h2', null, 'Could not load this page'), h('p', { class: 'muted' }, String(e.message || e))));
    return;
  }
  if (token !== navToken) return;
  clear(viewRoot);
  enterView();
  const ctx = { ...ctxBase, params, query, root: viewRoot };
  try {
    const instance = await mod.mount(viewRoot, ctx);
    if (token !== navToken) { instance?.destroy?.(); return; }
    current = { instance: instance || {}, route: r, path };
    tipsRoute(path);
  } catch (e) {
    console.error(e);
    replace(viewRoot, h('div', { class: 'card' }, h('h2', null, 'Something went wrong'), h('p', { class: 'muted' }, String(e.message || e))));
  }
  window.scrollTo(0, 0);
}

/** Replay the view's enter animation. Forcing a reflow between removing and
 *  re-adding the class restarts it. The class is dropped again shortly after so
 *  that content re-rendered by a live update does not animate in a second time;
 *  by then the animation has finished and the elements are at their end state. */
let enterTimer = 0;
function enterView() {
  viewRoot.classList.remove('view-enter');
  void viewRoot.offsetWidth;
  viewRoot.classList.add('view-enter');
  clearTimeout(enterTimer);
  enterTimer = setTimeout(() => viewRoot.classList.remove('view-enter'), 900);
}

function setNav(name) {
  document.querySelectorAll('.nav-link[data-nav]').forEach((a) => {
    const active = a.dataset.nav === name;
    a.classList.toggle('active', active);
    if (active) a.setAttribute('aria-current', 'page'); else a.removeAttribute('aria-current');
  });
}

/** The page bar is only there when it has something in it. */
function syncPageBar() {
  if (!pageBar) return;
  pageBar.hidden = !dotsEl.childElementCount && !actionsEl.childElementCount;
}

/* ---------- Connection banner ---------- */
function updateBanner() {
  banner.hidden = connection.ok;
}
onConnection(updateBanner);
document.getElementById('api-banner-retry').addEventListener('click', () => { refreshCurrent(); refreshHealth(); refreshStatus(); });

/* ---------- Service health dot ---------- */
let lastHealth = null;
async function refreshHealth() {
  try {
    lastHealth = await api.get('/api/health');
    renderHealth();
  } catch {
    lastHealth = null;
    healthLink.className = 'nav-link service-health issue';
    healthLink.querySelector('.health-text').textContent = 'Service unreachable';
  }
}
function renderHealth() {
  if (!lastHealth) return;
  const hl = lastHealth;
  const issues = [];
  if (!hl.serviceRunning) issues.push('service stopped');
  if (!hl.schedulerRunning) issues.push('scheduler stopped');
  if (hl.lastCheckAt && Date.now() - new Date(hl.lastCheckAt).getTime() > 10 * 60e3 && hl.checksEnabled > 0) issues.push('no checks recently');
  if (hl.retention?.lastError) issues.push('retention error');
  if (hl.backup?.lastBackupAt && !hl.backup.lastBackupOk) issues.push('backup failed');
  if (hl.recentErrors?.length) issues.push('recent errors');
  healthLink.className = `nav-link service-health ${issues.length ? 'issue' : 'ok'}`;
  healthLink.querySelector('.health-text').textContent = issues.length ? 'Service issue' : 'Service healthy';
  healthLink.title = issues.length ? issues.join(', ') : `Service healthy · last check ${relTime(hl.lastCheckAt)}`;
}
export function getHealth() { return lastHealth; }

/* ---------- Header status circles ---------- */
async function refreshStatus() {
  let st;
  try { st = await api.get('/api/status'); } catch { clear(dotsEl); syncPageBar(); return; }
  clear(dotsEl);
  const dot = (cls, n, label, href, title) => h('a', { class: `sdot ${cls}`, href, title }, h('i'), h('span', null, n != null ? `${n} ${label}` : label));
  const items = [];
  if (st.down > 0) items.push(dot('s-down', st.down, 'down', '#/nodes?status=down', 'Nodes that are down'));
  if (st.degraded > 0) items.push(dot('s-degraded', st.degraded, 'degraded', '#/nodes?status=degraded', 'Nodes that are degraded'));
  if (st.certWarnings > 0) items.push(dot('s-cert', st.certWarnings, st.certWarnings === 1 ? 'cert' : 'certs', '#/incidents?type=cert_warning', 'Certificates expiring soon or invalid'));
  if (!st.serviceOk) items.push(dot('s-service', null, 'service', '#/settings/health', (st.serviceIssues || []).join(', ') || 'Service issue'));
  if (st.unknown > 0 && !items.length) items.push(dot('s-unknown', st.unknown, 'waiting', '#/nodes?status=unknown', 'Waiting for first results'));
  if (!items.length) items.push(dot('s-ok', null, st.total ? 'all clear' : 'no nodes', '#/dashboard', st.total ? `${st.up} of ${st.total} nodes healthy` : 'Add a node to start monitoring'));
  dotsEl.append(...items);
  syncPageBar();
}

/* ---------- Live updates ---------- */
const refreshCurrent = debounce(() => {
  if (current?.instance?.refresh) {
    Promise.resolve(current.instance.refresh()).catch((e) => console.warn('refresh failed', e));
  }
}, 500);
const refreshHealthDebounced = debounce(refreshHealth, 2000);
const refreshStatusDebounced = debounce(refreshStatus, 800);

subscribeUpdates((update) => {
  refreshCurrent();
  refreshStatusDebounced();
  if (!update || update.kind === 'state' || update.kind === 'config' || update.kind === 'event' || update.kind === 'health') refreshHealthDebounced();
  if (update && update.kind === 'config') loadAppearance();
});
setInterval(refreshHealth, 60000);
setInterval(refreshStatus, 30000);
refreshHealth();
refreshStatus();

/* ---------- Relative-time ticking ---------- */
setInterval(() => {
  document.querySelectorAll('[data-rel]').forEach((el) => { if (el.dataset.rel) el.textContent = relTime(el.dataset.rel); });
}, 10000);

/* ---------- Sign-in gate ---------- */
// Cached so that every navigation does not re-ask; a 401 from anywhere clears
// it, which is what takes an expired session back to the sign-in screen.
let authSetup = null;
async function loginRequired() {
  if (authSetup === null) {
    try { authSetup = await getAuthSetup(); } catch { return false; } // the banner covers an unreachable service
  }
  return !!authSetup.loginRequired;
}
onAuthChallenge(() => {
  authSetup = null;
  if (parseHash().path !== '/login') navigate('/login');
});
onDenied((message) => toast(message, { kind: 'error', timeout: 8000 }));

/* ---------- Hardware, which is now part of Nodes ---------- */
// Machines used to have a section of their own. They are nodes now, so a link
// to #/hardware/<key> is followed to the node that machine belongs to. A host
// key contains a colon ("agent:3"), which is why it is matched loosely.
async function redirectHardware(path) {
  const m = /^\/hardware(?:\/(.+))?$/.exec(path);
  if (!m) return false;
  const key = m[1] && decodeURIComponent(m[1]);
  if (key) {
    try {
      const host = await api.get(`/api/hosts/${encodeURIComponent(key)}`);
      if (host?.nodeId) { navigate(`/nodes/${host.nodeId}`); return true; }
    } catch { /* fall through to the list */ }
  }
  navigate('/nodes');
  return true;
}

window.addEventListener('hashchange', route);
window.addEventListener('error', (e) => { if (e.message) console.error('Unhandled:', e.message); });
window.addEventListener('unhandledrejection', (e) => {
  const msg = e.reason?.message || String(e.reason);
  if (e.reason?.name === 'ApiError' && e.reason.status === 0) return; // banner handles it
  console.error('Unhandled promise rejection:', msg);
  toast(msg, { kind: 'error' });
});

/* ---------- First run ---------- */
// Nobody has been through the tour yet, so start there rather than on an empty
// dashboard. Only the default landing page is taken over, so a link to a
// particular node, chart or the wallboard still arrives where it was aimed,
// and skipping or finishing the tour sets the flag that stops this happening
// a second time.
// Returns true when it took over, in which case the hashchange it just caused
// does the routing — calling route() as well would mount the tour twice.
function firstRunRedirect() {
  if (onboardingDone()) return false;
  if (parseHash().path !== '/dashboard') return false;
  navigate('/onboarding');
  return true;
}

// Who is looking has to be known before the first view is built: views read
// ctx.me to decide what they may offer, and the theme travels with it.
loadAppearance().finally(() => { if (!firstRunRedirect()) route(); });

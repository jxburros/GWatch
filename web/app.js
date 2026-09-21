// GWatch web UI entry: hash router, shell (rail / topbar / header
// indicators), theme + accent handling and live updates.

import { api, onConnection, connection, subscribeUpdates, debounce, refreshMe, signOut, getAuthSetup, onAuthChallenge, onDenied } from './api.js';
import { h, icon, clear, toast, closeMenus, replace, applyTheme, applyAccent, onThemeChange, openModal } from './components.js';
import { relTime } from './fmt.js';
import { notifyRoute as tipsRoute, closeTip, onboardingDone } from './tips.js';

const routes = [
  { pattern: /^\/dashboard(?:\/(\d+))?$/, view: () => import('./views/dashboard.js'), params: ['id'], nav: 'dashboard' },
  { pattern: /^\/nodes$/, view: () => import('./views/nodes.js'), nav: 'nodes' },
  { pattern: /^\/nodes\/bulk$/, view: () => import('./views/bulk-edit.js'), nav: 'nodes' },
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
const dotsEl = document.getElementById('indicators');
const rail = document.getElementById('rail');
const railPin = document.getElementById('rail-pin');

let current = null; // { instance, route, path }
let navToken = 0;

/* Rail icons */
document.querySelectorAll('[data-icon]').forEach((el) => { el.append(icon(el.dataset.icon)); });

/* Skip link */
// Its href is #view, and to the hash router that reads as a page called
// "view", which would send a keyboard user back to the dashboard instead of
// past the sidebar. So it moves focus itself and leaves the hash alone.
document.querySelector('.skip-link')?.addEventListener('click', (e) => {
  e.preventDefault();
  viewRoot.focus({ preventScroll: true });
  viewRoot.scrollIntoView({ block: 'start' });
});

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
 *  served alongside the identity in /api/me and every account gets styled. The
 *  header's indicator rules travel the same road, for the same reason. */
async function loadAppearance() {
  try {
    const me = await refreshMe();
    applyTheme(me.theme || 'dark');
    applyAccent(me.accentColor || '#43c9c0');
    applyIdentity(me);
    setIndicatorRules(me.indicators);
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
  enterTimer = setTimeout(endEnterAnimation, 900);
}

/** Entrance animations belong to navigation and to nothing else. The timer
 *  above is not enough on its own: an update arriving inside that window would
 *  hand freshly built elements to `.view-enter`, and they would fade in from
 *  nothing under content that was already on screen. So a refresh ends the
 *  animation first — anything it renders is then simply there. */
function endEnterAnimation() {
  clearTimeout(enterTimer);
  enterTimer = 0;
  viewRoot.classList.remove('view-enter');
}

function setNav(name) {
  document.querySelectorAll('.nav-link[data-nav]').forEach((a) => {
    const active = a.dataset.nav === name;
    a.classList.toggle('active', active);
    if (active) a.setAttribute('aria-current', 'page'); else a.removeAttribute('aria-current');
  });
}

/** The page bar is only there when this page has controls to put in it. The
 *  status indicators moved up into the header, so on a page that offers
 *  nothing the bar would otherwise be an empty stripe. */
function syncPageBar() {
  if (!pageBar) return;
  pageBar.hidden = !actionsEl.childElementCount;
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

/* ---------- Header indicators ---------- */
/** The orbs under the page name. Which ones appear is decided by rules the
 *  administrator configures in Settings › Indicators; because a viewer may not
 *  read settings, the effective list arrives with the identity from /api/me,
 *  exactly as the theme does, so every account evaluates the same rules.
 *
 *  Evaluation is done here rather than on the server so that the whole thing
 *  costs no more than the one /api/status poll the header already made. */
let indicatorRules = [];

// Reddest first: an orb that is only telling you something is unknown should
// never sit in front of one telling you something is down.
const INDICATOR_RANK = { red: 0, orange: 1, yellow: 2 };

/** How many things the rule's condition currently counts. Every kind here is
 *  answerable from the one summary document, which is the whole reason the
 *  vocabulary is this short. */
function indicatorCount(st, cond) {
  switch (cond.kind) {
    case 'nodesInStatus': return Number({ down: st.down, degraded: st.degraded, unknown: st.unknown, maintenance: st.maintenance }[cond.status]) || 0;
    case 'certWarnings': return Number(st.certWarnings) || 0;
    case 'attention': return Number(st.attention) || 0;
    // Unwell or not; the number of complaints is for the tooltip, not the rule.
    case 'serviceHealth': return st.serviceOk ? 0 : Math.max(1, (st.serviceIssues || []).length);
    default: return 0;
  }
}

/** Whether there is anything for the header to report on at all: a node that
 *  exists, is being watched, and has produced at least one result. A fresh
 *  install, one whose nodes are all paused, and one where the first round of
 *  checks has not finished yet all answer no — and all three are honestly
 *  described by the blue orb rather than by a green "all clear" nobody has
 *  earned or a yellow warning about something that is merely young. */
function anythingConnected(st) {
  return (Number(st.total) || 0) > 0 && ((Number(st.up) || 0) + (Number(st.degraded) || 0) + (Number(st.down) || 0)) > 0;
}

/** Where an orb leads when it is clicked: to the thing it is complaining
 *  about, filtered down to it where the page can be. */
function indicatorHref(cond) {
  switch (cond.kind) {
    case 'nodesInStatus': return `#/nodes?status=${encodeURIComponent(cond.status || 'down')}`;
    case 'certWarnings': return '#/incidents?type=cert_warning';
    case 'attention': return '#/incidents';
    case 'serviceHealth': return '#/settings/health';
    default: return '#/dashboard';
  }
}

function indicatorOrb({ colour, label, href, count }) {
  return h('a', { class: `indicator ind-${colour}`, href, title: label, 'aria-label': label },
    h('span', { class: 'orb', 'aria-hidden': 'true' }),
    count > 1 ? h('span', { class: 'indicator-count', 'aria-hidden': 'true' }, String(count)) : null);
}

function renderIndicators(st) {
  clear(dotsEl);
  const connected = anythingConnected(st);
  const firing = [];
  for (const rule of indicatorRules) {
    if (!rule || !rule.enabled) continue;
    const cond = rule.condition || {};
    // With nothing connected there is no node state worth reporting: the blue
    // orb already says so, and a yellow "waiting for first results" beside it
    // would only say it again in a more alarming colour. The monitor's own
    // health is a different matter and is still worth hearing about.
    if (!connected && cond.kind !== 'serviceHealth') continue;
    const n = indicatorCount(st, cond);
    if (n < Math.max(1, Number(cond.minCount) || 1)) continue;
    firing.push({ rule, count: n });
  }
  if (firing.length) {
    firing.sort((a, b) => (INDICATOR_RANK[a.rule.colour] ?? 9) - (INDICATOR_RANK[b.rule.colour] ?? 9));
    for (const f of firing) {
      const detail = f.rule.condition?.kind === 'serviceHealth' ? (st.serviceIssues || []).join(', ') : `${f.count}`;
      dotsEl.append(indicatorOrb({
        colour: INDICATOR_RANK[f.rule.colour] != null ? f.rule.colour : 'yellow',
        label: detail ? `${f.rule.name} — ${detail}` : f.rule.name,
        href: indicatorHref(f.rule.condition || {}),
        count: f.count,
      }));
    }
  } else if (!connected) {
    dotsEl.append(indicatorOrb({
      colour: 'idle',
      label: st.total ? 'Nothing connected yet — waiting for the first results' : 'No nodes yet — add one to start monitoring',
      href: st.total ? '#/nodes' : '#/nodes/new',
    }));
  } else {
    dotsEl.append(indicatorOrb({ colour: 'ok', label: `All clear — ${st.up} of ${st.total} nodes healthy`, href: '#/dashboard' }));
  }
}

let lastStatus = null;
async function refreshStatus() {
  try { lastStatus = await api.get('/api/status'); } catch { clear(dotsEl); return; }
  renderIndicators(lastStatus);
}

/** Called when /api/me lands, and again whenever the configuration changes,
 *  so that saving a rule relights the header without a reload. */
function setIndicatorRules(rules) {
  indicatorRules = Array.isArray(rules) ? rules : [];
  if (lastStatus) renderIndicators(lastStatus);
}

/* ---------- Application updates ---------- */
/** The badge in the header is the only place an update announces itself
 *  outside Settings. It appears for an administrator — nobody else can install
 *  one — and leads to Settings › Updates, where the version is chosen. */
const updateBadge = document.getElementById('update-badge');
const updateBadgeLabel = document.getElementById('update-badge-label');
let updateStatus = null;
let promptedThisLoad = false;

/** A check on open would hit GitHub on every reload, so a check made in the
 *  last quarter of an hour is taken as current and the cached one is used. */
function checkedRecently(st) {
  if (!st?.lastCheckAt) return false;
  return Date.now() - new Date(st.lastCheckAt).getTime() < 15 * 60e3;
}

function renderUpdateBadge() {
  if (!updateBadge) return;
  const last = updateStatus?.last;
  const available = !!(last?.updateAvailable && !last.error && identity.isAdmin);
  updateBadge.hidden = !available;
  if (!available) return;
  updateBadgeLabel.textContent = last.latestVersion || 'Update';
  updateBadge.title = `GWatch ${last.latestVersion} is available${last.prerelease ? ' (pre-release)' : ''} — you are running ${last.currentVersion}`;
}

/** Offer the update when GWatch is opened. "Not now" asks again next time, as
 *  intended; "Skip this version" stops it for that version only, so the next
 *  release asks again. The choice is per browser, which is where the dialog
 *  is: it is a prompt, not a policy. */
function skippedVersion() {
  try { return localStorage.getItem('gw.updateSkip') || ''; } catch { return ''; }
}
function skipVersion(v) {
  try { localStorage.setItem('gw.updateSkip', v); } catch { /* private window: it asks again */ }
}

function promptForUpdate() {
  const last = updateStatus?.last;
  if (promptedThisLoad || !identity.isAdmin) return;
  if (!updateStatus?.promptOnOpen || !last?.updateAvailable || last.error) return;
  if (!updateStatus.canApply || !last.assetUrl) return; // nothing this dialog could do
  if (skippedVersion() === last.latestVersion) return;
  promptedThisLoad = true;
  const m = openModal({
    title: `GWatch ${last.latestVersion} is available`,
    body: [
      h('p', null, `You are running ${last.currentVersion}. ${last.prerelease ? 'This is a pre-release, published for testing and not finished work. ' : ''}Installing downloads the release, checks its signature, replaces this copy and restarts the service — monitoring pauses for a few seconds.`),
      last.releaseNotes ? h('details', { class: 'collapsible' }, h('summary', null, 'Release notes'), h('div', { class: 'update-notes' }, last.releaseNotes)) : null,
    ],
    footer: [
      h('button', { class: 'btn', type: 'button', onclick: () => { skipVersion(last.latestVersion); m.close(); } }, 'Skip this version'),
      h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, 'Not now'),
      h('button', { class: 'btn btn-primary', type: 'button', onclick: () => { m.close(); navigate('/settings/updates'); } }, 'Go to updates'),
    ],
  });
}

/** Refresh what the badge shows. With `open`, this is the check made because
 *  GWatch was just opened: it asks the service to contact GitHub (unless that
 *  was done moments ago, or automatic checks are off) and then offers the
 *  update. */
async function refreshUpdates({ open = false } = {}) {
  if (!identity.isAdmin) { if (updateBadge) updateBadge.hidden = true; return; }
  try {
    let doc = await api.get('/api/update/status');
    let st = doc?.status;
    if (open && st?.autoCheck && !checkedRecently(st)) {
      try {
        await api.post('/api/update/check');
        st = (await api.get('/api/update/status'))?.status;
      } catch { /* offline, rate-limited: the cached answer still shows */ }
    }
    updateStatus = st || null;
    renderUpdateBadge();
    if (open) promptForUpdate();
  } catch { /* the connection banner covers this */ }
}

/* ---------- Live updates ---------- */
const refreshCurrent = debounce(() => {
  if (current?.instance?.refresh) {
    endEnterAnimation();
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
// Saving the Indicators tab relights the header there and then, rather than
// leaving the author of the rule to wonder until the next update arrives.
window.addEventListener('gw:indicators-changed', () => { loadAppearance().then(refreshStatus); });
setInterval(refreshHealth, 60000);
setInterval(refreshStatus, 30000);
// Half-hourly, so a check the service made in the background reaches the
// header without a reload. The check itself is the service's business.
setInterval(() => refreshUpdates(), 30 * 60e3);
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
loadAppearance().finally(() => {
  if (!firstRunRedirect()) route();
  // Opening GWatch is one of the moments it looks for a new version.
  refreshUpdates({ open: true });
});

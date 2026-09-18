// GWatch web UI entry: hash router, shell (sidebar/topbar), live updates.

import { api, onConnection, connection, subscribeUpdates, debounce } from './api.js';
import { h, icon, clear, toast, closeMenus, replace } from './components.js';
import { relTime } from './fmt.js';

const routes = [
  { pattern: /^\/dashboard(?:\/(\d+))?$/, view: () => import('./views/dashboard.js'), params: ['id'], nav: 'dashboard' },
  { pattern: /^\/nodes$/, view: () => import('./views/nodes.js'), nav: 'nodes' },
  { pattern: /^\/nodes\/new$/, view: () => import('./views/node-editor.js'), nav: 'nodes' },
  { pattern: /^\/nodes\/(\d+)\/edit$/, view: () => import('./views/node-editor.js'), params: ['id'], nav: 'nodes' },
  { pattern: /^\/nodes\/(\d+)$/, view: () => import('./views/node-detail.js'), params: ['id'], nav: 'nodes' },
  { pattern: /^\/incidents$/, view: () => import('./views/incidents.js'), nav: 'incidents' },
  { pattern: /^\/settings(?:\/([a-z]+))?$/, view: () => import('./views/settings.js'), params: ['tab'], nav: 'settings' },
  { pattern: /^\/wallboard$/, view: () => import('./views/wallboard.js'), nav: 'wallboard', wallboard: true },
];

const viewRoot = document.getElementById('view');
const titleEl = document.getElementById('page-title');
const subtitleEl = document.getElementById('page-subtitle');
const actionsEl = document.getElementById('page-actions');
const banner = document.getElementById('api-banner');
const healthLink = document.getElementById('service-health');

let current = null; // { instance, route, path }
let navToken = 0;

/* Sidebar icons */
document.querySelectorAll('[data-icon]').forEach((el) => { el.append(icon(el.dataset.icon)); });

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
  setTitle(title, { subtitle, actions } = {}) {
    titleEl.textContent = title;
    document.title = title === 'Dashboard' ? 'GWatch' : `${title} — GWatch`;
    subtitleEl.hidden = !subtitle;
    subtitleEl.textContent = subtitle || '';
    clear(actionsEl);
    if (actions) replace(actionsEl, actions);
  },
};

async function route() {
  const token = ++navToken;
  closeMenus();
  const { path, query } = parseHash();
  let match = null; let r = null;
  for (const candidate of routes) {
    const m = candidate.pattern.exec(path);
    if (m) { match = m; r = candidate; break; }
  }
  if (!r) { navigate('/dashboard'); return; }
  const params = {};
  (r.params || []).forEach((name, i) => { params[name] = match[i + 1]; });

  // Same view, only params changed? Let the view handle it if it can.
  if (current && current.route === r && current.instance?.update) {
    const handled = await current.instance.update(params, query);
    if (handled) { current.path = path; setNav(r.nav); return; }
  }

  if (current?.instance?.destroy) { try { current.instance.destroy(); } catch (e) { console.error(e); } }
  current = null;
  document.body.classList.toggle('wallboard-mode', !!r.wallboard);
  setNav(r.nav);
  clear(actionsEl);
  subtitleEl.hidden = true;
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
  const ctx = { ...ctxBase, params, query, root: viewRoot };
  try {
    const instance = await mod.mount(viewRoot, ctx);
    if (token !== navToken) { instance?.destroy?.(); return; }
    current = { instance: instance || {}, route: r, path };
  } catch (e) {
    console.error(e);
    replace(viewRoot, h('div', { class: 'card' }, h('h2', null, 'Something went wrong'), h('p', { class: 'muted' }, String(e.message || e))));
  }
  window.scrollTo(0, 0);
}

function setNav(name) {
  document.querySelectorAll('.nav-link[data-nav]').forEach((a) => {
    const active = a.dataset.nav === name;
    a.classList.toggle('active', active);
    if (active) a.setAttribute('aria-current', 'page'); else a.removeAttribute('aria-current');
  });
}

/* ---------- Connection banner ---------- */
function updateBanner() {
  banner.hidden = connection.ok;
}
onConnection(updateBanner);
document.getElementById('api-banner-retry').addEventListener('click', () => { refreshCurrent(); refreshHealth(); });

/* ---------- Service health dot ---------- */
let lastHealth = null;
async function refreshHealth() {
  try {
    lastHealth = await api.get('/api/health');
    renderHealth();
  } catch {
    lastHealth = null;
    healthLink.className = 'service-health issue';
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
  healthLink.className = `service-health ${issues.length ? 'issue' : 'ok'}`;
  healthLink.querySelector('.health-text').textContent = issues.length ? 'Service issue' : `Service healthy`;
  healthLink.title = issues.length ? issues.join(', ') : `Last check ${relTime(hl.lastCheckAt)}`;
}
export function getHealth() { return lastHealth; }

/* ---------- Live updates ---------- */
const refreshCurrent = debounce(() => {
  if (current?.instance?.refresh) {
    Promise.resolve(current.instance.refresh()).catch((e) => console.warn('refresh failed', e));
  }
}, 500);
const refreshHealthDebounced = debounce(refreshHealth, 2000);

subscribeUpdates((update) => {
  refreshCurrent();
  if (!update || update.kind === 'state' || update.kind === 'config' || update.kind === 'event') refreshHealthDebounced();
});
setInterval(refreshHealth, 60000);
refreshHealth();

/* ---------- Relative-time ticking ---------- */
setInterval(() => {
  document.querySelectorAll('[data-rel]').forEach((el) => { if (el.dataset.rel) el.textContent = relTime(el.dataset.rel); });
}, 10000);

window.addEventListener('hashchange', route);
window.addEventListener('error', (e) => { if (e.message) console.error('Unhandled:', e.message); });
window.addEventListener('unhandledrejection', (e) => {
  const msg = e.reason?.message || String(e.reason);
  if (e.reason?.name === 'ApiError' && e.reason.status === 0) return; // banner handles it
  console.error('Unhandled promise rejection:', msg);
  toast(msg, { kind: 'error' });
});

route();

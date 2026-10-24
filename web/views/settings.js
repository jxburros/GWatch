// Settings: general, appearance, network access, alerts, automation
// (endpoints + all triggers), rules, AI & MCP, retention, maintenance,
// backups, database, updates, monitor health. Logs moved to the Audit tab.

import { api, qs } from '../api.js';
import { h, icon, clear, replace, field, numberInput, toast, emptyState, skeleton, banner, busy } from '../components.js';
import { create as createShared } from './settings/shared.js';

// `viewer: true` marks the sections an account without the admin role may
// open. Everything else reads or writes settings, which the server refuses to
// a viewer, so those tabs are not offered at all.
const TABS = [
  { id: 'general', label: 'General' }, { id: 'appearance', label: 'Appearance', viewer: true },
  { id: 'indicators', label: 'Indicators' }, { id: 'users', label: 'Users & access' },
  { id: 'network', label: 'Network access' }, { id: 'alerts', label: 'Alerts' },
  { id: 'automation', label: 'Automation' }, { id: 'rules', label: 'Rules' },
  { id: 'mcp', label: 'AI & MCP' }, { id: 'retention', label: 'Retention' }, { id: 'maintenance', label: 'Maintenance' },
  { id: 'backups', label: 'Backups' }, { id: 'database', label: 'Database' }, { id: 'updates', label: 'Updates' }, { id: 'health', label: 'Monitor health', viewer: true },
  { id: 'about', label: 'About', viewer: true },
];

export async function mount(root, ctx) {
  const isAdmin = !!ctx.me?.isAdmin;
  const visibleTabs = TABS.filter((t) => isAdmin || t.viewer);
  const state = { tab: visibleTabs.some((t) => t.id === ctx.params.tab) ? ctx.params.tab : (isAdmin ? 'general' : 'health'), settings: null, version: null, destroyed: false, panelRefresh: null };
  if (ctx.params.tab === 'logs') { ctx.navigate('/audit/log'); return { destroy() {} }; }
  // Machines are nodes, and adding one is part of Add node; an old link to the
  // Hardware section goes to the node list, where that now lives.
  if (ctx.params.tab === 'hardware') { ctx.navigate('/nodes'); return { destroy() {} }; }
  const nav = h('nav', { class: 'settings-nav', 'aria-label': 'Settings sections' });
  const panel = h('div', { class: 'settings-panel' });
  const versionLink = h('a', { href: '#/settings/about' });
  const versionEl = h('div', { class: 'version-line' }, versionLink);
  root.append(h('div', { class: 'settings-layout' }, nav, h('div', null, panel, versionEl)));

  api.get('/api/version').then((v) => { state.version = v; versionLink.textContent = `GWatch ${v.version || ''} · ${v.platform || ''}`; }).catch(() => {});

  function renderNav() {
    clear(nav);
    const search = h('input', { type: 'search', 'aria-label': 'Find a settings section', placeholder: 'Find a section…', oninput: () => { for (const link of nav.querySelectorAll('a')) link.hidden = !link.textContent.toLowerCase().includes(search.value.toLowerCase()); } });
    nav.append(search);
    for (const t of visibleTabs) nav.append(h('a', { href: `#/settings/${t.id}`, class: t.id === state.tab ? 'active' : '', 'aria-current': t.id === state.tab ? 'page' : null }, t.label));
  }

  async function loadSettings() { state.settings = await api.get('/api/settings'); return state.settings; }
  /** Resolves to the saved document, or to null when the save was refused —
   *  a caller that shows what was stored must not take its own edit for it. */
  async function saveSettings(btn) {
    const done = btn ? busy(btn, 'Saving…') : () => {};
    let saved = null;
    try { saved = state.settings = adoptSaved(state.settings, await api.put('/api/settings', state.settings)); toast('Settings saved', { kind: 'success' }); }
    catch (e) { toast(e.message, { kind: 'error' }); }
    done();
    return saved;
  }

  /** Takes the server's copy into the document in place, rather than putting
   *  a new object in its place: the open tab's fields hold on to the objects
   *  inside it (general, alerts.smtp, backups …), and edits written to a
   *  replaced copy would never be sent by the next save. */
  function adoptSaved(into, from) {
    if (!into || typeof into !== 'object' || Array.isArray(into)) return from;
    for (const [k, v] of Object.entries(from || {})) {
      const cur = into[k];
      if (v && typeof v === 'object' && !Array.isArray(v) && cur && typeof cur === 'object' && !Array.isArray(cur)) adoptSaved(cur, v);
      else into[k] = v;
    }
    return into;
  }

  async function renderTab() {
    const t = visibleTabs.find((x) => x.id === state.tab) || visibleTabs[0];
    state.tab = t.id;
    renderNav();
    ctx.setTitle('Settings');
    replace(panel, skeleton({ lines: 5 }));
    state.panelRefresh = null;
    try {
      // Each section is its own module, web/views/settings/<tab>.js, loaded
      // the first time it is opened (#118). Its tab function is tab<Tab>.
      const part = (await import(`./settings/${state.tab}.js`)).create(shell);
      const fn = part[`tab${state.tab[0].toUpperCase()}${state.tab.slice(1)}`];
      const el = await fn();
      if (state.destroyed) return;
      replace(panel, isAdmin ? el : h('div', { class: 'stack' }, readOnlyNotice(), el));
    } catch (e) { replace(panel, h('div', { class: 'card' }, emptyState({ icon: 'alert', title: 'Could not load settings', text: e.message }))); }
  }

  // A viewer sees why most of this page is missing, rather than a page that
  // just looks broken.
  const readOnlyNotice = () => banner('info',
    `You are signed in as a viewer${ctx.me?.name ? ` (${ctx.me.name})` : ''}. You can see this monitor but not change it — settings, accounts, automation and backups need an administrator account.`);

  const saveBar = (label = 'Save changes') => { const b = h('button', { class: 'btn btn-primary', type: 'button', onclick: () => saveSettings(b) }, icon('save'), label); return h('div', { class: 'form-actions' }, b); };
  const unit = (input, u) => h('div', { class: 'input-with-unit' }, input, h('span', { class: 'unit' }, u));
  const numField = (obj, key, label, { unitLabel, help, min = 0, max, step } = {}) => {
    const input = numberInput({ value: obj[key] ?? '', min, step, ...(max != null ? { max } : {}), oninput: () => { obj[key] = Number(input.value) || 0; } });
    return field({ label, input: unitLabel ? unit(input, unitLabel) : input, help });
  };

  // The parts of this view (#118) are created with the bindings they read.
  const shell = { ctx, isAdmin, loadSettings, numField, saveBar, saveSettings, state, unit };
  Object.assign(shell, createShared(shell));
  await renderTab();
  return {
    refresh: () => state.panelRefresh && state.panelRefresh(),
    async update(params) {
      // The same two retired links as at mount, followed when they are reached
      // from inside Settings, where the view is updated rather than mounted.
      if (params.tab === 'hardware') { ctx.navigate('/nodes'); return true; }
      if (params.tab === 'logs') { ctx.navigate('/audit/log'); return true; }
      const tab = visibleTabs.some((t) => t.id === params.tab) ? params.tab : visibleTabs[0].id;
      if (tab !== state.tab) { state.tab = tab; await renderTab(); }
      return true;
    },
    destroy() { state.destroyed = true; },
  };
}

export { qs };

// Settings: general, appearance, network access, alerts, automation
// (endpoints + all triggers), hardware, AI & MCP, retention, maintenance,
// backups, updates, monitor health. Logs moved to the Audit tab.

import { api, qs } from '../api.js';
import { h, icon, clear, replace, field, textInput, numberInput, textarea, selectInput, checkbox, toggle, chipInput, toast, confirmDialog, openModal, emptyState, skeleton, banner, eventRow, busy, applyTheme, applyAccent, ACCENT_PRESETS, hexToRgb, applyDensity, currentDensity } from '../components.js';
import { relTime, dateTime, bytes, num, duration, retentionSpan, toLocalInput, fromLocalInput, weekdayShort, timeShort, plural, isBeta } from '../fmt.js';
import { openEndpointEditor, endpointRow, triggerRow, openTriggerEditor } from './automation.js';
import { tipsEnabled, setTipsEnabled, resetTips, seenCount, resetOnboarding, TIPS } from '../tips.js';

// `viewer: true` marks the sections an account without the admin role may
// open. Everything else reads or writes settings, which the server refuses to
// a viewer, so those tabs are not offered at all.
const TABS = [
  { id: 'general', label: 'General' }, { id: 'appearance', label: 'Appearance', viewer: true },
  { id: 'indicators', label: 'Indicators' }, { id: 'users', label: 'Users & access' },
  { id: 'network', label: 'Network access' }, { id: 'alerts', label: 'Alerts' },
  { id: 'automation', label: 'Automation' }, { id: 'hardware', label: 'Hardware' },
  { id: 'mcp', label: 'AI & MCP' }, { id: 'retention', label: 'Retention' }, { id: 'maintenance', label: 'Maintenance' },
  { id: 'backups', label: 'Backups' }, { id: 'updates', label: 'Updates' }, { id: 'health', label: 'Monitor health', viewer: true },
  { id: 'about', label: 'About', viewer: true },
];

// The ping methods GeneralSettings.PingMethod accepts. A check may override
// the global choice with the same values, plus "" for "follow the setting".
const PING_METHODS = [
  { value: 'auto', label: 'Auto (recommended)' },
  { value: 'builtin', label: 'Built-in sender' },
  { value: 'system', label: 'System ping command' },
];

const REPO_URL = 'https://github.com/jxburros/GWatch';
const GWATCH_COPYRIGHT = 'Copyright (c) 2026 JX Holdings. Original developers: Jeffrey Guntly and Garrett Guntly.';
// Go module dependencies from go.mod, with licenses confirmed by reading each
// module's LICENSE file under $(go env GOMODCACHE).
const DEPENDENCIES = [
  { name: 'kardianos/service', use: 'runs GWatch as a background service on Windows, macOS and Linux', license: 'zlib' },
  { name: 'modernc.org/sqlite', use: 'the embedded database that stores history, events and settings', license: 'BSD-3-Clause' },
  { name: 'golang.org/x/crypto', use: 'password hashing for accounts and the access password', license: 'BSD-3-Clause' },
];

export async function mount(root, ctx) {
  const isAdmin = !!ctx.me?.isAdmin;
  const visibleTabs = TABS.filter((t) => isAdmin || t.viewer);
  const state = { tab: visibleTabs.some((t) => t.id === ctx.params.tab) ? ctx.params.tab : (isAdmin ? 'general' : 'health'), settings: null, version: null, destroyed: false, panelRefresh: null };
  if (ctx.params.tab === 'logs') { ctx.navigate('/audit/log'); return { destroy() {} }; }
  const nav = h('nav', { class: 'settings-nav', 'aria-label': 'Settings sections' });
  const panel = h('div', { class: 'settings-panel' });
  const versionLink = h('a', { href: '#/settings/about' });
  const versionEl = h('div', { class: 'version-line' }, versionLink);
  root.append(h('div', { class: 'settings-layout' }, nav, h('div', null, panel, versionEl)));

  api.get('/api/version').then((v) => { state.version = v; versionLink.textContent = `GWatch ${v.version || ''} · ${v.platform || ''}`; }).catch(() => {});

  function renderNav() {
    clear(nav);
    for (const t of visibleTabs) nav.append(h('a', { href: `#/settings/${t.id}`, class: t.id === state.tab ? 'active' : '', 'aria-current': t.id === state.tab ? 'page' : null }, t.label));
  }

  async function loadSettings() { state.settings = await api.get('/api/settings'); return state.settings; }
  async function saveSettings(btn) {
    const done = btn ? busy(btn, 'Saving…') : () => {};
    try { state.settings = await api.put('/api/settings', state.settings); toast('Settings saved', { kind: 'success' }); }
    catch (e) { toast(e.message, { kind: 'error' }); }
    done();
    return state.settings;
  }

  async function renderTab() {
    const t = visibleTabs.find((x) => x.id === state.tab) || visibleTabs[0];
    state.tab = t.id;
    renderNav();
    ctx.setTitle('Settings');
    replace(panel, skeleton({ lines: 5 }));
    state.panelRefresh = null;
    try {
      const fn = { general: tabGeneral, appearance: tabAppearance, indicators: tabIndicators, users: tabUsers, network: tabNetwork, alerts: tabAlerts, automation: tabAutomation, hardware: tabHardware, mcp: tabMcp, retention: tabRetention, maintenance: tabMaintenance, backups: tabBackups, updates: tabUpdates, health: tabHealth, about: tabAbout }[state.tab];
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
  const numField = (obj, key, label, { unitLabel, help, min = 0, step } = {}) => {
    const input = numberInput({ value: obj[key] ?? '', min, step, oninput: () => { obj[key] = Number(input.value) || 0; } });
    return field({ label, input: unitLabel ? unit(input, unitLabel) : input, help });
  };

  /* ---------- General ---------- */
  async function tabGeneral() {
    const s = state.settings || await loadSettings();
    const g = s.general;
    const name = textInput({ value: g.instanceName || '', placeholder: 'GWatch', oninput: () => { g.instanceName = name.value; } });
    const pingMethod = selectInput({ options: PING_METHODS, value: g.pingMethod || 'auto', onchange: () => { g.pingMethod = pingMethod.value; } });
    return h('form', { class: 'stack', onsubmit: (e) => { e.preventDefault(); saveSettings(); } },
      h('section', { class: 'card' }, h('h2', null, 'General'), h('p', { class: 'lead' }, 'Defaults for new checks and protection against overloading the machine.'),
        h('div', { class: 'form-grid' },
          field({ label: 'Instance name', input: name, help: 'Shown in the wallboard and in alert emails.' }),
          numField(g, 'wallboardRefreshSeconds', 'Wallboard refresh', { unitLabel: 'seconds', min: 5 }),
          numField(g, 'defaultIntervalSeconds', 'Default check interval', { unitLabel: 'seconds', min: 10, help: 'Used for new checks; each check can override it.' }),
          numField(g, 'defaultTimeoutSeconds', 'Default timeout', { unitLabel: 'seconds', min: 1 }),
          numField(g, 'minIntervalSeconds', 'Minimum interval allowed', { unitLabel: 'seconds', min: 5, help: 'Prevents accidental very aggressive schedules.' }),
          numField(g, 'maxConcurrentChecks', 'Max concurrent checks', { min: 1, help: 'How many checks may run at the same time.' }),
          numField(g, 'latencyWarnMs', 'Latency warning default', { unitLabel: 'ms', help: '0 = off. Marks checks degraded when slower than this.' }),
          numField(g, 'packetLossWarnPct', 'Packet-loss warning default', { unitLabel: '%', help: '0 = off. Applies to ping checks.' }),
        )),
      // Ping is the one check type whose mechanics a reader may have to take a
      // hand in: sending an echo request needs a socket the operating system
      // may refuse to hand out. The setting sits here with the other defaults,
      // and any individual check can override it.
      h('section', { class: 'card' }, h('h2', null, 'Ping'),
        h('p', { class: 'lead' }, 'How ping checks send their echo requests. Any individual check can override this.'),
        h('div', { class: 'form-grid' },
          field({
            label: 'Ping method',
            input: pingMethod,
            help: 'Auto tries the built-in sender and falls back to the system ping command. Built-in never runs an external program. System ping uses the operating system\'s ping command, which is the way out where raw sockets are not permitted.',
          }),
        ),
        h('hr', { class: 'divider' }), saveBar()));
  }

  /* ---------- Appearance ---------- */
  async function tabAppearance() {
    // A viewer cannot read settings, so their theme choice is theirs alone:
    // it starts from what /api/me reported and is remembered by this browser.
    const g = isAdmin ? (state.settings || await loadSettings()).general : { theme: ctx.me?.theme || 'dark', accentColor: ctx.me?.accentColor || '#43c9c0' };
    const themes = [
      { value: 'dark', label: 'Dark', desc: 'Low-glare, for wall displays and night owls.', bg: '#0f1114', card: '#16181d', fg: '#f2f4f6' },
      { value: 'light', label: 'Light', desc: 'Bright, high contrast on white.', bg: '#f1f2f4', card: '#ffffff', fg: '#10151d' },
      { value: 'system', label: 'System', desc: 'Follow the operating system preference.', bg: 'linear-gradient(90deg, #0f1114 50%, #f1f2f4 50%)', card: 'linear-gradient(90deg, #16181d 50%, #ffffff 50%)', fg: '#9aa3ac' },
    ];
    const themeWrap = h('div', { class: 'theme-options', role: 'radiogroup', 'aria-label': 'Theme' });
    const renderThemes = () => {
      clear(themeWrap);
      for (const t of themes) {
        themeWrap.append(h('button', { type: 'button', role: 'radio', class: `theme-option ${g.theme === t.value ? 'active' : ''}`, 'aria-checked': g.theme === t.value ? 'true' : 'false', onclick: () => { g.theme = t.value; applyTheme(t.value); renderThemes(); } },
          h('div', { class: 'preview', style: { background: t.bg } }, h('i', { style: { background: t.card } }), h('i', { style: { background: t.card, margin: '8px', boxShadow: `inset 0 3px 0 rgb(var(--accent-rgb))` } })),
          h('b', null, t.label), h('span', null, t.desc)));
      }
    };
    renderThemes();
    // #32: density is a per-browser preference, like the pinned sidebar — it
    // never touches state.settings and there is nothing here for a viewer to
    // be locked out of, so it renders the same way for both roles.
    const densities = [
      { value: 'compact', label: 'Compact (default)', desc: 'Tighter rows, so a long node list scrolls easily.' },
      { value: 'comfortable', label: 'Breathing room', desc: 'The roomier spacing GWatch used to ship with.' },
    ];
    const densityWrap = h('div', { class: 'theme-options density-options', role: 'radiogroup', 'aria-label': 'List density' });
    const renderDensities = () => {
      clear(densityWrap);
      const current = currentDensity();
      for (const d of densities) {
        densityWrap.append(h('button', { type: 'button', role: 'radio', class: `theme-option ${current === d.value ? 'active' : ''}`, 'aria-checked': current === d.value ? 'true' : 'false', onclick: () => { applyDensity(d.value); renderDensities(); } },
          h('div', { class: `density-preview ${d.value === 'comfortable' ? 'is-comfortable' : ''}` }, h('i'), h('i'), h('i')),
          h('b', null, d.label), h('span', null, d.desc)));
      }
    };
    renderDensities();
    const swatches = h('div', { class: 'swatches', role: 'radiogroup', 'aria-label': 'Accent colour' });
    const custom = h('input', { type: 'color', value: g.accentColor || '#43c9c0', 'aria-label': 'Custom accent colour', oninput: () => { g.accentColor = custom.value; applyAccent(custom.value); renderSwatches(); } });
    const hex = textInput({ value: g.accentColor || '#43c9c0', class: 'mono', style: { maxWidth: '110px' }, 'aria-label': 'Accent hex', oninput: () => { if (hexToRgb(hex.value)) { g.accentColor = hex.value.toLowerCase(); custom.value = g.accentColor; applyAccent(g.accentColor); renderSwatches(); } } });
    const renderSwatches = () => {
      clear(swatches);
      for (const p of ACCENT_PRESETS) swatches.append(h('button', { type: 'button', role: 'radio', class: `swatch ${(g.accentColor || '').toLowerCase() === p.hex ? 'active' : ''}`, 'aria-checked': (g.accentColor || '').toLowerCase() === p.hex ? 'true' : 'false', title: p.name, style: { background: p.hex }, onclick: () => { g.accentColor = p.hex; custom.value = p.hex; hex.value = p.hex; applyAccent(p.hex); renderSwatches(); } }));
      swatches.append(custom, hex);
    };
    renderSwatches();
    return h('form', { class: 'stack', onsubmit: (e) => { e.preventDefault(); saveSettings(); } },
      h('section', { class: 'card' }, h('h2', null, 'Theme'),
        h('p', { class: 'lead' }, isAdmin
          ? 'Changes apply immediately; press Save to keep them for every browser that opens this GWatch.'
          : 'Changes apply immediately and are remembered by this browser. Only an administrator can change the theme for everyone.'),
        themeWrap),
      h('section', { class: 'card' }, h('h2', null, 'Density'),
        h('p', { class: 'lead' }, 'How tightly node rows, checks and lists are packed. Remembered by this browser only — there is no shared setting for it.'),
        densityWrap),
      h('section', { class: 'card' }, h('h2', null, 'Accent colour'), h('p', { class: 'lead' }, 'Used for buttons, highlights, the active navigation item and the first chart line.'), swatches,
        h('div', { class: 'row', style: { marginTop: '14px', gap: '8px' } }, h('button', { class: 'btn btn-primary', type: 'button' }, 'Primary button'), h('button', { class: 'btn', type: 'button' }, 'Button'), h('span', { class: 'chip active' }, 'Active chip'), h('a', { href: '#/settings/appearance' }, 'A link')),
        isAdmin ? h('hr', { class: 'divider' }) : null, isAdmin ? saveBar() : null),
      guidanceCard());
  }

  /* Tips and the first-run tour are a property of this browser, not of the
     monitor, so they sit with the theme rather than in the saved settings and
     a viewer may change them as freely as an administrator. */
  function guidanceCard() {
    const seen = h('p', { class: 'note' });
    const refresh = () => { seen.textContent = tipsEnabled() ? `${seenCount()} of ${TIPS.length} tips shown so far.` : 'Tips are off. Nothing appears until you turn them on.'; };
    const tips = toggle({ label: 'Show tips as I go', checked: tipsEnabled(), onChange: (on) => { setTipsEnabled(on); refresh(); } });
    refresh();
    return h('section', { class: 'card' }, h('h2', null, 'Guidance'),
      h('p', { class: 'lead' }, 'Tips are small hints that point at one control and explain what it does. Each one appears once and is then gone. Remembered by this browser only.'),
      h('div', { class: 'stack-sm' }, tips, seen,
        h('div', { class: 'btn-group', style: { marginTop: '6px' } },
          h('button', { class: 'btn', type: 'button', onclick: () => { resetTips(); refresh(); toast('Tips reset', { kind: 'success' }); } }, icon('refresh'), 'Reset tips'),
          h('button', { class: 'btn', type: 'button', onclick: () => { resetOnboarding(); ctx.navigate('/onboarding'); } }, icon('play'), 'Restart onboarding'),
          h('a', { class: 'btn', href: '#/help' }, icon('help'), 'Open Help'))));
  }

  /* ---------- Indicators ---------- */
  // The conditions a rule may test, flattened into one list because
  // "nodes down" and "nodes degraded" are one choice to a reader even though
  // they are one kind and two statuses to the server. Anything offered here
  // has to be answerable from GET /api/status, which is the one document the
  // header already polls; see model.IndicatorCondition.
  const INDICATOR_CONDITIONS = [
    { value: 'nodesInStatus:down', kind: 'nodesInStatus', status: 'down', label: 'Nodes that are down', counted: 'nodes' },
    { value: 'nodesInStatus:degraded', kind: 'nodesInStatus', status: 'degraded', label: 'Nodes that are degraded', counted: 'nodes' },
    { value: 'nodesInStatus:unknown', kind: 'nodesInStatus', status: 'unknown', label: 'Nodes waiting for a first result', counted: 'nodes' },
    { value: 'nodesInStatus:maintenance', kind: 'nodesInStatus', status: 'maintenance', label: 'Nodes in maintenance', counted: 'nodes' },
    { value: 'certWarnings:', kind: 'certWarnings', status: '', label: 'Certificates expiring or invalid', counted: 'certificates' },
    { value: 'attention:', kind: 'attention', status: '', label: 'Checks needing attention', counted: 'checks' },
    { value: 'serviceHealth:', kind: 'serviceHealth', status: '', label: 'The monitor itself is unwell', counted: '' },
  ];
  const INDICATOR_COLOURS = [
    { value: 'red', label: 'Red — needs looking at now' },
    { value: 'orange', label: 'Orange — worth knowing about' },
    { value: 'yellow', label: 'Yellow — for information' },
  ];
  const condKey = (c = {}) => `${c.kind || 'nodesInStatus'}:${c.kind === 'nodesInStatus' ? (c.status || 'down') : ''}`;
  const condOf = (key) => INDICATOR_CONDITIONS.find((c) => c.value === key) || INDICATOR_CONDITIONS[0];

  async function tabIndicators() {
    // `s` is re-pointed after every save, because saveSettings replaces
    // state.settings with the document the server sends back — normalised,
    // with the ids and counts it filled in.
    let s = state.settings || await loadSettings();
    if (!Array.isArray(s.indicators)) s.indicators = [];
    const wrap = h('div', { class: 'stack' });
    // A rule the browser has never seen a server id for gets one here, so
    // that a freshly added row is as complete as a stored one.
    const newId = () => `indicator-${Date.now().toString(36)}-${Math.floor(Math.random() * 1e4)}`;

    const render = () => {
      clear(wrap);
      const rules = h('div', { class: 'ind-rules' });
      s.indicators.forEach((rule, i) => rules.append(ruleRow(rule, i)));
      const card = h('section', { class: 'card' },
        h('div', { class: 'card-head' }, h('h2', null, 'Indicators'),
          h('button', { class: 'btn', type: 'button', onclick: () => { s.indicators.push({ id: newId(), name: 'New indicator', enabled: true, colour: 'yellow', condition: { kind: 'nodesInStatus', status: 'down', minCount: 1 } }); render(); } }, icon('plus'), 'Add indicator')),
        h('p', { class: 'lead' }, 'The circles under the page name in the header. One green circle means nothing here is firing; blue means there is nothing to report on yet — no nodes, or none that has produced a result. When a rule fires, green gives way to one circle per firing rule, reddest first, and clicking one opens what it is about.'),
        s.indicators.length ? rules : emptyState({ icon: 'alert', title: 'No indicators', text: 'With no rules the header only ever shows the green or blue circle. Add one, or restore the defaults below.', compact: true }),
        h('hr', { class: 'divider' }),
        h('div', { class: 'form-actions' },
          h('button', { class: 'btn btn-primary', type: 'button', onclick: (e) => save(e.currentTarget) }, icon('save'), 'Save changes'),
          h('button', { class: 'btn', type: 'button', onclick: async () => {
            if (!await confirmDialog({ title: 'Restore the default indicators?', message: 'The rules you have added or changed here are replaced by the six GWatch ships with. Nothing is saved until you press Save changes.', confirmLabel: 'Restore' })) return;
            // An empty list is what the server reads as "seed the defaults",
            // so saving is what fetches them back: this one button does not
            // wait for Save changes, because there is nothing to review.
            s.indicators = [];
            await saveSettings();
            adopt();
            window.dispatchEvent(new CustomEvent('gw:indicators-changed'));
            render();
          } }, icon('refresh'), 'Restore defaults')));
      wrap.append(card, previewCard(), h('section', { class: 'card' }, h('h2', null, 'Notes'),
        h('div', { class: 'stack-sm', style: { marginTop: '8px' } },
          h('p', { class: 'note' }, h('b', null, 'Who sees them: '), 'everyone. The rules are read from settings, which only an administrator may open, so the effective list is served to every account alongside the theme — a viewer’s header shows exactly what yours does.'),
          h('p', { class: 'note' }, h('b', null, 'Where they are evaluated: '), 'in the browser, against the same status summary the header already fetches. That is why the list of conditions is short: each one has to be answerable from that one summary, without a second request on every poll.'),
          h('p', { class: 'note' }, h('b', null, 'Green and blue: '), 'neither is a rule. Green is what is left when nothing fires, and blue says GWatch has nothing to go on yet — while it is blue, only the monitor’s own health can still light a circle.'))));
    };

    const previewCard = () => {
      const row = h('div', { class: 'indicators' });
      const note = h('span', { class: 'muted tiny' }, 'Loading the current status…');
      api.get('/api/status').then((st) => {
        const firing = s.indicators.filter((r) => r.enabled && count(st, r.condition || {}) >= Math.max(1, Number(r.condition?.minCount) || 1));
        clear(row);
        const rank = { red: 0, orange: 1, yellow: 2 };
        firing.sort((a, b) => (rank[a.colour] ?? 9) - (rank[b.colour] ?? 9));
        for (const r of firing) row.append(h('span', { class: `indicator ind-${r.colour}`, title: r.name }, h('span', { class: 'orb' })));
        if (!firing.length) row.append(h('span', { class: 'indicator ind-ok' }, h('span', { class: 'orb' })));
        note.textContent = firing.length ? `${firing.map((r) => r.name).join(', ')} — as your rules stand, unsaved changes included.` : 'Nothing is firing right now, so the header shows the single all-clear circle.';
      }).catch(() => { note.textContent = 'The current status could not be read.'; });
      const count = (st, c) => {
        switch (c.kind) {
          case 'nodesInStatus': return Number({ down: st.down, degraded: st.degraded, unknown: st.unknown, maintenance: st.maintenance }[c.status]) || 0;
          case 'certWarnings': return Number(st.certWarnings) || 0;
          case 'attention': return Number(st.attention) || 0;
          case 'serviceHealth': return st.serviceOk ? 0 : 1;
          default: return 0;
        }
      };
      return h('section', { class: 'card' }, h('h2', null, 'Right now'),
        h('p', { class: 'lead' }, 'What the header would show with these rules.'),
        h('div', { class: 'ind-preview' }, row, note));
    };

    const ruleRow = (rule, i) => {
      rule.condition = rule.condition || { kind: 'nodesInStatus', status: 'down', minCount: 1 };
      const row = h('div', { class: `ind-rule ${rule.enabled ? '' : 'off'}` });
      const on = toggle({ checked: rule.enabled !== false, ariaLabel: `Enable ${rule.name || 'indicator'}`, onChange: (v) => { rule.enabled = v; row.classList.toggle('off', !v); } });
      const swatches = h('div', { class: 'ind-swatches', role: 'radiogroup', 'aria-label': 'Severity' });
      const renderSwatches = () => {
        clear(swatches);
        for (const c of INDICATOR_COLOURS) {
          swatches.append(h('button', { type: 'button', role: 'radio', class: `ind-swatch ind-swatch-${c.value}`, title: c.label, 'aria-label': c.label, 'aria-checked': rule.colour === c.value ? 'true' : 'false', onclick: () => { rule.colour = c.value; renderSwatches(); } }));
        }
      };
      renderSwatches();
      const name = textInput({ value: rule.name || '', class: 'ind-rule-name', 'aria-label': 'Indicator name', placeholder: 'What it means', oninput: () => { rule.name = name.value; } });
      const cond = selectInput({ options: INDICATOR_CONDITIONS.map((c) => ({ value: c.value, label: c.label })), value: condKey(rule.condition), 'aria-label': 'Condition' });
      const min = numberInput({ value: rule.condition.minCount || 1, min: 1, step: 1, 'aria-label': 'How many it takes', oninput: () => { rule.condition.minCount = Math.max(1, Number(min.value) || 1); } });
      const minWrap = h('span', { class: 'ind-rule-cond' }, h('span', { class: 'muted tiny' }, 'at least'), min, h('span', { class: 'muted tiny' }, ''));
      const syncCond = () => {
        const c = condOf(cond.value);
        rule.condition.kind = c.kind;
        rule.condition.status = c.status;
        // The monitor is unwell or it is not, so a count would be a fiction.
        minWrap.hidden = c.kind === 'serviceHealth';
        minWrap.lastChild.textContent = c.counted;
        if (minWrap.hidden) rule.condition.minCount = 1;
      };
      cond.addEventListener('change', syncCond);
      syncCond();
      const move = (delta) => {
        const to = i + delta;
        if (to < 0 || to >= s.indicators.length) return;
        const [moved] = s.indicators.splice(i, 1);
        s.indicators.splice(to, 0, moved);
        render();
      };
      row.append(on, swatches,
        h('div', { class: 'ind-rule-cond' }, name, cond, minWrap),
        h('div', { class: 'ind-rule-actions' },
          h('button', { class: 'btn btn-sm', type: 'button', 'aria-label': 'Move up', title: 'Move up', disabled: i === 0, onclick: () => move(-1) }, '↑'),
          h('button', { class: 'btn btn-sm', type: 'button', 'aria-label': 'Move down', title: 'Move down', disabled: i === s.indicators.length - 1, onclick: () => move(1) }, '↓'),
          h('button', { class: 'btn btn-sm btn-danger', type: 'button', 'aria-label': `Remove ${rule.name || 'indicator'}`, title: 'Remove', onclick: () => { s.indicators.splice(i, 1); render(); } }, icon('trash'))));
      return row;
    };

    /** Take up the document the server sent back in place of the one edited
     *  here, so the next edit starts from what is actually stored. */
    function adopt() {
      s = state.settings;
      if (!Array.isArray(s.indicators)) s.indicators = [];
    }

    async function save(btn) {
      for (const r of s.indicators) { if (!r.id) r.id = newId(); if (!String(r.name || '').trim()) r.name = 'Indicator'; }
      await saveSettings(btn);
      adopt();
      // Relight the header at once; otherwise the author of the rule waits
      // for the next configuration update to come down the stream.
      window.dispatchEvent(new CustomEvent('gw:indicators-changed'));
      render();
    }

    render();
    return wrap;
  }

  /* ---------- Network access ---------- */
  async function tabNetwork() {
    const s = state.settings || await loadSettings();
    const g = s.general;
    const info = await api.get('/api/network').catch(() => null);
    const remote = toggle({ label: 'Allow access from other devices on my network', checked: !!g.remoteAccess, onChange: (v) => { g.remoteAccess = v; } });
    const pw = h('input', { type: 'password', value: g.accessPassword || '', autocomplete: 'new-password', placeholder: info?.passwordSet ? '(unchanged)' : 'Optional but recommended', oninput: () => { g.accessPassword = pw.value; } });
    const clearPw = h('button', { class: 'btn btn-sm', type: 'button', onclick: () => { pw.value = ''; g.accessPassword = ''; toast('Password will be removed when you save', { kind: 'info' }); } }, 'Remove password');
    const urls = h('div', { class: 'url-list' });
    const renderUrls = (ni) => {
      clear(urls);
      if (!ni) { urls.append(h('span', { class: 'muted' }, 'Network information unavailable.')); return; }
      urls.append(h('a', { href: ni.localUrl, target: '_blank', rel: 'noopener' }, icon('home'), ni.localUrl, h('span', { class: 'dim' }, ' — this computer')));
      if (ni.remoteAccess) {
        if (!ni.lanUrls?.length) urls.append(h('span', { class: 'muted' }, 'No network addresses found on this computer.'));
        for (const u of ni.lanUrls || []) urls.append(h('a', { href: u, target: '_blank', rel: 'noopener' }, icon('wifi'), u));
      } else {
        urls.append(h('span', { class: 'muted' }, icon('lock'), 'Only reachable from this computer right now.'));
      }
    };
    renderUrls(info);
    const saveBtn = h('button', { class: 'btn btn-primary', type: 'button', onclick: async () => {
      if (g.remoteAccess && !g.accessPassword && !(info?.passwordSet && pw.value === '')) {
        const ok = await confirmDialog({ title: 'Open without a password?', message: 'Anyone on your network will be able to see and change everything, including triggers that run commands on this computer. Setting a password is strongly recommended.', confirmLabel: 'Open anyway', danger: true });
        if (!ok) return;
      }
      await saveSettings(saveBtn);
      setTimeout(async () => { const ni = await api.get('/api/network').catch(() => null); renderUrls(ni); if (ni?.restartNeeded) toast('Could not rebind the port; restart GWatch to apply the change.', { kind: 'error' }); }, 800);
    } }, icon('save'), 'Save changes');
    return h('form', { class: 'stack', onsubmit: (e) => { e.preventDefault(); saveBtn.click(); } },
      h('section', { class: 'card' }, h('h2', null, 'Remote access'), h('p', { class: 'lead' }, 'By default the interface is only served on this computer. Turn this on to open it from a phone, tablet or another PC on the same network. GWatch is never exposed to the internet by itself.'),
        h('div', { class: 'stack' }, remote,
          info?.listenAddress ? h('p', { class: 'note' }, 'Listening on ', h('code', null, info.listenAddress), info.remoteAccess ? ' — reachable from the network.' : ' — this computer only.', info.restartNeeded ? h('span', { class: 'text-down' }, ' Rebinding failed; a restart is needed.') : null) : null,
        ),
        h('hr', { class: 'divider' }), h('div', { class: 'form-actions' }, saveBtn)),
      h('section', { class: 'card' }, h('h2', null, 'Open GWatch from another device'), h('p', { class: 'lead' }, 'Use one of these addresses. A firewall on this computer may need to allow the port.'), urls),
      h('section', { class: 'card' }, h('h2', null, 'Legacy: shared access password'),
        h('p', { class: 'lead' }, 'One password, no user name, the same for everyone — GWatch’s original way of keeping other devices out. User accounts under ', h('a', { href: '#/settings/users' }, 'Users & access'), ' replace it: they give each person their own password, a viewer role that cannot change anything, and a name in the audit log.'),
        h('p', { class: 'note' }, 'It still works, so nothing breaks on upgrade, and scripts using it keep going. Leave it empty once you have accounts.'),
        h('div', { class: 'form-grid' }, field({ label: 'Access password', input: h('div', { class: 'input-with-unit' }, pw, clearPw), help: 'Other devices are asked for it (any user name). This computer is not, unless you require a sign-in here as well.' })),
        h('hr', { class: 'divider' }), saveBar()),
      h('section', { class: 'card' }, h('h2', null, 'Command-line alternative'), h('p', { class: 'note' }, 'You can also start the service with ', h('code', null, '--listen 0.0.0.0:7230'), ' (or set ', h('code', null, 'GWATCH_LISTEN'), ') to bind every interface regardless of this setting.')));
  }

  /* ---------- Alerts ---------- */
  async function tabAlerts() {
    const s = state.settings || await loadSettings();
    const a = s.alerts; a.smtp = a.smtp || { port: 587, security: 'starttls' };
    const enabled = toggle({ label: 'Send email alerts', checked: !!a.enabled, onChange: (v) => { a.enabled = v; } });
    const recipients = chipInput({ values: a.recipients || [], placeholder: 'Add an email address and press Enter', validate: (v) => /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(v), onChange: (v) => { a.recipients = v; } });
    const notifyRecovery = checkbox({ label: 'Send a recovery email when a check becomes healthy again', checked: !!a.notifyRecovery, onChange: (v) => { a.notifyRecovery = v; } });
    const notifyWarnings = checkbox({ label: 'Also email for warnings (high latency, packet loss, expiring certificates, content changes)', checked: !!a.notifyWarnings, onChange: (v) => { a.notifyWarnings = v; } });
    const smtp = a.smtp;
    const host = textInput({ value: smtp.host || '', placeholder: 'smtp.example.com', oninput: () => { smtp.host = host.value; } });
    const port = numberInput({ value: smtp.port || 587, min: 1, max: 65535, oninput: () => { smtp.port = Number(port.value); } });
    const security = selectInput({ options: [{ value: 'starttls', label: 'STARTTLS (port 587)' }, { value: 'tls', label: 'TLS / SSL (port 465)' }, { value: 'none', label: 'None (unencrypted)' }], value: smtp.security || 'starttls', onchange: () => { smtp.security = security.value; } });
    const user = textInput({ value: smtp.username || '', autocomplete: 'off', oninput: () => { smtp.username = user.value; } });
    const pass = h('input', { type: 'password', value: smtp.password || '', autocomplete: 'new-password', placeholder: smtp.password ? '' : 'App password or SMTP password', oninput: () => { smtp.password = pass.value; } });
    const from = textInput({ value: smtp.from || '', placeholder: 'gwatch@example.com', oninput: () => { smtp.from = from.value; } });
    const testTo = textInput({ placeholder: 'Optional: send to a different address', 'aria-label': 'Test email recipient' });
    const testMsg = h('div', { class: 'note', role: 'status' });
    const testBtn = h('button', { class: 'btn', type: 'button', onclick: async () => {
      const done = busy(testBtn, 'Sending…'); replace(testMsg, '');
      try { const r = await api.post('/api/settings/test-email', { to: testTo.value.trim() || undefined }); replace(testMsg, h('span', { class: 'status-glyph text-up' }, icon('check'), r.message || 'Test email sent.')); }
      catch (e) { replace(testMsg, h('span', { class: 'status-glyph text-down' }, icon('x'), e.message)); }
      done();
    } }, icon('mail'), 'Send test email');
    return h('form', { class: 'stack', onsubmit: (e) => { e.preventDefault(); saveSettings(); } },
      h('section', { class: 'card' }, h('h2', null, 'Alerts'), h('p', { class: 'lead' }, 'Alerts are meant to be quiet and explainable: one email when something breaks, one when it recovers. For webhooks, scripts or git commands see Automation.'),
        h('div', { class: 'stack' },
          enabled,
          field({ label: 'Recipients', input: recipients }),
          h('div', { class: 'form-grid-3' },
            numField(a, 'failureThreshold', 'Failures before alerting', { min: 1, help: 'Consecutive failed runs before a check is down.' }),
            numField(a, 'cooldownMinutes', 'Cooldown', { unitLabel: 'min', help: 'Minimum time between repeated alerts for the same check.' }),
            numField(a, 'certWarnDays', 'Certificate warning', { unitLabel: 'days', min: 1, help: 'Warn when a certificate expires within this many days.' })),
          h('div', { class: 'stack-sm' }, notifyRecovery, notifyWarnings),
        )),
      h('section', { class: 'card' }, h('h2', null, 'Outgoing email (SMTP)'), h('p', { class: 'lead' }, 'Use an app password where your provider offers one. The password is stored locally and never shown again.'),
        h('div', { class: 'form-grid' },
          field({ label: 'SMTP host', input: host }), field({ label: 'Port', input: port }),
          field({ label: 'Security', input: security }), field({ label: 'From address', input: from }),
          field({ label: 'Username', input: user }), field({ label: 'Password', input: pass }),
        ),
        h('hr', { class: 'divider' }),
        h('div', { class: 'row', style: { alignItems: 'flex-start' } }, h('div', { style: { flex: '1', minWidth: '220px', maxWidth: '360px' } }, testTo), testBtn),
        testMsg,
        h('hr', { class: 'divider' }), saveBar()),
      h('section', { class: 'card' }, h('h2', null, 'How suppression works'),
        h('div', { class: 'stack-sm', style: { marginTop: '8px' } },
          h('p', { class: 'note' }, h('b', null, 'Dependencies: '), 'when a node "depends on" another (say Plex depends on Gateway) and the parent is down, failures on the child are recorded as "affected by Gateway" and no separate email is sent for it.'),
          h('p', { class: 'note' }, h('b', null, 'Cooldown: '), 'after an alert is sent for a check, further alerts for that same check are held back for the cooldown period. A recovery email is still sent as soon as it comes back.'),
          h('p', { class: 'note' }, h('b', null, 'Maintenance and silences: '), 'checks keep running and history is kept, but alerts are suppressed and shown as such in the incident timeline.'))));
  }

  /* ---------- Automation: endpoints + all triggers ---------- */
  async function tabAutomation() {
    const wrap = h('div', { class: 'stack' });
    let nodes = [];
    const load = async () => {
      const [eps, trs, ns] = await Promise.all([api.get('/api/endpoints').catch(() => []), api.get('/api/triggers').catch(() => []), api.get('/api/nodes').catch(() => [])]);
      if (state.destroyed) return;
      nodes = ns || [];
      render(eps || [], trs || []);
    };
    const render = (eps, trs) => {
      clear(wrap);
      // /hook/ URLs are not covered by the access password, so an endpoint
      // without a token is open to everyone who can reach this port.
      const open = (eps || []).filter((e) => !e.token);
      if (open.length) {
        wrap.append(banner('warn', h('span', null, h('b', null, open.length === 1 ? 'One custom endpoint has no token: ' : `${open.length} custom endpoints have no token: `),
          ...open.flatMap((e, i) => [i ? ', ' : '', h('code', null, `/hook/${e.slug}`)]),
          '. Anyone who can reach this computer on the network can call them.'), {
          actions: open.map((e) => h('button', { class: 'btn btn-sm admin-only', type: 'button', onclick: async () => { const saved = await openEndpointEditor(e, { nodes }); if (saved) load(); } }, icon('edit'), open.length === 1 ? 'Edit' : `Edit ${e.name}`)),
        }));
      }
      const epCard = h('section', { class: 'card' },
        h('div', { class: 'card-head' }, h('h2', null, 'Custom endpoints'),
          h('button', { class: 'btn btn-primary admin-only', type: 'button', onclick: async () => { const saved = await openEndpointEditor(null, { nodes }); if (saved) load(); } }, icon('plus'), 'New endpoint')),
        h('p', { class: 'lead' }, 'URLs other systems can call to make GWatch do something: run a node\'s checks after a reboot, run a script, call a webhook or pull a git repository. Each lives at ', h('code', null, '/hook/<name>'), '.'));
      if (!eps.length) epCard.append(emptyState({ icon: 'webhook', title: 'No endpoints yet', text: 'Create one and call its URL from a script, a router, Home Assistant, a CI job — anything that can make an HTTP request.', compact: true }));
      for (const e of eps) epCard.append(endpointRow(e, { nodes, onChange: load }));
      const trCard = h('section', { class: 'card' },
        h('div', { class: 'card-head' }, h('h2', null, 'Triggers on nodes'),
          nodes.length ? h('button', { class: 'btn admin-only', type: 'button', onclick: async () => {
            const sel = h('select', null, nodes.map((n) => h('option', { value: n.id }, n.name)));
            const ok = await confirmDialog({ title: 'New trigger', message: 'Which node should it watch?', confirmLabel: 'Continue', body: h('div', { class: 'field' }, sel) });
            if (!ok) return;
            const node = nodes.find((n) => String(n.id) === sel.value);
            const saved = await openTriggerEditor(null, { node, nodes });
            if (saved) load();
          } }, icon('plus'), 'New trigger') : null),
        h('p', { class: 'lead' }, 'Triggers run an action when something happens on a node. They are created on each node\'s page; this is the overview.'));
      if (!trs.length) trCard.append(emptyState({ icon: 'zap', title: 'No triggers yet', text: 'Open a node and add a trigger, e.g. "when Plex goes down, restart its container", "when the gateway recovers, post to Discord".', compact: true }));
      for (const t of trs) {
        const node = nodes.find((n) => n.id === t.nodeId) || { id: t.nodeId, name: `node ${t.nodeId}`, checks: [] };
        const row = triggerRow(t, { node, nodes, onChange: load });
        row.querySelector('.t-name')?.prepend(h('a', { href: `#/nodes/${t.nodeId}`, class: 'tag tag-group' }, node.name), ' ');
        trCard.append(row);
      }
      wrap.append(epCard, trCard,
        h('section', { class: 'card' }, h('h2', null, 'Notes'),
          h('div', { class: 'stack-sm', style: { marginTop: '8px' } },
            h('p', { class: 'note' }, h('b', null, 'Placeholders: '), 'URL, body, headers, git arguments and script code may contain {{node.name}}, {{status}}, {{message}}, {{latencyMs}} and more. Scripts also get GWATCH_* environment variables.'),
            h('p', { class: 'note' }, h('b', null, 'Security: '), 'actions run on the computer that runs GWatch with its permissions. Protect endpoints with a token and set an access password before enabling remote access.'),
            h('p', { class: 'note' }, h('b', null, 'History: '), 'every run is recorded in the Audit tab (event types "Trigger" and "Endpoint") together with its output.'))));
    };
    await load();
    state.panelRefresh = load;
    return wrap;
  }

  /* ---------- Retention ---------- */
  /* ---------- Hardware ---------- */
  async function tabHardware() {
    const wrap = h('section', { class: 'card' });
    let nodes = [];

    async function reload() {
      const [agents, nodeList] = await Promise.all([
        api.get('/api/agents'),
        api.get('/api/nodes').catch(() => []),
      ]);
      nodes = nodeList || [];
      replace(wrap,
        h('div', { class: 'card-head' }, h('h2', null, 'Machines'),
          h('button', { class: 'btn btn-primary btn-sm', type: 'button', onclick: () => registerAgent(reload) }, icon('plus'), 'Register a machine')),
        h('p', { class: 'lead' }, 'GWatch reads this computer by itself. To see another machine\u2019s hardware, register it here and install gwatch-agent on it — the agent only sends readings out, so registering a machine gives GWatch no way into it. Each machine gets a node of its own, and its readings are shown there.'),
        agents.length
          ? h('div', { class: 'table-wrap' }, h('table', { class: 'table' },
              h('thead', null, h('tr', null, h('th', null, 'Name'), h('th', null, 'Token'), h('th', null, 'Reporting'), h('th', null, 'Last seen'), h('th', null, 'Node'), h('th', null, ''))),
              h('tbody', null, ...agents.map((a) => agentRow(a, reload)))))
          : emptyState({ icon: 'cpu', title: 'No machines registered', text: 'Register one to watch another computer\u2019s processor, memory, disk and network.', compact: true }));
    }

    function agentRow(a, reloadFn) {
      const revoked = !!a.revokedAt;
      const state_ = revoked ? 'revoked' : a.enabled ? (a.lastSeenAt ? 'reporting' : 'waiting for first reading') : 'paused';
      return h('tr', null,
        h('td', null, h('div', null, h('b', null, a.name)),
          a.hostname ? h('div', { class: 'muted', style: { fontSize: '12px' } }, `${a.hostname}${a.os ? ` · ${a.os}/${a.arch}` : ''}`) : null),
        h('td', { class: 'mono' }, `${a.prefix}\u2026`),
        h('td', null, h('span', { class: revoked ? 'muted' : '' }, state_),
          a.lastVersion ? h('div', { class: 'muted', style: { fontSize: '12px' } }, `agent ${a.lastVersion}`) : null),
        h('td', null, a.lastSeenAt ? relTime(a.lastSeenAt) : h('span', { class: 'muted' }, 'never')),
        h('td', null, nodes.find((n) => n.id === a.nodeId)?.name || h('span', { class: 'muted' }, '—')),
        h('td', { style: { textAlign: 'right', whiteSpace: 'nowrap' } },
          a.nodeId ? h('a', { class: 'btn btn-sm', href: `#/nodes/${a.nodeId}` }, 'Open node') : null,
          revoked
            ? h('button', { class: 'btn btn-sm btn-danger', type: 'button', onclick: () => purgeAgent(a, reloadFn) }, 'Delete')
            : h('button', { class: 'btn btn-sm', type: 'button', onclick: () => revokeAgent(a, reloadFn) }, 'Revoke')));
    }

    await reload();
    state.panelRefresh = () => reload().catch(() => {});
    return h('div', { class: 'stack' }, wrap, agentExplainerCard());
  }

  async function registerAgent(reload) {
    const name = textInput({ autocomplete: 'off', placeholder: 'e.g. Living room NAS' });
    const nodes = await api.get('/api/nodes').catch(() => []);
    const nodeSel = selectInput({
      options: [{ value: '', label: 'Not attached to a node' }, ...nodes.map((n) => ({ value: String(n.id), label: n.name }))],
      value: '',
    });
    const ok = await confirmDialog({
      title: 'Register a machine',
      body: h('div', { class: 'stack' },
        field({ label: 'Name', input: name, help: 'How this machine appears under Hardware.' }),
        field({ label: 'Node', input: nodeSel, help: 'Optional. Attaching it lets a hardware check on that node watch this machine.' })),
      confirmLabel: 'Register and get a token',
    });
    if (!ok) return;
    try {
      const res = await api.post('/api/agents', {
        name: name.value.trim(),
        nodeId: nodeSel.value ? Number(nodeSel.value) : null,
      });
      await reload();
      showAgentToken(res.token);
    } catch (e) { toast(e.message, { kind: 'error' }); }
  }

  // The token is shown here and nowhere else, so the install command is shown
  // with it: copying one line is the whole setup on the other machine.
  function showAgentToken(token) {
    const base = `${location.protocol}//${location.host}`;
    const command = `gwatch-agent install --server ${base} --token ${token}`;
    const copy = h('button', { class: 'btn btn-primary', type: 'button', onclick: async () => {
      try { await navigator.clipboard.writeText(command); toast('Install command copied', { kind: 'success' }); }
      catch { toast('Could not copy — select the command and copy it by hand.', { kind: 'error' }); }
    } }, icon('copy'), 'Copy install command');
    openModal({
      title: 'Install the agent on that machine',
      wide: true,
      body: h('div', { class: 'stack' },
        h('p', { class: 'lead' }, 'Run this on the machine you want to watch. GWatch keeps only a fingerprint of the token, so it cannot be shown again — if you lose it, revoke this machine and register it afresh.'),
        h('code', { class: 'agent-setup' }, command),
        h('p', { class: 'note' }, 'The token is good for exactly one thing: submitting that machine\u2019s hardware readings. It cannot read or change anything in GWatch, and GWatch never connects back to the machine.'),
        h('p', { class: 'note' }, 'Without ', h('code', null, 'install'), ' the agent runs in the foreground, which is the quickest way to see that it works. ',
          h('code', null, 'gwatch-agent once --server … --token …'), ' sends a single reading and exits.'),
        h('p', { class: 'note' }, 'If this GWatch is reached over HTTPS with a self-signed certificate, add ', h('code', null, '--insecure'), ' — the agent still uses TLS, it just stops checking the certificate.')),
      footer: copy,
    });
  }

  async function revokeAgent(a, reload) {
    const ok = await confirmDialog({
      title: `Revoke ${a.name}?`,
      message: 'The agent on that machine stops being accepted immediately. Its past readings are kept, and you can delete them separately afterwards.',
      confirmLabel: 'Revoke token', danger: true,
    });
    if (!ok) return;
    try { await api.del(`/api/agents/${a.id}`); toast('Machine revoked', { kind: 'success' }); await reload(); }
    catch (e) { toast(e.message, { kind: 'error' }); }
  }

  async function purgeAgent(a, reload) {
    const ok = await confirmDialog({
      title: `Delete ${a.name} and its readings?`,
      message: 'Every hardware reading this machine sent is deleted with it. This cannot be undone.',
      confirmLabel: 'Delete machine and readings', danger: true,
    });
    if (!ok) return;
    try { await api.del(`/api/agents/${a.id}?purge=1`); toast('Machine deleted', { kind: 'success' }); await reload(); }
    catch (e) { toast(e.message, { kind: 'error' }); }
  }

  function agentExplainerCard() {
    const item = (t, d) => h('div', null, h('b', null, t), h('div', { class: 'muted', style: { fontSize: '13px' } }, d));
    return h('section', { class: 'card' }, h('h2', null, 'How another machine reports in'),
      h('div', { class: 'stack-sm', style: { marginTop: '8px' } },
        item('The agent connects to GWatch, never the other way round', 'It posts a reading every minute and hangs up. Nothing needs to be opened on that machine, and GWatch holds no credential for it.'),
        item('A token can do one thing', 'Submit that one machine\u2019s readings. It cannot read your nodes, your settings or anyone else\u2019s readings, and it is not an API key.'),
        item('Losing a token is small', 'Someone holding it could send false readings for that machine. Revoke it here and it stops working on the next request.'),
        item('If you would rather GWatch did the asking', 'Run the agent with `serve` instead, and add a hardware check with its source set to a metrics URL. That opens a port on the machine, which is why pushing is the default.')),
      h('p', { class: 'note' }, 'Full details, including how to build the agent for another platform, are in ', h('code', null, 'docs/HARDWARE.md'), '.'));
  }

  /* ---------- AI & MCP ---------- */
  // The MCP companion (gwatch-mcp) is a separate program that talks to this
  // GWatch with an API key. Nothing here changes a setting; the tab is the
  // set-up guide, the trust model in plain words, and the agent skill.
  async function tabMcp() {
    const status = await api.get('/api/mcp/status').catch(() => null);
    return h('div', { class: 'stack' }, mcpIntroCard(), mcpSetupCard(), mcpScopeCard(), mcpSkillCard(status));
  }

  // A code block with a copy button, for the pieces of setup that are meant
  // to be pasted somewhere else rather than typed.
  function copyBlock(text, what) {
    const copy = h('button', { class: 'btn btn-sm', type: 'button', onclick: async () => {
      try { await navigator.clipboard.writeText(text); toast(`${what} copied`, { kind: 'success' }); }
      catch { toast('Could not copy — select the text and copy it by hand.', { kind: 'error' }); }
    } }, icon('copy'), 'Copy');
    return h('div', { class: 'mcp-code' }, h('code', { class: 'agent-setup' }, text), copy);
  }

  function mcpIntroCard() {
    const item = (t, d) => h('div', null, h('b', null, t), h('div', { class: 'muted', style: { fontSize: '13px' } }, d));
    return h('section', { class: 'card' }, h('h2', null, 'AI assistants and MCP'),
      h('p', { class: 'lead' }, 'gwatch-mcp is a small companion program that lets an AI assistant — Claude Desktop, Claude Code or any other MCP client — ask this GWatch what is up, what is down and since when. It is optional, separate from GWatch itself, and off until you set it up.'),
      h('div', { class: 'stack-sm', style: { marginTop: '8px' } },
        item('It runs where the assistant runs', 'Usually your own computer. It talks to GWatch over the same API a browser uses, with an API key you create here. GWatch never connects out to the assistant.'),
        item('Read-only unless you say otherwise', 'A read-only key lets the assistant answer questions. Changing what is monitored takes a read & write key and a flag on gwatch-mcp — two separate decisions, both yours.'),
        item('Some things are never on offer', 'No key of any scope can reach settings, backups, accounts, other keys, automation or updates, so neither can an assistant.')),
      h('p', { class: 'note' }, 'The full manual, including the trust model in detail, is ',
        h('a', { href: `${REPO_URL}/blob/main/mcp/README.md`, target: '_blank', rel: 'noopener' }, 'mcp/README.md on GitHub', icon('external')), '.'));
  }

  function mcpSetupCard() {
    const origin = `${location.protocol}//${location.host}`;
    const desktopConfig = JSON.stringify({ mcpServers: { gwatch: { command: 'gwatch-mcp', env: { GWATCH_URL: origin, GWATCH_API_KEY: 'gw_paste_your_read_only_key_here' } } } }, null, 2);
    const claudeCode = `claude mcp add gwatch gwatch-mcp -e GWATCH_URL=${origin} -e GWATCH_API_KEY=gw_paste_your_read_only_key_here`;
    const check = `gwatch-mcp check --url ${origin} --api-key gw_…`;
    const step = (title, ...body) => h('li', null, h('b', null, title), ...body);
    // Minting a key from here reuses the Users & access dialog; the list on
    // that tab is what refreshes, so there is nothing to reload on this one.
    const newKey = h('button', { class: 'btn btn-sm', type: 'button', onclick: () => createKey(async () => {}) }, icon('key'), 'Create a read-only key');
    return h('section', { class: 'card' }, h('h2', null, 'Set it up'),
      h('ol', { class: 'mcp-steps' },
        step('Create a read-only API key. ',
          h('p', null, 'Under ', h('a', { href: '#/settings/users' }, 'Users & access'), ' › API keys, or with the button below. Name it after the assistant ("Claude Desktop"), keep the scope read-only, and copy the key when it is shown — it is shown once.'),
          h('div', null, newKey)),
        step('Install gwatch-mcp on the computer the assistant runs on. ',
          h('p', null, 'Every GWatch release includes a gwatch-mcp binary for each platform; put it somewhere on your PATH. Or, with Go installed:'),
          copyBlock('go install github.com/jxburros/GWatch/mcp/cmd/gwatch-mcp@latest', 'Install command')),
        step('Tell the assistant about it. ',
          h('p', null, 'Claude Desktop reads ', h('code', null, 'claude_desktop_config.json'), ' (macOS: ', h('code', null, '~/Library/Application Support/Claude/'), ', Windows: ', h('code', null, '%APPDATA%\\Claude\\'), '). Add this, with your key in place of the placeholder:'),
          copyBlock(desktopConfig, 'Claude Desktop config'),
          h('p', null, 'Claude Code takes one command instead:'),
          copyBlock(claudeCode, 'Claude Code command'),
          h('p', { class: 'note' }, 'Any other MCP client: run gwatch-mcp as a stdio server with the same two environment variables. Keep the key in the environment rather than on the command line, so it stays out of the process list. If the assistant is not on this network, use the address it reaches GWatch by; docs/REMOTE-ACCESS.md covers doing that safely.')),
        step('Check it. ',
          h('p', null, 'From that same computer, this reports the connection, the key’s scope and which tools the assistant will see:'),
          copyBlock(check, 'Check command'))),
      h('p', { class: 'note' }, 'The address above is the one this browser is using to reach GWatch. The assistant needs one that works from where it runs.'));
  }

  function mcpScopeCard() {
    const item = (t, d) => h('div', null, h('b', null, t), h('div', { class: 'muted', style: { fontSize: '13px' } }, d));
    return h('section', { class: 'card' }, h('h2', null, 'What the assistant can and cannot do'),
      h('div', { class: 'stack-sm', style: { marginTop: '8px' } },
        item('With a read-only key', 'The overview, nodes and their checks, recent results, history over a range, the event timeline, groups and tags, the node templates, and whether the monitor itself is healthy. Nine tools; nothing that changes anything.'),
        item('With a read & write key and --allow-write', 'Also create, change, enable, disable, run and silence nodes, add notes to the timeline, test a check without saving it, and delete a node — which additionally has to be confirmed in the tool call. Both gates have to be open: GWatch refuses a write from a read-only key whatever gwatch-mcp was started with.'),
        item('Never, whatever the key', 'Settings, backups and restores, updates, user accounts, API keys, automation triggers and endpoints, the configuration export and the service log. There are no tools for these, and a key could not use them if there were.')),
      h('p', { class: 'note' }, 'Every change an assistant makes is recorded in the event timeline under the key’s name, the same as any other API key.'));
  }

  // The skill is a short document that teaches an assistant how to use the
  // tools well. It has its own version; the card says when it was last
  // downloaded and, only when the skill has moved on since, mentions it in an
  // inline note. Nothing outside this card announces it.
  function mcpSkillCard(status) {
    const version = status?.skillVersion;
    const dl = h('a', { class: 'btn btn-primary', href: '/api/mcp/skill', download: version ? `gwatch-skill-${version}.zip` : 'gwatch-skill.zip', onclick: () => {
      // The download itself is what the server records; this only keeps the
      // "last downloaded" line honest without a reload.
      setTimeout(() => { state.panelRefresh?.(); }, 800);
    } }, icon('download'), version ? `Download skill ${version}` : 'Download skill');
    const md = h('a', { class: 'btn btn-sm', href: '/api/mcp/skill?format=md', download: 'SKILL.md' }, 'SKILL.md only');
    const last = status?.lastDownloadedAt
      ? h('p', { class: 'muted', style: { fontSize: '13px' } }, `Last downloaded ${relTime(status.lastDownloadedAt)} (version ${status.lastDownloadedVersion})${status.lastDownloadedBy ? ` by ${status.lastDownloadedBy}` : ''}.`)
      : h('p', { class: 'muted', style: { fontSize: '13px' } }, 'Not downloaded yet.');
    return h('section', { class: 'card' },
      h('div', { class: 'card-head' }, h('h2', null, 'Agent skill'), version ? h('span', { class: 'chip' }, `Version ${version}`) : null),
      h('p', { class: 'lead' }, 'A short guide that teaches the assistant how to use GWatch well: start with the overview, how to drill into a problem, what the statuses mean, and how to be careful with the write tools. The tools describe themselves; the skill teaches judgement.'),
      status?.updateAvailable ? banner('info', `The skill has been updated since it was last downloaded (${status.lastDownloadedVersion} → ${status.skillVersion}). Download it again when convenient and replace the copy the assistant uses.`) : null,
      status ? h('div', { class: 'btn-group' }, dl, md) : h('p', { class: 'note' }, 'This build does not include the skill.'),
      last,
      h('p', { class: 'note' }, 'Where to put it: for Claude Code, unzip it into ', h('code', null, '~/.claude/skills/'), ' (or a project’s ', h('code', null, '.claude/skills/'), ') so the file lands at ', h('code', null, 'skills/gwatch/SKILL.md'), '. For Claude Desktop and assistants without a skills folder, paste the body of SKILL.md into the assistant’s instructions.'));
  }

  async function tabRetention() {
    const s = state.settings || await loadSettings();
    const r = s.retention;
    const summary = h('div', { class: 'retention-summary', 'aria-live': 'polite' });
    const update = () => {
      const raw = retentionSpan(r.rawDays), five = retentionSpan(r.fiveMinDays), hourly = retentionSpan(r.hourlyDays), daily = retentionSpan(r.dailyDays), ev = retentionSpan(r.eventDays);
      replace(summary,
        'Every result is kept for ', h('b', null, raw), ', then ', h('b', null, '5-minute summaries'), ' until ', h('b', null, five === 'forever' ? 'forever' : `${five} old`), ', ',
        h('b', null, 'hourly'), ' until ', h('b', null, hourly === 'forever' ? 'forever' : `${hourly} old`), ', and ', h('b', null, 'daily'), ' ', daily === 'forever' ? h('b', null, 'forever') : h('span', null, 'until ', h('b', null, `${daily} old`), ', then deleted'), '. ',
        'Events are kept for ', h('b', null, ev), '.');
    };
    const f = (key, label, help) => { const el = numField(r, key, label, { unitLabel: 'days', help }); el.querySelector('input').addEventListener('input', update); return el; };
    update();
    const statusBox = h('div', null, skeleton({ lines: 3 }));
    const runBtn = h('button', { class: 'btn', type: 'button', onclick: async () => { const done = busy(runBtn, 'Running…'); try { const st = await api.post('/api/retention/run'); renderStatus(st); toast('Retention run finished', { kind: 'success' }); } catch (e) { toast(e.message, { kind: 'error' }); } done(); } }, icon('database'), 'Run retention now');
    const renderStatus = (st) => {
      replace(statusBox,
        h('div', { class: 'health-cards' },
          hcard(num(st.rawRows), 'Raw results', st.oldestRaw ? `oldest ${relTime(st.oldestRaw)}` : ''),
          hcard(num(st.rollupRows5m), '5-minute rollups'), hcard(num(st.rollupRows1h), 'Hourly rollups'), hcard(num(st.rollupRows1d), 'Daily rollups'), hcard(num(st.eventRows), 'Events'),
          hcard(st.lastRunAt ? relTime(st.lastRunAt) : 'never', 'Last run', st.lastRunAt ? `${duration(st.lastDurationMs / 1000)} · removed ${num(st.deletedLastRun)} rows` : '')),
        st.lastError ? h('div', { style: { marginTop: '10px' } }, banner('down', `Last run failed: ${st.lastError}`)) : null,
        st.plan?.length ? h('ul', { class: 'note', style: { marginTop: '12px', paddingLeft: '18px' } }, st.plan.map((p) => h('li', null, p))) : null);
    };
    api.get('/api/retention/status').then(renderStatus).catch((e) => replace(statusBox, h('p', { class: 'note' }, e.message)));
    const pathsBox = h('div', null, skeleton({ lines: 2 }));
    loadDataPaths(pathsBox);
    state.panelRefresh = () => api.get('/api/retention/status').then(renderStatus).catch(() => {});
    return h('form', { class: 'stack', onsubmit: (e) => { e.preventDefault(); saveSettings(); } },
      h('section', { class: 'card' }, h('h2', null, 'History retention'), h('p', { class: 'lead' }, 'Recent data stays detailed; older data is summarised so the database never grows without limit. 0 = keep forever.'),
        summary,
        h('div', { class: 'form-grid-3', style: { marginTop: '14px' } },
          f('rawDays', 'Keep every result for'), f('fiveMinDays', 'Keep 5-minute summaries for'), f('hourlyDays', 'Keep hourly summaries for'), f('dailyDays', 'Keep daily summaries for'), f('eventDays', 'Keep events for'),
          f('hostDays', 'Keep hardware readings for', 'Each reading is a whole snapshot of a machine rather than a single number, so these are kept for less time than check results.')),
        h('hr', { class: 'divider' }), saveBar()),
      h('section', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, 'Current storage'), runBtn), statusBox, h('hr', { class: 'divider' }), pathsBox));
  }

  function hcard(value, label, sub) { return h('div', { class: 'health-card' }, h('div', { class: 'hv' }, value), h('div', { class: 'hl' }, label), sub ? h('div', { class: 'hs' }, sub) : null); }

  // dataPathsBlock shows where GWatch's files actually live, in a read-only
  // monospace block. Used on both the Retention and Backups tabs, since that
  // is where people go looking for "where is my data".
  function dataPathsBlock(hl) {
    const dl = h('dl', { class: 'kv' });
    const row = (k, v) => { if (v) dl.append(h('dt', null, k), h('dd', { class: 'mono' }, v)); };
    row('Data directory', hl.dataDir);
    row('Database', hl.databasePath);
    row('SQLite driver', hl.databaseDriver);
    row('Key file', hl.keyPath);
    row('Backups folder', hl.backupDir);
    return h('div', { style: { marginTop: '10px' } }, dl,
      h('p', { class: 'note', style: { marginTop: '8px' } },
        'These are ordinary files on this computer’s own disk, in a permanent folder — never a temp directory the OS can clear on reboot or under disk pressure.'));
  }
  function loadDataPaths(box) {
    api.get('/api/health').then((hl) => replace(box, dataPathsBlock(hl))).catch(() => clear(box));
  }

  /* ---------- Maintenance ---------- */
  async function tabMaintenance() {
    const wrap = h('section', { class: 'card' });
    let nodes = [];
    let groups = [];
    const load = async () => {
      const [list, ns, gs] = await Promise.all([api.get('/api/maintenance'), api.get('/api/nodes').catch(() => []), api.get('/api/groups').catch(() => ({ groups: [] }))]);
      nodes = ns || []; groups = gs?.groups || [];
      render(list || []);
    };
    const scopeLabel = (w) => w.nodeId ? (nodes.find((n) => n.id === w.nodeId)?.name || `node ${w.nodeId}`) : w.group ? `group ${w.group}` : 'all nodes';
    const whenLabel = (w) => w.weekdays?.length ? `Weekly on ${w.weekdays.map(weekdayShort).join(', ')} at ${timeShort(w.startAt)} for ${duration((w.durationMinutes || 60) * 60)}` : `${dateTime(w.startAt, { seconds: false })} → ${dateTime(w.endAt, { seconds: false })}`;
    const render = (list) => {
      clear(wrap);
      wrap.append(h('div', { class: 'card-head' }, h('h2', null, 'Maintenance windows'), h('button', { class: 'btn btn-primary', type: 'button', onclick: () => edit(null) }, icon('plus'), 'New window')),
        h('p', { class: 'lead' }, 'Planned reboots and updates should not page you. Checks keep running; alerts are held and the node is marked as in maintenance.'));
      if (!list.length) { wrap.append(emptyState({ icon: 'wrench', title: 'No maintenance windows', text: 'Create one for a planned reboot, or a weekly one for your update schedule.', compact: true })); return; }
      const rows = h('div', null);
      for (const w of list) {
        rows.append(h('div', { class: 'maint-row' },
          h('div', null,
            h('div', { class: 'm-title' }, w.name, w.active ? h('span', { class: 'pill status-maintenance' }, icon('wrench'), 'Active now') : null, w.enabled === false ? h('span', { class: 'tag' }, 'disabled') : null),
            h('div', { class: 'm-sub' }, `${scopeLabel(w)} · ${whenLabel(w)}`), w.notes ? h('div', { class: 'm-sub' }, w.notes) : null),
          h('div', { class: 'btn-group' }, h('button', { class: 'btn btn-sm', type: 'button', onclick: () => edit(w) }, icon('edit'), 'Edit'), h('button', { class: 'btn btn-sm btn-danger', type: 'button', onclick: async () => { if (await confirmDialog({ title: `Delete "${w.name}"?`, confirmLabel: 'Delete', danger: true })) { try { await api.del(`/api/maintenance/${w.id}`); toast('Window deleted', { kind: 'success' }); load(); } catch (e) { toast(e.message, { kind: 'error' }); } } } }, icon('trash'), 'Delete'))));
      }
      wrap.append(rows);
    };
    const edit = (existing) => {
      const w = existing ? { ...existing, weekdays: [...(existing.weekdays || [])] } : { name: '', nodeId: null, group: '', enabled: true, startAt: new Date(Date.now() + 5 * 60e3).toISOString(), endAt: new Date(Date.now() + 65 * 60e3).toISOString(), weekdays: [], durationMinutes: 60, notes: '' };
      const name = textInput({ value: w.name, placeholder: 'e.g. Sunday updates, NAS reboot' });
      const scope = selectInput({ options: [{ value: 'all', label: 'All nodes' }, { value: 'group', label: 'A group' }, { value: 'node', label: 'One node' }], value: w.nodeId ? 'node' : w.group ? 'group' : 'all' });
      const groupSel = selectInput({ options: groups.map((g) => ({ value: g.name, label: g.name })), value: w.group || groups[0]?.name || '' });
      const nodeSel = selectInput({ options: nodes.map((n) => ({ value: n.id, label: n.name })), value: w.nodeId ?? nodes[0]?.id ?? '' });
      const groupField = field({ label: 'Group', input: groupSel }); const nodeField = field({ label: 'Node', input: nodeSel });
      const syncScope = () => { groupField.hidden = scope.value !== 'group'; nodeField.hidden = scope.value !== 'node'; };
      scope.addEventListener('change', syncScope); syncScope();
      const kind = selectInput({ options: [{ value: 'once', label: 'One-off (start and end)' }, { value: 'weekly', label: 'Weekly recurring' }], value: w.weekdays?.length ? 'weekly' : 'once' });
      const start = h('input', { type: 'datetime-local', value: toLocalInput(w.startAt) });
      const end = h('input', { type: 'datetime-local', value: toLocalInput(w.endAt) });
      const startTime = h('input', { type: 'time', value: toLocalInput(w.startAt).slice(11, 16) || '03:00' });
      const durationIn = numberInput({ value: w.durationMinutes || 60, min: 5 });
      const days = h('div', { class: 'weekdays', role: 'group', 'aria-label': 'Weekdays' });
      [1, 2, 3, 4, 5, 6, 0].forEach((dIdx) => days.append(h('label', null, h('input', { type: 'checkbox', value: dIdx, checked: w.weekdays.includes(dIdx) }), weekdayShort(dIdx))));
      const onceFields = h('div', { class: 'form-grid' }, field({ label: 'Starts', input: start }), field({ label: 'Ends', input: end }));
      const weeklyFields = h('div', { class: 'stack-sm' }, field({ label: 'Days', input: days }), h('div', { class: 'form-grid' }, field({ label: 'Start time', input: startTime }), field({ label: 'Duration', input: unit(durationIn, 'minutes') })));
      const syncKind = () => { onceFields.hidden = kind.value !== 'once'; weeklyFields.hidden = kind.value !== 'weekly'; };
      kind.addEventListener('change', syncKind); syncKind();
      const notes = textarea({ value: w.notes || '', rows: 2, placeholder: 'Optional' });
      const enabledT = toggle({ label: 'Enabled', checked: w.enabled !== false });
      const form = h('form', { class: 'stack', onsubmit: (e) => { e.preventDefault(); submit(); } },
        field({ label: 'Name', input: name }),
        h('div', { class: 'form-grid' }, field({ label: 'Applies to', input: scope }), groupField, nodeField, field({ label: 'Schedule', input: kind })),
        onceFields, weeklyFields, field({ label: 'Notes', input: notes }), enabledT);
      const m = openModal({ title: existing ? 'Edit maintenance window' : 'New maintenance window', wide: true, body: form, footer: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, 'Cancel'), h('button', { class: 'btn btn-primary', type: 'button', onclick: () => submit() }, existing ? 'Save' : 'Create')] });
      async function submit() {
        if (!name.value.trim()) { name.focus(); toast('Give the window a name', { kind: 'error' }); return; }
        const payload = { ...w, name: name.value.trim(), nodeId: scope.value === 'node' ? Number(nodeSel.value) : null, group: scope.value === 'group' ? groupSel.value : '', notes: notes.value, enabled: enabledT.input.checked };
        if (kind.value === 'once') {
          payload.weekdays = []; payload.durationMinutes = 0;
          payload.startAt = fromLocalInput(start.value); payload.endAt = fromLocalInput(end.value);
          if (!payload.startAt || !payload.endAt || new Date(payload.endAt) <= new Date(payload.startAt)) { toast('The end must be after the start', { kind: 'error' }); return; }
        } else {
          payload.weekdays = [...days.querySelectorAll('input:checked')].map((i) => Number(i.value));
          if (!payload.weekdays.length) { toast('Pick at least one weekday', { kind: 'error' }); return; }
          const [hh, mm] = (startTime.value || '03:00').split(':').map(Number);
          const sd = new Date(); sd.setHours(hh, mm, 0, 0);
          payload.startAt = sd.toISOString(); payload.durationMinutes = Number(durationIn.value) || 60;
          payload.endAt = new Date(sd.getTime() + payload.durationMinutes * 60e3).toISOString();
        }
        delete payload.active;
        try {
          if (existing) await api.put(`/api/maintenance/${existing.id}`, payload); else await api.post('/api/maintenance', payload);
          m.close(); toast(existing ? 'Window saved' : 'Window created', { kind: 'success' }); load();
        } catch (e) { toast(e.message, { kind: 'error' }); }
      }
      setTimeout(() => name.focus(), 50);
    };
    await load();
    state.panelRefresh = load;
    return wrap;
  }

  /* ---------- Backups ---------- */
  async function tabBackups() {
    const s = state.settings || await loadSettings();
    s.backups = s.backups || { enabled: false, intervalHours: 24, keep: 7, includeHistory: true, password: '' };
    const bk = s.backups;
    const listCard = h('section', { class: 'card' });
    const statusLine = h('div');
    const nextLine = h('div');
    const load = async () => {
      const data = await api.get('/api/backups');
      render(data || { backups: [], status: {} });
    };
    const render = (data) => {
      clear(listCard); clear(statusLine); clear(nextLine);
      const st = data.status || {};
      if (st.lastBackupAt) statusLine.append(banner(st.lastBackupOk ? 'up' : 'down', h('span', null, h('b', null, st.lastBackupOk ? 'Last backup succeeded ' : 'Last backup failed '), `${relTime(st.lastBackupAt)}${st.lastBackupFile ? ' · ' + st.lastBackupFile : ''}${st.lastError ? ' · ' + st.lastError : ''}`, st.lastRestoreAt ? ` · last restore ${relTime(st.lastRestoreAt)}` : '')));
      else statusLine.append(banner('info', 'No backup has been made yet. Create one so you can move to a new computer without re-creating every node.'));
      if (bk.enabled) nextLine.append(banner('info', data.nextScheduledAt ? h('span', null, h('b', null, 'Next scheduled backup: '), relTime(data.nextScheduledAt)) : 'Automatic backups are enabled; the next one runs once saved.'));
      listCard.append(h('div', { class: 'card-head' }, h('h2', null, 'Backups on this computer')),
        h('p', { class: 'lead' }, data.dir ? h('span', { class: 'mono' }, data.dir) : 'Encrypted archives stored locally.'));
      if (!data.backups?.length) { listCard.append(emptyState({ icon: 'save', title: 'No backups yet', compact: true })); return; }
      for (const b of data.backups) {
        listCard.append(h('div', { class: 'backup-row' },
          h('div', null, h('div', { class: 'b-name' }, b.fileName), h('div', { class: 'b-sub' }, `${dateTime(b.createdAt, { seconds: false })} · ${bytes(b.sizeBytes)} · ${b.includeHistory ? 'configuration + history' : 'configuration only'}${b.encrypted ? ' · encrypted' : ''}`)),
          h('div', { class: 'btn-group' },
            h('a', { class: 'btn btn-sm', href: `/api/backups/${encodeURIComponent(b.fileName)}/download`, download: b.fileName }, icon('download'), 'Download'),
            h('button', { class: 'btn btn-sm', type: 'button', onclick: () => restoreExisting(b) }, icon('upload'), 'Restore'),
            h('button', { class: 'btn btn-sm btn-danger', type: 'button', onclick: async () => { if (await confirmDialog({ title: `Delete ${b.fileName}?`, confirmLabel: 'Delete', danger: true })) { try { await api.del(`/api/backups/${encodeURIComponent(b.fileName)}`); toast('Backup deleted', { kind: 'success' }); load(); } catch (e) { toast(e.message, { kind: 'error' }); } } } }, icon('trash')))));
      }
    };
    const pw = h('input', { type: 'password', autocomplete: 'new-password', placeholder: 'Choose a password' });
    const pw2 = h('input', { type: 'password', autocomplete: 'new-password', placeholder: 'Repeat the password' });
    const inclHist = checkbox({ label: 'Include performance history and events (bigger archive)', checked: true });
    const createResult = h('div', { role: 'status' });
    const createBtn = h('button', { class: 'btn btn-primary', type: 'button', onclick: async () => {
      if (pw.value.length < 4) { toast('Use a password of at least 4 characters', { kind: 'error' }); pw.focus(); return; }
      if (pw.value !== pw2.value) { toast('The passwords do not match', { kind: 'error' }); pw2.focus(); return; }
      const done = busy(createBtn, 'Creating…');
      try { const b = await api.post('/api/backups', { password: pw.value, includeHistory: inclHist.input.checked }); replace(createResult, banner('up', `Backup created: ${b.fileName} (${bytes(b.sizeBytes)})`)); pw.value = ''; pw2.value = ''; load(); }
      catch (e) { replace(createResult, banner('down', `Backup failed: ${e.message}`)); }
      done();
    } }, icon('save'), 'Create backup');
    const createCard = h('section', { class: 'card' }, h('h2', null, 'Create a backup'), h('p', { class: 'lead' }, 'The archive is encrypted with the password you choose and includes nodes, dashboards, saved charts, triggers and endpoints. Keep the password somewhere safe.'),
      h('div', { class: 'form-grid' }, field({ label: 'Password', input: pw }), field({ label: 'Confirm password', input: pw2 }), h('div', { class: 'span-2' }, inclHist)),
      h('div', { class: 'form-actions', style: { marginTop: '12px' } }, createBtn), createResult);
    const file = h('input', { type: 'file', accept: '.gwbackup,.zip,.bin,*/*' });
    const rpw = h('input', { type: 'password', autocomplete: 'off', placeholder: 'Backup password' });
    const rHist = checkbox({ label: 'Also restore history and events', checked: true });
    const restoreResult = h('div', { role: 'status' });
    const restoreBtn = h('button', { class: 'btn btn-danger', type: 'button', onclick: async () => {
      if (!file.files?.[0]) { toast('Choose a backup file first', { kind: 'error' }); return; }
      if (!rpw.value) { toast('Enter the backup password', { kind: 'error' }); rpw.focus(); return; }
      const ok = await confirmDialog({ title: 'Restore from file?', message: 'This replaces the current configuration (nodes, checks, dashboards, maintenance windows, automation and settings) with the contents of the backup. History is replaced too if you chose to include it.', confirmLabel: 'Restore', danger: true });
      if (!ok) return;
      const fd = new FormData(); fd.append('file', file.files[0]); fd.append('password', rpw.value); fd.append('includeHistory', rHist.input.checked ? 'true' : 'false');
      const done = busy(restoreBtn, 'Restoring…');
      try { const r = await api.upload('/api/backups/restore', fd); replace(restoreResult, banner('up', `Restored ${plural(r.nodes ?? 0, 'node')}, ${plural(r.checks ?? 0, 'check')} and ${num(r.results ?? 0)} results.`)); toast('Restore complete', { kind: 'success' }); load(); }
      catch (e) { replace(restoreResult, banner('down', `Restore failed: ${e.message}`)); }
      done();
    } }, icon('upload'), 'Restore from file');
    const restoreCard = h('section', { class: 'card' }, h('h2', null, 'Restore from a file'), h('p', { class: 'lead' }, 'Moving to a new computer? Install GWatch, then restore the archive you downloaded from the old one.'),
      h('div', { class: 'form-grid' }, field({ label: 'Backup file', input: file }), field({ label: 'Password', input: rpw }), h('div', { class: 'span-2' }, rHist)),
      h('div', { class: 'form-actions', style: { marginTop: '12px' } }, restoreBtn), restoreResult);
    async function restoreExisting(b) {
      const pwIn = h('input', { type: 'password', autocomplete: 'off', placeholder: 'Backup password' });
      const hist = checkbox({ label: 'Also restore history and events', checked: b.includeHistory, disabled: !b.includeHistory });
      const ok = await confirmDialog({ title: `Restore ${b.fileName}?`, confirmLabel: 'Restore', danger: true, body: h('div', { class: 'stack-sm' }, banner('warn', 'This replaces the current configuration. Nodes, checks, dashboards, maintenance windows, automation and settings will be overwritten.'), field({ label: 'Password', input: pwIn }), hist) });
      if (!ok) return;
      if (!pwIn.value) { toast('The backup password is required', { kind: 'error' }); return; }
      try { const r = await api.post('/api/backups/restore-existing', { fileName: b.fileName, password: pwIn.value, includeHistory: hist.input.checked }); toast(`Restored ${plural(r.nodes ?? 0, 'node')} and ${plural(r.checks ?? 0, 'check')}`, { kind: 'success' }); load(); }
      catch (e) { toast(`Restore failed: ${e.message}`, { kind: 'error' }); }
    }
    const autoEnabled = toggle({ label: 'Create backups automatically', checked: !!bk.enabled, onChange: (v) => { bk.enabled = v; } });
    const autoInterval = numField(bk, 'intervalHours', 'Every', { unitLabel: 'hours', min: 1 });
    const autoKeep = numField(bk, 'keep', 'Keep the newest', { unitLabel: 'archives', min: 1 });
    const autoHist = checkbox({ label: 'Include performance history and events (bigger archives)', checked: !!bk.includeHistory, onChange: (v) => { bk.includeHistory = v; } });
    const autoPw = h('input', { type: 'password', value: bk.password || '', autocomplete: 'new-password', placeholder: bk.password ? '' : 'Required to enable automatic backups', oninput: () => { bk.password = autoPw.value; } });
    const autoPw2 = h('input', { type: 'password', autocomplete: 'new-password', placeholder: 'Repeat the password (leave blank to keep the saved one)' });
    const autoSaveBtn = h('button', { class: 'btn btn-primary', type: 'button', onclick: async () => {
      if (autoPw2.value && autoPw.value !== autoPw2.value) { toast('The passwords do not match', { kind: 'error' }); autoPw2.focus(); return; }
      if (bk.enabled && !bk.password) { toast('A password is required to enable automatic backups', { kind: 'error' }); autoPw.focus(); return; }
      const done = busy(autoSaveBtn, 'Saving…');
      await saveSettings();
      done();
      load();
    } }, icon('save'), 'Save automatic backup settings');
    const pathsBox = h('div', null, skeleton({ lines: 2 }));
    loadDataPaths(pathsBox);
    const autoCard = h('section', { class: 'card' }, h('h2', null, 'Automatic backups'), h('p', { class: 'lead' }, 'Runs unattended in the background on the schedule below, using the same encrypted format as a manual backup. Archives older than the number to keep are deleted automatically.'),
      autoEnabled,
      h('div', { class: 'form-grid', style: { marginTop: '12px' } },
        autoInterval, autoKeep, h('div', { class: 'span-2' }, autoHist),
        field({ label: 'Password', input: autoPw, help: bk.password ? 'A password is already saved; leave blank to keep it.' : 'Backups are always encrypted, so a password is required to enable this.' }),
        field({ label: 'Confirm password', input: autoPw2 })),
      h('div', { class: 'form-actions', style: { marginTop: '12px' } }, autoSaveBtn), nextLine,
      h('hr', { class: 'divider' }), h('p', { class: 'lead' }, 'Where archives land, alongside the database itself:'), pathsBox);
    await load();
    state.panelRefresh = load;
    return h('div', { class: 'stack' }, statusLine, createCard, autoCard, listCard, restoreCard);
  }

  /* ---------- Updates ---------- */
  async function tabUpdates() {
    const s = state.settings || await loadSettings();
    const g = s.general;
    const u = s.updates || (s.updates = { checkAutomatically: true, checkIntervalHours: 24, includePrerelease: false, promptOnOpen: true });
    const box = h('div', { class: 'update-box' });
    const listBox = h('div', { class: 'stack-sm' });
    let releases = null;      // null until loaded; [] when the fetch failed
    let showPre = !!u.includePrerelease;
    const repo = textInput({ value: g.updateRepo || 'jxburros/GWatch', class: 'mono', placeholder: 'owner/repository', oninput: () => { g.updateRepo = repo.value; } });

    const install = async (btn, version, label) => {
      const what = version ? `GWatch ${version}` : 'the update';
      const ok = await confirmDialog({
        title: `Install ${what} now?`,
        message: `GWatch downloads the release executable, checks its signature, replaces the current one (the previous version is kept as .old) and restarts. Monitoring pauses for a few seconds.`,
        confirmLabel: 'Install and restart', danger: true,
      });
      if (!ok) return;
      const done = busy(btn, label || 'Installing…');
      try {
        const r = await api.post('/api/update/apply', version ? { version } : {});
        toast(`Installed ${r.info?.latestVersion || 'update'}; restarting…`, { kind: 'success', timeout: 8000 });
      } catch (e) { toast(e.message, { kind: 'error', timeout: 8000 }); }
      await load(); done();
    };

    const render = (doc) => {
      clear(box);
      const st = doc?.status || {};
      const last = st.last;
      box.append(h('div', { class: 'health-cards' },
        hcard(doc?.version || '?', 'Installed version', st.executable || ''),
        hcard(last ? (last.latestVersion || '—') : '—', 'Latest release', last?.publishedAt ? `published ${relTime(last.publishedAt)}` : (last ? 'no release found' : 'not checked yet')),
        hcard(last ? (last.error ? h('span', { class: 'text-down' }, 'Check failed') : last.updateAvailable ? h('span', { class: 'text-degraded' }, 'Update available') : h('span', { class: 'text-up' }, 'Up to date')) : h('span', { class: 'muted' }, 'Unknown'), 'Status',
          st.lastCheckAt ? `checked ${relTime(st.lastCheckAt)}${st.nextCheckAt ? ` · next ${relTime(st.nextCheckAt)}` : ''}` : (st.autoCheck ? 'first check shortly' : 'automatic checks are off'))));
      if (last?.error) box.append(banner('down', last.error));
      if (st.lastError) box.append(banner('down', `Last update attempt failed: ${st.lastError}`));
      if (st.applied) box.append(banner('up', `A new version was installed ${relTime(st.lastApplyAt)}. ${st.restarting ? 'The service is restarting — reload this page in a few seconds.' : 'Restart the service to run it.'}`));
      if (last?.updateAvailable && !last.error) {
        box.append(banner('info', h('span', null,
          h('b', null, `GWatch ${last.latestVersion} is available`),
          last.prerelease ? ' — a pre-release' : '',
          last.currentIsDev ? ' (you are running a development build).' : '.',
          last.assetName ? ` The release includes ${last.assetName} for this platform.` : ' The release has no executable for this platform; build from source or use the installer.')));
        if (last.releaseNotes) box.append(h('details', { class: 'collapsible' }, h('summary', null, icon('chevronRight'), 'Release notes'), h('div', { class: 'update-notes' }, last.releaseNotes)));
      }
      if (!st.canApply && st.executable) box.append(h('p', { class: 'note' }, icon('lock'), ' The executable directory is not writable by the service, so updates cannot be installed from here. Re-run the installer with the new build instead.'));
    };

    // The version list. Anything newer than what is running can be installed,
    // not only the most recent: a release that has been out a while is
    // sometimes the one you want. Pre-releases are shown only when asked for,
    // and say what they are.
    const renderList = () => {
      clear(listBox);
      // The warning belongs to the list, not to the card: it has to come and
      // go with the toggle, which only redraws this box.
      if (showPre) listBox.append(banner('warn', 'Pre-releases are published for testing and are not finished work. Installing one is at your own risk.'));
      if (releases === null) { listBox.append(skeleton({ lines: 3 })); return; }
      const shown = releases.filter((r) => showPre || !r.prerelease);
      if (!shown.length) { listBox.append(emptyState({ icon: 'rocket', title: 'No releases listed', text: 'Either the repository has published none, or GitHub could not be reached.', compact: true })); return; }
      for (const r of shown) {
        const tags = [];
        if (r.running) tags.push(h('span', { class: 'pill' }, 'Running'));
        if (r.prerelease) tags.push(h('span', { class: 'pill pill-warn' }, 'Pre-release'));
        if (!r.installable) tags.push(h('span', { class: 'pill' }, 'No build for this platform'));
        const btn = h('button', { class: 'btn btn-sm', type: 'button', disabled: !r.newer || !r.installable, onclick: () => install(btn, r.version) },
          icon('rocket'), r.newer ? 'Install' : 'Older');
        listBox.append(h('div', { class: 'release-row' },
          h('div', { class: 'release-main' },
            h('div', { class: 'release-title' }, h('b', null, r.name || `GWatch ${r.version}`), ...tags),
            h('div', { class: 'muted small' }, r.publishedAt ? `published ${relTime(r.publishedAt)}` : 'not dated',
              r.assetName ? ` · ${r.assetName}` : '',
              ' · ', h('a', { href: r.url, target: '_blank', rel: 'noopener' }, 'notes'))),
          btn));
      }
    };

    const load = async () => { try { render(await api.get('/api/update/status')); } catch (e) { replace(box, banner('down', e.message)); } };
    const loadList = async () => {
      try { const doc = await api.get('/api/update/releases'); releases = doc.releases || []; }
      catch { releases = []; }
      renderList();
    };

    const checkBtn = h('button', { class: 'btn btn-primary', type: 'button', onclick: async () => {
      const done = busy(checkBtn, 'Checking…');
      try { const info = await api.post('/api/update/check'); toast(info.updateAvailable ? `Update available: ${info.latestVersion}` : `Up to date (${info.latestVersion})`, { kind: info.updateAvailable ? 'info' : 'success' }); }
      catch (e) { toast(e.message, { kind: 'error' }); }
      releases = null; renderList();
      await Promise.all([load(), loadList()]); done();
    } }, icon('refresh'), 'Check now');
    const applyBtn = h('button', { class: 'btn btn-danger', type: 'button', onclick: () => install(applyBtn, '') }, icon('rocket'), 'Install the newest version');

    const preToggle = toggle({ label: 'Show pre-releases', checked: showPre, onChange: (v) => { showPre = v; renderList(); } });
    const autoRow = toggle({ label: 'Check for updates automatically', checked: !!u.checkAutomatically, onChange: (v) => { u.checkAutomatically = v; } });
    const promptRow = toggle({ label: 'Ask me when I open GWatch and an update is waiting', checked: !!u.promptOnOpen, onChange: (v) => { u.promptOnOpen = v; } });
    const preSetting = toggle({ label: 'Offer pre-releases as updates', checked: !!u.includePrerelease, onChange: (v) => { u.includePrerelease = v; showPre = v; preToggle.input.checked = v; renderList(); } });

    await Promise.all([load(), loadList()]);
    state.panelRefresh = load;
    return h('div', { class: 'stack' },
      h('section', { class: 'card' }, h('h2', null, 'Application updates'),
        h('p', { class: 'lead' }, 'GWatch checks the GitHub releases of the repository below, installs the version you choose, verifies its signature and restarts itself.'),
        box,
        h('div', { class: 'btn-group', style: { marginTop: '12px' } }, checkBtn, applyBtn),
        h('div', { class: 'stack-sm', style: { marginTop: '12px' } }, h('a', { href: `https://github.com/${g.updateRepo || 'jxburros/GWatch'}/releases`, target: '_blank', rel: 'noopener', class: 'small' }, icon('external'), ' Open the releases page'))),
      h('section', { class: 'card' }, h('h2', null, 'Available versions'),
        h('p', { class: 'lead' }, 'Install any version newer than the one running — the most recent, or an earlier one you would rather have. An older version than the one running is not installed over it: the database has already been migrated.'),
        h('div', { class: 'row-between', style: { marginBottom: '8px' } }, preToggle),
        listBox),
      h('form', { class: 'card', onsubmit: (e) => { e.preventDefault(); saveSettings(); } }, h('h2', null, 'How GWatch checks'),
        h('p', { class: 'lead' }, 'Checking asks GitHub for the list of releases and nothing else — no account, no machine identity, nothing about what you monitor. Switch it off and GWatch contacts nobody unless you press Check now.'),
        h('div', { class: 'stack-sm' }, autoRow, promptRow, preSetting),
        h('div', { class: 'form-grid', style: { marginTop: '12px' } },
          numField(u, 'checkIntervalHours', 'Check every', { unitLabel: 'hours', min: 1, help: 'Between 1 and 720 hours (30 days). The default is once a day.' }),
          field({ label: 'GitHub repository', input: repo, help: 'Release assets are expected to be named gwatch-<os>-<arch>[.exe], which is what the release job publishes.' })),
        h('hr', { class: 'divider' }), saveBar()));
  }

  /* ---------- Health ---------- */
  async function tabHealth() {
    const wrap = h('div', { class: 'stack' });
    const render = (hl) => {
      clear(wrap);
      const ok = (v, yes = 'Yes', no = 'No') => h('span', { class: `status-glyph ${v ? 'text-up' : 'text-down'}` }, icon(v ? 'check' : 'x'), v ? yes : no);
      const gap = hl.lastGap;
      wrap.append(
        h('section', { class: 'card' }, h('h2', null, 'Service'), h('p', { class: 'lead' }, 'A quiet inbox is only good news if the monitor itself is running.'),
          h('div', { class: 'health-cards' },
            hcard(ok(hl.serviceRunning, 'Running', 'Stopped'), `Background ${hl.serviceMode === 'service' ? 'service' : 'process'}`, `up ${duration(hl.uptimeSeconds)} · since ${dateTime(hl.startedAt, { seconds: false })}`),
            hcard(ok(hl.schedulerRunning, 'Running', 'Stopped'), 'Scheduler', `${hl.checksEnabled} of ${hl.checksTotal} checks enabled · ${hl.checksRunning} running now`),
            hcard(hl.lastCheckAt ? relTime(hl.lastCheckAt) : 'never', 'Last check completed', hl.lastSuccessAt ? `last success ${relTime(hl.lastSuccessAt)}` : ''),
            hcard(hl.nextCheckAt ? relTime(hl.nextCheckAt) : '—', 'Next scheduled check', hl.nextCheckAt ? dateTime(hl.nextCheckAt) : ''),
            hcard(gap ? duration(gap.seconds) : 'None detected', 'Last sleep / offline gap', gap ? `${dateTime(gap.from, { seconds: false })} → ${timeShort(gap.to)}` : 'The monitoring computer has not been asleep or offline recently.'),
            hcard(h('span', { class: 'mono', style: { fontSize: '14px' } }, hl.listenAddress || '—'), 'Listening on', `${hl.platform || ''} · v${hl.version || '?'}`))),
        h('section', { class: 'card' }, h('h2', null, 'Storage and retention'),
          h('div', { class: 'health-cards', style: { marginTop: '10px' } },
            hcard(bytes(hl.databaseBytes), 'Database size', h('span', { class: 'mono' }, hl.databasePath || '')),
            hcard(hl.retention?.lastRunAt ? relTime(hl.retention.lastRunAt) : 'never', 'Last retention run', hl.retention?.lastError ? h('span', { class: 'text-down' }, hl.retention.lastError) : `${num(hl.retention?.rawRows)} raw · ${num(hl.retention?.rollupRows5m)} 5-min · ${num(hl.retention?.rollupRows1h)} hourly · ${num(hl.retention?.rollupRows1d)} daily`),
            hcard(hl.backup?.lastBackupAt ? ok(hl.backup.lastBackupOk, relTime(hl.backup.lastBackupAt), `failed ${relTime(hl.backup.lastBackupAt)}`) : h('span', { class: 'muted' }, 'never'), 'Last backup', hl.backup?.lastBackupFile || hl.backup?.lastError || '')),
          h('div', { class: 'btn-group', style: { marginTop: '12px' } }, h('a', { class: 'btn btn-sm', href: '#/settings/retention' }, 'Retention settings'), h('a', { class: 'btn btn-sm', href: '#/settings/backups' }, 'Backups'))),
        h('section', { class: 'card' }, h('h2', null, 'Alerting'),
          h('div', { class: 'health-cards', style: { marginTop: '10px' } },
            hcard(ok(hl.alertsEnabled, 'Enabled', 'Disabled'), 'Email alerts'),
            hcard(ok(hl.smtpConfigured, 'Configured', 'Not configured'), 'SMTP'),
            hcard(hl.lastAlertAt ? relTime(hl.lastAlertAt) : 'never', 'Last alert sent', hl.lastAlertError ? h('span', { class: 'text-down' }, hl.lastAlertError) : '')),
          h('div', { class: 'btn-group', style: { marginTop: '12px' } }, h('a', { class: 'btn btn-sm', href: '#/settings/alerts' }, 'Alert settings'))),
        h('section', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, 'Recent internal errors'), h('a', { class: 'btn btn-sm', href: '#/audit/log' }, 'Open service log')),
          hl.recentErrors?.length ? h('div', { class: 'event-rows' }, hl.recentErrors.map((e) => eventRow(e))) : h('div', { class: 'all-good', style: { padding: '10px' } }, icon('check'), h('strong', null, 'No internal errors recorded'))),
      );
    };
    const load = () => api.get('/api/health').then(render);
    await load();
    state.panelRefresh = load;
    return wrap;
  }

  /* ---------- Users & access ---------- */
  async function tabUsers() {
    const wrap = h('div', { class: 'stack' });
    const render = async () => {
      const [users, keys] = await Promise.all([api.get('/api/users'), api.get('/api/apikeys')]);
      const s = state.settings || await loadSettings();
      const admins = users.filter((u) => u.role === 'admin').length;
      replace(wrap, usersCard(users, admins, render), localLoginCard(s, admins), apiKeysCard(keys, render), scopesCard());
    };
    await render();
    state.panelRefresh = render;
    return wrap;
  }

  function usersCard(users, admins, reload) {
    const rows = h('div', { class: 'stack-joined' });
    for (const u of users) {
      // The server refuses to remove or demote the last administrator; the
      // controls say so up front instead of letting the person find out.
      const isLastAdmin = u.role === 'admin' && admins <= 1;
      const role = selectInput({
        options: [{ value: 'admin', label: 'Administrator' }, { value: 'viewer', label: 'Viewer' }],
        value: u.role, disabled: isLastAdmin,
        title: isLastAdmin ? 'This is the only administrator account' : null,
        onchange: async () => {
          try {
            await api.put(`/api/users/${u.id}`, { role: role.value });
            toast(`${u.username} is now ${role.value === 'admin' ? 'an administrator' : 'a viewer'}`, { kind: 'success' });
            await reload();
          } catch (e) { toast(e.message, { kind: 'error' }); role.value = u.role; }
        },
      });
      rows.append(h('div', { class: 'access-row' },
        h('div', null,
          h('div', { class: 'a-name' }, icon('user'), h('b', null, u.username)),
          h('div', { class: 'a-meta' },
            h('span', null, `Added ${dateTime(u.createdAt)}`),
            h('span', null, u.lastLoginAt ? `Last signed in ${relTime(u.lastLoginAt)}` : 'Never signed in'))),
        h('div', { class: 'row', style: { gap: '6px' } },
          role,
          h('button', { class: 'btn btn-sm', type: 'button', onclick: () => resetPassword(u, reload) }, 'Reset password'),
          h('button', {
            class: 'btn btn-sm btn-danger', type: 'button', disabled: isLastAdmin,
            title: isLastAdmin ? 'The only administrator account cannot be deleted' : `Delete ${u.username}`,
            onclick: () => deleteUser(u, reload),
          }, icon('trash'))),
      ));
    }
    return h('section', { class: 'card' },
      h('div', { class: 'card-head' }, h('h2', null, 'User accounts'),
        h('button', { class: 'btn btn-primary btn-sm', type: 'button', onclick: () => addUser(reload) }, icon('plus'), 'Add user')),
      h('p', { class: 'lead' }, 'Administrators can change anything. Viewers see dashboards, charts, history, incidents and the audit log, and are refused — with an explanation — on every change.'),
      users.length ? rows : emptyState({ icon: 'users', title: 'No accounts yet', text: 'Without accounts, anyone using this computer has full access and nobody else has any. Add an administrator to sign in from other devices.', compact: true }));
  }

  async function addUser(reload) {
    const name = textInput({ autocomplete: 'off', placeholder: 'e.g. pat' });
    const pw = h('input', { type: 'password', autocomplete: 'new-password', placeholder: 'At least 8 characters' });
    const role = selectInput({ options: [{ value: 'viewer', label: 'Viewer — can look, cannot change' }, { value: 'admin', label: 'Administrator — full access' }], value: 'viewer' });
    const ok = await confirmDialog({
      title: 'Add a user',
      body: h('div', { class: 'stack' },
        field({ label: 'User name', input: name }),
        field({ label: 'Password', input: pw, help: 'Choose something long. There is no reset by email; an administrator resets it here.' }),
        field({ label: 'Role', input: role })),
      confirmLabel: 'Create account',
    });
    if (!ok) return;
    try {
      await api.post('/api/users', { username: name.value.trim(), password: pw.value, role: role.value });
      toast(`Account created for ${name.value.trim()}`, { kind: 'success' });
      await reload();
    } catch (e) { toast(e.message, { kind: 'error' }); }
  }

  async function resetPassword(u, reload) {
    const pw = h('input', { type: 'password', autocomplete: 'new-password', placeholder: 'At least 8 characters' });
    const ok = await confirmDialog({
      title: `Reset the password for ${u.username}`,
      body: h('div', { class: 'stack' },
        h('p', { class: 'lead' }, 'Every browser signed in as this account is signed out.'),
        field({ label: 'New password', input: pw })),
      confirmLabel: 'Reset password',
    });
    if (!ok) return;
    try { await api.put(`/api/users/${u.id}`, { password: pw.value }); toast('Password reset', { kind: 'success' }); await reload(); }
    catch (e) { toast(e.message, { kind: 'error' }); }
  }

  async function deleteUser(u, reload) {
    const ok = await confirmDialog({ title: `Delete ${u.username}?`, message: 'The account and its sessions are removed. Anything it set up stays, and the audit log keeps its name.', confirmLabel: 'Delete account', danger: true });
    if (!ok) return;
    try { await api.del(`/api/users/${u.id}`); toast('Account deleted', { kind: 'success' }); await reload(); }
    catch (e) { toast(e.message, { kind: 'error' }); }
  }

  function localLoginCard(s, admins) {
    const g = s.general;
    const sw = toggle({
      label: 'Require sign-in on this computer too',
      checked: !!g.requireLoginLocally,
      disabled: admins === 0,
      onChange: async (v) => {
        g.requireLoginLocally = v;
        const saved = await saveSettings();
        g.requireLoginLocally = !!saved?.general?.requireLoginLocally;
        sw.input.checked = g.requireLoginLocally;
      },
    });
    return h('section', { class: 'card' }, h('h2', null, 'Sign-in on this computer'),
      h('p', { class: 'lead' }, 'A browser on the computer GWatch runs on is treated as an administrator without signing in — that is what makes a fresh install usable straight away. Turn this on once you have accounts and want everyone, here included, to sign in.'),
      sw,
      admins === 0
        ? h('p', { class: 'note' }, 'Create an administrator account first — otherwise this would lock you out of your own monitor.')
        : h('p', { class: 'note' }, 'Keep a password manager entry for at least one administrator. If every password is lost, the way back in is to restore a backup taken before this was turned on.'));
  }

  function apiKeysCard(keys, reload) {
    const rows = h('div', { class: 'stack-joined' });
    for (const k of keys) {
      rows.append(h('div', { class: `access-row ${k.revokedAt ? 'revoked' : ''}` },
        h('div', null,
          h('div', { class: 'a-name' }, icon('key'), h('b', null, k.name),
            h('span', { class: 'chip' }, k.scope === 'readwrite' ? 'Read & write' : 'Read-only'),
            k.revokedAt ? h('span', { class: 'chip' }, 'Revoked') : null),
          h('div', { class: 'a-meta' },
            h('code', null, `${k.prefix}…`),
            h('span', null, `Created ${dateTime(k.createdAt)}${k.createdBy ? ` by ${k.createdBy}` : ''}`),
            h('span', null, k.lastUsedAt ? `Last used ${relTime(k.lastUsedAt)}` : 'Never used'),
            k.revokedAt ? h('span', null, `Revoked ${relTime(k.revokedAt)}`) : null)),
        k.revokedAt ? h('span', { class: 'muted' }, '—') : h('button', { class: 'btn btn-sm btn-danger', type: 'button', onclick: () => revokeKey(k, reload) }, 'Revoke'),
      ));
    }
    return h('section', { class: 'card' },
      h('div', { class: 'card-head' }, h('h2', null, 'API keys'),
        h('button', { class: 'btn btn-primary btn-sm', type: 'button', onclick: () => createKey(reload) }, icon('plus'), 'New API key')),
      h('p', { class: 'lead' }, 'For scripts, home-automation systems and read-only dashboards away from home. A key is shown once, when you create it.'),
      keys.length ? rows : emptyState({ icon: 'key', title: 'No API keys', text: 'Create one when something other than a browser needs to read this monitor.', compact: true }));
  }

  async function createKey(reload) {
    const name = textInput({ autocomplete: 'off', placeholder: 'e.g. Home Assistant' });
    const scope = selectInput({
      options: [
        { value: 'read', label: 'Read-only — query data, change nothing' },
        { value: 'readwrite', label: 'Read & write — may also create and change nodes and checks' },
      ], value: 'read',
    });
    const ok = await confirmDialog({
      title: 'Create an API key',
      body: h('div', { class: 'stack' },
        field({ label: 'Name', input: name, help: 'So you can recognise it later and revoke the right one.' }),
        field({ label: 'Scope', input: scope, help: 'Start read-only. No key of either scope can reach settings, backups, updates, accounts or automation.' })),
      confirmLabel: 'Create key',
    });
    if (!ok) return;
    try {
      const res = await api.post('/api/apikeys', { name: name.value.trim(), scope: scope.value });
      await reload();
      showKeyOnce(res.key);
    } catch (e) { toast(e.message, { kind: 'error' }); }
  }

  function showKeyOnce(key) {
    const copy = h('button', { class: 'btn btn-primary', type: 'button', onclick: async () => {
      try { await navigator.clipboard.writeText(key); toast('Key copied', { kind: 'success' }); }
      catch { toast('Could not copy — select the key and copy it by hand.', { kind: 'error' }); }
    } }, icon('copy'), 'Copy key');
    openModal({
      title: 'Your new API key',
      body: h('div', { class: 'stack' },
        h('p', { class: 'lead' }, 'Copy it now. GWatch keeps only a fingerprint of it, so it cannot be shown again — if you lose it, revoke it and make another.'),
        h('div', { class: 'key-reveal' }, key),
        h('p', { class: 'note' }, 'Send it as ', h('code', null, 'Authorization: Bearer <key>'), ' or ', h('code', null, 'X-API-Key: <key>'), '.')),
      footer: copy,
    });
  }

  async function revokeKey(k, reload) {
    const ok = await confirmDialog({ title: `Revoke ${k.name}?`, message: 'Anything still using this key stops working immediately. This cannot be undone.', confirmLabel: 'Revoke key', danger: true });
    if (!ok) return;
    try { await api.del(`/api/apikeys/${k.id}`); toast('Key revoked', { kind: 'success' }); await reload(); }
    catch (e) { toast(e.message, { kind: 'error' }); }
  }

  function scopesCard() {
    const item = (t, d) => h('div', null, h('b', null, t), h('div', { class: 'muted', style: { fontSize: '13px' } }, d));
    return h('section', { class: 'card' }, h('h2', null, 'What a key can and cannot do'),
      h('div', { class: 'stack-sm', style: { marginTop: '8px' } },
        item('Read-only', 'Overview, status, nodes and checks, history and charts, incidents, dashboards, and the CSV exports of history, results and events.'),
        item('Read & write', 'Everything above, plus creating, changing, deleting, enabling, running and silencing nodes and checks, notes, maintenance windows, dashboards and charts.'),
        item('Never, whatever the scope', 'Settings, backups and restores, updates, user accounts, API keys, the configuration export, the service log, and anything that runs a trigger, a custom endpoint or an action on this computer.')),
      h('p', { class: 'note' }, 'That last line is deliberate: a key is for reading a monitor from elsewhere, not for administering the machine it runs on. ', h('code', null, 'docs/REMOTE-ACCESS.md'), ' covers how to reach GWatch from outside your network safely.'));
  }

  /* ---------- About ---------- */
  async function tabAbout() {
    const v = state.version || await api.get('/api/version').catch(() => null);
    const versionText = v?.version ? `v${v.version}` : 'unknown';
    const platformText = v?.platform || 'unknown';
    const beta = isBeta(v?.version) ? h('span', { class: 'beta-tag' }, 'Beta') : null;
    return h('div', { class: 'stack' },
      h('section', { class: 'card' },
        h('div', { style: { display: 'flex', gap: '16px', alignItems: 'center', flexWrap: 'wrap' } },
          h('img', { src: 'logo.svg', alt: 'GWatch logo', width: '64', height: '64' }),
          h('div', null,
            h('h2', { style: { marginBottom: '2px' } }, 'GWatch', beta),
            h('p', { class: 'lead', style: { margin: 0 } }, 'A self-hosted monitor for the machines, sites and services on your own network.'),
            h('div', { class: 'muted', style: { marginTop: '4px' } }, `${versionText} · ${platformText}`))),
        h('div', { class: 'health-cards', style: { marginTop: '14px' } },
          hcard(versionText, 'Installed version'), hcard(platformText, 'Platform')),
        isBeta(v?.version)
          ? h('p', { class: 'note', style: { marginTop: '12px' } }, 'This is a beta. GWatch is usable and its data is kept safely, but features and the layout can still change between releases, and a bug here is more likely than it will be at 1.0. ', h('a', { href: 'https://github.com/jxburros/GWatch/issues', target: '_blank', rel: 'noopener' }, 'Report anything that looks wrong.'))
          : null),
      h('section', { class: 'card' },
        h('h2', null, 'Copyright & credit'),
        h('p', null, GWATCH_COPYRIGHT),
        h('p', { class: 'muted' }, 'This notice, the GWatch name and the logo above are required attribution under the project license (see below) and may not be removed or obscured in a copy or derivative of this software.')),
      h('section', { class: 'card' },
        h('h2', null, 'Links'),
        h('div', { class: 'stack-sm' },
          h('div', null, h('a', { href: REPO_URL, target: '_blank', rel: 'noopener' }, 'GitHub repository')),
          h('div', null, h('a', { href: `${REPO_URL}/releases`, target: '_blank', rel: 'noopener' }, 'Releases')),
          h('div', null, h('a', { href: `${REPO_URL}/tree/main/docs`, target: '_blank', rel: 'noopener' }, 'Documentation')),
          h('div', null, h('a', { href: `${REPO_URL}/blob/main/LICENSE`, target: '_blank', rel: 'noopener' }, 'LICENSE')),
          h('div', null, h('a', { href: `${REPO_URL}/blob/main/TRADEMARKS.md`, target: '_blank', rel: 'noopener' }, 'TRADEMARKS.md')))),
      h('section', { class: 'card' },
        h('h2', null, 'License summary'),
        h('p', null, 'GWatch is source-available under the GWatch Community License 1.0. You are free to use, modify and distribute it, including for commercial deployment and support work. You may not resell it unmodified as a competing product, and every copy must keep the required attribution above. This summary is informal — the ', h('a', { href: `${REPO_URL}/blob/main/LICENSE`, target: '_blank', rel: 'noopener' }, 'LICENSE'), ' file is what actually governs.')),
      h('section', { class: 'card' },
        h('h2', null, 'Acknowledgements'),
        h('p', { class: 'lead' }, 'GWatch is written in Go and depends on a small number of open-source libraries:'),
        h('div', { class: 'stack-sm' }, ...DEPENDENCIES.map((d) => h('div', null, h('b', null, d.name), h('span', { class: 'tag', style: { marginLeft: '8px' } }, d.license), h('div', { class: 'muted', style: { fontSize: '13px' } }, d.use))))));
  }

  await renderTab();
  return {
    refresh: () => state.panelRefresh && state.panelRefresh(),
    async update(params) { const tab = visibleTabs.some((t) => t.id === params.tab) ? params.tab : (params.tab === 'logs' ? 'health' : visibleTabs[0].id); if (tab !== state.tab) { state.tab = tab; await renderTab(); } return true; },
    destroy() { state.destroyed = true; },
  };
}

export { qs };

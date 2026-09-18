// Settings: general, appearance, network access, alerts, automation
// (endpoints + all triggers), retention, maintenance, backups, updates,
// monitor health. Logs moved to the Audit tab.

import { api, qs } from '../api.js';
import { h, icon, clear, replace, field, textInput, numberInput, textarea, selectInput, checkbox, toggle, chipInput, toast, confirmDialog, openModal, emptyState, skeleton, banner, eventRow, busy, applyTheme, applyAccent, ACCENT_PRESETS, hexToRgb } from '../components.js';
import { relTime, dateTime, bytes, num, duration, retentionSpan, toLocalInput, fromLocalInput, weekdayShort, timeShort, plural } from '../fmt.js';
import { openEndpointEditor, endpointRow, triggerRow, openTriggerEditor } from './automation.js';

const TABS = [
  { id: 'general', label: 'General' }, { id: 'appearance', label: 'Appearance' }, { id: 'network', label: 'Network access' }, { id: 'alerts', label: 'Alerts' },
  { id: 'automation', label: 'Automation' }, { id: 'retention', label: 'Retention' }, { id: 'maintenance', label: 'Maintenance' },
  { id: 'backups', label: 'Backups' }, { id: 'updates', label: 'Updates' }, { id: 'health', label: 'Monitor health' },
];

export async function mount(root, ctx) {
  const state = { tab: TABS.some((t) => t.id === ctx.params.tab) ? ctx.params.tab : 'general', settings: null, version: null, destroyed: false, panelRefresh: null };
  if (ctx.params.tab === 'logs') { ctx.navigate('/audit/log'); return { destroy() {} }; }
  const nav = h('nav', { class: 'settings-nav', 'aria-label': 'Settings sections' });
  const panel = h('div', { class: 'settings-panel' });
  const versionEl = h('div', { class: 'version-line' });
  root.append(h('div', { class: 'settings-layout' }, nav, h('div', null, panel, versionEl)));

  api.get('/api/version').then((v) => { state.version = v; versionEl.textContent = `GWatch ${v.version || ''} · ${v.platform || ''}`; }).catch(() => {});

  function renderNav() {
    clear(nav);
    for (const t of TABS) nav.append(h('a', { href: `#/settings/${t.id}`, class: t.id === state.tab ? 'active' : '', 'aria-current': t.id === state.tab ? 'page' : null }, t.label));
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
    renderNav();
    const t = TABS.find((x) => x.id === state.tab);
    ctx.setTitle('Settings', { subtitle: t.label });
    replace(panel, skeleton({ lines: 5 }));
    state.panelRefresh = null;
    try {
      const fn = { general: tabGeneral, appearance: tabAppearance, network: tabNetwork, alerts: tabAlerts, automation: tabAutomation, retention: tabRetention, maintenance: tabMaintenance, backups: tabBackups, updates: tabUpdates, health: tabHealth }[state.tab];
      const el = await fn();
      if (!state.destroyed) replace(panel, el);
    } catch (e) { replace(panel, h('div', { class: 'card' }, emptyState({ icon: 'alert', title: 'Could not load settings', text: e.message }))); }
  }

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
        ),
        h('hr', { class: 'divider' }), saveBar()));
  }

  /* ---------- Appearance ---------- */
  async function tabAppearance() {
    const s = state.settings || await loadSettings();
    const g = s.general;
    const themes = [
      { value: 'dark', label: 'Dark', desc: 'Low-glare, for wall displays and night owls.', bg: '#0a0c10', card: '#10131a', fg: '#e9edf2' },
      { value: 'light', label: 'Light', desc: 'Bright, high contrast on white.', bg: '#eef1f5', card: '#ffffff', fg: '#10151d' },
      { value: 'system', label: 'System', desc: 'Follow the operating system preference.', bg: 'linear-gradient(90deg, #0a0c10 50%, #eef1f5 50%)', card: 'linear-gradient(90deg, #10131a 50%, #ffffff 50%)', fg: '#98a2b3' },
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
    const swatches = h('div', { class: 'swatches', role: 'radiogroup', 'aria-label': 'Accent colour' });
    const custom = h('input', { type: 'color', value: g.accentColor || '#7c6cff', 'aria-label': 'Custom accent colour', oninput: () => { g.accentColor = custom.value; applyAccent(custom.value); renderSwatches(); } });
    const hex = textInput({ value: g.accentColor || '#7c6cff', class: 'mono', style: { maxWidth: '110px' }, 'aria-label': 'Accent hex', oninput: () => { if (hexToRgb(hex.value)) { g.accentColor = hex.value.toLowerCase(); custom.value = g.accentColor; applyAccent(g.accentColor); renderSwatches(); } } });
    const renderSwatches = () => {
      clear(swatches);
      for (const p of ACCENT_PRESETS) swatches.append(h('button', { type: 'button', role: 'radio', class: `swatch ${(g.accentColor || '').toLowerCase() === p.hex ? 'active' : ''}`, 'aria-checked': (g.accentColor || '').toLowerCase() === p.hex ? 'true' : 'false', title: p.name, style: { background: p.hex }, onclick: () => { g.accentColor = p.hex; custom.value = p.hex; hex.value = p.hex; applyAccent(p.hex); renderSwatches(); } }));
      swatches.append(custom, hex);
    };
    renderSwatches();
    return h('form', { class: 'stack', onsubmit: (e) => { e.preventDefault(); saveSettings(); } },
      h('section', { class: 'card' }, h('h2', null, 'Theme'), h('p', { class: 'lead' }, 'Changes apply immediately; press Save to keep them for every browser that opens this GWatch.'), themeWrap),
      h('section', { class: 'card' }, h('h2', null, 'Accent colour'), h('p', { class: 'lead' }, 'Used for buttons, highlights, the active navigation item and the first chart line.'), swatches,
        h('div', { class: 'row', style: { marginTop: '14px', gap: '8px' } }, h('button', { class: 'btn btn-primary', type: 'button' }, 'Primary button'), h('button', { class: 'btn', type: 'button' }, 'Button'), h('span', { class: 'chip active' }, 'Active chip'), h('a', { href: '#/settings/appearance' }, 'A link')),
        h('hr', { class: 'divider' }), saveBar()));
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
          h('div', { class: 'form-grid' }, field({ label: 'Access password', input: h('div', { class: 'input-with-unit' }, pw, clearPw), help: 'Other devices are asked for it (any user name). This computer never is.' })),
          info?.listenAddress ? h('p', { class: 'note' }, 'Listening on ', h('code', null, info.listenAddress), info.remoteAccess ? ' — reachable from the network.' : ' — this computer only.', info.restartNeeded ? h('span', { class: 'text-down' }, ' Rebinding failed; a restart is needed.') : null) : null,
        ),
        h('hr', { class: 'divider' }), h('div', { class: 'form-actions' }, saveBtn)),
      h('section', { class: 'card' }, h('h2', null, 'Open GWatch from another device'), h('p', { class: 'lead' }, 'Use one of these addresses. A firewall on this computer may need to allow the port.'), urls),
      h('section', { class: 'card' }, h('h2', null, 'Command-line alternative'), h('p', { class: 'note' }, 'You can also start the service with ', h('code', null, '--listen 0.0.0.0:8080'), ' (or set ', h('code', null, 'GWATCH_LISTEN'), ') to bind every interface regardless of this setting.')));
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
      const epCard = h('section', { class: 'card' },
        h('div', { class: 'card-head' }, h('div', null, h('h2', null, 'Custom endpoints'), h('p', { class: 'lead', style: { marginBottom: 0 } }, 'URLs other systems can call to make GWatch do something: run a node\'s checks after a reboot, run a script, call a webhook or pull a git repository. Each lives at ', h('code', null, '/hook/<name>'), '.')),
          h('button', { class: 'btn btn-primary', type: 'button', onclick: async () => { const saved = await openEndpointEditor(null, { nodes }); if (saved) load(); } }, icon('plus'), 'New endpoint')));
      if (!eps.length) epCard.append(emptyState({ icon: 'webhook', title: 'No endpoints yet', text: 'Create one and call its URL from a script, a router, Home Assistant, a CI job — anything that can make an HTTP request.', compact: true }));
      for (const e of eps) epCard.append(endpointRow(e, { nodes, onChange: load }));
      const trCard = h('section', { class: 'card' },
        h('div', { class: 'card-head' }, h('div', null, h('h2', null, 'Triggers on nodes'), h('p', { class: 'lead', style: { marginBottom: 0 } }, 'Triggers run an action when something happens on a node. They are created on each node\'s page; this is the overview.')),
          nodes.length ? h('button', { class: 'btn', type: 'button', onclick: async () => {
            const sel = h('select', null, nodes.map((n) => h('option', { value: n.id }, n.name)));
            const ok = await confirmDialog({ title: 'New trigger', message: 'Which node should it watch?', confirmLabel: 'Continue', body: h('div', { class: 'field' }, sel) });
            if (!ok) return;
            const node = nodes.find((n) => String(n.id) === sel.value);
            const saved = await openTriggerEditor(null, { node, nodes });
            if (saved) load();
          } }, icon('plus'), 'New trigger') : null));
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
    state.panelRefresh = () => api.get('/api/retention/status').then(renderStatus).catch(() => {});
    return h('form', { class: 'stack', onsubmit: (e) => { e.preventDefault(); saveSettings(); } },
      h('section', { class: 'card' }, h('h2', null, 'History retention'), h('p', { class: 'lead' }, 'Recent data stays detailed; older data is summarised so the database never grows without limit. 0 = keep forever.'),
        summary,
        h('div', { class: 'form-grid-3', style: { marginTop: '14px' } },
          f('rawDays', 'Keep every result for'), f('fiveMinDays', 'Keep 5-minute summaries for'), f('hourlyDays', 'Keep hourly summaries for'), f('dailyDays', 'Keep daily summaries for'), f('eventDays', 'Keep events for')),
        h('hr', { class: 'divider' }), saveBar()),
      h('section', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, 'Current storage'), runBtn), statusBox));
  }

  function hcard(value, label, sub) { return h('div', { class: 'health-card' }, h('div', { class: 'hv' }, value), h('div', { class: 'hl' }, label), sub ? h('div', { class: 'hs' }, sub) : null); }

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
      wrap.append(h('div', { class: 'card-head' }, h('div', null, h('h2', null, 'Maintenance windows'), h('p', { class: 'lead', style: { marginBottom: 0 } }, 'Planned reboots and updates should not page you. Checks keep running; alerts are held and the node is marked as in maintenance.')), h('button', { class: 'btn btn-primary', type: 'button', onclick: () => edit(null) }, icon('plus'), 'New window')));
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
    const listCard = h('section', { class: 'card' });
    const statusLine = h('div');
    const load = async () => {
      const data = await api.get('/api/backups');
      render(data || { backups: [], status: {} });
    };
    const render = (data) => {
      clear(listCard); clear(statusLine);
      const st = data.status || {};
      if (st.lastBackupAt) statusLine.append(banner(st.lastBackupOk ? 'up' : 'down', h('span', null, h('b', null, st.lastBackupOk ? 'Last backup succeeded ' : 'Last backup failed '), `${relTime(st.lastBackupAt)}${st.lastBackupFile ? ' · ' + st.lastBackupFile : ''}${st.lastError ? ' · ' + st.lastError : ''}`, st.lastRestoreAt ? ` · last restore ${relTime(st.lastRestoreAt)}` : '')));
      else statusLine.append(banner('info', 'No backup has been made yet. Create one so you can move to a new computer without re-creating every node.'));
      listCard.append(h('div', { class: 'card-head' }, h('div', null, h('h2', null, 'Backups on this computer'), h('p', { class: 'lead', style: { marginBottom: 0 } }, data.dir ? h('span', { class: 'mono' }, data.dir) : 'Encrypted archives stored locally.'))));
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
    await load();
    state.panelRefresh = load;
    return h('div', { class: 'stack' }, statusLine, createCard, listCard, restoreCard);
  }

  /* ---------- Updates ---------- */
  async function tabUpdates() {
    const s = state.settings || await loadSettings();
    const g = s.general;
    const box = h('div', { class: 'update-box' });
    const repo = textInput({ value: g.updateRepo || 'jxburros/GWatch', class: 'mono', placeholder: 'owner/repository', oninput: () => { g.updateRepo = repo.value; } });
    const render = (doc) => {
      clear(box);
      const st = doc?.status || {};
      const last = st.last;
      box.append(h('div', { class: 'health-cards' },
        hcard(doc?.version || '?', 'Installed version', st.executable || ''),
        hcard(last ? (last.latestVersion || '—') : '—', 'Latest release', last?.publishedAt ? `published ${relTime(last.publishedAt)}` : (last ? 'no release found' : 'not checked yet')),
        hcard(last ? (last.error ? h('span', { class: 'text-down' }, 'Check failed') : last.updateAvailable ? h('span', { class: 'text-degraded' }, 'Update available') : h('span', { class: 'text-up' }, 'Up to date')) : h('span', { class: 'muted' }, 'Unknown'), 'Status', last?.checkedAt ? `checked ${relTime(last.checkedAt)}` : '')));
      if (last?.error) box.append(banner('down', last.error));
      if (st.lastError) box.append(banner('down', `Last update attempt failed: ${st.lastError}`));
      if (st.applied) box.append(banner('up', `A new version was installed ${relTime(st.lastApplyAt)}. ${st.restarting ? 'The service is restarting — reload this page in a few seconds.' : 'Restart the service to run it.'}`));
      if (last?.updateAvailable && !last.error) {
        box.append(banner('info', h('span', null, h('b', null, `GWatch ${last.latestVersion} is available`), last.currentIsDev ? ' (you are running a development build).' : '.', last.assetName ? ` The release includes ${last.assetName} for this platform.` : ' The release has no executable for this platform; build from source or use the installer script.')));
        if (last.releaseNotes) box.append(h('details', { class: 'collapsible' }, h('summary', null, icon('chevronRight'), 'Release notes'), h('div', { class: 'update-notes' }, last.releaseNotes)));
      }
      if (!st.canApply && st.executable) box.append(h('p', { class: 'note' }, icon('lock'), ' The executable directory is not writable by the service, so updates cannot be installed from here. Re-run the installer script with the new build instead.'));
    };
    const load = async () => { try { render(await api.get('/api/update/status')); } catch (e) { replace(box, banner('down', e.message)); } };
    const checkBtn = h('button', { class: 'btn btn-primary', type: 'button', onclick: async () => {
      const done = busy(checkBtn, 'Checking…');
      try { const info = await api.post('/api/update/check'); toast(info.updateAvailable ? `Update available: ${info.latestVersion}` : `Up to date (${info.latestVersion})`, { kind: info.updateAvailable ? 'info' : 'success' }); }
      catch (e) { toast(e.message, { kind: 'error' }); }
      await load(); done();
    } }, icon('refresh'), 'Check for updates');
    const applyBtn = h('button', { class: 'btn btn-danger', type: 'button', onclick: async () => {
      const ok = await confirmDialog({ title: 'Install the update now?', message: 'GWatch downloads the release executable, replaces the current one (the previous version is kept as .old) and restarts. Monitoring pauses for a few seconds.', confirmLabel: 'Install and restart', danger: true });
      if (!ok) return;
      const done = busy(applyBtn, 'Installing…');
      try { const r = await api.post('/api/update/apply'); toast(`Installed ${r.info?.latestVersion || 'update'}; restarting…`, { kind: 'success', timeout: 8000 }); }
      catch (e) { toast(e.message, { kind: 'error', timeout: 8000 }); }
      await load(); done();
    } }, icon('rocket'), 'Download and install');
    await load();
    state.panelRefresh = load;
    return h('div', { class: 'stack' },
      h('section', { class: 'card' }, h('h2', null, 'Application updates'), h('p', { class: 'lead' }, 'Checks the GitHub releases of the repository below. Nothing is contacted automatically; only when you press the button.'),
        box, h('div', { class: 'btn-group', style: { marginTop: '12px' } }, checkBtn, applyBtn),
        h('div', { class: 'stack-sm', style: { marginTop: '12px' } }, h('a', { href: `https://github.com/${g.updateRepo || 'jxburros/GWatch'}/releases`, target: '_blank', rel: 'noopener', class: 'small' }, icon('external'), ' Open the releases page'))),
      h('form', { class: 'card', onsubmit: (e) => { e.preventDefault(); saveSettings(); } }, h('h2', null, 'Source repository'), h('p', { class: 'lead' }, 'Release assets are expected to be named gwatch-<os>-<arch>[.exe], which is what the CI release job publishes.'),
        h('div', { class: 'form-grid' }, field({ label: 'GitHub repository', input: repo })), h('hr', { class: 'divider' }), saveBar()));
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

  await renderTab();
  return {
    refresh: () => state.panelRefresh && state.panelRefresh(),
    async update(params) { const tab = TABS.some((t) => t.id === params.tab) ? params.tab : (params.tab === 'logs' ? 'health' : 'general'); if (tab !== state.tab) { state.tab = tab; await renderTab(); } return true; },
    destroy() { state.destroyed = true; },
  };
}

export { qs };

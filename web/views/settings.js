// Settings: general, alerts, retention, maintenance, backups, health, logs.

import { api, qs } from '../api.js';
import { h, icon, clear, replace, field, textInput, numberInput, textarea, selectInput, checkbox, toggle, chipInput, toast, confirmDialog, promptDialog, openModal, emptyState, skeleton, banner, eventRow, busy } from '../components.js';
import { relTime, dateTime, bytes, num, duration, retentionSpan, toLocalInput, fromLocalInput, weekdayShort, timeShort, plural } from '../fmt.js';

const TABS = [
  { id: 'general', label: 'General' }, { id: 'alerts', label: 'Alerts' }, { id: 'retention', label: 'Retention' }, { id: 'maintenance', label: 'Maintenance' },
  { id: 'backups', label: 'Backups' }, { id: 'health', label: 'Monitor health' }, { id: 'logs', label: 'Logs' },
];

export async function mount(root, ctx) {
  const state = { tab: TABS.some((t) => t.id === ctx.params.tab) ? ctx.params.tab : 'general', settings: null, version: null, destroyed: false, panelRefresh: null };
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
  }

  async function renderTab() {
    renderNav();
    const t = TABS.find((x) => x.id === state.tab);
    ctx.setTitle('Settings', { subtitle: t.label });
    replace(panel, skeleton({ lines: 5 }));
    state.panelRefresh = null;
    try {
      const fn = { general: tabGeneral, alerts: tabAlerts, retention: tabRetention, maintenance: tabMaintenance, backups: tabBackups, health: tabHealth, logs: tabLogs }[state.tab];
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
      h('section', { class: 'card' }, h('h2', null, 'Alerts'), h('p', { class: 'lead' }, 'Alerts are meant to be quiet and explainable: one email when something breaks, one when it recovers.'),
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
        h('div', { class: 'stack-sm', style: { marginTop: '10px' } },
          h('p', { class: 'note' }, h('b', null, 'Dependencies: '), 'when a node "depends on" another (say Plex depends on Gateway) and the parent is down, failures on the child are recorded as "affected by Gateway" and no separate email is sent for it. You get one email about the gateway instead of twenty about everything behind it.'),
          h('p', { class: 'note' }, h('b', null, 'Cooldown: '), 'after an alert is sent for a check, further alerts for that same check are held back for the cooldown period. A recovery email is still sent as soon as it comes back.'),
          h('p', { class: 'note' }, h('b', null, 'Maintenance and silences: '), 'checks keep running and history is kept, but alerts are suppressed and shown as such in the incident timeline.'))));
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
        st.lastError ? h('div', { style: { marginTop: '12px' } }, banner('down', `Last run failed: ${st.lastError}`)) : null,
        st.plan?.length ? h('ul', { class: 'note', style: { marginTop: '14px', paddingLeft: '18px' } }, st.plan.map((p) => h('li', null, p))) : null);
    };
    api.get('/api/retention/status').then(renderStatus).catch((e) => replace(statusBox, h('p', { class: 'note' }, e.message)));
    state.panelRefresh = () => api.get('/api/retention/status').then(renderStatus).catch(() => {});
    return h('form', { class: 'stack', onsubmit: (e) => { e.preventDefault(); saveSettings(); } },
      h('section', { class: 'card' }, h('h2', null, 'History retention'), h('p', { class: 'lead' }, 'Recent data stays detailed; older data is summarised so the database never grows without limit. 0 = keep forever.'),
        summary,
        h('div', { class: 'form-grid-3', style: { marginTop: '20px' } },
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
          const s = new Date(); s.setHours(hh, mm, 0, 0);
          payload.startAt = s.toISOString(); payload.durationMinutes = Number(durationIn.value) || 60;
          payload.endAt = new Date(s.getTime() + payload.durationMinutes * 60e3).toISOString();
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

    // Create form
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
    const createCard = h('section', { class: 'card' }, h('h2', null, 'Create a backup'), h('p', { class: 'lead' }, 'The archive is encrypted with the password you choose. Keep it somewhere safe — without it the backup cannot be restored.'),
      h('div', { class: 'form-grid' }, field({ label: 'Password', input: pw }), field({ label: 'Confirm password', input: pw2 }), h('div', { class: 'span-2' }, inclHist)),
      h('div', { class: 'form-actions', style: { marginTop: '16px' } }, createBtn), createResult);

    // Restore from file
    const file = h('input', { type: 'file', accept: '.gwbackup,.zip,.bin,*/*' });
    const rpw = h('input', { type: 'password', autocomplete: 'off', placeholder: 'Backup password' });
    const rHist = checkbox({ label: 'Also restore history and events', checked: true });
    const restoreResult = h('div', { role: 'status' });
    const restoreBtn = h('button', { class: 'btn btn-danger', type: 'button', onclick: async () => {
      if (!file.files?.[0]) { toast('Choose a backup file first', { kind: 'error' }); return; }
      if (!rpw.value) { toast('Enter the backup password', { kind: 'error' }); rpw.focus(); return; }
      const ok = await confirmDialog({ title: 'Restore from file?', message: 'This replaces the current configuration (nodes, checks, dashboards, maintenance windows and settings) with the contents of the backup. History is replaced too if you chose to include it.', confirmLabel: 'Restore', danger: true });
      if (!ok) return;
      const fd = new FormData(); fd.append('file', file.files[0]); fd.append('password', rpw.value); fd.append('includeHistory', rHist.input.checked ? 'true' : 'false');
      const done = busy(restoreBtn, 'Restoring…');
      try { const r = await api.upload('/api/backups/restore', fd); replace(restoreResult, banner('up', `Restored ${plural(r.nodes ?? 0, 'node')}, ${plural(r.checks ?? 0, 'check')} and ${num(r.results ?? 0)} results.`)); toast('Restore complete', { kind: 'success' }); load(); }
      catch (e) { replace(restoreResult, banner('down', `Restore failed: ${e.message}`)); }
      done();
    } }, icon('upload'), 'Restore from file');
    const restoreCard = h('section', { class: 'card' }, h('h2', null, 'Restore from a file'), h('p', { class: 'lead' }, 'Moving to a new computer? Install GWatch, then restore the archive you downloaded from the old one.'),
      h('div', { class: 'form-grid' }, field({ label: 'Backup file', input: file }), field({ label: 'Password', input: rpw }), h('div', { class: 'span-2' }, rHist)),
      h('div', { class: 'form-actions', style: { marginTop: '16px' } }, restoreBtn), restoreResult);

    async function restoreExisting(b) {
      const pwIn = h('input', { type: 'password', autocomplete: 'off', placeholder: 'Backup password' });
      const hist = checkbox({ label: 'Also restore history and events', checked: b.includeHistory, disabled: !b.includeHistory });
      const ok = await confirmDialog({ title: `Restore ${b.fileName}?`, confirmLabel: 'Restore', danger: true, body: h('div', { class: 'stack-sm' }, banner('warn', 'This replaces the current configuration. Nodes, checks, dashboards, maintenance windows and settings will be overwritten.'), field({ label: 'Password', input: pwIn }), hist) });
      if (!ok) return;
      if (!pwIn.value) { toast('The backup password is required', { kind: 'error' }); return; }
      try { const r = await api.post('/api/backups/restore-existing', { fileName: b.fileName, password: pwIn.value, includeHistory: hist.input.checked }); toast(`Restored ${plural(r.nodes ?? 0, 'node')} and ${plural(r.checks ?? 0, 'check')}`, { kind: 'success' }); load(); }
      catch (e) { toast(`Restore failed: ${e.message}`, { kind: 'error' }); }
    }

    await load();
    state.panelRefresh = load;
    return h('div', { class: 'stack' }, statusLine, createCard, listCard, restoreCard);
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
            hcard(h('span', { class: 'mono', style: { fontSize: '15px' } }, hl.listenAddress || '—'), 'Listening on', `${hl.platform || ''} · v${hl.version || '?'}`))),
        h('section', { class: 'card' }, h('h2', null, 'Storage and retention'),
          h('div', { class: 'health-cards', style: { marginTop: '14px' } },
            hcard(bytes(hl.databaseBytes), 'Database size', h('span', { class: 'mono' }, hl.databasePath || '')),
            hcard(hl.retention?.lastRunAt ? relTime(hl.retention.lastRunAt) : 'never', 'Last retention run', hl.retention?.lastError ? h('span', { class: 'text-down' }, hl.retention.lastError) : `${num(hl.retention?.rawRows)} raw · ${num(hl.retention?.rollupRows5m)} 5-min · ${num(hl.retention?.rollupRows1h)} hourly · ${num(hl.retention?.rollupRows1d)} daily`),
            hcard(hl.backup?.lastBackupAt ? ok(hl.backup.lastBackupOk, relTime(hl.backup.lastBackupAt), `failed ${relTime(hl.backup.lastBackupAt)}`) : h('span', { class: 'muted' }, 'never'), 'Last backup', hl.backup?.lastBackupFile || hl.backup?.lastError || '')),
          h('div', { class: 'btn-group', style: { marginTop: '16px' } }, h('a', { class: 'btn btn-sm', href: '#/settings/retention' }, 'Retention settings'), h('a', { class: 'btn btn-sm', href: '#/settings/backups' }, 'Backups'))),
        h('section', { class: 'card' }, h('h2', null, 'Alerting'),
          h('div', { class: 'health-cards', style: { marginTop: '14px' } },
            hcard(ok(hl.alertsEnabled, 'Enabled', 'Disabled'), 'Email alerts'),
            hcard(ok(hl.smtpConfigured, 'Configured', 'Not configured'), 'SMTP'),
            hcard(hl.lastAlertAt ? relTime(hl.lastAlertAt) : 'never', 'Last alert sent', hl.lastAlertError ? h('span', { class: 'text-down' }, hl.lastAlertError) : '')),
          h('div', { class: 'btn-group', style: { marginTop: '16px' } }, h('a', { class: 'btn btn-sm', href: '#/settings/alerts' }, 'Alert settings'))),
        h('section', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, 'Recent internal errors'), h('a', { class: 'btn btn-sm', href: '#/settings/logs' }, 'Open logs')),
          hl.recentErrors?.length ? h('div', { class: 'event-rows' }, hl.recentErrors.map((e) => eventRow(e))) : h('div', { class: 'all-good', style: { padding: '12px' } }, icon('check'), h('strong', null, 'No internal errors recorded'))),
      );
    };
    const load = () => api.get('/api/health').then(render);
    await load();
    state.panelRefresh = load;
    return wrap;
  }

  /* ---------- Logs ---------- */
  async function tabLogs() {
    const box = h('pre', { class: 'log-box', tabindex: 0, 'aria-label': 'Log output' });
    const limit = selectInput({ options: [100, 200, 500, 1000].map((n) => ({ value: n, label: `Last ${n} lines` })), value: 200 });
    const fileEl = h('span', { class: 'mono small muted' });
    const load = async () => {
      const data = await api.get(`/api/logs${qs({ limit: limit.value })}`);
      clear(box);
      fileEl.textContent = data.file || '';
      for (const line of data.lines || []) {
        const cls = /\b(ERROR|error|panic)\b/.test(line) ? 'lvl-error' : /\b(WARN|warning)\b/i.test(line) ? 'lvl-warn' : '';
        box.append(h('span', { class: cls }, line + '\n'));
      }
      if (!(data.lines || []).length) box.textContent = '(log is empty)';
      box.scrollTop = box.scrollHeight;
    };
    limit.addEventListener('change', load);
    const refreshBtn = h('button', { class: 'btn btn-sm', type: 'button', onclick: load }, icon('refresh'), 'Refresh');
    await load();
    return h('section', { class: 'card' }, h('div', { class: 'card-head' }, h('div', null, h('h2', null, 'Service log'), fileEl), h('div', { class: 'card-actions' }, limit, refreshBtn)), box);
  }

  await renderTab();
  return {
    refresh: () => state.panelRefresh && state.panelRefresh(),
    async update(params) { const tab = TABS.some((t) => t.id === params.tab) ? params.tab : 'general'; if (tab !== state.tab) { state.tab = tab; await renderTab(); } return true; },
    destroy() { state.destroyed = true; },
  };
}

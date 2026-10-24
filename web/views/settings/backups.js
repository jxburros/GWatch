// Settings › Backups.
//
// Part of settings.js, split out for #118: the code is the
// view's own, moved here unchanged inside create(shell), which hands it the
// view's shared state and helpers.

import { api } from '../../api.js';
import { h, icon, clear, replace, field, checkbox, toggle, toast, confirmDialog, emptyState, skeleton, banner, busy } from '../../components.js';
import { relTime, dateTime, bytes, num, plural } from '../../fmt.js';

/** @param {Record<string, any>} shell the view's state and helpers */
export function create(shell) {
  const { loadDataPaths, loadSettings, numField, saveSettings, state } = shell;

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
            h('button', { class: 'btn btn-sm btn-danger', type: 'button', 'aria-label': `Delete ${b.fileName}`, title: 'Delete', onclick: async () => { if (await confirmDialog({ title: `Delete ${b.fileName}?`, confirmLabel: 'Delete', danger: true })) { try { await api.del(`/api/backups/${encodeURIComponent(b.fileName)}`); toast('Backup deleted', { kind: 'success' }); load(); } catch (e) { toast(e.message, { kind: 'error' }); } } } }, icon('trash')))));
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
      h('div', { class: 'form-actions u-mt-12' }, createBtn), createResult);
    const file = h('input', { type: 'file', accept: '.gwbackup,.zip,.bin,*/*' });
    const rpw = h('input', { type: 'password', autocomplete: 'off', placeholder: 'Backup password' });
    const rHist = checkbox({ label: 'Also restore history and events', checked: true });
    const rAccounts = accountsCheckbox();
    const restoreResult = h('div', { role: 'status' });
    const restoreBtn = h('button', { class: 'btn btn-danger', type: 'button', onclick: async () => {
      if (!file.files?.[0]) { toast('Choose a backup file first', { kind: 'error' }); return; }
      if (!rpw.value) { toast('Enter the backup password', { kind: 'error' }); rpw.focus(); return; }
      const form = () => { const fd = new FormData(); fd.append('file', file.files[0]); fd.append('password', rpw.value); fd.append('includeHistory', rHist.input.checked ? 'true' : 'false'); fd.append('restoreAccounts', rAccounts.input.checked ? 'true' : 'false'); return fd; };
      // First the preview (#148): nothing is changed until it has been read
      // and confirmed.
      let done = busy(restoreBtn, 'Reading the backup…');
      let preview;
      try { preview = await api.upload('/api/backups/inspect', form()); }
      catch (e) { done(); replace(restoreResult, restoreFailure('Cannot restore this backup', e)); return; }
      done();
      const ok = await confirmRestore('Restore from file?', preview, { includeHistory: rHist.input.checked, restoreAccounts: rAccounts.input.checked });
      if (!ok) return;
      done = busy(restoreBtn, 'Restoring…');
      try { const r = await api.upload('/api/backups/restore', form()); replace(restoreResult, banner('up', `Restored ${plural(r.nodes ?? 0, 'node')}, ${plural(r.checks ?? 0, 'check')} and ${num(r.results ?? 0)} results.${restoreNotes(r)}`)); toast('Restore complete', { kind: 'success' }); load(); }
      catch (e) { replace(restoreResult, restoreFailure('Restore failed', e)); if (e.body?.configRestored) load(); }
      done();
    } }, icon('upload'), 'Restore from file');
    const restoreCard = h('section', { class: 'card' }, h('h2', null, 'Restore from a file'), h('p', { class: 'lead' }, 'Moving to a new computer? Install GWatch, then restore the archive you downloaded from the old one.'),
      h('div', { class: 'form-grid' }, field({ label: 'Backup file', input: file }), field({ label: 'Password', input: rpw }), h('div', { class: 'span-2' }, rHist), h('div', { class: 'span-2' }, rAccounts)),
      h('div', { class: 'form-actions u-mt-12' }, restoreBtn), restoreResult);
    // A restore takes a backup of what it replaces first, and leaves out the
    // accounts and agents deleted since the backup was made unless asked.
    function accountsCheckbox() { return checkbox({ label: 'Re-create accounts and agents that have been deleted since this backup', checked: false }); }
    function restoreNotes(r) {
      const notes = [];
      if (r.skippedUsers?.length) notes.push(`Not re-created: ${plural(r.skippedUsers.length, 'account')} (${r.skippedUsers.join(', ')}).`);
      if (r.skippedAgents?.length) notes.push(`Not re-created: ${plural(r.skippedAgents.length, 'agent')} (${r.skippedAgents.join(', ')}).`);
      if (r.safetyBackup) notes.push(`What was here before is kept as ${r.safetyBackup}.`);
      return notes.length ? ` ${notes.join(' ')}` : '';
    }
    async function restoreExisting(b) {
      const pwIn = h('input', { type: 'password', autocomplete: 'off', placeholder: 'Backup password' });
      const hist = checkbox({ label: 'Also restore history and events', checked: b.includeHistory, disabled: !b.includeHistory });
      const accounts = accountsCheckbox();
      const ok = await confirmDialog({ title: `Restore ${b.fileName}?`, confirmLabel: 'Continue', body: h('div', { class: 'stack-sm' }, h('p', null, 'Enter the backup’s password. You will see what it contains, and what it would change, before anything is restored.'), field({ label: 'Password', input: pwIn }), hist, accounts) });
      if (!ok) return;
      if (!pwIn.value) { toast('The backup password is required', { kind: 'error' }); return; }
      const opts = { includeHistory: hist.input.checked, restoreAccounts: accounts.input.checked };
      let preview;
      try { preview = await api.post('/api/backups/inspect-existing', { fileName: b.fileName, password: pwIn.value, restoreAccounts: opts.restoreAccounts }); }
      catch (e) { toast(`Cannot restore this backup: ${failureText(e)}`, { kind: 'error', timeout: 10000 }); return; }
      if (!await confirmRestore(`Restore ${b.fileName}?`, preview, opts)) return;
      try { const r = await api.post('/api/backups/restore-existing', { fileName: b.fileName, password: pwIn.value, ...opts }); toast(`Restored ${plural(r.nodes ?? 0, 'node')} and ${plural(r.checks ?? 0, 'check')}.${restoreNotes(r)}`, { kind: 'success', timeout: 10000 }); load(); }
      catch (e) { toast(`Restore failed: ${failureText(e)}`, { kind: 'error', timeout: 10000 }); if (e.body?.configRestored) load(); }
    }
    // A refusal for the archive's content carries the list of problems.
    function failureText(e) { return e.body?.problems?.length ? `${e.body.problems.length === 1 ? 'one problem' : plural(e.body.problems.length, 'problem')}: ${e.body.problems.join('; ')}` : e.message; }
    function restoreFailure(title, e) {
      const problems = e.body?.problems || [];
      return banner('down', problems.length
        ? h('div', null, h('b', null, `${title}: its configuration does not pass validation, so nothing was changed.`), h('ul', { class: 'restore-problems' }, problems.map((p) => h('li', null, p))))
        : `${title}: ${e.message}`);
    }
    // The preview of a restore (POST /api/backups/inspect, #148): what the
    // backup holds, and everything in it that matters for security, shown
    // before the administrator confirms. A backup with validation problems
    // cannot be restored, so its dialog only lists them.
    async function confirmRestore(title, p, { includeHistory, restoreAccounts }) {
      if (p.problems?.length) {
        await confirmDialog({ title: 'This backup cannot be restored', confirmLabel: 'Close', body: h('div', { class: 'stack-sm' },
          banner('down', `Its configuration does not pass the checks GWatch applies when the same things are saved by hand, so it would be refused. ${plural(p.problems.length, 'problem')}:`),
          h('ul', { class: 'restore-problems' }, p.problems.map((x) => h('li', null, x)))) });
        return false;
      }
      const c = p.counts || {};
      const parts = [plural(c.nodes ?? 0, 'node'), plural(c.checks ?? 0, 'check'), plural(c.dashboards ?? 0, 'dashboard'), plural(c.triggers ?? 0, 'trigger'), plural(c.endpoints ?? 0, 'endpoint'), plural(c.rules ?? 0, 'notification rule'), plural(c.wallboards ?? 0, 'wallboard'), plural(c.users ?? 0, 'account'), plural(c.apiKeys ?? 0, 'API key'), plural(c.agents ?? 0, 'agent')];
      const made = p.manifest?.createdAt ? ` Made ${dateTime(p.manifest.createdAt)}${p.manifest.appVersion ? ` by GWatch ${p.manifest.appVersion}` : ''}.` : '';
      const history = includeHistory && c.history ? ` History: ${num(c.results ?? 0)} results, ${num(c.rollups ?? 0)} rollups and ${num(c.events ?? 0)} events replace the current history.` : ' The current history is kept.';
      const willAdd = restoreAccounts || p.accountsRecreatedAnyway;
      const notes = [];
      if (p.accounts?.length) {
        const names = p.accounts.map((a) => `${a.username}${a.admin ? ' (administrator)' : ''}`).join(', ');
        notes.push(willAdd ? `${plural(p.accounts.length, 'account')} will be added: ${names}.` : `${plural(p.accounts.length, 'account')} in the backup ${p.accounts.length === 1 ? 'is' : 'are'} not on this server and will be left out: ${names}.`);
      }
      if (p.agents?.length) notes.push(willAdd ? `${plural(p.agents.length, 'agent')} will be added: ${p.agents.join(', ')}.` : `${plural(p.agents.length, 'agent')} not on this server will be left out: ${p.agents.join(', ')}.`);
      if (p.apiKeys?.length) notes.push(`${plural(p.apiKeys.length, 'API key')} will be added: ${p.apiKeys.map((k) => `${k.name} (${k.scope === 'readwrite' ? 'read & write' : 'read only'}${k.revoked ? ', revoked' : ''})`).join(', ')}.`);
      for (const x of p.commands || []) notes.push(`Runs a command on this computer: ${x.kind} ${x.name} — ${x.detail}.`);
      for (const slug of p.tokenlessEndpoints || []) notes.push(`/hook/${slug} can be called by anyone who can reach GWatch, without a token.`);
      for (const line of p.accessChanges || []) notes.push(line);
      const security = notes.length
        ? banner('warn', h('div', null, h('b', null, 'Check these before you restore:'), h('ul', { class: 'restore-notes' }, notes.map((n) => h('li', null, n)))))
        : banner('info', 'Nothing in this backup adds accounts or API keys, runs commands, opens an endpoint without a token, or changes network access or sign-in.');
      return confirmDialog({ title, confirmLabel: 'Restore', danger: true, body: h('div', { class: 'stack-sm restore-preview' },
        h('p', null, `This backup holds ${parts.join(', ')}.${made}${history}`),
        security,
        h('p', { class: 'note' }, 'Restoring replaces the current configuration: nodes, checks, dashboards, maintenance windows, automation and settings. Nodes and checks absent from the backup are removed with their history. A backup of what is here now is taken first.')) });
    }
    const autoEnabled = toggle({ label: 'Create backups automatically', checked: !!bk.enabled, onChange: (v) => { bk.enabled = v; } });
    const autoInterval = numField(bk, 'intervalHours', 'Every', { unitLabel: 'hours', min: 1 });
    const autoKeep = numField(bk, 'keep', 'Keep the newest', { unitLabel: 'archives', min: 1 });
    const autoHist = checkbox({ label: 'Include performance history and events (bigger archives)', checked: !!bk.includeHistory, onChange: (v) => { bk.includeHistory = v; } });
    const savedPw = bk.password || '';
    const autoPw = h('input', { type: 'password', value: savedPw, autocomplete: 'new-password', placeholder: bk.password ? '' : 'Required to enable automatic backups', oninput: () => { bk.password = autoPw.value; } });
    const autoPw2 = h('input', { type: 'password', autocomplete: 'new-password', placeholder: 'Repeat the password (leave blank to keep the saved one)' });
    const autoSaveBtn = h('button', { class: 'btn btn-primary', type: 'button', onclick: async () => {
      // Only the saved password (shown masked) may stand unconfirmed: a newly
      // typed one is checked against the confirmation, blank or not.
      if ((autoPw2.value || autoPw.value !== savedPw) && autoPw.value !== autoPw2.value) { toast('The passwords do not match', { kind: 'error' }); autoPw2.focus(); return; }
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
      h('div', { class: 'form-grid u-mt-12' },
        autoInterval, autoKeep, h('div', { class: 'span-2' }, autoHist),
        field({ label: 'Password', input: autoPw, help: bk.password ? 'A password is already saved; leave blank to keep it.' : 'Backups are always encrypted, so a password is required to enable this.' }),
        field({ label: 'Confirm password', input: autoPw2 })),
      h('div', { class: 'form-actions u-mt-12' }, autoSaveBtn), nextLine,
      h('hr', { class: 'divider' }), h('p', { class: 'lead' }, 'Where archives land, alongside the database itself:'), pathsBox);
    await load();
    state.panelRefresh = load;
    return h('div', { class: 'stack' }, statusLine, createCard, autoCard, listCard, restoreCard);
  }

  return { tabBackups };
}

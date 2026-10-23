// Settings: what more than one section uses.
//
// Part of settings.js, split out for #118: the code is the
// view's own, moved here unchanged inside create(shell), which hands it the
// view's shared state and helpers.

import { api } from '../../api.js';
import { h, icon, clear, replace, field, textInput, selectInput, toast, confirmDialog, openModal } from '../../components.js';

export const REPO_URL = 'https://github.com/jxburros/GWatch';

/** @param {Record<string, any>} shell the view's state and helpers */
export function create(shell) {
  function hcard(value, label, sub) { return h('div', { class: 'health-card' }, h('div', { class: 'hv' }, value), h('div', { class: 'hl' }, label), sub ? h('div', { class: 'hs' }, sub) : null); }

  // dataPathsBlock shows where GWatch's files actually live, in a read-only
  // monospace block. Used on both the Retention and Backups tabs, since that
  // is where people go looking for "where is my data".
  function dataPathsBlock(hl) {
    const dl = h('dl', { class: 'kv' });
    const row = (k, v) => { if (v) dl.append(h('dt', null, k), h('dd', { class: 'mono' }, v)); };
    row('Data directory', hl.dataDir);
    row('Database', hl.databasePath);
    row('Database driver', hl.databaseDriver);
    row('Key file', hl.keyPath);
    row('Backups folder', hl.backupDir);
    return h('div', { class: 'u-mt-10' }, dl,
      h('p', { class: 'note u-mt-8' },
        'These are ordinary files on this computer’s own disk, in a permanent folder — never a temp directory the OS can clear on reboot or under disk pressure.'));
  }
  function loadDataPaths(box) {
    api.get('/api/health').then((hl) => replace(box, dataPathsBlock(hl))).catch(() => clear(box));
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

  return { createKey, hcard, loadDataPaths };
}

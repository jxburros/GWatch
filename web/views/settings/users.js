// Settings › Users & access: accounts, local sign-in, password hashing and API keys.
//
// Part of settings.js, split out for #118: the code is the
// view's own, moved here unchanged inside create(shell), which hands it the
// view's shared state and helpers.

import { api } from '../../api.js';
import { h, icon, replace, field, textInput, selectInput, toggle, toast, confirmDialog, emptyState } from '../../components.js';
import { relTime, dateTime } from '../../fmt.js';

/** @param {Record<string, any>} shell the view's state and helpers */
export function create(shell) {
  const { createKey, isAdmin, loadSettings, saveSettings, state } = shell;

  async function tabUsers() {
    const wrap = h('div', { class: 'stack' });
    const render = async () => {
      const [users, keys] = await Promise.all([api.get('/api/users'), api.get('/api/apikeys')]);
      const s = state.settings || await loadSettings();
      const admins = users.filter((u) => u.role === 'admin').length;
      replace(wrap, usersCard(users, admins, render), localLoginCard(s, admins), passwordHashingCard(s), apiKeysCard(keys, render), scopesCard());
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
        h('div', { class: 'row u-gap-6' },
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

  // How account passwords and backup keys are derived (#49). Argon2id is the
  // default and the recommendation; PBKDF2 is there for owners who would
  // rather GWatch depended on the Go standard library alone. Changing it never
  // locks anybody out: existing hashes keep verifying, and each account moves
  // to the selected algorithm the next time it signs in.
  function passwordHashingCard(s) {
    const g = s.general;
    const sel = selectInput({
      options: [
        { value: 'argon2id', label: 'Argon2id (recommended)' },
        { value: 'pbkdf2-sha256', label: 'PBKDF2 (standard library only)' },
      ],
      value: g.passwordHashing || 'argon2id',
      disabled: !isAdmin,
      onchange: async () => {
        g.passwordHashing = sel.value;
        const saved = await saveSettings();
        g.passwordHashing = saved?.general?.passwordHashing || 'argon2id';
        sel.value = g.passwordHashing;
      },
    });
    return h('section', { class: 'card' }, h('h2', null, 'Password hashing'),
      h('p', { class: 'lead' }, 'How account passwords are stored and how backup files are encrypted. Argon2id needs a lot of memory for every guess, which makes stolen hashes and backups expensive to attack with graphics cards. PBKDF2-SHA256 (600,000 rounds) comes from the Go standard library alone, but at the same cost to this computer it is cheaper to attack that way.'),
      field({ label: 'Password hashing', input: sel, help: 'Existing passwords keep working either way. Each account switches to the chosen method the next time it signs in, and new backups use it from now on; older backups still restore.' }),
      h('p', { class: 'note' }, 'PBKDF2 is acceptable for a monitor that stays on your own network, but Argon2id remains the recommendation. Backups made with PBKDF2 cannot be restored by GWatch releases that predate this option.'));
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

  async function revokeKey(k, reload) {
    const ok = await confirmDialog({ title: `Revoke ${k.name}?`, message: 'Anything still using this key stops working immediately. This cannot be undone.', confirmLabel: 'Revoke key', danger: true });
    if (!ok) return;
    try { await api.del(`/api/apikeys/${k.id}`); toast('Key revoked', { kind: 'success' }); await reload(); }
    catch (e) { toast(e.message, { kind: 'error' }); }
  }

  function scopesCard() {
    const item = (t, d) => h('div', null, h('b', null, t), h('div', { class: 'muted u-fs-13' }, d));
    return h('section', { class: 'card' }, h('h2', null, 'What a key can and cannot do'),
      h('div', { class: 'stack-sm u-mt-8' },
        item('Read-only', 'Overview, status, nodes and checks, history and charts, incidents, dashboards, and the CSV exports of history, results and events.'),
        item('Read & write', 'Everything above, plus creating, changing, deleting, enabling, running and silencing nodes and checks, notes, maintenance windows, dashboards and charts.'),
        item('Never, whatever the scope', 'Settings, backups and restores, updates, user accounts, API keys, the configuration export, the service log, and anything that runs a trigger, a custom endpoint or an action on this computer.')),
      h('p', { class: 'note' }, 'That last line is deliberate: a key is for reading a monitor from elsewhere, not for administering the machine it runs on. ', h('code', null, 'docs/REMOTE-ACCESS.md'), ' covers how to reach GWatch from outside your network safely.'));
  }

  return { tabUsers };
}

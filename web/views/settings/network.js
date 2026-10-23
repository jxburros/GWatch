// Settings › Network access.
//
// Part of settings.js, split out for #118: the code is the
// view's own, moved here unchanged inside create(shell), which hands it the
// view's shared state and helpers.

import { api } from '../../api.js';
import { h, icon, clear, field, numberInput, checkbox, toggle, toast, confirmDialog, banner } from '../../components.js';

/** @param {Record<string, any>} shell the view's state and helpers */
export function create(shell) {
  const { loadSettings, saveBar, saveSettings, state } = shell;

  async function tabNetwork() {
    const s = state.settings || await loadSettings();
    const g = s.general;
    const info = await api.get('/api/network').catch(() => null);
    const port = numberInput({ value: g.listenPort || 7230, min: 1, max: 65535, oninput: () => { g.listenPort = Number(port.value); } });
    const proxy = checkbox({ label: 'GWatch is behind an HTTPS reverse proxy', checked: !!g.behindHTTPSProxy, onChange: (v) => { g.behindHTTPSProxy = v; } });
    const remote = toggle({ label: 'Allow access from other devices on my network', checked: !!g.remoteAccess, onChange: (v) => { g.remoteAccess = v; } });
    const pw = h('input', { type: 'password', value: g.accessPassword || '', autocomplete: 'new-password', placeholder: info?.passwordSet ? '(unchanged)' : 'Optional but recommended', oninput: () => { g.accessPassword = pw.value; } });
    const clearPw = h('button', { class: 'btn btn-sm', type: 'button', onclick: () => { pw.value = ''; g.accessPassword = ''; toast('Password will be removed when you save', { kind: 'info' }); } }, 'Remove password');
    const urls = h('div', { class: 'url-list' });
    const renderUrls = (ni) => {
      clear(urls);
      if (!ni) { urls.append(h('span', { class: 'muted' }, 'Network information unavailable.')); return; }
      if (ni.restartNeeded) urls.append(h('button', { type: 'button', class: 'btn', onclick: async () => { try { await api.post('/api/restart'); toast('GWatch is restarting'); } catch (e) { toast(e.message, { kind: 'error' }); } } }, 'Restart GWatch'));
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
          field({ label: 'Listen port', input: port, help: 'Changes take effect after saving. Reopen GWatch at the new port.' }),
          proxy,
          h('p', { class: 'note' }, 'Enable the proxy setting only when browsers always reach GWatch over HTTPS. It marks session cookies Secure.'),
          (g.remoteAccess || info?.remoteAccess) && (g.accessPassword || info?.passwordSet) && !g.behindHTTPSProxy && !info?.localUrl?.startsWith('https:') ? banner('warn', 'The shared access password is sent without encryption over HTTP. Use HTTPS before sending it across your network.') : null,
          info?.firewallCommand ? h('div', null, h('p', { class: 'note' }, 'Allow this port through your firewall:'), h('pre', null, info.firewallCommand)) : null,
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

  return { tabNetwork };
}

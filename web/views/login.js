// Sign-in screen. Shown full page by app.js when /api/auth/setup says this
// client needs to sign in, and again whenever any API call comes back 401.

import { api, signIn, getAuthSetup, refreshMe } from '../api.js';
import { h, icon, field, textInput, replace, busy, toast, banner } from '../components.js';

export async function mount(root, ctx) {
  const setup = await getAuthSetup().catch(() => ({}));
  const state = { destroyed: false };

  const user = textInput({ id: 'signin-user', autocomplete: 'username', autocapitalize: 'none', autocorrect: 'off', spellcheck: false, required: true, placeholder: 'Your user name' });
  const pass = h('input', { type: 'password', id: 'signin-pass', autocomplete: 'current-password', required: true, placeholder: 'Your password' });
  const message = h('div', { class: 'stack-sm', role: 'alert', 'aria-live': 'polite' });
  const submit = h('button', { class: 'btn btn-primary btn-lg', type: 'submit' }, icon('lock'), 'Sign in');

  const form = h('form', { class: 'stack', onsubmit: onSubmit },
    field({ label: 'User name', input: user }),
    field({ label: 'Password', input: pass }),
    message,
    h('div', { class: 'form-actions' }, submit));

  async function onSubmit(e) {
    e.preventDefault();
    replace(message);
    const name = user.value.trim();
    if (!name || !pass.value) {
      replace(message, banner('warn', 'Enter your user name and password.'));
      return;
    }
    const done = busy(submit, 'Signing in…');
    try {
      await signIn(name, pass.value);
      await refreshMe();
      toast('Signed in', { kind: 'success' });
      // A full reload is the simplest way to rebuild the shell — the rail, the
      // health poll and the live-update stream all start from a clean slate
      // with the new identity.
      location.hash = '#/dashboard';
      location.reload();
    } catch (err) {
      done();
      pass.value = '';
      replace(message, banner('error', err.message || 'Could not sign in.'));
      pass.focus();
    }
  }

  // A first-run install with no accounts: explain where accounts come from
  // rather than showing a form nobody can use yet.
  const intro = setup.usersConfigured
    ? h('p', { class: 'lead' }, 'Sign in with your GWatch account to see this monitor.')
    : h('p', { class: 'lead' }, 'No accounts have been created on this GWatch yet. Open it on the computer it runs on and add the first account under Settings → Users & access.');

  root.append(h('div', { class: 'signin-page' },
    h('section', { class: 'card card-lg signin-card' },
      h('div', { class: 'signin-brand' }, h('img', { class: 'brand-logo', src: 'logo.svg', alt: '', width: '34', height: '34' }), h('b', null, 'GWatch')),
      h('h1', null, 'Sign in'),
      intro,
      setup.usersConfigured ? form : null,
      setup.accessPasswordSet && !setup.usersConfigured
        ? h('p', { class: 'note' }, 'This GWatch still uses the older shared access password. Your browser will ask for it directly.')
        : null,
    )));

  if (setup.usersConfigured) user.focus();
  ctx.setTitle?.('Sign in');
  return { destroy() { state.destroyed = true; } };
}

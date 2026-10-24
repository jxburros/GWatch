// The mock always reports usersConfigured: false (see web/mock.js's
// /api/auth/setup), so the happy path here is the first-run explanation
// rather than a sign-in form — both are real states the view has to render.

import '../dom.mjs';
import '../../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as loginView from '../../../web/views/login.js';
import { mountView, waitFor } from '../view-harness.mjs';

test('login view explains the first-run state and titles the page "Sign in"', async (t) => {
  const { root, ctx } = await mountView(loginView, undefined, t);
  assert.equal(ctx.setTitleCalls.at(-1).title, 'Sign in');
  assert.equal(root.querySelector('h1').textContent, 'Sign in');
  assert.match(root.querySelector('.signin-card').textContent, /No accounts have been created/);
  assert.equal(root.querySelector('form'), null); // no accounts yet: nothing to sign in with
});

// A refused sign-in is shown as an error banner. banner('error') has no style,
// so the message used to sit in an unstyled box under an info icon.
test('a refused sign-in shows the server\'s reason in an error banner', async (t) => {
  const realFetch = window.fetch;
  const json = (status, body) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
  window.fetch = async (url, init) => {
    const u = String(url);
    if (u === '/api/auth/setup') return json(200, { usersConfigured: true, loginRequired: true });
    if (u === '/api/auth/login') return json(401, { error: 'wrong user name or password' });
    return realFetch(url, init);
  };
  t.after(() => { window.fetch = realFetch; });
  const { root } = await mountView(loginView, undefined, t);
  root.querySelector('#signin-user').value = 'pat';
  root.querySelector('#signin-pass').value = 'nope';
  root.querySelector('form').dispatchEvent(new window.Event('submit', { cancelable: true }));
  const b = await waitFor(() => root.querySelector('[role="alert"] .banner'));
  assert.ok(b.classList.contains('banner-down'));
  assert.match(b.textContent, /wrong user name or password/);
});

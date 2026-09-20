// The mock always reports usersConfigured: false (see web/mock.js's
// /api/auth/setup), so the happy path here is the first-run explanation
// rather than a sign-in form — both are real states the view has to render.

import '../dom.mjs';
import '../../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as loginView from '../../../web/views/login.js';
import { mountView } from '../view-harness.mjs';

test('login view explains the first-run state and titles the page "Sign in"', async (t) => {
  const { root, ctx } = await mountView(loginView, undefined, t);
  assert.equal(ctx.setTitleCalls.at(-1).title, 'Sign in');
  assert.equal(root.querySelector('h1').textContent, 'Sign in');
  assert.match(root.querySelector('.signin-card').textContent, /No accounts have been created/);
  assert.equal(root.querySelector('form'), null); // no accounts yet: nothing to sign in with
});

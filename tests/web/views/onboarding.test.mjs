// web/views/onboarding.js is a self-contained wizard — no network — so the
// happy path is simply "step one renders, and its progress dots agree".

import '../dom.mjs';
import '../../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as onboardingView from '../../../web/views/onboarding.js';
import { mountView } from '../view-harness.mjs';

test('onboarding view opens on step one and titles the page', async (t) => {
  const { root, ctx } = await mountView(onboardingView, undefined, t);
  assert.equal(ctx.setTitleCalls[0].title, 'Welcome to GWatch');
  assert.equal(root.querySelector('h2').textContent, 'This is GWatch');
  assert.equal(root.querySelector('.ob-count').textContent, 'Step 1 of 6');
  assert.equal(root.querySelectorAll('.ob-dot').length, 6);
});

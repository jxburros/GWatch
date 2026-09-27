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

// #79: the step about other machines is the agent's, so it wears the agent's
// own mark in place of the step icon; every other step keeps its icon.
test('onboarding hardware step shows the agent mark', async (t) => {
  const { root } = await mountView(onboardingView, undefined, t);
  assert.equal(root.querySelector('.agent-logo'), null, 'step one has no agent mark');
  for (let n = 0; n < 3; n++) root.querySelector('.ob-foot .btn-primary').click();
  assert.equal(root.querySelector('h2').textContent, 'Watch the machines themselves');
  const img = root.querySelector('.ob-step img.agent-logo');
  assert.ok(img, 'the agent mark stands in for the step icon');
  assert.equal(img.getAttribute('src'), 'agent-logo.png');
  assert.equal(img.getAttribute('alt'), '', 'decorative: the heading beside it says what it is');
  assert.equal(root.querySelector('.ob-step .ob-icon'), null);
});

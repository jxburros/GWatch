// web/views/wallboard.js shows the first configured wallboard (the mock
// seeds one — see web/mock.js's `wallboards`) using web/wall-render.js. The
// wall draws itself asynchronously after its own first fetch, which
// mount() does not wait for (it owns a refresh timer for the life of the
// view), so the test drains a couple of microtask turns before asserting.

import '../dom.mjs';
import '../../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as wallboardView from '../../../web/views/wallboard.js';
import { mountView, waitFor } from '../view-harness.mjs';

test('wallboard view draws the configured board\'s panels', async (t) => {
  const { root, ctx } = await mountView(wallboardView, undefined, t);
  await waitFor(() => root.querySelectorAll('.wall-panel, .wall-headline, .wall-counts').length > 0);
  assert.equal(ctx.setTitleCalls.at(-1).title, 'Office screen'); // web/mock.js's wallboards[0].name
});

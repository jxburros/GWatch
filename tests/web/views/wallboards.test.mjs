// web/views/wallboards.js is the wallboard editor (as opposed to
// web/views/wallboard.js, which projects one). It should list the mock's
// one configured board as a tab and render its panels/layout/share cards.

import '../dom.mjs';
import '../../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as wallboardsView from '../../../web/views/wallboards.js';
import { mountView } from '../view-harness.mjs';

test('wallboards view lists the configured board and its panel count', async (t) => {
  const { root, ctx } = await mountView(wallboardsView, undefined, t);
  assert.equal(ctx.setTitleCalls.at(-1).title, 'Wallboards');
  const tabs = [...root.querySelectorAll('[role="tab"]')].map((el) => el.textContent);
  assert.deepEqual(tabs, ['Office screen']);
  assert.ok(root.querySelector('.card[aria-label="Panels"]'), 'expected a Panels card for the selected board');
});

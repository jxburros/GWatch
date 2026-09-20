// web/views/help.js never touches the network, so this is the simplest of
// the view smoke tests: every topic renders once, and the search box filters
// the same set it started with.

import '../dom.mjs';
import '../../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as helpView from '../../../web/views/help.js';
import { mountView } from '../view-harness.mjs';

test('help view renders every topic and titles the page "Help"', async (t) => {
  const { root, ctx } = await mountView(helpView, undefined, t);
  assert.equal(ctx.setTitleCalls[0].title, 'Help');
  const topics = root.querySelectorAll('.help-topic');
  assert.ok(topics.length >= 10, 'expects a double-digit number of help topics');
  assert.equal(root.querySelector('.filter-count').textContent, `${topics.length} topics`);
});

test('help view search narrows the visible topics', async (t) => {
  const { root } = await mountView(helpView, undefined, t);
  const total = root.querySelectorAll('.help-topic').length;
  const search = root.querySelector('input[type="search"]');
  search.value = 'backup';
  search.dispatchEvent(new window.Event('input'));
  const narrowed = root.querySelectorAll('.help-topic').length;
  assert.ok(narrowed > 0 && narrowed < total);
});

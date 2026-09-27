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

// #44: the topics for this round's features are there, the tutorial is
// offered but not started, and a stored place is offered as a resume.
test('help covers the new features and offers the tutorial without starting it', async (t) => {
  localStorage.removeItem('gw.tutorial.step');
  const { root } = await mountView(helpView, undefined, t);
  for (const id of ['charts', 'hardware', 'ai-mcp', 'database']) assert.ok(root.querySelector(`#help-${id}`), `expected a ${id} topic`);
  assert.ok([...root.querySelectorAll('button')].some((b) => b.textContent.includes('Start the tutorial')));
  assert.equal(document.querySelectorAll('.tip-tutorial').length, 0, 'the tutorial never starts by itself');
});

test('help offers to resume the tutorial where it stopped', async (t) => {
  localStorage.setItem('gw.tutorial.step', '3');
  t.after(() => localStorage.removeItem('gw.tutorial.step'));
  const { root } = await mountView(helpView, undefined, t);
  assert.match(root.querySelector('.help-tutorial').textContent, /You stopped at step 4 of \d+/);
  assert.ok([...root.querySelectorAll('button')].some((b) => b.textContent.includes('Resume the tutorial')));
});

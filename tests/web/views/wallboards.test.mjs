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

test('wallboard tabs are real tabs: one panel, roving tabindex, arrow keys select', async (t) => {
  // A second board, so there is somewhere for the arrow keys to go.
  await window.fetch('/api/wallboards', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name: 'Lobby' }) });
  const { root } = await mountView(wallboardsView, undefined, t);
  const list = root.querySelector('[role="tablist"]');
  const panel = root.querySelector('[role="tabpanel"]');
  assert.ok(list && panel);
  let tabs = [...list.querySelectorAll('[role="tab"]')];
  assert.deepEqual(tabs.map((el) => el.textContent), ['Office screen', 'Lobby']);
  assert.deepEqual(tabs.map((el) => el.getAttribute('aria-selected')), ['true', 'false']);
  assert.deepEqual(tabs.map((el) => el.tabIndex), [0, -1], 'only the selected tab is in the tab order');
  assert.equal(tabs[0].getAttribute('aria-controls'), panel.id);
  assert.equal(panel.getAttribute('aria-labelledby'), tabs[0].id);

  tabs[0].focus();
  list.dispatchEvent(new window.KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true, cancelable: true }));
  tabs = [...list.querySelectorAll('[role="tab"]')];
  assert.deepEqual(tabs.map((el) => el.getAttribute('aria-selected')), ['false', 'true'], 'ArrowRight selects the next board');
  assert.equal(document.activeElement, tabs[1], 'and focuses its tab');
  assert.equal(panel.getAttribute('aria-labelledby'), tabs[1].id);
  list.dispatchEvent(new window.KeyboardEvent('keydown', { key: 'Home', bubbles: true, cancelable: true }));
  tabs = [...list.querySelectorAll('[role="tab"]')];
  assert.equal(tabs[0].getAttribute('aria-selected'), 'true', 'Home goes back to the first');
  assert.equal(document.activeElement, tabs[0]);
});

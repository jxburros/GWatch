import test from 'node:test';
import assert from 'node:assert/strict';
import { document } from './dom.mjs';

test('page action links are inside a landmark to satisfy axe region', () => {
  const actions = document.querySelector('#page-actions');
  assert.ok(actions);
  const link = document.createElement('a');
  link.href = '#/nodes/bulk';
  link.textContent = 'Bulk edit';
  actions.append(link);

  assert.ok(actions.closest('nav[aria-label="Page actions"]'),
    'axe region violation: page action links are outside a landmark');
});

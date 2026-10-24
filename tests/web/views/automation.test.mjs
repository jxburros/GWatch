// web/views/automation.js: the action editor shared by triggers, custom
// endpoints and the alert channels.

import '../dom.mjs';
import '../../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import { actionEditor } from '../../../web/views/automation.js';

// The remove button deleted the header name its row was drawn with, so once
// the name had been edited it removed nothing and the header was still sent.
test('an HTTP action header can be removed after it has been renamed', () => {
  const editor = actionEditor({ type: 'http', url: 'https://example.com/hook' });
  document.body.append(editor);
  const rows = () => editor.querySelector('.kv-rows');
  [...rows().querySelectorAll('button')].find((b) => b.textContent.includes('Add header')).click();
  const name = rows().querySelector('[aria-label="Header name"]');
  name.value = 'Authorization';
  name.dispatchEvent(new window.Event('change', { bubbles: true }));
  assert.deepEqual(editor.value.headers, { Authorization: '' });
  rows().querySelector('[aria-label="Remove header"]').click();
  assert.equal(rows().querySelectorAll('.kv-row').length, 0);
  assert.deepEqual(editor.value.headers, {});
  editor.remove();
});

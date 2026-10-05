import './dom.mjs';
import test from 'node:test';
import assert from 'node:assert/strict';
import { debounce, subscribeUpdates } from '../../web/api.js';
import { applyAccent, onThemeChange } from '../../web/components.js';
test('a sustained update stream flushes at its maximum wait', (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  let calls = 0;
  const update = debounce(() => calls++, 500, 2000);
  for (let i = 0; i < 6; i++) { update(); t.mock.timers.tick(400); }
  assert.equal(calls, 1);
  t.mock.timers.tick(500);
  assert.equal(calls, 2);
});
test('unchanged accent does not rebuild charts', () => {
  applyAccent('#123456');
  let calls = 0;
  const off = onThemeChange(() => calls++);
  try { applyAccent('#123456'); assert.equal(calls, 0); applyAccent('#654321'); assert.equal(calls, 1); } finally { off(); }
});
test('hidden tabs close the stream and visible tabs reconnect', (t) => {
  let hidden = false, opened = 0, closed = 0;
  const own = Object.getOwnPropertyDescriptor(document, 'hidden');
  Object.defineProperty(document, 'hidden', { configurable: true, get: () => hidden });
  t.after(() => { if (own) Object.defineProperty(document, 'hidden', own); else delete document.hidden; });
  const original = window.EventSource;
  window.EventSource = class { constructor() { opened++; } addEventListener() {} close() { closed++; } };
  t.after(() => { window.EventSource = original; });
  const off = subscribeUpdates(() => {});
  assert.equal(opened, 1);
  hidden = true; document.dispatchEvent(new window.Event('visibilitychange'));
  assert.equal(closed, 1);
  hidden = false; document.dispatchEvent(new window.Event('visibilitychange'));
  assert.equal(opened, 2);
  off(); assert.equal(closed, 2);
});

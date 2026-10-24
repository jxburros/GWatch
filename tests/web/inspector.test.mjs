// web/views/inspector.js draws whatever a result carries. A hardware reading
// from a metrics URL or an agent may leave out any optional figure, and the
// inspector must say less rather than throw (which took the whole node page
// with it while the result was open).

import './dom.mjs';
import test from 'node:test';
import assert from 'node:assert/strict';
import { resultInspector } from '../../web/views/inspector.js';

test('a reading with only the one-minute load average still inspects', () => {
  const result = { ts: new Date().toISOString(), success: true, status: 'up', message: 'ok', details: { host: { hostname: 'nas', cpu: { usagePct: 12, cores: 4, load1: 0.5 } } } };
  const el = resultInspector(result, { type: 'system' });
  assert.match(el.textContent, /0\.50 \/ — \/ —/);
});

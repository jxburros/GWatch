// The availability report's window is made of the reader's own calendar days.
// The zone is pinned to one far from UTC before anything reads the clock, so
// a window computed in UTC rather than local time shows up as a wrong hour.
process.env.TZ = 'America/Los_Angeles';

import '../dom.mjs';
import '../../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as view from '../../../web/views/reports.js';
import { mountView } from '../view-harness.mjs';

test('the report runs from local midnight to local midnight, and defaults to today', async (t) => {
  const { root } = await mountView(view, { isAdmin: false }, t);
  const [from, to] = root.querySelectorAll('input[type="date"]');
  const now = new Date();
  const today = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`;
  assert.equal(to.value, today, 'the end date is today here, not today in UTC');
  from.value = '2026-03-01'; to.value = '2026-03-08';
  const link = [...root.querySelectorAll('a')].find((a) => a.textContent === 'Open printable report');
  link.addEventListener('click', (e) => e.preventDefault());
  link.click();
  const q = new URL(link.href, 'http://localhost').searchParams;
  assert.equal(q.get('from'), new Date(2026, 2, 1).toISOString());
  assert.equal(q.get('to'), new Date(2026, 2, 8).toISOString());
  assert.equal(q.get('from'), '2026-03-01T08:00:00.000Z', 'midnight in Los Angeles');
});

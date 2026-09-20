// web/views/audit.js defaults to the "Event log" tab. The happy path is that
// tab rendering the mock's seeded events with all three tabs present.

import '../dom.mjs';
import '../../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as auditView from '../../../web/views/audit.js';
import { mountView } from '../view-harness.mjs';

test('audit view defaults to the event log tab and lists mock events', async (t) => {
  const { root, ctx } = await mountView(auditView, undefined, t);
  assert.equal(ctx.setTitleCalls.some((c) => c.title === 'Audit'), true);
  const tabLabels = [...root.querySelectorAll('.tab')].map((el) => el.textContent);
  assert.deepEqual(tabLabels, ['Event log', 'Service log', 'Exports']);
  assert.equal(root.querySelector('.tab.active').textContent, 'Event log');
  const rows = root.querySelectorAll('.event-row, tbody tr');
  assert.ok(rows.length > 0, 'expected the event log to list at least one mock event');
});

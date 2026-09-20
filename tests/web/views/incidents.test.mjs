// web/views/incidents.js groups events by day. The mock seeds a healthy
// handful of down/recovered/etc events (see web/mock.js), so the happy path
// is "at least one day group renders, with at least one event row in it".

import '../dom.mjs';
import '../../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as incidentsView from '../../../web/views/incidents.js';
import { mountView } from '../view-harness.mjs';

test('incidents view renders the mock event timeline and titles the page', async (t) => {
  const { root, ctx } = await mountView(incidentsView, undefined, t);
  assert.equal(ctx.setTitleCalls[0].title, 'Incidents');
  const rows = root.querySelectorAll('.event-row');
  assert.ok(rows.length > 0, 'expected at least one event row from the mock timeline');
  // The mock seeds a down event for the Gateway's ping check (see web/mock.js) —
  // it should surface somewhere in the rendered timeline text.
  assert.match(root.textContent, /Gateway/);
});

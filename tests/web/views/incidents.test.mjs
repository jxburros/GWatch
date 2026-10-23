// web/views/incident-events.js groups events by day. The mock seeds a healthy
// handful of down/recovered/etc events (see web/mock.js), so the happy path
// is "at least one day group renders, with at least one event row in it".

import '../dom.mjs';
import '../../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as incidentsView from '../../../web/views/incident-events.js';
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

// #113: a filter change is written into the address and into the Export CSV
// link, so a reload, a shared link and the download all match the screen.
test('incident event filters go into the address and the export link', async (t) => {
  window.location.hash = '#/incidents?tab=events';
  const { root, ctx } = await mountView(incidentsView, { query: new URLSearchParams('tab=events') }, t);
  const typeSel = root.querySelector('select[aria-label="Event type"]');
  typeSel.value = 'down';
  typeSel.dispatchEvent(new window.Event('change'));
  const params = new URLSearchParams(window.location.hash.split('?')[1]);
  assert.equal(params.get('type'), 'down');
  assert.equal(params.get('tab'), 'events', 'the rest of the query is kept');
  const exportLink = ctx.setTitleCalls[0].opts.actions.find((a) => a.textContent.includes('Export CSV'));
  assert.match(exportLink.getAttribute('href'), /type=down/);
  typeSel.value = '';
  typeSel.dispatchEvent(new window.Event('change'));
  assert.equal(new URLSearchParams(window.location.hash.split('?')[1]).get('type'), null, 'a cleared filter leaves the address');
});

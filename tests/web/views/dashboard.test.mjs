// web/views/dashboard.js renders the first dashboard's widgets on a 4-column
// grid. The mock's "Overview" dashboard (see web/mock.js) has a dozen
// widgets, several of which draw canvas charts — dom.mjs's canvas stub is
// what keeps those from throwing.

import '../dom.mjs';
import '../../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as dashboardView from '../../../web/views/dashboard.js';
import { mountView, settle } from '../view-harness.mjs';

test('dashboard view renders the default dashboard\'s widgets', async (t) => {
  const { root, ctx } = await mountView(dashboardView, undefined, t);
  const mockDashboard = window.__gwatchMock.dashboards[0];
  assert.equal(ctx.setTitleCalls.at(-1).title, mockDashboard.name);

  const widgets = root.querySelectorAll('.widget');
  assert.equal(widgets.length, mockDashboard.widgets.length);

  const tabNames = [...root.querySelectorAll('.dash-tabs a')].map((el) => el.textContent);
  assert.deepEqual(tabNames, window.__gwatchMock.dashboards.map((d) => d.name));

  await settle(); // chart widgets fetch their history asynchronously after first paint
});

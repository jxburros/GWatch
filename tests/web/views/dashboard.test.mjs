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

// A widget dropped below the others floats up under them instead of staying
// stranded where it was let go, and nothing overlaps afterwards.
test('resolve keeps a moved widget near the rest of the grid', () => {
  const items = [
    { id: 'a', x: 0, y: 0, w: 2, h: 1 },
    { id: 'b', x: 2, y: 0, w: 2, h: 1 },
    { id: 'c', x: 0, y: 1, w: 2, h: 2 },
  ];
  const moved = items[0];
  moved.x = 2; moved.y = 17;
  dashboardView.resolve(items, moved);
  assert.ok(moved.y <= 1, `moved widget sits at row ${moved.y}, not 17`);
  for (const p of items) for (const q of items) if (p !== q) assert.equal(dashboardView.collides(p, q), false, `${p.id} overlaps ${q.id}`);
});

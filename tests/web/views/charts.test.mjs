// web/views/charts.js picks the first saved chart (the mock seeds one — see
// web/mock.js's savedCharts) and renders it, with the saved-charts list and
// the config editor beside it.

import '../dom.mjs';
import '../../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as chartsView from '../../../web/views/charts.js';
import { mountView, settle } from '../view-harness.mjs';

test('charts view opens on the first saved chart and titles the page after it', async (t) => {
  const { root, ctx } = await mountView(chartsView, undefined, t);
  assert.equal(ctx.setTitleCalls.at(-1).title, 'Gateway latency');
  assert.equal(root.querySelectorAll('.saved-item').length, 1);
  assert.ok(root.querySelector('.chart-card'), 'expected the main chart card to render');
  await settle();
});

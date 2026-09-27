// web/views/charts.js picks the first saved chart (the mock seeds two — see
// web/mock.js's savedCharts) and renders it, with the saved-charts list and
// the config editor beside it.

import '../dom.mjs';
import '../../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as chartsView from '../../../web/views/charts.js';
import { mountView, settle, waitFor } from '../view-harness.mjs';

test('charts view opens on the first saved chart and titles the page after it', async (t) => {
  const { root, ctx } = await mountView(chartsView, undefined, t);
  assert.equal(ctx.setTitleCalls.at(-1).title, 'Gateway latency');
  assert.equal(root.querySelectorAll('.saved-item').length, 2);
  assert.ok(root.querySelector('.chart-card'), 'expected the main chart card to render');
  // The chart was saved before series existed and opens as one: its check is
  // open in the picker, with its latency ticked.
  const open = root.querySelector('details.metric-check[open]');
  assert.ok(open, 'the check the chart plots is open in the picker');
  assert.equal(open.querySelector('[data-metric="avg"] input').checked, true);
  await settle();
});

test('a saved chart mixing a latency with a machine’s readings draws two axes (#73)', async (t) => {
  const { root } = await mountView(chartsView, { params: { id: 'chart-2' } }, t);
  const legend = await waitFor(() => root.querySelectorAll('.chart-card .legend-item').length >= 3 && root.querySelectorAll('.chart-card .legend-item'));
  const text = [...legend].map((el) => el.textContent);
  assert.match(text[0], /Latency \(average\)ms · left axis$/);
  assert.match(text[1], /Processor/);
  assert.match(text[1], /% · right axis$/);
  assert.equal(root.querySelector('.chart-card .card-head .tag').textContent, '3 metrics');
});

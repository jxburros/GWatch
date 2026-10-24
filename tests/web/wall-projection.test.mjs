import './dom.mjs';
import test from 'node:test';
import assert from 'node:assert/strict';
import { createWall } from '../../web/wall-render.js';
import { freshRoot } from './dom.mjs';

// The server sends a board only what its panels draw (internal/api/wallboards.go
// projectWallboard): no summary or health when nothing shows them, and nodes as
// { node: { id, name, groups, tags }, status }. The renderer must draw that.
async function render(t, doc) {
  t.mock.method(globalThis, 'setInterval', () => 1);
  t.mock.method(globalThis, 'clearInterval', () => {});
  const root = freshRoot();
  const wall = createWall(root, async () => doc, { onError: (e) => { throw e; } });
  t.after(() => wall.destroy());
  await new Promise((r) => setTimeout(r, 0));
  return root;
}

test('wall draws a clock-only projection with no summary or health', async (t) => {
  const root = await render(t, {
    wallboard: { name: 'Clock', layout: { hideChrome: true }, panels: [{ type: 'clock', config: {} }] },
    nodes: [], groups: [], attention: [], certWarnings: [], maintenance: [], trends: [],
  });
  assert.ok(root.querySelector('.wall-clock'), 'clock drawn');
  assert.equal(root.querySelector('.wall-foot'), null);
});

test('wall draws a projected nodes panel and footer', async (t) => {
  const root = await render(t, {
    wallboard: { name: 'Lobby', layout: {}, panels: [{ type: 'nodes', config: { group: 'lobby' } }] },
    summary: { up: 1, total: 1 },
    nodes: [{ node: { id: 1, name: 'Lobby AP', group: 'lobby', groups: ['lobby'], tags: [] }, status: 'up' }],
    health: { serviceRunning: true, schedulerRunning: true, lastCheckAt: null, checksEnabled: 1 },
    groups: [], attention: [], certWarnings: [], maintenance: [], trends: [],
  });
  assert.match(root.textContent, /Lobby AP/);
  assert.match(root.textContent, /Service healthy/);
  assert.match(root.textContent, /1 nodes monitored/);
});

test('a trend with no samples in its range shows no availability, not 0.00%', async (t) => {
  const now = Date.now();
  const root = await render(t, {
    wallboard: { name: 'Trends', layout: { hideChrome: true }, panels: [{ type: 'trends', config: { range: '24h', limit: 4, checkIds: [] } }] },
    nodes: [], groups: [], attention: [], certWarnings: [], maintenance: [],
    trends: [{ checkId: 7, checkName: 'Ping', nodeName: 'Paused NAS', checkType: 'ping', from: new Date(now - 86400e3).toISOString(), to: new Date(now).toISOString(), bucketSeconds: 0, points: [], summary: { availability: 0, avgMs: null, count: 0, failures: 0 } }],
  });
  assert.equal(root.querySelector('.wc-val').textContent, '— available');
});

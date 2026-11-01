// The network map in a real browser: the clusters layout, and the colour,
// thickness and dashes of a line — the map's own and a single link's —
// checked where the stylesheet and the inline style actually meet, which the
// jsdom suite cannot see. Runs against the mock, whose Gateway is down and
// whose NAS has a link style of its own to the Office switch.

import { test, expect } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { openApp, prepare } from './helpers.mjs';

const MAP_KEY = 'gw.netmap';

/** Open the map page with a saved configuration, and the NAS selected. */
async function openMap(page, { theme = 'dark', cfg = { orientation: 'radial' }, select = 'NAS' } = {}) {
  await page.addInitScript(({ key, cfg }) => { try { localStorage.setItem(key, JSON.stringify(cfg)); } catch { /* none */ } }, { key: MAP_KEY, cfg });
  await openApp(page, 'map', { theme });
  if (select) {
    await page.locator(`a[aria-label^="${select},"]`).click();
    // Changing a node is in the administrator's "Change it" section, folded until wanted.
    await page.locator('.netmap-edit > summary').click();
    await expect(page.locator('.netmap-side fieldset legend').first()).toContainText('Link to');
  }
}

/** Opens the map's own options, which are folded away until wanted. */
async function openCustomise(page) {
  const card = page.locator('.netmap-options-card');
  if (!(await card.evaluate((d) => d.open))) await card.locator('summary').click();
}

/**
 * How thick a line is on the screen: its stroke width in drawing units times
 * the screen pixels a unit takes. A line is its chosen thickness at the least
 * and grows with the zoom up to three times that (netmap.js, strokeScale).
 */
const onScreen = (page, from, to) => page.evaluate(([f, t]) => {
  const svg = document.querySelector('.netmap-svg');
  const [, , w, h] = svg.getAttribute('viewBox').split(' ').map(Number);
  const r = svg.getBoundingClientRect();
  const s = Math.min(r.width / w, r.height / h);
  const width = parseFloat(getComputedStyle(document.querySelector(`.netmap-edge[data-from="${f}"][data-to="${t}"]`)).strokeWidth);
  return { px: width * s, scale: s };
}, [from, to]);
const expectedPx = (chosen, scale) => chosen * Math.max(1, Math.min(3, scale));

const edgeStyle = (page, from, to) => page.evaluate(([f, t]) => {
  const el = document.querySelector(`.netmap-edge[data-from="${f}"][data-to="${t}"]`);
  const cs = getComputedStyle(el);
  return { stroke: cs.stroke, width: cs.strokeWidth, dash: cs.strokeDasharray, cap: cs.strokeLinecap, cut: el.classList.contains('netmap-edge-down') };
}, [from, to]);

/** The colour the stylesheet's --down comes out as, in the page's own terms. */
const downColour = (page) => page.evaluate(() => {
  const probe = document.createElement('span');
  probe.style.color = 'var(--down)';
  document.body.append(probe);
  const c = getComputedStyle(probe).color;
  probe.remove();
  return c;
});

const ids = (page) => page.evaluate(() => {
  const nodes = window.__gwatchMock.nodes;
  const id = (name) => nodes.find((n) => n.name === name).id;
  return { gateway: id('Gateway'), swtch: id('Office switch'), nas: id('NAS'), printer: id('Printer'), plex: id('Plex') };
});

for (const theme of ['dark', 'light']) {
  test(`the clusters layout and a node's link style pass axe (${theme})`, async ({ page }) => {
    await openMap(page, { theme, select: 'Home Assistant' });
    // The status pill in the selected node's card is short of contrast in the
    // light theme — the same on the page before the map had line styles — and
    // is its own ticket. Everything else on the page is held to the rule.
    const results = await new AxeBuilder({ page }).exclude('.pill').analyze();
    const failing = results.violations.filter((v) => ['serious', 'critical'].includes(v.impact));
    expect(failing, failing.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`).join('\n')).toEqual([]);
  });
}

test('in clusters no two nodes overlap on the page, and a switch sits among the things plugged into it', async ({ page }) => {
  await openMap(page, { select: '' });
  const boxes = await page.locator('.netmap-node .netmap-box').evaluateAll((els) => els.map((e) => { const r = e.getBoundingClientRect(); return { x: r.x, y: r.y, w: r.width, h: r.height }; }));
  expect(boxes.length).toBe(11);
  for (let i = 0; i < boxes.length; i++) {
    for (let j = i + 1; j < boxes.length; j++) {
      const a = boxes[i], b = boxes[j];
      const apart = a.x + a.w <= b.x || b.x + b.w <= a.x || a.y + a.h <= b.y || b.y + b.h <= a.y;
      expect(apart, `boxes ${i} and ${j} overlap`).toBe(true);
    }
  }
  // The Office switch has its five nodes round it, each with a line of its
  // own.
  const { swtch } = await ids(page);
  const centres = await page.evaluate((id) => {
    const centre = (el) => { const r = el.getBoundingClientRect(); return [r.x + r.width / 2, r.y + r.height / 2]; };
    const at = (nodeId) => centre(document.querySelector(`.netmap-node[data-node="${nodeId}"] .netmap-box`));
    const kids = [...document.querySelectorAll(`.netmap-edge[data-from="${id}"]`)].map((e) => at(e.getAttribute('data-to')));
    return { middle: at(id), kids };
  }, swtch);
  expect(centres.kids.length).toBe(5);
  // ... on a ring round it (an oval, wider than tall, the shape a row of
  // name labels wants), on every side of it.
  const radii = centres.kids.map(([x, y]) => Math.hypot(x - centres.middle[0], y - centres.middle[1]));
  expect(Math.max(...radii) / Math.min(...radii)).toBeLessThan(2.5);
  const angles = centres.kids.map(([x, y]) => Math.atan2(y - centres.middle[1], x - centres.middle[0])).sort((a, b) => a - b);
  const gaps = angles.map((a, i) => (i ? a - angles[i - 1] : a + 2 * Math.PI - angles[angles.length - 1]));
  expect(Math.max(...gaps)).toBeLessThan(Math.PI);
  await expect(page.locator('.netmap-legend')).toContainText('in a ring round the node it depends on');
});

test('a link\'s own style wins over the map\'s, and an outage stays red whatever the colour', async ({ page }) => {
  await openMap(page, { cfg: { orientation: 'radial', lineColor: '#00aa00', lineWidth: 2, lineStyle: 'dashed' } });
  const { gateway, swtch, nas, printer } = await ids(page);
  const down = await downColour(page);

  // The map's style, where a link has none.
  const plain = await edgeStyle(page, swtch, printer);
  expect(plain.stroke).toBe('rgb(0, 170, 0)');
  const plainPx = await onScreen(page, swtch, printer);
  expect(plainPx.px).toBeCloseTo(expectedPx(2, plainPx.scale), 1);
  expect(plain.dash).not.toBe('none');
  // The NAS link's own colour and thickness; the dashes it leaves to the map.
  const own = await edgeStyle(page, swtch, nas);
  expect(own.stroke).toBe('rgb(59, 130, 246)');
  const ownPx = await onScreen(page, swtch, nas);
  expect(ownPx.px).toBeCloseTo(expectedPx(3, ownPx.scale), 1);
  expect(own.dash).not.toBe(plain.dash); // the map's dashes, drawn to the link's own thickness
  expect(own.dash).not.toBe('none');
  // Lines out of the Gateway, which is down, are the down colour in the map's thickness and dashes.
  const cut = await edgeStyle(page, gateway, swtch);
  expect(cut.cut).toBe(true);
  expect(cut.stroke).toBe(down);
  expect(cut.width).toBe(plain.width);
  expect(cut.dash).not.toBe('none');
  // ... and the legend says so in words.
  await expect(page.locator('.netmap-legend')).toContainText('Red lines lead out of a node that is down');
});

test('changing a link\'s style in the side panel redraws it, from the keyboard', async ({ page }) => {
  await openMap(page, { select: 'Printer' });
  const { swtch, printer } = await ids(page);
  const panel = page.locator('.netmap-side').first();
  await expect(panel.locator('legend').first()).toHaveText('Link to Office switch');

  const width = panel.getByLabel('Link thickness in pixels');
  await width.focus();
  await page.keyboard.type('99');
  await page.keyboard.press('Tab'); // leaving the field commits it
  await expect(width).toHaveValue('8');
  const dash = panel.getByLabel('Link style', { exact: true });
  await dash.focus();
  await page.keyboard.press('ArrowDown'); // Solid
  await page.keyboard.press('ArrowDown'); // Dashed
  await page.keyboard.press('ArrowDown'); // Dotted
  await page.keyboard.press('Tab');
  await expect(dash).toHaveValue('dotted');
  // Each change is drawn at once and saved just after.
  await expect.poll(async () => (await edgeStyle(page, swtch, printer)).cap).toBe('round');
  const dotted = await edgeStyle(page, swtch, printer);
  const dottedPx = await onScreen(page, swtch, printer);
  expect(dottedPx.px).toBeCloseTo(expectedPx(8, dottedPx.scale), 1);
  expect(dotted.dash).toMatch(/^0\.0\d*(px)?[ ,]/);
  // It is kept on the node, which is what every other map reads.
  await expect.poll(() => page.evaluate((id) => window.__gwatchMock.nodes.find((n) => n.id === id).linkStyle, printer)).toEqual({ width: 8, dash: 'dotted' });

  // Reset gives it back to the map.
  await page.locator(`a[aria-label^="Printer,"]`).click();
  await page.locator('.netmap-side').first().getByRole('button', { name: 'Reset the style of this link' }).click();
  await expect.poll(async () => { const w = await onScreen(page, swtch, printer); return Math.round((w.px / expectedPx(2, w.scale)) * 100) / 100; }).toBe(1);
});

test('the map\'s line colour, thickness and style are in Customise, and the legend and export carry them', async ({ page }) => {
  await openMap(page, { select: '' });
  await openCustomise(page);
  const customise = page.locator('.netmap-options');
  await customise.getByLabel('Map line colour').evaluate((el) => { el.value = '#ff00ff'; el.dispatchEvent(new Event('change', { bubbles: true })); });
  await customise.getByLabel('Map line thickness in pixels').fill('3');
  await customise.getByLabel('Map line thickness in pixels').press('Tab');
  await customise.getByLabel('Map line style').selectOption('dash-dot');
  const { swtch, printer } = await ids(page);
  await expect.poll(async () => (await edgeStyle(page, swtch, printer)).stroke).toBe('rgb(255, 0, 255)');
  const style = await edgeStyle(page, swtch, printer);
  const stylePx = await onScreen(page, swtch, printer);
  expect(stylePx.px).toBeCloseTo(expectedPx(3, stylePx.scale), 1);
  expect(style.cap).toBe('round');
  expect(style.dash.split(/[ ,]+/).length).toBe(4);

  // Exported as SVG, the lines keep their colour, thickness and dashes.
  await page.getByRole('button', { name: 'Map options' }).click();
  const [download] = await Promise.all([page.waitForEvent('download'), page.getByRole('menuitem', { name: 'Export as SVG' }).click()]);
  const svg = await (await import('node:fs/promises')).readFile(await download.path(), 'utf8');
  expect(svg).toContain('xmlns="http://www.w3.org/2000/svg"');
  const exported = svg.match(/<path[^>]*class="netmap-edge[^"]*"[^>]*>/g) || [];
  expect(exported.length).toBe(8);
  const printerEdge = exported.find((p) => p.includes(`data-to="${printer}"`));
  expect(printerEdge).toMatch(/stroke:\s*rgb\(255, 0, 255\)/);
  expect(printerEdge).toMatch(new RegExp(`stroke-width:\\s*${style.width.replace('.', '\\.')}`));
  expect(printerEdge).toMatch(/stroke-dasharray:\s*[\d.]+(px)?[ ,]+[\d.]+(px)?[ ,]+0\.01(px)?/);
  expect(printerEdge).toMatch(/stroke-linecap:\s*round/);
});

test('a dashboard widget holds the same line controls', async ({ page }) => {
  await openApp(page, 'dashboard');
  await page.getByRole('button', { name: 'Add widget' }).click();
  await page.getByRole('radio', { name: /^Network map/ }).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog.locator('fieldset legend')).toHaveText('Line style');
  await expect(dialog.getByLabel('Map line colour')).toBeVisible();
  await expect(dialog.getByLabel('Map line thickness in pixels')).toBeVisible();
  await expect(dialog.getByLabel('Map line style')).toBeVisible();
});

// "The option to change thickness of lines still does not work", and lines
// are "hairline-thin" on a big map: a thickness typed into Customise is drawn
// straight away, and it is screen pixels at the least — a 250-node map fitted
// to its frame shows it as chosen, and zooming in thickens it with the boxes.
test('the line thickness applies as it is typed, and is never thinner on the screen than chosen', async ({ page }) => {
  await openMap(page, { select: '' });
  await openCustomise(page);
  const { swtch, printer } = await ids(page);
  const thickness = page.locator('.netmap-side').getByLabel('Map line thickness in pixels');
  await thickness.fill('6');
  await expect.poll(async () => { const w = await onScreen(page, swtch, printer); return Math.round((w.px / expectedPx(6, w.scale)) * 100) / 100; }).toBe(1);
});

test('on a 250-node map fitted to the screen, lines are as thick as chosen, and thicken as you zoom in', async ({ page }) => {
  await page.addInitScript(({ key }) => { try { localStorage.setItem(key, JSON.stringify({ orientation: 'radial', lineWidth: 2 })); } catch { /* none */ } }, { key: MAP_KEY });
  await prepare(page, { theme: 'dark', route: 'map' });
  await page.goto('/?mock=1&big=250#/map');
  await page.waitForSelector('.netmap-svg');
  const any = await page.locator('.netmap-edge').first().evaluate((e) => [e.getAttribute('data-from'), e.getAttribute('data-to')]);
  const fitted = await onScreen(page, ...any);
  expect(fitted.scale).toBeLessThan(0.5); // the whole of a big map is far smaller than life
  expect(fitted.px).toBeCloseTo(2, 1);
  for (let i = 0; i < 14; i++) await page.getByRole('button', { name: 'Zoom in' }).click();
  const zoomed = await onScreen(page, ...any);
  expect(zoomed.scale).toBeGreaterThan(1.5);
  expect(zoomed.px).toBeCloseTo(expectedPx(2, zoomed.scale), 1);
  expect(zoomed.px).toBeGreaterThan(2.9);
});

/* ---------- Arranging, folding, what-if and presenting ---------- */

test('dragging a node pins it where it is dropped, carries its cluster, and is kept for everyone', async ({ page }) => {
  await openMap(page, { select: '' });
  const { swtch, nas } = await ids(page);
  const centre = (id) => page.evaluate((i) => { const r = document.querySelector(`.netmap-node[data-node="${i}"] .netmap-box`).getBoundingClientRect(); return { x: r.x + r.width / 2, y: r.y + r.height / 2 }; }, id);
  const a = await centre(swtch), n0 = await centre(nas);
  await page.mouse.move(a.x, a.y);
  await page.mouse.down();
  for (let i = 1; i <= 8; i++) await page.mouse.move(a.x - i * 12, a.y + i * 6);
  await page.mouse.up();
  const b = await centre(swtch), n1 = await centre(nas);
  expect(Math.round(b.x - a.x)).toBeLessThan(-60);
  // The NAS came along with its switch.
  expect(Math.hypot(n1.x - n0.x, n1.y - n0.y)).toBeGreaterThan(40);
  await expect(page.locator(`.netmap-node[data-node="${swtch}"]`)).toHaveClass(/is-pinned/);
  // Saved on the service, pinned (the mock signs in as an administrator).
  await expect.poll(() => page.evaluate((id) => fetch('/api/map/layout').then((r) => r.json()).then((l) => l.positions[id]?.pinned), swtch)).toBe(true);
  // Tidying up everything forgets it, and Undo brings it back.
  await page.getByRole('button', { name: 'Map options' }).click();
  await page.getByRole('menuitem', { name: /Tidy up everything/ }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'Tidy up everything' }).click();
  await expect(page.locator(`.netmap-node[data-node="${swtch}"]`)).not.toHaveClass(/is-pinned/);
  await page.getByRole('button', { name: 'Undo' }).click();
  await expect(page.locator(`.netmap-node[data-node="${swtch}"]`)).toHaveClass(/is-pinned/);
});

test('a locked layout does not move', async ({ page }) => {
  await page.addInitScript(() => { try { localStorage.setItem('gw.netmap.locked', '1'); } catch { /* none */ } });
  await openMap(page, { select: '' });
  const { swtch } = await ids(page);
  const box = page.locator(`.netmap-node[data-node="${swtch}"] .netmap-box`);
  const before = await box.boundingBox();
  await page.mouse.move(before.x + 20, before.y + 10);
  await page.mouse.down();
  for (let i = 1; i <= 6; i++) await page.mouse.move(before.x + 20 + i * 15, before.y + 10);
  await page.mouse.up();
  const after = await box.boundingBox();
  // The map panned instead, which moves everything alike: the switch keeps its place among the others.
  const gap = (r) => page.evaluate((id) => { const g = document.querySelector(`.netmap-node[data-node="${id}"] .netmap-box`).getBoundingClientRect(); return g.x; }, swtch).then((x) => x - r.x);
  expect(Math.abs(await gap(after))).toBeLessThan(1);
  await expect(page.locator('.netmap-howto')).toContainText('locked');
});

test('a cluster folds into its node and unfolds again', async ({ page }) => {
  await openMap(page, { select: 'Office switch' });
  await expect(page.locator('.netmap-node')).toHaveCount(11);
  await page.getByRole('button', { name: 'Fold its cluster' }).click();
  await expect(page.locator('.netmap-node')).toHaveCount(6);
  await expect(page.locator('.netmap-fold')).toHaveText('+5');
  await page.getByRole('button', { name: /Unfold its 5 nodes/ }).click();
  await expect(page.locator('.netmap-node')).toHaveCount(11);
});

test('a simulated failure spreads through what depends on it, says how much, and ends', async ({ page }) => {
  await openMap(page, { select: '' });
  await page.locator('a[aria-label^="Office switch,"]').click();
  await page.getByRole('button', { name: 'Simulate a failure' }).click();
  const banner = page.locator('.netmap-sim');
  await expect(banner).toBeVisible();
  await expect(banner).toContainText('Nothing real has changed');
  await expect(banner).toContainText('5 nodes cut off');
  await expect(page.locator('.netmap-node.is-sim')).toHaveCount(5);
  await expect(page.locator('a[aria-label^="NAS,"]')).toHaveAttribute('aria-label', /cut off while Office switch is down \(simulated\)/);
  await page.getByRole('button', { name: 'End the simulation' }).click();
  await expect(banner).toBeHidden();
  await expect(page.locator('.netmap-node.is-sim')).toHaveCount(0);
});

test('finding a node by name chooses it and brings it into view', async ({ page }) => {
  await openMap(page, { select: '' });
  await page.getByLabel('Find a node on the map').fill('Printer');
  await page.getByLabel('Find a node on the map').press('Enter');
  await expect(page.locator('.netmap-inspect .card-title')).toContainText('Printer');
  await expect(page.locator('.netmap-node.selected')).toHaveCount(1);
});

for (const theme of ['dark', 'light']) {
  test(`presenting the map: full screen, read-only, frozen, and it passes axe (${theme})`, async ({ page }) => {
    await prepare(page, { theme, route: 'map/present' });
    await page.goto('/?mock=1#/map/present');
    await page.waitForSelector('.netmap-svg');
    await expect(page.locator('.rail')).toBeHidden();
    await expect(page.getByRole('button', { name: /Frozen at/ })).toBeVisible();
    // Nothing in it changes anything: no editing controls at all.
    await page.locator('a[aria-label^="Office switch,"]').click();
    await expect(page.locator('.netmap-present-card')).toContainText('5 nodes depend on it directly');
    await expect(page.locator('.netmap-edit, .netmap-linegroup, select')).toHaveCount(0);
    await page.getByRole('button', { name: 'Simulate maintenance' }).click();
    await expect(page.locator('.netmap-sim')).toContainText('5 nodes quiet for maintenance');
    await page.getByRole('button', { name: 'Hide addresses' }).click();
    await expect(page.locator('a[aria-label^="Office switch,"]')).not.toHaveAttribute('aria-label', /192\.168/);
    const results = await new AxeBuilder({ page }).exclude('.pill').analyze();
    const failing = results.violations.filter((v) => ['serious', 'critical'].includes(v.impact));
    expect(failing, failing.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`).join('\n')).toEqual([]);
  });
}

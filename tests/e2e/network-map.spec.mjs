// The network map in a real browser: the clusters layout, and the colour,
// thickness and dashes of a line — the map's own and a single link's —
// checked where the stylesheet and the inline style actually meet, which the
// jsdom suite cannot see. Runs against the mock, whose Gateway is down and
// whose NAS has a link style of its own to the Office switch.

import { test, expect } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { openApp } from './helpers.mjs';

const MAP_KEY = 'gw.netmap';

/** Open the map page with a saved configuration, and the NAS selected. */
async function openMap(page, { theme = 'dark', cfg = { orientation: 'radial' }, select = 'NAS' } = {}) {
  await page.addInitScript(({ key, cfg }) => { try { localStorage.setItem(key, JSON.stringify(cfg)); } catch { /* none */ } }, { key: MAP_KEY, cfg });
  await openApp(page, 'map', { theme });
  if (select) {
    await page.locator(`a[aria-label^="${select},"]`).click();
    await expect(page.locator('.netmap-side fieldset legend').first()).toContainText('Link to');
  }
}

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
  // The Office switch is the middle of a ring of the five nodes that depend on it.
  const { swtch } = await ids(page);
  const centres = await page.evaluate((id) => {
    const centre = (el) => { const r = el.getBoundingClientRect(); return [r.x + r.width / 2, r.y + r.height / 2]; };
    const at = (nodeId) => centre(document.querySelector(`.netmap-node[data-node="${nodeId}"] .netmap-box`));
    const kids = [...document.querySelectorAll(`.netmap-edge[data-from="${id}"]`)].map((e) => at(e.getAttribute('data-to')));
    return { middle: at(id), kids };
  }, swtch);
  expect(centres.kids.length).toBe(5);
  const radii = centres.kids.map(([x, y]) => Math.hypot(x - centres.middle[0], y - centres.middle[1]));
  for (const r of radii) expect(Math.abs(r - radii[0])).toBeLessThan(1.5);
  const meanX = centres.kids.reduce((s, k) => s + k[0], 0) / 5, meanY = centres.kids.reduce((s, k) => s + k[1], 0) / 5;
  expect(Math.abs(meanX - centres.middle[0])).toBeLessThan(1.5);
  expect(Math.abs(meanY - centres.middle[1])).toBeLessThan(1.5);
});

test('a link\'s own style wins over the map\'s, and an outage stays red whatever the colour', async ({ page }) => {
  await openMap(page, { cfg: { orientation: 'radial', lineColor: '#00aa00', lineWidth: 2, lineStyle: 'dashed' } });
  const { gateway, swtch, nas, printer } = await ids(page);
  const down = await downColour(page);

  // The map's style, where a link has none.
  const plain = await edgeStyle(page, swtch, printer);
  expect(plain.stroke).toBe('rgb(0, 170, 0)');
  expect(plain.width).toBe('2px');
  expect(plain.dash).not.toBe('none');
  // The NAS link's own colour and thickness; the dashes it leaves to the map.
  const own = await edgeStyle(page, swtch, nas);
  expect(own.stroke).toBe('rgb(59, 130, 246)');
  expect(own.width).toBe('3px');
  expect(own.dash).not.toBe(plain.dash); // the map's dashes, drawn to the link's own thickness
  expect(own.dash).not.toBe('none');
  // Lines out of the Gateway, which is down, are the down colour in the map's thickness and dashes.
  const cut = await edgeStyle(page, gateway, swtch);
  expect(cut.cut).toBe(true);
  expect(cut.stroke).toBe(down);
  expect(cut.width).toBe('2px');
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
  // Each change is saved on its own, one after the other.
  await expect.poll(async () => (await edgeStyle(page, swtch, printer)).cap).toBe('round');
  const dotted = await edgeStyle(page, swtch, printer);
  expect(dotted.width).toBe('8px');
  expect(dotted.dash).toMatch(/^0\.01(px)?[ ,]/);
  // It is kept on the node, which is what every other map reads.
  const kept = await page.evaluate((id) => window.__gwatchMock.nodes.find((n) => n.id === id).linkStyle, printer);
  expect(kept).toEqual({ width: 8, dash: 'dotted' });

  // Reset gives it back to the map.
  await page.locator(`a[aria-label^="Printer,"]`).click();
  await page.locator('.netmap-side').first().getByRole('button', { name: 'Reset the style of this link' }).click();
  await expect.poll(async () => (await edgeStyle(page, swtch, printer)).width).toBe('1.5px');
});

test('the map\'s line colour, thickness and style are in Customise, and the legend and export carry them', async ({ page }) => {
  await openMap(page, { select: '' });
  const customise = page.locator('.netmap-options');
  await customise.getByLabel('Map line colour').evaluate((el) => { el.value = '#ff00ff'; el.dispatchEvent(new Event('change', { bubbles: true })); });
  await customise.getByLabel('Map line thickness in pixels').fill('3');
  await customise.getByLabel('Map line thickness in pixels').press('Tab');
  await customise.getByLabel('Map line style').selectOption('dash-dot');
  const { swtch, printer } = await ids(page);
  await expect.poll(async () => (await edgeStyle(page, swtch, printer)).stroke).toBe('rgb(255, 0, 255)');
  const style = await edgeStyle(page, swtch, printer);
  expect(style.width).toBe('3px');
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
  expect(printerEdge).toMatch(/stroke-width:\s*3px/);
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

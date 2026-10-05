import { test, expect } from '@playwright/test';
import { openApp } from './helpers.mjs';
test('SSE refresh retains node filter keyboard focus', async ({ page }) => {
  await openApp(page, 'nodes');
  const chip = page.locator('[data-focus-key="Group:Home Network"]');
  await chip.focus();
  await page.evaluate(async () => { const { api } = await import('/api.js'); const node = window.__gwatchMock.nodes[0]; await api.put(`/api/nodes/${node.id}`, node); });
  await page.waitForTimeout(1800);
  await expect(chip).toBeFocused();
});
for (const viewport of [{ width: 1280, height: 720 }, { width: 1920, height: 1080 }]) {
  test(`wallboard fits ${viewport.width} by ${viewport.height}`, async ({ page }) => {
    await page.setViewportSize(viewport);
    await openApp(page, 'wallboard');
    await expect(page.locator('.wall-foot')).toBeVisible();
    await expect(page.locator('.wall-group').first()).toBeVisible();
    const bounds = await page.locator('.wall').boundingBox();
    expect(bounds.y + bounds.height).toBeLessThanOrEqual(viewport.height + 1);
    const footer = await page.locator('.wall-foot').boundingBox();
    expect(footer.y + footer.height).toBeLessThanOrEqual(viewport.height + 1);
    for (const canvas of await page.locator('.wall-chart canvas').all()) { const rect = await canvas.boundingBox(); const panel = await canvas.locator('xpath=ancestor::*[contains(@class, \"wall-panel\")][1]').boundingBox(); expect(rect.y + rect.height).toBeLessThanOrEqual(panel.y + panel.height + 1); }
    await page.screenshot({ path: `test-results/wallboard-${viewport.width}.png` });
  });
}

test('SSE refresh retains dashboard link keyboard focus', async ({ page }) => {
  await openApp(page, 'dashboard');
  const link = page.locator('.attention-list a').first();
  const href = await link.getAttribute('href');
  await link.focus();
  await page.evaluate(async () => { const { api } = await import('/api.js'); const node = window.__gwatchMock.nodes[0]; await api.put(`/api/nodes/${node.id}`, node); });
  await page.waitForTimeout(1800);
  expect(await page.evaluate(() => document.activeElement.getAttribute('href'))).toBe(href);
});

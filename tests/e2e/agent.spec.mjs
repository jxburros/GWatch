// A machine's agent in a real browser: the card on its node (token, rotation,
// what it collects) and the Add node dialog that enrols a machine, in both
// themes with axe, and with the layout holding at a narrow width. Runs against
// the mock, whose NAS node is read through an agent.

import { test, expect } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { openApp } from './helpers.mjs';

const nasId = (page) => page.evaluate(() => window.__gwatchMock.nodes.find((n) => n.checks.some((c) => c.type === 'system' && c.config?.agentId === 1)).id);

async function openNas(page, theme) {
  await openApp(page, 'nodes', { theme });
  const id = await nasId(page);
  await page.evaluate((id) => { location.hash = `#/nodes/${id}`; }, id);
  await expect(page.locator('.agent-card')).toBeVisible({ timeout: 15000 });
  return id;
}

for (const theme of ['dark', 'light']) {
  test(`the agent card on a machine's node passes axe (${theme})`, async ({ page }) => {
    await openNas(page, theme);
    await page.getByRole('button', { name: 'Show token' }).click();
    await expect(page.locator('.agent-token')).toBeVisible();
    const results = await new AxeBuilder({ page }).include('.agent-card').analyze();
    expect(results.violations, JSON.stringify(results.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target) })), null, 2)).toEqual([]);
  });

  test(`the Add node picker and the agent dialog pass axe (${theme})`, async ({ page }) => {
    await openApp(page, 'nodes', { theme });
    await page.getByRole('button', { name: 'Add node' }).click();
    await expect(page.getByText('A machine with the GWatch agent')).toBeVisible();
    await page.getByText('A machine with the GWatch agent').click();
    await expect(page.getByRole('dialog', { name: 'Add a machine with the GWatch agent' })).toBeVisible();
    await page.getByText('What to monitor').click();
    const results = await new AxeBuilder({ page }).include('.modal').analyze();
    expect(results.violations, JSON.stringify(results.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target) })), null, 2)).toEqual([]);
  });
}

test('rotating a token and choosing what is collected work from the keyboard', async ({ page }) => {
  await openNas(page, 'dark');
  await page.getByRole('button', { name: /Rotate token/ }).focus();
  await page.keyboard.press('Enter');
  const dialog = page.getByRole('dialog', { name: /Rotate the token for/ });
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText('Replace now');
  await page.keyboard.press('Escape');
  await expect(dialog).toBeHidden();

  const only = page.locator('.agent-card input[id$="-interfaces-only"]');
  await only.check();
  await expect(page.locator('.agent-card [data-section="collect"]').getByText('docker0')).toBeVisible();
});

test('the agent card fits a phone', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 800 });
  await openNas(page, 'dark');
  // Judged on the card, not the page: the top bar's status indicators already
  // run past a 390px screen on every page, which is a separate matter.
  const fit = await page.evaluate(() => {
    const card = document.querySelector('.agent-card');
    const vw = document.documentElement.clientWidth;
    const over = [...card.querySelectorAll('*')].filter((el) => {
      const b = el.getBoundingClientRect();
      return b.width > 0 && b.right > vw + 1;
    }).map((el) => el.tagName.toLowerCase() + '.' + String(el.className).slice(0, 30));
    return { right: Math.round(card.getBoundingClientRect().right), vw, scrolls: card.scrollWidth - card.clientWidth, over };
  });
  expect(fit.right).toBeLessThanOrEqual(fit.vw);
  expect(fit.scrolls).toBeLessThanOrEqual(1);
  expect(fit.over, JSON.stringify(fit.over)).toEqual([]);
});

test('Settings has no Hardware section any more, and the old link lands on Nodes', async ({ page }) => {
  await openApp(page, 'settings/general');
  await expect(page.locator('.settings-nav')).not.toContainText('Hardware');
  await page.evaluate(() => { location.hash = '#/settings/hardware'; });
  await expect(page).toHaveURL(/#\/nodes$/);
});

// The Help page's "Keyboard" topic is a contract; this walks it in a real
// browser: the skip link, Escape and Tab in a dialog, Enter and Space on a
// result row, and the way out of the script box where Tab indents.

import { test, expect } from '@playwright/test';
import { FIRST_NODE, openApp, openConfirm } from './helpers.mjs';

const focused = (page) => page.evaluate(() => {
  const el = document.activeElement;
  return el ? { tag: el.tagName.toLowerCase(), cls: el.className || '', id: el.id || '', text: (el.textContent || '').trim().slice(0, 40) } : null;
});

test('Tab from the top of the page reveals "Skip to content", which moves focus past the sidebar', async ({ page }) => {
  await openApp(page, 'incidents');
  await page.keyboard.press('Tab');
  const skip = page.locator('.skip-link');
  await expect(skip).toBeFocused();
  await expect(skip).toBeVisible();
  await page.keyboard.press('Enter');
  await expect(page.locator('#view')).toBeFocused();
  // It must not read as a route: the page is still the one we were on.
  expect(page.url()).toContain('#/incidents');
});

test('Escape closes a dialog and focus goes back to where it was', async ({ page }) => {
  await openApp(page, 'nodes');
  await page.locator('#page-actions button, #page-actions a').first().focus();
  const before = await focused(page);
  await openConfirm(page);
  await expect(page.locator('#app')).toHaveAttribute('inert', '');
  await page.keyboard.press('Escape');
  await expect(page.locator('[role="dialog"]')).toHaveCount(0);
  await expect(page.locator('#app')).not.toHaveAttribute('inert', '');
  expect(await focused(page)).toEqual(before);
  expect(await page.evaluate(() => window.__confirm)).toBe(false);
});

test('Tab and Shift+Tab cycle inside a dialog and never leave it', async ({ page }) => {
  await openApp(page, 'nodes');
  await openConfirm(page);
  const dialog = page.locator('[role="dialog"]');
  await expect(dialog).toHaveAttribute('aria-labelledby', /.+/);
  await expect(dialog.locator('h2')).toHaveText('Remove this?');
  const inDialog = () => page.evaluate(() => !!document.activeElement?.closest('[role="dialog"]'));
  // The first focusable thing in the body or footer takes focus on open.
  await expect.poll(inDialog).toBe(true);
  const seen = new Set();
  for (let i = 0; i < 8; i++) {
    await page.keyboard.press('Tab');
    expect(await inDialog(), `Tab press ${i + 1} left the dialog`).toBe(true);
    seen.add(JSON.stringify(await focused(page)));
  }
  expect(seen.size).toBe(3); // close (×), Cancel, Remove — and round again
  for (let i = 0; i < 4; i++) {
    await page.keyboard.press('Shift+Tab');
    expect(await inDialog(), `Shift+Tab press ${i + 1} left the dialog`).toBe(true);
  }
  await page.keyboard.press('Escape');
});

test('Enter or Space on a result row opens and closes its detail', async ({ page }) => {
  await openApp(page, `nodes/${FIRST_NODE}`);
  await page.getByRole('button', { name: 'Inspect last result' }).first().click();
  const toggle = page.locator('.row-toggle').first();
  await toggle.waitFor();
  await expect(toggle).toHaveAttribute('aria-expanded', 'false');
  await toggle.focus();
  await page.keyboard.press('Enter');
  await expect(toggle).toHaveAttribute('aria-expanded', 'true');
  const detailId = await toggle.getAttribute('aria-controls');
  await expect(page.locator(`#${detailId}`)).toBeVisible();
  await expect(toggle).toBeFocused();
  await page.keyboard.press('Space');
  await expect(toggle).toHaveAttribute('aria-expanded', 'false');
  await expect(page.locator(`#${detailId}`)).toHaveCount(0);
});

test('in the script box Tab indents, and Esc then Tab (or Shift+Tab) leaves', async ({ page }) => {
  await openApp(page, 'settings/automation');
  await page.evaluate(async () => {
    const { openEndpointEditor } = await import('/views/automation.js');
    openEndpointEditor(null, { nodes: [] });
  });
  const dialog = page.locator('[role="dialog"]');
  await dialog.waitFor();
  await dialog.getByLabel('Action', { exact: true }).selectOption('script');
  const code = dialog.locator('textarea.code');
  await code.waitFor();
  await expect(code).toHaveAttribute('aria-describedby', /.+/);
  await code.focus();
  await page.keyboard.press('Tab');
  await expect(code).toBeFocused();
  await expect(code).toHaveValue('  ');
  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(1, { timeout: 500 }); // Escape in the box does not close the dialog by itself…
  await page.keyboard.press('Tab');
  await expect(code).not.toBeFocused();
  expect(await page.evaluate(() => !!document.activeElement?.closest('[role="dialog"]'))).toBe(true);
  await code.focus();
  await page.keyboard.press('Shift+Tab');
  await expect(code).not.toBeFocused();
  await code.focus();
  await page.keyboard.press('Tab');
  await expect(code).toHaveValue('    '); // …and the next plain Tab indents again
});

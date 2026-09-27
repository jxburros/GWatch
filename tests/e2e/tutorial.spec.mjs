// The opt-in tutorial (#44): it starts only from Help, walks from page to
// page pointing at real controls, pauses on Escape or when the person goes
// somewhere else, and Help then offers to carry on from the same step.

import { test, expect } from '@playwright/test';
import { openApp } from './helpers.mjs';

test('the tutorial never appears by itself', async ({ page }) => {
  await openApp(page, 'dashboard');
  await page.waitForTimeout(1500);
  await expect(page.locator('.tip-tutorial')).toHaveCount(0);
});

test('the tutorial walks the pages, pauses, and resumes from Help', async ({ page }) => {
  await openApp(page, 'help');
  // Keep the pointer off the sidebar, which widens over the page on hover.
  await page.mouse.move(900, 500);
  await page.getByRole('button', { name: 'Start the tutorial' }).click();

  const step = page.locator('.tip-tutorial');
  await expect(step).toBeVisible({ timeout: 8000 });
  await expect(page).toHaveURL(/#\/dashboard$/);
  await expect(step.locator('.tip-badge')).toHaveText(/Tutorial · 1 of \d+/);
  await expect(page.locator('#indicators')).toHaveClass(/tip-target/);
  // Focus starts on Next, so Enter moves on.
  await expect(step.getByRole('button', { name: 'Next' })).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(step.locator('.tip-badge')).toHaveText(/2 of/, { timeout: 8000 });

  // Next again goes to another page, and the step follows it there.
  await step.getByRole('button', { name: 'Next' }).click();
  await expect(page).toHaveURL(/#\/nodes$/);
  await expect(page.locator('.tip-tutorial .tip-badge')).toHaveText(/3 of/, { timeout: 8000 });
  await page.locator('.tip-tutorial').getByRole('button', { name: 'Back' }).click();
  await expect(page).toHaveURL(/#\/dashboard$/);
  await expect(page.locator('.tip-tutorial .tip-badge')).toHaveText(/2 of/, { timeout: 8000 });

  // Escape pauses it where it is.
  await page.keyboard.press('Escape');
  await expect(page.locator('.tip-tutorial')).toHaveCount(0);

  await page.goto('/?mock=1#/help');
  await expect(page.getByText('You stopped at step 2 of')).toBeVisible({ timeout: 8000 });
  await page.mouse.move(900, 500);
  await page.getByRole('button', { name: 'Resume the tutorial' }).click();
  await expect(page).toHaveURL(/#\/dashboard$/);
  await expect(page.locator('.tip-tutorial .tip-badge')).toHaveText(/2 of/, { timeout: 8000 });

  // Wandering off to another page pauses it rather than dragging you back.
  await page.goto('/?mock=1#/incidents');
  await page.waitForTimeout(1500);
  await expect(page.locator('.tip-tutorial')).toHaveCount(0);
});

test('#/help?topic= opens on that topic', async ({ page }) => {
  await openApp(page, 'help?topic=hardware');
  await expect(page.locator('#help-hardware')).toBeInViewport({ timeout: 5000 });
});

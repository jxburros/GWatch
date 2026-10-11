// Extra screenshots of the real GWatch interface for the 60-second video.
//
//   npm ci                                   # once, from the repository root
//   node marketing/video/screenshots.mjs     # writes marketing/video/screens/*.png
//
// Like marketing/beta/screenshots.mjs (whose dashboard, map, node, charts and wallboard
// shots the video also uses), it serves web/ and opens each page with ?mock=1, the
// interface's built-in demo network, then removes the "Mock data" banner.
import { spawn } from 'node:child_process';
import { mkdir } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from '@playwright/test';

const here = dirname(fileURLToPath(import.meta.url));
const root = join(here, '..', '..');
const out = join(here, 'screens');
const port = 4181;
const base = `http://127.0.0.1:${port}`;

// [file, route, action run before the shot]
const PAGES = [
  ['automation', 'settings/automation', null],
  ['discovery', 'nodes', async (page) => {
    await page.getByRole('button', { name: 'Discover', exact: true }).first().click();
    await page.waitForTimeout(800);
    await page.locator('.modal textarea, dialog textarea').first().fill('192.168.1.0/24');
    await page.getByRole('button', { name: 'Start scan' }).click();
    await page.waitForTimeout(3500); // the demo scan takes two seconds
  }],
];

const server = spawn(process.execPath, [join(root, 'tests/e2e/serve.mjs')], { env: { ...process.env, PORT: String(port) }, stdio: 'ignore' });
await new Promise((r) => setTimeout(r, 800));
await mkdir(out, { recursive: true });
const browser = await chromium.launch({ executablePath: process.env.CHROMIUM || '/opt/pw-browsers/chromium' }).catch(() => chromium.launch());
try {
  const ctx = await browser.newContext({ viewport: { width: 1600, height: 1000 }, deviceScaleFactor: 1.5 });
  await ctx.addInitScript(() => {
    localStorage.setItem('gw.theme', 'dark');
    localStorage.setItem('gw.tips.enabled', '0');
    localStorage.setItem('gw.onboarding.done', '1');
  });
  for (const [name, route, action] of PAGES) {
    const page = await ctx.newPage(); // a fresh page, so no dialog carries over
    await page.goto(`${base}/?mock=1#/${route}`);
    await page.waitForFunction(() => {
      const v = document.getElementById('view');
      return v && v.children.length > 0 && !v.querySelector('.skeleton, .skeleton-block');
    }, null, { timeout: 15000 });
    await page.waitForTimeout(1500);
    if (action) await action(page);
    await page.evaluate(() => document.getElementById('gwatch-mock-banner')?.remove());
    await page.waitForTimeout(200);
    await page.screenshot({ path: join(out, `${name}-dark.png`), clip: { x: 0, y: 26, width: 1600, height: 974 } });
    console.log('wrote', `screens/${name}-dark.png`);
    await page.close();
  }
  await ctx.close();
} finally {
  await browser.close();
  server.kill();
}

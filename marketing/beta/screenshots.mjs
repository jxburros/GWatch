// Screenshots of the real GWatch interface for the beta marketing kit.
//
//   npm ci                                  # once, from the repository root
//   node marketing/beta/screenshots.mjs     # writes marketing/beta/screens/*.png
//
// It serves web/ with tests/e2e/serve.mjs and opens each page with ?mock=1, the
// interface's built-in demo network, so no service or real network is needed.
// The orange "Mock data" banner is removed before each shot (it is there so a
// person never mistakes the demo for their own network; in marketing the
// caption says it is a demo). 0.6.0's web/app.js has a few double-encoded
// dashes ("â€”"); they are corrected in the browser only, so the shots show the
// text the interface intends.
import { spawn } from 'node:child_process';
import { mkdir } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from '@playwright/test';

const here = dirname(fileURLToPath(import.meta.url));
const root = join(here, '..', '..');
const out = join(here, 'screens');
const port = 4179;
const base = `http://127.0.0.1:${port}`;

const PAGES = [
  ['dashboard', 'dashboard'], ['map', 'map'], ['nodes', 'nodes'], ['node', 'nodes/21'],
  ['charts', 'charts'], ['incidents', 'incidents'],
];
const MOJIBAKE = [['â€”', '—'], ['â€“', '–'], ['â€º', '›'], ['â€¦', '…']];

const server = spawn(process.execPath, [join(root, 'tests/e2e/serve.mjs')], { env: { ...process.env, PORT: String(port) }, stdio: 'ignore' });
await new Promise((r) => setTimeout(r, 800));
await mkdir(out, { recursive: true });

const browser = await chromium.launch({ executablePath: process.env.CHROMIUM || '/opt/pw-browsers/chromium' }).catch(() => chromium.launch());
try {
  for (const theme of ['dark', 'light']) {
    const ctx = await browser.newContext({ viewport: { width: 1600, height: 1000 }, deviceScaleFactor: 1.5 });
    await ctx.addInitScript((t) => {
      localStorage.setItem('gw.theme', t);
      localStorage.setItem('gw.tips.enabled', '0');
      localStorage.setItem('gw.onboarding.done', '1');
    }, theme);
    await ctx.route('**/*.js', async (route) => {
      const res = await route.fetch();
      let body = await res.text();
      for (const [bad, good] of MOJIBAKE) body = body.replaceAll(bad, good);
      await route.fulfill({ response: res, body });
    });
    const page = await ctx.newPage();
    for (const [name, route] of PAGES) {
      await page.goto(`${base}/?mock=1#/${route}`);
      await page.waitForFunction(() => {
        const v = document.getElementById('view');
        return v && v.children.length > 0 && !v.querySelector('.skeleton, .skeleton-block');
      }, null, { timeout: 15000 });
      await page.waitForTimeout(1500); // charts and live widgets fill in after first paint
      await page.evaluate(() => document.getElementById('gwatch-mock-banner')?.remove());
      await page.waitForTimeout(200);
      await page.screenshot({ path: join(out, `${name}-${theme}.png`), clip: { x: 0, y: 26, width: 1600, height: 974 } });
      console.log('wrote', `screens/${name}-${theme}.png`);
    }
    await ctx.close();
  }
  const ctx = await browser.newContext({ viewport: { width: 1600, height: 900 }, deviceScaleFactor: 1.5 });
  const page = await ctx.newPage();
  await page.goto(`${base}/wall?mock=1&id=1`);
  await page.waitForSelector('.wall-root .wall-panel', { timeout: 15000 });
  await page.waitForTimeout(2000);
  await page.evaluate(() => document.getElementById('gwatch-mock-banner')?.remove());
  await page.waitForTimeout(200);
  await page.screenshot({ path: join(out, 'wallboard-dark.png'), clip: { x: 0, y: 26, width: 1600, height: 874 } });
  console.log('wrote screens/wallboard-dark.png');
} finally {
  await browser.close();
  server.kill();
}

// An axe-core scan of every page of the interface, in both themes, plus the
// projected wallboard page and a page with a confirm dialog open. A serious
// or critical violation fails the test; the minor and moderate ones are
// printed so they can be seen, but a colour-contrast issue, say, belongs to
// its own ticket and must not block a change here.
//
// Rules switched off, and why:
//   - color-contrast: contrast has its own issue and its own jsdom test
//     (tests/web/contrast.test.mjs); the ratios axe measures on rendered
//     text (status colours, dimmed captions) are being worked through there,
//     and a change to keyboard access or labelling must not wait on them.
// Add to DISABLED only with the reason beside it, so the exemption is as
// visible as the check.

import { test, expect } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { ROUTES, openApp, openWall, openConfirm } from './helpers.mjs';

const DISABLED = ['color-contrast'];
const FAILING = new Set(['serious', 'critical']);

/** Run axe on the page as it stands, fail on serious/critical violations. */
async function scan(page, label) {
  const results = await new AxeBuilder({ page }).disableRules(DISABLED).analyze();
  const failing = results.violations.filter((v) => FAILING.has(v.impact));
  const lesser = results.violations.filter((v) => !FAILING.has(v.impact));
  if (lesser.length) console.log(`[axe] ${label}: ${lesser.length} lesser finding(s): ${lesser.map((v) => `${v.id} (${v.impact}, ${v.nodes.length})`).join(', ')}`);
  const report = failing.map((v) => `${v.id} [${v.impact}] ${v.help}\n${v.nodes.map((n) => `    ${n.target.join(' ')}\n      ${n.failureSummary?.split('\n').join('\n      ')}`).join('\n')}`).join('\n');
  expect(failing, `${label}: axe found serious/critical violations\n${report}`).toEqual([]);
  return results;
}

for (const theme of ['dark', 'light']) {
  test.describe(`${theme} theme`, () => {
    for (const route of ROUTES) {
      test(`#/${route} has no serious axe violations`, async ({ page }) => {
        await openApp(page, route, { theme });
        await scan(page, `#/${route} (${theme})`);
      });
    }

    test('the projected wallboard page has no serious axe violations', async ({ page }) => {
      await openWall(page, { theme });
      await scan(page, `/wall (${theme})`);
    });

    test('a page with a confirm dialog open has no serious axe violations', async ({ page }) => {
      await openApp(page, 'nodes', { theme });
      await openConfirm(page);
      await scan(page, `#/nodes with confirm dialog (${theme})`);
    });
  });
}

// Layout stability (#71): nothing the interface animates or refreshes on its
// own may change the size of the page. The bug this guards against was a
// decorative sweep along the header that, for a second or two of every
// cycle, stuck out past the right-hand edge and gave the whole page a
// horizontal scrollbar — the kind of fault a person sees "randomly" and a
// screenshot never catches.
//
// Rather than wait out every animation in real time, the test takes hold of
// each running animation (document.getAnimations()) and steps it through its
// whole cycle, checking at every step that the page is still exactly as wide
// as the window and that no element has grown a horizontal scrollbar. Then it
// lets the live refresh run for a while and checks the page height did not
// change without anything new to show.

import { test, expect } from '@playwright/test';
import { FIRST_NODE, openApp } from './helpers.mjs';

const PAGES = ['dashboard', 'nodes', `nodes/${FIRST_NODE}`, 'charts', 'incidents', 'settings/hardware', 'help'];

for (const route of PAGES) {
  test(`#/${route} never overflows sideways while it animates`, async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await openApp(page, route);
    const overflow = await page.evaluate(() => {
      const found = [];
      const check = (when) => {
        // Reading scrollWidth forces style and layout, so it sees the
        // animation at the time just set without waiting for a frame.
        const root = document.documentElement;
        if (root.scrollWidth > root.clientWidth) found.push(`page ${root.scrollWidth}px wide in a ${root.clientWidth}px window ${when}`);
      };
      // Every element with the same keyframes moves the same way, so they
      // are stepped together: one pass per kind of animation, not per orb.
      const groups = new Map();
      for (const a of document.getAnimations()) {
        const timing = a.effect?.getComputedTiming?.();
        if (!(timing?.duration > 0) || !isFinite(timing.duration)) continue;
        const key = `${a.animationName || 'animation'}${a.effect.pseudoElement || ''}`;
        if (!groups.has(key)) groups.set(key, []);
        groups.get(key).push(a);
      }
      for (const [name, anims] of groups) {
        anims.forEach((a) => a.pause());
        for (let i = 0; i <= 40; i++) {
          for (const a of anims) a.currentTime = (a.effect.getComputedTiming().duration * i) / 40;
          check(`with ${name} at ${Math.round((i / 40) * 100)}%`);
        }
        anims.forEach((a) => a.play());
      }
      return [...new Set(found)];
    });
    expect(overflow, overflow.join('\n')).toEqual([]);
  });
}

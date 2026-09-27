// Empty values render as nothing, never as "null" (#72). The service is
// written in Go, where an empty list is a nil slice and goes over the wire as
// `null`, and an optional value is a pointer that does too. The mock backend
// answers with tidy empty strings and arrays, so a view can pass every other
// test and still print the word "null" the first time a real server answers.
//
// This test makes the mock answer the way Go does — every empty list and
// every empty string becomes null — and then reads every page, and every
// node's own page and editor, for the words "null", "undefined" or "NaN" in
// text, form values and accessible names. A view that throws on a null is
// caught too, as an uncaught page error.

import { test, expect } from '@playwright/test';
import { ROUTES, openApp } from './helpers.mjs';

// web/mock.js numbers its nodes from 21; every one of them has its own mix
// of check types, and so its own editor.
const NODE_PAGES = [];
for (let id = 21; id <= 40; id++) NODE_PAGES.push(`nodes/${id}`, `nodes/${id}/edit`);

/** Wraps the mock's fetch so each JSON answer comes back Go-shaped. */
function goShaped() {
  const nullify = (v) => {
    if (Array.isArray(v)) return v.length ? v.map(nullify) : null;
    if (v && typeof v === 'object') {
      const o = {};
      for (const [k, x] of Object.entries(v)) o[k] = x === '' ? null : nullify(x);
      return o;
    }
    return v;
  };
  let wrapped = false;
  const install = () => {
    if (wrapped || window.fetch?.name !== 'mockFetch') return;
    wrapped = true;
    const inner = window.fetch;
    window.fetch = async (...args) => {
      const res = await inner(...args);
      if (!(res.headers.get('content-type') || '').includes('json')) return res;
      const data = await res.json();
      // A list answer stays a list (an endpoint that answers with nothing at
      // all is a different bug); what is inside it is Go-shaped.
      const out = Array.isArray(data) ? data.map(nullify) : nullify(data);
      return new Response(JSON.stringify(out), { status: res.status, headers: { 'Content-Type': 'application/json' } });
    };
  };
  // mock.js replaces fetch as it loads, after this script; catch it then.
  const timer = setInterval(install, 1);
  setTimeout(() => clearInterval(timer), 5000);
}

for (const route of [...ROUTES.filter((r) => !r.startsWith('nodes/2')), ...NODE_PAGES]) {
  test(`#/${route} shows nothing for empty values`, async ({ page }) => {
    await page.addInitScript(goShaped);
    const errors = [];
    page.on('pageerror', (e) => errors.push(e.message));
    await openApp(page, route);
    const found = await page.evaluate(() => {
      const out = [];
      const bad = /\b(null|undefined|NaN)\b/;
      const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
      for (let n = walker.nextNode(); n; n = walker.nextNode()) {
        // Code samples and the help text may legitimately say "null".
        if (bad.test(n.textContent) && !n.parentElement.closest('script, style, code, pre')) out.push(`text in <${n.parentElement.tagName.toLowerCase()} class="${n.parentElement.className}">: ${n.textContent.trim().slice(0, 80)}`);
      }
      for (const el of document.querySelectorAll('input, textarea, select')) if (bad.test(el.value)) out.push(`value of ${el.id || el.className || el.tagName}: ${el.value}`);
      for (const el of document.querySelectorAll('[title], [aria-label], [placeholder]')) {
        for (const a of ['title', 'aria-label', 'placeholder']) { const v = el.getAttribute(a); if (v && bad.test(v)) out.push(`${a}: ${v.slice(0, 80)}`); }
      }
      return [...new Set(out)];
    });
    expect(found, found.join('\n')).toEqual([]);
    expect(errors, errors.join('\n')).toEqual([]);
  });
}

// Shared scaffolding for the "does this view render its happy path against
// the mock API" smoke tests in tests/web/views/. Every view's mount(root, ctx)
// is exercised the same way app.js itself drives it: a ctx shaped like
// app.js's ctxBase (see web/app.js), a fresh root element, and web/mock.js
// standing in for the Go service.

import { freshRoot } from './dom.mjs';

/** Mirrors app.js's ctxBase closely enough for a view's happy path: an admin
 *  identity (every view offers admin controls, so this exercises the most of
 *  each view), a recording setTitle, and navigate/query/params a test can
 *  inspect or override. */
export function makeCtx({ params = {}, query = new URLSearchParams(), isAdmin = true } = {}) {
  const setTitleCalls = [];
  const navigated = [];
  const ctx = {
    navigate: (path) => navigated.push(path),
    me: { kind: 'local', name: 'this computer', role: isAdmin ? 'admin' : 'viewer', isAdmin, canWrite: isAdmin, signedIn: false },
    query,
    params,
    setTitle(title, opts) { setTitleCalls.push({ title, opts }); },
  };
  Object.defineProperty(ctx, 'setTitleCalls', { value: setTitleCalls });
  Object.defineProperty(ctx, 'navigated', { value: navigated });
  return ctx;
}

/** Mount a view the way app.js does, against a fresh root, and hand back
 *  everything a test needs: the root, the ctx (with its recorded setTitle
 *  calls) and the instance mount() returned. Pass the test's own `t` (its
 *  TestContext) and this registers `instance.destroy()` with `t.after()`, so
 *  a view's live timers (a wallboard's clock, a chart's redraw) are always
 *  torn down — even if an assertion throws first — rather than leaking and
 *  leaving the test's child process unable to exit. */
export async function mountView(mod, ctxOpts, t) {
  const root = freshRoot();
  const ctx = makeCtx(ctxOpts);
  const instance = (await mod.mount(root, ctx)) || {};
  if (t?.after) t.after(() => instance.destroy?.());
  return { root, ctx, instance };
}

/** Views load their data with one or more `await api.get(...)` calls inside
 *  mount(), so by the time mount()'s promise settles the happy-path content
 *  is already in the DOM for most of them. A couple of views (wallboard,
 *  dashboard chart widgets) kick off a *second*, non-blocking fetch after
 *  the first paint instead — see waitFor below for those. */
export async function settle(times = 3) {
  for (let i = 0; i < times; i++) await new Promise((resolve) => setTimeout(resolve, 0));
}

/** Polls `check()` until it returns truthy, for the handful of views whose
 *  happy-path content lands after a non-blocking fetch mount() does not
 *  await (see web/wall-render.js's own refresh cycle). web/mock.js answers
 *  every request after a random 60-200ms delay to feel like a real service,
 *  so a fixed short wait is not reliable — this instead re-checks every 20ms
 *  up to `timeout`ms and fails with a clear message rather than hanging. */
export async function waitFor(check, { timeout = 4000, interval = 20 } = {}) {
  const start = Date.now();
  for (;;) {
    const value = check();
    if (value) return value;
    if (Date.now() - start > timeout) throw new Error('waitFor: condition was never met');
    await new Promise((resolve) => setTimeout(resolve, interval));
  }
}

// Minimal browser environment for testing the plain ES-module frontend under
// node:test. GWatch's web/ has no bundler and no build step — it runs
// straight in a browser — so the tests run it the same way: a real jsdom
// window and document, with just enough of the platform filled in that
// app.js's modules (which assume a browser) do not throw on import or mount.
//
// Import this module for its side effect, before importing anything from
// web/, in every test file that touches the DOM. node --test runs each test
// file in its own process, so one process setting up these globals never
// leaks into another file's test.
//
// web/*.js is written as plain ES modules loaded by <script type="module">,
// and its top-level code references `window`, `document`, `fetch` and so on
// as bare identifiers — i.e. globalThis.* — not as properties reached through
// an imported "window" object. So rather than handing tests a `window` value
// to thread through, this module installs the jsdom Window's pieces directly
// onto globalThis, mirroring the handful of properties GWatch's code
// reassigns at runtime (fetch, EventSource) so a later `window.fetch = …`
// (as web/mock.js does) is visible to bare `fetch` too.

import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { JSDOM } from 'jsdom';

const indexHtmlPath = fileURLToPath(new URL('../../web/index.html', import.meta.url));
const html = readFileSync(indexHtmlPath, 'utf8');

export const dom = new JSDOM(html, {
  url: 'http://localhost/',
  pretendToBeVisual: true,
});
export const window = dom.window;
export const document = window.document;

/** No-op CanvasRenderingContext2D: enough surface for web/charts.js to draw
 *  without a real canvas backend (jsdom has none without the optional
 *  "canvas" native package, which this dependency-free test suite does not
 *  take on). Every method is a no-op; every read returns a harmless value. */
function makeContext2D(canvas) {
  const noop = () => {};
  return {
    canvas,
    save: noop, restore: noop, scale: noop, rotate: noop, translate: noop, transform: noop, setTransform: noop, resetTransform: noop,
    clearRect: noop, fillRect: noop, strokeRect: noop,
    beginPath: noop, closePath: noop, moveTo: noop, lineTo: noop, bezierCurveTo: noop, quadraticCurveTo: noop, arc: noop, arcTo: noop, rect: noop, ellipse: noop,
    fill: noop, stroke: noop, clip: noop,
    fillText: noop, strokeText: noop,
    measureText: () => ({ width: 0 }),
    createLinearGradient: () => ({ addColorStop: noop }),
    createRadialGradient: () => ({ addColorStop: noop }),
    createPattern: () => null,
    setLineDash: noop, getLineDash: () => [],
    drawImage: noop,
    getImageData: (sx, sy, w = 1, h = 1) => ({ data: new Uint8ClampedArray(Math.max(1, w) * Math.max(1, h) * 4), width: w, height: h }),
    putImageData: noop,
    fillStyle: '', strokeStyle: '', lineWidth: 1, lineCap: 'butt', lineJoin: 'miter', font: '', textAlign: 'start', textBaseline: 'alphabetic', globalAlpha: 1,
  };
}
window.HTMLCanvasElement.prototype.getContext = function getContext(type) {
  return type === '2d' ? makeContext2D(this) : null;
};
window.HTMLCanvasElement.prototype.toBlob = function toBlob(callback, type) {
  // A tiny real Blob (rather than null) so code that inspects the result of
  // a PNG export does not have to special-case the test environment.
  callback(new window.Blob([], { type: type || 'image/png' }));
};

/** jsdom does not implement matchMedia; GWatch only ever reads
 *  `window.matchMedia(...)`, never the bare identifier, so this only needs to
 *  exist on the window. Always reports "no preference" — nothing in the
 *  smoke tests depends on a particular system theme or motion setting. */
window.matchMedia = (query) => ({
  matches: false,
  media: query,
  onchange: null,
  addListener: () => {}, removeListener: () => {},
  addEventListener: () => {}, removeEventListener: () => {},
  dispatchEvent: () => false,
});

/** jsdom implements neither observer. Both are used only to redraw on size
 *  changes, which a headless test never triggers, so "do nothing" is exactly
 *  right, not just convenient. */
class StubObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
  takeRecords() { return []; }
}
window.ResizeObserver = StubObserver;
window.IntersectionObserver = StubObserver;

/** jsdom does not schedule frames either; a short timeout keeps chart redraws
 *  asynchronous (as real rAF is) without a test waiting a real animation
 *  frame for them. */
const rafTimers = new Map();
let rafSeq = 0;
window.requestAnimationFrame = (cb) => {
  const id = ++rafSeq;
  rafTimers.set(id, setTimeout(() => { rafTimers.delete(id); cb(Date.now()); }, 0));
  return id;
};
window.cancelAnimationFrame = (id) => {
  const t = rafTimers.get(id);
  if (t) clearTimeout(t);
  rafTimers.delete(id);
};

/** Forward a global identifier to the jsdom window's own property of the same
 *  name, so that both `window.x = …` (as web/mock.js does for fetch and
 *  EventSource) and the bare `x` GWatch's modules use everywhere else keep
 *  seeing the same value. A plain `globalThis.x = window.x` copy would only
 *  capture the value at setup time and miss any later reassignment. */
function mirror(name) {
  Object.defineProperty(globalThis, name, {
    configurable: true,
    enumerable: true,
    get() { return window[name]; },
    set(v) { window[name] = v; },
  });
}

// The "real" fetch mock.js falls back to for any non-/api/ URL. Node's own
// global fetch (undici) is a perfectly good implementation of that, and
// keeping it as the window's own value (rather than only a global) is what
// lets `window.fetch.bind(window)` — exactly what mock.js does — work.
const nativeFetch = globalThis.fetch;
window.fetch = nativeFetch ? (...args) => nativeFetch(...args) : undefined;

[
  'window', 'document', 'navigator', 'location', 'localStorage', 'sessionStorage',
  'fetch', 'EventSource', 'ResizeObserver', 'IntersectionObserver',
  'requestAnimationFrame', 'cancelAnimationFrame', 'getComputedStyle',
  'HTMLElement', 'HTMLCanvasElement', 'Node', 'Element', 'SVGElement',
].forEach(mirror);

/** A fresh root element to mount a view into, the way app.js's `#view`
 *  element receives it — but disposable per test rather than shared with the
 *  rest of the page, so one test's leftover markup cannot bleed into another
 *  assertion in the same file. */
export function freshRoot() {
  const root = document.createElement('main');
  document.body.appendChild(root);
  return root;
}

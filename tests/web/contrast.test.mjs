// A WCAG contrast pass over web/app.css's colour tokens (issue #37).
//
// This test does not load a browser: it reads app.css as text, pulls the
// declarations out of the `:root { … }` and `:root[data-theme="light"] { … }`
// blocks with a couple of small regexes, resolves each token to an opaque
// sRGB colour (following `var(...)`, `rgb(var(--x-rgb))`, `rgba(r,g,b,a)`
// alpha-composited onto a caller-given background, and the one
// `color-mix(in srgb, var(--accent) N%, white|black)` pattern the sheet
// actually uses for --accent-hover), and checks a declared table of
// foreground/background pairs against the WCAG 2.x contrast ratio each pair
// needs to clear. No dependencies — node:test and node:assert only, so it
// runs as `node --test tests/web/contrast.test.mjs` with nothing installed.
//
// The table is deliberately explicit about which threshold applies to which
// pair rather than blanket-applying 4.5:1: a few tokens (the zero-count
// numeral, the ink on a filled accent button used only for large glyphs
// elsewhere) are large text or UI components and only need 3:1. Everything
// else here is small text — tiny uppercase labels, status words, pill text —
// so it needs 4.5:1, and the table says so per pair rather than assuming.

import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const CSS_PATH = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', '..', 'web', 'app.css');
const css = readFileSync(CSS_PATH, 'utf8');

/* ---------- pull the two :root blocks out of the sheet ---------- */

function block(source, selectorRe) {
  const m = selectorRe.exec(source);
  if (!m) throw new Error(`contrast test: could not find ${selectorRe} in app.css`);
  return m[1];
}
// Custom properties never nest braces, so a non-greedy match up to the first
// `}` after the selector is the whole rule body.
const rootBlock = block(css, /(?:^|\n):root\s*\{([^}]*)\}/);
const lightBlock = block(css, /:root\[data-theme="light"\]\s*\{([^}]*)\}/);

function parseDeclarations(blockText) {
  const vars = {};
  const re = /--([\w-]+)\s*:\s*([^;]+);/g;
  let m;
  while ((m = re.exec(blockText))) vars[m[1]] = m[2].trim();
  return vars;
}
const rootVars = parseDeclarations(rootBlock);
const lightVars = parseDeclarations(lightBlock);
// The light theme selector only overrides what it restates; everything else
// cascades from :root, same as in the browser.
const darkTheme = { ...rootVars };
const lightTheme = { ...rootVars, ...lightVars };

/* ---------- resolve a token to a colour ---------- */
// A resolved colour is either {solid: '#rrggbb'} or, for a translucent value
// that has not yet been composited onto a background, {rgb: [r,g,b], a}.

function hex6(n) { return n.map((x) => Math.round(Math.max(0, Math.min(255, x))).toString(16).padStart(2, '0')).join(''); }

function resolveRaw(value, theme, seen) {
  value = value.trim();
  let m;
  if ((m = /^#([0-9a-fA-F]{6})$/.exec(value))) return { solid: '#' + m[1].toLowerCase() };
  if ((m = /^#([0-9a-fA-F]{3})$/.exec(value))) {
    const [r, g, b] = m[1].split('');
    return { solid: `#${r}${r}${g}${g}${b}${b}`.toLowerCase() };
  }
  // rgb(var(--x-rgb)) — the sheet's way of turning a raw triplet into a colour.
  if ((m = /^rgb\(\s*var\(--([\w-]+)\)\s*\)$/.exec(value))) {
    const triplet = resolveVar(m[1], theme, seen);
    if (!triplet || !triplet.raw) throw new Error(`expected a raw "r, g, b" token for --${m[1]}`);
    return { solid: '#' + hex6(triplet.raw) };
  }
  // rgba(var(--x-rgb), a) — same, but translucent.
  if ((m = /^rgba\(\s*var\(--([\w-]+)\)\s*,\s*([\d.]+)\s*\)$/.exec(value))) {
    const triplet = resolveVar(m[1], theme, seen);
    if (!triplet || !triplet.raw) throw new Error(`expected a raw "r, g, b" token for --${m[1]}`);
    return { rgb: triplet.raw, a: Number(m[2]) };
  }
  // rgb(r, g, b) or rgba(r, g, b, a) with literal numbers.
  if ((m = /^rgba?\(\s*([\d.]+)\s*,\s*([\d.]+)\s*,\s*([\d.]+)\s*(?:,\s*([\d.]+))?\s*\)$/.exec(value))) {
    const rgb = [Number(m[1]), Number(m[2]), Number(m[3])];
    const a = m[4] === undefined ? 1 : Number(m[4]);
    return a >= 1 ? { solid: '#' + hex6(rgb) } : { rgb, a };
  }
  // A bare "r, g, b" triplet, as --up-rgb etc. are declared.
  if ((m = /^([\d.]+)\s*,\s*([\d.]+)\s*,\s*([\d.]+)$/.exec(value))) {
    return { raw: [Number(m[1]), Number(m[2]), Number(m[3])] };
  }
  // color-mix(in srgb, var(--accent) 88%, white) — the one pattern app.css
  // uses, for --accent-hover in both themes. "in srgb" mixes the gamma-coded
  // channel values directly, so this is a plain weighted average per channel.
  if ((m = /^color-mix\(\s*in\s+srgb\s*,\s*var\(--([\w-]+)\)\s+(\d+(?:\.\d+)?)%\s*,\s*(white|black)\s*\)$/.exec(value))) {
    const base = resolveVar(m[1], theme, seen);
    if (!base || !base.solid) throw new Error(`expected a solid colour for --${m[1]} inside color-mix`);
    const pct = Number(m[2]) / 100;
    const other = m[3] === 'white' ? [255, 255, 255] : [0, 0, 0];
    const baseRgb = hexToRgb(base.solid);
    const mixed = baseRgb.map((c, i) => c * pct + other[i] * (1 - pct));
    return { solid: '#' + hex6(mixed) };
  }
  // var(--other-token) with nothing else around it.
  if ((m = /^var\(\s*--([\w-]+)\s*\)$/.exec(value))) return resolveVar(m[1], theme, seen);
  throw new Error(`contrast test: don't know how to resolve token value "${value}"`);
}

function resolveVar(name, theme, seen = new Set()) {
  if (seen.has(name)) throw new Error(`contrast test: circular token reference at --${name}`);
  const raw = theme[name];
  if (raw === undefined) throw new Error(`contrast test: --${name} is not declared`);
  return resolveRaw(raw, theme, new Set(seen).add(name));
}

function hexToRgb(hex) {
  hex = hex.replace('#', '');
  return [0, 2, 4].map((i) => parseInt(hex.slice(i, i + 2), 16));
}

/** Resolve a token to an opaque hex colour, compositing over `bgHex` if it is translucent. */
function opaque(name, theme, bgHex) {
  const c = resolveVar(name, theme);
  if (c.solid) return c.solid;
  if (c.rgb) {
    const bg = hexToRgb(bgHex);
    const mixed = c.rgb.map((v, i) => v * c.a + bg[i] * (1 - c.a));
    return '#' + hex6(mixed);
  }
  throw new Error(`--${name} resolved to a raw triplet, not a colour — is it an "-rgb" token used directly?`);
}

/* ---------- WCAG 2.x contrast ---------- */

function relLuminance([r, g, b]) {
  const f = (c) => { c /= 255; return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4); };
  const [R, G, B] = [f(r), f(g), f(b)];
  return 0.2126 * R + 0.7152 * G + 0.0722 * B;
}
function contrast(hexA, hexB) {
  const La = relLuminance(hexToRgb(hexA));
  const Lb = relLuminance(hexToRgb(hexB));
  const [hi, lo] = La > Lb ? [La, Lb] : [Lb, La];
  return (hi + 0.05) / (lo + 0.05);
}

/* ---------- the pairs this palette has to clear ---------- */
// threshold 4.5 = normal/small text (everything here that isn't called out
// otherwise: tiny uppercase labels, status words, pill and chip text).
// threshold 3.0 = large text (≥18.66px bold or ≥24px regular) or a UI glyph.

const SURFACES = ['bg', 'card', 'elev'];
const SMALL = 4.5;
const LARGE = 3.0;

const PAIRS = [
  // token, backgrounds, threshold, note
  ['text', SURFACES, SMALL, 'body text'],
  ['muted', SURFACES, SMALL, 'secondary text'],
  ['dim', SURFACES, SMALL, 'tiny uppercase labels (table/section headers) — small text'],
  ['num-zero', SURFACES, LARGE, 'the zeroed-out summary numeral — large text'],
  ['paused-text', SURFACES, SMALL, 'paused status pill/word text'],
  ['unknown', SURFACES, SMALL, 'unknown status pill/word text'],
  ['up', SURFACES, SMALL, 'up status pill/word text'],
  ['down', SURFACES, SMALL, 'down status pill/word text'],
  ['warn', SURFACES, SMALL, 'warn status pill/word text'],
  ['maint', SURFACES, SMALL, 'maintenance status pill/word text'],
  ['accent-hover', ['bg', 'card'], SMALL, 'accent-coloured links/text (e.g. .text-accent)'],
];

function runPairs(theme, themeName) {
  for (const [token, bgs, threshold, note] of PAIRS) {
    for (const bgToken of bgs) {
      test(`${themeName}: --${token} on --${bgToken} (${note}) clears ${threshold}:1`, () => {
        const bgHex = opaque(bgToken, theme, null);
        const fgHex = opaque(token, theme, bgHex);
        const ratio = contrast(fgHex, bgHex);
        assert.ok(
          ratio >= threshold,
          `--${token} (${fgHex}) on --${bgToken} (${bgHex}) in ${themeName} theme is only ${ratio.toFixed(2)}:1, needs ${threshold}:1`,
        );
      });
    }
  }
  test(`${themeName}: --on-accent on --accent clears ${SMALL}:1 (filled button text)`, () => {
    const bgHex = opaque('accent', theme, null);
    const fgHex = opaque('on-accent', theme, bgHex);
    const ratio = contrast(fgHex, bgHex);
    assert.ok(
      ratio >= SMALL,
      `--on-accent (${fgHex}) on --accent (${bgHex}) in ${themeName} theme is only ${ratio.toFixed(2)}:1, needs ${SMALL}:1`,
    );
  });
}

// Each call registers its own top-level `test()`s (see runPairs above), so
// this just has to happen once per theme at module scope.
runPairs(darkTheme, 'dark');
runPairs(lightTheme, 'light');

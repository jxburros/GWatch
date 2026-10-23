// #118: web/mock.js stands in for the Go service in the jsdom suite, the
// Playwright suite and the ?mock=1 demo, and it is kept in step with the API
// by hand. This is the half of the contract that holds it to the API: the Go
// test internal/api/mock_contract_test.go records the shape of the real
// handlers' answers to a set of GETs in tests/contract/api-shapes.json, and
// here the mock is asked the same routes. The mock breaks the contract when
//   - it leaves out a key the service sends, or
//   - it sends a key as a different JSON type than the service does (a
//     string for a number, an object for an array).
// A renamed field shows up as the first. What is *not* held against the mock
// is a key the service did not send: the service leaves many fields out when
// they are empty (Go's omitempty), the seeded test server has little in it,
// and the mock's demo data is deliberately rich. A null on either side says
// nothing about the type, and an empty array nothing about its elements, so
// neither is compared further; a key the service sends as null may also be
// missing from the mock, since the interface reads the two the same way.
//
// When this fails after a handler changed: change web/mock.js to match. When
// it fails after the mock changed: the mock now answers differently from the
// service, and the interface tested against it may not work for real.

import './dom.mjs';
import '../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const shapes = JSON.parse(readFileSync(fileURLToPath(new URL('../contract/api-shapes.json', import.meta.url)), 'utf8'));

// Objects whose keys are data rather than field names. A leading `*` matches
// any path ending that way (`[]` is "every element of"). `values` compares
// every value against every other — a map from check id to its state — and
// `skip` compares nothing: a check's config holds whichever keys its type
// takes, and the seeded node has only a ping and an HTTP check.
const MAPS = [
  { at: '*.stateByCheck', as: 'values' },
  { at: '*.lastResults', as: 'values' },
  { at: '*.checks[].config', as: 'skip' },
  { at: '*.check.config', as: 'skip' },
];

/** The same description of a JSON value internal/api/mock_contract_test.go
 *  writes: "string" | "number" | "boolean" | "null" | {array} | {object}. */
function shapeOf(v) {
  if (v === null || v === undefined) return 'null';
  if (Array.isArray(v)) return { array: v.reduce((el, e) => mergeShape(el, shapeOf(e)), null) };
  if (typeof v === 'object') return { object: Object.fromEntries(Object.entries(v).map(([k, e]) => [k, shapeOf(e)])) };
  return typeof v;
}

function mergeShape(a, b) {
  if (a == null || a === 'null') return b;
  if (b == null || b === 'null') return a;
  if (a.object && b.object) {
    const out = { ...a.object };
    for (const [k, v] of Object.entries(b.object)) out[k] = mergeShape(out[k], v);
    return { object: out };
  }
  if ('array' in Object(a) && 'array' in Object(b)) return { array: mergeShape(a.array, b.array) };
  return a;
}

function kind(s) {
  if (typeof s === 'string') return s;
  return s.object ? 'object' : 'array';
}

const mapAt = (path) => MAPS.find(({ at }) => (at.startsWith('*') ? path.endsWith(at.slice(1)) : path === at))?.as;
const mergedValues = (obj) => Object.values(obj).reduce((acc, v) => mergeShape(acc, v), null);

/** Every way `mock` breaks `real`, as "path: what is wrong" lines. */
function compare(real, mock, path, out) {
  if (real === 'null' || mock === 'null' || real == null || mock == null) return out;
  if (kind(real) !== kind(mock)) { out.push(`${path}: the service sends ${kind(real)}, the mock ${kind(mock)}`); return out; }
  if (real.array !== undefined) return compare(real.array, mock.array, `${path}[]`, out);
  if (!real.object) return out;
  const map = mapAt(path);
  if (map === 'skip') return out;
  if (map === 'values') return compare(mergedValues(real.object), mergedValues(mock.object), `${path}{}`, out);
  for (const [k, v] of Object.entries(real.object)) {
    if (k in mock.object) compare(v, mock.object[k], `${path}.${k}`, out);
    else if (v !== 'null') out.push(`${path}.${k}: the service sends it (${kind(v)}), the mock leaves it out`);
  }
  return out;
}

// {node} is a node with an HTTP check that is up, {check} that check — the
// seeded node on the Go side has the same.
const nodes = await (await fetch('/api/nodes')).json();
const upHTTP = (n) => (n.checks || []).find((c) => c.type === 'http' && n.stateByCheck?.[c.id]?.status === 'up');
const node = nodes.find(upHTTP);
const check = upHTTP(node);
const fill = (route) => route.replace('{node}', node.id).replace('{check}', check.id);

for (const [key, real] of Object.entries(shapes)) {
  const route = key.replace(/^GET /, '');
  test(`web/mock.js answers ${key} in the shape the service does`, async () => {
    const res = await fetch(fill(route));
    assert.equal(res.status, 200, `the mock answers ${route}`);
    const mock = shapeOf(await res.json());
    const problems = compare(real, mock, '$', []);
    assert.deepEqual(problems, [], `web/mock.js differs from the service on ${route}:\n  ${problems.join('\n  ')}`);
  });
}

test('the comparison itself: a missing key and a changed type are caught, extras and nulls are not', () => {
  const real = shapeOf({ a: 1, b: 'x', c: null, d: [{ e: true }], stateByCheck: { 1: { s: 'up' } } });
  assert.deepEqual(compare(real, shapeOf({ a: 2, b: 'y', d: [{ e: false, extra: 1 }], stateByCheck: { 9: { s: 'down' } }, more: 1 }), '$', []), []);
  assert.deepEqual(compare(real, shapeOf({ a: '2', d: [{}], stateByCheck: { 9: { s: 1 } } }), '$', []), [
    '$.a: the service sends number, the mock string',
    '$.b: the service sends it (string), the mock leaves it out',
    '$.d[].e: the service sends it (boolean), the mock leaves it out',
    '$.stateByCheck{}.s: the service sends string, the mock number',
  ]);
});

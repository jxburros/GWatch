// web/api.js's identity handling (#70). People were shown as viewers when
// they were administrators (a failed first /api/me left the placeholder in
// place) and sent to the sign-in screen when nothing had signed them out (any
// single 401 did it). These tests drive api.js against a scripted fetch, so
// each case says exactly what the service answered.
//
// api.js keeps its identity in module state, so the tests run in order and
// each one starts from where the previous one left the page.

import test from 'node:test';
import assert from 'node:assert/strict';

/** The service, scripted: `routes[path]` answers each request to that path —
 *  a function returning [status, body], or throwing to play "unreachable". */
const routes = {};
const calls = [];
globalThis.fetch = async (path) => {
  calls.push(path);
  const handler = routes[path];
  if (!handler) return new Response('not found', { status: 404 });
  const [status, body] = handler();
  return new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } });
};

const api = await import('../../web/api.js');

const ADMIN = { kind: 'user', name: 'pat', role: 'admin', userId: 1, isAdmin: true, canWrite: true, signedIn: true, theme: 'dark' };
const VIEWER = { ...ADMIN, role: 'viewer', isAdmin: false, canWrite: false };
const ANON = { kind: '', isAdmin: false, canWrite: false, signedIn: false };
const busy = () => [503, { error: 'GWatch could not check who you are just now; try again in a moment' }];

/** Collect what the shell would hear: identity changes, sign-in challenges
 *  and refusals. */
const heard = { identity: [], challenge: [], denied: [] };
api.onIdentity((next, prev) => heard.identity.push({ next, prev }));
api.onAuthChallenge((reason) => heard.challenge.push(reason));
api.onDenied((msg) => heard.denied.push(msg));
const reset = () => { heard.identity.length = 0; heard.challenge.length = 0; heard.denied.length = 0; calls.length = 0; };

/** Let the fire-and-forget identity checks a refusal starts run to the end. */
const settle = async () => { for (let i = 0; i < 10; i++) await new Promise((r) => setTimeout(r, 0)); };

test('a failed first /api/me leaves the identity unknown, not "viewer"', async () => {
  reset();
  routes['/api/me'] = busy;
  const me = await api.refreshMe();
  assert.equal(api.identityKnown(), false);
  assert.equal(me.kind, '');
  assert.equal(heard.identity.length, 0, 'nothing was learned, so nothing may change');
});

test('refreshMe retries a busy service and then reports the real identity', async () => {
  reset();
  let n = 0;
  routes['/api/me'] = () => (++n < 3 ? busy() : [200, ADMIN]);
  const me = await api.refreshMe({ retries: 3, retryDelay: 1 });
  assert.equal(n, 3);
  assert.equal(api.identityKnown(), true);
  assert.equal(me.isAdmin, true);
  assert.equal(heard.identity.length, 1);
  assert.equal(heard.identity[0].next.role, 'admin');
});

test('a failed refresh never downgrades a known administrator', async () => {
  reset();
  routes['/api/me'] = busy;
  const me = await api.refreshMe({ retries: 1, retryDelay: 1 });
  assert.equal(me.isAdmin, true);
  assert.equal(api.me.isAdmin, true);
  assert.equal(heard.identity.length, 0);
});

test('an unchanged answer is not an identity change, even if the theme moved', async () => {
  reset();
  routes['/api/me'] = () => [200, { ...ADMIN, theme: 'light' }];
  await api.refreshMe();
  assert.equal(heard.identity.length, 0);
  assert.equal(api.me.theme, 'light');
});

test('a stray 401 does not sign out a browser /api/me says is signed in', async () => {
  reset();
  routes['/api/me'] = () => [200, ADMIN];
  routes['/api/nodes'] = () => [401, { error: 'sign in required' }];
  await assert.rejects(api.api.get('/api/nodes'), (e) => e.status === 401);
  await settle();
  assert.deepEqual(heard.challenge, []);
  assert.ok(calls.includes('/api/me'), 'the 401 should have been checked against /api/me');
});

test('a 401 while the service cannot say who this is does not sign out either', async () => {
  reset();
  routes['/api/me'] = busy;
  await assert.rejects(api.api.get('/api/nodes'), (e) => e.status === 401);
  await settle();
  assert.deepEqual(heard.challenge, []);
  assert.equal(api.me.isAdmin, true);
});

test('a 401 confirmed by /api/me takes the page to the sign-in screen', async () => {
  reset();
  routes['/api/me'] = () => [200, ANON];
  await assert.rejects(api.api.get('/api/nodes'), (e) => e.status === 401);
  await settle();
  assert.deepEqual(heard.challenge, ['sign in required']);
  assert.equal(api.me.signedIn, false);
  assert.equal(heard.identity.length, 1, 'the page is told its identity is gone');
});

test('a 403 re-reads the identity, so a changed role restyles the page', async () => {
  // Signed in again as an administrator…
  routes['/api/me'] = () => [200, ADMIN];
  await api.refreshMe();
  reset();
  // …who has since been made a viewer by someone else.
  routes['/api/me'] = () => [200, VIEWER];
  routes['/api/settings'] = () => [403, { error: 'only an administrator can do this; you are signed in as viewer "pat"' }];
  await assert.rejects(api.api.get('/api/settings'), (e) => e.status === 403);
  await settle();
  assert.equal(heard.denied.length, 1);
  assert.equal(heard.identity.length, 1);
  assert.equal(heard.identity[0].prev.isAdmin, true);
  assert.equal(heard.identity[0].next.isAdmin, false);
  assert.deepEqual(heard.challenge, [], 'a refusal is not a sign-out');
});

test('coming back from an outage re-reads the identity', async () => {
  reset();
  routes['/api/nodes'] = () => { throw new TypeError('network down'); };
  await assert.rejects(api.api.get('/api/nodes'), (e) => e.status === 0);
  assert.equal(api.connection.ok, false);

  routes['/api/nodes'] = () => [200, []];
  routes['/api/me'] = () => [200, ADMIN];
  await api.api.get('/api/nodes');
  await settle();
  assert.equal(api.connection.ok, true);
  assert.equal(api.me.isAdmin, true, 'the reconnect should have picked up the promotion');
  assert.equal(heard.identity.length, 1);
});

test('concurrent refreshes share one /api/me request', async () => {
  reset();
  routes['/api/me'] = () => [200, ADMIN];
  await Promise.all([api.refreshMe(), api.refreshMe(), api.refreshMe()]);
  assert.equal(calls.filter((p) => p === '/api/me').length, 1);
});

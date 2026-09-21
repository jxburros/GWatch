// The projected wallboard's own entry point. See wall.html for what this page
// is and why it is not the application.
//
// It talks to one endpoint, with the token from its own address, and nothing
// else: no session, no live stream, no navigation. A display left on this page
// for a month should still be right, so every failure here is temporary by
// construction — it says so on the screen and tries again.

import { h, applyTheme, applyAccent } from './components.js';
import { createWall } from './wall-render.js';

// The same switch as entry.js: with ?mock=1 the in-browser mock backend
// answers the one request this page makes, so the board can be looked at
// (and its accessibility checked) with no service behind it.
if (/(^|[?&])mock=1(&|$)/.test(location.search)) {
  await import('./mock.js');
}

const root = document.getElementById('wall');
if (!root) throw new Error('wall.html is missing its #wall element');
const params = new URLSearchParams(location.search);
const id = params.get('id') || params.get('board') || '';
const token = params.get('token') || '';

// A projected board may be opened on a screen nobody has configured, so the
// address may carry the look with it: ?theme=contrast&accent=ff9f6e.
applyTheme(params.get('mode') === 'light' ? 'light' : 'dark');
const accent = params.get('accent');
if (accent && /^#?[0-9a-f]{6}$/i.test(accent)) applyAccent(accent.startsWith('#') ? accent : `#${accent}`);

if (!id) {
  showProblem('This address is missing the wallboard it should show.',
    'Open Wallboards in GWatch, switch projection on for the board you want, and use the address it gives you.');
} else {
  start();
}

function start() {
  let failures = 0;
  const wall = createWall(root, async () => {
    const url = `/api/wallboards/${encodeURIComponent(id)}/view${token ? `?token=${encodeURIComponent(token)}` : ''}`;
    const res = await fetch(url, { headers: { Accept: 'application/json' }, credentials: 'same-origin' });
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      const err = new Error(body.error || `The service answered ${res.status}.`);
      err.status = res.status;
      throw err;
    }
    failures = 0;
    clearProblem();
    return res.json();
  }, {
    onError(e) {
      failures += 1;
      // 401 is the one failure that will not fix itself: the address is wrong
      // or projection was switched off. Everything else — the service
      // restarting, the network blinking — is worth waiting out quietly, and
      // only worth saying anything about once it has happened twice running.
      if (e.status === 401) {
        showProblem('This wallboard is not available at this address.',
          'Its projection may have been switched off, or the address may have been changed. Get a fresh one from Wallboards in GWatch.');
        wall.destroy();
        return;
      }
      if (failures >= 2) showProblem('Waiting for GWatch', e.message || 'The service is not answering. This screen will carry on trying.');
    },
  });

  // Keep the screen awake where the browser allows it. A wallboard that blanks
  // after ten minutes is not a wallboard.
  keepAwake();
}

/** An overlay rather than a replacement: the last good board stays behind it,
 *  because a stale board is more use on a wall than an error message. */
function showProblem(title, detail) {
  clearProblem();
  document.body.append(h('div', { class: 'wall-problem', id: 'wall-problem' },
    h('h2', null, title),
    h('p', null, detail)));
}

function clearProblem() {
  document.getElementById('wall-problem')?.remove();
}

async function keepAwake() {
  if (!('wakeLock' in navigator)) return;
  let lock = null;
  const acquire = async () => {
    try { lock = await navigator.wakeLock.request('screen'); } catch { /* denied or unsupported; the screen may sleep */ }
  };
  await acquire();
  // A wake lock is dropped when the tab is hidden, so it is taken again when
  // the tab comes back.
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible' && lock?.released !== false) acquire();
  });
}

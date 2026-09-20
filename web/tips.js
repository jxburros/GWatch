// Coach-marks: one small popover at a time, pointing at a real control and
// saying the thing the control itself cannot say in its label. They stay off
// until somebody asks for them, each one is shown once and then never again,
// and everything they remember lives in this browser — the service is not told
// which hints a person has read.

import { h, icon } from './components.js';

/* ---------- What this browser remembers ---------- */
// Same "gw." prefix as the theme and the rail pin, so clearing site data
// clears the tour along with everything else the interface remembers locally.
const ONBOARDING_KEY = 'gw.onboarding.done';
const ENABLED_KEY = 'gw.tips.enabled';
const SEEN_KEY = 'gw.tips.seen';

function read(key, fallback = '') { try { return localStorage.getItem(key) ?? fallback; } catch { return fallback; } }
function write(key, value) { try { localStorage.setItem(key, value); } catch { /* private window, or storage is full */ } }
function drop(key) { try { localStorage.removeItem(key); } catch { /* ignore */ } }

export function onboardingDone() { return read(ONBOARDING_KEY) === '1'; }
export function setOnboardingDone() { write(ONBOARDING_KEY, '1'); }
export function resetOnboarding() { drop(ONBOARDING_KEY); }

/** Tips are opt-in: no key means off, which is what a new install has. */
export function tipsEnabled() { return read(ENABLED_KEY) === '1'; }
export function setTipsEnabled(on) {
  write(ENABLED_KEY, on ? '1' : '0');
  if (!on) closeTip({ seen: false });
}

function seenSet() {
  try { const raw = JSON.parse(read(SEEN_KEY, '[]')); return new Set(Array.isArray(raw) ? raw : []); } catch { return new Set(); }
}
function markSeen(id) { const s = seenSet(); s.add(id); write(SEEN_KEY, JSON.stringify([...s])); }
export function seenCount() { return seenSet().size; }
export function resetTips() { drop(SEEN_KEY); }

/* ---------- The registry ---------- */
// Data, not behaviour: each entry names an anchor to point at, the routes it
// belongs to, and two sentences that teach something the interface does not
// already spell out. A tip whose anchor is missing is simply skipped, so a tip
// may safely describe a control that only an administrator is shown.
export const TIPS = [
  {
    id: 'header-counts',
    route: /^\/(dashboard|nodes|incidents)/,
    anchor: '#indicators .indicator',
    place: 'bottom',
    title: 'The indicators are links',
    body: 'Every orb under the page name opens what it is about — a red one goes straight to the nodes that are down. One green orb means nothing is firing, blue means nothing has been checked yet, and which conditions light which colour is yours to set in Settings › Indicators.',
  },
  {
    id: 'service-health',
    route: /^\//,
    anchor: '#service-health',
    place: 'right',
    title: 'GWatch watches itself too',
    body: 'This line is the monitor\'s own health, not your network\'s: a stopped scheduler, no checks for ten minutes, a failed backup or a retention error all surface here first.',
  },
  {
    id: 'rail-pin',
    route: /^\//,
    anchor: '#rail-pin',
    place: 'right',
    title: 'The sidebar can stay open',
    body: 'It collapses to icons so charts and tables get the width. Pin it if you would rather read the labels — this browser remembers the choice.',
  },
  {
    id: 'dash-widget-drag',
    route: /^\/dashboard/,
    anchor: '.dash-grid .widget-drag',
    place: 'right',
    title: 'Widgets move and resize',
    body: 'Drag a widget by this grip to rearrange the four-column grid, or pull its edges to resize it. The layout is stored with that dashboard, so each board can be laid out differently.',
  },
  {
    id: 'dash-tabs',
    route: /^\/dashboard/,
    anchor: '.dash-tabs',
    place: 'bottom',
    title: 'More than one dashboard',
    body: 'Dashboards are separate boards with their own widgets — one for the house, one for the servers, one aimed at the wallboard in the hall.',
  },
  {
    id: 'nodes-filter-urls',
    route: /^\/nodes$/,
    anchor: '.filter-bar',
    place: 'bottom',
    title: 'Filters travel in the address bar',
    body: 'This list reads its filters from the link that opened it, which is how the header counts jump here. #/nodes?status=down, ?group=, ?tag= and ?q= are all bookmarkable.',
  },
  {
    id: 'node-run-now',
    route: /^\/nodes\/\d+$/,
    anchor: '.check-card-head',
    place: 'bottom',
    title: 'Do not wait for the schedule',
    body: 'Run now performs the check immediately and shows the whole result — timings, status code, final URL, resolved addresses, certificate details — which is the quickest way to prove a fix worked.',
  },
  {
    id: 'hardware-agent',
    route: /^\/nodes/,
    anchor: '.topbar-actions .btn',
    place: 'bottom',
    title: 'Other machines report inward',
    body: 'A machine is a node like any other. Pair one and GWatch makes its node for you; the agent you install on it connects out and hangs up — GWatch never connects back and holds no credential for that machine.',
  },
  {
    id: 'incidents-suppressed',
    route: /^\/incidents/,
    anchor: '.timeline, .filter-bar',
    place: 'bottom',
    title: 'The alerts you did not get are here',
    body: '"Alert suppressed" and "affected by parent" entries explain GWatch\'s silence: when the gateway is down, everything behind it is recorded as affected rather than mailed to you twenty times.',
  },
  {
    id: 'charts-pin',
    route: /^\/charts/,
    anchor: '.charts-side, .chart-card .card-actions',
    place: 'left',
    title: 'A chart you like can stay',
    body: 'Any chart you build here can be saved under a name, exported as PNG or CSV, or pinned to a dashboard as a widget with exactly these settings.',
  },
  {
    id: 'settings-users',
    route: /^\/settings/,
    anchor: '.settings-nav a[href="#/settings/users"]',
    place: 'right',
    title: 'Accounts beat one shared password',
    body: 'A viewer account can see everything and change nothing, and each change lands in the audit log under a name. The single access password is the older way in and cannot do either.',
  },
  {
    id: 'settings-backups',
    route: /^\/settings/,
    anchor: '.settings-nav a[href="#/settings/backups"]',
    place: 'right',
    title: 'Backups are one portable file',
    body: 'A backup is a password-encrypted archive of the configuration, optionally with the whole history. Restoring it on a fresh install is the supported way to move GWatch to another computer.',
  },
];

/* ---------- The engine ---------- */

function prefersReducedMotion() {
  return typeof window.matchMedia === 'function' && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
}

// A view animates in over roughly half a second (see `replayEnter` in app.js),
// and a popover that lands mid-animation points at a control that is still
// sliding. Wait for the dust to settle, then look for an anchor a few times in
// case the view is still fetching what it needs.
const SETTLE_MS = 950;
const RETRY_MS = 600;
const RETRIES = 6;

let active = null;   // { el, tip, anchor, prevFocus, describedBy, reposition }
let hunt = 0;        // timer that looks for the next tip's anchor
let seq = 0;

/** Called by the router once a view has mounted. Closes whatever is open and,
 *  if tips are on, starts hunting for the first unseen tip on this route. */
export function notifyRoute(path) {
  clearTimeout(hunt);
  closeTip({ seen: false });
  if (!tipsEnabled()) return;
  const candidates = TIPS.filter((t) => t.route.test(path) && !seenSet().has(t.id));
  if (!candidates.length) return;
  let tries = 0;
  const look = () => {
    if (active || !tipsEnabled()) return;
    for (const tip of candidates) {
      if (seenSet().has(tip.id)) continue;
      const anchor = visibleAnchor(tip.anchor);
      if (anchor) { showTip(tip, anchor); return; }
    }
    if (++tries <= RETRIES) hunt = setTimeout(look, RETRY_MS);
  };
  hunt = setTimeout(look, prefersReducedMotion() ? 200 : SETTLE_MS);
}

function visibleAnchor(selector) {
  for (const el of document.querySelectorAll(selector)) {
    const r = el.getBoundingClientRect();
    if (r.width > 0 && r.height > 0 && r.bottom > 0 && r.top < window.innerHeight) return el;
  }
  return null;
}

export function closeTip({ seen = true } = {}) {
  if (!active) return;
  const { el, tip, anchor, prevFocus, describedBy, reposition, onKey, onDown } = active;
  active = null;
  if (seen) markSeen(tip.id);
  el.remove();
  anchor.classList.remove('tip-target');
  if (describedBy == null) anchor.removeAttribute('aria-describedby'); else anchor.setAttribute('aria-describedby', describedBy);
  window.removeEventListener('resize', reposition);
  window.removeEventListener('scroll', reposition, true);
  document.removeEventListener('keydown', onKey, true);
  document.removeEventListener('mousedown', onDown, true);
  // Give focus back to whatever had it, unless that element left with the view.
  if (prevFocus && prevFocus.isConnected && prevFocus.focus) { try { prevFocus.focus(); } catch { /* ignore */ } }
}

function showTip(tip, anchor) {
  const titleId = `tip-title-${++seq}`;
  const bodyId = `tip-body-${seq}`;
  const el = h('div', { class: 'tip', role: 'dialog', 'aria-labelledby': titleId, 'aria-describedby': bodyId });
  const dismiss = h('button', { class: 'btn btn-sm btn-primary', type: 'button', onclick: () => closeTip() }, 'Got it');
  el.append(
    h('div', { class: 'tip-head' },
      h('span', { class: 'tip-badge' }, icon('info'), 'Tip'),
      h('button', { class: 'btn btn-ghost btn-sm icon-btn', type: 'button', 'aria-label': 'Dismiss this tip', onclick: () => closeTip() }, icon('x'))),
    h('h2', { class: 'tip-title', id: titleId }, tip.title),
    h('p', { class: 'tip-body', id: bodyId }, tip.body),
    h('div', { class: 'tip-foot' },
      h('button', { class: 'tip-off', type: 'button', onclick: () => setTipsEnabled(false) }, 'Turn tips off'),
      dismiss),
  );
  document.body.appendChild(el);

  const describedBy = anchor.getAttribute('aria-describedby');
  anchor.setAttribute('aria-describedby', bodyId);
  anchor.classList.add('tip-target');

  const reposition = () => place(el, anchor, tip.place || 'bottom');
  const onKey = (e) => { if (e.key === 'Escape') { e.preventDefault(); closeTip(); } };
  const onDown = (e) => { if (!el.contains(e.target)) closeTip(); };
  window.addEventListener('resize', reposition);
  window.addEventListener('scroll', reposition, true);
  document.addEventListener('keydown', onKey, true);
  document.addEventListener('mousedown', onDown, true);

  active = { el, tip, anchor, prevFocus: document.activeElement, describedBy, reposition, onKey, onDown };
  reposition();
  dismiss.focus();
}

/** Put the popover beside the anchor, trying the preferred side first and
 *  falling back through the others until one fits, then clamping to the
 *  viewport so a tip near an edge is never half off-screen. */
function place(el, anchor, preferred) {
  const gap = 10;
  const pad = 8;
  const a = anchor.getBoundingClientRect();
  const w = el.offsetWidth;
  const ht = el.offsetHeight;
  const vw = window.innerWidth;
  const vh = window.innerHeight;
  const fits = {
    bottom: vh - a.bottom - gap >= ht,
    top: a.top - gap >= ht,
    right: vw - a.right - gap >= w,
    left: a.left - gap >= w,
  };
  const order = [preferred, 'bottom', 'right', 'top', 'left'];
  const side = order.find((s) => fits[s]) || 'bottom';
  let left; let top;
  if (side === 'bottom' || side === 'top') {
    left = a.left + a.width / 2 - w / 2;
    top = side === 'bottom' ? a.bottom + gap : a.top - gap - ht;
  } else {
    left = side === 'right' ? a.right + gap : a.left - gap - w;
    top = a.top + a.height / 2 - ht / 2;
  }
  el.style.left = `${Math.max(pad, Math.min(left, vw - w - pad))}px`;
  el.style.top = `${Math.max(pad, Math.min(top, vh - ht - pad))}px`;
  el.dataset.side = side;
}

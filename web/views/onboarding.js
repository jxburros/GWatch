// First run: six screens that together take under a minute. It is shown once,
// automatically, and can be replayed from the Help page or Settings. Nothing
// here changes the service — every step points at the page that does, because
// a wizard that quietly configures things is a wizard nobody trusts.

import { h, icon, replace, banner, toggle } from '../components.js';
import { setOnboardingDone, tipsEnabled, setTipsEnabled } from '../tips.js';

const STEPS = [
  {
    id: 'welcome',
    icon: 'rocket',
    title: 'This is GWatch',
    lead: 'A monitor for your own network, running on this computer.',
    body: () => [
      h('p', null, 'GWatch checks the routers, servers, websites and APIs you tell it about on a schedule, keeps the history in a database beside itself, and tells you when something changes.'),
      h('p', null, 'There is no cloud account and nothing is sent anywhere. The interface is served on this machine only until you decide otherwise.'),
    ],
  },
  {
    id: 'password',
    icon: 'lock',
    title: 'Set a password first',
    lead: 'A fresh GWatch has no password at all. This is the one step worth doing now.',
    important: true,
    body: () => [
      banner('warn', 'Until you create an account, anyone who can reach this interface can change anything — including triggers that run commands on this computer.'),
      h('p', null, 'Create an administrator account under ', h('a', { href: '#/settings/users' }, 'Settings › Users & access'), '. Everyone then gets their own password, a name in the audit log, and you can hand out viewer accounts that can look but not touch.'),
      h('p', null, 'Do it before you turn on ', h('a', { href: '#/settings/network' }, 'remote access'), ', which is what lets a phone or another PC on your network open GWatch.'),
      h('p', { class: 'note' }, 'Nothing here is exposed to the internet by itself, and GWatch never asks you to open a port.'),
    ],
  },
  {
    id: 'node',
    icon: 'server',
    title: 'Add your first node',
    lead: 'A node is one thing you care about: a device, a server or a website.',
    body: () => [
      h('p', null, 'Each node holds one or more checks, so "Plex" can be a ping, a TCP port and the web page all at once, and goes down as one thing rather than three.'),
      h('p', null, 'The check types you will reach for first: ', h('b', null, 'ping'), ' (is it reachable, and how fast), ', h('b', null, 'TCP'), ' (is a port open), ', h('b', null, 'HTTP/S'), ' (does the page answer with the status you expect), ', h('b', null, 'DNS'), ' (does the name resolve) and ', h('b', null, 'certificate'), ' (is the TLS certificate valid, and how long until it expires).'),
      h('p', { class: 'note' }, 'Templates fill in sensible defaults for a website, a home server, a router, an API or a DNS name — everything stays editable afterwards.'),
    ],
    actions: () => [h('a', { class: 'btn', href: '#/nodes/new' }, icon('plus'), 'Add a node')],
  },
  {
    id: 'hardware',
    icon: 'cpu',
    title: 'Watch the machines themselves',
    lead: 'Optional, and useful the first time a disk quietly fills up.',
    body: () => [
      h('p', null, 'GWatch reads this computer\'s processor, memory, disk space and throughput on its own. For any other machine, you install the small ', h('b', null, 'agent'), ' on it and it reports in every minute.'),
      h('p', null, 'The agent connects outwards and hangs up: GWatch never connects back, holds no credential for that machine, and the agent\'s token can do exactly one thing — submit that machine\'s readings.'),
      h('p', { class: 'note' }, 'Register a machine under Settings › Hardware; it appears on the Hardware page within a minute.'),
    ],
    actions: () => [h('a', { class: 'btn', href: '#/settings/hardware' }, icon('cpu'), 'Register a machine')],
  },
  {
    id: 'alerts',
    icon: 'bell',
    title: 'Getting told about it',
    lead: 'Email for people, triggers for everything else.',
    body: () => [
      h('p', null, 'Point ', h('a', { href: '#/settings/alerts' }, 'Settings › Alerts'), ' at any SMTP provider and send the test email before you rely on it. Alerts fire after a number of consecutive failures, again on recovery, and respect a cooldown so one flapping check cannot fill your inbox.'),
      h('p', null, h('a', { href: '#/settings/automation' }, 'Automation'), ' covers the rest: webhooks, Slack, Teams, ntfy, Pushover, a git command or your own script, run when a node goes down, recovers or changes status.'),
      h('p', { class: 'note' }, 'When the gateway is down, the things behind it are recorded as "affected by Gateway" instead of mailing you about each one.'),
    ],
  },
  {
    id: 'finish',
    icon: 'check',
    title: 'That is the tour',
    lead: 'The rest is on the Help page whenever you want it.',
    body: () => [
      h('p', null, 'Help holds the same material in more depth, a search box over every topic, links to the full documentation, and the button that replays this tour.'),
      h('p', null, 'Tips are small hints that point at one control and explain what it does. They are off unless you ask for them, each appears once, and you can turn them off again from Help or Settings › Appearance.'),
    ],
    extra: () => {
      const t = toggle({
        label: 'Show tips as I go',
        checked: tipsEnabled(),
        onChange: (on) => setTipsEnabled(on),
      });
      return h('div', { class: 'ob-optin' }, t);
    },
    actions: () => [h('a', { class: 'btn', href: '#/help' }, icon('help'), 'Open Help')],
  },
];

export async function mount(root, ctx) {
  let i = 0;
  const card = h('section', { class: 'card card-lg onboard' });
  root.append(h('div', { class: 'onboard-wrap' }, card));

  // Finishing and skipping both count as "seen": the point of the flag is that
  // nobody is shown this twice without asking for it.
  const leave = (to = '/dashboard') => { setOnboardingDone(); ctx.navigate(to); };

  function render() {
    const s = STEPS[i];
    ctx.setTitle('Welcome to GWatch', { subtitle: `Step ${i + 1} of ${STEPS.length} · ${s.title}` });
    const dots = h('ol', { class: 'ob-dots', 'aria-label': `Step ${i + 1} of ${STEPS.length}` });
    STEPS.forEach((st, n) => dots.append(h('li', { class: `ob-dot ${n === i ? 'active' : ''} ${n < i ? 'done' : ''}`, 'aria-current': n === i ? 'step' : null, title: st.title })));
    const last = i === STEPS.length - 1;
    replace(card,
      h('div', { class: 'ob-progress' },
        h('span', { class: 'ob-count' }, `Step ${i + 1} of ${STEPS.length}`),
        dots,
        h('div', { class: 'ob-bar' }, h('i', { style: { width: `${((i + 1) / STEPS.length) * 100}%` } }))),
      h('div', { class: `ob-step ${s.important ? 'ob-step-important' : ''}` },
        h('span', { class: 'ob-icon' }, icon(s.icon)),
        h('div', { class: 'ob-copy' },
          h('h2', null, s.title),
          h('p', { class: 'lead' }, s.lead),
          s.body(),
          s.extra ? s.extra() : null,
          s.actions ? h('div', { class: 'btn-group', style: { marginTop: '4px' } }, s.actions()) : null)),
      h('div', { class: 'ob-foot' },
        h('button', { class: 'btn btn-ghost', type: 'button', onclick: () => leave() }, last ? 'Close' : 'Skip the tour'),
        h('div', { class: 'btn-group' },
          h('button', { class: 'btn', type: 'button', disabled: i === 0, onclick: back }, icon('arrowLeft'), 'Back'),
          h('button', { class: 'btn btn-primary', type: 'button', onclick: next }, last ? 'Finish' : 'Next', last ? icon('check') : icon('arrowRight')))),
      h('p', { class: 'ob-keys note' }, 'Enter for the next step, Esc to skip.'),
    );
    // The primary action is the sensible place to land, and it keeps Enter
    // doing the same thing whether or not the person has touched the keyboard.
    card.querySelector('.ob-foot .btn-primary')?.focus();
  }

  function next() { if (i < STEPS.length - 1) { i++; render(); } else leave(); }
  function back() { if (i > 0) { i--; render(); } }

  // Enter advances and Esc skips. Enter is only handled here when nothing
  // focusable owns the keystroke — the Next button is focused on every step,
  // so usually the browser's own click does the advancing and this does not
  // fire twice.
  function onKey(e) {
    if (e.key === 'Escape') { e.preventDefault(); leave(); return; }
    if (e.key !== 'Enter' || e.shiftKey || e.ctrlKey || e.metaKey || e.altKey) return;
    const a = document.activeElement;
    if (a && a !== document.body && a.closest('a[href], button, input, select, textarea')) return;
    e.preventDefault();
    next();
  }
  document.addEventListener('keydown', onKey);

  render();
  return { destroy() { document.removeEventListener('keydown', onKey); } };
}

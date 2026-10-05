// Help: the manual, filtered client-side, and the place the opt-in tutorial
// starts from. The words themselves live in web/help-content.js, one copy
// shared with the tutorial; this view only lays them out.

import { api } from '../api.js';
import { h, icon, clear, replace, toggle, emptyState, toast } from '../components.js';
import { onboardingDone, resetOnboarding, tipsEnabled, setTipsEnabled, resetTips, seenCount, TIPS, startTutorial, tutorialProgress, resetTutorial } from '../tips.js';
import { isBeta } from '../fmt.js';
import { TOPICS, REPO_URL, DOCS_URL } from '../help-content.js';

// The repo honours prefers-reduced-motion everywhere else, and a jump link is
// exactly the kind of long travel that people turn it off for.
function scrollBehavior() {
  return typeof window.matchMedia === 'function' && window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth';
}

function matches(topic, q) {
  if (!q) return true;
  const hay = `${topic.title} ${topic.q} ${topic.el.textContent}`.toLowerCase();
  return q.split(/\s+/).filter(Boolean).every((w) => hay.includes(w));
}

export async function mount(root, ctx) {
  ctx.setTitle('Help');

  // Bodies are built once so the filter can search their real text rather than
  // a summary that would drift out of step with it.
  for (const t of TOPICS) {
    t.el = h('section', { class: 'card help-topic', id: `help-${t.id}` },
      h('h2', null, h('span', { class: 'help-topic-icon' }, icon(t.icon)), t.title),
      h('div', { class: 'help-body' }, t.body()));
  }

  const search = h('input', { type: 'search', placeholder: 'Search help — "agent", "certificate", "backup"…', 'aria-label': 'Search help topics', 'aria-controls': 'help-results' });
  const count = h('span', { class: 'filter-count', role: 'status' });
  const jump = h('nav', { class: 'help-jump', 'aria-label': 'Help topics' });
  const results = h('div', { class: 'stack', id: 'help-results' });

  const render = () => {
    const q = search.value.trim().toLowerCase();
    const hits = TOPICS.filter((t) => matches(t, q));
    clear(jump);
    for (const t of hits) jump.append(h('a', { class: 'chip', href: '#/help', onclick: (e) => { e.preventDefault(); t.el.scrollIntoView({ behavior: scrollBehavior(), block: 'start' }); } }, t.title));
    replace(results, hits.length
      ? hits.map((t) => t.el)
      : emptyState({ icon: 'search', title: 'Nothing matches', text: 'Try a shorter word, or clear the search to read everything.', compact: true, actions: h('button', { class: 'btn btn-sm', type: 'button', onclick: () => { search.value = ''; render(); search.focus(); } }, 'Clear search') }));
    count.textContent = q ? `${hits.length} of ${TOPICS.length} topics` : `${TOPICS.length} topics`;
  };
  search.addEventListener('input', render);

  // #/help?topic=<id> opens on that topic, which is how the docs and the
  // tutorial point at one paragraph without repeating it.
  const topicId = ctx.query?.get?.('topic');
  const showTopic = (id) => {
    const t = TOPICS.find((x) => x.id === id);
    if (!t) return;
    if (!t.el.isConnected) { search.value = ''; render(); }
    requestAnimationFrame(() => t.el.scrollIntoView({ behavior: scrollBehavior(), block: 'start' }));
  };

  root.append(h('p', { class: 'note' }, h('a', { href: `${REPO_URL}/blob/main/docs/INSTALL-LINUX.md`, target: '_blank', rel: 'noopener' }, 'Linux installation and troubleshooting')));
  root.append(
    h('div', { class: 'filter-bar help-search' },
      h('div', { class: 'toolbar' },
        h('div', { class: 'search' }, icon('search'), search),
        count),
      jump),
    h('div', { class: 'stack' }, tourCard(ctx), results, docsCard(), aboutCard()),
  );
  render();
  if (topicId) showTopic(topicId);
  return {
    update(params, query) { const id = query?.get?.('topic'); if (id) showTopic(id); return true; },
    destroy() {},
  };
}

/* ---------- The tour, and the tips that follow it ---------- */

function tourCard(ctx) {
  const seen = h('span', { class: 'note' });
  const refreshSeen = () => {
    const n = seenCount();
    seen.textContent = tipsEnabled()
      ? `${n} of ${TIPS.length} tips shown so far.`
      : 'Tips are off. Nothing will appear until you turn them on.';
  };
  const tipsToggle = toggle({
    label: 'Show tips as I go',
    checked: tipsEnabled(),
    onChange: (on) => { setTipsEnabled(on); refreshSeen(); toast(on ? 'Tips are on — they appear as you move around' : 'Tips are off', { kind: 'success' }); },
  });
  refreshSeen();
  return h('section', { class: 'card help-tour' },
    h('h2', null, 'Tutorial, tour and tips'),
    tutorialBlock(),
    h('p', { class: 'lead' }, 'The tour is the six-screen introduction shown on the first run. Tips are small hints that point at one control and explain what it does; each appears once and is then gone for good.'),
    h('div', { class: 'stack-sm' },
      tipsToggle,
      seen,
      h('div', { class: 'btn-group', style: { marginTop: '6px' } },
        h('button', { class: 'btn', type: 'button', onclick: () => { resetOnboarding(); ctx.navigate('/onboarding'); } }, icon('play'), 'Restart onboarding'),
        h('button', { class: 'btn', type: 'button', onclick: () => { resetTips(); refreshSeen(); toast('Tips reset — you will see them again', { kind: 'success' }); } }, icon('refresh'), 'Reset tips'),
      ),
      onboardingDone() ? null : h('p', { class: 'note' }, 'You have not been through the tour yet.'),
    ));
}

/** The tutorial walks the real screens a step at a time (web/tips.js). It is
 *  offered here and nowhere else, and it never starts or resumes by itself:
 *  the buttons below are the only way in, and they say where it got to. */
function tutorialBlock() {
  const p = tutorialProgress();
  const start = (from) => (e) => { e.currentTarget.blur?.(); startTutorial(from); };
  const buttons = [];
  let status;
  if (p.started && !p.done) {
    status = `You stopped at step ${p.next + 1} of ${p.total}.`;
    buttons.push(
      h('button', { class: 'btn btn-primary', type: 'button', onclick: start(p.next) }, icon('play'), `Resume the tutorial`),
      h('button', { class: 'btn', type: 'button', onclick: (e) => { resetTutorial(); start(0)(e); } }, icon('refresh'), 'Start over'));
  } else {
    status = p.done ? 'You have been through it; it is here to take again.' : `${p.total} short steps across the pages you will use most. Leave whenever you like — Esc pauses it, and this page remembers where you were.`;
    buttons.push(h('button', { class: 'btn btn-primary', type: 'button', onclick: start(0) }, icon('play'), p.done ? 'Take the tutorial again' : 'Start the tutorial'));
  }
  return h('div', { class: 'stack-sm help-tutorial' },
    h('p', { class: 'lead' }, 'The tutorial is a guided walk through GWatch itself: each step goes to the page it is about, points at the control and says what it is for.'),
    h('p', { class: 'note' }, status),
    h('div', { class: 'btn-group' }, buttons));
}

/* ---------- Documentation, licence and legal ---------- */

function docsCard() {
  const link = (href, label, sub) => h('a', { class: 'help-link', href, target: '_blank', rel: 'noopener' },
    icon('file'), h('span', null, h('b', null, label), sub ? h('span', { class: 'dim' }, sub) : null), icon('external'));
  return h('section', { class: 'card help-docs' },
    h('h2', null, 'Documentation'),
    h('p', { class: 'lead' }, 'The full manual lives with the source on GitHub and always matches the release you are running.'),
    h('div', { class: 'help-links' },
      link(`${DOCS_URL}/USER-GUIDE.md`, 'User guide', 'Every screen and setting, in order — the long version of this page'),
      link(`${DOCS_URL}/INSTALL.md`, 'Installing on Windows', 'Setup program, upgrading, uninstalling'),
      link(`${DOCS_URL}/HARDWARE.md`, 'Hardware health', 'The agent, pairing, packages, what is measured, thresholds'),
      link(`${DOCS_URL}/DATABASE.md`, 'Database', 'SQLite, or your own PostgreSQL or MySQL/MariaDB server, and moving between them'),
      link(`${DOCS_URL}/DOCKER.md`, 'Running in Docker', 'The container image, its volume and its limits'),
      link(`${DOCS_URL}/SNMP.md`, 'SNMP checks', 'Enabling SNMP, choosing OIDs, traffic in bits per second'),
      link(`${DOCS_URL}/REMOTE-ACCESS.md`, 'Remote access', 'Reaching GWatch from outside, safely'),
      link(`${DOCS_URL}/RESTORE.md`, 'Restore on a new machine', 'Backup, install, restore'),
      link(`${DOCS_URL}/RECIPES.md`, 'Trigger & endpoint recipes', 'Home Assistant, Discord, ntfy, Docker, git'),
      link(`${DOCS_URL}/API.md`, 'The localhost API', 'Endpoints, roles and the event stream'),
      link(`${REPO_URL}/blob/main/mcp/README.md`, 'AI assistants (MCP)', 'gwatch-mcp, the trust model, the agent skill; set up under Settings › AI & MCP'),
    ),
    h('h3', { class: 'section-title', style: { marginTop: '18px' } }, 'Licence and legal'),
    h('div', { class: 'help-links' },
      link(`${REPO_URL}/blob/main/LICENSE`, 'Licence', 'The terms this software is released under'),
      link(`${DOCS_URL}/TERMS.md`, 'Terms of use'),
      link(`${DOCS_URL}/PRIVACY.md`, 'Privacy'),
      link(`${DOCS_URL}/DISCLAIMER.md`, 'Disclaimer'),
    ));
}

/* ---------- About ---------- */

function aboutCard() {
  // The same source the Settings › About tab uses, so the two never disagree.
  const version = h('span', { class: 'mono' }, '…');
  // Filled in once the service answers; the beta note appears with it rather
  // than flashing in on a version nobody has read yet.
  const betaNote = h('p', { class: 'note', hidden: true });
  api.get('/api/version').then((v) => {
    version.textContent = `${v.version ? `v${v.version}` : 'unknown'} · ${v.platform || 'unknown'}`;
    if (!isBeta(v.version)) return;
    betaNote.hidden = false;
    betaNote.append(h('b', null, 'This is a beta release. '),
      'GWatch works and keeps your data safely, but features and the layout can still change between releases. ',
      h('a', { href: `${REPO_URL}/issues`, target: '_blank', rel: 'noopener' }, 'Report anything that looks wrong', icon('external')), '.');
  }).catch(() => { version.textContent = 'version unavailable'; });
  return h('section', { class: 'card help-about' },
    h('h2', null, 'About GWatch'),
    h('div', { class: 'row', style: { alignItems: 'flex-start', gap: '16px' } },
      h('img', { src: 'logo.svg', alt: '', width: '48', height: '48' }),
      h('div', { class: 'stack-sm' },
        h('p', null, 'A self-hosted monitor for the machines, sites and services on your own network.'),
        h('p', { class: 'muted' }, version),
        h('p', null, 'By ', h('b', null, 'JX Holdings, LLC'), '. Developed by ', h('b', null, 'Jeffrey Guntly'), ' and ', h('b', null, 'Garrett Guntly'), '.'),
        betaNote,
        h('p', { class: 'note' }, 'Source, releases and issues: ', h('a', { href: REPO_URL, target: '_blank', rel: 'noopener' }, 'github.com/jxburros/GWatch', icon('external'))))));
}

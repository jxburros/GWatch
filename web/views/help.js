// Help: the manual, filtered client-side. Every claim on this page is one the
// interface or docs/ actually makes — a help page that promises a feature the
// build does not have is worse than no help page at all.

import { api } from '../api.js';
import { h, icon, clear, replace, toggle, emptyState, toast } from '../components.js';
import { onboardingDone, resetOnboarding, tipsEnabled, setTipsEnabled, resetTips, seenCount, TIPS } from '../tips.js';
import { isBeta } from '../fmt.js';

const REPO_URL = 'https://github.com/jxburros/GWatch';
const DOCS_URL = `${REPO_URL}/blob/main/docs`;

const doc = (path, label) => h('a', { href: `${DOCS_URL}/${path}`, target: '_blank', rel: 'noopener' }, label, icon('external'));

// `q` is the extra vocabulary somebody might search for but that the prose
// does not happen to use; the title and body are searched as well.
const TOPICS = [
  {
    id: 'getting-started',
    title: 'Getting started',
    icon: 'rocket',
    q: 'setup first run install service port 7230 localhost begin tour onboarding',
    body: () => [
      h('p', null, 'GWatch runs as a background service on this computer and serves this interface on ', h('code', null, 'http://127.0.0.1:7230'), '. It checks what you tell it to on a schedule, keeps the history in an embedded database beside itself, and sends alerts. There is no cloud account and nothing leaves the machine unless you configure somewhere for it to go.'),
      h('p', null, 'A sensible first hour: create an administrator account under ', h('a', { href: '#/settings/users' }, 'Settings › Users & access'), ', add the router as your first node, add the two or three things you actually notice when they break, then set up email under ', h('a', { href: '#/settings/alerts' }, 'Settings › Alerts'), ' and send the test message.'),
      h('p', { class: 'note' }, 'Install, upgrade and uninstall steps for Windows live in ', doc('INSTALL.md', 'INSTALL.md'), '.'),
    ],
  },
  {
    id: 'nodes',
    title: 'Nodes and check types',
    icon: 'server',
    q: 'ping tcp http https dns certificate keyword json custom script hardware snmp oid router switch template group tag dependency importance interval timeout discover discovery scan subnet range cidr find devices',
    body: () => [
      h('p', null, 'A ', h('b', null, 'node'), ' is one thing you care about — a device, a server, a site — and it holds one or more ', h('b', null, 'checks'), '. Grouping them means a machine goes down once rather than three times, and one dependency ("Plex depends on Gateway") keeps a router outage from looking like a dozen separate failures.'),
      h('ul', { class: 'help-list' },
        h('li', null, h('b', null, 'Ping'), ' — reachability and latency over ICMP, with min/max, jitter and packet loss.'),
        h('li', null, h('b', null, 'HTTP/S'), ' — status-code expectations, redirects, DNS/connect/TLS/first-byte timing and the final URL.'),
        h('li', null, h('b', null, 'HTTPS certificate'), ' — issuer, validity and days remaining, so expiry is a warning and not a surprise.'),
        h('li', null, h('b', null, 'TCP port'), ' — can a connection be opened, which is the honest test for SSH, databases or Plex on 32400.'),
        h('li', null, h('b', null, 'DNS'), ' — does the name resolve, optionally to the addresses you expect.'),
        h('li', null, h('b', null, 'Keyword'), ' and ', h('b', null, 'JSON'), ' — is some text present on a page, or does a value at a path in an API response match.'),
        h('li', null, h('b', null, 'Custom script'), ' — run your own command on a schedule and parse its status from the output.'),
        h('li', null, h('b', null, 'Hardware health'), ' — processor, memory, disk and throughput for this computer or a machine running the agent.'),
        h('li', null, h('b', null, 'SNMP'), ' — read a router, switch or access point directly: interface traffic and errors, processor load, uptime, each OID with its own thresholds and its own chart.'),
      ),
      h('p', null, 'Nodes carry groups, tags, notes and an importance, can be duplicated or disabled, and templates prefill defaults for a website, home server, network device, API, TCP service, DNS name or ping-only device. Every check can be run on demand from the node\'s page, which shows the complete result rather than just a verdict.'),
      h('p', null, h('b', null, 'Discover'), ' on the ', h('a', { href: '#/nodes' }, 'Nodes'), ' page pings a range of addresses — ', h('code', null, '192.168.1.0/24'), ', ', h('code', null, '192.168.1.10-50'), ' — looks up the name of everything that answers, and offers the lot in a list with a suggested template each, so a whole house can be added in one go instead of one address at a time.'),
      h('p', null, h('a', { href: '#/nodes/bulk' }, 'Bulk edit'), ', from the Nodes page, changes one setting — interval, timeout, failure threshold, groups, tags, importance, enabled, alert overrides — across as many nodes and checks as you tick, in one go. A check\'s type is the one thing it will not change, because the type decides what the rest of its configuration means.'),
      h('p', { class: 'note' }, 'History is kept at full resolution for 30 days and then rolled up into 5-minute, hourly and daily summaries, so the database does not grow without bound. ', h('a', { href: '#/settings/retention' }, 'Settings › Retention'), ' spells out exactly what is kept and what is deleted.'),
    ],
  },
  {
    id: 'incidents',
    title: 'Incidents and alerts',
    icon: 'bell',
    q: 'email smtp webhook slack teams ntfy pushover trigger silence maintenance window cooldown suppressed dependency timeline events',
    body: () => [
      h('p', null, 'The ', h('a', { href: '#/incidents' }, 'Incidents'), ' timeline records outages, recoveries, warnings, certificate warnings, alerts sent and suppressed, silences, maintenance windows, configuration changes, service starts and stops, sleep gaps, backups and your own notes. ', h('a', { href: '#/audit' }, 'Audit'), ' is the same log with full search, filters and CSV export.'),
      h('p', null, 'Email alerts fire after a number of consecutive failures, again on recovery, and for warnings such as latency, packet loss or certificate expiry. A cooldown stops a flapping check from filling your inbox, and per-check overrides, manual silencing and one-off or weekly maintenance windows all narrow it further. Any SMTP provider works — STARTTLS on 587, implicit TLS on 465, or none.'),
      h('p', null, 'Alerts are dependency-aware: when the gateway is down you get one message, and the things behind it are recorded as "affected by Gateway".'),
      h('p', null, h('a', { href: '#/settings/automation' }, 'Automation'), ' handles everything that is not email — an HTTP request, a native Slack, Microsoft Teams, ntfy or Pushover notification, a git command, a script, or "run another node\'s checks now". Custom endpoints expose the same actions at ', h('code', null, '/hook/<name>'), ' so a router or a CI job can poke GWatch. Ready-made recipes: ', doc('RECIPES.md', 'RECIPES.md'), '.'),
      h('p', { class: 'note' }, 'Triggers and endpoints run on this computer with the service\'s permissions. Give endpoints a token, and create an administrator account before enabling remote access.'),
    ],
  },
  {
    id: 'hardware',
    title: 'Hardware agents',
    icon: 'cpu',
    q: 'agent gwatch-agent token cpu memory swap disk filesystem throughput machine register install once print status',
    body: () => [
      h('p', null, 'GWatch reads this computer\'s processor, memory and swap, filesystem space, and network and disk throughput by itself. Any other machine needs ', h('code', null, 'gwatch-agent'), ' installed on it; it posts a reading every minute, and its readings appear on the machine\u2019s own node within a minute of starting.'),
      h('p', null, 'Pair the machine from ', h('a', { href: '#/nodes' }, 'Nodes'), ' — GWatch creates its node for you — or register it under ', h('a', { href: '#/settings/hardware' }, 'Settings › Hardware'), ', and it gives you an install command with this server\'s address and a new token already in it. The agent connects outwards and hangs up: GWatch never connects back, holds no credential for that machine, and the token can do exactly one thing — submit that machine\'s readings. A machine that goes quiet is reported as down, which is the point.'),
      h('p', null, 'Before installing anything, ', h('code', null, 'gwatch-agent print'), ' shows exactly what would be sent and contacts nothing, and ', h('code', null, 'gwatch-agent once --server … --token …'), ' sends one reading and exits, which is the quickest way to prove a token works.'),
      h('p', { class: 'note' }, 'Thresholds, what each figure really means and the full agent reference: ', doc('HARDWARE.md', 'HARDWARE.md'), '.'),
    ],
  },
  {
    id: 'wallboards',
    title: 'Wallboards',
    icon: 'monitor',
    q: 'wallboard screen display television project share address token panel headline counts clock kiosk',
    body: () => [
      h('p', null, 'A ', h('a', { href: '#/wallboards' }, 'wallboard'), ' is what a spare screen shows. It is a cousin of a dashboard rather than the same thing: a dashboard is read at a desk by somebody who came looking for an answer, a wallboard is read across a room by somebody who did not, so it has its own panels and its own look.'),
      h('p', null, 'You can have as many as you like — one for the office screen, one for the rack — and each one is arranged from its own panels: a headline, the counts, a clock, what needs attention, groups, a grid of nodes, trend charts, certificates, maintenance, or a line of text for whoever walks past. Columns, theme, type size and how often it reloads are all yours to set.'),
      h('p', null, h('b', null, 'Putting one on a screen.'), ' Switch projection on for a board and GWatch gives you an address. Type that into the browser on the television, tablet or old laptop you want it on, and it shows the board and keeps itself up to date. That screen never signs in and never has to: it can read that one board and nothing else.'),
      h('p', { class: 'note' }, 'An address that needs no sign-in is worth treating like a key to that board. It is shown only to an administrator, it works only while projection is on, and "Change address" takes the old one back at once. Do not forward it to the internet — see ', doc('REMOTE-ACCESS.md', 'REMOTE-ACCESS.md'), '.'),
    ],
  },
  {
    id: 'users',
    title: 'Users and access',
    icon: 'users',
    q: 'account administrator viewer role password api key read-only scope sign in audit argon2 legacy access password',
    body: () => [
      h('p', null, 'Accounts live under ', h('a', { href: '#/settings/users' }, 'Settings › Users & access'), ' and come in two roles. An ', h('b', null, 'administrator'), ' can change everything; a ', h('b', null, 'viewer'), ' sees dashboards, charts, history, incidents and the audit log, and is refused — with an explanation — on every change. GWatch will not let you remove or demote the last administrator.'),
      h('p', null, h('b', null, 'API keys'), ' are for things that are not browsers, and are scoped read-only or read-write. Either scope is always refused on settings, backups and restores, updates, accounts, keys, the configuration export, the service log and anything that runs a trigger or an endpoint. Revoking is immediate, and the audit log keeps the key\'s name so you can still read back what it did.'),
      h('p', null, 'The older ', h('b', null, 'shared access password'), ' — one password, no user name — still works so nothing breaks on upgrade, but it gives everyone the same powers and no name in the log. Leave it empty once you have accounts.'),
      h('p', { class: 'note' }, 'Account passwords are stored as argon2id hashes; session tokens and API keys only as sha256 digests. None of them can be read back out of the database.'),
    ],
  },
  {
    id: 'remote-access',
    title: 'Remote access',
    icon: 'wifi',
    q: 'lan network phone tablet vpn reverse proxy tls port forward tailscale wireguard listen 0.0.0.0 firewall',
    body: () => [
      h('p', null, 'By default the interface answers on this computer only. ', h('a', { href: '#/settings/network' }, 'Settings › Network access'), ' opens it to the rest of your LAN and lists the addresses a phone or tablet can use; a firewall on this computer may still need to allow the port. Starting the service with ', h('code', null, '--listen 0.0.0.0:7230'), ' does the same from the command line.'),
      h('p', null, 'GWatch never becomes an internet-facing service on its own: there is no hosted relay and no tunnel helper. To reach it from outside the house, bring your own way in — a private network (VPN) or a reverse proxy with TLS that you control — and use a viewer account or a read-only API key rather than your administrator credentials.'),
      h('p', { class: 'note' }, 'Never port-forward GWatch\'s HTTP port to the internet: plain HTTP puts your password on the wire in the clear. The full argument and the recommended setups: ', doc('REMOTE-ACCESS.md', 'REMOTE-ACCESS.md'), '.'),
    ],
  },
  {
    id: 'backups',
    title: 'Backups and restore',
    icon: 'save',
    q: 'backup restore archive gwbackup encrypted argon2id aes password schedule automatic move new machine migrate',
    body: () => [
      h('p', null, 'A backup is a single password-encrypted archive (Argon2id + AES-256-GCM) of your configuration, optionally including the whole history and event timeline. Create and download one from ', h('a', { href: '#/settings/backups' }, 'Settings › Backups'), '; scheduled automatic backups run unattended on an interval and keep only the newest few.'),
      h('p', null, 'Restoring that archive on a fresh install is the supported way to move GWatch to another computer or to come back from a reinstall. A backup does not need the ', h('code', null, 'gwatch.key'), ' file that sits beside the database — it carries the settings in the clear inside an archive that is already encrypted with your password.'),
      h('p', { class: 'note' }, 'Step-by-step move to a new machine: ', doc('RESTORE.md', 'RESTORE.md'), '.'),
    ],
  },
  {
    id: 'updates',
    title: 'Updates',
    icon: 'download',
    q: 'upgrade release github version signature ed25519 install in place restart old binary prerelease beta automatic check notify',
    body: () => [
      h('p', null, h('a', { href: '#/settings/updates' }, 'Settings › Updates'), ' lists the releases the project has published and installs the one you choose — the newest, or an earlier one you would rather have. The new executable goes in place of the running one, the previous one is kept as ', h('code', null, '.old'), ', and the service restarts itself.'),
      h('p', null, 'GWatch looks for a new version shortly after it starts, once a day after that, and when you open the interface; an indicator appears beside Settings when one is waiting, and it can offer the update in a dialog as you arrive. All of that switches off in one place, and with it off GWatch contacts nobody until you press ', h('b', null, 'Check now'), '.'),
      h('p', null, 'Pre-releases are hidden until you ask for them. They are published for testing and are not finished work, so installing one is at your own risk.'),
      h('p', null, 'An update is only installed if its ed25519 signature verifies against a release key pinned into the running binary. An unsigned, differently signed or altered download is refused outright, never installed with a warning. A build with no key pinned — the default for forks and local builds — still reports new releases but will not install them.'),
      h('p', { class: 'note' }, 'Your data is untouched by an update; reinstalling over the top on Windows replaces the executable only.'),
    ],
  },
  {
    id: 'troubleshooting',
    title: 'Troubleshooting',
    icon: 'wrench',
    q: 'not responding banner unreachable ping permission agent missing no checks sleep gap smtp test email service stopped scheduler',
    body: () => [
      h('p', null, h('b', null, 'The banner says the service is not responding.'), ' The web page is still open but the GWatch service behind it is not answering — check that the service is running, then use Retry in the banner.'),
      h('p', null, h('b', null, 'Checks are not running.'), ' The service-health line at the foot of the sidebar names the reason: a stopped scheduler, no checks in the last ten minutes, a retention error or a failed backup. ', h('a', { href: '#/settings/health' }, 'Settings › Monitor health'), ' has the detail, including recent internal errors and the log.'),
      h('p', null, h('b', null, 'A quiet period in the history.'), ' When the computer was asleep or off, the timeline records a monitoring gap, so a period with no data is never mistaken for a healthy one.'),
      h('p', null, h('b', null, 'Ping fails but the host is up.'), ' GWatch sends its own echo requests: on Linux and macOS it tries an unprivileged ICMP socket first and then a raw one, on Windows a raw socket, which the service account may open. If none of those is permitted it falls back to the system ', h('code', null, 'ping'), ' command. Settings › General › Ping chooses between that automatic order, the built-in sender alone and the system command alone.'),
      h('p', null, h('b', null, 'Email never arrives.'), ' Send the test message from ', h('a', { href: '#/settings/alerts' }, 'Settings › Alerts'), ' before relying on alerts; it reports the SMTP error verbatim.'),
      h('p', null, h('b', null, 'An agent machine never appears.'), ' Run ', h('code', null, 'gwatch-agent once --server … --token …'), ' on it by hand: a rejected token says so plainly, and a connection failure names what went wrong reaching the server.'),
    ],
  },
  {
    id: 'keyboard',
    title: 'Keyboard',
    icon: 'terminal',
    q: 'shortcuts keys esc escape tab enter space accessibility skip link focus',
    body: () => [
      h('p', null, 'GWatch has no single-key global shortcuts — nothing happens because you leant on a key while reading a dashboard. What it does have is ordinary keyboard behaviour that works everywhere:'),
      h('dl', { class: 'kbd-list' },
        h('dt', null, h('kbd', null, 'Tab'), ' from the top of the page'), h('dd', null, 'Reveals "Skip to content", which jumps past the sidebar.'),
        h('dt', null, h('kbd', null, 'Esc')), h('dd', null, 'Closes the open dialog, dropdown menu or tip.'),
        h('dt', null, h('kbd', null, 'Tab'), ' / ', h('kbd', null, 'Shift'), ' + ', h('kbd', null, 'Tab'), ' in a dialog'), h('dd', null, 'Cycles inside it — focus cannot wander behind the dialog, and returns where it started when the dialog closes.'),
        h('dt', null, h('kbd', null, 'Enter'), ' in a form'), h('dd', null, 'Submits it. In the Audit search box it runs the search.'),
        h('dt', null, h('kbd', null, 'Enter'), ' or ', h('kbd', null, 'Space'), ' on a result row'), h('dd', null, 'Opens that result\'s detail on a node page.'),
        h('dt', null, h('kbd', null, 'Tab'), ' in the script editor'), h('dd', null, 'Inserts two spaces instead of leaving the box, so code stays indentable.'),
        h('dt', null, h('kbd', null, 'Enter'), ' / ', h('kbd', null, 'Esc'), ' in the tour'), h('dd', null, 'Next step, and skip the tour.'),
      ),
    ],
  },
];

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

  root.append(
    h('div', { class: 'filter-bar help-search' },
      h('div', { class: 'toolbar' },
        h('div', { class: 'search' }, icon('search'), search),
        count),
      jump),
    h('div', { class: 'stack' }, tourCard(ctx), results, docsCard(), aboutCard()),
  );
  render();
  return { destroy() {} };
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
    h('h2', null, 'Guided tour and tips'),
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

/* ---------- Documentation, licence and legal ---------- */

function docsCard() {
  const link = (href, label, sub) => h('a', { class: 'help-link', href, target: '_blank', rel: 'noopener' },
    icon('file'), h('span', null, h('b', null, label), sub ? h('span', { class: 'dim' }, sub) : null), icon('external'));
  return h('section', { class: 'card help-docs' },
    h('h2', null, 'Documentation'),
    h('p', { class: 'lead' }, 'The full manual lives with the source on GitHub and always matches the release you are running.'),
    h('div', { class: 'help-links' },
      link(`${DOCS_URL}/INSTALL.md`, 'Installing on Windows', 'Setup program, upgrading, uninstalling'),
      link(`${DOCS_URL}/HARDWARE.md`, 'Hardware health', 'The agent, what is measured, thresholds'),
      link(`${DOCS_URL}/SNMP.md`, 'SNMP checks', 'Enabling SNMP, choosing OIDs, traffic in bits per second'),
      link(`${DOCS_URL}/REMOTE-ACCESS.md`, 'Remote access', 'Reaching GWatch from outside, safely'),
      link(`${DOCS_URL}/RESTORE.md`, 'Restore on a new machine', 'Backup, install, restore'),
      link(`${DOCS_URL}/RECIPES.md`, 'Trigger & endpoint recipes', 'Home Assistant, Discord, ntfy, Docker, git'),
      link(`${DOCS_URL}/API.md`, 'The localhost API', 'Endpoints, roles and the event stream'),
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

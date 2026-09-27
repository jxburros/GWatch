// The words of the Help page and the tutorial, kept in one place (#44).
//
// Help (web/views/help.js) renders TOPICS; the opt-in tutorial (web/tips.js)
// walks TUTORIAL. Both are data here rather than markup in a view so there is
// exactly one copy of each sentence: a topic's `id` is stable and addressable
// as #/help?topic=<id>, which is how docs/USER-GUIDE.md, the tutorial's last
// step and the MCP companion's skill point a person at the right paragraph
// without restating it. Every claim here is one the interface or docs/
// actually makes — help that promises a feature the build does not have is
// worse than no help at all.

import { h, icon, agentLogo } from './components.js';

export const REPO_URL = 'https://github.com/jxburros/GWatch';
export const DOCS_URL = `${REPO_URL}/blob/main/docs`;

export const doc = (path, label) => h('a', { href: `${DOCS_URL}/${path}`, target: '_blank', rel: 'noopener' }, label, icon('external'));

// `q` is the extra vocabulary somebody might search for but that the prose
// does not happen to use; the title and body are searched as well.
export const TOPICS = [
  {
    id: 'getting-started',
    title: 'Getting started',
    icon: 'rocket',
    q: 'setup first run install service port 7230 localhost begin tour onboarding',
    body: () => [
      h('p', null, 'GWatch runs as a background service on this computer and serves this interface on ', h('code', null, 'http://127.0.0.1:7230'), '. It checks what you tell it to on a schedule, keeps the history in an embedded database beside itself, and sends alerts. There is no cloud account and nothing leaves the machine unless you configure somewhere for it to go.'),
      h('p', null, 'A sensible first hour: create an administrator account under ', h('a', { href: '#/settings/users' }, 'Settings › Users & access'), ', add the router as your first node, add the two or three things you actually notice when they break, then set up email under ', h('a', { href: '#/settings/alerts' }, 'Settings › Alerts'), ' and send the test message.'),
      h('p', null, 'Would rather be shown round? ', h('b', null, 'Start the tutorial'), ' at the top of this page: it walks through the real screens one control at a time, and stops the moment you want it to.'),
      h('p', { class: 'note' }, 'Install, upgrade and uninstall steps for Windows live in ', doc('INSTALL.md', 'INSTALL.md'), '; the container image in ', doc('DOCKER.md', 'DOCKER.md'), '.'),
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
        h('li', null, h('b', null, 'Keyword'), ' and ', h('b', null, 'JSON'), ' — is some text present on a page, or does a value at a path in an API response match. Tick ', h('b', null, 'Record this value'), ' on a JSON check and the number at that path is kept with every run, charted with its unit and held to its own warning and critical levels — any API becomes a time series.'),
        h('li', null, h('b', null, 'Custom script'), ' — run your own command on a schedule and parse its status from the output.'),
        h('li', null, h('b', null, 'Hardware health'), ' — processor, memory, swap, load, every disk and its inodes, every network interface and disk throughput, for this computer or a machine running the agent. Each of those readings stands on its own, with its own value, status, threshold, chart and incident.'),
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
    q: 'email smtp webhook slack teams ntfy pushover trigger silence maintenance window cooldown suppressed dependency timeline events rule rules across nodes all any at least quorum',
    body: () => [
      h('p', null, 'The ', h('a', { href: '#/incidents' }, 'Incidents'), ' timeline records outages, recoveries, warnings, certificate warnings, alerts sent and suppressed, silences, maintenance windows, configuration changes, service starts and stops, sleep gaps, backups and your own notes. ', h('a', { href: '#/audit' }, 'Audit'), ' is the same log with full search, filters and CSV export.'),
      h('p', null, 'Email alerts fire after a number of consecutive failures, again on recovery, and for warnings such as latency, packet loss or certificate expiry. A cooldown stops a flapping check from filling your inbox, and per-check overrides, manual silencing and one-off or weekly maintenance windows all narrow it further. Any SMTP provider works — STARTTLS on 587, implicit TLS on 465, or none.'),
      h('p', null, 'Alerts are dependency-aware: when the gateway is down you get one message, and the things behind it are recorded as "affected by Gateway".'),
      h('p', null, h('a', { href: '#/settings/rules' }, 'Notification rules'), ' look across nodes: "tell me when two of my three DNS servers are down". A rule is a list of conditions on any checks or whole nodes, joined by all, any or at-least-N, with the same actions triggers use, a cooldown and an optional notice when it clears. It fires once each time it becomes true and keeps its state across restarts.'),
      h('p', null, h('a', { href: '#/settings/automation' }, 'Automation'), ' handles everything that is not email — an HTTP request, a native Slack, Microsoft Teams, ntfy or Pushover notification, a git command, a script, or "run another node\'s checks now". Custom endpoints expose the same actions at ', h('code', null, '/hook/<name>'), ' so a router or a CI job can poke GWatch. Ready-made recipes: ', doc('RECIPES.md', 'RECIPES.md'), '.'),
      h('p', { class: 'note' }, 'Triggers and endpoints run on this computer with the service\'s permissions. Give endpoints a token, and create an administrator account before enabling remote access.'),
    ],
  },
  {
    id: 'charts',
    title: 'Charts and dashboards',
    icon: 'chart',
    q: 'chart graph metric cpu memory disk network bandwidth throughput latency loss jitter availability axis units compare overlay save pin export png csv dashboard widget',
    body: () => [
      h('p', null, 'The ', h('a', { href: '#/charts' }, 'Charts'), ' page draws any history GWatch keeps. Pick the series you want — latency, jitter, packet loss or availability for any check, and every metric a check measures: a machine\u2019s processor, memory, each disk and each network interface, an SNMP reading, a JSON check\u2019s recorded value — from as many nodes as you like.'),
      h('p', null, 'Series in different units share one chart: the first unit is read off the left-hand axis and the second off the right, and each line\u2019s legend entry says which unit it is in, so ping time from the router and processor load on the NAS can sit on the same timeline. Throughput is shown as a rate a person reads — KB/s, MB/s, GB/s — rather than a count of bytes.'),
      h('p', null, 'A chart can be saved under a name, exported as a PNG or as CSV, or pinned to a ', h('a', { href: '#/dashboard' }, 'dashboard'), ' as a widget with exactly the same settings. Dashboards are separate boards of widgets — status tiles, what needs attention, groups, certificates, charts — each laid out on its own grid.'),
      h('p', { class: 'note' }, 'Every chart can be walked with the arrow keys once it has focus, and has its figures under "View as table".'),
    ],
  },
  {
    id: 'hardware',
    title: 'Hardware agents',
    icon: 'cpu',
    q: 'agent gwatch-agent pair pairing code token cpu memory swap disk filesystem inodes throughput network bandwidth machine install once print status update rollback self-update version winget homebrew brew deb rpm apt docker',
    body: () => [
      h('div', { class: 'agent-intro' },
        agentLogo(),
        h('p', null, 'GWatch reads this computer\u2019s processor, memory and swap, filesystem space, and network and disk throughput by itself. Any other machine needs ', h('code', null, 'gwatch-agent'), ' — the GWatch Agent — installed on it; it posts a reading every minute, and its readings appear on the machine\u2019s own node within a minute of starting.')),
      h('p', null, h('b', null, 'Pairing.'), ' Press ', h('b', null, 'Pair a machine'), ' on ', h('a', { href: '#/nodes' }, 'Nodes'), ' and GWatch shows an eight-character code, good for one machine for a quarter of an hour. Type it into the agent\u2019s Windows installer, or run ', h('code', null, 'gwatch-agent install --server … --code …'), ' on the machine. The dialog that shows the code watches it: it says so as soon as the machine has paired, and again when its first reading arrives.'),
      h('p', null, 'The agent connects outwards and hangs up: GWatch never connects back, holds no credential for that machine, and what the machine ends up holding can do exactly one thing — submit that machine\u2019s readings. A machine that goes quiet is reported as down, which is the point.'),
      h('p', null, h('b', null, 'Keeping agents up to date.'), ' The agent has its own version and its own releases, and a running agent installs newer ones by itself after checking their signature. GWatch cannot tell an agent to update and cannot push anything to it; ', h('a', { href: '#/settings/hardware' }, 'Settings › Hardware'), ' only marks machines that are behind. An agent installed from a package manager leaves updating to that package manager.'),
      h('p', null, 'Before installing anything, ', h('code', null, 'gwatch-agent print'), ' shows exactly what would be sent and contacts nothing.'),
      h('p', { class: 'note' }, 'Thresholds, what each figure really means, packages and the full agent reference: ', doc('HARDWARE.md', 'HARDWARE.md'), '.'),
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
      h('p', null, 'A browser stays signed in for 30 days from when it was last used. Changing an account\u2019s role takes effect at once in every browser already signed in to it — the page redraws itself for the new role — while changing its password signs it out everywhere.'),
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
      h('p', null, 'A browser on this computer is let in without signing in. A visitor arriving through a reverse proxy running on this computer is not: the proxy says it is passing a request on, and GWatch treats that request as the remote one it is.'),
      h('p', { class: 'note' }, 'Never port-forward GWatch\'s HTTP port to the internet: plain HTTP puts your password on the wire in the clear. The full argument and the recommended setups: ', doc('REMOTE-ACCESS.md', 'REMOTE-ACCESS.md'), '.'),
    ],
  },
  {
    id: 'ai-mcp',
    title: 'AI assistants (MCP)',
    icon: 'terminal',
    q: 'ai assistant mcp claude model context protocol gwatch-mcp skill read-only api key companion',
    body: () => [
      h('p', null, 'GWatch has no AI features of its own. What it offers is a way for an assistant you already use to ', h('i', null, 'read'), ' your monitoring: ', h('code', null, 'gwatch-mcp'), ', a separate companion program that speaks the Model Context Protocol and talks to GWatch over its API with a key you give it.'),
      h('p', null, h('a', { href: '#/settings/mcp' }, 'Settings › AI & MCP'), ' walks through it: create a read-only API key, install the companion, copy a client configuration with this install\u2019s address already filled in, and check it with ', h('code', null, 'gwatch-mcp check'), '. It also offers an ', h('b', null, 'agent skill'), ' to download, which teaches an assistant how to use GWatch well.'),
      h('p', { class: 'note' }, 'With a read-only key an assistant can look and never touch. What it can and cannot do, and the trust model: ', h('a', { href: `${REPO_URL}/blob/main/mcp/README.md`, target: '_blank', rel: 'noopener' }, 'mcp/README.md', icon('external')), '.'),
    ],
  },
  {
    id: 'database',
    title: 'Database and running in Docker',
    icon: 'server',
    q: 'database sqlite postgres postgresql mysql mariadb server migrate-db move docker container compose image volume data directory driver',
    body: () => [
      h('p', null, 'GWatch keeps everything in one SQLite database beside itself, which needs no setup and is what every release is tested against first. If you already run a PostgreSQL or MySQL/MariaDB server, ', h('a', { href: '#/settings/database' }, 'Settings › Database'), ' can point GWatch at it: it tests the connection first and takes effect at the next restart. ', h('code', null, 'gwatch migrate-db'), ' copies an existing SQLite install across in one go.'),
      h('p', null, 'GWatch is also published as a container image, ', h('code', null, 'ghcr.io/jxburros/gwatch'), ', with its data in a ', h('code', null, '/data'), ' volume. Inside a container the hardware readings are the container\u2019s own, so watch the host with the agent.'),
      h('p', { class: 'note' }, 'Choosing and moving a database: ', doc('DATABASE.md', 'DATABASE.md'), '. The container, ping capabilities and discovery under bridge networking: ', doc('DOCKER.md', 'DOCKER.md'), '.'),
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
      h('p', null, h('b', null, 'An agent machine never appears.'), ' If the pairing dialog never said "paired", the code was not used: it may have expired or been mistyped — close the dialog and pair again. If it paired but never reported, run ', h('code', null, 'gwatch-agent once'), ' on that machine by hand: a rejected token says so plainly, and a connection failure names what went wrong reaching the server.'),
      h('p', null, h('b', null, '"GWatch could not check who you are just now".'), ' The service could not read its own database for a moment, so it asked the browser to try again rather than signing you out. If it persists, ', h('a', { href: '#/settings/health' }, 'Settings › Monitor health'), ' and the service log say why.'),
    ],
  },
  {
    id: 'keyboard',
    title: 'Keyboard',
    icon: 'terminal',
    q: 'shortcuts keys esc escape tab enter space arrow accessibility skip link focus menu chart widget move resize',
    body: () => [
      h('p', null, 'GWatch has no single-key global shortcuts — nothing happens because you leant on a key while reading a dashboard. What it does have is ordinary keyboard behaviour that works everywhere:'),
      h('dl', { class: 'kbd-list' },
        h('dt', null, h('kbd', null, 'Tab'), ' from the top of the page'), h('dd', null, 'Reveals "Skip to content", which jumps past the sidebar.'),
        h('dt', null, h('kbd', null, 'Esc')), h('dd', null, 'Closes the open dialog, dropdown menu or tip.'),
        h('dt', null, h('kbd', null, 'Tab'), ' / ', h('kbd', null, 'Shift'), ' + ', h('kbd', null, 'Tab'), ' in a dialog'), h('dd', null, 'Cycles inside it — focus cannot wander behind the dialog, and returns where it started when the dialog closes.'),
        h('dt', null, h('kbd', null, 'Enter'), ' in a form'), h('dd', null, 'Submits it. In the Audit search box it runs the search.'),
        h('dt', null, h('kbd', null, 'Enter'), ' or ', h('kbd', null, 'Space'), ' on a result row'), h('dd', null, 'Opens that result\'s detail on a node page.'),
        h('dt', null, h('kbd', null, 'Tab'), ' in the script editor'), h('dd', null, 'Inserts two spaces instead of leaving the box, so code stays indentable. To leave it, press ', h('kbd', null, 'Esc'), ' and then ', h('kbd', null, 'Tab'), ', or ', h('kbd', null, 'Shift'), ' + ', h('kbd', null, 'Tab'), '.'),
        h('dt', null, h('kbd', null, '↑'), ' / ', h('kbd', null, '↓'), ' in a menu'), h('dd', null, 'Move between its items; ', h('kbd', null, 'Home'), ' and ', h('kbd', null, 'End'), ' jump to the ends.'),
        h('dt', null, h('kbd', null, '←'), ' / ', h('kbd', null, '→'), ' on a chart'), h('dd', null, 'Step the tooltip through the points of a focused chart; every chart also has its figures under "View as table".'),
        h('dt', null, h('kbd', null, '←'), ' / ', h('kbd', null, '→'), ' on the wallboard tabs'), h('dd', null, 'Switch between boards.'),
        h('dt', null, 'Arrow keys on a widget\'s grip'), h('dd', null, 'Move the widget one cell on the dashboard grid; with ', h('kbd', null, 'Shift'), ' held they resize it. The new place is read out as it changes.'),
        h('dt', null, h('kbd', null, 'Enter'), ' / ', h('kbd', null, 'Esc'), ' in the tour'), h('dd', null, 'Next step, and skip the tour.'),
        h('dt', null, h('kbd', null, 'Enter'), ' / ', h('kbd', null, 'Esc'), ' in the tutorial'), h('dd', null, 'Next step (focus starts on Next), and pause the tutorial where it is — Help offers to carry on from there.'),
      ),
    ],
  },
];


/* ---------- The tutorial ---------- */
// One step per control worth knowing, in the order someone new would meet
// them. Each names the page it lives on (`route`, exactly as the address bar
// has it after "#"), the control to point at (`anchor`, a selector; the first
// visible match wins) and two or three sentences. A step whose control is not
// there — an administrator's button seen by a viewer, a dashboard with no
// tabs — is still shown, in the middle of the page, so the sequence never
// skips a beat. Keep the words consistent with the TOPICS above; the last step
// hands over to them.
export const TUTORIAL = [
  {
    id: 'indicators',
    route: '/dashboard',
    anchor: '#indicators',
    place: 'bottom',
    title: 'Everything at a glance',
    body: 'These orbs are the state of everything GWatch watches. Each one is a link: a red one goes straight to the nodes that are down. Which conditions light which colour is yours to choose under Settings › Indicators.',
  },
  {
    id: 'dashboard',
    route: '/dashboard',
    anchor: '.dash-tabs, .dash-grid',
    place: 'bottom',
    title: 'Dashboards are yours to arrange',
    body: 'A dashboard is a board of widgets — status tiles, what needs attention, groups, certificates, charts. Have as many as you like, drag widgets by their grip and pull their edges to resize them.',
  },
  {
    id: 'nodes',
    route: '/nodes',
    anchor: '.filter-bar',
    place: 'bottom',
    title: 'Nodes hold your checks',
    body: 'A node is one thing you care about — a router, a server, a website — and holds the checks that watch it. Filter by status, group or tag here; the filters live in the address bar, so a filtered list can be bookmarked.',
  },
  {
    id: 'add',
    route: '/nodes',
    anchor: '#page-actions',
    place: 'bottom',
    title: 'Three ways to add things',
    body: 'Add a node by hand from a template, Discover a whole subnet at once, or Pair a machine to watch its hardware with the GWatch Agent. Pairing shows a short code, and the dialog tells you the moment the machine has paired and reported.',
  },
  {
    id: 'charts',
    route: '/charts',
    anchor: '.charts-side',
    place: 'right',
    title: 'Chart anything, together',
    body: 'Pick any series GWatch keeps — latency, loss, a disk, a network interface, an SNMP reading — from any nodes. Different units share the chart on a left and a right axis. Save a chart, export it, or pin it to a dashboard.',
  },
  {
    id: 'incidents',
    route: '/incidents',
    anchor: '.filter-bar, .timeline',
    place: 'bottom',
    title: 'What happened, and what GWatch did about it',
    body: 'Outages, recoveries, warnings, the alerts sent — and the ones deliberately not sent, such as everything behind a gateway that was already down. Audit is the same record with full search and CSV export.',
  },
  {
    id: 'alerts',
    route: '/settings/alerts',
    anchor: '.settings-nav a[href="#/settings/alerts"]',
    place: 'right',
    title: 'Alerts by email',
    body: 'Set up your mail server here and send the test message before relying on it. Alerts wait for a number of consecutive failures, tell you on recovery, and have a cooldown so a flapping check does not fill your inbox.',
  },
  {
    id: 'rules',
    route: '/settings/rules',
    anchor: '.settings-nav a[href="#/settings/rules"]',
    place: 'right',
    title: 'Rules across nodes',
    body: '"Tell me when two of my three DNS servers are down." A rule joins conditions on any checks or nodes with all, any or at-least-N, and runs the same actions a trigger can — email, a webhook, Slack, ntfy and more.',
  },
  {
    id: 'hardware',
    route: '/settings/hardware',
    anchor: '.settings-nav a[href="#/settings/hardware"]',
    place: 'right',
    title: 'The machines that report in',
    body: 'Every machine running the agent is listed here, with the version it runs. Agents keep themselves up to date; GWatch only marks the ones that are behind, and has no way to change anything on them.',
  },
  {
    id: 'users',
    route: '/settings/users',
    anchor: '.settings-nav a[href="#/settings/users"]',
    place: 'right',
    title: 'Accounts before remote access',
    body: 'Create an administrator account before opening GWatch to the rest of the network. A viewer account sees everything and changes nothing, and every change lands in the audit log under a name.',
  },
  {
    id: 'help',
    route: '/help',
    anchor: '.help-search',
    place: 'bottom',
    title: 'The rest is in Help',
    body: 'Search Help for anything — "agent", "certificate", "backup". This tutorial stays here to take again, and the small tips can be switched on from the same place.',
  },
];

// web/views/settings.js: an administrator sees every tab and lands on
// "General" by default, with the mock's instance name filled into the form.

import '../dom.mjs';
import '../../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as settingsView from '../../../web/views/settings.js';
import { api } from '../../../web/api.js';
import { mountView, waitFor } from '../view-harness.mjs';

test('settings view (admin) lists every tab and opens on General', async (t) => {
  const { root } = await mountView(settingsView, undefined, t);
  const tabLinks = [...root.querySelectorAll('.settings-nav a')];
  const tabLabels = tabLinks.map((a) => a.textContent);
  assert.deepEqual(tabLabels, [
    'General', 'Appearance', 'Indicators', 'Users & access', 'Network access', 'Alerts', 'Automation', 'Rules',
    'AI & MCP', 'Retention', 'Maintenance', 'Backups', 'Database', 'Updates', 'Monitor health', 'About',
  ]);
  assert.ok(tabLinks.find((a) => a.textContent === 'General').classList.contains('active'));
  const nameInput = root.querySelector('.settings-panel input[type="text"]');
  assert.equal(nameInput.value, 'Home monitor'); // web/mock.js's settings.general.instanceName
});

test('settings view (viewer) is limited to the tabs marked viewer: true', async (t) => {
  const { root } = await mountView(settingsView, { isAdmin: false }, t);
  const tabLabels = [...root.querySelectorAll('.settings-nav a')].map((a) => a.textContent);
  assert.deepEqual(tabLabels, ['Appearance', 'Monitor health', 'About']);
  assert.match(root.textContent, /signed in as a viewer/);
});

// #47: the General tab carries a Ping card with the three-way method choice,
// set from settings.general.pingMethod.
test('settings General has a Ping card with the method choice', async (t) => {
  const { root } = await mountView(settingsView, undefined, t);
  const panel = root.querySelector('.settings-panel');
  const heads = [...panel.querySelectorAll('h2')].map((el) => el.textContent);
  assert.ok(heads.includes('Ping'), `a Ping card (got ${heads.join(', ')})`);

  const select = [...panel.querySelectorAll('select')].find((el) => [...el.options].some((o) => o.value === 'builtin'));
  assert.ok(select, 'the ping method select is on the tab');
  assert.deepEqual([...select.options].map((o) => o.value), ['auto', 'builtin', 'system']);
  assert.equal(select.value, 'auto'); // web/mock.js's settings.general.pingMethod
  assert.match(panel.textContent, /falls back to the system ping command/);
});

// #131: Monitor health says which way ping works and why not the first choice.
test('settings Monitor health shows the ping method and the reason it is not the first choice', async (t) => {
  const { root } = await mountView(settingsView, { params: { tab: 'health' } }, t);
  await waitFor(() => /Built-in \(raw socket\)/.test(root.textContent));
  const card = [...root.querySelectorAll('.health-card')].find((c) => c.querySelector('.hl')?.textContent === 'Ping');
  assert.ok(card, 'a Ping health card');
  assert.match(card.textContent, /Method setting: auto/);
  assert.match(card.textContent, /windows has no unprivileged ICMP socket/);
  assert.doesNotMatch(card.textContent, /How to allow ping/, 'no help link when the built-in ping works');
});

// #56: the AI & MCP tab carries the set-up guide (a read-only key first), the
// skill download, and — because web/mock.js's fixture has an older download
// behind it — the quiet "updated since" note inside the skill card only.
test('settings AI & MCP tab has the setup guide, the skill download and the update note', async (t) => {
  const { root } = await mountView(settingsView, { params: { tab: 'mcp' } }, t);
  const panel = root.querySelector('.settings-panel');
  const heads = [...panel.querySelectorAll('h2')].map((el) => el.textContent);
  assert.deepEqual(heads, ['AI assistants and MCP', 'Set it up', 'What the assistant can and cannot do', 'Agent skill']);

  assert.match(panel.textContent, /Create a read-only API key/);
  assert.ok(panel.querySelector('a[href="#/settings/users"]'), 'links to Users & access');
  assert.match(panel.textContent, /go install github\.com\/jxburros\/GWatch\/mcp\/cmd\/gwatch-mcp@latest/);
  assert.match(panel.textContent, /"GWATCH_API_KEY": "gw_paste_your_read_only_key_here"/);
  assert.match(panel.textContent, /claude mcp add gwatch/);
  assert.match(panel.textContent, /gwatch-mcp check/);

  const dl = panel.querySelector('a[href="/api/mcp/skill"]');
  assert.ok(dl, 'the skill download link');
  assert.equal(dl.getAttribute('download'), 'gwatch-skill-1.1.0.zip');
  assert.match(dl.textContent, /Download skill 1\.1\.0/);
  assert.ok(panel.querySelector('a[href="/api/mcp/skill?format=md"]'), 'the SKILL.md-only link');
  assert.match(panel.textContent, /Last downloaded .* \(version 1\.0\.0\) by local/);

  // The update note is an inline info banner inside the skill card, and the
  // only one on the tab.
  const banners = [...panel.querySelectorAll('.banner-info')];
  assert.equal(banners.length, 1);
  assert.match(banners[0].textContent, /updated since it was last downloaded \(1\.0\.0 → 1\.1\.0\)/);
  assert.ok(banners[0].closest('.card').textContent.includes('Agent skill'));
  assert.equal(document.querySelectorAll('.toast, .modal').length, 0, 'no toast or modal for the update');
});

// #34: the Database tab shows the backend in use, offers the three choices
// with the server fields hidden for SQLite, tests a connection without
// saving, and after a save says plainly that a restart is needed.
test('settings Database tab shows the active backend and saves a server for the next start', async (t) => {
  const { root } = await mountView(settingsView, { params: { tab: 'database' } }, t);
  const panel = root.querySelector('.settings-panel');
  const heads = [...panel.querySelectorAll('h2')].map((el) => el.textContent);
  assert.deepEqual(heads, ['Database in use', 'Database for the next start', 'Moving existing data']);
  assert.match(panel.textContent, /SQLite/);
  assert.match(panel.textContent, /modernc\.org\/sqlite/);
  assert.match(panel.textContent, /gwatch\.db/);

  const driver = [...panel.querySelectorAll('select')].find((el) => [...el.options].some((o) => o.value === 'postgres'));
  assert.ok(driver, 'the driver select');
  assert.deepEqual([...driver.options].map((o) => o.value), ['sqlite', 'postgres', 'mysql']);
  assert.equal(driver.value, 'sqlite');
  const hostInput = [...panel.querySelectorAll('input')].find((el) => el.placeholder === 'db.example.lan');
  assert.ok(hostInput.closest('.form-grid').hidden, 'server fields are hidden while SQLite is chosen');
  assert.equal(panel.querySelectorAll('.banner-warn').length, 0, 'no restart banner while nothing is saved');

  // Choose PostgreSQL: the server fields appear and the port follows.
  driver.value = 'postgres';
  driver.dispatchEvent(new window.Event('change'));
  assert.equal(hostInput.closest('.form-grid').hidden, false);
  const portInput = [...panel.querySelectorAll('input[type="number"]')][0];
  assert.equal(portInput.value, '5432');
  const set = (el, v) => { el.value = v; el.dispatchEvent(new window.Event('input')); };
  set(hostInput, 'db.lan');
  set([...panel.querySelectorAll('input')].find((el) => el.placeholder === 'gwatch' && el.type === 'text'), 'gwatch');
  set(panel.querySelector('input[type="password"]'), 'pw');
  const dbInput = [...panel.querySelectorAll('input')].filter((el) => el.placeholder === 'gwatch' && el.type === 'text')[1];
  set(dbInput, 'gwatch');

  // Test connection reports the server version and saves nothing.
  const testBtn = [...panel.querySelectorAll('button')].find((b) => b.textContent.includes('Test connection'));
  testBtn.click();
  await waitFor(() => panel.querySelector('.banner-up'));
  assert.match(panel.querySelector('.banner-up').textContent, /PostgreSQL 16\.4/);
  assert.equal(panel.querySelectorAll('.banner-warn').length, 0, 'testing does not save');

  // Save: the confirmation is accepted, and the restart banner appears.
  const saveBtn = [...panel.querySelectorAll('button')].find((b) => b.textContent.trim() === 'Save');
  saveBtn.click();
  const confirm = await waitFor(() => [...document.querySelectorAll('.modal button')].find((b) => b.textContent.trim() === 'Save'));
  confirm.click();
  await waitFor(() => panel.querySelector('.banner-warn'));
  assert.match(panel.textContent, /GWatch will use this database after the service is restarted/);
  assert.match(panel.textContent, /PostgreSQL at db\.lan:5432\/gwatch/);
});

// Machines are nodes, so there is no Hardware section under Settings any more:
// adding one is part of Add node, and managing one is done on its node. An old
// link to the section still goes somewhere sensible.
test('settings no longer has a Hardware section, and the old link goes to Nodes', async (t) => {
  const { root, ctx } = await mountView(settingsView, { params: { tab: 'hardware' } }, t);
  assert.deepEqual(ctx.navigated, ['/nodes']);
  assert.equal(root.querySelector('.settings-nav'), null, 'nothing of Settings is drawn for a link that is passed on');
});

// A viewer's theme is "remembered by this browser", as the tab says: the shell
// styles the page with lookFor(me) on every identity check, and that used to
// be the shared theme alone, so the viewer's choice lasted until the next one.
test('a viewer\'s own theme outlasts the next identity check', async (t) => {
  const { lookFor } = await import('../../../web/components.js');
  t.after(() => { try { localStorage.removeItem('gw.ownLook'); } catch { /* ignore */ } });
  const viewer = { isAdmin: false, theme: 'dark', accentColor: '#43c9c0' };
  assert.deepEqual(lookFor(viewer), { theme: 'dark', accentColor: '#43c9c0' });
  const { root } = await mountView(settingsView, { isAdmin: false, params: { tab: 'appearance' } }, t);
  const light = await waitFor(() => [...root.querySelectorAll('.theme-option')].find((b) => b.textContent.startsWith('Light')));
  light.click();
  assert.equal(lookFor(viewer).theme, 'light', 'the shell keeps the viewer\'s choice');
  assert.equal(lookFor({ ...viewer, isAdmin: true }).theme, 'dark', 'an administrator follows the shared setting');
});

// #49: Users & access carries the password-hashing choice. Argon2id is the
// default and labelled as recommended; choosing PBKDF2 saves the setting
// straight away, like the other switches on this tab.
test('settings Users & access offers the password hashing choice and saves it', async (t) => {
  const { root } = await mountView(settingsView, { params: { tab: 'users' } }, t);
  const panel = root.querySelector('.settings-panel');
  await waitFor(() => [...panel.querySelectorAll('h2')].some((el) => el.textContent === 'Password hashing'));
  const select = [...panel.querySelectorAll('select')].find((el) => [...el.options].some((o) => o.value === 'pbkdf2-sha256'));
  assert.ok(select, 'the password hashing select');
  assert.deepEqual([...select.options].map((o) => o.value), ['argon2id', 'pbkdf2-sha256']);
  assert.match(select.options[0].textContent, /Argon2id \(recommended\)/);
  assert.match(select.options[1].textContent, /PBKDF2 \(standard library only\)/);
  assert.equal(select.value, 'argon2id'); // web/mock.js's settings.general.passwordHashing
  assert.match(panel.textContent, /Argon2id remains the recommendation/);

  select.value = 'pbkdf2-sha256';
  select.dispatchEvent(new window.Event('change'));
  await waitFor(() => [...document.querySelectorAll('.toast')].find((el) => /Settings saved/.test(el.textContent)));
  const saved = await api.get('/api/settings');
  assert.equal(saved.general.passwordHashing, 'pbkdf2-sha256');
  // Put the mock back the way the other tests expect it.
  await api.put('/api/settings', { ...saved, general: { ...saved.general, passwordHashing: 'argon2id' } });
});

// A refused save leaves the control showing what is stored, not the choice
// that was refused (it used to read the edit back as the saved value).
test('a refused password hashing change puts the select back', async (t) => {
  const { root } = await mountView(settingsView, { params: { tab: 'users' } }, t);
  const panel = root.querySelector('.settings-panel');
  await waitFor(() => [...panel.querySelectorAll('h2')].some((el) => el.textContent === 'Password hashing'));
  const select = [...panel.querySelectorAll('select')].find((el) => [...el.options].some((o) => o.value === 'pbkdf2-sha256'));
  const realFetch = window.fetch;
  window.fetch = async (url, init) => (init?.method === 'PUT' && String(url) === '/api/settings'
    ? new Response(JSON.stringify({ error: 'settings are read-only right now' }), { status: 409, headers: { 'Content-Type': 'application/json' } })
    : realFetch(url, init));
  t.after(() => { window.fetch = realFetch; });
  select.value = 'pbkdf2-sha256';
  select.dispatchEvent(new window.Event('change'));
  await waitFor(() => [...document.querySelectorAll('.toast-error')].find((el) => /read-only right now/.test(el.textContent)));
  assert.equal(select.value, 'argon2id');
});

// A rule with no `enabled` field showed its switch on but its row dimmed as
// off, and saved as disabled; it is on, everywhere, and saved that way.
test('an indicator rule without an enabled field is on in its switch, its row and its save', async (t) => {
  const before = await api.get('/api/settings');
  t.after(() => api.put('/api/settings', before));
  const { enabled, ...bare } = before.indicators[0];
  assert.equal(enabled, true);
  await api.put('/api/settings', { ...before, indicators: [bare] });
  const { root } = await mountView(settingsView, { params: { tab: 'indicators' } }, t);
  const row = await waitFor(() => root.querySelector('.ind-rule'));
  assert.equal(row.querySelector('input[role="switch"]').checked, true);
  assert.equal(row.classList.contains('off'), false, 'the row is not dimmed as off');
  document.querySelectorAll('.toast').forEach((el) => el.remove());
  [...root.querySelectorAll('button')].find((b) => /Save changes/.test(b.textContent)).click();
  await waitFor(() => [...document.querySelectorAll('.toast')].find((el) => /Settings saved/.test(el.textContent)));
  assert.equal((await api.get('/api/settings')).indicators[0].enabled, true);
});

// A window for a group nobody is in any more could not show that group in its
// select, read back blank, and was saved as a window for every node.
test('saving a maintenance window keeps a group that no node is in any more', async (t) => {
  const made = await api.post('/api/maintenance', { name: 'Old racks', nodeId: null, group: 'Retired racks', enabled: true, startAt: new Date(Date.now() + 3600e3).toISOString(), endAt: new Date(Date.now() + 7200e3).toISOString(), weekdays: [], durationMinutes: 0, notes: '' });
  t.after(() => api.del(`/api/maintenance/${made.id}`));
  const { root } = await mountView(settingsView, { params: { tab: 'maintenance' } }, t);
  const row = await waitFor(() => [...root.querySelectorAll('.maint-row')].find((r) => r.textContent.includes('Old racks')));
  [...row.querySelectorAll('button')].find((b) => b.textContent.includes('Edit')).click();
  const dialog = document.getElementById('modals').lastElementChild;
  [...dialog.querySelectorAll('.modal-foot button')].find((b) => b.textContent === 'Save').click();
  await waitFor(() => [...document.querySelectorAll('.toast')].find((el) => /Window saved/.test(el.textContent)));
  const saved = (await api.get('/api/maintenance')).find((w) => w.id === made.id);
  assert.equal(saved.group, 'Retired racks');
});

test('a maintenance window for "a group" with no group to pick is refused, not saved for every node', async (t) => {
  const realFetch = window.fetch;
  const posts = [];
  window.fetch = async (url, init) => {
    if (String(url) === '/api/groups') return new Response(JSON.stringify({ groups: [], tags: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    if (init?.method === 'POST' && String(url) === '/api/maintenance') posts.push(JSON.parse(init.body));
    return realFetch(url, init);
  };
  t.after(() => { window.fetch = realFetch; });
  const { root } = await mountView(settingsView, { params: { tab: 'maintenance' } }, t);
  const add = await waitFor(() => [...root.querySelectorAll('button')].find((b) => b.textContent.includes('New window')));
  add.click();
  const dialog = document.getElementById('modals').lastElementChild;
  dialog.querySelector('input').value = 'Racks';
  const scope = [...dialog.querySelectorAll('select')].find((s) => [...s.options].some((o) => o.value === 'group'));
  scope.value = 'group';
  scope.dispatchEvent(new window.Event('change'));
  [...dialog.querySelectorAll('.modal-foot button')].find((b) => b.textContent === 'Create').click();
  await waitFor(() => [...document.querySelectorAll('.toast-error')].find((el) => /group/i.test(el.textContent)));
  assert.equal(posts.length, 0, 'nothing was sent');
  dialog.querySelector('.modal-foot button').click();
});

// The confirm box said "leave blank to keep the saved one", and the save took
// that to mean a newly typed password needed no confirming: one typo and
// every automatic backup from then on was locked with a password nobody knew.
test('a new automatic-backup password is not saved without its confirmation', async (t) => {
  const before = await api.get('/api/settings');
  t.after(() => api.put('/api/settings', before));
  const { root } = await mountView(settingsView, { params: { tab: 'backups' } }, t);
  const pw = await waitFor(() => [...root.querySelectorAll('input[type="password"]')].find((el) => /Required to enable automatic backups/.test(el.placeholder)));
  pw.value = 'tpyo-secret';
  pw.dispatchEvent(new window.Event('input'));
  document.querySelectorAll('.toast').forEach((el) => el.remove());
  [...root.querySelectorAll('button')].find((b) => b.textContent.includes('Save automatic backup settings')).click();
  await waitFor(() => [...document.querySelectorAll('.toast')].find((el) => /do not match|confirm/i.test(el.textContent) || /Settings saved/.test(el.textContent)));
  assert.notEqual((await api.get('/api/settings')).backups.password, 'tpyo-secret');
});

// A save replaces the settings document with the server's copy, and the form
// went on writing into the old one, so a second edit and save on the same tab
// said "Settings saved" and sent nothing new.
test('a second save from the same settings tab sends the second edit', async (t) => {
  const before = await api.get('/api/settings');
  t.after(() => api.put('/api/settings', before));
  const { root } = await mountView(settingsView, undefined, t);
  const name = root.querySelector('.settings-panel input[type="text"]');
  const save = [...root.querySelectorAll('.settings-panel button')].find((b) => b.textContent.includes('Save changes'));
  for (const value of ['First name', 'Second name']) {
    name.value = value;
    name.dispatchEvent(new window.Event('input'));
    document.querySelectorAll('.toast').forEach((el) => el.remove());
    save.click();
    await waitFor(() => [...document.querySelectorAll('.toast')].find((el) => /Settings saved/.test(el.textContent)));
    assert.equal((await api.get('/api/settings')).general.instanceName, value);
  }
});

// The exception meant for "the saved password, left as it is" was written as
// "a password is saved and the box is empty" — which is exactly what Remove
// password leaves — so removing it with remote access on skipped the warning.
test('removing the access password with remote access on still asks before opening up', async (t) => {
  const before = await api.get('/api/settings');
  t.after(() => api.put('/api/settings', before));
  await api.put('/api/settings', { ...before, general: { ...before.general, remoteAccess: true, accessPassword: 'hunter22' } });
  const { root } = await mountView(settingsView, { params: { tab: 'network' } }, t);
  const remove = await waitFor(() => [...root.querySelectorAll('button')].find((b) => b.textContent === 'Remove password'));
  remove.click();
  [...root.querySelectorAll('button')].find((b) => b.textContent.includes('Save changes')).click();
  const dialog = await waitFor(() => [...document.querySelectorAll('.modal h2')].find((el) => el.textContent === 'Open without a password?'));
  [...dialog.closest('.modal').querySelectorAll('.modal-foot button')].find((b) => b.textContent === 'Cancel').click();
  assert.equal((await api.get('/api/settings')).general.accessPassword, '********', 'nothing was saved');
});

// #122: Settings › Alerts lists the "also notify" channels, adds one through
// the channel editor (which offers notification types only and tests through
// /api/settings/test-channel), and saves it with the settings.
test('settings Alerts lists, adds, tests and saves alert channels', async (t) => {
  const { root } = await mountView(settingsView, { params: { tab: 'alerts' } }, t);
  const panel = root.querySelector('.settings-panel');
  const card = [...panel.querySelectorAll('.card')].find((c) => c.querySelector('h2')?.textContent === 'Also notify');
  assert.ok(card, 'an Also notify card');
  const names = () => [...card.querySelectorAll('.trigger-row .t-name')].map((el) => el.textContent);
  assert.equal(names().length, 2);
  assert.match(names()[0], /Family Telegram/);
  assert.match(card.textContent, /Outages only · groups: Servers · SMS → \+15550002222/);

  [...card.querySelectorAll('button')].find((b) => b.textContent.includes('Add channel')).click();
  const modal = await waitFor(() => document.querySelector('.modal'));
  const typeSel = [...modal.querySelectorAll('select')].find((s) => [...s.options].some((o) => o.value === 'pagerduty'));
  const types = [...typeSel.options].map((o) => o.value);
  assert.ok(!types.includes('script') && !types.includes('http') && !types.includes('git'), `only notification types (got ${types})`);
  assert.ok(['telegram', 'discord', 'gotify', 'matrix', 'twilio', 'pagerduty', 'opsgenie'].every((v) => types.includes(v)));
  typeSel.value = 'pagerduty';
  typeSel.dispatchEvent(new window.Event('change'));
  const set = (el, v) => { el.value = v; el.dispatchEvent(new window.Event('input')); };
  set([...modal.querySelectorAll('input')].find((el) => (el.placeholder || '').startsWith('Integration key')), 'R0UTING');
  set([...modal.querySelectorAll('input')].find((el) => (el.placeholder || '').startsWith('e.g. On-call')), 'Pager');

  [...modal.querySelectorAll('button')].find((b) => b.textContent.includes('Test this action')).click();
  await waitFor(() => modal.querySelector('.result-box.ok'));
  assert.match(modal.querySelector('.result-box').textContent, /pagerduty → 200 OK \(mock\)/);

  [...document.querySelectorAll('.modal button')].find((b) => b.textContent.trim() === 'Add channel').click();
  await waitFor(() => names().length === 3);
  assert.match(names()[2], /Pager/);

  // An earlier test's "Settings saved" toast may still be up; clear it so the
  // wait below is for this save.
  document.querySelectorAll('.toast').forEach((el) => el.remove());
  [...card.querySelectorAll('button')].find((b) => b.textContent.includes('Save changes')).click();
  await waitFor(() => [...document.querySelectorAll('.toast')].some((el) => /Settings saved/.test(el.textContent)));
  const saved = await api.get('/api/settings');
  const pager = saved.alerts.channels.find((c) => c.name === 'Pager');
  assert.ok(pager?.id, 'the saved channel has an ID');
  assert.equal(pager.action.type, 'pagerduty');
  assert.equal(pager.action.routingKey, '********', 'the key comes back masked');
});

// #99: Settings › Alerts has the per-node grouping window and the warning
// digest, and saves both with the settings.
test('settings Alerts edits the grouping window and the warning digest', async (t) => {
  const { root } = await mountView(settingsView, { params: { tab: 'alerts' } }, t);
  const panel = root.querySelector('.settings-panel');
  const byLabel = (re) => { const l = [...panel.querySelectorAll('label')].find((el) => re.test(el.textContent)); return l && document.getElementById(l.htmlFor); };
  const window_ = byLabel(/Group alerts for the same node/);
  const digest = byLabel(/Send warnings as a digest/);
  assert.ok(window_ && digest, 'both controls are labelled');
  assert.equal(window_.value, '30');
  assert.equal(window_.max, '300');
  assert.deepEqual([...digest.options].map((o) => o.value), ['off', 'hourly', 'daily']);
  window_.value = '60'; window_.dispatchEvent(new window.Event('input'));
  digest.value = 'daily'; digest.dispatchEvent(new window.Event('change'));
  document.querySelectorAll('.toast').forEach((el) => el.remove());
  [...panel.querySelectorAll('button')].find((b) => b.textContent.includes('Save changes')).click();
  await waitFor(() => [...document.querySelectorAll('.toast')].some((el) => /Settings saved/.test(el.textContent)));
  const saved = await api.get('/api/settings');
  assert.equal(saved.alerts.groupWindowSeconds, 60);
  assert.equal(saved.alerts.warningDigest, 'daily');
});

// #148: restoring a backup asks for its password, then shows what the backup
// holds and everything in it that matters for security, and restores only
// once that is confirmed. A backup that fails validation cannot be restored
// from the dialog at all.
test('settings Backups previews a restore before applying it', async (t) => {
  t.after(() => { document.querySelectorAll('.modal-backdrop').forEach((b) => b.remove()); });
  const { root } = await mountView(settingsView, { params: { tab: 'backups' } }, t);
  const restoreButton = () => waitFor(() => [...root.querySelectorAll('button')].find((b) => b.textContent.trim() === 'Restore'));
  const visibleModal = () => [...document.querySelectorAll('.modal')].at(-1);
  const before = (await api.get('/api/backups')).status.lastRestoreAt;

  (await restoreButton()).click();
  let modal = await waitFor(() => visibleModal());
  modal.querySelector('input[type="password"]').value = 'secret';
  [...modal.querySelectorAll('button')].find((b) => b.textContent === 'Continue').click();
  modal = await waitFor(() => [...document.querySelectorAll('.modal')].find((m) => /Check these before you restore/.test(m.textContent)));
  assert.match(modal.textContent, /This backup holds \d+ nodes/);
  assert.match(modal.textContent, /sam \(administrator\)/, 'the account not on this server, marked as an administrator');
  assert.match(modal.textContent, /left out/, 'without re-create ticked, missing accounts are left out');
  assert.match(modal.textContent, /Home Assistant \(read only\)/, 'the API key that would be added');
  assert.match(modal.textContent, /Runs a command on this computer: trigger Plex server › Restart Plex — script \(bash\)/);
  assert.match(modal.textContent, /LAN access is turned on/);
  [...modal.querySelectorAll('button')].find((b) => b.textContent === 'Cancel').click();
  await waitFor(() => !document.querySelector('.modal'));
  assert.equal((await api.get('/api/backups')).status.lastRestoreAt, before, 'cancelling the preview restores nothing');

  // A backup whose configuration does not validate lists its problems and
  // offers no way to go on.
  (await restoreButton()).click();
  modal = await waitFor(() => visibleModal());
  modal.querySelector('input[type="password"]').value = 'invalid';
  [...modal.querySelectorAll('button')].find((b) => b.textContent === 'Continue').click();
  modal = await waitFor(() => [...document.querySelectorAll('.modal')].find((m) => /cannot be restored/.test(m.textContent)));
  assert.match(modal.textContent, /custom interpreter/);
  assert.ok(![...modal.querySelectorAll('button')].some((b) => b.textContent === 'Restore'), 'no Restore button for an invalid backup');
  [...modal.querySelectorAll('button')].find((b) => b.textContent === 'Close').click();
  await waitFor(() => !document.querySelector('.modal'));

  // Confirmed, the restore goes ahead.
  (await restoreButton()).click();
  modal = await waitFor(() => visibleModal());
  modal.querySelector('input[type="password"]').value = 'secret';
  [...modal.querySelectorAll('button')].find((b) => b.textContent === 'Continue').click();
  modal = await waitFor(() => [...document.querySelectorAll('.modal')].find((m) => /Check these before you restore/.test(m.textContent)));
  [...modal.querySelectorAll('button')].find((b) => b.textContent === 'Restore').click();
  await waitFor(() => [...document.querySelectorAll('.toast')].some((el) => /Restored \d+ nodes/.test(el.textContent)));
  assert.notEqual((await api.get('/api/backups')).status.lastRestoreAt, before);
});

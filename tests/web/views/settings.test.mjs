// web/views/settings.js: an administrator sees every tab and lands on
// "General" by default, with the mock's instance name filled into the form.

import '../dom.mjs';
import '../../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as settingsView from '../../../web/views/settings.js';
import { mountView, waitFor } from '../view-harness.mjs';

test('settings view (admin) lists every tab and opens on General', async (t) => {
  const { root } = await mountView(settingsView, undefined, t);
  const tabLinks = [...root.querySelectorAll('.settings-nav a')];
  const tabLabels = tabLinks.map((a) => a.textContent);
  assert.deepEqual(tabLabels, [
    'General', 'Appearance', 'Indicators', 'Users & access', 'Network access', 'Alerts', 'Automation',
    'Hardware', 'AI & MCP', 'Retention', 'Maintenance', 'Backups', 'Database', 'Updates', 'Monitor health', 'About',
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

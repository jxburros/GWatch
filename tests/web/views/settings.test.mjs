// web/views/settings.js: an administrator sees every tab and lands on
// "General" by default, with the mock's instance name filled into the form.

import '../dom.mjs';
import '../../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as settingsView from '../../../web/views/settings.js';
import { mountView } from '../view-harness.mjs';

test('settings view (admin) lists every tab and opens on General', async (t) => {
  const { root } = await mountView(settingsView, undefined, t);
  const tabLinks = [...root.querySelectorAll('.settings-nav a')];
  const tabLabels = tabLinks.map((a) => a.textContent);
  assert.deepEqual(tabLabels, [
    'General', 'Appearance', 'Indicators', 'Users & access', 'Network access', 'Alerts', 'Automation',
    'Hardware', 'Retention', 'Maintenance', 'Backups', 'Updates', 'Monitor health', 'About',
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

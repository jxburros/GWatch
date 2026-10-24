// A machine is a node: adding one is part of Add node, and its agent — token,
// rotation, what it collects — is managed on the node itself. Under
// web/views/agent-card.js, agent-collect.js, machines.js (addAgentNode) and the
// template picker in nodes.js, against web/mock.js.

import '../dom.mjs';
import '../../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as nodeDetailView from '../../../web/views/node-detail.js';
import * as nodesView from '../../../web/views/nodes.js';
import { collectionPicker, describeSelection } from '../../../web/views/agent-collect.js';
import { addAgentNode, pairingWatch } from '../../../web/views/machines.js';
import { mountView, waitFor } from '../view-harness.mjs';

const button = (root, text) => [...root.querySelectorAll('button, a.btn')].find((b) => b.textContent.trim().includes(text));
const nasNode = () => window.__gwatchMock.nodes.find((n) => n.checks.some((c) => c.type === 'system' && c.config?.agentId === 1));

async function mountNas(t, opts) {
  const mounted = await mountView(nodeDetailView, { params: { id: String(nasNode().id) }, ...opts }, t);
  t.after(() => { document.querySelectorAll('.modal-backdrop').forEach((b) => b.remove()); });
  return mounted;
}

test('a machine\'s node carries its agent: identity, newer-release note and the agent mark', async (t) => {
  const { root } = await mountNas(t);
  const card = await waitFor(() => root.querySelector('.agent-card'));
  assert.match(card.textContent, /nas\.lan/);
  assert.match(card.textContent, /0\.5\.0/, 'the running version is shown');
  assert.match(card.textContent, /0\.6\.0 available/, 'a newer release is marked');
  const img = card.querySelector('img.agent-logo');
  assert.ok(img && img.getAttribute('alt') === '', 'the agent mark is decorative');
  assert.match(card.textContent, /Last asked for settings/);
});

test('the token is shown on request, so it can be compared with the one in agent.json', async (t) => {
  const { root } = await mountNas(t);
  const card = await waitFor(() => root.querySelector('.agent-card'));
  assert.doesNotMatch(card.textContent, /gwa_k3m9vq2xabcdef/, 'not shown until asked for');
  assert.match(card.querySelector('[data-token-prefix]').textContent, /^gwa_k3m9vq2x…$/);
  button(card, 'Show token').click();
  await waitFor(() => card.querySelector('.agent-token'));
  assert.equal(card.querySelector('.agent-token').textContent, 'gwa_k3m9vq2xabcdefghij234567abcdefghij234567');
  assert.match(card.textContent, /agent\.json/, 'it says where the other copy is');
});

test('a token GWatch holds only a digest of says how to get a readable one', async (t) => {
  const agent = window.__gwatchMock.agents?.[0];
  const before = agent ? { ...agent } : null;
  const { root } = await mountNas(t);
  const card = await waitFor(() => root.querySelector('.agent-card'));
  // The mock's agent is viewable; the wording for the other case is exercised
  // through the card directly.
  const { agentCard } = await import('../../../web/views/agent-card.js');
  const el = agentCard({ id: 9, name: 'old', prefix: 'gwa_abcd1234', enabled: true, tokenViewable: false, lastSeenAt: new Date().toISOString() }, {});
  assert.equal(button(el, 'Show token'), undefined, 'there is nothing to show');
  assert.match(el.textContent, /rotate it to get one/);
  assert.ok(card);
  if (before) assert.deepEqual(window.__gwatchMock.agents?.[0] ?? before, before);
});

test('rotating offers both ways, and the default one waits for the agent to pick it up', async (t) => {
  const { root } = await mountNas(t);
  const card = await waitFor(() => root.querySelector('.agent-card'));
  button(card, 'Rotate token').click();
  const modal = await waitFor(() => document.querySelector('.modal'));
  assert.match(modal.textContent, /Rotate \(recommended\)/);
  assert.match(modal.textContent, /Replace now \(if the token may have leaked\)/);
  assert.match(modal.textContent, /nothing is sent to the machine/i);
  button(modal, 'Rotate and let the agent pick it up').click();
  await waitFor(() => root.querySelector('[data-rotation="pending"]'), { timeout: 5000 });
  assert.match(root.querySelector('[data-rotation="pending"]').textContent, /picks it up the next time it asks/);

  // While waiting, both tokens can be compared.
  button(root.querySelector('.agent-card'), 'Show token').click();
  await waitFor(() => root.querySelectorAll('.agent-token').length === 2);
  assert.match(root.querySelector('.agent-card').textContent, /New token \(waiting to be picked up\)/);

  button(root.querySelector('.agent-card'), 'Cancel rotation').click();
  await waitFor(() => !root.querySelector('[data-rotation="pending"]'), { timeout: 5000 });
});

test('replacing now shows the new token to put on the machine by hand', async (t) => {
  const { root } = await mountNas(t);
  const card = await waitFor(() => root.querySelector('.agent-card'));
  button(card, 'Rotate token').click();
  const modal = await waitFor(() => document.querySelector('.modal'));
  button(modal, 'Replace now and show me the new token').click();
  const next = await waitFor(() => [...document.querySelectorAll('.modal')].find((m) => /Put the new token on the machine/.test(m.textContent)));
  assert.match(next.querySelector('.agent-token').textContent, /^gwa_rotated/);
  assert.match(next.textContent, /gwatch-agent config path/);
});

test('what an agent collects is chosen from what the machine says it has', async (t) => {
  const { root } = await mountNas(t);
  const card = await waitFor(() => root.querySelector('.agent-card'));
  const section = card.querySelector('[data-section="collect"]');
  assert.match(section.textContent, /eth0/);
  assert.match(section.textContent, /docker0/);
  assert.match(section.textContent, /hidden by default/, 'virtual interfaces are offered but not ticked');
  const boxes = (kind) => [...section.querySelectorAll(`input[id*="-${kind}-"][type="checkbox"]`)];
  const [eth0, docker0] = boxes('interfaces');
  assert.ok(eth0.checked && !docker0.checked, 'automatic ticks the sensible ones');

  // Narrow: only the tunnel-ish one, and only processor and network.
  section.querySelector('input[id$="-interfaces-only"]').click();
  docker0.checked = true;
  eth0.checked = false;
  for (const fam of ['memory', 'swap', 'load', 'disk', 'inodes', 'diskio', 'temp', 'psi', 'pool']) section.querySelector(`input[id$="-${fam}"]`).checked = false;
  button(section, 'Save what to collect').click();
  await waitFor(() => {
    const a = window.__gwatchMock.agents?.[0];
    return a && a.collect?.interfaces?.includes('docker0');
  }).catch(() => null);
  const saved = (await (await fetch('/api/agents')).json())[0].collect;
  assert.deepEqual(saved.metrics.sort(), ['cpu', 'net']);
  assert.ok(saved.interfaces.includes('docker0') && !saved.interfaces.includes('eth0'));
  assert.deepEqual(saved.disks, [], 'a kind left on automatic stays empty');
});

test('a viewer never sees an agent card or its credentials', async (t) => {
  const { root } = await mountNas(t, { isAdmin: false });
  await waitFor(() => root.querySelector('.check-card'));
  await new Promise((r) => setTimeout(r, 400));
  assert.equal(root.querySelector('.agent-card'), null);
});

test('revoking asks first and leaves the card saying so', async (t) => {
  const { root } = await mountNas(t);
  const card = await waitFor(() => root.querySelector('.agent-card'));
  button(card, 'Revoke token').click();
  const modal = await waitFor(() => [...document.querySelectorAll('.modal')].find((m) => /Revoke Living room NAS/.test(m.textContent)));
  button(modal, 'Revoke token').click();
  await waitFor(() => /has been revoked/.test(root.querySelector('.agent-card')?.textContent || ''), { timeout: 5000 });
  assert.ok(button(root.querySelector('.agent-card'), 'Delete agent'));
  // Put the mock back for the tests that follow.
  window.__gwatchMock.agents[0].revokedAt = null;
});

/* ---------- Add node ---------- */

test('Add node offers the agent beside the other things GWatch can watch', async (t) => {
  const { root, ctx } = await mountView(nodesView, undefined, t);
  t.after(() => { document.querySelectorAll('.modal-backdrop').forEach((b) => b.remove()); });
  await waitFor(() => root.querySelector('.node-list'));
  const labels = (ctx.setTitleCalls.at(-1).opts.actions || []).map((a) => a.textContent.trim());
  assert.ok(!labels.some((l) => /Pair a machine/.test(l)), `the old button is gone: ${labels}`);
  assert.ok(labels.includes('Add node'));

  const add = ctx.setTitleCalls.at(-1).opts.actions.find((a) => a.textContent.trim() === 'Add node');
  add.click();
  const picker = await waitFor(() => document.querySelector('.template-grid'));
  await waitFor(() => picker.querySelectorAll('.template-card').length > 3);
  const names = [...picker.querySelectorAll('.template-card b')].map((b) => b.textContent);
  assert.ok(names.includes('A machine with the GWatch agent'), names.join(' | '));
  assert.ok(names.indexOf('A machine with the GWatch agent') > names.findIndex((n) => /Website|Router|Ping/.test(n)), 'it follows the ordinary templates');
});

test('adding a machine takes a name and what to monitor, then shows the code', async (t) => {
  pairingWatch.intervalMs = 50;
  Object.assign(window.__gwatchMockPairing, { redeemMs: 200, reportMs: 200 });
  t.after(() => { document.querySelectorAll('.modal-backdrop').forEach((b) => b.remove()); });

  const started = addAgentNode();
  const name = await waitFor(() => document.querySelector('.modal input[placeholder^="e.g. Living room"]'));
  name.value = 'Garage Pi';
  const modal = document.querySelector('.modal');
  assert.match(modal.textContent, /What to monitor/);
  assert.match(modal.textContent, /Processor use/);
  assert.doesNotMatch(modal.textContent, /Network interfaces/, 'nothing has reported yet, so no interface list');
  // Untick swap and inodes.
  modal.querySelector('input[id$="-swap"]').checked = false;
  modal.querySelector('input[id$="-inodes"]').checked = false;
  [...modal.querySelectorAll('.modal-foot .btn')].find((b) => b.textContent.includes('Get a pairing code')).click();
  await started;

  const code = await waitFor(() => document.querySelector('.modal .mono'));
  assert.equal(code.textContent, 'KQWX-HMTZ');
  const command = document.querySelector('.agent-setup');
  assert.match(command.textContent, /gwatch-agent pair --server .* --code KQWX-HMTZ/);

  // The address can be changed to what the other machine can reach.
  const server = document.querySelector('.modal input#agent-server');
  server.value = 'http://192.168.1.2:7230';
  server.dispatchEvent(new window.Event('input', { bubbles: true }));
  assert.match(command.textContent, /--server http:\/\/192\.168\.1\.2:7230 --code KQWX-HMTZ/);

  // The choice rode with the code and reaches the agent when it arrives.
  const pairings = await (await fetch('/api/agents/pairings')).json();
  const mine = pairings.find((p) => p.name === 'Garage Pi');
  assert.deepEqual(mine.collect.metrics.sort(), ['cpu', 'disk', 'diskio', 'load', 'memory', 'net', 'pool', 'psi', 'temp']);
  const status = document.querySelector('.modal [role="status"]');
  await waitFor(() => /is paired and reporting/.test(status.textContent), { timeout: 5000 });
  const agents = await (await fetch('/api/agents')).json();
  const agent = agents.find((a) => a.name === 'Garage Pi');
  assert.deepEqual(agent.collect.metrics.sort(), ['cpu', 'disk', 'diskio', 'load', 'memory', 'net', 'pool', 'psi', 'temp']);
});

/* ---------- the picker on its own ---------- */

test('the collection picker writes nothing it was not asked to', () => {
  const picker = collectionPicker({ value: {}, inventory: { interfaces: [{ name: 'eth0', auto: true }, { name: 'docker0', auto: false }] } });
  assert.deepEqual(picker.value(), { metrics: [], interfaces: [], disks: [], mounts: [], services: [], processes: [] }, 'untouched means automatic everywhere, and watches nothing');
  assert.equal(picker.summary(), 'everything, with virtual interfaces left out');

  picker.el.querySelector('input[id$="-cpu"]').checked = false;
  assert.deepEqual(picker.value().metrics.length, 10);
  assert.match(picker.summary(), /10 of 11 kinds of reading/);
});

test('a name that was chosen but is not on the machine now is kept, not forgotten', () => {
  const picker = collectionPicker({ value: { mounts: ['/', '/mnt/usb'] }, inventory: { mounts: [{ name: '/', auto: true }, { name: '/data', auto: true }] } });
  assert.match(picker.el.textContent, /not seen now/);
  assert.deepEqual(picker.value().mounts.sort(), ['/', '/mnt/usb']);
  assert.match(describeSelection({ mounts: ['/'], metrics: ['cpu'] }), /1 of 11 kinds of reading, 1 filesystems/);
});

test('kinds only: no instance lists before the machine has reported', () => {
  const picker = collectionPicker({ instances: false });
  assert.doesNotMatch(picker.el.textContent, /Network interfaces|Filesystems/);
  assert.deepEqual(Object.keys(picker.value()), ['metrics']);
});

// An empty list is how the API spells "automatic", so unticking every kind
// of reading (or ticking nothing under "Only the ones I tick") saved as
// "collect everything" — the opposite of what was asked.
test('saving a collection with nothing ticked is refused rather than saved as everything', async (t) => {
  const { root } = await mountNas(t);
  const card = await waitFor(() => root.querySelector('.agent-card'));
  const section = card.querySelector('[data-section="collect"]');
  const before = JSON.stringify((await (await fetch('/api/agents')).json())[0].collect);
  for (const box of section.querySelectorAll('.collect-group:first-of-type input[type="checkbox"]')) box.checked = false;
  document.querySelectorAll('.toast').forEach((el) => el.remove());
  button(section, 'Save what to collect').click();
  await waitFor(() => [...document.querySelectorAll('.toast-error')].find((el) => /at least one kind of reading/i.test(el.textContent)));
  assert.equal(JSON.stringify((await (await fetch('/api/agents')).json())[0].collect), before, 'nothing was saved');

  const picker = collectionPicker({ value: {}, inventory: { interfaces: [{ name: 'eth0', auto: true }] } });
  assert.equal(picker.problem(), '');
  picker.el.querySelector('input[id$="-interfaces-only"]').click();
  picker.el.querySelector('input[id*="-interfaces-"][type="checkbox"]').checked = false;
  assert.match(picker.problem(), /network interfaces/i);
});

// #132: a Linux machine's sensors, pressure figures and ZFS pool are shown
// with its other readings, and the pool is one filesystem, not one per dataset.
test('a Linux machine shows its temperatures, pressure and ZFS pool', async (t) => {
  const { root } = await mountNas(t);
  await waitFor(() => /cpu_thermal\/temp1/.test(root.textContent), { timeout: 5000 });
  assert.match(root.textContent, /Pressure stall/);
  assert.match(root.textContent, /ZFS pools/);
  assert.match(root.textContent, /ONLINE/);
  assert.match(root.textContent, /zfs · 4 datasets/);
});

/* ---------- services and programs (#62) ---------- */

// A machine's watched services and programs are shown with its readings, each
// with its state, and the hardware check judges each one.
test('a machine shows the services and programs it watches', async (t) => {
  const { root } = await mountNas(t);
  await waitFor(() => /Watched services/.test(root.textContent), { timeout: 5000 });
  assert.match(root.textContent, /smbd/);
  assert.match(root.textContent, /Samba SMB Daemon/);
  assert.match(root.textContent, /active \(running\)/);
  assert.match(root.textContent, /Watched programs/);
  assert.match(root.textContent, /Plex Media Server/);
});

// They are chosen on the agent card with the rest of what it collects,
// offered from the list the machine sent at its check-in.
test('an agent is told which services and programs to watch', async (t) => {
  const { root } = await mountNas(t);
  const card = await waitFor(() => root.querySelector('.agent-card'));
  const section = card.querySelector('[data-section="collect"]');
  const svc = section.querySelector('[data-watch="services"]');
  assert.ok(svc, 'the services box is there');
  assert.match(svc.textContent, /docker/, 'what is watched now is shown');
  assert.match(svc.textContent, /5 on this machine, 4 running/, 'the machine\'s own list is offered');
  assert.ok([...svc.querySelectorAll('datalist option')].some((o) => o.value === 'nginx'));
  const input = svc.querySelector('input');
  input.value = 'nginx';
  input.dispatchEvent(new window.Event('change'));
  button(section, 'Save what to collect').click();
  await waitFor(async () => (await (await fetch('/api/agents')).json())[0].collect.services.includes('nginx'), { timeout: 5000 });
  const saved = (await (await fetch('/api/agents')).json())[0].collect;
  assert.deepEqual(saved.services.sort(), ['docker', 'nginx', 'smbd']);
  assert.deepEqual(saved.processes, ['Plex Media Server']);
  assert.match(describeSelection(saved), /watching 3 services and 1 program/);
});

// The computer GWatch runs on has no agent: its list is set on its own
// hardware panel, and what it has is listed only when asked.
test('this computer\'s services are chosen on its hardware panel', async (t) => {
  const { hardwarePanel } = await import('../../../web/views/machines.js');
  const panel = hardwarePanel('local', { isAdmin: true });
  document.body.append(panel.el);
  t.after(() => { panel.destroy(); panel.el.remove(); });
  const section = await waitFor(() => panel.el.querySelector('[data-section="watch"]'), { timeout: 5000 });
  assert.equal(section.querySelectorAll('datalist option').length, 0, 'nothing is listed until asked');
  button(section, 'List what this computer has').click();
  await waitFor(() => section.querySelectorAll('datalist option').length > 0, { timeout: 5000 });
  assert.match(section.textContent, /4 on this machine, 3 running/);
  const input = section.querySelector('[data-watch="services"] input');
  input.value = 'Spooler';
  input.dispatchEvent(new window.Event('change'));
  button(section, 'Save what to watch').click();
  await waitFor(async () => (await (await fetch('/api/hosts/local')).json()).watch?.services?.includes('Spooler'), { timeout: 5000 });
  const host = await (await fetch('/api/hosts/local')).json();
  assert.equal(host.metrics.services[0].name, 'Spooler');

  // A viewer gets the readings but not the editor.
  const viewer = hardwarePanel('local', { isAdmin: false });
  document.body.append(viewer.el);
  t.after(() => { viewer.destroy(); viewer.el.remove(); });
  await viewer.ready;
  assert.equal(viewer.el.querySelector('[data-section="watch"]'), null);
  // Leave the mock as it was found.
  await fetch('/api/hosts/local/watch', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: '{}' });
});

// A live update and a refresh (or two updates) each load the machine; the
// older answer arriving last drew the older reading over the newer one.
test('a hardware panel draws only its newest reading', async (t) => {
  const { hardwarePanel } = await import('../../../web/views/machines.js');
  const waiting = { host: [] };
  const realFetch = window.fetch;
  window.fetch = (url) => {
    const u = String(url);
    if (u.startsWith('/api/hosts/race/history')) return new Promise(() => {});
    const kind = u.startsWith('/api/hosts/race') ? 'host' : null;
    if (!kind) return realFetch(url);
    return new Promise((resolve) => waiting[kind].push((body) => resolve(new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } }))));
  };
  t.after(() => { window.fetch = realFetch; });
  const panel = hardwarePanel('race');
  document.body.append(panel.el);
  t.after(() => { panel.destroy(); panel.el.remove(); });
  const tick = () => new Promise((r) => setTimeout(r, 0));
  await tick();
  panel.refresh();
  await tick();
  assert.equal(waiting.host.length, 2, 'two readings are out at once');
  const host = (hostname) => ({ key: 'race', source: 'agent', status: 'up', metrics: { ts: new Date().toISOString(), hostname, platform: 'linux' } });
  waiting.host[1](host('newer-box'));
  await tick();
  waiting.host[0](host('older-box'));
  await tick(); await tick();
  assert.match(panel.el.textContent, /newer-box/);
  assert.doesNotMatch(panel.el.textContent, /older-box/);

});

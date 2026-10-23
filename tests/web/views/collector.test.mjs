// Remote collectors (#123) in the interface: the agent card's switch and
// status, and the node editor's "Check from". Under web/views/agent-card.js and
// web/views/node-editor.js, against web/mock.js.

import '../dom.mjs';
import '../../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as nodeDetailView from '../../../web/views/node-detail.js';
import * as nodeEditorView from '../../../web/views/node-editor.js';
import { agentCard, collectorWords } from '../../../web/views/agent-card.js';
import { mountView, waitFor } from '../view-harness.mjs';

const mock = () => window.__gwatchMock;
const nasNode = () => mock().nodes.find((n) => n.checks.some((c) => c.type === 'system' && c.config?.agentId === 1));

test('the agent card has a collector switch that turns collecting on in GWatch', async (t) => {
  const agent = mock().agents[0];
  t.after(() => { agent.collector = false; });
  const { root } = await mountView(nodeDetailView, { params: { id: String(nasNode().id) } }, t);
  const card = await waitFor(() => root.querySelector('.agent-card'));
  const section = card.querySelector('[data-section="collector"]');
  assert.ok(section, 'the card has a remote collector section');
  assert.match(section.textContent, /"collector": true/, 'it names the machine\'s own half of the agreement');
  assert.match(section.textContent, /SNMP communities and HTTP headers/, 'it says what is sent to the machine');
  const sw = section.querySelector('input[role="switch"]');
  assert.equal(sw.checked, false);
  sw.click();
  await waitFor(() => agent.collector === true);
  const again = await waitFor(() => root.querySelector('.agent-card [data-collector-status]'));
  assert.equal(again.getAttribute('data-collector-status'), 'idle', 'allowed on the machine, and no node checks from it yet');
});

test('the collector status is put in words for each state', () => {
  assert.match(collectorWords({ collectorStatus: 'waiting' }).text, /"collector": true/);
  assert.equal(collectorWords({ collectorStatus: 'running', collectorChecks: 3 }).label, 'running 3 checks');
  assert.match(collectorWords({ collectorStatus: 'unreachable', collectorChecks: 2 }).text, /one alert was raised for the collector/);
  const el = agentCard({ id: 5, name: 'branch', prefix: 'gwa_x', enabled: true, lastSeenAt: new Date().toISOString(), collector: true, collectorAllowed: true, collectorStatus: 'unreachable', collectorChecks: 4 }, {});
  assert.equal(el.querySelector('[data-collector-status]').getAttribute('data-collector-status'), 'unreachable');
  assert.match(el.textContent, /unreachable/);
});

test('the node editor chooses where a node is checked from, and saves it', async (t) => {
  const gateway = mock().nodes.find((n) => n.name === 'Gateway');
  const agent = mock().agents[0];
  agent.collector = true;
  t.after(() => { agent.collector = false; gateway.collectorId = null; });
  const { root, ctx } = await mountView(nodeEditorView, { params: { id: String(gateway.id) } }, t);
  const fieldEl = [...root.querySelectorAll('.field')].find((f) => f.textContent.startsWith('Check from'));
  assert.ok(fieldEl, 'expected a Check from field');
  const select = fieldEl.querySelector('select');
  assert.deepEqual([...select.options].map((o) => o.value), ['', String(agent.id)]);
  assert.equal(select.value, '', 'a node checks from this server unless told otherwise');
  select.value = String(agent.id);
  select.dispatchEvent(new window.Event('change'));
  assert.match(fieldEl.querySelector('[data-collector-note]').textContent, /run from Living room NAS/);
  const save = ctx.setTitleCalls[0].opts.actions.find((b) => b.textContent.includes('Save'));
  save.click();
  await waitFor(() => gateway.collectorId === agent.id);
});

test('an agent with collecting off is offered, with a note that says so', async (t) => {
  const gateway = mock().nodes.find((n) => n.name === 'Gateway');
  const { root } = await mountView(nodeEditorView, { params: { id: String(gateway.id) } }, t);
  const fieldEl = [...root.querySelectorAll('.field')].find((f) => f.textContent.startsWith('Check from'));
  const select = fieldEl.querySelector('select');
  assert.match(select.options[1].textContent, /collecting is off/);
  select.value = select.options[1].value;
  select.dispatchEvent(new window.Event('change'));
  assert.match(fieldEl.querySelector('[data-collector-note]').textContent, /Collecting is off/);
});

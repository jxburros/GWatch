// The style of the line between a node and the node it depends on, from the
// two places a person sets it: beside "Depends on" in the Network map's side
// panel, and in the node editor. web/mock.js stands in for the service, and
// like it keeps a link's style on the node at its lower end.

import '../dom.mjs';
import '../../../web/mock.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as networkMapView from '../../../web/views/network-map.js';
import * as nodeEditorView from '../../../web/views/node-editor.js';
import * as dashboardView from '../../../web/views/dashboard.js';
import { mountView, settle, waitFor } from '../view-harness.mjs';

const mock = () => window.__gwatchMock;
const nodeNamed = (name) => mock().nodes.find((n) => n.name === name);
const change = (el) => el.dispatchEvent(new window.Event('change', { bubbles: true }));
const fieldset = (root) => root.querySelector('fieldset.netmap-linegroup');
// The selected node's card is the side panel's first; the map's own options come after it.
const selectedCard = (root) => root.querySelector('.netmap-side > section:first-child');
const widthOf = (el) => el.querySelector('input[type="number"]');
const edge = (root, from, to) => root.querySelector(`.netmap-edge[data-from="${from}"][data-to="${to}"]`);
const styleOf = (root, from, to) => edge(root, from, to).getAttribute('style');
const button = (root, text) => [...root.querySelectorAll('button')].find((b) => b.textContent.trim() === text);
const saveButton = (ctx) => ctx.setTitleCalls[0].opts.actions.find((a) => a.textContent.includes('Save changes'));

// The demo data styles the NAS's link to the Office switch; every test starts from that.
const STYLED = { color: '#3b82f6', width: 3 };
function resetLinkStyles() {
  for (const n of mock().nodes) n.linkStyle = null;
  nodeNamed('NAS').linkStyle = { ...STYLED };
}

test('the network map side panel offers a link style for the node\'s line to its parent', async (t) => {
  resetLinkStyles();
  const nas = nodeNamed('NAS'), swtch = nodeNamed('Office switch'), gateway = nodeNamed('Gateway');
  const { root } = await mountView(networkMapView, { query: new URLSearchParams({ node: String(nas.id) }) }, t);

  const group = fieldset(selectedCard(root));
  assert.ok(group, 'a selected node with a parent has a link group');
  assert.equal(group.querySelector('legend').textContent, 'Link to Office switch');
  assert.equal(group.querySelector('input[type="color"]').value, '#3b82f6');
  assert.equal(widthOf(group).value, '3');
  assert.equal(group.querySelector('select').value, '', 'a style it does not set follows the map');
  assert.ok(button(group, 'Reset'), 'with a Reset');
  // ... and the map draws that line in it.
  assert.match(styleOf(root, swtch.id, nas.id), /stroke:#3b82f6/);
  assert.match(styleOf(root, swtch.id, nas.id), /stroke-width:3/);
  assert.equal(styleOf(root, swtch.id, nodeNamed('Printer').id), 'stroke-width:1.5', 'a link with no style of its own is as it was');

  // A node with nothing it depends on has no link to style.
  const { root: rootNode } = await mountView(networkMapView, { query: new URLSearchParams({ node: String(gateway.id) }) }, t);
  assert.equal(fieldset(selectedCard(rootNode)), null);
});

test('changing a link\'s style in the side panel is saved on the node and redrawn on the map', async (t) => {
  resetLinkStyles();
  const printer = nodeNamed('Printer'), swtch = nodeNamed('Office switch'), nas = nodeNamed('NAS');
  const { root } = await mountView(networkMapView, { query: new URLSearchParams({ node: String(printer.id) }) }, t);
  const group = fieldset(selectedCard(root));
  assert.equal(group.querySelector('legend').textContent, 'Link to Office switch');
  assert.equal(widthOf(group).value, '');

  widthOf(group).value = '5'; change(widthOf(group));
  const dash = group.querySelector('select'); dash.value = 'dotted'; change(dash);
  await waitFor(() => printer.linkStyle?.dash === 'dotted');
  assert.deepEqual(printer.linkStyle, { width: 5, dash: 'dotted' });
  assert.deepEqual(nas.linkStyle, STYLED, 'the other links are untouched');
  await waitFor(() => /stroke-dasharray:0\.01 /.test(styleOf(root, swtch.id, printer.id)));
  assert.match(styleOf(root, swtch.id, printer.id), /stroke-width:5/);

  // Reset gives it back to the map.
  button(group, 'Reset').click();
  await waitFor(() => printer.linkStyle === null);
  await waitFor(() => styleOf(root, swtch.id, printer.id) === 'stroke-width:1.5');
  assert.equal(widthOf(group).value, '');
  assert.equal(group.querySelector('select').value, '');
});

test('a viewer sees the link style but cannot change it', async (t) => {
  resetLinkStyles();
  const { root } = await mountView(networkMapView, { query: new URLSearchParams({ node: String(nodeNamed('NAS').id) }), isAdmin: false }, t);
  const group = fieldset(selectedCard(root));
  assert.equal(widthOf(group).value, '3');
  for (const el of group.querySelectorAll('input, select')) assert.ok(el.disabled, `${el.outerHTML.slice(0, 50)} is disabled`);
});

test('the map options on the page hold the line colour, thickness and style', async (t) => {
  resetLinkStyles();
  localStorage.removeItem('gw.netmap');
  const { root } = await mountView(networkMapView, {}, t);
  const options = root.querySelector('.netmap-options');
  assert.ok(options.querySelector('fieldset legend').textContent.includes('Line style'));
  const width = options.querySelector('input[type="number"]');
  width.value = '4'; change(width);
  await settle();
  const nas = nodeNamed('NAS'), swtch = nodeNamed('Office switch'), printer = nodeNamed('Printer');
  assert.match(styleOf(root, swtch.id, printer.id), /stroke-width:4/, 'every line takes the map\'s thickness');
  assert.match(styleOf(root, swtch.id, nas.id), /stroke-width:3/, 'a link\'s own over it');
  assert.equal(JSON.parse(localStorage.getItem('gw.netmap')).lineWidth, 4, 'and it is remembered');
  localStorage.removeItem('gw.netmap');
});

test('the node editor has a Link style group beside Depends on, only while there is a parent', async (t) => {
  resetLinkStyles();
  const nas = nodeNamed('NAS');
  const { root } = await mountView(nodeEditorView, { params: { id: String(nas.id) } }, t);
  const node = root.querySelector('.card[aria-label="Node"]');
  const group = fieldset(node);
  assert.ok(group, 'the group is on the node card');
  assert.equal(group.querySelector('legend').textContent, 'Link to Office switch');
  assert.equal(group.closest('.span-2').hidden, false);
  assert.equal(group.querySelector('input[type="color"]').value, '#3b82f6');
  assert.equal(widthOf(group).value, '3');
  // It sits straight after the Depends on field.
  const depends = [...node.querySelectorAll('.field')].find((f) => f.querySelector('label')?.textContent === 'Depends on');
  assert.equal(depends.nextElementSibling, group.closest('.span-2'));

  // No parent, no link to style.
  const select = depends.querySelector('select');
  select.value = ''; change(select);
  assert.equal(group.closest('.span-2').hidden, true);
  select.value = String(nodeNamed('Gateway').id); change(select);
  assert.equal(group.closest('.span-2').hidden, false);
  assert.equal(group.querySelector('legend').textContent, 'Link to Gateway');

  // A new node has none until it is given a parent.
  const { root: fresh } = await mountView(nodeEditorView, { params: {}, query: new URLSearchParams() }, t);
  assert.equal(fieldset(fresh.querySelector('.card[aria-label="Node"]')).closest('.span-2').hidden, true);
});

test('saving the node editor keeps the link style, and drops it with the dependency', async (t) => {
  resetLinkStyles();
  const plex = nodeNamed('Plex'), gateway = nodeNamed('Gateway');
  const { root, ctx } = await mountView(nodeEditorView, { params: { id: String(plex.id) } }, t);
  const group = fieldset(root.querySelector('.card[aria-label="Node"]'));
  widthOf(group).value = '6'; change(widthOf(group));
  group.querySelector('input[type="color"]').value = '#ff8800'; change(group.querySelector('input[type="color"]'));
  const dash = group.querySelector('select'); dash.value = 'dash-dot'; change(dash);
  saveButton(ctx).click();
  await waitFor(() => ctx.navigated.length);
  assert.deepEqual(plex.linkStyle, { color: '#ff8800', width: 6, dash: 'dash-dot' });
  assert.equal(plex.dependsOnNodeId, gateway.id);

  // Take the dependency away and save again: the style goes with the link.
  const again = await mountView(nodeEditorView, { params: { id: String(plex.id) } }, t);
  const select = [...again.root.querySelectorAll('.field')].find((f) => f.querySelector('label')?.textContent === 'Depends on').querySelector('select');
  select.value = ''; change(select);
  saveButton(again.ctx).click();
  await waitFor(() => again.ctx.navigated.length);
  assert.equal(plex.dependsOnNodeId, null);
  assert.equal(plex.linkStyle, null);
  plex.dependsOnNodeId = gateway.id;
});

test('the mock refuses a link colour that is not a hex colour, as the service does', async () => {
  resetLinkStyles();
  const nas = nodeNamed('NAS');
  const patch = (linkStyle) => fetch('/api/nodes/bulk', { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ nodeIds: [nas.id], node: { linkStyle } }) });
  assert.equal((await patch({ color: 'red' })).status, 400);
  assert.equal((await patch({ dash: 'wavy' })).status, 400);
  assert.deepEqual(nas.linkStyle, STYLED, 'and nothing changed');
  assert.equal((await patch({ width: 99 })).status, 200);
  assert.deepEqual(nas.linkStyle, { width: 8 });
  assert.equal((await patch(null)).status, 200);
  assert.equal(nas.linkStyle, null);
  resetLinkStyles();
});

test('a network map widget draws with the line style of its configuration, and the links\' own over it', async (t) => {
  resetLinkStyles();
  mock().dashboards.push({ id: 77, name: 'Map board', sortOrder: 9, widgets: [
    { id: 'wm', type: 'network_map', title: 'The map', x: 0, y: 0, width: 4, height: 3, config: { orientation: 'radial', lineColor: '#ff0000', lineWidth: 4, lineStyle: 'dashed' } },
  ] });
  try {
    const { root } = await mountView(dashboardView, { params: { id: '77' } }, t);
    await settle();
    const widget = root.querySelector('.widget-map');
    assert.ok(widget, 'the map widget is on the dashboard');
    const swtch = nodeNamed('Office switch');
    assert.match(styleOf(widget, swtch.id, nodeNamed('Printer').id), /^stroke-width:4;stroke:#ff0000;stroke-dasharray:\d+(\.\d)? \d+(\.\d)?;stroke-linecap:butt$/);
    const own = styleOf(widget, swtch.id, nodeNamed('NAS').id);
    assert.match(own, /stroke:#3b82f6/, 'the NAS link\'s own colour');
    assert.match(own, /stroke-width:3(;|$)/, 'and thickness');
    assert.match(own, /stroke-dasharray:\d/, 'and the map\'s dashes, which it did not choose for itself');
  } finally {
    mock().dashboards.pop();
  }
});

test('the widget editor offers the same line controls as the page', async (t) => {
  resetLinkStyles();
  mock().dashboards.push({ id: 78, name: 'Map board 2', sortOrder: 9, widgets: [
    { id: 'wm2', type: 'network_map', title: 'The map', x: 0, y: 0, width: 4, height: 3, config: { lineColor: '#00ff00', lineWidth: 2, lineStyle: 'dotted' } },
  ] });
  try {
    const { root } = await mountView(dashboardView, { params: { id: '78' } }, t);
    await settle();
    root.querySelector('.widget button[aria-label="Widget options"]').click();
    [...document.querySelectorAll('.menu-item')].find((b) => b.textContent.trim() === 'Edit widget').click();
    const modal = await waitFor(() => document.querySelector('.modal'));
    assert.equal(modal.querySelector('fieldset legend').textContent, 'Line style');
    assert.equal(modal.querySelector('input[type="color"]').value, '#00ff00');
    assert.equal(modal.querySelector('input[type="number"]').value, '2');
    assert.equal([...modal.querySelectorAll('select')].find((x) => [...x.options].some((o) => o.value === 'dash-dot')).value, 'dotted');
    [...modal.querySelectorAll('button')].find((b) => b.textContent.trim() === 'Cancel').click();
  } finally {
    mock().dashboards.pop();
  }
});

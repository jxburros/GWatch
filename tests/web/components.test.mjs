// DOM-level behaviour of web/components.js: the element builder, icons,
// status helpers, theme persistence and the toast/modal/confirm surfaces.

import './dom.mjs';
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  h, icon, clear, replace, toggle, statusWord, tagList, applyTheme, currentTheme,
  toast, openModal, confirmDialog, eventRow,
} from '../../web/components.js';

// #60: an event about one of a check's metrics names it, so a disk filling
// and the memory on the same check read as two incidents.
test('eventRow names the metric a metric-scoped event is about', () => {
  const row = eventRow({ id: 1, ts: new Date().toISOString(), type: 'warning', nodeId: 3, nodeName: 'NAS', checkName: 'Hardware', title: 'Disk /srv warning', detail: 'Disk /srv is 88%', metric: 'disk:/srv' });
  const tag = row.querySelector('.ev-metric');
  assert.ok(tag, 'the metric is shown');
  assert.equal(tag.textContent, 'Disk /srv');
  assert.equal(tag.getAttribute('title'), 'disk:/srv');
  const plain = eventRow({ id: 2, ts: new Date().toISOString(), type: 'down', nodeId: 3, nodeName: 'NAS', title: 'Down' });
  assert.equal(plain.querySelector('.ev-metric'), null, 'a check-level event has none');
});

test('h() sets attributes, class, style objects and dataset', () => {
  const el = h('div', { class: 'a b', id: 'x', style: { color: 'red', fontSize: '12px' }, dataset: { foo: 'bar' }, title: 'hi' });
  assert.equal(el.tagName, 'DIV');
  assert.equal(el.className, 'a b');
  assert.equal(el.id, 'x');
  assert.equal(el.style.color, 'red');
  assert.equal(el.style.fontSize, '12px');
  assert.equal(el.dataset.foo, 'bar');
  assert.equal(el.getAttribute('title'), 'hi');
});

test('h() wires up "on*" handlers as real event listeners', () => {
  let clicks = 0;
  const el = h('button', { onclick: () => { clicks++; } });
  el.dispatchEvent(new window.Event('click'));
  assert.equal(clicks, 1);
});

test('h() supports the text/html shortcuts and skips null/false/true children', () => {
  const withText = h('span', { text: 'hello <b>' });
  assert.equal(withText.textContent, 'hello <b>');
  assert.equal(withText.children.length, 0); // textContent, not parsed markup

  const withHtml = h('span', { html: '<b>bold</b>' });
  assert.equal(withHtml.querySelector('b').textContent, 'bold');

  const el = h('div', null, 'a', null, false, true, 0, 'b', h('i'));
  // null/false/true children are skipped; 0 is kept (it is valid content)
  assert.equal(el.textContent, 'a0b');
  assert.equal(el.children.length, 1);
});

test('h() treats boolean form properties (checked, disabled…) as DOM properties, not attributes', () => {
  const input = h('input', { type: 'checkbox', checked: true, disabled: false });
  assert.equal(input.checked, true);
  assert.equal(input.disabled, false);
  assert.equal(input.hasAttribute('disabled'), false);
});

test('icon() returns an inline SVG for a known name and falls back for an unknown one', () => {
  const known = icon('check');
  assert.equal(known.tagName, 'svg');
  assert.ok(known.innerHTML.includes('<path'));
  const unknown = icon('this-icon-does-not-exist');
  assert.equal(unknown.innerHTML, icon('dot').innerHTML); // falls back to the same glyph as "dot"
});

test('clear() empties a node and replace() clears then appends', () => {
  const el = h('div', null, h('span'), h('span'));
  assert.equal(el.children.length, 2);
  clear(el);
  assert.equal(el.children.length, 0);
  replace(el, h('i'), h('b'));
  assert.equal(el.children.length, 2);
  assert.equal(el.firstChild.tagName, 'I');
});

test('toggle() builds an accessible switch whose checked state drives onChange', () => {
  let last = null;
  const sw = toggle({ label: 'Enabled', checked: true, onChange: (v) => { last = v; } });
  assert.equal(sw.input.type, 'checkbox');
  assert.equal(sw.input.getAttribute('role'), 'switch');
  assert.equal(sw.input.checked, true);
  sw.input.checked = false;
  sw.input.dispatchEvent(new window.Event('change'));
  assert.equal(last, false);
});

test('statusWord() labels a known status and falls back to "unknown" styling for anything else', () => {
  const up = statusWord('up');
  assert.equal(up.textContent, 'Up');
  assert.ok(up.className.includes('text-up'));
  const bogus = statusWord('not-a-real-status');
  assert.equal(bogus.textContent, 'Unknown');
  assert.ok(bogus.className.includes('text-unknown'));
  const labelled = statusWord('down', { label: 'Offline' });
  assert.equal(labelled.textContent, 'Offline');
});

test('tagList() renders an optional group chip followed by one chip per tag', () => {
  const withGroup = tagList(['a', 'b'], { group: 'Servers' });
  const chips = [...withGroup.children].map((c) => c.textContent);
  assert.deepEqual(chips, ['Servers', 'a', 'b']);
  const empty = tagList([]);
  assert.equal(empty.children.length, 0);
});

test('applyTheme resolves "system" against matchMedia, persists to localStorage and currentTheme reads it back', () => {
  window.localStorage.removeItem('gw.theme');
  applyTheme('light');
  assert.equal(currentTheme(), 'light');
  assert.equal(window.localStorage.getItem('gw.theme'), 'light');
  applyTheme('bogus'); // an invalid value falls back to "dark"
  assert.equal(currentTheme(), 'dark');
  assert.equal(window.localStorage.getItem('gw.theme'), 'dark');
});

test('toast() appends a dismissible toast to #toasts and removes itself on click', () => {
  const root = document.getElementById('toasts');
  clear(root);
  const el = toast('Saved', { kind: 'success', timeout: 0 });
  assert.equal(root.children.length, 1);
  assert.ok(el.className.includes('toast-success'));
  assert.equal(el.textContent.includes('Saved'), true);
  el.querySelector('button').click();
  assert.equal(root.children.length, 0);
});

test('openModal renders into #modals, focus-traps and closes on Escape', () => {
  const before = document.getElementById('modals').children.length;
  const m = openModal({ title: 'Confirm', body: h('p', null, 'Are you sure?') });
  assert.equal(document.getElementById('modals').children.length, before + 1);
  assert.equal(m.el.querySelector('h2').textContent, 'Confirm');
  m.close();
  assert.equal(document.getElementById('modals').children.length, before);
});

test('confirmDialog resolves true/false depending on which button is pressed', async () => {
  const okPromise = confirmDialog({ title: 'Delete?', confirmLabel: 'Delete', danger: true });
  const dialog = document.getElementById('modals').lastElementChild;
  dialog.querySelector('.btn-danger').click();
  assert.equal(await okPromise, true);

  const cancelPromise = confirmDialog({ title: 'Delete?' });
  const dialog2 = document.getElementById('modals').lastElementChild;
  dialog2.querySelector('.modal-head button').click(); // the header's own close (×) button
  assert.equal(await cancelPromise, false);
});

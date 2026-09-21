// DOM-level behaviour of web/components.js: the element builder, icons,
// status helpers, theme persistence and the toast/modal/confirm surfaces.

import './dom.mjs';
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  h, icon, clear, replace, toggle, statusWord, statusPill, tagList, applyTheme, currentTheme,
  toast, openModal, confirmDialog, showMenu, menuButton, closeMenus, field, textInput, chipInput, checkChip,
} from '../../web/components.js';

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

/* ---------- Accessibility contract (#38) ---------- */

const key = (el, k, init = {}) => el.dispatchEvent(new window.KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true, ...init }));

test('openModal is named by its heading, makes the page inert and hands focus back on close', () => {
  const opener = h('button', null, 'Open');
  document.body.append(opener);
  opener.focus();
  const m = openModal({ title: 'Rename', body: h('input', { id: 'rn' }) });
  assert.equal(m.el.getAttribute('aria-labelledby'), m.el.querySelector('h2').id);
  assert.equal(m.el.hasAttribute('aria-label'), false);
  const app = document.getElementById('app');
  assert.equal(app.hasAttribute('inert'), true, 'the page behind a modal is inert');
  assert.equal(app.getAttribute('aria-hidden'), 'true');
  m.close();
  assert.equal(app.hasAttribute('inert'), false, 'closing the last modal releases the page');
  assert.equal(app.hasAttribute('aria-hidden'), false);
  assert.equal(document.activeElement, opener, 'focus returns to the element that opened the modal');
  opener.remove();
});

test('a modal opened from a modal keeps the page inert until both are closed', () => {
  const app = document.getElementById('app');
  const a = openModal({ title: 'First' });
  const b = openModal({ title: 'Second' });
  b.close();
  assert.equal(app.hasAttribute('inert'), true);
  a.close();
  assert.equal(app.hasAttribute('inert'), false);
});

test('Tab inside a modal cycles between its first and last focusable elements without needing layout', () => {
  const first = h('input', { id: 'first' });
  const last = h('button', { type: 'button' }, 'OK');
  const m = openModal({ title: 'Trap', body: [first, h('button', { type: 'button', hidden: true }, 'hidden')], footer: last });
  // jsdom has no layout, so offsetParent is always null there: the trap must
  // judge focusability from markup, which is what makes this testable.
  const closeBtn = m.el.querySelector('.modal-head button');
  last.focus();
  key(document, 'Tab');
  assert.equal(document.activeElement, closeBtn, 'Tab from the last element wraps to the first (the header\'s close button)');
  key(document, 'Tab', { shiftKey: true });
  assert.equal(document.activeElement, last, 'Shift+Tab from the first wraps to the last');
  first.focus();
  key(document, 'Tab');
  assert.equal(document.activeElement, first, 'a Tab in the middle is left to the browser');
  m.close();
});

test('confirmDialog describes the dialog by its message', async () => {
  const p = confirmDialog({ title: 'Delete?', message: 'This cannot be undone.' });
  const dialog = document.getElementById('modals').lastElementChild.querySelector('[role="dialog"]');
  const desc = dialog.getAttribute('aria-describedby');
  assert.ok(desc);
  assert.equal(document.getElementById(desc).textContent, 'This cannot be undone.');
  dialog.querySelector('.modal-foot .btn').click();
  assert.equal(await p, false);
});

test('showMenu: arrow keys rove between items, Escape closes and returns focus to the trigger', () => {
  const calls = [];
  const btn = menuButton([
    { label: 'One', onClick: () => calls.push('one') },
    { label: 'Two', onClick: () => calls.push('two'), disabled: true },
    { sep: true },
    { label: 'Three', onClick: () => calls.push('three') },
  ], { label: 'Options' });
  document.body.append(btn);
  assert.equal(btn.getAttribute('aria-expanded'), 'false');
  btn.click();
  const menu = document.body.querySelector('.menu');
  assert.ok(menu, 'the menu is on the page');
  assert.equal(btn.getAttribute('aria-expanded'), 'true');
  assert.equal(btn.getAttribute('aria-controls'), menu.id);
  const items = [...menu.querySelectorAll('.menu-item')];
  assert.equal(document.activeElement, items[0], 'the first item takes focus');
  assert.equal(items[0].tabIndex, 0);
  assert.equal(items[2].tabIndex, -1);
  key(menu, 'ArrowDown');
  assert.equal(document.activeElement, items[2], 'ArrowDown skips the disabled item');
  assert.equal(items[2].tabIndex, 0);
  assert.equal(items[0].tabIndex, -1);
  key(menu, 'ArrowDown');
  assert.equal(document.activeElement, items[0], 'ArrowDown wraps');
  key(menu, 'End');
  assert.equal(document.activeElement, items[2]);
  key(menu, 'Home');
  assert.equal(document.activeElement, items[0]);
  key(menu, 'ArrowUp');
  assert.equal(document.activeElement, items[2], 'ArrowUp wraps the other way');
  key(document, 'Escape');
  assert.equal(document.body.querySelector('.menu'), null, 'Escape closes the menu');
  assert.equal(btn.getAttribute('aria-expanded'), 'false');
  assert.equal(document.activeElement, btn, 'focus goes back to the button');
  btn.remove();
  closeMenus();
});

test('field() points the control at its help and error text and marks it invalid', () => {
  const input = textInput({});
  const f = field({ label: 'Name', input, help: 'What to call it.' });
  const helpId = f.querySelector('.help').id;
  assert.ok(helpId);
  assert.equal(input.getAttribute('aria-describedby'), helpId);
  assert.equal(input.hasAttribute('aria-invalid'), false);
  f.setError('Required');
  const errId = f.querySelector('.error').id;
  assert.ok(errId);
  assert.equal(input.getAttribute('aria-describedby'), `${helpId} ${errId}`);
  assert.equal(input.getAttribute('aria-invalid'), 'true');
  f.setError('');
  assert.equal(input.getAttribute('aria-describedby'), helpId);
  assert.equal(input.hasAttribute('aria-invalid'), false);
  const withError = field({ label: 'Port', input: textInput({}), error: 'Out of range' });
  assert.equal(withError.querySelector('input').getAttribute('aria-invalid'), 'true');
  // A composite control: the label points at the element that takes typing.
  const chips = chipInput({ values: ['a'] });
  const cf = field({ label: 'Tags', input: chips });
  assert.equal(cf.querySelector('label').getAttribute('for'), chips.input.id);
});

test('chipInput renders its chips as a list and announces adds and removes', () => {
  const chips = chipInput({ values: ['alpha', 'beta'] });
  document.body.append(chips);
  const list = chips.querySelector('[role="list"]');
  assert.ok(list);
  assert.deepEqual([...list.querySelectorAll('[role="listitem"]')].map((c) => c.firstChild.textContent), ['alpha', 'beta']);
  const live = chips.querySelector('[aria-live="polite"]');
  assert.ok(live);
  chips.input.value = 'gamma';
  key(chips.input, 'Enter');
  assert.deepEqual(chips.value, ['alpha', 'beta', 'gamma']);
  assert.equal(live.textContent, 'gamma added');
  list.querySelector('[aria-label="Remove alpha"]').click();
  assert.deepEqual(chips.value, ['beta', 'gamma']);
  assert.equal(live.textContent, 'alpha removed');
  assert.equal(document.activeElement, chips.input, 'focus moves to the box when a chip\'s button goes');
  chips.remove();
});

test('status pills are static labels, not live regions; a check chip reads out its last message', () => {
  assert.equal(statusPill('up').hasAttribute('role'), false);
  assert.equal(statusWord('down').hasAttribute('role'), false);
  const chip = checkChip({ name: 'HTTPS' }, { status: 'down', lastMessage: 'connection refused' });
  const hidden = [...chip.querySelectorAll('.sr-only')].map((e) => e.textContent.trim());
  assert.ok(hidden.some((t) => t.includes('connection refused')), 'the last message is in hidden text, not only in title');
});

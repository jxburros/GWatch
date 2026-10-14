'use strict';
/* The agent's control panel. Plain DOM, no libraries, nothing fetched from
   anywhere but the agent that served it. Forms are built once and left alone
   while the status refreshes, so a refresh never eats what someone is typing. */

const $ = (sel, root = document) => root.querySelector(sel);

function h(tag, attrs, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v == null || v === false) continue;
    if (k === 'class') el.className = v;
    else if (k.startsWith('on') && typeof v === 'function') el.addEventListener(k.slice(2), v);
    else if (v === true) el.setAttribute(k, '');
    else el.setAttribute(k, String(v));
  }
  for (const kid of kids.flat(Infinity)) {
    if (kid == null || kid === false) continue;
    el.append(kid instanceof Node ? kid : document.createTextNode(String(kid)));
  }
  return el;
}

async function api(method, path, body) {
  const res = await fetch(path, {
    method,
    credentials: 'same-origin',
    headers: Object.assign({ 'X-GWatch-Panel': '1' }, body !== undefined ? { 'Content-Type': 'application/json' } : {}),
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  let data = null;
  try { data = await res.json(); } catch { /* not JSON */ }
  if (!res.ok) {
    const err = new Error((data && data.error) || `The agent answered HTTP ${res.status}`);
    err.problems = data && data.problems;
    throw err;
  }
  return data;
}

function ago(iso) {
  if (!iso) return 'never';
  const secs = Math.max(0, Math.round((Date.now() - new Date(iso).getTime()) / 1000));
  if (secs < 5) return 'just now';
  if (secs < 90) return `${secs} s ago`;
  const mins = Math.round(secs / 60);
  if (mins < 90) return `${mins} min ago`;
  const hrs = Math.round(mins / 60);
  return hrs < 48 ? `${hrs} h ago` : `${Math.round(hrs / 24)} days ago`;
}

function parseSeconds(text) {
  const m = /^(\d+(?:\.\d+)?)\s*(ms|s|m|h)$/.exec(String(text).trim());
  if (!m) return NaN;
  return Number(m[1]) * { ms: 0.001, s: 1, m: 60, h: 3600 }[m[2]];
}

let state = null;
const ui = { result: null, cards: {} };

/* The cards that hold forms are built once and replaced one at a time, so that
   saving one never throws away what someone has typed into another. The file
   card is replaced after any save, since it shows the whole file. */
function replaceCard(name, build) {
  const fresh = build();
  if (ui.cards[name] && ui.cards[name].isConnected) ui.cards[name].replaceWith(fresh);
  ui.cards[name] = fresh;
}
function afterSave(...names) {
  for (const n of names) replaceCard(n, { settings: buildSettings, collect: buildCollect, token: buildToken, file: buildFile }[n]);
  renderOverview();
  savedBanner();
}

function banner(kind, ...content) {
  const b = $('#banner');
  b.className = `banner ${kind || ''}`;
  b.replaceChildren(h('div', null, ...content));
  b.hidden = false;
  b.scrollIntoView({ block: 'nearest' });
}
function clearBanner() { $('#banner').hidden = true; }

function readOnly() { return state && !state.file.writable; }

/* ---------- overview ---------- */

function health() {
  const s = state.status;
  const svc = state.service;
  if (!state.settings.server && !state.tokenSet) return { cls: 'pill-warn', text: 'Not set up', line: 'This machine has not been paired with a GWatch server yet.' };
  if (svc.managed && svc.state === 'not installed') return { cls: 'pill-unknown', text: 'Not installed', line: svc.hint || 'The agent is not installed as a service.' };
  if (svc.managed && svc.state === 'stopped') return { cls: 'pill-warn', text: 'Stopped', line: 'The agent service is not running.' };
  if (!s) return { cls: 'pill-unknown', text: 'No report yet', line: 'The agent has not written a status yet; if the service is running it will within a minute.' };
  const interval = parseSeconds(s.interval) || 60;
  const stale = state.statusAgeSeconds > Math.max(180, interval * 3);
  if (stale) return { cls: 'pill-warn', text: 'Not running?', line: `The agent last updated its status ${ago(s.updatedAt)}.` };
  if (!s.reportOk && s.lastReport) return { cls: 'pill-down', text: "Can't reach GWatch", line: s.reportError || 'The last reading did not go through.' };
  return { cls: 'pill-up', text: 'Reporting', line: `Sending a reading about every ${s.interval}.` };
}

function renderOverview() {
  const box = $('#overview-body');
  const s = state.status;
  const hl = health();
  const pill = $('#svc-pill');
  pill.className = `pill ${hl.cls}`;
  pill.textContent = hl.text;
  $('#sub').textContent = `Version ${state.version} · ${state.platform}`;

  const fact = (k, v) => h('div', null, h('dt', null, k), h('dd', null, v || '—'));
  const svc = state.service;
  const controls = svc.managed
    ? h('div', { class: 'row' },
        h('button', { type: 'button', disabled: readOnly() && false, onclick: () => serviceAction('start') }, 'Start'),
        h('button', { type: 'button', onclick: () => serviceAction('stop') }, 'Stop'),
        h('button', { type: 'button', onclick: () => serviceAction('restart') }, 'Restart'))
    : h('p', { class: 'note' }, `The service is managed by ${svc.manager}. To restart it: `, h('code', null, svc.hint || ''));

  box.replaceChildren(
    h('p', { class: 'lead' }, hl.line),
    h('dl', { class: 'facts' },
      fact('GWatch server', state.settings.server),
      fact('This machine is called', state.settings.name || '(its own hostname)'),
      fact('Service', svc.state + (svc.error ? ` — ${svc.error}` : '')),
      fact('Last reading sent', s && s.lastReport ? `${ago(s.lastReport)}${s.reportOk ? '' : ' (failed)'}` : 'never'),
      fact('Last asked GWatch for settings', s && s.lastCheckIn ? `${ago(s.lastCheckIn)}${s.checkInError ? ' (failed)' : ''}` : 'not yet'),
      fact('Readings waiting to be sent', s ? String(s.held || 0) : '—'),
      s && s.latestAgent ? fact('Newest agent GWatch knows of', s.latestAgent) : null),
    controls,
    h('div', { class: 'row' },
      h('button', { type: 'button', onclick: testReading }, 'Send a test reading'),
      h('button', { type: 'button', onclick: checkUpdates }, 'Check for updates')),
    ui.result || (ui.result = h('p', { class: 'result', role: 'status', 'aria-live': 'polite' })));
}

function say(text, good) {
  ui.result.className = `result ${good === true ? 'good' : good === false ? 'bad' : ''}`;
  ui.result.textContent = text;
}

async function testReading(ev) {
  ev.target.disabled = true;
  say('Sending a reading…');
  try {
    const out = await api('POST', '/api/test');
    say(out.message, out.ok);
  } catch (e) { say(e.message, false); }
  ev.target.disabled = false;
}

async function checkUpdates(ev) {
  ev.target.disabled = true;
  say('Looking for a newer agent…');
  try {
    const out = await api('POST', '/api/update-check');
    say(out.message, out.ok);
  } catch (e) { say(e.message, false); }
  ev.target.disabled = false;
}

async function serviceAction(action) {
  say(`${action[0].toUpperCase()}${action.slice(1)}ing the service…`.replace('Stoping', 'Stopping'));
  try {
    state = await api('POST', `/api/service/${action}`);
    clearBanner();
    renderOverview();
    say(`Done: the service is ${state.service.state}.`, true);
  } catch (e) { say(e.message, false); }
}

/* ---------- settings ---------- */

function card(title, ...body) {
  return h('section', { class: 'card' }, h('div', { class: 'head' }, h('h2', null, title)), h('div', { class: 'body' }, body));
}

function textField(id, label, value, help, opts = {}) {
  const input = h('input', { type: 'text', id, value: value || '', autocomplete: 'off', spellcheck: 'false', readonly: opts.readonly });
  return { el: h('div', { class: 'field' }, h('label', { for: id }, label), input, help ? h('span', { class: 'help' }, help) : null), input };
}

function checkField(id, label, help, checked) {
  const input = h('input', { type: 'checkbox', id, checked });
  return { el: h('div', { class: 'field' }, h('label', { class: 'check', for: id }, input, h('span', { class: 't' }, label, help ? h('span', { class: 'help' }, help) : null))), input };
}

function showProblems(host, e) {
  host.replaceChildren();
  if (e.problems && e.problems.length) host.append(h('ul', { class: 'problems' }, e.problems.map((p) => h('li', null, p))));
}

function savedBanner() {
  const svc = state.service;
  const restart = svc.managed && svc.state !== 'not installed'
    ? h('button', { type: 'button', class: 'primary', onclick: () => serviceAction('restart') }, 'Restart the agent now')
    : null;
  banner('good', `Saved to ${state.file.path}. The agent applies changes when it restarts.`, ' ', restart);
}

function buildSettings() {
  const st = state.settings;
  const f = {
    server: textField('f-server', 'GWatch server', st.server, 'The address of your GWatch, with the port: https://gwatch.lan:7230'),
    name: textField('f-name', 'Name for this machine', st.name, 'Leave empty to use this computer’s own name.'),
    interval: textField('f-interval', 'Send a reading every', st.interval, `Like 30s or 1m. At least 10s. Default ${state.defaults.interval}.`),
    checkIn: textField('f-checkin', 'Ask GWatch for settings every', st.checkIn, `Like 15m or 1h. Default ${state.defaults.checkIn}.`),
    buffer: textField('f-buffer', 'Keep readings if GWatch is unreachable for up to', st.buffer, `Held in memory and sent when GWatch is back. 0s keeps nothing. Default ${state.defaults.buffer}, at most 48h.`),
    pin: textField('f-pin', 'Pinned server certificate', st.certPin, 'Optional SHA-256 of the server’s public key.'),
  };
  const auto = checkField('f-auto', 'Keep this agent up to date', 'Installs new signed agent releases by itself.', st.autoUpdate);
  const insecure = checkField('f-insecure', 'Accept a self-signed certificate', 'Only if you recognise the server’s certificate. A pinned certificate is safer.', st.insecure);
  const problems = h('div');
  const save = h('button', { type: 'submit', class: 'primary', disabled: readOnly() }, 'Save settings');
  const form = h('form', { class: 'grid', novalidate: true, onsubmit: async (ev) => {
    ev.preventDefault();
    save.disabled = true;
    try {
      state = await api('PUT', '/api/settings', {
        server: f.server.input.value, name: f.name.input.value, interval: f.interval.input.value,
        checkIn: f.checkIn.input.value, buffer: f.buffer.input.value, certPin: f.pin.input.value,
        autoUpdate: auto.input.checked, insecure: insecure.input.checked,
      });
      problems.replaceChildren();
      afterSave('file');
    } catch (e) { showProblems(problems, e); banner('bad', e.message); }
    save.disabled = readOnly();
  } },
  Object.values(f).map((x) => x.el), auto.el, insecure.el);
  return card('Settings', form, problems, h('div', { class: 'row' }, save),
    h('p', { class: 'note' }, 'Everything here is stored in the configuration file below. Changes take effect when the agent restarts.'));
}

/* ---------- collection ---------- */

const CATEGORIES = [
  { key: 'interfaces', title: 'Network interfaces', hint: 'Automatic leaves out virtual, bridge and tunnel interfaces.' },
  { key: 'disks', title: 'Disks (throughput)', hint: 'Automatic reports every physical disk.' },
  { key: 'mounts', title: 'Filesystems (space)', hint: 'Automatic reports every mounted filesystem.' },
];

function buildCollect() {
  const coll = state.settings.collect || {};
  const inv = (state.status && state.status.inventory) || {};
  const chosenFamilies = new Set(coll.metrics && coll.metrics.length ? coll.metrics : state.families.map((x) => x.key));
  const famBoxes = state.families.map((fam) => {
    const input = h('input', { type: 'checkbox', id: `fam-${fam.key}`, checked: chosenFamilies.has(fam.key) });
    return { fam, input, el: h('label', { class: 'check', for: `fam-${fam.key}` }, input, h('span', { class: 't' }, fam.label)) };
  });

  const cats = CATEGORIES.map((cat) => {
    const items = inv[cat.key] || [];
    const current = new Set(coll[cat.key] || []);
    const only = current.size > 0;
    const seen = new Set(items.map((i) => i.name));
    const rows = items.map((i) => ({ name: i.name, detail: i.detail, auto: i.auto, here: true }))
      .concat([...current].filter((n) => !seen.has(n)).map((n) => ({ name: n, here: false })));
    const boxes = rows.map((r, idx) => {
      const input = h('input', { type: 'checkbox', id: `${cat.key}-${idx}`, checked: only ? current.has(r.name) : r.auto !== false });
      return { name: r.name, input, el: h('label', { class: 'check', for: `${cat.key}-${idx}` }, input,
        h('span', { class: 't' }, h('span', null, r.name, r.auto === false ? h('span', { class: 'tag' }, 'hidden by default') : null, r.here ? null : h('span', { class: 'tag' }, 'not seen now')),
          r.detail ? h('span', { class: 'd' }, r.detail) : null)) };
    });
    const list = h('div', { class: 'cols' }, boxes.map((b) => b.el));
    const modeAuto = h('input', { type: 'radio', name: `mode-${cat.key}`, id: `mode-${cat.key}-auto`, checked: !only });
    const modeOnly = h('input', { type: 'radio', name: `mode-${cat.key}`, id: `mode-${cat.key}-only`, checked: only });
    const sync = () => { list.hidden = !modeOnly.checked; };
    modeAuto.addEventListener('change', sync);
    modeOnly.addEventListener('change', sync);
    sync();
    return {
      key: cat.key,
      el: h('fieldset', null, h('legend', null, cat.title),
        h('div', { class: 'row' },
          h('label', { class: 'check', for: modeAuto.id }, modeAuto, h('span', { class: 't' }, 'Automatic')),
          h('label', { class: 'check', for: modeOnly.id }, modeOnly, h('span', { class: 't' }, 'Only the ones I tick'))),
        h('p', { class: 'note' }, cat.hint),
        items.length === 0 && !only ? h('p', { class: 'note' }, 'This list appears after the agent has reported once.') : null,
        list),
      value: () => (modeOnly.checked ? boxes.filter((b) => b.input.checked).map((b) => b.name) : []),
    };
  });

  const problems = h('div');
  const save = h('button', { type: 'button', class: 'primary', disabled: readOnly(), onclick: async () => {
    const ticked = famBoxes.filter((b) => b.input.checked).map((b) => b.fam.key);
    const collect = { metrics: ticked.length === famBoxes.length ? [] : ticked };
    for (const c of cats) collect[c.key] = c.value();
    save.disabled = true;
    try {
      state = await api('PUT', '/api/settings', { collect });
      problems.replaceChildren();
      afterSave('file');
    } catch (e) { showProblems(problems, e); banner('bad', e.message); }
    save.disabled = readOnly();
  } }, 'Save what to collect');

  const gw = state.status && state.status.fromGwatch;
  const gwText = gw && ((gw.metrics || []).length + (gw.interfaces || []).length + (gw.disks || []).length + (gw.mounts || []).length) > 0
    ? 'GWatch has asked for a particular selection; the agent applies it inside the limits set here.'
    : 'GWatch has not asked for a particular selection, so the agent reports everything allowed here.';
  return card('What this machine reports',
    h('p', { class: 'lead' }, 'These are the limits of what leaves this machine. An administrator in GWatch can narrow them further for this machine, never widen them.'),
    h('fieldset', null, h('legend', null, 'Kinds of reading'), h('div', { class: 'cols' }, famBoxes.map((b) => b.el))),
    cats.map((c) => c.el), problems, h('div', { class: 'row' }, save), h('p', { class: 'note' }, gwText));
}

/* ---------- token ---------- */

function buildToken() {
  const view = h('div', { class: 'token', hidden: true });
  const show = h('button', { type: 'button', onclick: async () => {
    if (!view.hidden) { view.hidden = true; show.textContent = 'Show token'; return; }
    try {
      const out = await api('POST', '/api/token');
      view.textContent = out.token || '(this machine has no token yet)';
      view.hidden = false;
      show.textContent = 'Hide token';
      copy.hidden = !out.token;
    } catch (e) { banner('bad', e.message); }
  } }, 'Show token');
  const copy = h('button', { type: 'button', hidden: true, onclick: async () => {
    try { await navigator.clipboard.writeText(view.textContent); say('Token copied.', true); } catch { say('Could not copy; select it and copy by hand.', false); }
  } }, 'Copy');
  return card('Token',
    h('p', { class: 'lead' }, 'The credential this machine uses to send its readings. It can do that one thing and nothing else.'),
    h('div', { class: 'token' }, state.tokenPreview + (state.tokenSet ? `  (from the ${state.tokenSource})` : '')),
    h('div', { class: 'row' }, show, copy), view,
    h('p', { class: 'note' }, 'To check it matches, compare it with the token GWatch shows on this machine’s node (Agent › Token). If an administrator rotates the token in GWatch, this agent picks up the new one by itself and updates the file.'));
}

/* ---------- the file ---------- */

function buildFile() {
  const area = h('textarea', { id: 'file-text', 'aria-label': 'Configuration file', spellcheck: 'false' });
  area.value = state.fileText || '';
  const problems = h('div');
  const save = h('button', { type: 'button', class: 'primary', disabled: readOnly(), onclick: async () => {
    save.disabled = true;
    try {
      state = await api('PUT', '/api/file', { text: area.value });
      problems.replaceChildren();
      afterSave('settings', 'collect', 'token', 'file');
    } catch (e) { showProblems(problems, e); banner('bad', e.message); }
    save.disabled = readOnly();
  } }, 'Save file');
  const revert = h('button', { type: 'button', onclick: () => { area.value = state.fileText || ''; problems.replaceChildren(); } }, 'Revert');
  return card('The configuration file',
    h('p', { class: 'lead' }, h('code', null, state.file.path), state.file.exists ? '' : ' (not created yet)'),
    area, problems, h('div', { class: 'row' }, save, revert),
    h('p', { class: 'note' }, 'The same settings as above, as text. The agent refuses to save a file it could not run with.'));
}

/* ---------- page ---------- */

function renderAll(rebuildForms = true) {
  if (rebuildForms) {
    const app = $('#app');
    ui.cards = { settings: buildSettings(), collect: buildCollect(), token: buildToken(), file: buildFile() };
    ui.result = null;
    app.replaceChildren(
      h('section', { class: 'card' }, h('div', { class: 'head' }, h('h2', null, 'Status')), h('div', { class: 'body', id: 'overview-body' })),
      ui.cards.settings, ui.cards.collect, ui.cards.token, ui.cards.file);
  }
  renderOverview();
  const f = state.file;
  if (rebuildForms) {
    if (f.problems && f.problems.length) banner('bad', 'The configuration file has problems: ', f.problems.join('; '), ' Fix them in the file editor below.');
    else if (!f.writable) banner('warn', 'This account cannot change the settings: ', 'run `sudo gwatch-agent ui` (on Windows, from an Administrator terminal) to edit them from here. You can still look around.');
  }
}

async function poll() {
  try {
    const fresh = await api('GET', '/api/state');
    // Only the status moves on its own; the forms are left as they are.
    state = Object.assign(state, { status: fresh.status, statusAgeSeconds: fresh.statusAgeSeconds, service: fresh.service });
    renderOverview();
  } catch { /* the panel closed, or the network blinked; the next tick tries again */ }
}

(async function start() {
  try {
    state = await api('GET', '/api/state');
    renderAll(true);
    setInterval(poll, 5000);
  } catch (e) {
    $('#app').replaceChildren(h('p', { class: 'note' }, `Could not load the panel: ${e.message}. Run gwatch-agent ui again for a fresh address.`));
  }
})();

// Discovery: ping a range of addresses, show what answered, and turn the ones
// you tick into nodes.
//
// The whole thing lives in one modal because it is one errand — "what is on my
// network, and which of it do I want to watch" — and splitting it across a
// page would mean leaving the node list to come back to it. The modal has
// three states and shows exactly one at a time: the form, the sweep running,
// and the results.

import { api, subscribeUpdates } from '../api.js';
import { h, icon, clear, replace, openModal, toast, field, textarea, textInput, selectInput, emptyState, banner } from '../components.js';
import { ms, plural } from '../fmt.js';

// The ports the server probes unless it is told otherwise. They are spelled
// out here rather than fetched so the field has something in it the moment the
// modal opens; the server has the same list and the last word on it.
const DEFAULT_PORTS = [22, 80, 443, 445, 3389, 8080, 8443, 9100, 32400, 1883];

// How often to ask the server where a run has got to. The update stream
// carries the same figures and arrives sooner, so this is the floor rather
// than the mechanism: it is what keeps the bar moving on a browser whose
// EventSource has fallen over, and what notices that a run has finished.
const POLL_MS = 2000;

/**
 * Open the discovery modal. `onDone` is called after nodes have been created,
 * so the caller can reload whatever list it is showing.
 */
export async function openDiscovery(ctx, onDone) {
  const state = {
    job: null,          // the run being watched, as the server last described it
    templates: [],      // for the per-row template picker
    groups: [],         // for the group field's suggestions
    selected: new Set(),// the addresses ticked in the results table
    closed: false,
    adding: false,
  };

  const body = h('div', { class: 'stack' });
  const foot = h('div', { class: 'btn-group' });
  const m = openModal({
    title: 'Discover devices',
    wide: true,
    body,
    footer: foot,
    onClose: () => {
      state.closed = true;
      stopWatching();
      // A run is deliberately left going: it is the install's run, not this
      // modal's, and reopening picks it back up where it got to.
    },
  });

  // ---- the form ----

  const rangesInput = textarea({
    rows: 3,
    placeholder: '192.168.1.0/24\n192.168.1.10-50\n10.0.0.5',
    spellcheck: 'false',
    autocapitalize: 'off',
    autocomplete: 'off',
  });
  const rangesField = field({
    label: 'Ranges to scan',
    input: rangesInput,
    help: 'One per line: a block such as 192.168.1.0/24, a range such as 192.168.1.10-50, or a single address. IPv4, up to 4096 addresses in one run.',
  });

  const portsInput = textInput({
    value: DEFAULT_PORTS.join(', '),
    spellcheck: 'false',
    autocomplete: 'off',
  });
  const portsField = field({
    label: 'Ports to try on whatever answers',
    input: portsInput,
    help: 'Only used to guess what each device is. Clear the field to probe nothing.',
  });

  const startBtn = h('button', { class: 'btn btn-primary', type: 'button', onclick: () => start() }, icon('radar'), 'Start scan');
  const cancelBtn = h('button', { class: 'btn', type: 'button', onclick: () => cancel() }, 'Stop scan');
  const againBtn = h('button', { class: 'btn', type: 'button', onclick: () => reset() }, icon('refresh'), 'Scan again');
  const addBtn = h('button', { class: 'btn btn-primary', type: 'button', onclick: () => addSelected() }, icon('plus'), 'Add selected');
  const closeBtn = h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, 'Close');

  // Enter in the ranges box is a newline, so the shortcut is the usual one for
  // "submit the thing I am typing into".
  rangesInput.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); start(); }
  });

  // ---- progress ----

  const progressLabel = h('span', { class: 'meter-label' }, 'Scanning');
  const progressValue = h('span', { class: 'meter-value' }, '—');
  const progressFill = h('div', { class: 'meter-fill', style: { width: '0%' } });
  const progressTrack = h('div', {
    class: 'meter-track', role: 'progressbar',
    'aria-valuemin': '0', 'aria-valuemax': '100', 'aria-valuenow': '0',
  }, progressFill);
  const progressDetail = h('div', { class: 'meter-detail muted' }, '');
  const progressEl = h('div', { class: 'meter' },
    h('div', { class: 'meter-head' }, progressLabel, progressValue),
    progressTrack,
    progressDetail,
  );

  // ---- results ----

  const groupInput = textInput({ placeholder: 'e.g. Home Network', autocomplete: 'off', list: 'discovery-groups' });
  // The suggestions are a plain <datalist>, so the field stays an ordinary
  // text input: type a new group or pick one that already exists, and the
  // keyboard behaves the way it does in every other browser field.
  const groupList = h('datalist', { id: 'discovery-groups' });
  const groupField = field({
    label: 'Put them in a group',
    input: groupInput,
    help: 'Optional. Leave it blank to use each template’s own group.',
  });
  groupField.append(groupList);
  const errorEl = h('div');

  // ---- wiring ----

  let pollTimer = null;
  let unsubscribe = null;

  function stopWatching() {
    if (pollTimer) { clearInterval(pollTimer); pollTimer = null; }
    if (unsubscribe) { unsubscribe(); unsubscribe = null; }
  }

  function startWatching() {
    if (pollTimer) return;
    // The stream carries the same counters and arrives within a few hundred
    // milliseconds of each step, so it is what actually animates the bar. The
    // poll is the safety net: a browser whose EventSource has fallen over
    // still sees the bar move, and still notices that the run has finished.
    pollTimer = setInterval(refresh, POLL_MS);
    unsubscribe = subscribeUpdates((u) => {
      if (!u || u.kind !== 'discovery' || !u.discovery) return;
      if (state.job && u.discovery.id !== state.job.id) return;
      applyProgress(u.discovery);
      if (u.discovery.state && u.discovery.state !== 'running') refresh();
    });
  }

  /** Fold a stream update's counters into the job on screen. The stream sends
   *  progress, not results, so a finished run is confirmed by a refresh(). */
  function applyProgress(p) {
    if (!state.job) return;
    state.job = { ...state.job, scanned: p.scanned, total: p.total, responders: p.responders };
    if (state.job.state === 'running') render();
  }

  async function refresh() {
    if (state.closed || !state.job) return;
    try {
      const job = await api.get(`/api/discovery/${encodeURIComponent(state.job.id)}`);
      if (state.closed) return;
      state.job = job;
      if (job.state !== 'running') stopWatching();
      render();
    } catch (e) {
      // A run the server no longer knows about (it was superseded, or the
      // service restarted) is not an error worth shouting about — the form
      // comes back and the person can scan again.
      if (e.status === 404) { stopWatching(); state.job = null; render(); }
    }
  }

  async function start() {
    showError('');
    const ranges = rangesInput.value.split('\n').map((s) => s.trim()).filter(Boolean);
    if (!ranges.length) {
      showError('Type at least one range, such as 192.168.1.0/24.');
      rangesInput.focus();
      return;
    }
    const ports = portsInput.value.split(/[\s,;]+/).map((s) => s.trim()).filter(Boolean).map(Number);
    if (ports.some((p) => !Number.isInteger(p) || p < 1 || p > 65535)) {
      showError('Ports must be whole numbers between 1 and 65535.');
      portsInput.focus();
      return;
    }
    startBtn.disabled = true;
    try {
      state.job = await api.post('/api/discovery', { ranges, ports });
      state.selected.clear();
      startWatching();
      render();
    } catch (e) {
      showError(e.message);
    } finally {
      startBtn.disabled = false;
    }
  }

  async function cancel() {
    if (!state.job) return;
    cancelBtn.disabled = true;
    try {
      state.job = await api.post(`/api/discovery/${encodeURIComponent(state.job.id)}/cancel`);
      stopWatching();
      render();
    } catch (e) {
      showError(e.message);
    } finally {
      cancelBtn.disabled = false;
    }
  }

  function reset() {
    stopWatching();
    state.job = null;
    state.selected.clear();
    showError('');
    render();
    rangesInput.focus();
  }

  async function addSelected() {
    const rows = responders().filter((r) => state.selected.has(r.ip));
    if (!rows.length) {
      showError('Tick at least one device first.');
      return;
    }
    showError('');
    state.adding = true;
    addBtn.disabled = true;
    const group = groupInput.value.trim();
    try {
      const items = rows.map((r) => ({
        ip: r.ip,
        name: (r.nameInput?.value || '').trim() || undefined,
        template: r.templateValue || undefined,
      }));
      // Both spellings of the group go out: one is the field as it is, the
      // other is the list it is becoming, and the server takes whichever it
      // understands.
      const body = { items };
      if (group) { body.group = group; body.groups = [group]; }
      const res = await api.post(`/api/discovery/${encodeURIComponent(state.job.id)}/add`, body);
      const created = res?.created?.length || 0;
      const skipped = res?.skipped || [];
      const message = skipped.length
        ? `Added ${plural(created, 'node')}; skipped ${skipped.length} (${skipped.map((s) => s.ip).join(', ')})`
        : `Added ${plural(created, 'node')}`;
      toast(message, { kind: created ? 'success' : 'info' });
      if (onDone) await onDone();
      if (created) m.close();
      else render();
    } catch (e) {
      showError(e.message);
    } finally {
      state.adding = false;
      addBtn.disabled = false;
    }
  }

  function showError(msg) {
    clear(errorEl);
    if (msg) errorEl.append(banner('down', msg));
  }

  // ---- rendering ----

  // The rows currently on screen, so "add selected" reads the name field and
  // the template picker the person actually touched.
  let rows = [];
  const responders = () => rows;

  function render() {
    const job = state.job;
    clear(foot);
    if (!job) {
      replace(body, errorEl, rangesField, portsField);
      foot.append(closeBtn, startBtn);
      return;
    }
    if (job.state === 'running') {
      updateProgress();
      replace(body, errorEl, summaryLine(), progressEl);
      foot.append(cancelBtn);
      return;
    }
    updateProgress();
    replace(body, errorEl, summaryLine(), resultsTable(), groupField);
    foot.append(closeBtn, againBtn, addBtn);
    addBtn.disabled = state.adding || !state.selected.size;
  }

  // One element, written into rather than replaced, so that its role="status"
  // announces the run's outcome once — when it changes — instead of on every
  // one of the dozens of renders a sweep produces.
  const summaryEl = h('p', { role: 'status' });

  function summaryLine() {
    const job = state.job;
    const what = (job.ranges || []).join(', ') || 'the ranges given';
    if (job.state === 'running') replace(summaryEl, 'Pinging ', h('b', null, what), '. This takes a minute or two for a full /24 — you can stop it at any time.');
    else if (job.state === 'cancelled') replace(summaryEl, 'Stopped after ', plural(job.scanned || 0, 'address', 'addresses'), ' of ', what, '. Whatever had already answered is below.');
    else if (job.state === 'failed') replace(summaryEl, 'The scan of ', what, ' stopped: ', job.error || 'no reason given', '.');
    else replace(summaryEl, 'Scanned ', plural(job.scanned || 0, 'address', 'addresses'), ' in ', h('b', null, what), ' — ', plural(job.responders || 0, 'device', 'devices'), ' answered.');
    return summaryEl;
  }

  function updateProgress() {
    const job = state.job;
    const total = job.total || 0;
    const scanned = Math.min(job.scanned || 0, total);
    const donePct = total ? Math.round((scanned / total) * 100) : 0;
    progressLabel.textContent = job.state === 'running' ? 'Scanning' : 'Scanned';
    progressValue.textContent = `${scanned} / ${total}`;
    progressFill.style.width = `${donePct}%`;
    progressTrack.setAttribute('aria-valuenow', String(donePct));
    progressTrack.setAttribute('aria-label', `Scanned ${scanned} of ${total} addresses`);
    progressDetail.textContent = `${plural(job.responders || 0, 'device', 'devices')} so far`;
  }

  function resultsTable() {
    rows = [];
    const found = state.job.results || [];
    if (!found.length) {
      return h('div', { class: 'card' }, emptyState({
        icon: 'radar',
        title: 'Nothing answered',
        text: 'No address in that range replied to a ping. Some devices ignore pings by design — a firewalled PC, for one — so you may still want to add them by hand.',
        compact: true,
      }));
    }

    const allBox = h('input', {
      type: 'checkbox', 'aria-label': 'Select every device that answered',
      onchange: () => {
        for (const r of rows) { r.box.checked = allBox.checked; }
        state.selected = new Set(allBox.checked ? found.map((f) => f.ip) : []);
        addBtn.disabled = state.adding || !state.selected.size;
      },
    });

    const tbody = h('tbody');
    for (const f of found) {
      const ip = f.ip;
      const box = h('input', {
        type: 'checkbox', checked: state.selected.has(ip),
        'aria-label': `Add ${f.hostname || ip}`,
        onchange: () => {
          if (box.checked) state.selected.add(ip); else state.selected.delete(ip);
          allBox.checked = state.selected.size === found.length;
          addBtn.disabled = state.adding || !state.selected.size;
        },
      });
      const nameInput = textInput({
        value: f.hostname || ip,
        'aria-label': `Name for ${ip}`,
        autocomplete: 'off',
      });
      const templateSel = selectInput({
        options: state.templates.map((t) => ({ value: t.id, label: t.name })),
        value: f.template || 'ping',
        'aria-label': `Template for ${ip}`,
      });
      const row = { ip, box, nameInput, get templateValue() { return templateSel.value; } };
      rows.push(row);

      tbody.append(h('tr', null,
        h('td', null, box),
        h('td', { class: 'mono' }, ip),
        h('td', null, nameInput),
        // The name fields take what space there is, so the round trip is
        // pinned to one line rather than being allowed to wrap mid-figure.
        h('td', { class: 'num', style: { whiteSpace: 'nowrap' } }, ms(f.rttMs)),
        h('td', { class: 'mono' }, (f.openPorts || []).join(' ') || h('span', { class: 'dim' }, '—')),
        h('td', null, templateSel, f.note ? h('div', { class: 'tiny muted' }, f.note) : null),
      ));
    }

    const table = h('table', { class: 'table' },
      h('thead', null, h('tr', null,
        h('th', null, allBox),
        h('th', null, 'Address'),
        h('th', null, 'Name'),
        h('th', { class: 'num' }, 'Reply'),
        h('th', null, 'Open ports'),
        h('th', null, 'Start from'),
      )),
      tbody,
    );
    return h('div', { class: 'table-wrap' }, table);
  }

  // ---- what the modal needs before it can draw anything ----

  render();

  const [templates, groups, network, latest] = await Promise.all([
    api.get('/api/templates').catch(() => []),
    api.get('/api/groups').catch(() => ({ groups: [] })),
    api.get('/api/network').catch(() => null),
    // A run that is already going, or the last one that finished: reopening
    // the modal rejoins it rather than starting from an empty form.
    api.get('/api/discovery').catch(() => null),
  ]);
  if (state.closed) return;

  state.templates = templates || [];
  state.groups = (groups?.groups || []).map((g) => g.name);
  replace(groupList, state.groups.map((g) => h('option', { value: g })));

  const guess = guessRange(network);
  if (guess) rangesInput.value = guess;

  if (latest && latest.id) {
    state.job = latest;
    if (latest.state === 'running') startWatching();
  }
  render();
  if (!state.job) rangesInput.focus();
}

/**
 * Work out the /24 this machine is on, so the field opens with the answer
 * already in it. NetworkInfo lists the addresses GWatch is reachable at; the
 * first private IPv4 among them is the network the person is looking for.
 * Returns '' when there is nothing to go on, and the placeholder does the
 * explaining instead.
 */
export function guessRange(network) {
  for (const url of network?.lanUrls || []) {
    const match = String(url).match(/(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})/);
    if (!match) continue;
    const [a, b, c] = [Number(match[1]), Number(match[2]), Number(match[3])];
    if ([a, b, c, Number(match[4])].some((n) => n > 255)) continue;
    const isPrivate = a === 10 || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168);
    if (!isPrivate) continue;
    return `${a}.${b}.${c}.0/24`;
  }
  return '';
}

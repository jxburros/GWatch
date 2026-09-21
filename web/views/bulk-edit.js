// Bulk edit: change one setting across many nodes and checks at once.
//
// Changing the check frequency on thirty nodes through the editor is thirty
// visits to the same form. This screen is the other shape of that job: pick
// what to change on the left, say what to change it to on the right, read the
// sentence it builds, and apply it once.
//
// Nothing is written until Apply, and the sentence above the button is the
// promise the request then keeps — so it is built from the same selection and
// the same rows that go into the body, not from a second description of them.

import { api } from '../api.js';
import { h, icon, clear, replace, field, textInput, numberInput, selectInput, chipInput, toast, confirmDialog, emptyState, skeleton, checkTypeLabel, busy, statusWord, uid } from '../components.js';
import { interval as fmtInterval, nodeGroups, inGroup } from '../fmt.js';
import { BULK_FIELDS, bulkField, bulkValue, setPath, ON_OFF_OPTIONS, TRI } from './node-fields.js';

const STATUS_ORDER = ['down', 'degraded', 'unknown', 'maintenance', 'up', 'paused'];

export async function mount(root, ctx) {
  const state = {
    nodes: [], groups: { groups: [], tags: [] },
    q: '', group: ctx.query.get('group') || '', status: '', tag: '',
    nodeIds: new Set(),   // nodes whose node-level settings change
    checkIds: new Set(),  // checks whose check-level settings change
    types: new Set(),     // narrow the check patch to these types; empty = all
    rows: [],             // the change builder: [{ id, key, value }]
    destroyed: false, applying: false,
  };

  ctx.setTitle('Bulk edit', {
    actions: [h('a', { class: 'btn', href: '#/nodes' }, icon('arrowLeft'), 'Back to nodes')],
  });

  const root2 = h('div', { class: 'bulk-layout' });
  const pickSide = h('section', { class: 'bulk-pick' }, skeleton({ lines: 6 }));
  const buildSide = h('section', { class: 'bulk-build' });
  root2.append(pickSide, buildSide);
  root.append(root2);

  const [nodes, groups] = await Promise.all([
    api.get('/api/nodes').catch(() => []),
    api.get('/api/groups').catch(() => ({ groups: [], tags: [] })),
  ]);
  if (state.destroyed) return {};
  state.nodes = nodes || [];
  state.groups = groups || { groups: [], tags: [] };

  if (!state.nodes.length) {
    replace(root, h('div', { class: 'card' }, emptyState({
      icon: 'server', title: 'Nothing to edit yet',
      text: 'Bulk edit changes settings across nodes you already have. Add a node first.',
      actions: h('a', { class: 'btn btn-primary', href: '#/nodes' }, 'Go to Nodes'),
    })));
    return {};
  }

  /* ---------- Left: what to change ---------- */

  const searchInput = h('input', { type: 'search', placeholder: 'Search name, host, group or tag…', 'aria-label': 'Search nodes', oninput: () => { state.q = searchInput.value; renderPick(); } });
  const countEl = h('span', { class: 'filter-count' });
  const filters = h('div', { class: 'filters' });
  const listEl = h('div', { class: 'bulk-nodes' });
  const selectAll = h('button', { class: 'btn btn-sm', type: 'button', onclick: () => toggleAllVisible() });

  function visibleNodes() {
    return state.nodes.filter((n) => {
      if (state.group && !inGroup(n, state.group)) return false;
      if (state.status && (n.status || 'unknown') !== state.status) return false;
      if (state.tag && !(n.tags || []).includes(state.tag)) return false;
      if (state.q) {
        const q = state.q.toLowerCase();
        const hay = [n.name, n.host, ...nodeGroups(n), ...(n.tags || []), ...(n.checks || []).map((c) => c.name)].filter((x) => x != null).join(' ').toLowerCase();
        if (!hay.includes(q)) return false;
      }
      return true;
    }).sort((a, b) => a.name.localeCompare(b.name));
  }

  function chipRow(label, items, current, onPick) {
    if (!items.length) return null;
    const row = h('div', { class: 'chip-row', role: 'group', 'aria-label': label }, h('span', { class: 'chip-label' }, label));
    row.append(h('button', { type: 'button', class: `chip ${!current ? 'active' : ''}`, 'aria-pressed': !current ? 'true' : 'false', onclick: () => onPick('') }, 'All'));
    for (const it of items) row.append(h('button', { type: 'button', class: `chip ${current === it.value ? 'active' : ''}`, 'aria-pressed': current === it.value ? 'true' : 'false', onclick: () => onPick(current === it.value ? '' : it.value) }, it.label));
    return row;
  }

  function renderFilters() {
    clear(filters);
    const counts = {};
    for (const n of state.nodes) counts[n.status || 'unknown'] = (counts[n.status || 'unknown'] || 0) + 1;
    filters.append(
      chipRow('Group', state.groups.groups.map((g) => ({ value: g.name, label: `${g.name} (${g.count})` })), state.group, (v) => { state.group = v; renderFilters(); renderPick(); }),
      chipRow('Status', STATUS_ORDER.filter((s) => counts[s]).map((s) => ({ value: s, label: `${s[0].toUpperCase()}${s.slice(1)} (${counts[s]})` })), state.status, (v) => { state.status = v; renderFilters(); renderPick(); }),
      chipRow('Tag', state.groups.tags.map((t) => ({ value: t.name, label: t.name })), state.tag, (v) => { state.tag = v; renderFilters(); renderPick(); }),
    );
  }

  /** Every check id of a node, as strings are never used for ids here. */
  const checksOf = (n) => n.checks || [];

  // Ticking a box must not rebuild the list under the pointer: a rebuilt
  // checkbox is a different element, and the focus ring — the only thing a
  // keyboard user has to hold their place with — goes with the old one. So
  // rows are built when the filters change and only their boxes are written
  // back when the selection does.
  const rowSync = new Map(); // node id -> () => void

  function setNodeSelected(n, on) {
    // Ticking a node ticks its checks with it: "change this node" almost
    // always means "and the things it is checked by".
    if (on) state.nodeIds.add(n.id); else state.nodeIds.delete(n.id);
    for (const c of checksOf(n)) { if (on) state.checkIds.add(c.id); else state.checkIds.delete(c.id); }
    rowSync.get(n.id)?.();
    afterSelection();
  }

  function setCheckSelected(n, c, on) {
    if (on) state.checkIds.add(c.id); else state.checkIds.delete(c.id);
    // A node is "selected" only when it was ticked itself; unticking one of
    // its checks leaves the node-level part of the edit alone.
    rowSync.get(n.id)?.();
    afterSelection();
  }

  function toggleAllVisible() {
    const vis = visibleNodes();
    const allOn = vis.length > 0 && vis.every((n) => state.nodeIds.has(n.id));
    for (const n of vis) {
      if (allOn) { state.nodeIds.delete(n.id); for (const c of checksOf(n)) state.checkIds.delete(c.id); }
      else { state.nodeIds.add(n.id); for (const c of checksOf(n)) state.checkIds.add(c.id); }
      rowSync.get(n.id)?.();
    }
    afterSelection();
  }

  /** What every selection change has to refresh: the "select all" label, the
   *  count band and the sentence on the right. */
  function afterSelection() {
    syncHead();
    renderSummary();
  }

  function syncHead() {
    const vis = visibleNodes();
    countEl.textContent = vis.length === state.nodes.length
      ? `${state.nodes.length} node${state.nodes.length === 1 ? '' : 's'}`
      : `${vis.length} of ${state.nodes.length} nodes`;
    const allOn = vis.length > 0 && vis.every((n) => state.nodeIds.has(n.id));
    selectAll.textContent = allOn ? 'Clear selection' : 'Select all shown';
    selectAll.disabled = !vis.length;
  }

  function renderPick() {
    syncHead();
    clear(listEl);
    rowSync.clear();
    const vis = visibleNodes();
    if (!vis.length) {
      listEl.append(emptyState({ icon: 'search', title: 'No nodes match', text: 'Try a different search or clear the filters.', compact: true }));
      return;
    }
    for (const n of vis) {
      const checks = checksOf(n);
      const cb = h('input', { type: 'checkbox', 'aria-label': `Select ${n.name}`, onchange: () => setNodeSelected(n, cb.checked) });
      const row = h('div', { class: 'bulk-node' },
        h('label', { class: 'bulk-node-head' },
          cb,
          h('span', { class: 'bulk-node-name' }, n.name),
          h('span', { class: 'bulk-node-host' }, n.host || 'targets set per check'),
          statusWord(n.status || 'unknown'),
        ));
      const checkList = h('div', { class: 'bulk-checks' });
      const checkBoxes = [];
      for (const c of checks) {
        const ccb = h('input', { type: 'checkbox', 'aria-label': `Select check ${c.name}`, onchange: () => setCheckSelected(n, c, ccb.checked) });
        checkBoxes.push([c, ccb]);
        checkList.append(h('label', { class: 'bulk-check' }, ccb,
          h('span', { class: 'bulk-check-name' }, c.name),
          h('span', { class: 'type-badge' }, checkTypeLabel(c.type)),
          h('span', { class: 'bulk-check-interval' }, fmtInterval(c.intervalSeconds))));
      }
      if (!checks.length) checkList.append(h('span', { class: 'dim small' }, 'No checks'));
      row.append(checkList);
      const sync = () => {
        const nodeOn = state.nodeIds.has(n.id);
        let picked = 0;
        for (const [c, box] of checkBoxes) {
          box.checked = state.checkIds.has(c.id);
          if (box.checked) picked++;
        }
        cb.checked = nodeOn;
        cb.indeterminate = !nodeOn && picked > 0;
        row.classList.toggle('picked', nodeOn || picked > 0);
      };
      rowSync.set(n.id, sync);
      sync();
      listEl.append(row);
    }
  }

  const band = h('div', { class: 'card-head' }, h('h2', { class: 'card-title' }, 'What to change'), countEl);
  pickSide.replaceChildren(
    h('section', { class: 'filter-bar', 'aria-label': 'Filter nodes' },
      band,
      h('div', { class: 'toolbar' }, h('div', { class: 'search' }, icon('search'), searchInput), selectAll),
      filters),
    listEl,
  );

  /* ---------- Right: the change builder ---------- */

  const rowsEl = h('div', { class: 'stack-sm' });
  const typesEl = h('div', { class: 'chip-row', role: 'group', 'aria-label': 'Limit to check types' }, h('span', { class: 'chip-label' }, 'Only'));
  const summaryEl = h('p', { class: 'bulk-summary', role: 'status' });
  const errorEl = h('div', { class: 'bulk-error', role: 'alert', hidden: true });
  const applyBtn = h('button', { class: 'btn btn-primary', type: 'button', onclick: apply }, icon('save'), 'Apply changes');
  const addRowBtn = h('button', { class: 'btn btn-sm', type: 'button', onclick: () => { state.rows.push(newRow()); renderBuild(); } }, icon('plus'), 'Add another change');

  function newRow() { return { id: uid('row'), key: BULK_FIELDS[0].key, value: undefined }; }
  state.rows.push(newRow());

  /** The value a row starts with when its parameter changes. */
  function initialValue(f) {
    if (f.kind === 'chips') return [];
    if (f.value !== undefined) return f.value;
    if (f.kind === 'select') return String(optionsFor(f)[0]?.value ?? '');
    return 0;
  }

  function optionsFor(f) {
    if (f.options === 'nodes') {
      return [{ value: '', label: 'None' }, ...state.nodes.map((n) => ({ value: n.id, label: `${n.name}${n.host ? ` (${n.host})` : ''}` }))];
    }
    if (typeof f.options === 'function') return f.options();
    return f.options || [];
  }

  function rowCard(row, index) {
    const f = bulkField(row.key) || BULK_FIELDS[0];
    if (row.value === undefined) row.value = initialValue(f);

    const paramSel = h('select', { 'aria-label': 'Parameter' });
    let lastGroup = null;
    let optGroup = null;
    for (const def of BULK_FIELDS) {
      if (def.group !== lastGroup) { lastGroup = def.group; optGroup = h('optgroup', { label: def.group }); paramSel.append(optGroup); }
      optGroup.append(h('option', { value: def.key }, def.label));
    }
    paramSel.value = row.key;
    paramSel.addEventListener('change', () => { row.key = paramSel.value; row.value = undefined; renderBuild(); });

    let input;
    switch (f.kind) {
      case 'chips':
        input = chipInput({
          values: row.value || [], placeholder: f.placeholder || 'Add a value and press Enter',
          suggestions: f.suggest === 'groups' ? state.groups.groups.map((g) => g.name) : f.suggest === 'tags' ? state.groups.tags.map((t) => t.name) : [],
          validate: f.validate, onChange: (v) => { row.value = v; renderSummary(); },
        });
        break;
      case 'toggle':
        input = selectInput({ options: ON_OFF_OPTIONS, value: row.value, onchange: () => { row.value = input.value; renderSummary(); } });
        break;
      case 'tri-state':
        input = selectInput({ options: TRI, value: row.value, onchange: () => { row.value = input.value; renderSummary(); } });
        break;
      case 'select': {
        const opts = optionsFor(f);
        if (f.allowCustom) {
          // The interval offers the usual choices and a way past them, the
          // same way the node editor does.
          const custom = !opts.some((o) => String(o.value) === String(row.value));
          const customIn = numberInput({ value: row.value, min: f.min || 5, hidden: !custom, 'aria-label': 'Custom interval in seconds', oninput: () => { row.value = Number(customIn.value); renderSummary(); } });
          const sel = selectInput({ options: [...opts, { value: 'custom', label: 'Custom…' }], value: custom ? 'custom' : row.value, onchange: () => {
            if (sel.value === 'custom') { customIn.hidden = false; customIn.focus(); }
            else { customIn.hidden = true; row.value = Number(sel.value); renderSummary(); }
          } });
          input = h('div', { class: 'stack-sm', style: { gap: '6px' } }, sel, customIn);
        } else {
          input = selectInput({ options: opts, value: row.value, onchange: () => { row.value = input.value; renderSummary(); } });
        }
        break;
      }
      case 'none':
        input = h('p', { class: 'dim small', style: { margin: '6px 0 0' } }, 'No value needed.');
        break;
      case 'metric-threshold': {
        // Which metric, then its warning and critical levels. The key is
        // free text so an instance ("disk:/srv") can be named as well as a
        // family; the datalist offers the families.
        const v = row.value = { ...(row.value || {}) };
        const keyIn = textInput({ value: v.metric || '', list: 'bulk-metric-keys', placeholder: 'e.g. disk or disk:/srv', 'aria-label': 'Metric', oninput: () => { v.metric = keyIn.value; renderSummary(); } });
        const datalist = h('datalist', { id: 'bulk-metric-keys' }, ...['cpu', 'memory', 'swap', 'load', 'disk', 'inodes', 'net', 'diskio'].map((k) => h('option', { value: k })));
        const warnIn = numberInput({ value: v.warn ?? '', min: 0, step: 'any', placeholder: 'off', 'aria-label': 'Warning level', oninput: () => { v.warn = warnIn.value === '' ? '' : Number(warnIn.value); renderSummary(); } });
        const critIn = numberInput({ value: v.crit ?? '', min: 0, step: 'any', placeholder: 'off', 'aria-label': 'Critical level', oninput: () => { v.crit = critIn.value === '' ? '' : Number(critIn.value); renderSummary(); } });
        input = h('div', { class: 'row', style: { gap: '6px', flexWrap: 'wrap' } }, keyIn, datalist,
          h('span', { class: 'dim small' }, 'warn'), warnIn, h('span', { class: 'dim small' }, 'crit'), critIn);
        break;
      }
      default: {
        const num = numberInput({ value: row.value, min: f.min ?? 0, max: f.max, oninput: () => { row.value = Number(num.value) || 0; renderSummary(); } });
        input = f.unit ? h('div', { class: 'input-with-unit' }, num, h('span', { class: 'unit' }, f.unit)) : num;
      }
    }

    return h('div', { class: 'bulk-row' },
      field({ label: index === 0 ? 'Parameter' : 'And also', input: paramSel }),
      field({ label: 'Value', input, help: f.help }),
      // The only row is the change itself; there is nothing to take away from
      // it, so the button is absent rather than present and unusable.
      state.rows.length > 1
        ? h('button', {
          class: 'btn btn-sm icon-btn btn-danger', type: 'button', 'aria-label': 'Remove this change', title: 'Remove',
          onclick: () => { state.rows.splice(index, 1); renderBuild(); },
        }, icon('trash'))
        : null);
  }

  function renderTypes() {
    [...typesEl.querySelectorAll('.chip')].forEach((c) => c.remove());
    const present = [...new Set(state.nodes.flatMap((n) => checksOf(n).map((c) => c.type)))].sort();
    typesEl.append(h('button', { type: 'button', class: `chip ${state.types.size ? '' : 'active'}`, 'aria-pressed': state.types.size ? 'false' : 'true', onclick: () => { state.types.clear(); renderTypes(); renderSummary(); } }, 'Every type'));
    for (const t of present) {
      const on = state.types.has(t);
      typesEl.append(h('button', { type: 'button', class: `chip ${on ? 'active' : ''}`, 'aria-pressed': on ? 'true' : 'false', onclick: () => { if (on) state.types.delete(t); else state.types.add(t); renderTypes(); renderSummary(); } }, checkTypeLabel(t)));
    }
  }

  function renderBuild() {
    clear(rowsEl);
    state.rows.forEach((row, i) => rowsEl.append(rowCard(row, i)));
    renderTypes();
    renderSummary();
  }

  /* ---------- What the request will say ---------- */

  /** The checks the patch would reach: every check of a selected node plus
   *  every separately selected check, narrowed by the type chips. */
  function affectedChecks() {
    const out = [];
    for (const n of state.nodes) {
      for (const c of checksOf(n)) {
        if (!state.checkIds.has(c.id)) continue;
        if (state.types.size && !state.types.has(c.type)) continue;
        out.push(c);
      }
    }
    return out;
  }

  const nodeName = (id) => state.nodes.find((n) => String(n.id) === String(id))?.name;

  /** Build the PATCH body from the selection and the rows. */
  function buildBody() {
    const body = { nodeIds: [...state.nodeIds] };
    const checks = affectedChecks();
    // Checks of a selected node come with the node, so only the ones that are
    // not covered that way need naming — and with a type filter on, even those
    // have to be named, because the server narrows the whole selection alike.
    const coveredByNode = new Set();
    for (const n of state.nodes) if (state.nodeIds.has(n.id)) for (const c of checksOf(n)) coveredByNode.add(c.id);
    const extra = checks.filter((c) => !coveredByNode.has(c.id)).map((c) => c.id);
    if (extra.length) body.checkIds = extra;
    if (state.types.size) body.checkFilter = { types: [...state.types] };
    for (const row of state.rows) {
      const f = bulkField(row.key);
      if (!f) continue;
      const scope = f.scope === 'node' ? 'node' : 'check';
      body[scope] = body[scope] || {};
      setPath(body[scope], f.path, bulkValue(f, row.value));
    }
    return body;
  }

  function renderSummary() {
    const nodeCount = state.nodeIds.size;
    const checkCount = affectedChecks().length;
    const nodeRows = state.rows.filter((r) => bulkField(r.key)?.scope === 'node');
    const checkRows = state.rows.filter((r) => bulkField(r.key)?.scope === 'check');
    const parts = [];
    const word = (n, one, many) => `${n} ${n === 1 ? one : many}`;
    for (const r of checkRows) {
      const f = bulkField(r.key);
      parts.push(`${cap(f.summary(r.value, { nodeName }))} on ${word(checkCount, 'check', 'checks')}`);
    }
    for (const r of nodeRows) {
      const f = bulkField(r.key);
      parts.push(`${cap(f.summary(r.value, { nodeName }))} on ${word(nodeCount, 'node', 'nodes')}`);
    }
    const nothing = (!checkRows.length || !checkCount) && (!nodeRows.length || !nodeCount);
    summaryEl.textContent = nothing
      ? 'Pick some nodes or checks on the left, then choose what to change.'
      : parts.join('; ') + '.';
    summaryEl.classList.toggle('dim', nothing);
    applyBtn.disabled = nothing || state.applying;
  }

  const cap = (s) => (s ? s[0].toUpperCase() + s.slice(1) : s);

  function showError(msg) {
    errorEl.hidden = !msg;
    errorEl.textContent = msg || '';
  }

  async function apply() {
    showError('');
    const body = buildBody();
    const ok = await confirmDialog({
      title: 'Apply to everything selected?',
      message: `${summaryEl.textContent} This cannot be undone, though every change is recorded in the timeline.`,
      confirmLabel: 'Apply changes',
    });
    if (!ok) return;
    state.applying = true;
    const done = busy(applyBtn, 'Applying…');
    try {
      const res = await api.patch('/api/nodes/bulk', body);
      const bits = [];
      if (res?.nodes) bits.push(`${res.nodes} node${res.nodes === 1 ? '' : 's'}`);
      if (res?.checks) bits.push(`${res.checks} check${res.checks === 1 ? '' : 's'}`);
      toast(`Updated ${bits.join(' and ') || 'nothing'}`, { kind: 'success', action: { label: 'Back to nodes', onClick: () => ctx.navigate('/nodes') } });
      // The list on the left is now out of date in exactly the way the edit
      // just changed, so it is read back rather than patched from memory.
      state.nodes = await api.get('/api/nodes').catch(() => state.nodes);
      state.groups = await api.get('/api/groups').catch(() => state.groups);
      renderFilters(); renderPick(); renderBuild();
    } catch (e) {
      showError(e.message);
    }
    state.applying = false;
    done();
    renderSummary();
  }

  buildSide.replaceChildren(
    h('section', { class: 'card' },
      h('div', { class: 'card-head' }, h('h2', { class: 'card-title' }, 'The change'), addRowBtn),
      rowsEl,
      h('div', { class: 'bulk-types' }, typesEl, h('div', { class: 'help' }, 'Narrows which of the selected checks a check setting reaches. Node settings are not affected.')),
    ),
    h('section', { class: 'card bulk-apply' },
      summaryEl,
      errorEl,
      h('div', { class: 'btn-group' }, applyBtn, h('a', { class: 'btn', href: '#/nodes' }, 'Cancel')),
    ),
  );

  renderFilters();
  renderPick();
  renderBuild();

  return { destroy() { state.destroyed = true; } };
}

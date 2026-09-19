// Wallboards: making them, arranging them, and giving one an address.
//
// A wallboard is configured here and looked at somewhere else — that is the
// whole shape of this page. It is an ordinary application page, in the
// application's own skin; the board itself has its own identity and is never
// previewed inside a card here, because a thing meant to be read across a room
// tells you nothing at thumbnail size. "Show it" opens it full screen instead.

import { api } from '../api.js';
import {
  h, icon, clear, replace, toast, confirmDialog, promptDialog, openModal, emptyState, skeleton,
  field, textInput, numberInput, selectInput, checkbox, toggle, menuButton, uid,
} from '../components.js';
import { PANEL_TYPES, panelMeta, WALL_THEMES } from '../wall-render.js';

export async function mount(root, ctx) {
  const state = { boards: [], nodes: [], groups: { groups: [], tags: [] }, id: Number(ctx.params.id) || null, destroyed: false, dirty: false };

  const tabsEl = h('div', { class: 'chip-row', role: 'tablist', 'aria-label': 'Wallboards' });
  const bodyEl = h('div', { class: 'stack' }, skeleton({ lines: 4 }));
  root.append(tabsEl, bodyEl);

  function current() { return state.boards.find((b) => b.id === state.id) || state.boards[0] || null; }

  async function load() {
    const [boards, nodes, groups] = await Promise.all([
      api.get('/api/wallboards'),
      api.get('/api/nodes').catch(() => []),
      api.get('/api/groups').catch(() => ({ groups: [], tags: [] })),
    ]);
    if (state.destroyed) return;
    state.boards = boards || [];
    state.nodes = nodes || [];
    state.groups = groups || { groups: [], tags: [] };
    if (!state.boards.some((b) => b.id === state.id)) state.id = state.boards[0]?.id ?? null;
    render();
  }

  /* ---------- the page ---------- */

  function render() {
    const board = current();
    ctx.setTitle('Wallboards', {
      actions: [
        board ? h('a', { class: 'btn', href: `#/wallboard/${board.id}` }, icon('monitor'), 'Show it') : null,
        h('button', { class: 'btn btn-primary admin-only', type: 'button', onclick: newBoard }, icon('plus'), 'New wallboard'),
      ].filter(Boolean),
    });

    clear(tabsEl);
    for (const b of state.boards) {
      tabsEl.append(h('button', {
        type: 'button', role: 'tab',
        class: `chip ${b.id === state.id ? 'active' : ''}`,
        'aria-selected': b.id === state.id ? 'true' : 'false',
        onclick: () => { state.id = b.id; render(); },
      }, b.share?.enabled ? icon('link') : null, b.name));
    }

    clear(bodyEl);
    if (!board) {
      bodyEl.append(h('div', { class: 'card' }, emptyState({
        icon: 'monitor',
        title: 'No wallboards yet',
        text: 'A wallboard is what a spare screen shows: the state of the network, in type you can read from the other side of the room. Make one and it can be projected to any browser on your network.',
        actions: h('button', { class: 'btn btn-primary admin-only', type: 'button', onclick: newBoard }, icon('plus'), 'Make a wallboard'),
      })));
      return;
    }
    bodyEl.append(panelsCard(board), layoutCard(board), shareCard(board));
  }

  /* ---------- panels ---------- */

  function panelsCard(board) {
    const card = h('section', { class: 'card', 'aria-label': 'Panels' });
    card.append(h('div', { class: 'card-head' },
      h('h2', { class: 'card-title' }, 'Panels'),
      h('div', { class: 'card-actions' },
        h('button', { class: 'btn btn-sm admin-only', type: 'button', onclick: () => editPanel(board, null) }, icon('plus'), 'Add panel'),
        menuButton([
          { label: 'Rename wallboard', icon: 'edit', onClick: () => rename(board), adminOnly: true },
          { label: 'Duplicate', icon: 'copy', onClick: () => duplicate(board), adminOnly: true },
          { sep: true, adminOnly: true },
          { label: 'Delete wallboard', icon: 'trash', danger: true, onClick: () => remove(board), adminOnly: true },
        ], { label: 'More actions', small: true }))));

    if (!board.panels?.length) {
      card.append(emptyState({ icon: 'grid', title: 'Nothing on this board', text: 'Add a headline, the counts and whatever else the room needs to see.', compact: true }));
      return card;
    }
    const list = h('div', { class: 'wb-panels' });
    board.panels.forEach((p, i) => list.append(panelRow(board, p, i)));
    card.append(list);
    card.append(h('p', { class: 'note' }, 'Panels are laid out left to right in this order, wrapping onto the next row when one is full. The board is ',
      h('b', null, `${board.layout?.columns || 12} columns`), ' wide.'));
    return card;
  }

  function panelRow(board, p, i) {
    const meta = panelMeta(p.type);
    const move = (delta) => {
      const to = i + delta;
      if (to < 0 || to >= board.panels.length) return;
      const [item] = board.panels.splice(i, 1);
      board.panels.splice(to, 0, item);
      save(board);
    };
    return h('div', { class: 'wb-panel-row' },
      h('div', { class: 'wb-panel-main' },
        h('div', { class: 'wb-panel-name' }, h('b', null, p.title || meta.label), h('span', { class: 'tag' }, meta.label)),
        h('div', { class: 'note' }, meta.desc)),
      h('div', { class: 'wb-panel-size mono' }, `${p.width}×${p.height}`),
      h('div', { class: 'wb-panel-actions admin-only' },
        h('button', { class: 'icon-btn', type: 'button', title: 'Move up', 'aria-label': `Move ${p.title || meta.label} up`, disabled: i === 0, onclick: () => move(-1) }, icon('arrowUp')),
        h('button', { class: 'icon-btn', type: 'button', title: 'Move down', 'aria-label': `Move ${p.title || meta.label} down`, disabled: i === board.panels.length - 1, onclick: () => move(1) }, icon('arrowDown')),
        h('button', { class: 'btn btn-sm', type: 'button', onclick: () => editPanel(board, p) }, 'Edit'),
        h('button', { class: 'icon-btn danger', type: 'button', title: 'Remove', 'aria-label': `Remove ${p.title || meta.label}`, onclick: async () => {
          const ok = await confirmDialog({ title: 'Remove this panel?', message: `"${p.title || meta.label}" comes off the board. The board itself is kept.`, confirmLabel: 'Remove', danger: true });
          if (!ok) return;
          board.panels = board.panels.filter((x) => x.id !== p.id);
          save(board);
        } }, icon('trash'))));
  }

  async function editPanel(board, existing) {
    const result = await panelEditor(existing, { nodes: state.nodes, groups: state.groups, columns: board.layout?.columns || 12 });
    if (!result) return;
    if (existing) board.panels = board.panels.map((p) => (p.id === existing.id ? result : p));
    else board.panels = [...(board.panels || []), result];
    save(board);
  }

  /* ---------- layout ---------- */

  function layoutCard(board) {
    const l = { ...board.layout };
    const columns = numberInput({ value: l.columns ?? 12, min: 4, max: 24, step: 1 });
    const theme = selectInput({ options: WALL_THEMES.map((t) => ({ value: t.value, label: t.label })), value: l.theme || 'signal' });
    const themeNote = h('p', { class: 'note' }, WALL_THEMES.find((t) => t.value === (l.theme || 'signal'))?.desc || '');
    theme.addEventListener('change', () => { themeNote.textContent = WALL_THEMES.find((t) => t.value === theme.value)?.desc || ''; });
    const scale = selectInput({
      options: [[0.8, 'Smaller — a monitor at a desk'], [1, 'As designed'], [1.25, 'Larger — a screen across a room'], [1.6, 'Largest — a screen down a corridor']]
        .map(([v, label]) => ({ value: String(v), label })),
      value: String(l.scale || 1),
    });
    const refresh = numberInput({ value: l.refreshSeconds ?? 20, min: 5, max: 3600, step: 5 });
    const chrome = toggle({ checked: !l.hideChrome, ariaLabel: 'Show the footer strip' });

    const card = h('section', { class: 'card', 'aria-label': 'Layout' },
      h('div', { class: 'card-head' }, h('h2', { class: 'card-title' }, 'How it is drawn')),
      h('div', { class: 'form-grid' },
        field({ label: 'Columns', input: columns, help: 'A panel’s width is counted in these. Fewer columns on a portrait screen.' }),
        field({ label: 'Refresh every', input: refresh, help: 'Seconds between reloads. The board reloads itself; nobody has to.' }),
        field({ label: 'Theme', input: theme, help: themeNote }),
        field({ label: 'Type size', input: scale, help: 'For a screen further away than the one you laid it out on.' }),
        field({ label: 'Footer strip', input: chrome, help: 'The service-health line along the bottom.' })),
      h('div', { class: 'card-actions admin-only', style: { marginTop: '12px' } },
        h('button', { class: 'btn btn-primary', type: 'button', onclick: () => {
          board.layout = {
            columns: Number(columns.value) || 12,
            theme: theme.value,
            scale: Number(scale.value) || 1,
            refreshSeconds: Number(refresh.value) || 20,
            hideChrome: !chrome.input.checked,
          };
          save(board);
        } }, 'Save layout')));
    return card;
  }

  /* ---------- projecting it ---------- */

  function shareCard(board) {
    const share = board.share || {};
    const card = h('section', { class: 'card', 'aria-label': 'Projection' },
      h('div', { class: 'card-head' }, h('h2', { class: 'card-title' }, icon('monitor'), 'Put it on a screen')));

    card.append(h('p', { class: 'lead' },
      'A projected wallboard can be opened by any browser on your network with nothing but its address — a television, a tablet on a shelf, an old laptop in the corner. Nothing on that screen can sign in, navigate, or see anything but this one board.'));

    if (!share.enabled) {
      card.append(
        h('p', { class: 'note' }, 'Projection is off, so this board is only visible to people who have signed in to GWatch.'),
        h('div', { class: 'card-actions admin-only' },
          h('button', { class: 'btn btn-primary', type: 'button', onclick: () => setShare(board, { enabled: true }) }, icon('link'), 'Project this wallboard')));
      return card;
    }

    if (share.redacted || !share.token) {
      // A viewer is told the board is shared but never how to reach it.
      card.append(h('p', { class: 'note' }, 'This board is projected. Only an administrator can see or change its address.'));
      return card;
    }

    const url = `${location.protocol}//${location.host}/wall?id=${board.id}&token=${encodeURIComponent(share.token)}`;
    const urlEl = h('code', { class: 'agent-setup', style: { userSelect: 'all', wordBreak: 'break-all' } }, url);
    card.append(
      urlEl,
      h('p', { class: 'note' }, 'Type this into the browser on the screen you want it on. Anyone who has it can read this board without signing in, so treat it like a key to this one board: it is worth nothing else, and changing the address below takes it back.'),
      h('div', { class: 'card-actions admin-only' },
        h('button', { class: 'btn btn-primary', type: 'button', onclick: async () => {
          try { await navigator.clipboard.writeText(url); toast('Address copied', { kind: 'success' }); }
          catch { toast('Could not copy — select the address and copy it by hand.', { kind: 'error' }); }
        } }, icon('copy'), 'Copy address'),
        h('a', { class: 'btn', href: url, target: '_blank', rel: 'noopener' }, icon('external'), 'Open it'),
        h('button', { class: 'btn', type: 'button', onclick: async () => {
          const ok = await confirmDialog({
            title: 'Change the address?',
            message: 'The current address stops working at once. Every screen showing this board needs the new one typed in.',
            confirmLabel: 'Change it', danger: true,
          });
          if (ok) setShare(board, { enabled: true, rotate: true });
        } }, 'Change address'),
        h('button', { class: 'btn btn-danger', type: 'button', onclick: async () => {
          const ok = await confirmDialog({
            title: 'Stop projecting this board?',
            message: 'The address stops working and screens showing it stop updating. The board itself is kept.',
            confirmLabel: 'Stop projecting', danger: true,
          });
          if (ok) setShare(board, { enabled: false });
        } }, 'Stop projecting')));
    return card;
  }

  async function setShare(board, body) {
    try {
      const saved = await api.post(`/api/wallboards/${board.id}/share`, body);
      state.boards = state.boards.map((b) => (b.id === saved.id ? saved : b));
      render();
      toast(body.enabled ? 'This wallboard can now be opened from your network' : 'Projection switched off', { kind: 'success' });
    } catch (e) { toast(e.message, { kind: 'error' }); }
  }

  /* ---------- saving ---------- */

  async function save(board) {
    try {
      const saved = await api.put(`/api/wallboards/${board.id}`, {
        name: board.name, sortOrder: board.sortOrder, layout: board.layout, panels: board.panels,
      });
      state.boards = state.boards.map((b) => (b.id === saved.id ? saved : b));
      state.id = saved.id;
      render();
    } catch (e) { toast(e.message, { kind: 'error' }); await load(); }
  }

  async function newBoard() {
    const name = await promptDialog({ title: 'New wallboard', label: 'Name', placeholder: 'e.g. Office screen', confirmLabel: 'Create' });
    if (!name) return;
    try {
      // Sent with no panels: the service fills in the default arrangement, so
      // a new board is something worth looking at rather than a blank screen.
      const created = await api.post('/api/wallboards', { name: name.trim() });
      state.id = created.id;
      await load();
      toast('Wallboard created', { kind: 'success' });
    } catch (e) { toast(e.message, { kind: 'error' }); }
  }

  async function rename(board) {
    const name = await promptDialog({ title: 'Rename wallboard', label: 'Name', value: board.name, confirmLabel: 'Save' });
    if (!name) return;
    board.name = name.trim();
    save(board);
  }

  async function duplicate(board) {
    try {
      const copy = await api.post('/api/wallboards', {
        name: `${board.name} copy`,
        layout: board.layout,
        // Fresh ids: two boards must never share a panel id.
        panels: (board.panels || []).map((p) => ({ ...p, id: uid('p') })),
      });
      state.id = copy.id;
      await load();
    } catch (e) { toast(e.message, { kind: 'error' }); }
  }

  async function remove(board) {
    const ok = await confirmDialog({
      title: `Delete ${board.name}?`,
      message: board.share?.enabled
        ? 'Its address stops working and any screen showing it goes blank. This cannot be undone.'
        : 'This cannot be undone.',
      confirmLabel: 'Delete', danger: true,
    });
    if (!ok) return;
    try {
      await api.del(`/api/wallboards/${board.id}`);
      state.id = null;
      await load();
      toast('Wallboard deleted', { kind: 'success' });
    } catch (e) { toast(e.message, { kind: 'error' }); }
  }

  try { await load(); } catch (e) {
    replace(bodyEl, h('div', { class: 'card' }, emptyState({ icon: 'alert', title: 'Could not load wallboards', text: e.message })));
  }

  return {
    refresh: () => load().catch(() => {}),
    destroy() { state.destroyed = true; },
    async update(params) {
      const id = Number(params.id) || null;
      if (id && id !== state.id) { state.id = id; render(); }
      return true;
    },
  };
}

/* ---------------- the panel editor ---------------- */

function panelEditor(existing, { nodes, groups, columns }) {
  return new Promise((resolve) => {
    const first = PANEL_TYPES[0];
    const p = existing
      ? { ...existing, config: parseConfig(existing.config) }
      : { id: uid('p'), type: first.type, title: '', width: first.w, height: first.h, config: { ...first.config } };
    let result = null;

    const picker = h('div', { class: 'widget-picker', role: 'radiogroup', 'aria-label': 'Panel type' });
    const titleInput = textInput({ value: p.title || '', placeholder: panelMeta(p.type).label });
    const widthSel = selectInput({
      options: Array.from({ length: columns }, (_, i) => ({ value: i + 1, label: `${i + 1} of ${columns}` })),
      value: Math.min(p.width || 4, columns),
    });
    const heightSel = selectInput({ options: [1, 2, 3, 4].map((n) => ({ value: n, label: `${n} row${n > 1 ? 's' : ''}` })), value: p.height || 1 });
    const cfgArea = h('div', { class: 'stack-sm' });
    let reads = {};

    function renderPicker() {
      clear(picker);
      for (const t of PANEL_TYPES) {
        picker.append(h('button', {
          type: 'button', role: 'radio',
          class: `wp ${t.type === p.type ? 'active' : ''}`,
          'aria-checked': t.type === p.type ? 'true' : 'false',
          onclick: () => {
            if (p.type === t.type) return;
            p.type = t.type;
            p.config = { ...t.config };
            if (!existing) { widthSel.value = String(Math.min(t.w, columns)); heightSel.value = String(t.h); }
            titleInput.placeholder = t.label;
            renderPicker(); renderCfg();
          },
        }, h('b', null, t.label), h('span', null, t.desc)));
      }
    }

    function renderCfg() {
      clear(cfgArea);
      reads = {};
      const cfg = p.config;
      switch (p.type) {
        case 'attention': {
          const n = numberInput({ value: cfg.limit || 6, min: 1, max: 20 });
          reads.limit = () => Number(n.value) || 6;
          cfgArea.append(field({ label: 'How many to list', input: n, help: 'Anything beyond this is counted rather than listed.' }));
          break;
        }
        case 'nodes': {
          const g = selectInput({ options: [{ value: '', label: 'All groups' }, ...(groups.groups || []).map((x) => ({ value: x.name, label: x.name }))], value: cfg.group || '' });
          const t = selectInput({ options: [{ value: '', label: 'Any tag' }, ...(groups.tags || []).map((x) => ({ value: x.name, label: x.name }))], value: cfg.tag || '' });
          const n = numberInput({ value: cfg.limit || 24, min: 1, max: 200 });
          reads.group = () => g.value; reads.tag = () => t.value; reads.limit = () => Number(n.value) || 24;
          cfgArea.append(h('div', { class: 'form-grid-3' }, field({ label: 'Group', input: g }), field({ label: 'Tag', input: t }), field({ label: 'Most to show', input: n })));
          cfgArea.append(h('p', { class: 'note' }, 'Whatever is wrong sorts to the front, so a full board still shows the problem first.'));
          break;
        }
        case 'trends': {
          const r = selectInput({ options: ['1h', '24h', '7d', '30d'].map((x) => ({ value: x, label: x })), value: cfg.range || '24h' });
          const n = numberInput({ value: cfg.limit || 4, min: 1, max: 8 });
          const sel = new Set((cfg.checkIds || []).map(Number));
          const list = h('div', { class: 'check-list' });
          for (const node of nodes) {
            for (const c of node.checks || []) {
              if (c.type === 'system') continue; // a machine's readings are not a trend line
              list.append(checkbox({
                label: `${node.name} › ${c.name}`,
                checked: sel.has(Number(c.id)),
                onChange: (v) => { if (v) sel.add(Number(c.id)); else sel.delete(Number(c.id)); },
              }));
            }
          }
          reads.range = () => r.value; reads.limit = () => Number(n.value) || 4; reads.checkIds = () => [...sel];
          cfgArea.append(
            h('div', { class: 'form-grid' }, field({ label: 'Time range', input: r }), field({ label: 'How many charts', input: n })),
            field({ label: 'Checks', input: list, help: 'Leave all unticked and GWatch picks the checks that matter most.' }));
          break;
        }
        case 'clock': {
          const c = checkbox({ label: 'Show seconds', checked: !!cfg.seconds });
          reads.seconds = () => c.input.checked;
          cfgArea.append(field({ label: 'Clock', input: c }));
          break;
        }
        case 'message': {
          const t = textInput({ value: cfg.text || '', placeholder: 'e.g. Network maintenance Saturday 08:00' });
          reads.text = () => t.value;
          cfgArea.append(field({ label: 'Text', input: t, help: 'Shown as written, in the board’s largest supporting type.' }));
          break;
        }
        default:
          cfgArea.append(h('p', { class: 'note' }, 'This panel has no options — it shows what it shows.'));
      }
    }

    renderPicker(); renderCfg();

    function submit() {
      const cfg = {};
      for (const [k, read] of Object.entries(reads)) cfg[k] = read();
      result = {
        id: p.id, type: p.type, title: titleInput.value.trim(),
        width: Number(widthSel.value), height: Number(heightSel.value), config: cfg,
      };
      m.close();
    }

    const form = h('form', { class: 'stack', onsubmit: (e) => { e.preventDefault(); submit(); } },
      field({ label: 'Panel', input: picker }),
      h('div', { class: 'form-grid-3' },
        field({ label: 'Title', input: titleInput, help: 'Leave blank for the default.' }),
        field({ label: 'Width', input: widthSel }),
        field({ label: 'Height', input: heightSel })),
      h('div', null, h('div', { class: 'section-title' }, 'Options'), cfgArea));

    const m = openModal({
      title: existing ? 'Edit panel' : 'Add panel', wide: true, body: form,
      footer: [
        h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, 'Cancel'),
        h('button', { class: 'btn btn-primary', type: 'button', onclick: submit }, existing ? 'Save panel' : 'Add panel'),
      ],
      onClose: () => resolve(result),
    });
  });
}

function parseConfig(config) {
  if (!config) return {};
  if (typeof config === 'string') { try { return JSON.parse(config); } catch { return {}; } }
  return { ...config };
}

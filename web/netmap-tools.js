// The network map's working parts that the map page and its presentation
// share: the hand arrangement (kept on the service, or only in this browser
// for someone who may not save it), the what-if simulation, finding a node by
// name, and saving the picture.

import { api } from './api.js';
import { h, icon, toast } from './components.js';
import { plural, compareNames } from './fmt.js';
import { descendantsOf } from './netmap.js';

/* ---------- The arrangement ---------- */

const LOCK_KEY = 'gw.netmap.locked';
/** Whether the layout is locked in this browser, so nothing is moved by accident. */
export function layoutLocked() { try { return localStorage.getItem(LOCK_KEY) === '1'; } catch { return false; } }
export function setLayoutLocked(on) { try { localStorage.setItem(LOCK_KEY, on ? '1' : '0'); } catch { /* private window */ } }

/**
 * The map's hand arrangement. `load()` fetches the shared one; `change(a)`
 * takes a new one and, for an administrator (`canSave()`), saves it a moment
 * later — a run of quick changes is one request. Anyone else keeps theirs in
 * this tab only, and is told so once.
 */
export function arrangementStore({ canSave }) {
  let current = { positions: {}, collapsed: [] };
  let timer = 0;
  let saving = Promise.resolve();
  let toldUnsaved = false;
  const store = {
    get value() { return current; },
    async load() {
      try { current = normalise(await api.get('/api/map/layout')); } catch { current = { positions: {}, collapsed: [] }; }
      return current;
    },
    change(next, { quiet = false } = {}) {
      current = normalise(next);
      if (!canSave()) {
        if (!toldUnsaved && !quiet) { toldUnsaved = true; toast('Arranged in this browser only: an administrator’s arrangement is the one everyone sees.'); }
        return Promise.resolve(current);
      }
      clearTimeout(timer);
      return new Promise((resolve) => {
        timer = setTimeout(() => {
          const body = current;
          saving = saving.then(() => api.put('/api/map/layout', body)).then(() => resolve(current), (err) => { toast(`The arrangement was not saved: ${err.message}`, { kind: 'error' }); resolve(current); });
        }, 400);
      });
    },
  };
  return store;
}

function normalise(a) {
  const src = a && typeof a === 'object' ? a : {};
  return { positions: { ...(src.positions || {}) }, collapsed: [...(src.collapsed || [])] };
}

/* ---------- What-if ---------- */

/**
 * A simulation of nodes failing or going into maintenance, and what that
 * cuts off below them. It spreads one ring of links at a time, so it can be
 * watched (Play) or talked through (Step). Nothing real changes: the map is
 * only drawn as if it had. `onChange(sim | null)` is given what to draw;
 * `names(id)` is a node's name. `.el` is the banner that goes over the map.
 */
export function createSimulation({ onChange, names, forest }) {
  const failed = new Map();
  let depth = 0;
  let timer = 0;
  const el = h('div', { class: 'netmap-sim', role: 'region', 'aria-label': 'Simulation' });
  el.hidden = true;
  const live = h('div', { class: 'sr-only', 'aria-live': 'polite' });

  const maxDepth = () => {
    const f = forest();
    let deepest = 0;
    for (const id of failed.keys()) {
      const walk = (x, d) => { deepest = Math.max(deepest, d); for (const k of f.children.get(x) || []) walk(k, d + 1); };
      walk(String(id), 0);
    }
    return deepest;
  };
  const state = () => (failed.size ? { failed: new Map(failed), depth } : null);
  const stop = () => { clearInterval(timer); timer = 0; };
  const emit = () => { onChange(state()); render(); };

  /** What the simulation reaches, all of it (however far it has spread yet). */
  function reach() {
    const f = forest();
    const down = new Set(), quiet = new Set();
    for (const [id, kind] of failed) for (const d of descendantsOf(f, id)) (kind === 'down' ? down : quiet).add(d);
    for (const id of failed.keys()) { down.delete(id); quiet.delete(id); }
    for (const id of down) quiet.delete(id);
    return { down, quiet };
  }

  function render() {
    el.hidden = !failed.size;
    if (!failed.size) { el.replaceChildren(); return; }
    const { down, quiet } = reach();
    const full = depth >= maxDepth();
    const chips = [...failed].sort(([a], [b]) => compareNames(names(a), names(b))).map(([id, kind]) => h('span', { class: `netmap-sim-chip netmap-sim-${kind}` },
      h('span', null, `${names(id)} ${kind === 'down' ? 'fails' : 'in maintenance'}`),
      h('button', { type: 'button', class: 'icon-btn btn-sm', 'aria-label': `Take ${names(id)} out of the simulation`, onclick: () => api.remove(id) }, icon('x'))));
    const parts = [];
    if (down.size) parts.push(`${plural(down.size, 'node')} cut off — their alerts would be held back`);
    if (quiet.size) parts.push(`${plural(quiet.size, 'node')} quiet for maintenance`);
    if (!parts.length) parts.push('Nothing depends on it');
    el.replaceChildren(
      h('div', { class: 'netmap-sim-head' }, icon('zap'), h('strong', null, 'Simulation'), h('span', { class: 'muted' }, 'Nothing real has changed.')),
      h('div', { class: 'netmap-sim-chips' }, chips),
      h('div', { class: 'netmap-sim-sum' }, parts.join(' · '), full ? null : h('span', { class: 'muted' }, ` (spreading: ${depth} of ${maxDepth()} links)`)),
      h('div', { class: 'row u-gap-6' },
        h('button', { type: 'button', class: 'btn btn-sm', disabled: full, onclick: () => { stop(); api.step(); } }, 'Step'),
        h('button', { type: 'button', class: 'btn btn-sm', onclick: () => api.play() }, icon('play'), 'Play'),
        h('button', { type: 'button', class: 'btn btn-sm', onclick: () => api.clear() }, icon('x'), 'End the simulation')),
      live);
  }

  const api = {
    el,
    get active() { return failed.size > 0; },
    get state() { return state(); },
    kindOf(id) { return failed.get(String(id)) || null; },
    /** `id` fails ('down') or goes into maintenance, and the effect spreads from it. */
    add(id, kind = 'down') {
      failed.set(String(id), kind);
      live.textContent = `${names(id)} ${kind === 'down' ? 'fails' : 'goes into maintenance'} in the simulation.`;
      api.play();
    },
    remove(id) {
      failed.delete(String(id));
      if (!failed.size) { api.clear(); return; }
      depth = Math.min(depth, maxDepth());
      emit();
    },
    step() { if (depth < maxDepth()) depth++; emit(); },
    /** Starts again from the failed nodes alone and spreads a ring at a time. */
    play() {
      stop();
      depth = 0;
      emit();
      const reduce = typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches;
      if (reduce) { depth = maxDepth(); emit(); return; }
      timer = setInterval(() => { if (depth >= maxDepth()) { stop(); render(); return; } depth++; emit(); }, 550);
    },
    clear() { stop(); failed.clear(); depth = 0; emit(); },
    destroy() { stop(); },
    /** The counts in words, for whoever reads the page. */
    summary() { const r = reach(); return { down: r.down.size, quiet: r.quiet.size }; },
  };
  return api;
}

/* ---------- Finding a node ---------- */

/** A search box: type a name or address, choose, and `onPick(id)` is called. */
export function nodeFinder(entries, onPick) {
  const listId = `netmap-find-${Math.random().toString(36).slice(2, 8)}`;
  const list = h('datalist', { id: listId });
  const input = h('input', { type: 'search', class: 'netmap-find-input', placeholder: 'Find a node…', 'aria-label': 'Find a node on the map', list: listId, autocomplete: 'off' });
  const fill = () => {
    list.replaceChildren(...entries().map((e) => e.node).sort((a, b) => compareNames(a.name, b.name)).slice(0, 400)
      .map((n) => h('option', { value: n.name }, n.host || '')));
  };
  // Enter picks, and so does choosing from the list (a change); leaving the
  // box afterwards is a change too, which must not pick the same node again
  // and redraw whatever was clicked to leave it.
  let picked = '';
  const pick = () => {
    const q = input.value.trim().toLowerCase();
    if (!q || q === picked) return;
    picked = q;
    const all = entries().map((e) => e.node);
    const n = all.find((x) => x.name.toLowerCase() === q) || all.find((x) => (x.host || '').toLowerCase() === q)
      || all.find((x) => x.name.toLowerCase().includes(q)) || all.find((x) => (x.host || '').toLowerCase().includes(q));
    if (n) onPick(String(n.id)); else toast(`No node called “${input.value.trim()}”`);
  };
  input.addEventListener('focus', fill);
  input.addEventListener('change', pick);
  input.addEventListener('input', () => { picked = ''; });
  input.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); picked = ''; pick(); } });
  return h('div', { class: 'netmap-find' }, icon('search'), input, list);
}

/* ---------- Saving the picture ---------- */

/** A copy of the map's SVG that stands on its own: the colours in use now, inline. */
function standalone(el) {
  const copy = el.cloneNode(true);
  const cs = getComputedStyle(el);
  copy.setAttribute('xmlns', 'http://www.w3.org/2000/svg');
  const src = [...el.querySelectorAll('*')], dst = [...copy.querySelectorAll('*')];
  src.forEach((s, i) => {
    const st = getComputedStyle(s);
    for (const prop of ['fill', 'stroke', 'stroke-width', 'stroke-dasharray', 'stroke-linecap', 'stroke-opacity', 'opacity', 'font-family', 'font-weight']) {
      const v = st.getPropertyValue(prop); if (v) dst[i].style.setProperty(prop, v);
    }
    dst[i].style.removeProperty('transition');
    dst[i].style.removeProperty('animation');
  });
  copy.style.background = cs.backgroundColor === 'rgba(0, 0, 0, 0)' ? getComputedStyle(document.body).backgroundColor : cs.backgroundColor;
  return copy;
}

function download(href, name) {
  const a = h('a', { href, download: name });
  document.body.append(a); a.click(); a.remove();
}

/** Saves the map as an SVG file. */
export function exportMapSvg(el, name = 'gwatch-network-map.svg') {
  if (!el) { toast('Nothing to export yet'); return; }
  const blob = new Blob([new XMLSerializer().serializeToString(standalone(el))], { type: 'image/svg+xml' });
  const url = URL.createObjectURL(blob);
  download(url, name);
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

/**
 * Saves the map as a PNG — what is in view, at twice the screen's
 * resolution, for slides and documents.
 */
export function exportMapPng(el, name = 'gwatch-network-map.png') {
  if (!el) { toast('Nothing to export yet'); return; }
  const r = el.getBoundingClientRect();
  const copy = standalone(el);
  const scale = 2;
  copy.setAttribute('width', String(Math.round(r.width)));
  copy.setAttribute('height', String(Math.round(r.height)));
  const url = URL.createObjectURL(new Blob([new XMLSerializer().serializeToString(copy)], { type: 'image/svg+xml' }));
  const img = new Image();
  img.onload = () => {
    const canvas = document.createElement('canvas');
    canvas.width = Math.round(r.width * scale); canvas.height = Math.round(r.height * scale);
    const g = canvas.getContext('2d');
    g.fillStyle = copy.style.background || '#0f1114';
    g.fillRect(0, 0, canvas.width, canvas.height);
    g.drawImage(img, 0, 0, canvas.width, canvas.height);
    URL.revokeObjectURL(url);
    canvas.toBlob((blob) => {
      if (!blob) { toast('Could not make the picture', { kind: 'error' }); return; }
      const out = URL.createObjectURL(blob);
      download(out, name);
      setTimeout(() => URL.revokeObjectURL(out), 1000);
    }, 'image/png');
  };
  img.onerror = () => { URL.revokeObjectURL(url); toast('Could not make the picture', { kind: 'error' }); };
  img.src = url;
}

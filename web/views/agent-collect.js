// Choosing what an agent reports: which kinds of reading, and — once the
// machine has said what it has — which network interfaces, disks and
// filesystems. Used when a machine is added (kinds only, since nothing has
// reported yet) and on the machine's node afterwards (everything).
//
// The empty answer is "automatic" everywhere, which is also what an agent does
// when nobody has chosen: every kind of reading, and every interface that is not
// obviously virtual. The picker therefore only writes a list when someone has
// narrowed something, so leaving it alone changes nothing.
//
// Services and programs to watch (#62) are the exception: empty means none,
// since "every service" is hundreds of readings nobody wants. They are names
// typed or picked from what the machine said it has (watchPicker).

import { h, chipInput } from '../components.js';

/** The kinds of reading an agent collects, in the order they are listed. */
export const COLLECT_FAMILIES = [
  { key: 'cpu', label: 'Processor use' },
  { key: 'load', label: 'Load average', help: 'For machines that cannot report processor use' },
  { key: 'memory', label: 'Memory' },
  { key: 'swap', label: 'Swap' },
  { key: 'disk', label: 'Disk space' },
  { key: 'inodes', label: 'Inodes', help: 'Needs disk space' },
  { key: 'net', label: 'Network throughput' },
  { key: 'diskio', label: 'Disk throughput' },
  { key: 'temp', label: 'Temperatures', help: 'Linux' },
  { key: 'psi', label: 'Pressure stall', help: 'Linux' },
  { key: 'pool', label: 'ZFS pool health', help: 'Linux' },
];

const INSTANCE_KINDS = [
  { key: 'interfaces', title: 'Network interfaces', auto: 'Automatic leaves out virtual, bridge and tunnel interfaces.' },
  { key: 'disks', title: 'Disks (throughput)', auto: 'Automatic reports every physical disk.' },
  { key: 'mounts', title: 'Filesystems (space)', auto: 'Automatic reports every mounted filesystem.' },
];

let seq = 0;

/**
 * collectionPicker({ value, inventory, instances }) → { el, value(), summary() }
 *
 * `value` is a selection as the API has it ({ metrics, interfaces, disks, mounts },
 * any of them absent). `inventory` is what the agent said it could collect
 * ({ interfaces: [{ name, detail, auto }], … }), or null before it has said.
 * `instances: false` shows only the kinds of reading.
 */
export function collectionPicker({ value = {}, inventory = null, instances = true, disabled = false } = {}) {
  const id = `collect-${++seq}`;
  const chosen = new Set(value.metrics?.length ? value.metrics : COLLECT_FAMILIES.map((f) => f.key));

  const famBoxes = COLLECT_FAMILIES.map((f) => {
    const input = h('input', { type: 'checkbox', id: `${id}-${f.key}`, checked: chosen.has(f.key), disabled });
    return { key: f.key, input, el: h('label', { class: 'checkbox', for: input.id }, input, h('span', null, f.label, f.help ? h('span', { class: 'dim small' }, ` — ${f.help}`) : null)) };
  });

  const kinds = instances ? INSTANCE_KINDS.map((k) => buildKind(id, k, value[k.key] || [], inventory?.[k.key] || [], disabled)) : [];
  const watch = instances ? watchPicker({ value, inventory }) : null;

  const el = h('div', { class: 'stack-sm collect-picker' },
    h('fieldset', { class: 'collect-group' }, h('legend', null, 'Kinds of reading'),
      h('div', { class: 'collect-cols' }, famBoxes.map((b) => b.el))),
    kinds.map((k) => k.el),
    instances && !inventory ? h('p', { class: 'note' }, 'Which network interfaces, disks and filesystems to report can be chosen once the machine has reported for the first time.') : null,
    watch ? watch.el : null);

  const api = {
    el,
    /** The selection as the API wants it. */
    value() {
      const ticked = famBoxes.filter((b) => b.input.checked).map((b) => b.key);
      const out = { metrics: ticked.length === famBoxes.length ? [] : ticked };
      for (const k of kinds) out[k.key] = k.value();
      if (watch) Object.assign(out, watch.value());
      // Nothing narrowed anywhere is "automatic": the empty selection.
      return out;
    },
    /** One line saying what is chosen, for a summary. */
    summary: () => describeSelection(api.value()),
  };
  return api;
}

/** A selection in words: what has been narrowed, or "everything" when nothing has. */
export function describeSelection(sel = {}) {
  const parts = [];
  if (sel.metrics?.length) parts.push(`${sel.metrics.length} of ${COLLECT_FAMILIES.length} kinds of reading`);
  for (const k of INSTANCE_KINDS) if (sel[k.key]?.length) parts.push(`${sel[k.key].length} ${k.title.toLowerCase()}`);
  const watched = describeWatch(sel);
  if (!parts.length) return watched ? `everything, with virtual interfaces left out; watching ${watched}` : 'everything, with virtual interfaces left out';
  return watched ? `${parts.join(', ')}; watching ${watched}` : parts.join(', ');
}

/** The services and programs a selection watches, in words, or '' for none. */
export function describeWatch(sel = {}) {
  const n = (list) => (list || []).filter(Boolean).length;
  const parts = [];
  if (n(sel.services)) parts.push(`${n(sel.services)} service${n(sel.services) === 1 ? '' : 's'}`);
  if (n(sel.processes)) parts.push(`${n(sel.processes)} program${n(sel.processes) === 1 ? '' : 's'}`);
  return parts.join(' and ');
}

/**
 * watchPicker({ value, inventory, note }) → { el, value(), setInventory(inv) }
 *
 * The services and programs to watch, each a list of names. A name can be
 * typed, or picked from the machine's own list (`inventory.services`,
 * `inventory.processes`), which an agent sends when it checks in and the
 * computer GWatch runs on makes when asked. The machine's lists are hundreds
 * long, so they are offered as suggestions while typing rather than as a
 * wall of checkboxes. value() is `{ services: [...], processes: [...] }`.
 */
export function watchPicker({ value = {}, inventory = null, note = null } = {}) {
  const kinds = [
    { key: 'services', title: 'Services to watch', placeholder: 'e.g. nginx, docker, smbd', help: 'Each is a reading of its own: 1 running, 0 stopped. The hardware check is critical when a watched service stops. A systemd unit on Linux, a service name or display name on Windows, a launchd label on macOS.' },
    { key: 'processes', title: 'Programs to watch', placeholder: 'e.g. plex, postgres', help: 'Each is a count of the copies running, by executable name. The hardware check is critical when none is running.' },
  ];
  const parts = kinds.map((k) => {
    const holder = h('div');
    let chips = null;
    const listNote = h('p', { class: 'note' });
    const build = (inv) => {
      const items = inv?.[k.key] || [];
      const current = chips ? chips.value : (value[k.key] || []).filter(Boolean);
      chips = chipInput({ values: current, placeholder: k.placeholder, suggestions: items.map((i) => i.name) });
      chips.input.setAttribute('aria-label', k.title);
      holder.replaceChildren(chips);
      const running = items.filter((i) => /^running|^\d+ running/.test(i.detail || '')).length;
      listNote.textContent = items.length
        ? `${items.length} on this machine${k.key === 'services' ? `, ${running} running` : ''} — start typing to pick one.`
        : '';
    };
    build(inventory);
    return {
      key: k.key,
      build,
      el: h('fieldset', { class: 'collect-group', 'data-watch': k.key }, h('legend', null, k.title),
        holder, h('p', { class: 'note' }, k.help), listNote),
      value: () => chips.value,
    };
  });
  const el = h('div', { class: 'stack-sm watch-picker' },
    note ? h('p', { class: 'note' }, note) : null,
    parts.map((p) => p.el));
  return {
    el,
    value: () => Object.fromEntries(parts.map((p) => [p.key, p.value()])),
    /** Offers a freshly made list of what the machine has, keeping what is chosen. */
    setInventory(inv) { for (const p of parts) p.build(inv); },
  };
}

function buildKind(id, kind, current, items, disabled) {
  const chosen = new Set(current);
  const only = chosen.size > 0;
  const seen = new Set(items.map((i) => i.name));
  // A name that was chosen but is not on the machine now (a drive that was
  // unplugged) stays in the list, so saving does not quietly forget it.
  const rows = items.map((i) => ({ ...i, here: true })).concat([...chosen].filter((n) => !seen.has(n)).map((n) => ({ name: n, here: false })));

  const boxes = rows.map((r, i) => {
    const input = h('input', { type: 'checkbox', id: `${id}-${kind.key}-${i}`, checked: only ? chosen.has(r.name) : r.auto !== false, disabled });
    return {
      name: r.name, input,
      el: h('label', { class: 'checkbox', for: input.id }, input,
        h('span', null, r.name,
          r.auto === false ? h('span', { class: 'tag' }, 'hidden by default') : null,
          r.here ? null : h('span', { class: 'tag' }, 'not seen now'),
          r.detail ? h('span', { class: 'dim small collect-detail' }, r.detail) : null)),
    };
  });
  const list = h('div', { class: 'collect-cols' }, boxes.map((b) => b.el));
  const auto = h('input', { type: 'radio', name: `${id}-${kind.key}-mode`, id: `${id}-${kind.key}-auto`, checked: !only, disabled });
  const pick = h('input', { type: 'radio', name: `${id}-${kind.key}-mode`, id: `${id}-${kind.key}-only`, checked: only, disabled });
  const sync = () => { list.hidden = !pick.checked; };
  auto.addEventListener('change', sync);
  pick.addEventListener('change', sync);
  sync();

  const el = h('fieldset', { class: 'collect-group' }, h('legend', null, kind.title),
    h('div', { class: 'collect-modes' },
      h('label', { class: 'radio', for: auto.id }, auto, h('span', null, 'Automatic')),
      h('label', { class: 'radio', for: pick.id }, pick, h('span', null, 'Only the ones I tick'))),
    h('p', { class: 'note' }, kind.auto),
    rows.length ? list : h('p', { class: 'note' }, 'Nothing listed yet.'));
  return { key: kind.key, el, value: () => (pick.checked ? boxes.filter((b) => b.input.checked).map((b) => b.name) : []) };
}

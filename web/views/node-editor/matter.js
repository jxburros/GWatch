// The node editor's Matter check fields: the server, the readings and commissioning.
//
// Part of node-editor.js, split out for #118: the code is the
// view's own, moved here unchanged inside create(shell), which hands it the
// view's shared state and helpers.

import { api } from '../../api.js';
import { h, icon, clear, replace, field, textInput, numberInput, selectInput, checkbox, toast, openModal, busy } from '../../components.js';
import { MATTER_READING_KINDS, MATTER_DEFAULT_PORT, MATTER_DEFAULT_PATH, matterKind } from '../node-fields.js';
import { SNMP_THRESHOLD_KEYS } from './shared.js';

/** @param {Record<string, any>} shell the view's state and helpers */
export function create(shell) {
  const { d, nodes } = shell;

  // The address the Matter server is asked at: the check's own, or the node
  // host on the default port and path — the same fallback the runner uses
  // (internal/matter.ServerURL), so what the placeholder promises is what a
  // run does.
  function matterServerShown(cfg) {
    const own = (cfg.matterServer || '').trim();
    if (own) return own;
    const host = (d.host || '').trim().replace(/^[a-z]+:\/\//i, '').replace(/[/:].*$/, '');
    return host ? `ws://${host}:${MATTER_DEFAULT_PORT}${MATTER_DEFAULT_PATH}` : '';
  }

  function matterFields(c, err) {
    const cfg = c.config;
    if (!Array.isArray(cfg.matterReadings)) cfg.matterReadings = [];

    const server = textInput({
      value: cfg.matterServer || '', class: 'mono',
      placeholder: matterServerShown({}) ? `Uses node host (${matterServerShown({})})` : `ws://192.168.1.20:${MATTER_DEFAULT_PORT}${MATTER_DEFAULT_PATH}`,
      oninput: () => { cfg.matterServer = server.value; },
    });
    const nodeId = numberInput({ value: cfg.matterNodeId || '', min: 1, placeholder: 'Pick with Load devices', oninput: () => { cfg.matterNodeId = Number(nodeId.value) || 0; } });

    /* ---- the readings table ---- */
    const rowsWrap = h('div', { class: 'table-wrap' });
    const small = (props) => numberInput({ ...props, style: { minWidth: '72px' } });
    function renderRows() {
      clear(rowsWrap);
      if (!cfg.matterReadings.length) {
        rowsWrap.append(h('p', { class: 'note' }, 'No readings yet. Load the devices from the Matter server and tick the ones to watch, or add a row by hand.'));
        return;
      }
      const table = h('table', { class: 'table' }, h('thead', null, h('tr', null,
        h('th', { class: 'num' }, 'Endpoint'), h('th', null, 'Reading'), h('th', { class: 'num' }, 'Cluster'), h('th', { class: 'num' }, 'Attribute'), h('th', { class: 'num' }, 'Scale'),
        h('th', null, 'Name'), h('th', null, 'Unit'), h('th', { class: 'num' }, 'Warn >'), h('th', { class: 'num' }, 'Crit >'),
        h('th', { class: 'num' }, 'Warn <'), h('th', { class: 'num' }, 'Crit <'), h('th', null, ''))));
      const tb = h('tbody');
      cfg.matterReadings.forEach((r, i) => {
        const plain = r.kind === 'attribute';
        const endpoint = small({ value: r.endpoint ?? 1, min: 0, 'aria-label': 'Endpoint', oninput: () => { r.endpoint = endpoint.value; } });
        const kind = selectInput({
          options: MATTER_READING_KINDS.map((k) => ({ value: k.value, label: k.label })), value: r.kind || 'temperature', 'aria-label': 'Reading',
          onchange: () => {
            const before = matterKind(r.kind), after = matterKind(kind.value);
            // A name or unit still showing the old kind's default follows
            // the new kind; one somebody typed is left alone.
            if (!r.name || r.name === before?.name) r.name = after?.name || r.name;
            if (!r.unit || r.unit === before?.unit) r.unit = after?.unit || '';
            r.kind = kind.value;
            renderRows();
          },
        });
        const cluster = small({ value: plain ? (r.cluster ?? '') : '', min: 0, disabled: !plain, placeholder: plain ? 'id' : '—', 'aria-label': 'Cluster id', oninput: () => { r.cluster = cluster.value; } });
        const attribute = small({ value: plain ? (r.attribute ?? '') : '', min: 0, disabled: !plain, placeholder: plain ? 'id' : '—', 'aria-label': 'Attribute id', oninput: () => { r.attribute = attribute.value; } });
        const scale = small({ value: plain ? (r.scale ?? 1) : '', step: 'any', disabled: !plain, placeholder: plain ? '1' : '—', 'aria-label': 'Scale', oninput: () => { r.scale = scale.value; } });
        const name = textInput({ value: r.name || '', placeholder: matterKind(r.kind)?.name || 'Name', 'aria-label': 'Reading name', oninput: () => { r.name = name.value; } });
        const unit = textInput({ value: r.unit || '', placeholder: '—', 'aria-label': 'Unit', style: { minWidth: '56px' }, oninput: () => { r.unit = unit.value; } });
        const th = SNMP_THRESHOLD_KEYS.map((k) => {
          const input = small({ value: r[k] ?? '', step: 'any', placeholder: 'off', 'aria-label': `${k} threshold`, oninput: () => { r[k] = input.value; } });
          return h('td', { class: 'num' }, input);
        });
        tb.append(h('tr', null,
          h('td', { class: 'num' }, endpoint), h('td', null, kind), h('td', { class: 'num' }, cluster), h('td', { class: 'num' }, attribute), h('td', { class: 'num' }, scale),
          h('td', null, name), h('td', null, unit), ...th,
          h('td', null, h('button', { class: 'btn btn-sm icon-btn btn-danger', type: 'button', 'aria-label': `Remove ${r.name || 'reading'}`, title: 'Remove', onclick: () => { cfg.matterReadings.splice(i, 1); renderRows(); } }, icon('x')))));
      });
      table.append(tb);
      rowsWrap.append(table);
    }
    renderRows();

    const loadOut = h('div', { class: 'snmp-test', 'aria-live': 'polite' });
    const loadBtn = h('button', { class: 'btn btn-sm btn-primary', type: 'button', onclick: () => loadMatterDevices(c, loadBtn, loadOut, () => { nodeId.value = cfg.matterNodeId || ''; renderRows(); }) }, icon('search'), 'Load devices');

    return h('div', { class: 'stack' },
      h('div', { class: 'snmp-guide' },
        h('div', { class: 'section-title' }, icon('home'), 'Reading Matter devices'),
        h('ol', null,
          h('li', null, 'Run ', h('b', null, 'python-matter-server'), ' (the Matter controller Home Assistant uses) next to GWatch — the Matter guide has a Docker Compose file. GWatch asks it over a WebSocket; the Matter fabric and its credentials stay in the server’s own storage.'),
          h('li', null, 'Add your devices to it below with a pairing code. A device already in Apple Home, Google Home, Alexa or SmartThings can join a second controller too: ask that app for a new pairing code.'),
          h('li', null, h('b', null, 'Load devices'), ', pick the node, and tick the readings to watch. A bridge lists each device it bridges as its own endpoint.')),
        h('a', { class: 'small', href: '#/help?topic=matter' }, 'More about Matter in Help')),
      h('div', { class: 'form-grid' },
        field({ label: 'Matter server', input: server, error: err.matterServer, help: `The python-matter-server WebSocket address. Leave blank to use the node host on port ${MATTER_DEFAULT_PORT}.` }),
        field({ label: 'Node id', input: nodeId, error: err.matterNodeId, help: 'The number the Matter server gave the device when it was added.' })),
      h('div', { class: 'row' }, loadBtn, h('span', { class: 'muted small' }, 'Lists every device the Matter server knows, with what each one can report.')),
      loadOut,
      h('div', { class: 'row-between u-mt-4' },
        h('div', { class: 'section-title u-mb-0' }, 'Readings'),
        h('div', { class: 'btn-group' },
          h('button', { class: 'btn btn-sm', type: 'button', onclick: () => { cfg.matterReadings.push({ endpoint: 1, kind: 'temperature', name: uniqueMatterName(cfg, 'Temperature'), unit: '°C' }); renderRows(); } }, icon('plus'), 'Add a reading'))),
      h('p', { class: 'note' }, 'On, open and occupied read as 1, off, shut and clear as 0, so a state becomes an alert with a threshold: ', h('b', null, 'Crit > 0'), ' on a door’s contact marks the check down while it is open. Thresholds are strict, as everywhere else. A device the Matter server cannot reach marks the check down; a sensor that reports no value right now marks it degraded.'),
      err.matterReadings ? h('div', { class: 'error small', style: { color: 'var(--down)' } }, err.matterReadings) : null,
      rowsWrap,
      commissionPanel(c, () => loadBtn.click()),
    );
  }

  // Names identify a metric in charts, so a picked reading cannot take a name
  // another reading on the check already has.
  function uniqueMatterName(cfg, base) {
    const taken = new Set((cfg.matterReadings || []).map((r) => (r.name || '').toLowerCase()));
    if (!taken.has(base.toLowerCase())) return base;
    for (let i = 2; i < 100; i++) {
      const candidate = `${base} (${i})`;
      if (!taken.has(candidate.toLowerCase())) return candidate;
    }
    return `${base} ${Date.now()}`;
  }

  // loadMatterDevices asks the Matter server which nodes it has and shows
  // them as a list to pick from: a node, then the readings each of its
  // endpoints offers, with what they read right now.
  async function loadMatterDevices(c, btn, out, onPicked) {
    const cfg = c.config;
    const done = busy(btn, 'Loading…');
    let answer;
    try {
      answer = await api.post('/api/matter/nodes', { server: (cfg.matterServer || '').trim(), host: (d.host || '').trim() });
    } catch (e) {
      replace(out, h('div', { class: 'snmp-test-fail' }, icon('alert'),
        h('div', null,
          h('b', null, 'Could not load devices'),
          h('div', { class: 'muted small' }, e.message),
          h('ul', { class: 'small' },
            h('li', null, 'Is python-matter-server running, and is its port (', String(MATTER_DEFAULT_PORT), ' by default) reachable from this computer?'),
            h('li', null, 'Is the address its WebSocket — ending in ', h('code', null, MATTER_DEFAULT_PATH), ' — rather than its web dashboard?')))));
      done();
      return;
    }
    done();
    const nodes = answer?.nodes || [];
    replace(out, h('div', { class: 'snmp-test-ok' }, icon('check'), h('div', null,
      h('b', null, `Connected to ${answer.server}`),
      h('div', { class: 'muted small' }, `${nodes.length} device${nodes.length === 1 ? '' : 's'} on this Matter server${answer.serverInfo?.sdk_version ? ` · SDK ${answer.serverInfo.sdk_version}` : ''}`))));
    if (!nodes.length) { toast('The Matter server has no devices yet. Add one with a pairing code below.', { kind: 'info' }); return; }

    const nodeTitle = (n) => n.label || [n.vendor, n.product].filter(Boolean).join(' ') || `Node ${n.nodeId}`;
    let chosen = nodes.find((n) => n.nodeId === Number(cfg.matterNodeId)) || nodes[0];
    const pick = selectInput({
      options: nodes.map((n) => ({ value: String(n.nodeId), label: `${n.nodeId} — ${nodeTitle(n)}${n.isBridge ? ' (bridge)' : ''}${n.available ? '' : ' — unavailable'}` })),
      value: String(chosen.nodeId), 'aria-label': 'Matter node',
      onchange: () => { chosen = nodes.find((n) => String(n.nodeId) === pick.value); renderList(); },
    });
    const listWrap = h('div', { class: 'table-wrap', style: { maxHeight: '44vh', overflow: 'auto' } });
    const boxes = new Map();
    function renderList() {
      clear(listWrap); boxes.clear();
      const switching = Number(cfg.matterNodeId) && Number(cfg.matterNodeId) !== chosen.nodeId;
      const have = new Set(switching ? [] : cfg.matterReadings.map((r) => `${r.endpoint}/${r.kind}`));
      const tb = h('tbody');
      for (const ep of chosen.endpoints || []) {
        for (const rd of ep.readings || []) {
          const already = have.has(`${ep.id}/${rd.kind}`);
          const box = h('input', { type: 'checkbox', checked: false, disabled: already, 'aria-label': `Add ${rd.name} on endpoint ${ep.id}` });
          boxes.set(box, { ep, rd });
          tb.append(h('tr', null, h('td', null, box), h('td', { class: 'num mono' }, String(ep.id)),
            h('td', null, h('b', null, ep.label || (ep.deviceTypes || []).join(', ') || '—'), ep.label && ep.deviceTypes?.length ? h('div', { class: 'muted small' }, ep.deviceTypes.join(', ')) : null),
            h('td', null, matterKind(rd.kind)?.label || rd.kind, already ? h('span', { class: 'tag' }, 'already added') : null),
            h('td', { class: 'num mono' }, rd.current ? `${rd.current}${rd.unit ? ` ${rd.unit}` : ''}` : h('span', { class: 'dim' }, '—'))));
        }
      }
      if (!tb.children.length) {
        listWrap.append(h('p', { class: 'note' }, 'This device reports none of the standard readings GWatch knows. Add an "Other attribute…" row by hand with its cluster and attribute id.'));
        return;
      }
      listWrap.append(h('table', { class: 'table' }, h('thead', null, h('tr', null, h('th', null, ''), h('th', { class: 'num' }, 'Endpoint'), h('th', null, 'Device'), h('th', null, 'Reading'), h('th', { class: 'num' }, 'Now'))), tb));
      if (switching) listWrap.append(h('p', { class: 'note' }, `This check reads node ${cfg.matterNodeId}. Adding readings from node ${chosen.nodeId} replaces the ones it has.`));
    }
    renderList();

    const go = h('button', { class: 'btn btn-primary', type: 'button', onclick: () => {
      const picked = [...boxes].filter(([b]) => b.checked).map(([, v]) => v);
      if (!picked.length) { m.close(); return; }
      if (Number(cfg.matterNodeId) !== chosen.nodeId) cfg.matterReadings = [];
      cfg.matterNodeId = chosen.nodeId;
      if (!(cfg.matterServer || '').trim() && answer.server !== matterServerShown({})) cfg.matterServer = answer.server;
      for (const { ep, rd } of picked) {
        const k = matterKind(rd.kind) || {};
        const base = ep.label ? `${ep.label} ${(k.name || rd.name).toLowerCase()}` : (k.name || rd.name);
        cfg.matterReadings.push({ endpoint: ep.id, kind: rd.kind, name: uniqueMatterName(cfg, base), unit: k.unit ?? rd.unit ?? '', ...(k.defaults || {}) });
      }
      if (cfg.matterReadings.length > 64) toast('A Matter check takes at most 64 readings: split the rest over a second check.', { kind: 'error' });
      m.close();
      onPicked();
      toast(`Added ${picked.length} reading${picked.length === 1 ? '' : 's'} from node ${chosen.nodeId}.`, { kind: 'success' });
    } }, 'Add readings');

    const m = openModal({
      title: 'Pick Matter readings',
      wide: true,
      body: h('div', { class: 'stack-sm' },
        field({ label: 'Device', input: pick, help: 'A bridge is one node; each device behind it is an endpoint below.' }),
        listWrap),
      footer: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, 'Cancel'), go],
    });
  }

  // commissionPanel adds a device to the Matter server's fabric with a
  // pairing code. GWatch relays the code and nothing else: the certificates
  // the device receives are made and kept by the Matter server.
  function commissionPanel(c, reload) {
    const cfg = c.config;
    const code = textInput({ placeholder: 'e.g. 3497-011-2332 or MT:Y.K90…', class: 'mono', autocomplete: 'off', 'aria-label': 'Pairing code' });
    const onNetwork = checkbox({ label: 'Already on the network (Wi-Fi, Ethernet or Thread through a border router)', checked: true });
    const out = h('div', { 'aria-live': 'polite' });
    const btn = h('button', { class: 'btn btn-sm', type: 'button', onclick: async () => {
      if (!code.value.trim()) { toast('Enter the pairing code first.', { kind: 'error' }); return; }
      const done = busy(btn, 'Adding… (up to a few minutes)');
      try {
        const answer = await api.post('/api/matter/commission', { server: (cfg.matterServer || '').trim(), host: (d.host || '').trim(), code: code.value.trim(), networkOnly: onNetwork.input.checked });
        const n = answer.node || {};
        code.value = '';
        replace(out, h('div', { class: 'snmp-test-ok' }, icon('check'), h('div', null,
          h('b', null, `Added node ${n.nodeId}${n.product ? ` — ${[n.vendor, n.product].filter(Boolean).join(' ')}` : ''}`),
          h('div', { class: 'muted small' }, 'Load devices to pick its readings.'))));
        reload();
      } catch (e) {
        replace(out, h('div', { class: 'snmp-test-fail' }, icon('alert'), h('div', null, h('b', null, 'Could not add the device'), h('div', { class: 'muted small' }, e.message))));
      } finally { done(); }
    } }, icon('plus'), 'Add device');
    return h('details', { class: 'collapsible admin-only' },
      h('summary', null, icon('chevronRight'), 'Add a device to the Matter server'),
      h('div', { class: 'stack-sm u-pt-8' },
        h('p', { class: 'note' }, 'Use the 11-digit code or the QR code text on the device or its box. For a device already set up in another app, open it there and choose ', h('i', null, 'Turn on pairing mode'), ' or ', h('i', null, 'Linked Matter apps & services'), ' to get a fresh code — the device stays in that app as well.'),
        h('div', { class: 'form-grid' }, field({ label: 'Pairing code', input: code }), field({ label: 'How to find it', input: h('div', { class: 'u-pt-6' }, onNetwork), help: 'Untick only for a brand-new device the Matter server should find over Bluetooth, which needs a Bluetooth adapter on that machine.' })),
        h('div', { class: 'row' }, btn),
        out,
        h('p', { class: 'note' }, 'A Thread device needs a Thread border router (an Apple TV, a HomePod mini, a Nest hub or an OpenThread border router) on the same network as the Matter server.')));
  }

  return { matterFields };
}

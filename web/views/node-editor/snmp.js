// The node editor's SNMP check fields: credentials, OIDs and the device walk.
//
// Part of node-editor.js, split out for #118: the code is the
// view's own, moved here unchanged inside create(shell), which hands it the
// view's shared state and helpers.

import { api } from '../../api.js';
import { h, icon, clear, replace, field, textInput, numberInput, selectInput, checkbox, toast, promptDialog, openModal, busy } from '../../components.js';
import { duration } from '../../fmt.js';
import { SNMP_PRESETS, SNMP_THRESHOLD_KEYS, oidRow } from './shared.js';

const SNMP_AUTH_PROTOCOLS = ['', 'MD5', 'SHA', 'SHA224', 'SHA256', 'SHA384', 'SHA512'];

const SNMP_PRIV_PROTOCOLS = ['', 'DES', 'AES', 'AES192', 'AES256', 'AES192C', 'AES256C'];

/** @param {Record<string, any>} shell the view's state and helpers */
export function create(shell) {
  const { d, targetField } = shell;

  function snmpFields(c, err) {
    const cfg = c.config;
    if (!Array.isArray(cfg.snmpOids)) cfg.snmpOids = [];

    const version = selectInput({
      options: [{ value: '2c', label: 'Version 2c — a community string' }, { value: '3', label: 'Version 3 — a user with authentication' }],
      value: cfg.snmpVersion || '2c',
      onchange: () => { cfg.snmpVersion = version.value; renderCreds(); },
    });
    const port = numberInput({ value: cfg.snmpPort || 161, min: 1, max: 65535, oninput: () => { cfg.snmpPort = Number(port.value) || 161; } });

    // A stored credential never comes back from the server: what arrives is
    // the same mask the settings screen uses. The box therefore starts empty
    // and the mask stays in the draft, so leaving the box alone keeps what is
    // stored and typing in it replaces that.
    const secretInput = (key, type = 'password') => {
      const stored = cfg[key] === '********';
      const input = textInput({
        type, value: stored ? '' : (cfg[key] || ''), autocomplete: 'off',
        placeholder: stored ? 'Leave blank to keep the stored value' : '',
        oninput: () => { cfg[key] = input.value; },
      });
      return { input, help: stored ? 'A value is stored — leave this blank to keep it.' : cfg[key] ? 'This value will be saved with the node.' : 'Nothing stored yet.' };
    };

    const community = secretInput('snmpCommunity', 'text');
    const communityRow = h('div', { class: 'form-grid' },
      field({ label: 'Community string', input: community.input, error: err.snmpCommunity, help: `${community.help} Most devices ship with "public", which is read-only.` }));

    const user = textInput({ value: cfg.snmpUser || '', placeholder: 'e.g. monitor', oninput: () => { cfg.snmpUser = user.value; } });
    const authProto = selectInput({
      options: SNMP_AUTH_PROTOCOLS.map((p) => ({ value: p, label: p || 'None (noAuthNoPriv)' })),
      value: cfg.snmpAuthProto || '', onchange: () => { cfg.snmpAuthProto = authProto.value; },
    });
    const privProto = selectInput({
      options: SNMP_PRIV_PROTOCOLS.map((p) => ({ value: p, label: p || 'None (no encryption)' })),
      value: cfg.snmpPrivProto || '', onchange: () => { cfg.snmpPrivProto = privProto.value; },
    });
    const authPass = secretInput('snmpAuthPass');
    const privPass = secretInput('snmpPrivPass');
    const v3Row = h('div', { class: 'form-grid' },
      field({ label: 'User', input: user, error: err.snmpUser }),
      field({ label: 'Authentication', input: authProto, help: 'SHA256 or better where the device offers it.' }),
      field({ label: 'Authentication password', input: authPass.input, error: err.snmpAuthPass, help: authPass.help }),
      field({ label: 'Encryption', input: privProto, help: 'Encryption needs authentication as well.' }),
      field({ label: 'Encryption password', input: privPass.input, error: err.snmpPrivPass, help: privPass.help }));

    const credsWrap = h('div');
    function renderCreds() {
      clear(credsWrap);
      credsWrap.append((cfg.snmpVersion || '2c') === '3' ? v3Row : communityRow);
    }
    renderCreds();

    /* ---- the readings table ---- */
    const rowsWrap = h('div', { class: 'table-wrap' });
    const small = (props) => numberInput({ ...props, style: { minWidth: '72px' } });

    function renderRows() {
      clear(rowsWrap);
      if (!cfg.snmpOids.length) {
        rowsWrap.append(h('p', { class: 'note' }, 'No readings yet. Add one from Presets, or add a blank row and paste an OID into it.'));
        return;
      }
      const table = h('table', { class: 'table' }, h('thead', null, h('tr', null,
        h('th', null, 'OID'), h('th', null, 'Name'), h('th', null, 'Kind'), h('th', { class: 'num' }, 'Scale'),
        h('th', null, 'Unit'), h('th', { class: 'num' }, 'Warn >'), h('th', { class: 'num' }, 'Crit >'),
        h('th', { class: 'num' }, 'Warn <'), h('th', { class: 'num' }, 'Crit <'), h('th', null, ''))));
      const tb = h('tbody');
      cfg.snmpOids.forEach((o, i) => {
        const oid = textInput({ value: o.oid || '', class: 'mono', placeholder: '1.3.6.1.2.1.1.3.0', 'aria-label': 'OID', oninput: () => { o.oid = oid.value; } });
        const name = textInput({ value: o.name || '', placeholder: 'Uptime', 'aria-label': 'Reading name', oninput: () => { o.name = name.value; } });
        const kind = selectInput({ options: [{ value: 'gauge', label: 'Gauge' }, { value: 'counter', label: 'Counter' }], value: o.kind || 'gauge', onchange: () => { o.kind = kind.value; } });
        const scale = small({ value: o.scale ?? 1, step: 'any', 'aria-label': 'Scale', oninput: () => { o.scale = scale.value; } });
        const unit = textInput({ value: o.unit || '', placeholder: '—', 'aria-label': 'Unit', style: { minWidth: '64px' }, oninput: () => { o.unit = unit.value; } });
        const th = SNMP_THRESHOLD_KEYS.map((k) => {
          const input = small({ value: o[k] ?? '', step: 'any', placeholder: 'off', 'aria-label': `${k} threshold`, oninput: () => { o[k] = input.value; } });
          return h('td', { class: 'num' }, input);
        });
        tb.append(h('tr', null,
          h('td', null, oid), h('td', null, name), h('td', null, kind), h('td', { class: 'num' }, scale), h('td', null, unit), ...th,
          h('td', null, h('button', { class: 'btn btn-sm icon-btn btn-danger', type: 'button', 'aria-label': `Remove ${o.name || 'reading'}`, title: 'Remove', onclick: () => { cfg.snmpOids.splice(i, 1); renderRows(); } }, icon('x')))));
      });
      table.append(tb);
      rowsWrap.append(table);
    }
    renderRows();

    const presets = selectInput({
      options: [{ value: '', label: 'Presets…' }],
      style: { width: 'auto' },
      'aria-label': 'Add a preset reading',
      onchange: async () => {
        const [gi, ii] = presets.value.split(':');
        presets.value = '';
        const preset = SNMP_PRESETS[Number(gi)]?.items[Number(ii)];
        if (!preset) return;
        let index = 1;
        if (preset.oid.includes('{N}')) {
          const answer = await promptDialog({
            title: 'Which one?',
            label: preset.oid.startsWith('1.3.6.1.2.1.25') ? 'Processor number' : 'Interface index',
            value: '1',
            confirmLabel: 'Add reading',
            message: 'SNMP numbers ports and processors itself, so the number here is the device’s, not the label on its case. The SNMP guide shows how to walk a device to find out which is which.',
          });
          if (answer == null || answer === '') return;
          index = Number(answer) || 1;
        }
        cfg.snmpOids.push(oidRow(preset, index));
        renderRows();
      },
    });
    for (const [gi, group] of SNMP_PRESETS.entries()) {
      const og = h('optgroup', { label: group.group });
      group.items.forEach((item, ii) => og.append(h('option', { value: `${gi}:${ii}` }, item.label)));
      presets.append(og);
    }

    // Test connection: read the device's system group, which every SNMP
    // agent answers, and say in words whether the settings above work.
    const testOut = h('div', { class: 'snmp-test', 'aria-live': 'polite' });
    const testBtn = h('button', { class: 'btn btn-sm', type: 'button', onclick: () => testConnection(c, testBtn, testOut) }, icon('zap'), 'Test connection');

    return h('div', { class: 'stack' },
      h('div', { class: 'snmp-guide' },
        h('div', { class: 'section-title' }, icon('router'), 'Reading a device over SNMP'),
        h('ol', null,
          h('li', null, 'Switch SNMP on in the device’s own admin page — usually under ', h('i', null, 'Administration › SNMP'), ' or ', h('i', null, 'Services'), '. Read-only is all GWatch needs; allow this computer’s address if the device asks which managers may read it.'),
          h('li', null, 'Copy the community string (version 2c) or the user and passwords (version 3) below. Many devices start with the community ', h('code', null, 'public'), '.'),
          h('li', null, h('b', null, 'Test connection'), ', then ', h('b', null, 'Pick interfaces'), ' to add link state and traffic for the ports you care about — no OIDs to look up.')),
        h('a', { class: 'small', href: '#/help?topic=snmp' }, 'More about SNMP in Help')),
      h('div', { class: 'form-grid' },
        targetField(c, err, 'Device override', d.host ? `Uses node host (${d.host})` : '192.168.1.1', 'The router, switch or access point to read. Leave blank to use the node host.'),
        field({ label: 'SNMP version', input: version }),
        field({ label: 'Port', input: port, error: err.snmpPort, help: 'UDP 161 unless the device was changed.' })),
      credsWrap,
      h('div', { class: 'row' }, testBtn, h('span', { class: 'muted small' }, 'Reads the device’s name, description and uptime with the settings above.')),
      testOut,
      h('div', { class: 'row-between u-mt-4' },
        h('div', { class: 'section-title u-mb-0' }, 'Readings'),
        h('div', { class: 'btn-group' },
          h('button', { class: 'btn btn-sm btn-primary', type: 'button', onclick: (e) => pickInterfaces(c, e.currentTarget, renderRows) }, icon('wifi'), 'Pick interfaces'),
          presets,
          h('button', { class: 'btn btn-sm', type: 'button', onclick: (e) => walkDevice(c, e.currentTarget, renderRows) }, icon('search'), 'Walk this device'),
          h('button', { class: 'btn btn-sm', type: 'button', onclick: () => { cfg.snmpOids.push(oidRow()); renderRows(); } }, icon('plus'), 'Add a reading'))),
      h('p', { class: 'note' }, 'A ', h('b', null, 'gauge'), ' is a value that already means something — a percentage, a temperature, a link state. A ', h('b', null, 'counter'), ' only ever climbs, so GWatch charts how fast it climbs: an interface’s octet counter with a scale of 8 becomes bits per second. The first run after a restart has nothing to compare against, so a counter has no reading (and no verdict) until the second one.'),
      h('p', { class: 'note' }, 'Thresholds are strict: crossing a ', h('b', null, 'Warn'), ' value marks the check degraded, crossing a ', h('b', null, 'Crit'), ' value marks it down. Setting both ', h('b', null, 'Crit >'), ' and ', h('b', null, 'Crit <'), ' to the same number means "must be exactly this", which is how a link-state reading is expressed. A reading that is text rather than a number is shown as it arrived and never thresholded.'),
      err.snmpOids ? h('div', { class: 'error small', style: { color: 'var(--down)' } }, err.snmpOids) : null,
      rowsWrap,
    );
  }

  // snmpWalk reads one subtree of the device with the check's settings. A
  // saved check's credentials never reach the browser, so its id goes along
  // and the service uses the stored ones for any box left blank.
  function snmpWalk(c, oid, max) {
    const cfg = c.config;
    const host = (cfg.target || d.host || '').trim();
    if (!host) return Promise.reject(new Error('Enter the device’s host on the node (or a target on this check) first.'));
    return api.post('/api/snmp/walk', {
      host,
      checkId: c.id || 0,
      version: cfg.snmpVersion || '2c',
      port: Number(cfg.snmpPort) || 161,
      community: cfg.snmpCommunity || '',
      user: cfg.snmpUser || '',
      authProto: cfg.snmpAuthProto || '',
      authPass: cfg.snmpAuthPass || '',
      privProto: cfg.snmpPrivProto || '',
      privPass: cfg.snmpPrivPass || '',
      oid,
      max,
    });
  }

  async function testConnection(c, btn, out) {
    const host = (c.config.target || d.host || '').trim();
    const done = busy(btn, 'Testing…');
    try {
      const answer = await snmpWalk(c, '1.3.6.1.2.1.1', 12);
      const rows = answer?.rows || [];
      const val = (oid) => rows.find((r) => r.oid === oid || r.oid === `.${oid}`)?.value || '';
      const name = val('1.3.6.1.2.1.1.5.0'), descr = val('1.3.6.1.2.1.1.1.0'), ticks = Number(val('1.3.6.1.2.1.1.3.0'));
      replace(out, h('div', { class: 'snmp-test-ok' }, icon('check'),
        h('div', null,
          h('b', null, `Connected to ${name || host}`),
          descr ? h('div', { class: 'muted small' }, descr) : null,
          Number.isFinite(ticks) && ticks > 0 ? h('div', { class: 'muted small' }, `Up for ${duration(ticks / 100)}`) : null,
          !rows.length ? h('div', { class: 'muted small' }, 'It answered, but reported no system details.') : null)));
    } catch (e) {
      replace(out, h('div', { class: 'snmp-test-fail' }, icon('alert'),
        h('div', null,
          h('b', null, `No answer from ${host || 'the device'}`),
          h('div', { class: 'muted small' }, e.message),
          h('ul', { class: 'small' },
            h('li', null, 'Is SNMP switched on in the device’s admin page, and is this computer allowed to read it?'),
            h('li', null, 'Is the community (or the v3 user and passwords) exactly as the device has it? They are case-sensitive.'),
            h('li', null, 'Is UDP port ', String(c.config.snmpPort || 161), ' open between here and the device?')))));
    } finally { done(); }
  }

  // pickInterfaces lists the device's ports by name — ifName, ifDescr and
  // whether each is up — and turns the ticked ones into readings. It saves
  // looking up which SNMP index belongs to which port.
  async function pickInterfaces(c, btn, onAdded) {
    const cfg = c.config;
    const done = busy(btn, 'Reading ports…');
    let descr, names, oper, alias;
    try {
      [descr, names, oper, alias] = await Promise.all([
        snmpWalk(c, '1.3.6.1.2.1.2.2.1.2', 200),
        snmpWalk(c, '1.3.6.1.2.1.31.1.1.1.1', 200).catch(() => ({ rows: [] })),
        snmpWalk(c, '1.3.6.1.2.1.2.2.1.8', 200).catch(() => ({ rows: [] })),
        snmpWalk(c, '1.3.6.1.2.1.31.1.1.1.18', 200).catch(() => ({ rows: [] })),
      ]);
    } catch (e) { toast(`Could not read the device’s ports: ${e.message}`, { kind: 'error' }); done(); return; }
    done();
    const ports = new Map();
    const idx = (oid) => String(oid).replace(/^\./, '').split('.').pop();
    const add = (list, key) => { for (const r of list?.rows || []) { const i = idx(r.oid); if (!ports.has(i)) ports.set(i, { index: i }); ports.get(i)[key] = r.value; } };
    add(descr, 'descr'); add(names, 'name'); add(oper, 'oper'); add(alias, 'alias');
    if (!ports.size) { toast('The device answered, but listed no interfaces.', { kind: 'error' }); return; }
    const list = [...ports.values()].sort((a, b) => Number(a.index) - Number(b.index));
    const label = (p) => String(p.name || p.descr || p.alias || `Port ${p.index}`).trim();
    const has = new Set(cfg.snmpOids.map((o) => (o.oid || '').replace(/^\./, '')));

    const boxes = new Map();
    const tb = h('tbody');
    for (const p of list) {
      const up = String(p.oper) === '1';
      const box = h('input', { type: 'checkbox', checked: up && !/^(lo|loopback|null|bridge|br-|veth|docker)/i.test(p.name || p.descr || ''), 'aria-label': `Add ${label(p)}` });
      boxes.set(box, p);
      tb.append(h('tr', null, h('td', null, box), h('td', { class: 'num mono' }, p.index), h('td', null, h('b', null, p.name || '—'), p.alias ? h('div', { class: 'muted small' }, p.alias) : null),
        h('td', { class: 'muted' }, p.descr || '—'),
        h('td', null, p.oper == null ? h('span', { class: 'dim' }, '—') : h('span', { class: up ? 'text-up' : 'muted' }, up ? 'Up' : 'Down'))));
    }
    const wantLink = checkbox({ label: 'Link up / down (down marks the check down)', checked: true });
    const wantTraffic = checkbox({ label: 'Traffic in and out, in bits per second', checked: true });
    const wantErrors = checkbox({ label: 'Errors in and out (any error marks the check degraded)', checked: false });
    const hc = checkbox({ label: 'Use 64-bit counters (right for anything faster than 100 Mbit/s)', checked: names.rows?.length > 0 });
    const filter = textInput({ placeholder: 'Filter ports', 'aria-label': 'Filter ports', oninput: () => { const q = filter.value.trim().toLowerCase(); for (const tr of tb.children) tr.hidden = q ? !tr.textContent.toLowerCase().includes(q) : false; } });
    const allUp = h('button', { class: 'btn btn-sm', type: 'button', onclick: () => { for (const [b, p] of boxes) b.checked = String(p.oper) === '1'; } }, 'Tick the ports that are up');
    const none = h('button', { class: 'btn btn-sm', type: 'button', onclick: () => { for (const b of boxes.keys()) b.checked = false; } }, 'Untick all');

    const go = h('button', { class: 'btn btn-primary', type: 'button', onclick: () => {
      let added = 0;
      const push = (oid, name, extra) => { if (has.has(oid)) return; cfg.snmpOids.push({ oid, name: uniqueReadingName(cfg, name), ...extra }); has.add(oid); added++; };
      for (const [box, p] of boxes) {
        if (!box.checked) continue;
        const i = p.index, n = label(p);
        if (wantLink.input.checked) push(`1.3.6.1.2.1.2.2.1.8.${i}`, `${n} link`, { kind: 'gauge', scale: 1, unit: '', critBelow: 1, critAbove: 1 });
        if (wantTraffic.input.checked) {
          push(hc.input.checked ? `1.3.6.1.2.1.31.1.1.1.6.${i}` : `1.3.6.1.2.1.2.2.1.10.${i}`, `${n} in`, { kind: 'counter', scale: 8, unit: 'bit/s' });
          push(hc.input.checked ? `1.3.6.1.2.1.31.1.1.1.10.${i}` : `1.3.6.1.2.1.2.2.1.16.${i}`, `${n} out`, { kind: 'counter', scale: 8, unit: 'bit/s' });
        }
        if (wantErrors.input.checked) {
          push(`1.3.6.1.2.1.2.2.1.14.${i}`, `${n} errors in`, { kind: 'counter', scale: 1, unit: '/s', warnAbove: 0 });
          push(`1.3.6.1.2.1.2.2.1.20.${i}`, `${n} errors out`, { kind: 'counter', scale: 1, unit: '/s', warnAbove: 0 });
        }
      }
      if (cfg.snmpOids.length > 64) toast('An SNMP check reads at most 64 values: untick some, or split the ports over a second SNMP check.', { kind: 'error' });
      m.close();
      if (added) { onAdded(); toast(`Added ${added} reading${added === 1 ? '' : 's'}.`, { kind: 'success' }); }
    } }, 'Add readings');

    const m = openModal({
      title: 'Pick interfaces',
      wide: true,
      body: h('div', { class: 'stack-sm' },
        h('p', { class: 'note' }, `${list.length} interface${list.length === 1 ? '' : 's'} found. Tick the ports to watch; GWatch works out the SNMP numbers for you.`),
        h('div', { class: 'row-between' }, filter, h('div', { class: 'btn-group' }, allUp, none)),
        h('div', { class: 'table-wrap', style: { maxHeight: '40vh', overflow: 'auto' } },
          h('table', { class: 'table' }, h('thead', null, h('tr', null, h('th', null, ''), h('th', { class: 'num' }, 'Index'), h('th', null, 'Name'), h('th', null, 'Description'), h('th', null, 'Link'))), tb)),
        h('div', { class: 'section-title' }, 'For each ticked port, read'),
        h('div', { class: 'stack-sm u-gap-6' }, wantLink, wantTraffic, wantErrors, hc)),
      footer: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, 'Cancel'), go],
    });
  }

  // walkDevice asks the device what it can tell us and shows the answer as a
  // list to tick. It is the only honest way to find an interface's SNMP
  // index, which is very often not the number printed on the case.
  async function walkDevice(c, btn, onAdded) {
    const cfg = c.config;
    const host = (cfg.target || d.host || '').trim();
    if (!host) { toast('Enter the device’s host on the node (or a target on this check) first.', { kind: 'error' }); return; }
    const done = busy(btn, 'Walking…');
    let answer;
    try {
      answer = await api.post('/api/snmp/walk', {
        host,
        checkId: c.id || 0,
        version: cfg.snmpVersion || '2c',
        port: Number(cfg.snmpPort) || 161,
        community: cfg.snmpCommunity || '',
        user: cfg.snmpUser || '',
        authProto: cfg.snmpAuthProto || '',
        authPass: cfg.snmpAuthPass || '',
        privProto: cfg.snmpPrivProto || '',
        privPass: cfg.snmpPrivPass || '',
        oid: '1.3.6.1.2.1',
      });
    } catch (e) { toast(`Could not read ${host}: ${e.message}`, { kind: 'error' }); done(); return; }
    done();

    const rows = answer?.rows || [];
    if (!rows.length) { toast(`${host} answered, but reported nothing under 1.3.6.1.2.1.`, { kind: 'error' }); return; }

    const already = new Set(cfg.snmpOids.map((o) => (o.oid || '').replace(/^\./, '')));
    const boxes = new Map();
    const table = h('table', { class: 'table' }, h('thead', null, h('tr', null,
      h('th', null, ''), h('th', null, 'OID'), h('th', null, 'Suggested name'), h('th', null, 'Type'), h('th', null, 'Value'))));
    const tb = h('tbody');
    for (const row of rows) {
      const have = already.has(row.oid);
      const box = h('input', { type: 'checkbox', disabled: have, 'aria-label': `Add ${row.name || row.oid}` });
      boxes.set(box, row);
      tb.append(h('tr', null,
        h('td', null, box),
        h('td', { class: 'mono' }, row.oid),
        h('td', null, row.name || h('span', { class: 'dim' }, '—'), have ? h('span', { class: 'tag' }, 'already added') : null),
        h('td', { class: 'dim' }, row.type),
        h('td', { class: 'mono truncate', style: { maxWidth: '260px' } }, row.value)));
    }
    table.append(tb);

    const filter = textInput({ placeholder: 'Filter by OID, name or value', 'aria-label': 'Filter readings', oninput: () => {
      const q = filter.value.trim().toLowerCase();
      for (const tr of tb.children) tr.hidden = q ? !tr.textContent.toLowerCase().includes(q) : false;
    } });

    const add = h('button', { class: 'btn btn-primary', type: 'button', onclick: () => {
      let added = 0;
      for (const [box, row] of boxes) {
        if (!box.checked) continue;
        const name = uniqueReadingName(cfg, row.name || row.oid);
        cfg.snmpOids.push({ oid: row.oid, name, kind: row.kind === 'counter' ? 'counter' : 'gauge', scale: 1, unit: '' });
        added++;
      }
      m.close();
      if (added) { onAdded(); toast(`Added ${added} reading${added === 1 ? '' : 's'}. Set the scale, unit and thresholds on each.`, { kind: 'success' }); }
    } }, 'Add ticked readings');

    const m = openModal({
      title: `What ${host} can tell us`,
      wide: true,
      body: h('div', { class: 'stack-sm' },
        h('p', { class: 'note' }, `${rows.length} reading${rows.length === 1 ? '' : 's'} under 1.3.6.1.2.1`, answer.truncated ? ` (stopped at the first ${answer.max} — narrow it down on the device if you need more)` : '', '. The last number of an interface row is its SNMP index, which is what the presets ask for. Ticked rows arrive as gauges or counters according to their type; the scale, unit and thresholds are yours to set.'),
        filter,
        h('div', { class: 'table-wrap', style: { maxHeight: '48vh', overflow: 'auto' } }, table)),
      footer: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, 'Cancel'), add],
    });
  }

  // Names identify a metric in charts, so a walked row cannot quietly take a
  // name another reading already has.
  function uniqueReadingName(cfg, base) {
    const taken = new Set((cfg.snmpOids || []).map((o) => (o.name || '').toLowerCase()));
    if (!taken.has(base.toLowerCase())) return base;
    for (let i = 2; i < 100; i++) {
      const candidate = `${base} (${i})`;
      if (!taken.has(candidate.toLowerCase())) return candidate;
    }
    return `${base} ${Date.now()}`;
  }

  return { snmpFields };
}

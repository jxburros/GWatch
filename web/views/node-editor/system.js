// The node editor's hardware health check fields: the source and the per-metric thresholds.
//
// Part of node-editor.js, split out for #118: the code is the
// view's own, moved here unchanged inside create(shell), which hands it the
// view's shared state and helpers.

import { api } from '../../api.js';
import { h, icon, clear, field, textInput, numberInput, selectInput, chipInput, toast } from '../../components.js';
import { METRIC_FAMILIES, SYSTEM_DEFAULTS, legacyMetricThresholds } from './shared.js';

/** @param {Record<string, any>} shell the view's state and helpers */
export function create(shell) {
  const { agents } = shell;

  function systemFields(c, err) {
    const cfg = c.config;
    const wrap = h('div', { class: 'stack' });

    const source = selectInput({
      options: [
        { value: 'local', label: 'This computer — the machine GWatch runs on' },
        { value: 'agent', label: 'A registered machine — it pushes its readings to GWatch' },
        { value: 'url', label: 'A metrics endpoint — GWatch reads it' },
      ],
      value: cfg.hostSource || 'local',
      onchange: () => { cfg.hostSource = source.value; renderSource(); },
    });

    const agentSel = selectInput({
      options: [{ value: '', label: 'Loading machines…' }],
      value: String(cfg.agentId || ''),
      onchange: () => { cfg.agentId = Number(agentSel.value) || 0; },
    });
    const agentRow = field({
      label: 'Machine', input: agentSel, error: err.agentId,
      help: 'Machines are added from Nodes › Add node › GWatch agent. The agent on that machine connects out to GWatch; GWatch never connects to it.',
    });
    loadAgents(agentSel, cfg);

    const urlIn = textInput({
      value: cfg.metricsUrl || '', placeholder: 'https://nas.lan:9713/metrics',
      oninput: () => { cfg.metricsUrl = urlIn.value; },
    });
    const tokenIn = textInput({
      type: 'password', value: cfg.metricsToken || '', placeholder: 'Bearer token for that endpoint', autocomplete: 'off',
      oninput: () => { cfg.metricsToken = tokenIn.value; },
    });
    const urlRow = h('div', { class: 'form-grid' },
      field({ label: 'Metrics URL', input: urlIn, error: err.metricsUrl, help: 'Where gwatch-agent is listening, when it is run with `serve` instead of pushing.' }),
      field({ label: 'Token', input: tokenIn, help: 'Sent as an Authorization header. It is stored in this check\u2019s configuration.' }));

    const sourceWrap = h('div');
    function renderSource() {
      clear(sourceWrap);
      const mode = cfg.hostSource || 'local';
      if (mode === 'agent') sourceWrap.append(agentRow);
      else if (mode === 'url') sourceWrap.append(urlRow);
      else sourceWrap.append(h('p', { class: 'note' }, 'Nothing to configure: GWatch already reads this computer every minute.'));
    }
    renderSource();

    const stale = numberInput({
      value: cfg.staleAfterSeconds || '', min: 0,
      placeholder: `Default (3 \u00d7 the interval, at least 60 s)`,
      oninput: () => { cfg.staleAfterSeconds = Number(stale.value) || 0; },
    });

    const mounts = chipInput({
      values: cfg.diskMounts || [], placeholder: 'e.g. / or C:',
      onChange: (v) => { cfg.diskMounts = v; },
    });

    if (!Array.isArray(cfg.metricThresholds)) cfg.metricThresholds = legacyMetricThresholds(cfg);
    const thresholdsEl = h('div', { class: 'stack-sm', 'aria-label': 'Hardware thresholds' });
    const renderThresholds = () => {
      clear(thresholdsEl);
      for (const f of METRIC_FAMILIES) {
        thresholdsEl.append(thresholdGroup(f, cfg, f.key === 'disk'
          ? field({ label: 'Watch only these mount points', input: mounts, help: 'Leave empty to watch every filesystem.' })
          : null));
      }
      thresholdsEl.append(instanceOverrides(cfg, renderThresholds));
    };
    renderThresholds();

    wrap.append(
      h('div', { class: 'form-grid' },
        field({ label: 'Read hardware from', input: source }),
        field({ label: 'Report down after no reading for', input: stale, help: 'A machine that stops reporting is the signal an agent exists to give.' })),
      sourceWrap,
      h('div', { class: 'section-title u-mt-4' }, 'Warning and critical thresholds'),
      h('p', { class: 'note' }, 'One check, one line of history per metric, and each metric has its own verdict: crossing ', h('b', null, 'warning'), ' marks that metric — and so the check — ', h('b', null, 'degraded'), '; crossing ', h('b', null, 'critical'), ' marks it ', h('b', null, 'down'), '. A warning on the disk and a warning on memory are two incidents, not one. Leave a box blank to turn that level off.'),
      // wrap.append is the DOM's own append, which writes a null argument
      // out as the text "null" (#72) — so the error is only passed when there
      // is one, never as a null placeholder the way h() children can be.
      ...(err.metricThresholds ? [h('div', { class: 'error', role: 'alert' }, err.metricThresholds)] : []),
      thresholdsEl,
    );
    return wrap;
  }

  // thresholdGroup is one metric family's warning/critical pair, boxed and
  // labelled so the two numbers that escalate together read as a unit rather
  // than as two rows in an unrelated grid, with the shipped default visible
  // in each field. `extra` adds a further field to the box, used for Disk's
  // mount-point filter.
  function thresholdGroup(f, cfg, extra) {
    const entry = familyEntry(cfg, f.key);
    const def = SYSTEM_DEFAULTS.metricThresholds.find((t) => t.metric === f.key) || {};
    return h('div', { class: 'threshold-group', 'data-metric': f.key, style: { border: '1px solid var(--line)', padding: '10px 12px 12px' } },
      h('div', { class: 'section-title u-mb-8' }, f.label),
      h('div', { class: 'form-grid' },
        levelField(entry, 'warn', f.below ? 'Warning at or below' : 'Warning', f, def.warn),
        levelField(entry, 'crit', f.below ? 'Critical at or below' : 'Critical', f, def.crit)),
      f.note ? h('p', { class: 'note u-mt-6' }, f.note) : null,
      extra ? h('div', { class: 'u-mt-10' }, extra) : null);
  }

  // familyEntry finds, or creates, the list entry for a family, so the
  // family's boxes always have somewhere to write. A created entry with no
  // level is dropped again on save (see cleanMetricThresholds).
  function familyEntry(cfg, key) {
    let entry = cfg.metricThresholds.find((t) => t.metric === key);
    if (!entry) { entry = { metric: key }; if (METRIC_FAMILIES.find((f) => f.key === key)?.below) entry.below = true; cfg.metricThresholds.push(entry); }
    return entry;
  }

  function levelField(entry, level, label, f, def) {
    const unit = f.key === 'diskio' && (entry.metric || '').endsWith('.busy') ? '%' : f.unit;
    const input = numberInput({
      value: entry[level] ?? '', min: 0, max: unit === '%' ? 100 : undefined, step: f.step || 1,
      placeholder: def != null ? `Default ${def}${unit}` : 'Off',
      'aria-label': `${f.label}${entry.metric.includes(':') ? ' ' + entry.metric.slice(entry.metric.indexOf(':') + 1) : ''} ${label.toLowerCase()} threshold`,
      oninput: () => { entry[level] = input.value === '' ? undefined : Number(input.value); },
    });
    return field({ label, input: unit ? h('div', { class: 'input-with-unit' }, input, h('span', { class: 'unit' }, unit)) : input });
  }

  // instanceOverrides lists the thresholds that name one disk, interface or
  // device, each overriding its family's pair for that instance alone, and
  // offers a row for adding another.
  function instanceOverrides(cfg, rerender) {
    const rows = cfg.metricThresholds.filter((t) => (t.metric || '').includes(':'));
    const box = h('div', { class: 'stack-sm', style: { border: '1px solid var(--line)', padding: '10px 12px 12px' } },
      h('div', { class: 'section-title u-mb-4' }, 'One disk, interface or device'),
      h('p', { class: 'note' }, 'An entry here replaces the family’s pair above for that one instance: a media disk that is always 90% full, an uplink that is meant to run hot. Both boxes blank means that instance is never alerted on.'));
    for (const t of rows) {
      const family = METRIC_FAMILIES.find((f) => f.key === t.metric.slice(0, t.metric.indexOf(':'))) || { key: '', label: t.metric, unit: '' };
      box.append(h('div', { class: 'threshold-group threshold-instance', 'data-metric': t.metric },
        h('div', { class: 'row-between' },
          h('div', { class: 'small' }, h('b', null, family.label), ' ', h('code', null, t.metric.slice(t.metric.indexOf(':') + 1))),
          h('button', { class: 'btn btn-sm icon-btn btn-danger', type: 'button', 'aria-label': `Remove the ${t.metric} threshold`, title: 'Remove', onclick: () => {
            cfg.metricThresholds.splice(cfg.metricThresholds.indexOf(t), 1); rerender();
          } }, icon('trash'))),
        h('div', { class: 'form-grid' },
          levelField(t, 'warn', t.below ? 'Warning at or below' : 'Warning', family),
          levelField(t, 'crit', t.below ? 'Critical at or below' : 'Critical', family))));
    }
    const familySel = selectInput({ options: METRIC_FAMILIES.filter((f) => f.instances).map((f) => ({ value: f.key, label: f.label })), 'aria-label': 'Family of the instance threshold' });
    const instanceIn = textInput({ placeholder: 'e.g. /srv, eth0, sda', 'aria-label': 'Which disk, interface or device' });
    const addBtn = h('button', { class: 'btn btn-sm', type: 'button', onclick: () => {
      const instance = instanceIn.value.trim();
      if (!instance) { toast('Name the disk, interface or device first.', { kind: 'error' }); return; }
      const key = `${familySel.value}:${instance}`;
      if (cfg.metricThresholds.some((t) => t.metric === key)) { toast(`There is already a threshold for ${key}.`, { kind: 'error' }); return; }
      cfg.metricThresholds.push(METRIC_FAMILIES.find((f) => f.key === familySel.value)?.below ? { metric: key, below: true } : { metric: key });
      rerender();
    } }, icon('plus'), 'Add a threshold for one disk / interface');
    box.append(h('div', { class: 'row', style: { gap: '8px', flexWrap: 'wrap', alignItems: 'end' } },
      field({ label: 'Family', input: familySel }),
      field({ label: 'Instance', input: instanceIn, help: METRIC_FAMILIES.find((f) => f.key === familySel.value)?.instances }),
      addBtn));
    return box;
  }

  // The machine list comes from the server, so a check cannot be pointed at a
  // machine that was never registered.
  async function loadAgents(select, cfg) {
    let agents = [];
    try { agents = await api.get('/api/agents'); } catch { agents = null; }
    clear(select);
    if (agents === null) {
      select.append(h('option', { value: '' }, 'Could not load the machine list'));
      return;
    }
    const usable = agents.filter((a) => !a.revokedAt);
    if (!usable.length) {
      select.append(h('option', { value: '' }, 'No machines yet — add one with Add node \u203a GWatch agent'));
      return;
    }
    select.append(h('option', { value: '' }, 'Choose a machine\u2026'));
    for (const a of usable) {
      select.append(h('option', { value: String(a.id) }, a.lastSeenAt ? `${a.name} (${a.hostname || 'reporting'})` : `${a.name} (not reporting yet)`));
    }
    select.value = String(cfg.agentId || '');
  }

  return { systemFields };
}

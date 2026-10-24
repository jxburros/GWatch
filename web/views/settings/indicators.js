// Settings › Indicators.
//
// Part of settings.js, split out for #118: the code is the
// view's own, moved here unchanged inside create(shell), which hands it the
// view's shared state and helpers.

import { api } from '../../api.js';
import { h, icon, clear, textInput, numberInput, selectInput, toggle, confirmDialog, emptyState } from '../../components.js';

/** @param {Record<string, any>} shell the view's state and helpers */
export function create(shell) {
  const { loadSettings, saveSettings, state } = shell;

  // The conditions a rule may test, flattened into one list because
  // "nodes down" and "nodes degraded" are one choice to a reader even though
  // they are one kind and two statuses to the server. Anything offered here
  // has to be answerable from GET /api/status, which is the one document the
  // header already polls; see model.IndicatorCondition.
  const INDICATOR_CONDITIONS = [
    { value: 'nodesInStatus:down', kind: 'nodesInStatus', status: 'down', label: 'Nodes that are down', counted: 'nodes' },
    { value: 'nodesInStatus:degraded', kind: 'nodesInStatus', status: 'degraded', label: 'Nodes that are degraded', counted: 'nodes' },
    { value: 'nodesInStatus:unknown', kind: 'nodesInStatus', status: 'unknown', label: 'Nodes waiting for a first result', counted: 'nodes' },
    { value: 'nodesInStatus:maintenance', kind: 'nodesInStatus', status: 'maintenance', label: 'Nodes in maintenance', counted: 'nodes' },
    { value: 'certWarnings:', kind: 'certWarnings', status: '', label: 'Certificates expiring or invalid', counted: 'certificates' },
    { value: 'attention:', kind: 'attention', status: '', label: 'Checks needing attention', counted: 'checks' },
    { value: 'serviceHealth:', kind: 'serviceHealth', status: '', label: 'The monitor itself is unwell', counted: '' },
  ];
  const INDICATOR_COLOURS = [
    { value: 'red', label: 'Red — needs looking at now' },
    { value: 'orange', label: 'Orange — worth knowing about' },
    { value: 'yellow', label: 'Yellow — for information' },
  ];
  const condKey = (c = {}) => `${c.kind || 'nodesInStatus'}:${c.kind === 'nodesInStatus' ? (c.status || 'down') : ''}`;
  const condOf = (key) => INDICATOR_CONDITIONS.find((c) => c.value === key) || INDICATOR_CONDITIONS[0];

  async function tabIndicators() {
    // `s` is re-pointed after every save, because saveSettings replaces
    // state.settings with the document the server sends back — normalised,
    // with the ids and counts it filled in.
    let s = state.settings || await loadSettings();
    if (!Array.isArray(s.indicators)) s.indicators = [];
    const wrap = h('div', { class: 'stack' });
    // A rule the browser has never seen a server id for gets one here, so
    // that a freshly added row is as complete as a stored one.
    const newId = () => `indicator-${Date.now().toString(36)}-${Math.floor(Math.random() * 1e4)}`;

    const render = () => {
      clear(wrap);
      const rules = h('div', { class: 'ind-rules' });
      s.indicators.forEach((rule, i) => rules.append(ruleRow(rule, i)));
      const card = h('section', { class: 'card' },
        h('div', { class: 'card-head' }, h('h2', null, 'Indicators'),
          h('button', { class: 'btn', type: 'button', onclick: () => { s.indicators.push({ id: newId(), name: 'New indicator', enabled: true, colour: 'yellow', condition: { kind: 'nodesInStatus', status: 'down', minCount: 1 } }); render(); } }, icon('plus'), 'Add indicator')),
        h('p', { class: 'lead' }, 'The circles under the page name in the header. One green circle means nothing here is firing; blue means there is nothing to report on yet — no nodes, or none that has produced a result. When a rule fires, green gives way to one circle per firing rule, reddest first, and clicking one opens what it is about.'),
        s.indicators.length ? rules : emptyState({ icon: 'alert', title: 'No indicators', text: 'With no rules the header only ever shows the green or blue circle. Add one, or restore the defaults below.', compact: true }),
        h('hr', { class: 'divider' }),
        h('div', { class: 'form-actions' },
          h('button', { class: 'btn btn-primary', type: 'button', onclick: (e) => save(e.currentTarget) }, icon('save'), 'Save changes'),
          h('button', { class: 'btn', type: 'button', onclick: async () => {
            if (!await confirmDialog({ title: 'Restore the default indicators?', message: 'The rules you have added or changed here are replaced by the six GWatch ships with. Nothing is saved until you press Save changes.', confirmLabel: 'Restore' })) return;
            // An empty list is what the server reads as "seed the defaults",
            // so saving is what fetches them back: this one button does not
            // wait for Save changes, because there is nothing to review.
            s.indicators = [];
            await saveSettings();
            adopt();
            window.dispatchEvent(new CustomEvent('gw:indicators-changed'));
            render();
          } }, icon('refresh'), 'Restore defaults')));
      wrap.append(card, previewCard(), h('section', { class: 'card' }, h('h2', null, 'Notes'),
        h('div', { class: 'stack-sm u-mt-8' },
          h('p', { class: 'note' }, h('b', null, 'Who sees them: '), 'everyone. The rules are read from settings, which only an administrator may open, so the effective list is served to every account alongside the theme — a viewer’s header shows exactly what yours does.'),
          h('p', { class: 'note' }, h('b', null, 'Where they are evaluated: '), 'in the browser, against the same status summary the header already fetches. That is why the list of conditions is short: each one has to be answerable from that one summary, without a second request on every poll.'),
          h('p', { class: 'note' }, h('b', null, 'Green and blue: '), 'neither is a rule. Green is what is left when nothing fires, and blue says GWatch has nothing to go on yet — while it is blue, only the monitor’s own health can still light a circle.'))));
    };

    const previewCard = () => {
      const row = h('div', { class: 'indicators' });
      const note = h('span', { class: 'muted tiny' }, 'Loading the current status…');
      api.get('/api/status').then((st) => {
        const firing = s.indicators.filter((r) => r.enabled && count(st, r.condition || {}) >= Math.max(1, Number(r.condition?.minCount) || 1));
        clear(row);
        const rank = { red: 0, orange: 1, yellow: 2 };
        firing.sort((a, b) => (rank[a.colour] ?? 9) - (rank[b.colour] ?? 9));
        for (const r of firing) row.append(h('span', { class: `indicator ind-${r.colour}`, title: r.name }, h('span', { class: 'orb' })));
        if (!firing.length) row.append(h('span', { class: 'indicator ind-ok' }, h('span', { class: 'orb' })));
        note.textContent = firing.length ? `${firing.map((r) => r.name).join(', ')} — as your rules stand, unsaved changes included.` : 'Nothing is firing right now, so the header shows the single all-clear circle.';
      }).catch(() => { note.textContent = 'The current status could not be read.'; });
      const count = (st, c) => {
        switch (c.kind) {
          case 'nodesInStatus': return Number({ down: st.down, degraded: st.degraded, unknown: st.unknown, maintenance: st.maintenance }[c.status]) || 0;
          case 'certWarnings': return Number(st.certWarnings) || 0;
          case 'attention': return Number(st.attention) || 0;
          case 'serviceHealth': return st.serviceOk ? 0 : 1;
          default: return 0;
        }
      };
      return h('section', { class: 'card' }, h('h2', null, 'Right now'),
        h('p', { class: 'lead' }, 'What the header would show with these rules.'),
        h('div', { class: 'ind-preview' }, row, note));
    };

    const ruleRow = (rule, i) => {
      rule.condition = rule.condition || { kind: 'nodesInStatus', status: 'down', minCount: 1 };
      // A rule that does not say is on, as its switch shows: settled here so
      // the row, the preview and the save all agree with the switch.
      rule.enabled = rule.enabled !== false;
      const row = h('div', { class: `ind-rule ${rule.enabled ? '' : 'off'}` });
      const on = toggle({ checked: rule.enabled !== false, ariaLabel: `Enable ${rule.name || 'indicator'}`, onChange: (v) => { rule.enabled = v; row.classList.toggle('off', !v); } });
      const swatches = h('div', { class: 'ind-swatches', role: 'radiogroup', 'aria-label': 'Severity' });
      const renderSwatches = () => {
        clear(swatches);
        for (const c of INDICATOR_COLOURS) {
          swatches.append(h('button', { type: 'button', role: 'radio', class: `ind-swatch ind-swatch-${c.value}`, title: c.label, 'aria-label': c.label, 'aria-checked': rule.colour === c.value ? 'true' : 'false', onclick: () => { rule.colour = c.value; renderSwatches(); } }));
        }
      };
      renderSwatches();
      const name = textInput({ value: rule.name || '', class: 'ind-rule-name', 'aria-label': 'Indicator name', placeholder: 'What it means', oninput: () => { rule.name = name.value; } });
      const cond = selectInput({ options: INDICATOR_CONDITIONS.map((c) => ({ value: c.value, label: c.label })), value: condKey(rule.condition), 'aria-label': 'Condition' });
      const min = numberInput({ value: rule.condition.minCount || 1, min: 1, step: 1, 'aria-label': 'How many it takes', oninput: () => { rule.condition.minCount = Math.max(1, Number(min.value) || 1); } });
      const minWrap = h('span', { class: 'ind-rule-cond' }, h('span', { class: 'muted tiny' }, 'at least'), min, h('span', { class: 'muted tiny' }, ''));
      const syncCond = () => {
        const c = condOf(cond.value);
        rule.condition.kind = c.kind;
        rule.condition.status = c.status;
        // The monitor is unwell or it is not, so a count would be a fiction.
        minWrap.hidden = c.kind === 'serviceHealth';
        minWrap.lastChild.textContent = c.counted;
        if (minWrap.hidden) rule.condition.minCount = 1;
      };
      cond.addEventListener('change', syncCond);
      syncCond();
      const move = (delta) => {
        const to = i + delta;
        if (to < 0 || to >= s.indicators.length) return;
        const [moved] = s.indicators.splice(i, 1);
        s.indicators.splice(to, 0, moved);
        render();
      };
      row.append(on, swatches,
        h('div', { class: 'ind-rule-cond' }, name, cond, minWrap),
        h('div', { class: 'ind-rule-actions' },
          h('button', { class: 'btn btn-sm', type: 'button', 'aria-label': 'Move up', title: 'Move up', disabled: i === 0, onclick: () => move(-1) }, '↑'),
          h('button', { class: 'btn btn-sm', type: 'button', 'aria-label': 'Move down', title: 'Move down', disabled: i === s.indicators.length - 1, onclick: () => move(1) }, '↓'),
          h('button', { class: 'btn btn-sm btn-danger', type: 'button', 'aria-label': `Remove ${rule.name || 'indicator'}`, title: 'Remove', onclick: () => { s.indicators.splice(i, 1); render(); } }, icon('trash'))));
      return row;
    };

    /** Take up the document the server sent back in place of the one edited
     *  here, so the next edit starts from what is actually stored. */
    function adopt() {
      s = state.settings;
      if (!Array.isArray(s.indicators)) s.indicators = [];
    }

    async function save(btn) {
      for (const r of s.indicators) { if (!r.id) r.id = newId(); if (!String(r.name || '').trim()) r.name = 'Indicator'; }
      await saveSettings(btn);
      adopt();
      // Relight the header at once; otherwise the author of the rule waits
      // for the next configuration update to come down the stream.
      window.dispatchEvent(new CustomEvent('gw:indicators-changed'));
      render();
    }

    render();
    return wrap;
  }

  return { tabIndicators };
}

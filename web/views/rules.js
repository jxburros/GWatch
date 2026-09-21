// Notification rules (Settings › Rules): conditions across nodes and checks
// joined by all / any / at least N, with a list of the same actions
// triggers run. The editor, the list rows and the tab itself live here;
// the action editor is shared with triggers and endpoints (automation.js).

import { api } from '../api.js';
import { h, icon, clear, replace, field, textInput, numberInput, selectInput, checkbox, toggle, toast, confirmDialog, openModal, busy, relTimeEl, emptyState } from '../components.js';
import { actionEditor, actionTester, actionBadge, actionSummary, automationMeta, resultBox } from './automation.js';

export const JOINS = [
  { value: 'all', label: 'All conditions' },
  { value: 'any', label: 'Any condition' },
  { value: 'at_least', label: 'At least N conditions' },
];
export const RULE_STATUSES = [
  { value: 'down', label: 'Down' },
  { value: 'degraded', label: 'Degraded or worse' },
];
const RULE_PLACEHOLDERS = ['rule.name', 'rule.join', 'rule.met', 'rule.needed', 'rule.total', 'rule.conditions', 'rule.summary', 'message', 'status', 'event', 'ts', 'instance'];

/** "All 3 conditions", "Any of 2 conditions", "At least 2 of 3 conditions". */
export function joinLabel(r) {
  const n = (r.conditions || []).length;
  if (r.join === 'any') return `Any of ${n} condition${n === 1 ? '' : 's'}`;
  if (r.join === 'at_least') return `At least ${r.atLeast || 1} of ${n} conditions`;
  return `All ${n} condition${n === 1 ? '' : 's'}`;
}

/** One line per condition: "Pi-hole › DNS is down", "any check of NAS is degraded or worse". */
export function conditionLabels(r, nodes = []) {
  return (r.conditions || []).map((c) => {
    const status = c.status === 'degraded' ? 'degraded or worse' : 'down';
    if (c.checkId != null) {
      for (const n of nodes) {
        const ch = (n.checks || []).find((x) => Number(x.id) === Number(c.checkId));
        if (ch) return `${n.name} › ${ch.name} is ${status}`;
      }
      return `check ${c.checkId} is ${status}`;
    }
    const n = nodes.find((x) => Number(x.id) === Number(c.nodeId));
    return `any check of ${n ? n.name : `node ${c.nodeId}`} is ${status}`;
  });
}

/** "Met since 20 min ago", "Not met · last fired 2 h ago", "Not met". */
export function stateEl(r) {
  const st = r.state || {};
  const el = h('span', { class: 'rule-state' });
  if (st.met) el.append(h('span', { class: 'text-down' }, 'Met', st.since ? ' since ' : '', st.since ? relTimeEl(st.since) : ''));
  else el.append(h('span', { class: 'dim' }, 'Not met'));
  if (st.lastFiredAt) el.append(h('span', { class: 'dim' }, ' · last fired '), relTimeEl(st.lastFiredAt));
  return el;
}

/** One condition row of the editor: node, check (or any), status, remove. */
function conditionRow(c, { nodes, onRemove }) {
  const nodeSel = selectInput({ options: nodes.map((n) => ({ value: n.id, label: n.name })), value: c.nodeId ?? nodes[0]?.id ?? '', 'aria-label': 'Node' });
  const checkSel = selectInput({ options: [], 'aria-label': 'Check' });
  const statusSel = selectInput({ options: RULE_STATUSES, value: c.status || 'down', 'aria-label': 'Status' });
  const fillChecks = (keep) => {
    const n = nodes.find((x) => String(x.id) === nodeSel.value);
    clear(checkSel);
    checkSel.append(h('option', { value: '' }, 'Any check of this node'));
    for (const ch of n?.checks || []) checkSel.append(h('option', { value: ch.id }, ch.name));
    checkSel.value = keep && c.checkId != null ? String(c.checkId) : '';
    if (checkSel.value === '' && keep) c.checkId = null;
  };
  // A check condition names its check; the node is implied by it. Find the
  // node the stored check belongs to so the two selects agree.
  if (c.checkId != null) {
    const owner = nodes.find((n) => (n.checks || []).some((ch) => Number(ch.id) === Number(c.checkId)));
    if (owner) nodeSel.value = String(owner.id);
  }
  fillChecks(true);
  nodeSel.addEventListener('change', () => fillChecks(false));
  const row = h('div', { class: 'rule-cond-row', role: 'group', 'aria-label': 'Condition' },
    nodeSel, checkSel, statusSel,
    h('button', { class: 'btn btn-sm btn-ghost icon-btn', type: 'button', 'aria-label': 'Remove condition', onclick: () => onRemove(row) }, icon('x')));
  Object.defineProperty(row, 'value', { get: () => {
    const out = { kind: 'status', status: statusSel.value };
    if (checkSel.value) out.checkId = Number(checkSel.value); else out.nodeId = Number(nodeSel.value);
    return out;
  } });
  return row;
}

/** One action of the editor: the shared action editor in a card with a remove button and a tester. */
function actionCard(a, { nodes, defaultInterpreter, onRemove }) {
  const editor = actionEditor(a, { nodes, defaultInterpreter });
  const card = h('div', { class: 'rule-action', role: 'group', 'aria-label': 'Action' },
    h('div', { class: 'rule-action-head' }, h('span', { class: 'section-title', style: { marginBottom: 0 } }, 'Action'),
      h('button', { class: 'btn btn-sm btn-ghost icon-btn', type: 'button', 'aria-label': 'Remove action', onclick: () => onRemove(card) }, icon('x'))),
    editor,
    actionTester(() => editor.value));
  Object.defineProperty(card, 'value', { get: () => editor.value });
  return card;
}

/**
 * Opens the rule editor modal. Resolves with the saved rule or null.
 */
export function openRuleEditor(existing, { nodes = [] } = {}) {
  return new Promise(async (resolve) => {
    const meta = await automationMeta();
    const r = existing
      ? { ...existing, conditions: (existing.conditions || []).map((c) => ({ ...c })), actions: (existing.actions || []).map((a) => ({ ...a })) }
      : { name: '', enabled: true, join: 'all', atLeast: 2, conditions: [{ kind: 'status', nodeId: nodes[0]?.id ?? null, checkId: null, status: 'down' }], actions: [{ type: 'http' }], cooldownMinutes: 0, notifyCleared: false };
    let result = null;

    const name = textInput({ value: r.name, placeholder: 'e.g. Two of three DNS servers down' });
    const nameField = field({ label: 'Name', input: name });
    const enabled = toggle({ label: 'Enabled', checked: r.enabled !== false });
    const joinSel = selectInput({ options: JOINS, value: r.join || 'all', onchange: () => syncJoin() });
    const atLeast = numberInput({ value: r.atLeast || 2, min: 1, step: 1 });
    const atLeastField = field({ label: 'How many', input: atLeast, help: 'The rule is met when at least this many of the conditions hold at once.' });
    const joinField = field({ label: 'Met when', input: joinSel });
    const syncJoin = () => { atLeastField.hidden = joinSel.value !== 'at_least'; };
    syncJoin();

    const condList = h('div', { class: 'stack-sm' });
    const removeCond = (row) => { row.remove(); };
    const addCond = (c) => { condList.append(conditionRow(c, { nodes, onRemove: removeCond })); };
    for (const c of r.conditions) addCond(c);
    const condField = field({ label: 'Conditions', input: condList, help: '"Degraded or worse" also holds while the check is down. A check in maintenance or silenced does not count while that lasts.' });
    const addCondBtn = h('button', { class: 'btn btn-sm', type: 'button', onclick: () => addCond({ kind: 'status', nodeId: nodes[0]?.id ?? null, checkId: null, status: 'down' }) }, icon('plus'), 'Add condition');

    const actionList = h('div', { class: 'stack' });
    const removeAction = (card) => { card.remove(); };
    const addAction = (a) => { actionList.append(actionCard(a, { nodes, defaultInterpreter: meta.defaultInterpreter || 'sh', onRemove: removeAction })); };
    for (const a of r.actions) addAction(a);
    const actionsField = field({ label: 'Actions', input: actionList, help: 'Every action runs when the rule is met. Placeholders: ' + RULE_PLACEHOLDERS.map((p) => `{{${p}}}`).join(', ') + '. In the default Slack, Teams, ntfy and Pushover text {{node.name}} is the rule\'s name and {{status}} is "met" or "cleared".' });
    const addActionBtn = h('button', { class: 'btn btn-sm', type: 'button', onclick: () => addAction({ type: 'http' }) }, icon('plus'), 'Add action');

    const cooldown = numberInput({ value: r.cooldownMinutes || '', min: 0, placeholder: '0 = none' });
    const notifyCleared = checkbox({ label: 'Also notify when the rule clears', checked: !!r.notifyCleared });

    const form = h('form', { class: 'stack', onsubmit: (e) => { e.preventDefault(); submit(); } },
      h('div', { class: 'form-grid' }, nameField, joinField, atLeastField),
      condField, h('div', null, addCondBtn),
      actionsField, h('div', null, addActionBtn),
      h('div', { class: 'form-grid' },
        field({ label: 'Cooldown', input: h('div', { class: 'input-with-unit' }, cooldown, h('span', { class: 'unit' }, 'min')), help: 'Shortest time between two runs of the actions. The rule still changes state and is recorded in the meantime.' }),
        field({ label: 'When it clears', input: notifyCleared, help: 'Runs the same actions with {{event}} = rule_cleared once the conditions come apart again.' })),
      enabled,
    );
    const m = openModal({ title: existing ? 'Edit rule' : 'New rule', wide: true, body: form, footer: [
      h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, 'Cancel'),
      h('button', { class: 'btn btn-primary', type: 'button', onclick: () => submit() }, existing ? 'Save rule' : 'Create rule'),
    ], onClose: () => resolve(result) });

    async function submit() {
      const conditions = [...condList.children].map((row) => row.value);
      const actions = [...actionList.children].map((card) => card.value);
      const n = Number(atLeast.value);
      let bad = false;
      nameField.setError(name.value.trim() ? '' : 'A name is required.'); bad = bad || !name.value.trim();
      condField.setError(conditions.length ? '' : 'Add at least one condition.'); bad = bad || !conditions.length;
      const badCount = joinSel.value === 'at_least' && (!Number.isInteger(n) || n < 1 || n > conditions.length);
      atLeastField.setError(badCount ? `Pick a number between 1 and ${conditions.length}.` : ''); bad = bad || badCount;
      actionsField.setError(actions.length ? '' : 'Add at least one action.'); bad = bad || !actions.length;
      if (bad) { form.querySelector('.has-error input, .has-error select, .has-error button')?.focus(); return; }
      const payload = { ...r, name: name.value.trim(), enabled: enabled.input.checked, join: joinSel.value, atLeast: joinSel.value === 'at_least' ? n : 0, conditions, actions, cooldownMinutes: Number(cooldown.value) || 0, notifyCleared: notifyCleared.input.checked };
      delete payload.state;
      try {
        result = existing ? await api.put(`/api/rules/${existing.id}`, payload) : await api.post('/api/rules', payload);
        m.close();
        toast(existing ? 'Rule saved' : 'Rule created', { kind: 'success' });
      } catch (e) { toast(e.message, { kind: 'error' }); }
    }
    setTimeout(() => name.focus(), 50);
  });
}

/** One rule row for the list, with its state, a test button, edit and delete. */
export function ruleRow(r, { nodes = [], onChange } = {}) {
  const testBtn = h('button', { class: 'btn btn-sm admin-only', type: 'button', title: 'Run the actions once with sample values', onclick: async () => {
    const done = busy(testBtn, 'Sending…');
    try {
      const results = await api.post(`/api/rules/${r.id}/test`);
      const ok = results.filter((x) => x.ok).length;
      toast(ok === results.length ? `Test sent: ${ok} of ${results.length} action${results.length === 1 ? '' : 's'} ran` : `${results.length - ok} of ${results.length} actions failed`, { kind: ok === results.length ? 'success' : 'error' });
      replace(out, h('div', { class: 'stack-sm' }, results.map(resultBox)));
    } catch (e) { toast(e.message, { kind: 'error' }); }
    done();
  } }, icon('play'), 'Send a test');
  const out = h('div');
  return h('div', { class: 'trigger-row rule-row' },
    h('div', { class: 'admin-only' }, toggle({ ariaLabel: `${r.name} enabled`, checked: r.enabled !== false, onChange: async (v) => {
      const { state, ...rule } = r;
      try { await api.put(`/api/rules/${r.id}`, { ...rule, enabled: v }); onChange && onChange(); } catch (e) { toast(e.message, { kind: 'error' }); onChange && onChange(); }
    } })),
    h('div', { style: { minWidth: 0 } },
      h('div', { class: 't-name' }, r.name, ...(r.actions || []).map(actionBadge), r.enabled === false ? h('span', { class: 'tag' }, 'disabled') : null),
      h('div', { class: 't-sub' }, `${joinLabel(r)}: ${conditionLabels(r, nodes).join('; ')}${r.cooldownMinutes ? ` · cooldown ${r.cooldownMinutes} min` : ''}${r.notifyCleared ? ' · notifies when cleared' : ''}`),
      h('div', { class: 't-sub' }, (r.actions || []).map((a, i) => h('span', null, i ? ' · ' : '', actionSummary(a, nodes)))),
      h('div', { class: 't-sub' }, stateEl(r)),
      out,
    ),
    h('div', { class: 'btn-group' }, testBtn,
      h('button', { class: 'btn btn-sm admin-only', type: 'button', onclick: async () => { const saved = await openRuleEditor(r, { nodes }); if (saved) onChange && onChange(); } }, icon('edit'), 'Edit'),
      h('button', { class: 'btn btn-sm btn-danger icon-btn admin-only', type: 'button', 'aria-label': 'Delete rule', onclick: async () => {
        if (await confirmDialog({ title: `Delete rule "${r.name}"?`, confirmLabel: 'Delete', danger: true })) {
          try { await api.del(`/api/rules/${r.id}`); toast('Rule deleted', { kind: 'success' }); onChange && onChange(); } catch (e) { toast(e.message, { kind: 'error' }); }
        }
      } }, icon('trash'))),
  );
}

/**
 * The Settings › Rules tab. `isDestroyed` says whether the view has been torn
 * down since a load began; `onRefresh` receives the function that reloads the
 * list, for the live-update hook.
 */
export async function rulesPanel({ isDestroyed = () => false, onRefresh } = {}) {
  const wrap = h('div', { class: 'stack' });
  let nodes = [];
  const load = async () => {
    const [rules, ns] = await Promise.all([api.get('/api/rules').catch(() => []), api.get('/api/nodes').catch(() => [])]);
    if (isDestroyed()) return;
    nodes = ns || [];
    render(rules || []);
  };
  const render = (rules) => {
    clear(wrap);
    const card = h('section', { class: 'card' },
      h('div', { class: 'card-head' }, h('h2', null, 'Rules'),
        h('button', { class: 'btn btn-primary admin-only', type: 'button', disabled: !nodes.length, onclick: async () => { const saved = await openRuleEditor(null, { nodes }); if (saved) load(); } }, icon('plus'), 'New rule')),
      h('p', { class: 'lead' }, 'A rule looks at several checks at once — "two of my three DNS servers are down", "the gateway and the switch are both unreachable" — and runs its actions once when its conditions come together. Per-node alerts and triggers carry on as before; rules sit beside them.'));
    if (!nodes.length) card.append(emptyState({ icon: 'sliders', title: 'Add a node first', text: 'A rule needs at least one node or check to look at.', compact: true }));
    else if (!rules.length) card.append(emptyState({ icon: 'sliders', title: 'No rules yet', text: 'Create one, e.g. "when at least two of my DNS servers are down, send a Pushover notification".', compact: true }));
    for (const r of rules) card.append(ruleRow(r, { nodes, onChange: load }));
    wrap.append(card,
      h('section', { class: 'card' }, h('h2', null, 'How rules behave'),
        h('div', { class: 'stack-sm', style: { marginTop: '8px' } },
          h('p', { class: 'note' }, h('b', null, 'Conditions: '), 'a check condition holds while that check is at the status you picked; a node condition holds while any of the node\'s enabled checks is. "Degraded or worse" also holds while the check is down.'),
          h('p', { class: 'note' }, h('b', null, 'Maintenance and silence: '), 'a check under a maintenance window or silenced does not count towards a rule while that lasts, and counts again afterwards.'),
          h('p', { class: 'note' }, h('b', null, 'Firing: '), 'a rule fires once when it goes from not met to met, and again only after it has cleared and come back. The cooldown is the shortest time between two runs of the actions; a firing inside it is still recorded in the timeline, just without the actions.'),
          h('p', { class: 'note' }, h('b', null, 'History: '), 'every firing and clearing is in Incidents and the Audit tab (event types "Rule fired" and "Rule cleared") with the conditions that held at the time.'),
          h('p', { class: 'note' }, h('b', null, 'Not yet: '), 'hold timers ("for at least 5 minutes") and metric conditions ("disk above 90 %") are deliberately left for later.'))));
  };
  await load();
  if (onRefresh) onRefresh(load);
  return wrap;
}

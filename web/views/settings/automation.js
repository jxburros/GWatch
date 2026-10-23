// Settings › Automation: endpoints and every trigger.
//
// Part of settings.js, split out for #118: the code is the
// view's own, moved here unchanged inside create(shell), which hands it the
// view's shared state and helpers.

import { api } from '../../api.js';
import { h, icon, clear, confirmDialog, emptyState, banner } from '../../components.js';
import { openEndpointEditor, endpointRow, triggerRow, openTriggerEditor } from '../automation.js';

/** @param {Record<string, any>} shell the view's state and helpers */
export function create(shell) {
  const { state } = shell;

  async function tabAutomation() {
    const wrap = h('div', { class: 'stack' });
    let nodes = [];
    const load = async () => {
      const [eps, trs, ns] = await Promise.all([api.get('/api/endpoints').catch(() => []), api.get('/api/triggers').catch(() => []), api.get('/api/nodes').catch(() => [])]);
      if (state.destroyed) return;
      nodes = ns || [];
      render(eps || [], trs || []);
    };
    const render = (eps, trs) => {
      clear(wrap);
      // /hook/ URLs are not covered by the access password, so an endpoint
      // without a token is open to everyone who can reach this port.
      const open = (eps || []).filter((e) => !e.token);
      if (open.length) {
        wrap.append(banner('warn', h('span', null, h('b', null, open.length === 1 ? 'One custom endpoint has no token: ' : `${open.length} custom endpoints have no token: `),
          ...open.flatMap((e, i) => [i ? ', ' : '', h('code', null, `/hook/${e.slug}`)]),
          '. Anyone who can reach this computer on the network can call them.'), {
          actions: open.map((e) => h('button', { class: 'btn btn-sm admin-only', type: 'button', onclick: async () => { const saved = await openEndpointEditor(e, { nodes }); if (saved) load(); } }, icon('edit'), open.length === 1 ? 'Edit' : `Edit ${e.name}`)),
        }));
      }
      const epCard = h('section', { class: 'card' },
        h('div', { class: 'card-head' }, h('h2', null, 'Custom endpoints'),
          h('button', { class: 'btn btn-primary admin-only', type: 'button', onclick: async () => { const saved = await openEndpointEditor(null, { nodes }); if (saved) load(); } }, icon('plus'), 'New endpoint')),
        h('p', { class: 'lead' }, 'URLs other systems can call to make GWatch do something: run a node\'s checks after a reboot, run a script, call a webhook or pull a git repository. Each lives at ', h('code', null, '/hook/<name>'), '.'));
      if (!eps.length) epCard.append(emptyState({ icon: 'webhook', title: 'No endpoints yet', text: 'Create one and call its URL from a script, a router, Home Assistant, a CI job — anything that can make an HTTP request.', compact: true }));
      for (const e of eps) epCard.append(endpointRow(e, { nodes, onChange: load }));
      const trCard = h('section', { class: 'card' },
        h('div', { class: 'card-head' }, h('h2', null, 'Triggers on nodes'),
          nodes.length ? h('button', { class: 'btn admin-only', type: 'button', onclick: async () => {
            const sel = h('select', null, nodes.map((n) => h('option', { value: n.id }, n.name)));
            const ok = await confirmDialog({ title: 'New trigger', message: 'Which node should it watch?', confirmLabel: 'Continue', body: h('div', { class: 'field' }, sel) });
            if (!ok) return;
            const node = nodes.find((n) => String(n.id) === sel.value);
            const saved = await openTriggerEditor(null, { node, nodes });
            if (saved) load();
          } }, icon('plus'), 'New trigger') : null),
        h('p', { class: 'lead' }, 'Triggers run an action when something happens on a node. They are created on each node\'s page; this is the overview.'));
      if (!trs.length) trCard.append(emptyState({ icon: 'zap', title: 'No triggers yet', text: 'Open a node and add a trigger, e.g. "when Plex goes down, restart its container", "when the gateway recovers, post to Discord".', compact: true }));
      for (const t of trs) {
        const node = nodes.find((n) => n.id === t.nodeId) || { id: t.nodeId, name: `node ${t.nodeId}`, checks: [] };
        const row = triggerRow(t, { node, nodes, onChange: load });
        row.querySelector('.t-name')?.prepend(h('a', { href: `#/nodes/${t.nodeId}`, class: 'tag tag-group' }, node.name), ' ');
        trCard.append(row);
      }
      wrap.append(epCard, trCard,
        h('section', { class: 'card' }, h('h2', null, 'Notes'),
          h('div', { class: 'stack-sm u-mt-8' },
            h('p', { class: 'note' }, h('b', null, 'Placeholders: '), 'URL, body, headers, git arguments and script code may contain {{node.name}}, {{status}}, {{message}}, {{latencyMs}} and more. Scripts also get GWATCH_* environment variables.'),
            h('p', { class: 'note' }, h('b', null, 'Security: '), 'actions run on the computer that runs GWatch with its permissions. Protect endpoints with a token and set an access password before enabling remote access.'),
            h('p', { class: 'note' }, h('b', null, 'History: '), 'every run is recorded in the Audit tab (event types "Trigger" and "Endpoint") together with its output.'))));
    };
    await load();
    state.panelRefresh = load;
    return wrap;
  }

  return { tabAutomation };
}

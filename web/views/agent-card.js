// The agent card on a machine's node: everything about the agent that reports
// for it, in the place the machine already lives. It used to be a table under
// Settings › Hardware; a machine is a node, so this is where it is managed.
//
//   * who it is and whether it is reporting (and whether a newer agent exists);
//   * its token — readable here so it can be compared with the one in the
//     machine's agent.json, and replaceable by a rotation that the agent picks
//     up by itself, or by a "replace now" for a token that may have leaked;
//   * what it collects, chosen from what the machine says it has;
//   * whether it is a remote collector — running other nodes' network checks
//     from where it is (#123) — and how that is going;
//   * revoke and delete.
//
// GWatch never connects to the machine. Everything here is stored in GWatch and
// reaches the agent the next time the agent asks (docs/AGENT-DECISIONS.md, 12).

import { api } from '../api.js';
import {
  h, icon, replace, statusPill, relTimeEl, openModal, confirmDialog, toast, agentLogo, toggle,
} from '../components.js';
import { agentIsBehind } from '../fmt.js';
import { collectionPicker, describeSelection } from './agent-collect.js';

/** How often an agent asks GWatch for its settings, as it is worded to people. */
const CHECK_IN_WORDS = 'about every 15 minutes';

async function copyText(text, what) {
  try { await navigator.clipboard.writeText(text); toast(`${what} copied`, { kind: 'success' }); }
  catch { toast('Could not copy — select it and copy it by hand.', { kind: 'error' }); }
}

/**
 * agentCard(agent, { latest, releaseURL, onChange }) → element.
 * `onChange` is called after anything that changes the agent, with the fresh
 * agent where there is one, so the caller can redraw.
 */
export function agentCard(agent, { latest = null, releaseURL = '', onChange = () => {} } = {}) {
  const revoked = !!agent.revokedAt;
  const body = h('div', { class: 'stack' });
  const el = h('section', { class: 'card agent-card', 'aria-label': `Agent: ${agent.name}` },
    h('div', { class: 'card-head' }, h('h2', { class: 'card-title' }, icon('cpu'), 'GWatch agent'), statusLine(agent)),
    body);

  body.append(identity(agent, latest, releaseURL));
  if (revoked) {
    body.append(h('p', { class: 'note' }, 'This agent has been revoked: its token no longer works. Its registration is kept; delete it to remove that too. The node, and the history its hardware check recorded, stay with the node either way.'),
      h('div', { class: 'btn-group' }, h('button', { class: 'btn btn-danger', type: 'button', onclick: () => purge(agent, onChange) }, icon('trash'), 'Delete agent')));
    return el;
  }
  body.append(tokenSection(agent, onChange), collectSection(agent, onChange), collectorSection(agent, onChange), dangerSection(agent, onChange));
  return el;
}

function statusLine(agent) {
  if (agent.revokedAt) return statusPill('paused', { label: 'revoked' });
  if (!agent.enabled) return statusPill('paused', { label: 'not accepted' });
  if (!agent.lastSeenAt) return statusPill('unknown', { label: 'waiting for first reading' });
  return h('span', { class: 'dim small' }, 'last reading ', relTimeEl(agent.lastSeenAt));
}

function identity(agent, latest, releaseURL) {
  const rows = [
    agent.hostname ? ['Machine', `${agent.hostname}${agent.os ? ` · ${agent.os}/${agent.arch}` : ''}`] : null,
    agent.lastVersion ? ['Agent version', versionCell(agent.lastVersion, latest, releaseURL)] : null,
    agent.lastAddr ? ['Reports from', agent.lastAddr] : null,
    agent.checkedInAt ? ['Last asked for settings', relTimeEl(agent.checkedInAt)] : ['Last asked for settings', h('span', { class: 'dim' }, 'not yet')],
  ].filter(Boolean);
  const dl = h('dl', { class: 'kv' });
  for (const [k, v] of rows) dl.append(h('dt', null, k), h('dd', null, v));
  return h('div', { class: 'agent-intro' }, agentLogo(), dl);
}

// A quiet note, not an alarm: an agent one release behind is still doing its
// job, and on automatic updates it catches up by itself within hours.
function versionCell(running, latest, releaseURL) {
  if (!agentIsBehind(running, latest)) return running;
  return h('span', null, running, ' ',
    h('a', {
      class: 'pill pill-warn', href: releaseURL || 'https://github.com/jxburros/GWatch/releases', target: '_blank', rel: 'noreferrer noopener',
      title: `This machine runs gwatch-agent ${running}; ${latest} has been released. An agent left on automatic updates takes it by itself.`,
    }, icon('download'), `${latest} available`));
}

/* ---------- token ---------- */

function tokenSection(agent, onChange) {
  const shown = h('div', { class: 'stack-sm', 'aria-live': 'polite' });
  const reveal = h('button', { class: 'btn', type: 'button', onclick: () => showToken(agent, shown) }, icon('eye'), 'Show token');
  const buttons = [
    agent.tokenViewable ? reveal : null,
    h('button', { class: 'btn', type: 'button', onclick: () => rotate(agent, onChange) }, icon('refresh'), 'Rotate token…'),
  ];
  return h('div', { class: 'stack-sm', 'data-section': 'token' },
    h('div', { class: 'section-title' }, 'Token'),
    h('p', { class: 'note' }, 'The credential this machine uses to send its readings; it can do that one thing and nothing else. Compare it with the ',
      h('code', null, 'token'), ' in the machine’s ', h('code', null, 'agent.json'), ' (', h('code', null, 'gwatch-agent config path'), ' says where that is).'),
    h('div', { class: 'agent-token-row' }, h('code', { class: 'mono', 'data-token-prefix': '' }, `${agent.prefix}…`),
      agent.tokenViewable ? null : h('span', { class: 'dim small' }, 'This token was registered before GWatch kept a readable copy; rotate it to get one.')),
    agent.rotationPending
      ? h('div', { class: 'banner banner-info', 'data-rotation': 'pending' }, icon('info'),
          h('div', { class: 'u-flex-1' }, h('b', null, 'A new token is waiting. '),
            `The agent picks it up the next time it asks for settings (${CHECK_IN_WORDS}), saves it in its file and switches over; the old token stops working when the new one is first used.`),
          h('div', { class: 'btn-group' },
            h('button', { class: 'btn btn-sm', type: 'button', onclick: () => cancelRotation(agent, onChange) }, 'Cancel rotation')))
      : null,
    h('div', { class: 'btn-group' }, buttons),
    shown);
}

async function showToken(agent, host) {
  try {
    const res = await api.post(`/api/agents/${agent.id}/token`);
    replace(host, tokenBox('Current token', res.token), res.pending ? tokenBox('New token (waiting to be picked up)', res.pending) : null);
  } catch (e) { toast(e.message, { kind: 'error' }); }
}

function tokenBox(label, token) {
  return h('div', { class: 'stack-sm' },
    h('div', { class: 'dim small' }, label),
    h('div', { class: 'agent-token-box' }, h('code', { class: 'mono agent-token' }, token),
      h('button', { class: 'btn btn-sm', type: 'button', onclick: () => copyText(token, 'Token') }, icon('copy'), 'Copy')));
}

// Two ways to replace a token, which answer two different worries. Offered side
// by side, in words, because the wrong one for the situation is easy to pick.
async function rotate(agent, onChange) {
  const m = openModal({
    title: `Rotate the token for ${agent.name}`,
    wide: true,
    body: h('div', { class: 'stack' },
      h('div', { class: 'stack-sm' },
        h('b', null, 'Rotate (recommended)'),
        h('p', { class: 'note' }, `GWatch makes a new token and the old one keeps working until the agent has picked up the new one — which it does by itself the next time it asks for settings (${CHECK_IN_WORDS}), saving it in its agent.json. Nothing to copy, no gap in readings. The new token is delivered to whoever holds the old one, so if the old one may have leaked, use the second option.`),
        h('button', { class: 'btn btn-primary', type: 'button', onclick: async () => {
          try { await api.post(`/api/agents/${agent.id}/rotate`, {}); m.close(); toast('A new token is waiting for the agent to pick up', { kind: 'success' }); onChange(); } catch (e) { toast(e.message, { kind: 'error' }); }
        } }, icon('refresh'), 'Rotate and let the agent pick it up')),
      h('hr'),
      h('div', { class: 'stack-sm' },
        h('b', null, 'Replace now (if the token may have leaked)'),
        h('p', { class: 'note' }, 'The old token stops working immediately and nothing is sent to the machine. You put the new token on it yourself — in its agent.json, then restart the agent. Readings stop until you have.'),
        h('button', { class: 'btn btn-danger', type: 'button', onclick: async () => {
          try {
            const res = await api.post(`/api/agents/${agent.id}/rotate`, { retireOld: true });
            m.close();
            showReplacement(agent, res.token);
            onChange();
          } catch (e) { toast(e.message, { kind: 'error' }); }
        } }, 'Replace now and show me the new token'))),
    footer: h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, 'Cancel'),
  });
}

function showReplacement(agent, token) {
  const m = openModal({
    title: 'Put the new token on the machine',
    wide: true,
    body: h('div', { class: 'stack' },
      h('p', null, `The old token for ${agent.name} no longer works. Open the machine’s configuration file, replace the value of `, h('code', null, 'token'), ' with this, and restart the agent:'),
      tokenBox('New token', token),
      h('p', { class: 'note' }, 'Find the file with ', h('code', null, 'gwatch-agent config path'), '; restart with ', h('code', null, 'gwatch-agent restart'), '. It can be shown again here later.')),
    footer: h('button', { class: 'btn btn-primary', type: 'button', onclick: () => m.close() }, 'Done'),
  });
  return m;
}

async function cancelRotation(agent, onChange) {
  try { await api.post(`/api/agents/${agent.id}/rotate/cancel`, {}); toast('Rotation cancelled', { kind: 'success' }); onChange(); } catch (e) { toast(e.message, { kind: 'error' }); }
}

/* ---------- what it collects ---------- */

function collectSection(agent, onChange) {
  const picker = collectionPicker({ value: agent.collect || {}, inventory: agent.inventory || null });
  const save = h('button', { class: 'btn btn-primary', type: 'button', onclick: async () => {
    const problem = picker.problem();
    if (problem) { toast(problem, { kind: 'error' }); return; }
    save.disabled = true;
    try {
      await api.put(`/api/agents/${agent.id}/collect`, picker.value());
      toast(`Saved. ${agent.name} applies this the next time it asks for settings (${CHECK_IN_WORDS}).`, { kind: 'success' });
      onChange();
    } catch (e) { toast(e.message, { kind: 'error' }); save.disabled = false; }
  } }, icon('save'), 'Save what to collect');
  return h('div', { class: 'stack-sm', 'data-section': 'collect' },
    h('div', { class: 'section-title' }, 'What it collects'),
    h('p', { class: 'note' }, 'Choose which readings this machine sends. The agent applies your choice inside whatever limit is set in its own file: you can narrow what it reports, never widen it. A reading it stops sending stops appearing in checks, charts and alerts.'),
    picker.el,
    h('div', { class: 'btn-group' }, save),
    h('p', { class: 'note' }, `Currently: ${summaryOf(agent)}.`));
}

function summaryOf(agent) { return describeSelection(agent.collect || {}); }

/* ---------- remote collector ---------- */

/**
 * What the collector status means, in words. `collectorStatus` comes from the
 * server: '' (off), waiting, idle, running or unreachable.
 */
export function collectorWords(agent) {
  const n = agent.collectorChecks || 0;
  const checks = `${n} check${n === 1 ? '' : 's'}`;
  switch (agent.collectorStatus) {
    case 'waiting': return { pill: 'unknown', label: 'waiting for the machine', text: `Turned on here, but the machine has not allowed it: its agent.json needs "collector": true (then restart the agent). Until then it is given no checks${n ? `, and the ${checks} assigned to it are unknown` : ''}.` };
    case 'idle': return { pill: 'paused', label: 'no checks yet', text: 'Ready. Choose this machine under "Check from" on a node to have it run that node’s network checks.' };
    case 'running': return { pill: 'up', label: `running ${checks}`, text: `Runs ${checks} for GWatch from where it is.` };
    case 'unreachable': return { pill: 'down', label: 'unreachable', text: `Has stopped sending results. Its ${checks} are unknown until it does; one alert was raised for the collector rather than one per check.` };
    default: return { pill: 'paused', label: 'off', text: '' };
  }
}

function collectorSection(agent, onChange) {
  const words = collectorWords(agent);
  const sw = toggle({
    label: 'Use this machine as a remote collector',
    checked: !!agent.collector,
    onChange: async (on) => {
      sw.input.disabled = true;
      try {
        await api.put(`/api/agents/${agent.id}/collector`, { enabled: on });
        toast(on ? `${agent.name} can now run checks for GWatch, once its own agent.json allows it.` : `${agent.name} stops running checks at its next check-in.`, { kind: 'success' });
        onChange();
      } catch (e) { toast(e.message, { kind: 'error' }); sw.input.checked = !on; sw.input.disabled = false; }
    },
  });
  return h('div', { class: 'stack-sm', 'data-section': 'collector' },
    h('div', { class: 'section-title' }, 'Remote collector'),
    h('p', { class: 'note' }, 'A collector runs the ping, TCP, HTTP, DNS and SNMP checks of the nodes you point at it — useful for a second site this server cannot reach. It needs two switches: this one, and ',
      h('code', null, '"collector": true'), ' in the machine’s own ', h('code', null, 'agent.json'), '. GWatch never connects to it; it picks up its checks when it asks for settings, and the checks’ SNMP communities and HTTP headers are sent to it then.'),
    sw,
    agent.collector
      ? h('div', { class: 'row', 'data-collector-status': agent.collectorStatus || 'off', style: { gap: '8px', alignItems: 'center', flexWrap: 'wrap' } },
          statusPill(words.pill, { label: words.label }),
          agent.collectorSeenAt ? h('span', { class: 'dim small' }, 'last results ', relTimeEl(agent.collectorSeenAt)) : null,
          h('span', { class: 'note' }, words.text))
      : (agent.collectorAllowed ? h('p', { class: 'note' }, 'The machine allows collecting in its own settings; turn it on here to use it.') : null));
}

/* ---------- revoke and delete ---------- */

function dangerSection(agent, onChange) {
  return h('div', { class: 'stack-sm', 'data-section': 'danger' },
    h('div', { class: 'section-title' }, 'Stop accepting this agent'),
    h('p', { class: 'note' }, 'Revoking makes the token stop working at once. What the machine\u2019s hardware check recorded stays on this node.'),
    h('div', { class: 'btn-group' }, h('button', { class: 'btn', type: 'button', onclick: () => revoke(agent, onChange) }, 'Revoke token')));
}

async function revoke(agent, onChange) {
  const ok = await confirmDialog({
    title: `Revoke ${agent.name}?`,
    message: 'The agent on that machine stops being accepted immediately. What its hardware check recorded stays on the node.',
    confirmLabel: 'Revoke token', danger: true,
  });
  if (!ok) return;
  try { await api.del(`/api/agents/${agent.id}`); toast('Agent revoked', { kind: 'success' }); onChange(); } catch (e) { toast(e.message, { kind: 'error' }); }
}

async function purge(agent, onChange) {
  const ok = await confirmDialog({
    title: `Delete the agent for ${agent.name}?`,
    message: 'The agent\u2019s registration is removed. The node and the history its hardware check recorded are not; delete the node to remove those.',
    confirmLabel: 'Delete agent', danger: true,
  });
  if (!ok) return;
  try { await api.del(`/api/agents/${agent.id}?purge=1`); toast('Agent deleted', { kind: 'success' }); onChange(); } catch (e) { toast(e.message, { kind: 'error' }); }
}


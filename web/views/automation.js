// Automation building blocks shared by the node page (triggers) and Settings
// (custom endpoints): the action editor (HTTP / git / script / run node), the
// trigger editor and list rows, with placeholder help and inline testing.

import { api } from '../api.js';
import { h, icon, clear, replace, field, textInput, numberInput, textarea, selectInput, checkbox, toggle, toast, confirmDialog, openModal, busy, relTimeEl } from '../components.js';
import { relTime } from '../fmt.js';

export const ACTION_TYPES = [
  { value: 'http', label: 'HTTP request', icon: 'webhook', desc: 'Call a webhook or any URL (GET / POST …) with an optional body.' },
  { value: 'slack', label: 'Slack', icon: 'hash', desc: 'Post a message to a Slack incoming webhook.' },
  { value: 'teams', label: 'Microsoft Teams', icon: 'layers', desc: 'Post an Adaptive Card to a Teams (or Power Automate) webhook.' },
  { value: 'ntfy', label: 'ntfy', icon: 'bell', desc: 'Publish a push notification to an ntfy.sh (or self-hosted) topic.' },
  { value: 'pushover', label: 'Pushover', icon: 'zap', desc: 'Send a push notification through Pushover.' },
  { value: 'git', label: 'Git command', icon: 'git', desc: 'Run git in a repository on this computer, e.g. pull --ff-only.' },
  { value: 'script', label: 'Custom code', icon: 'code', desc: 'Run a script with sh, bash, PowerShell, cmd, Python, Node or any command.' },
  { value: 'run_node', label: 'Run a node now', icon: 'play', desc: 'Run every check of a node immediately.' },
];
const NOTIFY_PRIORITIES = [
  { value: '', label: 'Default' }, { value: 'min', label: 'Min' }, { value: 'low', label: 'Low' }, { value: 'default', label: 'Default (explicit)' }, { value: 'high', label: 'High' }, { value: 'urgent', label: 'Urgent' },
];
const PUSHOVER_PRIORITIES = [
  { value: '', label: 'Normal (0)' }, { value: '-2', label: 'Lowest (-2)' }, { value: '-1', label: 'Low (-1)' }, { value: '0', label: 'Normal (0)' }, { value: '1', label: 'High (1)' }, { value: '2', label: 'Emergency (2)' },
];
export const CONDITIONS = [
  { value: 'down', label: 'Goes down' }, { value: 'recovered', label: 'Recovers' }, { value: 'degraded', label: 'Becomes degraded' }, { value: 'warning_cleared', label: 'Warning cleared' },
  { value: 'cert_warning', label: 'Certificate warning' }, { value: 'content_changed', label: 'Response changed' }, { value: 'affected_by_parent', label: 'Affected by dependency' },
  { value: 'status_change', label: 'Any status change' }, { value: 'any_failure', label: 'Every failed run' }, { value: 'any_success', label: 'Every successful run' }, { value: 'latency_over', label: 'Latency above…' }, { value: 'metric_over', label: 'A metric above…' },
];
export const INTERPRETERS = [
  { value: 'sh', label: 'sh' }, { value: 'bash', label: 'bash' }, { value: 'powershell', label: 'PowerShell (pwsh / powershell)' }, { value: 'cmd', label: 'cmd.exe' }, { value: 'python', label: 'Python' }, { value: 'node', label: 'Node.js' }, { value: 'custom', label: 'Custom command…' },
];
const PLACEHOLDERS = ['node.name', 'node.host', 'node.group', 'node.groups', 'check.name', 'check.type', 'target', 'status', 'prev_status', 'message', 'error', 'latencyMs', 'lossPct', 'statusCode', 'failures', 'event', 'ts', 'instance', 'metric', 'metric.label', 'metric.value', 'metric.status', 'metrics.<key>', 'body', 'query.<name>'];

let metaCache = null;
export async function automationMeta() {
  if (!metaCache) { try { metaCache = await api.get('/api/automation/meta'); } catch { metaCache = { defaultInterpreter: 'sh', interpreters: INTERPRETERS.map((i) => i.value) }; } }
  return metaCache;
}

export function actionLabel(type) { return (ACTION_TYPES.find((a) => a.value === type) || { label: type }).label; }
export function actionBadge(a) {
  const t = ACTION_TYPES.find((x) => x.value === a?.type) || { icon: 'zap', label: a?.type || 'action' };
  return h('span', { class: `action-badge a-${a?.type || ''}` }, icon(t.icon), t.label);
}
export function actionSummary(a, nodes = []) {
  if (!a) return '';
  switch (a.type) {
    case 'http': return `${a.method || (a.body ? 'POST' : 'GET')} ${a.url || ''}`;
    case 'slack': return `Slack → ${a.webhookUrl || '?'}`;
    case 'teams': return `Teams → ${a.webhookUrl || '?'}`;
    case 'ntfy': return `ntfy → ${(a.server || 'https://ntfy.sh').replace(/\/$/, '')}/${a.topic || '?'}`;
    case 'pushover': return `Pushover → user ${a.userKey || '?'}`;
    case 'git': return `git ${a.gitArgs || ''} in ${a.repo || '?'}`;
    case 'script': return `${a.interpreter || 'script'} · ${(a.code || '').split('\n')[0].slice(0, 60)}`;
    case 'run_node': { const n = nodes.find((x) => Number(x.id) === Number(a.nodeId)); return `run checks of ${n ? n.name : `node ${a.nodeId ?? '?'}`}`; }
    default: return a.type;
  }
}

function placeholderHelp(extra = []) {
  const list = h('div', { class: 'placeholder-list' });
  for (const p of [...PLACEHOLDERS, ...extra]) list.append(h('code', { title: 'Click to copy', onclick: () => { navigator.clipboard?.writeText(`{{${p}}}`); toast(`Copied {{${p}}}`, { kind: 'info', timeout: 1500 }); } }, `{{${p}}}`));
  return h('details', { class: 'collapsible' }, h('summary', null, icon('chevronRight'), 'Placeholders you can use'), h('div', { class: 'stack-sm', style: { paddingTop: '6px' } }, h('p', { class: 'note' }, 'Placeholders are replaced when the action runs. Scripts also receive them as environment variables (GWATCH_NODE_NAME, GWATCH_STATUS …) and the request body on stdin for endpoints.'),
    h('p', { class: 'note' }, 'Inside script code a placeholder does not become raw text: it becomes a safe reference to its value — ', h('code', null, '"${GWATCH_MESSAGE}"'), ' in sh/bash, ', h('code', null, '${env:GWATCH_MESSAGE}'), ' in PowerShell, a string literal in Python and Node. Values can contain anything the sender chose, so they are never allowed to be read as code. The GWATCH_* environment variables are always available and are the clearest thing to use.'),
    list));
}

/**
 * Action editor. Returns an element with `.value` → action object.
 */
export function actionEditor(action = {}, { nodes = [], defaultInterpreter = 'sh', nodeId = null } = {}) {
  const a = { type: 'http', method: '', url: '', headers: {}, body: '', ignoreTlsErrors: false, expectedStatus: '', repo: '', gitArgs: '', interpreter: defaultInterpreter, command: '', code: '', workDir: '', allowUntrustedInput: false, nodeId: nodeId, timeoutSeconds: 30, webhookUrl: '', title: '', message: '', topic: '', server: '', priority: '', tags: '', token: '', userKey: '', ...(action || {}) };
  if (!a.interpreter) a.interpreter = defaultInterpreter;
  const typeSel = selectInput({ options: ACTION_TYPES.map((t) => ({ value: t.value, label: t.label })), value: a.type, onchange: () => { a.type = typeSel.value; renderType(); } });
  const typeDesc = h('div', { class: 'help' });
  const typeArea = h('div', { class: 'stack-sm' });
  const timeout = numberInput({ value: a.timeoutSeconds || 30, min: 1, max: 600, oninput: () => { a.timeoutSeconds = Number(timeout.value) || 30; } });

  // http
  const method = selectInput({ options: [{ value: '', label: 'Auto (POST with body, else GET)' }, 'GET', 'POST', 'PUT', 'PATCH', 'DELETE'], value: a.method || '', onchange: () => { a.method = method.value; } });
  const url = textInput({ value: a.url || '', placeholder: 'https://example.com/webhook/{{node.name}}', oninput: () => { a.url = url.value; } });
  const body = textarea({ class: 'code', value: a.body || '', rows: 4, placeholder: '{"text": "{{node.name}} is {{status}}: {{message}}"}', oninput: () => { a.body = body.value; } });
  const expected = textInput({ value: a.expectedStatus || '', placeholder: '200-399', oninput: () => { a.expectedStatus = expected.value; } });
  const tls = checkbox({ label: 'Ignore TLS certificate errors', checked: !!a.ignoreTlsErrors, onChange: (v) => { a.ignoreTlsErrors = v; } });
  const headersWrap = h('div', { class: 'kv-rows' });
  const renderHeaders = () => {
    clear(headersWrap);
    const entries = Object.entries(a.headers || {});
    entries.forEach(([k, v]) => {
      const kIn = textInput({ value: k, placeholder: 'Header', 'aria-label': 'Header name' });
      const vIn = textInput({ value: v, placeholder: 'Value', 'aria-label': 'Header value' });
      const commit = () => { const next = {}; headersWrap.querySelectorAll('.kv-row').forEach((r) => { const [x, y] = r.querySelectorAll('input'); if (x.value.trim()) next[x.value.trim()] = y.value; }); a.headers = next; };
      kIn.addEventListener('change', commit); vIn.addEventListener('input', commit);
      headersWrap.append(h('div', { class: 'kv-row' }, kIn, vIn, h('button', { class: 'btn btn-sm icon-btn', type: 'button', 'aria-label': 'Remove header', onclick: () => { const nh = { ...a.headers }; delete nh[k]; a.headers = nh; renderHeaders(); } }, icon('x'))));
    });
    headersWrap.append(h('div', null, h('button', { class: 'btn btn-sm', type: 'button', onclick: () => { a.headers = { ...(a.headers || {}), [`Header-${entries.length + 1}`]: '' }; renderHeaders(); } }, icon('plus'), 'Add header')));
  };
  renderHeaders();

  // slack / teams
  const webhookUrl = textInput({ value: a.webhookUrl || '', class: 'mono', placeholder: 'https://hooks.slack.com/services/…', oninput: () => { a.webhookUrl = webhookUrl.value; } });
  const notifyTitle = textInput({ value: a.title || '', placeholder: 'GWatch {{instance}}', oninput: () => { a.title = notifyTitle.value; } });
  const notifyMessage = textarea({ class: 'code', value: a.message || '', rows: 3, placeholder: '{{node.name}} is {{status}}: {{message}}', oninput: () => { a.message = notifyMessage.value; } });

  // ntfy
  const ntfyServer = textInput({ value: a.server || '', class: 'mono', placeholder: 'https://ntfy.sh (default)', oninput: () => { a.server = ntfyServer.value; } });
  const ntfyTopic = textInput({ value: a.topic || '', class: 'mono', placeholder: 'gwatch-alerts', oninput: () => { a.topic = ntfyTopic.value; } });
  const ntfyPriority = selectInput({ options: NOTIFY_PRIORITIES, value: a.priority || '', onchange: () => { a.priority = ntfyPriority.value; } });
  const ntfyTags = textInput({ value: a.tags || '', placeholder: 'warning,{{event}}', oninput: () => { a.tags = ntfyTags.value; } });
  const ntfyToken = textInput({ value: a.token || '', class: 'mono', placeholder: 'Optional access token', oninput: () => { a.token = ntfyToken.value; } });

  // pushover
  const pushoverToken = textInput({ value: a.token || '', class: 'mono', placeholder: 'Application token', oninput: () => { a.token = pushoverToken.value; } });
  const pushoverUser = textInput({ value: a.userKey || '', class: 'mono', placeholder: 'User key', oninput: () => { a.userKey = pushoverUser.value; } });
  const pushoverPriority = selectInput({ options: PUSHOVER_PRIORITIES, value: a.priority || '', onchange: () => { a.priority = pushoverPriority.value; } });

  // git
  const repo = textInput({ value: a.repo || '', class: 'mono', placeholder: 'C:\\repos\\homelab or /srv/homelab', oninput: () => { a.repo = repo.value; } });
  const gitArgs = textInput({ value: a.gitArgs || '', class: 'mono', placeholder: 'pull --ff-only', oninput: () => { a.gitArgs = gitArgs.value; } });

  // script
  const interp = selectInput({ options: INTERPRETERS, value: a.interpreter || defaultInterpreter, onchange: () => { a.interpreter = interp.value; cmdField.hidden = interp.value !== 'custom'; untrustedField.hidden = interp.value !== 'custom'; } });
  const cmd = textInput({ value: a.command || '', class: 'mono', placeholder: 'e.g. perl {{file}}  (the script path is appended when {{file}} is absent)', oninput: () => { a.command = cmd.value; } });
  const cmdField = field({ label: 'Command line', input: cmd }); cmdField.hidden = (a.interpreter || defaultInterpreter) !== 'custom';
  const code = textarea({ class: 'code', value: a.code || '', rows: 8, placeholder: '# your code here\necho "$GWATCH_NODE_NAME is {{status}}"', spellcheck: 'false', oninput: () => { a.code = code.value; } });
  // Tab indents rather than leaving the box, so the box needs a way out:
  // Shift+Tab always leaves, and Escape arms the next Tab to leave too. The
  // Help page's Keyboard topic documents both. The first Escape is kept from
  // the dialog around the editor (which would otherwise close on it); a
  // second Escape, with Tab already armed, reaches the dialog as usual.
  let tabLeaves = false;
  code.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') { if (!tabLeaves) { tabLeaves = true; e.preventDefault(); e.stopPropagation(); } return; }
    if (e.key === 'Tab') {
      if (e.shiftKey || tabLeaves) { tabLeaves = false; return; }
      e.preventDefault(); const s = code.selectionStart; code.setRangeText('  ', s, code.selectionEnd, 'end'); a.code = code.value;
      return;
    }
    tabLeaves = false;
  });
  code.addEventListener('blur', () => { tabLeaves = false; });
  const workDir = textInput({ value: a.workDir || '', class: 'mono', placeholder: 'Optional working directory', oninput: () => { a.workDir = workDir.value; } });
  const untrusted = checkbox({ label: 'This script may run untrusted input (I understand {{body}}, {{message}} and {{query.*}} can contain anything the sender chooses)', checked: !!a.allowUntrustedInput, onChange: (v) => { a.allowUntrustedInput = v; } });
  const untrustedField = h('div', { class: 'stack-sm' }, untrusted, h('p', { class: 'note' }, 'With a custom interpreter GWatch cannot know how to quote a value, so placeholders in the code are refused unless you tick this. Using the GWATCH_* environment variables instead is safer and needs no acknowledgement.'));
  untrustedField.hidden = (a.interpreter || defaultInterpreter) !== 'custom';

  // run_node
  const nodeSel = selectInput({ options: nodes.map((n) => ({ value: n.id, label: n.name })), value: a.nodeId ?? nodes[0]?.id ?? '', onchange: () => { a.nodeId = nodeSel.value ? Number(nodeSel.value) : null; } });

  function renderType() {
    clear(typeArea);
    const t = ACTION_TYPES.find((x) => x.value === a.type) || ACTION_TYPES[0];
    typeDesc.textContent = t.desc;
    switch (a.type) {
      case 'http':
        typeArea.append(h('div', { class: 'form-grid' }, field({ label: 'URL', input: url, cls: 'span-2' }), field({ label: 'Method', input: method }), field({ label: 'Expected status', input: expected, help: 'A code, range or list. Anything else counts as a failed run.' }), field({ label: 'Request headers', input: headersWrap, cls: 'span-2' }), field({ label: 'Body', input: body, cls: 'span-2', help: 'JSON bodies get Content-Type: application/json automatically.' }), h('div', { class: 'span-2' }, tls)));
        break;
      case 'slack':
      case 'teams':
        typeArea.append(h('div', { class: 'form-grid' },
          field({ label: 'Webhook URL', input: webhookUrl, cls: 'span-2', help: a.type === 'slack' ? 'A Slack incoming webhook URL (https://hooks.slack.com/services/…).' : 'A Teams (or Power Automate workflow) webhook URL.' }),
          field({ label: 'Title', input: notifyTitle, help: a.type === 'teams' ? 'Used as the Adaptive Card heading. Leave blank for "GWatch {{instance}}".' : 'Not sent to Slack, kept for consistency with other channels.' }),
          field({ label: 'Message', input: notifyMessage, cls: 'span-2', help: 'Leave blank for "{{node.name}} is {{status}}: {{message}}". Use Test this action to send a test message.' }),
        ));
        break;
      case 'ntfy':
        typeArea.append(h('div', { class: 'form-grid' },
          field({ label: 'Server', input: ntfyServer, help: 'Leave blank for https://ntfy.sh.' }),
          field({ label: 'Topic', input: ntfyTopic, help: 'Letters, digits, - and _ only.' }),
          field({ label: 'Priority', input: ntfyPriority }),
          field({ label: 'Tags', input: ntfyTags, help: 'Comma-separated ntfy tags/emoji shortcodes.' }),
          field({ label: 'Access token', input: ntfyToken, help: 'Optional, for a protected topic.' }),
          field({ label: 'Title', input: notifyTitle }),
          field({ label: 'Message', input: notifyMessage, cls: 'span-2', help: 'Leave blank for "{{node.name}} is {{status}}: {{message}}". Use Test this action to send a test message.' }),
        ));
        break;
      case 'pushover':
        typeArea.append(h('div', { class: 'form-grid' },
          field({ label: 'Application token', input: pushoverToken }),
          field({ label: 'User key', input: pushoverUser }),
          field({ label: 'Priority', input: pushoverPriority }),
          field({ label: 'Title', input: notifyTitle }),
          field({ label: 'Message', input: notifyMessage, cls: 'span-2', help: 'Leave blank for "{{node.name}} is {{status}}: {{message}}". Use Test this action to send a test message.' }),
        ));
        break;
      case 'git':
        typeArea.append(h('div', { class: 'form-grid' }, field({ label: 'Repository directory', input: repo, cls: 'span-2', help: 'The command runs in this directory on the computer running GWatch.' }), field({ label: 'Git arguments', input: gitArgs, cls: 'span-2', help: 'Everything after "git". Placeholders are expanded, e.g. commit -am "{{node.name}} {{status}}".' })));
        break;
      case 'script':
        typeArea.append(h('div', { class: 'form-grid' }, field({ label: 'Interpreter', input: interp }), field({ label: 'Working directory', input: workDir }), h('div', { class: 'span-2' }, cmdField), field({ label: 'Code', input: code, cls: 'span-2', help: 'Tab indents by two spaces; press Esc then Tab, or Shift+Tab, to leave the box.' }), h('div', { class: 'span-2' }, untrustedField)));
        break;
      case 'run_node':
        typeArea.append(field({ label: 'Node', input: nodeSel }));
        if (nodeSel.value && a.nodeId == null) a.nodeId = Number(nodeSel.value);
        break;
    }
  }
  renderType();
  const wrap = h('div', { class: 'stack-sm' },
    h('div', { class: 'form-grid' }, field({ label: 'Action', input: typeSel, help: typeDesc }), field({ label: 'Timeout', input: h('div', { class: 'input-with-unit' }, timeout, h('span', { class: 'unit' }, 'seconds')) })),
    typeArea,
    placeholderHelp(),
  );
  Object.defineProperty(wrap, 'value', { get: () => {
    const out = { type: a.type, timeoutSeconds: Number(a.timeoutSeconds) || 30 };
    if (a.type === 'http') Object.assign(out, { method: a.method || '', url: a.url.trim(), headers: a.headers || {}, body: a.body || '', expectedStatus: a.expectedStatus || '', ignoreTlsErrors: !!a.ignoreTlsErrors });
    if (a.type === 'slack' || a.type === 'teams') Object.assign(out, { webhookUrl: (a.webhookUrl || '').trim(), title: a.title || '', message: a.message || '' });
    if (a.type === 'ntfy') Object.assign(out, { server: (a.server || '').trim(), topic: (a.topic || '').trim(), priority: a.priority || '', tags: a.tags || '', token: a.token || '', title: a.title || '', message: a.message || '' });
    if (a.type === 'pushover') Object.assign(out, { token: (a.token || '').trim(), userKey: (a.userKey || '').trim(), priority: a.priority || '', title: a.title || '', message: a.message || '' });
    if (a.type === 'git') Object.assign(out, { repo: a.repo.trim(), gitArgs: a.gitArgs.trim() });
    if (a.type === 'script') Object.assign(out, { interpreter: a.interpreter || defaultInterpreter, command: a.command || '', code: a.code || '', workDir: a.workDir || '', allowUntrustedInput: !!a.allowUntrustedInput });
    if (a.type === 'run_node') Object.assign(out, { nodeId: a.nodeId != null ? Number(a.nodeId) : null });
    return out;
  } });
  return wrap;
}

export function resultBox(res) {
  if (!res) return null;
  const lines = [];
  lines.push(`${res.ok ? 'OK' : 'FAILED'}${res.statusCode ? ` · HTTP ${res.statusCode}` : ''} · ${res.durationMs ?? 0} ms`);
  if (res.error) lines.push(`Error: ${res.error}`);
  if (res.output) lines.push(res.output);
  return h('div', { class: `result-box ${res.ok ? 'ok' : 'failed'}` }, lines.join('\n'));
}

/** "Test this action" button + result area. */
export function actionTester(getAction, { nodeId = null } = {}) {
  const out = h('div');
  const btn = h('button', { class: 'btn btn-sm', type: 'button', onclick: async () => {
    const done = busy(btn, 'Running…');
    try { const res = await api.post('/api/actions/test', { action: getAction(), nodeId: nodeId ? Number(nodeId) : null }); replace(out, resultBox(res)); }
    catch (e) { replace(out, h('div', { class: 'result-box failed' }, e.message)); }
    done();
  } }, icon('play'), 'Test this action');
  return h('div', { class: 'stack-sm' }, h('div', null, btn), out);
}

/**
 * Opens the trigger editor modal. Resolves with the saved trigger or null.
 */
export function openTriggerEditor(existing, { node, nodes = [] }) {
  return new Promise(async (resolve) => {
    const meta = await automationMeta();
    const t = existing ? { ...existing, on: [...(existing.on || [])], action: { ...(existing.action || {}) } } : { nodeId: node.id, name: '', description: '', enabled: true, on: ['down'], checkId: null, latencyOverMs: 0, metric: '', metricOver: 0, cooldownMinutes: 0, action: { type: 'http' } };
    let result = null;
    const name = textInput({ value: t.name, placeholder: 'e.g. Restart Plex container, Post to Discord, Pull config repo' });
    const desc = textInput({ value: t.description || '', placeholder: 'Optional note' });
    const enabled = toggle({ label: 'Enabled', checked: t.enabled !== false });
    const conds = h('div', { class: 'cond-chips', role: 'group', 'aria-label': 'Conditions' });
    const latency = numberInput({ value: t.latencyOverMs || '', min: 1, placeholder: 'ms' });
    const latencyField = field({ label: 'Latency threshold', input: h('div', { class: 'input-with-unit' }, latency, h('span', { class: 'unit' }, 'ms')) });
    // metric_over watches one named metric — a hardware check's "cpu" or
    // "disk:/srv", an SNMP check's OID name — against a number of its own.
    const metricKey = textInput({ value: t.metric || '', placeholder: 'e.g. cpu, disk:/srv, net:eth0.rx', 'aria-label': 'Metric key' });
    const metricOver = numberInput({ value: t.metricOver || '', step: 'any', placeholder: 'value', 'aria-label': 'Metric threshold' });
    const metricField = field({ label: 'Metric above', input: h('div', { class: 'row', style: { gap: '6px' } }, metricKey, metricOver), help: 'The key as it appears in the check’s last result (Inspect last result › Metrics).' });
    const syncLatency = () => {
      latencyField.hidden = !conds.querySelector('input[value="latency_over"]:checked');
      metricField.hidden = !conds.querySelector('input[value="metric_over"]:checked');
    };
    for (const c of CONDITIONS) conds.append(h('label', null, h('input', { type: 'checkbox', value: c.value, checked: t.on.includes(c.value), onchange: syncLatency }), c.label));
    syncLatency();
    const checkSel = selectInput({ options: [{ value: '', label: 'Any check on this node' }, ...(node.checks || []).map((c) => ({ value: c.id, label: c.name }))], value: t.checkId ?? '' });
    const cooldown = numberInput({ value: t.cooldownMinutes || '', min: 0, placeholder: '0 = none' });
    const editor = actionEditor(t.action, { nodes, defaultInterpreter: meta.defaultInterpreter || 'sh', nodeId: node.id });
    const form = h('form', { class: 'stack', onsubmit: (e) => { e.preventDefault(); submit(); } },
      h('div', { class: 'form-grid' }, field({ label: 'Name', input: name }), field({ label: 'Description', input: desc })),
      field({ label: 'Run when this node…', input: conds }),
      h('div', { class: 'form-grid-3' }, latencyField, metricField, field({ label: 'Only for', input: checkSel }), field({ label: 'Cooldown', input: h('div', { class: 'input-with-unit' }, cooldown, h('span', { class: 'unit' }, 'min')), help: 'Minimum time between two runs of this trigger.' })),
      h('div', null, h('div', { class: 'section-title' }, 'Action'), editor),
      actionTester(() => editor.value, { nodeId: node.id }),
      enabled,
    );
    const m = openModal({ title: existing ? 'Edit trigger' : 'New trigger', wide: true, body: form, footer: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, 'Cancel'), h('button', { class: 'btn btn-primary', type: 'button', onclick: () => submit() }, existing ? 'Save trigger' : 'Create trigger')], onClose: () => resolve(result) });
    async function submit() {
      const on = [...conds.querySelectorAll('input:checked')].map((i) => i.value);
      if (!on.length) { toast('Pick at least one condition', { kind: 'error' }); return; }
      const payload = { ...t, name: name.value.trim() || 'Trigger', description: desc.value.trim(), enabled: enabled.input.checked, on, checkId: checkSel.value ? Number(checkSel.value) : null, latencyOverMs: Number(latency.value) || 0, metric: metricKey.value.trim(), metricOver: Number(metricOver.value) || 0, cooldownMinutes: Number(cooldown.value) || 0, action: editor.value };
      try {
        result = existing ? await api.put(`/api/triggers/${existing.id}`, payload) : await api.post('/api/triggers', payload);
        m.close();
        toast(existing ? 'Trigger saved' : 'Trigger created', { kind: 'success' });
      } catch (e) { toast(e.message, { kind: 'error' }); }
    }
    setTimeout(() => name.focus(), 50);
  });
}

/** One trigger row for lists. */
export function triggerRow(t, { node, nodes = [], onChange } = {}) {
  const condLabels = (t.on || []).map((c) => (CONDITIONS.find((x) => x.value === c) || { label: c }).label + (c === 'latency_over' && t.latencyOverMs ? ` ${t.latencyOverMs} ms` : '') + (c === 'metric_over' && t.metric ? ` ${t.metric} > ${t.metricOver ?? 0}` : ''));
  const checkName = t.checkId ? (node?.checks || []).find((c) => c.id === t.checkId)?.name : null;
  const runBtn = h('button', { class: 'btn btn-sm admin-only', type: 'button', title: 'Run this trigger now', onclick: async () => {
    const done = busy(runBtn, 'Running…');
    try { const res = await api.post(`/api/triggers/${t.id}/run`); toast(res.ok ? 'Trigger ran' : `Trigger failed: ${res.error || ''}`, { kind: res.ok ? 'success' : 'error' }); onChange && onChange(); }
    catch (e) { toast(e.message, { kind: 'error' }); }
    done();
  } }, icon('play'), 'Run');
  const last = t.lastRunAt ? h('span', { class: t.lastStatus === 'failed' ? 'text-down' : 'text-up' }, `${t.lastStatus === 'failed' ? 'failed' : 'ran'} `, relTimeEl(t.lastRunAt), ` (${t.runCount} run${t.runCount === 1 ? '' : 's'})`) : h('span', { class: 'dim' }, 'never run');
  return h('div', { class: 'trigger-row' },
    h('div', { class: 'admin-only' }, toggle({ ariaLabel: `${t.name} enabled`, checked: t.enabled !== false, onChange: async (v) => { try { await api.put(`/api/triggers/${t.id}`, { ...t, enabled: v }); onChange && onChange(); } catch (e) { toast(e.message, { kind: 'error' }); onChange && onChange(); } } })),
    h('div', { style: { minWidth: 0 } },
      h('div', { class: 't-name' }, t.name, actionBadge(t.action), t.enabled === false ? h('span', { class: 'tag' }, 'disabled') : null),
      h('div', { class: 't-sub' }, `on: ${condLabels.join(', ')}${checkName ? ` · only ${checkName}` : ''}${t.cooldownMinutes ? ` · cooldown ${t.cooldownMinutes} min` : ''} · ${actionSummary(t.action, nodes)}`),
      h('div', { class: 't-sub' }, last),
      t.lastOutput ? h('div', { class: 't-out', title: t.lastOutput }, t.lastOutput) : null,
    ),
    h('div', { class: 'btn-group' }, runBtn,
      h('button', { class: 'btn btn-sm admin-only', type: 'button', onclick: async () => { const saved = await openTriggerEditor(t, { node, nodes }); if (saved) onChange && onChange(); } }, icon('edit'), 'Edit'),
      h('button', { class: 'btn btn-sm btn-danger icon-btn admin-only', type: 'button', 'aria-label': 'Delete trigger', onclick: async () => { if (await confirmDialog({ title: `Delete trigger "${t.name}"?`, confirmLabel: 'Delete', danger: true })) { try { await api.del(`/api/triggers/${t.id}`); toast('Trigger deleted', { kind: 'success' }); onChange && onChange(); } catch (e) { toast(e.message, { kind: 'error' }); } } } }, icon('trash'))),
  );
}

/** Opens the endpoint editor modal. Resolves with the saved endpoint or null. */
export function openEndpointEditor(existing, { nodes = [] } = {}) {
  return new Promise(async (resolve) => {
    const meta = await automationMeta();
    // New endpoints get a token by default: /hook/ is not covered by the LAN
    // access password, so a tokenless endpoint is open to the whole network.
    const e = existing ? { ...existing, action: { ...(existing.action || {}) } } : { name: '', slug: '', description: '', enabled: true, method: 'ANY', token: randomToken(), allowNoToken: false, action: { type: 'run_node' } };
    let result = null;
    const name = textInput({ value: e.name, placeholder: 'e.g. Router rebooted, Deploy finished', oninput: () => { if (!slugTouched) slug.value = slugify(name.value); } });
    let slugTouched = !!existing;
    const slug = textInput({ value: e.slug || '', class: 'mono', placeholder: 'router-rebooted', oninput: () => { slugTouched = true; } });
    const desc = textInput({ value: e.description || '', placeholder: 'Optional note' });
    const method = selectInput({ options: [{ value: 'ANY', label: 'Any method' }, 'GET', 'POST', 'PUT', 'DELETE'], value: e.method || 'ANY' });
    const token = textInput({ value: e.token || '', class: 'mono', placeholder: 'Shared secret, at least 8 characters', autocomplete: 'off' });
    const genBtn = h('button', { class: 'btn btn-sm', type: 'button', onclick: () => { token.value = randomToken(); noToken.input.checked = false; syncPreview(); } }, icon('refresh'), 'Generate');
    const noToken = checkbox({ label: 'Allow calls without a token (not recommended)', checked: !!e.allowNoToken });
    const enabled = toggle({ label: 'Enabled', checked: e.enabled !== false });
    const editor = actionEditor(e.action, { nodes, defaultInterpreter: meta.defaultInterpreter || 'sh' });
    const urlPreview = h('div', { class: 'note mono' });
    const syncPreview = () => { urlPreview.textContent = `${location.origin}/hook/${slug.value || slugify(name.value) || '…'}${token.value ? '?token=…' : ''}`; };
    for (const el of [name, slug, token]) el.addEventListener('input', syncPreview);
    syncPreview();
    const form = h('form', { class: 'stack', onsubmit: (ev) => { ev.preventDefault(); submit(); } },
      h('div', { class: 'form-grid' }, field({ label: 'Name', input: name }), field({ label: 'URL name', input: slug, help: urlPreview }), field({ label: 'Accepts', input: method }), field({ label: 'Token', input: h('div', { class: 'input-with-unit' }, token, genBtn), help: 'Callers send it as ?token=, an X-GWatch-Token header or a Bearer token.' }), field({ label: 'Description', input: desc, cls: 'span-2' }), h('div', { class: 'span-2' }, noToken)),
      h('div', null, h('div', { class: 'section-title' }, 'What the endpoint does'), editor),
      actionTester(() => editor.value),
      enabled,
    );
    const m = openModal({ title: existing ? 'Edit endpoint' : 'New endpoint', wide: true, body: form, footer: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, 'Cancel'), h('button', { class: 'btn btn-primary', type: 'button', onclick: () => submit() }, existing ? 'Save endpoint' : 'Create endpoint')], onClose: () => resolve(result) });
    async function submit() {
      if (!name.value.trim()) { name.focus(); toast('Give the endpoint a name', { kind: 'error' }); return; }
      const tokenValue = token.value.trim();
      const allowNoToken = !tokenValue && noToken.input.checked;
      if (!tokenValue && !noToken.input.checked) {
        token.focus();
        toast('A token is required. Generate one, or tick "allow calls without a token".', { kind: 'error' });
        return;
      }
      if (allowNoToken) {
        const ok = await confirmDialog({
          title: 'Leave this endpoint open?',
          message: `Anyone who can reach this computer on the network can call /hook/${slug.value.trim() || slugify(name.value)} and make GWatch run this action. The access password does not protect /hook/ URLs.`,
          confirmLabel: 'Leave it open', danger: true,
        });
        if (!ok) { token.focus(); return; }
      }
      const payload = { ...e, name: name.value.trim(), slug: slug.value.trim() || slugify(name.value), description: desc.value.trim(), method: method.value, token: tokenValue, allowNoToken, enabled: enabled.input.checked, action: editor.value };
      try {
        result = existing ? await api.put(`/api/endpoints/${existing.id}`, payload) : await api.post('/api/endpoints', payload);
        m.close(); toast(existing ? 'Endpoint saved' : 'Endpoint created', { kind: 'success' });
      } catch (err) { toast(err.message, { kind: 'error' }); }
    }
    setTimeout(() => name.focus(), 50);
  });
}

export function slugify(s) { return String(s || '').toLowerCase().replace(/[^a-z0-9_-]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 64); }
export function randomToken() {
  const bytes = new Uint8Array(18);
  (window.crypto || {}).getRandomValues ? crypto.getRandomValues(bytes) : bytes.forEach((_, i) => { bytes[i] = Math.floor(Math.random() * 256); });
  return [...bytes].map((b) => b.toString(16).padStart(2, '0')).join('');
}

/** One endpoint row for the Settings list. */
export function endpointRow(e, { nodes = [], onChange } = {}) {
  const url = `${location.origin}/hook/${e.slug}`;
  const runBtn = h('button', { class: 'btn btn-sm admin-only', type: 'button', title: 'Run the action now', onclick: async () => {
    const done = busy(runBtn, 'Running…');
    try { const res = await api.post(`/api/endpoints/${e.id}/run`); toast(res.ok ? 'Endpoint action ran' : `Failed: ${res.error || ''}`, { kind: res.ok ? 'success' : 'error' }); onChange && onChange(); }
    catch (err) { toast(err.message, { kind: 'error' }); }
    done();
  } }, icon('play'), 'Run');
  const last = e.lastCalledAt ? h('span', { class: e.lastStatus === 'failed' ? 'text-down' : 'text-up' }, `${e.lastStatus === 'failed' ? 'failed' : 'ok'} · called `, relTimeEl(e.lastCalledAt), ` (${e.callCount} call${e.callCount === 1 ? '' : 's'})`) : h('span', { class: 'dim' }, 'never called');
  return h('div', { class: 'endpoint-row' },
    h('div', { style: { minWidth: 0 } },
      h('div', { class: 't-name', style: { fontWeight: 650, display: 'flex', gap: '8px', alignItems: 'center', flexWrap: 'wrap' } }, e.name, actionBadge(e.action), h('span', { class: 'tag' }, e.method || 'ANY'), e.token ? h('span', { class: 'tag', title: 'Token required' }, icon('lock'), ' token') : h('span', { class: 'tag', style: { color: 'var(--down)', borderColor: 'var(--down)' }, title: 'Anyone who can reach this port can call this URL' }, icon('alert'), ' no token'), e.enabled === false ? h('span', { class: 'tag' }, 'disabled') : null),
      h('div', { class: 'e-url' }, url, ' ', h('button', { class: 'btn btn-ghost btn-sm icon-btn', type: 'button', title: 'Copy URL', 'aria-label': 'Copy URL', onclick: () => { navigator.clipboard?.writeText(e.token ? `${url}?token=${e.token}` : url); toast('URL copied', { kind: 'info', timeout: 1500 }); } }, icon('copy'))),
      h('div', { class: 't-sub' }, `${actionSummary(e.action, nodes)}${e.description ? ' · ' + e.description : ''}`),
      h('div', { class: 't-sub' }, last),
      e.lastOutput ? h('div', { class: 't-out', title: e.lastOutput }, e.lastOutput) : null,
    ),
    h('div', { class: 'btn-group' }, runBtn,
      h('button', { class: 'btn btn-sm admin-only', type: 'button', onclick: async () => { const saved = await openEndpointEditor(e, { nodes }); if (saved) onChange && onChange(); } }, icon('edit'), 'Edit'),
      h('button', { class: 'btn btn-sm btn-danger icon-btn admin-only', type: 'button', 'aria-label': 'Delete endpoint', onclick: async () => { if (await confirmDialog({ title: `Delete endpoint "${e.name}"?`, message: 'Anything calling this URL will get a 404.', confirmLabel: 'Delete', danger: true })) { try { await api.del(`/api/endpoints/${e.id}`); toast('Endpoint deleted', { kind: 'success' }); onChange && onChange(); } catch (err) { toast(err.message, { kind: 'error' }); } } } }, icon('trash'))),
  );
}

export { relTime };

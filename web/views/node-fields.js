// The settings a node and its checks have, described once.
//
// Two screens ask the same question about a node: the editor, which shows one
// node's fields laid out as a form, and the bulk editor, which builds a
// dropdown of "what would you like to change?" out of them. Keeping the
// vocabulary — the interval choices, the importance levels, the three states
// an alert override can be in — in one place is what stops the two drifting
// into disagreeing about what a setting is called or what values it takes.
//
// A definition says what the field is (`key`, `label`, `scope`), how it is
// asked for (`kind`, `options`, `help`) and where it goes in a PATCH body
// (`path`, a dotted path into the node or check patch). The editor takes the
// vocabulary; the bulk editor takes whole definitions.

import { interval as fmtInterval } from '../fmt.js';

/** The interval choices offered as a dropdown, in seconds. */
export const INTERVALS = [30, 60, 120, 300, 600, 900, 1800, 3600];

/** The interval choices as select options. */
export const intervalOptions = () => INTERVALS.map((s) => ({ value: s, label: `Every ${fmtInterval(s)}` }));

/** The three states a per-check alert override can be in: follow the global
 *  setting, or say yes or no for this check alone. */
export const TRI = [
  { value: '', label: 'Use global default' },
  { value: 'true', label: 'Yes' },
  { value: 'false', label: 'No' },
];

/** How critical a node is. Order is least to most, as it reads in a list. */
export const IMPORTANCE_OPTIONS = [
  { value: 'low', label: 'Low' },
  { value: 'normal', label: 'Normal' },
  { value: 'high', label: 'High' },
  { value: 'critical', label: 'Critical' },
];

/** Which pinger a ping check uses. An empty value follows Settings › General. */
export const PING_METHOD_OPTIONS = [
  { value: '', label: 'Use global setting' },
  { value: 'builtin', label: 'Built-in' },
  { value: 'system', label: 'System ping' },
];

/** Yes/no as a dropdown, for a screen where a switch would read as "this is
 *  the current state" when it means "set every one of them to this". */
export const ON_OFF_OPTIONS = [
  { value: 'true', label: 'Enabled' },
  { value: 'false', label: 'Disabled' },
];

/** The per-check alert override fields, in the order the editor shows them. */
export const ALERT_OVERRIDE_FIELDS = [
  { key: 'enabled', label: 'Send alerts', kind: 'tri-state' },
  { key: 'cooldownMinutes', label: 'Cooldown', kind: 'number', unit: 'min', min: 0 },
  { key: 'notifyRecovery', label: 'Notify on recovery', kind: 'tri-state' },
  { key: 'notifyWarnings', label: 'Notify on warnings', kind: 'tri-state' },
  { key: 'recipients', label: 'Recipients', kind: 'chips', help: 'Replaces the global recipients for this check when set.' },
];

// ---- the bulk-edit parameter table ----

// `kind` says what control asks for the value:
//   number     — a number box, with an optional `unit` and `min`/`max`
//   select     — a dropdown over `options`
//   chips      — a list of words, entered one at a time
//   toggle     — enabled / disabled, sent as a boolean
//   tri-state  — yes / no / follow the global setting, sent as true/false/null
//   none       — no value at all; the field's `value` is sent as it stands
//
// `path` is where the value goes in the PATCH /api/nodes/bulk body, relative
// to the `node` or `check` object named by `scope`. The bulk screen builds the
// nested object from it, so a new parameter needs a row here and nothing else.

/** Every parameter a bulk edit can set, in the order the dropdown lists them. */
export const BULK_FIELDS = [
  // --- checks: schedule ---
  {
    key: 'intervalSeconds', scope: 'check', path: 'intervalSeconds', group: 'Schedule',
    label: 'Check interval', kind: 'select', options: intervalOptions, allowCustom: true, min: 5,
    help: 'How often each selected check runs.',
    summary: (v) => `set interval to ${fmtInterval(Number(v))}`,
  },
  {
    key: 'timeoutSeconds', scope: 'check', path: 'timeoutSeconds', group: 'Schedule',
    label: 'Timeout', kind: 'number', unit: 'seconds', min: 1, max: 120, value: 10,
    help: 'How long one attempt may take before it counts as a failure.',
    summary: (v) => `set timeout to ${v} s`,
  },
  {
    key: 'retries', scope: 'check', path: 'retries', group: 'Schedule',
    label: 'Retries', kind: 'number', min: 0, max: 10, value: 1,
    help: 'Immediate retries inside one run before a failure is counted.',
    summary: (v) => `set retries to ${v}`,
  },
  {
    key: 'failureThreshold', scope: 'check', path: 'failureThreshold', group: 'Schedule',
    label: 'Failures before down', kind: 'number', min: 0, max: 50, value: 2,
    help: 'Consecutive failed runs before the check is down and alerts fire. 0 follows the global default.',
    summary: (v) => (Number(v) === 0 ? 'set failures before down to the global default' : `set failures before down to ${v}`),
  },
  {
    key: 'checkEnabled', scope: 'check', path: 'enabled', group: 'Schedule',
    label: 'Checks enabled', kind: 'toggle', value: 'true',
    help: 'A disabled check keeps its configuration and its history, and stops running.',
    summary: (v) => (v === 'true' ? 'enable' : 'disable'),
  },

  // --- checks: thresholds ---
  {
    key: 'latencyWarnMs', scope: 'check', path: 'config.latencyWarnMs', group: 'Thresholds',
    label: 'Warn when slower than', kind: 'number', unit: 'ms', min: 0, value: 0,
    help: 'Marks the check degraded rather than down. 0 turns it off.',
    summary: (v) => (Number(v) === 0 ? 'turn the latency warning off' : `warn over ${v} ms`),
  },
  {
    key: 'packetLossWarnPct', scope: 'check', path: 'config.packetLossWarnPct', group: 'Thresholds',
    label: 'Warn when packet loss exceeds', kind: 'number', unit: '%', min: 0, max: 100, value: 0,
    help: 'Ping checks only. 0 turns it off.',
    summary: (v) => (Number(v) === 0 ? 'turn the packet-loss warning off' : `warn over ${v} % loss`),
  },
  {
    key: 'certWarnDays', scope: 'check', path: 'config.certWarnDays', group: 'Thresholds',
    label: 'Certificate warning', kind: 'number', unit: 'days', min: 0, max: 365, value: 14,
    help: 'Warn this many days before a certificate expires. 0 follows the global setting.',
    summary: (v) => `set the certificate warning to ${v} days`,
  },
  {
    key: 'pingMethod', scope: 'check', path: 'config.pingMethod', group: 'Thresholds',
    label: 'Ping method', kind: 'select', options: () => PING_METHOD_OPTIONS, value: '',
    help: 'Ping checks only. Built-in sends the echo requests itself; system ping runs the operating system\'s command.',
    summary: (v) => `set the ping method to ${v || 'the global setting'}`,
  },

  // --- checks: alerts ---
  {
    key: 'alertsEnabled', scope: 'check', path: 'alerts.enabled', group: 'Alerts',
    label: 'Send alerts', kind: 'tri-state', value: '',
    help: 'Overrides Settings › Alerts for the selected checks.',
    summary: (v) => `set alerts to ${triWord(v)}`,
  },
  {
    key: 'alertsCooldown', scope: 'check', path: 'alerts.cooldownMinutes', group: 'Alerts',
    label: 'Alert cooldown', kind: 'number', unit: 'min', min: 0, value: 60,
    summary: (v) => `set the alert cooldown to ${v} min`,
  },
  {
    key: 'alertsRecovery', scope: 'check', path: 'alerts.notifyRecovery', group: 'Alerts',
    label: 'Notify on recovery', kind: 'tri-state', value: '',
    summary: (v) => `set recovery notices to ${triWord(v)}`,
  },
  {
    key: 'alertsWarnings', scope: 'check', path: 'alerts.notifyWarnings', group: 'Alerts',
    label: 'Notify on warnings', kind: 'tri-state', value: '',
    summary: (v) => `set warning notices to ${triWord(v)}`,
  },
  {
    key: 'alertsRecipients', scope: 'check', path: 'alerts.recipients', group: 'Alerts',
    label: 'Alert recipients', kind: 'chips', placeholder: 'Add an email and press Enter',
    validate: (v) => /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(v),
    help: 'Replaces the global recipients for the selected checks.',
    summary: (v) => `set the alert recipients to ${(v || []).join(', ') || 'nobody'}`,
  },
  {
    key: 'alertsClear', scope: 'check', path: 'alerts', group: 'Alerts',
    label: 'Clear all alert overrides', kind: 'none', value: null,
    help: 'Puts the selected checks back on the global alert settings.',
    summary: () => 'clear the alert overrides',
  },

  // --- nodes ---
  {
    key: 'addGroups', scope: 'node', path: 'addGroups', group: 'Nodes',
    label: 'Add to groups', kind: 'chips', suggest: 'groups', placeholder: 'Add a group and press Enter',
    summary: (v) => `add to ${(v || []).join(', ')}`,
  },
  {
    key: 'removeGroups', scope: 'node', path: 'removeGroups', group: 'Nodes',
    label: 'Remove from groups', kind: 'chips', suggest: 'groups', placeholder: 'Add a group and press Enter',
    summary: (v) => `remove from ${(v || []).join(', ')}`,
  },
  {
    key: 'groups', scope: 'node', path: 'groups', group: 'Nodes',
    label: 'Replace groups with', kind: 'chips', suggest: 'groups', placeholder: 'Add a group and press Enter',
    help: 'Replaces the whole list. Leave it empty to take the nodes out of every group.',
    summary: (v) => ((v || []).length ? `set groups to ${v.join(', ')}` : 'clear the groups'),
  },
  {
    key: 'addTags', scope: 'node', path: 'addTags', group: 'Nodes',
    label: 'Add tags', kind: 'chips', suggest: 'tags', placeholder: 'Add a tag and press Enter',
    summary: (v) => `add tag ${(v || []).join(', ')}`,
  },
  {
    key: 'removeTags', scope: 'node', path: 'removeTags', group: 'Nodes',
    label: 'Remove tags', kind: 'chips', suggest: 'tags', placeholder: 'Add a tag and press Enter',
    summary: (v) => `remove tag ${(v || []).join(', ')}`,
  },
  {
    key: 'tags', scope: 'node', path: 'tags', group: 'Nodes',
    label: 'Replace tags with', kind: 'chips', suggest: 'tags', placeholder: 'Add a tag and press Enter',
    help: 'Replaces the whole list. Leave it empty to remove every tag.',
    summary: (v) => ((v || []).length ? `set tags to ${v.join(', ')}` : 'clear the tags'),
  },
  {
    key: 'importance', scope: 'node', path: 'importance', group: 'Nodes',
    label: 'Importance', kind: 'select', options: () => IMPORTANCE_OPTIONS, value: 'normal',
    help: 'Critical and high nodes are listed first on the wallboard.',
    summary: (v) => `set importance to ${v}`,
  },
  {
    key: 'nodeEnabled', scope: 'node', path: 'enabled', group: 'Nodes',
    label: 'Nodes enabled', kind: 'toggle', value: 'true',
    help: 'A disabled node keeps its configuration and stops being checked.',
    summary: (v) => (v === 'true' ? 'enable' : 'disable'),
  },
  {
    key: 'dependsOn', scope: 'node', path: 'dependsOnNodeId', group: 'Nodes',
    label: 'Depends on', kind: 'select', options: 'nodes', value: '',
    help: 'Alerts are suppressed while the parent is down. "None" clears the dependency.',
    summary: (v, ctx) => (v ? `depend on ${ctx?.nodeName?.(v) || `node ${v}`}` : 'clear the dependency'),
  },
];

/** One definition by key. */
export const bulkField = (key) => BULK_FIELDS.find((f) => f.key === key);

/** The check-configuration keys a bulk edit may set, matching the server's
 *  whitelist in internal/api/bulk.go. */
export const BULK_CONFIG_KEYS = BULK_FIELDS
  .filter((f) => f.path.startsWith('config.'))
  .map((f) => f.path.slice('config.'.length));

function triWord(v) {
  if (v === 'true') return 'yes';
  if (v === 'false') return 'no';
  return 'the global default';
}

/** Turn a definition's raw control value into what the API expects. */
export function bulkValue(field, raw) {
  switch (field.kind) {
    case 'number': return Number(raw) || 0;
    case 'toggle': return raw === 'true' || raw === true;
    case 'tri-state': return raw === '' || raw == null ? null : raw === 'true';
    case 'chips': return [...(raw || [])];
    case 'none': return field.value ?? null;
    case 'select':
      if (field.path === 'dependsOnNodeId') return raw ? Number(raw) : null;
      if (field.path === 'intervalSeconds') return Number(raw);
      return raw;
    default: return raw;
  }
}

/** Write `value` at a dotted `path` inside `target`, creating the objects the
 *  path runs through. Several rows writing under `alerts.` or `config.` merge
 *  into one object this way, which is exactly what the API expects. */
export function setPath(target, path, value) {
  const parts = path.split('.');
  let cur = target;
  for (let i = 0; i < parts.length - 1; i++) {
    if (cur[parts[i]] == null || typeof cur[parts[i]] !== 'object') cur[parts[i]] = {};
    cur = cur[parts[i]];
  }
  cur[parts[parts.length - 1]] = value;
  return target;
}

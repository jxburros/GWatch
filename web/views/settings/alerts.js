// Settings › Alerts.
//
// Part of settings.js, split out for #118: the code is the
// view's own, moved here unchanged inside create(shell), which hands it the
// view's shared state and helpers.

import { api } from '../../api.js';
import { h, icon, clear, replace, field, textInput, numberInput, selectInput, checkbox, toggle, chipInput, confirmDialog, busy } from '../../components.js';
import { openAlertChannelEditor, actionBadge, actionSummary, ALERT_SEVERITIES } from '../automation.js';

/** @param {Record<string, any>} shell the view's state and helpers */
export function create(shell) {
  const { loadSettings, numField, saveBar, saveSettings, state } = shell;

  async function tabAlerts() {
    const s = state.settings || await loadSettings();
    const a = s.alerts; a.smtp = a.smtp || { port: 587, security: 'starttls' };
    const enabled = toggle({ label: 'Send alerts', checked: !!a.enabled, onChange: (v) => { a.enabled = v; } });
    a.channels = a.channels || [];
    const recipients = chipInput({ values: a.recipients || [], placeholder: 'Add an email address and press Enter', validate: (v) => /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(v), onChange: (v) => { a.recipients = v; } });
    const notifyRecovery = checkbox({ label: 'Send a recovery email when a check becomes healthy again', checked: !!a.notifyRecovery, onChange: (v) => { a.notifyRecovery = v; } });
    const notifyWarnings = checkbox({ label: 'Also email for warnings (high latency, packet loss, expiring certificates, content changes)', checked: !!a.notifyWarnings, onChange: (v) => { a.notifyWarnings = v; } });
    /* Grouping and the digest (#99) only change how many e-mails carry the
     * alerts and when they leave, never whether an alert is due. */
    const digest = selectInput({ options: [{ value: 'off', label: 'Off — email each warning' }, { value: 'hourly', label: 'Hourly digest' }, { value: 'daily', label: 'Daily digest (08:00)' }], value: a.warningDigest || 'off', onchange: () => { a.warningDigest = digest.value; } });
    const smtp = a.smtp;
    const host = textInput({ value: smtp.host || '', placeholder: 'smtp.example.com', oninput: () => { smtp.host = host.value; } });
    const port = numberInput({ value: smtp.port || 587, min: 1, max: 65535, oninput: () => { smtp.port = Number(port.value); } });
    const security = selectInput({ options: [{ value: 'starttls', label: 'STARTTLS (port 587)' }, { value: 'tls', label: 'TLS / SSL (port 465)' }, { value: 'none', label: 'None (unencrypted)' }], value: smtp.security || 'starttls', onchange: () => { smtp.security = security.value; } });
    const user = textInput({ value: smtp.username || '', autocomplete: 'off', oninput: () => { smtp.username = user.value; } });
    const pass = h('input', { type: 'password', value: smtp.password || '', autocomplete: 'new-password', placeholder: smtp.password ? '' : 'App password or SMTP password', oninput: () => { smtp.password = pass.value; } });
    const from = textInput({ value: smtp.from || '', placeholder: 'gwatch@example.com', oninput: () => { smtp.from = from.value; } });
    const testTo = textInput({ placeholder: 'Optional: send to a different address', 'aria-label': 'Test email recipient' });
    const testMsg = h('div', { class: 'note', role: 'status' });
    const testBtn = h('button', { class: 'btn', type: 'button', onclick: async () => {
      const done = busy(testBtn, 'Sending…'); replace(testMsg, '');
      try { const r = await api.post('/api/settings/test-email', { to: testTo.value.trim() || undefined }); replace(testMsg, h('span', { class: 'status-glyph text-up' }, icon('check'), r.message || 'Test email sent.')); }
      catch (e) { replace(testMsg, h('span', { class: 'status-glyph text-down' }, icon('x'), e.message)); }
      done();
    } }, icon('mail'), 'Send test email');
    return h('form', { class: 'stack', onsubmit: (e) => { e.preventDefault(); saveSettings(); } },
      h('section', { class: 'card' }, h('h2', null, 'Alerts'), h('p', { class: 'lead' }, 'Alerts are meant to be quiet and explainable: one email when something breaks, one when it recovers. For webhooks, scripts or git commands see Automation.'),
        h('div', { class: 'stack' },
          enabled,
          field({ label: 'Recipients', input: recipients }),
          h('div', { class: 'form-grid-3' },
            numField(a, 'failureThreshold', 'Failures before alerting', { min: 1, help: 'Consecutive failed runs before a check is down.' }),
            numField(a, 'cooldownMinutes', 'Cooldown', { unitLabel: 'min', help: 'Minimum time between repeated alerts for the same check.' }),
            numField(a, 'certWarnDays', 'Certificate warning', { unitLabel: 'days', min: 1, help: 'Warn when a certificate expires within this many days.' })),
          h('div', { class: 'stack-sm' }, notifyRecovery, notifyWarnings),
          h('div', { class: 'form-grid' },
            numField(a, 'groupWindowSeconds', 'Group alerts for the same node within', { unitLabel: 'seconds', max: 300, help: 'When several checks on one node go down (or recover) within this time, send one email listing them all instead of one each. Delays the first alert by up to this long. 0 = off, at most 300.' }),
            field({ label: 'Send warnings as a digest', input: digest, help: 'Collect warning emails into one summary instead of sending each. Down and recovery emails are always sent straight away; notification channels still get each warning.' })),
        )),
      alsoNotifyCard(a),
      h('section', { class: 'card' }, h('h2', null, 'Outgoing email (SMTP)'), h('p', { class: 'lead' }, 'Use an app password where your provider offers one. The password is stored locally and never shown again.'),
        h('div', { class: 'form-grid' },
          field({ label: 'SMTP host', input: host }), field({ label: 'Port', input: port }),
          field({ label: 'Security', input: security }), field({ label: 'From address', input: from }),
          field({ label: 'Username', input: user }), field({ label: 'Password', input: pass }),
        ),
        h('hr', { class: 'divider' }),
        h('div', { class: 'row', style: { alignItems: 'flex-start' } }, h('div', { style: { flex: '1', minWidth: '220px', maxWidth: '360px' } }, testTo), testBtn),
        testMsg,
        h('hr', { class: 'divider' }), saveBar()),
      h('section', { class: 'card' }, h('h2', null, 'How suppression works'),
        h('div', { class: 'stack-sm u-mt-8' },
          h('p', { class: 'note' }, h('b', null, 'Dependencies: '), 'when a node "depends on" another (say Plex depends on Gateway) and the parent is down, failures on the child are recorded as "affected by Gateway" and no separate email is sent for it.'),
          h('p', { class: 'note' }, h('b', null, 'Cooldown: '), 'after an alert is sent for a check, further alerts for that same check are held back for the cooldown period. A recovery email is still sent as soon as it comes back.'),
          h('p', { class: 'note' }, h('b', null, 'Maintenance and silences: '), 'checks keep running and history is kept, but alerts are suppressed and shown as such in the incident timeline.'))));
  }

  /* "Also notify": notification channels sent to at exactly the moments an
   * alert e-mail is, so maintenance, silences, dependencies, acknowledgements
   * and the cooldown hold them back just the same. Edits change the settings
   * document in memory; the card's Save button saves it with everything else.
   */
  function alsoNotifyCard(a) {
    const list = h('div', { class: 'alert-channels' });
    let groups = [];
    api.get('/api/groups').then((g) => { groups = (g?.groups || []).map((x) => x.name); }).catch(() => {});
    const render = () => {
      clear(list);
      if (!a.channels.length) {
        list.append(h('p', { class: 'note' }, 'No channels yet. Alerts go by email only.'));
        return;
      }
      a.channels.forEach((ch, i) => {
        const sev = ALERT_SEVERITIES.find((s) => s.value === (ch.minSeverity || 'warning'));
        const scope = ch.groups?.length ? `groups: ${ch.groups.join(', ')}` : 'all nodes';
        list.append(h('div', { class: 'trigger-row' },
          toggle({ ariaLabel: `${ch.name || 'Channel'} enabled`, checked: !!ch.enabled, onChange: (v) => { ch.enabled = v; } }),
          h('div', { class: 'u-min-w-0' },
            h('div', { class: 't-name' }, ch.name || ch.action?.type, actionBadge(ch.action)),
            h('div', { class: 't-sub' }, `${sev ? sev.label.split(':')[0] : ''} · ${scope} · ${actionSummary(ch.action)}`)),
          h('div', { class: 'btn-group' },
            h('button', { class: 'btn btn-sm', type: 'button', onclick: async () => { const next = await openAlertChannelEditor(ch, { groups }); if (next) { a.channels[i] = next; render(); } } }, icon('edit'), 'Edit'),
            h('button', { class: 'btn btn-sm btn-danger icon-btn', type: 'button', 'aria-label': `Remove ${ch.name || 'channel'}`, onclick: async () => {
              if (await confirmDialog({ title: `Remove "${ch.name || 'this channel'}"?`, message: 'It stops receiving alerts once you save.', confirmLabel: 'Remove', danger: true })) { a.channels.splice(i, 1); render(); }
            } }, icon('trash')))));
      });
    };
    render();
    const add = h('button', { class: 'btn', type: 'button', onclick: async () => { const ch = await openAlertChannelEditor(null, { groups }); if (ch) { a.channels.push(ch); render(); } } }, icon('plus'), 'Add channel');
    return h('section', { class: 'card' }, h('h2', null, 'Also notify'),
      h('p', { class: 'lead' }, 'Send the same alerts to Slack, Teams, Discord, Telegram, Matrix, ntfy, Gotify, Pushover, SMS (Twilio), PagerDuty or Opsgenie. A channel gets an alert exactly when the email would go out, so maintenance windows, silences, dependencies and the cooldown apply to it too. Set a channel to "outages only", or to some groups, to route only critical alerts there.'),
      h('p', { class: 'note' }, 'PagerDuty and Opsgenie incidents are resolved when the check recovers, even if recovery emails are off. Keys and webhook URLs are stored encrypted and never shown again; edit a channel without retyping them to keep the saved ones.'),
      list,
      h('div', { class: 'btn-group u-mt-8' }, add),
      h('hr', { class: 'divider' }), saveBar());
  }

  return { tabAlerts };
}

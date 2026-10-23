// Settings › Monitor health.
//
// Part of settings.js, split out for #118: the code is the
// view's own, moved here unchanged inside create(shell), which hands it the
// view's shared state and helpers.

import { api } from '../../api.js';
import { h, icon, clear, eventRow } from '../../components.js';
import { relTime, dateTime, bytes, num, duration, timeShort } from '../../fmt.js';

/** @param {Record<string, any>} shell the view's state and helpers */
export function create(shell) {
  const { hcard, state } = shell;

  // pingCard says which way ping works on the monitoring machine and, when it
  // is not the first choice, why (#131): the unprivileged socket the kernel
  // refused, the raw socket that needs CAP_NET_RAW, the last time a check fell
  // back to the system ping command. The reason is the useful part, so it is
  // the card's sub-line rather than a tooltip.
  function pingCard(p) {
    const via = {
      unprivileged: ['Built-in', true],
      raw: ['Built-in (raw socket)', true],
      system: ['System ping command', false],
      none: ['Not available', false],
    }[p.auto] || ['Unknown', false];
    const value = h('span', { class: `status-glyph ${via[1] ? 'text-up' : (p.auto === 'none' ? 'text-down' : 'text-degraded')}` }, icon(via[1] ? 'check' : 'x'), via[0]);
    const reasons = p.auto === 'unprivileged' ? [] : [p.unprivilegedReason, p.auto === 'raw' ? '' : p.rawReason].filter(Boolean);
    const sub = h('span', null,
      `Method setting: ${p.configured || 'auto'}`,
      reasons.length ? h('span', null, h('br'), reasons.join('; ')) : null,
      p.lastFallback ? h('span', null, h('br'), `Last fell back to the ping command ${relTime(p.lastFallbackAt)}: ${p.lastFallback}`) : null,
      p.auto === 'unprivileged' || p.auto === 'raw' ? null : h('span', null, h('br'), h('a', { href: 'https://github.com/jxburros/GWatch/blob/main/docs/INSTALL-LINUX.md#network-and-ping', target: '_blank', rel: 'noopener' }, 'How to allow ping')));
    return hcard(value, 'Ping', sub);
  }

  async function tabHealth() {
    const wrap = h('div', { class: 'stack' });
    const render = (hl) => {
      clear(wrap);
      const ok = (v, yes = 'Yes', no = 'No') => h('span', { class: `status-glyph ${v ? 'text-up' : 'text-down'}` }, icon(v ? 'check' : 'x'), v ? yes : no);
      const gap = hl.lastGap;
      wrap.append(
        h('section', { class: 'card' }, h('h2', null, 'Service'), h('p', { class: 'lead' }, 'A quiet inbox is only good news if the monitor itself is running.'),
          h('div', { class: 'health-cards' },
            hcard(ok(hl.serviceRunning, 'Running', 'Stopped'), `Background ${hl.serviceMode === 'service' ? 'service' : 'process'}`, `up ${duration(hl.uptimeSeconds)} · since ${dateTime(hl.startedAt, { seconds: false })}`),
            hcard(ok(hl.schedulerRunning, 'Running', 'Stopped'), 'Scheduler', `${hl.checksEnabled} of ${hl.checksTotal} checks enabled · ${hl.checksRunning} running now`),
            hcard(hl.overdueChecks || 0, 'Overdue checks', hl.overdueChecks ? `Oldest overdue by ${duration(hl.oldestOverdueSeconds)}` : 'No checks are overdue'),
            hcard(hl.lastCheckAt ? relTime(hl.lastCheckAt) : 'never', 'Last check completed', hl.lastSuccessAt ? `last success ${relTime(hl.lastSuccessAt)}` : ''),
            hcard(hl.nextCheckAt ? relTime(hl.nextCheckAt) : '—', 'Next scheduled check', hl.nextCheckAt ? dateTime(hl.nextCheckAt) : ''),
            hcard(gap ? duration(gap.seconds) : 'None detected', 'Last sleep / offline gap', gap ? `${dateTime(gap.from, { seconds: false })} → ${timeShort(gap.to)}` : 'The monitoring computer has not been asleep or offline recently.'),
            hcard(h('span', { class: 'mono', style: { fontSize: '14px' } }, hl.listenAddress || '—'), 'Listening on', `${hl.platform || ''} · v${hl.version || '?'}`),
            hl.ping ? pingCard(hl.ping) : null)),
        h('section', { class: 'card' }, h('h2', null, 'Storage and retention'),
          h('div', { class: 'health-cards u-mt-10' },
            hcard(bytes(hl.databaseBytes), 'Database size', h('span', { class: 'mono' }, hl.databasePath || '')),
            hcard(hl.retention?.lastRunAt ? relTime(hl.retention.lastRunAt) : 'never', 'Last retention run', hl.retention?.lastError ? h('span', { class: 'text-down' }, hl.retention.lastError) : `${num(hl.retention?.rawRows)} raw · ${num(hl.retention?.rollupRows5m)} 5-min · ${num(hl.retention?.rollupRows1h)} hourly · ${num(hl.retention?.rollupRows1d)} daily`),
            hcard(hl.backup?.lastBackupAt ? ok(hl.backup.lastBackupOk, relTime(hl.backup.lastBackupAt), `failed ${relTime(hl.backup.lastBackupAt)}`) : h('span', { class: 'muted' }, 'never'), 'Last backup', hl.backup?.lastBackupFile || hl.backup?.lastError || '')),
          h('div', { class: 'btn-group u-mt-12' }, h('a', { class: 'btn btn-sm', href: '#/settings/retention' }, 'Retention settings'), h('a', { class: 'btn btn-sm', href: '#/settings/backups' }, 'Backups'))),
        h('section', { class: 'card' }, h('h2', null, 'Alerting'),
          h('div', { class: 'health-cards u-mt-10' },
            hcard(ok(hl.alertsEnabled, 'Enabled', 'Disabled'), 'Email alerts'),
            hcard(ok(hl.smtpConfigured, 'Configured', 'Not configured'), 'SMTP'),
            hcard(hl.lastAlertAt ? relTime(hl.lastAlertAt) : 'never', 'Last alert sent', hl.lastAlertError ? h('span', { class: 'text-down' }, hl.lastAlertError) : '')),
          h('div', { class: 'btn-group u-mt-12' }, h('a', { class: 'btn btn-sm', href: '#/settings/alerts' }, 'Alert settings'))),
        h('section', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, 'Recent internal errors'), h('a', { class: 'btn btn-sm', href: '#/audit/log' }, 'Open service log')),
          hl.recentErrors?.length ? h('div', { class: 'event-rows' }, hl.recentErrors.map((e) => eventRow(e))) : h('div', { class: 'all-good', style: { padding: '10px' } }, icon('check'), h('strong', null, 'No internal errors recorded'))),
      );
    };
    const load = () => api.get('/api/health').then(render);
    await load();
    state.panelRefresh = load;
    return wrap;
  }

  return { tabHealth };
}

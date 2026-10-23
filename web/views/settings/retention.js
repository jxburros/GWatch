// Settings › Retention.
//
// Part of settings.js, split out for #118: the code is the
// view's own, moved here unchanged inside create(shell), which hands it the
// view's shared state and helpers.

import { api } from '../../api.js';
import { h, icon, replace, toast, skeleton, banner, busy } from '../../components.js';
import { relTime, num, duration, retentionSpan } from '../../fmt.js';

/** @param {Record<string, any>} shell the view's state and helpers */
export function create(shell) {
  const { hcard, loadDataPaths, loadSettings, numField, saveBar, saveSettings, state } = shell;

  async function tabRetention() {
    const s = state.settings || await loadSettings();
    const r = s.retention;
    const summary = h('div', { class: 'retention-summary', 'aria-live': 'polite' });
    const update = () => {
      const raw = retentionSpan(r.rawDays), five = retentionSpan(r.fiveMinDays), hourly = retentionSpan(r.hourlyDays), daily = retentionSpan(r.dailyDays), ev = retentionSpan(r.eventDays);
      replace(summary,
        'Every result is kept for ', h('b', null, raw), ', then ', h('b', null, '5-minute summaries'), ' until ', h('b', null, five === 'forever' ? 'forever' : `${five} old`), ', ',
        h('b', null, 'hourly'), ' until ', h('b', null, hourly === 'forever' ? 'forever' : `${hourly} old`), ', and ', h('b', null, 'daily'), ' ', daily === 'forever' ? h('b', null, 'forever') : h('span', null, 'until ', h('b', null, `${daily} old`), ', then deleted'), '. ',
        'Events are kept for ', h('b', null, ev), '.');
    };
    const f = (key, label, help) => { const el = numField(r, key, label, { unitLabel: 'days', help }); el.querySelector('input').addEventListener('input', update); return el; };
    update();
    const statusBox = h('div', null, skeleton({ lines: 3 }));
    const runBtn = h('button', { class: 'btn', type: 'button', onclick: async () => { const done = busy(runBtn, 'Running…'); try { const st = await api.post('/api/retention/run'); renderStatus(st); toast('Retention run finished', { kind: 'success' }); } catch (e) { toast(e.message, { kind: 'error' }); } done(); } }, icon('database'), 'Run retention now');
    const renderStatus = (st) => {
      replace(statusBox,
        h('div', { class: 'health-cards' },
          hcard(num(st.rawRows), 'Raw results', st.oldestRaw ? `oldest ${relTime(st.oldestRaw)}` : ''),
          hcard(num(st.rollupRows5m), '5-minute rollups'), hcard(num(st.rollupRows1h), 'Hourly rollups'), hcard(num(st.rollupRows1d), 'Daily rollups'), hcard(num(st.eventRows), 'Events'),
          hcard(st.lastRunAt ? relTime(st.lastRunAt) : 'never', 'Last run', st.lastRunAt ? `${duration(st.lastDurationMs / 1000)} · removed ${num(st.deletedLastRun)} rows` : '')),
        st.lastError ? h('div', { class: 'u-mt-10' }, banner('down', `Last run failed: ${st.lastError}`)) : null,
        st.plan?.length ? h('ul', { class: 'note', style: { marginTop: '12px', paddingLeft: '18px' } }, st.plan.map((p) => h('li', null, p))) : null);
    };
    api.get('/api/retention/status').then(renderStatus).catch((e) => replace(statusBox, h('p', { class: 'note' }, e.message)));
    const pathsBox = h('div', null, skeleton({ lines: 2 }));
    loadDataPaths(pathsBox);
    state.panelRefresh = () => api.get('/api/retention/status').then(renderStatus).catch(() => {});
    return h('form', { class: 'stack', onsubmit: (e) => { e.preventDefault(); saveSettings(); } },
      h('section', { class: 'card' }, h('h2', null, 'History retention'), h('p', { class: 'lead' }, 'Recent data stays detailed; older data is summarised so the database never grows without limit. 0 = keep forever.'),
        summary,
        h('div', { class: 'form-grid-3 u-mt-14' },
          f('rawDays', 'Keep every result for'), f('fiveMinDays', 'Keep 5-minute summaries for'), f('hourlyDays', 'Keep hourly summaries for'), f('dailyDays', 'Keep daily summaries for'), f('eventDays', 'Keep events for'),
          f('hostDays', 'Keep hardware check results for', 'A hardware check\u2019s result is a whole snapshot of a machine rather than a single number, so these have a retention of their own; every other check follows the setting for individual results above.')),
        h('hr', { class: 'divider' }), saveBar()),
      h('section', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, 'Current storage'), runBtn), statusBox, h('hr', { class: 'divider' }), pathsBox));
  }

  return { tabRetention };
}

// Printable availability reports and recurring delivery definitions.
import { api, qs } from '../api.js';
import { h, field, textInput, numberInput, selectInput, checkbox, replace, toast, emptyState } from '../components.js';
export async function mount(root, ctx) {
  ctx.setTitle('Reports');
  let destroyed = false, definitions = [];
  const date = (d) => d.toISOString().slice(0, 10);
  const to = h('input', { type: 'date', value: date(new Date()) });
  const from = h('input', { type: 'date', value: date(new Date(Date.now() - 7 * 86400000)) });
  const groups = textInput({ placeholder: 'All groups' }), tags = textInput({ placeholder: 'All tags' });
  const target = numberInput({ value: 99.9, min: 0, max: 100, step: 0.01 });
  const charts = checkbox({ label: 'Include latency charts by group', checked: false });
  const error = h('p', { role: 'alert' });
  const link = h('a', { class: 'btn btn-primary', target: '_blank', rel: 'noopener', href: '#', onclick: (e) => {
    if (!from.value || !to.value || from.value >= to.value || !target.checkValidity()) { e.preventDefault(); error.textContent = 'Choose an end date after the start date and a target from 0 to 100%.'; return; }
    error.textContent = '';
    link.href = `/api/reports/generate${qs({ from: new Date(from.value).toISOString(), to: new Date(to.value).toISOString(), groups: groups.value.trim(), tags: tags.value.trim(), target: target.value, charts: charts.input.checked ? '1' : null })}`;
  } }, 'Open printable report');
  const list = h('div', { class: 'stack' });
  root.append(h('section', { class: 'card' }, h('h2', null, 'Availability report'), h('p', { class: 'lead' }, 'Choose a time window and scope. Open the report, then use your browser’s Print or Save as PDF.'),
    h('div', { class: 'form-grid' }, field({ label: 'From', input: from }), field({ label: 'Until (exclusive)', input: to }), field({ label: 'Groups (comma separated)', input: groups }), field({ label: 'Tags (comma separated)', input: tags }), field({ label: 'Availability target (%)', input: target })), charts, error, link), list);
  const split = (s) => s.split(',').map((x) => x.trim()).filter(Boolean);
  async function save() {
    try { definitions = await api.put('/api/reports', definitions); toast('Reports saved', { kind: 'success' }); render(); } catch (e) { toast(e.message, { kind: 'error' }); }
  }
  function render() {
    if (destroyed) return;
    replace(list, h('h2', null, 'Scheduled reports'));
    for (const def of definitions) {
      const input = (key, label, array = false) => { const el = textInput({ value: array ? (def[key] || []).join(', ') : def[key] || '', oninput: () => { def[key] = array ? split(el.value) : el.value; }, disabled: !ctx.me?.isAdmin }); return field({ label, input: el }); };
      const includeCharts = checkbox({ label: 'Include latency charts by group', checked: !!def.includeLatencyCharts, disabled: !ctx.me?.isAdmin, onChange: (v) => { def.includeLatencyCharts = v; } });
      const sla = numberInput({ value: def.targetAvailability ?? 99.9, min: 0, max: 100, step: 0.01, disabled: !ctx.me?.isAdmin, oninput: () => { def.targetAvailability = Number(sla.value); } });
      const period = selectInput({ value: def.period || 'weekly', options: [{ value: 'weekly', label: 'Weekly' }, { value: 'monthly', label: 'Monthly' }], disabled: !ctx.me?.isAdmin, onchange: () => { def.period = period.value; } });
      list.append(h('section', { class: 'card' }, checkbox({ label: 'Enabled', checked: def.enabled, disabled: !ctx.me?.isAdmin, onChange: (v) => { def.enabled = v; } }), h('div', { class: 'form-grid' }, input('name', 'Report name'), field({ label: 'Delivery period', input: period }), input('recipients', 'Recipients (comma separated)', true), input('groups', 'Groups', true), input('tags', 'Tags', true), field({ label: 'Availability target (%)', input: sla, help: '0 disables target comparison.' })), includeCharts, ctx.me?.isAdmin ? h('button', { class: 'btn btn-danger', type: 'button', onclick: () => { definitions = definitions.filter((d) => d !== def); render(); } }, 'Remove schedule') : null));
    }
    if (!definitions.length) list.append(emptyState({ icon: 'file', title: 'No scheduled reports', text: 'Add a weekly or monthly email report. Delivery uses the configured SMTP settings.' }));
    if (ctx.me?.isAdmin) list.append(h('div', { class: 'btn-group' }, h('button', { class: 'btn', type: 'button', onclick: () => { definitions.push({ id: `report-${Date.now()}`, name: 'Availability report', enabled: true, groups: [], tags: [], period: 'weekly', recipients: [], targetAvailability: 99.9 }); render(); } }, 'Add schedule'), h('button', { class: 'btn btn-primary', type: 'button', onclick: save }, 'Save schedules')));
  }
  try { if (ctx.me?.isAdmin) definitions = await api.get('/api/reports'); if (ctx.me?.isAdmin) render(); } catch (e) { list.append(h('p', { class: 'note' }, e.message)); }
  return { destroy() { destroyed = true; } };
}

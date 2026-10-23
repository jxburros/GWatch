// Settings › Maintenance windows.
//
// Part of settings.js, split out for #118: the code is the
// view's own, moved here unchanged inside create(shell), which hands it the
// view's shared state and helpers.

import { api } from '../../api.js';
import { h, icon, clear, field, textInput, numberInput, textarea, selectInput, toggle, toast, confirmDialog, openModal, emptyState } from '../../components.js';
import { dateTime, duration, toLocalInput, fromLocalInput, weekdayShort, timeShort } from '../../fmt.js';

/** @param {Record<string, any>} shell the view's state and helpers */
export function create(shell) {
  const { state, unit } = shell;

  async function tabMaintenance() {
    const wrap = h('section', { class: 'card' });
    let nodes = [];
    let groups = [];
    const load = async () => {
      const [list, ns, gs] = await Promise.all([api.get('/api/maintenance'), api.get('/api/nodes').catch(() => []), api.get('/api/groups').catch(() => ({ groups: [] }))]);
      nodes = ns || []; groups = gs?.groups || [];
      render(list || []);
    };
    const scopeLabel = (w) => w.nodeId ? (nodes.find((n) => n.id === w.nodeId)?.name || `node ${w.nodeId}`) : w.group ? `group ${w.group}` : 'all nodes';
    const whenLabel = (w) => w.weekdays?.length ? `Weekly on ${w.weekdays.map(weekdayShort).join(', ')} at ${w.startTime || timeShort(w.startAt)} (${w.timeZone || 'server local time — legacy'}) for ${duration((w.durationMinutes || 60) * 60)}` : `${dateTime(w.startAt, { seconds: false })} → ${dateTime(w.endAt, { seconds: false })}`;
    const render = (list) => {
      clear(wrap);
      wrap.append(h('div', { class: 'card-head' }, h('h2', null, 'Maintenance windows'), h('button', { class: 'btn btn-primary', type: 'button', onclick: () => edit(null) }, icon('plus'), 'New window')),
        h('p', { class: 'lead' }, 'Planned reboots and updates should not page you. Checks keep running; alerts are held and the node is marked as in maintenance.'));
      if (!list.length) { wrap.append(emptyState({ icon: 'wrench', title: 'No maintenance windows', text: 'Create one for a planned reboot, or a weekly one for your update schedule.', compact: true })); return; }
      const rows = h('div', null);
      for (const w of list) {
        rows.append(h('div', { class: 'maint-row' },
          h('div', null,
            h('div', { class: 'm-title' }, w.name, w.active ? h('span', { class: 'pill status-maintenance' }, icon('wrench'), 'Active now') : null, w.enabled === false ? h('span', { class: 'tag' }, 'disabled') : null),
            h('div', { class: 'm-sub' }, `${scopeLabel(w)} · ${whenLabel(w)}`), w.notes ? h('div', { class: 'm-sub' }, w.notes) : null),
          h('div', { class: 'btn-group' }, h('button', { class: 'btn btn-sm', type: 'button', onclick: () => edit(w) }, icon('edit'), 'Edit'), h('button', { class: 'btn btn-sm btn-danger', type: 'button', onclick: async () => { if (await confirmDialog({ title: `Delete "${w.name}"?`, confirmLabel: 'Delete', danger: true })) { try { await api.del(`/api/maintenance/${w.id}`); toast('Window deleted', { kind: 'success' }); load(); } catch (e) { toast(e.message, { kind: 'error' }); } } } }, icon('trash'), 'Delete'))));
      }
      wrap.append(rows);
    };
    const edit = (existing) => {
      const w = existing ? { ...existing, weekdays: [...(existing.weekdays || [])] } : { name: '', nodeId: null, group: '', enabled: true, startAt: new Date(Date.now() + 5 * 60e3).toISOString(), endAt: new Date(Date.now() + 65 * 60e3).toISOString(), weekdays: [], durationMinutes: 60, notes: '' };
      const name = textInput({ value: w.name, placeholder: 'e.g. Sunday updates, NAS reboot' });
      const scope = selectInput({ options: [{ value: 'all', label: 'All nodes' }, { value: 'group', label: 'A group' }, { value: 'node', label: 'One node' }], value: w.nodeId ? 'node' : w.group ? 'group' : 'all' });
      const groupSel = selectInput({ options: groups.map((g) => ({ value: g.name, label: g.name })), value: w.group || groups[0]?.name || '' });
      const nodeSel = selectInput({ options: nodes.map((n) => ({ value: n.id, label: n.name })), value: w.nodeId ?? nodes[0]?.id ?? '' });
      const groupField = field({ label: 'Group', input: groupSel }); const nodeField = field({ label: 'Node', input: nodeSel });
      const syncScope = () => { groupField.hidden = scope.value !== 'group'; nodeField.hidden = scope.value !== 'node'; };
      scope.addEventListener('change', syncScope); syncScope();
      const kind = selectInput({ options: [{ value: 'once', label: 'One-off (start and end)' }, { value: 'weekly', label: 'Weekly recurring' }], value: w.weekdays?.length ? 'weekly' : 'once' });
      const start = h('input', { type: 'datetime-local', value: toLocalInput(w.startAt) });
      const end = h('input', { type: 'datetime-local', value: toLocalInput(w.endAt) });
      const startTime = h('input', { type: 'time', value: w.startTime || toLocalInput(w.startAt).slice(11, 16) || '03:00' });
      const timeZone = textInput({ value: w.timeZone || Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC', placeholder: 'e.g. America/Chicago' });
      const durationIn = numberInput({ value: w.durationMinutes || 60, min: 5 });
      const days = h('div', { class: 'weekdays', role: 'group', 'aria-label': 'Weekdays' });
      [1, 2, 3, 4, 5, 6, 0].forEach((dIdx) => days.append(h('label', null, h('input', { type: 'checkbox', value: dIdx, checked: w.weekdays.includes(dIdx) }), weekdayShort(dIdx))));
      const onceFields = h('div', { class: 'form-grid' }, field({ label: 'Starts', input: start }), field({ label: 'Ends', input: end }));
      const weeklyFields = h('div', { class: 'stack-sm' }, field({ label: 'Days', input: days }), h('div', { class: 'form-grid' }, field({ label: 'Start time', input: startTime }), field({ label: 'Time zone', input: timeZone, help: 'IANA zone. The weekly time stays in this zone when daylight saving time changes.' }), field({ label: 'Duration', input: unit(durationIn, 'minutes') })));
      const syncKind = () => { onceFields.hidden = kind.value !== 'once'; weeklyFields.hidden = kind.value !== 'weekly'; };
      kind.addEventListener('change', syncKind); syncKind();
      const notes = textarea({ value: w.notes || '', rows: 2, placeholder: 'Optional' });
      const enabledT = toggle({ label: 'Enabled', checked: w.enabled !== false });
      const form = h('form', { class: 'stack', onsubmit: (e) => { e.preventDefault(); submit(); } },
        field({ label: 'Name', input: name }),
        h('div', { class: 'form-grid' }, field({ label: 'Applies to', input: scope }), groupField, nodeField, field({ label: 'Schedule', input: kind })),
        onceFields, weeklyFields, field({ label: 'Notes', input: notes }), enabledT);
      const m = openModal({ title: existing ? 'Edit maintenance window' : 'New maintenance window', wide: true, body: form, footer: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, 'Cancel'), h('button', { class: 'btn btn-primary', type: 'button', onclick: () => submit() }, existing ? 'Save' : 'Create')] });
      async function submit() {
        if (!name.value.trim()) { name.focus(); toast('Give the window a name', { kind: 'error' }); return; }
        const payload = { ...w, name: name.value.trim(), nodeId: scope.value === 'node' ? Number(nodeSel.value) : null, group: scope.value === 'group' ? groupSel.value : '', notes: notes.value, enabled: enabledT.input.checked };
        if (kind.value === 'once') {
          payload.weekdays = []; payload.durationMinutes = 0; payload.startTime = ''; payload.timeZone = '';
          payload.startAt = fromLocalInput(start.value); payload.endAt = fromLocalInput(end.value);
          if (!payload.startAt || !payload.endAt || new Date(payload.endAt) <= new Date(payload.startAt)) { toast('The end must be after the start', { kind: 'error' }); return; }
        } else {
          payload.weekdays = [...days.querySelectorAll('input:checked')].map((i) => Number(i.value));
          if (!payload.weekdays.length) { toast('Pick at least one weekday', { kind: 'error' }); return; }
          payload.startTime = startTime.value || '03:00'; payload.timeZone = timeZone.value.trim();
          const [hh, mm] = payload.startTime.split(':').map(Number);
          const sd = new Date(); sd.setHours(hh, mm, 0, 0);
          payload.startAt = sd.toISOString(); payload.durationMinutes = Number(durationIn.value) || 60;
          payload.endAt = new Date(sd.getTime() + payload.durationMinutes * 60e3).toISOString();
        }
        delete payload.active;
        try {
          if (existing) await api.put(`/api/maintenance/${existing.id}`, payload); else await api.post('/api/maintenance', payload);
          m.close(); toast(existing ? 'Window saved' : 'Window created', { kind: 'success' }); load();
        } catch (e) { toast(e.message, { kind: 'error' }); }
      }
      setTimeout(() => name.focus(), 50);
    };
    await load();
    state.panelRefresh = load;
    return wrap;
  }

  return { tabMaintenance };
}

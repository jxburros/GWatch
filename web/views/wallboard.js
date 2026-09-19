// Showing a wallboard inside the application: the same board a projected
// display shows, minus the projection. The chrome is hidden (see
// body.wallboard-mode in app.css) so this is what it will look like on the
// wall, and one tap gets back out.

import { api } from '../api.js';
import { h, icon, clear, emptyState } from '../components.js';
import { createWall } from '../wall-render.js';

export async function mount(root, ctx) {
  const state = { wall: null, boards: [], destroyed: false };
  ctx.setTitle('Wallboard');

  const host = h('div');
  root.append(host);

  let boards = [];
  try {
    boards = await api.get('/api/wallboards');
  } catch (e) {
    clear(root).append(h('div', { class: 'card', style: { margin: '40px' } }, emptyState({
      icon: 'alert', title: 'Could not load wallboards', text: e.message,
      actions: h('a', { class: 'btn', href: '#/dashboard' }, 'Back'),
    })));
    return { destroy() { state.destroyed = true; } };
  }
  if (state.destroyed) return { destroy() {} };
  state.boards = boards;

  if (!boards.length) {
    clear(root).append(h('div', { class: 'card', style: { margin: '40px' } }, emptyState({
      icon: 'monitor',
      title: 'No wallboards yet',
      text: 'A wallboard is a screen-sized view of the network for a spare display. Make one and it can be projected to any browser on your network.',
      actions: [
        h('a', { class: 'btn btn-primary admin-only', href: '#/wallboards' }, icon('plus'), 'Make a wallboard'),
        h('a', { class: 'btn', href: '#/dashboard' }, 'Back'),
      ],
    })));
    return { destroy() { state.destroyed = true; } };
  }

  const id = Number(ctx.params.id) || boards[0].id;
  const board = boards.find((b) => b.id === id) || boards[0];
  ctx.setTitle(board.name);

  state.wall = createWall(host, () => api.get(`/api/wallboards/${board.id}/view`));
  // The way back out, and the way to the next board. Both sit over the board
  // rather than in it, so they are absent from what a projected screen shows.
  host.append(h('div', { class: 'wall-exit' },
    boards.length > 1
      ? h('select', {
        'aria-label': 'Which wallboard',
        onchange: (e) => ctx.navigate(`/wallboard/${e.target.value}`),
      }, ...boards.map((b) => h('option', { value: String(b.id), selected: b.id === board.id ? '' : null }, b.name)))
      : null,
    h('a', { class: 'btn btn-sm', href: '#/wallboards' }, icon('settings'), 'Configure'),
    h('a', { class: 'btn btn-sm', href: '#/dashboard' }, icon('logout'), 'Exit')));

  return {
    refresh: () => state.wall?.refresh(),
    themeChanged() { state.wall?.redraw(); },
    destroy() { state.destroyed = true; state.wall?.destroy(); },
  };
}

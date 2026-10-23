// Settings › Rules.
//
// Part of settings.js, split out for #118: the code is the
// view's own, moved here unchanged inside create(shell), which hands it the
// view's shared state and helpers.

import { rulesPanel } from '../rules.js';

/** @param {Record<string, any>} shell the view's state and helpers */
export function create(shell) {
  const { state } = shell;

  // Notification rules across nodes and checks (#31); the tab itself lives
  // in views/rules.js beside its editor and rows.
  async function tabRules() {
    return rulesPanel({ isDestroyed: () => state.destroyed, onRefresh: (fn) => { state.panelRefresh = fn; } });
  }

  return { tabRules };
}

// Settings › General.
//
// Part of settings.js, split out for #118: the code is the
// view's own, moved here unchanged inside create(shell), which hands it the
// view's shared state and helpers.

import { h, field, textInput, selectInput } from '../../components.js';

// The ping methods GeneralSettings.PingMethod accepts. A check may override
// the global choice with the same values, plus "" for "follow the setting".
const PING_METHODS = [
  { value: 'auto', label: 'Auto (recommended)' },
  { value: 'builtin', label: 'Built-in sender' },
  { value: 'system', label: 'System ping command' },
];

/** @param {Record<string, any>} shell the view's state and helpers */
export function create(shell) {
  const { loadSettings, numField, saveBar, saveSettings, state } = shell;

  async function tabGeneral() {
    const s = state.settings || await loadSettings();
    const g = s.general;
    const name = textInput({ value: g.instanceName || '', placeholder: 'GWatch', oninput: () => { g.instanceName = name.value; } });
    const pingMethod = selectInput({ options: PING_METHODS, value: g.pingMethod || 'auto', onchange: () => { g.pingMethod = pingMethod.value; } });
    return h('form', { class: 'stack', onsubmit: (e) => { e.preventDefault(); saveSettings(); } },
      h('section', { class: 'card' }, h('h2', null, 'General'), h('p', { class: 'lead' }, 'Defaults for new checks and protection against overloading the machine.'),
        h('div', { class: 'form-grid' },
          field({ label: 'Instance name', input: name, help: 'Shown in the wallboard and in alert emails.' }),
          numField(g, 'wallboardRefreshSeconds', 'Wallboard refresh', { unitLabel: 'seconds', min: 5 }),
          numField(g, 'defaultIntervalSeconds', 'Default check interval', { unitLabel: 'seconds', min: 10, help: 'Used for new checks; each check can override it.' }),
          numField(g, 'defaultTimeoutSeconds', 'Default timeout', { unitLabel: 'seconds', min: 1 }),
          numField(g, 'minIntervalSeconds', 'Minimum interval allowed', { unitLabel: 'seconds', min: 5, help: 'Prevents accidental very aggressive schedules.' }),
          numField(g, 'maxConcurrentChecks', 'Max concurrent checks', { min: 1, help: 'How many checks may run at the same time.' }),
          numField(g, 'latencyWarnMs', 'Latency warning default', { unitLabel: 'ms', help: '0 = off. Marks checks degraded when slower than this.' }),
          numField(g, 'packetLossWarnPct', 'Packet-loss warning default', { unitLabel: '%', help: '0 = off. Applies to ping checks.' }),
        )),
      // Ping is the one check type whose mechanics a reader may have to take a
      // hand in: sending an echo request needs a socket the operating system
      // may refuse to hand out. The setting sits here with the other defaults,
      // and any individual check can override it.
      h('section', { class: 'card' }, h('h2', null, 'Ping'),
        h('p', { class: 'lead' }, 'How ping checks send their echo requests. Any individual check can override this.'),
        h('div', { class: 'form-grid' },
          field({
            label: 'Ping method',
            input: pingMethod,
            help: 'Auto tries the built-in sender and falls back to the system ping command. Built-in never runs an external program. System ping uses the operating system\'s ping command, which is the way out where raw sockets are not permitted.',
          }),
        ),
        h('hr', { class: 'divider' }), saveBar()));
  }

  return { tabGeneral };
}

// Settings › Appearance.
//
// Part of settings.js, split out for #118: the code is the
// view's own, moved here unchanged inside create(shell), which hands it the
// view's shared state and helpers.

import { h, icon, clear, textInput, toggle, toast, applyTheme, applyAccent, ACCENT_PRESETS, hexToRgb, applyDensity, currentDensity } from '../../components.js';
import { tipsEnabled, setTipsEnabled, resetTips, seenCount, resetOnboarding, TIPS } from '../../tips.js';

/** @param {Record<string, any>} shell the view's state and helpers */
export function create(shell) {
  const { ctx, isAdmin, loadSettings, saveBar, saveSettings, state } = shell;

  async function tabAppearance() {
    // A viewer cannot read settings, so their theme choice is theirs alone:
    // it starts from what /api/me reported and is remembered by this browser.
    const g = isAdmin ? (state.settings || await loadSettings()).general : { theme: ctx.me?.theme || 'dark', accentColor: ctx.me?.accentColor || '#43c9c0' };
    const themes = [
      { value: 'dark', label: 'Dark', desc: 'Low-glare, for wall displays and night owls.', bg: '#0f1114', card: '#16181d', fg: '#f2f4f6' },
      { value: 'light', label: 'Light', desc: 'Bright, high contrast on white.', bg: '#f1f2f4', card: '#ffffff', fg: '#10151d' },
      { value: 'system', label: 'System', desc: 'Follow the operating system preference.', bg: 'linear-gradient(90deg, #0f1114 50%, #f1f2f4 50%)', card: 'linear-gradient(90deg, #16181d 50%, #ffffff 50%)', fg: '#9aa3ac' },
    ];
    const themeWrap = h('div', { class: 'theme-options', role: 'radiogroup', 'aria-label': 'Theme' });
    const renderThemes = () => {
      clear(themeWrap);
      for (const t of themes) {
        themeWrap.append(h('button', { type: 'button', role: 'radio', class: `theme-option ${g.theme === t.value ? 'active' : ''}`, 'aria-checked': g.theme === t.value ? 'true' : 'false', onclick: () => { g.theme = t.value; applyTheme(t.value); renderThemes(); } },
          h('div', { class: 'preview', style: { background: t.bg } }, h('i', { style: { background: t.card } }), h('i', { style: { background: t.card, margin: '8px', boxShadow: `inset 0 3px 0 rgb(var(--accent-rgb))` } })),
          h('b', null, t.label), h('span', null, t.desc)));
      }
    };
    renderThemes();
    // #32: density is a per-browser preference, like the pinned sidebar — it
    // never touches state.settings and there is nothing here for a viewer to
    // be locked out of, so it renders the same way for both roles.
    const densities = [
      { value: 'compact', label: 'Compact (default)', desc: 'Tighter rows, so a long node list scrolls easily.' },
      { value: 'comfortable', label: 'Breathing room', desc: 'The roomier spacing GWatch used to ship with.' },
    ];
    const densityWrap = h('div', { class: 'theme-options density-options', role: 'radiogroup', 'aria-label': 'List density' });
    const renderDensities = () => {
      clear(densityWrap);
      const current = currentDensity();
      for (const d of densities) {
        densityWrap.append(h('button', { type: 'button', role: 'radio', class: `theme-option ${current === d.value ? 'active' : ''}`, 'aria-checked': current === d.value ? 'true' : 'false', onclick: () => { applyDensity(d.value); renderDensities(); } },
          h('div', { class: `density-preview ${d.value === 'comfortable' ? 'is-comfortable' : ''}` }, h('i'), h('i'), h('i')),
          h('b', null, d.label), h('span', null, d.desc)));
      }
    };
    renderDensities();
    const swatches = h('div', { class: 'swatches', role: 'radiogroup', 'aria-label': 'Accent colour' });
    const custom = h('input', { type: 'color', value: g.accentColor || '#43c9c0', 'aria-label': 'Custom accent colour', oninput: () => { g.accentColor = custom.value; applyAccent(custom.value); renderSwatches(); } });
    const hex = textInput({ value: g.accentColor || '#43c9c0', class: 'mono', style: { maxWidth: '110px' }, 'aria-label': 'Accent hex', oninput: () => { if (hexToRgb(hex.value)) { g.accentColor = hex.value.toLowerCase(); custom.value = g.accentColor; applyAccent(g.accentColor); renderSwatches(); } } });
    const renderSwatches = () => {
      clear(swatches);
      for (const p of ACCENT_PRESETS) swatches.append(h('button', { type: 'button', role: 'radio', class: `swatch ${(g.accentColor || '').toLowerCase() === p.hex ? 'active' : ''}`, 'aria-checked': (g.accentColor || '').toLowerCase() === p.hex ? 'true' : 'false', title: p.name, style: { background: p.hex }, onclick: () => { g.accentColor = p.hex; custom.value = p.hex; hex.value = p.hex; applyAccent(p.hex); renderSwatches(); } }));
      swatches.append(custom, hex);
    };
    renderSwatches();
    return h('form', { class: 'stack', onsubmit: (e) => { e.preventDefault(); saveSettings(); } },
      h('section', { class: 'card' }, h('h2', null, 'Theme'),
        h('p', { class: 'lead' }, isAdmin
          ? 'Changes apply immediately; press Save to keep them for every browser that opens this GWatch.'
          : 'Changes apply immediately and are remembered by this browser. Only an administrator can change the theme for everyone.'),
        themeWrap),
      h('section', { class: 'card' }, h('h2', null, 'Density'),
        h('p', { class: 'lead' }, 'How tightly node rows, checks and lists are packed. Remembered by this browser only — there is no shared setting for it.'),
        densityWrap),
      h('section', { class: 'card' }, h('h2', null, 'Accent colour'), h('p', { class: 'lead' }, 'Used for buttons, highlights, the active navigation item and the first chart line.'), swatches,
        h('div', { class: 'row', style: { marginTop: '14px', gap: '8px' } }, h('button', { class: 'btn btn-primary', type: 'button' }, 'Primary button'), h('button', { class: 'btn', type: 'button' }, 'Button'), h('span', { class: 'chip active' }, 'Active chip'), h('a', { href: '#/settings/appearance' }, 'A link')),
        isAdmin ? h('hr', { class: 'divider' }) : null, isAdmin ? saveBar() : null),
      guidanceCard());
  }

  /* Tips and the first-run tour are a property of this browser, not of the
     monitor, so they sit with the theme rather than in the saved settings and
     a viewer may change them as freely as an administrator. */
  function guidanceCard() {
    const seen = h('p', { class: 'note' });
    const refresh = () => { seen.textContent = tipsEnabled() ? `${seenCount()} of ${TIPS.length} tips shown so far.` : 'Tips are off. Nothing appears until you turn them on.'; };
    const tips = toggle({ label: 'Show tips as I go', checked: tipsEnabled(), onChange: (on) => { setTipsEnabled(on); refresh(); } });
    refresh();
    return h('section', { class: 'card' }, h('h2', null, 'Guidance'),
      h('p', { class: 'lead' }, 'Tips are small hints that point at one control and explain what it does. Each one appears once and is then gone. Remembered by this browser only.'),
      h('div', { class: 'stack-sm' }, tips, seen,
        h('div', { class: 'btn-group u-mt-6' },
          h('button', { class: 'btn', type: 'button', onclick: () => { resetTips(); refresh(); toast('Tips reset', { kind: 'success' }); } }, icon('refresh'), 'Reset tips'),
          h('button', { class: 'btn', type: 'button', onclick: () => { resetOnboarding(); ctx.navigate('/onboarding'); } }, icon('play'), 'Restart onboarding'),
          h('a', { class: 'btn', href: '#/help' }, icon('help'), 'Open Help'))));
  }

  return { tabAppearance };
}

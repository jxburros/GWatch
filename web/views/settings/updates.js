// Settings › Updates.
//
// Part of settings.js, split out for #118: the code is the
// view's own, moved here unchanged inside create(shell), which hands it the
// view's shared state and helpers.

import { api } from '../../api.js';
import { h, icon, clear, replace, field, textInput, toggle, toast, confirmDialog, emptyState, skeleton, banner, busy } from '../../components.js';
import { relTime } from '../../fmt.js';

/** @param {Record<string, any>} shell the view's state and helpers */
export function create(shell) {
  const { hcard, loadSettings, numField, saveBar, saveSettings, state } = shell;

  async function tabUpdates() {
    const s = state.settings || await loadSettings();
    const g = s.general;
    const u = s.updates || (s.updates = { checkAutomatically: true, checkIntervalHours: 24, includePrerelease: false, promptOnOpen: true });
    const box = h('div', { class: 'update-box' });
    const listBox = h('div', { class: 'stack-sm' });
    let releases = null;      // null until loaded; [] when the fetch failed
    let showPre = !!u.includePrerelease;
    let packaged = false;     // installed by a package manager: nothing here installs
    const repo = textInput({ value: g.updateRepo || 'jxburros/GWatch', class: 'mono', placeholder: 'owner/repository', oninput: () => { g.updateRepo = repo.value; } });

    const install = async (btn, version, label) => {
      const what = version ? `GWatch ${version}` : 'the update';
      const ok = await confirmDialog({
        title: `Install ${what} now?`,
        message: `GWatch downloads the release executable, checks its signature, replaces the current one (the previous version is kept as .old) and restarts. Monitoring pauses for a few seconds.`,
        confirmLabel: 'Install and restart', danger: true,
      });
      if (!ok) return;
      const done = busy(btn, label || 'Installing…');
      try {
        const r = await api.post('/api/update/apply', version ? { version } : {});
        toast(`Installed ${r.info?.latestVersion || 'update'}; restarting…`, { kind: 'success', timeout: 8000 });
      } catch (e) { toast(e.message, { kind: 'error', timeout: 8000 }); }
      await load(); done();
    };

    const render = (doc) => {
      clear(box);
      const st = doc?.status || {};
      const last = st.last;
      box.append(h('div', { class: 'health-cards' },
        hcard(doc?.version || '?', 'Installed version', st.executable || ''),
        hcard(last ? (last.latestVersion || '—') : '—', 'Latest release', last?.publishedAt ? `published ${relTime(last.publishedAt)}` : (last ? 'no release found' : 'not checked yet')),
        hcard(last ? (last.error ? h('span', { class: 'text-down' }, 'Check failed') : last.updateAvailable ? h('span', { class: 'text-degraded' }, 'Update available') : h('span', { class: 'text-up' }, 'Up to date')) : h('span', { class: 'muted' }, 'Unknown'), 'Status',
          st.lastCheckAt ? `checked ${relTime(st.lastCheckAt)}${st.nextCheckAt ? ` · next ${relTime(st.nextCheckAt)}` : ''}` : (st.autoCheck ? 'first check shortly' : 'automatic checks are off'))));
      if (last?.error) box.append(banner('down', last.error));
      if (st.lastError) box.append(banner('down', `Last update attempt failed: ${st.lastError}`));
      if (st.applied) box.append(banner('up', `A new version was installed ${relTime(st.lastApplyAt)}. ${st.restarting ? 'The service is restarting — reload this page in a few seconds.' : 'Restart the service to run it.'}`));
      if (last?.updateAvailable && !last.error) {
        box.append(banner('info', h('span', null,
          h('b', null, `GWatch ${last.latestVersion} is available`),
          last.prerelease ? ' — a pre-release' : '',
          last.currentIsDev ? ' (you are running a development build).' : '.',
          last.assetName ? ` The release includes ${last.assetName} for this platform.` : ' The release has no executable for this platform; build from source or use the installer.')));
        if (last.releaseNotes) box.append(h('details', { class: 'collapsible' }, h('summary', null, icon('chevronRight'), 'Release notes'), h('div', { class: 'update-notes' }, last.releaseNotes)));
      }
      // A package manager owns a packaged build: GWatch still says what is
      // new, but installing it is the package manager's job (#126).
      const wasPackaged = packaged;
      packaged = !!st.packagedBy;
      applyBtn.disabled = packaged;
      if (packaged) box.append(h('p', { class: 'note' }, icon('lock'), ` Updated by ${st.packageManager || st.packagedBy}: this copy was installed from a package, so GWatch does not replace it. To update, ${st.packageCommand}`));
      else if (!st.canApply && st.executable) box.append(h('p', { class: 'note' }, icon('lock'), ' The executable directory is not writable by the service, so updates cannot be installed from here. Re-run the installer with the new build instead.'));
      if (wasPackaged !== packaged && releases !== null) renderList();
    };

    // The version list. Anything newer than what is running can be installed,
    // not only the most recent: a release that has been out a while is
    // sometimes the one you want. Pre-releases are shown only when asked for,
    // and say what they are.
    const renderList = () => {
      clear(listBox);
      // The warning belongs to the list, not to the card: it has to come and
      // go with the toggle, which only redraws this box.
      if (showPre) listBox.append(banner('warn', 'Pre-releases are published for testing and are not finished work. Installing one is at your own risk.'));
      if (releases === null) { listBox.append(skeleton({ lines: 3 })); return; }
      const shown = releases.filter((r) => showPre || !r.prerelease);
      if (!shown.length) { listBox.append(emptyState({ icon: 'rocket', title: 'No releases listed', text: 'Either the repository has published none, or GitHub could not be reached.', compact: true })); return; }
      for (const r of shown) {
        const tags = [];
        if (r.running) tags.push(h('span', { class: 'pill' }, 'Running'));
        if (r.prerelease) tags.push(h('span', { class: 'pill pill-warn' }, 'Pre-release'));
        if (!r.installable) tags.push(h('span', { class: 'pill' }, 'No build for this platform'));
        const btn = h('button', { class: 'btn btn-sm', type: 'button', disabled: packaged || !r.newer || !r.installable, onclick: () => install(btn, r.version) },
          icon('rocket'), r.newer ? 'Install' : 'Older');
        listBox.append(h('div', { class: 'release-row' },
          h('div', { class: 'release-main' },
            h('div', { class: 'release-title' }, h('b', null, r.name || `GWatch ${r.version}`), ...tags),
            h('div', { class: 'muted small' }, r.publishedAt ? `published ${relTime(r.publishedAt)}` : 'not dated',
              r.assetName ? ` · ${r.assetName}` : '',
              ' · ', h('a', { href: r.url, target: '_blank', rel: 'noopener' }, 'notes'))),
          btn));
      }
    };

    const load = async () => { try { render(await api.get('/api/update/status')); } catch (e) { replace(box, banner('down', e.message)); } };
    const loadList = async () => {
      try { const doc = await api.get('/api/update/releases'); releases = doc.releases || []; }
      catch { releases = []; }
      renderList();
    };

    const checkBtn = h('button', { class: 'btn btn-primary', type: 'button', onclick: async () => {
      const done = busy(checkBtn, 'Checking…');
      try { const info = await api.post('/api/update/check'); toast(info.updateAvailable ? `Update available: ${info.latestVersion}` : `Up to date (${info.latestVersion})`, { kind: info.updateAvailable ? 'info' : 'success' }); }
      catch (e) { toast(e.message, { kind: 'error' }); }
      releases = null; renderList();
      await Promise.all([load(), loadList()]); done();
    } }, icon('refresh'), 'Check now');
    const applyBtn = h('button', { class: 'btn btn-danger', type: 'button', onclick: () => install(applyBtn, '') }, icon('rocket'), 'Install the newest version');

    const preToggle = toggle({ label: 'Show pre-releases', checked: showPre, onChange: (v) => { showPre = v; renderList(); } });
    const autoRow = toggle({ label: 'Check for updates automatically', checked: !!u.checkAutomatically, onChange: (v) => { u.checkAutomatically = v; } });
    const promptRow = toggle({ label: 'Ask me when I open GWatch and an update is waiting', checked: !!u.promptOnOpen, onChange: (v) => { u.promptOnOpen = v; } });
    const preSetting = toggle({ label: 'Offer pre-releases as updates', checked: !!u.includePrerelease, onChange: (v) => { u.includePrerelease = v; showPre = v; preToggle.input.checked = v; renderList(); } });

    await Promise.all([load(), loadList()]);
    state.panelRefresh = load;
    return h('div', { class: 'stack' },
      h('section', { class: 'card' }, h('h2', null, 'Application updates'),
        h('p', { class: 'lead' }, 'GWatch checks the GitHub releases of the repository below, installs the version you choose, verifies its signature and restarts itself.'),
        box,
        h('div', { class: 'btn-group u-mt-12' }, checkBtn, applyBtn),
        h('div', { class: 'stack-sm u-mt-12' }, h('a', { href: `https://github.com/${g.updateRepo || 'jxburros/GWatch'}/releases`, target: '_blank', rel: 'noopener', class: 'small' }, icon('external'), ' Open the releases page'))),
      h('section', { class: 'card' }, h('h2', null, 'Available versions'),
        h('p', { class: 'lead' }, 'Install any version newer than the one running — the most recent, or an earlier one you would rather have. An older version than the one running is not installed over it: the database has already been migrated.'),
        h('div', { class: 'row-between u-mb-8' }, preToggle),
        listBox),
      h('form', { class: 'card', onsubmit: (e) => { e.preventDefault(); saveSettings(); } }, h('h2', null, 'How GWatch checks'),
        h('p', { class: 'lead' }, 'Checking asks GitHub for the list of releases and nothing else — no account, no machine identity, nothing about what you monitor. Switch it off and GWatch contacts nobody unless you press Check now.'),
        h('div', { class: 'stack-sm' }, autoRow, promptRow, preSetting),
        h('div', { class: 'form-grid u-mt-12' },
          numField(u, 'checkIntervalHours', 'Check every', { unitLabel: 'hours', min: 1, help: 'Between 1 and 720 hours (30 days). The default is once a day.' }),
          field({ label: 'GitHub repository', input: repo, help: 'Release assets are expected to be named gwatch-<os>-<arch>[.exe], which is what the release job publishes.' })),
        h('hr', { class: 'divider' }), saveBar()));
  }

  return { tabUpdates };
}

// Settings › About.
//
// Part of settings.js, split out for #118: the code is the
// view's own, moved here unchanged inside create(shell), which hands it the
// view's shared state and helpers.

import { api } from '../../api.js';
import { h } from '../../components.js';
import { isBeta } from '../../fmt.js';
import { REPO_URL } from './shared.js';

const GWATCH_COPYRIGHT = 'Copyright (c) 2026 JX Holdings. Original developers: Jeffrey Guntly and Garrett Guntly.';

// Go module dependencies from go.mod, with licenses confirmed by reading each
// module's LICENSE file under $(go env GOMODCACHE).
const DEPENDENCIES = [
  { name: 'modernc.org/sqlite', use: 'the embedded database that stores history, events and settings', license: 'BSD-3-Clause' },
  { name: 'jackc/pgx', use: 'talks to a PostgreSQL server when Settings › Database points at one', license: 'MIT' },
  { name: 'go-sql-driver/mysql', use: 'talks to a MySQL or MariaDB server when Settings › Database points at one', license: 'MPL-2.0' },
  { name: 'golang.org/x/crypto', use: 'password hashing for accounts and the access password', license: 'BSD-3-Clause' },
];

/** @param {Record<string, any>} shell the view's state and helpers */
export function create(shell) {
  const { hcard, state } = shell;

  async function tabAbout() {
    const v = state.version || await api.get('/api/version').catch(() => null);
    const versionText = v?.version ? `v${v.version}` : 'unknown';
    const platformText = v?.platform || 'unknown';
    const beta = isBeta(v?.version) ? h('span', { class: 'beta-tag' }, 'Beta') : null;
    return h('div', { class: 'stack' },
      h('section', { class: 'card' },
        h('div', { style: { display: 'flex', gap: '16px', alignItems: 'center', flexWrap: 'wrap' } },
          h('img', { src: 'logo.svg', alt: 'GWatch logo', width: '64', height: '64' }),
          h('div', null,
            h('h2', { style: { marginBottom: '2px' } }, 'GWatch', beta),
            h('p', { class: 'lead', style: { margin: 0 } }, 'A self-hosted monitor for the machines, sites and services on your own network.'),
            h('div', { class: 'muted u-mt-4' }, `${versionText} · ${platformText}`))),
        h('div', { class: 'health-cards u-mt-14' },
          hcard(versionText, 'Installed version'), hcard(platformText, 'Platform')),
        isBeta(v?.version)
          ? h('p', { class: 'note u-mt-12' }, 'This is a beta. GWatch is usable and its data is kept safely, but features and the layout can still change between releases, and a bug here is more likely than it will be at 1.0. ', h('a', { href: 'https://github.com/jxburros/GWatch/issues', target: '_blank', rel: 'noopener' }, 'Report anything that looks wrong.'))
          : null),
      h('section', { class: 'card' },
        h('h2', null, 'Copyright & credit'),
        h('p', null, GWATCH_COPYRIGHT),
        h('p', { class: 'muted' }, 'This notice, the GWatch name and the logo above are required attribution under the project license (see below) and may not be removed or obscured in a copy or derivative of this software.')),
      h('section', { class: 'card' },
        h('h2', null, 'Links'),
        h('div', { class: 'stack-sm' },
          h('div', null, h('a', { href: REPO_URL, target: '_blank', rel: 'noopener' }, 'GitHub repository')),
          h('div', null, h('a', { href: `${REPO_URL}/releases`, target: '_blank', rel: 'noopener' }, 'Releases')),
          h('div', null, h('a', { href: `${REPO_URL}/tree/main/docs`, target: '_blank', rel: 'noopener' }, 'Documentation')),
          h('div', null, h('a', { href: `${REPO_URL}/blob/main/LICENSE`, target: '_blank', rel: 'noopener' }, 'LICENSE')),
          h('div', null, h('a', { href: `${REPO_URL}/blob/main/TRADEMARKS.md`, target: '_blank', rel: 'noopener' }, 'TRADEMARKS.md')))),
      h('section', { class: 'card' },
        h('h2', null, 'License summary'),
        h('p', null, 'GWatch is source-available under the GWatch Community License 1.0. You are free to use, modify and distribute it, including for commercial deployment and support work. You may not resell it unmodified as a competing product, and every copy must keep the required attribution above. This summary is informal — the ', h('a', { href: `${REPO_URL}/blob/main/LICENSE`, target: '_blank', rel: 'noopener' }, 'LICENSE'), ' file is what actually governs.')),
      h('section', { class: 'card' },
        h('h2', null, 'Acknowledgements'),
        h('p', { class: 'lead' }, 'GWatch is written in Go and depends on a small number of open-source libraries:'),
        h('div', { class: 'stack-sm' }, ...DEPENDENCIES.map((d) => h('div', null, h('b', null, d.name), h('span', { class: 'tag', style: { marginLeft: '8px' } }, d.license), h('div', { class: 'muted u-fs-13' }, d.use))))));
  }

  return { tabAbout };
}

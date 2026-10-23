// Settings › Database.
//
// Part of settings.js, split out for #118: the code is the
// view's own, moved here unchanged inside create(shell), which hands it the
// view's shared state and helpers.

import { api } from '../../api.js';
import { h, icon, clear, replace, field, textInput, numberInput, selectInput, toast, confirmDialog, banner, busy } from '../../components.js';
import { bytes } from '../../fmt.js';

/** @param {Record<string, any>} shell the view's state and helpers */
export function create(shell) {
  const { hcard } = shell;

  // Which database GWatch keeps its data in. Unlike every other tab this does
  // not edit the settings document: the connection has to be known before the
  // database is open, so it lives in database.json in the data directory and
  // only takes effect at the next start. See docs/DATABASE.md.
  const DB_DRIVERS = [
    { value: 'sqlite', label: 'SQLite file in the data directory (default)' },
    { value: 'postgres', label: 'PostgreSQL server' },
    { value: 'mysql', label: 'MySQL or MariaDB server' },
  ];
  // An empty mode lets the service choose: verify-full for a server across
  // the network, prefer for one on this machine (docs/DATABASE.md).
  const DB_TLS = [
    { value: '', label: 'Automatic (verify the certificate unless the server is on this machine)' },
    { value: 'prefer', label: 'Prefer (encrypt when the server offers it; can be downgraded)' },
    { value: 'require', label: 'Require (encrypt, do not verify the certificate)' },
    { value: 'verify-ca', label: 'Verify CA' },
    { value: 'verify-full', label: 'Verify CA and host name' },
    { value: 'disable', label: 'Disable' },
  ];
  async function tabDatabase() {
    const status = await api.get('/api/database');
    const cfg = { ...status.saved };
    if (!cfg.driver) cfg.driver = 'sqlite';
    if (!cfg.sslMode) cfg.sslMode = '';
    const wrap = h('div', { class: 'stack' });
    const active = status.active || {};
    const activeCard = h('section', { class: 'card' }, h('h2', null, 'Database in use'),
      h('p', { class: 'lead' }, 'Where this copy of GWatch is reading and writing right now.'),
      h('div', { class: 'health-cards u-mt-10' },
        hcard({ sqlite: 'SQLite', postgres: 'PostgreSQL', mysql: 'MySQL / MariaDB' }[active.driver] || active.driver || '—', 'Backend', active.label || ''),
        hcard(h('span', { class: 'mono u-fs-13' }, active.description || '—'), 'Location'),
        hcard(String(active.schemaVersion ?? '—'), 'Schema version'),
        hcard(bytes(active.sizeBytes || 0), 'Size')));

    const pending = h('div', { role: 'status' });
    const renderPending = (st) => {
      clear(pending);
      if (st.restartRequired) pending.append(banner('warn', h('span', null, h('b', null, 'GWatch will use this database after the service is restarted. '), `Saved: ${describe(st.saved)}. Until then it keeps using ${st.active?.description || 'the current database'}.`)));
    };
    const describe = (c) => c.driver === 'sqlite' || !c.driver ? 'the SQLite file in the data directory' : `${c.driver === 'postgres' ? 'PostgreSQL' : 'MySQL/MariaDB'} at ${c.host || '(connection string)'}${c.port ? ':' + c.port : ''}/${c.database || ''}${c.schema ? ` (schema ${c.schema})` : ''}`;
    renderPending(status);

    const driver = selectInput({ options: DB_DRIVERS, value: cfg.driver, onchange: () => { cfg.driver = driver.value; if (cfg.driver === 'postgres' && (!cfg.port || cfg.port === 3306)) { cfg.port = 5432; port.value = '5432'; } if (cfg.driver === 'mysql' && (!cfg.port || cfg.port === 5432)) { cfg.port = 3306; port.value = '3306'; } syncServerFields(); } });
    const host = textInput({ value: cfg.host || '', placeholder: 'db.example.lan', autocomplete: 'off', oninput: () => { cfg.host = host.value.trim(); } });
    const port = numberInput({ value: cfg.port || (cfg.driver === 'mysql' ? 3306 : 5432), min: 1, max: 65535, oninput: () => { cfg.port = Number(port.value) || 0; } });
    const user = textInput({ value: cfg.user || '', placeholder: 'gwatch', autocomplete: 'off', oninput: () => { cfg.user = user.value.trim(); } });
    const password = h('input', { type: 'password', value: cfg.password || '', autocomplete: 'new-password', placeholder: cfg.password ? '' : 'Database password', oninput: () => { cfg.password = password.value; } });
    const database = textInput({ value: cfg.database || '', placeholder: 'gwatch', autocomplete: 'off', oninput: () => { cfg.database = database.value.trim(); } });
    const schema = textInput({ value: cfg.schema || '', placeholder: 'public', autocomplete: 'off', oninput: () => { cfg.schema = schema.value.trim(); } });
    const tls = selectInput({ options: DB_TLS, value: cfg.sslMode, onchange: () => { cfg.sslMode = tls.value; } });
    const schemaField = field({ label: 'Schema (PostgreSQL)', input: schema, help: 'Leave empty for the user\u2019s default search path, normally "public".' });
    const serverGrid = h('div', { class: 'form-grid' },
      field({ label: 'Host', input: host }), field({ label: 'Port', input: port }),
      field({ label: 'User', input: user }), field({ label: 'Password', input: password, help: 'Stored encrypted with this computer\u2019s gwatch.key, like the SMTP password.' }),
      field({ label: 'Database', input: database }), schemaField,
      h('div', { class: 'span-2' }, field({ label: 'TLS', input: tls, help: 'For MySQL, "require" encrypts without verifying and the two verify options both check the server certificate.' })));
    const sqliteNote = h('p', { class: 'note' }, 'The embedded SQLite file needs no configuration and is right for almost every install. Choose a server only if you already run one and want GWatch\u2019s history kept there.');
    const syncServerFields = () => { const server = cfg.driver !== 'sqlite'; serverGrid.hidden = !server; sqliteNote.hidden = server; schemaField.hidden = cfg.driver !== 'postgres'; };
    syncServerFields();

    const result = h('div', { role: 'status' });
    const payload = () => ({ driver: cfg.driver, host: cfg.host || '', port: Number(cfg.port) || 0, user: cfg.user || '', password: cfg.password || '', database: cfg.database || '', schema: cfg.driver === 'postgres' ? (cfg.schema || '') : '', sslMode: cfg.sslMode || '' });
    const testBtn = h('button', { class: 'btn', type: 'button', onclick: async () => {
      const done = busy(testBtn, 'Connecting\u2026');
      try { const r = await api.post('/api/database/test', payload()); replace(result, banner('up', r.message || `Connected (${r.serverVersion}).`)); }
      catch (e) { replace(result, banner('down', e.message)); }
      done();
    } }, icon('database'), 'Test connection');
    const saveBtn = h('button', { class: 'btn btn-primary', type: 'button', onclick: async () => {
      if (cfg.driver !== 'sqlite') {
        const ok = await confirmDialog({ title: 'Switch database?', message: `GWatch will start using ${describe(payload())} the next time it is started. Existing data stays in the current database and is not copied; run "gwatch migrate-db" first if it should come along (docs/DATABASE.md).`, confirmLabel: 'Save' });
        if (!ok) return;
      }
      const done = busy(saveBtn, 'Saving\u2026');
      try {
        const st = await api.put('/api/database', payload());
        cfg.password = st.saved?.password || '';
        password.value = cfg.password;
        renderPending(st);
        replace(result, banner(st.restartRequired ? 'warn' : 'up', st.restartRequired ? 'Saved. GWatch will use this database after the service is restarted.' : 'Saved. This is the database already in use.'));
        toast('Database settings saved', { kind: 'success' });
      } catch (e) { replace(result, banner('down', e.message)); }
      done();
    } }, icon('save'), 'Save');
    const formCard = h('section', { class: 'card' }, h('h2', null, 'Database for the next start'),
      h('p', { class: 'lead' }, 'Point GWatch at your own PostgreSQL or MySQL/MariaDB server, or go back to the SQLite file. Saving writes database.json in the data directory; nothing changes until GWatch is restarted.'),
      pending,
      h('div', { class: 'form-grid', style: { marginBottom: '12px' } }, h('div', { class: 'span-2' }, field({ label: 'Database', input: driver }))),
      sqliteNote, serverGrid,
      h('div', { class: 'form-actions u-mt-12' }, testBtn, saveBtn), result);
    const howCard = h('section', { class: 'card' }, h('h2', null, 'Moving existing data'),
      h('p', null, 'Saving here changes where GWatch looks, not what is there. To take the nodes, history and accounts in the SQLite file along, stop the service and run ', h('code', null, 'gwatch migrate-db'), ' once: it copies everything into the database saved here and leaves the SQLite file in place as a fallback. Backups and restores work the same on every backend.'),
      h('p', { class: 'note' }, 'Settings › Database and ', h('code', null, 'docs/DATABASE.md'), ' describe creating the database and a least-privilege user on the server, the ', h('code', null, '--db-*'), ' flags and ', h('code', null, 'GWATCH_DB_*'), ' variables that override this file, and what a server backend does not do (the pre-upgrade file copy).'));
    wrap.append(activeCard, formCard, howCard);
    return wrap;
  }

  return { tabDatabase };
}

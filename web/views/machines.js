// Machines: a machine is not a separate kind of thing in GWatch, it is a node
// with a hardware check. So this is a helper module, not a page or a route —
// there is no #/machines URL and nothing in app.js imports it as a view. It
// hands the node list the pairing flow (pairMachine) and hands the node
// detail view the readings panel for that node's machine (hardwarePanel).
// The old #/hardware/<key> URLs still work: app.js redirects them to the
// owning node.

import { api, subscribeUpdates } from '../api.js';
import {
  h, icon, clear, replace, statusPill, emptyState, skeleton, rangeChips, relTimeEl,
  openModal, confirmDialog, field, textInput, selectInput, toast,
} from '../components.js';
import { LineChart, SERIES_COLORS } from '../charts.js';
import { bytes, pct, duration, dateTime, num, agentIsBehind } from '../fmt.js';

// Shading for a usage bar. These are display thresholds only — what actually
// raises an alert is the hardware check's own configuration, which the person
// running GWatch chose.
const WARN_AT = 80;
const CRIT_AT = 92;

/* ---------------- Pairing a machine ---------------- */

// Pairing exists because the other way round is miserable: an agent token is
// forty characters of noise, and getting it onto another computer means
// copying it through whatever channel is to hand. A pairing code is eight
// characters somebody can read off this screen and type into an installer
// prompt on the other machine — and it is worth almost nothing if it leaks,
// being good for one machine, once, for about a quarter of an hour.

export async function pairMachine(reload) {
  const name = textInput({ autocomplete: 'off', placeholder: 'e.g. Living room NAS' });
  const nodes = await api.get('/api/nodes').catch(() => []);
  const nodeSel = selectInput({
    options: [{ value: '', label: 'Not attached to a node' }, ...nodes.map((n) => ({ value: String(n.id), label: n.name }))],
    value: '',
  });
  const ok = await confirmDialog({
    title: 'Pair a machine',
    body: h('div', { class: 'stack' },
      h('p', { class: 'lead' }, 'You will get a short code to type into the agent on the other machine. It enrols that one machine and then stops working.'),
      field({ label: 'Name', input: name, help: 'How this machine appears under Hardware.' }),
      field({ label: 'Node', input: nodeSel, help: 'Optional. Attaching it lets a hardware check on that node watch this machine.' })),
    confirmLabel: 'Get a pairing code',
  });
  if (!ok) return;
  try {
    const res = await api.post('/api/agents/pairings', {
      name: name.value.trim(),
      nodeId: nodeSel.value ? Number(nodeSel.value) : null,
    });
    showPairingCode(res.code, res.pairing, reload);
  } catch (e) { toast(e.message, { kind: 'error' }); }
}

// The code is shown here and nowhere else — GWatch keeps only a fingerprint of
// it — so the command to run on the other machine is shown with it, and the
// clock runs where the reader can see it rather than expiring behind their back.
function showPairingCode(code, pairing, reload) {
  const base = `${location.protocol}//${location.host}`;
  const command = `gwatch-agent pair --server ${base} --code ${code}`;

  const codeEl = h('div', {
    class: 'mono',
    style: {
      fontSize: '38px', fontWeight: '600', letterSpacing: '0.12em', textAlign: 'center',
      padding: '18px 12px', border: '1px solid var(--line-strong)', background: 'var(--elev)',
      userSelect: 'all', wordBreak: 'break-all',
    },
  }, code);
  const clockEl = h('span', { class: 'mono' }, '—');
  const noteEl = h('p', { class: 'note' }, 'Expires in ', clockEl, '. Nothing is enrolled until the code is typed in.');

  const copy = (what, label) => h('button', { class: 'btn', type: 'button', onclick: async () => {
    try { await navigator.clipboard.writeText(what); toast(`${label} copied`, { kind: 'success' }); }
    catch { toast('Could not copy — select it and copy it by hand.', { kind: 'error' }); }
  } }, icon('copy'), label);

  const cancelBtn = h('button', { class: 'btn btn-danger', type: 'button', onclick: async () => {
    try {
      await api.del(`/api/agents/pairings/${pairing.id}`);
      toast('Pairing code cancelled', { kind: 'success' });
      modal.close();
    } catch (e) { toast(e.message, { kind: 'error' }); }
  } }, 'Cancel this code');

  const expiresAt = +new Date(pairing.expiresAt);
  const tick = () => {
    const left = expiresAt - Date.now();
    if (left > 0) {
      const secs = Math.floor(left / 1000);
      clockEl.textContent = `${Math.floor(secs / 60)}:${String(secs % 60).padStart(2, '0')}`;
      return;
    }
    clearInterval(timer);
    codeEl.style.opacity = '0.45';
    codeEl.style.textDecoration = 'line-through';
    cancelBtn.disabled = true;
    replace(noteEl, h('b', null, 'This code has expired.'), ' Close this and pair the machine again to get a fresh one.');
  };
  const timer = setInterval(tick, 1000);
  tick();

  const modal = openModal({
    title: 'Type this code on the other machine',
    wide: true,
    onClose: () => { clearInterval(timer); reload?.(); },
    body: h('div', { class: 'stack' },
      codeEl,
      noteEl,
      h('p', { class: 'lead' }, 'On the machine you want to watch, install gwatch-agent and run:'),
      h('code', { class: 'agent-setup' }, command),
      h('p', { class: 'note' }, 'The agent swaps the code for its own token, keeps the token on that machine and sends one reading to prove it worked. Use ',
        h('code', null, 'gwatch-agent install --server … --code …'), ' instead to pair and install the background service in one go — the Windows installer asks for the address and this code and does exactly that.'),
      h('p', { class: 'note' }, 'Letters only, in any case, and the dash does not matter. There is no I, L, O or U and no 0 or 1 in a code, so nothing here is the character you think it might be.'),
      h('p', { class: 'note' }, 'What the machine ends up holding can do one thing: submit its own hardware readings. It cannot read or change anything in GWatch, and GWatch never connects back to it.'),
      h('p', { class: 'note' }, 'If this GWatch is reached over HTTPS with a self-signed certificate, add ', h('code', null, '--insecure'), ' — the agent still uses TLS, it just stops checking the certificate.')),
    footer: [cancelBtn, copy(code, 'Copy code'), h('button', { class: 'btn btn-primary', type: 'button', onclick: async () => {
      try { await navigator.clipboard.writeText(command); toast('Command copied', { kind: 'success' }); }
      catch { toast('Could not copy — select the command and copy it by hand.', { kind: 'error' }); }
    } }, icon('copy'), 'Copy command')],
  });
}

// statusWording says what the status means for a machine, which is not the
// same thing it means for a check: here it is only about whether readings are
// still arriving.
function statusWording(host) {
  if (host.status === 'paused') return 'not accepted';
  if (host.stale) return 'not reporting';
  if (host.status === 'unknown') return 'waiting';
  return 'reporting';
}

/** Processor load as a percentage, falling back to load per core where a
 *  platform cannot report utilisation (macOS). */
function cpuValue(m) {
  if (m.cpu?.usagePct != null) return m.cpu.usagePct;
  if (m.cpu?.loadPerCore != null) return Math.min(100, m.cpu.loadPerCore * 100);
  return null;
}

function cpuDetail(m) {
  if (m.cpu?.usagePct != null) {
    return m.cpu.cores ? `${m.cpu.cores} cores` : '';
  }
  if (m.cpu?.loadPerCore != null) return `load ${m.cpu.load1?.toFixed(2)} across ${m.cpu.cores} cores`;
  return 'not reported';
}

function topFilesystems(m, limit) {
  return (m.filesystems || [])
    .filter((fs) => fs.totalBytes > 0)
    .slice()
    .sort((a, b) => b.usedPct - a.usedPct)
    .slice(0, limit);
}

function throughputSummary(m) {
  const rx = sumRate(m.interfaces, 'rxBytesPerSec');
  const tx = sumRate(m.interfaces, 'txBytesPerSec');
  if (rx == null && tx == null) return '';
  return `↓ ${rate(rx)}  ↑ ${rate(tx)}`;
}

function sumRate(rows, key) {
  let total = null;
  for (const r of rows || []) {
    if (r[key] != null) total = (total || 0) + r[key];
  }
  return total;
}

function rate(bytesPerSec) {
  return bytesPerSec == null ? '—' : `${bytes(bytesPerSec)}/s`;
}

function upFor(m) {
  return m.uptimeSeconds ? `up ${duration(m.uptimeSeconds)}` : '';
}

/** A labelled usage bar. Shading is by the display thresholds above. */
function usageMeter(label, value, { detail = '', level: given = null } = {}) {
  const known = value != null && !isNaN(value);
  const level = !known ? '' : given != null ? given : value >= CRIT_AT ? ' crit' : value >= WARN_AT ? ' warn' : '';
  return h('div', { class: `meter${level}` },
    h('div', { class: 'meter-head' },
      h('span', { class: 'meter-label' }, label),
      h('span', { class: 'meter-value' }, known ? pct(value, 0) : '—'),
    ),
    h('div', {
      class: 'meter-track', role: 'img',
      'aria-label': `${label}: ${known ? pct(value, 0) : 'not reported'}`,
    }, h('div', { class: 'meter-fill', style: { width: `${known ? Math.max(0, Math.min(100, value)) : 0}%` } })),
    detail ? h('div', { class: 'meter-detail muted' }, detail) : null,
  );
}

/* ---------------- The panel a node shows for its machine ---------------- */

/**
 * hardwarePanel builds the readings for one machine: what it is, what it is
 * doing now, and what it has been doing. It is a panel rather than a page
 * because the machine is the node — there is nowhere else for it to live.
 *
 * Returns { el, refresh, themeChanged, destroy }; the caller owns the element
 * and must call destroy when it drops it, or the charts leak.
 */
// `metricStatus`, when given, returns the per-metric verdicts of the hardware
// check that reads this machine ({ 'disk:/srv': 'degraded', ... }); the
// meters are then shaded by those rather than by the display cut-offs above.
// The newest agent release, asked for once per page load and shared by every
// machine panel on it. It is only ever used to draw a label: an agent updates
// itself from signed releases it verifies on its own, and GWatch has no way to
// update one — see docs/HARDWARE.md#keeping-agents-up-to-date.
//
// Anything that goes wrong here — no updater in this build, a viewer who may
// not ask, GitHub unreachable — resolves to null, and a null marks nothing.
// Being unable to find out what is newest is not evidence that a machine is
// behind.
let latestAgentPromise = null;
function latestAgentRelease() {
  latestAgentPromise ||= api.get('/api/agents/latest')
    .then((r) => (r?.version ? { version: r.version, url: r.url || '' } : null))
    .catch(() => null);
  return latestAgentPromise;
}

// agentVersionRow renders the running version, with a quiet note when a newer
// agent exists. It is deliberately not an alarm: an agent one release behind
// is still doing its job, and on automatic updates it will catch up by itself
// within a few hours.
function agentVersionRow(running, latest, releaseURL) {
  if (!agentIsBehind(running, latest)) return running;
  return h('span', null,
    running, ' ',
    h('a', {
      class: 'pill pill-warn',
      href: releaseURL || 'https://github.com/jxburros/GWatch/releases',
      target: '_blank', rel: 'noreferrer noopener',
      title: `This machine runs gwatch-agent ${running}; ${latest} has been released. An agent left on automatic updates takes it by itself.`,
    }, icon('download'), `${latest} available`));
}

export function hardwarePanel(key, { title = 'Hardware', metricStatus = null } = {}) {
  const state = { host: null, range: '24h', charts: [], destroyed: false, latestAgent: null, agentReleaseURL: null };

  const headEl = h('div', null, skeleton({ lines: 3 }));
  const snapEl = h('div', { class: 'stack' });
  const chartsEl = h('div');
  const el = h('section', { class: 'card', 'aria-label': `${title}: ${key}` },
    h('div', { class: 'card-head' }, h('h2', { class: 'card-title' }, icon('cpu'), title), h('span', { class: 'mono dim small' }, key)),
    h('div', { class: 'stack' }, headEl, snapEl, chartsEl));

  async function load() {
    try {
      state.host = await api.get(`/api/hosts/${encodeURIComponent(key)}`);
      if (state.destroyed) return;
      renderHead();
      // Drawn again once the newest release is known, rather than holding the
      // whole panel up for a question that only decides a label.
      latestAgentRelease().then((rel) => {
        if (state.destroyed || !rel || state.latestAgent === rel.version) return;
        state.latestAgent = rel.version;
        state.agentReleaseURL = rel.url;
        renderHead();
      });
      renderSnapshot();
    } catch (e) {
      replace(headEl, emptyState({ icon: 'alert', title: 'Could not load this machine', text: e.message, compact: true }));
    }
  }

  function renderHead() {
    const host = state.host;
    const m = host.metrics;
    const rows = [
      ['Status', statusPill(host.status, { label: statusWording(host) })],
      ['Source', sourceWording(host)],
      m?.ts ? ['Last reading', relTimeEl(m.ts)] : null,
      m ? ['System', [m.platform, m.arch, m.hostname].filter(Boolean).join(' · ') || '—'] : null,
      m?.bootTime ? ['Started', `${dateTime(m.bootTime)} (up ${duration(m.uptimeSeconds)})`] : null,
      m?.cpu?.model ? ['Processor', `${m.cpu.model} — ${num(m.cpu.cores)} cores`] : null,
      host.agent?.lastVersion ? ['Agent version', agentVersionRow(host.agent.lastVersion, state.latestAgent, state.agentReleaseURL)] : null,
      host.agent?.lastAddr ? ['Reported from', host.agent.lastAddr] : null,
    ].filter(Boolean);

    const dl = h('dl', { class: 'kv' });
    for (const [k, v] of rows) dl.append(h('dt', null, k), h('dd', null, v));
    replace(headEl, dl);

    if (host.warnings?.length) {
      // The collector could not read something. Say so rather than showing a
      // gap the reader has to guess at.
      headEl.append(h('div', { class: 'banner banner-info', style: { marginTop: '12px' } },
        icon('info'),
        h('div', null,
          h('strong', null, 'Some readings are not available on this machine'),
          h('ul', { class: 'note', style: { margin: '4px 0 0', paddingLeft: '18px' } },
            ...host.warnings.map((w) => h('li', null, w))),
        )));
    }
  }

  function renderSnapshot() {
    const m = state.host.metrics;
    clear(snapEl);
    if (!m) {
      snapEl.append(emptyState({
        icon: 'cpu', title: 'No reading yet', compact: true,
        text: 'Install gwatch-agent on this machine and give it the token you were shown when you registered it.',
      }));
      return;
    }

    // The check's own verdict for each metric, where a check is watching this
    // machine; the display cut-offs otherwise (the Machines list has no check
    // to ask).
    const verdicts = metricStatus ? metricStatus() : null;
    const levelFor = (metricKey, value) => {
      const st = verdicts?.[metricKey];
      if (st === 'down') return ' crit';
      if (st === 'degraded') return ' warn';
      if (st === 'up') return '';
      return meterLevel(value);
    };

    snapEl.append(h('div', null,
      h('div', { class: 'section-title' }, 'Now'),
      h('div', { class: 'host-meters' },
        usageMeter('Processor', cpuValue(m), { detail: cpuDetail(m), level: levelFor(m.cpu?.usagePct != null ? 'cpu' : 'load', cpuValue(m)) }),
        usageMeter('Memory', m.memory?.totalBytes ? m.memory.usedPct : null, {
          detail: m.memory?.totalBytes ? `${bytes(m.memory.usedBytes)} used, ${bytes(m.memory.availableBytes)} available` : '',
          level: levelFor('memory', m.memory?.usedPct),
        }),
        m.memory?.swapTotalBytes
          ? usageMeter('Swap', m.memory.swapUsedPct, { detail: `${bytes(m.memory.swapUsedBytes)} of ${bytes(m.memory.swapTotalBytes)}`, level: levelFor('swap', m.memory.swapUsedPct) })
          : null,
      ),
    ));

    if (m.filesystems?.length) {
      snapEl.append(h('div', null,
        h('div', { class: 'section-title' }, 'Disks'),
        table(['Mount', 'Device', 'Type', 'Used', 'Free', 'Size', ''],
          m.filesystems.map((fs) => [
            fs.mount,
            h('span', { class: 'mono' }, fs.device || '—'),
            fs.fsType || '—',
            pct(fs.usedPct, 0),
            bytes(fs.freeBytes),
            bytes(fs.totalBytes),
            h('div', { class: 'meter meter-inline' + levelFor(`disk:${fs.mount}`, fs.usedPct) },
              h('div', { class: 'meter-track' }, h('div', { class: 'meter-fill', style: { width: `${fs.usedPct}%` } }))),
          ])),
      ));
    }

    if (m.interfaces?.length) {
      snapEl.append(h('div', null,
        h('div', { class: 'section-title' }, 'Network'),
        table(['Interface', 'Link', 'Download', 'Upload', 'Received', 'Sent', 'Errors'],
          m.interfaces.map((n) => [
            n.name,
            n.up ? (n.speedMbit ? `${num(n.speedMbit)} Mbit/s` : 'up') : h('span', { class: 'muted' }, 'down'),
            rate(n.rxBytesPerSec),
            rate(n.txBytesPerSec),
            bytes(n.rxBytes),
            bytes(n.txBytes),
            n.rxErrors || n.txErrors || n.rxDropped || n.txDropped
              ? `${num((n.rxErrors || 0) + (n.txErrors || 0))} err, ${num((n.rxDropped || 0) + (n.txDropped || 0))} dropped`
              : h('span', { class: 'muted' }, 'none'),
          ])),
      ));
    }

    if (m.disks?.length) {
      snapEl.append(h('div', null,
        h('div', { class: 'section-title' }, 'Disk throughput'),
        table(['Device', 'Read', 'Write', 'Reads/s', 'Writes/s', 'Busy'],
          m.disks.map((d) => [
            d.name,
            rate(d.readBytesPerSec),
            rate(d.writeBytesPerSec),
            d.readOpsPerSec == null ? '—' : num(Math.round(d.readOpsPerSec)),
            d.writeOpsPerSec == null ? '—' : num(Math.round(d.writeOpsPerSec)),
            d.busyPct == null ? '—' : pct(d.busyPct, 0),
          ])),
      ));
    }
  }

  function meterLevel(v) {
    return v >= CRIT_AT ? ' crit' : v >= WARN_AT ? ' warn' : '';
  }

  function table(headers, rows) {
    return h('div', { class: 'table-wrap' },
      h('table', { class: 'table' },
        h('thead', null, h('tr', null, ...headers.map((t) => h('th', null, t)))),
        h('tbody', null, ...rows.map((cells) => h('tr', null, ...cells.map((c) => h('td', { class: typeof c === 'string' ? 'mono' : null }, c))))),
      ));
  }

  /* ---- history ---- */

  function clearCharts() {
    for (const c of state.charts) c.destroy?.();
    state.charts = [];
  }

  async function loadHistory() {
    clearCharts();
    clear(chartsEl);
    chartsEl.append(h('div', { class: 'row-between' },
      h('div', { class: 'section-title' }, 'History'),
      rangeChips(state.range, (r) => { state.range = r; loadHistory(); }),
    ));
    const body = h('div', { class: 'stack' }, skeleton({ lines: 4 }));
    chartsEl.append(body);

    let data;
    try {
      data = await api.get(`/api/hosts/${encodeURIComponent(key)}/history?range=${state.range}`);
    } catch (e) {
      replace(body, h('p', { class: 'note' }, `Could not load history: ${e.message}`));
      return;
    }
    if (state.destroyed) return;
    const samples = data.samples || [];
    if (!samples.length) {
      replace(body, h('p', { class: 'note' }, 'No readings in this range yet.'));
      return;
    }
    clear(body);

    const from = +new Date(data.from);
    const to = +new Date(data.to);
    const series = (name, key_, color) => ({
      name, color,
      points: samples.map((s) => ({ t: +new Date(s.ts), v: s[key_] == null ? null : Number(s[key_]) })),
    });
    const hasAny = (key_) => samples.some((s) => s[key_] != null);

    addChart(body, 'Processor and memory', { unit: '%', yMin: 0, yMax: 100, height: 220 }, [
      hasAny('cpuPct') ? series('Processor', 'cpuPct', SERIES_COLORS[0]) : null,
      hasAny('memPct') ? series('Memory', 'memPct', SERIES_COLORS[1]) : null,
      hasAny('swapPct') ? series('Swap', 'swapPct', SERIES_COLORS[2]) : null,
      hasAny('diskPct') ? series('Fullest disk', 'diskPct', SERIES_COLORS[3]) : null,
    ].filter(Boolean), from, to);

    if (hasAny('netRxBytesPerSec') || hasAny('netTxBytesPerSec')) {
      addChart(body, 'Network throughput', { unit: 'B/s', yMin: 0, height: 200 }, [
        series('Download', 'netRxBytesPerSec', SERIES_COLORS[0]),
        series('Upload', 'netTxBytesPerSec', SERIES_COLORS[4]),
      ], from, to);
    }
    if (hasAny('diskReadBytesPerSec') || hasAny('diskWriteBytesPerSec')) {
      addChart(body, 'Disk throughput', { unit: 'B/s', yMin: 0, height: 200 }, [
        series('Read', 'diskReadBytesPerSec', SERIES_COLORS[5]),
        series('Write', 'diskWriteBytesPerSec', SERIES_COLORS[2]),
      ], from, to);
    }
  }

  function addChart(parent, chartTitle, opts, series, from, to) {
    const host = h('div');
    const chart = new LineChart(host, { legend: true, alwaysLegend: true, ariaLabel: chartTitle, title: chartTitle, ...opts });
    state.charts.push(chart);
    chart.setData({ series, from, to });
    parent.append(h('div', null, h('div', { class: 'section-title' }, chartTitle), host));
  }

  const started = load().then(loadHistory);
  const unsub = subscribeUpdates((u) => { if (!u || u.kind === 'host') load(); });

  return {
    el,
    ready: started,
    refresh: () => load().catch(() => {}),
    // Redraws the snapshot from the reading already in hand, for when the
    // check's verdicts changed but the machine has not reported again.
    redraw() { if (state.host && !state.destroyed) renderSnapshot(); },
    themeChanged() { for (const c of state.charts) c.scheduleDraw?.(); },
    destroy() { state.destroyed = true; unsub(); clearCharts(); },
  };
}

function sourceWording(host) {
  switch (host.source) {
    case 'local': return 'The computer GWatch runs on';
    case 'agent': return 'An agent on this machine sends its readings to GWatch. GWatch never connects to it.';
    case 'url': return 'GWatch reads this machine’s metrics endpoint.';
    default: return host.source || '—';
  }
}

// Settings › AI & MCP.
//
// Part of settings.js, split out for #118: the code is the
// view's own, moved here unchanged inside create(shell), which hands it the
// view's shared state and helpers.

import { api } from '../../api.js';
import { h, icon, toast, banner } from '../../components.js';
import { relTime } from '../../fmt.js';
import { REPO_URL } from './shared.js';

/** @param {Record<string, any>} shell the view's state and helpers */
export function create(shell) {
  const { createKey, state } = shell;

  // The MCP companion (gwatch-mcp) is a separate program that talks to this
  // GWatch with an API key. Nothing here changes a setting; the tab is the
  // set-up guide, the trust model in plain words, and the agent skill.
  async function tabMcp() {
    const status = await api.get('/api/mcp/status').catch(() => null);
    return h('div', { class: 'stack' }, mcpIntroCard(), mcpSetupCard(), mcpScopeCard(), mcpSkillCard(status));
  }

  // A code block with a copy button, for the pieces of setup that are meant
  // to be pasted somewhere else rather than typed.
  function copyBlock(text, what) {
    const copy = h('button', { class: 'btn btn-sm', type: 'button', onclick: async () => {
      try { await navigator.clipboard.writeText(text); toast(`${what} copied`, { kind: 'success' }); }
      catch { toast('Could not copy — select the text and copy it by hand.', { kind: 'error' }); }
    } }, icon('copy'), 'Copy');
    return h('div', { class: 'mcp-code' }, h('code', { class: 'agent-setup' }, text), copy);
  }

  function mcpIntroCard() {
    const item = (t, d) => h('div', null, h('b', null, t), h('div', { class: 'muted u-fs-13' }, d));
    return h('section', { class: 'card' }, h('h2', null, 'AI assistants and MCP'),
      h('p', { class: 'lead' }, 'gwatch-mcp is a small companion program that lets an AI assistant — Claude Desktop, Claude Code or any other MCP client — ask this GWatch what is up, what is down and since when. It is optional, separate from GWatch itself, and off until you set it up.'),
      h('div', { class: 'stack-sm u-mt-8' },
        item('It runs where the assistant runs', 'Usually your own computer. It talks to GWatch over the same API a browser uses, with an API key you create here. GWatch never connects out to the assistant.'),
        item('Read-only unless you say otherwise', 'A read-only key lets the assistant answer questions. Changing what is monitored takes a read & write key and a flag on gwatch-mcp — two separate decisions, both yours.'),
        item('Some things are never on offer', 'No key of any scope can reach settings, backups, accounts, other keys, automation or updates, so neither can an assistant.')),
      h('p', { class: 'note' }, 'The full manual, including the trust model in detail, is ',
        h('a', { href: `${REPO_URL}/blob/main/mcp/README.md`, target: '_blank', rel: 'noopener' }, 'mcp/README.md on GitHub', icon('external')), '.'));
  }

  function mcpSetupCard() {
    const origin = `${location.protocol}//${location.host}`;
    const desktopConfig = JSON.stringify({ mcpServers: { gwatch: { command: 'gwatch-mcp', env: { GWATCH_URL: origin, GWATCH_API_KEY: 'gw_paste_your_read_only_key_here' } } } }, null, 2);
    const claudeCode = `claude mcp add gwatch gwatch-mcp -e GWATCH_URL=${origin} -e GWATCH_API_KEY=gw_paste_your_read_only_key_here`;
    const check = `gwatch-mcp check --url ${origin} --api-key gw_…`;
    const step = (title, ...body) => h('li', null, h('b', null, title), ...body);
    // Minting a key from here reuses the Users & access dialog; the list on
    // that tab is what refreshes, so there is nothing to reload on this one.
    const newKey = h('button', { class: 'btn btn-sm', type: 'button', onclick: () => createKey(async () => {}) }, icon('key'), 'Create a read-only key');
    return h('section', { class: 'card' }, h('h2', null, 'Set it up'),
      h('ol', { class: 'mcp-steps' },
        step('Create a read-only API key. ',
          h('p', null, 'Under ', h('a', { href: '#/settings/users' }, 'Users & access'), ' › API keys, or with the button below. Name it after the assistant ("Claude Desktop"), keep the scope read-only, and copy the key when it is shown — it is shown once.'),
          h('div', null, newKey)),
        step('Install gwatch-mcp on the computer the assistant runs on. ',
          h('p', null, 'Every GWatch release includes a gwatch-mcp binary for each platform; put it somewhere on your PATH. Or, with Go installed:'),
          copyBlock('go install github.com/jxburros/GWatch/mcp/cmd/gwatch-mcp@latest', 'Install command')),
        step('Tell the assistant about it. ',
          h('p', null, 'Claude Desktop reads ', h('code', null, 'claude_desktop_config.json'), ' (macOS: ', h('code', null, '~/Library/Application Support/Claude/'), ', Windows: ', h('code', null, '%APPDATA%\\Claude\\'), '). Add this, with your key in place of the placeholder:'),
          copyBlock(desktopConfig, 'Claude Desktop config'),
          h('p', null, 'Claude Code takes one command instead:'),
          copyBlock(claudeCode, 'Claude Code command'),
          h('p', { class: 'note' }, 'Any other MCP client: run gwatch-mcp as a stdio server with the same two environment variables. Keep the key in the environment rather than on the command line, so it stays out of the process list. If the assistant is not on this network, use the address it reaches GWatch by; docs/REMOTE-ACCESS.md covers doing that safely.')),
        step('Check it. ',
          h('p', null, 'From that same computer, this reports the connection, the key’s scope and which tools the assistant will see:'),
          copyBlock(check, 'Check command'))),
      h('p', { class: 'note' }, 'The address above is the one this browser is using to reach GWatch. The assistant needs one that works from where it runs.'));
  }

  function mcpScopeCard() {
    const item = (t, d) => h('div', null, h('b', null, t), h('div', { class: 'muted u-fs-13' }, d));
    return h('section', { class: 'card' }, h('h2', null, 'What the assistant can and cannot do'),
      h('div', { class: 'stack-sm u-mt-8' },
        item('With a read-only key', 'The overview, nodes and their checks, recent results, history over a range, the event timeline, groups and tags, the node templates, and whether the monitor itself is healthy. Nine tools; nothing that changes anything.'),
        item('With a read & write key and --allow-write', 'Also create, change, enable, disable, run and silence nodes, add notes to the timeline, test a check without saving it, and delete a node — which additionally has to be confirmed in the tool call. Both gates have to be open: GWatch refuses a write from a read-only key whatever gwatch-mcp was started with.'),
        item('Never, whatever the key', 'Settings, backups and restores, updates, user accounts, API keys, automation triggers and endpoints, the configuration export and the service log. There are no tools for these, and a key could not use them if there were.')),
      h('p', { class: 'note' }, 'Every change an assistant makes is recorded in the event timeline under the key’s name, the same as any other API key.'));
  }

  // The skill is a short document that teaches an assistant how to use the
  // tools well. It has its own version; the card says when it was last
  // downloaded and, only when the skill has moved on since, mentions it in an
  // inline note. Nothing outside this card announces it.
  function mcpSkillCard(status) {
    const version = status?.skillVersion;
    const dl = h('a', { class: 'btn btn-primary', href: '/api/mcp/skill', download: version ? `gwatch-skill-${version}.zip` : 'gwatch-skill.zip', onclick: () => {
      // The download itself is what the server records; this only keeps the
      // "last downloaded" line honest without a reload.
      setTimeout(() => { state.panelRefresh?.(); }, 800);
    } }, icon('download'), version ? `Download skill ${version}` : 'Download skill');
    const md = h('a', { class: 'btn btn-sm', href: '/api/mcp/skill?format=md', download: 'SKILL.md' }, 'SKILL.md only');
    const last = status?.lastDownloadedAt
      ? h('p', { class: 'muted u-fs-13' }, `Last downloaded ${relTime(status.lastDownloadedAt)} (version ${status.lastDownloadedVersion})${status.lastDownloadedBy ? ` by ${status.lastDownloadedBy}` : ''}.`)
      : h('p', { class: 'muted u-fs-13' }, 'Not downloaded yet.');
    return h('section', { class: 'card' },
      h('div', { class: 'card-head' }, h('h2', null, 'Agent skill'), version ? h('span', { class: 'chip' }, `Version ${version}`) : null),
      h('p', { class: 'lead' }, 'A short guide that teaches the assistant how to use GWatch well: start with the overview, how to drill into a problem, what the statuses mean, and how to be careful with the write tools. The tools describe themselves; the skill teaches judgement.'),
      status?.updateAvailable ? banner('info', `The skill has been updated since it was last downloaded (${status.lastDownloadedVersion} → ${status.skillVersion}). Download it again when convenient and replace the copy the assistant uses.`) : null,
      status ? h('div', { class: 'btn-group' }, dl, md) : h('p', { class: 'note' }, 'This build does not include the skill.'),
      last,
      h('p', { class: 'note' }, 'Where to put it: for Claude Code, unzip it into ', h('code', null, '~/.claude/skills/'), ' (or a project’s ', h('code', null, '.claude/skills/'), ') so the file lands at ', h('code', null, 'skills/gwatch/SKILL.md'), '. For Claude Desktop and assistants without a skills folder, paste the body of SKILL.md into the assistant’s instructions.'));
  }

  return { tabMcp };
}

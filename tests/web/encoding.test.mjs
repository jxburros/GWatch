// Guards web/ against double-encoded UTF-8 (mojibake): text that was UTF-8,
// read back as cp1252 and saved as UTF-8 again, so an em dash becomes "â€”"
// on screen. The 0.6.0 release shipped exactly that in the header indicator
// labels. Every em dash, ellipsis, curly quote and › leaves the byte pair
// for "â€" behind when it is mangled this way, so that pair is the tell.
// Binary assets (fonts, images) are skipped: their bytes are not text.

import test from 'node:test';
import assert from 'node:assert/strict';
import { readdirSync, readFileSync } from 'node:fs';
import { join, extname, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../../', import.meta.url));
const webDir = join(root, 'web');
const binary = new Set(['.png', '.jpg', '.jpeg', '.gif', '.ico', '.webp', '.woff', '.woff2', '.ttf', '.otf', '.syso', '.exe', '.bmp', '.zip', '.gz', '.db']);
const mojibake = Buffer.from('â€', 'utf8');

function* textFiles(dir) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) { if (!skipDirs.has(entry.name)) yield* textFiles(path); }
    else if (!binary.has(extname(entry.name).toLowerCase())) yield path;
  }
}

// The rest of the source: documentation, Go strings that reach the screen,
// e-mails and logs, scripts and packaging. (marketing/ is left out: it quotes
// the mangled text on purpose, to describe what was fixed.)
const otherDirs = ['docs', 'internal', 'cmd', 'scripts', 'packaging', 'mcp', 'skill'].map(d => join(root, d));
const skipDirs = new Set(['node_modules', '.git', 'dist']);

function* sourceFiles() {
  yield* textFiles(webDir);
  for (const dir of otherDirs) {
    try { yield* textFiles(dir); } catch { /* not in this checkout */ }
  }
  for (const entry of readdirSync(root, { withFileTypes: true })) {
    if (entry.isFile() && extname(entry.name).toLowerCase() === '.md') yield join(root, entry.name);
  }
}

test('no source file contains double-encoded UTF-8 ("â€")', () => {
  const hits = [];
  for (const path of sourceFiles()) {
    const bytes = readFileSync(path);
    if (!bytes.includes(mojibake)) continue;
    bytes.toString('utf8').split('\n').forEach((line, i) => {
      if (line.includes('â€')) hits.push(`${relative(root, path)}:${i + 1}: ${line.trim().slice(0, 100)}`);
    });
  }
  assert.deepEqual(hits, [], `mojibake found — re-decode these as cp1252→UTF-8:\n${hits.join('\n')}`);
});

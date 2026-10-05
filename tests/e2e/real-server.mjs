// Starts a real service with disposable data; never uses the installed monitor.
import { spawn, spawnSync } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
const dir = mkdtempSync(join(tmpdir(), 'gwatch-browser-'));
const binary = process.env.GWATCH_BINARY ? resolve(process.env.GWATCH_BINARY) : join(dir, process.platform === 'win32' ? 'gwatch.exe' : 'gwatch');
if (!process.env.GWATCH_BINARY) {
  const build = spawnSync('go', ['build', '-o', binary, '.'], { stdio: 'inherit' });
  if (build.status !== 0) { rmSync(dir, { recursive: true, force: true }); process.exit(build.status || 1); }
}
const child = spawn(binary, ['run', '--data-dir', join(dir, 'data'), '--listen', `127.0.0.1:${process.env.PORT || 4173}`], { stdio: 'inherit', windowsHide: true });
for (const signal of ['SIGTERM', 'SIGINT']) process.on(signal, () => child.kill(signal));
child.on('exit', (code) => { rmSync(dir, { recursive: true, force: true }); process.exit(code || 0); });

// A static file server for web/, for the Playwright suite in this directory.
//
// The browser tests need the interface served over HTTP (module scripts do
// not load from file://), but they do not need the Go service: opened with
// ?mock=1 the page installs web/mock.js over fetch and answers every API
// call itself. So this serves the files in web/ and nothing else, the way
// the Go binary's embedded handler does — including /wall for wall.html,
// which is the address a projected wallboard is opened at.
//
// Started by playwright.config.mjs (see its webServer); runs on its own too:
//     node tests/e2e/serve.mjs            # http://127.0.0.1:4173/?mock=1
//     PORT=8000 node tests/e2e/serve.mjs

import { createServer } from 'node:http';
import { createReadStream, statSync } from 'node:fs';
import { extname, join, normalize, sep } from 'node:path';
import { fileURLToPath } from 'node:url';

const webDir = fileURLToPath(new URL('../../web/', import.meta.url));
const port = Number(process.env.PORT) || 4173;

const TYPES = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.mjs': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
  '.ico': 'image/x-icon',
  '.json': 'application/json; charset=utf-8',
  '.woff2': 'font/woff2',
  '.woff': 'font/woff',
  '.ttf': 'font/ttf',
  '.txt': 'text/plain; charset=utf-8',
};

const server = createServer((req, res) => {
  let path = decodeURIComponent(new URL(req.url, 'http://localhost').pathname);
  if (path === '/' || path === '/index.html') path = '/index.html';
  else if (path === '/wall') path = '/wall.html';
  // Resolve inside web/ only: a path that climbs out of it is refused.
  const file = normalize(join(webDir, path));
  if (!file.startsWith(webDir) || file.includes(`${sep}..${sep}`)) { res.writeHead(403).end(); return; }
  let st;
  try { st = statSync(file); } catch { st = null; }
  if (!st || !st.isFile()) {
    // Anything under /api/ is the mock's job in the browser; a request that
    // still reaches here means mock mode is off, so say so plainly.
    if (path.startsWith('/api/')) { res.writeHead(503, { 'Content-Type': 'application/json' }).end(JSON.stringify({ error: 'no service behind tests/e2e/serve.mjs — open the page with ?mock=1' })); return; }
    res.writeHead(404, { 'Content-Type': 'text/plain' }).end('not found');
    return;
  }
  res.writeHead(200, { 'Content-Type': TYPES[extname(file).toLowerCase()] || 'application/octet-stream', 'Content-Length': st.size, 'Cache-Control': 'no-store' });
  createReadStream(file).pipe(res);
});

server.listen(port, '127.0.0.1', () => {
  console.log(`serving ${webDir} at http://127.0.0.1:${port}/?mock=1`);
});

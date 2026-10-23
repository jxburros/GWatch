// #118: `npm run typecheck` only reports on the web/ files that opt in with
// `// @ts-check` (see tsconfig.json). These are the ones that must stay
// checked — the shared toolkit every view is built from, the API client, the
// formatters and the two big shared editors — so dropping the line from one
// of them, which would quietly take it out of CI's type check, fails here.
// Add a file to the list when you bring it under the checker.

import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const CHECKED = ['components.js', 'api.js', 'fmt.js', 'chart-config.js', 'netmap.js'];

for (const file of CHECKED) {
  test(`web/${file} is type checked`, () => {
    const src = readFileSync(fileURLToPath(new URL(`../../web/${file}`, import.meta.url)), 'utf8');
    assert.equal(src.split('\n')[0].trim(), '// @ts-check', `web/${file} must start with // @ts-check`);
  });
}

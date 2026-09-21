// Shared scaffolding for the browser tests: opening a page of the interface
// in mock mode with a given theme, and waiting for the view to have drawn.

/** The first mock node's id: web/mock.js numbers nodes from 21 upwards. */
export const FIRST_NODE = 21;

/** Every route web/app.js knows, as the hash path after "#". Node detail
 *  and the node editor use the first mock node; settings lists every
 *  section, since each is its own page. */
export const ROUTES = [
  'dashboard', 'nodes', 'nodes/new', 'nodes/bulk', `nodes/${FIRST_NODE}`, `nodes/${FIRST_NODE}/edit`,
  'charts', 'incidents', 'audit', 'audit/log', 'audit/exports',
  'settings/general', 'settings/appearance', 'settings/indicators', 'settings/users', 'settings/network', 'settings/alerts',
  'settings/automation', 'settings/hardware', 'settings/mcp', 'settings/retention', 'settings/maintenance', 'settings/backups', 'settings/database',
  'settings/updates', 'settings/health', 'settings/about',
  'help', 'wallboards', 'wallboard', 'login', 'onboarding',
];

/** What the page remembers between visits, written before any script runs so
 *  the first paint is already in the wanted theme. The tour is marked done
 *  (except when the tour itself is the page), and tips are off, so a popover
 *  cannot land on top of whatever is being checked. */
export async function prepare(page, { theme = 'dark', route = '' } = {}) {
  await page.addInitScript(({ theme, onboarding }) => {
    try {
      localStorage.setItem('gw.theme', theme);
      localStorage.setItem('gw.tips.enabled', '0');
      if (onboarding) localStorage.setItem('gw.onboarding.done', '1'); else localStorage.removeItem('gw.onboarding.done');
    } catch { /* storage unavailable */ }
  }, { theme, onboarding: route !== 'onboarding' });
}

/** Open a route of the application in mock mode and wait until its view has
 *  content and no skeleton is still standing in for it. The mock answers
 *  after a short random delay, like a real service, hence the waits. */
export async function openApp(page, route, { theme = 'dark' } = {}) {
  await prepare(page, { theme, route });
  await page.goto(`/?mock=1#/${route}`);
  await page.waitForFunction(() => {
    const view = document.getElementById('view');
    return view && view.children.length > 0 && !view.querySelector('.skeleton, .skeleton-block');
  }, null, { timeout: 15000 });
  // Live widgets and charts fill in after the first paint; give them a beat.
  await page.waitForTimeout(400);
}

/** The projected wallboard page (web/wall.html), served at /wall as the Go
 *  binary serves it, showing the mock's one board. */
export async function openWall(page, { theme = 'dark' } = {}) {
  await page.goto(`/wall?mock=1&id=1${theme === 'light' ? '&mode=light' : ''}`);
  await page.waitForSelector('.wall-root .wall-panel', { timeout: 15000 });
  await page.waitForTimeout(400);
}

/** Open a confirm dialog from the running page's own components module. */
export async function openConfirm(page) {
  await page.evaluate(async () => {
    const { confirmDialog } = await import('/components.js');
    window.__confirm = false;
    confirmDialog({ title: 'Remove this?', message: 'It will be gone for good.', confirmLabel: 'Remove', danger: true }).then((ok) => { window.__confirm = ok; });
  });
  await page.waitForSelector('[role="dialog"]');
}

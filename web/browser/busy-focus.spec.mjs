import { test, expect } from '@playwright/test';

const ownIP = { 'X-Forwarded-For': '203.0.113.47' };

// Lets the browser's focus fix-up run (it runs with rendering) before focus is read.
const settled = (page) => page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));
const focusedName = (page) => page.evaluate(() => document.activeElement?.textContent?.trim() || document.activeElement?.tagName);

// Answers url with `answer` once `gate.open()` is called; until then each request waits.
async function gated(page, url, answer) {
  let open;
  let opened = new Promise((r) => { open = r; });
  await page.route(url, async (route) => {
    await opened;
    await route.fulfill(answer);
  });
  return {
    open: () => open(),
    close: () => { opened = new Promise((r) => { open = r; }); },
  };
}

async function newPage(browser, testInfo) {
  const { viewport, colorScheme } = testInfo.project.use;
  const context = await browser.newContext({ extraHTTPHeaders: ownIP, viewport, colorScheme });
  return { context, page: await context.newPage() };
}

// A busy button is aria-disabled, never disabled: a focused button that becomes disabled drops
// keyboard focus to the page, and nothing returns it. The outages are staged in the page: the
// real server cannot be made unreachable for one request.
test('busy focus: Try again keeps keyboard focus while it retries', async ({ browser }, testInfo) => {
  const { context, page } = await newPage(browser, testInfo);
  const me = await gated(page, '/api/auth/me', { status: 503, json: { error: 'KyCalendar is temporarily unavailable; try again', code: 'unavailable' } });
  me.open();
  await page.goto('/');
  const retry = page.getByRole('button', { name: 'Try again' });
  await retry.focus();
  me.close();
  await page.keyboard.press('Enter');
  const busy = page.getByRole('button', { name: 'Trying…' });
  await expect(busy).toHaveAttribute('aria-disabled', 'true');
  await settled(page);
  expect(await focusedName(page)).toBe('Trying…');
  me.open();
  await expect(retry).toBeVisible();
  await settled(page);
  expect(await focusedName(page)).toBe('Try again');
  await context.close();
});

test('busy focus: Refresh on Backups keeps keyboard focus while it reloads', async ({ browser }, testInfo) => {
  const { context, page } = await newPage(browser, testInfo);
  await page.goto('/');
  await page.getByLabel('Username', { exact: true }).fill('admin');
  await page.locator('input[type=password]').fill('BrowserUpdated456!');
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await page.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name: 'Backups', exact: true }).click();
  const refresh = page.getByRole('button', { name: 'Refresh' });
  await expect(refresh).toHaveAttribute('aria-disabled', 'false');
  const status = await gated(page, '/api/backup/status', { status: 502, contentType: 'text/html', body: '<html>Bad gateway</html>' });
  await refresh.focus();
  await page.keyboard.press('Enter');
  await expect(refresh).toHaveAttribute('aria-disabled', 'true');
  await settled(page);
  expect(await focusedName(page)).toBe('Refresh');
  status.open();
  await expect(page.getByText('Could not load backup status (HTTP 502)')).toBeVisible();
  await settled(page);
  expect(await focusedName(page)).toBe('Refresh');
  await context.close();
});

const PAIRED = {
  key_pinned: true, paired: true, interval_sec: 86400, recovery_url: 'https://recovery.example', recovery_key_id: 'k1',
  threshold: 2, total_shares: 3, database_driver: 'sqlite', members: ['data/kycalendar.db'], local_dir: '/app/backups', local_keep: 7, local_copies: [],
};
const UNPINNED = { key_pinned: false, paired: false, interval_sec: 0, database_driver: 'sqlite', members: ['data/kycalendar.db'] };

// Holds the next request to url until released, then answers it with a 400.
async function held(page, url) {
  let arrived, release;
  const seen = new Promise((r) => { arrived = r; });
  const gate = new Promise((r) => { release = r; });
  await page.route(url, async (route) => {
    arrived();
    await gate;
    await route.fulfill({ status: 400, json: { error: 'Refused by the test' } });
  });
  return { seen, release: () => release() };
}

// Every busy Backups action keeps keyboard focus (aria-disabled, not disabled), and its result
// shows in its own section, focused and on screen. Status and actions are staged in the page:
// the disposable server has nothing paired or pinned.
test('busy focus: every Backups action keeps keyboard focus, and its result is on screen', async ({ browser }, testInfo) => {
  const { context, page } = await newPage(browser, testInfo);
  let status = PAIRED;
  await page.route('/api/backup/status', (r) => r.fulfill({ json: status }));
  page.on('dialog', (d) => d.accept());
  await page.goto('/');
  await page.getByLabel('Username', { exact: true }).fill('admin');
  await page.locator('input[type=password]').fill('BrowserUpdated456!');
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await page.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name: 'Backups', exact: true }).click();

  const actions = [
    { name: 'Back up now', url: '/api/backup/deposit', result: true },
    { name: 'Run restore drill', url: '/api/backup/drill' },
    { name: 'Save', url: '/api/backup/schedule', before: () => page.getByLabel('Back up automatically').selectOption({ label: 'Weekly' }) },
    { name: 'Re-pair', url: '/api/backup/pair-remote', result: true, before: () => page.getByLabel('Pairing code').fill('123456') },
    { name: 'Unpair', url: '/api/backup/pairing', result: true },
    { name: 'Pin key', url: '/api/backup/pin-key', unpinned: true, before: () => page.getByLabel('Suite recovery public key').fill('not-a-key') },
    { name: 'Pair', url: '/api/backup/pair-remote', unpinned: true, result: true, before: async () => {
      await page.getByLabel('Server URL').fill('https://recovery.example');
      await page.getByLabel('Pairing code').fill('123456');
    } },
  ];
  for (const a of actions) {
    if (a.unpinned && status !== UNPINNED) {
      status = UNPINNED;
      await page.getByRole('button', { name: 'Refresh' }).click();
    }
    await a.before?.();
    const button = page.getByRole('button', { name: a.name, exact: true });
    await button.evaluate((el) => { el.dataset.probe = 'busy'; });
    const gate = await held(page, a.url);
    await button.focus();
    await page.keyboard.press('Enter');
    await gate.seen;
    await settled(page);
    expect(await page.evaluate(() => document.activeElement?.dataset.probe), `${a.name} while busy`).toBe('busy');
    expect(await page.evaluate(() => document.querySelector('[data-probe=busy]')?.getAttribute('aria-disabled')), a.name).toBe('true');
    gate.release();
    await expect.poll(() => page.evaluate(() => document.querySelector('[data-probe=busy]')?.getAttribute('aria-disabled')), `${a.name} done`).toBe('false');
    if (a.result) {
      // The error shows beside the action, focused, inside the viewport.
      await expect.poll(() => page.evaluate(() => {
        const el = document.activeElement;
        return el?.getAttribute('role') === 'alert' && el.textContent.includes('Refused by the test') && !!el.closest('.panel')?.querySelector('[data-probe=busy]');
      }), `${a.name}'s result focused in its own section`).toBe(true);
      await expect(page.locator(':focus')).toBeInViewport();
    }
    await page.unroute(a.url);
    await page.evaluate(() => document.querySelector('[data-probe=busy]')?.removeAttribute('data-probe'));
  }
  await context.close();
});

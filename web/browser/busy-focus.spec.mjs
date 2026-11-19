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

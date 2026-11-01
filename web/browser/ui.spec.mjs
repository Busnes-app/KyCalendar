import { test, expect } from '@playwright/test';

async function fits(page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
}

test('production CSP, worker, themes, keyboard and responsive shell', async ({ page, context }, testInfo) => {
  const violations = [];
  let challenges = 0;
  page.on('request', request => { if (new URL(request.url()).pathname === '/api/auth/pow-challenge') challenges++; });
  page.on('console', message => {
    if (/Content Security Policy|violates.*directive/i.test(message.text())) violations.push(message.text());
  });
  const response = await page.goto('/');
  expect(response.headers()['content-security-policy']).toContain("script-src 'self'");
  expect(response.headers()['content-security-policy']).not.toContain("script-src 'self' 'unsafe-inline'");
  await expect.poll(() => page.evaluate(async () => (await navigator.serviceWorker.getRegistration())?.active?.state)).toBe('activated');
  await page.evaluate(async () => {
    const key = (await caches.keys())[0];
    await (await caches.open(key)).put('/', new Response('<h1>Stale shell</h1>', {
      headers: { 'Content-Type': 'text/html' },
    }));
  });
  // Online navigation must refresh the shell rather than pinning a previous deploy.
  await page.reload();
  // A solved challenge stays stable while typing; a failed attempt still obtains a fresh one.
  await expect(page.getByText('Browser check passed')).toBeVisible();
  const initialChallenges = challenges;
  await page.getByLabel('Username', { exact: true }).fill('admin');
  await page.locator('input[type=password]').fill('IncorrectPassword123!');
  await page.waitForTimeout(250);
  expect(challenges).toBe(initialChallenges);
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(page.locator('form').locator('..')).toContainText(/invalid|incorrect|failed/i);
  await expect.poll(() => challenges).toBe(initialChallenges + 1);
  await fits(page);
  await page.locator('input[type=password]').fill('BrowserUpdated456!');
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  const nav = page.getByRole('navigation', { name: 'Primary' });
  await expect(nav).toBeVisible();
  await expect(page.getByRole('heading', { name: /Welcome/ })).toBeVisible();
  await fits(page);
  // Themes are chosen on the Appearance page; the shell carries no theme select.
  await expect(page.getByLabel('Color theme')).toHaveCount(0);
  // Appearance is a Settings section, behind the account footer's gear.
  const appearance = async (p) => {
    await p.getByRole('button', { name: 'Settings', exact: true }).click();
    await p.getByRole('menu', { name: 'Settings' }).getByRole('menuitem', { name: 'Appearance', exact: true }).click();
  };
  const theme = (p, pair, mode) => p.getByRole('group', { name: pair, exact: true }).getByRole('radio', { name: new RegExp(`^${mode}`) }).check();
  await appearance(page);
  await expect(page.getByRole('button', { name: 'Settings', exact: true })).toHaveAttribute('aria-current', 'page');
  await expect(nav.locator('[aria-current]')).toHaveCount(0);
  await theme(page, 'Nord', 'Light');
  await expect(page.locator('body')).toHaveCSS('background-color', 'rgb(236, 239, 244)');
  await theme(page, 'Busnes', 'Dark');
  await page.reload();
  await expect(page.locator('body')).toHaveCSS('background-color', 'rgb(24, 35, 38)');
  const other = await context.newPage();
  await other.goto('/');
  await appearance(other);
  await theme(other, 'Busnes', 'Light');
  await expect(page.locator('body')).toHaveCSS('background-color', 'rgb(248, 246, 240)');
  await other.close();
  await appearance(page);
  await theme(page, 'Busnes', 'Match system');
  await page.emulateMedia({ colorScheme: 'dark' });
  await expect(page.locator('body')).toHaveCSS('background-color', 'rgb(24, 35, 38)');
  await page.emulateMedia({ colorScheme: 'light' });
  await expect(page.locator('body')).toHaveCSS('background-color', 'rgb(248, 246, 240)');
  await nav.getByRole('button', { name: 'Overview', exact: true }).click();
  await page.emulateMedia({ colorScheme: testInfo.project.use.colorScheme });
  await nav.getByRole('button').first().focus();
  await page.keyboard.press('Tab');
  await expect(nav.getByRole('button').nth(1)).toBeFocused();
  await expect(nav.getByRole('button').nth(1)).toHaveCSS('outline-style', 'solid');
  await nav.getByRole('button', { name: 'Server settings', exact: true }).click();
  await expect(nav.getByRole('button', { name: 'Server settings', exact: true })).toHaveAttribute('aria-current', 'page');
  await expect(page.getByRole('heading', { name: 'Server settings', exact: true })).toBeVisible();
  await fits(page);
  // Device pairing was removed (R27): the shell offers no pairing entry point.
  await expect(page.getByRole('button', { name: 'Pair Device' })).toHaveCount(0);
  await page.evaluate(() => fetch('/browser-regression-uncached'));
  const cachedDynamic = await page.evaluate(async () => {
    const keys = await caches.keys();
    for (const key of keys) for (const req of await (await caches.open(key)).keys()) {
      if (/^\/(api|scim|saml|browser-regression-uncached)(\/|$)/.test(new URL(req.url).pathname)) return true;
    }
    return false;
  });
  expect(cachedDynamic).toBe(false);
  expect(violations).toEqual([]);
  await page.screenshot({ path: testInfo.outputPath('settings.png'), fullPage: true });
  await context.setOffline(true);
  const offline = await page.reload();
  expect(offline.ok()).toBe(true);
  expect(await offline.text()).toContain('id="root"');
  await context.setOffline(false);
});

import { test, expect } from '@playwright/test';

// Own client address (the harness trusts loopback as a proxy) so these sign-ins do not spend
// the login budget the other specs share.
const ownIP = { 'X-Forwarded-For': '203.0.113.24' };

test('month: legible other-month days, labelled events, honest offline state', async ({ browser }, testInfo) => {
  const { viewport, colorScheme } = testInfo.project.use;
  const context = await browser.newContext({ extraHTTPHeaders: ownIP, viewport, colorScheme });
  const page = await context.newPage();
  await page.goto('/');
  await page.getByLabel('Username', { exact: true }).fill('walter');
  await page.locator('input[type=password]').fill('WalterUpdated456!');
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(page.locator('.fc')).toBeVisible();

  const title = `Month ${testInfo.project.name} ${Date.now()}`;
  await page.getByRole('button', { name: 'New event' }).click();
  await page.getByLabel('Title').fill(title);
  await page.getByRole('dialog').getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await page.locator('.fc-dayGridMonth-button').click();

  // Other months' day numbers reach 4.5:1 against what is behind them, opacity composited.
  const ratio = await page.locator('.fc-day-other .fc-daygrid-day-number').first().evaluate((el) => {
    const rgba = (s) => s.match(/[\d.]+/g).map(Number);
    let alpha = 1;
    for (let e = el; e; e = e.parentElement) alpha *= Number(getComputedStyle(e).opacity);
    let bg = null;
    for (let e = el; e && !bg; e = e.parentElement) {
      const c = rgba(getComputedStyle(e).backgroundColor);
      if ((c[3] ?? 1) === 1) bg = c;
    }
    const [r, g, b, a = 1] = rgba(getComputedStyle(el).color);
    const fg = [r, g, b].map((v, i) => v * a * alpha + bg[i] * (1 - a * alpha));
    const lum = (c) => c.map((v) => { v /= 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; }).reduce((s, v, i) => s + v * [0.2126, 0.7152, 0.0722][i], 0);
    const [hi, lo] = [lum(fg), lum(bg)].sort((x, y) => y - x);
    return (hi + 0.05) / (lo + 0.05);
  });
  testInfo.annotations.push({ type: 'other-month day contrast', description: ratio.toFixed(2) });
  expect(ratio).toBeGreaterThanOrEqual(4.5);

  // Phones show start times rather than abbreviated names; accessible names retain the title.
  const event = page.locator('.fc-daygrid-event').filter({ hasText: title });
  await expect(page.getByRole('button', { name: new RegExp(title) })).toHaveCount(1);
  const visibleLabel = event.locator(viewport.width <= 720 ? '.fc-event-time' : '.fc-event-title');
  expect(await visibleLabel.evaluate((el) => el.getBoundingClientRect().width)).toBeGreaterThan(8);
  await expect(visibleLabel).toHaveCSS('clip-path', 'none');
  await expect(visibleLabel).not.toHaveText('');
  await event.click();
  await expect(page.getByLabel('Title')).toHaveValue(title);
  page.once('dialog', (d) => d.accept());
  await page.getByRole('dialog').getByRole('button', { name: 'Delete', exact: true }).click();
  await expect(event).toHaveCount(0);

  // Offline: the alert names the failure and the empty list does not claim there are no events.
  await page.locator('.fc-listWeek-button').click();
  await context.setOffline(true);
  // Six weeks on is past the range Month already fetched, so the list must ask the server.
  for (let i = 0; i < 6; i++) await page.locator('.fc-next-button').click();
  await expect(page.getByRole('alert')).toContainText('could not be reached');
  await expect(page.locator('.fc-list-empty')).toHaveText('Events could not be loaded.');
  await context.setOffline(false);
  await context.close();
});

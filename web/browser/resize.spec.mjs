import { test, expect } from '@playwright/test';

// Own client address (the harness trusts loopback as a proxy) so these sign-ins do not spend
// the login budget the other specs share.
const ownIP = { 'X-Forwarded-For': '203.0.113.23' };

// Crossing the 720px breakpoint must not remount the calendar list: half-typed forms survive.
test('calendar list keeps its drafts across the phone breakpoint', async ({ browser }, testInfo) => {
  const { width, height } = testInfo.project.use.viewport;
  const other = width > 720 ? 390 : 1280;
  const context = await browser.newContext({ extraHTTPHeaders: ownIP, viewport: { width, height }, colorScheme: testInfo.project.use.colorScheme });
  const page = await context.newPage();
  await page.goto('/');
  await page.getByLabel('Username', { exact: true }).fill('walter');
  await page.locator('input[type=password]').fill('WalterUpdated456!');
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(page.locator('.fc')).toBeVisible();

  const list = page.getByRole('complementary', { name: 'Calendars' });
  const picker = page.locator('.app-header details.kc-picker');
  const summary = picker.locator('summary');
  // Phones show the list behind the header's Calendars disclosure.
  if (width <= 720) {
    await expect(summary).toBeVisible();
    await expect(list).toBeHidden();
    await summary.click();
  }
  await expect(list.getByLabel('Show Calendar')).toBeVisible();
  await list.evaluate((el) => { el.dataset.probe = 'same node'; });

  await list.getByRole('button', { name: 'Edit Calendar', exact: true }).click();
  const rename = list.locator('input[id^="name-"]');
  await rename.fill('Draft rename');
  await list.getByRole('button', { name: 'Subscribe to a calendar' }).click();
  const sub = list.getByRole('form', { name: 'Subscribe to a calendar' });
  await sub.getByLabel('Link').fill('https://example.test/feed.ics');
  await sub.getByLabel('Name', { exact: true }).fill('Draft feed');

  const kept = async () => {
    expect(await list.evaluate((el) => ({ probe: el.dataset.probe, inShell: !!el.closest('.app-header') }))).toEqual({ probe: 'same node', inShell: true });
    await expect(rename).toBeVisible();
    await expect(rename).toHaveValue('Draft rename');
    await expect(sub.getByLabel('Link')).toHaveValue('https://example.test/feed.ics');
    await expect(sub.getByLabel('Name', { exact: true })).toHaveValue('Draft feed');
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  };
  // Typing in the list keeps the disclosure open when the screen narrows, and back.
  for (const w of [other, width]) {
    await page.setViewportSize({ width: w, height });
    await kept();
  }

  // On a phone the disclosure closes (Escape returns focus to it) and reopens with the drafts.
  await page.setViewportSize({ width: 390, height });
  await sub.getByLabel('Link').focus();
  await page.keyboard.press('Escape');
  await expect(list).toBeHidden();
  await expect(summary).toBeFocused();
  await page.keyboard.press('Enter');
  await kept();

  // The add form, opened on one side, survives the trip too.
  await sub.getByRole('button', { name: 'Cancel' }).click();
  await list.getByRole('button', { name: 'New calendar' }).click();
  await list.getByLabel('New calendar name').fill('Draft calendar');
  for (const w of [1280, 390]) {
    await page.setViewportSize({ width: w, height });
    await expect(list.getByLabel('New calendar name')).toHaveValue('Draft calendar');
    await expect(list.getByLabel('New calendar name')).toBeVisible();
  }
  await context.close();
});

import { test, expect } from '@playwright/test';

test('calendar: CSP, create by form, list view by keyboard, escape, delete', async ({ page }, testInfo) => {
  const violations = [];
  page.on('console', (m) => { if (/Content Security Policy|violates.*directive/i.test(m.text())) violations.push(m.text()); });
  const title = `Planning ${testInfo.project.name} ${Date.now()}`;

  await page.goto('/');
  await page.getByPlaceholder('admin', { exact: true }).fill('walter');
  await page.locator('input[type=password]').fill('WalterUpdated456!');
  await page.getByRole('button', { name: 'Sign In', exact: true }).click();

  // Everyday users land on the calendar; FullCalendar renders under the production CSP.
  await expect(page.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name: 'Calendar' })).toHaveAttribute('aria-current', 'page');
  await expect(page.locator('.fc')).toBeVisible();

  await page.getByRole('button', { name: 'New event' }).click();
  await expect(page.getByRole('dialog')).toBeVisible();
  await page.getByLabel('Title').fill(title);
  await page.getByRole('dialog').getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);

  await page.locator('.fc-listWeek-button').click();
  // The week starts on Sunday: from Saturday 23:xx the next whole hour is in next week.
  const now = new Date();
  const rolled = now.getDay() === 6 && new Date(now.getTime() + 3600_000).getDay() === 0;
  if (rolled) await page.locator('.fc-next-button').click();
  const item = page.locator('.fc-list-event', { hasText: title });
  await expect(item).toBeVisible();

  // Open by keyboard, close with Escape, open again.
  const first = item.locator('a, [tabindex]').first();
  await first.focus();
  await expect(first).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('dialog')).toBeVisible();
  await expect(page.getByLabel('Title')).toHaveValue(title);
  // Every close refetches events and FullCalendar re-renders the list: wait for it so focus lands on the final node.
  const refetched = page.waitForResponse((r) => new URL(r.url()).pathname === '/api/events' && r.request().method() === 'GET');
  await page.keyboard.press('Escape');
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await refetched;
  await expect(item).toBeVisible();

  const link = item.locator('a, [tabindex]').first();
  await link.focus();
  await expect(link).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('dialog')).toBeVisible();
  page.once('dialog', (d) => d.accept());
  await page.getByRole('dialog').getByRole('button', { name: 'Delete', exact: true }).click();
  await expect(page.locator('.fc-list-event', { hasText: title })).toHaveCount(0);

  // The delete reached the server: a fresh load does not show the event either.
  const events = () => page.waitForResponse((r) => new URL(r.url()).pathname === '/api/events' && r.ok());
  const loaded = events();
  await page.reload();
  await loaded;
  await page.locator('.fc-listWeek-button').click();
  if (rolled) {
    const next = events();
    await page.locator('.fc-next-button').click();
    await next;
  }
  await expect(page.locator('.fc-list-event', { hasText: title })).toHaveCount(0);

  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  expect(violations).toEqual([]);
});

import { test, expect } from '@playwright/test';

const ownIP = { 'X-Forwarded-For': '203.0.113.31' };

// A CSV goes in through the Calendars + menu into a new calendar under the production CSP and
// CSRF: the result counts the rows, the event's markup title shows as text, and nothing runs.
test('import: a CSV file into a new calendar', async ({ browser }, testInfo) => {
  const { viewport, colorScheme } = testInfo.project.use;
  const phone = viewport.width <= 720;
  const context = await browser.newContext({ extraHTTPHeaders: ownIP, viewport, colorScheme });
  const page = await context.newPage();
  await page.goto('/');
  await page.getByLabel('Username', { exact: true }).fill('walter');
  await page.locator('input[type=password]').fill('WalterUpdated456!');
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(page.locator('.fc')).toBeVisible();
  const list = page.getByRole('complementary', { name: 'Calendars' });
  if (phone) await page.locator('.ky-sidebar summary', { hasText: 'Calendars' }).click();

  const plus = list.getByRole('button', { name: 'Add a calendar' });
  await plus.click();
  await page.screenshot({ path: testInfo.outputPath('menu.png'), animations: 'disabled' });
  await page.getByRole('menuitem', { name: 'Import a CSV file' }).click();
  const dialog = page.getByRole('dialog', { name: 'Import a CSV file' });
  await expect(dialog).toBeVisible();

  const name = `Imported ${testInfo.project.name}`;
  const today = new Date().toLocaleDateString('en-CA'); // YYYY-MM-DD, today where the test and browser run
  const title = '<img src=x onerror="window.__pwned=1">';
  const csv = `Subject,Start Date,Start Time,End Date,End Time\n"${title.replaceAll('"', '""')}",${today},10:00,${today},11:00\nBroken,not a date,,,\n`;
  await dialog.getByLabel('File').setInputFiles({ name: 'trip.csv', mimeType: 'text/csv', buffer: Buffer.from(csv) });
  await dialog.getByLabel('Add to').selectOption({ label: 'New calendar…' });
  await dialog.getByLabel('Name').fill(name);
  await expect(dialog.getByRole('button', { name: 'Import' })).toBeEnabled();
  await page.screenshot({ path: testInfo.outputPath('import-form.png'), animations: 'disabled' });
  await dialog.getByRole('button', { name: 'Import' }).click();

  const status = dialog.getByRole('status');
  await expect(status).toContainText('Imported 1 event. 1 row skipped.');
  await expect(status).toContainText('Row 3: Start Date is missing or not a date');
  await page.screenshot({ path: testInfo.outputPath('import-result.png'), animations: 'disabled' });
  await dialog.getByRole('button', { name: 'Done' }).click();
  await expect(dialog).toBeHidden();
  if (!phone) await expect(plus).toBeFocused();

  if (phone && !(await list.isVisible())) await page.locator('.ky-sidebar summary', { hasText: 'Calendars' }).click();
  await expect(list.getByText(name, { exact: true })).toBeVisible();
  // The title is text in the calendar: it names the event and no image was made of it.
  if (phone) await page.keyboard.press('Escape');
  await expect(page.locator('.fc').getByText(title).first()).toBeVisible();
  expect(await page.locator('.fc img').count()).toBe(0);
  expect(await page.evaluate(() => window.__pwned)).toBeUndefined();

  // Clean up: the calendar and its event go.
  if (phone) await page.locator('.ky-sidebar summary', { hasText: 'Calendars' }).click();
  page.once('dialog', (d) => d.accept());
  await list.getByRole('button', { name: `Delete ${name}` }).click({ force: true });
  await expect(list.getByText(name, { exact: true })).toHaveCount(0);
  await context.close();
});

import { test, expect } from '@playwright/test';

async function signIn(page, username, password) {
  await page.goto('/');
  await page.getByPlaceholder('admin', { exact: true }).fill(username);
  await page.locator('input[type=password]').fill(password);
  await page.getByRole('button', { name: 'Sign In', exact: true }).click();
}

// Own client address so this spec's sign-ins keep a separate login-limiter bucket.
const ownIP = { 'X-Forwarded-For': '203.0.113.21' };

test('people: an admin adds a person who signs in and lands on the calendar', async ({ page, browser }, testInfo) => {
  const username = `p${Date.now()}-${testInfo.project.name}`;

  await page.setExtraHTTPHeaders(ownIP);
  await signIn(page, 'admin', 'BrowserUpdated456!');
  await page.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name: 'People', exact: true }).click();
  await page.getByRole('button', { name: 'Add person' }).click();
  const add = page.getByRole('dialog', { name: 'Add person' });
  await add.getByLabel('Username').fill(username);
  await add.getByLabel('Display name').fill('Pat Example');
  await add.getByRole('button', { name: 'Create' }).click();

  const handover = page.getByRole('dialog', { name: 'Temporary password' });
  const temporary = (await handover.getByTestId('temporary-password').textContent()).trim();
  expect(temporary.length).toBeGreaterThanOrEqual(12);
  await handover.getByRole('button', { name: 'Done' }).click();
  await expect(page.getByRole('row').filter({ hasText: username })).toBeVisible();
  await expect(page.getByText(temporary)).toHaveCount(0);

  // The person signs in with the temporary password, must replace it, then lands on the calendar.
  const context = await browser.newContext({ extraHTTPHeaders: ownIP });
  const person = await context.newPage();
  await signIn(person, username, temporary);
  await person.getByLabel('Current password').fill(temporary);
  await person.getByLabel('New password', { exact: true }).fill('PersonChosen789!');
  await person.getByLabel('Confirm new password').fill('PersonChosen789!');
  await person.getByRole('button', { name: 'Change password' }).click();
  await expect(person.getByRole('status')).toContainText('Password changed');
  await person.getByPlaceholder('admin', { exact: true }).fill(username);
  await person.locator('input[type=password]').fill('PersonChosen789!');
  await person.getByRole('button', { name: 'Sign In', exact: true }).click();
  await expect(person.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name: 'Calendar' })).toHaveAttribute('aria-current', 'page');
  await expect(person.locator('.fc')).toBeVisible();
  await context.close();
});

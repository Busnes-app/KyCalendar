import { test, expect } from '@playwright/test';

async function signIn(page, username, password) {
  await page.goto('/');
  await page.getByPlaceholder('admin', { exact: true }).fill(username);
  await page.locator('input[type=password]').fill(password);
  await page.getByRole('button', { name: 'Sign In', exact: true }).click();
}

// Own client address (the harness trusts loopback as a proxy) so this spec's sign-ins do not
// spend the login budget the other specs share.
const ownIP = { 'X-Forwarded-For': '203.0.113.12' };

test('groups: an admin puts walter in a group and walter sees its calendar', async ({ page, browser }, testInfo) => {
  const suffix = `${testInfo.project.name} ${Date.now()}`;
  const group = `Crew ${suffix}`;
  const calendar = `Rota ${suffix}`;

  await page.setExtraHTTPHeaders(ownIP);
  await signIn(page, 'admin', 'BrowserUpdated456!');
  const nav = page.getByRole('navigation', { name: 'Primary' });
  await nav.getByRole('button', { name: 'Groups', exact: true }).click();
  await page.getByLabel('Group name').fill(group);
  await page.getByRole('button', { name: 'Create group' }).click();

  const card = page.getByRole('article', { name: group });
  await card.getByRole('button', { name: 'Members' }).click();
  await card.getByLabel('Find a person').fill('walter');
  await card.getByRole('button', { name: 'Search' }).click();
  await card.getByRole('button', { name: 'Add walter' }).click();
  await expect(card.getByRole('button', { name: 'Remove walter' })).toBeVisible();

  await nav.getByRole('button', { name: 'Group calendars', exact: true }).click();
  await page.getByLabel('Calendar name').fill(calendar);
  await page.getByRole('button', { name: 'Create', exact: true }).click();
  const calCard = page.getByRole('article', { name: calendar });
  await calCard.getByLabel('Group', { exact: true }).selectOption({ label: group });
  await calCard.getByRole('button', { name: 'Add group' }).click();
  await expect(calCard.getByRole('button', { name: `Remove ${group}` })).toBeVisible();

  // walter signs in in a separate cookie jar and finds the calendar beside his own.
  const context = await browser.newContext({ extraHTTPHeaders: ownIP });
  const walter = await context.newPage();
  await signIn(walter, 'walter', 'WalterUpdated456!');
  await expect(walter.getByRole('complementary', { name: 'Calendars' }).getByLabel(`Show ${calendar}`)).toBeVisible();
  await context.close();
});

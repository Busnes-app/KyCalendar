import { test, expect } from '@playwright/test';

const ownIP = { 'X-Forwarded-For': '203.0.113.44' };

// The board: a list view switched to Board shows three status columns; the arrow button (the
// keyboard twin of dragging) moves a card and the status is saved.
test('board: move a task between status columns', async ({ browser }, testInfo) => {
  const { viewport, colorScheme } = testInfo.project.use;
  const context = await browser.newContext({ extraHTTPHeaders: ownIP, viewport, colorScheme });
  const page = await context.newPage();
  await page.goto('/');
  await page.getByLabel('Username', { exact: true }).fill('walter');
  await page.locator('input[type=password]').fill('WalterUpdated456!');
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await page.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name: 'Tasks' }).click();
  if (viewport.width <= 720) await page.locator('.kc-picker > summary').click();
  await page.getByRole('navigation', { name: 'Task lists' }).getByRole('button', { name: /^Inbox/ }).click();
  const title = `Board ${testInfo.project.name} ${Date.now()}`;
  await page.getByLabel('Quick add a task').fill(title);
  await page.getByLabel('Quick add a task').press('Enter');
  await page.getByRole('radio', { name: 'Board' }).check();
  const notStarted = page.locator('.tb-col', { has: page.getByRole('heading', { name: /^Not started/ }) });
  await expect(notStarted.locator('.tb-card', { hasText: title })).toBeVisible();
  // The arrow is named for its card and column, explains itself on hover, and is a 32px target.
  const move = page.getByRole('button', { name: `Move ${title} to In progress` });
  await expect(move).toHaveAttribute('title', 'Move to In progress');
  const box = await move.boundingBox();
  expect(box.width).toBeGreaterThanOrEqual(32);
  expect(box.height).toBeGreaterThanOrEqual(32);
  await move.click();
  const inProgress = page.locator('.tb-col', { has: page.getByRole('heading', { name: /^In progress/ }) });
  await expect(inProgress.locator('.tb-card', { hasText: title })).toBeVisible();
  await page.reload();
  await expect(page.locator('.tb-col', { has: page.getByRole('heading', { name: /^In progress/ }) }).locator('.tb-card', { hasText: title })).toBeVisible();
  await page.getByRole('radio', { name: 'List' }).check();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await context.close();
});

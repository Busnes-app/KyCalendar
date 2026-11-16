import { test, expect } from '@playwright/test';

const ownIP = { 'X-Forwarded-For': '203.0.113.45' };

// Goals, habits and insights: a goal is made against a list and shows its progress table; a
// repeating task becomes a habit with a streak; Insights has its chart and a table view.
test('goals, habits and insights', async ({ browser }, testInfo) => {
  const { viewport, colorScheme } = testInfo.project.use;
  const context = await browser.newContext({ extraHTTPHeaders: ownIP, viewport, colorScheme });
  const page = await context.newPage();
  await page.goto('/');
  await page.getByLabel('Username', { exact: true }).fill('walter');
  await page.locator('input[type=password]').fill('WalterUpdated456!');
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await page.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name: 'Tasks' }).click();
  const narrow = viewport.width <= 720;
  const open = async (name) => {
    if (narrow) await page.locator('.kc-picker > summary').click();
    await page.getByRole('navigation', { name: 'Task lists' }).getByRole('button', { name: new RegExp(`^${name}`) }).click();
    await expect(page.getByRole('heading', { level: 1, name })).toBeVisible();
  };
  await open('Inbox');
  const title = `Habit ${testInfo.project.name} ${Date.now()}`;
  await page.getByLabel('Quick add a task').fill(`${title} every day`);
  await page.getByLabel('Quick add a task').press('Enter');
  await page.getByRole('button', { name: new RegExp(title) }).click();
  await page.getByLabel('Track as a habit').check();
  await page.getByRole('button', { name: 'Save' }).click();
  await page.locator('.tk-row', { hasText: title }).getByRole('checkbox').click();

  await open('Habits');
  await expect(page.locator('.th-list li', { hasText: title }).getByText(/1-day streak/)).toBeVisible();

  await open('Goals');
  await page.getByRole('button', { name: 'New goal' }).click();
  const goal = `Goal ${testInfo.project.name} ${Date.now()}`;
  await page.getByLabel('Goal', { exact: true }).fill(goal);
  await page.getByRole('dialog').getByLabel('Inbox').check();
  await page.getByRole('dialog').getByRole('button', { name: 'Save' }).click();
  await expect(page.getByRole('region', { name: goal }).getByRole('cell', { name: 'Inbox' })).toBeVisible();

  await open('Insights');
  await expect(page.getByRole('img', { name: /Tasks completed per week/ })).toBeVisible();
  await page.getByRole('button', { name: 'Show table' }).click();
  await expect(page.getByRole('columnheader', { name: 'Week of' })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await context.close();
});

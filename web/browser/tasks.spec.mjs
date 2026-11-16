import { test, expect } from '@playwright/test';

const ownIP = { 'X-Forwarded-For': '203.0.113.42' };

// Tasks under the production CSP: quick add with its preview, the row's words (not colour
// alone), completing, the detail dialog, the capture box on n, and one left column.
test('tasks: quick add, complete, detail and capture', async ({ browser }, testInfo) => {
  const { viewport, colorScheme } = testInfo.project.use;
  const context = await browser.newContext({ extraHTTPHeaders: ownIP, viewport, colorScheme });
  const page = await context.newPage();
  const violations = [];
  page.on('console', (m) => { if (/Content Security Policy/i.test(m.text())) violations.push(m.text()); });
  await page.goto('/');
  await page.getByLabel('Username', { exact: true }).fill('walter');
  await page.locator('input[type=password]').fill('WalterUpdated456!');
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await page.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name: 'Tasks' }).click();
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
  const narrow = viewport.width <= 720;
  const lists = page.getByRole('navigation', { name: 'Task lists' });
  const open = async (name) => {
    if (narrow) await page.locator('.kc-picker > summary').click();
    await lists.getByRole('button', { name: new RegExp(`^${name}`) }).click();
    await expect(page.getByRole('heading', { level: 1, name })).toBeVisible();
  };
  // One left column: the lists live in the app sidebar slot.
  await expect(page.locator('.app-sidebar-slot .tk-sidebar')).toHaveCount(1);
  await open('Inbox');

  const title = `Spec ${testInfo.project.name} ${Date.now()}`;
  const quick = page.getByLabel('Quick add a task');
  await quick.fill(`${title} tomorrow !high #spec`);
  await expect(page.locator('#tk-quick-preview')).toContainText('High priority');
  await expect(page.locator('#tk-quick-preview')).toContainText('#spec');
  await quick.press('Enter');
  const row = page.locator('.tk-row', { hasText: title });
  await expect(row).toContainText('Tomorrow');
  await expect(row).toContainText('High');
  await expect(row).toHaveAttribute('data-priority', 'high');

  await row.getByRole('button', { name: new RegExp(title) }).click();
  const dialog = page.getByRole('dialog', { name: 'Edit task' });
  await expect(dialog.getByLabel('Title')).toHaveValue(title);
  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);

  await row.getByRole('checkbox', { name: `Complete ${title}` }).check();
  await expect(row).toHaveCount(0);
  await open('Completed');
  await expect(page.locator('.tk-row', { hasText: title })).toBeVisible();

  // The capture box, from anywhere: n outside fields.
  await page.locator('h1').click();
  await page.keyboard.press('n');
  await expect(page.getByRole('dialog', { name: 'Add' })).toBeVisible();
  await page.keyboard.press('Escape');

  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  expect(violations).toEqual([]);
  await context.close();
});

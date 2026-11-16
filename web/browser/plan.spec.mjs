import { test, expect } from '@playwright/test';

const ownIP = { 'X-Forwarded-For': '203.0.113.43' };

// Plan and Today: a candidate placed with + lands on the timeline as a task block (never an
// event), shows in Today's Next card, and Mark done completes it.
test('plan my day: place a task, see it in Today, mark it done', async ({ browser }, testInfo) => {
  const { viewport, colorScheme } = testInfo.project.use;
  const context = await browser.newContext({ extraHTTPHeaders: ownIP, viewport, colorScheme });
  const page = await context.newPage();
  // A fixed morning, so the working day has room whenever CI runs.
  const morning = new Date(); morning.setHours(8, 5, 0, 0);
  await page.clock.setFixedTime(morning);
  await page.goto('/');
  await page.getByLabel('Username', { exact: true }).fill('walter');
  await page.locator('input[type=password]').fill('WalterUpdated456!');
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await page.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name: 'Tasks' }).click();
  if (viewport.width <= 720) await page.locator('.kc-picker > summary').click();
  await page.getByRole('navigation', { name: 'Task lists' }).getByRole('button', { name: /^Today/ }).click();

  const title = `Plan ${testInfo.project.name} ${Date.now()}`;
  await page.getByLabel('Quick add a task').fill(`${title} !high`);
  await page.getByLabel('Quick add a task').press('Enter');
  await page.getByRole('button', { name: 'Plan my day' }).first().click();
  if (viewport.width <= 720) await page.getByRole('tab', { name: /Candidates/ }).click();
  await page.getByRole('button', { name: `Place ${title} in the first free gap` }).click();
  if (viewport.width <= 720) await page.getByRole('tab', { name: 'Plan' }).click();
  const block = page.locator('.tp-task', { hasText: title });
  await expect(block).toBeVisible();
  await expect(block).toHaveAttribute('aria-label', /High priority/);
  await expect(page.locator('.tp-event', { hasText: title })).toHaveCount(0);

  await page.getByRole('button', { name: 'Back to Today' }).click();
  // The next planned task shows once, in the Next card; Mark done moves it into the day as done.
  const next = page.getByRole('region', { name: 'Next' });
  await expect(next).toContainText(title);
  await next.getByRole('button', { name: 'Mark done' }).click();
  await expect(page.locator('.td-rows .td-task', { hasText: title })).toHaveAttribute('data-done', 'true');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await context.close();
});

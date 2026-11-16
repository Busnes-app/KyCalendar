import { test, expect } from '@playwright/test';

const ownIP = { 'X-Forwarded-For': '203.0.113.46' };

// The Tasks section of the sidebar is short (owner, 2026-11-16): Inbox, Today, Upcoming, All
// tasks and Progress, then the user's lists, which stay in view at 1280x800. Every view the
// section used to list is a tab on the page of its row, and a saved choice of one of them still
// opens it there.
test('tasks navigation: short section, every view reachable, old choices land on their tab', async ({ browser }, testInfo) => {
  const { viewport, colorScheme } = testInfo.project.use;
  const context = await browser.newContext({ extraHTTPHeaders: ownIP, viewport: viewport.width > 720 ? { width: 1280, height: 800 } : viewport, colorScheme });
  const page = await context.newPage();
  await page.goto('/');
  await page.getByLabel('Username', { exact: true }).fill('walter');
  await page.locator('input[type=password]').fill('WalterUpdated456!');
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await page.locator('.ky-content').waitFor();
  const csrf = (await context.cookies()).find((c) => c.name === 'ky_csrf')?.value;
  const listName = `Nav list ${testInfo.project.name}`;
  const lists = await (await page.request.get('/api/calendars')).json();
  if (!lists.some((c) => c.name === listName)) {
    expect((await page.request.post('/api/calendars', { data: { name: listName, components: ['VTODO'] }, headers: { 'X-CSRF-Token': csrf } })).ok()).toBe(true);
  }
  await page.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name: 'Tasks' }).click();
  const narrow = viewport.width <= 720;
  const nav = page.getByRole('navigation', { name: 'Task lists' });
  const openNav = async () => { if (narrow && !(await page.locator('.kc-picker[open]').count())) await page.locator('.kc-picker > summary').click(); };
  await openNav();
  const fixed = nav.locator('.tk-navlist').first().locator('.tk-nav > span:not(.tk-count):not(.tk-nav-note)');
  await expect(fixed).toHaveText(['Inbox', 'Today', 'Upcoming', 'All tasks', 'Progress']);
  if (!narrow) {
    await page.locator('.app-sidebar-slot').evaluate((el) => { el.scrollTop = 0; });
    await expect(nav.getByRole('button', { name: new RegExp(`^${listName}`) })).toBeInViewport();
  }

  const tabs = async (row, names) => {
    await openNav();
    await nav.getByRole('button', { name: new RegExp(`^${row}`) }).click();
    await expect(page.getByRole('heading', { level: 1, name: row })).toBeVisible();
    for (const name of names) {
      const tab = page.getByRole('navigation', { name: `${row} views` }).getByRole('button', { name: new RegExp(`^${name}`) });
      await tab.click();
      await expect(tab).toHaveAttribute('aria-current', 'page');
      await expect(page.getByRole('heading', { level: 1, name: row })).toBeVisible();
    }
  };
  await tabs('All tasks', ['Open', 'Overdue', 'Someday', 'Flagged from mail', 'Completed']);
  await tabs('Progress', ['Habits', 'Goals', 'Insights']);

  // A choice saved by the old sidebar opens on the row that now holds it.
  for (const [id, row, tab] of [['overdue', 'All tasks', 'Overdue'], ['someday', 'All tasks', 'Someday'], ['flagged', 'All tasks', 'Flagged from mail'], ['completed', 'All tasks', 'Completed'], ['habits', 'Progress', 'Habits'], ['goals', 'Progress', 'Goals'], ['insights', 'Progress', 'Insights']]) {
    await page.evaluate((v) => localStorage.setItem('kycalendar.tasksView', JSON.stringify({ kind: 'smart', id: v })), id);
    await page.reload();
    await expect(page.getByRole('heading', { level: 1, name: row })).toBeVisible();
    const chosen = page.getByRole('navigation', { name: `${row} views` }).getByRole('button', { name: new RegExp(`^${tab}`) });
    await expect(chosen).toHaveAttribute('aria-current', 'page');
    // The chosen tab is in view inside its row, which scrolls sideways on phones.
    expect(await chosen.evaluate((el) => { const r = el.getBoundingClientRect(), n = el.parentElement.getBoundingClientRect(); return r.left >= n.left - 1 && r.right <= n.right + 1; }), `${tab} tab in view`).toBe(true);
    await openNav();
    await expect(nav.getByRole('button', { name: new RegExp(`^${row}`) })).toHaveAttribute('aria-current', 'page');
    if (narrow) await page.locator('.kc-picker > summary').click();
  }
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await context.close();
});

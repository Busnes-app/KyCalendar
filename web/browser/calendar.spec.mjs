import { test, expect } from '@playwright/test';

test('calendar: CSP, create by form, list view by keyboard, escape, delete', async ({ page }, testInfo) => {
  const violations = [];
  page.on('console', (m) => { if (/Content Security Policy|violates.*directive/i.test(m.text())) violations.push(m.text()); });
  const title = `Planning ${testInfo.project.name} ${Date.now()}`;

  await page.goto('/');
  await page.getByLabel('Username', { exact: true }).fill('walter');
  await page.locator('input[type=password]').fill('WalterUpdated456!');
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();

  // Everyday users land on the calendar; FullCalendar renders under the production CSP.
  await expect(page.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name: 'Calendar' })).toHaveAttribute('aria-current', 'page');
  await expect(page.locator('.fc')).toBeVisible();
  // One left column: the calendar list lives in the app sidebar; phones open it from the header's
  // Calendars disclosure, closed at first so the canvas leads.
  const list = page.getByRole('complementary', { name: 'Calendars' });
  if (testInfo.project.use.viewport.width <= 720) {
    await expect(list).toBeHidden();
    await page.locator('.ky-sidebar summary', { hasText: 'Calendars' }).click();
  }
  await expect(list.getByLabel('Show Calendar')).toBeVisible();
  expect(await list.evaluate((el) => !!el.closest('.ky-sidebar'))).toBe(true);
  if (testInfo.project.use.viewport.width <= 720) await page.locator('.ky-sidebar summary', { hasText: 'Calendars' }).click();

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
  await expect(link).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('dialog')).toBeVisible();
  page.once('dialog', (d) => d.accept());
  await page.getByRole('dialog').getByRole('button', { name: 'Delete', exact: true }).click();
  await expect(page.locator('.fc-list-event', { hasText: title })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'New event' })).toBeFocused();

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

  // Week view opens on the working day (07:00), not midnight, even when switched to from month.
  await page.locator('.fc-dayGridMonth-button').click();
  await page.locator('.fc-timeGridWeek-button').click();
  const slot7 = page.locator('.fc-timegrid-slot[data-time="07:00:00"]').first();
  await expect.poll(() => page.evaluate(() => document.querySelector('.fc-timegrid .fc-scroller-liquid-absolute')?.scrollTop ?? 0)).toBeGreaterThan(200);
  await expect(slot7).toBeInViewport();
  // A transformed cell can have an in-viewport rectangle while its text is clipped by an
  // ancestor. Verify that the visible gutter actually paints above the event grid.
  await expect.poll(() => slot7.evaluate((el) => {
    const r = el.getBoundingClientRect();
    const painted = document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2);
    return !!painted && el.contains(painted);
  })).toBe(true);
  if (testInfo.project.use.viewport.width <= 720) {
    const week = page.getByRole('region', { name: 'Week calendar' });
    await expect(page.getByText('Scroll across the week to see other days.')).toBeVisible();
    expect(await week.evaluate((el) => el.scrollWidth > el.clientWidth)).toBe(true);
    await week.focus();
    const start = await week.evaluate((el) => el.scrollLeft);
    await page.keyboard.press(start > 0 ? 'ArrowLeft' : 'ArrowRight');
    await expect.poll(() => week.evaluate((el) => el.scrollLeft)).not.toBe(start);
    await expect(slot7).toBeInViewport();
  }

  // FullCalendar reuses time-grid DOM across Day/Week; neither view inherits the other's name.
  await page.locator('.fc-timeGridDay-button').click();
  await expect(page.locator('.fc-timeGridDay-view')).not.toHaveAttribute('aria-label', 'Week calendar');
  await page.locator('.fc-timeGridWeek-button').click();
  await expect(page.getByRole('region', { name: 'Week calendar' })).toHaveAttribute('tabindex', '0');

  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);

  // AI agent setup: its long commands scroll inside their own blocks, never the page.
  await page.getByRole('button', { name: 'Settings', exact: true }).click();
  await page.getByRole('menu', { name: 'Settings' }).getByRole('menuitem', { name: 'Phones & apps' }).click();
  await page.getByLabel(/AI agent/).check();
  // Three access levels, narrowest first and chosen by default.
  const access = page.getByRole('group', { name: 'Agent access' }).getByRole('radio');
  await expect(access).toHaveCount(3);
  for (const [i, name] of ['Read only', 'Edit its own events', 'Edit any event'].entries()) {
    await expect(access.nth(i)).toBeVisible();
    await expect(page.getByLabel(name)).toBeVisible();
  }
  await expect(access.first()).toBeChecked();
  await page.getByLabel('Agent name').fill(`Agent ${testInfo.project.name} ${Date.now()}`);
  await page.getByRole('button', { name: 'Create', exact: true }).click();
  const install = page.getByLabel('Hermes Agent skill install command', { exact: true });
  await expect(install).toBeVisible();
  expect(await install.evaluate((el) => getComputedStyle(el).overflowX)).toBe('auto');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  expect(violations).toEqual([]);
});

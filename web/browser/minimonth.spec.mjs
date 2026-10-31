import { test, expect } from '@playwright/test';

const ownIP = { 'X-Forwarded-For': '203.0.113.29' };

// The sidebar mini-month: today marked, the visible week highlighted, a click or the keyboard
// moves the main view, and the sidebar still fits at 1440x900 without scrolling. Phones have none.
test('mini-month: marks today and the week, navigates, fits the sidebar', async ({ browser }, testInfo) => {
  const { viewport, colorScheme } = testInfo.project.use;
  const wide = viewport.width > 720;
  const context = await browser.newContext({ extraHTTPHeaders: ownIP, viewport: wide ? { width: 1440, height: 900 } : viewport, colorScheme });
  const page = await context.newPage();
  await page.goto('/');
  await page.getByLabel('Username', { exact: true }).fill('walter');
  await page.locator('input[type=password]').fill('WalterUpdated456!');
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(page.locator('.fc')).toBeVisible();

  const month = new Intl.DateTimeFormat('en-US', { month: 'long', year: 'numeric' }).format(new Date());
  if (!wide) {
    await expect(page.getByRole('grid')).toHaveCount(0);
    await context.close();
    return;
  }
  const grid = page.getByRole('grid', { name: month });
  await expect(grid).toBeVisible();
  expect(await grid.evaluate((el) => !!el.closest('.ky-sidebar'))).toBe(true);
  await expect(grid.getByRole('gridcell', { selected: true })).toHaveCount(7);
  const today = grid.locator('button[aria-current="date"]');
  await expect(today).toHaveCount(1);
  await expect(today).toHaveAttribute('tabindex', '0');

  // The sidebar keeps the shared width, and at 1440x900 the mini-month, the Calendars heading and
  // the first calendar sit in view above the account block without scrolling (other specs leave
  // extra calendars, so the list itself may scroll further down).
  expect((await page.locator('.ky-sidebar').boundingBox()).width).toBe(240);
  const fit = await page.locator('.app-sidebar-slot').evaluate((slot) => {
    const row = slot.querySelector('.kc-sidebar li').getBoundingClientRect();
    return { scrollTop: slot.scrollTop, rowBottom: row.bottom, slotBottom: slot.getBoundingClientRect().bottom };
  });
  expect(fit.scrollTop).toBe(0);
  // Clear of the slot's 2.5rem scroll fade, too.
  expect(fit.rowBottom).toBeLessThanOrEqual(fit.slotBottom - 40);

  // Keyboard: a week down from today, then Enter, moves the main week view along with it.
  const title = page.locator('.fc-toolbar-title');
  const before = await title.textContent();
  await today.focus();
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('Enter');
  await expect(title).not.toHaveText(before);
  await expect(page.locator('.fc-timeGridWeek-view')).toBeVisible();
  await expect(page.getByRole('grid').getByRole('gridcell', { selected: true })).toHaveCount(7);

  // Paging and a click: the 15th of the month paged to becomes the Day view's day.
  await page.locator('.fc-timeGridDay-button').click();
  const shown = page.locator('.kc-mini h2');
  const was = await shown.textContent();
  await page.getByRole('button', { name: 'Next month' }).click();
  await expect(shown).not.toHaveText(was);
  const [name, year] = (await shown.textContent()).split(' ');
  const next = new Date(`${name} 15, ${year}`);
  const label = new Intl.DateTimeFormat('en-US', { weekday: 'long', month: 'long', day: 'numeric', year: 'numeric' }).format(next);
  await page.getByRole('button', { name: label }).click();
  await expect(title).toHaveText(new Intl.DateTimeFormat('en-US', { month: 'long', day: 'numeric', year: 'numeric' }).format(next));
  await expect(page.getByRole('grid').getByRole('gridcell', { selected: true })).toHaveCount(1);
  await context.close();
});

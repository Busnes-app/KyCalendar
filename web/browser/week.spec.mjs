import { test, expect } from '@playwright/test';

const ownIP = { 'X-Forwarded-For': '203.0.113.28' };

// Week view chrome: Today matches the arrows' outline even while today is in view, and the hour
// grid scrolls under sticky day headings with a faded edge saying more hours follow.
test('week: Today outline, sticky headings, scroll cue', async ({ browser }, testInfo) => {
  const { viewport, colorScheme } = testInfo.project.use;
  test.skip(viewport.width <= 720, 'phones open on the list view');
  const context = await browser.newContext({ extraHTTPHeaders: ownIP, viewport, colorScheme });
  const page = await context.newPage();
  await page.goto('/');
  await page.getByLabel('Username', { exact: true }).fill('walter');
  await page.locator('input[type=password]').fill('WalterUpdated456!');
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(page.locator('.fc-timeGridWeek-view')).toBeVisible();

  const look = (sel) => page.locator(sel).evaluate((el) => {
    const s = getComputedStyle(el);
    return { border: s.borderTopColor, background: s.backgroundColor, color: s.color };
  });
  const today = page.locator('.fc-today-button');
  await expect(today).toBeDisabled();
  expect(await look('.fc-today-button')).toEqual(await look('.fc-prev-button'));

  const scroller = page.locator('.fc-timegrid .fc-scrollgrid-section-liquid .fc-scroller');
  await expect(scroller).toHaveAttribute('data-cue', /end/);
  const head = page.locator('.fc-col-header');
  const before = await head.boundingBox();
  await scroller.evaluate((el) => { el.scrollTop = el.scrollHeight; });
  await expect(scroller).toHaveAttribute('data-cue', 'start');
  expect((await head.boundingBox()).y).toBe(before.y);
  await context.close();
});

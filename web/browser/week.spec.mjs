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

// Nord halves: today's column stays a quiet tint of the page (0.4.3 painted it Busnes cream in Nord
// Dark) and day headings keep 4.5:1, each colour composited over every background behind it.
for (const theme of ['nord', 'paper']) {
  test(`week: ${theme} today column and headings`, async ({ browser }, testInfo) => {
    const { viewport, colorScheme } = testInfo.project.use;
    test.skip(viewport.width <= 720, 'phones open on the list view');
    const context = await browser.newContext({ extraHTTPHeaders: ownIP, viewport, colorScheme });
    await context.addInitScript((t) => localStorage.setItem('ky_theme', t), theme);
    const page = await context.newPage();
    await page.goto('/');
    await page.getByLabel('Username', { exact: true }).fill('walter');
    await page.locator('input[type=password]').fill('WalterUpdated456!');
    await page.getByRole('button', { name: 'Sign in', exact: true }).click();
    await expect(page.locator('.fc-timeGridWeek-view .fc-timegrid-col.fc-day-today')).toBeVisible();
    await expect(page.locator('html')).toHaveAttribute('data-theme', theme);

    const { todayVsPage, headings } = await page.evaluate(() => {
      const ctx = Object.assign(document.createElement('canvas'), { width: 1, height: 1 }).getContext('2d', { willReadFrequently: true });
      const layers = (el) => { const l = []; for (let e = el; e; e = e.parentElement) l.unshift(getComputedStyle(e).backgroundColor); return l; };
      const paint = (l) => { ctx.fillStyle = '#fff'; ctx.fillRect(0, 0, 1, 1); for (const c of l) { ctx.fillStyle = c; ctx.fillRect(0, 0, 1, 1); } return [...ctx.getImageData(0, 0, 1, 1).data.slice(0, 3)]; };
      const lum = (c) => c.map((v) => { v /= 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; }).reduce((s, v, i) => s + v * [0.2126, 0.7152, 0.0722][i], 0);
      const ratio = (a, b) => { const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x); return (hi + 0.05) / (lo + 0.05); };
      const bg = (sel) => paint(layers(document.querySelector(sel)));
      return {
        todayVsPage: ratio(bg('.fc-timegrid-col.fc-day-today'), bg('.fc-timegrid-col:not(.fc-day-today)')),
        headings: [...document.querySelectorAll('.fc-col-header-cell-cushion, .kc-day-head b')]
          .map((el) => ratio(paint([...layers(el), getComputedStyle(el).color]), paint(layers(el)))),
      };
    });
    const weakest = Math.min(...headings);
    testInfo.annotations.push({ type: `${theme} heading contrast`, description: weakest.toFixed(2) });
    testInfo.annotations.push({ type: `${theme} today vs page`, description: todayVsPage.toFixed(2) });
    expect(weakest).toBeGreaterThanOrEqual(4.5);
    expect(todayVsPage).toBeLessThan(1.5);
    expect(headings.length).toBeGreaterThanOrEqual(14);
    await context.close();
  });
}

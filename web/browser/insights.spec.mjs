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
    const tab = ['Habits', 'Goals', 'Insights'].includes(name);
    if (narrow) await page.locator('.kc-picker > summary').click();
    await page.getByRole('navigation', { name: 'Task lists' }).getByRole('button', { name: new RegExp(`^${tab ? 'Progress' : name}`) }).click();
    if (tab) await page.getByRole('navigation', { name: 'Progress views' }).getByRole('button', { name }).click();
    await expect(page.getByRole('heading', { level: 1, name: tab ? 'Progress' : name })).toBeVisible();
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
  await expect(page.locator('.th-list li', { hasText: title }).getByRole('definition').first()).toHaveText('1 day');
  // The grid says in words what its colours show.
  await expect(page.getByRole('img', { name: new RegExp(`^${title}, .+: 1 of \\d+ days?, streak 1 day`) })).toBeVisible();

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

// Habit cells and insight marks hold 3:1: done against the card and against missed, in every
// list colour and the accent, lightened on dark themes; earlier weeks' bars and the free track.
test('habit and insight marks hold 3:1', async ({ browser }, testInfo) => {
  const { viewport, colorScheme } = testInfo.project.use;
  const context = await browser.newContext({ extraHTTPHeaders: ownIP, viewport, colorScheme });
  const page = await context.newPage();
  await page.goto('/');
  await page.getByLabel('Username', { exact: true }).fill('walter');
  await page.locator('input[type=password]').fill('WalterUpdated456!');
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await page.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name: 'Tasks' }).click();
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
  const worst = await page.evaluate(() => {
    const rgb = (el, prop) => {
      const c = document.createElement('canvas').getContext('2d');
      const v = getComputedStyle(el)[prop];
      c.fillStyle = prop === 'boxShadow' ? v.match(/^(rgba?|color)\([^)]*\)/)[0] : v;
      c.fillRect(0, 0, 1, 1);
      return [...c.getImageData(0, 0, 1, 1).data].slice(0, 3);
    };
    const lum = (v) => { const [r, g, b] = v.map((x) => { x /= 255; return x <= 0.04045 ? x / 12.92 : ((x + 0.055) / 1.055) ** 2.4; }); return 0.2126 * r + 0.7152 * g + 0.0722 * b; };
    const ratio = (a, b) => { const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p); return (x + 0.05) / (y + 0.05); };
    const host = document.createElement('div');
    host.className = 'card';
    document.querySelector('.ky-content').append(host);
    const card = rgb(host, 'backgroundColor');
    const out = [];
    for (const color of ['var(--accent)', '#1f7a6d', '#1b6f91', '#3b63b8', '#7b4fc0', '#a0661a', '#b8336a', '#4f7a28', '#5d6b75']) {
      host.innerHTML = `<section class="th" style="--habit: ${color}"><span class="th-day" data-done></span><span class="th-day"></span><span class="th-day" data-before></span></section>`;
      const [d, m, b] = host.querySelectorAll('.th-day');
      const [done, missed] = [d, m].map((el) => rgb(el, 'backgroundColor'));
      out.push([`habit ${color} done/card`, ratio(done, card)], [`habit ${color} done/missed`, ratio(done, missed)],
        [`habit ${color} missed edge/card`, ratio(rgb(m, 'boxShadow'), card)], [`habit ${color} before outline/card`, ratio(rgb(b, 'outlineColor'), card)]);
    }
    host.innerHTML = '<svg><rect class="ti-bar"/><rect class="ti-bar ti-bar-now"/><rect class="ti-bar ti-bar-zero"/></svg><span class="ti-bullet"></span>';
    const [past, now, zero] = [...host.querySelectorAll('rect')].map((el) => rgb(el, 'fill'));
    out.push(['past week bar/card', ratio(past, card)], ['this week bar/card', ratio(now, card)], ['zero week stub/card', ratio(zero, card)]);
    host.remove();
    return out.sort((a, b) => a[1] - b[1]).slice(0, 3);
  });
  for (const [what, r] of worst) expect(r, what).toBeGreaterThanOrEqual(3);
  await context.close();
});

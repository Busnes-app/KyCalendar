import { test, expect } from '@playwright/test';

// Own client address (the harness trusts loopback as a proxy) so these sign-ins do not spend
// the login budget the other specs share.
const ownIP = { 'X-Forwarded-For': '203.0.113.25' };

// Hover on the primary navigation stays neutral: the product's global primary-button hover
// (an accent fill) must never reach a navigation row, whose ink would sit on orange.
test('navigation hover: idle rows stay neutral, the selected row keeps its rail', async ({ browser }, testInfo) => {
  const { viewport, colorScheme } = testInfo.project.use;
  const context = await browser.newContext({ extraHTTPHeaders: ownIP, viewport, colorScheme });
  const page = await context.newPage();
  await page.goto('/');
  await page.getByLabel('Username', { exact: true }).fill('walter');
  await page.locator('input[type=password]').fill('WalterUpdated456!');
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  // Nothing saved: Busnes Light or Dark follows the project's colour scheme.
  // The calendar page mounts the header's Calendars disclosure, which narrows the phone row: hover
  // only once that layout is final.
  await expect(page.locator('.fc')).toBeVisible();
  const nav = page.getByRole('navigation', { name: 'Primary' });
  for (const button of await nav.getByRole('button').all()) {
    await button.focus();
    await expect.poll(() => button.evaluate((el) => {
      const item = el.getBoundingClientRect(), strip = el.closest('nav').getBoundingClientRect();
      return item.left >= strip.left - 1 && item.right <= strip.right + 1;
    })).toBe(true);
    if (viewport.width <= 720) await expect(nav).toHaveCSS('mask-image', 'none');
  }

  // Colours after the hover transition has finished, normalised through a 1px canvas (the hover
  // mixes colours, which compute to color(srgb …)), with the text's contrast on its background.
  const hovered = async (item) => {
    await item.hover();
    return item.evaluate(async (el) => {
      // A transition cancelled mid-way (the phone row scrolls on hover) is replaced by a new one.
      while (el.getAnimations().length) await Promise.all(el.getAnimations().map((a) => a.finished.catch(() => {})));
      const g = new OffscreenCanvas(1, 1).getContext('2d');
      const rgb = (css) => { g.clearRect(0, 0, 1, 1); g.fillStyle = css; g.fillRect(0, 0, 1, 1); return [...g.getImageData(0, 0, 1, 1).data.slice(0, 3)]; };
      const lum = (c) => c.map((v) => { v /= 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; }).reduce((t, v, i) => t + v * [0.2126, 0.7152, 0.0722][i], 0);
      const root = getComputedStyle(document.documentElement);
      const cs = getComputedStyle(el);
      const [bg, fg] = [rgb(cs.backgroundColor), rgb(cs.color)];
      const [hi, lo] = [lum(fg), lum(bg)].sort((x, y) => y - x);
      return {
        hover: el.matches(':hover'), bg, contrast: (hi + 0.05) / (lo + 0.05), shadow: cs.boxShadow,
        panelHover: rgb(root.getPropertyValue('--panel-hover')), accentSoft: rgb(root.getPropertyValue('--accent-soft')), accent: rgb(root.getPropertyValue('--accent')),
      };
    });
  };

  const calendar = nav.getByRole('button', { name: 'Calendar', exact: true });
  const selected = await hovered(calendar);
  expect(selected.bg).toEqual(selected.accentSoft);
  expect(selected.shadow).toContain(`rgb(${selected.accent.join(', ')})`);
  expect(selected.shadow).toContain('inset');
  expect(selected.contrast).toBeGreaterThanOrEqual(4.5);

  // On Settings no navigation row is current: Calendar and the closed section tabs take the neutral hover.
  await page.getByRole('button', { name: 'Settings', exact: true }).click();
  await page.getByRole('menu', { name: 'Settings' }).getByRole('menuitem', { name: 'Account' }).click();
  const tabs = page.getByRole('navigation', { name: 'Settings sections' });
  await expect(calendar).not.toHaveAttribute('aria-current');
  for (const [name, item] of [['Calendar', calendar], ['Phones & apps tab', tabs.getByRole('link', { name: 'Phones & apps', exact: true })], ['Appearance tab', tabs.getByRole('link', { name: 'Appearance', exact: true })]]) {
    const s = await hovered(item);
    testInfo.annotations.push({ type: `hover contrast: ${name}`, description: s.contrast.toFixed(2) });
    expect(s.hover).toBe(true);
    expect(s.bg).toEqual(s.panelHover);
    expect(s.contrast).toBeGreaterThanOrEqual(4.5);
  }
  await context.close();
});

import { test, expect } from '@playwright/test';

const ownIP = { 'X-Forwarded-For': '203.0.113.27' };

// The licence line opens the About window with the server's version and the MIT text, on phones too.
test('about: the licence line opens the version and licence', async ({ browser }, testInfo) => {
  const { viewport, colorScheme } = testInfo.project.use;
  const context = await browser.newContext({ extraHTTPHeaders: ownIP, viewport, colorScheme });
  const page = await context.newPage();
  await page.goto('/');
  await page.getByLabel('Username', { exact: true }).fill('walter');
  await page.locator('input[type=password]').fill('WalterUpdated456!');
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(page.locator('.fc')).toBeVisible();
  const link = page.getByRole('button', { name: /MIT License/ });
  await link.click();
  const about = page.getByRole('dialog', { name: 'About KyCalendar' });
  await expect(about).toBeVisible();
  await expect(about.locator('.ky-about-version')).toHaveText(/^v\d+\.\d+\.\d+/);
  await expect(about.getByLabel('MIT License')).toHaveValue(/^MIT License\n\nCopyright \(c\) 2026 Busnes\.app/);
  await page.screenshot({ path: testInfo.outputPath('about.png') });
  await page.keyboard.press('Escape');
  await expect(about).toBeHidden();
  await expect(link).toBeFocused();
});

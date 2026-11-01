import { test, expect } from '@playwright/test';

const ownIP = { 'X-Forwarded-For': '203.0.113.30' };

async function signIn(browser, testInfo, username, password) {
  const { viewport, colorScheme } = testInfo.project.use;
  const context = await browser.newContext({ extraHTTPHeaders: ownIP, viewport, colorScheme });
  const page = await context.newPage();
  await page.goto('/');
  await page.getByLabel('Username', { exact: true }).fill(username);
  await page.locator('input[type=password]').fill(password);
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(page.getByRole('navigation', { name: 'Primary' })).toBeVisible();
  return { context, page };
}

// The gear opens a menu of the Settings sections (ky-ui 0.11.0), in the footer and the phone header.
test('settings menu: keyboard, current section and closing', async ({ browser }, testInfo) => {
  const { context, page } = await signIn(browser, testInfo, 'walter', 'WalterUpdated456!');
  await expect(page.locator('.fc')).toBeVisible();
  const gear = page.getByRole('button', { name: 'Settings', exact: true });
  const menu = page.getByRole('menu', { name: 'Settings' });
  const items = menu.getByRole('menuitem');
  await expect(gear).toHaveCount(1); // the other placement is hidden at this width
  await expect(gear).toHaveAttribute('aria-expanded', 'false');
  await expect(menu).toBeHidden();

  await gear.focus();
  await page.keyboard.press('Enter');
  await expect(menu).toBeVisible();
  await expect(items).toHaveText(['Account', 'Phones & apps', 'Appearance']);
  await expect(items.first()).toBeFocused();
  // The panel stays inside the window.
  const box = await menu.boundingBox();
  const { width, height } = page.viewportSize();
  expect(box.x).toBeGreaterThanOrEqual(0);
  expect(box.y).toBeGreaterThanOrEqual(0);
  expect(box.x + box.width).toBeLessThanOrEqual(width);
  expect(box.y + box.height).toBeLessThanOrEqual(height);
  await page.keyboard.press('ArrowDown');
  await expect(items.nth(1)).toBeFocused();
  await page.keyboard.press('End');
  await expect(items.nth(2)).toBeFocused();
  await page.keyboard.press('ArrowDown');
  await expect(items.first()).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(menu).toBeHidden();
  await expect(gear).toBeFocused();

  // Space opens too; Enter on an item follows it and leaves focus on the gear.
  await page.keyboard.press('Space');
  await expect(items.first()).toBeFocused();
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(/#\/settings\/devices$/);
  await expect(menu).toBeHidden();
  await expect(gear).toBeFocused();
  await expect(gear).toHaveAttribute('aria-current', 'page');
  await expect(page.getByRole('navigation', { name: 'Settings sections' }).getByRole('link', { name: 'Phones & apps' })).toHaveAttribute('aria-current', 'page');

  await page.keyboard.press('ArrowDown');
  await expect(menu.getByRole('menuitem', { name: 'Phones & apps' })).toHaveAttribute('aria-current', 'page');
  await expect(menu.locator('[aria-current]')).toHaveCount(1);
  await page.keyboard.press('Tab');
  await expect(menu).toBeHidden();
  await gear.click();
  await expect(menu).toBeVisible();
  // A corner outside the menu (on phones it covers the page head).
  await page.mouse.click(4, height - 4);
  await expect(menu).toBeHidden();
  await context.close();
});

test('settings menu: administrators get no Phones & apps', async ({ browser }, testInfo) => {
  const { context, page } = await signIn(browser, testInfo, 'admin', 'BrowserUpdated456!');
  await page.getByRole('button', { name: 'Settings', exact: true }).click();
  await expect(page.getByRole('menu', { name: 'Settings' }).getByRole('menuitem')).toHaveText(['Account', 'Appearance']);
  await context.close();
});

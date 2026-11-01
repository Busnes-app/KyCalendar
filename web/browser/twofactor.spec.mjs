import { test, expect } from '@playwright/test';
import { createHmac } from 'node:crypto';

// RFC 6238 with the server's parameters (SHA-1, 6 digits, 30 s), from the base32 setup key.
function totp(secret, at = Date.now()) {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
  let bits = '';
  for (const ch of secret.replace(/=+$/, '')) bits += alphabet.indexOf(ch).toString(2).padStart(5, '0');
  const key = Buffer.from(bits.match(/.{8}/g).map((b) => parseInt(b, 2)));
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(at / 1000 / 30)));
  const mac = createHmac('sha1', key).update(counter).digest();
  const offset = mac[mac.length - 1] & 0xf;
  return String((mac.readUInt32BE(offset) & 0x7fffffff) % 1_000_000).padStart(6, '0');
}

async function signIn(page, username, password) {
  await page.getByLabel('Username', { exact: true }).fill(username);
  await page.locator('input[type=password]').fill(password);
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
}

// Own client address so this spec's sign-ins keep a separate login-limiter bucket.
const ownIP = { 'X-Forwarded-For': '203.0.113.22' };

test('two-step verification: a person enrols, then sign-in asks for the code', async ({ page, browser }, testInfo) => {
  const username = `t${Date.now()}-${testInfo.project.name}`;
  const chosen = 'PersonChosen789!';

  await page.setExtraHTTPHeaders(ownIP);
  await page.goto('/');
  await signIn(page, 'admin', 'BrowserUpdated456!');
  await page.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name: 'People', exact: true }).click();
  await page.getByRole('button', { name: 'Add person' }).click();
  const add = page.getByRole('dialog', { name: 'Add person' });
  await add.getByLabel('Username').fill(username);
  await add.getByLabel('Display name').fill('Tess Example');
  await add.getByRole('button', { name: 'Create' }).click();
  const handover = page.getByRole('dialog', { name: 'Temporary password' });
  const temporary = (await handover.getByTestId('temporary-password').textContent()).trim();
  await handover.getByRole('button', { name: 'Done' }).click();

  const context = await browser.newContext({ extraHTTPHeaders: ownIP });
  const person = await context.newPage();
  await person.goto('/');
  await signIn(person, username, temporary);
  await person.getByLabel('Current password').fill(temporary);
  await person.getByLabel('New password', { exact: true }).fill(chosen);
  await person.getByLabel('Confirm new password').fill(chosen);
  await person.getByRole('button', { name: 'Change password' }).click();
  await expect(person.getByRole('status')).toContainText('Password changed');
  await signIn(person, username, chosen);

  // Two-step verification is in Settings, on the Account section of the gear's menu.
  await person.getByRole('button', { name: 'Settings', exact: true }).click();
  await person.getByRole('menu', { name: 'Settings' }).getByRole('menuitem', { name: 'Account' }).click();
  await expect(person.getByRole('navigation', { name: 'Settings sections' }).getByRole('link', { name: 'Account' })).toHaveAttribute('aria-current', 'page');
  await person.getByLabel('Password').fill(chosen);
  await person.getByRole('button', { name: 'Set up' }).click();
  await expect(person.getByRole('link', { name: 'setup link' })).toHaveAttribute('href', /^otpauth:\/\/totp\//);
  const secret = (await person.locator('.secret code').textContent()).trim();

  // A wrong code leaves it off.
  await person.getByLabel('Code from the app').fill(totp(secret) === '000000' ? '111111' : '000000');
  await person.getByRole('button', { name: 'Turn on' }).click();
  await expect(person.getByRole('alert')).toContainText('Invalid');

  // The previous step's code confirms, leaving the current one for the sign-in below.
  await person.getByLabel('Code from the app').fill(totp(secret, Date.now() - 30_000));
  await person.getByRole('button', { name: 'Turn on' }).click();
  await expect(person.getByRole('status')).toContainText('Save your recovery codes');
  await person.getByRole('button', { name: 'Done' }).click();
  await expect(person.getByRole('heading', { name: 'Two-step verification is on' })).toBeVisible();

  await person.getByRole('button', { name: 'Sign out' }).click();
  await signIn(person, username, chosen);
  await expect(person.getByRole('heading', { name: 'Two-step verification', exact: true })).toBeVisible();
  await person.getByLabel('Authenticator code').fill(totp(secret));
  await person.getByRole('button', { name: 'Verify and sign in' }).click();
  await expect(person.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name: 'Calendar' })).toBeVisible();
  await context.close();
});

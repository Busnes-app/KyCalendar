// Captures the Tasks + menu and the Flagged from mail dialog at 1280px and 390px in light and
// dark. Not a spec (CI skips it). `unlinked` names no KyPost; `linked` names one that is never
// reached (the link is up from the environment) and also captures the switch turned off.
// Usage (from web/): node browser/flag-shots.mjs <server-binary> <out-dir> <unlinked|linked>
// Starts its own throwaway server on 127.0.0.1:5396 (relays empty) and removes its data after.
import { mkdtemp, mkdir, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { spawn, spawnSync } from 'node:child_process';
import { chromium, request } from '@playwright/test';

const [binary, out] = process.argv.slice(2, 4).map((p) => resolve(p));
const mode = process.argv[4] ?? 'unlinked';
const base = 'http://127.0.0.1:5396';
const dir = await mkdtemp(join(tmpdir(), 'ky-flag-shots-'));
const env = {
  PATH: process.env.PATH, PUSH_RELAY_URL: '', APNS_RELAY_URL: '',
  KY_APP_URL: base, KY_HOST: '127.0.0.1', KY_PORT: '5396', KY_DB_DRIVER: 'sqlite',
  KY_DATA_DIR: join(dir, 'data'), KYCALENDAR_BACKUP_DIR: join(dir, 'backups'),
  KY_CAPTCHA_PROVIDER: 'none', KY_TRUSTED_PROXIES: '127.0.0.1', KYCALENDAR_SCHEDULING: 'false',
};
if (mode === 'linked') {
  await writeFile(join(dir, 'kypost.secret'), 'flag-shots-secret-0123456789abcdef\n', { mode: 0o600 });
  Object.assign(env, { KYCALENDAR_KYPOST_URL: 'https://kypost.invalid', KYCALENDAR_KYPOST_SECRET_FILE: join(dir, 'kypost.secret') });
}
const cli = (args, pw) => {
  if (spawnSync(binary, args, { cwd: dir, env, input: pw + '\n', stdio: ['pipe', 'inherit', 'inherit'] }).status !== 0) throw new Error(args.join(' '));
};
cli(['init-admin'], 'ShotsInitial123!');
cli(['create-user', '-username', 'walter'], 'ShotsInitial123!');
const server = spawn(binary, [], { cwd: dir, env, stdio: 'inherit' });
try {
  for (let i = 0; ; i++) {
    try { if ((await fetch(base)).ok) break; } catch { if (i > 100) throw new Error('server did not start'); }
    await new Promise((r) => setTimeout(r, 200));
  }
  const api = await request.newContext({ baseURL: base, extraHTTPHeaders: { 'X-Forwarded-For': '203.0.113.92' } });
  await api.get('/api/auth/me');
  const csrf = async () => (await api.storageState()).cookies.find((c) => c.name === 'ky_csrf')?.value;
  const ok = async (r) => { if (!r.ok()) throw new Error(`${r.status()} ${await r.text()}`); return r; };
  const login = async (password) => ok(await api.post('/api/auth/login', { data: { username: 'walter', password }, headers: { 'X-CSRF-Token': await csrf() } }));
  const call = async (method, path, data) => ok(await api.fetch(path, { method, data, headers: { 'X-CSRF-Token': await csrf() } }));
  await login('ShotsInitial123!');
  await call('POST', '/api/auth/change-password', { current_password: 'ShotsInitial123!', new_password: 'ShotsUpdated456!' });
  await api.get('/api/auth/me');
  await login('ShotsUpdated456!');
  await call('GET', '/api/calendars');
  const inbox = (await (await call('POST', '/api/tasks/inbox')).json()).calendar_id;
  const work = (await (await call('POST', '/api/calendars', { name: 'Website relaunch', components: ['VTODO'] })).json()).id;
  for (const [cal, title] of [[inbox, 'Renew passport'], [work, 'Compress the hero images'], [work, 'Write launch announcement']]) {
    await call('POST', `/api/calendars/${cal}/tasks`, { zone: 'UTC', title });
  }

  await mkdir(out, { recursive: true });
  const browser = await chromium.launch();
  const shoot = async (page, name, width, scheme) => {
    await page.waitForTimeout(400);
    await page.screenshot({ path: join(out, `${name}-${width}-${scheme}.png`) });
  };
  for (const colorScheme of ['light', 'dark']) {
    for (const width of [1280, 390]) {
      const ctx = await browser.newContext({ baseURL: base, viewport: { width, height: 800 }, colorScheme, extraHTTPHeaders: { 'X-Forwarded-For': '203.0.113.93' } });
      const page = await ctx.newPage();
      await page.goto('/');
      await page.getByLabel('Username', { exact: true }).fill('walter');
      await page.locator('input[type=password]').fill('ShotsUpdated456!');
      await page.getByRole('button', { name: 'Sign in', exact: true }).click();
      await page.locator('.ky-content').waitFor();
      await page.goto('/#/tasks');
      await page.locator('.tk-head h1').waitFor();
      if (width === 390) await page.locator('.kc-picker > summary').click();
      await page.getByRole('button', { name: 'Add a list or area' }).click();
      await shoot(page, `${mode}-menu`, width, colorScheme);
      await page.getByRole('menuitem', { name: 'Flagged from mail…' }).click();
      await page.getByRole('dialog').getByRole(mode === 'linked' ? 'switch' : 'note').waitFor();
      await shoot(page, `${mode}-dialog`, width, colorScheme);
      if (mode === 'linked') {
        await page.getByRole('switch').click();
        await page.waitForFunction(() => !document.querySelector('[role=switch]')?.checked);
        await shoot(page, 'linked-dialog-off', width, colorScheme);
        await page.getByRole('switch').click();
        await page.waitForFunction(() => document.querySelector('[role=switch]')?.checked);
      }
      await ctx.close();
    }
  }
  await browser.close();
} finally {
  server.kill('SIGTERM');
  await new Promise((r) => server.once('exit', r));
  await rm(dir, { recursive: true, force: true });
}

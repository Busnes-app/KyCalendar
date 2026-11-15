// Captures the booking page screens: Server settings (the switch), Settings → Booking pages and
// its editor as walter, and the public page a visitor sees, then the details form.
// Usage (from web/): node browser/booking-shots.mjs <server-binary> <out-dir> [before|room]
// (not a spec: CI skips it). "before" captures only the screens that exist without the feature;
// "room" captures the editor of a type with a meeting room (set in the disposable database).
// Starts its own throwaway server on 127.0.0.1:5391 (scheduling on, KyPost named but never
// reached, relays empty) and removes its data afterwards. No mail is sent: no hold is made.
import { mkdtemp, mkdir, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { spawn, spawnSync } from 'node:child_process';
import { chromium, request } from '@playwright/test';

const [binary, out] = process.argv.slice(2, 4).map((p) => resolve(p));
const before = process.argv[4] === 'before';
const room = process.argv[4] === 'room';
const base = 'http://127.0.0.1:5391';
const dir = await mkdtemp(join(tmpdir(), 'ky-booking-shots-'));
await writeFile(join(dir, 'kypost.secret'), 'booking-shots-secret-0123456789abcdef\n', { mode: 0o600 });
const env = {
  PATH: process.env.PATH, PUSH_RELAY_URL: '', APNS_RELAY_URL: '',
  KY_APP_URL: base, KY_HOST: '127.0.0.1', KY_PORT: '5391', KY_DB_DRIVER: 'sqlite',
  KY_DATA_DIR: join(dir, 'data'), KYCALENDAR_BACKUP_DIR: join(dir, 'backups'),
  KY_CAPTCHA_PROVIDER: 'none', KY_TRUSTED_PROXIES: '127.0.0.1', KYCALENDAR_SCHEDULING: 'true',
  // A KyPost that does not exist: the link is "paired", so booking pages can be allowed.
  KYCALENDAR_KYPOST_URL: 'https://kypost.invalid', KYCALENDAR_KYPOST_SECRET_FILE: join(dir, 'kypost.secret'),
};
const run = (args, pw) => {
  if (spawnSync(binary, args, { cwd: dir, env, input: pw + '\n', stdio: ['pipe', 'inherit', 'inherit'] }).status !== 0) throw new Error(args.join(' '));
};
run(['init-admin'], 'ShotsInitial123!');
run(['create-user', '-username', 'walter'], 'ShotsInitial123!');
// walter as KyPost knows a member: an email and a KyIdentity binding (the server is stopped).
const sql = `import sqlite3,sys; c=sqlite3.connect(sys.argv[1]); c.execute("UPDATE users SET email='walter@example.com', display_name='Walter Reyes', sso_subject='shots-walter', sso_issuer='kyidentity https://id.invalid' WHERE username='walter'"); c.commit()`;
if (spawnSync('python3', ['-I', '-c', sql, join(dir, 'data', 'kycalendar.db')], { stdio: 'inherit' }).status !== 0) throw new Error('seed walter');
const server = spawn(binary, [], { cwd: dir, env, stdio: 'inherit' });
try {
  for (let i = 0; ; i++) {
    try { if ((await fetch(base)).ok) break; } catch { if (i > 100) throw new Error('server did not start'); }
    await new Promise((r) => setTimeout(r, 200));
  }
  const signIn = async (name, ip) => {
    const api = await request.newContext({ baseURL: base, extraHTTPHeaders: { 'X-Forwarded-For': ip } });
    await api.get('/api/auth/me');
    const ok = async (r) => { if (!r.ok()) throw new Error(`${name}: ${r.status()} ${await r.text()}`); return r; };
    const csrf = async () => (await api.storageState()).cookies.find((c) => c.name === 'ky_csrf')?.value;
    await ok(await api.post('/api/auth/login', { data: { username: name, password: 'ShotsInitial123!' }, headers: { 'X-CSRF-Token': await csrf() } }));
    api.call = async (method, path, data) => ok(await api.fetch(path, { method, data, headers: { 'X-CSRF-Token': await csrf() } }));
    await api.call('POST', '/api/auth/change-password', { current_password: 'ShotsInitial123!', new_password: 'ShotsUpdated456!' });
    await api.get('/api/auth/me');
    await ok(await api.post('/api/auth/login', { data: { username: name, password: 'ShotsUpdated456!' }, headers: { 'X-CSRF-Token': await csrf() } }));
    return api;
  };
  const admin = await signIn('admin', '203.0.113.70');
  const walter = await signIn('walter', '203.0.113.71');
  let slug = '';
  if (!before) {
    await admin.call('PUT', '/api/admin/booking', { allowed: true });
    const cal = (await (await walter.call('GET', '/api/calendars')).json())[0].id;
    const windows = [1, 2, 3, 4, 5].map((day) => ({ day, start: '09:00', end: '17:00' }));
    const t = await (await walter.call('POST', '/api/booking-types', {
      enabled: true, title: 'Project intro call', duration_min: 30, zone: 'America/New_York', windows, buffer_after_min: 10, calendar_id: cal,
    })).json();
    slug = t.slug;
    if (room) {
      const set = `import sqlite3,sys; c=sqlite3.connect(sys.argv[1]); c.execute("UPDATE booking_types SET meeting_room=1"); c.commit()`;
      if (spawnSync('python3', ['-I', '-c', set, join(dir, 'data', 'kycalendar.db')], { stdio: 'inherit' }).status !== 0) throw new Error('set room');
    }
  }

  await mkdir(out, { recursive: true });
  const browser = await chromium.launch();
  const shoot = async (page, name, width, scheme) => {
    await page.waitForTimeout(300);
    await page.screenshot({ path: join(out, `${name}-${width}-${scheme}.png`), fullPage: width !== 390 });
  };
  for (const colorScheme of ['light', 'dark']) {
    for (const width of [1280, 390]) {
      const ctx = await browser.newContext({ baseURL: base, viewport: { width, height: 900 }, colorScheme, extraHTTPHeaders: { 'X-Forwarded-For': '203.0.113.72' } });
      const signInPage = async (name) => {
        const page = await ctx.newPage();
        await page.goto('/');
        await page.getByLabel('Username', { exact: true }).fill(name);
        await page.locator('input[type=password]').fill('ShotsUpdated456!');
        await page.getByRole('button', { name: 'Sign in', exact: true }).click();
        await page.locator('.ky-content').waitFor();
        return page;
      };
      const a = await signInPage('admin');
      await a.goto('/#/server-settings');
      await a.getByRole('heading', { name: 'Server settings' }).waitFor();
      if (!before) await a.getByRole('switch', { name: 'Allow booking pages' }).scrollIntoViewIfNeeded();
      await shoot(a, 'server-settings', width, colorScheme);
      await a.getByRole('button', { name: /sign out/i }).first().click().catch(() => {});
      await ctx.clearCookies();
      const w = await signInPage('walter');
      await w.goto(before ? '/#/settings/account' : '/#/settings/booking');
      await w.getByRole('heading', { level: 1, name: 'Settings' }).waitFor();
      if (!before) {
        await w.getByText('Project intro call').first().waitFor();
        await shoot(w, 'member-list', width, colorScheme);
        await w.getByRole('button', { name: 'Edit' }).click();
        await w.getByRole('dialog').waitFor();
        if (room) {
          await w.getByLabel('Add a KyMessages meeting room').scrollIntoViewIfNeeded();
          await shoot(w, 'member-editor-room', width, colorScheme);
          await ctx.close();
          continue;
        }
        await shoot(w, 'member-editor', width, colorScheme);
      } else {
        await shoot(w, 'settings-tabs', width, colorScheme);
      }
      await ctx.close();
      if (before) continue;
      // A visitor: a fresh browser with no account, in another time zone.
      const vctx = await browser.newContext({ baseURL: base, viewport: { width, height: 900 }, colorScheme, timezoneId: 'Europe/Berlin', extraHTTPHeaders: { 'X-Forwarded-For': '203.0.113.73' } });
      const v = await vctx.newPage();
      await v.goto(`/book/${slug}`);
      await v.getByRole('heading', { level: 1, name: 'Project intro call' }).waitFor();
      await shoot(v, 'public-page', width, colorScheme);
      await v.locator('.book-slots button').first().click();
      await v.getByLabel('Your name').fill('Carol Visitor');
      await v.getByLabel('Your email').fill('carol@example.net');
      await shoot(v, 'public-form', width, colorScheme);
      await vctx.close();
    }
  }
  await browser.close();
} finally {
  server.kill('SIGTERM');
  await new Promise((r) => server.once('exit', r));
  await rm(dir, { recursive: true, force: true });
}

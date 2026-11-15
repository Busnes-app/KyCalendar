// Captures walter's calendar after alice cancels a meeting he answered and she then moved.
// Usage (from web/): node browser/cancel-shots.mjs <server-binary> <out-dir> (not a spec: CI skips it)
// Starts its own throwaway server on 127.0.0.1:5391 (scheduling on, relays empty) and removes
// its data afterwards.
import { mkdtemp, mkdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { spawn, spawnSync } from 'node:child_process';
import { chromium, request } from '@playwright/test';

const [binary, out] = process.argv.slice(2).map((p) => resolve(p));
const base = 'http://127.0.0.1:5391';
const dir = await mkdtemp(join(tmpdir(), 'ky-cancel-shots-'));
const env = {
  PATH: process.env.PATH, PUSH_RELAY_URL: '', APNS_RELAY_URL: '',
  KY_APP_URL: base, KY_HOST: '127.0.0.1', KY_PORT: '5391', KY_DB_DRIVER: 'sqlite',
  KY_DATA_DIR: join(dir, 'data'), KYCALENDAR_BACKUP_DIR: join(dir, 'backups'),
  KY_CAPTCHA_PROVIDER: 'none', KY_TRUSTED_PROXIES: '127.0.0.1', KYCALENDAR_SCHEDULING: 'true',
};
const run = (args, pw) => {
  if (spawnSync(binary, args, { cwd: dir, env, input: pw + '\n', stdio: ['pipe', 'inherit', 'inherit'] }).status !== 0) throw new Error(args.join(' '));
};
run(['init-admin'], 'ShotsInitial123!');
for (const u of ['alice', 'walter']) run(['create-user', '-username', u], 'ShotsInitial123!');
const server = spawn(binary, [], { cwd: dir, env, stdio: 'inherit' });
try {
  for (let i = 0; ; i++) {
    try { if ((await fetch(base)).ok) break; } catch { if (i > 100) throw new Error('server did not start'); }
    await new Promise((r) => setTimeout(r, 200));
  }
  // signIn is an API session for name, with its first password replaced.
  const signIn = async (name, ip) => {
    const api = await request.newContext({ baseURL: base, extraHTTPHeaders: { 'X-Forwarded-For': ip } });
    await api.get('/');
    const ok = async (r) => { if (!r.ok()) throw new Error(`${name}: ${r.status()} ${await r.text()}`); return r; };
    await ok(await api.post('/api/auth/login', { data: { username: name, password: 'ShotsInitial123!' } }));
    api.call = async (method, path, data, headers = {}) => {
      const token = (await api.storageState()).cookies.find((c) => c.name === 'ky_csrf')?.value;
      return ok(await api.fetch(path, { method, data, headers: { 'X-CSRF-Token': token, ...headers } }));
    };
    await api.call('POST', '/api/auth/change-password', { current_password: 'ShotsInitial123!', new_password: 'ShotsUpdated456!' });
    await ok(await api.post('/api/auth/login', { data: { username: name, password: 'ShotsUpdated456!' } })); // the change ends the session
    return api;
  };
  const admin = await signIn('admin', '203.0.113.60');
  const users = await (await admin.call('GET', '/api/admin/users')).json();
  for (const u of (users.users ?? users)) {
    if (u.username === 'alice' || u.username === 'walter') await admin.call('PATCH', `/api/admin/users/${u.id}`, { email: `${u.username}@example.com` });
  }
  const alice = await signIn('alice', '203.0.113.61');
  const walter = await signIn('walter', '203.0.113.62');
  const calOf = async (api) => (await (await api.call('GET', '/api/calendars')).json())[0].id;
  const aliceCal = await calOf(alice);
  const meeting = (start, end) => ({ title: 'Quarterly planning', start, end, zone: 'UTC', attendees: ['walter@example.com'] });
  const made = await (await alice.call('POST', `/api/calendars/${aliceCal}/events`, meeting('2026-11-17T10:00:00Z', '2026-11-17T11:00:00Z'))).json();
  // waitFor polls f until it returns a value (the scheduler delivers every 5 s).
  const waitFor = async (f) => { for (let i = 0; i < 60; i++) { const v = await f(); if (v) return v; await new Promise((r) => setTimeout(r, 1000)); } throw new Error('timed out'); };
  const walterEvent = async () => (await (await walter.call('GET', '/api/events?start=2026-11-01T00:00:00Z&end=2026-12-01T00:00:00Z&tz=UTC')).json()).find((e) => e.uid === made.uid);
  const copy = await waitFor(walterEvent);
  await walter.call('POST', `/api/events/${copy.calendar_id}/${made.uid}/rsvp`, { partstat: 'ACCEPTED' }, { 'If-Match': `"${copy.etag}"` });
  // alice moves it an hour (walter is asked again), then deletes it.
  const mine = async () => (await (await alice.call('GET', '/api/events?start=2026-11-01T00:00:00Z&end=2026-12-01T00:00:00Z&tz=UTC')).json()).find((e) => e.uid === made.uid);
  await new Promise((r) => setTimeout(r, 6000));
  await alice.call('PUT', `/api/events/${aliceCal}/${made.uid}`, meeting('2026-11-17T11:00:00Z', '2026-11-17T12:00:00Z'), { 'If-Match': `"${(await mine()).etag}"` });
  await waitFor(async () => (await walterEvent())?.start?.startsWith('2026-11-17T11'));
  await alice.call('DELETE', `/api/events/${aliceCal}/${made.uid}?scope=all`, undefined, { 'If-Match': `"${(await mine()).etag}"` });
  await new Promise((r) => setTimeout(r, 12000)); // two scheduler passes
  console.log('walter after the cancel:', JSON.stringify((await walterEvent()) ?? 'no copy'));

  await mkdir(out, { recursive: true });
  const browser = await chromium.launch();
  for (const colorScheme of ['light', 'dark']) {
    for (const width of [1280, 390]) {
      const ctx = await browser.newContext({ baseURL: base, viewport: { width, height: 900 }, colorScheme, extraHTTPHeaders: { 'X-Forwarded-For': '203.0.113.63' } });
      const page = await ctx.newPage();
      await page.goto('/');
      await page.getByLabel('Username', { exact: true }).fill('walter');
      await page.locator('input[type=password]').fill('ShotsUpdated456!');
      await page.getByRole('button', { name: 'Sign in', exact: true }).click();
      await page.locator('.fc').waitFor();
      await page.locator('.fc-next-button').click().catch(() => {}); // the meeting is in the next week on phone Day/3-day views
      await page.locator('.fc-prev-button').click().catch(() => {});
      await page.locator('.fc-listWeek-button, .fc-list-button').first().click().catch(() => {});
      await page.waitForTimeout(800);
      const tag = `${colorScheme}-${width}`;
      await page.screenshot({ path: join(out, `calendar-${tag}.png`), animations: 'disabled' });
      const ev = page.getByText(/Quarterly planning/).first();
      if (await ev.isVisible().catch(() => false)) {
        await ev.click();
        await page.locator('dialog.kc-dialog[open]').waitFor();
        await page.waitForTimeout(300);
        await page.screenshot({ path: join(out, `event-${tag}.png`), animations: 'disabled' });
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

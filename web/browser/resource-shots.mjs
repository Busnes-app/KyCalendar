// Captures the room and equipment screens: the administrator's Rooms and equipment list and
// editor, and as members the room finder in the event editor, an event with rooms and Room
// requests. Usage (from web/): node browser/resource-shots.mjs <server-binary> <out-dir> [mode]
// (not a spec: CI skips it). Modes: admin (default), before (the admin navigation without the
// feature), booking (the member screens), booking-before (the event editor without the feature).
// Starts its own throwaway server on 127.0.0.1:5391 (relays empty, scheduling off) and removes
// its data afterwards.
import { mkdtemp, mkdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { spawn, spawnSync } from 'node:child_process';
import { chromium, request } from '@playwright/test';

const [binary, out] = process.argv.slice(2, 4).map((p) => resolve(p));
const mode = process.argv[4] ?? 'admin';
const base = 'http://127.0.0.1:5391';
const dir = await mkdtemp(join(tmpdir(), 'ky-resource-shots-'));
const env = {
  PATH: process.env.PATH, PUSH_RELAY_URL: '', APNS_RELAY_URL: '',
  KY_APP_URL: base, KY_HOST: '127.0.0.1', KY_PORT: '5391', KY_DB_DRIVER: 'sqlite',
  KY_DATA_DIR: join(dir, 'data'), KYCALENDAR_BACKUP_DIR: join(dir, 'backups'),
  KY_CAPTCHA_PROVIDER: 'none', KY_TRUSTED_PROXIES: '127.0.0.1', KYCALENDAR_SCHEDULING: 'false',
};
const run = (args, pw) => {
  if (spawnSync(binary, args, { cwd: dir, env, input: pw + '\n', stdio: ['pipe', 'inherit', 'inherit'] }).status !== 0) throw new Error(args.join(' '));
};
run(['init-admin'], 'ShotsInitial123!');
for (const name of ['walter', 'ann']) run(['create-user', '-username', name], 'ShotsInitial123!');
const sql = `import sqlite3,sys; c=sqlite3.connect(sys.argv[1]); c.execute("UPDATE users SET email='walter@example.com', display_name='Walter Reyes' WHERE username='walter'"); c.execute("UPDATE users SET display_name='Ann Okafor' WHERE username='ann'"); c.commit()`;
if (spawnSync('python3', ['-I', '-c', sql, join(dir, 'data', 'kycalendar.db')], { stdio: 'inherit' }).status !== 0) throw new Error('seed people');
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
  const admin = await signIn('admin', '203.0.113.80');
  const walterAPI = await signIn('walter', '203.0.113.81');
  await signIn('ann', '203.0.113.82');
  const booking = mode.startsWith('booking');
  if (!mode.endsWith('before')) {
    const users = (await (await admin.call('GET', '/api/admin/users?q=ann')).json()).users;
    const group = await (await admin.call('POST', '/api/admin/groups', { display_name: 'Studio' })).json();
    const room = { location: 'Floor 2, east wing', audience: 'everyone', approval: 'auto', max_minutes: 240, horizon_days: 180, enabled: true };
    await admin.call('POST', '/api/admin/resources', { ...room, kind: 'room', name: 'Fjord', capacity: 8, tags: ['screen', 'video'] });
    await admin.call('POST', '/api/admin/resources', { ...room, kind: 'room', name: 'Harbour', location: 'Floor 4', capacity: 20, tags: ['screen', 'whiteboard'],
      audience: 'groups', groups: [group.id], approval: 'approvers', approvers: [users[0].id] });
    await admin.call('POST', '/api/admin/resources', { ...room, kind: 'equipment', name: 'Projector kit', location: 'Reception', capacity: 0, tags: [], max_minutes: 1440, enabled: false });
    const walterID = (await (await admin.call('GET', '/api/admin/users?q=walter')).json()).users[0].id;
    await admin.call('PUT', '/api/admin/groups/' + group.id + '/members/' + walterID);
  }
  // walter's event tomorrow at 10:00 local (UTC here) with Fjord, and Harbour waiting for ann.
  const day = new Date(Date.now() + 86400000);
  const at = (h) => new Date(Date.UTC(day.getUTCFullYear(), day.getUTCMonth(), day.getUTCDate(), h)).toISOString().replace('.000Z', 'Z');
  if (mode === 'booking') {
    const walter = walterAPI;
    const ids = (await (await walter.call('GET', '/api/resources')).json()).resources.map((r) => r.id);
    const cal = (await (await walter.call('GET', '/api/calendars')).json())[0].id;
    await walter.call('POST', '/api/calendars/' + cal + '/events', { title: 'Quarterly review', start: at(10), end: at(11), zone: 'UTC', resources: ids.slice(0, 1) });
    await walter.call('POST', '/api/calendars/' + cal + '/events', { title: 'Workshop', start: at(16), end: at(17), zone: 'UTC', resources: ids.slice(1, 2) });
  }

  await mkdir(out, { recursive: true });
  const browser = await chromium.launch();
  const shoot = async (page, name, width, scheme) => {
    await page.waitForTimeout(300);
    await page.screenshot({ path: join(out, `${name}-${width}-${scheme}.png`), fullPage: width !== 390 });
  };
  for (const colorScheme of ['light', 'dark']) {
    for (const width of [1280, 390]) {
      const ctx = await browser.newContext({ baseURL: base, viewport: { width, height: 900 }, colorScheme, timezoneId: 'UTC', extraHTTPHeaders: { 'X-Forwarded-For': '203.0.113.83' } });
      const page = await ctx.newPage();
      await page.goto('/');
      await page.getByLabel('Username', { exact: true }).fill(booking ? 'walter' : 'admin');
      await page.locator('input[type=password]').fill('ShotsUpdated456!');
      await page.getByRole('button', { name: 'Sign in', exact: true }).click();
      await page.locator('.ky-content').waitFor();
      if (booking) {
        await page.goto('/#/calendar');
        if (width <= 720) await page.locator('.ky-sidebar summary', { hasText: 'Calendars' }).click().catch(() => {});
        await page.getByRole('button', { name: 'New event' }).click();
        const dialog = page.getByRole('dialog');
        await dialog.getByLabel('Title').fill('Design review');
        if (mode === 'booking') {
          await dialog.getByLabel('Start').fill(at(14).slice(0, 16));
          await dialog.getByLabel('End').fill(at(15).slice(0, 16));
          await dialog.getByRole('button', { name: 'Add room' }).click();
          await dialog.getByRole('button', { name: 'Add Harbour' }).waitFor();
          await dialog.getByRole('button', { name: 'Add room' }).or(dialog.getByRole('group', { name: 'Find a room' })).first().scrollIntoViewIfNeeded();
          await shoot(page, 'room-finder', width, colorScheme);
          await dialog.getByRole('button', { name: 'Add Harbour' }).click();
          await dialog.getByRole('button', { name: 'Save', exact: true }).click();
          await dialog.waitFor({ state: 'hidden' });
          await page.locator('.fc-listWeek-button').click();
          const item = page.locator('.fc-list-event', { hasText: 'Quarterly review' });
          if (!(await item.count())) await page.locator('.fc-next-button').click();
          await item.click();
          await page.getByRole('dialog').getByText('Rooms and equipment').scrollIntoViewIfNeeded();
          await shoot(page, 'event-with-room', width, colorScheme);
          await page.getByRole('dialog').getByRole('button', { name: 'Cancel' }).click();
          const pending = page.locator('.fc-list-event', { hasText: 'Design review' });
          await pending.click();
          await page.getByRole('dialog').getByText('Waiting for approval').scrollIntoViewIfNeeded();
          await shoot(page, 'event-pending-room', width, colorScheme);
          page.once('dialog', (d) => void d.accept());
          await page.getByRole('dialog').getByRole('button', { name: 'Delete' }).click();
          await page.getByRole('dialog').waitFor({ state: 'hidden' });
        } else {
          await shoot(page, 'event-editor-before', width, colorScheme);
        }
        await ctx.close();
        if (mode === 'booking') {
          const actx = await browser.newContext({ baseURL: base, viewport: { width, height: 900 }, colorScheme, extraHTTPHeaders: { 'X-Forwarded-For': '203.0.113.85' } });
          const ann = await actx.newPage();
          await ann.goto('/');
          await ann.getByLabel('Username', { exact: true }).fill('ann');
          await ann.locator('input[type=password]').fill('ShotsUpdated456!');
          await ann.getByRole('button', { name: 'Sign in', exact: true }).click();
          await ann.locator('.ky-content').waitFor();
          await ann.goto('/#/room-requests');
          await ann.getByRole('heading', { name: 'Harbour' }).waitFor();
          await shoot(ann, 'room-requests', width, colorScheme);
          await actx.close();
        }
        continue;
      }
      if (mode === 'before') {
        await page.goto('/#/group-calendars');
        await page.getByRole('heading', { name: 'Group calendars' }).waitFor();
        await shoot(page, 'admin-nav-before', width, colorScheme);
      } else {
        await page.goto('/#/resources');
        await page.getByRole('heading', { name: /Harbour/ }).waitFor();
        await shoot(page, 'admin-list', width, colorScheme);
        await page.getByRole('button', { name: 'Edit' }).nth(1).click();
        await page.getByRole('dialog').waitFor();
        await shoot(page, 'admin-editor', width, colorScheme);
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

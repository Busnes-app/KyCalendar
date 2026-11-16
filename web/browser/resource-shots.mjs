// Captures the room and equipment screens: the administrator's Rooms and equipment list and
// editor, and as members the room finder (busy count, filters matching nothing, no free room,
// scrolled to the end), events with rooms (booked room and equipment, pending, declined, a long
// name, read only, a repeating request) and Room requests (several with a repeating one, after an
// answer, and none). Usage (from web/): node browser/resource-shots.mjs <server-binary> <out-dir> [mode]
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
for (const name of ['walter', 'ann', 'noor']) run(['create-user', '-username', name], 'ShotsInitial123!');
const sql = `import sqlite3,sys; c=sqlite3.connect(sys.argv[1]); c.execute("UPDATE users SET email='walter@example.com', display_name='Walter Reyes' WHERE username='walter'"); c.execute("UPDATE users SET display_name='Ann Okafor' WHERE username='ann'"); c.execute("UPDATE users SET display_name='Noor Haddad' WHERE username='noor'"); c.commit()`;
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
  const annAPI = await signIn('ann', '203.0.113.82');
  await signIn('noor', '203.0.113.84');
  const booking = mode.startsWith('booking');
  if (!mode.endsWith('before')) {
    const users = (await (await admin.call('GET', '/api/admin/users?q=ann')).json()).users;
    const group = await (await admin.call('POST', '/api/admin/groups', { display_name: 'Studio' })).json();
    const room = { location: 'Floor 2, east wing', audience: 'everyone', approval: 'auto', max_minutes: 240, horizon_days: 180, enabled: true };
    await admin.call('POST', '/api/admin/resources', { ...room, kind: 'room', name: 'Fjord', capacity: 8, tags: ['screen', 'video'] });
    await admin.call('POST', '/api/admin/resources', { ...room, kind: 'room', name: 'Harbour', location: 'Floor 4', capacity: 20, tags: ['screen', 'whiteboard'],
      audience: 'groups', groups: [group.id], approval: 'approvers', approvers: [users[0].id] });
    await admin.call('POST', '/api/admin/resources', { ...room, kind: 'equipment', name: 'Projector kit', location: 'Reception', capacity: 0, tags: [], max_minutes: 1440, enabled: false });
    if (booking) {
      await admin.call('POST', '/api/admin/resources', { ...room, kind: 'room', name: 'Boardroom at the Old Customs House, Harbourside', location: 'Ground floor, north entrance', capacity: 14, tags: ['whiteboard'] });
      await admin.call('POST', '/api/admin/resources', { ...room, kind: 'equipment', name: 'Portable screen', location: 'Reception', capacity: 0, tags: ['video'] });
      await admin.call('POST', '/api/admin/resources', { ...room, kind: 'room', name: 'Quiet room', location: 'Floor 3', capacity: 2, tags: [], approval: 'approvers', approvers: [users[0].id] });
    }
    const walterID = (await (await admin.call('GET', '/api/admin/users?q=walter')).json()).users[0].id;
    await admin.call('PUT', '/api/admin/groups/' + group.id + '/members/' + walterID);
    if (booking) {
      // A group calendar walter reads and ann edits, for an event with a room walter cannot change.
      const leads = await (await admin.call('POST', '/api/admin/groups', { display_name: 'Leads' })).json();
      await admin.call('PUT', '/api/admin/groups/' + leads.id + '/members/' + users[0].id);
      const team = await (await admin.call('POST', '/api/admin/calendars', { name: 'Studio', color: '', description: '', components: ['VEVENT'] })).json();
      await admin.call('PUT', '/api/calendars/' + team.id + '/grants/' + group.id, { role: 'reader' });
      await admin.call('PUT', '/api/calendars/' + team.id + '/grants/' + leads.id, { role: 'editor' });
      // noor approves a room only Leads may book and nobody has asked for.
      const noorID = (await (await admin.call('GET', '/api/admin/users?q=noor')).json()).users[0].id;
      await admin.call('POST', '/api/admin/resources', { ...room, kind: 'room', name: 'Recording booth', location: 'Floor 1', capacity: 3, tags: [], audience: 'groups', groups: [leads.id], approval: 'approvers', approvers: [noorID] });
    }
  }
  // walter's events tomorrow (UTC): rooms booked, declined, pending, repeating and a long name.
  const day = new Date(Date.now() + 86400000);
  const at = (h, m = 0) => new Date(Date.UTC(day.getUTCFullYear(), day.getUTCMonth(), day.getUTCDate(), h, m)).toISOString().replace('.000Z', 'Z');
  if (mode === 'booking') {
    const walter = walterAPI;
    const byName = Object.fromEntries((await (await walter.call('GET', '/api/resources')).json()).resources.map((r) => [r.name, r.id]));
    const cal = (await (await walter.call('GET', '/api/calendars')).json()).find((c) => c.kind === 'personal').id;
    const ev = (title, from, to, rooms, extra = {}) => walter.call('POST', '/api/calendars/' + cal + '/events', { title, start: from, end: to, zone: 'UTC', resources: rooms.map((n) => byName[n]), ...extra });
    await ev('All hands prep', at(8), at(9), Object.keys(byName));
    await ev('Quarterly review', at(10), at(11), ['Fjord', 'Portable screen'], { location: 'Fjord, Floor 2, east wing' });
    await ev('Board meeting', at(12), at(13), ['Boardroom at the Old Customs House, Harbourside']);
    await ev('Standup sync', at(14), at(15), ['Fjord']);
    await ev('Budget check', at(14, 30), at(15), ['Fjord', 'Portable screen']);
    await ev('Workshop', at(16), at(17), ['Harbour']);
    await ev('Weekly planning', at(17, 30), at(18, 30), ['Harbour'], { repeat: { freq: 'weekly' } });
    const team = (await (await annAPI.call('GET', '/api/calendars')).json()).find((c) => c.name === 'Studio').id;
    await annAPI.call('POST', '/api/calendars/' + team + '/events', { title: 'Studio offsite', start: at(18, 45), end: at(19, 45), zone: 'UTC', resources: [byName['Fjord']] });
  }

  await mkdir(out, { recursive: true });
  const browser = await chromium.launch();
  const shoot = async (page, name, width, scheme) => {
    await page.waitForTimeout(300);
    // Viewport only: the sidebar is sticky at viewport height, so a stitched full page shows it ending mid-page.
    await page.screenshot({ path: join(out, `${name}-${width}-${scheme}.png`) });
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
        await page.getByRole('button', { name: 'New event' }).click();
        const dialog = page.getByRole('dialog');
        await dialog.locator('#kc-title').fill('Design review');
        if (mode === 'booking') {
          // Centre it: the dialog's sticky footer would cover what scrollIntoViewIfNeeded leaves at the edge.
          const reveal = (l) => l.evaluate((e) => e.scrollIntoView({ block: 'center' }));
          const setTime = async (a, b) => {
            await dialog.locator('#kc-start').fill(a.slice(0, 16));
            await dialog.locator('#kc-end').fill(b.slice(0, 16));
          };
          await setTime(at(14), at(15));
          await dialog.getByRole('button', { name: 'Add room' }).click();
          await dialog.getByRole('button', { name: 'Request Harbour' }).waitFor();
          await reveal(dialog.getByLabel('People'));
          await shoot(page, 'room-finder', width, colorScheme);
          await reveal(dialog.getByText(/busy then/));
          await shoot(page, 'room-finder-end', width, colorScheme);
          await dialog.getByLabel('People').fill('500');
          await reveal(dialog.getByText('Nothing matches these filters.'));
          await shoot(page, 'room-finder-no-match', width, colorScheme);
          await dialog.getByLabel('People').fill('');
          await setTime(at(8), at(9));
          await dialog.locator('#kc-end').blur();
          await reveal(dialog.getByText('Nothing is free at this time.'));
          await shoot(page, 'room-finder-none-free', width, colorScheme);
          await setTime(at(14), at(15));
          await dialog.getByRole('button', { name: 'Request Harbour' }).click();
          await dialog.getByRole('button', { name: 'Save', exact: true }).click();
          await dialog.waitFor({ state: 'hidden' });
          await page.locator('.fc-listWeek-button').click();
          const open = async (title, text, name) => {
            const item = page.locator('.fc-list-event', { hasText: title }).first();
            if (!(await item.count())) await page.locator('.fc-next-button').click();
            await item.click();
            await reveal(page.getByRole('dialog').getByText(text).first());
            await shoot(page, name, width, colorScheme);
            const d = page.getByRole('dialog');
            await d.getByRole('button', { name: /^(Cancel|Close)$/ }).first().click();
            await d.waitFor({ state: 'hidden' });
          };
          await open('Quarterly review', 'Rooms and equipment', 'event-with-room');
          await open('Budget check', 'Declined', 'event-declined-room');
          await open('Board meeting', 'Rooms and equipment', 'event-long-room');
          await open('Studio offsite', 'Rooms and equipment', 'event-read-only');
          await open('Weekly planning', 'Waiting for approval', 'event-repeating-pending');
          const pending = page.locator('.fc-list-event', { hasText: 'Design review' });
          await pending.click();
          await reveal(page.getByRole('dialog').getByText('Waiting for approval'));
          await shoot(page, 'event-pending-room', width, colorScheme);
          page.once('dialog', (d) => void d.accept());
          await page.getByRole('dialog').getByRole('button', { name: 'Delete' }).click();
          await page.getByRole('dialog').waitFor({ state: 'hidden' });
        } else {
          await shoot(page, 'event-editor-before', width, colorScheme);
        }
        await ctx.close();
        if (mode === 'booking') {
          // A fresh request each pass for ann to answer on screen; the times differ so passes never collide.
          const n = (colorScheme === 'dark' ? 2 : 0) + (width === 390 ? 1 : 0);
          const cal = (await (await walterAPI.call('GET', '/api/calendars')).json()).find((c) => c.kind === 'personal').id;
          const harbour = (await (await walterAPI.call('GET', '/api/resources')).json()).resources.find((r) => r.name === 'Harbour').id;
          await walterAPI.call('POST', '/api/calendars/' + cal + '/events', { title: 'Retro', start: at(20, n * 10), end: at(20, n * 10 + 10), zone: 'UTC', resources: [harbour] });
          for (const [who, name, ready] of [['ann', 'room-requests', 'Harbour'], ['noor', 'room-requests-empty', 'No requests waiting.']]) {
            const actx = await browser.newContext({ baseURL: base, viewport: { width, height: 900 }, colorScheme, timezoneId: 'UTC', extraHTTPHeaders: { 'X-Forwarded-For': '203.0.113.85' } });
            const ap = await actx.newPage();
            await ap.goto('/');
            await ap.getByLabel('Username', { exact: true }).fill(who);
            await ap.locator('input[type=password]').fill('ShotsUpdated456!');
            await ap.getByRole('button', { name: 'Sign in', exact: true }).click();
            await ap.locator('.ky-content').waitFor();
            await ap.goto('/#/room-requests');
            await ap.getByText(ready).first().waitFor();
            await shoot(ap, name, width, colorScheme);
            if (who === 'ann') {
              await ap.getByText('Repeats').first().evaluate((e) => e.scrollIntoView({ block: 'center' }));
              await shoot(ap, 'room-requests-repeating', width, colorScheme);
              await ap.getByRole('button', { name: 'Accept' }).last().click();
              await ap.getByText(/^Accepted: Harbour/).waitFor();
              await ap.evaluate(() => window.scrollTo(0, 0));
              await shoot(ap, 'room-requests-after-accept', width, colorScheme);
            }
            await actx.close();
          }
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

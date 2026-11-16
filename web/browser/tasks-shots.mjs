// Captures the Tasks screens with fake data: Today, Upcoming, a list, the task detail and the
// capture box, at 1280px and 390px in light and dark. Not a spec (CI skips it).
// Usage (from web/): node browser/tasks-shots.mjs <server-binary> <out-dir> [before]
// "before" captures the calendar sidebar's tasks-only list only (the screen as it was).
// Starts its own throwaway server on 127.0.0.1:5394 (relays empty) and removes its data after.
import { mkdtemp, mkdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { spawn, spawnSync } from 'node:child_process';
import { chromium, request } from '@playwright/test';

const [binary, out] = process.argv.slice(2, 4).map((p) => resolve(p));
const before = process.argv[4] === 'before';
const base = 'http://127.0.0.1:5394';
const dir = await mkdtemp(join(tmpdir(), 'ky-tasks-shots-'));
const env = {
  PATH: process.env.PATH, PUSH_RELAY_URL: '', APNS_RELAY_URL: '',
  KY_APP_URL: base, KY_HOST: '127.0.0.1', KY_PORT: '5394', KY_DB_DRIVER: 'sqlite',
  KY_DATA_DIR: join(dir, 'data'), KYCALENDAR_BACKUP_DIR: join(dir, 'backups'),
  KY_CAPTCHA_PROVIDER: 'none', KY_TRUSTED_PROXIES: '127.0.0.1', KYCALENDAR_SCHEDULING: 'false',
};
const run = (args, pw) => {
  if (spawnSync(binary, args, { cwd: dir, env, input: pw + '\n', stdio: ['pipe', 'inherit', 'inherit'] }).status !== 0) throw new Error(args.join(' '));
};
run(['init-admin'], 'ShotsInitial123!');
run(['create-user', '-username', 'walter'], 'ShotsInitial123!');
const server = spawn(binary, [], { cwd: dir, env, stdio: 'inherit' });
const pad = (n) => String(n).padStart(2, '0');
const day = (n) => { const d = new Date(); d.setDate(d.getDate() + n); return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`; };
const at = (n, h, m = 0) => { const d = new Date(); d.setDate(d.getDate() + n); d.setHours(h, m, 0, 0); return d.toISOString(); };
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
  const walter = await signIn('walter', '203.0.113.80');
  const json = async (r) => (await r).json();
  const list = async (name) => (await json(walter.call('POST', '/api/calendars', { name, components: ['VTODO'] }))).id;
  const add = async (cal, b) => (await json(walter.call('POST', `/api/calendars/${cal}/tasks`, { zone: 'UTC', ...b }))).uid;
  const work = await list('Website relaunch');
  const home = await list('Home');
  const health = await list('Health');
  if (!before) {
    const inbox = (await json(walter.call('POST', '/api/tasks/inbox'))).calendar_id;
    await walter.call('PUT', '/api/tasks/areas', [
      { id: 'work', name: 'Work', lists: [work] }, { id: 'personal', name: 'Personal', lists: [home] }, { id: 'health', name: 'Health', lists: [health] },
    ]);
    await add(work, { title: 'Compress the hero images', due: day(0), priority: 1, estimate_min: 15, tags: ['design'] });
    const launch = await add(work, { title: 'Write launch announcement', due: day(1), priority: 5, estimate_min: 45, tags: ['writing'], notes: 'Keep it under 300 words. Link the changelog.' });
    const outline = await add(work, { title: 'Draft outline', parent: launch });
    await add(work, { title: 'Get legal sign-off', parent: launch });
    await add(work, { title: 'Schedule post', parent: launch });
    await add(work, { title: 'Fix contact form validation', due: day(-2), priority: 1, tags: ['bug'] });
    await add(health, { title: 'Book dentist appointment', due: day(3), priority: 9 });
    await add(health, { title: 'Morning run', due: at(0, 7), rrule: 'FREQ=DAILY', estimate_min: 30 });
    await add(home, { title: 'Pay electricity bill', due: day(5), priority: 5, rrule: 'FREQ=MONTHLY' });
    await add(home, { title: 'Replace bathroom light bulb' });
    await add(inbox, { title: "Read 'Designing Data-Intensive Applications'", tags: ['reading'] });
    await add(inbox, { title: 'Renew passport', due: day(10), priority: 1 });
    await add(home, { title: 'Water the plants', due: day(2), rrule: 'FREQ=WEEKLY;INTERVAL=2' });
    const staging = await add(work, { title: 'Set up staging server' });
    for (const uid of [outline, staging]) {
      const t = (await json(walter.call('GET', `/api/tasks?status=completed,needs-action&tz=UTC`))).tasks.find((x) => x.uid === uid);
      await walter.fetch(`/api/tasks/${t.calendar_id}/${uid}?tz=UTC`, { method: 'PATCH', data: { status: 'COMPLETED' }, headers: { 'If-Match': `"${t.etag}"`, 'X-CSRF-Token': (await walter.storageState()).cookies.find((c) => c.name === 'ky_csrf')?.value } });
    }
  }

  await mkdir(out, { recursive: true });
  const browser = await chromium.launch();
  const shoot = async (page, name, width, scheme) => {
    await page.waitForTimeout(400);
    await page.screenshot({ path: join(out, `${name}-${width}-${scheme}.png`), fullPage: false });
  };
  for (const colorScheme of ['light', 'dark']) {
    for (const width of [1280, 390]) {
      const ctx = await browser.newContext({ baseURL: base, viewport: { width, height: 900 }, colorScheme, timezoneId: 'UTC', extraHTTPHeaders: { 'X-Forwarded-For': '203.0.113.81' } });
      const page = await ctx.newPage();
      await page.goto('/');
      await page.getByLabel('Username', { exact: true }).fill('walter');
      await page.locator('input[type=password]').fill('ShotsUpdated456!');
      await page.getByRole('button', { name: 'Sign in', exact: true }).click();
      await page.locator('.ky-content').waitFor();
      if (before) {
        await page.goto('/#/calendar');
        await page.locator('.fc').waitFor();
        if (width === 390) await page.getByText('Calendars', { exact: true }).click();
        await shoot(page, 'calendar-sidebar', width, colorScheme);
        await ctx.close();
        continue;
      }
      await page.goto('/#/tasks');
      const view = async (name) => {
        if (width === 390) await page.locator('.kc-picker > summary').click();
        await page.getByRole('navigation', { name: 'Task lists' }).getByRole('button', { name: new RegExp(`^${name}`) }).click();
        await page.getByRole('heading', { level: 1, name }).waitFor();
      };
      await view('Today');
      await page.getByText('Compress the hero images').waitFor();
      await shoot(page, 'today', width, colorScheme);
      await view('Upcoming');
      await shoot(page, 'upcoming', width, colorScheme);
      await view('Website relaunch');
      await shoot(page, 'list', width, colorScheme);
      await page.getByRole('button', { name: /Write launch announcement/ }).click();
      await page.getByRole('dialog').waitFor();
      await shoot(page, 'detail', width, colorScheme);
      await page.keyboard.press('Escape');
      await page.getByLabel('Quick add a task').fill('Call Ana tomorrow 3pm !high #home');
      await shoot(page, 'quickadd', width, colorScheme);
      await page.getByLabel('Quick add a task').fill('');
      await page.locator('body').click({ position: { x: 5, y: 890 } });
      await page.keyboard.press('n');
      await page.getByRole('dialog', { name: 'Add' }).waitFor();
      await page.getByLabel('Task', { exact: true }).fill('Plan the offsite next fri !medium @website');
      await shoot(page, 'capture', width, colorScheme);
      await ctx.close();
    }
  }
  await browser.close();
} finally {
  server.kill('SIGTERM');
  await new Promise((r) => server.once('exit', r));
  await rm(dir, { recursive: true, force: true });
}

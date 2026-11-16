// Captures the Tasks navigation and the screens it reaches (Today, a list, every view the
// sidebar used to list, Habits with one and three habits, Goals, Insights) at 1280px and 390px
// in light and dark, with fake data. Not a spec (CI skips it). Views are opened through the
// saved view key, so the same script captures the old and the new sidebar.
// Usage (from web/): node browser/tasks-nav-shots.mjs <server-binary> <out-dir> <before|after>
// Starts its own throwaway server on 127.0.0.1:5395 (relays empty) and removes its data after.
import { mkdtemp, mkdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { spawn, spawnSync } from 'node:child_process';
import { chromium, request } from '@playwright/test';

const [binary, out] = process.argv.slice(2, 4).map((p) => resolve(p));
const stage = process.argv[4] ?? 'after';
const base = 'http://127.0.0.1:5395';
const dir = await mkdtemp(join(tmpdir(), 'ky-tasks-nav-shots-'));
const env = {
  PATH: process.env.PATH, PUSH_RELAY_URL: '', APNS_RELAY_URL: '',
  KY_APP_URL: base, KY_HOST: '127.0.0.1', KY_PORT: '5395', KY_DB_DRIVER: 'sqlite',
  KY_DATA_DIR: join(dir, 'data'), KYCALENDAR_BACKUP_DIR: join(dir, 'backups'),
  KY_CAPTCHA_PROVIDER: 'none', KY_TRUSTED_PROXIES: '127.0.0.1', KYCALENDAR_SCHEDULING: 'false',
};
const cli = (args, pw) => {
  if (spawnSync(binary, args, { cwd: dir, env, input: pw + '\n', stdio: ['pipe', 'inherit', 'inherit'] }).status !== 0) throw new Error(args.join(' '));
};
cli(['init-admin'], 'ShotsInitial123!');
cli(['create-user', '-username', 'walter'], 'ShotsInitial123!');
const server = spawn(binary, [], { cwd: dir, env, stdio: 'inherit' });
const zone = Intl.DateTimeFormat().resolvedOptions().timeZone;
const pad = (n) => String(n).padStart(2, '0');
const day = (n) => { const d = new Date(); d.setDate(d.getDate() + n); return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`; };
const at = (n, h, m = 0) => { const d = new Date(); d.setDate(d.getDate() + n); d.setHours(h, m, 0, 0); return d.toISOString(); };
try {
  for (let i = 0; ; i++) {
    try { if ((await fetch(base)).ok) break; } catch { if (i > 100) throw new Error('server did not start'); }
    await new Promise((r) => setTimeout(r, 200));
  }
  const api = await request.newContext({ baseURL: base, extraHTTPHeaders: { 'X-Forwarded-For': '203.0.113.90' } });
  await api.get('/api/auth/me');
  const csrf = async () => (await api.storageState()).cookies.find((c) => c.name === 'ky_csrf')?.value;
  const ok = async (r) => { if (!r.ok()) throw new Error(`${r.status()} ${await r.text()}`); return r; };
  await ok(await api.post('/api/auth/login', { data: { username: 'walter', password: 'ShotsInitial123!' }, headers: { 'X-CSRF-Token': await csrf() } }));
  const call = async (method, path, data, headers = {}) => ok(await api.fetch(path, { method, data, headers: { 'X-CSRF-Token': await csrf(), ...headers } }));
  await call('POST', '/api/auth/change-password', { current_password: 'ShotsInitial123!', new_password: 'ShotsUpdated456!' });
  await api.get('/api/auth/me');
  await ok(await api.post('/api/auth/login', { data: { username: 'walter', password: 'ShotsUpdated456!' }, headers: { 'X-CSRF-Token': await csrf() } }));
  const json = async (r) => (await r).json();
  const list = async (name) => (await json(call('POST', '/api/calendars', { name, components: ['VTODO'] }))).id;
  const add = async (cal, b) => (await json(call('POST', `/api/calendars/${cal}/tasks`, { zone: 'UTC', ...b }))).uid;
  await call('GET', '/api/calendars');
  const work = await list('Website relaunch');
  const home = await list('Home');
  const health = await list('Health');
  const inbox = (await json(call('POST', '/api/tasks/inbox'))).calendar_id;
  await call('PUT', '/api/tasks/areas', [{ id: 'work', name: 'Work', lists: [work] }]);
  await add(work, { title: 'Compress the hero images', due: day(0), priority: 1, estimate_min: 15, tags: ['design'] });
  const launch = await add(work, { title: 'Write launch announcement', due: day(1), priority: 5, estimate_min: 45, tags: ['writing'] });
  await add(work, { title: 'Draft outline', parent: launch });
  await add(work, { title: 'Get legal sign-off', parent: launch });
  await add(work, { title: 'Fix contact form validation', due: day(-2), priority: 1, tags: ['bug'] });
  await add(health, { title: 'Book dentist appointment', due: day(3), priority: 9 });
  await add(health, { title: 'Morning run', due: at(0, 7), rrule: 'FREQ=DAILY', estimate_min: 30 });
  await add(health, { title: 'Stretch for ten minutes', due: at(0, 21), rrule: 'FREQ=DAILY', estimate_min: 15 });
  await add(home, { title: 'Weekly review', due: day(4), rrule: 'FREQ=WEEKLY' });
  await add(home, { title: 'Pay electricity bill', due: day(5), priority: 5, rrule: 'FREQ=MONTHLY' });
  await add(home, { title: 'Replace bathroom light bulb' });
  await add(inbox, { title: "Read 'Designing Data-Intensive Applications'", tags: ['reading'] });
  await add(inbox, { title: 'Renew passport', due: day(10), priority: 1 });
  await add(work, { title: 'Set up staging server' });
  const tasks = async () => (await json(call('GET', '/api/tasks?status=completed,needs-action&tz=UTC'))).tasks;
  const patch = async (title, data) => {
    const t = (await tasks()).find((x) => x.title === title);
    await call('PATCH', `/api/tasks/${t.calendar_id}/${t.uid}?tz=${zone}`, data, { 'If-Match': `"${t.etag}"` });
  };
  const cal = (await json(call('GET', '/api/calendars'))).find((c) => c.components.includes('VEVENT')).id;
  for (const [title, h, m, len] of [['Team standup', 9, 30, 30], ['Lunch with Maria', 12, 30, 45], ['Design review', 15, 0, 45]]) {
    await call('POST', `/api/calendars/${cal}/events`, { title, start: at(0, h, m), end: at(0, h, m + len), zone });
  }
  await patch('Write launch announcement', { start: at(0, 13, 30), duration_min: 45, zone });
  await patch('Fix contact form validation', { start: at(0, 10, 0), duration_min: 60, zone });
  for (const title of ['Draft outline', 'Set up staging server']) await patch(title, { status: 'COMPLETED' });
  // Completions straight into the throwaway database: the log records them only as they happen.
  const seed = (rows) => {
    const ins = `import sqlite3,sys,json; c=sqlite3.connect(sys.argv[1]); u=c.execute("SELECT id FROM users WHERE username='walter'").fetchone()[0]
for r in json.loads(sys.argv[2]): c.execute("INSERT INTO task_completions (user_id, calendar_id, uid, occurrence, completed_at) VALUES (?,?,?,?,?)", (u,*r))
c.commit()`;
    if (spawnSync('python3', ['-I', '-c', ins, join(dir, 'data', 'kycalendar.db'), JSON.stringify(rows)], { stdio: 'inherit' }).status !== 0) throw new Error('seed');
  };
  const now = Math.floor(Date.now() / 1000);
  const habit = async (title, every, keep) => {
    await patch(title, { habit: true });
    const t = (await tasks()).find((x) => x.title === title);
    const rows = [];
    for (let d = 1; d <= every; d++) if (keep(d)) rows.push([t.calendar_id, t.uid, `seed:${d}`, now - d * 86400]);
    seed(rows);
  };
  // Other completions spread over the weeks, for Insights.
  const extra = [];
  const staging = (await tasks()).find((x) => x.title === 'Set up staging server');
  for (let d = 3; d <= 50; d += 4) extra.push([staging.calendar_id, staging.uid, `x:${d}`, now - d * 86400]);
  seed(extra);
  await habit('Morning run', 40, (d) => d % 6 !== 0 && d !== 2);
  await call('POST', '/api/tasks/goals', { title: 'Launch the new website', period: 'quarter', starts: day(-30), ends: day(60), areas: ['work'], lists: [] });

  await mkdir(out, { recursive: true });
  const browser = await chromium.launch();
  const shoot = async (page, name, width, scheme) => {
    await page.mouse.move(2, 2);
    await page.waitForTimeout(500);
    await page.screenshot({ path: join(out, `${stage}-${name}-${width}-${scheme}.png`) });
  };
  const capture = async (names) => {
    for (const colorScheme of ['light', 'dark']) {
      for (const width of [1280, 390]) {
        const ctx = await browser.newContext({ baseURL: base, viewport: { width, height: 800 }, colorScheme, timezoneId: zone, extraHTTPHeaders: { 'X-Forwarded-For': '203.0.113.91' } });
        const page = await ctx.newPage();
        const noon = new Date(); noon.setHours(13, 24, 0, 0);
        await page.clock.setFixedTime(noon);
        await page.goto('/');
        await page.getByLabel('Username', { exact: true }).fill('walter');
        await page.locator('input[type=password]').fill('ShotsUpdated456!');
        await page.getByRole('button', { name: 'Sign in', exact: true }).click();
        await page.locator('.ky-content').waitFor();
        for (const [name, view, card] of names) {
          await page.evaluate((v) => localStorage.setItem('kycalendar.tasksView', JSON.stringify(v)), view);
          await page.goto('/#/tasks');
          await page.reload();
          await page.locator('.tk-head h1').waitFor();
          await page.waitForTimeout(700);
          await page.evaluate(() => { const s = document.querySelector('.app-sidebar-slot'); if (s) s.scrollTop = 0; });
          if (card) await page.locator('.th-card', { hasText: card }).evaluate((el) => el.scrollIntoView({ block: 'start' }));
          await shoot(page, name, width, colorScheme);
          if (name === 'today' && width === 390) {
            await page.locator('.kc-picker > summary').click();
            await shoot(page, 'nav-open', width, colorScheme);
          }
        }
        await ctx.close();
      }
    }
  };
  await capture([['habits-1', { kind: 'smart', id: 'habits' }]]);
  await habit('Stretch for ten minutes', 20, (d) => d % 3 !== 0);
  await habit('Weekly review', 70, (d) => d % 7 === 4 && d !== 25);
  await capture([
    ['today', { kind: 'smart', id: 'today' }], ['list', { kind: 'list', id: work }], ['upcoming', { kind: 'smart', id: 'upcoming' }],
    ['overdue', { kind: 'smart', id: 'overdue' }], ['someday', { kind: 'smart', id: 'someday' }], ['flagged', { kind: 'smart', id: 'flagged' }],
    ['completed', { kind: 'smart', id: 'completed' }], ['habits-3', { kind: 'smart', id: 'habits' }], ['habits-weekly', { kind: 'smart', id: 'habits' }, 'Weekly review'], ['goals', { kind: 'smart', id: 'goals' }],
    ['insights', { kind: 'smart', id: 'insights' }],
  ]);
  await browser.close();
} finally {
  server.kill('SIGTERM');
  await new Promise((r) => server.once('exit', r));
  await rm(dir, { recursive: true, force: true });
}

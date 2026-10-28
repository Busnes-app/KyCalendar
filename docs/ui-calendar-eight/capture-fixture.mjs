// Real-server fixture, adapted from web/browser/{server,setup}.mjs and the fleet capture driver.
// Usage: node capture-fixture.mjs init|start PRIVATE_DIRECTORY
// Build once beforehand, with the authorized capped Go command, to PRIVATE_DIRECTORY/server.
import { chmod, readFile, writeFile, mkdir, open } from 'node:fs/promises';
import { spawn, spawnSync, execFileSync } from 'node:child_process';
import { randomBytes, createHash } from 'node:crypto';
import { createServer } from 'node:net';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import assert from 'node:assert/strict';

process.umask(0o077);
const root = resolve(fileURLToPath(new URL('../..', import.meta.url)));
const [mode, dirArg] = process.argv.slice(2);
assert(['init', 'start'].includes(mode) && dirArg, 'usage: capture-fixture.mjs init|start PRIVATE_DIRECTORY');
const dir = resolve(dirArg);
assert(!dir.startsWith(root + '/'), 'fixture must be outside git');
await chmod(dir, 0o700);
const pwModule = process.env.CAPTURE_PLAYWRIGHT ?? '/home/yoshi/git/busnes.app/KyCalendar/web/node_modules/@playwright/test/index.mjs';
const { request } = await import(pwModule);
const sha = (b) => createHash('sha256').update(b).digest('hex');
const json = async (file, data) => writeFile(file, JSON.stringify(data, null, 2) + '\n', { mode: 0o600 });
const secret = () => `Capture-${randomBytes(24).toString('base64url')}9!`;
let config;
if (mode === 'init') {
  const port = await new Promise((res, rej) => {
    const s = createServer(); s.on('error', rej); s.listen(0, '127.0.0.1', () => { const p = s.address().port; s.close(() => res(p)); });
  });
  assert(port !== 5391);
  config = { root, port, origin: `http://127.0.0.1:${port}`, binary: join(dir, 'server'), display_date: '2026-10-27', timezone: 'America/New_York', locale: 'en-US' };
  await json(join(dir, 'fixture.json'), config);
  await json(join(dir, 'credentials.json'), { adminInit: secret(), admin: secret(), personInit: secret(), person: secret() });
} else config = JSON.parse(await readFile(join(dir, 'fixture.json'), 'utf8'));
const env = { PATH: process.env.PATH, GOMAXPROCS: '2', TZ: config.timezone, KY_APP_URL: config.origin,
  KY_HOST: '127.0.0.1', KY_PORT: String(config.port), KY_DB_DRIVER: 'sqlite', KY_DATA_DIR: join(dir, 'data'),
  KYCALENDAR_BACKUP_DIR: join(dir, 'backups'), KY_CAPTCHA_PROVIDER: 'none', KY_TRUSTED_PROXIES: '127.0.0.1' };
const passwords = JSON.parse(await readFile(join(dir, 'credentials.json'), 'utf8'));
if (mode === 'init') {
  for (const [args, value] of [[['init-admin'], passwords.adminInit], [['create-user', '-username', 'casey'], passwords.personInit]]) {
    const r = spawnSync(config.binary, args, { cwd: dir, env, input: value + '\n', encoding: 'utf8' });
    await writeFile(join(dir, args[0] + '.log'), r.stdout + r.stderr, { mode: 0o600 });
    assert.equal(r.status, 0, args[0] + ' failed; inspect private log');
  }
}
const log = await open(join(dir, 'server.log'), 'a', 0o600);
const child = spawn(config.binary, [], { cwd: dir, env, detached: true, stdio: ['ignore', log.fd, log.fd] });
child.unref(); await log.close();
const proc = await readFile(`/proc/${child.pid}/stat`, 'utf8');
const startticks = proc.slice(proc.lastIndexOf(')') + 2).split(' ')[19];
const processRecord = { pid: child.pid, startticks, executable: config.binary, sha256: sha(await readFile(config.binary)),
  started_at: new Date().toISOString(), build_info: execFileSync('go', ['version', '-m', config.binary], { encoding: 'utf8' }),
  build_command: 'CGO_ENABLED=0 GOMAXPROCS=2 go build -p 1 -trimpath -o PRIVATE_DIRECTORY/server ./cmd/server',
  tracked_status_at_start: execFileSync('git', ['status', '--short', '--untracked-files=no'], { cwd: root, encoding: 'utf8' }) };
await json(join(dir, 'process.json'), processRecord);
await writeFile(join(dir, 'stop.sh'), `#!/bin/sh\nset -eu\npython3 - '${dir}' <<'PY'\nimport sys,json,os,signal,hashlib\nfrom pathlib import Path\nd=Path(sys.argv[1]); p=json.loads((d/'process.json').read_text()); q=Path('/proc')/str(p['pid'])\nif not q.exists():\n print('already stopped'); sys.exit(0)\ns=(q/'stat').read_text().rsplit(')',1)[1].split()[19]\nassert s==p['startticks'], 'PID reused: refused'\nassert os.readlink(q/'exe')==p['executable'], 'executable changed: refused'\nassert hashlib.sha256(Path(p['executable']).read_bytes()).hexdigest()==p['sha256'], 'binary changed: refused'\nos.kill(p['pid'],signal.SIGTERM)\nprint('owned server stop requested')\nPY\n`, { mode: 0o700 });
for (let i = 0; i < 100; i++) {
  try { if ((await fetch(config.origin + '/healthz')).ok) break; } catch { /* bounded startup */ }
  if (i === 99) throw new Error('server did not become ready; inspect private server.log');
  await new Promise((r) => setTimeout(r, 200));
}
async function session(username, password) {
  const api = await request.newContext({ baseURL: config.origin, extraHTTPHeaders: { 'X-Forwarded-For': '198.51.100.240' } });
  await api.get('/api/auth/me');
  let csrf = (await api.storageState()).cookies.find((c) => c.name === 'ky_csrf')?.value;
  const r = await api.post('/api/auth/login', { headers: { 'X-CSRF-Token': csrf }, data: { username, password } });
  assert(r.ok(), username + ' fixture login failed: ' + r.status());
  csrf = (await api.storageState()).cookies.find((c) => c.name === 'ky_csrf')?.value;
  assert(csrf);
  return { api, headers: { 'X-CSRF-Token': csrf }, state: await r.json() };
}
async function ok(response, label) {
  assert(response.ok(), label + ': HTTP ' + response.status());
  return response.status() === 204 ? null : response.json();
}
if (mode === 'init') {
  const auth = [];
  for (const [username, before, after] of [['admin', passwords.adminInit, passwords.admin], ['casey', passwords.personInit, passwords.person]]) {
    const { api, headers, state } = await session(username, before);
    assert(state.must_change_password ?? state.user?.must_change_password);
    const r = await api.post('/api/auth/change-password', { headers, data: { current_password: before, new_password: after } });
    assert(r.ok(), 'password replacement failed: ' + r.status());
    auth.push({ username, forced_change_verified: true, replaced_via_real_auth_csrf: true });
    await api.dispose();
  }
  const { api, headers } = await session('casey', passwords.person);
  const calendars = await ok(await api.get('/api/calendars'), 'calendar list');
  const work = await ok(await api.patch('/api/calendars/' + calendars[0].id, { headers, data: { name: 'Work', color: '#476da5' } }), 'rename Work');
  const personal = await ok(await api.post('/api/calendars', { headers, data: { name: 'Personal', color: '#3f7f6e' } }), 'create Personal');
  const projects = await ok(await api.post('/api/calendars', { headers, data: { name: 'Projects', color: '#985c87' } }), 'create Projects');
  const timed = (cal, title, date, start, end, extra = {}) => ({ cal, body: { title, location: '', description: '', all_day: false,
    zone: config.timezone, repeat: { freq: '' }, start: `${date}T${start}:00-04:00`, end: `${date}T${end}:00-04:00`, ...extra } });
  const events = [
    timed(work.id, 'Team check-in', '2026-10-26', '09:00', '09:20'),
    timed(work.id, 'Design review', '2026-10-27', '10:00', '11:00', { location: 'Meeting room', description: 'Review the next release checklist and agree on the handover.' }),
    timed(projects.id, 'Planning the community workshop and preparing the shared materials', '2026-10-27', '13:00', '14:30'),
    timed(personal.id, 'Lunch break', '2026-10-27', '12:00', '12:45'),
    timed(work.id, 'Supplier call', '2026-10-28', '14:00', '14:45'),
    timed(personal.id, 'Morning appointment', '2026-10-29', '08:30', '09:15'),
    timed(projects.id, 'Project handover', '2026-10-29', '15:00', '16:00'),
    timed(work.id, 'Weekly wrap-up', '2026-10-30', '15:30', '16:15'),
    timed(personal.id, 'Weekend walk', '2026-10-31', '09:00', '10:30'),
    { cal: work.id, body: { title: 'Release preparation', all_day: true, start: '2026-10-28', end: '2026-10-30', repeat: { freq: '' } } },
    { cal: personal.id, body: { title: 'Library day', all_day: true, start: '2026-10-27', end: '2026-10-28', repeat: { freq: '' } } },
  ];
  for (const e of events) e.result = await ok(await api.post(`/api/calendars/${e.cal}/events`, { headers, data: e.body }), 'seed event');
  assert.equal((await api.get('/api/admin/users')).status(), 403, 'everyday must be denied People');
  await api.dispose();
  const admin = await session('admin', passwords.admin);
  assert.equal((await admin.api.get('/api/calendars')).status(), 403, 'admin must be denied everyday calendars');
  const group = await ok(await admin.api.post('/api/admin/calendars', { headers: admin.headers, data: { name: 'Shared planning', color: '#8c6b37', description: 'Synthetic organisation calendar; no grants.' } }), 'group calendar');
  await admin.api.dispose();
  await json(join(dir, 'seed.json'), { seeded_once_at: new Date().toISOString(), auth, calendars: [work, personal, projects], events, group_calendar: group,
    role_isolation: { everyday_people_status: 403, admin_calendar_status: 403 }, display_date: config.display_date });
  // Bootstrap values are no longer needed. Only current disposable credentials remain private.
  await json(join(dir, 'credentials.json'), { admin: passwords.admin, person: passwords.person });
}
console.log(JSON.stringify({ fixture: dir, origin: config.origin, pid: child.pid, startticks, sha256: processRecord.sha256, seeded: mode === 'init' }));

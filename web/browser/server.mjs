import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawn, spawnSync } from 'node:child_process';

// Always own disposable data. Never reuse a running development/production server.
const dir = await mkdtemp(join(tmpdir(), 'ky-browser-'));
const binary = fileURLToPath(new URL('../../.browser/server', import.meta.url));
const env = {
  PATH: process.env.PATH,
  KY_APP_URL: 'http://127.0.0.1:5391',
  KY_HOST: '127.0.0.1', KY_PORT: '5391', KY_DB_DRIVER: 'sqlite',
  KY_DATA_DIR: join(dir, 'data'), KYCALENDAR_BACKUP_DIR: join(dir, 'backups'),
  KY_CAPTCHA_PROVIDER: 'none',
};
// The server only bootstraps its admin into an empty database, so create the admin first.
const admin = spawnSync(binary, ['init-admin', '-password', 'BrowserInitial123!'], { cwd: dir, env, stdio: 'inherit' });
if (admin.status !== 0) { await rm(dir, { recursive: true, force: true }); process.exit(admin.status ?? 1); }
// The everyday user the calendar specs sign in as.
const created = spawnSync(binary, ['create-user', '-username', 'walter'], { cwd: dir, env, input: 'WalterInitial123!\n', stdio: ['pipe', 'inherit', 'inherit'] });
if (created.status !== 0) { await rm(dir, { recursive: true, force: true }); process.exit(created.status ?? 1); }
const server = spawn(binary, [], { cwd: dir, env, stdio: 'inherit' });
for (const signal of ['SIGTERM', 'SIGINT']) process.on(signal, () => server.kill('SIGTERM'));
server.on('error', async error => { console.error(error.message); await rm(dir, { recursive: true, force: true }); process.exit(1); });
server.on('exit', async code => { await rm(dir, { recursive: true, force: true }); process.exit(code ?? 1); });

// Owner-requested phone Month restoration: two matched originals, no review scores.
import { readFile, writeFile } from 'node:fs/promises';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { resolve, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import assert from 'node:assert/strict';
import { fixtureOrigin } from './fixture-network.mjs';

process.umask(0o077);
const root = resolve(fileURLToPath(new URL('../..', import.meta.url)));
const out = resolve(fileURLToPath(new URL('.', import.meta.url)));
const dir = resolve(process.argv[2]);
const config = JSON.parse(await readFile(join(dir, 'fixture.json'), 'utf8'));
const origin = fixtureOrigin(config.origin);
assert.equal(Number(new URL(origin).port), config.port);
const credentials = JSON.parse(await readFile(join(dir, 'credentials.json'), 'utf8'));
const processRecord = JSON.parse(await readFile(join(dir, 'process.json'), 'utf8'));
const sha = b => createHash('sha256').update(b).digest('hex');
const git = (...args) => execFileSync('git', args, { cwd: root, encoding: 'utf8' });
assert.equal(git('diff', 'HEAD', '--', 'web/src', 'web/dist'), '');
const paths = git('ls-files', '-z', '--', 'web/src/styles/calendar.css', 'web/dist').split('\0').filter(Boolean);
const sourceFiles = await Promise.all(paths.map(async file => ({ file, sha256: sha(await readFile(join(root, file))) })));
const manifest = { stage: '05-after', purpose: 'Owner rejected round3 mobile Month; restore single-line labels',
  started_at: new Date().toISOString(), source: git('rev-parse', 'HEAD').trim(), source_files: sourceFiles,
  process: processRecord, seed_sha256: sha(await readFile(join(dir, 'seed.json'))), files: [], assets: [], failures: [],
  conditions: { viewport: [390, 844], dpr: 1, locale: 'en-US', timezone: config.timezone, saved_theme: 'none',
    display_date: config.display_date, clock: 'real; no mocks', pointer: [389, 843], service_workers: 'allowed' } };
const manifestPath = join(out, 'manifest-05-after.json');
await writeFile(manifestPath, JSON.stringify(manifest, null, 2) + '\n', { flag: 'wx' });
const { chromium } = await import('/home/yoshi/git/busnes.app/KyCalendar/web/node_modules/@playwright/test/index.mjs');
const browser = await chromium.launch({ args: ['--renderer-process-limit=2'] });
manifest.browser = browser.version();
const settle = async page => {
  await page.evaluate(async () => { await document.fonts.ready; await new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))); });
  await page.waitForFunction(() => !document.querySelector('.kc-workspace-heading [role=status]')?.textContent?.includes('Loading'));
  await page.waitForTimeout(400);
};
try {
  for (const scheme of ['light', 'dark']) {
    const context = await browser.newContext({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 1,
      colorScheme: scheme, locale: 'en-US', timezoneId: config.timezone, serviceWorkers: 'allow', extraHTTPHeaders: { 'X-Forwarded-For': scheme === 'light' ? '198.51.100.201' : '198.51.100.202' } });
    try {
      const page = await context.newPage();
      const bodies = [];
      page.on('pageerror', error => manifest.failures.push({ scheme, error: error.message }));
      page.on('response', response => {
        const url = new URL(response.url());
        if (url.origin === origin && response.ok() && /\.(js|css)$/.test(url.pathname)) bodies.push((async () => {
          const bytes = await response.body(), file = 'web/dist' + url.pathname;
          const disk = await readFile(join(root, file));
          manifest.assets.push({ scheme, file, sha256: sha(bytes), matches_disk: sha(bytes) === sha(disk) });
        })());
      });
      await page.goto(origin + '/');
      await page.getByLabel('Username', { exact: true }).fill('casey');
      await page.getByLabel('Password', { exact: true }).fill(credentials.person);
      await page.getByRole('button', { name: 'Sign in', exact: true }).click();
      await page.locator('.fc').waitFor();
      await page.locator('.fc-timeGridDay-button').click(); await settle(page);
      const date = await page.locator('.fc-timegrid-col[data-date]').first().getAttribute('data-date');
      const delta = Math.round((Date.parse(config.display_date) - Date.parse(date)) / 86400000);
      assert(Math.abs(delta) <= 400);
      for (let i = 0; i < Math.abs(delta); i++) await page.locator(delta < 0 ? '.fc-prev-button' : '.fc-next-button').click();
      await page.locator('.fc-dayGridMonth-button').click(); await settle(page);
      await page.evaluate(() => scrollTo(0, 0)); await page.mouse.move(389, 843);
      const geometry = await page.evaluate(() => ({ overflow: document.documentElement.scrollWidth - innerWidth,
        saved_theme: localStorage.getItem('ky_theme'), theme: document.documentElement.dataset.theme,
        heading: document.querySelector('.fc-toolbar-title').textContent, real_time: new Date().toISOString(),
        titles: [...document.querySelectorAll('.fc-daygrid-event .fc-event-title')].map(el => ({ text: el.textContent,
          white_space: getComputedStyle(el).whiteSpace, height: el.getBoundingClientRect().height, line_height: getComputedStyle(el).lineHeight })) }));
      assert.equal(geometry.overflow, 0); assert.equal(geometry.saved_theme, null);
      assert(geometry.titles.length > 0 && geometry.titles.every(t => t.white_space === 'nowrap'));
      const bytes = await page.screenshot({ fullPage: false });
      const file = `screenshots/kycalendar-05-after-month-mobile-${scheme}.png`;
      await writeFile(join(out, file), bytes, { flag: 'wx' });
      manifest.files.push({ file, sha256: sha(bytes), bytes: bytes.length, width: 390, height: 844, scheme,
        captured_at: new Date().toISOString(), geometry });
      await Promise.all(bodies);
    } finally { await context.close(); }
  }
  assert(manifest.assets.every(asset => asset.matches_disk));
} finally {
  await browser.close(); manifest.browser_closed = true; manifest.finished_at = new Date().toISOString();
  await writeFile(manifestPath, JSON.stringify(manifest, null, 2) + '\n');
}
console.log(JSON.stringify({ images: manifest.files.length, failures: manifest.failures.length, source: manifest.source, browser_closed: manifest.browser_closed }));

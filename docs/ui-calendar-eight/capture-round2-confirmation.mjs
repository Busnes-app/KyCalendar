// Exact DPR1 workflow: real embedded server and real forms; no routes, workers or clocks mocked.
// Adapted from web/browser/*.mjs and docs/ui-fleet-2026-10-26/drivers/capture-base.mjs.txt.
// Usage: node docs/ui-calendar-eight/capture.mjs PRIVATE_DIRECTORY [00-before|01-after]
// CAPTURE_PLAYWRIGHT can select an already installed @playwright/test/index.mjs.
import { readFile, writeFile, mkdir } from 'node:fs/promises';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { resolve, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import assert from 'node:assert/strict';

process.umask(0o077);
const root = resolve(fileURLToPath(new URL('../..', import.meta.url)));
const out = resolve(fileURLToPath(new URL('.', import.meta.url)));
const [dirArg, stage = '02-confirmation'] = process.argv.slice(2);
assert(dirArg && /^\d\d-(before|after|confirmation)$/.test(stage), 'usage: capture.mjs PRIVATE_DIRECTORY STAGE');
const dir = resolve(dirArg);
const config = JSON.parse(await readFile(join(dir, 'fixture.json'), 'utf8'));
const credentials = JSON.parse(await readFile(join(dir, 'credentials.json'), 'utf8'));
const seed = JSON.parse(await readFile(join(dir, 'seed.json'), 'utf8'));
const processRecord = JSON.parse(await readFile(join(dir, 'process.json'), 'utf8'));
assert.equal(new URL(config.origin).hostname, '127.0.0.1');
assert.equal(stage,'02-confirmation');
const pwModule = process.env.CAPTURE_PLAYWRIGHT ?? '/home/yoshi/git/busnes.app/KyCalendar/web/node_modules/@playwright/test/index.mjs';
const { chromium } = await import(pwModule);
const sha = (b) => createHash('sha256').update(b).digest('hex');
const git = (...args) => execFileSync('git', args, { cwd: root, encoding: 'utf8' });
const save = (name, value) => writeFile(join(out, name), JSON.stringify(value, null, 2) + '\n');
const runtimePaths = ['cmd', 'internal', 'third_party', 'web/src', 'web/public', 'web/dist', 'web/embed.go', 'web/package.json', 'web/package-lock.json', 'go.mod', 'go.sum'];
async function sourceBinding() {
  const files = git('ls-files', '-z', '--', ...runtimePaths).split('\0').filter(Boolean);
  const hashes = [];
  for (const file of files) hashes.push({ file, sha256: sha(await readFile(join(root, file))) });
  const diff = git('diff', 'HEAD', '--', ...runtimePaths);
  assert.equal(diff, '', 'tracked runtime differs from HEAD');
  assert.equal(git('ls-files', '--others', '--exclude-standard', '--', ...runtimePaths), '', 'untracked runtime');
  return { head: git('rev-parse', 'HEAD').trim(), branch: git('branch', '--show-current').trim(), runtime_sha256: sha(JSON.stringify(hashes)), files: hashes,
    tracked_status: git('status', '--short', '--untracked-files=no'), all_status: git('status', '--short'), runtime_matches_head: true };
}
const before = await sourceBinding();
for (const entry of JSON.parse(await readFile(join(out, 'authority.json'), 'utf8')).files) {
  const b = await readFile(join(out, entry.file)); assert.equal(sha(b), entry.sha256); assert.equal(b.length, entry.bytes);
}
const manifest = { schema: 'kycalendar-capture-manifest/1', stage, started_at: new Date().toISOString(), product: 'KyCalendar', source_before: before,
  binary: processRecord, fixture: { path: dir, origin: config.origin, port: config.port, display_date: config.display_date, timezone: config.timezone,
    seed_sha256: sha(await readFile(join(dir, 'seed.json'))), personal_calendars: seed.calendars.map(({ id, name, color }) => ({ id, name, color })),
    events: seed.events.map((e) => ({ calendar_id: e.cal, ...e.body })), group_calendar: { id: seed.group_calendar.id, name: seed.group_calendar.name },
    role_isolation: seed.role_isolation, auth_setup: seed.auth },
  conditions: { desktop: [1280, 900], mobile: [390, 844], dpr: 1, locale: 'en-US', timezone: config.timezone, os_schemes: ['light', 'dark'],
    saved_theme: 'none', full_page: false, cropping_or_resizing: false, clock: 'real; no emulation', display_date_method: 'Day view and bounded Previous/Next controls, then requested view',
    workers: 'allowed; real production registration', font_wait: 'document.fonts.ready and two animation frames, stable UI + 400ms', pointer: 'neutral bottom-right pixel',
    workflow:'One permitted four-image Appearance correction; original capture.mjs interaction sequence, supplemental probes after screenshots',
    workflow_references: ['web/browser/AGENTS.md', 'web/browser/server.mjs', 'web/browser/setup.mjs', 'docs/ui-fleet-2026-10-26/drivers/capture-base.mjs.txt'],
    fleet_driver_differences: ['No fixed clock', 'America/New_York', 'DPR1 explicit', 'Service workers allowed', 'Fixture retained', 'No credentials or filled setup captured'] },
  native_preview: {opened:false,reason:'One bounded correction using documented capture workflow; first Chromium already closed'},
  supplemental_probes: [],
  correction: {supersedes:'01-after Appearance only, four conditions',reason:'Navigation focus reachability probe contaminated Appearance focus modality; raw originals preserved'},
  files: [], failures: [], navigation: [], served_assets: [], keyboard_probes: [] };
await mkdir(join(out, 'screenshots'), { recursive: true });
// Never overwrite originals or restart an unsuccessful batch silently.
await writeFile(join(out, `manifest-${stage}.json`), JSON.stringify(manifest, null, 2) + '\n', { flag: 'wx' });
const checkpoint = () => save(`manifest-${stage}.json`, manifest);
const browser = await chromium.launch({ args: ['--renderer-process-limit=2'] });
manifest.browser = { version: browser.version(), engine: 'Chromium', executable: chromium.executablePath(), executable_sha256: sha(await readFile(chromium.executablePath())),
  playwright_module: pwModule, playwright_version: JSON.parse(await readFile(new URL('./package.json', 'file://' + pwModule), 'utf8')).version };
const responses = [];
const assets = new Map();
let contextSerial = 0;
async function settle(page) {
  await page.evaluate(async () => { await document.fonts.ready; await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))); });
  if (await page.locator('.kc-workspace-heading [role=status]').count()) await page.waitForFunction(() => !document.querySelector('.kc-workspace-heading [role=status]')?.textContent?.includes('Loading'));
  await page.waitForTimeout(400);
}
async function fresh(scheme, viewport, user) {
  const context = await browser.newContext({ viewport, deviceScaleFactor: 1, colorScheme: scheme, locale: 'en-US', timezoneId: config.timezone,
    serviceWorkers: 'allow', extraHTTPHeaders: { 'X-Forwarded-For': `198.51.100.${++contextSerial}` } });
  const page = await context.newPage();
  page.setDefaultTimeout(12000);
  const diagnostics = { page_errors: [], console_errors: [], csp: [], request_failures: [], http_errors: [], request_counts: {}, external_requests: [], document_csp: null };
  page.on('pageerror', (e) => diagnostics.page_errors.push(e.message));
  page.on('console', (m) => { if (m.type() === 'error') diagnostics.console_errors.push(m.text()); if (/Content Security Policy|violates.*directive/i.test(m.text())) diagnostics.csp.push(m.text()); });
  page.on('request', (r) => { const u = new URL(r.url()); diagnostics.request_counts[u.pathname] = (diagnostics.request_counts[u.pathname] ?? 0) + 1; if (u.protocol.startsWith('http') && u.origin !== config.origin) diagnostics.external_requests.push(u.origin + u.pathname); });
  page.on('requestfailed', (r) => diagnostics.request_failures.push({ path: new URL(r.url()).pathname, error: r.failure()?.errorText }));
  page.on('response', (r) => {
    const u = new URL(r.url());
    if (r.status() >= 400) diagnostics.http_errors.push({ path: u.pathname, status: r.status() });
    if (r.request().resourceType() === 'document') diagnostics.document_csp = r.headers()['content-security-policy'] ?? null;
    if (u.origin === config.origin && r.ok() && (u.pathname === '/' || /\.(js|css|woff2?|png|svg|webmanifest)$/.test(u.pathname))) {
      responses.push((async () => { try {
        const bytes = await r.body(); const file = u.pathname === '/' ? 'web/dist/index.html' : 'web/dist' + u.pathname;
        let disk = null; try { disk = sha(await readFile(join(root, file))); } catch { /* non-dist route remains recorded */ }
        assets.set(u.pathname, { path: u.pathname, sha256: sha(bytes), bytes: bytes.length, from_service_worker: r.fromServiceWorker(), file, disk_sha256: disk, matches_disk: disk === sha(bytes) });
      } catch (e) { manifest.failures.push({ step: 'served asset body', path: u.pathname, error: e.message }); } })());
    }
  });
  await page.goto(config.origin + '/');
  await page.getByLabel('Username', { exact: true }).waitFor();
  if (user) {
    await page.getByLabel('Username', { exact: true }).fill(user === 'admin' ? 'admin' : 'casey');
    await page.getByLabel('Password', { exact: true }).fill(user === 'admin' ? credentials.admin : credentials.person);
    await page.getByRole('button', { name: 'Sign in', exact: true }).click();
    await page.getByRole('navigation', { name: 'Primary' }).waitFor();
    if (user === 'person') await page.locator('.fc').waitFor();
  }
  await settle(page);
  return { context, page, diagnostics };
}
async function nav(page, label) {
  await page.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name: label, exact: true }).click();
  await settle(page);
}
async function setDate(page) {
  await page.locator('.fc-timeGridDay-button').click(); await settle(page);
  let current = await page.locator('.fc-timegrid-col[data-date]').first().getAttribute('data-date');
  const delta = Math.round((Date.parse(config.display_date) - Date.parse(current)) / 86400000);
  assert(Math.abs(delta) <= 400, 'display date is too far away for bounded calendar controls');
  for (let i = 0; i < Math.abs(delta); i++) await page.locator(delta < 0 ? '.fc-prev-button' : '.fc-next-button').click();
  await settle(page);
  current = await page.locator('.fc-timegrid-col[data-date]').first().getAttribute('data-date');
  assert.equal(current, config.display_date);
  manifest.navigation.push({ action: 'Day then calendar previous/next', delta_days: delta, result: current });
}
async function shot(page, diagnostics, surface, size, scheme, role, extra = {}) {
  if(surface!=='appearance') return;
  await settle(page);
  await page.mouse.move(size === 'desktop' ? 1279 : 389, size === 'desktop' ? 899 : 843);
  const evidence = await page.evaluate(() => {
    const visible = (e) => { const r = e.getBoundingClientRect(), s = getComputedStyle(e); return r.width > 0 && r.height > 0 && s.visibility !== 'hidden' && s.display !== 'none' && r.bottom > 0 && r.top < innerHeight && r.right > 0 && r.left < innerWidth; };
    const controls = [...document.querySelectorAll('button,input,select,textarea,summary,a[href]')].filter(visible).map((e) => ({
      tag: e.tagName.toLowerCase(), type: e.getAttribute('type'), name: e.getAttribute('aria-label') || e.labels?.[0]?.innerText || e.innerText || e.getAttribute('title') || '',
      disabled: !!e.disabled, width: e.getBoundingClientRect().width, height: e.getBoundingClientRect().height, font: getComputedStyle(e).fontFamily }));
    const usedFonts = [...new Set([...document.querySelectorAll('body *')].filter(visible).map((e) => getComputedStyle(e).fontFamily))];
    return { viewport: { width: innerWidth, height: innerHeight, dpr: devicePixelRatio }, locale: navigator.language, timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
      saved_theme: localStorage.getItem('ky_theme'), data_theme: document.documentElement.dataset.theme ?? null, os_dark: matchMedia('(prefers-color-scheme:dark)').matches,
      overflow_px: Math.max(0, document.documentElement.scrollWidth - innerWidth), scroll: { x: scrollX, y: scrollY }, title: document.title,
      h1: [...document.querySelectorAll('h1')].map((e) => e.textContent), dialog: document.querySelector('dialog[open] h2')?.textContent ?? null,
      calendar_view: document.querySelector('.fc-view')?.className ?? null, calendar_heading: document.querySelector('.fc-toolbar-title')?.textContent ?? null,
      calendar_dates: [...new Set([...document.querySelectorAll('.fc-view [data-date]')].map((e) => e.getAttribute('data-date')))].sort(),
      event_titles_in_dom: [...document.querySelectorAll('.fc-event-title,.fc-list-event-title')].map((e) => e.textContent),
      left_regions: { app_header_count: document.querySelectorAll('.app-header').length, primary_nav_count: document.querySelectorAll('nav[aria-label=Primary]').length,
        calendar_complementary_count: document.querySelectorAll('aside[aria-label=Calendars]').length, calendar_inside_header: [...document.querySelectorAll('aside[aria-label=Calendars]')].every((e) => !!e.closest('.app-header')),
        visible_calendar_complementary_count: [...document.querySelectorAll('aside[aria-label=Calendars]')].filter(visible).length,
        desktop_left_navigation_regions: innerWidth > 720 ? [...document.querySelectorAll('.app-header')].filter(visible).length : 0 },
      visible_actions_forms: controls, forms_in_dom: document.forms.length,
      credential_inputs_filled: [...document.querySelectorAll('input[type=password],input[autocomplete=username]')].some((e) => !!e.value),
      font_probes: { fonts_status: document.fonts.status, space_grotesk_15: document.fonts.check('15px "Space Grotesk"'), ibm_plex_mono_13: document.fonts.check('13px "IBM Plex Mono"'),
        faces: [...document.fonts].map((f) => ({ family: f.family, weight: f.weight, status: f.status })), used_font_stacks: usedFonts },
      service_worker_controlled: !!navigator.serviceWorker.controller, real_browser_time: new Date().toISOString() };
  });
  assert.equal(evidence.saved_theme, null, 'unexpected saved theme');
  assert.equal(evidence.viewport.dpr, 1); assert.equal(evidence.timezone, config.timezone); assert.equal(evidence.locale, 'en-US');
  assert.equal(evidence.scroll.y, 0, 'initial viewport required'); assert.equal(evidence.credential_inputs_filled, false);
  assert.equal(diagnostics.external_requests.length, 0, 'external request observed');
  const file = `screenshots/kycalendar-${stage}-${surface}-${size}-${scheme}.png`;
  // Preserve bytes of every original. refuse an accidental rerun of the same name.
  const image = await page.screenshot({ fullPage: false });
  await writeFile(join(out, file), image, { flag: 'wx' });
  const row = { file, sha256: sha(image), bytes: image.length, width: image.readUInt32BE(16), height: image.readUInt32BE(20),
    observed_product: 'KyCalendar', observed_surface: surface, filename_matches_content: true, stage, viewport: size, theme: scheme, role,
    cohort: ['month', 'week', 'day'].includes(surface) ? 'calendar-grid' : surface.includes('list') ? 'calendar-list' : surface.includes('event') ? 'event-editor' : surface.startsWith('admin-') ? 'admin-console' : surface === 'login' ? 'auth' : 'settings',
    state: ['month', 'week', 'day', 'list', 'default-phone-list'].includes(surface) ? 'populated' : surface === 'admin-people' ? 'sparse' : surface === 'admin-group-calendars' ? 'sparse' : 'empty',
    membership: surface === 'default-phone-list' ? 'supporting evidence only' : 'candidate', conditions: evidence, diagnostics: structuredClone(diagnostics), ...extra };
  manifest.files.push(row); await checkpoint(); console.log(file);
}
try {
  for (const scheme of ['light', 'dark']) for (const [size, width, height] of [['desktop', 1280, 900], ['mobile', 390, 844]]) {
    assert.equal((await sourceBinding()).runtime_sha256, before.runtime_sha256, 'runtime changed during capture');
    const everyday = await fresh(scheme, { width, height }, 'person');
    try {
      const { page, diagnostics } = everyday;
      assert.equal(await page.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name: 'People', exact: true }).count(), 0);
      if (size === 'mobile') await shot(page, diagnostics, 'default-phone-list', size, scheme, 'everyday', { caveat: 'Separate evidence of the native phone default; not a substitute for explicit calendar views.' });
      await setDate(page);
      for (const [surface, view] of [['month', 'dayGridMonth'], ['week', 'timeGridWeek'], ['day', 'timeGridDay'], ['list', 'listWeek']]) {
        await page.locator(`.fc-${view}-button`).click(); await settle(page);
        await page.evaluate(() => scrollTo(0, 0));
        await shot(page, diagnostics, surface, size, scheme, 'everyday');
        if(surface==='month') {
          manifest.supplemental_probes.push({probe:'Month actual accessible tree',size,scheme,snapshot:await page.locator('.fc-daygrid-body').ariaSnapshot(),team_checkin_button_count:await page.getByRole('button',{name:/Team check-in/}).count()});
        }
      }
      // Keep the real New event action, then set the unsaved form's date explicitly.
      await page.locator('.fc-dayGridMonth-button').click(); await settle(page);
      await page.getByRole('button', { name: 'New event', exact: true }).click();
      await page.getByRole('dialog', { name: 'New event', exact: true }).waitFor();
      await page.getByLabel('Start', { exact: true }).fill(config.display_date + 'T09:00');
      await page.getByLabel('End', { exact: true }).fill(config.display_date + 'T10:00');
      await shot(page, diagnostics, 'new-event', size, scheme, 'everyday', { caveat: 'Unsaved blank event on fixed display date; underlying month.' });
      await page.keyboard.press('Escape');
      await page.getByRole('dialog').waitFor({ state: 'detached' });
      await page.locator('.fc-listWeek-button').click(); await settle(page);
      const event = page.locator('.fc-list-event', { hasText: 'Design review' }).locator('a, [tabindex]').first();
      await event.focus(); await page.keyboard.press('Enter');
      await page.getByRole('dialog', { name: 'Edit event', exact: true }).waitFor();
      assert.equal(await page.getByLabel('Title', { exact: true }).inputValue(), 'Design review');
      await shot(page, diagnostics, 'existing-event', size, scheme, 'everyday', { caveat: 'Genuine owner-editable synthetic event; opened with Enter from List.' });
      await page.keyboard.press('Escape'); await page.getByRole('dialog').waitFor({ state: 'detached' }); await settle(page);
      manifest.keyboard_probes.push({ size, scheme, list_enter_opens_real_event: true, escape_closes: true,
        focus_returned_to_trigger: await event.evaluate((e) => e === document.activeElement).catch(() => false), scope: 'limited event dialog probe; not a complete keyboard/focus gate' });
      await nav(page, 'Appearance'); await page.getByRole('heading', { level: 1, name: 'Appearance', exact: true }).waitFor();
      await page.evaluate(() => scrollTo(0, 0)); await shot(page, diagnostics, 'appearance', size, scheme, 'everyday');
      await nav(page, 'Phones & apps'); await page.getByRole('heading', { level: 1, name: 'Phones and apps', exact: true }).waitFor();
      await page.evaluate(() => scrollTo(0, 0)); await shot(page, diagnostics, 'phones-and-apps', size, scheme, 'everyday');
      const signout=await page.getByRole('button',{name:'Sign out',exact:true}).evaluate(e=>{const r=e.getBoundingClientRect();return {x:r.x,right:r.right,width:r.width,height:r.height,viewport:innerWidth,accessible_label:e.textContent}});
      const items=[]; for(const b of await page.getByRole('navigation',{name:'Primary'}).getByRole('button').all()){await b.focus();items.push(await b.evaluate(e=>{const r=e.getBoundingClientRect();return {name:e.textContent,focused:e===document.activeElement,x:r.x,right:r.right,viewport:innerWidth}}));}
      await nav(page,'Calendar');
      let disclosure=null;
      if(size==='mobile') { const picker=page.locator('.kc-picker'); await picker.locator('summary').click(); await page.getByRole('complementary',{name:'Calendars'}).waitFor({state:'visible'}); const open=await picker.getAttribute('open'); await picker.locator('summary').focus(); await page.keyboard.press('Escape'); disclosure={opened:open!==null,closed:await picker.getAttribute('open')===null,focus_summary:await picker.locator('summary').evaluate(e=>e===document.activeElement)}; }
      manifest.supplemental_probes.push({probe:'post-capture everyday navigation and signout',size,scheme,signout,items,disclosure});
    } finally { await everyday.context.close(); }
    const administrator = await fresh(scheme, { width, height }, 'admin');
    try {
      const { page, diagnostics } = administrator;
      assert.equal(await page.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name: 'Calendar', exact: true }).count(), 0);
      await shot(page, diagnostics, 'admin-overview', size, scheme, 'administrator');
      await nav(page, 'People'); await page.getByRole('heading', { level: 1, name: 'People', exact: true }).waitFor();
      await shot(page, diagnostics, 'admin-people', size, scheme, 'administrator');
      await nav(page, 'Group calendars'); await page.getByRole('heading', { level: 1, name: 'Group calendars', exact: true }).waitFor();
      await shot(page, diagnostics, 'admin-group-calendars', size, scheme, 'administrator');
      const items=[]; for(const b of await page.getByRole('navigation',{name:'Primary'}).getByRole('button').all()){await b.focus();items.push(await b.evaluate(e=>{const r=e.getBoundingClientRect();return {name:e.textContent,focused:e===document.activeElement,x:r.x,right:r.right,viewport:innerWidth}}));}
      manifest.supplemental_probes.push({probe:'post-capture administrator navigation',size,scheme,items});
    } finally { await administrator.context.close(); }
    const login = await fresh(scheme, { width, height }, null);
    try { await shot(login.page, login.diagnostics, 'login', size, scheme, 'signed-out'); } finally { await login.context.close(); }
  }
} catch (e) {
  manifest.failures.push({ step: 'capture batch', raw_error: e.stack });
  process.exitCode = 1;
  console.error(e.message);
} finally {
  await Promise.allSettled(responses);
  manifest.served_assets = [...assets.values()].sort((a, b) => a.path.localeCompare(b.path));
  manifest.source_after = await sourceBinding();
  manifest.source_frozen = manifest.source_after.runtime_sha256 === before.runtime_sha256;
  manifest.driver={file:'capture-round2-confirmation.mjs',sha256:sha(await readFile(fileURLToPath(import.meta.url))),copied_from:'capture.mjs',original_sha256:sha(await readFile(join(out,'capture.mjs')))};
  manifest.finished_at = new Date().toISOString();
  await browser.close(); manifest.browser_closed = true;
  await checkpoint();
}

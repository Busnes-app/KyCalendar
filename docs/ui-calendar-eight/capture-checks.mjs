// Targeted evidence audit only: no build, suites, browser, integrations, or product writes.
// Usage: node capture-checks.mjs PRIVATE_DIRECTORY [00-before]
import { readFile, writeFile, stat, readdir, readlink } from 'node:fs/promises';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { resolve, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import assert from 'node:assert/strict';
const root = resolve(fileURLToPath(new URL('../..', import.meta.url)));
const out = resolve(fileURLToPath(new URL('.', import.meta.url)));
const [dir, stage = '00-before', reuseConfirmation] = process.argv.slice(2);
assert(dir);
assert(reuseConfirmation === undefined || reuseConfirmation === '--reuse-static-confirmation');
const suffix = stage === '00-before' ? 'v1' : stage;
const checksName = stage === '00-before' ? 'capture-checks' : `capture-checks-${stage}`;
const readJSON = async (name) => JSON.parse(await readFile(join(out, name), 'utf8'));
const manifest = await readJSON(`manifest-${stage}.json`);
const inventory = await readJSON(`inventory-${suffix}.json`);
const artifacts = await readJSON(`artifacts-${suffix}.json`);
const hash = (b) => createHash('sha256').update(b).digest('hex');
const result = { schema: 'kycalendar-capture-checks/1', checked_at: new Date().toISOString(), stage, source_head: manifest.source_before.head,
  workflow: 'documented Playwright capture; artifact audits use Node/Pillow/numpy; native preview only inspected empty login',
  checks: [], behavior_gates: {}, limitations: [], supplemental_assets: [], driver_bindings: [], per_file: [], raw_failures: manifest.failures };
const check = (name, pass, evidence) => result.checks.push({ name, status: pass ? 'pass' : 'fail', evidence });
const files = manifest.files;
const matrix = files.filter((r) => r.membership === 'candidate');
check('complete requested matrix', matrix.length === 48 && new Set(matrix.map((r) => `${r.observed_surface}/${r.viewport}/${r.theme}`)).size === 48, '12 surfaces × 2 sizes × 2 OS schemes; 2 extra default-phone-list originals');
check('source stayed frozen', manifest.source_frozen && manifest.source_before.head === manifest.source_after.head, manifest.source_before.runtime_sha256);
check('runtime still matches captured source', execFileSync('git', ['diff', 'HEAD', '--', 'cmd', 'internal', 'third_party', 'web', 'go.mod', 'go.sum'], { cwd: root, encoding: 'utf8' }) === '', 'Current tracked runtime diff is empty; root AGENTS and evidence docs intentionally changed.');
check('browser closed', manifest.browser_closed, 'capture.mjs finally block closed Chromium; native preview was closed beforehand');
check('exact conditions', files.every((r) => r.width === (r.viewport === 'desktop' ? 1280 : 390) && r.height === (r.viewport === 'desktop' ? 900 : 844) && r.conditions.viewport.dpr === 1 && r.conditions.locale === 'en-US' && r.conditions.timezone === 'America/New_York' && r.conditions.saved_theme === null && r.conditions.scroll.y === 0), 'PNG IHDR dimensions plus runtime DPR/locale/zone/storage/scroll probes per file');
check('real displayed calendar date', files.filter((r) => r.observed_surface === 'day').every((r) => r.conditions.calendar_heading === 'October 27, 2026'), 'Day view normalized using Previous/Next controls. Month October 2026; week/list October 25–31. No clock mock.');
check('no horizontal page overflow', files.every((r) => r.conditions.overflow_px === 0), 'documentElement.scrollWidth − innerWidth = 0 on all 50 originals');
check('one left navigation region', files.filter((r) => r.role !== 'signed-out').every((r) => r.conditions.left_regions.app_header_count === 1 && r.conditions.left_regions.primary_nav_count === 1 && r.conditions.left_regions.calendar_complementary_count <= 1 && r.conditions.left_regions.calendar_inside_header), 'DOM containment: one app-header/Primary nav; calendar complementary is inside it; desktop left count 1. Phone uses top navigation.');
check('no page errors or CSP console violations', files.every((r) => !r.diagnostics.page_errors.length && !r.diagnostics.csp.length && !r.diagnostics.console_errors.length), 'Playwright pageerror/console listeners active before navigation');
check('no failed HTTP/network requests', files.every((r) => !r.diagnostics.http_errors.length && !r.diagnostics.request_failures.length), 'Observed page responses and requestfailed events; workers retained');
check('security headers retained', files.every((r) => /script-src 'self'/.test(r.diagnostics.document_csp ?? '')), 'Original document Content-Security-Policy recorded per file; no response interception');
check('service workers retained', files.every((r) => r.conditions.service_worker_controlled), 'Real navigator.serviceWorker.controller true for all 50 screenshots; serviceWorkers:allow');
check('role isolation', manifest.fixture.role_isolation.everyday_people_status === 403 && manifest.fixture.role_isolation.admin_calendar_status === 403, 'Real authenticated API requests: everyday GET /api/admin/users 403; admin GET /api/calendars 403; role-specific navigation absence asserted.');
check('forced password changes', manifest.fixture.auth_setup.length === 2 && manifest.fixture.auth_setup.every((r) => r.forced_change_verified && r.replaced_via_real_auth_csrf), 'init-admin/create-user bootstrap; own real auth jars; password replacements; then real browser form sign-ins');
check('no filled credential capture', files.every((r) => !r.conditions.credential_inputs_filled), 'No traces/videos/storage-state files exported; login originals have empty credentials. No app password created.');
check('original response assets match frozen dist', manifest.served_assets.every((r) => r.matches_disk), 'Original page response bodies hashed and matched to web/dist; worker-owned requests and TTF bodies require supplemental audit below.');
check('font readiness', files.every((r) => r.conditions.font_probes.fonts_status === 'loaded' && r.conditions.font_probes.space_grotesk_15), 'document.fonts.ready; loaded Space Grotesk face. IBM Plex Mono probe/usage recorded without treating unused faces as failures.');
let originalsOK = true;
for (const row of artifacts.files) {
  const b = await readFile(join(out, row.file));
  originalsOK &&= hash(b) === row.sha256 && b.length === row.bytes && b.readUInt32BE(16) === row.width && b.readUInt32BE(20) === row.height;
}
check('original image inventory integrity', originalsOK && artifacts.files.length === 50, 'All 50 SHA-256/byte sizes/dimensions rechecked against immutable PNG originals');
check('pairwise inventory and membership', artifacts.same_size_pairs.length === 601 && inventory.files.length === 48 && inventory.unscored.length === 2, 'All same-sized pairs compared; no exact duplicate; 2 near-duplicate pairs. Per rubric, default-phone-list is first by filename and represents each matching phone List.');
const processRecord = JSON.parse(await readFile(join(dir, 'process.json'), 'utf8'));
const proc = await readFile(`/proc/${processRecord.pid}/stat`, 'utf8');
check('owned live process', proc.slice(proc.lastIndexOf(')') + 2).split(' ')[19] === processRecord.startticks && await readlink(`/proc/${processRecord.pid}/exe`) === processRecord.executable && hash(await readFile(processRecord.executable)) === processRecord.sha256, `pid ${processRecord.pid}; startticks ${processRecord.startticks}; executable SHA-256 ${processRecord.sha256}`);
const privatePaths = [];
async function walk(folder) {
  const s = await stat(folder); privatePaths.push({ path: folder.slice(dir.length) || '.', mode: (s.mode & 0o777).toString(8), kind: s.isDirectory() ? 'directory' : 'file' });
  if (s.isDirectory()) for (const name of await readdir(folder)) await walk(join(folder, name));
}
await walk(dir);
check('private fixture permissions', privatePaths.every((r) => r.kind === 'directory' ? r.mode === '700' : ['600', '700'].includes(r.mode)), privatePaths);
// ONE bounded non-browser confirmation. TTF requests were observed but original body
// collection covered woff only; verify their served bytes and the active worker now.
const requested = new Set(files.flatMap((r) => Object.keys(r.diagnostics.request_counts)).filter((p) => p === '/' || /\.(js|css|ttf|woff2?|png|svg|webmanifest)$/.test(p)));
requested.add('/sw.js');
if (reuseConfirmation) {
  const previous = await readJSON(checksName + '.json');
  assert.equal(previous.source_head, manifest.source_before.head);
  assert.equal(processRecord.sha256, manifest.binary.sha256);
  assert(previous.supplemental_assets.every((r) => r.matches));
  result.supplemental_assets = previous.supplemental_assets;
  result.supplemental_confirmation_reused_from = previous.supplemental_confirmation_reused_from ?? previous.checked_at;
} else for (const path of [...requested].sort()) {
  const response = await fetch(manifest.fixture.origin + path, { signal: AbortSignal.timeout(5000) });
  const bytes = Buffer.from(await response.arrayBuffer());
  const file = path === '/' ? 'web/dist/index.html' : 'web/dist' + path;
  const disk = await readFile(join(root, file));
  result.supplemental_assets.push({ path, file, status: response.status, bytes: bytes.length, sha256: hash(bytes), disk_sha256: hash(disk), matches: response.ok && hash(bytes) === hash(disk),
    evidence: path === '/sw.js' ? 'Real controlling worker observed; its public script fetched for byte binding after capture' : 'Path observed in original browser requests; loopback response body byte-bound after capture' });
}
check('supplemental served font and worker bindings', result.supplemental_assets.every((r) => r.matches), 'One bounded post-capture loopback fetch per actually requested static path, plus controlling /sw.js; no browser reopened, no mocks');
for (const name of ['capture.mjs', 'capture-fixture.mjs', 'capture-inventory.mjs', 'capture-checks.mjs']) {
  const bytes = await readFile(join(out, name)); result.driver_bindings.push({ file: name, sha256: hash(bytes), bytes: bytes.length });
}
result.per_file = files.map((r) => ({ file: r.file, overflow_px: r.conditions.overflow_px, left_regions: r.conditions.left_regions, font_probes: r.conditions.font_probes,
  actions_forms: r.conditions.visible_actions_forms, form_count: r.conditions.forms_in_dom, errors: r.diagnostics, title: r.conditions.h1, dialog: r.conditions.dialog,
  calendar_heading: r.conditions.calendar_heading, calendar_view: r.conditions.calendar_view, event_titles: r.conditions.event_titles_in_dom }));
result.behavior_gates = {
  keyboard: { status: 'fail', evidence: manifest.keyboard_probes, reason: 'Enter opens real owner event and Escape closes, but focus return to event trigger failed in all four conditions. Other task paths not comprehensively keyboard-tested.' },
  focus: { status: 'untested', reason: 'Visible autofocus ring appears in dialog captures, but every interactive element in both themes was not measured or exercised. See failed focus-return probe.' },
  contrast: { status: 'untested', reason: 'No computed text/border/focus contrast sweep across every shipped theme. Screenshot legibility is not a contrast pass.' },
  states: { status: 'untested', reason: 'Fresh populated calendar, sparse People/group calendar, empty app passwords and blank form states captured. Loading/error state matrix not exercised.' },
  responsive: { status: 'untested', measured: 'All 50 initial viewport originals have zero horizontal page overflow; real nav clicks reached requested pages; calendar views rendered at 390px and 1280px.', reason: 'Full target usability and every navigation/focus path not exercised; no screenshot-only gate pass.' }
};
result.limitations = [
  'No ratings or prior scores. Full Go/frontend/browser/race/integration suites are hosted-CI only and were not run locally; frontend was not rebuilt.',
  'One capped Go build embeds committed web/dist at 0f01550. Actual binary metadata says vcs.modified=false (build preceded evidence edits). Capture tracked status says M AGENTS.md and evidence is untracked. This is not a claim of a clean rebuild of the current documentation tree.',
  'All images are initial viewport PNG originals, with pointer at neutral bottom-right. Appearance and tall admin pages intentionally continue below the fold.',
  'Near-duplicate phone List originals are retained. Per first-filename rule, default-phone-list representatives enter inventory; explicit list-mobile files are excluded. No exact duplicates.',
  'Keyboard focus return failed after closing existing event. The four raw probes remain in the manifest; no fix or recapture attempted.',
  'Login generated repeated real /api/auth/pow-challenge requests (maximum 573 observed in a single context). Visible UI settled and requests succeeded, but network quiescence is not claimed. No challenge/token bodies recorded.',
  'Original response collector did not include .ttf and does not see all worker-owned requests. Supplemental loopback byte bindings verify observed font paths and active sw.js against the same frozen dist; those are explicitly post-capture evidence.',
  'Calendar inside-header containment is reliable. The simple descendant rectangle visibility probe can count phone calendar content despite the closed disclosure; do not interpret that field as a second visible navigation region.',
  'Real clock remains live: today accent, time indicator, relative login times, and New event default can change in later matched captures. Calendar date and unsaved Start/End are explicitly normalized; fixture is retained.',
  'Local standalone fixture only: no SSO, production, external feeds, recovery pairing, mail, or network integration. Admin group calendar has no grants; personal fixture belongs only to everyday account.',
  'MySlop handoff failed: HTTP 401 using both environment credential and documented curl configuration. No board post claimed; parent must post with working credentials.',
  'DOX pass: applicable contracts read; no AGENTS files changed by capture worker. Parent owns the evidence hierarchy/index and authority inputs.'
];
result.summary = { matrix_images: 48, extra_images: 2, unique_candidates: inventory.files.length, all_images: artifacts.files.length, pairwise_comparisons: 601,
  checks_passed: result.checks.filter((r) => r.status === 'pass').length, checks_failed: result.checks.filter((r) => r.status === 'fail').length,
  behavior_gate_failure: 'keyboard focus return failed in all four probes; remaining complete gates untested',
  full_suites: 'not run; hosted CI only', na_surfaces: [], product_code_changes: false, commit_push: false };
await writeFile(join(out, checksName + '.json'), JSON.stringify(result, null, 2) + '\n');
const md = `# Fresh KyCalendar baseline capture checks\n\n` +
  `48 requested originals plus 2 native phone-default List originals; 48 unique scoring candidates after duplicate handling. No surfaces N/A. No ratings.\n\n` +
  `**Behavior failure: focus return to the event trigger failed in 4/4 probes. Complete focus, contrast, states and responsive gates remain untested.** The 22 passing checks below cover capture integrity and specific measured conditions.\n\n` +
  `Source: \`${manifest.source_before.head}\` (\`${manifest.source_before.branch}\`). Runtime stayed frozen, committed embedded dist reused; root AGENTS/evidence docs changed. Actual build metadata: \`vcs.modified=false\`; this build preceded evidence edits and is not a clean-rebuild claim for the current docs tree.\n\n` +
  `Capture: Chromium ${manifest.browser.version}, Playwright ${manifest.browser.playwright_version}, DPR1; 1280×900 and 390×844; OS light/dark; en-US; America/New_York; no saved theme. Display date 2026-10-27, month October 2026, week/list October 25–31. Real clock unchanged. Native preview only inspected the empty login page, then closed. Exact captures use documented Playwright.\n\n` +
  `## Actual targeted checks\n\n` + result.checks.map((r) => `- **${r.status.toUpperCase()} — ${r.name}:** ${typeof r.evidence === 'string' ? r.evidence : 'Raw structured evidence in capture-checks.json.'}`).join('\n') + '\n\n' +
  `## Behavior gates\n\n` + Object.entries(result.behavior_gates).map(([name, r]) => `- **${name}: ${r.status}.** ${r.reason}`).join('\n') + '\n\n' +
  `## Duplicate measurements\n\nNo SHA-256 exact duplicates. The same-theme default-phone-list / explicit List pairs are near duplicates: light 7/329160 changed pixels (0.0021266%), max channel delta 1; dark 10/329160 (0.0030380%), max delta 1. All 601 same-size pairs are recorded in artifacts-v1.json. Score each group once on the first filename; the two explicit phone List originals remain preserved and excluded.\n\n` +
  `## Files\n\n- manifest-${stage}.json — source hashes before/after, served original response hashes, fixture/date, binary and per-image conditions/diagnostics.\n- inventory-v1.json — 48 current unique candidates and 2 exclusions, with hashes/bytes/dimensions.\n- artifacts-v1.json — all 50 originals and every same-size pixel comparison.\n- capture-checks.json — targeted results, all visible actions/forms, font probes, supplemental served-font/worker bindings and raw failures.\n- screenshots/ — untouched PNG originals.\n- capture.mjs, capture-fixture.mjs, capture-inventory.mjs, capture-checks.mjs — reproducible driver and audits.\n\n` +
  `## Private fixture and rerun\n\nFixture: \`${dir}\` (0700), origin \`${manifest.fixture.origin}\`. Credentials, database, logs and process metadata remain private (0600); executables/owned stop script are 0700. The live PID is ${processRecord.pid}, startticks ${processRecord.startticks}; SHA-256 \`${processRecord.sha256}\`. Stop only with \`${dir}/stop.sh\`, which verifies PID startticks, executable and hash.\n\n` +
  `For a matched after: preserve this directory and seed.json. Stop the old process with its owned stop script before replacing the private server binary. If stopped, run \`node docs/ui-calendar-eight/capture-fixture.mjs start ${dir}\` (does not reseed). Use the intended new binary deliberately; the start command records its identity. Then run \`node docs/ui-calendar-eight/capture.mjs ${dir} 01-after\`. Set CAPTURE_PLAYWRIGHT only to an existing installed module if its path changes; no install needed. Capture refuses runtime diff from HEAD, so after runtime changes must be committed by the parent first or that precondition must be deliberately revised and recorded. The worker does not commit/push. Each stage refuses to overwrite its manifest or originals. Run \`node docs/ui-calendar-eight/capture-inventory.mjs 01-after\` and \`node docs/ui-calendar-eight/capture-checks.mjs ${dir} 01-after\`; stage-specific audit outputs preserve the before v1 inventories/checks. Baseline-specific assertions/summary prose in the audit must be reassessed for a changed after result rather than treated as acceptance.\n\n` +
  `## Limitations and handoff\n\n` + result.limitations.map((s) => `- ${s}`).join('\n') + '\n';
await writeFile(join(out, checksName + '.md'), md);
console.log(JSON.stringify(result.summary));

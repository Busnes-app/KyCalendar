# Fresh KyCalendar baseline capture checks

48 requested originals plus 2 native phone-default List originals; 48 unique scoring candidates after duplicate handling. No surfaces N/A. No ratings.

**Behavior failure: focus return to the event trigger failed in 4/4 probes. Complete focus, contrast, states and responsive gates remain untested.** The 22 passing checks below cover capture integrity and specific measured conditions.

Source: `0f0155027a87f3510740afe1b524a96846002fd3` (`ui/calendar-eight`). Runtime stayed frozen, committed embedded dist reused; root AGENTS/evidence docs changed. Actual build metadata: `vcs.modified=false`; this build preceded evidence edits and is not a clean-rebuild claim for the current docs tree.

Capture: Chromium 153.0.8010.12, Playwright 1.63.0, DPR1; 1280×900 and 390×844; OS light/dark; en-US; America/New_York; no saved theme. Display date 2026-10-27, month October 2026, week/list October 25–31. Real clock unchanged. Native preview only inspected the empty login page, then closed. Exact captures use documented Playwright.

## Actual targeted checks

- **PASS — complete requested matrix:** 12 surfaces × 2 sizes × 2 OS schemes; 2 extra default-phone-list originals
- **PASS — source stayed frozen:** 09ee05752478ce7384e37a790f282cb002bb2e51e6fbe9c00012930e2c6f8101
- **PASS — runtime still matches captured source:** Current tracked runtime diff is empty; root AGENTS and evidence docs intentionally changed.
- **PASS — browser closed:** capture.mjs finally block closed Chromium; native preview was closed beforehand
- **PASS — exact conditions:** PNG IHDR dimensions plus runtime DPR/locale/zone/storage/scroll probes per file
- **PASS — real displayed calendar date:** Day view normalized using Previous/Next controls. Month October 2026; week/list October 25–31. No clock mock.
- **PASS — no horizontal page overflow:** documentElement.scrollWidth − innerWidth = 0 on all 50 originals
- **PASS — one left navigation region:** DOM containment: one app-header/Primary nav; calendar complementary is inside it; desktop left count 1. Phone uses top navigation.
- **PASS — no page errors or CSP console violations:** Playwright pageerror/console listeners active before navigation
- **PASS — no failed HTTP/network requests:** Observed page responses and requestfailed events; workers retained
- **PASS — security headers retained:** Original document Content-Security-Policy recorded per file; no response interception
- **PASS — service workers retained:** Real navigator.serviceWorker.controller true for all 50 screenshots; serviceWorkers:allow
- **PASS — role isolation:** Real authenticated API requests: everyday GET /api/admin/users 403; admin GET /api/calendars 403; role-specific navigation absence asserted.
- **PASS — forced password changes:** init-admin/create-user bootstrap; own real auth jars; password replacements; then real browser form sign-ins
- **PASS — no filled credential capture:** No traces/videos/storage-state files exported; login originals have empty credentials. No app password created.
- **PASS — original response assets match frozen dist:** Original page response bodies hashed and matched to web/dist; worker-owned requests and TTF bodies require supplemental audit below.
- **PASS — font readiness:** document.fonts.ready; loaded Space Grotesk face. IBM Plex Mono probe/usage recorded without treating unused faces as failures.
- **PASS — original image inventory integrity:** All 50 SHA-256/byte sizes/dimensions rechecked against immutable PNG originals
- **PASS — pairwise inventory and membership:** All same-sized pairs compared; no exact duplicate; 2 near-duplicate pairs. Per rubric, default-phone-list is first by filename and represents each matching phone List.
- **PASS — owned live process:** pid 575742; startticks 2419119; executable SHA-256 88a45315e884497ab598547eee35830a2cfeccc429a22264c166ff9e22f0be17
- **PASS — private fixture permissions:** Raw structured evidence in capture-checks.json.
- **PASS — supplemental served font and worker bindings:** One bounded post-capture loopback fetch per actually requested static path, plus controlling /sw.js; no browser reopened, no mocks

## Behavior gates

- **keyboard: fail.** Enter opens real owner event and Escape closes, but focus return to event trigger failed in all four conditions. Other task paths not comprehensively keyboard-tested.
- **focus: untested.** Visible autofocus ring appears in dialog captures, but every interactive element in both themes was not measured or exercised. See failed focus-return probe.
- **contrast: untested.** No computed text/border/focus contrast sweep across every shipped theme. Screenshot legibility is not a contrast pass.
- **states: untested.** Fresh populated calendar, sparse People/group calendar, empty app passwords and blank form states captured. Loading/error state matrix not exercised.
- **responsive: untested.** Full target usability and every navigation/focus path not exercised; no screenshot-only gate pass.

## Duplicate measurements

No SHA-256 exact duplicates. The same-theme default-phone-list / explicit List pairs are near duplicates: light 7/329160 changed pixels (0.0021266%), max channel delta 1; dark 10/329160 (0.0030380%), max delta 1. All 601 same-size pairs are recorded in artifacts-v1.json. Score each group once on the first filename; the two explicit phone List originals remain preserved and excluded.

## Files

- manifest-00-before.json — source hashes before/after, served original response hashes, fixture/date, binary and per-image conditions/diagnostics.
- inventory-v1.json — 48 current unique candidates and 2 exclusions, with hashes/bytes/dimensions.
- artifacts-v1.json — all 50 originals and every same-size pixel comparison.
- capture-checks.json — targeted results, all visible actions/forms, font probes, supplemental served-font/worker bindings and raw failures.
- screenshots/ — untouched PNG originals.
- capture.mjs, capture-fixture.mjs, capture-inventory.mjs, capture-checks.mjs — reproducible driver and audits.

## Private fixture and rerun

Fixture: `/tmp/kycalendar-eight-Fqv5Ll` (0700), origin `http://127.0.0.1:36827`. Credentials, database, logs and process metadata remain private (0600); executables/owned stop script are 0700. The live PID is 575742, startticks 2419119; SHA-256 `88a45315e884497ab598547eee35830a2cfeccc429a22264c166ff9e22f0be17`. Stop only with `/tmp/kycalendar-eight-Fqv5Ll/stop.sh`, which verifies PID startticks, executable and hash.

For a matched after: preserve this directory and seed.json. Stop the old process with its owned stop script before replacing the private server binary. If stopped, run `node docs/ui-calendar-eight/capture-fixture.mjs start /tmp/kycalendar-eight-Fqv5Ll` (does not reseed). Use the intended new binary deliberately; the start command records its identity. Then run `node docs/ui-calendar-eight/capture.mjs /tmp/kycalendar-eight-Fqv5Ll 01-after`. Set CAPTURE_PLAYWRIGHT only to an existing installed module if its path changes; no install needed. Capture refuses runtime diff from HEAD, so after runtime changes must be committed by the parent first or that precondition must be deliberately revised and recorded. The worker does not commit/push. Each stage refuses to overwrite its manifest or originals. Run `node docs/ui-calendar-eight/capture-inventory.mjs 01-after` and `node docs/ui-calendar-eight/capture-checks.mjs /tmp/kycalendar-eight-Fqv5Ll 01-after`; stage-specific audit outputs preserve the before v1 inventories/checks. Baseline-specific assertions/summary prose in the audit must be reassessed for a changed after result rather than treated as acceptance.

## Limitations and handoff

- No ratings or prior scores. Full Go/frontend/browser/race/integration suites are hosted-CI only and were not run locally; frontend was not rebuilt.
- One capped Go build embeds committed web/dist at 0f01550. Actual binary metadata says vcs.modified=false (build preceded evidence edits). Capture tracked status says M AGENTS.md and evidence is untracked. This is not a claim of a clean rebuild of the current documentation tree.
- All images are initial viewport PNG originals, with pointer at neutral bottom-right. Appearance and tall admin pages intentionally continue below the fold.
- Near-duplicate phone List originals are retained. Per first-filename rule, default-phone-list representatives enter inventory; explicit list-mobile files are excluded. No exact duplicates.
- Keyboard focus return failed after closing existing event. The four raw probes remain in the manifest; no fix or recapture attempted.
- Login generated repeated real /api/auth/pow-challenge requests (maximum 573 observed in a single context). Visible UI settled and requests succeeded, but network quiescence is not claimed. No challenge/token bodies recorded.
- Original response collector did not include .ttf and does not see all worker-owned requests. Supplemental loopback byte bindings verify observed font paths and active sw.js against the same frozen dist; those are explicitly post-capture evidence.
- Calendar inside-header containment is reliable. The simple descendant rectangle visibility probe can count phone calendar content despite the closed disclosure; do not interpret that field as a second visible navigation region.
- Real clock remains live: today accent, time indicator, relative login times, and New event default can change in later matched captures. Calendar date and unsaved Start/End are explicitly normalized; fixture is retained.
- Local standalone fixture only: no SSO, production, external feeds, recovery pairing, mail, or network integration. Admin group calendar has no grants; personal fixture belongs only to everyday account.
- MySlop handoff failed: HTTP 401 using both environment credential and documented curl configuration. No board post claimed; parent must post with working credentials.
- DOX pass: applicable contracts read; no AGENTS files changed by capture worker. Parent owns the evidence hierarchy/index and authority inputs.

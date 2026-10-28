# Matched KyCalendar round2 capture checks

48 current unique quality candidates from 50 selected originals. All 54 after/correction originals individually inspected at full resolution; 50 baseline + 54 new + 2 brand originals retained (106 total). One four-Appearance correction; no runtime edits/builds, commits or pushes.

27 targeted checks pass; 2 fail. Complete behavior gates remain untested. Focus return passes 4/4; phone Week ArrowRight 2/2; login challenge count stays 1 in all four typing probes.

Source `d7e3b89843c5893af0d18cc7e3ac147880cc91c4`; private process PID 595033, startticks 2696963, executable `/tmp/kycalendar-eight-Fqv5Ll/server-final`, SHA-256 `e216cb1fc54afc51bae13a46fc52ac1e7e020948bf081f1d59848b283b1785cd`. Actual running executable metadata: vcs.modified=false, matching revision; Go1.27.3. Fixture `/tmp/kycalendar-eight-Fqv5Ll` retained at `http://127.0.0.1:36827`, never reseeded. Stop only with owned private stop.sh, after verifying record and port ownership.

## Current membership and correction

Use inventory-v2.json files for quality. 01-after Appearance four files are diagnostic only; 02-confirmation Appearance replaces them. Two near-duplicate explicit phone List images remain unscored; first-filename default-phone-list representatives enter quality. Historical baseline and KyIdentity brand references never enter the current mean. Stage-specific inventory-01-after/artifacts-01-after outputs are preserved and describe that original stage, not canonical current membership.

All 601 same-sized CURRENT selected-original pairs are in artifacts-v2.json. Matched 50 before/current pixel/hash pairs are in round2-matched-pair-audit.json. 22 current images exactly match baseline by SHA-256; rating changes on these are reviewer/rubric drift, not product improvement.

## Targeted checks

- **PASS — complete current matched matrix**
- **PASS — immutable baseline and original drivers**
- **PASS — source frozen at requested HEAD**
- **PASS — current runtime still matches captured file hashes**
- **PASS — owned live process and actual metadata**
- **PASS — seed reused unchanged**
- **PASS — one browser at a time and both closed**
- **PASS — dimensions and capture conditions**
- **PASS — zero horizontal PAGE overflow**
- **PASS — one left region**
- **PASS — no page errors or CSP violations**
- **PASS — no unexpected HTTP/network/console errors**
- **PASS — security headers and service workers retained**
- **PASS — no filled credential captures**
- **PASS — font readiness**
- **PASS — original served assets match embedded dist**
- **PASS — supplemental fonts/worker/static bindings**
- **PASS — Enter/Escape focus returns to replacement anchor 4/4**
- **PASS — phone Week horizontal keyboard movement 2/2**
- **PASS — phone Week hint and internal grid**
- **PASS — phone Month visible title boxes**
- **PASS — Month full accessible time/title content**
- **PASS — real role API isolation 8/8**
- **PASS — login challenge request counts stable 4/4**
- **FAIL — navigation controls focused and reachable**
- **PASS — phone Calendars disclosure open/Escape/focus**
- **FAIL — phone signout right alignment**
- **PASS — private fixture permissions**
- **PASS — original image bytes and pinned duplicate rule**

## Behavior gate limits

- **keyboard: untested** — Targeted Enter/Escape, replacement event anchor 4/4, Week ArrowRight 2/2, disclosure Escape and nav focus reachability pass; not every task keyboard-only.
- **focus: untested** — Event anchor return passes; all controls/themes visible-focus measurement not done.
- **contrast: untested** — No complete computed text/border/focus contrast measurement across all shipped themes.
- **states: untested** — Populated/sparse/empty matrix captured; full loading/error state matrix not exercised.
- **responsive: untested** — Zero page overflow and targeted nav/Week geometry pass; full target usability at both sizes not exhaustively exercised.

## Per-image full-resolution inspection

- `screenshots/kycalendar-01-after-month-desktop-light.png` — CURRENT: Weekday/date grid and semantic calendar colours remain. Short titles and event times readable; long workshop title ellipsizes in cell; spanning Release preparation remains blue.
- `screenshots/kycalendar-01-after-week-desktop-light.png` — CURRENT: One-line titles ellipsize instead of breaking mid-line; semantic blue/green/purple event fills remain. All seven days visible; Design review and short Team check-in readable; long workshop and appointment names ellipsize.
- `screenshots/kycalendar-01-after-day-desktop-light.png` — CURRENT: Library day, Design review and Lunch break readable; orange selected Day and neutral today column remain. Long workshop title fits on one line; time grid opens at 07:00.
- `screenshots/kycalendar-01-after-list-desktop-light.png` — CURRENT: Readable time/title rows and day headers; semantic colour dots retained. All seeded week rows fit; long workshop title fits on one line.
- `screenshots/kycalendar-01-after-new-event-desktop-light.png` — CURRENT: Empty title; unsaved Oct27 09:00–10:00; End field focused; labelled inputs, Cancel and Save visible. Centered dialog with Start/End in one row.
- `screenshots/kycalendar-01-after-existing-event-desktop-light.png` — CURRENT: Editable Design review, Meeting room and full handover description; Oct27 10:00–11:00; Title focused; Delete/Cancel/Save visible. Escape/refetch returns focus to replacement anchor.
- `screenshots/kycalendar-01-after-appearance-desktop-light.png` — DIAGNOSTIC: Superseded capture: extra focused Appearance navigation outline caused by driver probe; retained diagnostic. Corrected original-sequence capture: no nav focus-ring contamination. Match system selected; labelled preview cards and browser-local explanation readable. Five-column card picker with all rows visible.
- `screenshots/kycalendar-01-after-phones-and-apps-desktop-light.png` — CURRENT: Calendar app selected, AI agent option visible, empty name, Create and No app passwords yet guidance readable. Setup instructions visible; synthetic loopback URL only.
- `screenshots/kycalendar-01-after-admin-overview-desktop-light.png` — CURRENT: Administrator separation text visible; six management destinations. Six cards in 3×2 layout; no calendar content.
- `screenshots/kycalendar-01-after-admin-people-desktop-light.png` — CURRENT: Synthetic everyday/admin rows, role labels, active status and management actions readable; live last-sign-in times differ from baseline. Two-row table; actions fit without page overflow.
- `screenshots/kycalendar-01-after-admin-group-calendars-desktop-light.png` — CURRENT: Shared planning remains ungranted. Plain access hint replaces nested empty box; disabled Group says No groups available; Role/ Add group visible. All-granted alternative not exercised because fixture has zero groups.
- `screenshots/kycalendar-01-after-login-desktop-light.png` — CURRENT: Empty username/password; Browser check passed; Sign in and Match system readable; no filled secrets or SSO button.
- `screenshots/kycalendar-01-after-default-phone-list-mobile-light.png` — CURRENT: Readable time/title rows and day headers; semantic colour dots retained. Long workshop name wraps across three lines; later days continue below the fold.
- `screenshots/kycalendar-01-after-month-mobile-light.png` — CURRENT: Weekday/date grid and semantic calendar colours remain. 13px event prefixes are visible with ellipsis (e.g. Tea…, Des…, Lun…, Pla…); 1px time box is visually hidden, full time/title is in accessible tree. Tiny 7-column cells still truncate heavily.
- `screenshots/kycalendar-01-after-week-mobile-light.png` — CURRENT: One-line titles ellipsize instead of breaking mid-line; semantic blue/green/purple event fills remain. Visible scroll instruction; first Sunday–Tuesday columns shown in 358px region, 768px grid. Other days need internal scroll; ArrowRight moved 40px.
- `screenshots/kycalendar-01-after-day-mobile-light.png` — CURRENT: Library day, Design review and Lunch break readable; orange selected Day and neutral today column remain. Long workshop name ellipsizes on one line; lower time rows continue below initial viewport.
- `screenshots/kycalendar-01-after-list-mobile-light.png` — CURRENT: Readable time/title rows and day headers; semantic colour dots retained. Long workshop name wraps across three lines; later days continue below the fold.
- `screenshots/kycalendar-01-after-new-event-mobile-light.png` — CURRENT: Empty title; unsaved Oct27 09:00–10:00; End field focused; labelled inputs, Cancel and Save visible. Single-column dialog within 390px viewport.
- `screenshots/kycalendar-01-after-existing-event-mobile-light.png` — CURRENT: Editable Design review, Meeting room and full handover description; Oct27 10:00–11:00; Title focused; Delete/Cancel/Save visible. Escape/refetch returns focus to replacement anchor.
- `screenshots/kycalendar-01-after-appearance-mobile-light.png` — DIAGNOSTIC: Superseded capture: extra focused Appearance navigation outline caused by driver probe; retained diagnostic. Corrected original-sequence capture: no nav focus-ring contamination. Match system selected; labelled preview cards and browser-local explanation readable. Two columns; remaining cards below fold; nav row horizontally scrolled to Appearance.
- `screenshots/kycalendar-01-after-phones-and-apps-mobile-light.png` — CURRENT: Calendar app selected, AI agent option visible, empty name, Create and No app passwords yet guidance readable. Setup continues below fold; no generated token or secret.
- `screenshots/kycalendar-01-after-admin-overview-mobile-light.png` — CURRENT: Administrator separation text visible; six management destinations. Cards stack; first People and Groups fully visible, Group calendars begins below.
- `screenshots/kycalendar-01-after-admin-people-mobile-light.png` — CURRENT: Synthetic everyday/admin rows, role labels, active status and management actions readable; live last-sign-in times differ from baseline. Stacked labelled cards; second row continues below fold.
- `screenshots/kycalendar-01-after-admin-group-calendars-mobile-light.png` — CURRENT: Shared planning remains ungranted. Plain access hint replaces nested empty box; disabled Group says No groups available; Role/ Add group visible. All-granted alternative not exercised because fixture has zero groups.
- `screenshots/kycalendar-01-after-login-mobile-light.png` — CURRENT: Empty username/password; Browser check passed; Sign in and Match system readable; no filled secrets or SSO button.
- `screenshots/kycalendar-01-after-month-desktop-dark.png` — CURRENT: Weekday/date grid and semantic calendar colours remain. Short titles and event times readable; long workshop title ellipsizes in cell; spanning Release preparation remains blue.
- `screenshots/kycalendar-01-after-week-desktop-dark.png` — CURRENT: One-line titles ellipsize instead of breaking mid-line; semantic blue/green/purple event fills remain. All seven days visible; Design review and short Team check-in readable; long workshop and appointment names ellipsize.
- `screenshots/kycalendar-01-after-day-desktop-dark.png` — CURRENT: Library day, Design review and Lunch break readable; orange selected Day and neutral today column remain. Long workshop title fits on one line; time grid opens at 07:00.
- `screenshots/kycalendar-01-after-list-desktop-dark.png` — CURRENT: Readable time/title rows and day headers; semantic colour dots retained. All seeded week rows fit; long workshop title fits on one line.
- `screenshots/kycalendar-01-after-new-event-desktop-dark.png` — CURRENT: Empty title; unsaved Oct27 09:00–10:00; End field focused; labelled inputs, Cancel and Save visible. Centered dialog with Start/End in one row.
- `screenshots/kycalendar-01-after-existing-event-desktop-dark.png` — CURRENT: Editable Design review, Meeting room and full handover description; Oct27 10:00–11:00; Title focused; Delete/Cancel/Save visible. Escape/refetch returns focus to replacement anchor.
- `screenshots/kycalendar-01-after-appearance-desktop-dark.png` — DIAGNOSTIC: Superseded capture: extra focused Appearance navigation outline caused by driver probe; retained diagnostic. Corrected original-sequence capture: no nav focus-ring contamination. Match system selected; labelled preview cards and browser-local explanation readable. Five-column card picker with all rows visible.
- `screenshots/kycalendar-01-after-phones-and-apps-desktop-dark.png` — CURRENT: Calendar app selected, AI agent option visible, empty name, Create and No app passwords yet guidance readable. Setup instructions visible; synthetic loopback URL only.
- `screenshots/kycalendar-01-after-admin-overview-desktop-dark.png` — CURRENT: Administrator separation text visible; six management destinations. Six cards in 3×2 layout; no calendar content.
- `screenshots/kycalendar-01-after-admin-people-desktop-dark.png` — CURRENT: Synthetic everyday/admin rows, role labels, active status and management actions readable; live last-sign-in times differ from baseline. Two-row table; actions fit without page overflow.
- `screenshots/kycalendar-01-after-admin-group-calendars-desktop-dark.png` — CURRENT: Shared planning remains ungranted. Plain access hint replaces nested empty box; disabled Group says No groups available; Role/ Add group visible. All-granted alternative not exercised because fixture has zero groups.
- `screenshots/kycalendar-01-after-login-desktop-dark.png` — CURRENT: Empty username/password; Browser check passed; Sign in and Match system readable; no filled secrets or SSO button.
- `screenshots/kycalendar-01-after-default-phone-list-mobile-dark.png` — CURRENT: Readable time/title rows and day headers; semantic colour dots retained. Long workshop name wraps across three lines; later days continue below the fold.
- `screenshots/kycalendar-01-after-month-mobile-dark.png` — CURRENT: Weekday/date grid and semantic calendar colours remain. 13px event prefixes are visible with ellipsis (e.g. Tea…, Des…, Lun…, Pla…); 1px time box is visually hidden, full time/title is in accessible tree. Tiny 7-column cells still truncate heavily.
- `screenshots/kycalendar-01-after-week-mobile-dark.png` — CURRENT: One-line titles ellipsize instead of breaking mid-line; semantic blue/green/purple event fills remain. Visible scroll instruction; first Sunday–Tuesday columns shown in 358px region, 768px grid. Other days need internal scroll; ArrowRight moved 40px.
- `screenshots/kycalendar-01-after-day-mobile-dark.png` — CURRENT: Library day, Design review and Lunch break readable; orange selected Day and neutral today column remain. Long workshop name ellipsizes on one line; lower time rows continue below initial viewport.
- `screenshots/kycalendar-01-after-list-mobile-dark.png` — CURRENT: Readable time/title rows and day headers; semantic colour dots retained. Long workshop name wraps across three lines; later days continue below the fold.
- `screenshots/kycalendar-01-after-new-event-mobile-dark.png` — CURRENT: Empty title; unsaved Oct27 09:00–10:00; End field focused; labelled inputs, Cancel and Save visible. Single-column dialog within 390px viewport.
- `screenshots/kycalendar-01-after-existing-event-mobile-dark.png` — CURRENT: Editable Design review, Meeting room and full handover description; Oct27 10:00–11:00; Title focused; Delete/Cancel/Save visible. Escape/refetch returns focus to replacement anchor.
- `screenshots/kycalendar-01-after-appearance-mobile-dark.png` — DIAGNOSTIC: Superseded capture: extra focused Appearance navigation outline caused by driver probe; retained diagnostic. Corrected original-sequence capture: no nav focus-ring contamination. Match system selected; labelled preview cards and browser-local explanation readable. Two columns; remaining cards below fold; nav row horizontally scrolled to Appearance.
- `screenshots/kycalendar-01-after-phones-and-apps-mobile-dark.png` — CURRENT: Calendar app selected, AI agent option visible, empty name, Create and No app passwords yet guidance readable. Setup continues below fold; no generated token or secret.
- `screenshots/kycalendar-01-after-admin-overview-mobile-dark.png` — CURRENT: Administrator separation text visible; six management destinations. Cards stack; first People and Groups fully visible, Group calendars begins below.
- `screenshots/kycalendar-01-after-admin-people-mobile-dark.png` — CURRENT: Synthetic everyday/admin rows, role labels, active status and management actions readable; live last-sign-in times differ from baseline. Stacked labelled cards; second row continues below fold.
- `screenshots/kycalendar-01-after-admin-group-calendars-mobile-dark.png` — CURRENT: Shared planning remains ungranted. Plain access hint replaces nested empty box; disabled Group says No groups available; Role/ Add group visible. All-granted alternative not exercised because fixture has zero groups.
- `screenshots/kycalendar-01-after-login-mobile-dark.png` — CURRENT: Empty username/password; Browser check passed; Sign in and Match system readable; no filled secrets or SSO button.
- `screenshots/kycalendar-02-confirmation-appearance-desktop-light.png` — CURRENT: Corrected original-sequence capture: no nav focus-ring contamination. Match system selected; labelled preview cards and browser-local explanation readable. Five-column card picker with all rows visible.
- `screenshots/kycalendar-02-confirmation-appearance-mobile-light.png` — CURRENT: Corrected original-sequence capture: no nav focus-ring contamination. Match system selected; labelled preview cards and browser-local explanation readable. Two columns; remaining cards below fold; nav row horizontally scrolled to Appearance.
- `screenshots/kycalendar-02-confirmation-appearance-desktop-dark.png` — CURRENT: Corrected original-sequence capture: no nav focus-ring contamination. Match system selected; labelled preview cards and browser-local explanation readable. Five-column card picker with all rows visible.
- `screenshots/kycalendar-02-confirmation-appearance-mobile-dark.png` — CURRENT: Corrected original-sequence capture: no nav focus-ring contamination. Match system selected; labelled preview cards and browser-local explanation readable. Two columns; remaining cards below fold; nav row horizontally scrolled to Appearance.

## Preserved raw diagnostics

No page errors, CSP violations or network failures. Deliberate role-denial GETs generated eight 403 responses and matching console messages, retained in raw cumulative file diagnostics. Original signout-selector null and absent explicit event aria-label remain raw; bounded confirmation records real accessible-name signout geometry and Month accessibility tree. No baseline-specific failure/prose reused as fresh acceptance.

## Limitations and handoff

- No full suites or frontend/Go builds run by this worker; hosted CI owns required full checks after host hardlocks. Parent-reported unit/browser results are context only, not fresh executions here.
- Full keyboard, visible focus, all-theme computed contrast, state and complete responsive gates remain untested. Targeted checks do not constitute screenshot-only accessibility acceptance.
- No seed event edited/deleted. Parent already tested deletion fallback on a separate disposable fixture; deletion fallback was not rerun here.
- No groups fixture means all-granted selector text/state is not exercised. Disabled No groups available is captured; Group role selector/Add group remain visible. No grants were added.
- Original signout geometry selector incorrectly assumed aria-label and returned null. Its raw manifest remains unchanged; supplemental actual-name locator verifies alignment. Original event accessible_label null denotes absent explicit aria-label, not an empty computed name; actual accessibility tree recorded in confirmation.
- One permitted correction recaptured four Appearance views after driver focus probe contaminated nav focus modality. Four raw originals, original round2 driver/log and manifest retained and explicitly unscored.
- Real clock live. Initial after Oct27 23:56–23:57 EDT, correction Oct28 after midnight. Calendar display date fixed Oct27; today may roll to Oct28 in supplemental probes. Sign-in times, Today enabled state and time indicator are clock/state changes, not UI improvement.
- 403 responses and associated console messages are expected role-denial probes and preserved raw. Zero raw-console-error claim is not made.
- DOM rectangle visibility helper may count descendants of closed phone disclosure; containment and native disclosure probes establish one navigation region, not that raw descendant visibility count.
- One local standalone origin, production CSP and workers, synthetic fixture. SSO/external feeds/mail/recovery and production integrations not exercised.
- MySlop handoff could not be read/posted: documented curl credential returned unknown or revoked token. A working machine credential is needed for parent to post.
- DOX pass completed for owned capture paths; AGENTS unchanged by explicit instruction and because existing capture ownership/contracts remain valid. Parent owns review inputs, authority, reviews and progression decision.

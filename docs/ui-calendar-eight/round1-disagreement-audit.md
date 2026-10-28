# KyCalendar R1 disagreement audit

**48/48 differences accounted for; 26/26 affected originals reopened individually at original resolution. No scores changed.** Four supplemental group-calendar originals and both pinned KyIdentity brand references were also opened separately. This is a factual/semantic audit of terminal R1 reports, not a new rating round.

Only this Markdown file and [the structured audit](round1-disagreement-audit.json) were written. The JSON retains all 48 original difference records, both reviewers’ 10 original findings and post-freeze factual notes, per-image hashes/dimensions/open logs, and pinned source-blob hashes. Both primary report pairs remain immutable.

| Frozen R1 reviewer | Existing quality | Independent satisfaction | Status |
|---|---|---|---|
| Opus | 3617/480 → 7.5 | false | Below owner’s 8.0 progression gate |
| Sol | 1889/240 → 7.9 | false | Complete; never pooled into Opus |
| Gemini | N/A | N/A | Unused; no retry |

The published Opus settings rows are correct: **127/16 = 7.9375 → 7.9**. Only the terminal settings summary was wrong. Overall **3617/480 → 7.5** remains correct. No cohort, brand, peer or historical averaging changes progression.

## Operational next batch

- **B1 — Phone Week event-label readability:** Both phone Week originals show unrecognizable timed fragments. Desktop Week also shows vertical cropping; cover that shared text treatment without broadening scope. Opus's hide-time/single-line/ellipsis proposal can leave only a few characters at the same column width. Sol's minimum-width contained scroller directly addresses width but is an unvalidated implementation option. Require readable event identity and synchronized header/all-day/timed columns, with no horizontal page overflow; no view replacement or new agenda.
- **B2 — Phone Month identifying labels:** Both phone Month originals show time-only dots and unlabelled all-day bars; baseline CSS deliberately visually hides event titles. This is visible identification loss, not deleted data or proof of missing accessible names. List is already the phone default. Preserve existing views, accessible names, date/event semantics and calendar data colors; replacing times with ellipses alone is not proven sufficient.
- **B3 — Phone header alignment and scrolling navigation:** Day/List/Month/Week place Sign out near x272 while Appearance/group screens place it at the right. The partial Phones & apps label and wrapped timezone are visible. Right alignment and active-item/fade clearance follow from the evidence. The fade is an intentional overflow cue; scrolling/navigation reachability and exact root cause need behavioral verification. Preserve single navigation, order/labels, disclosure and drafts across resize; no second sidebar.
- **B4 — Flatten group-calendar empty state and correct empty-group option:** Four supplemental originals show a dashed box inside the bordered Shared planning card and simultaneously No access yet/No group has access versus Every group has access. Pinned source selects that option for any available.length===0. Distinguish zero groups from all existing groups granted (and preserve error/loading handling). Do not treat the placeholder as an actual grant or access-control failure; captured total group count is not independently proven. Flatten this local empty state; avoid a global component/admin redesign. Correcting this bug is authorized by the user, overriding the raw Opus approval request.
- **B5 — Event-dialog keyboard focus return:** Frozen capture checks record Enter/open and Escape/close success but focus_returned_to_trigger=false in desktop/mobile × light/dark, four probes. Source already captures document.activeElement and calls prev.focus() on cleanup; close also refetches to refresh etags. Missing cleanup is not the diagnosis. DOM replacement or timing is a hypothesis; reproduce before fixing and verify current rendered trigger/fallback with all four probes, preserving refetch. No own browser/test was run in this audit.

Retain the shared Appearance picker, named themes, saved choices/OS following, one navigation region, drafts across resize, authorization, CSP/CSRF and event semantics. No permission request is required: the user already authorized refinement and actual bug fixes, including the misleading group option.

## Factual and semantic resolutions

These dispositions resolve claims; the original numerical disagreements remain published. Each difference below points to one disposition and its own reopened image. Baseline product source was read exclusively with `git show 0f0155027a87f3510740afe1b524a96846002fd3:path`; mutable working source is not audit evidence.

**R1 — Appearance cards and phone navigation are different subregions (2 differences).** Both phone originals show a checked Match system radio plus orange card border; Appearance alone has the orange navigation rail. The strip is scrolled to Two-step verification/Appearance, and the account icon is right-aligned here. The dark Two-step tab has a raised fill, but no active rail. Sol correctly describes card affordances but does not address the shell; Opus correctly notices the shell difference but pixels do not establish two active destinations or missing routes. **Action:** Preserve the successful picker. Refine header/navigation allocation and keep the active item clear; do not add a second sidebar or replace radio cards. Source: `web/src/components/AppHeader.tsx:72-85`; `web/src/styles/theme.css:188-201`.

**R2 — Calendar orange is semantic selection/current-date state (16 differences).** Orange checkboxes visibly select Work/Personal/Projects on desktop; the separate blue/green/plum dots and event fills are calendar data. New event is primary, navigation/view rails mark selection, and orange 27/Tue/header rules identify today in grid views. The pinned product contract expressly documents an accent today header. These are not decorative orange, and the shared design permits accent selection. List has no orange today numeral/header rule: that part of Opus's reused observation does not describe those images. Phone checkboxes are hidden, so Sol references to checked calendars there are contextual rather than visible evidence. **Action:** Preserve orange visibility selections, view/navigation selection, focus, primary actions, and the documented current-date state. Do not recolor user-calendar states or calendar data to satisfy a generic accent penalty. Source: `web/AGENTS.md (calendar chrome contract)`; `web/src/styles/theme.css:290`; `web/src/styles/calendar.css:82-94`.

**R3 — Phone controls are recognizable; header allocation still has a visible defect (4 differences).** Day and default-List phone captures show New event, arrows, Today and view tabs as controls. They also show Phones & apps fading beside Calendars and Sign out around x272 rather than the right edge, unlike Appearance. Content begins around y314; the timezone wraps to a second line. Sol's control facts do not answer Opus's shell observation. Today is correctly disabled while its date range is already visible (recorded capture evidence); a hidden visual label is not an absent accessible name. **Action:** Align account control consistently, improve nav scroll/active-item clearance and compact existing heading/toolbars. Preserve scrollable navigation, Calendars disclosure, and accessible Sign out text. Do not infer unreachable routes or page overflow. Source: `web/src/styles/theme.css:188-201`; `web/src/components/AppHeader.tsx:97-100`; `web/src/styles/calendar.css:132-150`; `manifest-00-before.json (visible_actions_forms)`.

**R4 — Edit dialog danger and Save have distinct roles (4 differences).** In all four edit originals, Delete is labelled, outlined, separated at the left; Save is filled and at the right, with Cancel beside it. Dark salmon Delete and peach Save are neighboring warm hues, so Opus's perceptual concern is understandable, but 'near-identical' does not establish action ambiguity or an accent violation. Light screenshots visibly use red Delete versus burnt-orange Save; invoking the dark hue in their observation is a theme carry-over. Token targets separate danger (#ff8176/#b42318) and accent (#f5865f/#bf3f18); source uses var(--danger), not var(--accent), for Delete. No computed color/contrast sweep was performed. **Action:** Preserve destructive semantics and separate action geometry. These screenshots do not require a danger recolor or establish a contrast failure/pass. Source: `runtime-tokens.css (danger and accent values)`; `web/src/styles/theme.css:258-259`.

**R5 — Native fields and create/edit field sets do not establish incoherence (8 differences).** All eight editor originals share labelled outlined fields, consistent corners, a native all-day checkbox, date widgets and a native select. New shows Calendar; Edit shows Repeat. Source deliberately shows Calendar only for create and Repeat only for all-scope edits; new events begin with scope=this. The native date selection/focus indicator is explicitly permitted by the design contract. Sol's common-geometry observation is visible; Opus's differing-field facts are also visible, but the inference that these require identical forms or replacement controls is unsupported. **Action:** Retain native controls and current create/edit semantics; no new Repeat-on-create feature or field-set parity work follows from this disagreement. Source: `web/src/components/EventDialog.tsx:38`; `web/src/components/EventDialog.tsx:163-193`; `design-contract.md (Focus; Forms and dialogs)`.

**R6 — Danger hue is not extra decorative accent (4 differences).** In each edit original the foreground accent identifies focused Title and filled Save. Delete is a separate labelled danger outline at the opposite end. Opus already calls it danger; hue proximity does not change its role into a second primary or decoration. Background selected calendars/navigation remain dimmed by the modal. **Action:** Keep destructive Delete distinct by text, outline and position; preserve focus and Save accent. No accent-removal change follows. Source: `web/src/styles/theme.css:258-259`; `runtime-tokens.css`.

**R7 — Blue date-segment selection is real native capture state (8 differences).** Every New event original visibly shows blue selection on the leading 10 (the month segment of 10/27/2026) inside the End field and an orange/peach outer focus ring. Sol's foreground-separation statement is true but omits that blue; Opus sees it correctly. The driver fills Start then End; this is native date-widget selection, not a custom off-palette surface or wrong initial autofocus. The product effect focuses Title on opening. The rubric scores artifacts as captured, so neither frozen score is erased. **Action:** Keep the capture caveat outside arrays. Do not replace date widgets, recolor native selection, or change initial focus to chase a palette score. Source: `web/src/components/EventDialog.tsx:53-60`; `capture.mjs:182-183`; `web/src/styles/theme.css:285-287`; `design-contract.md (native focus indicator allowed)`.

**R8 — Phone Week event geometry is materially unreadable (2 differences).** Both 390px Week originals show seven columns plus the hour gutter, with Design review reduced to Desi/revie, Morning appointment to Morr, and Lunch break to Lunc. All-day labels use ellipses, while timed text is arbitrarily cropped. The timezone/header crowding Opus cites is real but does not account for the dominant event-identity defect Sol describes. Regular toolbar heights do not make narrow event labels readable. Both raw judgments remain; no compromise rating is assigned. **Action:** Make existing Week titles meaningfully identifiable. Ellipsis/padding/time suppression may reduce clipping but cannot be assumed to solve ~43px columns; a contained schedule scroller/minimum column width is a candidate, not an accepted fix. Preserve list default and all views. Source: `web/src/styles/calendar.css:98-104`; `web/src/pages/CalendarPage.tsx:295-305`.

## Complete difference ledger

Values are copied from the parent record, **Opus / Sol**, unchanged. Q = quality; B = brand. Full original observations are preserved verbatim in JSON under `original_parent_difference`; factual qualifications stay outside the raw report arrays.

| ID | Original | Dimension | Raw values | Resolution |
|---|---|---|---|---|
| D01 | [appearance-mobile-dark](screenshots/kycalendar-00-before-appearance-mobile-dark.png) | Q.visible_affordances | 6 / 9 | R1 |
| D02 | [appearance-mobile-light](screenshots/kycalendar-00-before-appearance-mobile-light.png) | Q.visible_affordances | 7 / 9 | R1 |
| D03 | [day-desktop-dark](screenshots/kycalendar-00-before-day-desktop-dark.png) | B.accent_discipline | 7 / 9 | R2 |
| D04 | [day-desktop-light](screenshots/kycalendar-00-before-day-desktop-light.png) | B.accent_discipline | 7 / 9 | R2 |
| D05 | [day-mobile-dark](screenshots/kycalendar-00-before-day-mobile-dark.png) | Q.visible_affordances | 6 / 8 | R3 |
| D06 | [day-mobile-dark](screenshots/kycalendar-00-before-day-mobile-dark.png) | B.accent_discipline | 7 / 9 | R2 |
| D07 | [day-mobile-light](screenshots/kycalendar-00-before-day-mobile-light.png) | Q.visible_affordances | 6 / 8 | R3 |
| D08 | [day-mobile-light](screenshots/kycalendar-00-before-day-mobile-light.png) | B.accent_discipline | 7 / 9 | R2 |
| D09 | [default-phone-list-mobile-dark](screenshots/kycalendar-00-before-default-phone-list-mobile-dark.png) | Q.visible_affordances | 6 / 8 | R3 |
| D10 | [default-phone-list-mobile-dark](screenshots/kycalendar-00-before-default-phone-list-mobile-dark.png) | B.accent_discipline | 7 / 9 | R2 |
| D11 | [default-phone-list-mobile-light](screenshots/kycalendar-00-before-default-phone-list-mobile-light.png) | Q.visible_affordances | 6 / 8 | R3 |
| D12 | [default-phone-list-mobile-light](screenshots/kycalendar-00-before-default-phone-list-mobile-light.png) | B.accent_discipline | 7 / 9 | R2 |
| D13 | [existing-event-desktop-dark](screenshots/kycalendar-00-before-existing-event-desktop-dark.png) | Q.color | 7 / 9 | R4 |
| D14 | [existing-event-desktop-dark](screenshots/kycalendar-00-before-existing-event-desktop-dark.png) | Q.coherence | 7 / 9 | R5 |
| D15 | [existing-event-desktop-dark](screenshots/kycalendar-00-before-existing-event-desktop-dark.png) | B.accent_discipline | 7 / 9 | R6 |
| D16 | [existing-event-desktop-light](screenshots/kycalendar-00-before-existing-event-desktop-light.png) | Q.color | 7 / 9 | R4 |
| D17 | [existing-event-desktop-light](screenshots/kycalendar-00-before-existing-event-desktop-light.png) | Q.coherence | 7 / 9 | R5 |
| D18 | [existing-event-desktop-light](screenshots/kycalendar-00-before-existing-event-desktop-light.png) | B.accent_discipline | 7 / 9 | R6 |
| D19 | [existing-event-mobile-dark](screenshots/kycalendar-00-before-existing-event-mobile-dark.png) | Q.color | 7 / 9 | R4 |
| D20 | [existing-event-mobile-dark](screenshots/kycalendar-00-before-existing-event-mobile-dark.png) | Q.coherence | 7 / 9 | R5 |
| D21 | [existing-event-mobile-dark](screenshots/kycalendar-00-before-existing-event-mobile-dark.png) | B.accent_discipline | 7 / 9 | R6 |
| D22 | [existing-event-mobile-light](screenshots/kycalendar-00-before-existing-event-mobile-light.png) | Q.color | 7 / 9 | R4 |
| D23 | [existing-event-mobile-light](screenshots/kycalendar-00-before-existing-event-mobile-light.png) | Q.coherence | 7 / 9 | R5 |
| D24 | [existing-event-mobile-light](screenshots/kycalendar-00-before-existing-event-mobile-light.png) | B.accent_discipline | 7 / 9 | R6 |
| D25 | [list-desktop-dark](screenshots/kycalendar-00-before-list-desktop-dark.png) | B.accent_discipline | 7 / 9 | R2 |
| D26 | [list-desktop-light](screenshots/kycalendar-00-before-list-desktop-light.png) | B.accent_discipline | 7 / 9 | R2 |
| D27 | [month-desktop-dark](screenshots/kycalendar-00-before-month-desktop-dark.png) | B.accent_discipline | 7 / 9 | R2 |
| D28 | [month-desktop-light](screenshots/kycalendar-00-before-month-desktop-light.png) | B.accent_discipline | 7 / 9 | R2 |
| D29 | [month-mobile-dark](screenshots/kycalendar-00-before-month-mobile-dark.png) | B.accent_discipline | 7 / 9 | R2 |
| D30 | [month-mobile-light](screenshots/kycalendar-00-before-month-mobile-light.png) | B.accent_discipline | 7 / 9 | R2 |
| D31 | [new-event-desktop-dark](screenshots/kycalendar-00-before-new-event-desktop-dark.png) | Q.color | 7 / 9 | R7 |
| D32 | [new-event-desktop-dark](screenshots/kycalendar-00-before-new-event-desktop-dark.png) | Q.coherence | 7 / 9 | R5 |
| D33 | [new-event-desktop-dark](screenshots/kycalendar-00-before-new-event-desktop-dark.png) | B.palette_tokens | 7 / 9 | R7 |
| D34 | [new-event-desktop-light](screenshots/kycalendar-00-before-new-event-desktop-light.png) | Q.color | 7 / 9 | R7 |
| D35 | [new-event-desktop-light](screenshots/kycalendar-00-before-new-event-desktop-light.png) | Q.coherence | 7 / 9 | R5 |
| D36 | [new-event-desktop-light](screenshots/kycalendar-00-before-new-event-desktop-light.png) | B.palette_tokens | 7 / 9 | R7 |
| D37 | [new-event-mobile-dark](screenshots/kycalendar-00-before-new-event-mobile-dark.png) | Q.color | 7 / 9 | R7 |
| D38 | [new-event-mobile-dark](screenshots/kycalendar-00-before-new-event-mobile-dark.png) | Q.coherence | 7 / 9 | R5 |
| D39 | [new-event-mobile-dark](screenshots/kycalendar-00-before-new-event-mobile-dark.png) | B.palette_tokens | 7 / 9 | R7 |
| D40 | [new-event-mobile-light](screenshots/kycalendar-00-before-new-event-mobile-light.png) | Q.color | 7 / 9 | R7 |
| D41 | [new-event-mobile-light](screenshots/kycalendar-00-before-new-event-mobile-light.png) | Q.coherence | 7 / 9 | R5 |
| D42 | [new-event-mobile-light](screenshots/kycalendar-00-before-new-event-mobile-light.png) | B.palette_tokens | 7 / 9 | R7 |
| D43 | [week-desktop-dark](screenshots/kycalendar-00-before-week-desktop-dark.png) | B.accent_discipline | 7 / 9 | R2 |
| D44 | [week-desktop-light](screenshots/kycalendar-00-before-week-desktop-light.png) | B.accent_discipline | 7 / 9 | R2 |
| D45 | [week-mobile-dark](screenshots/kycalendar-00-before-week-mobile-dark.png) | B.type_geometry | 6 / 4 | R8 |
| D46 | [week-mobile-dark](screenshots/kycalendar-00-before-week-mobile-dark.png) | B.accent_discipline | 7 / 9 | R2 |
| D47 | [week-mobile-light](screenshots/kycalendar-00-before-week-mobile-light.png) | B.type_geometry | 6 / 4 | R8 |
| D48 | [week-mobile-light](screenshots/kycalendar-00-before-week-mobile-light.png) | B.accent_discipline | 7 / 9 | R2 |

## Original-resolution opening log

Each row is a separate `view_image(detail="original")` call on the original PNG: no montage, crop or resize. Order below is the actual affected-image opening order. Full hashes and visible per-image observations are in JSON.

| # | Original opened | Pixels | SHA-256 prefix |
|---:|---|---|---|
| 1 | [appearance-mobile-dark](screenshots/kycalendar-00-before-appearance-mobile-dark.png) | 390×844 | `b20fe2e52c870c42` |
| 2 | [appearance-mobile-light](screenshots/kycalendar-00-before-appearance-mobile-light.png) | 390×844 | `e09ec8eb31549e60` |
| 3 | [day-desktop-dark](screenshots/kycalendar-00-before-day-desktop-dark.png) | 1280×900 | `497bd1713f1fe939` |
| 4 | [day-desktop-light](screenshots/kycalendar-00-before-day-desktop-light.png) | 1280×900 | `34a03d0a2aee8f16` |
| 5 | [day-mobile-dark](screenshots/kycalendar-00-before-day-mobile-dark.png) | 390×844 | `e503202569c3e367` |
| 6 | [day-mobile-light](screenshots/kycalendar-00-before-day-mobile-light.png) | 390×844 | `af749f175745eb86` |
| 7 | [default-phone-list-mobile-dark](screenshots/kycalendar-00-before-default-phone-list-mobile-dark.png) | 390×844 | `5611cea03c6fbda6` |
| 8 | [default-phone-list-mobile-light](screenshots/kycalendar-00-before-default-phone-list-mobile-light.png) | 390×844 | `797f4ab1af744b97` |
| 9 | [existing-event-desktop-dark](screenshots/kycalendar-00-before-existing-event-desktop-dark.png) | 1280×900 | `d0f3835470fa02f3` |
| 10 | [existing-event-desktop-light](screenshots/kycalendar-00-before-existing-event-desktop-light.png) | 1280×900 | `627c5b7d96823850` |
| 11 | [existing-event-mobile-dark](screenshots/kycalendar-00-before-existing-event-mobile-dark.png) | 390×844 | `370aabaa2ea1a948` |
| 12 | [existing-event-mobile-light](screenshots/kycalendar-00-before-existing-event-mobile-light.png) | 390×844 | `86e237d61482fc9b` |
| 13 | [list-desktop-dark](screenshots/kycalendar-00-before-list-desktop-dark.png) | 1280×900 | `b92e394cc8c982c7` |
| 14 | [list-desktop-light](screenshots/kycalendar-00-before-list-desktop-light.png) | 1280×900 | `a9a8d9c55785ba9b` |
| 15 | [month-desktop-dark](screenshots/kycalendar-00-before-month-desktop-dark.png) | 1280×900 | `7b073f252a70d9e5` |
| 16 | [month-desktop-light](screenshots/kycalendar-00-before-month-desktop-light.png) | 1280×900 | `58c1aa6eee330029` |
| 17 | [month-mobile-dark](screenshots/kycalendar-00-before-month-mobile-dark.png) | 390×844 | `f77733da1a540079` |
| 18 | [month-mobile-light](screenshots/kycalendar-00-before-month-mobile-light.png) | 390×844 | `58cd3ed22aaed9f0` |
| 19 | [new-event-desktop-dark](screenshots/kycalendar-00-before-new-event-desktop-dark.png) | 1280×900 | `c97e5cf8df9d38a0` |
| 20 | [new-event-desktop-light](screenshots/kycalendar-00-before-new-event-desktop-light.png) | 1280×900 | `87865cc37f7f2f42` |
| 21 | [new-event-mobile-dark](screenshots/kycalendar-00-before-new-event-mobile-dark.png) | 390×844 | `7636b2bd81fc0d91` |
| 22 | [new-event-mobile-light](screenshots/kycalendar-00-before-new-event-mobile-light.png) | 390×844 | `4bd1ed8fb26914ce` |
| 23 | [week-desktop-dark](screenshots/kycalendar-00-before-week-desktop-dark.png) | 1280×900 | `9922335a70bd79a4` |
| 24 | [week-desktop-light](screenshots/kycalendar-00-before-week-desktop-light.png) | 1280×900 | `303281e6114fddf3` |
| 25 | [week-mobile-dark](screenshots/kycalendar-00-before-week-mobile-dark.png) | 390×844 | `bb0dcdccbcf5b124` |
| 26 | [week-mobile-light](screenshots/kycalendar-00-before-week-mobile-light.png) | 390×844 | `f7c2dbfdb5e8d022` |

Separately opened after these 26: [admin-group-calendars-desktop-dark](screenshots/kycalendar-00-before-admin-group-calendars-desktop-dark.png), [admin-group-calendars-desktop-light](screenshots/kycalendar-00-before-admin-group-calendars-desktop-light.png), [admin-group-calendars-mobile-dark](screenshots/kycalendar-00-before-admin-group-calendars-mobile-dark.png), [admin-group-calendars-mobile-light](screenshots/kycalendar-00-before-admin-group-calendars-mobile-light.png).

Brand-only originals opened first: [KyIdentity light](brand-reference-kyidentity-appearance-desktop-light.png) and [KyIdentity dark](brand-reference-kyidentity-appearance-desktop-dark.png). Both are 1280×900, full resolution; neither was quality-scored.

## Material qualifications

- **Filled edit metadata:** All four existing-event originals are filled Design review edit forms over a populated List. Inventory/raw state='empty' is retained unchanged. Opus already records this in observed_state_note; use the filled-edit clarification outside both arrays when interpreting density/empty-state quality.
- **Today button:** The recorded action metadata marks Today disabled while today is inside the displayed range. It looks disabled because it is disabled. Do not turn it into a functional defect; explanatory styling is at most a separate judgment.
- **Native date state:** The blue 10 selection is the month segment, not evidence that the 10:00 hour itself is selected. It was produced after the capture driver's End fill, while source initial autofocus targets Title. Native indicators are allowed; the artifact remains part of the original appearance judgment.
- **Semantic accent and themes:** Keep selected orange calendar visibility states and documented today markers; blue/green/plum event hues and theme-preview swatches are content. Do not apply dark danger-hue criticism as if it describes a light screenshot, or repeat today-grid marks on List where they are absent.
- **Limits of pixels and static source:** No screenshot passes complete keyboard/focus/contrast/states/responsive/integration/security gates. Accessible Sign out text exists in the DOM but is visually hidden on phones. Source retains Month title text; that is not a full screen-reader acceptance claim. Capture-worker measurements remain external recorded evidence.
- **Recommendation scope/count:** Opus's '12 affected originals' recapture shorthand is not a valid filename manifest: four phone views in two themes plus desktop Week and group calendars already total 14, before separate header examples. Parent should enumerate actual filenames and duplicate handling. This audit's 26 discrepancy originals are an audit set, not a new capture/scoring cohort.
- **Sparse calendar density:** Earlier empty Month weeks reflect the fixture/date distribution. Empty date space alone is not a density bug under the state-aware rubric; missing identifying labels and clipping are the actionable evidence.

## Integrity and verification

All four raw files matched the parent’s hashes before inspection and after publishing this audit. Canonical frozen arrays use `json.dumps(files,ensure_ascii=False,sort_keys=True,separators=(',',':'),allow_nan=False).encode('utf-8')`, no trailing newline.

| Raw report | SHA-256 before = after |
|---|---|
| [opus-round1.json](opus-round1.json) | `7d5ad791a69b9cc35cd80fed25da5370456e518c52ead7d6d87867bc1e59a84e` |
| [opus-round1.md](opus-round1.md) | `a17b69d642dc1f53d7afdfa7436638900f58a85c10630e5a9acfecbd6f688586` |
| [sol-round1.json](sol-round1.json) | `730488d9d45ec4dcce63e5ede8af37924894d3bf8ff56690386763fcf51d45e0` |
| [sol-round1.md](sol-round1.md) | `9d034eaff007ec0c58fa8ea5f43a3365766a5858a5db58ce6b4f820d6cc40457` |

| Frozen entire files array | SHA-256 before = after | UTF-8 bytes |
|---|---|---:|
| opus | `3576acfb8e039337529c19c4a73d89bf3a74c6248424e85ea770ee4f1016e0c5` | 102803 |
| sol | `a7e63724cf2bed6b5a9991c2c47512dcbaf02fb80e6febf61cfb6b3fab7a715b` | 191970 |

Parent record SHA-256: `696e65b3bcd11cb0f18d6c0ceeba87605efc1df7171382a05b2f2d1041ea9d8e`. Seven review-input pins matched, including the pinned rubric, design contract and runtime tokens. Forty-eight unique candidate image identities plus the two brand references matched their hashes, byte counts and PNG dimensions. The structured audit records eight source blobs pinned to the baseline commit.

**Not run:** build, browser, application tests, subdelegation or commit, per assignment. The keyboard failure is capture-worker evidence; no full accessibility, behavior, integration or security acceptance follows from pixels or this static audit. Parent/owning AGENTS remain unchanged because this evidence pair changes no durable behavior, ownership or workflow. Only the two requested audit files were written.

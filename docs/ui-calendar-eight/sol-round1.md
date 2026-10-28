# KyCalendar round 1 — independent Sol screenshot review

**Quality 1889/240 = 7.8708(3) → 7.9/10; independent satisfaction: false.**

- Reviewed 48 unique current originals once, individually at full original resolution; verified hashes, byte counts and dimensions for all 50 current PNGs plus 2 brand references.
- Busnes palette, one navigation region and shared Appearance are cohesive. Phone Month/Week lose readable event identity.
- Complete vectors, observations and identities are frozen in [sol-round1.json](sol-round1.json). Later facts stay outside its `files` array.
- **Review this:** [phone Week](screenshots/kycalendar-00-before-week-mobile-light.png), [phone Month](screenshots/kycalendar-00-before-month-mobile-dark.png) and the [recorded focus-return failure](capture-checks.md#behavior-gates).
- **Checked:** input pins, 52 image identities, all 48 inventory members, integer vectors, exact weights/totals/means and canonical freeze. **Not checked by Sol:** browser interaction, builds, application tests or whole behavior/production acceptance; explicitly outside this assignment.
- **You need to:** nothing for this review artifact. The parent applies the independent Opus progression result and may take the bounded implementation batch below.

## Scope and progression

Reviewer: `gpt-6.1-sol`. Round 1, fresh `00-before` baseline. Worktree: `/home/yoshi/git/busnes.app/KyCalendar/.claude/worktrees/calendar-eight`; branch `ui/calendar-eight`; captured runtime `0f0155027a87f3510740afe1b524a96846002fd3`. Runtime source binding: `09ee05752478ce7384e37a790f282cb002bb2e51e6fbe9c00012930e2c6f8101`.

Progression uses **only current-round Opus overall quality**, the mean of unique rubric-rounded file totals, HALF_UP one decimal: ≥8.0 accepts. Sol, brand, history and individual cohort shortfalls are not pooled into or allowed to veto that gate. Owner rejection reopens it. 9.0 remains the longer-term goal. My independent dissatisfaction concerns substantial phone grid legibility defects; it is separate from progression.

| Reviewer | Current-round result | Status |
|---|---|---|
| Sol | 1889/240 = 7.8708(3) → 7.9; satisfaction false | Complete and frozen |
| Opus | Not inspected | Independent current-round result unknown to this reviewer |
| Gemini | N/A | Unused; known quota exhausted; no request/retry |

No competitors were captured. Cohort coverage is **not a ranking**. No peer/prior review, own current-pass prior or historical quality image was read; `opus-round1.*` was never read.

## Blind freeze and provenance

- Freeze time: `2026-10-28T03:29:55.174831+00:00`.
- Canonical entire-array SHA-256: `a7e63724cf2bed6b5a9991c2c47512dcbaf02fb80e6febf61cfb6b3fab7a715b`.
- Canonical UTF-8 length: **191,970 bytes**, no trailing newline.
- Canonicalization: `json.dumps(files,ensure_ascii=False,sort_keys=True,separators=(',',':'),allow_nan=False).encode('utf-8')`.
- `freeze.canonical_files_utf8` contains the exact canonical array string. Both canonical bytes and the original pretty-printed `files` array literal were preserved after freeze.
- Frozen before source, capture checks, parent audits, peer/prior reviews or historical quality images. Permitted KyIdentity brand references were viewed before brand scoring and never quality scored.
- **No pre-freeze recalibration.** Every image received one vector on its first individual original-resolution viewing. No provisional aggregate was calculated until all 48 vectors existed; no threshold-directed re-reading or adjustment occurred.

Allowed blind inputs: review-inputs-v1.json, authority.json and pinned rubric/design contract/runtime tokens, inventory-v1.json, artifacts-v1.json, manifest-00-before.json, current originals and two brand references. Root/worktree/owning AGENTS.md contracts and applicable skills were read. Source and capture checks were read only after freeze; the TypeScript skill governed the source cross-check.

| Input pin | SHA-256 | Bytes |
|---|---|---:|
| [authority.json](authority.json) | `2c4abf7b3d659eb16e2ba19095907ede6b8720bf80e06de82b55dcb3e8133e49` | 1398 |
| [rubric.md](rubric.md) | `ad3516620420fbdc30f5273f82b7c72c76e1365d1b6296fcaa56be3cbebf2c41` | 7817 |
| [design-contract.md](design-contract.md) | `392afb88c51fb3cfad6c94d6b841804b194d107a0431c31f35ff81d3a7a444d3` | 6593 |
| [runtime-tokens.css](runtime-tokens.css) | `636fcf04ce783edd7625b5265f63abb8b3abd9fae78ee653b1a2ea3142f235f4` | 3980 |
| [inventory-v1.json](inventory-v1.json) | `dbd4250c021c882db41ed3f59b38ddfc14325dc3367820b3303609a04a38937f` | 33115 |
| [artifacts-v1.json](artifacts-v1.json) | `98dab08b6bf04dc67528112ae33c1b353f4d582c57c292f577b872af4030d9ae` | 247039 |
| [manifest-00-before.json](manifest-00-before.json) | `86ddf34e219b342c48287b1a0dd76715d15a04a38476a3ff77022210c4ec1225` | 657323 |

Review-inputs SHA-256: `f7e408267454203c8760989542a80fe036f5e2ec997e68e4e411e17e061e7021`.

Recorded capture conditions: Chromium 153.0.8010.12 / Playwright 1.63.0; desktop 1280×900 and phone 390×844, DPR1; OS light/dark, no saved theme; en-US, America/New_York; viewport-only originals without cropping/resizing. Display date October 27, 2026; month October 2026; Week/List October 25–31. Real clock unchanged. Capture worker reused committed embedded dist, allowed the real service worker, awaited font readiness and used synthetic local everyday/admin accounts. These are recorded conditions, not browser work by Sol.

## Arithmetic

Pinned `ky-suite-v2` weights: hierarchy .20, typography .15, density_layout .20, color .15, visible_affordances .15, coherence .15. Dimensions are integers 0–10. Each exact weighted file sum is Decimal ROUND_HALF_UP to one decimal. Overall is the equal-weight mean of those 48 **rounded file totals**.

- Sum of rubric-rounded totals: **377.8 = 1889/5**.
- Quality overall: **377.8 / 48 = 1889/240 = 7.8708(3) → 7.9**.
- Separately, unrounded-vector mean: **3769/480 = 7.85208(3) → 7.9**.
- Brand uses equal weights among numeric available dimensions per file. Signed-out Login excludes `shell` (N/A) rather than zero. Mean of the 48 exact per-file brand means: **25/3 = 8.(3) → 8.3**. Never added to quality.
- Suite cohesion has two-product evidence: KyCalendar and KyIdentity brand-only references. References never enter quality.
- Parenthesized digits repeat forever: `7.8708(3)` means `7.870833333…`. JSON retains exact numerator/denominator and 90-significant-digit rendering. Group means are descriptive, never used to reconstruct overall from rounded group values.

### Per surface — not a ranking

| Group | n | Sum rounded quality | Exact quality mean | Full exact decimal | HALF_UP 1dp | Exact brand mean |
|---|---:|---:|---|---|---:|---|
| admin-group-calendars | 4 | 29.6 | 37/5 | 7.4 | 7.4 | 42/5 |
| admin-overview | 4 | 31.6 | 79/10 | 7.9 | 7.9 | 42/5 |
| admin-people | 4 | 31.2 | 39/5 | 7.8 | 7.8 | 42/5 |
| appearance | 4 | 34.8 | 87/10 | 8.7 | 8.7 | 44/5 |
| day | 4 | 31.4 | 157/20 | 7.85 | 7.9 | 83/10 |
| existing-event | 4 | 34.8 | 87/10 | 8.7 | 8.7 | 42/5 |
| list | 4 | 31.6 | 79/10 | 7.9 | 7.9 | 83/10 |
| login | 4 | 34 | 17/2 | 8.5 | 8.5 | 17/2 |
| month | 4 | 28 | 7 | 7 | 7.0 | 8 |
| new-event | 4 | 33.2 | 83/10 | 8.3 | 8.3 | 42/5 |
| phones-and-apps | 4 | 31.2 | 39/5 | 7.8 | 7.8 | 42/5 |
| week | 4 | 26.4 | 33/5 | 6.6 | 6.6 | 77/10 |

### Per cohort — not a ranking

| Group | n | Sum rounded quality | Exact quality mean | Full exact decimal | HALF_UP 1dp | Exact brand mean |
|---|---:|---:|---|---|---:|---|
| admin-console | 12 | 92.4 | 77/10 | 7.7 | 7.7 | 42/5 |
| auth | 4 | 34 | 17/2 | 8.5 | 8.5 | 17/2 |
| calendar-grid | 12 | 85.8 | 143/20 | 7.15 | 7.2 | 8 |
| calendar-list | 4 | 31.6 | 79/10 | 7.9 | 7.9 | 83/10 |
| event-editor | 8 | 68 | 17/2 | 8.5 | 8.5 | 42/5 |
| settings | 8 | 66 | 33/4 | 8.25 | 8.3 | 43/5 |

### Per theme — not a ranking

| Group | n | Sum rounded quality | Exact quality mean | Full exact decimal | HALF_UP 1dp | Exact brand mean |
|---|---:|---:|---|---|---:|---|
| dark | 24 | 188.9 | 1889/240 | 7.8708(3) | 7.9 | 25/3 |
| light | 24 | 188.9 | 1889/240 | 7.8708(3) | 7.9 | 25/3 |

### Per device — not a ranking

| Group | n | Sum rounded quality | Exact quality mean | Full exact decimal | HALF_UP 1dp | Exact brand mean |
|---|---:|---:|---|---|---:|---|
| desktop | 24 | 194 | 97/12 | 8.08(3) | 8.1 | 1009/120 |
| mobile | 24 | 183.8 | 919/120 | 7.658(3) | 7.7 | 991/120 |

### Per device/theme — not a ranking

| Group | n | Sum rounded quality | Exact quality mean | Full exact decimal | HALF_UP 1dp | Exact brand mean |
|---|---:|---:|---|---|---:|---|
| desktop-light | 12 | 97 | 97/12 | 8.08(3) | 8.1 | 1009/120 |
| desktop-dark | 12 | 97 | 97/12 | 8.08(3) | 8.1 | 1009/120 |
| mobile-light | 12 | 91.9 | 919/120 | 7.658(3) | 7.7 | 991/120 |
| mobile-dark | 12 | 91.9 | 919/120 | 7.658(3) | 7.7 | 991/120 |

Default-phone-list originals visibly show List, so they join desktop List in the semantic surface mean: four unique candidates per each of 12 surfaces. Full per-state and unrounded-vector group arithmetic remains in JSON. No individual shortfall vetoes the Opus overall rule.

## Findings

### S1 — Closing the event dialog loses the event trigger focus in the recorded probes. (high)

The capture worker recorded Enter opening a real event and Escape closing it, with focus_returned_to_trigger=false in desktop/light, mobile/light, desktop/dark and mobile/dark. Screenshots alone do not reveal this failure.

Images: [kycalendar-00-before-existing-event-mobile-light.png](screenshots/kycalendar-00-before-existing-event-mobile-light.png), [kycalendar-00-before-existing-event-desktop-dark.png](screenshots/kycalendar-00-before-existing-event-desktop-dark.png).

Post-freeze sources: [web/src/components/EventDialog.tsx:53](../../web/src/components/EventDialog.tsx#L53), [web/src/pages/CalendarPage.tsx:147](../../web/src/pages/CalendarPage.tsx#L147), [docs/ui-calendar-eight/capture-checks.md:38](../../docs/ui-calendar-eight/capture-checks.md#L38).

Bounded fix: Retain the triggering event identity across close/refetch and restore focus to its current rendered control, with a stable calendar fallback if it no longer exists. Preserve the stale-etag refetch. Confirm the four existing real-server probes.

Evidence limit: Recorded capture-worker failure, not an interaction run by Sol. The existing cleanup already calls prev.focus(); the specific replacement/timing cause is a hypothesis requiring reproduction. Quality vectors were frozen before this evidence was read.

### S2 — Phone Week clips recognizable event names into fragments. (high)

At 390px, the seven-column Week shows partial names such as Desi/revie, Morr and Lunc. The purple workshop block also cuts text at its boundaries. The screen cannot be scanned for event identity.

Images: [kycalendar-00-before-week-mobile-light.png](screenshots/kycalendar-00-before-week-mobile-light.png), [kycalendar-00-before-week-mobile-dark.png](screenshots/kycalendar-00-before-week-mobile-dark.png), [kycalendar-00-before-week-desktop-light.png](screenshots/kycalendar-00-before-week-desktop-light.png).

Post-freeze sources: [web/src/styles/calendar.css:98](../../web/src/styles/calendar.css#L98), [web/src/styles/calendar.css:102](../../web/src/styles/calendar.css#L102), [web/src/pages/CalendarPage.tsx:300](../../web/src/pages/CalendarPage.tsx#L300).

Bounded fix: Keep the existing Week view and use readable minimum day-column widths within a contained horizontal schedule scroller at phone size; keep headers/all-day/timed columns synchronized and the page itself free of horizontal overflow. Use deliberate truncation where a complete title cannot fit instead of arbitrary character fragments.

Evidence limit: This is a visual recommendation for the existing view, not a new agenda or feature. Its scrolling, touch targets and accessibility require real-browser verification.

### S3 — Phone Month represents events without visible titles. (medium)

The populated final week shows dots with abbreviated times and blank colored all-day bars. Library day and Release preparation cannot be identified from their bars. Desktop Month also truncates several longer event names.

Images: [kycalendar-00-before-month-mobile-light.png](screenshots/kycalendar-00-before-month-mobile-light.png), [kycalendar-00-before-month-mobile-dark.png](screenshots/kycalendar-00-before-month-mobile-dark.png), [kycalendar-00-before-month-desktop-dark.png](screenshots/kycalendar-00-before-month-desktop-dark.png).

Post-freeze sources: [web/src/styles/calendar.css:152](../../web/src/styles/calendar.css#L152), [web/src/styles/calendar.css:154](../../web/src/styles/calendar.css#L154), [web/src/pages/CalendarPage.tsx:303](../../web/src/pages/CalendarPage.tsx#L303).

Bounded fix: Revisit the existing phone Month presentation so event labels remain visibly meaningful, for example with readable minimum cell widths and a contained horizontal grid. Retain event accessible names and the existing List default. Confirm all-day bars carry a visible identifying label.

Evidence limit: Post-freeze source confirms titles are intentionally visually hidden and retained in accessible names. This is not a claim that the event data or a screen-reader label is missing; List remains the phone default.

### S4 — Phone calendar controls consume substantial height and reveal only part of the next navigation label. (medium)

The calendar grid starts around y=314 at 390x844 after the brand/navigation, title/timezone, date controls and view tabs. Phones & apps is cut/faded beside the Calendars disclosure.

Images: [kycalendar-00-before-day-mobile-light.png](screenshots/kycalendar-00-before-day-mobile-light.png), [kycalendar-00-before-default-phone-list-mobile-dark.png](screenshots/kycalendar-00-before-default-phone-list-mobile-dark.png).

Post-freeze sources: [web/src/styles/theme.css:192](../../web/src/styles/theme.css#L192), [web/src/styles/theme.css:196](../../web/src/styles/theme.css#L196), [web/src/styles/calendar.css:132](../../web/src/styles/calendar.css#L132), [web/src/pages/CalendarPage.tsx:296](../../web/src/pages/CalendarPage.tsx#L296).

Bounded fix: Reflow existing calendar heading/date/view controls to reduce stacked height and give the scrolling navigation a clearer visible endpoint. Preserve labels, the Calendars disclosure, the single navigation region and all existing routes; do not add a second sidebar.

Evidence limit: The fade is a deliberate overflow cue, not proof of unreachable links or horizontal page overflow. Full navigation usability was not exercised by Sol.

### S5 — The no-access calendar simultaneously says Every group has access. (medium)

Shared planning displays No access yet and No group has access, but its Group select displays Every group has access. This presents contradictory permission status at a glance.

Images: [kycalendar-00-before-admin-group-calendars-desktop-light.png](screenshots/kycalendar-00-before-admin-group-calendars-desktop-light.png), [kycalendar-00-before-admin-group-calendars-mobile-dark.png](screenshots/kycalendar-00-before-admin-group-calendars-mobile-dark.png).

Post-freeze sources: [web/src/pages/GroupCalendars.tsx:155](../../web/src/pages/GroupCalendars.tsx#L155), [web/src/pages/GroupCalendars.tsx:173](../../web/src/pages/GroupCalendars.tsx#L173), [web/src/pages/GroupCalendars.tsx:207](../../web/src/pages/GroupCalendars.tsx#L207).

Bounded fix: Distinguish no groups exist from all existing groups already granted. Show accurate guidance toward the existing Groups page in the zero-groups state and make the unavailable Add group state clear, while preserving all authorization rules.

Evidence limit: Source chooses the placeholder whenever available.length is zero, which includes zero total groups. This is a presentation defect; the image does not prove an access-control failure.

## One feasible next batch

**Existing calendar phone readability and event-close focus.**

- Fix event-dialog focus return across existing close/refetch.
- Refine existing phone Month/Week grids for readable event identity using contained width and deliberate truncation; preserve List default and all existing views.
- Reflow existing phone calendar heading/toolbars and the navigation/disclosure allocation without adding a second navigation region.

Keep this within existing calendar views and the event-close path. Defer group-calendar state copy to a separate small follow-up; preserve the successful Appearance picker. No new feature, route or dependency. Preserve List default, all views and accessible names, named themes, saved choices/OS following, draft persistence, everyday/admin separation, permissions, event semantics, CSRF and CSP.

- Matched before/after originals for Month, Week, Day, List and new/existing event dialogs at 1280x900 and 390x844 in both themes, using the same fixture/date/storage conditions.
- One bounded inspection/fix confirmation; record remaining defects rather than threshold-directed repeated rounds.
- Confirm event trigger focus after Enter/open and Escape/close for all four size/theme conditions against the real server; preserve stale-etag refresh behavior.
- Check inner schedule scrolling, aligned headers, no horizontal page overflow, readable event identity and reachable phone navigation.
- Run affected repository checks and required hosted full suites as the implementing parent workflow requires. This review ran none.

This is a recommendation for substantive defects, not cosmetic score nudges. It assumes no future score and cannot override a qualifying Opus overall.

## Later factual clarifications — vectors immutable

- **Phone Month labels:** The lack of visible titles is an intentional CSS rule; accessible names retain title text. It is a visual legibility judgment, not data loss or proof of inaccessible event names.
- **Phone navigation fade:** Source documents the fade as an overflow cue in a horizontally scrollable navigation region. No unreachable-navigation claim is established.
- **Login theme control:** web/AGENTS.md explicitly permits the signed-out theme select; the compact theme select is absent from the authenticated shell. This is not a violation of the signed-in Appearance requirement.
- **Font evidence:** Capture worker records Space Grotesk loaded and supplemental font-byte binding. Sol did not independently measure computed font use; original pixel-only font caveats remain unchanged.

During blind review, state metadata was clarified inside the freeze: Appearance is a populated collection of 23 choices; existing-event is a populated edit form; default-phone-list visibly identifies as List while default-open is capture provenance. Original values remain under `inventory_identity` with explicit mismatch reasons. Membership, original image identities and exclusions stayed unchanged.

## Behavior and evidence limits

Own keyboard, focus, contrast, states, responsive, integration and security gates remain **untested**. Screenshots establish visible appearance, not full keyboard order, focus containment/return, computed contrast, touch usability, loading/errors, persistence or integration. Font identity and actual computed text/border/focus contrast were unproven from pixels during scoring. Missing flows were not assigned zero.

Post-freeze [capture checks](capture-checks.md#behavior-gates) record a **keyboard failure in 4/4 event-trigger focus-return probes**, while Enter/open and Escape/close succeeded. This external measured failure is separate from screenshot scores and is not an own test execution. Complete focus/contrast/states/responsive gates remain untested. Targeted recorded role checks return 403 for everyday `/api/admin/users` and admin `/api/calendars`; this does not establish full security acceptance.

These are initial viewport states: dialogs without onscreen keyboard; settings/admin continuation below the fold; synthetic populated calendars; sparse People/group calendar; empty app-password collection and Login. No production, SSO, external calendars, recovery, mail or whole security acceptance is claimed.

## Complete raw scores and exact opened-original record

Every linked candidate below was opened **individually at full original resolution**, in listed order, with `view_image(detail="original")`. H=hierarchy, T=typography, D=density_layout, C=color, A=visible_affordances, K=coherence. Brand order: palette_tokens, type_geometry, shell, accent_discipline, suite_cohesion. JSON retains concrete observations for every dimension, identities/hash/bytes/size, state, viewport/cohort/theme, confidence, caveats, artifacts and exact arithmetic for spreadsheet export.

| # | Original opened | Observed surface | Size/theme | H | T | D | C | A | K | Unrounded | Rubric 1dp | Brand vector | Exact brand |
|---:|---|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---|---|
| 1 | [admin-group-calendars-desktop-dark.png](screenshots/kycalendar-00-before-admin-group-calendars-desktop-dark.png) | admin-group-calendars | 1280×900 dark | 8 | 8 | 7 | 8 | 7 | 7 | 7.50 | 7.5 | 9, 8, 8, 9, 8 | 42/5 |
| 2 | [admin-group-calendars-desktop-light.png](screenshots/kycalendar-00-before-admin-group-calendars-desktop-light.png) | admin-group-calendars | 1280×900 light | 8 | 8 | 7 | 8 | 7 | 7 | 7.50 | 7.5 | 9, 8, 8, 9, 8 | 42/5 |
| 3 | [admin-group-calendars-mobile-dark.png](screenshots/kycalendar-00-before-admin-group-calendars-mobile-dark.png) | admin-group-calendars | 390×844 dark | 7 | 8 | 7 | 8 | 7 | 7 | 7.30 | 7.3 | 9, 8, 8, 9, 8 | 42/5 |
| 4 | [admin-group-calendars-mobile-light.png](screenshots/kycalendar-00-before-admin-group-calendars-mobile-light.png) | admin-group-calendars | 390×844 light | 7 | 8 | 7 | 8 | 7 | 7 | 7.30 | 7.3 | 9, 8, 8, 9, 8 | 42/5 |
| 5 | [admin-overview-desktop-dark.png](screenshots/kycalendar-00-before-admin-overview-desktop-dark.png) | admin-overview | 1280×900 dark | 8 | 8 | 8 | 8 | 8 | 8 | 8.00 | 8.0 | 9, 8, 8, 9, 8 | 42/5 |
| 6 | [admin-overview-desktop-light.png](screenshots/kycalendar-00-before-admin-overview-desktop-light.png) | admin-overview | 1280×900 light | 8 | 8 | 8 | 8 | 8 | 8 | 8.00 | 8.0 | 9, 8, 8, 9, 8 | 42/5 |
| 7 | [admin-overview-mobile-dark.png](screenshots/kycalendar-00-before-admin-overview-mobile-dark.png) | admin-overview | 390×844 dark | 8 | 8 | 7 | 8 | 8 | 8 | 7.80 | 7.8 | 9, 8, 8, 9, 8 | 42/5 |
| 8 | [admin-overview-mobile-light.png](screenshots/kycalendar-00-before-admin-overview-mobile-light.png) | admin-overview | 390×844 light | 8 | 8 | 7 | 8 | 8 | 8 | 7.80 | 7.8 | 9, 8, 8, 9, 8 | 42/5 |
| 9 | [admin-people-desktop-dark.png](screenshots/kycalendar-00-before-admin-people-desktop-dark.png) | admin-people | 1280×900 dark | 8 | 8 | 8 | 8 | 8 | 8 | 8.00 | 8.0 | 9, 8, 8, 9, 8 | 42/5 |
| 10 | [admin-people-desktop-light.png](screenshots/kycalendar-00-before-admin-people-desktop-light.png) | admin-people | 1280×900 light | 8 | 8 | 8 | 8 | 8 | 8 | 8.00 | 8.0 | 9, 8, 8, 9, 8 | 42/5 |
| 11 | [admin-people-mobile-dark.png](screenshots/kycalendar-00-before-admin-people-mobile-dark.png) | admin-people | 390×844 dark | 7 | 8 | 7 | 8 | 8 | 8 | 7.60 | 7.6 | 9, 8, 8, 9, 8 | 42/5 |
| 12 | [admin-people-mobile-light.png](screenshots/kycalendar-00-before-admin-people-mobile-light.png) | admin-people | 390×844 light | 7 | 8 | 7 | 8 | 8 | 8 | 7.60 | 7.6 | 9, 8, 8, 9, 8 | 42/5 |
| 13 | [appearance-desktop-dark.png](screenshots/kycalendar-00-before-appearance-desktop-dark.png) | appearance | 1280×900 dark | 9 | 8 | 9 | 9 | 9 | 9 | 8.85 | 8.9 | 9, 9, 8, 9, 9 | 44/5 |
| 14 | [appearance-desktop-light.png](screenshots/kycalendar-00-before-appearance-desktop-light.png) | appearance | 1280×900 light | 9 | 8 | 9 | 9 | 9 | 9 | 8.85 | 8.9 | 9, 9, 8, 9, 9 | 44/5 |
| 15 | [appearance-mobile-dark.png](screenshots/kycalendar-00-before-appearance-mobile-dark.png) | appearance | 390×844 dark | 8 | 8 | 8 | 9 | 9 | 9 | 8.45 | 8.5 | 9, 9, 8, 9, 9 | 44/5 |
| 16 | [appearance-mobile-light.png](screenshots/kycalendar-00-before-appearance-mobile-light.png) | appearance | 390×844 light | 8 | 8 | 8 | 9 | 9 | 9 | 8.45 | 8.5 | 9, 9, 8, 9, 9 | 44/5 |
| 17 | [day-desktop-dark.png](screenshots/kycalendar-00-before-day-desktop-dark.png) | day | 1280×900 dark | 8 | 8 | 8 | 8 | 8 | 8 | 8.00 | 8.0 | 9, 8, 8, 9, 8 | 42/5 |
| 18 | [day-desktop-light.png](screenshots/kycalendar-00-before-day-desktop-light.png) | day | 1280×900 light | 8 | 8 | 8 | 8 | 8 | 8 | 8.00 | 8.0 | 9, 8, 8, 9, 8 | 42/5 |
| 19 | [day-mobile-dark.png](screenshots/kycalendar-00-before-day-mobile-dark.png) | day | 390×844 dark | 8 | 7 | 7 | 8 | 8 | 8 | 7.65 | 7.7 | 9, 8, 7, 9, 8 | 41/5 |
| 20 | [day-mobile-light.png](screenshots/kycalendar-00-before-day-mobile-light.png) | day | 390×844 light | 8 | 7 | 7 | 8 | 8 | 8 | 7.65 | 7.7 | 9, 8, 7, 9, 8 | 41/5 |
| 21 | [default-phone-list-mobile-dark.png](screenshots/kycalendar-00-before-default-phone-list-mobile-dark.png) | list | 390×844 dark | 8 | 7 | 8 | 8 | 8 | 8 | 7.85 | 7.9 | 9, 8, 7, 9, 8 | 41/5 |
| 22 | [default-phone-list-mobile-light.png](screenshots/kycalendar-00-before-default-phone-list-mobile-light.png) | list | 390×844 light | 8 | 7 | 8 | 8 | 8 | 8 | 7.85 | 7.9 | 9, 8, 7, 9, 8 | 41/5 |
| 23 | [existing-event-desktop-dark.png](screenshots/kycalendar-00-before-existing-event-desktop-dark.png) | existing-event | 1280×900 dark | 9 | 8 | 8 | 9 | 9 | 9 | 8.65 | 8.7 | 9, 8, 8, 9, 8 | 42/5 |
| 24 | [existing-event-desktop-light.png](screenshots/kycalendar-00-before-existing-event-desktop-light.png) | existing-event | 1280×900 light | 9 | 8 | 8 | 9 | 9 | 9 | 8.65 | 8.7 | 9, 8, 8, 9, 8 | 42/5 |
| 25 | [existing-event-mobile-dark.png](screenshots/kycalendar-00-before-existing-event-mobile-dark.png) | existing-event | 390×844 dark | 9 | 8 | 8 | 9 | 9 | 9 | 8.65 | 8.7 | 9, 8, 8, 9, 8 | 42/5 |
| 26 | [existing-event-mobile-light.png](screenshots/kycalendar-00-before-existing-event-mobile-light.png) | existing-event | 390×844 light | 9 | 8 | 8 | 9 | 9 | 9 | 8.65 | 8.7 | 9, 8, 8, 9, 8 | 42/5 |
| 27 | [list-desktop-dark.png](screenshots/kycalendar-00-before-list-desktop-dark.png) | list | 1280×900 dark | 8 | 7 | 8 | 8 | 8 | 8 | 7.85 | 7.9 | 9, 8, 8, 9, 8 | 42/5 |
| 28 | [list-desktop-light.png](screenshots/kycalendar-00-before-list-desktop-light.png) | list | 1280×900 light | 8 | 7 | 8 | 8 | 8 | 8 | 7.85 | 7.9 | 9, 8, 8, 9, 8 | 42/5 |
| 29 | [login-desktop-dark.png](screenshots/kycalendar-00-before-login-desktop-dark.png) | login | 1280×900 dark | 9 | 8 | 8 | 9 | 9 | 8 | 8.50 | 8.5 | 9, 8, N/A, 9, 8 | 17/2 |
| 30 | [login-desktop-light.png](screenshots/kycalendar-00-before-login-desktop-light.png) | login | 1280×900 light | 9 | 8 | 8 | 9 | 9 | 8 | 8.50 | 8.5 | 9, 8, N/A, 9, 8 | 17/2 |
| 31 | [login-mobile-dark.png](screenshots/kycalendar-00-before-login-mobile-dark.png) | login | 390×844 dark | 9 | 8 | 8 | 9 | 9 | 8 | 8.50 | 8.5 | 9, 8, N/A, 9, 8 | 17/2 |
| 32 | [login-mobile-light.png](screenshots/kycalendar-00-before-login-mobile-light.png) | login | 390×844 light | 9 | 8 | 8 | 9 | 9 | 8 | 8.50 | 8.5 | 9, 8, N/A, 9, 8 | 17/2 |
| 33 | [month-desktop-dark.png](screenshots/kycalendar-00-before-month-desktop-dark.png) | month | 1280×900 dark | 8 | 7 | 7 | 8 | 8 | 8 | 7.65 | 7.7 | 9, 7, 8, 9, 8 | 41/5 |
| 34 | [month-desktop-light.png](screenshots/kycalendar-00-before-month-desktop-light.png) | month | 1280×900 light | 8 | 7 | 7 | 8 | 8 | 8 | 7.65 | 7.7 | 9, 7, 8, 9, 8 | 41/5 |
| 35 | [month-mobile-dark.png](screenshots/kycalendar-00-before-month-mobile-dark.png) | month | 390×844 dark | 7 | 5 | 5 | 8 | 6 | 7 | 6.30 | 6.3 | 9, 6, 7, 9, 8 | 39/5 |
| 36 | [month-mobile-light.png](screenshots/kycalendar-00-before-month-mobile-light.png) | month | 390×844 light | 7 | 5 | 5 | 8 | 6 | 7 | 6.30 | 6.3 | 9, 6, 7, 9, 8 | 39/5 |
| 37 | [new-event-desktop-dark.png](screenshots/kycalendar-00-before-new-event-desktop-dark.png) | new-event | 1280×900 dark | 8 | 8 | 8 | 9 | 8 | 9 | 8.30 | 8.3 | 9, 8, 8, 9, 8 | 42/5 |
| 38 | [new-event-desktop-light.png](screenshots/kycalendar-00-before-new-event-desktop-light.png) | new-event | 1280×900 light | 8 | 8 | 8 | 9 | 8 | 9 | 8.30 | 8.3 | 9, 8, 8, 9, 8 | 42/5 |
| 39 | [new-event-mobile-dark.png](screenshots/kycalendar-00-before-new-event-mobile-dark.png) | new-event | 390×844 dark | 8 | 8 | 8 | 9 | 8 | 9 | 8.30 | 8.3 | 9, 8, 8, 9, 8 | 42/5 |
| 40 | [new-event-mobile-light.png](screenshots/kycalendar-00-before-new-event-mobile-light.png) | new-event | 390×844 light | 8 | 8 | 8 | 9 | 8 | 9 | 8.30 | 8.3 | 9, 8, 8, 9, 8 | 42/5 |
| 41 | [phones-and-apps-desktop-dark.png](screenshots/kycalendar-00-before-phones-and-apps-desktop-dark.png) | phones-and-apps | 1280×900 dark | 8 | 8 | 7 | 8 | 8 | 8 | 7.80 | 7.8 | 9, 8, 8, 9, 8 | 42/5 |
| 42 | [phones-and-apps-desktop-light.png](screenshots/kycalendar-00-before-phones-and-apps-desktop-light.png) | phones-and-apps | 1280×900 light | 8 | 8 | 7 | 8 | 8 | 8 | 7.80 | 7.8 | 9, 8, 8, 9, 8 | 42/5 |
| 43 | [phones-and-apps-mobile-dark.png](screenshots/kycalendar-00-before-phones-and-apps-mobile-dark.png) | phones-and-apps | 390×844 dark | 8 | 8 | 7 | 8 | 8 | 8 | 7.80 | 7.8 | 9, 8, 8, 9, 8 | 42/5 |
| 44 | [phones-and-apps-mobile-light.png](screenshots/kycalendar-00-before-phones-and-apps-mobile-light.png) | phones-and-apps | 390×844 light | 8 | 8 | 7 | 8 | 8 | 8 | 7.80 | 7.8 | 9, 8, 8, 9, 8 | 42/5 |
| 45 | [week-desktop-dark.png](screenshots/kycalendar-00-before-week-desktop-dark.png) | week | 1280×900 dark | 8 | 7 | 7 | 8 | 8 | 8 | 7.65 | 7.7 | 9, 7, 8, 9, 8 | 41/5 |
| 46 | [week-desktop-light.png](screenshots/kycalendar-00-before-week-desktop-light.png) | week | 1280×900 light | 8 | 7 | 7 | 8 | 8 | 8 | 7.65 | 7.7 | 9, 7, 8, 9, 8 | 41/5 |
| 47 | [week-mobile-dark.png](screenshots/kycalendar-00-before-week-mobile-dark.png) | week | 390×844 dark | 7 | 3 | 4 | 8 | 5 | 6 | 5.50 | 5.5 | 9, 4, 7, 9, 7 | 36/5 |
| 48 | [week-mobile-light.png](screenshots/kycalendar-00-before-week-mobile-light.png) | week | 390×844 light | 7 | 3 | 4 | 8 | 5 | 6 | 5.50 | 5.5 | 9, 4, 7, 9, 7 | 36/5 |

Separately opened at original resolution, brand only: [KyIdentity Appearance light](brand-reference-kyidentity-appearance-desktop-light.png) and [dark](brand-reference-kyidentity-appearance-desktop-dark.png). Neither enters quality.

Verified but **not opened for scoring**: [explicit phone List dark](screenshots/kycalendar-00-before-list-mobile-dark.png) and [light](screenshots/kycalendar-00-before-list-mobile-light.png). They are preserved near duplicates excluded by first filename. Dark: 10/329160 changed pixels, max channel delta 1. Light: 7/329160, max delta 1. Corresponding `default-phone-list` originals represent them. No exact duplicate was reported. All 52 hash/byte/dimension records remain in JSON.

## Verification and write scope

Recomputed all weighted totals, HALF_UP rounding, exact overall/unrounded/brand means and group arithmetic; checked membership, integers and observation coverage. Reconstructed canonical array bytes and verified hash/length after later additions. The full original `files` JSON literal is unchanged.

Only `docs/ui-calendar-eight/sol-round1.json` and `sol-round1.md` were written. No browser, build, application test, product edit, checkout, commit, push or subdelegation. No peer review opened. AGENTS.md documents remain unchanged because this pair adds evidence under the existing contract without changing durable ownership/workflow/product behavior. Parent owns spreadsheet export and later implementation/PR work.


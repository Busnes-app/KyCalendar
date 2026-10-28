# KyCalendar UI Council — Opus round 3

**Overall quality 633/80 = 7.912500 → 7.9** (mean of 48 rubric-rounded totals; unrounded mean 2527/320 = 7.896875 → 7.9). Gate ≥ 8.0: **not accepted** (0.0375 below the 7.95 half-up boundary (sum of rounded totals 379.8; 381.6 needed)). Ky brand fit, separate: 1361/160 → 8.5. Independent satisfaction: **false**.

Reviewer Claude Opus 5.5 (`claude-opus-5-5`), rubric `ky-suite-v2` (pins verified), runtime `3aca6fcf934dacb0149aafce388cf38f02244015`, stage 04-confirmation. Capture: 1280×900 and 390×844, DPR 1, en-US, America/New_York, OS light/dark, no saved theme, real clock (displayed Oct 27 via real controls; actual Oct 28), no crops.

## Freeze

Whole `files` array, `json.dumps(files,ensure_ascii=False,sort_keys=True,separators=(',',':'),allow_nan=False).encode()`, no trailing newline: sha256 `f10f0c22e95312d689315de62cad07dfc1cfa22453e7851171e9a5aef049c455`, 111936 bytes, frozen 2026-10-28T12:20:20Z. Frozen before reading my round 1/2, responses, the round-2 audit, round-3 checks, build-report.py or source. No row changed after freeze; later facts are annotations outside the array.

Disclosure: COUNCIL.md (directory index) was read before the freeze while reading the directory contract; it lists round-1/2 aggregate means for Opus (7.5, 7.8) and Sol (7.9, 8.5), no per-file scores or findings.

## Inputs

206/206 sha256, bytes and PNG IHDR dimensions match artifacts-v4.json; 48 candidates equal inventory-v4 and manifest-04 entries; brand_only 2/2 match. Review inputs `review-inputs-v3.json` sha256 `ba99ffd97280c01c575851caaebf0c6ac55790c037a357ee7d49a9ace5ebb283`. 48 candidates viewed once each at full resolution; the two KyIdentity Appearance references viewed once for brand fit only. 2 exact SHA-256 pairs (phone list); first filename default-phone-list scored once, list-mobile unscored. No historical or diagnostic image viewed.

## Cohorts (Ky coverage, not a ranking)

| Cohort | n | Quality exact | Quality | Unrounded exact | Brand exact | Brand |
|---|---:|---|---:|---|---|---:|
| admin-console | 12 | 233/30 | 7.8 | 31/4 | 259/30 | 8.6 |
| auth | 4 | 83/10 | 8.3 | 331/40 | 67/8 | 8.4 |
| calendar-grid | 12 | 457/60 | 7.6 | 911/120 | 251/30 | 8.4 |
| calendar-list | 4 | 81/10 | 8.1 | 323/40 | 17/2 | 8.5 |
| event-editor | 8 | 81/10 | 8.1 | 81/10 | 17/2 | 8.5 |
| settings | 8 | 81/10 | 8.1 | 259/32 | 43/5 | 8.6 |

desktop 961/120 → 8.0 (brand 8.6); mobile 469/60 → 7.8 (brand 8.4).

dark 79/10 → 7.9 (brand 8.5); light 317/40 → 7.9 (brand 8.5).

## Per-file scores

H/T/D/C/A/Co = hierarchy, typography, density_layout, color, visible_affordances, coherence. Brand = palette/type/shell/accent/suite.

| File | Cohort | H | T | D | C | A | Co | Unrounded | Total | Brand | ky_fit |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---|---:|
| [admin-group-calendars-desktop-dark](screenshots/kycalendar-04-confirmation-admin-group-calendars-desktop-dark.png) | admin-console | 7 | 8 | 8 | 8 | 7 | 7 | 7.50 | 7.5 | 9/8/9/9/8 | 8.6 |
| [admin-group-calendars-desktop-light](screenshots/kycalendar-04-confirmation-admin-group-calendars-desktop-light.png) | admin-console | 7 | 8 | 8 | 8 | 7 | 7 | 7.50 | 7.5 | 9/8/9/9/8 | 8.6 |
| [admin-group-calendars-mobile-dark](screenshots/kycalendar-04-confirmation-admin-group-calendars-mobile-dark.png) | admin-console | 7 | 8 | 8 | 8 | 7 | 7 | 7.50 | 7.5 | 9/8/8/9/8 | 8.4 |
| [admin-group-calendars-mobile-light](screenshots/kycalendar-04-confirmation-admin-group-calendars-mobile-light.png) | admin-console | 7 | 8 | 8 | 8 | 7 | 7 | 7.50 | 7.5 | 9/8/8/9/8 | 8.4 |
| [admin-overview-desktop-dark](screenshots/kycalendar-04-confirmation-admin-overview-desktop-dark.png) | admin-console | 7 | 8 | 8 | 9 | 8 | 8 | 7.95 | 8.0 | 9/8/9/9/9 | 8.8 |
| [admin-overview-desktop-light](screenshots/kycalendar-04-confirmation-admin-overview-desktop-light.png) | admin-console | 7 | 8 | 8 | 9 | 8 | 8 | 7.95 | 8.0 | 9/8/9/9/9 | 8.8 |
| [admin-overview-mobile-dark](screenshots/kycalendar-04-confirmation-admin-overview-mobile-dark.png) | admin-console | 7 | 8 | 8 | 9 | 8 | 8 | 7.95 | 8.0 | 9/8/8/9/9 | 8.6 |
| [admin-overview-mobile-light](screenshots/kycalendar-04-confirmation-admin-overview-mobile-light.png) | admin-console | 7 | 8 | 8 | 9 | 8 | 8 | 7.95 | 8.0 | 9/8/8/9/9 | 8.6 |
| [admin-people-desktop-dark](screenshots/kycalendar-04-confirmation-admin-people-desktop-dark.png) | admin-console | 8 | 8 | 7 | 8 | 8 | 8 | 7.80 | 7.8 | 9/8/9/9/9 | 8.8 |
| [admin-people-desktop-light](screenshots/kycalendar-04-confirmation-admin-people-desktop-light.png) | admin-console | 8 | 8 | 7 | 8 | 8 | 8 | 7.80 | 7.8 | 9/8/9/9/9 | 8.8 |
| [admin-people-mobile-dark](screenshots/kycalendar-04-confirmation-admin-people-mobile-dark.png) | admin-console | 8 | 8 | 7 | 8 | 8 | 8 | 7.80 | 7.8 | 9/8/8/9/9 | 8.6 |
| [admin-people-mobile-light](screenshots/kycalendar-04-confirmation-admin-people-mobile-light.png) | admin-console | 8 | 8 | 7 | 8 | 8 | 8 | 7.80 | 7.8 | 9/8/8/9/9 | 8.6 |
| [appearance-desktop-dark](screenshots/kycalendar-04-confirmation-appearance-desktop-dark.png) | settings | 8 | 8 | 8 | 8 | 9 | 9 | 8.30 | 8.3 | 9/8/9/9/9 | 8.8 |
| [appearance-desktop-light](screenshots/kycalendar-04-confirmation-appearance-desktop-light.png) | settings | 8 | 8 | 8 | 8 | 9 | 9 | 8.30 | 8.3 | 9/8/9/9/9 | 8.8 |
| [appearance-mobile-dark](screenshots/kycalendar-04-confirmation-appearance-mobile-dark.png) | settings | 8 | 8 | 8 | 8 | 7 | 9 | 8.00 | 8.0 | 9/8/8/9/9 | 8.6 |
| [appearance-mobile-light](screenshots/kycalendar-04-confirmation-appearance-mobile-light.png) | settings | 8 | 8 | 8 | 8 | 8 | 9 | 8.15 | 8.2 | 9/8/8/9/9 | 8.6 |
| [day-desktop-dark](screenshots/kycalendar-04-confirmation-day-desktop-dark.png) | calendar-grid | 8 | 8 | 8 | 8 | 8 | 8 | 8.00 | 8.0 | 9/8/9/9/8 | 8.6 |
| [day-desktop-light](screenshots/kycalendar-04-confirmation-day-desktop-light.png) | calendar-grid | 8 | 8 | 8 | 8 | 8 | 8 | 8.00 | 8.0 | 9/8/9/9/8 | 8.6 |
| [day-mobile-dark](screenshots/kycalendar-04-confirmation-day-mobile-dark.png) | calendar-grid | 8 | 8 | 8 | 8 | 7 | 8 | 7.85 | 7.9 | 9/8/8/9/8 | 8.4 |
| [day-mobile-light](screenshots/kycalendar-04-confirmation-day-mobile-light.png) | calendar-grid | 8 | 8 | 8 | 8 | 7 | 8 | 7.85 | 7.9 | 9/8/8/9/8 | 8.4 |
| [default-phone-list-mobile-dark](screenshots/kycalendar-04-confirmation-default-phone-list-mobile-dark.png) | calendar-list | 8 | 8 | 8 | 9 | 7 | 8 | 8.00 | 8.0 | 9/8/8/9/8 | 8.4 |
| [default-phone-list-mobile-light](screenshots/kycalendar-04-confirmation-default-phone-list-mobile-light.png) | calendar-list | 8 | 8 | 8 | 9 | 7 | 8 | 8.00 | 8.0 | 9/8/8/9/8 | 8.4 |
| [existing-event-desktop-dark](screenshots/kycalendar-04-confirmation-existing-event-desktop-dark.png) | event-editor | 9 | 8 | 8 | 8 | 8 | 8 | 8.20 | 8.2 | 9/8/9/9/8 | 8.6 |
| [existing-event-desktop-light](screenshots/kycalendar-04-confirmation-existing-event-desktop-light.png) | event-editor | 9 | 8 | 8 | 8 | 8 | 8 | 8.20 | 8.2 | 9/8/9/9/8 | 8.6 |
| [existing-event-mobile-dark](screenshots/kycalendar-04-confirmation-existing-event-mobile-dark.png) | event-editor | 9 | 8 | 8 | 8 | 8 | 8 | 8.20 | 8.2 | 9/8/8/9/8 | 8.4 |
| [existing-event-mobile-light](screenshots/kycalendar-04-confirmation-existing-event-mobile-light.png) | event-editor | 9 | 8 | 8 | 8 | 8 | 8 | 8.20 | 8.2 | 9/8/8/9/8 | 8.4 |
| [list-desktop-dark](screenshots/kycalendar-04-confirmation-list-desktop-dark.png) | calendar-list | 8 | 8 | 8 | 9 | 8 | 8 | 8.15 | 8.2 | 9/8/9/9/8 | 8.6 |
| [list-desktop-light](screenshots/kycalendar-04-confirmation-list-desktop-light.png) | calendar-list | 8 | 8 | 8 | 9 | 8 | 8 | 8.15 | 8.2 | 9/8/9/9/8 | 8.6 |
| [login-desktop-dark](screenshots/kycalendar-04-confirmation-login-desktop-dark.png) | auth | 9 | 8 | 8 | 8 | 8 | 8 | 8.20 | 8.2 | 8/8/N/A/9/8 | 8.3 |
| [login-desktop-light](screenshots/kycalendar-04-confirmation-login-desktop-light.png) | auth | 9 | 8 | 8 | 9 | 8 | 8 | 8.35 | 8.4 | 9/8/N/A/9/8 | 8.5 |
| [login-mobile-dark](screenshots/kycalendar-04-confirmation-login-mobile-dark.png) | auth | 9 | 8 | 8 | 8 | 8 | 8 | 8.20 | 8.2 | 8/8/N/A/9/8 | 8.3 |
| [login-mobile-light](screenshots/kycalendar-04-confirmation-login-mobile-light.png) | auth | 9 | 8 | 8 | 9 | 8 | 8 | 8.35 | 8.4 | 9/8/N/A/9/8 | 8.5 |
| [month-desktop-dark](screenshots/kycalendar-04-confirmation-month-desktop-dark.png) | calendar-grid | 8 | 8 | 7 | 8 | 8 | 8 | 7.80 | 7.8 | 9/8/9/8/8 | 8.4 |
| [month-desktop-light](screenshots/kycalendar-04-confirmation-month-desktop-light.png) | calendar-grid | 8 | 8 | 7 | 8 | 8 | 8 | 7.80 | 7.8 | 9/8/9/8/8 | 8.4 |
| [month-mobile-dark](screenshots/kycalendar-04-confirmation-month-mobile-dark.png) | calendar-grid | 7 | 5 | 6 | 8 | 7 | 7 | 6.65 | 6.7 | 9/8/8/8/8 | 8.2 |
| [month-mobile-light](screenshots/kycalendar-04-confirmation-month-mobile-light.png) | calendar-grid | 7 | 5 | 6 | 8 | 7 | 7 | 6.65 | 6.7 | 9/8/8/8/8 | 8.2 |
| [new-event-desktop-dark](screenshots/kycalendar-04-confirmation-new-event-desktop-dark.png) | event-editor | 8 | 8 | 8 | 8 | 8 | 8 | 8.00 | 8.0 | 9/8/9/9/8 | 8.6 |
| [new-event-desktop-light](screenshots/kycalendar-04-confirmation-new-event-desktop-light.png) | event-editor | 8 | 8 | 8 | 8 | 8 | 8 | 8.00 | 8.0 | 9/8/9/9/8 | 8.6 |
| [new-event-mobile-dark](screenshots/kycalendar-04-confirmation-new-event-mobile-dark.png) | event-editor | 8 | 8 | 8 | 8 | 8 | 8 | 8.00 | 8.0 | 9/8/8/9/8 | 8.4 |
| [new-event-mobile-light](screenshots/kycalendar-04-confirmation-new-event-mobile-light.png) | event-editor | 8 | 8 | 8 | 8 | 8 | 8 | 8.00 | 8.0 | 9/8/8/9/8 | 8.4 |
| [phones-and-apps-desktop-dark](screenshots/kycalendar-04-confirmation-phones-and-apps-desktop-dark.png) | settings | 8 | 8 | 8 | 9 | 8 | 7 | 8.00 | 8.0 | 9/8/9/9/8 | 8.6 |
| [phones-and-apps-desktop-light](screenshots/kycalendar-04-confirmation-phones-and-apps-desktop-light.png) | settings | 8 | 8 | 8 | 9 | 8 | 7 | 8.00 | 8.0 | 9/8/9/9/8 | 8.6 |
| [phones-and-apps-mobile-dark](screenshots/kycalendar-04-confirmation-phones-and-apps-mobile-dark.png) | settings | 8 | 8 | 8 | 9 | 8 | 7 | 8.00 | 8.0 | 9/8/8/9/8 | 8.4 |
| [phones-and-apps-mobile-light](screenshots/kycalendar-04-confirmation-phones-and-apps-mobile-light.png) | settings | 8 | 8 | 8 | 9 | 8 | 7 | 8.00 | 8.0 | 9/8/8/9/8 | 8.4 |
| [week-desktop-dark](screenshots/kycalendar-04-confirmation-week-desktop-dark.png) | calendar-grid | 8 | 8 | 8 | 8 | 8 | 8 | 8.00 | 8.0 | 9/8/9/8/8 | 8.4 |
| [week-desktop-light](screenshots/kycalendar-04-confirmation-week-desktop-light.png) | calendar-grid | 8 | 8 | 8 | 8 | 8 | 8 | 8.00 | 8.0 | 9/8/9/8/8 | 8.4 |
| [week-mobile-dark](screenshots/kycalendar-04-confirmation-week-mobile-dark.png) | calendar-grid | 7 | 8 | 6 | 8 | 7 | 8 | 7.25 | 7.3 | 9/8/8/8/8 | 8.2 |
| [week-mobile-light](screenshots/kycalendar-04-confirmation-week-mobile-light.png) | calendar-grid | 7 | 8 | 6 | 8 | 7 | 8 | 7.25 | 7.3 | 9/8/8/8/8 | 8.2 |

Per-dimension observations, confidence (all high), artifacts, caveats and per-file capture references are in [opus-round3.json](opus-round3.json).

## Drift against my round 2

All 48: mean 31/240 (0.129167). No file moved by 0.5 or more.

- 18 images byte-identical to the round-2 originals rose by mean +43/180 (0.238889; range +0.1..+0.4). This is my calibration drift, not product improvement.
- 16 images byte-identical to the 00-before baseline sit +9/40 (0.225; range 0..+0.4) above my round-1 scores: calibration only.
- Informational only, not the gate: holding the 18 identical images at round-2 totals gives 751/96 = 7.822917 -> 7.8. Most of the +0.13 overall rise is reviewer calibration; the 30 changed images moved +19/300 (0.063).

| Files | Prior | Now | Δ | Cause |
|---|---:|---:|---:|---|
| admin-group-calendars desktop light/dark | 7.9 | 7.5 | -0.4 | anchor/correction: Add group is now correctly disabled, but Role stays enabled beside a disabled Group select and two disabled treatments (filled select, ghost button) share one row; Delete calendar competes as the only emphasis on the single item. |
| month-mobile light/dark | 7.0 | 6.7 | -0.3 | product change plus anchor: two-line titles reveal more identity ('Team che…', full 'Release preparation') but overflow-wrap:anywhere (calendar.css:167) breaks words mid-glyph ('Librar/y day', 'Suppl/ier'); typography 7 -> 5 follows the round-2 audit's legibility anchor. |
| week-mobile light/dark | 7.1 | 7.3 | +0.2 | coherence +1; density 6 kept on a partly false premise (see annotation A1). |

## Post-freeze annotations (no row changed)

- **A1** (correction): My frozen density/hierarchy observations call Sun/Mon empty and fault today (Wed) being off-screen. Monday visibly holds Team check-in; the displayed date (Tue 27) is fully visible; Wednesday-as-today is a live-clock effect. This repeats the round-2 premise the audit corrected. Remaining valid concern: three of seven days visible with a text-only scroll hint. Scores stand frozen. Counterfactual density 7 gives 7.45 -> 7.5 for both files and overall 1901/240 = 7.920833 -> 7.9; the gate result is unchanged.
- **A2** (qualification): The cut 'Phones &' label is the designed continuation fade at rest; capture checks record all 56 focused navigation rows fully visible and unmasked. My affordance 7 judges the at-rest cue only; no route-reachability claim.
- **A3** (artifact (cause unproven)): Raised Two-step verification pill persists from round 2; my round-2 note matched it to the nav hover token (--panel-hover equals the light sidebar, so light shows nothing). Stale-hover cause unproven; affordance 7 stands as captured.
- **A4** (artifact): End-field focus and blue native segment selection are the retained driver artifact; scored as captured. No palette or focus change recommended.
- **A5** (source confirmation): git show 3aca6fc:web/src/styles/calendar.css:167 sets -webkit-line-clamp:2 with overflow-wrap:anywhere on phone Month titles, which produces the visible mid-word breaks.
- **A6** (source confirmation): GroupCalendars.tsx at 3aca6fc: Group select disabled when available.length===0 (:206), Add group disabled when !group||available.length===0 (:221), Role select (:215) has no disabled state — matching the mixed captured state.
- **A7** (clock): Today highlight on Oct 28, the week time indicator and People last-sign-in times are live-clock effects; no credit or penalty intended for them.

## Response dispositions

- **phone_week_legibility** — accepted. Empty-Monday/clipped-Tuesday premise is false; I repeated it blind (A1). Readable columns and painted gutter visible. Limited 3-day overview remains a product choice, not a defect I ask to fix.
- **phone_month_names** — partly effective; objection retained. More identity per event, but mid-word wrapping is a new legibility defect. Substantive, not cosmetic.
- **phone_header_height** — resolved. Time-zone note is one full-width line; Day/List/Month content starts near y281.
- **tall_timed_names** — resolved. Week desktop 'Planning the / community…' shows two lines; short blocks stay single-line.
- **group_unavailable** — resolved; minor residual. Add group visibly disabled and phone Group/Role fields fill the card. Role select still enabled (A6).
- **keyboard_lifecycle** — not screenshot-visible. Recorded probes pass; untested by this reviewer.
- **focused_navigation** — accepted per recorded check. Focus visibility not visible in stills; at-rest fade judged as captured (A2).
- **people_action_density** — deferral accepted. Still the main density drag on People (7); optional quieter Edit/Reset/Make admin styling.
- **new_event_focus_and_palette** — accepted. Driver artifact; semantic orange retained. No recolor requested.

## Remaining issues

1. **Phone Month titles break mid-word** (6.7 / 6.7): 'Librar/y day', 'Suppl/ier…', 'Desig/n…', 'Lunc/h…' in ~45px chips. Source: `web/src/styles/calendar.css:167 (overflow-wrap:anywhere)`. Fix: Break at word boundaries within the two-line clamp (overflow-wrap:normal; consider hyphens:auto with the document lang) so words stay whole; verify long single words do not clip without ellipsis.
2. **Mixed disabled state in Group calendars access row** (7.5 x4): Disabled filled Group select, enabled Role select, ghost Add group; no inline path to create a group. Source: `web/src/pages/GroupCalendars.tsx:215`. Fix: Disable Role with the same condition as Group (available.length===0) so the row reads as one unavailable state; optionally link 'add a group' in the help line.
3. **People row actions heavy (optional, deferred)** (7.8 x4): Four outlined 40px buttons per everyday row; Disable wraps alone on phone. Fix: Optional quieter tertiary style for Edit/Reset/Make admin; keep Disable as danger outline and 40px targets.

Not recommended: Recolouring semantic orange (checkboxes, today, selection); Changing New event focus or native date controls; Another capture round only to move sub-0.3 calibration differences; Restyling theme cards or the login.

## Satisfaction and progression

Independent satisfaction **false**: Round 3 fixed the header, tall titles, Add group state and the Week paint regression, and desktop now averages 961/120 -> 8.0. Phone Month still reads worse than it should (mid-word breaks, 6.7) and the admin Group calendars row shows mixed states (7.5), so overall is 7.9. Two small, source-located fixes address the substantive remainder; most of the round-2 to round-3 rise is my calibration (+0.24 on byte-identical images), not product change. Separate from the gate.

Progression input 7.9 (633/80) is below 8.0; not accepted. Brand fit 8.5 stays separate. 9/10 remains a longer-term target. Gemini unused (known exhausted quota). Not a competitive ranking or behavior/accessibility/production acceptance.

## Gates

All five behavior gates are **untested** by this reviewer. Recorded capture-worker probes: Enter/Escape focus return 4/4, phone Week keyboard offsets 0→40→0, 56 focused nav rows fully visible, zero horizontal page overflow on 50 originals. Screenshots cannot pass a gate.

## Export compatibility

Checked after freeze against build-report.py: rows carry total_unrounded (exact decimal string), integer scores, half-up total, viewport string, ky_fit dict with scores/observations; top-level freeze.sha256/bytes and independent_satisfaction dict. Login ky_fit.scores.shell is JSON null: excluded from the brand mean, but the exporter's Brand evidence column will print 'None' rather than 'N/A'. A read-only replica of the exporter's per-row assertions passes.

## Limitations

- Screenshots show appearance only; keyboard, focus, contrast, states and responsive gates untested by this reviewer.
- Only the first viewport of long pages is visible (Phones and apps, admin overview/people/group calendars on phone, Appearance).
- Calibration: +0.24 mean on 18 byte-identical images; sub-0.3 changes are within my noise.
- Frozen week-mobile observations contain a known misreading (A1); counterfactual leaves the gate unchanged.
- COUNCIL.md aggregate means were seen before freeze (blind_order.disclosed_exposure).
- Computed contrast and font identity unproven from pixels.
- Hand-off: MySlop unavailable: known revoked token (401); not retried.

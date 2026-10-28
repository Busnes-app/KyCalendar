# KyCalendar UI Council — Opus round 2

**Overall quality 467/60 = 7.783333 → 7.8** (mean of 48 rubric-rounded totals; unrounded mean 7457/960 = 7.767708). Gate ≥ 8.0: **not accepted**. Ky brand fit, separate: 79/10 → 7.9. Independent satisfaction: **false**.

Reviewer `claude-opus-5-5`, rubric `ky-suite-v2` (pins verified), source `d7e3b89843c5893af0d18cc7e3ac147880cc91c4`, stages 01-after + 02-confirmation (Appearance). Capture: 1280×900 and 390×844, DPR 1, en-US, America/New_York, OS light/dark, no saved theme, real clock, no crops.

## Freeze

Whole `files` array, `json.dumps(files,ensure_ascii=False,sort_keys=True,separators=(',',':'),allow_nan=False).encode(), no trailing newline`: sha256 `9341638318d1603fd79f80ee435c096f5b96dbd65297581bab1f396ee4b076ab`, 174890 bytes, frozen 2026-10-28T04:20:12.551234Z — before reading my round 1, responses, the disagreement audit, round-2 capture checks, build-report.py or source. No vector changed after freeze. Peer reports, historical baseline and superseded diagnostic images were never opened.

## Inputs

All 106 preserved images hashed, byte-counted and PNG-header-checked: 0 mismatches against artifacts-v2.json / review-inputs-v2.json. 48 quality candidates viewed individually at full resolution; the 2 KyIdentity Appearance references viewed for brand fit only. Two phone List near-duplicates unscored (first-filename default-phone-list scored). Four 02-confirmation Appearance originals replace the 01-after diagnostics.

## Cohorts (Ky coverage, not a ranking)

| Cohort | n | Quality exact | Quality | Brand |
|---|---:|---|---:|---:|
| admin-console | 12 | 23/3 | 7.7 | 8.0 |
| auth | 4 | 8/1 | 8.0 | 8.0 |
| calendar-grid | 12 | 457/60 | 7.6 | 7.7 |
| calendar-list | 4 | 79/10 | 7.9 | 7.7 |
| event-editor | 8 | 317/40 | 7.9 | 7.9 |
| settings | 8 | 79/10 | 7.9 | 8.1 |

Desktop 1901/240 → 7.9; phone 367/48 → 7.6.

## Per-file scores

H/T/D/C/A/Co = hierarchy, typography, density_layout, color, visible_affordances, coherence. Brand vector = palette/type/shell/accent/suite.

| File | Cohort | H | T | D | C | A | Co | Unrounded | Total | Brand vector | ky_fit |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---|---:|
| [01-after-admin-group-calendars-desktop-dark](screenshots/kycalendar-01-after-admin-group-calendars-desktop-dark.png) | admin-console | 8 | 8 | 8 | 8 | 7 | 8 | 7.85 | 7.9 | 8/8/8/8/8 | 8.0 |
| [01-after-admin-group-calendars-desktop-light](screenshots/kycalendar-01-after-admin-group-calendars-desktop-light.png) | admin-console | 8 | 8 | 8 | 8 | 7 | 8 | 7.85 | 7.9 | 8/8/8/8/8 | 8.0 |
| [01-after-admin-group-calendars-mobile-dark](screenshots/kycalendar-01-after-admin-group-calendars-mobile-dark.png) | admin-console | 7 | 8 | 7 | 8 | 7 | 7 | 7.30 | 7.3 | 8/8/8/8/8 | 8.0 |
| [01-after-admin-group-calendars-mobile-light](screenshots/kycalendar-01-after-admin-group-calendars-mobile-light.png) | admin-console | 7 | 8 | 7 | 8 | 7 | 7 | 7.30 | 7.3 | 8/8/8/8/8 | 8.0 |
| [01-after-admin-overview-desktop-dark](screenshots/kycalendar-01-after-admin-overview-desktop-dark.png) | admin-console | 7 | 8 | 8 | 8 | 8 | 8 | 7.80 | 7.8 | 8/8/8/8/8 | 8.0 |
| [01-after-admin-overview-desktop-light](screenshots/kycalendar-01-after-admin-overview-desktop-light.png) | admin-console | 7 | 8 | 8 | 8 | 8 | 8 | 7.80 | 7.8 | 8/8/8/8/8 | 8.0 |
| [01-after-admin-overview-mobile-dark](screenshots/kycalendar-01-after-admin-overview-mobile-dark.png) | admin-console | 7 | 8 | 7 | 8 | 8 | 8 | 7.60 | 7.6 | 8/8/8/8/8 | 8.0 |
| [01-after-admin-overview-mobile-light](screenshots/kycalendar-01-after-admin-overview-mobile-light.png) | admin-console | 7 | 8 | 7 | 8 | 8 | 8 | 7.60 | 7.6 | 8/8/8/8/8 | 8.0 |
| [01-after-admin-people-desktop-dark](screenshots/kycalendar-01-after-admin-people-desktop-dark.png) | admin-console | 8 | 8 | 7 | 8 | 8 | 8 | 7.80 | 7.8 | 8/8/8/8/8 | 8.0 |
| [01-after-admin-people-desktop-light](screenshots/kycalendar-01-after-admin-people-desktop-light.png) | admin-console | 8 | 8 | 7 | 8 | 8 | 8 | 7.80 | 7.8 | 8/8/8/8/8 | 8.0 |
| [01-after-admin-people-mobile-dark](screenshots/kycalendar-01-after-admin-people-mobile-dark.png) | admin-console | 7 | 8 | 7 | 8 | 8 | 8 | 7.60 | 7.6 | 8/8/8/8/8 | 8.0 |
| [01-after-admin-people-mobile-light](screenshots/kycalendar-01-after-admin-people-mobile-light.png) | admin-console | 7 | 8 | 7 | 8 | 8 | 8 | 7.60 | 7.6 | 8/8/8/8/8 | 8.0 |
| [01-after-day-desktop-dark](screenshots/kycalendar-01-after-day-desktop-dark.png) | calendar-grid | 8 | 8 | 8 | 8 | 8 | 8 | 8.00 | 8.0 | 8/8/8/7/8 | 7.8 |
| [01-after-day-desktop-light](screenshots/kycalendar-01-after-day-desktop-light.png) | calendar-grid | 8 | 8 | 8 | 8 | 8 | 8 | 8.00 | 8.0 | 8/8/8/7/8 | 7.8 |
| [01-after-day-mobile-dark](screenshots/kycalendar-01-after-day-mobile-dark.png) | calendar-grid | 8 | 8 | 7 | 8 | 8 | 8 | 7.80 | 7.8 | 8/8/7/7/8 | 7.6 |
| [01-after-day-mobile-light](screenshots/kycalendar-01-after-day-mobile-light.png) | calendar-grid | 8 | 8 | 7 | 8 | 8 | 8 | 7.80 | 7.8 | 8/8/7/7/8 | 7.6 |
| [01-after-default-phone-list-mobile-dark](screenshots/kycalendar-01-after-default-phone-list-mobile-dark.png) | calendar-list | 8 | 8 | 7 | 8 | 8 | 8 | 7.80 | 7.8 | 8/8/7/7/8 | 7.6 |
| [01-after-default-phone-list-mobile-light](screenshots/kycalendar-01-after-default-phone-list-mobile-light.png) | calendar-list | 8 | 8 | 7 | 8 | 8 | 8 | 7.80 | 7.8 | 8/8/7/7/8 | 7.6 |
| [01-after-existing-event-desktop-dark](screenshots/kycalendar-01-after-existing-event-desktop-dark.png) | event-editor | 8 | 8 | 8 | 7 | 8 | 8 | 7.85 | 7.9 | 8/8/8/8/8 | 8.0 |
| [01-after-existing-event-desktop-light](screenshots/kycalendar-01-after-existing-event-desktop-light.png) | event-editor | 8 | 8 | 8 | 8 | 8 | 8 | 8.00 | 8.0 | 8/8/8/8/8 | 8.0 |
| [01-after-existing-event-mobile-dark](screenshots/kycalendar-01-after-existing-event-mobile-dark.png) | event-editor | 8 | 8 | 8 | 7 | 8 | 8 | 7.85 | 7.9 | 8/8/8/8/8 | 8.0 |
| [01-after-existing-event-mobile-light](screenshots/kycalendar-01-after-existing-event-mobile-light.png) | event-editor | 8 | 8 | 8 | 8 | 8 | 8 | 8.00 | 8.0 | 8/8/8/8/8 | 8.0 |
| [01-after-list-desktop-dark](screenshots/kycalendar-01-after-list-desktop-dark.png) | calendar-list | 8 | 8 | 8 | 8 | 8 | 8 | 8.00 | 8.0 | 8/8/8/7/8 | 7.8 |
| [01-after-list-desktop-light](screenshots/kycalendar-01-after-list-desktop-light.png) | calendar-list | 8 | 8 | 8 | 8 | 8 | 8 | 8.00 | 8.0 | 8/8/8/7/8 | 7.8 |
| [01-after-login-desktop-dark](screenshots/kycalendar-01-after-login-desktop-dark.png) | auth | 8 | 8 | 8 | 8 | 8 | 8 | 8.00 | 8.0 | 8/8/N/A/8/8 | 8.0 |
| [01-after-login-desktop-light](screenshots/kycalendar-01-after-login-desktop-light.png) | auth | 8 | 8 | 8 | 8 | 8 | 8 | 8.00 | 8.0 | 8/8/N/A/8/8 | 8.0 |
| [01-after-login-mobile-dark](screenshots/kycalendar-01-after-login-mobile-dark.png) | auth | 8 | 8 | 8 | 8 | 8 | 8 | 8.00 | 8.0 | 8/8/N/A/8/8 | 8.0 |
| [01-after-login-mobile-light](screenshots/kycalendar-01-after-login-mobile-light.png) | auth | 8 | 8 | 8 | 8 | 8 | 8 | 8.00 | 8.0 | 8/8/N/A/8/8 | 8.0 |
| [01-after-month-desktop-dark](screenshots/kycalendar-01-after-month-desktop-dark.png) | calendar-grid | 8 | 8 | 7 | 8 | 8 | 8 | 7.80 | 7.8 | 8/8/8/7/8 | 7.8 |
| [01-after-month-desktop-light](screenshots/kycalendar-01-after-month-desktop-light.png) | calendar-grid | 8 | 8 | 7 | 8 | 8 | 8 | 7.80 | 7.8 | 8/8/8/7/8 | 7.8 |
| [01-after-month-mobile-dark](screenshots/kycalendar-01-after-month-mobile-dark.png) | calendar-grid | 7 | 7 | 6 | 8 | 7 | 7 | 6.95 | 7.0 | 8/8/7/7/8 | 7.6 |
| [01-after-month-mobile-light](screenshots/kycalendar-01-after-month-mobile-light.png) | calendar-grid | 7 | 7 | 6 | 8 | 7 | 7 | 6.95 | 7.0 | 8/8/7/7/8 | 7.6 |
| [01-after-new-event-desktop-dark](screenshots/kycalendar-01-after-new-event-desktop-dark.png) | event-editor | 8 | 8 | 8 | 7 | 8 | 8 | 7.85 | 7.9 | 7/8/8/8/8 | 7.8 |
| [01-after-new-event-desktop-light](screenshots/kycalendar-01-after-new-event-desktop-light.png) | event-editor | 8 | 8 | 8 | 7 | 8 | 8 | 7.85 | 7.9 | 7/8/8/8/8 | 7.8 |
| [01-after-new-event-mobile-dark](screenshots/kycalendar-01-after-new-event-mobile-dark.png) | event-editor | 8 | 8 | 8 | 7 | 8 | 8 | 7.85 | 7.9 | 7/8/8/8/8 | 7.8 |
| [01-after-new-event-mobile-light](screenshots/kycalendar-01-after-new-event-mobile-light.png) | event-editor | 8 | 8 | 8 | 7 | 8 | 8 | 7.85 | 7.9 | 7/8/8/8/8 | 7.8 |
| [01-after-phones-and-apps-desktop-dark](screenshots/kycalendar-01-after-phones-and-apps-desktop-dark.png) | settings | 8 | 8 | 8 | 8 | 8 | 7 | 7.85 | 7.9 | 8/8/8/8/8 | 8.0 |
| [01-after-phones-and-apps-desktop-light](screenshots/kycalendar-01-after-phones-and-apps-desktop-light.png) | settings | 8 | 8 | 8 | 8 | 8 | 7 | 7.85 | 7.9 | 8/8/8/8/8 | 8.0 |
| [01-after-phones-and-apps-mobile-dark](screenshots/kycalendar-01-after-phones-and-apps-mobile-dark.png) | settings | 8 | 8 | 8 | 8 | 8 | 7 | 7.85 | 7.9 | 8/8/8/8/8 | 8.0 |
| [01-after-phones-and-apps-mobile-light](screenshots/kycalendar-01-after-phones-and-apps-mobile-light.png) | settings | 8 | 8 | 8 | 8 | 8 | 7 | 7.85 | 7.9 | 8/8/8/8/8 | 8.0 |
| [01-after-week-desktop-dark](screenshots/kycalendar-01-after-week-desktop-dark.png) | calendar-grid | 8 | 8 | 8 | 8 | 8 | 8 | 8.00 | 8.0 | 8/8/8/7/8 | 7.8 |
| [01-after-week-desktop-light](screenshots/kycalendar-01-after-week-desktop-light.png) | calendar-grid | 8 | 8 | 8 | 8 | 8 | 8 | 8.00 | 8.0 | 8/8/8/7/8 | 7.8 |
| [01-after-week-mobile-dark](screenshots/kycalendar-01-after-week-mobile-dark.png) | calendar-grid | 7 | 8 | 6 | 8 | 7 | 7 | 7.10 | 7.1 | 8/8/7/7/8 | 7.6 |
| [01-after-week-mobile-light](screenshots/kycalendar-01-after-week-mobile-light.png) | calendar-grid | 7 | 8 | 6 | 8 | 7 | 7 | 7.10 | 7.1 | 8/8/7/7/8 | 7.6 |
| [02-confirmation-appearance-desktop-dark](screenshots/kycalendar-02-confirmation-appearance-desktop-dark.png) | settings | 8 | 8 | 8 | 8 | 8 | 8 | 8.00 | 8.0 | 8/8/8/9/9 | 8.4 |
| [02-confirmation-appearance-desktop-light](screenshots/kycalendar-02-confirmation-appearance-desktop-light.png) | settings | 8 | 8 | 8 | 8 | 8 | 8 | 8.00 | 8.0 | 8/8/8/9/9 | 8.4 |
| [02-confirmation-appearance-mobile-dark](screenshots/kycalendar-02-confirmation-appearance-mobile-dark.png) | settings | 8 | 8 | 8 | 8 | 7 | 7 | 7.70 | 7.7 | 8/8/7/9/8 | 8.0 |
| [02-confirmation-appearance-mobile-light](screenshots/kycalendar-02-confirmation-appearance-mobile-light.png) | settings | 8 | 8 | 8 | 8 | 7 | 8 | 7.85 | 7.9 | 8/8/7/9/8 | 8.0 |

Per-dimension observations, confidence (all high), artifacts, caveats and capture conditions are in [opus-round2.json](opus-round2.json).

## Drift against my round 1

Mean delta all 48: 119/480 (0.247917). **22 byte-identical before/after pairs**: mean 3/110 (0.027273), range -3/10..3/10 — calibration only, not product improvement. 26 changed images: mean 113/260 (0.434615).

Byte-identical: 01-after-admin-overview-desktop-dark, 01-after-admin-overview-desktop-light, 01-after-admin-overview-mobile-dark, 01-after-admin-overview-mobile-light, 01-after-day-desktop-dark, 01-after-day-desktop-light, 01-after-list-desktop-dark, 01-after-list-desktop-light, 01-after-login-desktop-dark, 01-after-login-desktop-light, 01-after-login-mobile-dark, 01-after-login-mobile-light, 01-after-month-desktop-dark, 01-after-month-desktop-light, 01-after-new-event-desktop-dark, 01-after-new-event-desktop-light, 01-after-phones-and-apps-desktop-dark, 01-after-phones-and-apps-desktop-light, 01-after-phones-and-apps-mobile-dark, 01-after-phones-and-apps-mobile-light, 02-confirmation-appearance-desktop-dark, 02-confirmation-appearance-desktop-light.

| File | Prior | Now | Δ | Cause |
|---|---:|---:|---:|---|
| 01-after-admin-group-calendars-desktop-dark | 7.2 | 7.9 | 7/10 | Nested dashed empty box is gone and the contradictory "Every group has access" option is now a disabled "No groups available" select (coherence +2, hierarchy/density +1); affordances stay 7 because Add group still looks enabled. |
| 01-after-admin-group-calendars-desktop-light | 7.2 | 7.9 | 7/10 | Nested dashed empty box is gone and the contradictory "Every group has access" option is now a disabled "No groups available" select (coherence +2, hierarchy/density +1); affordances stay 7 because Add group still looks enabled. |
| 01-after-day-mobile-dark | 7.2 | 7.8 | 3/5 | Sign out now sits at the right edge of the phone header and the long title ellipsises on one line, so the round-1 header-misplacement penalty no longer applies (affordances +2, typography/coherence +1). |
| 01-after-day-mobile-light | 7.2 | 7.8 | 3/5 | Sign out now sits at the right edge of the phone header and the long title ellipsises on one line, so the round-1 header-misplacement penalty no longer applies (affordances +2, typography/coherence +1). |
| 01-after-default-phone-list-mobile-dark | 7.2 | 7.8 | 3/5 | Same phone header fix as Day: Sign out right-aligned, header no longer reads as misallocated (affordances +2, typography/coherence +1). |
| 01-after-default-phone-list-mobile-light | 7.2 | 7.8 | 3/5 | Same phone header fix as Day: Sign out right-aligned, header no longer reads as misallocated (affordances +2, typography/coherence +1). |
| 01-after-month-mobile-dark | 6.5 | 7.0 | 1/2 | Event chips now show title prefixes with ellipsis and all-day bars carry text instead of time-only dots and blank bars; still only 3-4 characters (typography, density, affordances +1 each). |
| 01-after-month-mobile-light | 6.5 | 7.0 | 1/2 | Event chips now show title prefixes with ellipsis and all-day bars carry text instead of time-only dots and blank bars; still only 3-4 characters (typography, density, affordances +1 each). |
| 01-after-week-desktop-dark | 7.5 | 8.0 | 1/2 | Timed titles now ellipsise on one line instead of being cut mid-glyph at the block bottom (typography, density, affordances +1). |
| 01-after-week-desktop-light | 7.5 | 8.0 | 1/2 | Timed titles now ellipsise on one line instead of being cut mid-glyph at the block bottom (typography, density, affordances +1). |
| 01-after-week-mobile-dark | 5.7 | 7.1 | 7/5 | Columns widened to ~102px inside an internally scrolling region with a hint, so titles are readable ("Design revi...", "Lunch break") instead of "Desi/revie" fragments (typography +4); density held at 6 because the view opens on empty Sun/Mon with today clipped. |
| 01-after-week-mobile-light | 5.7 | 7.1 | 7/5 | Columns widened to ~102px inside an internally scrolling region with a hint, so titles are readable ("Design revi...", "Lunch break") instead of "Desi/revie" fragments (typography +4); density held at 6 because the view opens on empty Sun/Mon with today clipped. |

## Round-1 objections

- **Unreadable phone Week labels** — resolved for legibility; new layout issue retained. Titles readable in ~102px columns. The view opens on empty Sun/Mon with today clipped at the right edge.
- **Phone Month time-only chips / blank all-day bars** — partly resolved; retained as minor. All-day bars and chips now carry text, but 51px cells leave 3-4 characters ('Tea...', 'Des...'). List remains the phone default.
- **Phone account control alignment** — resolved. Sign out sits at the right edge on every phone capture.
- **Time-grid title clipping mid-line** — resolved; refinement remains. Single-line ellipsis; tall blocks (1.5h 'Planning the co...') still show one line.
- **Nested group-calendar empty state** — resolved. Plain guidance line inside one card.
- **Misleading group dropdown label** — resolved; adjacent issue retained. Select reads 'No groups available' and is disabled; Add group beside it still renders enabled (GroupCalendars.tsx:221).
- **Event dialog focus return** — resolved per recorded probe (not testable from screenshots). Not a full keyboard/focus gate.
- **Login challenge churn** — resolved per recorded probe (not testable from screenshots). Screens show one Browser check passed line, consistent with this.

## Remaining issues

1. **Phone Week opens on empty days with today clipped** (7.1 / 7.1): Sun 25 and Mon 26 fill most of the 358px region; Tue 27 (today, with all its events) is cut at the right edge. Fix: On entering Week at <=720px, scroll the Week calendar region so today (or the first day with events) starts at the left edge. No layout change. [01-after-week-mobile-light](screenshots/kycalendar-01-after-week-mobile-light.png) [01-after-week-mobile-dark](screenshots/kycalendar-01-after-week-mobile-dark.png)
2. **Phone Month labels still 3-4 characters** (7.0 / 7.0): 'Tea...', 'Des...', 'Lun...', 'Pla...' in 51px cells; four empty weeks take most of the height. Fix: Lowest-cost option: accept as is (List is the phone default) or allow two-line chips. A larger redesign is out of scope. [01-after-month-mobile-light](screenshots/kycalendar-01-after-month-mobile-light.png) [01-after-month-mobile-dark](screenshots/kycalendar-01-after-month-mobile-dark.png)
3. **Phone calendar header stack pushes content to ~315px** (7.8 / 7.8 / 7.0): 'Times shown in America/New_York' wraps to two lines next to New event; toolbar and view switcher take two more rows. Fix: Let the time-zone note take the full width below the title row (or one line) to recover ~24px. [01-after-day-mobile-light](screenshots/kycalendar-01-after-day-mobile-light.png) [01-after-default-phone-list-mobile-light](screenshots/kycalendar-01-after-default-phone-list-mobile-light.png) [01-after-month-mobile-light](screenshots/kycalendar-01-after-month-mobile-light.png)
4. **Add group looks enabled while the Group select is disabled** (7.9 / 7.3): Greyed 'No groups available' select beside a normal outlined Add group button; on phone the controls end short of the card's right edge. Fix: disabled={available.length === 0} on Add group; on phone let Group/Role controls fill the card width. [01-after-admin-group-calendars-desktop-light](screenshots/kycalendar-01-after-admin-group-calendars-desktop-light.png) [01-after-admin-group-calendars-mobile-dark](screenshots/kycalendar-01-after-admin-group-calendars-mobile-dark.png)
5. **Tall time-grid events show a single title line** (8.0): 'Planning the co...' and 'Morning appoint...' truncate on one line inside blocks with room for a second. Fix: Line-clamp titles to the block height instead of a fixed single line; keep the single line for short events. [01-after-week-desktop-light](screenshots/kycalendar-01-after-week-desktop-light.png)
6. **Admin People row actions crowd and wrap** (7.8 / 7.6): Four 40px outlined buttons per row; on phone Disable wraps to its own line. Email column is all em dashes in this fixture. Fix: Optional: quieter tertiary style for Edit/Reset/Make admin, keeping Disable as danger outline. [01-after-admin-people-desktop-light](screenshots/kycalendar-01-after-admin-people-desktop-light.png) [01-after-admin-people-mobile-light](screenshots/kycalendar-01-after-admin-people-mobile-light.png)

Not recommended: Recolouring calendar checkboxes or today header (semantic, per round-1 audit); Changing New event focus (capture-driver artifact).

## Post-freeze notes (no vector changed)

- **C1** (correction): Observation text was reused from Month and mentions an accent today date; List shows no today accent. Its visible accent marks are New event, the nav rail, the List underline and the three calendar checkboxes.
- **C2** (correction): Observation text was reused from phone Month: it names the Month underline and Phones & apps clipping. These show the Day/Week/List underline; List has no accent today date. Scores were set by viewing each image and stand.
- **C3** (qualification): The round-1 audit (R2) and responses classify the orange visibility checkboxes and today header as semantic selection/current-date state that the product contract documents. I keep 7 as a brand-fit judgement (more orange marks than the shared contract's primary/selection/focus list), not a quality defect; it never enters progression.
- **C4** (artifact): Round-1 records (my C2, audit R7) show the End focus and blue segment selection come from the capture driver filling Start then End; the product focuses Title on open. Scored as captured per the rubric; not a product defect to fix. The driver, not the UI, should change if the parent wants a clean New event capture.
- **C5** (artifact (probable, unproven)): The raised fill on Two-step verification matches .app-nav button:hover (--panel-hover, theme.css:160 at d7e3b89). In Busnes light --panel-hover equals the sidebar surface (#f2efe7), which explains why the light capture shows nothing. Whether a stale hover survived the pointer move is unproven; the round-1 audit saw the same fill on the baseline.
- **C6** (confirmation): Source confirms the select is disabled when available.length === 0 (GroupCalendars.tsx:206-207) but the Add group submit button has no disabled state (GroupCalendars.tsx:221). The capture worker records the fixture has zero groups.
- **C7** (confirmation): CalendarPage.tsx at d7e3b89 renders the hint (:318) and labels the region 'Week calendar' (:341) but contains no scrollLeft/scrollIntoView, so the internal scroller opens at Sunday. Capture checks record a 358px region over a 768px grid.

## Gates

All five behaviour gates are **untested** by this reviewer. Recorded capture-worker probes: event focus return 4/4, phone Week ArrowRight 2/2, login challenge count stable 4/4, zero page overflow, phone Sign out right-aligned. Screenshots cannot pass a gate.

## Export compatibility

Checked after freeze. build-report.py asserts Fraction(str(row['total_unrounded'])) per row. My frozen rows carry the same value as weighted_total_unrounded (float, exact to 2 dp) and weighted_total_exact (fraction string), not total_unrounded. Adding the key would change the frozen array, so the array is unchanged. All other row checks (scores ints, total half-up, sha256/bytes vs inputs, file set, ky_fit float + ky_fit_scores/ky_fit_observations fallback, independent_satisfaction dict) pass in a read-only replica. build-report.py needs a one-line fallback, e.g. row.get('total_unrounded', row.get('weighted_total_unrounded')), mirroring its existing ky_fit fallback. I do not own that file and did not edit it.

## Limitations

- Screenshots show appearance only; keyboard, focus, contrast, states and responsive gates untested by this reviewer.
- Computed contrast and font identity unproven from pixels.
- Only the first viewport of long pages (Phones and apps, admin overview/people on phone) is visible.
- Real clock: today marker, sign-in times and the after-midnight Appearance recapture are capture effects, not product changes.
- Drift compares with my own round 1 only; rater calibration on 22 byte-identical images is +3/110 mean (range -0.3..+0.3), so sub-0.3 changes are within my noise.
- Frozen rows lack the total_unrounded key build-report.py reads (see schema_compatibility).
- Peer reports, historical/diagnostic images and review-state.json were never read or viewed. MySlop hand-off unavailable: credentials verified revoked (401), not retried.

# KyCalendar UI Council: Opus round 1 (00-before baseline)

Reviewer `claude-opus-5-5` · rubric `ky-suite-v2` · branch `ui/calendar-eight` · captured source `0f0155027a87f3510740afe1b524a96846002fd3` · schema `ky-ui-council-scores/2`. Raw data: [opus-round1.json](opus-round1.json).

## Result

- **Opus overall quality: 7.5** = mean of 48 unique rubric-rounded totals = `3617/480` = 7.53541(6) repeating.
- Unrounded-vector mean: `1201/160` = 7.50625 → 7.5.
- **Gate (half-up 1dp ≥ 8.0): not met** (7.5). Nothing is pooled with Sol, brand fit or history, and no cohort can veto the result. If the owner rejects the layout, the gate reopens. 9.0 is the longer-term target and is not assumed.
- **Ky brand fit (separate, never pooled): 7.7** = `123/16` = 7.6875. Signed-out Login shell is N/A and excluded, not zeroed.
- **Independent satisfaction: not satisfied.** Desktop everyday and settings surfaces are solid (7.5-8.3), but phone Week (5.7) and Month (6.5) are not readable and the phone header misplaces the account control. That is not refined suite quality yet. Separate from the gate.
- This covers Ky captures only and is not a ranking. It is not acceptance of behaviour or production readiness. Gemini was not used because its quota is known to be exhausted, and no request was made.

## Evidence handling

- Before rating: all 7 review-input pins and 3 authority pins matched; all 52 images (50 originals + 2 brand refs) matched SHA-256, byte count and PNG IHDR dimensions; inventory/review-inputs/artifacts/manifest file sets and hashes agree; 0 mismatches.
- I opened all 48 scored originals one at a time at full resolution, plus both KyIdentity brand references (brand fit only). I used no thumbnails or montages. I did not open the two excluded phone List near-duplicates (10 and 7 changed pixels, max delta 1).
- **Freeze:** the whole `files` array, canonical `json.dumps(files, ensure_ascii=False, sort_keys=True, separators=(',',':'), allow_nan=False).encode('utf-8')` with no newline. SHA-256 `3576acfb8e039337529c19c4a73d89bf3a74c6248424e85ea770ee4f1016e0c5`, **102803 bytes**, frozen at 2026-10-28T03:29:18+00:00. The freeze came before any source, capture-checks or parent audit was read. I never read review-state.json. I never read `sol-round1.*`. The array bytes were re-verified after the final write.
- Pre-freeze recalibration, reported candidly:
  - After viewing later phone captures, mobile default-phone-list and mobile day visible_affordances were set to 6 and their density_layout to 7 so the shared phone-header defects are scored consistently across calendar phone views.
  - Week desktop density_layout lowered 8->7 for the clipped multi-line event block once compared with Day.
  - No score was adjusted with reference to the 8.0 threshold; the overall mean was computed only after these vectors were fixed.
- Metadata: membership matches `inventory-v1.json` exactly. One state note: the existing-event captures show a filled form, but the inventory labels them `empty`. The label is kept and the difference is recorded per row.

## Means (n, exact fraction, half-up 1dp; unrounded-vector 1dp)

### Cohort (coverage, not a ranking)

| Group | n | Mean of rounded | 1dp | Unrounded 1dp |
|---|---|---|---|---|
| admin-console | 12 | `149/20` | 7.5 | 7.4 |
| auth | 4 | `81/10` | 8.1 | 8.1 |
| calendar-grid | 12 | `85/12` | 7.1 | 7.1 |
| calendar-list | 4 | `151/20` | 7.6 | 7.5 |
| event-editor | 8 | `153/20` | 7.7 | 7.6 |
| settings | 8 | `127/16` | 7.9 | 7.9 |

### Device

| Group | n | Mean of rounded | 1dp | Unrounded 1dp |
|---|---|---|---|---|
| desktop | 24 | `467/60` | 7.8 | 7.8 |
| mobile | 24 | `583/80` | 7.3 | 7.3 |

### Theme

| Group | n | Mean of rounded | 1dp | Unrounded 1dp |
|---|---|---|---|---|
| dark | 24 | `1807/240` | 7.5 | 7.5 |
| light | 24 | `181/24` | 7.5 | 7.5 |

### Role

| Group | n | Mean of rounded | 1dp | Unrounded 1dp |
|---|---|---|---|---|
| administrator | 12 | `149/20` | 7.5 | 7.4 |
| everyday | 32 | `2399/320` | 7.5 | 7.5 |
| signed-out | 4 | `81/10` | 8.1 | 8.1 |

### Surface

| Group | n | Mean of rounded | 1dp | Unrounded 1dp |
|---|---|---|---|---|
| admin-group-calendars | 4 | `71/10` | 7.1 | 7.1 |
| admin-overview | 4 | `77/10` | 7.7 | 7.7 |
| admin-people | 4 | `151/20` | 7.6 | 7.5 |
| appearance | 4 | `323/40` | 8.1 | 8.1 |
| day | 4 | `151/20` | 7.6 | 7.5 |
| default-phone-list | 2 | `36/5` | 7.2 | 7.2 |
| existing-event | 4 | `77/10` | 7.7 | 7.7 |
| list | 2 | `79/10` | 7.9 | 7.9 |
| login | 4 | `81/10` | 8.1 | 8.1 |
| month | 4 | `71/10` | 7.1 | 7.1 |
| new-event | 4 | `38/5` | 7.6 | 7.6 |
| phones-and-apps | 4 | `39/5` | 7.8 | 7.8 |
| week | 4 | `33/5` | 6.6 | 6.6 |

### Surface × viewport

| Group | n | Mean of rounded | 1dp | Unrounded 1dp |
|---|---|---|---|---|
| admin-group-calendars/desktop | 2 | `36/5` | 7.2 | 7.2 |
| admin-group-calendars/mobile | 2 | `7/1` | 7.0 | 7.0 |
| admin-overview/desktop | 2 | `39/5` | 7.8 | 7.8 |
| admin-overview/mobile | 2 | `38/5` | 7.6 | 7.6 |
| admin-people/desktop | 2 | `39/5` | 7.8 | 7.8 |
| admin-people/mobile | 2 | `73/10` | 7.3 | 7.3 |
| appearance/desktop | 2 | `83/10` | 8.3 | 8.3 |
| appearance/mobile | 2 | `157/20` | 7.9 | 7.9 |
| day/desktop | 2 | `79/10` | 7.9 | 7.9 |
| day/mobile | 2 | `36/5` | 7.2 | 7.2 |
| default-phone-list/mobile | 2 | `36/5` | 7.2 | 7.2 |
| existing-event/desktop | 2 | `77/10` | 7.7 | 7.7 |
| existing-event/mobile | 2 | `77/10` | 7.7 | 7.7 |
| list/desktop | 2 | `79/10` | 7.9 | 7.9 |
| login/desktop | 2 | `81/10` | 8.1 | 8.1 |
| login/mobile | 2 | `81/10` | 8.1 | 8.1 |
| month/desktop | 2 | `77/10` | 7.7 | 7.7 |
| month/mobile | 2 | `13/2` | 6.5 | 6.5 |
| new-event/desktop | 2 | `38/5` | 7.6 | 7.6 |
| new-event/mobile | 2 | `38/5` | 7.6 | 7.6 |
| phones-and-apps/desktop | 2 | `79/10` | 7.9 | 7.9 |
| phones-and-apps/mobile | 2 | `77/10` | 7.7 | 7.7 |
| week/desktop | 2 | `15/2` | 7.5 | 7.5 |
| week/mobile | 2 | `57/10` | 5.7 | 5.7 |

### Brand fit by cohort and dimension

| Cohort | n | Fraction | 1dp |
|---|---|---|---|
| admin-console | 12 | `47/6` | 7.8 |
| auth | 4 | `31/4` | 7.8 |
| calendar-grid | 12 | `221/30` | 7.4 |
| calendar-list | 4 | `37/5` | 7.4 |
| event-editor | 8 | `77/10` | 7.7 |
| settings | 8 | `161/20` | 8.1 |

| Dimension | n | Fraction | 1dp |
|---|---|---|---|
| palette_tokens | 48 | `97/12` | 8.1 |
| type_geometry | 48 | `23/3` | 7.7 |
| shell | 44 | `161/22` | 7.3 |
| accent_discipline | 48 | `31/4` | 7.8 |
| suite_cohesion | 48 | `91/12` | 7.6 |

## Top findings (source read after freeze)

1. **Phone Week view clips event titles mid-word** (5.7 / 5.7). 'Desi revie', 'Plani the comi work', 'Morr', 'Weel walk': titles are cut horizontally in the ~43px columns with no ellipsis.
   Images: [week-mobile-light.png](screenshots/kycalendar-00-before-week-mobile-light.png), [week-mobile-dark.png](screenshots/kycalendar-00-before-week-mobile-dark.png). Source: [`web/src/styles/calendar.css:102-104`](../../web/src/styles/calendar.css#L102).
   Fix: At <=720px in timeGrid views: hide the time line, show titles single-line with text-overflow: ellipsis (or line-clamp), and use a smaller min event padding.
2. **Phone shell: sign-out icon floats mid-header on calendar pages; nav tabs clip under fades** (7.2 / 7.2 / 7.7). Sign-out icon at x~272 on calendar pages but at the right edge elsewhere; "Phones &" fades under the Calendars disclosure; Calendar/Two-ste clip at both ends on Phones and apps.
   Images: [default-phone-list-mobile-light.png](screenshots/kycalendar-00-before-default-phone-list-mobile-light.png), [day-mobile-light.png](screenshots/kycalendar-00-before-day-mobile-light.png), [phones-and-apps-mobile-light.png](screenshots/kycalendar-00-before-phones-and-apps-mobile-light.png). Source: [`web/src/styles/theme.css:190`](../../web/src/styles/theme.css#L190), [`web/src/styles/theme.css:199`](../../web/src/styles/theme.css#L199).
   Fix: Add justify-content:end (or margin-inline-start:auto on .app-user) to phone .app-header-actions so the account control is always at the right; scroll the selected tab into view (scroll-padding exists) so the selected tab never sits under a fade.
3. **Phone Month shows time-only chips and textless all-day bars** (6.5 / 6.5). Events read as '9a', '10a', '3:30p' dots; the all-day bars carry no text; '3:30p' runs to the cell edge; four empty weeks dominate.
   Images: [month-mobile-light.png](screenshots/kycalendar-00-before-month-mobile-light.png), [month-mobile-dark.png](screenshots/kycalendar-00-before-month-mobile-dark.png). Source: [`web/src/styles/calendar.css:154-156`](../../web/src/styles/calendar.css#L154).
   Fix: On phone month, show ellipsized titles instead of times (keep the dot), so the deliberate compression still identifies events; keep the all-day title text in the bar.
4. **Group calendars: nested box-in-card and contradictory group placeholder** (7.2 / 7.0). Dashed empty-state box inside a bordered card, plus divider and inner form; the select reads 'Every group has access' directly under 'No group has access.'
   Images: [admin-group-calendars-desktop-light.png](screenshots/kycalendar-00-before-admin-group-calendars-desktop-light.png), [admin-group-calendars-mobile-light.png](screenshots/kycalendar-00-before-admin-group-calendars-mobile-light.png). Source: [`web/src/pages/GroupCalendars.tsx:173`](../../web/src/pages/GroupCalendars.tsx#L173), [`web/src/pages/GroupCalendars.tsx:207`](../../web/src/pages/GroupCalendars.tsx#L207).
   Fix: Render the compact empty state flat inside the card (no dashed border) and keep Delete calendar quiet. The placeholder copy correction (e.g. 'No groups to add') is a factual copy change and needs owner approval under the contract's Preserve rule.
5. **Desktop Week clips multi-line event text mid-glyph; accent spread in calendar sidebar** (7.5 / 7.7). '...preparing the' cut at the block bottom; orange appears on three calendar visibility checkboxes plus today numeral/header.
   Images: [week-desktop-light.png](screenshots/kycalendar-00-before-week-desktop-light.png), [month-desktop-light.png](screenshots/kycalendar-00-before-month-desktop-light.png). Source: [`web/src/styles/calendar.css:102`](../../web/src/styles/calendar.css#L102), [`web/src/styles/theme.css:290`](../../web/src/styles/theme.css#L290), [`web/src/styles/calendar.css:87-94`](../../web/src/styles/calendar.css#L87).
   Fix: line-clamp timeGrid titles to the block height; optionally scope calendar visibility checkboxes to ink, not accent (contract permits accent for selection, so this is optional).

Recorded behaviour failure: Measured by the capture worker, not by this reviewer: event dialog focus did not return to the List trigger in 4/4 conditions (capture-checks.json behavior_gates.keyboard). EventDialog.tsx:55-60 restores the element that was active at open; the cause of the failure is unproven.

## Recommended bounded batch

Scope: CSS-only refinement of existing UI, no new features, everyday/admin separation and the single left navigation region untouched.

- Phone header: pin account control right (theme.css:199) and keep the selected tab out of the fade
- Phone timeGrid (Week/Day): single-line ellipsized titles, hide time line when narrow (calendar.css:102-104)
- Phone Month: ellipsized titles instead of time-only chips; keep all-day bar text (calendar.css:154-156)
- Desktop timeGrid: line-clamp event titles to block height (calendar.css:102)
- Group calendars: flatten the nested empty state inside the card (GroupCalendars.tsx:173 styling)

Tracked separately:
- Focus-return-to-trigger behavior fix with a keyboard test (behavior gate, not visual)
- Placeholder copy 'Every group has access' -> owner approval required before change

Re-capture the 12 affected originals (week/month/day/list phone x2 themes, phone settings/admin x2 for header, week desktop x2, group calendars x4) under identical conditions.

## Post-freeze factual corrections (outside the frozen array; vectors unchanged)

- **C1 (correction)**, month/week/day/list desktop (8) and default-phone-list/month/week/day mobile (8): manifest visible_actions_forms records the Today button as disabled:true while today is in view. The frozen observations call it 'ambiguous'/'reads as disabled with no explicit reason'; it is correctly disabled. A residual point stands only that the disabled styling gives no reason.
- **C2 (artifact)**, new-event x4: End-field focus and browser-blue text selection were produced by the capture driver filling Start then End (capture.mjs:182-183). The product focuses Title on open (web/src/components/EventDialog.tsx:57, ref at :163). The artifact was listed and scored as captured, per the rubric.
- **C3 (confirmation)**, new-event / existing-event: Field-set difference is intentional code: Calendar select only for new events (EventDialog.tsx:179); Repeat only when scope==='all', and new events start at 'this' (EventDialog.tsx:38, :186). Adding Repeat to new events would be a feature change, out of scope.
- **C4 (confirmation)**, month mobile x2: Phone month hides event titles deliberately (web/src/styles/calendar.css:154); phone default view is List (web/src/pages/CalendarPage.tsx:295), so Month/Week on phone are explicit secondary choices.
- **C5 (confirmation)**, admin-group-calendars x4: 'Every group has access' is the placeholder whenever no group is still available (GroupCalendars.tsx:155,207). That includes the captured case with no grants and, it appears, no groups, which contradicts 'No group has access.' (:173). The fixture's group count is not recorded in the manifest, so 'zero groups' is inferred, not proven.

## Gates (screenshots cannot pass a gate)

- keyboard: **fail**. capture-checks.json behavior_gates.keyboard: Enter opens real event from List and Escape closes, but focus_returned_to_trigger=false in 4/4 (desktop/mobile x light/dark), source 0f01550.
- focus, contrast, states, responsive: **untested** by this reviewer. Zero horizontal overflow was recorded by the capture worker and does not count as a gate pass.

## Limitations

- Font identity and computed contrast unproven from pixels (manifest font probes report Space Grotesk loaded; that is capture-worker evidence, not this reviewer's measurement).
- Keyboard, focus, contrast, states and responsive gates not tested by this reviewer.
- Loading/error states absent from the capture set; not penalised as zero.
- Only the first viewport of long pages (Phones and apps, admin mobile) is visible.
- sol-round1.*, review-state.json, historical quality images and prior scores were never read.

## Raw scores (spreadsheet export)

| image | surface | cohort | viewport | theme | role | state | hierarchy | typography | density_layout | color | visible_affordances | coherence | total_unrounded | total | palette_tokens | type_geometry | shell | accent_discipline | suite_cohesion | ky_fit_fraction | ky_fit_1dp |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| [admin-group-calendars-desktop-dark.png](screenshots/kycalendar-00-before-admin-group-calendars-desktop-dark.png) | admin-group-calendars | admin-console | desktop | dark | administrator | sparse | 7 | 8 | 7 | 8 | 7 | 6 | 7.15 | 7.2 | 8 | 7 | 8 | 8 | 7 | 38/5 | 7.6 |
| [admin-group-calendars-desktop-light.png](screenshots/kycalendar-00-before-admin-group-calendars-desktop-light.png) | admin-group-calendars | admin-console | desktop | light | administrator | sparse | 7 | 8 | 7 | 8 | 7 | 6 | 7.15 | 7.2 | 8 | 7 | 8 | 8 | 7 | 38/5 | 7.6 |
| [admin-group-calendars-mobile-dark.png](screenshots/kycalendar-00-before-admin-group-calendars-mobile-dark.png) | admin-group-calendars | admin-console | mobile | dark | administrator | sparse | 7 | 8 | 6 | 8 | 7 | 6 | 6.95 | 7.0 | 8 | 7 | 7 | 8 | 7 | 37/5 | 7.4 |
| [admin-group-calendars-mobile-light.png](screenshots/kycalendar-00-before-admin-group-calendars-mobile-light.png) | admin-group-calendars | admin-console | mobile | light | administrator | sparse | 7 | 8 | 6 | 8 | 7 | 6 | 6.95 | 7.0 | 8 | 7 | 7 | 8 | 7 | 37/5 | 7.4 |
| [admin-overview-desktop-dark.png](screenshots/kycalendar-00-before-admin-overview-desktop-dark.png) | admin-overview | admin-console | desktop | dark | administrator | empty | 7 | 8 | 7 | 9 | 8 | 8 | 7.75 | 7.8 | 9 | 8 | 8 | 9 | 8 | 42/5 | 8.4 |
| [admin-overview-desktop-light.png](screenshots/kycalendar-00-before-admin-overview-desktop-light.png) | admin-overview | admin-console | desktop | light | administrator | empty | 7 | 8 | 7 | 9 | 8 | 8 | 7.75 | 7.8 | 9 | 8 | 8 | 9 | 8 | 42/5 | 8.4 |
| [admin-overview-mobile-dark.png](screenshots/kycalendar-00-before-admin-overview-mobile-dark.png) | admin-overview | admin-console | mobile | dark | administrator | empty | 7 | 8 | 7 | 9 | 7 | 8 | 7.6 | 7.6 | 9 | 8 | 7 | 9 | 8 | 41/5 | 8.2 |
| [admin-overview-mobile-light.png](screenshots/kycalendar-00-before-admin-overview-mobile-light.png) | admin-overview | admin-console | mobile | light | administrator | empty | 7 | 8 | 7 | 9 | 7 | 8 | 7.6 | 7.6 | 9 | 8 | 7 | 9 | 8 | 41/5 | 8.2 |
| [admin-people-desktop-dark.png](screenshots/kycalendar-00-before-admin-people-desktop-dark.png) | admin-people | admin-console | desktop | dark | administrator | sparse | 8 | 8 | 7 | 8 | 8 | 8 | 7.8 | 7.8 | 8 | 8 | 8 | 8 | 8 | 8/1 | 8.0 |
| [admin-people-desktop-light.png](screenshots/kycalendar-00-before-admin-people-desktop-light.png) | admin-people | admin-console | desktop | light | administrator | sparse | 8 | 8 | 7 | 8 | 8 | 8 | 7.8 | 7.8 | 8 | 8 | 8 | 8 | 8 | 8/1 | 8.0 |
| [admin-people-mobile-dark.png](screenshots/kycalendar-00-before-admin-people-mobile-dark.png) | admin-people | admin-console | mobile | dark | administrator | sparse | 7 | 8 | 6 | 8 | 8 | 7 | 7.25 | 7.3 | 8 | 7 | 7 | 8 | 7 | 37/5 | 7.4 |
| [admin-people-mobile-light.png](screenshots/kycalendar-00-before-admin-people-mobile-light.png) | admin-people | admin-console | mobile | light | administrator | sparse | 7 | 8 | 6 | 8 | 8 | 7 | 7.25 | 7.3 | 8 | 7 | 7 | 8 | 7 | 37/5 | 7.4 |
| [appearance-desktop-dark.png](screenshots/kycalendar-00-before-appearance-desktop-dark.png) | appearance | settings | desktop | dark | everyday | empty | 8 | 8 | 8 | 9 | 8 | 9 | 8.3 | 8.3 | 9 | 8 | 8 | 9 | 9 | 43/5 | 8.6 |
| [appearance-desktop-light.png](screenshots/kycalendar-00-before-appearance-desktop-light.png) | appearance | settings | desktop | light | everyday | empty | 8 | 8 | 8 | 9 | 8 | 9 | 8.3 | 8.3 | 9 | 8 | 8 | 9 | 9 | 43/5 | 8.6 |
| [appearance-mobile-dark.png](screenshots/kycalendar-00-before-appearance-mobile-dark.png) | appearance | settings | mobile | dark | everyday | empty | 8 | 8 | 8 | 8 | 6 | 8 | 7.7 | 7.7 | 9 | 8 | 7 | 9 | 8 | 41/5 | 8.2 |
| [appearance-mobile-light.png](screenshots/kycalendar-00-before-appearance-mobile-light.png) | appearance | settings | mobile | light | everyday | empty | 8 | 8 | 8 | 9 | 7 | 8 | 8 | 8.0 | 9 | 8 | 7 | 9 | 8 | 41/5 | 8.2 |
| [day-desktop-dark.png](screenshots/kycalendar-00-before-day-desktop-dark.png) | day | calendar-grid | desktop | dark | everyday | populated | 8 | 8 | 8 | 8 | 7 | 8 | 7.85 | 7.9 | 8 | 8 | 8 | 7 | 8 | 39/5 | 7.8 |
| [day-desktop-light.png](screenshots/kycalendar-00-before-day-desktop-light.png) | day | calendar-grid | desktop | light | everyday | populated | 8 | 8 | 8 | 8 | 7 | 8 | 7.85 | 7.9 | 8 | 8 | 8 | 7 | 8 | 39/5 | 7.8 |
| [day-mobile-dark.png](screenshots/kycalendar-00-before-day-mobile-dark.png) | day | calendar-grid | mobile | dark | everyday | populated | 8 | 7 | 7 | 8 | 6 | 7 | 7.2 | 7.2 | 8 | 7 | 6 | 7 | 7 | 7/1 | 7.0 |
| [day-mobile-light.png](screenshots/kycalendar-00-before-day-mobile-light.png) | day | calendar-grid | mobile | light | everyday | populated | 8 | 7 | 7 | 8 | 6 | 7 | 7.2 | 7.2 | 8 | 7 | 6 | 7 | 7 | 7/1 | 7.0 |
| [default-phone-list-mobile-dark.png](screenshots/kycalendar-00-before-default-phone-list-mobile-dark.png) | default-phone-list | calendar-list | mobile | dark | everyday | populated | 8 | 7 | 7 | 8 | 6 | 7 | 7.2 | 7.2 | 8 | 7 | 6 | 7 | 7 | 7/1 | 7.0 |
| [default-phone-list-mobile-light.png](screenshots/kycalendar-00-before-default-phone-list-mobile-light.png) | default-phone-list | calendar-list | mobile | light | everyday | populated | 8 | 7 | 7 | 8 | 6 | 7 | 7.2 | 7.2 | 8 | 7 | 6 | 7 | 7 | 7/1 | 7.0 |
| [existing-event-desktop-dark.png](screenshots/kycalendar-00-before-existing-event-desktop-dark.png) | existing-event | event-editor | desktop | dark | everyday | empty | 8 | 8 | 8 | 7 | 8 | 7 | 7.7 | 7.7 | 8 | 8 | 8 | 7 | 8 | 39/5 | 7.8 |
| [existing-event-desktop-light.png](screenshots/kycalendar-00-before-existing-event-desktop-light.png) | existing-event | event-editor | desktop | light | everyday | empty | 8 | 8 | 8 | 7 | 8 | 7 | 7.7 | 7.7 | 8 | 8 | 8 | 7 | 8 | 39/5 | 7.8 |
| [existing-event-mobile-dark.png](screenshots/kycalendar-00-before-existing-event-mobile-dark.png) | existing-event | event-editor | mobile | dark | everyday | empty | 8 | 8 | 8 | 7 | 8 | 7 | 7.7 | 7.7 | 8 | 8 | 7 | 7 | 8 | 38/5 | 7.6 |
| [existing-event-mobile-light.png](screenshots/kycalendar-00-before-existing-event-mobile-light.png) | existing-event | event-editor | mobile | light | everyday | empty | 8 | 8 | 8 | 7 | 8 | 7 | 7.7 | 7.7 | 8 | 8 | 7 | 7 | 8 | 38/5 | 7.6 |
| [list-desktop-dark.png](screenshots/kycalendar-00-before-list-desktop-dark.png) | list | calendar-list | desktop | dark | everyday | populated | 8 | 8 | 8 | 8 | 7 | 8 | 7.85 | 7.9 | 8 | 8 | 8 | 7 | 8 | 39/5 | 7.8 |
| [list-desktop-light.png](screenshots/kycalendar-00-before-list-desktop-light.png) | list | calendar-list | desktop | light | everyday | populated | 8 | 8 | 8 | 8 | 7 | 8 | 7.85 | 7.9 | 8 | 8 | 8 | 7 | 8 | 39/5 | 7.8 |
| [login-desktop-dark.png](screenshots/kycalendar-00-before-login-desktop-dark.png) | login | auth | desktop | dark | signed-out | empty | 9 | 8 | 8 | 8 | 8 | 7 | 8.05 | 8.1 | 8 | 8 | N/A | 8 | 7 | 31/4 | 7.8 |
| [login-desktop-light.png](screenshots/kycalendar-00-before-login-desktop-light.png) | login | auth | desktop | light | signed-out | empty | 9 | 8 | 8 | 8 | 8 | 7 | 8.05 | 8.1 | 8 | 8 | N/A | 8 | 7 | 31/4 | 7.8 |
| [login-mobile-dark.png](screenshots/kycalendar-00-before-login-mobile-dark.png) | login | auth | mobile | dark | signed-out | empty | 9 | 8 | 8 | 8 | 8 | 7 | 8.05 | 8.1 | 8 | 8 | N/A | 8 | 7 | 31/4 | 7.8 |
| [login-mobile-light.png](screenshots/kycalendar-00-before-login-mobile-light.png) | login | auth | mobile | light | signed-out | empty | 9 | 8 | 8 | 8 | 8 | 7 | 8.05 | 8.1 | 8 | 8 | N/A | 8 | 7 | 31/4 | 7.8 |
| [month-desktop-dark.png](screenshots/kycalendar-00-before-month-desktop-dark.png) | month | calendar-grid | desktop | dark | everyday | populated | 8 | 8 | 7 | 8 | 7 | 8 | 7.65 | 7.7 | 8 | 8 | 8 | 7 | 8 | 39/5 | 7.8 |
| [month-desktop-light.png](screenshots/kycalendar-00-before-month-desktop-light.png) | month | calendar-grid | desktop | light | everyday | populated | 8 | 8 | 7 | 8 | 7 | 8 | 7.65 | 7.7 | 8 | 8 | 8 | 7 | 8 | 39/5 | 7.8 |
| [month-mobile-dark.png](screenshots/kycalendar-00-before-month-mobile-dark.png) | month | calendar-grid | mobile | dark | everyday | populated | 7 | 6 | 5 | 8 | 6 | 7 | 6.45 | 6.5 | 8 | 7 | 6 | 7 | 7 | 7/1 | 7.0 |
| [month-mobile-light.png](screenshots/kycalendar-00-before-month-mobile-light.png) | month | calendar-grid | mobile | light | everyday | populated | 7 | 6 | 5 | 8 | 6 | 7 | 6.45 | 6.5 | 8 | 7 | 6 | 7 | 7 | 7/1 | 7.0 |
| [new-event-desktop-dark.png](screenshots/kycalendar-00-before-new-event-desktop-dark.png) | new-event | event-editor | desktop | dark | everyday | empty | 8 | 8 | 8 | 7 | 7 | 7 | 7.55 | 7.6 | 7 | 8 | 8 | 8 | 8 | 39/5 | 7.8 |
| [new-event-desktop-light.png](screenshots/kycalendar-00-before-new-event-desktop-light.png) | new-event | event-editor | desktop | light | everyday | empty | 8 | 8 | 8 | 7 | 7 | 7 | 7.55 | 7.6 | 7 | 8 | 8 | 8 | 8 | 39/5 | 7.8 |
| [new-event-mobile-dark.png](screenshots/kycalendar-00-before-new-event-mobile-dark.png) | new-event | event-editor | mobile | dark | everyday | empty | 8 | 8 | 8 | 7 | 7 | 7 | 7.55 | 7.6 | 7 | 8 | 7 | 8 | 8 | 38/5 | 7.6 |
| [new-event-mobile-light.png](screenshots/kycalendar-00-before-new-event-mobile-light.png) | new-event | event-editor | mobile | light | everyday | empty | 8 | 8 | 8 | 7 | 7 | 7 | 7.55 | 7.6 | 7 | 8 | 7 | 8 | 8 | 38/5 | 7.6 |
| [phones-and-apps-desktop-dark.png](screenshots/kycalendar-00-before-phones-and-apps-desktop-dark.png) | phones-and-apps | settings | desktop | dark | everyday | empty | 8 | 8 | 8 | 8 | 8 | 7 | 7.85 | 7.9 | 8 | 8 | 8 | 8 | 7 | 39/5 | 7.8 |
| [phones-and-apps-desktop-light.png](screenshots/kycalendar-00-before-phones-and-apps-desktop-light.png) | phones-and-apps | settings | desktop | light | everyday | empty | 8 | 8 | 8 | 8 | 8 | 7 | 7.85 | 7.9 | 8 | 8 | 8 | 8 | 7 | 39/5 | 7.8 |
| [phones-and-apps-mobile-dark.png](screenshots/kycalendar-00-before-phones-and-apps-mobile-dark.png) | phones-and-apps | settings | mobile | dark | everyday | empty | 8 | 8 | 8 | 8 | 7 | 7 | 7.7 | 7.7 | 8 | 8 | 7 | 8 | 7 | 38/5 | 7.6 |
| [phones-and-apps-mobile-light.png](screenshots/kycalendar-00-before-phones-and-apps-mobile-light.png) | phones-and-apps | settings | mobile | light | everyday | empty | 8 | 8 | 8 | 8 | 7 | 7 | 7.7 | 7.7 | 8 | 8 | 7 | 8 | 7 | 38/5 | 7.6 |
| [week-desktop-dark.png](screenshots/kycalendar-00-before-week-desktop-dark.png) | week | calendar-grid | desktop | dark | everyday | populated | 8 | 7 | 7 | 8 | 7 | 8 | 7.5 | 7.5 | 8 | 8 | 8 | 7 | 8 | 39/5 | 7.8 |
| [week-desktop-light.png](screenshots/kycalendar-00-before-week-desktop-light.png) | week | calendar-grid | desktop | light | everyday | populated | 8 | 7 | 7 | 8 | 7 | 8 | 7.5 | 7.5 | 8 | 8 | 8 | 7 | 8 | 39/5 | 7.8 |
| [week-mobile-dark.png](screenshots/kycalendar-00-before-week-mobile-dark.png) | week | calendar-grid | mobile | dark | everyday | populated | 7 | 4 | 4 | 7 | 5 | 7 | 5.65 | 5.7 | 8 | 6 | 6 | 7 | 7 | 34/5 | 6.8 |
| [week-mobile-light.png](screenshots/kycalendar-00-before-week-mobile-light.png) | week | calendar-grid | mobile | light | everyday | populated | 7 | 4 | 4 | 7 | 5 | 7 | 5.65 | 5.7 | 8 | 6 | 6 | 7 | 7 | 34/5 | 6.8 |

## Per-file observations (frozen)

### [admin-group-calendars-desktop-dark.png](screenshots/kycalendar-00-before-admin-group-calendars-desktop-dark.png): 7.2 (unrounded 7.15), ky_fit 7.6

admin-group-calendars · admin-console · sparse · dark · desktop 1280×900 DPR1 · administrator · sha256 `d6597e5ea6de9c1d…` · 70413 B · artifacts: none · confidence high

- hierarchy **7**: Title and long intro; orange 'Create' sits inline after the name/holds fields while 'Delete calendar' is prominent in the card header.
- typography **8**: Card title 'Shared planning' with muted 'No access yet'; labels bold 13px.
- density_layout **7**: Create form, then a card containing a dashed empty box, a divider and an add-group form; lower half of the page empty.
- color **8**: Restrained; brown calendar dot is data; danger outline for delete.
- visible_affordances **7**: Group select defaults to 'Every group has access' directly under 'No group has access.', which reads as contradictory state.
- coherence **6**: Dashed empty-state box nested inside the bordered card plus an inner form row: box-in-box composition.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [admin-group-calendars-desktop-light.png](screenshots/kycalendar-00-before-admin-group-calendars-desktop-light.png): 7.2 (unrounded 7.15), ky_fit 7.6

admin-group-calendars · admin-console · sparse · light · desktop 1280×900 DPR1 · administrator · sha256 `5ea90e7d2de9626f…` · 70213 B · artifacts: none · confidence high

- hierarchy **7**: Title and long intro; orange 'Create' sits inline after the name/holds fields while 'Delete calendar' is prominent in the card header.
- typography **8**: Card title 'Shared planning' with muted 'No access yet'; labels bold 13px.
- density_layout **7**: Create form, then a card containing a dashed empty box, a divider and an add-group form; lower half of the page empty.
- color **8**: Restrained; brown calendar dot is data; danger outline for delete.
- visible_affordances **7**: Group select defaults to 'Every group has access' directly under 'No group has access.', which reads as contradictory state.
- coherence **6**: Dashed empty-state box nested inside the bordered card plus an inner form row: box-in-box composition.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [admin-group-calendars-mobile-dark.png](screenshots/kycalendar-00-before-admin-group-calendars-mobile-dark.png): 7.0 (unrounded 6.95), ky_fit 7.4

admin-group-calendars · admin-console · sparse · dark · mobile 390×844 DPR1 · administrator · sha256 `e952a3e7b3e4c10a…` · 47969 B · artifacts: none · confidence high

- hierarchy **7**: Long five-line intro pushes the Create row to y~400; Delete calendar again prominent.
- typography **8**: Type steps match desktop.
- density_layout **6**: Group select stops short of full width while other fields are full width; card content continues past the fold.
- color **8**: Restrained; same data/danger colours.
- visible_affordances **7**: Same 'Every group has access' vs 'No group has access.' ambiguity; nav clips 'Sign-in'.
- coherence **6**: Same nested dashed box inside card.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [admin-group-calendars-mobile-light.png](screenshots/kycalendar-00-before-admin-group-calendars-mobile-light.png): 7.0 (unrounded 6.95), ky_fit 7.4

admin-group-calendars · admin-console · sparse · light · mobile 390×844 DPR1 · administrator · sha256 `912510c2260e2f62…` · 47578 B · artifacts: none · confidence high

- hierarchy **7**: Long five-line intro pushes the Create row to y~400; Delete calendar again prominent.
- typography **8**: Type steps match desktop.
- density_layout **6**: Group select stops short of full width while other fields are full width; card content continues past the fold.
- color **8**: Restrained; same data/danger colours.
- visible_affordances **7**: Same 'Every group has access' vs 'No group has access.' ambiguity; nav clips 'Sign-in'.
- coherence **6**: Same nested dashed box inside card.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [admin-overview-desktop-dark.png](screenshots/kycalendar-00-before-admin-overview-desktop-dark.png): 7.8 (unrounded 7.75), ky_fit 8.4

admin-overview · admin-console · empty · dark · desktop 1280×900 DPR1 · administrator · sha256 `b8d5c8b7f2f32800…` · 87904 B · artifacts: none · confidence high

- hierarchy **7**: 'Welcome, Administrator' title with a 3x2 grid of cards, each with an equal-weight outline button; no single lead action.
- typography **8**: Card titles with icons, muted descriptions, small status pills ('Passwords only', 'Off').
- density_layout **7**: Even card grid, but it ends at y~580 and leaves the lower third empty; cards repeat the sidebar destinations.
- color **9**: Almost entirely neutral; orange only on the selected Overview nav row.
- visible_affordances **8**: Outline 'Manage ...' buttons with arrows and status pills read clearly.
- coherence **8**: Consistent card/button vocabulary; admin nav clearly separate from everyday Calendar nav.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [admin-overview-desktop-light.png](screenshots/kycalendar-00-before-admin-overview-desktop-light.png): 7.8 (unrounded 7.75), ky_fit 8.4

admin-overview · admin-console · empty · light · desktop 1280×900 DPR1 · administrator · sha256 `14a2abe19d540c35…` · 87772 B · artifacts: none · confidence high

- hierarchy **7**: 'Welcome, Administrator' title with a 3x2 grid of cards, each with an equal-weight outline button; no single lead action.
- typography **8**: Card titles with icons, muted descriptions, small status pills ('Passwords only', 'Off').
- density_layout **7**: Even card grid, but it ends at y~580 and leaves the lower third empty; cards repeat the sidebar destinations.
- color **9**: Almost entirely neutral; orange only on the selected Overview nav row.
- visible_affordances **8**: Outline 'Manage ...' buttons with arrows and status pills read clearly.
- coherence **8**: Consistent card/button vocabulary; admin nav clearly separate from everyday Calendar nav.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [admin-overview-mobile-dark.png](screenshots/kycalendar-00-before-admin-overview-mobile-dark.png): 7.6 (unrounded 7.6), ky_fit 8.2

admin-overview · admin-console · empty · dark · mobile 390×844 DPR1 · administrator · sha256 `cb2305a70978d42e…` · 44536 B · artifacts: none · confidence high

- hierarchy **7**: Title and stacked cards; no lead action, as on desktop.
- typography **8**: Same type steps; descriptions wrap cleanly.
- density_layout **7**: Generous card padding means only ~2.5 of 6 cards are visible; long scroll.
- color **9**: Neutral with orange only on the selected Overview tab.
- visible_affordances **7**: Nav strip shows a clipped fourth icon at the right edge; no account name/role visible on phone.
- coherence **8**: Same cards and buttons as desktop.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [admin-overview-mobile-light.png](screenshots/kycalendar-00-before-admin-overview-mobile-light.png): 7.6 (unrounded 7.6), ky_fit 8.2

admin-overview · admin-console · empty · light · mobile 390×844 DPR1 · administrator · sha256 `44178d205dd16535…` · 43681 B · artifacts: none · confidence high

- hierarchy **7**: Title and stacked cards; no lead action, as on desktop.
- typography **8**: Same type steps; descriptions wrap cleanly.
- density_layout **7**: Generous card padding means only ~2.5 of 6 cards are visible; long scroll.
- color **9**: Neutral with orange only on the selected Overview tab.
- visible_affordances **7**: Nav strip shows a clipped fourth icon at the right edge; no account name/role visible on phone.
- coherence **8**: Same cards and buttons as desktop.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [admin-people-desktop-dark.png](screenshots/kycalendar-00-before-admin-people-desktop-dark.png): 7.8 (unrounded 7.8), ky_fit 8.0

admin-people · admin-console · sparse · dark · desktop 1280×900 DPR1 · administrator · sha256 `6b52a139eda11f33…` · 73329 B · artifacts: none · confidence high

- hierarchy **8**: 'People' title with filled orange 'Add person' top-right; search field and table below.
- typography **8**: Column headers 13px muted, names bold with muted 'casey · Everyday' meta.
- density_layout **7**: Each row carries up to four 40px outline buttons; with unequal action counts, Edit sits at different x positions per row, which will scale noisily.
- color **8**: Green 'Active' status pills with text; Disable in danger outline; otherwise neutral.
- visible_affordances **8**: Buttons, search, status pills and the danger action read correctly.
- coherence **8**: Consistent controls; admin-only actions on an admin-only surface.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [admin-people-desktop-light.png](screenshots/kycalendar-00-before-admin-people-desktop-light.png): 7.8 (unrounded 7.8), ky_fit 8.0

admin-people · admin-console · sparse · light · desktop 1280×900 DPR1 · administrator · sha256 `0f4351ad491aab51…` · 72961 B · artifacts: none · confidence high

- hierarchy **8**: 'People' title with filled orange 'Add person' top-right; search field and table below.
- typography **8**: Column headers 13px muted, names bold with muted 'casey · Everyday' meta.
- density_layout **7**: Each row carries up to four 40px outline buttons; with unequal action counts, Edit sits at different x positions per row, which will scale noisily.
- color **8**: Green 'Active' status pills with text; Disable in danger outline; otherwise neutral.
- visible_affordances **8**: Buttons, search, status pills and the danger action read correctly.
- coherence **8**: Consistent controls; admin-only actions on an admin-only surface.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [admin-people-mobile-dark.png](screenshots/kycalendar-00-before-admin-people-mobile-dark.png): 7.3 (unrounded 7.25), ky_fit 7.4

admin-people · admin-console · sparse · dark · mobile 390×844 DPR1 · administrator · sha256 `3358131a44f927cd…` · 46733 B · artifacts: none · confidence high

- hierarchy **7**: Orange 'Add person' follows a six-line intro paragraph, so the list starts below y~460.
- typography **8**: Label/value grid per person is legible.
- density_layout **6**: Row actions wrap with 'Disable' alone on a second line; only ~1.3 people fit above the fold.
- color **8**: Semantic green status and danger outline only.
- visible_affordances **8**: All controls clear; danger action visibly distinct.
- coherence **7**: Per-person cards with label/value grids differ from the desktop table but stay within one vocabulary; wrapped button row looks accidental.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [admin-people-mobile-light.png](screenshots/kycalendar-00-before-admin-people-mobile-light.png): 7.3 (unrounded 7.25), ky_fit 7.4

admin-people · admin-console · sparse · light · mobile 390×844 DPR1 · administrator · sha256 `d860dc0259c56d6f…` · 46191 B · artifacts: none · confidence high

- hierarchy **7**: Orange 'Add person' follows a six-line intro paragraph, so the list starts below y~460.
- typography **8**: Label/value grid per person is legible.
- density_layout **6**: Row actions wrap with 'Disable' alone on a second line; only ~1.3 people fit above the fold.
- color **8**: Semantic green status and danger outline only.
- visible_affordances **8**: All controls clear; danger action visibly distinct.
- coherence **7**: Per-person cards with label/value grids differ from the desktop table but stay within one vocabulary; wrapped button row looks accidental.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [appearance-desktop-dark.png](screenshots/kycalendar-00-before-appearance-desktop-dark.png): 8.3 (unrounded 8.3), ky_fit 8.6

appearance · settings · empty · dark · desktop 1280×900 DPR1 · everyday · sha256 `eaf70d9c310f9ad8…` · 69013 B · artifacts: none · confidence high

- hierarchy **8**: 'Appearance' title, explanatory sentence, 'Theme' label and a 5-column card grid with Match system selected; no primary needed (applies immediately).
- typography **8**: Title, body, 13px label and card name/meta pairs mirror the KyIdentity reference.
- density_layout **8**: Even 5x5 grid with consistent gutters; description sits tight under the title (~8px) compared with the reference's larger gap.
- color **9**: Theme previews are content; chrome is neutral with orange only on the selected card border/radio and nav rail.
- visible_affordances **8**: Selected card has orange border and filled radio; unselected radios are small (~14px) but clear.
- coherence **9**: Matches the shared Appearance picker vocabulary; calendar list correctly absent from the sidebar here.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [appearance-desktop-light.png](screenshots/kycalendar-00-before-appearance-desktop-light.png): 8.3 (unrounded 8.3), ky_fit 8.6

appearance · settings · empty · light · desktop 1280×900 DPR1 · everyday · sha256 `856d1d3a96175b6a…` · 68309 B · artifacts: none · confidence high

- hierarchy **8**: 'Appearance' title, explanatory sentence, 'Theme' label and a 5-column card grid with Match system selected; no primary needed (applies immediately).
- typography **8**: Title, body, 13px label and card name/meta pairs mirror the KyIdentity reference.
- density_layout **8**: Even 5x5 grid with consistent gutters; description sits tight under the title (~8px) compared with the reference's larger gap.
- color **9**: Theme previews are content; chrome is neutral with orange only on the selected card border/radio and nav rail.
- visible_affordances **8**: Selected card has orange border and filled radio; unselected radios are small (~14px) but clear.
- coherence **9**: Matches the shared Appearance picker vocabulary; calendar list correctly absent from the sidebar here.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [appearance-mobile-dark.png](screenshots/kycalendar-00-before-appearance-mobile-dark.png): 7.7 (unrounded 7.7), ky_fit 8.2

appearance · settings · empty · dark · mobile 390×844 DPR1 · everyday · sha256 `b20fe2e52c870c42…` · 39405 B · artifacts: none · confidence high

- hierarchy **8**: Title, explanation and 2-column theme cards with Match system selected.
- typography **8**: Same type steps as desktop; names and meta wrap consistently.
- density_layout **8**: Two-column grid with even gutters; the list continues below the fold as expected.
- color **8**: Neutral chrome, but the 'Two-step verification' tab carries a raised panel fill next to the selected Appearance tab.
- visible_affordances **6**: Nav strip is scrolled so only 'Two-step verification' and 'Appearance' show; sign-out icon now at the right edge, unlike calendar pages. Dark: the filled Two-step tab looks like a second selected/hover state.
- coherence **8**: Cards match desktop and the reference.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [appearance-mobile-light.png](screenshots/kycalendar-00-before-appearance-mobile-light.png): 8.0 (unrounded 8), ky_fit 8.2

appearance · settings · empty · light · mobile 390×844 DPR1 · everyday · sha256 `e09ec8eb31549e60…` · 38515 B · artifacts: none · confidence high

- hierarchy **8**: Title, explanation and 2-column theme cards with Match system selected.
- typography **8**: Same type steps as desktop; names and meta wrap consistently.
- density_layout **8**: Two-column grid with even gutters; the list continues below the fold as expected.
- color **9**: Neutral chrome, orange on selected card and Appearance tab.
- visible_affordances **7**: Nav strip is scrolled so only 'Two-step verification' and 'Appearance' show; sign-out icon now at the right edge, unlike calendar pages.
- coherence **8**: Cards match desktop and the reference.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [day-desktop-dark.png](screenshots/kycalendar-00-before-day-desktop-dark.png): 7.9 (unrounded 7.85), ky_fit 7.8

day · calendar-grid · populated · dark · desktop 1280×900 DPR1 · everyday · sha256 `497bd1713f1fe939…` · 63640 B · artifacts: none · confidence high

- hierarchy **8**: Title, single orange New event, and 'October 27, 2026' heading with Day segment selected; nothing competes.
- typography **8**: Event labels (12px time, bold 13px title) are legible; the long workshop title fits on one line.
- density_layout **8**: Full-width day column with all-day bar and three events spaced on even hour rows; grid fills the viewport height.
- color **8**: Whole day column carries the today tint; orange limited to primary, nav rail, Day underline, day header and checkboxes.
- visible_affordances **7**: Selected segment and all-day bar clear; Today grey state still ambiguous.
- coherence **8**: Same component set as Month/Week; no stray styles.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [day-desktop-light.png](screenshots/kycalendar-00-before-day-desktop-light.png): 7.9 (unrounded 7.85), ky_fit 7.8

day · calendar-grid · populated · light · desktop 1280×900 DPR1 · everyday · sha256 `34a03d0a2aee8f16…` · 63694 B · artifacts: none · confidence high

- hierarchy **8**: Title, single orange New event, and 'October 27, 2026' heading with Day segment selected; nothing competes.
- typography **8**: Event labels (12px time, bold 13px title) are legible; the long workshop title fits on one line.
- density_layout **8**: Full-width day column with all-day bar and three events spaced on even hour rows; grid fills the viewport height.
- color **8**: Whole day column carries the today tint; orange limited to primary, nav rail, Day underline, day header and checkboxes.
- visible_affordances **7**: Selected segment and all-day bar clear; Today grey state still ambiguous.
- coherence **8**: Same component set as Month/Week; no stray styles.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [day-mobile-dark.png](screenshots/kycalendar-00-before-day-mobile-dark.png): 7.2 (unrounded 7.2), ky_fit 7.0

day · calendar-grid · populated · dark · mobile 390×844 DPR1 · everyday · sha256 `e503202569c3e367…` · 39778 B · artifacts: none · confidence high

- hierarchy **8**: Title, New event and 'October 27, 2026' with Day selected give a clear lead.
- typography **7**: Event labels legible and the long title wraps over two lines; timezone caption wraps awkwardly under the title.
- density_layout **7**: ~37% of the viewport is chrome before the grid; the day grid itself is evenly spaced.
- color **8**: Restrained; today tint and orange header line as on desktop.
- visible_affordances **6**: Same phone-header ambiguity: clipped 'Phones &' tab and floating unlabeled sign-out icon; Today looks disabled.
- coherence **7**: Consistent with desktop Day apart from the phone header mix.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [day-mobile-light.png](screenshots/kycalendar-00-before-day-mobile-light.png): 7.2 (unrounded 7.2), ky_fit 7.0

day · calendar-grid · populated · light · mobile 390×844 DPR1 · everyday · sha256 `af749f175745eb86…` · 39783 B · artifacts: none · confidence high

- hierarchy **8**: Title, New event and 'October 27, 2026' with Day selected give a clear lead.
- typography **7**: Event labels legible and the long title wraps over two lines; timezone caption wraps awkwardly under the title.
- density_layout **7**: ~37% of the viewport is chrome before the grid; the day grid itself is evenly spaced.
- color **8**: Restrained; today tint and orange header line as on desktop.
- visible_affordances **6**: Same phone-header ambiguity: clipped 'Phones &' tab and floating unlabeled sign-out icon; Today looks disabled.
- coherence **7**: Consistent with desktop Day apart from the phone header mix.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [default-phone-list-mobile-dark.png](screenshots/kycalendar-00-before-default-phone-list-mobile-dark.png): 7.2 (unrounded 7.2), ky_fit 7.0

default-phone-list · calendar-list · populated · dark · mobile 390×844 DPR1 · everyday · sha256 `5611cea03c6fbda6…` · 59058 B · artifacts: none · confidence high

- hierarchy **8**: 'Calendar' title with orange 'New event' to its right; List segment selected; day-grouped list begins at y~315.
- typography **7**: Title/body/meta steps are clear, but the timezone caption wraps to 'Times shown in / America/New_York' under the title.
- density_layout **7**: Header uses ~315px (logo row, horizontal nav, title, toolbar, segment) before content; list rows themselves are even and the long workshop title wraps cleanly.
- color **8**: Restrained cream/charcoal; orange on primary, selected Calendar tab underline and List underline.
- visible_affordances **6**: Horizontal nav clips 'Phones &' under a fade beside the 'Calendars' disclosure; an unlabeled sign-out icon floats mid-header at x~272, not at the right edge.
- coherence **7**: Phone header mixes a scroll-tab strip, a disclosure button and a stray icon button; content components match desktop.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven. Inherits duplicate kycalendar-00-before-list-mobile-dark.png (10/7 px diff, max delta 1); that duplicate was not opened.

### [default-phone-list-mobile-light.png](screenshots/kycalendar-00-before-default-phone-list-mobile-light.png): 7.2 (unrounded 7.2), ky_fit 7.0

default-phone-list · calendar-list · populated · light · mobile 390×844 DPR1 · everyday · sha256 `797f4ab1af744b97…` · 59107 B · artifacts: none · confidence high

- hierarchy **8**: 'Calendar' title with orange 'New event' to its right; List segment selected; day-grouped list begins at y~315.
- typography **7**: Title/body/meta steps are clear, but the timezone caption wraps to 'Times shown in / America/New_York' under the title.
- density_layout **7**: Header uses ~315px (logo row, horizontal nav, title, toolbar, segment) before content; list rows themselves are even and the long workshop title wraps cleanly.
- color **8**: Restrained cream/charcoal; orange on primary, selected Calendar tab underline and List underline.
- visible_affordances **6**: Horizontal nav clips 'Phones &' under a fade beside the 'Calendars' disclosure; an unlabeled sign-out icon floats mid-header at x~272, not at the right edge.
- coherence **7**: Phone header mixes a scroll-tab strip, a disclosure button and a stray icon button; content components match desktop.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven. Inherits duplicate kycalendar-00-before-list-mobile-light.png (10/7 px diff, max delta 1); that duplicate was not opened.

### [existing-event-desktop-dark.png](screenshots/kycalendar-00-before-existing-event-desktop-dark.png): 7.7 (unrounded 7.7), ky_fit 7.8

existing-event · event-editor · empty · dark · desktop 1280×900 DPR1 · everyday · sha256 `d0f3835470fa02f3…` · 105972 B · artifacts: modal scrim over calendar, visible focus ring · confidence high

- hierarchy **8**: 'Edit event' modal; Save (filled) and Cancel right, Delete separated on the left; focus in Title.
- typography **8**: Same clear label/field type steps as New event; filled values legible.
- density_layout **8**: Fields evenly spaced; footer separated by a quiet rule; fits viewport.
- color **7**: Delete uses the danger hue, which sits close to the orange accent; in dark theme the salmon Delete text and orange Save are near-identical hues.
- visible_affordances **8**: Delete outline, Cancel outline, Save fill and Title focus ring are distinct at a glance.
- coherence **7**: Native datetime/select controls; field set shows Repeat but not Calendar, differing from New event.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [existing-event-desktop-light.png](screenshots/kycalendar-00-before-existing-event-desktop-light.png): 7.7 (unrounded 7.7), ky_fit 7.8

existing-event · event-editor · empty · light · desktop 1280×900 DPR1 · everyday · sha256 `627c5b7d96823850…` · 109464 B · artifacts: modal scrim over calendar, visible focus ring · confidence high

- hierarchy **8**: 'Edit event' modal; Save (filled) and Cancel right, Delete separated on the left; focus in Title.
- typography **8**: Same clear label/field type steps as New event; filled values legible.
- density_layout **8**: Fields evenly spaced; footer separated by a quiet rule; fits viewport.
- color **7**: Delete uses the danger hue, which sits close to the orange accent; in dark theme the salmon Delete text and orange Save are near-identical hues.
- visible_affordances **8**: Delete outline, Cancel outline, Save fill and Title focus ring are distinct at a glance.
- coherence **7**: Native datetime/select controls; field set shows Repeat but not Calendar, differing from New event.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [existing-event-mobile-dark.png](screenshots/kycalendar-00-before-existing-event-mobile-dark.png): 7.7 (unrounded 7.7), ky_fit 7.6

existing-event · event-editor · empty · dark · mobile 390×844 DPR1 · everyday · sha256 `370aabaa2ea1a948…` · 42075 B · artifacts: modal scrim over calendar, visible focus ring · confidence high

- hierarchy **8**: 'Edit event' modal; Save (filled) and Cancel right, Delete separated on the left; focus in Title.
- typography **8**: Same clear label/field type steps as New event; filled values legible.
- density_layout **8**: Fields evenly spaced; footer separated by a quiet rule; fits viewport.
- color **7**: Delete uses the danger hue, which sits close to the orange accent; in dark theme the salmon Delete text and orange Save are near-identical hues.
- visible_affordances **8**: Delete outline, Cancel outline, Save fill and Title focus ring are distinct at a glance.
- coherence **7**: Native datetime/select controls; field set shows Repeat but not Calendar, differing from New event.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [existing-event-mobile-light.png](screenshots/kycalendar-00-before-existing-event-mobile-light.png): 7.7 (unrounded 7.7), ky_fit 7.6

existing-event · event-editor · empty · light · mobile 390×844 DPR1 · everyday · sha256 `86e237d61482fc9b…` · 43156 B · artifacts: modal scrim over calendar, visible focus ring · confidence high

- hierarchy **8**: 'Edit event' modal; Save (filled) and Cancel right, Delete separated on the left; focus in Title.
- typography **8**: Same clear label/field type steps as New event; filled values legible.
- density_layout **8**: Fields evenly spaced; footer separated by a quiet rule; fits viewport.
- color **7**: Delete uses the danger hue, which sits close to the orange accent; in dark theme the salmon Delete text and orange Save are near-identical hues.
- visible_affordances **8**: Delete outline, Cancel outline, Save fill and Title focus ring are distinct at a glance.
- coherence **7**: Native datetime/select controls; field set shows Repeat but not Calendar, differing from New event.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [list-desktop-dark.png](screenshots/kycalendar-00-before-list-desktop-dark.png): 7.9 (unrounded 7.85), ky_fit 7.8

list · calendar-list · populated · dark · desktop 1280×900 DPR1 · everyday · sha256 `b92e394cc8c982c7…` · 95215 B · artifacts: none · confidence high

- hierarchy **8**: Header unchanged; List segment selected; day groups 'Monday ... October 26, 2026' give clear scan anchors.
- typography **8**: Bold day/date group rows, muted time column, regular-weight titles: three clear levels at restrained sizes.
- density_layout **8**: 36px rows divided by quiet lines, consistent time/dot/title columns; list ends at y~806 inside a bordered table rather than full-bleed rows.
- color **8**: Neutral tinted group rows; colour only on calendar dots and accent marks.
- visible_affordances **7**: Rows have no visible hover/open affordance in the still; Today button reads disabled.
- coherence **8**: Table, group headers and toolbar share the same line weight and radius.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [list-desktop-light.png](screenshots/kycalendar-00-before-list-desktop-light.png): 7.9 (unrounded 7.85), ky_fit 7.8

list · calendar-list · populated · light · desktop 1280×900 DPR1 · everyday · sha256 `a9a8d9c55785ba9b…` · 95202 B · artifacts: none · confidence high

- hierarchy **8**: Header unchanged; List segment selected; day groups 'Monday ... October 26, 2026' give clear scan anchors.
- typography **8**: Bold day/date group rows, muted time column, regular-weight titles: three clear levels at restrained sizes.
- density_layout **8**: 36px rows divided by quiet lines, consistent time/dot/title columns; list ends at y~806 inside a bordered table rather than full-bleed rows.
- color **8**: Neutral tinted group rows; colour only on calendar dots and accent marks.
- visible_affordances **7**: Rows have no visible hover/open affordance in the still; Today button reads disabled.
- coherence **8**: Table, group headers and toolbar share the same line weight and radius.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [login-desktop-dark.png](screenshots/kycalendar-00-before-login-desktop-dark.png): 8.1 (unrounded 8.05), ky_fit 7.8

login · auth · empty · dark · desktop 1280×900 DPR1 · signed-out · sha256 `479a79849358177a…` · 23768 B · artifacts: visible focus ring · confidence high

- hierarchy **9**: Centred stamp, 'KyCalendar', tagline, and a card with Username, Password and one full-width orange 'Sign in'.
- typography **8**: Wordmark, tagline and 13px labels form clear steps.
- density_layout **8**: Centred column with even spacing; card padding consistent.
- color **8**: Neutral surfaces; orange on Sign in and focus ring; green 'Browser check passed' with icon.
- visible_affordances **8**: Username focused with visible ring; fields and button unambiguous.
- coherence **7**: A native compact 'Match system' theme select floats alone in the top-right corner, outside the card vocabulary. Dark: the stamp's dark tile merges with the background so only the white glyph reads.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [login-desktop-light.png](screenshots/kycalendar-00-before-login-desktop-light.png): 8.1 (unrounded 8.05), ky_fit 7.8

login · auth · empty · light · desktop 1280×900 DPR1 · signed-out · sha256 `110de186dd99e738…` · 24043 B · artifacts: visible focus ring · confidence high

- hierarchy **9**: Centred stamp, 'KyCalendar', tagline, and a card with Username, Password and one full-width orange 'Sign in'.
- typography **8**: Wordmark, tagline and 13px labels form clear steps.
- density_layout **8**: Centred column with even spacing; card padding consistent.
- color **8**: Neutral surfaces; orange on Sign in and focus ring; green 'Browser check passed' with icon.
- visible_affordances **8**: Username focused with visible ring; fields and button unambiguous.
- coherence **7**: A native compact 'Match system' theme select floats alone in the top-right corner, outside the card vocabulary.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [login-mobile-dark.png](screenshots/kycalendar-00-before-login-mobile-dark.png): 8.1 (unrounded 8.05), ky_fit 7.8

login · auth · empty · dark · mobile 390×844 DPR1 · signed-out · sha256 `e9c28f02c223cb80…` · 20334 B · artifacts: visible focus ring · confidence high

- hierarchy **9**: Same single-column sign-in with one orange Sign in.
- typography **8**: Same clear steps.
- density_layout **8**: Card fills width with 16px margins; balanced vertical placement.
- color **8**: Neutral with orange on primary/focus and green status text.
- visible_affordances **8**: Focused Username, clear fields and button.
- coherence **7**: Native theme select pinned top-right as on desktop. Dark: the stamp's dark tile merges with the background so only the white glyph reads.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [login-mobile-light.png](screenshots/kycalendar-00-before-login-mobile-light.png): 8.1 (unrounded 8.05), ky_fit 7.8

login · auth · empty · light · mobile 390×844 DPR1 · signed-out · sha256 `4682130636d2250f…` · 20512 B · artifacts: visible focus ring · confidence high

- hierarchy **9**: Same single-column sign-in with one orange Sign in.
- typography **8**: Same clear steps.
- density_layout **8**: Card fills width with 16px margins; balanced vertical placement.
- color **8**: Neutral with orange on primary/focus and green status text.
- visible_affordances **8**: Focused Username, clear fields and button.
- coherence **7**: Native theme select pinned top-right as on desktop.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [month-desktop-dark.png](screenshots/kycalendar-00-before-month-desktop-dark.png): 7.7 (unrounded 7.65), ky_fit 7.8

month · calendar-grid · populated · dark · desktop 1280×900 DPR1 · everyday · sha256 `7b073f252a70d9e5…` · 76030 B · artifacts: none · confidence high

- hierarchy **8**: 'Calendar' title top-left with timezone caption and a single filled orange 'New event' top-right; toolbar of prev/next, Today, centred 'October 2026', Month/Week/Day/List segment.
- typography **8**: Clear step from 24px-class title to centred period heading to 13px weekday/day numbers; event chips use bold title after muted time ('10a Design review').
- density_layout **7**: Six-week grid ends at y~700, leaving a ~200px empty band below; populated week compresses four chips into the 27th and truncates 'Planning the c...', 'Morning ap...', 'Weekly wr...'.
- color **8**: Neutral grid lines and surfaces; orange appears on New event, selected nav rail, Month underline, three calendar checkboxes and the '27' numeral, so accent is spread beyond primary/selection.
- visible_affordances **7**: Selected segment (fill + orange underline) and checked calendars read clearly; 'Today' is grey-on-grey and reads as disabled with no explicit reason.
- coherence **8**: One vocabulary: 40px outline controls, segment, chips and calendar list all share radius and line weight; calendar selection sits inside the one sidebar.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [month-desktop-light.png](screenshots/kycalendar-00-before-month-desktop-light.png): 7.7 (unrounded 7.65), ky_fit 7.8

month · calendar-grid · populated · light · desktop 1280×900 DPR1 · everyday · sha256 `58c1aa6eee330029…` · 76278 B · artifacts: none · confidence high

- hierarchy **8**: 'Calendar' title top-left with timezone caption and a single filled orange 'New event' top-right; toolbar of prev/next, Today, centred 'October 2026', Month/Week/Day/List segment.
- typography **8**: Clear step from 24px-class title to centred period heading to 13px weekday/day numbers; event chips use bold title after muted time ('10a Design review').
- density_layout **7**: Six-week grid ends at y~700, leaving a ~200px empty band below; populated week compresses four chips into the 27th and truncates 'Planning the c...', 'Morning ap...', 'Weekly wr...'.
- color **8**: Neutral grid lines and surfaces; orange appears on New event, selected nav rail, Month underline, three calendar checkboxes and the '27' numeral, so accent is spread beyond primary/selection.
- visible_affordances **7**: Selected segment (fill + orange underline) and checked calendars read clearly; 'Today' is grey-on-grey and reads as disabled with no explicit reason.
- coherence **8**: One vocabulary: 40px outline controls, segment, chips and calendar list all share radius and line weight; calendar selection sits inside the one sidebar.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [month-mobile-dark.png](screenshots/kycalendar-00-before-month-mobile-dark.png): 6.5 (unrounded 6.45), ky_fit 7.0

month · calendar-grid · populated · dark · mobile 390×844 DPR1 · everyday · sha256 `f77733da1a540079…` · 39548 B · artifacts: none · confidence high

- hierarchy **7**: Title and New event are clear; month grid gives no readable event content to lead with.
- typography **6**: Event chips shrink to dots plus times ('9a', '10a', '3:30p') with no titles; all-day bars carry no text.
- density_layout **5**: Four empty weeks take most of the screen while all events crowd the 25-31 row; '3:30p' runs to the Saturday cell edge; the final week row is cut at the fold.
- color **8**: Calendar hues are data; today column tint and orange 27 consistent with desktop.
- visible_affordances **6**: Textless bars and time-only chips are ambiguous at a glance; shared phone-header clipping (see list) applies.
- coherence **7**: Same tokens and grid lines as desktop; phone header issues as in list.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [month-mobile-light.png](screenshots/kycalendar-00-before-month-mobile-light.png): 6.5 (unrounded 6.45), ky_fit 7.0

month · calendar-grid · populated · light · mobile 390×844 DPR1 · everyday · sha256 `58cd3ed22aaed9f0…` · 39558 B · artifacts: none · confidence high

- hierarchy **7**: Title and New event are clear; month grid gives no readable event content to lead with.
- typography **6**: Event chips shrink to dots plus times ('9a', '10a', '3:30p') with no titles; all-day bars carry no text.
- density_layout **5**: Four empty weeks take most of the screen while all events crowd the 25-31 row; '3:30p' runs to the Saturday cell edge; the final week row is cut at the fold.
- color **8**: Calendar hues are data; today column tint and orange 27 consistent with desktop.
- visible_affordances **6**: Textless bars and time-only chips are ambiguous at a glance; shared phone-header clipping (see list) applies.
- coherence **7**: Same tokens and grid lines as desktop; phone header issues as in list.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [new-event-desktop-dark.png](screenshots/kycalendar-00-before-new-event-desktop-dark.png): 7.6 (unrounded 7.55), ky_fit 7.8

new-event · event-editor · empty · dark · desktop 1280×900 DPR1 · everyday · sha256 `c97e5cf8df9d38a0…` · 75954 B · artifacts: modal scrim over calendar, browser text-selection highlight in End field, visible focus ring · confidence high

- hierarchy **8**: Modal titled 'New event' over a dimmed calendar with Cancel/Save bottom-right and filled orange Save as the single primary.
- typography **8**: Bold 13px labels above fields, '(optional)' in muted text, 20px-class dialog title; clean steps.
- density_layout **8**: Labels-above fields on an 8px-ish rhythm; Start/End side by side on desktop, stacked on phone; dialog fits the viewport.
- color **7**: End field shows the browser's blue text-selection highlight on '10' inside an orange focus ring, an off-palette blue beside the accent.
- visible_affordances **7**: Focus sits on End rather than the empty Title; native datetime/select controls carry browser icons; no required marker on Title.
- coherence **7**: Custom 40px fields mixed with native datetime pickers, native select chevron and native checkbox; field set shows Calendar but no Repeat, unlike Edit.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven. Unsaved blank event; underlying month.

### [new-event-desktop-light.png](screenshots/kycalendar-00-before-new-event-desktop-light.png): 7.6 (unrounded 7.55), ky_fit 7.8

new-event · event-editor · empty · light · desktop 1280×900 DPR1 · everyday · sha256 `87865cc37f7f2f42…` · 78204 B · artifacts: modal scrim over calendar, browser text-selection highlight in End field, visible focus ring · confidence high

- hierarchy **8**: Modal titled 'New event' over a dimmed calendar with Cancel/Save bottom-right and filled orange Save as the single primary.
- typography **8**: Bold 13px labels above fields, '(optional)' in muted text, 20px-class dialog title; clean steps.
- density_layout **8**: Labels-above fields on an 8px-ish rhythm; Start/End side by side on desktop, stacked on phone; dialog fits the viewport.
- color **7**: End field shows the browser's blue text-selection highlight on '10' inside an orange focus ring, an off-palette blue beside the accent.
- visible_affordances **7**: Focus sits on End rather than the empty Title; native datetime/select controls carry browser icons; no required marker on Title.
- coherence **7**: Custom 40px fields mixed with native datetime pickers, native select chevron and native checkbox; field set shows Calendar but no Repeat, unlike Edit.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven. Unsaved blank event; underlying month.

### [new-event-mobile-dark.png](screenshots/kycalendar-00-before-new-event-mobile-dark.png): 7.6 (unrounded 7.55), ky_fit 7.6

new-event · event-editor · empty · dark · mobile 390×844 DPR1 · everyday · sha256 `7636b2bd81fc0d91…` · 32329 B · artifacts: modal scrim over calendar, browser text-selection highlight in End field, visible focus ring · confidence high

- hierarchy **8**: Modal titled 'New event' over a dimmed calendar with Cancel/Save bottom-right and filled orange Save as the single primary.
- typography **8**: Bold 13px labels above fields, '(optional)' in muted text, 20px-class dialog title; clean steps.
- density_layout **8**: Labels-above fields on an 8px-ish rhythm; Start/End side by side on desktop, stacked on phone; dialog fits the viewport.
- color **7**: End field shows the browser's blue text-selection highlight on '10' inside an orange focus ring, an off-palette blue beside the accent.
- visible_affordances **7**: Focus sits on End rather than the empty Title; native datetime/select controls carry browser icons; no required marker on Title.
- coherence **7**: Custom 40px fields mixed with native datetime pickers, native select chevron and native checkbox; field set shows Calendar but no Repeat, unlike Edit.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven. Unsaved blank event; underlying month.

### [new-event-mobile-light.png](screenshots/kycalendar-00-before-new-event-mobile-light.png): 7.6 (unrounded 7.55), ky_fit 7.6

new-event · event-editor · empty · light · mobile 390×844 DPR1 · everyday · sha256 `4bd1ed8fb26914ce…` · 33709 B · artifacts: modal scrim over calendar, browser text-selection highlight in End field, visible focus ring · confidence high

- hierarchy **8**: Modal titled 'New event' over a dimmed calendar with Cancel/Save bottom-right and filled orange Save as the single primary.
- typography **8**: Bold 13px labels above fields, '(optional)' in muted text, 20px-class dialog title; clean steps.
- density_layout **8**: Labels-above fields on an 8px-ish rhythm; Start/End side by side on desktop, stacked on phone; dialog fits the viewport.
- color **7**: End field shows the browser's blue text-selection highlight on '10' inside an orange focus ring, an off-palette blue beside the accent.
- visible_affordances **7**: Focus sits on End rather than the empty Title; native datetime/select controls carry browser icons; no required marker on Title.
- coherence **7**: Custom 40px fields mixed with native datetime pickers, native select chevron and native checkbox; field set shows Calendar but no Repeat, unlike Edit.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven. Unsaved blank event; underlying month.

### [phones-and-apps-desktop-dark.png](screenshots/kycalendar-00-before-phones-and-apps-desktop-dark.png): 7.9 (unrounded 7.85), ky_fit 7.8

phones-and-apps · settings · empty · dark · desktop 1280×900 DPR1 · everyday · sha256 `3e19671f81a1c77a…` · 83946 B · artifacts: none · confidence high

- hierarchy **8**: 'Phones and apps' title with explanation; 'Add a device or agent' form with filled orange Create is the clear action.
- typography **8**: Section headings 17px-class, labels bold 13px, helper text muted; URLs in mono chips.
- density_layout **8**: Three stacked sections (form card, dashed empty state, setup table) with even spacing; content continues below the fold.
- color **8**: Restrained; orange only on selected radio, Create and nav rail.
- visible_affordances **8**: Radios with helper lines, input with placeholder and Create button read clearly; empty state names the next step.
- coherence **7**: Solid card, dashed empty-state box and bordered setup table are three box treatments on one page, versus the flat Appearance page.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [phones-and-apps-desktop-light.png](screenshots/kycalendar-00-before-phones-and-apps-desktop-light.png): 7.9 (unrounded 7.85), ky_fit 7.8

phones-and-apps · settings · empty · light · desktop 1280×900 DPR1 · everyday · sha256 `0186944ea81f9acc…` · 82846 B · artifacts: none · confidence high

- hierarchy **8**: 'Phones and apps' title with explanation; 'Add a device or agent' form with filled orange Create is the clear action.
- typography **8**: Section headings 17px-class, labels bold 13px, helper text muted; URLs in mono chips.
- density_layout **8**: Three stacked sections (form card, dashed empty state, setup table) with even spacing; content continues below the fold.
- color **8**: Restrained; orange only on selected radio, Create and nav rail.
- visible_affordances **8**: Radios with helper lines, input with placeholder and Create button read clearly; empty state names the next step.
- coherence **7**: Solid card, dashed empty-state box and bordered setup table are three box treatments on one page, versus the flat Appearance page.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [phones-and-apps-mobile-dark.png](screenshots/kycalendar-00-before-phones-and-apps-mobile-dark.png): 7.7 (unrounded 7.7), ky_fit 7.6

phones-and-apps · settings · empty · dark · mobile 390×844 DPR1 · everyday · sha256 `3eec7a3aa979a863…` · 45844 B · artifacts: none · confidence high

- hierarchy **8**: Title, explanation and the form card with orange Create lead clearly.
- typography **8**: Same type steps as desktop; helper text fits.
- density_layout **8**: Sections stack evenly with full-width fields; setup continues below.
- color **8**: Restrained palette; orange limited to radio, Create and selected tab.
- visible_affordances **7**: Nav strip clips both 'Calendar' (left) and 'Two-ste' (right) edges.
- coherence **7**: Same three box treatments as desktop.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [phones-and-apps-mobile-light.png](screenshots/kycalendar-00-before-phones-and-apps-mobile-light.png): 7.7 (unrounded 7.7), ky_fit 7.6

phones-and-apps · settings · empty · light · mobile 390×844 DPR1 · everyday · sha256 `e391531e9c4da721…` · 45118 B · artifacts: none · confidence high

- hierarchy **8**: Title, explanation and the form card with orange Create lead clearly.
- typography **8**: Same type steps as desktop; helper text fits.
- density_layout **8**: Sections stack evenly with full-width fields; setup continues below.
- color **8**: Restrained palette; orange limited to radio, Create and selected tab.
- visible_affordances **7**: Nav strip clips both 'Calendar' (left) and 'Two-ste' (right) edges.
- coherence **7**: Same three box treatments as desktop.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [week-desktop-dark.png](screenshots/kycalendar-00-before-week-desktop-dark.png): 7.5 (unrounded 7.5), ky_fit 7.8

week · calendar-grid · populated · dark · desktop 1280×900 DPR1 · everyday · sha256 `9922335a70bd79a4…` · 85161 B · artifacts: none · confidence high

- hierarchy **8**: Same title/primary/toolbar header; 'Oct 25 - 31, 2026' centred; Week segment selected.
- typography **7**: Event blocks carry 12px time + bold title; 'Planning the community workshop and preparing the' wraps and the last line is cut mid-glyph at the block bottom; 'Morning appointment' shows only 'Morning'.
- density_layout **7**: Time grid 7am-5pm fills the viewport to y~872 with even hour/half-hour rules; the clipped multi-line block and a 20-minute 'Team check-in' sliver without time are the visible rhythm breaks.
- color **8**: Calendar hues (blue/green/plum) are user data; today column gets a subtle tint plus orange day header and underline.
- visible_affordances **7**: All-day row, today column and selected Week segment are unambiguous; Today button again looks disabled.
- coherence **8**: Grid, all-day row and chips share the same line and radius vocabulary as Month.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [week-desktop-light.png](screenshots/kycalendar-00-before-week-desktop-light.png): 7.5 (unrounded 7.5), ky_fit 7.8

week · calendar-grid · populated · light · desktop 1280×900 DPR1 · everyday · sha256 `303281e6114fddf3…` · 84995 B · artifacts: none · confidence high

- hierarchy **8**: Same title/primary/toolbar header; 'Oct 25 - 31, 2026' centred; Week segment selected.
- typography **7**: Event blocks carry 12px time + bold title; 'Planning the community workshop and preparing the' wraps and the last line is cut mid-glyph at the block bottom; 'Morning appointment' shows only 'Morning'.
- density_layout **7**: Time grid 7am-5pm fills the viewport to y~872 with even hour/half-hour rules; the clipped multi-line block and a 20-minute 'Team check-in' sliver without time are the visible rhythm breaks.
- color **8**: Calendar hues (blue/green/plum) are user data; today column gets a subtle tint plus orange day header and underline.
- visible_affordances **7**: All-day row, today column and selected Week segment are unambiguous; Today button again looks disabled.
- coherence **8**: Grid, all-day row and chips share the same line and radius vocabulary as Month.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [week-mobile-dark.png](screenshots/kycalendar-00-before-week-mobile-dark.png): 5.7 (unrounded 5.65), ky_fit 6.8

week · calendar-grid · populated · dark · mobile 390×844 DPR1 · everyday · sha256 `bb0dcdccbcf5b124…` · 46960 B · artifacts: none · confidence high

- hierarchy **7**: Header/primary clear; seven ~43px day columns underneath.
- typography **4**: Event titles are cut mid-word without ellipsis: 'Desi revie', 'Plani the comi work', 'Morr', 'Weel walk', 'Libr...', 'Team'.
- density_layout **4**: Seven columns at 390px leave events unreadable; grid continues below the fold; all-day row truncates both bars.
- color **7**: Hues are data, but tiny saturated blocks with white text dominate the narrow columns.
- visible_affordances **5**: Events read as coloured tiles rather than identifiable items; phone-header clipping repeats.
- coherence **7**: Components match desktop; the composition simply does not adapt to the width.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.

### [week-mobile-light.png](screenshots/kycalendar-00-before-week-mobile-light.png): 5.7 (unrounded 5.65), ky_fit 6.8

week · calendar-grid · populated · light · mobile 390×844 DPR1 · everyday · sha256 `f7c2dbfdb5e8d022…` · 46859 B · artifacts: none · confidence high

- hierarchy **7**: Header/primary clear; seven ~43px day columns underneath.
- typography **4**: Event titles are cut mid-word without ellipsis: 'Desi revie', 'Plani the comi work', 'Morr', 'Weel walk', 'Libr...', 'Team'.
- density_layout **4**: Seven columns at 390px leave events unreadable; grid continues below the fold; all-day row truncates both bars.
- color **7**: Hues are data, but tiny saturated blocks with white text dominate the narrow columns.
- visible_affordances **5**: Events read as coloured tiles rather than identifiable items; phone-header clipping repeats.
- coherence **7**: Components match desktop; the composition simply does not adapt to the width.
- caveat: Pixels only: font identity, computed contrast, focus order and behaviour unproven.


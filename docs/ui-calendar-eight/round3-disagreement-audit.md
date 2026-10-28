# KyCalendar round 3 disagreement audit

The owner-rejected phone Month has a substantive task-readability problem: its timed events show fragmented titles without visible start times. The parent is restoring start-time-led Month; that newer layout is **UNSCORED** and receives no validation or rating here. The pinned round-3 Opus result remains **633/80 → 7.9**, below the numeric progression condition; Sol remains separate at **43/5 → 8.6**. Both independent satisfaction results remain false. Owner rejection overrides progression regardless of scores. This audit issues no acceptance.

- Added factual dispositions for all nine two-point quality disagreements, plus phone Month/Week, header and optional People context.
- Individually opened **20 current stage-04 originals at full resolution**, including every parent-affected file. No historical, diagnostic or current-runtime view was substituted.
- **Review this:** [Month disposition](#d4), [Group/Role disposition](#d1), and [Week reliability qualification](#d5). The [JSON audit](round3-disagreement-audit.json) contains each spread, exact opened identities and before/after hashes.
- **Checked:** all 206 preserved image hashes/bytes/PNG dimensions, 48 candidate memberships, seven input pins, both complete-array freezes, existing weighted arithmetic and the exact nine spread rows. Raw report bytes are unchanged.
- **Not checked:** browser, builds, application tests or complete behavior gates; this evidence-only assignment prohibits them. No fresh execution is claimed for earlier capture probes.
- **Parent continuation:** finish the concrete Month correction already underway. No Role-disable requirement, People restyle, score repair, numerical consensus or additional round is requested by this audit.

## Scope and evidence

Auditor: GPT-6.1 Sol, high reasoning, acting as the review sub-agent after both terminal raw reviews. This is a factual audit, not a blind scoring round. Rubric `ky-suite-v2`, pinned SHA-256 `ad3516620420fbdc30f5273f82b7c72c76e1365d1b6296fcaa56be3cbebf2c41`. Source is **`3aca6fcf934dacb0149aafce388cf38f02244015`**, stage **04-confirmation**, runtime binding `2fe32fb65ce4151b54edd81947e41c74d4b6e0f7a357446742129496f2d9da2b`. Stage 04 is an artifact stage, not a fourth review round.

Read both terminal raw Markdown/JSON pairs, [parent comparison](round3-parent-review-check.json), [Opus parent check](round3-parent-opus-check.json), all Opus A1–A7 annotations and the pinned [inputs-v3](review-inputs-v3.json), [inventory-v4](inventory-v4.json), [artifacts-v4](artifacts-v4.json), confirmation manifest, authority, rubric, contract and runtime tokens. Raw records remain immutable; the audit distinguishes supported pixels, disproved claims and calibration without replacing any grade.

Pinned capture conditions: desktop 1280×900, phone 390×844, DPR 1, OS light/dark, no saved theme, en-US, America/New_York, Chromium 153.0.8010.12 / Playwright 1.63.0. Original viewport PNGs, no crop or resize; synthetic fixture; real clock. Oct 27 is reached through real controls while the actual Oct 28 clock remains live. Today/time-indicator/sign-in-age differences are capture effects. The audit uses original local images and Git source only, with no browser or new capture.

The older input/report wording “user did not reject layout” refers to the stage-03 history. It does not negate the owner's later explicit rejection of R3 phone Month. The newer time-led implementation remains unscored regardless of which parent branch is checked out.

At closeout, the parent added [owner-decisions.json](owner-decisions.json), SHA-256 `4963545993b827b6d98a07562d2dd549857334b9d8e57fffd4a5721e684d7163`, binding round 3 and source 3aca6fc to “round 3 mobile month is a downgrade” and the preference for baseline times over “3 letters and some dots.” This is supplemental owner context, not a frozen reviewer input. The latest local AGENTS rule was re-read; both parent-owned files were left untouched by this auditor.

## Every quality spread of two or more points

All nine parent rows were independently reproduced from the frozen vectors. Each affected original was then opened individually. These are **unchanged raw scores**, not new audit ratings; numerical disagreement is deliberately retained after factual disposition.

| Original | Dimension | Opus | Sol | Disposition |
|---|---|---:|---:|---|
| [admin-group-calendars-desktop-dark.png](screenshots/kycalendar-04-confirmation-admin-group-calendars-desktop-dark.png) | coherence | 7 | 9 | [D1](#d1) |
| [admin-group-calendars-desktop-light.png](screenshots/kycalendar-04-confirmation-admin-group-calendars-desktop-light.png) | coherence | 7 | 9 | [D1](#d1) |
| [admin-group-calendars-mobile-dark.png](screenshots/kycalendar-04-confirmation-admin-group-calendars-mobile-dark.png) | coherence | 7 | 9 | [D1](#d1) |
| [admin-group-calendars-mobile-light.png](screenshots/kycalendar-04-confirmation-admin-group-calendars-mobile-light.png) | coherence | 7 | 9 | [D1](#d1) |
| [appearance-mobile-dark.png](screenshots/kycalendar-04-confirmation-appearance-mobile-dark.png) | visible_affordances | 7 | 9 | [D2](#d2) |
| [day-mobile-dark.png](screenshots/kycalendar-04-confirmation-day-mobile-dark.png) | visible_affordances | 7 | 9 | [D3](#d3) |
| [day-mobile-light.png](screenshots/kycalendar-04-confirmation-day-mobile-light.png) | visible_affordances | 7 | 9 | [D3](#d3) |
| [default-phone-list-mobile-dark.png](screenshots/kycalendar-04-confirmation-default-phone-list-mobile-dark.png) | visible_affordances | 7 | 9 | [D3](#d3) |
| [default-phone-list-mobile-light.png](screenshots/kycalendar-04-confirmation-default-phone-list-mobile-light.png) | visible_affordances | 7 | 9 | [D3](#d3) |

<a id="d1"></a>

## D1. Group availability, Role preselection and disabled styling

**Disposition:** visible-state facts supported; mandatory Role-disable fix not established; coherence severity is calibration.

- All four originals show Group labelled 'No groups available', a faded Add group button, and a normal Reader Role selector.
- Create is filled orange; Delete calendar is an outline inside the existing calendar card. The phone Group and Role fields fill their card width.
- The Groups navigation destination is visible in all four originals.

A disabled select and disabled secondary button need not share their fill: they are different control types. Both communicate unavailability in the capture.

Enabled Role can intentionally allow permission preselection. At pinned source, changing Role updates local state; submission returns when group is empty and Add group is disabled until a group is selected. No accidental grant, false success, unsafe access or blocked task is demonstrated.

Opus's 'no inline link to create a group' is literally true. Its broader phone 'no path to create a group' is not supported: Groups is visibly in navigation. Do not infer missing group creation from this screen.

The recommendation to disable Role solely for cosmetic symmetry is not a required fix. There is no requirement here to change destructive-action styling or add a shortcut link.

Pinned source: `3aca6fc:web/src/pages/GroupCalendars.tsx:153-155`; `3aca6fc:web/src/pages/GroupCalendars.tsx:194-221`.

**Action:** Retain the current audit disposition; no group-control change or capture round required by these facts.

<a id="d2"></a>

## D2. Appearance dark navigation pill and selected theme

**Disposition:** real at-rest visual difference; cause and task harm unproven; calibration remains separate.

- Dark Appearance shows a raised/filled Two-step verification neighbor while Appearance has the orange bottom rail. Match system alone has the selected radio and orange card outline.
- The matching light original does not show the same contrasting neighbor fill.

The neighboring pill is visible and may read as hover/emphasis, so Sol's theme-selection evidence does not address every navigation detail.

There are not two selected themes or two orange active navigation rails. Stale hover is a hypothesis, not a proven cause; A3 correctly leaves that unknown.

A missing or subtle left continuation cue is an at-rest discoverability judgment; this still cannot prove unreachable previous tabs or a focus failure. The pinned CSS defines edge fades and removes the mask during focus.

Pinned source: `3aca6fc:web/src/ky-ui/navigation.css:3-29`; `3aca6fc:web/src/styles/theme.css:192-196`; `3aca6fc:web/src/components/AppHeader.tsx:14-36,75-82`.

**Action:** Preserve the captured caveat and both raw scores; no theme-card or navigation restyle required by the audit.

<a id="d3"></a>

## D3. Phone Day/List navigation fade and date controls

**Disposition:** literal no-fade premise rejected; at-rest discoverability preference retained only as calibration.

- All four originals visibly fade the trailing 'Phones &' text before the Calendars disclosure.
- Previous/next and view controls have visible bounds and the selected segment has a bottom rail. Today appears enabled on Oct 27 Day and muted on the Oct 25-31 List range that includes the live Oct 28 date.
- Day event time ranges precede titles; List has separate time and title columns.

Opus's frozen 'no fade/scroll cue' wording is too strong. A2 correctly qualifies the same pixels as a designed continuation fade.

Sol's clear-control observation is supported but does not itself establish full navigation, keyboard or focus usability.

No missing route or broken date-control behavior follows from these screenshots. Focus probes are pre-existing recorded evidence, not a fresh audit execution.

Pinned source: `3aca6fc:web/src/styles/theme.css:192-196`; `3aca6fc:web/src/components/AppHeader.tsx:80`.

**Action:** Keep the factual correction outside raw files; no navigation-fix round solely to close the two-point spread.

<a id="d4"></a>

## D4. Owner-rejected phone Month: title fragments replace useful start times

**Disposition:** supported substantive task-readability concern; owner rejection remains controlling.

- Both originals break words into fragments: Library day crosses a word boundary mid-word; Design review, Lunch break and Planning... have split/ellipsized prefixes. Release preparation fits across its multiday bar, so not every event is equally affected.
- Timed Month events show colored dots and title fragments without visible start times. A user scanning the month cannot read their start times in this capture.
- Pinned calendar.css uses a two-line title clamp with overflow-wrap:anywhere and visually clips the event-time element.

Opus's frozen '2-3 lines' wording overstates the captured title line count: source clamps to two and the originals show two title rows. This does not erase the real fragmentation.

Opus's 'mid-glyph' drift phrase is imprecise: the visible problem is a break between letters within a word, not evidence of sliced glyphs.

Four unpopulated calendar weeks reflect the synthetic captured state and ordinary month chronology; empty rows alone are not a product defect or a reason to add fixture data.

Word-boundary wrapping or hyphenation alone would not satisfy the owner preference for original baseline start times. Sol's wider-day/selected-day alternatives are suggestions, not selected work.

Pinned source: `3aca6fc:web/src/styles/calendar.css:165-170`.

**Action:** Record the owner's preference for start-time-led phone Month and the parent's concrete correction. That newer layout is UNSCORED; do not inspect its runtime, request a new capture, invent a score, or claim the correction validated here.

<a id="d5"></a>

## D5. Phone Week false empty-Monday/hidden-Today premise; residual title truncation

**Disposition:** false premise rejected; native week-start layout retained; bounded title truncation supported.

- Both phone originals show Sun 25, Mon 26 and Tue 27. Monday visibly contains Team check-in around 9am; Tuesday is a fully visible column, including Library day and its timed events.
- The time gutter is painted and legible at the captured zero offset. The explicit instruction says 'Scroll across the week to see other days.'
- Only three of seven day columns are initially visible. Phone Design review and workshop titles end in ellipses; the desktop originals also retain workshop/Morning appointment truncation.

Sunday is empty; Monday is not. A1 and both reviewers' dispositions correctly withdraw the repeated empty-Monday premise.

Wednesday 28 is offscreen at week start, but it is the live-clock today, not the Oct 27 displayed reference date. This is not a demonstrated failure to reveal the intended Tuesday, nor a requirement for auto-positioning.

The narrower initial overview and lack of a peeking fourth column are composition tradeoffs with an explicit scroll instruction; do not invent an automatic-current-day or fixed-gutter-after-scroll contract.

Screenshots do not establish keyboard scrolling or complete accessible names. Existing recorded 0->40->0 probes are not rerun.

Truncated titles can remain a low-priority readability observation without requiring another line-limit or cosmetic review round.

Pinned source: `3aca6fc:web/src/styles/calendar.css:161-164`.

**Action:** Preserve A1 outside the frozen rows, acknowledge the repeated misreading, retain week-start behavior, and make no score repair/counterfactual adjustment.

<a id="d6"></a>

## D6. Phone header height and Sol Day line-count annotation

**Disposition:** observable geometry; resolved versus partly improved is calibration, not a new blocker.

- The timezone occupies one line. Day/Month/List grids start around y281; Week starts around y309 after the scroll instruction.
- Day workshop has a time line plus two title lines, with its entire title readable.

Sol's frozen 'three clear title lines' is inaccurate and already corrected outside its freeze: the third line is time.

Whether this header is resolved (Opus) or still somewhat tall (Sol) is a judgment about the same visible composition. The extra Week instruction has a clear purpose.

No new compact-header pass is required simply to reconcile that wording.

**Action:** Keep both factual annotations and the distinct raw judgments; no extra round.

<a id="d7"></a>

## D7. People row action density

**Disposition:** supported optional styling observation; deferral accepted.

- Everyday casey has Edit, Reset password, Make admin and Disable. Disable wraps onto its own phone row; all four labels are readable.
- The desktop administrator row shows only Edit and Reset password. The phone administrator record continues below the viewport, so its full action row is not visually verified here.

Four outlined actions do add visual weight, but Disable already has a distinct danger color; 'equally outlined' does not mean identical semantics.

No clipped everyday action, missing task, authorization failure or new gate failure is shown. Last-sign-in time differences are live-clock effects.

**Action:** Keep People action styling optional and deferred. Do not add it to the parent's concrete Month correction.

## Review reliability and calibration

Opus disclosed reading `COUNCIL.md` before freezing: prior aggregate means for Opus (7.5, 7.8) and Sol (7.9, 8.5), without per-file scores or findings. Qualify the review as **partially unblinded to prior aggregates**. This audit has no evidence that Opus read peer per-file reports before freeze or deliberately tuned scores. Its repeated empty-Monday premise, despite the prior correction, further limits confidence in the frozen Week rationale. A1 corrects that fact outside the array; it does not repair the grade.

A2's fade qualification is supported. A3 correctly leaves the raised pill's cause unproven. A5's Month source binding is confirmed, with the two-line/mid-word precision corrections above. A6 confirms control states but does not establish a mandatory Role-disable fix. A7 matches the recorded live-clock conditions. A4's native End-field artifact is retained as a recorded qualification; no New-event original was opened here and no fresh focus/palette result follows.

Sol also has a frozen Day wording error: two title lines plus time were called three title lines. Its external annotation is confirmed by both Day originals. A correction to evidence wording is not numerical calibration credit.

Both reviews disclose calibration on unchanged captures. Opus reports +43/180 across 18 byte-identical R2 images and +9/40 across 16 baseline-identical images. Sol reports +1/9 across 18 unchanged R2 images and +5/8 across 16 baseline-identical images, including a 1.1 desktop-List shift since R1. These are reviewer calibration, **not product gains**. The audit retains these existing disclosures; it does not re-rate historical pixels, pool rounds or peers, compute an adjusted grade, or request a new round merely to erase calibration.

## Immutable raw reports and freezes

Initial-read hashes and the post-inspection hashes/lengths agree byte for byte. Complete-array canonicalization is Python `json.dumps(files,ensure_ascii=False,sort_keys=True,separators=(',',':'),allow_nan=False).encode()`, with no trailing newline. Final publication verification is recorded in the JSON.

| Raw report | Bytes | SHA-256 before and after | Preservation |
|---|---:|---|---|
| [opus-round3.json](opus-round3.json) | 225557 | `2c649996931bc02d7906f9cc9d33b59f7743733392264bc2b557a08f9600d5c6` | identical |
| [opus-round3.md](opus-round3.md) | 19220 | `7f4c94807807719779334fefb0f897e31f5ccaa22bc277559064e0532028bd75` | identical |
| [sol-round3.json](sol-round3.json) | 645447 | `93ab92c6d8b7c3004e7f723bb4d191ab1387af86255e128e78f7af2fdf0520b1` | identical |
| [sol-round3.md](sol-round3.md) | 187834 | `f65e7136d33711af1277f30b5c6ad65d789c362e18a7c6f64f6996047a2eb459` | identical |

- Opus complete `files` array: **111936 bytes**, SHA-256 `f10f0c22e95312d689315de62cad07dfc1cfa22453e7851171e9a5aef049c455`; original freeze `2026-10-28T12:20:20Z`.
- Sol complete `files` array: **181740 bytes**, SHA-256 `f60651a6b6b6cb28ca4af219853c65e4969ab37458100e2729e49a7346c044b6`; original freeze `2026-10-28T12:18:32.298661+00:00`.

The JSON also retains initial/post-inspection identities for both parent checks, inputs-v3, inventory-v4 and artifacts-v4, and all seven input pins. All 206 image identities match the pinned artifact inventory. This verifies preservation, not behavior or a clean rebuild.

After the 13:00 publication check, the parent revised only its Opus check's owner-rejection wording from “single-line restoration” to “time-led restoration.” Its new identity is 1012 bytes, SHA-256 `e330f1ab97b7d7aea673dcd9cc8ccb8f180e8e43c0d22f556c439161ff1e6e03`. The audit retains the earlier snapshot and records this concurrent parent-context change separately. The terminal recheck confirms both raw pairs, complete-array freezes, all seven pinned inputs and all 206 originals are still unchanged; the numeric gate is unchanged.

## Pinned source inspection

All source inspection used `git show 3aca6fc:<path>`; no conclusions use the parent's newer runtime. These are Git blob and SHA-256 identities for the inspected source files.

| Path at 3aca6fc | Git blob | SHA-256 |
|---|---|---|
| `web/src/pages/GroupCalendars.tsx` | `24296bdfe808fa30c39508be461cf0f0ac89c2eb` | `cda871699bbd5448a8d2b880c88283a3ed30e748fe5e6b40ca874a9d5f3bcf3e` |
| `web/src/components/AppHeader.tsx` | `51f067a57aa3c3c5255a2473cc56bad57ae00f18` | `4c77b78d13a1d035bd0a1a549abb87d271a299b24651bd246fae169430fe0b57` |
| `web/src/styles/calendar.css` | `41db2ef9020705f213243a41e04698c490d27bac` | `8097a5a1e57d9d4cd1ce289b5a740182657d880b5ae37475ddc5c6ff4e0802b4` |
| `web/src/styles/theme.css` | `78647dc5b9eed6a1f8fbc83055572a04585f3b82` | `589c29f19649539a0f2680d003e415f8a1419094a94472ab30ace9abf26a8be0` |
| `web/src/ky-ui/navigation.css` | `f9bdf846159f0f25db0202a5ed57bf15d0458653` | `cc6dbaa07a01f4ed088d38d41ff6cf95155a480bcb29327f413cbfa1ec11ac55` |

## Exact original-image opened list

Each numbered file was opened separately with `tools.view_image(detail="original")` and forwarded at original detail. No resized composite or gallery substitutes for inspection. This is the complete list of images opened by this audit, in order: nine spread originals, four Month/phone-Week originals, matching light Appearance, four People originals and two desktop Week originals.

1. [screenshots/kycalendar-04-confirmation-admin-group-calendars-desktop-dark.png](screenshots/kycalendar-04-confirmation-admin-group-calendars-desktop-dark.png) — 1280×900, 67563 bytes, SHA-256 `d5b0f4c0b5334c5aa7d84e87bfe6bbc07ddeb8217d22a9b452d2d6ccaca48b85`.
2. [screenshots/kycalendar-04-confirmation-admin-group-calendars-desktop-light.png](screenshots/kycalendar-04-confirmation-admin-group-calendars-desktop-light.png) — 1280×900, 67616 bytes, SHA-256 `3b06f2b1ffb2dc431c1dc413be8f0fc90a18c1e74f05d43d2f83f09c3d04b766`.
3. [screenshots/kycalendar-04-confirmation-admin-group-calendars-mobile-dark.png](screenshots/kycalendar-04-confirmation-admin-group-calendars-mobile-dark.png) — 390×844, 44782 bytes, SHA-256 `4dcbee2cce50e3165aff10d09bec1f5f558a17969dbd7f5d894ca59c812b7206`.
4. [screenshots/kycalendar-04-confirmation-admin-group-calendars-mobile-light.png](screenshots/kycalendar-04-confirmation-admin-group-calendars-mobile-light.png) — 390×844, 44779 bytes, SHA-256 `8b16973600e64f6901cd3efaa42285aef4738e51fd720a111b7050591257ba37`.
5. [screenshots/kycalendar-04-confirmation-appearance-mobile-dark.png](screenshots/kycalendar-04-confirmation-appearance-mobile-dark.png) — 390×844, 39346 bytes, SHA-256 `17daa61802253461750d54e4a839c364610756e353801c63f55ec6507d136683`.
6. [screenshots/kycalendar-04-confirmation-day-mobile-dark.png](screenshots/kycalendar-04-confirmation-day-mobile-dark.png) — 390×844, 39030 bytes, SHA-256 `df2c06702a304a41e1590999a164f825b28198ac2cdd462d81cfe8253eb6b856`.
7. [screenshots/kycalendar-04-confirmation-day-mobile-light.png](screenshots/kycalendar-04-confirmation-day-mobile-light.png) — 390×844, 38866 bytes, SHA-256 `b836b0b8276e317efa64efc9a964e1ee59fae1d9180cef04e3f92a3f7d748325`.
8. [screenshots/kycalendar-04-confirmation-default-phone-list-mobile-dark.png](screenshots/kycalendar-04-confirmation-default-phone-list-mobile-dark.png) — 390×844, 61243 bytes, SHA-256 `284aa79a9e90f85396268fb075636a8eb5b31d89716cd47e84b36b1ec1edca37`.
9. [screenshots/kycalendar-04-confirmation-default-phone-list-mobile-light.png](screenshots/kycalendar-04-confirmation-default-phone-list-mobile-light.png) — 390×844, 61331 bytes, SHA-256 `77252dcd49460f381ee79ffd9270234bfd5c8c70206183f1964ca076d45f57fc`.
10. [screenshots/kycalendar-04-confirmation-month-mobile-dark.png](screenshots/kycalendar-04-confirmation-month-mobile-dark.png) — 390×844, 45585 bytes, SHA-256 `68ad52fc8a2606d9703d69a0fb6b59fcb494fe6c0af03180a39a9bfb4915ce0c`.
11. [screenshots/kycalendar-04-confirmation-month-mobile-light.png](screenshots/kycalendar-04-confirmation-month-mobile-light.png) — 390×844, 45585 bytes, SHA-256 `fc79c96ec6b0274cd0c2e7eb253cfed456f1ebe15b877250cecb529bf772931d`.
12. [screenshots/kycalendar-04-confirmation-week-mobile-dark.png](screenshots/kycalendar-04-confirmation-week-mobile-dark.png) — 390×844, 42503 bytes, SHA-256 `f5d5c653a86954ecd2264ee8d465e2f0c210241f969307f71d544ada4d4147d0`.
13. [screenshots/kycalendar-04-confirmation-week-mobile-light.png](screenshots/kycalendar-04-confirmation-week-mobile-light.png) — 390×844, 42421 bytes, SHA-256 `c48d26384dc85331b42108ac259793f351f6a6e1d4a9b31c2f75d94e4d70923d`.
14. [screenshots/kycalendar-04-confirmation-appearance-mobile-light.png](screenshots/kycalendar-04-confirmation-appearance-mobile-light.png) — 390×844, 38448 bytes, SHA-256 `f6837c6d43d89ad35781385e2ea5e9d04b51f95f8b7ea8f85ecdd10825c1a8cf`.
15. [screenshots/kycalendar-04-confirmation-admin-people-desktop-dark.png](screenshots/kycalendar-04-confirmation-admin-people-desktop-dark.png) — 1280×900, 73911 bytes, SHA-256 `f1b895133c97a12447138a87fb51ea95df37464da58a79c713dc2694aef70294`.
16. [screenshots/kycalendar-04-confirmation-admin-people-desktop-light.png](screenshots/kycalendar-04-confirmation-admin-people-desktop-light.png) — 1280×900, 73953 bytes, SHA-256 `8480d18746ecda83249593ffdccdcabbce6384d02be60abfaf5fe32ee33b711a`.
17. [screenshots/kycalendar-04-confirmation-admin-people-mobile-dark.png](screenshots/kycalendar-04-confirmation-admin-people-mobile-dark.png) — 390×844, 47142 bytes, SHA-256 `3c05ce58927033ee4997c73716b7607e6eb9e71bbdfa4e6ff2ea2798f5360b0d`.
18. [screenshots/kycalendar-04-confirmation-admin-people-mobile-light.png](screenshots/kycalendar-04-confirmation-admin-people-mobile-light.png) — 390×844, 46753 bytes, SHA-256 `b552e625441602206e9b041d1a7fed6122aba23054f08401887f000447d7be1e`.
19. [screenshots/kycalendar-04-confirmation-week-desktop-dark.png](screenshots/kycalendar-04-confirmation-week-desktop-dark.png) — 1280×900, 83516 bytes, SHA-256 `1993596e8e0198d050a796f28f37ed9a360ed9d4fce69a9efe2c6d737197a0dc`.
20. [screenshots/kycalendar-04-confirmation-week-desktop-light.png](screenshots/kycalendar-04-confirmation-week-desktop-light.png) — 1280×900, 83260 bytes, SHA-256 `a05b35ea4a9354b1f2bb39c59dc7d44c7721a741e1bb2f9a2b8b5210eba220eb`.

## Verification limits and DOX

The fresh work was read-only integrity/arithmetic validation plus original-image and pinned-source inspection. It establishes no complete keyboard, focus, contrast, state or responsive gate: all five remain **untested** by this auditor. Existing navigation/Week/focus probes stay recorded evidence and were not rerun. No browser, build, application test, runtime edit, capture, commit or subdelegation occurred.

Rechecked the applicable DOX chain, including the parent's concurrent owner-decision rule. The auditor left AGENTS untouched: this assigned evidence pair adds no auditor change to durable ownership, workflow, contract or child index. Concurrent parent changes to AGENTS, the council summary, workbook/exporter/ratings and owner-decision record were not edited or reverted. The owner's existing preference is recorded here within assigned scope.

MySlop remains unavailable because the credential is known revoked (401). Per instruction, no read/post/retry or board-status update was attempted. Parent continuation is the concrete Month correction, with no acceptance issued here.

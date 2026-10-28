# Rubric `ky-suite-v2`

Fixed for every product and every round until a new version is named. If you change a
weight, an anchor or a cohort definition, give the rubric a new version and report drift.

## Inventory

Before scoring, list every file in the source set, including files you will not score.

- Record `file`, `sha256`, `bytes`, `width`, `height`.
- **Exact duplicate**: same SHA-256. **Near duplicate**: same pixel dimensions and a pixel
  diff below 0.1% of pixels, max channel delta ≤ 8. Record the measured diff either way.
- Score each duplicate group once, on its first file by name. Every other member gets
  `duplicate_of` and inherits the scores, and is excluded from every count and mean.
- Record `filename_matches_content: false` with what the screen actually shows. Score the
  observed product.
- Compare the per-file list against the inventory before publishing. Report every unscored file
  with its reason.

## Evidence row (one per unique image)

| Field | Content |
|---|---|
| `observed_product`, `observed_surface` | from visible content |
| `cohort` | one cohort key from the list below |
| `state` | `empty`, `sparse` (1–4 items), `populated` |
| `theme` | `light`, `dark`, or a named theme; note user wallpapers |
| `viewport` | `desktop` / `mobile`, with pixel size and scale if known |
| `artifacts` | overlays, cursors, browser prompts, password managers, redactions, video stills |
| `scores` | integer 0–10 per dimension |
| `observations` | one concrete visible fact per dimension that justifies the score |
| `confidence` | `high` (clear full-resolution view), `medium` (artifacts or low resolution obscure part), `low` (most of the surface obscured or ambiguous) |
| `caveat` | what this image cannot show or what distorts it |

Score what was captured: an overlay or wallpaper stays in the score, with a caveat. Do not
mentally remove it, and do not penalise the product for a third-party overlay beyond what
it hides.

## Quality score (all products, brand-neutral)

The weights match the initial round, so drift comes from the anchors and cohorts.

| Key | Weight | Judges |
|---|---|---|
| `hierarchy` | 0.20 | one obvious primary action, a clear title, scannable grouping |
| `typography` | 0.15 | scale steps, weight use, legibility, restraint |
| `density_layout` | 0.20 | alignment, rhythm and space use, **judged for the captured state** |
| `color` | 0.15 | restraint, semantic use, apparent contrast in the theme as captured |
| `visible_affordances` | 0.15 | buttons, inputs, selection and disabled states that read correctly at a glance |
| `coherence` | 0.15 | one component vocabulary on the screen, no stray styles or nested chrome |

`total = Σ weight × score`, rounded half-up to one decimal
(`Decimal(str(x)).quantize(Decimal("0.1"), ROUND_HALF_UP)`). Compute it with a script.

### Anchors

| Score | Meaning |
|---|---|
| 3 | Broken. Overlap, clipping, unreadable text, or no discernible primary action. |
| 5 | Works but fights you. Competing emphasis, mixed component styles, nested boxes, weak or ambiguous states. |
| 7 | Solid. One clear primary, consistent components, minor rhythm, contrast or emphasis slips that a scanning user notices. |
| 9 | Refined. Nothing to remove. Every element earns its emphasis, rhythm is even, states are unambiguous, and type and color carry hierarchy without decoration. |

Use 4, 6 and 8 for the space between anchors, and 10 only when no defect is visible at full resolution.

- **State.** An empty state is judged on empty-state quality, meaning guidance and the next action. A sparse
  list is judged on how its rows would scale. Density is never rewarded for having more data.
- **Theme.** A dark theme gets no automatic bonus or penalty. Judge each theme against its own background.
  A user-chosen wallpaper is scored as captured, with a caveat.
- **Vintage.** Score each client generation as captured, and label it (for example "Outlook 2013 desktop, video
  still"). Different generations are separate products in rankings.

## Ky brand fit (Ky screens only)

Score Ky screens only, and report the result as `ky_fit`. Never add it to `total` or compare it with competitors.
Each dimension has weight 0.20; the anchors are the same, with the design contract as the 9.

| Key | Judges |
|---|---|
| `palette_tokens` | surfaces, ink and accent read as the ky-ui token palette; no off-palette hues outside user data |
| `type_geometry` | contract type sizes, 8px rhythm, 40px controls, 8px control radius |
| `shell` | contract sidebar, wordmark, navigation and account region |
| `accent_discipline` | orange marks only the primary action, the selection and focus |
| `suite_cohesion` | the screen could belong to any other Ky product's captured set |

`suite_cohesion` requires captures from at least two Ky products. Otherwise it is `N/A`, and
`ky_fit` is the mean of the remaining dimensions, with that noted.

## Cohorts

Rank only inside one cohort. Split views from editors.

`mail-list` · `mail-read` · `compose` · `calendar-grid` (month, week or day) ·
`calendar-list` · `event-editor` · `contacts-list` · `contact-editor` · `settings` ·
`admin-console` · `member-home` · `chat` · `notes` · `drive` · `auth`

- Rank products by their mean of unique files in that cohort, and show `n`.
- Mark `matched: false` when the compared files differ in state, theme or viewport. Rank
  them anyway, but flag the Ky gap as unmatched.
- **Ky gap** = Ky mean − the best competitor mean in the same cohort.
- Publish no provider-level or cross-cohort means as rankings. If you show a coverage
  summary, label it "not a ranking".
- A cohort with no Ky file is `N/A` for Ky.

## Drift (every re-rank)

Score all original files blind first. Only then load the previous round. For each unique
file, record `prior_total`, `total`, `delta` and `cause`. Valid causes:

- `anchor`: the new anchors or state and theme rules
- `cohort`: the file moved to a different cohort
- `correction`: the previous round misread the image (name the misreading)
- `artifact`: an artifact is now handled differently

Explain every |delta| ≥ 0.5 in one sentence. Summarise the mean drift for Ky files and for
competitor files separately. If Ky drifts up much more than competitors do, find the cause before
publishing. Re-scoring unchanged images measures a rubric change, never a product improvement.

When an independent second reviewer has scored the same files, report the per-file spread.
Resolve differences of 2 or more points on any dimension by re-inspecting the image, not by
averaging.

## Behavior gates (Accept only)

Each gate is `pass`, `fail` or `untested`, and each needs evidence: a command, test name or
capture path, plus the revision. Screenshots alone cannot pass a gate.

| Gate | Pass requires |
|---|---|
| `keyboard` | every task path in scope is operable by keyboard alone, with a logical order and no traps; dialogs return focus |
| `focus` | a visible focus indicator on every interactive element in both themes |
| `contrast` | computed-style measurement: text ≥ 4.5:1 (≥ 3:1 for large text), and borders of controls, focus rings and state indicators ≥ 3:1, in every shipped theme |
| `states` | empty, loading and error states captured for each task view, with errors announced and tied to their fields |
| `responsive` | 390px and 1280px, with no horizontal overflow, navigation reachable, and targets usable |

## `scores.json` shape

The top-level keys are `schema` (`ky-ui-council-scores/2`), `round`, `reviewer`, `rubric`
(version, weights, rounding), `inventory`, `files`, `cohort_rankings`, `drift`, `gates`
(Accept only), `unscored`. A file entry holds the evidence-row fields plus `sha256`,
`width`, `height`, `duplicate_of`, `total`, and `ky_fit` where it applies.

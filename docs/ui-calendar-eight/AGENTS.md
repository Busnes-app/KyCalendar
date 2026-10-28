# KyCalendar UI Council

## Purpose

Own fresh synthetic captures, independent ratings, provenance and visual progression evidence for the focused KyCalendar pass.

## Ownership

- Parent owns review inputs, audits, comparison galleries and spreadsheet exports.
- Capture worker owns its drivers, original screenshots, manifests and capture checks.
- Each reviewer owns only its assigned raw Markdown/JSON pair; preserve published bytes.
- Raw Markdown EOF whitespace stays intact; root `.gitattributes` excludes only its `blank-at-eof` warning.
- The captured `capture-round3-confirmation.mjs` trailing whitespace stays byte-identical to its manifest; root `.gitattributes` exempts `blank-at-eol` only for that driver.

## Local Contracts

- Preserve everyday/admin separation, authorization, routes, CSP, CSRF, event semantics, calendar visibility, named themes, saved choices and OS following.
- Use one left navigation region. Keep calendar selection in the existing app-sidebar slot and phone disclosure, preserving drafts across resize.
- Use the pinned `ky-suite-v2` rubric. Keep quality and Ky brand fit separate.
- Freeze the complete per-file score vectors and observations before reading peer/prior reviews, historical quality images or parent audits. Missing evidence is N/A.
- Progression uses only current-round Opus overall quality, rounded half-up to one decimal; 8.0 accepts. No peer/history/brand pooling or individual-cohort veto. Owner rejection reopens progression.
- `owner-decisions.json` binds explicit owner rejections to their round/source; exports show that disposition without changing frozen ratings.
- Show all round scores with pending/unused reviewers. Gemini is advisory after both primary reviewers are satisfied; its known exhausted quota is not retried or a progression blocker.

## Work Guidance

- KyCalendar is the owner's highest-priority remaining web product. Begin from a fresh baseline of everyday calendar tasks and separate administrator screens.
- Phone Month prioritizes start times for timed entries, as in the fresh baseline; retain full accessible titles and all-day labels. The owner rejected wrapped or abbreviated timed titles.
- Use real local sign-in and synthetic calendar data in an owned private fixture. No production or external integrations; no authentication mocks or relaxed security.
- Match 1280×900 and 390×844, DPR1, OS light/dark, locale/timezone, displayed dates, fixture and saved-choice state across before/after views.
- Preserve raw failures and originals. Permit one bounded correction/confirmation per batch; do not overwrite earlier attempts.
- After machine hard locks, use targeted checks, capped sequential builds and one browser. Run required full suites in hosted CI and reuse persistent caches.
- Keep credentials, tokens, private keys and databases outside git in owned 0700/0600 paths. Capture no filled credentials or one-time secrets.
- `capture-fixture.mjs` and `capture-checks.mjs` validate a literal `127.0.0.1` origin and unprivileged port, reject path traversal/query/fragment, and refuse redirects. Historical helper bytes remain in `*.mjs.baseline.txt` for provenance only; `driver-revisions.json` maps captured hashes to those archives.
- Screenshots establish appearance only; record complete behavior, accessibility and integration gates separately.

## Verification

- Follow `../../web/browser/AGENTS.md` for real-server browser workflows and the parent repository's required hosted CI checks.
- `node --test docs/ui-calendar-eight/fixture-network.test.mjs` checks origin/path rejection and redirect refusal; the frontend CI job runs it.
- Recompute weighted totals and half-up means; verify review membership, complete-vector freezes, image hashes/dimensions and source/served-asset bindings before publishing scores.
- Accept frozen `total_unrounded` or `weighted_total_unrounded` values in the exporter; normalize viewport display labels there without rewriting raw rows.
- `build-gallery.py --round R --scored-through T --output FILE` verifies and embeds 48 matched original pairs in an offline gallery; T explicitly limits reads to terminal review rounds. Run the HTML-page offline/contrast checker on its output.
- Export rating history and per-file evidence to a spreadsheet, preserving raw reports and unscored evidence. `python3 docs/ui-calendar-eight/build-report.py --current-round N --pr URL` validates terminal freezes/arithmetic and XLSX CRC/XML/data round-trip; it links terminal factual audits and reads only rounds through N so later partial reports stay unread.

## Child DOX Index

None.

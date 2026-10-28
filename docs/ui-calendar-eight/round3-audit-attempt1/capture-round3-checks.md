Round3 matched capture evidence at `27690a367bf1808224aba7ef4b39ab0c8937bcf7`. All50 originals retained; Chromium closed; fixture retained. No scores.

Checks: {'pass': 17, 'fail': 6}. Failures are explicit product observations, not a visual gate pass.

Product findings:
- Phone Week time labels fail actual painting despite in-viewport cell boxes; both original Week images retain the defect. CSS counter-scroll cell transforms plus clipping/stacking need parent diagnosis.
- Phone Day→Week after returning from Phones & apps has no role/aria-label/tabindex. Parent hypothesis: FullCalendar reuses timegrid DOM, so viewDidMount does not rerun; datesSet may need to reapply attributes. No runtime edit here.
- Phone Group and Role grant fields are288px and160px instead of324px; later footer flex shorthand likely wins. Add group disabled works4/4.
- Settled phone nav focus clips Phones & apps, and admin Sign-in/Backups/Two-step verification in both schemes; complete navigation visibility gate remains open.

Outputs: `inventory-v3.json` (48 unique candidates), `artifacts-v3.json` (156 originals), `manifest-03-after.json`, `sourceprovenance-round3.json`, `round3-preservation-proof.json`, `round3-title-geometry.json`, `round3-matched-pair-audit.json`, `round3-per-file-inspection.json`.

Capture correction: attempt1 stopped after21 originals on missing Week region role. ONE bounded completion wrote29 missing originals; first21 hashes unchanged. Raw driver/manifest/log and failure retained; no cosmetic recapture.

Checks:
- PASS: all protected bytes preserved
- PASS: 50 originals and exact original role/surface/order
- PASS: source HEAD and runtime frozen
- FAIL: owned live fixture identity and Go metadata
- PASS: unchanged seed and display contract
- PASS: one bounded completion; no originals replaced
- PASS: 156 unique originals with complete identities
- PASS: current membership and pinned duplicate protocol
- PASS: matched capture conditions
- PASS: browser HTTP CSP console and network diagnostics
- PASS: production CSP and real service workers
- PASS: original and supplemental served-asset bindings
- PASS: Enter Escape replacement event focus in four conditions
- PASS: stable challenge typing clearing four conditions
- PASS: phone Week keyboard movement and original offset restoration
- PASS: initial phone Week today header and column within internal viewport
- FAIL: phone Week time gutter actually painted before and after movement
- FAIL: phone Week accessible region and tabindex after Day to Week/remount
- FAIL: settled focus probes show full phone navigation controls
- PASS: Add group disabled with no selection in zero-group fixture
- FAIL: phone grant fields use full card width
- FAIL: phone Month two visible title rows without page overflow
- PASS: tall timed titles permit two lines

Limitations:
- Complete behavior/accessibility/integration and physical-device gates remain untested. No full tests/builds rerun: user capture-only scope; previous parent test results are not this worker fresh evidence.
- Real Oct28 clock retained. Today moves toWednesday28; time indicator and People last-sign-in changes are clock state. Captures span initial attempt and bounded missing-file completion; use per-image timestamps.
- Week opens today/current anchor by selected refinement. Previous Monday-empty/Tuesday-clipped premise was disproved; Tuesday already fit. Raw R1/R2 opinions remain unchanged; no false repair or rerating claimed.
- Native date End segment selection remains original driver artifact; no focus cleared or product focus changed.
- No extra capture attempt after the one bounded completion. Role-locator timeout is retained and maps to a possible product Day/Week DOM lifecycle defect, not merely driver error.
- MySlop unavailable: user reports known revoked401; no retry or external requests.
- DOX pass: root/web/browser/evidence AGENTS intentionally unchanged; this task adds evidence under existing ownership/contracts and is expressly scoped away from runtime/instruction edits.

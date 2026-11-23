# Skeleton

## Purpose
Splits a calendar object into the readable skeleton the server stores in `calendar_objects.data`,
and merges the skeleton with the opened sealed parts for a reader. This is content encryption
spec E1, E2, E4 and E5, with the rules in `docs/CONTENT-ENCRYPTION-FORMAT.md` under "Skeleton
and content".

## Ownership
Owns the readable allowlist, the skeleton split, the merge, and the patch operations and labels.
It does no cryptography: `internal/contentkey` seals and opens the plaintexts. Nothing calls it
until build step CE2.

## Local Contracts
- **The allowlist is what the operator can read.**
  - Adding a property makes it readable on every install.
  - Removing one breaks any worker that reads it. KyCalendar's own `X-KY-*` and
    `X-KYCALENDAR-*` markers are on the list because workers read them.
  - Any change updates spec E1 and the format document in the same commit.
- **The skeleton is the truth for readable properties and for the set of components and
  alarms.** The newest base gives content, and the newest unmarked base gives alarm text.
- **Overrides:**
  - an override the newest base doesn't carry takes its master's content;
  - one the base does carry takes only its own;
  - patches for an override apply after either.
- **Patches:**
  - `set` and `remove` touch only content properties, and `EncodePatch` and `DecodePatch`
    refuse a readable one;
  - a label never carries a value;
  - a patch older than the newest base doesn't apply.
- **Fast path:** a lone base whose own skeleton equals the stored one is served byte for byte,
  which keeps the "served as the raw bytes received" contract.

## Verification
`go test ./internal/skeleton`. It covers:
- the corpus of real client fixtures (split leaves no content, fast path is byte-exact, the slow
  path keeps every property);
- the merge and patch rules.

The repository's `make ci` runs it too.

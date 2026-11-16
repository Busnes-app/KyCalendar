# Tasks

## Purpose
Reads a VTODO's master component into a `Task` and edits it in place (`Patch`, `Apply`, `New`) for the web Tasks API and the MCP task tools. `Gaps` splits a day into busy blocks and free gaps.

## Ownership
Owns the VTODO property mapping and the repeating-completion maths. Validation of request bounds, authorization and storage are `internal/api`'s; the write path is `davbackend`'s.

## Local Contracts
- An edit changes only the properties its `Patch` names; every other property, parameter, VALARM and VTIMEZONE stays. Parameter order is the encoder's.
- DUE and DURATION never coexist (RFC 5545 §3.6.2): a length on a task with DUE, or without DTSTART, moves to `X-KY-ESTIMATE`.
- Completing a task with an RRULE moves DUE and DTSTART to the rule's next date after the current one (after the completion day with `X-KY-REPEAT-FROM:COMPLETION`), keeping the wall-clock time across DST; `COUNT` drops by one; no next date completes it. `X-KY-REPEAT-FROM` and `X-KY-HABIT` are removed with the rule.
- Only DAILY to YEARLY rules that pass `calendar.SafeRule` are stepped, and the next occurrence is searched for within 10 years; any other stored rule refuses completion (`ErrInvalid`, 400 at the API) and leaves the task unchanged.
- `Change(prev, next, received)` decides what a write means for the completion log: newly completed, reopened (same occurrence), or a repeating task moved to the rule's next occurrence (judged by completing a copy of prev); the occurrence is the DUE (else DTSTART) value with its TZID.
- No I/O, no logging.

## Verification
- `go test ./internal/tasks/` (DST, repeat-from-completion, nth and last weekday, interop fixtures)

## Child DOX Index
None.

# Tasks

## Purpose
Reads a VTODO's master component into a `Task` and edits it in place (`Patch`, `Apply`, `New`) for the web Tasks API and the MCP task tools. `Gaps` splits a day into busy blocks and free gaps.

## Ownership
Owns the VTODO property mapping and the repeating-completion maths. Validation of request bounds, authorization and storage are `internal/api`'s; the write path is `davbackend`'s.

## Local Contracts
- An edit changes only the properties its `Patch` names; every other property, parameter, VALARM and VTIMEZONE stays. Parameter order is the encoder's.
- DUE and DURATION never coexist (RFC 5545 §3.6.2): a length on a task with DUE, or without DTSTART, moves to `X-KY-ESTIMATE`.
- Completing a task with an RRULE moves DUE and DTSTART to the rule's next date after the current one (after the completion day with `X-KY-REPEAT-FROM:COMPLETION`), keeping the wall-clock time across DST; `COUNT` drops by one; no next date completes it. `X-KY-REPEAT-FROM` and `X-KY-HABIT` are removed with the rule.
- No I/O, no logging.

## Verification
- `go test ./internal/tasks/` (DST, repeat-from-completion, nth and last weekday, interop fixtures)

## Child DOX Index
None.

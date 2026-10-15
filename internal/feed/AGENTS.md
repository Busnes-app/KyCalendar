# Feed

## Purpose
Mirrors published ICS links (an Outlook or Google calendar's "publish" link) into read-only
subscribed calendars.

## Ownership
Owns link checks, the fetching client and its address policy, feed parsing, link sealing and
the refresh (`Refresher.Check`, `RunDue`). Routes live in `internal/api/subscriptions.go`;
storage and the read-only guard in `internal/store`.

## Local Contracts
- A link is a bearer secret. `Seal`/`Open` encrypt it under `crypto.DeriveKey(master, "kycalendar:calendar-subscription-url")`. Logs and errors carry the host at most; `Message` maps every failure to a fixed user-facing string and never to response text or the URL.
- `NormalizeURL`: `https://` or `webcal://` (rewritten to https), a host, no user info, at most 2048 bytes; the fragment is dropped.
- `NewClient` is `sso.NewGuardedClientLimit` with the body cap at `MaxBody`+1 (10 MiB; over it is "too large") and up to 3 redirects; every hop is HTTPS and every dial passes the policy. `RefusePrivate` (production) refuses everything `sso.RefuseLocal` does plus RFC 1918, IPv6 ULA and 100.64.0.0/10: links come from everyday users, so the server never fetches the LAN.
- `Parse` groups VEVENTs by UID (master and `RECURRENCE-ID` overrides together) with the VTIMEZONEs they name, drops VALARMs, METHOD and other components, and passes each object through `davbackend.Prepare` (the CalDAV write checks); failures are skipped and counted. More than `MaxObjects` (5000) UIDs refuses the feed. Encoding is deterministic and DTSTAMP is pinned to LAST-MODIFIED (else the epoch), because Exchange restamps DTSTAMP on every fetch; `TestParseStable` pins that an unchanged feed gives identical bytes.
- Feeds are hostile input. `Parse` recovers go-ical's panics (it panics on some malformed parameters, and refreshes run outside HTTP recovery) as "not a calendar"; refuses any parameter value over `maxParamValue` (1 KiB) before decoding, because go-ical builds parameter values a byte at a time (`longParams` splits lines and parameters exactly as `decoder.go` does: lines on `\n` with only a trailing `\r` dropped, a parameter name running to the next `=` even past a `:`, quoted and unquoted values; keep it in step if go-ical is bumped); and refuses a feed once the encoder has produced `maxParsed` (32 MiB), because time zones are copied into every object that names them: every byte offered to the encoder counts, kept or skipped, and each object's encoding stops at `calendar.MaxObjectSize` (`capped`). `TestParseContainsDecoderPanics`, `TestParseRefusesHugeParameters` and `TestParseCapsExpandedSize` pin these.
- Time is bounded too: `calendar.Inspect` may spend its per-object recurrence budget on every one of up to 5000 objects, so `Parse(ctx, ...)` gets `maxParseTime` (5 s) for the whole feed, checked between objects ("too large" when it runs out), and stops early when the caller's `ctx` ends. `TestParseStopsAtItsDeadline` and `TestLoadHonoursItsContext` pin both.
- `Refresher.Load` holds one of 4 server-wide `slots` while it fetches and parses, so routes and the refresh loop together never run more than 4 feeds at once.
- `Check` records every outcome: success clears `failures`/`error` and schedules `Interval` (15 min) plus up to 2 min jitter; a failure keeps the last events and backs off 15 min, 1 h, then 6 h. 404/410 say the link no longer works. A 304 changes nothing.

## Verification
- `go test ./internal/feed/` (fixture `testdata/m365.ics` is shaped like an Exchange published calendar); route-level tests in `internal/api/subscriptions_test.go`.

## Child DOX Index
None.

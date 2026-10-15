# KyCalendar ICS subscriptions — design

Status: approved 2026-10-15 and implemented on `feat/ics-subscriptions`. Where the build
differs from the first draft, this text was updated to match it.

## Goal

A user subscribes a personal, read-only calendar to a published ICS link, such as their
Microsoft 365 work calendar. KyCalendar keeps it current, shows it on the web and on CalDAV
devices, and their AI agent reads it through the MCP tools
(`2026-10-14-kycalendar-mcp-agent-access-design.md`) to plan around work meetings.

Invariants carried over unchanged: admin identities get no calendars; every route is in
`authz_matrix_test.go`; secrets are never logged.

## Decisions

| Question | Decision |
|---|---|
| Live or snapshot | Live subscription. A one-time import is stale the first time a meeting moves, and an agent then plans around the wrong day |
| Writes | None. No user, CalDAV client or MCP token can create, change or delete a subscribed event. Edits would be overwritten by the next refresh and never reach the employer |
| Owner | The subscribing user. Personal calendar, so the MCP tools see it with `editable: false` |
| Link | A bearer secret (anyone holding it reads the calendar). Sealed at rest, never returned by the API, never logged or audited |
| Addresses | HTTPS to public addresses only. Private LAN refused as well as loopback and link-local: the URL comes from an everyday user, not an admin |
| Refresh | Every 15 minutes, conditional GET; "Refresh now" at most once a minute |
| File upload | Not offered. M365 publishes links; a snapshot import is a separate spec if a calendar ever can't |
| Alarms | Dropped from mirrored events, so a work calendar never rings the user's phone |

Out of scope: one-time `.ics` file import, writing back to the source, CalDAV-to-CalDAV sync,
subscriptions for group calendars, an admin allowlist for LAN sources.

## 1. Data model

Migration 15, both dialects:

```sql
CREATE TABLE calendar_subscriptions (
    calendar_id TEXT PRIMARY KEY REFERENCES calendars(id) ON DELETE CASCADE,
    url_sealed TEXT NOT NULL,
    url_host TEXT NOT NULL,          -- shown in the UI and audit; no path, no query
    etag TEXT NOT NULL DEFAULT '',
    last_modified TEXT NOT NULL DEFAULT '',
    refreshed_at DATETIME,           -- last successful fetch
    checked_at DATETIME,             -- last attempt
    next_check_at DATETIME NOT NULL, -- indexed; the refresh loop picks rows at or before now
    failures INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT ''   -- short, ours, never the response body
);  -- Postgres: VARCHAR(64) id, TIMESTAMPTZ
```

- `calendars` is unchanged. `store.Calendar` gains `Subscribed bool`, read through a
  subquery in the shared calendar column list, so every calendar read carries it.
- The URL is sealed with `crypto.EncryptAESGCM` under `crypto.DeriveKey(master,
  "kycalendar:calendar-subscription-url")`, as `internal/sso` seals the client secret.
- A user has at most 10 subscriptions. Their events count toward the storage quota.

## 2. Read-only enforcement

`access.Resolve` stays as it is: the owner still renames, recolours and deletes the calendar.

- **The boundary is the store.** `PutObject` and `DeleteObject` return `ErrReadOnly` for a
  subscribed calendar inside their transaction, so no write path (web, CalDAV, MCP, or one
  added later) can edit one. This replaces the draft's lint test: a guard in the one write
  path cannot be bypassed by a new caller.
- **The callers explain it.** `access.CanEditEvents(c, role)` (`CanWrite` and not subscribed)
  replaces `role.CanWrite()` at every event-write site, for a clear 403 and for the
  `editable` flag. CalDAV advertises the calendar read-only (`ReadOnly` in the privilege
  set), so iOS and DAVx5 grey out editing. The MCP tools answer "this calendar mirrors a
  subscription and is read-only".
- The refresh is the only writer: `ReplaceSubscriptionObjects`, which runs the CalDAV write
  checks first (`davbackend.Prepare`).

A test drives every write path (web create, update, delete; CalDAV PUT, DELETE; MCP create,
update under both write scopes) against a subscribed calendar and expects a refusal.

## 3. Fetching

A client from `sso.NewGuardedClient` with a new policy and a per-client body cap:

- **Policy `RefusePrivate`:** `RefuseLocal` plus `IsPrivate` (RFC 1918, IPv6 ULA) and
  100.64.0.0/10. Applied at dial time to the resolved address, so DNS rebinding to the LAN is
  refused.
- HTTPS only. `webcal://` is accepted on input and rewritten to `https://`.
- Up to 3 redirects, each HTTPS. Every hop dials through the same policy. (The SSO client
  refuses redirects; whether M365 published links redirect is unproven and is checked against
  a real link in the plan.)
- Body cap 10 MiB (the SSO client's 1 MiB becomes a parameter). Over the cap → error, nothing
  replaced.
- 20 s total timeout, `If-None-Match` / `If-Modified-Since` from the stored values. A 304
  updates only `checked_at` and `refreshed_at`.

## 4. Applying a feed

1. Decode one `VCALENDAR`. Not a calendar → error, nothing replaced.
2. Group `VEVENT`s by UID: master plus its `RECURRENCE-ID` overrides become one object, with
   the `VTIMEZONE`s it references. Other components (VTODO, VJOURNAL) and VALARMs are dropped.
3. Each object goes through the CalDAV write checks (`davbackend.Prepare`: size,
   `ValidateCalendarObject`, `calendar.Inspect`). Objects they refuse are skipped and counted
   in the log; M365's Windows zone names resolve through the existing `windowszones` table.
   The object name is a hash of the UID, so it is stable across refreshes and URL-safe.
4. Limits: 5000 objects; over that → error, nothing replaced.
5. Encoding is deterministic, and DTSTAMP is pinned to LAST-MODIFIED (else the epoch):
   Exchange restamps DTSTAMP on every fetch, which would otherwise rewrite every event on
   every refresh.
6. In one transaction: upsert objects whose bytes changed, delete UIDs no longer in the feed,
   one change row (and `seq` step) per object changed. An unchanged feed writes nothing. CalDAV
   sync tokens then report the diff to devices as usual.

An empty but valid feed empties the calendar: that is what the source says.

## 5. Refresh and errors

- A per-minute loop in `cmd/server` picks up to 20 subscriptions whose `next_check_at` has
  come, one after another. A success schedules the next check 15 minutes on, plus up to 2
  minutes of jitter so many users do not hit M365 in one burst.
- Failures back off to 15 min, 1 h, then 6 h. The last good events stay. `error` holds one of
  our messages, for example "the link no longer works (404)", "not a calendar", "too large",
  "address refused", "storage full".
- 404 or 410 means the user stopped publishing or reset the link. The calendar keeps its last
  events and says so; it never deletes itself.
- `POST /api/calendars/{id}/refresh` runs one fetch now; 429 if the last check was under a
  minute ago.

## 6. API

| Method | Route | Auth | Body → result |
|---|---|---|---|
| POST | `/api/subscriptions` | everyday session | `{url, name, color?}` → 201 calendar. Fetches once before saving: a bad link fails here with its error (422), and nothing is stored |
| POST | `/api/calendars/{id}/refresh` | everyday session, owner | → 200 calendar view, a failure in `subscription.error`; 429 under a minute after the last check; 404 for others and for non-subscribed calendars |
| PATCH | `/api/subscriptions/{id}` | everyday session, owner | `{url}` → replace the link (the user reset it in Outlook). Same first fetch |

Calendar views gain `subscription: {host, refreshed_at, checked_at, error?}`. Never the URL.
Delete is the existing calendar delete. Admins get 403 on all of these, as on every calendar
route. Authz matrix rows for each. Each link a user tries is a fetch by the server, so create
and PATCH share a limit of 10 tries per user per 15 minutes (429).

Audit: `subscription.create` and `subscription.url_change` with user, calendar ID and
`host=`; deletion is the existing `calendar.delete`. Refresh failures are logged with host and
error, not audited.

## 7. Web UI

- "Subscribe to a calendar" next to "New calendar": link field, name, colour. Help text for
  Outlook: Settings → Calendar → Shared calendars → Publish a calendar → "Can view all
  details" → copy the ICS link.
- The help text says the link lets anyone who has it read the calendar, and that whatever the
  user connects to KyCalendar (their AI agent included) will see these events.
- Subscribed calendars show "Updated 5 min ago" or the error, and a "Refresh" button. Their
  events open read-only and cannot be dragged.

## 8. MCP

No new tools. `list_calendars` gains `read_only` (true for subscriptions) so the agent knows
before it tries. `list_events` already reports `editable: false`. The MCP spec's section 6
applies with more force: work invitations carry text written by outsiders. The skill's rule 1
covers it; the skill adds one line: "Read-only calendars mirror another system. To change
those events, the user changes them at the source."

## 9. Backup

Resolved: a capsule is a snapshot of the whole SQLite database plus `data/encryption.key`
(`internal/backup`), so subscriptions, their sealed links and their events restore, and the
restored key opens the links. `ResetAfterRestore` does not touch them. If a link ever cannot
be opened, the calendar keeps its events and shows an error; "Change link" fixes it.

## 10. Testing

- Address policy: loopback, link-local, 169.254.169.254, RFC 1918, ULA, CGNAT and a DNS name
  resolving to each are refused; a redirect to any of them is refused; `http://` is refused.
- Read-only: CalDAV PUT and DELETE, REST create, update and delete, and MCP create and update
  (both write scopes) are refused on a subscribed calendar; rename, recolour and delete of the
  calendar itself work.
- Apply: a fixture shaped like an M365 published feed (Windows zone names, recurring series
  with overrides and exceptions, alarms, METHOD); written by hand, not captured from a real
  link. A Google fixture is not included. A second feed with one event moved, one removed and
  one added produces exactly that diff; an unchanged or restamped feed writes nothing; a 304
  changes nothing.
- Limits: oversized body, 5001 objects, non-calendar body and quota overflow each leave the
  previous events intact and set `error`.
- Secrets: the URL is absent from every API response, log line and audit row (a test greps
  captured logs for the URL's path).
- Authz matrix rows for the three routes, including another user's calendar (404) and admin
  (403).
- Before merge: subscribe a real M365 published link on a dev instance; confirm it appears on
  the web, on an iOS or DAVx5 device read-only, and through `list_events` with
  `editable: false`. Record it in `docs/evidence/`.

## Open questions

1. Refresh interval: 15 minutes is a guess at what M365 tolerates and how quickly it
   republishes changes. The real-link check measures how stale the published feed is.

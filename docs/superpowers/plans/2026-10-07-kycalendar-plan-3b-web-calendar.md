# KyCalendar Plan 3b: Web Calendar Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A FullCalendar web calendar for everyday users on top of Plan 3's JSON API, with month, week, day and list views, a calendar sidebar, an event form and drag editing where the user's role allows.

**Architecture:**
- A typed API module (`calendarApi.ts`) wraps the Plan 3 routes.
- Pure helpers (`eventForm.ts`, `linkify.tsx`) translate between form state and API bodies and render untrusted text.
- React components (sidebar, event dialog, scope dialog, `CalendarPage`) drive FullCalendar v6, whose events come from `GET /api/events`.
- Two small server additions: `series_start`/`series_end` on every event instance, so "all occurrences" edits start from the series rather than the clicked occurrence; and a `create-user` CLI command so the browser suite can sign in as an everyday user.

**Tech Stack:** React 19 + TypeScript + Vite; FullCalendar standard 6.1.21 (MIT): `@fullcalendar/core`, `react`, `daygrid`, `timegrid`, `list`, `interaction`; vitest with jsdom; Playwright Chromium against the real server; Go for the two server additions.

**Spec:** `docs/superpowers/specs/2026-10-06-kycalendar-v1a-design.md`, section 3 (UI bullets). This plan is built on `feat/kycalendar-plan-3` (PR #9): the API is described in `internal/api/AGENTS.md` on that branch.

## Global Constraints

- UI (FullCalendar standard):
  - month, week, day and list views; a sidebar of personal and group calendars with visibility toggles and colours;
  - drag to create, move and resize where the role allows;
  - event form: title, time, all-day, location, description, calendar; repeat presets none, daily, weekly with weekdays, monthly, yearly. Rules outside the presets show "Custom (edit on your device)" and are preserved;
  - settings: app passwords created and shown once, listed and revoked, plus the CalDAV URL and setup steps for iOS, DAVx5 and Thunderbird (this already exists as the Phones and apps page and stays);
  - ky-ui tokens vendored with `check-vendor.mjs`; FullCalendar CSS variables mapped to `--ky-*`; Busnes light/dark following the OS;
  - the list view and the event form are the keyboard path for every action.
- Event text is untrusted: descriptions and locations render as text, only `http`/`https` links are linkified, and the strict CSP forbids inline script. Do not relax the CSP. FullCalendar v6 injects its stylesheet from JavaScript, which `style-src 'self' 'unsafe-inline'` already allows; the browser suite must prove no CSP violation.
- Owners may add, rename and recolour personal calendars. Managers may rename and recolour group calendars. Group calendars are created and deleted by administrators only.
- Administrator identities never see the calendar (Plan 2). Their navigation has no calendar tab, and the API refuses them anyway.
- Dependencies are pinned exactly. Every FullCalendar package must be the same 6.1.21 release: v7 changed the core's structure and has not published v7 view plugins, so mixing the two lines is not allowed.
- `web/dist` is committed and embedded: rebuild it and commit it with every frontend change. Restore `web/tsconfig.tsbuildinfo` if a build touched it.
- The browser suite runs light and dark at 390 px and 1280 px against the real server with production CSP; it never mocks responses.

Decisions this plan makes that the spec leaves open:
- **The calendar is the landing page for everyday users.** Administrators keep the overview.
- **On screens 720 px wide or less,** the initial view is the week list, which is also the keyboard path. Wider screens start on the week grid.
- **Calendar visibility** is browser-local, stored in `localStorage` under `kycalendar.hiddenCalendars`.
- **Dragging a recurring occurrence** asks "This event / All events / Cancel". "All events" shifts the series start and end by the drag delta and keeps the stored rule (`repeat.freq = "custom"`).
- **When editing an event,** the request's `zone` is the event's stored zone if the browser recognises it as an IANA name, otherwise the browser's zone. Instants are always computed from the browser's local form values.
- **All-day end dates are inclusive in the form** and exclusive on the wire, as the API and FullCalendar both use.

## Review Focus

1. **A recurring event's "All events" edit opened from a later occurrence** must keep the series' original start. Pinned in Task 3 (`formFromEvent` test using `series_start`) and Task 2 (`series_start` in the API).
2. **An event whose title or description contains HTML or a `javascript:` link** renders as text, with no link. Pinned in Task 3 (`linkify` tests) and Task 5 (dialog test).
3. **A phone changed the event while the form was open.** Saving shows a conflict message and reloads instead of overwriting. Pinned in Task 5 (412 handling test).
4. **A reader** sees events but no drag handles and no Save or Delete buttons. Pinned in Task 4 (`editable` false) and Task 5 (read-only dialog).
5. **At 390 px wide** the page does not scroll horizontally and the list view works by keyboard. Pinned in Task 7 (browser test).

---

## File Structure

| Path | Responsibility | Task |
|---|---|---|
| `cmd/server/createuser.go`, `cmd/server/createuser_test.go` (new) | `create-user` CLI: a local everyday account, password from stdin | 1 |
| `internal/calendar/expand.go` | `SeriesTimes`: a master's start and end for a viewer | 2 |
| `internal/api/events.go` | `series_start`/`series_end` on event instances | 2 |
| `web/src/calendarApi.ts` (new) | Typed client for the Plan 3 routes | 3 |
| `web/src/eventForm.ts` (new) | Form state ↔ API body, date helpers, presets | 3 |
| `web/src/linkify.tsx` (new) | Text rendering with http/https links only | 3 |
| `web/src/components/CalendarSidebar.tsx` (new) | Calendar list, visibility, new/rename/recolour/delete | 4 |
| `web/src/pages/CalendarPage.tsx` (new), `web/src/styles/calendar.css` (new) | FullCalendar views, event source, theme mapping, navigation | 4 |
| `web/src/components/EventDialog.tsx` (new) | Create, edit, delete, read-only details, scope choice | 5 |
| `web/src/components/ScopeDialog.tsx` (new) | "This event / All events / Cancel" for recurring drags | 6 |
| `web/browser/calendar.spec.mjs` (new), `web/browser/server.mjs`, `web/browser/setup.mjs` | Real-server browser regression for the calendar | 7 |
| `AGENTS.md`, `web/AGENTS.md`, `README.md` | Contracts and operator notes | each task, 7 |

---

### Task 1: `create-user` CLI

**Files:**
- Create: `cmd/server/createuser.go`, `cmd/server/createuser_test.go`
- Modify: `cmd/server/main.go` (the `switch os.Args[1]` and the usage text, if any)
- Docs: `README.md` (CLI section), root `AGENTS.md` (Operating rules)

**Interfaces:**
- Produces: `kycalendar create-user -username <name>`, which reads the initial password from stdin; `func createUser(ctx context.Context, st store.Store, username, pw string) error`; `var errUserExists`.

- [ ] **Step 1: Write the failing test**

`cmd/server/createuser_test.go`:

```go
package main

import (
	"context"
	"errors"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

func TestCreateUser(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	if err := createUser(ctx, st, "walter", "WalterInitial123!"); err != nil {
		t.Fatal(err)
	}
	u, err := st.Users().GetUserByUsername(ctx, "walter")
	if err != nil || u.Role != "user" || u.Status != "active" || u.SSOProvider != "local" || !u.MustChangePassword || u.PasswordHash == "" {
		t.Fatalf("created %+v %v", u, err)
	}
	if err := createUser(ctx, st, "walter", "AnotherPassword123!"); !errors.Is(err, errUserExists) {
		t.Fatalf("second create: %v, want errUserExists (never reset an account)", err)
	}
	for _, bad := range []struct{ name, pw string }{{"", "WalterInitial123!"}, {"x", "short"}} {
		if err := createUser(ctx, st, bad.name, bad.pw); err == nil {
			t.Errorf("createUser(%q, %q) accepted", bad.name, bad.pw)
		}
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./cmd/server/ -run TestCreateUser`
Expected: FAIL to compile (`createUser` undefined).

- [ ] **Step 3: Implement `cmd/server/createuser.go`**

```go
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/Busnes-app/ky-primitives/password"
	"github.com/Busnes-app/kycalendar/internal/auth"
	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/store"
)

var errUserExists = errors.New("a user with that name already exists")

// runCreateUser creates a local everyday account. The initial password is read from stdin so it
// never appears in the process list; the user must replace it at first sign-in.
func runCreateUser(args []string) {
	fs := flag.NewFlagSet("create-user", flag.ExitOnError)
	username := fs.String("username", "", "username (required)")
	_ = fs.Parse(args)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		log.Fatal("Error: write the initial password to stdin")
	}
	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}
	ctx := context.Background()
	st, err := store.Open(ctx, cfg.Database)
	if err != nil {
		log.Fatalf("DB error: %v", err)
	}
	defer st.Close()
	if err := createUser(ctx, st, *username, strings.TrimRight(line, "\r\n")); err != nil {
		log.Fatalf("Failed to create user: %v", err)
	}
	log.Printf("✓ User %q created; they must change the password at first sign-in", *username)
}

func createUser(ctx context.Context, st store.Store, username, pw string) error {
	if err := auth.ValidateUsername(username); err != nil {
		return err
	}
	if err := auth.ValidatePassword(pw); err != nil {
		return err
	}
	if _, err := st.Users().GetUserByUsername(ctx, username); err == nil {
		return errUserExists
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	hash, err := password.Hash(pw)
	if err != nil {
		return err
	}
	return st.Users().CreateUser(ctx, &store.User{
		ID: fmt.Sprintf("usr_%s", crypto.RandomHex(12)), Username: username, DisplayName: username,
		PasswordHash: hash, Role: "user", Status: "active", SSOProvider: "local", MustChangePassword: true,
	})
}
```

In `cmd/server/main.go`, add `case "create-user": runCreateUser(os.Args[2:]); return` beside `init-admin`, in the same style as the neighbouring cases. Read `auth.ValidateUsername` and `auth.ValidatePassword` first. If `ValidateUsername("")` does not already return an error, add an explicit empty-name check. Do not change the shared validators.

- [ ] **Step 4: Run the tests**

Run: `go test ./cmd/server/`
Expected: PASS.

- [ ] **Step 5: Docs and commit**

- `README.md`: in the section that documents `init-admin`, add `create-user`: it creates a local everyday account for testing or for instances without KyIdentity; the password is read from stdin, e.g. `printf '%s\n' "$PW" | kycalendar create-user -username alice`; the user must change it at first sign-in; an existing name is refused, never reset.
- Root `AGENTS.md` Operating rules, after the init-admin bullet: "`create-user` makes a local everyday account (password from stdin, forced change at first sign-in); it refuses an existing name."

```bash
git add cmd/server README.md AGENTS.md
git commit -m "feat(cli): create local everyday accounts with the password on stdin"
```

---

### Task 2: Series start and end on event instances

**Files:**
- Modify: `internal/calendar/expand.go` (add `SeriesTimes`), `internal/calendar/expand_test.go`
- Modify: `internal/api/events.go` (`eventView`, `instanceView` and its call site), `internal/api/events_test.go`
- Docs: `internal/api/AGENTS.md`, `internal/calendar/AGENTS.md`

**Interfaces:**
- Consumes: `spanOf`, `span.endAt` (existing, unexported).
- Produces: `func SeriesTimes(master *ical.Component, viewer *time.Location) (start, end time.Time, allDay bool, err error)`; event JSON fields `series_start` and `series_end`, in the same format as `start`/`end` (a date for all-day, RFC 3339 in the viewer's zone otherwise).

- [ ] **Step 1: Write the failing tests**

Append to `internal/calendar/expand_test.go`:

```go
func TestSeriesTimes(t *testing.T) {
	m, err := Master(fixture(t, "apple_weekly_override.ics"))
	if err != nil {
		t.Fatal(err)
	}
	start, end, allDay, err := SeriesTimes(m, time.UTC)
	if err != nil || allDay || start.Format(time.RFC3339) != "2026-10-05T07:00:00Z" || end.Format(time.RFC3339) != "2026-10-05T08:00:00Z" {
		t.Fatalf("%v %v %v %v", start, end, allDay, err)
	}
	m, _ = Master(fixture(t, "thunderbird_allday.ics"))
	start, end, allDay, _ = SeriesTimes(m, zone(t, "Pacific/Auckland"))
	if !allDay || start.Format("2006-01-02") != "2026-10-07" || end.Format("2006-01-02") != "2026-10-08" {
		t.Fatalf("all-day %v %v %v", start, end, allDay)
	}
}
```

Add `SeriesStart string \`json:"series_start"\`` and `SeriesEnd string \`json:"series_end"\`` to the `eventJSON` struct in `internal/api/events_test.go`, then append:

```go
func TestEventsCarrySeriesTimes(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	cookie := loginAs(t, srv, st, "sia", "user")
	group := groupCalendar(t, st, "Team")
	grantRole(t, st, group, "reader", "usr_sia")
	putObject(t, st, group, "weekly.ics", "weekly", recurringICS, 1791183600)
	w := call(t, srv, "GET", "/api/events?start=2026-10-10T00:00:00Z&end=2026-10-20T00:00:00Z&tz=Europe/Berlin", "", cookie)
	var evs []eventJSON
	if err := json.Unmarshal(w.Body.Bytes(), &evs); err != nil || len(evs) == 0 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if evs[0].Start != "2026-10-12T09:00:00+02:00" || evs[0].SeriesStart != "2026-10-05T09:00:00+02:00" || evs[0].SeriesEnd != "2026-10-05T10:00:00+02:00" {
		t.Fatalf("instance %+v", evs[0])
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/calendar/ -run SeriesTimes; go test ./internal/api/ -run Series`
Expected: FAIL (`SeriesTimes` undefined; `series_start` empty).

- [ ] **Step 3: Implement**

In `internal/calendar/expand.go`:

```go
// SeriesTimes is a master's own start and end for a viewer: the first occurrence, which the web
// edits when the user changes all occurrences.
func SeriesTimes(master *ical.Component, viewer *time.Location) (start, end time.Time, allDay bool, err error) {
	s, err := spanOf(master, viewer)
	if err != nil {
		return time.Time{}, time.Time{}, false, err
	}
	return s.start, s.endAt(s.start), s.allDay, nil
}
```

In `internal/api/events.go`:
- Add to `eventView`, after `End`: `SeriesStart string \`json:"series_start"\`` and `SeriesEnd string \`json:"series_end"\``.
- In the per-object block that already computes `repeat` and `zone` from the master, also compute the series times once: `ss, se, sAllDay, err := calendar.SeriesTimes(master, viewer)`. On error, leave them empty. Pass them into `instanceView` as one extra parameter, a small struct `series{start, end string}` formatted with the same rule `instanceView` uses (dates when `sAllDay`, RFC 3339 in `viewer` otherwise). For a non-recurring event this equals the instance's own `start`/`end`.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/calendar/ ./internal/api/`
Expected: PASS.

- [ ] **Step 5: Docs and commit**

- `internal/api/AGENTS.md`: add `series_start, series_end` to the list of event instance fields, described as "the series' first occurrence, same format as start/end; the web edits all occurrences from these".
- `internal/calendar/AGENTS.md`: one line on `SeriesTimes`.

```bash
git add internal/calendar internal/api
git commit -m "feat(api): return each event's series start and end"
```

---

### Task 3: API client, form helpers and safe text

**Files:**
- Modify: `web/package.json`, `web/package-lock.json` (FullCalendar 6.1.21, pinned exactly)
- Create: `web/src/calendarApi.ts`, `web/src/eventForm.ts`, `web/src/eventForm.test.ts`, `web/src/linkify.tsx`, `web/src/linkify.test.tsx`
- Docs: `web/AGENTS.md`

**Interfaces:**
- Produces:
  - Types `CalendarInfo`, `RepeatInfo`, `EventInfo`, `EventBody` and the class `ApiError`.
  - API functions:

    ```ts
    listCalendars(): Promise<CalendarInfo[]>
    createCalendar(b: { name: string; color?: string }): Promise<CalendarInfo>
    patchCalendar(id: string, b: { name?: string; color?: string }): Promise<CalendarInfo>
    deleteCalendar(id: string): Promise<void>
    listEvents(start: Date, end: Date, tz: string, ids: string[]): Promise<EventInfo[]>
    createEvent(calendarId: string, b: EventBody): Promise<{ uid: string; etag: string }>
    updateEvent(ev: EventInfo, b: EventBody): Promise<{ etag: string }>
    deleteEvent(ev: EventInfo, scope: 'all' | 'this'): Promise<void>
    browserZone(): string
    ```
  - Form helpers: `FormState`, `WEEKDAYS`, `localDate`, `localDateTime`, `parseLocal`, `emptyForm`, `formFromEvent`, `bodyFromForm`, `validZone`.
  - `linkify(text: string): React.ReactNode[]`.

- [ ] **Step 1: Install FullCalendar**

Run: `cd web && npm install --save-exact @fullcalendar/core@6.1.21 @fullcalendar/react@6.1.21 @fullcalendar/daygrid@6.1.21 @fullcalendar/timegrid@6.1.21 @fullcalendar/list@6.1.21 @fullcalendar/interaction@6.1.21 && npm audit --audit-level=high`
Expected: installed, with no high or critical advisories. If audit reports one, stop and report it.

- [ ] **Step 2: Write the failing tests**

`web/src/eventForm.test.ts`:

```ts
import { describe, expect, it } from 'vitest';
import { bodyFromForm, emptyForm, formFromEvent, localDate, parseLocal, validZone } from './eventForm';
import type { EventInfo } from './calendarApi';

const base: EventInfo = {
  calendar_id: 'cal_1', uid: 'u1', recurrence_id: '20261012T070000Z', etag: 'e1', title: 'Standup',
  location: 'Room 1', description: 'd', start: '2026-10-12T09:00:00+02:00', end: '2026-10-12T10:00:00+02:00',
  series_start: '2026-10-05T09:00:00+02:00', series_end: '2026-10-05T10:00:00+02:00', all_day: false,
  recurring: true, repeat: { freq: 'weekly', weekdays: ['MO'] }, zone: 'Europe/Berlin', editable: true,
};

describe('eventForm', () => {
  it('edits all occurrences from the series start, one occurrence from its own', () => {
    const all = formFromEvent(base, 'all');
    const one = formFromEvent(base, 'this');
    expect(parseLocal(all.start).getTime()).toBe(new Date('2026-10-05T07:00:00Z').getTime());
    expect(parseLocal(one.start).getTime()).toBe(new Date('2026-10-12T07:00:00Z').getTime());
    expect(all.freq).toBe('weekly');
    expect(all.weekdays).toEqual(['MO']);
  });

  it('turns a timed form into an API body with instants and a zone', () => {
    const f = { ...emptyForm(new Date('2026-10-07T09:00:00Z'), new Date('2026-10-07T10:00:00Z'), false, 'cal_1'), title: 'A' };
    const b = bodyFromForm(f, 'Europe/Berlin');
    expect(typeof b).not.toBe('string');
    if (typeof b === 'string') return;
    expect(new Date(b.start).getTime()).toBe(new Date('2026-10-07T09:00:00Z').getTime());
    expect(b.zone).toBe('Europe/Berlin');
    expect(b.all_day).toBe(false);
    expect(b.repeat).toEqual({ freq: '' });
  });

  it('uses inclusive all-day end dates in the form and exclusive ones on the wire', () => {
    const f = emptyForm(new Date(2026, 9, 7), new Date(2026, 9, 9), true, 'cal_1');
    expect(f.end).toBe('2026-10-08');
    const b = bodyFromForm({ ...f, title: 'Trip' }, 'UTC');
    if (typeof b === 'string') throw new Error(b);
    expect([b.start, b.end, b.all_day, b.zone]).toEqual(['2026-10-07', '2026-10-09', true, undefined]);
  });

  it('refuses an end before the start and keeps custom rules', () => {
    const f = emptyForm(new Date('2026-10-07T10:00:00Z'), new Date('2026-10-07T09:00:00Z'), false, 'cal_1');
    expect(bodyFromForm(f, 'UTC')).toBe('End must not be before start');
    const custom = formFromEvent({ ...base, repeat: { freq: 'custom' } }, 'all');
    const b = bodyFromForm(custom, 'Europe/Berlin', 'all');
    if (typeof b === 'string') throw new Error(b);
    expect(b.repeat).toEqual({ freq: 'custom' });
    expect(b.scope).toBe('all');
  });

  it('accepts only IANA zones the browser knows', () => {
    expect(validZone('Europe/Berlin')).toBe(true);
    expect(validZone('Eastern Standard Time')).toBe(false);
    expect(validZone('')).toBe(false);
    expect(localDate(new Date(2026, 0, 2))).toBe('2026-01-02');
  });
});
```

`web/src/linkify.test.tsx`:

```tsx
import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { linkify } from './linkify';

describe('linkify', () => {
  it('links only http and https, renders everything else as text', () => {
    const { container } = render(<p>{linkify('see https://example.com/a?b=1. <b>bold</b> javascript:alert(1) http://x.test')}</p>);
    const links = [...container.querySelectorAll('a')].map((a) => a.getAttribute('href'));
    expect(links).toEqual(['https://example.com/a?b=1', 'http://x.test']);
    expect(container.querySelector('b')).toBeNull();
    expect(container.textContent).toContain('<b>bold</b>');
    for (const a of container.querySelectorAll('a')) {
      expect(a.getAttribute('rel')).toBe('noopener noreferrer');
      expect(a.getAttribute('target')).toBe('_blank');
    }
  });
});
```

- [ ] **Step 3: Run them to see them fail**

Run: `cd web && npx vitest run src/eventForm.test.ts src/linkify.test.tsx`
Expected: FAIL (modules not found).

- [ ] **Step 4: Write `web/src/calendarApi.ts`**

```ts
import { secureFetch } from './api';

export interface CalendarInfo {
  id: string; name: string; color: string; description: string;
  kind: 'personal' | 'group'; role: 'owner' | 'manager' | 'editor' | 'reader'; dav_path: string;
}
export type Freq = '' | 'daily' | 'weekly' | 'monthly' | 'yearly' | 'custom';
export interface RepeatInfo { freq: Freq; weekdays?: string[] }
export interface EventInfo {
  calendar_id: string; uid: string; recurrence_id?: string; etag: string; title: string;
  location?: string; description?: string; start: string; end: string; series_start: string; series_end: string;
  all_day: boolean; recurring: boolean; override?: boolean; repeat: RepeatInfo; floating?: boolean;
  unknown_zone?: boolean; partial?: boolean; zone?: string; editable: boolean;
}
export interface EventBody {
  title: string; location: string; description: string; start: string; end: string; all_day: boolean;
  zone?: string; repeat: RepeatInfo; scope?: 'all' | 'this'; recurrence_id?: string;
}

export class ApiError extends Error {
  constructor(public status: number, public code: string, message: string) {
    super(message);
  }
}

const JSON_HEADERS = { 'Content-Type': 'application/json' };

// Resolves to null on a network error so every caller handles one failure path.
const send = (url: string, init?: RequestInit) => secureFetch(url, init).catch(() => null);

async function parse<T>(res: Response | null): Promise<T> {
  if (!res) throw new ApiError(0, 'network', 'The server could not be reached');
  if (res.ok) return (res.status === 204 ? undefined : await res.json()) as T;
  const body = await res.json().catch(() => null);
  throw new ApiError(res.status, body?.code ?? '', body?.error ?? `Request failed (${res.status})`);
}

const eventPath = (ev: EventInfo) => `/api/events/${encodeURIComponent(ev.calendar_id)}/${encodeURIComponent(ev.uid)}`;
const ifMatch = (ev: EventInfo) => ({ 'If-Match': `"${ev.etag}"` });

export const listCalendars = () => send('/api/calendars').then((r) => parse<CalendarInfo[]>(r));
export const createCalendar = (b: { name: string; color?: string }) =>
  send('/api/calendars', { method: 'POST', headers: JSON_HEADERS, body: JSON.stringify(b) }).then((r) => parse<CalendarInfo>(r));
export const patchCalendar = (id: string, b: { name?: string; color?: string }) =>
  send(`/api/calendars/${encodeURIComponent(id)}`, { method: 'PATCH', headers: JSON_HEADERS, body: JSON.stringify(b) }).then((r) => parse<CalendarInfo>(r));
export const deleteCalendar = (id: string) =>
  send(`/api/calendars/${encodeURIComponent(id)}`, { method: 'DELETE' }).then((r) => parse<void>(r));

export function listEvents(start: Date, end: Date, tz: string, ids: string[]) {
  const q = new URLSearchParams({ start: start.toISOString(), end: end.toISOString(), tz });
  if (ids.length) q.set('calendar', ids.join(','));
  return send(`/api/events?${q}`).then((r) => parse<EventInfo[]>(r));
}
export const createEvent = (calendarId: string, b: EventBody) =>
  send(`/api/calendars/${encodeURIComponent(calendarId)}/events`, { method: 'POST', headers: JSON_HEADERS, body: JSON.stringify(b) })
    .then((r) => parse<{ uid: string; etag: string }>(r));
export const updateEvent = (ev: EventInfo, b: EventBody) =>
  send(eventPath(ev), { method: 'PUT', headers: { ...JSON_HEADERS, ...ifMatch(ev) }, body: JSON.stringify(b) })
    .then((r) => parse<{ etag: string }>(r));
export function deleteEvent(ev: EventInfo, scope: 'all' | 'this') {
  const q = new URLSearchParams({ scope });
  if (scope === 'this' && ev.recurrence_id) q.set('recurrence_id', ev.recurrence_id);
  return send(`${eventPath(ev)}?${q}`, { method: 'DELETE', headers: ifMatch(ev) }).then((r) => parse<void>(r));
}

export const browserZone = () => Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
```

- [ ] **Step 5: Write `web/src/eventForm.ts`**

```ts
import type { EventBody, EventInfo, Freq } from './calendarApi';

export const WEEKDAYS = ['MO', 'TU', 'WE', 'TH', 'FR', 'SA', 'SU'];

export interface FormState {
  title: string; location: string; description: string; allDay: boolean;
  start: string; end: string; // 'YYYY-MM-DDTHH:mm' local, or 'YYYY-MM-DD' (all-day, end inclusive)
  calendarId: string; freq: Freq; weekdays: string[];
}

const pad = (n: number) => String(n).padStart(2, '0');
export const localDate = (d: Date) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
export const localDateTime = (d: Date) => `${localDate(d)}T${pad(d.getHours())}:${pad(d.getMinutes())}`;

// parseLocal reads a form value in the browser's zone.
export function parseLocal(s: string): Date {
  const [date, time = '00:00'] = s.split('T');
  const [y, m, d] = date.split('-').map(Number);
  const [h, mi] = time.split(':').map(Number);
  return new Date(y, m - 1, d, h, mi);
}

const addDays = (d: Date, n: number) => new Date(d.getFullYear(), d.getMonth(), d.getDate() + n);

export function validZone(zone: string | undefined): boolean {
  if (!zone) return false;
  try {
    new Intl.DateTimeFormat('en', { timeZone: zone });
    return zone.includes('/') || zone === 'UTC';
  } catch {
    return false;
  }
}

// emptyForm starts a new event; an all-day end arrives exclusive (as FullCalendar gives it).
export function emptyForm(start: Date, end: Date, allDay: boolean, calendarId: string): FormState {
  return {
    title: '', location: '', description: '', allDay, calendarId, freq: '', weekdays: [],
    start: allDay ? localDate(start) : localDateTime(start),
    end: allDay ? localDate(addDays(end, -1)) : localDateTime(end),
  };
}

// formFromEvent fills the form for one occurrence ('this') or the whole series ('all').
export function formFromEvent(ev: EventInfo, scope: 'this' | 'all'): FormState {
  const [s, e] = scope === 'all' ? [ev.series_start, ev.series_end] : [ev.start, ev.end];
  const start = ev.all_day ? parseLocal(s) : new Date(s);
  const end = ev.all_day ? parseLocal(e) : new Date(e);
  return {
    title: ev.title, location: ev.location ?? '', description: ev.description ?? '', allDay: ev.all_day,
    calendarId: ev.calendar_id, freq: ev.repeat.freq, weekdays: ev.repeat.weekdays ?? [],
    start: ev.all_day ? localDate(start) : localDateTime(start),
    end: ev.all_day ? localDate(addDays(end, -1)) : localDateTime(end),
  };
}

// bodyFromForm builds the API body, or returns a message for the user.
export function bodyFromForm(f: FormState, zone: string, scope?: 'all' | 'this', recurrenceId?: string): EventBody | string {
  const start = parseLocal(f.start);
  const end = parseLocal(f.end);
  if (end < start) return 'End must not be before start';
  const repeat = f.freq === 'weekly' && f.weekdays.length ? { freq: f.freq, weekdays: f.weekdays } : { freq: f.freq };
  const body: EventBody = { title: f.title, location: f.location, description: f.description, all_day: f.allDay, repeat, start: '', end: '' };
  if (f.allDay) {
    body.start = localDate(start);
    body.end = localDate(addDays(end, 1));
  } else {
    body.start = start.toISOString();
    body.end = end.toISOString();
    body.zone = zone;
  }
  if (scope) body.scope = scope;
  if (scope === 'this' && recurrenceId) body.recurrence_id = recurrenceId;
  return body;
}
```

- [ ] **Step 6: Write `web/src/linkify.tsx`**

```tsx
import type { ReactNode } from 'react';

const URL_PATTERN = /(https?:\/\/[^\s<>"']+)/g;
const TRAILING = /[.,;:!?)\]]+$/;

// linkify renders untrusted text: plain text, plus http(s) URLs as links that open safely.
export function linkify(text: string): ReactNode[] {
  return text.split(URL_PATTERN).map((part, i) => {
    if (i % 2 === 0) return part;
    const trail = part.match(TRAILING)?.[0] ?? '';
    const href = trail ? part.slice(0, -trail.length) : part;
    let ok = false;
    try {
      const u = new URL(href);
      ok = u.protocol === 'http:' || u.protocol === 'https:';
    } catch {
      ok = false;
    }
    if (!ok) return part;
    return (
      <span key={i}>
        <a href={href} target="_blank" rel="noopener noreferrer">{href}</a>
        {trail}
      </span>
    );
  });
}
```

- [ ] **Step 7: Run the tests, build and commit**

Run: `cd web && npx vitest run src/eventForm.test.ts src/linkify.test.tsx && npm test && npm run build`
Expected: PASS and a successful build. Because the module is not imported yet, `web/dist` may be unchanged. Commit it if it changed, and restore `web/tsconfig.tsbuildinfo`.

In `web/AGENTS.md` Local Contracts, add: "`src/calendarApi.ts` is the only client of the calendar and event routes. It sends a strong `If-Match` on event writes and throws `ApiError{status, code}`. `src/eventForm.ts` is the pure form↔body translation: series times for 'all' edits, inclusive all-day end dates in the form and exclusive on the wire, instants from browser-local values. `src/linkify.tsx` is the only way event text becomes markup: plain text plus http/https links with `rel="noopener noreferrer"`. FullCalendar is pinned to 6.1.21 for every package."

```bash
git add web
git commit -m "feat(web): calendar API client, event form helpers and safe text"
```

---

### Task 4: Calendar page, sidebar and navigation

**Files:**
- Create: `web/src/components/CalendarSidebar.tsx`, `web/src/components/CalendarSidebar.test.tsx`, `web/src/pages/CalendarPage.tsx`, `web/src/pages/CalendarPage.test.tsx`, `web/src/styles/calendar.css`
- Modify: `web/src/App.tsx` (tab and default landing), `web/src/components/AppHeader.tsx` (nav item)
- Rebuild: `web/dist`
- Docs: `web/AGENTS.md`

**Interfaces:**
- Consumes: everything from Task 3.
- Produces:
  - `CalendarSidebar` with props `{ calendars: CalendarInfo[]; hidden: Set<string>; onToggle(id: string): void; onChanged(): void }`.
  - `CalendarPage` with props `{}` (it loads its own data).
  - Exported pure helpers from `CalendarPage.tsx`: `toFcEvent(ev: EventInfo, color: string)`, `loadHidden(): Set<string>`, `saveHidden(s: Set<string>): void`, and `HIDDEN_KEY = 'kycalendar.hiddenCalendars'`.
  - Hooks for Tasks 5 and 6: `CalendarPage` keeps `dialog` state and handlers named `openCreate`, `openEvent`, and `onMove` (drop/resize), with stub bodies in this task that Tasks 5 and 6 fill in.

- [ ] **Step 1: Write the failing tests**

FullCalendar needs layout APIs that jsdom lacks, so component tests replace `@fullcalendar/react` with a stub that records its props.

`web/src/pages/CalendarPage.test.tsx`:

```tsx
import { cleanup, render, screen, waitFor, fireEvent } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const fcProps: Record<string, unknown>[] = [];
vi.mock('@fullcalendar/react', () => ({
  default: (props: Record<string, unknown>) => {
    fcProps.push(props);
    return <div data-testid="fullcalendar" />;
  },
}));

import { CalendarPage, HIDDEN_KEY, toFcEvent } from './CalendarPage';
import type { EventInfo } from '../calendarApi';

const calendars = [
  { id: 'cal_p', name: 'Mine', color: '#00aa11', description: '', kind: 'personal', role: 'owner', dav_path: '/dav/u/calendars/default/' },
  { id: 'cal_g', name: 'Team', color: '', description: '', kind: 'group', role: 'reader', dav_path: '/dav/u/calendars/_cal_g/' },
];
const ev: EventInfo = {
  calendar_id: 'cal_g', uid: 'u1', etag: 'e', title: '<b>x</b>', start: '2026-10-07T09:00:00+02:00', end: '2026-10-07T10:00:00+02:00',
  series_start: '2026-10-07T09:00:00+02:00', series_end: '2026-10-07T10:00:00+02:00', all_day: false, recurring: false,
  repeat: { freq: '' }, editable: false,
};

function mockFetch(handlers: Record<string, () => unknown>) {
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input).split('?')[0];
    const handler = handlers[path];
    if (!handler) throw new Error(`unexpected ${path}`);
    return new Response(JSON.stringify(handler()), { status: 200 });
  });
}

beforeEach(() => {
  fcProps.length = 0;
  localStorage.clear();
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe('CalendarPage', () => {
  it('maps API events to FullCalendar events, read-only for readers', () => {
    const fc = toFcEvent(ev, '#123456');
    expect(fc).toMatchObject({ title: '<b>x</b>', allDay: false, editable: false, backgroundColor: '#123456' });
    expect(toFcEvent({ ...ev, title: '' }, '#123456').title).toBe('(no title)');
    expect(toFcEvent({ ...ev, editable: true, partial: true }, '#1').editable).toBe(false);
  });

  it('loads calendars, offers the four views and fetches only visible calendars', async () => {
    mockFetch({ '/api/calendars': () => calendars, '/api/events': () => [ev] });
    render(<CalendarPage />);
    await screen.findByLabelText('Show Team');
    const props = fcProps.at(-1)!;
    expect(String((props.headerToolbar as { right: string }).right)).toBe('dayGridMonth,timeGridWeek,timeGridDay,listWeek');
    expect(props.timeZone).toBe('local');
    expect(props.eventInteractive).toBe(true);

    fireEvent.click(screen.getByLabelText('Show Team'));
    expect(JSON.parse(localStorage.getItem(HIDDEN_KEY)!)).toEqual(['cal_g']);
    const success = vi.fn();
    const fetchEvents = fcProps.at(-1)!.events as (info: { start: Date; end: Date }, ok: (e: unknown[]) => void, fail: (e: Error) => void) => void;
    fetchEvents({ start: new Date('2026-10-05T00:00:00Z'), end: new Date('2026-10-12T00:00:00Z') }, success, vi.fn());
    await waitFor(() => expect(success).toHaveBeenCalled());
    const eventsCall = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls.map((c) => String(c[0])).find((u) => u.startsWith('/api/events'))!;
    expect(new URL(eventsCall, 'http://x').searchParams.get('calendar')).toBe('cal_p');
  });

  it('shows an alert when calendars cannot load', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('{}', { status: 500 }));
    render(<CalendarPage />);
    expect((await screen.findByRole('alert')).textContent).toMatch(/could not load/i);
  });
});
```

`web/src/components/CalendarSidebar.test.tsx`:

```tsx
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { CalendarSidebar } from './CalendarSidebar';
import type { CalendarInfo } from '../calendarApi';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const cals: CalendarInfo[] = [
  { id: 'cal_p', name: 'Mine', color: '#00aa11', description: '', kind: 'personal', role: 'owner', dav_path: '' },
  { id: 'cal_m', name: 'Team', color: '', description: '', kind: 'group', role: 'manager', dav_path: '' },
  { id: 'cal_r', name: 'News', color: '', description: '', kind: 'group', role: 'reader', dav_path: '' },
];

describe('CalendarSidebar', () => {
  it('toggles visibility and offers edits only where the role allows', () => {
    const onToggle = vi.fn();
    render(<CalendarSidebar calendars={cals} hidden={new Set(['cal_r'])} onToggle={onToggle} onChanged={vi.fn()} />);
    expect((screen.getByLabelText('Show News') as HTMLInputElement).checked).toBe(false);
    fireEvent.click(screen.getByLabelText('Show Mine'));
    expect(onToggle).toHaveBeenCalledWith('cal_p');
    expect(screen.getByRole('button', { name: 'Edit Mine' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Edit Team' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Edit News' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Delete Mine' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Delete Team' })).toBeNull();
  });

  it('creates a personal calendar', async () => {
    const post = vi.fn(() => new Response(JSON.stringify(cals[0]), { status: 201 }));
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (_i, init) => post(init));
    const onChanged = vi.fn();
    render(<CalendarSidebar calendars={cals} hidden={new Set()} onToggle={vi.fn()} onChanged={onChanged} />);
    fireEvent.change(screen.getByLabelText('New calendar name'), { target: { value: 'Work' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add calendar' }));
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
  });
});
```

- [ ] **Step 2: Run them to see them fail**

Run: `cd web && npx vitest run src/pages/CalendarPage.test.tsx src/components/CalendarSidebar.test.tsx`
Expected: FAIL (modules not found).

- [ ] **Step 3: Write `web/src/components/CalendarSidebar.tsx`**

```tsx
import { useState } from 'react';
import { createCalendar, deleteCalendar, patchCalendar, type CalendarInfo } from '../calendarApi';

interface Props {
  calendars: CalendarInfo[];
  hidden: Set<string>;
  onToggle: (id: string) => void;
  onChanged: () => void;
}

const canManage = (c: CalendarInfo) => c.role === 'owner' || c.role === 'manager';

export function CalendarSidebar({ calendars, hidden, onToggle, onChanged }: Props) {
  const [name, setName] = useState('');
  const [editing, setEditing] = useState<string | null>(null);
  const [draft, setDraft] = useState({ name: '', color: '' });
  const [error, setError] = useState<string | null>(null);

  async function run(action: () => Promise<unknown>, failure: string) {
    setError(null);
    try {
      await action();
      onChanged();
      return true;
    } catch (e) {
      setError(e instanceof Error && e.message ? e.message : failure);
      return false;
    }
  }

  return (
    <aside className="kc-sidebar" aria-label="Calendars">
      <h2>Calendars</h2>
      {error && <p role="alert">{error}</p>}
      <ul>
        {calendars.map((c) => (
          <li key={c.id}>
            <label>
              <input type="checkbox" aria-label={`Show ${c.name}`} checked={!hidden.has(c.id)} onChange={() => onToggle(c.id)} />
              <span className="kc-swatch" style={{ background: c.color || 'var(--ky-accent)' }} aria-hidden="true" />
              {c.name}
            </label>
            {canManage(c) && editing !== c.id && (
              <button type="button" aria-label={`Edit ${c.name}`} onClick={() => { setEditing(c.id); setDraft({ name: c.name, color: c.color || '#e8590c' }); }}>
                Edit
              </button>
            )}
            {c.kind === 'personal' && c.role === 'owner' && (
              <button
                type="button"
                aria-label={`Delete ${c.name}`}
                onClick={() => window.confirm(`Delete ${c.name} and every event in it?`) && void run(() => deleteCalendar(c.id), 'Could not delete the calendar')}
              >
                Delete
              </button>
            )}
            {editing === c.id && (
              <form
                onSubmit={async (e) => {
                  e.preventDefault();
                  if (await run(() => patchCalendar(c.id, draft), 'Could not save the calendar')) setEditing(null);
                }}
              >
                <label htmlFor={`name-${c.id}`}>Name</label>
                <input id={`name-${c.id}`} value={draft.name} maxLength={255} required onChange={(e) => setDraft({ ...draft, name: e.target.value })} />
                <label htmlFor={`color-${c.id}`}>Colour</label>
                <input id={`color-${c.id}`} type="color" value={draft.color} onChange={(e) => setDraft({ ...draft, color: e.target.value })} />
                <button type="submit">Save</button>
                <button type="button" onClick={() => setEditing(null)}>Cancel</button>
              </form>
            )}
          </li>
        ))}
      </ul>
      <form
        onSubmit={async (e) => {
          e.preventDefault();
          if (await run(() => createCalendar({ name }), 'Could not create the calendar')) setName('');
        }}
      >
        <label htmlFor="kc-new-calendar">New calendar name</label>
        <input id="kc-new-calendar" value={name} maxLength={255} required onChange={(e) => setName(e.target.value)} />
        <button type="submit">Add calendar</button>
      </form>
    </aside>
  );
}
```

The swatch's inline `style` is a React style property, which CSP's `style-src 'unsafe-inline'` allows; there is no inline script.

- [ ] **Step 4: Write `web/src/pages/CalendarPage.tsx` and `web/src/styles/calendar.css`**

```tsx
import { useCallback, useEffect, useRef, useState } from 'react';
import FullCalendar from '@fullcalendar/react';
import dayGridPlugin from '@fullcalendar/daygrid';
import timeGridPlugin from '@fullcalendar/timegrid';
import listPlugin from '@fullcalendar/list';
import interactionPlugin from '@fullcalendar/interaction';
import type { DateSelectArg, EventClickArg, EventDropArg, EventInput } from '@fullcalendar/core';
import type { EventResizeDoneArg } from '@fullcalendar/interaction';
import { browserZone, listCalendars, listEvents, type CalendarInfo, type EventInfo } from '../calendarApi';
import { CalendarSidebar } from '../components/CalendarSidebar';
import '../styles/calendar.css';

export const HIDDEN_KEY = 'kycalendar.hiddenCalendars';

export function loadHidden(): Set<string> {
  try {
    const v = JSON.parse(localStorage.getItem(HIDDEN_KEY) ?? '[]');
    return new Set(Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : []);
  } catch {
    return new Set();
  }
}

export function saveHidden(s: Set<string>) {
  localStorage.setItem(HIDDEN_KEY, JSON.stringify([...s]));
}

// toFcEvent hands FullCalendar an API instance; titles stay text (FullCalendar escapes them).
export function toFcEvent(ev: EventInfo, color: string): EventInput {
  return {
    id: `${ev.calendar_id}|${ev.uid}|${ev.recurrence_id ?? ''}`,
    title: ev.title || '(no title)',
    start: ev.start,
    end: ev.end,
    allDay: ev.all_day,
    editable: ev.editable && !ev.partial,
    backgroundColor: color,
    borderColor: color,
    extendedProps: { info: ev },
  };
}

const narrow = () => typeof window.matchMedia === 'function' && window.matchMedia('(max-width: 720px)').matches;

export function CalendarPage() {
  const [calendars, setCalendars] = useState<CalendarInfo[]>([]);
  const [hidden, setHidden] = useState<Set<string>>(loadHidden);
  const [error, setError] = useState<string | null>(null);
  const ref = useRef<FullCalendar>(null);

  const loadCalendars = useCallback(async () => {
    try {
      setCalendars(await listCalendars());
    } catch {
      setError('Could not load your calendars. Reload the page to try again.');
    }
  }, []);
  useEffect(() => {
    void loadCalendars();
  }, [loadCalendars]);

  const refetch = () => ref.current?.getApi().refetchEvents();
  useEffect(refetch, [hidden, calendars]);

  const colorOf = (id: string) => calendars.find((c) => c.id === id)?.color || 'var(--ky-accent)';
  const visible = calendars.filter((c) => !hidden.has(c.id)).map((c) => c.id);
  const writable = calendars.filter((c) => c.role !== 'reader');

  const fetchEvents = (info: { start: Date; end: Date }, success: (e: EventInput[]) => void, failure: (e: Error) => void) => {
    if (calendars.length === 0 || visible.length === 0) {
      success([]);
      return;
    }
    listEvents(info.start, info.end, browserZone(), visible)
      .then((evs) => success(evs.map((ev) => toFcEvent(ev, colorOf(ev.calendar_id)))))
      .catch((e: Error) => {
        setError(e.message || 'Could not load events.');
        failure(e);
      });
  };

  function toggle(id: string) {
    const next = new Set(hidden);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    saveHidden(next);
    setHidden(next);
  }

  // Filled in by Tasks 5 and 6.
  const openCreate = (_start: Date, _end: Date, _allDay: boolean) => {};
  const openEvent = (_ev: EventInfo) => {};
  const onMove = (arg: EventDropArg | EventResizeDoneArg) => arg.revert();

  return (
    <section className="page kc-layout">
      <CalendarSidebar calendars={calendars} hidden={hidden} onToggle={toggle} onChanged={loadCalendars} />
      <div className="kc-calendar">
        {error && <p role="alert">{error}</p>}
        <button type="button" disabled={writable.length === 0} onClick={() => { const s = new Date(); s.setMinutes(0, 0, 0); s.setHours(s.getHours() + 1); openCreate(s, new Date(s.getTime() + 3600_000), false); }}>
          New event
        </button>
        <FullCalendar
          ref={ref}
          plugins={[dayGridPlugin, timeGridPlugin, listPlugin, interactionPlugin]}
          initialView={narrow() ? 'listWeek' : 'timeGridWeek'}
          headerToolbar={{ left: 'prev,next today', center: 'title', right: 'dayGridMonth,timeGridWeek,timeGridDay,listWeek' }}
          timeZone="local"
          height="auto"
          nowIndicator
          editable
          eventInteractive
          selectable={writable.length > 0}
          selectMirror
          events={fetchEvents}
          select={(arg: DateSelectArg) => openCreate(arg.start, arg.end, arg.allDay)}
          eventClick={(arg: EventClickArg) => openEvent(arg.event.extendedProps.info as EventInfo)}
          eventDrop={onMove}
          eventResize={onMove}
        />
      </div>
    </section>
  );
}
```

`web/src/styles/calendar.css`:

```css
.kc-layout { display: grid; grid-template-columns: minmax(12rem, 16rem) minmax(0, 1fr); gap: 1rem; align-items: start; }
@media (max-width: 720px) { .kc-layout { grid-template-columns: minmax(0, 1fr); } }
.kc-sidebar ul { list-style: none; padding: 0; margin: 0 0 1rem; display: grid; gap: 0.5rem; }
.kc-swatch { display: inline-block; width: 0.75rem; height: 0.75rem; border-radius: 50%; margin: 0 0.4rem; vertical-align: middle; }
.kc-calendar {
  min-width: 0;
  color: var(--ky-ink);
  --fc-border-color: var(--ky-line);
  --fc-page-bg-color: var(--ky-bg);
  --fc-neutral-bg-color: var(--ky-panel);
  --fc-neutral-text-color: var(--ky-ink-muted);
  --fc-list-event-hover-bg-color: var(--ky-panel-hover);
  --fc-today-bg-color: var(--ky-accent-soft);
  --fc-event-bg-color: var(--ky-accent);
  --fc-event-border-color: var(--ky-accent);
  --fc-event-text-color: var(--ky-button-text);
  --fc-button-bg-color: var(--ky-panel);
  --fc-button-border-color: var(--ky-line-strong);
  --fc-button-text-color: var(--ky-ink);
  --fc-button-hover-bg-color: var(--ky-panel-hover);
  --fc-button-hover-border-color: var(--ky-line-strong);
  --fc-button-active-bg-color: var(--ky-accent);
  --fc-button-active-border-color: var(--ky-accent);
  --fc-now-indicator-color: var(--ky-danger);
}
.kc-calendar .fc .fc-toolbar { flex-wrap: wrap; gap: 0.5rem; }
.kc-calendar .fc .fc-toolbar-title { font-size: 1.1rem; }
```

- [ ] **Step 5: Navigation**

- In `web/src/components/AppHeader.tsx`, add `CalendarDays` to the `lucide-react` import (it is already used by the group calendars item; reuse it) and make the calendar the first item for non-admins:

```tsx
  const navItems = [
    ...(user?.role !== 'admin' ? [{ id: 'calendar', label: 'Calendar', icon: CalendarDays }] : []),
    { id: 'dashboard', label: 'Overview', icon: LayoutDashboard },
    // … the existing items, unchanged …
  ];
```

- In `web/src/App.tsx`, import `{ CalendarPage }` from `./pages/CalendarPage`, render it, and land everyday users on it:

```tsx
  useEffect(() => {
    if (user && user.role !== 'admin') setActiveTab('calendar');
  }, [user?.id]);
  // … inside <main>:
  {activeTab === 'calendar' && user.role !== 'admin' && <CalendarPage />}
```

Read the current `navItems` array and `App.tsx` first, and keep every existing item and tab exactly as it is.
- Check that `web/browser/ui.spec.mjs` still passes. It signs in as the admin, whose navigation is unchanged.

- [ ] **Step 6: Run the tests, build and commit**

Run: `cd web && npm test && npm run build`
Expected: PASS. Commit `web/dist` with the source and restore `web/tsconfig.tsbuildinfo`. Then build the server (`go build -o .browser/server ./cmd/server` at the repo root) and run `cd web && npx playwright install chromium && npm run test:browser`.
Expected: the existing suite passes. If Playwright cannot install its browser here, report that it did not run.

`web/AGENTS.md`: add that `src/pages/CalendarPage.tsx` is the landing page for everyday users and is never shown to admins. Note also:
- the views are month, week, day and list, with week list as the initial view at 720 px or less;
- events come from `GET /api/events` in the browser's zone, for visible calendars only;
- visibility is stored in `localStorage` key `kycalendar.hiddenCalendars`;
- FullCalendar's CSS variables are mapped to `--ky-*` in `src/styles/calendar.css`;
- the sidebar adds, renames, recolours and deletes calendars only where the role allows.

```bash
git add web
git commit -m "feat(web): calendar page with FullCalendar views and a calendar sidebar"
```

---

### Task 5: Event dialog

**Files:**
- Create: `web/src/components/EventDialog.tsx`, `web/src/components/EventDialog.test.tsx`
- Modify: `web/src/pages/CalendarPage.tsx` (`openCreate`, `openEvent`, dialog rendering)
- Rebuild: `web/dist`
- Docs: `web/AGENTS.md`

**Interfaces:**
- Consumes: `FormState`, `emptyForm`, `formFromEvent`, `bodyFromForm`, `validZone`, `WEEKDAYS`, `linkify`, `createEvent`, `updateEvent`, `deleteEvent`, `browserZone`, `ApiError`.
- Produces: `EventDialog` with props `{ calendars: CalendarInfo[]; event?: EventInfo; initial: FormState; onDone(): void; onClose(): void }`. With `event` set it edits, otherwise it creates; it reads only when `event.editable` is false.

- [ ] **Step 1: Write the failing test**

`web/src/components/EventDialog.test.tsx`:

```tsx
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { EventDialog } from './EventDialog';
import { emptyForm, formFromEvent } from '../eventForm';
import type { CalendarInfo, EventInfo } from '../calendarApi';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const cals: CalendarInfo[] = [
  { id: 'cal_p', name: 'Mine', color: '', description: '', kind: 'personal', role: 'owner', dav_path: '' },
  { id: 'cal_r', name: 'News', color: '', description: '', kind: 'group', role: 'reader', dav_path: '' },
];
const recurring: EventInfo = {
  calendar_id: 'cal_p', uid: 'u1', recurrence_id: '20261012T070000Z', etag: 'e1', title: 'Standup',
  description: 'notes https://example.com <img src=x onerror=alert(1)>', start: '2026-10-12T09:00:00+02:00',
  end: '2026-10-12T10:00:00+02:00', series_start: '2026-10-05T09:00:00+02:00', series_end: '2026-10-05T10:00:00+02:00',
  all_day: false, recurring: true, repeat: { freq: 'custom' }, zone: 'Europe/Berlin', editable: true,
};

function capture() {
  const calls: { method: string; url: string; body?: unknown; ifMatch?: string | null }[] = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const headers = new Headers(init?.headers);
    calls.push({ method: init?.method ?? 'GET', url: String(input), body: init?.body ? JSON.parse(String(init.body)) : undefined, ifMatch: headers.get('If-Match') });
    return new Response(JSON.stringify({ etag: 'e2', uid: 'new' }), { status: init?.method === 'POST' ? 201 : 200 });
  });
  return calls;
}

describe('EventDialog', () => {
  it('creates an event in a writable calendar only', async () => {
    const calls = capture();
    const onDone = vi.fn();
    render(<EventDialog calendars={cals} initial={emptyForm(new Date('2026-10-07T09:00:00Z'), new Date('2026-10-07T10:00:00Z'), false, 'cal_p')} onDone={onDone} onClose={vi.fn()} />);
    expect(screen.queryByRole('option', { name: 'News' })).toBeNull();
    fireEvent.change(screen.getByLabelText('Title'), { target: { value: 'Lunch' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(onDone).toHaveBeenCalled());
    expect(calls[0]).toMatchObject({ method: 'POST', url: '/api/calendars/cal_p/events' });
    expect((calls[0].body as { title: string }).title).toBe('Lunch');
  });

  it('edits one occurrence or the series, keeping a custom rule, with If-Match', async () => {
    const calls = capture();
    render(<EventDialog calendars={cals} event={recurring} initial={formFromEvent(recurring, 'this')} onDone={vi.fn()} onClose={vi.fn()} />);
    expect(screen.getByText('Custom (edit on your device)')).toBeTruthy();
    fireEvent.click(screen.getByLabelText('All events'));
    expect((screen.getByLabelText('Start') as HTMLInputElement).value).toContain('T');
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(calls).toHaveLength(1));
    expect(calls[0]).toMatchObject({ method: 'PUT', url: '/api/events/cal_p/u1', ifMatch: '"e1"' });
    expect(calls[0].body).toMatchObject({ scope: 'all', repeat: { freq: 'custom' }, zone: 'Europe/Berlin' });
    expect(new Date((calls[0].body as { start: string }).start).toISOString()).toBe('2026-10-05T07:00:00.000Z');
  });

  it('deletes one occurrence', async () => {
    const calls = capture();
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    render(<EventDialog calendars={cals} event={recurring} initial={formFromEvent(recurring, 'this')} onDone={vi.fn()} onClose={vi.fn()} />);
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(calls).toHaveLength(1));
    expect(calls[0].method).toBe('DELETE');
    expect(calls[0].url).toBe('/api/events/cal_p/u1?scope=this&recurrence_id=20261012T070000Z');
  });

  it('reports a conflict and asks to reload', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({ error: 'changed', code: 'conflict' }), { status: 412 }));
    const onDone = vi.fn();
    render(<EventDialog calendars={cals} event={recurring} initial={formFromEvent(recurring, 'this')} onDone={onDone} onClose={vi.fn()} />);
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect((await screen.findByRole('alert')).textContent).toMatch(/changed elsewhere/i);
    expect(onDone).not.toHaveBeenCalled();
  });

  it('shows a read-only event as text with safe links and no actions', () => {
    const ro = { ...recurring, calendar_id: 'cal_r', editable: false };
    const { container } = render(<EventDialog calendars={cals} event={ro} initial={formFromEvent(ro, 'this')} onDone={vi.fn()} onClose={vi.fn()} />);
    expect(screen.queryByRole('button', { name: 'Save' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Delete' })).toBeNull();
    expect(container.querySelector('img')).toBeNull();
    expect([...container.querySelectorAll('a')].map((a) => a.getAttribute('href'))).toEqual(['https://example.com']);
  });
});
```

- [ ] **Step 2: Run it to see it fail**

Run: `cd web && npx vitest run src/components/EventDialog.test.tsx`
Expected: FAIL (module not found).

- [ ] **Step 3: Write `web/src/components/EventDialog.tsx`**

```tsx
import { useEffect, useRef, useState } from 'react';
import { ApiError, browserZone, createEvent, deleteEvent, updateEvent, type CalendarInfo, type EventInfo, type Freq } from '../calendarApi';
import { WEEKDAYS, bodyFromForm, formFromEvent, validZone, type FormState } from '../eventForm';
import { linkify } from '../linkify';

interface Props {
  calendars: CalendarInfo[];
  event?: EventInfo;
  initial: FormState;
  onDone: () => void;
  onClose: () => void;
}

const PRESETS: { value: Freq; label: string }[] = [
  { value: '', label: 'Does not repeat' },
  { value: 'daily', label: 'Daily' },
  { value: 'weekly', label: 'Weekly' },
  { value: 'monthly', label: 'Monthly' },
  { value: 'yearly', label: 'Yearly' },
];

function message(e: unknown, fallback: string): string {
  if (e instanceof ApiError && e.status === 412) return 'This event changed elsewhere. Close and reopen it to see the latest version.';
  return e instanceof Error && e.message ? e.message : fallback;
}

export function EventDialog({ calendars, event, initial, onDone, onClose }: Props) {
  const [form, setForm] = useState<FormState>(initial);
  const [scope, setScope] = useState<'this' | 'all'>('this');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const first = useRef<HTMLInputElement>(null);
  useEffect(() => first.current?.focus(), []);

  const writable = calendars.filter((c) => c.role !== 'reader');
  const readOnly = event !== undefined && !event.editable;
  const recurring = event?.recurring ?? false;
  const zone = validZone(event?.zone) ? event!.zone! : browserZone();
  const set = (patch: Partial<FormState>) => setForm({ ...form, ...patch });

  function chooseScope(next: 'this' | 'all') {
    setScope(next);
    if (event) setForm(formFromEvent(event, next));
  }

  async function act(fn: () => Promise<unknown>, fallback: string) {
    setError(null);
    setBusy(true);
    try {
      await fn();
      onDone();
    } catch (e) {
      setError(message(e, fallback));
    } finally {
      setBusy(false);
    }
  }

  function save(e: React.FormEvent) {
    e.preventDefault();
    const body = bodyFromForm(form, zone, event ? (recurring ? scope : 'all') : undefined, event?.recurrence_id);
    if (typeof body === 'string') {
      setError(body);
      return;
    }
    void act(() => (event ? updateEvent(event, body) : createEvent(form.calendarId, body)), 'Could not save the event');
  }

  function remove() {
    if (!event) return;
    const s = recurring ? scope : 'all';
    if (!window.confirm(s === 'this' ? 'Delete this occurrence?' : 'Delete this event?')) return;
    void act(() => deleteEvent(event, s), 'Could not delete the event');
  }

  return (
    <div role="dialog" aria-modal="true" aria-labelledby="kc-event-title" className="kc-dialog" onKeyDown={(e) => e.key === 'Escape' && onClose()}>
      <h2 id="kc-event-title">{event ? (readOnly ? form.title || '(no title)' : 'Edit event') : 'New event'}</h2>
      {error && <p role="alert">{error}</p>}
      {readOnly ? (
        <div>
          <p>{event!.all_day ? `${form.start} – ${form.end}` : `${new Date(event!.start).toLocaleString()} – ${new Date(event!.end).toLocaleString()}`}</p>
          {form.location && <p>{linkify(form.location)}</p>}
          {form.description && <p className="kc-description">{linkify(form.description)}</p>}
          <button type="button" onClick={onClose}>Close</button>
        </div>
      ) : (
        <form onSubmit={save}>
          {recurring && (
            <fieldset>
              <legend>Change</legend>
              <label><input type="radio" name="scope" checked={scope === 'this'} onChange={() => chooseScope('this')} /> This event</label>
              <label><input type="radio" name="scope" checked={scope === 'all'} onChange={() => chooseScope('all')} /> All events</label>
            </fieldset>
          )}
          <label htmlFor="kc-title">Title</label>
          <input id="kc-title" ref={first} value={form.title} maxLength={1000} onChange={(e) => set({ title: e.target.value })} />
          <label><input type="checkbox" checked={form.allDay} onChange={(e) => set({ allDay: e.target.checked, start: form.start.slice(0, 10) + (e.target.checked ? '' : 'T09:00'), end: form.end.slice(0, 10) + (e.target.checked ? '' : 'T10:00') })} /> All day</label>
          <label htmlFor="kc-start">Start</label>
          <input id="kc-start" type={form.allDay ? 'date' : 'datetime-local'} value={form.start} required onChange={(e) => set({ start: e.target.value })} />
          <label htmlFor="kc-end">End</label>
          <input id="kc-end" type={form.allDay ? 'date' : 'datetime-local'} value={form.end} required onChange={(e) => set({ end: e.target.value })} />
          <label htmlFor="kc-location">Location</label>
          <input id="kc-location" value={form.location} maxLength={1000} onChange={(e) => set({ location: e.target.value })} />
          <label htmlFor="kc-description">Description</label>
          <textarea id="kc-description" value={form.description} maxLength={65536} onChange={(e) => set({ description: e.target.value })} />
          {!event && (
            <>
              <label htmlFor="kc-calendar">Calendar</label>
              <select id="kc-calendar" value={form.calendarId} onChange={(e) => set({ calendarId: e.target.value })}>
                {writable.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
              </select>
            </>
          )}
          {(!recurring || scope === 'all') && (
            form.freq === 'custom' ? (
              <p>Repeats: <span>Custom (edit on your device)</span></p>
            ) : (
              <>
                <label htmlFor="kc-repeat">Repeat</label>
                <select id="kc-repeat" value={form.freq} onChange={(e) => set({ freq: e.target.value as Freq })}>
                  {PRESETS.map((p) => <option key={p.value} value={p.value}>{p.label}</option>)}
                </select>
                {form.freq === 'weekly' && (
                  <fieldset>
                    <legend>On</legend>
                    {WEEKDAYS.map((d) => (
                      <label key={d}>
                        <input type="checkbox" checked={form.weekdays.includes(d)} onChange={(e) => set({ weekdays: e.target.checked ? [...form.weekdays, d] : form.weekdays.filter((x) => x !== d) })} /> {d}
                      </label>
                    ))}
                  </fieldset>
                )}
              </>
            )
          )}
          {recurring && scope === 'this' && form.freq === 'custom' && <p>Repeats: <span>Custom (edit on your device)</span></p>}
          <button type="submit" disabled={busy}>Save</button>
          {event && <button type="button" disabled={busy} onClick={remove}>Delete</button>}
          <button type="button" onClick={onClose}>Cancel</button>
        </form>
      )}
    </div>
  );
}
```

The "edits one occurrence" test expects the text "Custom (edit on your device)" to be visible while the scope is still "This event". That is the last `{recurring && scope === 'this' && …}` line. Once the user switches to "All events", the same text renders in the repeat block. Exactly one copy is visible at any time.

- [ ] **Step 4: Wire it into `CalendarPage`**

In `CalendarPage.tsx`, add state `const [dialog, setDialog] = useState<{ event?: EventInfo; initial: FormState } | null>(null)`. Then replace the stubs:

```tsx
  const openCreate = (start: Date, end: Date, allDay: boolean) => {
    if (writable.length === 0) return;
    const target = writable.find((c) => !hidden.has(c.id)) ?? writable[0];
    setDialog({ initial: emptyForm(start, end, allDay, target.id) });
  };
  const openEvent = (ev: EventInfo) => setDialog({ event: ev, initial: formFromEvent(ev, 'this') });
```

and render, after the FullCalendar element:

```tsx
        {dialog && (
          <EventDialog
            calendars={calendars}
            event={dialog.event}
            initial={dialog.initial}
            onClose={() => setDialog(null)}
            onDone={() => { setDialog(null); refetch(); }}
          />
        )}
```

Add `.kc-dialog` styles to `calendar.css`: a fixed, centred panel using `var(--ky-panel)`, `var(--ky-line)` and `var(--ky-radius)`, with a `max-width: min(32rem, 100vw - 2rem)` and an overflow scroll, so it fits at 390 px.

- [ ] **Step 5: Run the tests, build and commit**

Run: `cd web && npm test && npm run build`
Expected: PASS. Commit `web/dist` with the source.

`web/AGENTS.md`: add a contract describing `EventDialog`:
- it creates, edits and deletes events, with the scope choice (this or all) for recurring events and "all" times loaded from the series;
- custom rules show "Custom (edit on your device)" and are kept;
- a 412 shows a reload message;
- read-only events render as text through `linkify`, with no actions;
- Escape closes the dialog.

```bash
git add web
git commit -m "feat(web): event dialog for creating, editing and deleting events"
```

---

### Task 6: Drag to move and resize

**Files:**
- Create: `web/src/components/ScopeDialog.tsx`, `web/src/pages/CalendarPage.drag.test.tsx`
- Modify: `web/src/pages/CalendarPage.tsx` (`onMove`)
- Rebuild: `web/dist`

**Interfaces:**
- Consumes: `updateEvent`, `bodyFromForm`, `formFromEvent`, `localDateTime`, `localDate`, `validZone`, `browserZone`.
- Produces: `ScopeDialog` with props `{ onChoose(scope: 'this' | 'all' | null): void }`.

- [ ] **Step 1: Write the failing test**

`web/src/pages/CalendarPage.drag.test.tsx` (reuses the FullCalendar stub pattern from `CalendarPage.test.tsx`):

```tsx
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

const fcProps: Record<string, unknown>[] = [];
vi.mock('@fullcalendar/react', () => ({
  default: (props: Record<string, unknown>) => {
    fcProps.push(props);
    return <div />;
  },
}));

import { CalendarPage } from './CalendarPage';
import type { EventInfo } from '../calendarApi';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  fcProps.length = 0;
});

const single: EventInfo = {
  calendar_id: 'cal_p', uid: 'u1', etag: 'e1', title: 'A', start: '2026-10-07T09:00:00Z', end: '2026-10-07T10:00:00Z',
  series_start: '2026-10-07T09:00:00Z', series_end: '2026-10-07T10:00:00Z', all_day: false, recurring: false,
  repeat: { freq: '' }, zone: 'UTC', editable: true,
};
const series: EventInfo = { ...single, uid: 'u2', recurring: true, recurrence_id: '20261014T090000Z', start: '2026-10-14T09:00:00Z', end: '2026-10-14T10:00:00Z', repeat: { freq: 'weekly' } };

function setup() {
  const puts: { url: string; body: Record<string, unknown> }[] = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const url = String(input);
    if (init?.method === 'PUT') {
      puts.push({ url, body: JSON.parse(String(init.body)) });
      return new Response(JSON.stringify({ etag: 'e2' }), { status: 200 });
    }
    if (url.startsWith('/api/calendars')) return new Response(JSON.stringify([{ id: 'cal_p', name: 'Mine', color: '', description: '', kind: 'personal', role: 'owner', dav_path: '' }]), { status: 200 });
    return new Response('[]', { status: 200 });
  });
  return puts;
}

function drag(ev: EventInfo, shiftHours: number) {
  const revert = vi.fn();
  const start = new Date(ev.start);
  const end = new Date(ev.end);
  const move = (d: Date) => new Date(d.getTime() + shiftHours * 3600_000);
  const arg = {
    event: { start: move(start), end: move(end), allDay: false, extendedProps: { info: ev } },
    oldEvent: { start, end, allDay: false },
    revert,
  };
  (fcProps.at(-1)!.eventDrop as (a: unknown) => void)(arg);
  return revert;
}

describe('drag', () => {
  it('moves a single event with If-Match and scope all', async () => {
    const puts = setup();
    render(<CalendarPage />);
    await waitFor(() => expect(fcProps.length).toBeGreaterThan(0));
    drag(single, 2);
    await waitFor(() => expect(puts).toHaveLength(1));
    expect(puts[0].url).toBe('/api/events/cal_p/u1');
    expect(new Date(puts[0].body.start as string).toISOString()).toBe('2026-10-07T11:00:00.000Z');
  });

  it('asks for a recurring event and shifts the whole series by the drag delta', async () => {
    const puts = setup();
    render(<CalendarPage />);
    await waitFor(() => expect(fcProps.length).toBeGreaterThan(0));
    drag(series, 1);
    fireEvent.click(await screen.findByRole('button', { name: 'All events' }));
    await waitFor(() => expect(puts).toHaveLength(1));
    expect(puts[0].body).toMatchObject({ scope: 'all', repeat: { freq: 'custom' } });
    expect(new Date(puts[0].body.start as string).toISOString()).toBe('2026-10-07T10:00:00.000Z');
  });

  it('reverts when the user cancels', async () => {
    const puts = setup();
    render(<CalendarPage />);
    await waitFor(() => expect(fcProps.length).toBeGreaterThan(0));
    const revert = drag(series, 1);
    fireEvent.click(await screen.findByRole('button', { name: 'Cancel' }));
    expect(revert).toHaveBeenCalled();
    expect(puts).toHaveLength(0);
  });
});
```

- [ ] **Step 2: Run it to see it fail**

Run: `cd web && npx vitest run src/pages/CalendarPage.drag.test.tsx`
Expected: FAIL (drops revert without saving; no scope dialog).

- [ ] **Step 3: Implement**

`web/src/components/ScopeDialog.tsx`:

```tsx
export function ScopeDialog({ onChoose }: { onChoose: (scope: 'this' | 'all' | null) => void }) {
  return (
    <div role="dialog" aria-modal="true" aria-labelledby="kc-scope-title" className="kc-dialog" onKeyDown={(e) => e.key === 'Escape' && onChoose(null)}>
      <h2 id="kc-scope-title">Change a repeating event</h2>
      <button type="button" autoFocus onClick={() => onChoose('this')}>This event</button>
      <button type="button" onClick={() => onChoose('all')}>All events</button>
      <button type="button" onClick={() => onChoose(null)}>Cancel</button>
    </div>
  );
}
```

In `CalendarPage.tsx`, replace `onMove` with the following, and render `{pending && <ScopeDialog onChoose={(s) => { const p = pending; setPending(null); if (s) void saveMove(p, s); else p.revert(); }} />}`. Keep `pending` in state: `useState<MoveArg | null>(null)`.

```tsx
  type MoveArg = EventDropArg | EventResizeDoneArg;

  async function saveMove(arg: MoveArg, scope: 'this' | 'all') {
    const ev = arg.event.extendedProps.info as EventInfo;
    const zone = validZone(ev.zone) ? ev.zone! : browserZone();
    const oldStart = arg.oldEvent.start!;
    const oldEnd = arg.oldEvent.end ?? oldStart;
    const newStart = arg.event.start!;
    const newEnd = arg.event.end ?? newStart;
    const base = formFromEvent(ev, scope);
    let start: Date;
    let end: Date;
    if (scope === 'all') {
      start = new Date(parseLocal(base.start).getTime() + (newStart.getTime() - oldStart.getTime()));
      end = new Date(parseLocal(base.end).getTime() + (newEnd.getTime() - oldEnd.getTime()));
    } else {
      start = newStart;
      end = newEnd;
    }
    const allDay = arg.event.allDay;
    const form = {
      ...base,
      allDay,
      freq: ev.recurring && scope === 'all' ? ('custom' as const) : base.freq,
      start: allDay ? localDate(start) : localDateTime(start),
      end: allDay ? localDate(new Date(end.getFullYear(), end.getMonth(), end.getDate() - 1)) : localDateTime(end),
    };
    const body = bodyFromForm(form, zone, ev.recurring ? scope : 'all', ev.recurrence_id);
    if (typeof body === 'string') {
      arg.revert();
      setError(body);
      return;
    }
    try {
      await updateEvent(ev, body);
      refetch();
    } catch (e) {
      arg.revert();
      setError(e instanceof ApiError && e.status === 412 ? 'This event changed elsewhere; the calendar has been reloaded.' : (e as Error).message || 'Could not move the event');
      refetch();
    }
  }

  const onMove = (arg: MoveArg) => {
    const ev = arg.event.extendedProps.info as EventInfo;
    if (ev.recurring) setPending(arg);
    else void saveMove(arg, 'all');
  };
```

Notes for the implementer:
- In the all-day branch, `end` is FullCalendar's exclusive end. The form wants the inclusive date, hence the minus one day. `bodyFromForm` adds it back.
- For `scope === 'all'`, `formFromEvent(ev, 'all')` gives the series start and end in the form's local format. `parseLocal` turns those back into Dates before the drag delta is added.
- Import `ApiError`, `updateEvent`, `formFromEvent`, `bodyFromForm`, `parseLocal`, `localDate`, `localDateTime`, `validZone` and `browserZone`.

- [ ] **Step 4: Run the tests, build and commit**

Run: `cd web && npm test && npm run build`
Expected: PASS. Commit `web/dist` with the source.

`web/AGENTS.md`: add a contract describing drag and resize:
- dragging or resizing saves through `updateEvent` with `If-Match`;
- a recurring occurrence asks "This event / All events / Cancel";
- "All events" shifts the series start and end by the drag delta and keeps the stored rule;
- a cancel or failure reverts, and a 412 reloads with a message;
- readers and partial events cannot be dragged (`editable` false).

```bash
git add web
git commit -m "feat(web): drag to move and resize events, with a scope choice for repeats"
```

---

### Task 7: Browser regression and docs

**Files:**
- Modify: `web/browser/server.mjs` (create the everyday user before the server starts), `web/browser/setup.mjs` (sign in and replace that user's password)
- Create: `web/browser/calendar.spec.mjs`
- Docs: `web/browser/AGENTS.md`, `web/AGENTS.md`, root `AGENTS.md` (Plan 3b contracts)

**Interfaces:**
- Consumes: `create-user` (Task 1); the calendar page (Tasks 4-6).

- [ ] **Step 1: Seed an everyday user**

In `web/browser/server.mjs`, before `spawn(...)`, run the same binary once with the same `cwd` and `env`:

```js
import { spawnSync } from 'node:child_process';
// … inside, after env is built and before the server starts:
const created = spawnSync(binary, ['create-user', '-username', 'walter'], { cwd: dir, env, input: 'WalterInitial123!\n', stdio: ['pipe', 'inherit', 'inherit'] });
if (created.status !== 0) { await rm(dir, { recursive: true, force: true }); process.exit(created.status ?? 1); }
```

Refactor the existing literal `env` object and binary path into `const env = {...}` and `const binary = fileURLToPath(...)`, used by both calls.

In `web/browser/setup.mjs`, after the admin password change, do the same for `walter`: log in with `WalterInitial123!`, read `ky_csrf` from that context's cookies, and change the password to `WalterUpdated456!`. Use a new `request.newContext` for walter so the admin and walter cookies stay separate.

- [ ] **Step 2: Write `web/browser/calendar.spec.mjs`**

```js
import { test, expect } from '@playwright/test';

test('calendar: CSP, create by form, list view by keyboard, delete', async ({ page }, testInfo) => {
  const violations = [];
  page.on('console', (m) => { if (/Content Security Policy|violates.*directive/i.test(m.text())) violations.push(m.text()); });
  const title = `Planning ${testInfo.project.name} ${Date.now()}`;

  await page.goto('/');
  await page.getByPlaceholder('admin', { exact: true }).fill('walter');
  await page.locator('input[type=password]').fill('WalterUpdated456!');
  await page.getByRole('button', { name: 'Sign In', exact: true }).click();

  // Everyday users land on the calendar; FullCalendar renders under the production CSP.
  await expect(page.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name: 'Calendar' })).toHaveAttribute('aria-current', 'page');
  await expect(page.locator('.fc')).toBeVisible();

  await page.getByRole('button', { name: 'New event' }).click();
  await page.getByLabel('Title').fill(title);
  await page.getByRole('button', { name: 'Save' }).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);

  // The list view and the event form are the keyboard path.
  await page.getByRole('button', { name: /list/i }).click();
  const item = page.locator('.fc-list-event', { hasText: title });
  await expect(item).toBeVisible();
  await item.locator('a, [tabindex]').first().focus();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('dialog')).toBeVisible();
  await expect(page.getByLabel('Title')).toHaveValue(title);

  page.once('dialog', (d) => d.accept());
  await page.getByRole('button', { name: 'Delete' }).click();
  await expect(page.locator('.fc-list-event', { hasText: title })).toHaveCount(0);

  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  expect(violations).toEqual([]);
});
```

The event is created for the next whole hour, today. The week list shows the current week, so the event is visible unless the next hour falls in next week, which happens only on Sunday at 23:xx. In that case click `next` once before looking. Implement that check with `new Date()` in the test.

- [ ] **Step 3: Run the browser suite**

Run: `go build -o .browser/server ./cmd/server` at the repo root, then `cd web && npm run build && npx playwright install chromium && npm run test:browser`.
Expected: all projects pass (light and dark, 390 and 1280), including the existing `ui.spec.mjs`. If a selector does not match FullCalendar's rendered markup, inspect the trace (`test-results/`) and adjust the selector, never the behavior being asserted. Do not relax CSP or mock anything.

- [ ] **Step 4: Docs, full CI and commit**

- `web/browser/AGENTS.md`: the harness seeds an everyday user `walter` with `create-user` before the server starts; `setup.mjs` replaces both bootstrap passwords; `calendar.spec.mjs` covers the calendar under production CSP (create by form, list view by keyboard, delete, no horizontal scroll at 390 px).
- Root `AGENTS.md`: add `#### Plan 3b web calendar contracts` after the Plan 3 section. It should state:
  - the calendar is the landing page for everyday users and is never shown to admins;
  - it uses FullCalendar 6.1.21 across all packages;
  - event text renders as text through `linkify`;
  - "all occurrences" edits use `series_start`/`series_end`;
  - drags of recurring events ask for the scope;
  - the browser suite covers the calendar under the production CSP.
- Run `KY_SMOKE_PORT=28931 make ci`. Port 18080 is held by an unrelated local service.

```bash
git add web AGENTS.md
git commit -m "test(web): browser regression for the calendar under production CSP"
```

# KyCalendar Plan 3: Recurrence and Event API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Server-side recurrence expansion and a JSON calendar and event API in which every web edit goes through the same write path as CalDAV.

**Architecture:** Pure functions in `internal/calendar` do three jobs. They resolve time zones (IANA names, the CLDR Windows map, Mozilla paths). They expand events over `rrule-go`, using KyCalendar's own RDATE and EXDATE parsing and RECURRENCE-ID overrides. They apply web edits to the stored iCalendar object while keeping unknown properties. `davbackend.Write` becomes the one write path, called by the CalDAV PUT and by the JSON handlers. The handlers authorize every request with `access.Resolve`, exactly as CalDAV does.

**Tech Stack:** Go on the `ky_server_base` scaffold, `github.com/emersion/go-ical` for parsing and encoding only, `github.com/teambition/rrule-go` v1.8.2 for rule iteration, CLDR `windowsZones.xml` (release-48-2), SQLite and PostgreSQL 17.

**Spec:** `docs/superpowers/specs/2026-10-06-kycalendar-v1a-design.md`, section 3, plus the testing rows of section 4. The spec's Plan 3 was "recurrence spike, JSON API, FullCalendar UI". This plan covers the spike, the recurrence engine and the JSON API. **Plan 3b** (a separate document) covers the FullCalendar UI on top of this API. Plan 4 stays KyRecovery backup, restore and the interop gate.

## Spike result (done 2026-10-07; this plan commits to it)

The spec marked "go-ical's recurrence helper over rrule-go" as unproven. Two throwaway tests against the pinned modules settled it:

- **`ical.Component.RecurrenceSet` cannot be used.**
  - It never reads RDATE: its second loop iterates EXDATE again.
  - A comma-separated `EXDATE:a,b` list is a hard error.
  - Any TZID that `time.LoadLocation` rejects is a hard error. That includes Outlook's `Eastern Standard Time` and old Thunderbird's `/mozilla.org/20050126_1/America/New_York`.
- **`rrule-go` sets are correct where we need them.**
  - A zoned rule keeps its wall-clock time across a DST change (09:00 EDT, then 09:00 EST).
  - UNTIL given in UTC on a zoned rule is honoured.
  - An EXDATE given in UTC removes a zone-local occurrence.
  - EXDATE removes RDATE instances too.
  - A DTSTART added as an RDATE is de-duplicated when the rule also yields it.
  - A DTSTART that does not match the rule is skipped unless it is added as an RDATE. RFC 5545 counts DTSTART as the first instance, so Expand always adds it.
- **Decision:** go-ical is used only to decode and encode. Expansion is `calendar.Expand` over `rrule-go`, with KyCalendar's own value-list parsing and zone resolution. `internal/calendar/object.go` already notes go-ical's RDATE gap for indexing.

The fixture corpus in Task 2 is hand-written in each client's export style. Recorded real exports from Apple, Google, Outlook and Thunderbird are Plan 4's interop gate.

## Global Constraints

- One write path: a web edit is a pure function over the stored iCalendar object that preserves unknown properties. Its output goes through the CalDAV PUT path: same validation, ETag check and change log.
- `GET /api/calendars`: visible calendars with role, colour and kind.
- `POST /api/calendars`, `PATCH /api/calendars/{id}`, `DELETE /api/calendars/{id}`: personal calendars. Managers may rename and recolour group calendars.
- `GET /api/events?start&end&calendar=…`: expanded instances. Range ≤ 400 days, at most 5000 instances per response.
- `POST /api/calendars/{id}/events`; `PUT` (with `If-Match`) and `DELETE /api/events/{cal}/{uid}`, with scope `this` or `all` for recurring events. 412 on conflict.
- Recurrence: server-side expansion of RRULE, RDATE, EXDATE and RECURRENCE-ID overrides.
- Time zones: TZIDs resolve against Go's embedded IANA data plus the CLDR Windows→IANA map. An unknown TZID is stored, shown in UTC and flagged. Floating times render in the viewer's local zone.
- Repeat presets: none, daily, weekly with weekdays, monthly, yearly. Rules outside the presets are reported as `custom` and preserved.
- Event text is untrusted. The API returns it as JSON strings; rendering rules are Plan 3b's.
- Logs carry IDs only: never passwords, tokens or event content. Audit: admin actions, app password create and revoke, auth failures.
- Admin identities cannot read events (Plan 2). Event routes use `requireEveryday`; roles come only from `access.Resolve`. A caller who cannot read a calendar gets 404; one who can read but lacks the role gets 403.
- Every new route gets a row in `internal/api/authz_matrix_test.go` in the same task that registers it. `TestEveryRouteIsInTheMatrix` fails otherwise.
- `make ci` passes; Postgres 17 runs in CI.

Decisions this plan makes that the spec leaves open:

- **Timed events written by the web** carry `TZID=<IANA zone>` plus a generated VTIMEZONE covering eleven years from the start's year, so clients without that zone's data still read the right instants. Events anchored to UTC use `Z` values and no VTIMEZONE.
- **"All occurrences" edits:**
  - Changing only title, location or description keeps every override and EXDATE. Overrides keep their own text.
  - Changing the start or the repeat preset removes overrides, EXDATE and RDATE, which refer to the old instants.
- **Recurrence IDs in the API** are the occurrence's original start: `YYYYMMDD` for all-day events, the wall-clock `YYYYMMDDTHHMMSS` for floating events, and UTC `YYYYMMDDTHHMMSSZ` for everything else.
- **All-day instances** are returned as dates (`2026-10-07`). Timed instances are RFC 3339 times in the viewer's zone. The viewer's zone is the `tz` query parameter (IANA), default UTC.
- **`If-Match` is required** on event `PUT` and `DELETE`: 428 without it.
- **Too many instances:** 422 `too_many_instances` when a response would exceed 5000. A range over 400 days, or a malformed range, is 400.
- **Personal calendar deletion** runs detached and tracked, like the group calendar delete. There is no step-up: the spec gives owners deletion, and the UI confirms first.
- **Indexing** (`calendar.Inspect`) still treats a Windows-named or Mozilla-path TZID as an unknown zone and widens its bounds. That is conservative and correct; tightening it is out of scope.

## Review Focus

1. **An event created on the web in Europe/Berlin and opened by a viewer in New York** shows at the same instant, with a VTIMEZONE present. Pinned in Task 3 (`TestNewEventRoundTripsAcrossZones`).
2. **Editing one occurrence of an Apple-made recurring event** keeps its `X-` properties, its VALARM and its ATTENDEE. Pinned in Task 3 (`TestEditOnePreservesUnknownProperties`).
3. **A phone changes an event while the web form is open.** The web save gets 412 and the stored bytes are unchanged. Pinned in Task 7 (`TestEventPutStaleETagIs412`).
4. **An override moved into the visible range from outside it** appears, and one moved out disappears. Pinned in Task 2 (fixture `override_moved.ics`).
5. **Deleting one occurrence of an all-day recurring event, for a viewer far from UTC (Pacific/Auckland),** hides exactly that date. Pinned in Task 3 (`TestDeleteOneAllDayInAuckland`).

---

## File Structure

| Path | Responsibility | Task |
|---|---|---|
| `internal/calendar/zone.go` (new) | `ResolveZone`: IANA, Windows (CLDR), Mozilla path | 1 |
| `internal/calendar/gen/windowszones/main.go` (new) | Regenerates the Windows table from a pinned, hash-checked CLDR file | 1 |
| `internal/calendar/windowszones_gen.go` (generated, committed) | `windowsZones` map | 1 |
| `internal/calendar/expand.go` (new) | `Instance`, `Expand`, `ErrTooManyInstances` | 2 |
| `internal/calendar/testdata/recurrence/*.ics` (new) | Client-style fixture corpus | 2 |
| `internal/calendar/edit.go` (new) | `EventInput`, `Repeat`, `RepeatOf`, `NewEvent`, `EditAll`, `EditOne`, `DeleteOne`, `Master` | 3 |
| `internal/calendar/vtimezone.go` (new) | `VTimezone` | 3 |
| `internal/davbackend/write.go` (new) | `Write`, `ErrInvalidResource`, `EnsureDefault` | 4 |
| `internal/store/calendars.go`, `store.go` | `GetObjectByUID` | 4 |
| `internal/api/calendars.go` (new) | `GET/POST /api/calendars`, `PATCH/DELETE /api/calendars/{id}` | 5, 6 |
| `internal/api/events.go` (new) | `GET /api/events`, event create/update/delete | 5, 7 |
| `internal/api/authz_matrix_test.go` | Rows for every new route | 5, 6, 7 |
| `AGENTS.md`, child `AGENTS.md` files | Plan 3 contracts | each task, 8 |

---

### Task 1: Time zone resolution

**Files:**
- Create: `internal/calendar/zone.go`, `internal/calendar/zone_test.go`, `internal/calendar/gen/windowszones/main.go`
- Generate and commit: `internal/calendar/windowszones_gen.go`
- Docs: `internal/calendar/AGENTS.md`

**Interfaces:**
- Consumes: nothing.
- Produces: `calendar.ResolveZone(tzid string) (*time.Location, bool)`; the unexported `windowsZones map[string]string`.

- [ ] **Step 1: Write the failing test**

`internal/calendar/zone_test.go`:

```go
package calendar

import "testing"

func TestResolveZone(t *testing.T) {
	cases := []struct {
		tzid, want string
		ok         bool
	}{
		{"Europe/Berlin", "Europe/Berlin", true},
		{"Eastern Standard Time", "America/New_York", true},
		{"W. Europe Standard Time", "Europe/Berlin", true},
		{"Tokyo Standard Time", "Asia/Tokyo", true},
		{"/mozilla.org/20050126_1/America/New_York", "America/New_York", true},
		{"Custom/Nowhere", "", false},
		{"", "", false},
		{"Local", "", false},
		{"../../etc/passwd", "", false},
	}
	for _, tc := range cases {
		loc, ok := ResolveZone(tc.tzid)
		if ok != tc.ok || (ok && loc.String() != tc.want) {
			t.Errorf("ResolveZone(%q) = %v, %v; want %q, %v", tc.tzid, loc, ok, tc.want, tc.ok)
		}
	}
}

// Every IANA zone the CLDR table names must load from Go's data, or the mapping is useless.
func TestWindowsZonesAllLoad(t *testing.T) {
	if len(windowsZones) < 130 {
		t.Fatalf("windows table has %d entries; regenerate it", len(windowsZones))
	}
	for win, iana := range windowsZones {
		if loc, ok := ResolveZone(win); !ok || loc.String() != iana {
			t.Errorf("%q -> %q does not load", win, iana)
		}
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/calendar/ -run 'ResolveZone|WindowsZones'`
Expected: FAIL to compile (`ResolveZone`, `windowsZones` undefined).

- [ ] **Step 3: Write the generator**

`internal/calendar/gen/windowszones/main.go`:

```go
// Command windowszones regenerates windowszones_gen.go from a pinned CLDR release. The file's
// SHA-256 is pinned too: a changed upstream file must be reviewed before it is trusted.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"flag"
	"fmt"
	"go/format"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	release = "release-48-2"
	source  = "https://raw.githubusercontent.com/unicode-org/cldr/" + release + "/common/supplemental/windowsZones.xml"
	digest  = "9cf3db6a31fb382fee21b70be6feba1e82766b0fcd06e6261fb7936a73e537ff"
)

type supplemental struct {
	Zones []struct {
		Other     string `xml:"other,attr"`
		Territory string `xml:"territory,attr"`
		Type      string `xml:"type,attr"`
	} `xml:"windowsZones>mapTimezones>mapZone"`
}

func main() {
	out := flag.String("out", "windowszones_gen.go", "output file")
	flag.Parse()
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(source)
	if err != nil {
		fail(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fail(fmt.Errorf("GET %s: %s", source, resp.Status))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		fail(err)
	}
	if sum := sha256.Sum256(body); hex.EncodeToString(sum[:]) != digest {
		fail(fmt.Errorf("%s changed upstream: review it, then update digest", source))
	}
	var doc supplemental
	if err := xml.Unmarshal(body, &doc); err != nil {
		fail(err)
	}
	zones := map[string]string{}
	for _, z := range doc.Zones {
		if z.Territory == "001" { // CLDR's default zone for each Windows name
			zones[z.Other] = z.Type
		}
	}
	names := make([]string, 0, len(zones))
	for name := range zones {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	fmt.Fprintf(&b, "// Code generated by gen/windowszones from CLDR %s; DO NOT EDIT.\n\npackage calendar\n\n", release)
	b.WriteString("// windowsZones maps a Windows zone name, as Outlook writes in TZID, to CLDR's default IANA zone.\nvar windowsZones = map[string]string{\n")
	for _, name := range names {
		fmt.Fprintf(&b, "\t%q: %q,\n", name, zones[name])
	}
	b.WriteString("}\n")
	src, err := format.Source([]byte(b.String()))
	if err != nil {
		fail(err)
	}
	if err := os.WriteFile(*out, src, 0o644); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "windowszones:", err)
	os.Exit(1)
}
```

- [ ] **Step 4: Write `internal/calendar/zone.go` and generate the table**

```go
package calendar

import (
	"strings"
	"time"
)

//go:generate go run ./gen/windowszones -out windowszones_gen.go

// ResolveZone maps an iCalendar TZID to a location. It accepts an IANA name, a Windows zone name
// (Outlook) through CLDR's default mapping, or an old Thunderbird "/mozilla.org/<version>/<IANA>"
// path. ok is false when none matches; callers show such times in UTC and flag them.
func ResolveZone(tzid string) (*time.Location, bool) {
	if tzid == "" || tzid == "Local" {
		return nil, false
	}
	if loc, err := time.LoadLocation(tzid); err == nil {
		return loc, true
	}
	if iana, ok := windowsZones[tzid]; ok {
		if loc, err := time.LoadLocation(iana); err == nil {
			return loc, true
		}
	}
	if rest, ok := strings.CutPrefix(tzid, "/mozilla.org/"); ok {
		if _, name, ok := strings.Cut(rest, "/"); ok {
			if loc, err := time.LoadLocation(name); err == nil {
				return loc, true
			}
		}
	}
	return nil, false
}
```

Run: `cd internal/calendar && go generate ./... && cd ../.. && head -5 internal/calendar/windowszones_gen.go`
Expected: the generated header, and a map with about 139 entries. The generator needs network access to raw.githubusercontent.com. If it fails on the digest, stop and report it: do not change the digest without reviewing the file.

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/calendar/ -run 'ResolveZone|WindowsZones'`
Expected: PASS. If an IANA name from CLDR does not load, report the name. Do not drop it from the table by hand.

- [ ] **Step 6: DOX**

In `internal/calendar/AGENTS.md` Local Contracts, add:

```markdown
- `ResolveZone(tzid)` is the only TZID lookup for expansion and edits: IANA via Go's data (the binary embeds `time/tzdata`), then Windows names through `windowsZones` (generated from CLDR release-48-2, territory 001, by `go generate`; the generator pins the source file's SHA-256), then old Thunderbird `/mozilla.org/<version>/<IANA>` paths. `Local` and the empty TZID never resolve. Unresolved zones are shown in UTC and flagged by callers.
```

- [ ] **Step 7: Commit**

```bash
git add internal/calendar/zone.go internal/calendar/zone_test.go internal/calendar/gen internal/calendar/windowszones_gen.go internal/calendar/AGENTS.md
git commit -m "feat(calendar): resolve IANA, Windows and Mozilla TZIDs"
```

---

### Task 2: Recurrence expansion

**Files:**
- Create: `internal/calendar/expand.go`, `internal/calendar/expand_test.go`, `internal/calendar/testdata/recurrence/{apple_weekly_override,google_until_exdate,outlook_windows_tz,thunderbird_allday,thunderbird_mozilla_tz,lists,floating,unknown_tz,override_moved,minutely,unmatched_dtstart}.ics`
- Docs: `internal/calendar/AGENTS.md`

**Interfaces:**
- Consumes: `ResolveZone` (Task 1); existing helpers in `object.go`: `parseWall(s string, loc *time.Location) (time.Time, error)`, `isDate(p *ical.Prop) bool`, `durationOf(comp) (time.Duration, error)`, `fastFreq(comp) bool`, `wideTimeSet(opt *rrule.ROption) bool`, `maxIndexOccurrences`.
- Produces:

```go
type Instance struct {
	UID          string
	RecurrenceID string    // original start as a key (see Global decisions); "" when not recurring
	Start, End   time.Time // absolute instants; an all-day instance spans viewer-local midnights
	AllDay       bool
	Floating     bool // wall-clock time with no zone, shown in the viewer's zone
	UnknownZone  bool // TZID unresolved: parsed as UTC
	Recurring    bool
	Override     bool // a RECURRENCE-ID component replaced the generated occurrence
	Partial      bool // the rule is not expanded safely; only the first occurrence is shown
	Event        *ical.Component // the VEVENT describing this instance
}
var ErrTooManyInstances = errors.New("calendar: too many instances in range")
func Expand(cal *ical.Calendar, from, to time.Time, viewer *time.Location, limit int) ([]Instance, error)
func occurrenceKey(t time.Time, allDay, floating bool) string // shared with Task 3
```

- [ ] **Step 1: Write the fixtures**

Create each file under `internal/calendar/testdata/recurrence/` with CRLF line endings (iCalendar requires them). Write them with LF in your editor, then convert: `sed -i 's/$/\r/' internal/calendar/testdata/recurrence/*.ics`.

`apple_weekly_override.ics`:
```
BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Apple Inc.//macOS 26.0//EN
BEGIN:VTIMEZONE
TZID:Europe/Berlin
BEGIN:DAYLIGHT
TZOFFSETFROM:+0100
TZOFFSETTO:+0200
DTSTART:19810329T020000
RRULE:FREQ=YEARLY;BYMONTH=3;BYDAY=-1SU
TZNAME:CEST
END:DAYLIGHT
BEGIN:STANDARD
TZOFFSETFROM:+0200
TZOFFSETTO:+0100
DTSTART:19961027T030000
RRULE:FREQ=YEARLY;BYMONTH=10;BYDAY=-1SU
TZNAME:CET
END:STANDARD
END:VTIMEZONE
BEGIN:VEVENT
UID:apple-1
DTSTAMP:20261001T080000Z
DTSTART;TZID=Europe/Berlin:20261005T090000
DTEND;TZID=Europe/Berlin:20261005T100000
RRULE:FREQ=WEEKLY;BYDAY=MO
EXDATE;TZID=Europe/Berlin:20261019T090000
SUMMARY:Standup
X-APPLE-TRAVEL-ADVISORY-BEHAVIOR:AUTOMATIC
ATTENDEE;CN=Bob;PARTSTAT=ACCEPTED:mailto:bob@example.com
BEGIN:VALARM
ACTION:DISPLAY
TRIGGER:-PT10M
DESCRIPTION:Reminder
END:VALARM
END:VEVENT
BEGIN:VEVENT
UID:apple-1
DTSTAMP:20261001T080000Z
RECURRENCE-ID;TZID=Europe/Berlin:20261012T090000
DTSTART;TZID=Europe/Berlin:20261013T100000
DTEND;TZID=Europe/Berlin:20261013T110000
SUMMARY:Standup (moved)
END:VEVENT
END:VCALENDAR
```

`google_until_exdate.ics`:
```
BEGIN:VCALENDAR
PRODID:-//Google Inc//Google Calendar 70.9054//EN
VERSION:2.0
BEGIN:VEVENT
DTSTART;TZID=America/Los_Angeles:20261005T170000
DTEND;TZID=America/Los_Angeles:20261005T173000
RRULE:FREQ=DAILY;UNTIL=20261009T065959Z
EXDATE;TZID=America/Los_Angeles:20261006T170000
EXDATE;TZID=America/Los_Angeles:20261007T170000
DTSTAMP:20261001T000000Z
UID:google-1@google.com
SUMMARY:Gym
END:VEVENT
END:VCALENDAR
```

`outlook_windows_tz.ics`:
```
BEGIN:VCALENDAR
PRODID:-//Microsoft Corporation//Outlook 16.0 MIMEDIR//EN
VERSION:2.0
BEGIN:VTIMEZONE
TZID:Eastern Standard Time
BEGIN:STANDARD
DTSTART:16011104T020000
RRULE:FREQ=YEARLY;BYDAY=1SU;BYMONTH=11
TZOFFSETFROM:-0400
TZOFFSETTO:-0500
END:STANDARD
BEGIN:DAYLIGHT
DTSTART:16010311T020000
RRULE:FREQ=YEARLY;BYDAY=2SU;BYMONTH=3
TZOFFSETFROM:-0500
TZOFFSETTO:-0400
END:DAYLIGHT
END:VTIMEZONE
BEGIN:VEVENT
UID:040000008200E00074C5B7101A82E008
DTSTAMP:20261001T000000Z
DTSTART;TZID=Eastern Standard Time:20261026T090000
DTEND;TZID=Eastern Standard Time:20261026T093000
RRULE:FREQ=WEEKLY;COUNT=3;BYDAY=MO
SUMMARY:Review
X-MICROSOFT-CDO-BUSYSTATUS:BUSY
END:VEVENT
END:VCALENDAR
```

`thunderbird_allday.ics`:
```
BEGIN:VCALENDAR
PRODID:-//Mozilla.org/NONSGML Mozilla Calendar V1.1//EN
VERSION:2.0
BEGIN:VEVENT
UID:tb-allday
DTSTAMP:20261001T000000Z
DTSTART;VALUE=DATE:20261007
DTEND;VALUE=DATE:20261008
RRULE:FREQ=YEARLY
SUMMARY:Birthday
END:VEVENT
END:VCALENDAR
```

`thunderbird_mozilla_tz.ics`:
```
BEGIN:VCALENDAR
PRODID:-//Mozilla.org/NONSGML Mozilla Calendar V1.1//EN
VERSION:2.0
BEGIN:VEVENT
UID:tb-mozilla
DTSTAMP:20261001T000000Z
DTSTART;TZID=/mozilla.org/20050126_1/America/New_York:20261007T090000
DTEND;TZID=/mozilla.org/20050126_1/America/New_York:20261007T100000
SUMMARY:Old Thunderbird
END:VEVENT
END:VCALENDAR
```

`lists.ics`:
```
BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//t//EN
BEGIN:VEVENT
UID:lists
DTSTAMP:20261001T000000Z
DTSTART:20261005T090000Z
DTEND:20261005T093000Z
RRULE:FREQ=DAILY;COUNT=4
EXDATE:20261006T090000Z,20261007T090000Z
RDATE:20261020T090000Z,20261021T090000Z
SUMMARY:Lists
END:VEVENT
END:VCALENDAR
```

`floating.ics`:
```
BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//t//EN
BEGIN:VEVENT
UID:floating
DTSTAMP:20261001T000000Z
DTSTART:20261007T090000
DTEND:20261007T100000
SUMMARY:Floating
END:VEVENT
END:VCALENDAR
```

`unknown_tz.ics`:
```
BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//t//EN
BEGIN:VEVENT
UID:unknown
DTSTAMP:20261001T000000Z
DTSTART;TZID=Custom/Nowhere:20261007T090000
DTEND;TZID=Custom/Nowhere:20261007T100000
SUMMARY:Unknown zone
END:VEVENT
END:VCALENDAR
```

`override_moved.ics`:
```
BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//t//EN
BEGIN:VEVENT
UID:moved
DTSTAMP:20261001T000000Z
DTSTART:20260928T090000Z
DTEND:20260928T100000Z
RRULE:FREQ=WEEKLY
SUMMARY:Weekly
END:VEVENT
BEGIN:VEVENT
UID:moved
DTSTAMP:20261001T000000Z
RECURRENCE-ID:20260928T090000Z
DTSTART:20261002T090000Z
DTEND:20261002T100000Z
SUMMARY:Moved in
END:VEVENT
BEGIN:VEVENT
UID:moved
DTSTAMP:20261001T000000Z
RECURRENCE-ID:20261012T090000Z
DTSTART:20261020T090000Z
DTEND:20261020T100000Z
SUMMARY:Moved out
END:VEVENT
END:VCALENDAR
```

`minutely.ics`:
```
BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//t//EN
BEGIN:VEVENT
UID:minutely
DTSTAMP:20261001T000000Z
DTSTART:20261007T090000Z
DTEND:20261007T090100Z
RRULE:FREQ=MINUTELY
SUMMARY:Too fine
END:VEVENT
END:VCALENDAR
```

`unmatched_dtstart.ics`:
```
BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//t//EN
BEGIN:VEVENT
UID:unmatched
DTSTAMP:20261001T000000Z
DTSTART;TZID=America/New_York:20261006T090000
DTEND;TZID=America/New_York:20261006T100000
RRULE:FREQ=WEEKLY;BYDAY=MO;COUNT=2
SUMMARY:Tuesday start, Monday rule
END:VEVENT
END:VCALENDAR
```

- [ ] **Step 2: Write the failing test**

`internal/calendar/expand_test.go`:

```go
package calendar

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
)

func fixture(t *testing.T, name string) *ical.Calendar {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "recurrence", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cal, err := ical.NewDecoder(f).Decode()
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return cal
}

func zone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

// describe renders an instance as "<start> [flags]": a viewer-local date for all-day instances,
// otherwise the UTC instant.
func describe(in Instance) string {
	s := in.Start.UTC().Format(time.RFC3339)
	if in.AllDay {
		s = in.Start.Format("2006-01-02")
	}
	for flag, on := range map[string]bool{"override": in.Override, "floating": in.Floating, "unknown": in.UnknownZone, "partial": in.Partial} {
		if on {
			s += " " + flag
		}
	}
	return s
}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func TestExpandCorpus(t *testing.T) {
	cases := []struct {
		file     string
		from, to time.Time
		viewer   string
		want     []string
	}{
		{"apple_weekly_override.ics", day(2026, 10, 1), day(2026, 11, 3), "UTC",
			[]string{"2026-10-05T07:00:00Z", "2026-10-13T08:00:00Z override", "2026-10-26T08:00:00Z", "2026-11-02T08:00:00Z"}},
		{"google_until_exdate.ics", day(2026, 10, 1), day(2026, 10, 31), "UTC",
			[]string{"2026-10-06T00:00:00Z", "2026-10-09T00:00:00Z"}},
		{"outlook_windows_tz.ics", day(2026, 10, 1), day(2026, 12, 1), "UTC",
			[]string{"2026-10-26T13:00:00Z", "2026-11-02T14:00:00Z", "2026-11-09T14:00:00Z"}},
		{"thunderbird_allday.ics", day(2026, 1, 1), day(2028, 12, 31), "Pacific/Auckland",
			[]string{"2026-10-07", "2027-10-07", "2028-10-07"}},
		{"thunderbird_mozilla_tz.ics", day(2026, 10, 1), day(2026, 10, 31), "UTC",
			[]string{"2026-10-07T13:00:00Z"}},
		{"lists.ics", day(2026, 10, 1), day(2026, 10, 31), "UTC",
			[]string{"2026-10-05T09:00:00Z", "2026-10-08T09:00:00Z", "2026-10-20T09:00:00Z", "2026-10-21T09:00:00Z"}},
		{"floating.ics", day(2026, 10, 1), day(2026, 10, 31), "Asia/Tokyo",
			[]string{"2026-10-07T00:00:00Z floating"}},
		{"floating.ics", day(2026, 10, 1), day(2026, 10, 31), "UTC",
			[]string{"2026-10-07T09:00:00Z floating"}},
		{"unknown_tz.ics", day(2026, 10, 1), day(2026, 10, 31), "Europe/Berlin",
			[]string{"2026-10-07T09:00:00Z unknown"}},
		{"override_moved.ics", day(2026, 10, 1), day(2026, 10, 15), "UTC",
			[]string{"2026-10-02T09:00:00Z override", "2026-10-05T09:00:00Z"}},
		{"minutely.ics", day(2026, 10, 1), day(2026, 10, 31), "UTC",
			[]string{"2026-10-07T09:00:00Z partial"}},
		{"unmatched_dtstart.ics", day(2026, 10, 1), day(2026, 10, 31), "UTC",
			[]string{"2026-10-06T13:00:00Z", "2026-10-12T13:00:00Z", "2026-10-19T13:00:00Z"}},
	}
	for _, tc := range cases {
		t.Run(tc.file+"/"+tc.viewer, func(t *testing.T) {
			got, err := Expand(fixture(t, tc.file), tc.from, tc.to, zone(t, tc.viewer), 5000)
			if err != nil {
				t.Fatal(err)
			}
			var desc []string
			for _, in := range got {
				desc = append(desc, describe(in))
			}
			if !reflect.DeepEqual(desc, tc.want) {
				t.Fatalf("got  %v\nwant %v", desc, tc.want)
			}
		})
	}
}

func TestExpandRecurrenceIDsAndEvents(t *testing.T) {
	got, err := Expand(fixture(t, "apple_weekly_override.ics"), day(2026, 10, 1), day(2026, 10, 20), time.UTC, 5000)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].RecurrenceID != "20261005T070000Z" || got[1].RecurrenceID != "20261012T070000Z" {
		t.Fatalf("recurrence ids: %+v", got)
	}
	if s, _ := got[1].Event.Props.Text(ical.PropSummary); s != "Standup (moved)" {
		t.Fatalf("override instance must carry the override's properties, got %q", s)
	}
	allDay, _ := Expand(fixture(t, "thunderbird_allday.ics"), day(2026, 1, 1), day(2026, 12, 31), time.UTC, 5000)
	if allDay[0].RecurrenceID != "20261007" || !allDay[0].End.Equal(allDay[0].Start.AddDate(0, 0, 1)) {
		t.Fatalf("all-day instance: %+v", allDay[0])
	}
	floating, _ := Expand(fixture(t, "floating.ics"), day(2026, 1, 1), day(2026, 12, 31), time.UTC, 5000)
	if floating[0].RecurrenceID != "" || floating[0].Recurring {
		t.Fatalf("non-recurring instance: %+v", floating[0])
	}
}

func TestExpandLimit(t *testing.T) {
	cal := fixture(t, "override_moved.ics") // weekly, no end
	if _, err := Expand(cal, day(2026, 1, 1), day(2027, 1, 1), time.UTC, 5); !errors.Is(err, ErrTooManyInstances) {
		t.Fatalf("want ErrTooManyInstances, got %v", err)
	}
}

// An all-day event spans local midnights even across a DST change.
func TestExpandAllDayAcrossDST(t *testing.T) {
	src := strings.ReplaceAll(`BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//t//EN
BEGIN:VEVENT
UID:dst-allday
DTSTAMP:20261001T000000Z
DTSTART;VALUE=DATE:20261031
DTEND;VALUE=DATE:20261102
RRULE:FREQ=WEEKLY;COUNT=2
SUMMARY:Weekend
END:VEVENT
END:VCALENDAR
`, "\n", "\r\n")
	cal, err := ical.NewDecoder(strings.NewReader(src)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	ny := zone(t, "America/New_York")
	got, err := Expand(cal, day(2026, 10, 1), day(2026, 12, 1), ny, 5000)
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
	for _, in := range got {
		if in.End.In(ny).Hour() != 0 || in.End.In(ny).Sub(in.Start.In(ny)) < 47*time.Hour {
			t.Fatalf("all-day instance does not end at local midnight two days later: %v -> %v", in.Start, in.End)
		}
	}
}
```

- [ ] **Step 3: Run it to see it fail**

Run: `go test ./internal/calendar/ -run Expand`
Expected: FAIL to compile (`Expand`, `Instance`, `ErrTooManyInstances` undefined).

- [ ] **Step 4: Write `internal/calendar/expand.go`**

```go
package calendar

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/teambition/rrule-go"
)

// Instance is one occurrence of an event as a viewer sees it.
type Instance struct {
	UID          string
	RecurrenceID string    // the occurrence's original start as a key; "" when not recurring
	Start, End   time.Time // absolute; an all-day instance spans viewer-local midnights
	AllDay       bool
	Floating     bool // wall-clock time with no zone, shown in the viewer's zone
	UnknownZone  bool // TZID unresolved: parsed as UTC
	Recurring    bool
	Override     bool // a RECURRENCE-ID component replaced the generated occurrence
	Partial      bool // the rule is not expanded safely; only the first occurrence is shown
	Event        *ical.Component
}

var ErrTooManyInstances = errors.New("calendar: too many instances in range")

// span is a VEVENT's start and length, resolved for one viewer.
type span struct {
	start                     time.Time
	dur                       time.Duration
	days                      int // all-day length in calendar days
	allDay, floating, unknown bool
}

// zoneOf resolves a date-time property: UTC for a Z value, the viewer's zone for dates and
// floating times, the TZID's zone, or UTC with unknown=true for an unresolvable TZID.
func zoneOf(p *ical.Prop, viewer *time.Location) (loc *time.Location, floating, unknown bool) {
	switch {
	case strings.HasSuffix(p.Value, "Z"):
		return time.UTC, false, false
	case isDate(p):
		return viewer, false, false
	}
	tzid := p.Params.Get(ical.ParamTimezoneID)
	if tzid == "" {
		return viewer, true, false
	}
	if loc, ok := ResolveZone(tzid); ok {
		return loc, false, false
	}
	return time.UTC, false, true
}

func spanOf(comp *ical.Component, viewer *time.Location) (span, error) {
	sp := comp.Props.Get(ical.PropDateTimeStart)
	if sp == nil {
		return span{}, fmt.Errorf("%w: DTSTART", ErrInvalidData)
	}
	loc, floating, unknown := zoneOf(sp, viewer)
	start, err := parseWall(sp.Value, loc)
	if err != nil {
		return span{}, fmt.Errorf("%w: DTSTART", ErrInvalidData)
	}
	s := span{start: start, allDay: isDate(sp), floating: floating, unknown: unknown}
	if ep := comp.Props.Get(ical.PropDateTimeEnd); ep != nil {
		eloc, _, _ := zoneOf(ep, viewer)
		if end, err := parseWall(ep.Value, eloc); err == nil && !end.Before(start) {
			s.dur = end.Sub(start)
		}
	} else if d, err := durationOf(comp); err == nil && d > 0 {
		s.dur = d
	}
	if s.allDay {
		s.days = max(1, int(math.Round(s.dur.Hours()/24)))
	}
	return s, nil
}

func (s span) endAt(occ time.Time) time.Time {
	if s.allDay {
		return occ.AddDate(0, 0, s.days)
	}
	return occ.Add(s.dur)
}

// occurrenceKey names an occurrence by its original start: a date for all-day events, the
// wall-clock time for floating events, else the UTC instant.
func occurrenceKey(t time.Time, allDay, floating bool) string {
	switch {
	case allDay:
		return t.Format("20060102")
	case floating:
		return t.Format("20060102T150405")
	}
	return t.UTC().Format("20060102T150405Z")
}

// dateList reads an EXDATE or RDATE value list; a PERIOD value contributes its start.
func dateList(p *ical.Prop, viewer *time.Location) ([]time.Time, error) {
	loc, _, _ := zoneOf(p, viewer)
	var out []time.Time
	for _, v := range strings.Split(p.Value, ",") {
		v, _, _ = strings.Cut(strings.TrimSpace(v), "/")
		t, err := parseWall(v, loc)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// ruleSet builds a master's recurrence set: nil for a non-recurring event, partial=true for a
// rule Inspect also refuses to expand (several RRULEs, SECONDLY/MINUTELY, an oversized time set,
// an unparseable rule or RDATE). DTSTART is always an instance (RFC 5545), so it is added as an
// RDATE; the set de-duplicates it when the rule yields it too.
func ruleSet(comp *ical.Component, s span, viewer *time.Location) (set *rrule.Set, partial bool) {
	rules, rdates := comp.Props[ical.PropRecurrenceRule], comp.Props[ical.PropRecurrenceDates]
	if len(rules) == 0 && len(rdates) == 0 {
		return nil, false
	}
	if len(rules) > 1 || fastFreq(comp) {
		return nil, true
	}
	set = &rrule.Set{}
	set.DTStart(s.start)
	set.RDate(s.start)
	if len(rules) == 1 {
		opt, err := comp.Props.RecurrenceRule()
		if err != nil || opt == nil || wideTimeSet(opt) {
			return nil, true
		}
		opt.Dtstart = s.start
		r, err := rrule.NewRRule(*opt)
		if err != nil {
			return nil, true
		}
		set.RRule(r)
	}
	for _, p := range rdates {
		ts, err := dateList(&p, viewer)
		if err != nil {
			return nil, true
		}
		for _, t := range ts {
			set.RDate(t)
		}
	}
	for _, p := range comp.Props[ical.PropExceptionDates] {
		ts, err := dateList(&p, viewer)
		if err != nil {
			continue // an unreadable EXDATE only fails to hide an occurrence
		}
		for _, t := range ts {
			set.ExDate(t)
		}
	}
	return set, false
}

func overlaps(start, end, from, to time.Time) bool {
	if end.Equal(start) {
		return !start.Before(from) && start.Before(to)
	}
	return start.Before(to) && end.After(from)
}

// Expand returns cal's instances overlapping [from, to), sorted by start. More than limit
// instances is ErrTooManyInstances. Each recurring master spends at most maxIndexOccurrences
// rule iterations, so a rule that yields nothing in range still stops.
func Expand(cal *ical.Calendar, from, to time.Time, viewer *time.Location, limit int) ([]Instance, error) {
	var masters []*ical.Component
	overrides := map[string]*ical.Component{}
	for _, c := range cal.Children {
		if c.Name != ical.CompEvent {
			continue
		}
		rid := c.Props.Get(ical.PropRecurrenceID)
		if rid == nil {
			masters = append(masters, c)
			continue
		}
		loc, floating, _ := zoneOf(rid, viewer)
		t, err := parseWall(rid.Value, loc)
		if err != nil {
			continue
		}
		overrides[occurrenceKey(t, isDate(rid), floating)] = c
	}

	var out []Instance
	add := func(in Instance) error {
		if len(out) >= limit {
			return ErrTooManyInstances
		}
		out = append(out, in)
		return nil
	}
	for _, m := range masters {
		s, err := spanOf(m, viewer)
		if err != nil {
			return nil, err
		}
		uid, _ := m.Props.Text(ical.PropUID)
		base := Instance{UID: uid, AllDay: s.allDay, Floating: s.floating, UnknownZone: s.unknown, Event: m}
		set, partial := ruleSet(m, s, viewer)
		if set == nil {
			in := base
			in.Start, in.End = s.start, s.endAt(s.start)
			if partial {
				in.Recurring, in.Partial = true, true
				in.RecurrenceID = occurrenceKey(s.start, s.allDay, s.floating)
			}
			if overlaps(in.Start, in.End, from, to) {
				if err := add(in); err != nil {
					return nil, err
				}
			}
			continue
		}
		base.Recurring = true
		next := set.Iterator()
		for budget := maxIndexOccurrences; budget > 0; budget-- {
			occ, ok := next()
			if !ok || !occ.Before(to) {
				break
			}
			end := s.endAt(occ)
			if !overlaps(occ, end, from, to) {
				continue
			}
			key := occurrenceKey(occ, s.allDay, s.floating)
			if _, moved := overrides[key]; moved {
				continue
			}
			in := base
			in.RecurrenceID, in.Start, in.End = key, occ, end
			if err := add(in); err != nil {
				return nil, err
			}
		}
	}
	for key, o := range overrides {
		s, err := spanOf(o, viewer)
		if err != nil {
			return nil, err
		}
		uid, _ := o.Props.Text(ical.PropUID)
		in := Instance{UID: uid, RecurrenceID: key, Start: s.start, End: s.endAt(s.start), AllDay: s.allDay,
			Floating: s.floating, UnknownZone: s.unknown, Recurring: true, Override: true, Event: o}
		if overlaps(in.Start, in.End, from, to) {
			if err := add(in); err != nil {
				return nil, err
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Start.Equal(out[j].Start) {
			return out[i].Start.Before(out[j].Start)
		}
		return out[i].RecurrenceID < out[j].RecurrenceID
	})
	return out, nil
}
```

`ical.Props` is `map[string][]Prop`, so `range comp.Props[...]` yields values. That is why `dateList(&p, viewer)` takes the address of the loop variable, which Go 1.22+ gives each iteration its own.

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/calendar/`
Expected: PASS, including every existing Inspect test. If a corpus case fails, recheck the fixture's expected instants by hand from the zone rules before changing anything:
- Berlin DST ends 2026-10-25.
- New York DST ends 2026-11-01.
- Los Angeles is UTC−7 in October.

Change code, not expectations, unless the expectation is provably wrong.

- [ ] **Step 6: DOX**

In `internal/calendar/AGENTS.md` Local Contracts, add:

```markdown
- `Expand(cal, from, to, viewer, limit)` is the only recurrence expansion. go-ical is used to decode and encode only: its `RecurrenceSet` drops RDATE, fails on comma-separated EXDATE lists and on non-IANA TZIDs (2026-10-07 spike). Expansion iterates an `rrule-go` set that always includes DTSTART (RFC 5545), adds every RDATE and EXDATE value (lists and PERIOD starts), and replaces occurrences by RECURRENCE-ID overrides keyed by `occurrenceKey` (a date for all-day, wall-clock for floating, else the UTC instant). Overrides are placed by their own DTSTART, so one moved into the range appears and one moved out disappears. Floating times and dates use the viewer's zone; unresolved TZIDs parse as UTC and set `UnknownZone`. Rules Inspect refuses to expand (several RRULEs, SECONDLY/MINUTELY, oversized time sets, unparseable rule or RDATE) yield only DTSTART with `Partial`. Each master spends at most 100000 iterations; more than `limit` instances is `ErrTooManyInstances`.
- `testdata/recurrence/` holds hand-written fixtures in Apple, Google, Outlook and Thunderbird export styles (CRLF). Recorded real exports are Plan 4's interop gate.
```

- [ ] **Step 7: Commit**

```bash
git add internal/calendar
git commit -m "feat(calendar): expand recurring events over rrule-go"
```

---

### Task 3: Web edits as pure functions

**Files:**
- Create: `internal/calendar/edit.go`, `internal/calendar/edit_test.go`, `internal/calendar/vtimezone.go`, `internal/calendar/vtimezone_test.go`
- Docs: `internal/calendar/AGENTS.md`

**Interfaces:**
- Consumes: `Expand`, `occurrenceKey`, `ResolveZone`, `parseWall`, `isDate` (Tasks 1-2 and `object.go`).
- Produces:

```go
type Repeat struct {
	Freq     string         // "", "daily", "weekly", "monthly", "yearly", "custom"
	Weekdays []time.Weekday // weekly only; empty means the start's weekday
}
type EventInput struct {
	Title, Location, Description string
	Start, End                   time.Time      // timed: instants; all-day: dates at 00:00 UTC, End exclusive
	AllDay                       bool
	Zone                         *time.Location // timed events: the zone repeats keep wall-clock time in
	Repeat                       Repeat         // Freq "custom" keeps the stored rule (edits only)
}
var (
	ErrNotRecurring     = errors.New("calendar: event does not repeat")
	ErrNoSuchOccurrence = errors.New("calendar: no such occurrence")
)
func Master(cal *ical.Calendar) (*ical.Component, error)
func RepeatOf(comp *ical.Component) Repeat
func NewEvent(uid string, in EventInput, now time.Time) *ical.Calendar
func EditAll(cal *ical.Calendar, in EventInput, now time.Time) error
func EditOne(cal *ical.Calendar, recurrenceID string, in EventInput, now time.Time) error
func DeleteOne(cal *ical.Calendar, recurrenceID string, now time.Time) error
func VTimezone(loc *time.Location, from, to time.Time) *ical.Component
const ProductID = "-//Busnes.app//KyCalendar//EN"
```

- [ ] **Step 1: Write the failing tests**

`internal/calendar/vtimezone_test.go`:

```go
package calendar

import (
	"testing"
	"time"

	"github.com/emersion/go-ical"
)

func TestVTimezoneTransitions(t *testing.T) {
	ny := zone(t, "America/New_York")
	tz := VTimezone(ny, day(2026, 1, 1), day(2027, 1, 1))
	if id, _ := tz.Props.Text(ical.PropTimezoneID); id != "America/New_York" {
		t.Fatalf("TZID %q", id)
	}
	if len(tz.Children) != 2 {
		t.Fatalf("want 2 observances in 2026, got %d", len(tz.Children))
	}
	d := tz.Children[0]
	start := d.Props.Get(ical.PropDateTimeStart).Value
	from, _ := d.Props.Text(ical.PropTimezoneOffsetFrom)
	to, _ := d.Props.Text(ical.PropTimezoneOffsetTo)
	if v := d.Props.Get(ical.PropTimezoneOffsetFrom).Params.Get(ical.ParamValue); v != "" {
		t.Fatalf("TZOFFSETFROM must not carry VALUE=%s", v)
	}
	if d.Name != ical.CompTimezoneDaylight || start != "20260308T020000" || from != "-0500" || to != "-0400" {
		t.Fatalf("daylight onset: %s %s %s %s", d.Name, start, from, to)
	}
	if s := tz.Children[1].Props.Get(ical.PropDateTimeStart).Value; tz.Children[1].Name != ical.CompTimezoneStandard || s != "20261101T020000" {
		t.Fatalf("standard onset: %s %s", tz.Children[1].Name, s)
	}
	tokyo := VTimezone(zone(t, "Asia/Tokyo"), day(2026, 1, 1), day(2037, 1, 1))
	if len(tokyo.Children) != 1 || tokyo.Children[0].Name != ical.CompTimezoneStandard {
		t.Fatalf("a zone without changes needs one STANDARD observance: %+v", tokyo.Children)
	}
	if off, _ := tokyo.Children[0].Props.Text(ical.PropTimezoneOffsetTo); off != "+0900" {
		t.Fatalf("Tokyo offset %q", off)
	}
	_ = time.UTC
}
```

`internal/calendar/edit_test.go`:

```go
package calendar

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
)

var now = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

// roundTrip encodes and decodes, as the store and a client would.
func roundTrip(t *testing.T, cal *ical.Calendar) (*ical.Calendar, string) {
	t.Helper()
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		t.Fatal(err)
	}
	out, err := ical.NewDecoder(bytes.NewReader(buf.Bytes())).Decode()
	if err != nil {
		t.Fatal(err)
	}
	return out, buf.String()
}

func TestNewEventRoundTripsAcrossZones(t *testing.T) {
	berlin := zone(t, "Europe/Berlin")
	in := EventInput{Title: "Planning", Start: time.Date(2026, 10, 7, 9, 0, 0, 0, berlin), End: time.Date(2026, 10, 7, 10, 0, 0, 0, berlin), Zone: berlin}
	cal, raw := roundTrip(t, NewEvent("uid-1", in, now))
	if !strings.Contains(raw, "BEGIN:VTIMEZONE") || !strings.Contains(raw, "DTSTART;TZID=Europe/Berlin:20261007T090000") {
		t.Fatalf("missing zone data:\n%s", raw)
	}
	if _, err := Inspect(cal); err != nil {
		t.Fatalf("a new event must pass Inspect: %v", err)
	}
	got, err := Expand(cal, day(2026, 10, 1), day(2026, 10, 31), zone(t, "America/New_York"), 10)
	if err != nil || len(got) != 1 || !got[0].Start.Equal(in.Start) {
		t.Fatalf("expanded %v %v; want %v", got, err, in.Start)
	}
}

func TestNewEventUTCAndAllDay(t *testing.T) {
	_, raw := roundTrip(t, NewEvent("u", EventInput{Start: day(2026, 10, 7).Add(9 * time.Hour), End: day(2026, 10, 7).Add(10 * time.Hour), Zone: time.UTC}, now))
	if strings.Contains(raw, "VTIMEZONE") || !strings.Contains(raw, "DTSTART:20261007T090000Z") {
		t.Fatalf("UTC event:\n%s", raw)
	}
	_, raw = roundTrip(t, NewEvent("a", EventInput{Start: day(2026, 10, 7), End: day(2026, 10, 8), AllDay: true, Repeat: Repeat{Freq: "yearly"}}, now))
	for _, want := range []string{"DTSTART;VALUE=DATE:20261007", "DTEND;VALUE=DATE:20261008", "RRULE:FREQ=YEARLY", "PRODID:" + ProductID} {
		if !strings.Contains(raw, want) {
			t.Fatalf("all-day event lacks %q:\n%s", want, raw)
		}
	}
}

func TestRepeatOfAndPresets(t *testing.T) {
	cases := map[string]Repeat{
		"":                                {},
		"FREQ=DAILY":                      {Freq: "daily"},
		"FREQ=WEEKLY":                     {Freq: "weekly"},
		"FREQ=WEEKLY;BYDAY=MO,WE":         {Freq: "weekly", Weekdays: []time.Weekday{time.Monday, time.Wednesday}},
		"FREQ=MONTHLY":                    {Freq: "monthly"},
		"FREQ=YEARLY;INTERVAL=1":          {Freq: "yearly"},
		"FREQ=WEEKLY;COUNT=3":             {Freq: "custom"},
		"FREQ=MONTHLY;BYDAY=-1FR":         {Freq: "custom"},
		"FREQ=DAILY;INTERVAL=2":           {Freq: "custom"},
		"FREQ=WEEKLY;UNTIL=20261231T000000Z": {Freq: "custom"},
	}
	for rule, want := range cases {
		comp := ical.NewEvent().Component
		if rule != "" {
			comp.Props.Set(&ical.Prop{Name: ical.PropRecurrenceRule, Params: ical.Params{}, Value: rule})
		}
		got := RepeatOf(comp)
		if got.Freq != want.Freq || len(got.Weekdays) != len(want.Weekdays) {
			t.Errorf("RepeatOf(%q) = %+v, want %+v", rule, got, want)
		}
	}
}

func TestEditOnePreservesUnknownProperties(t *testing.T) {
	cal := fixture(t, "apple_weekly_override.ics")
	in := EventInput{Title: "Standup (this week)", Start: time.Date(2026, 10, 26, 8, 30, 0, 0, time.UTC), End: time.Date(2026, 10, 26, 9, 30, 0, 0, time.UTC), Zone: zone(t, "Europe/Berlin"), Repeat: Repeat{Freq: "custom"}}
	if err := EditOne(cal, "20261026T080000Z", in, now); err != nil {
		t.Fatal(err)
	}
	out, raw := roundTrip(t, cal)
	for _, keep := range []string{"X-APPLE-TRAVEL-ADVISORY-BEHAVIOR:AUTOMATIC", "ATTENDEE;CN=Bob", "BEGIN:VALARM", "RRULE:FREQ=WEEKLY;BYDAY=MO", "Standup (moved)"} {
		if !strings.Contains(raw, keep) {
			t.Fatalf("edit lost %q:\n%s", keep, raw)
		}
	}
	if !strings.Contains(raw, "RECURRENCE-ID;TZID=Europe/Berlin:20261026T090000") {
		t.Fatalf("override must name the occurrence in the master's zone:\n%s", raw)
	}
	got, _ := Expand(out, day(2026, 10, 25), day(2026, 10, 28), time.UTC, 10)
	if len(got) != 1 || !got[0].Override || got[0].Start.Format(time.RFC3339) != "2026-10-26T08:30:00Z" {
		t.Fatalf("edited occurrence: %+v", got)
	}
	if s, _ := got[0].Event.Props.Text(ical.PropSummary); s != "Standup (this week)" {
		t.Fatalf("summary %q", s)
	}
	if got[0].Event.Props.Get(ical.PropRecurrenceRule) != nil {
		t.Fatal("an override must not carry the master's RRULE")
	}
}

func TestEditAllTextKeepsOverrides(t *testing.T) {
	cal := fixture(t, "apple_weekly_override.ics")
	berlin := zone(t, "Europe/Berlin")
	in := EventInput{Title: "Daily standup", Start: time.Date(2026, 10, 5, 9, 0, 0, 0, berlin), End: time.Date(2026, 10, 5, 10, 0, 0, 0, berlin), Zone: berlin, Repeat: Repeat{Freq: "custom"}}
	if err := EditAll(cal, in, now); err != nil {
		t.Fatal(err)
	}
	_, raw := roundTrip(t, cal)
	if strings.Contains(raw, "VALUE=TEXT") {
		t.Fatalf("an edit wrote VALUE=TEXT:\n%s", raw)
	}
	for _, keep := range []string{"SUMMARY:Daily standup", "Standup (moved)", "EXDATE", "BEGIN:VALARM", "SEQUENCE:1"} {
		if !strings.Contains(raw, keep) {
			t.Fatalf("text-only edit of all lost %q:\n%s", keep, raw)
		}
	}
}

func TestEditAllTimeChangeDropsExceptions(t *testing.T) {
	cal := fixture(t, "apple_weekly_override.ics")
	berlin := zone(t, "Europe/Berlin")
	in := EventInput{Title: "Standup", Start: time.Date(2026, 10, 5, 10, 0, 0, 0, berlin), End: time.Date(2026, 10, 5, 11, 0, 0, 0, berlin), Zone: berlin, Repeat: Repeat{Freq: "weekly", Weekdays: []time.Weekday{time.Monday}}}
	if err := EditAll(cal, in, now); err != nil {
		t.Fatal(err)
	}
	_, raw := roundTrip(t, cal)
	if strings.Contains(raw, "Standup (moved)") || strings.Contains(raw, "EXDATE") || strings.Contains(raw, "RECURRENCE-ID") {
		t.Fatalf("exceptions referring to old instants survived:\n%s", raw)
	}
	if !strings.Contains(raw, "BEGIN:VALARM") || !strings.Contains(raw, "X-APPLE-TRAVEL-ADVISORY-BEHAVIOR") {
		t.Fatalf("unknown properties lost:\n%s", raw)
	}
}

func TestDeleteOneAllDayInAuckland(t *testing.T) {
	cal := fixture(t, "thunderbird_allday.ics")
	if err := DeleteOne(cal, "20271007", now); err != nil {
		t.Fatal(err)
	}
	out, raw := roundTrip(t, cal)
	if !strings.Contains(raw, "EXDATE;VALUE=DATE:20271007") {
		t.Fatalf("all-day EXDATE must be a DATE:\n%s", raw)
	}
	got, _ := Expand(out, day(2026, 1, 1), day(2028, 12, 31), zone(t, "Pacific/Auckland"), 10)
	var dates []string
	for _, in := range got {
		dates = append(dates, in.Start.Format("2006-01-02"))
	}
	if strings.Join(dates, ",") != "2026-10-07,2028-10-07" {
		t.Fatalf("dates after deleting 2027: %v", dates)
	}
}

func TestDeleteOneRemovesOverride(t *testing.T) {
	cal := fixture(t, "apple_weekly_override.ics")
	if err := DeleteOne(cal, "20261012T070000Z", now); err != nil {
		t.Fatal(err)
	}
	out, raw := roundTrip(t, cal)
	if strings.Contains(raw, "Standup (moved)") {
		t.Fatalf("deleting a moved occurrence must remove its override:\n%s", raw)
	}
	got, _ := Expand(out, day(2026, 10, 10), day(2026, 10, 15), time.UTC, 10)
	if len(got) != 0 {
		t.Fatalf("occurrence still shows: %+v", got)
	}
}

func TestEditErrors(t *testing.T) {
	single := fixture(t, "floating.ics")
	if err := EditOne(single, "20261007T090000", EventInput{}, now); !errors.Is(err, ErrNotRecurring) {
		t.Fatalf("EditOne on a single event: %v", err)
	}
	if err := DeleteOne(fixture(t, "lists.ics"), "garbage", now); !errors.Is(err, ErrNoSuchOccurrence) {
		t.Fatalf("bad recurrence id: %v", err)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/calendar/ -run 'VTimezone|NewEvent|RepeatOf|EditOne|EditAll|DeleteOne|EditErrors'`
Expected: FAIL to compile.

- [ ] **Step 3: Write `internal/calendar/vtimezone.go`**

```go
package calendar

import (
	"fmt"
	"time"

	"github.com/emersion/go-ical"
)

// VTimezone describes loc for clients without its IANA data: one observance per offset change
// in [from, to), or a single STANDARD observance for a zone that does not change there.
func VTimezone(loc *time.Location, from, to time.Time) *ical.Component {
	tz := ical.NewComponent(ical.CompTimezone)
	tz.Props.SetText(ical.PropTimezoneID, loc.String())
	for t := from.In(loc); t.Before(to); {
		_, end := t.ZoneBounds()
		if end.IsZero() || !end.Before(to) {
			break
		}
		tz.Children = append(tz.Children, observance(end, end.Add(-time.Second)))
		t = end
	}
	if len(tz.Children) == 0 {
		at := from.In(loc)
		tz.Children = append(tz.Children, observance(at, at))
	}
	return tz
}

// observance is the onset at 'at', from the offset in force at 'before'. Its DTSTART is local
// time in the previous offset, as RFC 5545 requires.
func observance(at, before time.Time) *ical.Component {
	name := ical.CompTimezoneStandard
	if at.IsDST() {
		name = ical.CompTimezoneDaylight
	}
	c := ical.NewComponent(name)
	_, fromOff := before.Zone()
	abbr, toOff := at.Zone()
	start := ical.NewProp(ical.PropDateTimeStart)
	start.Value = at.In(time.FixedZone("", fromOff)).Format("20060102T150405")
	c.Props.Set(start)
	setRaw(c, ical.PropTimezoneOffsetFrom, utcOffset(fromOff))
	setRaw(c, ical.PropTimezoneOffsetTo, utcOffset(toOff))
	c.Props.SetText(ical.PropTimezoneName, abbr)
	return c
}

// setRaw sets a property's value verbatim. Props.SetText marks non-TEXT properties such as
// SEQUENCE or TZOFFSETFROM with VALUE=TEXT, which clients reject.
func setRaw(comp *ical.Component, name, value string) {
	comp.Props.Set(&ical.Prop{Name: name, Params: ical.Params{}, Value: value})
}

func utcOffset(sec int) string {
	sign := "+"
	if sec < 0 {
		sign, sec = "-", -sec
	}
	return fmt.Sprintf("%s%02d%02d", sign, sec/3600, sec%3600/60)
}
```

- [ ] **Step 4: Write `internal/calendar/edit.go`**

```go
package calendar

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"
)

// ProductID names KyCalendar in objects it creates.
const ProductID = "-//Busnes.app//KyCalendar//EN"

var (
	ErrNotRecurring     = errors.New("calendar: event does not repeat")
	ErrNoSuchOccurrence = errors.New("calendar: no such occurrence")
)

// Repeat is a web repeat preset. Freq "custom" is any rule outside the presets; edits with it
// keep the stored rule.
type Repeat struct {
	Freq     string
	Weekdays []time.Weekday
}

// EventInput is one web edit, validated at the API boundary.
type EventInput struct {
	Title, Location, Description string
	Start, End                   time.Time
	AllDay                       bool
	Zone                         *time.Location
	Repeat                       Repeat
}

// vtimezoneYears is how far ahead a generated VTIMEZONE lists offset changes.
const vtimezoneYears = 11

var weekdayCodes = []string{"SU", "MO", "TU", "WE", "TH", "FR", "SA"}

// Master is the object's VEVENT without RECURRENCE-ID.
func Master(cal *ical.Calendar) (*ical.Component, error) {
	for _, c := range cal.Children {
		if c.Name == ical.CompEvent && c.Props.Get(ical.PropRecurrenceID) == nil {
			return c, nil
		}
	}
	return nil, ErrInvalidData
}

// RepeatOf classifies a component's recurrence as a preset or "custom".
func RepeatOf(comp *ical.Component) Repeat {
	rules := comp.Props[ical.PropRecurrenceRule]
	switch {
	case len(rules) == 0 && len(comp.Props[ical.PropRecurrenceDates]) == 0:
		return Repeat{}
	case len(rules) != 1 || len(comp.Props[ical.PropRecurrenceDates]) > 0:
		return Repeat{Freq: "custom"}
	}
	var r Repeat
	for _, part := range strings.Split(strings.ToUpper(rules[0].Value), ";") {
		key, val, _ := strings.Cut(part, "=")
		switch key {
		case "FREQ":
			r.Freq = strings.ToLower(val)
		case "INTERVAL":
			if val != "1" {
				return Repeat{Freq: "custom"}
			}
		case "BYDAY":
			for _, code := range strings.Split(val, ",") {
				i := slices.Index(weekdayCodes, code)
				if i < 0 {
					return Repeat{Freq: "custom"} // an ordinal such as -1FR
				}
				r.Weekdays = append(r.Weekdays, time.Weekday(i))
			}
		case "WKST":
		default:
			return Repeat{Freq: "custom"}
		}
	}
	switch {
	case r.Freq != "daily" && r.Freq != "weekly" && r.Freq != "monthly" && r.Freq != "yearly":
		return Repeat{Freq: "custom"}
	case len(r.Weekdays) > 0 && r.Freq != "weekly":
		return Repeat{Freq: "custom"}
	}
	return r
}

func (r Repeat) equal(o Repeat) bool {
	return r.Freq == o.Freq && slices.Equal(r.Weekdays, o.Weekdays)
}

func setRepeat(comp *ical.Component, r Repeat) {
	if r.Freq == "custom" {
		return
	}
	comp.Props.Del(ical.PropRecurrenceRule)
	if r.Freq == "" {
		return
	}
	rule := "FREQ=" + strings.ToUpper(r.Freq)
	if r.Freq == "weekly" && len(r.Weekdays) > 0 {
		codes := make([]string, len(r.Weekdays))
		for i, d := range r.Weekdays {
			codes[i] = weekdayCodes[d]
		}
		rule += ";BYDAY=" + strings.Join(codes, ",")
	}
	setRaw(comp, ical.PropRecurrenceRule, rule)
}

func setText(comp *ical.Component, name, value string) {
	if value == "" {
		comp.Props.Del(name)
		return
	}
	comp.Props.SetText(name, value)
}

func setTimes(cal *ical.Calendar, comp *ical.Component, in EventInput) {
	comp.Props.Del(ical.PropDuration)
	for name, t := range map[string]time.Time{ical.PropDateTimeStart: in.Start, ical.PropDateTimeEnd: in.End} {
		p := ical.NewProp(name)
		switch {
		case in.AllDay:
			p.SetDate(t)
		case in.Zone == time.UTC:
			p.SetDateTime(t.UTC())
		default:
			p.SetDateTime(t.In(in.Zone))
		}
		comp.Props.Set(p)
	}
	if !in.AllDay && in.Zone != time.UTC {
		ensureVTimezone(cal, in.Zone, in.Start)
	}
}

// ensureVTimezone adds a VTIMEZONE for loc unless one with that TZID exists.
func ensureVTimezone(cal *ical.Calendar, loc *time.Location, start time.Time) {
	for _, c := range cal.Children {
		if id, _ := c.Props.Text(ical.PropTimezoneID); c.Name == ical.CompTimezone && id == loc.String() {
			return
		}
	}
	from := time.Date(start.In(loc).Year(), 1, 1, 0, 0, 0, 0, loc)
	cal.Children = append([]*ical.Component{VTimezone(loc, from, from.AddDate(vtimezoneYears, 0, 0))}, cal.Children...)
}

// touch records a change for clients: DTSTAMP and LAST-MODIFIED now, SEQUENCE + 1.
func touch(comp *ical.Component, now time.Time) {
	for _, name := range []string{ical.PropDateTimeStamp, ical.PropLastModified} {
		p := ical.NewProp(name)
		p.SetDateTime(now.UTC())
		comp.Props.Set(p)
	}
	seq := 0
	if p := comp.Props.Get(ical.PropSequence); p != nil {
		seq, _ = p.Int()
	}
	setRaw(comp, ical.PropSequence, strconv.Itoa(seq+1))
}

func applyText(comp *ical.Component, in EventInput) {
	setText(comp, ical.PropSummary, in.Title)
	setText(comp, ical.PropLocation, in.Location)
	setText(comp, ical.PropDescription, in.Description)
}

// NewEvent builds a one-VEVENT object for a web create.
func NewEvent(uid string, in EventInput, now time.Time) *ical.Calendar {
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, ProductID)
	ev := ical.NewEvent()
	ev.Props.SetText(ical.PropUID, uid)
	created := ical.NewProp(ical.PropCreated)
	created.SetDateTime(now.UTC())
	ev.Props.Set(created)
	applyText(ev.Component, in)
	cal.Children = append(cal.Children, ev.Component)
	setTimes(cal, ev.Component, in)
	setRepeat(ev.Component, in.Repeat)
	touch(ev.Component, now)
	setRaw(ev.Component, ical.PropSequence, "0")
	return cal
}

// EditAll edits the master. A change of start or repeat preset removes overrides, EXDATE and
// RDATE: they name instants of the old series. Text-only edits keep them; overrides keep their
// own text.
func EditAll(cal *ical.Calendar, in EventInput, now time.Time) error {
	m, err := Master(cal)
	if err != nil {
		return err
	}
	old, err := spanOf(m, time.UTC)
	if err != nil {
		return err
	}
	moved := !old.start.Equal(in.Start) || old.allDay != in.AllDay ||
		(in.Repeat.Freq != "custom" && !RepeatOf(m).equal(in.Repeat))
	applyText(m, in)
	setTimes(cal, m, in)
	setRepeat(m, in.Repeat)
	if moved {
		m.Props.Del(ical.PropExceptionDates)
		m.Props.Del(ical.PropRecurrenceDates)
		cal.Children = slices.DeleteFunc(cal.Children, func(c *ical.Component) bool {
			return c.Name == ical.CompEvent && c.Props.Get(ical.PropRecurrenceID) != nil
		})
	}
	touch(m, now)
	return nil
}

// occurrenceProp names an occurrence (RECURRENCE-ID or EXDATE) in the form of the master's
// DTSTART, so every client matches it: a DATE, wall time in the TZID as written, or UTC.
func occurrenceProp(master *ical.Component, name, key string) (*ical.Prop, error) {
	ds := master.Props.Get(ical.PropDateTimeStart)
	p := ical.NewProp(name)
	switch {
	case isDate(ds):
		t, err := time.Parse("20060102", key)
		if err != nil {
			return nil, ErrNoSuchOccurrence
		}
		p.SetDate(t)
	case strings.HasSuffix(key, "Z"):
		t, err := time.Parse("20060102T150405Z", key)
		if err != nil {
			return nil, ErrNoSuchOccurrence
		}
		tzid := ds.Params.Get(ical.ParamTimezoneID)
		if loc, ok := ResolveZone(tzid); ok {
			p.Params.Set(ical.ParamTimezoneID, tzid)
			p.Value = t.In(loc).Format("20060102T150405")
		} else {
			p.SetDateTime(t)
		}
	default:
		if _, err := time.Parse("20060102T150405", key); err != nil {
			return nil, ErrNoSuchOccurrence
		}
		p.Value = key
	}
	return p, nil
}

// overrideFor returns the override for key, or nil.
func overrideFor(cal *ical.Calendar, key string) *ical.Component {
	for _, c := range cal.Children {
		rid := c.Props.Get(ical.PropRecurrenceID)
		if c.Name != ical.CompEvent || rid == nil {
			continue
		}
		loc, floating, _ := zoneOf(rid, time.UTC)
		if t, err := parseWall(rid.Value, loc); err == nil && occurrenceKey(t, isDate(rid), floating) == key {
			return c
		}
	}
	return nil
}

// EditOne writes an override for one occurrence: a copy of the master without its recurrence,
// with the edit applied. An existing override for that occurrence is edited in place.
func EditOne(cal *ical.Calendar, recurrenceID string, in EventInput, now time.Time) error {
	m, err := Master(cal)
	if err != nil {
		return err
	}
	if m.Props.Get(ical.PropRecurrenceRule) == nil && m.Props.Get(ical.PropRecurrenceDates) == nil {
		return ErrNotRecurring
	}
	rid, err := occurrenceProp(m, ical.PropRecurrenceID, recurrenceID)
	if err != nil {
		return err
	}
	o := overrideFor(cal, recurrenceID)
	if o == nil {
		o = ical.NewComponent(ical.CompEvent)
		for name, props := range m.Props {
			switch name {
			case ical.PropRecurrenceRule, ical.PropRecurrenceDates, ical.PropExceptionDates:
				continue
			}
			o.Props[name] = slices.Clone(props)
		}
		o.Children = slices.Clone(m.Children)
		o.Props.Set(rid)
		cal.Children = append(cal.Children, o)
	}
	applyText(o, in)
	setTimes(cal, o, in)
	touch(o, now)
	return nil
}

// DeleteOne hides one occurrence: an EXDATE on the master, and its override removed.
func DeleteOne(cal *ical.Calendar, recurrenceID string, now time.Time) error {
	m, err := Master(cal)
	if err != nil {
		return err
	}
	if m.Props.Get(ical.PropRecurrenceRule) == nil && m.Props.Get(ical.PropRecurrenceDates) == nil {
		return ErrNotRecurring
	}
	ex, err := occurrenceProp(m, ical.PropExceptionDates, recurrenceID)
	if err != nil {
		return err
	}
	m.Props.Add(ex)
	if o := overrideFor(cal, recurrenceID); o != nil {
		cal.Children = slices.DeleteFunc(cal.Children, func(c *ical.Component) bool { return c == o })
	}
	touch(m, now)
	return nil
}
```

Notes for the implementer:
- `EditOne` copies `m.Children` (the VALARM) by pointer. Neither component mutates alarms, so sharing them is safe, and the encoder writes each copy.
- `TestEditErrors` calls `DeleteOne(…, "garbage", …)` on `lists.ics`, which has a UTC DTSTART, so `occurrenceProp` takes the floating branch and fails to parse. That is `ErrNoSuchOccurrence`.

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/calendar/`
Expected: PASS. If go-ical's encoder writes properties in a different textual form than a test's `strings.Contains` expects, check the round-tripped output first. Examples: `SEQUENCE:1` as written, or the parameter order in `ATTENDEE`. Adjust the assertion only to the equivalent encoded form, never to drop a property.

- [ ] **Step 6: DOX**

In `internal/calendar/AGENTS.md` Local Contracts, add:

```markdown
- Web edits are pure functions over the decoded object that mutate in place, so unknown properties, X- properties, ATTENDEEs and VALARMs survive: `NewEvent`, `EditAll`, `EditOne` (writes or edits a RECURRENCE-ID override copied from the master without RRULE/RDATE/EXDATE), `DeleteOne` (EXDATE plus removal of that occurrence's override). Each edit sets DTSTAMP and LAST-MODIFIED and bumps SEQUENCE. `EditAll` drops overrides, EXDATE and RDATE only when the start, all-day flag or repeat preset changes; text-only edits keep them and overrides keep their own text.
- Occurrences are named in the master DTSTART's form (DATE, wall time in the TZID as written, or UTC). Timed web events are written with `TZID=<IANA>` plus a generated VTIMEZONE (`VTimezone`, offset changes for 11 years from the start's year); UTC-anchored events use `Z` values. Repeat presets are `FREQ=DAILY|WEEKLY[;BYDAY=…]|MONTHLY|YEARLY`; anything else (`COUNT`, `UNTIL`, `INTERVAL`≠1, ordinal `BYDAY`, other BY parts, RDATE) is `custom` and kept.
```

- [ ] **Step 7: Commit**

```bash
git add internal/calendar
git commit -m "feat(calendar): apply web edits to stored objects without losing properties"
```

---

### Task 4: One write path

**Files:**
- Create: `internal/davbackend/write.go`, `internal/davbackend/write_test.go`
- Modify: `internal/davbackend/backend.go` (`ensureDefault`, `PutCalendarObject` use the new functions)
- Modify: `internal/store/store.go`, `internal/store/calendars.go` (`GetObjectByUID`), `internal/store/calendars_test.go`
- Docs: `internal/davbackend/AGENTS.md`, `internal/store/AGENTS.md`

**Interfaces:**
- Consumes: `calendar.Inspect`, `caldav.ValidateCalendarObject`, `store.CalendarStore.PutObject`.
- Produces:

```go
// package davbackend
var ErrInvalidResource = errors.New("davbackend: not a valid calendar object resource")
func Write(ctx context.Context, st store.Store, calendarID, name string, cal *ical.Calendar, raw []byte, ifMatch string, ifNoneMatch bool, lim store.OwnerLimits) (*store.CalendarObject, error)
func EnsureDefault(ctx context.Context, st store.Store, userID string, maxCalendars int) error
// package store, on CalendarStore
GetObjectByUID(ctx context.Context, calendarID, uid string) (*CalendarObject, error)
```

`Write` returns `ErrInvalidResource`, `calendar.ErrUnsupportedComponent`, an error wrapping `calendar.ErrInvalidData`, `store.ErrPreconditionFailed`, `store.ErrUIDConflict`, `store.ErrQuotaExceeded`, or a store error.

- [ ] **Step 1: Write the failing tests**

Append to `internal/store/calendars_test.go`:

```go
func TestGetObjectByUID(t *testing.T) {
	ctx := context.Background()
	cs, c := calStore(t)
	if _, err := cs.PutObject(ctx, obj(c.ID, "file-name.ics", "the-uid", "x", 0, nil), "", false, store.OwnerLimits{}); err != nil {
		t.Fatal(err)
	}
	got, err := cs.GetObjectByUID(ctx, c.ID, "the-uid")
	if err != nil || got.Name != "file-name.ics" || string(got.Data) != "x" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := cs.GetObjectByUID(ctx, c.ID, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
```

`internal/davbackend/write_test.go`:

```go
package davbackend_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/emersion/go-ical"

	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

const event = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:w1\r\nDTSTAMP:20261001T000000Z\r\nDTSTART:20261007T090000Z\r\nDTEND:20261007T100000Z\r\nSUMMARY:W\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

func decode(t *testing.T, s string) *ical.Calendar {
	t.Helper()
	cal, err := ical.NewDecoder(bytes.NewReader([]byte(s))).Decode()
	if err != nil {
		t.Fatal(err)
	}
	return cal
}

func TestWrite(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := davbackend.EnsureDefault(ctx, st, "usr_w", 0); err != nil {
		t.Fatal(err)
	}
	cals, _ := st.Calendars().ListCalendarsByOwner(ctx, "user", "usr_w")
	if len(cals) != 1 || cals[0].Slug != "default" {
		t.Fatalf("default calendar: %+v", cals)
	}
	if err := davbackend.EnsureDefault(ctx, st, "usr_w", 0); err != nil {
		t.Fatal("EnsureDefault must be idempotent:", err)
	}
	id := cals[0].ID

	o, err := davbackend.Write(ctx, st, id, "w1.ics", decode(t, event), []byte(event), "", true, store.OwnerLimits{})
	if err != nil || o.ETag == "" {
		t.Fatalf("write: %+v %v", o, err)
	}
	if _, err := davbackend.Write(ctx, st, id, "w1.ics", decode(t, event), []byte(event), "", true, store.OwnerLimits{}); !errors.Is(err, store.ErrPreconditionFailed) {
		t.Fatalf("If-None-Match on an existing object: %v", err)
	}
	if _, err := davbackend.Write(ctx, st, id, "w1.ics", decode(t, event), []byte(event), "stale", false, store.OwnerLimits{}); !errors.Is(err, store.ErrPreconditionFailed) {
		t.Fatalf("stale If-Match: %v", err)
	}
	todo := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VTODO\r\nUID:t1\r\nDTSTAMP:20261001T000000Z\r\nEND:VTODO\r\nEND:VCALENDAR\r\n"
	if _, err := davbackend.Write(ctx, st, id, "t1.ics", decode(t, todo), []byte(todo), "", true, store.OwnerLimits{}); !errors.Is(err, calendar.ErrUnsupportedComponent) {
		t.Fatalf("VTODO: %v", err)
	}
	method := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nMETHOD:REQUEST\r\nBEGIN:VEVENT\r\nUID:m1\r\nDTSTAMP:20261001T000000Z\r\nDTSTART:20261007T090000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	if _, err := davbackend.Write(ctx, st, id, "m1.ics", decode(t, method), []byte(method), "", true, store.OwnerLimits{}); !errors.Is(err, davbackend.ErrInvalidResource) {
		t.Fatalf("METHOD: %v", err)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/store/ -run GetObjectByUID; go test ./internal/davbackend/`
Expected: FAIL to compile.

- [ ] **Step 3: Store lookup**

Add to the `CalendarStore` interface in `internal/store/store.go`: `GetObjectByUID(ctx context.Context, calendarID, uid string) (*CalendarObject, error)`. Then add to `internal/store/calendars.go`:

```go
func (c *calendarStore) GetObjectByUID(ctx context.Context, calendarID, uid string) (*CalendarObject, error) {
	return scanObject(c.store.db.QueryRowContext(ctx, c.q(`SELECT `+objectCols+` FROM calendar_objects
WHERE calendar_id = ? AND uid = ?`), calendarID, uid))
}
```

- [ ] **Step 4: Write `internal/davbackend/write.go`**

```go
package davbackend

import (
	"context"
	"errors"
	"fmt"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav/caldav"
	"github.com/google/uuid"

	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// ErrInvalidResource is an object CalDAV refuses outright, such as one carrying METHOD.
var ErrInvalidResource = errors.New("davbackend: not a valid calendar object resource")

// Write is the one write path for calendar objects, from CalDAV PUT and from web edits alike:
// CalDAV validation, Inspect, then a store write with ETag preconditions, quotas and a change
// row in one transaction.
func Write(ctx context.Context, st store.Store, calendarID, name string, cal *ical.Calendar, raw []byte, ifMatch string, ifNoneMatch bool, lim store.OwnerLimits) (*store.CalendarObject, error) {
	if _, _, err := caldav.ValidateCalendarObject(cal); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidResource, err)
	}
	info, err := calendar.Inspect(cal)
	if err != nil {
		return nil, err
	}
	o := &store.CalendarObject{CalendarID: calendarID, Name: name, UID: info.UID, Data: raw, FirstStart: info.FirstStart, LastEnd: info.LastEnd}
	if _, err := st.Calendars().PutObject(ctx, o, ifMatch, ifNoneMatch, lim); err != nil {
		return nil, err
	}
	return o, nil
}

// EnsureDefault creates the user's default personal calendar when they own none.
func EnsureDefault(ctx context.Context, st store.Store, userID string, maxCalendars int) error {
	cals, err := st.Calendars().ListCalendarsByOwner(ctx, ownerUser, userID)
	if err != nil || len(cals) > 0 {
		return err
	}
	err = st.Calendars().CreateCalendar(ctx, &store.Calendar{
		ID: "cal_" + uuid.NewString(), OwnerKind: ownerUser, OwnerID: userID,
		Slug: defaultSlug, Name: "Calendar",
	}, maxCalendars)
	if errors.Is(err, store.ErrAlreadyExists) {
		return nil
	}
	return err
}
```

In `internal/davbackend/backend.go`:
- Replace the body of `ensureDefault` with `return EnsureDefault(ctx, b.Store, b.User.ID, b.MaxCalendarsPerUser)`.
- In `PutCalendarObject`, replace everything from `if _, _, err := caldav.ValidateCalendarObject(cal)` through the `PutObject` error switch with:

```go
	ifMatch, err := ifMatchETag(opts.IfMatch)
	if err != nil {
		return nil, err
	}
	ifNoneMatch := opts.IfNoneMatch.IsSet() && opts.IfNoneMatch.IsWildcard()
	// A group calendar is its own owner, so the per-owner limits apply to each group calendar.
	o, err := Write(ctx, b.Store, c.ID, name, cal, opts.Raw, ifMatch, ifNoneMatch, b.limits())
	switch {
	case errors.Is(err, ErrInvalidResource):
		return nil, caldav.NewPreconditionError(caldav.PreconditionValidCalendarObjectResource)
	case errors.Is(err, calendar.ErrUnsupportedComponent):
		return nil, caldav.NewPreconditionError(caldav.PreconditionSupportedCalendarComponent)
	case errors.Is(err, calendar.ErrInvalidData):
		return nil, caldav.NewPreconditionError(caldav.PreconditionValidCalendarData)
	case errors.Is(err, store.ErrQuotaExceeded):
		return nil, errQuota
	case errors.Is(err, store.ErrPreconditionFailed):
		return nil, webdav.NewHTTPError(http.StatusPreconditionFailed, err)
	case errors.Is(err, store.ErrUIDConflict):
		return nil, caldav.NewPreconditionError(caldav.PreconditionNoUIDConflict)
	case err != nil:
		return nil, err
	}
```

and keep the existing `return &caldav.CalendarObject{...}` line, which uses `o.ETag` and `o.ModifiedAt`. Add:

```go
func (b *Backend) limits() store.OwnerLimits {
	return store.OwnerLimits{MaxObjects: b.MaxObjectsPerUser, MaxBytes: b.MaxBytesPerUser, MaxTotalBytes: b.MaxBytesTotal}
}
```

Before this change, every non-`ErrUnsupportedComponent` error from `Inspect` answered `valid-calendar-data`. Check that every error `Inspect` returns wraps `calendar.ErrInvalidData` or is `ErrUnsupportedComponent`: read `Inspect`, `eventSpan` and the helpers in `object.go`. If one does not, wrap it there with `%w: ErrInvalidData` rather than adding a catch-all mapping.

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/store/ ./internal/davbackend/ ./internal/api/`
Expected: PASS, including every Plan 1 and Plan 2 CalDAV test, which proves the extraction kept behavior.

- [ ] **Step 6: DOX**

- In `internal/davbackend/AGENTS.md` Local Contracts, add: "`Write` is the one calendar-object write path, used by `PutCalendarObject` and the JSON event API: `caldav.ValidateCalendarObject` (`ErrInvalidResource`), `calendar.Inspect` (`ErrUnsupportedComponent`, `ErrInvalidData`), then `store.PutObject` with ETag preconditions, owner and instance quotas and the change row in one transaction. `EnsureDefault` creates a user's default calendar when they own none; CalDAV listing and `GET /api/calendars` both call it."
- In `internal/store/AGENTS.md`, add: "`GetObjectByUID` finds an object by its UID within one calendar (UIDs are unique per calendar), for the JSON event API."

- [ ] **Step 7: Commit**

```bash
git add internal/davbackend internal/store
git commit -m "refactor(davbackend): one write path for CalDAV and web edits"
```

---

### Task 5: Calendar list and event read API

**Files:**
- Create: `internal/api/calendars.go`, `internal/api/events.go`, `internal/api/events_test.go`
- Modify: `internal/api/server.go` (`routes()`), `internal/api/authz_matrix_test.go`
- Docs: `internal/api/AGENTS.md`

**Interfaces:**
- Consumes: `calendar.Expand`, `calendar.RepeatOf`, `calendar.Master`, `calendar.ResolveZone`; `davbackend.EnsureDefault`, `davbackend.Prefix`; `access.Resolve`; `sessionUser`, `requireEveryday`, `s.handle` (Plan 2); `store.CalendarStore.ListObjectsInRange`, `GetCalendarByID`, `UserGrants`, `ListCalendarsByOwner`.
- Produces:
  - Routes: `GET /api/calendars` and `GET /api/events`.
  - Helpers later tasks reuse:

```go
type calendarView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
	Kind        string `json:"kind"` // "personal" | "group"
	Role        string `json:"role"` // "owner" | "manager" | "editor" | "reader"
	DAVPath     string `json:"dav_path"`
}
func (s *Server) visibleCalendars(ctx context.Context, user *store.User) ([]*store.Calendar, []store.CalendarGrant, error)
// calendarFor loads a calendar and the user's role; it writes 404 (cannot read) or 500 and returns nil.
func (s *Server) calendarFor(w http.ResponseWriter, r *http.Request, id string) (*store.Calendar, access.Role)
func (s *Server) objectLimits() store.OwnerLimits
func roleName(r access.Role) string
```

- [ ] **Step 1: Write the failing tests**

`internal/api/events_test.go`:

```go
package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/store"
)

const recurringICS = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:weekly\r\nDTSTAMP:20261001T000000Z\r\nDTSTART;TZID=Europe/Berlin:20261005T090000\r\nDTEND;TZID=Europe/Berlin:20261005T100000\r\nRRULE:FREQ=WEEKLY\r\nSUMMARY:<b>Standup</b>\r\nLOCATION:Room 1\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

func putObject(t *testing.T, st store.Store, cal *store.Calendar, name, uid, data string, first int64) {
	t.Helper()
	o := &store.CalendarObject{CalendarID: cal.ID, Name: name, UID: uid, Data: []byte(data), FirstStart: first}
	if _, err := st.Calendars().PutObject(context.Background(), o, "", false, store.OwnerLimits{}); err != nil {
		t.Fatal(err)
	}
}

type eventJSON struct {
	CalendarID   string `json:"calendar_id"`
	UID          string `json:"uid"`
	RecurrenceID string `json:"recurrence_id"`
	ETag         string `json:"etag"`
	Title        string `json:"title"`
	Location     string `json:"location"`
	Start        string `json:"start"`
	End          string `json:"end"`
	AllDay       bool   `json:"all_day"`
	Recurring    bool   `json:"recurring"`
	Editable     bool   `json:"editable"`
	Repeat       struct {
		Freq string `json:"freq"`
	} `json:"repeat"`
}

func TestCalendarsAndEventsForAReader(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	cookie := loginAs(t, srv, st, "rita", "user")
	group := groupCalendar(t, st, "Team")
	grantRole(t, st, group, "reader", "usr_rita")
	putObject(t, st, group, "weekly.ics", "weekly", recurringICS, 1791183600)

	w := call(t, srv, "GET", "/api/calendars", "", cookie)
	var cals []struct{ ID, Kind, Role, DAVPath string }
	if err := json.Unmarshal(w.Body.Bytes(), &cals); err != nil || w.Code != http.StatusOK {
		t.Fatalf("calendars %d %s", w.Code, w.Body.String())
	}
	if len(cals) != 2 || cals[0].Kind != "personal" || cals[0].Role != "owner" || cals[1].ID != group.ID || cals[1].Role != "reader" {
		t.Fatalf("calendars %+v", cals)
	}

	w = call(t, srv, "GET", "/api/events?start=2026-10-01T00:00:00Z&end=2026-10-20T00:00:00Z&tz=America/New_York", "", cookie)
	var evs []eventJSON
	if err := json.Unmarshal(w.Body.Bytes(), &evs); err != nil || w.Code != http.StatusOK {
		t.Fatalf("events %d %s", w.Code, w.Body.String())
	}
	if len(evs) != 3 {
		t.Fatalf("want 3 weekly instances, got %+v", evs)
	}
	e := evs[0]
	if e.Start != "2026-10-05T03:00:00-04:00" || e.Title != "<b>Standup</b>" || e.Editable || !e.Recurring || e.Repeat.Freq != "weekly" || e.RecurrenceID != "20261005T070000Z" || e.ETag == "" {
		t.Fatalf("instance %+v", e)
	}
}

func TestEventsValidation(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	cookie := loginAs(t, srv, st, "vic", "user")
	for _, q := range []string{
		"",
		"?start=2026-10-01T00:00:00Z",
		"?start=2026-10-02T00:00:00Z&end=2026-10-01T00:00:00Z",
		"?start=2026-01-01T00:00:00Z&end=2027-02-15T00:00:00Z",
		"?start=2026-10-01T00:00:00Z&end=2026-10-02T00:00:00Z&tz=Not/AZone",
		"?start=2026-10-01T00:00:00Z&end=2026-10-02T00:00:00Z&tz=Local",
	} {
		if w := call(t, srv, "GET", "/api/events"+q, "", cookie); w.Code != http.StatusBadRequest {
			t.Errorf("%q: %d, want 400", q, w.Code)
		}
	}
	other := groupCalendar(t, st, "Secret")
	if w := call(t, srv, "GET", "/api/events?start=2026-10-01T00:00:00Z&end=2026-10-02T00:00:00Z&calendar="+other.ID, "", cookie); w.Code != http.StatusNotFound {
		t.Errorf("unreadable calendar: %d, want 404", w.Code)
	}
}

func TestEventsInstanceCap(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	cookie := loginAs(t, srv, st, "hal", "user")
	group := groupCalendar(t, st, "Busy")
	grantRole(t, st, group, "editor", "usr_hal")
	minutely := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:hourly\r\nDTSTAMP:20261001T000000Z\r\nDTSTART:20260101T000000Z\r\nDTEND:20260101T000500Z\r\nRRULE:FREQ=HOURLY\r\nSUMMARY:x\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	putObject(t, st, group, "hourly.ics", "hourly", minutely, 1767225600)
	w := call(t, srv, "GET", "/api/events?start=2026-01-01T00:00:00Z&end=2026-12-31T00:00:00Z", "", cookie)
	if w.Code != http.StatusUnprocessableEntity || !json.Valid(w.Body.Bytes()) {
		t.Fatalf("hourly for a year: %d %s", w.Code, w.Body.String())
	}
}
```

The weekly fixture starts 2026-10-05 09:00 Berlin, which is 07:00Z (CEST), so its unix `first_start` is 1791183600. Instances on Oct 5, 12 and 19 fall before Oct 20 00:00Z. Oct 5 07:00Z is 03:00 EDT in New York.

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/api/ -run 'CalendarsAndEvents|EventsValidation|EventsInstanceCap'`
Expected: FAIL: the routes return the SPA (200 HTML) or 404.

- [ ] **Step 3: Write `internal/api/calendars.go`**

```go
package api

import (
	"context"
	"errors"
	"net/http"
	"sort"

	"github.com/Busnes-app/kycalendar/internal/access"
	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/store"
)

type calendarView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
	Kind        string `json:"kind"`
	Role        string `json:"role"`
	DAVPath     string `json:"dav_path"`
}

func roleName(r access.Role) string {
	switch r {
	case access.Owner:
		return "owner"
	case access.Manager:
		return "manager"
	case access.Editor:
		return "editor"
	case access.Reader:
		return "reader"
	}
	return ""
}

func (s *Server) objectLimits() store.OwnerLimits {
	l := s.config.Calendar
	return store.OwnerLimits{MaxObjects: l.MaxObjectsPerUser, MaxBytes: l.MaxBytesPerUser, MaxTotalBytes: l.MaxBytesTotal}
}

func (s *Server) viewOf(user *store.User, c *store.Calendar, role access.Role) calendarView {
	kind, seg := "personal", c.Slug
	if c.OwnerKind == "group" {
		kind, seg = "group", "_"+c.ID
	}
	return calendarView{ID: c.ID, Name: c.Name, Color: c.Color, Description: c.Description, Kind: kind, Role: roleName(role),
		DAVPath: davbackend.Prefix + "/" + user.ID + "/calendars/" + seg + "/"}
}

// visibleCalendars lists the user's personal calendars (creating the default) and every group
// calendar a grant lets them read, with the user's grants for resolving roles.
func (s *Server) visibleCalendars(ctx context.Context, user *store.User) ([]*store.Calendar, []store.CalendarGrant, error) {
	if err := davbackend.EnsureDefault(ctx, s.store, user.ID, s.config.Calendar.MaxCalendarsPerUser); err != nil {
		return nil, nil, err
	}
	cals, err := s.store.Calendars().ListCalendarsByOwner(ctx, "user", user.ID)
	if err != nil {
		return nil, nil, err
	}
	grants, err := s.store.Calendars().UserGrants(ctx, user.ID)
	if err != nil {
		return nil, nil, err
	}
	seen := map[string]bool{}
	var groups []*store.Calendar
	for _, g := range grants {
		if seen[g.CalendarID] {
			continue
		}
		seen[g.CalendarID] = true
		c, err := s.store.Calendars().GetCalendarByID(ctx, g.CalendarID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		if access.Resolve(user, c, grants).CanRead() {
			groups = append(groups, c)
		}
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
	return append(cals, groups...), grants, nil
}

// calendarFor loads the calendar named id and the session user's role on it. A calendar the user
// cannot read answers 404, like one that does not exist; the caller checks the role it needs.
func (s *Server) calendarFor(w http.ResponseWriter, r *http.Request, id string) (*store.Calendar, access.Role) {
	user := sessionUser(r.Context())
	c, err := s.store.Calendars().GetCalendarByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "No such calendar")
		return nil, access.None
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load the calendar")
		return nil, access.None
	}
	grants, err := s.store.Calendars().UserGrants(r.Context(), user.ID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load access")
		return nil, access.None
	}
	role := access.Resolve(user, c, grants)
	if !role.CanRead() {
		s.writeError(w, http.StatusNotFound, "No such calendar")
		return nil, access.None
	}
	return c, role
}

func (s *Server) handleListCalendars(w http.ResponseWriter, r *http.Request) {
	user := sessionUser(r.Context())
	cals, grants, err := s.visibleCalendars(r.Context(), user)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to list calendars")
		return
	}
	out := make([]calendarView, 0, len(cals))
	for _, c := range cals {
		out = append(out, s.viewOf(user, c, access.Resolve(user, c, grants)))
	}
	s.writeJSON(w, http.StatusOK, out)
}
```

- [ ] **Step 4: Write `internal/api/events.go` (read side)**

```go
package api

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/emersion/go-ical"

	"github.com/Busnes-app/kycalendar/internal/access"
	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/store"
)

const (
	maxEventRange     = 400 * 24 * time.Hour
	maxEventInstances = 5000
)

type repeatView struct {
	Freq     string   `json:"freq"`
	Weekdays []string `json:"weekdays,omitempty"`
}

type eventView struct {
	CalendarID   string     `json:"calendar_id"`
	UID          string     `json:"uid"`
	RecurrenceID string     `json:"recurrence_id,omitempty"`
	ETag         string     `json:"etag"`
	Title        string     `json:"title"`
	Location     string     `json:"location,omitempty"`
	Description  string     `json:"description,omitempty"`
	Start        string     `json:"start"`
	End          string     `json:"end"`
	AllDay       bool       `json:"all_day"`
	Recurring    bool       `json:"recurring"`
	Override     bool       `json:"override,omitempty"`
	Repeat       repeatView `json:"repeat"`
	Floating     bool       `json:"floating,omitempty"`
	UnknownZone  bool       `json:"unknown_zone,omitempty"`
	Partial      bool       `json:"partial,omitempty"`
	Zone         string     `json:"zone,omitempty"`
	Editable     bool       `json:"editable"`
}

var weekdayCodes = []string{"SU", "MO", "TU", "WE", "TH", "FR", "SA"}

func repeatViewOf(r calendar.Repeat) repeatView {
	v := repeatView{Freq: r.Freq}
	for _, d := range r.Weekdays {
		v.Weekdays = append(v.Weekdays, weekdayCodes[d])
	}
	return v
}

// viewerZone reads the tz query parameter: an IANA name, default UTC. "Local" is refused: it
// would be the server's zone, not the viewer's.
func viewerZone(r *http.Request) (*time.Location, bool) {
	name := r.URL.Query().Get("tz")
	if name == "" {
		return time.UTC, true
	}
	if name == "Local" {
		return nil, false
	}
	loc, err := time.LoadLocation(name)
	return loc, err == nil
}

func instanceView(c *store.Calendar, o *store.CalendarObject, master *ical.Component, in calendar.Instance, viewer *time.Location, role access.Role) eventView {
	text := func(name string) string { v, _ := in.Event.Props.Text(name); return v }
	v := eventView{CalendarID: c.ID, UID: in.UID, RecurrenceID: in.RecurrenceID, ETag: o.ETag,
		Title: text(ical.PropSummary), Location: text(ical.PropLocation), Description: text(ical.PropDescription),
		AllDay: in.AllDay, Recurring: in.Recurring, Override: in.Override, Floating: in.Floating,
		UnknownZone: in.UnknownZone, Partial: in.Partial, Editable: role.CanWrite()}
	if master != nil {
		v.Repeat = repeatViewOf(calendar.RepeatOf(master))
		if p := master.Props.Get(ical.PropDateTimeStart); p != nil {
			v.Zone = p.Params.Get(ical.ParamTimezoneID)
		}
	}
	if in.AllDay {
		v.Start, v.End = in.Start.In(viewer).Format(time.DateOnly), in.End.In(viewer).Format(time.DateOnly)
	} else {
		v.Start, v.End = in.Start.In(viewer).Format(time.RFC3339), in.End.In(viewer).Format(time.RFC3339)
	}
	return v
}

func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	user := sessionUser(r.Context())
	q := r.URL.Query()
	start, err1 := time.Parse(time.RFC3339, q.Get("start"))
	end, err2 := time.Parse(time.RFC3339, q.Get("end"))
	if err1 != nil || err2 != nil || !end.After(start) || end.Sub(start) > maxEventRange {
		s.writeError(w, http.StatusBadRequest, "start and end must be RFC 3339 times, end after start, at most 400 days apart")
		return
	}
	viewer, ok := viewerZone(r)
	if !ok {
		s.writeError(w, http.StatusBadRequest, "tz must be an IANA time zone")
		return
	}
	cals, grants, err := s.visibleCalendars(r.Context(), user)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to list calendars")
		return
	}
	if want := q.Get("calendar"); want != "" {
		byID := map[string]*store.Calendar{}
		for _, c := range cals {
			byID[c.ID] = c
		}
		cals = cals[:0]
		for _, id := range strings.Split(want, ",") {
			c, ok := byID[id]
			if !ok {
				s.writeError(w, http.StatusNotFound, "No such calendar")
				return
			}
			cals = append(cals, c)
		}
	}
	out := []eventView{}
	for _, c := range cals {
		role := access.Resolve(user, c, grants)
		objs, err := s.store.Calendars().ListObjectsInRange(r.Context(), c.ID, start.Unix(), end.Unix())
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, "Failed to load events")
			return
		}
		for _, o := range objs {
			cal, err := ical.NewDecoder(bytes.NewReader(o.Data)).Decode()
			if err != nil {
				s.writeError(w, http.StatusInternalServerError, "A stored event does not parse")
				return
			}
			insts, err := calendar.Expand(cal, start, end, viewer, maxEventInstances-len(out))
			if errors.Is(err, calendar.ErrTooManyInstances) {
				s.writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "Too many events in this range; choose a shorter range", "code": "too_many_instances"})
				return
			}
			if err != nil {
				s.writeError(w, http.StatusInternalServerError, "A stored event does not expand")
				return
			}
			master, _ := calendar.Master(cal)
			for _, in := range insts {
				out = append(out, instanceView(c, o, master, in, viewer, role))
			}
		}
	}
	s.writeJSON(w, http.StatusOK, out)
}
```

If `ListObjectsInRange` returns no rows because the index widened (unknown zones, unbounded rules), the object is still returned and `Expand` decides. A store object whose `Expand` answers zero instances adds nothing.

When `maxEventInstances-len(out)` reaches 0, the next object with any instance returns `ErrTooManyInstances`, which is the intended 422.

- [ ] **Step 5: Register the routes and matrix rows**

In `routes()` in `internal/api/server.go`, after the group calendar block:

```go
	// Calendars and events for everyday users. Roles come from access.Resolve; a calendar the
	// caller cannot read is 404.
	s.handle("GET /api/calendars", s.requireEveryday(s.handleListCalendars))
	s.handle("GET /api/events", s.requireEveryday(s.handleListEvents))
```

In `internal/api/authz_matrix_test.go` `apiRows`, add:

```go
		"GET /api/calendars": {method: "GET", path: "/api/calendars", want: everyday},
		"GET /api/events":    {method: "GET", path: "/api/events?start=2026-10-01T00:00:00Z&end=2026-10-31T00:00:00Z", want: everyday},
```

- [ ] **Step 6: Run the tests**

Run: `go test ./internal/api/ -run 'CalendarsAndEvents|EventsValidation|EventsInstanceCap|Matrix|EveryRoute'`
Expected: PASS.

- [ ] **Step 7: DOX**

In `internal/api/AGENTS.md`, add this route table and contract:

```markdown
| Method | Path | Guard | Response |
|---|---|---|---|
| GET | `/api/calendars` | everyday | `[{id,name,color,description,kind,role,dav_path}]`, personal first (default created), then readable group calendars by name |
| GET | `/api/events?start&end[&tz][&calendar=id,id]` | everyday | expanded instances; 400 bad or > 400-day range or non-IANA `tz`; 404 an unreadable `calendar`; 422 `too_many_instances` over 5000 |

- Event instances carry `calendar_id, uid, recurrence_id, etag, title, location, description, start, end, all_day, recurring, override, repeat{freq,weekdays}, floating, unknown_zone, partial, zone, editable`. Timed `start`/`end` are RFC 3339 in the viewer's `tz`; all-day ones are dates. Text is returned verbatim; rendering it safely is the UI's job.
```

- [ ] **Step 8: Commit**

```bash
git add internal/api
git commit -m "feat(api): list calendars and expanded events"
```

---

### Task 6: Personal calendar management

**Files:**
- Modify: `internal/api/calendars.go`, `internal/api/server.go`, `internal/api/authz_matrix_test.go` (rows and a spare personal calendar in `newWorld`)
- Create: `internal/api/calendars_test.go`
- Docs: `internal/api/AGENTS.md`

**Interfaces:**
- Consumes: `calendarFor`, `viewOf`, `roleName` (Task 5); `calendar.CheckProps`; `crypto.RandomHex`; `s.tracked`, `context.WithoutCancel` pattern (Plan 2).
- Produces: `POST /api/calendars`, `PATCH /api/calendars/{id}`, `DELETE /api/calendars/{id}`.

- [ ] **Step 1: Write the failing tests**

`internal/api/calendars_test.go`:

```go
package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/store"
)

func TestPersonalCalendarLifecycle(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	cookie := loginAs(t, srv, st, "pia", "user")
	w := call(t, srv, "POST", "/api/calendars", `{"name":"Work","color":"#00aa11"}`, cookie)
	if w.Code != http.StatusCreated {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	var c struct{ ID, Name, Kind, Role string }
	_ = json.Unmarshal(w.Body.Bytes(), &c)
	if c.Kind != "personal" || c.Role != "owner" {
		t.Fatalf("created %+v", c)
	}
	if w := call(t, srv, "PATCH", "/api/calendars/"+c.ID, `{"name":"Office","color":""}`, cookie); w.Code != http.StatusOK {
		t.Fatalf("patch %d %s", w.Code, w.Body.String())
	}
	got, _ := st.Calendars().GetCalendarByID(context.Background(), c.ID)
	if got.Name != "Office" || got.Color != "" {
		t.Fatalf("patched %+v", got)
	}
	for _, body := range []string{`{"name":"  "}`, `{"color":"red"}`, `nope`} {
		if w := call(t, srv, "PATCH", "/api/calendars/"+c.ID, body, cookie); w.Code != http.StatusBadRequest {
			t.Errorf("patch %s: %d, want 400", body, w.Code)
		}
	}
	if w := call(t, srv, "DELETE", "/api/calendars/"+c.ID, "", cookie); w.Code != http.StatusNoContent {
		t.Fatalf("delete %d", w.Code)
	}
	if _, err := st.Calendars().GetCalendarByID(context.Background(), c.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("calendar survived: %v", err)
	}
}

func TestGroupCalendarPatchAndDeleteRules(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	group := groupCalendar(t, st, "Team")
	mgr := loginAs(t, srv, st, "mia", "user")
	ed := loginAs(t, srv, st, "eli", "user")
	grantRole(t, st, group, "manager", "usr_mia")
	grantRole(t, st, group, "editor", "usr_eli")
	if w := call(t, srv, "PATCH", "/api/calendars/"+group.ID, `{"name":"Team A"}`, mgr); w.Code != http.StatusOK {
		t.Fatalf("manager rename %d %s", w.Code, w.Body.String())
	}
	if w := call(t, srv, "PATCH", "/api/calendars/"+group.ID, `{"name":"x"}`, ed); w.Code != http.StatusForbidden {
		t.Fatalf("editor rename %d, want 403", w.Code)
	}
	if w := call(t, srv, "DELETE", "/api/calendars/"+group.ID, "", mgr); w.Code != http.StatusForbidden {
		t.Fatalf("manager delete of a group calendar %d, want 403 (admins delete them)", w.Code)
	}
	other := loginAs(t, srv, st, "oz", "user")
	if w := call(t, srv, "DELETE", "/api/calendars/"+group.ID, "", other); w.Code != http.StatusNotFound {
		t.Fatalf("non-member delete %d, want 404", w.Code)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/api/ -run 'PersonalCalendarLifecycle|GroupCalendarPatchAndDelete'`
Expected: FAIL (routes absent).

- [ ] **Step 3: Implement the handlers**

Append to `internal/api/calendars.go` (add `"context"`, `"encoding/json"`, `"strings"`, and the `calendar` and `crypto` packages to the imports as needed):

```go
type calendarBody struct {
	Name        *string `json:"name"`
	Color       *string `json:"color"`
	Description *string `json:"description"`
}

// decodeCalendarBody validates a create or patch at the boundary; a present name must not be blank.
func decodeCalendarBody(r *http.Request) (calendarBody, error) {
	var b calendarBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		return b, errors.New("Invalid JSON body")
	}
	if b.Name != nil {
		trimmed := strings.TrimSpace(*b.Name)
		if trimmed == "" {
			return b, errors.New("Name must not be blank")
		}
		b.Name = &trimmed
	}
	if err := calendar.CheckProps(b.Name, b.Description, b.Color); err != nil {
		return b, err
	}
	return b, nil
}

func (s *Server) handleCreateCalendar(w http.ResponseWriter, r *http.Request) {
	user := sessionUser(r.Context())
	b, err := decodeCalendarBody(r)
	if err == nil && b.Name == nil {
		err = errors.New("Name is required")
	}
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	c := &store.Calendar{ID: "cal_" + uuid.NewString(), OwnerKind: "user", OwnerID: user.ID, Slug: "c-" + crypto.RandomHex(4), Name: *b.Name}
	if b.Color != nil {
		c.Color = *b.Color
	}
	if b.Description != nil {
		c.Description = *b.Description
	}
	err = s.store.Calendars().CreateCalendar(r.Context(), c, s.config.Calendar.MaxCalendarsPerUser)
	switch {
	case errors.Is(err, store.ErrQuotaExceeded):
		s.writeJSON(w, http.StatusConflict, map[string]string{"error": "You have reached the calendar limit", "code": "quota"})
		return
	case err != nil:
		s.writeError(w, http.StatusInternalServerError, "Failed to create the calendar")
		return
	}
	s.writeJSON(w, http.StatusCreated, s.viewOf(user, c, access.Owner))
}

func (s *Server) handlePatchCalendar(w http.ResponseWriter, r *http.Request) {
	c, role := s.calendarFor(w, r, r.PathValue("id"))
	if c == nil {
		return
	}
	if !role.CanManage() {
		s.writeError(w, http.StatusForbidden, "Only the owner or a manager can change this calendar")
		return
	}
	b, err := decodeCalendarBody(r)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.Calendars().UpdateCalendar(r.Context(), c.ID, b.Name, b.Description, b.Color); err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to update the calendar")
		return
	}
	if c.OwnerKind == "group" {
		s.auditCalendar(r.Context(), r, "calendar.update", c.ID, "")
	}
	updated, err := s.store.Calendars().GetCalendarByID(r.Context(), c.ID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load the calendar")
		return
	}
	s.writeJSON(w, http.StatusOK, s.viewOf(sessionUser(r.Context()), updated, role))
}

// handleDeleteCalendar deletes a personal calendar with its events. It runs detached, like the
// group calendar delete, so a dropped connection cannot leave it half-reported.
func (s *Server) handleDeleteCalendar(w http.ResponseWriter, r *http.Request) {
	c, role := s.calendarFor(w, r, r.PathValue("id"))
	if c == nil {
		return
	}
	if role != access.Owner {
		s.writeError(w, http.StatusForbidden, "Group calendars are deleted by an administrator")
		return
	}
	ctx := context.WithoutCancel(r.Context())
	if err := s.store.Calendars().DeleteCalendar(ctx, c.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusInternalServerError, "Failed to delete the calendar")
		return
	}
	s.auditCalendar(ctx, r, "calendar.delete", c.ID, "")
	w.WriteHeader(http.StatusNoContent)
}
```

`s.auditCalendar` came from Plan 2 with the signature `(ctx context.Context, r *http.Request, action, resource, details string)`. Check the current signature with `grep -n 'func (s \*Server) auditCalendar' internal/api/*.go` and match it. `crypto` is `github.com/Busnes-app/kycalendar/internal/crypto`; `uuid` is `github.com/google/uuid`.

- [ ] **Step 4: Routes and matrix rows**

In `routes()`:

```go
	s.handle("POST /api/calendars", s.requireEveryday(s.handleCreateCalendar))
	s.handle("PATCH /api/calendars/{id}", s.requireEveryday(s.handlePatchCalendar))
	s.handle("DELETE /api/calendars/{id}", s.tracked(s.requireEveryday(s.handleDeleteCalendar)))
```

In `internal/api/authz_matrix_test.go`:
- Add to the `world` struct: `spare *store.Calendar // a personal calendar of the owner, deleted by the owner row`.
- At the end of `newWorld`, before `return w`:

```go
	w.spare = &store.Calendar{ID: "cal_spare_owner", OwnerKind: "user", OwnerID: "usr_owner", Slug: "spare", Name: "Spare"}
	if err := st.Calendars().CreateCalendar(ctx, w.spare, 0); err != nil {
		t.Fatal(err)
	}
```

- Add the expectation sets:

```go
	groupManage = expect{anon: 401, deactivated: 401, admin: 403, manager: allow, reader: 403, editor: 403, owner: 404, nonmember: 404}
	ownerOnly   = expect{anon: 401, deactivated: 401, admin: 403, owner: allow, reader: 404, editor: 404, manager: 404, nonmember: 404}
```

- Add the rows:

```go
		"POST /api/calendars":         {method: "POST", path: "/api/calendars", body: `{"name":"Matrix"}`, want: everyday},
		"PATCH /api/calendars/{id}":   {method: "PATCH", path: "/api/calendars/" + w.group.ID, body: `{"name":"Team"}`, want: groupManage},
		"DELETE /api/calendars/{id}":  {method: "DELETE", path: "/api/calendars/" + w.spare.ID, want: ownerOnly},
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/api/`
Expected: PASS.

- [ ] **Step 6: DOX**

Add to the route table in `internal/api/AGENTS.md`:

```markdown
| POST | `/api/calendars` | everyday | `{name,color?,description?}` → 201 personal calendar; 409 `quota` at `KY_CALENDAR_MAX_CALENDARS_PER_USER` |
| PATCH | `/api/calendars/{id}` | everyday; owner or manager | partial `{name?,color?,description?}` → 200; 403 reader/editor; 404 cannot read; group changes audit `calendar.update` |
| DELETE | `/api/calendars/{id}` | everyday; owner, detached and tracked | 204; 403 group calendar (admins delete those); 404 cannot read; audits `calendar.delete` |
```

Also add `handleDeleteCalendar` to the AGENTS.md sentence listing detached handlers.

- [ ] **Step 7: Commit**

```bash
git add internal/api
git commit -m "feat(api): create, rename and delete personal calendars"
```

---

### Task 7: Event create, update and delete

**Files:**
- Modify: `internal/api/events.go`, `internal/api/server.go`, `internal/api/authz_matrix_test.go`, `internal/api/events_test.go`
- Docs: `internal/api/AGENTS.md`

**Interfaces:**
- Consumes: `calendar.NewEvent`, `EditAll`, `EditOne`, `DeleteOne`, `ErrNotRecurring`, `ErrNoSuchOccurrence`, `Repeat`, `EventInput`; `davbackend.Write`, `ErrInvalidResource`; `store.GetObjectByUID`, `DeleteObject`; `calendarFor`, `objectLimits` (Task 5).
- Produces: `POST /api/calendars/{id}/events`, `PUT /api/events/{cal}/{uid}`, `DELETE /api/events/{cal}/{uid}`.

Request body for create and update:

```json
{"title":"","location":"","description":"","start":"2026-10-07T09:00:00+02:00","end":"2026-10-07T10:00:00+02:00",
 "all_day":false,"zone":"Europe/Berlin","repeat":{"freq":"weekly","weekdays":["MO","WE"]},
 "scope":"all","recurrence_id":""}
```

- Timed events: `start`/`end` are RFC 3339 and `zone` is a required IANA name.
- All-day events: `start`/`end` are dates and `end` is exclusive.
- `scope` and `recurrence_id` apply to `PUT` only.
- `repeat.freq` is one of `""`, `daily`, `weekly`, `monthly`, `yearly`, or `custom` (`custom` on `PUT` only, meaning keep the stored rule).
- Limits: title ≤ 1000 bytes, location ≤ 1000 bytes, description ≤ 65536 bytes.

- [ ] **Step 1: Write the failing tests**

Append to `internal/api/events_test.go` (add `"strings"`, `"net/http/httptest"`, `"github.com/Busnes-app/kycalendar/internal/api"` and `"github.com/Busnes-app/kycalendar/internal/auth"` to its imports):

```go
func withIfMatch(t *testing.T, srv *api.Server, method, path, body, etag string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if etag != "" {
		req.Header.Set("If-Match", `"`+etag+`"`)
	}
	req.AddCookie(cookie)
	req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: "test-csrf"})
	req.Header.Set(auth.HeaderCSRF, "test-csrf")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

func eventsIn(t *testing.T, srv *api.Server, cookie *http.Cookie, calID string) []eventJSON {
	t.Helper()
	w := call(t, srv, "GET", "/api/events?start=2026-10-01T00:00:00Z&end=2026-11-01T00:00:00Z&tz=Europe/Berlin&calendar="+calID, "", cookie)
	var evs []eventJSON
	if err := json.Unmarshal(w.Body.Bytes(), &evs); err != nil {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	return evs
}

func TestEventLifecycleOnAGroupCalendar(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	cookie := loginAs(t, srv, st, "eda", "user")
	group := groupCalendar(t, st, "Team")
	grantRole(t, st, group, "editor", "usr_eda")

	create := `{"title":"Sync","start":"2026-10-05T09:00:00+02:00","end":"2026-10-05T10:00:00+02:00","zone":"Europe/Berlin","repeat":{"freq":"weekly","weekdays":["MO"]}}`
	w := call(t, srv, "POST", "/api/calendars/"+group.ID+"/events", create, cookie)
	if w.Code != http.StatusCreated {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	var created struct{ UID, ETag string }
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	evs := eventsIn(t, srv, cookie, group.ID)
	if len(evs) != 4 || evs[0].Start != "2026-10-05T09:00:00+02:00" || !evs[0].Editable {
		t.Fatalf("created series %+v", evs)
	}

	// Move one occurrence.
	one := `{"title":"Sync (Tue)","start":"2026-10-13T09:00:00+02:00","end":"2026-10-13T10:00:00+02:00","zone":"Europe/Berlin","repeat":{"freq":"custom"},"scope":"this","recurrence_id":"` + evs[1].RecurrenceID + `"}`
	w = withIfMatch(t, srv, "PUT", "/api/events/"+group.ID+"/"+created.UID, one, created.ETag, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("edit one %d %s", w.Code, w.Body.String())
	}
	var updated struct{ ETag string }
	_ = json.Unmarshal(w.Body.Bytes(), &updated)
	evs = eventsIn(t, srv, cookie, group.ID)
	if evs[1].Title != "Sync (Tue)" || evs[1].Start != "2026-10-13T09:00:00+02:00" {
		t.Fatalf("moved occurrence %+v", evs[1])
	}

	// Delete another occurrence, then the series.
	w = withIfMatch(t, srv, "DELETE", "/api/events/"+group.ID+"/"+created.UID+"?scope=this&recurrence_id="+evs[2].RecurrenceID, "", updated.ETag, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("delete one %d %s", w.Code, w.Body.String())
	}
	_ = json.Unmarshal(w.Body.Bytes(), &updated)
	if got := eventsIn(t, srv, cookie, group.ID); len(got) != 3 {
		t.Fatalf("after deleting one: %+v", got)
	}
	w = withIfMatch(t, srv, "DELETE", "/api/events/"+group.ID+"/"+created.UID+"?scope=all", "", updated.ETag, cookie)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete all %d %s", w.Code, w.Body.String())
	}
	if got := eventsIn(t, srv, cookie, group.ID); len(got) != 0 {
		t.Fatalf("after deleting the series: %+v", got)
	}
}

func TestEventPutStaleETagIs412(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	cookie := loginAs(t, srv, st, "sam", "user")
	group := groupCalendar(t, st, "Team")
	grantRole(t, st, group, "editor", "usr_sam")
	w := call(t, srv, "POST", "/api/calendars/"+group.ID+"/events", `{"title":"A","start":"2026-10-07T09:00:00Z","end":"2026-10-07T10:00:00Z","zone":"UTC"}`, cookie)
	var created struct{ UID, ETag string }
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	before, _ := st.Calendars().GetObjectByUID(context.Background(), group.ID, created.UID)

	edit := `{"title":"B","start":"2026-10-07T09:00:00Z","end":"2026-10-07T10:00:00Z","zone":"UTC","scope":"all"}`
	if w := withIfMatch(t, srv, "PUT", "/api/events/"+group.ID+"/"+created.UID, edit, "0000", cookie); w.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale If-Match: %d %s", w.Code, w.Body.String())
	}
	if w := withIfMatch(t, srv, "PUT", "/api/events/"+group.ID+"/"+created.UID, edit, "", cookie); w.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing If-Match: %d", w.Code)
	}
	after, _ := st.Calendars().GetObjectByUID(context.Background(), group.ID, created.UID)
	if string(after.Data) != string(before.Data) || after.ETag != before.ETag {
		t.Fatal("a refused edit changed the stored object")
	}
}

func TestEventWriteRolesAndValidation(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	group := groupCalendar(t, st, "Team")
	reader := loginAs(t, srv, st, "ray", "user")
	editor := loginAs(t, srv, st, "eve", "user")
	grantRole(t, st, group, "reader", "usr_ray")
	grantRole(t, st, group, "editor", "usr_eve")
	ok := `{"title":"A","start":"2026-10-07T09:00:00Z","end":"2026-10-07T10:00:00Z","zone":"UTC"}`
	if w := call(t, srv, "POST", "/api/calendars/"+group.ID+"/events", ok, reader); w.Code != http.StatusForbidden {
		t.Fatalf("reader create %d, want 403", w.Code)
	}
	for _, bad := range []string{
		`{"title":"A","start":"2026-10-07T10:00:00Z","end":"2026-10-07T09:00:00Z","zone":"UTC"}`,
		`{"title":"A","start":"2026-10-07T09:00:00Z","end":"2026-10-07T10:00:00Z"}`,
		`{"title":"A","start":"2026-10-07T09:00:00Z","end":"2026-10-07T10:00:00Z","zone":"Local"}`,
		`{"title":"A","start":"2026-10-07","end":"2026-10-07","all_day":true}`,
		`{"title":"A","start":"2026-10-07T09:00:00Z","end":"2026-10-07T10:00:00Z","zone":"UTC","repeat":{"freq":"hourly"}}`,
		`{"title":"A","start":"2026-10-07T09:00:00Z","end":"2026-10-07T10:00:00Z","zone":"UTC","repeat":{"freq":"daily","weekdays":["MO"]}}`,
		`{"title":"A","start":"2026-10-07T09:00:00Z","end":"2026-10-07T10:00:00Z","zone":"UTC","repeat":{"freq":"custom"}}`,
		`{"title":"` + strings.Repeat("x", 1001) + `","start":"2026-10-07T09:00:00Z","end":"2026-10-07T10:00:00Z","zone":"UTC"}`,
	} {
		if w := call(t, srv, "POST", "/api/calendars/"+group.ID+"/events", bad, editor); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", bad, w.Code)
		}
	}
	if w := withIfMatch(t, srv, "PUT", "/api/events/"+group.ID+"/missing", ok, "x", editor); w.Code != http.StatusNotFound {
		t.Fatalf("missing event %d, want 404", w.Code)
	}
}
```

`withIfMatch` and `eventsIn` take the `*api.Server` from `setupTestServer`; add `"github.com/Busnes-app/kycalendar/internal/api"` to the file's imports.

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/api/ -run 'EventLifecycle|StaleETag|EventWriteRoles'`
Expected: FAIL (routes absent).

- [ ] **Step 3: Implement the write handlers**

Append to `internal/api/events.go` (add `"encoding/json"`, `"slices"`, `"github.com/google/uuid"` and `"github.com/Busnes-app/kycalendar/internal/davbackend"` to the imports):

```go
const (
	maxTitleBytes       = 1000
	maxLocationBytes    = 1000
	maxDescriptionBytes = 65536
)

type eventBody struct {
	Title        string     `json:"title"`
	Location     string     `json:"location"`
	Description  string     `json:"description"`
	Start        string     `json:"start"`
	End          string     `json:"end"`
	AllDay       bool       `json:"all_day"`
	Zone         string     `json:"zone"`
	Repeat       repeatView `json:"repeat"`
	Scope        string     `json:"scope"`
	RecurrenceID string     `json:"recurrence_id"`
}

var errBadEvent = errors.New("bad event")

// eventInput validates a body at the boundary. allowCustom is true for edits, where "custom"
// keeps the stored rule.
func eventInput(b eventBody, allowCustom bool) (calendar.EventInput, error) {
	in := calendar.EventInput{Title: b.Title, Location: b.Location, Description: b.Description, AllDay: b.AllDay}
	switch {
	case len(b.Title) > maxTitleBytes, len(b.Location) > maxLocationBytes, len(b.Description) > maxDescriptionBytes:
		return in, fmt.Errorf("%w: text too long", errBadEvent)
	}
	if b.AllDay {
		s, err1 := time.Parse(time.DateOnly, b.Start)
		e, err2 := time.Parse(time.DateOnly, b.End)
		if err1 != nil || err2 != nil || !e.After(s) {
			return in, fmt.Errorf("%w: all-day start and end must be dates, end after start", errBadEvent)
		}
		in.Start, in.End = s, e
	} else {
		if b.Zone == "" || b.Zone == "Local" {
			return in, fmt.Errorf("%w: zone must be an IANA time zone", errBadEvent)
		}
		loc, err := time.LoadLocation(b.Zone)
		if err != nil {
			return in, fmt.Errorf("%w: zone must be an IANA time zone", errBadEvent)
		}
		s, err1 := time.Parse(time.RFC3339, b.Start)
		e, err2 := time.Parse(time.RFC3339, b.End)
		if err1 != nil || err2 != nil || e.Before(s) {
			return in, fmt.Errorf("%w: start and end must be RFC 3339, end not before start", errBadEvent)
		}
		in.Start, in.End, in.Zone = s, e, loc
	}
	switch b.Repeat.Freq {
	case "", "daily", "monthly", "yearly":
		if len(b.Repeat.Weekdays) > 0 {
			return in, fmt.Errorf("%w: weekdays only apply to weekly", errBadEvent)
		}
	case "weekly":
		for _, code := range b.Repeat.Weekdays {
			i := slices.Index(weekdayCodes, code)
			if i < 0 || slices.Contains(in.Repeat.Weekdays, time.Weekday(i)) {
				return in, fmt.Errorf("%w: weekdays must be distinct MO..SU", errBadEvent)
			}
			in.Repeat.Weekdays = append(in.Repeat.Weekdays, time.Weekday(i))
		}
	case "custom":
		if !allowCustom {
			return in, fmt.Errorf("%w: custom repeat cannot be created here", errBadEvent)
		}
	default:
		return in, fmt.Errorf("%w: unknown repeat", errBadEvent)
	}
	in.Repeat.Freq = b.Repeat.Freq
	return in, nil
}

// ifMatch reads a strong If-Match ETag; ok is false when absent or weak.
func ifMatch(r *http.Request) (string, bool) {
	v := strings.TrimSpace(r.Header.Get("If-Match"))
	if v == "" || strings.HasPrefix(v, "W/") || v == "*" {
		return "", false
	}
	return strings.Trim(v, `"`), true
}

// writeEvent stores cal through the one write path and maps its errors to JSON.
func (s *Server) writeEvent(w http.ResponseWriter, r *http.Request, c *store.Calendar, name string, cal *ical.Calendar, etag string, create bool) (*store.CalendarObject, bool) {
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		s.writeError(w, http.StatusUnprocessableEntity, "The event cannot be encoded")
		return nil, false
	}
	o, err := davbackend.Write(r.Context(), s.store, c.ID, name, cal, buf.Bytes(), etag, create, s.objectLimits())
	switch {
	case err == nil:
		return o, true
	case errors.Is(err, store.ErrPreconditionFailed):
		s.writeJSON(w, http.StatusPreconditionFailed, map[string]string{"error": "The event changed elsewhere; reload it", "code": "conflict"})
	case errors.Is(err, store.ErrUIDConflict):
		s.writeError(w, http.StatusConflict, "An event with this UID already exists")
	case errors.Is(err, store.ErrQuotaExceeded):
		s.writeJSON(w, http.StatusInsufficientStorage, map[string]string{"error": "Calendar storage is full", "code": "quota"})
	case errors.Is(err, davbackend.ErrInvalidResource), errors.Is(err, calendar.ErrInvalidData), errors.Is(err, calendar.ErrUnsupportedComponent):
		s.writeError(w, http.StatusUnprocessableEntity, "The event is not valid")
	default:
		s.writeError(w, http.StatusInternalServerError, "Failed to save the event")
	}
	return nil, false
}

func (s *Server) handleCreateEvent(w http.ResponseWriter, r *http.Request) {
	c, role := s.calendarFor(w, r, r.PathValue("id"))
	if c == nil {
		return
	}
	if !role.CanWrite() {
		s.writeError(w, http.StatusForbidden, "This calendar is read-only for you")
		return
	}
	var b eventBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	in, err := eventInput(b, false)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	uid := uuid.NewString()
	o, ok := s.writeEvent(w, r, c, uid+".ics", calendar.NewEvent(uid, in, time.Now()), "", true)
	if !ok {
		return
	}
	s.writeJSON(w, http.StatusCreated, map[string]string{"calendar_id": c.ID, "uid": uid, "etag": o.ETag})
}

// loadEvent authorizes a write and loads the event's object and decoded calendar.
func (s *Server) loadEvent(w http.ResponseWriter, r *http.Request) (*store.Calendar, *store.CalendarObject, *ical.Calendar, string, bool) {
	c, role := s.calendarFor(w, r, r.PathValue("cal"))
	if c == nil {
		return nil, nil, nil, "", false
	}
	if !role.CanWrite() {
		s.writeError(w, http.StatusForbidden, "This calendar is read-only for you")
		return nil, nil, nil, "", false
	}
	etag, ok := ifMatch(r)
	if !ok {
		s.writeError(w, http.StatusPreconditionRequired, "If-Match with the event's ETag is required")
		return nil, nil, nil, "", false
	}
	o, err := s.store.Calendars().GetObjectByUID(r.Context(), c.ID, r.PathValue("uid"))
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "No such event")
		return nil, nil, nil, "", false
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to load the event")
		return nil, nil, nil, "", false
	}
	cal, err := ical.NewDecoder(bytes.NewReader(o.Data)).Decode()
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "The stored event does not parse")
		return nil, nil, nil, "", false
	}
	return c, o, cal, etag, true
}

func (s *Server) editError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, calendar.ErrNotRecurring):
		s.writeError(w, http.StatusBadRequest, "This event does not repeat")
	case errors.Is(err, calendar.ErrNoSuchOccurrence):
		s.writeError(w, http.StatusBadRequest, "No such occurrence")
	default:
		s.writeError(w, http.StatusUnprocessableEntity, "The stored event cannot be edited")
	}
}

func (s *Server) handleUpdateEvent(w http.ResponseWriter, r *http.Request) {
	var b eventBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	c, o, cal, etag, ok := s.loadEvent(w, r)
	if !ok {
		return
	}
	in, err := eventInput(b, true)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	switch b.Scope {
	case "", "all":
		err = calendar.EditAll(cal, in, time.Now())
	case "this":
		err = calendar.EditOne(cal, b.RecurrenceID, in, time.Now())
	default:
		s.writeError(w, http.StatusBadRequest, "scope must be this or all")
		return
	}
	if err != nil {
		s.editError(w, err)
		return
	}
	if saved, ok := s.writeEvent(w, r, c, o.Name, cal, etag, false); ok {
		s.writeJSON(w, http.StatusOK, map[string]string{"etag": saved.ETag})
	}
}

func (s *Server) handleDeleteEvent(w http.ResponseWriter, r *http.Request) {
	c, o, cal, etag, ok := s.loadEvent(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	switch q.Get("scope") {
	case "", "all":
		err := s.store.Calendars().DeleteObject(r.Context(), c.ID, o.Name, etag)
		switch {
		case errors.Is(err, store.ErrPreconditionFailed):
			s.writeJSON(w, http.StatusPreconditionFailed, map[string]string{"error": "The event changed elsewhere; reload it", "code": "conflict"})
		case errors.Is(err, store.ErrNotFound):
			s.writeError(w, http.StatusNotFound, "No such event")
		case err != nil:
			s.writeError(w, http.StatusInternalServerError, "Failed to delete the event")
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	case "this":
		if err := calendar.DeleteOne(cal, q.Get("recurrence_id"), time.Now()); err != nil {
			s.editError(w, err)
			return
		}
		if saved, ok := s.writeEvent(w, r, c, o.Name, cal, etag, false); ok {
			s.writeJSON(w, http.StatusOK, map[string]string{"etag": saved.ETag})
		}
	default:
		s.writeError(w, http.StatusBadRequest, "scope must be this or all")
	}
}
```

Add `"fmt"` to the imports for `eventInput`. Note the ordering in `handleUpdateEvent`: `loadEvent` runs the 404/403/428 checks before the body is validated, so an unauthorized caller learns nothing from validation errors. The body is decoded first only because `r.Body` must be read once. Malformed JSON answers 400 before authorization, which reveals nothing about the calendar.

- [ ] **Step 4: Routes and matrix rows**

In `routes()`:

```go
	s.handle("POST /api/calendars/{id}/events", s.requireEveryday(s.handleCreateEvent))
	s.handle("PUT /api/events/{cal}/{uid}", s.requireEveryday(s.handleUpdateEvent))
	s.handle("DELETE /api/events/{cal}/{uid}", s.requireEveryday(s.handleDeleteEvent))
```

In `internal/api/authz_matrix_test.go`, add the expectation set:

```go
	groupWrite = expect{anon: 401, deactivated: 401, admin: 403, editor: allow, manager: allow, reader: 403, owner: 404, nonmember: 404}
```

and the rows. The matrix's `call` sends no `If-Match`, so writers get 428, which counts as let through, and nothing is changed or deleted:

```go
		"POST /api/calendars/{id}/events": {method: "POST", path: "/api/calendars/" + w.group.ID + "/events",
			body: `{"title":"M","start":"2026-10-07T09:00:00Z","end":"2026-10-07T10:00:00Z","zone":"UTC"}`, want: groupWrite},
		"PUT /api/events/{cal}/{uid}": {method: "PUT", path: "/api/events/" + w.group.ID + "/seed.ics",
			body: `{"title":"M","start":"2026-10-07T09:00:00Z","end":"2026-10-07T10:00:00Z","zone":"UTC"}`, want: groupWrite},
		"DELETE /api/events/{cal}/{uid}": {method: "DELETE", path: "/api/events/" + w.group.ID + "/seed.ics?scope=all", want: groupWrite},
```

The matrix world seeds `seed.ics` with UID `seed.ics` (Plan 2's `put` helper uses the name as the UID).

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/api/` then `make ci`
Expected: PASS.

- [ ] **Step 6: DOX**

Add to the route table in `internal/api/AGENTS.md`:

```markdown
| POST | `/api/calendars/{id}/events` | everyday; editor or owner | event body → 201 `{calendar_id,uid,etag}`; 400 invalid; 403 reader; 404 cannot read |
| PUT | `/api/events/{cal}/{uid}` | everyday; editor or owner; `If-Match` | body + `scope` (`all`/`this`) + `recurrence_id` → 200 `{etag}`; 428 no `If-Match`; 412 `conflict`; 400 invalid, not recurring or no such occurrence |
| DELETE | `/api/events/{cal}/{uid}?scope&recurrence_id` | everyday; editor or owner; `If-Match` | `all` → 204; `this` → 200 `{etag}`; 428, 412 as above |

- Event writes decode the stored object, apply `calendar.NewEvent`/`EditAll`/`EditOne`/`DeleteOne`, encode, and store through `davbackend.Write` with the request's `If-Match` (strong ETags only; `*` and weak are refused), so web edits get CalDAV's validation, ETag check, quotas and change log. Authorization (404/403) and `If-Match` (428) come before body validation.
```

- [ ] **Step 7: Commit**

```bash
git add internal/api
git commit -m "feat(api): create, edit and delete events through the CalDAV write path"
```

---

### Task 8: Root contracts

**Files:**
- Modify: `AGENTS.md` (`## KyCalendar`), `docs/superpowers/specs/2026-10-06-kycalendar-v1a-design.md` (section 3, the "Unproven" sentence only)

**Interfaces:** none.

- [ ] **Step 1: Root `AGENTS.md`**

After `#### Plan 2 access contracts`, add:

```markdown
#### Plan 3 recurrence and event API contracts

- Recurrence is `internal/calendar.Expand` over `rrule-go`; go-ical decodes and encodes only (its `RecurrenceSet` drops RDATE and fails on EXDATE lists and non-IANA TZIDs). TZIDs resolve through `calendar.ResolveZone`: IANA, CLDR Windows names (release-48-2, generated and hash-pinned), Mozilla paths; unresolved zones show in UTC and are flagged.
- One write path: CalDAV PUT and every JSON event write call `davbackend.Write`. Web edits are `calendar.NewEvent`/`EditAll`/`EditOne`/`DeleteOne`, which mutate the stored object in place so unknown properties survive.
- `GET /api/events` returns at most 5000 instances over at most 400 days (422 `too_many_instances`, 400 for a bad range); event `PUT`/`DELETE` require a strong `If-Match` (428 without, 412 on conflict).
- Plan 3b builds the FullCalendar UI on this API; Plan 4's interop gate records real client exports.
```

- [ ] **Step 2: Spec**

In `docs/superpowers/specs/2026-10-06-kycalendar-v1a-design.md` section 3, replace the sentence beginning "Working assumption is go-ical's recurrence helper over `rrule-go`. **Unproven:**" with: "Spiked 2026-10-07: go-ical's recurrence helper is unusable (drops RDATE, fails on EXDATE lists and non-IANA TZIDs); expansion is KyCalendar's own over `rrule-go`, which handles DST, UNTIL and EXDATE correctly. Recorded Apple, Google and Outlook exports remain part of the interop gate."

- [ ] **Step 3: Verify and commit**

Run: `make ci`
Expected: PASS.

```bash
git add AGENTS.md docs/superpowers/specs/2026-10-06-kycalendar-v1a-design.md
git commit -m "docs: record the Plan 3 recurrence and event API contracts"
```

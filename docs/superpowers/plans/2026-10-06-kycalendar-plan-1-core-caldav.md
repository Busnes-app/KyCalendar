# KyCalendar Plan 1: Core CalDAV Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A KyCalendar server, scaffolded from `ky_server_base`, where a signed-in everyday user creates an app password and syncs a personal calendar with a native CalDAV client.

**Architecture:** Go server from `ky_server_base` (session auth, SCIM, store, web shell). CalDAV comes from a vendored fork of `emersion/go-webdav` v0.7.0 in `third_party/go-webdav`, patched for colour, ctag, sync tokens, read-only privileges, raw-byte storage, PROPPATCH and MKCALENDAR. A per-request `davbackend.Backend` maps CalDAV onto a `CalendarStore` that keeps raw iCalendar bytes, a per-calendar sequence and a change log. Native clients authenticate with per-device app passwords over HTTP Basic.

**Tech Stack:** Go 1.26.6, `net/http`, `modernc.org/sqlite` (+ pgx via the base), `github.com/emersion/go-webdav` (fork), `github.com/emersion/go-ical`, `github.com/teambition/rrule-go`, React 19 + Vite + vitest.

**Spec:** `docs/superpowers/specs/2026-10-06-kycalendar-v1a-design.md`

This is Plan 1 of 4 for v1a. Plan 2: group calendars, grants, admin separation from the `roles` claim, authorization matrix. Plan 3: recurrence spike, JSON API, FullCalendar UI. Plan 4: KyRecovery backup, `restore` with sync-epoch reset, interop gate evidence.

## Global Constraints

- Go `1.26.6` (base `go.mod`); module `github.com/Busnes-app/kycalendar`; binary `kycalendar`.
- Env prefix stays `KY_` (suite convention).
- Fork lives in `third_party/go-webdav`, module path unchanged, wired with a `replace`; keep its MIT `LICENSE`.
- Calendar object size limit: `1 << 20` bytes (`calendar.MaxObjectSize`).
- Components: VEVENT only; advertise `supported-calendar-component-set` = VEVENT.
- Raw iCalendar bytes are stored and served unchanged.
- Change rows older than 90 days are pruned; older tokens get `DAV:valid-sync-token`.
- App passwords: at most 20 per user; token `kc_<id>_<secret>` with a 256-bit secret, stored as SHA-256 hex.
- DAV auth failures: 10 per IP and 50 per username per 15 minutes, then 429 with `Retry-After`.
- Users with `Role == "admin"` get 403 on every CalDAV route and on app-password routes (Plan 2 moves this to the `roles` claim).
- DAV paths: principal `/dav/<user-id>/`, home `/dav/<user-id>/calendars/`, calendar `/dav/<user-id>/calendars/<slug>/`, object `/dav/<user-id>/calendars/<slug>/<name>`.
- Logs and audit rows carry IDs only: never passwords, tokens or event content.
- Personal calendar deletion over CalDAV is refused (403) in Plan 1.
- Per-user object quota: `KY_CALENDAR_MAX_OBJECTS_PER_USER`, default 20000; over quota is 507.

## Review Focus

1. **Outlook-style TZID** (`DTSTART;TZID=Eastern Standard Time:…`): PUT is accepted and the event still appears in a time-range query around its date. Test: Task 7 `TestInspectUnknownTZIDWidens`, Task 11 `TestCalDAVUnknownTZIDQueryable`.
2. **Another user's path** with valid credentials (`/dav/usr_bob/…` as alice): 403 and no data. Test: Task 11 `TestCalDAVOtherUsersPathForbidden`.
3. **Stale, foreign or garbage sync token**: 403 `valid-sync-token`, never a silent empty diff. Test: Task 8 `TestChangesSinceExpired`, Task 11 `TestCalDAVSyncBadToken`.
4. **Conflicting edits**: PUT with a stale `If-Match`, or `If-None-Match: *` on an existing name, is 412 and changes nothing. Test: Task 8 `TestPutObjectPreconditions`, Task 11 `TestCalDAVStaleETag`.
5. **Deactivated user with a configured phone**: the next request is 401 even though the password row was not yet deleted. Test: Task 10 `TestDAVAuthInactiveUser`.

---

## File Structure

| Path | Responsibility |
|---|---|
| `third_party/go-webdav/` | Vendored fork; `FORK.md` lists patches |
| `third_party/go-webdav/caldav/caldav.go` | `Calendar`, `CalendarObject` gain fields; `SyncBackend`, `CalendarUpdater`, `CalendarUpdate`, `SyncResult`, `ErrInvalidSyncToken` |
| `third_party/go-webdav/caldav/elements.go` | New XML names and prop types |
| `third_party/go-webdav/caldav/server.go` | PROPFIND props, raw bytes, size limit, sync-collection, PROPPATCH, MKCALENDAR |
| `third_party/go-webdav/caldav/fork_test.go` | Tests for every fork patch |
| `internal/calendar/object.go` | Pure: validate a decoded calendar, compute UID and index bounds |
| `internal/calendar/synctoken.go` | Pure: format and parse sync tokens |
| `internal/store/calendars.go` | `CalendarStore` SQL implementation |
| `internal/store/apppasswords.go` | `AppPasswordStore` SQL implementation |
| `internal/store/migrations/migrations.go` | Migrations v6 (calendars) and v7 (app passwords) |
| `internal/apppass/apppass.go` | Pure: token generate, parse, hash, compare |
| `internal/davbackend/backend.go` | `caldav.Backend` + `SyncBackend` + `CalendarUpdater` over the store, per request |
| `internal/api/dav.go` | Mount `/dav/`, `/.well-known/caldav`; per-request handler |
| `internal/api/dav_auth.go` | Basic auth with app passwords, failure limiter |
| `internal/api/app_passwords.go` | `/api/app-passwords` handlers |
| `web/src/pages/AppPasswords.tsx` | Create, list, revoke app passwords; setup steps |

---

### Task 1: Scaffold KyCalendar from ky_server_base

**Files:**
- Create: everything copied from `ky_server_base` except root `AGENTS.md` and `LICENSE.txt`
- Modify: `AGENTS.md` (KyCalendar root), `Makefile`, `Dockerfile`, `.gitignore`, `.dockerignore`, `docker-compose.yml`, `internal/config/config.go`

**Interfaces:**
- Produces: module `github.com/Busnes-app/kycalendar`; `make ci` green; SQLite file `kycalendar.db`.

- [ ] **Step 1: Scaffold into a temp dir** (the script refuses non-empty targets, and KyCalendar already holds `AGENTS.md`, `LICENSE.txt`, `docs/`)

```bash
rm -rf /tmp/kycalendar-scaffold
bash /home/yoshi/git/busnes.app/ky_server_base/scripts/ky-init.sh kycalendar /tmp/kycalendar-scaffold
```
Expected: ends with `Successfully scaffolded kycalendar`.

- [ ] **Step 2: Copy into the repo without clobbering the root contract or licence**

```bash
rsync -a --exclude='/AGENTS.md' --exclude='/LICENSE.txt' /tmp/kycalendar-scaffold/ /home/yoshi/git/busnes.app/KyCalendar/
```

- [ ] **Step 3: Rename what `ky-init.sh` misses**

```bash
cd /home/yoshi/git/busnes.app/KyCalendar
sed -i 's/ky_server_base/kycalendar/g' Makefile Dockerfile .gitignore .dockerignore
sed -i 's|ghcr.io/busnes-app/ky-server-base|ghcr.io/busnes-app/kycalendar|g' docker-compose.yml
sed -i 's/ky_server\.db/kycalendar.db/' internal/config/config.go
grep -rn 'ky_server_base\|ky-server-base\|ky_server\.db' --exclude-dir=node_modules --exclude-dir=.git --exclude-dir=docs .
```
Expected: the final grep prints only hits inside nested `AGENTS.md`/`README.md` prose. Fix each by hand to say `kycalendar`. Leave `KY_` env names and the Postgres DB name `ky_server` alone (Plan 4 decides capsule naming).

- [ ] **Step 4: Merge the base root contract into KyCalendar's `AGENTS.md`**

Read `/tmp/kycalendar-scaffold/AGENTS.md`. Under the existing `## KyCalendar` section of `/home/yoshi/git/busnes.app/KyCalendar/AGENTS.md`, add a `### Server contracts` subsection containing the base file's product-specific sections (everything not already present verbatim in the suite text above it), and its `Child DOX Index` entries rewritten as repo-relative paths (the base file has stale `file:///home/yoshi/git/ky_server_base/...` links). Do not duplicate the suite DOX or KyRecovery text.

- [ ] **Step 5: Build and run CI**

```bash
make ci
```
Expected: PASS (tidy-check, lint, test-race, test-web, smoke). If `web/dist` is missing, run `make all` first.

- [ ] **Step 6: Commit**

```bash
git add -A . ':!AGENTS.md' ':!LICENSE.txt'
git add AGENTS.md LICENSE.txt
git commit -m "chore: scaffold kycalendar from ky_server_base"
```
(Yoshi authored `AGENTS.md` and `LICENSE.txt`; Step 4 edited `AGENTS.md`, so it is committed here deliberately. Confirm with Yoshi before this first commit of those two files.)

---

### Task 2: Vendor go-webdav v0.7.0 as a local fork

**Files:**
- Create: `third_party/go-webdav/` (copy), `third_party/go-webdav/FORK.md`
- Modify: `go.mod`, `Makefile`

**Interfaces:**
- Produces: `github.com/emersion/go-webdav` resolves to `./third_party/go-webdav`; `make test-fork`.

- [ ] **Step 1: Copy the module and make it writable**

```bash
cd /home/yoshi/git/busnes.app/KyCalendar
go mod download github.com/emersion/go-webdav@v0.7.0
mkdir -p third_party
cp -r "$(go env GOMODCACHE)/github.com/emersion/go-webdav@v0.7.0" third_party/go-webdav
chmod -R u+w third_party/go-webdav
```

- [ ] **Step 2: Wire the replace**

```bash
go mod edit -require=github.com/emersion/go-webdav@v0.7.0 -replace=github.com/emersion/go-webdav=./third_party/go-webdav
```

- [ ] **Step 3: Write `third_party/go-webdav/FORK.md`**

```markdown
# go-webdav fork

Upstream: github.com/emersion/go-webdav v0.7.0 (MIT, see LICENSE). Module path unchanged;
KyCalendar wires it with a `replace` in its go.mod.

Patches (each one is a candidate upstream PR):

1. Calendar colour, getctag, sync-token, supported-report-set and per-calendar read-only privileges.
2. Raw iCalendar bytes on PUT and GET; max-resource-size precondition.
3. RFC 6578 sync-collection REPORT through an optional SyncBackend.
4. PROPPATCH through an optional CalendarUpdater; MKCALENDAR.
```

- [ ] **Step 4: Add the fork's tests to CI**

In `Makefile`, add the target and put it in `ci`:

```makefile
test-fork:
	go -C third_party/go-webdav test ./...
```
and change the `ci:` line to `ci: tidy-check lint test-race test-fork test-web smoke`.

- [ ] **Step 5: Run the fork's own tests**

Run: `make test-fork`
Expected: PASS (upstream tests unchanged).

- [ ] **Step 6: Commit**

```bash
git add third_party/go-webdav go.mod go.sum Makefile
git commit -m "chore: vendor go-webdav v0.7.0 as a local fork"
```

---

### Task 3: Fork patch 1 — calendar properties and read-only privileges

**Files:**
- Modify: `third_party/go-webdav/caldav/caldav.go` (`Calendar` struct)
- Modify: `third_party/go-webdav/caldav/elements.go`
- Modify: `third_party/go-webdav/caldav/server.go` (`propFindCalendar`)
- Test: `third_party/go-webdav/caldav/fork_test.go`

**Interfaces:**
- Produces: `caldav.Calendar{…, Color string, CTag string, SyncToken string, ReadOnly bool}`.

- [ ] **Step 1: Write the failing test** in `third_party/go-webdav/caldav/fork_test.go`

```go
package caldav

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func propfind(t *testing.T, h *Handler, path, body string) string {
	t.Helper()
	req := httptest.NewRequest("PROPFIND", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/xml")
	req.Header.Set("Depth", "0")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	data, _ := io.ReadAll(w.Result().Body)
	return string(data)
}

const propfindCalendarProps = `<d:propfind xmlns:d="DAV:" xmlns:cs="http://calendarserver.org/ns/" xmlns:ic="http://apple.com/ns/ical/">
  <d:prop><ic:calendar-color/><cs:getctag/><d:sync-token/><d:current-user-privilege-set/><d:supported-report-set/></d:prop>
</d:propfind>`

func TestForkCalendarProps(t *testing.T) {
	cal := Calendar{Path: "/user/calendars/cal/", Color: "#ff8800", CTag: "42", SyncToken: "urn:test:7"}
	h := &Handler{Backend: testBackend{calendars: []Calendar{cal}}}
	resp := propfind(t, h, cal.Path, propfindCalendarProps)
	for _, want := range []string{"#ff8800", ">42<", "urn:test:7", "sync-collection", "calendar-multiget", "write"} {
		if !strings.Contains(resp, want) {
			t.Errorf("missing %q in:\n%s", want, resp)
		}
	}
}

func TestForkReadOnlyCalendarHasNoWrite(t *testing.T) {
	cal := Calendar{Path: "/user/calendars/cal/", ReadOnly: true}
	h := &Handler{Backend: testBackend{calendars: []Calendar{cal}}}
	resp := propfind(t, h, cal.Path, propfindCalendarProps)
	if !strings.Contains(resp, "read") || strings.Contains(resp, "write") {
		t.Errorf("read-only calendar privileges wrong:\n%s", resp)
	}
	if strings.Contains(resp, "sync-collection") {
		t.Errorf("sync-collection advertised without a sync token:\n%s", resp)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go -C third_party/go-webdav test ./caldav/ -run TestFork`
Expected: FAIL to compile (`unknown field Color`).

- [ ] **Step 3: Add the fields** to `Calendar` in `caldav/caldav.go`

```go
type Calendar struct {
	Path                  string
	Name                  string
	Description           string
	MaxResourceSize       int64
	SupportedComponentSet []string
	// Fork: served as http://apple.com/ns/ical/ calendar-color when set.
	Color string
	// Fork: served as http://calendarserver.org/ns/ getctag when set.
	CTag string
	// Fork: served as DAV:sync-token when set; also advertises sync-collection.
	SyncToken string
	// Fork: current-user-privilege-set omits write when true.
	ReadOnly bool
}
```

- [ ] **Step 4: Add XML names and types** to `caldav/elements.go`

```go
var (
	calendarColorName      = xml.Name{"http://apple.com/ns/ical/", "calendar-color"}
	getCTagName            = xml.Name{"http://calendarserver.org/ns/", "getctag"}
	syncTokenName          = xml.Name{"DAV:", "sync-token"}
	supportedReportSetName = xml.Name{"DAV:", "supported-report-set"}
	syncCollectionName     = xml.Name{"DAV:", "sync-collection"}
	mkcalendarName         = xml.Name{namespace, "mkcalendar"}
)

type calendarColor struct {
	XMLName xml.Name `xml:"http://apple.com/ns/ical/ calendar-color"`
	Color   string   `xml:",chardata"`
}

type getCTag struct {
	XMLName xml.Name `xml:"http://calendarserver.org/ns/ getctag"`
	CTag    string   `xml:",chardata"`
}

type syncTokenProp struct {
	XMLName xml.Name `xml:"DAV: sync-token"`
	Token   string   `xml:",chardata"`
}

func supportedReportSet(reports ...xml.Name) *internal.RawXMLValue {
	var children []internal.RawXMLValue
	for _, name := range reports {
		report := internal.NewRawXMLElement(xml.Name{"DAV:", "report"}, nil,
			[]internal.RawXMLValue{*internal.NewRawXMLElement(name, nil, nil)})
		children = append(children, *internal.NewRawXMLElement(xml.Name{"DAV:", "supported-report"}, nil,
			[]internal.RawXMLValue{*report}))
	}
	return internal.NewRawXMLElement(supportedReportSetName, nil, children)
}
```
(`elements.go` already imports `encoding/xml` and `internal`; add the import if missing.)

- [ ] **Step 5: Serve them** in `propFindCalendar` in `caldav/server.go`. Replace the `internal.CurrentUserPrivilegeSetName` entry in the `props` map with:

```go
		internal.CurrentUserPrivilegeSetName: internal.PropFindValue(calendarPrivileges(cal.ReadOnly)),
```
and after the existing `if cal.MaxResourceSize > 0 {…}` block add:

```go
	if cal.Color != "" {
		props[calendarColorName] = internal.PropFindValue(&calendarColor{Color: cal.Color})
	}
	if cal.CTag != "" {
		props[getCTagName] = internal.PropFindValue(&getCTag{CTag: cal.CTag})
	}
	reports := []xml.Name{calendarQueryName, calendarMultigetName}
	if cal.SyncToken != "" {
		props[syncTokenName] = internal.PropFindValue(&syncTokenProp{Token: cal.SyncToken})
		reports = append(reports, syncCollectionName)
	}
	props[supportedReportSetName] = internal.PropFindValue(supportedReportSet(reports...))
```
and add the helper at file scope:

```go
func calendarPrivileges(readOnly bool) *internal.CurrentUserPrivilegeSet {
	privs := []internal.Privilege{{Read: &struct{}{}}}
	if !readOnly {
		privs = append(privs, internal.Privilege{Write: &struct{}{}})
	}
	return &internal.CurrentUserPrivilegeSet{Privilege: privs}
}
```

- [ ] **Step 6: Run the tests**

Run: `go -C third_party/go-webdav test ./caldav/`
Expected: PASS (new and upstream tests).

- [ ] **Step 7: Commit**

```bash
git add third_party/go-webdav/caldav
git commit -m "feat(fork): serve calendar colour, ctag, sync-token and read-only privileges"
```

---

### Task 4: Fork patch 2 — raw bytes and the size limit

**Files:**
- Modify: `third_party/go-webdav/caldav/caldav.go` (`CalendarObject`)
- Modify: `third_party/go-webdav/caldav/server.go` (`PutCalendarObjectOptions`, `Handler`, `backend`, `Put`, `HeadGet`, `propFindCalendarObject`, every `backend{…}` literal)
- Test: `third_party/go-webdav/caldav/fork_test.go`

**Interfaces:**
- Produces: `CalendarObject.Raw []byte`; `PutCalendarObjectOptions.Raw []byte`; `Handler.MaxResourceSize int64` (0 = unlimited).

- [ ] **Step 1: Write the failing tests** (append to `fork_test.go`; add imports `bytes`, `context`, `fmt`, `github.com/emersion/go-ical`)

```go
const rawEvent = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Test//EN\r\nBEGIN:VEVENT\r\nUID:raw-1\r\nDTSTAMP:20261006T120000Z\r\nDTSTART:20261007T090000Z\r\nX-APPLE-ODD;X-P=1:keep  this\r\nSUMMARY:Raw\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

type rawBackend struct {
	testBackend
	putRaw []byte
}

func (b *rawBackend) PutCalendarObject(ctx context.Context, path string, cal *ical.Calendar, opts *PutCalendarObjectOptions) (*CalendarObject, error) {
	b.putRaw = opts.Raw
	return &CalendarObject{Path: path, ETag: "e1"}, nil
}

func (b *rawBackend) GetCalendarObject(ctx context.Context, path string, req *CalendarCompRequest) (*CalendarObject, error) {
	cal, err := ical.NewDecoder(bytes.NewReader([]byte(rawEvent))).Decode()
	if err != nil {
		return nil, err
	}
	return &CalendarObject{Path: path, ETag: "e1", Data: cal, Raw: []byte(rawEvent)}, nil
}

func TestForkPutPassesRawBytes(t *testing.T) {
	b := &rawBackend{}
	h := &Handler{Backend: b, MaxResourceSize: 1 << 20}
	req := httptest.NewRequest("PUT", "/user/calendars/cal/raw-1.ics", strings.NewReader(rawEvent))
	req.Header.Set("Content-Type", "text/calendar")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 201 {
		t.Fatalf("PUT: %d %s", w.Code, w.Body.String())
	}
	if string(b.putRaw) != rawEvent {
		t.Fatalf("raw bytes changed:\n%q", b.putRaw)
	}
}

func TestForkGetServesRawBytes(t *testing.T) {
	h := &Handler{Backend: &rawBackend{}}
	req := httptest.NewRequest("GET", "/user/calendars/cal/raw-1.ics", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Body.String() != rawEvent {
		t.Fatalf("GET body differs:\n%q", w.Body.String())
	}
	if w.Header().Get("Content-Length") != fmt.Sprint(len(rawEvent)) {
		t.Fatalf("Content-Length %q", w.Header().Get("Content-Length"))
	}
}

func TestForkPutOverLimit(t *testing.T) {
	h := &Handler{Backend: &rawBackend{}, MaxResourceSize: 64}
	req := httptest.NewRequest("PUT", "/user/calendars/cal/raw-1.ics", strings.NewReader(rawEvent))
	req.Header.Set("Content-Type", "text/calendar")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "max-resource-size") {
		t.Fatalf("want max-resource-size precondition, got %d %s", w.Code, w.Body.String())
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go -C third_party/go-webdav test ./caldav/ -run TestFork`
Expected: FAIL to compile (`unknown field Raw`, `unknown field MaxResourceSize`).

- [ ] **Step 3: Add fields**

In `caldav/caldav.go`, add to `CalendarObject`:
```go
	// Fork: when set, served byte-for-byte instead of re-encoding Data.
	Raw []byte
```
In `caldav/server.go`, add to `PutCalendarObjectOptions`:
```go
	// Fork: the request body exactly as received.
	Raw []byte
```
Add to `Handler` and to the unexported `backend` struct:
```go
	// Fork: maximum PUT body size in bytes; 0 means unlimited.
	MaxResourceSize int64
```
Update every `backend{Backend: …, Prefix: …}` literal in `server.go` to also set `MaxResourceSize: h.MaxResourceSize`.

- [ ] **Step 4: Read the body once in `Put`.** Replace the block from `// TODO: check CALDAV:max-resource-size precondition` through the decode error check with:

```go
	body := io.Reader(r.Body)
	if b.MaxResourceSize > 0 {
		body = io.LimitReader(r.Body, b.MaxResourceSize+1)
	}
	raw, err := io.ReadAll(body)
	if err != nil {
		return internal.HTTPErrorf(http.StatusBadRequest, "caldav: failed to read body: %v", err)
	}
	if b.MaxResourceSize > 0 && int64(len(raw)) > b.MaxResourceSize {
		return NewPreconditionError(PreconditionMaxResourceSize)
	}
	cal, err := ical.NewDecoder(bytes.NewReader(raw)).Decode()
	if err != nil {
		return NewPreconditionError(PreconditionValidCalendarData)
	}
	opts.Raw = raw
```
(add `io` to imports).

- [ ] **Step 5: Serve raw bytes.** In `HeadGet`, replace the Content-Length block and the final encode:

```go
	if co.Raw != nil {
		w.Header().Set("Content-Length", strconv.Itoa(len(co.Raw)))
	} else if co.ContentLength > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(co.ContentLength, 10))
	}
```
and
```go
	if r.Method != http.MethodHead {
		if co.Raw != nil {
			_, err := w.Write(co.Raw)
			return err
		}
		return ical.NewEncoder(w).Encode(co.Data)
	}
```
In `propFindCalendarObject`, change the `calendarDataName` func body to:
```go
			if co.Raw != nil {
				return &calendarDataResp{Data: co.Raw}, nil
			}
			var buf bytes.Buffer
			if err := ical.NewEncoder(&buf).Encode(co.Data); err != nil {
				return nil, err
			}
			return &calendarDataResp{Data: buf.Bytes()}, nil
```

- [ ] **Step 6: Run the tests**

Run: `go -C third_party/go-webdav test ./caldav/`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add third_party/go-webdav/caldav
git commit -m "feat(fork): store and serve raw iCalendar bytes; enforce max-resource-size"
```

---

### Task 5: Fork patch 3 — sync-collection REPORT

**Files:**
- Modify: `third_party/go-webdav/caldav/caldav.go`, `caldav/elements.go` (`reportReq`), `caldav/server.go` (`handleReport`)
- Test: `third_party/go-webdav/caldav/fork_test.go`

**Interfaces:**
- Produces:
```go
type SyncBackend interface {
	SyncCalendar(ctx context.Context, path, syncToken string) (*SyncResult, error)
}
type SyncResult struct {
	SyncToken string
	Updated   []CalendarObject
	Deleted   []string // object paths
}
var ErrInvalidSyncToken error
```

- [ ] **Step 1: Write the failing tests** (append)

```go
type syncBackend struct {
	rawBackend
	gotToken string
}

func (b *syncBackend) SyncCalendar(ctx context.Context, path, token string) (*SyncResult, error) {
	b.gotToken = token
	if token == "bad" {
		return nil, ErrInvalidSyncToken
	}
	obj, _ := b.GetCalendarObject(ctx, path+"raw-1.ics", nil)
	return &SyncResult{SyncToken: "urn:test:9", Updated: []CalendarObject{*obj}, Deleted: []string{path + "gone.ics"}}, nil
}

func syncReport(t *testing.T, h *Handler, token string) *httptest.ResponseRecorder {
	t.Helper()
	body := `<d:sync-collection xmlns:d="DAV:"><d:sync-token>` + token + `</d:sync-token><d:sync-level>1</d:sync-level><d:prop><d:getetag/></d:prop></d:sync-collection>`
	req := httptest.NewRequest("REPORT", "/user/calendars/cal/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/xml")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestForkSyncCollection(t *testing.T) {
	b := &syncBackend{}
	w := syncReport(t, &Handler{Backend: b}, "urn:test:7")
	if w.Code != 207 {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	resp := w.Body.String()
	for _, want := range []string{"urn:test:9", "raw-1.ics", "e1", "gone.ics", "404"} {
		if !strings.Contains(resp, want) {
			t.Errorf("missing %q in:\n%s", want, resp)
		}
	}
	if b.gotToken != "urn:test:7" {
		t.Errorf("token passed %q", b.gotToken)
	}
}

func TestForkSyncCollectionInvalidToken(t *testing.T) {
	w := syncReport(t, &Handler{Backend: &syncBackend{}}, "bad")
	if w.Code != 403 || !strings.Contains(w.Body.String(), "valid-sync-token") {
		t.Fatalf("want 403 valid-sync-token, got %d %s", w.Code, w.Body.String())
	}
}

func TestForkSyncCollectionUnsupported(t *testing.T) {
	w := syncReport(t, &Handler{Backend: &rawBackend{}}, "")
	if w.Code != 403 {
		t.Fatalf("want 403 for backend without SyncBackend, got %d", w.Code)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go -C third_party/go-webdav test ./caldav/ -run TestForkSync`
Expected: FAIL to compile (`undefined: SyncResult`).

- [ ] **Step 3: Add the types** to `caldav/caldav.go` (add `context` and `errors` imports)

```go
// SyncBackend is implemented by backends that support RFC 6578 sync-collection.
type SyncBackend interface {
	SyncCalendar(ctx context.Context, path, syncToken string) (*SyncResult, error)
}

// SyncResult lists changes since a token. Deleted holds object paths.
type SyncResult struct {
	SyncToken string
	Updated   []CalendarObject
	Deleted   []string
}

// ErrInvalidSyncToken makes the server answer 403 DAV:valid-sync-token.
var ErrInvalidSyncToken = errors.New("caldav: invalid sync token")
```

- [ ] **Step 4: Decode the report.** In `caldav/elements.go`, add a field to `reportReq`:

```go
	Sync *internal.SyncCollectionQuery
```
and in `reportReq.UnmarshalXML`'s `switch start.Name` add:

```go
	case syncCollectionName:
		r.Sync = &internal.SyncCollectionQuery{}
		v = r.Sync
```

- [ ] **Step 5: Handle it.** In `handleReport` in `caldav/server.go`, before the final error return add:

```go
	} else if report.Sync != nil {
		return h.handleSyncCollection(r, w, report.Sync)
```
and add the method (add `encoding/xml` and `errors` imports if missing):

```go
func (h *Handler) handleSyncCollection(r *http.Request, w http.ResponseWriter, sync *internal.SyncCollectionQuery) error {
	sb, ok := h.Backend.(SyncBackend)
	if !ok {
		return internal.HTTPErrorf(http.StatusForbidden, "caldav: sync-collection not supported")
	}
	if sync.SyncLevel != "" && sync.SyncLevel != "1" {
		return internal.HTTPErrorf(http.StatusForbidden, "caldav: only sync-level 1 is supported")
	}
	res, err := sb.SyncCalendar(r.Context(), r.URL.Path, sync.SyncToken)
	if errors.Is(err, ErrInvalidSyncToken) {
		return &internal.HTTPError{Code: http.StatusForbidden, Err: &internal.Error{
			Raw: []internal.RawXMLValue{*internal.NewRawXMLElement(xml.Name{Space: "DAV:", Local: "valid-sync-token"}, nil, nil)},
		}}
	}
	if err != nil {
		return err
	}

	b := backend{Backend: h.Backend, Prefix: strings.TrimSuffix(h.Prefix, "/"), MaxResourceSize: h.MaxResourceSize}
	prop := sync.Prop
	if prop == nil {
		prop = &internal.Prop{Raw: []internal.RawXMLValue{*internal.NewRawXMLElement(internal.GetETagName, nil, nil)}}
	}
	propfind := internal.PropFind{Prop: prop}

	var resps []internal.Response
	for i := range res.Updated {
		resp, err := b.propFindCalendarObject(r.Context(), &propfind, &res.Updated[i])
		if err != nil {
			return err
		}
		resps = append(resps, *resp)
	}
	for _, p := range res.Deleted {
		resps = append(resps, internal.Response{
			Hrefs:  []internal.Href{{Path: p}},
			Status: &internal.Status{Code: http.StatusNotFound},
		})
	}
	ms := internal.NewMultiStatus(resps...)
	ms.SyncToken = res.SyncToken
	return internal.ServeMultiStatus(w, ms)
}
```

- [ ] **Step 6: Run the tests**

Run: `go -C third_party/go-webdav test ./caldav/`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add third_party/go-webdav/caldav
git commit -m "feat(fork): RFC 6578 sync-collection through an optional SyncBackend"
```

---

### Task 6: Fork patch 4 — PROPPATCH and MKCALENDAR

**Files:**
- Modify: `third_party/go-webdav/caldav/caldav.go`, `caldav/server.go` (`Handler.ServeHTTP`, `backend.PropPatch`, `backend.Options`)
- Test: `third_party/go-webdav/caldav/fork_test.go`

**Interfaces:**
- Produces:
```go
type CalendarUpdate struct{ Name, Description, Color *string }
type CalendarUpdater interface {
	UpdateCalendar(ctx context.Context, path string, update *CalendarUpdate) error
}
```
- MKCALENDAR calls the existing `Backend.CreateCalendar(ctx, *Calendar)` with `Path`, `Name`, `Description`, `Color` set.

- [ ] **Step 1: Write the failing tests** (append)

```go
type collBackend struct {
	rawBackend
	created *Calendar
	update  *CalendarUpdate
}

func (b *collBackend) CreateCalendar(ctx context.Context, cal *Calendar) error {
	b.created = cal
	return nil
}

func (b *collBackend) UpdateCalendar(ctx context.Context, path string, u *CalendarUpdate) error {
	b.update = u
	return nil
}

func send(t *testing.T, h *Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/xml")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestForkMkcalendar(t *testing.T) {
	b := &collBackend{}
	body := `<c:mkcalendar xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav" xmlns:ic="http://apple.com/ns/ical/"><d:set><d:prop><d:displayname>Work</d:displayname><ic:calendar-color>#00ff00</ic:calendar-color></d:prop></d:set></c:mkcalendar>`
	w := send(t, &Handler{Backend: b}, "MKCALENDAR", "/user/calendars/work/", body)
	if w.Code != 201 {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if b.created == nil || b.created.Name != "Work" || b.created.Color != "#00ff00" || b.created.Path != "/user/calendars/work/" {
		t.Fatalf("created %+v", b.created)
	}
}

func TestForkMkcalendarWrongPlace(t *testing.T) {
	w := send(t, &Handler{Backend: &collBackend{}}, "MKCALENDAR", "/user/", "")
	if w.Code != 403 {
		t.Fatalf("want 403, got %d", w.Code)
	}
}

func TestForkProppatch(t *testing.T) {
	b := &collBackend{}
	body := `<d:propertyupdate xmlns:d="DAV:" xmlns:ic="http://apple.com/ns/ical/"><d:set><d:prop><d:displayname>Home</d:displayname><ic:calendar-color>#123456</ic:calendar-color></d:prop></d:set></d:propertyupdate>`
	w := send(t, &Handler{Backend: b}, "PROPPATCH", "/user/calendars/cal/", body)
	if w.Code != 207 || !strings.Contains(w.Body.String(), "200") {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if b.update == nil || *b.update.Name != "Home" || *b.update.Color != "#123456" || b.update.Description != nil {
		t.Fatalf("update %+v", b.update)
	}
}

func TestForkProppatchUnknownPropIsAtomic(t *testing.T) {
	b := &collBackend{}
	body := `<d:propertyupdate xmlns:d="DAV:" xmlns:ic="http://apple.com/ns/ical/"><d:set><d:prop><d:displayname>Home</d:displayname><ic:calendar-order>3</ic:calendar-order></d:prop></d:set></d:propertyupdate>`
	w := send(t, &Handler{Backend: b}, "PROPPATCH", "/user/calendars/cal/", body)
	resp := w.Body.String()
	if w.Code != 207 || !strings.Contains(resp, "403") || !strings.Contains(resp, "424") {
		t.Fatalf("want 403 + 424 propstats, got %d %s", w.Code, resp)
	}
	if b.update != nil {
		t.Fatal("update applied despite a failed property")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go -C third_party/go-webdav test ./caldav/ -run 'TestForkMkcalendar|TestForkProppatch'`
Expected: FAIL to compile (`undefined: CalendarUpdate`).

- [ ] **Step 3: Add the types** to `caldav/caldav.go`

```go
// CalendarUpdate carries PROPPATCH changes; nil fields are unchanged, "" clears.
type CalendarUpdate struct {
	Name, Description, Color *string
}

// CalendarUpdater is implemented by backends that accept PROPPATCH on calendars.
type CalendarUpdater interface {
	UpdateCalendar(ctx context.Context, path string, update *CalendarUpdate) error
}
```

- [ ] **Step 4: Implement PROPPATCH.** Replace `backend.PropPatch` in `caldav/server.go`:

```go
func (b *backend) PropPatch(r *http.Request, update *internal.PropertyUpdate) (*internal.Response, error) {
	cu, ok := b.Backend.(CalendarUpdater)
	if !ok || b.resourceTypeAtPath(r.URL.Path) != resourceTypeCalendar {
		return nil, internal.HTTPErrorf(http.StatusForbidden, "caldav: PROPPATCH not supported here")
	}

	var u CalendarUpdate
	var names, rejected []xml.Name
	apply := func(prop internal.Prop, remove bool) error {
		for i := range prop.Raw {
			raw := &prop.Raw[i]
			name, ok := raw.XMLName()
			if !ok {
				continue
			}
			names = append(names, name)
			value := ""
			switch name {
			case internal.DisplayNameName:
				if !remove {
					var v internal.DisplayName
					if err := raw.Decode(&v); err != nil {
						return err
					}
					value = v.Name
				}
				u.Name = &value
			case calendarDescriptionName:
				if !remove {
					var v calendarDescription
					if err := raw.Decode(&v); err != nil {
						return err
					}
					value = v.Description
				}
				u.Description = &value
			case calendarColorName:
				if !remove {
					var v calendarColor
					if err := raw.Decode(&v); err != nil {
						return err
					}
					value = v.Color
				}
				u.Color = &value
			default:
				rejected = append(rejected, name)
			}
		}
		return nil
	}
	for _, s := range update.Set {
		if err := apply(s.Prop, false); err != nil {
			return nil, &internal.HTTPError{Code: http.StatusBadRequest, Err: err}
		}
	}
	for _, rm := range update.Remove {
		if err := apply(rm.Prop, true); err != nil {
			return nil, &internal.HTTPError{Code: http.StatusBadRequest, Err: err}
		}
	}

	resp := &internal.Response{Hrefs: []internal.Href{{Path: r.URL.Path}}}
	if len(rejected) > 0 {
		// RFC 4918 9.2: PROPPATCH is atomic. Refused properties get 403, the rest 424.
		for _, name := range names {
			code := http.StatusFailedDependency
			for _, bad := range rejected {
				if bad == name {
					code = http.StatusForbidden
				}
			}
			if err := resp.EncodeProp(code, internal.NewRawXMLElement(name, nil, nil)); err != nil {
				return nil, err
			}
		}
		return resp, nil
	}
	if err := cu.UpdateCalendar(r.Context(), r.URL.Path, &u); err != nil {
		return nil, err
	}
	for _, name := range names {
		if err := resp.EncodeProp(http.StatusOK, internal.NewRawXMLElement(name, nil, nil)); err != nil {
			return nil, err
		}
	}
	return resp, nil
}
```

- [ ] **Step 5: Implement MKCALENDAR.** In `Handler.ServeHTTP`, add a case before `default:`:

```go
	case "MKCALENDAR":
		b := backend{Backend: h.Backend, Prefix: strings.TrimSuffix(h.Prefix, "/"), MaxResourceSize: h.MaxResourceSize}
		err = b.mkcalendar(r)
		if err == nil {
			w.WriteHeader(http.StatusCreated)
		}
```
and add:

```go
type mkcalendarReq struct {
	XMLName xml.Name      `xml:"urn:ietf:params:xml:ns:caldav mkcalendar"`
	Set     *internal.Set `xml:"DAV: set"`
}

func (b *backend) mkcalendar(r *http.Request) error {
	if b.resourceTypeAtPath(r.URL.Path) != resourceTypeCalendar {
		return internal.HTTPErrorf(http.StatusForbidden, "caldav: calendar creation not allowed at given location")
	}
	cal := Calendar{Path: r.URL.Path}
	if !internal.IsRequestBodyEmpty(r) {
		var m mkcalendarReq
		if err := internal.DecodeXMLRequest(r, &m); err != nil {
			return err
		}
		if m.Set != nil {
			var name internal.DisplayName
			if err := m.Set.Prop.Decode(&name); err == nil {
				cal.Name = name.Name
			}
			var desc calendarDescription
			if err := m.Set.Prop.Decode(&desc); err == nil {
				cal.Description = desc.Description
			}
			var color calendarColor
			if err := m.Set.Prop.Decode(&color); err == nil {
				cal.Color = color.Color
			}
		}
	}
	return b.Backend.CreateCalendar(r.Context(), &cal)
}
```
In `backend.Options`, change the collection allow list to:
```go
		return caps, []string{http.MethodOptions, "PROPFIND", "PROPPATCH", "REPORT", "DELETE", "MKCOL", "MKCALENDAR"}, nil
```

- [ ] **Step 6: Run the tests**

Run: `go -C third_party/go-webdav test ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add third_party/go-webdav/caldav
git commit -m "feat(fork): PROPPATCH via CalendarUpdater and MKCALENDAR"
```

---

### Task 7: Pure calendar inspection and sync tokens

**Files:**
- Create: `internal/calendar/object.go`, `internal/calendar/synctoken.go`, `internal/calendar/AGENTS.md`
- Test: `internal/calendar/object_test.go`, `internal/calendar/synctoken_test.go`
- Modify: `cmd/server/main.go` (embed tzdata)

**Interfaces:**
- Produces:
```go
const MaxObjectSize = 1 << 20
type Object struct {
	UID        string
	FirstStart int64  // unix seconds
	LastEnd    *int64 // nil: recurs without end
}
var ErrInvalidData, ErrUnsupportedComponent error
func Inspect(cal *ical.Calendar) (Object, error)
func FormatSyncToken(epoch string, seq int64) string
func ParseSyncToken(token string) (epoch string, seq int64, err error)
```

- [ ] **Step 1: Confirm the go-ical and rrule-go API this task uses**

```bash
go get github.com/emersion/go-ical@v0.0.0-20240127095438-fc1c9d8fb2b6
go doc github.com/emersion/go-ical Event.DateTimeStart
go doc github.com/emersion/go-ical Event.DateTimeEnd
go doc github.com/emersion/go-ical Component.RecurrenceSet
go doc github.com/teambition/rrule-go Set.Between
go doc github.com/teambition/rrule-go Set.After
go doc github.com/emersion/go-ical Prop.ValueType
go doc github.com/emersion/go-ical ValueDate
go doc github.com/emersion/go-ical ParamTimezoneID
```
Expected: `DateTimeStart(loc *time.Location) (time.Time, error)`, `DateTimeEnd(loc *time.Location) (time.Time, error)`, `RecurrenceSet(loc *time.Location) (*rrule.Set, error)`, `Between(after, before time.Time, inc bool) []time.Time`, `After(dt time.Time, inc bool) time.Time`, `ValueType() ValueType`, and the constants `ValueDate` and `ParamTimezoneID`. If a signature differs, adapt Step 3's calls and note it in `internal/calendar/AGENTS.md`.

- [ ] **Step 2: Write the failing tests** in `internal/calendar/object_test.go`

```go
package calendar

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
)

func decode(t *testing.T, s string) *ical.Calendar {
	t.Helper()
	cal, err := ical.NewDecoder(strings.NewReader(strings.ReplaceAll(s, "\n", "\r\n"))).Decode()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return cal
}

func ev(body string) string {
	return "BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//t//EN\nBEGIN:VEVENT\nUID:u1\nDTSTAMP:20261001T000000Z\n" + body + "END:VEVENT\nEND:VCALENDAR\n"
}

func unix(s string) int64 {
	t, _ := time.Parse("20060102T150405Z", s)
	return t.Unix()
}

func TestInspectSingle(t *testing.T) {
	o, err := Inspect(decode(t, ev("DTSTART:20261007T090000Z\nDTEND:20261007T100000Z\n")))
	if err != nil {
		t.Fatal(err)
	}
	if o.UID != "u1" || o.FirstStart != unix("20261007T090000Z") || o.LastEnd == nil || *o.LastEnd != unix("20261007T100000Z") {
		t.Fatalf("%+v", o)
	}
}

func TestInspectAllDay(t *testing.T) {
	o, err := Inspect(decode(t, ev("DTSTART;VALUE=DATE:20261007\n")))
	if err != nil {
		t.Fatal(err)
	}
	// All-day without DTEND lasts one day; floating dates widen by 14h each side.
	if o.FirstStart > unix("20261007T000000Z") || *o.LastEnd < unix("20261008T000000Z") {
		t.Fatalf("%+v", o)
	}
}

func TestInspectUnboundedRecurrence(t *testing.T) {
	o, err := Inspect(decode(t, ev("DTSTART:20261007T090000Z\nDTEND:20261007T100000Z\nRRULE:FREQ=WEEKLY\n")))
	if err != nil {
		t.Fatal(err)
	}
	if o.LastEnd != nil {
		t.Fatalf("unbounded rule must have nil LastEnd, got %d", *o.LastEnd)
	}
}

func TestInspectBoundedRecurrence(t *testing.T) {
	o, err := Inspect(decode(t, ev("DTSTART:20261007T090000Z\nDTEND:20261007T100000Z\nRRULE:FREQ=DAILY;COUNT=3\n")))
	if err != nil {
		t.Fatal(err)
	}
	if o.LastEnd == nil || *o.LastEnd != unix("20261009T100000Z") {
		t.Fatalf("%+v", o)
	}
}

func TestInspectUnknownTZIDWidens(t *testing.T) {
	o, err := Inspect(decode(t, ev("DTSTART;TZID=Eastern Standard Time:20261007T090000\nDTEND;TZID=Eastern Standard Time:20261007T100000\n")))
	if err != nil {
		t.Fatalf("unknown TZID must be accepted: %v", err)
	}
	real := unix("20261007T130000Z") // 09:00 EDT
	if o.FirstStart > real || o.LastEnd == nil || *o.LastEnd < real+3600 {
		t.Fatalf("index must cover the real instant: %+v", o)
	}
	rec, err := Inspect(decode(t, ev("DTSTART;TZID=Eastern Standard Time:20261007T090000\nDTEND;TZID=Eastern Standard Time:20261007T100000\nRRULE:FREQ=WEEKLY;COUNT=4\n")))
	if err != nil {
		t.Fatalf("recurring event with unknown TZID must be accepted: %v", err)
	}
	if rec.FirstStart > real {
		t.Fatalf("recurring index starts too late: %+v", rec)
	}
}

func TestInspectRejects(t *testing.T) {
	todo := "BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//t//EN\nBEGIN:VTODO\nUID:t1\nDTSTAMP:20261001T000000Z\nEND:VTODO\nEND:VCALENDAR\n"
	if _, err := Inspect(decode(t, todo)); !errors.Is(err, ErrUnsupportedComponent) {
		t.Fatalf("VTODO: %v", err)
	}
	noUID := "BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//t//EN\nBEGIN:VEVENT\nDTSTAMP:20261001T000000Z\nDTSTART:20261007T090000Z\nEND:VEVENT\nEND:VCALENDAR\n"
	if _, err := Inspect(decode(t, noUID)); !errors.Is(err, ErrInvalidData) {
		t.Fatalf("no UID: %v", err)
	}
	twoUIDs := "BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//t//EN\nBEGIN:VEVENT\nUID:a\nDTSTAMP:20261001T000000Z\nDTSTART:20261007T090000Z\nEND:VEVENT\nBEGIN:VEVENT\nUID:b\nDTSTAMP:20261001T000000Z\nDTSTART:20261008T090000Z\nEND:VEVENT\nEND:VCALENDAR\n"
	if _, err := Inspect(decode(t, twoUIDs)); !errors.Is(err, ErrInvalidData) {
		t.Fatalf("two UIDs: %v", err)
	}
}

func TestInspectOverrideExtendsBounds(t *testing.T) {
	s := "BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//t//EN\n" +
		"BEGIN:VEVENT\nUID:r\nDTSTAMP:20261001T000000Z\nDTSTART:20261007T090000Z\nDTEND:20261007T100000Z\nRRULE:FREQ=DAILY;COUNT=2\nEND:VEVENT\n" +
		"BEGIN:VEVENT\nUID:r\nDTSTAMP:20261001T000000Z\nRECURRENCE-ID:20261008T090000Z\nDTSTART:20261020T090000Z\nDTEND:20261020T100000Z\nEND:VEVENT\n" +
		"END:VCALENDAR\n"
	o, err := Inspect(decode(t, s))
	if err != nil {
		t.Fatal(err)
	}
	if o.LastEnd == nil || *o.LastEnd != unix("20261020T100000Z") {
		t.Fatalf("moved occurrence must extend LastEnd: %+v", o)
	}
}
```
and `internal/calendar/synctoken_test.go`:

```go
package calendar

import "testing"

func TestSyncTokenRoundTrip(t *testing.T) {
	tok := FormatSyncToken("ab12", 42)
	epoch, seq, err := ParseSyncToken(tok)
	if err != nil || epoch != "ab12" || seq != 42 {
		t.Fatalf("%q -> %q %d %v", tok, epoch, seq, err)
	}
}

func TestSyncTokenGarbage(t *testing.T) {
	for _, s := range []string{"", "nope", "urn:kycalendar:sync:ab12", "urn:kycalendar:sync:ab12:-1", "urn:kycalendar:sync::4", "urn:kycalendar:sync:ab12:x"} {
		if _, _, err := ParseSyncToken(s); err == nil {
			t.Errorf("accepted %q", s)
		}
	}
}
```

- [ ] **Step 3: Run to verify failure**

Run: `go test ./internal/calendar/`
Expected: FAIL to compile (`undefined: Inspect`).

- [ ] **Step 4: Implement** `internal/calendar/synctoken.go`

```go
package calendar

import (
	"errors"
	"strconv"
	"strings"
)

const syncTokenPrefix = "urn:kycalendar:sync:"

var errBadSyncToken = errors.New("calendar: malformed sync token")

// FormatSyncToken encodes the instance sync epoch and a calendar sequence as a URI.
func FormatSyncToken(epoch string, seq int64) string {
	return syncTokenPrefix + epoch + ":" + strconv.FormatInt(seq, 10)
}

// ParseSyncToken is the inverse of FormatSyncToken.
func ParseSyncToken(token string) (string, int64, error) {
	rest, ok := strings.CutPrefix(token, syncTokenPrefix)
	if !ok {
		return "", 0, errBadSyncToken
	}
	epoch, seqText, ok := strings.Cut(rest, ":")
	if !ok || epoch == "" {
		return "", 0, errBadSyncToken
	}
	seq, err := strconv.ParseInt(seqText, 10, 64)
	if err != nil || seq < 0 {
		return "", 0, errBadSyncToken
	}
	return epoch, seq, nil
}
```

- [ ] **Step 5: Implement** `internal/calendar/object.go`

```go
// Package calendar holds pure iCalendar rules: what KyCalendar accepts and how it is indexed.
package calendar

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/emersion/go-ical"
)

// MaxObjectSize bounds one stored calendar object in bytes.
const MaxObjectSize = 1 << 20

var (
	ErrInvalidData          = errors.New("calendar: invalid calendar data")
	ErrUnsupportedComponent = errors.New("calendar: only VEVENT is supported")
)

// Object is what the store indexes for one calendar resource.
type Object struct {
	UID        string
	FirstStart int64  // unix seconds
	LastEnd    *int64 // nil: recurs without end
}

// zoneSlack widens bounds when a time's zone is unknown or floating: UTC-12 to UTC+14.
const zoneSlack = 14 * time.Hour

// horizon caps recurrence expansion while indexing; rules running past it index as unbounded.
const horizon = 100 * 365 * 24 * time.Hour

// Inspect validates a decoded calendar object and computes its index bounds.
func Inspect(cal *ical.Calendar) (Object, error) {
	var o Object
	first, last := int64(0), int64(0)
	unbounded, seen := false, false

	for _, comp := range cal.Children {
		switch comp.Name {
		case ical.CompTimezone:
			continue
		case ical.CompEvent:
		default:
			return Object{}, fmt.Errorf("%w: %s", ErrUnsupportedComponent, comp.Name)
		}
		uid, err := comp.Props.Text(ical.PropUID)
		if err != nil || uid == "" {
			return Object{}, fmt.Errorf("%w: missing UID", ErrInvalidData)
		}
		if o.UID != "" && uid != o.UID {
			return Object{}, fmt.Errorf("%w: conflicting UIDs", ErrInvalidData)
		}
		o.UID = uid

		start, end, slack, err := eventSpan(comp)
		if err != nil {
			return Object{}, err
		}
		compFirst, compLast := start.Add(-slack), end.Add(slack)

		if comp.Props.Get(ical.PropRecurrenceRule) != nil {
			rset, err := comp.RecurrenceSet(time.UTC)
			limit := start.Add(horizon)
			if err != nil && slack > 0 {
				// Zone unknown, so the rule cannot be expanded here; index as unbounded.
				unbounded = true
			} else if err != nil || rset == nil {
				return Object{}, fmt.Errorf("%w: bad recurrence", ErrInvalidData)
			} else if !rset.After(limit, false).IsZero() {
				unbounded = true
			} else if occ := rset.Between(start, limit, true); len(occ) > 0 {
				compLast = occ[len(occ)-1].Add(end.Sub(start)).Add(slack)
			}
		}

		if !seen || compFirst.Unix() < first {
			first = compFirst.Unix()
		}
		if !seen || compLast.Unix() > last {
			last = compLast.Unix()
		}
		seen = true
	}
	if !seen {
		return Object{}, fmt.Errorf("%w: no VEVENT", ErrInvalidData)
	}
	o.FirstStart = first
	if !unbounded {
		o.LastEnd = &last
	}
	return o, nil
}

// eventSpan returns start and end in UTC, plus the slack to apply when the zone is not exact.
func eventSpan(comp *ical.Component) (time.Time, time.Time, time.Duration, error) {
	event := ical.Event{Component: comp}
	var slack time.Duration
	start, err := event.DateTimeStart(time.UTC)
	if err != nil {
		start, err = parseFloating(comp.Props.Get(ical.PropDateTimeStart))
		if err != nil {
			return time.Time{}, time.Time{}, 0, fmt.Errorf("%w: DTSTART", ErrInvalidData)
		}
		slack = zoneSlack
	}
	if isFloating(comp.Props.Get(ical.PropDateTimeStart)) {
		slack = zoneSlack
	}
	end, err := event.DateTimeEnd(time.UTC)
	if err != nil || end.Before(start) {
		if parsed, perr := parseFloating(comp.Props.Get(ical.PropDateTimeEnd)); perr == nil && !parsed.Before(start) {
			end = parsed
		} else {
			end = start
		}
	}
	if end.Equal(start) && isDate(comp.Props.Get(ical.PropDateTimeStart)) {
		end = start.Add(24 * time.Hour)
	}
	return start, end, slack, nil
}

func isDate(p *ical.Prop) bool {
	return p != nil && p.ValueType() == ical.ValueDate
}

// isFloating reports a DATE or a DATE-TIME with neither TZID nor a Z suffix.
func isFloating(p *ical.Prop) bool {
	if p == nil {
		return false
	}
	return isDate(p) || (p.Params.Get(ical.ParamTimezoneID) == "" && !strings.HasSuffix(p.Value, "Z"))
}

// parseFloating reads the wall-clock value as UTC, ignoring an unresolvable TZID.
func parseFloating(p *ical.Prop) (time.Time, error) {
	if p == nil {
		return time.Time{}, errors.New("missing")
	}
	for _, layout := range []string{"20060102T150405Z", "20060102T150405", "20060102"} {
		if t, err := time.ParseInLocation(layout, p.Value, time.UTC); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("unparseable")
}
```
If Step 1 showed `ical.Event` has no `Component` field name (it is an embedded `*Component`), `ical.Event{Component: comp}` still compiles because the embedded field's name is `Component`.

- [ ] **Step 6: Embed time zone data** so `LoadLocation` works in the alpine image. In `cmd/server/main.go` add the import:

```go
	_ "time/tzdata"
```

- [ ] **Step 7: Run the tests**

Run: `go test ./internal/calendar/`
Expected: PASS. If `TestInspectAllDay` fails because go-ical resolves `VALUE=DATE` without error, the `isFloating` slack still applies; check the assertion against the computed values and fix the code, not the test.

- [ ] **Step 8: Write `internal/calendar/AGENTS.md`**

```markdown
# internal/calendar

## Purpose
Pure iCalendar rules: what a calendar object must contain and how it is indexed; sync token format.

## Local Contracts
- No I/O. Callers pass decoded `*ical.Calendar`.
- VEVENT only; one UID per object; every VEVENT needs DTSTART.
- `FirstStart`/`LastEnd` are conservative: unknown or floating zones widen by 14h; rules past 100 years index as unbounded (`LastEnd == nil`).
- Sync tokens are `urn:kycalendar:sync:<epoch>:<seq>`.

## Verification
- `go test ./internal/calendar/`
```

- [ ] **Step 9: Commit**

```bash
git add internal/calendar cmd/server/main.go go.mod go.sum
git commit -m "feat: calendar object inspection and sync tokens"
```

---

### Task 8: Calendar store

**Files:**
- Modify: `internal/store/migrations/migrations.go` (add v6), `internal/store/store.go`, `internal/store/sqlstore.go`, `internal/store/models.go`
- Create: `internal/store/calendars.go`
- Test: `internal/store/calendars_test.go`

**Interfaces:**
- Consumes: `testdb.Config(t)`, `store.Open(ctx, cfg)`.
- Produces (package `store`):
```go
type Calendar struct {
	ID, OwnerKind, OwnerID, Slug, Name, Color, Description string
	Seq       int64
	CreatedAt time.Time
}
type CalendarObject struct {
	CalendarID, Name, UID, ETag string
	Data       []byte
	FirstStart int64
	LastEnd    *int64
	ModifiedAt time.Time
}
type CalendarChange struct {
	Seq     int64
	Name    string
	Deleted bool
}
var ErrPreconditionFailed, ErrUIDConflict, ErrSyncTokenExpired error
type CalendarStore interface {
	CreateCalendar(ctx context.Context, c *Calendar) error
	GetCalendarBySlug(ctx context.Context, ownerKind, ownerID, slug string) (*Calendar, error)
	ListCalendarsByOwner(ctx context.Context, ownerKind, ownerID string) ([]*Calendar, error)
	UpdateCalendar(ctx context.Context, id string, name, description, color *string) error
	GetObject(ctx context.Context, calendarID, name string) (*CalendarObject, error)
	ListObjects(ctx context.Context, calendarID string) ([]*CalendarObject, error)
	ListObjectsInRange(ctx context.Context, calendarID string, start, end int64) ([]*CalendarObject, error)
	PutObject(ctx context.Context, o *CalendarObject, ifMatch string, ifNoneMatch bool) (created bool, err error)
	DeleteObject(ctx context.Context, calendarID, name, ifMatch string) error
	ChangesSince(ctx context.Context, calendarID string, seq int64) ([]CalendarChange, error)
	PruneChanges(ctx context.Context, before time.Time) error
	CountObjectsByOwner(ctx context.Context, ownerKind, ownerID string) (int, error)
	SyncEpoch(ctx context.Context) (string, error)
}
// Store gains: Calendars() CalendarStore
```
`PutObject` sets `o.ETag` (SHA-256 hex of `o.Data`) and `o.ModifiedAt`. `ifMatch == ""` means no condition.

- [ ] **Step 1: Write the failing tests** in `internal/store/calendars_test.go`

```go
package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

func calStore(t *testing.T) (store.CalendarStore, *store.Calendar) {
	t.Helper()
	st, err := store.Open(context.Background(), testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	c := &store.Calendar{ID: "cal_1", OwnerKind: "user", OwnerID: "usr_a", Slug: "default", Name: "Calendar"}
	if err := st.Calendars().CreateCalendar(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return st.Calendars(), c
}

func obj(cal, name, uid, data string, first int64, last *int64) *store.CalendarObject {
	return &store.CalendarObject{CalendarID: cal, Name: name, UID: uid, Data: []byte(data), FirstStart: first, LastEnd: last}
}

func i64(v int64) *int64 { return &v }

func TestCreateCalendarSlugUnique(t *testing.T) {
	cs, _ := calStore(t)
	dup := &store.Calendar{ID: "cal_2", OwnerKind: "user", OwnerID: "usr_a", Slug: "default", Name: "x"}
	if err := cs.CreateCalendar(context.Background(), dup); !errors.Is(err, store.ErrAlreadyExists) {
		t.Fatalf("want ErrAlreadyExists, got %v", err)
	}
}

func TestPutObjectSeqAndChanges(t *testing.T) {
	ctx := context.Background()
	cs, c := calStore(t)
	o := obj(c.ID, "a.ics", "u1", "v1", 100, i64(200))
	created, err := cs.PutObject(ctx, o, "", false)
	if err != nil || !created || o.ETag == "" {
		t.Fatalf("put: created=%v etag=%q err=%v", created, o.ETag, err)
	}
	first := o.ETag
	o2 := obj(c.ID, "a.ics", "u1", "v2", 100, i64(200))
	if created, err := cs.PutObject(ctx, o2, first, false); err != nil || created {
		t.Fatalf("update: created=%v err=%v", created, err)
	}
	if err := cs.DeleteObject(ctx, c.ID, "a.ics", ""); err != nil {
		t.Fatal(err)
	}
	changes, err := cs.ChangesSince(ctx, c.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 3 || changes[0].Seq != 1 || changes[2].Seq != 3 || !changes[2].Deleted || changes[1].Deleted {
		t.Fatalf("changes %+v", changes)
	}
	got, _ := cs.GetCalendarBySlug(ctx, "user", "usr_a", "default")
	if got.Seq != 3 {
		t.Fatalf("seq %d", got.Seq)
	}
}

func TestPutObjectPreconditions(t *testing.T) {
	ctx := context.Background()
	cs, c := calStore(t)
	o := obj(c.ID, "a.ics", "u1", "v1", 100, i64(200))
	if _, err := cs.PutObject(ctx, o, "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.PutObject(ctx, obj(c.ID, "a.ics", "u1", "v2", 100, i64(200)), "", true); !errors.Is(err, store.ErrPreconditionFailed) {
		t.Fatalf("If-None-Match on existing: %v", err)
	}
	if _, err := cs.PutObject(ctx, obj(c.ID, "a.ics", "u1", "v2", 100, i64(200)), "stale", false); !errors.Is(err, store.ErrPreconditionFailed) {
		t.Fatalf("stale If-Match: %v", err)
	}
	if err := cs.DeleteObject(ctx, c.ID, "a.ics", "stale"); !errors.Is(err, store.ErrPreconditionFailed) {
		t.Fatalf("stale delete: %v", err)
	}
	if _, err := cs.PutObject(ctx, obj(c.ID, "b.ics", "u1", "v1", 100, i64(200)), "", false); !errors.Is(err, store.ErrUIDConflict) {
		t.Fatalf("UID reuse: %v", err)
	}
	got, _ := cs.GetObject(ctx, c.ID, "a.ics")
	if string(got.Data) != "v1" {
		t.Fatalf("failed writes changed data: %q", got.Data)
	}
	cal, _ := cs.GetCalendarBySlug(ctx, "user", "usr_a", "default")
	if cal.Seq != 1 {
		t.Fatalf("failed writes advanced seq to %d", cal.Seq)
	}
}

func TestListObjectsInRange(t *testing.T) {
	ctx := context.Background()
	cs, c := calStore(t)
	cs.PutObject(ctx, obj(c.ID, "past.ics", "p", "x", 10, i64(20)), "", false)
	cs.PutObject(ctx, obj(c.ID, "in.ics", "i", "x", 100, i64(200)), "", false)
	cs.PutObject(ctx, obj(c.ID, "forever.ics", "f", "x", 5, nil), "", false)
	cs.PutObject(ctx, obj(c.ID, "future.ics", "u", "x", 1000, i64(2000)), "", false)
	got, err := cs.ListObjectsInRange(ctx, c.ID, 150, 500)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, o := range got {
		names[o.Name] = true
	}
	if len(got) != 2 || !names["in.ics"] || !names["forever.ics"] {
		t.Fatalf("got %v", names)
	}
}

func TestChangesSinceExpired(t *testing.T) {
	ctx := context.Background()
	cs, c := calStore(t)
	cs.PutObject(ctx, obj(c.ID, "a.ics", "u1", "v1", 1, i64(2)), "", false)
	cs.PutObject(ctx, obj(c.ID, "b.ics", "u2", "v1", 1, i64(2)), "", false)
	if err := cs.PruneChanges(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.ChangesSince(ctx, c.ID, 1); !errors.Is(err, store.ErrSyncTokenExpired) {
		t.Fatalf("token older than pruned rows: %v", err)
	}
	if ch, err := cs.ChangesSince(ctx, c.ID, 2); err != nil || len(ch) != 0 {
		t.Fatalf("current token: %v %v", ch, err)
	}
}

func TestSyncEpochStable(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a, err := st.Calendars().SyncEpoch(ctx)
	b, _ := st.Calendars().SyncEpoch(ctx)
	if err != nil || a == "" || a != b {
		t.Fatalf("epoch %q %q %v", a, b, err)
	}
}

func TestCountObjectsByOwner(t *testing.T) {
	ctx := context.Background()
	cs, c := calStore(t)
	cs.PutObject(ctx, obj(c.ID, "a.ics", "u1", "x", 1, i64(2)), "", false)
	cs.PutObject(ctx, obj(c.ID, "b.ics", "u2", "x", 1, i64(2)), "", false)
	if n, err := cs.CountObjectsByOwner(ctx, "user", "usr_a"); err != nil || n != 2 {
		t.Fatalf("count %d %v", n, err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/store/ -run 'Calendar|PutObject|ListObjects|ChangesSince|SyncEpoch|CountObjects'`
Expected: FAIL to compile (`st.Calendars undefined`).

- [ ] **Step 3: Add migration v6** to `registry` in `internal/store/migrations/migrations.go` (after v5)

```go
	{
		Version: 6,
		Name:    "calendars",
		SQLite: `
CREATE TABLE calendars (
    id TEXT PRIMARY KEY,
    owner_kind TEXT NOT NULL CHECK (owner_kind IN ('user', 'group')),
    owner_id TEXT NOT NULL,
    slug TEXT NOT NULL,
    name TEXT NOT NULL,
    color TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    seq INTEGER NOT NULL DEFAULT 0,
    min_sync_seq INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL,
    UNIQUE (owner_kind, owner_id, slug)
);
CREATE TABLE calendar_objects (
    calendar_id TEXT NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    uid TEXT NOT NULL,
    etag TEXT NOT NULL,
    data BLOB NOT NULL,
    first_start INTEGER NOT NULL,
    last_end INTEGER,
    modified_at DATETIME NOT NULL,
    PRIMARY KEY (calendar_id, name),
    UNIQUE (calendar_id, uid)
);
CREATE INDEX idx_calendar_objects_range ON calendar_objects(calendar_id, first_start);
CREATE TABLE calendar_changes (
    calendar_id TEXT NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
    seq INTEGER NOT NULL,
    name TEXT NOT NULL,
    deleted INTEGER NOT NULL,
    changed_at DATETIME NOT NULL,
    PRIMARY KEY (calendar_id, seq)
);
CREATE INDEX idx_calendar_changes_time ON calendar_changes(changed_at);
CREATE TABLE calendar_meta (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
INSERT INTO calendar_meta (key, value) VALUES ('sync_epoch', lower(hex(randomblob(8))));`,
		Postgres: `
CREATE TABLE calendars (
    id VARCHAR(64) PRIMARY KEY,
    owner_kind VARCHAR(16) NOT NULL CHECK (owner_kind IN ('user', 'group')),
    owner_id VARCHAR(64) NOT NULL,
    slug VARCHAR(64) NOT NULL,
    name TEXT NOT NULL,
    color VARCHAR(32) NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    seq BIGINT NOT NULL DEFAULT 0,
    min_sync_seq BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (owner_kind, owner_id, slug)
);
CREATE TABLE calendar_objects (
    calendar_id VARCHAR(64) NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    uid TEXT NOT NULL,
    etag VARCHAR(64) NOT NULL,
    data BYTEA NOT NULL,
    first_start BIGINT NOT NULL,
    last_end BIGINT,
    modified_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (calendar_id, name),
    UNIQUE (calendar_id, uid)
);
CREATE INDEX idx_calendar_objects_range ON calendar_objects(calendar_id, first_start);
CREATE TABLE calendar_changes (
    calendar_id VARCHAR(64) NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
    seq BIGINT NOT NULL,
    name TEXT NOT NULL,
    deleted INTEGER NOT NULL,
    changed_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (calendar_id, seq)
);
CREATE INDEX idx_calendar_changes_time ON calendar_changes(changed_at);
CREATE TABLE calendar_meta (
    key VARCHAR(64) PRIMARY KEY,
    value TEXT NOT NULL
);
INSERT INTO calendar_meta (key, value) VALUES ('sync_epoch', substr(md5(random()::text), 1, 16));`,
	},
```

- [ ] **Step 4: Add models and the interface.** In `internal/store/models.go` add the `Calendar`, `CalendarObject` and `CalendarChange` structs from **Interfaces** above. In `internal/store/store.go` add to the `var (…)` errors:

```go
	ErrPreconditionFailed = errors.New("precondition failed")
	ErrUIDConflict        = errors.New("uid already used in calendar")
	ErrSyncTokenExpired   = errors.New("sync token expired")
```
add `Calendars() CalendarStore` to `Store`, and the `CalendarStore` interface from **Interfaces**. In `internal/store/sqlstore.go` add field `calendars *calendarStore` to `SQLStore`, `s.calendars = &calendarStore{store: s}` in `newSQLStore`, and `func (s *SQLStore) Calendars() CalendarStore { return s.calendars }`.

- [ ] **Step 5: Implement** `internal/store/calendars.go`

```go
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

type calendarStore struct{ store *SQLStore }

func (c *calendarStore) q(query string) string { return c.store.rebind(query) }

func isUniqueViolation(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") || strings.Contains(msg, "duplicate key")
}

func (c *calendarStore) CreateCalendar(ctx context.Context, cal *Calendar) error {
	cal.CreatedAt = time.Now().UTC()
	_, err := c.store.db.ExecContext(ctx, c.q(`INSERT INTO calendars (id, owner_kind, owner_id, slug, name, color, description, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`), cal.ID, cal.OwnerKind, cal.OwnerID, cal.Slug, cal.Name, cal.Color, cal.Description, cal.CreatedAt)
	if err != nil && isUniqueViolation(err) {
		return ErrAlreadyExists
	}
	return err
}

const calendarCols = `id, owner_kind, owner_id, slug, name, color, description, seq, created_at`

func scanCalendar(row interface{ Scan(...any) error }) (*Calendar, error) {
	var cal Calendar
	err := row.Scan(&cal.ID, &cal.OwnerKind, &cal.OwnerID, &cal.Slug, &cal.Name, &cal.Color, &cal.Description, &cal.Seq, &cal.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &cal, err
}

func (c *calendarStore) GetCalendarBySlug(ctx context.Context, ownerKind, ownerID, slug string) (*Calendar, error) {
	return scanCalendar(c.store.db.QueryRowContext(ctx, c.q(`SELECT `+calendarCols+` FROM calendars
WHERE owner_kind = ? AND owner_id = ? AND slug = ?`), ownerKind, ownerID, slug))
}

func (c *calendarStore) ListCalendarsByOwner(ctx context.Context, ownerKind, ownerID string) ([]*Calendar, error) {
	rows, err := c.store.db.QueryContext(ctx, c.q(`SELECT `+calendarCols+` FROM calendars
WHERE owner_kind = ? AND owner_id = ? ORDER BY created_at, id`), ownerKind, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Calendar
	for rows.Next() {
		cal, err := scanCalendar(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, cal)
	}
	return out, rows.Err()
}

func (c *calendarStore) UpdateCalendar(ctx context.Context, id string, name, description, color *string) error {
	res, err := c.store.db.ExecContext(ctx, c.q(`UPDATE calendars SET
name = COALESCE(?, name), description = COALESCE(?, description), color = COALESCE(?, color) WHERE id = ?`),
		name, description, color, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

const objectCols = `calendar_id, name, uid, etag, data, first_start, last_end, modified_at`

func scanObject(row interface{ Scan(...any) error }) (*CalendarObject, error) {
	var o CalendarObject
	var last sql.NullInt64
	err := row.Scan(&o.CalendarID, &o.Name, &o.UID, &o.ETag, &o.Data, &o.FirstStart, &last, &o.ModifiedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if last.Valid {
		o.LastEnd = &last.Int64
	}
	return &o, err
}

func (c *calendarStore) queryObjects(ctx context.Context, query string, args ...any) ([]*CalendarObject, error) {
	rows, err := c.store.db.QueryContext(ctx, c.q(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*CalendarObject
	for rows.Next() {
		o, err := scanObject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (c *calendarStore) GetObject(ctx context.Context, calendarID, name string) (*CalendarObject, error) {
	return scanObject(c.store.db.QueryRowContext(ctx, c.q(`SELECT `+objectCols+` FROM calendar_objects
WHERE calendar_id = ? AND name = ?`), calendarID, name))
}

func (c *calendarStore) ListObjects(ctx context.Context, calendarID string) ([]*CalendarObject, error) {
	return c.queryObjects(ctx, `SELECT `+objectCols+` FROM calendar_objects WHERE calendar_id = ? ORDER BY name`, calendarID)
}

func (c *calendarStore) ListObjectsInRange(ctx context.Context, calendarID string, start, end int64) ([]*CalendarObject, error) {
	return c.queryObjects(ctx, `SELECT `+objectCols+` FROM calendar_objects
WHERE calendar_id = ? AND first_start < ? AND (last_end IS NULL OR last_end > ?) ORDER BY name`, calendarID, end, start)
}

// bump advances the calendar sequence and logs one change, inside tx.
func (c *calendarStore) bump(ctx context.Context, tx *sql.Tx, calendarID, name string, deleted bool) error {
	var seq int64
	if err := tx.QueryRowContext(ctx, c.q(`UPDATE calendars SET seq = seq + 1 WHERE id = ? RETURNING seq`), calendarID).Scan(&seq); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	flag := 0
	if deleted {
		flag = 1
	}
	_, err := tx.ExecContext(ctx, c.q(`INSERT INTO calendar_changes (calendar_id, seq, name, deleted, changed_at) VALUES (?, ?, ?, ?, ?)`),
		calendarID, seq, name, flag, time.Now().UTC())
	return err
}

func (c *calendarStore) currentETag(ctx context.Context, tx *sql.Tx, calendarID, name string) (string, bool, error) {
	var etag string
	err := tx.QueryRowContext(ctx, c.q(`SELECT etag FROM calendar_objects WHERE calendar_id = ? AND name = ?`), calendarID, name).Scan(&etag)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return etag, err == nil, err
}

func (c *calendarStore) PutObject(ctx context.Context, o *CalendarObject, ifMatch string, ifNoneMatch bool) (bool, error) {
	sum := sha256.Sum256(o.Data)
	o.ETag = hex.EncodeToString(sum[:])
	o.ModifiedAt = time.Now().UTC()

	tx, err := c.store.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	etag, exists, err := c.currentETag(ctx, tx, o.CalendarID, o.Name)
	if err != nil {
		return false, err
	}
	if (ifNoneMatch && exists) || (ifMatch != "" && (!exists || etag != ifMatch)) {
		return false, ErrPreconditionFailed
	}
	var other string
	err = tx.QueryRowContext(ctx, c.q(`SELECT name FROM calendar_objects WHERE calendar_id = ? AND uid = ? AND name <> ?`),
		o.CalendarID, o.UID, o.Name).Scan(&other)
	if err == nil {
		return false, ErrUIDConflict
	} else if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}

	if err := c.bump(ctx, tx, o.CalendarID, o.Name, false); err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, c.q(`INSERT INTO calendar_objects (`+objectCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (calendar_id, name) DO UPDATE SET uid = excluded.uid, etag = excluded.etag, data = excluded.data,
first_start = excluded.first_start, last_end = excluded.last_end, modified_at = excluded.modified_at`),
		o.CalendarID, o.Name, o.UID, o.ETag, o.Data, o.FirstStart, o.LastEnd, o.ModifiedAt)
	if err != nil {
		return false, err
	}
	return !exists, tx.Commit()
}

func (c *calendarStore) DeleteObject(ctx context.Context, calendarID, name, ifMatch string) error {
	tx, err := c.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	etag, exists, err := c.currentETag(ctx, tx, calendarID, name)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	if ifMatch != "" && etag != ifMatch {
		return ErrPreconditionFailed
	}
	if _, err := tx.ExecContext(ctx, c.q(`DELETE FROM calendar_objects WHERE calendar_id = ? AND name = ?`), calendarID, name); err != nil {
		return err
	}
	if err := c.bump(ctx, tx, calendarID, name, true); err != nil {
		return err
	}
	return tx.Commit()
}

func (c *calendarStore) ChangesSince(ctx context.Context, calendarID string, seq int64) ([]CalendarChange, error) {
	var minSeq int64
	if err := c.store.db.QueryRowContext(ctx, c.q(`SELECT min_sync_seq FROM calendars WHERE id = ?`), calendarID).Scan(&minSeq); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if seq < minSeq {
		return nil, ErrSyncTokenExpired
	}
	rows, err := c.store.db.QueryContext(ctx, c.q(`SELECT seq, name, deleted FROM calendar_changes
WHERE calendar_id = ? AND seq > ? ORDER BY seq`), calendarID, seq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CalendarChange
	for rows.Next() {
		var ch CalendarChange
		var deleted int
		if err := rows.Scan(&ch.Seq, &ch.Name, &deleted); err != nil {
			return nil, err
		}
		ch.Deleted = deleted == 1
		out = append(out, ch)
	}
	return out, rows.Err()
}

func (c *calendarStore) PruneChanges(ctx context.Context, before time.Time) error {
	tx, err := c.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, c.q(`UPDATE calendars SET min_sync_seq = (
SELECT MAX(seq) FROM calendar_changes WHERE calendar_changes.calendar_id = calendars.id AND changed_at < ?)
WHERE EXISTS (SELECT 1 FROM calendar_changes WHERE calendar_changes.calendar_id = calendars.id AND changed_at < ?)`),
		before.UTC(), before.UTC())
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, c.q(`DELETE FROM calendar_changes WHERE changed_at < ?`), before.UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

func (c *calendarStore) CountObjectsByOwner(ctx context.Context, ownerKind, ownerID string) (int, error) {
	var n int
	err := c.store.db.QueryRowContext(ctx, c.q(`SELECT COUNT(*) FROM calendar_objects o JOIN calendars c ON c.id = o.calendar_id
WHERE c.owner_kind = ? AND c.owner_id = ?`), ownerKind, ownerID).Scan(&n)
	return n, err
}

func (c *calendarStore) SyncEpoch(ctx context.Context) (string, error) {
	var v string
	err := c.store.db.QueryRowContext(ctx, c.q(`SELECT value FROM calendar_meta WHERE key = 'sync_epoch'`)).Scan(&v)
	return v, err
}
```
Note on `min_sync_seq`: a token whose seq equals the highest pruned seq is still valid (its changes after that seq are all retained), which is why `TestChangesSinceExpired` expects seq 2 to pass and seq 1 to fail.

- [ ] **Step 6: Run the tests**

Run: `go test ./internal/store/`
Expected: PASS. If another type implements `store.Store` (grep `func (.*) Groups() store.GroupStore`), add a `Calendars()` method to it.

- [ ] **Step 7: Run against Postgres when available**

Run: `make test-postgres` (skips without `KY_TEST_POSTGRES_DSN`)
Expected: PASS or skipped.

- [ ] **Step 8: Commit**

```bash
git add internal/store
git commit -m "feat(store): calendars, objects and change log with sync epochs"
```

---

### Task 9: App password tokens and store

**Files:**
- Create: `internal/apppass/apppass.go`, `internal/apppass/apppass_test.go`, `internal/store/apppasswords.go`, `internal/store/apppasswords_test.go`
- Modify: `internal/store/migrations/migrations.go` (v7), `internal/store/store.go`, `internal/store/sqlstore.go`, `internal/store/models.go`

**Interfaces:**
- Produces (package `apppass`):
```go
func Generate() (id, token, hash string, err error) // token "kc_<id>_<secret>", hash = SHA-256 hex of secret
func Parse(token string) (id, secret string, ok bool)
func Matches(hash, secret string) bool // constant time
```
- Produces (package `store`):
```go
type AppPassword struct {
	ID, UserID, Label, Hash string
	CreatedAt               time.Time
	LastUsedAt              *time.Time
}
type AppPasswordStore interface {
	Create(ctx context.Context, p *AppPassword) error
	Get(ctx context.Context, id string) (*AppPassword, error)
	ListByUser(ctx context.Context, userID string) ([]*AppPassword, error)
	Delete(ctx context.Context, userID, id string) error // ErrNotFound if not the user's
	DeleteByUser(ctx context.Context, userID string) error
	TouchLastUsed(ctx context.Context, id string, at time.Time) error
}
// Store gains: AppPasswords() AppPasswordStore
```

- [ ] **Step 1: Write failing tests.** `internal/apppass/apppass_test.go`:

```go
package apppass

import (
	"strings"
	"testing"
)

func TestGenerateParseMatch(t *testing.T) {
	id, token, hash, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, "kc_"+id+"_") || strings.Contains(hash, token) {
		t.Fatalf("token %q hash %q", token, hash)
	}
	gotID, secret, ok := Parse(token)
	if !ok || gotID != id || !Matches(hash, secret) {
		t.Fatalf("round trip failed: %v %q", ok, gotID)
	}
	if Matches(hash, secret+"x") {
		t.Fatal("wrong secret matched")
	}
}

func TestParseRejects(t *testing.T) {
	for _, s := range []string{"", "kc_", "kc_abc", "xx_abc_def", "kc__def", "kc_abc_"} {
		if _, _, ok := Parse(s); ok {
			t.Errorf("accepted %q", s)
		}
	}
}
```
`internal/store/apppasswords_test.go`:

```go
package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

func TestAppPasswords(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, u := range []string{"usr_a", "usr_b"} {
		if err := st.Users().CreateUser(ctx, &store.User{ID: u, Username: u, Role: "user", Status: "active", SSOProvider: "local"}); err != nil {
			t.Fatal(err)
		}
	}
	ap := st.AppPasswords()
	if err := ap.Create(ctx, &store.AppPassword{ID: "p1", UserID: "usr_a", Label: "phone", Hash: "h1"}); err != nil {
		t.Fatal(err)
	}
	ap.Create(ctx, &store.AppPassword{ID: "p2", UserID: "usr_a", Label: "laptop", Hash: "h2"})
	ap.Create(ctx, &store.AppPassword{ID: "p3", UserID: "usr_b", Label: "phone", Hash: "h3"})

	if err := ap.Delete(ctx, "usr_b", "p1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleting another user's password: %v", err)
	}
	if err := ap.TouchLastUsed(ctx, "p1", time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := ap.Get(ctx, "p1")
	if err != nil || got.LastUsedAt == nil || got.UserID != "usr_a" {
		t.Fatalf("get %+v %v", got, err)
	}
	if err := ap.DeleteByUser(ctx, "usr_a"); err != nil {
		t.Fatal(err)
	}
	if list, _ := ap.ListByUser(ctx, "usr_a"); len(list) != 0 {
		t.Fatalf("left %d", len(list))
	}
	if list, _ := ap.ListByUser(ctx, "usr_b"); len(list) != 1 {
		t.Fatalf("usr_b lost passwords: %d", len(list))
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/apppass/ ./internal/store/ -run 'Generate|Parse|AppPasswords'`
Expected: FAIL to compile.

- [ ] **Step 3: Implement** `internal/apppass/apppass.go`

```go
// Package apppass makes and checks app passwords for native CalDAV clients.
//
// The secret is 256 random bits, so a fast hash is enough: there is nothing to
// brute-force offline. The id lets a login find its one row without scanning.
package apppass

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"strings"
)

var enc = base32.StdEncoding.WithPadding(base32.NoPadding)

func Generate() (id, token, hash string, err error) {
	idBytes := make([]byte, 10)
	secretBytes := make([]byte, 32)
	if _, err = rand.Read(idBytes); err != nil {
		return
	}
	if _, err = rand.Read(secretBytes); err != nil {
		return
	}
	id = strings.ToLower(enc.EncodeToString(idBytes))
	secret := strings.ToLower(enc.EncodeToString(secretBytes))
	return id, "kc_" + id + "_" + secret, hashSecret(secret), nil
}

func Parse(token string) (id, secret string, ok bool) {
	rest, ok := strings.CutPrefix(token, "kc_")
	if !ok {
		return "", "", false
	}
	id, secret, ok = strings.Cut(rest, "_")
	if !ok || id == "" || secret == "" {
		return "", "", false
	}
	return id, secret, true
}

func Matches(hash, secret string) bool {
	return subtle.ConstantTimeCompare([]byte(hash), []byte(hashSecret(secret))) == 1
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}
```

- [ ] **Step 4: Add migration v7**

```go
	{
		Version: 7,
		Name:    "app_passwords",
		SQLite: `
CREATE TABLE app_passwords (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    label TEXT NOT NULL,
    hash TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    last_used_at DATETIME
);
CREATE INDEX idx_app_passwords_user ON app_passwords(user_id);`,
		Postgres: `
CREATE TABLE app_passwords (
    id VARCHAR(64) PRIMARY KEY,
    user_id VARCHAR(64) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    label VARCHAR(64) NOT NULL,
    hash VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    last_used_at TIMESTAMPTZ
);
CREATE INDEX idx_app_passwords_user ON app_passwords(user_id);`,
	},
```
Check the `users.id` column type in migration v1; if it is not `TEXT`/`VARCHAR(64)`, match it.

- [ ] **Step 5: Wire the store.** Add `AppPassword` to `models.go`, `AppPasswordStore` + `AppPasswords() AppPasswordStore` to `store.go`, the field/constructor/accessor to `sqlstore.go` (same pattern as Task 8 Step 4). Implement `internal/store/apppasswords.go`:

```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type appPasswordStore struct{ store *SQLStore }

func (a *appPasswordStore) q(s string) string { return a.store.rebind(s) }

func scanAppPassword(row interface{ Scan(...any) error }) (*AppPassword, error) {
	var p AppPassword
	var last sql.NullTime
	err := row.Scan(&p.ID, &p.UserID, &p.Label, &p.Hash, &p.CreatedAt, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if last.Valid {
		p.LastUsedAt = &last.Time
	}
	return &p, err
}

const appPasswordCols = `id, user_id, label, hash, created_at, last_used_at`

func (a *appPasswordStore) Create(ctx context.Context, p *AppPassword) error {
	p.CreatedAt = time.Now().UTC()
	_, err := a.store.db.ExecContext(ctx, a.q(`INSERT INTO app_passwords (id, user_id, label, hash, created_at) VALUES (?, ?, ?, ?, ?)`),
		p.ID, p.UserID, p.Label, p.Hash, p.CreatedAt)
	return err
}

func (a *appPasswordStore) Get(ctx context.Context, id string) (*AppPassword, error) {
	return scanAppPassword(a.store.db.QueryRowContext(ctx, a.q(`SELECT `+appPasswordCols+` FROM app_passwords WHERE id = ?`), id))
}

func (a *appPasswordStore) ListByUser(ctx context.Context, userID string) ([]*AppPassword, error) {
	rows, err := a.store.db.QueryContext(ctx, a.q(`SELECT `+appPasswordCols+` FROM app_passwords WHERE user_id = ? ORDER BY created_at`), userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AppPassword
	for rows.Next() {
		p, err := scanAppPassword(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (a *appPasswordStore) Delete(ctx context.Context, userID, id string) error {
	res, err := a.store.db.ExecContext(ctx, a.q(`DELETE FROM app_passwords WHERE id = ? AND user_id = ?`), id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (a *appPasswordStore) DeleteByUser(ctx context.Context, userID string) error {
	_, err := a.store.db.ExecContext(ctx, a.q(`DELETE FROM app_passwords WHERE user_id = ?`), userID)
	return err
}

func (a *appPasswordStore) TouchLastUsed(ctx context.Context, id string, at time.Time) error {
	_, err := a.store.db.ExecContext(ctx, a.q(`UPDATE app_passwords SET last_used_at = ? WHERE id = ?`), at.UTC(), id)
	return err
}
```

- [ ] **Step 6: Revoke on deactivation and role change.** At each `DeleteUserSessions` call site, add the app-password deletion right after it:
  - `internal/scim/handler.go` near line 180: `_ = h.store.AppPasswords().DeleteByUser(r.Context(), user.ID)`
  - `internal/sso/kysignon.go` near lines 102 and 128: replace `return k.store.Sessions().DeleteUserSessions(ctx, existing.ID)` with

```go
		if err := k.store.Sessions().DeleteUserSessions(ctx, existing.ID); err != nil {
			return err
		}
		return k.store.AppPasswords().DeleteByUser(ctx, existing.ID)
```
  Then add a test in `internal/scim` (next to the existing deactivation test; find it with `grep -n 'inactive' internal/scim/*_test.go`) that creates an app password row, PATCHes `active:false`, and asserts `ListByUser` is empty.

- [ ] **Step 7: Run the tests**

Run: `go test ./internal/apppass/ ./internal/store/ ./internal/scim/ ./internal/sso/`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/apppass internal/store internal/scim internal/sso
git commit -m "feat: app password tokens and store; revoke on deactivation"
```

---

### Task 10: DAV authentication and app-password API

**Files:**
- Create: `internal/api/dav_auth.go`, `internal/api/app_passwords.go`, `internal/api/dav_auth_test.go`, `internal/api/app_passwords_test.go`
- Modify: `internal/api/server.go` (routes, fields)

**Interfaces:**
- Consumes: `apppass.Parse/Matches/Generate`, `store.AppPasswords()`, `s.sessions.AuthenticateRequest`, `s.requestIP(r)`.
- Produces:
```go
func (s *Server) withDAVAuth(next http.Handler) http.Handler // puts *store.User in context
func davUser(ctx context.Context) *store.User
func (s *Server) requireEveryday(h http.HandlerFunc) http.HandlerFunc
```
Routes: `GET /api/app-passwords`, `POST /api/app-passwords` (`{"label"}` → `{"id","label","password","created_at"}`), `DELETE /api/app-passwords/{id}`.

- [ ] **Step 1: Write the failing tests.** `internal/api/app_passwords_test.go`:

```go
package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/auth"
)

func doJSON(t *testing.T, srv *api.Server, method, path string, cookie *http.Cookie, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: "test-csrf"})
	req.Header.Set(auth.HeaderCSRF, "test-csrf")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

func TestAppPasswordLifecycle(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	alice := loginAs(t, srv, st, "alice", "user")
	w := doJSON(t, srv, "POST", "/api/app-passwords", alice, map[string]string{"label": "iPhone"})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created struct{ ID, Label, Password string }
	json.Unmarshal(w.Body.Bytes(), &created)
	if created.Password == "" || created.Label != "iPhone" {
		t.Fatalf("created %+v", created)
	}

	w = do(t, srv, "GET", "/api/app-passwords", alice)
	if w.Code != 200 || bytes.Contains(w.Body.Bytes(), []byte(created.Password)) || !bytes.Contains(w.Body.Bytes(), []byte("iPhone")) {
		t.Fatalf("list must not reveal the password: %s", w.Body.String())
	}

	if w = do(t, srv, "DELETE", "/api/app-passwords/"+created.ID, alice); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", w.Code)
	}
}

func TestAppPasswordRules(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	admin := loginAs(t, srv, st, "root", "admin")
	if w := doJSON(t, srv, "POST", "/api/app-passwords", admin, map[string]string{"label": "x"}); w.Code != http.StatusForbidden {
		t.Fatalf("admin must be refused: %d", w.Code)
	}
	alice := loginAs(t, srv, st, "alice", "user")
	if w := doJSON(t, srv, "POST", "/api/app-passwords", alice, map[string]string{"label": "  "}); w.Code != http.StatusBadRequest {
		t.Fatalf("blank label: %d", w.Code)
	}
	for i := 0; i < 20; i++ {
		doJSON(t, srv, "POST", "/api/app-passwords", alice, map[string]string{"label": "d"})
	}
	if w := doJSON(t, srv, "POST", "/api/app-passwords", alice, map[string]string{"label": "21st"}); w.Code != http.StatusConflict {
		t.Fatalf("21st password: %d", w.Code)
	}
	bob := loginAs(t, srv, st, "bob", "user")
	list, _ := st.AppPasswords().ListByUser(t.Context(), "usr_alice")
	if w := do(t, srv, "DELETE", "/api/app-passwords/"+list[0].ID, bob); w.Code != http.StatusNotFound {
		t.Fatalf("bob deleting alice's password: %d", w.Code)
	}
}
```
`internal/api/dav_auth_test.go`:

```go
package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/apppass"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// davUser creates an everyday user with one app password and returns the token.
func davUser(t *testing.T, st store.Store, username, role string) string {
	t.Helper()
	ctx := context.Background()
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_" + username, Username: username, Role: role, Status: "active", SSOProvider: "local"}); err != nil {
		t.Fatal(err)
	}
	id, token, hash, err := apppass.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AppPasswords().Create(ctx, &store.AppPassword{ID: id, UserID: "usr_" + username, Label: "t", Hash: hash}); err != nil {
		t.Fatal(err)
	}
	return token
}

func davDo(srv *api.Server, method, path, user, pass string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

func TestDAVAuthChallenge(t *testing.T) {
	srv, _, _ := setupTestServer(t)
	w := davDo(srv, "PROPFIND", "/dav/", "", "")
	if w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("want 401 challenge, got %d", w.Code)
	}
}

func TestDAVAuthWrongPassword(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	token := davUser(t, st, "alice", "user")
	if w := davDo(srv, "PROPFIND", "/dav/", "alice", token+"x"); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong secret: %d", w.Code)
	}
	other := davUser(t, st, "bob", "user")
	if w := davDo(srv, "PROPFIND", "/dav/", "alice", other); w.Code != http.StatusUnauthorized {
		t.Fatalf("bob's token as alice: %d", w.Code)
	}
}

func TestDAVAuthInactiveUser(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	token := davUser(t, st, "alice", "user")
	u, _ := st.Users().GetUserByUsername(context.Background(), "alice")
	u.Status = "inactive"
	if err := st.Users().UpdateUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if w := davDo(srv, "PROPFIND", "/dav/", "alice", token); w.Code != http.StatusUnauthorized {
		t.Fatalf("inactive user: %d", w.Code)
	}
}

func TestDAVAuthAdminRefused(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	token := davUser(t, st, "root", "admin")
	if w := davDo(srv, "PROPFIND", "/dav/", "root", token); w.Code != http.StatusForbidden {
		t.Fatalf("admin: %d", w.Code)
	}
}

func TestDAVAuthLockout(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	token := davUser(t, st, "alice", "user")
	for i := 0; i < 10; i++ {
		davDo(srv, "PROPFIND", "/dav/", "alice", "kc_bad_bad")
	}
	w := davDo(srv, "PROPFIND", "/dav/", "alice", token)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("want 429 after 10 failures, got %d", w.Code)
	}
}
```
Check `UpdateUser`'s exact name in `internal/store/store.go` (`UserStore`); use what is there.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/api/ -run 'AppPassword|DAVAuth'`
Expected: FAIL (404s / compile errors).

- [ ] **Step 3: Implement** `internal/api/dav_auth.go`

```go
package api

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Busnes-app/kycalendar/internal/apppass"
	"github.com/Busnes-app/kycalendar/internal/store"
)

const (
	davFailWindow   = 15 * time.Minute
	davFailsPerIP   = 10
	davFailsPerUser = 50
	davFailKeysCap  = 10000
)

// davFailures counts failed DAV logins per key; successes never count, so syncing phones are not throttled.
type davFailures struct {
	mu   sync.Mutex
	seen map[string]davFailure
}

type davFailure struct {
	count int
	since time.Time
}

func (f *davFailures) blocked(key string, limit int, now time.Time) (bool, time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.seen[key]
	if !ok || now.Sub(e.since) > davFailWindow {
		return false, 0
	}
	return e.count >= limit, davFailWindow - now.Sub(e.since)
}

func (f *davFailures) fail(key string, now time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.seen == nil || len(f.seen) >= davFailKeysCap {
		f.seen = map[string]davFailure{}
	}
	e := f.seen[key]
	if now.Sub(e.since) > davFailWindow {
		e = davFailure{since: now}
	}
	e.count++
	f.seen[key] = e
}

type davUserKey struct{}

func davUser(ctx context.Context) *store.User {
	u, _ := ctx.Value(davUserKey{}).(*store.User)
	return u
}

func davChallenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="KyCalendar", charset="UTF-8"`)
	http.Error(w, "Authentication required", http.StatusUnauthorized)
}

func (s *Server) withDAVAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, token, ok := r.BasicAuth()
		if !ok {
			davChallenge(w)
			return
		}
		now := time.Now()
		ipKey, userKey := "ip:"+s.requestIP(r), "user:"+username
		for _, k := range []struct {
			key   string
			limit int
		}{{ipKey, davFailsPerIP}, {userKey, davFailsPerUser}} {
			if blocked, wait := s.davFails.blocked(k.key, k.limit, now); blocked {
				w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
				http.Error(w, "Too many failed attempts", http.StatusTooManyRequests)
				return
			}
		}

		user, ok := s.checkAppPassword(r.Context(), username, token)
		if !ok {
			s.davFails.fail(ipKey, now)
			s.davFails.fail(userKey, now)
			_ = s.store.Audit().LogAudit(r.Context(), &store.AuditRecord{Action: "dav.auth_failed", Resource: "user:" + username, IPAddress: s.requestIP(r)})
			davChallenge(w)
			return
		}
		if user.Role == "admin" {
			http.Error(w, "Administrator accounts cannot use calendars", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), davUserKey{}, user)))
	})
}

// checkAppPassword resolves the user only if the token is theirs and they are active.
func (s *Server) checkAppPassword(ctx context.Context, username, token string) (*store.User, bool) {
	id, secret, ok := apppass.Parse(token)
	if !ok {
		return nil, false
	}
	p, err := s.store.AppPasswords().Get(ctx, id)
	if err != nil || !apppass.Matches(p.Hash, secret) {
		return nil, false
	}
	user, err := s.store.Users().GetUserByUsername(ctx, username)
	if err != nil || user.ID != p.UserID || user.Status != "active" {
		return nil, false
	}
	_ = s.store.AppPasswords().TouchLastUsed(ctx, p.ID, time.Now())
	return user, true
}
```
Add `davFails davFailures` as a field on `Server` in `server.go` (zero value is ready).

- [ ] **Step 4: Implement** `internal/api/app_passwords.go`

```go
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Busnes-app/kycalendar/internal/apppass"
	"github.com/Busnes-app/kycalendar/internal/auth"
	"github.com/Busnes-app/kycalendar/internal/store"
)

const maxAppPasswordsPerUser = 20

// requireEveryday admits signed-in non-admin users; admin identities never use calendars.
func (s *Server) requireEveryday(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _, err := s.sessions.AuthenticateRequest(r)
		if err != nil {
			if errors.Is(err, auth.ErrPasswordChangeRequired) {
				s.writeJSON(w, http.StatusForbidden, map[string]string{"error": "Change your password before continuing", "code": "password_change_required"})
			} else {
				s.writeError(w, http.StatusUnauthorized, "Authentication required")
			}
			return
		}
		if user.Role == "admin" {
			s.writeError(w, http.StatusForbidden, "Administrator accounts cannot use calendars")
			return
		}
		h(w, r)
	}
}

type appPasswordView struct {
	ID         string     `json:"id"`
	Label      string     `json:"label"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

func (s *Server) handleListAppPasswords(w http.ResponseWriter, r *http.Request) {
	user, _, _ := s.sessions.AuthenticateRequest(r)
	list, err := s.store.AppPasswords().ListByUser(r.Context(), user.ID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Could not list app passwords")
		return
	}
	out := make([]appPasswordView, 0, len(list))
	for _, p := range list {
		out = append(out, appPasswordView{ID: p.ID, Label: p.Label, CreatedAt: p.CreatedAt, LastUsedAt: p.LastUsedAt})
	}
	s.writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateAppPassword(w http.ResponseWriter, r *http.Request) {
	user, _, _ := s.sessions.AuthenticateRequest(r)
	var req struct {
		Label string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	label := strings.TrimSpace(req.Label)
	if label == "" || len([]rune(label)) > 64 {
		s.writeError(w, http.StatusBadRequest, "Label must be 1 to 64 characters")
		return
	}
	existing, err := s.store.AppPasswords().ListByUser(r.Context(), user.ID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Could not create app password")
		return
	}
	if len(existing) >= maxAppPasswordsPerUser {
		s.writeError(w, http.StatusConflict, "Revoke an app password before creating another")
		return
	}
	id, token, hash, err := apppass.Generate()
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Could not create app password")
		return
	}
	p := &store.AppPassword{ID: id, UserID: user.ID, Label: label, Hash: hash}
	if err := s.store.AppPasswords().Create(r.Context(), p); err != nil {
		s.writeError(w, http.StatusInternalServerError, "Could not create app password")
		return
	}
	_ = s.store.Audit().LogAudit(r.Context(), &store.AuditRecord{UserID: user.ID, Action: "app_password.create", Resource: "app_password:" + id, IPAddress: s.requestIP(r)})
	s.writeJSON(w, http.StatusCreated, map[string]any{"id": id, "label": label, "password": token, "created_at": p.CreatedAt})
}

func (s *Server) handleDeleteAppPassword(w http.ResponseWriter, r *http.Request) {
	user, _, _ := s.sessions.AuthenticateRequest(r)
	id := r.PathValue("id")
	if err := s.store.AppPasswords().Delete(r.Context(), user.ID, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "App password not found")
		} else {
			s.writeError(w, http.StatusInternalServerError, "Could not revoke app password")
		}
		return
	}
	_ = s.store.Audit().LogAudit(r.Context(), &store.AuditRecord{UserID: user.ID, Action: "app_password.revoke", Resource: "app_password:" + id, IPAddress: s.requestIP(r)})
	w.WriteHeader(http.StatusNoContent)
}
```
Register in `routes()` before the SPA fallback:

```go
	// App passwords for native CalDAV clients. Everyday users only.
	s.mux.HandleFunc("GET /api/app-passwords", s.requireEveryday(s.handleListAppPasswords))
	s.mux.HandleFunc("POST /api/app-passwords", s.requireEveryday(s.handleCreateAppPassword))
	s.mux.HandleFunc("DELETE /api/app-passwords/{id}", s.requireEveryday(s.handleDeleteAppPassword))
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/api/ -run 'AppPassword|DAVAuth'`
Expected: `TestAppPassword*` PASS. `TestDAVAuth*` still fail with 404 until Task 11 mounts `/dav/`; that is expected here.

- [ ] **Step 6: Commit**

```bash
git add internal/api
git commit -m "feat(api): app password endpoints and DAV basic auth"
```

---

### Task 11: CalDAV backend and mounting

**Files:**
- Create: `internal/davbackend/backend.go`, `internal/davbackend/AGENTS.md`, `internal/api/dav.go`, `internal/api/dav_test.go`
- Modify: `internal/api/server.go` (`routes`, `ServeHTTP`), `internal/config/config.go`, `internal/config/AGENTS.md`

**Interfaces:**
- Consumes: `store.CalendarStore`, `calendar.Inspect`, `calendar.FormatSyncToken/ParseSyncToken`, `calendar.MaxObjectSize`, fork types from Tasks 3–6, `withDAVAuth`, `davUser`.
- Produces:
```go
package davbackend
type Backend struct {
	Store             store.Store
	User              *store.User
	MaxObjectsPerUser int
}
func WithIfMatch(ctx context.Context, etag string) context.Context
const Prefix = "/dav"
```
- Config: `Config.Calendar.MaxObjectsPerUser` from `KY_CALENDAR_MAX_OBJECTS_PER_USER` (default 20000, must be > 0).

- [ ] **Step 1: Confirm client and conditional APIs**

```bash
go doc github.com/emersion/go-webdav ConditionalMatch
go doc github.com/emersion/go-webdav HTTPClientWithBasicAuth
go doc github.com/emersion/go-webdav/caldav Client
```
Expected: `ConditionalMatch` has `IsSet() bool`, `IsWildcard() bool`, `ETag() (string, error)`; `HTTPClientWithBasicAuth(c HTTPClient, username, password string) HTTPClient`; `caldav.NewClient`, `FindCalendarHomeSet`, `FindCalendars`, `PutCalendarObject`, `GetCalendarObject`, `QueryCalendar`, and the embedded `FindCurrentUserPrincipal`. Adapt names below if they differ.

- [ ] **Step 2: Write the failing integration tests** in `internal/api/dav_test.go`

```go
package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"
)

const evA = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:evt-a\r\nDTSTAMP:20261001T000000Z\r\nDTSTART:20261007T090000Z\r\nDTEND:20261007T100000Z\r\nSUMMARY:A\r\nX-KEEP;P=1:value\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

func davClient(t *testing.T, ts *httptest.Server, user, pass string) *caldav.Client {
	t.Helper()
	c, err := caldav.NewClient(webdav.HTTPClientWithBasicAuth(ts.Client(), user, pass), ts.URL+"/dav/")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func rawDAV(t *testing.T, ts *httptest.Server, method, path, user, pass, body string, hdr map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
	req.SetBasicAuth(user, pass)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func readAll(r *http.Response) string {
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := r.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			return b.String()
		}
	}
}

func TestCalDAVRoundTrip(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	token := davUser(t, st, "alice", "user")
	ts := httptest.NewServer(srv)
	defer ts.Close()
	ctx := context.Background()
	c := davClient(t, ts, "alice", token)

	principal, err := c.FindCurrentUserPrincipal(ctx)
	if err != nil || principal != "/dav/usr_alice/" {
		t.Fatalf("principal %q %v", principal, err)
	}
	home, err := c.FindCalendarHomeSet(ctx, principal)
	if err != nil {
		t.Fatal(err)
	}
	cals, err := c.FindCalendars(ctx, home)
	if err != nil || len(cals) != 1 || cals[0].Path != "/dav/usr_alice/calendars/default/" {
		t.Fatalf("calendars %+v %v", cals, err)
	}

	resp := rawDAV(t, ts, "PUT", cals[0].Path+"evt-a.ics", "alice", token, evA, map[string]string{"Content-Type": "text/calendar", "If-None-Match": "*"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("PUT %d %s", resp.StatusCode, readAll(resp))
	}
	get := rawDAV(t, ts, "GET", cals[0].Path+"evt-a.ics", "alice", token, "", nil)
	if body := readAll(get); body != evA {
		t.Fatalf("GET must return the exact bytes:\n%q", body)
	}

	start := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	objs, err := c.QueryCalendar(ctx, cals[0].Path, &caldav.CalendarQuery{
		CompRequest: caldav.CalendarCompRequest{Name: "VCALENDAR", AllProps: true, AllComps: true},
		CompFilter: caldav.CompFilter{Name: "VCALENDAR", Comps: []caldav.CompFilter{{Name: "VEVENT", Start: start, End: start.Add(24 * time.Hour)}}},
	})
	if err != nil || len(objs) != 1 {
		t.Fatalf("query %d %v", len(objs), err)
	}
	if _, err := c.QueryCalendar(ctx, cals[0].Path, &caldav.CalendarQuery{
		CompRequest: caldav.CalendarCompRequest{Name: "VCALENDAR"},
		CompFilter:  caldav.CompFilter{Name: "VCALENDAR", Comps: []caldav.CompFilter{{Name: "VEVENT", Start: start.AddDate(1, 0, 0), End: start.AddDate(1, 0, 1)}}},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCalDAVSyncCollection(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	token := davUser(t, st, "alice", "user")
	ts := httptest.NewServer(srv)
	defer ts.Close()
	cal := "/dav/usr_alice/calendars/default/"
	sync := func(tok string) (int, string) {
		body := `<d:sync-collection xmlns:d="DAV:"><d:sync-token>` + tok + `</d:sync-token><d:sync-level>1</d:sync-level><d:prop><d:getetag/></d:prop></d:sync-collection>`
		r := rawDAV(t, ts, "REPORT", cal, "alice", token, body, map[string]string{"Content-Type": "application/xml"})
		return r.StatusCode, readAll(r)
	}
	code, body := sync("")
	if code != 207 {
		t.Fatalf("initial sync %d %s", code, body)
	}
	first := between(body, "<sync-token>", "</sync-token>")
	rawDAV(t, ts, "PUT", cal+"evt-a.ics", "alice", token, evA, map[string]string{"Content-Type": "text/calendar"})
	code, body = sync(first)
	if code != 207 || !strings.Contains(body, "evt-a.ics") {
		t.Fatalf("delta after PUT %d %s", code, body)
	}
	second := between(body, "<sync-token>", "</sync-token>")
	rawDAV(t, ts, "DELETE", cal+"evt-a.ics", "alice", token, "", nil)
	code, body = sync(second)
	if code != 207 || !strings.Contains(body, "evt-a.ics") || !strings.Contains(body, "404") {
		t.Fatalf("delta after DELETE %d %s", code, body)
	}
}

func between(s, a, b string) string {
	i := strings.Index(s, a)
	if i < 0 {
		return ""
	}
	s = s[i+len(a):]
	if j := strings.Index(s, b); j >= 0 {
		return s[:j]
	}
	return ""
}

func TestCalDAVSyncBadToken(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	token := davUser(t, st, "alice", "user")
	ts := httptest.NewServer(srv)
	defer ts.Close()
	for _, tok := range []string{"garbage", "urn:kycalendar:sync:otherepoch:1"} {
		body := `<d:sync-collection xmlns:d="DAV:"><d:sync-token>` + tok + `</d:sync-token><d:sync-level>1</d:sync-level><d:prop><d:getetag/></d:prop></d:sync-collection>`
		r := rawDAV(t, ts, "REPORT", "/dav/usr_alice/calendars/default/", "alice", token, body, map[string]string{"Content-Type": "application/xml"})
		if b := readAll(r); r.StatusCode != 403 || !strings.Contains(b, "valid-sync-token") {
			t.Fatalf("token %q: %d %s", tok, r.StatusCode, b)
		}
	}
}

func TestCalDAVStaleETag(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	token := davUser(t, st, "alice", "user")
	ts := httptest.NewServer(srv)
	defer ts.Close()
	p := "/dav/usr_alice/calendars/default/evt-a.ics"
	rawDAV(t, ts, "PUT", p, "alice", token, evA, map[string]string{"Content-Type": "text/calendar"})
	if r := rawDAV(t, ts, "PUT", p, "alice", token, evA, map[string]string{"Content-Type": "text/calendar", "If-Match": `"stale"`}); r.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("stale If-Match: %d", r.StatusCode)
	}
	if r := rawDAV(t, ts, "PUT", p, "alice", token, evA, map[string]string{"Content-Type": "text/calendar", "If-None-Match": "*"}); r.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("If-None-Match on existing: %d", r.StatusCode)
	}
	if r := rawDAV(t, ts, "DELETE", p, "alice", token, "", map[string]string{"If-Match": `"stale"`}); r.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("stale DELETE: %d", r.StatusCode)
	}
}

func TestCalDAVOtherUsersPathForbidden(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	alice := davUser(t, st, "alice", "user")
	davUser(t, st, "bob", "user")
	ts := httptest.NewServer(srv)
	defer ts.Close()
	for _, m := range []string{"PROPFIND", "GET", "PUT", "DELETE", "REPORT"} {
		r := rawDAV(t, ts, m, "/dav/usr_bob/calendars/default/", "alice", alice, "", nil)
		if r.StatusCode != http.StatusForbidden {
			t.Fatalf("%s on bob's calendar as alice: %d", m, r.StatusCode)
		}
	}
}

func TestCalDAVRejectsBadData(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	token := davUser(t, st, "alice", "user")
	ts := httptest.NewServer(srv)
	defer ts.Close()
	cal := "/dav/usr_alice/calendars/default/"
	todo := strings.ReplaceAll(evA, "VEVENT", "VTODO")
	if r := rawDAV(t, ts, "PUT", cal+"t.ics", "alice", token, todo, map[string]string{"Content-Type": "text/calendar"}); r.StatusCode != 409 || !strings.Contains(readAll(r), "supported-calendar-component") {
		t.Fatalf("VTODO: %d", r.StatusCode)
	}
	rawDAV(t, ts, "PUT", cal+"a.ics", "alice", token, evA, map[string]string{"Content-Type": "text/calendar"})
	if r := rawDAV(t, ts, "PUT", cal+"b.ics", "alice", token, evA, map[string]string{"Content-Type": "text/calendar"}); r.StatusCode != 409 || !strings.Contains(readAll(r), "no-uid-conflict") {
		t.Fatalf("UID reuse: %d", r.StatusCode)
	}
	big := strings.Replace(evA, "SUMMARY:A", "SUMMARY:"+strings.Repeat("x", 1<<20), 1)
	if r := rawDAV(t, ts, "PUT", cal+"big.ics", "alice", token, big, map[string]string{"Content-Type": "text/calendar"}); r.StatusCode != 409 || !strings.Contains(readAll(r), "max-resource-size") {
		t.Fatalf("oversize: %d", r.StatusCode)
	}
	if r := rawDAV(t, ts, "DELETE", cal, "alice", token, "", nil); r.StatusCode != http.StatusForbidden {
		t.Fatalf("deleting a calendar over DAV: %d", r.StatusCode)
	}
}

func TestCalDAVUnknownTZIDQueryable(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	token := davUser(t, st, "alice", "user")
	ts := httptest.NewServer(srv)
	defer ts.Close()
	ev := strings.NewReplacer("DTSTART:20261007T090000Z", "DTSTART;TZID=Eastern Standard Time:20261007T090000", "DTEND:20261007T100000Z", "DTEND;TZID=Eastern Standard Time:20261007T100000").Replace(evA)
	cal := "/dav/usr_alice/calendars/default/"
	if r := rawDAV(t, ts, "PUT", cal+"evt-a.ics", "alice", token, ev, map[string]string{"Content-Type": "text/calendar"}); r.StatusCode != http.StatusCreated {
		t.Fatalf("PUT Windows TZID: %d %s", r.StatusCode, readAll(r))
	}
	c := davClient(t, ts, "alice", token)
	start := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	objs, err := c.QueryCalendar(context.Background(), cal, &caldav.CalendarQuery{
		CompRequest: caldav.CalendarCompRequest{Name: "VCALENDAR"},
		CompFilter:  caldav.CompFilter{Name: "VCALENDAR", Comps: []caldav.CompFilter{{Name: "VEVENT", Start: start, End: start.Add(3 * time.Hour)}}},
	})
	if err != nil || len(objs) != 1 {
		t.Fatalf("Windows-TZID event missing from range query: %d %v", len(objs), err)
	}
}

func TestCalDAVMkcalendarAndProppatch(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	token := davUser(t, st, "alice", "user")
	ts := httptest.NewServer(srv)
	defer ts.Close()
	body := `<c:mkcalendar xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:set><d:prop><d:displayname>Work</d:displayname></d:prop></d:set></c:mkcalendar>`
	if r := rawDAV(t, ts, "MKCALENDAR", "/dav/usr_alice/calendars/work/", "alice", token, body, map[string]string{"Content-Type": "application/xml"}); r.StatusCode != http.StatusCreated {
		t.Fatalf("MKCALENDAR %d %s", r.StatusCode, readAll(r))
	}
	patch := `<d:propertyupdate xmlns:d="DAV:" xmlns:ic="http://apple.com/ns/ical/"><d:set><d:prop><ic:calendar-color>#ff8800</ic:calendar-color></d:prop></d:set></d:propertyupdate>`
	if r := rawDAV(t, ts, "PROPPATCH", "/dav/usr_alice/calendars/work/", "alice", token, patch, map[string]string{"Content-Type": "application/xml"}); r.StatusCode != 207 {
		t.Fatalf("PROPPATCH %d", r.StatusCode)
	}
	cal, err := st.Calendars().GetCalendarBySlug(context.Background(), "user", "usr_alice", "work")
	if err != nil || cal.Name != "Work" || cal.Color != "#ff8800" {
		t.Fatalf("stored %+v %v", cal, err)
	}
}

func TestDAVWellKnownAndOptions(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	token := davUser(t, st, "alice", "user")
	ts := httptest.NewServer(srv)
	defer ts.Close()
	client := ts.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, _ := http.NewRequest("PROPFIND", ts.URL+"/.well-known/caldav", nil)
	req.SetBasicAuth("alice", token)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusPermanentRedirect || resp.Header.Get("Location") != "/dav/usr_alice/" {
		t.Fatalf("well-known: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	opt := rawDAV(t, ts, "OPTIONS", "/dav/usr_alice/calendars/default/", "alice", token, "", nil)
	if !strings.Contains(opt.Header.Get("DAV"), "calendar-access") {
		t.Fatalf("OPTIONS DAV header %q", opt.Header.Get("DAV"))
	}
}
```

- [ ] **Step 3: Run to verify failure**

Run: `go test ./internal/api/ -run 'CalDAV|DAV'`
Expected: FAIL (404s).

- [ ] **Step 4: Add config.** In `internal/config/config.go` add:

```go
type CalendarConfig struct {
	MaxObjectsPerUser int
}
```
a `Calendar CalendarConfig` field on `Config`, and in `LoadFromEnv`:

```go
		Calendar: CalendarConfig{
			MaxObjectsPerUser: getEnvInt("KY_CALENDAR_MAX_OBJECTS_PER_USER", 20000),
		},
```
followed by validation next to the existing checks:

```go
	if cfg.Calendar.MaxObjectsPerUser <= 0 {
		return nil, fmt.Errorf("KY_CALENDAR_MAX_OBJECTS_PER_USER must be positive")
	}
```
Document the variable in `internal/config/AGENTS.md`.

- [ ] **Step 5: Implement** `internal/davbackend/backend.go`

```go
// Package davbackend maps CalDAV onto the calendar store for one authenticated user.
package davbackend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"

	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/store"
)

const Prefix = "/dav"

const ownerUser = "user"

var slugPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// Backend is built per request; User is the authenticated, active, non-admin user.
type Backend struct {
	Store             store.Store
	User              *store.User
	MaxObjectsPerUser int
}

type ifMatchKey struct{}

// WithIfMatch carries a DELETE If-Match header, which go-webdav does not pass to backends.
func WithIfMatch(ctx context.Context, etag string) context.Context {
	return context.WithValue(ctx, ifMatchKey{}, strings.Trim(etag, `"`))
}

func ifMatchFrom(ctx context.Context) string {
	v, _ := ctx.Value(ifMatchKey{}).(string)
	return v
}

func (b *Backend) principal() string { return Prefix + "/" + b.User.ID + "/" }
func (b *Backend) home() string      { return b.principal() + "calendars/" }

func (b *Backend) CurrentUserPrincipal(ctx context.Context) (string, error) { return b.principal(), nil }
func (b *Backend) CalendarHomeSetPath(ctx context.Context) (string, error) { return b.home(), nil }

// split parses "<home><slug>/<name>"; name is empty for the calendar itself.
func (b *Backend) split(p string) (slug, name string, err error) {
	rest, ok := strings.CutPrefix(p, b.home())
	if !ok {
		return "", "", webdav.NewHTTPError(http.StatusForbidden, errors.New("path outside the user's calendars"))
	}
	slug, name, _ = strings.Cut(rest, "/")
	if !slugPattern.MatchString(slug) || strings.Contains(name, "/") {
		return "", "", webdav.NewHTTPError(http.StatusNotFound, errors.New("no such calendar"))
	}
	return slug, name, nil
}

func (b *Backend) ensureDefault(ctx context.Context) error {
	cals, err := b.Store.Calendars().ListCalendarsByOwner(ctx, ownerUser, b.User.ID)
	if err != nil || len(cals) > 0 {
		return err
	}
	err = b.Store.Calendars().CreateCalendar(ctx, &store.Calendar{
		ID: "cal_" + uuid.NewString(), OwnerKind: ownerUser, OwnerID: b.User.ID,
		Slug: "default", Name: "Calendar",
	})
	if errors.Is(err, store.ErrAlreadyExists) {
		return nil
	}
	return err
}

func (b *Backend) toDAV(ctx context.Context, c *store.Calendar) (caldav.Calendar, error) {
	epoch, err := b.Store.Calendars().SyncEpoch(ctx)
	if err != nil {
		return caldav.Calendar{}, err
	}
	return caldav.Calendar{
		Path:                  b.home() + c.Slug + "/",
		Name:                  c.Name,
		Description:           c.Description,
		Color:                 c.Color,
		CTag:                  strconv.FormatInt(c.Seq, 10),
		SyncToken:             calendar.FormatSyncToken(epoch, c.Seq),
		MaxResourceSize:       calendar.MaxObjectSize,
		SupportedComponentSet: []string{ical.CompEvent},
	}, nil
}

func (b *Backend) calendar(ctx context.Context, slug string) (*store.Calendar, error) {
	c, err := b.Store.Calendars().GetCalendarBySlug(ctx, ownerUser, b.User.ID, slug)
	if errors.Is(err, store.ErrNotFound) {
		return nil, webdav.NewHTTPError(http.StatusNotFound, err)
	}
	return c, err
}

func (b *Backend) ListCalendars(ctx context.Context) ([]caldav.Calendar, error) {
	if err := b.ensureDefault(ctx); err != nil {
		return nil, err
	}
	cals, err := b.Store.Calendars().ListCalendarsByOwner(ctx, ownerUser, b.User.ID)
	if err != nil {
		return nil, err
	}
	out := make([]caldav.Calendar, 0, len(cals))
	for _, c := range cals {
		dc, err := b.toDAV(ctx, c)
		if err != nil {
			return nil, err
		}
		out = append(out, dc)
	}
	return out, nil
}

func (b *Backend) GetCalendar(ctx context.Context, p string) (*caldav.Calendar, error) {
	if err := b.ensureDefault(ctx); err != nil {
		return nil, err
	}
	slug, _, err := b.split(p)
	if err != nil {
		return nil, err
	}
	c, err := b.calendar(ctx, slug)
	if err != nil {
		return nil, err
	}
	dc, err := b.toDAV(ctx, c)
	return &dc, err
}

func (b *Backend) CreateCalendar(ctx context.Context, cal *caldav.Calendar) error {
	slug, name, err := b.split(cal.Path)
	if err != nil || name != "" {
		return webdav.NewHTTPError(http.StatusForbidden, errors.New("calendars live directly under the home set"))
	}
	display := cal.Name
	if display == "" {
		display = slug
	}
	err = b.Store.Calendars().CreateCalendar(ctx, &store.Calendar{
		ID: "cal_" + uuid.NewString(), OwnerKind: ownerUser, OwnerID: b.User.ID,
		Slug: slug, Name: display, Description: cal.Description, Color: cal.Color,
	})
	if errors.Is(err, store.ErrAlreadyExists) {
		return webdav.NewHTTPError(http.StatusMethodNotAllowed, err)
	}
	return err
}

func (b *Backend) UpdateCalendar(ctx context.Context, p string, u *caldav.CalendarUpdate) error {
	slug, _, err := b.split(p)
	if err != nil {
		return err
	}
	c, err := b.calendar(ctx, slug)
	if err != nil {
		return err
	}
	return b.Store.Calendars().UpdateCalendar(ctx, c.ID, u.Name, u.Description, u.Color)
}

func (b *Backend) toObject(slug string, o *store.CalendarObject) (*caldav.CalendarObject, error) {
	data, err := ical.NewDecoder(bytes.NewReader(o.Data)).Decode()
	if err != nil {
		return nil, fmt.Errorf("stored object %s/%s does not parse: %w", slug, o.Name, err)
	}
	return &caldav.CalendarObject{
		Path: b.home() + slug + "/" + o.Name, ModTime: o.ModifiedAt, ContentLength: int64(len(o.Data)),
		ETag: o.ETag, Data: data, Raw: o.Data,
	}, nil
}

func (b *Backend) objectAt(ctx context.Context, p string) (string, *store.Calendar, string, error) {
	slug, name, err := b.split(p)
	if err != nil {
		return "", nil, "", err
	}
	c, err := b.calendar(ctx, slug)
	return slug, c, name, err
}

func (b *Backend) GetCalendarObject(ctx context.Context, p string, req *caldav.CalendarCompRequest) (*caldav.CalendarObject, error) {
	slug, c, name, err := b.objectAt(ctx, p)
	if err != nil {
		return nil, err
	}
	o, err := b.Store.Calendars().GetObject(ctx, c.ID, name)
	if errors.Is(err, store.ErrNotFound) {
		return nil, webdav.NewHTTPError(http.StatusNotFound, err)
	}
	if err != nil {
		return nil, err
	}
	return b.toObject(slug, o)
}

func (b *Backend) convert(slug string, list []*store.CalendarObject) ([]caldav.CalendarObject, error) {
	out := make([]caldav.CalendarObject, 0, len(list))
	for _, o := range list {
		co, err := b.toObject(slug, o)
		if err != nil {
			return nil, err
		}
		out = append(out, *co)
	}
	return out, nil
}

func (b *Backend) ListCalendarObjects(ctx context.Context, p string, req *caldav.CalendarCompRequest) ([]caldav.CalendarObject, error) {
	slug, c, _, err := b.objectAt(ctx, p)
	if err != nil {
		return nil, err
	}
	list, err := b.Store.Calendars().ListObjects(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	return b.convert(slug, list)
}

func (b *Backend) QueryCalendarObjects(ctx context.Context, p string, q *caldav.CalendarQuery) ([]caldav.CalendarObject, error) {
	slug, c, _, err := b.objectAt(ctx, p)
	if err != nil {
		return nil, err
	}
	var list []*store.CalendarObject
	if r, ok := eventRange(q); ok {
		list, err = b.Store.Calendars().ListObjectsInRange(ctx, c.ID, r[0], r[1])
	} else {
		list, err = b.Store.Calendars().ListObjects(ctx, c.ID)
	}
	if err != nil {
		return nil, err
	}
	objs, err := b.convert(slug, list)
	if err != nil {
		return nil, err
	}
	out := objs[:0]
	for i := range objs {
		// Keep an object when matching errors (e.g. an unknown TZID): an extra result is harmless, a missing one is not.
		if ok, err := caldav.Match(q.CompFilter, &objs[i]); ok || err != nil {
			out = append(out, objs[i])
		}
	}
	return out, nil
}

// eventRange returns the VEVENT time-range of a query as unix seconds, if it has one.
func eventRange(q *caldav.CalendarQuery) ([2]int64, bool) {
	for _, cf := range q.CompFilter.Comps {
		if cf.Name == ical.CompEvent && !cf.Start.IsZero() {
			end := int64(1<<62)
			if !cf.End.IsZero() {
				end = cf.End.Unix()
			}
			return [2]int64{cf.Start.Unix(), end}, true
		}
	}
	return [2]int64{}, false
}

func (b *Backend) PutCalendarObject(ctx context.Context, p string, cal *ical.Calendar, opts *caldav.PutCalendarObjectOptions) (*caldav.CalendarObject, error) {
	slug, c, name, err := b.objectAt(ctx, p)
	if err != nil {
		return nil, err
	}
	if name == "" {
		return nil, webdav.NewHTTPError(http.StatusMethodNotAllowed, errors.New("PUT needs an object name"))
	}
	if _, _, err := caldav.ValidateCalendarObject(cal); err != nil {
		return nil, caldav.NewPreconditionError(caldav.PreconditionValidCalendarObjectResource)
	}
	info, err := calendar.Inspect(cal)
	switch {
	case errors.Is(err, calendar.ErrUnsupportedComponent):
		return nil, caldav.NewPreconditionError(caldav.PreconditionSupportedCalendarComponent)
	case err != nil:
		return nil, caldav.NewPreconditionError(caldav.PreconditionValidCalendarData)
	}
	if _, err := b.Store.Calendars().GetObject(ctx, c.ID, name); errors.Is(err, store.ErrNotFound) {
		n, err := b.Store.Calendars().CountObjectsByOwner(ctx, ownerUser, b.User.ID)
		if err != nil {
			return nil, err
		}
		if n >= b.MaxObjectsPerUser {
			return nil, webdav.NewHTTPError(http.StatusInsufficientStorage, errors.New("calendar quota reached"))
		}
	} else if err != nil {
		return nil, err
	}

	ifMatch := ""
	if opts.IfMatch.IsSet() && !opts.IfMatch.IsWildcard() {
		if ifMatch, err = opts.IfMatch.ETag(); err != nil {
			return nil, webdav.NewHTTPError(http.StatusBadRequest, err)
		}
	}
	ifNoneMatch := opts.IfNoneMatch.IsSet() && opts.IfNoneMatch.IsWildcard()
	o := &store.CalendarObject{CalendarID: c.ID, Name: name, UID: info.UID, Data: opts.Raw, FirstStart: info.FirstStart, LastEnd: info.LastEnd}
	_, err = b.Store.Calendars().PutObject(ctx, o, ifMatch, ifNoneMatch)
	switch {
	case errors.Is(err, store.ErrPreconditionFailed):
		return nil, webdav.NewHTTPError(http.StatusPreconditionFailed, err)
	case errors.Is(err, store.ErrUIDConflict):
		return nil, caldav.NewPreconditionError(caldav.PreconditionNoUIDConflict)
	case err != nil:
		return nil, err
	}
	return &caldav.CalendarObject{Path: b.home() + slug + "/" + name, ETag: o.ETag, ModTime: o.ModifiedAt}, nil
}

func (b *Backend) DeleteCalendarObject(ctx context.Context, p string) error {
	_, c, name, err := b.objectAt(ctx, p)
	if err != nil {
		return err
	}
	if name == "" {
		return webdav.NewHTTPError(http.StatusForbidden, errors.New("calendars cannot be deleted over CalDAV"))
	}
	err = b.Store.Calendars().DeleteObject(ctx, c.ID, name, ifMatchFrom(ctx))
	switch {
	case errors.Is(err, store.ErrNotFound):
		return webdav.NewHTTPError(http.StatusNotFound, err)
	case errors.Is(err, store.ErrPreconditionFailed):
		return webdav.NewHTTPError(http.StatusPreconditionFailed, err)
	}
	return err
}

func (b *Backend) SyncCalendar(ctx context.Context, p, token string) (*caldav.SyncResult, error) {
	slug, c, _, err := b.objectAt(ctx, p)
	if err != nil {
		return nil, err
	}
	epoch, err := b.Store.Calendars().SyncEpoch(ctx)
	if err != nil {
		return nil, err
	}
	since := int64(0)
	if token != "" {
		tokEpoch, seq, err := calendar.ParseSyncToken(token)
		if err != nil || tokEpoch != epoch || seq > c.Seq {
			return nil, caldav.ErrInvalidSyncToken
		}
		since = seq
	}

	res := &caldav.SyncResult{SyncToken: calendar.FormatSyncToken(epoch, since)}
	if token == "" {
		list, err := b.Store.Calendars().ListObjects(ctx, c.ID)
		if err != nil {
			return nil, err
		}
		if res.Updated, err = b.convert(slug, list); err != nil {
			return nil, err
		}
		res.SyncToken = calendar.FormatSyncToken(epoch, c.Seq)
		return res, nil
	}

	changes, err := b.Store.Calendars().ChangesSince(ctx, c.ID, since)
	if errors.Is(err, store.ErrSyncTokenExpired) {
		return nil, caldav.ErrInvalidSyncToken
	}
	if err != nil {
		return nil, err
	}
	latest := map[string]bool{} // name -> deleted, last change wins
	maxSeq := since
	for _, ch := range changes {
		latest[ch.Name] = ch.Deleted
		maxSeq = ch.Seq
	}
	for name, deleted := range latest {
		path := b.home() + slug + "/" + name
		if deleted {
			res.Deleted = append(res.Deleted, path)
			continue
		}
		o, err := b.Store.Calendars().GetObject(ctx, c.ID, name)
		if errors.Is(err, store.ErrNotFound) {
			res.Deleted = append(res.Deleted, path)
			continue
		}
		if err != nil {
			return nil, err
		}
		co, err := b.toObject(slug, o)
		if err != nil {
			return nil, err
		}
		res.Updated = append(res.Updated, *co)
	}
	res.SyncToken = calendar.FormatSyncToken(epoch, maxSeq)
	return res, nil
}
```
Add `"github.com/google/uuid"` to the imports (the base already depends on it). Two first requests racing in `ensureDefault` both try slug `default`; the loser gets `ErrAlreadyExists`, which is ignored.

- [ ] **Step 6: Mount it.** Create `internal/api/dav.go`:

```go
package api

import (
	"net/http"
	"strings"

	"github.com/emersion/go-webdav/caldav"

	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/davbackend"
)

func isDAVPath(p string) bool {
	return p == "/.well-known/caldav" || strings.HasPrefix(p, davbackend.Prefix+"/")
}

// handleDAV serves CalDAV for the user withDAVAuth put in the context.
func (s *Server) handleDAV(w http.ResponseWriter, r *http.Request) {
	user := davUser(r.Context())
	if rest, ok := strings.CutPrefix(r.URL.Path, davbackend.Prefix+"/"); ok && rest != "" {
		if owner, _, _ := strings.Cut(rest, "/"); owner != user.ID {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	}
	backend := &davbackend.Backend{Store: s.store, User: user, MaxObjectsPerUser: s.config.Calendar.MaxObjectsPerUser}
	h := &caldav.Handler{Backend: backend, Prefix: davbackend.Prefix, MaxResourceSize: calendar.MaxObjectSize}
	h.ServeHTTP(w, r.WithContext(davbackend.WithIfMatch(r.Context(), r.Header.Get("If-Match"))))
}
```
In `routes()`, before the SPA fallback:

```go
	// CalDAV for native clients; app-password Basic auth, never the session cookie.
	dav := s.withDAVAuth(http.HandlerFunc(s.handleDAV))
	s.mux.Handle("/dav/", dav)
	s.mux.Handle("/.well-known/caldav", dav)
```
In `ServeHTTP`, change the OPTIONS short-circuit and the body cap so DAV paths reach the mux with room for the fork's own limit:

```go
	if r.Method == http.MethodOptions && !isDAVPath(r.URL.Path) {
```
and

```go
	if r.Body != nil {
		limit := int64(1 << 20)
		if isDAVPath(r.URL.Path) {
			limit = calendar.MaxObjectSize + 1<<16 // the fork answers max-resource-size itself
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
	}
```
(add the `calendar` import to `server.go`).

- [ ] **Step 7: Run all API tests**

Run: `go test ./internal/api/`
Expected: PASS, including Task 10's `TestDAVAuth*`.

- [ ] **Step 8: Write `internal/davbackend/AGENTS.md`**

```markdown
# internal/davbackend

## Purpose
Maps CalDAV (the go-webdav fork) onto the calendar store for one authenticated user per request.

## Local Contracts
- Paths: `/dav/<user-id>/calendars/<slug>/<name>`; anything outside the user's home is 403.
- Plan 1 serves personal calendars only (`owner_kind = 'user'`); a default calendar is created on first listing.
- Bytes from PUT are stored as received; GET and calendar-data serve them unchanged.
- Calendars cannot be deleted over CalDAV (403).
- Query results keep objects whose filter match errors.

## Verification
- `go test ./internal/api/ -run 'CalDAV|DAV'`
```

- [ ] **Step 9: Commit**

```bash
git add internal/davbackend internal/api internal/config
git commit -m "feat: CalDAV server for personal calendars"
```

---

### Task 12: App passwords page

**Files:**
- Create: `web/src/pages/AppPasswords.tsx`, `web/src/pages/AppPasswords.test.tsx`
- Modify: `web/src/App.tsx`, `web/src/components/AppHeader.tsx`

**Interfaces:**
- Consumes: `secureFetch` from `web/src/api.ts`; `GET/POST/DELETE /api/app-passwords`.

- [ ] **Step 1: Write the failing test** `web/src/pages/AppPasswords.test.tsx`

```tsx
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import AppPasswords from './AppPasswords';

afterEach(() => vi.restoreAllMocks());

function mockFetch(handlers: Record<string, (init?: RequestInit) => unknown>) {
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const key = `${init?.method ?? 'GET'} ${String(input)}`;
    const handler = handlers[key];
    if (!handler) throw new Error(`unexpected ${key}`);
    const body = handler(init);
    return new Response(body === null ? null : JSON.stringify(body), { status: body === null ? 204 : 200 });
  });
}

describe('AppPasswords', () => {
  it('shows a new password once and lists only labels', async () => {
    let list: unknown[] = [];
    mockFetch({
      'GET /api/app-passwords': () => list,
      'POST /api/app-passwords': () => {
        list = [{ id: 'p1', label: 'iPhone', created_at: '2026-10-06T00:00:00Z', last_used_at: null }];
        return { id: 'p1', label: 'iPhone', password: 'kc_p1_secret', created_at: '2026-10-06T00:00:00Z' };
      },
    });
    render(<AppPasswords username="alice" />);
    fireEvent.change(screen.getByLabelText(/device name/i), { target: { value: 'iPhone' } });
    fireEvent.click(screen.getByRole('button', { name: /create/i }));
    expect(await screen.findByText('kc_p1_secret')).toBeTruthy();
    expect(screen.getByText('alice')).toBeTruthy();
    await waitFor(() => expect(screen.getAllByText('iPhone').length).toBeGreaterThan(0));
  });

  it('revokes a password', async () => {
    let list: unknown[] = [{ id: 'p1', label: 'iPhone', created_at: '2026-10-06T00:00:00Z', last_used_at: null }];
    const del = vi.fn(() => {
      list = [];
      return null;
    });
    mockFetch({ 'GET /api/app-passwords': () => list, 'DELETE /api/app-passwords/p1': del });
    render(<AppPasswords username="alice" />);
    fireEvent.click(await screen.findByRole('button', { name: /revoke iphone/i }));
    await waitFor(() => expect(del).toHaveBeenCalled());
  });
});
```

- [ ] **Step 2: Run to verify failure**

Run: `cd web && npx vitest run src/pages/AppPasswords.test.tsx`
Expected: FAIL (cannot resolve `./AppPasswords`).

- [ ] **Step 3: Implement** `web/src/pages/AppPasswords.tsx`

```tsx
import { useCallback, useEffect, useState } from 'react';
import { secureFetch } from '../api';

interface AppPassword {
  id: string;
  label: string;
  created_at: string;
  last_used_at: string | null;
}

export default function AppPasswords({ username }: { username: string }) {
  const [list, setList] = useState<AppPassword[]>([]);
  const [label, setLabel] = useState('');
  const [created, setCreated] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    const res = await secureFetch('/api/app-passwords');
    if (res.ok) setList(await res.json());
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  async function create(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    const res = await secureFetch('/api/app-passwords', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ label }),
    });
    if (!res.ok) {
      setError((await res.json().catch(() => null))?.error ?? 'Could not create the app password');
      return;
    }
    setCreated((await res.json()).password);
    setLabel('');
    await load();
  }

  async function revoke(id: string) {
    await secureFetch(`/api/app-passwords/${encodeURIComponent(id)}`, { method: 'DELETE' });
    await load();
  }

  return (
    <section className="page">
      <h1>Phones and apps</h1>
      <p>Each device gets its own app password. Revoke one and only that device stops syncing.</p>

      <form onSubmit={create}>
        <label>
          Device name
          <input value={label} onChange={(e) => setLabel(e.target.value)} maxLength={64} required />
        </label>
        <button type="submit">Create</button>
      </form>
      {error && <p role="alert">{error}</p>}

      {created && (
        <div role="status">
          <p>Enter these on the device now. The password is not shown again.</p>
          <dl>
            <dt>Server</dt>
            <dd>{window.location.host}</dd>
            <dt>Username</dt>
            <dd>{username}</dd>
            <dt>Password</dt>
            <dd><code>{created}</code></dd>
          </dl>
          <button type="button" onClick={() => setCreated(null)}>Done</button>
        </div>
      )}

      <h2>Setup</h2>
      <ul>
        <li>iPhone and iPad: Settings → Calendar → Accounts → Add Account → Other → Add CalDAV Account.</li>
        <li>Android: DAVx5 → Add account → Login with URL and user name, URL <code>https://{window.location.host}/</code>.</li>
        <li>Thunderbird: New Calendar → On the Network, location <code>https://{window.location.host}/</code>.</li>
      </ul>

      <h2>Devices</h2>
      {list.length === 0 ? (
        <p>No app passwords yet.</p>
      ) : (
        <ul>
          {list.map((p) => (
            <li key={p.id}>
              <span>{p.label}</span>{' '}
              <small>{p.last_used_at ? `last used ${new Date(p.last_used_at).toLocaleString()}` : 'never used'}</small>{' '}
              <button type="button" aria-label={`Revoke ${p.label}`} onClick={() => revoke(p.id)}>
                Revoke
              </button>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
```

- [ ] **Step 4: Wire navigation.** In `web/src/components/AppHeader.tsx` add a nav item (import `Smartphone` from `lucide-react`) shown only to non-admins:

```tsx
    ...(user?.role !== 'admin' ? [{ id: 'devices', label: 'Phones & apps', icon: Smartphone }] : []),
```
(check the `user` prop's type in `AppHeader.tsx`; it is passed from `App.tsx`). In `web/src/App.tsx` add the import and:

```tsx
        {activeTab === 'devices' && user && <AppPasswords username={user.username} />}
```

- [ ] **Step 5: Run web tests and build**

Run: `make test-web && make all`
Expected: PASS and `web/dist` rebuilt.

- [ ] **Step 6: Commit**

```bash
git add web
git commit -m "feat(web): phones and apps page for app passwords"
```

---

### Task 13: Prune job, docs, CI and a real-client smoke check

**Files:**
- Modify: `cmd/server/main.go`, `README.md`, `AGENTS.md`, `internal/api/AGENTS.md`, `internal/store/AGENTS.md`
- Create: `docs/evidence/2026-10-plan-1-smoke.md`

**Interfaces:**
- Consumes: `store.CalendarStore.PruneChanges`.

- [ ] **Step 1: Prune old change rows daily.** In `cmd/server/main.go`, after the store opens and before serving, start:

```go
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			if err := st.Calendars().PruneChanges(ctx, time.Now().Add(-90*24*time.Hour)); err != nil {
				log.Printf("calendar change prune failed: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
```
Use the names `main.go` already uses for the store, root context and logger.

- [ ] **Step 2: Update docs (DOX pass).**
  - `AGENTS.md` (`## KyCalendar`): add the Child DOX Index entries `internal/calendar/`, `internal/davbackend/`, and state the Plan 1 contracts: DAV path scheme, app-password token format and SHA-256 storage, admin refusal, VEVENT only, raw-byte storage.
  - `internal/api/AGENTS.md`: `/dav/` and `/.well-known/caldav` are exempt from the global OPTIONS answer and use a larger body cap; `/api/app-passwords` is everyday-only.
  - `internal/store/AGENTS.md`: migrations v6 and v7, `CalendarStore`, `AppPasswordStore`, the `min_sync_seq` rule.
  - `README.md`: a "Connect a phone" section mirroring the page's setup steps and `KY_CALENDAR_MAX_OBJECTS_PER_USER`.

- [ ] **Step 3: Run full CI**

Run: `make ci`
Expected: PASS.

- [ ] **Step 4: Smoke check with a real client** (manual, needs Yoshi's device; this is not the Plan 4 interop gate)

```bash
make run
```
Then: sign in as an everyday user, create an app password on "Phones & apps", add the account in DAVx5 (or Thunderbird) against the machine's HTTPS address, create an event on the device, confirm it appears via `GET /dav/<user-id>/calendars/default/`, edit it on the device, delete it, then revoke the password and confirm the next sync fails. Record each step's result, client name and version in `docs/evidence/2026-10-plan-1-smoke.md`. If a step fails, record the failure; do not mark the plan done.

- [ ] **Step 5: Commit**

```bash
git add cmd/server/main.go README.md AGENTS.md internal/*/AGENTS.md docs/evidence
git commit -m "docs: plan 1 contracts, prune job and smoke evidence"
```

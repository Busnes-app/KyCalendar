package api_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
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
	b, _ := io.ReadAll(r.Body)
	return string(b)
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
		CompFilter:  caldav.CompFilter{Name: "VCALENDAR", Comps: []caldav.CompFilter{{Name: "VEVENT", Start: start, End: start.Add(24 * time.Hour)}}},
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

// Unbounded recurrences skip caldav.Match: its expander can disagree with the indexer and drop them.
func TestCalDAVUnboundedRecurrenceQueryable(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	token := davUser(t, st, "alice", "user")
	ts := httptest.NewServer(srv)
	defer ts.Close()
	cal := "/dav/usr_alice/calendars/default/"
	ev := strings.Replace(evA, "SUMMARY:A", "SUMMARY:A\r\nRRULE:FREQ=SECONDLY;BYMONTH=2;BYMONTHDAY=30", 1)
	if r := rawDAV(t, ts, "PUT", cal+"evt-a.ics", "alice", token, ev, map[string]string{"Content-Type": "text/calendar"}); r.StatusCode != http.StatusCreated {
		t.Fatalf("PUT SECONDLY: %d %s", r.StatusCode, readAll(r))
	}
	c := davClient(t, ts, "alice", token)
	start := time.Date(2036, 1, 1, 0, 0, 0, 0, time.UTC)
	began := time.Now()
	objs, err := c.QueryCalendar(context.Background(), cal, &caldav.CalendarQuery{
		CompRequest: caldav.CalendarCompRequest{Name: "VCALENDAR"},
		CompFilter:  caldav.CompFilter{Name: "VCALENDAR", Comps: []caldav.CompFilter{{Name: "VEVENT", Start: start, End: start.Add(time.Hour)}}},
	})
	if err != nil || len(objs) != 1 {
		t.Fatalf("SECONDLY event missing from range query: %d %v", len(objs), err)
	}
	if d := time.Since(began); d > 2*time.Second {
		t.Fatalf("query took %s", d)
	}
}

// Bounded recurring objects skip caldav.Match in ranged queries too (R29): expanding a large
// series costs CPU on every query. The range sits between occurrences, so only the no-Match path returns it.
func TestCalDAVBoundedRecurrenceSkipsMatch(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	token := davUser(t, st, "alice", "user")
	ts := httptest.NewServer(srv)
	defer ts.Close()
	cal := "/dav/usr_alice/calendars/default/"
	ev := strings.Replace(strings.Replace(evA, "DTEND:20261007T100000Z", "DTEND:20261007T091000Z", 1),
		"SUMMARY:A", "SUMMARY:A\r\nRRULE:FREQ=HOURLY;COUNT=99000", 1)
	if r := rawDAV(t, ts, "PUT", cal+"evt-a.ics", "alice", token, ev, map[string]string{"Content-Type": "text/calendar"}); r.StatusCode != http.StatusCreated {
		t.Fatalf("PUT HOURLY: %d %s", r.StatusCode, readAll(r))
	}
	c := davClient(t, ts, "alice", token)
	start := time.Date(2036, 1, 1, 9, 20, 0, 0, time.UTC)
	began := time.Now()
	objs, err := c.QueryCalendar(context.Background(), cal, &caldav.CalendarQuery{
		CompRequest: caldav.CalendarCompRequest{Name: "VCALENDAR"},
		CompFilter:  caldav.CompFilter{Name: "VCALENDAR", Comps: []caldav.CompFilter{{Name: "VEVENT", Start: start, End: start.Add(30 * time.Minute)}}},
	})
	if err != nil || len(objs) != 1 {
		t.Fatalf("bounded series not returned conservatively: %d %v", len(objs), err)
	}
	if d := time.Since(began); d > 500*time.Millisecond {
		t.Fatalf("query took %s", d)
	}
}

func TestCalDAVIfMatch(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	token := davUser(t, st, "alice", "user")
	ts := httptest.NewServer(srv)
	defer ts.Close()
	p := "/dav/usr_alice/calendars/default/evt-a.ics"
	ics := map[string]string{"Content-Type": "text/calendar"}
	star := map[string]string{"Content-Type": "text/calendar", "If-Match": "*"}
	if r := rawDAV(t, ts, "PUT", p, "alice", token, evA, star); r.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("If-Match * PUT on missing: %d", r.StatusCode)
	}
	if r := rawDAV(t, ts, "DELETE", p, "alice", token, "", map[string]string{"If-Match": "*"}); r.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("If-Match * DELETE on missing: %d", r.StatusCode)
	}
	r := rawDAV(t, ts, "PUT", p, "alice", token, evA, ics)
	etag := r.Header.Get("ETag")
	if r.StatusCode != http.StatusCreated || etag == "" {
		t.Fatalf("create: %d %q", r.StatusCode, etag)
	}
	changed := strings.Replace(evA, "SUMMARY:A", "SUMMARY:B", 1)
	r = rawDAV(t, ts, "PUT", p, "alice", token, changed, map[string]string{"Content-Type": "text/calendar", "If-Match": etag})
	if r.StatusCode != http.StatusCreated || r.Header.Get("ETag") == etag {
		t.Fatalf("PUT with current ETag: %d", r.StatusCode)
	}
	etag = r.Header.Get("ETag")
	if r := rawDAV(t, ts, "PUT", p, "alice", token, evA, star); r.StatusCode != http.StatusCreated {
		t.Fatalf("If-Match * PUT on existing: %d", r.StatusCode)
	}
	etag = rawDAV(t, ts, "GET", p, "alice", token, "", nil).Header.Get("ETag")
	if r := rawDAV(t, ts, "DELETE", p, "alice", token, "", map[string]string{"If-Match": etag}); r.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE with current ETag: %d", r.StatusCode)
	}
	if r := rawDAV(t, ts, "GET", p, "alice", token, "", nil); r.StatusCode != http.StatusNotFound {
		t.Fatalf("after DELETE: %d", r.StatusCode)
	}
}

func TestCalDAVCalendarCap(t *testing.T) {
	t.Setenv("KY_CALENDAR_MAX_CALENDARS_PER_USER", "2")
	srv, st, _ := setupTestServer(t)
	token := davUser(t, st, "alice", "user")
	ts := httptest.NewServer(srv)
	defer ts.Close()
	for i, want := range []int{http.StatusCreated, http.StatusCreated, http.StatusInsufficientStorage} {
		p := "/dav/usr_alice/calendars/c" + string(rune('0'+i)) + "/"
		body := `<c:mkcalendar xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:set><d:prop><d:displayname>x</d:displayname></d:prop></d:set></c:mkcalendar>`
		if r := rawDAV(t, ts, "MKCALENDAR", p, "alice", token, body, map[string]string{"Content-Type": "application/xml"}); r.StatusCode != want {
			t.Fatalf("MKCALENDAR %d: %d, want %d", i, r.StatusCode, want)
		}
	}
}

func TestCalDAVByteQuota(t *testing.T) {
	t.Setenv("KY_CALENDAR_MAX_BYTES_PER_USER", strconv.Itoa(len(evA)*3/2))
	srv, st, _ := setupTestServer(t)
	token := davUser(t, st, "alice", "user")
	ts := httptest.NewServer(srv)
	defer ts.Close()
	cal := "/dav/usr_alice/calendars/default/"
	ics := map[string]string{"Content-Type": "text/calendar"}
	if r := rawDAV(t, ts, "PUT", cal+"a.ics", "alice", token, evA, ics); r.StatusCode != http.StatusCreated {
		t.Fatalf("first: %d", r.StatusCode)
	}
	if r := rawDAV(t, ts, "PUT", cal+"a.ics", "alice", token, strings.Replace(evA, "SUMMARY:A", "SUMMARY:Z", 1), ics); r.StatusCode != http.StatusCreated {
		t.Fatalf("same-size update must not count the old copy: %d", r.StatusCode)
	}
	if r := rawDAV(t, ts, "PUT", cal+"b.ics", "alice", token, strings.ReplaceAll(evA, "evt-a", "evt-b"), ics); r.StatusCode != http.StatusInsufficientStorage {
		t.Fatalf("over byte quota: %d", r.StatusCode)
	}
}

func TestCalDAVTextMatchFiltersUnbounded(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	token := davUser(t, st, "alice", "user")
	ts := httptest.NewServer(srv)
	defer ts.Close()
	cal := "/dav/usr_alice/calendars/default/"
	ics := map[string]string{"Content-Type": "text/calendar"}
	rawDAV(t, ts, "PUT", cal+"a.ics", "alice", token, evA, ics)
	unbounded := strings.Replace(strings.ReplaceAll(evA, "evt-a", "evt-b"), "SUMMARY:A", "SUMMARY:A\r\nRRULE:FREQ=SECONDLY", 1)
	if r := rawDAV(t, ts, "PUT", cal+"b.ics", "alice", token, unbounded, ics); r.StatusCode != http.StatusCreated {
		t.Fatalf("PUT unbounded: %d", r.StatusCode)
	}
	objs, err := davClient(t, ts, "alice", token).QueryCalendar(context.Background(), cal, &caldav.CalendarQuery{
		CompRequest: caldav.CalendarCompRequest{Name: "VCALENDAR"},
		CompFilter: caldav.CompFilter{Name: "VCALENDAR", Comps: []caldav.CompFilter{{Name: "VEVENT",
			Props: []caldav.PropFilter{{Name: "UID", TextMatch: &caldav.TextMatch{Text: "evt-a"}}}}}},
	})
	if err != nil || len(objs) != 1 || !strings.HasSuffix(objs[0].Path, "/a.ics") {
		t.Fatalf("UID text-match: %+v %v", objs, err)
	}
}

func TestCalDAVObjectNames(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	token := davUser(t, st, "alice", "user")
	ts := httptest.NewServer(srv)
	defer ts.Close()
	cal := "/dav/usr_alice/calendars/default/"
	ics := map[string]string{"Content-Type": "text/calendar"}
	for _, n := range []string{"%2E", "%2E%2E", "%00", "a%0Ab.ics", "a%7Fb.ics", "a%FFb.ics", strings.Repeat("a", 256)} {
		if r := rawDAV(t, ts, "PUT", cal+n, "alice", token, evA, ics); r.StatusCode != http.StatusForbidden {
			t.Fatalf("name %q: %d", n, r.StatusCode)
		}
	}
	// Names are opaque keys: real clients use UIDs, including Outlook's braces and base64.
	good := []string{"{9A8B7C6D-1111-2222-3333-444455556666}.ics", "040000008200E00074C5B7101A82E008=.ics", "_x.ics", "a~b.ics", "with%20space.ics", strings.Repeat("a", 255)}
	for i, n := range good {
		ev := strings.ReplaceAll(evA, "evt-a", "evt-"+strconv.Itoa(i))
		if r := rawDAV(t, ts, "PUT", cal+n, "alice", token, ev, ics); r.StatusCode != http.StatusCreated {
			t.Fatalf("name %q: %d", n, r.StatusCode)
		}
		if r := rawDAV(t, ts, "GET", cal+n, "alice", token, "", nil); r.StatusCode != http.StatusOK || readAll(r) != ev {
			t.Fatalf("GET %q: %d", n, r.StatusCode)
		}
	}
}

// Bare-host discovery: DAV methods on / redirect to the well-known URL instead of serving the SPA.
func TestCalDAVBareHostDiscovery(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	token := davUser(t, st, "alice", "user")
	for _, m := range []string{"PROPFIND", "REPORT", "OPTIONS"} {
		w := davDo(srv, m, "/", "", "")
		if w.Code != http.StatusPermanentRedirect || w.Header().Get("Location") != "/.well-known/caldav" || strings.Contains(w.Body.String(), "<html") {
			t.Fatalf("%s /: %d %q", m, w.Code, w.Header().Get("Location"))
		}
	}
	if w := davDo(srv, "GET", "/", "", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "<html") {
		t.Fatalf("GET / must still serve the SPA: %d", w.Code)
	}
	// A client that follows redirects keeps PROPFIND and lands on the principal.
	ts := httptest.NewServer(srv)
	defer ts.Close()
	body := `<d:propfind xmlns:d="DAV:"><d:prop><d:current-user-principal/></d:prop></d:propfind>`
	r := rawDAV(t, ts, "PROPFIND", "/", "alice", token, body, map[string]string{"Depth": "0", "Content-Type": "application/xml"})
	if got := readAll(r); r.StatusCode != http.StatusMultiStatus || !strings.Contains(got, "/dav/usr_alice/") {
		t.Fatalf("PROPFIND / followed: %d %s", r.StatusCode, got)
	}
}

// Encoded slashes and dot segments never carry alice's request into bob's home.
func TestCalDAVEncodedTraversal(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	alice := davUser(t, st, "alice", "user")
	bob := davUser(t, st, "bob", "user")
	ts := httptest.NewServer(srv)
	defer ts.Close()
	bobObj := "/dav/usr_bob/calendars/default/x.ics"
	secret := strings.Replace(evA, "SUMMARY:A", "SUMMARY:bob-secret", 1)
	if r := rawDAV(t, ts, "PUT", bobObj, "bob", bob, secret, map[string]string{"Content-Type": "text/calendar"}); r.StatusCode != http.StatusCreated {
		t.Fatalf("bob PUT: %d", r.StatusCode)
	}
	paths := []string{
		"/dav/usr_alice/calendars/default/%2F..%2F..%2Fusr_bob%2Fcalendars%2Fdefault%2Fx.ics",
		"/dav/usr_alice/calendars/default/..%2F..%2F..%2Fusr_bob%2Fcalendars%2Fdefault%2Fx.ics",
		"/dav/usr_alice/..%2Fusr_bob/calendars/default/x.ics",
		"/dav/usr_alice/..%2Fusr_bob/",
		"/dav/usr_alice/%2E%2E/usr_bob/calendars/default/x.ics",
		"/dav/usr_alice/calendars/%2E%2E%2F%2E%2E%2F%2E%2E%2Fusr_bob%2Fcalendars%2Fdefault/x.ics",
		"/dav/usr_alice/../usr_bob/calendars/default/x.ics",
		"/dav/%75sr_bob/calendars/default/x.ics",
	}
	for _, p := range paths {
		for _, m := range []string{"GET", "PROPFIND", "PUT", "DELETE"} {
			body, hdr := "", map[string]string{}
			switch m {
			case "PROPFIND":
				body, hdr = `<d:propfind xmlns:d="DAV:"><d:allprop/></d:propfind>`, map[string]string{"Depth": "1", "Content-Type": "application/xml"}
			case "PUT":
				body, hdr = strings.Replace(evA, "SUMMARY:A", "SUMMARY:alice-overwrote", 1), map[string]string{"Content-Type": "text/calendar"}
			}
			r := rawDAV(t, ts, m, p, "alice", alice, body, hdr)
			got := readAll(r)
			if (r.StatusCode != http.StatusForbidden && r.StatusCode != http.StatusNotFound) || strings.Contains(got, "bob-secret") || strings.Contains(got, "usr_bob") {
				t.Fatalf("%s %s: %d %q", m, p, r.StatusCode, got)
			}
		}
	}
	r := rawDAV(t, ts, "GET", bobObj, "bob", bob, "", nil)
	if got := readAll(r); r.StatusCode != http.StatusOK || got != secret {
		t.Fatalf("bob's object changed: %d %q", r.StatusCode, got)
	}
}

// Calendar colour, name and description are bounded at the DAV boundary.
func TestCalDAVCalendarPropsValidated(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	token := davUser(t, st, "alice", "user")
	ts := httptest.NewServer(srv)
	defer ts.Close()
	xmlCT := map[string]string{"Content-Type": "application/xml"}
	mk := func(prop string) string {
		return `<c:mkcalendar xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav" xmlns:ic="http://apple.com/ns/ical/"><d:set><d:prop>` + prop + `</d:prop></d:set></c:mkcalendar>`
	}
	patch := func(prop string) string {
		return `<d:propertyupdate xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav" xmlns:ic="http://apple.com/ns/ical/"><d:set><d:prop>` + prop + `</d:prop></d:set></d:propertyupdate>`
	}
	bad := []string{
		`<ic:calendar-color>red</ic:calendar-color>`,
		`<ic:calendar-color>#ff88</ic:calendar-color>`,
		`<ic:calendar-color>#ff8800;x</ic:calendar-color>`,
		`<d:displayname>` + strings.Repeat("n", 256) + `</d:displayname>`,
		`<c:calendar-description>` + strings.Repeat("d", 4097) + `</c:calendar-description>`,
	}
	for i, prop := range bad {
		if r := rawDAV(t, ts, "MKCALENDAR", "/dav/usr_alice/calendars/bad"+strconv.Itoa(i)+"/", "alice", token, mk(prop), xmlCT); r.StatusCode != http.StatusBadRequest {
			t.Fatalf("MKCALENDAR %d: %d", i, r.StatusCode)
		}
	}
	good := `<d:displayname>` + strings.Repeat("n", 255) + `</d:displayname><ic:calendar-color>#FF8800CC</ic:calendar-color><c:calendar-description>` + strings.Repeat("d", 4096) + `</c:calendar-description>`
	if r := rawDAV(t, ts, "MKCALENDAR", "/dav/usr_alice/calendars/work/", "alice", token, mk(good), xmlCT); r.StatusCode != http.StatusCreated {
		t.Fatalf("MKCALENDAR at the limits: %d %s", r.StatusCode, readAll(r))
	}
	for i, prop := range bad {
		if r := rawDAV(t, ts, "PROPPATCH", "/dav/usr_alice/calendars/work/", "alice", token, patch(prop), xmlCT); r.StatusCode != http.StatusForbidden {
			t.Fatalf("PROPPATCH %d: %d", i, r.StatusCode)
		}
	}
	cal, err := st.Calendars().GetCalendarBySlug(context.Background(), "user", "usr_alice", "work")
	if err != nil || cal.Color != "#FF8800CC" || len(cal.Name) != 255 {
		t.Fatalf("rejected PROPPATCH changed the calendar: %+v %v", cal, err)
	}
	if r := rawDAV(t, ts, "PROPPATCH", "/dav/usr_alice/calendars/work/", "alice", token, patch(`<ic:calendar-color>#00aa11</ic:calendar-color>`), xmlCT); r.StatusCode != http.StatusMultiStatus {
		t.Fatalf("valid PROPPATCH: %d", r.StatusCode)
	}
}

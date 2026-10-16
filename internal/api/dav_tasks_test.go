package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// taskFull carries every property a shared todo list relies on, as Tasks.org and jtx Board write
// them: folded text, escaped commas, several categories, a link, a parent, an alarm, a rule and
// X- properties with parameters. The server must hand back exactly these bytes.
const taskFull = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:+//IDN tasks.org//android-140102//EN\r\n" +
	"BEGIN:VTODO\r\nDTSTAMP:20261016T080000Z\r\nUID:task-full\r\nCREATED:20261016T080000Z\r\nLAST-MODIFIED:20261016T080000Z\r\n" +
	"SUMMARY:Renew the domain\\, then tell Lain\r\n" +
	"DESCRIPTION:Notes that run long enough to be folded by the client across mo\r\n re than one line\\nsecond line\r\n" +
	"URL:https://registrar.example/renew?d=urlxl.com\r\n" +
	"DTSTART;TZID=Europe/Berlin:20261007T090000\r\nDUE;TZID=Europe/Berlin:20261009T170000\r\n" +
	"PRIORITY:1\r\nSTATUS:NEEDS-ACTION\r\nPERCENT-COMPLETE:0\r\n" +
	"CATEGORIES:home,admin\r\nCATEGORIES:shared\r\n" +
	"RELATED-TO;RELTYPE=PARENT:task-parent\r\n" +
	"RRULE:FREQ=WEEKLY;COUNT=4\r\n" +
	"X-APPLE-SORT-ORDER:42\r\nX-TASKS-ORG;X-P=1:kept\r\n" +
	"BEGIN:VALARM\r\nACTION:DISPLAY\r\nDESCRIPTION:Reminder\r\nTRIGGER;RELATED=END:-PT1H\r\nEND:VALARM\r\n" +
	"END:VTODO\r\n" +
	"BEGIN:VTIMEZONE\r\nTZID:Europe/Berlin\r\nBEGIN:STANDARD\r\nDTSTART:19701025T030000\r\nTZOFFSETFROM:+0200\r\nTZOFFSETTO:+0100\r\nRRULE:FREQ=YEARLY;BYMONTH=10;BYDAY=-1SU\r\nEND:STANDARD\r\n" +
	"BEGIN:DAYLIGHT\r\nDTSTART:19700329T020000\r\nTZOFFSETFROM:+0100\r\nTZOFFSETTO:+0200\r\nRRULE:FREQ=YEARLY;BYMONTH=3;BYDAY=-1SU\r\nEND:DAYLIGHT\r\nEND:VTIMEZONE\r\n" +
	"END:VCALENDAR\r\n"

// taskUndated has neither DTSTART nor DUE: every time-range query returns it.
const taskUndated = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VTODO\r\nUID:task-someday\r\nDTSTAMP:20261016T080000Z\r\nSUMMARY:Someday\r\nCATEGORIES:ideas\r\nEND:VTODO\r\nEND:VCALENDAR\r\n"

// taskDue has only DUE and no rule, so the index and caldav.Match decide every range exactly.
const taskDue = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VTODO\r\nUID:task-due\r\nDTSTAMP:20261016T080000Z\r\nSUMMARY:Pay\r\nDUE:20261020T150000Z\r\nPRIORITY:5\r\nEND:VTODO\r\nEND:VCALENDAR\r\n"

var icsHeader = map[string]string{"Content-Type": "text/calendar"}
var xmlHeader = map[string]string{"Content-Type": "application/xml", "Depth": "1"}

func mkcalendar(comps ...string) string {
	set := ""
	if len(comps) > 0 {
		set = `<c:supported-calendar-component-set>`
		for _, c := range comps {
			set += `<c:comp name="` + c + `"/>`
		}
		set += `</c:supported-calendar-component-set>`
	}
	return `<c:mkcalendar xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:set><d:prop><d:displayname>Tasks</d:displayname>` + set + `</d:prop></d:set></c:mkcalendar>`
}

// taskQuery is a calendar-query REPORT for comp, with a time range when start is set.
func taskQuery(comp, start, end string) string {
	tr := ""
	if start != "" {
		tr = `<c:time-range start="` + start + `" end="` + end + `"/>`
	}
	return `<c:calendar-query xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:prop><d:getetag/></d:prop><c:filter><c:comp-filter name="VCALENDAR"><c:comp-filter name="` + comp + `">` + tr + `</c:comp-filter></c:comp-filter></c:filter></c:calendar-query>`
}

func hrefs(body string, names ...string) map[string]bool {
	out := map[string]bool{}
	for _, n := range names {
		out[n] = strings.Contains(body, n)
	}
	return out
}

// A client creates a task list with MKCALENDAR, then writes, reads, queries, completes and
// deletes a task; a second client follows every change through sync-collection.
func TestCalDAVTaskList(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	token := davUser(t, st, "alice", "user")
	ts := httptest.NewServer(srv)
	defer ts.Close()
	list := "/dav/usr_alice/calendars/tasks/"

	if r := rawDAV(t, ts, "MKCALENDAR", list, "alice", token, mkcalendar("VTODO"), xmlHeader); r.StatusCode != http.StatusCreated {
		t.Fatalf("MKCALENDAR %d %s", r.StatusCode, readAll(r))
	}
	prop := `<d:propfind xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:prop><c:supported-calendar-component-set/></d:prop></d:propfind>`
	body := readAll(rawDAV(t, ts, "PROPFIND", list, "alice", token, prop, map[string]string{"Content-Type": "application/xml", "Depth": "0"}))
	if !strings.Contains(body, `name="VTODO"`) || strings.Contains(body, `name="VEVENT"`) {
		t.Fatalf("supported-calendar-component-set of a task list:\n%s", body)
	}

	// The second client's starting point.
	syncBody := func(tok string) string {
		return `<d:sync-collection xmlns:d="DAV:"><d:sync-token>` + tok + `</d:sync-token><d:sync-level>1</d:sync-level><d:prop><d:getetag/></d:prop></d:sync-collection>`
	}
	sync := func(tok string) string {
		r := rawDAV(t, ts, "REPORT", list, "alice", token, syncBody(tok), xmlHeader)
		b := readAll(r)
		if r.StatusCode != 207 {
			t.Fatalf("sync %d %s", r.StatusCode, b)
		}
		return b
	}
	other := between(sync(""), "<sync-token>", "</sync-token>")

	for name, ics := range map[string]string{"task-full.ics": taskFull, "task-someday.ics": taskUndated, "task-due.ics": taskDue} {
		if r := rawDAV(t, ts, "PUT", list+name, "alice", token, ics, map[string]string{"Content-Type": "text/calendar", "If-None-Match": "*"}); r.StatusCode != http.StatusCreated {
			t.Fatalf("PUT %s: %d %s", name, r.StatusCode, readAll(r))
		}
	}
	get := rawDAV(t, ts, "GET", list+"task-full.ics", "alice", token, "", nil)
	if got := readAll(get); got != taskFull {
		t.Fatalf("GET must return the exact bytes:\n%q", got)
	}
	etag := get.Header.Get("ETag")

	if r := rawDAV(t, ts, "PUT", list+"evt-a.ics", "alice", token, evA, icsHeader); r.StatusCode != http.StatusConflict || !strings.Contains(readAll(r), "supported-calendar-component") {
		t.Fatalf("VEVENT into a task list: %d", r.StatusCode)
	}

	// RFC 4791 §9.9 for a DUE-only task: start < DUE AND end >= DUE. The recurring task is kept on
	// its series' index span (Oct 7 to Oct 30), never expanded per query.
	for _, q := range []struct {
		name, comp, start, end string
		want                   string // the tasks returned, in this order: full, someday, due
	}{
		{"every task", "VTODO", "", "", "full someday due"},
		{"range over the first occurrence", "VTODO", "20261009T000000Z", "20261010T000000Z", "full someday"},
		{"range ending at DUE", "VTODO", "20261020T140000Z", "20261020T150000Z", "full someday due"},
		{"range starting at DUE", "VTODO", "20261020T150000Z", "20261020T160000Z", "full someday"},
		{"range a year later", "VTODO", "20271001T000000Z", "20271002T000000Z", "someday"},
		{"events only", "VEVENT", "", "", ""},
		{"events in range", "VEVENT", "20261009T000000Z", "20261010T000000Z", ""},
	} {
		r := rawDAV(t, ts, "REPORT", list, "alice", token, taskQuery(q.comp, q.start, q.end), xmlHeader)
		body := readAll(r)
		var got []string
		for _, n := range []string{"full", "someday", "due"} {
			if strings.Contains(body, "task-"+n+".ics") {
				got = append(got, n)
			}
		}
		if r.StatusCode != 207 || strings.Join(got, " ") != q.want {
			t.Fatalf("%s: %d %v, want %q\n%s", q.name, r.StatusCode, got, q.want, body)
		}
	}

	multiget := `<c:calendar-multiget xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:prop><d:getetag/><c:calendar-data/></d:prop><d:href>` + list + `task-full.ics</d:href><d:href>` + list + `task-someday.ics</d:href></c:calendar-multiget>`
	body = readAll(rawDAV(t, ts, "REPORT", list, "alice", token, multiget, xmlHeader))
	if !strings.Contains(body, "Renew the domain") || !strings.Contains(body, "Someday") || !strings.Contains(body, "X-TASKS-ORG;X-P=1:kept") {
		t.Fatalf("multiget:\n%s", body)
	}

	// Complete it, as Tasks.org does: STATUS, COMPLETED and PERCENT-COMPLETE, under If-Match.
	done := strings.NewReplacer("STATUS:NEEDS-ACTION", "STATUS:COMPLETED\r\nCOMPLETED:20261016T090000Z", "PERCENT-COMPLETE:0", "PERCENT-COMPLETE:100").Replace(taskFull)
	if r := rawDAV(t, ts, "PUT", list+"task-full.ics", "alice", token, done, map[string]string{"Content-Type": "text/calendar", "If-Match": `"stale"`}); r.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("stale If-Match: %d", r.StatusCode)
	}
	if r := rawDAV(t, ts, "PUT", list+"task-full.ics", "alice", token, done, map[string]string{"Content-Type": "text/calendar", "If-Match": etag}); r.StatusCode != http.StatusCreated { // the fork answers 201 to every PUT
		t.Fatalf("complete: %d %s", r.StatusCode, readAll(r))
	}
	if got := readAll(rawDAV(t, ts, "GET", list+"task-full.ics", "alice", token, "", nil)); got != done {
		t.Fatalf("completed task bytes:\n%q", got)
	}

	delta := sync(other)
	if !strings.Contains(delta, "task-full.ics") || !strings.Contains(delta, "task-someday.ics") || !strings.Contains(delta, "task-due.ics") {
		t.Fatalf("second client misses the new tasks:\n%s", delta)
	}
	other = between(delta, "<sync-token>", "</sync-token>")
	if r := rawDAV(t, ts, "DELETE", list+"task-someday.ics", "alice", token, "", nil); r.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE %d", r.StatusCode)
	}
	delta = sync(other)
	if !strings.Contains(delta, "task-someday.ics") || !strings.Contains(delta, "404") || strings.Contains(delta, "task-full.ics") {
		t.Fatalf("second client's delta after DELETE:\n%s", delta)
	}
}

// MKCALENDAR keeps the requested set: tasks, events or both. A component KyCalendar cannot hold
// refuses the whole request; no set is an events calendar, as before tasks.
func TestCalDAVMkcalendarComponentSets(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	token := davUser(t, st, "alice", "user")
	ts := httptest.NewServer(srv)
	defer ts.Close()
	ctx := context.Background()
	for slug, c := range map[string]struct {
		comps []string
		want  string
	}{
		"plain": {nil, "VEVENT"},
		"todo":  {[]string{"VTODO"}, "VTODO"},
		"both":  {[]string{"VTODO", "VEVENT"}, "VEVENT,VTODO"},
	} {
		if r := rawDAV(t, ts, "MKCALENDAR", "/dav/usr_alice/calendars/"+slug+"/", "alice", token, mkcalendar(c.comps...), xmlHeader); r.StatusCode != http.StatusCreated {
			t.Fatalf("%s: MKCALENDAR %d %s", slug, r.StatusCode, readAll(r))
		}
		got, err := st.Calendars().GetCalendarBySlug(ctx, "user", "usr_alice", slug)
		if err != nil || strings.Join(got.Components, ",") != c.want {
			t.Fatalf("%s: stored %+v %v, want %s", slug, got, err, c.want)
		}
	}
	if r := rawDAV(t, ts, "MKCALENDAR", "/dav/usr_alice/calendars/journal/", "alice", token, mkcalendar("VTODO", "VJOURNAL"), xmlHeader); r.StatusCode != http.StatusForbidden {
		t.Fatalf("VJOURNAL: %d", r.StatusCode)
	}
	if _, err := st.Calendars().GetCalendarBySlug(ctx, "user", "usr_alice", "journal"); err == nil {
		t.Fatal("a refused MKCALENDAR created a calendar")
	}

	// In a calendar holding both, each query reads its own component only: an undated task never
	// answers an event range, and an endless series never answers a task range.
	both := "/dav/usr_alice/calendars/both/"
	forever := strings.Replace(evA, "SUMMARY:A", "RRULE:FREQ=DAILY", 1)
	for name, ics := range map[string]string{"evt-a.ics": forever, "task-someday.ics": taskUndated} {
		if r := rawDAV(t, ts, "PUT", both+name, "alice", token, ics, icsHeader); r.StatusCode != http.StatusCreated {
			t.Fatalf("PUT %s: %d %s", name, r.StatusCode, readAll(r))
		}
	}
	for comp, want := range map[string]string{"VEVENT": "evt-a.ics", "VTODO": "task-someday.ics"} {
		body := readAll(rawDAV(t, ts, "REPORT", both, "alice", token, taskQuery(comp, "20271001T000000Z", "20271002T000000Z"), xmlHeader))
		got := hrefs(body, "evt-a.ics", "task-someday.ics")
		if len(got) != 2 || !got[want] || got["evt-a.ics"] == got["task-someday.ics"] {
			t.Fatalf("%s range in a mixed calendar: %v\n%s", comp, got, body)
		}
	}
}

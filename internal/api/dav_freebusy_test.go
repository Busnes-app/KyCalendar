package api_test

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func freeBusyQuery(start, end string) string {
	return `<c:free-busy-query xmlns:c="urn:ietf:params:xml:ns:caldav"><c:time-range start="` + start + `" end="` + end + `"/></c:free-busy-query>`
}

func TestCalDAVFreeBusyQuery(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	token := davUser(t, st, "alice", "user")
	ts := httptest.NewServer(srv)
	defer ts.Close()
	cal := "/dav/usr_alice/calendars/default/"
	put := func(uid, props string) {
		t.Helper()
		body := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:" + uid + "\r\nDTSTAMP:20261001T000000Z\r\n" + props +
			"SUMMARY:Secret summary\r\nLOCATION:Secret room\r\nDESCRIPTION:Secret notes\r\nORGANIZER:mailto:boss@example.com\r\nATTENDEE:mailto:guest@example.com\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
		if r := rawDAV(t, ts, "PUT", cal+uid+".ics", "alice", token, body, map[string]string{"Content-Type": "text/calendar"}); r.StatusCode != 201 {
			t.Fatalf("PUT %s: %d %s", uid, r.StatusCode, readAll(r))
		}
	}
	put("hidden-busy", "DTSTART:20261110T090000Z\r\nDTEND:20261110T100000Z\r\n")
	put("hidden-free", "DTSTART:20261110T130000Z\r\nDTEND:20261110T140000Z\r\nTRANSP:TRANSPARENT\r\n")
	put("hidden-maybe", "DTSTART:20261110T150000Z\r\nDTEND:20261110T160000Z\r\nSTATUS:TENTATIVE\r\n")
	put("hidden-weekly", "DTSTART:20261103T080000Z\r\nDTEND:20261103T083000Z\r\nRRULE:FREQ=WEEKLY\r\n")
	hdr := map[string]string{"Content-Type": "application/xml", "Depth": "1"}

	r := rawDAV(t, ts, "REPORT", cal, "alice", token, freeBusyQuery("20261110T000000Z", "20261111T000000Z"), hdr)
	body := readAll(r)
	if r.StatusCode != 200 || r.Header.Get("Content-Type") != "text/calendar" {
		t.Fatalf("free-busy %d %q %s", r.StatusCode, r.Header.Get("Content-Type"), body)
	}
	for _, want := range []string{"BEGIN:VFREEBUSY", "FREEBUSY:20261110T080000Z/20261110T083000Z", "FREEBUSY:20261110T090000Z/20261110T100000Z",
		"FREEBUSY;FBTYPE=BUSY-TENTATIVE:20261110T150000Z/20261110T160000Z"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
	for _, leak := range []string{"Secret", "example.com", "hidden-", "20261110T130000Z/"} {
		if strings.Contains(body, leak) {
			t.Errorf("free-busy reveals %q:\n%s", leak, body)
		}
	}

	if r := rawDAV(t, ts, "REPORT", cal, "alice", token, freeBusyQuery("20260101T000000Z", "20270301T000000Z"), hdr); r.StatusCode != 403 {
		t.Errorf("a range over 400 days: %d, want 403", r.StatusCode)
	}
	if r := rawDAV(t, ts, "REPORT", cal+"hidden-busy.ics", "alice", token, freeBusyQuery("20261110T000000Z", "20261111T000000Z"), hdr); r.StatusCode != 403 {
		t.Errorf("free-busy on an object: %d, want 403", r.StatusCode)
	}
}

// A task list has no events, so nothing is busy, and the query still answers.
func TestCalDAVFreeBusyQueryTaskList(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	token := davUser(t, st, "alice", "user")
	ts := httptest.NewServer(srv)
	defer ts.Close()
	mk := `<c:mkcalendar xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:set><d:prop><c:supported-calendar-component-set><c:comp name="VTODO"/></c:supported-calendar-component-set></d:prop></d:set></c:mkcalendar>`
	if r := rawDAV(t, ts, "MKCALENDAR", "/dav/usr_alice/calendars/todo/", "alice", token, mk, map[string]string{"Content-Type": "application/xml"}); r.StatusCode != 201 {
		t.Fatalf("MKCALENDAR %d %s", r.StatusCode, readAll(r))
	}
	r := rawDAV(t, ts, "REPORT", "/dav/usr_alice/calendars/todo/", "alice", token, freeBusyQuery("20261110T000000Z", "20261111T000000Z"), map[string]string{"Content-Type": "application/xml"})
	if body := readAll(r); r.StatusCode != 200 || !strings.Contains(body, "BEGIN:VFREEBUSY") || strings.Contains(body, "FREEBUSY:") {
		t.Fatalf("task list free-busy %d %s", r.StatusCode, body)
	}
}

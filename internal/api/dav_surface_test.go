package api_test

import (
	"fmt"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

// What iOS, DAVx5 and Thunderbird ask for during discovery and sync, scheduling included: with
// scheduling off, every scheduling property must keep answering 404.
const surfacePropfind = `<?xml version="1.0"?>
<d:propfind xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav" xmlns:cs="http://calendarserver.org/ns/" xmlns:a="http://apple.com/ns/ical/">
<d:prop><d:current-user-principal/><d:principal-URL/><d:owner/><d:resourcetype/><d:displayname/>
<d:supported-report-set/><d:current-user-privilege-set/><d:sync-token/><cs:getctag/><a:calendar-color/>
<c:calendar-home-set/><c:calendar-description/><c:supported-calendar-component-set/><c:max-resource-size/>
<c:calendar-user-address-set/><c:schedule-inbox-URL/><c:schedule-outbox-URL/><c:schedule-default-calendar-URL/>
<c:schedule-calendar-transp/><c:calendar-free-busy-set/><cs:email-address-set/></d:prop></d:propfind>`

var surfaceVolatile = regexp.MustCompile(`(<(?:sync-token|getctag|getetag)[^>]*>)[^<]+`)

// TestCalDAVWireSurface pins what a CalDAV client sees, byte for byte after masking tokens and
// ETags. A change here changes every native client: regenerate only on purpose, with
// KY_UPDATE_GOLDEN=1, and review the diff.
func TestCalDAVWireSurface(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	token := davUser(t, st, "alice", "user")
	ts := httptest.NewServer(srv)
	defer ts.Close()
	cal := "/dav/usr_alice/calendars/default/"
	rawDAV(t, ts, "PROPFIND", "/dav/usr_alice/calendars/", "alice", token, "", map[string]string{"Depth": "1"})
	if r := rawDAV(t, ts, "PUT", cal+"evt-a.ics", "alice", token, evA, map[string]string{"Content-Type": "text/calendar"}); r.StatusCode != 201 {
		t.Fatalf("PUT %d", r.StatusCode)
	}

	var out strings.Builder
	for _, p := range []string{"/dav/usr_alice/", "/dav/usr_alice/calendars/", cal, cal + "evt-a.ics", cal + "missing.ics"} {
		r := rawDAV(t, ts, "OPTIONS", p, "alice", token, "", nil)
		fmt.Fprintf(&out, "OPTIONS %s\n%d DAV: %s\nAllow: %s\n\n", p, r.StatusCode, r.Header.Get("DAV"), r.Header.Get("Allow"))
	}
	for _, p := range []string{"/dav/usr_alice/", "/dav/usr_alice/calendars/", cal, cal + "evt-a.ics"} {
		r := rawDAV(t, ts, "PROPFIND", p, "alice", token, surfacePropfind, map[string]string{"Depth": "0", "Content-Type": "application/xml"})
		body := surfaceVolatile.ReplaceAllString(readAll(r), "${1}X")
		fmt.Fprintf(&out, "PROPFIND %s\n%d %s\n%s\n\n", p, r.StatusCode, r.Header.Get("Content-Type"), body)
	}

	const golden = "testdata/dav_surface.golden"
	if os.Getenv("KY_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(out.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if out.String() != string(want) {
		t.Errorf("CalDAV wire surface changed; got:\n%s", out.String())
	}
}

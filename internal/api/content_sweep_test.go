package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/config"
)

// TestSweepKeepsCalDAVByteIdentical: real clients' events (Apple, Thunderbird, Google, Outlook)
// written before setup answer GET, multiget, calendar-query and sync byte for byte the same
// after the sweep sealed them, ETags included, so a client notices nothing.
func TestSweepKeepsCalDAVByteIdentical(t *testing.T) {
	srv, st, _ := setupTestServerWith(t, func(c *config.Config) { c.Calendar.ContentEncryption = true })
	alice := loginAs(t, srv, st, "alice", "user")
	phone := seedAppPassword(t, st, "usr_alice")
	ts := httptest.NewServer(srv)
	defer ts.Close()
	path := "/dav/usr_alice/calendars/default/"
	rawDAV(t, ts, "PROPFIND", "/dav/usr_alice/calendars/", "alice", phone, "", map[string]string{"Depth": "1"})

	var names []string
	files, _ := filepath.Glob("../calendar/testdata/recurrence/*.ics")
	for _, f := range files {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(f)
		if r := rawDAV(t, ts, "PUT", path+name, "alice", phone, string(body), map[string]string{"Content-Type": "text/calendar"}); r.StatusCode == http.StatusCreated {
			names = append(names, name)
		}
	}
	for _, prefix := range []string{"apple_", "thunderbird_", "google_", "outlook_"} {
		found := false
		for _, n := range names {
			found = found || strings.HasPrefix(n, prefix)
		}
		if !found {
			t.Fatalf("no %s fixture was stored", prefix)
		}
	}
	hrefs := ""
	for _, n := range names {
		hrefs += "<D:href>" + path + n + "</D:href>"
	}
	multiget := `<?xml version="1.0"?><C:calendar-multiget xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav"><D:prop><D:getetag/><C:calendar-data/></D:prop>` + hrefs + `</C:calendar-multiget>`
	query := `<?xml version="1.0"?><C:calendar-query xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav"><D:prop><D:getetag/><C:calendar-data/></D:prop><C:filter><C:comp-filter name="VCALENDAR"/></C:filter></C:calendar-query>`
	syncReq := `<?xml version="1.0"?><D:sync-collection xmlns:D="DAV:"><D:sync-token/><D:sync-level>1</D:sync-level><D:prop><D:getetag/></D:prop></D:sync-collection>`
	capture := func() map[string]string {
		out := map[string]string{}
		for _, n := range names {
			r := rawDAV(t, ts, "GET", path+n, "alice", phone, "", nil)
			out["GET "+n] = r.Header.Get("ETag") + "\n" + readAll(r)
		}
		out["multiget"] = readAll(rawDAV(t, ts, "REPORT", path, "alice", phone, multiget, map[string]string{"Content-Type": "application/xml", "Depth": "1"}))
		out["query"] = readAll(rawDAV(t, ts, "REPORT", path, "alice", phone, query, map[string]string{"Content-Type": "application/xml", "Depth": "1"}))
		out["sync"] = readAll(rawDAV(t, ts, "REPORT", path, "alice", phone, syncReq, map[string]string{"Content-Type": "application/xml"}))
		return out
	}
	before := capture()

	w := do(t, srv, "POST", "/api/encryption/recovery-code", alice)
	code := between(w.Body.String(), `"recovery_code":"`, `"`)
	if w := doJSON(t, srv, "POST", "/api/encryption/setup", alice, map[string]string{"passphrase": "correct horse battery", "recovery_code": code}); w.Code != http.StatusNoContent {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}
	if n, _ := st.Content().LegacyRows(context.Background()); n != len(names) {
		t.Fatalf("legacy rows before the sweep = %d, want %d", n, len(names))
	}
	srv.SweepContent(context.Background())
	if n, _ := st.Content().LegacyRows(context.Background()); n != 0 {
		t.Fatalf("%d legacy rows left after the sweep", n)
	}
	cals, err := st.Calendars().ListCalendarsByOwner(context.Background(), "user", "usr_alice")
	if err != nil || len(cals) != 1 {
		t.Fatal(err)
	}
	for _, n := range names {
		if o, err := st.Calendars().GetObject(context.Background(), cals[0].ID, n); err != nil || o.Sealed == nil {
			t.Fatalf("%s not sealed: %v", n, err)
		}
	}
	after := capture()
	for k, v := range before {
		if after[k] != v {
			t.Fatalf("%s changed across the sweep:\nbefore %q\nafter  %q", k, v, after[k])
		}
	}
}

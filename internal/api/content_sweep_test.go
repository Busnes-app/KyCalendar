package api_test

import (
	"context"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/config"
)

// TestSweepKeepsCalDAVBodiesAndReportsNewETags: real clients' events (Apple, Thunderbird, Google,
// Outlook) written before setup answer GET, multiget and calendar-query with the same bytes after
// the sweep sealed them. Only the ETags change: the old ones were the plaintext's SHA-256, so the
// sweep replaces them and logs a change, and an incremental sync reports every swept object
// once with its new ETag. The sweep then truncates SQLite's WAL.
func TestSweepKeepsCalDAVBodiesAndReportsNewETags(t *testing.T) {
	srv, st, cfg := setupTestServerWith(t, func(c *config.Config) { c.Calendar.ContentEncryption = true })
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
	syncReq := func(tok string) string {
		return `<?xml version="1.0"?><D:sync-collection xmlns:D="DAV:"><D:sync-token>` + tok + `</D:sync-token><D:sync-level>1</D:sync-level><D:prop><D:getetag/></D:prop></D:sync-collection>`
	}
	etags := regexp.MustCompile(`<getetag[^>]*>[^<]*</getetag>`)
	capture := func() (map[string]string, map[string]string) {
		out, tags := map[string]string{}, map[string]string{}
		for _, n := range names {
			r := rawDAV(t, ts, "GET", path+n, "alice", phone, "", nil)
			out["GET "+n] = readAll(r)
			tags[n] = strings.Trim(r.Header.Get("ETag"), `"`)
		}
		out["multiget"] = etags.ReplaceAllString(readAll(rawDAV(t, ts, "REPORT", path, "alice", phone, multiget, map[string]string{"Content-Type": "application/xml", "Depth": "1"})), "")
		out["query"] = etags.ReplaceAllString(readAll(rawDAV(t, ts, "REPORT", path, "alice", phone, query, map[string]string{"Content-Type": "application/xml", "Depth": "1"})), "")
		return out, tags
	}
	before, beforeTags := capture()
	token := between(readAll(rawDAV(t, ts, "REPORT", path, "alice", phone, syncReq(""), map[string]string{"Content-Type": "application/xml"})), "<sync-token>", "</sync-token>")

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
	// The WAL's older frames held the replaced plaintext: the sweep truncates it.
	if fi, err := os.Stat(filepath.Join(cfg.Database.DataDir, "kycalendar.db-wal")); cfg.Database.Driver == "sqlite" && (err != nil || fi.Size() != 0) {
		t.Fatalf("WAL after the sweep: %v %v", fi, err)
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
	after, afterTags := capture()
	for k, v := range before {
		if after[k] != v {
			t.Fatalf("%s changed across the sweep:\nbefore %q\nafter  %q", k, v, after[k])
		}
	}
	delta := readAll(rawDAV(t, ts, "REPORT", path, "alice", phone, syncReq(token), map[string]string{"Content-Type": "application/xml"}))
	for _, n := range names {
		if afterTags[n] == beforeTags[n] || afterTags[n] == "" {
			t.Fatalf("%s kept its plaintext ETag %q", n, beforeTags[n])
		}
		if !strings.Contains(delta, path+n) || !strings.Contains(delta, afterTags[n]) || strings.Contains(delta, beforeTags[n]) {
			t.Fatalf("the incremental sync does not report %s with its new ETag:\n%s", n, delta)
		}
	}
}

// TestTaskSearchIsTheSameAfterEncryption: a list's search answers the same queries with the
// same tasks before setup (the index) and after (decrypted tasks matched per request).
func TestTaskSearchIsTheSameAfterEncryption(t *testing.T) {
	srv, st, cfg := setupTestServerWith(t, func(c *config.Config) { c.Calendar.ContentEncryption = true })
	alice := loginAs(t, srv, st, "alice", "user")
	list := taskList(t, srv, alice)
	for _, task := range []map[string]any{
		{"title": "Compress the hero images"}, {"title": "Superhero cape", "notes": "Café receipts"},
		{"title": "Reply by e-mail", "tags": []string{"Urgent"}}, {"title": "Straße naïve résumé"},
	} {
		createTask(t, srv, alice, list, task)
	}
	queries := []string{"hero", "superhero", "ero", "cafe", "e-mail", "mail", "urg", "strasse", "stra", "naive res", "images hero"}
	search := func() map[string][]string {
		out := map[string][]string{}
		for _, q := range queries {
			for _, x := range listTasks(t, srv, alice, "q="+url.QueryEscape(q)) {
				out[q] = append(out[q], x.Title)
			}
			slices.Sort(out[q])
		}
		return out
	}
	before := search()
	hero := []string{"Compress the hero images"} // SQLite matches word prefixes, PostgreSQL substrings
	if cfg.Database.Driver != "sqlite" {
		hero = append(hero, "Superhero cape")
	}
	if !slices.Equal(before["hero"], hero) {
		t.Fatalf("fixture: hero found %v", before["hero"])
	}
	w := do(t, srv, "POST", "/api/encryption/recovery-code", alice)
	code := between(w.Body.String(), `"recovery_code":"`, `"`)
	if w := doJSON(t, srv, "POST", "/api/encryption/setup", alice, map[string]string{"passphrase": "correct horse battery", "recovery_code": code}); w.Code != http.StatusNoContent {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}
	srv.SweepContent(context.Background())
	if n, _ := st.Content().LegacyRows(context.Background()); n != 0 {
		t.Fatalf("%d legacy rows left", n)
	}
	if after := search(); !maps.EqualFunc(before, after, slices.Equal) {
		t.Fatalf("search changed with encryption:\nbefore %v\nafter  %v", before, after)
	}
}

// TestEmptySweepStillPurgesTheWAL: an unlocked save seals the last legacy row before the
// sweep runs, so the sweep seals nothing, and it still truncates the WAL that holds the old
// plaintext.
func TestEmptySweepStillPurgesTheWAL(t *testing.T) {
	srv, st, cfg := setupTestServerWith(t, func(c *config.Config) { c.Calendar.ContentEncryption = true })
	if cfg.Database.Driver != "sqlite" {
		t.Skip("SQLite's WAL")
	}
	alice := loginAs(t, srv, st, "alice", "user")
	phone := seedAppPassword(t, st, "usr_alice")
	ts := httptest.NewServer(srv)
	defer ts.Close()
	path := "/dav/usr_alice/calendars/default/"
	rawDAV(t, ts, "PROPFIND", "/dav/usr_alice/calendars/", "alice", phone, "", map[string]string{"Depth": "1"})
	put := func() {
		if r := rawDAV(t, ts, "PUT", path+"evt-s.ics", "alice", phone, evSecret, map[string]string{"Content-Type": "text/calendar"}); r.StatusCode >= 300 {
			t.Fatalf("PUT %d %s", r.StatusCode, readAll(r))
		}
	}
	put()
	w := do(t, srv, "POST", "/api/encryption/recovery-code", alice)
	code := between(w.Body.String(), `"recovery_code":"`, `"`)
	if w := doJSON(t, srv, "POST", "/api/encryption/setup", alice, map[string]string{"passphrase": "correct horse battery", "recovery_code": code}); w.Code != http.StatusNoContent {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}
	put() // the phone is unlocked through the transition, so this save seals the row
	if n, _ := st.Content().LegacyRows(context.Background()); n != 0 {
		t.Fatalf("%d legacy rows left after the unlocked save", n)
	}
	wal := filepath.Join(cfg.Database.DataDir, "kycalendar.db-wal")
	if b, _ := os.ReadFile(wal); !strings.Contains(string(b), "Secret merger call") {
		t.Fatal("fixture: the WAL does not hold the old plaintext")
	}
	srv.SweepContent(context.Background())
	if b, err := os.ReadFile(wal); err != nil || strings.Contains(string(b), "Secret merger call") {
		t.Fatalf("the WAL still holds the old plaintext after an empty sweep (%v)", err)
	}
}

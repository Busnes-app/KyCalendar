package api_test

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// go-ical decodes a parameter value a byte at a time: one 256 KiB value costs seconds of CPU.
// A PUT carrying one is refused before decoding, with the fork's own valid-calendar-data answer.
func TestCalDAVPutRefusesLongParameterQuickly(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	token := davUser(t, st, "alice", "user")
	ts := httptest.NewServer(srv)
	defer ts.Close()
	cal := "/dav/usr_alice/calendars/default/"
	ct := map[string]string{"Content-Type": "text/calendar"}

	garbage := rawDAV(t, ts, "PUT", cal+"garbage.ics", "alice", token, "not a calendar", ct)
	want := readAll(garbage)
	if garbage.StatusCode != 409 || !strings.Contains(want, "valid-calendar-data") {
		t.Fatalf("fork's own refusal: %d %s", garbage.StatusCode, want)
	}

	long := strings.Replace(evA, "X-KEEP;P=1:value", "X-KEEP;P="+strings.Repeat("a", 900<<10)+":value", 1)
	quoted := strings.Replace(evA, "X-KEEP;P=1:value", `X-KEEP;P="`+strings.Repeat("a", 900<<10)+`":value`, 1)
	for name, body := range map[string]string{"plain": long, "quoted": quoted} {
		start := time.Now()
		r := rawDAV(t, ts, "PUT", cal+"long-"+name+".ics", "alice", token, body, ct)
		got := readAll(r)
		if r.StatusCode != 409 || got != want || r.Header.Get("Content-Type") != garbage.Header.Get("Content-Type") {
			t.Errorf("%s: %d %q %q, want the fork's answer %q", name, r.StatusCode, r.Header.Get("Content-Type"), got, want)
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("%s: refusal took %v", name, d)
		}
		if g := rawDAV(t, ts, "GET", cal+"long-"+name+".ics", "alice", token, "", nil); g.StatusCode != 404 {
			t.Errorf("%s: refused object stored: GET %d", name, g.StatusCode)
		}
	}

	ok := strings.Replace(evA, "X-KEEP;P=1:value", "X-KEEP;P="+strings.Repeat("a", 1<<10)+":value", 1)
	if r := rawDAV(t, ts, "PUT", cal+"ok.ics", "alice", token, ok, ct); r.StatusCode != 201 {
		t.Fatalf("a 1 KiB parameter: %d %s", r.StatusCode, readAll(r))
	}
	if body := readAll(rawDAV(t, ts, "GET", cal+"ok.ics", "alice", token, "", nil)); body != ok {
		t.Fatal("an accepted PUT is no longer stored byte for byte")
	}
}

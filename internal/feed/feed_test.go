package feed

import (
	"bytes"
	"context"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"testing"
)

func TestNormalizeURL(t *testing.T) {
	for _, tc := range []struct{ in, link, host string }{
		{"https://outlook.office365.com/owa/calendar/abc/def/reachcalendar.ics", "https://outlook.office365.com/owa/calendar/abc/def/reachcalendar.ics", "outlook.office365.com"},
		{"  webcal://Calendar.Example.com/x.ics#frag ", "https://Calendar.Example.com/x.ics", "calendar.example.com"},
		{"WEBCAL://example.com/x?k=v", "https://example.com/x?k=v", "example.com"},
	} {
		link, host, err := NormalizeURL(tc.in)
		if err != nil || link != tc.link || host != tc.host {
			t.Errorf("NormalizeURL(%q) = %q %q %v, want %q %q", tc.in, link, host, err, tc.link, tc.host)
		}
	}
	for _, bad := range []string{"", "http://example.com/x.ics", "ftp://example.com/x", "https:///x.ics", "https://user:pw@example.com/x.ics", "file:///etc/passwd", "https://example.com/" + strings.Repeat("a", maxURL)} {
		if _, _, err := NormalizeURL(bad); err != ErrLink {
			t.Errorf("NormalizeURL(%q) = %v, want ErrLink", bad, err)
		}
	}
}

func TestRefusePrivate(t *testing.T) {
	for addr, refused := range map[string]bool{
		"127.0.0.1": true, "::1": true, "169.254.169.254": true, "fe80::1": true, "0.0.0.0": true,
		"10.0.0.1": true, "172.16.5.4": true, "192.168.1.1": true, "fd00::1": true, "100.64.0.1": true,
		"100.100.100.200": true, "::ffff:192.168.1.1": true, "fd00:ec2::254": true,
		"52.96.0.1": false, "2603:1026::1": false, "1.1.1.1": false,
	} {
		if err := RefusePrivate(netip.MustParseAddr(addr)); (err != nil) != refused {
			t.Errorf("RefusePrivate(%s) = %v, want refused=%t", addr, err, refused)
		}
	}
}

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/m365.ics")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseM365(t *testing.T) {
	objs, skipped, err := Parse(context.Background(), "cal_1", fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 2 {
		t.Errorf("skipped %d, want 2 (no UID, unreadable DTSTART)", skipped)
	}
	byUID := map[string]string{}
	for _, o := range objs {
		if o.CalendarID != "cal_1" || !strings.HasSuffix(o.Name, ".ics") || strings.Contains(o.Name, o.UID) {
			t.Errorf("object %q in %q: want a hashed .ics name", o.Name, o.CalendarID)
		}
		byUID[o.UID] = string(o.Data)
	}
	if len(objs) != 3 {
		t.Fatalf("got %d objects %v, want 3", len(objs), byUID)
	}
	standup := byUID["040000008200E00074C5B7101A82E00800000000A1B2C3D4E5F6"]
	for _, want := range []string{"Weekly standup (moved)", "RECURRENCE-ID", "TZID:W. Europe Standard Time", "DTSTAMP:20261001T080000Z"} {
		if !strings.Contains(standup, want) {
			t.Errorf("series object lacks %q:\n%s", want, standup)
		}
	}
	for _, unwanted := range []string{"VALARM", "Pacific Standard Time", "METHOD", "DTSTAMP:20261015T120000Z"} {
		if strings.Contains(standup, unwanted) {
			t.Errorf("series object carries %q:\n%s", unwanted, standup)
		}
	}
	if strings.Contains(byUID["all-day-1"], "VTIMEZONE") {
		t.Error("an all-day event got a time zone it does not name")
	}
	if _, ok := byUID["todo-1"]; ok {
		t.Error("a VTODO was mirrored")
	}
}

// TestParseStable: Exchange restamps DTSTAMP on every fetch. Unchanged events must still give
// identical bytes, or every refresh rewrites the whole calendar.
func TestParseStable(t *testing.T) {
	a, _, err := Parse(context.Background(), "cal_1", fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		b, _, err := Parse(context.Background(), "cal_1", bytes.ReplaceAll(fixture(t), []byte("20261015T120000Z"), []byte("20261016T070000Z")))
		if err != nil {
			t.Fatal(err)
		}
		for i := range a {
			if a[i].Name != b[i].Name || !bytes.Equal(a[i].Data, b[i].Data) {
				t.Fatalf("object %s changed between fetches:\n%s\n---\n%s", a[i].UID, a[i].Data, b[i].Data)
			}
		}
	}
}

func TestParseRefuses(t *testing.T) {
	if _, _, err := Parse(context.Background(), "c", []byte("<html>not a calendar</html>")); Message(err) != string(errNotCalendar) {
		t.Errorf("html: %v", err)
	}
	var many strings.Builder
	many.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\n")
	for i := range MaxObjects + 1 {
		fmt.Fprintf(&many, "BEGIN:VEVENT\r\nUID:%d\r\nDTSTAMP:20260101T000000Z\r\nDTSTART:20260101T000000Z\r\nEND:VEVENT\r\n", i)
	}
	many.WriteString("END:VCALENDAR\r\n")
	if _, _, err := Parse(context.Background(), "c", []byte(many.String())); Message(err) != string(errTooMany) {
		t.Errorf("%d events: %v", MaxObjects+1, err)
	}
}

func TestSealOpen(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	sealed, err := Seal(key, "https://example.com/secret-path")
	if err != nil || strings.Contains(sealed, "secret") {
		t.Fatalf("Seal: %q %v", sealed, err)
	}
	if link, err := Open(key, sealed); err != nil || link != "https://example.com/secret-path" {
		t.Fatalf("Open: %q %v", link, err)
	}
	if _, err := Open(bytes.Repeat([]byte{8}, 32), sealed); err == nil {
		t.Fatal("opened under another key")
	}
}

func allowAll(netip.Addr) error { return nil }

func tlsClient(ts *httptest.Server, policy func(netip.Addr) error) *http.Client {
	pool := x509.NewCertPool()
	pool.AddCert(ts.Certificate())
	return NewClient(policy, pool)
}

func TestFetch(t *testing.T) {
	body := fixture(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/cal.ics", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/gone.ics", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	mux.HandleFunc("/denied.ics", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) })
	mux.HandleFunc("/big.ics", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(make([]byte, MaxBody+1)) })
	mux.HandleFunc("/hop/", func(w http.ResponseWriter, r *http.Request) {
		var n int
		fmt.Sscanf(strings.TrimPrefix(r.URL.Path, "/hop/"), "%d", &n)
		if n == 0 {
			http.Redirect(w, r, "/cal.ics", http.StatusFound)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/hop/%d", n-1), http.StatusFound)
	})
	ts := httptest.NewTLSServer(mux)
	defer ts.Close()
	plain := httptest.NewServer(mux)
	defer plain.Close()
	mux.HandleFunc("/to-http", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/cal.ics", http.StatusFound)
	})

	ctx := context.Background()
	client := tlsClient(ts, allowAll)

	f, err := Fetch(ctx, client, ts.URL+"/cal.ics", "", "")
	if err != nil || !bytes.Equal(f.Body, body) || f.ETag != `"v1"` {
		t.Fatalf("200: %v etag %q", err, f.ETag)
	}
	if f, err := Fetch(ctx, client, ts.URL+"/cal.ics", `"v1"`, ""); err != nil || f.Body != nil || f.ETag != `"v1"` {
		t.Fatalf("304: %v %+v", err, f)
	}
	// Two redirects then the calendar (3 hops in all) loads; one more is refused.
	if f, err := Fetch(ctx, client, ts.URL+"/hop/2", "", ""); err != nil || f.Body == nil {
		t.Fatalf("3 redirects: %v", err)
	}
	for path, want := range map[string]userError{
		"/gone.ics": errGone, "/denied.ics": errStatus, "/big.ics": errTooLarge, "/hop/3": errRedirects, "/to-http": errUnreachable,
	} {
		if _, err := Fetch(ctx, client, ts.URL+path, "", ""); Message(err) != string(want) {
			t.Errorf("%s: %v (%q), want %q", path, err, Message(err), want)
		}
	}
	// The real policy refuses the loopback listener, after DNS, at dial time.
	if _, err := Fetch(ctx, tlsClient(ts, RefusePrivate), ts.URL+"/cal.ics", "", ""); Message(err) != string(errRefused) {
		t.Errorf("loopback: %v", err)
	}
	if _, err := Fetch(ctx, tlsClient(ts, RefusePrivate), strings.Replace(ts.URL, "127.0.0.1", "localhost", 1)+"/cal.ics", "", ""); Message(err) != string(errRefused) {
		t.Errorf("localhost by name: %v", err)
	}
}

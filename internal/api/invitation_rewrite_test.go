package api_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCalDAVSponsoredRewritePaysWriterQuota(t *testing.T) {
	t.Setenv("KYCALENDAR_SCHEDULING", "true")
	srv, st, cfg := setupTestServer(t)
	w := &schedWorld{t: t, srv: srv, st: st, ts: httptest.NewServer(srv), tokens: map[string]string{}}
	t.Cleanup(w.ts.Close)
	for _, name := range []string{"alice", "bob"} {
		w.tokens[name] = schedUser(t, st, name)
	}
	body := meeting("sponsored", "alice", "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:bob@example.com")
	if code, _ := w.put("alice", "sponsored.ics", body, ""); code != http.StatusCreated {
		t.Fatalf("invite: %d", code)
	}
	w.deliver()
	original, copy := w.copyOf("bob", "sponsored")
	if copy == nil || copy.InvitationSender != "usr_alice" {
		t.Fatal("missing sponsored copy")
	}
	filler := meeting("own", "bob", "DESCRIPTION:"+strings.Repeat("x", 4000))
	if code, _ := w.put("bob", "own.ics", filler, ""); code != http.StatusCreated {
		t.Fatalf("fill recipient quota: %d", code)
	}
	_, filled := w.copyOf("bob", "own")
	cfg.Calendar.MaxBytesPerUser = int64(len(filled.Data))
	driver := "sqlite"
	if cfg.Database.Driver == "postgres" {
		driver = "pgx"
	}
	db, err := sql.Open(driver, cfg.Database.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	senderUsage := func() int64 {
		t.Helper()
		var n int64
		err := db.QueryRow(`SELECT COALESCE(SUM(LENGTH(o.data)),0) FROM calendar_objects o JOIN calendars c ON c.id=o.calendar_id WHERE o.invitation_sender='usr_alice' OR (o.invitation_sender='' AND o.invitation_recipient='' AND c.owner_kind='user' AND c.owner_id='usr_alice')`).Scan(&n)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	beforeUsage := senderUsage()
	path := "/dav/usr_bob/calendars/default/" + copy.Name
	grown := strings.Replace(original, "END:VEVENT", "DESCRIPTION:"+strings.Repeat("g", 1000)+"\r\nEND:VEVENT", 1)
	for _, rewrite := range []struct{ name, body string }{{"growth", grown}, {"uid", strings.ReplaceAll(original, "UID:sponsored", "UID:different")}, {"organizer", strings.ReplaceAll(original, "mailto:alice@example.com", "mailto:carol@example.com")}} {
		t.Run(rewrite.name, func(t *testing.T) {
			res := rawDAV(t, w.ts, "PUT", path, "bob", w.tokens["bob"], rewrite.body, map[string]string{"Content-Type": "text/calendar", "If-Match": `"` + copy.ETag + `"`})
			if res.StatusCode != http.StatusInsufficientStorage {
				t.Fatalf("writer at quota changed a sponsored copy: %d %s", res.StatusCode, readAll(res))
			}
			got, err := st.Calendars().GetObject(context.Background(), copy.CalendarID, copy.Name)
			if err != nil {
				t.Fatal(err)
			}
			if got.ETag != copy.ETag || got.InvitationSender != "usr_alice" || string(got.Data) != original || senderUsage() != beforeUsage {
				t.Fatal("refused rewrite changed sender usage or invitation")
			}
		})
	}
	// Once the writer has room, the same enlargement is theirs to store and pay for.
	if err := st.Calendars().DeleteObject(context.Background(), filled.CalendarID, filled.Name, filled.ETag); err != nil {
		t.Fatal(err)
	}
	res := rawDAV(t, w.ts, "PUT", path, "bob", w.tokens["bob"], grown, map[string]string{"Content-Type": "text/calendar", "If-Match": `"` + copy.ETag + `"`})
	if res.StatusCode != 201 && res.StatusCode != 204 {
		t.Fatalf("writer with room: %d %s", res.StatusCode, readAll(res))
	}
	got, err := st.Calendars().GetObject(context.Background(), copy.CalendarID, copy.Name)
	if err != nil {
		t.Fatal(err)
	}
	if got.InvitationSender != "" || got.InvitationRecipient != "usr_bob" || senderUsage() != beforeUsage-int64(len(copy.Data)) {
		t.Fatal("enlarged copy was not transferred to writer")
	}
}

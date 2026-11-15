package api_test

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/kypost"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// A copy the attendee made by answering on the KyPost card stays theirs after the organizer
// moves the meeting (which asks them again, resetting PARTSTAT) and is kept cancelled by the
// CANCEL, with no alarm: the wave 1 end-to-end case (erin), on both delivery paths.
func TestCancelKeepsACopyAnsweredOnTheCard(t *testing.T) {
	alarm := "BEGIN:VALARM\r\nACTION:DISPLAY\r\nDESCRIPTION:Soon\r\nTRIGGER:-PT10M\r\nEND:VALARM\r\nEND:VEVENT"
	// addAlarm is bob's own reminder, put on his copy over CalDAV.
	addAlarm := func(t *testing.T, ts *httptest.Server, token string, st store.Store, uid string) {
		t.Helper()
		o := bobCopy(t, st, uid)
		r := rawDAV(t, ts, "PUT", "/dav/usr_bob/calendars/default/"+o.Name, "bob", token, strings.Replace(string(o.Data), "END:VEVENT", alarm, 1),
			map[string]string{"Content-Type": "text/calendar", "If-Match": `"` + o.ETag + `"`})
		if r.StatusCode != 204 && r.StatusCode != 201 {
			t.Fatalf("bob's alarm: %d %s", r.StatusCode, readAll(r))
		}
	}
	cancelled := func(t *testing.T, st store.Store, uid string) {
		t.Helper()
		o := bobCopy(t, st, uid)
		if o == nil {
			t.Fatal("bob's answered copy was deleted by the cancel")
		}
		got := string(o.Data)
		if !strings.Contains(got, "STATUS:CANCELLED") || strings.Contains(got, "BEGIN:VALARM") || !strings.Contains(got, "mailto:bob@example.com") {
			t.Fatalf("bob's copy after the cancel:\n%s", got)
		}
	}

	t.Run("internal organizer", func(t *testing.T) {
		srv, ts, st, kp := kypostCardWorld(t)
		aliceToken := ssoUser(t, st, "alice")
		deliver := func() { srv.DeliverScheduled(context.Background()) }
		cal := "/dav/usr_alice/calendars/default/m.ics"
		body := meeting("m-card", "alice", "ATTENDEE;PARTSTAT=NEEDS-ACTION;RSVP=TRUE:mailto:bob@example.com")
		if r := rawDAV(t, ts, "PUT", cal, "alice", aliceToken, body, map[string]string{"Content-Type": "text/calendar"}); r.StatusCode != 201 {
			t.Fatalf("invite %d", r.StatusCode)
		}
		deliver() // bob has no account yet: the invitation is mailed
		sent := kp.take()
		if len(sent) != 1 || sent[0].Method != "REQUEST" {
			t.Fatalf("mailed %+v", sent)
		}
		bobToken := ssoUser(t, st, "bob") // bob joins KyCalendar, then answers on the card
		resp := kypost.RespondRequest{User: kypost.User{Sub: "sub-bob", Issuer: "https://id.example"}, UID: "m-card", Partstat: "ACCEPTED", ICS: sent[0].ICS}
		if w := signedPost(t, srv, "/api/itip/respond", kypost.EventRespond, "rsp-card", resp); w.Code != 200 {
			t.Fatalf("respond %d %s", w.Code, w.Body.String())
		}
		deliver()
		if o := bobCopy(t, st, "m-card"); o == nil || o.InvitationSender != "" || o.InvitationRecipient != "usr_bob" {
			t.Fatalf("the copy bob's answer made is not his: %+v", o)
		}
		addAlarm(t, ts, bobToken, st, "m-card")
		// alice moves the meeting: bob is asked again (NEEDS-ACTION), then she deletes it.
		got := readAll(rawDAV(t, ts, "GET", cal, "alice", aliceToken, "", nil))
		moved := strings.ReplaceAll(got, "T090000Z", "T110000Z")
		moved = strings.ReplaceAll(moved, "DTEND:20261110T100000Z", "DTEND:20261110T120000Z")
		if r := rawDAV(t, ts, "PUT", cal, "alice", aliceToken, moved, map[string]string{"Content-Type": "text/calendar"}); r.StatusCode != 204 && r.StatusCode != 201 {
			t.Fatalf("move %d %s", r.StatusCode, readAll(r))
		}
		deliver()
		if o := bobCopy(t, st, "m-card"); o == nil || !strings.Contains(string(o.Data), "DTSTART:20261110T110000Z") || !strings.Contains(string(o.Data), "PARTSTAT=NEEDS-ACTION") {
			t.Fatalf("bob's copy after the move: %+v", o)
		}
		if r := rawDAV(t, ts, "DELETE", cal, "alice", aliceToken, "", nil); r.StatusCode != 204 {
			t.Fatalf("delete %d", r.StatusCode)
		}
		deliver()
		cancelled(t, st, "m-card")
		// The cancelled copy is free time and still bob's to delete.
		fb := readAll(rawDAV(t, ts, "REPORT", "/dav/usr_bob/calendars/default/", "bob", bobToken, freeBusyQuery("20261110T000000Z", "20261111T000000Z"),
			map[string]string{"Content-Type": "application/xml", "Depth": "1"}))
		if !strings.Contains(fb, "BEGIN:VFREEBUSY") || strings.Contains(fb, "FREEBUSY:") || strings.Contains(fb, "FREEBUSY;") {
			t.Fatalf("bob's cancelled copy reads busy:\n%s", fb)
		}
		o := bobCopy(t, st, "m-card")
		if r := rawDAV(t, ts, "DELETE", "/dav/usr_bob/calendars/default/"+o.Name, "bob", bobToken, "", nil); r.StatusCode != 204 {
			t.Fatalf("bob deletes his cancelled copy: %d", r.StatusCode)
		}
	})

	t.Run("kypost forwarded", func(t *testing.T) {
		srv, ts, st, _ := kypostCardWorld(t)
		bobToken := ssoUser(t, st, "bob")
		bob := kypost.User{Sub: "sub-bob", Issuer: "https://id.example"}
		msg := func(method, seq, start, extra string) string {
			return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nMETHOD:" + method + "\r\nBEGIN:VEVENT\r\nUID:dave-card\r\nDTSTAMP:20261101T000000Z\r\nSEQUENCE:" + seq + "\r\n" +
				"DTSTART:" + start + "\r\nDURATION:PT1H\r\nSUMMARY:Supplier call\r\nORGANIZER:mailto:dave@example.org\r\n" + extra +
				"ATTENDEE;PARTSTAT=NEEDS-ACTION;RSVP=TRUE:mailto:bob@example.com\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
		}
		inbound := func(id, ics string) string {
			t.Helper()
			w := signedPost(t, srv, "/api/itip/inbound", kypost.EventInbound, id, kypost.InboundRequest{User: bob, From: "dave@example.org", DKIMAligned: true, ICS: ics})
			if w.Code != 200 {
				t.Fatalf("inbound %s: %d %s", id, w.Code, w.Body.String())
			}
			return w.Body.String()
		}
		// A CANCEL for a meeting bob never had creates nothing.
		if got := inbound("c-0", msg("CANCEL", "0", "20261112T090000Z", "STATUS:CANCELLED\r\n")); !strings.Contains(got, "ignored") || bobCopy(t, st, "dave-card") != nil {
			t.Fatalf("a cancel without a copy: %s", got)
		}
		invite := msg("REQUEST", "0", "20261112T090000Z", "")
		inbound("r-0", invite)
		if w := signedPost(t, srv, "/api/itip/respond", kypost.EventRespond, "rsp-d", kypost.RespondRequest{User: bob, UID: "dave-card", Partstat: "ACCEPTED", ICS: invite}); w.Code != 200 {
			t.Fatalf("respond %d %s", w.Code, w.Body.String())
		}
		addAlarm(t, ts, bobToken, st, "dave-card")
		inbound("r-1", msg("REQUEST", "1", "20261112T110000Z", "")) // dave moves it: bob is asked again
		if o := bobCopy(t, st, "dave-card"); o == nil || !strings.Contains(string(o.Data), "PARTSTAT=NEEDS-ACTION") {
			t.Fatalf("bob's copy after the move: %+v", o)
		}
		// A late CANCEL older than the move is ignored.
		if got := inbound("c-stale", msg("CANCEL", "0", "20261112T090000Z", "STATUS:CANCELLED\r\n")); !strings.Contains(got, "ignored") {
			t.Fatalf("a stale cancel: %s", got)
		}
		if o := bobCopy(t, st, "dave-card"); o == nil || strings.Contains(string(o.Data), "CANCELLED") {
			t.Fatalf("bob's copy after a stale cancel: %+v", o)
		}
		if got := inbound("c-1", msg("CANCEL", "2", "20261112T110000Z", "STATUS:CANCELLED\r\n")); !strings.Contains(got, "applied") {
			t.Fatalf("dave's cancel: %s", got)
		}
		cancelled(t, st, "dave-card")
		if got := string(bobCopy(t, st, "dave-card").Data); !strings.Contains(got, "SEQUENCE:2") {
			t.Fatalf("the cancel's SEQUENCE was not taken:\n%s", got)
		}
	})
}

// kypostCardWorld is a server linked to a fake KyPost with scheduling on, and that fake.
func kypostCardWorld(t *testing.T) (*api.Server, *httptest.Server, store.Store, *fakeKyPost) {
	t.Helper()
	t.Setenv("KYCALENDAR_SCHEDULING", "true")
	t.Setenv("KYCALENDAR_KYPOST_URL", "https://kypost.example")
	secretFile := filepath.Join(t.TempDir(), "kypost.secret")
	if err := os.WriteFile(secretFile, append(kypostSecret, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KYCALENDAR_KYPOST_SECRET_FILE", secretFile)
	srv, st, _ := setupTestServer(t)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	fake := &fakeKyPost{}
	kp := httptest.NewTLSServer(fake)
	t.Cleanup(kp.Close)
	api.SetKyPostClientForTest(srv, &kypost.Client{BaseURL: kp.URL, Secret: kypostSecret, HTTP: kp.Client()})
	return srv, ts, st, fake
}

// bobCopy is bob's object with uid in his default calendar, or nil.
func bobCopy(t *testing.T, st store.Store, uid string) *store.CalendarObject {
	t.Helper()
	c, err := st.Calendars().GetCalendarBySlug(context.Background(), "user", "usr_bob", "default")
	if err != nil {
		return nil
	}
	o, err := st.Calendars().GetObjectByUID(context.Background(), c.ID, uid)
	if err != nil {
		return nil
	}
	return o
}

package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Busnes-app/ky-primitives/syncauth"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/apppass"
	"github.com/Busnes-app/kycalendar/internal/kypost"
	"github.com/Busnes-app/kycalendar/internal/store"
)

var kypostSecret = []byte("kypost-test-secret-0123456789abcdef")

// fakeKyPost records every signed send it accepts.
type fakeKyPost struct {
	mu   sync.Mutex
	sent []kypost.SendRequest
}

func (f *fakeKyPost) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	h := syncauth.FromRequest(r)
	if _, err := syncauth.Verify(kypostSecret, h, body, syncauth.Options{}); err != nil || h.EventType != kypost.EventSend {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}
	var req kypost.SendRequest
	_ = json.Unmarshal(body, &req)
	f.mu.Lock()
	f.sent = append(f.sent, req)
	f.mu.Unlock()
	w.WriteHeader(http.StatusAccepted)
}

func (f *fakeKyPost) take() []kypost.SendRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.sent
	f.sent = nil
	return out
}

// ssoUser is an everyday user bound to KyIdentity (as KyPost knows them), with an email and a
// dav token.
func ssoUser(t *testing.T, st store.Store, name string) string {
	t.Helper()
	ctx := context.Background()
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_" + name, Username: name, Email: name + "@example.com", Role: "user", Status: "active",
		SSOProvider: "kysignon", SSOSubject: "sub-" + name, SSOIssuer: "kyidentity https://id.example"}); err != nil {
		t.Fatal(err)
	}
	id, token, hash, err := apppass.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AppPasswords().Create(ctx, store.Seed, &store.AppPassword{Scope: store.ScopeDAV, ID: id, UserID: "usr_" + name, Label: "t", Hash: hash}, 0); err != nil {
		t.Fatal(err)
	}
	return token
}

func signedPost(t *testing.T, srv http.Handler, path, event, id string, v any) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(v)
	h, err := syncauth.Sign(kypostSecret, time.Now(), event, id, body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", path, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	h.Apply(r)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	return w
}

func TestKyPostRoutes(t *testing.T) {
	t.Setenv("KYCALENDAR_SCHEDULING", "true")
	t.Setenv("KYCALENDAR_KYPOST_URL", "https://kypost.example")
	secretFile := filepath.Join(t.TempDir(), "kypost.secret")
	if err := os.WriteFile(secretFile, append(kypostSecret, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KYCALENDAR_KYPOST_SECRET_FILE", secretFile)
	srv, st, _ := setupTestServer(t)
	fake := &fakeKyPost{}
	kp := httptest.NewTLSServer(fake)
	defer kp.Close()
	api.SetKyPostClientForTest(srv, &kypost.Client{BaseURL: kp.URL, Secret: kypostSecret, HTTP: kp.Client()})
	ts := httptest.NewServer(srv)
	defer ts.Close()
	tokens := map[string]string{"alice": ssoUser(t, st, "alice"), "bob": ssoUser(t, st, "bob")}
	deliver := func() { srv.DeliverScheduled(context.Background()) }
	alice := kypost.User{Sub: "sub-alice", Issuer: "https://id.example"}
	bob := kypost.User{Sub: "sub-bob", Issuer: "https://id.example"}

	// Outbound: an invitation to someone outside goes to KyPost, signed, as alice.
	cal := "/dav/usr_alice/calendars/default/"
	body := meeting("ext1", "alice", "ATTENDEE:mailto:carol@example.net")
	if r := rawDAV(t, ts, "PUT", cal+"ext1.ics", "alice", tokens["alice"], body, map[string]string{"Content-Type": "text/calendar"}); r.StatusCode != 201 {
		t.Fatalf("PUT %d", r.StatusCode)
	}
	deliver()
	sent := fake.take()
	if len(sent) != 1 || sent[0].Method != "REQUEST" || sent[0].From != "alice@example.com" || sent[0].Sub != "sub-alice" ||
		len(sent[0].Recipients) != 1 || sent[0].Recipients[0] != "carol@example.net" || !strings.Contains(sent[0].ICS, "METHOD:REQUEST") {
		t.Fatalf("sent %+v", sent)
	}

	// Inbound REPLY from carol lands on alice's event; its replay is acknowledged, not reapplied.
	reply := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nMETHOD:REPLY\r\nBEGIN:VEVENT\r\nUID:ext1\r\nDTSTAMP:20261101T000000Z\r\n" +
		"ORGANIZER:mailto:alice@example.com\r\nATTENDEE;PARTSTAT=ACCEPTED:mailto:carol@example.net\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	in := kypost.InboundRequest{User: alice, From: "carol@example.net", DKIMAligned: true, ICS: reply}
	if w := signedPost(t, srv, "/api/itip/inbound", kypost.EventInbound, "in-1", in); w.Code != 200 || !strings.Contains(w.Body.String(), "applied") {
		t.Fatalf("inbound reply %d %s", w.Code, w.Body.String())
	}
	if got := readAll(rawDAV(t, ts, "GET", cal+"ext1.ics", "alice", tokens["alice"], "", nil)); !strings.Contains(got, "PARTSTAT=ACCEPTED:mailto:carol@example.net") {
		t.Fatalf("alice's event after carol's reply:\n%s", got)
	}
	if w := signedPost(t, srv, "/api/itip/inbound", kypost.EventInbound, "in-1", in); w.Code != 200 || !strings.Contains(w.Body.String(), "duplicate") {
		t.Errorf("replay %d %s", w.Code, w.Body.String())
	}
	// Refusals: unsigned, unverified sender, a reply from someone who is not an attendee.
	unsigned := httptest.NewRecorder()
	srv.ServeHTTP(unsigned, httptest.NewRequest("POST", "/api/itip/inbound", strings.NewReader(`{}`)))
	if unsigned.Code != 401 {
		t.Errorf("unsigned: %d", unsigned.Code)
	}
	in.DKIMAligned = false
	if w := signedPost(t, srv, "/api/itip/inbound", kypost.EventInbound, "in-2", in); w.Code != 422 {
		t.Errorf("unverified sender: %d", w.Code)
	}
	forged := kypost.InboundRequest{User: alice, From: "mallory@example.org", DKIMAligned: true, ICS: strings.Replace(reply, "carol@example.net", "mallory@example.org", 1)}
	if w := signedPost(t, srv, "/api/itip/inbound", kypost.EventInbound, "in-3", forged); w.Code != 200 || !strings.Contains(w.Body.String(), "ignored") {
		t.Errorf("a non-attendee's reply: %d %s", w.Code, w.Body.String())
	}

	// A new invitation from outside waits in the mailbox; bob's answer writes it and replies.
	invite := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nMETHOD:REQUEST\r\nBEGIN:VEVENT\r\nUID:dave-1\r\nDTSTAMP:20261101T000000Z\r\n" +
		"DTSTART:20261112T090000Z\r\nDTEND:20261112T100000Z\r\nSUMMARY:Supplier call\r\nORGANIZER:mailto:dave@example.org\r\n" +
		"ATTENDEE;RSVP=TRUE:mailto:bob@example.com\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	req := kypost.InboundRequest{User: bob, From: "dave@example.org", DKIMAligned: true, ICS: invite}
	if w := signedPost(t, srv, "/api/itip/inbound", kypost.EventInbound, "in-4", req); !strings.Contains(w.Body.String(), "pending") {
		t.Fatalf("new external invitation: %d %s", w.Code, w.Body.String())
	}
	if r := rawDAV(t, ts, "GET", "/dav/usr_bob/calendars/default/", "bob", tokens["bob"], "", nil); strings.Contains(readAll(r), "dave-1") {
		t.Fatal("an unanswered external invitation was written")
	}
	resp := kypost.RespondRequest{User: bob, UID: "dave-1", Partstat: "ACCEPTED", ICS: invite}
	if w := signedPost(t, srv, "/api/itip/respond", kypost.EventRespond, "rsp-1", resp); w.Code != 200 {
		t.Fatalf("respond %d %s", w.Code, w.Body.String())
	}
	deliver()
	sent = fake.take()
	if len(sent) != 1 || sent[0].Method != "REPLY" || sent[0].From != "bob@example.com" || sent[0].Recipients[0] != "dave@example.org" ||
		!strings.Contains(sent[0].ICS, "PARTSTAT=ACCEPTED:mailto:bob@example.com") {
		t.Fatalf("bob's reply %+v", sent)
	}

	// Dave cancels: bob's copy is cancelled. Mallory "cancelling" dave's meeting changes nothing.
	cancel := strings.Replace(strings.Replace(invite, "METHOD:REQUEST", "METHOD:CANCEL", 1), "SUMMARY:Supplier call", "SUMMARY:Supplier call\r\nSTATUS:CANCELLED", 1)
	fake2 := kypost.InboundRequest{User: bob, From: "mallory@example.org", DKIMAligned: true, ICS: strings.Replace(cancel, "dave@example.org", "mallory@example.org", 1)}
	if w := signedPost(t, srv, "/api/itip/inbound", kypost.EventInbound, "in-5", fake2); !strings.Contains(w.Body.String(), "ignored") {
		t.Errorf("an impostor's cancel: %s", w.Body.String())
	}
	if w := signedPost(t, srv, "/api/itip/inbound", kypost.EventInbound, "in-6", kypost.InboundRequest{User: bob, From: "dave@example.org", DKIMAligned: true, ICS: cancel}); !strings.Contains(w.Body.String(), "applied") {
		t.Fatalf("dave's cancel: %s", w.Body.String())
	}
	r := rawDAV(t, ts, "REPORT", "/dav/usr_bob/calendars/default/", "bob", tokens["bob"],
		`<c:calendar-query xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:prop><c:calendar-data/></d:prop><c:filter><c:comp-filter name="VCALENDAR"/></c:filter></c:calendar-query>`,
		map[string]string{"Content-Type": "application/xml", "Depth": "1"})
	if got := readAll(r); !strings.Contains(got, "dave-1") || !strings.Contains(got, "STATUS:CANCELLED") {
		t.Fatalf("bob's copy after dave cancelled:\n%s", got)
	}

	// An outsider's invitation reusing the UID of alice's meeting cannot answer for that meeting.
	if r := rawDAV(t, ts, "PUT", cal+"shared.ics", "alice", tokens["alice"], meeting("shared", "alice", "ATTENDEE:mailto:bob@example.com"),
		map[string]string{"Content-Type": "text/calendar"}); r.StatusCode != 201 {
		t.Fatalf("alice's meeting %d", r.StatusCode)
	}
	deliver()
	fake.take()
	evil := strings.Replace(strings.Replace(invite, "UID:dave-1", "UID:shared", 1), "dave@example.org", "evil@example.org", 1)
	resp = kypost.RespondRequest{User: bob, UID: "shared", Partstat: "DECLINED", ICS: evil}
	if w := signedPost(t, srv, "/api/itip/respond", kypost.EventRespond, "rsp-2", resp); w.Code != 422 {
		t.Fatalf("answering through a colliding invitation: %d %s", w.Code, w.Body.String())
	}
	if w := signedPost(t, srv, "/api/itip/respond", kypost.EventRespond, "rsp-3", kypost.RespondRequest{User: bob, UID: "shared", Partstat: "DECLINED"}); w.Code != 422 {
		t.Errorf("an answer without the invitation: %d", w.Code)
	}
	deliver()
	if sent := fake.take(); len(sent) != 0 {
		t.Fatalf("a refused answer sent %+v", sent)
	}
	if got := readAll(rawDAV(t, ts, "GET", cal+"shared.ics", "alice", tokens["alice"], "", nil)); strings.Contains(got, "DECLINED") {
		t.Fatalf("alice's meeting took bob's answer to another organizer's invitation:\n%s", got)
	}
}

// kypostBob is a server linked to KyPost with scheduling on and bob, an SSO user, and his dav token.
func kypostBob(t *testing.T) (*api.Server, *httptest.Server, string) {
	srv, ts, _, token := kypostWorld(t)
	return srv, ts, token
}

// kypostWorld is kypostBob with the store.
func kypostWorld(t *testing.T) (*api.Server, *httptest.Server, store.Store, string) {
	t.Helper()
	t.Setenv("KYCALENDAR_SCHEDULING", "true")
	t.Setenv("KYCALENDAR_KYPOST_URL", "https://kypost.example")
	secretFile := filepath.Join(t.TempDir(), "kypost.secret")
	if err := os.WriteFile(secretFile, append(kypostSecret, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KYCALENDAR_KYPOST_SECRET_FILE", secretFile)
	srv, st, _ := setupTestServer(t)
	kp := httptest.NewTLSServer(&fakeKyPost{})
	t.Cleanup(kp.Close)
	api.SetKyPostClientForTest(srv, &kypost.Client{BaseURL: kp.URL, Secret: kypostSecret, HTTP: kp.Client()})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return srv, ts, st, ssoUser(t, st, "bob")
}

// A message refused with 429 was not acted on: KyPost's retry under the same event ID is not a
// duplicate.
func TestKyPostRetryAfterRefusalIsNotADuplicate(t *testing.T) {
	srv, _, _ := kypostBob(t)
	reply := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nMETHOD:REPLY\r\nBEGIN:VEVENT\r\nUID:none\r\nDTSTAMP:20261101T000000Z\r\n" +
		"ORGANIZER:mailto:bob@example.com\r\nATTENDEE;PARTSTAT=ACCEPTED:mailto:carol@example.net\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	in := kypost.InboundRequest{User: kypost.User{Sub: "sub-bob", Issuer: "https://id.example"}, From: "carol@example.net", DKIMAligned: true, ICS: reply}
	limited := ""
	for i := 0; i < 100 && limited == ""; i++ {
		id := fmt.Sprintf("flood-%d", i)
		if w := signedPost(t, srv, "/api/itip/inbound", kypost.EventInbound, id, in); w.Code == http.StatusTooManyRequests {
			limited = id
		}
	}
	if limited == "" {
		t.Fatal("never rate limited")
	}
	if w := signedPost(t, srv, "/api/itip/inbound", kypost.EventInbound, limited, in); w.Code != http.StatusTooManyRequests {
		t.Fatalf("the retry of a refused message: %d %s", w.Code, w.Body.String())
	}
}

// An outside organizer moving or cancelling one occurrence sends only that occurrence: bob's copy
// keeps the series and every other occurrence.
func TestKyPostInboundOneOccurrence(t *testing.T) {
	srv, ts, token := kypostBob(t)
	bob := kypost.User{Sub: "sub-bob", Issuer: "https://id.example"}
	vevent := func(extra string) string {
		return "BEGIN:VEVENT\r\nUID:dave-w\r\nDTSTAMP:20261101T000000Z\r\n" + extra + "SUMMARY:Supplier call\r\n" +
			"ORGANIZER:mailto:dave@example.org\r\nATTENDEE;RSVP=TRUE:mailto:bob@example.com\r\nEND:VEVENT\r\n"
	}
	msg := func(method, body string) string {
		return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nMETHOD:" + method + "\r\n" + body + "END:VCALENDAR\r\n"
	}
	invite := msg("REQUEST", vevent("DTSTART:20261112T090000Z\r\nDTEND:20261112T100000Z\r\nRRULE:FREQ=WEEKLY;COUNT=4\r\n"))
	if w := signedPost(t, srv, "/api/itip/respond", kypost.EventRespond, "rsp-w", kypost.RespondRequest{User: bob, UID: "dave-w", Partstat: "ACCEPTED", ICS: invite}); w.Code != 200 {
		t.Fatalf("respond %d %s", w.Code, w.Body.String())
	}
	copyOf := func() string {
		t.Helper()
		r := rawDAV(t, ts, "REPORT", "/dav/usr_bob/calendars/default/", "bob", token,
			`<c:calendar-query xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:prop><c:calendar-data/></d:prop><c:filter><c:comp-filter name="VCALENDAR"/></c:filter></c:calendar-query>`,
			map[string]string{"Content-Type": "application/xml", "Depth": "1"})
		return readAll(r)
	}
	moved := msg("REQUEST", vevent("RECURRENCE-ID:20261119T090000Z\r\nDTSTART:20261119T140000Z\r\nDTEND:20261119T150000Z\r\nSEQUENCE:1\r\n"))
	in := kypost.InboundRequest{User: bob, From: "dave@example.org", DKIMAligned: true, ICS: moved}
	if w := signedPost(t, srv, "/api/itip/inbound", kypost.EventInbound, "in-w1", in); !strings.Contains(w.Body.String(), "applied") {
		t.Fatalf("moving one occurrence: %d %s", w.Code, w.Body.String())
	}
	got := copyOf()
	if !strings.Contains(got, "RRULE:FREQ=WEEKLY;COUNT=4") || !strings.Contains(got, "DTSTART:20261119T140000Z") || !strings.Contains(got, "PARTSTAT=ACCEPTED") {
		t.Fatalf("bob's copy after one occurrence moved:\n%s", got)
	}
	in.ICS = msg("CANCEL", vevent("RECURRENCE-ID:20261126T090000Z\r\nDTSTART:20261126T090000Z\r\nSEQUENCE:1\r\nSTATUS:CANCELLED\r\n"))
	if w := signedPost(t, srv, "/api/itip/inbound", kypost.EventInbound, "in-w2", in); !strings.Contains(w.Body.String(), "applied") {
		t.Fatalf("cancelling one occurrence: %d %s", w.Code, w.Body.String())
	}
	got = copyOf()
	if strings.Count(got, "STATUS:CANCELLED") != 1 || !strings.Contains(got, "RRULE:FREQ=WEEKLY;COUNT=4") || !strings.Contains(got, "DTSTART:20261119T140000Z") {
		t.Fatalf("bob's copy after one occurrence was cancelled:\n%s", got)
	}
	for _, part := range strings.Split(got, "BEGIN:VEVENT")[1:] {
		if strings.Contains(part, "STATUS:CANCELLED") != strings.Contains(part, "RECURRENCE-ID:20261126T090000Z") {
			t.Fatalf("only the 26 November occurrence is cancelled:\n%s", got)
		}
	}

}

// Real clients name an occurrence of a zoned series in its zone (RECURRENCE-ID;TZID=...): moving
// and cancelling one that way reaches the right occurrence, and its answer is kept for a change
// that is not significant.
func TestKyPostInboundOneOccurrenceOfAZonedSeries(t *testing.T) {
	srv, ts, token := kypostBob(t)
	bob := kypost.User{Sub: "sub-bob", Issuer: "https://id.example"}
	vevent := func(extra string) string {
		return "BEGIN:VEVENT\r\nUID:dave-z\r\nDTSTAMP:20261101T000000Z\r\n" + extra + "SUMMARY:Supplier call\r\n" +
			"ORGANIZER:mailto:dave@example.org\r\nATTENDEE;RSVP=TRUE:mailto:bob@example.com\r\nEND:VEVENT\r\n"
	}
	msg := func(method, body string) string {
		return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nMETHOD:" + method + "\r\n" + body + "END:VCALENDAR\r\n"
	}
	invite := msg("REQUEST", vevent("DTSTART;TZID=Europe/Berlin:20261112T090000\r\nDTEND;TZID=Europe/Berlin:20261112T100000\r\nRRULE:FREQ=WEEKLY;COUNT=4\r\n"))
	if w := signedPost(t, srv, "/api/itip/respond", kypost.EventRespond, "rsp-z", kypost.RespondRequest{User: bob, UID: "dave-z", Partstat: "ACCEPTED", ICS: invite}); w.Code != 200 {
		t.Fatalf("respond %d %s", w.Code, w.Body.String())
	}
	copyOf := func() string {
		t.Helper()
		r := rawDAV(t, ts, "REPORT", "/dav/usr_bob/calendars/default/", "bob", token,
			`<c:calendar-query xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:prop><c:calendar-data/></d:prop><c:filter><c:comp-filter name="VCALENDAR"/></c:filter></c:calendar-query>`,
			map[string]string{"Content-Type": "application/xml", "Depth": "1"})
		return strings.ReplaceAll(readAll(r), "\r\n ", "")
	}
	in := kypost.InboundRequest{User: bob, From: "dave@example.org", DKIMAligned: true}
	in.ICS = msg("REQUEST", vevent("RECURRENCE-ID;TZID=Europe/Berlin:20261119T090000\r\nDTSTART;TZID=Europe/Berlin:20261119T090000\r\nDTEND;TZID=Europe/Berlin:20261119T100000\r\nDESCRIPTION:Agenda attached\r\n"))
	if w := signedPost(t, srv, "/api/itip/inbound", kypost.EventInbound, "in-z1", in); !strings.Contains(w.Body.String(), "applied") {
		t.Fatalf("updating one occurrence: %d %s", w.Code, w.Body.String())
	}
	got := copyOf()
	if strings.Count(got, "BEGIN:VEVENT") != 2 || !strings.Contains(got, "Agenda attached") || strings.Count(got, "PARTSTAT=ACCEPTED") != 2 {
		t.Fatalf("bob's copy after one occurrence changed its description:\n%s", got)
	}
	// Clients send the zone's VTIMEZONE alongside.
	berlin := "BEGIN:VTIMEZONE\r\nTZID:Europe/Berlin\r\nBEGIN:STANDARD\r\nDTSTART:19701025T030000\r\nRRULE:FREQ=YEARLY;BYMONTH=10;BYDAY=-1SU\r\n" +
		"TZOFFSETFROM:+0200\r\nTZOFFSETTO:+0100\r\nEND:STANDARD\r\nBEGIN:DAYLIGHT\r\nDTSTART:19700329T020000\r\nRRULE:FREQ=YEARLY;BYMONTH=3;BYDAY=-1SU\r\n" +
		"TZOFFSETFROM:+0100\r\nTZOFFSETTO:+0200\r\nEND:DAYLIGHT\r\nEND:VTIMEZONE\r\n"
	in.ICS = msg("CANCEL", berlin+vevent("RECURRENCE-ID;TZID=Europe/Berlin:20261126T090000\r\nDTSTART;TZID=Europe/Berlin:20261126T090000\r\nSTATUS:CANCELLED\r\n"))
	if w := signedPost(t, srv, "/api/itip/inbound", kypost.EventInbound, "in-z2", in); !strings.Contains(w.Body.String(), "applied") {
		t.Fatalf("cancelling one occurrence: %d %s", w.Code, w.Body.String())
	}
	got = copyOf()
	cancelled := 0
	for _, part := range strings.Split(got, "BEGIN:VEVENT")[1:] {
		if strings.Contains(part, "STATUS:CANCELLED") {
			cancelled++
			if !strings.Contains(part, "RECURRENCE-ID;TZID=Europe/Berlin:20261126T090000") {
				t.Fatalf("the wrong occurrence was cancelled:\n%s", got)
			}
		}
	}
	if cancelled != 1 || !strings.Contains(got, "RRULE:FREQ=WEEKLY;COUNT=4") {
		t.Fatalf("bob's copy after one occurrence was cancelled:\n%s", got)
	}
	in.ICS = msg("CANCEL", vevent("RECURRENCE-ID;TZID=Europe/Berlin:20261127T090000\r\nDTSTART;TZID=Europe/Berlin:20261127T090000\r\nSTATUS:CANCELLED\r\n"))
	if w := signedPost(t, srv, "/api/itip/inbound", kypost.EventInbound, "in-z3", in); !strings.Contains(w.Body.String(), "ignored") {
		t.Fatalf("cancelling a day the series does not have: %d %s", w.Code, w.Body.String())
	}
}

// Bob keeps his copy of an outside organizer's meeting in a group calendar he can edit: the
// organizer's cancel still reaches it there.
func TestKyPostInboundReachesACopyInAGroupCalendar(t *testing.T) {
	srv, ts, st, token := kypostWorld(t)
	team := groupCalendar(t, st, "Team")
	grantRole(t, st, team, "editor", "usr_bob")
	invite := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nBEGIN:VEVENT\r\nUID:dave-g\r\nDTSTAMP:20261101T000000Z\r\n" +
		"DTSTART:20261112T090000Z\r\nDTEND:20261112T100000Z\r\nSUMMARY:Supplier call\r\nORGANIZER:mailto:dave@example.org\r\n" +
		"ATTENDEE;PARTSTAT=ACCEPTED:mailto:bob@example.com\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	if r := rawDAV(t, ts, "PUT", "/dav/usr_bob/calendars/_"+team.ID+"/dave-g.ics", "bob", token, invite, map[string]string{"Content-Type": "text/calendar"}); r.StatusCode != 201 {
		t.Fatalf("PUT %d", r.StatusCode)
	}
	cancel := strings.Replace(strings.Replace(invite, "PRODID:x\r\n", "PRODID:x\r\nMETHOD:CANCEL\r\n", 1), "SUMMARY", "STATUS:CANCELLED\r\nSUMMARY", 1)
	in := kypost.InboundRequest{User: kypost.User{Sub: "sub-bob", Issuer: "https://id.example"}, From: "dave@example.org", DKIMAligned: true, ICS: cancel}
	if w := signedPost(t, srv, "/api/itip/inbound", kypost.EventInbound, "in-g", in); !strings.Contains(w.Body.String(), "applied") {
		t.Fatalf("cancel: %d %s", w.Code, w.Body.String())
	}
	if o, err := st.Calendars().GetObjectByUID(context.Background(), team.ID, "dave-g"); err != nil || !strings.Contains(string(o.Data), "STATUS:CANCELLED") {
		t.Fatalf("bob's copy in the group calendar was not cancelled (%v)", err)
	}
}

// Bob is invited to single occurrences only (no master), as Outlook invites someone added to one
// occurrence: his answer to one is recorded, a second occurrence is added beside the first, and a
// CANCEL of one leaves the other.
func TestKyPostInvitationToSingleOccurrences(t *testing.T) {
	srv, ts, token := kypostBob(t)
	bob := kypost.User{Sub: "sub-bob", Issuer: "https://id.example"}
	occurrence := func(rid, extra string) string {
		return "BEGIN:VEVENT\r\nUID:dave-o\r\nDTSTAMP:20261101T000000Z\r\nRECURRENCE-ID;TZID=Europe/Berlin:" + rid + "\r\nDTSTART;TZID=Europe/Berlin:" + rid +
			"\r\nDTEND;TZID=Europe/Berlin:" + strings.Replace(rid, "T09", "T10", 1) + "\r\n" + extra + "SUMMARY:Supplier call\r\n" +
			"ORGANIZER:mailto:dave@example.org\r\nATTENDEE;RSVP=TRUE:mailto:bob@example.com\r\nEND:VEVENT\r\n"
	}
	msg := func(method, body string) string {
		return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nMETHOD:" + method + "\r\n" + body + "END:VCALENDAR\r\n"
	}
	copyOf := func() string {
		t.Helper()
		r := rawDAV(t, ts, "REPORT", "/dav/usr_bob/calendars/default/", "bob", token,
			`<c:calendar-query xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:prop><c:calendar-data/></d:prop><c:filter><c:comp-filter name="VCALENDAR"/></c:filter></c:calendar-query>`,
			map[string]string{"Content-Type": "application/xml", "Depth": "1"})
		return readAll(r)
	}
	invite := msg("REQUEST", occurrence("20261119T090000", ""))
	// KyPost names the occurrence as the mail writes it: wall time in Berlin.
	resp := kypost.RespondRequest{User: bob, UID: "dave-o", RecurrenceID: "20261119T090000", Partstat: "ACCEPTED", ICS: invite}
	if w := signedPost(t, srv, "/api/itip/respond", kypost.EventRespond, "rsp-o", resp); w.Code != 200 {
		t.Fatalf("answering one occurrence as mailed: %d %s", w.Code, w.Body.String())
	}
	if got := copyOf(); !strings.Contains(got, "PARTSTAT=ACCEPTED") {
		t.Fatalf("bob's answer was not recorded:\n%s", got)
	}
	in := kypost.InboundRequest{User: bob, From: "dave@example.org", DKIMAligned: true, ICS: msg("REQUEST", occurrence("20261126T090000", ""))}
	if w := signedPost(t, srv, "/api/itip/inbound", kypost.EventInbound, "in-o1", in); !strings.Contains(w.Body.String(), "applied") {
		t.Fatalf("a second occurrence: %d %s", w.Code, w.Body.String())
	}
	got := copyOf()
	if !strings.Contains(got, "RECURRENCE-ID;TZID=Europe/Berlin:20261119T090000") || !strings.Contains(got, "RECURRENCE-ID;TZID=Europe/Berlin:20261126T090000") || !strings.Contains(got, "PARTSTAT=ACCEPTED") {
		t.Fatalf("the second occurrence replaced the first:\n%s", got)
	}
	in.ICS = msg("CANCEL", occurrence("20261126T090000", "STATUS:CANCELLED\r\n"))
	if w := signedPost(t, srv, "/api/itip/inbound", kypost.EventInbound, "in-o2", in); !strings.Contains(w.Body.String(), "applied") {
		t.Fatalf("cancelling one occurrence: %d %s", w.Code, w.Body.String())
	}
	got = copyOf()
	for _, part := range strings.Split(got, "BEGIN:VEVENT")[1:] {
		if strings.Contains(part, "STATUS:CANCELLED") != strings.Contains(part, "RECURRENCE-ID;TZID=Europe/Berlin:20261126T090000") {
			t.Fatalf("only 26 November is cancelled:\n%s", got)
		}
	}
	// An occurrence the invitation does not have is a permanent refusal, not a retried 500.
	resp.RecurrenceID = "20261203T090000"
	if w := signedPost(t, srv, "/api/itip/respond", kypost.EventRespond, "rsp-o2", resp); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("answering an occurrence bob is not invited to: %d %s", w.Code, w.Body.String())
	}
}

// A REQUEST or CANCEL older than what bob's copy last took from its organizer (a late retry or
// replay) changes nothing; bob's own edit raising his copy's SEQUENCE does not stop the
// organizer's next update.
func TestKyPostStaleOrganizerMessagesAreIgnored(t *testing.T) {
	srv, _, st, _ := kypostWorld(t)
	bob := kypost.User{Sub: "sub-bob", Issuer: "https://id.example"}
	ctx := context.Background()
	msg := func(method, seq, start string) string {
		return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nMETHOD:" + method + "\r\nBEGIN:VEVENT\r\nUID:dave-s\r\nDTSTAMP:20261101T000000Z\r\nSEQUENCE:" + seq +
			"\r\nDTSTART:" + start + "\r\nDTEND:20261112T230000Z\r\nSUMMARY:Supplier call\r\nORGANIZER:mailto:dave@example.org\r\n" +
			"ATTENDEE;PARTSTAT=ACCEPTED;RSVP=TRUE:mailto:bob@example.com\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	}
	if w := signedPost(t, srv, "/api/itip/respond", kypost.EventRespond, "rsp-s", kypost.RespondRequest{User: bob, UID: "dave-s", Partstat: "ACCEPTED", ICS: msg("REQUEST", "0", "20261112T090000Z")}); w.Code != 200 {
		t.Fatalf("respond %d %s", w.Code, w.Body.String())
	}
	copyOf := func() *store.CalendarObject {
		t.Helper()
		def, err := st.Calendars().GetCalendarBySlug(ctx, "user", "usr_bob", "default")
		if err != nil {
			t.Fatal(err)
		}
		o, err := st.Calendars().GetObjectByUID(ctx, def.ID, "dave-s")
		if err != nil {
			t.Fatalf("bob's copy: %v", err)
		}
		return o
	}
	in := func(id, ics string) string {
		t.Helper()
		w := signedPost(t, srv, "/api/itip/inbound", kypost.EventInbound, id, kypost.InboundRequest{User: bob, From: "dave@example.org", DKIMAligned: true, ICS: ics})
		return w.Body.String()
	}
	if got := in("s1", msg("REQUEST", "2", "20261112T100000Z")); !strings.Contains(got, "applied") {
		t.Fatalf("the update: %s", got)
	}
	for id, late := range map[string]string{"s2": msg("REQUEST", "1", "20261112T080000Z"), "s3": msg("CANCEL", "1", "20261112T080000Z")} {
		if got := in(id, late); strings.Contains(got, "applied") {
			t.Errorf("a late message (%s) was applied: %s", id, got)
		}
	}
	if data := string(copyOf().Data); !strings.Contains(data, "DTSTART:20261112T100000Z") || strings.Contains(data, "CANCELLED") {
		t.Fatalf("bob's copy after late messages:\n%s", data)
	}
	// Bob's calendar app raises his copy's SEQUENCE; the organizer's SEQUENCE 3 still applies.
	o := copyOf()
	o.Data = []byte(strings.Replace(string(o.Data), "SEQUENCE:2", "SEQUENCE:9", 1))
	if _, err := st.Calendars().PutObject(ctx, o, o.ETag, false, store.OwnerLimits{}); err != nil {
		t.Fatal(err)
	}
	if got := in("s4", msg("REQUEST", "3", "20261112T110000Z")); !strings.Contains(got, "applied") {
		t.Fatalf("the organizer's update after bob's edit: %s", got)
	}
}

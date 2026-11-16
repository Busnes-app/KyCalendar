package api_test

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/kypost"
	"github.com/Busnes-app/kycalendar/internal/store"
)

const oooPath = "/api/kypost/out-of-office"

type oooWorld struct {
	srv    *api.Server
	st     store.Store
	fake   *fakeKyPost
	tokens map[string]string
	n      int
}

func newOOOWorld(t *testing.T) *oooWorld {
	t.Helper()
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
	t.Cleanup(kp.Close)
	api.SetKyPostClientForTest(srv, &kypost.Client{BaseURL: kp.URL, Secret: kypostSecret, HTTP: kp.Client()})
	return &oooWorld{srv: srv, st: st, fake: fake, tokens: map[string]string{"alice": ssoUser(t, st, "alice"), "bob": ssoUser(t, st, "bob")}}
}

func (w *oooWorld) set(t *testing.T, in kypost.OutOfOfficeRequest) *httptest.ResponseRecorder {
	t.Helper()
	w.n++
	if in.Sub == "" {
		in.User = kypost.User{Sub: "sub-bob", Issuer: "https://id.example"}
	}
	return signedPost(t, w.srv, oooPath, kypost.EventOOO, "ooo-"+strconv.Itoa(w.n), in)
}

// event is bob's out-of-office object, or nil.
func (w *oooWorld) event(t *testing.T) *ical.Calendar {
	t.Helper()
	ctx := context.Background()
	cals, err := w.st.Calendars().ListCalendarsByOwner(ctx, "user", "usr_bob")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cals {
		if o, err := w.st.Calendars().GetObjectByUID(ctx, c.ID, davbackend.OutOfOfficeUID("usr_bob")); err == nil {
			cal, err := ical.NewDecoder(bytes.NewReader(o.Data)).Decode()
			if err != nil {
				t.Fatal(err)
			}
			return cal
		}
	}
	return nil
}

// KyPost's period becomes one busy, out-of-office event; new dates move it, an older revision
// is ignored, and turning it off removes it.
func TestKyPostOutOfOfficeEvent(t *testing.T) {
	w := newOOOWorld(t)
	period := kypost.OutOfOfficeRequest{Revision: 1, Away: true, Start: "2026-12-01T05:00:00Z", End: "2026-12-05T05:00:00Z", AllDay: true, TimeZone: "America/New_York"}
	if r := w.set(t, period); r.Code != 200 || !strings.Contains(r.Body.String(), "applied") {
		t.Fatalf("set: %d %s", r.Code, r.Body.String())
	}
	cal := w.event(t)
	if cal == nil {
		t.Fatal("no event")
	}
	ev := cal.Children[0]
	if ev.Props.Get(ical.PropTransparency).Value != "OPAQUE" || ev.Props.Get("X-MICROSOFT-CDO-BUSYSTATUS").Value != "OOF" ||
		ev.Props.Get(ical.PropDateTimeStart).Value != "20261201" || ev.Props.Get(ical.PropDateTimeEnd).Value != "20261205" {
		t.Fatalf("event %+v", ev.Props)
	}
	busy, err := calendar.BusyTimes(cal, time.Date(2026, 12, 2, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 3, 0, 0, 0, 0, time.UTC), time.UTC, 10)
	if err != nil || len(busy) != 1 || busy[0].Tentative {
		t.Fatalf("free/busy %+v %v", busy, err)
	}

	moved := kypost.OutOfOfficeRequest{Revision: 3, Away: true, Start: "2026-12-07T09:00:00Z", End: "2026-12-07T17:00:00Z"}
	if r := w.set(t, moved); r.Code != 200 {
		t.Fatalf("move: %d %s", r.Code, r.Body.String())
	}
	if got := w.event(t).Children[0].Props.Get(ical.PropDateTimeStart).Value; got != "20261207T090000Z" {
		t.Fatalf("moved start %s", got)
	}
	if r := w.set(t, period); !strings.Contains(r.Body.String(), "stale") || w.event(t).Children[0].Props.Get(ical.PropDateTimeStart).Value != "20261207T090000Z" {
		t.Fatalf("an older revision changed the event: %s", r.Body.String())
	}
	if r := w.set(t, kypost.OutOfOfficeRequest{Revision: 4}); r.Code != 200 || w.event(t) != nil {
		t.Fatalf("off: %d %s, event left: %v", r.Code, r.Body.String(), w.event(t) != nil)
	}
	// Off again, with nothing to remove, is fine.
	if r := w.set(t, kypost.OutOfOfficeRequest{Revision: 5}); r.Code != 200 {
		t.Fatalf("off twice: %d", r.Code)
	}
}

// While the period runs, a new invitation from outside is answered TENTATIVE with the remote
// text, and one from a KyCalendar user with the local text. Nothing is ever declined.
func TestKyPostOutOfOfficeAnswersTentative(t *testing.T) {
	w := newOOOWorld(t)
	now := time.Now().UTC()
	on := kypost.OutOfOfficeRequest{Revision: 1, Away: true, Start: now.Add(-time.Hour).Format(time.RFC3339), End: now.Add(48 * time.Hour).Format(time.RFC3339),
		Tentative: true, CommentLocal: "Away; ask Sam.", CommentRemote: "I am away."}
	if r := w.set(t, on); r.Code != 200 {
		t.Fatalf("set: %d %s", r.Code, r.Body.String())
	}
	invite := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nMETHOD:REQUEST\r\nBEGIN:VEVENT\r\nUID:dave-1\r\nDTSTAMP:20261101T000000Z\r\n" +
		"DTSTART:20261212T090000Z\r\nDTEND:20261212T100000Z\r\nSUMMARY:Supplier call\r\nORGANIZER:mailto:dave@example.org\r\n" +
		"ATTENDEE;RSVP=TRUE:mailto:bob@example.com\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	bob := kypost.User{Sub: "sub-bob", Issuer: "https://id.example"}
	if r := signedPost(t, w.srv, "/api/itip/inbound", kypost.EventInbound, "in-1", kypost.InboundRequest{User: bob, From: "dave@example.org", DKIMAligned: true, ICS: invite}); !strings.Contains(r.Body.String(), "applied") {
		t.Fatalf("inbound: %d %s", r.Code, r.Body.String())
	}
	w.srv.DeliverScheduled(context.Background())
	sent := w.fake.take()
	if len(sent) != 1 || sent[0].Method != "REPLY" || !strings.Contains(sent[0].ICS, "PARTSTAT=TENTATIVE:mailto:bob@example.com") ||
		!strings.Contains(sent[0].ICS, "COMMENT:I am away.") {
		t.Fatalf("reply %+v", sent)
	}
	// An update of the same invitation is not answered again.
	update := strings.Replace(invite, "SUMMARY:Supplier call", "SUMMARY:Supplier call v2\r\nSEQUENCE:1", 1)
	signedPost(t, w.srv, "/api/itip/inbound", kypost.EventInbound, "in-2", kypost.InboundRequest{User: bob, From: "dave@example.org", DKIMAligned: true, ICS: update})
	w.srv.DeliverScheduled(context.Background())
	if sent := w.fake.take(); len(sent) != 0 {
		t.Fatalf("an update was answered again: %+v", sent)
	}

	// alice (a KyCalendar user) invites bob: his copy is TENTATIVE with the local text.
	ts := httptest.NewServer(w.srv)
	defer ts.Close()
	if r := rawDAV(t, ts, "PUT", "/dav/usr_alice/calendars/default/plan.ics", "alice", w.tokens["alice"], meeting("plan", "alice", "ATTENDEE;RSVP=TRUE:mailto:bob@example.com"),
		map[string]string{"Content-Type": "text/calendar"}); r.StatusCode != 201 {
		t.Fatalf("alice's PUT %d", r.StatusCode)
	}
	w.srv.DeliverScheduled(context.Background())
	w.srv.DeliverScheduled(context.Background())
	got := readAll(rawDAV(t, ts, "GET", "/dav/usr_alice/calendars/default/plan.ics", "alice", w.tokens["alice"], "", nil))
	if !strings.Contains(got, "ATTENDEE;PARTSTAT=TENTATIVE;RSVP=TRUE:mailto:bob@example.com") {
		t.Fatalf("alice's event after bob's automatic answer:\n%s", got)
	}

	// Off: the next invitation waits for bob again.
	if r := w.set(t, kypost.OutOfOfficeRequest{Revision: 2}); r.Code != 200 {
		t.Fatal(r.Code)
	}
	next := strings.ReplaceAll(invite, "dave-1", "dave-2")
	if r := signedPost(t, w.srv, "/api/itip/inbound", kypost.EventInbound, "in-3", kypost.InboundRequest{User: bob, From: "dave@example.org", DKIMAligned: true, ICS: next}); !strings.Contains(r.Body.String(), "pending") {
		t.Fatalf("after off: %s", r.Body.String())
	}
}

func TestKyPostOutOfOfficeRefusals(t *testing.T) {
	w := newOOOWorld(t)
	ok := kypost.OutOfOfficeRequest{User: kypost.User{Sub: "sub-bob", Issuer: "https://id.example"}, Revision: 1, Away: true, Start: "2026-12-01T00:00:00Z", End: "2026-12-02T00:00:00Z"}
	// Wrong event type, a bad signature, a replay.
	if r := signedPost(t, w.srv, oooPath, kypost.EventInbound, "x-1", ok); r.Code != 401 {
		t.Errorf("wrong event type: %d", r.Code)
	}
	unsigned := httptest.NewRecorder()
	w.srv.ServeHTTP(unsigned, httptest.NewRequest("POST", oooPath, strings.NewReader(`{}`)))
	if unsigned.Code != 401 {
		t.Errorf("unsigned: %d", unsigned.Code)
	}
	if r := signedPost(t, w.srv, oooPath, kypost.EventOOO, "x-2", ok); r.Code != 200 {
		t.Fatalf("first: %d", r.Code)
	}
	if r := signedPost(t, w.srv, oooPath, kypost.EventOOO, "x-2", ok); !strings.Contains(r.Body.String(), "duplicate") {
		t.Errorf("replay: %s", r.Body.String())
	}
	// Unknown user, wrong issuer, administrator identity.
	if err := w.st.Users().CreateUser(context.Background(), &store.User{ID: "usr_root", Username: "root", Email: "root@example.com", Role: "admin", Status: "active",
		SSOProvider: "kysignon", SSOSubject: "sub-root", SSOIssuer: "kyidentity https://id.example"}); err != nil {
		t.Fatal(err)
	}
	for name, who := range map[string]kypost.User{
		"unknown": {Sub: "sub-nobody", Issuer: "https://id.example"},
		"issuer":  {Sub: "sub-bob", Issuer: "https://other.example"},
		"admin":   {Sub: "sub-root", Issuer: "https://id.example"},
	} {
		in := ok
		in.User = who
		if r := w.set(t, in); r.Code != 404 || !strings.Contains(r.Body.String(), "no_user") {
			t.Errorf("%s: %d %s", name, r.Code, r.Body.String())
		}
	}
	// Bad bodies.
	long := strings.Repeat("é", 4001)
	for name, in := range map[string]kypost.OutOfOfficeRequest{
		"no revision":      {Away: true, Start: ok.Start, End: ok.End},
		"end before start": {Revision: 9, Away: true, Start: ok.End, End: ok.Start},
		"no dates":         {Revision: 9, Away: true},
		"over a year":      {Revision: 9, Away: true, Start: "2026-01-01T00:00:00Z", End: "2027-06-01T00:00:00Z"},
		"all day, no zone": {Revision: 9, Away: true, Start: ok.Start, End: ok.End, AllDay: true},
		"all day, noon":    {Revision: 9, Away: true, Start: "2026-12-01T12:00:00Z", End: ok.End, AllDay: true, TimeZone: "UTC"},
		"long comment":     {Revision: 9, Away: true, Start: ok.Start, End: ok.End, CommentLocal: long},
		"control":          {Revision: 9, Away: true, Start: ok.Start, End: ok.End, CommentRemote: "a\x07b"},
	} {
		if r := w.set(t, in); r.Code != 400 || !strings.Contains(r.Body.String(), "bad_body") {
			t.Errorf("%s: %d %s", name, r.Code, r.Body.String())
		}
	}
	// Rate limit: the route has its own per-user budget.
	limited := false
	for i := range 70 {
		in := ok
		in.Revision = int64(100 + i)
		if r := w.set(t, in); r.Code == 429 {
			limited = true
			break
		}
	}
	if !limited {
		t.Error("never rate limited")
	}
}

// Without a linked KyPost the route does not exist.
func TestKyPostOutOfOfficeUnpaired(t *testing.T) {
	srv, _, _ := setupTestServer(t)
	in := kypost.OutOfOfficeRequest{User: kypost.User{Sub: "sub-bob", Issuer: "https://id.example"}, Revision: 1}
	if r := signedPost(t, srv, oooPath, kypost.EventOOO, "unpaired-1", in); r.Code != 404 {
		t.Fatalf("unpaired: %d %s", r.Code, r.Body.String())
	}
}

// An object carrying the out-of-office UID in a calendar shared to bob is never adopted: the
// period goes to bob's own calendar and the shared object stays as it was.
func TestKyPostOutOfOfficeIgnoresSharedCalendars(t *testing.T) {
	w := newOOOWorld(t)
	ctx := context.Background()
	team := groupCalendar(t, w.st, "Team")
	grantRole(t, w.st, team, "editor", "usr_bob")
	uid := davbackend.OutOfOfficeUID("usr_bob")
	planted := []byte(eventICS(uid))
	o := &store.CalendarObject{CalendarID: team.ID, Name: "planted.ics", Component: "VEVENT", UID: uid, Data: planted}
	if _, err := w.st.Calendars().PutObject(ctx, o, "", false, store.OwnerLimits{}); err != nil {
		t.Fatal(err)
	}
	period := kypost.OutOfOfficeRequest{Revision: 1, Away: true, Start: "2026-12-01T09:00:00Z", End: "2026-12-01T17:00:00Z"}
	if r := w.set(t, period); r.Code != 200 {
		t.Fatalf("set: %d %s", r.Code, r.Body.String())
	}
	if w.event(t) == nil {
		t.Fatal("no event in bob's own calendar")
	}
	got, err := w.st.Calendars().GetObjectByUID(ctx, team.ID, uid)
	if err != nil || !bytes.Equal(got.Data, planted) {
		t.Fatalf("the shared object changed: %v", err)
	}
	if r := w.set(t, kypost.OutOfOfficeRequest{Revision: 2}); r.Code != 200 || w.event(t) != nil {
		t.Fatalf("off: %d", r.Code)
	}
	if _, err := w.st.Calendars().GetObjectByUID(ctx, team.ID, uid); err != nil {
		t.Fatalf("turning off removed the shared object: %v", err)
	}
}

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Busnes-app/ky-primitives/suitedir"
	"github.com/Busnes-app/ky-primitives/syncauth"
	"github.com/emersion/go-ical"

	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/kymessages"
	"github.com/Busnes-app/kycalendar/internal/store"
)

var meetSecret = []byte("kymessages-pair-secret-0123456789abcdef")

// meetCall is one request the fake KyMessages saw, signature already checked.
type meetCall struct {
	Method, ID string
	Body       kymessages.PutRequest
}

// fakeKyMessages is the KyMessages console: it checks every signature and event type and
// answers with reply (default: a room link per meeting ID).
type fakeKyMessages struct {
	*httptest.Server
	mu    sync.Mutex
	calls []meetCall
	reply func(method, id string) (int, string)
}

func (f *fakeKyMessages) seen() []meetCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]meetCall(nil), f.calls...)
}

func (f *fakeKyMessages) answer(fn func(method, id string) (int, string)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reply = fn
}

func roomURL(id string) string { return "https://chat.example/#/room/!" + id + ":example" }

type meetRig struct {
	s     *Server
	km    *fakeKyMessages
	org   *store.User
	cal   *store.Calendar
	clock time.Time
}

func newMeetRig(t *testing.T) *meetRig {
	t.Helper()
	s, _ := davInternalServer(t)
	p := newSuiteIdP(t).provider()
	s.signin.Store(p)
	km := &fakeKyMessages{}
	km.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		h := syncauth.FromRequest(r)
		want := map[string]string{http.MethodPut: kymessages.EventPut, http.MethodDelete: kymessages.EventClose}[r.Method]
		if _, err := syncauth.Verify(meetSecret, h, b, syncauth.Options{}); err != nil || h.EventType != want {
			http.Error(w, `{"error":"x","code":"bad_signature"}`, http.StatusUnauthorized)
			return
		}
		c := meetCall{Method: r.Method, ID: strings.TrimPrefix(r.URL.Path, kymessages.MeetPath)}
		_ = json.Unmarshal(b, &c.Body)
		km.mu.Lock()
		km.calls = append(km.calls, c)
		reply := km.reply
		km.mu.Unlock()
		status, body := 200, `{"outcome":"closed"}`
		if r.Method == http.MethodPut {
			body = fmt.Sprintf(`{"member_url":%q,"skipped_subs":[]}`, roomURL(c.ID))
		}
		if reply != nil {
			status, body = reply(r.Method, c.ID)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(km.Close)
	s.kymessages.HTTP = km.Client()
	s.kymessages.Set(&kymessages.Peer{URL: km.URL, Secret: meetSecret, Source: kymessages.SourceEnvironment})

	ctx := context.Background()
	org := ssoUser("usr_org", "sub-org", p)
	org.Email = "org@example.com"
	if err := s.store.Users().CreateUser(ctx, org); err != nil {
		t.Fatal(err)
	}
	if err := davbackend.EnsureDefault(ctx, s.store, org.ID, 0); err != nil {
		t.Fatal(err)
	}
	cals, err := s.store.Calendars().ListCalendarsByOwner(ctx, "user", org.ID)
	if err != nil || len(cals) != 1 {
		t.Fatalf("calendars %v %v", cals, err)
	}
	rig := &meetRig{s: s, km: km, org: org, cal: cals[0], clock: time.Now()}
	old := meetingNow
	meetingNow = func() time.Time { return rig.clock }
	oldResolve := resolvePeople
	resolvePeople = func(_ context.Context, _ *suitedir.Client, sub string, emails []string) ([]string, error) {
		if sub != "sub-org" {
			t.Errorf("resolved as %q", sub)
		}
		var subs []string
		for _, e := range emails {
			if name, ok := strings.CutSuffix(e, "@example.com"); ok {
				subs = append(subs, "sub-"+name)
			}
		}
		return subs, nil
	}
	t.Cleanup(func() { meetingNow, resolvePeople = old, oldResolve })
	return rig
}

// sync runs the worker once, later than any backoff so far.
func (r *meetRig) sync() {
	r.clock = r.clock.Add(2 * time.Hour)
	r.s.SyncMeetings(context.Background())
}

func (r *meetRig) do(t *testing.T, u *store.User, method, path string, body any, h http.HandlerFunc, values map[string]string, etag string) *httptest.ResponseRecorder {
	t.Helper()
	var b bytes.Buffer
	_ = json.NewEncoder(&b).Encode(body)
	req := httptest.NewRequest(method, path, &b)
	for k, v := range values {
		req.SetPathValue(k, v)
	}
	if etag != "" {
		req.Header.Set("If-Match", `"`+etag+`"`)
	}
	w := httptest.NewRecorder()
	h(w, withSessionUser(req, u, nil))
	return w
}

func (r *meetRig) create(t *testing.T, body map[string]any) string {
	t.Helper()
	start := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	ev := map[string]any{"title": "Weekly sync", "start": start.Format(time.RFC3339), "end": start.Add(time.Hour).Format(time.RFC3339), "zone": "UTC"}
	for k, v := range body {
		ev[k] = v
	}
	w := r.do(t, r.org, http.MethodPost, "/api/calendars/x/events", ev, r.s.handleCreateEvent, map[string]string{"id": r.cal.ID}, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var out struct{ UID string }
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out.UID
}

func (r *meetRig) update(t *testing.T, uid string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	o := r.object(t, uid)
	v := r.view(t, uid)
	ev := map[string]any{"title": v.Title, "description": v.Description, "start": v.SeriesStart, "end": v.SeriesEnd, "zone": "UTC", "scope": "all", "repeat": v.Repeat}
	for k, val := range body {
		ev[k] = val
	}
	return r.do(t, r.org, http.MethodPut, "/api/events/x/y", ev, r.s.handleUpdateEvent, map[string]string{"cal": r.cal.ID, "uid": uid}, o.ETag)
}

func (r *meetRig) object(t *testing.T, uid string) *store.CalendarObject {
	t.Helper()
	o, err := r.s.store.Calendars().GetObjectByUID(context.Background(), r.cal.ID, uid)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func (r *meetRig) view(t *testing.T, uid string) eventView {
	t.Helper()
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	out, apiErr := r.s.expandEvents(context.Background(), r.org, []*store.Calendar{r.cal}, nil, from, from.AddDate(0, 2, 0), time.UTC)
	if apiErr != nil {
		t.Fatal(apiErr.msg)
	}
	for _, v := range out {
		if v.UID == uid {
			return v
		}
	}
	t.Fatalf("no event %s", uid)
	return eventView{}
}

func methods(calls []meetCall) string {
	var out []string
	for _, c := range calls {
		out = append(out, c.Method)
	}
	return strings.Join(out, " ")
}

func TestMeetingIsCreatedThenLinkedIntoTheEvent(t *testing.T) {
	r := newMeetRig(t)
	uid := r.create(t, map[string]any{"meeting": true, "description": "Agenda"})
	if v := r.view(t, uid); !v.Meeting || v.MeetingURL != "" {
		t.Fatalf("before sync, want pending: %+v", v)
	}
	r.sync()
	calls := r.km.seen()
	if methods(calls) != "PUT" || !kymessages.ValidID(calls[0].ID) || strings.Contains(calls[0].ID, uid) {
		t.Fatalf("calls %+v", calls)
	}
	b := calls[0].Body
	if b.Sub != "sub-org" || b.Issuer != r.org.SSOIssuer || b.Title != "Weekly sync" || len(b.MemberSubs) != 0 {
		t.Fatalf("body %+v", b)
	}
	data := string(r.object(t, uid).Data)
	if !strings.Contains(data, "CONFERENCE;FEATURE=VIDEO;LABEL=KyMessages;VALUE=URI:"+roomURL(calls[0].ID)) || !strings.Contains(data, `Join: `+roomURL(calls[0].ID)) {
		t.Fatalf("stored event lacks the room:\n%s", data)
	}
	v := r.view(t, uid)
	if !v.Meeting || v.MeetingURL != roomURL(calls[0].ID) || v.Description != "Agenda" {
		t.Fatalf("view %+v", v)
	}
	// Writing the link changed neither title nor members: nothing more is sent.
	r.sync()
	if n := len(r.km.seen()); n != 1 {
		t.Fatalf("%d calls after a quiet sync", n)
	}
}

func TestMeetingSurvivesKyMessagesDownAndRetries(t *testing.T) {
	r := newMeetRig(t)
	r.km.answer(func(string, string) (int, string) { return 503, `{"code":"unavailable"}` })
	uid := r.create(t, map[string]any{"meeting": true}) // saving does not wait on KyMessages
	r.sync()
	if v := r.view(t, uid); !v.Meeting || v.MeetingURL != "" {
		t.Fatalf("view %+v", v)
	}
	// Within the backoff nothing is retried.
	r.s.SyncMeetings(context.Background())
	if n := len(r.km.seen()); n != 1 {
		t.Fatalf("%d calls inside the backoff", n)
	}
	r.km.answer(nil)
	r.sync()
	if calls := r.km.seen(); methods(calls) != "PUT PUT" || calls[0].ID != calls[1].ID {
		t.Fatalf("calls %+v", calls)
	}
	if v := r.view(t, uid); v.MeetingURL == "" {
		t.Fatal("no link after the retry")
	}
}

// A PUT still owed when the event is deleted is never sent after the close: the meeting row is
// its own outbox, and its latest state wins.
func TestMeetingCloseFollowsPuts(t *testing.T) {
	r := newMeetRig(t)
	uid := r.create(t, map[string]any{"meeting": true})
	r.sync()
	r.update(t, uid, map[string]any{"title": "Renamed"})
	r.sync()
	if calls := r.km.seen(); methods(calls) != "PUT PUT" || calls[1].Body.Title != "Renamed" {
		t.Fatalf("calls %+v", calls)
	}
	// KyMessages down: the next PUT fails, then the event goes; only a DELETE follows.
	r.km.answer(func(string, string) (int, string) { return 503, `{}` })
	r.update(t, uid, map[string]any{"title": "Again"})
	r.sync()
	o := r.object(t, uid)
	if w := r.do(t, r.org, http.MethodDelete, "/api/events/x/y", nil, r.s.handleDeleteEvent, map[string]string{"cal": r.cal.ID, "uid": uid}, o.ETag); w.Code != http.StatusNoContent {
		t.Fatalf("delete %d %s", w.Code, w.Body)
	}
	r.km.answer(nil)
	r.sync()
	r.sync()
	calls := r.km.seen()
	if methods(calls) != "PUT PUT PUT DELETE" || calls[3].ID != calls[0].ID || calls[3].Body.Sub != "sub-org" {
		t.Fatalf("calls %s %+v", methods(calls), calls)
	}
}

func TestMeetingSwitchOffClosesAndRemovesTheLink(t *testing.T) {
	r := newMeetRig(t)
	uid := r.create(t, map[string]any{"meeting": true})
	r.sync()
	if w := r.update(t, uid, map[string]any{"meeting": false}); w.Code != http.StatusOK {
		t.Fatalf("switch off %d %s", w.Code, w.Body)
	}
	if data := string(r.object(t, uid).Data); strings.Contains(data, "CONFERENCE") || strings.Contains(data, "Join:") {
		t.Fatalf("link kept:\n%s", data)
	}
	r.sync()
	calls := r.km.seen()
	if methods(calls) != "PUT DELETE" || calls[0].ID != calls[1].ID {
		t.Fatalf("calls %+v", calls)
	}
	if v := r.view(t, uid); v.Meeting || v.MeetingURL != "" {
		t.Fatalf("view %+v", v)
	}
	// On again: a new room, never the closed one.
	r.update(t, uid, map[string]any{"meeting": true})
	r.sync()
	if calls := r.km.seen(); methods(calls) != "PUT DELETE PUT" || calls[2].ID == calls[0].ID {
		t.Fatalf("calls %+v", calls)
	}
}

func TestMeetingFollowsAttendeesAndCancellation(t *testing.T) {
	r := newMeetRig(t)
	r.s.startScheduling()
	uid := r.create(t, map[string]any{"meeting": true, "attendees": []string{"ann@example.com", "out@elsewhere.example"}})
	r.sync()
	calls := r.km.seen()
	if len(calls) != 1 || fmt.Sprint(calls[0].Body.MemberSubs) != "[sub-ann]" {
		t.Fatalf("calls %+v", calls)
	}
	r.update(t, uid, map[string]any{"attendees": []string{"ann@example.com", "bob@example.com"}})
	r.sync()
	if calls = r.km.seen(); len(calls) != 2 || fmt.Sprint(calls[1].Body.MemberSubs) != "[sub-ann sub-bob]" {
		t.Fatalf("calls %+v", calls)
	}
	// The invitation queued after the link was written carries the room (as KyPost mails it).
	due, err := r.s.store.Calendars().DueOutbox(context.Background(), time.Now().Add(time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	room := roomURL(calls[0].ID)
	found := false
	for _, m := range due {
		body := string(m.Body)
		if m.Method == "REQUEST" && strings.Contains(body, "CONFERENCE;FEATURE=VIDEO;LABEL=KyMessages;VALUE=URI:"+room) && strings.Contains(body, "Join: "+room) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no queued REQUEST carries the room (%d messages)", len(due))
	}
	// A cancelled event closes its room.
	o := r.object(t, uid)
	cal, _ := ical.NewDecoder(bytes.NewReader(o.Data)).Decode()
	master, _ := calendar.Master(cal)
	master.Props.SetText(ical.PropStatus, "CANCELLED")
	if _, apiErr := r.s.saveEvent(context.Background(), r.org, r.cal, o.Name, cal, o.ETag, false); apiErr != nil {
		t.Fatal(apiErr.msg)
	}
	r.sync()
	if calls = r.km.seen(); methods(calls) != "PUT PUT DELETE" {
		t.Fatalf("calls %s", methods(calls))
	}
}

func TestRecurringSeriesUsesOneRoom(t *testing.T) {
	r := newMeetRig(t)
	uid := r.create(t, map[string]any{"meeting": true, "repeat": map[string]any{"freq": "weekly"}})
	r.sync()
	v := r.view(t, uid)
	if w := r.update(t, uid, map[string]any{"scope": "this", "recurrence_id": "20261123T090000Z", "title": "Moved", "start": "2026-11-23T10:00:00Z", "end": "2026-11-23T11:00:00Z", "repeat": map[string]any{}}); w.Code != http.StatusOK {
		t.Fatalf("edit one: %d %s", w.Code, w.Body)
	}
	if w := r.update(t, uid, map[string]any{"scope": "this", "recurrence_id": "20261123T090000Z", "meeting": false}); w.Code != http.StatusBadRequest {
		t.Fatalf("switch on one occurrence: %d", w.Code)
	}
	r.sync()
	ids := map[string]bool{}
	for _, c := range r.km.seen() {
		ids[c.ID] = true
		if c.Method != http.MethodPut {
			t.Fatalf("calls %s", methods(r.km.seen()))
		}
	}
	if len(ids) != 1 {
		t.Fatalf("rooms %v", ids)
	}
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	all, _ := r.s.expandEvents(context.Background(), r.org, []*store.Calendar{r.cal}, nil, from, from.AddDate(0, 1, 0), time.UTC)
	for _, in := range all {
		if in.MeetingURL != v.MeetingURL || in.MeetingURL == "" {
			t.Fatalf("occurrence %s link %q, series %q", in.RecurrenceID, in.MeetingURL, v.MeetingURL)
		}
	}
}

func TestMeetingNeedsKyMessagesAndKyIdentity(t *testing.T) {
	r := newMeetRig(t)
	local := &store.User{ID: "usr_loc", Username: "loc", Role: "user", Status: "active", SSOProvider: "local", Email: "loc@example.com"}
	admin := *r.org
	admin.Role = "admin"
	if r.s.meetingsFor(local) || r.s.meetingsFor(&admin) || !r.s.meetingsFor(r.org) {
		t.Fatal("meetingsFor")
	}
	uid := r.create(t, map[string]any{"meeting": true})
	r.s.kymessages.Set(nil)
	start := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	ev := map[string]any{"title": "x", "start": start.Format(time.RFC3339), "end": start.Add(time.Hour).Format(time.RFC3339), "zone": "UTC", "meeting": true}
	w := r.do(t, r.org, http.MethodPost, "/", ev, r.s.handleCreateEvent, map[string]string{"id": r.cal.ID}, "")
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "meetings_off") {
		t.Fatalf("unpaired create: %d %s", w.Code, w.Body)
	}
	// Queued while paired, unpaired since: nothing leaves, it waits.
	r.sync()
	if len(r.km.seen()) != 0 {
		t.Fatal("sent while unpaired")
	}
	m, err := r.s.store.Calendars().OpenMeeting(context.Background(), r.cal.ID, uid+".ics")
	if err != nil || m.LastError != "not_connected" || m.DoneRev >= m.Rev {
		t.Fatalf("meeting %+v %v", m, err)
	}
	// Turning a meeting off never needs KyMessages.
	if w := r.update(t, uid, map[string]any{"meeting": false}); w.Code != http.StatusOK {
		t.Fatalf("switch off while unpaired: %d %s", w.Code, w.Body)
	}
}

// An invitee's copy carries the organizer's room: it shows Join and cannot drive the room.
func TestInviteeCopyCannotDriveTheRoom(t *testing.T) {
	r := newMeetRig(t)
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, "-//test//EN")
	ev := ical.NewComponent(ical.CompEvent)
	ev.Props.SetText(ical.PropUID, "invite-1")
	ev.Props.SetDateTime(ical.PropDateTimeStamp, time.Now().UTC())
	ev.Props.SetDateTime(ical.PropDateTimeStart, time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC))
	ev.Props.SetDateTime(ical.PropDateTimeEnd, time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC))
	ev.Props.SetText(ical.PropSummary, "Their meeting")
	boss := &store.User{ID: "usr_boss", Username: "boss", Role: "user", Status: "active", SSOProvider: "local", Email: "boss@example.com"}
	if err := r.s.store.Users().CreateUser(context.Background(), boss); err != nil {
		t.Fatal(err)
	}
	org := ical.NewProp(ical.PropOrganizer)
	org.Value = "mailto:boss@example.com"
	ev.Props.Add(org)
	att := ical.NewProp(ical.PropAttendee)
	att.Value = "mailto:org@example.com"
	ev.Props.Add(att)
	cal.Children = append(cal.Children, ev)
	calendar.SetConference(cal, "https://chat.example/#/room/!theirs:example")
	var buf bytes.Buffer
	_ = ical.NewEncoder(&buf).Encode(cal)
	if _, err := davbackend.Write(context.Background(), r.s.store, r.cal, "invite-1.ics", cal, buf.Bytes(), "", true, store.OwnerLimits{}, nil, r.org.ID); err != nil {
		t.Fatal(err)
	}
	v := r.view(t, "invite-1")
	if v.Meeting || v.MeetingURL != "https://chat.example/#/room/!theirs:example" || strings.Contains(v.Description, "Join:") {
		t.Fatalf("invitee view %+v", v)
	}
	for _, on := range []bool{true, false} {
		w := r.update(t, "invite-1", map[string]any{"meeting": on})
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "not_organizer") {
			t.Fatalf("meeting=%v: %d %s", on, w.Code, w.Body)
		}
	}
	r.sync()
	if len(r.km.seen()) != 0 {
		t.Fatal("an invitee copy reached KyMessages")
	}
	// Members only: an outside organizer's "KyMessages" link gets no Join button, and the
	// description keeps it as text.
	o := r.object(t, "invite-1")
	outside := strings.ReplaceAll(string(o.Data), "boss@example.com", "boss@elsewhere.example")
	outCal, _ := ical.NewDecoder(strings.NewReader(outside)).Decode()
	if _, err := davbackend.Write(context.Background(), r.s.store, r.cal, "invite-1.ics", outCal, []byte(outside), o.ETag, false, store.OwnerLimits{}, nil, r.org.ID); err != nil {
		t.Fatal(err)
	}
	if v := r.view(t, "invite-1"); v.MeetingURL != "" || !strings.Contains(v.Description, "Join: https://chat.example/") {
		t.Fatalf("outside organizer view %+v", v)
	}
}

// A room whose event changes organizer (a CalDAV client rewrote it) is closed, not driven.
func TestMeetingClosedWhenOrganizerChanges(t *testing.T) {
	r := newMeetRig(t)
	uid := r.create(t, map[string]any{"meeting": true})
	r.sync()
	o := r.object(t, uid)
	cal, _ := ical.NewDecoder(bytes.NewReader(o.Data)).Decode()
	master, _ := calendar.Master(cal)
	org := ical.NewProp(ical.PropOrganizer)
	org.Value = "mailto:someone@elsewhere.example"
	master.Props.Set(org)
	var buf bytes.Buffer
	_ = ical.NewEncoder(&buf).Encode(cal)
	if _, err := davbackend.Write(context.Background(), r.s.store, r.cal, o.Name, cal, buf.Bytes(), o.ETag, false, store.OwnerLimits{}, nil, r.org.ID); err != nil {
		t.Fatal(err)
	}
	r.sync()
	if calls := r.km.seen(); methods(calls) != "PUT DELETE" {
		t.Fatalf("calls %s", methods(calls))
	}
}

func TestMeetingRefusals(t *testing.T) {
	r := newMeetRig(t)
	r.km.answer(func(string, string) (int, string) { return 404, `{"error":"x","code":"no_user"}` })
	uid := r.create(t, map[string]any{"meeting": true})
	r.sync()
	r.sync()
	if n := len(r.km.seen()); n != 1 {
		t.Fatalf("a refusal was retried: %d calls", n)
	}
	if v := r.view(t, uid); !v.Meeting || v.MeetingError != "no_user" {
		t.Fatalf("view %+v", v)
	}
	// The next edit tries again; KyMessages closed the room meanwhile: the meeting is over.
	r.km.answer(func(string, string) (int, string) { return 409, `{"error":"x","code":"closed"}` })
	r.update(t, uid, map[string]any{"title": "Retry"})
	r.sync()
	if v := r.view(t, uid); v.Meeting || v.MeetingURL != "" {
		t.Fatalf("closed room still on: %+v", v)
	}
	if strings.Contains(string(r.object(t, uid).Data), "CONFERENCE") {
		t.Fatal("a closed room's link stayed in the event")
	}
	// A wrong secret on our side is refused as a bad signature and never acted on.
	r.s.kymessages.Set(&kymessages.Peer{URL: r.km.URL, Secret: []byte("another-secret-0123456789abcdefghijkl"), Source: kymessages.SourceEnvironment})
	r.km.answer(nil)
	uid2 := r.create(t, map[string]any{"meeting": true})
	r.sync()
	if v := r.view(t, uid2); v.MeetingError != "bad_signature" || v.MeetingURL != "" {
		t.Fatalf("view %+v", v)
	}
	var audits []string
	recs, _, _ := r.s.store.Audit().ListAuditRecords(context.Background(), 0, 100)
	for _, a := range recs {
		if strings.HasPrefix(a.Action, "meeting.") {
			audits = append(audits, a.Action+" "+a.Details)
			if strings.Contains(a.Details, "Retry") || strings.Contains(a.Details, "example") {
				t.Fatalf("audit leaks content: %+v", a)
			}
		}
	}
	if len(audits) != 3 {
		t.Fatalf("audits %v", audits)
	}
}

func TestDeletingACalendarClosesItsRooms(t *testing.T) {
	r := newMeetRig(t)
	r.create(t, map[string]any{"meeting": true})
	r.sync()
	if err := r.s.store.Calendars().DeleteCalendar(context.Background(), r.cal.ID); err != nil {
		t.Fatal(err)
	}
	r.sync()
	if calls := r.km.seen(); methods(calls) != "PUT DELETE" {
		t.Fatalf("calls %s", methods(calls))
	}
}

func TestRoomClosedByKyMessagesLeavesNoDeadLink(t *testing.T) {
	r := newMeetRig(t)
	uid := r.create(t, map[string]any{"meeting": true})
	r.sync()
	if !strings.Contains(string(r.object(t, uid).Data), "CONFERENCE") {
		t.Fatal("no link written")
	}
	r.km.answer(func(string, string) (int, string) { return 409, `{"error":"x","code":"closed"}` })
	r.update(t, uid, map[string]any{"title": "Renamed"})
	r.sync()
	data := string(r.object(t, uid).Data)
	if strings.Contains(data, "CONFERENCE") || strings.Contains(data, "Join:") {
		t.Fatalf("dead link kept:\n%s", data)
	}
	if v := r.view(t, uid); v.Meeting || v.MeetingURL != "" {
		t.Fatalf("view %+v", v)
	}
	r.sync()
	if calls := r.km.seen(); methods(calls) != "PUT PUT" {
		t.Fatalf("calls %s", methods(calls))
	}
}

package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/store"
)

type meetingJSON struct {
	UID       string `json:"uid"`
	ETag      string `json:"etag"`
	Organizer string `json:"organizer"`
	MyStatus  string `json:"my_status"`
	Attendees []struct {
		Email  string `json:"email"`
		Status string `json:"status"`
	} `json:"attendees"`
	CalendarID string `json:"calendar_id"`
}

// webUser signs name in and gives them an email; it returns their cookie and personal calendar.
func webUser(t *testing.T, srv *api.Server, st store.Store, name string) (*http.Cookie, string) {
	t.Helper()
	ctx := context.Background()
	cookie := loginAs(t, srv, st, name, "user")
	u, err := st.Users().GetUserByID(ctx, "usr_"+name)
	if err != nil {
		t.Fatal(err)
	}
	u.Email = name + "@example.com"
	if err := st.Users().UpdateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := davbackend.EnsureDefault(ctx, st, u.ID, 0); err != nil {
		t.Fatal(err)
	}
	cals, err := st.Calendars().ListCalendarsByOwner(ctx, "user", u.ID)
	if err != nil || len(cals) == 0 {
		t.Fatal(err)
	}
	return cookie, cals[0].ID
}

func meetingsOf(t *testing.T, srv *api.Server, cookie *http.Cookie) map[string]meetingJSON {
	t.Helper()
	w := call(t, srv, "GET", "/api/events?start=2026-11-01T00:00:00Z&end=2026-12-01T00:00:00Z&tz=UTC", "", cookie)
	var evs []meetingJSON
	if err := json.Unmarshal(w.Body.Bytes(), &evs); err != nil {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	out := map[string]meetingJSON{}
	for _, e := range evs {
		out[e.UID] = e
	}
	return out
}

const meetingBody = `{"title":"Planning","start":"2026-11-10T09:00:00Z","end":"2026-11-10T10:00:00Z","zone":"UTC","attendees":["bob@example.com"]}`

func TestWebInviteAndAnswer(t *testing.T) {
	t.Setenv("KYCALENDAR_SCHEDULING", "true")
	srv, st, _ := setupTestServer(t)
	alice, aliceCal := webUser(t, srv, st, "alice")
	bob, _ := webUser(t, srv, st, "bob")

	w := call(t, srv, "POST", "/api/calendars/"+aliceCal+"/events", meetingBody, alice)
	if w.Code != http.StatusCreated {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	var made struct{ UID string }
	_ = json.Unmarshal(w.Body.Bytes(), &made)
	srv.DeliverScheduled(context.Background())

	got, ok := meetingsOf(t, srv, bob)[made.UID]
	if !ok || got.Organizer != "alice@example.com" || got.MyStatus != "NEEDS-ACTION" {
		t.Fatalf("bob's view %+v", got)
	}
	copyBefore, err := st.Calendars().GetObjectByUID(context.Background(), got.CalendarID, made.UID)
	if err != nil || copyBefore.InvitationSender != "usr_alice" {
		t.Fatalf("unanswered copy not charged to sender: %+v %v", copyBefore, err)
	}
	if r := withIfMatch(t, srv, "POST", "/api/events/"+got.CalendarID+"/"+made.UID+"/rsvp", `{"partstat":"MAYBE"}`, got.ETag, bob); r.Code != http.StatusBadRequest {
		t.Errorf("bad answer: %d", r.Code)
	}
	if r := withIfMatch(t, srv, "POST", "/api/events/"+got.CalendarID+"/"+made.UID+"/rsvp", `{"partstat":"ACCEPTED"}`, got.ETag, bob); r.Code != http.StatusOK {
		t.Fatalf("rsvp %d %s", r.Code, r.Body.String())
	}
	copyAfter, err := st.Calendars().GetObjectByUID(context.Background(), got.CalendarID, made.UID)
	if err != nil || copyAfter.InvitationSender != "" {
		t.Fatalf("answered copy still charged to sender: %+v %v", copyAfter, err)
	}
	srv.DeliverScheduled(context.Background())

	mine := meetingsOf(t, srv, alice)[made.UID]
	answered := false
	for _, a := range mine.Attendees {
		answered = answered || (a.Email == "bob@example.com" && a.Status == "ACCEPTED")
	}
	if !answered || mine.MyStatus != "" {
		t.Fatalf("alice's view after bob accepted %+v", mine)
	}

	// Bob cannot rewrite the guest list of alice's meeting on his copy.
	bobs := meetingsOf(t, srv, bob)[made.UID]
	body := `{"title":"Planning","start":"2026-11-10T09:00:00Z","end":"2026-11-10T10:00:00Z","zone":"UTC","attendees":["mallory@example.com"]}`
	if r := withIfMatch(t, srv, "PUT", "/api/events/"+bobs.CalendarID+"/"+made.UID, body, bobs.ETag, bob); r.Code != http.StatusForbidden {
		t.Errorf("bob changing attendees: %d %s", r.Code, r.Body.String())
	}
	// The organizer cannot answer her own meeting.
	if r := withIfMatch(t, srv, "POST", "/api/events/"+aliceCal+"/"+made.UID+"/rsvp", `{"partstat":"DECLINED"}`, mine.ETag, alice); r.Code != http.StatusUnprocessableEntity {
		t.Errorf("organizer answering: %d", r.Code)
	}
	bad := `{"title":"X","start":"2026-11-11T09:00:00Z","end":"2026-11-11T10:00:00Z","zone":"UTC","attendees":["not an email"]}`
	if r := call(t, srv, "POST", "/api/calendars/"+aliceCal+"/events", bad, alice); r.Code != http.StatusBadRequest {
		t.Errorf("bad email: %d", r.Code)
	}
}

func TestWebAttendeesNeedScheduling(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	alice, aliceCal := webUser(t, srv, st, "alice")
	if r := call(t, srv, "POST", "/api/calendars/"+aliceCal+"/events", meetingBody, alice); r.Code != http.StatusUnprocessableEntity {
		t.Errorf("attendees with scheduling off: %d %s", r.Code, r.Body.String())
	}
	plain := `{"title":"Solo","start":"2026-11-10T09:00:00Z","end":"2026-11-10T10:00:00Z","zone":"UTC"}`
	if r := call(t, srv, "POST", "/api/calendars/"+aliceCal+"/events", plain, alice); r.Code != http.StatusCreated {
		t.Errorf("a plain event with scheduling off: %d", r.Code)
	}
	var settings map[string]any
	_ = json.Unmarshal(call(t, srv, "GET", "/api/settings", "", nil).Body.Bytes(), &settings)
	if settings["scheduling"] != false {
		t.Errorf("settings scheduling = %v", settings["scheduling"])
	}
}

func TestWebFreeBusyPicker(t *testing.T) {
	t.Setenv("KYCALENDAR_SCHEDULING", "true")
	srv, st, _ := setupTestServer(t)
	alice, _ := webUser(t, srv, st, "alice")
	bob, bobCal := webUser(t, srv, st, "bob")
	secret := `{"title":"Secret interview","location":"Room 9","start":"2026-11-10T13:00:00Z","end":"2026-11-10T14:00:00Z","zone":"UTC"}`
	if r := call(t, srv, "POST", "/api/calendars/"+bobCal+"/events", secret, bob); r.Code != http.StatusCreated {
		t.Fatalf("bob's event %d", r.Code)
	}
	r := call(t, srv, "GET", "/api/freebusy?users=bob@example.com,carol@example.net&start=2026-11-10T00:00:00Z&end=2026-11-11T00:00:00Z", "", alice)
	body := r.Body.String()
	if r.Code != 200 || !strings.Contains(body, `"email":"bob@example.com","busy":[{"start":"2026-11-10T13:00:00Z","end":"2026-11-10T14:00:00Z"}]`) ||
		!strings.Contains(body, `"email":"carol@example.net","unknown":true`) {
		t.Fatalf("free/busy %d %s", r.Code, body)
	}
	for _, leak := range []string{"Secret", "Room 9"} {
		if strings.Contains(body, leak) {
			t.Errorf("free/busy reveals %q: %s", leak, body)
		}
	}
	for _, q := range []string{"users=bob@example.com&start=2026-11-10T00:00:00Z&end=2026-11-09T00:00:00Z",
		"users=bob@example.com&start=2026-01-01T00:00:00Z&end=2026-06-01T00:00:00Z", "start=2026-11-10T00:00:00Z&end=2026-11-11T00:00:00Z",
		"users=not-an-email&start=2026-11-10T00:00:00Z&end=2026-11-11T00:00:00Z"} {
		if r := call(t, srv, "GET", "/api/freebusy?"+q, "", alice); r.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", q, r.Code)
		}
	}
}

// Busy times list by start, a series' occurrences interleaved with other events.
func TestWebFreeBusyIsInOrder(t *testing.T) {
	t.Setenv("KYCALENDAR_SCHEDULING", "true")
	srv, st, _ := setupTestServer(t)
	alice, _ := webUser(t, srv, st, "alice")
	bob, bobCal := webUser(t, srv, st, "bob")
	for _, ev := range []string{
		`{"title":"Lunch","start":"2026-11-10T12:00:00Z","end":"2026-11-10T13:00:00Z","zone":"UTC"}`,
		`{"title":"Gym","start":"2026-11-10T08:00:00Z","end":"2026-11-10T09:00:00Z","zone":"UTC","repeat":{"freq":"daily"}}`,
	} {
		if r := call(t, srv, "POST", "/api/calendars/"+bobCal+"/events", ev, bob); r.Code != http.StatusCreated {
			t.Fatalf("bob's event %d %s", r.Code, r.Body)
		}
	}
	r := call(t, srv, "GET", "/api/freebusy?users=bob@example.com&start=2026-11-10T00:00:00Z&end=2026-11-12T00:00:00Z", "", alice)
	want := `"busy":[{"start":"2026-11-10T08:00:00Z","end":"2026-11-10T09:00:00Z"},{"start":"2026-11-10T12:00:00Z","end":"2026-11-10T13:00:00Z"},{"start":"2026-11-11T08:00:00Z","end":"2026-11-11T09:00:00Z"}]`
	if r.Code != 200 || !strings.Contains(r.Body.String(), want) {
		t.Errorf("free/busy %d %s, want %s", r.Code, r.Body, want)
	}
}

func TestWebFreeBusyNeedsScheduling(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	alice, _ := webUser(t, srv, st, "alice")
	if r := call(t, srv, "GET", "/api/freebusy?users=bob@example.com&start=2026-11-10T00:00:00Z&end=2026-11-11T00:00:00Z", "", alice); r.Code != http.StatusUnprocessableEntity {
		t.Errorf("scheduling off: %d", r.Code)
	}
}

// Bob answers one occurrence of alice's weekly meeting in Berlin time from the web: his answer is
// recorded on that occurrence in his calendar and in alice's, and every other occurrence keeps
// the series' answer.
func TestWebAnswerOneOccurrenceOfAZonedSeries(t *testing.T) {
	t.Setenv("KYCALENDAR_SCHEDULING", "true")
	srv, st, _ := setupTestServer(t)
	alice, aliceCal := webUser(t, srv, st, "alice")
	bob, _ := webUser(t, srv, st, "bob")
	body := `{"title":"Weekly","start":"2026-11-10T09:00:00+01:00","end":"2026-11-10T10:00:00+01:00","zone":"Europe/Berlin","repeat":{"freq":"weekly"},"attendees":["bob@example.com"]}`
	if w := call(t, srv, "POST", "/api/calendars/"+aliceCal+"/events", body, alice); w.Code != http.StatusCreated {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	srv.DeliverScheduled(context.Background())
	type occ struct {
		meetingJSON
		RecurrenceID string `json:"recurrence_id"`
		Start        string `json:"start"`
	}
	occurrences := func(c *http.Cookie) []occ {
		t.Helper()
		w := call(t, srv, "GET", "/api/events?start=2026-11-01T00:00:00Z&end=2026-12-01T00:00:00Z&tz=UTC", "", c)
		var evs []occ
		if err := json.Unmarshal(w.Body.Bytes(), &evs); err != nil || len(evs) != 3 {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		return evs
	}
	second := occurrences(bob)[1]
	path := "/api/events/" + second.CalendarID + "/" + second.UID + "/rsvp"
	if r := withIfMatch(t, srv, "POST", path, `{"partstat":"DECLINED","recurrence_id":"`+second.RecurrenceID+`"}`, second.ETag, bob); r.Code != http.StatusOK {
		t.Fatalf("answering one occurrence: %d %s", r.Code, r.Body.String())
	}
	srv.DeliverScheduled(context.Background())
	for who, c := range map[string]*http.Cookie{"bob": bob, "alice": alice} {
		for i, e := range occurrences(c) {
			status := ""
			for _, a := range e.Attendees {
				if a.Email == "bob@example.com" {
					status = a.Status
				}
			}
			if want := map[bool]string{true: "DECLINED", false: "NEEDS-ACTION"}[i == 1]; status != want || e.Start != occurrences(bob)[i].Start {
				t.Errorf("%s, occurrence %d (%s): bob %q, want %q", who, i, e.Start, status, want)
			}
		}
	}
}

// Guests are one list for the series (Q1): a "this event" edit that names attendees is refused
// and changes nothing, so nobody is invited to, or cancelled from, the whole series by surprise.
func TestOccurrenceEditCannotChangeGuests(t *testing.T) {
	t.Setenv("KYCALENDAR_SCHEDULING", "true")
	srv, st, _ := setupTestServer(t)
	alice, aliceCal := webUser(t, srv, st, "alice")
	body := `{"title":"Weekly","start":"2026-11-10T09:00:00Z","end":"2026-11-10T10:00:00Z","zone":"UTC","repeat":{"freq":"weekly"},"attendees":["bob@example.com"]}`
	w := call(t, srv, "POST", "/api/calendars/"+aliceCal+"/events", body, alice)
	if w.Code != http.StatusCreated {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	var made struct{ UID, ETag string }
	_ = json.Unmarshal(w.Body.Bytes(), &made)
	path := "/api/events/" + aliceCal + "/" + made.UID
	edit := `{"title":"Weekly","start":"2026-11-17T09:00:00Z","end":"2026-11-17T10:00:00Z","zone":"UTC","scope":"this","recurrence_id":"20261117T090000Z","attendees":["carol@example.com"]}`
	r := withIfMatch(t, srv, "PUT", path, edit, made.ETag, alice)
	if r.Code != http.StatusBadRequest || !strings.Contains(r.Body.String(), `"code":"attendees_series_only"`) {
		t.Fatalf("occurrence guest change: %d %s", r.Code, r.Body.String())
	}
	if r := withIfMatch(t, srv, "PUT", path, strings.Replace(edit, `,"attendees":["carol@example.com"]`, "", 1), made.ETag, alice); r.Code != http.StatusOK {
		t.Fatalf("occurrence edit without guests: %d %s", r.Code, r.Body.String())
	}
}

// A meeting called off stays in an attendee's calendar only when they had answered it, and their
// event view says it is cancelled (Q2, Q7); an unanswered copy is removed.
func TestCancelledMeetingIsMarkedOrRemoved(t *testing.T) {
	t.Setenv("KYCALENDAR_SCHEDULING", "true")
	srv, st, _ := setupTestServer(t)
	alice, aliceCal := webUser(t, srv, st, "alice")
	bob, _ := webUser(t, srv, st, "bob")
	carol, _ := webUser(t, srv, st, "carol")
	body := strings.Replace(meetingBody, `["bob@example.com"]`, `["bob@example.com","carol@example.com"]`, 1)
	w := call(t, srv, "POST", "/api/calendars/"+aliceCal+"/events", body, alice)
	if w.Code != http.StatusCreated {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	var made struct{ UID, ETag string }
	_ = json.Unmarshal(w.Body.Bytes(), &made)
	srv.DeliverScheduled(context.Background())
	got := meetingsOf(t, srv, bob)[made.UID]
	if r := withIfMatch(t, srv, "POST", "/api/events/"+got.CalendarID+"/"+made.UID+"/rsvp", `{"partstat":"ACCEPTED"}`, got.ETag, bob); r.Code != http.StatusOK {
		t.Fatalf("rsvp %d %s", r.Code, r.Body.String())
	}
	srv.DeliverScheduled(context.Background())
	mine := meetingsOf(t, srv, alice)[made.UID]
	if r := withIfMatch(t, srv, "DELETE", "/api/events/"+aliceCal+"/"+made.UID+"?scope=all", "", mine.ETag, alice); r.Code != http.StatusNoContent {
		t.Fatalf("delete %d %s", r.Code, r.Body.String())
	}
	srv.DeliverScheduled(context.Background())
	w = call(t, srv, "GET", "/api/events?start=2026-11-01T00:00:00Z&end=2026-12-01T00:00:00Z&tz=UTC", "", bob)
	if !strings.Contains(w.Body.String(), `"cancelled":true`) {
		t.Fatalf("bob's answered copy after the cancel: %s", w.Body.String())
	}
	if _, ok := meetingsOf(t, srv, carol)[made.UID]; ok {
		t.Fatal("carol never answered, but her cancelled copy stayed")
	}
}

// One sender may newly invite at most davbackend.InviteLimit people an hour; the refusal says
// when they can send again, and sending a meeting again to its own guests is not counted.
func TestInviteLimitPerHour(t *testing.T) {
	t.Setenv("KYCALENDAR_SCHEDULING", "true")
	srv, st, _ := setupTestServer(t)
	alice, aliceCal := webUser(t, srv, st, "alice")
	meeting := func(from int) string {
		var to []string
		for i := from; i < from+100; i++ {
			to = append(to, fmt.Sprintf(`"guest%d@example.net"`, i))
		}
		return `{"title":"Big","start":"2026-11-10T09:00:00Z","end":"2026-11-10T10:00:00Z","zone":"UTC","attendees":[` + strings.Join(to, ",") + `]}`
	}
	var last struct{ UID, ETag string }
	for i := 0; i < davbackend.InviteLimit/100; i++ {
		w := call(t, srv, "POST", "/api/calendars/"+aliceCal+"/events", meeting(i*100), alice)
		if w.Code != http.StatusCreated {
			t.Fatalf("meeting %d: %d %s", i, w.Code, w.Body.String())
		}
		_ = json.Unmarshal(w.Body.Bytes(), &last)
		srv.DeliverScheduled(context.Background()) // no KyPost: they fail at once, freeing the outbox
	}
	// Renaming the last meeting re-sends it to its 100 guests: not new invitations.
	renamed := strings.Replace(meeting(davbackend.InviteLimit-100), `"Big"`, `"Bigger"`, 1)
	if r := withIfMatch(t, srv, "PUT", "/api/events/"+aliceCal+"/"+last.UID, renamed, last.ETag, alice); r.Code != http.StatusOK {
		t.Fatalf("re-sending to the same guests: %d %s", r.Code, r.Body.String())
	}
	w := call(t, srv, "POST", "/api/calendars/"+aliceCal+"/events", strings.Replace(meetingBody, "bob@", "one-more@", 1), alice)
	if w.Code != http.StatusTooManyRequests || !strings.Contains(w.Body.String(), `"code":"invite_limit"`) || !strings.Contains(w.Body.String(), "again at") {
		t.Fatalf("over the limit: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, srv, "POST", "/api/calendars/"+aliceCal+"/events", `{"title":"Solo","start":"2026-11-11T09:00:00Z","end":"2026-11-11T10:00:00Z","zone":"UTC"}`, alice); w.Code != http.StatusCreated {
		t.Fatalf("an event with no guests is not an invitation: %d %s", w.Code, w.Body.String())
	}
}

// An email two active accounts share is neither's scheduling address (Q3): the account that
// copied a colleague's email cannot invite as them.
func TestSharedEmailHasNoSchedulingAddress(t *testing.T) {
	t.Setenv("KYCALENDAR_SCHEDULING", "true")
	srv, st, _ := setupTestServer(t)
	bob, bobCal := webUser(t, srv, st, "bob")
	if w := call(t, srv, "POST", "/api/calendars/"+bobCal+"/events", strings.Replace(meetingBody, "bob@", "carol@", 1), bob); w.Code != http.StatusCreated {
		t.Fatalf("bob alone: %d %s", w.Code, w.Body.String())
	}
	mallory, malloryCal := webUser(t, srv, st, "mallory")
	u, err := st.Users().GetUserByID(context.Background(), "usr_mallory")
	if err != nil {
		t.Fatal(err)
	}
	u.Email = "Bob@example.com"
	if err := st.Users().UpdateUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	for who, c := range map[string][2]any{"mallory": {mallory, malloryCal}, "bob": {bob, bobCal}} {
		w := call(t, srv, "POST", "/api/calendars/"+c[1].(string)+"/events", strings.Replace(meetingBody, "bob@", "carol@", 1), c[0].(*http.Cookie))
		if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), `"code":"no_address"`) {
			t.Fatalf("%s with a shared email: %d %s", who, w.Code, w.Body.String())
		}
	}
}

// Deleting a calendar cancels the meetings in it, as deleting each would: the guests do not keep
// a live meeting nobody can cancel any more.
func TestDeletingACalendarCancelsItsMeetings(t *testing.T) {
	t.Setenv("KYCALENDAR_SCHEDULING", "true")
	srv, st, _ := setupTestServer(t)
	alice, aliceCal := webUser(t, srv, st, "alice")
	bob, _ := webUser(t, srv, st, "bob")
	w := call(t, srv, "POST", "/api/calendars/"+aliceCal+"/events", meetingBody, alice)
	if w.Code != http.StatusCreated {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	var made struct{ UID string }
	_ = json.Unmarshal(w.Body.Bytes(), &made)
	srv.DeliverScheduled(context.Background())
	got := meetingsOf(t, srv, bob)[made.UID]
	if r := withIfMatch(t, srv, "POST", "/api/events/"+got.CalendarID+"/"+made.UID+"/rsvp", `{"partstat":"ACCEPTED"}`, got.ETag, bob); r.Code != http.StatusOK {
		t.Fatalf("rsvp %d %s", r.Code, r.Body.String())
	}
	srv.DeliverScheduled(context.Background())
	if w := call(t, srv, "DELETE", "/api/calendars/"+aliceCal, "", alice); w.Code != http.StatusNoContent {
		t.Fatalf("delete calendar %d %s", w.Code, w.Body.String())
	}
	srv.DeliverScheduled(context.Background())
	w = call(t, srv, "GET", "/api/events?start=2026-11-01T00:00:00Z&end=2026-12-01T00:00:00Z&tz=UTC", "", bob)
	if !strings.Contains(w.Body.String(), `"cancelled":true`) {
		t.Fatalf("bob's copy after alice deleted the calendar: %s", w.Body.String())
	}
}

// Deleting a group calendar cancels a meeting as its organizer only when it can be theirs: an
// editor who copies alice's private meeting into the group calendar cannot have it cancelled as
// alice when an administrator deletes the calendar. Alice's own group meeting still is.
func TestDeletingAGroupCalendarCancelsOnlyItsOrganizersMeetings(t *testing.T) {
	t.Setenv("KYCALENDAR_SCHEDULING", "true")
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	admin := loginAs(t, srv, st, "root", "admin")
	alice, aliceCal := webUser(t, srv, st, "alice")
	bob, _ := webUser(t, srv, st, "bob")
	webUser(t, srv, st, "mallory")
	group := groupCalendar(t, st, "Team")
	grantRole(t, st, group, "editor", "usr_alice")
	if err := st.Groups().AddGroupMember(ctx, "grp_editor_"+group.ID, "usr_mallory"); err != nil {
		t.Fatal(err)
	}

	private := `{"title":"1:1","start":"2030-11-10T09:00:00Z","end":"2030-11-10T10:00:00Z","zone":"UTC","attendees":["bob@example.com"]}`
	w := call(t, srv, "POST", "/api/calendars/"+aliceCal+"/events", private, alice)
	if w.Code != http.StatusCreated {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	var made struct{ UID string }
	_ = json.Unmarshal(w.Body.Bytes(), &made)
	shared := `{"title":"Team sync","start":"2030-11-11T09:00:00Z","end":"2030-11-11T10:00:00Z","zone":"UTC","attendees":["bob@example.com"]}`
	w = call(t, srv, "POST", "/api/calendars/"+group.ID+"/events", shared, alice)
	if w.Code != http.StatusCreated {
		t.Fatalf("create in the group calendar %d %s", w.Code, w.Body.String())
	}
	var team struct{ UID string }
	_ = json.Unmarshal(w.Body.Bytes(), &team)
	srv.DeliverScheduled(ctx)

	// Mallory's forged copy, as a CalDAV client would store it: alice's UID and ORGANIZER.
	ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nBEGIN:VEVENT\r\nUID:" + made.UID + "\r\nDTSTAMP:20301101T000000Z\r\n" +
		"DTSTART:20301110T090000Z\r\nDTEND:20301110T100000Z\r\nSEQUENCE:9\r\nSUMMARY:1:1\r\nORGANIZER:mailto:alice@example.com\r\n" +
		"ATTENDEE:mailto:bob@example.com\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	cal, err := ical.NewDecoder(strings.NewReader(ics)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	o, err := davbackend.Prepare(group.ID, "forged.ics", cal, []byte(ics))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Calendars().PutObject(ctx, o, "", true, store.OwnerLimits{}); err != nil {
		t.Fatal(err)
	}

	if w := call(t, srv, "DELETE", "/api/admin/calendars/"+group.ID, "", admin); w.Code != http.StatusNoContent {
		t.Fatalf("delete group calendar %d %s", w.Code, w.Body.String())
	}
	srv.DeliverScheduled(ctx)
	w = call(t, srv, "GET", "/api/events?start=2030-11-01T00:00:00Z&end=2030-12-01T00:00:00Z&tz=UTC", "", bob)
	var evs []struct {
		UID       string
		Cancelled bool
	}
	if err := json.Unmarshal(w.Body.Bytes(), &evs); err != nil {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	seen := map[string]bool{}
	for _, e := range evs {
		seen[e.UID] = true
		if e.UID == made.UID && e.Cancelled {
			t.Fatal("alice's private meeting was cancelled through mallory's copy")
		}
	}
	if !seen[made.UID] {
		t.Fatalf("bob lost alice's private meeting: %s", w.Body.String())
	}
	if seen[team.UID] {
		t.Fatalf("alice's group meeting was not cancelled for bob (he never answered): %s", w.Body.String())
	}
}

// Alice's own group meeting is still cancelled when she has left the group before an
// administrator deletes the calendar.
func TestDeletingAGroupCalendarCancelsAFormerMembersMeeting(t *testing.T) {
	t.Setenv("KYCALENDAR_SCHEDULING", "true")
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	admin := loginAs(t, srv, st, "root", "admin")
	alice, _ := webUser(t, srv, st, "alice")
	bob, _ := webUser(t, srv, st, "bob")
	group := groupCalendar(t, st, "Team")
	grantRole(t, st, group, "editor", "usr_alice")
	body := `{"title":"Sync","start":"2030-11-10T09:00:00Z","end":"2030-11-10T10:00:00Z","zone":"UTC","attendees":["bob@example.com"]}`
	if w := call(t, srv, "POST", "/api/calendars/"+group.ID+"/events", body, alice); w.Code != http.StatusCreated {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	srv.DeliverScheduled(ctx)
	if err := st.Groups().RemoveGroupMember(ctx, "grp_editor_"+group.ID, "usr_alice"); err != nil {
		t.Fatal(err)
	}
	if w := call(t, srv, "DELETE", "/api/admin/calendars/"+group.ID, "", admin); w.Code != http.StatusNoContent {
		t.Fatalf("delete group calendar %d %s", w.Code, w.Body.String())
	}
	srv.DeliverScheduled(ctx)
	if w := call(t, srv, "GET", "/api/events?start=2030-11-01T00:00:00Z&end=2030-12-01T00:00:00Z&tz=UTC", "", bob); strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("bob kept the meeting of a deleted group calendar: %s", w.Body.String())
	}
}

// A copy of alice's group meeting in a calendar she only reads is no sign the meeting is hers
// elsewhere: deleting the group calendar still cancels it.
func TestDeletingAGroupCalendarIgnoresAReadOnlyCopy(t *testing.T) {
	t.Setenv("KYCALENDAR_SCHEDULING", "true")
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	admin := loginAs(t, srv, st, "root", "admin")
	alice, _ := webUser(t, srv, st, "alice")
	bob, _ := webUser(t, srv, st, "bob")
	team, board := groupCalendar(t, st, "Team"), groupCalendar(t, st, "Board")
	grantRole(t, st, team, "editor", "usr_alice")
	grantRole(t, st, board, "reader", "usr_alice")
	body := `{"title":"Sync","start":"2030-11-10T09:00:00Z","end":"2030-11-10T10:00:00Z","zone":"UTC","attendees":["bob@example.com"]}`
	w := call(t, srv, "POST", "/api/calendars/"+team.ID+"/events", body, alice)
	if w.Code != http.StatusCreated {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	var made struct{ UID string }
	_ = json.Unmarshal(w.Body.Bytes(), &made)
	srv.DeliverScheduled(ctx)
	src, err := st.Calendars().GetObjectByUID(ctx, team.ID, made.UID)
	if err != nil {
		t.Fatal(err)
	}
	cp := *src
	cp.CalendarID, cp.Name = board.ID, "copy.ics"
	if _, err := st.Calendars().PutObject(ctx, &cp, "", true, store.OwnerLimits{}); err != nil {
		t.Fatal(err)
	}
	if w := call(t, srv, "DELETE", "/api/admin/calendars/"+team.ID, "", admin); w.Code != http.StatusNoContent {
		t.Fatalf("delete group calendar %d %s", w.Code, w.Body.String())
	}
	srv.DeliverScheduled(ctx)
	if w := call(t, srv, "GET", "/api/events?start=2030-11-01T00:00:00Z&end=2030-12-01T00:00:00Z&tz=UTC", "", bob); strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("bob kept the meeting of a deleted group calendar: %s", w.Body.String())
	}
}

// The same forgery of a meeting alice organizes from another group calendar: deleting the
// calendar mallory wrote it into still cancels nothing of alice's.
func TestDeletingAGroupCalendarSparesAMeetingOfAnotherGroupCalendar(t *testing.T) {
	t.Setenv("KYCALENDAR_SCHEDULING", "true")
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	admin := loginAs(t, srv, st, "root", "admin")
	alice, _ := webUser(t, srv, st, "alice")
	bob, _ := webUser(t, srv, st, "bob")
	webUser(t, srv, st, "mallory")
	home, doomed := groupCalendar(t, st, "Home"), groupCalendar(t, st, "Doomed")
	grantRole(t, st, home, "editor", "usr_alice")
	grantRole(t, st, doomed, "editor", "usr_alice")
	if err := st.Groups().AddGroupMember(ctx, "grp_editor_"+doomed.ID, "usr_mallory"); err != nil {
		t.Fatal(err)
	}
	body := `{"title":"Sync","start":"2030-11-10T09:00:00Z","end":"2030-11-10T10:00:00Z","zone":"UTC","attendees":["bob@example.com"]}`
	w := call(t, srv, "POST", "/api/calendars/"+home.ID+"/events", body, alice)
	if w.Code != http.StatusCreated {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	var made struct{ UID string }
	_ = json.Unmarshal(w.Body.Bytes(), &made)
	srv.DeliverScheduled(ctx)
	ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nBEGIN:VEVENT\r\nUID:" + made.UID + "\r\nDTSTAMP:20301101T000000Z\r\n" +
		"DTSTART:20301110T090000Z\r\nDTEND:20301110T100000Z\r\nSEQUENCE:9\r\nSUMMARY:Sync\r\nORGANIZER:mailto:alice@example.com\r\n" +
		"ATTENDEE:mailto:bob@example.com\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	cal, err := ical.NewDecoder(strings.NewReader(ics)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	o, err := davbackend.Prepare(doomed.ID, "forged.ics", cal, []byte(ics))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Calendars().PutObject(ctx, o, "", true, store.OwnerLimits{}); err != nil {
		t.Fatal(err)
	}
	if w := call(t, srv, "DELETE", "/api/admin/calendars/"+doomed.ID, "", admin); w.Code != http.StatusNoContent {
		t.Fatalf("delete group calendar %d %s", w.Code, w.Body.String())
	}
	srv.DeliverScheduled(ctx)
	w = call(t, srv, "GET", "/api/events?start=2030-11-01T00:00:00Z&end=2030-12-01T00:00:00Z&tz=UTC", "", bob)
	if !strings.Contains(w.Body.String(), made.UID) || strings.Contains(w.Body.String(), `"cancelled":true`) {
		t.Fatalf("bob's copy of alice's group meeting after the forged copy's calendar went: %s", w.Body.String())
	}
}

// A subscription's mirrored meetings are someone else's: deleting the subscription sends nothing
// and still works with scheduling on.
func TestDeletingASubscriptionWithMeetings(t *testing.T) {
	t.Setenv("KYCALENDAR_SCHEDULING", "true")
	srv, st, _ := setupTestServer(t)
	alice, _ := webUser(t, srv, st, "alice")
	ctx := context.Background()
	sub := &store.Calendar{ID: "cal_sub_alice", OwnerKind: "user", OwnerID: "usr_alice", Slug: "sub", Name: "Work"}
	if err := st.Calendars().CreateSubscribedCalendar(ctx, sub, &store.Subscription{URLSealed: "x", URLHost: "feed.invalid", NextCheckAt: time.Now().Add(time.Hour)}, 0, 0); err != nil {
		t.Fatal(err)
	}
	ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nBEGIN:VEVENT\r\nUID:feed-m\r\nDTSTAMP:20261101T000000Z\r\nDTSTART:20261110T090000Z\r\nDTEND:20261110T100000Z\r\n" +
		"SUMMARY:Board\r\nORGANIZER:mailto:alice@example.com\r\nATTENDEE:mailto:bob@example.com\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	cal, err := ical.NewDecoder(strings.NewReader(ics)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	o, err := davbackend.Prepare(sub.ID, "feed-m.ics", cal, []byte(ics))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Calendars().ReplaceSubscriptionObjects(ctx, sub.ID, []*store.CalendarObject{o}, store.OwnerLimits{}); err != nil {
		t.Fatal(err)
	}
	if w := call(t, srv, "DELETE", "/api/calendars/"+sub.ID, "", alice); w.Code != http.StatusNoContent {
		t.Fatalf("delete subscription %d %s", w.Code, w.Body.String())
	}
	if due, _ := st.Calendars().DueOutbox(ctx, time.Now().Add(time.Hour), 10); len(due) != 0 {
		t.Fatalf("deleting a subscription queued %d messages", len(due))
	}
}

// Alice deletes one occurrence of a weekly meeting bob never answered: that occurrence leaves
// bob's calendar instead of staying as a cancelled copy (Q7); the others stay.
func TestCancelledOccurrenceBobNeverAnsweredGoes(t *testing.T) {
	t.Setenv("KYCALENDAR_SCHEDULING", "true")
	srv, st, _ := setupTestServer(t)
	alice, aliceCal := webUser(t, srv, st, "alice")
	bob, _ := webUser(t, srv, st, "bob")
	body := `{"title":"Weekly","start":"2026-11-10T09:00:00Z","end":"2026-11-10T10:00:00Z","zone":"UTC","repeat":{"freq":"weekly"},"attendees":["bob@example.com"]}`
	w := call(t, srv, "POST", "/api/calendars/"+aliceCal+"/events", body, alice)
	if w.Code != http.StatusCreated {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	var made struct{ UID, ETag string }
	_ = json.Unmarshal(w.Body.Bytes(), &made)
	srv.DeliverScheduled(context.Background())
	if r := withIfMatch(t, srv, "DELETE", "/api/events/"+aliceCal+"/"+made.UID+"?scope=this&recurrence_id=20261117T090000Z", "", made.ETag, alice); r.Code != http.StatusOK {
		t.Fatalf("delete one occurrence %d %s", r.Code, r.Body.String())
	}
	srv.DeliverScheduled(context.Background())
	w = call(t, srv, "GET", "/api/events?start=2026-11-01T00:00:00Z&end=2026-12-01T00:00:00Z&tz=UTC", "", bob)
	if strings.Contains(w.Body.String(), `"cancelled":true`) || strings.Contains(w.Body.String(), "2026-11-17") || !strings.Contains(w.Body.String(), "2026-11-24") {
		t.Fatalf("bob's calendar after alice deleted 17 November: %s", w.Body.String())
	}
}

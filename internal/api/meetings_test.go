package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

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
	if r := withIfMatch(t, srv, "POST", "/api/events/"+got.CalendarID+"/"+made.UID+"/rsvp", `{"partstat":"MAYBE"}`, got.ETag, bob); r.Code != http.StatusBadRequest {
		t.Errorf("bad answer: %d", r.Code)
	}
	if r := withIfMatch(t, srv, "POST", "/api/events/"+got.CalendarID+"/"+made.UID+"/rsvp", `{"partstat":"ACCEPTED"}`, got.ETag, bob); r.Code != http.StatusOK {
		t.Fatalf("rsvp %d %s", r.Code, r.Body.String())
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

func TestWebFreeBusyNeedsScheduling(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	alice, _ := webUser(t, srv, st, "alice")
	if r := call(t, srv, "GET", "/api/freebusy?users=bob@example.com&start=2026-11-10T00:00:00Z&end=2026-11-11T00:00:00Z", "", alice); r.Code != http.StatusUnprocessableEntity {
		t.Errorf("scheduling off: %d", r.Code)
	}
}

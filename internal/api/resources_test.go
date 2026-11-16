package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-ical"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/resource"
	"github.com/Busnes-app/kycalendar/internal/store"
)

type roomEvent struct {
	UID        string `json:"uid"`
	ETag       string `json:"etag"`
	Title      string `json:"title"`
	Location   string `json:"location"`
	CalendarID string `json:"calendar_id"`
	Attendees  []struct {
		Email string `json:"email"`
	} `json:"attendees"`
	Resources []struct {
		ID, Name, Kind, Location, Status, Reason, Detail string
	} `json:"resources"`
}

// slot is an hour-long slot days from now, on the hour, as RFC 3339 start and end.
func slot(days, hour int) (string, string) {
	d := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, days).Add(time.Duration(hour) * time.Hour)
	return d.Format(time.RFC3339), d.Add(time.Hour).Format(time.RFC3339)
}

func makeResource(t *testing.T, st store.Store, r resource.Resource) string {
	t.Helper()
	r.ID = fmt.Sprintf("%032x", time.Now().UnixNano())
	if r.Kind == "" {
		r.Kind = resource.Room
	}
	if r.Audience == "" {
		r.Audience = resource.Everyone
	}
	if r.Approval == "" {
		r.Approval = resource.Auto
	}
	if r.MaxMinutes == 0 {
		r.MaxMinutes = 240
	}
	if r.HorizonDays == 0 {
		r.HorizonDays = 60
	}
	r.Enabled = true
	if err := st.Resources().CreateResource(context.Background(), &r); err != nil {
		t.Fatal(err)
	}
	return r.ID
}

func bookBody(title, start, end, extra string, ids ...string) string {
	q, _ := json.Marshal(append([]string{}, ids...))
	return fmt.Sprintf(`{"title":%q,"start":%q,"end":%q,"zone":"UTC","resources":%s%s}`, title, start, end, q, extra)
}

func createEvent(t *testing.T, srv *api.Server, cookie *http.Cookie, cal, body string) string {
	t.Helper()
	w := call(t, srv, "POST", "/api/calendars/"+cal+"/events", body, cookie)
	if w.Code != http.StatusCreated {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	var made struct{ UID string }
	_ = json.Unmarshal(w.Body.Bytes(), &made)
	return made.UID
}

func roomEvents(t *testing.T, srv *api.Server, cookie *http.Cookie) map[string]roomEvent {
	t.Helper()
	from := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	to := time.Now().UTC().AddDate(0, 0, 140).Format(time.RFC3339)
	w := call(t, srv, "GET", "/api/events?start="+from+"&end="+to+"&tz=UTC", "", cookie)
	var evs []roomEvent
	if err := json.Unmarshal(w.Body.Bytes(), &evs); err != nil {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	out := map[string]roomEvent{}
	for _, e := range evs {
		if _, seen := out[e.UID]; !seen {
			out[e.UID] = e
		}
	}
	return out
}

func roomStatus(t *testing.T, srv *api.Server, cookie *http.Cookie, uid string) (string, string, string) {
	t.Helper()
	e, ok := roomEvents(t, srv, cookie)[uid]
	if !ok || len(e.Resources) != 1 {
		t.Fatalf("event %s: %+v", uid, e)
	}
	return e.Resources[0].Status, e.Resources[0].Reason, e.Resources[0].Detail
}

func TestRoomBookingAcceptsDeclinesAndReleases(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	alice, aliceCal := webUser(t, srv, st, "alice")
	bob, bobCal := webUser(t, srv, st, "bob")
	room := makeResource(t, st, resource.Resource{Name: "Fjord", Location: "Floor 2", Capacity: 8})
	s, e := slot(3, 9)

	uid := createEvent(t, srv, alice, aliceCal, bookBody("Board review", s, e, "", room))
	ev := roomEvents(t, srv, alice)[uid]
	if len(ev.Resources) != 1 || ev.Resources[0].Status != "accepted" || ev.Resources[0].Name != "Fjord" || ev.Location != "Fjord, Floor 2" {
		t.Fatalf("booked %+v", ev)
	}
	if len(ev.Attendees) != 1 || ev.Attendees[0].Email != "alice@example.com" {
		t.Fatalf("the room is not a person: %+v", ev.Attendees)
	}
	o, _ := st.Calendars().GetObjectByUID(context.Background(), aliceCal, uid)
	if !strings.Contains(unfoldICS(string(o.Data)), "CUTYPE=ROOM") || !strings.Contains(unfoldICS(string(o.Data)), "PARTSTAT=ACCEPTED") {
		t.Fatalf("stored object:\n%s", o.Data)
	}

	// Bob's overlapping event is declined, and says when.
	s2, e2 := slot(3, 9)
	other := createEvent(t, srv, bob, bobCal, bookBody("Standup", s2, e2, "", room))
	if st, reason, detail := roomStatus(t, srv, bob, other); st != "declined" || reason != "busy" || detail != s[:10] {
		t.Fatalf("double booking: %s %s %s", st, reason, detail)
	}
	// Bob saving again while the room is still taken keeps the decline.
	bev := roomEvents(t, srv, bob)[other]
	if r := withIfMatch(t, srv, "PUT", "/api/events/"+bobCal+"/"+other, `{"title":"Standup!","start":"`+s2+`","end":"`+e2+`","zone":"UTC"}`, bev.ETag, bob); r.Code != http.StatusOK {
		t.Fatalf("bob edit %d", r.Code)
	}
	if st, _, _ := roomStatus(t, srv, bob, other); st != "declined" {
		t.Fatalf("bob re-save: %s", st)
	}
	// The finder shows the room busy to Bob, and free to Alice for her own event.
	if free := finderFree(t, srv, bob, s, e, "", ""); free[room] {
		t.Fatal("finder shows a busy room free")
	}
	if free := finderFree(t, srv, alice, s, e, aliceCal, uid); !free[room] {
		t.Fatal("finder counts the event's own booking as busy")
	}

	// A title edit keeps the answer; deleting Alice's event releases the room for Bob.
	b := `{"title":"Board review (moved agenda)","start":"` + s + `","end":"` + e + `","zone":"UTC"}`
	if r := withIfMatch(t, srv, "PUT", "/api/events/"+aliceCal+"/"+uid, b, ev.ETag, alice); r.Code != http.StatusOK {
		t.Fatalf("edit %d %s", r.Code, r.Body.String())
	}
	if st, _, _ := roomStatus(t, srv, alice, uid); st != "accepted" {
		t.Fatalf("title edit changed the answer: %s", st)
	}
	ev = roomEvents(t, srv, alice)[uid]
	if r := withIfMatch(t, srv, "DELETE", "/api/events/"+aliceCal+"/"+uid, "", ev.ETag, alice); r.Code != http.StatusNoContent {
		t.Fatalf("delete %d %s", r.Code, r.Body.String())
	}
	if free := finderFree(t, srv, bob, s, e, "", ""); !free[room] {
		t.Fatal("deleting the event did not release the room")
	}
	// Bob's next save books the freed room.
	bev = roomEvents(t, srv, bob)[other]
	if r := withIfMatch(t, srv, "PUT", "/api/events/"+bobCal+"/"+other, `{"title":"Standup","start":"`+s2+`","end":"`+e2+`","zone":"UTC"}`, bev.ETag, bob); r.Code != http.StatusOK {
		t.Fatalf("bob edit %d", r.Code)
	}
	if st, _, _ := roomStatus(t, srv, bob, other); st != "accepted" {
		t.Fatalf("freed room on re-save: %s", st)
	}
	bev = roomEvents(t, srv, bob)[other]
	if r := withIfMatch(t, srv, "DELETE", "/api/events/"+bobCal+"/"+other, "", bev.ETag, bob); r.Code != http.StatusNoContent {
		t.Fatalf("bob delete %d", r.Code)
	}
	// Removing the room from an event releases it too.
	again := createEvent(t, srv, alice, aliceCal, bookBody("Retro", s, e, "", room))
	ev = roomEvents(t, srv, alice)[again]
	if r := withIfMatch(t, srv, "PUT", "/api/events/"+aliceCal+"/"+again, bookBody("Retro", s, e, ""), ev.ETag, alice); r.Code != http.StatusOK {
		t.Fatalf("remove room %d %s", r.Code, r.Body.String())
	}
	if ev := roomEvents(t, srv, alice)[again]; len(ev.Resources) != 0 || len(ev.Attendees) != 0 {
		t.Fatalf("room removed: %+v", ev)
	}
	if free := finderFree(t, srv, bob, s, e, "", ""); !free[room] {
		t.Fatal("removing the room did not release it")
	}
}

func unfoldICS(s string) string { return strings.ReplaceAll(s, "\r\n ", "") }

func finderFree(t *testing.T, srv *api.Server, cookie *http.Cookie, start, end, cal, uid string) map[string]bool {
	t.Helper()
	w := call(t, srv, "GET", "/api/resources?start="+start+"&end="+end+"&calendar="+cal+"&uid="+uid, "", cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("finder %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Resources []struct {
			ID   string
			Free *bool
		}
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	out := map[string]bool{}
	for _, r := range body.Resources {
		out[r.ID] = r.Free != nil && *r.Free
	}
	return out
}

func TestRoomDoubleBookingRace(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	room := makeResource(t, st, resource.Resource{Name: "Fjord"})
	s, e := slot(5, 14)
	type booker struct {
		cookie *http.Cookie
		cal    string
	}
	var people []booker
	for i := range 8 {
		c, cal := webUser(t, srv, st, fmt.Sprintf("racer%d", i))
		people = append(people, booker{c, cal})
	}
	var wg sync.WaitGroup
	uids := make([]string, len(people))
	for i, p := range people {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/api/calendars/"+p.cal+"/events", strings.NewReader(bookBody("Race", s, e, "", room)))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(p.cookie)
			addCSRF(req, p.cookie)
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)
			var made struct{ UID string }
			_ = json.Unmarshal(w.Body.Bytes(), &made)
			uids[i] = made.UID
		}()
	}
	wg.Wait()
	accepted := 0
	for i, p := range people {
		if uids[i] == "" {
			t.Fatalf("racer %d failed to write", i)
		}
		if st, _, _ := roomStatus(t, srv, p.cookie, uids[i]); st == "accepted" {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("%d of %d racers got the room", accepted, len(people))
	}
}

func TestRoomOnlyForAllowedGroups(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	alice, aliceCal := webUser(t, srv, st, "alice")
	bob, bobCal := webUser(t, srv, st, "bob")
	if err := st.Groups().CreateGroup(ctx, &store.Group{ID: "grp_board", DisplayName: "Board"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Groups().AddGroupMember(ctx, "grp_board", "usr_alice"); err != nil {
		t.Fatal(err)
	}
	room := makeResource(t, st, resource.Resource{Name: "Boardroom", Audience: resource.Groups, Groups: []string{"grp_board"}})
	s, e := slot(2, 10)

	if free := finderFree(t, srv, bob, s, e, "", ""); len(free) != 0 {
		t.Fatalf("finder lists a room bob may not book: %v", free)
	}
	if w := call(t, srv, "GET", "/api/resources/"+room+"/busy?start="+s+"&end="+e, "", bob); w.Code != http.StatusNotFound {
		t.Fatalf("busy for an outsider: %d", w.Code)
	}
	if w := call(t, srv, "POST", "/api/calendars/"+bobCal+"/events", bookBody("Sneak", s, e, "", room), bob); w.Code != http.StatusBadRequest || codeOf(t, w.Body.Bytes()) != "unknown_resource" {
		t.Fatalf("web booking by an outsider: %d %s", w.Code, w.Body.String())
	}
	uid := createEvent(t, srv, alice, aliceCal, bookBody("Board", s, e, "", room))
	if st, _, _ := roomStatus(t, srv, alice, uid); st != "accepted" {
		t.Fatalf("member: %s", st)
	}
	// Leaving the group: her next time change is declined.
	if err := st.Groups().RemoveGroupMember(ctx, "grp_board", "usr_alice"); err != nil {
		t.Fatal(err)
	}
	ev := roomEvents(t, srv, alice)[uid]
	s2, e2 := slot(2, 11)
	if r := withIfMatch(t, srv, "PUT", "/api/events/"+aliceCal+"/"+uid, `{"title":"Board","start":"`+s2+`","end":"`+e2+`","zone":"UTC"}`, ev.ETag, alice); r.Code != http.StatusOK {
		t.Fatalf("move %d %s", r.Code, r.Body.String())
	}
	if st, reason, _ := roomStatus(t, srv, alice, uid); st != "declined" || reason != "not_allowed" {
		t.Fatalf("former member: %s %s", st, reason)
	}
}

func TestRoomApproval(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	alice, aliceCal := webUser(t, srv, st, "alice")
	bob, bobCal := webUser(t, srv, st, "bob")
	carol, _ := webUser(t, srv, st, "carol")
	room := makeResource(t, st, resource.Resource{Name: "Hall", Approval: resource.Approvers, Approvers: []string{"usr_carol"}})
	s, e := slot(4, 13)
	uid := createEvent(t, srv, alice, aliceCal, bookBody("Town hall", s, e, "", room))
	if st, _, _ := roomStatus(t, srv, alice, uid); st != "pending" {
		t.Fatalf("approval room: %s", st)
	}
	// A pending request holds the slot.
	if other := createEvent(t, srv, bob, bobCal, bookBody("Clash", s, e, "", room)); true {
		if st, reason, _ := roomStatus(t, srv, bob, other); st != "declined" || reason != "busy" {
			t.Fatalf("pending slot taken: %s %s", st, reason)
		}
	}
	var settings map[string]any
	_ = json.Unmarshal(call(t, srv, "GET", "/api/settings", "", carol).Body.Bytes(), &settings)
	if settings["resource_approver"] != true {
		t.Fatalf("carol is an approver: %v", settings["resource_approver"])
	}
	var reqs struct {
		Requests []struct {
			ID, ResourceName, Organizer string
			Occurrences                 int
		}
	}
	w := call(t, srv, "GET", "/api/resources/requests", "", carol)
	_ = json.Unmarshal(w.Body.Bytes(), &reqs)
	if len(reqs.Requests) != 1 || reqs.Requests[0].Organizer != "alice" || reqs.Requests[0].Occurrences != 1 || strings.Contains(w.Body.String(), "Town hall") {
		t.Fatalf("carol's requests: %s", w.Body.String())
	}
	id := reqs.Requests[0].ID
	if w := call(t, srv, "GET", "/api/resources/requests", "", bob); strings.Contains(w.Body.String(), id) {
		t.Fatal("a non-approver sees the request")
	}
	if w := call(t, srv, "POST", "/api/resources/requests/"+id, `{"decision":"accept"}`, bob); w.Code != http.StatusNotFound {
		t.Fatalf("non-approver decides: %d", w.Code)
	}
	if w := call(t, srv, "POST", "/api/resources/requests/"+id, `{"decision":"maybe"}`, carol); w.Code != http.StatusBadRequest {
		t.Fatalf("bad decision: %d", w.Code)
	}
	if w := call(t, srv, "POST", "/api/resources/requests/"+id, `{"decision":"accept"}`, carol); w.Code != http.StatusNoContent {
		t.Fatalf("accept: %d %s", w.Code, w.Body.String())
	}
	if st, _, _ := roomStatus(t, srv, alice, uid); st != "accepted" {
		t.Fatalf("after accept: %s", st)
	}
	if w := call(t, srv, "POST", "/api/resources/requests/"+id, `{"decision":"decline"}`, carol); w.Code != http.StatusConflict {
		t.Fatalf("second decision: %d", w.Code)
	}

	// A second request, rejected: the slot is free again.
	s2, e2 := slot(4, 15)
	uid2 := createEvent(t, srv, alice, aliceCal, bookBody("Offsite", s2, e2, "", room))
	_ = json.Unmarshal(call(t, srv, "GET", "/api/resources/requests", "", carol).Body.Bytes(), &reqs)
	if len(reqs.Requests) != 1 {
		t.Fatalf("requests %+v", reqs)
	}
	if w := call(t, srv, "POST", "/api/resources/requests/"+reqs.Requests[0].ID, `{"decision":"decline"}`, carol); w.Code != http.StatusNoContent {
		t.Fatalf("decline: %d", w.Code)
	}
	if st, reason, _ := roomStatus(t, srv, alice, uid2); st != "declined" || reason != "rejected" {
		t.Fatalf("after decline: %s %s", st, reason)
	}
	if free := finderFree(t, srv, bob, s2, e2, "", ""); !free[room] {
		t.Fatal("a rejected request still holds the slot")
	}
}

func TestRoomRecurringConflictDeclinesTheSeries(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	alice, aliceCal := webUser(t, srv, st, "alice")
	bob, bobCal := webUser(t, srv, st, "bob")
	room := makeResource(t, st, resource.Resource{Name: "Fjord", HorizonDays: 60})
	// Bob holds the room on day 15; Alice wants it every day at that time.
	bs, be := slot(15, 9)
	createEvent(t, srv, bob, bobCal, bookBody("Bob", bs, be, "", room))
	s, e := slot(1, 9)
	series := createEvent(t, srv, alice, aliceCal, bookBody("Daily", s, e, `,"repeat":{"freq":"daily"}`, room))
	if st, reason, detail := roomStatus(t, srv, alice, series); st != "declined" || reason != "busy" || detail != bs[:10] {
		t.Fatalf("series: %s %s %q (want %s)", st, reason, detail, bs[:10])
	}
	if free := finderFree(t, srv, bob, s, e, "", ""); !free[room] {
		t.Fatal("a declined series holds the room")
	}

	// A weekly series that meets nobody within the horizon is accepted, and keeps every later
	// occurrence: the room shows busy past the horizon, and a booking there is declined.
	ws, we := slot(1, 18)
	weekly := createEvent(t, srv, alice, aliceCal, bookBody("Weekly", ws, we, `,"repeat":{"freq":"weekly"}`, room))
	if st, _, _ := roomStatus(t, srv, alice, weekly); st != "accepted" {
		t.Fatalf("weekly series: %s", st)
	}
	ls, le := slot(1+7*10, 18) // the 11th occurrence, past the 60-day horizon
	if free := finderFree(t, srv, bob, ls, le, "", ""); free[room] {
		t.Fatal("an occurrence past the horizon is not reserved")
	}
	r, err := st.Resources().GetResource(context.Background(), room)
	if err != nil {
		t.Fatal(err)
	}
	r.HorizonDays = 120
	if err := st.Resources().UpdateResource(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	late := createEvent(t, srv, bob, bobCal, bookBody("Late", ls, le, "", room))
	if st, reason, detail := roomStatus(t, srv, bob, late); st != "declined" || reason != "busy" || detail != ls[:10] {
		t.Fatalf("booking over a reserved occurrence: %s %s %s", st, reason, detail)
	}
	// A single event past the horizon is declined.
	fs, fe := slot(130, 9)
	far := createEvent(t, srv, bob, bobCal, bookBody("Far", fs, fe, "", room))
	if st, reason, _ := roomStatus(t, srv, bob, far); st != "declined" || reason != "beyond_horizon" {
		t.Fatalf("past the horizon: %s %s", st, reason)
	}
}

func TestRoomMoveIntoBusySlot(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	alice, aliceCal := webUser(t, srv, st, "alice")
	bob, bobCal := webUser(t, srv, st, "bob")
	room := makeResource(t, st, resource.Resource{Name: "Fjord"})
	bs, be := slot(6, 9)
	createEvent(t, srv, bob, bobCal, bookBody("Bob", bs, be, "", room))
	s, e := slot(6, 11)
	uid := createEvent(t, srv, alice, aliceCal, bookBody("Alice", s, e, "", room))
	ev := roomEvents(t, srv, alice)[uid]
	if r := withIfMatch(t, srv, "PUT", "/api/events/"+aliceCal+"/"+uid, `{"title":"Alice","start":"`+bs+`","end":"`+be+`","zone":"UTC"}`, ev.ETag, alice); r.Code != http.StatusOK {
		t.Fatalf("move %d %s", r.Code, r.Body.String())
	}
	if st, reason, _ := roomStatus(t, srv, alice, uid); st != "declined" || reason != "busy" {
		t.Fatalf("moved into a busy slot: %s %s", st, reason)
	}
	// Moving back to a free slot books it again.
	ev = roomEvents(t, srv, alice)[uid]
	if r := withIfMatch(t, srv, "PUT", "/api/events/"+aliceCal+"/"+uid, `{"title":"Alice","start":"`+s+`","end":"`+e+`","zone":"UTC"}`, ev.ETag, alice); r.Code != http.StatusOK {
		t.Fatalf("move back %d %s", r.Code, r.Body.String())
	}
	if st, _, _ := roomStatus(t, srv, alice, uid); st != "accepted" {
		t.Fatalf("moved back: %s", st)
	}
}

func TestRoomHorizonLengthAndDisabled(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	alice, aliceCal := webUser(t, srv, st, "alice")
	room := makeResource(t, st, resource.Resource{Name: "Fjord", HorizonDays: 10, MaxMinutes: 90})
	far, farEnd := slot(30, 9)
	if uid := createEvent(t, srv, alice, aliceCal, bookBody("Far", far, farEnd, "", room)); true {
		if st, reason, _ := roomStatus(t, srv, alice, uid); st != "declined" || reason != "beyond_horizon" {
			t.Fatalf("beyond horizon: %s %s", st, reason)
		}
	}
	s, _ := slot(2, 9)
	_, long := slot(2, 11)
	if uid := createEvent(t, srv, alice, aliceCal, bookBody("Long", s, long, "", room)); true {
		if st, reason, _ := roomStatus(t, srv, alice, uid); st != "declined" || reason != "too_long" {
			t.Fatalf("too long: %s %s", st, reason)
		}
	}
	s, e := slot(3, 9)
	kept := createEvent(t, srv, alice, aliceCal, bookBody("Kept", s, e, "", room))

	// Disabling keeps the booking (its history) and declines new ones.
	r, err := st.Resources().GetResource(context.Background(), room)
	if err != nil {
		t.Fatal(err)
	}
	r.Enabled = false
	if err := st.Resources().UpdateResource(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := roomStatus(t, srv, alice, kept); st != "accepted" {
		t.Fatalf("disabling dropped a booking: %s", st)
	}
	s2, e2 := slot(4, 9)
	if w := call(t, srv, "POST", "/api/calendars/"+aliceCal+"/events", bookBody("New", s2, e2, "", room), alice); w.Code != http.StatusBadRequest {
		t.Fatalf("web booking of a disabled room: %d", w.Code)
	}
	ev := roomEvents(t, srv, alice)[kept]
	if r := withIfMatch(t, srv, "PUT", "/api/events/"+aliceCal+"/"+kept, `{"title":"Kept","start":"`+s2+`","end":"`+e2+`","zone":"UTC"}`, ev.ETag, alice); r.Code != http.StatusOK {
		t.Fatalf("move %d", r.Code)
	}
	if st, reason, _ := roomStatus(t, srv, alice, kept); st != "declined" || reason != "disabled" {
		t.Fatalf("moving a booking of a disabled room: %s %s", st, reason)
	}
}

func TestRoomBusyNeverShowsTitles(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	alice, aliceCal := webUser(t, srv, st, "alice")
	bob, _ := webUser(t, srv, st, "bob")
	room := makeResource(t, st, resource.Resource{Name: "Fjord"})
	s, e := slot(2, 9)
	createEvent(t, srv, alice, aliceCal, `{"title":"Secret merger","location":"Somewhere","description":"Top secret","start":"`+s+`","end":"`+e+`","zone":"UTC","resources":["`+room+`"]}`)
	from, _ := slot(1, 0)
	to, _ := slot(10, 0)
	w := call(t, srv, "GET", "/api/resources/"+room+"/busy?start="+from+"&end="+to, "", bob)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), s) {
		t.Fatalf("busy %d %s", w.Code, w.Body.String())
	}
	for _, leak := range []string{"Secret", "Somewhere", "alice", "Top secret"} {
		if strings.Contains(w.Body.String(), leak) {
			t.Fatalf("busy leaks %q: %s", leak, w.Body.String())
		}
	}
	if w := call(t, srv, "GET", "/api/resources?start="+s+"&end="+e, "", bob); strings.Contains(w.Body.String(), "Secret") {
		t.Fatalf("finder leaks the title: %s", w.Body.String())
	}
	if w := call(t, srv, "GET", "/api/resources/"+room+"/busy?start="+from+"&end="+time.Now().AddDate(0, 3, 0).UTC().Format(time.RFC3339), "", bob); w.Code != http.StatusBadRequest {
		t.Fatalf("busy over 62 days: %d", w.Code)
	}
}

func TestRoomCalDAVClientGetsTheAnswer(t *testing.T) {
	t.Setenv("KYCALENDAR_SCHEDULING", "true")
	srv, st, _ := setupTestServer(t)
	token := davUser(t, st, "dave", "user")
	u, _ := st.Users().GetUserByID(context.Background(), "usr_dave")
	u.Email = "dave@example.com"
	if err := st.Users().UpdateUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	alice, aliceCal := webUser(t, srv, st, "alice")
	room := makeResource(t, st, resource.Resource{Name: "Fjord"})
	s, e := slot(3, 9)
	createEvent(t, srv, alice, aliceCal, bookBody("Alice", s, e, "", room))
	ts := httptest.NewServer(srv)
	defer ts.Close()

	at := func(v string) string { tt, _ := time.Parse(time.RFC3339, v); return tt.Format("20060102T150405Z") }
	ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\nBEGIN:VEVENT\r\nUID:dav-room\r\nDTSTAMP:20261101T000000Z\r\n" +
		"DTSTART:" + at(s) + "\r\nDTEND:" + at(e) + "\r\nSUMMARY:Dave\r\nORGANIZER:mailto:dave@example.com\r\n" +
		"ATTENDEE;PARTSTAT=ACCEPTED:mailto:dave@example.com\r\n" +
		"ATTENDEE;PARTSTAT=ACCEPTED:" + string(resource.Address(room)) + "\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	resp := rawDAV(t, ts, "PUT", "/dav/usr_dave/calendars/default/dav-room.ics", "dave", token, ics, map[string]string{"Content-Type": "text/calendar"})
	if resp.StatusCode != http.StatusCreated || resp.Header.Get("ETag") != "" {
		t.Fatalf("PUT %d etag %q", resp.StatusCode, resp.Header.Get("ETag"))
	}
	got := unfoldICS(readAll(rawDAV(t, ts, "GET", "/dav/usr_dave/calendars/default/dav-room.ics", "dave", token, "", nil)))
	if !strings.Contains(got, "PARTSTAT=DECLINED") || strings.Count(got, "PARTSTAT=ACCEPTED") != 1 || !strings.Contains(got, "CUTYPE=ROOM") {
		t.Fatalf("the client's own ACCEPTED was kept on a busy room:\n%s", got)
	}
	// Nothing is queued for the room's address.
	srv.DeliverScheduled(context.Background())
	due, err := st.Calendars().DueOutbox(context.Background(), time.Now().Add(time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range due {
		if strings.Contains(m.Recipient, resource.Domain) {
			t.Fatalf("queued a message to a resource: %s", m.Recipient)
		}
	}
}

// putRaw writes ics as object name of calendar cal through the store's write path, as writer
// (empty: the scheduler's delivery).
func putRaw(t *testing.T, st store.Store, cal, name, ics, writer string) {
	t.Helper()
	parsed, err := ical.NewDecoder(strings.NewReader(ics)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	o, err := davbackend.Prepare(cal, name, parsed, []byte(ics))
	if err != nil {
		t.Fatal(err)
	}
	o.WriterID = writer
	if _, err := st.Calendars().PutObjectPlanned(context.Background(), o, "", false, store.OwnerLimits{}, nil); err != nil {
		t.Fatal(err)
	}
}

func icsTime(v string) string {
	tt, _ := time.Parse(time.RFC3339, v)
	return tt.UTC().Format("20060102T150405Z")
}

func roomICS(uid, room string, comps ...string) string {
	out := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\n"
	for _, c := range comps {
		out += "BEGIN:VEVENT\r\nUID:" + uid + "\r\nDTSTAMP:20261101T000000Z\r\n" + c +
			"ORGANIZER:mailto:alice@example.com\r\nATTENDEE;PARTSTAT=ACCEPTED:mailto:alice@example.com\r\n" +
			"ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:bob@example.com\r\nATTENDEE:" + string(resource.Address(room)) + "\r\nEND:VEVENT\r\n"
	}
	return out + "END:VCALENDAR\r\n"
}

func TestRoomKeptWhenAnAttendeeAnswersOneOccurrence(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	alice, aliceCal := webUser(t, srv, st, "alice")
	bob, _ := webUser(t, srv, st, "bob")
	room := makeResource(t, st, resource.Resource{Name: "Fjord"})
	s, e := slot(1, 9)
	master := "DTSTART:" + icsTime(s) + "\r\nDTEND:" + icsTime(e) + "\r\nRRULE:FREQ=WEEKLY\r\nSUMMARY:Weekly\r\n"
	putRaw(t, st, aliceCal, "weekly.ics", roomICS("weekly", room, master), "usr_alice")
	if st, _, _ := roomStatus(t, srv, alice, "weekly"); st != "accepted" {
		t.Fatalf("series: %s", st)
	}
	// The scheduler applies bob's answer to the second occurrence: an override, same times.
	s2, e2 := slot(8, 9)
	override := "RECURRENCE-ID:" + icsTime(s2) + "\r\nDTSTART:" + icsTime(s2) + "\r\nDTEND:" + icsTime(e2) + "\r\nSUMMARY:Weekly\r\n"
	answered := strings.Replace(roomICS("weekly", room, master, override), "PARTSTAT=NEEDS-ACTION:mailto:bob", "PARTSTAT=ACCEPTED:mailto:bob", 1)
	putRaw(t, st, aliceCal, "weekly.ics", answered, "")
	if st, reason, _ := roomStatus(t, srv, alice, "weekly"); st != "accepted" {
		t.Fatalf("an attendee's answer released the room: %s %s", st, reason)
	}
	for _, sl := range [][2]string{{s, e}, {s2, e2}} {
		if free := finderFree(t, srv, bob, sl[0], sl[1], "", ""); free[room] {
			t.Fatalf("slot %s is free after the answer", sl[0])
		}
	}
	// The same for an answer to an occurrence past the horizon (60 days), which the booking
	// holds only through its open series.
	s4, e4 := slot(1+7*12, 9)
	farAnswer := "RECURRENCE-ID:" + icsTime(s4) + "\r\nDTSTART:" + icsTime(s4) + "\r\nDTEND:" + icsTime(e4) + "\r\nSUMMARY:Weekly\r\n"
	answered = strings.Replace(roomICS("weekly", room, master, override, farAnswer), "PARTSTAT=NEEDS-ACTION:mailto:bob", "PARTSTAT=ACCEPTED:mailto:bob", 2)
	putRaw(t, st, aliceCal, "weekly.ics", answered, "")
	if st, reason, _ := roomStatus(t, srv, alice, "weekly"); st != "accepted" {
		t.Fatalf("an answer past the horizon released the room: %s %s", st, reason)
	}
	if free := finderFree(t, srv, bob, s4, e4, "", ""); free[room] {
		t.Fatal("the answered occurrence past the horizon is free")
	}
	// Moving that occurrence is a change the booking does not hold: decided again, and an
	// unknown writer may not book.
	s3, e3 := slot(8, 13)
	moved := "RECURRENCE-ID:" + icsTime(s2) + "\r\nDTSTART:" + icsTime(s3) + "\r\nDTEND:" + icsTime(e3) + "\r\nSUMMARY:Weekly\r\n"
	putRaw(t, st, aliceCal, "weekly.ics", roomICS("weekly", room, master, moved), "")
	if st, reason, _ := roomStatus(t, srv, alice, "weekly"); st != "declined" || reason != "not_allowed" {
		t.Fatalf("moved occurrence by no writer: %s %s", st, reason)
	}
}

func TestRoomConflictReachesTheLongestOccurrence(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	alice, aliceCal := webUser(t, srv, st, "alice")
	bob, bobCal := webUser(t, srv, st, "bob")
	room := makeResource(t, st, resource.Resource{Name: "Fjord", MaxMinutes: 2000})
	// Bob holds day 3, 15:00. Alice's two-occurrence series: day 2 09:00 stretched to day 3
	// 18:00 by an override, and day 3 09:00-10:00.
	bs, be := slot(3, 15)
	createEvent(t, srv, bob, bobCal, bookBody("Bob", bs, be, "", room))
	s, e := slot(2, 9)
	_, long := slot(3, 17)
	master := "DTSTART:" + icsTime(s) + "\r\nDTEND:" + icsTime(e) + "\r\nRRULE:FREQ=DAILY;COUNT=2\r\nSUMMARY:Long\r\n"
	override := "RECURRENCE-ID:" + icsTime(s) + "\r\nDTSTART:" + icsTime(s) + "\r\nDTEND:" + icsTime(long) + "\r\nSUMMARY:Long\r\n"
	putRaw(t, st, aliceCal, "long.ics", roomICS("long", room, master, override), "usr_alice")
	if st, reason, _ := roomStatus(t, srv, alice, "long"); st != "declined" || reason != "busy" {
		t.Fatalf("overlap with an earlier, longer occurrence: %s %s", st, reason)
	}
}

func TestRoomOversizedFutureOverrideIsTooLong(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	alice, aliceCal := webUser(t, srv, st, "alice")
	room := makeResource(t, st, resource.Resource{Name: "Fjord", MaxMinutes: 120, HorizonDays: 30})
	s, e := slot(1, 9)
	master := "DTSTART:" + icsTime(s) + "\r\nDTEND:" + icsTime(e) + "\r\nRRULE:FREQ=WEEKLY\r\nSUMMARY:Weekly\r\n"
	fs, _ := slot(1+7*20, 9)
	_, fe := slot(1+7*20, 17)
	far := "RECURRENCE-ID:" + icsTime(fs) + "\r\nDTSTART:" + icsTime(fs) + "\r\nDTEND:" + icsTime(fe) + "\r\nSUMMARY:Weekly\r\n"
	putRaw(t, st, aliceCal, "far.ics", roomICS("far", room, master, far), "usr_alice")
	if st, reason, _ := roomStatus(t, srv, alice, "far"); st != "declined" || reason != "too_long" {
		t.Fatalf("oversized override past the horizon: %s %s", st, reason)
	}
	for i, rdate := range []string{"RDATE;VALUE=PERIOD:", "RDATE:"} {
		uid := fmt.Sprintf("period%d", i)
		period := "DTSTART:" + icsTime(s) + "\r\nDTEND:" + icsTime(e) + "\r\nRRULE:FREQ=WEEKLY\r\n" + rdate + icsTime(fs) + "/PT8H\r\nSUMMARY:Weekly\r\n"
		putRaw(t, st, aliceCal, uid+".ics", roomICS(uid, room, period), "usr_alice")
		if st, reason, _ := roomStatus(t, srv, alice, uid); st != "declined" || reason != "too_long" {
			t.Fatalf("oversized period %q: %s %s", rdate, st, reason)
		}
	}
}

func TestRoomChangedLaterOverrideNeedsApprovalAgain(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	alice, aliceCal := webUser(t, srv, st, "alice")
	carol, _ := webUser(t, srv, st, "carol")
	room := makeResource(t, st, resource.Resource{Name: "Hall", HorizonDays: 30, Approval: resource.Approvers, Approvers: []string{"usr_carol"}})
	s, e := slot(1, 9)
	master := "DTSTART:" + icsTime(s) + "\r\nDTEND:" + icsTime(e) + "\r\nRRULE:FREQ=WEEKLY\r\nSUMMARY:Weekly\r\n"
	fs, fe := slot(1+7*20, 9)
	far := "RECURRENCE-ID:" + icsTime(fs) + "\r\nDTSTART:" + icsTime(fs) + "\r\nDTEND:" + icsTime(fe) + "\r\nSUMMARY:Weekly\r\n"
	putRaw(t, st, aliceCal, "w.ics", roomICS("w", room, master, far), "usr_alice")
	var reqs struct{ Requests []struct{ ID string } }
	_ = json.Unmarshal(call(t, srv, "GET", "/api/resources/requests", "", carol).Body.Bytes(), &reqs)
	if len(reqs.Requests) != 1 {
		t.Fatalf("requests %+v", reqs)
	}
	if w := call(t, srv, "POST", "/api/resources/requests/"+reqs.Requests[0].ID, `{"decision":"accept"}`, carol); w.Code != http.StatusNoContent {
		t.Fatalf("accept %d", w.Code)
	}
	if st, _, _ := roomStatus(t, srv, alice, "w"); st != "accepted" {
		t.Fatalf("approved: %s", st)
	}
	// Moving that far occurrence to another hour asks the approvers again.
	ms, me := slot(1+7*20, 14)
	moved := "RECURRENCE-ID:" + icsTime(fs) + "\r\nDTSTART:" + icsTime(ms) + "\r\nDTEND:" + icsTime(me) + "\r\nSUMMARY:Weekly\r\n"
	putRaw(t, st, aliceCal, "w.ics", roomICS("w", room, master, moved), "usr_alice")
	if st, _, _ := roomStatus(t, srv, alice, "w"); st != "pending" {
		t.Fatalf("a moved occurrence past the horizon kept the approval: %s", st)
	}
}

func TestRoomAmbiguousObjectIsDeclined(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	alice, aliceCal := webUser(t, srv, st, "alice")
	room := makeResource(t, st, resource.Resource{Name: "Hall", MaxMinutes: 120, HorizonDays: 30, Approval: resource.Approvers, Approvers: []string{"usr_alice"}})
	s, e := slot(1, 9)
	master := "DTSTART:" + icsTime(s) + "\r\nDTEND:" + icsTime(e) + "\r\nRRULE:FREQ=WEEKLY\r\nSUMMARY:Weekly\r\n"
	fs, fe := slot(1+7*20, 9)
	_, long := slot(1+7*20, 17)
	// Two components for one occurrence, the same instant written two ways.
	one := "RECURRENCE-ID:" + icsTime(fs) + "\r\nDTSTART:" + icsTime(fs) + "\r\nDTEND:" + icsTime(fe) + "\r\nSUMMARY:Weekly\r\n"
	tt, _ := time.Parse(time.RFC3339, fs)
	twin := "RECURRENCE-ID;TZID=Europe/Berlin:" + tt.In(mustZone(t, "Europe/Berlin")).Format("20060102T150405") + "\r\nDTSTART:" + icsTime(fs) + "\r\nDTEND:" + icsTime(long) + "\r\nSUMMARY:Weekly\r\n"
	putRaw(t, st, aliceCal, "dup.ics", roomICS("dup", room, master, one, twin), "usr_alice")
	if st, reason, _ := roomStatus(t, srv, alice, "dup"); st != "declined" || reason != "ambiguous" {
		t.Fatalf("duplicate occurrence: %s %s", st, reason)
	}
}

func mustZone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestRoomFiniteSeriesGoingPastTheHorizonNeedsApprovalAgain(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	alice, aliceCal := webUser(t, srv, st, "alice")
	carol, _ := webUser(t, srv, st, "carol")
	room := makeResource(t, st, resource.Resource{Name: "Hall", HorizonDays: 30, Approval: resource.Approvers, Approvers: []string{"usr_carol"}})
	s, e := slot(1, 9)
	master := "DTSTART:" + icsTime(s) + "\r\nDTEND:" + icsTime(e) + "\r\nRRULE:FREQ=WEEKLY;COUNT=3\r\nSUMMARY:Three\r\n"
	putRaw(t, st, aliceCal, "three.ics", roomICS("three", room, master), "usr_alice")
	var reqs struct{ Requests []struct{ ID string } }
	_ = json.Unmarshal(call(t, srv, "GET", "/api/resources/requests", "", carol).Body.Bytes(), &reqs)
	if len(reqs.Requests) != 1 {
		t.Fatalf("requests %+v", reqs)
	}
	if w := call(t, srv, "POST", "/api/resources/requests/"+reqs.Requests[0].ID, `{"decision":"accept"}`, carol); w.Code != http.StatusNoContent {
		t.Fatalf("accept %d", w.Code)
	}
	// The second occurrence moves 100 days out, past the 30-day horizon.
	s2, _ := slot(8, 9)
	fs, fe := slot(100, 9)
	moved := "RECURRENCE-ID:" + icsTime(s2) + "\r\nDTSTART:" + icsTime(fs) + "\r\nDTEND:" + icsTime(fe) + "\r\nSUMMARY:Three\r\n"
	putRaw(t, st, aliceCal, "three.ics", roomICS("three", room, master, moved), "usr_alice")
	if st, _, _ := roomStatus(t, srv, alice, "three"); st != "pending" {
		t.Fatalf("an approved finite series gained time past the horizon and kept its approval: %s", st)
	}
}

func TestRoomImplicitAllDayOverrideIsMeasured(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	alice, aliceCal := webUser(t, srv, st, "alice")
	room := makeResource(t, st, resource.Resource{Name: "Fjord", MaxMinutes: 120, HorizonDays: 30})
	s, e := slot(1, 9)
	master := "DTSTART:" + icsTime(s) + "\r\nDTEND:" + icsTime(e) + "\r\nRRULE:FREQ=WEEKLY\r\nSUMMARY:Weekly\r\n"
	fs, _ := slot(1+7*20, 9)
	day := fs[:4] + fs[5:7] + fs[8:10]
	allDay := "RECURRENCE-ID:" + icsTime(fs) + "\r\nDTSTART;VALUE=DATE:" + day + "\r\nSUMMARY:Weekly\r\n"
	putRaw(t, st, aliceCal, "allday.ics", roomICS("allday", room, master, allDay), "usr_alice")
	if st, reason, _ := roomStatus(t, srv, alice, "allday"); st != "declined" || reason != "too_long" {
		t.Fatalf("implicit one-day override past the horizon: %s %s", st, reason)
	}
}

func TestRoomOverlongMasterOutsideHorizonIsDeclined(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	alice, aliceCal := webUser(t, srv, st, "alice")
	bob, _ := webUser(t, srv, st, "bob")
	room := makeResource(t, st, resource.Resource{Name: "Fjord", MaxMinutes: 120, HorizonDays: 10})
	// A five-hour weekly master whose only occurrences within the horizon are short overrides.
	s, _ := slot(2, 9)
	_, long := slot(2, 14)
	master := "DTSTART:" + icsTime(s) + "\r\nDTEND:" + icsTime(long) + "\r\nRRULE:FREQ=WEEKLY\r\nSUMMARY:Weekly\r\n"
	var comps []string
	for _, d := range []int{2, 9} {
		os, oe := slot(d, 9)
		comps = append(comps, "RECURRENCE-ID:"+icsTime(os)+"\r\nDTSTART:"+icsTime(os)+"\r\nDTEND:"+icsTime(oe)+"\r\nSUMMARY:Weekly\r\n")
	}
	putRaw(t, st, aliceCal, "master.ics", roomICS("master", room, append([]string{master}, comps...)...), "usr_alice")
	if st, reason, _ := roomStatus(t, srv, alice, "master"); st != "declined" || reason != "too_long" {
		t.Fatalf("overlong master: %s %s", st, reason)
	}
	ls, le := slot(16, 9)
	if free := finderFree(t, srv, bob, ls, le, "", ""); !free[room] {
		t.Fatal("a declined series reserves its later occurrences")
	}
}

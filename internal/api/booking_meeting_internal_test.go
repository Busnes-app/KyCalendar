package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/auth"
	"github.com/Busnes-app/kycalendar/internal/kymessages"
	"github.com/Busnes-app/kycalendar/internal/kypost"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// bookWithRoom books the org's type that adds a meeting room, as a visitor, confirms it, lets the
// room open and delivers: what the booker is mailed.
func bookWithRoom(t *testing.T, guestsCode string) (*meetRig, []string) {
	t.Helper()
	r, box := guestRig(t, guestsCode)
	ctx := context.Background()
	r.s.kypost.Set(&kypost.Peer{URL: "https://kypost.example", Secret: []byte("kypost-secret-0123456789abcdef0123"), Source: kypost.SourceEnvironment})
	if err := r.s.store.Settings().SetSetting(ctx, bookingAllowedSetting, "true"); err != nil {
		t.Fatal(err)
	}
	windows := `[{"day":0,"start":"00:00","end":"24:00"},{"day":1,"start":"00:00","end":"24:00"},{"day":2,"start":"00:00","end":"24:00"},{"day":3,"start":"00:00","end":"24:00"},{"day":4,"start":"00:00","end":"24:00"},{"day":5,"start":"00:00","end":"24:00"},{"day":6,"start":"00:00","end":"24:00"}]`
	slug := strings.Repeat("ab", 16)
	if err := r.s.store.Bookings().CreateType(ctx, &store.BookingType{ID: "bt_room", UserID: r.org.ID, Slug: slug, Enabled: true, Title: "Intro",
		DurationMin: 60, Zone: "UTC", Windows: windows, HorizonDays: 3, CalendarID: r.cal.ID, MeetingRoom: true}, 20); err != nil {
		t.Fatal(err)
	}
	var cookies []*http.Cookie
	do := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.RemoteAddr = "198.51.100.9:1"
		for _, c := range cookies {
			req.AddCookie(c)
			if c.Name == auth.CSRFCookieName {
				req.Header.Set(auth.HeaderCSRF, c.Value)
			}
		}
		w := httptest.NewRecorder()
		r.s.ServeHTTP(w, req)
		cookies = append(cookies, w.Result().Cookies()...)
		return w
	}
	page := do("GET", "/api/book/"+slug, "")
	var p struct{ Slots []struct{ Start string } }
	_ = json.Unmarshal(page.Body.Bytes(), &p)
	if page.Code != 200 || len(p.Slots) < 3 {
		t.Fatalf("page %d %s", page.Code, page.Body.String())
	}
	if w := do("POST", "/api/book/"+slug+"/hold", fmt.Sprintf(`{"start":%q,"name":"Carol","email":"carol@outside.example"}`, p.Slots[2].Start)); w.Code != 202 {
		t.Fatalf("hold %d %s", w.Code, w.Body.String())
	}
	hold := box.to("carol@outside.example")
	m := regexp.MustCompile(`/book/c#([0-9a-f-]{36})\.([0-9a-f]{64})`).FindStringSubmatch(strings.ReplaceAll(hold[0], "\r\n ", ""))
	if w := do("POST", "/api/book/confirm", fmt.Sprintf(`{"id":%q,"token":%q}`, m[1], m[2])); w.Code != 200 {
		t.Fatalf("confirm %d %s", w.Code, w.Body.String())
	}
	r.sync()
	r.sync() // the room link written into the event is a change too
	r.clock = r.clock.Add(3 * 60 * 60 * 1e9)
	r.deliver(t)
	return r, box.to("carol@outside.example")[1:]
}

// A booking with a meeting room opens it through the wave 1 PUT, and the booker joins through
// their own guest link only: never the member room link.
func TestBookingMeetingRoomGivesTheBookerOnlyTheirGuestLink(t *testing.T) {
	r, mail := bookWithRoom(t, "")
	calls := r.km.seen()
	if len(calls) == 0 || calls[0].Method != "PUT" || len(calls[0].Body.Guests) != 1 {
		t.Fatalf("KyMessages calls %+v", calls)
	}
	gs, _ := r.s.store.Calendars().MeetingGuests(context.Background(), calls[0].ID)
	if len(gs) != 1 {
		t.Fatalf("guests %+v", gs)
	}
	if len(mail) == 0 {
		t.Fatal("no invitation")
	}
	for _, ics := range mail {
		ics = strings.ReplaceAll(ics, "\r\n ", "")
		if !strings.Contains(ics, kymessagesGuestLine+guestURL(gs[0].Key)) || strings.Contains(ics, roomURL(calls[0].ID)) || strings.Contains(ics, "CONFERENCE") {
			t.Fatalf("booker's invitation:\n%s", ics)
		}
	}
	// The member's own event keeps the room.
	objs, _ := r.s.store.Calendars().ListObjects(context.Background(), r.cal.ID)
	if len(objs) != 1 || !strings.Contains(string(objs[0].Data), roomURL(calls[0].ID)) {
		t.Fatal("the member's event lost its room")
	}
}

// When KyMessages takes no guests, the room is for members only and the invitation says
// nothing about joining.
func TestBookingMeetingRoomWithoutGuestsSaysNothingAboutJoining(t *testing.T) {
	for _, code := range []string{kymessages.CodeGuestsOff, kymessages.CodeGuestsUnsupported} {
		t.Run(code, func(t *testing.T) {
			r, mail := bookWithRoom(t, code)
			if len(mail) == 0 {
				t.Fatal("no invitation")
			}
			for _, ics := range mail {
				ics = strings.ReplaceAll(ics, "\r\n ", "")
				if strings.Contains(ics, "Join") || strings.Contains(ics, "CONFERENCE") || strings.Contains(ics, "chat.example") {
					t.Fatalf("an invitation mentions joining:\n%s", ics)
				}
			}
			if calls := r.km.seen(); len(calls) < 2 {
				t.Fatalf("the room was not opened without guests: %+v", calls)
			}
		})
	}
}

// A member without video meetings cannot ask for a meeting room.
func TestBookingMeetingRoomNeedsMeetings(t *testing.T) {
	s, _ := davInternalServer(t)
	err := s.applyBookingType(context.Background(), &store.User{ID: "usr_x", Role: "user"}, bookingTypeBody{MeetingRoom: true}, &store.BookingType{})
	if err == nil || err.code != "meetings_off" {
		t.Fatalf("got %v", err)
	}
	// A type that already has a room keeps it through an edit while meetings are down.
	if err := s.applyBookingType(context.Background(), &store.User{ID: "usr_x", Role: "user"}, bookingTypeBody{MeetingRoom: true}, &store.BookingType{MeetingRoom: true}); err != nil && err.code == "meetings_off" {
		t.Fatal("an existing room blocked an edit")
	}
}

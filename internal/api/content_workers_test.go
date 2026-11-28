package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/contentkey"
	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/skeleton"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// setUpEncryption runs the web setup for the signed-in cookie's account.
func setUpEncryption(t *testing.T, srv *api.Server, cookie *http.Cookie) {
	t.Helper()
	w := do(t, srv, "POST", "/api/encryption/recovery-code", cookie)
	var rc struct {
		Code string `json:"recovery_code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &rc)
	if w := doJSON(t, srv, "POST", "/api/encryption/setup", cookie, map[string]string{"passphrase": "correct horse battery", "recovery_code": rc.Code}); w.Code != http.StatusNoContent {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}
}

type eventView struct {
	CalendarID  string `json:"calendar_id"`
	UID         string `json:"uid"`
	ETag        string `json:"etag"`
	Title       string `json:"title"`
	Location    string `json:"location"`
	Description string `json:"description"`
	MeetingURL  string `json:"meeting_url"`
}

func eventsOf(t *testing.T, srv *api.Server, cookie *http.Cookie) map[string]eventView {
	t.Helper()
	w := call(t, srv, "GET", "/api/events?start=2026-11-01T00:00:00Z&end=2026-12-01T00:00:00Z&tz=UTC", "", cookie)
	var evs []eventView
	if err := json.Unmarshal(w.Body.Bytes(), &evs); err != nil {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	out := map[string]eventView{}
	for _, e := range evs {
		out[e.UID] = e
	}
	return out
}

// An organizer's update reaches an encrypted attendee's sealed copy (a new base, CE3) with the
// new title and no location. A write by someone without the key (the meeting sync's room link)
// is planned from the organizer's skeleton: every attendee copy, sealed or plaintext, keeps its
// title and takes only the link.
func TestOrganizerChangesReachEncryptedAndPlainCopies(t *testing.T) {
	t.Setenv("KYCALENDAR_SCHEDULING", "true")
	srv, st, _ := setupTestServerWith(t, func(c *config.Config) { c.Calendar.ContentEncryption = true })
	ctx := context.Background()
	alice, aliceCal := webUser(t, srv, st, "alice")
	bob, _ := webUser(t, srv, st, "bob")
	carol, _ := webUser(t, srv, st, "carol") // never sets up: her copy stays plaintext
	setUpEncryption(t, srv, alice)
	setUpEncryption(t, srv, bob)

	body := `{"title":"Secret plan","location":"Room 42","start":"2026-11-10T09:00:00Z","end":"2026-11-10T10:00:00Z","zone":"UTC","attendees":["bob@example.com","carol@example.com"]}`
	w := call(t, srv, "POST", "/api/calendars/"+aliceCal+"/events", body, alice)
	if w.Code != http.StatusCreated {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	var made struct{ UID, ETag string }
	_ = json.Unmarshal(w.Body.Bytes(), &made)
	srv.DeliverScheduled(ctx)
	bobs := eventsOf(t, srv, bob)[made.UID]
	if bobs.Title != "Secret plan" || bobs.Location != "Room 42" {
		t.Fatalf("bob's first copy: %+v", bobs)
	}

	moved := `{"title":"Moved plan","start":"2026-11-10T09:00:00Z","end":"2026-11-10T10:00:00Z","zone":"UTC","attendees":["bob@example.com","carol@example.com"]}`
	if r := withIfMatch(t, srv, "PUT", "/api/events/"+aliceCal+"/"+made.UID, moved, eventsOf(t, srv, alice)[made.UID].ETag, alice); r.Code != http.StatusOK {
		t.Fatalf("alice's update %d %s", r.Code, r.Body.String())
	}
	srv.DeliverScheduled(ctx)
	for who, cookie := range map[string]*http.Cookie{"bob": bob, "carol": carol} {
		if got := eventsOf(t, srv, cookie)[made.UID]; got.Title != "Moved plan" || got.Location != "" {
			t.Fatalf("%s's copy after the update: %+v", who, got)
		}
	}
	// The room link, written by a worker over alice's sealed event and planned from its skeleton.
	o, err := st.Calendars().GetObjectByUID(ctx, aliceCal, made.UID)
	if err != nil || o.Sealed == nil {
		t.Fatalf("alice's event: %v", err)
	}
	cal, err := ical.NewDecoder(bytes.NewReader(o.Data)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	url := "https://messages.example.com/r/1"
	calendar.SetConference(cal, url)
	var raw bytes.Buffer
	_ = ical.NewEncoder(&raw).Encode(cal)
	owner, _ := st.Users().GetUserByID(ctx, "usr_alice")
	c, _ := st.Calendars().GetCalendarByID(ctx, aliceCal)
	sched := &davbackend.Scheduler{Store: st}
	wctx := store.WithWorkerContent(ctx, &store.WorkerContent{Patch: []skeleton.Op{{Op: "conference", URL: url}}})
	if _, err := davbackend.WriteMeeting(wctx, st, c, o.Name, cal, raw.Bytes(), o.ETag, false, store.OwnerLimits{}, sched.Plan(wctx, owner, cal), owner.ID, nil); err != nil {
		t.Fatal(err)
	}
	srv.DeliverScheduled(ctx)
	for who, cookie := range map[string]*http.Cookie{"alice": alice, "bob": bob, "carol": carol} {
		got := eventsOf(t, srv, cookie)[made.UID]
		if got.Title != "Moved plan" || !strings.Contains(got.Description, url) && got.MeetingURL != url {
			t.Fatalf("%s's copy after a skeleton-planned update: %+v", who, got)
		}
		obj, err := st.Calendars().GetObjectByUID(ctx, got.CalendarID, made.UID)
		if err != nil {
			t.Fatal(err)
		}
		if who == "carol" {
			if obj.Sealed != nil || !strings.Contains(string(obj.Data), "Moved plan") || !strings.Contains(string(obj.Data), url) {
				t.Fatalf("carol's plaintext copy: %s", obj.Data)
			}
			continue
		}
		if bytes.Contains(obj.Data, []byte("Moved plan")) || bytes.Contains(obj.Data, []byte(url)) {
			t.Fatalf("%s's sealed copy holds content in data: %s", who, obj.Data)
		}
	}
}

// A booker who reschedules a booking whose host has encryption gets an update with the booking's
// title, never an untitled one, and the host's sealed event keeps its content.
func TestRescheduleOfASealedBookingKeepsATitle(t *testing.T) {
	t.Setenv("KYCALENDAR_CONTENT_ENCRYPTION", "true")
	w := newBookingWorld(t)
	ctx := context.Background()
	_, code, _ := contentkey.NewRecoveryCode()
	sess := store.Credential{UserID: "usr_bob", Kind: contentkey.SlotSession, SlotID: "sess-bob", Secret: bytes.Repeat([]byte{7}, 32)}
	if err := w.st.Content().Setup(ctx, "usr_bob", store.Setup{Passphrase: "correct horse battery", Recovery: code, Session: sess, Transition: bytes.Repeat([]byte{9}, 32), Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	v := newVisitor()
	id, token := w.hold(t, v, v.page(t, w, w.slug).Slots[0].Start)
	if r := v.do(t, w.srv, "POST", "/api/book/confirm", linkJSON(id, token)); r.Code != 200 {
		t.Fatalf("confirm: %d %s", r.Code, r.Body.String())
	}
	w.srv.DeliverScheduled(ctx)
	mid, mtoken := linkIn(t, manageLink, w.fake.take()[0].ICS)
	objs, _ := w.st.Calendars().ListObjects(ctx, w.cal)
	if len(objs) != 1 || objs[0].Sealed == nil || bytes.Contains(objs[0].Data, []byte("Intro call")) {
		t.Fatalf("the booking's event is not sealed: %+v", objs)
	}
	r := v.do(t, w.srv, "POST", "/api/book/manage", linkJSON(mid, mtoken))
	var m struct{ Slots []struct{ Start string } }
	_ = json.Unmarshal(r.Body.Bytes(), &m)
	if len(m.Slots) == 0 {
		t.Fatalf("manage: %d %s", r.Code, r.Body.String())
	}
	if r := v.do(t, w.srv, "POST", "/api/book/manage/reschedule", fmt.Sprintf(`{"id":%q,"token":%q,"start":%q}`, mid, mtoken, m.Slots[len(m.Slots)-1].Start)); r.Code != 200 {
		t.Fatalf("reschedule: %d %s", r.Code, r.Body.String())
	}
	w.srv.DeliverScheduled(ctx)
	sent := w.fake.take()
	if len(sent) != 1 || !strings.Contains(unfold(sent[0].ICS), "SUMMARY:Intro call") {
		t.Fatalf("the reschedule mail has no title: %+v", sent)
	}
	objs, _ = w.st.Calendars().ListObjects(ctx, w.cal)
	if len(objs) != 1 || objs[0].Sealed == nil || bytes.Contains(objs[0].Data, []byte("Intro call")) {
		t.Fatal("the reschedule put content in data")
	}
}

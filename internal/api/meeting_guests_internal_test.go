package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/kymessages"
	"github.com/Busnes-app/kycalendar/internal/kypost"
)

// mailbox is KyPost as the scheduler sees it: every message it was asked to mail.
type mailbox struct {
	mu   sync.Mutex
	sent []kypost.SendRequest
}

func (b *mailbox) Send(_ context.Context, _ string, req kypost.SendRequest) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sent = append(b.sent, req)
	return nil
}

func (b *mailbox) to(email string) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for _, m := range b.sent {
		if len(m.Recipients) != 1 {
			panic("a message to several recipients")
		}
		if m.Recipients[0] == email {
			out = append(out, m.ICS)
		}
	}
	return out
}

func guestURL(key string) string { return "https://chat.example/meet/secret-" + key }

// guestRig is a meeting rig with scheduling on, KyPost as a mailbox and a KyMessages that gives
// every guest key a link (guestsCode: refuse guests with it instead).
func guestRig(t *testing.T, guestsCode string) (*meetRig, *mailbox) {
	t.Helper()
	r := newMeetRig(t)
	r.s.startScheduling()
	box := &mailbox{}
	r.s.sched.KyPost = box
	r.s.sched.Now = func() time.Time { return r.clock }
	r.km.answer(func(method, id string) (int, string) {
		if method != "PUT" {
			return 200, `{"outcome":"closed"}`
		}
		calls := r.km.seen()
		last := calls[len(calls)-1].Body
		if len(last.Guests) > 0 && guestsCode != "" {
			return 422, `{"error":"x","code":"` + guestsCode + `"}`
		}
		links := map[string]string{}
		for _, g := range last.Guests {
			links[g.Key] = guestURL(g.Key)
		}
		b, _ := json.Marshal(map[string]any{"member_url": roomURL(id), "skipped_subs": []string{}, "guest_urls": links})
		return 200, string(b)
	})
	return r, box
}

func (r *meetRig) deliver(t *testing.T) {
	t.Helper()
	if _, err := r.s.sched.DeliverDue(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// The guest link never reaches anyone but its own attendee: not the stored event, its
// CONFERENCE property or description, the queued messages, nor any other attendee's copy.
func TestGuestLinkReachesOnlyItsOwnAttendee(t *testing.T) {
	r, box := guestRig(t, "")
	uid := r.create(t, map[string]any{"meeting": true, "description": "Agenda",
		"attendees": []string{"ann@example.com", "Out@Elsewhere.example", "eve@other.example"}})
	// Before KyMessages answers, invitations wait for their links.
	r.deliver(t)
	if n := len(box.to("out@elsewhere.example")); n != 0 {
		t.Fatalf("%d invitations left before the guest link existed", n)
	}
	r.sync()
	r.sync() // the room link written into the event is a change too
	calls := r.km.seen()
	put := calls[0].Body
	if len(put.Guests) != 2 || len(put.Windows) != 1 || put.Windows[0].Start != "2026-11-16T08:50:00Z" || put.Windows[0].End != "2026-11-16T10:00:00Z" {
		t.Fatalf("PUT %+v", put)
	}
	raw, _ := json.Marshal(put)
	for _, leak := range []string{"elsewhere", "other.example", "Out", "eve"} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("KyMessages saw %q: %s", leak, raw)
		}
	}
	keys := map[string]string{}
	gs, _ := r.s.store.Calendars().MeetingGuests(context.Background(), calls[0].ID)
	for _, g := range gs {
		keys[g.Email] = g.Key
		if strings.Contains(g.SealedURL, "secret") || g.SealedURL == "" {
			t.Fatalf("link stored %q", g.SealedURL)
		}
	}
	outURL, eveURL := guestURL(keys["out@elsewhere.example"]), guestURL(keys["eve@other.example"])

	// Nothing stored or queued carries a guest link.
	due, _ := r.s.store.Calendars().DueOutbox(context.Background(), r.clock.Add(time.Hour), 100)
	stored := string(r.object(t, uid).Data)
	for _, text := range append([]string{stored}, func() (b []string) {
		for _, m := range due {
			b = append(b, string(m.Body))
		}
		return
	}()...) {
		if strings.Contains(text, "secret-") || strings.Contains(text, kymessagesGuestLine) {
			t.Fatalf("a guest link outside its invitation:\n%s", text)
		}
	}
	if !strings.Contains(stored, "CONFERENCE;FEATURE=VIDEO;LABEL=KyMessages;VALUE=URI:"+roomURL(calls[0].ID)) {
		t.Fatalf("no room:\n%s", stored)
	}
	v := r.view(t, uid)
	if fmt.Sprint(v.Guests) != "[eve@other.example out@elsewhere.example]" || v.GuestsError != "" || strings.Contains(v.Description, "secret") || v.MeetingURL != roomURL(calls[0].ID) {
		t.Fatalf("view %+v", v)
	}

	r.clock = r.clock.Add(3 * time.Hour)
	r.deliver(t)
	outMail, eveMail, annMail := box.to("out@elsewhere.example"), box.to("eve@other.example"), box.to("ann@example.com")
	if len(outMail) == 0 || len(eveMail) == 0 || len(annMail) == 0 {
		t.Fatalf("mail: out %d eve %d ann %d", len(outMail), len(eveMail), len(annMail))
	}
	for _, ics := range outMail {
		if !strings.Contains(ics, kymessagesGuestLine+outURL) || strings.Contains(ics, eveURL) || strings.Contains(ics, "CONFERENCE;FEATURE=VIDEO;LABEL=KyMessages;VALUE=URI:https://chat.example/meet") {
			t.Fatalf("out's invitation:\n%s", ics)
		}
	}
	for _, ics := range eveMail {
		if !strings.Contains(ics, kymessagesGuestLine+eveURL) || strings.Contains(ics, outURL) {
			t.Fatalf("eve's invitation:\n%s", ics)
		}
	}
	for _, ics := range annMail { // a member: the room, never a guest link
		if strings.Contains(ics, "secret-") || strings.Contains(ics, kymessagesGuestLine) {
			t.Fatalf("ann's invitation:\n%s", ics)
		}
	}
	// Dropping a guest drops their key (KyMessages revokes the link); the other keeps theirs.
	r.update(t, uid, map[string]any{"attendees": []string{"ann@example.com", "eve@other.example"}})
	r.sync()
	calls = r.km.seen()
	if last := calls[len(calls)-1].Body; len(last.Guests) != 1 || last.Guests[0].Key != keys["eve@other.example"] {
		t.Fatalf("after removal %+v", last)
	}
}

const kymessagesGuestLine = "Join as a guest: "

// A KyMessages that takes no guests, now or ever, still gets its room: the same PUT without
// guests, the editor names who cannot join, and invitations leave without a guest link.
func TestMeetingWithoutGuestsWhenKyMessagesRefusesThem(t *testing.T) {
	for _, code := range []string{kymessages.CodeGuestsOff, kymessages.CodeGuestsUnsupported} {
		t.Run(code, func(t *testing.T) {
			r, box := guestRig(t, code)
			uid := r.create(t, map[string]any{"meeting": true, "attendees": []string{"ann@example.com", "out@elsewhere.example"}})
			r.sync()
			r.sync()
			calls := r.km.seen()
			if len(calls) < 2 || len(calls[0].Body.Guests) != 1 || len(calls[1].Body.Guests) != 0 || len(calls[1].Body.Windows) != 0 {
				t.Fatalf("calls %+v", calls)
			}
			v := r.view(t, uid)
			if v.GuestsError != code || fmt.Sprint(v.Guests) != "[out@elsewhere.example]" || v.MeetingURL == "" || v.MeetingError != "" {
				t.Fatalf("view %+v", v)
			}
			r.clock = r.clock.Add(3 * time.Hour)
			r.deliver(t)
			mail := box.to("out@elsewhere.example")
			if len(mail) == 0 {
				t.Fatal("the outside attendee was never invited")
			}
			for _, ics := range mail {
				if strings.Contains(ics, kymessagesGuestLine) {
					t.Fatalf("a guest line without a link:\n%s", ics)
				}
			}
			// Asked again once a day, so an administrator switching guests on is picked up.
			n := len(r.km.seen())
			r.clock = r.clock.Add(25 * time.Hour)
			r.s.SyncMeetings(context.Background())
			if len(r.km.seen()) <= n {
				t.Fatal("guests never offered again")
			}
		})
	}
}

// A guest key that gets no link (KyMessages answers only new keys) is replaced, never left
// without one; a recurring meeting's windows cover 90 days and are sent again daily.
func TestGuestKeysAndWindows(t *testing.T) {
	r, _ := guestRig(t, "")
	dropFirst := true
	r.km.answer(func(method, id string) (int, string) {
		calls := r.km.seen()
		last := calls[len(calls)-1].Body
		links := map[string]string{}
		for _, g := range last.Guests {
			if !dropFirst {
				links[g.Key] = guestURL(g.Key)
			}
		}
		dropFirst = false
		b, _ := json.Marshal(map[string]any{"member_url": roomURL(id), "guest_urls": links})
		return 200, string(b)
	})
	r.create(t, map[string]any{"meeting": true, "repeat": map[string]any{"freq": "daily"}, "attendees": []string{"out@elsewhere.example"}})
	r.clock = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC).Add(-2 * time.Hour)
	r.sync()
	r.sync()
	calls := r.km.seen()
	if len(calls) < 2 || calls[0].Body.Guests[0].Key == calls[1].Body.Guests[0].Key {
		t.Fatalf("key not replaced: %+v", calls)
	}
	w := calls[1].Body.Windows
	if len(w) != 90 || w[0].Start != "2026-11-17T08:50:00Z" {
		t.Fatalf("windows %d %+v", len(w), w[:min(len(w), 2)])
	}
	for _, win := range w {
		if s, _ := time.Parse(time.RFC3339, win.Start); s.After(r.clock.AddDate(0, 0, 90)) {
			t.Fatalf("window past 90 days: %+v", win)
		}
	}
	n := len(r.km.seen())
	r.clock = r.clock.Add(25 * time.Hour)
	r.s.SyncMeetings(context.Background())
	if got := r.km.seen(); len(got) != n+1 || got[n].Body.Windows[0].Start == w[0].Start {
		t.Fatalf("windows not refreshed: %d calls", len(got))
	}
}

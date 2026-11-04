package api_test

import (
	"bytes"
	"context"
	"database/sql"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/itip"
	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/emersion/go-ical"
)

func TestSharedInvitationRequiresItsRespondingAttendee(t *testing.T) {
	for _, first := range []string{"bob", "mallory"} {
		for _, occurrence := range []bool{false, true} {
			t.Run(first+map[bool]string{false: "-series", true: "-occurrence"}[occurrence], func(t *testing.T) {
				w := newSchedWorld(t, true)
				ctx := context.Background()
				team := groupCalendar(t, w.st, "Shared invite")
				grantRole(t, w.st, team, "editor", "usr_bob")
				if err := w.st.Groups().AddGroupMember(ctx, "grp_editor_"+team.ID, "usr_mallory"); err != nil {
					t.Fatal(err)
				}
				body := meeting("shared-invite", "alice", "RRULE:FREQ=DAILY;COUNT=3", "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:bob@example.com", "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:mallory@example.com")
				if _, err := davbackend.Write(ctx, w.st, team, "shared.ics", decode(t, body), []byte(body), "", true, store.OwnerLimits{}, nil, ""); err != nil {
					t.Fatal(err)
				}
				deliverInOrder := func() {
					t.Helper()
					due, err := w.st.Calendars().DueOutbox(ctx, time.Now().Add(time.Minute), 100)
					if err != nil {
						t.Fatal(err)
					}
					var held []*store.OutboxMessage
					for _, m := range due {
						if m.Recipient != "mailto:"+first+"@example.com" {
							held = append(held, m)
							if err := w.st.Calendars().FinishOutbox(ctx, m.ID, "pending", time.Now().Add(time.Hour), ""); err != nil {
								t.Fatal(err)
							}
						}
					}
					w.srv.DeliverScheduled(ctx)
					for _, m := range held {
						if err := w.st.Calendars().FinishOutbox(ctx, m.ID, "pending", time.Now().Add(-time.Second), ""); err != nil {
							t.Fatal(err)
						}
					}
					w.deliver()
				}
				if code, _ := w.put("alice", "shared.ics", body, ""); code != 201 {
					t.Fatalf("organizer create: %d", code)
				}
				deliverInOrder()
				_, _, etag := w.get("alice", "shared.ics")
				forged := strings.ReplaceAll(strings.Replace(body, "SUMMARY:Planning", "SUMMARY:Updated", 1), "PARTSTAT=NEEDS-ACTION", "PARTSTAT=ACCEPTED")
				if code, _ := w.put("alice", "shared.ics", forged, etag); code != 201 && code != 204 {
					t.Fatalf("organizer update: %d", code)
				}
				deliverInOrder()
				stored, err := w.st.Calendars().GetObject(ctx, team.ID, "shared.ics")
				if err != nil {
					t.Fatal(err)
				}
				if stored.InvitationSender != "usr_alice" {
					t.Fatalf("organizer forged a billable answer: %+v", stored)
				}
				for _, ev := range decode(t, string(stored.Data)).Children {
					for _, a := range itip.AttendeesOf(ev) {
						if a.Status != "NEEDS-ACTION" {
							t.Fatalf("organizer supplied %s answer for %s", a.Status, a.Address)
						}
					}
				}
				cal := decode(t, string(stored.Data))
				rid := ""
				if occurrence {
					p := ical.NewProp(ical.PropRecurrenceID)
					p.Value = "20261111T090000Z"
					var ok bool
					rid, ok = calendar.OccurrenceKey(cal, p)
					if !ok {
						t.Fatal("no occurrence")
					}
					if _, err := calendar.AddOverride(cal, rid); err != nil {
						t.Fatal(err)
					}
				}
				if err := itip.SetPartstat(cal, rid, "mailto:mallory@example.com", "ACCEPTED"); err != nil {
					t.Fatal(err)
				}
				var raw bytes.Buffer
				if err := ical.NewEncoder(&raw).Encode(cal); err != nil {
					t.Fatal(err)
				}
				res := rawDAV(t, w.ts, "PUT", "/dav/usr_mallory/calendars/_"+team.ID+"/shared.ics", "mallory", w.tokens["mallory"], raw.String(), map[string]string{"Content-Type": "text/calendar", "If-Match": `"` + stored.ETag + `"`})
				if res.StatusCode != 201 && res.StatusCode != 204 {
					t.Fatalf("genuine response: %d %s", res.StatusCode, readAll(res))
				}
				stored, _ = w.st.Calendars().GetObject(ctx, team.ID, "shared.ics")
				if stored.InvitationSender != "" || stored.InvitationRecipient != "usr_mallory" {
					t.Fatalf("response not charged to responding attendee: %+v", stored)
				}
			})
		}
	}
}

func TestQuotaRefusedInviteDoesNotSpendHourlyBytes(t *testing.T) {
	t.Setenv("KYCALENDAR_SCHEDULING", "true")
	srv, st, cfg := setupTestServer(t)
	alice, cal := webUser(t, srv, st, "alice")
	webUser(t, srv, st, "bob")
	driver := "sqlite"
	if cfg.Database.Driver == "postgres" {
		driver = "pgx"
	}
	db, err := sql.Open(driver, cfg.Database.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	q := `INSERT INTO invitation_byte_budget(sender_id,window_start,bytes) VALUES ('usr_alice',?,?)`
	if cfg.Database.Driver == "postgres" {
		q = strings.Replace(q, "?", "$1", 1)
		q = strings.Replace(q, "?", "$2", 1)
	}
	before := store.InviteBytesLimit - 10000
	if _, err := db.Exec(q, time.Now().Unix(), before); err != nil {
		t.Fatal(err)
	}
	cfg.Calendar.MaxBytesPerUser = 50
	path := "/api/calendars/" + cal + "/events"
	if w := call(t, srv, "POST", path, meetingBody, alice); w.Code != http.StatusInsufficientStorage {
		t.Fatalf("quota refusal: %d %s", w.Code, w.Body.String())
	}
	var after int64
	if err := db.QueryRow(`SELECT bytes FROM invitation_byte_budget WHERE sender_id='usr_alice'`).Scan(&after); err != nil || after != before {
		t.Fatalf("failed save spent byte budget: %d -> %d (%v)", before, after, err)
	}
	cfg.Calendar.MaxBytesPerUser = 4000
	if w := call(t, srv, "POST", path, meetingBody, alice); w.Code != http.StatusCreated {
		t.Fatalf("fitting retry: %d %s", w.Code, w.Body.String())
	}
	if _, err := db.Exec(`UPDATE invitation_byte_budget SET bytes=67108864`); err != nil {
		t.Fatal(err)
	}
	if w := call(t, srv, "POST", path, meetingBody, alice); w.Code != http.StatusTooManyRequests || !strings.Contains(w.Body.String(), `"code":"invite_limit"`) || !strings.Contains(w.Body.String(), "again at") {
		t.Fatalf("byte refusal shape: %d %s", w.Code, w.Body.String())
	}
}

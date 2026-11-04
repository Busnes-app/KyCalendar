package api_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/emersion/go-ical"
)

func TestCalDAVRevokedGroupGrantCancellation(t *testing.T) {
	for _, name := range []string{"sender_funded", "recipient_funded", "legacy"} {
		paid, legacy := name == "recipient_funded", name == "legacy"
		t.Run(name, func(t *testing.T) {
			w, db := cancellationWorld(t, 12000, 50000)
			ctx := context.Background()
			team := groupCalendar(t, w.st, "Delivered group copy")
			grantRole(t, w.st, team, "editor", "usr_bob")
			path := "/dav/usr_bob/calendars/_" + team.ID + "/group.ics"
			body := meeting("revoked-copy", "alice", "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:bob@example.com")
			res := rawDAV(t, w.ts, "PUT", path, "bob", w.tokens["bob"], body, map[string]string{"Content-Type": "text/calendar"})
			if res.StatusCode != 201 {
				t.Fatalf("initial group copy: %d %s", res.StatusCode, readAll(res))
			}
			if code, _ := w.put("alice", "source.ics", body, ""); code != 201 {
				t.Fatalf("source admission: %d", code)
			}
			w.deliver()
			copy, err := w.st.Calendars().GetObject(ctx, team.ID, "group.ics")
			if err != nil {
				t.Fatal(err)
			}
			if copy.InvitationSender != "usr_alice" || copy.InvitationRecipient != "usr_bob" {
				t.Fatal("REQUEST did not establish trusted sponsorship")
			}
			if paid {
				answer := strings.ReplaceAll(string(copy.Data), "PARTSTAT=NEEDS-ACTION", "PARTSTAT=ACCEPTED")
				res = rawDAV(t, w.ts, "PUT", path, "bob", w.tokens["bob"], answer, map[string]string{"Content-Type": "text/calendar", "If-Match": `"` + copy.ETag + `"`})
				if res.StatusCode != 201 && res.StatusCode != 204 {
					t.Fatalf("billable answer: %d %s", res.StatusCode, readAll(res))
				}
				w.deliver()
				copy, err = w.st.Calendars().GetObject(ctx, team.ID, "group.ics")
				if err != nil {
					t.Fatal(err)
				}
				if copy.InvitationSender != "" || copy.InvitationRecipient != "usr_bob" {
					t.Fatal("response did not establish Bob's payment")
				}
			}
			if legacy {
				if _, err := db.Exec(`UPDATE calendar_objects SET invitation_sender='',invitation_attendee='',invitation_recipient='' WHERE calendar_id=$1 AND name=$2`, team.ID, copy.Name); err != nil {
					t.Fatal(err)
				}
				copy.InvitationSender, copy.InvitationAttendee, copy.InvitationRecipient = "", "", ""
			}
			if err := w.st.Calendars().DeleteGrant(ctx, team.ID, "grp_editor_"+team.ID); err != nil {
				t.Fatal(err)
			}
			for _, method := range []string{"GET", "PUT", "DELETE"} {
				res = rawDAV(t, w.ts, method, path, "bob", w.tokens["bob"], string(copy.Data), map[string]string{"Content-Type": "text/calendar"})
				if res.StatusCode != 404 {
					t.Fatalf("revoked recipient %s access: %d %s", method, res.StatusCode, readAll(res))
				}
			}
			_, _, etag := w.get("alice", "source.ics")
			res = rawDAV(t, w.ts, "DELETE", "/dav/usr_alice/calendars/default/source.ics", "alice", w.tokens["alice"], "", map[string]string{"If-Match": etag})
			if res.StatusCode != 204 {
				t.Fatalf("source cancellation: %d %s", res.StatusCode, readAll(res))
			}
			w.deliver()
			var objects int
			var chargedBytes int64
			if err := db.QueryRow(`SELECT COUNT(*),COALESCE(SUM(LENGTH(data)),0) FROM calendar_objects WHERE invitation_sender='usr_alice'`).Scan(&objects, &chargedBytes); err != nil {
				t.Fatal(err)
			}
			if objects != 0 || chargedBytes != 0 {
				t.Errorf("revocation stranded sender quota: objects=%d bytes=%d", objects, chargedBytes)
			}
			got, err := w.st.Calendars().GetObject(ctx, team.ID, "group.ics")
			if !paid && !legacy {
				if !errors.Is(err, store.ErrNotFound) {
					t.Fatalf("sender-funded copy survived grant loss and cancellation: %v", err)
				}
				return
			}
			if err != nil || got.ETag != copy.ETag || got.InvitationSender != copy.InvitationSender || got.InvitationRecipient != copy.InvitationRecipient {
				t.Fatal("cleanup changed inaccessible paid/legacy content")
			}
		})
	}
}

func seedSponsoredGroup(t *testing.T, w *schedWorld, body string) *store.Calendar {
	t.Helper()
	team := groupCalendar(t, w.st, "Sponsored copy")
	grantRole(t, w.st, team, "editor", "usr_bob")
	res := rawDAV(t, w.ts, "PUT", "/dav/usr_bob/calendars/_"+team.ID+"/group.ics", "bob", w.tokens["bob"], body, map[string]string{"Content-Type": "text/calendar"})
	if res.StatusCode != 201 {
		t.Fatalf("seed CalDAV group copy: %d %s", res.StatusCode, readAll(res))
	}
	return team
}

func TestRevokedSponsoredCancellationIdentityScopes(t *testing.T) {
	for _, kind := range []string{"organizer", "recipient", "uid"} {
		t.Run(kind, func(t *testing.T) {
			w, _ := cancellationWorld(t, 12000, 50000)
			ctx := context.Background()
			body := meeting("scoped-copy", "alice", "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:bob@example.com")
			team := seedSponsoredGroup(t, w, body)
			if code, _ := w.put("alice", "source.ics", body, ""); code != 201 {
				t.Fatalf("source: %d", code)
			}
			w.deliver()
			original, err := w.st.Calendars().GetObject(ctx, team.ID, "group.ics")
			if err != nil {
				t.Fatal(err)
			}
			if err := w.st.Calendars().DeleteGrant(ctx, team.ID, "grp_editor_"+team.ID); err != nil {
				t.Fatal(err)
			}
			writer, uid, guest, slug := "alice", "scoped-copy", "bob", "default"
			switch kind {
			case "organizer":
				writer = "carol"
			case "recipient":
				guest, slug = "carol", "other"
				if err := w.st.Calendars().CreateCalendar(ctx, &store.Calendar{ID: "cal_other", OwnerKind: "user", OwnerID: "usr_alice", Slug: slug, Name: "Other"}, 0); err != nil {
					t.Fatal(err)
				}
			case "uid":
				uid = "other-copy"
			}
			other := meeting(uid, writer, "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:"+guest+"@example.com")
			path := "/dav/usr_" + writer + "/calendars/" + slug + "/other.ics"
			res := rawDAV(t, w.ts, "PUT", path, writer, w.tokens[writer], other, map[string]string{"Content-Type": "text/calendar"})
			if res.StatusCode != 201 {
				t.Fatalf("other admission: %d %s", res.StatusCode, readAll(res))
			}
			w.deliver()
			res = rawDAV(t, w.ts, "DELETE", path, writer, w.tokens[writer], "", nil)
			if res.StatusCode != 204 {
				t.Fatalf("other cancellation: %d %s", res.StatusCode, readAll(res))
			}
			w.deliver()
			got, err := w.st.Calendars().GetObject(ctx, team.ID, "group.ics")
			if err != nil || got.ETag != original.ETag || got.InvitationSender != "usr_alice" || got.InvitationRecipient != "usr_bob" {
				t.Fatal("mismatched cancellation changed unrelated sponsorship")
			}
		})
	}
}

func TestSponsoredCancellationRechecksPaidSameETag(t *testing.T) {
	w, _ := cancellationWorld(t, 12000, 50000)
	ctx := context.Background()
	body := meeting("paid-race", "alice", "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:bob@example.com")
	team := seedSponsoredGroup(t, w, body)
	if code, _ := w.put("alice", "source.ics", body, ""); code != 201 {
		t.Fatalf("source: %d", code)
	}
	w.deliver()
	res := rawDAV(t, w.ts, "DELETE", "/dav/usr_alice/calendars/default/source.ics", "alice", w.tokens["alice"], "", nil)
	if res.StatusCode != 204 {
		t.Fatalf("cancel: %d", res.StatusCode)
	}
	due, err := w.st.Calendars().DueOutbox(ctx, time.Now().Add(time.Minute), 100)
	if err != nil || len(due) != 1 {
		t.Fatalf("pending CANCEL: %v %d", err, len(due))
	}
	if err := w.st.Calendars().ApplyInternalCancellation(ctx, due[0].ID, "usr_carol", nil, store.OwnerLimits{}); !errors.Is(err, store.ErrPreconditionFailed) {
		t.Fatalf("wrong recipient acknowledged an empty cancellation batch: %v", err)
	}
	copies, err := w.st.Calendars().SponsoredCancellationCopies(ctx, due[0].ID, "usr_bob", "paid-race")
	if err != nil || len(copies) != 1 {
		t.Fatalf("trusted cancellation lookup: %v %d", err, len(copies))
	}
	old := copies[0]
	path := "/dav/usr_bob/calendars/_" + team.ID + "/group.ics"
	answer := strings.ReplaceAll(string(old.Data), "PARTSTAT=NEEDS-ACTION", "PARTSTAT=ACCEPTED")
	for _, data := range []string{answer, string(old.Data)} {
		res = rawDAV(t, w.ts, "PUT", path, "bob", w.tokens["bob"], data, map[string]string{"Content-Type": "text/calendar"})
		if res.StatusCode != 201 && res.StatusCode != 204 {
			t.Fatalf("answer/restore: %d %s", res.StatusCode, readAll(res))
		}
	}
	paid, err := w.st.Calendars().GetObject(ctx, team.ID, "group.ics")
	if err != nil || paid.ETag != old.ETag || paid.InvitationSender != "" || paid.InvitationRecipient != "usr_bob" {
		t.Fatal("did not establish paid copy with original ETag")
	}
	if err := w.st.Calendars().DeleteGrant(ctx, team.ID, "grp_editor_"+team.ID); err != nil {
		t.Fatal(err)
	}
	err = w.st.Calendars().ApplyInternalCancellation(ctx, due[0].ID, "usr_bob", []store.CancellationChange{{Previous: old}}, store.OwnerLimits{})
	if !errors.Is(err, store.ErrPreconditionFailed) {
		t.Fatalf("stale sponsorship authorized deletion: %v", err)
	}
	w.deliver()
	got, err := w.st.Calendars().GetObject(ctx, team.ID, "group.ics")
	if err != nil || got.ETag != paid.ETag || got.InvitationSender != "" || got.InvitationRecipient != "usr_bob" {
		t.Fatal("cleanup reclaimed newly paid content")
	}
}

func TestRevokedSponsoredPartialCancellationBudgetAndRollback(t *testing.T) {
	const cap int64 = 12000
	w, db := cancellationWorld(t, cap, 50000)
	ctx := context.Background()
	body := meeting("partial-budget", "alice", "RRULE:FREQ=DAILY;COUNT=3", "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:bob@example.com")
	first := seedSponsoredGroup(t, w, body)
	if code, _ := w.put("alice", "source.ics", body, ""); code != 201 {
		t.Fatalf("source: %d", code)
	}
	w.deliver()
	if err := w.st.Calendars().DeleteGrant(ctx, first.ID, "grp_editor_"+first.ID); err != nil {
		t.Fatal(err)
	}
	second := seedSponsoredGroup(t, w, body)
	_, _, etag := w.get("alice", "source.ics")
	body = strings.Replace(body, "SUMMARY:Planning", "SUMMARY:Updated", 1)
	if code, _ := w.put("alice", "source.ics", body, etag); code != 201 && code != 204 {
		t.Fatalf("update source: %d", code)
	}
	w.deliver()
	if err := w.st.Calendars().DeleteGrant(ctx, second.ID, "grp_editor_"+second.ID); err != nil {
		t.Fatal(err)
	}
	_, _, etag = w.get("alice", "source.ics")
	// A source now containing only this occurrence sends an occurrence-only REQUEST,
	// then an occurrence-only CANCEL on deletion. The older inaccessible series stay.
	single := meeting("partial-budget", "alice", "RECURRENCE-ID:20261111T090000Z", "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:bob@example.com")
	single = strings.ReplaceAll(single, "20261110T", "20261111T")
	if code, _ := w.put("alice", "source.ics", single, etag); code != 201 && code != 204 {
		t.Fatalf("occurrence-only source: %d", code)
	}
	w.deliver()
	cancel := rawDAV(t, w.ts, "DELETE", "/dav/usr_alice/calendars/default/source.ics", "alice", w.tokens["alice"], "", nil)
	if cancel.StatusCode != 204 {
		t.Fatalf("queue occurrence-only CANCEL: %d %s", cancel.StatusCode, readAll(cancel))
	}
	used, _, _ := reservationUsage(t, db)
	base := meeting("fill", "alice")
	padding := int(cap-used) - len(base) - len("DESCRIPTION:\r\n")
	if padding < 0 {
		t.Fatalf("no ordinary budget: %d", used)
	}
	filler := strings.Replace(base, "END:VEVENT", "DESCRIPTION:"+strings.Repeat("f", padding)+"\r\nEND:VEVENT", 1)
	if code, _ := w.put("alice", "fill.ics", filler, ""); code != 201 {
		t.Fatalf("fill usable quota: %d", code)
	}
	before, reserved, count := reservationUsage(t, db)
	if before != cap {
		t.Fatalf("ordinary budget not full: %d", before)
	}
	due, err := w.st.Calendars().DueOutbox(ctx, time.Now().Add(time.Minute), 100)
	if err != nil || len(due) != 1 {
		t.Fatalf("partial CANCEL: %v %d", err, len(due))
	}
	copies, err := w.st.Calendars().SponsoredCancellationCopies(ctx, due[0].ID, "usr_bob", "partial-budget")
	if err != nil || len(copies) != 3 {
		t.Fatalf("expected two inaccessible series and one new occurrence copy: %v %d", err, len(copies))
	}
	var changes []store.CancellationChange
	masters := 0
	for _, old := range copies {
		cal := decode(t, string(old.Data))
		if _, err := calendar.Master(cal); err != nil {
			changes = append(changes, store.CancellationChange{Previous: old})
			continue
		}
		masters++
		if err := calendar.DeleteOne(cal, "20261111T090000Z", time.Now()); err != nil {
			t.Fatal(err)
		}
		var raw bytes.Buffer
		if err := ical.NewEncoder(&raw).Encode(cal); err != nil {
			t.Fatal(err)
		}
		data := raw.Bytes()
		if masters == 2 {
			data = []byte(strings.Replace(raw.String(), "END:VEVENT", "DESCRIPTION:"+strings.Repeat("x", 12000)+"\r\nEND:VEVENT", 1))
		}
		next, err := davbackend.Prepare(old.CalendarID, old.Name, decode(t, string(data)), data)
		if err != nil {
			t.Fatal(err)
		}
		changes = append(changes, store.CancellationChange{Previous: old, Next: next})
	}
	err = w.st.Calendars().ApplyInternalCancellation(ctx, due[0].ID, "usr_bob", changes, store.OwnerLimits{MaxBytes: cap, MaxTotalBytes: 50000})
	if !errors.Is(err, store.ErrQuotaExceeded) {
		t.Fatalf("over-budget cancellation batch: %v", err)
	}
	after, afterReserved, afterCount := reservationUsage(t, db)
	if after != before || afterReserved != reserved || afterCount != count {
		t.Fatal("failed batch changed receipt/storage/reservations")
	}
	for _, old := range copies {
		got, err := w.st.Calendars().GetObject(ctx, old.CalendarID, old.Name)
		if err != nil || got.ETag != old.ETag || got.InvitationSender != "usr_alice" {
			t.Fatal("failed batch partially applied")
		}
	}
	w.deliver()
	for _, old := range copies {
		got, err := w.st.Calendars().GetObject(ctx, old.CalendarID, old.Name)
		if _, masterErr := calendar.Master(decode(t, string(old.Data))); masterErr != nil {
			if !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("last occurrence copy survived: %v", err)
			}
			continue
		}
		if err != nil || !strings.Contains(string(got.Data), "EXDATE:20261111T090000Z") || strings.Contains(string(got.Data), "STATUS:CANCELLED") || got.InvitationSender != "usr_alice" {
			t.Fatalf("partial cancellation did not preserve series semantics: %v", err)
		}
	}
	if used, reserved, count := reservationUsage(t, db); used > cap || reserved != 0 || count != 0 {
		t.Fatalf("cancellation exceeded bound or retained source reservation: %d %d %d", used, reserved, count)
	}
}

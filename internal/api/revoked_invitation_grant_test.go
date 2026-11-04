package api_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/itip"
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
	fillCancellationUsableQuota(t, w, cap)
	before, reserved, count := reservationUsage(t, db)
	if before >= cap {
		t.Fatalf("copy growth was not reserved: used=%d", before)
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

func TestSponsoredCancellationIgnoresNonapplicablePaidFallback(t *testing.T) {
	for _, kind := range []string{"stale", "no_occurrence"} {
		t.Run(kind, func(t *testing.T) {
			w, db := cancellationWorld(t, 12000, 50000)
			ctx := context.Background()
			body := meeting("paid-fallback", "alice", "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:bob@example.com")
			if kind == "no_occurrence" {
				body = meeting("paid-fallback", "alice", "RECURRENCE-ID:20261111T090000Z", "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:bob@example.com")
				body = strings.ReplaceAll(body, "20261110T", "20261111T")
			}
			team := seedSponsoredGroup(t, w, body)
			if code, _ := w.put("alice", "source.ics", body, ""); code != 201 {
				t.Fatalf("source: %d", code)
			}
			w.deliver()
			if err := w.st.Calendars().DeleteGrant(ctx, team.ID, "grp_editor_"+team.ID); err != nil {
				t.Fatal(err)
			}
			_, _, etag := w.get("alice", "source.ics")
			body = strings.Replace(body, "SUMMARY:Planning", "SUMMARY:Updated", 1)
			if code, _ := w.put("alice", "source.ics", body, etag); code != 201 && code != 204 {
				t.Fatalf("update: %d", code)
			}
			w.deliver()
			_, personal := w.copyOf("bob", "paid-fallback")
			if personal == nil {
				t.Fatal("no personal copy after REQUEST")
			}
			rewritten := strings.ReplaceAll(string(personal.Data), "X-KYCALENDAR-ORGANIZER-SEQUENCE:0", "X-KYCALENDAR-ORGANIZER-SEQUENCE:9")
			if kind == "no_occurrence" {
				rewritten = strings.ReplaceAll(string(personal.Data), "20261111T", "20261112T")
			}
			if rewritten == string(personal.Data) {
				t.Fatal("fixture did not replace binding")
			}
			if code, _ := w.put("bob", personal.Name, rewritten, `"`+personal.ETag+`"`); code != 201 && code != 204 {
				t.Fatalf("paid replacement: %d", code)
			}
			_, paid := w.copyOf("bob", "paid-fallback")
			if paid.InvitationSender != "" || paid.InvitationRecipient != "usr_bob" {
				t.Fatal("replacement not charged to writer")
			}
			res := rawDAV(t, w.ts, "DELETE", "/dav/usr_alice/calendars/default/source.ics", "alice", w.tokens["alice"], "", nil)
			if res.StatusCode != 204 {
				t.Fatalf("cancel source: %d", res.StatusCode)
			}
			w.deliver()
			if _, err := w.st.Calendars().GetObject(ctx, team.ID, "group.ics"); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("paid fallback vetoed sponsored cleanup: %v", err)
			}
			_, got := w.copyOf("bob", "paid-fallback")
			if !bytes.Equal(got.Data, paid.Data) || got.ETag != paid.ETag || got.InvitationSender != "" || got.InvitationRecipient != "usr_bob" {
				t.Fatal("nonapplicable paid copy changed")
			}
			var objects int
			var used int64
			if err := db.QueryRow(`SELECT COUNT(*),COALESCE(SUM(LENGTH(data)),0) FROM calendar_objects WHERE invitation_sender='usr_alice'`).Scan(&objects, &used); err != nil {
				t.Fatal(err)
			}
			if objects != 0 || used != 0 {
				t.Fatalf("sender charge stranded: %d objects/%d bytes", objects, used)
			}
			var failed int
			if err := db.QueryRow(`SELECT COUNT(*) FROM scheduling_outbox WHERE method='CANCEL' AND status<>'delivered'`).Scan(&failed); err != nil {
				t.Fatal(err)
			}
			if failed != 0 {
				t.Fatalf("CANCEL discarded instead of applying valid cleanup: %d", failed)
			}
		})
	}
}

func TestRevokedSponsoredAggregatePartialCancellationCapacity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cap    int64
		copies int
		fanout bool
	}{
		{"64_copies_one_recipient", 60000, 64, false},
		{"three_recipients", 12000, 3, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cap := tc.cap
			w, db := cancellationWorld(t, cap, cap)
			ctx := context.Background()
			original := meeting("aggregate-partial", "alice", "RRULE:FREQ=DAILY;COUNT=3", "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:bob@example.com")
			if tc.fanout {
				original = strings.Replace(original, "END:VEVENT", "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:carol@example.com\r\nATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:dan@example.com\r\nEND:VEVENT", 1)
			}
			var teams []*store.Calendar
			for i := 0; i < tc.copies; i++ {
				team := seedSponsoredGroup(t, w, original)
				teams = append(teams, team)
				body := strings.Replace(original, "SUMMARY:Planning", fmt.Sprintf("SUMMARY:Round %d", i), 1)
				etag := ""
				if i > 0 {
					_, _, etag = w.get("alice", "source.ics")
				}
				if code, _ := w.put("alice", "source.ics", body, etag); code != 201 && code != 204 {
					t.Fatalf("source admission %d: %d", i, code)
				}
				w.deliver()
				copy, err := w.st.Calendars().GetObject(ctx, team.ID, "group.ics")
				if err != nil || copy.InvitationSender != "usr_alice" {
					t.Fatalf("copy %d not trusted: %v", i, err)
				}
				if err := w.st.Calendars().DeleteGrant(ctx, team.ID, "grp_editor_"+team.ID); err != nil {
					t.Fatal(err)
				}
			}
			_, _, etag := w.get("alice", "source.ics")
			single := meeting("aggregate-partial", "alice", "RECURRENCE-ID:20261111T090000Z", "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:bob@example.com")
			single = strings.ReplaceAll(single, "20261110T", "20261111T")
			if tc.fanout {
				single = strings.Replace(single, "END:VEVENT", "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:carol@example.com\r\nATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:dan@example.com\r\nEND:VEVENT", 1)
			}
			// At the old ordinary limit the new multi-copy growth must refuse admission atomically.
			fillCancellationUsableQuota(t, w, cap)
			_, oldReserved, oldCount := reservationUsage(t, db)
			if code, _ := w.put("alice", "source.ics", single, etag); code != 507 {
				t.Fatalf("growth liability not admitted atomically: %d", code)
			}
			_, _, sameETag := w.get("alice", "source.ics")
			if sameETag != etag {
				t.Fatal("refused growth update changed source")
			}
			if _, r, n := reservationUsage(t, db); r != oldReserved || n != oldCount {
				t.Fatal("refused growth update changed source reservation")
			}
			res := rawDAV(t, w.ts, "DELETE", "/dav/usr_alice/calendars/default/fill.ics", "alice", w.tokens["alice"], "", nil)
			if res.StatusCode != 204 {
				t.Fatalf("free ordinary data: %d", res.StatusCode)
			}
			if code, _ := w.put("alice", "source.ics", single, etag); code != 201 && code != 204 {
				t.Fatalf("occurrence source: %d", code)
			}
			w.deliver()
			fillCancellationUsableQuota(t, w, cap)
			used, reserved, _ := reservationUsage(t, db)
			if used >= cap {
				t.Fatalf("copy growth capacity missing: %d", used)
			}
			t.Logf("admitted %d trusted inaccessible series; ordinary quota full before source deletion; reserved=%d", len(teams), reserved)
			res = rawDAV(t, w.ts, "DELETE", "/dav/usr_alice/calendars/default/source.ics", "alice", w.tokens["alice"], "", nil)
			if res.StatusCode != 204 {
				t.Fatalf("admitted source cannot cancel: %d %s", res.StatusCode, readAll(res))
			}
			// Source removal must keep each pending copy's growth protected from ordinary writes.
			fillCancellationUsableQuota(t, w, cap)
			w.srv.DeliverScheduled(ctx)
			pending, err := w.st.Calendars().DueOutbox(ctx, time.Now().Add(48*time.Hour), 100)
			if err != nil {
				t.Fatal(err)
			}
			if len(pending) != 0 {
				used, reserved, count := reservationUsage(t, db)
				t.Fatalf("admitted partial cancellation stranded: pending=%d reason=%s used=%d reserved=%d count=%d", len(pending), pending[0].LastError, used, reserved, count)
			}
			for _, team := range teams {
				got, err := w.st.Calendars().GetObject(ctx, team.ID, "group.ics")
				if err != nil || !strings.Contains(string(got.Data), "EXDATE:20261111T090000Z") || strings.Contains(string(got.Data), "STATUS:CANCELLED") || got.InvitationSender != "usr_alice" {
					t.Fatalf("partial cleanup did not preserve unrelated series: %v", err)
				}
			}
			if used, reserved, count := reservationUsage(t, db); used > cap || reserved != 0 || count != 0 {
				t.Fatalf("post-cancellation bounds/reservation: %d %d %d", used, reserved, count)
			}
		})
	}
}

// Fill through the public quota boundary: physical bytes alone exclude reserved growth.
func fillCancellationUsableQuota(t *testing.T, w *schedWorld, cap int64) {
	t.Helper()
	base := meeting("fill", "alice")
	low, high := 0, int(cap)
	for low < high {
		padding := (low + high + 1) / 2
		body := strings.Replace(base, "END:VEVENT", "DESCRIPTION:"+strings.Repeat("f", padding)+"\r\nEND:VEVENT", 1)
		code, _ := w.put("alice", "fill.ics", body, "")
		switch code {
		case 201, 204:
			low = padding
		case 507:
			high = padding - 1
		default:
			t.Fatalf("fill ordinary quota: %d", code)
		}
	}
	body := strings.Replace(base, "END:VEVENT", "DESCRIPTION:"+strings.Repeat("f", low)+"\r\nEND:VEVENT", 1)
	if code, _ := w.put("alice", "fill.ics", body, ""); code != 201 && code != 204 {
		t.Fatalf("ordinary boundary changed: %d", code)
	}
	body = strings.Replace(body, "DESCRIPTION:", "DESCRIPTION:f", 1)
	if code, _ := w.put("alice", "fill.ics", body, ""); code != 507 {
		t.Fatalf("ordinary write spent cancellation capacity: %d", code)
	}
}

func TestSponsoredCopyAdmissionReservesPendingCancellationGrowth(t *testing.T) {
	const cap int64 = 12000
	w, db := cancellationWorld(t, cap, cap)
	ctx := context.Background()
	series := meeting("copy-admission", "alice", "RRULE:FREQ=DAILY;COUNT=3", "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:bob@example.com")
	oldTeam := seedSponsoredGroup(t, w, series)
	if code, _ := w.put("alice", "source.ics", series, ""); code != 201 {
		t.Fatalf("source: %d", code)
	}
	w.deliver()
	if err := w.st.Calendars().DeleteGrant(ctx, oldTeam.ID, "grp_editor_"+oldTeam.ID); err != nil {
		t.Fatal(err)
	}
	_, _, etag := w.get("alice", "source.ics")
	occurrence := meeting("copy-admission", "alice", "RECURRENCE-ID:20261111T090000Z", "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:bob@example.com")
	occurrence = strings.ReplaceAll(occurrence, "20261110T", "20261111T")
	if code, _ := w.put("alice", "source.ics", occurrence, etag); code != 201 && code != 204 {
		t.Fatalf("partial source: %d", code)
	}
	w.deliver()
	res := rawDAV(t, w.ts, "DELETE", "/dav/usr_alice/calendars/default/source.ics", "alice", w.tokens["alice"], "", nil)
	if res.StatusCode != 204 {
		t.Fatalf("pending partial cancel: %d", res.StatusCode)
	}
	if code, _ := w.put("alice", "next.ics", strings.Replace(series, "SUMMARY:Planning", "SUMMARY:New planning", 1), ""); code != 201 {
		t.Fatalf("next source: %d", code)
	}
	due, err := w.st.Calendars().DueOutbox(ctx, time.Now().Add(time.Minute), 100)
	if err != nil {
		t.Fatal(err)
	}
	var request *store.OutboxMessage
	for _, m := range due {
		if m.Method == itip.MethodRequest {
			request = m
		}
	}
	if request == nil {
		t.Fatal("no pending REQUEST")
	}
	team := groupCalendar(t, w.st, "Pending growth admission")
	msg, err := itip.Decode(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	cal, err := itip.AttendeeCopy(msg, nil, itip.Address("mailto:bob@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range cal.Children {
		if ev.Name == ical.CompEvent {
			ev.Props.SetText("X-KYCALENDAR-ORGANIZER-SEQUENCE", "0")
		}
	}
	var encoded bytes.Buffer
	if err := ical.NewEncoder(&encoded).Encode(cal); err != nil {
		t.Fatal(err)
	}
	incoming, err := davbackend.Prepare(team.ID, "incoming.ics", cal, encoded.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	incoming.DeliveryID, incoming.DeliveryRecipient = request.ID, "usr_bob"
	fillCancellationUsableQuota(t, w, cap)
	before, reserved, count := reservationUsage(t, db)
	_, err = w.st.Calendars().PutObject(ctx, incoming, "", true, store.OwnerLimits{MaxBytes: cap, MaxTotalBytes: cap})
	if !errors.Is(err, store.ErrQuotaExceeded) {
		t.Fatalf("copy admitted without its pending growth: %v", err)
	}
	if _, err := w.st.Calendars().GetObject(ctx, team.ID, "incoming.ics"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("refused copy persisted: %v", err)
	}
	if after, r, n := reservationUsage(t, db); after != before || r != reserved || n != count {
		t.Fatal("refused copy consumed pending body or source reservation")
	}
	res = rawDAV(t, w.ts, "DELETE", "/dav/usr_alice/calendars/default/fill.ics", "alice", w.tokens["alice"], "", nil)
	if res.StatusCode != 204 {
		t.Fatalf("free ordinary capacity: %d", res.StatusCode)
	}
	if _, err := w.st.Calendars().PutObject(ctx, incoming, "", true, store.OwnerLimits{MaxBytes: cap, MaxTotalBytes: cap}); err != nil {
		t.Fatalf("copy with its liability: %v", err)
	}
	stored, err := w.st.Calendars().GetObject(ctx, team.ID, "incoming.ics")
	if err != nil || stored.InvitationSender != "usr_alice" {
		t.Fatalf("trusted copy missing: %v", err)
	}
	fillCancellationUsableQuota(t, w, cap)
	// The pending occurrence CANCEL reaches the newly admitted series too, without a grant.
	w.deliver()
	got, err := w.st.Calendars().GetObject(ctx, team.ID, "incoming.ics")
	if err != nil || !strings.Contains(string(got.Data), "EXDATE:20261111T090000Z") {
		t.Fatalf("new copy lacked cancellation capacity: %v", err)
	}
	stable := got.ETag
	w.deliver()
	got, err = w.st.Calendars().GetObject(ctx, team.ID, "incoming.ics")
	if err != nil || got.ETag != stable {
		t.Fatal("repeat delivery changed the copy or consumed capacity twice")
	}
	// Physical deletion releases both charged data and any still-live copy liability.
	before, _, _ = reservationUsage(t, db)
	if err := w.st.Calendars().DeleteObject(ctx, team.ID, "incoming.ics", stable); err != nil {
		t.Fatal(err)
	}
	after, _, _ := reservationUsage(t, db)
	if before-after != int64(len(got.Data)) {
		t.Fatal("copy deletion did not reclaim charged bytes")
	}
	fillCancellationUsableQuota(t, w, cap)
}

func TestSponsoredOverlappingCancellationGrowth(t *testing.T) {
	for _, order := range []string{"forward", "reverse"} {
		t.Run(order, func(t *testing.T) {
			const cap int64 = 200000
			const count = 24
			w, db := cancellationWorld(t, cap, cap)
			ctx := context.Background()
			original := decode(t, meeting("overlap", "alice", "RRULE:FREQ=DAILY;COUNT=60", "LAST-MODIFIED:20261101T000000Z", "SEQUENCE:100", "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:bob@example.com"))
			override, err := calendar.AddOverride(original, "20261111T090000Z")
			if err != nil {
				t.Fatal(err)
			}
			override.Props.SetText(ical.PropSequence, "100")
			var encoded bytes.Buffer
			if err := ical.NewEncoder(&encoded).Encode(original); err != nil {
				t.Fatal(err)
			}
			body := encoded.String()
			var teams []*store.Calendar
			for i := 0; i < 16; i++ {
				team := seedSponsoredGroup(t, w, body)
				teams = append(teams, team)
				next := strings.Replace(body, "SUMMARY:Planning", fmt.Sprintf("SUMMARY:Round %d", i), 1)
				etag := ""
				if i > 0 {
					_, _, etag = w.get("alice", "source.ics")
				}
				if code, _ := w.put("alice", "source.ics", next, etag); code != 201 && code != 204 {
					t.Fatalf("source %d: %d", i, code)
				}
				w.deliver()
				if err := w.st.Calendars().DeleteGrant(ctx, team.ID, "grp_editor_"+team.ID); err != nil {
					t.Fatal(err)
				}
			}
			var paths []string
			for i := 0; i < count; i++ {
				pair := ical.NewCalendar()
				pair.Props.SetText(ical.PropVersion, "2.0")
				pair.Props.SetText(ical.PropProductID, "-//Busnes.app//Overlap test//EN")
				for _, day := range []time.Time{time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 12+i, 0, 0, 0, 0, time.UTC)} {
					raw := meeting("overlap", "alice", "RECURRENCE-ID:"+day.Format("20060102")+"T090000Z", "LAST-MODIFIED:20261101T000000Z", "SEQUENCE:100", "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:bob@example.com")
					raw = strings.ReplaceAll(raw, "20261110T", day.Format("20060102")+"T")
					pair.Children = append(pair.Children, decode(t, raw).Children...)
				}
				encoded.Reset()
				if err := ical.NewEncoder(&encoded).Encode(pair); err != nil {
					t.Fatal(err)
				}
				slug := fmt.Sprintf("notices-%02d", i)
				cal := &store.Calendar{ID: slug, OwnerKind: "user", OwnerID: "usr_alice", Slug: slug, Name: slug}
				if err := w.st.Calendars().CreateCalendar(ctx, cal, 50); err != nil {
					t.Fatal(err)
				}
				path := "/dav/usr_alice/calendars/" + slug + "/notice.ics"
				paths = append(paths, path)
				res := rawDAV(t, w.ts, "PUT", path, "alice", w.tokens["alice"], encoded.String(), map[string]string{"Content-Type": "text/calendar"})
				if res.StatusCode != 201 {
					t.Fatalf("overlap source %d: %d %s", i, res.StatusCode, readAll(res))
				}
				w.deliver()
			}
			// All future liabilities must already fit before deletion, with ordinary space full.
			fillCancellationUsableQuota(t, w, cap)
			for n := range paths {
				i := n
				if order == "reverse" {
					i = len(paths) - 1 - n
				}
				res := rawDAV(t, w.ts, "DELETE", paths[i], "alice", w.tokens["alice"], "", nil)
				if res.StatusCode != 204 {
					t.Fatalf("admitted cancellation %d: %d %s", i, res.StatusCode, readAll(res))
				}
			}
			fillCancellationUsableQuota(t, w, cap)
			before, _, _ := reservationUsage(t, db)
			t.Logf("%d physical copies / %d distinct overlapping notices; full usable quota, physical+body+source=%d", len(teams), count, before)
			// Each bounded delivery batch must make progress at the admitted full boundary.
			for batch := 0; batch < count; batch++ {
				due, err := w.st.Calendars().DueOutbox(ctx, time.Now().Add(48*time.Hour), 100)
				if err != nil {
					t.Fatal(err)
				}
				if len(due) == 0 {
					break
				}
				w.srv.DeliverScheduled(ctx)
				remaining, err := w.st.Calendars().DueOutbox(ctx, time.Now().Add(48*time.Hour), 100)
				if err != nil {
					t.Fatal(err)
				}
				if len(remaining) >= len(due) {
					t.Fatalf("overlap cancellation stalled: before=%d after=%d reason=%s", len(due), len(remaining), remaining[0].LastError)
				}
			}
			w.deliver()
			for _, team := range teams {
				got, err := w.st.Calendars().GetObject(ctx, team.ID, "group.ics")
				if err != nil {
					t.Fatal(err)
				}
				text := string(got.Data)
				if strings.Contains(text, "RECURRENCE-ID:20261111T090000Z") || !strings.Contains(text, "EXDATE:20261111T090000Z") || strings.Contains(text, "STATUS:CANCELLED") || got.InvitationSender != "usr_alice" {
					t.Fatal("overlapping cleanup changed unrelated series or retained shared override")
				}
				for i := 0; i < count; i++ {
					day := time.Date(2026, 11, 12+i, 0, 0, 0, 0, time.UTC)
					if !strings.Contains(text, "EXDATE:"+day.Format("20060102")+"T090000Z") {
						t.Fatalf("missing cancellation %d", i)
					}
				}
			}
			if used, _, _ := reservationUsage(t, db); used > cap {
				t.Fatalf("storage exceeded hard cap: %d", used)
			}
		})
	}
}

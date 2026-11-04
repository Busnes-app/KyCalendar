package api_test

import (
	"strings"
	"testing"
)

func TestCalDAVCancellationUsesTrustedCopyOwnership(t *testing.T) {
	for _, occurrenceOnly := range []bool{false, true} {
		for _, tc := range []struct {
			name, status             string
			keep, writerPaid, legacy bool
		}{
			{name: "unsupported_completed", status: "COMPLETED"},
			{name: "unsupported_in_process", status: "IN-PROCESS"},
			{name: "unsupported_extension", status: "X-MAYBE"},
			{name: "accepted", status: "ACCEPTED", keep: true},
			{name: "declined", status: "DECLINED", keep: true},
			{name: "tentative", status: "TENTATIVE", keep: true},
			{name: "delegated", status: "DELEGATED", keep: true},
			{name: "writer_paid", status: "NEEDS-ACTION", keep: true, writerPaid: true},
			{name: "untracked_legacy", status: "X-MAYBE", keep: true, legacy: true},
		} {
			if occurrenceOnly && tc.writerPaid {
				continue // Paid copies keep the existing per-occurrence answer/removal rules.
			}
			name := tc.name
			if occurrenceOnly {
				name += "_occurrence_only"
			}
			t.Run(name, func(t *testing.T) {
				w, db := cancellationWorld(t, 12000, 50000)
				extra := []string{"ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:bob@example.com"}
				if occurrenceOnly {
					extra = append(extra, "RECURRENCE-ID:20261110T090000Z")
				}
				body := meeting("trusted-cancel", "alice", extra...)
				if code, _ := w.put("alice", "source.ics", body, ""); code != 201 {
					t.Fatalf("admit invitation: %d", code)
				}
				w.deliver()
				original, copy := w.copyOf("bob", "trusted-cancel")
				if copy == nil || copy.InvitationSender != "usr_alice" {
					t.Fatal("missing sponsored invitation")
				}
				if tc.legacy {
					// Model a pre-migration row without inventing trusted response history.
					if _, err := db.Exec(`UPDATE calendar_objects SET invitation_sender='',invitation_attendee='',invitation_recipient='' WHERE calendar_id=$1 AND name=$2`, copy.CalendarID, copy.Name); err != nil {
						t.Fatal(err)
					}
				}
				if !tc.keep {
					base := meeting("full-recipient", "bob")
					filler := strings.Replace(base, "END:VEVENT", "DESCRIPTION:"+strings.Repeat("f", 12000-len(base)-len("DESCRIPTION:\r\n"))+"\r\nEND:VEVENT", 1)
					if code, _ := w.put("bob", "full.ics", filler, ""); code != 201 {
						t.Fatalf("fill recipient quota: %d", code)
					}
				}
				rewrite := strings.ReplaceAll(original, "PARTSTAT=NEEDS-ACTION", "PARTSTAT="+tc.status)
				if tc.writerPaid {
					rewrite = strings.Replace(rewrite, "END:VEVENT", "DESCRIPTION:Writer-owned content\r\nEND:VEVENT", 1)
				}
				res := rawDAV(t, w.ts, "PUT", "/dav/usr_bob/calendars/default/"+copy.Name, "bob", w.tokens["bob"], rewrite, map[string]string{"Content-Type": "text/calendar", "If-Match": `"` + copy.ETag + `"`})
				if res.StatusCode != 201 && res.StatusCode != 204 {
					t.Fatalf("client status/content edit: %d %s", res.StatusCode, readAll(res))
				}
				w.deliver()
				_, edited := w.copyOf("bob", "trusted-cancel")
				if edited == nil {
					t.Fatal("missing edited invitation")
				}
				if !tc.keep && (edited.InvitationSender != "usr_alice" || edited.InvitationAttendee != "mailto:bob@example.com") {
					t.Fatal("unsupported status unexpectedly established a billable response")
				}
				if tc.keep && !tc.legacy && (edited.InvitationSender != "" || edited.InvitationRecipient != "usr_bob") {
					t.Fatal("authenticated response/rewrite did not charge Bob")
				}
				_, _, etag := w.get("alice", "source.ics")
				res = rawDAV(t, w.ts, "DELETE", "/dav/usr_alice/calendars/default/source.ics", "alice", w.tokens["alice"], "", map[string]string{"If-Match": etag})
				if res.StatusCode != 204 {
					t.Fatalf("cancel source: %d %s", res.StatusCode, readAll(res))
				}
				w.deliver()
				if _, reserved, n := reservationUsage(t, db); reserved != 0 || n != 0 {
					t.Fatalf("cancellation retained sender reservation: bytes=%d rows=%d", reserved, n)
				}
				var objects int
				var chargedBytes int64
				if err := db.QueryRow(`SELECT COUNT(*),COALESCE(SUM(LENGTH(o.data)),0) FROM calendar_objects o JOIN calendars c ON c.id=o.calendar_id WHERE o.invitation_sender='usr_alice' OR (o.invitation_sender='' AND o.invitation_recipient='' AND c.owner_kind='user' AND c.owner_id='usr_alice')`).Scan(&objects, &chargedBytes); err != nil {
					t.Fatal(err)
				}
				if objects != 0 || chargedBytes != 0 {
					t.Errorf("cancellation stranded sender quota: objects=%d bytes=%d", objects, chargedBytes)
				}
				raw, remaining := w.copyOf("bob", "trusted-cancel")
				if !tc.keep {
					if remaining != nil {
						t.Fatal("unsupported client status preserved an unresponded sponsored copy")
					}
					return
				}
				if remaining == nil || !strings.Contains(raw, "STATUS:CANCELLED") {
					t.Fatal("paid/answered or legacy copy was not retained cancelled")
				}
				if !tc.legacy && (remaining.InvitationSender != "" || remaining.InvitationRecipient != "usr_bob") {
					t.Fatal("retained copy changed its trusted payer")
				}
				if tc.legacy && (remaining.InvitationSender != "" || remaining.InvitationRecipient != "") {
					t.Fatal("legacy cancellation invented trusted attribution")
				}
			})
		}
	}
}

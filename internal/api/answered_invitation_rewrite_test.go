package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"bytes"
	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/itip"
	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/emersion/go-ical"
)

func TestCalDAVAnsweredGroupRewritePaysSecondWriter(t *testing.T) {
	const cap = 12000
	w, db := cancellationWorld(t, cap, 50000)
	ctx := context.Background()
	team := groupCalendar(t, w.st, "Answered copy")
	grantRole(t, w.st, team, "editor", "usr_bob")
	if err := w.st.Groups().AddGroupMember(ctx, "grp_editor_"+team.ID, "usr_carol"); err != nil {
		t.Fatal(err)
	}
	body := meeting("answered-group", "alice", "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:bob@example.com", "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:carol@example.com")
	if _, err := davbackend.Write(ctx, w.st, team, "answered.ics", decode(t, body), []byte(body), "", true, store.OwnerLimits{}, nil, ""); err != nil {
		t.Fatal(err)
	}
	if code, _ := w.put("alice", "answered.ics", body, ""); code != 201 {
		t.Fatalf("invite: %d", code)
	}
	w.deliver()
	copy, err := w.st.Calendars().GetObject(ctx, team.ID, "answered.ics")
	if err != nil {
		t.Fatal(err)
	}
	cal := decode(t, string(copy.Data))
	if err := itip.SetPartstat(cal, "", "mailto:bob@example.com", "ACCEPTED"); err != nil {
		t.Fatal(err)
	}
	var raw bytes.Buffer
	if err := ical.NewEncoder(&raw).Encode(cal); err != nil {
		t.Fatal(err)
	}
	bobPath := "/dav/usr_bob/calendars/_" + team.ID + "/answered.ics"
	res := rawDAV(t, w.ts, "PUT", bobPath, "bob", w.tokens["bob"], raw.String(), map[string]string{"Content-Type": "text/calendar", "If-Match": `"` + copy.ETag + `"`})
	if res.StatusCode != 201 && res.StatusCode != 204 {
		t.Fatalf("Bob's answer: %d %s", res.StatusCode, readAll(res))
	}
	w.deliver()
	copy, err = w.st.Calendars().GetObject(ctx, team.ID, "answered.ics")
	if err != nil {
		t.Fatal(err)
	}
	if copy.InvitationSender != "" || copy.InvitationRecipient != "usr_bob" {
		t.Fatal("answer did not establish Bob's charge")
	}
	original := string(copy.Data)
	if !strings.Contains(original, "X-KYCALENDAR-ORGANIZER-SEQUENCE:0\r\n") {
		t.Fatal("missing delivery binding")
	}
	base := meeting("carol-full", "carol")
	padding := cap - len(base) - len("DESCRIPTION:\r\n")
	filler := strings.Replace(base, "END:VEVENT", "DESCRIPTION:"+strings.Repeat("f", padding)+"\r\nEND:VEVENT", 1)
	if code, _ := w.put("carol", "full.ics", filler, ""); code != 201 {
		t.Fatalf("fill second writer quota: %d", code)
	}
	used, reserved, count := reservationUsage(t, db)
	grown := strings.Replace(original, "END:VEVENT", "DESCRIPTION:"+strings.Repeat("g", 1000)+"\r\nEND:VEVENT", 1)
	path := "/dav/usr_carol/calendars/_" + team.ID + "/answered.ics"
	for _, rewrite := range []struct{ name, body string }{{"growth", grown}, {"uid", strings.ReplaceAll(original, "UID:answered-group", "UID:replaced-group")}, {"organizer", strings.ReplaceAll(original, "mailto:alice@example.com", "mailto:other@example.com")}, {"delivery_binding", strings.Replace(original, "X-KYCALENDAR-ORGANIZER-SEQUENCE:0", "X-KYCALENDAR-ORGANIZER-SEQUENCE:9", 1)}, {"removed_binding", strings.Replace(original, "X-KYCALENDAR-ORGANIZER-SEQUENCE:0\r\n", "", 1)}} {
		t.Run(rewrite.name, func(t *testing.T) {
			res := rawDAV(t, w.ts, "PUT", path, "carol", w.tokens["carol"], rewrite.body, map[string]string{"Content-Type": "text/calendar", "If-Match": `"` + copy.ETag + `"`})
			if res.StatusCode != http.StatusInsufficientStorage {
				t.Fatalf("full second writer used Bob's quota: %d %s", res.StatusCode, readAll(res))
			}
			got, err := w.st.Calendars().GetObject(ctx, team.ID, copy.Name)
			if err != nil {
				t.Fatal(err)
			}
			if got.ETag != copy.ETag || got.InvitationRecipient != "usr_bob" || got.InvitationSender != "" || string(got.Data) != original {
				t.Fatal("refused replacement changed answered copy or charge")
			}
			nowUsed, nowReserved, nowCount := reservationUsage(t, db)
			if nowUsed != used || nowReserved != reserved || nowCount != count {
				t.Fatal("refused replacement changed storage/cancellation capacity")
			}
		})
	}
	_, full := w.copyOf("carol", "carol-full")
	if full == nil {
		t.Fatal("missing filler")
	}
	if err := w.st.Calendars().DeleteObject(ctx, full.CalendarID, full.Name, full.ETag); err != nil {
		t.Fatal(err)
	}
	owned := strings.ReplaceAll(grown, "UID:answered-group", "UID:replaced-group")
	res = rawDAV(t, w.ts, "PUT", path, "carol", w.tokens["carol"], owned, map[string]string{"Content-Type": "text/calendar", "If-Match": `"` + copy.ETag + `"`})
	if res.StatusCode != 201 && res.StatusCode != 204 {
		t.Fatalf("second writer with room: %d %s", res.StatusCode, readAll(res))
	}
	got, err := w.st.Calendars().GetObject(ctx, team.ID, copy.Name)
	if err != nil {
		t.Fatal(err)
	}
	if got.InvitationSender != "" || got.InvitationRecipient != "usr_carol" || got.UID != "replaced-group" {
		t.Fatal("valid rewrite was not charged to its writer")
	}
	_, nowReserved, nowCount := reservationUsage(t, db)
	if nowReserved != reserved || nowCount != count {
		t.Fatal("answered-copy rewrite changed original cancellation reservation")
	}
}

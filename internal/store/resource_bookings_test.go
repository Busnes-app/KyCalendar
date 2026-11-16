package store

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/resource"
	"github.com/Busnes-app/kycalendar/internal/testdb"
	"github.com/emersion/go-ical"
)

func TestRoomNeutralReplyBetweenYearlyWindows(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	oldClock := resourceNow
	resourceNow = func() time.Time { return now }
	defer func() { resourceNow = oldClock }()
	if err := st.Users().CreateUser(ctx, &User{ID: "alice", Username: "alice", Email: "alice@example.com", Role: "user", Status: "active", SSOProvider: "local"}); err != nil {
		t.Fatal(err)
	}
	r := &resource.Resource{ID: strings.Repeat("ab", 16), Kind: resource.Room, Name: "Fjord", Enabled: true, Audience: resource.Everyone, Approval: resource.Auto, MaxMinutes: 120, HorizonDays: 30}
	if err := st.Resources().CreateResource(ctx, r); err != nil {
		t.Fatal(err)
	}
	c := &Calendar{ID: "cal", OwnerKind: "user", OwnerID: "alice", Slug: "default", Name: "Calendar"}
	if err := st.Calendars().CreateCalendar(ctx, c, 0); err != nil {
		t.Fatal(err)
	}
	master := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\nBEGIN:VEVENT\r\nUID:yearly\r\nDTSTAMP:20261101T000000Z\r\nDTSTART:20261120T090000Z\r\nDTEND:20261120T100000Z\r\nRRULE:FREQ=YEARLY\r\nORGANIZER:mailto:alice@example.com\r\nATTENDEE:" + string(resource.Address(r.ID)) + "\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	put := func(data, writer, name string) {
		t.Helper()
		cal, err := ical.NewDecoder(strings.NewReader(data)).Decode()
		if err != nil {
			t.Fatal(err)
		}
		info, err := calendar.Inspect(cal)
		if err != nil {
			t.Fatal(err)
		}
		o := &CalendarObject{CalendarID: c.ID, Name: name, UID: info.UID, Component: info.Component, Data: []byte(data), FirstStart: info.FirstStart, LastEnd: info.LastEnd, WriterID: writer}
		if _, err := st.Calendars().PutObject(ctx, o, "", false, OwnerLimits{}); err != nil {
			t.Fatal(err)
		}
	}
	put(master, "alice", "yearly.ics")
	check := func() *ResourceBooking {
		t.Helper()
		bs, err := st.Resources().BookingsIn(ctx, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		b := bs["yearly.ics"][r.ID]
		if b == nil || b.Status != resource.Accepted {
			t.Fatalf("booking: %+v", b)
		}
		from := time.Date(2027, 11, 20, 9, 0, 0, 0, time.UTC).Unix()
		busy, err := st.Resources().Busy(ctx, r.ID, from, from+3600, "", "")
		if err != nil || len(busy) != 1 {
			t.Fatalf("future reservation: %v %v", busy, err)
		}
		return b
	}
	before := check()
	now = time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)
	answer := "BEGIN:VEVENT\r\nUID:yearly\r\nDTSTAMP:20270301T000000Z\r\nRECURRENCE-ID:20271120T090000Z\r\nDTSTART:20271120T090000Z\r\nDTEND:20271120T100000Z\r\nATTENDEE;PARTSTAT=ACCEPTED:mailto:bob@example.com\r\nEND:VEVENT\r\n"
	put(strings.Replace(master, "END:VCALENDAR", answer+"END:VCALENDAR", 1), "", "yearly.ics")
	after := check()
	if before.ID != after.ID || before.OpenAfter != after.OpenAfter {
		t.Fatalf("neutral reply replaced held booking: before=%+v after=%+v", before, after)
	}
	got, err := st.Calendars().GetObject(ctx, c.ID, "yearly.ics")
	if err != nil || !bytes.Contains(got.Data, []byte("PARTSTAT=ACCEPTED")) {
		t.Fatalf("answer missing: %v", err)
	}
	// A cancellation between yearly windows releases that occurrence. Restoring the original
	// bytes later must not reuse a fingerprint from before the cancellation.
	cancelled := strings.Replace(master, "END:VCALENDAR", strings.Replace(answer, "DTSTAMP:", "STATUS:CANCELLED\r\nDTSTAMP:", 1)+"END:VCALENDAR", 1)
	for _, tc := range []struct{ name, writer, status, reason string }{
		{"authorization", "", resource.Declined, resource.ReasonNotAllowed},
		{"approval", "alice", resource.Pending, ""},
		{"conflict", "alice", resource.Declined, resource.ReasonBusy},
	} {
		if err := st.Calendars().DeleteObject(ctx, c.ID, "yearly.ics", ""); err != nil {
			t.Fatal(err)
		}
		r.Approval, r.Approvers = resource.Auto, nil
		if err := st.Resources().UpdateResource(ctx, r); err != nil {
			t.Fatal(err)
		}
		now = time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
		put(master, "alice", "yearly.ics")
		now = time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)
		put(cancelled, "", "yearly.ics")
		cal, err := ical.NewDecoder(strings.NewReader(cancelled)).Decode()
		if err != nil {
			t.Fatal(err)
		}
		var fingerprint string
		db := st.(*SQLStore)
		if err := db.db.QueryRowContext(ctx, db.rebind(`SELECT fingerprint FROM resource_bookings WHERE calendar_id = ? AND object_name = ?`), c.ID, "yearly.ics").Scan(&fingerprint); err != nil {
			t.Fatal(err)
		}
		if fingerprint != resource.Fingerprint(cal) {
			t.Errorf("%s: preserved fingerprint is stale", tc.name)
		}
		from := time.Date(2027, 11, 20, 9, 0, 0, 0, time.UTC).Unix()
		if busy, err := st.Resources().Busy(ctx, r.ID, from, from+3600, "", ""); err != nil || len(busy) != 0 {
			t.Fatalf("cancelled occurrence is busy: %v %v", busy, err)
		}
		now = time.Date(2027, 11, 1, 0, 0, 0, 0, time.UTC)
		if tc.name == "approval" {
			r.Approval, r.Approvers = resource.Approvers, []string{"alice"}
			if err := st.Resources().UpdateResource(ctx, r); err != nil {
				t.Fatal(err)
			}
		}
		if tc.name == "conflict" {
			other := strings.ReplaceAll(master, "UID:yearly", "UID:other")
			other = strings.ReplaceAll(other, "20261120", "20271120")
			// The competing hold is one occurrence; a second infinite yearly rule
			// would itself conflict with this reservation in later years.
			other = strings.ReplaceAll(other, "RRULE:FREQ=YEARLY\r\n", "")
			put(other, "alice", "other.ics")
		}
		put(master, tc.writer, "yearly.ics")
		bs, err := st.Resources().BookingsIn(ctx, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		b := bs["yearly.ics"][r.ID]
		if b.Status != tc.status || b.Reason != tc.reason {
			t.Errorf("%s: restored occurrence bypassed new decision: %+v", tc.name, b)
		}
	}

}

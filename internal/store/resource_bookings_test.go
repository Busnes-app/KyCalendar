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
	put := func(data, writer string) {
		t.Helper()
		cal, err := ical.NewDecoder(strings.NewReader(data)).Decode()
		if err != nil {
			t.Fatal(err)
		}
		info, err := calendar.Inspect(cal)
		if err != nil {
			t.Fatal(err)
		}
		o := &CalendarObject{CalendarID: c.ID, Name: "yearly.ics", UID: info.UID, Component: info.Component, Data: []byte(data), FirstStart: info.FirstStart, LastEnd: info.LastEnd, WriterID: writer}
		if _, err := st.Calendars().PutObject(ctx, o, "", false, OwnerLimits{}); err != nil {
			t.Fatal(err)
		}
	}
	put(master, "alice")
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
	put(strings.Replace(master, "END:VCALENDAR", answer+"END:VCALENDAR", 1), "")
	after := check()
	if before.ID != after.ID || before.OpenAfter != after.OpenAfter {
		t.Fatalf("neutral reply replaced held booking: before=%+v after=%+v", before, after)
	}
	got, err := st.Calendars().GetObject(ctx, c.ID, "yearly.ics")
	if err != nil || !bytes.Contains(got.Data, []byte("PARTSTAT=ACCEPTED")) {
		t.Fatalf("answer missing: %v", err)
	}
}

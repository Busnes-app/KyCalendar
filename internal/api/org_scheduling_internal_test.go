package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"

	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/itip"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// L8 and L9 (tenancy spec D5): in multi-tenant mode a meeting link stays clickable only for an
// organizer in the viewer's org, and a group calendar's meeting is cancelled as its organizer only
// when they are in the calendar's org.
func TestMultiTenantMeetingMembersAndGroupOrganizersAreInTheOrg(t *testing.T) {
	t.Setenv("KYCALENDAR_SCHEDULING", "true")
	s, _ := davInternalServer(t)
	ctx := context.Background()
	st := s.store
	const other store.OrgID = "org-other"
	if err := st.ApplyOrgs(ctx, "", []store.MirroredOrg{{ID: other, Status: "active", Revision: 1, Enabled: true, Quotas: "{}"}}, 1); err != nil {
		t.Fatal(err)
	}
	for _, u := range []*store.User{
		{ID: "usr_ann", Username: "ann", Email: "ann@example.com", Role: "user", Status: "active", SSOProvider: "local"},
		{ID: "usr_alice", Username: "alice", Email: "alice@example.com", Role: "user", Status: "active", SSOProvider: "local"},
		{ID: "usr_dave", Username: "dave", Email: "dave@example.com", Role: "user", Status: "active", SSOProvider: "local", OrgID: other},
	} {
		if err := st.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	if err := davbackend.EnsureDefault(ctx, st, "usr_ann", 0); err != nil {
		t.Fatal(err)
	}
	cal, err := st.Calendars().GetCalendarBySlug(ctx, "user", "usr_ann", "default")
	if err != nil {
		t.Fatal(err)
	}
	put := func(c *store.Calendar, uid, organizer string, room bool) {
		t.Helper()
		ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nBEGIN:VEVENT\r\nUID:" + uid + "\r\nDTSTAMP:20261101T000000Z\r\n" +
			"DTSTART:20261110T090000Z\r\nDTEND:20261110T100000Z\r\nSUMMARY:Sync\r\nORGANIZER:mailto:" + organizer + "@example.com\r\n" +
			"ATTENDEE:mailto:ann@example.com\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
		parsed, err := ical.NewDecoder(strings.NewReader(ics)).Decode()
		if err != nil {
			t.Fatal(err)
		}
		if room {
			calendar.SetConference(parsed, "https://messages.example/r/"+uid)
		}
		var b strings.Builder
		if err := ical.NewEncoder(&b).Encode(parsed); err != nil {
			t.Fatal(err)
		}
		o, err := davbackend.Prepare(c.ID, uid+".ics", parsed, []byte(b.String()))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.Calendars().PutObject(ctx, o, "", true, store.OwnerLimits{}); err != nil {
			t.Fatal(err)
		}
	}
	put(cal, "from-alice", "alice", true)
	put(cal, "from-dave", "dave", true)

	if err := st.Settings().SetSetting(ctx, store.TenancySetting, "multi"); err != nil {
		t.Fatal(err)
	}
	ann, err := st.Users().GetUserByID(ctx, "usr_ann")
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	views, apiErr := s.expandEvents(ctx, ann, []*store.Calendar{cal}, nil, from, from.AddDate(0, 1, 0), time.UTC)
	if apiErr != nil {
		t.Fatal(apiErr.msg)
	}
	links := map[string]string{}
	for _, v := range views {
		links[v.UID] = v.MeetingURL
	}
	if links["from-alice"] == "" {
		t.Error("a colleague's meeting link is not clickable")
	}
	if links["from-dave"] != "" {
		t.Errorf("another org's organizer kept a clickable link %q", links["from-dave"])
	}

	// L4: Plan counts an internal REQUEST copy's bytes only for a colleague.
	alice, err := st.Users().GetUserByID(ctx, "usr_alice")
	if err != nil {
		t.Fatal(err)
	}
	invite, err := ical.NewDecoder(strings.NewReader("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nBEGIN:VEVENT\r\nUID:plan\r\n" +
		"DTSTAMP:20261101T000000Z\r\nDTSTART:20261110T090000Z\r\nDTEND:20261110T100000Z\r\nSUMMARY:Plan\r\nORGANIZER:mailto:alice@example.com\r\n" +
		"ATTENDEE:mailto:ann@example.com\r\nATTENDEE:mailto:dave@example.com\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")).Decode()
	if err != nil {
		t.Fatal(err)
	}
	_, rows, err := s.sched.Plan(ctx, alice, invite)(nil)
	if err != nil {
		t.Fatal(err)
	}
	counted := map[string]bool{}
	for _, m := range rows {
		counted[m.Recipient] = m.CountInviteBytes
	}
	if !counted["mailto:ann@example.com"] || counted["mailto:dave@example.com"] || len(counted) != 2 {
		t.Errorf("internal REQUEST copies counted %v, want only the colleague's", counted)
	}

	if err := st.Calendars().CreateCalendar(ctx, &store.Calendar{ID: "cal_g", OwnerKind: "group", OwnerID: "cal_g", Slug: "g", Name: "G"}, 0); err != nil {
		t.Fatal(err)
	}
	group, err := st.Calendars().GetCalendarByID(ctx, "cal_g")
	if err != nil || group.OrgID != store.DefaultOrg {
		t.Fatalf("group calendar %+v %v", group, err)
	}
	for who, want := range map[string]string{"alice": "usr_alice", "dave": ""} {
		addr, _ := itip.ParseAddress("mailto:" + who + "@example.com")
		got := ""
		if u := s.groupOrganizer(ctx, group, addr, "uid-"+who); u != nil {
			got = u.ID
		}
		if got != want {
			t.Errorf("group calendar meeting organized by %s cancels as %q, want %q", who, got, want)
		}
	}
}

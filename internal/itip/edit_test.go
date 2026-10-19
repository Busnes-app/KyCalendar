package itip

import (
	"errors"
	"strings"
	"testing"

	"github.com/emersion/go-ical"
)

func TestSetAttendees(t *testing.T) {
	cal := ics(t, "ATTENDEE;PARTSTAT=ACCEPTED;CN=Bob:mailto:bob@example.com", "ATTENDEE:mailto:old@example.com")
	if err := SetAttendees(cal, alice, []Address{bob, carol, carol, alice}); err != nil {
		t.Fatal(err)
	}
	s := encode(t, cal)
	for _, want := range []string{"ORGANIZER:mailto:alice@example.com", "PARTSTAT=ACCEPTED:mailto:alice@example.com",
		"CN=Bob", "PARTSTAT=NEEDS-ACTION;RSVP=TRUE:mailto:carol@example.net"} {
		if !strings.Contains(s, want) && !strings.Contains(s, strings.Replace(want, "PARTSTAT=NEEDS-ACTION;RSVP=TRUE", "RSVP=TRUE;PARTSTAT=NEEDS-ACTION", 1)) {
			t.Errorf("missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "old@example.com") || strings.Count(s, "carol@example.net") != 1 {
		t.Errorf("a removed or duplicated attendee:\n%s", s)
	}
	got := AttendeesOf(cal.Children[0])
	if len(got) != 3 || got[0].Address != alice || got[1].Status != "ACCEPTED" || got[2].Status != "NEEDS-ACTION" {
		t.Errorf("AttendeesOf %+v", got)
	}

	if err := SetAttendees(cal, alice, nil); err != nil {
		t.Fatal(err)
	}
	if s := encode(t, cal); strings.Contains(s, "ATTENDEE") || strings.Contains(s, "ORGANIZER") {
		t.Errorf("an empty list leaves a meeting:\n%s", s)
	}

	theirs := ics(t, "ATTENDEE:mailto:alice@example.com")
	if err := SetAttendees(theirs, bob, []Address{carol}); !errors.Is(err, ErrNotOrganizer) {
		t.Errorf("bob rewriting alice's meeting: %v", err)
	}
	orphan := ics(t, "ATTENDEE:mailto:bob@example.com")
	orphan.Children[0].Props.Del(ical.PropOrganizer)
	if err := SetAttendees(orphan, alice, []Address{carol}); !errors.Is(err, ErrNotOrganizer) {
		t.Errorf("attendees with no organizer: %v", err)
	}
}

func TestSetPartstat(t *testing.T) {
	cal := ics(t, "ATTENDEE;PARTSTAT=NEEDS-ACTION;RSVP=TRUE:mailto:bob@example.com")
	if err := SetPartstat(cal, "", bob, "accepted"); err != nil {
		t.Fatal(err)
	}
	if s := encode(t, cal); !strings.Contains(s, "ATTENDEE;PARTSTAT=ACCEPTED:mailto:bob@example.com") {
		t.Errorf("answer:\n%s", s)
	}
	if err := SetPartstat(cal, "", carol, "DECLINED"); !errors.Is(err, ErrNotInvited) {
		t.Errorf("a stranger's answer: %v", err)
	}
	for _, bad := range []string{"DELEGATED", "NEEDS-ACTION", "MAYBE", ""} {
		if err := SetPartstat(cal, "", bob, bad); !errors.Is(err, ErrBadPartstat) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if err := SetPartstat(cal, "20261117T090000Z", bob, "DECLINED"); !errors.Is(err, ErrNotInvited) {
		t.Errorf("an occurrence with no override: %v", err)
	}
}

package itip

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
)

var now = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

const (
	alice = Address("mailto:alice@example.com")
	bob   = Address("mailto:bob@example.com")
	carol = Address("mailto:carol@example.net")
)

// ics builds a one-VEVENT object; extra lines go inside the VEVENT, before END.
func ics(t *testing.T, extra ...string) *ical.Calendar {
	t.Helper()
	body := "BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//t//EN\nBEGIN:VEVENT\nUID:m1\nDTSTAMP:20261001T000000Z\n" +
		"DTSTART:20261110T090000Z\nDTEND:20261110T100000Z\nSUMMARY:Planning\nORGANIZER:mailto:alice@example.com\n" +
		strings.Join(extra, "\n") + "\nEND:VEVENT\nEND:VCALENDAR\n"
	cal, err := Decode([]byte(strings.ReplaceAll(strings.ReplaceAll(body, "\n\n", "\n"), "\n", "\r\n")))
	if err != nil {
		t.Fatal(err)
	}
	return cal
}

func encode(t *testing.T, cal *ical.Calendar) string {
	t.Helper()
	var b bytes.Buffer
	if err := ical.NewEncoder(&b).Encode(cal); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func summary(msgs []Message) string {
	var out []string
	for _, m := range msgs {
		var to []string
		for _, r := range m.Recipients {
			to = append(to, r.Email())
		}
		out = append(out, m.Method+" "+strings.Join(to, ","))
	}
	return strings.Join(out, "; ")
}

func TestParseAddress(t *testing.T) {
	for in, want := range map[string]Address{
		"mailto:Bob@Example.COM":  "mailto:bob@example.com",
		"MAILTO:bob@example.com":  "mailto:bob@example.com",
		" mailto:bob@example.com": "mailto:bob@example.com",
	} {
		if got, ok := ParseAddress(in); !ok || got != want {
			t.Errorf("%q: %q %v", in, got, ok)
		}
	}
	for _, bad := range []string{
		"bob@example.com", "mailto:", "mailto:bob", "http://x", "mailto:Bob <bob@example.com>",
		"mailto:bob@example.com\r\nBcc: x@y", "mailto:a@b,c@d", "mailto:" + strings.Repeat("a", 251) + "@b.c",
		"mailto:a b@c.d", `mailto:"a"@b.c`,
	} {
		if got, ok := ParseAddress(bad); ok {
			t.Errorf("%q accepted as %q", bad, got)
		}
	}
}

func TestOrganizerCreateUpdateDelete(t *testing.T) {
	base := ics(t, "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:bob@example.com", "ATTENDEE:mailto:carol@example.net",
		"ATTENDEE;PARTSTAT=ACCEPTED:mailto:alice@example.com")
	if got := summary(Outgoing(alice, nil, base, now)); got != "REQUEST bob@example.com,carol@example.net" {
		t.Errorf("create: %s", got)
	}

	moved := ics(t, "ATTENDEE:mailto:bob@example.com", "ATTENDEE:mailto:carol@example.net")
	moved.Children[0].Props.SetText(ical.PropDateTimeStart, "20261110T110000Z")
	if got := summary(Outgoing(alice, base, moved, now)); got != "REQUEST bob@example.com,carol@example.net" {
		t.Errorf("move: %s", got)
	}

	answered := ics(t, "ATTENDEE;PARTSTAT=ACCEPTED:mailto:bob@example.com", "ATTENDEE:mailto:carol@example.net",
		"ATTENDEE;PARTSTAT=ACCEPTED:mailto:alice@example.com")
	if got := summary(Outgoing(alice, base, answered, now)); got != "" {
		t.Errorf("only a recorded answer changed, yet: %s", got)
	}

	dropped := ics(t, "ATTENDEE:mailto:bob@example.com")
	if got := summary(Outgoing(alice, base, dropped, now)); got != "CANCEL carol@example.net; REQUEST bob@example.com" {
		t.Errorf("remove carol: %s", got)
	}

	if got := summary(Outgoing(alice, base, nil, now)); got != "CANCEL bob@example.com,carol@example.net" {
		t.Errorf("delete: %s", got)
	}
	cancel := Outgoing(alice, base, nil, now)[0].Calendar
	if s := encode(t, cancel); !strings.Contains(s, "METHOD:CANCEL") || !strings.Contains(s, "STATUS:CANCELLED") {
		t.Errorf("cancel body:\n%s", s)
	}
}

func TestOrganizerMessageIsSanitised(t *testing.T) {
	cal := ics(t, "ATTENDEE;SCHEDULE-STATUS=1.2;RSVP=TRUE:mailto:bob@example.com", "BEGIN:VALARM\nACTION:DISPLAY\nTRIGGER:-PT15M\nDESCRIPTION:mine\nEND:VALARM")
	cal.Children[0].Props.Get(ical.PropOrganizer).Params.Set("SCHEDULE-AGENT", "SERVER")
	msgs := Outgoing(alice, nil, cal, now)
	if len(msgs) != 1 {
		t.Fatalf("%s", summary(msgs))
	}
	s := encode(t, msgs[0].Calendar)
	for _, gone := range []string{"VALARM", "SCHEDULE-STATUS", "SCHEDULE-AGENT"} {
		if strings.Contains(s, gone) {
			t.Errorf("%s sent:\n%s", gone, s)
		}
	}
	if !strings.Contains(s, "METHOD:REQUEST") || !strings.Contains(s, "RSVP=TRUE") {
		t.Errorf("request:\n%s", s)
	}
	if !strings.Contains(encode(t, cal), "VALARM") {
		t.Error("building the message changed the stored object")
	}
}

func TestScheduleAgentClientIsLeftAlone(t *testing.T) {
	cal := ics(t, "ATTENDEE;SCHEDULE-AGENT=CLIENT:mailto:bob@example.com", "ATTENDEE;SCHEDULE-AGENT=NONE:mailto:carol@example.net")
	if got := summary(Outgoing(alice, nil, cal, now)); got != "" {
		t.Errorf("client-scheduled attendees got: %s", got)
	}
}

func TestNotTheOrganizerOrAnAttendee(t *testing.T) {
	cal := ics(t, "ATTENDEE:mailto:bob@example.com")
	if got := summary(Outgoing("mailto:mallory@example.org", nil, cal, now)); got != "" {
		t.Errorf("a stranger's write sent: %s", got)
	}
	plain := ics(t)
	plain.Children[0].Props.Del(ical.PropOrganizer)
	if got := Outgoing(alice, nil, plain, now); got != nil {
		t.Errorf("an event without ORGANIZER sent: %s", summary(got))
	}
}

func TestAttendeeReply(t *testing.T) {
	invited := ics(t, "SEQUENCE:2", "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:bob@example.com", "ATTENDEE:mailto:carol@example.net")
	accepted := ics(t, "SEQUENCE:2", "ATTENDEE;PARTSTAT=ACCEPTED:mailto:bob@example.com", "ATTENDEE:mailto:carol@example.net")
	msgs := Outgoing(bob, invited, accepted, now)
	if got := summary(msgs); got != "REPLY alice@example.com" {
		t.Fatalf("accept: %s", got)
	}
	s := encode(t, msgs[0].Calendar)
	for _, want := range []string{"METHOD:REPLY", "ATTENDEE;PARTSTAT=ACCEPTED:mailto:bob@example.com", "UID:m1", "SEQUENCE:2", "ORGANIZER:mailto:alice@example.com"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q:\n%s", want, s)
		}
	}
	for _, leak := range []string{"carol", "SUMMARY"} {
		if strings.Contains(s, leak) {
			t.Errorf("REPLY carries %q:\n%s", leak, s)
		}
	}

	retitled := ics(t, "SEQUENCE:2", "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:bob@example.com", "ATTENDEE:mailto:carol@example.net")
	retitled.Children[0].Props.SetText(ical.PropSummary, "Mine now")
	retitled.Children[0].Props.SetText(ical.PropDateTimeStart, "20261111T090000Z")
	if got := summary(Outgoing(bob, invited, retitled, now)); got != "" {
		t.Errorf("an attendee's own edits are never sent: %s", got)
	}

	if got := summary(Outgoing(bob, invited, nil, now)); got != "REPLY alice@example.com" {
		t.Fatalf("delete: %s", got)
	}
	if s := encode(t, Outgoing(bob, invited, nil, now)[0].Calendar); !strings.Contains(s, "PARTSTAT=DECLINED") {
		t.Errorf("deleting an invitation declines it:\n%s", s)
	}
	if got := summary(Outgoing(bob, nil, invited, now)); got != "" {
		t.Errorf("an attendee's first write sends nothing: %s", got)
	}
}

func TestSignificantAndBumpSequence(t *testing.T) {
	old := ics(t, "SEQUENCE:3", "ATTENDEE:mailto:bob@example.com")
	same := ics(t, "SEQUENCE:3", "ATTENDEE;PARTSTAT=ACCEPTED:mailto:bob@example.com")
	same.Children[0].Props.SetText(ical.PropDescription, "notes")
	if Significant(old, same) || BumpSequence(old, same) {
		t.Error("a description and an answer are not significant")
	}
	if !Changed(old, same) {
		t.Error("a description change is worth telling attendees")
	}
	moved := ics(t, "SEQUENCE:3", "ATTENDEE:mailto:bob@example.com")
	moved.Children[0].Props.SetText(ical.PropLocation, "Room 2")
	if !BumpSequence(old, moved) || moved.Children[0].Props.Get(ical.PropSequence).Value != "4" {
		t.Errorf("location change: SEQUENCE %v", moved.Children[0].Props.Get(ical.PropSequence))
	}
	bumped := ics(t, "SEQUENCE:9", "DTEND:20261110T110000Z")
	if BumpSequence(old, bumped) {
		t.Error("a client that raised SEQUENCE itself is left alone")
	}
	if BumpSequence(nil, moved) {
		t.Error("a create bumps nothing")
	}
}

func TestDecodeIsHostileInputSafe(t *testing.T) {
	for name, body := range map[string]string{
		"go-ical panic":  "BEGIN:VCALENDAR\r\nX;A=\"b\"c:v\r\nEND:VCALENDAR\r\n",
		"long parameter": "BEGIN:VCALENDAR\r\nX;A=" + strings.Repeat("a", 2<<10) + ":v\r\nEND:VCALENDAR\r\n",
		"oversize":       "BEGIN:VCALENDAR\r\nX:" + strings.Repeat("a", 1<<20) + "\r\nEND:VCALENDAR\r\n",
		"not a calendar": "BEGIN:VEVENT\r\nUID:x\r\nEND:VEVENT\r\n",
		"truncated":      "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\n",
	} {
		start := time.Now()
		if _, err := Decode([]byte(body)); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
		if d := time.Since(start); d > time.Second {
			t.Errorf("%s: took %v", name, d)
		}
	}
}

func TestValidate(t *testing.T) {
	reply := func(lines ...string) *ical.Calendar {
		cal := ics(t, lines...)
		cal.Props.SetText(ical.PropMethod, MethodReply)
		return cal
	}
	if m, uid, err := Validate(reply("ATTENDEE;PARTSTAT=ACCEPTED:mailto:bob@example.com")); err != nil || m != MethodReply || uid != "m1" {
		t.Errorf("valid reply: %q %q %v", m, uid, err)
	}
	many := make([]string, 101)
	for i := range many {
		many[i] = "ATTENDEE:mailto:a" + strings.Repeat("x", i%5) + "@example.com"
	}
	for name, cal := range map[string]*ical.Calendar{
		"no method":     ics(t),
		"publish":       func() *ical.Calendar { c := ics(t); c.Props.SetText(ical.PropMethod, "PUBLISH"); return c }(),
		"COUNTER":       func() *ical.Calendar { c := ics(t); c.Props.SetText(ical.PropMethod, "COUNTER"); return c }(),
		"bad attendee":  reply("ATTENDEE:mailto:bob@example.com\\r\\nBcc: x@y"),
		"101 attendees": reply(many...),
		"no organizer":  func() *ical.Calendar { c := reply(); c.Children[0].Props.Del(ical.PropOrganizer); return c }(),
		"second UID": func() *ical.Calendar {
			c := reply()
			e := ical.NewComponent(ical.CompEvent)
			e.Props.SetText(ical.PropUID, "other")
			c.Children = append(c.Children, e)
			return c
		}(),
		"a VTODO": func() *ical.Calendar {
			c := reply()
			c.Children = append(c.Children, ical.NewComponent(ical.CompToDo))
			return c
		}(),
	} {
		if _, _, err := Validate(cal); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestApplyReply(t *testing.T) {
	stored := func() *ical.Calendar {
		return ics(t, "SEQUENCE:2", "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:bob@example.com", "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:carol@example.net")
	}
	reply := func(lines ...string) *ical.Calendar {
		body := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nMETHOD:REPLY\r\nBEGIN:VEVENT\r\nUID:m1\r\nDTSTAMP:20261101T000000Z\r\n" +
			"ORGANIZER:mailto:alice@example.com\r\n" + strings.Join(lines, "\r\n") + "\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
		cal, err := Decode([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
		return cal
	}

	s := stored()
	changed, err := ApplyReply(s, reply("SEQUENCE:2", "ATTENDEE;PARTSTAT=ACCEPTED:mailto:bob@example.com"), bob)
	if err != nil || !changed || !strings.Contains(encode(t, s), "PARTSTAT=ACCEPTED:mailto:bob@example.com") {
		t.Fatalf("accept: %v %v\n%s", changed, err, encode(t, s))
	}

	// The reply speaks for carol and moves the meeting; only the sender's own answer counts.
	s = stored()
	forged := reply("SEQUENCE:2", "DTSTART:20261201T000000Z", "SUMMARY:Hijacked",
		"ATTENDEE;PARTSTAT=DECLINED:mailto:carol@example.net", "ATTENDEE;PARTSTAT=ACCEPTED:mailto:bob@example.com")
	if _, err := ApplyReply(s, forged, bob); err != nil {
		t.Fatal(err)
	}
	out := encode(t, s)
	if strings.Contains(out, "Hijacked") || strings.Contains(out, "20261201") || strings.Contains(out, "DECLINED") {
		t.Errorf("a reply changed more than the sender's answer:\n%s", out)
	}

	if _, err := ApplyReply(stored(), reply("ATTENDEE;PARTSTAT=ACCEPTED:mailto:mallory@example.org"), "mailto:mallory@example.org"); !errors.Is(err, ErrNotAttendee) {
		t.Errorf("a non-attendee: %v", err)
	}
	if _, err := ApplyReply(stored(), reply("ATTENDEE;PARTSTAT=ACCEPTED:mailto:bob@example.com"), carol); !errors.Is(err, ErrNotAttendee) {
		t.Errorf("bob's answer sent by carol: %v", err)
	}
	other := reply("ATTENDEE;PARTSTAT=ACCEPTED:mailto:bob@example.com")
	other.Children[0].Props.SetText(ical.PropUID, "m2")
	if _, err := ApplyReply(stored(), other, bob); !errors.Is(err, ErrUIDMismatch) {
		t.Errorf("another UID: %v", err)
	}
	s = stored()
	if changed, _ := ApplyReply(s, reply("SEQUENCE:1", "ATTENDEE;PARTSTAT=DECLINED:mailto:bob@example.com"), bob); changed {
		t.Error("an answer to an older SEQUENCE is ignored")
	}
	if changed, _ := ApplyReply(s, reply("SEQUENCE:2", "ATTENDEE;PARTSTAT=MAYBE:mailto:bob@example.com"), bob); changed {
		t.Error("an unknown PARTSTAT is ignored")
	}
	if _, err := ApplyReply(s, ics(t, "ATTENDEE:mailto:bob@example.com"), bob); !errors.Is(err, ErrInvalid) {
		t.Errorf("not a REPLY: %v", err)
	}
	s = stored()
	if _, err := ApplyReply(s, reply("RECURRENCE-ID:20261117T090000Z", "ATTENDEE;PARTSTAT=DECLINED:mailto:bob@example.com"), bob); !errors.Is(err, ErrNotAttendee) {
		t.Errorf("an answer for an occurrence the object lacks: %v", err)
	}
	if len(s.Children) != 1 {
		t.Error("a reply created an override")
	}
}

func TestAttendeeCopy(t *testing.T) {
	request := ics(t, "SEQUENCE:1", "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:bob@example.com")
	request.Props.SetText(ical.PropMethod, MethodRequest)
	first := AttendeeCopy(request, nil, bob)
	if s := encode(t, first); strings.Contains(s, "METHOD") || !strings.Contains(s, "PARTSTAT=NEEDS-ACTION:mailto:bob@example.com") {
		t.Errorf("first delivery:\n%s", s)
	}

	mine := ics(t, "SEQUENCE:1", "ATTENDEE;PARTSTAT=ACCEPTED:mailto:bob@example.com", "BEGIN:VALARM\nACTION:DISPLAY\nTRIGGER:-PT5M\nDESCRIPTION:bob's\nEND:VALARM")
	retitled := ics(t, "SEQUENCE:1", "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:bob@example.com")
	retitled.Children[0].Props.SetText(ical.PropSummary, "Planning v2")
	s := encode(t, AttendeeCopy(retitled, mine, bob))
	if !strings.Contains(s, "Planning v2") || !strings.Contains(s, "PARTSTAT=ACCEPTED:mailto:bob@example.com") || !strings.Contains(s, "bob's") {
		t.Errorf("a retitle keeps bob's answer and alarm:\n%s", s)
	}

	moved := ics(t, "SEQUENCE:2", "ATTENDEE;PARTSTAT=ACCEPTED:mailto:bob@example.com")
	moved.Children[0].Props.SetText(ical.PropDateTimeStart, "20261110T150000Z")
	if s := encode(t, AttendeeCopy(moved, mine, bob)); !strings.Contains(s, "PARTSTAT=NEEDS-ACTION:mailto:bob@example.com") {
		t.Errorf("a move asks bob again:\n%s", s)
	}
}

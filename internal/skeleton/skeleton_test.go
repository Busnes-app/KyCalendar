package skeleton

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/emersion/go-ical"
)

// corpus is every real client object in the repository's fixtures: Apple, Google, Outlook,
// Thunderbird and Microsoft 365 events and tasks.
func corpus(t *testing.T) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for _, glob := range []string{"../calendar/testdata/recurrence/*.ics", "../tasks/testdata/*.ics", "../feed/testdata/*.ics"} {
		files, err := filepath.Glob(glob)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			if c, err := decode(b); err != nil {
				continue // a fixture of a parse failure
			} else if _, err := encode(c.Component); err != nil {
				continue // not storable as it is: m365.ics holds an event with no UID, which the feed skips
			}
			out[filepath.Base(f)] = b
		}
	}
	if len(out) < 10 {
		t.Fatalf("corpus of %d objects", len(out))
	}
	return out
}

// props is c's properties as sorted "COMP NAME;PARAMS:VALUE" lines, its children's too, in
// order: what a reader sees, whatever the byte layout.
func props(c *ical.Component) []string {
	var out []string
	for name, ps := range c.Props {
		for _, p := range ps {
			var params []string
			for k, v := range p.Params {
				params = append(params, k+"="+strings.Join(v, ","))
			}
			slices.Sort(params)
			out = append(out, fmt.Sprintf("%s %s;%s:%s", c.Name, name, strings.Join(params, ";"), p.Value))
		}
	}
	slices.Sort(out)
	for i, child := range c.Children {
		for _, l := range props(child) {
			out = append(out, fmt.Sprintf("%d/%s", i, l))
		}
	}
	return out
}

func mustDecode(t *testing.T, b []byte) *ical.Calendar {
	t.Helper()
	c, err := decode(b)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// E1, E2: a skeleton holds no content property, and a base on its own skeleton is served back
// byte for byte; the slow path (any patch present) serves every property the client wrote.
func TestCorpusSplitsAndMergesBack(t *testing.T) {
	for name, raw := range corpus(t) {
		skel, err := Split(raw)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var check func(c *ical.Component)
		check = func(c *ical.Component) {
			for p := range c.Props {
				if !isReadable(c.Name, p) {
					t.Errorf("%s: skeleton keeps %s %s", name, c.Name, p)
				}
			}
			for _, child := range c.Children {
				check(child)
			}
		}
		check(mustDecode(t, skel).Component)
		for _, secret := range []string{"SUMMARY", "DESCRIPTION", "LOCATION"} {
			if bytes.Contains(skel, []byte("\n"+secret)) {
				t.Errorf("%s: skeleton holds %s", name, secret)
			}
		}
		got, err := Merge(skel, []Layer{{Base: true, Seq: 1, Plaintext: raw}})
		if err != nil || !bytes.Equal(got, raw) {
			t.Errorf("%s: fast path changed the bytes (%v)", name, err)
		}
		empty, _ := EncodePatch(nil)
		slow, err := Merge(skel, []Layer{{Base: true, Seq: 1, Plaintext: raw}, {Seq: 2, Plaintext: empty}})
		if err != nil {
			t.Fatalf("%s: slow path: %v", name, err)
		}
		if a, b := props(mustDecode(t, raw).Component), props(mustDecode(t, slow).Component); !slices.Equal(a, b) {
			t.Errorf("%s: slow path lost or changed properties:\n%s\nwant\n%s", name, strings.Join(b, "\n"), strings.Join(a, "\n"))
		}
	}
}

const meeting = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nX-WR-CALNAME:Private name\r\n" +
	"BEGIN:VEVENT\r\nUID:m1\r\nDTSTAMP:20261101T000000Z\r\nDTSTART:20261110T090000Z\r\nDTEND:20261110T100000Z\r\n" +
	"RRULE:FREQ=WEEKLY;COUNT=4\r\nSUMMARY:Board review\r\nDESCRIPTION:Numbers\r\nLOCATION:Room 1\r\nX-APPLE-SECRET:x\r\n" +
	"ORGANIZER:mailto:ann@example.com\r\nATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:bob@example.com\r\n" +
	"BEGIN:VALARM\r\nACTION:DISPLAY\r\nTRIGGER:-PT15M\r\nDESCRIPTION:Mine\r\nEND:VALARM\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

func text(t *testing.T, cal *ical.Calendar, rid, name string) string {
	t.Helper()
	for _, c := range cal.Children {
		if c.Name == ical.CompEvent && recurrenceID(c) == rid {
			if p := c.Props.Get(name); p != nil {
				return p.Value
			}
			return ""
		}
	}
	t.Fatalf("no component with RECURRENCE-ID %q", rid)
	return ""
}

// E2: readable properties come from the skeleton, so a worker's change to times or PARTSTAT,
// made in the skeleton alone, reaches the reader with the content intact. Calendar-level and
// unknown X- content survives.
func TestSkeletonWinsForReadableProperties(t *testing.T) {
	skel, _ := Split([]byte(meeting))
	moved := strings.Replace(strings.Replace(string(skel), "DTSTART:20261110T090000Z", "DTSTART:20261110T110000Z", 1),
		"PARTSTAT=NEEDS-ACTION", "PARTSTAT=ACCEPTED", 1)
	got, err := Merge([]byte(moved), []Layer{{Base: true, Seq: 1, Plaintext: []byte(meeting)}})
	if err != nil {
		t.Fatal(err)
	}
	cal := mustDecode(t, got)
	if v := text(t, cal, "", "DTSTART"); v != "20261110T110000Z" {
		t.Errorf("DTSTART %q", v)
	}
	if !strings.Contains(string(got), "PARTSTAT=ACCEPTED") || strings.Contains(string(got), "NEEDS-ACTION") {
		t.Errorf("PARTSTAT not from the skeleton:\n%s", got)
	}
	for _, want := range []string{"SUMMARY:Board review", "X-APPLE-SECRET:x", "X-WR-CALNAME:Private name", "DESCRIPTION:Mine"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("lost %s:\n%s", want, got)
		}
	}
}

// E2: an override a worker made (in the skeleton only) shows its master's content, then a patch
// for that occurrence alone; the master keeps its own.
func TestOverridesInheritTheMasterThenTheirPatches(t *testing.T) {
	skel, _ := Split([]byte(meeting))
	override := "BEGIN:VEVENT\r\nUID:m1\r\nDTSTAMP:20261101T000000Z\r\nRECURRENCE-ID:20261117T090000Z\r\n" +
		"DTSTART:20261117T130000Z\r\nDTEND:20261117T140000Z\r\nORGANIZER:mailto:ann@example.com\r\n" +
		"ATTENDEE;PARTSTAT=DECLINED:mailto:bob@example.com\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	withOverride := strings.Replace(string(skel), "END:VCALENDAR\r\n", override, 1)
	patch, err := EncodePatch([]Op{{Op: "set", Component: "VEVENT", RecurrenceID: "20261117T090000Z", Property: "LOCATION", Values: []PropValue{{Value: "Room 9"}}}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Merge([]byte(withOverride), []Layer{{Base: true, Seq: 1, Plaintext: []byte(meeting)}, {Seq: 2, Plaintext: patch}})
	if err != nil {
		t.Fatal(err)
	}
	cal := mustDecode(t, got)
	for rid, want := range map[string][2]string{"": {"Board review", "Room 1"}, "20261117T090000Z": {"Board review", "Room 9"}} {
		if s, l := text(t, cal, rid, "SUMMARY"), text(t, cal, rid, "LOCATION"); s != want[0] || l != want[1] {
			t.Errorf("RECURRENCE-ID %q: SUMMARY %q LOCATION %q, want %v", rid, s, l, want)
		}
	}
}

// E4: an organizer's update (a keep-alarms base) replaces every content property, so a cleared
// LOCATION is gone, and the attendee's own alarm text comes from the base before it. A patch
// older than the newest base no longer applies.
func TestKeepAlarmsBase(t *testing.T) {
	update := strings.NewReplacer("SUMMARY:Board review", "SUMMARY:Board review v2", "LOCATION:Room 1\r\n", "",
		"DESCRIPTION:Mine", "DESCRIPTION:Organizer alarm").Replace(meeting)
	skel, _ := Split([]byte(update))
	stale, _ := EncodePatch([]Op{{Op: "set", Component: "VEVENT", Property: "LOCATION", Values: []PropValue{{Value: "Room 5"}}}})
	got, err := Merge(skel, []Layer{
		{Base: true, Seq: 1, Plaintext: []byte(meeting)},
		{Seq: 2, Plaintext: stale},
		{Base: true, KeepAlarms: true, Seq: 3, Plaintext: []byte(update)},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, "SUMMARY:Board review v2") || strings.Contains(s, "LOCATION") {
		t.Errorf("content not from the newest base:\n%s", s)
	}
	if !strings.Contains(s, "DESCRIPTION:Mine") || strings.Contains(s, "Organizer alarm") {
		t.Errorf("alarm text not kept from the earlier base:\n%s", s)
	}
}

// E5: set, remove and conference change only what they name, in sequence order, and a patch
// cannot touch a readable property.
func TestPatchOperations(t *testing.T) {
	skel, _ := Split([]byte(meeting))
	first, _ := EncodePatch([]Op{{Op: "remove", Component: "VEVENT", Property: "DESCRIPTION"}})
	second, _ := EncodePatch([]Op{{Op: "conference", URL: "https://messages.example/r/1"}})
	got, err := Merge(skel, []Layer{{Seq: 9, Plaintext: second}, {Base: true, Seq: 1, Plaintext: []byte(meeting)}, {Seq: 5, Plaintext: first}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	for _, want := range []string{"SUMMARY:Board review", "LOCATION:Room 1", "LABEL=KyMessages", "Join: https://messages.example/r/1"} {
		if !strings.Contains(s, want) {
			t.Errorf("lost %s:\n%s", want, s)
		}
	}
	if strings.Contains(s, "Numbers") {
		t.Errorf("removed DESCRIPTION came back:\n%s", s)
	}
	for _, bad := range []Op{
		{Op: "set", Component: "VEVENT", Property: "DTSTART"},
		{Op: "set", Component: "VEVENT", Property: "ATTENDEE"},
		{Op: "set", Property: "LOCATION"},
		{Op: "rename", Component: "VEVENT", Property: "LOCATION"},
	} {
		if _, err := EncodePatch([]Op{bad}); err == nil {
			t.Errorf("%+v encoded", bad)
		}
	}
	if Label([]Op{{Op: "set", Component: "VEVENT", Property: "LOCATION", Values: []PropValue{{Value: "secret"}}}}) != "set LOCATION VEVENT " ||
		strings.Contains(Label([]Op{{Op: "conference", URL: "https://secret"}}), "secret") {
		t.Error("a label carries a value")
	}
	if _, err := Merge(skel, []Layer{{Seq: 1, Plaintext: first}}); err == nil {
		t.Error("merged with no base")
	}
}
